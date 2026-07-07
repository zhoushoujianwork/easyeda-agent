package app

import (
	"math"
	"testing"
)

// part is a tiny test helper: a placed part whose anchor sits at its bbox center
// (anchorOffset 0), so target-center == target-anchor and the asserted
// coordinates read directly.
func part(des string, minX, minY, maxX, maxY float64) alPart {
	b := layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
	cx, cy := bboxCenter(b)
	return alPart{Designator: des, PrimitiveID: "id-" + des, AnchorX: cx, AnchorY: cy, BBox: b, HasBBox: true}
}

func a4Sheet() *layoutBBox { return &layoutBBox{MinX: 0, MinY: 0, MaxX: 1188, MaxY: 840} }

func rulesAL() autolayoutRules { return defaultAutolayoutRules() }

func placementOf(rep alReport, des string) (alPlacement, bool) {
	for _, p := range rep.Placements {
		if p.Designator == des {
			return p, true
		}
	}
	return alPlacement{}, false
}

func TestPlanAutolayout_CorePlacedAtZoneCenter(t *testing.T) {
	parts := []alPart{part("U1", 0, 0, 40, 40)}
	modules := []alModuleSpec{{Name: "MCU", Zone: "center", Core: "U1", Parts: []string{"U1"}}}
	rep := planAutolayout(modules, parts, a4Sheet(), rulesAL())
	if !rep.OK {
		t.Fatalf("expected OK plan, got errors=%v", rep.Errors)
	}
	pl, ok := placementOf(rep, "U1")
	if !ok {
		t.Fatal("U1 not placed")
	}
	// center zone center: x in [1/3,2/3] of 1188 → mid = 594; y full → 420.
	// The emitted anchor is grid-snapped (schAnchorGrid) so pins stay on the
	// netflag grid — accept within half a grid step of the exact center.
	zr := zoneRect("center", *a4Sheet())
	wantX, wantY := bboxCenter(zr)
	if math.Abs(pl.X-wantX) > schAnchorGrid/2 || math.Abs(pl.Y-wantY) > schAnchorGrid/2 {
		t.Errorf("core not at (snapped) zone center: got (%.2f,%.2f) want (%.2f,%.2f)±%.1f", pl.X, pl.Y, wantX, wantY, schAnchorGrid/2.0)
	}
	if pl.X != math.Round(pl.X/schAnchorGrid)*schAnchorGrid || pl.Y != math.Round(pl.Y/schAnchorGrid)*schAnchorGrid {
		t.Errorf("anchor (%.2f,%.2f) not on the %v-grid", pl.X, pl.Y, schAnchorGrid)
	}
}

func TestPlanAutolayout_Deterministic(t *testing.T) {
	parts := []alPart{
		part("U1", 0, 0, 40, 40),
		part("C18", 0, 0, 8, 8),
		part("C19", 0, 0, 8, 8),
		part("R6", 0, 0, 6, 12),
		part("U8", 500, 500, 540, 540),
		part("C28", 500, 500, 508, 508),
	}
	modules := []alModuleSpec{
		{Name: "MCU", Zone: "center", Core: "U1", Parts: []string{"U1", "C18", "C19", "R6"}},
		{Name: "SD", Zone: "right", Core: "U8", Parts: []string{"U8", "C28"}},
	}
	a := planAutolayout(modules, parts, a4Sheet(), rulesAL())
	b := planAutolayout(modules, parts, a4Sheet(), rulesAL())
	if len(a.Placements) != len(b.Placements) {
		t.Fatalf("placement count differs: %d vs %d", len(a.Placements), len(b.Placements))
	}
	for i := range a.Placements {
		if a.Placements[i] != b.Placements[i] {
			t.Fatalf("non-deterministic at %d: %+v vs %+v", i, a.Placements[i], b.Placements[i])
		}
	}
}

func TestPlanAutolayout_NoOverlaps(t *testing.T) {
	parts := []alPart{
		part("U1", 0, 0, 40, 40),
		part("C18", 0, 0, 8, 8),
		part("C19", 0, 0, 8, 8),
		part("C20", 0, 0, 8, 8),
		part("R6", 0, 0, 6, 12),
		part("R7", 0, 0, 6, 12),
	}
	modules := []alModuleSpec{{Name: "MCU", Zone: "center", Core: "U1",
		Parts: []string{"U1", "C18", "C19", "C20", "R6", "R7"}}}
	rep := planAutolayout(modules, parts, a4Sheet(), rulesAL())
	if !rep.OK {
		t.Fatalf("expected OK, got errors=%v", rep.Errors)
	}
	if rep.Validation.PartOverlaps != 0 {
		t.Errorf("expected 0 overlaps, got %d", rep.Validation.PartOverlaps)
	}
	if len(rep.Placements) != 6 {
		t.Errorf("expected 6 placements, got %d", len(rep.Placements))
	}
}

func TestPlanAutolayout_MissingCoreErrors(t *testing.T) {
	parts := []alPart{part("C18", 0, 0, 8, 8)}
	modules := []alModuleSpec{{Name: "MCU", Zone: "center", Core: "U1", Parts: []string{"U1", "C18"}}}
	rep := planAutolayout(modules, parts, a4Sheet(), rulesAL())
	if rep.OK {
		t.Fatal("expected NOT OK when core is missing")
	}
	if len(rep.Errors) == 0 {
		t.Fatal("expected a clear error for the missing core")
	}
}

func TestPlanAutolayout_TitleBlockKeepout(t *testing.T) {
	// A module pinned to the right-bottom zone (where the A4 title block sits)
	// must NOT land any part inside the keep-out.
	parts := []alPart{
		part("U8", 0, 0, 40, 40),
		part("C28", 0, 0, 8, 8),
		part("C29", 0, 0, 8, 8),
	}
	modules := []alModuleSpec{{Name: "SD", Zone: "right-bottom", Core: "U8", Parts: []string{"U8", "C28", "C29"}}}
	rep := planAutolayout(modules, parts, a4Sheet(), rulesAL())
	if rep.Validation.TitleBlockHits != 0 {
		t.Errorf("expected 0 title-block hits, got %d", rep.Validation.TitleBlockHits)
	}
	if !rep.OK {
		t.Fatalf("expected OK plan, got errors=%v", rep.Errors)
	}
	// Every placed box must be clear of the keep-out.
	tb, _ := titleBlockKeepout(a4Sheet())
	for _, p := range rep.Placements {
		// reconstruct the placed box center from the anchor (offset 0 here)
		if tb != nil && p.X >= tb.MinX && p.X <= tb.MaxX && p.Y >= tb.MinY && p.Y <= tb.MaxY {
			t.Errorf("%s landed inside the title-block keep-out at (%.2f,%.2f)", p.Designator, p.X, p.Y)
		}
	}
}

func TestPlanAutolayout_CollisionRetryAcrossModules(t *testing.T) {
	// Two modules whose zones both reach toward the center: parts must not
	// overlap even when cores are close. Validation must stay clean.
	parts := []alPart{
		part("U1", 0, 0, 60, 60),
		part("C1", 0, 0, 10, 10),
		part("C2", 0, 0, 10, 10),
		part("U2", 0, 0, 60, 60),
		part("C3", 0, 0, 10, 10),
		part("C4", 0, 0, 10, 10),
	}
	modules := []alModuleSpec{
		{Name: "A", Zone: "center", Core: "U1", Parts: []string{"U1", "C1", "C2"}},
		{Name: "B", Zone: "center", Core: "U2", Parts: []string{"U2", "C3", "C4"}},
	}
	rep := planAutolayout(modules, parts, a4Sheet(), rulesAL())
	if !rep.OK {
		t.Fatalf("expected OK, got errors=%v", rep.Errors)
	}
	if rep.Validation.PartOverlaps != 0 {
		t.Errorf("two same-zone modules still overlap: %d", rep.Validation.PartOverlaps)
	}
}

func TestPlanAutolayout_SkipsUnplacedPeripheral(t *testing.T) {
	// A peripheral named in the spec but not placed is warned + skipped, not a
	// hard error (v1 only moves existing parts).
	parts := []alPart{
		part("U1", 0, 0, 40, 40),
		part("C18", 0, 0, 8, 8),
	}
	modules := []alModuleSpec{{Name: "MCU", Zone: "center", Core: "U1", Parts: []string{"U1", "C18", "C99"}}}
	rep := planAutolayout(modules, parts, a4Sheet(), rulesAL())
	if !rep.OK {
		t.Fatalf("expected OK (missing peripheral is a warning), got errors=%v", rep.Errors)
	}
	if len(rep.Warnings) == 0 {
		t.Error("expected a warning for the unplaced peripheral C99")
	}
	if _, ok := placementOf(rep, "C99"); ok {
		t.Error("C99 should not have been placed")
	}
}

func TestPlanAutolayout_ProvisionalWhenNoSheet(t *testing.T) {
	parts := []alPart{
		part("U1", 100, 100, 140, 140),
		part("C1", 100, 100, 108, 108),
	}
	modules := []alModuleSpec{{Name: "MCU", Zone: "center", Core: "U1", Parts: []string{"U1", "C1"}}}
	rep := planAutolayout(modules, parts, nil, rulesAL())
	if !rep.TitleBlockProvisional {
		t.Error("expected provisional title block when no sheet bbox")
	}
	if !rep.OK {
		t.Fatalf("expected OK plan, got errors=%v", rep.Errors)
	}
}

func TestZoneRect_Partitions(t *testing.T) {
	u := layoutBBox{MinX: 0, MinY: 0, MaxX: 900, MaxY: 600}
	lt := zoneRect("left-top", u)
	if lt.MinX != 0 || lt.MaxX != 300 || lt.MinY != 0 || lt.MaxY != 300 {
		t.Errorf("left-top wrong: %+v", lt)
	}
	rb := zoneRect("right-bottom", u)
	if rb.MinX != 600 || rb.MaxX != 900 || rb.MinY != 300 || rb.MaxY != 600 {
		t.Errorf("right-bottom wrong: %+v", rb)
	}
	// unknown zone → center, full height
	ce := zoneRect("nonsense", u)
	if ce.MinX != 300 || ce.MaxX != 600 || ce.MinY != 0 || ce.MaxY != 600 {
		t.Errorf("unknown zone should fall back to center/full: %+v", ce)
	}
}

func TestMakePlacement_AnchorOffsetPreserved(t *testing.T) {
	// A part whose anchor is NOT at its bbox center: moving must shift the anchor
	// by the same delta the bbox center moves.
	p := alPart{Designator: "U1", AnchorX: 0, AnchorY: 0,
		BBox: layoutBBox{MinX: 10, MinY: 10, MaxX: 30, MaxY: 30}, HasBBox: true}
	// bbox center is (20,20); move it to (120, 220) → anchor delta (+100,+200).
	pl := makePlacement(p, 120, 220, "M", 0)
	if pl.X != 100 || pl.Y != 200 {
		t.Errorf("anchor offset not preserved: got (%.2f,%.2f) want (100,200)", pl.X, pl.Y)
	}
}

// Probe round #3 regression: zone centering produces fractional centers
// (825/4 = 206.25); the emitted anchor must land on the 5-unit schematic grid
// or every pin goes off-grid and connect_pin stubs fail ("Failed to create
// pin-stub wire") or float.
func TestMakePlacementSnapsAnchorToGrid(t *testing.T) {
	p := alPart{
		Designator: "U2",
		AnchorX:    200, AnchorY: 600, // placed on-grid by the user
		HasBBox: true,
		BBox:    layoutBBox{MinX: 170, MinY: 570, MaxX: 230, MaxY: 630},
	}
	// Fractional zone center like the real A4 quarters produce.
	got := makePlacement(p, 195.0, 618.75, "POWER", 0)
	for _, v := range []float64{got.X, got.Y} {
		if v != math.Round(v/schAnchorGrid)*schAnchorGrid {
			t.Fatalf("anchor (%v, %v) not on the %v-grid", got.X, got.Y, schAnchorGrid)
		}
	}
	// Still near the requested center (within one grid step per axis).
	if math.Abs(got.X-195) > schAnchorGrid || math.Abs(got.Y-(600+18.75)) > schAnchorGrid {
		t.Fatalf("snap moved the part too far: (%v, %v)", got.X, got.Y)
	}
}
