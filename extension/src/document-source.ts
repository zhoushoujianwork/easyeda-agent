/// <reference types="@jlceda/pro-api-types" />
import { readResponseContext } from './eda-context';
import { armDeadline, type DeadlineHandle } from './deadlines';
import { ActionError, type ActionResult, ErrorCodes } from './protocol';
import { describeThrown, requireString } from './util';

type Payload = Record<string, unknown>;
type DocumentType = 'schematic' | 'pcb';
interface Target { projectUuid: string; documentUuid: string; documentType: DocumentType }
interface SourceManager {
	getDocumentSource?: () => Promise<unknown>;
	setDocumentSource?: (source: string) => Promise<unknown>;
}
const SOURCE_LIMIT = 2 * 1024 * 1024;
const READ_TIMEOUT_MS = 10000;
const SCHEMA_VERSION = 1;

function refuse(message: string): never {
	throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, message);
}
function fields(value: Payload, allowed: string[], name: string): void {
	for (const key of Object.keys(value)) if (!allowed.includes(key)) refuse(`Unknown ${name} field: ${key}.`);
}
function targetRequest(payload: Payload): Pick<Target, 'projectUuid' | 'documentUuid'> {
	const projectUuid = requireString(payload, 'projectUuid');
	const documentUuid = requireString(payload, 'documentUuid');
	if (projectUuid !== projectUuid.trim() || documentUuid !== documentUuid.trim()) refuse('Target UUIDs must not contain surrounding whitespace.');
	return { projectUuid, documentUuid };
}
function checkedSource(value: unknown): string {
	if (typeof value !== 'string' || !value.trim()) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'Official document source is empty or does not use the supported string schema.');
	}
	if (value.length > SOURCE_LIMIT || new TextEncoder().encode(value).length > SOURCE_LIMIT) {
		throw new ActionError(ErrorCodes.INVALID_STATE, 'Document source exceeds the 2 MiB UTF-8 limit.');
	}
	return value;
}
/** Bound reads only. A timed-out setter could still write later, so it must
 * remain pending, retaining the transport FIFO after an abandoned response. */
async function boundedRead<T>(label: string, read: () => Promise<T>): Promise<T> {
	let deadline: DeadlineHandle | undefined;
	try {
		return await Promise.race([
			Promise.resolve().then(read),
			new Promise<never>((_, reject) => {
				deadline = armDeadline(READ_TIMEOUT_MS, () => reject(new ActionError(ErrorCodes.EDA_CALL_FAILED,
					`${label} did not settle within ${READ_TIMEOUT_MS} ms; inspect state before retrying.`)));
			}),
		]);
	} finally { deadline?.cancel(); }
}
async function contextFor(requested: Pick<Target, 'projectUuid' | 'documentUuid'>, expectedType?: DocumentType): Promise<Target> {
	const context = await boundedRead('Document identity read', readResponseContext);
	if (context.projectUuid !== requested.projectUuid || context.documentUuid !== requested.documentUuid) {
		refuse('Active project/document identity does not match the explicit target, or changed during the probe.');
	}
	if (context.documentType !== 'schematic' && context.documentType !== 'pcb') {
		refuse('Document source probes support only schematic or PCB documents with readable identity.');
	}
	if (expectedType !== undefined && context.documentType !== expectedType) refuse('Active document type changed during the source probe.');
	return { ...requested, documentType: context.documentType };
}
function sourceManager() {
	const manager = (typeof eda === 'undefined' ? undefined : eda.sys_FileManager) as SourceManager | undefined;
	const availability = {
		getDocumentSource: typeof manager?.getDocumentSource === 'function',
		setDocumentSource: typeof manager?.setDocumentSource === 'function',
	};
	return { manager, availability };
}
function requireGetter(manager: SourceManager | undefined): SourceManager & Required<Pick<SourceManager, 'getDocumentSource'>> {
	if (typeof manager?.getDocumentSource !== 'function') {
		throw new ActionError(ErrorCodes.EDA_API_UNAVAILABLE, 'Official sys_FileManager.getDocumentSource is unavailable.');
	}
	return manager as SourceManager & Required<Pick<SourceManager, 'getDocumentSource'>>;
}
async function freshSource(manager: SourceManager): Promise<string> {
	const readable = requireGetter(manager);
	return checkedSource(await boundedRead('Document source read', () => readable.getDocumentSource()));
}
function snapshotOf(payload: Payload, requested: Pick<Target, 'projectUuid' | 'documentUuid'>): Target & { source: string } {
	const value = payload.snapshot;
	if (!value || typeof value !== 'object' || Array.isArray(value)) refuse('A document source snapshot is required for exact fresh-source comparison.');
	const snapshot = value as Payload;
	fields(snapshot, ['schemaVersion', 'projectUuid', 'documentUuid', 'documentType', 'source'], 'snapshot');
	if (snapshot.schemaVersion !== SCHEMA_VERSION) refuse('Unsupported document source snapshot schemaVersion; expected 1.');
	if (snapshot.projectUuid !== requested.projectUuid || snapshot.documentUuid !== requested.documentUuid) refuse('Snapshot project/document UUIDs do not match the explicit target.');
	if (snapshot.documentType !== 'schematic' && snapshot.documentType !== 'pcb') refuse('Snapshot documentType must be schematic or pcb.');
	let source: string;
	try { source = checkedSource(snapshot.source); }
	catch (err) { refuse(`Invalid snapshot source: ${describeThrown(err)}`); }
	return { ...requested, documentType: snapshot.documentType, source };
}

/** Read the exact active document source. No import, save, or view change. */
export async function documentSourceGet(payload: Payload): Promise<ActionResult> {
	fields(payload, ['projectUuid', 'documentUuid'], 'document source get');
	const requested = targetRequest(payload);
	const target = await contextFor(requested);
	const { manager, availability } = sourceManager();
	const source = await freshSource(requireGetter(manager));
	await contextFor(requested, target.documentType);
	return { result: { schemaVersion: SCHEMA_VERSION, target, availability, source,
		bytes: new TextEncoder().encode(source).length, verified: true, partial: false, incomplete: false }, context: target };
}

/** Only re-submit the freshly read host string, once. The caller's snapshot is
 * a compare-and-swap guard, never a candidate source to write. */
export async function documentSourceRoundtrip(payload: Payload): Promise<ActionResult> {
	fields(payload, ['projectUuid', 'documentUuid', 'snapshot', 'dryRun'], 'document source roundtrip');
	if (payload.dryRun !== undefined && typeof payload.dryRun !== 'boolean') refuse('dryRun must be a boolean.');
	const requested = targetRequest(payload);
	const snapshot = snapshotOf(payload, requested);
	const target = await contextFor(requested, snapshot.documentType);
	const { manager, availability } = sourceManager();
	const readable = requireGetter(manager);
	if (typeof readable.setDocumentSource !== 'function') {
		throw new ActionError(ErrorCodes.EDA_API_UNAVAILABLE, 'Official sys_FileManager.setDocumentSource is unavailable.');
	}
	const beforeSource = await freshSource(readable);
	await contextFor(requested, target.documentType);
	if (beforeSource !== snapshot.source) refuse('Fresh document source differs from the snapshot; no source was written.');
	const base = { schemaVersion: SCHEMA_VERSION, target, availability, beforeSource };
	if (payload.dryRun === true) return { result: { ...base, dryRun: true, afterSource: beforeSource,
		writeAttempted: false, written: false, verified: true, partial: false, incomplete: false }, context: target };
	// Recheck the target immediately before the one setter call. Do not add an
	// automatic retry, rollback, save, or timeout race around this write.
	await contextFor(requested, target.documentType);
	let written: boolean | null = null;
	let writeError: string | undefined;
	try {
		const result = await readable.setDocumentSource(beforeSource);
		if (typeof result === 'boolean') written = result;
		else writeError = `Official setter returned unsupported ${typeof result} result.`;
	} catch (err) { writeError = describeThrown(err); }
	let afterSource: string | null = null;
	let readbackError: string | undefined;
	let identityAfter: Target | undefined;
	try {
		// Never read the source of a different document after a host transition.
		await contextFor(requested, target.documentType);
		afterSource = await freshSource(readable);
		identityAfter = await contextFor(requested, target.documentType);
	} catch (err) { readbackError = describeThrown(err); }
	const verified = written === true && !readbackError && afterSource === beforeSource;
	return { result: { ...base, dryRun: false, afterSource, identityAfter, writeAttempted: true, written,
		writeError, readbackError, verified, partial: !verified, incomplete: !verified },
		// An explicit empty context avoids the dispatcher's unbounded fallback
		// when the post-write identity read itself failed. Never label the old
		// target as the current context after an unconfirmed host transition.
		context: identityAfter ?? {},
		...(!verified ? { warnings: ['Same-source roundtrip was not confirmed. Preserve before/after evidence; do not automatically retry or roll back.'] } : {}) };
}
