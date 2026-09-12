package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func composePreplacedFixture(t *testing.T) (schCompositionSource, SchematicRenderInput) {
	t.Helper()
	src := composeFixture(2)
	src.Sheet = layoutBBox{MinX: 0, MinY: 0, MaxX: 1500, MaxY: 900}
	border := layoutBBox{MinX: 10, MinY: 10, MaxX: 1490, MaxY: 890}
	src.SheetBorder = &border
	src.Keepouts = []layoutBBox{}
	spacing := 20.0
	in := SchematicRenderInput{SchemaVersion: 1, Spacing: &spacing, Sheet: &SchematicRenderSheet{Bounds: src.Sheet, Border: border, Keepouts: []SchematicBox{}, Padding: spacing, Gap: spacing, Flow: "z"}}
	for i, m := range src.Modules {
		p := powerLayoutPlan{Placements: m.Placements, Wires: m.Wires, Flags: m.Flags}
		frame, err := measureSchModuleFrameObstaclesSpacing(m.ID, m.Title, powerLayoutContentObstacles(&p), nil, nil, &spacing)
		if err != nil {
			t.Fatal(err)
		}
		bounds := powerLayoutContentBounds(&p)
		c := src.Connectivity.Components[i]
		layout := &SchematicLayoutResult{SchemaVersion: 1, Placements: m.Placements, Wires: m.Wires, Flags: m.Flags, ComponentIDs: map[string]string{c.Ref: c.ID}, PinStates: map[string]map[string]string{c.ID: {"3": "nc"}}}
		in.Zones = append(in.Zones, SchematicRenderZone{ID: m.ID, Title: m.Title, CoreComponentID: c.ID, Layout: layout, Frame: &frame, ContentBounds: &bounds})
	}
	pages, err := PlanSchematicSheets(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages.Pages) != 1 {
		t.Fatal("synthetic fixture should fit one page")
	}
	return src, pages.Pages[0]
}

func TestComposePreplacedPreservesTwentyRawSpacingAndGeometry(t *testing.T) {
	src, page := composePreplacedFixture(t)
	sourceBefore, _ := json.Marshal(src)
	pageBefore, _ := json.Marshal(page)
	plan, err := planSchCompositionWithPage(src, &page)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PageMargin != 20 || plan.ModuleGap != 20 || plan.UsableBounds != sheetPreviewUsable(*page.Sheet) {
		t.Fatal("approved spacing or usable area changed")
	}
	for i, z := range page.Zones {
		dx, dy := z.SheetPosition.X-z.Frame.Rect.MinX, z.SheetPosition.Y-z.Frame.Rect.MaxY
		want := powerLayoutPlan{Placements: z.Layout.Placements, Wires: z.Layout.Wires, Flags: z.Layout.Flags, Frames: []schFrameSpec{*z.Frame}}
		raw, _ := json.Marshal(want)
		want = powerLayoutPlan{}
		json.Unmarshal(raw, &want)
		translatePowerLayout(&want, dx, dy)
		want.Placements[0].PrimitiveID = ""
		if !reflect.DeepEqual(plan.Layout.Placements[i], want.Placements[0]) || !reflect.DeepEqual(plan.Layout.Frames[i], want.Frames[0]) {
			t.Fatal("preplaced geometry or full title occupancy was repacked")
		}
		if !reflect.DeepEqual(plan.Layout.Wires[i*2:i*2+2], want.Wires) || !reflect.DeepEqual(plan.Layout.Flags[i*2:i*2+2], want.Flags) {
			t.Fatal("wires/markers were not translated as a rigid unit")
		}
	}
	sourceAfter, _ := json.Marshal(src)
	pageAfter, _ := json.Marshal(page)
	if !bytes.Equal(sourceBefore, sourceAfter) || !bytes.Equal(pageBefore, pageAfter) {
		t.Fatal("adapter mutated source evidence")
	}
	before := map[string]any{"context": map[string]any{"projectUuid": src.Connectivity.ProjectID, "documentUuid": src.Connectivity.DocumentID}, "result": map[string]any{"components": []any{map[string]any{"componentType": "sheet", "primitiveId": "sheet", "bbox": src.Sheet}}, "wires": []any{}, "count": 1, "connectivitySummary": map[string]any{"scope": "activePage", "wires": 0, "buses": 0, "shortSymbols": 0}}}
	pb, err := schCompositionPlaybook(plan, composeApplyBytes(t, before), true)
	if err != nil || !pb.RequireFullExecution {
		t.Fatalf("guarded existing writer: %v", err)
	}
	found := false
	for _, step := range pb.Steps {
		found = found || step.ID == "verify-physical-pins-before-wiring"
	}
	if !found {
		t.Fatal("physical geometry guard missing")
	}
}

func TestComposePreplacedRejectsDivergentInput(t *testing.T) {
	tests := []struct {
		name string
		edit func(*schCompositionSource, *SchematicRenderInput)
	}{
		{"wrong paper", func(s *schCompositionSource, p *SchematicRenderInput) { p.Sheet.Bounds.MaxX += 5 }},
		{"wrong border", func(s *schCompositionSource, p *SchematicRenderInput) { p.Sheet.Border.MinX += 5 }},
		{"wrong keepout", func(s *schCompositionSource, p *SchematicRenderInput) {
			p.Sheet.Keepouts = append(p.Sheet.Keepouts, layoutBBox{0, 0, 5, 5})
		}},
		{"missing paper", func(s *schCompositionSource, p *SchematicRenderInput) { p.Sheet = nil }},
		{"missing spacing", func(s *schCompositionSource, p *SchematicRenderInput) { p.Spacing = nil }},
		{"diagnostic", func(s *schCompositionSource, p *SchematicRenderInput) { p.Diagnostic = true }},
		{"unselected variants", func(s *schCompositionSource, p *SchematicRenderInput) {
			p.Zones[0].Variants = []SchematicZoneVariant{{ID: "other"}}
		}},
		{"wrong pin net", func(s *schCompositionSource, p *SchematicRenderInput) {
			s.Modules[0].Placements[0].Pins[0].Net = "wrong"
		}},
		{"canonical net", func(s *schCompositionSource, p *SchematicRenderInput) {
			s.Connectivity.Connections[0].NetID = "stable-gnd-id"
		}},
		{"canonical NC", func(s *schCompositionSource, p *SchematicRenderInput) {
			s.Connectivity.Components[0].Pins[2].NoConnected = false
		}},
		{"wrong core", func(s *schCompositionSource, p *SchematicRenderInput) {
			p.Zones[0].CoreComponentID = p.Zones[1].CoreComponentID
		}},
		{"wrong position", func(s *schCompositionSource, p *SchematicRenderInput) { p.Zones[0].SheetPosition.X += 5 }},
		{"invalid frame", func(s *schCompositionSource, p *SchematicRenderInput) {
			p.Zones[0].Frame.Rect.MinX = p.Zones[0].Frame.Rect.MaxX
		}},
		{"stale title obstacles", func(s *schCompositionSource, p *SchematicRenderInput) { p.Zones[0].Frame.TitleLayout.Obstacles = nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src, page := composePreplacedFixture(t)
			// Separate any shared fixture arrays before applying a one-sided edit.
			raw, _ := json.Marshal(page)
			page = SchematicRenderInput{}
			json.Unmarshal(raw, &page)
			tc.edit(&src, &page)
			if _, err := planSchCompositionWithPage(src, &page); err == nil {
				t.Fatal("divergent input accepted")
			}
		})
	}
}

func TestComposePreplacedDoesNotRunLegacyRepacking(t *testing.T) {
	src, page := composePreplacedFixture(t)
	src.Keepouts = []layoutBBox{{MinX: 10, MinY: 700, MaxX: 400, MaxY: 890}}
	page.Sheet.Keepouts = src.Keepouts
	for i := range page.Zones {
		page.Zones[i].SheetPosition = nil
	}
	pages, err := PlanSchematicSheets(page)
	if err != nil || len(pages.Pages) != 1 {
		t.Fatalf("selected Z placement should clear keepout: %v", err)
	}
	if _, err := planSchComposition(src); err == nil {
		t.Fatal("legacy fixed-row planner should intersect this synthetic keepout")
	}
	if _, err := planSchCompositionWithPage(src, &pages.Pages[0]); err != nil {
		t.Fatalf("valid selected page must not be rejected by legacy repacking: %v", err)
	}
}

func TestComposePreplacedCLIAndRawEvidence(t *testing.T) {
	src, page := composePreplacedFixture(t)
	dir := t.TempDir()
	from, selected, out := filepath.Join(dir, "source.json"), filepath.Join(dir, "page.json"), filepath.Join(dir, "plan.json")
	write := func(path string, v any) {
		raw, _ := json.Marshal(v)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(from, src)
	write(selected, page)
	run := func(output string) error {
		c := newSchComposeCmd(&bytes.Buffer{}, &bytes.Buffer{})
		c.SetArgs([]string{"--from", from, "--layout-page", selected, "--out", output})
		return c.Execute()
	}
	if err := run(out); err != nil {
		t.Fatal(err)
	}
	beforePath, playbookPath := filepath.Join(dir, "before.json"), filepath.Join(dir, "apply.json")
	write(beforePath, map[string]any{"context": map[string]any{"projectUuid": src.Connectivity.ProjectID, "documentUuid": src.Connectivity.DocumentID}, "result": map[string]any{"components": []any{map[string]any{"componentType": "sheet", "primitiveId": "sheet", "bbox": src.Sheet}}, "wires": []any{}, "count": 1, "connectivitySummary": map[string]any{"scope": "activePage", "wires": 0, "buses": 0, "shortSymbols": 0}}})
	compile := newSchComposeCmd(&bytes.Buffer{}, &bytes.Buffer{})
	compile.SetArgs([]string{"--from", from, "--layout-page", selected, "--out", out, "--before", beforePath, "--replace", "--playbook", playbookPath})
	if err := compile.Execute(); err != nil {
		t.Fatal(err)
	}
	queue, err := os.ReadFile(playbookPath)
	if err != nil || !bytes.Contains(queue, []byte(`"requireFullExecution": true`)) {
		t.Fatalf("CLI did not emit protected queue: %v", err)
	}
	good, _ := os.ReadFile(out)
	var rawPage map[string]any
	b, _ := json.Marshal(page)
	json.Unmarshal(b, &rawPage)
	z := rawPage["zones"].([]any)[0].(map[string]any)
	delete(z["frame"].(map[string]any), "titleX")
	write(selected, rawPage)
	if err := run(out); err == nil {
		t.Fatal("missing explicit frame coordinate accepted")
	}
	after, _ := os.ReadFile(out)
	if !bytes.Equal(after, good) {
		t.Fatal("failed validation overwrote last good plan")
	}
	write(selected, page)
	for _, path := range []string{from, selected} {
		if err := run(path); err == nil {
			t.Fatal("input overwrite accepted")
		}
	}
	link := filepath.Join(dir, "alias.json")
	if err := os.Link(selected, link); err != nil {
		t.Fatal(err)
	}
	if err := run(link); err == nil {
		t.Fatal("hardlinked input overwrite accepted")
	}
	z["frame"].(map[string]any)["titleX"] = page.Zones[0].Frame.TitleX
	z["variants"] = []any{}
	write(selected, rawPage)
	if err := run(out); err == nil {
		t.Fatal("unmaterialized variants field accepted")
	}
	write(selected, page)
	var rawSource map[string]any
	b, _ = json.Marshal(src)
	json.Unmarshal(b, &rawSource)
	delete(rawSource["sheet"].(map[string]any), "minX")
	write(from, rawSource)
	if err := run(out); err == nil {
		t.Fatal("missing source zero coordinate accepted")
	}
}
