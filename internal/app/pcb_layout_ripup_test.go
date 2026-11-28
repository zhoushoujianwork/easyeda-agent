package app

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// ── tiny boards for the rule (one net "N", top layer, 10 mil tracks, 20 mil pads) ──

func rpTrack(id string, x1, y1, x2, y2 float64) ripupElem {
	return ripupElem{ID: id, Net: "N", Kind: "track", Layer: 1, P: [2][2]float64{{x1, y1}, {x2, y2}}, Width: 10,
		Curve: []netPathPoint{{x: x1, y: y1}, {x: x2, y: y2}}}
}

func rpVia(id string, x, y float64) ripupElem {
	return ripupElem{ID: id, Net: "N", Kind: "via", P: [2][2]float64{{x, y}}, Width: 24}
}

func rpPad(comp string, x, y float64) ripupPad {
	return ripupPad{Comp: comp, Net: "N", X: x, Y: y, Layer: 1, W: 20, H: 20}
}

func rpCleared(elems []ripupElem, pads []ripupPad, moved ...string) string {
	m := map[string]bool{}
	for _, id := range moved {
		m[id] = true
	}
	var ids []string
	for _, e := range ripupClear(elems, pads, m) {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func TestRipup_Rule(t *testing.T) {
	locked := rpTrack("L", 100, 0, 200, 0)
	locked.Locked = true
	other := rpTrack("X", 50, -50, 50, 50)
	other.Net = "OTHER"
	netless := rpTrack("Z", 0, 0, 0, 80)
	netless.Net = ""
	cases := []struct {
		name  string
		elems []ripupElem
		pads  []ripupPad
		moved []string
		want  string
	}{
		{"single track moved → unmoved", []ripupElem{rpTrack("t1", 0, 0, 100, 0)},
			[]ripupPad{rpPad("M", 0, 0), rpPad("U", 100, 0)}, []string{"M"}, "t1"},
		{"nothing moved", []ripupElem{rpTrack("t1", 0, 0, 100, 0)},
			[]ripupPad{rpPad("M", 0, 0), rpPad("U", 100, 0)}, nil, ""},
		{"moved pad → via → track → unmoved pad",
			[]ripupElem{rpTrack("t1", 0, 0, 50, 0), rpVia("v1", 50, 0), rpTrack("t2", 50, 0, 100, 0)},
			[]ripupPad{rpPad("M", 0, 0), rpPad("U", 100, 0)}, []string{"M"}, "t1,t2,v1"},
		{"T-junction keeps the A–B path",
			[]ripupElem{rpTrack("a", -100, 0, 0, 0), rpTrack("b", 0, 0, 100, 0), rpTrack("stub", 0, 0, 0, 100)},
			[]ripupPad{rpPad("A", -100, 0), rpPad("B", 100, 0), rpPad("M", 0, 100)}, []string{"M"}, "stub"},
		{"stub ending on a track body (no split)",
			[]ripupElem{rpTrack("ab", -100, 0, 100, 0), rpTrack("stub", 0, 0, 0, 100)},
			[]ripupPad{rpPad("A", -100, 0), rpPad("B", 100, 0), rpPad("M", 0, 100)}, []string{"M"}, "stub"},
		{"locked copper stays, path to it goes",
			[]ripupElem{rpTrack("t1", 0, 0, 100, 0), locked, rpTrack("t2", 200, 0, 300, 0)},
			[]ripupPad{rpPad("M", 0, 0), rpPad("U", 300, 0)}, []string{"M"}, "t1"},
		{"other net and netless copper untouched",
			[]ripupElem{rpTrack("t1", 0, 0, 100, 0), other, netless},
			[]ripupPad{rpPad("M", 0, 0)}, []string{"M"}, "t1"},
		{"loop hanging on the moved pad only",
			[]ripupElem{rpTrack("l1", 0, 0, 100, 0), rpTrack("l2", 100, 0, 100, 100), rpTrack("l3", 100, 100, 0, 0)},
			[]ripupPad{rpPad("M", 0, 0)}, []string{"M"}, "l1,l2,l3"},
		{"ring on a stub from the moved pad",
			[]ripupElem{rpTrack("s", 0, 0, 100, 0), rpTrack("r1", 100, 0, 200, 0), rpTrack("r2", 200, 0, 200, 100),
				rpTrack("r3", 200, 100, 100, 100), rpTrack("r4", 100, 100, 100, 0)},
			[]ripupPad{rpPad("M", 0, 0)}, []string{"M"}, "r1,r2,r3,r4,s"},
		{"dangling copper elsewhere untouched",
			[]ripupElem{rpTrack("t1", 0, 0, 100, 0), rpTrack("far", 500, 500, 600, 500)},
			[]ripupPad{rpPad("M", 0, 0), rpPad("U", 100, 0)}, []string{"M"}, "t1"},
		{"web between unmoved parts stays",
			[]ripupElem{rpTrack("ab", 0, 0, 100, 0), rpTrack("bc", 100, 0, 200, 0)},
			[]ripupPad{rpPad("A", 0, 0), rpPad("B", 100, 0), rpPad("C", 200, 0), rpPad("M", 400, 0)}, []string{"M"}, ""},
		{"both ends moved (two moved parts)", []ripupElem{rpTrack("t1", 0, 0, 100, 0)},
			[]ripupPad{rpPad("M", 0, 0), rpPad("M2", 100, 0)}, []string{"M", "M2"}, "t1"},
		{"track ending off-centre inside the pad touches",
			[]ripupElem{rpTrack("t1", 8, 6, 100, 0)},
			[]ripupPad{rpPad("M", 0, 0), rpPad("U", 100, 0)}, []string{"M"}, "t1"},
		{"track ending 5 mil outside the pad does not touch",
			// pad edge at x=10, track half width 5: the end at x=20 is 5 mil beyond the grown pad.
			[]ripupElem{rpTrack("t1", 20, 0, 100, 0)},
			[]ripupPad{rpPad("M", 0, 0), rpPad("U", 100, 0)}, []string{"M"}, ""},
		{"track on another layer does not touch a top pad",
			[]ripupElem{func() ripupElem { e := rpTrack("t1", 0, 0, 100, 0); e.Layer = 2; return e }()},
			[]ripupPad{rpPad("M", 0, 0), rpPad("U", 100, 0)}, []string{"M"}, ""},
		{"embedded copper of a moved part is never listed",
			[]ripupElem{func() ripupElem { e := rpVia("Me1", 0, 0); e.Owner = "M"; return e }(), rpTrack("t1", 0, 0, 100, 0)},
			[]ripupPad{rpPad("U", 100, 0)}, []string{"M"}, "t1"},
	}
	for _, c := range cases {
		if got := rpCleared(c.elems, c.pads, c.moved...); got != c.want {
			t.Errorf("%s: cleared %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRipup_RotatedShapedPad(t *testing.T) {
	// A 40×10 RECT pad rotated 90°: its copper runs along y. An end at (0,15) is on it,
	// an end at (15,0) is not (it would be on the unrotated pad).
	pad := ripupPad{Comp: "M", Net: "N", X: 0, Y: 0, Layer: 1, W: 40, H: 10, shaped: true,
		node: pcbNetPathNode{kind: "pad", net: "N", layer: 1, w: 40, h: 10, rotation: 90, shape: "RECT"}}
	on := rpTrack("on", 0, 15, 0, 200)
	off := rpTrack("off", 15, 0, 200, 0)
	if got := rpCleared([]ripupElem{on, off}, []ripupPad{pad}, "M"); got != "on" {
		t.Fatalf("cleared %q, want on", got)
	}
}

// ── through simulate ─────────────────────────────────────────────────────────

func rpLine(id, net string, x1, y1, x2, y2 float64, locked bool) map[string]any {
	return map[string]any{"primitiveId": id, "net": net, "layer": 1.0, "startX": x1, "startY": y1, "endX": x2, "endY": y2, "lineWidth": 10.0, "locked": locked}
}

// rpRoutedSnap: U1.RF (190,0) → L1.1 (2480,400) routed with a via in the middle,
// a GND track between ANT1.2 (1550,0) and J1.3 (-1300,-40), a GND pour.
func rpRoutedSnap(t *testing.T) *boardSnapshot {
	t.Helper()
	snap := simBoardSnap(t)
	yes := true
	snap.Copper = &boardCopperSnapshot{
		Availability:  map[string]string{"routing": "available", "vias": "available", "regions": "available"},
		ArcsAvailable: &yes, Regions: []any{}, Arcs: []any{},
		Lines: []any{
			rpLine("rf1", "RF_CHIP", 190, 0, 1000, 200, false),
			rpLine("rf2", "RF_CHIP", 1000, 200, 2480, 400, false),
			rpLine("g1", "GND", 1550, 0, -1300, -40, false),
		},
		Vias:  []any{map[string]any{"primitiveId": "rfv", "net": "RF_CHIP", "x": 1000.0, "y": 200.0, "holeDiameter": 12.0, "diameter": 24.0, "locked": false}},
		Pours: []any{map[string]any{"primitiveId": "pg", "net": "GND"}, map[string]any{"primitiveId": "prf", "net": "RF_CHIP"}},
	}
	simRehash(t, snap)
	return snap
}

func TestLayoutSimulate_RipupForMovedParts(t *testing.T) {
	snap := rpRoutedSnap(t)
	r := simRun(t, snap, lsPose{PrimitiveID: "p-u1", X: fp(0), Y: fp(-600), Rotation: fp(0)})
	rp := r.Ripup
	if rp.Availability != "available" {
		t.Fatalf("ripup = %+v", rp)
	}
	if strings.Join(rp.Tracks, ",") != "rf1,rf2" || strings.Join(rp.Vias, ",") != "rfv" || len(rp.Arcs) != 0 {
		t.Fatalf("ripup tracks %v vias %v arcs %v, want rf1,rf2 / rfv", rp.Tracks, rp.Vias, rp.Arcs)
	}
	if len(rp.Nets) != 1 || rp.Nets[0] != (lsRipupNet{Net: "RF_CHIP", Tracks: 2, Vias: 1}) {
		t.Errorf("nets = %+v", rp.Nets)
	}
	if strings.Join(rp.PourNets, ",") != "RF_CHIP" {
		t.Errorf("pourNets = %v, want RF_CHIP only (GND copper is not cleared)", rp.PourNets)
	}
	// Every listed id exists, is unlocked and is on a net of a moved part's pad.
	movedNets := map[string]bool{}
	for _, m := range r.Moves {
		for _, c := range snap.Components {
			if c.ID == m.PrimitiveID {
				for _, p := range c.Pads {
					movedNets[p.Net] = true
				}
			}
		}
	}
	byID := map[string]map[string]any{}
	for _, g := range [][]any{snap.Copper.Lines, snap.Copper.Vias} {
		for _, raw := range g {
			m := raw.(map[string]any)
			byID[m["primitiveId"].(string)] = m
		}
	}
	for _, id := range append(append([]string{}, rp.Tracks...), rp.Vias...) {
		m, ok := byID[id]
		if !ok || m["locked"] == true || !movedNets[m["net"].(string)] {
			t.Errorf("listed %s: %v", id, m)
		}
	}
	b, _ := json.Marshal(rp)
	if !strings.HasPrefix(string(b), `{"availability":"available","tracks":["rf1","rf2"],"arcs":[],"vias":["rfv"],"nets":[`) {
		t.Errorf("ripup JSON shape: %s", b)
	}
}

func TestLayoutSimulate_RipupEmptyAndUnknown(t *testing.T) {
	// No poses on a board already in place (keep-unmoved): no moves, nothing to clear.
	snap := rpRoutedSnap(t)
	r, err := simulatePcbLayoutOpts(snap, simPoses(snap.SemanticSHA256), false, lsSimOptions{KeepUnmoved: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Moves) != 0 || r.Ripup.Availability != "available" || len(r.Ripup.Tracks)+len(r.Ripup.Vias) != 0 {
		t.Errorf("no moves: ripup = %+v (moves %d)", r.Ripup, len(r.Moves))
	}
	// Routing unread: unknown with empty lists, even with a move.
	snap = rpRoutedSnap(t)
	snap.Copper.Availability["vias"] = "unknown"
	simRehash(t, snap)
	r = simRun(t, snap, lsPose{PrimitiveID: "p-u1", X: fp(0), Y: fp(-600), Rotation: fp(0)})
	if r.Ripup.Availability != "unknown" || len(r.Ripup.Tracks)+len(r.Ripup.Vias)+len(r.Ripup.Arcs)+len(r.Ripup.Nets)+len(r.Ripup.PourNets) != 0 {
		t.Errorf("unread: ripup = %+v", r.Ripup)
	}
	// A copper line that cannot be parsed: unknown, never a partial list.
	snap = rpRoutedSnap(t)
	delete(snap.Copper.Lines[0].(map[string]any), "endX")
	simRehash(t, snap)
	r = simRun(t, snap, lsPose{PrimitiveID: "p-u1", X: fp(0), Y: fp(-600), Rotation: fp(0)})
	if r.Ripup.Availability != "unknown" || len(r.Ripup.Tracks) != 0 {
		t.Errorf("unparsable: ripup = %+v", r.Ripup)
	}
}
