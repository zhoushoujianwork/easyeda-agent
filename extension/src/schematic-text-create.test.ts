/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';

function fixture(mode = '') {
 const g = globalThis as any, old = g.eda, types = g.EDMT_EditorDocumentType;
 let writes = 0, reads = 0, contexts = 0, page = 'page-1'; const args: unknown[][] = [];
 let state: Record<string, any> = { primitiveId: 'new-1', content: '中文说明\n第二行😀', x: 0, y: -20, rotation: 90, fontSize: 10 };
 const text = () => Object.fromEntries(Object.entries(state).map(([key, value]) => ['getState_' + (key === 'primitiveId' ? 'PrimitiveId' : key[0].toUpperCase() + key.slice(1)), () => value]));
 g.EDMT_EditorDocumentType = { HOME: -1, BLANK: 0, SCHEMATIC_PAGE: 1, PCB: 3, SYMBOL_COMPONENT: 4, FOOTPRINT: 5 };
 g.eda = {
  dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: mode === 'missing-project' ? '' : 'project-1' }) },
  dmt_SelectControl: { getCurrentDocumentInfo: async () => {
   contexts++; if (mode === 'outer-drift' && contexts === 5) page = 'other';
   return { uuid: page, tabId: mode === 'missing-tab' ? '' : 'tab-1', documentType: mode === 'pcb' ? 3 : mode === 'symbol' ? 4 : mode === 'footprint' ? 5 : 1 };
  } },
  sch_PrimitiveText: {
   getAll: async () => {
    reads++;
    if (reads === 1) { if (mode === 'before-drift') page = 'other'; return mode === 'existing-id' ? [text()] : []; }
    if (mode === 'read-throw') throw new Error('read unavailable');
    if (mode === 'read-undefined') return undefined;
    if (mode === 'after-drift') page = 'other';
    if (mode === 'read-missing') return [];
    if (mode === 'read-duplicate') return [text(), text()];
    if (mode.startsWith('mismatch-')) { const key = mode.slice('mismatch-'.length); state[key] = key === 'content' ? 'changed' : 123; }
    return [text()];
   },
   create: async (...a: unknown[]) => {
    writes++; args.push(a); state = { ...state, x: a[0], y: a[1], content: a[2], rotation: a[3], fontSize: a[6] ?? 9 };
    if (mode === 'create-throw') throw new Error('write may already exist');
    if (mode === 'create-undefined') return undefined;
    if (mode === 'no-id') return { getState_PrimitiveId: () => '' };
    return text();
   },
   delete: async () => { assert.fail('must never delete uncertain text'); },
  },
 };
 return { payload: () => ({ content: '中文说明\n第二行😀', x: 0, y: -20, rotation: 90, fontSize: 10 }), writes: () => writes, reads: () => reads, args,
  restore: () => { g.eda = old; g.EDMT_EditorDocumentType = types; } };
}

test('text create preserves Chinese, LF and zero; exact official argument order and fresh ID readback', async () => {
 const f = fixture(); try {
  const r: any = (await runAction('schematic.text.create', f.payload())).result;
  assert.equal(r.verified, true); assert.equal(r.partial, false); assert.equal(r.knownID, 'new-1'); assert.equal(r.after.content, f.payload().content);
  assert.deepEqual(f.args, [[0, -20, f.payload().content, 90, undefined, undefined, 10]]); assert.equal(f.reads(), 2); assert.equal(f.writes(), 1);
 } finally { f.restore(); }
});

test('omitted optional fields use rotation zero and leave font size to host; Unicode limit counts code points', async () => {
 const f = fixture(); try {
  const content = '😀'.repeat(16000);
  const r: any = (await runAction('schematic.text.create', { content, x: 0, y: 0 })).result;
  assert.equal(r.verified, true); assert.equal(r.after.rotation, 0); assert.equal(r.after.fontSize, 9);
  assert.deepEqual(f.args, [[0, 0, content, 0, undefined, undefined, undefined]]);
 } finally { f.restore(); }
});

for (const [name, patch] of Object.entries({
 blank: { content: ' \t\n　' }, 'unicode-blank': { content: '\u0085' }, empty: { content: '' }, nul: { content: 'a\0b' }, oversized: { content: '😀'.repeat(16001) },
 'missing-content': { content: undefined }, 'missing-x': { x: undefined }, 'missing-y': { y: undefined }, 'null-x': { x: null }, 'nan-x': { x: NaN }, 'inf-y': { y: Infinity }, 'string-x': { x: '0' },
 'font-zero': { fontSize: 0 }, 'font-negative': { fontSize: -1 }, 'font-inf': { fontSize: Infinity }, 'font-null': { fontSize: null }, 'font-undefined': { fontSize: undefined },
 rotation: { rotation: 45 }, 'rotation-null': { rotation: null }, 'rotation-undefined': { rotation: undefined }, style: { color: '#fff' },
})) test(`invalid ${name} refuses with zero writes`, async () => {
 const f = fixture(); try { await assert.rejects(() => runAction('schematic.text.create', { ...f.payload(), ...patch })); assert.equal(f.writes(), 0); } finally { f.restore(); }
});

for (const mode of ['pcb', 'symbol', 'footprint', 'missing-project', 'missing-tab', 'before-drift']) test(`wrong or drifting page refuses before write: ${mode}`, async () => {
 const f = fixture(mode); try { await assert.rejects(() => runAction('schematic.text.create', f.payload())); assert.equal(f.writes(), 0); } finally { f.restore(); }
});

for (const mode of ['create-throw', 'create-undefined', 'no-id', 'read-throw', 'read-undefined', 'read-missing', 'read-duplicate', 'existing-id',
 'mismatch-content', 'mismatch-x', 'mismatch-y', 'mismatch-rotation', 'mismatch-fontSize', 'after-drift', 'outer-drift']) test(`uncertain ${mode} retains evidence, creates exactly once and never deletes`, async () => {
 const f = fixture(mode); try {
  const r: any = (await runAction('schematic.text.create', f.payload())).result;
  assert.equal(r.verified, false); assert.equal(r.partial, true); assert.equal(r.attempted, true); assert.ok(r.errors.length); assert.equal(f.writes(), 1);
  assert.equal(r.knownID, ['create-throw', 'create-undefined', 'no-id'].includes(mode) ? null : 'new-1');
 } finally { f.restore(); }
});
