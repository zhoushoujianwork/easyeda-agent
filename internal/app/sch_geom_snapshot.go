package app

// sch_geom_snapshot.go — 一次只读命令内共享的几何快照。
//
// 立项数据(2026-08-16 `audit cost` 首测):`schematic.components.list` 吃掉
// **41% 的 daemon 时间**(676s / 527 次)。而一次 `sch gate --strict` 就打 3 发,
// 其中两发 payload 完全相同:
//
//	{includeBBox, includePins}   ← layout-lint
//	{includeBBox, includePins}   ← clusters
//	{includeBBox}                ← check 的 marker 几何规则
//
// 三关判的是**同一张画布的同一时刻** —— gate 全程只读。6 个器件的页上这 3 发是
// 0.93s,但 includePins 的代价随引脚数涨(81 脚模组单次实测 18s),同一页就是 54s。
//
// **为什么是显式传递而不是 dispatch 层的隐式缓存**:隐式版本试过并回退了,两个致命
// 问题 ——(1)fake-dispatcher 测试靠「每次注入不同响应」工作,缓存直接让 12 个测试
// 失效;(2)`debug.exec_js` 被标记为写动作(它确实能改任何东西),而 gate 各关之间
// 就夹着 exec_js,缓存每次都被清空,真机一点没省。
//
// 显式快照没有失效问题可谈:它的作用域就是一次只读流程,读完即用完,调用方一眼能看出
// 数据从哪来。nil 快照 = 各自去读。clusters 的导线、pins 和 marker 必须来自同一
// 含 includeWires 的快照,不能把两次读取拼成无接点交叉的证据。

import "fmt"

// schGeomSnapshot 是一次只读流程内的 components.list 快照。
// **nil 是合法值**,表示「没有预读,自己去拿」—— 单命令路径就走这条。
type schGeomSnapshot struct {
	comps []layoutComp
	res   *actionResult
	err   error
	// flags 记录这份快照是用什么参数读的,便于 compsOr 判定它能不能服务某次请求。
	withPins  bool
	withWires bool
	allPages  bool
}

// gatePreloadGeometry 预读一次最宽的几何(bbox + pins + wires)。读失败**不报错**:
// 各 stage 会各自去读并给出它自己的错误信息 —— 预读只是优化,不是新的失败点。
func gatePreloadGeometry(cfg *appConfig, window string, allPages bool) *schGeomSnapshot {
	payload := map[string]any{"includeBBox": true, "includePins": true, "includeWires": true}
	if allPages {
		payload["allPages"] = true
	}
	snap := &schGeomSnapshot{withPins: true, withWires: true, allPages: allPages}
	res, err := requestAction(cfg, "schematic.components.list", window, payload)
	if err != nil {
		snap.err = err
		return snap
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		snap.err = perr
		return snap
	}
	snap.res, snap.comps = res, comps
	return snap
}

// covers 判这份快照能不能服务一次请求:**参数超集**才行。缓存了 {bbox,pins} 可以
// 服务 {bbox}(多读的字段不影响少读的判据),反之不行 —— 拿没有引脚的结果去做
// 引脚判据,会静默判出「零个引脚问题」。allPages 必须完全一致:它改变的是返回集合。
func (s *schGeomSnapshot) covers(payload map[string]any) bool {
	if s == nil || s.err != nil || s.comps == nil {
		return false
	}
	wantWires, _ := payload["includeWires"].(bool)
	if wantWires && !s.withWires {
		return false
	}
	wantPins, _ := payload["includePins"].(bool)
	wantAll, _ := payload["allPages"].(bool)
	if wantPins && !s.withPins {
		return false
	}
	return wantAll == s.allPages
}

// compsOr 返回快照里的器件;快照不适用(nil / 读失败 / 参数不够)就现读一次。
func (s *schGeomSnapshot) compsOr(cfg *appConfig, window string, payload map[string]any) ([]layoutComp, error) {
	if s.covers(payload) {
		return s.comps, nil
	}
	res, err := requestAction(cfg, "schematic.components.list", window, payload)
	if err != nil {
		return nil, err
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		return nil, fmt.Errorf("parse components: %w", perr)
	}
	return comps, nil
}

// resultOr 同 compsOr,但返回原始 actionResult(有些判据要读 result 里的别的字段)。
func (s *schGeomSnapshot) resultOr(cfg *appConfig, window string, payload map[string]any) (*actionResult, error) {
	if s.covers(payload) && s.res != nil {
		return s.res, nil
	}
	return requestAction(cfg, "schematic.components.list", window, payload)
}
