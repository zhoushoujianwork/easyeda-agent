/// <reference types="@jlceda/pro-api-types" />

import assert from 'node:assert/strict';
import { after, test } from 'node:test';

const globals = globalThis as Record<string, any>;
const registrations: string[] = [];

globals.Worker = class {
	set onmessage(_handler: () => void) {}
};

globals.eda = {
	sys_WebSocket: {
		register(id: string): void {
			registrations.push(id);
		},
		send(): void {},
		close(): void {},
	},
	sys_Message: { showToastMessage(): void {} },
	sys_I18n: { text: (value: string): string => value },
	sys_Log: { add(): void {} },
	sys_Storage: { getExtensionUserConfig: (): boolean => true },
	sys_Environment: { getEditorCurrentVersion: (): string => '3.2.175-test' },
	dmt_Project: { getCurrentProjectInfo: async (): Promise<never> => { throw new Error('no project'); } },
	dmt_SelectControl: { getCurrentDocumentInfo: async (): Promise<never> => { throw new Error('no document'); } },
};

const transport = require('./transport') as {
	bootstrapFromModuleLoad: () => void;
	deactivate: () => void;
	getConnectionStatus: () => { connecting: boolean };
	reconnect: () => void;
	start: (source?: 'activate' | 'bootstrap') => void;
	stop: (showToast?: boolean) => void;
};

after(() => transport.deactivate());

const sleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

test('module bootstrap cannot restart after stop invalidates its delayed callback', async () => {
	transport.bootstrapFromModuleLoad();
	transport.stop(false);

	await sleep(350);
	assert.equal(registrations.length, 0, 'deactivate/stop must cancel the pending module-load retry');

	transport.reconnect();
	await sleep(250);
	assert.equal(registrations.length, 1, 'an explicit reconnect must still be allowed after stop');

	transport.stop(false);
});

test('re-evaluating the bundle reuses one controller and delegates lifecycle actions', async () => {
	registrations.length = 0;
	transport.deactivate();
	const modulePath = require.resolve('./transport');
	const loadFresh = (): typeof transport => {
		delete require.cache[modulePath];
		return require('./transport');
	};

	const firstEvaluation = loadFresh();
	firstEvaluation.bootstrapFromModuleLoad();
	await sleep(250);
	assert.equal(registrations.length, 1, 'the owning evaluation should register once');

	const secondEvaluation = loadFresh();
	secondEvaluation.bootstrapFromModuleLoad();
	secondEvaluation.start('activate');
	await sleep(250);
	assert.equal(registrations.length, 1, 'a later evaluation must reuse the existing controller');
	assert.equal(secondEvaluation.getConnectionStatus().connecting, true, 'status must come from the owning evaluation');

	secondEvaluation.stop(false);
	secondEvaluation.reconnect();
	await sleep(250);
	assert.equal(registrations.length, 2, 'reconnect from a later evaluation must reach the owning controller');

	secondEvaluation.deactivate();
	const thirdEvaluation = loadFresh();
	thirdEvaluation.bootstrapFromModuleLoad();
	await sleep(250);
	assert.equal(registrations.length, 3, 'deactivate must release ownership for a subsequent extension load');
	thirdEvaluation.stop(false);
});
