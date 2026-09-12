package app

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Independent synthetic occupied rectangles; no exercise topology or devices.
func relationZone(id string, width, height float64) SchematicRenderZone {
	return SchematicRenderZone{
		ID: id, Title: id,
		Frame:  &schFrameSpec{ID: id, Title: id, Rect: SchematicBox{0, 0, width, height}, TitleX: 10, TitleY: height - 10, FontSize: 10, Color: "#AA00AA", LineType: 1},
		Layout: &SchematicLayoutResult{SchemaVersion: 1, ComponentIDs: map[string]string{id: id}, PinStates: map[string]map[string]string{id: {"1": "nc"}}, Placements: []SchematicPlacement{{Designator: id, X: 40, Y: 40, BBox: SchematicBox{30, 30, 50, 50}, Pins: []SchematicPin{{Number: "1", X: 20, Y: 40}}}}},
	}
}

func relationSheet(width, height float64, zones ...SchematicRenderZone) SchematicRenderInput {
	// These fixtures exercise the opt-in free-packing search, not Z reading order.
	return SchematicRenderInput{SchemaVersion: 1, Title: "Relations", Zones: zones, Sheet: &SchematicRenderSheet{Bounds: SchematicBox{0, 0, width, height}, Border: SchematicBox{0, 0, width, height}, Keepouts: []SchematicBox{}, Padding: 10, Gap: 10, Flow: "compact"}}
}

func TestSheetSamePageRelationsAreTransitiveCyclicAndAtomic(t *testing.T) {
	a, b, c := relationZone("A", 100, 100), relationZone("B", 100, 100), relationZone("C", 100, 100)
	a.Placement = &SchematicZonePlacement{SamePageAs: "B", PreferAdjacent: true}
	b.Placement = &SchematicZonePlacement{SamePageAs: "C", PreferAdjacent: true}
	c.Placement = &SchematicZonePlacement{SamePageAs: "A", PreferAdjacent: true}
	in := relationSheet(360, 260, relationZone("D", 220, 190), a, b, c)
	before, _ := json.Marshal(in)
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pages) != 2 {
		t.Fatalf("expected one atomic related page plus independent page, got %d", len(plan.Pages))
	}
	pageOf := map[string]int{}
	for i, page := range plan.Pages {
		if err := validateSchematicSheet(page); err != nil {
			t.Fatal(err)
		}
		for _, z := range page.Zones {
			pageOf[z.ID] = i
			for _, original := range in.Zones {
				if original.ID == z.ID && (!reflect.DeepEqual(original.Frame, z.Frame) || !reflect.DeepEqual(original.Layout, z.Layout) || !reflect.DeepEqual(original.Placement, z.Placement)) {
					t.Fatal("page packing modified local geometry or dropped relations")
				}
			}
		}
	}
	if pageOf["A"] != pageOf["B"] || pageOf["B"] != pageOf["C"] || pageOf["D"] == pageOf["A"] {
		t.Fatal("same-page connected component was split")
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated relation input")
	}
	again, err := PlanSchematicSheets(in)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatal("relation packing is not deterministic", err)
	}
}

func TestSheetRelationGroupBacktracksHostAroundKeepout(t *testing.T) {
	host, child := relationZone("H", 100, 100), relationZone("C", 180, 100)
	child.Placement = &SchematicZonePlacement{SamePageAs: "H", PreferAdjacent: true}
	in := relationSheet(320, 270, host, child)
	in.Sheet.Keepouts = []SchematicBox{{0, 0, 180, 125}}
	u := sheetPreviewUsable(*in.Sheet)
	// Host-first/top-left is a genuine dead end: the child's only suitable
	// upper-left region is blocked, while the lower-left is a keepout.
	firstHost := host
	firstHost.SheetPosition = &SchematicSheetPosition{X: u.MinX, Y: u.MaxY}
	search := &sheetRelationSearch{}
	if got := sheetRelationCandidates(child, []SchematicRenderZone{firstHost}, *in.Sheet, search); len(got) != 0 {
		t.Fatal("fixture is not a host-first dead end")
	}
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal("did not backtrack the host", err)
	}
	if len(plan.Pages) != 1 || len(plan.Pages[0].Zones) != 2 {
		t.Fatal("split or discarded a group instead of moving its host")
	}
	for _, z := range plan.Pages[0].Zones {
		if z.ID == "H" && *z.SheetPosition == *firstHost.SheetPosition {
			t.Fatal("host was not moved out of the dead end")
		}
	}
}

func TestSheetRelationPreferenceUsesRectangleDistance(t *testing.T) {
	host := relationZone("H", 100, 100)
	host.SheetPosition = &SchematicSheetPosition{X: 600, Y: 140}
	child := relationZone("C", 100, 100)
	in := relationSheet(1000, 400, host, child)
	plain := sheetRelationCandidates(child, []SchematicRenderZone{host}, *in.Sheet, &sheetRelationSearch{})
	child.Placement = &SchematicZonePlacement{SamePageAs: "H", PreferAdjacent: true}
	preferred := sheetRelationCandidates(child, []SchematicRenderZone{host}, *in.Sheet, &sheetRelationSearch{})
	if len(plain) == 0 || len(preferred) == 0 || plain[0].position == preferred[0].position {
		t.Fatal("soft adjacency did not change the candidate ranking")
	}
	if preferred[0].score[0] != 15 {
		t.Fatalf("wanted nearest legal 5-grid rectangle gap, got %g", preferred[0].score[0])
	}
	if plain[0].position.X != 15 || plain[0].position.Y != 385 {
		t.Fatal("unpreferred candidates lost deterministic top-left order")
	}
}

func TestSheetRelationEqualEdgeDistancePrefersCompactEnvelope(t *testing.T) {
	host, satellite := relationZone("H", 500, 300), relationZone("S", 200, 150)
	satellite.Placement = &SchematicZonePlacement{SamePageAs: "H", PreferAdjacent: true}
	in := relationSheet(1000, 1000, host, satellite)
	plan, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pages) != 1 {
		t.Fatal("unexpected split")
	}
	score := sheetRelationPackingScore(plan.Pages[0].Zones)
	// Both right and below can have edge distance 15. Center distance alone
	// favors below (240 vs 365), but that wastes a taller 500x465 envelope.
	if score[0] != 15 || score[1] != 715*300 {
		t.Fatalf("equal-gap placement ignored the compact envelope: %v", score)
	}
}

func TestSheetRelationCandidatesRetainWideAndTallShapes(t *testing.T) {
	host, satellite := relationZone("H", 500, 300), relationZone("S", 200, 150)
	satellite.Placement = &SchematicZonePlacement{SamePageAs: "H", PreferAdjacent: true}
	in := relationSheet(1000, 1000, host, satellite)
	page := in
	page.Zones = nil
	candidates, _ := placeSheetRelationGroupCandidates(sheetRelationGroup{zones: in.Zones}, page)
	if len(candidates) < 2 || len(candidates) > sheetRelationAlternatives {
		t.Fatal("missing or unbounded alternatives")
	}
	wide, tall := false, false
	for _, candidate := range candidates {
		class := sheetRelationAlignmentClass(candidate)
		wide = wide || strings.Contains(class, ":left-") || strings.Contains(class, ":right-")
		tall = tall || strings.Contains(class, ":above-") || strings.Contains(class, ":below-")
		trial := in
		trial.Zones = candidate
		if err := validateSchematicSheet(trial); err != nil {
			t.Fatal("alternative is not a complete legal group", err)
		}
	}
	if !wide || !tall {
		t.Fatal("local objective crowded all alternate envelope shapes out of the bounded beam")
	}
}

func TestSheetRelationCannotSplitOnFailureOrBypassRender(t *testing.T) {
	a, b := relationZone("A", 300, 200), relationZone("B", 300, 200)
	b.Placement = &SchematicZonePlacement{SamePageAs: "A"}
	in := relationSheet(360, 260, a, b)
	if plan, err := PlanSchematicSheets(in); plan != nil || err == nil || !strings.Contains(err.Error(), "same-page group") || !strings.Contains(err.Error(), "not a capacity proof") {
		t.Fatal("infeasible group was silently split or returned partial output", err)
	}
	// Direct rendering and validation must not allow a relation target to be
	// removed just because only the child's rectangle remains on this page.
	in.Zones = in.Zones[1:]
	in.Zones[0].SheetPosition = &SchematicSheetPosition{X: 15, Y: 245}
	if validateSchematicSheet(in) == nil {
		t.Fatal("page validator ignored an off-page relation target")
	}
	if _, err := RenderSchematicLayoutSVG(in); err == nil {
		t.Fatal("direct renderer bypassed hard same-page validation")
	}
}

func TestSheetRelationReusesValidExistingPositions(t *testing.T) {
	host, child := relationZone("H", 100, 100), relationZone("C", 100, 100)
	host.SheetPosition = &SchematicSheetPosition{X: 100, Y: 300}
	child.SheetPosition = &SchematicSheetPosition{X: 700, Y: 150}
	child.Placement = &SchematicZonePlacement{SamePageAs: "H", PreferAdjacent: true}
	in := relationSheet(1000, 400, host, child)
	plan, err := PlanSchematicSheets(in)
	if err != nil || plan.PlacementMode != "reused" || !reflect.DeepEqual(in.Zones, plan.Pages[0].Zones) {
		t.Fatal("soft preference forcibly moved valid existing positions", err)
	}
}
