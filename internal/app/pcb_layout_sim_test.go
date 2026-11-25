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
