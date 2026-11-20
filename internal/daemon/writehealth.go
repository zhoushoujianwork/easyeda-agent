package daemon

// writehealth.go — 连接器负载退化的 daemon 侧对策(REPORT esp32mini-round2
// 新 3:长会话下 document.open 失败率 7%→41%、写操作系统性劣化,agent 收到
// 瞬时失败后盲重试造重复,是最贵的时间黑洞)。两件事:
//
//  1. **滚动健康度**:按窗口维护最近 N 次转发动作的成败环形窗 + 连续失败计数,
//     /health 暴露(writeHealth 字段),失败响应上附加结构化 degraded 提示。
//  2. **自适应退避重试(范围收窄,见下)**:失败后自动插一次轻读 + 短延迟再
//     重发一次 —— 但只对白名单里的动作。
//
// ## 口径修订(2026-08-19 真机复验):测「效果」不是测「返回码」
//
// 首版只统计**调用是否返回成功**。真机跑完一整场端到端后它全程 failureRate
// 0.05 / degraded:false,而同期画布上大面积的写根本没生效 —— 因为本轮的主要
// 故障形态是**返回成功但画布没变**(假成功):
//
//   - `schematic.primitives.delete` 返回 ok:true,连接器自己回读却报
//     `1 primitive(s) survived the delete`(issue #164);
//   - `sch block-apply` 6 件器件只落地 1 件,底层每次 `component.place` 大多返回成功;
//   - 反向也有**假失败**:`connect_pin` 报 `connector did not respond`,回读发现
//     连接已经建好(盲重试会造重复旗)。
//
// 所以健康度的样本判据改成「写的效果」:
//
//	返回成功 + 回读证实没生效  → 计 failure(FakeSuccesses,首版完全看不见的那一类)
//	返回失败 + 回读证实已落地  → **不**计 failure,单独归 FakeFailures
//	                             (同样是连接器不健康的信号,但形态不同:重试会造重复,
//	                              而不是"这条路没在工作"——两者的处置动作相反,混在
//	                              一个数字里就没法指导决策)
//	没有回读证据            → 退回返回码(ok=成功 / 失败=失败),并计入 Verified 之外
//
// 「回读证据」有两条来源,互不重叠:
//
//   - **通道 A(daemon 侧内省,零耦合)**:连接器自己回读后把结论写在
//     response.result 里(`partial` / `survivedTotal` / `notApplied` / `deleted:false`)。
//     effectFromResponse 在转发路径上直接读它 —— 不需要 CLI 配合,也不需要
//     改 action 目录或重打 .eext。
//   - **通道 B(CLI 侧回传,POST /writeverify)**:回读发生在**响应之外**
//     (block-apply 落地回读、connect_pin 的 slow-landed 复核、zone-draw 的
//     landed-check),响应里没有任何证据可挖,只有发起回读的命令知道结论。
//     选独立上报通道而不是「塞进下一条 action 的字段」,理由:(1) 判决往往在
//     若干次读之后才出来,没有可搭车的响应;(2) 一次判决常覆盖**多次**调用
//     (6 次 place 只落 1 件),搭车字段表达不了;(3) 不动 protocol 的 action
//     目录 = 不动连接器 = 不用重装 .eext(本项目最贵的一步)。
//
// ## 分桶(否则单条路的高失败率被稀释)
//
// 真机上 `connect_pin` 一批里约 40% 失败,混合窗口读数仍是 5% —— 20 样本混所有
// action 的均值天然会把"某条路根本没在工作"抹平。所以除了窗口级环形窗,再按
// action 各留一条环形窗;**任一 action 桶退化 ⇒ 窗口 degraded**,并在
// degradedActions 里点名。degraded 从此能被真机那批数字触发。
//
// ## 重试范围的取舍(硬约束:绝不盲重发可能已落地的写)
//
// 通用层面无法可靠判定「上次写落没落地」:每种写的复核方式都不同(connect_pin
// 要查网表、place 要查器件存在、exec_js 是任意代码根本没有通用判据)。按任务
// 约定收窄为两档:
//
//   - **白名单重试**(retryableOnFailure):只收「重发与首发收敛到同一状态」的
//     幂等导航类动作(document.open / schematic.page.open —— 打开已打开的文档
//     是 no-op,不存在"造重复"),这类动作恰好也是实测退化最重的(41%)。重试
//     前先发一次轻读探测(project.current):轻读也失败 = 连接器停摆,加压只会
//     更糟 → 透传原失败,不重发。
//   - **其余写失败一律透传**,但当窗口处于退化态时在响应里附加结构化提示
//     (result.degraded + warning):告诉调用方先轻读复核上次写是否已落地
//     (假失败定律:报失败的写大概率已落地),复核到已落地就不要重发。逐命令
//     的复核留给命令层(zone-draw 的 survey、connect_pin 的网表回读)——
//     它们才知道各自的"落地"长什么样,复核完再走通道 B 回传。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// retryableOnFailure lists the ONLY actions the daemon will auto-retry after a
// failure: idempotent navigation ops where a duplicate send converges to the
// same state whether or not the first send landed. Content-mutating actions
// and debug.exec_js (arbitrary code) must NEVER be here.
var retryableOnFailure = map[string]bool{
	"document.open":       true,
	"schematic.page.open": true,
}

// backoffProbeAction is the light read inserted before an auto-retry. It is
// cheap, domain-agnostic, and — measured on the degraded connector — inserting
// a read between writes markedly raises the next call's success odds.
const backoffProbeAction = "project.current"

// backoffProbeTimeout bounds the probe so a wedged connector fails the probe
// fast and the original failure is passed through instead of piling on load.
const backoffProbeTimeout = 5 * time.Second

// backoffSettleDelay is the pause between the probe and the retry (package
// variable so tests run without real sleeps).
var backoffSettleDelay = 500 * time.Millisecond

// writeHealthWindow is the ring size of per-window recent outcomes.
const writeHealthWindow = 20

// writeHealthActionWindow is the ring size of the PER-ACTION outcome window.
// Same depth as the mixed window so a bursty action (a batch of connect_pin)
// keeps its own history even while unrelated traffic flows through the window
// ring — that dilution is exactly what hid the 40% connect_pin failure rate.
const writeHealthActionWindow = 20

// writeHealthActionHorizon bounds how long a per-action bucket stays relevant,
// counted in the window's own forwarded outcomes. Without it a road that failed
// early in a session would latch degraded forever (its bucket never refills,
// because nobody calls it any more). 60 ≈ three window rings: long enough that a
// burst interleaved with the 2–3 probes every CLI command fires stays visible,
// short enough that ancient history stops shouting.
const writeHealthActionHorizon = 60

// Degradation thresholds: enough samples to mean something, or an unbroken
// streak that speaks for itself.
const (
	writeHealthMinSamples   = 8
	writeHealthDegradedRate = 0.35
	writeHealthConsecFails  = 3

	// Per-action thresholds are deliberately reachable with fewer samples: a
	// single action that fails 2 of its last 5 calls IS a broken road, and the
	// whole point of bucketing is to see it before the mixed average moves.
	// The sample floor applies to BOTH bucket rules (rate and streak): a bare
	// 3-call streak is already covered by the window-level rule, and letting a
	// 3-sample bucket flag on its own would latch degraded on any action that
	// failed a few times and was never called again.
	writeHealthActionMinSamples = 5
	writeHealthActionRate       = 0.35
	writeHealthActionConsec     = 3
)

// effectVerdict is what a READ-BACK proved about a write's effect on the canvas.
// Unknown is the honest default: ok=true is NOT evidence that the write landed
// (that assumption is the whole defect this file was rewritten for).
type effectVerdict uint8

const (
	effectUnknown effectVerdict = iota
	effectLanded
	effectNotLanded
)

// healthSample is one forwarded attempt: what the call RETURNED (ok) and what a
// read-back later PROVED (verdict). Samples live in two rings at once (window +
// per-action) as pointers, so one amendment updates both views.
type healthSample struct {
	action    string
	requestID string
	ok        bool
	verdict   effectVerdict
	at        time.Time
	// err is the failing response's "CODE: message" (empty when ok) — what
	// health shows as lastFailureError, so a red window says why, not just which.
	err string
	// seq is this window's monotonic outcome counter at push time — the clock
	// the per-action horizon runs on (see writeHealthActionHorizon).
	seq uint64
	// mutates / timedOut: see outcome; read by writeStallLocked.
	mutates  bool
	timedOut bool
}

// failed is the effect-level verdict: the number that drives degraded.
//
//	ok=true  + 证实没生效 → 失败(假成功;首版看不见的那一类)
//	ok=true  + 其余       → 成功
//	ok=false + 证实已落地 → **不**算失败(假失败:写其实成了,重发才是错的)
//	ok=false + 其余       → 失败
func (s *healthSample) failed() bool {
	if s.ok {
		return s.verdict == effectNotLanded
	}
	return s.verdict != effectLanded
}

func (s *healthSample) fakeSuccess() bool { return s.ok && s.verdict == effectNotLanded }
func (s *healthSample) fakeFailure() bool { return !s.ok && s.verdict == effectLanded }
func (s *healthSample) verified() bool    { return s.verdict != effectUnknown }

// outcome is one forwarded attempt as the dispatch path reports it.
type outcome struct {
	Action    string
	RequestID string
	OK        bool
	// Verdict carries通道 A 的证据(effectFromResponse):连接器在响应里自带的
	// 回读结论。effectUnknown = 响应没给证据,不是"证明落地了"。
	Verdict effectVerdict
	// ErrorCode is the failing response's code (empty when ok). Used ONLY to
	// drop request-refusal samples — see requestRefusalCodes.
	ErrorCode string
	// ErrorMsg is the failing response's "CODE: message" for lastFailureError.
	ErrorMsg string
	// Mutates / TimedOut feed the write-stall diagnosis (writestall.go): a
	// content write that got no answer within its budget.
	Mutates  bool
	TimedOut bool
}

// outcomeOf builds the health outcome of one forwarded attempt.
func outcomeOf(req *protocol.Request, resp *protocol.Response, err error) outcome {
	code := responseErrorCode(resp)
	return outcome{
		Action:    req.Action,
		RequestID: req.ID,
		OK:        err == nil && resp != nil && resp.OK,
		Verdict:   effectFromResponse(req, resp),
		ErrorCode: code,
		ErrorMsg:  responseErrorMsg(resp),
		Mutates:   requestMutates(req),
		TimedOut:  isTimeoutErr(err) || code == "ACTION_ABANDONED",
	}
}

// requestRefusalCodes 是「**请求本身讲不通,而且一个字节都没写**」的错误码。
//
// 这类响应**完全不进写健康度采样**:它不含任何关于连接器是否健康的信息。
// 把它们计成失败会产生一种很坏的误报 —— 用户把网名打错三次,连接器就被染成
// DEGRADED,而真正的停摆信号(#185 那类:socket 死了、register 被静默忽略)
// 被淹没在同一个 failureRate 里。**健康度要衡量的是「这条路还通不通」,不是
// 「调用方参数对不对」。**
//
// 判定必须机械、保守。这里只收三个码:
//   - PRECONDITION_REFUSED:handler 动手前拒绝的专用码(零变异是它的使用前提);
//   - MISSING_PAYLOAD_FIELD / UNKNOWN_ACTION:请求根本没成形,连接器无从执行。
//
// **INVALID_STATE 故意不在此列**:它既可能是「你要的事讲不通」,也可能是
// 「编辑器状态真的坏了」,一刀切会把真故障也吞掉。要豁免的 handler 应显式
// 改用 PRECONDITION_REFUSED,而不是放宽这里。
var requestRefusalCodes = map[string]bool{
	"PRECONDITION_REFUSED":  true,
	"MISSING_PAYLOAD_FIELD": true,
	"UNKNOWN_ACTION":        true,
}

// responseErrorCode pulls the error code out of a response, tolerating nil.
func responseErrorCode(resp *protocol.Response) string {
	if resp == nil || resp.Error == nil {
		return ""
	}
	return resp.Error.Code
}

// responseErrorMsg is the failing response's "CODE: message", bounded so one
// long platform error cannot bloat /health.
func responseErrorMsg(resp *protocol.Response) string {
	if resp == nil || resp.Error == nil {
		return ""
	}
	msg := resp.Error.Code + ": " + resp.Error.Message
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	return msg
}

// ActionWriteHealth is one action's slice of a window's health — the bucket that
// keeps "this one road is not working" from being averaged away.
type ActionWriteHealth struct {
	Samples             int     `json:"samples"`
	Failures            int     `json:"failures"`
	FailureRate         float64 `json:"failureRate"`
	ConsecutiveFailures int     `json:"consecutiveFailures"`
	Verified            int     `json:"verified"`
	FakeSuccesses       int     `json:"fakeSuccesses,omitempty"`
	FakeFailures        int     `json:"fakeFailures,omitempty"`
	Degraded            bool    `json:"degraded"`
}

// WindowWriteHealth is the /health-exposed snapshot of one window's recent
// forwarded-action outcomes. Failures/FailureRate are EFFECT-level (see
// healthSample.failed), not return-code level.
type WindowWriteHealth struct {
	Samples             int     `json:"samples"`
	Failures            int     `json:"failures"`
	FailureRate         float64 `json:"failureRate"`
	ConsecutiveFailures int     `json:"consecutiveFailures"`
	Degraded            bool    `json:"degraded"`
	// Verified is how many of the samples carry read-back evidence at all. A low
	// number next to a green FailureRate means "nobody checked", not "all good".
	Verified int `json:"verified"`
	// FakeSuccesses: returned ok, read-back proved it did NOT land. Counted as
	// failures — this is the class the first version was blind to.
	FakeSuccesses int `json:"fakeSuccesses,omitempty"`
	// FakeFailures: returned failure, read-back proved it DID land. NOT counted
	// as failures; a resend here mints duplicates (假失败定律).
	FakeFailures      int    `json:"fakeFailures,omitempty"`
	LastFailureAction string `json:"lastFailureAction,omitempty"`
	LastFailureAt     string `json:"lastFailureAt,omitempty"`
	// LastFailureError is that failure's "CODE: message"; a fake success (ok
	// reply, read-back proved nothing landed) has no error and says so.
	LastFailureError string `json:"lastFailureError,omitempty"`
	// DegradedActions names the per-action buckets over threshold — the roads
	// that are not working, even when the mixed average looks fine.
	DegradedActions []string `json:"degradedActions,omitempty"`
	// Actions reports the buckets worth looking at (degraded, or with any
	// effect-level failure). Clean buckets are omitted to keep /health readable.
	Actions map[string]ActionWriteHealth `json:"actions,omitempty"`
}

type windowHealthState struct {
	recent   []*healthSample // mixed ring, newest last
	byAction map[string][]*healthSample
	seq      uint64 // monotonic count of outcomes ever pushed to this window
}

func (st *windowHealthState) push(s *healthSample) {
	st.seq++
	s.seq = st.seq
	st.recent = append(st.recent, s)
	if len(st.recent) > writeHealthWindow {
		st.recent = st.recent[len(st.recent)-writeHealthWindow:]
	}
	if st.byAction == nil {
		st.byAction = map[string][]*healthSample{}
	}
	b := append(st.byAction[s.action], s)
	if len(b) > writeHealthActionWindow {
		b = b[len(b)-writeHealthActionWindow:]
	}
	st.byAction[s.action] = b
}

// writeHealthTracker keeps the rolling per-window outcome window. All methods
// are safe for concurrent use. Zero external cost: pure in-memory counters
// updated on the dispatch path the daemon already owns.
type writeHealthTracker struct {
	mu       sync.Mutex
	byWindow map[string]*windowHealthState
}

func newWriteHealthTracker() *writeHealthTracker {
	return &writeHealthTracker{byWindow: map[string]*windowHealthState{}}
}

// stateLocked returns (creating if needed) a window's state. Caller holds mu.
func (t *writeHealthTracker) stateLocked(windowID string) *windowHealthState {
	st := t.byWindow[windowID]
	if st == nil {
		st = &windowHealthState{byAction: map[string][]*healthSample{}}
		t.byWindow[windowID] = st
	}
	return st
}

// observe records one forwarded-action outcome for a window.
func (t *writeHealthTracker) observe(windowID string, o outcome) {
	if t == nil || windowID == "" {
		return
	}
	// A request refusal says nothing about connector health — dropping it here
	// (rather than counting it as a non-failure) keeps it out of `samples` too,
	// so it can't dilute the denominator either way. See requestRefusalCodes.
	if !o.OK && requestRefusalCodes[o.ErrorCode] {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stateLocked(windowID).push(&healthSample{
		action:    o.Action,
		requestID: o.RequestID,
		ok:        o.OK,
		verdict:   o.Verdict,
		at:        time.Now().UTC(),
		err:       o.ErrorMsg,
		mutates:   o.Mutates,
		timedOut:  o.TimedOut,
	})
}

// WriteVerification is 通道 B 的载荷:一条命令**回读之后**得出的效果判决。
//
// 一次判决可以覆盖多次调用(block-apply 一口气发 6 次 place,回读发现只有 1 件
// 在页面上 → landed=1 notLanded=5),所以计数而不是布尔。RequestID 能给就给
// (daemon 分配、响应里回显的 id),给了就精确改写那一条样本;给不了(回读是
// 按器件/网络算的,映射不回单次调用)就按 action 回填最近的未验证样本。
type WriteVerification struct {
	WindowID  string `json:"windowId,omitempty"`
	Project   string `json:"project,omitempty"`
	Action    string `json:"action"`
	RequestID string `json:"requestId,omitempty"`
	// Source is the command that did the verifying ("sch block-apply"), for
	// diagnostics only — it never affects the arithmetic.
	Source string `json:"source,omitempty"`
	// ReturnedOK is what the verified call(s) RETURNED. nil = true (the common
	// case: a write that reported success and is now being checked).
	ReturnedOK *bool `json:"returnedOK,omitempty"`
	Landed     int   `json:"landed,omitempty"`
	NotLanded  int   `json:"notLanded,omitempty"`
}

func (v WriteVerification) returnedOK() bool { return v.ReturnedOK == nil || *v.ReturnedOK }

// verify applies a read-back verdict to a window's samples.
//
//	有 requestId → 精确改写那一条(重试产生的同 id 样本取最新的一条,它才是定案)
//	无 requestId → 按 action 回填最近的**未验证且成败面相符**的样本;样本不够
//	               (早滚出环形窗了)就补记新样本 —— 迟到的判决绝不能丢,否则
//	               "6 件只落 1 件"这种批量假成功又变回不可见。
func (t *writeHealthTracker) verify(windowID string, v WriteVerification) {
	if t == nil || windowID == "" || strings.TrimSpace(v.Action) == "" {
		return
	}
	if v.Landed <= 0 && v.NotLanded <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.stateLocked(windowID)
	ok := v.returnedOK()

	if v.RequestID != "" {
		// A single addressed call is either landed or not; 任一 notLanded 就按
		// 没生效算(部分应用也是没写全,#151 的 partial 同义)。
		verdict := effectLanded
		if v.NotLanded > 0 {
			verdict = effectNotLanded
		}
		// Search the action bucket first: it outlives the mixed window ring, so a
		// verdict that took a while to compute still finds its sample.
		for _, ring := range [][]*healthSample{st.byAction[v.Action], st.recent} {
			for i := len(ring) - 1; i >= 0; i-- {
				if ring[i].requestID == v.RequestID {
					ring[i].verdict = verdict
					return
				}
			}
		}
	}

	assign := func(n int, verdict effectVerdict) {
		bucket := st.byAction[v.Action]
		for i := len(bucket) - 1; i >= 0 && n > 0; i-- {
			s := bucket[i]
			if s.verified() || s.ok != ok {
				continue
			}
			s.verdict = verdict
			n--
		}
		for ; n > 0; n-- {
			st.push(&healthSample{action: v.Action, ok: ok, verdict: verdict, at: time.Now().UTC()})
		}
	}
	// notLanded first: when a report covers a mixed batch, the failures must win
	// the (limited) supply of matching unverified samples.
	assign(v.NotLanded, effectNotLanded)
	assign(v.Landed, effectLanded)
}

// forget drops a window's state (call when its connector goes away — a
// reconnected window starts clean, matching the stale-read guard's lifetime).
func (t *writeHealthTracker) forget(windowID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.byWindow, windowID)
}

func rate(failures, samples int) float64 {
	if samples <= 0 {
		return 0
	}
	return math.Round(float64(failures)/float64(samples)*10000) / 10000
}

// snapshotActionLocked summarizes one action's bucket, ignoring samples older
// than minSeq (the recency horizon). Returns Samples==0 when the whole bucket
// has aged out — the caller then omits the action entirely.
func snapshotActionLocked(samples []*healthSample, minSeq uint64) ActionWriteHealth {
	fresh := samples
	for i, s := range samples {
		if s.seq > minSeq {
			fresh = samples[i:]
			break
		}
		fresh = nil
	}
	samples = fresh
	a := ActionWriteHealth{Samples: len(samples)}
	for _, s := range samples {
		if s.failed() {
			a.Failures++
		}
		if s.verified() {
			a.Verified++
		}
		if s.fakeSuccess() {
			a.FakeSuccesses++
		}
		if s.fakeFailure() {
			a.FakeFailures++
		}
	}
	a.FailureRate = rate(a.Failures, a.Samples)
	for i := len(samples) - 1; i >= 0 && samples[i].failed(); i-- {
		a.ConsecutiveFailures++
	}
	a.Degraded = a.Samples >= writeHealthActionMinSamples &&
		(a.FailureRate >= writeHealthActionRate || a.ConsecutiveFailures >= writeHealthActionConsec)
	return a
}

func snapshotLocked(st *windowHealthState) WindowWriteHealth {
	s := WindowWriteHealth{Samples: len(st.recent)}
	for _, x := range st.recent {
		if x.failed() {
			s.Failures++
		}
		if x.verified() {
			s.Verified++
		}
		if x.fakeSuccess() {
			s.FakeSuccesses++
		}
		if x.fakeFailure() {
			s.FakeFailures++
		}
	}
	s.FailureRate = rate(s.Failures, s.Samples)
	// Recomputed from the ring (not a running counter) so a LATE verdict — a
	// reported failure proven landed — actually breaks the streak instead of
	// leaving a stale 3 that keeps the window red forever.
	for i := len(st.recent) - 1; i >= 0 && st.recent[i].failed(); i-- {
		s.ConsecutiveFailures++
	}
	for i := len(st.recent) - 1; i >= 0; i-- {
		if st.recent[i].failed() {
			s.LastFailureAction = st.recent[i].action
			s.LastFailureAt = st.recent[i].at.Format(time.RFC3339)
			s.LastFailureError = st.recent[i].err
			if st.recent[i].fakeSuccess() {
				s.LastFailureError = "reported ok, but read-back proved it did not land"
			}
			break
		}
	}
	var minSeq uint64
	if st.seq > writeHealthActionHorizon {
		minSeq = st.seq - writeHealthActionHorizon
	}
	for action, samples := range st.byAction {
		a := snapshotActionLocked(samples, minSeq)
		if a.Samples == 0 {
			continue
		}
		if a.Degraded {
			s.DegradedActions = append(s.DegradedActions, action)
		}
		if a.Degraded || a.Failures > 0 {
			if s.Actions == nil {
				s.Actions = map[string]ActionWriteHealth{}
			}
			s.Actions[action] = a
		}
	}
	sort.Strings(s.DegradedActions)
	s.Degraded = (s.Samples >= writeHealthMinSamples && s.FailureRate >= writeHealthDegradedRate) ||
		s.ConsecutiveFailures >= writeHealthConsecFails ||
		len(s.DegradedActions) > 0
	return s
}

// snapshot returns one window's health (zero-value when never observed).
func (t *writeHealthTracker) snapshot(windowID string) WindowWriteHealth {
	if t == nil {
		return WindowWriteHealth{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.byWindow[windowID]
	if st == nil {
		return WindowWriteHealth{}
	}
	return snapshotLocked(st)
}

// all returns every observed window's health, for /health. nil when empty so
// the JSON field is omitted on a quiet daemon.
func (t *writeHealthTracker) all() map[string]WindowWriteHealth {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.byWindow) == 0 {
		return nil
	}
	out := make(map[string]WindowWriteHealth, len(t.byWindow))
	for id, st := range t.byWindow {
		out[id] = snapshotLocked(st)
	}
	return out
}

// ── 通道 A:从响应里挖回读证据 ────────────────────────────────────────────────

// effectFromResponse mines the response ITSELF for the connector's own read-back
// verdict. Several handlers already re-read the page after writing and report
// what survived / was not applied (the #151 partial convention, issue #164's
// verified delete) — that evidence used to die in the JSON while the daemon
// counted the call as a clean success.
//
// Only NEGATIVE evidence lives in a response: "ok plus no complaint" is not
// proof the write landed, so the absence of these keys stays effectUnknown.
// Positive (landed) verdicts come from 通道 B, where a command actually read the
// canvas back.
func effectFromResponse(req *protocol.Request, resp *protocol.Response) effectVerdict {
	if req == nil || resp == nil || !resp.OK || resp.Result == nil {
		return effectUnknown
	}
	if !requestMutates(req) {
		return effectUnknown // a read has no "effect" to verify
	}
	// #151 partial-application convention: the canvas changed, but not fully.
	if b, ok := resp.Result["partial"].(bool); ok && b {
		return effectNotLanded
	}
	// issue #164: primitives.delete re-reads and reports the survivors.
	if n, ok := numberField(resp.Result["survivedTotal"]); ok && n > 0 {
		return effectNotLanded
	}
	if nonEmptyCollection(resp.Result["survived"]) || nonEmptyCollection(resp.Result["survivedIds"]) {
		return effectNotLanded
	}
	if nonEmptyCollection(resp.Result["notApplied"]) {
		return effectNotLanded
	}
	// component.delete / pin.disconnect report a plain boolean verdict.
	for _, key := range []string{"deleted", "disconnected"} {
		if b, ok := resp.Result[key].(bool); ok && !b {
			return effectNotLanded
		}
	}
	return effectUnknown
}

func numberField(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func nonEmptyCollection(v any) bool {
	switch c := v.(type) {
	case []any:
		return len(c) > 0
	case []string:
		return len(c) > 0
	case map[string]any:
		return len(c) > 0
	}
	return false
}

// ── 通道 B:/writeverify 上报端点 ─────────────────────────────────────────────

// handleWriteVerify accepts a command's out-of-band read-back verdict and folds
// it into the rolling health window. Deliberately NOT a typed action: the
// verdict lands after the action's response (often several reads later), often
// covers MANY calls at once, and adding an action would drag the connector
// (.eext re-import) into a change that is purely daemon-local.
func (s *Server) handleWriteVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var v WriteVerification
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "invalid write-verification body: " + err.Error()})
		return
	}
	if strings.TrimSpace(v.Action) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "action is required"})
		return
	}
	if v.Landed <= 0 && v.NotLanded <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "landed and/or notLanded must be > 0"})
		return
	}
	windowID := strings.TrimSpace(v.WindowID)
	if windowID == "" && v.Project != "" {
		if id, found, ambiguous := s.hub.windowForProject(v.Project, docTypeForAction(v.Action)); found && !ambiguous {
			windowID = id
		}
	}
	if windowID == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok": false, "error": "cannot attribute the verdict: pass windowId, or a project that resolves to exactly one window"})
		return
	}
	s.writeHealth.verify(windowID, v)
	if v.NotLanded > 0 {
		// 假成功在日志里必须留痕:它在响应层长得和成功一模一样,不打这一行,
		// 现场只能靠事后翻 /health 才知道刚才那批写其实没落地。
		s.logf("write verification: %s — %d of %d did NOT land on window %s (source %s)",
			v.Action, v.NotLanded, v.Landed+v.NotLanded, windowID, v.Source)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"windowId":    windowID,
		"writeHealth": s.writeHealth.snapshot(windowID),
	})
}

// annotateDegraded attaches the structured degraded advisory to a response the
// window's rolling health says it cannot trust: a FAILED response, or a response
// that returned ok while the connector's own read-back proved part of it did not
// land (假成功 — the class that used to sail through as a clean success).
// Purely additive — healthy windows are untouched.
func (t *writeHealthTracker) annotateDegraded(req *protocol.Request, resp *protocol.Response) {
	if t == nil || req == nil || resp == nil {
		return
	}
	fakeSuccess := resp.OK && effectFromResponse(req, resp) == effectNotLanded
	if resp.OK && !fakeSuccess {
		return
	}
	h := t.snapshot(req.WindowID)
	if !h.Degraded {
		return
	}
	if resp.Result == nil {
		resp.Result = map[string]any{}
	}
	deg := map[string]any{
		"degraded":            true,
		"recentFailureRate":   h.FailureRate,
		"recentSamples":       h.Samples,
		"consecutiveFailures": h.ConsecutiveFailures,
		"verifiedSamples":     h.Verified,
		"fakeSuccesses":       h.FakeSuccesses,
		"fakeFailures":        h.FakeFailures,
	}
	if len(h.DegradedActions) > 0 {
		deg["degradedActions"] = h.DegradedActions
	}
	resp.Result["degraded"] = deg

	advice := "connector looks DEGRADED under load (recent EFFECT failure rate %.0f%%, %d consecutive failures)"
	switch {
	case fakeSuccess:
		advice += " — this call returned ok but the connector's own re-read proves part of it did NOT land (假成功): treat it as a failed write, re-read and re-apply the missing part"
	case requestMutates(req):
		advice += " — this write may have LANDED despite the failure (假失败定律): verify with a light read before resending, and do NOT blind-retry"
	default:
		advice += " — insert a light read + short pause before retrying; consider `easyeda doc reload` if this persists"
	}
	if len(h.DegradedActions) > 0 {
		advice += fmt.Sprintf(". Worst road(s): %s", strings.Join(h.DegradedActions, ", "))
	}
	resp.Warnings = append(resp.Warnings, fmt.Sprintf(advice, h.FailureRate*100, h.ConsecutiveFailures))
}

// dispatchFn matches (*conn).dispatch — injected so the retry protocol is
// unit-testable without a live WebSocket.
type dispatchFn func(ctx context.Context, req protocol.Request) (*protocol.Response, error)

// adaptiveHooks are the side effects of the retry protocol, injected for tests.
type adaptiveHooks struct {
	// observe records one attempt's outcome in the rolling health window.
	observe func(o outcome)
	// auditFirst logs the superseded first attempt so audit-derived failure
	// rates keep seeing the degradation the retry papers over.
	auditFirst func(resp *protocol.Response, err error)
	sleep      func(d time.Duration)
}

// forwardWithAdaptiveRetry forwards one request; on failure of a whitelisted
// idempotent action it inserts a light read + settle delay and retries ONCE.
//
//	原失败 ──不在白名单──────────────────────────▶ 透传(上层按 degraded 提示自理)
//	原失败 ──白名单──▶ 轻读探测 ──探测也失败──▶ 透传(连接器停摆,不加压)
//	                        └──探测 OK──▶ settle ──▶ 重发一次(幂等,重发无害)
func forwardWithAdaptiveRetry(ctx context.Context, req protocol.Request, dispatch dispatchFn, h adaptiveHooks) (*protocol.Response, error, bool) {
	resp, err := dispatch(ctx, req)
	ok := err == nil && resp != nil && resp.OK
	if h.observe != nil {
		h.observe(outcomeOf(&req, resp, err))
	}
	if ok || errors.Is(err, errConnectorDisconnected) || !retryableOnFailure[req.Action] {
		return resp, err, false
	}

	// Light-read probe: proves the connector is answering at all, and measurably
	// improves the retry's odds on a load-degraded connector.
	probe := protocol.Request{
		Envelope: protocol.Envelope{
			ID:        req.ID + "_probe",
			Type:      protocol.TypeRequest,
			Version:   req.Version,
			WindowID:  req.WindowID,
			CreatedAt: time.Now().UTC(),
		},
		Action: backoffProbeAction,
	}
	pctx, cancel := context.WithTimeout(ctx, backoffProbeTimeout)
	presp, perr := dispatch(pctx, probe)
	cancel()
	if perr != nil || presp == nil || !presp.OK {
		// Cannot even read → the connector is wedged; pass the original failure
		// through rather than piling more load on (恢复探测用轻读非重发).
		return resp, err, false
	}

	if h.auditFirst != nil {
		h.auditFirst(resp, err)
	}
	if h.sleep != nil {
		h.sleep(backoffSettleDelay)
	}
	resp2, err2 := dispatch(ctx, req)
	ok2 := err2 == nil && resp2 != nil && resp2.OK
	if h.observe != nil {
		h.observe(outcomeOf(&req, resp2, err2))
	}
	if ok2 {
		resp2.Warnings = append(resp2.Warnings, fmt.Sprintf(
			"%s failed once and was auto-retried by the daemon after a light read + %s settle (connector load degradation, idempotent action)",
			req.Action, backoffSettleDelay))
	}
	return resp2, err2, true
}
