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
} = {}) {
	let otherProperty: Record<string, string | number | boolean> = {
		Description: 'keep me',
		Value: '',
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
				if (options.apply !== false && patch.otherProperty) {
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
