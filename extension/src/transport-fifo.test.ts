/// <reference types="@jlceda/pro-api-types" />
/**
 * transport 层的接线回归 —— 队列本身正确不代表**接对了**。
 *
 * 这个文件驱动的是**真正的** transport.ts:装一个假 `eda` 全局,拿到它注册进
 * `eda.sys_WebSocket` 的那个真 onMessage 回调,在同一个 tick 里连发多条 request
 * 帧,然后检查真实发出的 response frame。
 *
 * 它守的是两条容易被无意破坏的性质:
 *
 *   1. **入队必须发生在第一个 await 之前**。handleRequest 里只要有人在
 *      `actionQueue.submit(...)` 前面插一个 await(读个 context、查个开关),
 *      入队顺序就不再等于消息到达顺序,FIFO 当场退化回并发 —— 而单测如果只测
 *      队列类,这个退化完全测不出来。
 *   2. **每一条响应都带顺序证据**(seq / seqAbandoned / unordered)。
 *
 * 改造前用同一套装置跑出来的基线(2026-08-20):
 *      ENTER slow.write → ENTER fast.read → EXIT fast.read → EXIT slow.write
 *      响应顺序 req-fast, req-slow      ← 读在写 settle 之前就被服务了
 */

import assert from 'node:assert/strict';
import { after, test } from 'node:test';

type Frame = { type: string; id?: string; seq?: number; seqAbandoned?: number; unordered?: boolean };

const sent: Frame[] = [];
let capturedOnMessage: ((event: { data: string }) => void) | null = null;

(globalThis as Record<string, unknown>).eda = {
	sys_WebSocket: {
		register(_id: string, _url: string, onMessage: (event: { data: string }) => void): void {
			capturedOnMessage = onMessage;
		},
		send(_id: string, data: string): void {
			sent.push(JSON.parse(data) as Frame);
		},
		close(): void { /* noop */ },
	},
	sys_Message: { showToastMessage(): void { /* noop */ } },
	sys_I18n: { text: (s: string) => s },
	sys_Log: { add(): void { /* noop */ } },
	sys_Storage: { getExtensionUserConfig: () => true },
	sys_Environment: { getEditorCurrentVersion: () => '3.2.175-test' },
	dmt_Project: { getCurrentProjectInfo: async (): Promise<never> => { throw new Error('no project'); } },
	dmt_SelectControl: { getCurrentDocumentInfo: async (): Promise<never> => { throw new Error('no doc'); } },
};

const events: string[] = [];
// TS→CJS 后 transport 里是 `actions_1.runAction(...)` 的属性查找,所以替换
// exports 就能把真实 handler 换成可控时序的假实现,而 transport 本身是真的。
// eslint-disable-next-line @typescript-eslint/no-var-requires
const actions = require('./actions') as { runAction: unknown };
actions.runAction = async (action: string): Promise<{ result: Record<string, unknown> }> => {
	events.push(`enter ${action}`);
	if (action === 'slow.write') {
		await new Promise((r) => setTimeout(r, 150));
	}
	events.push(`exit ${action}`);
	return { result: { action } };
};

// eslint-disable-next-line @typescript-eslint/no-var-requires
const transport = require('./transport') as { reconnect: () => void; stop: (showToast?: boolean) => void; deactivate: () => void };
after(() => transport.deactivate());

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

test('transport:同 tick 到达的动作按到达顺序串行,响应带顺序证据,旁路自曝 unordered', async () => {
	transport.reconnect();
	for (let i = 0; i < 100 && !capturedOnMessage; i++) {
		await sleep(20); // REGISTER_DELAY_MS = 200
	}
	assert.ok(capturedOnMessage, 'transport 从未调用 eda.sys_WebSocket.register()');
	const onMessage = capturedOnMessage;

	onMessage({ data: JSON.stringify({ type: 'handshake', service: 'easyeda-agent' }) });
	await sleep(50);
	sent.length = 0;
	events.length = 0;

	// 同一个 tick 连发三条:一条慢写、一条旁路诊断读、一条快读。
	onMessage({ data: JSON.stringify({ type: 'request', id: 'req-slow', action: 'slow.write', timeoutMs: 20000 }) });
	onMessage({ data: JSON.stringify({ type: 'request', id: 'req-bypass', action: 'document.current', timeoutMs: 20000 }) });
	onMessage({ data: JSON.stringify({ type: 'request', id: 'req-fast', action: 'fast.read', timeoutMs: 20000 }) });

	await sleep(500);

	// 判据分两条写,不写死整条时间线:旁路与队首谁先进是微任务时序细节,
	// 而下面两条才是这次改动的全部意义。
	const at = (what: string): number => {
		const i = events.indexOf(what);
		assert.notEqual(i, -1, `时间线里没有 ${what}:${events.join(' | ')}`);
		return i;
	};
	assert.ok(at('enter fast.read') > at('exit slow.write'),
		`FIFO 上的读绝不能在写 settle 之前开跑:${events.join(' | ')}`);
	assert.ok(at('exit document.current') < at('exit slow.write'),
		`旁路必须能在队首还在跑时给出答案(wedge 期唯一的观测手段):${events.join(' | ')}`);

	const responses = sent.filter((f) => f.type === 'response');
	assert.deepEqual(responses.map((f) => f.id), ['req-bypass', 'req-slow', 'req-fast']);

	const byId = new Map(responses.map((f) => [f.id, f]));
	const bypass = byId.get('req-bypass');
	assert.equal(bypass?.unordered, true, '旁路响应必须打 unordered —— 它的 seq 不构成任何顺序证据');
	assert.equal(bypass?.seq, 0, '旁路不推进 seq');

	assert.equal(byId.get('req-slow')?.seq, 1);
	assert.equal(byId.get('req-slow')?.unordered, undefined);
	assert.equal(byId.get('req-fast')?.seq, 2,
		'fast.read 的 seq 必须严格大于 slow.write —— 这就是「读的 handler 在写 settle 之后才开跑」的可传输形式');
	for (const r of responses) {
		assert.equal(r.seqAbandoned, 0, '本用例没有任何动作被放弃');
	}

	transport.stop(false);
});
