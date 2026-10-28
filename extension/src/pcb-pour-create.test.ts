/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';

const points = [[0, 0], [20, 0], [20, 20], [0, 20]];
const source = [0, 0, 'L', 20, 0, 20, 20, 0, 20, 0, 0];
const expected = { PrimitiveId: 'created', Net: 'GND', Layer: 1, PourFillMethod: 'solid',
	PourName: 'GROUND', PourPriority: 2, LineWidth: 0.2, PrimitiveLock: false };
const arcSource: Array<string | number> = [0, 0, 'L', 100, 0, 'ARC', 90, 110, 10,
	'L', 110, 100, 0, 100, 0, 0];

function primitive(values: Record<string, unknown>, rebuild?: () => Promise<boolean>) {
	return new Proxy({}, { get: (_target, key) => key === 'rebuildCopperRegion' ? rebuild
		: String(key).startsWith('getState_') ? () => values[String(key).slice(9)] : undefined });
}

for (const [label, actual, verified] of [
	['same path', arcSource, true],
	['rotated start', [100, 0, 'ARC', 90, 110, 10, 'L', 110, 100, 0, 100, 0, 0, 100, 0], true],
	['reversed winding', [0, 0, 'L', 0, 100, 110, 100, 110, 10, 'ARC', -90, 100, 0, 'L', 0, 0], true],
	['flattened arc', [0, 0, 'L', 100, 0, 105, 3, 110, 10, 110, 100, 0, 100, 0, 0], false],
	['wrong sweep', [0, 0, 'L', 100, 0, 'ARC', -90, 110, 10, 'L', 110, 100, 0, 100, 0, 0], false],
	['shifted arc end', [0, 0, 'L', 100, 0, 'ARC', 90, 110.03, 10, 'L', 110, 100, 0, 100, 0, 0], false],
] as const) {
	test(`native ARC pour readback ${label}`, async t => {
		let creates = 0, rebuilds = 0;
		(globalThis as any).eda = {
			pcb_MathPolygon: { createPolygon: (source: unknown) => {
				assert.deepEqual(source, arcSource, 'constructor must receive the original native ARC path');
				return { getSource: () => source };
			} },
			pcb_PrimitivePour: {
				create: async () => { creates++; return primitive(expected); },
				getAll: async () => [primitive({ ...expected, ComplexPolygon: { getSource: () => actual } },
					async () => { rebuilds++; return true; })],
			},
		};
		t.after(() => { delete (globalThis as any).eda; });
		const { result }: any = await runAction('pcb.pour.create', { source: arcSource, net: 'GND', name: 'GROUND', lineWidth: 0.2 });
		assert.equal(creates, 1);
		assert.equal(result.verified, verified);
		assert.equal(rebuilds, verified ? 1 : 0);
		if (!verified) assert.deepEqual(result.differences, ['geometry']);
	});
}

for (const [label, payload] of [
	['both inputs', { points, source: arcSource }],
	['neither input', {}],
	['open path', { source: arcSource.slice(0, -2) }],
	['unsupported curve', { source: [0, 0, 'CARC', 90, 100, 0, 'L', 0, 0] }],
	['invalid sweep', { source: [0, 0, 'ARC', 0, 100, 0, 'L', 100, 100, 0, 0] }],
] as const) {
	test(`native ARC pour rejects ${label} before host write`, async t => {
		(globalThis as any).eda = {};
		t.after(() => { delete (globalThis as any).eda; });
		await assert.rejects(() => runAction('pcb.pour.create', { ...payload, net: 'GND' }),
			(error: any) => ['MISSING_PAYLOAD_FIELD', 'PRECONDITION_REFUSED'].includes(error.code));
	});
}

const circleSource: Array<string | number> = [0, 0, 'ARC', 180, 100, 0, 'ARC', 180, 0, 0];
const halfDiscSource: Array<string | number> = [0, 0, 'ARC', 180, 100, 0, 'L', 0, 0];

// [label, requested source, host readback source, verified]
const nativeReadbackCases: Array<[string, Array<string | number>, unknown, boolean]> = [
	['circle', circleSource, circleSource, true],
	['semicircle', halfDiscSource, halfDiscSource, true],
	['circle rotated start', circleSource, [100, 0, 'ARC', 180, 0, 0, 'ARC', 180, 100, 0], true],
	['circle reversed winding', circleSource, [0, 0, 'ARC', -180, 100, 0, 'ARC', -180, 0, 0], true],
	['nested contour readback', arcSource, [arcSource], true],
	['repeated closing vertex readback', arcSource, [...arcSource, 0, 0], true],
	['multi-contour readback', arcSource, [arcSource, arcSource], false],
	['circle altered sweep', circleSource, [0, 0, 'ARC', 180, 100, 0, 'ARC', -180, 0, 0], false],
	['circle shifted endpoint', circleSource, [0, 0, 'ARC', 180, 100.03, 0, 'ARC', 180, 0, 0], false],
];

for (const [label, requested, actual, verified] of nativeReadbackCases) {
	test(`native contour pour readback ${label}`, async t => {
		let creates = 0, rebuilds = 0;
		(globalThis as any).eda = {
			pcb_MathPolygon: { createPolygon: (source: unknown) => {
				assert.deepEqual(source, requested, 'constructor must receive the original native source');
				return { getSource: () => source };
			} },
			pcb_PrimitivePour: {
				create: async () => { creates++; return primitive(expected); },
				getAll: async () => [primitive({ ...expected, ComplexPolygon: { getSource: () => actual } },
					async () => { rebuilds++; return true; })],
			},
		};
		t.after(() => { delete (globalThis as any).eda; });
		const { result }: any = await runAction('pcb.pour.create', { source: requested, net: 'GND', name: 'GROUND', lineWidth: 0.2 });
		assert.equal(creates, 1);
		assert.equal(result.verified, verified);
		assert.equal(rebuilds, verified ? 1 : 0);
		if (!verified) assert.deepEqual(result.differences, ['geometry']);
	});
}

for (const [label, source] of [
	['internal zero-length edge', [0, 0, 'L', 100, 0, 100, 0, 100, 100, 0, 100, 0, 0]],
	['degenerate zero-area contour', [0, 0, 'L', 100, 0, 'L', 50, 0, 0, 0]],
	['nested request source', [[0, 0, 'L', 100, 0, 100, 100, 0, 100, 0, 0]]],
] as const) {
	test(`native contour pour rejects ${label} before host write`, async t => {
		(globalThis as any).eda = {};
		t.after(() => { delete (globalThis as any).eda; });
		await assert.rejects(() => runAction('pcb.pour.create', { source, net: 'GND' }),
			(error: any) => ['MISSING_PAYLOAD_FIELD', 'PRECONDITION_REFUSED'].includes(error.code));
	});
}

for (const field of ['LineWidth', 'Net', 'Layer', 'PourFillMethod', 'PourName', 'geometry'] as const) {
	test(`pour create rejects fresh ${field} mismatch despite matching create echo`, async t => {
		let creates = 0, rebuilds = 0, reads = 0;
		const values: Record<string, unknown> = { ...expected, ComplexPolygon: { getSource: () => source } };
		const changes = { LineWidth: 1, Net: 'OTHER', Layer: 2, PourFillMethod: '90grid', PourName: null,
			PourPriority: 3, geometry: { getSource: () => [0, 0, 'L', 2, 0, 2, 2, 0, 0] } };
		values[field === 'geometry' ? 'ComplexPolygon' : field] = changes[field];
		(globalThis as any).eda = {
			pcb_MathPolygon: { createPolygon: () => ({ getSource: () => source }) },
			pcb_PrimitivePour: {
				create: async (...args: unknown[]) => { creates++; assert.equal(args[7], 0.2); return primitive(expected); },
				getAll: async (net?: string) => { reads++; assert.equal(net, undefined); return [primitive(values, async () => { rebuilds++; return true; })]; },
			},
		};
		t.after(() => { delete (globalThis as any).eda; });
		const { result }: any = await runAction('pcb.pour.create', { points, net: 'GND', name: 'GROUND', lineWidth: 0.2 });
		assert.equal(creates, 1); assert.equal(reads, 1); assert.equal(rebuilds, 0);
		assert.equal(result.primitiveId, 'created'); assert.equal(result.verified, false); assert.equal(result.partial, true);
		assert.equal(result.rebuildAttempted, false); assert.equal(result.poured, false);
		assert.deepEqual(result.differences, [{ LineWidth: 'lineWidth', Net: 'net', Layer: 'layer', PourFillMethod: 'fill',
			PourName: 'pourName', PourPriority: 'priority', geometry: 'geometry' }[field]]);
		if (field === 'LineWidth') assert.equal(result.lineWidth, 1, 'report fresh width, not requested width');
	});
}

for (const mode of ['missing', 'duplicate', 'unavailable', 'throws', 'after-rebuild-unavailable'] as const) {
	test(`pour create preserves accountable partial ID when readback is ${mode}`, async t => {
		let creates = 0, reads = 0, rebuilds = 0;
		const fresh = primitive({ ...expected, ComplexPolygon: { getSource: () => source } }, async () => { rebuilds++; return true; });
		(globalThis as any).eda = {
			pcb_MathPolygon: { createPolygon: () => ({ getSource: () => source }) },
			pcb_PrimitivePour: {
				create: async () => { creates++; return primitive(expected); },
				getAll: async () => {
					reads++;
					if (mode === 'throws') throw new Error('read failed');
					if (mode === 'unavailable' || (mode === 'after-rebuild-unavailable' && reads === 2)) return undefined;
					if (mode === 'missing') return [];
					return mode === 'duplicate' ? [fresh, fresh] : [fresh];
				},
			},
		};
		t.after(() => { delete (globalThis as any).eda; });
		const { result, warnings }: any = await runAction('pcb.pour.create', { points, net: 'GND', lineWidth: 0.2 });
		assert.equal(creates, 1); assert.equal(result.primitiveId, 'created');
		assert.equal(result.verified, false); assert.equal(result.partial, true); assert.ok(result.error); assert.ok(warnings.length);
		assert.equal(rebuilds, mode === 'after-rebuild-unavailable' ? 1 : 0);
	});
}

for (const mode of ['filled', 'empty', 'rebuild-throws', 'changed-after-rebuild', 'equivalent-polygon', 'host-default-width'] as const) {
	test(`pour boundary verification distinguishes materialization: ${mode}`, async t => {
		let creates = 0, reads = 0, rebuilds = 0;
		const actualSource = mode === 'equivalent-polygon' ? [20, 20, 'L', 20, 0, 'L', 0, 0, 'L', 0, 20, 'L', 20, 20] : source;
		(globalThis as any).eda = {
			pcb_MathPolygon: { createPolygon: () => ({ getSource: () => source }) },
			pcb_PrimitivePour: {
				create: async () => { creates++; return primitive(expected, async () => { throw new Error('must rebuild fresh object'); }); },
				getAll: async () => {
					reads++;
					return [primitive({ ...expected, LineWidth: mode === 'host-default-width' || (mode === 'changed-after-rebuild' && reads === 2) ? 1 : 0.2,
						ComplexPolygon: { getSource: () => actualSource } }, async () => {
							rebuilds++;
							if (mode === 'rebuild-throws') throw new Error('no materialization');
							return mode !== 'empty';
						})];
				},
			},
		};
		t.after(() => { delete (globalThis as any).eda; });
		const { result }: any = await runAction('pcb.pour.create', { points, net: 'GND',
			...(mode === 'host-default-width' ? {} : { lineWidth: 0.2 }), name: 'GROUND' });
		assert.equal(creates, 1); assert.equal(reads, 2); assert.equal(rebuilds, 1);
		assert.equal(result.rebuildAttempted, true); assert.equal(result.verified, mode !== 'changed-after-rebuild');
		assert.equal(result.poured, !['empty', 'rebuild-throws'].includes(mode));
		if (mode === 'rebuild-throws') assert.match(result.rebuildError, /no materialization/);
		if (mode === 'changed-after-rebuild') { assert.equal(result.partial, true); assert.deepEqual(result.differences, ['lineWidth']); }
		else assert.notEqual(result.partial, true);
	});
}
