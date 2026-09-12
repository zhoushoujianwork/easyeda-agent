package app

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

// Synthetic placement alternatives translate only a peripheral component.
// Both components have explicit NC, so no hidden topology answer is supplied.
func sheetVariantFixture(t *testing.T, id string) SchematicRenderZone {
	t.Helper()
	coreID, passiveID := id+"-core", id+"-passive"
	coreRef, passiveRef := id+"1", id+"2"
	part := SchematicPlacement{Designator: coreRef, X: 0, Y: 0, BBox: SchematicBox{-10, -10, 10, 10}, Pins: []SchematicPin{{Number: "1", X: -20, Y: 0}}}
	peripheral := part
	peripheral.Designator = passiveRef
	peripheral.Pins = append([]SchematicPin(nil), part.Pins...)
	layout := &SchematicLayoutResult{SchemaVersion: 1, ComponentIDs: map[string]string{coreRef: coreID, passiveRef: passiveID}, PinStates: map[string]map[string]string{coreID: {"1": "nc"}, passiveID: {"1": "nc"}}, AllowedRotations: map[string][]float64{coreID: {0}, passiveID: {0}}}
	zone := SchematicZone{ID: id, Title: id, CoreComponentID: coreID, ComponentIDs: []string{coreID, passiveID}}
	var variants []SchematicZoneVariant
	for i, position := range [][2]float64{{200, 0}, {0, -200}} {
		local := *layout
		local.Placements = []SchematicPlacement{part, plTranslate(peripheral, position[0], position[1])}
		variant, err := measureSchematicZoneVariant(zone, []string{"original-shape", "upright-shape"}[i], &local, nil)
		if err != nil {
			t.Fatal(err)
		}
		variants = append(variants, variant)
	}
	main := variants[0]
	return SchematicRenderZone{ID: id, Title: id, CoreComponentID: coreID, Layout: main.Layout, Frame: &main.Frame, ContentBounds: &main.ContentBounds, Variants: variants}
}

func sheetVariantLookaheadFixture(t *testing.T) SchematicRenderInput {
	a := sheetVariantFixture(t, "A")
	alt := a.Variants[1].Frame.Rect
	altWidth, altHeight := alt.MaxX-alt.MinX, alt.MaxY-alt.MinY
	b := relationZone("B", 200, math.Max(altHeight, 200))
	// The taller alternate fits beside B on one row, whereas the shorter but
	// wider default forces B onto a new page. No coordinate is changed by packing.
	in := zFlowFixture(altWidth+200+15+30, math.Max(altHeight, 200)+30, a, b)
	return in
}

func TestSheetVariantsLookAheadRatherThanChooseLocalArea(t *testing.T) {
	in := sheetVariantLookaheadFixture(t)
	before, _ := json.Marshal(in)
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pages) != 1 || plan.VariantSearch == nil || plan.VariantSearch.DefaultPageCount != 2 || plan.VariantSearch.BaselinePageCount != 2 {
		t.Fatalf("did not improve both complete baselines: %+v", plan.VariantSearch)
	}
	if got := plan.Pages[0].Zones[0].SelectedVariantID; got != "upright-shape" {
		t.Fatalf("did not revisit early default after later geometry arrived: %s", got)
	}
	for _, page := range plan.Pages {
		if err := validateSchematicSheet(page); err != nil {
			t.Fatal(err)
		}
		for _, z := range page.Zones {
			if len(z.Variants) != 0 {
				t.Fatal("sheet leaked unselected candidate package")
			}
			if z.ID == "A" {
				variant := in.Zones[0].Variants[1]
				if !reflect.DeepEqual(z.Layout, variant.Layout) || !reflect.DeepEqual(*z.Frame, variant.Frame) || !reflect.DeepEqual(*z.ContentBounds, variant.ContentBounds) {
					t.Fatal("sheet edited a candidate instead of selecting it")
				}
			}
		}
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("variant selection mutated input")
	}
	again, err := PlanSchematicSheets(in)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatal("variant beam not deterministic", err)
	}
}

func TestSheetVariantMayFitWhenDefaultCannotFitEmptyPage(t *testing.T) {
	z := sheetVariantFixture(t, "A")
	r := z.Variants[1].Frame.Rect
	in := zFlowFixture(r.MaxX-r.MinX+30, r.MaxY-r.MinY+30, z)
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal("rejected all variants based on the default rectangle", err)
	}
	if len(plan.Pages) != 1 || plan.Pages[0].Zones[0].SelectedVariantID != "upright-shape" || plan.VariantSearch.DefaultFeasible || plan.VariantSearch.BaselineFeasible {
		t.Fatal("unexpected oversized-default result")
	}
}

func TestSheetVariantSamePageGroupStaysAtomic(t *testing.T) {
	in := sheetVariantLookaheadFixture(t)
	in.Zones[1].Placement = &SchematicZonePlacement{SamePageAs: "A", PreferAdjacent: true}
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pages) != 1 || len(plan.Pages[0].Zones) != 2 || plan.Pages[0].Zones[1].Placement.SamePageAs != "A" {
		t.Fatal("candidate selection split the relation group")
	}
	if err := validateSchematicSheet(plan.Pages[0]); err != nil {
		t.Fatal(err)
	}
}

func TestSheetVariantBudgetExhaustionRetainsOriginalAndDefaultBaselines(t *testing.T) {
	z := sheetVariantFixture(t, "A")
	// Main is an optimized candidate; original baseline remains variants[0].
	main := z.Variants[1]
	z.Layout, z.Frame, z.ContentBounds, z.SelectedVariantID = main.Layout, &main.Frame, &main.ContentBounds, main.ID
	in := zFlowFixture(700, 500, z)
	in.Sheet.Flow = "z"
	plan, report, err := planSchematicZVariantSheetsWithSearch(in, in.Zones, &schematicZSearch{candidates: schematicZMaxCandidates})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || !report.BudgetLimited || !report.BaselineFeasible || !report.DefaultFeasible || plan[0].Zones[0].SelectedVariantID != "original-shape" {
		t.Fatalf("lost better original safeguard: %+v", report)
	}
}

func TestSheetVariantBudgetRetainsNewCompletePlanWithoutFeasibleBaseline(t *testing.T) {
	z := sheetVariantFixture(t, "A")
	duplicate := z.Variants[1]
	duplicate.ID = "third-valid-shape"
	z.Variants = append(z.Variants, duplicate)
	r := duplicate.Frame.Rect
	in := zFlowFixture(r.MaxX-r.MinX+30, r.MaxY-r.MinY+30, z)
	in.Sheet.Flow = "z"
	// The default is too wide and consumes no position tests. The second
	// candidate fits on the final permitted test; probing the third then stops.
	// That stop must preserve the complete second candidate even with no fallback.
	plan, report, err := planSchematicZVariantSheetsWithSearch(in, in.Zones, &schematicZSearch{candidates: schematicZMaxCandidates - 1})
	if err != nil || len(plan) != 1 || !report.BudgetLimited || report.BaselineFeasible || report.DefaultFeasible {
		t.Fatal("lost a complete plan discovered just before exhaustion", err, report)
	}
	if plan[0].Zones[0].SelectedVariantID != "upright-shape" {
		t.Fatal("returned the wrong surviving candidate")
	}
}

func TestSheetVariantMainMatchingIgnoresUntrustedSearchScores(t *testing.T) {
	z := sheetVariantFixture(t, "A")
	main := *z.Layout
	main.CandidatesUsed, main.Score = 999, [4]float64{1e6, 1e6, 1e6, 1e6}
	main.Search = &SchematicLayoutSearchDiagnostics{Strategy: "whole-zone-search", Backtracks: 12, RepairAttempts: 30}
	main.OptimizationReport = &SchematicOptimizationReport{AttemptsUsed: 24, AcceptedCandidates: 2, RemainingCandidates: 123, StopReason: "attempt-limit"}
	z.Layout = &main
	in := zFlowFixture(700, 500, z)
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal("treated diagnostic score differences as different geometry", err)
	}
	if plan.Pages[0].Zones[0].SelectedVariantID != "original-shape" {
		t.Fatal("trusted supplied score rather than real wire/frame geometry")
	}
	if !reflect.DeepEqual(plan.Pages[0].Zones[0].Layout, z.Variants[0].Layout) || z.Layout.CandidatesUsed != 999 || z.Layout.OptimizationReport.AttemptsUsed != 24 {
		t.Fatal("sheet failed to materialize the selected candidate or changed primary diagnostics")
	}
}

func TestSheetVariantsCompactRejectedAndAbsentVariantsStayLegacy(t *testing.T) {
	in := sheetVariantLookaheadFixture(t)
	in.Sheet.Flow = "compact"
	if plan, err := PlanSchematicSheets(in); plan != nil || err == nil {
		t.Fatal("compact silently selected or discarded zone variants")
	}
	plain := zFlowFixture(400, 400, relationZone("A", 100, 100))
	plan, err := PlanSchematicSheets(plain)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plan)
	if plan.VariantSearch != nil || bytes.Contains(raw, []byte("variantSearch")) || bytes.Contains(raw, []byte("selectedVariantId")) {
		t.Fatal("plain layout acquired variant output metadata")
	}
}
