package app

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func TestComposeNumberBoundaryPreservesStrictMarkerGuard(t *testing.T) {
	p := powerLayoutPlan{Flags: []powerLayoutFlag{{Net: "GND", Kind: "ground", Direction: "down", PinX: math.Nextafter(205, 206), PinY: math.Nextafter(450, 451), Offset: 10}}}
	live := map[string]any{
		"connectivitySummary": map[string]any{"scope": "activePage", "buses": 0, "shortSymbols": 0},
		"components":          []any{map[string]any{"componentType": "netflag", "net": "GND", "x": 205.0, "y": 440.0, "rotation": 0.0}},
		"wires":               []any{map[string]any{"x0": 205.0, "y0": 450.0, "x1": 205.0, "y1": 440.0}},
	}
	before, _ := json.Marshal(live)
	if err := (&schematicDrawingExpectation{Flags: p.Flags}).check(live); err == nil {
		t.Fatal("fixture must reproduce the exact marker-key arithmetic-tail failure")
	}
	normalizeSchCompositionGeometry(&p)
	if p.Flags[0].PinX != 205 || p.Flags[0].PinY != 450 {
		t.Fatal("API arithmetic tails were not removed")
	}
	if err := (&schematicDrawingExpectation{Flags: p.Flags}).check(live); err != nil {
		t.Fatalf("normalized plan rejected exact official marker readback: %v", err)
	}
	after, _ := json.Marshal(live)
	if !bytes.Equal(before, after) {
		t.Fatal("readback evidence was changed")
	}
	// No grid snap and no relaxed comparator: a real sub-grid marker movement
	// remains a mismatch even while the wire inventory is held unchanged.
	live["components"].([]any)[0].(map[string]any)["x"] = 205.000001
	if err := (&schematicDrawingExpectation{Flags: p.Flags}).check(live); err == nil {
		t.Fatal("real sub-grid marker displacement was hidden")
	}
	p.Flags[0].PinX = 205.0000001
	normalizeSchCompositionGeometry(&p)
	if p.Flags[0].PinX != 205.0000001 {
		t.Fatal("real sub-grid geometry was snapped to the schematic grid")
	}
	if err := (&schematicDrawingExpectation{Flags: p.Flags}).check(live); err == nil {
		t.Fatal("different sub-grid marker positions were accepted")
	}
}

func TestComposeNumberBoundaryNormalizesGeneratedGeometryOnly(t *testing.T) {
	src := composeFixture(1)
	// Put an arithmetic tail on a marker lead orthogonal to an existing wire;
	// source evidence remains explicit and valid under the existing tolerance.
	src.Modules[0].Flags[0].PinY += 1e-10
	before, _ := json.Marshal(src)
	plan, err := planSchComposition(src)
	if err != nil {
		t.Fatal(err)
	}
	if y := plan.Layout.Flags[0].PinY; y != math.Round(y) {
		t.Fatalf("compile boundary did not normalize translated marker coordinate: %.17g", y)
	}
	after, _ := json.Marshal(src)
	if !bytes.Equal(before, after) {
		t.Fatal("compose changed source measurement evidence")
	}
	flag := math.Nextafter(205, 206)
	p := powerLayoutPlan{
		Placements: []powerLayoutPlacement{{X: flag, Y: flag, Rotation: 90, BBox: layoutBBox{flag, flag, 210.0000001, 215}, TextBBoxes: []layoutBBox{{flag, flag, 210, 215}}, Pins: []powerLayoutPin{{X: flag, Y: flag}}}},
		Wires:      []powerLayoutWire{{Points: [][2]float64{{flag, flag}, {210, flag}}}},
		Frames:     []schFrameSpec{{Rect: layoutBBox{flag, flag, 220, 225}, TitleX: flag, TitleY: flag, FontSize: 20, TitleLayout: &schFrameTitleLayout{Width: flag, Height: 20, Clearance: 5, Obstacles: []layoutBBox{{flag, flag, 210, 215}}}}},
	}
	normalizeSchCompositionGeometry(&p)
	c, f := p.Placements[0], p.Frames[0]
	if c.X != 205 || c.Y != 205 || c.BBox.MinX != 205 || c.BBox.MaxX != 210.0000001 || c.TextBBoxes[0].MinX != 205 || c.Pins[0].X != 205 || p.Wires[0].Points[0][0] != 205 || f.Rect.MinX != 205 || f.TitleX != 205 || f.TitleLayout.Width != 205 || f.TitleLayout.Obstacles[0].MinX != 205 {
		t.Fatal("generated geometry normalization missed a coordinate or erased a real displacement")
	}
	first, _ := json.Marshal(p)
	normalizeSchCompositionGeometry(&p)
	second, _ := json.Marshal(p)
	if !bytes.Equal(first, second) {
		t.Fatal("numeric compile normalization must be idempotent")
	}
}

func TestComposeNumberBoundaryReverifiesWithoutReplace(t *testing.T) {
	plan, live := composeApplyFixture(t, true)
	before := composeApplyBytes(t, live)
	plan.Layout.Flags[0].PinX = math.Nextafter(plan.Layout.Flags[0].PinX, plan.Layout.Flags[0].PinX+1)
	if _, err := schCompositionPlaybook(plan, before, false); err == nil {
		t.Fatal("unnormalized marker-key tail should refuse matching-only compile")
	}
	normalizeSchCompositionGeometry(&plan.Layout)
	pb, err := schCompositionPlaybook(plan, before, false)
	if err != nil {
		t.Fatalf("existing runtime guard should accept a verification-only plan: %v", err)
	}
	for _, step := range pb.Steps {
		if step.ID == "reset-target-preserving-sheet" || step.Action == "schematic.component.place" || step.Action == "schematic.wire.create" || step.Action == "schematic.power.connect_pin" {
			t.Fatalf("tail-only normalization unexpectedly generated circuit mutation: %s", step.ID)
		}
	}
	if !bytes.Equal(before, composeApplyBytes(t, live)) {
		t.Fatal("matching-only compile changed the fresh before evidence")
	}
}
