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

import { schematicComponentPlace, schematicPinSetNoConnect } from './actions';

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

// ─── schematic.pin.set_no_connect (live component pin lifecycle) ──────────

/**
 * Install a pin-state model where setState_NoConnected only stages a value and
 * done() persists it. Every getAllPins() call returns fresh handles, matching the
 * EasyEDA runtime behavior that exposed the missing-done regression.
 */
function installNoConnectStub(initial: Record<string, boolean>) {
	const stored = new Map(Object.entries(initial));
	const doneCalls: Array<{ pin: string; value: boolean }> = [];
	const getCalls: string[] = [];
	const componentId = 'u1-pid';

	const component = {
		getAllPins: async () => [...stored.entries()].map(([number, persisted]) => {
			let staged = persisted;
			const pin: any = {
				getState_PinNumber: () => number,
				getState_NoConnected: () => staged,
				setState_NoConnected: (value: boolean) => { staged = value; return pin; },
				done: async () => {
					stored.set(number, staged);
					doneCalls.push({ pin: number, value: staged });
					return pin;
				},
			};
			return pin;
		}),
	};

	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			getAll: async () => [{
				getState_Designator: () => 'U1',
				getState_PrimitiveId: () => componentId,
			}],
			get: async (id: string) => {
				getCalls.push(id);
				return id === componentId ? component : undefined;
			},
		},
	};
	return { stored, doneCalls, getCalls };
}

test('no-connect: commits every target pin with done() and verifies fresh instance state', async () => {
	const fx = installNoConnectStub({ '10': false, '11': false, '12': false });
	const res: any = await schematicPinSetNoConnect({ designator: 'U1', pins: ['10', 11] });

	assert.deepEqual(fx.doneCalls, [
		{ pin: '10', value: true },
		{ pin: '11', value: true },
	]);
	assert.deepEqual(fx.getCalls, ['u1-pid', 'u1-pid'], 'initial mutation + fresh verification use component.get');
	assert.equal(fx.stored.get('10'), true);
	assert.equal(fx.stored.get('11'), true);
	assert.equal(fx.stored.get('12'), false);
	assert.deepEqual(res.result.pins, [
		{ pin: '10', noConnected: true },
		{ pin: '11', noConnected: true },
	]);
	assert.deepEqual(res.result.notApplied, []);
	delete (globalThis as any).eda;
});

test('no-connect: noConnected=false clears and persists an existing X marker', async () => {
	const fx = installNoConnectStub({ '10': true });
	const res: any = await schematicPinSetNoConnect({ designator: 'U1', pins: ['10'], noConnected: false });

	assert.deepEqual(fx.doneCalls, [{ pin: '10', value: false }]);
	assert.equal(fx.stored.get('10'), false);
	assert.deepEqual(res.result.pins, [{ pin: '10', noConnected: false }]);
	assert.deepEqual(res.result.notApplied, []);
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

/**
 * Stub every pcb_Primitive* class pcbPageClear touches; record deleted ids per
 * class. A successful delete REMOVES the primitives from the class's live list —
 * the handler re-enumerates until a pass comes back empty (#112), so a stub whose
 * getAll never drained would just spin to the round cap. A rejected batch
 * (delResult:false) deliberately leaves them, as the real API does.
 */
function installPcbClearStub(fx: {
	components?: any[]; lines?: any[]; arcs?: any[]; vias?: any[];
	pours?: any[]; fills?: any[]; regions?: any[]; strings?: any[]; polylines?: any[];
	delResult?: boolean;
}): { deleted: Record<string, string[]> } {
	const deleted: Record<string, string[]> = {};
	const delResult = fx.delResult ?? true;
	const live: Record<string, any[]> = {};
	const mk = (key: string, items: any[] | undefined) => {
		live[key] = [...(items ?? [])];
		return {
			getAll: async () => [...live[key]],
			delete: async (ids: string[]) => {
				(deleted[key] ??= []).push(...ids);
				if (delResult) live[key] = live[key].filter(p => !ids.includes(p.getState_PrimitiveId()));
				return delResult;
			},
		};
	};
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

/** An all-empty eda stub with per-class overrides (for the round-loop tests). */
function pcbClearEdaStub(overrides: Record<string, any>): any {
	const classes = [
		'pcb_PrimitiveComponent', 'pcb_PrimitiveLine', 'pcb_PrimitiveArc', 'pcb_PrimitiveVia',
		'pcb_PrimitivePour', 'pcb_PrimitiveFill', 'pcb_PrimitiveRegion', 'pcb_PrimitiveString',
		'pcb_PrimitivePolyline',
	];
	const stub: any = {};
	for (const k of classes) stub[k] = { getAll: async () => [], delete: async () => true };
	return Object.assign(stub, overrides);
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

// ─── pcb.page.clear round loop (issue #112a) ─────────────────────────────
// One enumerate→delete pass is not enough on a real board: a 153-track clear
// reported 153 deleted, but a reload + --dry-run still found 8. The handler now
// re-enumerates until a pass comes back empty.

test('pcbPageClear: re-enumerates until clean — a stale first pass no longer leaves copper behind', async () => {
	// Round 1 sees 2 tracks; the engine index only reveals the 3rd once the batch
	// settles (this is the 153→8 leftover from the real board, in miniature).
	const passes: any[][] = [[pcbPrim('t1', 1), pcbPrim('t2', 1)], [pcbPrim('t3', 1)], []];
	const gone: string[] = [];
	let call = 0;
	(globalThis as any).eda = pcbClearEdaStub({
		pcb_PrimitiveLine: {
			getAll: async () => passes[Math.min(call++, passes.length - 1)],
			delete: async (ids: string[]) => { gone.push(...ids); return true; },
		},
	});
	const res: any = await pcbPageClear({ only: 'routing' });
	assert.deepEqual(gone, ['t1', 't2', 't3'], 'the leftover the first pass missed is cleared in the SAME call');
	assert.equal(res.result.deleted.tracks, 3);
	assert.equal(res.result.total, 3);
	assert.equal(res.result.rounds, 3, 'two delete rounds + the empty confirming pass');
	delete (globalThis as any).eda;
});

test('pcbPageClear: dryRun never loops — one enumeration pass only', async () => {
	let calls = 0;
	(globalThis as any).eda = pcbClearEdaStub({
		pcb_PrimitiveVia: {
			getAll: async () => { calls++; return [pcbPrim('v1')]; },
			delete: async () => { throw new Error('dryRun must not delete'); },
		},
	});
	const res: any = await pcbPageClear({ only: 'routing', dryRun: true });
	assert.equal(res.result.rounds, 1, 'dry-run reports a single enumeration, never retries');
	assert.equal(calls, 1);
	assert.equal(res.result.deleted.vias, 1);
	delete (globalThis as any).eda;
});

test('pcbPageClear: a class that never drains stops at the round cap and warns', async () => {
	let attempts = 0;
	(globalThis as any).eda = pcbClearEdaStub({
		pcb_PrimitiveVia: {
			getAll: async () => [pcbPrim('v1')],           // never drains
			delete: async () => { attempts++; return true; }, // yet claims success
		},
	});
	const res: any = await pcbPageClear({ only: 'routing' });
	assert.equal(res.result.rounds, 5, 'bounded by PCB_CLEAR_MAX_ROUNDS — no infinite loop');
	assert.equal(attempts, 5);
	assert.equal(res.result.deleted.vias, 1, 'a re-enumerated id is not counted once per round');
	assert.ok((res.result.warnings ?? []).some((w: string) => /did not converge/.test(w)),
		'non-convergence must be surfaced, not reported as a clean clear');
	delete (globalThis as any).eda;
});

test('pcbPageClear: a class whose delete is REJECTED is not hammered every round', async () => {
	let attempts = 0;
	(globalThis as any).eda = pcbClearEdaStub({
		pcb_PrimitiveVia: {
			getAll: async () => [pcbPrim('v1')],
			delete: async () => { attempts++; return false; }, // batch rejected
		},
	});
	const res: any = await pcbPageClear({ only: 'routing' });
	assert.equal(attempts, 1, 'a rejected batch is a reported condition, not a stale-enumeration retry');
	assert.deepEqual(res.result.failed, ['vias']);
	assert.equal((res.result.warnings ?? []).filter((w: string) => w.includes('vias')).length, 1,
		'the failure is reported once, not once per round');
	delete (globalThis as any).eda;
});
