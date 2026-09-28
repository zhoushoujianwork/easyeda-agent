package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUnboundedMeasuredZones(t *testing.T) {
	for _, name := range []string{"usb", "mux", "uart", "boot", "mcu", "input", "slew", "buck", "detect", "led"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("../../docs/reviews/fixtures/2026-09-27-cli-layout", name+"-input.json"))
			if err != nil {
				t.Fatal(err)
			}
			in, err := decodeSchematicZonesInput(raw)
			if err != nil {
				t.Fatal(err)
			}
			in.LayoutMode, in.Optimization = "unbounded", nil
			before, _ := json.Marshal(in)
			out, err := PlanSchematicZones(in)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Zones) != 1 {
				t.Fatal("incomplete output")
			}
			layout := out.Zones[0].Layout
			if layout.Search.Strategy != "unbounded-channels-v1" {
				t.Fatal("wrong strategy")
			}
			for _, c := range in.Components {
				p := powerLayoutPlan{Placements: layout.Placements}
				actual := libPlacementByDesignator(&p, c.Measurement.Designator)
				if actual == nil || actual.Rotation != c.Measurement.Rotation || actual.Mirror != c.Measurement.Mirror {
					t.Fatal("measured pose lost")
				}
				if layout.ComponentIDs[actual.Designator] != c.ID {
					t.Fatal("ownership lost")
				}
				if len(layout.PinStates[c.ID]) != len(c.PinStates) {
					t.Fatal("NC/unconnected contract changed")
				}
				for number, state := range c.PinStates {
					if layout.PinStates[c.ID][number] != state {
						t.Fatal("NC/unconnected contract changed")
					}
				}
				want := plTranslate(c.Measurement, actual.X-c.Measurement.X, actual.Y-c.Measurement.Y)
				if !reflect.DeepEqual(want, *actual) {
					t.Fatal("measured body, text or pins changed")
				}
			}
			after, _ := json.Marshal(in)
			if !bytes.Equal(before, after) {
				t.Fatal("source mutated")
			}
			repeat, err := PlanSchematicZones(in)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(out)
			b, _ := json.Marshal(repeat)
			if !bytes.Equal(a, b) {
				t.Fatal("nondeterministic layout")
			}
			in.MaxCandidates = 1
			if partial, err := PlanSchematicZones(in); err == nil || partial != nil {
				t.Fatal("budget allowed partial result")
			}
		})
	}
}

func TestUnboundedFormerlyExhaustedPages(t *testing.T) {
	for _, name := range []string{"P1-20k", "P2-20k"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("../../docs/reviews/fixtures/2026-09-29-unbounded-layout", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			in, err := decodeSchematicZonesInput(raw)
			if err != nil {
				t.Fatal(err)
			}
			in.LayoutMode = "unbounded"
			out, err := PlanSchematicZones(in)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Zones) != len(in.Zones) || out.CandidatesUsed > 100 {
				t.Fatal("incomplete result or search regression")
			}
			crossings := 0
			for _, zone := range out.Zones {
				layout := zone.Layout
				if findings := layoutContractRuntimeFindings(t, layout); len(findings) != 0 {
					t.Fatalf("runtime guard: %+v", findings)
				}
				p := powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}
				for i, a := range p.Wires {
					for _, b := range p.Wires[:i] {
						if a.Net != b.Net && plSegmentsMeet(a.Points[0], a.Points[1], b.Points[0], b.Points[1]) {
							if plSegmentsContact(a.Points[0], a.Points[1], b.Points[0], b.Points[1]) {
								t.Fatal("foreign-net contact escaped gate")
							}
							crossings++
						}
					}
				}
			}
			if crossings == 0 {
				t.Fatal("fixture no longer exercises allowed X crossings")
			}
			packet, _ := json.Marshal(out)
			var input SchematicRenderInput
			if err := json.Unmarshal(packet, &input); err != nil {
				t.Fatal(err)
			}
			if _, err := RenderSchematicLayoutSVG(input); err != nil {
				t.Fatal("paper-free render failed", err)
			}
		})
	}
}

func TestUnboundedModeRejectsInvalidContracts(t *testing.T) {
	for name, edit := range map[string]func(*SchematicLayoutInput){
		"unknown mode": func(in *SchematicLayoutInput) { in.LayoutMode = "infinite" },
		"optimization": func(in *SchematicLayoutInput) { in.Optimization = &SchematicLayoutOptimization{MaxAttempts: 1} },
		"unknown peripheral pin": func(in *SchematicLayoutInput) {
			in.Attachments = []SchematicLayoutPeripheral{{ComponentID: "peripheral", PinNumber: "missing"}}
		},
		"unknown host pin": func(in *SchematicLayoutInput) {
			in.Attachments = []SchematicLayoutPeripheral{{ComponentID: "peripheral", AttachTo: &SchematicLayoutAttach{ComponentID: "anchor", PinNumber: "missing"}}}
		},
		"mismatched attachment net": func(in *SchematicLayoutInput) {
			in.Attachments = []SchematicLayoutPeripheral{{ComponentID: "peripheral", PinNumber: "2", AttachTo: &SchematicLayoutAttach{ComponentID: "anchor", PinNumber: "1"}}}
		},
		"blocked measured pin exit": func(in *SchematicLayoutInput) {
			in.Components[0].Measurement.TextBBoxes = []SchematicBox{{129, 99, 136, 101}}
		},
		"unknown NC":              func(in *SchematicLayoutInput) { in.Components[0].PinStates = nil },
		"foreign coincident pins": func(in *SchematicLayoutInput) { in.Components[1].Measurement.Pins[1].X = 280 },
		"explicit marker anchor": func(in *SchematicLayoutInput) {
			in.MarkerAnchors = []SchematicMarkerAnchor{{Type: "pin", ComponentID: "anchor", PinNumber: "1"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := standaloneLayoutFixture()
			in.LayoutMode = "unbounded"
			edit(&in)
			if out, err := PlanSchematicLayout(in); err == nil || out != nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
}

func TestUnboundedCLIRecordsOverrideAndPreservesFailedOutput(t *testing.T) {
	for _, fail := range []bool{false, true} {
		dir := t.TempDir()
		from, out, report := filepath.Join(dir, "input.json"), filepath.Join(dir, "layout.json"), filepath.Join(dir, "report.json")
		in := standaloneLayoutFixture()
		if fail {
			in.MaxCandidates = 1
		}
		raw, _ := json.Marshal(in)
		if err := os.WriteFile(from, raw, 0600); err != nil {
			t.Fatal(err)
		}
		previous := []byte("previous validated layout")
		if err := os.WriteFile(out, previous, 0600); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		cmd := newSchLayoutPlanCmd(&stdout)
		cmd.SetArgs([]string{"--unbounded", "--from", from, "--out", out, "--report", report})
		if err := cmd.Execute(); (err != nil) != fail {
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
		if r.LayoutMode != "unbounded" || r.SourceSHA256 != sha256Hex(raw) || r.GlobalInfeasibilityProven {
			t.Fatal("wrong report evidence")
		}
		kept, _ := os.ReadFile(from)
		if !bytes.Equal(kept, raw) {
			t.Fatal("source changed")
		}
		if fail {
			kept, _ = os.ReadFile(out)
			if !bytes.Equal(kept, previous) || stdout.Len() != 0 || r.FailureClass != "candidate-budget-exhausted" {
				t.Fatal("partial output on failure")
			}
		}
	}
}
