/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';
import { silkSlotFacts, validSilkRect, type SilkRect } from './pcb-silk-placement';

const rect = (x: number, y: number, w: number, h: number): SilkRect => ({ minX: x, minY: y, maxX: x + w, maxY: y + h });

test('a negative clearance reward cannot clear a hard label collision', () => {
	const label = rect(50, 50, 40, 12);
	const facts = silkSlotFacts(label, 'c', 'a', 3, [], { other: { rect: rect(70, 55, 40, 12), layer: 3 } }, rect(0, 0, 200, 200), 6);
	assert.equal(facts.clean, false);
	assert.deepEqual(facts.conflicts, [{ kind: 'LABEL', owner: 'other' }]);
	assert.equal(facts.minClearance, 0);
	const oldCost = 10000 - 25; // the old reward threshold accepted this collision
	assert.equal(oldCost < 10000, true);
});

test('the owner rendered envelope is a hard obstacle even beyond its pad union', () => {
	const facts = silkSlotFacts(rect(70, 10, 15, 12), 'owner', 'attr', 3, [
		{ kind: 'BODY', owner: 'owner', rect: rect(0, 0, 100, 50), m: 6, layer: 3 },
		{ kind: 'PAD', owner: 'owner', rect: rect(5, 5, 10, 10), m: 0, layer: 3 },
	], {}, null, 6);
	assert.equal(facts.clean, false);
	assert.deepEqual(facts.conflicts, [{ kind: 'BODY', owner: 'owner' }]);
});

test('frozen live geometry regressions reject all three intersecting pairs and the owner overlap', () => {
	// De-identified measured rectangles from the dev.13 saved/reloaded failure.
	// This tests collision facts, not a replay of the complete SDK placement inventory.
	for (const [candidate, other] of [
		[rect(1776.402, 183.1355, 36.1, 45), rect(1765.254, 219.843, 72.3, 45)],
		[rect(2512.857, 1280.071, 52, 45), rect(2478.3, 1272.3045, 43.4, 45)],
		[rect(624, 1333.7155, 52, 45), rect(671.5985, 1364.924, 38.5, 45)],
	]) assert.equal(silkSlotFacts(candidate, 'owner', 'a', 3, [], { other: { rect: other, layer: 3 } }, null, 6).clean, false);
	assert.equal(silkSlotFacts(rect(744.232, 150.631, 47.1, 45), 'owner', 'a', 3, [
		{ kind: 'BODY', owner: 'owner', rect: rect(365.684, 20, 434.04, 315.611), m: 6 },
	], {}, null, 6).clean, false);
});

test('opposite-side silk is independent; through-layer obstacles and frozen text still block', () => {
	const r = rect(0, 0, 20, 12);
	assert.equal(silkSlotFacts(r, 'c', 'a', 3, [{ kind: 'BODY', owner: 'bottom', rect: r, m: 6, layer: 4 }], { b: { rect: r, layer: 4 } }, null, 6).clean, true);
	assert.equal(silkSlotFacts(r, 'c', 'a', 3, [{ kind: 'PAD', owner: 'through', rect: r, m: 0 }], {}, null, 6).clean, false);
	assert.equal(silkSlotFacts(r, 'c', 'a', 3, [{ kind: 'FROZEN', owner: 'legend', rect: r, m: 6, layer: 3 }], {}, null, 6).clean, false);
	for (const b of [null, { ...r, minX: NaN }, { ...r, maxX: Infinity }, { ...r, maxX: r.minX }]) assert.equal(validSilkRect(b), false);
});

function fixture(options: { missingPadBBox?: boolean; boxed?: boolean; ignorePosition?: boolean; undefinedWrite?: boolean; bottom?: boolean; ignoreMirror?: boolean; rotation?: number; changedRenderedBBox?: boolean } = {}) {
	const body = rect(-50, -25, 100, 50);
	const state = { x: 0, y: 0, layer: 3, mirror: false, reverse: false, rotation: options.rotation ?? 0 };
	const writes: Array<Record<string, unknown>> = [];
	const attr = {
		getState_PrimitiveId: () => 'a', getState_ParentPrimitiveId: () => 'c', getState_Key: () => 'Designator', getState_Value: () => 'J1',
		getState_KeyVisible: () => false, getState_ValueVisible: () => true,
		getState_X: () => state.x, getState_Y: () => state.y, getState_Layer: () => state.layer,
		getState_Mirror: () => state.mirror, getState_Reverse: () => state.reverse, getState_Rotation: () => state.rotation,
	};
	const component = { getState_PrimitiveId: () => 'c', getState_Designator: () => 'J1', getState_Layer: () => options.bottom ? 2 : 1 };
	const pad = { getState_PrimitiveId: () => 'pad', getState_Layer: () => 12 };
	const sdk = {
		pcb_PrimitiveComponent: { getAll: async () => [component], getAllPinsByPrimitiveId: async () => [pad] },
		pcb_PrimitiveAttribute: {
			getAll: async () => [attr], get: async () => attr,
			modify: async (_: string, update: Record<string, unknown>) => {
				writes.push({ ...update });
				const applied = { ...update };
				if (options.ignorePosition) { delete applied.x; delete applied.y; }
				if (options.ignoreMirror) delete applied.mirror;
				Object.assign(state, applied);
				return options.undefinedWrite ? undefined : attr;
			},
		},
		pcb_Primitive: { getPrimitivesBBox: async ([id]: string[]) => {
			if (id === 'outline') return options.boxed ? rect(-50, -25, 100, 50) : rect(-1000, -1000, 2000, 2000);
			if (id === 'c') return body;
			if (id === 'pad') return options.missingPadBBox ? undefined : rect(-5, -5, 10, 10);
			if (id === 'a') return options.changedRenderedBBox && writes.length > 0 ? rect(0, 0, 20, 12) : rect(state.x, state.y, state.rotation === 0 ? 20 : 12, state.rotation === 0 ? 12 : 20);
			throw new Error(`unknown geometry ${id}`);
		} },
		pcb_PrimitiveRegion: { getAll: async () => [] }, pcb_PrimitiveString: { getAll: async () => [] },
		pcb_PrimitiveLine: { getAll: async () => [{ getState_PrimitiveId: () => 'outline', getState_Layer: () => 11 }] },
		pcb_PrimitiveArc: { getAll: async () => [] }, pcb_PrimitivePolyline: { getAll: async () => [] },
	};
	return { sdk, state, writes, body };
}

async function align(f: ReturnType<typeof fixture>, payload: Record<string, unknown> = {}) {
	const globals = globalThis as any, previous = globals.eda;
	globals.eda = f.sdk;
	try { return (await runAction('pcb.silk.align', payload)).result!; }
	finally { globals.eda = previous; }
}

test('handler uses the full envelope and reports fresh collision-free geometry', async () => {
	const f = fixture(), r = await align(f);
	assert.equal(r.verified, true); assert.equal(r.partial, false); assert.equal(r.aligned, 1);
	assert.equal(f.writes.length, 1);
	const measured = (r.verification as any[])[0];
	assert.equal(measured.clean, true); assert.deepEqual(measured.conflicts, []);
	assert.equal(silkSlotFacts(measured.bbox, 'c', 'a', 3, [{ kind: 'BODY', owner: 'c', rect: f.body, m: 6 }], {}, null, 6).clean, true);
});

test('missing bbox and boxed-in searches return unverified without positional writes', async () => {
	for (const options of [{ missingPadBBox: true }, { boxed: true }]) {
		const f = fixture(options), r = await align(f);
		assert.equal(r.verified, false); assert.equal(r.partial, false); assert.equal(f.writes.length, 0);
		assert.ok(Number(r.skipped) + Number(r.unresolved) > 0);
	}
});

test('requested refs are required and cannot silently skip to success', async () => {
	const f = fixture(), r = await align(f, { refs: ['missing'] });
	assert.equal(r.verified, false); assert.equal(f.writes.length, 0); assert.ok(Number(r.skipped) > 0);
});

test('truthy modify cannot hide actual anchor mismatch; undefined return preserves attempted IDs', async () => {
	for (const options of [{ ignorePosition: true }, { undefinedWrite: true }]) {
		const f = fixture(options), r = await align(f);
		assert.equal(r.verified, false); assert.equal(r.partial, true); assert.equal(f.writes.length, 1);
		assert.deepEqual(r.appliedIds, ['a']); assert.ok(Number(r.skipped) > 0);
	}
});

test('actual rendered collision fails even when the SDK anchor and pose match the plan', async () => {
	const f = fixture({ changedRenderedBBox: true }), r = await align(f);
	assert.equal(r.verified, false); assert.equal(r.partial, true); assert.equal(f.writes.length, 1);
	assert.equal((r.verification as any[])[0].clean, false);
	assert.ok((r.verification as any[])[0].conflicts.some((c: any) => c.kind === 'BODY'));
	assert.equal((r.details as any[])[0].clean, false);
});

test('rotated text is normalized and freshly measured before planning', async () => {
	const f = fixture({ rotation: 90 }), r = await align(f);
	assert.equal(r.verified, true); assert.equal(f.writes.length, 2); assert.equal(f.state.rotation, 0);
	const b = (r.verification as any[])[0].bbox;
	assert.equal(b.maxX - b.minX, 20); assert.equal(b.maxY - b.minY, 12);
	assert.equal((r.normalization as any[])[0].ok, true);
});

test('bottom pose must read back exactly; no retry without mirror or layer', async () => {
	const f = fixture({ bottom: true, ignoreMirror: true }), r = await align(f);
	assert.equal(r.verified, false); assert.equal(r.partial, true); assert.equal(f.writes.length, 1);
	assert.equal(f.writes[0].mirror, true); assert.equal(f.writes[0].layer, 4); assert.equal(r.aligned, 0);
});

test('invalid spacing is refused before engineering writes', async () => {
	for (const spacing of [0, -1, Infinity]) {
		const f = fixture();
		await assert.rejects(() => align(f, { spacing }), /Invalid silk/);
		assert.equal(f.writes.length, 0);
	}
});

test('unknown obstacle inventories cannot be treated as empty space', async () => {
	for (const api of ['pcb_PrimitiveAttribute', 'pcb_PrimitiveRegion', 'pcb_PrimitiveString', 'pcb_PrimitiveLine', 'pcb_PrimitiveArc', 'pcb_PrimitivePolyline']) {
		const f = fixture(), sdk = f.sdk as any, original = sdk[api].getAll;
		sdk[api].getAll = async (parent?: string) => parent ? original(parent) : undefined;
		const r = await align(f);
		assert.equal(r.verified, false, api); assert.equal(r.partial, false); assert.equal(f.writes.length, 0);
	}
	const f = fixture(); (f.sdk as any).pcb_PrimitiveComponent.getAllPinsByPrimitiveId = async () => undefined;
	assert.equal((await align(f)).verified, false); assert.equal(f.writes.length, 0);
});

test('unreadable fresh pose getters cannot coerce into verified zero/false', async () => {
	for (const name of ['getState_X', 'getState_Y', 'getState_Rotation', 'getState_Layer', 'getState_Mirror', 'getState_Reverse']) {
		const f = fixture(), attr = await f.sdk.pcb_PrimitiveAttribute.get();
		const original = (attr as any)[name];
		(attr as any)[name] = () => f.writes.length > 0 ? undefined : original();
		const r = await align(f);
		assert.equal(r.verified, false, name); assert.equal(r.partial, true); assert.equal(f.writes.length, 1);
	}
});

test('fresh component inventory drift fails after one write and retains the attempted ID', async () => {
	const f = fixture(), sdk = f.sdk as any, original = sdk.pcb_PrimitiveComponent.getAll;
	sdk.pcb_PrimitiveComponent.getAll = async () => {
		const inventory = await original();
		if (f.writes.length > 0) inventory.push({ getState_PrimitiveId: () => 'new-c', getState_Designator: () => 'R2', getState_Layer: () => 1 });
		return inventory;
	};
	const r = await align(f);
	assert.equal(r.verified, false); assert.equal(r.partial, true); assert.equal(f.writes.length, 1);
	assert.deepEqual(r.appliedIds, ['a']); assert.match(JSON.stringify(r.skippedDetails), /inventory changed/);
});

test('malformed scope/options are refused before reading or writing the board', async () => {
	for (const payload of [{ refs: 'J1' }, { refs: {} }, { refs: null }, { refs: [] }, { refs: [1] }, { refs: [''] }, { spacing: '1.5' }, { offset: null }, { side: 3 }]) {
		const f = fixture(); await assert.rejects(() => align(f, payload)); assert.equal(f.writes.length, 0);
	}
});

test('initial parent/value mismatches are rejected before normalization can touch a wrong object', async () => {
	for (const field of ['getState_ParentPrimitiveId', 'getState_Value']) {
		const f = fixture({ rotation: 90 }), attr = await f.sdk.pcb_PrimitiveAttribute.get();
		(attr as any)[field] = () => 'wrong';
		const r = await align(f); assert.equal(r.verified, false); assert.equal(r.partial, false); assert.equal(f.writes.length, 0);
	}
});

test('fresh identity drift is refused before normalization and before positional writes', async () => {
	for (const rotation of [0, 90]) for (const field of ['getState_PrimitiveId', 'getState_ParentPrimitiveId', 'getState_Value']) {
		const f = fixture({ rotation }), original = f.sdk.pcb_PrimitiveAttribute.get;
		(f.sdk as any).pcb_PrimitiveAttribute.get = async () => ({ ...await original(), [field]: () => 'wrong' });
		const r = await align(f); assert.equal(r.verified, false); assert.equal(r.partial, false); assert.equal(f.writes.length, 0);
		assert.deepEqual(r.normalization, []); assert.deepEqual(r.appliedIds, []);
	}
});

test('selected visibility is rechecked after writing, including unknown and hidden states', async () => {
	for (const value of [undefined, null, false]) {
		const f = fixture(), attr = await f.sdk.pcb_PrimitiveAttribute.get();
		(attr as any).getState_ValueVisible = () => f.writes.length ? value : true;
		const r = await align(f); assert.equal(r.verified, false); assert.equal(r.partial, true); assert.equal(f.writes.length, 1);
	}
});

test('strict outline layers and region rule members cannot silently become harmless obstacles', async () => {
	const f = fixture(); (f.sdk as any).pcb_PrimitiveArc.getAll = async () => [{ getState_PrimitiveId: () => 'unknown-outline', getState_Layer: () => undefined }];
	assert.equal((await align(f)).verified, false); assert.equal(f.writes.length, 0);
	for (const rules of [undefined, ['2'], [999]]) {
		const f = fixture(); (f.sdk as any).pcb_PrimitiveRegion.getAll = async () => [{ getState_PrimitiveId: () => 'c', getState_Layer: () => 1, getState_RuleType: () => rules }];
		assert.equal((await align(f)).verified, false); assert.equal(f.writes.length, 0);
	}
});
