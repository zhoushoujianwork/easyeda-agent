import assert from 'node:assert/strict';
import { test } from 'node:test';
import { documentSourceGet, documentSourceRoundtrip } from './document-source';
import { runAction } from './actions';
import { ActionQueue, isBypassAction, mustHoldAfterAbandon } from './action-queue';
import { pendingDeadlines, sweepDeadlines } from './deadlines';
import { ActionError, ErrorCodes } from './protocol';

const source = '{"type":"DOCHEAD"}||{"docType":"SCH_PAGE","uuid":"document-1"}|\r\n{"type":"TEXT"}||{"text":"原样保留"}|\r\n';
const target = { projectUuid: 'project-1', documentUuid: 'document-1' };
function snapshot(documentType: 'schematic' | 'pcb' = 'schematic', text = source) {
	return { schemaVersion: 1, ...target, documentType, source: text };
}
interface HostOptions {
	type?: number;
	get?: (() => Promise<unknown>) | null;
	set?: ((value: string) => Promise<unknown>) | null;
	context?: (read: number) => { projectUuid?: string; documentUuid?: string; type?: number };
}
function installHost(options: HostOptions = {}) {
	const globals = globalThis as any;
	const previousEda = globals.eda, previousTypes = globals.EDMT_EditorDocumentType;
	let contextReads = 0, sourceReads = 0;
	const writes: string[] = [];
	globals.EDMT_EditorDocumentType = { HOME: -1, BLANK: 0, SCHEMATIC_PAGE: 1, PCB: 3 };
	const context = () => ({ ...target, type: options.type ?? 1, ...options.context?.(contextReads) });
	globals.eda = {
		dmt_Project: { getCurrentProjectInfo: async () => { contextReads++; return { uuid: context().projectUuid }; } },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: context().documentUuid, documentType: context().type, tabId: 'tab-1' }) },
		sys_FileManager: {
			...(options.get === null ? {} : { getDocumentSource: async () => { sourceReads++; return options.get ? options.get() : source; } }),
			...(options.set === null ? {} : { setDocumentSource: async (value: string) => { writes.push(value); return options.set ? options.set(value) : true; } }),
		},
	};
	return {
		writes, sourceReads: () => sourceReads,
		restore: () => {
			if (previousEda === undefined) delete globals.eda; else globals.eda = previousEda;
			if (previousTypes === undefined) delete globals.EDMT_EditorDocumentType; else globals.EDMT_EditorDocumentType = previousTypes;
		},
	};
}
function hasCode(code: string) { return (error: unknown) => error instanceof ActionError && error.code === code; }

test('source get reads exact UTF-8 source and target without calling any setter', async () => {
	const host = installHost({ set: null });
	try {
		const result = (await documentSourceGet(target)).result!;
		assert.equal(result.schemaVersion, 1);
		assert.equal(result.source, source);
		assert.deepEqual(result.target, { ...target, documentType: 'schematic' });
		assert.deepEqual(result.availability, { getDocumentSource: true, setDocumentSource: false });
		assert.equal(result.bytes, new TextEncoder().encode(source).length);
		assert.equal(result.verified, true);
		assert.equal(host.sourceReads(), 1);
		assert.deepEqual(host.writes, []);
	} finally { host.restore(); }
});

test('same-source roundtrip writes the fresh host string once and verifies both document kinds', async () => {
	for (const [type, documentType] of [[1, 'schematic'], [3, 'pcb']] as const) {
		const host = installHost({ type });
		try {
			const result = (await documentSourceRoundtrip({ ...target, snapshot: snapshot(documentType) })).result!;
			assert.deepEqual(host.writes, [source]);
			assert.equal(host.sourceReads(), 2);
			assert.equal(result.beforeSource, source);
			assert.equal(result.afterSource, source);
			assert.equal(result.writeAttempted, true);
			assert.equal(result.written, true);
			assert.equal(result.dryRun, false);
			assert.equal(result.verified, true);
			assert.equal(result.partial, false);
			assert.equal(result.incomplete, false);
		} finally { host.restore(); }
	}
});

test('dry run performs the same target/source guards and never writes', async () => {
	const host = installHost();
	try {
		const result = (await documentSourceRoundtrip({ ...target, snapshot: snapshot(), dryRun: true })).result!;
		assert.equal(result.dryRun, true);
		assert.equal(result.writeAttempted, false);
		assert.equal(result.written, false);
		assert.equal(result.beforeSource, source);
		assert.equal(result.afterSource, source);
		assert.equal(host.sourceReads(), 1);
		assert.deepEqual(host.writes, []);
		await assert.rejects(documentSourceRoundtrip({ ...target, snapshot: snapshot('schematic', source + '\n'), dryRun: true }), /differs from the snapshot/);
		assert.deepEqual(host.writes, []);
	} finally { host.restore(); }
});

test('snapshot schema, target, document kind, and arbitrary candidate fields are refused before writing', async () => {
	const host = installHost();
	try {
		for (const bad of [
			{ ...target },
			{ ...target, snapshot: { ...snapshot(), schemaVersion: 2 } },
			{ ...target, snapshot: { ...snapshot(), projectUuid: 'different-project' } },
			{ ...target, snapshot: { ...snapshot(), documentUuid: 'different-document' } },
			{ ...target, snapshot: { ...snapshot(), documentType: 'symbol' } },
			{ ...target, snapshot: { ...snapshot(), source: '' } },
			{ ...target, snapshot: { ...snapshot(), candidate: source } },
			{ ...target, snapshot: snapshot(), source: 'arbitrary candidate' },
			{ ...target, snapshot: snapshot(), dryRun: 'true' },
			{ ...target, snapshot: snapshot(), timeoutMs: 1 },
		]) await assert.rejects(documentSourceRoundtrip(bad));
		assert.equal(host.sourceReads(), 0);
		assert.deepEqual(host.writes, []);
	} finally { host.restore(); }
});

test('get refuses blank/implicit targets and unrelated payloads', async () => {
	const host = installHost();
	try {
		for (const bad of [{}, { ...target, projectUuid: ' ' }, { ...target, documentUuid: '' }, { ...target, typo: true }]) {
			await assert.rejects(documentSourceGet(bad));
		}
		assert.equal(host.sourceReads(), 0);
		assert.deepEqual(host.writes, []);
	} finally { host.restore(); }
});

test('missing official methods and undefined/malformed source never cause a write', async () => {
	for (const options of [{ get: null }, { set: null }, { get: async () => undefined }, { get: async () => ({ source }) }, { get: async () => '  ' }]) {
		const host = installHost(options);
		try {
			await assert.rejects(documentSourceRoundtrip({ ...target, snapshot: snapshot() }));
			assert.deepEqual(host.writes, []);
		} finally { host.restore(); }
	}
	const host = installHost({ get: null });
	try { await assert.rejects(documentSourceGet(target), hasCode(ErrorCodes.EDA_API_UNAVAILABLE)); }
	finally { host.restore(); }
});

test('unsupported active document type, missing identity, and snapshot kind mismatch never write', async () => {
	for (const options of [{ type: 8 }, { context: () => ({ documentUuid: undefined }) }, { context: () => ({ projectUuid: 'another' }) }, { type: 3 }]) {
		const host = installHost(options);
		try {
			await assert.rejects(documentSourceRoundtrip({ ...target, snapshot: snapshot() }), hasCode(ErrorCodes.PRECONDITION_REFUSED));
			assert.deepEqual(host.writes, []);
		} finally { host.restore(); }
	}
});

test('fresh source mismatch is refused and caller text is never sent to the setter', async () => {
	const host = installHost({ get: async () => source + 'host changed' });
	try {
		await assert.rejects(documentSourceRoundtrip({ ...target, snapshot: snapshot() }), /differs from the snapshot/);
		assert.deepEqual(host.writes, []);
	} finally { host.restore(); }
});

test('identity drift during get or immediately before the setter refuses with zero writes', async () => {
	for (const changeAt of [2, 3]) {
		const host = installHost({ context: read => read >= changeAt ? { documentUuid: 'document-2' } : {} });
		try {
			await assert.rejects(documentSourceRoundtrip({ ...target, snapshot: snapshot() }), /identity.*changed/);
			assert.deepEqual(host.writes, []);
		} finally { host.restore(); }
	}
	const host = installHost({ context: read => read >= 2 ? { projectUuid: 'project-2' } : {} });
	try { await assert.rejects(documentSourceGet(target), /identity.*changed/); }
	finally { host.restore(); }
});

test('setter false, undefined, or throw remains incomplete with exact before/after and no retry', async () => {
	for (const set of [async () => false, async () => undefined, async () => { throw Error('host rejected source'); }]) {
		const host = installHost({ set });
		try {
			const result = (await documentSourceRoundtrip({ ...target, snapshot: snapshot() })).result!;
			assert.equal(result.verified, false);
			assert.equal(result.partial, true);
			assert.equal(result.incomplete, true);
			assert.equal(result.beforeSource, source);
			assert.equal(result.afterSource, source);
			assert.deepEqual(host.writes, [source]);
		} finally { host.restore(); }
	}
});

test('host normalization is evidence of an incomplete roundtrip, never silently accepted or rolled back', async () => {
	let reads = 0;
	const normalized = source.replaceAll('\r\n', '\n');
	const host = installHost({ get: async () => ++reads === 1 ? source : normalized });
	try {
		const result = (await documentSourceRoundtrip({ ...target, snapshot: snapshot() })).result!;
		assert.equal(result.beforeSource, source);
		assert.equal(result.afterSource, normalized);
		assert.equal(result.verified, false);
		assert.equal(result.partial, true);
		assert.deepEqual(host.writes, [source]);
	} finally { host.restore(); }
});

test('post-write transition does not read source from the newly active document', async () => {
	const host = installHost({ context: read => read >= 4 ? { documentUuid: 'document-2' } : {} });
	try {
		const response = await runAction('document.source.roundtrip', { ...target, snapshot: snapshot() });
		const result = response.result!;
		assert.equal(result.afterSource, null);
		assert.equal(result.partial, true);
		assert.match(String(result.readbackError), /identity/);
		assert.equal(host.sourceReads(), 1);
		assert.deepEqual(host.writes, [source]);
		assert.deepEqual(response.context, {}, 'dispatcher must not add a fallback identity read after the transition');
	} finally { host.restore(); }
});

test('registered source actions retain FIFO across an abandoned pending setter while diagnostics remain available', async () => {
	let release!: (value: boolean) => void;
	const host = installHost({ set: () => new Promise<boolean>(resolve => { release = resolve; }) });
	const queue = new ActionQueue({ graceMs: 0, fallbackTimeoutMs: 3_600_000 });
	let laterWriteRan = false;
	try {
		assert.equal(isBypassAction('document.source.get'), false);
		assert.equal(isBypassAction('document.source.roundtrip'), false);
		assert.equal(mustHoldAfterAbandon('document.source.roundtrip'), true);
		assert.equal(mustHoldAfterAbandon('document.source.get'), false);
		const first = queue.submit({
			id: 'source-roundtrip', timeoutMs: 3_600_000,
			holdAfterAbandon: mustHoldAfterAbandon('document.source.roundtrip'),
			run: () => runAction('document.source.roundtrip', { ...target, snapshot: snapshot() }),
		});
		const laterWrite = queue.submit({ id: 'later-write', run: async () => { laterWriteRan = true; return true; } });
		const read = queue.submit({ id: 'source-get', run: () => runAction('document.source.get', target) });
		for (let i = 0; i < 100 && host.writes.length === 0; i++) await Promise.resolve();
		assert.deepEqual(host.writes, [source]);
		assert.equal(host.sourceReads(), 1);
		assert.equal(laterWriteRan, false);
		sweepDeadlines(Date.now() + 3_600_001);
		const abandoned = await first;
		assert.equal(abandoned.status, 'abandoned');
		assert.equal(abandoned.stamp.seqAbandoned, 1);
		for (let i = 0; i < 20; i++) await Promise.resolve();
		assert.equal(laterWriteRan, false, 'response abandonment must not release the pending setter FIFO');
		assert.equal(host.sourceReads(), 1, 'queued get must not run while the pending setter remains unresolved');
		const diagnostic = await queue.submit({ id: 'current', bypass: isBypassAction('document.current'), run: async () => 'alive' });
		assert.equal(diagnostic.status, 'ok');
		assert.equal(diagnostic.stamp.unordered, true);
		assert.equal(queue.pending(), 2);
		release(true);
		const next = await laterWrite;
		const readOutcome = await read;
		assert.equal(next.status, 'ok');
		assert.equal(next.stamp.seq, 1, 'late abandoned completion must not retroactively increment seq');
		assert.equal(readOutcome.status, 'ok');
		assert.equal(readOutcome.stamp.seq, 2);
		assert.equal(readOutcome.stamp.seqAbandoned, 1);
		if (readOutcome.status === 'ok') assert.equal(readOutcome.value.result?.source, source);
		assert.equal(host.sourceReads(), 3);
		assert.deepEqual(host.writes, [source]);
		assert.equal(pendingDeadlines(), 0);
	} finally { if (release) release(true); host.restore(); }
});

test('source read deadline is enforced by worker sweep when main-thread timers are frozen', async () => {
	let reads = 0;
	const host = installHost({ get: () => { reads++; return new Promise(() => {}); } });
	try {
		const pending = documentSourceGet(target);
		const rejected = assert.rejects(pending, /within 10000 ms/);
		for (let i = 0; i < 100 && reads === 0; i++) await Promise.resolve();
		assert.equal(reads, 1);
		sweepDeadlines(Date.now() + 10001);
		await rejected;
		assert.equal(pendingDeadlines(), 0);
		assert.deepEqual(host.writes, []);
	} finally { host.restore(); }
});

test('unreadable post-write source preserves the before evidence without retries', async () => {
	let reads = 0;
	const host = installHost({ get: async () => ++reads === 1 ? source : undefined });
	try {
		const result = (await documentSourceRoundtrip({ ...target, snapshot: snapshot() })).result!;
		assert.equal(result.beforeSource, source);
		assert.equal(result.afterSource, null);
		assert.equal(result.incomplete, true);
		assert.equal(host.sourceReads(), 2);
		assert.deepEqual(host.writes, [source]);
	} finally { host.restore(); }
});

test('UTF-8 byte limit rejects multibyte input before writing', async () => {
	const large = '字'.repeat(700000);
	const host = installHost({ get: async () => large });
	try {
		await assert.rejects(documentSourceGet(target), /2 MiB/);
		await assert.rejects(documentSourceRoundtrip({ ...target, snapshot: snapshot('schematic', large) }), /2 MiB/);
		assert.deepEqual(host.writes, []);
	} finally { host.restore(); }
});

test('a pending setter keeps the handler pending until it settles and is never retried', async () => {
	let release!: (value: boolean) => void;
	const host = installHost({ set: () => new Promise<boolean>(resolve => { release = resolve; }) });
	try {
		let settled = false;
		const pending = documentSourceRoundtrip({ ...target, snapshot: snapshot() }).then(value => { settled = true; return value; });
		for (let i = 0; i < 50 && host.writes.length === 0; i++) await Promise.resolve();
		assert.deepEqual(host.writes, [source]);
		assert.equal(settled, false);
		assert.equal(host.sourceReads(), 1);
		release(true);
		assert.equal((await pending).result?.verified, true);
		assert.deepEqual(host.writes, [source]);
	} finally { host.restore(); }
});

test('pre-write read timeout refuses without writing; post-write read timeout reports incomplete evidence', async (t) => {
	for (const postWrite of [false, true]) {
		let reads = 0;
		const host = installHost({ get: () => ++reads === (postWrite ? 2 : 1) ? new Promise(() => {}) : Promise.resolve(source) });
		t.mock.timers.enable({ apis: ['setTimeout'] });
		try {
			const pending = documentSourceRoundtrip({ ...target, snapshot: snapshot() });
			const rejected = postWrite ? undefined : assert.rejects(pending, /within 10000 ms/);
			for (let i = 0; i < 100 && reads < (postWrite ? 2 : 1); i++) await Promise.resolve();
			assert.equal(reads, postWrite ? 2 : 1);
			t.mock.timers.tick(10000);
			if (postWrite) {
				const result = (await pending).result!;
				assert.equal(result.beforeSource, source);
				assert.equal(result.afterSource, null);
				assert.equal(result.verified, false);
				assert.equal(result.incomplete, true);
				assert.match(String(result.readbackError), /within 10000 ms/);
				assert.deepEqual(host.writes, [source]);
			} else {
				await rejected;
				assert.deepEqual(host.writes, []);
			}
		} finally { t.mock.timers.reset(); host.restore(); }
	}
});
