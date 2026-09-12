package app

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// Default-flow fixtures intentionally do not use relationSheet: that helper
// explicitly opts into the legacy compact-search tests.
func zFlowFixture(width, height float64, zones ...SchematicRenderZone) SchematicRenderInput {
	return SchematicRenderInput{SchemaVersion: 1, Title: "Synthetic Z flow", Zones: zones, Sheet: &SchematicRenderSheet{
		Bounds: SchematicBox{0, 0, width, height}, Border: SchematicBox{0, 0, width, height},
		Keepouts: []SchematicBox{}, Padding: 10, Gap: 10,
	}}
}

func zFlowIDs(plan *SchematicSheetsPreview) [][]string {
	ids := make([][]string, len(plan.Pages))
	for i, page := range plan.Pages {
		for _, z := range page.Zones {
			ids[i] = append(ids[i], z.ID)
		}
	}
	return ids
}

func requireZFlowPosition(t *testing.T, zone SchematicRenderZone, x, y float64) {
	t.Helper()
	if zone.SheetPosition == nil || *zone.SheetPosition != (SchematicSheetPosition{X: x, Y: y}) {
		t.Fatalf("zone %s: want (%g,%g), got %+v", zone.ID, x, y, zone.SheetPosition)
	}
}

func TestSheetZDefaultPreservesInputOrderAndUsesTallestRow(t *testing.T) {
	in := zFlowFixture(400, 600,
		relationZone("A", 100, 200),
		relationZone("B", 100, 100),
		relationZone("C", 180, 150))
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := zFlowIDs(plan), [][]string{{"A", "B", "C"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Z flow reordered by size: got %v, want %v", got, want)
	}
	zones := plan.Pages[0].Zones
	requireZFlowPosition(t, zones[0], 15, 585)
	requireZFlowPosition(t, zones[1], 130, 585)
	requireZFlowPosition(t, zones[2], 15, 370) // 585 - tallest 200 - grid-safe gap 15.
	if err := validateSchematicSheet(plan.Pages[0]); err != nil {
		t.Fatal(err)
	}
}

func TestSheetZDoesNotFillVoidBelowShorterRowMember(t *testing.T) {
	in := zFlowFixture(350, 600,
		relationZone("tall", 180, 200), relationZone("short", 100, 100), relationZone("next", 100, 100))
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pages) != 1 {
		t.Fatal("unexpected page break")
	}
	// A compact packer can fit 'next' under 'short', but reading-order shelves
	// must return to the left below the tallest frame instead of filling that void.
	requireZFlowPosition(t, plan.Pages[0].Zones[2], 15, 370)
}

func TestSheetZDoesNotBackfillPreviousPage(t *testing.T) {
	in := zFlowFixture(420, 300,
		relationZone("A", 250, 220), relationZone("B", 250, 220), relationZone("C", 100, 100))
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := zFlowIDs(plan), [][]string{{"A"}, {"B", "C"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("backfilled a completed page or changed order: got %v, want %v", got, want)
	}
	requireZFlowPosition(t, plan.Pages[1].Zones[1], 280, 285)
}

func TestSheetZKeepoutSkipPreservesGridStrokeAndTopAlignment(t *testing.T) {
	in := zFlowFixture(500, 400, relationZone("A", 101, 103), relationZone("B", 100, 100))
	in.Sheet.Keepouts = []SchematicBox{{0, 250, 150, 400}}
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pages) != 1 {
		t.Fatal("keepout caused an unnecessary page break")
	}
	page := plan.Pages[0]
	requireZFlowPosition(t, page.Zones[0], 165, 385) // ceil(keepout right + gap + half stroke).
	requireZFlowPosition(t, page.Zones[1], 280, 385) // ceil(previous right + gap + two half strokes).
	if err := validateSchematicSheet(page); err != nil {
		t.Fatal(err)
	}
	for _, z := range page.Zones {
		if !plGrid(z.SheetPosition.X) || !plGrid(z.SheetPosition.Y) {
			t.Fatal("non-grid Z origin")
		}
	}
	a, _ := sheetPreviewRect(page.Zones[0])
	b, _ := sheetPreviewRect(page.Zones[1])
	if a.MinX-in.Sheet.Keepouts[0].MaxX-.5 < in.Sheet.Gap || b.MinX-a.MaxX-1 < in.Sheet.Gap {
		t.Fatal("ignored visible stroke clearances")
	}
}

func TestSheetZNoncontiguousCyclicRelationsGatherInInputOrder(t *testing.T) {
	a, b, c, d := relationZone("A", 100, 100), relationZone("B", 100, 100), relationZone("C", 100, 100), relationZone("D", 100, 100)
	a.Placement = &SchematicZonePlacement{SamePageAs: "D", PreferAdjacent: true}
	c.Placement = &SchematicZonePlacement{SamePageAs: "A", PreferAdjacent: true}
	d.Placement = &SchematicZonePlacement{SamePageAs: "C", PreferAdjacent: true}
	in := zFlowFixture(400, 300, a, b, c, d)
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := zFlowIDs(plan), [][]string{{"A", "C", "D", "B"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("same-page collection lost first-member/inner input order: got %v, want %v", got, want)
	}
	for i, x := range []float64{15, 130, 245} {
		requireZFlowPosition(t, plan.Pages[0].Zones[i], x, 285)
	}
	requireZFlowPosition(t, plan.Pages[0].Zones[3], 15, 170)
}

func TestSheetZSamePageGroupRollsBackPartialTrialAndMovesAtomically(t *testing.T) {
	a, b := relationZone("A", 100, 100), relationZone("B", 100, 100)
	b.Placement = &SchematicZonePlacement{SamePageAs: "A"}
	in := zFlowFixture(420, 300, relationZone("earlier", 250, 220), a, b)
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := zFlowIDs(plan), [][]string{{"earlier"}, {"A", "B"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("atomic same-page retry leaked a partial member: got %v, want %v", got, want)
	}
	requireZFlowPosition(t, plan.Pages[1].Zones[0], 15, 285)
	requireZFlowPosition(t, plan.Pages[1].Zones[1], 130, 285)

	tooLarge := zFlowFixture(420, 300, relationZone("H", 250, 220), relationZone("S", 250, 220))
	tooLarge.Zones[1].Placement = &SchematicZonePlacement{SamePageAs: "H"}
	if out, err := PlanSchematicSheets(tooLarge); out != nil || err == nil {
		t.Fatal("unplaceable same-page group returned partial pages or was silently split")
	}
}

func TestSheetZPreservesLocalEvidenceAndIsDeterministic(t *testing.T) {
	a, b := relationZone("A", 180, 200), relationZone("B", 100, 100)
	b.Placement = &SchematicZonePlacement{SamePageAs: "A", PreferAdjacent: true}
	in := zFlowFixture(400, 400, a, b, relationZone("C", 250, 200))
	before, _ := json.Marshal(in)
	first, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanSchematicSheets(in)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("Z output is not repeatable", err)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("Z planner mutated caller evidence")
	}
	originals := map[string]SchematicRenderZone{}
	for _, z := range in.Zones {
		originals[z.ID] = z
	}
	for _, page := range first.Pages {
		if err := validateSchematicSheet(page); err != nil {
			t.Fatal(err)
		}
		for _, z := range page.Zones {
			original := originals[z.ID]
			if !reflect.DeepEqual(z.Layout, original.Layout) || !reflect.DeepEqual(z.Frame, original.Frame) || !reflect.DeepEqual(z.Placement, original.Placement) {
				t.Fatal("sheet translation changed local layout, frame, or ownership")
			}
		}
	}
}

func TestSheetZRecomputesLegalButNonZPositionsAndCompactRetainsThem(t *testing.T) {
	z := relationZone("A", 100, 100)
	z.SheetPosition = &SchematicSheetPosition{X: 200, Y: 300}
	in := zFlowFixture(600, 400, z)
	in.Sheet.Flow = "z"
	if _, err := RenderSchematicLayoutSVG(in); err == nil {
		t.Fatal("direct rendering accepted non-Z coordinates labeled as Z flow")
	}
	in.Sheet.Flow = ""
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlacementMode == "reused" {
		t.Fatal("default Z reused arbitrary legal free-packing coordinates")
	}
	requireZFlowPosition(t, plan.Pages[0].Zones[0], 15, 385)
	again, err := PlanSchematicSheets(plan.Pages[0])
	if err != nil || again.PlacementMode != "reused" || !reflect.DeepEqual(again.Pages[0].Zones, plan.Pages[0].Zones) {
		t.Fatal("canonical Z coordinates were not reused consistently", err)
	}
	in.Sheet.Flow = "compact"
	compact, err := PlanSchematicSheets(in)
	if err != nil || compact.PlacementMode != "reused" || !reflect.DeepEqual(compact.Pages[0].Zones[0].SheetPosition, z.SheetPosition) {
		t.Fatal("explicit compact mode lost existing legal-placement behavior", err)
	}
	if z.SheetPosition.X != 200 || z.SheetPosition.Y != 300 {
		t.Fatal("planner overwrote caller coordinates")
	}
}

func TestSheetZRejectsUnknownFlow(t *testing.T) {
	in := zFlowFixture(400, 400, relationZone("A", 100, 100))
	in.Sheet.Flow = "spiral"
	if out, err := PlanSchematicSheets(in); out != nil || err == nil {
		t.Fatal("accepted unknown sheet flow")
	}
}
