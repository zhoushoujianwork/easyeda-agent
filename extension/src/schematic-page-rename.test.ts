/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { renameSchematicPage } from './schematic-page-rename';
import { ActionError } from './protocol';
import { runAction } from './actions';
import { pendingDeadlines, sweepDeadlines } from './deadlines';

const page = (name: string) => [{ uuid: 'page', name }];
async function fixture(host: unknown, reads: unknown[], run: (counts: () => { writes: number; reads: number }) => Promise<void>) {
	const globals = globalThis as Record<string, any>, original = globals.eda;
	let writes = 0, readCount = 0;
	globals.eda = { dmt_Schematic: {
		modifySchematicPageName: async (uuid: string, name: string) => {
			assert.equal(uuid, 'page'); assert.equal(name, 'new'); writes++;
			if (host instanceof Error) throw host;
			return host;
		},
		getAllSchematicPagesInfo: async () => {
			const value = reads[Math.min(readCount++, reads.length - 1)];
			if (value instanceof Error) throw value;
			return value;
		},
	} };
	try { await run(() => ({ writes, reads: readCount })); }
	finally { globals.eda = original; }
}

for (const [host, observed, status] of [
	[false, page('old'), 'different'],
	[false, page('new'), 'matched'],
	[undefined, page('old'), 'different'],
	[true, new Error('inventory failed'), 'unavailable'],
	[true, [], 'missing'],
	[true, [...page('new'), ...page('new')], 'ambiguous'],
	[true, undefined, 'unavailable'],
	[new Error('call failed'), page('new'), 'matched'],
] as const) {
	test(`rename refuses host=${String(host)} observation=${status} without mutation retry`, async () => fixture(host, [observed], async counts => {
		await assert.rejects(renameSchematicPage('page', 'new'), error => {
			assert.ok(error instanceof ActionError); assert.equal(error.code, 'EDA_CALL_FAILED');
			const detail = JSON.parse(error.detail!);
			assert.equal(detail.pageUuid, 'page'); assert.equal(detail.requestedName, 'new');
			assert.equal(detail.verified, false); assert.equal(detail.observation.status, status);
			assert.doesNotMatch(error.message, /cache|already submitted|unrelated write/i);
			return true;
		});
		assert.deepEqual(counts(), { writes: 1, reads: 1 });
	}));
}

test('accepted rename requires the actual matching page name', async () => fixture(true, [page('old'), page('new')], async counts => {
	assert.deepEqual(await renameSchematicPage('page', 'new'), { result: { ok: true, verified: true, pageUuid: 'page', name: 'new' } });
	assert.deepEqual(counts(), { writes: 1, reads: 2 });
}));

test('accepted rename that never appears rejects with final observed name', async () => fixture(true, [page('old')], async counts => {
	await assert.rejects(renameSchematicPage('page', 'new'), error => {
		assert.ok(error instanceof ActionError);
		assert.equal(JSON.parse(error.detail!).hostAccepted, true);
		assert.equal(JSON.parse(error.detail!).observation.name, 'old'); return true;
	});
	assert.deepEqual(counts(), { writes: 1, reads: 4 });
}));

test('real action catalog propagates false host result as a typed error', async () => fixture(false, [page('old')], async counts => {
	await assert.rejects(runAction('schematic.page.rename', { pageUuid: 'page', name: 'new' }), ActionError);
	assert.equal(counts().writes, 1);
}));


test('worker-swept read timeout preserves known refusal and ignores late read completion', async () => {
	const globals = globalThis as Record<string, any>;
	const original = { eda: globals.eda, setTimeout: globals.setTimeout, clearTimeout: globals.clearTimeout, now: Date.now };
	let now = 10000, finishRead: ((rows: unknown) => void) | undefined, writes = 0, reads = 0;
	const before = pendingDeadlines();
	globals.setTimeout = () => 1; globals.clearTimeout = () => {}; Date.now = () => now;
	globals.eda = { dmt_Schematic: {
		modifySchematicPageName: async () => { writes++; return false; },
		getAllSchematicPagesInfo: () => { reads++; return new Promise(resolve => { finishRead = resolve; }); },
	} };
	try {
		const result = renameSchematicPage('page', 'new');
		const checked = assert.rejects(result, error => {
			assert.ok(error instanceof ActionError);
			const detail = JSON.parse(error.detail!);
			assert.equal(detail.hostAccepted, false); assert.equal(detail.pageUuid, 'page');
			assert.equal(detail.observation.status, 'unavailable');
			assert.match(detail.observation.error, /timed out/); return true;
		});
		for (let i=0; i<6; i++) await Promise.resolve();
		assert.ok(finishRead); now += 2000; sweepDeadlines(); await checked;
		finishRead(page('new')); for (let i=0; i<6; i++) await Promise.resolve();
		assert.equal(writes, 1); assert.equal(reads, 1); assert.equal(pendingDeadlines(), before);
	} finally { Object.assign(globals, { eda: original.eda, setTimeout: original.setTimeout, clearTimeout: original.clearTimeout }); Date.now = original.now; }
});

// Issue #55: the host refuses at once until its window has loaded a second page of the schematic.
function twoPageHost({ current = 'pcb' as string | undefined, refuseUntilOpened = 2, failOpen = '' } = {}) {
	const names: Record<string, string> = { page: 'old', other: 'P1' }, opened: string[] = [], log: string[] = [];
	let active = current, writes = 0;
	return { names, opened, log, writes: () => writes, active: () => active, eda: {
		dmt_Schematic: {
			modifySchematicPageName: async (uuid: string, name: string) => {
				writes++; log.push(`rename:${uuid}`);
				if (new Set(opened).size < refuseUntilOpened) return false;
				names[uuid] = name; return true;
			},
			getAllSchematicPagesInfo: async () => [
				{ uuid: 'page', name: names.page, parentSchematicUuid: 'sch' },
				{ uuid: 'other', name: names.other, parentSchematicUuid: 'sch' },
				{ uuid: 'foreign', name: 'X', parentSchematicUuid: 'sch-2' },
			],
		},
		dmt_EditorControl: { openDocument: async (uuid: string) => { if (uuid === failOpen) throw new Error('open failed'); log.push(`open:${uuid}`); if (uuid !== 'pcb') opened.push(uuid); active = uuid; return uuid; } },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => { if (!active) throw new Error('No active document.'); return { uuid: active }; } },
	} };
}
async function withHost<T>(host: ReturnType<typeof twoPageHost>, run: () => Promise<T>): Promise<T> {
	const globals = globalThis as Record<string, any>, original = globals.eda;
	globals.eda = host.eda;
	try { return await run(); } finally { globals.eda = original; }
}

test('known refusal loads a sibling and the target page, restores the foreground, then renames once more', async () => {
	const host = twoPageHost();
	const { result } = await withHost(host, () => renameSchematicPage('page', 'new'));
	assert.equal(result.verified, true); assert.equal(host.names.page, 'new'); assert.equal(host.names.other, 'P1');
	assert.deepEqual(host.log, ['rename:page', 'open:other', 'open:page', 'open:pcb', 'rename:page']);
	assert.equal(host.active(), 'pcb');
	assert.deepEqual(result.recovery, { reason: 'host-refused-before-second-page-loaded', opened: ['other', 'page'], restored: 'pcb' });
});

test('a fresh window without an active document keeps the target page open after the recovery', async () => {
	const host = twoPageHost({ current: '' }) // '' = the host reports no active document;
	const { result } = await withHost(host, () => renameSchematicPage('page', 'new'));
	assert.equal(result.verified, true); assert.equal(host.active(), 'page'); assert.equal(result.recovery!.restored, null);
});

test('a second refusal after loading pages is final and reports the recovery', async () => {
	const host = twoPageHost({ refuseUntilOpened: 9 });
	await withHost(host, () => assert.rejects(renameSchematicPage('page', 'new'), error => {
		assert.ok(error instanceof ActionError);
		const detail = JSON.parse(error.detail!);
		assert.equal(detail.hostAccepted, false); assert.equal(detail.observation.name, 'old');
		assert.deepEqual(detail.recovery.opened, ['other', 'page']); return true;
	}));
	assert.equal(host.writes(), 2); assert.equal(host.active(), 'pcb');
});

test('a failed page load skips the second call and still restores the foreground', async () => {
	const host = twoPageHost({ failOpen: 'page' });
	await withHost(host, () => assert.rejects(renameSchematicPage('page', 'new'), error => {
		assert.match(JSON.parse((error as ActionError).detail!).recovery.error, /open failed/); return true;
	}));
	assert.equal(host.writes(), 1); assert.equal(host.active(), 'pcb');
});
