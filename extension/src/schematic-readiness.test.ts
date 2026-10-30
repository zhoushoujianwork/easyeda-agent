/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pendingDeadlines, sweepDeadlines } from './deadlines';
import { captureSchematicReadiness, readinessWaitMs, waitForSchematicReady } from './schematic-readiness';
import { BUDGET_REPLY_MARGIN_MS, remainingBudgetMs, setActiveDeadline } from './request-budget';

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

// Live 2026-10-17: a background tab failed healthy writes after 5 s with
// "bring the tab to the foreground" although the request had most of its 20 s
// budget left. Background waits use the running request's remaining budget;
// the foreground keeps the short wait.
test('readiness waits for the remaining request budget in a background tab', () => {
	assert.equal(readinessWaitMs(5000, false, 18000), 5000, 'foreground keeps its short wait');
	assert.equal(readinessWaitMs(5000, true, 18000), 18000, 'background waits as long as the request allows');
	assert.equal(readinessWaitMs(5000, true, 1000), 5000, 'never shorter than the foreground wait');
	assert.equal(readinessWaitMs(5000, true, undefined), 5000, 'no running request: the given timeout');
});

test('the running request budget is visible to handler waits and cleared after', () => {
	setActiveDeadline(10_000);
	assert.equal(remainingBudgetMs(4_000), 10_000 - 4_000 - BUDGET_REPLY_MARGIN_MS);
	assert.equal(remainingBudgetMs(20_000), 0);
	setActiveDeadline(undefined);
	assert.equal(remainingBudgetMs(), undefined);
});

test('a quiet window restarts when a sync appears after the first idle sample (#256/#257)', async t => {
	const { runner, idle } = fixture(t);
	idle();
	// Drive every observed host state and deadline explicitly. Real timers can
	// skip the entire busy interval when the CI event loop is delayed.
	const originalNow = Date.now;
	let now = 1000, settled = false;
	Date.now = () => now;
	t.after(() => { Date.now = originalNow; });
	const work = waitForSchematicReady(context, 5000, undefined, 60).then(() => { settled = true; });
	await flush();
	assert.equal(settled, false);

	now = 1020;
	Object.assign(runner, { running: true, currentAction: { name: 'RealTimeSync', status: 'running' } });
	sweepDeadlines(); await flush();
	assert.equal(settled, false);

	now = 1050; idle();
	sweepDeadlines(); await flush();
	now = 1060; sweepDeadlines(); await flush();
	assert.equal(settled, false, 'the original quiet deadline must not survive an observed sync');
	now = 1109; sweepDeadlines(); await flush();
	assert.equal(settled, false, '59 ms of post-sync idle is not a full 60 ms window');
	now = 1110; sweepDeadlines(); await work;
	assert.equal(settled, true);
});

test('without a quiet window one idle sample is still enough', async t => {
	const { g, idle } = fixture(t);
	idle();
	let reads = 0;
	g.eda.dmt_SelectControl.getCurrentDocumentInfo = async () => { reads++; return { ...context }; };
	await waitForSchematicReady(context);
	assert.equal(reads, 1, 'idle readiness must not schedule another identity sample');
	assert.equal(pendingDeadlines(), 0, 'idle readiness completes without another deadline sample');
});
