/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';

type Receiver = (event: { data: string }) => void;
const handshake = JSON.stringify({ type: 'handshake', service: 'easyeda-agent' });

// Only worker messages run. Main-thread timers remain queued: this tests the
// production transport without sleeps or assuming a background timer will fire.
async function fixture(run: (f: {
 transport: typeof import('./transport'); tick: (ms?: number) => Promise<void>;
 advance: (ms: number) => void; receive: (index: number, data?: string) => Promise<void>;
 registrations: Array<{ id: string; receive: Receiver }>; frames: Array<any>;
 timers: Map<number, () => void>; workers: Array<{ onmessage: (() => void) | null; terminated: boolean }>;
 deferContexts: () => void; contexts: Array<(project: any) => void>;
}) => Promise<void>): Promise<void> {
 const globals = globalThis as Record<string, any>;
 const original = { setTimeout: globals.setTimeout, clearTimeout: globals.clearTimeout,
  Worker: globals.Worker, eda: globals.eda, now: Date.now };
 let now = 10000, nextTimer = 0;
 let workerTick: (() => void) | null | undefined;
 const workers: Array<{ onmessage: (() => void) | null; terminated: boolean }> = [];
 const timers = new Map<number, () => void>();
 const registrations: Array<{ id: string; receive: Receiver }> = [];
 const frames: Array<any> = [];
 let deferContext = false; const contexts: Array<(project: any) => void> = [];
 const flush = async () => { for (let i = 0; i < 6; i++) await Promise.resolve(); };
 globals.setTimeout = (fn: () => void) => { timers.set(++nextTimer, fn); return nextTimer; };
 globals.clearTimeout = (id: number) => { timers.delete(id); };
 globals.Worker = class {
  terminated = false; private callback: (() => void) | null = null;
  constructor() { workers.push(this); }
  set onmessage(fn: (() => void) | null) { workerTick = fn; this.callback = fn; }
  get onmessage() { return this.callback; }
  terminate() { this.terminated = true; }
 };
 Date.now = () => now;
 globals.eda = {
  sys_WebSocket: {
   register(id: string, _url: string, receive: Receiver) { registrations.push({ id, receive }); },
   send(_id: string, data: string) { frames.push(JSON.parse(data)); }, close() {},
  },
  sys_Message: { showToastMessage() {} }, sys_I18n: { text: (s: string) => s },
  sys_Log: { add() {} }, sys_Storage: { getExtensionUserConfig: () => true },
  sys_Environment: { getEditorCurrentVersion: () => '4.1.60-test' },
  dmt_Project: { getCurrentProjectInfo: () => deferContext ? new Promise(resolve => contexts.push(resolve)) : Promise.resolve(undefined) },
  dmt_SelectControl: { getCurrentDocumentInfo: async () => undefined },
 };
 delete require.cache[require.resolve('./transport')];
 const transport = require('./transport') as typeof import('./transport');
 try {
  await run({ transport, registrations, frames, timers, workers, contexts, deferContexts: () => { deferContext = true; },
   advance: (ms) => { now += ms; },
   tick: async (ms = 3000) => { now += ms; assert.ok(workerTick); workerTick(); await flush(); },
   receive: async (index, data = handshake) => { registrations[index].receive({ data }); await flush(); },
  });
 }
 finally {
  transport.deactivate(); await flush();
  Date.now = original.now;
  Object.assign(globals, { setTimeout: original.setTimeout, clearTimeout: original.clearTimeout, Worker: original.Worker, eda: original.eda });
 }
}

test('worker can register and reconnect after lost pongs with main-thread timers frozen', async () => fixture(async f => {
 f.transport.start();
 assert.equal(f.registrations.length, 0, 'release delay must precede register');
 await f.tick();
 assert.equal(f.registrations.length, 1, 'worker fallback must register without main-thread timers');
 await f.receive(0);
 assert.equal(f.transport.getConnectionStatus().connected, true);
 for (let i = 0; i < 5; i++) await f.tick();
 assert.equal(f.registrations.length, 2, 'missed pongs must initiate a new real registration');
 await f.receive(1);
 assert.equal(f.transport.getConnectionStatus().connected, true);
}));

test('handshake timeout progresses and retries using worker ticks only', async () => fixture(async f => {
 f.transport.start(); await f.tick(); await f.tick();
 assert.equal(f.transport.getConnectionStatus().connected, false);
 assert.equal(f.transport.getConnectionStatus().connecting, false);
 await f.receive(0); // Expired callback before the next attempt.
 assert.equal(f.frames.filter(x => x.type === 'register').length, 0);
 await f.tick(); await f.tick();
 assert.equal(f.registrations.length, 2);
 await f.receive(0); // Expired callback while the new attempt is pending.
 assert.equal(f.frames.filter(x => x.type === 'register').length, 0);
 await f.receive(1);
 assert.equal(f.transport.getConnectionStatus().connected, true);
}));

test('late handshake is refused even before delayed timeout callbacks run', async () => fixture(async f => {
 f.transport.start(); await f.tick(); f.advance(1500); await f.receive(0);
 assert.equal(f.transport.getConnectionStatus().connected, false);
 assert.equal(f.frames.filter(x => x.type === 'register').length, 0);
}));

test('stop before release cancels worker and queued timer registration', async () => fixture(async f => {
 f.transport.start();
 const callbacks = [...f.timers.values()];
 f.transport.stop(false); await f.tick();
 for (const callback of callbacks) callback();
 await f.tick();
 assert.equal(f.registrations.length, 0);
 assert.equal(f.timers.size, 0);
}));

test('stop after registration ignores late handshake and request frames', async () => fixture(async f => {
 f.transport.start(); await f.tick(); f.transport.stop(false);
 await f.receive(0);
 await f.receive(0, JSON.stringify({ type: 'request', id: 'late', action: 'document.current' }));
 await f.tick();
 assert.equal(f.transport.getConnectionStatus().connected, false);
 assert.equal(f.frames.filter(x => x.type === 'register' || x.type === 'response').length, 0);
 assert.equal(f.timers.size, 0);
}));

test('explicit reconnect cancels prior release callbacks without cancelling the new attempt', async () => fixture(async f => {
 f.transport.start(); const callbacks = [...f.timers.values()];
 f.transport.reconnect();
 for (const callback of callbacks) callback();
 await f.tick();
 assert.equal(f.registrations.length, 1);
 await f.receive(0);
 assert.equal(f.transport.getConnectionStatus().connected, true);
}));

test('duplicate handshake preserves accepted window identity', async () => fixture(async f => {
 f.transport.start(); await f.tick(); await f.receive(0);
 const before = f.transport.getConnectionStatus().windowId;
 await f.receive(0);
 assert.equal(f.transport.getConnectionStatus().windowId, before);
 assert.equal(f.frames.filter(x => x.type === 'register').length, 1);
}));

test('wrong service cannot later turn the failed attempt into a valid connection', async () => fixture(async f => {
 f.transport.start(); await f.tick();
 await f.receive(0, JSON.stringify({ type: 'handshake', service: 'other' }));
 await f.receive(0);
 assert.equal(f.transport.getConnectionStatus().connected, false);
 assert.equal(f.frames.filter(x => x.type === 'register').length, 0);
}));


test('old context finishing after reconnect cannot rename the new connection', async () => fixture(async f => {
 f.deferContexts(); f.transport.start(); await f.tick(); await f.receive(0);
 const oldWindow = f.transport.getConnectionStatus().windowId;
 f.transport.reconnect(); await f.tick(); await f.receive(1);
 const newWindow = f.transport.getConnectionStatus().windowId;
 assert.notEqual(oldWindow, newWindow);
 assert.equal(f.contexts.length, 2);
 f.contexts[1]({ uuid: 'new-project' }); await f.receive(1);
 f.contexts[0]({ uuid: 'old-project' }); await f.receive(1);
 assert.deepEqual(f.frames.filter(x => x.type === 'context').map(x => [x.windowId, x.projectUuid]), [[newWindow, 'new-project']]);
}));

test('older context in the same session cannot overwrite a newer completed read', async () => fixture(async f => {
 f.deferContexts(); f.transport.start(); await f.tick(); await f.receive(0);
 await f.tick(); // Heartbeat starts another context read in the same session.
 assert.equal(f.contexts.length, 2);
 f.contexts[1]({ uuid: 'new-project' }); await f.receive(0);
 f.contexts[0]({ uuid: 'old-project' }); await f.receive(0);
 assert.deepEqual(f.frames.filter(x => x.type === 'context').map(x => x.projectUuid), ['new-project']);
}));


test('stop preserves in-flight deadline sweeps while deactivate releases the clock', async () => fixture(async f => {
 f.transport.start();
 const deadlines = require('./deadlines') as typeof import('./deadlines');
 let timedOut = false;
 deadlines.armDeadline(1000, () => { timedOut = true; });
 f.transport.stop(false); await f.tick();
 assert.equal(timedOut, true, 'stopping the connection must not strand action guards');
 assert.equal(f.workers.length, 1); assert.equal(f.workers[0].terminated, false);
 const queuedTick = f.workers[0].onmessage!;
 f.transport.deactivate(); assert.equal(f.workers[0].terminated, true);
 queuedTick(); assert.equal(f.registrations.length, 0);
 f.transport.reconnect(); assert.equal(f.workers.length, 2);
 queuedTick(); assert.equal(f.registrations.length, 0, 'disposed clock cannot drive the new attempt');
 await f.tick(); assert.equal(f.registrations.length, 1);
 await f.receive(0); assert.equal(f.transport.getConnectionStatus().connected, true);
}));
