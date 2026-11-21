/// <reference types="@jlceda/pro-api-types" />
import { armDeadline } from './deadlines';
import { ActionError, ErrorCodes } from './protocol';

type Observation = { status: 'matched' | 'different' | 'missing' | 'ambiguous' | 'unavailable'; name?: string; error?: string };
type PageInventory = Awaited<ReturnType<typeof eda.dmt_Schematic.getAllSchematicPagesInfo>>;
type Recovery = { reason: 'host-refused-before-second-page-loaded'; opened: string[]; restored: string | null; error?: string };

const READ_TIMEOUT_MS = 2000;

// A pending read must not hide an already known host refusal. This timeout
// cancels only our wait, never claims to cancel an EDA operation.
function readPageInventory(): Promise<Awaited<ReturnType<typeof eda.dmt_Schematic.getAllSchematicPagesInfo>>> {
	return new Promise((resolve, reject) => {
		let settled = false;
		const expiresAt = Date.now() + READ_TIMEOUT_MS;
		const deadline = armDeadline(READ_TIMEOUT_MS, () => finish(new Error('Page inventory read timed out.')));
		const finish = (error?: unknown, pages?: Awaited<ReturnType<typeof eda.dmt_Schematic.getAllSchematicPagesInfo>>) => {
			if (settled) return;
			settled = true;
			deadline.cancel();
			if (error !== undefined) reject(error);
			else resolve(pages!);
		};
		Promise.resolve().then(() => eda.dmt_Schematic.getAllSchematicPagesInfo()).then(pages => {
			if (Date.now() >= expiresAt) finish(new Error('Page inventory read timed out.'));
			else finish(undefined, pages);
		}, error => finish(error));
	});
}

// The inventory of the last successful read: the recovery picks a sibling page
// from it instead of spending another host read.
let lastInventory: PageInventory | undefined;

async function observePage(pageUuid: string, name: string): Promise<Observation> {
	try {
		const pages = await readPageInventory();
		lastInventory = Array.isArray(pages) ? pages : undefined;
		if (!Array.isArray(pages)) return { status: 'unavailable', error: 'Page inventory is not an array.' };
		const matches = pages.filter(page => page.uuid === pageUuid);
		if (matches.length === 0) return { status: 'missing' };
		if (matches.length !== 1) return { status: 'ambiguous' };
		const observed = matches[0].name;
		if (typeof observed !== 'string') return { status: 'unavailable', error: 'Page name is unavailable.' };
		return { status: observed === name ? 'matched' : 'different', name: observed };
	}
	catch (error) { return { status: 'unavailable', error: String(error) }; }
}

const SETTLE_MS = 250;
const pause = (ms: number) => new Promise<void>(resolve => { armDeadline(ms, resolve); });

/**
 * The host answers false at once while its window has loaded fewer than two
 * pages of the schematic (a freshly opened project, or only one page visited);
 * after a second page was opened it renames any page (issue #55, Web 4.1.105).
 * Navigation only: open the target and one sibling page, then put the original
 * foreground document back. Never writes.
 */
async function loadPagesForRename(pageUuid: string, pages: PageInventory): Promise<Recovery | undefined> {
	const target = pages.find(page => page.uuid === pageUuid);
	const sibling = target && pages.find(page => page.uuid !== pageUuid && page.parentSchematicUuid === target.parentSchematicUuid);
	if (!sibling) return undefined; // A single-page schematic has nothing else to load.
	const recovery: Recovery = { reason: 'host-refused-before-second-page-loaded', opened: [], restored: null };
	let original: string | undefined;
	try {
		try { original = (await eda.dmt_SelectControl.getCurrentDocumentInfo())?.uuid; }
		catch { original = undefined; } // "No active document" is the usual fresh-window state.
		for (const uuid of [sibling.uuid, pageUuid]) {
			await eda.dmt_EditorControl.openDocument(uuid);
			if ((await eda.dmt_SelectControl.getCurrentDocumentInfo())?.uuid !== uuid) throw new Error(`page ${uuid} did not become active`);
			recovery.opened.push(uuid);
			await pause(SETTLE_MS);
		}
	}
	catch (error) { recovery.error = String(error); }
	finally {
		// Put the caller's foreground document back; without one the target page stays open.
		try {
			if (original && original !== pageUuid) {
				await eda.dmt_EditorControl.openDocument(original);
				if ((await eda.dmt_SelectControl.getCurrentDocumentInfo())?.uuid === original) recovery.restored = original;
				else recovery.error = [recovery.error, 'original document did not become active again'].filter(Boolean).join('; ');
			}
			else if (original) recovery.restored = original;
		}
		catch (error) { recovery.error = [recovery.error, `restore original document: ${String(error)}`].filter(Boolean).join('; '); }
	}
	return recovery;
}

/**
 * No retry of an unknown outcome and no unrelated write to refresh host
 * metadata. The one second call happens only after a known refusal (host said
 * false and the fresh name is unchanged) and after loading the pages the host
 * needs; a second refusal is final.
 */
export async function renameSchematicPage(pageUuid: string, name: string) {
	let hostAccepted: unknown;
	let callError: string | undefined;
	let recovery: Recovery | undefined;
	// Leave a pending mutation attached to the transport queue guard: ending our
	// own wait early would incorrectly allow another write while it still runs.
	try { hostAccepted = await eda.dmt_Schematic.modifySchematicPageName(pageUuid, name); }
	catch (error) { callError = String(error); }
	let observed = await observePage(pageUuid, name);
	if (callError === undefined && hostAccepted === false && observed.status === 'different' && lastInventory) {
		recovery = await loadPagesForRename(pageUuid, lastInventory);
		if (recovery && !recovery.error) {
			try { hostAccepted = await eda.dmt_Schematic.modifySchematicPageName(pageUuid, name); }
			catch (error) { callError = String(error); }
			observed = await observePage(pageUuid, name);
		}
	}
	const fail = (message: string): never => {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, message, JSON.stringify({
			pageUuid, requestedName: name, hostAccepted: hostAccepted ?? null, callError,
			verified: false, observation: observed, ...(recovery ? { recovery } : {}),
		}));
	};
	if (callError !== undefined) fail('Schematic page rename call failed; write outcome is unknown. Inspect fresh state before any retry.');
	if (hostAccepted !== true) fail('Host did not confirm schematic page rename. No mutation was retried; inspect the returned readback.');
	for (const delay of [0, 120, 250, 500]) {
		if (delay > 0) {
			await new Promise<void>(resolve => { armDeadline(delay, resolve); });
			observed = await observePage(pageUuid, name);
		}
		if (observed.status === 'matched') return { result: { ok: true, verified: true, pageUuid, name, ...(recovery ? { recovery } : {}) } };
		// Missing/ambiguous identity and unavailable reads are unknown, not a cache delay.
		if (observed.status !== 'different') break;
	}
	return fail('Host accepted schematic page rename, but fresh page name was not verified. Stop dependent writes and inspect the returned readback.');
}
