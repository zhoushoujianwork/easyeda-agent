package app

import (
	"strings"
	"testing"
)

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
	rep := analyzePcbCheck(pads, tracks, nil, 0)
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
	rep := analyzePcbCheck(pads, tracks, nil, 0)
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
	rep := analyzePcbCheck(pads, tracks, nil, 0)
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
	rep := analyzePcbCheck(pads, tracks, nil, 0)
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
	rep := analyzePcbCheck(nil, nil, vias, 0)
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
	rep := analyzePcbCheck(pads, tracks, vias, 0)
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
	rep := analyzePcbCheck(pads, tracks, vias, 0)
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
	rep := analyzePcbCheck(pads, tracks, nil, 0)
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
	rep := analyzePcbCheck(nil, tracks, nil, 0)
	if got := countType(rep, "duplicate-segment"); got != 1 {
		t.Fatalf("duplicate-segment = %d, want 1 (findings: %+v)", got, rep.Findings)
	}
}

// Two different-net parallel traces 5 mil apart (need 3×10=30) over 100 mil.
func TestPcbCheck_ParallelCoupling(t *testing.T) {
	tracks := []pcbTrack{
		{ID: "t1", Net: "A", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "B", Layer: 1, X1: 0, Y1: 5, X2: 100, Y2: 5, Width: 10},
	}
	rep := analyzePcbCheck(nil, tracks, nil, 0)
	if got := countType(rep, "parallel-coupling"); got != 1 {
		t.Fatalf("parallel-coupling = %d, want 1 (findings: %+v)", got, rep.Findings)
	}
}

// Parallel traces far enough apart (50 > 30), same-net pairs, and crossing (not
// parallel) pairs must NOT be flagged as coupling.
func TestPcbCheck_CouplingSpacedOK(t *testing.T) {
	far := []pcbTrack{
		{ID: "t1", Net: "A", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "B", Layer: 1, X1: 0, Y1: 50, X2: 100, Y2: 50, Width: 10},
	}
	if got := countType(analyzePcbCheck(nil, far, nil, 0), "parallel-coupling"); got != 0 {
		t.Fatalf("far-apart coupling = %d, want 0", got)
	}
	sameNet := []pcbTrack{
		{ID: "t1", Net: "A", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "A", Layer: 1, X1: 0, Y1: 5, X2: 100, Y2: 5, Width: 10},
	}
	if got := countType(analyzePcbCheck(nil, sameNet, nil, 0), "parallel-coupling"); got != 0 {
		t.Fatalf("same-net coupling = %d, want 0 (intentional)", got)
	}
	crossing := []pcbTrack{
		{ID: "t1", Net: "A", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "B", Layer: 1, X1: 50, Y1: -50, X2: 50, Y2: 50, Width: 10},
	}
	if got := countType(analyzePcbCheck(nil, crossing, nil, 0), "parallel-coupling"); got != 0 {
		t.Fatalf("crossing coupling = %d, want 0 (not parallel)", got)
	}
}

// A single free-angle diagonal trace (63°) is non-orthogonal; a 45° trace is not.
func TestPcbCheck_NonOrthogonal(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "A", Number: "1", Net: "N1", Layer: 1, X: 0, Y: 0},
		{Designator: "B", Number: "1", Net: "N1", Layer: 1, X: 50, Y: 98},
	}
	diag := []pcbTrack{{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 50, Y2: 98, Width: 10}}
	if got := countType(analyzePcbCheck(pads, diag, nil, 0), "non-orthogonal"); got != 1 {
		t.Fatalf("non-orthogonal(63°) = %d, want 1", got)
	}
	ok45 := []pcbTrack{{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 100, Width: 10}}
	if got := countType(analyzePcbCheck(nil, ok45, nil, 0), "non-orthogonal"); got != 0 {
		t.Fatalf("non-orthogonal(45°) = %d, want 0 (45° is on-grid)", got)
	}
}

// A track whose body runs over a foreign-net pad center (not an endpoint) on the
// same layer is a short (ERROR); over a same-net pad it's a WARN. A pad on the
// other layer is ignored.
func TestPcbCheck_TrackOverPad(t *testing.T) {
	// t1 spans x[0..200] on layer 1; pad M at (100,0) net OTHER sits mid-body.
	pads := []pcbPadP{
		{Designator: "U1", Number: "1", Net: "N1", Layer: 1, X: 0, Y: 0},
		{Designator: "U1", Number: "2", Net: "N1", Layer: 1, X: 200, Y: 0},
		{Designator: "M1", Number: "1", Net: "OTHER", Layer: 1, X: 100, Y: 0},
	}
	tracks := []pcbTrack{{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 200, Y2: 0, Width: 10}}
	rep := analyzePcbCheck(pads, tracks, nil, 0)
	if got := countType(rep, "track-over-pad"); got != 1 {
		t.Fatalf("track-over-pad = %d, want 1 (findings: %+v)", got, rep.Findings)
	}
	if rep.Summary.Errors != 1 {
		t.Fatalf("errors = %d, want 1 (cross-net short)", rep.Summary.Errors)
	}
	// Same-net mid-body pad → WARN, not ERROR.
	pads[2].Net = "N1"
	rep = analyzePcbCheck(pads, tracks, nil, 0)
	if got := countType(rep, "track-over-pad"); got != 1 || rep.Summary.Errors != 0 {
		t.Fatalf("same-net over-pad: type=%d errors=%d, want 1/0", got, rep.Summary.Errors)
	}
	// Foreign pad on the OTHER layer → ignored.
	pads[2].Net, pads[2].Layer = "OTHER", 2
	if got := countType(analyzePcbCheck(pads, tracks, nil, 0), "track-over-pad"); got != 0 {
		t.Fatalf("other-layer over-pad = %d, want 0", got)
	}
}

// Silkscreen orientation: a top-side designator on the bottom silk (side
// mismatch), a top-silk label that is mirrored (reads backwards), and a correct
// bottom-side part (bottom silk + mirrored) that must NOT trip.
func TestPcbCheck_SilkscreenFlipped(t *testing.T) {
	// R1 on TOP but its designator got flipped onto the bottom silk → side mismatch.
	sideMismatch := []pcbSilkText{
		{ID: "a1", Kind: "attribute", Text: "R1", Layer: 4, Mirror: true, CompID: "c1", CompLayer: 1},
	}
	if got := countType(analyzePcbCheckFull(nil, nil, nil, nil,sideMismatch, 0), "silkscreen-flipped"); got != 1 {
		t.Fatalf("side-mismatch = %d, want 1", got)
	}
	// Free label on TOP silk but mirrored → reads backwards.
	backwards := []pcbSilkText{
		{ID: "s1", Kind: "string", Text: "REV A", Layer: 3, Mirror: true},
	}
	if got := countType(analyzePcbCheckFull(nil, nil, nil, nil,backwards, 0), "silkscreen-flipped"); got != 1 {
		t.Fatalf("mirrored-top = %d, want 1", got)
	}
	// Correct states: top part / top silk / un-mirrored, AND a bottom part / bottom
	// silk / mirrored. Neither is flipped.
	ok := []pcbSilkText{
		{ID: "a1", Kind: "attribute", Text: "U1", Layer: 3, Mirror: false, CompID: "c1", CompLayer: 1},
		{ID: "a2", Kind: "attribute", Text: "U2", Layer: 4, Mirror: true, CompID: "c2", CompLayer: 2},
		{ID: "s1", Kind: "string", Text: "LOGO", Layer: 3, Mirror: false},
	}
	rep := analyzePcbCheckFull(nil, nil, nil, nil,ok, 0)
	if got := countType(rep, "silkscreen-flipped"); got != 0 {
		t.Fatalf("correct silk = %d, want 0 (findings: %+v)", got, rep.Findings)
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
	rep := analyzePcbCheck(pads, tracks, nil, 0)
	if !rep.Passed || rep.Summary.Total != 0 {
		t.Fatalf("expected clean board, got %d findings: %+v", rep.Summary.Total, rep.Findings)
	}
}

// ── audit-fix regressions (adversarial workflow wf_9afc4dbe-b08) ────────────

// FIX #1: a layer-1 track end whose XY is only crossed by a DIFFERENT-layer track
// (no via) is a real dangling stub — cross-layer copper is not a connection.
func TestPcbCheck_Fix1_DanglingCrossLayer(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "P1", Number: "1", Net: "N1", Layer: 1, X: 0, Y: 0},
		{Designator: "P2", Number: "1", Net: "N1", Layer: 2, X: 100, Y: -50},
		{Designator: "P3", Number: "1", Net: "N1", Layer: 2, X: 100, Y: 50},
	}
	tracks := []pcbTrack{
		{ID: "A", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "B", Net: "N1", Layer: 2, X1: 100, Y1: -50, X2: 100, Y2: 50, Width: 10},
	}
	if got := countType(analyzePcbCheck(pads, tracks, nil, 0), "dangling-end"); got != 1 {
		t.Fatalf("cross-layer stub dangling-end = %d, want 1", got)
	}
	vias := []pcbViaP{{ID: "v", Net: "N1", X: 100, Y: 0, Hole: 12, Dia: 24}}
	if got := countType(analyzePcbCheck(pads, tracks, vias, 0), "dangling-end"); got != 0 {
		t.Fatalf("with via, dangling-end = %d, want 0", got)
	}
}

// FIX #4: two collinear same-direction segments (0° "bend") are overlap, not an
// acid-trap acute corner — duplicate-segment covers them, acute must NOT fire.
func TestPcbCheck_Fix4_NoZeroDegreeAcute(t *testing.T) {
	tracks := []pcbTrack{
		{ID: "t1", Net: "N", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "N", Layer: 1, X1: 0, Y1: 0, X2: 50, Y2: 0, Width: 10},
	}
	rep := analyzePcbCheck(nil, tracks, nil, 0)
	if got := countType(rep, "acute-angle"); got != 0 {
		t.Fatalf("0° collinear acute-angle = %d, want 0 (findings: %+v)", got, rep.Findings)
	}
	if got := countType(rep, "duplicate-segment"); got != 1 {
		t.Fatalf("0° collinear duplicate-segment = %d, want 1", got)
	}
}

// FIX #5: single-layer-via must count only the via's OWN net; a foreign net's
// track crossing the via XY on another layer doesn't give it a layer transition.
func TestPcbCheck_Fix5_SingleLayerViaNetAware(t *testing.T) {
	vias := []pcbViaP{{ID: "v1", Net: "SIG1", X: 0, Y: 0, Hole: 12, Dia: 24}}
	tracks := []pcbTrack{
		{ID: "t1", Net: "SIG1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10},
		{ID: "t2", Net: "SIG2", Layer: 2, X1: 0, Y1: 0, X2: 0, Y2: 100, Width: 10},
	}
	if got := countType(analyzePcbCheck(nil, tracks, vias, 0), "single-layer-via"); got != 1 {
		t.Fatalf("foreign-net-masked single-layer-via = %d, want 1", got)
	}
}

// FIX #6: width-mismatch must ignore an unrelated track that merely crosses the
// pad XY on another layer/net — only the pad's own-net entering tracks count.
func TestPcbCheck_Fix6_WidthMismatchNetAware(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "R1", Number: "1", Net: "SIG_A", Layer: 1, X: 0, Y: 0},
		{Designator: "R1", Number: "2", Net: "SIG_B", Layer: 1, X: 100, Y: 0},
		{Designator: "PX", Number: "1", Net: "SIG_A", Layer: 1, X: -50, Y: 0},
		{Designator: "PY", Number: "1", Net: "SIG_B", Layer: 1, X: 150, Y: 0},
	}
	tracks := []pcbTrack{
		{ID: "a", Net: "SIG_A", Layer: 1, X1: 0, Y1: 0, X2: -50, Y2: 0, Width: 10},
		{ID: "b", Net: "SIG_B", Layer: 1, X1: 100, Y1: 0, X2: 150, Y2: 0, Width: 10},
		{ID: "c", Net: "OTHER", Layer: 2, X1: 100, Y1: 0, X2: 100, Y2: 80, Width: 30},
	}
	if got := countType(analyzePcbCheck(pads, tracks, nil, 0), "width-mismatch"); got != 0 {
		t.Fatalf("cross-layer-inflated width-mismatch = %d, want 0", got)
	}
}

// FIX #7: duplicate detection must be order-independent — a short segment sitting
// on a long slightly-angled one is a duplicate regardless of slice order.
func TestPcbCheck_Fix7_DuplicateOrderIndependent(t *testing.T) {
	S := pcbTrack{ID: "S", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 40, Y2: 0, Width: 10}
	L := pcbTrack{ID: "L", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 400, Y2: 4, Width: 10}
	if got := countType(analyzePcbCheck(nil, []pcbTrack{S, L}, nil, 0), "duplicate-segment"); got != 1 {
		t.Fatalf("[S,L] duplicate-segment = %d, want 1", got)
	}
	if got := countType(analyzePcbCheck(nil, []pcbTrack{L, S}, nil, 0), "duplicate-segment"); got != 1 {
		t.Fatalf("[L,S] duplicate-segment = %d, want 1", got)
	}
}

// Silk orientation: a reference designator (位号) not reading upright (0°) — 180°
// upside-down or 90/270° sideways — is flagged; upright + non-designator ignored.
func TestPcbCheck_SilkDesignatorOrientation(t *testing.T) {
	silk := []pcbSilkText{
		{ID: "s1", Kind: "attribute", Key: "Designator", Text: "C1", Layer: silkTopLayer, Rotation: 180},   // upside-down
		{ID: "s2", Kind: "attribute", Key: "Designator", Text: "LED1", Layer: silkTopLayer, Rotation: 450}, // →90 sideways
		{ID: "s3", Kind: "attribute", Key: "Designator", Text: "U1", Layer: silkTopLayer, Rotation: 0},     // upright OK
		{ID: "s4", Kind: "attribute", Key: "Footprint", Text: "C0402", Layer: silkTopLayer, Rotation: 180}, // not a RefDes → ignore
	}
	rep := analyzePcbCheckFull(nil, nil, nil, nil,silk, 0)
	if got := countType(rep, "silkscreen-flipped"); got != 2 {
		t.Fatalf("silk orientation = %d, want 2 (C1 180° + LED1 90°; upright + footprint ignored): %+v", got, rep.Findings)
	}
}

// A mirrored/reversed top-silk text reads backwards → ERROR.
func TestPcbCheck_SilkReversed(t *testing.T) {
	silk := []pcbSilkText{
		{ID: "s1", Kind: "attribute", Key: "Designator", Text: "R9", Layer: silkTopLayer, Reverse: true},
	}
	rep := analyzePcbCheckFull(nil, nil, nil, nil,silk, 0)
	if got := countType(rep, "silkscreen-flipped"); got != 1 {
		t.Fatalf("reversed top-silk = %d, want 1 (reads backwards)", got)
	}
}

// Antenna keep-out: an antenna module with no overlapping no-copper region is
// flagged; a covering no-copper region clears it; a non-copper region does not.
func TestPcbCheck_AntennaKeepout(t *testing.T) {
	ants := []pcbAntComp{{Designator: "U1", Device: "ESP32-S3-WROOM-1", BBox: &layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 200}}}
	bb := &layoutBBox{MinX: 0, MinY: 150, MaxX: 100, MaxY: 260} // overlaps the antenna
	// no keep-out at all → flagged.
	if got := len(findAntennaKeepout(ants, nil, 4)); got != 1 {
		t.Fatalf("no keep-out = %d, want 1", got)
	}
	// TOP-only keep-out on a 4-layer board → still flagged (bottom + inner missing) — the user's bug.
	topOnly := []pcbKeepRegion{{BBox: bb, Layer: 1, NoOuterCopper: true, NoInnerElectrical: true}}
	if got := len(findAntennaKeepout(ants, topOnly, 4)); got != 1 {
		t.Fatalf("top-only keep-out (4L) = %d, want 1 (bottom missing)", got)
	}
	// TOP (with inner) + BOTTOM keep-out on 4-layer → clear.
	full := []pcbKeepRegion{
		{BBox: bb, Layer: 1, NoOuterCopper: true, NoInnerElectrical: true},
		{BBox: bb, Layer: 2, NoOuterCopper: true},
	}
	if got := len(findAntennaKeepout(ants, full, 4)); got != 0 {
		t.Fatalf("top+bottom+inner keep-out (4L) = %d, want 0", got)
	}
	// On a 2-layer board, top+bottom is enough (no inner requirement).
	twoLayer := []pcbKeepRegion{
		{BBox: bb, Layer: 1, NoOuterCopper: true},
		{BBox: bb, Layer: 2, NoOuterCopper: true},
	}
	if got := len(findAntennaKeepout(ants, twoLayer, 2)); got != 0 {
		t.Fatalf("top+bottom keep-out (2L) = %d, want 0", got)
	}
	// A SINGLE MULTI-layer (12/多层) no-copper region covers top+bottom+inner at once → clear on 4L.
	multi := []pcbKeepRegion{{BBox: bb, Layer: pcbLayerMulti, NoOuterCopper: true}}
	if got := len(findAntennaKeepout(ants, multi, 4)); got != 0 {
		t.Fatalf("multi-layer keep-out (4L) = %d, want 0 (12 covers all layers)", got)
	}
	if got := len(findAntennaKeepout(ants, multi, 2)); got != 0 {
		t.Fatalf("multi-layer keep-out (2L) = %d, want 0", got)
	}
	if !isAntennaDevice("ESP32-S3-WROOM-1", "U1") || !isAntennaDevice("", "ANT1") || isAntennaDevice("0402WGF", "R2") {
		t.Fatal("isAntennaDevice classification wrong")
	}
}

// FIX #8: diverging (wedge) traces that nearly touch at one end must be flagged —
// the closest approach over the overlap is the coupling risk, not the midpoint.
func TestPcbCheck_Fix8_CouplingDivergingWedge(t *testing.T) {
	tracks := []pcbTrack{
		{ID: "a", Net: "SIG1", Layer: 1, X1: 0, Y1: 0, X2: 300, Y2: 0, Width: 10},
		{ID: "b", Net: "SIG2", Layer: 1, X1: 0, Y1: 2, X2: 291.09, Y2: 74.58, Width: 10},
	}
	if got := countType(analyzePcbCheck(nil, tracks, nil, 0), "parallel-coupling"); got != 1 {
		t.Fatalf("diverging-wedge parallel-coupling = %d, want 1", got)
	}
}

// findClearanceViolations flags a track running under the spacing rule to another
// net's pad / via / track (same layer for pads), skips same-net + endpoint + pure
// overlap (track-over-pad owns those) + other-layer pads.
func TestFindClearanceViolations(t *testing.T) {
	// Track on layer 1 passing 8mil from a SIG2 pad (clearance 6 + nominalPadHalf 12
	// = 18mil band; 8 < 18 → flagged).
	tracks := []pcbTrack{{ID: "t", Net: "SIG1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 10}}
	pad := []pcbPadP{{Designator: "R1", Number: "1", Net: "SIG2", Layer: 1, X: 50, Y: 8}}
	if got := countType(findClearanceViolationsReport(tracks, pad, nil, 6), "clearance"); got != 1 {
		t.Fatalf("track 8mil from other-net pad: clearance = %d, want 1", got)
	}
	// Same net → not a violation.
	if got := countType(findClearanceViolationsReport(tracks, []pcbPadP{{Net: "SIG1", Layer: 1, X: 50, Y: 8}}, nil, 6), "clearance"); got != 0 {
		t.Errorf("same-net pad flagged, want 0")
	}
	// Other layer → not a violation (layer-aware).
	if got := countType(findClearanceViolationsReport(tracks, []pcbPadP{{Net: "SIG2", Layer: 2, X: 50, Y: 8}}, nil, 6), "clearance"); got != 0 {
		t.Errorf("other-layer pad flagged, want 0")
	}
	// Endpoint pad (legitimate termination) → not a violation.
	if got := countType(findClearanceViolationsReport(tracks, []pcbPadP{{Net: "SIG2", Layer: 1, X: 100, Y: 0}}, nil, 6), "clearance"); got != 0 {
		t.Errorf("endpoint pad flagged, want 0")
	}
	// Other-net via 10mil away (clearance 6 + via radius 12 = 18mil band) → flagged.
	via := []pcbViaP{{ID: "v", Net: "SIG2", X: 50, Y: 10, Dia: 24}}
	if got := countType(findClearanceViolationsReport(tracks, nil, via, 6), "clearance"); got != 1 {
		t.Errorf("track near other-net via: clearance = %d, want 1", got)
	}
}

func findClearanceViolationsReport(tracks []pcbTrack, pads []pcbPadP, vias []pcbViaP, clr float64) pcbCheckReport {
	return pcbCheckReport{Findings: findClearanceViolations(tracks, pads, vias, nil, clr)}
}

// Slot (board cutout) + via↔pad clearance — the classes native DRC reports as
// 挖槽区域到导线/过孔 and 贴片焊盘到过孔.
func TestFindClearanceViolations_SlotAndVia(t *testing.T) {
	slot := []pcbSlotP{{ID: "s1", MinX: 100, MinY: 100, MaxX: 220, MaxY: 220}}
	// Vertical track at x=90, width 10 → copper edge reaches x=95, slot edge at
	// x=100 → 5mil gap < 8mil cutout rule → flagged.
	tr := []pcbTrack{{ID: "t", Net: "+5V", Layer: 2, X1: 90, Y1: 0, X2: 90, Y2: 300, Width: 10}}
	if got := countType(pcbCheckReport{Findings: findClearanceViolations(tr, nil, nil, slot, 6)}, "clearance"); got != 1 {
		t.Fatalf("track 5mil from slot: clearance = %d, want 1", got)
	}
	// Via at x=90 (radius 12 → edge x=102, inside the slot band) → flagged.
	v := []pcbViaP{{ID: "v", Net: "+5V", X: 90, Y: 150, Dia: 24}}
	if got := countType(pcbCheckReport{Findings: findClearanceViolations(nil, nil, v, slot, 6)}, "clearance"); got != 1 {
		t.Fatalf("via near slot: clearance = %d, want 1", got)
	}
	// Track comfortably away (copper edge gap 25mil ≥ 8) → clean.
	far := []pcbTrack{{ID: "t2", Net: "+5V", Layer: 2, X1: 70, Y1: 0, X2: 70, Y2: 300, Width: 10}}
	if got := countType(pcbCheckReport{Findings: findClearanceViolations(far, nil, nil, slot, 6)}, "clearance"); got != 1 {
		// edge gap = 100-70-5 = 25 ≥ 8 → 0 findings expected; keep explicit check
		if got != 0 {
			t.Errorf("far track findings = %d, want 0", got)
		}
	}
	// via ↔ pad (through-hole clash, any layer): center 25mil, minus viaR 12 and
	// padHalf 12 → 1mil < 6 → flagged.
	pad := []pcbPadP{{Designator: "J1", Number: "A7", Net: "USB_DN", Layer: 1, X: 0, Y: 0}}
	nearVia := []pcbViaP{{ID: "v2", Net: "+5V", X: 25, Y: 0, Dia: 24}}
	if got := countType(pcbCheckReport{Findings: findClearanceViolations(nil, pad, nearVia, nil, 6)}, "clearance"); got != 1 {
		t.Fatalf("via near other-net pad: clearance = %d, want 1", got)
	}
}

// #43 regression: clearance must be judged (and PRINTED) as a COPPER-EDGE gap,
// not a centerline distance. The old centerline test produced self-contradictory
// messages ("runs 16.9mil from via — under the 6mil rule") and under-reported
// track↔track overlap.
func TestFindClearance_EdgeDistanceSemantics(t *testing.T) {
	// track↔via: 10mil track, 24mil via (r=12). Centerline 16.9 → edge 16.9-12-5 = -0.1 → violation.
	tracks := []pcbTrack{{ID: "t1", Net: "A", Layer: 1, X1: 0, Y1: 16.9, X2: 200, Y2: 16.9, Width: 10}}
	vias := []pcbViaP{{ID: "v1", Net: "B", X: 100, Y: 0, Hole: 12, Dia: 24}}
	out := findClearanceViolations(tracks, nil, vias, nil, 6)
	if len(out) != 1 {
		t.Fatalf("track 16.9mil (centerline) from a 24mil via = edge -0.1mil, want 1 violation, got %d", len(out))
	}
	if !strings.Contains(out[0].Message, "0.0mil") {
		t.Errorf("message must print the EDGE gap (0.0), got: %s", out[0].Message)
	}
	// Same via, track pulled far enough that the EDGE gap clears 6mil:
	// need centerD >= 6 + 12 + 5 = 23.
	farTracks := []pcbTrack{{ID: "t2", Net: "A", Layer: 1, X1: 0, Y1: 23.5, X2: 200, Y2: 23.5, Width: 10}}
	if out := findClearanceViolations(farTracks, nil, vias, nil, 6); len(out) != 0 {
		t.Errorf("edge gap 6.5mil clears the 6mil rule, got %d violation(s): %+v", len(out), out)
	}
	// track↔track: two 10mil tracks 8mil apart on centerlines OVERLAP (edge -2).
	pair := []pcbTrack{
		{ID: "a", Net: "A", Layer: 1, X1: 0, Y1: 0, X2: 200, Y2: 0, Width: 10},
		{ID: "b", Net: "B", Layer: 1, X1: 0, Y1: 8, X2: 200, Y2: 8, Width: 10},
	}
	if out := findClearanceViolations(pair, nil, nil, nil, 6); len(out) != 1 {
		t.Fatalf("two 10mil tracks 8mil apart on centerlines overlap — want 1 violation, got %d", len(out))
	}
	// Centerline 16.5 → edge 6.5 → clears.
	okPair := []pcbTrack{
		{ID: "a", Net: "A", Layer: 1, X1: 0, Y1: 0, X2: 200, Y2: 0, Width: 10},
		{ID: "b", Net: "B", Layer: 1, X1: 0, Y1: 16.5, X2: 200, Y2: 16.5, Width: 10},
	}
	if out := findClearanceViolations(okPair, nil, nil, nil, 6); len(out) != 0 {
		t.Errorf("edge gap 6.5mil clears, got %d: %+v", len(out), out)
	}
}

// #43 R2: two tracks that actually CROSS (centerD == 0) are a dead short — the
// worst case — and must always report. A `centerD > pcbOverPadEps` guard used to
// skip exactly them while flagging the near-misses beside them ("擦肩报错,撞上
//放行"): the track↔pad branch hands its overlap band to track-over-pad, but
// nothing owns track↔track overlap.
func TestFindClearance_CrossingTracksAreReported(t *testing.T) {
	crossing := []pcbTrack{
		{ID: "a", Net: "SPIHD", Layer: 1, X1: 700, Y1: 407.4, X2: 900, Y2: 407.4, Width: 10},
		{ID: "b", Net: "SPIWP", Layer: 1, X1: 815.4, Y1: 380.7, X2: 815.4, Y2: 423.1, Width: 10},
	}
	out := findClearanceViolations(crossing, nil, nil, nil, 6)
	if len(out) != 1 {
		t.Fatalf("crossing tracks (dead short) must report, got %d violation(s)", len(out))
	}
	if !strings.Contains(out[0].Message, "0.0mil") {
		t.Errorf("a crossing prints a 0.0mil gap, got: %s", out[0].Message)
	}
	// Same-net crossing is legitimate (a T-junction) — never flagged.
	sameNet := []pcbTrack{
		{ID: "a", Net: "N", Layer: 1, X1: 700, Y1: 400, X2: 900, Y2: 400, Width: 10},
		{ID: "b", Net: "N", Layer: 1, X1: 800, Y1: 350, X2: 800, Y2: 450, Width: 10},
	}
	if out := findClearanceViolations(sameNet, nil, nil, nil, 6); len(out) != 0 {
		t.Errorf("same-net crossing is a junction, got %d: %+v", len(out), out)
	}
	// Different LAYER crossing is fine (that's what layers are for).
	crossLayer := []pcbTrack{
		{ID: "a", Net: "A", Layer: 1, X1: 700, Y1: 400, X2: 900, Y2: 400, Width: 10},
		{ID: "b", Net: "B", Layer: 2, X1: 800, Y1: 350, X2: 800, Y2: 450, Width: 10},
	}
	if out := findClearanceViolations(crossLayer, nil, nil, nil, 6); len(out) != 0 {
		t.Errorf("cross-layer crossing is legal, got %d: %+v", len(out), out)
	}
}
