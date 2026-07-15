/// <reference types="@jlceda/pro-api-types" />
/**
 * Unit tests for schematic component serialization (issue #52).
 *
 * Run with: `npm test` (node:test via ts-node, no EasyEDA runtime needed).
 * These exercise pure helpers that do not touch the `eda` global. The
 * triple-slash reference above loads the ambient `eda` declaration so ts-node
 * (which follows imports, not tsconfig's include glob) can compile actions.ts.
 */

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { normalizeDeviceRef, serializeComponent } from './actions';

/** A minimal mock of eda.sch_PrimitiveComponent exposing only the getters
 *  serializeComponent reads. Casts through unknown since the real type is huge. */
function mockComponent(overrides: Record<string, unknown> = {}): any {
	const base: Record<string, unknown> = {
		PrimitiveId: 'e123',
		ComponentType: 'component',
		Designator: 'USB1',
		Name: 'TYPE-C 16PIN 2MD(073)',
		X: 100,
		Y: 200,
		Rotation: 0,
		Mirror: false,
		Net: '',
		SubPartName: '',
		AddIntoBom: true,
		AddIntoPcb: true,
		UniqueId: 'uq-1',
		Manufacturer: 'XKB',
		ManufacturerId: 'U262-16-C-N',
		Supplier: 'LCSC',
		SupplierId: 'C2765186',
		Component: { libraryUuid: 'LIB-A', uuid: 'DEV-A' },
		Symbol: { libraryUuid: 'LIB-S', uuid: 'SYM-INSTANCE' },
		Footprint: { libraryUuid: 'LIB-F', uuid: 'FP-INSTANCE' },
		OtherProperty: {},
		...overrides,
	};
	const obj: Record<string, unknown> = {};
	for (const [k, v] of Object.entries(base)) {
		obj[`getState_${k}`] = () => v;
	}
	return obj;
}

test('serializeComponent: exposes structured device identity (issue #52)', () => {
	const out = serializeComponent(mockComponent());
	assert.deepEqual(out.device, {
		libraryUuid: 'LIB-A',
		uuid: 'DEV-A',
		name: 'TYPE-C 16PIN 2MD(073)',
	});
});

test('serializeComponent: device.uuid is the device (not footprint) uuid', () => {
	const out = serializeComponent(mockComponent());
	const device = out.device as Record<string, unknown>;
	const footprint = out.footprint as Record<string, unknown>;
	assert.equal(device.uuid, 'DEV-A');
	assert.notEqual(device.uuid, footprint.uuid);
});

test('serializeComponent: keeps raw component field for backward compat', () => {
	const out = serializeComponent(mockComponent());
	assert.deepEqual(out.component, { libraryUuid: 'LIB-A', uuid: 'DEV-A' });
});

test('normalizeDeviceRef: empty libraryUuid (imported device) reported faithfully', () => {
	const ref = normalizeDeviceRef({ libraryUuid: '', uuid: 'DEV-X' }, 'Some Part');
	assert.deepEqual(ref, { libraryUuid: '', uuid: 'DEV-X', name: 'Some Part' });
});

test('normalizeDeviceRef: missing/undefined raw yields empty strings, never throws', () => {
	assert.deepEqual(normalizeDeviceRef(undefined, undefined), { libraryUuid: '', uuid: '', name: '' });
	assert.deepEqual(normalizeDeviceRef(null, 42), { libraryUuid: '', uuid: '', name: '' });
});

test('normalizeDeviceRef: non-string uuid/libraryUuid coerced to empty', () => {
	const ref = normalizeDeviceRef({ libraryUuid: 123, uuid: null }, 'X');
	assert.deepEqual(ref, { libraryUuid: '', uuid: '', name: 'X' });
});

import { schematicComponentPlace } from './actions';

/** Install a fake `eda.sch_PrimitiveComponent` on the global for one test.
 *  create() returns a placeholder-designator component; modify() records its
 *  args and returns the post-assignment component. Returns the call log. */
function installEdaStub(placeholderDesignator = 'R?') {
	const calls: { modify: Array<{ id: string; patch: any }> } = { modify: [] };
	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			create: async () => mockComponent({ Designator: placeholderDesignator, PrimitiveId: 'p1' }),
			modify: async (id: string, patch: any) => {
				calls.modify.push({ id, patch });
				return mockComponent({ Designator: patch.designator, PrimitiveId: id });
			},
		},
	};
	return calls;
}

test('place with designator: assigns atomically and returns final designator (issue #68)', async () => {
	const calls = installEdaStub('R?');
	const res: any = await schematicComponentPlace({
		libraryUuid: 'LIB-A', uuid: 'DEV-A', x: 100, y: 200, designator: 'R12',
	});
	assert.equal(calls.modify.length, 1);
	assert.equal(calls.modify[0].id, 'p1');
	assert.deepEqual(calls.modify[0].patch, { designator: 'R12' });
	assert.equal(res.result.primitiveId, 'p1');
	assert.equal((res.result.component as any).designator, 'R12');
	delete (globalThis as any).eda;
});

test('place without designator: no modify call, keeps placeholder (issue #68)', async () => {
	const calls = installEdaStub('C?');
	const res: any = await schematicComponentPlace({
		libraryUuid: 'LIB-A', uuid: 'DEV-A', x: 100, y: 200,
	});
	assert.equal(calls.modify.length, 0);
	assert.equal((res.result.component as any).designator, 'C?');
	delete (globalThis as any).eda;
});

// ─── pcb.page.clear scope parsing (pure, no eda runtime) ─────────────────
import { parsePcbClearScopes } from './actions';

test('parsePcbClearScopes: omitted → all five scopes', () => {
	assert.deepEqual(parsePcbClearScopes(undefined), ['components', 'routing', 'copper', 'regions', 'silk']);
	assert.deepEqual(parsePcbClearScopes(''), ['components', 'routing', 'copper', 'regions', 'silk']);
	assert.deepEqual(parsePcbClearScopes(null), ['components', 'routing', 'copper', 'regions', 'silk']);
});

test('parsePcbClearScopes: comma string is trimmed, lower-cased, de-duped, canonical order', () => {
	// Input order (silk before routing) must NOT survive — canonical order wins.
	assert.deepEqual(parsePcbClearScopes(' Silk , routing , SILK '), ['routing', 'silk']);
});

test('parsePcbClearScopes: accepts a string[]', () => {
	assert.deepEqual(parsePcbClearScopes(['copper', 'components']), ['components', 'copper']);
});

test('parsePcbClearScopes: whitespace-only → all scopes (not empty)', () => {
	assert.deepEqual(parsePcbClearScopes(' , '), ['components', 'routing', 'copper', 'regions', 'silk']);
});

test('parsePcbClearScopes: unknown scope throws', () => {
	assert.throws(() => parsePcbClearScopes('components,bogus'), /Unknown clear scope/);
});

// ─── pcb.page.clear handler (mock eda) — locks in the review fixes ────────
import { pcbPageClear } from './actions';

/** A minimal PCB primitive: id + optional layer + lock state. */
function pcbPrim(id: string, layer?: number, locked = false): any {
	const o: any = { getState_PrimitiveId: () => id, getState_PrimitiveLock: () => locked };
	if (layer !== undefined) o.getState_Layer = () => layer;
	return o;
}

/** Stub every pcb_Primitive* class pcbPageClear touches; record deleted ids per class. */
function installPcbClearStub(fx: {
	components?: any[]; lines?: any[]; arcs?: any[]; vias?: any[];
	pours?: any[]; fills?: any[]; regions?: any[]; strings?: any[]; polylines?: any[];
	delResult?: boolean;
}): { deleted: Record<string, string[]> } {
	const deleted: Record<string, string[]> = {};
	const delResult = fx.delResult ?? true;
	const mk = (key: string, items: any[] | undefined) => ({
		getAll: async () => items ?? [],
		delete: async (ids: string[]) => { (deleted[key] ??= []).push(...ids); return delResult; },
	});
	(globalThis as any).eda = {
		pcb_PrimitiveComponent: mk('components', fx.components),
		pcb_PrimitiveLine: mk('lines', fx.lines),
		pcb_PrimitiveArc: mk('arcs', fx.arcs),
		pcb_PrimitiveVia: mk('vias', fx.vias),
		pcb_PrimitivePour: mk('pours', fx.pours),
		pcb_PrimitiveFill: mk('fills', fx.fills),
		pcb_PrimitiveRegion: mk('regions', fx.regions),
		pcb_PrimitiveString: mk('strings', fx.strings),
		pcb_PrimitivePolyline: mk('polylines', fx.polylines),
	};
	return { deleted };
}

test('pcbPageClear: default clears silk (layer 3/4) + copper, keeps copper/doc strings and layer-11 outline', async () => {
	const { deleted } = installPcbClearStub({
		strings: [pcbPrim('s-top', 3), pcbPrim('s-bot', 4), pcbPrim('s-cu', 1), pcbPrim('s-doc', 12)],
		lines: [pcbPrim('trk', 1), pcbPrim('silkL', 3), pcbPrim('outL', 11)],
		components: [pcbPrim('U1', 1)],
	});
	await pcbPageClear({});
	// silk strings: ONLY layer 3/4 (copper/doc strings are artwork, preserved)
	assert.deepEqual((deleted.strings ?? []).sort(), ['s-bot', 's-top']);
	// lines: copper track + silk-layer line deleted; layer-11 outline preserved
	assert.deepEqual((deleted.lines ?? []).sort(), ['silkL', 'trk']);
	assert.ok(!(deleted.lines ?? []).includes('outL'), 'board outline must survive default clear');
	delete (globalThis as any).eda;
});

test('pcbPageClear: locked preserved by default, removed with includeLocked', async () => {
	let s = installPcbClearStub({ components: [pcbPrim('U1', 1, false), pcbPrim('U2', 1, true)] });
	const res: any = await pcbPageClear({});
	assert.deepEqual(s.deleted.components ?? [], ['U1']);
	assert.equal(res.result.skippedLockedTotal, 1);
	delete (globalThis as any).eda;

	s = installPcbClearStub({ components: [pcbPrim('U1', 1, false), pcbPrim('U2', 1, true)] });
	await pcbPageClear({ includeLocked: true });
	assert.deepEqual((s.deleted.components ?? []).sort(), ['U1', 'U2']);
	delete (globalThis as any).eda;
});

test('pcbPageClear: dryRun reports counts without calling any delete', async () => {
	const { deleted } = installPcbClearStub({ components: [pcbPrim('U1', 1)], pours: [pcbPrim('p1', 1)] });
	const res: any = await pcbPageClear({ dryRun: true });
	assert.equal(Object.keys(deleted).length, 0, 'dryRun must not delete');
	assert.equal(res.result.total, 2);
	assert.equal(res.result.deleted.components, 1);
	assert.equal(res.result.deleted.pours, 1);
	delete (globalThis as any).eda;
});

test('pcbPageClear: --only silk narrows to silkscreen artwork only', async () => {
	const { deleted } = installPcbClearStub({
		components: [pcbPrim('U1', 1)],
		lines: [pcbPrim('trk', 1), pcbPrim('silkL', 3)],
		strings: [pcbPrim('s', 4)],
	});
	await pcbPageClear({ only: 'silk' });
	assert.equal(deleted.components, undefined, 'components untouched under --only silk');
	assert.deepEqual(deleted.lines ?? [], ['silkL'], 'copper track must NOT be cleared by silk scope');
	assert.deepEqual(deleted.strings ?? [], ['s']);
	delete (globalThis as any).eda;
});

test('pcbPageClear: a delete returning false is surfaced (no false-clean report)', async () => {
	installPcbClearStub({ pours: [pcbPrim('p1', 1)], delResult: false });
	const res: any = await pcbPageClear({ only: 'copper' });
	assert.ok(res.result.failed?.includes('pours'), 'failed list must name the bucket');
	assert.ok((res.result.warnings ?? []).some((w: string) => w.includes('pours')), 'warning must mention the failed delete');
	delete (globalThis as any).eda;
});

test('pcbPageClear: --no-preserve-outline removes the locked board outline', async () => {
	const { deleted } = installPcbClearStub({ lines: [pcbPrim('outL', 11, true)] });
	await pcbPageClear({ preserveOutline: false });
	assert.deepEqual(deleted.lines ?? [], ['outL'], 'outline bypasses the lock guard under --no-preserve-outline');
	delete (globalThis as any).eda;
});
