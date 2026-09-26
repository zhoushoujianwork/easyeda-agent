/// <reference types="@jlceda/pro-api-types" />
/**
 * Regression tests: a muted netlist must never masquerade as a design fact.
 *
 * Live context (B04_2_1 review, 2026-09-20). The reviewer read a page with
 * `sch list --include-pins`, saw every pin report `net: null` while the
 * component-level `pinsAvailable` was `true`, and concluded the block instances
 * simply do not expose their nets from the platform. The real cause was a netlist
 * export that returned no file: `collectNetlistPinNets()` degraded to `available:
 * false`, the caller dropped that flag on the floor, and every pin's net was
 * forced to `null` — indistinguishable from "known unconnected".
 *
 * These tests pin the contract: when the netlist cannot be fetched or parsed, the
 * result says so at BOTH the envelope (`pinNetsAvailable` / `netlistAvailable`)
 * and the per-component level (`netlistAvailable` / `netlistError`), with a cause.
 */

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { runAction, schematicComponentsList } from './actions';

function pin(number: string, name: string, x: number, y: number): unknown {
	return {
		getState_PrimitiveId: () => `pin-${number}`,
		getState_PinNumber: () => number,
		getState_PinName: () => name,
		getState_X: () => x,
		getState_Y: () => y,
		getState_Rotation: () => 0,
		getState_NoConnected: () => false,
	};
}

function part(designator: string, x: number, y: number): unknown {
	return {
		getState_PrimitiveId: () => `prim-${designator}`,
		getState_ComponentType: () => 'part',
		getState_Designator: () => designator,
		getState_Name: () => 'R0603',
		getState_X: () => x,
		getState_Y: () => y,
		getState_Rotation: () => 0,
		getState_Mirror: () => false,
		getState_Net: () => '',
		getState_SubPartName: () => '',
		getState_AddIntoBom: () => true,
		getState_AddIntoPcb: () => true,
		getState_UniqueId: () => `uid-${designator}`,
		getState_Manufacturer: () => '',
		getState_ManufacturerId: () => '',
		getState_Supplier: () => '',
		getState_SupplierId: () => 'C25804',
		getState_Component: () => ({ uuid: 'a'.repeat(32), libraryUuid: 'b'.repeat(32), name: 'R0603' }),
		getState_Symbol: () => ({ uuid: 'c'.repeat(32) }),
		getState_Footprint: () => ({ uuid: 'd'.repeat(32) }),
		getState_OtherProperty: () => ({}),
	};
}

/** Install one R1 with two pins, plus a netlist source that behaves as asked. */
function install(t: { after: (f: () => void) => void }, netlist: () => Promise<unknown>): void {
	const globals = globalThis as any;
	const previous = globals.eda;
	t.after(() => { globals.eda = previous; });
	globals.eda = {
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'page', tabId: 'page@project' }) },
		dmt_EditorControl: { activateDocument: async () => true },
		sch_PrimitiveComponent: {
			getAll: async () => [part('R1', 100, 200)],
			getAllPinsByPrimitiveId: async () => [pin('1', 'a', 90, 200), pin('2', 'b', 110, 200)],
		},
		sch_PrimitiveWire: { getAll: async () => [] },
		sch_ManufactureData: { getNetlistFile: netlist },
	};
}

const goodNetlist = async () => ({
	text: async () => JSON.stringify({
		components: {
			'lib-1': { props: { Designator: 'R1' }, pinInfoMap: { p1: { number: '1', net: 'VCC' }, p2: { number: '2', net: 'GND' } } },
		},
	}),
});

test('list: a healthy netlist reports pinNetsAvailable and real per-pin nets', async t => {
	install(t, goodNetlist);
	const out: any = await schematicComponentsList({ includePins: true });
	assert.equal(out.result.pinNetsAvailable, true);
	assert.equal(out.result.pinNetsError, undefined);
	assert.deepEqual(out.result.components[0].pins.map((p: any) => p.net), ['VCC', 'GND']);
	assert.equal(out.result.components[0].netlistAvailable, true);
	assert.equal(out.result.components[0].netlistError, undefined);
});

test('list: an export that returns no file is reported, and nulls are explained', async t => {
	install(t, async () => undefined);
	const out: any = await schematicComponentsList({ includePins: true });
	assert.equal(out.result.pinNetsAvailable, false);
	assert.match(out.result.pinNetsError, /no file/);
	assert.equal(out.result.components[0].pinsAvailable, true);
	assert.ok(out.result.components[0].pins.every((p: any) => p.net === null));
	// The component says WHY the nulls are null — this is the flag that was missing.
	assert.equal(out.result.components[0].netlistAvailable, false);
	assert.match(out.result.components[0].netlistError, /no file/);
});

test('list: an export that throws is reported with its cause', async t => {
	install(t, async () => { throw new Error('EDA_CALL_FAILED: platform refused'); });
	const out: any = await schematicComponentsList({ includePins: true });
	assert.equal(out.result.pinNetsAvailable, false);
	assert.match(out.result.pinNetsError, /platform refused/);
	assert.equal(out.result.components[0].netlistAvailable, false);
});

test('list: unparseable netlist JSON is reported rather than silently emptying every net', async t => {
	install(t, async () => ({ text: async () => '<html>not json</html>' }));
	const out: any = await schematicComponentsList({ includePins: true });
	assert.equal(out.result.pinNetsAvailable, false);
	assert.match(out.result.pinNetsError, /parse failed/);
});

test('list: the netlist flags are omitted when pin nets were not requested', async t => {
	install(t, goodNetlist);
	const out: any = await schematicComponentsList({ includePins: true, includePinNets: false });
	assert.equal('pinNetsAvailable' in out.result, false);
	assert.equal('pinNetsError' in out.result, false);
});

test('read: a muted netlist flags the floating-pin list as untrustworthy', async t => {
	install(t, async () => undefined);
	const out: any = await runAction('schematic.read', { includeCheck: false });
	assert.equal(out.result.netlistAvailable, false);
	assert.match(out.result.netlistError, /no file/);
	// Every pin lands in floatingPins precisely because the netlist is missing — the
	// flag is the only thing separating that from a real "nothing is connected".
	assert.deepEqual(out.result.floatingPins.sort(), ['R1.1', 'R1.2']);
	assert.equal(out.result.netCount, 0);
});

test('read: a healthy netlist carries available=true and builds the net table', async t => {
	install(t, goodNetlist);
	const out: any = await runAction('schematic.read', { includeCheck: false });
	assert.equal(out.result.netlistAvailable, true);
	assert.equal(out.result.netlistError, undefined);
	assert.deepEqual(out.result.floatingPins, []);
	assert.deepEqual(out.result.nets.map((n: any) => [n.net, n.degree]).sort(), [['GND', 1], ['VCC', 1]]);
});
