package app

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ── fixture: chip + decoupling caps + crystal + RF chain + antenna + locked part ──

func simPadAt(num, net string, x, y float64) boardPad {
	return boardPad{Number: num, Net: net, Layer: 1, X: x, Y: y, W: 20, H: 20}
}

// simPart is a part whose anchor is its bbox centre.
func simPart(id, des, name string, cx, cy, w, h float64, locked bool, pads ...boardPad) boardComp {
	return boardComp{ID: id, Designator: des, Name: name, Layer: 1, X: cx, Y: cy, Locked: locked,
		BBox: &layoutBBox{MinX: cx - w/2, MinY: cy - h/2, MaxX: cx + w/2, MaxY: cy + h/2}, Pads: pads}
}

// simTwoPin is a 60×30 part drawn at (cx,cy) with pad 1 at its left end, pad 2 at its right end.
func simTwoPin(id, des, name, netA, netB string, cx, cy float64) boardComp {
	return simPart(id, des, name, cx, cy, 60, 30, false, simPadAt("1", netA, cx-20, cy), simPadAt("2", netB, cx+20, cy))
}

// simBoardSnap: U1 (main, 400×400 at the origin) with 3V3/XI/XO on its left edge and
// the RF pad on its right edge; three 3V3 caps and a crystal parked far away; an RF
// chain U1.RF → L1 → C4 → ANT1 with C5 shunting the L1/C4 junction; a locked R9; a
// 3-pin connector J1. Hashed like a real dump.
func simBoardSnap(t testing.TB) *boardSnapshot {
	t.Helper()
	u1 := simPart("p-u1", "U1", "MCU", 0, 0, 400, 400, false,
		simPadAt("1", "3V3", -190, 0), simPadAt("2", "XI", -190, 60), simPadAt("3", "XO", -190, 100),
		simPadAt("4", "RF_CHIP", 190, 0), simPadAt("5", "SIG1", 190, -60), simPadAt("6", "SIG2", 190, -100),
		simPadAt("7", "GND", 0, -190), simPadAt("8", "GND", 40, -190), simPadAt("9", "", 80, -190), simPadAt("10", "", 120, -190))
	snap := &boardSnapshot{
		Components: []boardComp{
			u1,
			simTwoPin("p-c1", "C1", "100nF", "3V3", "GND", 2500, -800),
			simTwoPin("p-c2", "C2", "10uF", "3V3", "GND", 2500, -900),
			simTwoPin("p-c3", "C3", "1uF", "3V3", "GND", 2500, -1000),
			simPart("p-y1", "Y1", "8MHz", 2500, -1200, 80, 40, false, simPadAt("1", "XI", 2480, -1200), simPadAt("2", "XO", 2520, -1200)),
			simPart("p-ant", "ANT1", "2.4G Antenna", 1500, 0, 100, 60, false, simPadAt("1", "ANT_FEED", 1450, 0), simPadAt("2", "GND", 1550, 0)),
			simTwoPin("p-l1", "L1", "3.9nH", "RF_CHIP", "N1", 2500, 400),
			simTwoPin("p-c4", "C4", "1.2pF", "ANT_FEED", "N1", 2500, 500),
			simTwoPin("p-c5", "C5", "1.8pF", "N1", "GND", 2500, 600),
			simPart("p-r9", "R9", "0R", 0, 900, 60, 30, true, simPadAt("1", "LA", -20, 900), simPadAt("2", "LB", 20, 900)),
			simPart("p-j1", "J1", "HDR-3", -1200, 0, 300, 250, false, simPadAt("1", "SIG1", -1300, 40), simPadAt("2", "SIG2", -1300, 0), simPadAt("3", "GND", -1300, -40)),
		},
		Outline:      &boardOutline{Source: "polygon", BBox: layoutBBox{MinX: -3000, MinY: -2000, MaxX: 3000, MaxY: 2000}, Points: [][2]float64{{-3000, -2000}, {3000, -2000}, {3000, 2000}, {-3000, 2000}}},
		CopperLayers: 2,
		Copper:       &boardCopperSnapshot{Availability: map[string]string{"regions": "available"}, Regions: []any{}},
	}
	hash, err := boardSnapshotSemanticSHA256(snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.SemanticSHA256 = hash
	return snap
}

func simPoses(hash string, poses ...lsPose) *lsPoseFile {
	return &lsPoseFile{BoardSemanticSHA256: hash, Poses: poses}
}

func fp(v float64) *float64 { return &v }

func simComp(t *testing.T, r *lsResult, des string) lsComponent {
	t.Helper()
	for _, c := range r.Components {
		if c.Designator == des {
			return c
		}
	}
	t.Fatalf("component %s missing from result", des)
	return lsComponent{}
}

func simMoveOf(r *lsResult, des string) (lsMove, bool) {
	for _, m := range r.Moves {
		if m.Designator == des {
			return m, true
		}
	}
	return lsMove{}, false
}

func simRun(t *testing.T, snap *boardSnapshot, poses ...lsPose) *lsResult {
	t.Helper()
	pf := simPoses(snap.SemanticSHA256, poses...)
	r, err := simulatePcbLayout(snap, pf, len(poses) > 0)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func simErr(t *testing.T, snap *boardSnapshot, pf *lsPoseFile) string {
	t.Helper()
	_, err := simulatePcbLayout(snap, pf, pf != nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err.Error()
}

// No poses: satellites are re-planned around the chip, everything else stays.
func TestLayoutSimulate_NoPosesReplansSatellitesAroundChip(t *testing.T) {
	snap := simBoardSnap(t)
	r := simRun(t, snap)
	if r.Schema != "easyeda.pcb.layout-sim/v1" || r.BoardSemanticSHA256 != snap.SemanticSHA256 {
		t.Errorf("schema/hash = %q / %q", r.Schema, r.BoardSemanticSHA256)
	}
	roles := map[string]string{"U1": "main", "C1": "satellite", "C2": "satellite", "C3": "satellite", "Y1": "satellite",
		"L1": "satellite", "C4": "satellite", "C5": "satellite", "ANT1": "antenna", "J1": "connector", "R9": "fixed"}
	for des, want := range roles {
		if got := simComp(t, r, des).Role; got != want {
			t.Errorf("%s role=%s, want %s", des, got, want)
		}
	}
	// Decoupling caps sit just left of the chip (bbox minX = -200), smallest value nearest the 3V3 pin.
	c1, c2, c3 := simComp(t, r, "C1"), simComp(t, r, "C2"), simComp(t, r, "C3")
	for _, c := range []lsComponent{c1, c2, c3} {
		if c.Owner != "U1" || c.X > -200 || !c.Moved {
			t.Errorf("%s owner=%q x=%.0f moved=%v, want a moved satellite left of U1", c.Designator, c.Owner, c.X, c.Moved)
		}
	}
	// 3V3 pin at y=0: slots by cost are y=0, -60 (the -d side wins ties), +60.
	if c1.Y != 0 || c3.Y != -60 || c2.Y != 60 {
		t.Errorf("100nF/1uF/10uF must take slots 0/-60/+60 in value order: y = %.0f %.0f %.0f", c1.Y, c3.Y, c2.Y)
	}
	// The RF chain lies on the line chip→antenna (y=0), antenna not moved.
	ant := simComp(t, r, "ANT1")
	if ant.Moved || ant.X != 1500 || ant.Owner != "U1" {
		t.Errorf("antenna must stay put and name its chip: %+v", ant)
	}
	for _, des := range []string{"L1", "C4"} {
		if c := simComp(t, r, des); math.Abs(c.Y) > 40 || c.X < 200 || c.X > 1450 {
			t.Errorf("%s at (%.0f,%.0f) is not on the feed line", des, c.X, c.Y)
		}
	}
	if m, ok := simMoveOf(r, "C4"); !ok || m.Via != "rf:ANT1" {
		t.Errorf("C4 move = %+v, want via rf:ANT1", m)
	}
	if m, ok := simMoveOf(r, "C1"); !ok || m.Via != "power" || !m.SetRotation {
		t.Errorf("C1 move = %+v, want via power with a rotation change", m)
	}
	// Unmoved parts are not in moves; moves are sorted by designator.
	for _, des := range []string{"U1", "ANT1", "J1", "R9"} {
		if _, ok := simMoveOf(r, des); ok {
			t.Errorf("%s did not change but is in moves", des)
		}
	}
	names := make([]string, len(r.Moves))
	for i, m := range r.Moves {
		names[i] = m.Designator
	}
	if !sort.SliceIsSorted(names, func(i, j int) bool { return simDesLess(names[i], names[j]) }) {
		t.Errorf("moves not sorted by designator: %v", names)
	}
	if r.Metrics.Unplaced != 0 {
		t.Errorf("unplaced=%d, want 0", r.Metrics.Unplaced)
	}
	// Planner notes come out as issues: the antenna anchor note is P3.
	found := false
	for _, is := range r.Issues {
		found = found || (is.Designator == "ANT1" && is.Severity == "P3")
	}
	if !found {
		t.Errorf("want the antenna anchor note as a P3 issue, got %+v", r.Issues)
	}
}

// Moving the chip by hand: its satellites follow, and the chip itself is a "pose" move.
func TestLayoutSimulate_ChipPoseSatellitesFollow(t *testing.T) {
	snap := simBoardSnap(t)
	base := simRun(t, snap)
	r := simRun(t, snap, lsPose{PrimitiveID: "p-u1", X: fp(500), Y: fp(0), Rotation: fp(0)})
	um, ok := simMoveOf(r, "U1")
	if !ok || um.Via != "pose" || um.X != 500 || um.Y != 0 {
		t.Fatalf("U1 move = %+v (found=%v), want a pose move to (500,0)", um, ok)
	}
	if um.SetRotation {
		t.Errorf("an unrotated pose must not set the rotation: %+v", um)
	}
	for _, des := range []string{"C1", "C2", "C3", "Y1"} {
		b, c := simComp(t, base, des), simComp(t, r, des)
		if math.Abs(c.X-(b.X+500)) > 0.01 || math.Abs(c.Y-b.Y) > 0.01 || c.Owner != "U1" {
			t.Errorf("%s at (%.1f,%.1f), want the baseline slot shifted +500 → (%.1f,%.1f)", des, c.X, c.Y, b.X+500, b.Y)
		}
		if m, ok := simMoveOf(r, des); !ok || math.Abs(m.X-c.X) > 0.01 {
			t.Errorf("%s move missing or stale: %+v", des, m)
		}
	}
	// The chip's pads travelled with it, and the RF chain re-aimed at the new RF pad (690,0).
	if p := simComp(t, r, "U1").Pads[0]; p.X != 310 || p.Y != 0 {
		t.Errorf("U1 pad 1 at (%.0f,%.0f), want (310,0)", p.X, p.Y)
	}
	if ant := simComp(t, r, "ANT1"); ant.Moved {
		t.Error("the antenna was not posed and must not move")
	}
	if c := simComp(t, r, "C4"); c.X < 700 || c.X > 1450 || math.Abs(c.Y) > 40 {
		t.Errorf("C4 at (%.0f,%.0f) is off the new feed line", c.X, c.Y)
	}
}

// A rotated pose rotates bbox, pads and pad extents about the anchor.
func TestLayoutSimulate_PoseRotatesAnchorBBoxAndPads(t *testing.T) {
	snap := simBoardSnap(t)
	// J1: anchor (-1200,0), bbox 300×250, pad 1 at (-1300,40), W=H=20 → make it non-square.
	snap.Components[10].Pads[0].W, snap.Components[10].Pads[0].H = 30, 10
	hash, err := boardSnapshotSemanticSHA256(snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.SemanticSHA256 = hash
	r := simRun(t, snap, lsPose{PrimitiveID: "p-j1", X: fp(-2000), Y: fp(500), Rotation: fp(90)})
	j := simComp(t, r, "J1")
	if j.X != -2000 || j.Y != 500 || j.Rotation != 90 {
		t.Errorf("J1 pose = (%.0f,%.0f,%.0f)", j.X, j.Y, j.Rotation)
	}
	// 90° CCW: bbox 300×250 → 250×300 about the anchor.
	if b := j.BBox; b.MinX != -2125 || b.MaxX != -1875 || b.MinY != 350 || b.MaxY != 650 {
		t.Errorf("J1 bbox = %+v, want x∈[-2125,-1875] y∈[350,650]", *b)
	}
	// pad 1 offset (-100,40) → rotated (-40,-100); extents swap to 10×30.
	if p := j.Pads[0]; p.X != -2040 || p.Y != 400 || p.W != 10 || p.H != 30 {
		t.Errorf("J1 pad 1 = %+v, want (-2040,400) 10×30", p)
	}
	if m, ok := simMoveOf(r, "J1"); !ok || !m.SetRotation || m.Rotation != 90 || m.Via != "pose" {
		t.Errorf("J1 move = %+v", m)
	}
}

// A posed antenna drags its RF chain along.
func TestLayoutSimulate_AntennaPoseMovesChain(t *testing.T) {
	snap := simBoardSnap(t)
	base := simRun(t, snap)
	r := simRun(t, snap, lsPose{PrimitiveID: "p-ant", X: fp(1500), Y: fp(800), Rotation: fp(0)})
	if m, ok := simMoveOf(r, "ANT1"); !ok || m.Via != "pose" || m.Y != 800 {
		t.Fatalf("ANT1 move = %+v", m)
	}
	b, c := simComp(t, base, "C4"), simComp(t, r, "C4")
	if c.Y <= b.Y+100 {
		t.Errorf("C4 y=%.0f did not follow the antenna up (baseline %.0f)", c.Y, b.Y)
	}
	if c.Owner != "U1" || simComp(t, r, "ANT1").Owner != "U1" {
		t.Errorf("owner of chain members/antenna must stay U1: %q %q", c.Owner, simComp(t, r, "ANT1").Owner)
	}
}

func TestLayoutSimulate_PoseValidation(t *testing.T) {
	snap := simBoardSnap(t)
	pose := func(id string, x, y, rot float64) *lsPoseFile {
		return simPoses(snap.SemanticSHA256, lsPose{PrimitiveID: id, X: fp(x), Y: fp(y), Rotation: fp(rot)})
	}
	for _, tc := range []struct {
		name string
		pf   *lsPoseFile
		want string
	}{
		{"satellite", pose("p-c1", 0, 0, 0), "satellite"},
		{"rf chain member", pose("p-l1", 0, 0, 0), "satellite"},
		{"locked part", pose("p-r9", 0, 0, 0), "locked"},
		{"unknown id", pose("nope", 0, 0, 0), "unknown primitiveId"},
		{"rotation 45", pose("p-u1", 0, 0, 45), "multiple of 90"},
		{"non-finite", pose("p-u1", math.Inf(1), 0, 0), "non-finite"},
		{"hash mismatch", simPoses("deadbeef", lsPose{PrimitiveID: "p-u1", X: fp(0), Y: fp(0), Rotation: fp(0)}), "does not match"},
		{"missing field", simPoses(snap.SemanticSHA256, lsPose{PrimitiveID: "p-u1", X: fp(1)}), "needs x, y and rotation"},
		{"duplicate", simPoses(snap.SemanticSHA256, lsPose{PrimitiveID: "p-u1", X: fp(0), Y: fp(0), Rotation: fp(0)}, lsPose{PrimitiveID: "p-u1", X: fp(1), Y: fp(0), Rotation: fp(0)}), "two poses"},
	} {
		if got := simErr(t, snap, tc.pf); !strings.Contains(got, tc.want) {
			t.Errorf("%s: error %q lacks %q", tc.name, got, tc.want)
		}
	}
	// The satellite error names the part.
	if got := simErr(t, snap, pose("p-c1", 0, 0, 0)); !strings.Contains(got, "C1") {
		t.Errorf("the error must name C1: %q", got)
	}
	// Rotations that are multiples of 90 in any form are accepted and normalised.
	for _, rot := range []float64{-90, 270, 450, 360} {
		if _, err := simulatePcbLayout(snap, pose("p-j1", -1200, 0, rot), true); err != nil {
			t.Errorf("rotation %v rejected: %v", rot, err)
		}
	}
	if r, _ := simulatePcbLayout(snap, pose("p-j1", -1200, 0, -90), true); simComp(t, r, "J1").Rotation != 270 {
		t.Errorf("-90 must be normalised to 270, got %v", simComp(t, r, "J1").Rotation)
	}
	// A posed part set back exactly where it is changes nothing.
	r, err := simulatePcbLayout(snap, pose("p-j1", -1200, 0, 0), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := simMoveOf(r, "J1"); ok || simComp(t, r, "J1").Moved {
		t.Error("a pose equal to the snapshot is not a move")
	}
}

func TestLayoutSimulate_RequiresSemanticHash(t *testing.T) {
	snap := simBoardSnap(t)
	snap.SemanticSHA256 = ""
	if got := simErr(t, snap, nil); !strings.Contains(got, "semanticSha256") || !strings.Contains(got, "pcb dump --include-copper") {
		t.Errorf("error %q must ask for a fresh pcb dump --include-copper", got)
	}
}

// Every net with n≥2 pads gets exactly n-1 ratsnest edges; unconnected pads none.
func TestLayoutSimulate_RatsnestIsAMinimumSpanningTreePerNet(t *testing.T) {
	r := simRun(t, simBoardSnap(t))
	pads := map[string]int{}
	for _, c := range r.Components {
		for _, p := range c.Pads {
			if p.Net != "" {
				pads[p.Net]++
			}
		}
	}
	edges := map[string]int{}
	total := 0.0
	for _, e := range r.Ratsnest {
		edges[e.Net]++
		total += math.Hypot(e.A[0]-e.B[0], e.A[1]-e.B[1])
	}
	connected := 0
	for net, n := range pads {
		want := n - 1
		if edges[net] != want {
			t.Errorf("net %s: %d pads → %d edges, want %d", net, n, edges[net], want)
		}
		if n >= 2 {
			connected++
		}
	}
	if connected == 0 || len(edges) != connected {
		t.Errorf("edges for %d nets, but %d nets have two or more pads", len(edges), connected)
	}
	if edges["LA"] != 0 || edges["LB"] != 0 {
		t.Error("a single-pad net has nothing to connect")
	}
	if _, ok := edges[""]; ok {
		t.Error("unconnected pads must not get ratsnest edges")
	}
	if math.Abs(r.Metrics.RatsnestMil-total) > 0.05 {
		t.Errorf("metrics.ratsnestMil=%.2f, sum of edges %.2f", r.Metrics.RatsnestMil, total)
	}
}

func TestLayoutSimulate_CrossingsAndCongestion(t *testing.T) {
	// A crafted X: two nets whose spanning segments cross once.
	x := simBuildRatsnest([]lsPadPt{{"A", 0, 0}, {"A", 100, 100}, {"B", 0, 100}, {"B", 100, 0}})
	if len(x) != 2 || simCountCrossings(x) != 1 {
		t.Fatalf("X: edges=%d crossings=%d, want 2 and 1", len(x), simCountCrossings(x))
	}
	// Same net never counts against itself; touching at a shared pad is not a crossing.
	same := simBuildRatsnest([]lsPadPt{{"A", 0, 0}, {"A", 100, 100}, {"A", 0, 100}, {"A", 100, 0}})
	if simCountCrossings(same) != 0 {
		t.Errorf("same-net segments counted: %d", simCountCrossings(same))
	}
	touch := simBuildRatsnest([]lsPadPt{{"A", 0, 0}, {"A", 100, 0}, {"B", 100, 0}, {"B", 100, 100}})
	if simCountCrossings(touch) != 0 {
		t.Errorf("segments touching at an endpoint counted: %d", simCountCrossings(touch))
	}
	// Congestion: three pads in one 50 mil cell plus a segment through it.
	pads := []lsPadPt{{"A", 10, 10}, {"B", 20, 20}, {"C", 30, 30}}
	rat := []lsRat{{Net: "N", A: [2]float64{-100, 25}, B: [2]float64{100, 25}}}
	if got := simCongestionPeak(pads, rat); got != 4 {
		t.Errorf("congestionPeak=%d, want 3 pads + 1 segment = 4", got)
	}
	// The segment is counted once per cell it touches, not per pad.
	if got := simCongestionPeak(nil, rat); got != 1 {
		t.Errorf("a lone segment peaks at %d, want 1", got)
	}
}

func TestLayoutSimulate_SeverityMapping(t *testing.T) {
	for reason, want := range map[string]string{
		"no free slot on U1's left edge within 2000mil of its pin (keep-outs/board edge/other satellites) — no closer free slot": "P1",
		"no shared net path to a main chip":                                              "P1",
		"no main chip on board (>= mainPins distinct pins) to anchor against":            "P1",
		"placed 140mil from U1.3V3 (limit 100mil) — blocked by D1; move it ~215mil away": "P2",
		"C1 sits 75mil off the RF feed line U1→ANT1 (limit 40mil) — free the straight path between them; no free slot on the line, placed by the generic search": "P2",
		"C1 left the RF feed line U1→ANT1 (no free slot on it; generic search placed it 20mil from it) — free the straight path between them":                    "P2",
		"antenna — mechanical anchor, not moved; place it on the board edge":                                                                                     "P3",
		"group:fixed — virtual PCB group member, not moved (--break-group to override)":                                                                          "P3",
	} {
		if got := simSeverity(reason); got != want {
			t.Errorf("%q → %s, want %s", reason, got, want)
		}
	}
}

// A keep-out in the dump bounds the planner and is echoed in the result.
func TestLayoutSimulate_KeepoutsFromDump(t *testing.T) {
	snap := simBoardSnap(t)
	region := map[string]any{"primitiveId": "ko", "layer": 12, "ruleType": []any{5, 6, 7, 8}, "geometryAvailable": true,
		"source": pointsPolygonSource([][2]float64{{-600, -300}, {-215, -300}, {-215, 300}, {-600, 300}})}
	snap.Copper.Regions = []any{region}
	hash, err := boardSnapshotSemanticSHA256(snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.SemanticSHA256 = hash
	r := simRun(t, snap)
	if len(r.Keepouts) != 1 || r.Keepouts[0].MinX != -600 || r.Keepouts[0].MaxX != -215 {
		t.Fatalf("keepouts = %+v", r.Keepouts)
	}
	// The keep-out covers the 3V3 pin's front, so every cap and the crystal must end
	// up outside it (above/below), and the over-limit placement is reported as P2.
	ko := r.Keepouts[0]
	for _, des := range []string{"C1", "C2", "C3", "Y1"} {
		if c := simComp(t, r, des); apOverlaps(*c.BBox, ko) {
			t.Errorf("%s bbox %+v enters the keep-out %+v", des, *c.BBox, ko)
		}
	}
	p2 := 0
	for _, is := range r.Issues {
		if is.Severity == "P2" && strings.Contains(is.Message, "blocked by keep-out") && is.Fix != "" {
			p2++
		}
	}
	if p2 != 4 {
		t.Errorf("want 4 P2 'blocked by keep-out' issues with a fix, got %d: %+v", p2, r.Issues)
	}
	// Without regions in the dump the planner runs unbounded and says so.
	snap2 := simBoardSnap(t)
	snap2.Copper = nil
	hash, _ = boardSnapshotSemanticSHA256(snap2)
	snap2.SemanticSHA256 = hash
	r2 := simRun(t, snap2)
	said := false
	for _, is := range r2.Issues {
		said = said || (is.Severity == "P3" && strings.Contains(is.Message, "copper.regions"))
	}
	if !said || len(r2.Keepouts) != 0 {
		t.Errorf("a dump without regions must say so (issues %+v)", r2.Issues)
	}
}

func TestLayoutSimulate_DeterministicBytes(t *testing.T) {
	snap := simBoardSnap(t)
	pf := simPoses(snap.SemanticSHA256, lsPose{PrimitiveID: "p-u1", X: fp(500), Y: fp(100), Rotation: fp(90)})
	var first []byte
	for i := 0; i < 5; i++ {
		r, err := simulatePcbLayout(snap, pf, true)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = raw
		} else if !bytes.Equal(first, raw) {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
	if !bytes.Contains(first, []byte(`"schema": "easyeda.pcb.layout-sim/v1"`)) {
		t.Error("schema missing from the output")
	}
}

func simWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func simRunCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := newPcbLayoutSimulateCmd(&out, &errb)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func TestLayoutSimulateCommand_WritesFileAndRefusesAliases(t *testing.T) {
	dir := t.TempDir()
	snap := simBoardSnap(t)
	board, poses, out := filepath.Join(dir, "board.json"), filepath.Join(dir, "poses.json"), filepath.Join(dir, "sub", "sim.json")
	simWriteJSON(t, board, snap)
	simWriteJSON(t, poses, map[string]any{"boardSemanticSha256": snap.SemanticSHA256,
		"poses": []any{map[string]any{"primitiveId": "p-u1", "x": 500, "y": 0, "rotation": 0}}})

	stdout, stderr, err := simRunCmd(t, "--board", board, "--poses", poses, "--out", out)
	if err != nil {
		t.Fatalf("simulate: %v\n%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != out {
		t.Errorf("stdout = %q, want the out path", stdout)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte("{\n  \"schema\": \"easyeda.pcb.layout-sim/v1\"")) {
		t.Errorf("output is not 2-space indented contract JSON: %.80s", raw)
	}
	var res lsResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if m, ok := simMoveOf(&res, "U1"); !ok || m.Via != "pose" {
		t.Errorf("result lacks the U1 pose move: %+v", res.Moves)
	}
	if _, err := os.Stat(out + ".tmp"); err == nil {
		t.Error("temporary file left behind")
	}
	// Re-running yields identical bytes.
	if _, _, err := simRunCmd(t, "--board", board, "--poses", poses, "--out", out); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(out)
	if !bytes.Equal(raw, again) {
		t.Error("two runs of the command differ")
	}

	// Aliasing: --out may overwrite neither the board nor the poses.
	for _, args := range [][]string{{"--board", board, "--out", board}, {"--board", board, "--poses", poses, "--out", poses}} {
		before, _ := os.ReadFile(args[len(args)-1])
		if _, _, err := simRunCmd(t, args...); err == nil || !strings.Contains(err.Error(), "must not overwrite input") {
			t.Errorf("%v: err = %v, want refusal to overwrite the input", args, err)
		}
		if after, _ := os.ReadFile(args[len(args)-1]); !bytes.Equal(before, after) {
			t.Errorf("%v: the input was modified", args)
		}
	}
	// Required flags and bad inputs.
	if _, _, err := simRunCmd(t, "--board", board); err == nil || !strings.Contains(err.Error(), "--board and --out") {
		t.Errorf("missing --out: %v", err)
	}
	noHash := snap
	noHash.SemanticSHA256 = ""
	bad := filepath.Join(dir, "nohash.json")
	simWriteJSON(t, bad, noHash)
	if _, _, err := simRunCmd(t, "--board", bad, "--out", filepath.Join(dir, "x.json")); err == nil || !strings.Contains(err.Error(), "semanticSha256") {
		t.Errorf("a dump without semanticSha256 must be refused, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.json")); err == nil {
		t.Error("a refused run must not write the output")
	}
	simWriteJSON(t, poses, map[string]any{"boardSemanticSha256": "stale", "poses": []any{}})
	if _, _, err := simRunCmd(t, "--board", board, "--poses", poses, "--out", filepath.Join(dir, "y.json")); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("stale poses must be refused, got %v", err)
	}
	// The group registers the subcommand.
	found := false
	for _, c := range newPcbLayoutCmd(&bytes.Buffer{}, &bytes.Buffer{}).Commands() {
		found = found || c.Name() == "simulate"
	}
	if !found {
		t.Error("pcb layout has no simulate subcommand")
	}
}

// ── small-IC mains, owner pins, --keep-unmoved, routing facts ────────────────

func simRehash(t testing.TB, snap *boardSnapshot) {
	t.Helper()
	hash, err := boardSnapshotSemanticSHA256(snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.SemanticSHA256 = hash
}

// simLdoSnap is simBoardSnap plus a 4-pin LDO U2 far to the lower left, its VIN cap
// C6 and 3V3 output cap C7 (3V3 is shared with the MCU U1; C7 is nearer to the
// LDO), a 3V3 cap C8 near the MCU, and a SOT-23 Q1 whose GATE net is the MCU's SIG2.
func simLdoSnap(t testing.TB) *boardSnapshot {
	t.Helper()
	snap := simBoardSnap(t)
	snap.Components = append(snap.Components,
		simPart("p-u2", "U2", "LDO", -2000, -1000, 100, 100, false,
			simPadAt("1", "VIN", -2040, -990), simPadAt("2", "GND", -2040, -1010), simPadAt("3", "3V3", -1960, -1000), simPadAt("4", "", -1960, -980)),
		simTwoPin("p-c6", "C6", "10uF", "VIN", "GND", -1000, -1500),
		simTwoPin("p-c7", "C7", "10uF", "3V3", "GND", -1000, -1400),
		simTwoPin("p-c8", "C8", "100nF", "3V3", "GND", 0, 1500),
		simPart("p-q1", "Q1", "SOT-23", 800, -1500, 60, 40, false, simPadAt("1", "SIG2", 790, -1500), simPadAt("2", "LOAD", 810, -1500), simPadAt("3", "GND", 800, -1510)),
	)
	simRehash(t, snap)
	return snap
}

func simOwnerOf(t *testing.T, r *lsResult, des string) string {
	t.Helper()
	return simComp(t, r, des).Owner
}

func simHasIssue(r *lsResult, sev, des, sub string) bool {
	for _, is := range r.Issues {
		if is.Severity == sev && is.Designator == des && strings.Contains(is.Message, sub) {
			return true
		}
	}
	return false
}

// An LDO (U2, 4 pins) is a main: it keeps its own caps, the SOT-23 Q1 stays a satellite.
func TestLayoutSimulate_SmallICIsMainWithItsOwnCaps(t *testing.T) {
	snap := simLdoSnap(t)
	r := simRun(t, snap)
	if u2 := simComp(t, r, "U2"); u2.Role != "main" || u2.Owner != "U2" || u2.Moved {
		t.Errorf("U2 = role %q owner %q moved %v, want an unmoved main owning itself", u2.Role, u2.Owner, u2.Moved)
	}
	for des, want := range map[string]string{"C6": "U2", "C7": "U2", "C8": "U1", "C1": "U1", "Q1": "U1"} {
		if got := simOwnerOf(t, r, des); got != want {
			t.Errorf("%s owner = %q, want %s", des, got, want)
		}
	}
	if q := simComp(t, r, "Q1"); q.Role != "satellite" {
		t.Errorf("Q1 role = %q, want satellite", q.Role)
	}
	// C6/C7 hug the LDO (bbox ±50 around (-2000,-1000)), not the MCU.
	for _, des := range []string{"C6", "C7"} {
		if c := simComp(t, r, des); math.Hypot(c.X+2000, c.Y+1000) > 300 {
			t.Errorf("%s at (%.0f,%.0f) is not next to the LDO", des, c.X, c.Y)
		}
	}
	// Posing the LDO moves its caps and none of the MCU's. Owners are pinned from the
	// baseline: unpinned, a 3V3 cap's owner is re-derived by nearest pad after the pose
	// (C7 would hop to the MCU), which is exactly the drift --owners exists to stop.
	base := r
	pins := map[string]string{}
	for _, c := range base.Components {
		if c.Role == "satellite" {
			pins[c.Designator] = c.Owner
		}
	}
	r, err := simulatePcbLayoutOpts(snap, simPoses(snap.SemanticSHA256, lsPose{PrimitiveID: "p-u2", X: fp(-2000), Y: fp(1000), Rotation: fp(0)}), true,
		lsSimOptions{Owners: &lsOwnerFile{BoardSemanticSHA256: snap.SemanticSHA256, Owners: pins}})
	if err != nil {
		t.Fatal(err)
	}
	for _, des := range []string{"C6", "C7"} {
		if b, c := simComp(t, base, des), simComp(t, r, des); math.Abs(c.Y-(b.Y+2000)) > 0.01 || math.Abs(c.X-b.X) > 0.01 {
			t.Errorf("%s did not follow the LDO: %.0f,%.0f → %.0f,%.0f", des, b.X, b.Y, c.X, c.Y)
		}
	}
	for _, des := range []string{"C1", "C2", "C3", "C8", "Q1"} {
		if b, c := simComp(t, base, des), simComp(t, r, des); b.X != c.X || b.Y != c.Y {
			t.Errorf("%s moved with the LDO: %.0f,%.0f → %.0f,%.0f", des, b.X, b.Y, c.X, c.Y)
		}
	}
}

func TestLayoutSimulate_OwnersPinSatellites(t *testing.T) {
	snap := simLdoSnap(t)
	run := func(owners map[string]string) *lsResult {
		t.Helper()
		r, err := simulatePcbLayoutOpts(snap, &lsPoseFile{}, false, lsSimOptions{Owners: &lsOwnerFile{BoardSemanticSHA256: snap.SemanticSHA256, Owners: owners}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	base := simRun(t, snap)
	if simOwnerOf(t, base, "C1") != "U1" || simOwnerOf(t, base, "C7") != "U2" {
		t.Fatalf("baseline owners: C1=%s C7=%s", simOwnerOf(t, base, "C1"), simOwnerOf(t, base, "C7"))
	}
	// Keys and values are trimmed and upper-cased; pinned parts go to the pinned main only.
	r := run(map[string]string{" c1 ": "u2", "C7": "U1", "L1": "U2"})
	if simOwnerOf(t, r, "C1") != "U2" || simOwnerOf(t, r, "C7") != "U1" {
		t.Errorf("pinned owners: C1=%s C7=%s, want U2 and U1", simOwnerOf(t, r, "C1"), simOwnerOf(t, r, "C7"))
	}
	if c := simComp(t, r, "C1"); math.Hypot(c.X+2000, c.Y+1000) > 300 {
		t.Errorf("C1 pinned to the LDO sits at (%.0f,%.0f), far from it", c.X, c.Y)
	}
	// RF chain members follow their antenna, whatever the file says.
	if simOwnerOf(t, r, "L1") != "U1" {
		t.Errorf("L1 is an RF chain member; owner = %s, want U1", simOwnerOf(t, r, "L1"))
	}
	// Pins that cannot be honoured: not a main, unknown part, unknown owner → P3, planner decides.
	r = run(map[string]string{"C2": "J1", "ZZ9": "U1", "C3": "U7", "R9": "", "J1": ""}) // empty owner = no pin, no issue
	if simOwnerOf(t, r, "C2") != "U1" || simOwnerOf(t, r, "C3") != "U1" {
		t.Errorf("an unusable pin must fall back to the planner: C2=%s C3=%s", simOwnerOf(t, r, "C2"), simOwnerOf(t, r, "C3"))
	}
	for _, want := range []struct{ des, sub string }{{"C2", "J1 is not a main"}, {"ZZ9", "not on this board"}, {"C3", "U7 is not on this board"}} {
		if !simHasIssue(r, "P3", want.des, want.sub) {
			t.Errorf("missing P3 issue for %s containing %q: %+v", want.des, want.sub, r.Issues)
		}
	}
	for _, is := range r.Issues {
		if is.Designator == "R9" || (is.Designator == "J1" && strings.Contains(is.Message, "owners")) {
			t.Errorf("an empty owner must not raise an owners issue: %+v", is)
		}
	}
	// A pin the part cannot reach (its signal nets have no pad on that main) leaves it
	// unplaced: P1 naming the pinned owner. (Power-only parts reach any main through GND.)
	r = run(map[string]string{"Y1": "U2"})
	if !simHasIssue(r, "P1", "Y1", "pinned owner U2") || r.Metrics.Unplaced != 1 {
		t.Errorf("Y1 pinned to U2 (no XI/XO pad there) must be a P1 naming U2, unplaced=%d: %+v", r.Metrics.Unplaced, r.Issues)
	}
	if y := simComp(t, r, "Y1"); y.Moved || y.Owner != "" {
		t.Errorf("an unplaced satellite stays put with no owner: %+v", y)
	}
	// Hash mismatch is refused like the poses; so are conflicting duplicates.
	_, err := simulatePcbLayoutOpts(snap, &lsPoseFile{}, false, lsSimOptions{Owners: &lsOwnerFile{BoardSemanticSHA256: "stale", Owners: map[string]string{}}})
	if err == nil || !strings.Contains(err.Error(), "owners boardSemanticSha256") || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("stale owners must be refused, got %v", err)
	}
	_, err = simulatePcbLayoutOpts(snap, &lsPoseFile{}, false, lsSimOptions{Owners: &lsOwnerFile{BoardSemanticSHA256: snap.SemanticSHA256, Owners: map[string]string{"c1": "U1", "C1": "U2"}}})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("conflicting duplicate pins must be refused, got %v", err)
	}
	// Without an owners file the result is byte-identical to a run with the flag unset.
	a, _ := json.Marshal(base)
	b, _ := json.Marshal(run(nil))
	if !bytes.Equal(a, b) {
		t.Error("an empty owners file must not change the result")
	}
}

// simTidy applies the no-pose simulation to the dump so every satellite sits at a
// planned slot: the "already laid out" board --keep-unmoved is meant for.
func simTidy(t *testing.T, snap *boardSnapshot) *boardSnapshot {
	t.Helper()
	r := simRun(t, snap)
	byDes := map[string]lsComponent{}
	for _, c := range r.Components {
		byDes[c.Designator] = c
	}
	out := *snap
	out.Components = append([]boardComp(nil), snap.Components...)
	for i, c := range out.Components {
		f := byDes[c.Designator]
		dx, dy := f.X-c.X, f.Y-c.Y
		moved := c
		moved.X, moved.Y, moved.Rotation = f.X, f.Y, f.Rotation
		if c.BBox != nil && f.BBox != nil {
			bb := *f.BBox
			moved.BBox = &bb
		}
		moved.Pads = nil
		for j, p := range c.Pads {
			q := p
			if j < len(f.Pads) {
				q.X, q.Y = f.Pads[j].X, f.Pads[j].Y
			} else {
				q.X, q.Y = p.X+dx, p.Y+dy
			}
			moved.Pads = append(moved.Pads, q)
		}
		out.Components[i] = moved
	}
	simRehash(t, &out)
	return &out
}

func simKeep(t *testing.T, snap *boardSnapshot, owners map[string]string, poses ...lsPose) *lsResult {
	t.Helper()
	so := lsSimOptions{KeepUnmoved: true}
	if owners != nil {
		so.Owners = &lsOwnerFile{BoardSemanticSHA256: snap.SemanticSHA256, Owners: owners}
	}
	r, err := simulatePcbLayoutOpts(snap, simPoses(snap.SemanticSHA256, poses...), len(poses) > 0, so)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func movedSet(r *lsResult) string {
	var d []string
	for _, m := range r.Moves {
		d = append(d, m.Designator)
	}
	return strings.Join(d, ",")
}

func TestLayoutSimulate_KeepUnmoved(t *testing.T) {
	tidy := simTidy(t, simLdoSnap(t))

	// No poses: nothing is disturbed, so nothing moves and nothing is "displaced".
	r := simKeep(t, tidy, nil)
	if len(r.Moves) != 0 {
		t.Errorf("a tidy board with no poses must not move anything, got %s", movedSet(r))
	}
	for _, c := range r.Components {
		if c.DisplacedBy != "" {
			t.Errorf("%s displacedBy %q without any pose", c.Designator, c.DisplacedBy)
		}
	}
	// The owners of the unmoved satellites are still reported.
	if simOwnerOf(t, r, "C7") != "U2" || simOwnerOf(t, r, "C8") != "U1" {
		t.Errorf("owners of staying satellites: C7=%s C8=%s", simOwnerOf(t, r, "C7"), simOwnerOf(t, r, "C8"))
	}

	// Pose the LDO far away: its caps (owner posed) follow; the MCU's satellites stay.
	away := lsPose{PrimitiveID: "p-u2", X: fp(-2000), Y: fp(1000), Rotation: fp(0)}
	r = simKeep(t, tidy, nil, away)
	if got := movedSet(r); got != "C6,C7,U2" {
		t.Errorf("moves = %s, want only the posed LDO and its two caps", got)
	}
	for _, des := range []string{"C6", "C7"} {
		if c := simComp(t, r, des); c.DisplacedBy != "" {
			t.Errorf("%s follows its posed owner and has displacedBy %q", des, c.DisplacedBy)
		}
	}

	// Without the flag every satellite is re-planned from scratch (unchanged behaviour).
	full := simRun(t, tidy, away)
	if len(full.Moves) < len(r.Moves) {
		t.Errorf("full re-plan moved %d parts, fewer than keep-unmoved's %d", len(full.Moves), len(r.Moves))
	}

	// Drop the LDO onto the MCU's 3V3 cap C8 (U2's 100×100 body also overlaps C1): both
	// give way, are displaced by U2, are re-planned around their own owner U1 only
	// and end clear of U2. C2/C3 and the other satellites are not touched.
	c8 := simComp(t, tidy2r(t, tidy), "C8")
	onto := lsPose{PrimitiveID: "p-u2", X: fp(c8.X), Y: fp(c8.Y), Rotation: fp(0)}
	r = simKeep(t, tidy, nil, onto)
	c8n := simComp(t, r, "C8")
	if c8n.DisplacedBy != "U2" || !c8n.Moved || c8n.Owner != "U1" {
		t.Errorf("C8 = displacedBy %q moved %v owner %q, want displaced by U2, moved, owner U1", c8n.DisplacedBy, c8n.Moved, c8n.Owner)
	}
	if u2 := simComp(t, r, "U2"); apOverlaps(*c8n.BBox, *u2.BBox) {
		t.Errorf("C8 %+v still overlaps the posed LDO %+v", *c8n.BBox, *u2.BBox)
	}
	if c1 := simComp(t, r, "C1"); c1.DisplacedBy != "U2" || c1.Owner != "U1" || apOverlaps(*c1.BBox, *simComp(t, r, "U2").BBox) {
		t.Errorf("C1 = displacedBy %q owner %q: want it displaced by U2, still owned by U1, clear of U2", c1.DisplacedBy, c1.Owner)
	}
	if got := movedSet(r); got != "C1,C6,C7,C8,U2" {
		t.Errorf("moves = %s, want the LDO, its caps and the displaced C1, C8", got)
	}
	// Nothing in the final layout overlaps a main/obstacle or another satellite.
	simNoOverlaps(t, r)

	// Pins and --keep-unmoved together: an unreachable pin stays unplaced (it does not hop
	// to another main), a reachable pin sticks to its owner after the pose.
	pinned := simKeep(t, tidy, map[string]string{"Y1": "U2", "C8": "U1"}, onto)
	if !simHasIssue(pinned, "P1", "Y1", "pinned owner U2") || pinned.Metrics.Unplaced != 1 {
		t.Errorf("Y1 pinned to U2 must stay unplaced under --keep-unmoved too, unplaced=%d: %+v", pinned.Metrics.Unplaced, pinned.Issues)
	}
	if c := simComp(t, pinned, "C8"); c.Owner != "U1" || c.DisplacedBy != "U2" {
		t.Errorf("pinned C8 = owner %q displacedBy %q", c.Owner, c.DisplacedBy)
	}

	// Two posed parts on the same stay satellite: the smallest designator by simDesLess wins (J1 < U2).
	two := simKeep(t, tidy, nil, onto, lsPose{PrimitiveID: "p-j1", X: fp(c8.X), Y: fp(c8.Y), Rotation: fp(0)})
	if got := simComp(t, two, "C8").DisplacedBy; got != "J1" {
		t.Errorf("C8 displacedBy = %q, want J1 (smallest designator of the colliders)", got)
	}
}

// tidy2r is the no-pose result of a tidy board (component poses as dumped).
func tidy2r(t *testing.T, tidy *boardSnapshot) *lsResult { return simKeep(t, tidy, nil) }

// simNoOverlaps fails when two component bboxes overlap by area on the same layer
// (the dump fixtures have no deliberate overlaps once planned).
func simNoOverlaps(t *testing.T, r *lsResult) {
	t.Helper()
	for i, a := range r.Components {
		for _, b := range r.Components[i+1:] {
			if a.BBox == nil || b.BBox == nil || a.Layer != b.Layer {
				continue
			}
			if apOverlaps(*a.BBox, *b.BBox) {
				t.Errorf("%s %+v overlaps %s %+v", a.Designator, *a.BBox, b.Designator, *b.BBox)
			}
		}
	}
}

// A freed satellite the packer cannot place counts as unplaced with a P1 naming its owner.
func TestLayoutSimulate_KeepUnmovedUnplacedIsP1NamingOwner(t *testing.T) {
	tidy := simTidy(t, simLdoSnap(t))
	// Keep-out covering the whole board bar a sliver: nothing can be placed anywhere.
	region := map[string]any{"primitiveId": "ko", "layer": 12, "ruleType": []any{5, 6, 7, 8}, "geometryAvailable": true,
		"source": pointsPolygonSource([][2]float64{{-2990, -1990}, {2990, -1990}, {2990, 1990}, {-2990, 1990}})}
	tidy.Copper.Regions = []any{region}
	simRehash(t, tidy)
	c8 := simComp(t, simKeep(t, tidy, nil), "C8")
	// Posing U2 frees its own caps (C6, C7) and, by overlap, C1 and C8: none of the four fits anywhere.
	r := simKeep(t, tidy, nil, lsPose{PrimitiveID: "p-u2", X: fp(c8.X), Y: fp(c8.Y), Rotation: fp(0)})
	if r.Metrics.Unplaced != 4 {
		t.Fatalf("with the whole board keep-out the 4 freed satellites cannot be re-placed, unplaced=%d moves=%s", r.Metrics.Unplaced, movedSet(r))
	}
	// Satellites that stayed are not "unplaced".
	if c2 := simComp(t, r, "C2"); c2.Moved || c2.Owner != "U1" {
		t.Errorf("C2 stays put: %+v", c2)
	}
	found := false
	for _, is := range r.Issues {
		found = found || (is.Severity == "P1" && is.Designator == "C8" && strings.Contains(is.Message, "U1"))
	}
	if !found {
		t.Errorf("want a P1 for C8 naming its owner U1, got %+v", r.Issues)
	}
}

// ── routing facts ────────────────────────────────────────────────────────────

func simLine(id, net string, layer int, locked bool) map[string]any {
	return map[string]any{"primitiveId": id, "net": net, "layer": float64(layer), "startX": 0.0, "startY": 0.0, "endX": 10.0, "endY": 0.0, "lineWidth": 6.0, "locked": locked}
}

// simNetPadXY is the board position of des's first pad on net.
func simNetPadXY(t testing.TB, snap *boardSnapshot, des, net string) (float64, float64) {
	t.Helper()
	for _, c := range snap.Components {
		if c.Designator != des {
			continue
		}
		for _, p := range boardCompToAP(c).pads {
			if p.net == net {
				return p.x, p.y
			}
		}
	}
	t.Fatalf("%s has no pad on %s", des, net)
	return 0, 0
}

func simVia(id, net string, locked bool) map[string]any {
	return map[string]any{"primitiveId": id, "net": net, "x": 0.0, "y": 0.0, "holeDiameter": 12.0, "diameter": 24.0, "locked": locked}
}

func simRoutedSnap(t testing.TB) *boardSnapshot {
	t.Helper()
	snap := simBoardSnap(t)
	yes := true
	snap.Copper = &boardCopperSnapshot{
		Availability:  map[string]string{"routing": "available", "vias": "available", "regions": "available"},
		ArcsAvailable: &yes,
		Regions:       []any{},
		Lines: []any{
			simLine("l1", "GND", 1, false), simLine("l2", "GND", 2, false), simLine("l3", "3V3", 1, false),
			simLine("l4", "GND", 11, false), // board outline: not routing
			simLine("l5", "", 1, false),     // netless copper: in the totals, in no net row
		},
		Arcs: []any{map[string]any{"primitiveId": "a1", "net": "3V3", "layer": 1.0, "startX": 0.0, "startY": 0.0, "endX": 5.0, "endY": 5.0, "arcAngle": 90.0, "lineWidth": 6.0, "locked": false}},
		Vias: []any{simVia("v1", "GND", false), simVia("v2", "3V3", false)},
	}
	simRehash(t, snap)
	return snap
}

func TestLayoutSimulate_RoutingFacts(t *testing.T) {
	r := simRun(t, simRoutedSnap(t))
	got := r.Routing
	if got.Availability != "available" || got.Tracks != 5 || got.Vias != 2 {
		t.Fatalf("routing = %+v, want available, 5 tracks (4 lines + 1 arc on copper), 2 vias", got)
	}
	if len(got.Nets) != 2 || got.Nets[0].Net != "3V3" || got.Nets[1].Net != "GND" {
		t.Fatalf("nets = %+v, want 3V3 then GND", got.Nets)
	}
	g33, gnd := got.Nets[0], got.Nets[1]
	if g33.Tracks != 2 || g33.Vias != 1 || g33.Locked != 0 || gnd.Tracks != 2 || gnd.Vias != 1 {
		t.Errorf("3V3 %+v GND %+v", g33, gnd)
	}
	// Parts with a pad on the net, naturally sorted (C2 < C10-style).
	if strings.Join(g33.Parts, ",") != "C1,C2,C3,U1" {
		t.Errorf("3V3 parts = %v", g33.Parts)
	}
	if strings.Join(gnd.Parts, ",") != "ANT1,C1,C2,C3,C5,J1,U1" {
		t.Errorf("GND parts = %v", gnd.Parts)
	}
	// Pose independent: identical routing whatever is posed.
	moved := simRun(t, simRoutedSnap(t), lsPose{PrimitiveID: "p-u1", X: fp(500), Y: fp(0), Rotation: fp(90)})
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(moved.Routing)
	if !bytes.Equal(a, b) {
		t.Errorf("routing changed with a pose:\n%s\n%s", a, b)
	}
	// JSON shape.
	if !strings.Contains(string(a), `"availability":"available","tracks":5,"vias":2,"nets":[{"net":"3V3","tracks":2,"vias":1,"locked":0,"parts":[`) {
		t.Errorf("routing JSON shape: %s", a)
	}
}

func TestLayoutSimulate_RoutingUnknownIsNeverNoRouting(t *testing.T) {
	yes, no := true, false
	for name, mutate := range map[string]func(*boardSnapshot){
		"no copper":        func(s *boardSnapshot) { s.Copper = nil },
		"routing unread":   func(s *boardSnapshot) { s.Copper.Availability["routing"] = "unknown" },
		"vias unread":      func(s *boardSnapshot) { s.Copper.Availability["vias"] = "unknown" },
		"arcs unavailable": func(s *boardSnapshot) { s.Copper.ArcsAvailable = &no },
	} {
		snap := simRoutedSnap(t)
		mutate(snap)
		simRehash(t, snap)
		r := simRun(t, snap)
		if r.Routing.Availability != "unknown" || r.Routing.Tracks != 0 || r.Routing.Vias != 0 || len(r.Routing.Nets) != 0 {
			t.Errorf("%s: routing = %+v, want unknown with nothing counted", name, r.Routing)
		}
		if raw, _ := json.Marshal(r.Routing); !strings.Contains(string(raw), `"nets":[]`) {
			t.Errorf("%s: nets must serialise as [] not null: %s", name, raw)
		}
		for _, c := range r.Components {
			if c.Reason != "" {
				t.Errorf("%s: %s has reason %q without readable routing", name, c.Designator, c.Reason)
			}
		}
	}
	// Readable and empty is "available" with zero counts (known empty).
	snap := simRoutedSnap(t)
	snap.Copper.Lines, snap.Copper.Arcs, snap.Copper.Vias = []any{}, []any{}, []any{}
	snap.Copper.ArcsAvailable = &yes
	simRehash(t, snap)
	if r := simRun(t, snap); r.Routing.Availability != "available" || r.Routing.Tracks != 0 || len(r.Routing.Nets) != 0 {
		t.Errorf("an empty but readable board: %+v", r.Routing)
	}
}

// A part with a pad on a net that carries a locked track or via is fixed: it
// cannot be posed, is not re-planned, and is an obstacle for the satellites.
func TestLayoutSimulate_LockedRoutingFixesItsParts(t *testing.T) {
	snap := simRoutedSnap(t)
	// A locked via sitting on L1's N1 pad fixes L1 only: C4/C5 share N1 but no
	// locked copper touches their pads.
	via := simVia("v3", "N1", true)
	via["x"], via["y"] = simNetPadXY(t, snap, "L1", "N1")
	snap.Copper.Vias = append(snap.Copper.Vias, via)
	simRehash(t, snap)
	r := simRun(t, snap)
	if n := r.Routing.Nets; len(n) == 0 {
		t.Fatal("no routing nets")
	}
	var n1 lsRoutingNet
	for _, n := range r.Routing.Nets {
		if n.Net == "N1" {
			n1 = n
		}
	}
	if n1.Vias != 1 || n1.Locked != 1 || strings.Join(n1.Parts, ",") != "C4,C5,L1" {
		t.Errorf("N1 = %+v, want 1 locked via and parts C4,C5,L1", n1)
	}
	if c := simComp(t, r, "L1"); c.Role != "fixed" || c.Reason != "locked-routing" || c.Moved {
		t.Errorf("L1 = role %q reason %q moved %v, want fixed/locked-routing/unmoved", c.Role, c.Reason, c.Moved)
	}
	for _, des := range []string{"C4", "C5"} {
		if c := simComp(t, r, des); c.Role == "fixed" || c.Reason != "" {
			t.Errorf("%s = role %q reason %q: a locked via on another pad of N1 must not fix it", des, c.Role, c.Reason)
		}
	}
	// Unrelated parts are untouched: C1 is still a re-planned satellite with no reason.
	if c := simComp(t, r, "C1"); c.Role != "satellite" || c.Reason != "" || !c.Moved {
		t.Errorf("C1 = %+v", c)
	}
	// The part cannot be posed: a satellite never could, so use a connector with a locked route.
	snap2 := simRoutedSnap(t)
	line := simLine("l9", "SIG1", 1, true)
	line["startX"], line["startY"] = simNetPadXY(t, snap2, "J1", "SIG1")
	line["endX"], line["endY"] = simNetPadXY(t, snap2, "U1", "SIG1")
	snap2.Copper.Lines = append(snap2.Copper.Lines, line)
	simRehash(t, snap2)
	r2 := simRun(t, snap2)
	for _, des := range []string{"J1", "U1"} { // both have a SIG1 pad
		if c := simComp(t, r2, des); c.Role != "fixed" || c.Reason != "locked-routing" {
			t.Errorf("%s = role %q reason %q, want fixed/locked-routing", des, c.Role, c.Reason)
		}
	}
	for _, id := range []string{"p-j1", "p-u1"} {
		pf := simPoses(snap2.SemanticSHA256, lsPose{PrimitiveID: id, X: fp(0), Y: fp(0), Rotation: fp(0)})
		if got := simErr(t, snap2, pf); !strings.Contains(got, "part has locked routing") {
			t.Errorf("posing %s: %q lacks \"part has locked routing\"", id, got)
		}
	}
	// An unlocked route fixes nothing.
	if c := simComp(t, simRun(t, simRoutedSnap(t)), "J1"); c.Role != "connector" || c.Reason != "" {
		t.Errorf("unlocked routing must not fix J1: %+v", c)
	}
}

func TestLayoutSimulateCommand_OwnersAndKeepUnmovedFlags(t *testing.T) {
	dir := t.TempDir()
	snap := simLdoSnap(t)
	board, owners, out := filepath.Join(dir, "board.json"), filepath.Join(dir, "owners.json"), filepath.Join(dir, "sim.json")
	simWriteJSON(t, board, snap)
	simWriteJSON(t, owners, map[string]any{"boardSemanticSha256": snap.SemanticSHA256, "owners": map[string]string{"C1": "U2"}})
	if _, stderr, err := simRunCmd(t, "--board", board, "--owners", owners, "--keep-unmoved", "--out", out); err != nil {
		t.Fatalf("simulate: %v\n%s", err, stderr)
	}
	raw, _ := os.ReadFile(out)
	var res lsResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if simOwnerOf(t, &res, "C1") != "U2" {
		t.Errorf("C1 owner = %s, want the pinned U2", simOwnerOf(t, &res, "C1"))
	}
	// A stale owners file is refused and writes nothing.
	simWriteJSON(t, owners, map[string]any{"boardSemanticSha256": "stale", "owners": map[string]string{}})
	bad := filepath.Join(dir, "bad.json")
	if _, _, err := simRunCmd(t, "--board", board, "--owners", owners, "--out", bad); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("stale owners must be refused, got %v", err)
	}
	if _, err := os.Stat(bad); err == nil {
		t.Error("a refused run must not write the output")
	}
	// --out may not overwrite the owners file; unknown fields in it are rejected.
	if _, _, err := simRunCmd(t, "--board", board, "--owners", owners, "--out", owners); err == nil || !strings.Contains(err.Error(), "must not overwrite input") {
		t.Errorf("owners aliasing: %v", err)
	}
	simWriteJSON(t, owners, map[string]any{"boardSemanticSha256": snap.SemanticSHA256, "owners": map[string]string{}, "extra": 1})
	if _, _, err := simRunCmd(t, "--board", board, "--owners", owners, "--out", bad); err == nil || !strings.Contains(err.Error(), "decode owners") {
		t.Errorf("unknown owners field must be refused, got %v", err)
	}
}

// --keep-unmoved with owners and routing is as deterministic as the plain run.
func TestLayoutSimulate_KeepUnmovedDeterministicBytes(t *testing.T) {
	tidy := simTidy(t, simLdoSnap(t))
	c8 := simComp(t, simKeep(t, tidy, nil), "C8")
	var first []byte
	for i := 0; i < 8; i++ {
		r := simKeep(t, tidy, map[string]string{"C8": "U1"},
			lsPose{PrimitiveID: "p-u2", X: fp(c8.X), Y: fp(c8.Y), Rotation: fp(0)},
			lsPose{PrimitiveID: "p-j1", X: fp(c8.X), Y: fp(c8.Y), Rotation: fp(90)})
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = raw
		} else if !bytes.Equal(first, raw) {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
}
