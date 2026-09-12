package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func relationZonesFixture() SchematicZonesInput {
	in := zonesFixture()
	spacing := 20.0
	in.Spacing = &spacing
	in.Zones[1].Placement = &SchematicZonePlacement{SamePageAs: in.Zones[0].ID, PreferAdjacent: true}
	return in
}

func TestZoneRelationsForwardWithoutChangingLocalGeometry(t *testing.T) {
	in := relationZonesFixture()
	before, _ := json.Marshal(in)
	withRelation, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	without := relationZonesFixture()
	without.Zones[1].Placement = nil
	withoutRelation, err := PlanSchematicZones(without)
	if err != nil {
		t.Fatal(err)
	}
	for i, z := range withRelation.Zones {
		base := withoutRelation.Zones[i]
		if !reflect.DeepEqual(z.Layout, base.Layout) || z.ContentBounds != base.ContentBounds || !reflect.DeepEqual(z.Frame, base.Frame) {
			t.Fatal("page relationship changed a zone's local electrical/geometry solution")
		}
		if !reflect.DeepEqual(z.Placement, in.Zones[i].Placement) {
			t.Fatal("lost page relationship during zone planning")
		}
	}
	if withRelation.CandidatesUsed != withoutRelation.CandidatesUsed {
		t.Fatal("page relationship changed local search budget")
	}
	raw, _ := json.Marshal(withRelation)
	var render SchematicRenderInput
	if err = json.Unmarshal(raw, &render); err != nil || !reflect.DeepEqual(render.Zones[1].Placement, in.Zones[1].Placement) {
		t.Fatal("standard zone result does not forward relationships to renderer", err)
	}
	withRelation.Zones[1].Placement.SamePageAs = "changed-output"
	withRelation.Zones[1].Placement.PreferAdjacent = false
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("output relation aliases caller input")
	}
}

func TestNonICCoreKeepsBothPeripheralsInExplicitRelatedZone(t *testing.T) {
	in := relationZonesFixture()
	// This is synthetic connector geometry, not a private circuit or a claim
	// that every functional core is an IC. Two complete peripheral records must
	// remain owned by the explicit J9 zone, despite its shared boundary net.
	in.Components[2].Measurement.Designator = "J9"
	in.Components[3].Measurement.Designator = "C20"
	extra := in.Components[3]
	extra.ID, extra.Measurement.Designator = "extra-passive", "C21"
	in.Components = append(in.Components, extra)
	in.Zones[1].ComponentIDs = append(in.Zones[1].ComponentIDs, extra.ID)
	in.NetPolicies["SUPPLY"] = "module_port"
	out, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Zones) != 2 || len(out.Zones[1].Layout.Placements) != 3 {
		t.Fatal("split off or lost the non-IC core's peripherals")
	}
	z := out.Zones[1]
	if z.Layout.Placements[0].Designator != "J9" || z.Layout.Placements[0].X != 0 || z.Layout.Placements[0].Y != 0 {
		t.Fatal("non-IC core is not the local anchor")
	}
	for _, ref := range []string{"J9", "C20", "C21"} {
		if z.Layout.ComponentIDs[ref] == "" {
			t.Fatal("lost explicitly owned part", ref)
		}
	}
	p := powerLayoutPlan{Placements: z.Layout.Placements, Wires: z.Layout.Wires, Flags: z.Layout.Flags}
	if err = validateSchCompositionNets(&p); err != nil {
		t.Fatal("non-IC zone is not a complete connected local result", err)
	}
	in.NetPolicies["SUPPLY"] = "direct"
	if bad, err := PlanSchematicZones(in); err == nil || bad != nil || !strings.Contains(err.Error(), "cross-zone net") {
		t.Fatal("same-page relationship incorrectly permits a direct cross-zone net", err)
	}
}

func replaceRelationJSON(t *testing.T, raw []byte, index int, placement string) []byte {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	var zones []map[string]json.RawMessage
	if err := json.Unmarshal(top["zones"], &zones); err != nil {
		t.Fatal(err)
	}
	zones[index]["placement"] = json.RawMessage(placement)
	top["zones"], _ = json.Marshal(zones)
	result, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestZoneRelationInvalidInputsPreserveExistingCLIOutput(t *testing.T) {
	raw, _ := json.Marshal(relationZonesFixture())
	dir := t.TempDir()
	from, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.json")
	good := []byte("existing-good-plan\n")
	for _, tc := range []struct{ name, placement string }{
		{"self", `{"samePageAs":"second"}`},
		{"unknown target", `{"samePageAs":"missing"}`},
		{"missing target", `{"preferAdjacent":true}`},
		{"null placement", `null`},
		{"null target", `{"samePageAs":null}`},
		{"null preference", `{"samePageAs":"first","preferAdjacent":null}`},
		{"wrong placement type", `"first"`},
		{"wrong target type", `{"samePageAs":12}`},
		{"wrong preference type", `{"samePageAs":"first","preferAdjacent":"yes"}`},
		{"unknown field", `{"samePageAs":"first","preferAdajcent":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := replaceRelationJSON(t, raw, 1, tc.placement)
			if err := os.WriteFile(from, bad, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(out, good, 0600); err != nil {
				t.Fatal(err)
			}
			var messages bytes.Buffer
			cmd := newSchLayoutPlanCmd(&messages)
			cmd.SetOut(&messages)
			cmd.SetErr(&messages)
			cmd.SetArgs([]string{"--zones", "--from", from, "--out", out})
			if err := cmd.Execute(); err == nil {
				t.Fatal("accepted invalid relation", tc.placement)
			}
			actual, err := os.ReadFile(out)
			if err != nil || !bytes.Equal(good, actual) {
				t.Fatal("invalid relation overwrote previous output", err)
			}
		})
	}
}

func TestZoneDetailValidatesFullRelationsBeforeSelecting(t *testing.T) {
	planned, err := PlanSchematicZones(relationZonesFixture())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(planned)
	var input SchematicRenderInput
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	input.Title = "Related functional zones"
	input.Sheet = &SchematicRenderSheet{Bounds: SchematicBox{0, 0, 1600, 1000}, Border: SchematicBox{0, 0, 1600, 1000}, Keepouts: []SchematicBox{}, Padding: 20, Gap: 20}
	input.Zones[0].SheetPosition = &SchematicSheetPosition{X: 30, Y: 950}
	input.Zones[1].SheetPosition = &SchematicSheetPosition{X: 600, Y: 950}
	if _, err = RenderSchematicLayoutSVG(input); err != nil {
		t.Fatal("fixture must be a valid full-sheet related input", err)
	}
	raw, _ = json.Marshal(input)
	dir := t.TempDir()
	from, out := filepath.Join(dir, "input.json"), filepath.Join(dir, "detail.svg")
	if err = os.WriteFile(from, raw, 0600); err != nil {
		t.Fatal(err)
	}
	run := func() error {
		var messages bytes.Buffer
		cmd := newSchLayoutRenderCmd(&messages)
		cmd.SetOut(&messages)
		cmd.SetErr(&messages)
		cmd.SetArgs([]string{"--from", from, "--zone", "second", "--out", out})
		return cmd.Execute()
	}
	if err = run(); err != nil {
		t.Fatal("valid relation should not block isolated detail rendering", err)
	}
	detail := input
	selected := input.Zones[1]
	selected.Placement, selected.SheetPosition = nil, nil
	detail.Sheet, detail.Zones = nil, []SchematicRenderZone{selected}
	want, err := RenderSchematicLayoutSVG(detail)
	if err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(want, good) || bytes.Contains(good, []byte("排版边界")) {
		t.Fatal("--zone did not produce a standalone detail without sheet constraints", err)
	}
	// The bad relation is on the UNSELECTED zone. Selection cannot hide it.
	for _, relation := range []string{`{"samePageAs":"missing"}`, `{"samePageAs":"first"}`, `null`} {
		if err = os.WriteFile(from, replaceRelationJSON(t, raw, 0, relation), 0600); err != nil {
			t.Fatal(err)
		}
		if err = run(); err == nil {
			t.Fatal("--zone bypassed an invalid reference in the original input", relation)
		}
		actual, readErr := os.ReadFile(out)
		if readErr != nil || !bytes.Equal(good, actual) {
			t.Fatal("failed detail render overwrote the last valid SVG", readErr)
		}
	}
}
