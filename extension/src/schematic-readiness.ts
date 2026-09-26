/// <reference types="@jlceda/pro-api-types" />
import { armDeadline } from './deadlines';
import { ActionError, ErrorCodes } from './protocol';

export class SchematicReadinessError extends ActionError {}

export interface SchematicContext { uuid: string; tabId: string }

type HostDocument = { uuid: string; tabId: string; profileSetting?: { readonlyMode?: boolean } };
export interface SchematicReadinessToken { runner: Runner; document: HostDocument }

type Runner = { running: boolean; currentAction: null | { name?: string; status: string } };

// Read-only compatibility adapter, observed in Web pro-sch 4.1.54.bfdf9a4d.
// Official delete calls actionRunner.run(), which refuses an existing transaction.
// No public eda readiness API exists. Never call private start/end/cancel/waitEnd,
// change history, suspend sync, or use this adapter to perform a design mutation.
function hostState(): SchematicReadinessToken {
	try {
		const host = globalThis as unknown as { window?: { SCH?: {
			app?: { actionRunner?: Runner };
			docMemoryManager?: { getActiveDoc?: () => HostDocument | undefined };
		} } };
		const sch = host.window?.SCH;
		const value = sch?.app?.actionRunner;
		if (!value || typeof value.running !== 'boolean' || !('currentAction' in value)
			|| typeof sch?.docMemoryManager?.getActiveDoc !== 'function') {
			throw new SchematicReadinessError(ErrorCodes.EDA_API_UNAVAILABLE, 'Schematic transaction readiness is unsupported by this host.');
		}
		const document = sch.docMemoryManager.getActiveDoc();
		if (!document || typeof document !== 'object') throw new SchematicReadinessError(ErrorCodes.INVALID_STATE, 'Active schematic document instance is unavailable.');
		return { runner: value, document };
	}
	catch (err) {
		throw err instanceof SchematicReadinessError ? err : new SchematicReadinessError(ErrorCodes.INVALID_STATE, `Cannot read schematic readiness: ${String(err)}`);
	}
}

function isIdle(value: Runner): boolean {
	const action = value.currentAction;
	if (!value.running && action === null) return true;
	if (value.running && action && action.name === 'RealTimeSync'
		&& ['willRun', 'running', 'willDidRun'].includes(action.status)) return false;
	throw new SchematicReadinessError(ErrorCodes.INVALID_STATE, 'Schematic has an unknown or non-sync transaction; refusing to write.');
}

export function captureSchematicReadiness(context: SchematicContext): SchematicReadinessToken {
	try {
		const state = hostState();
		assertHostDocument(context, state.document);
		return state;
	}
	catch (err) {
		throw err instanceof SchematicReadinessError ? err : new SchematicReadinessError(ErrorCodes.INVALID_STATE, `Cannot read schematic readiness: ${String(err)}`);
	}
}

function assertHostDocument(context: SchematicContext, doc: HostDocument): void {
	if (doc.uuid !== context.uuid || doc.tabId !== context.tabId || doc.profileSetting?.readonlyMode !== false) {
		throw new SchematicReadinessError(ErrorCodes.INVALID_STATE, 'Schematic host identity or writable state is unavailable or changed.');
	}
}

export function assertSchematicReadinessContext(context: SchematicContext, token: SchematicReadinessToken): void {
	const current = captureSchematicReadiness(context);
	if (current.runner !== token.runner || current.document !== token.document) {
		throw new SchematicReadinessError(ErrorCodes.INVALID_STATE, 'Schematic runner or document instance changed during this operation.');
	}
}

/** Wait on observed RealTimeSync state, never on an assumed fixed settling delay. */
export async function waitForSchematicReady(context: SchematicContext, timeoutMs = 5000, captured = captureSchematicReadiness(context)): Promise<void> {
	const expiresAt = Date.now() + timeoutMs;
	await new Promise<void>((resolve, reject) => {
		let settled = false;
		let poll: ReturnType<typeof armDeadline> | undefined;
		const finish = (error?: unknown): void => {
			if (settled) return;
			settled = true;
			deadline.cancel();
			poll?.cancel();
			if (error) reject(error); else resolve();
		};
		const deadline = armDeadline(timeoutMs, () => finish(new SchematicReadinessError(
			ErrorCodes.INVALID_STATE, 'Timed out waiting for schematic RealTimeSync; no further writes are allowed.',
		)));
		const check = async (): Promise<void> => {
			try {
				const current = await eda.dmt_SelectControl.getCurrentDocumentInfo();
				if (settled) return; // A late identity response must never resume a timed-out wait.
				if (Date.now() >= expiresAt) throw new SchematicReadinessError(ErrorCodes.INVALID_STATE, 'Timed out waiting for schematic RealTimeSync; no further writes are allowed.');
				if (current?.uuid !== context.uuid || current.tabId !== context.tabId) {
					throw new SchematicReadinessError(ErrorCodes.INVALID_STATE, 'Schematic document identity changed while waiting for readiness.');
				}
				assertSchematicReadinessContext(context, captured);
				if (isIdle(captured.runner)) finish();
				else poll = armDeadline(50, () => { void check(); });
			}
			catch (err) { finish(err instanceof SchematicReadinessError ? err : new SchematicReadinessError(ErrorCodes.INVALID_STATE, `Cannot read schematic readiness: ${String(err)}`)); }
		};
		void check();
	});
}
