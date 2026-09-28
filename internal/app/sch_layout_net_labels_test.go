package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNetLabelsExplicitlyReplacesDirectPaths(t *testing.T) {
	in := layoutContractFixture()
	in.LayoutMode = "net-labels"
	in.NetPolicies["SUPPLY"] = "direct"
	in.Attachments = []SchematicLayoutPeripheral{{ComponentID: "peripheral", PinNumber: "1", AttachTo: &SchematicLayoutAttach{ComponentID: "anchor", PinNumber: "1"}}}
	before, _ := json.Marshal(in)
	out, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.LayoutMode != "net-labels" || out.Search.Strategy != "net-labels-v1" {
		t.Fatal("mode override not explicit")
	}
	p := powerLayoutPlan{Placements: out.Placements, Wires: out.Wires, Flags: out.Flags}
	a, _ := libPin(p.Placements[0], "1")
	b, _ := libPin(p.Placements[1], "1")
	if libPinsShareIsland(&p, a, b) {
		t.Fatal("label mode retained inter-component wires")
	}
	if err = validateSchCompositionNets(&p); err != nil {
		t.Fatal("lost named electrical connection", err)
	}
	if findings := layoutContractRuntimeFindings(t, out); len(findings) != 0 {
		t.Fatal(findings)
	}
	if out.PinStates["anchor"]["3"] != "nc" {
		t.Fatal("NC lost")
	}
	for _, f := range out.Flags {
		if f.Offset <= 0 || f.Anchor == nil {
			t.Fatal("missing real marker lead/anchor")
		}
	}
	for _, c := range in.Components {
		actual := libPlacementByDesignator(&p, c.Measurement.Designator)
		want := plTranslate(c.Measurement, actual.X-c.Measurement.X, actual.Y-c.Measurement.Y)
		if !reflect.DeepEqual(want, *actual) {
			t.Fatal("measured pose/geometry changed")
		}
	}
	again, err := PlanSchematicLayout(in)
	if err != nil || !reflect.DeepEqual(again, out) {
		t.Fatal("nondeterministic", err)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("source direct policy or attachment mutated")
	}
	in.LayoutMode = ""
	physical, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	p = powerLayoutPlan{Placements: physical.Placements, Wires: physical.Wires, Flags: physical.Flags}
	a, _ = libPin(p.Placements[0], "1")
	b, _ = libPin(p.Placements[1], "1")
	if !libPinsShareIsland(&p, a, b) {
		t.Fatal("default mode silently downgraded physical attachment")
	}
}

func TestNetLabelsPowerIssue273(t *testing.T) {
	for _, name := range []string{"input", "boost-input"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("../../docs/reviews/fixtures/2026-09-29-power-layout", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			in, err := decodeLibLayout(raw)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			from, out, report := filepath.Join(dir, "source.json"), filepath.Join(dir, "layout.json"), filepath.Join(dir, "report.json")
			if err = os.WriteFile(from, raw, 0600); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			cmd := newSchLayoutPlanCmd(&stdout)
			cmd.SetArgs([]string{"--lib", "--net-labels", "--from", from, "--out", out, "--report", report})
			if err = cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			packet, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			var result SchematicZonesResult
			if err = json.Unmarshal(packet, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Zones) != 1 || result.CandidatesUsed > in.MaxCandidates {
				t.Fatal("incomplete or over budget")
			}
			layout := result.Zones[0].Layout
			if layout.LayoutMode != "net-labels" || len(layout.Placements) != len(in.Measurements) {
				t.Fatal("lost mode/parts")
			}
			if findings := layoutContractRuntimeFindings(t, layout); len(findings) != 0 {
				t.Fatal(findings)
			}
			p := powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}
			for _, original := range in.Measurements {
				actual := libPlacementByDesignator(&p, original.Designator)
				if actual == nil {
					t.Fatal("lost component")
				}
				if !reflect.DeepEqual(plTranslate(original, actual.X-original.X, actual.Y-original.Y), *actual) {
					t.Fatal("measurement changed", original.Designator)
				}
			}
			if err = validateSchCompositionNets(&p); err != nil {
				t.Fatal(err)
			}
			for i, c := range p.Placements {
				for _, other := range p.Placements[:i] {
					for _, a := range c.Pins {
						for _, b := range other.Pins {
							if a.Net != "" && a.Net == b.Net && libPinsShareIsland(&p, a, b) {
								t.Fatal("unexpected inter-component physical path")
							}
						}
					}
				}
			}
			var render SchematicRenderInput
			if err = json.Unmarshal(packet, &render); err != nil {
				t.Fatal(err)
			}
			if _, err = RenderSchematicLayoutSVG(render); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			var r schLayoutReport
			if err = json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			if r.SourceSHA256 != sha256Hex(raw) || r.LayoutMode != "net-labels" || r.Status != "planned" {
				t.Fatal("wrong provenance")
			}
		})
	}
}

func TestNetLabelsKeepsGuards(t *testing.T) {
	for name, edit := range map[string]func(*SchematicLayoutInput){
		"budget":       func(in *SchematicLayoutInput) { in.MaxCandidates = 1 },
		"optimization": func(in *SchematicLayoutInput) { in.Optimization = &SchematicLayoutOptimization{MaxAttempts: 1} },
		"foreign attachment": func(in *SchematicLayoutInput) {
			in.Attachments = []SchematicLayoutPeripheral{{ComponentID: "peripheral", PinNumber: "2", AttachTo: &SchematicLayoutAttach{ComponentID: "anchor", PinNumber: "1"}}}
		},
		"blocked exit": func(in *SchematicLayoutInput) {
			in.Components[0].Measurement.TextBBoxes = []layoutBBox{{129, 99, 136, 101}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := standaloneLayoutFixture()
			in.LayoutMode = "net-labels"
			edit(&in)
			if out, err := PlanSchematicLayout(in); err == nil || out != nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
	var stdout bytes.Buffer
	cmd := newSchLayoutPlanCmd(&stdout)
	cmd.SetArgs([]string{"--unbounded", "--net-labels", "--from", "unused.json"})
	if err := cmd.Execute(); err == nil || stdout.Len() != 0 {
		t.Fatal("conflicting flags accepted")
	}
}
