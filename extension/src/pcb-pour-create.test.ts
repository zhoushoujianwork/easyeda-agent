/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';

const points = [[0, 0], [20, 0], [20, 20], [0, 20]];
const source = [0, 0, 'L', 20, 0, 20, 20, 0, 20, 0, 0];
const expected = { PrimitiveId: 'created', Net: 'GND', Layer: 1, PourFillMethod: 'solid',
	PourName: 'GROUND', PourPriority: 2, LineWidth: 1, PrimitiveLock: false };

function primitive(values: Record<string, unknown>, rebuild?: () => Promise<boolean>) {
	return new Proxy({}, { get: (_target, key) => key === 'rebuildCopperRegion' ? rebuild
		: String(key).startsWith('getState_') ? () => values[String(key).slice(9)] : undefined });
}

for (const field of ['LineWidth', 'Net', 'Layer', 'PourFillMethod', 'PourName', 'PourPriority', 'geometry'] as const) {
	test(`pour create rejects fresh ${field} mismatch despite matching create echo`, async t => {
		let creates = 0, rebuilds = 0, reads = 0;
		const values: Record<string, unknown> = { ...expected, ComplexPolygon: { getSource: () => source } };
		const changes = { LineWidth: 0.2, Net: 'OTHER', Layer: 2, PourFillMethod: '90grid', PourName: null,
			PourPriority: 3, geometry: { getSource: () => [0, 0, 'L', 2, 0, 2, 2, 0, 0] } };
		values[field === 'geometry' ? 'ComplexPolygon' : field] = changes[field];
		(globalThis as any).eda = {
			pcb_MathPolygon: { createPolygon: () => ({ getSource: () => source }) },
			pcb_PrimitivePour: {
				create: async (...args: unknown[]) => { creates++; assert.equal(args[7], 1); return primitive(expected); },
				getAll: async (net?: string) => { reads++; assert.equal(net, undefined); return [primitive(values, async () => { rebuilds++; return true; })]; },
			},
		};
		t.after(() => { delete (globalThis as any).eda; });
		const { result }: any = await runAction('pcb.pour.create', { points, net: 'GND', name: 'GROUND', priority: 2, lineWidth: 1 });
		assert.equal(creates, 1); assert.equal(reads, 1); assert.equal(rebuilds, 0);
		assert.equal(result.primitiveId, 'created'); assert.equal(result.verified, false); assert.equal(result.partial, true);
		assert.equal(result.rebuildAttempted, false); assert.equal(result.poured, false);
		assert.deepEqual(result.differences, [{ LineWidth: 'lineWidth', Net: 'net', Layer: 'layer', PourFillMethod: 'fill',
			PourName: 'pourName', PourPriority: 'priority', geometry: 'geometry' }[field]]);
		if (field === 'LineWidth') assert.equal(result.lineWidth, 0.2, 'report fresh width, not requested width');
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
		const { result, warnings }: any = await runAction('pcb.pour.create', { points, net: 'GND', lineWidth: 1 });
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
					return [primitive({ ...expected, LineWidth: mode === 'host-default-width' || (mode === 'changed-after-rebuild' && reads === 2) ? 0.2 : 1,
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
			...(mode === 'host-default-width' ? {} : { lineWidth: 1 }), name: 'GROUND', priority: 2 });
		assert.equal(creates, 1); assert.equal(reads, 2); assert.equal(rebuilds, 1);
		assert.equal(result.rebuildAttempted, true); assert.equal(result.verified, mode !== 'changed-after-rebuild');
		assert.equal(result.poured, !['empty', 'rebuild-throws'].includes(mode));
		if (mode === 'rebuild-throws') assert.match(result.rebuildError, /no materialization/);
		if (mode === 'changed-after-rebuild') { assert.equal(result.partial, true); assert.deepEqual(result.differences, ['lineWidth']); }
		else assert.notEqual(result.partial, true);
	});
}
