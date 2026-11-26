package app

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// Real geometry pulled from the fixed ESP32-S3 regression board (ceshi):
// `easyeda pcb list --include-pads --include-bbox`. U1 is the lone main chip;
// the 7 satellites must hug the U1 edge nearest the pad they connect to.

// mkComp builds a component whose bbox is centered on (cx,cy) with size w×h, so
// the anchor↔bbox-center offset is zero (the planner preserves it either way).
func mkComp(id, des string, cx, cy, w, h float64, pads []apPad) apComp {
	return apComp{
		id: id, designator: des, x: cx, y: cy, hasBBox: true,
		minX: cx - w/2, maxX: cx + w/2, minY: cy - h/2, maxY: cy + h/2,
		pads: pads,
	}
}

// p builds a TOP-layer pad (layer 1) — all fixture parts are single-sided.
func p(num, net string, x, y float64) apPad {
	return apPad{num: num, net: net, x: x, y: y, layer: 1}
}

func ceshiBoard() []apComp {
	// U1 key pads at their real coordinates (left edge: GND/3V3/EN; right edge:
	// IO0/BLINK/GND), plus a couple of center thermal GND pads to push the pin
	// count over the main-chip threshold.
	u1pads := []apPad{
		p("1", "GND", 2050, -1640), p("2", "3V3", 2050, -1690), p("3", "EN", 2050, -1740),
		p("27", "IO0", 2740, -2290), p("38", "BLINK", 2740, -1740), p("40", "GND", 2740, -1640),
		p("41", "GND", 2336, -1944), p("42", "", 2400, -2360), p("43", "", 2400, -1350),
		p("44", "", 2100, -2360),
	}
	return []apComp{
		mkComp("u1", "U1", 2395, -1855, 748, 1030, u1pads),
		mkComp("c3", "C3", 259, -1128, 74.8, 45.3, []apPad{p("2", "EN", 0, 0), p("1", "GND", 0, 0)}),
		mkComp("r1", "R1", 1855, -1128, 80.4, 45.3, []apPad{p("2", "EN", 0, 0), p("1", "3V3", 0, 0)}),
		mkComp("r2", "R2", 259, -2209, 80.3, 45.3, []apPad{p("2", "3V3", 0, 0), p("1", "IO0", 0, 0)}),
		mkComp("r3", "R3", 1057, -2209, 80.3, 45.3, []apPad{p("2", "LED_A", 0, 0), p("1", "BLINK", 0, 0)}),
		mkComp("c2", "C2", 2509, -652, 160.6, 77.2, []apPad{p("2", "3V3", 0, 0), p("1", "GND", 0, 0)}),
		mkComp("led1", "LED1", 1062, -1128, 176, 91, []apPad{p("2", "GND", 0, 0), p("1", "LED_A", 0, 0)}),
		mkComp("c1", "C1", 2827, -636, 74.8, 45.3, []apPad{p("2", "GND", 0, 0), p("1", "3V3", 0, 0)}),
	}
}

func TestPlanAutoPlace_Ceshi(t *testing.T) {
	comps := ceshiBoard()
	moves, diags := planAutoPlace(comps, defaultApOptions())

	if len(diags) != 0 {
		t.Fatalf("expected every satellite placed, got diags: %+v", diags)
	}
	if len(moves) != 7 {
		t.Fatalf("expected 7 satellites moved, got %d", len(moves))
	}

	byDes := map[string]apMove{}
	for _, m := range moves {
		byDes[m.Designator] = m
		if m.Main != "U1" {
			t.Errorf("%s anchored to %q, want U1", m.Designator, m.Main)
		}
	}

	// Expected edge + connecting net for each satellite.
	want := map[string]struct {
		edge string
		net  string
	}{
		"C3": {"left", "EN"}, "R1": {"left", "EN"},
		"C1": {"left", "3V3"}, "C2": {"left", "3V3"},
		"R2": {"right", "IO0"}, "R3": {"right", "BLINK"},
		"LED1": {"right", "LED_A"},
	}
	for des, w := range want {
		m, ok := byDes[des]
		if !ok {
			t.Errorf("%s not placed", des)
			continue
		}
		if m.Edge != w.edge {
			t.Errorf("%s edge=%s, want %s", des, m.Edge, w.edge)
		}
		if m.TargetNet != w.net {
			t.Errorf("%s targetNet=%s, want %s", des, m.TargetNet, w.net)
		}
	}
	// LED1 must chain off R3 (it shares LED_A with no chip pad).
	if v := byDes["LED1"].Via; v != "chain:R3" {
		t.Errorf("LED1 via=%q, want chain:R3", v)
	}

	// Every satellite must end OUTSIDE the U1 bbox, on the correct side.
	u1 := comps[0]
	for des, m := range byDes {
		switch want[des].edge {
		case "left":
			if m.NewX >= u1.minX {
				t.Errorf("%s newX=%.1f not left of U1 (minX=%.1f)", des, m.NewX, u1.minX)
			}
		case "right":
			if m.NewX <= u1.maxX {
				t.Errorf("%s newX=%.1f not right of U1 (maxX=%.1f)", des, m.NewX, u1.maxX)
			}
		}
	}

	// No two satellites may overlap after placement.
	sizes := map[string][2]float64{}
	for _, c := range comps {
		sizes[c.designator] = [2]float64{c.width(), c.height()}
	}
	ms := moves
	for i := 0; i < len(ms); i++ {
		for j := i + 1; j < len(ms); j++ {
			a, b := ms[i], ms[j]
			sa, sb := sizes[a.Designator], sizes[b.Designator]
			dx := math.Abs(a.NewX - b.NewX)
			dy := math.Abs(a.NewY - b.NewY)
			if dx < (sa[0]+sb[0])/2 && dy < (sa[1]+sb[1])/2 {
				t.Errorf("overlap: %s(%.0f,%.0f) vs %s(%.0f,%.0f)", a.Designator, a.NewX, a.NewY, b.Designator, b.NewX, b.NewY)
			}
		}
	}
}

// A board with no chip (only 2-pad parts) places nothing and says why.
func TestPlanAutoPlace_NoMainChip(t *testing.T) {
	comps := []apComp{
		mkComp("r1", "R1", 0, 0, 80, 45, []apPad{p("1", "A", 0, 0), p("2", "B", 0, 0)}),
		mkComp("c1", "C1", 100, 0, 75, 45, []apPad{p("1", "B", 0, 0), p("2", "GND", 0, 0)}),
	}
	moves, diags := planAutoPlace(comps, defaultApOptions())
	if len(moves) != 0 {
		t.Errorf("expected no moves without a main chip, got %d", len(moves))
	}
	if len(diags) != 2 {
		t.Errorf("expected a diag per satellite, got %d", len(diags))
	}
}

// orientSatellite picks the rotation that points a 2-pin part's connecting pad at
// the chip: the 3V3 pad sits at +x natively, so a left-edge part keeps rot 0
// (pad already faces the chip on its right), a right-edge part flips to 180, and a
// top/bottom part turns 90° so the pad axis goes vertical.
func TestOrientSatellite(t *testing.T) {
	// cap centered at origin, rot 0: pad "2"(3V3) at +x, pad "1"(GND) at -x.
	cap := mkComp("c", "C1", 0, 0, 40, 20, []apPad{p("2", "3V3", 15, 0), p("1", "GND", -15, 0)})

	cases := []struct {
		edge               apEdge
		wantRot            float64
		wantEffW, wantEffH float64
	}{
		{edgeLeft, 0, 40, 20},    // faces +x → pad already there
		{edgeRight, 180, 40, 20}, // faces -x → flip
		{edgeTop, 270, 20, 40},   // faces -y → pad axis vertical, swapped bbox
		{edgeBottom, 90, 20, 40}, // faces +y
	}
	for _, c := range cases {
		rot, effW, effH, oriented := orientSatellite(cap, c.edge, "3V3", true)
		if !oriented {
			t.Errorf("%s: expected oriented=true", c.edge)
		}
		if rot != c.wantRot {
			t.Errorf("%s: rot=%.0f want %.0f", c.edge, rot, c.wantRot)
		}
		if effW != c.wantEffW || effH != c.wantEffH {
			t.Errorf("%s: eff=%.0fx%.0f want %.0fx%.0f", c.edge, effW, effH, c.wantEffW, c.wantEffH)
		}
		// The connecting pad must end up pointing at the chip.
		ndx, ndy := rotateVec(15, 0, rot) // 3V3 pad native offset, rotated
		fx, fy := edgeFacing(c.edge)
		if ndx*fx+ndy*fy <= 0 {
			t.Errorf("%s: connecting pad does not face the chip (%.0f,%.0f vs facing %.0f,%.0f)", c.edge, ndx, ndy, fx, fy)
		}
	}

	// --no-rotate / non-2-pin parts are left as-is.
	if rot, _, _, oriented := orientSatellite(cap, edgeLeft, "3V3", false); oriented || rot != cap.rotation {
		t.Errorf("rotate=false should not re-orient (oriented=%v rot=%.0f)", oriented, rot)
	}
}

// A 2-pin satellite on the board gets a rotation patch (SetRot) and still no overlap.
func TestPlanAutoPlace_RotatesSatellite(t *testing.T) {
	moves, _ := planAutoPlace(ceshiBoard(), defaultApOptions())
	anyRot := false
	for _, m := range moves {
		if m.SetRot {
			anyRot = true
		}
	}
	if !anyRot {
		t.Error("expected at least one re-oriented (SetRot) satellite on the ceshi board")
	}
}

// Two 8-pin chips that overlap should be spread into a row with >= multiGap
// between bboxes; the leftmost stays put, the other gets a "chip-spacing" move.
func twoChipBoard() []apComp {
	mk8 := func(id, des string, cx float64) apComp {
		pads := []apPad{
			p("1", "", cx, 0), p("2", "", cx, 0), p("3", "", cx, 0), p("4", "", cx, 0),
			p("5", "", cx, 0), p("6", "", cx, 0), p("7", "", cx, 0), p("8", "", cx, 0),
		}
		return mkComp(id, des, cx, 0, 400, 400, pads)
	}
	return []apComp{
		mk8("u1", "U1", 0),   // bbox x ∈ [-200, 200]
		mk8("u2", "U2", 100), // bbox x ∈ [-100, 300] — overlaps U1
	}
}

func TestPlanAutoPlace_MultiChipSpacing(t *testing.T) {
	opt := defaultApOptions() // multiGap = 150
	moves, _ := planAutoPlace(twoChipBoard(), opt)

	var u2 *apMove
	for i := range moves {
		if moves[i].Designator == "U2" {
			u2 = &moves[i]
		}
		if moves[i].Designator == "U1" {
			t.Errorf("leftmost chip U1 should not move, got %+v", moves[i])
		}
	}
	if u2 == nil {
		t.Fatal("U2 (overlapping chip) should get a chip-spacing move")
	}
	if u2.Via != "chip-spacing" {
		t.Errorf("U2 move via=%q, want chip-spacing", u2.Via)
	}
	// U1 right edge = 200; U2 half-width = 200, so U2 center must be ≥ 200+150+200.
	if got, want := u2.NewX, 200.0+opt.multiGap+200.0; got < want-0.01 {
		t.Errorf("U2 newX=%.0f, want ≥ %.0f (U1.right + multiGap + half)", got, want)
	}

	// multiGap=0 disables spacing → no chip moves.
	opt.multiGap = 0
	moves0, _ := planAutoPlace(twoChipBoard(), opt)
	for _, m := range moves0 {
		if m.Via == "chip-spacing" {
			t.Errorf("multiGap=0 should not space chips, got %+v", m)
		}
	}
}

// A pre-anchored 2D floorplan (#91): two columns of 8-pin chips, each column
// stacked vertically. spaceMains would judge the columns' X-projections as
// "overlapping / too close" and flatten them into a row; the 2D guard must
// preserve the anchors so no chip-spacing move is emitted.
func floorplan2DBoard() []apComp {
	mk8 := func(id, des string, cx, cy float64) apComp {
		pads := []apPad{
			p("1", "", cx, cy), p("2", "", cx, cy), p("3", "", cx, cy), p("4", "", cx, cy),
			p("5", "", cx, cy), p("6", "", cx, cy), p("7", "", cx, cy), p("8", "", cx, cy),
		}
		return mkComp(id, des, cx, cy, 400, 400, pads)
	}
	// Column A at x≈0, column B at x≈100 (X-projections overlap → 1D would spread),
	// but each column has two vertically stacked chips (Y-projections disjoint).
	return []apComp{
		mk8("u1", "U1", 0, 0),     // col A, lower
		mk8("u2", "U2", 0, 600),   // col A, upper (stacked over U1)
		mk8("u3", "U3", 100, 0),   // col B, lower
		mk8("u4", "U4", 100, 600), // col B, upper
	}
}

func TestPlanAutoPlace_Preserve2DFloorplan(t *testing.T) {
	opt := defaultApOptions() // multiGap = 150
	moves, _ := planAutoPlace(floorplan2DBoard(), opt)
	for _, m := range moves {
		if m.Via == "chip-spacing" {
			t.Errorf("2D floorplan must be preserved, got chip-spacing move: %+v", m)
		}
	}
}

// A low-pin, large-bbox connector on the board edge (#91): J_VEH shares GND with
// the chip but must NOT be dragged toward it — it's skipped with a diag so
// place-constrained can seat it on the edge.
func TestPlanAutoPlace_SkipsEdgeConnector(t *testing.T) {
	comps := ceshiBoard()
	// 3-pin power terminal on the left edge, GND tied to the global net.
	jveh := mkComp("jveh", "J1", -500, -1855, 300, 250, []apPad{
		p("1", "VEH_12V", 0, 0), p("2", "GND", 0, 0), p("3", "GND", 0, 0),
	})
	comps = append(comps, jveh)

	moves, diags := planAutoPlace(comps, defaultApOptions())
	for _, m := range moves {
		if m.Designator == "J1" {
			t.Errorf("edge connector J1 should not be moved, got %+v", m)
		}
	}
	found := false
	for _, d := range diags {
		if d.Designator == "J1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a skip diag for edge connector J1, got diags: %+v", diags)
	}
}

// isEdgeConnector guards on all three signals: designator prefix, low pin count,
// and a relatively large footprint. A small J* (a 2-pin test point) or a chip-like
// connector must not be treated as a board-edge connector.
func TestIsEdgeConnector(t *testing.T) {
	big := mkComp("j", "J1", 0, 0, 300, 250, []apPad{p("1", "A", 0, 0), p("2", "GND", 0, 0)})
	if !isEdgeConnector(big, 8) {
		t.Error("large low-pin J1 should be an edge connector")
	}
	small := mkComp("j", "J2", 0, 0, 80, 45, []apPad{p("1", "A", 0, 0), p("2", "GND", 0, 0)})
	if isEdgeConnector(small, 8) {
		t.Error("small J2 (below size floor) should NOT be an edge connector")
	}
	notConn := mkComp("r", "R9", 0, 0, 300, 250, []apPad{p("1", "A", 0, 0), p("2", "GND", 0, 0)})
	if isEdgeConnector(notConn, 8) {
		t.Error("R9 (non-connector designator) should NOT be an edge connector")
	}
	bigChip := mkComp("j", "J3", 0, 0, 300, 250, []apPad{
		p("1", "", 0, 0), p("2", "", 0, 0), p("3", "", 0, 0), p("4", "", 0, 0),
		p("5", "", 0, 0), p("6", "", 0, 0), p("7", "", 0, 0), p("8", "", 0, 0),
	})
	if isEdgeConnector(bigChip, 8) {
		t.Error("8-pin J3 (chip-like) should NOT be classed as an edge connector")
	}
}

// #131: a HIGH-pin connector (USB-C 16P out-pins a small IC) must not compete
// for main — it neither moves nor owns satellites; the decoupling cap goes to
// the IC it serves. Explicit --anchor / --exclude-main beat every heuristic.
func TestPlanAutoPlace_HighPinConnectorIsNotMain(t *testing.T) {
	// U9: an 8-pin regulator (a legit main). J3: a 16-pad USB-C right next to
	// the cap — pin count would win main under the old rule and steal C9.
	pads := []apPad{}
	for i := 1; i <= 16; i++ {
		pads = append(pads, p(fmt.Sprintf("%d", i), fmt.Sprintf("S%d", i), 0, 0))
	}
	j3 := mkComp("j3", "J3", 900, 0, 350, 300, pads)
	u9 := mkComp("u9", "U9", 0, 0, 200, 200, []apPad{
		p("1", "VIN", -100, 0), p("2", "GND", -100, -50), p("3", "3V3", 100, 0),
		p("4", "EN", 100, 50), p("5", "PG", 100, -50), p("6", "NC1", 0, 100),
		p("7", "NC2", 0, -100), p("8", "GND", -100, 50),
	})
	c9 := mkComp("c9", "C9", 850, 200, 60, 30, []apPad{p("1", "3V3", 0, 0), p("2", "GND", 30, 0)})

	moves, diags := planAutoPlace([]apComp{j3, u9, c9}, defaultApOptions())
	for _, m := range moves {
		if m.Designator == "J3" {
			t.Errorf("high-pin connector J3 must not move, got %+v", m)
		}
		if m.Designator == "C9" && m.Main != "U9" {
			t.Errorf("C9 must belong to U9, not %q", m.Main)
		}
	}
	foundC9 := false
	for _, m := range moves {
		if m.Designator == "C9" {
			foundC9 = true
		}
	}
	if !foundC9 {
		t.Errorf("C9 should be placed against U9, moves: %+v diags: %+v", moves, diags)
	}
	connDiag := false
	for _, d := range diags {
		if d.Designator == "J3" && strings.Contains(d.Reason, "connector") {
			connDiag = true
		}
	}
	if !connDiag {
		t.Errorf("expected a high-pin-connector diag for J3, got %+v", diags)
	}

	// --anchor J3 forces it back into main; --exclude-main U9 demotes U9 (stays put).
	opt := defaultApOptions()
	opt.anchors = designatorSet("J3")
	opt.excludeMain = designatorSet("u9") // case-insensitive
	moves, diags = planAutoPlace([]apComp{j3, u9, c9}, opt)
	for _, m := range moves {
		if m.Designator == "C9" && m.Main != "J3" {
			t.Errorf("with --anchor J3 --exclude-main U9, C9's main = %q, want J3", m.Main)
		}
		if m.Designator == "U9" {
			t.Errorf("excluded U9 must stay put, got %+v", m)
		}
	}
	excluded := false
	for _, d := range diags {
		if d.Designator == "U9" && strings.Contains(d.Reason, "exclude-main") {
			excluded = true
		}
	}
	if !excluded {
		t.Errorf("expected an exclude-main diag for U9, got %+v", diags)
	}
}

// Live 2026-10-13 (ESP32-S3 blink demo): the one-way packer started C1/C3 at the
// module's top and pushed them into the full-width antenna keep-out, 200–320mil
// from the 3V3 pin they decouple.
func TestPlanAutoPlaceKeepsSatellitesOutOfKeepoutsAndNearTheirPin(t *testing.T) {
	u1pads := []apPad{
		p("1", "GND", 29.5, -643), p("2", "3V3", 29.5, -693), p("3", "EN", 29.5, -743), p("4", "LED_CTRL", 29.5, -793),
		p("27", "IO0", 718.5, -1293), p("40", "GND", 718.5, -643), p("41", "GND", 315, -947),
		p("5", "", 29.5, -843), p("6", "", 29.5, -893), p("7", "", 29.5, -943),
	}
	comps := []apComp{
		mkComp("u1", "U1", 374, -858, 748, 1030, u1pads),
		mkComp("c1", "C1", -120, -805, 160.6, 77.2, []apPad{p("1", "3V3", 0, 0), p("2", "GND", 0, 0)}),
		mkComp("c3", "C3", -120, -690, 74.8, 45.3, []apPad{p("1", "3V3", 0, 0), p("2", "GND", 0, 0)}),
		mkComp("r1", "R1", -120, -930, 80.4, 45.3, []apPad{p("2", "3V3", 0, 0), p("1", "EN", 0, 0)}),
		mkComp("c2", "C2", -120, -1045, 74.8, 45.3, []apPad{p("1", "EN", 0, 0), p("2", "GND", 0, 0)}),
		mkComp("r3", "R3", -120, -1165, 80.4, 45.3, []apPad{p("1", "LED_CTRL", 0, 0), p("2", "LED1_N2", 0, 0)}),
	}
	opt := defaultApOptions()
	keepout := layoutBBox{MinX: -240, MaxX: 940, MinY: -643, MaxY: -343}
	opt.keepouts = []layoutBBox{keepout}
	opt.board = &layoutBBox{MinX: -240, MaxX: 940, MinY: -1600, MaxY: -343}
	moves, diags := planAutoPlace(comps, opt)
	// v2 adds distance-limit diags ("placed Nmil from … (limit …)") for parts that
	// ARE placed but sit beyond the decoupling limit; only a missing move is a failure.
	for _, d := range diags {
		if strings.Contains(d.Reason, "no free slot") {
			t.Fatalf("every satellite must find a slot: %+v", diags)
		}
	}
	if len(moves) != 5 {
		t.Fatalf("want 5 satellites moved, got %d (diags %+v)", len(moves), diags)
	}
	size := map[string][2]float64{}
	for _, c := range comps {
		size[c.designator] = [2]float64{c.width(), c.height()}
	}
	for _, m := range moves {
		// On a left/right edge a 2-pin part is turned to face the chip, so its
		// extent along the edge (y) is its short side.
		half := math.Max(size[m.Designator][0], size[m.Designator][1]) / 2
		if m.Edge == "left" || m.Edge == "right" {
			half = math.Min(size[m.Designator][0], size[m.Designator][1]) / 2
		}
		if m.NewY+half > keepout.MinY {
			t.Errorf("%s at y=%.1f reaches into the antenna keep-out (y > %.0f)", m.Designator, m.NewY, keepout.MinY)
		}
		if m.Designator == "C1" || m.Designator == "C3" {
			if d := math.Abs(m.NewY - (-693)); d > 200 {
				t.Errorf("%s decouples U1.2 (y=-693) but sits %.0fmil away along the edge", m.Designator, d)
			}
		}
	}
}

// ── v2 packer: rings, obstacles, cost, limits ────────────────────────────────

// v2Chip is a 400×400 main chip centered on the origin (bbox ±200). `left` pads
// are placed on the left edge (x=-190); filler pads on the right edge with no net
// bring the distinct-pin count over the main-chip threshold.
func v2Chip(left ...apPad) apComp {
	pads := append([]apPad(nil), left...)
	for i := 0; i < 8; i++ {
		pads = append(pads, p(fmt.Sprintf("F%d", i), "", 190, float64(-140+i*40)))
	}
	return mkComp("u1", "U1", 0, 0, 400, 400, pads)
}

// v2Opt: no rotation so a w×h part keeps w across the edge and h along it.
func v2Opt() apOptions {
	opt := defaultApOptions() // gap 40, pitch 30
	opt.rotate = false
	return opt
}

func v2Move(t *testing.T, moves []apMove, des string) apMove {
	t.Helper()
	for _, m := range moves {
		if m.Designator == des {
			return m
		}
	}
	t.Fatalf("%s not placed; moves=%+v", des, moves)
	return apMove{}
}

func diagFor(diags []apDiag, des string) string {
	for _, d := range diags {
		if d.Designator == des {
			return d.Reason
		}
	}
	return ""
}

// Ring 0 is fully blocked by a keep-out: the part must land in ring 1 (one
// ringStep = widest satellite + pitch further out) instead of "no free slot".
func TestPlanAutoPlaceV2_RingFallbackWhenRingZeroBlocked(t *testing.T) {
	chip := v2Chip(p("1", "SIG", -190, 0))
	r1 := mkComp("r1", "R1", 900, 900, 60, 30, []apPad{p("1", "SIG", 0, 0), p("2", "GND", 0, 0)})
	opt := v2Opt()
	// Ring 0 body is x ∈ [-300,-240]; ring 1 is x ∈ [-390,-330].
	opt.keepouts = []layoutBBox{{MinX: -320, MaxX: -230, MinY: -2500, MaxY: 2500}}
	moves, diags := planAutoPlace([]apComp{chip, r1}, opt)
	if len(diags) != 0 {
		t.Fatalf("ring 1 is free, expected no diag: %+v", diags)
	}
	m := v2Move(t, moves, "R1")
	if m.NewX != -360 || m.NewY != 0 {
		t.Errorf("R1 at (%.1f,%.1f), want ring 1 slot (-360,0)", m.NewX, m.NewY)
	}

	// All three rings blocked → no slot, and the diag still names the blocker.
	opt.keepouts = []layoutBBox{{MinX: -5000, MaxX: -210, MinY: -3000, MaxY: 3000}}
	moves, diags = planAutoPlace([]apComp{chip, r1}, opt)
	if len(moves) != 0 {
		t.Fatalf("nothing is free, got moves %+v", moves)
	}
	if r := diagFor(diags, "R1"); !strings.Contains(r, "no free slot") || !strings.Contains(r, "blocked by keep-out") {
		t.Errorf("want a no-free-slot diag naming the keep-out, got %q", r)
	}
}

// A locked (non-satellite) part sitting on the ideal slot is an obstacle with zero
// clearance: the satellite slides along the edge until it just clears it.
func TestPlanAutoPlaceV2_LockedPartIsAvoided(t *testing.T) {
	chip := v2Chip(p("1", "SIG", -190, 0))
	locked := mkComp("q1", "Q1", -270, 0, 60, 30, nil)
	locked.locked = true
	r1 := mkComp("r1", "R1", 900, 900, 60, 30, []apPad{p("1", "SIG", 0, 0), p("2", "GND", 0, 0)})
	moves, diags := planAutoPlace([]apComp{chip, locked, r1}, v2Opt())
	if len(diags) != 0 {
		t.Fatalf("unexpected diags %+v", diags)
	}
	m := v2Move(t, moves, "R1")
	if m.NewX != -270 || m.NewY != -40 {
		t.Errorf("R1 at (%.1f,%.1f), want (-270,-40): beside the locked part with apObstacleClearMil, still in ring 0", m.NewX, m.NewY)
	}
	for _, mv := range moves {
		if mv.Designator == "Q1" {
			t.Errorf("locked part must not move: %+v", mv)
		}
	}
}

// The smallest capacitance claims the slot nearest the pin; an unknown value
// sorts after every known one even when its designator sorts first.
func TestPlanAutoPlaceV2_SmallestCapacitanceNearestPin(t *testing.T) {
	chip := v2Chip(p("1", "3V3", -190, 0))
	cap2 := func(id, des, name string) apComp {
		c := mkComp(id, des, 900, 900, 60, 30, []apPad{p("1", "3V3", 0, 0), p("2", "GND", 0, 0)})
		c.name = name
		return c
	}
	comps := []apComp{
		chip,
		cap2("c0", "C0", "STM32-BULK"), // unknown value: sorts last despite the first designator
		cap2("c1", "C1", "10uF"),
		cap2("c2", "C2", "100nF"),
		cap2("c3", "C3", "1uF"),
	}
	moves, diags := planAutoPlace(comps, v2Opt())
	// Slot costs: ring 0 at y=0 (0), -60, +60 (60); ring 1 at y=0 costs ringStep=90,
	// cheaper than a fourth ring-0 slot (120), so the unknown value goes there.
	want := map[string][2]float64{"C2": {-270, 0}, "C3": {-270, -60}, "C1": {-270, 60}, "C0": {-360, 0}}
	for des, xy := range want {
		m := v2Move(t, moves, des)
		if m.NewX != xy[0] || m.NewY != xy[1] {
			t.Errorf("%s at (%.1f,%.1f), want (%.0f,%.0f) (100nF < 1uF < 10uF < unknown)", des, m.NewX, m.NewY, xy[0], xy[1])
		}
	}
	// Only the far one (140mil, beyond the 100mil decoupling limit) is reported.
	if len(diags) != 1 || diags[0].Designator != "C0" || !strings.Contains(diags[0].Reason, "limit 100mil") {
		t.Errorf("want one limit diag for C0, got %+v", diags)
	}
}

// A body parked in front of a pad of ANOTHER net blocks that pad's fan-out. With
// an equally near clean slot the satellite takes it; when the pad shares the
// satellite's net there is no penalty and the on-target slot stays.
func TestPlanAutoPlaceV2_EscapeStripShiftsOffForeignPad(t *testing.T) {
	run := func(foreignNet string) apMove {
		chip := v2Chip(p("1", "3V3", -190, 0),
			apPad{num: "9", net: foreignNet, x: -190, y: 0, layer: 1, w: 10, h: 10})
		c := mkComp("c1", "C1", 900, 900, 40, 20, []apPad{p("1", "3V3", 0, 0), p("2", "GND", 0, 0)})
		opt := v2Opt()
		opt.gap = 10 // body x ∈ [-250,-210], fully inside the 50mil-deep strip
		moves, diags := planAutoPlace([]apComp{chip, c}, opt)
		if len(diags) != 0 {
			t.Fatalf("foreign=%s: unexpected diags %+v", foreignNet, diags)
		}
		return v2Move(t, moves, "C1")
	}
	if m := run("SIG"); m.NewY != -20 {
		t.Errorf("foreign SIG pad in front: C1 y=%.1f, want -20 (nearest clean slot, -d side first)", m.NewY)
	}
	if m := run("GND"); m.NewY != 0 {
		t.Errorf("a pad on the cap's own net has no escape strip: C1 y=%.1f, want 0", m.NewY)
	}
}

// Inductors add a penalty for sitting near a keep-out. With the centre slot taken,
// the two equally near slots differ only by that penalty: an inductor picks the
// side away from the keep-out, a resistor keeps the usual -d side.
func TestPlanAutoPlaceV2_InductorMovesAwayFromKeepout(t *testing.T) {
	run := func(des string) apMove {
		chip := v2Chip(p("1", "SW", -190, 0))
		blocker := mkComp("q1", "Q1", -270, 0, 60, 30, nil)
		blocker.locked = true
		part := mkComp("p1", des, 900, 900, 60, 30, []apPad{p("1", "SW", 0, 0), p("2", "GND", 0, 0)})
		opt := v2Opt()
		opt.keepouts = []layoutBBox{{MinX: -320, MaxX: -230, MinY: -300, MaxY: -100}}
		moves, diags := planAutoPlace([]apComp{chip, blocker, part}, opt)
		if len(diags) != 0 {
			t.Fatalf("%s: unexpected diags %+v", des, diags)
		}
		return v2Move(t, moves, des)
	}
	if m := run("R1"); m.NewY != -40 {
		t.Errorf("resistor y=%.1f, want -40 (tie → -d side first, clear of the blocker)", m.NewY)
	}
	if m := run("L1"); m.NewY != 40 {
		t.Errorf("inductor y=%.1f, want +40 (away from the keep-out below)", m.NewY)
	}
}

// A decoupling cap pushed out of its 100mil limit is still placed, and the diag
// says by how much and which part is in the way.
func TestPlanAutoPlaceV2_FarPlacementDiagNamesBlocker(t *testing.T) {
	chip := v2Chip(p("1", "3V3", -190, 0))
	wall := mkComp("d1", "D1", -270, 0, 60, 400, nil) // x ∈ [-300,-240], y ∈ [-200,200]
	wall.locked = true
	c1 := mkComp("c1", "C1", 900, 900, 60, 30, []apPad{p("1", "3V3", 0, 0), p("2", "GND", 0, 0)})
	moves, diags := planAutoPlace([]apComp{chip, wall, c1}, v2Opt())
	m := v2Move(t, moves, "C1")
	if m.NewX != -360 || m.NewY != 0 {
		t.Errorf("C1 at (%.1f,%.1f), want ring 1 (-360,0)", m.NewX, m.NewY)
	}
	r := diagFor(diags, "C1")
	for _, want := range []string{"placed 140mil from U1.3V3", "limit 100mil", "blocked by D1", "move it ~215mil away"} {
		if !strings.Contains(r, want) {
			t.Errorf("diag %q lacks %q", r, want)
		}
	}
	// Same cap, nothing in the way → within the limit, no diag at all.
	if _, diags := planAutoPlace([]apComp{chip, c1}, v2Opt()); len(diags) != 0 {
		t.Errorf("a near placement must be silent, got %+v", diags)
	}
}

func TestParseCapacitanceFarads(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"100nF", 1e-7, true},
		{"100NF", 1e-7, true},
		{"0.1uF", 1e-7, true},
		{"0.1µF", 1e-7, true}, // U+00B5
		{"0.1μF", 1e-7, true}, // U+03BC
		{"10u", 1e-5, true},
		{"10 uF", 1e-5, true},
		{"4.7uF/16V", 4.7e-6, true},
		{"1nF", 1e-9, true},
		{"22pF", 2.2e-11, true},
		{"100nF_0402", 1e-7, true},
		{"1mF", 1e-3, true},
		{"", 0, false},
		{"STM32F415RGT6", 0, false},
		{"CL05B104KO5NNNC", 0, false},
		{"0402", 0, false},
		{"10k", 0, false},
		{"10uH", 0, false},  // inductance, not capacitance
		{"100mΩ", 0, false}, // resistance
		{"10m", 0, false},   // bare milli: ambiguous, needs the F
		{"abc 100nF", 0, false},
	}
	for _, c := range cases {
		got, ok := parseCapacitanceFarads(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && math.Abs(got-c.want) > math.Abs(c.want)*1e-9 {
			t.Errorf("%q: %g F, want %g F", c.in, got, c.want)
		}
	}
}

// Satellites of two different edges share one occupancy list. Three parts on the
// left edge spill up past the chip's top-left corner, exactly where the wide top-
// edge parts want to go (R3 at y∈[215,245] vs R4 at y∈[240,270], x ranges overlap
// by 10mil); a per-group packer would stack them, the shared one must not.
func TestPlanAutoPlaceV2_CornerSatellitesNeverOverlap(t *testing.T) {
	chip := v2Chip(p("1", "SIGA", -190, 170), p("2", "SIGB", -190, 195)) // 2nd pad: top edge
	comps := []apComp{chip}
	for i, des := range []string{"R1", "R2", "R3"} {
		comps = append(comps, mkComp(fmt.Sprintf("a%d", i), des, 900, 900+float64(i)*100, 60, 30, []apPad{p("1", "SIGA", 0, 0), p("2", "GND", 0, 0)}))
	}
	for i, des := range []string{"R4", "R5", "R6"} {
		comps = append(comps, mkComp(fmt.Sprintf("b%d", i), des, 1500, 900+float64(i)*100, 120, 30, []apPad{p("1", "SIGB", 0, 0), p("2", "GND", 0, 0)}))
	}
	size := map[string][2]float64{"R1": {60, 30}, "R2": {60, 30}, "R3": {60, 30}, "R4": {120, 30}, "R5": {120, 30}, "R6": {120, 30}}
	opt := v2Opt()
	moves, diags := planAutoPlace(comps, opt)
	if len(moves) != 6 {
		t.Fatalf("want 6 placed, got %d (diags %+v)", len(moves), diags)
	}
	edges := map[string]bool{}
	box := map[string]layoutBBox{}
	for _, m := range moves {
		edges[m.Edge] = true
		sz := size[m.Designator] // rotate=false: the body keeps its size
		box[m.Designator] = layoutBBox{MinX: m.NewX - sz[0]/2, MaxX: m.NewX + sz[0]/2, MinY: m.NewY - sz[1]/2, MaxY: m.NewY + sz[1]/2}
	}
	if !edges["left"] || !edges["top"] {
		t.Fatalf("test needs satellites on both left and top edges, got %v", edges)
	}
	des := []string{"R1", "R2", "R3", "R4", "R5", "R6"}
	for i := 0; i < len(des); i++ {
		for j := i + 1; j < len(des); j++ {
			if apOverlaps(apInflate(box[des[i]], opt.pitch), box[des[j]]) {
				t.Errorf("%s %+v and %s %+v are closer than pitch %.0f", des[i], box[des[i]], des[j], box[des[j]], opt.pitch)
			}
		}
	}
}

// ── RF matching chain ────────────────────────────────────────────────────────

// rfChip is a 400×400 chip (bbox ±200) whose right edge carries the RF pad
// (net RF_CHIP, at (190,0)) and a 3V3 pad at (190,-20); filler pads bring the
// distinct-pin count over the main-chip threshold.
func rfChip() apComp {
	pads := []apPad{p("20", "RF_CHIP", 190, 0), p("21", "3V3", 190, -20)}
	for i := 0; i < 8; i++ {
		pads = append(pads, p(fmt.Sprintf("F%d", i), "", -190, float64(-140+i*40)))
	}
	return mkComp("u1", "U1", 0, 0, 400, 400, pads)
}

// rfAnt is an antenna at (900,0), bbox x ∈ [850,950]; its feed pad (net
// ANT_FEED) is at the left side, (850,0), facing the chip.
func rfAnt() apComp {
	return mkComp("ant", "ANT1", 900, 0, 100, 60, []apPad{p("1", "ANT_FEED", 850, 0), p("2", "GND", 950, 0)})
}

// rfPart is a 60×30 two-pin part centred at (1500,y0): pad "1" (netA) at its
// left end, pad "2" (netB) at its right end, as drawn (rotation 0).
func rfPart(des, name, netA, netB string, y0 float64) apComp {
	c := mkComp(strings.ToLower(des), des, 1500, y0, 60, 30, []apPad{p("1", netA, 1480, y0), p("2", netB, 1520, y0)})
	c.name = name
	return c
}

// rfMoveBox is the body a move occupies (all test parts are 60×30 at rotation 0
// with the anchor at the bbox centre, so a quarter turn swaps the extents).
func rfMoveBox(m apMove) layoutBBox {
	w, h := 60.0, 30.0
	if m.SetRot && oddQuarter(m.NewRot) {
		w, h = h, w
	}
	return layoutBBox{MinX: m.NewX - w/2, MaxX: m.NewX + w/2, MinY: m.NewY - h/2, MaxY: m.NewY + h/2}
}

// chip → L1 → C1 → ANT1, with C2 shunting the L1/C1 junction (N1) to ground.
func rfBoard() []apComp {
	return []apComp{
		rfChip(), rfAnt(),
		rfPart("C1", "1.2pF", "ANT_FEED", "N1", 1000),
		rfPart("L1", "3.9nH", "RF_CHIP", "N1", 1100),
		rfPart("C2", "1.8pF", "N1", "GND", 1200),
	}
}

func TestIsAntennaComp(t *testing.T) {
	for _, c := range []struct {
		des, name string
		want      bool
	}{
		{"ANT1", "", true}, {"ant12", "", true}, {"AE1", "", true}, {"ae3", "", true},
		{"J7", "2.4G Antenna Connector", true}, {"U9", "PCB ANTENNA", true},
		{"ANTX", "", false}, {"C1", "100nF", false}, {"AEX1", "", false}, {"U1", "ESP32", false},
	} {
		if got := isAntennaComp(apComp{designator: c.des, name: c.name}); got != c.want {
			t.Errorf("isAntennaComp(%q,%q)=%v, want %v", c.des, c.name, got, c.want)
		}
	}
	// A pin-rich module that only mentions its antenna in the Name stays a main chip.
	module := rfChip()
	module.designator, module.name = "U7", "ESP32-WROOM-32 (PCB antenna)"
	if isAntennaComp(module) || !isMainComp(module, defaultApOptions()) {
		t.Error("a 10-pin module whose Name mentions an antenna must remain a main chip")
	}
	// A pin-rich antenna must still never become a main chip.
	many := rfChip()
	many.designator = "ANT2"
	if isMainComp(many, defaultApOptions()) {
		t.Error("an antenna must not be a main chip whatever its pin count")
	}
}

func TestPlanAutoPlaceRF_ChainLiesOnFeedLineAntennaFirst(t *testing.T) {
	moves, diags := planAutoPlace(rfBoard(), defaultApOptions())
	for _, m := range moves {
		if m.Designator == "ANT1" {
			t.Errorf("the antenna must not move: %+v", m)
		}
	}
	if r := diagFor(diags, "ANT1"); !strings.Contains(r, "mechanical anchor") {
		t.Errorf("want the antenna anchor diag, got %q", r)
	}
	for _, d := range diags {
		if d.Designator != "ANT1" {
			t.Errorf("a clean chain must be silent, got %+v", d)
		}
	}
	c1, l1, c2 := v2Move(t, moves, "C1"), v2Move(t, moves, "L1"), v2Move(t, moves, "C2")
	for _, m := range []apMove{c1, l1, c2} {
		if m.Edge != "rf-chain" || m.Main != "U1" || m.Via != "rf:ANT1" {
			t.Errorf("%s: edge/main/via = %q/%q/%q, want rf-chain/U1/rf:ANT1", m.Designator, m.Edge, m.Main, m.Via)
		}
	}
	if c1.TargetNet != "ANT_FEED" || l1.TargetNet != "N1" || c2.TargetNet != "N1" {
		t.Errorf("target nets C1=%s L1=%s C2=%s, want ANT_FEED, N1, N1", c1.TargetNet, l1.TargetNet, c2.TargetNet)
	}
	// P=(190,0), A=(850,0): the line is y=0. Series parts sit on it, antenna side first.
	for _, m := range []apMove{c1, l1} {
		if d := math.Abs(m.NewY); d > 40 {
			t.Errorf("%s is %.0fmil off the feed line", m.Designator, d)
		}
	}
	if !(c1.NewX > l1.NewX && l1.NewX > 190) || c1.NewX > 850 {
		t.Errorf("order must be chip < L1 < C1 < antenna, got L1 x=%.0f C1 x=%.0f", l1.NewX, c1.NewX)
	}
	// Rotation: C1's ANT_FEED pad (pad 1, left end) must face the antenna (+x) → 180;
	// L1's N1 pad (pad 2, right end) faces the antenna already → 0.
	if !c1.SetRot || c1.NewRot != 180 || !l1.SetRot || l1.NewRot != 0 {
		t.Errorf("rotations C1=%v/%.0f L1=%v/%.0f, want 180 and 0", c1.SetRot, c1.NewRot, l1.SetRot, l1.NewRot)
	}
	// The shunt hangs beside the line, between the two series parts' span, rotated across it.
	if math.Abs(c2.NewY) < 30 || math.Abs(c2.NewY) > 120 || c2.NewX < l1.NewX-60 || c2.NewX > c1.NewX+60 {
		t.Errorf("shunt C2 at (%.0f,%.0f) is not beside the chain", c2.NewX, c2.NewY)
	}
	if !c2.SetRot || (c2.NewRot != 90 && c2.NewRot != 270) {
		t.Errorf("shunt must be turned across the line, got rot %.0f (set=%v)", c2.NewRot, c2.SetRot)
	}
	// No overlaps among members, nor with chip or antenna.
	opt := defaultApOptions()
	boxes := map[string]layoutBBox{"C1": rfMoveBox(c1), "L1": rfMoveBox(l1), "C2": rfMoveBox(c2)}
	names := []string{"C1", "L1", "C2"}
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if apOverlaps(apInflate(boxes[names[i]], opt.pitch), boxes[names[j]]) {
				t.Errorf("%s and %s closer than pitch", names[i], names[j])
			}
		}
		if apOverlaps(boxes[names[i]], layoutBBox{MinX: 850, MaxX: 950, MinY: -30, MaxY: 30}) ||
			apOverlaps(boxes[names[i]], layoutBBox{MinX: -200, MaxX: 200, MinY: -200, MaxY: 200}) {
			t.Errorf("%s overlaps the antenna or the chip", names[i])
		}
	}
}

// An antenna whose feed net never reaches a chip gets a diag; its parts are not
// treated as a chain, so a part with a chip net still goes through normal packing.
func TestPlanAutoPlaceRF_UnreachableAntennaFallsBack(t *testing.T) {
	comps := []apComp{
		rfChip(), rfAnt(),
		rfPart("C1", "1.2pF", "ANT_FEED", "N1", 1000), // dead end: N1 goes nowhere
		rfPart("L1", "3.9nH", "RF_CHIP", "N2", 1100),  // chip side, but not joined to N1
		rfPart("C9", "100nF", "3V3", "GND", 1200),
	}
	moves, diags := planAutoPlace(comps, defaultApOptions())
	found := false
	for _, d := range diags {
		found = found || (d.Designator == "ANT1" && strings.Contains(d.Reason, "no matching chain"))
	}
	if !found {
		t.Errorf("want a 'no matching chain' diag on ANT1, got %+v", diags)
	}
	for _, m := range moves {
		if m.Edge == "rf-chain" || strings.HasPrefix(m.Via, "rf:") {
			t.Errorf("no chain exists, yet %s was placed as RF: %+v", m.Designator, m)
		}
		if m.Designator == "ANT1" {
			t.Errorf("the antenna must not move: %+v", m)
		}
	}
	if m := v2Move(t, moves, "L1"); m.Via != "local" || m.Edge != "right" {
		t.Errorf("L1 must fall back to normal packing at the RF pad, got %+v", m)
	}
	v2Move(t, moves, "C9")
	if r := diagFor(diags, "C1"); !strings.Contains(r, "no shared net path") {
		t.Errorf("the dead-end part has nowhere to go, got %q", r)
	}
}

// A keep-out around the antenna must not block the matching network that sits at
// the feed, but still blocks an ordinary decoupling cap that the same keep-out covers.
func TestPlanAutoPlaceRF_AntennaKeepoutSparesChainNotOrdinaryCaps(t *testing.T) {
	comps := append(rfBoard(), rfPart("C9", "100nF", "3V3", "GND", 1300))
	baseMoves, _ := planAutoPlace(comps, defaultApOptions())
	opt := defaultApOptions()
	ko := layoutBBox{MinX: 230, MaxX: 1100, MinY: -100, MaxY: 100} // touches the antenna zone
	opt.keepouts = []layoutBBox{ko}
	moves, diags := planAutoPlace(comps, opt)
	for _, d := range diags {
		if d.Designator != "ANT1" && !strings.Contains(d.Reason, "limit") {
			t.Errorf("unexpected diag %+v", d)
		}
	}
	for _, des := range []string{"C1", "L1", "C2"} {
		m := v2Move(t, moves, des)
		if m.Edge != "rf-chain" {
			t.Errorf("%s left the chain: %+v", des, m)
		}
		b0 := v2Move(t, baseMoves, des)
		if m.NewX != b0.NewX || m.NewY != b0.NewY {
			t.Errorf("%s moved because of the keep-out: (%.0f,%.0f) vs (%.0f,%.0f)", des, m.NewX, m.NewY, b0.NewX, b0.NewY)
		}
	}
	c9, c9base := v2Move(t, moves, "C9"), v2Move(t, baseMoves, "C9")
	if c9base.NewY != -20 {
		t.Fatalf("precondition: without the keep-out C9 sits at the 3V3 pin (y=-20), got %.0f", c9base.NewY)
	}
	if apOverlaps(layoutBBox{MinX: c9.NewX - 30, MaxX: c9.NewX + 30, MinY: c9.NewY - 15, MaxY: c9.NewY + 15}, ko) || c9.NewY > -115 {
		t.Errorf("ordinary cap C9 at (%.0f,%.0f) must stay out of the keep-out", c9.NewX, c9.NewY)
	}
}

// A wall across the straight path leaves no slot on the line: the member is still
// placed (generic search at the chip RF pad) and the diag says how far off it is.
func TestPlanAutoPlaceRF_BlockedPathReportsOffLineMember(t *testing.T) {
	wall := mkComp("q1", "Q1", 537.5, 0, 615, 120, nil) // x ∈ [230,845], y ∈ [-60,60]
	wall.locked = true
	comps := append(rfBoard(), wall)
	moves, diags := planAutoPlace(comps, defaultApOptions())
	r := diagFor(diags, "C1")
	for _, want := range []string{"C1 sits 85mil off the RF feed line U1→ANT1 (limit 40mil)", "free the straight path"} {
		if !strings.Contains(r, want) {
			t.Errorf("C1 diag %q lacks %q", r, want)
		}
	}
	m := v2Move(t, moves, "C1")
	if m.Edge != "rf-chain" || m.Via != "rf:ANT1" {
		t.Errorf("a fallback member keeps its RF identity, got %+v", m)
	}
	if math.Abs(m.NewY) < 40 {
		t.Errorf("C1 at y=%.0f is not off the line", m.NewY)
	}
	// Nothing may overlap the wall.
	for _, mv := range moves {
		if apOverlaps(rfMoveBox(mv), layoutBBox{MinX: 230, MaxX: 845, MinY: -60, MaxY: 60}) {
			t.Errorf("%s was placed on the wall", mv.Designator)
		}
	}
}

// A part on the other side of a double-sided board is not an obstacle: a
// bottom-side cap may sit right under a top-side locked part, while a top-side
// cap must step around it. Through-hole parts (MULTI pads) block both sides.
func TestPlanAutoPlaceV2_OppositeSideDoesNotCollide(t *testing.T) {
	chip := v2Chip(p("1", "SIG", -190, 0))
	chip.layer = 1
	locked := mkComp("q1", "Q1", -270, 0, 60, 30, nil)
	locked.locked, locked.layer = true, 1
	bottom := mkComp("r1", "R1", 900, 900, 60, 30, []apPad{{num: "1", net: "SIG", x: 0, y: 0, layer: 2}, {num: "2", net: "GND", x: 0, y: 0, layer: 2}})
	bottom.layer = 2
	moves, diags := planAutoPlace([]apComp{chip, locked, bottom}, v2Opt())
	if len(diags) != 0 {
		t.Fatalf("unexpected diags %+v", diags)
	}
	if m := v2Move(t, moves, "R1"); m.NewX != -270 || m.NewY != 0 {
		t.Errorf("bottom-side R1 at (%.1f,%.1f), want (-270,0) under the top-side locked part", m.NewX, m.NewY)
	}

	top := mkComp("r1", "R1", 900, 900, 60, 30, []apPad{p("1", "SIG", 0, 0), p("2", "GND", 0, 0)})
	top.layer = 1
	moves, _ = planAutoPlace([]apComp{chip, locked, top}, v2Opt())
	if m := v2Move(t, moves, "R1"); m.NewX == -270 && m.NewY == 0 {
		t.Errorf("top-side R1 must not overlap the top-side locked part")
	}

	tht := mkComp("q1", "Q1", -270, 0, 60, 30, []apPad{{num: "1", x: -270, y: 0, layer: pcbLayerMulti}})
	tht.locked, tht.layer = true, 1
	moves, _ = planAutoPlace([]apComp{chip, tht, bottom}, v2Opt())
	if m := v2Move(t, moves, "R1"); m.NewX == -270 && m.NewY == 0 {
		t.Errorf("a through-hole part blocks the bottom side too")
	}
}

// ── small-IC mains, owner pins, stay satellites ──────────────────────────────

// ldoBoard: MCU U2 (10 pins, origin), a 4-pin LDO U1 at x=1000 sharing 3V3 with
// the MCU, the LDO's own input/output caps (C5 on VIN, C6 on 3V3 beside the LDO),
// an MCU decoupling cap C2, and a SOT-23 Q1 whose GATE net reaches the MCU.
func ldoBoard() []apComp {
	mcu := []apPad{
		p("1", "3V3", -190, 0), p("2", "GND", -190, -60), p("3", "GATE", 190, -60), p("4", "", 190, 0),
		p("5", "", 0, 190), p("6", "", 40, 190), p("7", "", 80, 190), p("8", "", 120, 190),
		p("9", "", 0, -190), p("10", "", 40, -190),
	}
	ldo := []apPad{p("1", "VIN", 950, 20), p("2", "GND", 950, -20), p("3", "3V3", 1050, 0), p("4", "", 1050, 20)}
	return []apComp{
		mkComp("u2", "U2", 0, 0, 400, 400, mcu),
		mkComp("u1", "U1", 1000, 0, 100, 100, ldo),
		mkComp("c5", "C5", 700, 300, 60, 30, []apPad{p("1", "VIN", 680, 300), p("2", "GND", 720, 300)}),
		mkComp("c6", "C6", 1300, 300, 60, 30, []apPad{p("1", "3V3", 1280, 300), p("2", "GND", 1320, 300)}),
		mkComp("c2", "C2", -600, 300, 60, 30, []apPad{p("1", "3V3", -620, 300), p("2", "GND", -580, 300)}),
		mkComp("q1", "Q1", 400, -300, 60, 40, []apPad{p("1", "GATE", 390, -300), p("2", "LOAD", 410, -300), p("3", "GND", 400, -310)}),
	}
}

func movesByDes(moves []apMove) map[string]apMove {
	out := map[string]apMove{}
	for _, m := range moves {
		out[m.Designator] = m
	}
	return out
}

// A 3–4 pin regulator with an IC-style designator is a main: it keeps its own
// input/output caps instead of being dragged to the MCU it shares 3V3 with.
func TestPlanAutoPlace_SmallICIsMainAndKeepsItsCaps(t *testing.T) {
	comps := ldoBoard()
	moves, diags, info := planAutoPlaceDetailed(comps, defaultApOptions())
	var mains []string
	for _, i := range info.Mains {
		mains = append(mains, comps[i].designator)
	}
	if strings.Join(mains, ",") != "U2,U1" {
		t.Fatalf("mains = %v, want U2 and the LDO U1", mains)
	}
	by := movesByDes(moves)
	if _, ok := by["U1"]; ok {
		t.Errorf("the LDO is a main and must not be moved: %+v", by["U1"])
	}
	for des, want := range map[string]string{"C5": "U1", "C6": "U1", "C2": "U2", "Q1": "U2"} {
		if m, ok := by[des]; !ok || m.Main != want {
			t.Errorf("%s main = %q (placed=%v), want %s; diags=%+v", des, m.Main, ok, want, diags)
		}
	}
	// C5 hangs on VIN, which only the LDO has: with U1 a satellite it had no chip at all.
	if v := by["C5"].Via; v != "power" {
		t.Errorf("C5 via = %q, want power", v)
	}
}

// --exclude-main still beats the new rule: a demoted LDO is a satellite again.
func TestPlanAutoPlace_ExcludeMainDemotesSmallIC(t *testing.T) {
	opt := defaultApOptions()
	opt.excludeMain = designatorSet("u1")
	moves, _, info := planAutoPlaceDetailed(ldoBoard(), opt)
	if len(info.Mains) != 1 {
		t.Fatalf("mains = %v, want only the MCU", info.Mains)
	}
	if m, ok := movesByDes(moves)["U1"]; !ok || m.Main != "U2" {
		t.Errorf("demoted U1 must be placed as a satellite of U2, got %+v (placed=%v)", m, ok)
	}
}

func TestIsMainComp_DesignatorPrefixRule(t *testing.T) {
	mk := func(des string, pins int) apComp {
		var pads []apPad
		for i := 1; i <= pins; i++ {
			pads = append(pads, p(fmt.Sprintf("%d", i), "N", 0, 0))
		}
		return mkComp("x", des, 0, 0, 100, 100, pads)
	}
	opt := defaultApOptions()
	for _, tc := range []struct {
		des  string
		pins int
		want bool
	}{
		{"U1", 3, true}, {"u1", 3, true}, {"U12", 4, true}, {"IC1", 3, true}, {"VR1", 3, true}, {"REG1", 5, true}, {"PS1", 3, true},
		{"U1", 2, false}, // below 3 pins
		{"R1", 3, false}, {"C1", 3, false}, {"L1", 3, false}, {"D1", 3, false}, {"LED1", 3, false}, {"FB1", 3, false},
		{"Y1", 3, false}, {"X1", 3, false}, {"F1", 3, false}, {"TP1", 3, false}, {"Q1", 3, false}, // passive prefixes
		{"UF1", 3, false}, {"J1", 3, false}, {"USB1", 3, false}, // not in the list / connector
		{"Q1", 8, true}, {"R1", 8, true}, {"J1", 8, false}, // >= mainPins exactly as before
	} {
		if got := isMainComp(mk(tc.des, tc.pins), opt); got != tc.want {
			t.Errorf("isMainComp(%s, %d pins) = %v, want %v", tc.des, tc.pins, got, tc.want)
		}
	}
	// Precedence: exclude-main > anchor > antenna > rules.
	both := defaultApOptions()
	both.excludeMain = designatorSet("U1")
	both.anchors = designatorSet("U1")
	if isMainComp(mk("U1", 3), both) {
		t.Error("exclude-main must beat anchor")
	}
	anc := defaultApOptions()
	anc.anchors = designatorSet("R1")
	if !isMainComp(mk("R1", 2), anc) {
		t.Error("an anchor is a main whatever its prefix or pin count")
	}
	ant := mk("U9", 3)
	ant.name = "PCB ANTENNA"
	if isMainComp(ant, opt) {
		t.Error("an antenna is never promoted by the prefix rule")
	}
	hi := mk("U9", 3)
	hi.name = "ESP32 (PCB antenna)"
	if isMainComp(hi, opt) {
		t.Error("an antenna-named U part with few pins is an antenna, not a main")
	}
}

// A pinned satellite is only assigned to its owner, pass 1 (signal net) and pass 3
// (power net) alike; a pin on a part or main that does not exist is ignored.
func TestPlanAutoPlace_OwnerPins(t *testing.T) {
	two := func() []apComp {
		l := []apPad{p("1", "3V3", -190, 0), p("2", "SIG", -190, 60), p("3", "", 190, 0), p("4", "", 190, 40),
			p("5", "", 190, 80), p("6", "", 190, 120), p("7", "", 190, -40), p("8", "", 190, -80)}
		r := []apPad{p("1", "3V3", 1010, 0), p("2", "SIG", 1010, 60), p("3", "", 1390, 0), p("4", "", 1390, 40),
			p("5", "", 1390, 80), p("6", "", 1390, 120), p("7", "", 1390, -40), p("8", "", 1390, -80)}
		return []apComp{
			mkComp("u1", "U1", 0, 0, 400, 400, l),
			mkComp("u2", "U2", 1200, 0, 400, 400, r),
			mkComp("c1", "C1", -500, 0, 60, 30, []apPad{p("1", "3V3", 0, 0), p("2", "GND", 0, 0)}), // power: nearest U1
			mkComp("r1", "R1", -500, 100, 60, 30, []apPad{p("1", "SIG", 0, 0), p("2", "X", 0, 0)}), // signal: nearest U1
		}
	}
	by := movesByDes(func() []apMove { m, _ := planAutoPlace(two(), defaultApOptions()); return m }())
	if by["C1"].Main != "U1" || by["R1"].Main != "U1" {
		t.Fatalf("unpinned baseline: C1→%s R1→%s, want U1", by["C1"].Main, by["R1"].Main)
	}
	opt := defaultApOptions()
	opt.owners = map[string]string{"C1": "U2", "R1": "U2"}
	by = movesByDes(func() []apMove { m, _ := planAutoPlace(two(), opt); return m }())
	if by["C1"].Main != "U2" || by["C1"].Via != "power" {
		t.Errorf("pinned C1 = %+v, want a power placement on U2", by["C1"])
	}
	if by["R1"].Main != "U2" || by["R1"].Via != "local" {
		t.Errorf("pinned R1 = %+v, want a local placement on U2", by["R1"])
	}
	// A pin whose target is not a main is ignored for that part.
	opt.owners = map[string]string{"C1": "U9", "R1": "C1"}
	by = movesByDes(func() []apMove { m, _ := planAutoPlace(two(), opt); return m }())
	if by["C1"].Main != "U1" || by["R1"].Main != "U1" {
		t.Errorf("pins to non-mains must be ignored: C1→%s R1→%s", by["C1"].Main, by["R1"].Main)
	}
	// A pin the part cannot honour (no pad of that net on the owner) leaves it unplaced with a clear diag.
	one := two()
	one[3].pads = []apPad{p("1", "ONLY_U1", 0, 0), p("2", "X", 0, 0)}
	one[0].pads = append(one[0].pads, p("9", "ONLY_U1", -190, 100))
	opt.owners = map[string]string{"R1": "U2"}
	moves, diags := planAutoPlace(one, opt)
	if _, ok := movesByDes(moves)["R1"]; ok || !strings.Contains(diagFor(diags, "R1"), "pinned owner U2") {
		t.Errorf("R1 pinned to a chip it cannot reach must be unplaced naming U2: %q", diagFor(diags, "R1"))
	}
	// Chain pass: a pinned part only inherits from a neighbour on its own owner.
	chain := two()
	chain = append(chain,
		mkComp("r3", "R3", -500, 200, 60, 30, []apPad{p("1", "SIG", 0, 0), p("2", "LA", 0, 0)}),
		mkComp("d1", "D1", -500, 300, 60, 30, []apPad{p("1", "LA", 0, 0), p("2", "GND", 0, 0)}))
	by = movesByDes(func() []apMove { m, _ := planAutoPlace(chain, defaultApOptions()); return m }())
	if by["D1"].Main != "U1" || by["D1"].Via != "chain:R3" {
		t.Fatalf("unpinned D1 = %+v, want chain:R3 on U1", by["D1"])
	}
	opt.owners = map[string]string{"R3": "U2"}
	by = movesByDes(func() []apMove { m, _ := planAutoPlace(chain, opt); return m }())
	if by["R3"].Main != "U2" || by["D1"].Main != "U2" {
		t.Errorf("D1 must follow R3 to U2: R3→%s D1→%s (%+v)", by["R3"].Main, by["D1"].Main, by["D1"])
	}
}

// A stay satellite is neither moved nor reported unplaced; it is an obstacle for
// the satellites that are still planned.
func TestPlanAutoPlace_StaySatelliteIsAnObstacle(t *testing.T) {
	opt := v2Opt()
	chip := v2Chip(p("L1", "3V3", -190, 0))
	// Where the 3V3 cap would like to sit: ring 0 on the left edge, centred on the pad.
	ideal := mkComp("c1", "C1", -200-opt.gap-30, 0, 60, 30, []apPad{p("1", "3V3", 0, 0), p("2", "GND", 0, 0)})
	cap2 := mkComp("c2", "C2", 500, 500, 60, 30, []apPad{p("1", "3V3", 0, 0), p("2", "GND", 0, 0)})
	comps := []apComp{chip, ideal, cap2}

	moves, _, base := planAutoPlaceDetailed(comps, opt)
	// Baseline: both caps are re-planned; C1 takes the on-pin slot, C2 the next one a pitch (30) further.
	if c1, c2 := v2Move(t, moves, "C1"), v2Move(t, moves, "C2"); math.Abs(c1.NewY) > 1 || math.Abs(c2.NewY) < 59 {
		t.Fatalf("baseline: C1/C2 y = %.0f/%.0f, want 0 and ±60", c1.NewY, c2.NewY)
	}
	opt.stay = map[int]bool{1: true}
	moves, _, info := planAutoPlaceDetailed(comps, opt)
	if !info.Stayed[1] || len(info.Stayed) != 1 {
		t.Errorf("Stayed = %v, want only C1 (idx 1)", info.Stayed)
	}
	if _, moved := movesByDes(moves)["C1"]; moved {
		t.Error("the stay satellite must not be moved")
	}
	c2 := v2Move(t, moves, "C2")
	// C2 now avoids C1's body (with apObstacleClearMil), i.e. cannot sit on the same spot.
	body := layoutBBox{MinX: c2.NewX - 30, MaxX: c2.NewX + 30, MinY: c2.NewY - 15, MaxY: c2.NewY + 15}
	if apOverlaps(body, layoutBBox{MinX: ideal.minX, MinY: ideal.minY, MaxX: ideal.maxX, MaxY: ideal.maxY}) {
		t.Errorf("C2 %+v overlaps the stay satellite", c2)
	}
	// An obstacle needs apObstacleClearMil (never touching), not the satellite pitch: ±40, not ±30 or ±60.
	if math.Abs(math.Abs(c2.NewY)-40) > 1 {
		t.Errorf("C2 y = %.0f, want it 10 mil clear of the stay body (±40)", c2.NewY)
	}
	if info.Owner[1] != 0 || info.Owner[2] != 0 || len(base.Owner) != 2 {
		t.Errorf("owners = %v (baseline %v), want both caps owned by the chip", info.Owner, base.Owner)
	}
}

// Pass 3b: when every 3V3 cap sits by the MCU, nearest-pad assignment would leave
// the LDO that drives 3V3 with no output cap. It takes the one nearest to its
// 3V3 pad, but never the MCU's last one, and never a pinned one.
func TestPlanAutoPlace_RegulatorKeepsOneRailCap(t *testing.T) {
	board := func() []apComp {
		comps := ldoBoard()
		comps[3] = mkComp("c6", "C6", -300, 300, 60, 30, []apPad{p("1", "3V3", -320, 300), p("2", "GND", -280, 300)}) // by the MCU now
		return comps
	}
	by := movesByDes(func() []apMove { m, _, _ := planAutoPlaceDetailed(board(), defaultApOptions()); return m }())
	if by["C6"].Main != "U1" || by["C2"].Main != "U2" {
		t.Errorf("C6 → %q, C2 → %q; want the LDO to take C6 (nearer its 3V3 pad) and the MCU to keep C2", by["C6"].Main, by["C2"].Main)
	}
	// The MCU's only 3V3 cap is never taken.
	b := board()
	only := append(b[:4:4], b[5]) // drop C2: C6 is the single 3V3 cap
	if m := movesByDes(func() []apMove { m, _, _ := planAutoPlaceDetailed(only, defaultApOptions()); return m }())["C6"]; m.Main != "U2" {
		t.Errorf("single 3V3 cap went to %q, want it to stay with U2", m.Main)
	}
	// A pinned cap stays with its pinned owner.
	opt := defaultApOptions()
	opt.owners = map[string]string{"C6": "U2"}
	m, _, _ := planAutoPlaceDetailed(board(), opt)
	if got := movesByDes(m); got["C6"].Main != "U2" || got["C2"].Main != "U1" {
		t.Errorf("with C6 pinned to U2: C6 → %q, C2 → %q; want C6 on U2 and the LDO to take C2", got["C6"].Main, got["C2"].Main)
	}
}
