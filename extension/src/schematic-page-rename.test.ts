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
