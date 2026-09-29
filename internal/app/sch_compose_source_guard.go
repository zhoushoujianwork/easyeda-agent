package app

import (
	"fmt"
)

// A replacement clears more than parts. The source snapshot must enumerate the
// same primitive classes as page.clear and carry the native state of each item.
// The scene comparison protects geometry/network values; this inventory also
// binds every deletable object's identity before the first write.
func schComposeProtectedPage(result map[string]any) (map[string]any, error) {
	if result["wiresAvailable"] != true || (result["pinNetsAvailable"] != true && !schComposeZeroDrawing(result)) {
		return nil, fmt.Errorf("before snapshot lacks complete wire or pin-net evidence")
	}
	page, ok := result["pagePrimitives"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("before snapshot lacks pagePrimitives; recapture sch list --include-page-primitives")
	}
	fields := map[string][]string{
		"components": {"componentType"},
		"wires":      {"Line", "Net", "Color", "LineWidth", "LineType"},
		"buses":      {"BusName", "Line", "Color", "LineWidth", "LineType"},
		"arcs":       {"StartX", "StartY", "ReferenceX", "ReferenceY", "EndX", "EndY", "Color", "FillColor", "LineWidth", "LineType"},
		"circles":    {"CenterX", "CenterY", "Radius", "Color", "FillColor", "LineWidth", "LineType", "FillStyle"},
		"rectangles": {"TopLeftX", "TopLeftY", "Width", "Height", "CornerRadius", "Rotation", "Color", "FillColor", "LineWidth", "LineType", "FillStyle"},
		"polygons":   {"Line", "Color", "FillColor", "LineWidth", "LineType"},
		"texts":      {"X", "Y", "Content", "Rotation", "TextColor", "FontName", "FontSize", "Bold", "Italic", "UnderLine", "AlignMode"},
		"attributes": {"X", "Y", "Rotation", "Color", "FontName", "FontSize", "Bold", "Italic", "UnderLine", "AlignMode", "FillColor", "Key", "Value", "KeyVisible", "ValueVisible", "ParentPrimitiveId"},
		"objects":    {"Content", "StartX", "StartY", "Width", "Height", "Rotation", "Mirror", "FileName"},
	}
	if len(page) != len(fields) {
		return nil, fmt.Errorf("before snapshot pagePrimitives has incomplete class inventory")
	}
	sets := map[string]map[string]bool{}
	for kind, required := range fields {
		rows, ok := page[kind].([]any)
		if !ok {
			return nil, fmt.Errorf("before snapshot pagePrimitives.%s unavailable", kind)
		}
		ids := map[string]bool{}
		for _, item := range rows {
			row, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("before snapshot malformed %s primitive", kind)
			}
			id, ok := row["primitiveId"].(string)
			if !ok || id == "" || ids[id] {
				return nil, fmt.Errorf("before snapshot missing/duplicate %s primitiveId", kind)
			}
			ids[id] = true
			for _, field := range required {
				if _, present := row[field]; !present {
					return nil, fmt.Errorf("before snapshot %s.%s unavailable", kind, field)
				}
			}
		}
		sets[kind] = ids
	}
	components, ok := result["components"].([]any)
	if !ok || len(components) != len(sets["components"]) {
		return nil, fmt.Errorf("before snapshot component and page primitive inventories differ")
	}
	componentCounts := map[string]int{}
	for _, item := range components {
		row, ok := item.(map[string]any)
		if !ok || !sets["components"][stringVal(row["primitiveId"])] {
			return nil, fmt.Errorf("before snapshot component identity differs from page inventory")
		}
		componentCounts[stringVal(row["componentType"])]++
	}
	wires, ok := result["wires"].([]any)
	if !ok {
		return nil, fmt.Errorf("before snapshot wire segment inventory unavailable")
	}
	seenWires := map[string]bool{}
	for _, item := range wires {
		row, ok := item.(map[string]any)
		if !ok || !sets["wires"][stringVal(row["primitiveId"])] {
			return nil, fmt.Errorf("before snapshot wire identity differs from page inventory")
		}
		seenWires[stringVal(row["primitiveId"])] = true
		for _, field := range []string{"x0", "y0", "x1", "y1"} {
			if n, ok := finiteFloat(row[field]); !ok || !finiteStateNumber(n) {
				return nil, fmt.Errorf("before snapshot wire %s unavailable", field)
			}
		}
		if _, ok := row["net"].(string); !ok {
			return nil, fmt.Errorf("before snapshot wire net unavailable")
		}
	}
	if len(seenWires) != len(sets["wires"]) {
		return nil, fmt.Errorf("before snapshot wire segments do not cover every raw wire")
	}
	summary, ok := result["connectivitySummary"].(map[string]any)
	if !ok || summary["scope"] != "activePage" {
		return nil, fmt.Errorf("before snapshot active-page connectivity summary unavailable")
	}
	for _, field := range []string{"wires", "buses", "netflags", "netports", "netlabels", "shortSymbols"} {
		if n, ok := finiteFloat(summary[field]); !ok || n < 0 || n != float64(int(n)) {
			return nil, fmt.Errorf("before snapshot connectivitySummary.%s unavailable", field)
		}
	}
	wiresCount, _ := finiteFloat(summary["wires"])
	busesCount, _ := finiteFloat(summary["buses"])
	if wiresCount != float64(len(sets["wires"])) || busesCount != float64(len(sets["buses"])) {
		return nil, fmt.Errorf("before snapshot connectivity summary differs from page primitive inventory")
	}
	for field, kind := range map[string]string{"netflags": "netflag", "netports": "netport", "netlabels": "netlabel", "shortSymbols": "short_symbol"} {
		count, _ := finiteFloat(summary[field])
		if count != float64(componentCounts[kind]) {
			return nil, fmt.Errorf("before snapshot connectivitySummary.%s differs from component inventory", field)
		}
	}
	return page, nil
}

// An empty native drawing cannot be exported as a netlist by some hosts. This
// proves only the absence of drawing objects, not the state of any pin; the
// caller must separately inspect every physical pin and its NC bit.
func schComposeZeroDrawing(result map[string]any) bool {
	summary, ok := result["connectivitySummary"].(map[string]any)
	if !ok || summary["scope"] != "activePage" {
		return false
	}
	for _, key := range []string{"wires", "buses", "netflags", "netports", "netlabels", "shortSymbols"} {
		if value, known := finiteFloat(summary[key]); !known || value != 0 {
			return false
		}
	}
	page, ok := result["pagePrimitives"].(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"wires", "buses", "arcs", "circles", "rectangles", "polygons", "texts", "objects"} {
		items, ok := page[key].([]any)
		if !ok || len(items) != 0 {
			return false
		}
	}
	return true
}

func schComposeOrdinaryClearable(page map[string]any) error {
	components := map[string]bool{}
	for _, item := range page["components"].([]any) {
		components[stringVal(item.(map[string]any)["primitiveId"])] = true
	}
	for _, item := range page["attributes"].([]any) {
		attribute := item.(map[string]any)
		if !components[stringVal(attribute["ParentPrimitiveId"])] {
			return fmt.Errorf("ordinary page clear cannot prove deletion of orphan attribute %s", stringVal(attribute["primitiveId"]))
		}
	}
	if len(page["objects"].([]any)) != 0 {
		return fmt.Errorf("ordinary page clear cannot prove deletion of embedded objects")
	}
	return nil
}
