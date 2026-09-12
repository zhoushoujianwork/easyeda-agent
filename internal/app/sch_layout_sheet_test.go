package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

func sheetFixture(t *testing.T) SchematicRenderInput {
	in := renderFixture(t)
	f, e := sheetPreviewFrame(in.Zones[0])
	if e != nil {
		t.Fatal(e)
	}
	w, h := f.Rect.MaxX-f.Rect.MinX, f.Rect.MaxY-f.Rect.MinY
	in.Sheet = &SchematicRenderSheet{Bounds: SchematicBox{0, 0, w + 100, h + 100}, Border: SchematicBox{0, 0, w + 100, h + 100}, Keepouts: []SchematicBox{}, Padding: 30, Gap: 20}
	return in
}

func TestSheetPreviewPreservesGeometryAndBlockedStatus(t *testing.T) {
	in := sheetFixture(t)
	for i := 1; i < 3; i++ {
		raw, _ := json.Marshal(in.Zones[0])
		var z SchematicRenderZone
		json.Unmarshal(raw, &z)
		z.ID = fmt.Sprintf("zone%d", i)
		for j := range z.Layout.Placements {
			z.Layout.Placements[j].Designator += fmt.Sprint(i)
		}
		z.Status = "blocked"
		in.Zones = append(in.Zones, z)
	}
	before, _ := json.Marshal(in)
	p, e := PlanSchematicSheets(in)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Pages) != 3 || len(p.BlockedZones) != 2 || !p.PreviewOnly {
		t.Fatalf("bad result %+v", p)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("input mutated")
	}
	for _, page := range p.Pages {
		if e := validateSchematicSheet(page); e != nil {
			t.Fatal(e)
		}
		svg, e := RenderSchematicLayoutSVG(page)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Contains(svg, []byte("排版边界")) {
			t.Fatal("no page guides")
		}
		z := page.Zones[0]
		var source SchematicRenderZone
		for _, s := range in.Zones {
			if s.ID == z.ID {
				source = s
			}
		}
		a, _ := json.Marshal(source.Layout)
		b, _ := json.Marshal(z.Layout)
		if !bytes.Equal(a, b) {
			t.Fatal("changed internal geometry")
		}
	}
	q, e := PlanSchematicSheets(in)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(p)
	b, _ := json.Marshal(q)
	if !bytes.Equal(a, b) {
		t.Fatal("not deterministic")
	}
}

func TestSheetPreviewRejectsOverlapPaddingAndKeepouts(t *testing.T) {
	p, e := PlanSchematicSheets(sheetFixture(t))
	if e != nil {
		t.Fatal(e)
	}
	page := p.Pages[0]
	bad := page
	bad.Zones = append(append([]SchematicRenderZone{}, page.Zones...), page.Zones[0])
	bad.Zones[1].ID = "other"
	if validateSchematicSheet(bad) == nil {
		t.Fatal("accepted overlapping frames")
	}
	bad = page
	bad.Zones = append([]SchematicRenderZone{}, page.Zones...)
	bad.Zones[0].SheetPosition = &SchematicSheetPosition{X: 0, Y: 0}
	if _, e := RenderSchematicLayoutSVG(bad); e == nil {
		t.Fatal("accepted insufficient padding")
	}
	bad = page
	s := *page.Sheet
	bad.Sheet = &s
	r, _ := sheetPreviewRect(page.Zones[0])
	s.Keepouts = []SchematicBox{r}
	if _, e := RenderSchematicLayoutSVG(bad); e == nil {
		t.Fatal("accepted keepout collision")
	}
	tooSmall := sheetFixture(t)
	tooSmall.Sheet.Padding = 10000
	if _, e := PlanSchematicSheets(tooSmall); e == nil {
		t.Fatal("accepted impossible sheet")
	}
}
