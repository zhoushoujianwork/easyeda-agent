package daemon

// writestall.go — 写超时的恢复信息(issue #317)。
//
// 现场形态:单个 EasyEDA 宿主会话累计数百次写之后,`createNetFlag` 等写调用在
// 宿主内部不再 settle;连接器按预算放弃队首、FIFO 继续流动,所以只读命令照常
// 秒回,而每一条写都烧满预算后报 `DISPATCH_FAILED: connector did not respond`。
// 重启 daemon、重连连接器都无效,只有重启宿主恢复。
//
// 预算本身已经存在(CLI 20s → daemon 预算 - 2s → 连接器 + 2s 放弃队首,见
// dispatch.go / extension/src/action-queue.ts),缺的是**可执行的恢复信息**:
// 通用的 "connector did not respond" 让调用方误以为是参数或连接问题。这里只做
// 诊断,不改错误码(调用方按 DISPATCH_FAILED 分支),不拦截、不重试:
//
//   - 任何一次写超时:写状态未知(宿主调用不可取消,可能稍后落地)→ 不要重发,
//     先回读;
//   - 连续写超时且其间有读成功:证据指向宿主写通道卡住,而不是连接器或参数 →
//     停批、尝试保存、重启 EasyEDA 宿主后回读再续跑。
//
// 只用 daemon 自己转发过的样本作证据;探针和旁路读不进样本。

import (
	"fmt"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// writeStallMinTimeouts is the consecutive-write-timeout count that, with a
// successful read in between, names the host write path as stalled.
const writeStallMinTimeouts = 2

// WriteStall is the structured advisory attached to a timed-out write.
type WriteStall struct {
	Action                   string `json:"action"`
	BudgetMs                 int64  `json:"budgetMs"`
	WriteState               string `json:"writeState"`
	ConsecutiveWriteTimeouts int    `json:"consecutiveWriteTimeouts"`
	ReadsAnsweredSince       int    `json:"readsAnsweredSince"`
	// Suspected is "write-timeout" for an isolated timeout, or
	// "host-write-path-stalled" when writes keep timing out while reads answer.
	Suspected string   `json:"suspected"`
	Recovery  []string `json:"recovery"`
}

// writeStallLocked walks the window ring newest-first: it counts the unbroken
// run of timed-out writes and the successful reads that came after the oldest
// of them. Any write that did not time out ends the run. Caller holds mu.
func writeStallLocked(st *windowHealthState) (timeouts, readsSince int) {
	pendingReads := 0
	for i := len(st.recent) - 1; i >= 0; i-- {
		s := st.recent[i]
		if !s.mutates {
			if s.ok {
				pendingReads++
			}
			continue
		}
		if !s.timedOut {
			break
		}
		timeouts++
		readsSince += pendingReads
		pendingReads = 0
	}
	return timeouts, readsSince
}

// annotateWriteTimeout attaches result.writeStall and a warning to the error
// response of a content write that got no answer within budget. Reads, dry
// runs and non-timeout failures are untouched.
func (t *writeHealthTracker) annotateWriteTimeout(req *protocol.Request, resp *protocol.Response, budget time.Duration) {
	if t == nil || req == nil || resp == nil || !requestMutates(req) {
		return
	}
	t.mu.Lock()
	timeouts, reads := 0, 0
	if st := t.byWindow[req.WindowID]; st != nil {
		timeouts, reads = writeStallLocked(st)
	}
	t.mu.Unlock()
	if timeouts == 0 {
		return
	}
	stall := WriteStall{
		Action:                   req.Action,
		BudgetMs:                 budget.Milliseconds(),
		WriteState:               "unknown",
		ConsecutiveWriteTimeouts: timeouts,
		ReadsAnsweredSince:       reads,
		Suspected:                "write-timeout",
		Recovery: []string{
			"do not resend this write: the EasyEDA call cannot be cancelled and may still land late",
			"read back the target (e.g. `easyeda sch list` on the page) and re-issue only what is proven missing; playbooks: reconcile, then `easyeda apply --resume`",
		},
	}
	warning := fmt.Sprintf("%s got no answer within %dms; write state is UNKNOWN (it may still land) — read back before any retry",
		req.Action, stall.BudgetMs)
	if timeouts >= writeStallMinTimeouts && reads > 0 {
		stall.Suspected = "host-write-path-stalled"
		stall.Recovery = append(stall.Recovery,
			"stop the batch: writes keep timing out while reads answer, so the EasyEDA host write path is stalled (seen after hundreds of writes in one host session); restarting the daemon or reconnecting the connector does not clear it",
			"try `easyeda sch save` (or `easyeda pcb save`), restart EasyEDA Pro, confirm `easyeda health`, read back the page, then resume",
		)
		warning = fmt.Sprintf("%d consecutive writes timed out while %d read(s) answered: the EasyEDA host write path looks STALLED. Stop writing, save if possible, restart EasyEDA Pro, read back, then resume — %s",
			timeouts, reads, warning)
	}
	if resp.Result == nil {
		resp.Result = map[string]any{}
	}
	resp.Result["writeStall"] = stall
	resp.Warnings = append(resp.Warnings, warning)
}
