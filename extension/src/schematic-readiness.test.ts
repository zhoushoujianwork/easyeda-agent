/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pendingDeadlines, sweepDeadlines } from './deadlines';
import { captureSchematicReadiness, waitForSchematicReady } from './schematic-readiness';

const context = { uuid: 'page', tabId: 'page@project' };
function fixture(t: any) {
	const g = globalThis as any, old = { eda: g.eda, window: g.window };
	const document = { uuid: 'page', tabId: 'page@project', profileSetting: { readonlyMode: false } };
	const runner = { running: true, currentAction: { name: 'RealTimeSync', status: 'running' } as any };
	g.window = { SCH: { docMemoryManager: { getActiveDoc: () => document }, app: { actionRunner: runner } } };
	g.eda = { dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ ...context }) } };
	t.after(() => { Object.assign(g, old); assert.equal(pendingDeadlines(), 0); });
	return { g, runner, idle: () => Object.assign(runner, { running: false, currentAction: null }) };
}
const flush = async () => { await Promise.resolve(); await Promise.resolve(); };

test('RealTimeSync waits for idle without touching host transactions', async t => {
	const { runner, idle } = fixture(t);
	for (const key of ['start', 'end', 'cancel', 'waitEnd']) Object.defineProperty(runner, key, { get() { throw new Error('private method accessed'); } });
	let writes = 0;
	const work = waitForSchematicReady(context).then(() => writes++);
	await flush(); assert.equal(writes, 0);
	sweepDeadlines(Date.now() + 100); await flush(); assert.equal(writes, 0);
	idle(); sweepDeadlines(Date.now() + 100); await work;
	assert.equal(writes, 1);
});

for (const mode of ['timeout', 'document-drift', 'runner-replaced', 'non-sync', 'malformed', 'unsupported', 'read-error']) {
	test(`readiness refuses ${mode}`, async t => {
		const { g, runner } = fixture(t);
		let writes = 0;
		const work = waitForSchematicReady(context).then(() => writes++);
		const check = assert.rejects(work);
		await flush();
		if (mode === 'document-drift') g.eda.dmt_SelectControl.getCurrentDocumentInfo = async () => ({ uuid: 'other', tabId: 'other@project' });
		if (mode === 'runner-replaced') g.window.SCH.app.actionRunner = { running: false, currentAction: null };
		if (mode === 'non-sync') runner.currentAction.name = 'copy';
		if (mode === 'malformed') runner.running = false;
		if (mode === 'unsupported') delete g.window.SCH;
		if (mode === 'read-error') g.eda.dmt_SelectControl.getCurrentDocumentInfo = async () => { throw new Error('read failed'); };
		sweepDeadlines(Date.now() + (mode === 'timeout' ? 6000 : 100));
		await check; assert.equal(writes, 0);
	});
}

test('a timed-out identity read cannot later issue a write', async t => {
	const { g, idle } = fixture(t);
	idle();
	let resolveRead!: (value: any) => void, writes = 0;
	g.eda.dmt_SelectControl.getCurrentDocumentInfo = () => new Promise(resolve => { resolveRead = resolve; });
	const work = waitForSchematicReady(context).then(() => writes++);
	const check = assert.rejects(work, /Timed out/);
	sweepDeadlines(Date.now() + 6000); await check;
	resolveRead(context); await flush();
	assert.equal(writes, 0);
});

test('sync ending into a different transaction is never accepted as idle', async t => {
	const { runner } = fixture(t);
	const work = waitForSchematicReady(context);
	const check = assert.rejects(work, /non-sync/);
	await flush(); runner.currentAction = { name: 'move', status: 'running' };
	sweepDeadlines(Date.now() + 100); await check;
});

test('same UUID and tab with a replacement document object is refused', async t => {
	const { g, idle } = fixture(t);
	const work = waitForSchematicReady(context), check = assert.rejects(work, /instance changed/);
	await flush(); idle(); g.window.SCH.docMemoryManager.getActiveDoc = () => ({ uuid: 'page', tabId: 'page@project', profileSetting: { readonlyMode: false } });
	sweepDeadlines(Date.now() + 100); await check;
});

test('elapsed deadline refuses idle even when deadline callbacks have not fired', async t => {
	const { g, idle } = fixture(t); idle();
	let resolveRead!: (value: any) => void, writes = 0;
	const oldNow = Date.now, start = oldNow(); t.after(() => { Date.now = oldNow; });
	g.eda.dmt_SelectControl.getCurrentDocumentInfo = () => new Promise(resolve => { resolveRead = resolve; });
	const work = waitForSchematicReady(context).then(() => writes++), check = assert.rejects(work, /Timed out/);
	Date.now = () => start + 6000; resolveRead(context); await check;
	assert.equal(writes, 0);
});

test('private host getter exceptions use a readiness error at initial capture', async t => {
	const { g } = fixture(t);
	Object.defineProperty(g.window.SCH, 'app', { get() { throw new Error('host getter failed'); } });
	await assert.rejects(waitForSchematicReady(context), err => {
		assert.equal((err as any).constructor.name, 'SchematicReadinessError'); return true;
	});
});

test('an operation token rejects same-document replacement before the wait begins', async t => {
	const { g, idle } = fixture(t); idle();
	const token = captureSchematicReadiness(context);
	g.window.SCH.docMemoryManager.getActiveDoc = () => ({ ...context, profileSetting: { readonlyMode: false } });
	await assert.rejects(waitForSchematicReady(context, 5000, token), /instance changed/);
});

for (const readonlyMode of [true, undefined]) {
	test(`readonly state ${readonlyMode} refuses readiness`, async t => {
		const { g, idle } = fixture(t); idle();
		g.window.SCH.docMemoryManager.getActiveDoc().profileSetting.readonlyMode = readonlyMode;
		await assert.rejects(waitForSchematicReady(context), /writable state/);
	});
}

test('private document getter exceptions use a readiness error at initial capture', async t => {
	const { g } = fixture(t);
	Object.defineProperty(g.window.SCH.docMemoryManager.getActiveDoc(), 'profileSetting', { get() { throw new Error('document getter failed'); } });
	await assert.rejects(waitForSchematicReady(context), err => {
		assert.equal((err as any).constructor.name, 'SchematicReadinessError'); return true;
	});
});
