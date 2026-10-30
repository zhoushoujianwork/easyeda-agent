import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pcbExportGerber, pcbExportPlacement, MAX_MANUFACTURE_BYTES } from './pcb-export-manufacture';

function host(options: { drift?: boolean; empty?: boolean; missing?: boolean; oversized?: boolean; noComponents?: boolean; unsupported?: boolean } = {}) {
	let reads = 0;
	const calls: unknown[][] = [];
	(globalThis as any).EDMT_EditorDocumentType = { PCB: 3 };
	const getter = async (...args: unknown[]) => {
		calls.push(args);
		if (options.missing) return undefined;
		if (options.oversized) return { size: MAX_MANUFACTURE_BYTES+1 };
		return new File([options.empty ? '' : 'exact-official-bytes'], 'host-result.zip');
	};
	(globalThis as any).eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'p' }) },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: options.drift && ++reads > 1 ? 'changed' : 'pcb', documentType: 3, tabId: 'tab' }) },
		pcb_PrimitiveComponent: { getAll: async () => options.noComponents ? [] : [{}] },
		pcb_ManufactureData: options.unsupported ? {} : { getGerberFile: getter, getPickAndPlaceFile: getter },
	};
	return calls;
}

test('manufacture exports preserve official bytes and exact parameter order', async () => {
	let calls = host();
	const gerber = await pcbExportGerber({ fileName: 'board.zip', unit: 'inch', colorSilkscreen: true });
	assert.deepEqual(calls[0], ['board.zip', true, 'inch', { integerNumber: 3, decimalNumber: 6 }]);
	assert.equal(gerber.result?.fileType, 'zip');
	assert.equal(Buffer.from(gerber.artifacts![0].inlineBase64!, 'base64').toString(), 'exact-official-bytes');
	assert.equal(gerber.artifacts![0].fileName, 'host-result.zip');
	calls = host();
	const placement = await pcbExportPlacement({ fileType: 'xlsx', unit: 'mil' });
	assert.deepEqual(calls[0], ['board.xlsx', 'xlsx', 'mil']);
	assert.equal(placement.result?.unit, 'mil');
});

test('manufacture defaults match documented official settings', async () => {
	let calls = host(); await pcbExportGerber({});
	assert.deepEqual(calls[0], ['board.zip', false, 'mm', { integerNumber: 4, decimalNumber: 5 }]);
	calls = host(); await pcbExportPlacement({});
	assert.deepEqual(calls[0], ['board.csv', 'csv', 'mm']);
});

test('unavailable, undefined, empty, oversized, empty-board and target-drift exports fail without artifacts', async () => {
	for (const opts of [{unsupported:true}, {missing:true}, {empty:true}, {oversized:true}, {noComponents:true}, {drift:true}]) {
		for (const exporter of [pcbExportGerber, pcbExportPlacement]) { host(opts); await assert.rejects(exporter({})); }
	}
	host({unsupported:true}); await assert.rejects(pcbExportGerber({}), (e: any) => e.code === 'EDA_API_UNAVAILABLE');
});

test('invalid manufacture options never invoke the getter', async () => {
	const calls = host();
	for (const bad of [{unit:'mil'}, {colorSilkscreen:'true'}, {digitalFormat:{integerNumber:0,decimalNumber:5}}, {digitalFormat:{integerNumber:4,decimalNumber:5,extra:1}}, {fileName:'../board'}, {surprise:true}]) await assert.rejects(pcbExportGerber(bad));
	for (const bad of [{unit:'inch'}, {fileType:'pdf'}, {fileName:'a/b'}, {digitalFormat:{}}]) await assert.rejects(pcbExportPlacement(bad));
	assert.equal(calls.length, 0);
});
