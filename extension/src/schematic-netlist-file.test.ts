/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { getSchematicNetlistFile } from './schematic-netlist-file';

for (const mode of ['success', 'empty', 'export-error', 'drift', 'activation-false', 'activation-drift', 'activation-error', 'readback-error']) {
	test(`netlist editor focus: ${mode}`, async t => {
		const globals = globalThis as any, old = globals.eda;
		t.after(() => { globals.eda = old; });
		let tab = { uuid: 'page', tabId: 'page@project' };
		const calls: Array<string> = [];
		const file = { name: 'netlist.json' };
		globals.eda = {
			dmt_SelectControl: { getCurrentDocumentInfo: async () => {
				if (mode === 'readback-error' && calls.length) throw new Error('host read failed');
				return { ...tab };
			} },
			dmt_EditorControl: { activateDocument: async (id: string) => {
				calls.push(`activate:${id}`);
				if (mode === 'activation-error') throw new Error('host activation failed');
				if (mode === 'activation-drift') tab = { uuid: 'other', tabId: 'other@project' };
				return mode !== 'activation-false';
			} },
			sch_ManufactureData: { getNetlistFile: async () => {
				calls.push('export');
				if (mode === 'drift') tab = { uuid: 'other', tabId: 'other@project' };
				if (mode === 'export-error') throw new Error('export failed');
				return mode === 'empty' ? undefined : file;
			} },
		};
		if (mode === 'success' || mode === 'empty') assert.equal(await getSchematicNetlistFile(), mode === 'empty' ? undefined : file);
		else await assert.rejects(getSchematicNetlistFile(), /export failed|document changed|restore editor focus|identity changed|Cannot verify editor focus/);
		assert.deepEqual(calls, mode === 'drift' || mode === 'readback-error' ? ['export'] : ['export', 'activate:page@project']);
	});
}
