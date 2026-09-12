package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func standaloneLayoutFixture() SchematicLayoutInput {
	return SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: "anchor", NetPolicies: map[string]string{"SUPPLY": "local_power", "RETURN": "local_ground"}, Components: []SchematicLayoutComponent{
		{ID: "anchor", Measurement: SchematicPlacement{Designator: "U1", X: 100, Y: 100, BBox: SchematicBox{80, 80, 120, 120}, Pins: []SchematicPin{{Number: "1", Net: "SUPPLY", X: 130, Y: 100}, {Number: "2", Net: "RETURN", X: 100, Y: 70}, {Number: "3", X: 70, Y: 110}}}, PinStates: map[string]string{"3": "nc"}},
		{ID: "peripheral", Measurement: SchematicPlacement{Designator: "R1", X: 300, Y: 100, BBox: SchematicBox{290, 90, 310, 110}, Pins: []SchematicPin{{Number: "1", Net: "SUPPLY", X: 280, Y: 100}, {Number: "2", Net: "RETURN", X: 320, Y: 100}}}},
	}}
}

func TestSchematicLayoutWithoutLibMetadata(t *testing.T) {
	in := standaloneLayoutFixture()
	before, _ := json.Marshal(in)
	out, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PlanSchematicLayout(in)
	if err != nil || !reflect.DeepEqual(out, again) {
		t.Fatal("not deterministic", err)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated input")
	}
	if len(out.Placements) != 2 || len(out.Wires) == 0 || out.CandidatesUsed <= 0 || out.Placements[0].X != 0 || out.Placements[0].Y != 0 {
		t.Fatalf("incomplete result: %+v", out)
	}
	if out.PinStates["anchor"]["3"] != "nc" || out.ComponentIDs["U1"] != "anchor" {
		t.Fatal("lost identity or NC")
	}
	out.PinStates["anchor"]["3"] = "unconnected"
	if in.Components[0].PinStates["3"] != "nc" {
		t.Fatal("result aliases caller state")
	}
}

func TestSchematicLayoutRejectsMissingOrConflictingEvidence(t *testing.T) {
	for _, edit := range []func(*SchematicLayoutInput){
		func(s *SchematicLayoutInput) { s.Components[0].PinStates = nil },
		func(s *SchematicLayoutInput) { s.Components[0].PinStates["1"] = "nc" },
		func(s *SchematicLayoutInput) { s.CoreComponentID = "missing" },
		func(s *SchematicLayoutInput) { s.Components[1].ID = "anchor" },
		func(s *SchematicLayoutInput) {
			s.Attachments = []SchematicLayoutPeripheral{{ComponentID: "peripheral", AttachTo: &SchematicLayoutAttach{ComponentID: "missing", PinNumber: "1"}}}
		},
		func(s *SchematicLayoutInput) { s.MaxCandidates = 1 },
	} {
		in := standaloneLayoutFixture()
		edit(&in)
		if out, err := PlanSchematicLayout(in); err == nil || out != nil {
			t.Fatal("invalid evidence accepted")
		}
	}
}

func TestLibAdapterMatchesStandaloneEngine(t *testing.T) {
	src := libLayoutFixture()
	local := SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: src.LayoutModules[0].CoreComponentID, NetPolicies: map[string]string{}}
	for _, n := range src.Connectivity.Nets {
		local.NetPolicies[n.Name] = src.LayoutModules[0].NetPolicies[n.ID]
	}
	for i, c := range src.Connectivity.Components {
		states := map[string]string{}
		for _, p := range c.Pins {
			if p.NoConnected {
				states[p.Number] = "nc"
			}
		}
		local.Components = append(local.Components, SchematicLayoutComponent{ID: c.ID, Measurement: src.Measurements[i], PinStates: states})
	}
	a, err := PlanSchematicLayout(local)
	if err != nil {
		t.Fatal(err)
	}
	b, err := planLibLayout(src)
	if err != nil {
		t.Fatal(err)
	}
	m := b.Modules[0]
	if !reflect.DeepEqual(a.Placements, m.Placements) || !reflect.DeepEqual(a.Wires, m.Wires) || !reflect.DeepEqual(a.Flags, m.Flags) {
		t.Fatal("Lib adapter diverged from common engine")
	}
}

func TestLayoutPlanCLIAndInputGuards(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "in.json")
	out := filepath.Join(dir, "out.json")
	raw, _ := json.Marshal(standaloneLayoutFixture())
	if err := os.WriteFile(from, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	cmd := newSchLayoutPlanCmd(&stdout)
	cmd.SetArgs([]string{"--from", from, "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result SchematicLayoutResult
	b, _ := os.ReadFile(out)
	if err := json.Unmarshal(b, &result); err != nil || len(result.Placements) != 2 {
		t.Fatal("bad command result", err)
	}
	cmd = newSchLayoutPlanCmd(&stdout)
	cmd.SetArgs([]string{"--from", from, "--out", from})
	if err := cmd.Execute(); err == nil {
		t.Fatal("overwrote input")
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	m := fields["components"].([]any)[0].(map[string]any)["measurement"].(map[string]any)
	delete(m, "mirror")
	bad, _ := json.Marshal(fields)
	if _, err := decodeSchematicLayoutInput(bad); err == nil {
		t.Fatal("invented missing mirror")
	}
	if err := os.WriteFile(from, bad, 0600); err != nil {
		t.Fatal(err)
	}
	cmd = newSchLayoutPlanCmd(&stdout)
	cmd.SetArgs([]string{"--from", from, "--out", out})
	if err := cmd.Execute(); err == nil {
		t.Fatal("invalid input accepted")
	}
	unchanged, _ := os.ReadFile(out)
	if !bytes.Equal(b, unchanged) {
		t.Fatal("failed plan overwrote good output")
	}
}
