package app

import (
	"strings"
	"testing"
)

// clearScene is an empty world: no parts, pins, flags, or title block. A pin in
// open space — every penalty is zero, so only offset cost + direction bonuses
// drive selection.
func clearScene() acScene { return acScene{} }

func rulesFor() autoconnectRules { return defaultAutoconnectRules() }

func TestEndpointFor_MatchesConnectorYDown(t *testing.T) {
	cases := []struct {
		dir   string
		wantX float64
		wantY float64
	}{
		{"up", 100, 70},    // y decreases
		{"down", 100, 130}, // y increases
		{"left", 70, 100},  // x decreases
		{"right", 130, 100},
	}
	for _, c := range cases {
		x, y := endpointFor(100, 100, 30, c.dir)
		if x != c.wantX || y != c.wantY {
			t.Errorf("dir %s: got (%.0f,%.0f), want (%.0f,%.0f)", c.dir, x, y, c.wantX, c.wantY)
		}
	}
}

func TestPlanConnection_GroundPrefersDownShortest(t *testing.T) {
	// A pin below its owner center → outward = down; kind gnd default = down.
	// Both bonuses stack on 'down', and the shortest offset wins ties.
	pin := acPin{X: 100, Y: 200, OwnerBBox: bb(80, 150, 120, 190)}
	all := planConnection(pin, "ground", clearScene(), rulesFor())
	sel := all[0]
	if sel.Direction != "down" {
		t.Fatalf("expected down (outward + kind default), got %s", sel.Direction)
	}
	if sel.Offset != 18 {
		t.Errorf("expected shortest offset 18, got %.0f", sel.Offset)
	}
	// down should score below any up candidate (up gets neither bonus here).
	for _, c := range all {
		if c.Direction == "up" && c.Offset == sel.Offset && c.Score <= sel.Score {
			t.Errorf("up should cost more than down at equal offset: down=%.2f up=%.2f", sel.Score, c.Score)
		}
	}
}

func TestScoreCandidate_PartOverlapDominates(t *testing.T) {
	// A part bbox sits right where the 'down' endpoint would land → +10000.
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Parts: []layoutBBox{bb(90, 115, 110, 135).deref()}}
	c := scoreCandidate(pin, "down", 24, "ground", scene, rulesFor())
	if c.Score < costPartOverlap {
		t.Fatalf("expected part-overlap penalty (>=%d), got %.2f", costPartOverlap, c.Score)
	}
}

func TestScoreCandidate_StubCrossesPin(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	// Another pin sits on the downward stub path (x=100, between y=100 and 130).
	scene := acScene{Pins: []acPin{{X: 100, Y: 115, Designator: "U2", PinNumber: "1"}}}
	c := scoreCandidate(pin, "down", 30, "ground", scene, rulesFor())
	hasCross := false
	for _, r := range c.Reasons {
		if r.Cost == costPinCross {
			hasCross = true
		}
	}
	if !hasCross {
		t.Fatalf("expected a pin-cross penalty, reasons=%+v", c.Reasons)
	}
}

func TestPlanConnection_AvoidsOverlappingDirection(t *testing.T) {
	// A wall of parts blocks 'down'; 'up' is clear. Planner must not pick down.
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Parts: []layoutBBox{bb(80, 110, 120, 200).deref()}}
	all := planConnection(pin, "ground", scene, rulesFor())
	if all[0].Direction == "down" {
		t.Fatalf("planner chose blocked direction down; scene=%+v score=%.2f", scene, all[0].Score)
	}
}

func TestPlanConnection_Deterministic(t *testing.T) {
	pin := acPin{X: 100, Y: 100, OwnerBBox: bb(80, 80, 120, 95)}
	a := planConnection(pin, "power", clearScene(), rulesFor())
	b := planConnection(pin, "power", clearScene(), rulesFor())
	if len(a) != len(b) {
		t.Fatalf("candidate count differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Direction != b[i].Direction || a[i].Offset != b[i].Offset || a[i].Score != b[i].Score {
			t.Fatalf("non-deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestPlanConnection_TieBreakStable(t *testing.T) {
	// No geometry, no owner bbox, kind with default 'down' → 'down' wins its
	// bonus, but among the OTHER three directions (all equal score per offset)
	// the lexical tie-break must order down<left<right<up. Confirm offsets too:
	// shortest first within a direction.
	pin := acPin{X: 0, Y: 0}
	all := planConnection(pin, "ground", clearScene(), rulesFor())
	if all[0].Direction != "down" || all[0].Offset != 18 {
		t.Fatalf("expected down@18 first, got %s@%.0f", all[0].Direction, all[0].Offset)
	}
	// Every 'down' candidate (cheapest direction) should precede the first
	// non-down candidate.
	firstNonDown := -1
	for i, c := range all {
		if c.Direction != "down" {
			firstNonDown = i
			break
		}
	}
	for i := 0; i < firstNonDown; i++ {
		if all[i].Direction != "down" {
			t.Fatalf("down block broken at %d: %s", i, all[i].Direction)
		}
	}
}

func TestCandidateOffsets_InclusiveRange(t *testing.T) {
	got := candidateOffsets(autoconnectRules{OffsetMin: 18, OffsetMax: 80, OffsetStep: 6})
	if got[0] != 18 {
		t.Errorf("first offset want 18, got %.0f", got[0])
	}
	last := got[len(got)-1]
	if last > 80 || last < 74 {
		t.Errorf("last offset should approach 80, got %.0f", last)
	}
}

func TestResolvePinCoord(t *testing.T) {
	scene := acScene{Pins: []acPin{
		{X: 10, Y: 20, Designator: "U1", PinNumber: "41", PinName: "GND"},
		{X: 30, Y: 40, Designator: "U1", PinNumber: "3", PinName: "3V3"},
	}}
	// by number
	p, err := resolvePinCoord(scene, "U1:41")
	if err != nil || p.X != 10 || p.Y != 20 {
		t.Fatalf("U1:41 → %+v err=%v", p, err)
	}
	// by name
	p, err = resolvePinCoord(scene, "U1:3V3")
	if err != nil || p.X != 30 {
		t.Fatalf("U1:3V3 → %+v err=%v", p, err)
	}
	// not found
	if _, err := resolvePinCoord(scene, "U9:1"); err == nil {
		t.Error("expected error for missing pin")
	}
	// malformed
	if _, err := resolvePinCoord(scene, "U1"); err == nil {
		t.Error("expected error for malformed ref")
	}
}

func TestResolvePinCoord_OffPageHintWithPageInfo(t *testing.T) {
	// D7 is known to the scene (from --all-pages) but has no pins here — it lives
	// on page B. The error must name the page and point at `doc switch`, not blame
	// a typo.
	scene := acScene{
		Pins: []acPin{{X: 10, Y: 20, Designator: "U1", PinNumber: "1"}},
		Components: []acComponent{
			{Designator: "U1", HasPins: true},
			{Designator: "D7", HasPins: false, PageUuid: "0395abcd", PageName: "Page B"},
		},
	}
	_, err := resolvePinCoord(scene, "D7:2")
	if err == nil {
		t.Fatal("expected an off-page error for D7:2")
	}
	msg := err.Error()
	for _, want := range []string{"0395abcd", "Page B", "doc switch", "ANOTHER"} {
		if !strings.Contains(msg, want) {
			t.Errorf("off-page hint missing %q; got: %s", want, msg)
		}
	}
	if strings.Contains(msg, "not placed") {
		t.Errorf("off-page hint should NOT say 'not placed'; got: %s", msg)
	}
}

func TestResolvePinCoord_OffPageHintWithoutPageInfo(t *testing.T) {
	// Same off-page component, but the extension didn't supply page uuid/name.
	// Degrade to a generic switch hint — still not "not placed".
	scene := acScene{Components: []acComponent{{Designator: "D7", HasPins: false}}}
	_, err := resolvePinCoord(scene, "D7:2")
	if err == nil {
		t.Fatal("expected an off-page error for D7:2")
	}
	msg := err.Error()
	if !strings.Contains(msg, "doc switch") || !strings.Contains(msg, "ANOTHER") {
		t.Errorf("generic off-page hint should mention doc switch and ANOTHER page; got: %s", msg)
	}
	if strings.Contains(msg, "not placed") {
		t.Errorf("off-page hint should NOT say 'not placed'; got: %s", msg)
	}
}

func TestResolvePinCoord_TrulyNotPlacedKeepsGenericError(t *testing.T) {
	// A designator the scene has never heard of → keep the original "not placed"
	// diagnostic (real typo / unplaced part), NOT the off-page hint.
	scene := acScene{Components: []acComponent{{Designator: "U1", HasPins: true}}}
	_, err := resolvePinCoord(scene, "U9:1")
	if err == nil {
		t.Fatal("expected error for unknown designator")
	}
	if !strings.Contains(err.Error(), "not placed") {
		t.Errorf("unknown designator should keep the 'not placed' hint; got: %s", err.Error())
	}
}

func TestBuildScene_ClassifiesPrimitives(t *testing.T) {
	result := map[string]any{"components": []any{
		map[string]any{
			"componentType": "part", "designator": "U1",
			"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 10.0, "maxY": 10.0},
			"pins": []any{
				map[string]any{"pinNumber": "1", "pinName": "VCC", "x": 0.0, "y": 5.0},
			},
		},
		map[string]any{
			"componentType": "netflag",
			"bbox":          map[string]any{"minX": 50.0, "minY": 50.0, "maxX": 54.0, "maxY": 54.0},
		},
		map[string]any{
			"componentType": "sheet",
			"bbox":          map[string]any{"minX": -100.0, "minY": -100.0, "maxX": 400.0, "maxY": 300.0},
		},
	}}
	scene := buildScene(result)
	if len(scene.Parts) != 1 {
		t.Errorf("expected 1 part bbox, got %d", len(scene.Parts))
	}
	if len(scene.Pins) != 1 || scene.Pins[0].Designator != "U1" || scene.Pins[0].OwnerBBox == nil {
		t.Errorf("pin not attached to owner: %+v", scene.Pins)
	}
	if len(scene.Flags) != 1 {
		t.Errorf("expected 1 flag bbox, got %d", len(scene.Flags))
	}
	if len(scene.Components) != 1 || scene.Components[0].Designator != "U1" || !scene.Components[0].HasPins {
		t.Errorf("expected 1 component U1 with pins, got %+v", scene.Components)
	}
	if scene.TitleBlock == nil || scene.TitleBlockProvisional {
		t.Errorf("title block keep-out should be derived from sheet, got tb=%+v prov=%v", scene.TitleBlock, scene.TitleBlockProvisional)
	}
}

func TestBuildScene_ProvisionalWhenNoSheet(t *testing.T) {
	result := map[string]any{"components": []any{
		map[string]any{"componentType": "part", "designator": "R1",
			"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 5.0, "maxY": 5.0}},
	}}
	scene := buildScene(result)
	if scene.TitleBlock != nil || !scene.TitleBlockProvisional {
		t.Errorf("no sheet → provisional & no enforced keep-out, got tb=%+v prov=%v", scene.TitleBlock, scene.TitleBlockProvisional)
	}
}

// ── idempotency: three-state decision (issue #50) ───────────────────────────

func TestDecideConnState_ThreeStates(t *testing.T) {
	cases := []struct {
		name       string
		currentNet string
		netKnown   bool
		targetNet  string
		want       acConnState
	}{
		// Pin floating (net known, empty) → normal new connection.
		{"floating pin → new", "", true, "GND", acStateNew},
		// Pin already on the target net → skip (the core idempotency case).
		{"same net → already-connected", "GND", true, "GND", acStateAlreadyConnected},
		// Pin on a different net → conflict (default error, --replace overrides).
		{"different net → conflict", "+3V3", true, "GND", acStateConflict},
		// Netlist unavailable → can't prove idempotency, fall back to new.
		{"net unknown → new (fallback)", "GND", false, "GND", acStateNew},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideConnState(tc.currentNet, tc.netKnown, tc.targetNet)
			if got != tc.want {
				t.Errorf("decideConnState(%q, %v, %q) = %q, want %q",
					tc.currentNet, tc.netKnown, tc.targetNet, got, tc.want)
			}
		})
	}
}

// TestBuildScene_ParsesPinNet verifies the pin's current net flows from the
// extension payload into acPin: a string sets NetKnown, a null does not.
func TestBuildScene_ParsesPinNet(t *testing.T) {
	result := map[string]any{"components": []any{
		map[string]any{
			"componentType": "part", "designator": "U1",
			"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 10.0, "maxY": 10.0},
			"pins": []any{
				map[string]any{"pinNumber": "1", "pinName": "GND", "x": 0.0, "y": 5.0, "net": "GND"},
				map[string]any{"pinNumber": "2", "pinName": "IN", "x": 0.0, "y": 8.0, "net": ""},
				// net: nil → netlist unavailable for this pin.
				map[string]any{"pinNumber": "3", "pinName": "OUT", "x": 0.0, "y": 9.0, "net": nil},
				// no net key at all → also unknown.
				map[string]any{"pinNumber": "4", "pinName": "NC", "x": 0.0, "y": 9.5},
			},
		},
	}}
	scene := buildScene(result)
	if len(scene.Pins) != 4 {
		t.Fatalf("expected 4 pins, got %d", len(scene.Pins))
	}
	byNum := map[string]acPin{}
	for _, p := range scene.Pins {
		byNum[p.PinNumber] = p
	}
	if p := byNum["1"]; !p.NetKnown || p.Net != "GND" {
		t.Errorf("pin 1: want net=GND known, got net=%q known=%v", p.Net, p.NetKnown)
	}
	if p := byNum["2"]; !p.NetKnown || p.Net != "" {
		t.Errorf("pin 2: want floating (empty, known), got net=%q known=%v", p.Net, p.NetKnown)
	}
	if p := byNum["3"]; p.NetKnown {
		t.Errorf("pin 3: net was null → should be unknown, got known=%v", p.NetKnown)
	}
	if p := byNum["4"]; p.NetKnown {
		t.Errorf("pin 4: no net key → should be unknown, got known=%v", p.NetKnown)
	}
}

func TestBuildScene_OffPageComponentIsPinlessWithPage(t *testing.T) {
	// --all-pages surfaces D7 (on page B) with page tags but NO pins (the active
	// page's pin lookup didn't return them). buildScene must record it as a
	// pin-less component carrying its page, so resolvePinCoord can hint correctly.
	result := map[string]any{"components": []any{
		map[string]any{"componentType": "part", "designator": "U1",
			"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 10.0, "maxY": 10.0},
			"pins": []any{map[string]any{"pinNumber": "1", "pinName": "VCC", "x": 0.0, "y": 5.0}},
		},
		map[string]any{"componentType": "part", "designator": "D7",
			"bbox":     map[string]any{"minX": 20.0, "minY": 20.0, "maxX": 24.0, "maxY": 24.0},
			"pageUuid": "0395abcd", "pageName": "Page B",
		},
	}}
	scene := buildScene(result)
	var d7 *acComponent
	for i := range scene.Components {
		if scene.Components[i].Designator == "D7" {
			d7 = &scene.Components[i]
		}
	}
	if d7 == nil {
		t.Fatalf("D7 not recorded in scene.Components: %+v", scene.Components)
	}
	if d7.HasPins {
		t.Error("off-page D7 should be pin-less (HasPins=false)")
	}
	if d7.PageUuid != "0395abcd" || d7.PageName != "Page B" {
		t.Errorf("D7 page info not carried: %+v", d7)
	}
}

// deref is a tiny test helper: turn the *layoutBBox from bb() into a value.
func (p *layoutBBox) deref() layoutBBox { return *p }
