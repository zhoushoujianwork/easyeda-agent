package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ── fixture: four chips crowded in one corner of a 2000×900 board ────────────

// spChip is a 100×100 chip at (cx,cy) with one pad per net, spread along its middle.
func spChip(des string, cx, cy float64, nets ...string) boardComp {
	pads := make([]boardPad, len(nets))
	for i, n := range nets {
		pads[i] = simPadAt(fmt.Sprint(i+1), n, cx-40+20*float64(i), cy)
	}
	return simPart("p-"+strings.ToLower(des), des, "IC", cx, cy, 100, 100, false, pads...)
}

// simSpreadSnap: U1–U3 and U2–U4 are strongly connected (three private nets each);
// U1–U2 share one net. Every chip has a rail cap next to it, so every chip owns
// a satellite. J1 (connector) and ANT1 (antenna) are fixed obstacles on the board,
// R9 is a locked resistor. The four chips start on top of each other.
func simSpreadSnap(t testing.TB) *boardSnapshot {
	t.Helper()
	snap := &boardSnapshot{
		Components: []boardComp{
			spChip("U1", 200, 200, "A1", "A2", "A3", "W12", "V1"),
			spChip("U2", 230, 220, "B1", "B2", "B3", "W12", "V2"),
			spChip("U3", 260, 240, "A1", "A2", "A3", "GND", "V3"),
			spChip("U4", 290, 260, "B1", "B2", "B3", "GND", "V4"),
			simTwoPin("p-c1", "C1", "100nF", "V1", "GND", 200, 300),
			simTwoPin("p-c2", "C2", "100nF", "V2", "GND", 230, 320),
			simTwoPin("p-c3", "C3", "100nF", "V3", "GND", 260, 340),
			simTwoPin("p-c4", "C4", "100nF", "V4", "GND", 290, 360),
			simPart("p-j1", "J1", "HDR-3", 1800, 100, 220, 100, false, simPadAt("1", "A1", 1780, 100), simPadAt("2", "B1", 1800, 100), simPadAt("3", "GND", 1820, 100)),
			simPart("p-ant", "ANT1", "2.4G Antenna", 1850, 800, 100, 60, false, simPadAt("1", "ANT_FEED", 1820, 800), simPadAt("2", "GND", 1880, 800)),
			simPart("p-r9", "R9", "0R", 1500, 600, 60, 30, true, simPadAt("1", "LA", 1480, 600), simPadAt("2", "LB", 1520, 600)),
		},
		Outline:      &boardOutline{Source: "polygon", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 2000, MaxY: 900}, Points: [][2]float64{{0, 0}, {2000, 0}, {2000, 900}, {0, 900}}},
		CopperLayers: 2,
		Copper:       &boardCopperSnapshot{Availability: map[string]string{"regions": "available"}, Regions: []any{}},
	}
	simRehash(t, snap)
	return snap
}

func spreadRun(t *testing.T, snap *boardSnapshot, so lsSimOptions, poses ...lsPose) *lsResult {
	t.Helper()
	so.Spread = true
	if so.Owners == nil && so.KeepUnmoved {
		so.Owners = &lsOwnerFile{BoardSemanticSHA256: snap.SemanticSHA256, Owners: spreadOwners(t, snap, so)}
	}
	r, err := simulatePcbLayoutOpts(snap, simPoses(snap.SemanticSHA256, poses...), len(poses) > 0, so)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// spreadOwners is the owners file Desk starts from: the satellites' owners of a plain run.
func spreadOwners(t *testing.T, snap *boardSnapshot, so lsSimOptions) map[string]string {
	t.Helper()
	so.Spread, so.KeepUnmoved, so.Owners = false, false, nil
	r, err := simulatePcbLayoutOpts(snap, &lsPoseFile{}, false, so)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range r.Components {
		if c.Role == "satellite" && c.Owner != "" {
			out[c.Designator] = c.Owner
		}
	}
	return out
}

func spreadMainsOf(r *lsResult) []lsComponent {
	var out []lsComponent
	for _, c := range r.Components {
		if c.Role == "main" {
			out = append(out, c)
		}
	}
	return out
}

func spreadDist(a, b lsComponent) float64 {
	ax, ay := (a.BBox.MinX+a.BBox.MaxX)/2, (a.BBox.MinY+a.BBox.MaxY)/2
	bx, by := (b.BBox.MinX+b.BBox.MaxX)/2, (b.BBox.MinY+b.BBox.MaxY)/2
	return math.Hypot(ax-bx, ay-by)
}

// spreadLegal asserts the contract of the spread: the mains' bboxes are mutually
// clear, clear (by at least the 40 mil gap) of every part that is neither a main
// nor a satellite, clear of the keep-outs and inside the board outline.
func spreadLegal(t *testing.T, r *lsResult) {
	t.Helper()
	mains := spreadMainsOf(r)
	for i, a := range mains {
		for _, b := range mains[i+1:] {
			if apOverlaps(*a.BBox, *b.BBox) {
				t.Errorf("main %s %+v overlaps main %s %+v", a.Designator, *a.BBox, b.Designator, *b.BBox)
			}
		}
		if a.BBox.MinX < r.Outline.MinX || a.BBox.MinY < r.Outline.MinY || a.BBox.MaxX > r.Outline.MaxX || a.BBox.MaxY > r.Outline.MaxY {
			t.Errorf("main %s %+v is outside the board %+v", a.Designator, *a.BBox, r.Outline)
		}
		for _, ko := range r.Keepouts {
			if apOverlaps(*a.BBox, ko) {
				t.Errorf("main %s %+v enters the keep-out %+v", a.Designator, *a.BBox, ko)
			}
		}
		for _, o := range r.Components {
			if o.Role == "main" || o.Role == "satellite" || o.BBox == nil {
				continue
			}
			if apOverlaps(apInflate(*a.BBox, 40), *o.BBox) {
				t.Errorf("main %s %+v is closer than the gap to %s %s %+v", a.Designator, *a.BBox, o.Role, o.Designator, *o.BBox)
			}
		}
	}
}

func spreadVia(r *lsResult, des string) string {
	m, _ := simMoveOf(r, des)
	return m.Via
}

// ── behaviour ────────────────────────────────────────────────────────────────

func TestLayoutSpread_MainsSeparateAndClearOfObstacles(t *testing.T) {
	snap := simSpreadSnap(t)
	for _, keep := range []bool{false, true} {
		r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: keep})
		if got := len(spreadMainsOf(r)); got != 4 {
			t.Fatalf("keep=%v: %d mains, want 4", keep, got)
		}
		spreadLegal(t, r)
		for _, des := range []string{"U1", "U2", "U3", "U4"} {
			if got := spreadVia(r, des); got != "spread" {
				t.Errorf("keep=%v: %s via = %q, want spread", keep, des, got)
			}
		}
		// Connector, antenna and the locked part stay where the dump put them.
		for _, des := range []string{"J1", "ANT1", "R9"} {
			if c := simComp(t, r, des); c.Moved {
				t.Errorf("keep=%v: %s moved", keep, des)
			}
		}
		for _, is := range r.Issues {
			if is.Severity == "P1" {
				t.Errorf("keep=%v: unexpected P1 %+v", keep, is)
			}
		}
		simNoOverlaps(t, r)
	}
}

// v2 may turn a chip; whatever it picks is a quarter turn and replaces the user's pose.
func TestLayoutSpread_ReplacesMainPoses(t *testing.T) {
	snap := simSpreadSnap(t)
	pose := lsPose{PrimitiveID: "p-u1", X: fp(1000), Y: fp(450), Rotation: fp(90)}
	r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: true}, pose)
	u1 := simComp(t, r, "U1")
	if rot := simNormDeg(u1.Rotation); math.Abs(rot-math.Round(rot/90)*90) > 1e-6 {
		t.Errorf("U1 rotation = %v, want a quarter turn", u1.Rotation)
	}
	if u1.X == 1000 && u1.Y == 450 {
		t.Errorf("a posed main must be replaced by its spread pose, still at the pose (%v,%v)", u1.X, u1.Y)
	}
	if got := spreadVia(r, "U1"); got != "spread" {
		t.Errorf("U1 via = %q, want spread", got)
	}
	spreadLegal(t, r)
}

func TestLayoutSpread_UserPoseOfConnectorIsAnObstacle(t *testing.T) {
	snap := simSpreadSnap(t)
	base := spreadRun(t, snap, lsSimOptions{})
	// Park the connector exactly where the plain spread put U3.
	u3 := simComp(t, base, "U3")
	cx, cy := (u3.BBox.MinX+u3.BBox.MaxX)/2, (u3.BBox.MinY+u3.BBox.MaxY)/2
	r := spreadRun(t, snap, lsSimOptions{}, lsPose{PrimitiveID: "p-j1", X: fp(cx), Y: fp(cy), Rotation: fp(0)})
	if j := simComp(t, r, "J1"); j.X != cx || j.Y != cy {
		t.Errorf("J1 = (%v,%v), want the posed (%v,%v)", j.X, j.Y, cx, cy)
	}
	if got := spreadVia(r, "J1"); got != "pose" {
		t.Errorf("J1 via = %q, want pose", got)
	}
	spreadLegal(t, r)
}

func TestLayoutSpread_StronglyConnectedMainsEndCloser(t *testing.T) {
	// Four chips: the strongly connected pairs U1-U3 and U2-U4 end nearer each
	// other than the weakly connected pairs do on average (v2 judges the real
	// ratsnest, so the strong pair need not be the single nearest one).
	snap := simSpreadSnap(t)
	r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: true})
	strong := map[string]bool{"U1-U3": true, "U2-U4": true}
	des := []string{"U1", "U2", "U3", "U4"}
	weak, nWeak := 0.0, 0
	for i, a := range des {
		for _, b := range des[i+1:] {
			if !strong[a+"-"+b] {
				weak += spreadDist(simComp(t, r, a), simComp(t, r, b))
				nWeak++
			}
		}
	}
	weak /= float64(nWeak)
	for _, pair := range [][2]string{{"U1", "U3"}, {"U2", "U4"}} {
		if d := spreadDist(simComp(t, r, pair[0]), simComp(t, r, pair[1])); d >= weak {
			t.Errorf("%s-%s are %.0f mil apart, weak pairs %.0f on average", pair[0], pair[1], d, weak)
		}
	}

	// Three chips: U1 and U3 share three nets, U2 none. In designator order U2 would
	// separate them; spread puts them next to each other and U2 at the end.
	snap = simSpreadSnap(t)
	snap.Components = []boardComp{
		spChip("U1", 200, 200, "A1", "A2", "A3", "V1"),
		spChip("U2", 230, 220, "B1", "V2"),
		spChip("U3", 260, 240, "A1", "A2", "A3", "V3"),
		simTwoPin("p-c1", "C1", "100nF", "V1", "GND", 200, 300),
		simTwoPin("p-c2", "C2", "100nF", "V2", "GND", 230, 320),
		simTwoPin("p-c3", "C3", "100nF", "V3", "GND", 260, 340),
	}
	simRehash(t, snap)
	r = spreadRun(t, snap, lsSimOptions{KeepUnmoved: true})
	u1, u2, u3 := simComp(t, r, "U1"), simComp(t, r, "U2"), simComp(t, r, "U3")
	if strong, far := spreadDist(u1, u3), math.Max(spreadDist(u1, u2), spreadDist(u3, u2)); strong >= far {
		t.Errorf("U1-U3 (%.0f mil) must be nearer than U2 is to the far one of them (%.0f)", strong, far)
	}
	spreadLegal(t, r)
}

// Identical inputs give identical bytes, and the dump's part order does not matter.
func TestLayoutSpread_DeterministicBytes(t *testing.T) {
	snap := simSpreadSnap(t)
	var first []byte
	for i := 0; i < 5; i++ {
		raw, err := json.MarshalIndent(spreadRun(t, snap, lsSimOptions{KeepUnmoved: true}), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = raw
		} else if !bytes.Equal(first, raw) {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
	// The satellite planner breaks some ties by dump order (a known limit of the
	// keep-unmoved pass), and every spread candidate is judged by it, so a
	// reversed dump may settle elsewhere; it must still be legal and never worse.
	rev := *snap
	rev.Components = append([]boardComp(nil), snap.Components...)
	for i, j := 0, len(rev.Components)-1; i < j; i, j = i+1, j-1 {
		rev.Components[i], rev.Components[j] = rev.Components[j], rev.Components[i]
	}
	simRehash(t, &rev)
	b := spreadRun(t, &rev, lsSimOptions{KeepUnmoved: true})
	spreadLegal(t, b)
	if b.Spread == nil || b.Spread.After.Score > b.Spread.Before.Score {
		t.Errorf("reversed dump: spread made the board worse: %+v", b.Spread)
	}
}

// The contract Desk relies on: keeping the spread poses of the mains as ordinary
// poses and running again without --spread (same owners, keep-unmoved) gives the
// same layout. The only difference is the label of the mains' moves.
func TestLayoutSpread_EqualsPosedRunWithoutSpread(t *testing.T) {
	for name, snap := range map[string]*boardSnapshot{"spread": simSpreadSnap(t), "ldo": simLdoSnap(t)} {
		for _, withPoses := range []bool{false, true} {
			var user []lsPose
			if withPoses {
				switch name {
				case "spread":
					user = []lsPose{{PrimitiveID: "p-j1", X: fp(1200), Y: fp(700), Rotation: fp(90)}, {PrimitiveID: "p-u2", X: fp(900), Y: fp(450), Rotation: fp(0)}}
				default:
					user = []lsPose{{PrimitiveID: "p-j1", X: fp(-1500), Y: fp(500), Rotation: fp(90)}}
				}
			}
			owners := spreadOwners(t, snap, lsSimOptions{})
			so := lsSimOptions{KeepUnmoved: true, Owners: &lsOwnerFile{BoardSemanticSHA256: snap.SemanticSHA256, Owners: owners}}
			first := spreadRun(t, snap, so, user...)

			// Desk keeps the connectors' user poses and the mains' final poses.
			again := append([]lsPose(nil), user...)
			posed := map[string]bool{}
			for _, p := range again {
				posed[p.PrimitiveID] = true
			}
			for _, c := range spreadMainsOf(first) {
				if posed[c.PrimitiveID] { // a user pose of a main is replaced by its spread pose
					for i := range again {
						if again[i].PrimitiveID == c.PrimitiveID {
							again[i] = lsPose{PrimitiveID: c.PrimitiveID, X: fp(c.X), Y: fp(c.Y), Rotation: fp(c.Rotation)}
						}
					}
					continue
				}
				again = append(again, lsPose{PrimitiveID: c.PrimitiveID, X: fp(c.X), Y: fp(c.Y), Rotation: fp(c.Rotation)})
			}
			so2 := so
			so2.Spread = false
			second, err := simulatePcbLayoutOpts(snap, simPoses(snap.SemanticSHA256, again...), true, so2)
			if err != nil {
				t.Fatal(err)
			}
			for i := range first.Moves {
				if first.Moves[i].Via == "spread" {
					first.Moves[i].Via = "pose"
				}
			}
			for _, part := range []struct {
				what string
				a, b any
			}{{"components", first.Components, second.Components}, {"moves", first.Moves, second.Moves}, {"ratsnest", first.Ratsnest, second.Ratsnest}, {"metrics", first.Metrics, second.Metrics}} {
				ja, _ := json.Marshal(part.a)
				jb, _ := json.Marshal(part.b)
				if !bytes.Equal(ja, jb) {
					t.Errorf("%s/poses=%v: %s differ between the spread run and the posed run", name, withPoses, part.what)
				}
			}
			if len(second.Moves) == 0 {
				t.Errorf("%s: the run moved nothing, the comparison proves nothing", name)
			}
		}
	}
}

func TestLayoutSpread_LockedFixedAndExcludedMainsStay(t *testing.T) {
	// U1 is high-pin and excluded: it stays put as an obstacle; U2 (LDO) is spread.
	snap := simLdoSnap(t)
	r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: true, ExcludeMain: map[string]bool{"U1": true}})
	if u1 := simComp(t, r, "U1"); u1.Moved || u1.Role == "main" {
		t.Errorf("an excluded main must stay: %+v", u1)
	}
	u2 := simComp(t, r, "U2")
	if !u2.Moved || spreadVia(r, "U2") != "spread" {
		t.Errorf("U2 must be spread: moved %v via %q", u2.Moved, spreadVia(r, "U2"))
	}
	if u1 := simComp(t, r, "U1"); apOverlaps(apInflate(*u2.BBox, 40), *u1.BBox) {
		t.Errorf("U2 %+v is within the gap of the excluded U1 %+v", *u2.BBox, *u1.BBox)
	}

	// A locked main stays; the others are spread around it.
	snap = simSpreadSnap(t)
	for i := range snap.Components {
		if snap.Components[i].Designator == "U2" {
			snap.Components[i].Locked = true
		}
	}
	simRehash(t, snap)
	r = spreadRun(t, snap, lsSimOptions{KeepUnmoved: true})
	if u2 := simComp(t, r, "U2"); u2.Moved || u2.Role != "main" {
		t.Errorf("a locked main must not move: %+v", u2)
	}
	for _, des := range []string{"U1", "U3", "U4"} {
		c := simComp(t, r, des)
		if apOverlaps(apInflate(*c.BBox, 40), *simComp(t, r, "U2").BBox) {
			t.Errorf("%s %+v is within the gap of the locked U2", des, *c.BBox)
		}
	}
	spreadLegal(t, r)
}

func TestLayoutSpread_LockedRoutingMainStays(t *testing.T) {
	snap := simSpreadSnap(t)
	yes := true
	line := simLine("l9", "A1", 1, true)
	line["startX"], line["startY"] = simNetPadXY(t, snap, "U3", "A1")
	line["endX"], line["endY"] = line["startX"], line["startY"]
	snap.Copper = &boardCopperSnapshot{
		Availability: map[string]string{"routing": "available", "vias": "available", "regions": "available"}, ArcsAvailable: &yes,
		Regions: []any{}, Lines: []any{line}, Vias: []any{},
	}
	simRehash(t, snap)
	r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: true})
	if u3 := simComp(t, r, "U3"); u3.Moved || u3.Role != "fixed" || u3.Reason != "locked-routing" {
		t.Errorf("U3 with locked routing must stay fixed: %+v", u3)
	}
	for _, des := range []string{"U1", "U2", "U4"} {
		if c := simComp(t, r, des); !c.Moved || spreadVia(r, des) != "spread" || apOverlaps(apInflate(*c.BBox, 40), *simComp(t, r, "U3").BBox) {
			t.Errorf("%s = moved %v via %q bbox %+v: must be spread clear of the fixed U3", des, c.Moved, spreadVia(r, des), *c.BBox)
		}
	}
}

func TestLayoutSpread_KeepoutsAreAvoided(t *testing.T) {
	snap := simSpreadSnap(t)
	// A keep-out over the whole lower-left of the board, where the shelf starts.
	snap.Copper.Regions = []any{map[string]any{"primitiveId": "ko", "layer": 12, "ruleType": []any{5, 6, 7, 8}, "geometryAvailable": true,
		"source": pointsPolygonSource([][2]float64{{0, 0}, {900, 0}, {900, 500}, {0, 500}})}}
	simRehash(t, snap)
	r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: true})
	if len(r.Keepouts) != 1 {
		t.Fatalf("keepouts = %+v", r.Keepouts)
	}
	spreadLegal(t, r)
	for _, des := range []string{"U1", "U2", "U3", "U4"} {
		if spreadVia(r, des) != "spread" {
			t.Errorf("%s was not spread", des)
		}
	}
}

func TestLayoutSpread_DoesNotFitLeavesMainsInPlace(t *testing.T) {
	snap := simSpreadSnap(t)
	snap.Outline = &boardOutline{Source: "polygon", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 300, MaxY: 300}, Points: [][2]float64{{0, 0}, {300, 0}, {300, 300}, {0, 300}}}
	simRehash(t, snap)
	for _, keep := range []bool{false, true} {
		r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: keep})
		if !simHasIssue(r, "P1", "", "spread: the main chips do not fit on the board; left in place") {
			t.Errorf("keep=%v: want the P1 does-not-fit issue, got %+v", keep, r.Issues)
		}
		for _, c := range spreadMainsOf(r) {
			if c.Moved {
				t.Errorf("keep=%v: %s moved although nothing fits", keep, c.Designator)
			}
		}
		for _, m := range r.Moves {
			if m.Via == "spread" {
				t.Errorf("keep=%v: %s has a spread move", keep, m.Designator)
			}
		}
	}
}

// When the ring that leaves room for the satellites does not fit, plain bboxes
// plus 2·gap are tried before giving up.
func TestLayoutSpread_FallsBackToPlainBBoxes(t *testing.T) {
	snap := simSpreadSnap(t)
	// Two chips with four big caps each: ring 100 → footprints 300×300 do not fit on
	// a 500×400 board, the 180×180 fallback footprints do.
	snap.Components = []boardComp{
		spChip("U1", 100, 100, "A1", "A2", "A3", "V1"),
		spChip("U2", 120, 110, "A1", "A2", "A3", "V2"),
		simTwoPin("p-c1", "C1", "10uF", "V1", "GND", 100, 200), simTwoPin("p-c2", "C2", "10uF", "V1", "GND", 100, 240),
		simTwoPin("p-c3", "C3", "10uF", "V2", "GND", 120, 280), simTwoPin("p-c4", "C4", "10uF", "V2", "GND", 120, 320),
	}
	snap.Outline = &boardOutline{Source: "polygon", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 500, MaxY: 400}, Points: [][2]float64{{0, 0}, {500, 0}, {500, 400}, {0, 400}}}
	simRehash(t, snap)
	r := spreadRun(t, snap, lsSimOptions{})
	for _, is := range r.Issues {
		if strings.HasPrefix(is.Message, "spread:") {
			t.Errorf("the fallback must succeed quietly, got %+v", is)
		}
	}
	for _, des := range []string{"U1", "U2"} {
		if spreadVia(r, des) != "spread" {
			t.Errorf("%s was not spread (via %q)", des, spreadVia(r, des))
		}
	}
	spreadLegal(t, r)
}

func TestLayoutSpread_NoMainIsAP3NoOp(t *testing.T) {
	snap := simSpreadSnap(t)
	r := spreadRun(t, snap, lsSimOptions{ExcludeMain: map[string]bool{"U1": true, "U2": true, "U3": true, "U4": true}})
	if !simHasIssue(r, "P3", "", "spread: no main chip to spread") {
		t.Errorf("want the P3 no-main issue, got %+v", r.Issues)
	}
	for _, m := range r.Moves {
		if m.Via == "spread" {
			t.Errorf("%s has a spread move", m.Designator)
		}
	}
}

func TestLayoutSpread_ManyMainsFitAndStayDeterministic(t *testing.T) {
	snap := simSpreadSnap(t)
	snap.Components = nil
	for i := 1; i <= 12; i++ {
		snap.Components = append(snap.Components, spChip(fmt.Sprintf("U%d", i), 300+float64(i), 300, fmt.Sprintf("N%d", i), fmt.Sprintf("N%d", (i%12)+1), "GND"))
	}
	snap.Outline = &boardOutline{Source: "polygon", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 1200, MaxY: 800}, Points: [][2]float64{{0, 0}, {1200, 0}, {1200, 800}, {0, 800}}}
	simRehash(t, snap)
	a, b := spreadRun(t, snap, lsSimOptions{}), spreadRun(t, snap, lsSimOptions{})
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !bytes.Equal(ja, jb) {
		t.Error("two runs differ")
	}
	if len(spreadMainsOf(a)) != 12 {
		t.Fatalf("mains = %d", len(spreadMainsOf(a)))
	}
	spreadLegal(t, a)
	for _, is := range a.Issues {
		if is.Severity == "P1" {
			t.Errorf("unexpected %+v", is)
		}
	}
	// A ring of 12 chips (N_i shared with N_i+1): neighbours are closer than opposite chips.
	if d1, d2 := spreadDist(simComp(t, a, "U1"), simComp(t, a, "U2")), spreadDist(simComp(t, a, "U1"), simComp(t, a, "U7")); d1 >= d2 {
		t.Errorf("U1-U2 (%.0f) should be nearer than U1-U7 (%.0f)", d1, d2)
	}
}

func TestLayoutSpread_RingFormula(t *testing.T) {
	// Area of the ring is 2.5× the satellites' bbox area.
	w, h, area := 100.0, 60.0, 4000.0
	r := spreadRing(w, h, area, 5, 10)
	if r <= 15 {
		t.Fatalf("the area term must dominate here, r = %v", r)
	}
	if got := (w+2*r)*(h+2*r) - w*h; math.Abs(got-2.5*area) > 1e-6 {
		t.Errorf("ring area = %v, want %v (r=%v)", got, 2.5*area, r)
	}
	// Never narrower than the largest satellite side plus the gap, or the gap itself.
	if r := spreadRing(w, h, 100, 80, 40); r != 120 {
		t.Errorf("ring = %v, want 120 (largest side 80 + gap 40)", r)
	}
	if r := spreadRing(w, h, 0, 0, 40); r != 40 {
		t.Errorf("ring without satellites = %v, want the gap 40", r)
	}
}

func TestLayoutSpreadCommand_Flag(t *testing.T) {
	dir := t.TempDir()
	snap := simSpreadSnap(t)
	board, out := filepath.Join(dir, "board.json"), filepath.Join(dir, "sim.json")
	simWriteJSON(t, board, snap)
	if _, stderr, err := simRunCmd(t, "--board", board, "--keep-unmoved", "--spread", "--out", out); err != nil {
		t.Fatalf("simulate --spread: %v\n%s", err, stderr)
	}
	var res lsResult
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if spreadVia(&res, "U1") != "spread" {
		t.Errorf("U1 via = %q, want spread", spreadVia(&res, "U1"))
	}
	spreadLegal(t, &res)
	// Without the flag nothing is spread.
	plain := filepath.Join(dir, "plain.json")
	if _, stderr, err := simRunCmd(t, "--board", board, "--keep-unmoved", "--out", plain); err != nil {
		t.Fatalf("simulate: %v\n%s", err, stderr)
	}
	var pr lsResult
	raw, err = os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatal(err)
	}
	for _, m := range pr.Moves {
		if m.Via == "spread" {
			t.Errorf("%s spread without --spread", m.Designator)
		}
	}
	// The mains' order in the result is independent of the map order.
	des := []string{}
	for _, c := range spreadMainsOf(&res) {
		des = append(des, c.Designator)
	}
	if !sort.StringsAreSorted(des) {
		t.Errorf("components not sorted: %v", des)
	}
}

// An L-shaped outline: the notch in the lower left is not board, so no main may
// reach into it even though its bbox is inside the outline's bbox (the shelf
// starts in that corner and has to skip right past it).
func TestLayoutSpread_RespectsPolygonOutline(t *testing.T) {
	snap := simSpreadSnap(t)
	l := [][2]float64{{1300, 0}, {2000, 0}, {2000, 900}, {0, 900}, {0, 600}, {1300, 600}}
	snap.Outline = &boardOutline{Source: "polygon", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 2000, MaxY: 900}, Points: l}
	snap.Components = snap.Components[:8] // the four chips and their caps
	simRehash(t, snap)
	r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: true})
	spreadLegal(t, r)
	for _, c := range spreadMainsOf(r) {
		if !simplePolygonContainsBBox(l, *c.BBox) {
			t.Errorf("main %s %+v is outside the L-shaped outline", c.Designator, *c.BBox)
		}
		if c.BBox.MinX < 1300 && c.BBox.MinY < 600 {
			t.Errorf("main %s %+v sits in the notch", c.Designator, *c.BBox)
		}
		if spreadVia(r, c.Designator) != "spread" {
			t.Errorf("%s was not spread", c.Designator)
		}
	}
}

// v2 reports itself, and a plain run carries no summary.
func TestLayoutSpread_Summary(t *testing.T) {
	snap := simSpreadSnap(t)
	r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: true})
	if r.Spread == nil || r.Spread.Evaluations < 2 {
		t.Fatalf("spread summary missing or no search: %+v", r.Spread)
	}
	if !r.Spread.Improved || r.Spread.After.Score >= r.Spread.Before.Score {
		t.Errorf("four stacked chips must improve: %+v", r.Spread)
	}
	if r.Spread.After.RatsnestMil != r.Metrics.RatsnestMil || r.Spread.After.Crossings != r.Metrics.Crossings {
		t.Errorf("summary after %+v does not describe the result %+v", r.Spread.After, r.Metrics)
	}
	plain, err := simulatePcbLayoutOpts(snap, &lsPoseFile{}, false, lsSimOptions{KeepUnmoved: true})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Spread != nil {
		t.Errorf("a run without --spread must not carry a summary")
	}
}

// Spreading the board a spread produced never makes it worse; when nothing better
// is found the chips stay and a P3 says so.
func TestLayoutSpread_NeverWorse(t *testing.T) {
	snap := simSpreadSnap(t)
	r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: true})
	applied := *snap
	applied.Components = append([]boardComp(nil), snap.Components...)
	for i, c := range applied.Components {
		f := simComp(t, r, c.Designator)
		c.X, c.Y, c.Rotation = f.X, f.Y, f.Rotation
		c.BBox = f.BBox
		c.Pads = append([]boardPad(nil), c.Pads...)
		for j := range c.Pads {
			for _, p := range f.Pads {
				if p.Num == c.Pads[j].Number {
					c.Pads[j].X, c.Pads[j].Y = p.X, p.Y
				}
			}
		}
		applied.Components[i] = c
	}
	simRehash(t, &applied)
	again := spreadRun(t, &applied, lsSimOptions{KeepUnmoved: true})
	if again.Spread == nil || again.Spread.After.Score > again.Spread.Before.Score {
		t.Fatalf("second spread made it worse: %+v", again.Spread)
	}
	if !again.Spread.Improved {
		for _, m := range again.Moves {
			if m.Via == "spread" {
				t.Errorf("no improvement but %s moved", m.Designator)
			}
		}
		found := false
		for _, is := range again.Issues {
			found = found || is.Message == spreadNoBetterMessage
		}
		if !found {
			t.Errorf("no improvement must say so: %+v", again.Issues)
		}
	}
}

// A chip whose pins face away from the (locked) connector they serve is turned round.
func TestLayoutSpread_UsesRotation(t *testing.T) {
	var pads []boardPad
	for i, n := range []string{"S1", "S2", "S3", "S4"} {
		pads = append(pads, simPadAt(fmt.Sprint(i+1), n, 300-45, 400-45+30*float64(i)))
	}
	pads = append(pads, simPadAt("5", "VCC", 300, 400+45), simPadAt("6", "GND", 300+45, 400+45), simPadAt("7", "", 300+45, 400), simPadAt("8", "", 300+45, 400-45))
	snap := &boardSnapshot{
		Components: []boardComp{
			simPart("p-u1", "U1", "IC", 300, 400, 100, 100, false, pads...),
			simTwoPin("p-c1", "C1", "100nF", "VCC", "GND", 300, 520),
			simPart("p-j1", "J1", "HDR-4", 1900, 400, 60, 160, true,
				simPadAt("1", "S1", 1900, 340), simPadAt("2", "S2", 1900, 380), simPadAt("3", "S3", 1900, 420), simPadAt("4", "S4", 1900, 460)),
		},
		Outline:      &boardOutline{Source: "polygon", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 2000, MaxY: 800}, Points: [][2]float64{{0, 0}, {2000, 0}, {2000, 800}, {0, 800}}},
		CopperLayers: 2,
		Copper:       &boardCopperSnapshot{Availability: map[string]string{"regions": "available"}, Regions: []any{}},
	}
	simRehash(t, snap)
	r := spreadRun(t, snap, lsSimOptions{KeepUnmoved: true})
	if r.Spread == nil || !r.Spread.Improved {
		t.Fatalf("expected an improvement: %+v", r.Spread)
	}
	if u1 := simComp(t, r, "U1"); simNormDeg(u1.Rotation) == 0 {
		t.Errorf("U1 kept rotation 0 although its signal pins face away from J1 (at %v,%v)", u1.X, u1.Y)
	}
}
