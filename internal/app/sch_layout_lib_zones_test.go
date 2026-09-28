package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPowerIssue273LocalLayoutPreservesCanonicalEvidence(t *testing.T) {
	for _, name := range []string{"input", "boost-input"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("../../docs/reviews/fixtures/2026-09-29-power-layout", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			input, err := decodeLibLayout(raw)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(input)
			result, err := planLibLayoutZones(input, "unbounded")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Zones) != 1 || result.CandidatesUsed > 200 {
				t.Fatal("incomplete result or expanded placement regression")
			}
			layout := result.Zones[0].Layout
			p := powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}
			if len(p.Placements) != len(input.Measurements) {
				t.Fatal("lost components")
			}
			for _, original := range input.Measurements {
				actual := libPlacementByDesignator(&p, original.Designator)
				if actual == nil {
					t.Fatal("missing component", original.Designator)
				}
				want := plTranslate(original, actual.X-original.X, actual.Y-original.Y)
				if !reflect.DeepEqual(want, *actual) {
					t.Fatal("source body/text/pin geometry or pose changed", original.Designator)
				}
			}
			for _, component := range input.Connectivity.Components {
				if layout.ComponentIDs[component.Ref] != component.ID {
					t.Fatal("identity lost")
				}
				for _, pin := range component.Pins {
					want := ""
					if pin.NoConnected {
						want = "nc"
					} else if pin.ConnectionState == "unconnected" {
						want = "unconnected"
					}
					if layout.PinStates[component.ID][pin.Number] != want {
						t.Fatal("pin state changed")
					}
				}
			}
			// Expanded mode promises real paths for every same-net pin in this
			// module, including all declared attachment pairs, not label islands.
			first := map[string]powerLayoutPin{}
			for _, c := range p.Placements {
				for _, pin := range c.Pins {
					if pin.Net == "" {
						continue
					}
					if prior, ok := first[pin.Net]; ok {
						if !libPinsShareIsland(&p, prior, pin) {
							t.Fatal("same-name labels replaced direct wire", pin.Net)
						}
					} else {
						first[pin.Net] = pin
					}
				}
			}
			if findings := layoutContractRuntimeFindings(t, layout); len(findings) != 0 {
				t.Fatalf("runtime guard: %+v", findings)
			}
			packet, _ := json.Marshal(result)
			var render SchematicRenderInput
			if err = json.Unmarshal(packet, &render); err != nil {
				t.Fatal(err)
			}
			if _, err = RenderSchematicLayoutSVG(render); err != nil {
				t.Fatal(err)
			}
			// The declared small sheet must still reject these expanded modules.
			composition := schCompositionSource{SchemaVersion: 1, Connectivity: input.Connectivity, Sheet: input.Sheet, SheetBorder: input.SheetBorder, Keepouts: input.Keepouts, Modules: []schCompositionModule{{ID: result.Zones[0].ID, Title: result.Zones[0].Title, Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}}}
			if _, err = planSchComposition(composition); err == nil {
				t.Fatal("expanded layout silently fit original paper")
			}
			after, _ := json.Marshal(input)
			if !bytes.Equal(before, after) {
				t.Fatal("canonical source or sheet mutated")
			}
			if partial, err := planLibLayout(input); err == nil || partial != nil {
				t.Fatal("baseline failure disappeared; reassess fixture")
			}
		})
	}
}

func TestLibLocalCLIUsesOriginalSourceHashAndFailureGuards(t *testing.T) {
	for _, scenario := range []string{"success", "budget", "canonical-pin", "conflicting-flags"} {
		t.Run(scenario, func(t *testing.T) {
			input := libLayoutFixture()
			input.Keepouts = []layoutBBox{}
			if scenario == "budget" {
				input.MaxCandidates = 1
			}
			if scenario == "canonical-pin" {
				input.Measurements[0].Pins[0].Net = "wrong"
			}
			raw, _ := json.Marshal(input)
			dir := t.TempDir()
			from, out, report := filepath.Join(dir, "source.json"), filepath.Join(dir, "layout.json"), filepath.Join(dir, "report.json")
			if err := os.WriteFile(from, raw, 0600); err != nil {
				t.Fatal(err)
			}
			old := []byte("previous layout")
			if err := os.WriteFile(out, old, 0600); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			cmd := newSchLayoutPlanCmd(&stdout)
			args := []string{"--lib", "--unbounded", "--from", from, "--out", out, "--report", report}
			if scenario == "conflicting-flags" {
				args = append(args, "--zones")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if (err == nil) != (scenario == "success") {
				t.Fatal(err)
			}
			if scenario != "success" {
				kept, _ := os.ReadFile(out)
				if !bytes.Equal(kept, old) || stdout.Len() != 0 {
					t.Fatal("failure emitted partial output")
				}
			}
			kept, _ := os.ReadFile(from)
			if !bytes.Equal(kept, raw) {
				t.Fatal("source mutated")
			}
			if scenario == "conflicting-flags" {
				return
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			var r schLayoutReport
			if err = json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			if r.SourceSHA256 != sha256Hex(raw) || r.LayoutMode != "unbounded" || !r.Zones || r.GlobalInfeasibilityProven {
				t.Fatal("wrong report evidence")
			}
			if scenario == "budget" && r.FailureClass != "candidate-budget-exhausted" {
				t.Fatal("lost wrapped resource failure")
			}
		})
	}
}
