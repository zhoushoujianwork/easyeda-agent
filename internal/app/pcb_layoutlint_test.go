package app

import "testing"

// bb(minX,minY,maxX,maxY) *layoutBBox is shared from cmd_sch_layout_test.go.

// Two signal nets whose shortest links cross at (5,5) → exactly one crossing.
// GND pads must NOT count as a signal net (poured, excluded).
func TestAnalyzePcbLayout_Crossing(t *testing.T) {
	comps := []pcbLComp{
		{Designator: "U1", BBox: bb(0, 0, 4, 4)},
		{Designator: "U2", BBox: bb(10, 10, 14, 14)},
	}
	pads := []pcbLPad{
		{Designator: "U1", Net: "A", X: 0, Y: 0},
		{Designator: "U2", Net: "A", X: 10, Y: 10},
		{Designator: "U1", Net: "B", X: 0, Y: 10},
		{Designator: "U2", Net: "B", X: 10, Y: 0},
		{Designator: "U1", Net: "GND", X: 1, Y: 1}, // poured net — excluded
		{Designator: "U2", Net: "GND", X: 9, Y: 9},
	}
	rep := analyzePcbLayout(comps, pads, nil, 2)
	if rep.SignalNets != 2 {
		t.Errorf("signalNets=%d, want 2 (GND excluded)", rep.SignalNets)
	}
	if rep.CrossingCount != 1 {
		t.Errorf("crossingCount=%d, want 1 (A×B at 5,5)", rep.CrossingCount)
	}
	if len(rep.Overlaps) != 0 || !rep.OK {
		t.Errorf("expected no overlaps / OK, got overlaps=%d ok=%v", len(rep.Overlaps), rep.OK)
	}
	if rep.Score != 96 { // 100 - 4*1 crossing
		t.Errorf("score=%d, want 96", rep.Score)
	}
}

// Overlapping footprints ⇒ not OK, verdict "overlap", score 0.
func TestAnalyzePcbLayout_Overlap(t *testing.T) {
	comps := []pcbLComp{
		{Designator: "R1", BBox: bb(0, 0, 10, 10)},
		{Designator: "R2", BBox: bb(5, 5, 15, 15)},
	}
	rep := analyzePcbLayout(comps, nil, nil, 6)
	if rep.OK || rep.Verdict != "overlap" || rep.Score != 0 {
		t.Errorf("overlap: ok=%v verdict=%q score=%d, want false/overlap/0", rep.OK, rep.Verdict, rep.Score)
	}
	if len(rep.Overlaps) != 1 {
		t.Errorf("overlaps=%d, want 1", len(rep.Overlaps))
	}
}

// A component reaching outside the outline is an ERROR; a clean parallel layout
// with no crossings scores 100 / "easy".
func TestAnalyzePcbLayout_OutlineAndClean(t *testing.T) {
	outline := bb(0, 0, 100, 100)
	off := []pcbLComp{{Designator: "J1", BBox: bb(90, 90, 110, 110)}} // pokes past maxX/maxY
	rep := analyzePcbLayout(off, nil, outline, 6)
	if rep.OK || len(rep.OutsideOutline) != 1 {
		t.Errorf("off-board: ok=%v outside=%d, want false/1", rep.OK, len(rep.OutsideOutline))
	}

	clean := []pcbLComp{
		{Designator: "U1", BBox: bb(0, 0, 4, 4)},
		{Designator: "U2", BBox: bb(20, 0, 24, 4)},
	}
	pads := []pcbLPad{
		{Designator: "U1", Net: "A", X: 0, Y: 0}, {Designator: "U2", Net: "A", X: 20, Y: 0},
		{Designator: "U1", Net: "B", X: 0, Y: 2}, {Designator: "U2", Net: "B", X: 20, Y: 2},
	}
	rep2 := analyzePcbLayout(clean, pads, bb(-10, -10, 40, 40), 2)
	if !rep2.OK || rep2.CrossingCount != 0 || rep2.Score != 100 || rep2.Verdict != "easy" {
		t.Errorf("clean: ok=%v cross=%d score=%d verdict=%q, want true/0/100/easy", rep2.OK, rep2.CrossingCount, rep2.Score, rep2.Verdict)
	}
}

// A connector whose BODY protrudes past the outline but whose PADS are all inside
// is an intentional edge-mount (Type-C mating face overhanging), NOT off-board. A
// pad actually landing outside still trips the check.
func TestAnalyzePcbLayout_ProtrudingConnector(t *testing.T) {
	outline := bb(0, 0, 100, 100)
	// J1 body reaches x=-20 (past the left edge) but both pads sit at x>=5 (inside).
	protrude := []pcbLComp{{Designator: "J1", BBox: bb(-20, 40, 30, 60)}}
	padsIn := []pcbLPad{
		{Designator: "J1", Net: "USB_DP", X: 5, Y: 45},
		{Designator: "J1", Net: "USB_DM", X: 5, Y: 55},
	}
	rep := analyzePcbLayout(protrude, padsIn, outline, 6)
	if !rep.OK || len(rep.OutsideOutline) != 0 {
		t.Errorf("protruding connector (pads inside): ok=%v outside=%d, want true/0", rep.OK, len(rep.OutsideOutline))
	}

	// Same body, but now a pad actually falls off the board (x=-5) → real error.
	padsOff := []pcbLPad{
		{Designator: "J1", Net: "USB_DP", X: -5, Y: 45},
		{Designator: "J1", Net: "USB_DM", X: 5, Y: 55},
	}
	rep2 := analyzePcbLayout(protrude, padsOff, outline, 6)
	if rep2.OK || len(rep2.OutsideOutline) != 1 {
		t.Errorf("pad off-board: ok=%v outside=%d, want false/1", rep2.OK, len(rep2.OutsideOutline))
	}
}
