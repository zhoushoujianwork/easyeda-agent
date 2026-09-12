package app

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func uniformSpacing(value float64) *float64 { return &value }

func TestUnifiedZoneSpacingPreservesCircuitAndPropagates(t *testing.T) {
	in := zonesFixture()
	legacy, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Spacing = uniformSpacing(30)
	before, _ := json.Marshal(in)
	planned, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("planning changed input evidence")
	}
	if planned.Spacing == nil || *planned.Spacing != 30 {
		t.Fatal("spacing was not forwarded to the render/sheet input")
	}
	for i, z := range planned.Zones {
		// Enough budget in this fixture makes the old and isolated runs equal.
		if !reflect.DeepEqual(z.Layout, legacy.Zones[i].Layout) {
			t.Fatal("presentation spacing changed electrical geometry")
		}
		p := powerLayoutPlan{Placements: z.Layout.Placements, Wires: z.Layout.Wires, Flags: z.Layout.Flags}
		if err := validateSchematicFrameSpacing(z.Frame, powerLayoutContentObstacles(&p), in.Spacing); err != nil {
			t.Fatal(err)
		}
	}
	*planned.Spacing = 50
	if *in.Spacing != 30 {
		t.Fatal("output spacing aliases caller configuration")
	}
}

func TestUnifiedZonesIsolateGeometryAndSearchBudget(t *testing.T) {
	in := zonesFixture()
	in.Spacing = uniformSpacing(20)
	first, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Components[0].Measurement.Value = "ChangedModel"
	edited, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(first.Zones[0], edited.Zones[0]) || !reflect.DeepEqual(first.Zones[1], edited.Zones[1]) {
		t.Fatal("editing one zone changed a different zone or was ignored")
	}
	// Removing the first zone changes the total work and owner iteration but
	// must not change any coordinates, wiring or accounting of the second.
	in.Components = in.Components[2:]
	in.Zones = in.Zones[1:]
	second, err := PlanSchematicZones(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Zones[1], second.Zones[0]) {
		t.Fatal("unmodified zone depends on another zone's budget or geometry")
	}
	if first.CandidatesUsed != first.Zones[0].Layout.CandidatesUsed+first.Zones[1].Layout.CandidatesUsed || second.CandidatesUsed != second.Zones[0].Layout.CandidatesUsed {
		t.Fatal("per-zone candidate cost was not summed")
	}
	// This cap fits one zone but not both when shared. Unified mode must
	// still solve both independently with the same exact per-zone allowance.
	in = zonesFixture()
	in.Spacing = uniformSpacing(20)
	in.MaxCandidates = first.Zones[0].Layout.CandidatesUsed
	if _, err := PlanSchematicZones(in); err != nil {
		t.Fatal("unified zones unexpectedly shared a cap", err)
	}
}

func TestUnifiedSpacingRejectsInvalidAndMissingEvidence(t *testing.T) {
	for _, value := range []float64{0, 5, 11, -10, math.Inf(1), math.NaN()} {
		in := zonesFixture()
		in.Spacing = uniformSpacing(value)
		if _, err := PlanSchematicZones(in); err == nil {
			t.Fatalf("accepted spacing %g", value)
		}
	}
	in := zonesFixture()
	in.Spacing = uniformSpacing(30)
	raw, _ := json.Marshal(in)
	if _, err := decodeSchematicZonesInput(raw); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeSchematicZonesInput([]byte(strings.Replace(string(raw), `"spacing":30`, `"spacing":null`, 1))); err == nil {
		t.Fatal("null spacing treated as omitted")
	}
}

func uniformSheetFixture(t *testing.T) SchematicRenderInput {
	in := renderFixture(t)
	in.Spacing = uniformSpacing(20)
	in.Sheet = &SchematicRenderSheet{Bounds: SchematicBox{0, 0, 1500, 1000}, Border: SchematicBox{0, 0, 1500, 1000}, Keepouts: []SchematicBox{}}
	return in
}

func TestSheetUnifiedSpacingDerivesOnceAndPreservesEvidence(t *testing.T) {
	in := uniformSheetFixture(t)
	raw, _ := json.Marshal(in.Zones[0])
	var second SchematicRenderZone
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatal(err)
	}
	second.ID = "second"
	for i := range second.Layout.Placements {
		second.Layout.Placements[i].Designator += "2"
	}
	in.Zones = append(in.Zones, second)
	before, _ := json.Marshal(in)
	out, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pages) != 1 || out.PlacementMode != "repacked" || *out.Spacing != 20 {
		t.Fatal("unexpected uniform page result")
	}
	page := out.Pages[0]
	if page.Sheet.Padding != 20 || page.Sheet.Gap != 20 || page.Spacing == nil || *page.Spacing != 20 {
		t.Fatal("page padding/gap lost the shared source")
	}
	if in.Sheet.Padding != 0 || in.Sheet.Gap != 0 {
		t.Fatal("filled defaults in caller's sheet")
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated source")
	}
	a, _ := sheetPreviewRect(page.Zones[0], page.Spacing)
	b, _ := sheetPreviewRect(page.Zones[1], page.Spacing)
	// With a 5-unit grid, 25 center-to-centerline gap is the first legal
	// value for P=20, because the two strokes consume one unit in total.
	if b.MinX-a.MaxX != 25 {
		t.Fatalf("gap was doubled or lost its stroke reserve: %g", b.MinX-a.MaxX)
	}
	if a.MinX-page.Sheet.Border.MinX-.5 < 20 || a.MinX-page.Sheet.Border.MinX-.5 >= 25 {
		t.Fatal("page margin is not the smallest legal grid value")
	}
	for i, z := range page.Zones {
		if !reflect.DeepEqual(z.Layout, in.Zones[i].Layout) {
			t.Fatal("sheet layer changed internal geometry")
		}
		p := powerLayoutPlan{Placements: z.Layout.Placements, Wires: z.Layout.Wires, Flags: z.Layout.Flags}
		if err := validateSchematicFrameSpacing(*z.Frame, powerLayoutContentObstacles(&p), page.Spacing); err != nil {
			t.Fatal(err)
		}
	}
	// One grid step closer leaves 19 raw clear space: geometrically disjoint
	// centerlines, but the requested visible clearance would be violated.
	bad := page
	bad.Zones = append([]SchematicRenderZone(nil), page.Zones...)
	position := *bad.Zones[1].SheetPosition
	position.X -= 5
	bad.Zones[1].SheetPosition = &position
	if validateSchematicSheet(bad) == nil {
		t.Fatal("accepted only centerline gap without stroke clearance")
	}
}

func TestSheetUniformSpacingRejectsConflictsAndDoesNotResizeFrames(t *testing.T) {
	for _, change := range []func(*SchematicRenderInput){
		func(in *SchematicRenderInput) { in.Sheet.Padding = 10 },
		func(in *SchematicRenderInput) { in.Sheet.Gap = 30 },
		func(in *SchematicRenderInput) { in.Sheet.Gap = math.NaN() },
		func(in *SchematicRenderInput) { in.Spacing = uniformSpacing(11) },
		func(in *SchematicRenderInput) {
			f, err := sheetPreviewFrame(in.Zones[0])
			if err != nil {
				t.Fatal(err)
			}
			in.Zones[0].Frame = &f // Existing 10-raw inner padding cannot become 20.
		},
	} {
		in := uniformSheetFixture(t)
		change(&in)
		if _, err := PlanSchematicSheets(in); err == nil {
			t.Fatal("accepted inconsistent uniform configuration or silently grew a frame")
		}
	}
}

func TestSheetReusesValidPositionsAndRepackagesGrownFrames(t *testing.T) {
	in := uniformSheetFixture(t)
	in.Sheet.Flow = "compact" // Arbitrary legal coordinates are only reusable in free-packing mode.
	first, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	page := first.Pages[0]
	// A deliberately non-canonical but legal location should remain unchanged.
	position := *page.Zones[0].SheetPosition
	position.X += 200
	position.Y -= 100
	page.Zones[0].SheetPosition = &position
	second, err := PlanSchematicSheets(page)
	if err != nil {
		t.Fatal(err)
	}
	if second.PlacementMode != "reused" || !reflect.DeepEqual(page.Zones, second.Pages[0].Zones) {
		t.Fatal("valid existing zone position was needlessly repacked")
	}
	grown := page
	grown.Zones = append([]SchematicRenderZone(nil), page.Zones...)
	frame := *grown.Zones[0].Frame
	frame.Rect.MaxX = frame.Rect.MinX + 1260
	grown.Zones[0].Frame = &frame
	third, err := PlanSchematicSheets(grown)
	if err != nil {
		t.Fatal(err)
	}
	if third.PlacementMode != "repacked" || reflect.DeepEqual(grown.Zones[0].SheetPosition, third.Pages[0].Zones[0].SheetPosition) {
		t.Fatal("outgrown page position was not repacked")
	}
	if !reflect.DeepEqual(grown.Zones[0].Layout, third.Pages[0].Zones[0].Layout) || !reflect.DeepEqual(grown.Zones[0].Frame, third.Pages[0].Zones[0].Frame) {
		t.Fatal("packing changed a local layout or frame")
	}
}
