package app

import (
	"strings"
	"testing"
)

func TestComposePaperlessDerivesBoundsAndRejectsMixedPaper(t *testing.T) {
	src := composeFixture(3)
	src.Paperless = true
	src.Sheet = layoutBBox{}
	plan, err := planSchComposition(src)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Paperless || plan.Rows != 1 || plan.PlacementBoundarySource != "content-derived-no-sheet" {
		t.Fatalf("paperless composition must use a single unlimited content row: %+v", plan)
	}
	for _, frame := range plan.Layout.Frames {
		if !boxInside(frame.Rect, plan.Sheet) {
			t.Fatalf("derived extent excludes module %s", frame.ID)
		}
	}
	src.Sheet = layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	if _, err := planSchComposition(src); err == nil || !strings.Contains(err.Error(), "paperless composition requires no sheet") {
		t.Fatalf("mixed physical/virtual sheet must fail: %v", err)
	}
}

func TestComposePaperlessApplyRequiresZeroPhysicalSheets(t *testing.T) {
	src := composeFixture(2)
	src.Paperless = true
	src.Sheet = layoutBBox{}
	plan, err := planSchComposition(src)
	if err != nil {
		t.Fatal(err)
	}
	_, env := composeApplyFixture(t, true)
	result := env["result"].(map[string]any)
	parts := result["components"].([]any)
	result["components"] = parts[1:]
	result["count"] = len(parts) - 1
	for _, item := range result["components"].([]any) {
		part := item.(map[string]any)
		for _, placement := range plan.Layout.Placements {
			if part["designator"] == placement.Designator {
				part["x"], part["y"], part["bbox"] = placement.X, placement.Y, placement.BBox
			}
		}
	}
	if _, err := schCompositionPlaybook(plan, composeApplyBytes(t, env), false); err == nil || strings.Contains(err.Error(), "sheet") {
		// This fixture retains old wire geometry, so only the normal target
		// mismatch is expected after the physical sheet guard passes.
		t.Fatalf("unexpected paperless target result: %v", err)
	}
	result["components"] = append([]any{parts[0]}, result["components"].([]any)...)
	result["count"] = len(parts)
	if _, err := schCompositionPlaybook(plan, composeApplyBytes(t, env), false); err == nil || !strings.Contains(err.Error(), "target sheet geometry differs") {
		t.Fatalf("physical sheet on paperless target must fail: %v", err)
	}
}

func TestComposePaperlessWiresBoundBlankPageWithoutClear(t *testing.T) {
	src := composeFixture(2)
	src.Paperless, src.Sheet = true, layoutBBox{}
	plan, err := planSchComposition(src)
	if err != nil {
		t.Fatal(err)
	}
	_, env := preserveComposeFixture(t)
	result := env["result"].(map[string]any)
	parts := []any{}
	for _, item := range result["components"].([]any) {
		part := item.(map[string]any)
		if part["componentType"] != "part" {
			continue
		}
		for _, item := range part["pins"].([]any) {
			pin := item.(map[string]any)
			pin["net"], pin["noConnected"] = "", false
		}
		parts = append(parts, part)
	}
	result["components"], result["count"], result["wires"] = parts, len(parts), []any{}
	result["connectivitySummary"] = map[string]any{"scope": "activePage", "wires": 0, "buses": 0, "netflags": 0, "netports": 0, "netlabels": 0, "shortSymbols": 0}
	pb, err := schCompositionPlaybook(plan, composeApplyBytes(t, env), true, true)
	if err != nil {
		t.Fatal(err)
	}
	composeStep(t, pb, "verify-fully-unwired-before-move")
	_, gate := composeStep(t, pb, "paperless-schematic-gate")
	if gate.Flags["strict"] != nil {
		t.Fatal("paperless page cannot claim the strict sheet boundary gate")
	}
	for _, step := range pb.Steps {
		if step.Action == "schematic.page.clear" || step.Run == "sch clear" {
			t.Fatal("paperless blank-page queue must preserve every original part")
		}
	}
	result["components"] = append(parts, map[string]any{"componentType": "sheet", "primitiveId": "unexpected-sheet", "bbox": plan.Sheet})
	result["count"] = len(parts) + 1
	if _, err := schCompositionPlaybook(plan, composeApplyBytes(t, env), true, true); err == nil || !strings.Contains(err.Error(), "target sheet geometry differs") {
		t.Fatalf("sheet drift was accepted: %v", err)
	}
}
