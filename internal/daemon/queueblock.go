package daemon

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// 连接器 FIFO 队首堵塞 —— 从「每条命令白烧 18 秒」变成「当场说清楚」。
//
// ── 这是什么形态(2026-08-24 真机,三份 daemon 日志 + 审计对齐) ──────────
//
//	09:10:47  schematic.power.connect_pin 进入连接器 FIFO 队首
//	09:10:47 ~ 09:16:52   队列一动不动(约 6 分钟)
//	          · 心跳一拍不落(退化前后都是 3.00s/次)
//	          · 旁路 document.current 每次 12ms 就回
//	          · 排在后面的 12 条 schematic.pages.list **每条各烧满 18s** 才报
//	            "connector did not respond" —— 合计白等 216 秒
//	09:16:52  队首终于 settle,积压的 12 条按 FIFO 顺序一次性冲出来,全部 ok:true
//
// 也就是说:WebSocket 活着、连接器主线程活着、eda.* 桥也活着 —— **堵的是连接器
// 那条每窗口一条的 FIFO 队列**(extension/src/action-queue.ts),而且只有它。
// 旧的 DEGRADED 提示把这写成「连接器退化了,插一次轻读再重试」,于是调用方继续
// 往一条已经堵死的队列里灌请求:每次 18s,还把积压推得更长。
//
// ── 判据必须是「证明」,不是「猜」 ────────────────────────────────────
//
// 只要一条**旁路读**(document.current,不进 FIFO)还秒回,而一条**入队的轻读**
// (project.current,进 FIFO)已经悬了超过 queueBlockGrace 还没回,那么队列堵死
// 有了近期旁路成功的证据。旁路失败/未知/过期时仍阻止新请求灌入，但返回
// CONNECTOR_HEALTH_UNVERIFIED；不能断言 socket/editor 健康，也不自动等待队列。
//
// 于是:
//   - 一条 FIFO 动作在 daemon 预算上超时 → 立刻挂一根**探针**(project.current),
//     不让任何调用方等它;
//   - 探针悬了超过 queueBlockGrace 之后,新来的 FIFO 动作**当场拒绝**
//     (CONNECTOR_QUEUE_BLOCKED),把「谁在堵、堵了多久、别重试」直接说出来;
//   - 探针一回来(不管成功失败)状态立刻解除 —— 队列一通,下一条命令就照常走。
//     误判窗口最多一个往返。
//
// 旁路动作(document.current)**永远放行**:它是停摆期唯一的观测手段,拦它等于
// 把唯一一只眼睛也蒙上。
//
// ── 为什么不做成"排队等"而是"当场拒绝" ────────────────────────────
//
// 连接器那条队列本来就会排。daemon 再排一层只会把同一份等待换个地方付,而且付
// 完还是超时。真正有价值的是**把 18 秒的沉默换成 0 秒的准确回答**,并且不再往
// 堵死的队列上加压(「假失败定律」:停摆期报失败的写大概率已落地,盲重试 = 造重复)。

// connectorBypassActions 镜像 extension/src/action-queue.ts 的 BYPASS_ACTIONS。
// 两侧必须一致:这里多列一个,就会在队列真堵时放一条注定超时的请求过去;少列
// 一个,就会把停摆期唯一还能用的观测手段拦掉。
var connectorBypassActions = map[string]bool{
	"document.current": true,
}

const (
	// queueBlockGrace 是「探针悬了多久才算证据」。轻读 p50 3ms、p95 数十毫秒,
	// 所以 2s 已经远在噪声之外;同时它短到足以在一条命令的尺度上省下 18s。
	queueBlockGrace = 2 * time.Second
	// queueProbeMaxLife 兜住探针本身:队列若长时间不通,探针到点自行结束、状态
	// 解除,下一条 FIFO 动作重新试一次(失败则重新挂探针)。没有它,一次永不
	// resolve 的队首会让这个窗口**永远**处于拒绝态。
	queueProbeMaxLife   = 5 * time.Minute
	queueBypassTimeout  = time.Second
	queueBypassInterval = 2 * time.Second
	queueBypassMaxAge   = 5 * time.Second
)

// queueProbe 是一根在飞的队列探针。
type queueProbe struct {
	// startedAt 是探针发出的时刻 —— 「悬了多久」按它算。
	startedAt time.Time
	// blockerAction / blockerID 是把探针逼出来的那条动作,用于点名。
	blockerAction string
	blockerID     string
	// blockedSince 是那条动作超时的时刻(比探针早一个预算,报出来更贴近人的感受)。
	blockedSince time.Time
	// Only an actual successful bypass response is evidence of responsiveness.
	bypassOK bool
	bypassAt time.Time
}

// queueBlockTracker 记录每个窗口是否有在飞的队列探针。
type queueBlockTracker struct {
	mu     sync.Mutex
	probes map[string]*queueProbe
	now    func() time.Time
}

func newQueueBlockTracker() *queueBlockTracker {
	return &queueBlockTracker{
		probes: map[string]*queueProbe{},
		now:    func() time.Time { return time.Now() },
	}
}

// beginProbe 记账一根新探针。第二个返回值为 false 表示该窗口已经有一根在飞,
// 调用方不应再发一根(探针必须唯一,否则它自己就成了积压的一部分)。
func (t *queueBlockTracker) beginProbe(windowID, blockerAction, blockerID string, blockedSince time.Time) (*queueProbe, bool) {
	if t == nil || windowID == "" {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.probes[windowID]; exists {
		return nil, false
	}
	p := &queueProbe{
		startedAt:     t.now(),
		blockerAction: blockerAction,
		blockerID:     blockerID,
		blockedSince:  blockedSince,
	}
	t.probes[windowID] = p
	return p, true
}

// endProbe 撤销记账。探针一回来就调用它 —— 队列已经在流动了。
func (t *queueBlockTracker) endProbe(windowID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.probes, windowID)
	t.mu.Unlock()
}

// blocked 报告入队探针是否悬过 queueBlockGrace。返回快照中的 bypassOK
// 进一步区分近期旁路成功和健康度未知；入队读超时本身不能证明旁路正常。
func (t *queueBlockTracker) blocked(windowID string) (*queueProbe, time.Duration, bool) {
	if t == nil || windowID == "" {
		return nil, 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.probes[windowID]
	if !ok {
		return nil, 0, false
	}
	waited := t.now().Sub(p.startedAt)
	if waited < queueBlockGrace {
		return nil, 0, false
	}
	// Return a value snapshot: the bypass observer updates the live record.
	snapshot := *p
	snapshot.bypassOK = p.bypassOK && !p.bypassAt.IsZero() && t.now().Sub(p.bypassAt) <= queueBypassMaxAge
	return &snapshot, waited, true
}

func (t *queueBlockTracker) recordBypass(windowID string, probe *queueProbe, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.probes[windowID] != probe {
		return // Ignore a result from a retired probe or an earlier connection.
	}
	probe.bypassOK = ok
	probe.bypassAt = t.now()
}

// queueBlockedResponse 构造拒绝回执。措辞必须停在可证边界内并给出**能执行的
// 下一步**:队首那个 handler 还在跑,它的效果可能稍后才落地 —— 所以「别重试」
// 不是建议,是判据(「假失败定律」)。
// queueBlockedResponse 构造拒绝回执。**message 必须自带全部要点** —— CLI 的
// requestAction 只把 message 交给上层,detail 会被丢掉。
func queueBlockedResponse(reqID string, p *queueProbe, waited time.Duration, blockedFor time.Duration) protocol.Response {
	if p == nil || !p.bypassOK {
		return errorResponse(reqID, "CONNECTOR_HEALTH_UNVERIFIED",
			"queued read unanswered after "+describeBlocker(p)+" — this action was NOT sent; do NOT re-issue the write. Bypass health is unverified; bring EasyEDA to the FOREGROUND and check document.current before continuing",
			"The queued project.current probe has been unanswered for "+waited.Round(100*time.Millisecond).String()+
				", but no recent successful BYPASS document.current response is available. This does not distinguish a blocked FIFO from an unresponsive editor. If the bypass read remains unresponsive, restart EasyEDA, then read back earlier writes before retrying. Automatic queue waiting is not justified.")
	}
	return errorResponse(reqID,
		"CONNECTOR_QUEUE_BLOCKED",
		"the connector's action queue is blocked by "+describeBlocker(p)+" for "+
			blockedFor.Round(time.Second).String()+
			" — this action was NOT sent; do NOT re-issue the write (the wedged handler is still running and its effect may land later)",
		"proof: a light QUEUED read (project.current, normally ~3ms) has been unanswered for "+
			waited.Round(100*time.Millisecond).String()+
			", while a recent BYPASS read document.current succeeded. Queued reads remain stalled after "+
			describeBlocker(p)+". Next step: wait — the daemon re-checks by itself and this refusal stops the moment the queue drains. If it never drains, bring the EasyEDA window to the FOREGROUND (background windows are where handlers wedge) and restart EasyEDA; then verify what actually landed with a read before re-issuing anything.")
}

func describeBlocker(p *queueProbe) string {
	if p == nil {
		return "unknown"
	}
	if p.blockerAction == "" {
		return "unknown"
	}
	if p.blockerID == "" {
		return p.blockerAction
	}
	return p.blockerAction + " (" + p.blockerID + ")"
}

// checkQueueBlocked is the pre-dispatch gate. It returns a refusal response when
// the target window's connector FIFO is proven blocked and this action would
// queue behind the stuck head. Bypass actions are never refused.
func (s *Server) checkQueueBlocked(req *protocol.Request) *protocol.Response {
	if req == nil || connectorBypassActions[req.Action] {
		return nil
	}
	p, waited, blocked := s.queueBlocks.blocked(req.WindowID)
	if !blocked {
		return nil
	}
	blockedFor := s.queueBlocks.now().Sub(p.blockedSince)
	resp := queueBlockedResponse(req.ID, p, waited, blockedFor)
	return &resp
}

// armQueueProbe fires the single in-flight light read that turns "one action
// timed out" into "the queue is (still) blocked" — or clears it. Called after a
// FIFO dispatch failure; a no-op when a probe is already in flight, when the
// action was a bypass one, or when the failure was not a timeout.
//
// The probe is deliberately NOT awaited by any caller: its whole purpose is to
// answer the NEXT command instantly instead of making it wait 18s to find out.
func (s *Server) armQueueProbe(target *conn, req *protocol.Request, dispatchErr error) {
	if target == nil || req == nil || dispatchErr == nil {
		return
	}
	if connectorBypassActions[req.Action] {
		return
	}
	// Only a timeout means "no answer came back". A write error / closed socket is
	// a different failure and the reconnect path owns it.
	if !isTimeoutErr(dispatchErr) {
		return
	}
	windowID := req.WindowID
	probe, started := s.queueBlocks.beginProbe(windowID, req.Action, req.ID, s.queueBlocks.now())
	if !started {
		return
	}
	go func() {
		defer s.queueBlocks.endProbe(windowID)
		base := s.connCtx
		if base == nil {
			base = context.Background()
		}
		ctx, cancel := context.WithTimeout(base, queueProbeMaxLife)
		defer cancel()
		// The bypass must run concurrently: waiting for the FIFO first would
		// prevent us from ever observing an editor while its queue is stalled.
		go s.observeQueueBypass(ctx, windowID, probe, req.ID, target.dispatch)
		p := protocol.Request{
			Envelope: protocol.Envelope{
				ID:        req.ID + "_qblock",
				Type:      protocol.TypeRequest,
				Version:   "v1",
				WindowID:  windowID,
				CreatedAt: time.Now().UTC(),
			},
			Action: backoffProbeAction,
		}
		if _, err := target.dispatch(ctx, p); err != nil {
			s.logf("queue-block probe on %s ended without an answer after %s (head was %s)",
				windowID, queueProbeMaxLife, describeBlocker(&queueProbe{blockerAction: req.Action, blockerID: req.ID}))
		}
	}()
}

func (s *Server) observeQueueBypass(ctx context.Context, windowID string, probe *queueProbe, requestID string, dispatch dispatchFn) {
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return
		}
		readCtx, cancel := context.WithTimeout(ctx, queueBypassTimeout)
		response, err := dispatch(readCtx, protocol.Request{
			Envelope: protocol.Envelope{ID: requestID + "_qblock_bypass_" + strconv.Itoa(attempt), Type: protocol.TypeRequest,
				Version: "v1", WindowID: windowID, CreatedAt: time.Now().UTC()},
			Action: "document.current", TimeoutMs: int(queueBypassTimeout / time.Millisecond),
		})
		cancel()
		s.queueBlocks.recordBypass(windowID, probe, err == nil && response != nil && response.OK)
		timer := time.NewTimer(queueBypassInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// isTimeoutErr reports whether a dispatch error is "the connector never answered"
// (context deadline / cancellation) rather than a transport write failure.
func isTimeoutErr(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
