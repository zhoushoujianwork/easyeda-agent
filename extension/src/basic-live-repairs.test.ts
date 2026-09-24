/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';
import { sweepDeadlines } from './deadlines';

(globalThis as any).EDMT_EditorDocumentType = { HOME:-1, BLANK:0, SCHEMATIC_PAGE:1, PCB:3 };
const context = { dmt_Project: { getCurrentProjectInfo: async () => ({uuid:'project'}) }, dmt_SelectControl: { getCurrentDocumentInfo: async () => ({uuid:'page', tabId:'tab', documentType:1}) } };

function primitive(values: Record<string, unknown>): any {
	return new Proxy({}, { get: (_target, key) => String(key).startsWith('getState_') ? () => values[String(key).replace('getState_', '')] ?? null : undefined });
}

for (const mode of ['empty-return', 'throw-after-write', 'no-write', 'duplicate', 'wrong-net', 'hidden', 'old-only', 'unreadable'] as const) {
	test(`native label verifies fresh side effects: ${mode}`, async (t) => {
		let calls = 0;
		const label = primitive({ PrimitiveId: 'label', ParentPrimitiveId: 'wire', Key: 'Name', Value: 'SIG', X: 20, Y: 30, ValueVisible: mode !== 'hidden' });
		(globalThis as any).eda = {
			...context,
			sch_PrimitiveAttribute: {
				getAll: async () => {
					if (mode === 'unreadable' && calls) return undefined;
					if (mode === 'old-only') return [label];
					return calls && mode !== 'no-write' ? (mode === 'duplicate' ? [label, primitive({ PrimitiveId: 'second', ParentPrimitiveId: 'wire', Key: 'Name', Value: 'SIG', X: 20, Y: 30 })] : [label]) : [];
				},
				createNetLabel: async () => { calls++; if (mode === 'throw-after-write') throw new Error('host reply failed'); return undefined; },
			},
			sch_PrimitiveWire: { get: async () => primitive({ PrimitiveId: 'wire', Net: mode === 'wrong-net' ? 'OTHER' : 'SIG' }) },
		};
		t.after(() => { delete (globalThis as any).eda; });
		const { result }: any = await runAction('schematic.netflag.create', { kind: 'net_label', net: 'SIG', x: 20, y: 30 });
		assert.equal(calls, 1, 'never retry a write with an uncertain response');
		const valid = mode === 'empty-return' || mode === 'throw-after-write';
		assert.equal(result.verified, valid);
		if (valid) { assert.equal(result.primitiveId, 'label'); assert.equal(result.recoveredFromReadback, true); }
		else { assert.equal(result.partial, true); assert.equal(result.writeState, 'unknown'); }
	});
}

test('PCB stale ID and unreadable inventory refuse before modify', async (t) => {
	let writes = 0;
	t.after(() => { delete (globalThis as any).eda; });
	for (const inventory of [[], undefined]) {
		(globalThis as any).eda = { pcb_PrimitiveComponent: {
			getAll: async () => inventory, modify: async () => { writes++; throw new Error('must not run'); },
		} };
		await assert.rejects(() => runAction('pcb.component.modify', { primitiveId: 'stale', patch: { x: 10 } }), /not dispatched|no modification was dispatched/);
	}
	assert.equal(writes, 0);
});

for (const mode of ['keepout', 'named-rule', 'invalid-name', 'echo-name', 'wrong-geometry', 'unreadable'] as const) {
	test(`region create reads actual persistent state: ${mode}`, async (t) => {
		let writes = 0;
		const source = [0, 0, 'L', 20, 0, 20, 20, 0, 20, 0, 0];
		const named = mode === 'named-rule' || mode === 'echo-name';
		const fresh = primitive({ PrimitiveId: 'region', Layer: 1, RuleType: [named ? 9 : 7],
			RegionName: mode === 'named-rule' ? 'RULE' : null, PrimitiveLock: false, LineWidth: 1,
			ComplexPolygon: { getSource: () => mode === 'wrong-geometry' ? [0, 0, 'L', 5, 0, 5, 5, 0, 0] : source } });
		(globalThis as any).eda = {
			...context,
			pcb_MathPolygon: { createPolygon: () => ({ getSource: () => source }) },
			pcb_PrimitiveRegion: {
				create: async () => { writes++; return primitive({ PrimitiveId: 'region', RegionName: 'RULE' }); },
				getAll: async () => mode === 'unreadable' ? undefined : [fresh],
			},
		};
		t.after(() => { delete (globalThis as any).eda; });
		const payload = { points: [[0, 0], [20, 0], [20, 20], [0, 20]], ruleType: [named ? 9 : 7],
			...(named || mode === 'invalid-name' ? { name: 'RULE' } : {}) };
		if (mode === 'invalid-name') {
			await assert.rejects(() => runAction('pcb.region.create', payload), /only supported with follow-rule/);
			assert.equal(writes, 0);
		} else {
			const { result }: any = await runAction('pcb.region.create', payload);
			assert.equal(writes, 1);
			assert.equal(result.verified, mode === 'keepout' || mode === 'named-rule');
			assert.equal(result.primitiveId, 'region');
			if (mode === 'echo-name') { assert.equal(result.regionName, null); assert.deepEqual(result.differences, ['regionName']); }
		}
	});
}

for (const mode of ['no-write', 'read-failure', 'pending-with-match', 'wrong-parent', 'wrong-document'] as const) {
	test(`connect label preserves accountable partial stub: ${mode}`, async (t) => {
		let called = false;
		let deletes = 0;
		let finish: ((v: undefined) => void) | undefined;
		const label = primitive({ PrimitiveId: 'label', ParentPrimitiveId: mode === 'wrong-parent' ? 'other-wire' : 'stub', Key: 'Name', Value: 'SIG', X: 40, Y: 0, ValueVisible: true });
		(globalThis as any).eda = {
			...context,
			sch_PrimitiveWire: {
				create: async () => primitive({ PrimitiveId: 'stub' }),
				get: async () => primitive({ PrimitiveId: 'stub', Net: 'SIG' }),
				delete: async () => { deletes++; return true; },
			},
			sch_PrimitiveAttribute: {
				getAll: async () => !called ? [] : mode === 'read-failure' ? undefined : ['pending-with-match', 'wrong-parent', 'wrong-document'].includes(mode) ? [label] : [],
				createNetLabel: async () => { called = true; return mode === 'pending-with-match' ? new Promise<undefined>(r => { finish = r; }) : undefined; },
			},
		};
		if (mode === 'wrong-document') (globalThis as any).eda.dmt_SelectControl = { getCurrentDocumentInfo: async () => ({uuid: called ? 'other-page' : 'page', tabId:'tab', documentType:1}) };
		t.after(() => { finish?.(undefined); delete (globalThis as any).eda; });
		const pending = runAction('schematic.power.connect_pin', { kind: 'net_label', net: 'SIG', pinX: 0, pinY: 0, direction: 'right', offset: 40 });
		if (mode === 'pending-with-match') { await new Promise(r => setImmediate(r)); sweepDeadlines(Date.now() + 10000); }
		const { result }: any = await pending;
		assert.equal(result.verified, false);
		assert.equal(result.partial, true);
		assert.equal(result.wirePrimitiveId, 'stub');
		assert.equal(deletes, 0, 'do not rollback a stub while native label write state is unknown');
		if (mode === 'pending-with-match') assert.deepEqual(result.addedAttributeIds, ['label']);
	});
}

for (const mode of ['applied', 'unique-id-dropped', 'dropped', 'unreadable'] as const) {
	test(`PCB add returns fresh binding, not create echo: ${mode}`, async (t) => {
		let designator = 'R1';
		let uniqueId = '';
		const original = primitive({ PrimitiveId: 'placed', Designator: 'R1', UniqueId: '' });
		(globalThis as any).eda = {
			pcb_PrimitiveComponent: {
				create: async () => original,
				modify: async () => { if (mode === 'applied' || mode === 'unique-id-dropped') designator = 'R9'; if (mode === 'applied') uniqueId = 'gge9'; return original; },
				getAllPinsByPrimitiveId: async () => [],
				getAll: async () => mode === 'unreadable' ? undefined : [primitive({ PrimitiveId: 'placed', Designator: designator, UniqueId: uniqueId })],
			},
			pcb_Document: { startCalculatingRatline: async () => true },
		};
		t.after(() => { delete (globalThis as any).eda; });
		const { result }: any = await runAction('pcb.add_component', { libraryUuid:'lib', uuid:'device', x:100, y:100, designator:'R9', uniqueId:'gge9' });
		assert.equal(result.primitiveId, 'placed');
		assert.equal(result.bindingVerified, mode === 'applied');
		assert.equal(result.designator, mode === 'applied' || mode === 'unique-id-dropped' ? 'R9' : mode === 'dropped' ? 'R1' : null);
		assert.equal(result.uniqueId, mode === 'applied' ? 'gge9' : mode === 'unreadable' ? null : '');
		if (mode !== 'applied') assert.equal(result.partial, true);
	});
}
