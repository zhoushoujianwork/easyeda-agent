/// <reference types="@jlceda/pro-api-types" />
import { armDeadline } from './deadlines';
import { ActionError, ErrorCodes } from './protocol';

type Observation = { status: 'matched' | 'different' | 'missing' | 'ambiguous' | 'unavailable'; name?: string; error?: string };

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

async function observePage(pageUuid: string, name: string): Promise<Observation> {
	try {
		const pages = await readPageInventory();
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

/** No mutation retry and no unrelated write to refresh host metadata. */
export async function renameSchematicPage(pageUuid: string, name: string) {
	let hostAccepted: unknown;
	let callError: string | undefined;
	// Leave a pending mutation attached to the transport queue guard: ending our
	// own wait early would incorrectly allow another write while it still runs.
	try { hostAccepted = await eda.dmt_Schematic.modifySchematicPageName(pageUuid, name); }
	catch (error) { callError = String(error); }
	let observed = await observePage(pageUuid, name);
	const fail = (message: string): never => {
		throw new ActionError(ErrorCodes.EDA_CALL_FAILED, message, JSON.stringify({
			pageUuid, requestedName: name, hostAccepted: hostAccepted ?? null, callError,
			verified: false, observation: observed,
		}));
	};
	if (callError !== undefined) fail('Schematic page rename call failed; write outcome is unknown. Inspect fresh state before any retry.');
	if (hostAccepted !== true) fail('Host did not confirm schematic page rename. No mutation was retried; inspect the returned readback.');
	for (const delay of [0, 120, 250, 500]) {
		if (delay > 0) {
			await new Promise<void>(resolve => { armDeadline(delay, resolve); });
			observed = await observePage(pageUuid, name);
		}
		if (observed.status === 'matched') return { result: { ok: true, verified: true, pageUuid, name } };
		// Missing/ambiguous identity and unavailable reads are unknown, not a cache delay.
		if (observed.status !== 'different') break;
	}
	return fail('Host accepted schematic page rename, but fresh page name was not verified. Stop dependent writes and inspect the returned readback.');
}
