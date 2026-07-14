package app

import "testing"

// ── silk-over-pad (§11.2) ────────────────────────────────────────────────────

func TestFindSilkOverPad(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "R1", Number: "1", Net: "N1", Layer: 1, X: 0, Y: 0},      // top pad under top silk
		{Designator: "R2", Number: "1", Net: "N2", Layer: 2, X: 0, Y: 0},      // bottom pad — top silk can't hit it
		{Designator: "R3", Number: "1", Net: "N3", Layer: 1, X: 500, Y: 500},  // far away
	}
	silk := []pcbSilkText{
		{ID: "s1", Kind: "attribute", Key: "Designator", Text: "R1", Layer: 3, X: 5, Y: 5},
		{ID: "s2", Kind: "string", Text: "OK", Layer: 3, X: 300, Y: 300}, // clear
	}
	out := findSilkOverPad(silk, pads)
	if len(out) != 1 {
		t.Fatalf("silk-over-pad = %d, want 1 (%+v)", len(out), out)
	}
	if out[0].Designator != "R1" || out[0].Primitives[0] != "s1" {
		t.Errorf("wrong target: %+v", out[0])
	}
}

// A through-hole (multi-layer) pad is exposed on BOTH silk sides.
func TestFindSilkOverPad_ThroughHole(t *testing.T) {
	pads := []pcbPadP{{Designator: "J1", Number: "1", Net: "N1", Layer: 11, X: 0, Y: 0}}
	silk := []pcbSilkText{{ID: "s1", Kind: "string", Text: "TXT", Layer: 4, X: 0, Y: 0}}
	if out := findSilkOverPad(silk, pads); len(out) != 1 {
		t.Fatalf("through-hole pad under bottom silk = %d findings, want 1", len(out))
	}
}

// 90°-rotated text swaps its extents: a pad beside the (now tall) box that would
// have been inside the unrotated wide box must NOT be flagged.
func TestFindSilkOverPad_Rotation(t *testing.T) {
	// 10-char text: unrotated half-width 120mil; rotated 90° half-width becomes 20.
	pads := []pcbPadP{{Designator: "R1", Number: "1", Net: "N1", Layer: 1, X: 100, Y: 0}}
	silk := []pcbSilkText{{ID: "s1", Kind: "string", Text: "ABCDEFGHIJ", Layer: 3, X: 0, Y: 0, Rotation: 90}}
	if out := findSilkOverPad(silk, pads); len(out) != 0 {
		t.Fatalf("rotated-away text flagged: %+v", out)
	}
}

// ── decap-too-far (§3.1) ─────────────────────────────────────────────────────

func TestFindDecapTooFar(t *testing.T) {
	pads := []pcbPadP{
		// U1: 3V3 pin at origin.
		{Designator: "U1", Number: "1", Net: "3V3", Layer: 1, X: 0, Y: 0},
		{Designator: "U1", Number: "2", Net: "GND", Layer: 1, X: 0, Y: 50},
		// C1: proper decap, 40mil away — OK.
		{Designator: "C1", Number: "1", Net: "3V3", Layer: 1, X: 40, Y: 0},
		{Designator: "C1", Number: "2", Net: "GND", Layer: 1, X: 80, Y: 0},
		// C2: decap parked 400mil away — flagged.
		{Designator: "C2", Number: "1", Net: "3V3", Layer: 1, X: 400, Y: 0},
		{Designator: "C2", Number: "2", Net: "GND", Layer: 1, X: 440, Y: 0},
		// C3: rail with no IC pad (VIN) — bulk cap, skipped.
		{Designator: "C3", Number: "1", Net: "VIN", Layer: 1, X: 900, Y: 0},
		{Designator: "C3", Number: "2", Net: "GND", Layer: 1, X: 940, Y: 0},
		// C4: signal-signal cap (AC coupling) — not a decap, skipped.
		{Designator: "C4", Number: "1", Net: "SIG_A", Layer: 1, X: 900, Y: 900},
		{Designator: "C4", Number: "2", Net: "SIG_B", Layer: 1, X: 940, Y: 900},
	}
	out := findDecapTooFar(pads)
	if len(out) != 1 {
		t.Fatalf("decap-too-far = %d, want 1 (%+v)", len(out), out)
	}
	if out[0].Designator != "C2" || out[0].Net != "3V3" {
		t.Errorf("wrong offender: %+v", out[0])
	}
}

// ── via-in-pad (§2.3) ────────────────────────────────────────────────────────

func TestFindViaInPad(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "U1", Number: "1", Net: "GND", Layer: 1, X: 0, Y: 0},
		{Designator: "U1", Number: "2", Net: "3V3", Layer: 1, X: 100, Y: 0},
	}
	vias := []pcbViaP{
		{ID: "v1", Net: "GND", X: 2, Y: 2, Hole: 12, Dia: 24},   // ON the GND pad → flagged
		{ID: "v2", Net: "GND", X: 40, Y: 0, Hole: 12, Dia: 24},  // offset dog-bone → OK
		{ID: "v3", Net: "3V3", X: 0, Y: 0, Hole: 12, Dia: 24},   // cross-net on pad — clearance rule's case, not ours
	}
	out := findViaInPad(vias, pads)
	if len(out) != 1 {
		t.Fatalf("via-in-pad = %d, want 1 (%+v)", len(out), out)
	}
	if out[0].Primitives[0] != "v1" {
		t.Errorf("wrong via: %+v", out[0])
	}
}

// ── copper-near-edge (§5.1) ──────────────────────────────────────────────────

func TestFindCopperNearEdge(t *testing.T) {
	outline := &layoutBBox{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 1000}
	tracks := []pcbTrack{
		// endpoint 4mil from the left edge (width 10 → copper edge at -1) → flagged
		{ID: "t1", Net: "SIG1", Layer: 1, X1: 4, Y1: 500, X2: 300, Y2: 500, Width: 10},
		// comfortably interior → OK
		{ID: "t2", Net: "SIG2", Layer: 1, X1: 500, Y1: 500, X2: 600, Y2: 500, Width: 10},
		// net-less (board outline segment itself) → skipped
		{ID: "t3", Net: "", Layer: 1, X1: 0, Y1: 0, X2: 1000, Y2: 0, Width: 10},
	}
	vias := []pcbViaP{
		{ID: "v1", Net: "GND", X: 995, Y: 500, Hole: 12, Dia: 24}, // 5mil to edge minus radius → flagged
		{ID: "v2", Net: "GND", X: 500, Y: 500, Hole: 12, Dia: 24}, // interior → OK
	}
	out := findCopperNearEdge(tracks, vias, outline, 8)
	if len(out) != 2 { // aggregated per net: SIG1 + GND
		t.Fatalf("copper-near-edge = %d findings, want 2 (%+v)", len(out), out)
	}
	nets := map[string]bool{}
	for _, f := range out {
		nets[f.Net] = true
	}
	if !nets["SIG1"] || !nets["GND"] {
		t.Errorf("expected SIG1+GND, got %v", nets)
	}
	// nil outline → rule disabled, never panics
	if out := findCopperNearEdge(tracks, vias, nil, 8); out != nil {
		t.Errorf("nil outline must disable the rule, got %+v", out)
	}
}

// ── fiducial-missing (§9) ────────────────────────────────────────────────────

func TestFindFiducialMissing(t *testing.T) {
	// SMT-scale board (40 top pads), no fiducials → INFO.
	var pads []pcbPadP
	for i := 0; i < 40; i++ {
		pads = append(pads, pcbPadP{Designator: "U1", Number: "p", Net: "N", Layer: 1, X: float64(i), Y: 0})
	}
	out := findFiducialMissing(pads)
	if len(out) != 1 || out[0].Level != "INFO" {
		t.Fatalf("fiducial-missing = %+v, want 1 INFO", out)
	}
	// With 3 fiducials → clean.
	withFids := append(pads,
		pcbPadP{Designator: "FID1", Layer: 1, X: 0, Y: 0},
		pcbPadP{Designator: "FID2", Layer: 1, X: 100, Y: 0},
		pcbPadP{Designator: "FID3", Layer: 1, X: 0, Y: 100},
	)
	if out := findFiducialMissing(withFids); len(out) != 0 {
		t.Fatalf("3 fiducials must satisfy the rule, got %+v", out)
	}
	// Small hand-solder board (few pads) → rule silent.
	if out := findFiducialMissing(pads[:10]); len(out) != 0 {
		t.Fatalf("small board must not be flagged, got %+v", out)
	}
}

// The full-core clean board stays clean with the new rules wired in (no false
// positives from a well-formed layout).
func TestPcbCheck_DFM2_CleanBoardStillClean(t *testing.T) {
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
