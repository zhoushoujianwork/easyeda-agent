/// <reference types="@jlceda/pro-api-types" />
// Official contract evidence: @jlceda/pro-api-types@0.4.25 index.d.ts declares
// IPCB_PrimitivePad.specialPad?, an optional constructor specialPad?, and
// getState_SpecialPad(): TPCB_PrimitiveSpecialPadShape | undefined. A getter
// that exists and completes with no optional value differs from an unreadable
// getter. The required base shape does not use this absence interpretation.
import assert from 'node:assert/strict';
import { beforeEach, test } from 'node:test';
import { runAction, serializePcbPad } from './actions';

beforeEach(t => {
	const globals = globalThis as any;
	const previous = globals.eda;
	(t as import('node:test').TestContext).after(() => { if (previous === undefined) delete globals.eda; else globals.eda = previous; });
});

function component(id = 'component-1'): any {
	return {
		getState_PrimitiveId: () => id, getState_UniqueId: () => 'unique-1',
		getState_Designator: () => 'U1', getState_Name: () => 'fixture',
		getState_Layer: () => 1, getState_X: () => 10, getState_Y: () => 20,
		getState_Rotation: () => 0, getState_PrimitiveLock: () => false,
		getState_AddIntoBom: () => true, getState_ManufacturerId: () => '', getState_SupplierId: () => '',
	};
}

function pad(overrides: Record<string, unknown> = {}): any {
	return {
		getState_PrimitiveId: () => 'pad-1', getState_PadNumber: () => '1',
		getState_Net: () => 'SIG', getState_Layer: () => 1,
		getState_X: () => 10, getState_Y: () => 20, getState_Rotation: () => 0,
		getState_PadType: () => 0, getState_Pad: () => ['RECT', 40, 10, 0],
		getState_SpecialPad: () => [], getState_Hole: () => null,
		getState_HoleOffsetX: () => 0, getState_HoleOffsetY: () => 0, getState_HoleRotation: () => 0, ...overrides,
	};
}

function host(padsGetter: (() => Promise<unknown>) | undefined, bboxGetter: (() => Promise<unknown>) | undefined): void {
	(globalThis as any).eda = {
		pcb_PrimitiveComponent: { getAll: async () => [component()], getAllPinsByPrimitiveId: padsGetter },
		pcb_Primitive: { getPrimitivesBBox: bboxGetter },
	};
}

test('PCB component capture labels unrequested fields without invoking their SDK getters', async () => {
	const fail = async () => { throw new Error('unrequested getter ran'); };
	host(fail, fail);
	const { result }: any = await runAction('pcb.components.list', {});
	assert.deepEqual(result.components[0].availability, { pads: 'not-requested', bbox: 'not-requested' });
	assert.deepEqual(result.components[0].captureErrors, {});
	assert.equal(result.components[0].pads, undefined);
	assert.equal(result.components[0].bbox, undefined);
	assert.equal(result.coverage.pads.notRequested, 1);
	assert.equal(result.coverage.bbox.notRequested, 1);
	assert.equal(result.partial, false);
	assert.equal(result.incomplete, false);
});

test('PCB component capture reports each thrown getter without changing unknown inventory to empty', async () => {
	host(async () => { throw new Error('pins SDK unavailable'); }, async () => { throw new Error('bbox SDK unavailable'); });
	const { result }: any = await runAction('pcb.components.list', { includePads: true, includeBBox: true });
	const got = result.components[0];
	assert.deepEqual(got.availability, { pads: 'unavailable', bbox: 'unavailable' });
	assert.equal(got.pads, undefined);
	assert.equal(got.bbox, undefined);
	assert.equal(got.captureErrors.pads.code, 'getter-threw');
	assert.match(got.captureErrors.pads.message, /pins SDK unavailable/);
	assert.equal(got.captureErrors.bbox.code, 'getter-threw');
	assert.match(got.captureErrors.bbox.message, /bbox SDK unavailable/);
	assert.equal(result.coverage.pads.unavailable, 1);
	assert.equal(result.coverage.bbox.unavailable, 1);
	assert.equal(result.partial, true);
	assert.equal(result.incomplete, true);
});

for (const [label, value, code] of [
	['undefined', undefined, 'undefined-result'], ['null', null, 'null-result'], ['malformed', {}, 'invalid-result'],
] as const) {
	test(`PCB component capture marks ${label} inventories and bboxes unavailable`, async () => {
		host(async () => value, async () => value);
		const { result }: any = await runAction('pcb.components.list', { includePads: true, includeBBox: true });
		const got = result.components[0];
		assert.deepEqual(got.availability, { pads: 'unavailable', bbox: 'unavailable' });
		assert.equal(got.pads, undefined);
		assert.equal(got.bbox, undefined);
		assert.equal(got.captureErrors.pads.code, code);
		assert.equal(got.captureErrors.bbox.code, code);
		assert.equal(result.partial, true);
	});
}

test('PCB component capture distinguishes missing SDK getters from returned empty inventory', async () => {
	host(undefined, undefined);
	const missing: any = (await runAction('pcb.components.list', { includePads: true, includeBBox: true })).result;
	assert.equal(missing.components[0].captureErrors.pads.code, 'missing-getter');
	assert.equal(missing.components[0].captureErrors.bbox.code, 'missing-getter');
	host(async () => [], async () => ({ minX: 0, minY: 0, maxX: 0, maxY: 0 }));
	const empty: any = (await runAction('pcb.components.list', { includePads: true, includeBBox: true })).result;
	assert.deepEqual(empty.components[0].availability, { pads: 'available', bbox: 'available' });
	assert.deepEqual(empty.components[0].pads, []);
	assert.deepEqual(empty.components[0].padFailures, []);
	assert.deepEqual(empty.components[0].captureErrors, {});
	assert.equal(empty.coverage.pads.available, 1);
	assert.equal(empty.coverage.pads.observed, 0);
	assert.equal(empty.partial, false);
});

test('PCB component capture retains good pads and failed identities in a mixed inventory', async () => {
	host(async () => [pad(), pad({ getState_PrimitiveId: () => 'pad-bad', getState_PadNumber: () => '2', getState_X: () => { throw new Error('unreadable x'); } }), pad({ getState_PrimitiveId: () => 'pad-3', getState_PadNumber: () => '3' })], undefined);
	const { result }: any = await runAction('pcb.components.list', { includePads: true });
	const got = result.components[0];
	assert.equal(got.availability.pads, 'unavailable');
	assert.deepEqual(got.pads.map((p: any) => p.primitiveId), ['pad-1', 'pad-3']);
	assert.deepEqual(got.padFailures, [{ index: 1, componentPrimitiveId: 'component-1', primitiveId: 'pad-bad', padNumber: '2', error: { code: 'pad-serialization-failed', message: 'unreadable x' } }]);
	assert.equal(got.captureErrors.pads.code, 'pad-serialization-failed');
	assert.equal(result.coverage.pads.observed, 3);
	assert.equal(result.coverage.pads.serialized, 2);
	assert.equal(result.coverage.pads.failed, 1);
	assert.equal(result.partial, true);
});

test('PCB component capture preserves a failed source slot when identity getters also fail', async () => {
	host(async () => [pad({ getState_PrimitiveId: () => { throw new Error('unreadable id'); }, getState_PadNumber: () => { throw new Error('unreadable number'); } })], undefined);
	const { result }: any = await runAction('pcb.components.list', { includePads: true });
	const got = result.components[0];
	assert.deepEqual(got.pads, []);
	assert.equal(got.availability.pads, 'unavailable', 'all failures are not known-empty');
	assert.deepEqual(got.padFailures[0], { index: 0, componentPrimitiveId: 'component-1', error: { code: 'pad-serialization-failed', message: 'unreadable id' } });
});

for (const field of ['shape', 'specialPad'] as const) {
	const getter = field === 'shape' ? 'getState_Pad' : 'getState_SpecialPad';
	const cases = [
		['missing getter', undefined, 'missing-getter'],
		['malformed', () => 'invalid', 'invalid-result'],
		['throwing getter', () => { throw new Error('geometry SDK failed'); }, 'getter-threw'],
		...(field === 'shape' ? [
			['undefined', () => undefined, 'undefined-result'],
			['null', () => null, 'null-result'],
		] as const : []),
	] as const;
	for (const [label, replacement, code] of cases) {
		test(`PCB pad ${field} ${label} stays unknown and cannot publish definite dimensions`, async () => {
			const input = pad({ [getter]: replacement });
			const got: any = serializePcbPad(input);
			assert.equal(got.availability[field], 'unavailable');
			assert.equal(got.captureErrors[field].code, code);
			assert.equal(got.width, undefined);
			assert.equal(got.height, undefined);
			if (field === 'specialPad') assert.equal(got.specialPadPresent, undefined, 'unreadable is not absent');
			host(async () => [input], undefined);
			const { result }: any = await runAction('pcb.components.list', { includePads: true });
			assert.equal(result.components[0].availability.pads, 'available', 'inventory availability is separate from geometry availability');
			assert.equal(result.coverage.pads.serialized, 1);
			assert.equal(result.coverage.pads.failed, 0);
			assert.equal(result.coverage.pads.geometryUnavailable, 1);
			assert.equal(result.partial, true);
		});
	}
}

for (const value of [undefined, null]) {
	test(`PCB pad optional specialPad returned ${String(value)} is absent after a successful getter`, async () => {
		const input = pad({ getState_SpecialPad: () => value });
		const got: any = serializePcbPad(input);
		assert.equal(got.specialPad, null);
		assert.equal(got.specialPadPresent, false);
		assert.equal(got.availability.specialPad, 'available');
		assert.equal(got.captureSources.specialPad, 'getState_SpecialPad');
		assert.deepEqual(got.captureErrors, {});
		assert.equal(got.width, 40);
		assert.equal(got.height, 10);
		host(async () => [input], undefined);
		const { result }: any = await runAction('pcb.components.list', { includePads: true });
		assert.equal(result.coverage.pads.geometryAvailable, 1);
		assert.equal(result.coverage.pads.geometryUnavailable, 0);
		assert.equal(result.partial, false);
	});
}

test('PCB pad capture preserves SDK ELLIPSE and POLYGON tuples without unit guesses', async () => {
	const ellipse = ['ELLIPSE', 2.5, 98.4];
	const polygon = ['POLYGON', [0, 0, 'L', 20, 0, 20, 10]];
	const ellipsePad = serializePcbPad(pad({ getState_Pad: () => ellipse }));
	assert.deepEqual(ellipsePad.shape, ellipse);
	assert.equal(ellipsePad.width, 2.5);
	assert.equal(ellipsePad.height, 98.4);
	const polygonPad = serializePcbPad(pad({ getState_Pad: () => polygon }));
	assert.deepEqual(polygonPad.shape, polygon);
	assert.equal(polygonPad.width, undefined);
	assert.equal(polygonPad.height, undefined);
	host(async () => [pad({ getState_Pad: () => ellipse }), pad({ getState_Pad: () => polygon })], undefined);
	const { result }: any = await runAction('pcb.components.list', { includePads: true });
	assert.equal(result.coverage.pads.geometryAvailable, 2);
	assert.equal(result.partial, false, 'unsupported extent derivation does not erase available raw geometry');
});

test('PCB pad capture keeps the raw drill tuple and plating; an unreadable drill stays unknown', () => {
	const drilled: any = serializePcbPad(pad({ getState_Layer: () => 12, getState_Hole: () => ['ROUND', 4.3308, 4.3308], getState_Metallization: () => true }));
	assert.deepEqual(drilled.hole, ['ROUND', 4.3308, 4.3308]);
	assert.equal(drilled.metallization, true);
	assert.equal(drilled.availability.hole, 'available');
	const unknown: any = serializePcbPad(pad({ getState_Hole: () => { throw new Error('no drill'); } }));
	assert.equal(unknown.availability.hole, 'unavailable');
	assert.equal(unknown.captureErrors.hole.code, 'getter-threw');
	assert.equal(unknown.metallization, undefined);
});

test('PCB pad capture records slot placement raw; a host without the getters reports it missing, not zero (#309)', () => {
	const slot: any = serializePcbPad(pad({
		getState_Layer: () => 12, getState_Hole: () => ['SLOT', 1, 3], getState_Metallization: () => false,
		getState_HoleOffsetX: () => 0.5, getState_HoleOffsetY: () => 0, getState_HoleRotation: () => 90,
	}));
	assert.deepEqual([slot.hole, slot.holeOffsetX, slot.holeOffsetY, slot.holeRotation], [['SLOT', 1, 3], 0.5, 0, 90]);
	assert.equal(slot.availability.holeRotation, 'available');
	assert.equal(slot.captureSources.holeRotation, 'getState_HoleRotation');
	const old: any = serializePcbPad(pad({ getState_HoleOffsetX: undefined, getState_HoleOffsetY: undefined, getState_HoleRotation: undefined }));
	assert.equal(old.holeOffsetX, null);
	assert.equal(old.availability.holeOffsetX, 'unavailable');
	assert.equal(old.captureErrors.holeRotation.code, 'missing-getter');
	assert.equal(old.availability.shape, 'available', 'placement gaps do not hide copper geometry');
});
