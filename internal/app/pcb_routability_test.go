package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// routabilityPocketFixture models the #306 four-layer board in miniature.
// J1 is a 0.4 mm (≈16 mil) pitch connector on BOTTOM: a signal row at y=150
// and a second row at y=120 (10 mil gap, too narrow for a 4 mil track at 6 mil
// clearance). Below the signal row, ESD D1 (left/right) and decoupling C1
// (bottom) close a pocket on BOTTOM. The pocket (x 160..240, y 160..212) has
// room for a 24/12 mil via, so J1.2..J1.4 pass only through that via site.
func routabilityPocketFixture() *boardSnapshot {
	rect := func(id, num, net string, layer int, x, y, w, h float64) boardPad {
		return boardPad{ID: id, Number: num, Net: net, Layer: layer, X: x, Y: y, W: w, H: h, Shape: []any{"RECT", w, h, 0.0}}
	}
	j1 := boardComp{ID: "j1", Designator: "J1", X: 200, Y: 135, Layer: 2}
	for i, x := range []float64{168, 184, 200, 216, 232} {
		n := string(rune('1' + i))
		j1.Pads = append(j1.Pads, rect("j1-"+n, n, "SIG"+n, 2, x, 150, 8, 20))
		m := string(rune('6' + i))
		if i == 4 {
			m = "10"
		}
		j1.Pads = append(j1.Pads, rect("j1-"+m, m, "ROW2_"+m, 2, x, 120, 8, 20))
	}
	d1 := boardComp{ID: "d1", Designator: "D1", X: 200, Y: 180, Layer: 2, Pads: []boardPad{rect("d1-1", "1", "SIG1", 2, 152, 180, 16, 60), rect("d1-2", "2", "SIG5", 2, 248, 180, 16, 60)}}
	c1 := boardComp{ID: "c1", Designator: "C1", X: 201, Y: 222, Layer: 2, Pads: []boardPad{rect("c1-1", "1", "VCC", 2, 176, 222, 48, 20), rect("c1-2", "2", "GND", 2, 226, 222, 48, 20)}}
	available := true
	return &boardSnapshot{
		Components:   []boardComp{j1, d1, c1},
		CopperLayers: 4,
		Outline:      &boardOutline{Source: "polygon", Format: "polyline", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 400, MaxY: 400}, Points: [][2]float64{{0, 0}, {400, 0}, {400, 400}, {0, 400}}},
		Rules: &boardRules{Source: "live", ClearanceMil: 6, ClearanceTrackTrackMil: 6, TrackWidthMinMil: 4, CopperToEdgeMil: 10, ViaDiameterMil: 24, ViaDrillMil: 12,
			Measured: []string{pcbRuleFieldClearance, pcbRuleFieldClearanceTrackTrack, pcbRuleFieldTrackWidthMin, pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter, pcbRuleFieldCopperToEdge}},
		Copper: &boardCopperSnapshot{Availability: map[string]string{"routing": "available", "vias": "available", "pours": "available", "poured": "available", "regions": "available", "fills": "available"},
			ArcsAvailable: &available, Lines: []any{}, Arcs: []any{}, Vias: []any{}, Pours: []any{}, Poured: []any{}, Regions: []any{}, Fills: []any{}},
	}
}

func routabilityPad(t *testing.T, rep pcbRoutabilityReport, name string) pcbRoutabilityPad {
	t.Helper()
	for _, p := range rep.Pads {
		if p.Pad == name {
			return p
		}
	}
	t.Fatalf("pad %s missing from report", name)
	return pcbRoutabilityPad{}
}

func routabilityHasBlocker(p pcbRoutabilityPad, kind, label string) bool {
	for _, b := range p.Blockers {
		if b.Kind == kind && (b.Ref == label || b.ID == label) {
			return true
		}
	}
	return false
}

func defaultRoutabilityOptions() pcbRoutabilityOptions {
	return pcbRoutabilityOptions{StepMil: pcbRoutabilityDefaultStepMil, RadiusMil: pcbRoutabilityDefaultRadiusMil}
}

func TestRoutabilityPocketWithViaSitePasses(t *testing.T) {
	rep, err := analyzePCBRoutability(routabilityPocketFixture(), defaultRoutabilityOptions())
	if err != nil || rep.Status != "pass" {
		t.Fatalf("status=%s err=%v trapped=%v", rep.Status, err, rep.Trapped)
	}
	p := routabilityPad(t, rep, "J1.3")
	if p.ViaSites == 0 || p.NearestViaSite == nil || len(p.EscapeDirections) != 0 {
		t.Fatalf("pocket pad should pass only through a via site: %+v", p)
	}
	if edge := routabilityPad(t, rep, "J1.1"); len(edge.EscapeDirections) == 0 {
		t.Fatalf("row-end pad should escape by track: %+v", edge)
	}
}

func TestRoutabilityTrappedScenariosAndRelease(t *testing.T) {
	scenarios := []struct {
		name    string
		block   func(*boardSnapshot)
		kind    string
		blocker string
	}{
		{"back-side large pad", func(s *boardSnapshot) {
			s.Components = append(s.Components, boardComp{ID: "u2", Designator: "U2", X: 200, Y: 186, Layer: 1, Pads: []boardPad{{ID: "u2-1", Number: "1", Net: "PWR", Layer: 1, X: 200, Y: 186, W: 90, H: 60, Shape: []any{"RECT", 90.0, 60.0, 0.0}}}})
		}, "pad", "U2.1"},
		{"poured power copper", func(s *boardSnapshot) {
			s.Copper.Poured = []any{map[string]any{"primitiveId": "pour-vcc", "net": "VBAT", "layer": float64(1), "fills": []any{map[string]any{"id": "vbat-fill", "fill": true, "lineWidth": 0.0, "sourceUnits": "mil", "lineWidthUnits": "mil", "arcSweepUnits": "degree", "source": []any{150.0, 155.0, "L", 250.0, 155.0, 250.0, 215.0, 150.0, 215.0}}}}}
		}, "copper", "vbat-fill"},
		{"pre-routed via in the pocket", func(s *boardSnapshot) {
			s.Copper.Vias = []any{map[string]any{"primitiveId": "via-gnd", "net": "GND", "x": 200.0, "y": 186.0, "holeDiameter": 12.0, "diameter": 24.0}}
		}, "via", "via-gnd"},
		{"pre-routed power track", func(s *boardSnapshot) {
			s.Copper.Lines = []any{map[string]any{"primitiveId": "trk-vbat", "net": "VBAT", "layer": float64(1), "startX": 140.0, "startY": 186.0, "endX": 260.0, "endY": 186.0, "lineWidth": 30.0}}
		}, "track", "trk-vbat"},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			s := routabilityPocketFixture()
			sc.block(s)
			rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions())
			if err == nil || rep.Status != "fail" {
				t.Fatalf("expected fail, got %s err=%v", rep.Status, err)
			}
			for _, name := range []string{"J1.2", "J1.3", "J1.4"} {
				p := routabilityPad(t, rep, name)
				if p.Status != "trapped" || p.ViaSites != 0 || len(p.EscapeDirections) != 0 {
					t.Fatalf("%s should be trapped: %+v", name, p)
				}
				if !routabilityHasBlocker(p, sc.kind, sc.blocker) {
					t.Fatalf("%s blockers miss %s %s: %+v", name, sc.kind, sc.blocker, p.Blockers)
				}
				for _, enclosure := range []string{"C1.1", "D1.1"} {
					if name == "J1.2" && !routabilityHasBlocker(p, "pad", enclosure) {
						t.Fatalf("%s blockers miss enclosure %s: %+v", name, enclosure, p.Blockers)
					}
				}
				if routabilityHasBlocker(p, "pad", name) {
					t.Fatalf("source pad listed as its own blocker: %+v", p.Blockers)
				}
			}
			if got := strings.Join(rep.Trapped, ","); got != "J1.2,J1.3,J1.4" {
				t.Fatalf("trapped list = %s", got)
			}
			// Moving the decoupling capacitor away opens the pocket downward.
			for i := range s.Components {
				if s.Components[i].Designator == "C1" {
					for j := range s.Components[i].Pads {
						s.Components[i].Pads[j].Y += 150
					}
				}
			}
			rep, err = analyzePCBRoutability(s, defaultRoutabilityOptions())
			if sc.kind == "via" {
				// The pre-routed via sits directly in front of J1.3, its only
				// exit; neighbours escape around it, J1.3 stays trapped by it.
				p := routabilityPad(t, rep, "J1.3")
				if rep.Status != "fail" || strings.Join(rep.Trapped, ",") != "J1.3" || !routabilityHasBlocker(p, "via", "via-gnd") {
					t.Fatalf("via must keep blocking J1.3 exit: %s %v %+v", rep.Status, rep.Trapped, p.Blockers)
				}
				s.Copper.Vias = []any{}
				rep, err = analyzePCBRoutability(s, defaultRoutabilityOptions())
			}
			if err != nil || rep.Status != "pass" {
				t.Fatalf("removing the blocker should release the pocket: %s %v %v", rep.Status, err, rep.Trapped)
			}
			if p := routabilityPad(t, rep, "J1.3"); len(p.EscapeDirections) == 0 {
				t.Fatalf("released pad should report escape directions: %+v", p)
			}
		})
	}
}

func TestRoutabilityRemovingBlockerReleasesPad(t *testing.T) {
	s := routabilityPocketFixture()
	s.Copper.Vias = []any{map[string]any{"primitiveId": "via-gnd", "net": "GND", "x": 200.0, "y": 186.0, "holeDiameter": 12.0, "diameter": 24.0}}
	if rep, _ := analyzePCBRoutability(s, defaultRoutabilityOptions()); rep.Status != "fail" {
		t.Fatalf("blocked board should fail: %s", rep.Status)
	}
	s.Copper.Vias = []any{}
	if rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions()); err != nil || rep.Status != "pass" {
		t.Fatalf("removing the via should pass: %s %v", rep.Status, err)
	}
}

func TestRoutabilityMissingInputsAreIncomplete(t *testing.T) {
	tests := map[string]func(*boardSnapshot){
		"fallback rules":    func(s *boardSnapshot) { s.Rules.Source = "fallback" },
		"no via rule":       func(s *boardSnapshot) { s.Rules.ViaDiameterMil = 0 },
		"unknown poured":    func(s *boardSnapshot) { s.Copper.Availability["poured"] = "unknown" },
		"no copper":         func(s *boardSnapshot) { s.Copper = nil },
		"bbox outline":      func(s *boardSnapshot) { s.Outline.Source = "bbox" },
		"unknown pad shape": func(s *boardSnapshot) { s.Components[0].Pads[0].Shape = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s := routabilityPocketFixture()
			mutate(s)
			rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions())
			if err == nil || rep.Status != "incomplete" || rep.Reason == "" {
				t.Fatalf("missing input must be incomplete, got %s err=%v", rep.Status, err)
			}
		})
	}
	opt := defaultRoutabilityOptions()
	opt.Components = []string{"NOPE"}
	if rep, err := analyzePCBRoutability(routabilityPocketFixture(), opt); err == nil || rep.Status != "incomplete" {
		t.Fatalf("unknown component filter must be incomplete: %s", rep.Status)
	}
}

func TestRoutabilityCommandIsReadOnlyAndReportsBlockers(t *testing.T) {
	s := routabilityPocketFixture()
	s.Copper.Vias = []any{map[string]any{"primitiveId": "via-gnd", "net": "GND", "x": 200.0, "y": 186.0, "holeDiameter": 12.0, "diameter": 24.0}}
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	out := filepath.Join(dir, "routability.json")
	raw, _ := json.Marshal(s)
	if err := os.WriteFile(board, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := newPcbRoutabilityCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", board, "--out", out, "--component", "J1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("trapped pads must exit non-zero")
	}
	if got, _ := os.ReadFile(board); !bytes.Equal(got, raw) {
		t.Fatal("read-only check modified its input")
	}
	var rep pcbRoutabilityReport
	blob, err := os.ReadFile(out)
	if err != nil || json.Unmarshal(blob, &rep) != nil {
		t.Fatalf("report unreadable: %v", err)
	}
	if rep.Status != "fail" || rep.BoardSHA256 != sha256String(raw) || rep.Summary.Trapped != 3 || rep.Summary.Checked != 10 {
		t.Fatalf("unexpected report %+v", rep.Summary)
	}
	if !strings.Contains(stderr.String(), "trapped J1.3") || !strings.Contains(stderr.String(), "via:via-gnd") {
		t.Fatalf("stderr lacks blocker summary: %s", stderr.String())
	}
	cmd = newPcbRoutabilityCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", board, "--out", board})
	if err := cmd.Execute(); err == nil {
		t.Fatal("output aliasing the input must be rejected")
	}
}

// Review round 1, item 1: an incomplete component/pad capture must never pass,
// even when the filter selects a different, fully captured component.
func TestRoutabilityIncompleteCaptureIsIncomplete(t *testing.T) {
	tests := map[string]func(*boardSnapshot){
		"components unavailable": func(s *boardSnapshot) {
			s.ComponentsAvailability = "unavailable"
			s.Components = nil
		},
		"one pad list unavailable": func(s *boardSnapshot) {
			s.Components[2].Availability = map[string]string{"pads": "unavailable"}
			s.Components[2].Pads = nil
		},
	}
	for name, mutate := range tests {
		for _, filter := range [][]string{nil, {"J1"}} {
			s := routabilityPocketFixture()
			mutate(s)
			opt := defaultRoutabilityOptions()
			opt.Components = filter
			rep, err := analyzePCBRoutability(s, opt)
			if err == nil || rep.Status != "incomplete" {
				t.Fatalf("%s filter=%v: got %s err=%v", name, filter, rep.Status, err)
			}
		}
	}
}

// Review round 1, item 2: a through via crosses inner layers too.
func TestRoutabilityViaSitesCheckInnerLayerCopper(t *testing.T) {
	s := routabilityPocketFixture()
	s.Copper.Lines = []any{map[string]any{"primitiveId": "trk-vbat-in", "net": "VBAT", "layer": float64(15), "startX": 140.0, "startY": 186.0, "endX": 260.0, "endY": 186.0, "lineWidth": 30.0}}
	rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions())
	if err == nil || rep.Status != "fail" {
		t.Fatalf("inner-layer track must block pocket vias: %s %v", rep.Status, err)
	}
	p := routabilityPad(t, rep, "J1.3")
	if p.Status != "trapped" || p.ViaSites != 0 || !routabilityHasBlocker(p, "track", "trk-vbat-in") {
		t.Fatalf("J1.3 should be trapped by the inner track: %+v", p)
	}
	for _, b := range p.Blockers {
		if b.ID == "trk-vbat-in" && b.Layer != 15 {
			t.Fatalf("inner blocker must keep its layer: %+v", b)
		}
	}
	// An inner track that leaves the pocket free keeps the via site legal.
	s.Copper.Lines = []any{map[string]any{"primitiveId": "trk-vbat-in", "net": "VBAT", "layer": float64(15), "startX": 140.0, "startY": 330.0, "endX": 260.0, "endY": 330.0, "lineWidth": 30.0}}
	if rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions()); err != nil || rep.Status != "pass" {
		t.Fatalf("distant inner track should not trap: %s %v", rep.Status, err)
	}
}

// Review round 1, item 3: rule provenance is per field, from parser to check.
func TestRoutabilityRequiresMeasuredRuleFields(t *testing.T) {
	// Only the track default width is present: the parser marks the board as
	// "live" but every field routability uses is still a default.
	partial := ruleUnitFixture("mil", "mil", "mil")
	cfg := mnav(partial, "rules", "config").(map[string]any)
	firstDataEntry(mnav(cfg, "Physics", "Track", "copperThickness1oz", "form", "data"))["defaultValue"] = 10.0
	delete(cfg, "Spacing")
	delete(mnav(cfg, "Physics").(map[string]any), "Via Size")
	parsed, err := parsePcbRules(partial)
	if err != nil || parsed.source != "live" {
		t.Fatalf("fixture premise: partial rule should parse as live: %+v %v", parsed, err)
	}
	s := routabilityPocketFixture()
	s.Rules = rulesToBoard(parsed)
	rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions())
	if err == nil || rep.Status != "incomplete" || !strings.Contains(rep.Reason, "viaDiameter") {
		t.Fatalf("partially defaulted rules must be incomplete: %s %s", rep.Status, rep.Reason)
	}
	// Snapshots that predate per-field provenance prove nothing field by field.
	s = routabilityPocketFixture()
	s.Rules.Measured = nil
	if rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions()); err == nil || rep.Status != "incomplete" {
		t.Fatalf("legacy rules must be incomplete: %s", rep.Status)
	}
	// A complete rule survives the snapshot JSON round trip and is accepted.
	full := ruleUnitFixture("mil", "mil", "mil")
	fcfg := mnav(full, "rules", "config")
	d := firstDataEntry(mnav(fcfg, "Physics", "Track", "copperThickness1oz", "form", "data"))
	d["defaultValue"], d["minValue"] = 10.0, 4.0
	via := mnav(fcfg, "Physics", "Via Size", "viaSize", "form").(map[string]any)
	via["viaInnerdiameterDefault"], via["viaOuterdiameterDefault"] = 12.0, 24.0
	rows := mnav(fcfg, "Spacing", "Safe Spacing", "copperThickness1oz", "tables", "1", "content").([]any)
	rows[0].([]any)[0], rows[1].([]any)[0], rows[3].([]any)[2] = 6.0, 6.0, 10.0
	parsed, err = parsePcbRules(full)
	if err != nil {
		t.Fatal(err)
	}
	s = routabilityPocketFixture()
	s.Rules = rulesToBoard(parsed)
	s = cloneEscapeSnapshot(s)
	if rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions()); err != nil || rep.Status != "pass" {
		t.Fatalf("fully measured rules should be accepted: %s %v", rep.Status, err)
	}
}

// Review round 1, item 4: an exit must leave the source pad's copper.
func TestRoutabilityLargePadExitIsMeasuredFromPadEdge(t *testing.T) {
	rect := func(id, net string, x, y, w, h float64) boardPad {
		return boardPad{ID: id, Number: id, Net: net, Layer: 1, X: x, Y: y, W: w, H: h, Shape: []any{"RECT", w, h, 0.0}}
	}
	build := func(open bool) *boardSnapshot {
		s := routabilityPocketFixture()
		s.Components = []boardComp{{ID: "u9", Designator: "U9", X: 600, Y: 600, Layer: 1, Pads: []boardPad{rect("1", "BIG", 600, 600, 220, 220)}}}
		walls := boardComp{ID: "w", Designator: "W1", X: 600, Y: 600, Layer: 1}
		// Other-net walls 8 mil from every side (too close for a 4 mil track at
		// 6 mil clearance) and 40 mil thick, closing the corners as well.
		walls.Pads = append(walls.Pads, rect("n", "WN", 600, 600-110-8-20, 316, 40), rect("w", "WW", 600-110-8-20, 600, 40, 316), rect("e", "WE", 600+110+8+20, 600, 40, 316))
		if !open {
			walls.Pads = append(walls.Pads, rect("s", "WS", 600, 600+110+8+20, 316, 40))
		}
		s.Components = append(s.Components, walls)
		s.Outline.Points = [][2]float64{{0, 0}, {1200, 0}, {1200, 1200}, {0, 1200}}
		return s
	}
	opt := defaultRoutabilityOptions()
	opt.Components = []string{"U9"}
	rep, err := analyzePCBRoutability(build(false), opt)
	if err == nil || rep.Status != "fail" {
		t.Fatalf("sealed large pad must be trapped: %s %v", rep.Status, err)
	}
	if p := routabilityPad(t, rep, "U9.1"); len(p.EscapeDirections) != 0 || p.ViaSites != 0 {
		t.Fatalf("no exit may be claimed inside the source pad: %+v", p)
	}
	rep, err = analyzePCBRoutability(build(true), opt)
	if err != nil || rep.Status != "pass" {
		t.Fatalf("opened side should release the pad: %s %v", rep.Status, err)
	}
	if p := routabilityPad(t, rep, "U9.1"); !containsString(p.EscapeDirections, "+Y") {
		t.Fatalf("open side must be reported as the escape: %+v", p.EscapeDirections)
	}
}

// Review rounds 1 (item 5) and 2 (item 3): a reachable same-net via or
// through-hole pad is a layer change only with proof it is plated and spans the
// source layer; unproven evidence is unverified, never pass.
func TestRoutabilityReachableSameNetTransitionNeedsProof(t *testing.T) {
	sameNetVia := []any{map[string]any{"primitiveId": "via-sig3", "net": "SIG3", "x": 200.0, "y": 186.0, "holeDiameter": 12.0, "diameter": 24.0}}
	// Four copper layers: the via readback has no type or layer span, so a
	// blind/buried via cannot be excluded.
	s := routabilityPocketFixture()
	s.Copper.Vias = sameNetVia
	rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions())
	p := routabilityPad(t, rep, "J1.3")
	// J1.2/J1.4 (other nets) stay trapped by the via, so the report fails.
	if err == nil || rep.Status != "fail" || p.Status != "unverified" || len(p.ExistingTransitions) != 0 || len(p.UnverifiedTransitions) != 1 || rep.Summary.Unverified != 1 {
		t.Fatalf("4-layer same-net via must be unverified: %s %+v", rep.Status, p)
	}
	if routabilityHasBlocker(p, "via", "via-sig3") {
		t.Fatalf("same-net transition listed as blocker: %+v", p.Blockers)
	}
	// When the unproven transition is the only way out for every pad that
	// lacks an exit, the report is incomplete, not pass.
	for i := range s.Components[0].Pads {
		if n := s.Components[0].Pads[i].Number; n == "2" || n == "4" {
			s.Components[0].Pads[i].Net = "SIG3"
		}
	}
	rep, err = analyzePCBRoutability(s, defaultRoutabilityOptions())
	if err == nil || rep.Status != "incomplete" || rep.Summary.Unverified != 3 || rep.Summary.Trapped != 0 {
		t.Fatalf("unproven-only escape must be incomplete: %s %+v", rep.Status, rep.Summary)
	}
	// Two copper layers: every via is TOP-BOTTOM, so the via is proven. The
	// via fills the pocket, so the pass rests on the transition alone.
	s.CopperLayers = 2
	rep, err = analyzePCBRoutability(s, defaultRoutabilityOptions())
	p = routabilityPad(t, rep, "J1.3")
	if err != nil || rep.Status != "pass" || p.ViaSites != 0 || len(p.EscapeDirections) != 0 || len(p.ExistingTransitions) != 1 || p.ExistingTransitions[0].ID != "via-sig3" {
		t.Fatalf("2-layer same-net via should pass as a transition: %s %+v", rep.Status, p)
	}

	hole := func(metal *bool, drill any) *boardSnapshot {
		s := routabilityPocketFixture()
		s.Components = append(s.Components, boardComp{ID: "h1", Designator: "H1", X: 200, Y: 186, Layer: 1, Pads: []boardPad{{ID: "h1-1", Number: "1", Net: "SIG3", Layer: pcbLayerMulti, X: 200, Y: 186, W: 24, H: 24, Shape: []any{"ELLIPSE", 24.0, 24.0}, Hole: drill, Metallization: metal}}})
		return s
	}
	yes, no := true, false
	// Non-plated drill: connects nothing, so J1.3 is plainly trapped.
	rep, _ = analyzePCBRoutability(hole(&no, []any{"ROUND", 1.2, 1.2}), defaultRoutabilityOptions())
	if p := routabilityPad(t, rep, "J1.3"); rep.Status != "fail" || p.Status != "trapped" || len(p.ExistingTransitions)+len(p.UnverifiedTransitions) != 0 || !routabilityHasBlocker(p, "pad", "H1.1") {
		t.Fatalf("non-plated hole must not be a transition: %s %+v", rep.Status, p)
	}
	// Unknown metallization: unverified (neighbours stay trapped by H1).
	rep, _ = analyzePCBRoutability(hole(nil, []any{"ROUND", 1.2, 1.2}), defaultRoutabilityOptions())
	if p := routabilityPad(t, rep, "J1.3"); p.Status != "unverified" || len(p.UnverifiedTransitions) != 1 || p.UnverifiedTransitions[0].Ref != "H1.1" {
		t.Fatalf("unknown metallization must be unverified: %s %+v", rep.Status, p)
	}
	// Proven plated through hole: pass on the transition alone.
	rep, _ = analyzePCBRoutability(hole(&yes, []any{"ROUND", 1.2, 1.2}), defaultRoutabilityOptions())
	if p := routabilityPad(t, rep, "J1.3"); p.Status != "pass" || len(p.ExistingTransitions) != 1 || p.ExistingTransitions[0].Ref != "H1.1" {
		t.Fatalf("plated through hole should be a transition: %s %+v", rep.Status, p)
	}
}

// Review round 2, item 1: a board slot is not copper-free space for vias or
// tracks; its edge keeps the cutout clearance.
func TestRoutabilitySlotBlocksViaSites(t *testing.T) {
	s := routabilityPocketFixture()
	s.Copper.Fills = []any{map[string]any{"primitiveId": "slot-1", "layer": float64(pcbLayerMulti), "geometryAvailable": true, "fillMode": "solid", "lineWidth": 0.0, "source": []any{160.0, 168.0, "L", 240.0, 168.0, 240.0, 211.0, 160.0, 211.0}}}
	rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions())
	p := routabilityPad(t, rep, "J1.3")
	if err == nil || rep.Status != "fail" || p.Status != "trapped" || p.ViaSites != 0 || !routabilityHasBlocker(p, "slot", "slot-1") {
		t.Fatalf("slot must block pocket vias: %s %+v", rep.Status, p)
	}
	// Unknown cutout stroke/fill semantics is incomplete, not free space.
	s.Copper.Fills[0].(map[string]any)["fillMode"] = "unknown"
	if rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions()); err == nil || rep.Status != "incomplete" {
		t.Fatalf("unknown cutout semantics must be incomplete: %s", rep.Status)
	}
}

// Review round 2, item 2: copper extends half the pour stroke width beyond
// the polygon outline.
func TestRoutabilityPourStrokeWidthCounts(t *testing.T) {
	pour := func(width any) *boardSnapshot {
		s := routabilityPocketFixture()
		s.Copper.Poured = []any{map[string]any{"primitiveId": "pour-vbat", "net": "VBAT", "layer": float64(1), "fills": []any{map[string]any{"id": "vbat-fill", "fill": true, "lineWidth": width, "source": []any{100.0, 152.0, "L", 300.0, 152.0, 300.0, 100.0, 100.0, 100.0}}}}}
		return s
	}
	// Outline top edge y=152; with an 80 mil stroke the copper reaches y=192.
	rep, err := analyzePCBRoutability(pour(80.0), defaultRoutabilityOptions())
	p := routabilityPad(t, rep, "J1.3")
	if err == nil || rep.Status != "fail" || p.ViaSites != 0 || !routabilityHasBlocker(p, "copper", "vbat-fill") {
		t.Fatalf("stroked pour must reach the pocket: %s %+v", rep.Status, p)
	}
	if rep, err := analyzePCBRoutability(pour(0.0), defaultRoutabilityOptions()); err != nil || rep.Status != "pass" {
		t.Fatalf("unstroked pour above the pocket should not trap: %s %v", rep.Status, err)
	}
	if rep, err := analyzePCBRoutability(pour(nil), defaultRoutabilityOptions()); err == nil || rep.Status != "incomplete" {
		t.Fatalf("unknown stroke width must be incomplete: %s", rep.Status)
	}
}

// Review round 2, item 4: the nearest via site is the Euclidean nearest legal
// grid point, not the first one found in flood order.
func TestRoutabilityNearestViaSiteIsEuclidean(t *testing.T) {
	s := routabilityPocketFixture()
	s.Components = []boardComp{{ID: "u1", Designator: "U1", X: 200, Y: 200, Layer: 1, Pads: []boardPad{{ID: "u1-1", Number: "1", Net: "A", Layer: 1, X: 200, Y: 200, W: 60, H: 60, Shape: []any{"RECT", 60.0, 60.0, 0.0}}}}}
	rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions())
	p := routabilityPad(t, rep, "U1.1")
	// Via edge needs 6 mil from the 30 mil half-width pad: centre 30+6+12 = 48.
	if err != nil || p.NearestViaSite == nil || p.NearestViaDistMil > 48+1e-6 || p.ViaSites != pcbRoutabilityViaSiteCap || !p.ViaSitesCapped {
		t.Fatalf("nearest via should be 48 mil away with the cap reached: %+v %v", p, err)
	}
}

// Review round 3, item 1: unfilled poured copper (connector thermal spokes,
// hollow outlines) is only its stroked path, never a solid area.
func TestRoutabilityUnfilledPourIsStrokedPath(t *testing.T) {
	pour := func(width any, source []any) *boardSnapshot {
		s := routabilityPocketFixture()
		s.Copper.Poured = []any{map[string]any{"primitiveId": "power", "net": "VBAT", "layer": 1.0, "fills": []any{map[string]any{"id": "path", "lineWidth": width, "fill": false, "geometryKind": "stroked-thermal-spoke-path", "source": source}}}}
		return s
	}
	// Two-point spoke exactly as pcb.poured.list returns it (nested path).
	if rep, err := analyzePCBRoutability(pour(10.0, []any{[]any{100.0, 200.0, "L", 300.0, 400.0}}), defaultRoutabilityOptions()); err != nil || rep.Status != "pass" || rep.Summary.Checked == 0 {
		t.Fatalf("two-point spoke must parse and stay out of the pocket: %s %v", rep.Status, err)
	}
	// Closed hollow perimeter around the pocket, far from every via site.
	perimeter := []any{70.0, 60.0, "L", 330.0, 60.0, 330.0, 300.0, 70.0, 300.0, 70.0, 60.0}
	if rep, err := analyzePCBRoutability(pour(10.0, perimeter), defaultRoutabilityOptions()); err != nil || rep.Status != "pass" {
		t.Fatalf("hollow outline must not act as solid copper: %s %v %v", rep.Status, err, rep.Trapped)
	}
	// A hollow outline whose stroke really runs through the pocket blocks it,
	// and is reported by its id on TOP.
	through := []any{140.0, 186.0, "L", 260.0, 186.0, 260.0, 380.0, 140.0, 380.0, 140.0, 186.0}
	rep, err := analyzePCBRoutability(pour(30.0, through), defaultRoutabilityOptions())
	p := routabilityPad(t, rep, "J1.3")
	if err == nil || rep.Status != "fail" || p.Status != "trapped" || !routabilityHasBlocker(p, "copper", "path") {
		t.Fatalf("stroke through the pocket must trap J1.3: %s %+v", rep.Status, p)
	}
	// An unfilled path without a positive stroke width has unknown copper.
	if rep, err := analyzePCBRoutability(pour(0.0, perimeter), defaultRoutabilityOptions()); err == nil || rep.Status != "incomplete" {
		t.Fatalf("zero-width unfilled path must be incomplete: %s", rep.Status)
	}
}

// Review round 3, item 2: a plated hole proves a layer change only with a
// valid measured drill that leaves copper on the pad.
func TestRoutabilityPlatedHoleNeedsValidDrill(t *testing.T) {
	plated := true
	for _, tc := range []struct {
		name  string
		drill any
		want  string
	}{
		{"missing size", []any{"ROUND"}, "unverified"},
		{"zero size", []any{"ROUND", 0.0, 0.0}, "unverified"},
		{"not finite tuple", []any{"ROUND", "x", 1.2}, "unverified"},
		{"unknown kind", []any{"OVAL?", 1.2, 1.2}, "unverified"},
		{"drill wider than pad", []any{"ROUND", 2.4, 2.4}, "unverified"},
		{"valid plated hole", []any{"ROUND", 1.2, 1.2}, "pass"},
	} {
		s := routabilityPocketFixture()
		s.Components = append(s.Components, boardComp{ID: "h1", Designator: "H1", Layer: 1, Pads: []boardPad{{ID: "h", Number: "1", Net: "SIG3", Layer: pcbLayerMulti, X: 200, Y: 186, W: 24, H: 24, Shape: []any{"ELLIPSE", 24.0, 24.0}, Hole: tc.drill, Metallization: &plated}}})
		rep, _ := analyzePCBRoutability(s, defaultRoutabilityOptions())
		p := routabilityPad(t, rep, "J1.3")
		if p.Status != tc.want || p.ViaSites != 0 || len(p.EscapeDirections) != 0 {
			t.Fatalf("%s: J1.3 = %s, want %s (%+v)", tc.name, p.Status, tc.want, p)
		}
		if tc.want == "unverified" && (len(p.UnverifiedTransitions) != 1 || p.UnverifiedTransitions[0].Reason == "") {
			t.Fatalf("%s: unverified transition must carry a reason: %+v", tc.name, p)
		}
	}
}

// Review round 4: copper whose layer is missing or malformed is unknown
// copper, never removed from the obstacle set. Command-level, both inputs.
func TestRoutabilityCopperLayerMustBeMeasured(t *testing.T) {
	poly := []any{150.0, 155.0, "L", 250.0, 155.0, 250.0, 215.0, 150.0, 215.0}
	for _, kind := range []string{"static", "poured"} {
		for _, layer := range []any{1.0, nil, "1", 1.5} {
			s := routabilityPocketFixture()
			object := map[string]any{"primitiveId": "power", "net": "VBAT"}
			if layer != nil {
				object["layer"] = layer
			}
			if kind == "static" {
				object["geometryAvailable"], object["fillMode"], object["lineWidth"], object["source"] = true, "solid", 0.0, poly
				s.Copper.Fills = []any{object}
			} else {
				object["fills"] = []any{map[string]any{"id": "island", "fill": true, "lineWidth": 0.0, "source": poly}}
				s.Copper.Poured = []any{object}
			}
			dir := t.TempDir()
			board, out := filepath.Join(dir, "board.json"), filepath.Join(dir, "report.json")
			raw, _ := json.Marshal(s)
			if err := os.WriteFile(board, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			cmd := newPcbRoutabilityCmd(&stdout, &stderr)
			cmd.SetArgs([]string{"--board", board, "--out", out})
			execErr := cmd.Execute()
			var rep pcbRoutabilityReport
			blob, err := os.ReadFile(out)
			if err != nil || json.Unmarshal(blob, &rep) != nil {
				t.Fatalf("%s layer=%v: report unreadable: %v", kind, layer, err)
			}
			want := "incomplete"
			if layer == 1.0 {
				want = "fail" // measured TOP copper traps J1.2–J1.4
			}
			if rep.Status != want || execErr == nil {
				t.Fatalf("%s layer=%v: status=%s err=%v trapped=%v, want %s with non-zero exit", kind, layer, rep.Status, execErr, rep.Trapped, want)
			}
		}
	}
	// A poured layer that is not copper is unsupported, not ignored.
	s := routabilityPocketFixture()
	s.Copper.Poured = []any{map[string]any{"primitiveId": "power", "net": "VBAT", "layer": 3.0, "fills": []any{map[string]any{"id": "island", "fill": true, "lineWidth": 0.0, "source": poly}}}}
	if rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions()); err == nil || rep.Status != "incomplete" {
		t.Fatalf("non-copper poured layer must be incomplete: %s", rep.Status)
	}
}

// polygonPad builds an absolute-coordinate POLYGON pad (straight edges) with
// its origin at (x, y) and copper over the rectangle [x0,x1]x[y0,y1].
func polygonPad(id, num, net string, layer int, x, y, x0, y0, x1, y1 float64) boardPad {
	return boardPad{ID: id, Number: num, Net: net, Layer: layer, X: x, Y: y,
		Shape: []any{"POLYGON", []any{x0, y0, "L", x1, y0, x1, y1, x0, y1, x0, y0}}}
}

// #306: straight-edged POLYGON pads (e.g. USB-C TYPE-C-31-M-12 shell pins)
// are exact copper for clearance, so they no longer make the board incomplete
// and they still block like any other pad.
func TestRoutabilityStraightPolygonPadIsMeasuredObstacle(t *testing.T) {
	s := routabilityPocketFixture()
	s.Components = append(s.Components, boardComp{ID: "u2", Designator: "U2", X: 200, Y: 186, Layer: 1,
		Pads: []boardPad{polygonPad("u2-1", "1", "PWR", 1, 200, 186, 155, 156, 245, 216)}})
	if c := s.geometryCoverage(false); c.Complete || c.ExactClearancePads != 1 {
		t.Fatalf("pad-contact coverage must stay incomplete and count the polygon: %+v", c)
	}
	rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions())
	if err == nil || rep.Status != "fail" {
		t.Fatalf("expected fail, got %s err=%v", rep.Status, err)
	}
	if p := routabilityPad(t, rep, "J1.3"); p.Status != "trapped" || !routabilityHasBlocker(p, "pad", "U2.1") {
		t.Fatalf("polygon pad should trap J1.3: %+v", p)
	}
}

// A POLYGON source pad need not contain its origin: the flood starts on its
// copper, not at the origin.
func TestRoutabilityPolygonSourcePadSeedsOnCopper(t *testing.T) {
	s := routabilityPocketFixture()
	s.Components = append(s.Components, boardComp{ID: "p1", Designator: "P1", X: 320, Y: 320, Layer: 2,
		Pads: []boardPad{polygonPad("p1-1", "1", "SIGP", 2, 300, 300, 330, 330, 350, 350)}},
		// A foreign pad covers the origin: a flood seeded there finds nothing.
		boardComp{ID: "r9", Designator: "R9", X: 300, Y: 300, Layer: 2,
			Pads: []boardPad{{ID: "r9-1", Number: "1", Net: "OTHER", Layer: 2, X: 300, Y: 300, W: 20, H: 20, Shape: []any{"RECT", 20.0, 20.0, 0.0}}}})
	opt := defaultRoutabilityOptions()
	opt.Components = []string{"P1"}
	rep, err := analyzePCBRoutability(s, opt)
	if err != nil || rep.Status != "pass" {
		t.Fatalf("status=%s err=%v reason=%s", rep.Status, err, rep.Reason)
	}
	if p := routabilityPad(t, rep, "P1.1"); p.ViaSites == 0 || len(p.EscapeDirections) == 0 {
		t.Fatalf("open-field polygon pad should escape: %+v", p)
	}
}

// Arc-edged POLYGON pads are not chord-flattened: still incomplete.
func TestRoutabilityArcPolygonPadStaysIncomplete(t *testing.T) {
	s := routabilityPocketFixture()
	s.Components = append(s.Components, boardComp{ID: "u3", Designator: "U3", X: 320, Y: 320, Layer: 1,
		Pads: []boardPad{{ID: "u3-1", Number: "1", Net: "X", Layer: 1, X: 320, Y: 320,
			Shape: []any{"POLYGON", []any{310.0, 320.0, "ARC", 180.0, 330.0, 320.0, "L", 310.0, 320.0}}}}})
	rep, err := analyzePCBRoutability(s, defaultRoutabilityOptions())
	if err == nil || rep.Status != "incomplete" || !strings.Contains(rep.Reason, "unsupported by exact clearance") {
		t.Fatalf("got %s err=%v reason=%s", rep.Status, err, rep.Reason)
	}
}

// Live USB-C fixture (testdata/pcb_polygon_pad): its 4 straight-edged POLYGON
// shell pads made the first live run incomplete; they are now measured.
func TestRoutabilityLiveUSBCPolygonPadsAreMeasured(t *testing.T) {
	data, err := os.ReadFile("testdata/pcb_polygon_pad/usb-c-component.json")
	if err != nil {
		t.Fatal(err)
	}
	var comp map[string]any
	if err := json.Unmarshal(data, &comp); err != nil {
		t.Fatal(err)
	}
	// The 2026-09 capture predates per-pad rotation; these pads are unrotated.
	for _, raw := range comp["pads"].([]any) {
		if pad := raw.(map[string]any); pad["rotation"] == nil {
			pad["rotation"] = 0.0
		}
	}
	s := routabilityPocketFixture()
	s.Components = parseBoardComponents(map[string]any{"components": []any{comp}})
	s.Outline = &boardOutline{Source: "polygon", Format: "polyline", BBox: layoutBBox{MinX: 1500, MinY: -1100, MaxX: 2100, MaxY: -500},
		Points: [][2]float64{{1500, -1100}, {2100, -1100}, {2100, -500}, {1500, -500}}}
	if c := s.geometryCoverage(false); c.UnsupportedGeometryPads != 4 || c.ExactClearancePads != 4 {
		t.Fatalf("fixture should carry 4 exact-clearance polygon pads: %+v", c)
	}
	rep, _ := analyzePCBRoutability(s, defaultRoutabilityOptions())
	if rep.Status == "incomplete" {
		t.Fatalf("polygon pads must not make the board incomplete: %s", rep.Reason)
	}
	polygons := 0
	for _, p := range rep.Pads {
		if p.Net == "GND" || p.Net == "USB_VBUS" {
			polygons++
		}
	}
	if polygons < 4 {
		t.Fatalf("polygon pads missing from report: %+v", rep.Pads)
	}
}
