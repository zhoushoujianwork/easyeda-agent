package app

import (
	"math"
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
		edge    apEdge
		wantRot float64
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
		mk8("u1", "U1", 0, 0),      // col A, lower
		mk8("u2", "U2", 0, 600),    // col A, upper (stacked over U1)
		mk8("u3", "U3", 100, 0),    // col B, lower
		mk8("u4", "U4", 100, 600),  // col B, upper
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
