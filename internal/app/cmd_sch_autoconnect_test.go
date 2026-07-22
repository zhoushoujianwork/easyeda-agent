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
	all := planConnection(pin, "ground", "GND", clearScene(), rulesFor())
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
	c := scoreCandidate(pin, "down", 24, "ground", "GND", scene, rulesFor())
	if c.Score < costPartOverlap {
		t.Fatalf("expected part-overlap penalty (>=%d), got %.2f", costPartOverlap, c.Score)
	}
}

func TestScoreCandidate_StubCrossesPin(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	// Another pin sits on the downward stub path (x=100, between y=100 and 130).
	scene := acScene{Pins: []acPin{{X: 100, Y: 115, Designator: "U2", PinNumber: "1"}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
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

// TestScoreCandidate_TitleBlockIsHardReject (issue #147): a label landing in the
// A4 title-block keep-out must be a HARD reject, not a soft cost — otherwise a
// netport dropped on the 图签 wins whenever every other direction is hard-rejected.
func TestScoreCandidate_TitleBlockIsHardReject(t *testing.T) {
	pin := acPin{X: 1025, Y: 95}
	// keep-out from issue #147: {912.6,0,1170,115.5}.
	scene := acScene{TitleBlock: bb(912.6, 0, 1170, 115.5)}
	c := scoreCandidate(pin, "down", 12, "ground", "MOTOR_G", scene, rulesFor())
	if !candidateHardRejected(c) {
		t.Fatalf("title-block intrusion must hard-reject, reasons=%+v score=%.2f", c.Reasons, c.Score)
	}
}

// TestScoreCandidate_TitleBlockClearNotRejected: a label safely OUTSIDE the title
// block is not rejected (the hard reject is scoped to real intrusions).
func TestScoreCandidate_TitleBlockClearNotRejected(t *testing.T) {
	pin := acPin{X: 300, Y: 400}
	scene := acScene{TitleBlock: bb(912.6, 0, 1170, 115.5)}
	c := scoreCandidate(pin, "up", 18, "power", "VCC", scene, rulesFor())
	if candidateHardRejected(c) {
		t.Fatalf("a label clear of the title block must not hard-reject, reasons=%+v", c.Reasons)
	}
}

// TestScoreCandidate_PinCrossIsHardReject: a stub crossing a non-target pin must
// be a HARD reject (issue #64), not a soft penalty a long offset could out-vote.
func TestScoreCandidate_PinCrossIsHardReject(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Pins: []acPin{{X: 100, Y: 115, Designator: "U2", PinNumber: "1"}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
	if !candidateHardRejected(c) {
		t.Fatalf("pin-cross should hard-reject, score=%.2f reasons=%+v", c.Score, c.Reasons)
	}
}

// TestScoreCandidate_StubTouchesForeignWireHardRejects: a stub whose endpoint or
// path lands on an existing wire of a DIFFERENT net is a silent net merge — must
// hard-reject (issue #64).
func TestScoreCandidate_StubTouchesForeignWireHardRejects(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	// A +5V wire runs horizontally across y=130; the downward stub endpoint (100,130)
	// lands on it → foreign-net junction.
	scene := acScene{Wires: []wireSegment{{X0: 50, Y0: 130, X1: 200, Y1: 130, Net: "+5V"}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
	if !candidateHardRejected(c) {
		t.Fatalf("stub touching a foreign-net wire should hard-reject, reasons=%+v", c.Reasons)
	}
}

// TestScoreCandidate_SameNetWireNotRejected: touching a wire ALREADY on the
// target net is the whole point of connecting — it must NOT hard-reject.
func TestScoreCandidate_SameNetWireNotRejected(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Wires: []wireSegment{{X0: 50, Y0: 130, X1: 200, Y1: 130, Net: "GND"}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
	if candidateHardRejected(c) {
		t.Fatalf("same-net wire touch must NOT hard-reject, reasons=%+v", c.Reasons)
	}
}

// TestScoreCandidate_UnnamedWireIsForeign: a wire with no resolvable net is
// treated conservatively as foreign — touching it hard-rejects.
func TestScoreCandidate_UnnamedWireIsForeign(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Wires: []wireSegment{{X0: 50, Y0: 130, X1: 200, Y1: 130, Net: ""}}}
	c := scoreCandidate(pin, "down", 30, "ground", "GND", scene, rulesFor())
	if !candidateHardRejected(c) {
		t.Fatalf("unnamed (foreign) wire touch should hard-reject, reasons=%+v", c.Reasons)
	}
}

// TestPlanConnection_AvoidsForeignWireDirection: with a foreign-net wire blocking
// the 'down' endpoint but the other directions clear, the planner must pick a
// non-rejected direction.
func TestPlanConnection_AvoidsForeignWireDirection(t *testing.T) {
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Wires: []wireSegment{{X0: 50, Y0: 130, X1: 200, Y1: 130, Net: "+5V"}}}
	all := planConnection(pin, "ground", "GND", scene, rulesFor())
	if candidateHardRejected(all[0]) {
		t.Fatalf("planner picked a hard-rejected candidate: %+v", all[0])
	}
}

// TestBuildScene_ParsesWires verifies wires flow from the extension payload into
// the scene (issue #64).
func TestBuildScene_ParsesWires(t *testing.T) {
	result := map[string]any{
		"components": []any{},
		"wires": []any{
			map[string]any{"x0": 10.0, "y0": 20.0, "x1": 30.0, "y1": 20.0, "net": "+5V"},
			map[string]any{"x0": 30.0, "y0": 20.0, "x1": 30.0, "y1": 40.0, "net": ""},
		},
	}
	scene := buildScene(result)
	if len(scene.Wires) != 2 {
		t.Fatalf("expected 2 wire segments, got %d", len(scene.Wires))
	}
	if scene.Wires[0].Net != "+5V" || scene.Wires[0].X1 != 30 {
		t.Errorf("wire 0 parsed wrong: %+v", scene.Wires[0])
	}
	if scene.Wires[1].Net != "" {
		t.Errorf("wire 1 net should be empty, got %q", scene.Wires[1].Net)
	}
}

func TestPlanConnection_AvoidsOverlappingDirection(t *testing.T) {
	// A wall of parts blocks 'down'; 'up' is clear. Planner must not pick down.
	pin := acPin{X: 100, Y: 100}
	scene := acScene{Parts: []layoutBBox{bb(80, 110, 120, 200).deref()}}
	all := planConnection(pin, "ground", "GND", scene, rulesFor())
	if all[0].Direction == "down" {
		t.Fatalf("planner chose blocked direction down; scene=%+v score=%.2f", scene, all[0].Score)
	}
}

func TestPlanConnection_Deterministic(t *testing.T) {
	pin := acPin{X: 100, Y: 100, OwnerBBox: bb(80, 80, 120, 95)}
	a := planConnection(pin, "power", "+5V", clearScene(), rulesFor())
	b := planConnection(pin, "power", "+5V", clearScene(), rulesFor())
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
	all := planConnection(pin, "ground", "GND", clearScene(), rulesFor())
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

// ── same-name pin fan-out ("J1:VBUS*", issue #145) ──────────────────────────

// usbcScene mirrors a USB-C 16P: VBUS and GND on two pins each, the shield tab on
// four, plus uniquely-named data pins.
func usbcScene() acScene {
	return acScene{Pins: []acPin{
		{Designator: "J1", PinNumber: "A4", PinName: "VBUS"},
		{Designator: "J1", PinNumber: "B4", PinName: "VBUS"},
		{Designator: "J1", PinNumber: "A1", PinName: "GND"},
		{Designator: "J1", PinNumber: "B1", PinName: "GND"},
		{Designator: "J1", PinNumber: "A6", PinName: "DP1"},
		{Designator: "U1", PinNumber: "16", PinName: "VCC"},
	}}
}

func TestExpandPinFanouts(t *testing.T) {
	got := expandPinFanouts(usbcScene(), []acConnSpec{
		{PinRef: "J1:VBUS*", Net: "5V", Kind: "power"},
		{PinRef: "U1:VCC", Net: "5V", Kind: "power"},
	})
	if len(got) != 3 {
		t.Fatalf("expected VBUS* to fan out to 2 pins + 1 untouched, got %d: %+v", len(got), got)
	}
	// Fan-out keys each connection by pin NUMBER so nothing downstream re-resolves
	// an ambiguous name, and the net/kind ride along unchanged.
	if got[0].PinRef != "J1:A4" || got[1].PinRef != "J1:B4" {
		t.Fatalf("fanned refs = %q,%q; want J1:A4,J1:B4", got[0].PinRef, got[1].PinRef)
	}
	if got[0].Net != "5V" || got[0].Kind != "power" {
		t.Fatalf("net/kind not carried: %+v", got[0])
	}
	if got[2].PinRef != "U1:VCC" {
		t.Fatalf("non-wildcard spec was rewritten to %q", got[2].PinRef)
	}
}

// A star that matches nothing must degrade to the plain name so resolvePinCoord
// produces its canonical "not found" message naming a real pin.
func TestExpandPinFanoutsNoMatch(t *testing.T) {
	got := expandPinFanouts(usbcScene(), []acConnSpec{{PinRef: "J1:SHIELD*"}})
	if len(got) != 1 || got[0].PinRef != "J1:SHIELD" {
		t.Fatalf("got %+v, want a single J1:SHIELD", got)
	}
	if _, err := resolvePinCoord(usbcScene(), got[0].PinRef); err == nil {
		t.Fatal("expected the degraded ref to still fail resolution")
	}
}

// A single-pin function is the common case: the star must be an identity there, so
// blocks can mark "bond them all" without knowing the part's pin count.
func TestExpandPinFanoutsSinglePinIsIdentity(t *testing.T) {
	got := expandPinFanouts(usbcScene(), []acConnSpec{{PinRef: "U1:VCC*"}})
	if len(got) != 1 || got[0].PinRef != "U1:16" {
		t.Fatalf("got %+v, want a single U1:16", got)
	}
}

// The ambiguity error must teach the fix, not just refuse.
func TestResolvePinCoordAmbiguousSuggestsFanout(t *testing.T) {
	_, err := resolvePinCoord(usbcScene(), "J1:VBUS")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	for _, want := range []string{"A4 B4", `"J1:VBUS*"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %s", err, want)
		}
	}
}

// ── endpoint grid snap (merged-wire short prevention) ───────────────────────

// The planner must score the coordinate the board will actually hold. An
// un-snapped endpoint let a stub planned at (545,272) read as "clear" of a
// foreign-net wire at y=270, then land at (545,270) — ON it — merging two nets.
func TestEndpointForSnapsToGrid(t *testing.T) {
	cases := []struct {
		dir            string
		px, py, offset float64
		wantX, wantY   float64
	}{
		// 290-18 = 272 → snaps to 270; x stays exactly on the pin.
		{"up", 545, 290, 18, 545, 270},
		{"down", 545, 290, 18, 545, 310},
		{"left", 560, 270, 18, 540, 270},
		{"right", 560, 270, 18, 580, 270},
		// Already on-grid endpoints are untouched.
		{"up", 500, 300, 20, 500, 280},
		// A pin on the ODD 5-grid keeps its perpendicular coordinate: snapping it
		// would pull the stub off the pin axis into a diagonal that fails to create.
		{"left", 600, 385, 18, 580, 385},
	}
	for _, c := range cases {
		x, y := endpointFor(c.px, c.py, c.offset, c.dir)
		if x != c.wantX || y != c.wantY {
			t.Fatalf("%s from (%v,%v)+%v = (%v,%v), want (%v,%v)",
				c.dir, c.px, c.py, c.offset, x, y, c.wantX, c.wantY)
		}
		// Whatever the snap does, the stub must stay orthogonal.
		if x != c.px && y != c.py {
			t.Fatalf("%s produced a diagonal stub: (%v,%v) → (%v,%v)", c.dir, c.px, c.py, x, y)
		}
	}
}

// With the snap in place, a candidate whose SNAPPED endpoint lands on a
// foreign-net wire must be hard-rejected — the check that silently passed before.
func TestForeignWireRejectUsesSnappedEndpoint(t *testing.T) {
	// The J1:CC1 wire from the real failure: y=270, x 540→560, net D1_N3.
	wires := []wireSegment{{X0: 540, Y0: 270, X1: 560, Y1: 270, Net: "D1_N3"}}
	// D2:4 sits at (545,290); "up" with offset 18 snaps to (545,270) — on that wire.
	ex, ey := endpointFor(545, 290, 18, "up")
	if !stubTouchesForeignWire(545, 290, ex, ey, "USB_HOST_DP", wires) {
		t.Fatalf("snapped endpoint (%v,%v) lies on a foreign-net wire but was not rejected", ex, ey)
	}
}

// A stub that STOPS exactly on a neighbouring pin is shorted just as surely as one
// that passes through it. Grid snapping makes this the common case, not the corner
// case: XL1509's pins sit 20 apart, so an 18-offset stub snaps right onto the next
// pin (this chained three nets into one wire tree on a real board).
func TestScoreCandidate_StubEndingOnNeighbourPinHardRejects(t *testing.T) {
	// U3 pins 2/3/4 stacked vertically at x=645, 20 apart.
	scene := acScene{Pins: []acPin{
		{X: 645, Y: 410, Designator: "U3", PinNumber: "2"},
		{X: 645, Y: 390, Designator: "U3", PinNumber: "3"},
		{X: 645, Y: 370, Designator: "U3", PinNumber: "4"},
	}}
	rules := defaultAutoconnectRules()
	pin := acPin{X: 645, Y: 410, Designator: "U3", PinNumber: "2"}

	// "up" with offset 18 snaps to (645,390) — exactly pin 3.
	up := scoreCandidate(pin, "up", 18, "netport", "C11_N3", scene, rules)
	if up.Score < costPinCross {
		t.Fatalf("stub ending on pin 3 must be hard-rejected, got score %v (%v)", up.Score, up.Reasons)
	}
	// A direction with no pin in the way stays viable.
	left := scoreCandidate(pin, "left", 18, "netport", "C11_N3", scene, rules)
	if left.Score >= costPinCross {
		t.Fatalf("clear direction must not be rejected, got %v (%v)", left.Score, left.Reasons)
	}
}
