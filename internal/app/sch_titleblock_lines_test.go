package app

import (
	"strings"
	"testing"
)

// A structural fixture following the official 4.1.105 whole-page export: fill
// rects, per-cell text and drawn strokes share the table-<table>-<cell> id
// prefix. Two rows of the A4 template (绘制 / 审阅) are kept; values anonymized.
const titleTableSVGFixture = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-20 -845 1211 866">
<g class="shapeBox"><g c_partid="sheet">
<g><rect style="stroke-width:1;stroke:#A00000;vector-effect:none;fill:none;" width="1170" height="825" x="0" y="-825" rx="0" ry="0" id="o"/></g>
<g><rect style="stroke-width:1;stroke:#A00000;vector-effect:none;fill:none;" width="1150" height="805" x="10" y="-815" rx="0" ry="0" id="i"/></g>
<g c_partid="table" id="t-1"><g id="table-fill-container">
<rect id="table-t-a-fill-rect" x="460" y="-130" width="100" height="20" style="stroke:none;fill:none;"/>
<rect id="table-t-b-fill-rect" x="560" y="-130" width="115" height="20" style="stroke:none;fill:none;"/>
<rect id="table-t-c-fill-rect" x="460" y="-110" width="100" height="20" style="stroke:none;fill:none;"/>
<rect id="table-t-d-fill-rect" x="560" y="-110" width="115" height="20" style="stroke:none;fill:none;"/>
</g><g id="table-text-container">
<text id="table-t-a-text" x="465" y="-120" transform="rotate(0 510 -120)" clip-path="rect(0 100 20 0)"><tspan x="465" dy="0">绘制</tspan></text>
<text id="table-t-c-text" x="465" y="-100" transform="rotate(0 510 -100)" clip-path="rect(0 100 20 0)"><tspan x="465" dy="0">审阅</tspan></text>
</g><g id="table-stroke-container">
<line id="table-t-a-line-0" class="table-cell-stroke" x1="460" y1="-130" x2="675" y2="-130" style="stroke-width:1px;stroke-dasharray:0;stroke:#A00000;stroke-linecap:square;vector-effect:none;"/>
<line id="table-t-a-line-1" class="table-cell-stroke" x1="460" y1="-110" x2="675" y2="-110" style="stroke-width:1px;stroke-dasharray:0;stroke:#A00000;"/>
<line id="table-t-a-line-2" class="table-cell-stroke" x1="460" y1="-90" x2="675" y2="-90" style="stroke-width:1px;stroke:#A00000;"/>
<line id="table-t-a-line-3" class="table-cell-stroke" x1="560" y1="-130" x2="560" y2="-90" style="stroke-width:1px;stroke:#A00000;"/>
</g></g>
</g></g></svg>`

func TestSheetSVGTitleCellsAndStrokes(t *testing.T) {
	g, err := parseSheetGeometrySVG([]byte(titleTableSVGFixture))
	if err != nil {
		t.Fatal(err)
	}
	cells := g.TitleBlock.Cells
	if len(cells) != 4 {
		t.Fatalf("cells: %+v", cells)
	}
	type cellWant struct {
		text, left string
		minY, maxY float64 // y-UP
	}
	want := map[string]cellWant{
		"table-t-a": {"绘制", "", 110, 130},
		"table-t-b": {"", "绘制", 110, 130},
		"table-t-c": {"审阅", "", 90, 110},
		"table-t-d": {"", "审阅", 90, 110},
	}
	for _, c := range cells {
		w, ok := want[c.ID]
		if !ok {
			t.Fatalf("unexpected cell %q", c.ID)
		}
		if c.Text != w.text || c.LeftText != w.left || c.BBox.MinY != w.minY || c.BBox.MaxY != w.maxY {
			t.Fatalf("cell %s = %+v, want %+v", c.ID, c, w)
		}
	}
	if len(g.TitleBlock.Lines) != 4 || g.TitleBlock.Lines[1] != (titleBlockLine{X1: 460, Y1: 110, X2: 675, Y2: 110}) {
		t.Fatalf("lines: %+v", g.TitleBlock.Lines)
	}
	if *g.TitleBlock.BBox != (layoutBBox{MinX: 460, MinY: 90, MaxX: 675, MaxY: 130}) {
		t.Fatalf("table: %+v", g.TitleBlock.BBox)
	}
}

func TestSheetSVGRejectsMovedOrHiddenStrokes(t *testing.T) {
	cases := map[string]string{
		"stroke transform":    strings.Replace(titleTableSVGFixture, `id="table-t-a-line-1"`, `id="table-t-a-line-1" transform="translate(0,5)"`, 1),
		"stroke hidden":       strings.Replace(titleTableSVGFixture, `id="table-t-a-line-1"`, `id="table-t-a-line-1" visibility="hidden"`, 1),
		"stroke css display":  strings.Replace(titleTableSVGFixture, `stroke-dasharray:0;stroke:#A00000;"`, `display:none;"`, 1),
		"container transform": strings.Replace(titleTableSVGFixture, `id="table-stroke-container"`, `id="table-stroke-container" transform="scale(2)"`, 1),
		"stroke nonfinite":    strings.Replace(titleTableSVGFixture, `y1="-110"`, `y1="NaN"`, 1),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSheetGeometrySVG([]byte(data)); err == nil {
				t.Fatal("accepted a stroke whose drawn position is not its attributes")
			}
		})
	}
}

func TestTitleBlockTextLineFindings(t *testing.T) {
	g, err := parseSheetGeometrySVG([]byte(titleTableSVGFixture))
	if err != nil {
		t.Fatal(err)
	}
	item := func(id string, b layoutBBox) visibleTextItem {
		return visibleTextItem{ID: id, Kind: "attribute", ComponentType: "sheet", Key: "Name", BBox: &b}
	}
	items := []visibleTextItem{
		item("straddle", layoutBBox{MinX: 570, MinY: 101, MaxX: 640, MaxY: 115}), // issue #324: Name across 绘制/审阅
		item("in-cell", layoutBBox{MinX: 570, MinY: 94, MaxX: 640, MaxY: 106}),   // centered in the 审阅 value cell
		item("grazing", layoutBBox{MinX: 570, MinY: 96, MaxX: 640, MaxY: 110.2}), // touches the line within eps
		item("vertical", layoutBBox{MinX: 540, MinY: 113, MaxX: 590, MaxY: 127}), // across the label/value column line
		item("outside", layoutBBox{MinX: 100, MinY: 101, MaxX: 170, MaxY: 115}),  // not in the table
		{ID: "unmeasured", Kind: "text", Content: "x"},                           // no bbox: never judged here
	}
	got := titleBlockTextLineFindings(items, g, 0.5)
	if len(got) != 2 || got[0].PrimitiveId != "straddle" || got[1].PrimitiveId != "vertical" {
		t.Fatalf("findings: %+v", got)
	}
	f := got[0]
	if f.Type != "titleblock-text-on-line" || f.Level != "warn" || f.At == nil || f.At.Y != 110 || f.BBox == nil {
		t.Fatalf("not locatable: %+v", f)
	}
	for _, want := range []string{"sheet 的可见属性 Name", "y=110.00 横线", "绘制右格", "审阅右格", "sheet-geometry --exact"} {
		if !strings.Contains(f.Message, want) {
			t.Fatalf("message lacks %q: %s", want, f.Message)
		}
	}
	if got[1].At.X != 560 || !strings.Contains(got[1].Message, "x=560.00 竖线") {
		t.Fatalf("vertical: %+v", got[1])
	}
	// No strokes → the pure rule claims nothing (the live wrapper reports unverified).
	g.TitleBlock.Lines = nil
	if got := titleBlockTextLineFindings(items, g, 0.5); len(got) != 0 {
		t.Fatalf("judged without strokes: %+v", got)
	}
}

func TestTextsReachingTitleBlockGatesExport(t *testing.T) {
	tb := &layoutBBox{MinX: 468, MinY: 0, MaxX: 1170, MaxY: 198}
	b := func(minX, minY, maxX, maxY float64) *layoutBBox {
		return &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
	}
	items := []visibleTextItem{
		{ID: "in", BBox: b(570, 101, 640, 115)},
		{ID: "near", BBox: b(440, 101, 455, 115)}, // within the 20 margin of the estimate
		{ID: "far", BBox: b(100, 500, 170, 515)},
		{ID: "nobox"},
	}
	got := textsReachingTitleBlock(items, tb, 20)
	if len(got) != 2 || got[0].ID != "in" || got[1].ID != "near" {
		t.Fatalf("gate: %+v", got)
	}
	if textsReachingTitleBlock(items, nil, 20) != nil {
		t.Fatal("hidden/absent title block must not trigger an export")
	}
}
