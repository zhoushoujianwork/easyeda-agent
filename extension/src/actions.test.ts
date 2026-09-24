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
import JSZip from 'jszip';
import { sweepDeadlines } from './deadlines';

import {
	connectPinEndpoint,
	constraintList,
	detectPolarityConventionOutliers,
	getComponentOrThrow,
	importConfirmStepSource,
	isGroundLikeNet,
	isPowerRailNet,
	normalizeDeviceRef,
	pcbPadExtent,
	planOtherPropertyBackfill,
	polygonSourceToPoints,
	PROJECTED_STATE_KEYS,
	runAction,
	schematicComponentsList,
	selectBoardOutlineSources,
	serializeComponent,
	serializePcbPad,
	summarizeActivePageConnectivity,
} from './actions';

test('PCB pad serialization preserves source shape/rotation and computes shape-aware bbox extents', () => {
	const pad: any = {
		getState_PrimitiveId: () => 'p1',
		getState_PadNumber: () => '1',
		getState_Net: () => 'SIG',
		getState_Layer: () => 1,
		getState_X: () => 10,
		getState_Y: () => 20,
		getState_Rotation: () => 45,
		getState_PadType: () => 0,
		getState_Pad: () => ['RECT', 40, 10, 0],
		getState_SpecialPad: () => undefined,
	};
	const got: any = serializePcbPad(pad);
	assert.deepEqual(got.shape, ['RECT', 40, 10, 0]);
	assert.equal(got.rotation, 45);
	assert.equal(got.specialPad, null);
	assert.ok(Math.abs(got.width - 35.3553390593) < 1e-6);
	assert.ok(Math.abs(got.height - 35.3553390593) < 1e-6);

	assert.deepEqual(pcbPadExtent(['NGON', 30, 6], 17), { width: 30, height: 30 },
		'NGON side count must never be misread as pad height');
	assert.equal(pcbPadExtent(['POLYGON', [0, 0, 'L', 1, 1]], 0), null);
});

test('special pads retain raw geometry and do not publish a misleading base-shape bbox', () => {
	const special = [[1, 1, ['POLYGON', [0, 0, 'L', 20, 0, 20, 10]]]];
	const pad: any = {
		getState_PrimitiveId: () => 'p-special', getState_PadNumber: () => 'EP',
		getState_Net: () => 'GND', getState_Layer: () => 1,
		getState_X: () => 0, getState_Y: () => 0, getState_Rotation: () => 0,
		getState_PadType: () => 0, getState_Pad: () => ['RECT', 20, 20, 0],
		getState_SpecialPad: () => special,
	};
	const got: any = serializePcbPad(pad);
	assert.deepEqual(got.specialPad, special);
	assert.equal(got.width, undefined);
	assert.equal(got.height, undefined);
});

function libraryDocumentControl(uuid: string, libraryUuid: string, documentType: number, tabId: string): Record<string, unknown> {
	let current = { uuid: 'previous', parentLibraryUuid: 'previous-library', documentType: 0, tabId: 'previous-tab' };
	return {
		dmt_SelectControl: { getCurrentDocumentInfo: async () => current },
		dmt_EditorControl: {
			getSplitScreenIdByTabId: async () => 'split-1',
			openLibraryDocument: async () => {
				current = { uuid, parentLibraryUuid: libraryUuid, documentType, tabId };
				return tabId;
			},
			activateDocument: async () => true,
		},
	};
}

test('pcb.snapshot defaults to the public board-outline fit and labels the viewport capture honestly', async () => {
	let boardFits = 0;
	let allFits = 0;
	(globalThis as any).eda = {
		dmt_EditorControl: {
			zoomToAllPrimitives: async () => { allFits++; },
			getCurrentRenderedAreaImage: async () => new Blob(['pcb-board'], { type: 'image/png' }),
		},
		pcb_Document: {
			zoomToBoardOutline: async () => { boardFits++; return true; },
			startCalculatingRatline: async () => true,
		},
	};
	try {
		const res: any = await runAction('pcb.snapshot', {});
		assert.equal(boardFits, 1);
		assert.equal(allFits, 0);
		assert.equal(res.result.fitModeRequested, 'board');
		assert.equal(res.result.fitModeApplied, 'board');
		assert.equal(res.result.fitApi, 'eda.pcb_Document.zoomToBoardOutline');
		assert.equal(res.result.captureKind, 'board-fitted-viewport-png');
		assert.equal(res.result.objectLevelExport, false);
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.snapshot reports the public zoom-to-all fallback when board fit is unavailable', async () => {
	let allFits = 0;
	(globalThis as any).eda = {
		dmt_EditorControl: {
			zoomToAllPrimitives: async () => { allFits++; },
			getCurrentRenderedAreaImage: async () => new Blob(['pcb-all'], { type: 'image/png' }),
		},
		pcb_Document: { zoomToBoardOutline: async () => false },
	};
	try {
		const res: any = await runAction('pcb.snapshot', { fitMode: 'board' });
		assert.equal(allFits, 1);
		assert.equal(res.result.fitModeRequested, 'board');
		assert.equal(res.result.fitModeApplied, 'all');
		assert.equal(res.result.captureKind, 'all-primitives-fitted-viewport-png');
		assert.match(res.result.fitFallbackReason, /zoomToBoardOutline/);
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.snapshot rejects unknown fit modes before capture', async () => {
	let captures = 0;
	(globalThis as any).eda = {
		dmt_EditorControl: {
			getCurrentRenderedAreaImage: async () => { captures++; return new Blob(['never']); },
		},
	};
	try {
		await assert.rejects(
			() => runAction('pcb.snapshot', { fitMode: 'selected' }),
			(err: any) => err.code === 'PRECONDITION_REFUSED' && /board.*all.*none/.test(err.message),
		);
		assert.equal(captures, 0);
	}
	finally { delete (globalThis as any).eda; }
});

function pouredBoundary(id: string, net: string, layer: number): any {
	return {
		getState_PrimitiveId: () => id,
		getState_Net: () => net,
		getState_Layer: () => layer,
	};
}

function materializedPour(id: string, boundaryId: string, fills: any): any {
	return {
		getState_PrimitiveId: () => id,
		getState_PourPrimitiveId: () => boundaryId,
		getState_PourFills: () => fills,
	};
}

test('pcb.poured.list normalizes nested materialized fill coordinates/width to mil and keeps ARC sweep degrees', async () => {
	const source = [[0, 0, 'L', 100, 0, 'ARC', 90, 100, 100, 'L', 0, 100, 0, 0], [20, 20, 'L', 20, 40, 40, 40, 40, 20, 20, 20]];
	(globalThis as any).eda = {
		pcb_PrimitivePour: { getAll: async () => [pouredBoundary('pour-gnd', 'GND', 1)] },
		pcb_PrimitivePoured: { getAll: async () => [materializedPour('poured-1', 'pour-gnd', [{ id: 'island-1', lineWidth: 8, fill: true, path: { getSourceStrictComplex: () => source } }])] },
	};
	try {
		const res: any = await runAction('pcb.poured.list', {});
		assert.equal(res.result.available, true);
		assert.equal(res.result.count, 1);
		assert.equal(res.result.poured[0].net, 'GND');
		assert.equal(res.result.poured[0].layer, 1);
		const fill = res.result.poured[0].fills[0];
		assert.deepEqual(fill.source, [
			[0, 0, 'L', 1000, 0, 'ARC', 90, 1000, 1000, 'L', 0, 1000, 0, 0],
			[200, 200, 'L', 200, 400, 400, 400, 400, 200, 200, 200],
		]);
		assert.equal(fill.lineWidth, 80);
		assert.equal(fill.sourceUnits, 'mil');
		assert.equal(fill.lineWidthUnits, 'mil');
		assert.equal(fill.arcSweepUnits, 'degree');
		assert.equal(fill.nativeSourceUnits, '0.1mil');
		assert.equal(fill.nativeLineWidthUnits, '0.1mil');
		assert.equal(fill.geometryKind, 'filled-complex-polygon');
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.poured.list normalizes compact/CARC/Bezier geometry by numeric role', async () => {
	const source = [
		['R', 10, 20, 30, 40, 45, 5],
		['CIRCLE', 50, 60, 7],
		[1, 2, 'CARC', -90, 3, 4, 'C', 5, 6, 7, 8, 9, 10],
	];
	(globalThis as any).eda = {
		pcb_PrimitivePour: { getAll: async () => [pouredBoundary('pour-gnd', 'GND', 1)] },
		pcb_PrimitivePoured: { getAll: async () => [materializedPour('poured-1', 'pour-gnd', [{ id: 'shapes', lineWidth: 2, fill: true, path: { getSourceStrictComplex: () => source } }])] },
	};
	try {
		const res: any = await runAction('pcb.poured.list', {});
		assert.deepEqual(res.result.poured[0].fills[0].source, [
			['R', 100, 200, 300, 400, 45, 50],
			['CIRCLE', 500, 600, 70],
			[10, 20, 'CARC', -90, 30, 40, 'C', 50, 60, 70, 80, 90, 100],
		]);
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.poured.list preserves fill=false thermal spokes as stroked mil paths', async () => {
	const spoke = { id: 'thermal-1', lineWidth: 1, fill: false, path: { getSourceStrictComplex: () => [[10, 20, 'L', 30, 40]] } };
	(globalThis as any).eda = {
		pcb_PrimitivePour: { getAll: async () => [pouredBoundary('pour-gnd', 'GND', 1)] },
		pcb_PrimitivePoured: { getAll: async () => [materializedPour('poured-1', 'pour-gnd', [spoke])] },
	};
	try {
		const res: any = await runAction('pcb.poured.list', {});
		const fill = res.result.poured[0].fills[0];
		assert.equal(fill.fill, false);
		assert.equal(fill.geometryKind, 'stroked-thermal-spoke-path');
		assert.equal(fill.lineWidth, 10);
		assert.deepEqual(fill.source, [[100, 200, 'L', 300, 400]]);
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.poured.list preserves a successful empty result', async () => {
	(globalThis as any).eda = {
		pcb_PrimitivePour: { getAll: async () => [] },
		pcb_PrimitivePoured: { getAll: async () => [] },
	};
	try {
		const res: any = await runAction('pcb.poured.list', {});
		assert.deepEqual(res.result, { available: true, poured: [], count: 0 });
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.poured.list filters through the requested boundary net', async () => {
	const requested: Array<string | undefined> = [];
	const fill = { id: 'f', lineWidth: 8, fill: true, path: { getSourceStrictComplex: () => [[0, 0, 'L', 10, 0, 10, 10, 0, 0]] } };
	(globalThis as any).eda = {
		pcb_PrimitivePour: { getAll: async (net?: string) => { requested.push(net); return [pouredBoundary('pour-gnd', 'GND', 2), pouredBoundary('pour-vcc', '+3V3', 1)]; } },
		pcb_PrimitivePoured: { getAll: async () => [materializedPour('pg', 'pour-gnd', [fill]), materializedPour('pv', 'pour-vcc', [fill])] },
	};
	try {
		const res: any = await runAction('pcb.poured.list', { net: 'GND' });
		assert.deepEqual(requested, [undefined], 'complete boundary inventory must be read before local filtering');
		assert.equal(res.result.count, 1);
		assert.equal(res.result.poured[0].primitiveId, 'pg');
	}
	finally { delete (globalThis as any).eda; }
});

for (const [name, pouredInventory, boundaryInventory, message] of [
	['undefined materialized inventory', undefined, [], /materialized poured copper inventory is unavailable/i],
	['null materialized inventory', null, [], /materialized poured copper inventory is unavailable/i],
	['undefined boundary inventory', [], undefined, /pour boundary inventory is unavailable/i],
	['null boundary inventory', [], null, /pour boundary inventory is unavailable/i],
] as const) {
	test(`pcb.poured.list fails closed for ${name}`, async () => {
		(globalThis as any).eda = {
			pcb_PrimitivePour: { getAll: async () => boundaryInventory },
			pcb_PrimitivePoured: { getAll: async () => pouredInventory },
		};
		try {
			await assert.rejects(
				() => runAction('pcb.poured.list', {}),
				(err: any) => err.code === 'EDA_CALL_FAILED' && message.test(err.message),
			);
		}
		finally { delete (globalThis as any).eda; }
	});
}

test('pcb.poured.list distinguishes readable empty fill inventory from unavailable fills', async () => {
	(globalThis as any).eda = {
		pcb_PrimitivePour: { getAll: async () => [pouredBoundary('pour-gnd', 'GND', 1)] },
		pcb_PrimitivePoured: { getAll: async () => [materializedPour('known-empty-fills', 'pour-gnd', [])] },
	};
	try {
		const res: any = await runAction('pcb.poured.list', {});
		assert.deepEqual(res.result.poured[0].fills, []);
	}
	finally { delete (globalThis as any).eda; }

	(globalThis as any).eda = {
		pcb_PrimitivePour: { getAll: async () => [pouredBoundary('pour-gnd', 'GND', 1)] },
		pcb_PrimitivePoured: { getAll: async () => [materializedPour('unknown-fills', 'pour-gnd', undefined)] },
	};
	try {
		await assert.rejects(
			() => runAction('pcb.poured.list', {}),
			(err: any) => err.code === 'EDA_CALL_FAILED' && /fills are unavailable/.test(err.message),
		);
	}
	finally { delete (globalThis as any).eda; }
});

for (const netFilter of [undefined, 'GND']) {
	test(`pcb.poured.list rejects missing boundary attribution${netFilter ? ' with --net filter' : ''}`, async () => {
		const fill = { id: 'f', lineWidth: 8, fill: true, path: { getSourceStrictComplex: () => [[0, 0, 'L', 10, 0, 10, 10, 0, 0]] } };
		(globalThis as any).eda = {
			pcb_PrimitivePour: { getAll: async () => [pouredBoundary('pour-gnd', 'GND', 1)] },
			pcb_PrimitivePoured: { getAll: async () => [materializedPour('orphan', 'missing-boundary', [fill])] },
		};
		try {
			await assert.rejects(
				() => runAction('pcb.poured.list', netFilter ? { net: netFilter } : {}),
				(err: any) => err.code === 'EDA_CALL_FAILED' && /references missing boundary/.test(err.message),
			);
		}
		finally { delete (globalThis as any).eda; }
	});
}

for (const [name, boundary] of [
	['missing net', pouredBoundary('pour-bad', '', 1)],
	['missing layer', pouredBoundary('pour-bad', 'GND', Number.NaN)],
] as const) {
	test(`pcb.poured.list rejects boundary with ${name}`, async () => {
		(globalThis as any).eda = {
			pcb_PrimitivePour: { getAll: async () => [boundary] },
			pcb_PrimitivePoured: { getAll: async () => [] },
		};
		try {
			await assert.rejects(
				() => runAction('pcb.poured.list', {}),
				(err: any) => err.code === 'EDA_CALL_FAILED' && new RegExp(name.split(' ')[1], 'i').test(err.message),
			);
		}
		finally { delete (globalThis as any).eda; }
	});
}

test('pcb.poured.list fails closed when one fill polygon cannot be read', async () => {
	(globalThis as any).eda = {
		pcb_PrimitivePour: { getAll: async () => [pouredBoundary('pour-gnd', 'GND', 1)] },
		pcb_PrimitivePoured: { getAll: async () => [materializedPour('poured-1', 'pour-gnd', [{ id: 'bad', lineWidth: 8, fill: true, path: { getSourceStrictComplex: () => { throw new Error('polygon unavailable'); } } }])] },
	};
	try {
		await assert.rejects(
			() => runAction('pcb.poured.list', {}),
			(err: any) => err.code === 'EDA_CALL_FAILED' && /unreadable polygon geometry/.test(err.message),
		);
	}
	finally { delete (globalThis as any).eda; }
});

// ─── Board-outline ARC decoding (#215) ─────────────────────────────────

test('polygonSourceToPoints decodes signed ARC sweeps without taking the long way', () => {
	const lower = polygonSourceToPoints([-10, 0, 'ARC', 180, 10, 0, 'L', -10, 0]);
	assert.equal(lower.format, 'arc-polyline');
	assert.ok(lower.points && lower.points.length > 80);
	const lowerXs = lower.points!.map(([x]) => x);
	const lowerYs = lower.points!.map(([, y]) => y);
	assert.ok(Math.min(...lowerXs) >= -10 - 1e-9 && Math.max(...lowerXs) <= 10 + 1e-9, 'wrong center made the arc overshoot its diameter');
	assert.ok(Math.abs(Math.min(...lowerYs) + 10) < 1e-9 && Math.max(...lowerYs) <= 1e-9, 'positive 180° sweep should take the lower semicircle');

	const upper = polygonSourceToPoints([-10, 0, 'ARC', -180, 10, 0, 'L', -10, 0]);
	const upperYs = upper.points!.map(([, y]) => y);
	assert.ok(Math.abs(Math.max(...upperYs) - 10) < 1e-9 && Math.min(...upperYs) >= -1e-9, 'negative sweep must choose the opposite center/direction');
});

test('polygonSourceToPoints keeps unverified curve commands fail-closed', () => {
	const result = polygonSourceToPoints([0, 0, 'CARC', 90, 10, 10]);
	assert.equal(result.points, null);
	assert.equal(result.format, 'unsupported-command:CARC');
});

test('polygonSourceToPoints reconstructs a 129-ARC large circle within 0.01mm', () => {
	const radius = 1291;
	const arcCount = 129;
	const sweep = 360 / arcCount;
	const source: Array<string | number> = [radius, 0];
	for (let i = 1; i <= arcCount; i++) {
		const angle = 2 * Math.PI * i / arcCount;
		source.push('ARC', sweep, i === arcCount ? radius : radius * Math.cos(angle), i === arcCount ? 0 : radius * Math.sin(angle));
	}
	const result = polygonSourceToPoints(source);
	assert.equal(result.format, 'arc-polyline');
	assert.ok(result.points);
	const xs = result.points!.map(([x]) => x);
	const ys = result.points!.map(([, y]) => y);
	const diameterX = Math.max(...xs) - Math.min(...xs);
	const diameterY = Math.max(...ys) - Math.min(...ys);
	const maxErrorMil = 0.01 / 0.0254;
	assert.ok(Math.abs(diameterX - 2 * radius) < maxErrorMil, `x diameter error ${diameterX - 2 * radius}mil`);
	assert.ok(Math.abs(diameterY - 2 * radius) < maxErrorMil, `y diameter error ${diameterY - 2 * radius}mil`);
});

test('selectBoardOutlineSources chooses one containing outer ring and rejects disjoint rings', () => {
	const outer = [-20, 0, 'ARC', 180, 20, 0, 'ARC', 180, -20, 0];
	const inner = [-5, -5, 'L', 5, -5, 5, 5, -5, 5, -5, -5];
	const selected = selectBoardOutlineSources([inner, outer]);
	assert.ok(selected.points && selected.points.length > 100, JSON.stringify({ format: selected.format, points: selected.points?.length }));
	assert.equal(selected.format, 'arc-polyline;outer-of:2');

	const disjoint = [30, 30, 'L', 35, 30, 35, 35, 30, 35, 30, 30];
	assert.deepEqual(selectBoardOutlineSources([outer, disjoint]), {
		points: null,
		format: 'ambiguous:2-polylines',
	});

	const concaveOuter = [0, 0, 'L', 10, 0, 10, 10, 7, 10, 7, 3, 3, 3, 3, 10, 0, 10, 0, 0];
	const crossesNotch = [2, 8, 'L', 8, 8, 5, 1, 2, 8];
	assert.deepEqual(selectBoardOutlineSources([concaveOuter, crossesNotch]), {
		points: null,
		format: 'ambiguous:2-polylines',
	}, 'vertices inside a concave ring do not prove the candidate edges are contained');
});

test('pcb.outline.get returns the sampled containing ring instead of degrading every multi-polyline outline', async () => {
	const outer = [-20, 0, 'ARC', 180, 20, 0, 'ARC', 180, -20, 0];
	const inner = [-5, -5, 'L', 5, -5, 5, 5, -5, 5, -5, -5];
	const primitive = (id: string, source: Array<string | number>) => ({
		getState_PrimitiveId: () => id,
		getState_Polygon: () => ({ getSource: () => source }),
	});
	(globalThis as any).eda = {
		pcb_PrimitivePolyline: { getAll: async () => [primitive('inner', inner), primitive('outer', outer)] },
		pcb_PrimitiveLine: { getAll: async () => [] },
		pcb_PrimitiveArc: { getAll: async () => [] },
		pcb_Primitive: { getPrimitivesBBox: async () => ({ minX: -21, maxX: 21, minY: -21, maxY: 21 }) },
	};
	try {
		const res: any = await runAction('pcb.outline.get', {});
		assert.equal(res.result.outline, 2);
		assert.equal(res.result.outlineFormat, 'arc-polyline;outer-of:2');
		assert.ok(res.result.points.length > 100);
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.outline.set preserves native ARC tokens in one locked 10mil polyline', async () => {
	const source: Array<string | number> = [
		10, 0, 'L', 90, 0, 'ARC', 90, 100, 10,
		'L', 100, 70, 'ARC', 90, 90, 80,
		'L', 10, 80, 'ARC', 90, 0, 70,
		'L', 0, 10, 'ARC', 90, 10, 0,
	];
	let polygonSource: unknown;
	let createArgs: unknown[] = [];
	(globalThis as any).eda = {
		pcb_MathPolygon: { createPolygon: (src: unknown) => { polygonSource = src; return { src }; } },
		pcb_PrimitivePolyline: {
			getAll: async () => [],
			delete: async () => true,
			create: async (...args: unknown[]) => {
				createArgs = args;
				return {
					getState_PrimitiveId: () => 'outline-1',
					getState_LineWidth: () => args[3],
					getState_PrimitiveLock: () => args[4],
				};
			},
		},
		pcb_PrimitiveLine: { getAll: async () => [], delete: async () => true },
		pcb_PrimitiveArc: { getAll: async () => [], delete: async () => true },
		pcb_PrimitiveComponent: { getAll: async () => [] },
		pcb_Document: { zoomToBoardOutline: async () => true },
	};
	try {
		const res: any = await runAction('pcb.outline.set', { source, lineWidth: 10 });
		assert.deepEqual(polygonSource, source, 'createPolygon must receive the ARC source unchanged');
		assert.equal(createArgs[1], 11);
		assert.equal(createArgs[3], 10);
		assert.equal(createArgs[4], true);
		assert.equal(res.result.segments, 4);
		assert.equal(res.result.arcs, 4);
		assert.equal(res.result.width, 100);
		assert.equal(res.result.height, 80);
		assert.ok(Math.abs(res.result.radius - 10) < 1e-9);
		assert.equal(res.result.locked, true);
		assert.equal(res.result.outlineFormat, 'arc-polyline');
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.outline.set refuses to create a replacement when an old outline cannot be deleted', async () => {
	const source: Array<string | number> = [0, 0, 'L', 100, 0, 100, 80, 0, 80, 0, 0];
	let creates = 0;
	const old = { getState_PrimitiveId: () => 'locked-outline' };
	(globalThis as any).eda = {
		pcb_MathPolygon: { createPolygon: (src: unknown) => ({ src }) },
		pcb_PrimitivePolyline: {
			getAll: async () => [old],
			delete: async () => false,
			create: async () => { creates++; return null; },
		},
		pcb_PrimitiveLine: { getAll: async () => [], delete: async () => true },
		pcb_PrimitiveArc: { getAll: async () => [], delete: async () => true },
	};
	try {
		await assert.rejects(
			() => runAction('pcb.outline.set', { source, lineWidth: 10 }),
			(err: any) => /no replacement was created/.test(err.message),
		);
		assert.equal(creates, 0);
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.outline.set confirms deleted outline IDs are absent before replacement', async () => {
	const source: Array<string | number> = [0, 0, 'L', 100, 0, 100, 80, 0, 80, 0, 0];
	let creates = 0;
	const old = { getState_PrimitiveId: () => 'stale-outline' };
	(globalThis as any).eda = {
		pcb_MathPolygon: { createPolygon: (src: unknown) => ({ src }) },
		pcb_PrimitivePolyline: {
			getAll: async () => [old],
			delete: async () => true,
			create: async () => { creates++; return null; },
		},
		pcb_PrimitiveLine: { getAll: async () => [], delete: async () => true },
		pcb_PrimitiveArc: { getAll: async () => [], delete: async () => true },
	};
	try {
		await assert.rejects(
			() => runAction('pcb.outline.set', { source, lineWidth: 10 }),
			(err: any) => /remained after delete/.test(err.message),
		);
		assert.equal(creates, 0);
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.outline.get reports center-line dimensions separately from rendered bbox', async () => {
	const source: Array<string | number> = [
		10, 0, 'L', 90, 0, 'ARC', 90, 100, 10,
		'L', 100, 70, 'ARC', 90, 90, 80,
		'L', 10, 80, 'ARC', 90, 0, 70,
		'L', 0, 10, 'ARC', 90, 10, 0,
	];
	const primitive = {
		getState_PrimitiveId: () => 'outline-1',
		getState_Polygon: () => ({ getSource: () => source }),
		getState_LineWidth: () => 10,
		getState_PrimitiveLock: () => true,
	};
	(globalThis as any).eda = {
		pcb_PrimitivePolyline: { getAll: async () => [primitive] },
		pcb_PrimitiveLine: { getAll: async () => [] },
		pcb_PrimitiveArc: { getAll: async () => [] },
		pcb_Primitive: { getPrimitivesBBox: async () => ({ minX: -5, maxX: 105, minY: -5, maxY: 85 }) },
	};
	try {
		const res: any = await runAction('pcb.outline.get', {});
		assert.deepEqual(res.result.bbox, { minX: -5, maxX: 105, minY: -5, maxY: 85 });
		assert.deepEqual(res.result.centerlineBBox, { minX: 0, maxX: 100, minY: 0, maxY: 80 });
		assert.equal(res.result.width, 100);
		assert.equal(res.result.height, 80);
		assert.ok(Math.abs(res.result.radius - 10) < 1e-9);
		assert.equal(res.result.lineWidth, 10);
		assert.equal(res.result.locked, true);
		assert.equal(res.result.arcs, 0, 'legacy primitive arc count remains backward compatible');
		assert.equal(res.result.legacyArcs, 0);
		assert.equal(res.result.sourceArcs, 4);
		assert.equal(res.result.nativeArcs, 4);
		assert.equal(res.result.sourceSegments, 4);
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb.outline.get recognizes the ARC-first source persisted by EasyEDA', async () => {
	const source: Array<string | number> = [
		118.11, 0,
		'ARC', -90, 0, 118.11,
		'L', 0, 1850.39,
		'ARC', -90, 118.11, 1968.5,
		'L', 3425.2, 1968.5,
		'ARC', -90, 3543.31, 1850.39,
		'L', 3543.31, 118.11,
		'ARC', -90, 3425.2, 0,
		'L', 118.11, 0,
	];
	const primitive = {
		getState_PrimitiveId: () => 'persisted-outline',
		getState_Polygon: () => ({ getSource: () => source }),
		getState_LineWidth: () => 10,
		getState_PrimitiveLock: () => true,
	};
	(globalThis as any).eda = {
		pcb_PrimitivePolyline: { getAll: async () => [primitive] },
		pcb_PrimitiveLine: { getAll: async () => [] },
		pcb_PrimitiveArc: { getAll: async () => [] },
		pcb_Primitive: { getPrimitivesBBox: async () => ({ minX: -5, maxX: 3548.31, minY: -5, maxY: 1973.5 }) },
	};
	try {
		const res: any = await runAction('pcb.outline.get', {});
		assert.ok(Math.abs(res.result.radius - 118.11) < 1e-9);
		assert.equal(res.result.width, 3543.31);
		assert.equal(res.result.height, 1968.5);
		assert.equal(res.result.sourceSegments, 4);
		assert.equal(res.result.sourceArcs, 4);
		assert.equal(res.result.nativeArcs, 4);
	}
	finally { delete (globalThis as any).eda; }
});

test('pcb origin get/set wraps canvas-origin API and verifies readback without geometry mutation', async () => {
	let origin = { offsetX: 25, offsetY: -50 };
	let setArgs: number[] = [];
	(globalThis as any).eda = {
		pcb_Document: {
			getCanvasOrigin: async () => ({ ...origin }),
			setCanvasOrigin: async (x: number, y: number) => {
				setArgs = [x, y];
				origin = { offsetX: x, offsetY: y };
				return true;
			},
		},
	};
	try {
		const before: any = await runAction('pcb.origin.get', {});
		assert.equal(before.result.offsetX, 25);
		assert.equal(before.result.offsetY, -50);
		assert.equal(before.result.affectsGeometry, false);

		const after: any = await runAction('pcb.origin.set', { offsetX: 118.11, offsetY: 118.11 });
		assert.deepEqual(setArgs, [118.11, 118.11]);
		assert.deepEqual(after.result.previous, { offsetX: 25, offsetY: -50 });
		assert.equal(after.result.offsetX, 118.11);
		assert.equal(after.result.offsetY, 118.11);
		assert.equal(after.result.verified, true);
		assert.equal(after.result.affectsGeometry, false);
	}
	finally { delete (globalThis as any).eda; }
});

// ─── document.open: keep navigation on a known editor split ──────────────

test('exec_js compile rejection never runs even the valid prefix', async (t) => {
	const globals = globalThis as any;
	const previous = globals.eda;
	let writes = 0;
	globals.eda = { write: () => { writes++; } };
	t.after(() => { globals.eda = previous; });
	await assert.rejects(runAction('debug.exec_js', {
		code: 'eda.write(); return String(1)===\\"sheet\\";',
	}), (err: any) => err.code === 'PRECONDITION_REFUSED' && /no code was executed/.test(err.message));
	assert.equal(writes, 0);
});

test('exec_js runtime SyntaxError remains a possible partial write', async (t) => {
	const globals = globalThis as any;
	const previous = globals.eda;
	let writes = 0;
	globals.eda = { write: () => { writes++; } };
	t.after(() => { globals.eda = previous; });
	await assert.rejects(runAction('debug.exec_js', {
		code: 'eda.write(); throw new SyntaxError("runtime parse failed");',
	}), (err: any) => err.code === 'EDA_CALL_FAILED');
	assert.equal(writes, 1);
});

function installDocumentOpenStub(t: { after: (fn: () => void) => void }, options: {
	split?: string;
	splitFails?: boolean;
	beforeTabId?: string;
	afterUuid?: string;
	afterReadFails?: boolean;
} = {}) {
	const globals = globalThis as any;
	const previousEda = globals.eda;
	const previousTypes = globals.EDMT_EditorDocumentType;
	t.after(() => {
		if (previousEda === undefined) delete globals.eda;
		else globals.eda = previousEda;
		if (previousTypes === undefined) delete globals.EDMT_EditorDocumentType;
		else globals.EDMT_EditorDocumentType = previousTypes;
	});
	globals.EDMT_EditorDocumentType = { HOME: -1, BLANK: 0, SCHEMATIC_PAGE: 1, PCB: 3 };
	const opens: string[][] = [];
	const splitReads: string[] = [];
	let opened = false;
	globals.eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'project-1' }) },
		dmt_SelectControl: {
			getCurrentDocumentInfo: async () => {
				if (opened && options.afterReadFails) throw new Error('identity unavailable');
				return opened
					? { uuid: options.afterUuid ?? 'pcb-target', tabId: 'tab-target', documentType: 3 }
					: { uuid: 'old-page', tabId: options.beforeTabId ?? 'tab-old', documentType: 1 };
			},
		},
		dmt_EditorControl: {
			getSplitScreenIdByTabId: async (tabId: string) => {
				splitReads.push(tabId);
				if (options.splitFails) throw new Error('split metadata unavailable');
				return options.split;
			},
			openDocument: async (...args: string[]) => {
				opens.push(args);
				opened = true;
				return 'tab-target';
			},
		},
	};
	return { opens, splitReads };
}

test('document.open uses the active tab\'s official split ID without guessing', async (t) => {
	const fx = installDocumentOpenStub(t, { split: 'official-split-42' });
	const res: any = await runAction('document.open', { uuid: 'pcb-target' });
	assert.deepEqual(fx.splitReads, ['tab-old']);
	assert.deepEqual(fx.opens, [['pcb-target', 'official-split-42']]);
	assert.deepEqual(res.result, { tabId: 'tab-target', ready: true });
});

test('document.open uses a caller-preserved split without consulting a post-close blank tab', async (t) => {
	const fx = installDocumentOpenStub(t, { beforeTabId: 'about-blank-tab', split: 'wrong-post-close-split' });
	const res: any = await runAction('document.open', {
		uuid: 'pcb-target', splitScreenId: 'target-split-before-close',
	});
	assert.deepEqual(fx.splitReads, [], 'an explicit pre-close split is already authoritative');
	assert.deepEqual(fx.opens, [['pcb-target', 'target-split-before-close']]);
	assert.deepEqual(res.result, { tabId: 'tab-target', ready: true });
});

for (const [name, options] of Object.entries({
	'unavailable split API': { splitFails: true },
	'missing split ID': {},
	'blank split ID': { split: '  ' },
	'blank active tab ID': { beforeTabId: '', split: 'unusable-split' },
})) {
	test(`document.open preserves single-argument fallback for ${name}`, async (t) => {
		const fx = installDocumentOpenStub(t, options);
		const res: any = await runAction('document.open', { uuid: 'pcb-target' });
		assert.deepEqual(fx.opens, [['pcb-target']], 'fallback must not invent or pass an empty destination');
		assert.equal(res.result.ready, true);
		if (name === 'blank active tab ID') assert.deepEqual(fx.splitReads, []);
	});
}

test('document.open does not report ready when the SDK returns a tab but the old page remains active', async (t) => {
	const fx = installDocumentOpenStub(t, { split: 'official-split-42', afterUuid: 'old-page' });
	const res: any = await runAction('document.open', { uuid: 'pcb-target' });
	assert.equal(res.result.ready, false);
	assert.equal(fx.opens.length, 1, 'an identity mismatch must not trigger a second navigation');
});

test('document.open does not report ready when activation cannot be read back', async (t) => {
	installDocumentOpenStub(t, { split: 'official-split-42', afterReadFails: true });
	const res: any = await runAction('document.open', { uuid: 'pcb-target' });
	assert.equal(res.result.ready, false);
});

// ─── document.close: typed, identity-pinned reload primitive ─────────

test('system.page_reload schedules a top-level refresh only for the exact active document', async (t) => {
	const globals = globalThis as any;
	const previous = { eda: globals.eda, window: globals.window, Function: globals.Function, setTimeout: globals.setTimeout };
	t.after(() => {
		for (const [key, value] of Object.entries(previous)) {
			if (value === undefined) delete globals[key];
			else globals[key] = value;
		}
	});
	let reloads = 0;
	let scheduled: (() => void) | undefined;
	globals.window = { top: { location: { reload: () => { reloads++; } } } };
	globals.Function = undefined;
	globals.setTimeout = (callback: () => void, delay: number) => {
		assert.equal(delay, 500);
		scheduled = callback;
		return 1;
	};
	globals.eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'project-1' }) },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'doc-1' }) },
	};
	await assert.rejects(
		runAction('system.page_reload', { projectUuid: 'project-1', documentUuid: 'other-doc' }),
		(err: any) => err.code === 'PRECONDITION_REFUSED',
	);
	assert.equal(scheduled, undefined);
	const response: any = await runAction('system.page_reload', { projectUuid: 'project-1', documentUuid: 'doc-1' });
	assert.equal(response.result.scheduled, true);
	assert.equal(reloads, 0);
	assert.equal(typeof scheduled, 'function');
	(scheduled as unknown as () => void)();
	assert.equal(reloads, 1);
});

function installDocumentCloseStub(t: { after: (fn: () => void) => void }, options: {
	uuid?: string;
	tabId?: string;
	split?: string;
	splitFails?: boolean;
	closeResult?: boolean;
} = {}) {
	const globals = globalThis as any;
	const previous = globals.eda;
	t.after(() => {
		if (previous === undefined) delete globals.eda;
		else globals.eda = previous;
	});
	const splitReads: string[] = [];
	const closes: string[] = [];
	globals.eda = {
		dmt_SelectControl: {
			getCurrentDocumentInfo: async () => ({
				uuid: options.uuid ?? 'pcb-target',
				tabId: options.tabId ?? 'tab-target',
			}),
		},
		dmt_EditorControl: {
			getSplitScreenIdByTabId: async (tabId: string) => {
				splitReads.push(tabId);
				if (options.splitFails) throw new Error('split unavailable');
				return options.split ?? 'split-target';
			},
			closeDocument: async (tabId: string) => {
				closes.push(tabId);
				return options.closeResult ?? true;
			},
		},
	};
	return { splitReads, closes };
}

test('document.close verifies uuid and tabId, captures split, then closes through the official API', async (t) => {
	const fx = installDocumentCloseStub(t);
	const res: any = await runAction('document.close', { uuid: 'pcb-target', tabId: 'tab-target' });
	assert.deepEqual(fx.splitReads, ['tab-target']);
	assert.deepEqual(fx.closes, ['tab-target']);
	assert.deepEqual(res.result, {
		closed: true, uuid: 'pcb-target', tabId: 'tab-target', splitScreenId: 'split-target',
	});
});

for (const [name, options] of Object.entries({
	'uuid drift': { uuid: 'other-pcb' },
	'tab drift': { tabId: 'other-tab' },
})) {
	test(`document.close refuses ${name} before reading split or closing`, async (t) => {
		const fx = installDocumentCloseStub(t, options);
		await assert.rejects(
			runAction('document.close', { uuid: 'pcb-target', tabId: 'tab-target' }),
			(err: any) => err.code === 'INVALID_STATE' && /Refusing to close/.test(err.message),
		);
		assert.deepEqual(fx.splitReads, []);
		assert.deepEqual(fx.closes, []);
	});
}

test('document.close tolerates unavailable optional split metadata', async (t) => {
	const fx = installDocumentCloseStub(t, { splitFails: true });
	const res: any = await runAction('document.close', { uuid: 'pcb-target', tabId: 'tab-target' });
	assert.deepEqual(fx.closes, ['tab-target']);
	assert.equal(res.result.splitScreenId, null);
});

test('document.close rejects a false close result', async (t) => {
	const fx = installDocumentCloseStub(t, { closeResult: false });
	await assert.rejects(
		runAction('document.close', { uuid: 'pcb-target', tabId: 'tab-target' }),
		(err: any) => err.code === 'EDA_CALL_FAILED' && /returned no success/.test(err.message),
	);
	assert.deepEqual(fx.closes, ['tab-target']);
});

// ─── Library asset authoring: footprint + symbol + Device ────────────────

test('library footprint create defaults to personal library and verifies by get', async () => {
	const calls: Array<unknown> = [];
	(globalThis as any).eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ name: 'motor-box' }) },
		lib_LibrariesList: { getPersonalLibraryUuid: async () => 'LIB-PERSONAL' },
		lib_Footprint: {
			create: async (...args: unknown[]) => { calls.push(args); return 'FP-1'; },
			get: async (uuid: string, libraryUuid: string) => ({ uuid, libraryUuid, name: 'MY_FP' }),
		},
	};
	try {
		const res: any = await runAction('library.footprint.create', { name: 'MY_FP', description: 'test' });
		assert.deepEqual(calls, [['LIB-PERSONAL', 'EA_AGENT__MY_FP', undefined, 'test']]);
		assert.equal(res.result.name, 'EA_AGENT__MY_FP');
		assert.equal(res.result.namespace, 'EA_AGENT');
		assert.equal(res.result.uuid, 'FP-1');
		assert.equal(res.result.libraryUuid, 'LIB-PERSONAL');
		assert.equal(res.result.verified, true);
		assert.equal(res.result.footprint.name, 'MY_FP');
	}
	finally { delete (globalThis as any).eda; }
});

test('library footprint create reports partial when creation cannot be read back', async () => {
	(globalThis as any).eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ friendlyName: '测试 项目' }) },
		lib_LibrariesList: { getProjectLibraryUuid: async () => 'LIB-PROJECT' },
		lib_Footprint: { create: async () => 'FP-2', get: async () => undefined },
	};
	try {
		const res: any = await runAction('library.footprint.create', { name: 'ASYNC_FP', scope: 'project' });
		assert.equal(res.result.partial, true);
		assert.equal(res.result.verified, false);
		assert.equal(res.result.created.uuid, 'FP-2');
		assert.match(res.warnings[0], /do not retry blindly/);
	}
	finally { delete (globalThis as any).eda; }
});

test('library footprint copy namespaces the lossless copy and verifies it', async () => {
	let args: unknown[] = [];
	(globalThis as any).eda = {
		lib_LibrariesList: { getPersonalLibraryUuid: async () => 'LIB-PERSONAL' },
		lib_Footprint: {
			get: async (uuid: string) => ({ uuid, name: uuid === 'SRC' ? 'sdCard' : 'EA_AGENT__SDCARD_V2' }),
			copy: async (...callArgs: unknown[]) => { args = callArgs; return 'COPY'; },
		},
	};
	try {
		const res: any = await runAction('library.footprint.copy', {
			uuid: 'SRC', sourceLibraryUuid: 'LIB-SRC', name: 'sdCard v2',
		});
		assert.deepEqual(args, ['SRC', 'LIB-SRC', 'LIB-PERSONAL', undefined, 'EA_AGENT__SDCARD_V2']);
		assert.equal(res.result.uuid, 'COPY');
		assert.equal(res.result.verified, true);
	}
	finally { delete (globalThis as any).eda; }
});

test('library footprint build opens the asset, creates pads/lines and verifies IDs', async () => {
	const padIds: string[] = [];
	const lineIds: string[] = [];
	(globalThis as any).eda = {
		...libraryDocumentControl('FP-1', 'LIB-F', 4, 'TAB-FP'),
		pcb_PrimitivePad: {
			getAllPrimitiveId: async () => [],
			create: async (_layer: number, number: string) => {
				const id = `pad-${number}`; padIds.push(id);
				return { getState_PrimitiveId: () => id };
			},
			get: async (ids: string[]) => ids.map(id => ({ id })),
			delete: async () => true,
		},
		pcb_MathPolygon: {
			createPolygon: (source: unknown) => ({ source }),
		},
		pcb_PrimitivePolyline: {
			getAllPrimitiveId: async () => [],
			create: async () => {
				const id = `line-${lineIds.length + 1}`; lineIds.push(id);
				return { getState_PrimitiveId: () => id };
			},
			get: async (ids: string[]) => ids.map(id => ({ id })),
			delete: async () => true,
		},
		pcb_Document: { save: async () => true },
	};
	try {
		const res: any = await runAction('library.footprint.build', {
			uuid: 'FP-1', libraryUuid: 'LIB-F',
			pads: [
				{ number: '1', layer: 1, x: -40, y: 0, shape: ['RECT', 40, 50, 4] },
				{ number: '2', layer: 1, x: 40, y: 0, shape: ['RECT', 40, 50, 4] },
			],
			lines: [{ layer: 3, startX: -60, startY: 35, endX: 60, endY: 35, width: 6 }],
		});
		assert.deepEqual(res.result.created.pads, ['pad-1', 'pad-2']);
		assert.deepEqual(res.result.created.lines, ['line-1']);
		assert.equal(res.result.tabId, 'TAB-FP');
		assert.equal(res.result.verified, true);
	}
	finally { delete (globalThis as any).eda; }
});

test('library footprint build refuses a replay before creating duplicate geometry', async () => {
	const padIds: string[] = [];
	const lineIds: string[] = [];
	let saves = 0;
	(globalThis as any).eda = {
		...libraryDocumentControl('FP-1', 'LIB-F', 4, 'TAB-FP'),
		pcb_PrimitivePad: {
			getAllPrimitiveId: async () => [...padIds],
			create: async (_layer: number, number: string) => {
				const id = `pad-${number}-${padIds.length + 1}`; padIds.push(id);
				return { getState_PrimitiveId: () => id };
			},
			get: async (ids: string[]) => ids.map(id => ({ id })),
			delete: async () => true,
		},
		pcb_MathPolygon: { createPolygon: (source: unknown) => ({ source }) },
		pcb_PrimitivePolyline: {
			getAllPrimitiveId: async () => [...lineIds],
			create: async () => {
				const id = `line-${lineIds.length + 1}`; lineIds.push(id);
				return { getState_PrimitiveId: () => id };
			},
			get: async (ids: string[]) => ids.map(id => ({ id })),
			delete: async () => true,
		},
		pcb_Document: { save: async () => { saves++; return true; } },
	};
	const payload = {
		uuid: 'FP-1', libraryUuid: 'LIB-F',
		pads: [{ number: '1', layer: 1, x: 0, y: 0, shape: ['RECT', 40, 40, 0] }],
		lines: [{ layer: 3, startX: -20, startY: 20, endX: 20, endY: 20, width: 6 }],
	};
	try {
		await runAction('library.footprint.build', payload);
		await assert.rejects(
			() => runAction('library.footprint.build', payload),
			(err: any) => err.code === 'PRECONDITION_REFUSED' && /requires an empty target/.test(err.message),
		);
		assert.deepEqual(padIds, ['pad-1-1']);
		assert.deepEqual(lineIds, ['line-1']);
		assert.equal(saves, 1, 'refused replay must not save or create anything');
	}
	finally { delete (globalThis as any).eda; }
});

test('library symbol build refuses a non-empty target with zero new primitives', async () => {
	let creates = 0;
	let saves = 0;
	(globalThis as any).eda = {
		...libraryDocumentControl('SYM-1', 'LIB-S', 2, 'TAB-SYM'),
		sch_PrimitivePin: {
			getAllPrimitiveId: async () => ['pin-existing'],
			create: async () => { creates++; return { getState_PrimitiveId: () => 'pin-new' }; },
			delete: async () => true,
		},
		sch_PrimitivePolygon: {
			getAllPrimitiveId: async () => [],
			create: async () => { creates++; return { getState_PrimitiveId: () => 'outline-new' }; },
			delete: async () => true,
		},
		sch_PrimitiveCircle: {
			getAllPrimitiveId: async () => [],
			create: async () => { creates++; return { getState_PrimitiveId: () => 'circle-new' }; },
			delete: async () => true,
		},
		sch_Document: { save: async () => { saves++; return true; } },
	};
	try {
		await assert.rejects(
			() => runAction('library.symbol.build', {
				uuid: 'SYM-1', libraryUuid: 'LIB-S',
				outline: [-20, -20, 20, -20, 20, 20, -20, 20],
				pins: [{ number: '1', name: 'IN', x: -40, y: 0 }],
			}),
			(err: any) => err.code === 'PRECONDITION_REFUSED' && /pins=1/.test(err.message),
		);
		assert.equal(creates, 0);
		assert.equal(saves, 0);
	}
	finally { delete (globalThis as any).eda; }
});

test('library footprint build fails closed when target inventory cannot be read', async () => {
	let creates = 0;
	(globalThis as any).eda = {
		...libraryDocumentControl('FP-1', 'LIB-F', 4, 'TAB-FP'),
		pcb_PrimitivePad: {
			getAllPrimitiveId: async () => { throw new Error('inventory unavailable'); },
			create: async () => { creates++; return { getState_PrimitiveId: () => 'pad-new' }; },
		},
		pcb_PrimitivePolyline: {
			getAllPrimitiveId: async () => [],
			create: async () => { creates++; return { getState_PrimitiveId: () => 'line-new' }; },
		},
	};
	try {
		await assert.rejects(
			() => runAction('library.footprint.build', {
				uuid: 'FP-1', libraryUuid: 'LIB-F',
				pads: [{ number: '1', layer: 1, x: 0, y: 0, shape: ['RECT', 40, 40, 0] }],
			}),
			(err: any) => err.code === 'PRECONDITION_REFUSED' && /could not prove/.test(err.message),
		);
		assert.equal(creates, 0);
	}
	finally { delete (globalThis as any).eda; }
});

test('library footprint build rejects duplicate pad numbers before opening/mutating', async () => {
	let opened = false;
	(globalThis as any).eda = { lib_Footprint: { openInEditor: async () => { opened = true; } } };
	try {
		await assert.rejects(
			() => runAction('library.footprint.build', {
				uuid: 'FP-1', libraryUuid: 'LIB-F', pads: [
					{ number: '1', layer: 1, x: 0, y: 0, shape: ['RECT', 40, 40, 0] },
					{ number: '1', layer: 1, x: 50, y: 0, shape: ['RECT', 40, 40, 0] },
				],
			}),
			(err: any) => err.code === 'PRECONDITION_REFUSED' && /Duplicate pad/.test(err.message),
		);
		assert.equal(opened, false);
	}
	finally { delete (globalThis as any).eda; }
});

test('library Device create binds explicit symbol and footprint refs', async () => {
	let createArgs: Array<unknown> = [];
	(globalThis as any).eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ name: 'motor-box' }) },
		lib_Device: {
			create: async (...args: unknown[]) => { createArgs = args; return 'DEV-1'; },
			get: async () => ({ uuid: 'DEV-1', association: { symbol: { uuid: 'SYM-1' }, footprint: { uuid: 'FP-1' } } }),
		},
	};
	try {
		const res: any = await runAction('library.device.create', {
			name: 'MY_DEVICE', libraryUuid: 'LIB-D',
			symbol: { uuid: 'SYM-1', libraryUuid: 'LIB-S' },
			footprint: { uuid: 'FP-1', libraryUuid: 'LIB-F' },
			property: { designator: 'U', addIntoBom: true, addIntoPcb: true },
		});
		assert.equal(createArgs[0], 'LIB-D');
		assert.equal(createArgs[1], 'EA_AGENT__MY_DEVICE');
		assert.deepEqual(createArgs[3], {
			symbol: { uuid: 'SYM-1', libraryUuid: 'LIB-S' },
			footprint: { uuid: 'FP-1', libraryUuid: 'LIB-F' },
		});
		assert.deepEqual(createArgs[5], { designator: 'U', addIntoBom: true, addIntoPcb: true });
		assert.equal(res.result.name, 'EA_AGENT__MY_DEVICE');
		assert.equal(res.result.verified, true);
	}
	finally { delete (globalThis as any).eda; }
});

test('library Device create refuses a malformed symbol ref before mutation', async () => {
	let mutated = false;
	(globalThis as any).eda = { lib_Device: { create: async () => { mutated = true; } } };
	try {
		await assert.rejects(
			() => runAction('library.device.create', { name: 'BAD', symbol: { uuid: 'SYM' } }),
			(err: any) => err.code === 'PRECONDITION_REFUSED',
		);
		assert.equal(mutated, false);
	}
	finally { delete (globalThis as any).eda; }
});

test('V4 plural device variants fail closed before create or placement mutation', async () => {
	let deviceCreates = 0;
	let placements = 0;
	(globalThis as any).eda = {
		lib_Device: {
			create: async () => { deviceCreates++; return 'DEV-NEW'; },
			get: async () => ({
				uuid: 'DEV-V4',
				association: { footprints: [{ uuid: 'FP-A' }, { uuid: 'FP-B' }] },
			}),
		},
		sch_PrimitiveComponent: {
			create: async () => { placements++; return mockComponent(); },
		},
	};
	try {
		await assert.rejects(
			() => runAction('library.device.create', {
				name: 'MULTI', libraryUuid: 'LIB-D',
				symbol: { uuid: 'SYM-1', libraryUuid: 'LIB-S' },
				footprints: [{ uuid: 'FP-A', libraryUuid: 'LIB-F' }, { uuid: 'FP-B', libraryUuid: 'LIB-F' }],
			}),
			(err: any) => err.code === 'PRECONDITION_REFUSED' && /multi-variant/.test(err.message),
		);
		await assert.rejects(
			() => runAction('schematic.component.place', {
				libraryUuid: 'LIB-D', uuid: 'DEV-V4', x: 100, y: 200,
			}),
			(err: any) => err.code === 'PRECONDITION_REFUSED' && /multi-variant/.test(err.message),
		);
		assert.equal(deviceCreates, 0);
		assert.equal(placements, 0);
	}
	finally { delete (globalThis as any).eda; }
});

test('library Device delete requires exact expected name and verifies absence', async () => {
	let live: any = { uuid: 'DEV-1', name: 'EA_AGENT__TEST' };
	let deleteCalls = 0;
	(globalThis as any).eda = {
		lib_Device: {
			get: async () => live,
			delete: async () => { deleteCalls++; live = undefined; return true; },
		},
	};
	try {
		await assert.rejects(
			() => runAction('library.device.delete', { uuid: 'DEV-1', libraryUuid: 'LIB', expectedName: 'USER_PART' }),
			(err: any) => err.code === 'PRECONDITION_REFUSED' && /name mismatch/.test(err.message),
		);
		assert.equal(deleteCalls, 0);
		const res: any = await runAction('library.device.delete', {
			uuid: 'DEV-1', libraryUuid: 'LIB', expectedName: 'EA_AGENT__TEST',
		});
		assert.equal(res.result.deleted, true);
		assert.equal(res.result.verified, true);
		assert.equal(deleteCalls, 1);
	}
	finally { delete (globalThis as any).eda; }
});

test('connect_pin endpoint contract is y-UP and matches Go autoconnect', () => {
	assert.deepEqual(connectPinEndpoint(100, 100, 30, 'up'), { x: 100, y: 130 });
	assert.deepEqual(connectPinEndpoint(100, 100, 30, 'down'), { x: 100, y: 70 });
	assert.deepEqual(connectPinEndpoint(100, 100, 30, 'left'), { x: 70, y: 100 });
	assert.deepEqual(connectPinEndpoint(100, 100, 30, 'right'), { x: 130, y: 100 });
	// Both implementations score/place the snapped coordinate, not the raw 18-unit end.
	assert.deepEqual(connectPinEndpoint(545, 290, 18, 'up'), { x: 545, y: 310 });
	assert.deepEqual(connectPinEndpoint(545, 290, 18, 'down'), { x: 545, y: 270 });
});

test('connect_pin net_label creates only its stub and native attribute without rotation calibration', async (t) => {
	const globals = globalThis as any;
	const previousEda = globals.eda;
	t.after(() => {
		if (previousEda === undefined) delete globals.eda;
		else globals.eda = previousEda;
	});
	const calls: Array<unknown[]> = [];
	globals.eda = {
		sch_PrimitiveComponent: {
			createNetFlag: async (...args: unknown[]) => {
				calls.push(['unexpected rotation probe', ...args]);
				throw new Error('native labels must not depend on power-flag creation');
			},
			getAll: async () => { calls.push(['unexpected calibration read']); return []; },
			delete: async () => { calls.push(['unexpected calibration cleanup']); return true; },
		},
		sch_PrimitiveWire: {
			create: async (...args: unknown[]) => {
				calls.push(['wire', ...args]);
				return { getState_PrimitiveId: () => 'stub-1' };
			},
		},
		sch_PrimitiveAttribute: {
			createNetLabel: async (...args: unknown[]) => {
				calls.push(['label', ...args]);
				return { getState_PrimitiveId: () => 'label-1' };
			},
		},
	};

	const result: any = await runAction('schematic.power.connect_pin', {
		kind: 'net_label', net: 'ISSUE191_SIGNAL', pinX: 545, pinY: 290,
		direction: 'up', offset: 18, rotation: 90,
	});
	assert.deepEqual(calls, [
		['wire', [545, 290, 545, 310]],
		['label', 545, 310, 'ISSUE191_SIGNAL'],
	], 'label coordinates follow the snapped stub endpoint; rotation is not an SDK argument');
	assert.equal(result.result.wirePrimitiveId, 'stub-1');
	assert.equal(result.result.flagPrimitiveId, 'label-1');
	assert.deepEqual(result.result.endPoint, { x: 545, y: 310 });
});

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

test('summarizeActivePageConnectivity: counts only connectivity primitives', () => {
	assert.deepEqual(
		summarizeActivePageConnectivity(
			['part', 'netflag', 'netflag', 'netport', 'netlabel', 'sheet', 'short_symbol'],
			[{}, {}, {}],
			[{}],
		),
		{
			scope: 'activePage',
			wires: 3,
			buses: 1,
			netflags: 2,
			netports: 1,
			netlabels: 1,
			shortSymbols: 1,
		},
	);
});

test('components.list: connectivitySummary stays scoped to active page with allPages=true', async () => {
	const activeComponents = [
		mockComponent({ PrimitiveId: 'active-part', ComponentType: 'part' }),
		mockComponent({ PrimitiveId: 'active-flag', ComponentType: 'netflag' }),
		mockComponent({ PrimitiveId: 'active-port', ComponentType: 'netport' }),
	];
	const allPageComponents = [
		...activeComponents,
		mockComponent({ PrimitiveId: 'other-label', ComponentType: 'netlabel' }),
		mockComponent({ PrimitiveId: 'other-flag', ComponentType: 'netflag' }),
	];
	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			getAll: async (_filter?: unknown, allPages?: boolean) => (
				allPages ? allPageComponents : activeComponents
			),
		},
		sch_PrimitiveWire: { getAll: async () => [{}, {}] },
		sch_PrimitiveBus: { getAll: async () => [{}] },
	};
	try {
		const res: any = await schematicComponentsList({
			allPages: true,
			includeConnectivitySummary: true,
		});
		assert.equal(res.result.count, 5);
		assert.deepEqual(res.result.connectivitySummary, {
			scope: 'activePage',
			wires: 2,
			buses: 1,
			netflags: 1,
			netports: 1,
			netlabels: 0,
			shortSymbols: 0,
		});
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('components.list: complete page inventory loads unvisited pages and rejects incomplete evidence', async (t) => {
	for (const failure of ['none', 'enumerate', 'open', 'read', 'restore']) {
		await t.test(failure, async () => {
			let active = 'page-1';
			let visitedOther = false;
			let globalReads = 0;
			let restores = 0;
			const loaded = new Set(['page-1']);
			const parts = new Map([
				['page-1', mockComponent({ PrimitiveId: 'p1', ComponentType: 'part', Designator: 'U1' })],
				['page-2', mockComponent({ PrimitiveId: 'p2', ComponentType: 'part', Designator: 'U2' })],
			]);
			(globalThis as any).eda = {
				dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: active }) },
				dmt_Schematic: { getAllSchematicPagesInfo: async () => {
					if (failure === 'enumerate') throw new Error('cannot enumerate');
					return [{ uuid: 'page-1', name: 'P1' }, { uuid: 'page-2', name: 'P2' }];
				} },
				dmt_EditorControl: { openDocument: async (uuid: string) => {
					if (uuid === 'page-2') {
						visitedOther = true;
						if (failure === 'open') throw new Error('cannot open page-2');
					}
					if (uuid === 'page-1' && visitedOther) {
						restores++;
						if (failure === 'restore') throw new Error('cannot restore page-1');
					}
					active = uuid;
					loaded.add(uuid);
				} },
				sch_PrimitiveComponent: { getAll: async (_filter?: unknown, allPages?: boolean) => {
					if (allPages) {
						globalReads++;
						return [...loaded].map(id => parts.get(id));
					}
					if (active === 'page-2' && failure === 'read') return undefined;
					return [parts.get(active)];
				} },
			};
			try {
				if (failure === 'none') {
					const res: any = await schematicComponentsList({ allPages: true, tagPages: true });
					assert.deepEqual(res.result.components.map((c: any) => [c.designator, c.pageUuid]), [['U1', 'page-1'], ['U2', 'page-2']]);
					assert.equal(globalReads, 1);
				}
				else {
					await assert.rejects(() => schematicComponentsList({ allPages: true, tagPages: true }), (err: any) => {
						assert.equal(err.code, 'EDA_CALL_FAILED');
						assert.match(err.message, /Full-project component inventory is incomplete/);
						return true;
					});
					assert.equal(globalReads, 0, 'must not serialize a partial inventory as successful');
				}
				if (visitedOther) assert.equal(restores, 1, 'always attempt to restore the original page');
				if (failure !== 'restore') assert.equal(active, 'page-1');
			}
			finally { delete (globalThis as any).eda; }
		});
	}
});

test('components.list: includePins distinguishes empty success, unavailable data, and failure', async () => {
	const components = [
		mockComponent({ PrimitiveId: 'pins-empty', ComponentType: 'part', Designator: 'U1' }),
		mockComponent({ PrimitiveId: 'pins-missing', ComponentType: 'part', Designator: 'U2' }),
		mockComponent({ PrimitiveId: 'pins-failed', ComponentType: 'part', Designator: 'U3' }),
	];
	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			getAll: async () => components,
			getAllPinsByPrimitiveId: async (primitiveId: string) => {
				if (primitiveId === 'pins-empty') return [];
				if (primitiveId === 'pins-missing') return undefined;
				throw new Error('pin channel unavailable');
			},
		},
		sch_ManufactureData: { getNetlistFile: async () => undefined },
	};
	try {
		const res: any = await schematicComponentsList({ includePins: true });
		const byId = new Map<string, Record<string, unknown>>(
			res.result.components.map((component: Record<string, unknown>) => [
				String(component.primitiveId),
				component,
			]),
		);

		assert.equal(byId.get('pins-empty')?.pinsAvailable, true);
		assert.deepEqual(byId.get('pins-empty')?.pins, []);
		assert.equal('pinsError' in (byId.get('pins-empty') ?? {}), false);

		assert.equal(byId.get('pins-missing')?.pinsAvailable, false);
		assert.equal(byId.get('pins-missing')?.pinsError, 'Pin API did not return an array.');
		assert.equal('pins' in (byId.get('pins-missing') ?? {}), false);

		assert.equal(byId.get('pins-failed')?.pinsAvailable, false);
		assert.equal(byId.get('pins-failed')?.pinsError, 'pin channel unavailable');
		assert.equal('pins' in (byId.get('pins-failed') ?? {}), false);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('components.list: V4 pin otherProperty is preserved in the snapshot', async () => {
	const pin = {
		getState_PrimitiveId: () => 'pin-1',
		getState_PinNumber: () => '1',
		getState_PinName: () => 'VCC',
		getState_X: () => 100,
		getState_Y: () => 200,
		getState_Rotation: () => 0,
		getState_NoConnected: () => false,
		getState_OtherProperty: () => ({ NameVisible: true, NameFontSize: 9, Alias: 'POWER' }),
	};
	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			getAll: async () => [mockComponent({ PrimitiveId: 'u1', ComponentType: 'part', Designator: 'U1' })],
			getAllPinsByPrimitiveId: async () => [pin],
		},
	};
	try {
		const res: any = await schematicComponentsList({ includePins: true, includePinNets: false });
		assert.deepEqual(res.result.components[0].pins[0].otherProperty, {
			NameVisible: true, NameFontSize: 9, Alias: 'POWER',
		});
	}
	finally { delete (globalThis as any).eda; }
});

test('components.list: geometry-only pin reads do not compile a netlist; wire read failures remain unknown', async (t) => {
	const previous = (globalThis as any).eda;
	t.after(() => { (globalThis as any).eda = previous; });
	let netlistReads = 0;
	let mode = 'valid';
	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			getAll: async () => [mockComponent({ PrimitiveId: 'u1', ComponentType: 'part', Designator: 'U1' })],
			getAllPinsByPrimitiveId: async () => [],
		},
		sch_ManufactureData: { getNetlistFile: async () => { netlistReads++; throw new Error('must not compile'); } },
		sch_PrimitiveWire: { getAll: async () => {
			if (mode === 'throw') throw new Error('wire channel unavailable');
			if (mode === 'missing') return undefined;
			if (mode === 'empty') return [];
			return [{ getState_Line: () => mode === 'bad' ? [0, NaN, 10, 0] : [0, 0, 10, 0], getState_Net: () => 'N', getState_PrimitiveId: () => 'w1' }];
		} },
	};
	for (mode of ['valid', 'empty', 'throw', 'missing', 'bad']) {
		const res: any = await schematicComponentsList({ includePins: true, includePinNets: false, includeWires: true });
		assert.equal(netlistReads, 0);
		assert.equal(res.result.components[0].pinsAvailable, true);
		assert.equal(res.result.wiresAvailable, mode === 'valid' || mode === 'empty');
		if (mode === 'valid') assert.equal(res.result.wires[0].primitiveId, 'w1');
		else assert.deepEqual(res.result.wires, []);
		if (['throw', 'missing', 'bad'].includes(mode)) assert.equal(typeof res.result.wiresError, 'string');
	}
});

test('components.list: connectivitySummary fails closed when an SDK inventory is unavailable', async () => {
	(globalThis as any).eda = {
		sch_PrimitiveComponent: { getAll: async () => [] },
		sch_PrimitiveWire: { getAll: async () => undefined },
		sch_PrimitiveBus: { getAll: async () => [] },
	};
	try {
		await assert.rejects(
			() => schematicComponentsList({ includeConnectivitySummary: true }),
			(err: any) => {
				assert.equal(err.code, 'EDA_CALL_FAILED');
				assert.match(err.detail, /wire getAll\(\) did not return an array/);
				return true;
			},
		);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

import { schematicComponentModify, schematicComponentPlace, schematicPinSetNoConnect } from './actions';

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

// ─── schematic.component.modify 自定义属性兼容与回读校验 ───────────────

function installComponentModifyStub(options: {
	/** false = SDK 全部静默丢弃(#150 假成功) */
	apply?: boolean;
	/** 只有这些键生效,其余静默丢弃(#151 部分应用) */
	applyKeys?: string[];
	/** 平台规范化:落库值一律 String() 化(数字 10 → "10") */
	normalize?: boolean;
	/** modify 成功后回读通道坏掉:get 恒抛错(#151 残洞) */
	failGetAfterModify?: boolean;
	/** 覆盖初始 otherProperty(默认 Description/Value 两键) */
	initial?: Record<string, string | number | boolean>;
	/** 平台在任何写入后硬删这些键(模拟保留写回也保不住的键,#175) */
	dropKeys?: string[];
} = {}) {
	let otherProperty: Record<string, string | number | boolean> = {
		...(options.initial ?? { Description: 'keep me', Value: '' }),
	};
	const calls: Array<{ id: string; patch: Record<string, unknown> }> = [];
	let modifyCalled = false;
	const current = () => mockComponent({
		PrimitiveId: 'r2-pid',
		Designator: 'R2',
		OtherProperty: { ...otherProperty },
	});
	const store = (v: string | number | boolean) => options.normalize ? String(v) : v;
	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			get: async (id: string) => {
				if (options.failGetAfterModify && modifyCalled) throw new Error('readback channel down');
				return id === 'r2-pid' ? current() : undefined;
			},
			modify: async (id: string, patch: Record<string, unknown>) => {
				calls.push({ id, patch });
				modifyCalled = true;
				if (options.apply !== false) {
					if (patch.otherProperty) {
						const next = patch.otherProperty as Record<string, string | number | boolean>;
						if (options.applyKeys) {
							const out = { ...otherProperty };
							for (const key of options.applyKeys) {
								if (key in next) out[key] = store(next[key]);
							}
							otherProperty = out;
						}
						else {
							otherProperty = Object.fromEntries(
								Object.entries(next).map(([k, v]) => [k, store(v)]),
							);
						}
					}
					else {
						// #175 平台真值:modify 对 otherProperty 是整体重写语义,
						// patch 不带 otherProperty ⇒ 现有自定义属性被整体清空。
						otherProperty = {};
					}
					for (const key of options.dropKeys ?? []) delete otherProperty[key];
				}
				return current();
			},
		},
	};
	return { calls, getOtherProperty: () => ({ ...otherProperty }) };
}

test('modify: maps customAttributes to SDK otherProperty and preserves existing fields', async () => {
	const fx = installComponentModifyStub();
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { customAttributes: { Value: '10kΩ' } },
	});

	assert.deepEqual(fx.calls[0], {
		id: 'r2-pid',
		patch: { otherProperty: { Description: 'keep me', Value: '10kΩ' } },
	});
	assert.deepEqual(fx.getOtherProperty(), { Description: 'keep me', Value: '10kΩ' });
	assert.equal(res.result.component.otherProperty.Value, '10kΩ');
	delete (globalThis as any).eda;
});

test('modify: partial otherProperty also merges instead of clearing metadata', async () => {
	const fx = installComponentModifyStub();
	await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { otherProperty: { Value: '4.7kΩ' } },
	});

	assert.deepEqual(fx.getOtherProperty(), { Description: 'keep me', Value: '4.7kΩ' });
	delete (globalThis as any).eda;
});

test('modify: rejects SDK success when requested properties were silently ignored', async () => {
	installComponentModifyStub({ apply: false });
	await assert.rejects(
		() => schematicComponentModify({
			primitiveId: 'r2-pid',
			patch: { customAttributes: { Value: '10kΩ' } },
		}),
		/returned success but did not apply properties: Value/,
	);
	delete (globalThis as any).eda;
});

test('modify: unknown top-level patch keys rejected BEFORE any eda call (issue #151)', async () => {
	const fx = installComponentModifyStub();
	await assert.rejects(
		() => schematicComponentModify({
			primitiveId: 'r2-pid',
			// typo of customAttributes — the SDK would silently drop it
			patch: { customAtributes: { Value: '10kΩ' } },
		}),
		/Unknown component patch field\(s\): customAtributes/,
	);
	// 前置拒绝 = 零变异:modify 从未被调用
	assert.equal(fx.calls.length, 0);
	// Allowed 列表标注别名互斥,不误导「两个都能传」
	await assert.rejects(
		() => schematicComponentModify({
			primitiveId: 'r2-pid',
			patch: { bogus: 1 },
		}),
		/alias of otherProperty — use one, not both/,
	);
	delete (globalThis as any).eda;
});

test('modify: partial application returns structured success with notApplied + propertiesBefore (issue #151)', async () => {
	const fx = installComponentModifyStub({ applyKeys: ['Value'] });
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { customAttributes: { Value: '10kΩ', Grade: 'A' } },
	});

	// ok:true(不抛错)→ daemon 照常 arm autosave,已应用子集得到落盘保护
	assert.equal(res.result.partial, true);
	assert.deepEqual(res.result.applied, ['Value']);
	assert.deepEqual(res.result.notApplied, ['Grade']);
	assert.deepEqual(res.result.alreadySet, []);
	// Value 在 before 里已有键(值 '')→ 不算新增键
	assert.deepEqual(res.result.addedKeys, []);
	// before 快照支撑「重放恢复」与审计 before/after
	assert.deepEqual(res.result.propertiesBefore, { Description: 'keep me', Value: '' });
	assert.equal(res.warnings.length, 1);
	assert.match(res.warnings[0], /Grade/);
	// 文案带组件身份:CLI 全局按文本 dedup,不同组件的同键 partial 不互吞
	assert.match(res.warnings[0], /r2-pid/);
	// 画布真值:Value 已生效,Grade 无踪影
	assert.deepEqual(fx.getOtherProperty(), { Description: 'keep me', Value: '10kΩ' });
	delete (globalThis as any).eda;
});

test('modify: already-equal key does NOT shield the all-dropped hard gate (issue #151 review)', async () => {
	// Description 期望值 === 原值:SDK 全部丢弃时回读命中纯属巧合,
	// 不可证明写入 → 画布确未变,必须报错而非 partial(#150 假成功检测不被绕过)
	installComponentModifyStub({ apply: false });
	await assert.rejects(
		() => schematicComponentModify({
			primitiveId: 'r2-pid',
			patch: { customAttributes: { Description: 'keep me', Grade: 'A' } },
		}),
		/returned success but did not apply properties: Grade/,
	);
	delete (globalThis as any).eda;
});

test('modify: newly-added keys reported in addedKeys — propertiesBefore replay cannot remove them (issue #151 review)', async () => {
	installComponentModifyStub({ applyKeys: ['NewA'] });
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { customAttributes: { NewA: '1', NewB: '2' } },
	});
	assert.equal(res.result.partial, true);
	assert.deepEqual(res.result.applied, ['NewA']);
	assert.deepEqual(res.result.notApplied, ['NewB']);
	// NewA 不在 before 快照里 → merge 语义下重放 propertiesBefore 删不掉它,
	// 结构化暴露 + 文案如实说明,不谎报「可恢复」
	assert.deepEqual(res.result.addedKeys, ['NewA']);
	assert.match(res.warnings[0], /NewA/);
	assert.match(res.warnings[0], /无法经 modify 移除/);
	delete (globalThis as any).eda;
});

test('modify: zero properties applied but geometry also patched → partial success, not error (issue #151)', async () => {
	installComponentModifyStub({ apply: false });
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		// x 可能已生效(stub 不建模几何,但真机上几何与属性独立提交)——
		// 抛错会把可能已变的画布压成 ok:false 丢 autosave
		patch: { x: 150, customAttributes: { Value: '10kΩ' } },
	});
	assert.equal(res.result.partial, true);
	assert.deepEqual(res.result.notApplied, ['Value']);
	delete (globalThis as any).eda;
});

test('modify: platform number→string normalization is NOT a false partial (issue #151)', async () => {
	installComponentModifyStub({ normalize: true });
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { customAttributes: { Value: 10 } },
	});
	// String(10) === "10":强转容忍比较,不误报 partial
	assert.equal(res.result.partial, undefined);
	assert.equal(res.result.component.otherProperty.Value, '10');
	// 全量成功也带 before 快照(审计 before/after 铁律)
	assert.deepEqual(res.result.propertiesBefore, { Description: 'keep me', Value: '' });
	delete (globalThis as any).eda;
});

test('modify: readback failure after successful modify degrades to verified:false, never ok:false (issue #151)', async () => {
	installComponentModifyStub({ failGetAfterModify: true });
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { customAttributes: { Value: '10kΩ' } },
	});
	// modify 已成功 ⇒ 画布已变;回读通道失败绝不能抛错(丢 autosave),
	// 降级为 verified:false + warning(pageRename 先例)
	assert.equal(res.result.verified, false);
	// 画布状态未经验证,恰是最需要 before 快照支撑恢复的场景
	assert.deepEqual(res.result.propertiesBefore, { Description: 'keep me', Value: '' });
	assert.equal(res.warnings.length, 1);
	assert.match(res.warnings[0], /回读校验/);
	delete (globalThis as any).eda;
});

test('modify: top-level-only patch preserves existing custom properties (issue #175)', async () => {
	// 平台 modify 整体重写 otherProperty(stub 默认建模):不带 otherProperty 的
	// patch 会把自定义属性清空。read-preserve-write 必须在同一次 modify 里原样写回。
	const fx = installComponentModifyStub();
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { supplierId: 'C2918502' },
	});

	assert.deepEqual(fx.calls[0], {
		id: 'r2-pid',
		patch: { supplierId: 'C2918502', otherProperty: { Description: 'keep me', Value: '' } },
	});
	// 画布真值:自定义属性原样存活,没有被整体重写清空
	assert.deepEqual(fx.getOtherProperty(), { Description: 'keep me', Value: '' });
	assert.equal(res.result.partial, undefined);
	// 显式回报被连带重写但原样保留的键 + before 快照(不再有静默面)
	assert.deepEqual(res.result.propertiesPreserved, ['Description', 'Value']);
	assert.deepEqual(res.result.propertiesBefore, { Description: 'keep me', Value: '' });
	delete (globalThis as any).eda;
});

test('modify: top-level-only patch with empty otherProperty adds no property write (issue #175)', async () => {
	// 无数据可保时不做无谓的整体写(整体写 otherProperty 有平台副作用先例,
	// 见 attrs_backfill 的投影键事故)
	const fx = installComponentModifyStub({ initial: {} });
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { supplierId: 'C2918502' },
	});
	assert.deepEqual(fx.calls[0], { id: 'r2-pid', patch: { supplierId: 'C2918502' } });
	assert.equal(res.result.propertiesPreserved, undefined);
	assert.equal(res.result.propertiesBefore, undefined);
	delete (globalThis as any).eda;
});

test('modify: preserved key the platform still drops → partial + notApplied, never silent (issue #175)', async () => {
	const fx = installComponentModifyStub({
		initial: { Description: 'keep me', Value: '10k' },
		dropKeys: ['Value'],
	});
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { supplierId: 'C2918502' },
	});
	// 顶层字段已生效是既成事实(ok:true 照常 arm autosave),但丢键必须结构化
	// 暴露:notApplied 非空 → CLI `sch modify` 非零退出,错误信号不丢
	assert.equal(res.result.partial, true);
	assert.deepEqual(res.result.notApplied, ['Value']);
	assert.deepEqual(res.result.propertiesBefore, { Description: 'keep me', Value: '10k' });
	assert.equal(res.warnings.length, 1);
	assert.match(res.warnings[0], /Value/);
	assert.match(res.warnings[0], /r2-pid/);
	assert.deepEqual(fx.getOtherProperty(), { Description: 'keep me' });
	delete (globalThis as any).eda;
});

test('modify: readback failure on preserve path degrades to verified:false with before snapshot (issue #175)', async () => {
	installComponentModifyStub({ failGetAfterModify: true });
	const res: any = await schematicComponentModify({
		primitiveId: 'r2-pid',
		patch: { supplierId: 'C2918502' },
	});
	// preserve 写回已随 modify 提交;回读通道失败绝不能抛错(丢 autosave)
	assert.equal(res.result.verified, false);
	assert.deepEqual(res.result.propertiesBefore, { Description: 'keep me', Value: '' });
	assert.equal(res.warnings.length, 1);
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

// ─── schematic.titleblock.modify 回读验证(平台对不认识的明细项返回 true) ───
//
// 官方 @beta remarks 原文:「任何无法识别的明细项将被忽略」,且「如若存在无法
// 识别的明细项但程序并未出错,将返回 true 的结果」。旧实现直接透传该 ok,
// 于是「改了个根本不存在的明细项」报成功。audit log 实测这个 action 32 次调用
// 0 次成功,失败 payload 是拿 Size/Width/Height 当纸张属性写 —— 那些不是明细项。

import { schematicTitleBlockModify } from './actions';

type TBData = Record<string, { showTitle?: boolean; showValue?: boolean; value?: unknown }>;

/** 装一个 dmt_Schematic 假件:第 1 次读返回 before,之后返回 after(默认 = before,
 *  即平台什么也没改)。readFails 模拟回读不可用。 */
function installTitleBlockStub(opts: {
	before: TBData;
	after?: TBData;
	showBefore?: boolean;
	showAfter?: boolean;
	ok?: boolean;
	readFails?: 'always' | 'afterOnly';
}) {
	let reads = 0;
	const calls: Array<{ show: unknown; data: unknown }> = [];
	(globalThis as any).eda = {
		dmt_Schematic: {
			getCurrentSchematicPageInfo: async () => {
				reads += 1;
				if (opts.readFails === 'always' || (opts.readFails === 'afterOnly' && reads > 1)) {
					throw new Error('page info unavailable');
				}
				const first = reads === 1;
				return {
					uuid: 'page-1',
					name: 'Page1',
					showTitleBlock: first
						? (opts.showBefore ?? true)
						: (opts.showAfter ?? opts.showBefore ?? true),
					titleBlockData: first ? opts.before : (opts.after ?? opts.before),
				};
			},
			modifySchematicPageTitleBlock: async (show: unknown, data: unknown) => {
				calls.push({ show, data });
				return opts.ok ?? true;
			},
		},
	};
	return calls;
}

test('titleblock: 结构键(拿明细表当纸张属性写)= 零变异前置拒绝,平台调用根本不发生 (#186)', async () => {
	// 复刻 audit log 里那次真实失败的 payload:拿明细表当纸张属性写。
	//
	// 行为已变(#186):以前是「照发给平台 → 回读发现没生效 → 报 nothing was applied」。
	// 但真机证明这条路会**损毁文档** —— 平台会把结构键的 value 写进图框的 UUID
	// 引用位(符号名灌进 component/device/symbol),保存后重启拒载 = 图框丢失。
	// 所以现在在下发之前就拒,且必须证明**一次平台调用都没发出**。
	const calls = installTitleBlockStub({
		before: { Title: { value: 'old' }, Size: { value: 'A4' }, Width: { value: '1170' } },
	});
	await assert.rejects(
		() => schematicTitleBlockModify({
			titleBlockData: { Size: { value: 'A2' }, Width: { value: '2340' } },
		}) as any,
		(err: any) => {
			assert.equal(err.code, 'PRECONDITION_REFUSED', '必须是零变异拒绝码,不能计进连接器健康度');
			assert.match(err.message, /Size, Width/, 'the refused items must be named');
			assert.match(err.message, /一个字节都没写/, '必须明说本次没有任何写入');
			return true;
		},
	);
	assert.equal(calls.length, 0, '结构键必须在下发之前被拦住 —— 一旦发出去就已经晚了');
	delete (globalThis as any).eda;
});

test('titleblock: 把 get 的整包原样传回(只改一个文本项)= 结构键被静默丢弃,不再损毁图框 (#186)', async () => {
	// issue #186 报告人的真实用法:titleblock.get 拿完整数据 → 只改 Title → 整包传回。
	// 那 32 个结构/投影键的值与画布一致(他并不想改它们),所以不该拒绝整次调用,
	// 而应把它们丢掉、只下发真正要改的文本项。
	const structural = {
		Device: { value: 'Drawing-Symbol_A4' },
		Symbol: { value: 'Drawing-Symbol_A4' },
		Border: { value: '1' },
		'Title Block': { value: '1' },
		'@Page No': { value: 1 },
	};
	const calls = installTitleBlockStub({
		before: { Title: { value: 'old' }, ...structural },
		after: { Title: { value: 'new' }, ...structural },
	});
	const res: any = await schematicTitleBlockModify({
		titleBlockData: { Title: { value: 'new' }, ...structural },
	});
	assert.equal(res.result.ok, true);
	assert.deepEqual(res.result.applied, ['Title']);
	assert.equal(calls.length, 1, '仍然只发一次平台调用');
	// 关键:下发的 payload 里**一个结构键都不许有**。
	assert.deepEqual(Object.keys(calls[0].data as object), ['Title'],
		'Device/Symbol/Border/@… 绝不能出现在下发数据里');
	assert.deepEqual((res.result.ignoredKeys as Array<string>).sort(),
		['@Page No', 'Border', 'Device', 'Symbol', 'Title Block'],
		'被丢掉的结构键要如实报给调用方');
	delete (globalThis as any).eda;
});

test('titleblock: every requested item lands → verified success, not partial', async () => {
	const calls = installTitleBlockStub({
		before: { Title: { value: 'old' }, Designer: { value: 'A' } },
		after: { Title: { value: 'new' }, Designer: { value: 'B' } },
	});
	const res: any = await schematicTitleBlockModify({
		titleBlockData: { Title: { value: 'new' }, Designer: { value: 'B' } },
	});
	assert.equal(calls.length, 1);
	assert.equal(res.result.verified, true);
	assert.equal(res.result.partial, undefined);
	assert.deepEqual(res.result.applied.sort(), ['Designer', 'Title']);
	delete (globalThis as any).eda;
});

test('titleblock: partial application keeps ok:true and lists notApplied', async () => {
	installTitleBlockStub({
		before: { Title: { value: 'old' } },
		after: { Title: { value: 'new' } },
	});
	const res: any = await schematicTitleBlockModify({
		titleBlockData: { Title: { value: 'new' }, Ghost: { value: 'z' } },
	});
	assert.equal(res.result.ok, true, 'the applied subset is on canvas — autosave must still arm');
	assert.equal(res.result.partial, true);
	assert.deepEqual(res.result.applied, ['Title']);
	assert.deepEqual(res.result.notApplied, ['Ghost']);
	assert.deepEqual(res.result.unknownKeys, ['Ghost']);
	assert.ok(res.warnings.some((w: string) => w.includes('Ghost')));
	delete (globalThis as any).eda;
});

test('titleblock: an already-equal item does NOT shield the all-dropped hard gate', async () => {
	// Title 改前就等于期望值 → 无法证明本次写入 → 不得豁免假成功检测(#151 review)。
	installTitleBlockStub({ before: { Title: { value: 'same' } } });
	await assert.rejects(
		() => schematicTitleBlockModify({
			titleBlockData: { Title: { value: 'same' }, Ghost: { value: 'z' } },
		}) as any,
		/nothing was applied/,
	);
	delete (globalThis as any).eda;
});

test('titleblock: platform number→string normalization is NOT a false partial', async () => {
	installTitleBlockStub({
		before: { Rev: { value: '1' } },
		after: { Rev: { value: '2' } },   // 平台回读成字符串
	});
	const res: any = await schematicTitleBlockModify({ titleBlockData: { Rev: { value: 2 } } });
	assert.equal(res.result.partial, undefined);
	assert.deepEqual(res.result.applied, ['Rev']);
	delete (globalThis as any).eda;
});

test('titleblock: visibility-only toggle that lands is a clean success', async () => {
	const calls = installTitleBlockStub({ before: {}, showBefore: true, showAfter: false });
	const res: any = await schematicTitleBlockModify({ showTitleBlock: false });
	assert.deepEqual(calls[0].show, false);
	assert.equal(res.result.visibilityApplied, true);
	assert.equal(res.result.partial, undefined);
	delete (globalThis as any).eda;
});

test('titleblock: visibility-only toggle that does NOT land is a hard failure', async () => {
	installTitleBlockStub({ before: {}, showBefore: true, showAfter: true });
	await assert.rejects(
		() => schematicTitleBlockModify({ showTitleBlock: false }) as any,
		/showTitleBlock/,
	);
	delete (globalThis as any).eda;
});

test('titleblock: readback failure degrades to verified:false, never ok:false', async () => {
	installTitleBlockStub({ before: { Title: { value: 'old' } }, readFails: 'afterOnly' });
	const res: any = await schematicTitleBlockModify({ titleBlockData: { Title: { value: 'new' } } });
	assert.equal(res.result.ok, true, 'the write already returned success — do not lose autosave');
	assert.equal(res.result.verified, false);
	assert.ok(res.warnings.some((w: string) => w.includes('verified:false')));
	delete (globalThis as any).eda;
});

test('titleblock: an explicit false from the SDK is surfaced as an error', async () => {
	installTitleBlockStub({ before: { Title: { value: 'old' } }, ok: false });
	await assert.rejects(
		() => schematicTitleBlockModify({ titleBlockData: { Title: { value: 'new' } } }) as any,
		/returned false/,
	);
	delete (globalThis as any).eda;
});

// ─── schematic.component.replace: diffPins ──────────────────────────────

import { diffPins } from './actions';

test('diffPins: identical pin tables → empty diff', () => {
	const pins = [
		{ pinNumber: '1', pinName: 'VCC', x: 100, y: 200 },
		{ pinNumber: '2', pinName: 'GND', x: 100, y: 180 },
	];
	const d = diffPins(pins, pins);
	assert.equal(d.removed.length, 0);
	assert.equal(d.added.length, 0);
	assert.equal(d.moved.length, 0);
});

test('diffPins: removed / added / moved are keyed by pinNumber', () => {
	const oldPins = [
		{ pinNumber: '1', pinName: 'VCC', x: 100, y: 200 },
		{ pinNumber: '2', pinName: 'GND', x: 100, y: 180 },
		{ pinNumber: '3', pinName: 'EN', x: 100, y: 160 },
	];
	const newPins = [
		{ pinNumber: '1', pinName: 'VDD', x: 100, y: 200 }, // renamed, same spot → NOT moved
		{ pinNumber: '2', pinName: 'GND', x: 120, y: 180 }, // moved
		{ pinNumber: '4', pinName: 'NC', x: 100, y: 140 }, // added
	];
	const d = diffPins(oldPins, newPins);
	assert.deepEqual(d.removed.map(p => p.pinNumber), ['3']);
	assert.deepEqual(d.added.map(p => p.pinNumber), ['4']);
	assert.deepEqual(d.moved, [
		{ pinNumber: '2', pinName: 'GND', from: { x: 100, y: 180 }, to: { x: 120, y: 180 } },
	]);
});

// ─── #162: "not on the active page" must not be reported as "does not exist" ──

/** Which page the stubbed editor currently has in front (tagComponentPages cycles it). */
let current = 'page-1';

/** eda stub with an active-page-scoped get() and a document-wide getAll(). */
function edaWithPages(activeIds: string[], otherPageIds: string[]) {
	const idComponent = (id: string) => ({ getState_PrimitiveId: () => id }) as any;
	return {
		sch_PrimitiveComponent: {
			get: async (id: string) => (activeIds.includes(id) ? idComponent(id) : undefined),
			getAll: async (_types?: unknown, allPages?: boolean) =>
				(allPages ? [...activeIds, ...otherPageIds] : activeIds).map(idComponent),
		},
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'page-1' }) },
		dmt_Schematic: {
			getAllSchematicPagesInfo: async () => [
				{ uuid: 'page-1', name: 'P1' },
				{ uuid: 'page-5', name: 'P5' },
			],
		},
		dmt_EditorControl: {
			openDocument: async (uuid: string) => {
				// Emulate active-page scoping: getAll() with no allPages follows the tab.
				current = uuid;
			},
		},
	};
}

test('getComponentOrThrow: off-active-page id is diagnosed as such, not as missing', async () => {
	const stub: any = edaWithPages(['on-active'], ['eefc6f2c400c3794']);
	// tagComponentPages walks pages; make getAll() (active-page form) follow the tab.
	stub.sch_PrimitiveComponent.getAll = async (_t?: unknown, allPages?: boolean) => {
		const ids = allPages
			? ['on-active', 'eefc6f2c400c3794']
			: current === 'page-5' ? ['eefc6f2c400c3794'] : ['on-active'];
		return ids.map(id => ({ getState_PrimitiveId: () => id })) as any;
	};
	(globalThis as any).eda = stub;
	try {
		await assert.rejects(
			() => getComponentOrThrow('eefc6f2c400c3794'),
			(err: any) => {
				assert.equal(err.code, 'INVALID_STATE');
				assert.match(err.message, /not the ACTIVE page/);
				assert.match(err.message, /P5/); // names the page it actually lives on
				assert.doesNotMatch(err.message, /No schematic component found/);
				return true;
			},
		);
	}
	finally {
		current = 'page-1';
		delete (globalThis as any).eda;
	}
});

test('getComponentOrThrow: a truly absent id still reports not-found (on any page)', async () => {
	(globalThis as any).eda = edaWithPages(['on-active'], ['elsewhere']) as any;
	try {
		await assert.rejects(
			() => getComponentOrThrow('ghost'),
			(err: any) => {
				assert.equal(err.code, 'INVALID_STATE');
				assert.match(err.message, /No schematic component found with primitiveId "ghost" on any page/);
				return true;
			},
		);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('getComponentOrThrow: returns the component when it is on the active page', async () => {
	(globalThis as any).eda = edaWithPages(['here'], []) as any;
	try {
		const c: any = await getComponentOrThrow('here');
		assert.equal(c.getState_PrimitiveId(), 'here');
	}
	finally {
		delete (globalThis as any).eda;
	}
});

// ─── #164: prim-delete must report VERIFIED deletions, never the request ─────

/** eda stub whose text delete is a no-op (the platform behaviour issue #164 hit). */
function edaWithUndeletableText(textIds: string[], wireIds: string[]) {
	const alive = { texts: [...textIds], wires: [...wireIds] };
	const prim = (id: string) => ({ getState_PrimitiveId: () => id }) as any;
	const noopClass = () => ({ getAll: async () => [], delete: async () => true });
	return {
		sch_PrimitiveComponent: { getAll: async () => [], delete: async () => true },
		sch_PrimitiveText: {
			getAll: async () => alive.texts.map(prim),
			delete: async () => true, // returns true, keeps the primitives
		},
		sch_PrimitiveWire: {
			getAll: async () => alive.wires.map(prim),
			delete: async (ids: string[]) => {
				alive.wires = alive.wires.filter(id => !ids.includes(id));
				return true;
			},
		},
		sch_PrimitiveBus: noopClass(),
		sch_PrimitiveArc: noopClass(),
		sch_PrimitiveCircle: noopClass(),
		sch_PrimitiveRectangle: noopClass(),
		sch_PrimitivePolygon: noopClass(),
		sch_SelectControl: { getAllSelectedPrimitives_PrimitiveId: async () => [] },
	};
}

test('prim-delete: primitives that survive the delete are reported, not counted as deleted', async () => {
	(globalThis as any).eda = edaWithUndeletableText(['t1', 't2'], ['w1']) as any;
	try {
		const res: any = await runAction('schematic.primitives.delete', { primitiveIds: ['t1', 't2', 'w1'] });
		assert.equal(res.result.deleted.texts, 0, 'undeletable texts must not be counted as deleted');
		assert.equal(res.result.deleted.wires, 1);
		assert.equal(res.result.total, 1, 'total counts only what actually went away');
		assert.equal(res.result.requested, 3);
		assert.equal(res.result.partial, true);
		assert.deepEqual(res.result.survived, { texts: ['t1', 't2'] });
		assert.equal(res.result.survivedTotal, 2);
		assert.deepEqual(res.result.deletedIds, { wires: ['w1'] });
		assert.match(String(res.warnings?.[0] ?? ''), /survived/);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('prim-delete: a fully successful delete carries no partial flag', async () => {
	(globalThis as any).eda = edaWithUndeletableText([], ['w1', 'w2']) as any;
	try {
		const res: any = await runAction('schematic.primitives.delete', { primitiveIds: ['w1', 'w2'] });
		assert.equal(res.result.total, 2);
		assert.equal(res.result.partial, undefined);
		assert.equal(res.result.survived, undefined);
		assert.equal(res.warnings, undefined);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('guarded schematic clear refuses changed wire, flag and graphic before deletion', async () => {
	const state: any = {
		part: { PrimitiveId: 'part-1', ComponentType: 'part', Designator: 'R1', UniqueId: 'gge1', Name: 'R', SubPartName: 'device.1', AddIntoBom: false, AddIntoPcb: true, Manufacturer: '', ManufacturerId: '', Supplier: '', SupplierId: '', OtherProperty: {}, Component: {}, Symbol: {}, Footprint: {}, X: 0, Y: 0, Rotation: 0, Mirror: false },
		component: { PrimitiveId: 'flag-1', ComponentType: 'netflag', Net: 'GND', X: 10, Y: 20, Rotation: 0 },
		sheet: { PrimitiveId: 'sheet-1', ComponentType: 'sheet', OtherProperty: { '@Update Date': 'old', '@Update Time': 'old', Title: 'Circuit' } },
		wire: { PrimitiveId: 'wire-1', Line: [10, 20, 30, 20], Net: 'GND', Color: '#000', LineWidth: 1, LineType: 0 },
		text: { PrimitiveId: 'text-1', X: 0, Y: 0, Content: 'A', Rotation: 0, TextColor: '#000', FontName: 'Arial', FontSize: 10, Bold: false, Italic: false, UnderLine: false, AlignMode: 0 },
		attribute: { PrimitiveId: 'orphan-attr', X: 0, Y: 0, Rotation: 0, Color: '#000', FontName: 'Arial', FontSize: 10, Bold: false, Italic: false, UnderLine: false, AlignMode: 0, FillColor: 'none', Key: 'Label', Value: 'A', KeyVisible: true, ValueVisible: true, ParentPrimitiveId: 'old-parent' },
		object: { PrimitiveId: 'embedded-1', Content: 'data', StartX: 0, StartY: 0, Width: 10, Height: 10, Rotation: 0, Mirror: false, FileName: 'logo.svg' },
	};
	let missingObjectGetter = false;
	const primitive = (name: string) => new Proxy({}, { get: (_target, prop) => {
		if (name === 'object' && prop === 'getState_Content' && missingObjectGetter) return undefined;
		if (typeof prop === 'string' && prop.startsWith('getState_')) return () => state[name][prop.slice(9)];
		return undefined;
	}});
	let deletes = 0;
	const klass = (name?: string) => ({ getAll: async () => name ? [primitive(name)] : [], delete: async () => { deletes++; return true; } });
	(globalThis as any).eda = {
		sch_PrimitiveComponent: { ...klass(), getAll: async () => [primitive('part'), primitive('component'), primitive('sheet')] }, sch_PrimitiveWire: klass('wire'), sch_PrimitiveText: klass('text'),
		sch_PrimitiveBus: klass(), sch_PrimitiveArc: klass(), sch_PrimitiveCircle: klass(), sch_PrimitiveRectangle: klass(), sch_PrimitivePolygon: klass(),
		sch_PrimitiveAttribute: { getAll: async (parent?: string) => parent ? [] : [primitive('attribute')], getAllPrimitiveId: async () => ['orphan-attr'], get: async () => primitive('attribute') },
		sch_PrimitiveObject: { getAll: async () => [primitive('object')] },
	};
	try {
		const before: any = await runAction('schematic.components.list', { includePagePrimitives: true });
		const expectedPagePrimitives = JSON.stringify(before.result.pagePrimitives);
		state.sheet.OtherProperty['@Update Date'] = 'new';
		state.sheet.OtherProperty['@Update Time'] = 'new';
		const originalState = JSON.parse(JSON.stringify(state));
		const restore = () => { for (const key of Object.keys(originalState)) state[key] = JSON.parse(JSON.stringify(originalState[key])); };
		const same: any = await runAction('schematic.page.clear', { preserveParts: true, preservePartIds: ['part-1'], dryRun: true, expectedPagePrimitives });
		assert.equal(same.result.dryRun, true);
		await assert.rejects(() => runAction('schematic.page.clear', { expectedPagePrimitives }), /ordinary clear cannot prove removal/);
		await assert.rejects(() => runAction('schematic.page.clear', { dryRun: true, requireCompleteInventory: true }), /Page is not empty/);
		assert.equal(deletes, 0);
		for (const [name, edit] of [
			['wire', () => { state.wire.LineWidth = 2; }],
			['flag', () => { state.component.Net = 'CHANGED'; }],
			['graphic', () => { state.text.Content = 'B'; }],
			['orphan attribute', () => { state.attribute.Value = 'B'; }],
			['embedded object', () => { state.object.Content = 'changed payload'; }],
		] as const) {
			restore();
			edit();
			await assert.rejects(() => runAction('schematic.page.clear', { preserveParts: true, preservePartIds: ['part-1'], expectedPagePrimitives }), /No primitives deleted: page drawing\/object state differs/,
				`${name} drift must refuse clear`);
			assert.equal(deletes, 0);
		}
		restore();
		missingObjectGetter = true;
		await assert.rejects(() => runAction('schematic.page.clear', { preserveParts: true, preservePartIds: ['part-1'], expectedPagePrimitives }), /Page objects.Content accessor unavailable/);
		assert.equal(deletes, 0);
	}
	finally { delete (globalThis as any).eda; }
});

test('page primitive attribute state uses typed get(id) when bulk getter is incomplete', async () => {
	const state: Record<string, unknown> = {
		PrimitiveId: 'attr-1', X: 0, Y: 0, Rotation: 0, Color: null, FontName: null,
		FontSize: null, Bold: null, Italic: null, UnderLine: null, AlignMode: null,
		FillColor: null, Key: 'Label', Value: 'old', KeyVisible: true,
		ValueVisible: true, ParentPrimitiveId: 'orphan-parent',
	};
	let directKeyVisible: boolean | undefined = true;
	let deletes = 0;
	const attribute = (bulk: boolean) => new Proxy({}, { get: (_target, prop) => {
		if (typeof prop === 'string' && prop.startsWith('getState_')) return () =>
			prop === 'getState_KeyVisible' ? (bulk ? undefined : directKeyVisible) : state[prop.slice(9)];
		return undefined;
	}});
	const klass = () => ({ getAll: async () => [], delete: async () => { deletes++; return true; } });
	(globalThis as any).eda = {
		sch_PrimitiveComponent: klass(), sch_PrimitiveWire: klass(), sch_PrimitiveBus: klass(),
		sch_PrimitiveArc: klass(), sch_PrimitiveCircle: klass(), sch_PrimitiveRectangle: klass(),
		sch_PrimitivePolygon: klass(), sch_PrimitiveText: klass(), sch_PrimitiveObject: klass(),
		sch_PrimitiveAttribute: {
			getAll: async () => [attribute(true)], getAllPrimitiveId: async () => ['attr-1'],
			get: async () => attribute(false),
		},
	};
	try {
		const read: any = await runAction('schematic.components.list', { includePagePrimitives: true });
		assert.equal(read.result.pagePrimitives.attributes[0].KeyVisible, true);
		const expectedPagePrimitives = JSON.stringify(read.result.pagePrimitives);
		directKeyVisible = undefined;
		await assert.rejects(() => runAction('schematic.page.clear', { expectedPagePrimitives }),
			/Official native project export is unavailable for attribute visibility recovery/);
		assert.equal(deletes, 0);
	}
	finally { delete (globalThis as any).eda; }
});

// ─── schematic.component.delete cascade (ADR-0004 Decision 5) ────────────────
// Deleting a part must also remove its EXCLUSIVE stub-wire trees + the netflags
// riding them (the residue is a ghost-connection boobytrap: the next part placed
// there silently inherits the stray net). Shared trees (still touching another
// live part's pin) are never deleted. `cascade:false` keeps the old behavior.

/**
 * eda stub for the delete-cascade tests: parts with real pins, wires as flat
 * polylines, netflags with coordinates. `keepWireIds`/`keepFlagIds` model the
 * platform's lying delete (returns true, keeps the primitive).
 */
function installComponentDeleteStub(opts: {
	parts: Array<{ id: string; designator: string; pins: Array<[number, number]> }>;
	flags?: Array<{ id: string; x: number; y: number; net?: string }>;
	wires?: Array<{ id: string; points: Array<number> }>;
	keepWireIds?: Array<string>;
	keepFlagIds?: Array<string>;
}) {
	const keepWires = new Set(opts.keepWireIds ?? []);
	const keepFlags = new Set(opts.keepFlagIds ?? []);
	let parts = [...opts.parts];
	let flags = [...(opts.flags ?? [])];
	let wires = [...(opts.wires ?? [])];
	const deleteCalls: Array<{ kind: string; ids: Array<string> }> = [];
	const mkPart = (p: { id: string; designator: string }): any => ({
		getState_PrimitiveId: () => p.id,
		getState_ComponentType: () => 'part',
		getState_Designator: () => p.designator,
	});
	const mkFlag = (f: { id: string; x: number; y: number; net?: string }): any => ({
		getState_PrimitiveId: () => f.id,
		getState_ComponentType: () => 'netflag',
		getState_X: () => f.x,
		getState_Y: () => f.y,
		getState_Net: () => f.net ?? 'GND',
	});
	const mkWire = (w: { id: string; points: Array<number> }): any => ({
		getState_PrimitiveId: () => w.id,
		getState_Line: () => [...w.points],
	});
	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			getAll: async () => [...parts.map(mkPart), ...flags.map(mkFlag)],
			getAllPinsByPrimitiveId: async (id: string) => {
				const p = opts.parts.find(x => x.id === id);
				return (p?.pins ?? []).map(([x, y], i) => ({
					getState_PinNumber: () => String(i + 1),
					getState_X: () => x,
					getState_Y: () => y,
				}));
			},
			delete: async (ids: Array<string>) => {
				deleteCalls.push({ kind: 'components', ids: [...ids] });
				parts = parts.filter(p => !ids.includes(p.id));
				flags = flags.filter(f => keepFlags.has(f.id) || !ids.includes(f.id));
				return true;
			},
		},
		sch_PrimitiveWire: {
			getAll: async () => wires.map(mkWire),
			delete: async (ids: Array<string>) => {
				deleteCalls.push({ kind: 'wires', ids: [...ids] });
				wires = wires.filter(w => keepWires.has(w.id) || !ids.includes(w.id));
				return true; // the platform reports success even when it silently kept some
			},
		},
	};
	return { deleteCalls, liveWireIds: () => wires.map(w => w.id), liveFlagIds: () => flags.map(f => f.id) };
}

test('component.delete: exclusive stub tree (wire + flag) is cascade-deleted and verified', async () => {
	const fx = installComponentDeleteStub({
		parts: [{ id: 'u1', designator: 'U1', pins: [[100, 100]] }],
		flags: [{ id: 'f1', x: 100, y: 130 }],
		wires: [{ id: 'w1', points: [100, 100, 100, 130] }],
	});
	try {
		const res: any = await runAction('schematic.component.delete', { primitiveIds: 'u1' });
		assert.equal(res.result.deleted, true);
		assert.deepEqual(res.result.cascaded, { wires: ['w1'], flags: ['f1'] });
		assert.equal(res.result.notApplied, undefined);
		assert.deepEqual(fx.liveWireIds(), [], 'the exclusive stub wire must actually be gone');
		assert.deepEqual(fx.liveFlagIds(), [], 'the riding netflag must actually be gone');
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('component.delete: a tree still touching a SURVIVING part pin is shared — never deleted', async () => {
	const fx = installComponentDeleteStub({
		parts: [
			{ id: 'u1', designator: 'U1', pins: [[100, 100]] },
			{ id: 'r2', designator: 'R2', pins: [[200, 100]] },
		],
		wires: [{ id: 'w1', points: [100, 100, 200, 100] }],
	});
	try {
		const res: any = await runAction('schematic.component.delete', { primitiveIds: ['u1'] });
		assert.equal(res.result.deleted, true);
		assert.deepEqual(res.result.cascaded, { wires: [], flags: [] });
		assert.deepEqual(fx.liveWireIds(), ['w1'], 'the shared wire must survive');
		const wireDeletes = fx.deleteCalls.filter(c => c.kind === 'wires');
		assert.equal(wireDeletes.length, 0, 'no wire delete may even be attempted for a shared tree');
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('component.delete: cascade:false keeps the old behavior (no wire/flag cleanup)', async () => {
	const fx = installComponentDeleteStub({
		parts: [{ id: 'u1', designator: 'U1', pins: [[100, 100]] }],
		flags: [{ id: 'f1', x: 100, y: 130 }],
		wires: [{ id: 'w1', points: [100, 100, 100, 130] }],
	});
	try {
		const res: any = await runAction('schematic.component.delete', { primitiveIds: 'u1', cascade: false });
		assert.equal(res.result.deleted, true);
		assert.equal(res.result.cascaded, undefined, 'cascade:false must not report a cascaded block');
		assert.deepEqual(fx.liveWireIds(), ['w1']);
		assert.deepEqual(fx.liveFlagIds(), ['f1']);
		assert.equal(fx.deleteCalls.filter(c => c.kind === 'wires').length, 0);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('component.delete: a lying cascade delete is reported as notApplied, never claimed removed', async () => {
	const fx = installComponentDeleteStub({
		parts: [{ id: 'u1', designator: 'U1', pins: [[100, 100]] }],
		flags: [{ id: 'f1', x: 100, y: 130 }],
		wires: [{ id: 'w1', points: [100, 100, 100, 130] }],
		keepWireIds: ['w1'],
	});
	try {
		const res: any = await runAction('schematic.component.delete', { primitiveIds: 'u1' });
		assert.equal(res.result.deleted, true, 'the component itself did go away');
		// Only PROVEN-removed ids are claimed; the survivor is structured notApplied (#151).
		assert.deepEqual(res.result.cascaded, { wires: [], flags: ['f1'] });
		assert.equal(res.result.partial, true);
		assert.deepEqual(res.result.notApplied, [{ kind: 'wire', id: 'w1' }]);
		assert.ok((res.warnings ?? []).some((w: string) => /w1/.test(w)), 'the survivor must be named in a warning');
		assert.deepEqual(fx.liveWireIds(), ['w1'], 'fixture sanity: the kept wire is still live');
	}
	finally {
		delete (globalThis as any).eda;
	}
});

// ─── schematic.pin.disconnect (multi-stub sweep + delete verified by re-read) ──
import { schematicPinDisconnect } from './actions';

/**
 * A pin at (100,100) hosting TWO stubs (one flag each) — the shape that exposed
 * the false success: the old locator took the FIRST wire touching the pin and
 * broke, and the delete was never verified, so `disconnected:true` came back
 * with a stub still wired (real machine: R5:1 / R5:2 / C4:2).
 *
 * `keepWireIds` models the platform's lying delete: those ids are kept on the
 * page while the delete call still resolves as success.
 */
function installDisconnectStub(opts: { keepWireIds?: Array<string> } = {}) {
	const keep = new Set(opts.keepWireIds ?? []);
	let wires = [
		{ id: 'w1', line: [100, 100, 100, 130] }, // stub up → flag f1
		{ id: 'w2', line: [100, 100, 70, 100] },  // stub left → flag f2
	];
	let flags = [
		{ id: 'f1', x: 100, y: 130 },
		{ id: 'f2', x: 70, y: 100 },
	];
	const mkWire = (w: { id: string; line: Array<number> }): any => ({
		getState_PrimitiveId: () => w.id,
		getState_Line: () => [...w.line],
	});
	const mkFlag = (f: { id: string; x: number; y: number }): any => ({
		getState_PrimitiveId: () => f.id,
		getState_ComponentType: () => 'netflag',
		getState_X: () => f.x,
		getState_Y: () => f.y,
	});
	const part: any = {
		getState_PrimitiveId: () => 'r5',
		getState_ComponentType: () => 'part',
		getState_Designator: () => 'R5',
		getState_X: () => 100,
		getState_Y: () => 100,
	};
	const deleteCalls: Array<{ kind: string; ids: Array<string> }> = [];
	(globalThis as any).eda = {
		sch_PrimitiveWire: {
			getAll: async () => wires.map(mkWire),
			delete: async (ids: Array<string>) => {
				deleteCalls.push({ kind: 'wires', ids: [...ids] });
				wires = wires.filter(w => keep.has(w.id) || !ids.includes(w.id));
				return true; // the platform reports success even when it silently kept some
			},
		},
		sch_PrimitiveComponent: {
			getAll: async () => [part, ...flags.map(mkFlag)],
			getAllPinsByPrimitiveId: async (id: string) => (id === 'r5'
				? [{ getState_PinNumber: () => '1', getState_X: () => 100, getState_Y: () => 100 }]
				: []),
			delete: async (ids: Array<string>) => {
				deleteCalls.push({ kind: 'components', ids: [...ids] });
				flags = flags.filter(f => !ids.includes(f.id));
				return true;
			},
		},
	};
	return { deleteCalls, liveWireIds: () => wires.map(w => w.id) };
}

test('disconnect: collects EVERY stub on the pin (no first-wire break) and verifies by re-read', async () => {
	const fx = installDisconnectStub();
	try {
		const res: any = await schematicPinDisconnect({ designator: 'R5', pin: '1' });
		assert.equal(res.result.disconnected, true);
		assert.equal(res.result.partial, undefined);
		assert.deepEqual([...res.result.deletedWires].sort(), ['w1', 'w2']);
		assert.deepEqual([...res.result.deletedFlags].sort(), ['f1', 'f2']);
		assert.deepEqual(res.result.notApplied, []);
		assert.deepEqual(res.result.survivedIds, []);
		assert.equal(res.warnings, undefined);
		// Both stubs went through the wire delete in ONE group call.
		const wireDeletes = fx.deleteCalls.filter(c => c.kind === 'wires');
		assert.equal(wireDeletes.length, 1);
		assert.deepEqual([...wireDeletes[0].ids].sort(), ['w1', 'w2']);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('disconnect: a lying platform delete yields structured partial, never disconnected:true', async () => {
	const fx = installDisconnectStub({ keepWireIds: ['w2'] });
	try {
		const res: any = await schematicPinDisconnect({ designator: 'R5', pin: '1' });
		assert.equal(res.result.disconnected, false, 'survivor present → must NOT claim disconnected');
		assert.equal(res.result.partial, true);
		assert.deepEqual(res.result.survivedIds, ['w2']);
		assert.deepEqual(res.result.notApplied, [{ kind: 'wire', id: 'w2' }]);
		// Only ids PROVEN gone are claimed deleted.
		assert.deepEqual(res.result.deletedWires, ['w1']);
		assert.deepEqual([...res.result.deletedFlags].sort(), ['f1', 'f2']);
		assert.equal(res.warnings?.length, 1);
		assert.match(String(res.warnings?.[0] ?? ''), /survived/);
		assert.deepEqual(fx.liveWireIds(), ['w2'], 'fixture sanity: the kept wire is still live');
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('disconnect: wire-id locator stays targeted (single wire) and still verifies', async () => {
	installDisconnectStub();
	try {
		const res: any = await schematicPinDisconnect({ wirePrimitiveId: 'w1' });
		assert.equal(res.result.disconnected, true);
		assert.deepEqual(res.result.deletedWires, ['w1']);
		// Only the flag riding w1 goes with it.
		assert.deepEqual(res.result.deletedFlags, ['f1']);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

// ─── pcb.component.modify / pcb.component.lock (issue #174) ──────────────
// The platform silently ignores unknown modify() keys and can drop lock
// writes while still returning a component object (fake success). These
// tests pin the three defenses: patch normalization (aliases + unknown-key
// rejection), fresh-readback verification, and the setState+done() fallback.

import {
	classifyLockReadback,
	normalizePcbComponentPatch,
	pcbComponentLock,
	pcbComponentModify,
	verifyPcbComponentPatch,
} from './actions';

test('pcb modify patch: locked/lock aliases normalize onto primitiveLock', () => {
	assert.deepEqual(normalizePcbComponentPatch({ locked: false }), { primitiveLock: false });
	assert.deepEqual(normalizePcbComponentPatch({ lock: true }), { primitiveLock: true });
	// Official keys pass through untouched.
	assert.deepEqual(
		normalizePcbComponentPatch({ x: 100, rotation: 90, primitiveLock: true }),
		{ x: 100, rotation: 90, primitiveLock: true },
	);
});

test('pcb modify patch: unknown keys hard-error instead of silently no-opping', () => {
	assert.throws(() => normalizePcbComponentPatch({ loked: false }), (err: any) => {
		assert.equal(err.code, 'MISSING_PAYLOAD_FIELD');
		assert.match(err.message, /loked/);
		assert.match(err.message, /primitiveLock/); // the error teaches the real contract
		return true;
	});
	assert.throws(() => normalizePcbComponentPatch({}), /empty/);
	// Conflicting alias + official key is ambiguous — refuse.
	assert.throws(() => normalizePcbComponentPatch({ locked: false, primitiveLock: true }), /conflicting/);
	// Same value through both spellings is fine.
	assert.deepEqual(normalizePcbComponentPatch({ locked: true, primitiveLock: true }), { primitiveLock: true });
});

test('pcb modify verify: classifies applied / notApplied / unverified from a fresh readback', () => {
	const readback = {
		primitiveId: 'p1', designator: 'H3', name: 'M3', layer: 1,
		x: 500, y: 250, rotation: 90, locked: true, addIntoBom: true,
		manufacturerId: 'X', supplierId: 'C1',
	};
	const v = verifyPcbComponentPatch(
		{ x: 500, rotation: 450, primitiveLock: false, manufacturer: 'ACME', layer: 'BOTTOM' },
		readback,
	);
	assert.deepEqual(v.applied.sort(), ['rotation', 'x']); // 450 ≡ 90 (mod 360)
	assert.deepEqual(v.notApplied, [{ field: 'primitiveLock', expected: false, actual: true }]);
	// manufacturer is not exposed by the serializer; a string layer literal has no trusted name→id table.
	assert.deepEqual(v.unverified.sort(), ['layer', 'manufacturer']);
	// null means "leave blank" — an empty readback matches.
	const v2 = verifyPcbComponentPatch({ name: null }, { ...readback, name: '' });
	assert.deepEqual(v2.applied, ['name']);
});

test('classifyLockReadback trusts only the fresh store state', () => {
	const fresh = new Map<string, boolean>([['a', false], ['b', true]]);
	assert.deepEqual(classifyLockReadback(['a', 'b', 'c'], fresh, false), {
		applied: ['a'],
		notApplied: ['b', 'c'], // 'b' still locked, 'c' vanished from the readback
	});
});

/**
 * Stub of eda.pcb_PrimitiveComponent with an authoritative store. Mock
 * primitives echo staged writes on their own getters (the real platform's
 * echo-input trap) while `get()` always reflects the committed store.
 */
function installPcbLockStub(opts: {
	comps: Array<{ id: string; locked: boolean; x?: number }>;
	dropModifyLock?: boolean;
	dropSetStateLock?: boolean;
}) {
	const store = new Map(opts.comps.map(c => [c.id, {
		primitiveId: c.id, uniqueId: `uq-${c.id}`, designator: `H-${c.id}`, name: 'M3',
		layer: 1, x: c.x ?? 0, y: 0, rotation: 0, locked: c.locked, addIntoBom: true,
		manufacturerId: '', supplierId: '',
	}]));
	const mock = (rec: any) => {
		const staged = { ...rec };
		return {
			getState_PrimitiveId: () => staged.primitiveId,
			getState_UniqueId: () => staged.uniqueId,
			getState_Designator: () => staged.designator,
			getState_Name: () => staged.name,
			getState_Layer: () => staged.layer,
			getState_X: () => staged.x,
			getState_Y: () => staged.y,
			getState_Rotation: () => staged.rotation,
			getState_PrimitiveLock: () => staged.locked,
			getState_AddIntoBom: () => staged.addIntoBom,
			getState_ManufacturerId: () => staged.manufacturerId,
			getState_SupplierId: () => staged.supplierId,
			setState_PrimitiveLock: (v: boolean) => { staged.locked = v; },
			done: async () => {
				const committed = store.get(staged.primitiveId);
				if (committed && !opts.dropSetStateLock) committed.locked = staged.locked;
				return undefined;
			},
		};
	};
	(globalThis as any).eda = {
		pcb_PrimitiveComponent: {
			get: async (ids: string | Array<string>) => {
				if (typeof ids === 'string') {
					const rec = store.get(ids);
					return rec ? mock(rec) : undefined;
				}
				return ids.filter(id => store.has(id)).map(id => mock(store.get(id)));
			},
			modify: async (id: string, patch: Record<string, unknown>) => {
				const rec = store.get(id);
				if (!rec) return undefined;
				for (const [k, v] of Object.entries(patch)) {
					if (k === 'primitiveLock') {
						if (!opts.dropModifyLock) rec.locked = v as boolean;
					}
					else if (k in rec) (rec as any)[k] = v;
				}
				// Echo-input trap: the RETURNED object reflects the request, not the store.
				return mock({ ...rec, locked: 'primitiveLock' in patch ? patch.primitiveLock : rec.locked });
			},
		},
	};
	return store;
}

test('pcb modify: dropped lock write falls back to setState+done and verifies (#174)', async () => {
	const store = installPcbLockStub({ comps: [{ id: 'p1', locked: true }], dropModifyLock: true });
	try {
		const res: any = await pcbComponentModify({ primitiveId: 'p1', patch: { locked: false } });
		assert.equal(res.result.verified, true);
		assert.equal(res.result.lockFallback, true, 'must have taken the setState+done path');
		assert.deepEqual(res.result.applied, ['primitiveLock']);
		assert.equal(res.result.component.locked, false);
		assert.equal(store.get('p1')!.locked, false, 'the store (survives reload) is unlocked');
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('pcb modify: full no-op (both lock paths dropped) is an ERROR, not ok:true (#174)', async () => {
	installPcbLockStub({ comps: [{ id: 'p1', locked: true }], dropModifyLock: true, dropSetStateLock: true });
	try {
		await assert.rejects(
			() => pcbComponentModify({ primitiveId: 'p1', patch: { locked: false } }),
			(err: any) => {
				assert.equal(err.code, 'EDA_CALL_FAILED');
				assert.match(err.message, /no patched field was applied/);
				return true;
			},
		);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('pcb modify: partial application returns ok with structured notApplied (#151)', async () => {
	installPcbLockStub({ comps: [{ id: 'p1', locked: true, x: 100 }], dropModifyLock: true, dropSetStateLock: true });
	try {
		const res: any = await pcbComponentModify({ primitiveId: 'p1', patch: { x: 500, locked: false } });
		assert.equal(res.result.verified, false);
		assert.deepEqual(res.result.applied, ['x']); // the canvas DID change — never throw
		assert.equal(res.result.notApplied.length, 1);
		assert.equal(res.result.notApplied[0].field, 'primitiveLock');
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('pcb lock: batch unlock applies, verifies via fresh readback, reports missing ids', async () => {
	const store = installPcbLockStub({ comps: [
		{ id: 'a', locked: true }, { id: 'b', locked: true }, { id: 'c', locked: false },
	] });
	try {
		const res: any = await pcbComponentLock({ primitiveIds: ['a', 'b', 'c', 'ghost'], locked: false });
		assert.deepEqual(res.result.applied.sort(), ['a', 'b']);
		assert.deepEqual(res.result.alreadyInState, ['c']);
		assert.deepEqual(res.result.missing, ['ghost']);
		assert.deepEqual(res.result.notApplied, []);
		assert.equal(res.result.verified, true);
		assert.equal(store.get('a')!.locked, false);
		assert.equal(store.get('b')!.locked, false);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('pcb lock: a write that does not stick is an ERROR when nothing changed (#174)', async () => {
	installPcbLockStub({ comps: [{ id: 'a', locked: true }], dropSetStateLock: true });
	try {
		await assert.rejects(
			() => pcbComponentLock({ primitiveIds: ['a'], locked: false }),
			(err: any) => {
				assert.equal(err.code, 'EDA_CALL_FAILED');
				assert.match(err.message, /did not stick/);
				return true;
			},
		);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

test('pcb lock: already-in-state components are idempotent success, not rewrites', async () => {
	installPcbLockStub({ comps: [{ id: 'a', locked: false }] });
	try {
		const res: any = await pcbComponentLock({ primitiveIds: ['a'], locked: false });
		assert.deepEqual(res.result.alreadyInState, ['a']);
		assert.deepEqual(res.result.applied, []);
		assert.equal(res.result.verified, true);
	}
	finally {
		delete (globalThis as any).eda;
	}
});

// ─── #183 phase 1: polarity-convention-outlier ─────────────────────────────

function polarityCap(designator: string, pin1Net: string, pin2Net: string) {
	return {
		designator,
		primitiveId: `prim-${designator}`,
		pins: [
			{ number: '1', net: pin1Net },
			{ number: '2', net: pin2Net },
		],
	};
}

test('polarity classifiers: GND family vs power rails vs unclassified signals', () => {
	for (const n of ['GND', 'gnd', 'AGND', 'DGND', 'PGND', 'VSS', 'Earth_1', 'GROUND']) {
		assert.ok(isGroundLikeNet(n), `${n} should classify as ground`);
		assert.ok(!isPowerRailNet(n), `${n} should not classify as a power rail`);
	}
	for (const n of ['+5V', '3V3', '12V0', 'VCC', 'VDD', 'VBUS', 'VBAT_RAW', 'VSYS_5V', 'vcc']) {
		assert.ok(isPowerRailNet(n), `${n} should classify as a power rail`);
		assert.ok(!isGroundLikeNet(n), `${n} should not classify as ground`);
	}
	for (const n of ['SW1_NODE', 'EN', 'TXD', 'USB_DP', 'SCL', 'VEN', '']) {
		assert.ok(!isGroundLikeNet(n) && !isPowerRailNet(n), `${JSON.stringify(n)} must stay unclassified`);
	}
});

test('polarity: 8:1 page flags exactly the single reversed cap (#183)', () => {
	const cands = [];
	for (let i = 1; i <= 8; i++) cands.push(polarityCap(`C${i}`, '+3V3', 'GND'));
	cands.push(polarityCap('C9', 'GND', '+3V3')); // the reversed tantalum from the incident
	const out = detectPolarityConventionOutliers(cands);
	assert.equal(out.length, 1);
	assert.equal(out[0].designator, 'C9');
	assert.equal(out[0].powerPin, '2');
	assert.equal(out[0].gndPin, '1');
	assert.equal(out[0].powerNet, '+3V3');
	assert.equal(out[0].gndNet, 'GND');
	assert.equal(out[0].majorityPowerPin, '1');
	assert.equal(out[0].majorityCount, 8);
	assert.equal(out[0].totalMatched, 9);
});

test('polarity: unanimous page stays silent', () => {
	const cands = [];
	for (let i = 1; i <= 5; i++) cands.push(polarityCap(`C${i}`, '+3V3', 'GND'));
	assert.deepEqual(detectPolarityConventionOutliers(cands), []);
});

test('polarity: below minimum group stays silent (no convention to violate)', () => {
	const out = detectPolarityConventionOutliers([
		polarityCap('C1', '+3V3', 'GND'),
		polarityCap('C2', 'GND', '+3V3'),
	]);
	assert.deepEqual(out, []);
});

test('polarity: tie stays silent', () => {
	const out = detectPolarityConventionOutliers([
		polarityCap('C1', '+3V3', 'GND'),
		polarityCap('C2', '+3V3', 'GND'),
		polarityCap('C3', 'GND', '+3V3'),
		polarityCap('C4', 'GND', '+3V3'),
	]);
	assert.deepEqual(out, []);
});

test('polarity: weak majority stays silent (--strict promotes WARNs — no coin flips)', () => {
	const cands = [
		polarityCap('C1', '+3V3', 'GND'),
		polarityCap('C2', '+3V3', 'GND'),
		polarityCap('C3', '+3V3', 'GND'),
		polarityCap('C4', '+3V3', 'GND'),
		polarityCap('C5', 'GND', '+3V3'),
		polarityCap('C6', 'GND', '+3V3'),
		polarityCap('C7', 'GND', '+3V3'),
	];
	// 4:3 — technically a majority but far under the 75% supermajority bar.
	assert.deepEqual(detectPolarityConventionOutliers(cands), []);
});

test('polarity: series/signal caps never enter the convention statistics', () => {
	const cands = [];
	for (let i = 1; i <= 6; i++) cands.push(polarityCap(`C${i}`, '+3V3', 'GND'));
	cands.push(polarityCap('C20', 'GND', '+3V3')); // would-be outlier
	cands.push(polarityCap('C21', 'AUDIO_IN', 'BIAS')); // coupling cap — no rail meaning
	cands.push(polarityCap('C22', 'EN', 'KEY_ROW')); // ditto
	const out = detectPolarityConventionOutliers(cands);
	assert.equal(out.length, 1);
	assert.equal(out[0].designator, 'C20');
	assert.equal(out[0].totalMatched, 7, 'coupling caps must be excluded from totalMatched');
});

test('sch check: polarity-convention-outlier fires on the #183 nine-cap page (handler wiring)', async () => {
	const caps: Array<{ id: string; designator: string; pinNets: Array<[string, string]> }> = [];
	for (let i = 1; i <= 8; i++) caps.push({ id: `c${i}`, designator: `C${i}`, pinNets: [['1', '+3V3'], ['2', 'GND']] });
	caps.push({ id: 'c9', designator: 'C9', pinNets: [['1', 'GND'], ['2', '+3V3']] });
	caps.push({ id: 'cn1', designator: 'CN1', pinNets: [['1', 'GND'], ['2', '+3V3']] }); // C+非数字:电源端子不得进电容票仓
	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			getAll: async () => caps.map(c => ({
				getState_ComponentType: () => 'component',
				getState_PrimitiveId: () => c.id,
				getState_Designator: () => c.designator,
			})),
			getAllPinsByPrimitiveId: async (pid: string) => {
				const c = caps.find(x => x.id === pid)!;
				return c.pinNets.map(([num], idx) => ({
					getState_PinNumber: () => num,
					getState_X: () => 100 * (idx + 1),
					getState_Y: () => 100,
				}));
			},
		},
		sch_PrimitiveWire: { getAll: async () => [] },
		sch_ManufactureData: {
			getNetlistFile: async () => ({
				text: async () => JSON.stringify({
					components: Object.fromEntries(caps.map(c => [`comp-${c.id}`, {
						props: { Designator: c.designator },
						pinInfoMap: Object.fromEntries(c.pinNets.map(([num, net], idx) => [`p${idx}`, { number: num, net }])),
					}])),
				}),
			}),
		},
	};
	try {
		const res: any = await runAction('schematic.check', {});
		const pol = res.result.findings.filter((f: any) => f.type === 'polarity-convention-outlier');
		assert.equal(pol.length, 1);
		assert.equal(pol[0].designator, 'C9');
		assert.deepEqual(pol[0].pins, ['2', '1']);
		assert.equal(res.result.summary.polarityConventionOutliers, 1);
		assert.equal(res.result.summary.total, 1, 'a fully-wired page must produce ONLY the polarity finding');
	}
	finally {
		delete (globalThis as any).eda;
	}
});

// ── planOtherPropertyBackfill (#186) ────────────────────────────────────────
//
// The real device record for a C0805 (live-read 2026-08-25) — note that it
// carries BOTH the values we want and the two landmines: a placeholder
// `Designator: "C?"` and a projection template `Name: "={Value}"`.
const DEVICE_OP_C0805 = {
	'Datasheet': 'https://item.szlcsc.com/datasheet/GRM21BR61H106KE43L/439567.html',
	'Description': '容值:10uF;精度:±10%;额定电压:50V;温度系数:X5R;',
	'Designator': 'C?',
	'JLCPCB Part Class': 'Basic Part',
	'LCSC Part Name': '10uF ±10% 50V',
	'Manufacturer': 'muRata(村田)',
	'Manufacturer Part': 'GRM21BR61H106KE43L',
	'Name': '={Value}',
	'Supplier': 'LCSC',
	'Supplier Part': 'C440198',
	'Temperature Coefficient': 'X5R',
	'Tolerance': '±10%',
	'Value': '10uF',
	'Voltage Rating': '50V',
	'3D Model': '1ba041120af144c991958decab20d241',
	'Footprint': 'ccb32feceadc4298b406326a506ce8e7',
};

// What the platform leaves on a freshly placed instance: the keys are there,
// every value is empty (this is the #186 defect being fixed).
const FRESH_INSTANCE_OP = {
	'Datasheet': '',
	'Description': '',
	'JLCPCB Part Class': '',
	'LCSC Part Name': '',
	'Supplier Footprint': '',
	'Temperature Coefficient': '',
	'Tolerance': '',
	'Value': '',
	'Voltage Rating': '',
};

test('planOtherPropertyBackfill: fills the empty values a fresh instance carries', () => {
	const { merged, filled } = planOtherPropertyBackfill(FRESH_INSTANCE_OP, DEVICE_OP_C0805, { onlyExistingKeys: true });
	assert.equal(merged['Value'], '10uF');
	assert.equal(merged['Tolerance'], '±10%');
	assert.equal(merged['Voltage Rating'], '50V');
	assert.equal(merged['Temperature Coefficient'], 'X5R');
	assert.ok(filled.includes('Value'), 'Value must be reported as filled');
	// A key the device has no value for is left untouched, not invented.
	assert.equal(merged['Supplier Footprint'], '');
});

test('planOtherPropertyBackfill: NEVER writes projected-state keys (the 166/166 designator wipe)', () => {
	const { merged, filled } = planOtherPropertyBackfill(FRESH_INSTANCE_OP, DEVICE_OP_C0805, { onlyExistingKeys: true });
	for (const key of PROJECTED_STATE_KEYS) {
		assert.ok(!(key in merged), `projected key ${key} must never be merged in`);
		assert.ok(!filled.includes(key), `projected key ${key} must never be reported as filled`);
	}
	// The placeholder that caused the wipe specifically.
	assert.equal(merged['Designator'], undefined);
});

test('planOtherPropertyBackfill: onlyExistingKeys refuses to introduce new keys', () => {
	const { merged } = planOtherPropertyBackfill(FRESH_INSTANCE_OP, DEVICE_OP_C0805, { onlyExistingKeys: true });
	// Present on the device record, absent from the instance ⇒ must stay absent.
	assert.ok(!('3D Model' in merged), '3D Model must not be introduced');
	assert.ok(!('Footprint' in merged), 'Footprint must not be introduced');
	assert.deepEqual(Object.keys(merged).sort(), Object.keys(FRESH_INSTANCE_OP).sort());
});

test('planOtherPropertyBackfill: never overwrites a value the instance already has', () => {
	const edited = { ...FRESH_INSTANCE_OP, 'Value': '22uF (hand-picked)' };
	const { merged, filled } = planOtherPropertyBackfill(edited, DEVICE_OP_C0805, { onlyExistingKeys: true });
	assert.equal(merged['Value'], '22uF (hand-picked)');
	assert.ok(!filled.includes('Value'));
});

test('planOtherPropertyBackfill: idempotent — a second pass fills nothing', () => {
	const first = planOtherPropertyBackfill(FRESH_INSTANCE_OP, DEVICE_OP_C0805, { onlyExistingKeys: true });
	const second = planOtherPropertyBackfill(first.merged, DEVICE_OP_C0805, { onlyExistingKeys: true });
	assert.deepEqual(second.filled, [], 'nothing left to fill on the second run');
});

test('planOtherPropertyBackfill: scrubs a stale placeholder Designator leaked by an older backfill', () => {
	const poisoned = { ...FRESH_INSTANCE_OP, 'Designator': 'C?' };
	const { merged, filled } = planOtherPropertyBackfill(poisoned, DEVICE_OP_C0805, { onlyExistingKeys: true });
	assert.equal(merged['Designator'], undefined, 'stale placeholder must be removed');
	assert.ok(filled.some(f => f.startsWith('Designator')), 'the scrub must be reported');
});

test('planOtherPropertyBackfill: a real designator in otherProperty is left alone', () => {
	// Only '?'-bearing placeholders are scrubbed — a real value is not ours to delete.
	const withReal = { ...FRESH_INSTANCE_OP, 'Designator': 'C9' };
	const { merged } = planOtherPropertyBackfill(withReal, DEVICE_OP_C0805, { onlyExistingKeys: true });
	assert.equal(merged['Designator'], 'C9');
});

// ── PCB length constraints (#176) ───────────────────────────────────────────
//
// Since EDA v3.4 `getAllDifferentialPairs` may hand back an object MAP instead
// of an array (a documented breaking change). Both the report and the new
// constraint handlers run every read through constraintList, so a shape change
// on the platform side must not turn into "the board has no constraints".
test('constraintList: normalizes both the array and the v3.4 object-map shape', () => {
	const asArray = [{ name: 'USB', positiveNet: 'DP', negativeNet: 'DM' }];
	const asMap = { USB: { name: 'USB', positiveNet: 'DP', negativeNet: 'DM' } };
	assert.deepEqual(constraintList(asArray), asArray);
	assert.deepEqual(constraintList(asMap), asArray);
});

test('constraintList: nullish and empty inputs read as an empty list, never throw', () => {
	assert.deepEqual(constraintList(undefined), []);
	assert.deepEqual(constraintList(null), []);
	assert.deepEqual(constraintList([]), []);
	assert.deepEqual(constraintList({}), []);
});

// 写后回读必须等落定 —— 平台提交明细表是异步的(#186 复验)。
//
// 真机实测:把 Name 写成 "TB-BOOL-TEST" 的调用回执报 `nothing was applied`,
// 三秒后再读值就在那儿。这条误报让「图签写不进去」成了流程里的既定结论,
// 而事实是写成功了、只是读早了。
test('titleblock: 慢落定的写不再被误报成 nothing-applied (#186)', async () => {
	let reads = 0;
	const calls: Array<unknown> = [];
	(globalThis as any).eda = {
		dmt_Schematic: {
			getCurrentSchematicPageInfo: async () => {
				reads += 1;
				// 第 1 次 = 改前快照;第 2 次 = 写后立刻读(平台还没提交,仍是旧值);
				// 第 3 次起才看到新值 —— 正是真机观察到的形态。
				const landed = reads >= 3;
				return {
					uuid: 'page-1',
					name: 'Page1',
					showTitleBlock: true,
					titleBlockData: { Title: { showTitle: true, showValue: true, value: landed ? 'new' : 'old' } },
				};
			},
			modifySchematicPageTitleBlock: async (show: unknown, data: unknown) => {
				calls.push({ show, data });
				return true;
			},
		},
	};
	const res: any = await schematicTitleBlockModify({
		titleBlockData: { Title: { showTitle: true, showValue: true, value: 'new' } },
	});
	assert.equal(res.result.ok, true, '慢落定不能报成失败');
	assert.deepEqual(res.result.applied, ['Title']);
	assert.equal(res.result.partial, undefined, '落定之后不是 partial');
	assert.equal(calls.length, 1, '只写一次 —— 重试的是读,不是写');
	delete (globalThis as any).eda;
});

// 反向:真的没生效时,轮询完仍要如实报 notApplied,不能把等待变成粉饰。
test('titleblock: 始终不生效的项在轮询后仍如实报失败 (#186)', async () => {
	installTitleBlockStub({
		before: { Title: { value: 'old' } },
		after: { Title: { value: 'old' } },
	});
	await assert.rejects(
		() => schematicTitleBlockModify({
			titleBlockData: { Title: { showTitle: true, showValue: true, value: 'new' } },
		}) as any,
		(err: any) => {
			assert.match(err.message, /nothing was applied/);
			return true;
		},
	);
	delete (globalThis as any).eda;
});

// ─── resolve_lcsc footprint matching (T-3 / T-16) ─────────────────────────

/** Build the eda mock resolve_lcsc needs: one part + a two-variant MPN hit. */
function resolveLcscEda(instanceFootprint: Record<string, unknown>): any {
	const part = mockComponent({
		PrimitiveId: 'p-r8',
		ComponentType: 'part',
		Designator: 'R8',
		Name: 'RC0603FR-0710KL',
		ManufacturerId: 'RC0603FR-0710KL',
		Supplier: '',
		SupplierId: '',
		Footprint: instanceFootprint,
	});
	return {
		sch_PrimitiveComponent: { getAll: async () => [part] },
		lib_Device: {
			getByLcscIds: async () => [],
			search: async () => [
				{ uuid: 'DEV-0603', libraryUuid: 'LIB-1', name: 'RC0603FR-0710KL', manufacturerId: 'RC0603FR-0710KL', supplierId: 'C98220', footprintName: 'R0603', footprintUuid: 'FP-R0603' },
				{ uuid: 'DEV-0805', libraryUuid: 'LIB-1', name: 'RC0805FR-0710KL', manufacturerId: 'RC0603FR-0710KL', supplierId: 'C17414', footprintName: 'R0805', footprintUuid: 'FP-R0805' },
			],
		},
	};
}

test('resolve_lcsc: a lower-cased instance footprint matches the library record (T-3)', async () => {
	(globalThis as any).eda = resolveLcscEda({ libraryUuid: 'LIB-F', uuid: '', name: 'r0603' });
	try {
		const res: any = await runAction('schematic.component.resolve_lcsc', {});
		assert.equal(res.result.unresolvedCount, 0);
		assert.equal(res.result.items[0].lcsc, 'C98220');
		assert.equal(res.result.items[0].via, 'mpn');
	}
	finally { delete (globalThis as any).eda; }
});

test('resolve_lcsc: a matching footprint uuid outranks a differently-named library record', async () => {
	(globalThis as any).eda = resolveLcscEda({ libraryUuid: 'LIB-F', uuid: 'FP-R0603', name: 'resistor-0603-local' });
	try {
		const res: any = await runAction('schematic.component.resolve_lcsc', {});
		assert.equal(res.result.unresolvedCount, 0);
		assert.equal(res.result.items[0].lcsc, 'C98220');
	}
	finally { delete (globalThis as any).eda; }
});

test('resolve_lcsc: a REAL package-variant mismatch is still refused', async () => {
	(globalThis as any).eda = resolveLcscEda({ libraryUuid: 'LIB-F', uuid: '', name: 'r1206' });
	try {
		const res: any = await runAction('schematic.component.resolve_lcsc', {});
		assert.equal(res.result.unresolvedCount, 1);
		assert.match(String(res.result.unresolved[0].reason), /package-variant mismatch/);
	}
	finally { delete (globalThis as any).eda; }
});

test('resolve_lcsc: a footprint UUID without a name still selects the matching variant', async () => {
	(globalThis as any).eda = resolveLcscEda({ uuid: 'FP-R0603' });
	try {
		const res: any = await runAction('schematic.component.resolve_lcsc', {});
		assert.equal(res.result.unresolvedCount, 0);
		assert.equal(res.result.items[0].lcsc, 'C98220');
	}
	finally { delete (globalThis as any).eda; }
});

test('resolve_lcsc: a lone wrong UUID is refused before apply even when the instance name is missing', async () => {
	const mock = resolveLcscEda({ uuid: 'FP-MISSING' });
	const hits = await mock.lib_Device.search();
	mock.lib_Device.search = async () => hits.slice(0, 1);
	let writes = 0;
	mock.sch_PrimitiveComponent.modify = async () => { writes++; return true; };
	(globalThis as any).eda = mock;
	try {
		const res: any = await runAction('schematic.component.resolve_lcsc', { apply: true });
		assert.equal(res.result.unresolvedCount, 1);
		assert.match(res.result.unresolved[0].reason, /FP-MISSING.*package-variant mismatch/);
		assert.equal(writes, 0);
	}
	finally { delete (globalThis as any).eda; }
});

test('resolve_lcsc: unresolved candidates expose the conflicting footprint identities, not only equal names', async () => {
 const mock = resolveLcscEda({ uuid: 'FP-INSTANCE', libraryUuid: 'LIB-F', name: 'R0603' });
 const hits = await mock.lib_Device.search();
 mock.lib_Device.search = async () => [{ ...hits[0], footprint: { uuid: 'FP-LIBRARY', libraryUuid: 'LIB-F', name: 'R0603' } }];
 (globalThis as any).eda = mock;
 try {
  const res: any = await runAction('schematic.component.resolve_lcsc', {});
  assert.equal(res.result.unresolvedCount, 1, 'same-name differing identities must still refuse');
  assert.match(res.result.unresolved[0].reason, /instance footprint uuid="FP-INSTANCE", libraryUuid="LIB-F"/);
  assert.deepEqual(res.result.unresolved[0].candidates[0], {
   name: 'RC0603FR-0710KL', lcsc: 'C98220', uuid: 'DEV-0603', libraryUuid: 'LIB-1',
   footprintName: 'R0603', footprintUuid: 'FP-LIBRARY', footprintLibraryUuid: 'LIB-F',
  });
 } finally { delete (globalThis as any).eda; }
});

test('resolve_lcsc: current SDK nested names use the same trimmed case-insensitive fallback', async () => {
	const mock = resolveLcscEda({ name: ' r0603 ' });
	const hits = await mock.lib_Device.search();
	mock.lib_Device.search = async () => hits.map(({ footprintName, footprintUuid, ...hit }: any) => ({
		...hit, footprint: { name: footprintName, uuid: footprintUuid, libraryUuid: 'LIB-F' },
	}));
	(globalThis as any).eda = mock;
	try {
		const res: any = await runAction('schematic.component.resolve_lcsc', {});
		assert.equal(res.result.unresolvedCount, 0);
		assert.equal(res.result.items[0].lcsc, 'C98220');
	}
	finally { delete (globalThis as any).eda; }
});

// Captured identity shape from the Hongen U1 page: public-library provenance,
// but project-instance (16-hex) device/footprint IDs. No editor is used here.
const identityLibrary = '0819f05c4eef4c71ace90d822a990e87';
const identityDevice = '9f9c6cb41c7449fd8acf96aceed2661a';
const identityFootprint = '20c29e37a9b84b4197418483096f9c05';
const identityPackage = 'SOT-223-3_L6.5-W3.4-P2.30-LS7.0-BR';

function nativeIdentitySource(instanceUuid: string, uuid: string, libraryUuid = identityLibrary): any {
	return {
		footprintUuid: instanceUuid,
		documentSource: `${JSON.stringify({ type: 'DOCHEAD' })}||${JSON.stringify({ docType: 'FOOTPRINT', uuid: instanceUuid })}|\n`
			+ `${JSON.stringify({ type: 'META', id: 'META' })}||${JSON.stringify({ title: identityPackage, source: `${uuid}|${libraryUuid}` })}|\n`,
	};
}

function instanceIdentityEda(): any {
	const state: Record<string, unknown> = {
		ComponentType: 'part', Designator: 'U1', Name: '={Manufacturer Part}',
		ManufacturerId: 'AMS1117-3.3', SupplierId: 'C6186',
		Component: { uuid: '6e8a0f3cb342d055', libraryUuid: identityLibrary, name: 'AMS1117-3.3_C6186' },
		Footprint: { uuid: 'abe23dba1def1246', libraryUuid: identityLibrary, name: identityPackage },
	};
	const hit: Record<string, unknown> = {
		uuid: identityDevice, libraryUuid: identityLibrary, supplierId: 'C6186', manufacturerId: 'AMS1117-3.3',
		footprintUuid: identityFootprint, footprintName: identityPackage,
	};
	const detail: Record<string, unknown> = {
		uuid: identityDevice, libraryUuid: identityLibrary, name: 'AMS1117-3.3_C6186',
		property: { supplierId: 'C6186', manufacturerId: 'AMS1117-3.3' },
		association: { footprint: { uuid: identityFootprint, libraryUuid: identityLibrary } },
	};
	const calls: unknown[] = [];
	const nativeSources = [nativeIdentitySource('abe23dba1def1246', identityFootprint)];
	return {
		state, hit, detail, calls, nativeSources,
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'project-identity-test' }) },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: '1234567890abcdef' }) },
		sys_FileManager: { getDocumentFootprintSources: async () => { calls.push(['getDocumentFootprintSources']); return nativeSources; } },
		sch_PrimitiveComponent: { getAll: async () => [mockComponent(state)] },
		lib_Device: {
			getByLcscIds: async (...args: unknown[]) => { calls.push(['getByLcscIds', ...args]); return [hit]; },
			get: async (...args: unknown[]) => { calls.push(['get', ...args]); return detail; },
			search: async () => { throw new Error('must not replace an incomplete exact-LCSC inventory with a search'); },
		},
	};
}

async function readInstanceIdentity(mock: any): Promise<any> {
	(globalThis as any).eda = mock;
	try {
		const res: any = await schematicComponentsList({ includeDeviceIdentity: true });
		return res.result.components[0];
	} finally { delete (globalThis as any).eda; }
}

test('device identity: 16-hex project footprint resolves only with native source + full LCSC + official association', async () => {
	const mock = instanceIdentityEda();
	(mock.state.Footprint as any).name = ` ${identityPackage.toLowerCase()} `;
	const part = await readInstanceIdentity(mock);
	assert.equal(part.device.uuid, identityDevice);
	assert.equal(part.placedDevice.uuid, '6e8a0f3cb342d055');
	assert.equal(part.footprint.uuid, 'abe23dba1def1246', 'instance identity is retained, never overwritten with an inferred asset');
	assert.equal(part.deviceResolution.via, 'lcsc-footprint-source');
	assert.equal(part.deviceResolution.sameFootprintUUID, true);
	assert.deepEqual(part.deviceResolution.footprintSource, { instanceUuid: 'abe23dba1def1246', uuid: identityFootprint, libraryUuid: identityLibrary });
	assert.deepEqual(mock.calls, [
		['getDocumentFootprintSources'],
		['getByLcscIds', ['C6186'], undefined, true],
		['get', identityDevice, identityLibrary],
	]);
});

test('device identity: Hongen C2 flattened search footprint gains asset-library evidence from device.get', async () => {
	const mock = instanceIdentityEda();
	mock.state.Designator = 'C2'; mock.state.Name = '={Value}'; mock.state.ManufacturerId = 'GRM21BR61H106KE43L'; mock.state.SupplierId = 'C440198';
	mock.state.Component = { uuid: 'dc16e8d0f259b0b7', libraryUuid: identityLibrary, name: 'GRM21BR61H106KE43L' };
	mock.state.Footprint = { uuid: '6f7ca9a9603aeb19', libraryUuid: identityLibrary, name: 'C0805' };
	mock.nativeSources.splice(0, 1, nativeIdentitySource('6f7ca9a9603aeb19', 'ccb32feceadc4298b406326a506ce8e7'));
	Object.assign(mock.hit, { uuid: '6e5726223dd84f70bc3b626fc7d1f72c', supplierId: 'C440198', manufacturerId: 'GRM21BR61H106KE43L', footprintName: 'C0805', footprintUuid: 'ccb32feceadc4298b406326a506ce8e7' });
	Object.assign(mock.detail, { uuid: mock.hit.uuid, property: { supplierId: 'C440198', manufacturerId: 'GRM21BR61H106KE43L' }, association: { footprint: { uuid: mock.hit.footprintUuid, libraryUuid: identityLibrary } } });
	const part = await readInstanceIdentity(mock);
	assert.equal(part.device.uuid, mock.hit.uuid);
	assert.equal(part.deviceResolution.via, 'lcsc-footprint-source');
});

test('device identity: exact native footprint source outranks a renamed package label', async () => {
	const mock = instanceIdentityEda(); mock.hit.footprintName = 'different-authored-display-name';
	assert.equal((await readInstanceIdentity(mock)).device.uuid, identityDevice);
});

test('device identity: native inventory is fetched once per list and refreshed on the next call', async () => {
	const mock = instanceIdentityEda();
	mock.sch_PrimitiveComponent.getAll = async () => [mockComponent(mock.state), mockComponent({ ...mock.state, Designator: 'U2' })];
	await readInstanceIdentity(mock); await readInstanceIdentity(mock);
	assert.equal(mock.calls.filter((c: any) => c[0] === 'getDocumentFootprintSources').length, 2);
	assert.equal(mock.calls.filter((c: any) => c[0] === 'getByLcscIds').length, 2, 'identical parts share proof only within one request');
	assert.equal(mock.calls.filter((c: any) => c[0] === 'get').length, 2);
});

test('device identity: cache never merges different instance provenance or model evidence', async () => {
	for (const change of [
		{ Footprint: { uuid: '0123456789abcdef', libraryUuid: identityLibrary, name: identityPackage } },
		{ ManufacturerId: 'other-model' },
		{ Name: 'other-name' },
		{ Component: { uuid: 'abcdef0123456789', libraryUuid: identityLibrary, name: 'other-source' } },
	]) {
		const mock = instanceIdentityEda();
		mock.sch_PrimitiveComponent.getAll = async () => [mockComponent(mock.state), mockComponent({ ...mock.state, ...change, Designator: 'U2' })];
		(globalThis as any).eda = mock;
		try {
			const res: any = await schematicComponentsList({ includeDeviceIdentity: true });
			const second = res.result.components[1];
			if ('Footprint' in change || 'ManufacturerId' in change) assert.ok(second.deviceIdentityError);
			else assert.equal(mock.calls.filter((c: any) => c[0] === 'getByLcscIds').length, 2);
		} finally { delete (globalThis as any).eda; }
	}
});

test('device identity: hung candidate query reports its stage without hydrating or waiting forever', async () => {
	const mock = instanceIdentityEda();
	let started!: () => void;
	const entered = new Promise<void>(resolve => { started = resolve; });
	mock.lib_Device.getByLcscIds = () => { started(); return new Promise(() => {}); };
	const read = readInstanceIdentity(mock);
	await entered;
	sweepDeadlines(Date.now() + 8000);
	const part = await read;
	assert.match(part.deviceIdentityError, /getByLcscIds.*timed out/);
	assert.equal(part.device.uuid, '6e8a0f3cb342d055');
	assert.equal(part.deviceResolution, undefined);
});

test('device identity: schematic empty source API falls back to the official current-project epro2 archive', async () => {
	const mock = instanceIdentityEda();
	const zip = new JSZip();
	zip.file('current-project.epru', mock.nativeSources[0].documentSource + '{"type":"DOCHEAD"}||{"docType":"SCH_PAGE","uuid":"1234567890abcdef"}|\n');
	const file = new Blob([await zip.generateAsync({ type: 'arraybuffer', compression: 'DEFLATE' })]);
	mock.sys_FileManager.getDocumentFootprintSources = async () => [];
	mock.sys_FileManager.getProjectFile = async (...args: unknown[]) => { mock.calls.push(['getProjectFile', ...args]); return file; };
	const part = await readInstanceIdentity(mock);
	assert.equal(part.device.uuid, identityDevice);
	assert.equal(part.deviceResolution.via, 'lcsc-footprint-source');
	assert.deepEqual(mock.calls[0], ['getProjectFile', 'easyeda-agent-identity.epro2', undefined, 'epro2']);
});

test('device identity: stable exact device name is allowed only when the instance has no MPN', async () => {
	const mock = instanceIdentityEda(); mock.state.ManufacturerId = '';
	delete mock.hit.manufacturerId; mock.hit.name = 'AMS1117-3.3_C6186'; delete mock.detail.property.manufacturerId;
	const part = await readInstanceIdentity(mock);
	assert.equal(part.device.uuid, identityDevice);
});

test('device identity: duplicate rows for one proven library device do not invent a second candidate', async () => {
	const mock = instanceIdentityEda(); mock.lib_Device.getByLcscIds = async () => [mock.hit, { ...mock.hit }];
	assert.equal((await readInstanceIdentity(mock)).device.uuid, identityDevice);
});

test('device identity: complete LCSC inventory with two proven devices stays ambiguous', async () => {
	const mock = instanceIdentityEda(); const second = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
	mock.lib_Device.getByLcscIds = async (_: unknown, __: unknown, multi: boolean) => multi ? [mock.hit, { ...mock.hit, uuid: second }] : [mock.hit];
	mock.lib_Device.get = async (uuid: string) => ({ ...mock.detail, uuid });
	const part = await readInstanceIdentity(mock);
	assert.equal(part.device.uuid, '6e8a0f3cb342d055');
	assert.match(part.deviceIdentityError, /2 distinct devices.*ambiguous/);
	assert.equal(part.deviceIdentityCandidates.length, 2);
});

const invalidInstanceIdentityCases: Array<[string, (mock: any) => void]> = [
	['different 32-hex footprint UUIDs even with the same package name', m => { m.state.Footprint.uuid = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'; }],
	['unclassified device UUID domain', m => { m.state.Component.uuid = 'unclassified'; }],
	['candidate footprint is not 32 hex', m => { m.hit.footprintUuid = 'abe23dba1def1246'; }],
	['missing source footprint asset library', m => { delete m.state.Footprint.libraryUuid; }],
	['wrong source footprint asset library', m => { m.state.Footprint.libraryUuid = 'different-asset-library'; }],
	['same asset UUID with conflicting candidate library', m => { m.hit.footprintLibraryUuid = 'different-asset-library'; }],
	['native source identifies a different asset despite equal package names', m => { m.nativeSources[0] = nativeIdentitySource('abe23dba1def1246', 'cccccccccccccccccccccccccccccccc'); }],
	['native source library conflicts despite equal package names', m => { m.nativeSources[0] = nativeIdentitySource('abe23dba1def1246', identityFootprint, 'different-library'); }],
	['native source API is absent; name-only evidence cannot hydrate', m => { delete m.sys_FileManager; }],
	['native source query fails; name-only evidence cannot hydrate', m => { m.sys_FileManager.getDocumentFootprintSources = async () => { throw new Error('source unavailable'); }; }],
	['native source inventory is empty', m => { m.nativeSources.length = 0; }],
	['native source document is malformed', m => { m.nativeSources[0].documentSource = 'not native source'; }],
	['source project context is missing', m => { delete m.dmt_Project; }],
	['source page changes after component snapshot', m => {
		let reads = 0;
		m.dmt_SelectControl.getCurrentDocumentInfo = async () => ({ uuid: ++reads === 1 ? '1234567890abcdef' : 'other-page' });
	}],
	['source project changes during export', m => {
		let project = 'project-identity-test';
		m.dmt_Project.getCurrentProjectInfo = async () => ({ uuid: project });
		m.sys_FileManager.getDocumentFootprintSources = async () => { project = 'other-project'; return m.nativeSources; };
	}],
	['search LCSC contradicts the exact requested C-number', m => { m.hit.supplierId = 'C9999'; }],
	['search MPN mismatch is not rescued by same name', m => { m.hit.manufacturerId = 'ams1117-3.3'; }],
	['official get returns the wrong device', m => { m.detail.uuid = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; }],
	['official get returns the wrong device library', m => { m.detail.libraryUuid = 'different-device-library'; }],
	['official get association footprint conflicts', m => { m.detail.association.footprint.uuid = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; }],
	['official get association source is absent', m => { delete m.detail.association.footprint.libraryUuid; }],
	['official get is absent', m => { m.lib_Device.get = async () => undefined; }],
	['official get fails', m => { m.lib_Device.get = async () => { throw new Error('offline'); }; }],
	['official get LCSC contradicts search', m => { m.detail.property.supplierId = 'C9999'; }],
	['official get MPN differs', m => { m.detail.property.manufacturerId = 'AMS1117-5.0'; }],
	['official get MPN is missing', m => { delete m.detail.property.manufacturerId; }],
	['only a formula name exists without MPN', m => { m.state.ManufacturerId = ''; m.state.Component.name = '={Value}'; }],
	['stable name is exact, not casefolded', m => { m.state.ManufacturerId = ''; m.detail.name = 'ams1117-3.3_c6186'; }],
	['complete lookup fails', m => { m.lib_Device.getByLcscIds = async () => { throw new Error('inventory unavailable'); }; }],
	['another plausible candidate cannot be read', m => {
		const second = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
		m.lib_Device.getByLcscIds = async () => [m.hit, { ...m.hit, uuid: second }];
		m.lib_Device.get = async (uuid: string) => uuid === second ? undefined : m.detail;
	}],
];
for (const [reason, alter] of invalidInstanceIdentityCases) {
	test(`device identity: refuses ${reason}`, async () => {
		const mock = instanceIdentityEda(); alter(mock);
		const part = await readInstanceIdentity(mock);
		assert.equal(part.device.uuid, mock.state.Component.uuid);
		assert.ok(part.deviceIdentityError, 'identity remains unresolved instead of hydrating an unsupported library asset');
		assert.equal(part.deviceResolution, undefined);
	});
}

test('resolve_lcsc: batch cache keeps same-named footprint UUIDs and libraries separate when applying', async () => {
	const footprints = [
		{ name: 'r0603', uuid: 'FP-A', libraryUuid: 'LIB-A' },
		{ name: 'r0603', uuid: 'FP-B', libraryUuid: 'LIB-A' },
		{ name: 'r0603', uuid: 'FP-B', libraryUuid: 'LIB-B' },
	];
	const parts = [...footprints, footprints[0]].map((footprint, i) => mockComponent({
		PrimitiveId: `p-${i}`, ComponentType: 'part', Designator: `R${i + 1}`,
		Name: 'SHARED-MPN', ManufacturerId: 'SHARED-MPN', SupplierId: '', Footprint: footprint,
	}));
	const writes: unknown[] = [];
	let searches = 0;
	(globalThis as any).eda = {
		sch_PrimitiveComponent: {
			getAll: async () => parts,
			modify: async (id: string, changes: unknown) => { writes.push([id, changes]); return true; },
		},
		lib_Device: {
			search: async () => {
				searches++;
				return footprints.map((footprint, i) => ({
					uuid: `DEV-${i}`, libraryUuid: 'DEVICE-LIB', manufacturerId: 'SHARED-MPN',
					supplierId: `C${i + 1}`, footprint,
				}));
			},
		},
	};
	try {
		const res: any = await runAction('schematic.component.resolve_lcsc', { apply: true });
		assert.equal(res.result.unresolvedCount, 0);
		assert.equal(res.result.appliedCount, 4);
		assert.deepEqual(writes, [
			['p-0', { supplierId: 'C1' }], ['p-1', { supplierId: 'C2' }],
			['p-2', { supplierId: 'C3' }], ['p-3', { supplierId: 'C1' }],
		]);
		assert.equal(searches, 3, 'only the fourth identical identity may reuse the first resolution');
	}
	finally { delete (globalThis as any).eda; }
});

test('resolve_lcsc: batch cache retains distinct project-name fallbacks for the same MPN and footprint', async () => {
	const names = ['IMPORTED-A', 'IMPORTED-B', 'IMPORTED-A'];
	const parts = names.map((name, i) => mockComponent({
		PrimitiveId: `p-${i}`, ComponentType: 'part', Designator: `U${i + 1}`,
		Name: name, ManufacturerId: 'UNSEARCHABLE-MPN', SupplierId: '', Footprint: { name: 'SOT23' },
	}));
	const searches: unknown[] = [];
	(globalThis as any).eda = {
		sch_PrimitiveComponent: { getAll: async () => parts },
		lib_Device: {
			search: async (query: string, scope?: string) => {
				searches.push([query, scope]);
				return scope === 'project' ? [{
					uuid: `DEV-${query}`, libraryUuid: 'PROJECT-LIB', name: query,
					supplierId: query === 'IMPORTED-A' ? 'C10' : 'C20', footprintName: 'SOT23',
				}] : [];
			},
		},
	};
	try {
		const res: any = await runAction('schematic.component.resolve_lcsc', {});
		assert.equal(res.result.unresolvedCount, 0);
		assert.deepEqual(res.result.items.map((item: any) => [item.lcsc, item.via]), [
			['C10', 'project-name'], ['C20', 'project-name'], ['C10', 'project-name'],
		]);
		assert.deepEqual(searches, [
			['UNSEARCHABLE-MPN', undefined], ['IMPORTED-A', 'project'],
			['UNSEARCHABLE-MPN', undefined], ['IMPORTED-B', 'project'],
		]);
	}
	finally { delete (globalThis as any).eda; }
});

test('PCB silk creation supplies a registered font and legal top-left anchor', async () => {
	let stored: unknown[] | undefined;
	(globalThis as any).eda = {
		pcb_PrimitiveString: {
			create: async (...args: unknown[]) => {
				// Model the native font validation that previously rejected empty strings.
				assert.ok(['default', 'default2'].includes(String(args[4])));
				assert.ok(Number(args[7]) >= 1 && Number(args[7]) <= 9);
				stored = args;
				return { getState_PrimitiveId: () => 'silk-regression' };
			},
		},
		pcb_Primitive: { getPrimitivesBBox: async () => ({ minX: 100, minY: 160, maxX: 300, maxY: 200 }) },
	};
	try {
		const res: any = await runAction('pcb.silk.add', { text: 'TEST', x: 100, y: 200 });
		assert.equal(res.result.primitiveId, 'silk-regression');
		assert.deepEqual(stored, [3, 100, 200, 'TEST', 'default', 40, 6, 1, 0, false, 0, false, false]);
	}
	finally { delete (globalThis as any).eda; }
});

// ─── import_changes confirm dialog follows the UI language ─────────────

// Runs the real DOM probe body against a fake document, the same way
// clickImportConfirm does (AsyncFunction resolving `document` from the global).
async function runImportConfirmStep(modalText: string, buttonLabels: string[]): Promise<{ outcome: unknown; clicked: string[] }> {
	const clicked: string[] = [];
	const buttons = buttonLabels.map(label => ({ innerText: label, offsetParent: {}, click: () => clicked.push(label) }));
	const footer = { offsetParent: {}, innerText: [modalText, ...buttonLabels].join('\n'), querySelectorAll: () => buttons };
	// Nested wrapper that carries the title but no footer buttons (live-verified shape).
	const inner = { offsetParent: {}, innerText: modalText, querySelectorAll: () => [] };
	(globalThis as any).document = { querySelectorAll: () => [inner, footer] };
	try {
		const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor as { new (body: string): () => Promise<unknown> };
		return { outcome: await new AsyncFunction(importConfirmStepSource())(), clicked };
	}
	finally { delete (globalThis as any).document; }
}

test('import confirm probe clicks the zh-Hans 应用修改 button', async () => {
	const r = await runImportConfirmStep('确认导入信息', ['导出报告', '应用修改', '取消']);
	assert.deepEqual(r, { outcome: 'clicked', clicked: ['应用修改'] });
});

test('import confirm probe clicks Apply Changes on the English UI', async () => {
	const r = await runImportConfirmStep('Confirm Importing changes information\nGroup by\nAction\nObject', ['Export Report', ' Apply  changes ', 'Cancel']);
	assert.deepEqual(r, { outcome: 'clicked', clicked: [' Apply  changes '] });
});

test('import confirm probe never clicks a non-apply button and ignores unrelated modals', async () => {
	const noButton = await runImportConfirmStep('Confirm Importing changes information', ['Export Report', 'Cancel']);
	assert.deepEqual(noButton, { outcome: 'no-button', clicked: [] });
	const unrelated = await runImportConfirmStep('Design Rule Check', ['Apply Changes']);
	assert.deepEqual(unrelated, { outcome: 'none', clicked: [] });
});
