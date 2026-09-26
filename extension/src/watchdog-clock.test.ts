import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(join(__dirname, 'watchdog-clock.ts'), 'utf8'), {
 compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
}).outputText;

function fixture(failure = '') {
 let ticks = 0, terminated = 0, cleared = 0, next = 0;
 const revoked: string[] = [], reports: string[] = [], workers: any[] = [], blobs: any[] = [];
 const intervals = new Map<number, () => void>();
 const URL = {
  createObjectURL(blob: any) { assert.equal(this, URL); if (failure === 'url') throw new Error('url denied'); blobs.push(blob); return 'blob:clock'; },
  revokeObjectURL(url: string) { assert.equal(this, URL); revoked.push(url); },
 };
 const sandbox = vm.createContext({
  URL,
  Blob: class { constructor(public parts: string[]) { if (failure === 'blob') throw new Error('blob denied'); } },
  Worker: class {
   onerror: ((event: { message: string }) => void) | null = null;
   private message: (() => void) | null = null;
   constructor(public url: string) { if (failure === 'worker') throw new Error('worker denied'); workers.push(this); }
   set onmessage(value: (() => void) | null) { if (failure === 'callbacks') throw new Error('callbacks denied'); this.message = value; }
   get onmessage() { return this.message; }
   terminate() { terminated++; }
  },
  setInterval(fn: () => void, ms: number) { assert.equal(ms, 3000); if (failure === 'fallback') throw new Error('timer denied'); intervals.set(++next, fn); return next; },
  clearInterval(id: number) { cleared++; intervals.delete(id); },
 });
 if (failure === 'fallback') (sandbox as any).Worker = undefined;
 const module = { exports: {} as typeof import('./watchdog-clock') };
 // Actual production source executes with the exact failure condition: bare
 // URL/Blob/Worker identifiers are shadowed, browser globals remain available.
 const load = vm.runInContext('(function(exports,module,URL,Blob,Worker){'+source+'})', sandbox);
 load(module.exports, module, undefined, undefined, undefined);
 const clock = module.exports.startWatchdogClock(() => ticks++, message => reports.push(message));
 return { clock, workers, intervals, revoked, reports, blobs, counts: () => ({ ticks, terminated, cleared }) };
}

test('masked extension names use browser globals synchronously, preserving URL receiver', () => {
 const f = fixture(); assert.equal(f.workers.length, 1); assert.equal(f.intervals.size, 0);
 assert.equal(f.workers[0].url, 'blob:clock');
 f.workers[0].onmessage(); assert.equal(f.counts().ticks, 1);
 assert.match(f.reports[0], /host worker ticker started/); f.clock.stop();
});

test('stop releases resources once and suppresses queued worker tick/error', () => {
 const f = fixture(), worker = f.workers[0], tick = worker.onmessage, error = worker.onerror;
 f.clock.stop(); f.clock.stop(); tick(); error({ message: 'late error' });
 assert.deepEqual(f.counts(), { ticks: 0, terminated: 1, cleared: 0 });
 assert.deepEqual(f.revoked, ['blob:clock']); assert.equal(f.intervals.size, 0);
});

for (const failure of ['blob', 'url', 'worker', 'callbacks']) {
 test(`${failure} failure cleans partial worker resources and provides cancellable fallback`, () => {
  const f = fixture(failure); assert.equal(f.intervals.size, 1);
  const tick = [...f.intervals.values()][0]; tick(); assert.equal(f.counts().ticks, 1);
  assert.match(f.reports[0], /unavailable/);
  assert.deepEqual(f.revoked, failure === 'worker' || failure === 'callbacks' ? ['blob:clock'] : []);
  assert.equal(f.counts().terminated, failure === 'callbacks' ? 1 : 0);
  f.clock.stop(); f.clock.stop(); tick();
  assert.equal(f.counts().ticks, 1); assert.equal(f.counts().cleared, 1);
 });
}

test('worker error falls back only once and rejects old queued messages', () => {
 const f = fixture(), worker = f.workers[0], tick = worker.onmessage, error = worker.onerror;
 error({ message: 'runtime failed' }); error({ message: 'duplicate' }); tick();
 assert.equal(f.intervals.size, 1); assert.equal(f.counts().ticks, 0);
 assert.equal(f.counts().terminated, 1); assert.deepEqual(f.revoked, ['blob:clock']);
 const fallback = [...f.intervals.values()][0]; fallback(); assert.equal(f.counts().ticks, 1);
 f.clock.stop(); fallback(); error({ message: 'late' });
 assert.equal(f.counts().ticks, 1); assert.equal(f.intervals.size, 0);
});

test('unavailable worker and fallback remain explicit and stoppable', () => {
 const f = fixture('fallback');
 assert.equal(f.intervals.size, 0); assert.match(f.reports[1], /fallback clock unavailable/);
 f.clock.stop();
});
