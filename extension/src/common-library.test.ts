/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { parseSystemCommonLibrary, listSystemCommonLibrary } from './common-library';
import { runAction } from './actions';

const fixture = JSON.parse(readFileSync(join(__dirname, 'testdata/common-library/system-preference.json'), 'utf8'));
const systemUuid = '0819f05c4eef4c71ace90d822a990e87';
function syntheticCatalogue(): any {
	return { success: true, result: { commSystemSetting: { BASE_LIBRARY: [{ name: '安装器件', data: [
		{ category: '安装器件', group: '螺丝', device: 'device-m2', deviceName: 'M2螺丝', pcb: true, sch: 'true' },
		{ category: '安装器件', group: '螺丝', device: 'device-m3', deviceName: 'M3螺丝', devicePath: 'model-library', pcb: 'true', sch: true },
		{ category: '安装器件', group: '', device: 'mark', deviceName: 'Mark', pcb: 'true', sch: false },
	] }] } } };
}

test('Common Library source preserves model groups and never verifies or selects their first model', () => {
	for (const result of [fixture.result, JSON.stringify(fixture.result)]) {
		const models = parseSystemCommonLibrary({ success: true, result }, systemUuid);
		assert.equal(models.length, 2);
		assert.deepEqual(models.map(m => m.group), ['RES-SMD', 'RES-SMD']);
		assert.equal(models[0].deviceUuid, 'f61af53b8d604d888b6dd9daa1a301b0');
		assert.equal(models[0].footprintUuid, '50b4943912284dab97752312e589e9e2');
		assert.equal(models[0].libraryUuid, systemUuid);
		assert.equal(models[0].identityVerified, false);
		assert.equal(models[0].pcb, true);
		assert.equal(models[0].schematic, true);
	}
	const models = parseSystemCommonLibrary(syntheticCatalogue(), systemUuid);
	assert.equal(models[1].libraryUuid, 'model-library');
	assert.equal(models[2].group, '');
	assert.equal(models[2].schematic, false);
});

test('unknown or partial Common Library source cannot turn into a successful empty library', () => {
	for (const source of [
		{}, { success: false, result: fixture.result }, { success: true, result: '{}' },
		{ success: true, result: '{' }, { success: true, result: { commSystemSetting: { BASE_LIBRARY: {} } } },
	]) assert.throws(() => parseSystemCommonLibrary(source, systemUuid), /Unsupported Common Library source/);
	for (const override of [{ device: '' }, { devicePath: 42 }, { category: 'different' }, { pcb: 'yes' }]) {
		const source = syntheticCatalogue();
		Object.assign(source.result.commSystemSetting.BASE_LIBRARY[0].data[1], override);
		assert.throws(() => parseSystemCommonLibrary(source, systemUuid), /Unsupported Common Library source/);
	}
	assert.throws(() => parseSystemCommonLibrary(fixture, ''), /System library UUID/);
	assert.throws(() => parseSystemCommonLibrary({ success: true, result: { commSystemSetting: {
		BASE_LIBRARY: [{ name: 'changed schema', devices: [] }],
	} } }, systemUuid), /unknown category fields/);
	assert.deepEqual(parseSystemCommonLibrary({ success: true, result: { commSystemSetting: { BASE_LIBRARY: [] } } }, systemUuid), []);
});

function mockEditor(t: import('node:test').TestContext, options: { international?: boolean; private?: boolean; web?: boolean; status?: number; source?: unknown } = {}): any {
	const globals = globalThis as any, previous = globals.eda;
	const requests: any[] = [];
	globals.eda = {
		sys_Environment: {
			isWeb: () => options.web !== false, isProPrivateEdition: () => options.private === true,
			isJLCEDAProEdition: () => !options.international, isEasyEDAProEdition: () => !!options.international,
		},
		sys_ClientUrl: { request: async (...args: any[]) => {
			requests.push(args);
			return new Response(JSON.stringify(options.source ?? syntheticCatalogue()), { status: options.status ?? 200 });
		} },
		lib_LibrariesList: { getSystemLibraryUuid: async () => systemUuid },
		lib_Device: { search: () => { throw new Error('LCSC search is forbidden in this test'); } },
	};
	t.after(() => { globals.eda = previous; });
	return requests;
}

test('typed Common Library action reads its own source and finds explicit M3 rather than the first grouped variant', async t => {
	const requests = mockEditor(t);
	const response = await runAction('library.common.list', { category: '安装器件', group: '螺丝', query: 'm3' });
	const result = response.result as any;
	assert.deepEqual(requests, [['https://pro.lceda.cn/api/system/preference', 'GET']]);
	assert.equal(result.count, 1);
	assert.equal(result.catalogueCount, 3);
	assert.equal(result.devices[0].deviceUuid, 'device-m3');
	assert.equal(result.devices[0].libraryUuid, 'model-library');
	assert.equal(result.devices[0].identityVerified, false);
	assert.equal(result.selectedModelAvailable, false);
});

test('Common Library refuses HTTP errors without changing site or falling back to a search', async t => {
	const requests = mockEditor(t, { international: true, status: 403 });
	await assert.rejects(() => listSystemCommonLibrary({}), /HTTP 403/);
	assert.deepEqual(requests, [['https://pro.easyeda.com/api/system/preference', 'GET']]);
});

test('Common Library rejects unsupported live schemas instead of reporting zero models', async t => {
	mockEditor(t, { source: { success: true, result: {} } });
	await assert.rejects(() => listSystemCommonLibrary({}), /commSystemSetting/);
});

test('missing configured model names cannot masquerade as no M3 matches', async t => {
	const source = syntheticCatalogue();
	delete source.result.commSystemSetting.BASE_LIBRARY[0].data[1].deviceName;
	mockEditor(t, { source });
	assert.equal((await listSystemCommonLibrary({ category: '安装器件' })).result?.count, 3);
	await assert.rejects(() => listSystemCommonLibrary({ category: '安装器件', query: 'M3' }), /model names are unavailable/);
});

test('Common Library source adapter declines client and private environments without HTTP requests', async t => {
	for (const options of [{ web: false }, { private: true }]) {
		await t.test(JSON.stringify(options), async child => {
			const requests = mockEditor(child, options);
			await assert.rejects(() => listSystemCommonLibrary({}), /public Web editor only/);
			assert.deepEqual(requests, []);
		});
	}
});
