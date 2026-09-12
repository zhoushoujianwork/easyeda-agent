package app

import (
	"strings"
	"testing"
)

func TestSchematicZShelfBudgetIsExplicitAndCannotMoveInput(t *testing.T) {
	sheet := SchematicRenderSheet{
		Bounds:  SchematicBox{MinX: 0, MinY: 0, MaxX: 500, MaxY: 500},
		Border:  SchematicBox{MinX: 0, MinY: 0, MaxX: 500, MaxY: 500},
		Padding: 10, Gap: 10, Keepouts: []SchematicBox{}, Flow: "z",
	}
	position := &SchematicSheetPosition{X: 100, Y: 100}
	zone := SchematicRenderZone{ID: "budget-fixture", SheetPosition: position,
		Frame: &schFrameSpec{Rect: SchematicBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}}}
	shelf := newSchematicZShelf(sheet)
	search := &schematicZSearch{candidates: schematicZMaxCandidates}
	ok, err := shelf.place(zone, sheet, search)
	if ok || err == nil || !strings.Contains(err.Error(), "budget exhausted") || !strings.Contains(err.Error(), "no capacity conclusion") {
		t.Fatalf("expected bounded failure, got ok=%v, err=%v", ok, err)
	}
	if len(shelf.zones) != 0 || search.candidates != schematicZMaxCandidates || zone.SheetPosition != position || *position != (SchematicSheetPosition{X: 100, Y: 100}) {
		t.Fatal("a budget failure changed source coordinates or returned partial placements")
	}
}
