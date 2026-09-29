/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction, schematicComponentModify } from './actions';
import { exactJSON, preservedInstance } from './preserve-instance';

function fixture() {
 return { primitiveId: 'p1', componentType: 'part', designator: 'C1', uniqueId: 'gge1', name: '={Value}',
  subPartName: 'device.1', x: 10, y: 20, rotation: 0, mirror: false, net: '', addIntoBom: false, addIntoPcb: true,
  manufacturer: '', manufacturerId: null, supplier: 'LCSC', supplierId: 'C100',
  otherProperty: { Value: '22µF', 'a.b': '${literal}', zero: 0, bool: false, empty: '' },
  component: { uuid: 'native-component', libraryUuid: 'lib' }, symbol: { uuid: 'symbol' }, footprint: { uuid: 'footprint' } } as Record<string, any>;
}

function primitive(record: Record<string, any>): any {
 const p: Record<string, any> = {};
 for (const key of Object.keys(record)) p['getState_' + key[0].toUpperCase() + key.slice(1)] = () => record[key];
 return p;
}

function install(t: { after(fn: () => void): void }, options: { drop?: string; failReadback?: boolean; enumerationFails?: boolean; residual?: boolean; lostOnClear?: boolean; globalAttributesEmpty?: boolean } = {}) {
 const globals = globalThis as any, old = globals.eda;
 t.after(() => { globals.eda = old; });
 const part = fixture(), sheet = { ...fixture(), primitiveId: 'sheet', componentType: 'sheet', designator: '', uniqueId: 'sheet-uid' };
 let parts = [part, sheet, { ...fixture(), primitiveId: 'flag', componentType: 'netflag', uniqueId: 'flag-uid' }];
 let wires = ['wire'], texts = ['text'], objects = ['object'], attributes = [
  { primitiveId: 'part-value', parentPrimitiveId: 'p1' }, { primitiveId: 'sheet-title', parentPrimitiveId: 'sheet' }, { primitiveId: 'orphan', parentPrimitiveId: 'old-marker' },
 ];
 const deletes: string[] = [], patches: any[] = [];
 const remove = async (ids: string[]) => {
  deletes.push(...ids);
  if (!options.residual) {
   parts = parts.filter(p => !ids.includes(p.primitiveId)); wires = wires.filter(id => !ids.includes(id)); texts = texts.filter(id => !ids.includes(id)); objects = objects.filter(id => !ids.includes(id)); attributes = attributes.filter(a => !ids.includes(a.primitiveId));
  }
  if (options.lostOnClear) part.otherProperty.Value = 'lost';
  return true;
 };
 const eda: any = {
  sch_PrimitiveComponent: {
   getAll: async () => { if (options.failReadback && patches.length) throw new Error('readback unavailable'); return parts.map(primitive); },
   get: async (id: string) => { if (options.failReadback && patches.length) throw new Error('readback unavailable'); const p = parts.find(p => p.primitiveId === id); return p && primitive(p); },
   modify: async (_id: string, patch: any) => { patches.push(patch); Object.assign(part, JSON.parse(JSON.stringify(patch))); if (options.drop === 'uniqueId') part.uniqueId = 'reset'; else if (options.drop) delete part.otherProperty[options.drop]; return primitive(part); },
   delete: remove,
  },
  sch_PrimitiveWire: { getAll: async () => wires.map(id => primitive({ primitiveId: id })), delete: remove },
  sch_PrimitiveText: { getAll: async () => { if (options.enumerationFails) throw new Error('text inventory unavailable'); return texts.map(id => primitive({ primitiveId: id })); }, delete: remove },
  sch_PrimitiveAttribute: { getAll: async (parent?: string) => (parent ? attributes.filter(a => a.parentPrimitiveId === parent) : options.globalAttributesEmpty ? [] : attributes).map(primitive) },
  sch_PrimitiveObject: { getAll: async () => objects.map(id => primitive({ primitiveId: id })), delete: remove },
 };
 for (const kind of ['Bus', 'Arc', 'Circle', 'Rectangle', 'Polygon']) eda['sch_Primitive' + kind] = { getAll: async () => [], delete: remove };
 globals.eda = eda;
 return { part, patches, deletes, getParts: () => parts, getAttributes: () => attributes };
}

test('preserved instance captures literal scalar attributes without normalization', () => {
 const original = fixture(), captured = preservedInstance(original);
 assert.equal((captured.otherProperty as any)['a.b'], '${literal}');
 assert.equal((captured.otherProperty as any).bool, false);
 assert.equal((captured.otherProperty as any).zero, 0);
 original.otherProperty.Value = 'changed';
 assert.equal((captured.otherProperty as any).Value, '22µF');
 assert.equal(exactJSON({ b: false, a: 0 }), exactJSON({ a: 0, b: false }));
 assert.notEqual(exactJSON(0), exactJSON('0'));
 for (const value of [null, [], { nested: {} }]) assert.throws(() => preservedInstance({ ...fixture(), otherProperty: value }));
});

test('preserveInstance accepts and retains an explicit empty native uniqueId', async t => {
 const fx = install(t);
 fx.part.uniqueId = '';
 const original = preservedInstance(fx.part);
 const result: any = await schematicComponentModify({ primitiveId: 'p1', patch: { x: 30, y: 40 }, preserveInstance: true });
 assert.equal(result.result.instancePreserved, true);
 assert.equal(fx.patches[0].uniqueId, '');
 assert.equal(exactJSON(preservedInstance(fx.part)), exactJSON(original));
 assert.equal(fx.part.x, 30);
});

test('geometry-only preserveInstance writes and verifies original native fields atomically', async t => {
 const fx = install(t), original = preservedInstance(fx.part);
 const result: any = await schematicComponentModify({ primitiveId: 'p1', patch: { x: 30, y: 40, rotation: 90, mirror: true }, preserveInstance: true });
 assert.equal(result.result.instancePreserved, true);
 assert.equal(exactJSON(preservedInstance(fx.part)), exactJSON(original));
 assert.equal(fx.patches[0].uniqueId, 'gge1');
 assert.equal(fx.patches[0].otherProperty['a.b'], '${literal}');
 assert.equal(fx.part.x, 30);
});

test('preserveInstance refuses non-geometric changes before any modify', async t => {
 const fx = install(t);
 await assert.rejects(schematicComponentModify({ primitiveId: 'p1', patch: { uniqueId: 'new' }, preserveInstance: true }), /geometry-only/);
 assert.equal(fx.patches.length, 0);
});

for (const options of [{ drop: 'Value' }, { drop: 'uniqueId' }, { failReadback: true }]) {
 test(`preserveInstance reports partial failure on silent loss ${JSON.stringify(options)}`, async t => {
  const fx = install(t, options);
  const result: any = await schematicComponentModify({ primitiveId: 'p1', patch: { x: 30 }, preserveInstance: true });
  assert.equal(result.result.instancePreserved, false);
  assert.equal(result.result.partial, true);
  assert.ok(result.result.notApplied.length);
  assert.equal(fx.patches.length, 1);
 });
}

test('preserveParts clear deletes only drawing content and orphan attributes', async t => {
 const fx = install(t), original = preservedInstance(fx.part, true);
 const result: any = await runAction('schematic.page.clear', { preserveParts: true, preservePartIds: ['p1'] });
 assert.equal(result.result.remaining, 0);
 assert.equal(result.result.instancesPreserved, true);
 assert.deepEqual(result.result.preservedPartIds, ['p1']);
 assert.deepEqual(fx.getParts().map(p => p.primitiveId), ['p1', 'sheet']);
 assert.deepEqual(fx.getAttributes().map(p => p.primitiveId), ['part-value', 'sheet-title']);
 assert.equal(exactJSON(preservedInstance(fx.part, true)), exactJSON(original));
 assert.deepEqual([...fx.deletes].sort(), ['flag', 'object', 'orphan', 'text', 'wire']);
});

for (const ids of [[], ['wrong'], ['p1', 'p1'], ['p1', 'absent']]) {
 test(`preserveParts rejects incomplete/duplicate identity set ${JSON.stringify(ids)} without writes`, async t => {
  const fx = install(t);
  await assert.rejects(runAction('schematic.page.clear', { preserveParts: true, preservePartIds: ids }));
  assert.deepEqual(fx.deletes, []);
 });
}

test('preserveParts requires complete inventory before any deletion', async t => {
 const fx = install(t, { enumerationFails: true });
 await assert.rejects(runAction('schematic.page.clear', { preserveParts: true, preservePartIds: ['p1'] }), /No primitives deleted/);
 assert.deepEqual(fx.deletes, []);
});

test('preserveParts enumerates protected attributes by parent when global API returns empty', async t => {
 const fx = install(t, { globalAttributesEmpty: true });
 const result: any = await runAction('schematic.page.clear', { preserveParts: true, preservePartIds: ['p1'] });
 assert.deepEqual(result.result.preservedAttributeIds, ['part-value', 'sheet-title']);
 assert.equal(result.result.attributeCoverage, 'existing-parents');
 assert.ok(result.result.coverageNotes.length);
 assert.ok(fx.getAttributes().some(a => a.primitiveId === 'part-value'));
 assert.ok(!fx.deletes.includes('part-value'));
 assert.ok(!fx.deletes.includes('sheet-title'));
 // The undiscoverable orphan is not misrepresented as inspected or removed.
 assert.ok(fx.getAttributes().some(a => a.primitiveId === 'orphan'));
});

test('preserveParts dry-run enumerates without deleting or claiming empty', async t => {
 const fx = install(t);
 const result: any = await runAction('schematic.page.clear', { preserveParts: true, preservePartIds: ['p1'], dryRun: true });
 assert.equal(result.result.remaining, 5);
 assert.equal(result.result.instancesPreserved, true);
 assert.deepEqual(fx.deletes, []);
});

for (const options of [{ residual: true }, { lostOnClear: true }]) {
 test(`preserveParts refuses residual or changed instances ${JSON.stringify(options)}`, async t => {
  install(t, options);
  const result: any = await runAction('schematic.page.clear', { preserveParts: true, preservePartIds: ['p1'] });
  assert.equal(result.result.partial, true);
  assert.ok(result.result.notApplied.length);
 });
}
