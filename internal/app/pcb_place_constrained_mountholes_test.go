package app

import (
	"math"
	"strings"
	"testing"
)

// ── #104 regression: cpHolesFromFills ────────────────────────────────────────

// pcb.fill.list returns NO polygon points — geometry only arrives as the
// rendered bbox (includeBBox). The old parser read a "points" field and always
// saw 0 holes (#104). This pins the bbox path with the real ceshi shape: a
// Ø126mil M3 slot plus lineWidth fringe.
func TestCpHolesFromFillsBBox(t *testing.T) {
	mkFill := func(layer float64, x0, y0, x1, y1 float64) map[string]any {
		return map[string]any{
			"primitiveId": "f", "layer": layer,
			"bbox": map[string]any{"minX": x0, "minY": y0, "maxX": x1, "maxY": y1},
		}
	}
	fills := []any{
		mkFill(12, -23.5, -33.5, 103.5, 93.5),        // ceshi bl M3 hole
		mkFill(12, 1566.5, -33.5, 1693.5, 93.5),      // ceshi br M3 hole
		mkFill(1, 0, 0, 500, 500),                    // TOP-layer copper fill — not a hole
		map[string]any{"layer": float64(12)},         // layer-12 fill without bbox/points → skipped
		map[string]any{"layer": float64(12), "bbox": nil}, // null bbox (getPrimitivesBBox failed)
	}
	holes := cpHolesFromFills(fills)
	if len(holes) != 2 {
		t.Fatalf("holes = %d, want 2", len(holes))
	}
	if math.Abs(holes[0].x-40) > 0.01 || math.Abs(holes[0].y-30) > 0.01 {
		t.Errorf("hole[0] center = (%g,%g), want (40,30)", holes[0].x, holes[0].y)
	}
	// radius = bbox half-extent (63.5) + 60mil washer margin
	if math.Abs(holes[0].r-123.5) > 0.01 {
		t.Errorf("hole[0] r = %g, want 123.5", holes[0].r)
	}
}

// Raw points (a caller feeding primitive data directly) still parse when no
// bbox is present.
func TestCpHolesFromFillsPointsFallback(t *testing.T) {
	fills := []any{map[string]any{
		"layer": float64(12),
		"points": []any{
			[]any{100.0, 100.0}, []any{200.0, 100.0},
			[]any{200.0, 200.0}, []any{100.0, 200.0},
		},
	}}
	holes := cpHolesFromFills(fills)
	if len(holes) != 1 {
		t.Fatalf("holes = %d, want 1", len(holes))
	}
	if holes[0].x != 150 || holes[0].y != 150 || holes[0].r != 110 {
		t.Errorf("hole = %+v, want center (150,150) r 110", holes[0])
	}
}

// ── #102: planMountHoles ─────────────────────────────────────────────────────

func TestPlanMountHolesCorners(t *testing.T) {
	board := cpRect{0, 0, 2000, 1500}
	plan, err := planMountHoles(board, []string{"tl", "tr", "bl", "br"}, 126, 197, 0, nil, nil, nil, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 4 {
		t.Fatalf("plan = %d corners, want 4", len(plan))
	}
	want := map[string][2]float64{ // y-up: top = maxY
		"tl": {197, 1303}, "tr": {1803, 1303}, "bl": {197, 197}, "br": {1803, 197},
	}
	for _, p := range plan {
		if p.Status != "plan" {
			t.Errorf("%s status = %q, want plan (%s)", p.Corner, p.Status, p.Reason)
		}
		w := want[p.Corner]
		if p.X != w[0] || p.Y != w[1] {
			t.Errorf("%s center = (%g,%g), want (%g,%g)", p.Corner, p.X, p.Y, w[0], w[1])
		}
	}
}

func TestPlanMountHolesConflictSkips(t *testing.T) {
	board := cpRect{0, 0, 2000, 1500}
	// C1 sits inside the tl keep-out circle (center (197,1303), R 118).
	comps := []cpComp{mkCP("C1", "cap.100nf", 1, 250, 1280, 60, 30, 2)}
	plan, err := planMountHoles(board, []string{"tl", "br"}, 126, 197, 0, comps, nil, nil, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Status != "skip-conflict" || !strings.Contains(plan[0].Reason, "C1") {
		t.Errorf("tl = %q (%s), want skip-conflict naming C1", plan[0].Status, plan[0].Reason)
	}
	if plan[1].Status != "plan" {
		t.Errorf("br = %q, want plan", plan[1].Status)
	}
}

// --clearance overrides the washer keep-out: a corner that conflicts under the
// auto radius (118) passes with a knowingly-smaller fastener head.
func TestPlanMountHolesClearanceOverride(t *testing.T) {
	board := cpRect{0, 0, 2000, 1500}
	// R3-style part ~110mil from the bl center (197,197): conflicts at R118, clears at R100.
	comps := []cpComp{mkCP("R3", "res.10k", 1, 197, 337, 80, 46, 2)}
	plan, err := planMountHoles(board, []string{"bl"}, 126, 197, 0, comps, nil, nil, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Status != "skip-conflict" {
		t.Fatalf("auto clearance: bl = %q, want skip-conflict", plan[0].Status)
	}
	plan, err = planMountHoles(board, []string{"bl"}, 126, 197, 100, comps, nil, nil, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Status != "plan" {
		t.Errorf("clearance 100: bl = %q (%s), want plan", plan[0].Status, plan[0].Reason)
	}
	// clearance below the hole radius is nonsense → error
	if _, err := planMountHoles(board, []string{"bl"}, 126, 197, 50, nil, nil, nil, nil, 8); err == nil {
		t.Error("clearance < dia/2 should error")
	}
}

func TestPlanMountHolesExistingIdempotent(t *testing.T) {
	board := cpRect{0, 0, 2000, 1500}
	existing := []cpHole{{x: 200, y: 200, r: 123.5}} // ≈ the bl corner (197,197)
	plan, err := planMountHoles(board, []string{"bl", "tr"}, 126, 197, 0, nil, existing, nil, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Status != "exists" {
		t.Errorf("bl = %q, want exists", plan[0].Status)
	}
	if plan[1].Status != "plan" {
		t.Errorf("tr = %q, want plan", plan[1].Status)
	}
}

func TestPlanMountHolesValidation(t *testing.T) {
	board := cpRect{0, 0, 2000, 1500}
	if _, err := planMountHoles(board, []string{"tl"}, 126, 50, 0, nil, nil, nil, nil, 8); err == nil {
		t.Error("inset < dia/2 should error (hole would cut the board edge)")
	}
	if _, err := planMountHoles(board, []string{"middle"}, 126, 197, 0, nil, nil, nil, nil, 8); err == nil {
		t.Error("unknown corner should error")
	}
	if _, err := planMountHoles(cpRect{0, 0, 400, 400}, []string{"tl"}, 126, 197, 0, nil, nil, nil, nil, 8); err == nil {
		t.Error("board smaller than 2*inset+dia should error")
	}
	// duplicate corners collapse to one
	plan, err := planMountHoles(board, []string{"tl", "TL", " tl "}, 126, 197, 0, nil, nil, nil, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 {
		t.Errorf("deduped plan = %d, want 1", len(plan))
	}
}

func TestMhCirclePolygon(t *testing.T) {
	pts := mhCirclePolygon(1000, -500, 126, 16)
	if len(pts) != 16 {
		t.Fatalf("points = %d, want 16", len(pts))
	}
	for _, p := range pts {
		r := math.Hypot(p[0]-1000, p[1]+500)
		if math.Abs(r-63) > 0.02 {
			t.Errorf("vertex (%g,%g) radius %g, want 63", p[0], p[1], r)
		}
	}
}

func TestMhClearanceRadius(t *testing.T) {
	if r := mhClearanceRadius(126); r != 118 { // M3: washer head dominates (63+40=103 < 118)
		t.Errorf("M3 clearance = %g, want 118 (washer)", r)
	}
	if r := mhClearanceRadius(250); r != 165 { // big hole: hole+margin dominates
		t.Errorf("Ø250 clearance = %g, want 165 (hole+40)", r)
	}
}

// TestPlanMountHolesCopperConflict is #122's second half: a hole must not be
// milled onto existing routed copper — a track/via inside the copper-to-cutout
// band of the hole edge is a skip-conflict, never force-placed.
func TestPlanMountHolesCopperConflict(t *testing.T) {
	board := cpRect{0, 0, 2000, 1500}
	// A track running right through the bl hole center (197,197).
	tracks := []pcbTrack{{ID: "t1", Net: "SPICS0", X1: 0, Y1: 197, X2: 500, Y2: 197, Layer: 2, Width: 10}}
	plan, err := planMountHoles(board, []string{"bl", "br"}, 126, 197, 0, nil, nil, tracks, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Status != "skip-conflict" || !strings.Contains(plan[0].Reason, "SPICS0") {
		t.Errorf("bl = %q (%s), want skip-conflict naming the SPICS0 track", plan[0].Status, plan[0].Reason)
	}
	if plan[1].Status != "plan" {
		t.Errorf("br = %q (%s), want plan (no copper there)", plan[1].Status, plan[1].Reason)
	}

	// A via hugging the br hole edge: center distance 70 − holeR 63 − viaR 12 < 8.
	vias := []pcbViaP{{ID: "v1", Net: "GND", X: 1803 + 70, Y: 197, Dia: 24}}
	plan, err = planMountHoles(board, []string{"br"}, 126, 197, 0, nil, nil, nil, vias, 8)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Status != "skip-conflict" || !strings.Contains(plan[0].Reason, "GND") {
		t.Errorf("br = %q (%s), want skip-conflict naming the GND via", plan[0].Status, plan[0].Reason)
	}

	// Copper safely outside the band stays plan.
	farTracks := []pcbTrack{{ID: "t2", Net: "SPICS0", X1: 0, Y1: 400, X2: 500, Y2: 400, Layer: 2, Width: 10}}
	plan, err = planMountHoles(board, []string{"bl"}, 126, 197, 0, nil, nil, farTracks, nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Status != "plan" {
		t.Errorf("bl = %q (%s), want plan (track 203mil away)", plan[0].Status, plan[0].Reason)
	}
}
