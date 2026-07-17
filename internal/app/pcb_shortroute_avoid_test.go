package app

import (
	"fmt"
	"testing"
)

func TestSegSegCross(t *testing.T) {
	// Proper X crossing.
	if !segSegCross(0, 0, 10, 10, 0, 10, 10, 0) {
		t.Error("expected proper crossing at (5,5)")
	}
	// Shared endpoint is NOT a crossing.
	if segSegCross(0, 0, 10, 0, 10, 0, 10, 10) {
		t.Error("shared endpoint must not count as a crossing")
	}
	// Parallel, no crossing.
	if segSegCross(0, 0, 10, 0, 0, 5, 10, 5) {
		t.Error("parallel segments must not cross")
	}
}

// A placed vertical track at x=50 (net P, y 15..40) sits between the hop's
// endpoints. The horizontal-first L would cross it at (50,20) — now a HARD
// infeasibility (#119), not just cost; the vertical-first L clears it by a
// legal edge gap. routeWithAvoid must pick vertical-first and report ok.
// (Pre-#119 this test's geometry had the "clear" orientation touching P's
// endpoint — itself a short the cost model tolerated.)
func TestRouteWithAvoid_PicksClearOrientation(t *testing.T) {
	placed := []rtSeg{{Net: "P", X1: 50, Y1: 15, X2: 50, Y2: 40, Layer: 1, Width: 6}}
	a := rtPad{comp: "U1", pin: "1", x: 0, y: 20, layer: 1}
	b := rtPad{comp: "U2", pin: "1", x: 100, y: 0, layer: 1}
	opt := defaultRtOptions()
	opt.corner = "90"

	got, ok := routeWithAvoid("S", a, b, 10, opt, placed, nil, nil)
	if len(got) == 0 {
		t.Fatal("no segments returned")
	}
	if !ok {
		t.Error("the vertical-first L clears the P track — the hop must be feasible")
	}
	// Vertical-first ⇒ the first segment runs from (0,20) straight down to (0,0).
	first := got[0]
	if !(first.X1 == 0 && first.Y1 == 20 && first.X2 == 0 && first.Y2 == 0) {
		t.Errorf("expected vertical-first (0,20)->(0,0), got (%.0f,%.0f)->(%.0f,%.0f)", first.X1, first.Y1, first.X2, first.Y2)
	}

	// With avoidance OFF, it reverts to the naive horizontal-first L (corner at 100,20).
	opt.avoid = false
	naive, _ := routeWithAvoid("S", a, b, 10, opt, placed, nil, nil)
	if naive[0].X2 != 100 || naive[0].Y2 != 20 {
		t.Errorf("no-avoid should be horizontal-first (corner 100,20), got corner (%.0f,%.0f)", naive[0].X2, naive[0].Y2)
	}
}

// TestHopFeasible_OtherNetTrack is #119: an other-net same-layer track is a hard
// veto — a proper crossing and an under-clearance parallel run are both
// infeasible (not "cheaper"), a cross-layer or same-net track is not an obstacle.
func TestHopFeasible_OtherNetTrack(t *testing.T) {
	a := rtPad{x: 0, y: 0, layer: 1}
	b := rtPad{x: 100, y: 0, layer: 1}
	cand := []rtSeg{{Net: "S", X1: 0, Y1: 0, X2: 100, Y2: 0, Layer: 1, Width: 10}}

	cross := []rtSeg{{Net: "P", X1: 50, Y1: -20, X2: 50, Y2: 20, Layer: 1, Width: 6}}
	if hopFeasible(cand, "S", a, b, cross, nil, nil, nil, 6) {
		t.Error("a proper crossing with an other-net same-layer track must be infeasible")
	}
	near := []rtSeg{{Net: "P", X1: 0, Y1: 12, X2: 100, Y2: 12, Layer: 1, Width: 6}} // edge gap 12-5-3=4 < 6
	if hopFeasible(cand, "S", a, b, near, nil, nil, nil, 6) {
		t.Error("an under-clearance parallel other-net track must be infeasible")
	}
	farEnough := []rtSeg{{Net: "P", X1: 0, Y1: 20, X2: 100, Y2: 20, Layer: 1, Width: 6}} // edge gap 20-5-3=12 ≥ 6
	if !hopFeasible(cand, "S", a, b, farEnough, nil, nil, nil, 6) {
		t.Error("a legal-gap parallel track must stay feasible")
	}
	otherLayer := []rtSeg{{Net: "P", X1: 50, Y1: -20, X2: 50, Y2: 20, Layer: 2, Width: 6}}
	if !hopFeasible(cand, "S", a, b, otherLayer, nil, nil, nil, 6) {
		t.Error("a cross-layer track is not an obstacle")
	}
	sameNet := []rtSeg{{Net: "S", X1: 50, Y1: -20, X2: 50, Y2: 20, Layer: 1, Width: 6}}
	if !hopFeasible(cand, "S", a, b, sameNet, nil, nil, nil, 6) {
		t.Error("a same-net track is never an obstacle")
	}
}

// TestHopFeasible_Slot is #122: copper within max(clearance,8) of a board
// cutout's milled edge is a hard veto (native DRC: Slot Region to Track), no
// longer a +6 preference.
func TestHopFeasible_Slot(t *testing.T) {
	a := rtPad{x: 0, y: 0, layer: 1}
	b := rtPad{x: 100, y: 0, layer: 1}
	cand := []rtSeg{{Net: "S", X1: 0, Y1: 0, X2: 100, Y2: 0, Layer: 1, Width: 10}}

	tooClose := []pcbSlotP{{MinX: 40, MinY: 10, MaxX: 60, MaxY: 30}} // edge gap 10-5=5 < 8
	if hopFeasible(cand, "S", a, b, nil, nil, nil, tooClose, 6) {
		t.Error("a track inside a slot's keep-away band must be infeasible")
	}
	clear := []pcbSlotP{{MinX: 40, MinY: 15, MaxX: 60, MaxY: 30}} // edge gap 15-5=10 ≥ 8
	if !hopFeasible(cand, "S", a, b, nil, nil, nil, clear, 6) {
		t.Error("a track clear of the slot band must stay feasible")
	}
}

// TestPlanShortRoutes_NoCrossingsEmitted is #119's acceptance: whatever the
// planner returns must contain ZERO same-layer other-net crossings/clearance
// hits among its own output — judged by the check's own findClearanceViolations
// (the R2 shorts were router output the old cost model chose to draw). The
// corridor between the two S pads is fully occupied by a P track, so the planner
// must either detour on L2 or report the hop — never draw through P on L1.
func TestPlanShortRoutes_NoCrossingsEmitted(t *testing.T) {
	comps := []apComp{
		{designator: "U1", pads: []apPad{{num: "1", net: "S", x: 0, y: 0, layer: 1}, {num: "2", net: "P", x: 0, y: 100, layer: 1}}},
		{designator: "U2", pads: []apPad{{num: "1", net: "S", x: 200, y: 0, layer: 1}, {num: "2", net: "P", x: 200, y: 100, layer: 1}}},
	}
	opt := defaultRtOptions()
	// A pre-existing P wall crossing the whole S corridor on L1.
	opt.existing = []rtSeg{{Net: "P", X1: 100, Y1: -50, X2: 100, Y2: 150, Layer: 1, Width: 10}}

	segs, vias, diags := planShortRoutes(comps, map[string]bool{"P": true}, opt)

	tracks := make([]pcbTrack, 0, len(segs)+1)
	for i, s := range segs {
		tracks = append(tracks, pcbTrack{ID: fmt.Sprintf("s%d", i), Net: s.Net, X1: s.X1, Y1: s.Y1, X2: s.X2, Y2: s.Y2, Layer: s.Layer, Width: s.Width})
	}
	tracks = append(tracks, pcbTrack{ID: "wall", Net: "P", X1: 100, Y1: -50, X2: 100, Y2: 150, Layer: 1, Width: 10})
	pv := make([]pcbViaP, 0, len(vias))
	for i, v := range vias {
		pv = append(pv, pcbViaP{ID: fmt.Sprintf("v%d", i), Net: v.Net, X: v.X, Y: v.Y, Dia: opt.viaDia})
	}
	if viol := findClearanceViolations(tracks, nil, pv, nil, opt.clearance); len(viol) > 0 {
		t.Fatalf("planner output violates its own check: %+v", viol)
	}
	if len(segs) == 0 {
		// Acceptable only if the hop was honestly reported instead.
		found := false
		for _, d := range diags {
			if d.Net == "S" {
				found = true
			}
		}
		if !found {
			t.Fatal("planner drew nothing and reported nothing for net S")
		}
	}
}

// A hop should avoid running through another net's pad.
func TestHopCost_CountsOtherNetPad(t *testing.T) {
	a := rtPad{x: 0, y: 0, layer: 1}
	b := rtPad{x: 10, y: 10, layer: 1}
	cand := lShape90("S", a, b, 10, true) // (0,0)->(10,0)->(10,10)
	// A P-net pad sitting on the horizontal leg at (5,0), same layer as the track.
	obst := []obPad{{net: "P", x: 5, y: 0, layer: 1}}
	if c := hopCost(cand, "S", a, b, nil, obst, nil, 6); c == 0 {
		t.Error("expected non-zero cost for a track running over another net's pad")
	}
	// The SAME pad on net S (the hop's own net) is not an obstacle.
	if c := hopCost(cand, "S", a, b, nil, []obPad{{net: "S", x: 5, y: 0, layer: 1}}, nil, 6); c != 0 {
		t.Errorf("same-net pad must not add cost, got %d", c)
	}
	// A P-net pad on a DIFFERENT layer than the track adds no cost (layer-aware).
	if c := hopCost(cand, "S", a, b, nil, []obPad{{net: "P", x: 5, y: 0, layer: 2}}, nil, 6); c != 0 {
		t.Errorf("other-layer pad must not add cost, got %d", c)
	}
	// A P-net VIA on the horizontal leg adds cost on any layer.
	if c := hopCost(cand, "S", a, b, nil, nil, []obVia{{net: "P", x: 5, y: 0, r: 12}}, 6); c == 0 {
		t.Error("expected non-zero cost for a track running over another net's via")
	}
}
