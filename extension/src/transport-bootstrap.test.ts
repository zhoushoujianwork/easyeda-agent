/// <reference types="@jlceda/pro-api-types" />

import assert from 'node:assert/strict';
import { test } from 'node:test';

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
	reconnect: () => void;
	stop: (showToast?: boolean) => void;
};

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
