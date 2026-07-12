package app

import "testing"

// Issue #99: hand-solder iron-access — a component boxed in on all four sides
// below the access corridor must be flagged; one clear flank is enough.

func accessComp(d string, minX, minY, maxX, maxY float64) pcbLComp {
	return pcbLComp{Designator: d, BBox: &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}}
}

func TestAnalyzeSolderAccessBoxedIn(t *testing.T) {
	// U1 (100×100) surrounded on all four sides at a 20mil gap — no 60mil entry.
	comps := []pcbLComp{
		accessComp("U1", 0, 0, 100, 100),
		accessComp("R1", 120, 0, 220, 100),  // right, gap 20
		accessComp("R2", -220, 0, -20, 100), // left, gap 20
		accessComp("C1", 0, 120, 100, 220),  // top, gap 20
		accessComp("C2", 0, -220, 100, -20), // bottom, gap 20
	}
	blocked := analyzeSolderAccess(comps, 60)
	found := false
	for _, b := range blocked {
		if b.Designator == "U1" {
			found = true
			if b.BestGap != 20 {
				t.Fatalf("U1 best gap should be 20, got %v", b.BestGap)
			}
		}
	}
	if !found {
		t.Fatalf("U1 is boxed in on all sides and must be flagged, got %+v", blocked)
	}

	// Remove the top neighbor → U1 gains an open flank and passes.
	open := append([]pcbLComp{}, comps[:3]...)
	open = append(open, comps[4])
	for _, b := range analyzeSolderAccess(open, 60) {
		if b.Designator == "U1" {
			t.Fatal("U1 with an open top flank must pass the access check")
		}
	}

	// A decap tight against its IC but open on the other side passes: that is
	// the issue's "去耦可贴近,但至少保留一侧可操作" rule.
	decap := []pcbLComp{
		accessComp("U2", 0, 0, 100, 100),
		accessComp("C3", 104, 0, 144, 100), // 4mil off U2's right edge, right side open
	}
	for _, b := range analyzeSolderAccess(decap, 60) {
		if b.Designator == "C3" {
			t.Fatal("a decap with one open flank must pass")
		}
	}
}

func TestEvalLayoutGateAccessBlockedFails(t *testing.T) {
	opt := pcbLayoutGateOpts{gate: true, minScore: 60, maxCrossings: 8}
	rep := pcbLayoutReport{
		Score: 90, CrossingCount: 0, AccessMil: 60,
		AccessBlocked: []pcbLAccessFinding{{Designator: "U1", BestGap: 20}},
	}
	if v := evalLayoutGate(rep, opt); v.Pass {
		t.Fatal("an iron-access-blocked component must fail the gate")
	}
}
