package app

import "testing"

// 快照的全部风险都在 covers 上:**多判一次命中,就是拿错数据去做判据**,而且是静默的
// (没有引脚的结果拿去做引脚判据,会安静地判出「零个引脚问题」)。所以这里测的
// 几乎全是「不该命中」。

func TestSchGeomSnapshot_NilAndFailedNeverCover(t *testing.T) {
	var nilSnap *schGeomSnapshot
	if nilSnap.covers(map[string]any{"includeBBox": true}) {
		t.Error("nil 快照不该命中 —— 单命令路径靠它退回自己读")
	}
	failed := &schGeomSnapshot{err: errStub, withPins: true}
	if failed.covers(map[string]any{"includeBBox": true}) {
		t.Error("读失败的快照不该命中")
	}
	empty := &schGeomSnapshot{withPins: true} // comps == nil
	if empty.covers(map[string]any{"includeBBox": true}) {
		t.Error("没有 comps 的快照不该命中")
	}
}

func TestSchGeomSnapshot_PinsSupersetOnly(t *testing.T) {
	withPins := &schGeomSnapshot{comps: []layoutComp{{}}, withPins: true}
	if !withPins.covers(map[string]any{"includeBBox": true}) {
		t.Error("{bbox,pins} 的快照该能服务 {bbox} 的请求")
	}
	if !withPins.covers(map[string]any{"includeBBox": true, "includePins": true}) {
		t.Error("同参数该命中")
	}
	noPins := &schGeomSnapshot{comps: []layoutComp{{}}, withPins: false}
	if noPins.covers(map[string]any{"includeBBox": true, "includePins": true}) {
		t.Fatal("不带引脚的快照绝不能服务要引脚的请求 —— 会静默判出「零个引脚问题」")
	}
	if !noPins.covers(map[string]any{"includeBBox": true}) {
		t.Error("同为 {bbox} 该命中")
	}
}

func TestSchGeomSnapshot_AllPagesMustMatchExactly(t *testing.T) {
	// allPages 改变的是**返回集合**,不是字段多少 —— 两个方向都不能替代。
	single := &schGeomSnapshot{comps: []layoutComp{{}}, withPins: true, allPages: false}
	if single.covers(map[string]any{"includeBBox": true, "allPages": true}) {
		t.Error("单页快照不该服务 allPages 请求(会漏掉别的页)")
	}
	all := &schGeomSnapshot{comps: []layoutComp{{}}, withPins: true, allPages: true}
	if all.covers(map[string]any{"includeBBox": true}) {
		t.Error("allPages 快照不该服务单页请求(会把别页的件算进来)")
	}
}

func TestSchGeomSnapshot_WiresNeedExplicitSuperset(t *testing.T) {
	s := &schGeomSnapshot{comps: []layoutComp{{}}, withPins: true}
	if s.covers(map[string]any{"includePins": true, "includeWires": true}) {
		t.Fatal("pins-only snapshot cannot prove crossings")
	}
	s.withWires = true
	if !s.covers(map[string]any{"includePins": true, "includeWires": true}) || !s.covers(map[string]any{"includePins": true}) {
		t.Fatal("full snapshot should serve subset requests")
	}
}

var errStub = stubErr("read failed")

type stubErr string

func (e stubErr) Error() string { return string(e) }
