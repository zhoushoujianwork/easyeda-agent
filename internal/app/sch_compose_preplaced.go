package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// A selected page is a geometry authority, not another request to pack modules.
// The existing canonical checks and guarded writer remain shared with compose.
func validateSchCompositionPreplaced(src schCompositionSource, page SchematicRenderInput) error {
	if page.SchemaVersion != 1 || page.Diagnostic || page.Sheet == nil || page.Spacing == nil || page.Sheet.Flow != "z" {
		return fmt.Errorf("layout-page requires a complete non-diagnostic single page with explicit spacing and Z flow")
	}
	if src.SheetBorder == nil || src.Keepouts == nil || src.Sheet != page.Sheet.Bounds || *src.SheetBorder != page.Sheet.Border || !reflect.DeepEqual(src.Keepouts, page.Sheet.Keepouts) {
		return fmt.Errorf("layout-page sheet, border and keepouts must exactly match composition evidence")
	}
	if len(src.Modules) != len(page.Zones) || len(src.Connectivity.Modules) != len(page.Zones) {
		return fmt.Errorf("layout-page must cover every composition module exactly once")
	}
	if err := validateCompleteLayoutPreview(page); err != nil {
		return err
	}
	if _, err := RenderSchematicLayoutSVG(page); err != nil {
		return err
	}
	components := map[string]connectivity.Component{}
	modules := map[string]connectivity.Module{}
	for _, c := range src.Connectivity.Components {
		components[c.ID] = c
	}
	for _, m := range src.Connectivity.Modules {
		modules[m.ID] = m
	}
	for i, z := range page.Zones {
		m, canonical := src.Modules[i], modules[z.ID]
		if z.Layout == nil || z.Frame == nil || z.ContentBounds == nil || z.SheetPosition == nil || z.CoreComponentID == "" || len(z.Variants) != 0 || len(z.Layout.Variants) != 0 {
			return fmt.Errorf("zone %s requires selected complete geometry, frame, bounds, core and position without variants", z.ID)
		}
		if z.ID != m.ID || z.Title != m.Title || canonical.ID != z.ID || len(m.Terminals) != 0 {
			return fmt.Errorf("zone %s differs from ordered module identity/title or has unresolved terminals", z.ID)
		}
		core := false
		members := map[string]bool{}
		for _, id := range canonical.CoreComponents {
			core = core || id == z.CoreComponentID
			members[id] = true
		}
		for _, id := range canonical.PeripheralComponents {
			members[id] = true
		}
		if !core || len(members) != len(z.Layout.Placements) {
			return fmt.Errorf("zone %s core/member set differs from canonical module", z.ID)
		}
		if !reflect.DeepEqual(m.Placements, z.Layout.Placements) || !reflect.DeepEqual(m.Wires, z.Layout.Wires) || !reflect.DeepEqual(m.Flags, z.Layout.Flags) {
			return fmt.Errorf("zone %s local placements/wires/flags differ from composition", z.ID)
		}
		for _, p := range z.Layout.Placements {
			id := z.Layout.ComponentIDs[p.Designator]
			c, exists := components[id]
			if !exists || !members[id] || c.Ref != p.Designator {
				return fmt.Errorf("zone %s component %s canonical identity differs", z.ID, p.Designator)
			}
			pins := map[string]connectivity.Pin{}
			for _, pin := range c.Pins {
				pins[pin.Number] = pin
			}
			for _, pin := range p.Pins {
				cp, exists := pins[pin.Number]
				state := ""
				if cp.NoConnected {
					state = "nc"
				} else if cp.ConnectionState == "unconnected" {
					state = "unconnected"
				}
				if !exists || cp.Name != pin.Name || z.Layout.PinStates[id][pin.Number] != state {
					return fmt.Errorf("zone %s pin %s.%s name/NC/open state differs from canonical", z.ID, p.Designator, pin.Number)
				}
			}
		}
		if err := validateSchematicVariantGeometry(page, z, z.Layout, *z.Frame, *z.ContentBounds); err != nil {
			return fmt.Errorf("zone %s selected geometry: %w", z.ID, err)
		}
		f := z.Frame
		p := powerLayoutPlan{Placements: m.Placements, Wires: m.Wires, Flags: m.Flags}
		if f.FontSize != schModuleTitleFontSize || f.Color != "#AA00AA" || f.LineType != 1 || f.TitleLayout == nil || !reflect.DeepEqual(f.TitleLayout.Obstacles, powerLayoutContentObstacles(&p)) {
			return fmt.Errorf("zone %s frame style/title occupancy must preserve complete selected geometry", z.ID)
		}
		if m.TitleMetrics != nil && (m.TitleMetrics.Title != f.Title || m.TitleMetrics.FontSize != f.FontSize || plCeil(m.TitleMetrics.Width) != f.TitleLayout.Width || plCeil(m.TitleMetrics.Height) != f.TitleLayout.Height) {
			return fmt.Errorf("zone %s title metrics differ from selected frame", z.ID)
		}
	}
	return nil
}

func schCompositionPreplacedRows(page SchematicRenderInput) []schModuleRowPlacement {
	rows := make([]schModuleRowPlacement, 0, len(page.Zones))
	row := 0
	for i, z := range page.Zones {
		if i > 0 && z.SheetPosition.Y != page.Zones[i-1].SheetPosition.Y {
			row++
		}
		dx, dy := z.SheetPosition.X-z.Frame.Rect.MinX, z.SheetPosition.Y-z.Frame.Rect.MaxY
		rows = append(rows, schModuleRowPlacement{Frame: translateSchFrame(*z.Frame, dx, dy), Row: row, DX: dx, DY: dy})
	}
	return rows
}

func decodeSchCompositionLayoutPage(raw []byte) (*SchematicRenderInput, error) {
	var page SchematicRenderInput
	if err := connectivity.DecodeStrictDesignJSON(raw, &page); err != nil {
		return nil, err
	}
	if err := validateRenderSheetJSON(raw); err != nil {
		return nil, err
	}
	if err := validateRenderMeasurementsJSON(raw); err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	if err := schPreplacedRequire(top, "sheet", "spacing", "zones"); err != nil {
		return nil, err
	}
	var zones []map[string]json.RawMessage
	if err := json.Unmarshal(top["zones"], &zones); err != nil {
		return nil, err
	}
	for _, z := range zones {
		if _, exists := z["variants"]; exists {
			return nil, fmt.Errorf("layout-page accepts a selected page only, not variants")
		}
		if err := schPreplacedRequire(z, "frame", "contentBounds", "sheetPosition", "coreComponentId"); err != nil {
			return nil, err
		}
		var f map[string]json.RawMessage
		if err := json.Unmarshal(z["frame"], &f); err != nil {
			return nil, err
		}
		if err := schPreplacedRequire(f, "id", "title", "rect", "titleX", "titleY", "fontSize", "color", "lineType", "titleLayout"); err != nil {
			return nil, err
		}
		for _, box := range []json.RawMessage{f["rect"], z["contentBounds"]} {
			if err := schPreplacedRequireBox(box); err != nil {
				return nil, err
			}
		}
		var title map[string]json.RawMessage
		if err := json.Unmarshal(f["titleLayout"], &title); err != nil {
			return nil, err
		}
		if err := schPreplacedRequire(title, "width", "height", "clearance", "obstacles"); err != nil {
			return nil, err
		}
		var obstacles []json.RawMessage
		if err := json.Unmarshal(title["obstacles"], &obstacles); err != nil {
			return nil, err
		}
		for _, box := range obstacles {
			if err := schPreplacedRequireBox(box); err != nil {
				return nil, err
			}
		}
	}
	return &page, nil
}

func validateSchCompositionPreplacedSourceJSON(raw []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	if err := schPreplacedRequire(top, "sheet", "sheetBorder", "keepouts", "modules"); err != nil {
		return err
	}
	for _, key := range []string{"sheet", "sheetBorder"} {
		if err := schPreplacedRequireBox(top[key]); err != nil {
			return err
		}
	}
	var keepouts []json.RawMessage
	if err := json.Unmarshal(top["keepouts"], &keepouts); err != nil {
		return err
	}
	for _, box := range keepouts {
		if err := schPreplacedRequireBox(box); err != nil {
			return err
		}
	}
	var modules []json.RawMessage
	if err := json.Unmarshal(top["modules"], &modules); err != nil {
		return err
	}
	zones := make([]map[string]json.RawMessage, 0, len(modules))
	for _, m := range modules {
		zones = append(zones, map[string]json.RawMessage{"layout": m})
	}
	measurements, _ := json.Marshal(map[string]any{"zones": zones})
	return validateRenderMeasurementsJSON(measurements)
}

func schPreplacedRequire(m map[string]json.RawMessage, keys ...string) error {
	for _, key := range keys {
		if len(m[key]) == 0 || string(m[key]) == "null" {
			return fmt.Errorf("layout-page requires explicit %s", key)
		}
	}
	return nil
}

func schPreplacedRequireBox(raw json.RawMessage) error {
	var box map[string]json.RawMessage
	if err := json.Unmarshal(raw, &box); err != nil {
		return err
	}
	return schPreplacedRequire(box, "minX", "minY", "maxX", "maxY")
}

func validateSchCompositionOutputPaths(inputs, outputs []string) error {
	protected := append([]string(nil), inputs...)
	for _, out := range outputs {
		if out == "" {
			continue
		}
		a, err := filepath.Abs(out)
		if err != nil {
			return err
		}
		fo, _ := os.Stat(out)
		for _, in := range protected {
			if in == "" {
				continue
			}
			b, err := filepath.Abs(in)
			if err != nil {
				return err
			}
			fi, _ := os.Stat(in)
			if a == b || (fi != nil && fo != nil && os.SameFile(fi, fo)) {
				return fmt.Errorf("composition outputs must not overwrite inputs or each other")
			}
		}
		protected = append(protected, out)
	}
	return nil
}
