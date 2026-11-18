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
		Rules:        &boardRules{Source: "live", ClearanceMil: 6, TrackWidthMinMil: 4, CopperToEdgeMil: 10, ViaDiameterMil: 24, ViaDrillMil: 12},
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
