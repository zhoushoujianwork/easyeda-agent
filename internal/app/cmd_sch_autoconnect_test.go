package app

import (
	"math"
	"strings"
	"testing"
)

// clearScene is an empty world: no parts, pins, flags, or title block. A pin in
// open space — every penalty is zero, so only offset cost + direction bonuses
// drive selection.
func clearScene() acScene { return acScene{} }

func rulesFor() autoconnectRules { return defaultAutoconnectRules() }

func TestEndpointFor_MatchesConnectorYUp(t *testing.T) {
	cases := []struct {
		dir   string
		wantX float64
		wantY float64
	}{
		{"up", 100, 130},  // y increases
		{"down", 100, 70}, // y decreases
		{"left", 70, 100}, // x decreases
		{"right", 130, 100},
	}
	for _, c := range cases {
		x, y := endpointFor(100, 100, 30, c.dir)
		if x != c.wantX || y != c.wantY {
			t.Errorf("dir %s: got (%.0f,%.0f), want (%.0f,%.0f)", c.dir, x, y, c.wantX, c.wantY)
		}
	}
}

func TestPlanConnection_GroundPrefersDownShortest(t *testing.T) {
	// A pin below its owner center → outward = down; kind gnd default = down.
	// Both bonuses stack on 'down', and the shortest offset wins ties.
	pin := acPin{X: 100, Y: 140, OwnerBBox: bb(80, 150, 120, 190)}
	all := planConnection(pin, "ground", "GND", clearScene(), rulesFor(), 0)
	sel := all[0]
	if sel.Direction != "down" {
		t.Fatalf("expected down (outward + kind default), got %s", sel.Direction)
	}
	if sel.Offset != 18 {
		t.Errorf("expected shortest offset 18, got %.0f", sel.Offset)
	}
	// down should score below any up candidate (up gets neither bonus here).
	for _, c := range all {
		if c.Direction == "up" && c.Offset == sel.Offset && c.Score <= sel.Score {
			t.Errorf("up should cost more than down at equal offset: down=%.2f up=%.2f", sel.Score, c.Score)
		}
	}
}

func TestScoreCandidate_PartOverlapDominates(t *testing.T) {
	// A part bbox sits right where the 'down' endpoint would land → +10000.
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Parts: []layoutBBox{bb(90, 65, 110, 85).deref()}}
	c := scoreCandidate(pin, "down", 24, "ground", "GND", scene, rulesFor())
	if c.Score < costPartOverlap {
		t.Fatalf("expected part-overlap penalty (>=%d), got %.2f", costPartOverlap, c.Score)
	}
}

func TestScoreCandidate_StubCrossesPin(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	// Another pin sits on the downward stub path (x=100, between y=70 and 100).
	scene := acScene{Pins: []acPin{{X: 100, Y: 85, Designator: "U2", PinNumber: "1"}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
	hasCross := false
	for _, r := range c.Reasons {
		if r.Cost == costPinCross {
			hasCross = true
		}
	}
	if !hasCross {
		t.Fatalf("expected a pin-cross penalty, reasons=%+v", c.Reasons)
	}
}

// TestScoreCandidate_TitleBlockIsHardReject (issue #147): a label landing in the
// A4 title-block keep-out must be a HARD reject, not a soft cost — otherwise a
// netport dropped on the 图签 wins whenever every other direction is hard-rejected.
func TestScoreCandidate_TitleBlockIsHardReject(t *testing.T) {
	pin := acPin{X: 1025, Y: 95}
	// keep-out from issue #147: {912.6,0,1170,115.5}.
	scene := acScene{TitleBlock: bb(912.6, 0, 1170, 115.5)}
	c := scoreCandidate(pin, "down", 12, "ground", "MOTOR_G", scene, rulesFor())
	if !candidateHardRejected(c) {
		t.Fatalf("title-block intrusion must hard-reject, reasons=%+v score=%.2f", c.Reasons, c.Score)
	}
}

// TestScoreCandidate_TitleBlockClearNotRejected: a label safely OUTSIDE the title
// block is not rejected (the hard reject is scoped to real intrusions).
func TestScoreCandidate_TitleBlockClearNotRejected(t *testing.T) {
	pin := acPin{X: 300, Y: 400}
	scene := acScene{TitleBlock: bb(912.6, 0, 1170, 115.5)}
	c := scoreCandidate(pin, "up", 18, "power", "VCC", scene, rulesFor())
	if candidateHardRejected(c) {
		t.Fatalf("a label clear of the title block must not hard-reject, reasons=%+v", c.Reasons)
	}
}

// netport 的**本体**是平台实测的六边形(恒 31,与网名无关),名字画在本体外面 ——
// 所以跟着网名变长的是 predictedMarkerBBox(本体 ∪ 文字带),不是 body。
// 实测佐证:同一页里 `C7_N3` 与 `USB_DTR` 的平台 bbox 一模一样,都是 31×11。
func TestPredictedMarkerBBox_NetPortWidthTracksNetName(t *testing.T) {
	short := predictedMarkerBody(100, 200, "net_port_bi", "right", "N1")
	long := predictedMarkerBody(100, 200, "net_port_bi", "right", "C7_N3")
	if short != long {
		t.Errorf("本体是六边形,不该跟网名变: short=%v long=%v", short, long)
	}
	sb := predictedMarkerBBox(100, 200, "net_port_bi", "right", "N1")
	lb := predictedMarkerBBox(100, 200, "net_port_bi", "right", "C7_N3")
	if !(lb.MaxX > sb.MaxX) {
		t.Errorf("总占地必须跟着网名变长: short=%v long=%v", sb.MaxX, lb.MaxX)
	}
	if got, want := lb.MaxX-100, 9.5+acPortTotalLen("C7_N3"); got != want {
		t.Errorf("netport 总伸出 = %v, want %v(六边形 + 名字)", got, want)
	}
	// ground/power 是固定符号,不该跟网名走
	g1 := predictedMarkerBody(100, 200, "ground", "right", "GND")
	g2 := predictedMarkerBody(100, 200, "ground", "right", "A_VERY_LONG_GROUND_NAME")
	if g1 != g2 {
		t.Errorf("ground 是固定尺寸符号,不该跟网名变: %v vs %v", g1, g2)
	}
}

// ⚠ 竖直两支的期望值 2026-08-14 按**真机实测**订正过:在 ceshi 用
// `sch connect --x 200 --y 200 --kind gnd --direction down --offset 40` 落一支旗,
// 端点 (200,160),读回来的真实 bbox 是 y 140.5..150.5 —— body 在端点**下方**
// (Near 9.5 / Far 19.5,与 ground profile 完全吻合)。此前 up/down 两支写反,
// 而这条测试(名字就叫 live calibration)把反的行为锁住了:于是所有竖直方向的
// marker 碰撞检查都在一个空位置上做,朝下的 GND 旗互相重合而评分器毫无反应。
// left/right 一直是对的,所以只在电源/地旗(恰恰全是竖直的)上显形。
// 评分器用的框必须 = 符号本体 ∪ 文字带,与 `sch check` 的 marker-overlap 判定
// (flagTextBand)同一把尺。少算文字带 → 评分器挑的「干净」位置在 check 眼里
// 照样重叠,实测剩余那批重叠量里的 12.00 就是文字带高度本身。
func TestPredictedMarkerBBox_IncludesTextBandLikeSchCheck(t *testing.T) {
	const x, y = 100.0, 200.0
	for _, dir := range []string{"left", "right", "up", "down"} {
		body := predictedMarkerBody(x, y, "ground", dir, "GND")
		full := predictedMarkerBBox(x, y, "ground", dir, "GND")
		if full == body {
			t.Errorf("%s: 预测框没有并入文字带", dir)
		}
		// 文字带算法与 flagTextBand 逐字对齐:长 6*len(net),高 12
		band := predictedFlagTextBand(x, y, body, "ground", dir, "GND")
		if band == nil {
			t.Fatalf("%s: ground 必须有文字带", dir)
		}
		if got := band.MaxY - band.MinY; dir == "up" || dir == "down" {
			if got != 12 {
				t.Errorf("%s: 文字带高 %v, want 12", dir, got)
			}
		}
		if dir == "left" || dir == "right" {
			if got, want := band.MaxX-band.MinX, 6*float64(len("GND")); got != want {
				t.Errorf("%s: 文字带长 %v, want %v", dir, got, want)
			}
		}
	}
	// netport 的名字画在**本体外**,必须有文字带 —— 长度 = 总占地 − 六边形。
	pb := predictedMarkerBody(x, y, "net_port_bi", "right", "C7_N3")
	nb := predictedFlagTextBand(x, y, pb, "net_port_bi", "right", "C7_N3")
	if nb == nil {
		t.Fatal("netport 的名字画在本体外,必须有文字带")
	}
	if got, want := nb.MaxX-nb.MinX, acPortTotalLen("C7_N3")-acPortBodyLen; got != want {
		t.Errorf("netport 文字带长 %v, want %v", got, want)
	}
}

func TestPredictedMarkerBBox_MatchesLiveFamilyDirectionCalibration(t *testing.T) {
	tests := []struct {
		name      string
		kind      string
		direction string
		want      layoutBBox
	}{
		// netport 期望值按 net="N1" 给:relayoutPortWidth("N1")=20 落到下限 31,
		// 也就是这张表原本写死的那个宽度。长网名另有专测(见下一条)。
		// netport: live body is 31×11 horizontally / 11×31 vertically,
		// starting 9.5 units beyond the endpoint and extending to 40.5.
		{"netport-left", "net_port_bi", "left", layoutBBox{59.5, 194.5, 90.5, 205.5}},
		{"netport-right", "net_port_bi", "right", layoutBBox{109.5, 194.5, 140.5, 205.5}},
		{"netport-up", "net_port_bi", "up", layoutBBox{94.5, 209.5, 105.5, 240.5}},
		{"netport-down", "net_port_bi", "down", layoutBBox{94.5, 159.5, 105.5, 190.5}},

		// ground: 10×21 horizontally / 21×10 vertically, 9.5..19.5 outward.
		{"ground-left", "ground", "left", layoutBBox{80.5, 189.5, 90.5, 210.5}},
		{"ground-right", "ground", "right", layoutBBox{109.5, 189.5, 119.5, 210.5}},
		{"ground-up", "ground", "up", layoutBBox{89.5, 209.5, 110.5, 219.5}},
		{"ground-down", "ground", "down", layoutBBox{89.5, 180.5, 110.5, 190.5}},

		// power: 6×11 horizontally / 11×6 vertically, 4.5..10.5 outward.
		{"power-left", "power", "left", layoutBBox{89.5, 194.5, 95.5, 205.5}},
		{"power-right", "power", "right", layoutBBox{104.5, 194.5, 110.5, 205.5}},
		{"power-up", "power", "up", layoutBBox{94.5, 204.5, 105.5, 210.5}},
		{"power-down", "power", "down", layoutBBox{94.5, 189.5, 105.5, 195.5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := predictedMarkerBody(100, 200, tt.kind, tt.direction, "N1")
			if got != tt.want {
				t.Fatalf("predictedMarkerBBox = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// The endpoint-centered 24×11 predictor missed a real netport body that starts
// away from the endpoint. Lock the scorer to the direction-shifted live bbox:
// right@(100,100) occupies x=109.5..140.5, so a part at x=120..130 must collide.
func TestScoreCandidate_UsesDirectionShiftedNetportBBox(t *testing.T) {
	pin := acPin{X: 80, Y: 100}
	scene := acScene{Parts: []layoutBBox{{MinX: 120, MinY: 96, MaxX: 130, MaxY: 104}}}
	c := scoreCandidate(pin, "right", 20, "net_port_bi", "SIG", scene, rulesFor())
	hasPartOverlap := false
	for _, r := range c.Reasons {
		if r.Cost == costPartOverlap {
			hasPartOverlap = true
		}
	}
	if !hasPartOverlap {
		t.Fatalf("direction-shifted netport body must overlap the part, reasons=%+v", c.Reasons)
	}
}

// TestScoreCandidate_MarkerHeightTriggersStaggerAt10Pitch (issue #148 Phase-2):
// the real ~11-tall marker box must overlap a neighbour's box at 10-unit pitch so
// the scorer's flag-collision penalty fires and drives auto-stagger. The old 8×8
// box never overlapped at 10 pitch, so parallel markers stacked silently.
func TestScoreCandidate_MarkerHeightTriggersStaggerAt10Pitch(t *testing.T) {
	// A marker already placed to the left of pin1 (endpoint 82,200), registered.
	scene := acScene{Flags: []layoutBBox{predictedMarkerBBox(82, 200, "ground", "left", "GND")}}
	// pin2 sits 10 above pin1; its left marker at the SAME offset lands at (82,210).
	pin2 := acPin{X: 100, Y: 210}
	c := scoreCandidate(pin2, "left", 18, "ground", "N2", scene, rulesFor())
	hasCollision := false
	for _, r := range c.Reasons {
		if r.Cost == costFlagCollision {
			hasCollision = true
		}
	}
	if !hasCollision {
		t.Fatalf("11-tall markers at 10 pitch must collide (stagger trigger), reasons=%+v", c.Reasons)
	}
}

// TestPlanConnection_StaggersAwayFromRegisteredMarker: given a registered marker,
// the planner's best candidate must avoid overlapping it (stagger to another
// offset/direction), not stack on top with only a soft penalty.
func TestPlanConnection_StaggersAwayFromRegisteredMarker(t *testing.T) {
	scene := acScene{Flags: []layoutBBox{predictedMarkerBBox(82, 200, "ground", "left", "GND")}}
	pin2 := acPin{X: 100, Y: 210}
	sel := planConnection(pin2, "ground", "N2", scene, rulesFor(), 0)[0]
	for _, r := range sel.Reasons {
		if r.Cost == costFlagCollision {
			t.Errorf("selected candidate should stagger away from the registered marker, got collision: %+v", sel)
		}
	}
}

// TestScoreCandidate_PinCrossIsHardReject: a stub crossing a non-target pin must
// be a HARD reject (issue #64), not a soft penalty a long offset could out-vote.
func TestScoreCandidate_PinCrossIsHardReject(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Pins: []acPin{{X: 100, Y: 85, Designator: "U2", PinNumber: "1"}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
	if !candidateHardRejected(c) {
		t.Fatalf("pin-cross should hard-reject, score=%.2f reasons=%+v", c.Score, c.Reasons)
	}
}

// TestScoreCandidate_StubTouchesForeignWireHardRejects: a stub whose endpoint or
// path lands on an existing wire of a DIFFERENT net is a silent net merge — must
// hard-reject (issue #64).
func TestScoreCandidate_StubTouchesForeignWireHardRejects(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	// A +5V wire runs horizontally across y=70; the downward stub endpoint (100,70)
	// lands on it → foreign-net junction.
	scene := acScene{Wires: []wireSegment{{X0: 50, Y0: 70, X1: 200, Y1: 70, Net: "+5V"}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
	if !candidateHardRejected(c) {
		t.Fatalf("stub touching a foreign-net wire should hard-reject, reasons=%+v", c.Reasons)
	}
}

// TestScoreCandidate_SameNetWireNotRejected: touching a wire ALREADY on the
// target net is the whole point of connecting — it must NOT hard-reject.
func TestScoreCandidate_SameNetWireNotRejected(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Wires: []wireSegment{{X0: 50, Y0: 70, X1: 200, Y1: 70, Net: "GND"}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
	if candidateHardRejected(c) {
		t.Fatalf("same-net wire touch must NOT hard-reject, reasons=%+v", c.Reasons)
	}
}

// TestScoreCandidate_UnnamedWireIsForeign: a wire with no resolvable net is
// treated conservatively as foreign — touching it hard-rejects.
func TestScoreCandidate_UnnamedWireIsForeign(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Wires: []wireSegment{{X0: 50, Y0: 70, X1: 200, Y1: 70, Net: ""}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
	if !candidateHardRejected(c) {
		t.Fatalf("unnamed (foreign) wire touch should hard-reject, reasons=%+v", c.Reasons)
	}
}

// TestPlanConnection_AvoidsForeignWireDirection: with a foreign-net wire blocking
// the 'down' endpoint but the other directions clear, the planner must pick a
// non-rejected direction.
func TestPlanConnection_AvoidsForeignWireDirection(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Wires: []wireSegment{{X0: 50, Y0: 70, X1: 200, Y1: 70, Net: "+5V"}}}
	all := planConnection(pin, "ground", "GND", scene, rulesFor(), 0)
	if candidateHardRejected(all[0]) {
		t.Fatalf("planner picked a hard-rejected candidate: %+v", all[0])
	}
}

// TestBuildScene_ParsesWires verifies wires flow from the extension payload into
// the scene (issue #64).
func TestBuildScene_ParsesWires(t *testing.T) {
	result := map[string]any{
		"components": []any{},
		"wires": []any{
			map[string]any{"x0": 10.0, "y0": 20.0, "x1": 30.0, "y1": 20.0, "net": "+5V"},
			map[string]any{"x0": 30.0, "y0": 20.0, "x1": 30.0, "y1": 40.0, "net": ""},
		},
	}
	scene := buildScene(result)
	if len(scene.Wires) != 2 {
		t.Fatalf("expected 2 wire segments, got %d", len(scene.Wires))
	}
	if scene.Wires[0].Net != "+5V" || scene.Wires[0].X1 != 30 {
		t.Errorf("wire 0 parsed wrong: %+v", scene.Wires[0])
	}
	if scene.Wires[1].Net != "" {
		t.Errorf("wire 1 net should be empty, got %q", scene.Wires[1].Net)
	}
}

func TestPlanConnection_AvoidsOverlappingDirection(t *testing.T) {
	// A wall of parts blocks 'down'; 'up' is clear. Planner must not pick down.
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Parts: []layoutBBox{bb(80, 0, 120, 90).deref()}}
	all := planConnection(pin, "ground", "GND", scene, rulesFor(), 0)
	if all[0].Direction == "down" {
		t.Fatalf("planner chose blocked direction down; scene=%+v score=%.2f", scene, all[0].Score)
	}
}

func TestPlanConnection_Deterministic(t *testing.T) {
	pin := acPin{X: 100, Y: 100, OwnerBBox: bb(80, 80, 120, 95)}
	a := planConnection(pin, "power", "+5V", clearScene(), rulesFor(), 0)
	b := planConnection(pin, "power", "+5V", clearScene(), rulesFor(), 0)
	if len(a) != len(b) {
		t.Fatalf("candidate count differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Direction != b[i].Direction || a[i].Offset != b[i].Offset || a[i].Score != b[i].Score {
			t.Fatalf("non-deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestPlanConnection_TieBreakStable(t *testing.T) {
	// No geometry, no owner bbox, kind with default 'down' → 'down' wins its
	// bonus, but among the OTHER three directions (all equal score per offset)
	// the lexical tie-break must order down<left<right<up. Confirm offsets too:
	// shortest first within a direction.
	pin := acPin{X: 0, Y: 0}
	all := planConnection(pin, "ground", "GND", clearScene(), rulesFor(), 0)
	if all[0].Direction != "down" || all[0].Offset != 18 {
		t.Fatalf("expected down@18 first, got %s@%.0f", all[0].Direction, all[0].Offset)
	}
	// Every 'down' candidate (cheapest direction) should precede the first
	// non-down candidate.
	firstNonDown := -1
	for i, c := range all {
		if c.Direction != "down" {
			firstNonDown = i
			break
		}
	}
	for i := 0; i < firstNonDown; i++ {
		if all[i].Direction != "down" {
			t.Fatalf("down block broken at %d: %s", i, all[i].Direction)
		}
	}
}

func TestCandidateOffsets_FineRangePlusStandardLanes(t *testing.T) {
	rules := autoconnectRules{OffsetMin: 18, OffsetMax: 80, OffsetStep: 6}
	got := candidateOffsets(rules, "netport", "N1")
	if got[0] != 18 {
		t.Errorf("first offset want 18, got %.0f", got[0])
	}
	// 细档还在(躲零碎障碍靠它):18..80 这一段每 6 一跳。
	has := func(v float64) bool {
		for _, o := range got {
			if math.Abs(o-v) < 0.01 {
				return true
			}
		}
		return false
	}
	for _, o := range []float64{18, 24, 72, 78} { // 18+10×6=78 是 ≤OffsetMax 的最后一档
		if !has(o) {
			t.Errorf("细档 %v 应该还在: %v", o, got)
		}
	}
	// **长短循环的标准档位必须常驻**:让开一支 marker 要越过它的整个占地(~85),
	// 这一档超出 OffsetMax,细档里根本没有 —— 真机 11 处 29×1 就是这么来的。
	step := laneStepFor("netport", "N1")
	for k := 1; k <= 3; k++ {
		if !has(18 + float64(k)*step) {
			t.Errorf("标准第 %d 档(%v)缺失: %v", k, 18+float64(k)*step, got)
		}
	}
	// 升序且不重复(评分器的 tie-break 依赖确定性顺序)。
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("档位必须严格升序去重: %v", got)
		}
	}
}

func TestResolvePinCoord(t *testing.T) {
	scene := acScene{Pins: []acPin{
		{X: 10, Y: 20, Designator: "U1", PinNumber: "41", PinName: "GND"},
		{X: 30, Y: 40, Designator: "U1", PinNumber: "3", PinName: "3V3"},
	}}
	// by number
	p, err := resolvePinCoord(scene, "U1:41")
	if err != nil || p.X != 10 || p.Y != 20 {
		t.Fatalf("U1:41 → %+v err=%v", p, err)
	}
	// by name
	p, err = resolvePinCoord(scene, "U1:3V3")
	if err != nil || p.X != 30 {
		t.Fatalf("U1:3V3 → %+v err=%v", p, err)
	}
	// not found
	if _, err := resolvePinCoord(scene, "U9:1"); err == nil {
		t.Error("expected error for missing pin")
	}
	// malformed
	if _, err := resolvePinCoord(scene, "U1"); err == nil {
		t.Error("expected error for malformed ref")
	}
}

func TestResolvePinCoord_OffPageHintWithPageInfo(t *testing.T) {
	// D7 is known to the scene (from --all-pages) but has no pins here — it lives
	// on page B. The error must name the page and point at `doc switch`, not blame
	// a typo.
	scene := acScene{
		Pins: []acPin{{X: 10, Y: 20, Designator: "U1", PinNumber: "1"}},
		Components: []acComponent{
			{Designator: "U1", HasPins: true},
			{Designator: "D7", HasPins: false, PageUuid: "0395abcd", PageName: "Page B"},
		},
	}
	_, err := resolvePinCoord(scene, "D7:2")
	if err == nil {
		t.Fatal("expected an off-page error for D7:2")
	}
	msg := err.Error()
	for _, want := range []string{"0395abcd", "Page B", "doc switch", "ANOTHER"} {
		if !strings.Contains(msg, want) {
			t.Errorf("off-page hint missing %q; got: %s", want, msg)
		}
	}
	if strings.Contains(msg, "not placed") {
		t.Errorf("off-page hint should NOT say 'not placed'; got: %s", msg)
	}
}

func TestResolvePinCoord_OffPageHintWithoutPageInfo(t *testing.T) {
	// Same off-page component, but the extension didn't supply page uuid/name.
	// Degrade to a generic switch hint — still not "not placed".
	scene := acScene{Components: []acComponent{{Designator: "D7", HasPins: false}}}
	_, err := resolvePinCoord(scene, "D7:2")
	if err == nil {
		t.Fatal("expected an off-page error for D7:2")
	}
	msg := err.Error()
	if !strings.Contains(msg, "doc switch") || !strings.Contains(msg, "ANOTHER") {
		t.Errorf("generic off-page hint should mention doc switch and ANOTHER page; got: %s", msg)
	}
	if strings.Contains(msg, "not placed") {
		t.Errorf("off-page hint should NOT say 'not placed'; got: %s", msg)
	}
}

func TestResolvePinCoord_TrulyNotPlacedKeepsGenericError(t *testing.T) {
	// A designator the scene has never heard of → keep the original "not placed"
	// diagnostic (real typo / unplaced part), NOT the off-page hint.
	scene := acScene{Components: []acComponent{{Designator: "U1", HasPins: true}}}
	_, err := resolvePinCoord(scene, "U9:1")
	if err == nil {
		t.Fatal("expected error for unknown designator")
	}
	if !strings.Contains(err.Error(), "not placed") {
		t.Errorf("unknown designator should keep the 'not placed' hint; got: %s", err.Error())
	}
}

func TestBuildScene_ClassifiesPrimitives(t *testing.T) {
	result := map[string]any{"components": []any{
		map[string]any{
			"componentType": "part", "designator": "U1",
			"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 10.0, "maxY": 10.0},
			"pins": []any{
				map[string]any{"pinNumber": "1", "pinName": "VCC", "x": 0.0, "y": 5.0},
			},
		},
		map[string]any{
			"componentType": "netflag",
			"bbox":          map[string]any{"minX": 50.0, "minY": 50.0, "maxX": 54.0, "maxY": 54.0},
		},
		map[string]any{
			"componentType": "sheet",
			"bbox":          map[string]any{"minX": -100.0, "minY": -100.0, "maxX": 400.0, "maxY": 300.0},
		},
	}}
	scene := buildScene(result)
	if len(scene.Parts) != 1 {
		t.Errorf("expected 1 part bbox, got %d", len(scene.Parts))
	}
	if len(scene.Pins) != 1 || scene.Pins[0].Designator != "U1" || scene.Pins[0].OwnerBBox == nil {
		t.Errorf("pin not attached to owner: %+v", scene.Pins)
	}
	if len(scene.Flags) != 1 {
		t.Errorf("expected 1 flag bbox, got %d", len(scene.Flags))
	}
	if len(scene.Components) != 1 || scene.Components[0].Designator != "U1" || !scene.Components[0].HasPins {
		t.Errorf("expected 1 component U1 with pins, got %+v", scene.Components)
	}
	if scene.TitleBlock == nil || scene.TitleBlockProvisional {
		t.Errorf("title block keep-out should be derived from sheet, got tb=%+v prov=%v", scene.TitleBlock, scene.TitleBlockProvisional)
	}
}

func TestBuildScene_ProvisionalWhenNoSheet(t *testing.T) {
	result := map[string]any{"components": []any{
		map[string]any{"componentType": "part", "designator": "R1",
			"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 5.0, "maxY": 5.0}},
	}}
	scene := buildScene(result)
	if scene.TitleBlock != nil || !scene.TitleBlockProvisional {
		t.Errorf("no sheet → provisional & no enforced keep-out, got tb=%+v prov=%v", scene.TitleBlock, scene.TitleBlockProvisional)
	}
}

// ── idempotency: three-state decision (issue #50) ───────────────────────────

func TestDecideConnState_ThreeStates(t *testing.T) {
	cases := []struct {
		name       string
		currentNet string
		netKnown   bool
		targetNet  string
		want       acConnState
	}{
		// Pin floating (net known, empty) → normal new connection.
		{"floating pin → new", "", true, "GND", acStateNew},
		// Pin already on the target net → skip (the core idempotency case).
		{"same net → already-connected", "GND", true, "GND", acStateAlreadyConnected},
		// Pin on a different net → conflict (default error, --replace overrides).
		{"different net → conflict", "+3V3", true, "GND", acStateConflict},
		// Netlist unavailable → can't prove idempotency, fall back to new.
		{"net unknown → new (fallback)", "GND", false, "GND", acStateNew},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideConnState(tc.currentNet, tc.netKnown, tc.targetNet)
			if got != tc.want {
				t.Errorf("decideConnState(%q, %v, %q) = %q, want %q",
					tc.currentNet, tc.netKnown, tc.targetNet, got, tc.want)
			}
		})
	}
}

// TestBuildScene_ParsesPinNet verifies the pin's current net flows from the
// extension payload into acPin: a string sets NetKnown, a null does not.
func TestBuildScene_ParsesPinNet(t *testing.T) {
	result := map[string]any{"components": []any{
		map[string]any{
			"componentType": "part", "designator": "U1",
			"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 10.0, "maxY": 10.0},
			"pins": []any{
				map[string]any{"pinNumber": "1", "pinName": "GND", "x": 0.0, "y": 5.0, "net": "GND"},
				map[string]any{"pinNumber": "2", "pinName": "IN", "x": 0.0, "y": 8.0, "net": ""},
				// net: nil → netlist unavailable for this pin.
				map[string]any{"pinNumber": "3", "pinName": "OUT", "x": 0.0, "y": 9.0, "net": nil},
				// no net key at all → also unknown.
				map[string]any{"pinNumber": "4", "pinName": "NC", "x": 0.0, "y": 9.5},
			},
		},
	}}
	scene := buildScene(result)
	if len(scene.Pins) != 4 {
		t.Fatalf("expected 4 pins, got %d", len(scene.Pins))
	}
	byNum := map[string]acPin{}
	for _, p := range scene.Pins {
		byNum[p.PinNumber] = p
	}
	if p := byNum["1"]; !p.NetKnown || p.Net != "GND" {
		t.Errorf("pin 1: want net=GND known, got net=%q known=%v", p.Net, p.NetKnown)
	}
	if p := byNum["2"]; !p.NetKnown || p.Net != "" {
		t.Errorf("pin 2: want floating (empty, known), got net=%q known=%v", p.Net, p.NetKnown)
	}
	if p := byNum["3"]; p.NetKnown {
		t.Errorf("pin 3: net was null → should be unknown, got known=%v", p.NetKnown)
	}
	if p := byNum["4"]; p.NetKnown {
		t.Errorf("pin 4: no net key → should be unknown, got known=%v", p.NetKnown)
	}
}

func TestBuildScene_OffPageComponentIsPinlessWithPage(t *testing.T) {
	// --all-pages surfaces D7 (on page B) with page tags but NO pins (the active
	// page's pin lookup didn't return them). buildScene must record it as a
	// pin-less component carrying its page, so resolvePinCoord can hint correctly.
	result := map[string]any{"components": []any{
		map[string]any{"componentType": "part", "designator": "U1",
			"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 10.0, "maxY": 10.0},
			"pins": []any{map[string]any{"pinNumber": "1", "pinName": "VCC", "x": 0.0, "y": 5.0}},
		},
		map[string]any{"componentType": "part", "designator": "D7",
			"bbox":     map[string]any{"minX": 20.0, "minY": 20.0, "maxX": 24.0, "maxY": 24.0},
			"pageUuid": "0395abcd", "pageName": "Page B",
		},
	}}
	scene := buildScene(result)
	var d7 *acComponent
	for i := range scene.Components {
		if scene.Components[i].Designator == "D7" {
			d7 = &scene.Components[i]
		}
	}
	if d7 == nil {
		t.Fatalf("D7 not recorded in scene.Components: %+v", scene.Components)
	}
	if d7.HasPins {
		t.Error("off-page D7 should be pin-less (HasPins=false)")
	}
	if d7.PageUuid != "0395abcd" || d7.PageName != "Page B" {
		t.Errorf("D7 page info not carried: %+v", d7)
	}
}

// deref is a tiny test helper: turn the *layoutBBox from bb() into a value.
func (p *layoutBBox) deref() layoutBBox { return *p }

// ── same-name pin fan-out ("J1:VBUS*", issue #145) ──────────────────────────

// usbcScene mirrors a USB-C 16P: VBUS and GND on two pins each, the shield tab on
// four, plus uniquely-named data pins.
func usbcScene() acScene {
	return acScene{Pins: []acPin{
		{Designator: "J1", PinNumber: "A4", PinName: "VBUS"},
		{Designator: "J1", PinNumber: "B4", PinName: "VBUS"},
		{Designator: "J1", PinNumber: "A1", PinName: "GND"},
		{Designator: "J1", PinNumber: "B1", PinName: "GND"},
		{Designator: "J1", PinNumber: "A6", PinName: "DP1"},
		{Designator: "U1", PinNumber: "16", PinName: "VCC"},
	}}
}

func TestExpandPinFanouts(t *testing.T) {
	got := expandPinFanouts(usbcScene(), []acConnSpec{
		{PinRef: "J1:VBUS*", Net: "5V", Kind: "power"},
		{PinRef: "U1:VCC", Net: "5V", Kind: "power"},
	})
	if len(got) != 3 {
		t.Fatalf("expected VBUS* to fan out to 2 pins + 1 untouched, got %d: %+v", len(got), got)
	}
	// Fan-out keys each connection by pin NUMBER so nothing downstream re-resolves
	// an ambiguous name, and the net/kind ride along unchanged.
	if got[0].PinRef != "J1:A4" || got[1].PinRef != "J1:B4" {
		t.Fatalf("fanned refs = %q,%q; want J1:A4,J1:B4", got[0].PinRef, got[1].PinRef)
	}
	if got[0].Net != "5V" || got[0].Kind != "power" {
		t.Fatalf("net/kind not carried: %+v", got[0])
	}
	if got[2].PinRef != "U1:VCC" {
		t.Fatalf("non-wildcard spec was rewritten to %q", got[2].PinRef)
	}
}

// A star that matches nothing must degrade to the plain name so resolvePinCoord
// produces its canonical "not found" message naming a real pin.
func TestExpandPinFanoutsNoMatch(t *testing.T) {
	got := expandPinFanouts(usbcScene(), []acConnSpec{{PinRef: "J1:SHIELD*"}})
	if len(got) != 1 || got[0].PinRef != "J1:SHIELD" {
		t.Fatalf("got %+v, want a single J1:SHIELD", got)
	}
	if _, err := resolvePinCoord(usbcScene(), got[0].PinRef); err == nil {
		t.Fatal("expected the degraded ref to still fail resolution")
	}
}

// A single-pin function is the common case: the star must be an identity there, so
// blocks can mark "bond them all" without knowing the part's pin count.
func TestExpandPinFanoutsSinglePinIsIdentity(t *testing.T) {
	got := expandPinFanouts(usbcScene(), []acConnSpec{{PinRef: "U1:VCC*"}})
	if len(got) != 1 || got[0].PinRef != "U1:16" {
		t.Fatalf("got %+v, want a single U1:16", got)
	}
}

// The ambiguity error must teach the fix, not just refuse.
func TestResolvePinCoordAmbiguousSuggestsFanout(t *testing.T) {
	_, err := resolvePinCoord(usbcScene(), "J1:VBUS")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	for _, want := range []string{"A4 B4", `"J1:VBUS*"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %s", err, want)
		}
	}
}

// ── endpoint grid snap (merged-wire short prevention) ───────────────────────

// The planner must score the coordinate the board will actually hold. An
// un-snapped endpoint let a stub planned at (545,272) read as "clear" of a
// foreign-net wire at y=270, then land at (545,270) — ON it — merging two nets.
func TestEndpointForSnapsToGrid(t *testing.T) {
	cases := []struct {
		dir            string
		px, py, offset float64
		wantX, wantY   float64
	}{
		// 290+18 = 308 → snaps to 310; x stays exactly on the pin.
		{"up", 545, 290, 18, 545, 310},
		{"down", 545, 290, 18, 545, 270},
		{"left", 560, 270, 18, 540, 270},
		{"right", 560, 270, 18, 580, 270},
		// Already on-grid endpoints are untouched.
		{"up", 500, 300, 20, 500, 320},
		// A pin on the ODD 5-grid keeps its perpendicular coordinate: snapping it
		// would pull the stub off the pin axis into a diagonal that fails to create.
		{"left", 600, 385, 18, 580, 385},
	}
	for _, c := range cases {
		x, y := endpointFor(c.px, c.py, c.offset, c.dir)
		if x != c.wantX || y != c.wantY {
			t.Fatalf("%s from (%v,%v)+%v = (%v,%v), want (%v,%v)",
				c.dir, c.px, c.py, c.offset, x, y, c.wantX, c.wantY)
		}
		// Whatever the snap does, the stub must stay orthogonal.
		if x != c.px && y != c.py {
			t.Fatalf("%s produced a diagonal stub: (%v,%v) → (%v,%v)", c.dir, c.px, c.py, x, y)
		}
	}
}

// With the snap in place, a candidate whose SNAPPED endpoint lands on a
// foreign-net wire must be hard-rejected — the check that silently passed before.
func TestForeignWireRejectUsesSnappedEndpoint(t *testing.T) {
	// A foreign wire at y=310, x 540→560, net D1_N3.
	wires := []wireSegment{{X0: 540, Y0: 310, X1: 560, Y1: 310, Net: "D1_N3"}}
	// D2:4 sits at (545,290); y-UP "up" with offset 18 snaps to (545,310).
	ex, ey := endpointFor(545, 290, 18, "up")
	if !stubTouchesForeignWire(545, 290, ex, ey, "USB_HOST_DP", wires) {
		t.Fatalf("snapped endpoint (%v,%v) lies on a foreign-net wire but was not rejected", ex, ey)
	}
}

// A stub that STOPS exactly on a neighbouring pin is shorted just as surely as one
// that passes through it. Grid snapping makes this the common case, not the corner
// case: XL1509's pins sit 20 apart, so an 18-offset stub snaps right onto the next
// pin (this chained three nets into one wire tree on a real board).
func TestScoreCandidate_StubEndingOnNeighbourPinHardRejects(t *testing.T) {
	// U3 pins 2/3/4 stacked vertically at x=645, 20 apart.
	scene := acScene{Pins: []acPin{
		{X: 645, Y: 370, Designator: "U3", PinNumber: "2"},
		{X: 645, Y: 390, Designator: "U3", PinNumber: "3"},
		{X: 645, Y: 410, Designator: "U3", PinNumber: "4"},
	}}
	rules := defaultAutoconnectRules()
	pin := acPin{X: 645, Y: 370, Designator: "U3", PinNumber: "2"}

	// y-UP "up" with offset 18 snaps to (645,390) — exactly pin 3.
	up := scoreCandidate(pin, "up", 18, "netport", "C11_N3", scene, rules)
	if up.Score < costPinCross {
		t.Fatalf("stub ending on pin 3 must be hard-rejected, got score %v (%v)", up.Score, up.Reasons)
	}
	// A direction with no pin in the way stays viable.
	left := scoreCandidate(pin, "left", 18, "netport", "C11_N3", scene, rules)
	if left.Score >= costPinCross {
		t.Fatalf("clear direction must not be rejected, got %v (%v)", left.Score, left.Reasons)
	}
}

// TestScoreCandidate_FoldedNetPortPenalty: standing a netport vertical costs
// costFoldedPort so horizontal placement wins on dense pin columns unless the
// horizontal candidates are genuinely colliding (≥ costFlagCollision); square
// ground markers stay exempt.
func TestScoreCandidate_FoldedNetPortPenalty(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	scene := acScene{}
	rules := autoconnectRules{}
	has := func(c acCandidate) bool {
		for _, r := range c.Reasons {
			if r.Cost == costFoldedPort {
				return true
			}
		}
		return false
	}
	if c := scoreCandidate(pin, "up", 18, "net_port_bi", "N", scene, rules); !has(c) {
		t.Fatalf("vertical netport must carry costFoldedPort: %+v", c.Reasons)
	}
	if c := scoreCandidate(pin, "down", 18, "net_port_bi", "N", scene, rules); !has(c) {
		t.Fatalf("vertical netport must carry costFoldedPort: %+v", c.Reasons)
	}
	if c := scoreCandidate(pin, "left", 18, "net_port_bi", "N", scene, rules); has(c) {
		t.Fatalf("horizontal netport must NOT carry costFoldedPort: %+v", c.Reasons)
	}
	if c := scoreCandidate(pin, "up", 18, "ground", "GND", scene, rules); has(c) {
		t.Fatalf("ground marker must stay exempt: %+v", c.Reasons)
	}
}

// 电路说明(自由文本)是页面上的同级占位对象(ADR-0003),marker 不许压上去。
// 这条过去是个静默漏洞:buildScene 只吃 components.list,而平台的 components.list
// **不返回文本**,于是文字对落点评分完全隐形。
func TestScoreCandidate_TextNoteIsAnObstacle(t *testing.T) {
	pin := acPin{X: 0, Y: 0, Designator: "U1", PinNumber: "1"}
	rules := defaultAutoconnectRules()
	// 说明必须挡在**默认最优方向**上,用例才证明得了东西:gnd 默认朝下,
	// 所以把说明铺在 pin 下方,盖住 down 档位的 ground body。
	note := layoutBBox{MinX: -100, MinY: -120, MaxX: 100, MaxY: -10}

	clean := planConnection(pin, "gnd", "GND", acScene{}, rules, 0)
	withNote := planConnection(pin, "gnd", "GND", acScene{Texts: []layoutBBox{note}}, rules, 0)
	if len(clean) == 0 || len(withNote) == 0 {
		t.Fatal("没有候选")
	}
	// 有说明时,最优候选不该落在说明上
	best := withNote[0]
	ex, ey := endpointFor(pin.X, pin.Y, best.Offset, best.Direction)
	lbl := predictedMarkerBBox(ex, ey, "gnd", best.Direction, "GND")
	if boxesOverlap(lbl, note) {
		t.Errorf("最优候选压在电路说明上: dir=%s off=%v lbl=%+v note=%+v",
			best.Direction, best.Offset, lbl, note)
	}
	// 且这个约束确实改变了选择(否则用例证明不了什么)
	if clean[0].Direction == best.Direction && clean[0].Offset == best.Offset {
		t.Errorf("说明没有影响落点选择,用例失效: %s@%v", best.Direction, best.Offset)
	}
}

// ── 同侧 lane 联合分配 ──────────────────────────────────────────────────────

// 步长必须 ≥ marker 自身 body 长度,否则后一个压在前一个身上。
func TestLaneStepFor_ExceedsMarkerBody(t *testing.T) {
	for _, c := range []struct{ kind, net string }{
		{"netport", "C7_N5"}, {"ground", "GND"}, {"power", "5V"},
	} {
		p := markerBBoxProfile(c.kind, c.net)
		if got, body := laneStepFor(c.kind, c.net), p.Far-p.Near; got <= body {
			t.Errorf("%s/%s 步长 %v 必须大于 body 长度 %v", c.kind, c.net, got, body)
		}
	}
	// 长网名的 netport body 更长,步长必须跟着变大
	if laneStepFor("netport", "A_VERY_LONG_NET") <= laneStepFor("netport", "N1") {
		t.Error("netport 步长必须随网名长度增长")
	}
}

// 同一侧第二个 marker 必须错开一个 body 长度 —— 这是相邻脚不互压的唯一办法
// (改 offset 只动 x,y 范围由引脚决定,不会变)。
func TestApplyLaneStagger_StaysShallowWhenLabelsDoNotCollide(t *testing.T) {
	// 同侧已经落过一支 marker,但新的这支在最浅档**并不撞** —— 就该待在最浅档。
	// 旧口径是阶梯(每多一支再深一个 step),6 个脚就要 276 深的通道,而器件本体
	// 才 71 宽:簇被标签撑成本体的 6 倍,邻居整个坐进来(`sch clusters` 实测
	// J1 体积 486×292,D1 115×109 全在里面)。深度只该由**真实碰撞**决定。
	all := []acCandidate{
		{Direction: "left", Offset: 18, Score: 1},
		{Direction: "left", Offset: 90, Score: 9},
	}
	lanes := map[string]float64{laneKeyOf("U3", "left"): 18}
	got := applyLaneStagger(all, lanes, "U3", "C7_N5", "netport")
	if got.Offset != 18 {
		t.Errorf("不撞就不该让开,阶梯是白给的深度: got offset=%v", got.Offset)
	}
}

// 撞了才让开:首选带着 costFlagCollision 时,挑同方向里第一个不撞的。
func TestApplyLaneStagger_StepsOutOnlyWhenItActuallyCollides(t *testing.T) {
	// 让开的那一档必须真的越过前一支的**整个占地**(body + 网名 + 间隙),
	// 否则深的那支 body 会落进浅的那支名字带里 —— 这就是「长短循环」的长档。
	step := laneStepFor("netport", "C7_N5")
	all := []acCandidate{
		{Direction: "left", Offset: 18, Score: 1001,
			Reasons: []acReason{{costFlagCollision, "label collides with an existing flag/port/label"}}},
		{Direction: "left", Offset: 18 + step - 6, Score: 6}, // 差一点,不够
		{Direction: "left", Offset: 18 + step, Score: 9},     // 正好一个完整步长
	}
	lanes := map[string]float64{laneKeyOf("U3", "left"): 18}
	got := applyLaneStagger(all, lanes, "U3", "C7_N5", "netport")
	if got.Offset != 18+step {
		t.Errorf("让开必须够一个完整步长(%v),got %v", 18+step, got.Offset)
	}
}

// 这一侧还没人时不该无故推远。
func TestApplyLaneStagger_FirstOnSideKeepsBest(t *testing.T) {
	all := []acCandidate{{Direction: "left", Offset: 18, Score: 1}, {Direction: "left", Offset: 60, Score: 5}}
	got := applyLaneStagger(all, map[string]float64{}, "U3", "N1", "netport")
	if got.Offset != 18 {
		t.Errorf("首个 marker 不该被推远: %v", got.Offset)
	}
}

// **错开不能以短路为代价**:够远的候选若被 #64 硬拒绝,必须跳过。
func TestApplyLaneStagger_NeverPicksAHardReject(t *testing.T) {
	all := []acCandidate{
		{Direction: "left", Offset: 18, Score: 1001,
			Reasons: []acReason{{costFlagCollision, "label collides with an existing flag/port/label"}}},
		{Direction: "left", Offset: 90, Score: 2e9,
			Reasons: []acReason{{costHardReject, "stub touches an existing (foreign-net) wire (hard reject)"}}},
		{Direction: "up", Offset: 18, Score: 3},
	}
	lanes := map[string]float64{laneKeyOf("U3", "left"): 18}
	got := applyLaneStagger(all, lanes, "U3", "N1", "netport")
	if candidateHardRejected(got) {
		t.Fatalf("绝不能为了错开选一个会短路的候选: %+v", got)
	}
	// 同方向没出路 → 换一个没被占用的方向
	if got.Direction != "up" {
		t.Errorf("同侧无出路时应换方向: got %s@%v", got.Direction, got.Offset)
	}
}

// **错开不能换一种破坏**:够远但压在器件上的候选必须跳过 —— 第一版只挡短路,
// 真机当场多出两条「D1(part) 与 MCU_TX(netport) 重叠 26.00×11.00」。
func TestApplyLaneStagger_NeverPicksACandidateOverAPart(t *testing.T) {
	all := []acCandidate{
		{Direction: "left", Offset: 18, Score: 1001,
			Reasons: []acReason{{costFlagCollision, "label collides with an existing flag/port/label"}}},
		{Direction: "left", Offset: 90, Score: 10001,
			Reasons: []acReason{{costPartOverlap, "label overlaps a part bbox"}}},
		{Direction: "down", Offset: 18, Score: 3},
	}
	lanes := map[string]float64{laneKeyOf("U3", "left"): 18}
	got := applyLaneStagger(all, lanes, "U3", "N1", "netport")
	if candidateHitsPartOrText(got) {
		t.Fatalf("为了错开而压器件,等于换一种破坏: %+v", got)
	}
	if got.Direction != "down" {
		t.Errorf("同侧无干净出路时应换方向: got %s@%v", got.Direction, got.Offset)
	}
}

// **背面引出是红线**:左侧引脚的 marker 从右边引出,桩线要穿过/绕过器件本体,
// 读图的人追不到线。代价必须比任何软破坏(压器件 10000)都贵 —— 实测 C7_N3 接
// U3 左侧的 V3 脚,marker 却落到了右边,就是因为朝向只值 -20 而撞标签值 +1000。
func TestScoreCandidate_OppositeSideIsCostlierThanAnySoftDamage(t *testing.T) {
	owner := layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	pin := acPin{X: -2, Y: 50, Designator: "U3", PinNumber: "4", OwnerBBox: &owner} // 左侧引脚
	if got := outwardDirection(pin); got != "left" {
		t.Fatalf("fixture 不成立:左侧引脚的朝外方向应是 left, got %q", got)
	}
	rules := defaultAutoconnectRules()
	// 左侧(朝外)全被已有 marker 占死,右侧(背面)干净 —— 旧权重下会选右侧
	var flags []layoutBBox
	for x := -300.0; x <= -10; x += 5 {
		flags = append(flags, layoutBBox{MinX: x - 3, MinY: 30, MaxX: x + 3, MaxY: 70})
	}
	got := planConnection(pin, "netport", "C7_N3", acScene{Flags: flags, Parts: []layoutBBox{owner}}, rules, 0)
	if len(got) == 0 {
		t.Fatal("没有候选")
	}
	if got[0].Direction == "right" {
		t.Errorf("宁可挤也不能从背面引出: 选了 %s@%v", got[0].Direction, got[0].Offset)
	}
	// 代价排序:背面 > 压器件
	if costOppositeSide <= costPartOverlap {
		t.Errorf("背面引出(%v)必须比压器件(%v)更贵", costOppositeSide, costPartOverlap)
	}
}

func TestOppositeDirection(t *testing.T) {
	for in, want := range map[string]string{"left": "right", "right": "left", "up": "down", "down": "up", "": ""} {
		if got := oppositeDirection(in); got != want {
			t.Errorf("oppositeDirection(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOutwardDirectionPrefersPinRotationForAsymmetricConnector(t *testing.T) {
	owner := layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	rot := 180.0 // rendered pin points left; bbox center alone is ambiguous
	pin := acPin{X: 50, Y: 50, OwnerBBox: &owner, PinRotation: &rot}
	if got := outwardDirection(pin); got != "left" {
		t.Fatalf("pin rotation must define outward side for asymmetric symbols: got %q", got)
	}
}

func TestOutwardDirectionFallsBackWithoutPinRotation(t *testing.T) {
	owner := layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	pin := acPin{X: -2, Y: 50, OwnerBBox: &owner}
	if got := outwardDirection(pin); got != "left" {
		t.Fatalf("missing pin rotation should retain bbox fallback: got %q", got)
	}
}

// **lane 错开不能凌驾于正确性之上**。真机:U3:V3 左侧撞标签(1583),lane 因为
// 「左侧已被占」去找没被占用的方向,把 marker 甩到了背面(50597)—— 分数高了 32 倍,
// 而排序本身是对的,是 lane 在排序之外强行改选。
func TestApplyLaneStagger_NeverFlipsToTheOppositeSide(t *testing.T) {
	all := []acCandidate{
		{Direction: "left", Offset: 30, Score: 1583,
			Reasons: []acReason{{costFlagCollision, "label collides with an existing flag/port/label"}}},
		{Direction: "right", Offset: 78, Score: 50597,
			Reasons: []acReason{{costOppositeSide, "引出方向与引脚朝外方向相反 —— 桩线要穿过/绕过器件本体"}}},
	}
	lanes := map[string]float64{laneKeyOf("U3", "left"): 18}
	got := applyLaneStagger(all, lanes, "U3", "C7_N3", "netport")
	if candidateGoesOppositeSide(got) {
		t.Fatalf("宁可挤在正面,也不能为了错开翻到背面: %+v", got)
	}
	if got.Direction != "left" {
		t.Errorf("应退回正面的最优: got %s@%v", got.Direction, got.Offset)
	}
}

// **上界必须跟着 lane 需求走,不能是拍出来的固定倍数**。真机:布局把 D1 推开 146
// 后,U3 左侧腾出 276 的通道,而第 6 个 marker 需要 offset 248 —— 旧上界
// 3×OffsetMax=240 < 248,空间给了却够不着,推开器件那一步白做。
func TestExtendedOffsets_UpperBoundFollowsLaneDemand(t *testing.T) {
	rules := defaultAutoconnectRules()
	base := extendedOffsets(rules, 0)
	if len(base) == 0 {
		t.Fatal("无 lane 压力时也该有扩展档位")
	}
	if got := base[len(base)-1]; got > 3*rules.OffsetMax+1 {
		t.Errorf("无 lane 压力时上界仍是 3×OffsetMax: got %v", got)
	}
	// lane 要求排到 248
	far := extendedOffsets(rules, 248)
	if len(far) == 0 {
		t.Fatal("有 lane 压力时必须有候选")
	}
	last := far[len(far)-1]
	if last < 248 {
		t.Errorf("上界没跟上 lane 需求: 最远只到 %v,需要 ≥248", last)
	}
	// 且必须真的枚举出 ≥248 的档位
	found := false
	for _, o := range far {
		if o >= 248 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("没有枚举出够远的档位: %v", far)
	}
}

// 同侧 lane 占到常规范围之外时,即使当前有干净候选也要铺远档 ——
// 否则 applyLaneStagger 手里根本没有够远的可选。
func TestPlanConnection_ExtendsWhenLaneFloorExceedsRegularRange(t *testing.T) {
	pin := acPin{X: 0, Y: 0, Designator: "U1", PinNumber: "1"}
	rules := defaultAutoconnectRules()
	got := planConnection(pin, "netport", "C7_N5", acScene{}, rules, 248)
	maxOff := 0.0
	for _, c := range got {
		if c.Offset > maxOff {
			maxOff = c.Offset
		}
	}
	if maxOff < 248 {
		t.Errorf("lane 要求 248 时,候选最远只到 %v", maxOff)
	}
}

// ── 桩长硬上限(OffsetCap)───────────────────────────────────────────────────
//
// 2026-08-20 收敛性缺陷:OffsetMax 只封住细档,而 candidateOffsets **常驻**
// laneStepFor 的标准档位(netport 一档 ~89、三档 ~285)、extendedOffsets 更是
// 跟着 laneFloor 无上界。对「刚体平移复现」「按收敛计划落地」这两类桩长被规划
// 定死的场景,评分器多走一档就等于把区框撑胖一档(真机 +208)。
func TestCandidateOffsets_HonorsOffsetCap(t *testing.T) {
	rules := defaultAutoconnectRules()
	lane := laneStepFor("net_port_bi", "USB_DTR")
	// 无上限:标准档位必须在(这是既有行为,别把它当上限的副作用删掉)。
	free := candidateOffsets(rules, "net_port_bi", "USB_DTR")
	if free[len(free)-1] < rules.OffsetMin+lane {
		t.Fatalf("无上限时该铺到 laneStepFor 档位(≥%.0f),got %v", rules.OffsetMin+lane, free[len(free)-1])
	}
	// 有上限:一档都不许越过。
	rules.OffsetCap = 48
	capped := candidateOffsets(rules, "net_port_bi", "USB_DTR")
	if len(capped) == 0 {
		t.Fatal("有上限也必须留下可选档位 —— 连不上比连得深还糟")
	}
	for _, o := range capped {
		if o > rules.OffsetCap {
			t.Fatalf("档位 %v 越过硬上限 %v:%v", o, rules.OffsetCap, capped)
		}
	}
	// 扩展档同样要夹(最容易漏的那一处:noCleanCandidate 时才铺,平时看不见)。
	for _, o := range extendedOffsets(rules, 400) {
		if o > rules.OffsetCap {
			t.Fatalf("扩展档 %v 越过硬上限 %v", o, rules.OffsetCap)
		}
	}
	// 上限比 OffsetMin 还紧:仍要给一档(夹到上限),不能返回空。
	rules.OffsetCap = 10
	if tight := candidateOffsets(rules, "ground", "GND"); len(tight) != 1 || tight[0] != 10 {
		t.Fatalf("上限紧于 OffsetMin 时该给夹到上限的单档 [10],got %v", tight)
	}
}
