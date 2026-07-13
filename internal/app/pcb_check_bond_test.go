package app

import (
	"testing"
)

// The `via-bond` ERROR rule (and its tests) were removed after pro-api-sdk#31
// was confirmed to be our misdiagnosis — track↔via DOES register as connected;
// see the header note in pcb_check_bond.go.

// ── floating-track-island ───────────────────────────────────────────────────

func TestFindFloatingTrackIslands(t *testing.T) {
	// Two touching tracks, no pad anywhere near → one island finding.
	tracks := []pcbTrack{
		{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 6},
		{ID: "t2", Net: "N1", Layer: 1, X1: 100, Y1: 0, X2: 100, Y2: 100, Width: 6},
	}
	got := findFloatingTrackIslands(tracks, nil, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Type != "floating-track-island" || f.Level != "WARN" {
		t.Errorf("bad type/level: %+v", f)
	}
	if len(f.Primitives) != 2 {
		t.Errorf("island should list both tracks: %+v", f.Primitives)
	}

	// Same island anchored to a same-net pad at one endpoint → silent.
	pads := []pcbPadP{{Designator: "R1", Net: "N1", Layer: 1, X: 0, Y: 0}}
	if got := findFloatingTrackIslands(tracks, nil, pads, nil); len(got) != 0 {
		t.Errorf("pad-anchored island flagged: %+v", got)
	}

	// Same-net pour on the island's layer → bonded, silent.
	pours := []pcbPourP{{ID: "p1", Net: "N1", Layer: 1}}
	if got := findFloatingTrackIslands(tracks, nil, nil, pours); len(got) != 0 {
		t.Errorf("pour-covered island flagged: %+v", got)
	}
}

func TestFindFloatingTrackIslandsSingleAndViaBridge(t *testing.T) {
	// A single floating track is dangling-end's territory — no island finding.
	single := []pcbTrack{{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 6}}
	if got := findFloatingTrackIslands(single, nil, nil, nil); len(got) != 0 {
		t.Errorf("single track must be left to dangling-end: %+v", got)
	}

	// Two tracks on DIFFERENT layers joined only by a via → one island (via
	// bridges); still floating (no pad) → one finding spanning both.
	tracks := []pcbTrack{
		{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 6},
		{ID: "t2", Net: "N1", Layer: 2, X1: 100, Y1: 0, X2: 200, Y2: 0, Width: 6},
	}
	vias := []pcbViaP{{ID: "v1", Net: "N1", X: 100, Y: 0, Dia: 24}}
	got := findFloatingTrackIslands(tracks, vias, nil, nil)
	if len(got) != 1 || len(got[0].Primitives) != 2 {
		t.Fatalf("via-bridged island wrong: %+v", got)
	}

	// Anchoring EITHER side's endpoint to a pad silences the whole island.
	pads := []pcbPadP{{Designator: "U1", Net: "N1", Layer: 2, X: 200, Y: 0}}
	if got := findFloatingTrackIslands(tracks, vias, pads, nil); len(got) != 0 {
		t.Errorf("pad on the far side should anchor the island: %+v", got)
	}
}

// ── dangling-end via-area anchoring upgrade ─────────────────────────────────

func TestDanglingEndViaAreaAnchor(t *testing.T) {
	// Endpoint inside a same-net via's copper but off its center (the round-#1
	// false positive on stubs official DRC accepted) → anchored, no finding.
	tracks := []pcbTrack{{ID: "t1", Net: "GND", Layer: 1, X1: 0, Y1: 0, X2: 192, Y2: 0, Width: 10}}
	vias := []pcbViaP{{ID: "v1", Net: "GND", X: 200, Y: 0, Dia: 24}}
	pads := []pcbPadP{{Designator: "C1", Net: "GND", Layer: 1, X: 0, Y: 0}} // anchors the left end
	got := findDanglingEnds(tracks, vias, pads, nil)
	if len(got) != 0 {
		t.Errorf("same-net endpoint inside via copper must anchor: %+v", got)
	}

	// A FOREIGN-net via at the same off-center spot must NOT anchor.
	foreign := []pcbViaP{{ID: "v1", Net: "+5V", X: 200, Y: 0, Dia: 24}}
	got = findDanglingEnds(tracks, foreign, pads, nil)
	if len(got) != 1 {
		t.Errorf("foreign via off-center must not anchor: %+v", got)
	}
}

// beautify rounds a corner into track→arc→track: the two tracks now terminate on the
// arc's endpoints, not on each other. A dangling-end check that ignores arcs would
// fabricate a false stub at every rounded corner — with arcs it stays clean.
func TestDanglingEndArcAnchor(t *testing.T) {
	// Corner at (100,0): horizontal track in from the left, vertical track down —
	// beautify replaces the shared vertex with a small arc whose endpoints sit just
	// before each track end. Both tracks are anchored at their far ends by pads.
	tracks := []pcbTrack{
		{ID: "h", Net: "SIG", Layer: 1, X1: 0, Y1: 0, X2: 90, Y2: 0, Width: 10},
		{ID: "v", Net: "SIG", Layer: 1, X1: 100, Y1: -10, X2: 100, Y2: -100, Width: 10},
	}
	pads := []pcbPadP{
		{Designator: "U1", Net: "SIG", Layer: 1, X: 0, Y: 0},
		{Designator: "U2", Net: "SIG", Layer: 1, X: 100, Y: -100},
	}
	arcs := []pcbArc{{ID: "a", Net: "SIG", Layer: 1, X1: 90, Y1: 0, X2: 100, Y2: -10}}

	// Without the arc, the inner ends (90,0) and (100,-10) dangle → 2 findings.
	if got := findDanglingEnds(tracks, nil, pads, nil); len(got) != 2 {
		t.Fatalf("precondition: without arc want 2 dangling, got %d", len(got))
	}
	// With the arc, both inner ends anchor on its endpoints → 0 findings.
	if got := findDanglingEnds(tracks, nil, pads, arcs); len(got) != 0 {
		t.Errorf("arc endpoints must anchor beautified track ends: %+v", got)
	}

	// A DIFFERENT-layer arc must NOT anchor (no via = no layer transition).
	otherLayer := []pcbArc{{ID: "a", Net: "SIG", Layer: 2, X1: 90, Y1: 0, X2: 100, Y2: -10}}
	if got := findDanglingEnds(tracks, nil, pads, otherLayer); len(got) != 2 {
		t.Errorf("arc on another layer must not anchor: got %d, want 2", len(got))
	}
}
