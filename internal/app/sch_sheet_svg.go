package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// This deliberately recognizes only the rectangular structure observed in the
// official whole-page export. It is not a general SVG renderer. Reject unknown
// geometry rather than reconstructing sheet dimensions from the viewport.
type sheetSVGNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr     `xml:",any,attr"`
	Children []sheetSVGNode `xml:",any"`
	Text     string         `xml:",chardata"`
}

func (n sheetSVGNode) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func sheetSVGUnsupported(format string, args ...any) error {
	return fmt.Errorf("unsupported sheet SVG: "+format, args...)
}

func sheetSVGRect(n sheetSVGNode) (layoutBBox, error) {
	values := make([]float64, 4)
	for i, key := range []string{"x", "y", "width", "height"} {
		v, err := strconv.ParseFloat(n.attr(key), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return layoutBBox{}, sheetSVGUnsupported("rect %q has invalid %s", n.attr("id"), key)
		}
		values[i] = v
	}
	if values[2] <= 0 || values[3] <= 0 {
		return layoutBBox{}, sheetSVGUnsupported("non-positive rectangle")
	}
	for _, key := range []string{"rx", "ry"} {
		if v := n.attr(key); v != "" && v != "0" {
			return layoutBBox{}, sheetSVGUnsupported("rounded rectangle")
		}
	}
	b := layoutBBox{MinX: values[0], MaxX: values[0] + values[2], MinY: -values[1] - values[3], MaxY: -values[1]}
	if math.IsInf(b.MaxX, 0) || math.IsInf(b.MinY, 0) {
		return layoutBBox{}, sheetSVGUnsupported("rectangle overflow")
	}
	return b, nil
}

func sheetSVGContains(a, b layoutBBox) bool {
	return a.MinX <= b.MinX && a.MinY <= b.MinY && a.MaxX >= b.MaxX && a.MaxY >= b.MaxY
}

// Check the ancestors of selected geometry too. Unsupported transforms or
// visibility must not silently turn local coordinates into purported measurements.
func sheetSVGUnsafe(n sheetSVGNode) bool {
	for _, key := range []string{"transform", "display", "visibility", "opacity", "clip-path", "mask", "filter", "class"} {
		if n.attr(key) != "" {
			return true
		}
	}
	style := strings.ToLower(n.attr("style"))
	for _, declaration := range strings.Split(style, ";") {
		if strings.TrimSpace(declaration) == "" {
			continue
		}
		key, _, ok := strings.Cut(declaration, ":")
		if !ok {
			return true
		}
		switch strings.TrimSpace(key) {
		case "fill", "stroke", "stroke-width", "stroke-linecap", "stroke-linejoin", "vector-effect":
			// Paint does not change the rectangle centerline coordinates.
		default:
			// SVG2 CSS x/y/width/height/rx/ry override presentation attributes.
			// Unknown properties are unsupported, not assumed harmless.
			return true
		}
	}
	return false
}

func parseSheetGeometrySVG(data []byte) (sheetGeometry, error) {
	var root sheetSVGNode
	d := xml.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(&root); err != nil {
		return sheetGeometry{}, err
	}
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return sheetGeometry{}, err
		}
		if c, ok := t.(xml.CharData); ok && strings.TrimSpace(string(c)) == "" {
			continue
		}
		return sheetGeometry{}, sheetSVGUnsupported("trailing XML content")
	}
	if root.XMLName.Local != "svg" {
		return sheetGeometry{}, sheetSVGUnsupported("root must be svg")
	}
	var borders []layoutBBox
	var cells []titleBlockCell
	var lines []titleBlockLine
	texts := map[string]string{}
	sheets, tables, containers := 0, 0, 0
	tableDepth, cellsDepth := -1, -1
	var walk func(sheetSVGNode, bool, bool, string, bool, int) error
	// region names the table child container being walked: "cells"
	// (table-fill-container), "texts" or "strokes"; "" elsewhere.
	walk = func(n sheetSVGNode, inSheet, inTable bool, region string, unsafe bool, depth int) error {
		// Official shapeBox is a structural class with no geometry styling.
		if n.XMLName.Local == "style" {
			css := strings.Join(strings.Fields(n.Text), "")
			if css != "" && css != "*{stroke-linejoin:round;stroke-linecap:round}" {
				return sheetSVGUnsupported("unrecognized stylesheet")
			}
		}
		if n.XMLName.Local == "g" && n.attr("class") == "shapeBox" {
			for i := range n.Attrs {
				if n.Attrs[i].Name.Local == "class" {
					n.Attrs[i].Value = ""
				}
			}
		}
		parentUnsafe := unsafe
		unsafe = unsafe || sheetSVGUnsafe(n)
		// Only root svg → g ancestors are supported for measured rectangles.
		// defs/symbol are not rendered, and a nested svg establishes a viewport.
		if depth > 0 && n.XMLName.Local != "g" && n.XMLName.Local != "rect" {
			unsafe = true
		}
		if n.XMLName.Space != "" && n.XMLName.Space != "http://www.w3.org/2000/svg" {
			unsafe = true
		}
		if n.attr("c_partid") == "sheet" {
			sheets++
			inSheet = true
		}
		if inSheet && n.attr("c_partid") == "table" {
			tables++
			inTable = true
			tableDepth = depth
		}
		if inTable && region == "" && n.XMLName.Local == "g" && depth == tableDepth+1 {
			switch n.attr("id") {
			case "table-text-container":
				region = "texts"
			case "table-stroke-container":
				region = "strokes"
			}
		}
		if inTable && n.attr("id") == "table-fill-container" {
			if n.XMLName.Local != "g" || depth != tableDepth+1 {
				return sheetSVGUnsupported("title cell container must belong directly to table")
			}
			containers++
			region = "cells"
			cellsDepth = depth
		}
		// Cell labels are reading aids, not geometry claims: keep the rendered
		// text of each cell keyed by the shared table-<table>-<cell> id prefix.
		if region == "texts" && n.XMLName.Local == "text" {
			if key, ok := strings.CutSuffix(n.attr("id"), "-text"); ok && key != "" {
				texts[key] = strings.TrimSpace(sheetSVGText(n))
			}
			return nil
		}
		// Drawn table strokes are the lines a title-block text must not sit on.
		// Their own class/stroke styling is paint; anything that could move or
		// hide them (ancestor or own transform/visibility) fails closed.
		if region == "strokes" && n.XMLName.Local == "line" {
			if parentUnsafe || sheetSVGLineUnsafe(n) {
				return sheetSVGUnsupported("transformed or hidden title-table stroke")
			}
			l, err := sheetSVGLine(n)
			if err != nil {
				return err
			}
			lines = append(lines, l)
			return nil
		}
		if inSheet && n.XMLName.Local == "rect" && (!inTable || region == "cells") {
			if unsafe {
				return sheetSVGUnsupported("transformed, hidden or styled geometry")
			}
			b, err := sheetSVGRect(n)
			if err != nil {
				return err
			}
			if region == "cells" {
				if depth != cellsDepth+1 {
					return sheetSVGUnsupported("cell rectangle must belong directly to cell container")
				}
				cells = append(cells, titleBlockCell{ID: strings.TrimSuffix(n.attr("id"), "-fill-rect"), BBox: b})
			} else {
				borders = append(borders, b)
			}
		}
		for _, child := range n.Children {
			if err := walk(child, inSheet, inTable, region, unsafe, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, false, false, "", false, 0); err != nil {
		return sheetGeometry{}, err
	}
	if sheets != 1 || len(borders) != 2 || tables != 1 || containers != 1 || len(cells) == 0 {
		return sheetGeometry{}, sheetSVGUnsupported("need one sheet, two border rectangles, one title table and one cell container (got %d/%d/%d/%d/%d cells)", sheets, len(borders), tables, containers, len(cells))
	}
	outer, inner := borders[0], borders[1]
	if sheetSVGContains(inner, outer) {
		outer, inner = inner, outer
	}
	if !(outer.MinX < inner.MinX && outer.MinY < inner.MinY && outer.MaxX > inner.MaxX && outer.MaxY > inner.MaxY) {
		return sheetGeometry{}, sheetSVGUnsupported("border rectangles are not strictly nested")
	}
	table := cells[0].BBox
	for _, c := range cells[1:] {
		b := c.BBox
		table.MinX = math.Min(table.MinX, b.MinX)
		table.MinY = math.Min(table.MinY, b.MinY)
		table.MaxX = math.Max(table.MaxX, b.MaxX)
		table.MaxY = math.Max(table.MaxY, b.MaxY)
	}
	if !sheetSVGContains(inner, table) {
		return sheetGeometry{}, sheetSVGUnsupported("title table outside inner border")
	}
	for i := range cells {
		if cells[i].ID != "" {
			cells[i].Text = texts[cells[i].ID]
		}
	}
	for i := range cells {
		cells[i].LeftText = leftCellText(cells, i)
	}
	warnings := []string{"offline export: verify page identity and freshness against the same-batch object readback; add stroke clearance in layout parameters"}
	if len(lines) == 0 {
		warnings = append(warnings, "no title-table stroke lines in this export; text-on-line checks cannot run against it")
	}
	visible := true
	return sheetGeometry{
		SourceSha256: fmt.Sprintf("%x", sha256.Sum256(data)), CoordinateSystem: "schematic-raw-y-up-vector-centerlines",
		Sheet:      sheetInfo{BBox: &outer, InnerBBox: &inner},
		TitleBlock: titleBlockInfo{Visible: &visible, BBox: &table, Source: "official-svg-vectors", Cells: cells, Lines: lines},
		Keepouts:   []keepout{{Name: "titleBlock", BBox: &table, Hard: true}},
		Warnings:   warnings,
	}, nil
}

// titleBlockCell is one measured title-table cell from the official export.
// Text is what the exporter rendered inside it ("" for an empty value cell);
// LeftText is the text of the cell immediately to its left, which names a
// value cell by its row label (e.g. the cell right of 审阅).
type titleBlockCell struct {
	ID       string     `json:"id,omitempty"`
	BBox     layoutBBox `json:"bbox"`
	Text     string     `json:"text"`
	LeftText string     `json:"leftText,omitempty"`
}

// titleBlockLine is one drawn title-table stroke (centerline, raw y-UP).
type titleBlockLine struct {
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
	X2 float64 `json:"x2"`
	Y2 float64 `json:"y2"`
}

func leftCellText(cells []titleBlockCell, i int) string {
	const tol = 0.01
	c := cells[i].BBox
	best, bestOverlap := "", 0.0
	for j, o := range cells {
		if j == i || math.Abs(o.BBox.MaxX-c.MinX) > tol {
			continue
		}
		if ov := math.Min(o.BBox.MaxY, c.MaxY) - math.Max(o.BBox.MinY, c.MinY); ov > bestOverlap {
			best, bestOverlap = o.Text, ov
		}
	}
	return best
}

func sheetSVGText(n sheetSVGNode) string {
	var b strings.Builder
	b.WriteString(n.Text)
	for _, c := range n.Children {
		b.WriteString(sheetSVGText(c))
	}
	return b.String()
}

func sheetSVGLineUnsafe(n sheetSVGNode) bool {
	for _, key := range []string{"transform", "display", "visibility", "opacity", "clip-path", "mask", "filter"} {
		if n.attr(key) != "" {
			return true
		}
	}
	for _, declaration := range strings.Split(strings.ToLower(n.attr("style")), ";") {
		key, value, _ := strings.Cut(declaration, ":")
		switch strings.TrimSpace(key) {
		case "display", "visibility", "transform":
			return true
		case "opacity", "stroke-opacity":
			if v := strings.TrimSpace(value); v == "0" {
				return true
			}
		}
	}
	return false
}

func sheetSVGLine(n sheetSVGNode) (titleBlockLine, error) {
	v := make([]float64, 4)
	for i, key := range []string{"x1", "y1", "x2", "y2"} {
		f, err := strconv.ParseFloat(n.attr(key), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return titleBlockLine{}, sheetSVGUnsupported("table stroke %q has invalid %s", n.attr("id"), key)
		}
		v[i] = f
	}
	return titleBlockLine{X1: v[0], Y1: -v[1], X2: v[2], Y2: -v[3]}, nil
}

func runSheetGeometrySVG(path string, asJSON bool, stdout io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	const maxBytes = 16 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxBytes {
		return sheetSVGUnsupported("input exceeds 16 MiB")
	}
	g, err := parseSheetGeometrySVG(data)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(stdout).Encode(map[string]any{"ok": true, "result": g})
	}
	renderSheetGeometry(g, stdout)
	return nil
}
