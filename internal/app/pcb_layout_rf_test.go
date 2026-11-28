package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func loadBoardFixture(t *testing.T, name string) *boardSnapshot {
	t.Helper()
	raw, err := os.ReadFile("testdata/boards/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var snap boardSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	return &snap
}

func rfBox(minX, minY, maxX, maxY float64) *layoutBBox {
	return &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
}

func rfComp(des string, b *layoutBBox, pads ...lsPad) lsComponent {
	return lsComponent{Designator: des, BBox: b, Pads: pads}
}

func rfHas(issues []lsIssue, des, prefix string) bool {
	for _, is := range issues {
		if is.Designator == des && strings.HasPrefix(is.Message, prefix) {
			return true
		}
	}
	return false
}

func TestRfPadlessSide(t *testing.T) {
	b := layoutBBox{MinX: 0, MinY: 0, MaxX: 700, MaxY: 1000}
	// Pads on the left, right and bottom thirds; the top 30 % is pad-free (a module antenna).
	var pads [][4]float64
	for y := 50.0; y <= 650; y += 100 {
		pads = append(pads, [4]float64{20, y, 30, 20}, [4]float64{680, y, 30, 20})
	}
	pads = append(pads, [4]float64{350, 20, 20, 30})
	side, depth, ok := rfPadlessSide(b, pads)
	if !ok || side != 3 {
		t.Fatalf("side %d ok %v, want top (3)", side, ok)
	}
	if depth < 330 || depth > 345 {
		t.Errorf("depth %.1f, want edge to the top pad (~340)", depth)
	}
	// A chip with pads all round has no pad-free side.
	pads = append(pads, [4]float64{350, 980, 20, 30})
	if _, _, ok := rfPadlessSide(b, pads); ok {
		t.Error("pads on all four sides must not look like a module")
	}
}

func TestRfInductorAndCrystalRules(t *testing.T) {
	r := rfCtx{antennas: map[string]bool{"ANT1": true}, antChip: map[string]string{}, antPads: map[string][2]string{},
		chain: map[string]bool{"L9": true}, modules: map[string]bool{}, switchers: map[string]bool{"U5": true}}
	comps := []lsComponent{
		rfComp("ANT1", rfBox(0, 0, 100, 60)),
		rfComp("L1", rfBox(100+314, 0, 100+344, 30)),    // 314 mil gap: under 8 mm
		rfComp("L2", rfBox(100+316, 200, 100+346, 230)), // dx 316: clear of the antenna
		rfComp("L9", rfBox(110, 0, 140, 30)),            // chain member: exempt
		rfComp("Y1", rfBox(2000, 0, 2080, 50)),
		rfComp("L3", rfBox(2080+156, 0, 2080+186, 30)), // 156 mil from Y1
		rfComp("U5", rfBox(2000, 500, 2200, 700)),
		rfComp("Y2", rfBox(2000, 700+158, 2080, 700+208)), // 158 mil from U5: clear
	}
	is := r.issues(comps, nil)
	if !rfHas(is, "L1", "inductor ") {
		t.Errorf("L1 within 8 mm of ANT1 must be flagged: %+v", is)
	}
	if rfHas(is, "L2", "inductor ") {
		t.Errorf("L2 is clear of the antenna: %+v", is)
	}
	if rfHas(is, "L9", "") {
		t.Errorf("chain member L9 must be exempt: %+v", is)
	}
	if !rfHas(is, "L3", "inductor ") {
		t.Errorf("L3 within 4 mm of Y1 must be flagged: %+v", is)
	}
	if rfHas(is, "Y2", "crystal ") {
		t.Errorf("Y2 is 158 mil from the switcher: %+v", is)
	}
	comps[7].BBox = rfBox(2000, 700+150, 2080, 700+200)
	if is := r.issues(comps, nil); !rfHas(is, "Y2", "crystal ") {
		t.Errorf("Y2 150 mil from switcher U5 must be flagged: %+v", is)
	}
}

func TestRfAntennaZoneOpenTowardsItsChip(t *testing.T) {
	r := rfCtx{antennas: map[string]bool{"ANT1": true}, antChip: map[string]string{"ANT1": "U1"}, antPads: map[string][2]string{},
		chain: map[string]bool{"C9": true}, modules: map[string]bool{}, switchers: map[string]bool{}}
	comps := []lsComponent{
		rfComp("ANT1", rfBox(1000, 0, 1100, 60)),
		rfComp("U1", rfBox(500, -100, 900, 160)),     // chip to the left: the left side is open
		rfComp("C1", rfBox(1110, 0, 1140, 30)),       // right of the antenna: inside
		rfComp("C2", rfBox(960, 20, 990, 50)),        // left side (open): fine
		rfComp("C9", rfBox(1110, 30, 1140, 60)),      // matching part: allowed
		rfComp("J1", rfBox(1000+60, 200, 1100, 260)), // 140 mil below: outside 118 mil
	}
	is := r.issues(comps, nil)
	if !rfHas(is, "C1", "inside the antenna clearance zone") {
		t.Errorf("C1 must be in the zone: %+v", is)
	}
	for _, des := range []string{"C2", "C9", "U1"} {
		if rfHas(is, des, "inside the antenna clearance zone") {
			t.Errorf("%s must not be flagged in the zone: %+v", des, is)
		}
	}
	if !rfHas(is, "ANT1", "antenna ") {
		t.Errorf("J1 is 140 mil from the antenna, under 5 mm: %+v", is)
	}
}

func TestRfModuleEdgeRule(t *testing.T) {
	r := rfCtx{antennas: map[string]bool{}, antChip: map[string]string{}, antPads: map[string][2]string{}, chain: map[string]bool{},
		modules: map[string]bool{"U1": true}, switchers: map[string]bool{}}
	var pads []lsPad
	for y := 50.0; y <= 650; y += 100 {
		pads = append(pads, lsPad{X: 20, Y: y, W: 30, H: 20}, lsPad{X: 680, Y: y, W: 30, H: 20})
	}
	pads = append(pads, lsPad{X: 350, Y: 20, W: 20, H: 30})
	board := layoutBBox{MinX: 0, MinY: 0, MaxX: 3000, MaxY: 1000}
	at := func(dy float64) []lsComponent {
		ps := make([]lsPad, len(pads))
		for i, p := range pads {
			p.Y += dy
			ps[i] = p
		}
		return []lsComponent{rfComp("U1", rfBox(0, dy, 700, 1000+dy), ps...)}
	}
	if is := r.issues(at(0), &board); rfHas(is, "U1", rfIssueEdgeMessage) {
		t.Errorf("antenna end on the top edge must pass: %+v", is)
	}
	if is := r.issues(at(-200), &board); !rfHas(is, "U1", rfIssueEdgeMessage) {
		t.Errorf("antenna end 200 mil inside must be flagged: %+v", is)
	}
}

// The RF fixture: spread keeps the antenna zone clear and never makes it worse.
func TestLayoutSpread_RfFixture(t *testing.T) {
	snap := loadBoardFixture(t, "reference-4stage-compact")
	simRehash(t, snap)
	r, err := simulatePcbLayoutOpts(snap, &lsPoseFile{}, false, lsSimOptions{KeepUnmoved: true, Spread: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Spread == nil || r.Spread.After.Score > r.Spread.Before.Score {
		t.Fatalf("spread made the RF board worse: %+v", r.Spread)
	}
	for _, is := range r.Issues {
		if strings.HasPrefix(is.Message, "inside the antenna clearance zone") {
			t.Errorf("spread left %s in the antenna zone", is.Designator)
		}
	}
}
