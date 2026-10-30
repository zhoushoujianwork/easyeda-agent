/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readConnectorLogs } from './connector-logs';

test('connector diagnostics are bounded, detached and exclude unrelated log messages', async () => {
	const rows = [{ timestamp: 1, type: 'info', message: 'private project message' },
		{ timestamp: 2, type: 'info', message: '[easyeda-agent] connected', extra: 'not returned' },
		{ timestamp: 3, type: 'warn', message: '[easyeda-agent] liveness lost' }];
	(globalThis as any).eda = { sys_Log: { sort: async () => rows } };
	assert.deepEqual(await readConnectorLogs(1), [{ timestamp: 3, type: 'warn', message: '[easyeda-agent] liveness lost' }]);
	assert.equal((await readConnectorLogs(100)).length, 2);
	assert.equal(rows.length, 3);
	for (const limit of [0, 101, -1, 1.5, null, '1']) await assert.rejects(readConnectorLogs(limit), /integer/);
});

test('connector diagnostics refuse missing or malformed observations and bound each line', async () => {
	(globalThis as any).eda = { sys_Log: { sort: async () => null } };
	await assert.rejects(readConnectorLogs(3), /unavailable/);
	(globalThis as any).eda.sys_Log.sort = async () => [{ text: 'unknown host shape' }];
	await assert.rejects(readConnectorLogs(3), /shape is unsupported/);
	(globalThis as any).eda.sys_Log.sort = async () => [{ message: '[easyeda-agent] incomplete' }];
	await assert.rejects(readConnectorLogs(3), /incomplete/);
	(globalThis as any).eda.sys_Log.sort = async () => [{ timestamp: 1, type: 'info', message: '[easyeda-agent] ' + 'x'.repeat(3000) }];
	assert.equal((await readConnectorLogs(1))[0].message.length, 2000);
});
