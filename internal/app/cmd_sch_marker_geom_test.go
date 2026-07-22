package app

import (
	"testing"
)

// findingByType returns the findings of a given type, in order.
func findingsOfType(fs []checkFinding, typ string) []checkFinding {
	var out []checkFinding
	for _, f := range fs {
		if f.Type == typ {
			out = append(out, f)
		}
	}
	return out
}

// ── duplicate-net-marker (issue #146) ───────────────────────────────────────

func TestDuplicateNetMarker_CoincidentAndFloatDrift(t *testing.T) {
	comps := []layoutComp{
		{ID: "g1", ComponentType: "netflag", Net: "GND", X: 1325, Y: 275},
		{ID: "g2", ComponentType: "netflag", Net: "GND", X: 1325, Y: 275},                 // exact duplicate
		{ID: "g3", ComponentType: "netflag", Net: "GND", X: 1324.9999999, Y: 275.0000001}, // float drift → same bucket
		{ID: "p1", ComponentType: "netport", Net: "MOTOR_G", X: 770, Y: 255},
		{ID: "p2", ComponentType: "netport", Net: "MOTOR_G", X: 770, Y: 255}, // duplicate
		{ID: "solo", ComponentType: "netflag", Net: "VCC", X: 500, Y: 500},   // no duplicate
		// NEGATIVE cases that MUST NOT merge with the GND stack at (1325,275):
		{ID: "diffnet", ComponentType: "netflag", Net: "5V", X: 1325, Y: 275},   // different net
		{ID: "difftype", ComponentType: "netport", Net: "GND", X: 1325, Y: 275}, // different marker kind
		{ID: "R1", ComponentType: "part", Designator: "R1", X: 1325, Y: 275},    // not a marker
	}
	got := duplicateNetMarkerFindings(comps)
	if len(got) != 2 {
		t.Fatalf("want 2 duplicate groups (GND, MOTOR_G), got %d: %+v", len(got), got)
	}
	// The GND group: keep the lexically-smallest id, delete the rest.
	var gnd *checkFinding
	for i := range got {
		if got[i].MarkerNet == "GND" {
			gnd = &got[i]
		}
	}
	if gnd == nil {
		t.Fatalf("no GND duplicate finding in %+v", got)
	}
	if gnd.Type != "duplicate-net-marker" || gnd.Level != "warn" {
		t.Errorf("GND finding type/level = %s/%s", gnd.Type, gnd.Level)
	}
	if len(gnd.PrimitiveIds) != 3 {
		t.Errorf("GND group should carry all 3 coincident ids (incl. float-drift g3), got %v", gnd.PrimitiveIds)
	}
	if gnd.SuggestKeepId != "g1" {
		t.Errorf("keep id should be lexically-smallest g1, got %q", gnd.SuggestKeepId)
	}
	if len(gnd.SuggestDeleteIds) != 2 {
		t.Errorf("should suggest deleting g2,g3, got %v", gnd.SuggestDeleteIds)
	}
}

// ── titleblock-overlap (issue #147) ─────────────────────────────────────────

func TestTitleblockOverlap_RealMotorG(t *testing.T) {
	keepout := bb(912.6, 0, 1170, 115.5) // issue #147 A4 title-block keep-out
	comps := []layoutComp{
		// MOTOR_G netport bbox from the issue — fully inside the keep-out.
		{ID: "motorG", ComponentType: "netport", Net: "MOTOR_G", BBox: bb(1019.5, 79.5, 1030.5, 110.5)},
		{ID: "safe", ComponentType: "netport", Net: "SAFE", BBox: bb(100, 200, 120, 220)}, // clear
		{ID: "sheet", ComponentType: "sheet", BBox: bb(0, 0, 1170, 825)},                  // spans page → must NOT report
	}
	got := titleblockOverlapFindings(comps, keepout, 0.5)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 titleblock-overlap (MOTOR_G), got %d: %+v", len(got), got)
	}
	if got[0].PrimitiveId != "motorG" {
		t.Errorf("expected MOTOR_G, got %q", got[0].PrimitiveId)
	}
	// overlap = 11 (x) × 31 (y).
	if got[0].OverlapX != 11 || got[0].OverlapY != 31 {
		t.Errorf("overlap = %.2f×%.2f, want 11×31", got[0].OverlapX, got[0].OverlapY)
	}
}

func TestTitleblockOverlap_NoKeepoutIsNoop(t *testing.T) {
	comps := []layoutComp{{ID: "x", ComponentType: "netport", BBox: bb(1000, 50, 1010, 60)}}
	if got := titleblockOverlapFindings(comps, nil, 0.5); len(got) != 0 {
		t.Errorf("nil keep-out must yield no findings, got %+v", got)
	}
}

// ── marker-overlap (issue #148) ─────────────────────────────────────────────

// realH2H4Comps uses the exact H2/H4 part + netport bboxes from issue #148.
func realH2H4Comps() []layoutComp {
	return []layoutComp{
		{ID: "H2", Designator: "H2", ComponentType: "part", BBox: bb(184.5, 579.5, 210.5, 660.5)},
		{ID: "H4", Designator: "H4", ComponentType: "part", BBox: bb(104.5, 599.5, 125.5, 640.5)},
		{ID: "mMICSD", ComponentType: "netport", Net: "MICSD", BBox: bb(194.5, 624.5, 225.5, 635.5)},
		{ID: "mBCLK", ComponentType: "netport", Net: "BCLK", BBox: bb(114.5, 634.5, 145.5, 645.5)},
		{ID: "mDIN", ComponentType: "netport", Net: "DIN", BBox: bb(114.5, 624.5, 145.5, 635.5)},
		{ID: "mSD", ComponentType: "netport", Net: "SD", BBox: bb(114.5, 604.5, 145.5, 615.5)},
	}
}

func TestMarkerOverlap_RealH2H4(t *testing.T) {
	got := markerOverlapFindings(realH2H4Comps(), 0.5)
	// Expect the 4 marker×part overlaps + BCLK×DIN (31×1) marker×marker = 5.
	if len(got) != 5 {
		t.Fatalf("want 5 marker-overlaps, got %d: %+v", len(got), summarizeOverlaps(got))
	}
	// Spot-check the issue's exact overlap extents.
	want := map[[2]string][2]float64{
		{"H2", "mMICSD"}: {16, 11},
		{"H4", "mBCLK"}:  {11, 6},
		{"H4", "mDIN"}:   {11, 11},
		{"H4", "mSD"}:    {11, 11},
		{"mBCLK", "mDIN"}: {31, 1}, // the boundary case: min axis 1 > eps 0.5 → reported
	}
	for _, f := range got {
		key := [2]string{f.PrimitiveId, f.Other.PrimitiveId}
		w, ok := want[key]
		if !ok {
			t.Errorf("unexpected overlap pair %v", key)
			continue
		}
		if f.OverlapX != w[0] || f.OverlapY != w[1] {
			t.Errorf("pair %v overlap = %.2f×%.2f, want %.2f×%.2f", key, f.OverlapX, f.OverlapY, w[0], w[1])
		}
		if len(f.PrimitiveIds) != 2 {
			t.Errorf("marker-overlap must carry BOTH primitive ids, got %v", f.PrimitiveIds)
		}
	}
}

func TestMarkerOverlap_ExcludesPartPairAndSubEps(t *testing.T) {
	comps := []layoutComp{
		// Two overlapping PARTS — layout-lint's job, NOT marker-overlap.
		{ID: "P1", Designator: "P1", ComponentType: "part", BBox: bb(0, 0, 20, 20)},
		{ID: "P2", Designator: "P2", ComponentType: "part", BBox: bb(10, 10, 30, 30)},
		// A marker grazing part P3 by only 0.3 on the min axis (x) — below eps 0.5.
		// (Placed at y 40..60 so it does NOT touch P1/P2.)
		{ID: "mA", ComponentType: "netport", Net: "A", BBox: bb(29.7, 40, 50, 60)},
		{ID: "P3", Designator: "P3", ComponentType: "part", BBox: bb(0, 40, 30, 60)},
	}
	got := markerOverlapFindings(comps, 0.5)
	if len(got) != 0 {
		t.Errorf("part×part excluded and sub-eps graze ignored → want 0, got %+v", summarizeOverlaps(got))
	}
}

func summarizeOverlaps(fs []checkFinding) []string {
	var s []string
	for _, f := range fs {
		other := ""
		if f.Other != nil {
			other = f.Other.PrimitiveId
		}
		s = append(s, f.PrimitiveId+"×"+other)
	}
	return s
}

// ── partial-run bookkeeping (issue #146) ────────────────────────────────────

func TestSplitConnResults_Partial(t *testing.T) {
	conns := []acConnResult{
		{Pin: "U1:1", Net: "GND"},                       // ok
		{Pin: "U1:2", Net: "GND", Error: "connect drop"}, // failed
		{Pin: "U1:3", Net: "VCC"},                       // ok
	}
	ok, failed, partial := splitConnResults(conns, false)
	if len(ok) != 2 || len(failed) != 1 {
		t.Fatalf("want 2 ok / 1 failed, got %v / %v", ok, failed)
	}
	if failed[0] != "U1:2" {
		t.Errorf("failed pin should be U1:2, got %v", failed)
	}
	if !partial {
		t.Error("a real batch with both successes and failures is partial")
	}
	// Dry-run is never 'partial' (nothing mutated), even with a resolve error.
	if _, _, p := splitConnResults(conns, true); p {
		t.Error("dry-run must not be reported as partial")
	}
	// All-success is not partial.
	if _, _, p := splitConnResults([]acConnResult{{Pin: "A:1"}}, false); p {
		t.Error("all-success run must not be partial")
	}
}
