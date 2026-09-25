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
	var borders, cells []layoutBBox
	sheets, tables, containers := 0, 0, 0
	tableDepth, cellsDepth := -1, -1
	var walk func(sheetSVGNode, bool, bool, bool, bool, int) error
	walk = func(n sheetSVGNode, inSheet, inTable, inCells, unsafe bool, depth int) error {
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
		if inTable && n.attr("id") == "table-fill-container" {
			if n.XMLName.Local != "g" || depth != tableDepth+1 {
				return sheetSVGUnsupported("title cell container must belong directly to table")
			}
			containers++
			inCells = true
			cellsDepth = depth
		}
		if inSheet && n.XMLName.Local == "rect" && (!inTable || inCells) {
			if unsafe {
				return sheetSVGUnsupported("transformed, hidden or styled geometry")
			}
			b, err := sheetSVGRect(n)
			if err != nil {
				return err
			}
			if inCells {
				if depth != cellsDepth+1 {
					return sheetSVGUnsupported("cell rectangle must belong directly to cell container")
				}
				cells = append(cells, b)
			} else {
				borders = append(borders, b)
			}
		}
		for _, child := range n.Children {
			if err := walk(child, inSheet, inTable, inCells, unsafe, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, false, false, false, false, 0); err != nil {
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
	table := cells[0]
	for _, b := range cells[1:] {
		table.MinX = math.Min(table.MinX, b.MinX)
		table.MinY = math.Min(table.MinY, b.MinY)
		table.MaxX = math.Max(table.MaxX, b.MaxX)
		table.MaxY = math.Max(table.MaxY, b.MaxY)
	}
	if !sheetSVGContains(inner, table) {
		return sheetGeometry{}, sheetSVGUnsupported("title table outside inner border")
	}
	visible := true
	return sheetGeometry{
		SourceSha256: fmt.Sprintf("%x", sha256.Sum256(data)), CoordinateSystem: "schematic-raw-y-up-vector-centerlines",
		Sheet:      sheetInfo{BBox: &outer, InnerBBox: &inner},
		TitleBlock: titleBlockInfo{Visible: &visible, BBox: &table, Source: "official-svg-vectors"},
		Keepouts:   []keepout{{Name: "titleBlock", BBox: &table, Hard: true}},
		Warnings:   []string{"offline export: verify page identity and freshness against the same-batch object readback; add stroke clearance in layout parameters"},
	}, nil
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
