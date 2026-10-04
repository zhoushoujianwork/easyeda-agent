/**
 * action-queue.ts — 连接器侧的**单一咽喉**:每个窗口一次只跑一个 action handler。
 *
 * ── 为什么需要它(实测,不是推测) ──────────────────────────────────────
 *
 * `transport.ts` 把每条 WebSocket 消息交给 `eda.sys_WebSocket.register()` 的
 * onMessage 回调,而 `await` **不跨回调排队**:两条消息同 tick 到达 = 两个
 * handler 同时在飞。2026-08-20 用真 transport.ts + 假 eda 全局跑的探针:
 *
 *     t+0    ENTER  slow.write
 *     t+0    ENTER  fast.read      ← 写还没 settle,读已经开跑
 *     t+0    EXIT   fast.read
 *     t+400  EXIT   slow.write
 *     响应顺序:req-fast 先于 req-slow
 *
 * 于是「先发 W 再发 R」在连接器里**不构成任何先后关系**,R 报「那里什么都没有」
 * 既可能是真的没有、也可能是读得太早 —— 两者在观测上完全等价。这正是
 * `internal/app/sch_place_adopt.go` 门⓪只能做成启发式的根本原因。
 *
 * ── 这个队列提供什么(以及**不**提供什么) ────────────────────────────
 *
 * 提供:**handler 级**的 happens-before。R 的 handler 开跑时,W 的 handler 已经
 * settle(resolve 或 reject)。`seq` 把它变成可传输的算术。
 *
 * **不**提供:「文档已提交」。`eda.*` 的 handler 返回 ≠ 平台已经把改动写进文档
 * 模型 —— 那一层我们没有任何观测点(见 `docs/` 与 Go 侧 sch_place_adopt.go 的
 * 「残余风险」)。任何基于 seq 的结论都必须停在 handler 边界内。
 *
 * ── 放弃机制(与 FIFO 同等重要) ──────────────────────────────────────
 *
 * 串行化本身是有代价的:一个**永不 resolve** 的 handler 会吞掉它后面的一切。
 * 真机实测过这个形态 —— 一次卡死的重调用让接下来 4.5 分钟的
 * place/delete/document.open 全部静默消失(轻读照常,所以看起来「连接器还活着」)。
 * 所以队首必须有截止时间:到点就**放弃它**(不再等),`seqAbandoned++`,记进
 * `abandonedIds` 环形缓冲,队列继续流动。
 *
 * 截止时间来自**请求自带的 timeoutMs**(daemon 已经在下发),不是写死的常数:
 * `sch check` / DRC 这类合法长操作能跑 60 秒以上,一个固定 30 秒的门会把它们
 * 全误杀。
 *
 * 被放弃的 handler 仍在后台跑 —— 我们只是不再等它,**它的效果可能稍后才落地**。
 * 这就是为什么 `seqAbandoned` 一旦变化,关于那段时间任何写的结论都不成立。
 * 整份文档源码 roundtrip 例外:响应可放弃,但 FIFO 保持阻塞直到 handler 真正
 * settle,避免不可取消的迟到 setter 覆盖后续工程写入。document.current 旁路可观测。
 *
 * ── 放弃闸的时基必须是 worker tick,不能是 setTimeout(2026-08-24 真机定案) ──
 *
 * 上面那套放弃机制**从上线到 2026-08-24 一次都没生效过**:三份 daemon 日志 +
 * 全部审计记录里 `ACTION_ABANDONED` 出现 0 次,而同期真机连续出现「队首卡死 6
 * 分钟、后面 12 条全部超时、然后按 FIFO 一次性冲出来」。根因不在这个文件的逻辑,
 * 在它用的**时钟**:后台窗口里主线程 setTimeout 会被节流/冻结(同一段时间里
 * worker tick 驱动的心跳一拍不落、旁路读 12ms 就回)。所以截止时间改为登记到
 * `deadlines.ts`,由 transport 的 **worker tick** 兜底触发。详见那个文件的头注释。
 */

import { armDeadline, type DeadlineHandle } from './deadlines';

/** 无 timeoutMs 的请求(裸 HTTP 调用方)用它兜底 —— 与 daemon 的 dispatchTimeout 同值。 */
export const ABANDON_FALLBACK_MS = 60_000;
/**
 * 加在请求预算之上的宽限。daemon 自己在 (timeoutMs - 2s) 就放弃等待,所以连接器
 * 必须**晚于**它才放弃:否则我们会在 daemon 还在等的时候把一个可能马上就要
 * settle 的 handler 丢掉,把「慢」变成「假失败」。
 */
export const ABANDON_GRACE_MS = 2_000;
/** 队列积压上限。满了明确拒绝,不无限堆积。 */
export const MAX_QUEUE_DEPTH = 64;
/** `abandonedIds` 环形缓冲长度 —— 让判定能点名,而不只是数数。 */
export const ABANDONED_ID_RING = 32;

/**
 * 每一个 response frame 都带的顺序证据。字段形状由 Go 侧
 * `internal/protocol/envelope.go` 镜像,**两侧必须一致**。
 */
export interface QueueStamp {
	/**
	 * 已完成的动作数:每个 FIFO handler settle(resolve 或 reject)之后 +1。
	 * 单调递增、永不重用、永不回退。响应上带的是「**本动作完成之后**」的值,
	 * 也就是「本动作是第 seq 个完成的」。
	 *
	 * 于是对一次读的响应,`seq - 1` = 这次读的 handler 开跑时已经完成的动作数
	 * —— 所有 seq 更小的动作都在它开跑**之前**就 settle 了。
	 */
	seq: number;
	/** 累计被放弃的动作数,单调递增。变化 = 那段时间的顺序证据作废。 */
	seqAbandoned: number;
	/**
	 * true = 这条响应走了旁路通道、不在 FIFO 里,因此它的 `seq` **不构成任何
	 * 顺序证据**。省略 = false。
	 */
	unordered?: boolean;
	/** 最近被放弃的 request id(最多 ABANDONED_ID_RING 条),供判定点名。 */
	abandonedIds?: string[];
}

/** 一次入队的结局。四种互斥,调用方据此构造 response frame。 */
export type QueueOutcome<T> =
	| { status: 'ok'; value: T; stamp: QueueStamp }
	| { status: 'error'; error: unknown; stamp: QueueStamp }
	| { status: 'abandoned'; waitedMs: number; stamp: QueueStamp }
	| { status: 'overflow'; depth: number; stamp: QueueStamp };

export interface QueueTask<T> {
	/** request id —— 被放弃时进 `abandonedIds`。 */
	id: string;
	/** 请求自带的往返预算(daemon 下发)。<=0 / 缺省 → ABANDON_FALLBACK_MS。 */
	timeoutMs?: number;
	/** true = 走旁路,不进 FIFO,不动 seq(见 transport.ts 的旁路名单)。 */
	bypass?: boolean;
	/**
	 * 超时仍返回 abandoned 诊断,但在 handler 真正 settle 前保留 FIFO。
	 * 整份文档源码 setter 不可取消;超时后放行别的写会与迟到 setter 竞态。
	 */
	holdAfterAbandon?: boolean;
	run: () => Promise<T>;
}

export interface ActionQueueOptions {
	maxDepth?: number;
	fallbackTimeoutMs?: number;
	graceMs?: number;
}

/**
 * 每窗口一条的串行执行链。`submit` **永不 reject** —— 所有结局都编码在
 * QueueOutcome 里,调用方不需要 try/catch。
 */
export class ActionQueue {
	private seq = 0;
	private abandoned = 0;
	private readonly abandonedIds: string[] = [];
	/** 已入队但还没轮到的任务数(队首一旦开跑就不再计入)。 */
	private depth = 0;
	/** 显式 promise 链:保留模式在响应放弃后仍等 handler settle。 */
	private tail: Promise<void> = Promise.resolve();

	private readonly maxDepth: number;
	private readonly fallbackTimeoutMs: number;
	private readonly graceMs: number;

	constructor(options: ActionQueueOptions = {}) {
		this.maxDepth = options.maxDepth ?? MAX_QUEUE_DEPTH;
		this.fallbackTimeoutMs = options.fallbackTimeoutMs ?? ABANDON_FALLBACK_MS;
		this.graceMs = options.graceMs ?? ABANDON_GRACE_MS;
	}

	/** 当前计数器快照(诊断用;不含 unordered 标记)。 */
	counters(): QueueStamp {
		return this.stamp(false);
	}

	/** 队列里等待开跑的任务数。 */
	pending(): number {
		return this.depth;
	}

	/**
	 * 提交一个动作。FIFO 任务按提交顺序**逐个**执行;bypass 任务立即执行且不动
	 * seq。返回的 promise 永不 reject。
	 *
	 * @param task - 要执行的动作
	 * @returns 这次提交的结局 + 顺序证据
	 */
	submit<T>(task: QueueTask<T>): Promise<QueueOutcome<T>> {
		if (task.bypass) {
			return this.runBypass(task);
		}
		if (this.depth >= this.maxDepth) {
			// 溢出保护:明确拒绝,而不是让积压无限增长。调用方把它翻译成一个
			// 可读的 error code,调用者于是知道「连接器堵了」而不是「超时了」。
			return Promise.resolve({ status: 'overflow', depth: this.depth, stamp: this.stamp(false) });
		}
		this.depth += 1;
		return new Promise<QueueOutcome<T>>((resolve) => {
			// 链上每一环都必须**永不 reject**,否则一次失败会把整条链断掉,
			// 后续动作全部静默消失 —— 那正是这个文件要根治的病。
			this.tail = this.tail.then(() => this.runHead(task, resolve));
		});
	}

	/** 旁路:不入链、不动 seq、永远打 unordered。 */
	private async runBypass<T>(task: QueueTask<T>): Promise<QueueOutcome<T>> {
		try {
			const value = await task.run();
			return { status: 'ok', value, stamp: this.stamp(true) };
		}
		catch (error) {
			return { status: 'error', error, stamp: this.stamp(true) };
		}
	}

	/** 跑队首一个任务,带截止时间;无论结局如何都 resolve,让链继续流动。 */
	private async runHead<T>(task: QueueTask<T>, resolve: (outcome: QueueOutcome<T>) => void): Promise<void> {
		this.depth -= 1;
		const budget = task.timeoutMs && task.timeoutMs > 0 ? task.timeoutMs : this.fallbackTimeoutMs;
		const deadlineMs = budget + this.graceMs;
		const startedAt = Date.now();

		// 截止时间登记到 deadlines.ts:setTimeout 是快路径,worker tick 的
		// sweepDeadlines() 是保底路径。**保底路径才是这个闸门真正的时基** ——
		// 见文件头「放弃闸的时基必须是 worker tick」。
		let handle: DeadlineHandle | undefined;
		const abandonSignal = new Promise<{ kind: 'abandoned' }>((res) => {
			handle = armDeadline(deadlineMs, () => res({ kind: 'abandoned' }));
		});

		// run() 可能**同步抛**(payload 校验之类),那也算一次正常的 settle。
		let running: Promise<{ kind: 'ok'; value: T } | { kind: 'error'; error: unknown }>;
		try {
			running = task.run().then(
				(value) => ({ kind: 'ok' as const, value }),
				(error: unknown) => ({ kind: 'error' as const, error }),
			);
		}
		catch (error) {
			running = Promise.resolve({ kind: 'error' as const, error });
		}

		const outcome = await Promise.race([running, abandonSignal]);
		handle?.cancel();

		if (outcome.kind === 'abandoned') {
			// 我们不再等它,但它**还在跑**:效果可能稍后才落地。这就是
			// seqAbandoned 的全部意义 —— 它一变,那段时间的顺序证据就作废。
			// 绝不给它补 seq:一个被放弃的动作没有可定位的完成时刻。
			this.abandoned += 1;
			this.abandonedIds.push(task.id);
			while (this.abandonedIds.length > ABANDONED_ID_RING) {
				this.abandonedIds.shift();
			}
			resolve({ status: 'abandoned', waitedMs: Date.now() - startedAt, stamp: this.stamp(false) });
			if (task.holdAfterAbandon) {
				// The diagnostic response has settled, but the FIFO tail has not.
				// No later ordered read/write can race an uncancellable late setter.
				await running;
			} else {
				running.then(() => undefined, () => undefined); // 防未处理拒绝
			}
			return;
		}

		// settle 了(成功或失败都算完成)→ 计数 +1,响应带的是完成之后的值。
		this.seq += 1;
		if (outcome.kind === 'ok') {
			resolve({ status: 'ok', value: outcome.value, stamp: this.stamp(false) });
		}
		else {
			resolve({ status: 'error', error: outcome.error, stamp: this.stamp(false) });
		}
	}

	private stamp(unordered: boolean): QueueStamp {
		const stamp: QueueStamp = { seq: this.seq, seqAbandoned: this.abandoned };
		if (unordered) {
			stamp.unordered = true;
		}
		if (this.abandonedIds.length > 0) {
			stamp.abandonedIds = [...this.abandonedIds];
		}
		return stamp;
	}
}

/**
 * 旁路名单 —— **必须短**,每一条都要写清入选理由。
 *
 * 为什么需要旁路:串行化把「队首卡死」变成「整条队列停摆」。放弃机制会在
 * `timeoutMs + grace` 之后解开它,但在那之前的那段时间里,**轻读还能用**是我们
 * 唯一的观测手段(真机 wedge 期正是这个形态:写全被吞、轻读照常)。失去它 =
 * 失去「连接器到底还活着吗」的唯一答案。
 *
 * 入选判据(三条全中才准进):
 *   1. 纯读,**绝不改画布**(不创建/修改/删除任何图元);
 *   2. 代价恒定且极小(不遍历图元、不跑 netlist / DRC / 导出);
 *   3. 它「读得太早」的失败方向是**安全**的。
 *
 * 名单:
 *   - `document.current` —— 「编辑器还活着吗、现在开着哪一页」的规范探针,只读
 *     `eda.dmt_*` 的当前文档信息,不碰任何图元。第 3 条尤其成立:它抢在一个还
 *     排在队里的 `document.open` 前面时,只会报**旧的**那一页,于是 `--doc` 门
 *     (ensureActiveDoc)判定「还没切过去」→ 重试;它不可能反过来谎报「已经切到
 *     目标页了」——那一页此刻确实还没被打开。也就是说旁路只会让它更保守。
 *
 * 铁律:**旁路的响应永远不能被当成新鲜度证据**。它们打 `unordered: true`,Go 侧
 * 见到这个标记就退回弱证据档(见 internal/app/sch_place_adopt.go)。
 */
export const BYPASS_ACTIONS: ReadonlySet<string> = new Set<string>([
	'document.current',
]);

/**
 * 判断一个 action 是否走旁路通道。
 *
 * @param action - action 名
 * @returns true = 不进 FIFO,响应打 unordered
 */
export function isBypassAction(action: string): boolean {
	return BYPASS_ACTIONS.has(action);
}

/** Whole-document writes retain FIFO even after their response budget expires. */
export function mustHoldAfterAbandon(action: string): boolean {
	return action === 'document.source.roundtrip';
}
