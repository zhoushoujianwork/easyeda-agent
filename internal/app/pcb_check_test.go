package app

import "testing"

func countType(rep pcbCheckReport, typ string) int {
	n := 0
	for _, f := range rep.Findings {
		if f.Type == typ {
			n++
		}
	}
	return n
}

// A track from a pad center to a free point leaves that far end dangling.
func TestPcbCheck_DanglingEnd(t *testing.T) {
	pads := []pcbPadP{{Designator: "R1", Number: "1", Net: "N1", Layer: 1, X: 0, Y: 0}}
	tracks := []pcbTrack{{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10}}
	rep := analyzePcbCheck(pads, tracks, nil)
	if got := countType(rep, "dangling-end"); got != 1 {
		t.Fatalf("dangling-end = %d, want 1 (findings: %+v)", got, rep.Findings)
	}
}

// A pad→track→track→pad chain has every end anchored: no dangling, and the
// collinear pass-through vertex is 180° (not acute).
func TestPcbCheck_ChainNoDangling(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "R1", Number: "1", Net: "N1", Layer: 1, X: 0, Y: 0},
		{Designator: "R2", Number: "1", Net: "N1", Layer: 1, X: 200, Y: 0},
	}
	tracks := []pcbTrack{
		{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "N1", Layer: 1, X1: 100, Y1: 0, X2: 200, Y2: 0, Width: 10},
	}
	rep := analyzePcbCheck(pads, tracks, nil)
	if !rep.Passed {
		t.Fatalf("expected clean chain, got findings: %+v", rep.Findings)
	}
}

// Two same-net same-layer segments meeting at 60° is an acid-trap acute angle.
func TestPcbCheck_AcuteAngle(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "A", Number: "1", Net: "N1", Layer: 1, X: 100, Y: 0},
		{Designator: "B", Number: "1", Net: "N1", Layer: 1, X: 50, Y: 86.6},
	}
	tracks := []pcbTrack{
		{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 50, Y2: 86.6, Width: 10},
	}
	rep := analyzePcbCheck(pads, tracks, nil)
	if got := countType(rep, "acute-angle"); got != 1 {
		t.Fatalf("acute-angle = %d, want 1 (findings: %+v)", got, rep.Findings)
	}
}

// A clean 90° corner is NOT an acute angle.
func TestPcbCheck_RightAngleOK(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "A", Number: "1", Net: "N1", Layer: 1, X: 100, Y: 0},
		{Designator: "B", Number: "1", Net: "N1", Layer: 1, X: 0, Y: 100},
	}
	tracks := []pcbTrack{
		{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 0, Y2: 100, Width: 10},
	}
	rep := analyzePcbCheck(pads, tracks, nil)
	if got := countType(rep, "acute-angle"); got != 0 {
		t.Fatalf("acute-angle = %d, want 0 for a 90° corner", got)
	}
}

// Two vias stacked on the same spot are redundant. Use a global net so the
// single-layer-via rule (skipped for power/GND) doesn't also fire.
func TestPcbCheck_OverlappingVia(t *testing.T) {
	vias := []pcbViaP{
		{ID: "v1", Net: "GND", X: 0, Y: 0, Hole: 12, Dia: 24},
		{ID: "v2", Net: "GND", X: 1, Y: 0, Hole: 12, Dia: 24},
	}
	rep := analyzePcbCheck(nil, nil, vias)
	if got := countType(rep, "overlapping-via"); got != 1 {
		t.Fatalf("overlapping-via = %d, want 1", got)
	}
	if got := countType(rep, "single-layer-via"); got != 0 {
		t.Fatalf("single-layer-via = %d, want 0 (GND vias are skipped)", got)
	}
}

// A signal via touched by tracks on only one layer serves no purpose.
func TestPcbCheck_SingleLayerVia(t *testing.T) {
	pads := []pcbPadP{{Designator: "A", Number: "1", Net: "SIG1", Layer: 1, X: 100, Y: 0}}
	vias := []pcbViaP{{ID: "v1", Net: "SIG1", X: 0, Y: 0, Hole: 12, Dia: 24}}
	tracks := []pcbTrack{{ID: "t1", Net: "SIG1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10}}
	rep := analyzePcbCheck(pads, tracks, vias)
	if got := countType(rep, "single-layer-via"); got != 1 {
		t.Fatalf("single-layer-via = %d, want 1 (findings: %+v)", got, rep.Findings)
	}
}

// A via bridging two layers is a legitimate layer transition.
func TestPcbCheck_TwoLayerViaOK(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "A", Number: "1", Net: "SIG1", Layer: 1, X: 100, Y: 0},
		{Designator: "B", Number: "1", Net: "SIG1", Layer: 2, X: 0, Y: 100},
	}
	vias := []pcbViaP{{ID: "v1", Net: "SIG1", X: 0, Y: 0, Hole: 12, Dia: 24}}
	tracks := []pcbTrack{
		{ID: "t1", Net: "SIG1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "SIG1", Layer: 2, X1: 0, Y1: 0, X2: 0, Y2: 100, Width: 10},
	}
	rep := analyzePcbCheck(pads, tracks, vias)
	if got := countType(rep, "single-layer-via"); got != 0 {
		t.Fatalf("single-layer-via = %d, want 0 for a real layer transition", got)
	}
}

// A 2-pin part whose two pads have asymmetric entering track widths.
func TestPcbCheck_WidthMismatch(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "R1", Number: "1", Net: "NA", Layer: 1, X: 0, Y: 0},
		{Designator: "R1", Number: "2", Net: "NB", Layer: 1, X: 100, Y: 0},
		{Designator: "PX", Number: "1", Net: "NA", Layer: 1, X: -50, Y: 0},
		{Designator: "PY", Number: "1", Net: "NB", Layer: 1, X: 150, Y: 0},
	}
	tracks := []pcbTrack{
		{ID: "t1", Net: "NA", Layer: 1, X1: 0, Y1: 0, X2: -50, Y2: 0, Width: 10},
		{ID: "t2", Net: "NB", Layer: 1, X1: 100, Y1: 0, X2: 150, Y2: 0, Width: 30},
	}
	rep := analyzePcbCheck(pads, tracks, nil)
	if got := countType(rep, "width-mismatch"); got != 1 {
		t.Fatalf("width-mismatch = %d, want 1 (findings: %+v)", got, rep.Findings)
	}
}

// Two collinear same-net overlapping segments are redundant copper.
func TestPcbCheck_DuplicateSegment(t *testing.T) {
	tracks := []pcbTrack{
		{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "N1", Layer: 1, X1: 50, Y1: 0, X2: 150, Y2: 0, Width: 10},
	}
	rep := analyzePcbCheck(nil, tracks, nil)
	if got := countType(rep, "duplicate-segment"); got != 1 {
		t.Fatalf("duplicate-segment = %d, want 1 (findings: %+v)", got, rep.Findings)
	}
}

// A well-formed 2-pin routed net: every end anchored, 90° corner, equal widths.
func TestPcbCheck_CleanBoard(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "U1", Number: "1", Net: "NET1", Layer: 1, X: 0, Y: 0},
		{Designator: "U1", Number: "2", Net: "NET1", Layer: 1, X: 100, Y: 100},
	}
	tracks := []pcbTrack{
		{ID: "t1", Net: "NET1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "NET1", Layer: 1, X1: 100, Y1: 0, X2: 100, Y2: 100, Width: 10},
	}
	rep := analyzePcbCheck(pads, tracks, nil)
	if !rep.Passed || rep.Summary.Total != 0 {
		t.Fatalf("expected clean board, got %d findings: %+v", rep.Summary.Total, rep.Findings)
	}
}
