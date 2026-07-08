package app

import "testing"

func bb(minX, minY, maxX, maxY float64) *layoutBBox {
	return &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
}

func TestAnalyzeLayout_Overlap(t *testing.T) {
	comps := []layoutComp{
		{Designator: "R1", BBox: bb(0, 0, 10, 10)},
		{Designator: "C2", BBox: bb(5, 5, 15, 15)}, // overlaps R1 by 5×5
	}
	rep := analyzeLayout(comps, 2.54, -1)
	if rep.OK {
		t.Fatal("expected OK=false when components overlap")
	}
	if len(rep.Overlaps) != 1 {
		t.Fatalf("expected 1 overlap, got %d", len(rep.Overlaps))
	}
	f := rep.Overlaps[0]
	if f.A != "C2" || f.B != "R1" { // labels sorted
		t.Errorf("expected pair C2↔R1, got %s↔%s", f.A, f.B)
	}
	if f.OvX != 5 || f.OvY != 5 {
		t.Errorf("expected overlap 5×5, got %.2f×%.2f", f.OvX, f.OvY)
	}
}

func TestAnalyzeLayout_TightSpacing(t *testing.T) {
	comps := []layoutComp{
		{Designator: "U1", BBox: bb(0, 0, 10, 10)},
		{Designator: "C5", BBox: bb(11, 0, 21, 10)}, // 1mm gap horizontally
	}
	rep := analyzeLayout(comps, 2.54, -1)
	if !rep.OK {
		t.Fatal("tight spacing alone should not fail OK (only overlaps do)")
	}
	if len(rep.TightPairs) != 1 {
		t.Fatalf("expected 1 tight pair, got %d", len(rep.TightPairs))
	}
	if g := rep.TightPairs[0].Gap; g != 1 {
		t.Errorf("expected gap 1.0mm, got %.2f", g)
	}
}

func TestAnalyzeLayout_Clear(t *testing.T) {
	comps := []layoutComp{
		{Designator: "U1", BBox: bb(0, 0, 10, 10)},
		{Designator: "C5", BBox: bb(20, 0, 30, 10)}, // 10mm gap, well clear
	}
	rep := analyzeLayout(comps, 2.54, -1)
	if !rep.OK || len(rep.Overlaps) != 0 || len(rep.TightPairs) != 0 {
		t.Fatalf("expected clean report, got %+v", rep)
	}
}

func TestAnalyzeLayout_TouchingEdgesNotOverlap(t *testing.T) {
	comps := []layoutComp{
		{Designator: "A", BBox: bb(0, 0, 10, 10)},
		{Designator: "B", BBox: bb(10, 0, 20, 10)}, // shares an edge, gap 0
	}
	rep := analyzeLayout(comps, 2.54, -1)
	if len(rep.Overlaps) != 0 {
		t.Fatalf("touching edges must not count as overlap, got %d", len(rep.Overlaps))
	}
	if len(rep.TightPairs) != 1 || rep.TightPairs[0].Gap != 0 {
		t.Fatalf("expected one tight pair at gap 0, got %+v", rep.TightPairs)
	}
}

func TestAnalyzeLayout_UnassignedDesignatorFallsBackToID(t *testing.T) {
	comps := []layoutComp{
		{ID: "aaa111", Designator: "C?", BBox: bb(0, 0, 10, 10)},
		{ID: "bbb222", Designator: "C?", BBox: bb(5, 5, 15, 15)}, // overlap
	}
	rep := analyzeLayout(comps, 2.54, -1)
	if len(rep.Overlaps) != 1 {
		t.Fatalf("expected 1 overlap, got %d", len(rep.Overlaps))
	}
	f := rep.Overlaps[0]
	// Both designators are unassigned ("C?") → labels disambiguate via id.
	if f.A == f.B {
		t.Fatalf("unassigned designators must disambiguate, got %q ↔ %q", f.A, f.B)
	}
	if f.A != "C?@aaa111" || f.B != "C?@bbb222" {
		t.Errorf("expected id-suffixed labels, got %q ↔ %q", f.A, f.B)
	}
}

func TestFilterLayoutComps_ExcludesNonPartsByDefault(t *testing.T) {
	comps := []layoutComp{
		{Designator: "R1", ComponentType: "part", BBox: bb(0, 0, 10, 10)},
		{Designator: "SHEET", ComponentType: "sheet", BBox: bb(-100, -100, 400, 300)}, // full-page frame
		{ID: "nf1", ComponentType: "netflag", BBox: bb(0, 0, 2, 2)},
		{Designator: "C2", ComponentType: "part", BBox: bb(20, 0, 30, 10)},
	}
	kept, skipped := filterLayoutComps(comps, false)
	if len(kept) != 2 {
		t.Fatalf("expected 2 parts kept, got %d", len(kept))
	}
	if skipped != 2 {
		t.Fatalf("expected 2 non-parts skipped, got %d", skipped)
	}
	for _, c := range kept {
		if c.ComponentType != "part" {
			t.Errorf("non-part leaked through: %+v", c)
		}
	}
}

func TestFilterLayoutComps_IncludeNonPartsKeepsAll(t *testing.T) {
	comps := []layoutComp{
		{Designator: "R1", ComponentType: "part", BBox: bb(0, 0, 10, 10)},
		{Designator: "SHEET", ComponentType: "sheet", BBox: bb(-100, -100, 400, 300)},
	}
	kept, skipped := filterLayoutComps(comps, true)
	if len(kept) != 2 || skipped != 0 {
		t.Fatalf("include-non-parts must keep all, got kept=%d skipped=%d", len(kept), skipped)
	}
}

func TestFilterLayoutComps_EmptyTypeKept(t *testing.T) {
	// An older connector that doesn't emit componentType must not have every
	// component silently dropped.
	comps := []layoutComp{
		{Designator: "R1", BBox: bb(0, 0, 10, 10)},
		{Designator: "C2", BBox: bb(20, 0, 30, 10)},
	}
	kept, skipped := filterLayoutComps(comps, false)
	if len(kept) != 2 || skipped != 0 {
		t.Fatalf("empty componentType must be kept, got kept=%d skipped=%d", len(kept), skipped)
	}
}

func TestFilterLayoutComps_SheetNoLongerFalseOverlaps(t *testing.T) {
	// Regression for issue #13: a full-page sheet bbox overlaps every real part.
	// After filtering, the analysis must report a clean layout.
	comps := []layoutComp{
		{Designator: "SHEET", ComponentType: "sheet", BBox: bb(-100, -100, 400, 300)},
		{Designator: "R1", ComponentType: "part", BBox: bb(0, 0, 10, 10)},
		{Designator: "C2", ComponentType: "part", BBox: bb(20, 0, 30, 10)},
	}
	parts, skipped := filterLayoutComps(comps, false)
	rep := analyzeLayout(parts, 2.54, -1)
	rep.SkippedNonParts = skipped
	if !rep.OK {
		t.Fatalf("expected clean report after excluding sheet, got %+v", rep.Overlaps)
	}
	if rep.SkippedNonParts != 1 {
		t.Errorf("expected SkippedNonParts=1, got %d", rep.SkippedNonParts)
	}
}

func TestAnalyzeLayout_NoBBoxSkipped(t *testing.T) {
	comps := []layoutComp{
		{Designator: "R1", BBox: bb(0, 0, 10, 10)},
		{Designator: "R2"}, // no bbox → skipped, recorded
	}
	rep := analyzeLayout(comps, 2.54, -1)
	if rep.WithBBox != 1 {
		t.Errorf("expected WithBBox=1, got %d", rep.WithBBox)
	}
	if len(rep.NoBBox) != 1 || rep.NoBBox[0] != "R2" {
		t.Errorf("expected R2 recorded as no-bbox, got %v", rep.NoBBox)
	}
}

func pin(num string, x, y float64) layoutPin { return layoutPin{Number: num, X: x, Y: y} }

func TestAnalyzeLayout_PinCoincidenceError(t *testing.T) {
	// Issue #63: a 1210 cap and a 0402 resistor whose pins land on the same
	// point — bboxes never touch, but the shared pin is an implicit short.
	comps := []layoutComp{
		{Designator: "C1", BBox: bb(255, 200, 265, 210), Pins: []layoutPin{pin("1", 255, 205), pin("2", 260, 205)}},
		{Designator: "R_Q3G", BBox: bb(260, 205, 270, 215), Pins: []layoutPin{pin("1", 260, 205), pin("2", 265, 205)}},
	}
	rep := analyzeLayout(comps, 2.54, 0)
	if rep.OK {
		t.Fatal("expected OK=false when pins of different parts coincide")
	}
	if len(rep.PinCoincidences) != 1 {
		t.Fatalf("expected 1 pin-coincidence, got %d: %+v", len(rep.PinCoincidences), rep.PinCoincidences)
	}
	f := rep.PinCoincidences[0]
	if f.Type != "pin-coincidence" {
		t.Errorf("expected type pin-coincidence, got %q", f.Type)
	}
	if f.A != "C1" || f.B != "R_Q3G" {
		t.Errorf("expected pair C1↔R_Q3G, got %s↔%s", f.A, f.B)
	}
	if f.APin != "2" || f.BPin != "1" {
		t.Errorf("expected pins C1:2 ↔ R_Q3G:1, got %s ↔ %s", f.APin, f.BPin)
	}
	if f.X != 260 || f.Y != 205 {
		t.Errorf("expected shared point (260,205), got (%.2f,%.2f)", f.X, f.Y)
	}
}

func TestAnalyzeLayout_SamePartPinsNoFalsePositive(t *testing.T) {
	// A single symbol's own pins sit at fixed offsets; they must never collide
	// with each other even if numerically close.
	comps := []layoutComp{
		{Designator: "U1", BBox: bb(0, 0, 20, 20), Pins: []layoutPin{pin("1", 5, 5), pin("2", 5, 5)}},
	}
	rep := analyzeLayout(comps, 2.54, 0)
	if len(rep.PinCoincidences) != 0 {
		t.Fatalf("same-component pins must not flag, got %+v", rep.PinCoincidences)
	}
	if !rep.OK {
		t.Fatal("single component must report OK")
	}
}

func TestAnalyzeLayout_PinEpsBoundary(t *testing.T) {
	// Two pins 0.5mm apart: clear under strict eps=0, flagged under eps=0.5.
	comps := []layoutComp{
		{Designator: "R1", BBox: bb(0, 0, 10, 10), Pins: []layoutPin{pin("1", 5, 5)}},
		{Designator: "R2", BBox: bb(20, 0, 30, 10), Pins: []layoutPin{pin("1", 5.5, 5)}},
	}
	if rep := analyzeLayout(comps, 2.54, 0); len(rep.PinCoincidences) != 0 {
		t.Fatalf("eps=0 must not flag pins 0.5mm apart, got %+v", rep.PinCoincidences)
	}
	rep := analyzeLayout(comps, 2.54, 0.5)
	if len(rep.PinCoincidences) != 1 {
		t.Fatalf("eps=0.5 must flag pins exactly 0.5mm apart, got %d", len(rep.PinCoincidences))
	}
}

func TestAnalyzeLayout_PinCheckDisabled(t *testing.T) {
	// Negative eps disables the pin check (internal bbox-only callers).
	comps := []layoutComp{
		{Designator: "C1", BBox: bb(0, 0, 10, 10), Pins: []layoutPin{pin("1", 5, 5)}},
		{Designator: "R1", BBox: bb(20, 0, 30, 10), Pins: []layoutPin{pin("1", 5, 5)}},
	}
	rep := analyzeLayout(comps, 2.54, -1)
	if len(rep.PinCoincidences) != 0 || !rep.OK {
		t.Fatalf("negative eps must disable pin check, got %+v OK=%v", rep.PinCoincidences, rep.OK)
	}
}

func TestParseLayoutComps_ExtractsPins(t *testing.T) {
	result := map[string]any{
		"components": []any{
			map[string]any{
				"primitiveId": "aaa",
				"designator":  "C1",
				"componentType": "part",
				"bbox": map[string]any{"minX": 255.0, "minY": 200.0, "maxX": 265.0, "maxY": 210.0},
				"pins": []any{
					map[string]any{"pinNumber": "1", "x": 255.0, "y": 205.0},
					map[string]any{"pinNumber": "2", "x": 260.0, "y": 205.0},
				},
			},
		},
	}
	comps, err := parseLayoutComps(result)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(comps) != 1 || len(comps[0].Pins) != 2 {
		t.Fatalf("expected 1 comp with 2 pins, got %+v", comps)
	}
	if comps[0].Pins[1].Number != "2" || comps[0].Pins[1].X != 260 || comps[0].Pins[1].Y != 205 {
		t.Errorf("unexpected pin[1]: %+v", comps[0].Pins[1])
	}
}
