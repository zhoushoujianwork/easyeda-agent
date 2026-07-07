package app

import (
	"strings"
	"testing"
)

// ── via-bond ────────────────────────────────────────────────────────────────

// A bare same-net track↔via junction on a 4-layer board must be an ERROR;
// a fill bbox covering the junction, or a same-net pour on the layer, exempts it.
func TestFindViaBondIssuesBareJunction(t *testing.T) {
	tracks := []pcbTrack{
		// endpoint exactly on via center (the round-#1 failing pattern)
		{ID: "t1", Net: "+5V", Layer: 1, X1: 100, Y1: 100, X2: 200, Y2: 100, Width: 10},
		// endpoint inside via copper but off-center
		{ID: "t2", Net: "+5V", Layer: 2, X1: 208, Y1: 100, X2: 300, Y2: 100, Width: 10},
	}
	vias := []pcbViaP{{ID: "v1", Net: "+5V", X: 200, Y: 100, Dia: 24}}

	got := findViaBondIssues(tracks, vias, nil, nil, 4, 0)
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2 (one per via+layer): %+v", len(got), got)
	}
	for _, f := range got {
		if f.Type != "via-bond" || f.Level != "ERROR" {
			t.Errorf("bad type/level: %+v", f)
		}
		if !strings.Contains(f.Message, "pro-api-sdk#31") || !strings.Contains(f.Message, "via-hop") {
			t.Errorf("message must cite #31 + the fix: %q", f.Message)
		}
		if len(f.Primitives) != 2 {
			t.Errorf("finding should carry via+track ids: %+v", f.Primitives)
		}
	}
}

func TestFindViaBondIssuesExemptions(t *testing.T) {
	tracks := []pcbTrack{{ID: "t1", Net: "GND", Layer: 1, X1: 100, Y1: 100, X2: 200, Y2: 100, Width: 10}}
	vias := []pcbViaP{{ID: "v1", Net: "GND", X: 200, Y: 100, Dia: 24}}

	// Bond fill bbox covering the junction → exempt.
	fills := []pcbFillP{{ID: "f1", Net: "GND", Layer: 1, MinX: 190, MinY: 90, MaxX: 210, MaxY: 110, HasBBox: true}}
	if got := findViaBondIssues(tracks, vias, fills, nil, 4, 0); len(got) != 0 {
		t.Errorf("fill-bonded junction flagged: %+v", got)
	}

	// Fill on the WRONG layer does not bond.
	wrongLayer := []pcbFillP{{ID: "f1", Net: "GND", Layer: 2, MinX: 190, MinY: 90, MaxX: 210, MaxY: 110, HasBBox: true}}
	if got := findViaBondIssues(tracks, vias, wrongLayer, nil, 4, 0); len(got) != 1 {
		t.Errorf("wrong-layer fill should not bond: %+v", got)
	}

	// Fill far away does not bond.
	farFill := []pcbFillP{{ID: "f1", Net: "GND", Layer: 1, MinX: 900, MinY: 900, MaxX: 920, MaxY: 920, HasBBox: true}}
	if got := findViaBondIssues(tracks, vias, farFill, nil, 4, 0); len(got) != 1 {
		t.Errorf("far fill should not bond: %+v", got)
	}

	// Older connector (no bbox): same-net fill on the layer → assumed bonded.
	noBBox := []pcbFillP{{ID: "f1", Net: "GND", Layer: 1}}
	if got := findViaBondIssues(tracks, vias, noBBox, nil, 4, 0); len(got) != 0 {
		t.Errorf("degraded (no-bbox) fill should exempt: %+v", got)
	}

	// Same-net pour on the layer → bonded.
	pours := []pcbPourP{{ID: "p1", Net: "GND", Layer: 1}}
	if got := findViaBondIssues(tracks, vias, nil, pours, 4, 0); len(got) != 0 {
		t.Errorf("pour-bonded junction flagged: %+v", got)
	}
}

func TestFindViaBondIssuesBoardClassGate(t *testing.T) {
	tracks := []pcbTrack{{ID: "t1", Net: "+5V", Layer: 1, X1: 100, Y1: 100, X2: 200, Y2: 100, Width: 10}}
	vias := []pcbViaP{{ID: "v1", Net: "+5V", X: 200, Y: 100, Dia: 24}}

	// Plain 2-layer board: junctions register fine — rule must stay silent.
	if got := findViaBondIssues(tracks, vias, nil, nil, 2, 0); len(got) != 0 {
		t.Errorf("2-layer board must not be flagged: %+v", got)
	}
	// 2 copper layers but a PLANE exists → the failing class.
	if got := findViaBondIssues(tracks, vias, nil, nil, 2, 1); len(got) != 1 {
		t.Errorf("PLANE-bearing board must be flagged: %+v", got)
	}
}

func TestFindViaBondIssuesViaOnBody(t *testing.T) {
	// Via pressed onto the middle of a track body (round #1: also non-conducting).
	tracks := []pcbTrack{{ID: "t1", Net: "U0TXD", Layer: 1, X1: 0, Y1: 0, X2: 400, Y2: 0, Width: 10}}
	vias := []pcbViaP{{ID: "v1", Net: "U0TXD", X: 200, Y: 5, Dia: 24}} // 5 mil off centerline, within Dia/2
	if got := findViaBondIssues(tracks, vias, nil, nil, 4, 0); len(got) != 1 {
		t.Fatalf("via-on-body junction not flagged: %+v", got)
	}
	// Different net never pairs.
	foreign := []pcbViaP{{ID: "v2", Net: "GND", X: 200, Y: 5, Dia: 24}}
	if got := findViaBondIssues(tracks, foreign, nil, nil, 4, 0); len(got) != 0 {
		t.Errorf("foreign-net via must not pair: %+v", got)
	}
}

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
	got := findDanglingEnds(tracks, vias, pads)
	if len(got) != 0 {
		t.Errorf("same-net endpoint inside via copper must anchor: %+v", got)
	}

	// A FOREIGN-net via at the same off-center spot must NOT anchor.
	foreign := []pcbViaP{{ID: "v1", Net: "+5V", X: 200, Y: 0, Dia: 24}}
	got = findDanglingEnds(tracks, foreign, pads)
	if len(got) != 1 {
		t.Errorf("foreign via off-center must not anchor: %+v", got)
	}
}
