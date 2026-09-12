package app

import (
	"fmt"
	"math"
	"sort"
)

type SchematicSheetPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type SchematicRenderSheet struct {
	Bounds       SchematicBox   `json:"bounds"`
	Border       SchematicBox   `json:"border"`
	Keepouts     []SchematicBox `json:"keepouts"`
	Padding      float64        `json:"padding"`
	Gap          float64        `json:"gap"`
	BorderSource string         `json:"borderSource,omitempty"`
	Flow         string         `json:"flow,omitempty"`
}
type SchematicSheetsPreview struct {
	SchemaVersion        int                          `json:"schemaVersion"`
	PreviewOnly          bool                         `json:"previewOnly"`
	PlacementMode        string                       `json:"placementMode"`
	Spacing              *float64                     `json:"spacing,omitempty"`
	ZoneCount            int                          `json:"zoneCount"`
	BlockedZones         []string                     `json:"blockedZones"`
	FrameArea            float64                      `json:"frameArea"`
	UsableAreaUpperBound float64                      `json:"usableAreaUpperBound"`
	Pages                []SchematicRenderInput       `json:"pages"`
	VariantSearch        *SchematicSheetVariantSearch `json:"variantSearch,omitempty"`
}

// The optional top-level spacing is the only authority in unified mode.
// Explicit legacy fields may confirm it, never override it. Copy the sheet so
// resolving omitted values cannot mutate a caller's geometry evidence.
func resolveSchematicRenderSpacing(in SchematicRenderInput) (SchematicRenderInput, error) {
	if err := validateSchematicSpacing(in.Spacing); err != nil {
		return in, err
	}
	if in.Spacing == nil || in.Sheet == nil {
		return in, nil
	}
	s := *in.Sheet
	for _, field := range []struct {
		name  string
		value float64
	}{{"padding", s.Padding}, {"gap", s.Gap}} {
		if field.value != 0 && field.value != *in.Spacing {
			return in, fmt.Errorf("sheet.%s must match unified spacing %g", field.name, *in.Spacing)
		}
	}
	s.Padding, s.Gap = *in.Spacing, *in.Spacing
	in.Sheet = &s
	return in, nil
}

func sheetPreviewUsable(s SchematicRenderSheet) SchematicBox {
	n := s.Padding + 0.5
	return SchematicBox{MinX: plCeil(s.Border.MinX + n), MinY: plCeil(s.Border.MinY + n), MaxX: plFloor(s.Border.MaxX - n), MaxY: plFloor(s.Border.MaxY - n)}
}
func sheetPreviewFrame(z SchematicRenderZone, spacingArg ...*float64) (schFrameSpec, error) {
	var spacing *float64
	if len(spacingArg) > 0 {
		spacing = spacingArg[0]
	}
	if err := validateSchematicSpacing(spacing); err != nil {
		return schFrameSpec{}, err
	}
	if z.Frame != nil {
		if spacing != nil {
			if z.Layout == nil {
				return schFrameSpec{}, fmt.Errorf("zone %s lacks layout", z.ID)
			}
			p := powerLayoutPlan{Placements: z.Layout.Placements, Wires: z.Layout.Wires, Flags: z.Layout.Flags}
			if err := validateSchematicFrameSpacing(*z.Frame, powerLayoutContentObstacles(&p), spacing); err != nil {
				return schFrameSpec{}, err
			}
		}
		return *z.Frame, nil
	}
	if z.Layout == nil {
		return schFrameSpec{}, fmt.Errorf("zone %s lacks layout", z.ID)
	}
	p := powerLayoutPlan{Placements: z.Layout.Placements, Wires: z.Layout.Wires, Flags: z.Layout.Flags}
	return measureSchModuleFrameObstaclesSpacing(z.ID, z.Title, powerLayoutContentObstacles(&p), nil, nil, spacing)
}
func sheetPreviewRect(z SchematicRenderZone, spacingArg ...*float64) (SchematicBox, error) {
	f, e := sheetPreviewFrame(z, spacingArg...)
	if e != nil {
		return SchematicBox{}, e
	}
	p := z.SheetPosition
	if p == nil || !plFinite(p.X) || !plFinite(p.Y) || p.X != plFloor(p.X) || p.Y != plFloor(p.Y) {
		return SchematicBox{}, fmt.Errorf("zone %s requires finite grid-aligned sheetPosition", z.ID)
	}
	return SchematicBox{MinX: p.X, MinY: p.Y - (f.Rect.MaxY - f.Rect.MinY), MaxX: p.X + (f.Rect.MaxX - f.Rect.MinX), MaxY: p.Y}, nil
}
func sheetPreviewConflict(a, b SchematicBox, gap float64) bool {
	return !(a.MaxX+gap <= b.MinX || b.MaxX+gap <= a.MinX || a.MaxY+gap <= b.MinY || b.MaxY+gap <= a.MinY)
}

func sheetPreviewPlacementError(id string, r, usable SchematicBox, sheet SchematicRenderSheet, placed []SchematicBox) error {
	if !boxInside(r, usable) {
		return fmt.Errorf("zone %s violates page padding", id)
	}
	for _, k := range sheet.Keepouts {
		if sheetPreviewConflict(r, k, sheet.Gap+.5) {
			return fmt.Errorf("zone %s enters keepout clearance", id)
		}
	}
	for _, b := range placed {
		// One requested gap, plus only the two half-strokes. Padding both
		// rectangles by Gap would incorrectly reserve twice the user's value.
		if sheetPreviewConflict(r, b, sheet.Gap+1) {
			return fmt.Errorf("zone %s overlaps another zone/clearance", id)
		}
	}
	return nil
}
func validateSheetSpec(s *SchematicRenderSheet) error {
	if s == nil || !plBoxValid(s.Bounds) || !plBoxValid(s.Border) || !boxInside(s.Border, s.Bounds) || !plFinite(s.Padding) || s.Padding < 10 || !plFinite(s.Gap) || s.Gap < 10 || s.Keepouts == nil {
		return fmt.Errorf("sheet requires valid bounds/border, explicit keepouts, padding/gap >= 10 raw")
	}
	if s.Flow != "" && s.Flow != "z" && s.Flow != "compact" {
		return fmt.Errorf("sheet.flow must be z or compact")
	}
	if s.Bounds.MaxX-s.Bounds.MinX > 5000 || s.Bounds.MaxY-s.Bounds.MinY > 5000 {
		return fmt.Errorf("sheet exceeds preview search bounds")
	}
	if !plBoxValid(sheetPreviewUsable(*s)) {
		return fmt.Errorf("padding consumes sheet")
	}
	for _, k := range s.Keepouts {
		if !plBoxValid(k) || !boxInside(k, s.Bounds) {
			return fmt.Errorf("invalid sheet keepout")
		}
	}
	return nil
}
func validateSchematicSheet(in SchematicRenderInput) error {
	var err error
	in, err = resolveSchematicRenderSpacing(in)
	if err != nil {
		return err
	}
	if e := validateSheetSpec(in.Sheet); e != nil {
		return e
	}
	if err := validateSheetZoneRelations(in.Zones); err != nil {
		return err
	}
	u := sheetPreviewUsable(*in.Sheet)
	placed := []SchematicBox{}
	for _, z := range in.Zones {
		r, e := sheetPreviewRect(z, in.Spacing)
		if e != nil {
			return e
		}
		if e := sheetPreviewPlacementError(z.ID, r, u, *in.Sheet, placed); e != nil {
			return e
		}
		placed = append(placed, r)
	}
	if in.Sheet.Flow == "z" {
		return validateSchematicZSheet(in)
	}
	return nil
}

// PlanSchematicSheets packs immutable local layouts. It does not merge nets,
// resize symbols, alter source pages or establish a live Apply baseline.
func PlanSchematicSheets(in SchematicRenderInput) (*SchematicSheetsPreview, error) {
	var err error
	in, err = resolveSchematicRenderSpacing(in)
	if err != nil {
		return nil, err
	}
	// Planning adopts Z flow by default; an omitted flow on an existing render
	// remains a legacy-compatible validation mode. Never stamp the caller's sheet.
	if in.Sheet != nil {
		sheet := *in.Sheet
		if sheet.Flow == "" {
			sheet.Flow = "z"
		}
		in.Sheet = &sheet
	}
	if e := validateSheetSpec(in.Sheet); e != nil {
		return nil, e
	}
	if len(in.Zones) > 64 {
		return nil, fmt.Errorf("at most 64 zones per preview")
	}
	if err := validateSheetZoneRelations(in.Zones); err != nil {
		return nil, err
	}
	if err := validateSchematicZoneVariants(in); err != nil {
		return nil, err
	}
	hasVariants := schematicSheetHasVariants(in.Zones)
	if hasVariants && in.Sheet.Flow != "z" {
		return nil, fmt.Errorf("zone variants require sheet.flow z; compact does not select variants")
	}
	validation := in
	validation.Sheet = nil
	if hasVariants {
		validation.Zones = append([]SchematicRenderZone(nil), in.Zones...)
		for i := range validation.Zones {
			validation.Zones[i].Variants = nil
		}
	}
	if _, e := RenderSchematicLayoutSVG(validation); e != nil {
		return nil, e
	}
	zones := append([]SchematicRenderZone(nil), in.Zones...)
	out := &SchematicSheetsPreview{SchemaVersion: 1, PreviewOnly: true, PlacementMode: "repacked", ZoneCount: len(zones), BlockedZones: []string{}}
	if in.Spacing != nil {
		spacing := *in.Spacing
		out.Spacing = &spacing
	}
	u := sheetPreviewUsable(*in.Sheet)
	out.UsableAreaUpperBound = (u.MaxX - u.MinX) * (u.MaxY - u.MinY)
	// Subtract only non-overlapping intersections to keep this an upper bound.
	maxKeepout := 0.0
	for _, k := range in.Sheet.Keepouts {
		maxKeepout = math.Max(maxKeepout, math.Max(0, math.Min(u.MaxX, k.MaxX)-math.Max(u.MinX, k.MinX))*math.Max(0, math.Min(u.MaxY, k.MaxY)-math.Max(u.MinY, k.MinY)))
	}
	out.UsableAreaUpperBound -= maxKeepout
	for i, z := range zones {
		f, e := sheetPreviewFrame(z, in.Spacing)
		if e != nil {
			return nil, e
		}
		zones[i].Frame = &f
		out.FrameArea += (f.Rect.MaxX - f.Rect.MinX) * (f.Rect.MaxY - f.Rect.MinY)
		if z.Status == "blocked" {
			out.BlockedZones = append(out.BlockedZones, z.ID)
		}
	}
	// Existing positions are page-level hints, not electrical coordinates. Z
	// mode only reuses its exact canonical result; compact mode may reuse any
	// complete valid page. Neither path alters immutable local layouts.
	allPositioned := true
	for _, z := range zones {
		if z.SheetPosition == nil {
			allPositioned = false
			continue
		}
		if !plGrid(z.SheetPosition.X) || !plGrid(z.SheetPosition.Y) {
			return nil, fmt.Errorf("zone %s requires finite grid-aligned sheetPosition", z.ID)
		}
	}
	if in.Sheet.Flow == "z" {
		in.Spacing = out.Spacing
		var pages []SchematicRenderInput
		var err error
		if hasVariants {
			pages, out.VariantSearch, err = planSchematicZVariantSheets(in, zones)
		} else {
			pages, err = planSchematicZSheets(in, zones)
		}
		if err != nil {
			return nil, err
		}
		out.Pages = pages
		if hasVariants {
			out.FrameArea = 0
			for _, page := range pages {
				for _, z := range page.Zones {
					w, h := sheetRelationDimensions(z)
					out.FrameArea += w * h
				}
			}
		}
		if !hasVariants && allPositioned && len(pages) == 1 && sameSchematicSheetPositions(zones, pages[0].Zones) {
			out.PlacementMode = "reused"
		}
		for i := range out.Pages {
			out.Pages[i].Title = fmt.Sprintf("%s · %d/%d", in.Title, i+1, len(out.Pages))
		}
		return out, nil
	}
	if allPositioned {
		page := in
		page.Zones = zones
		if validateSchematicSheet(page) == nil {
			out.PlacementMode = "reused"
			out.Pages = []SchematicRenderInput{page}
			return out, nil
		}
	}
	if sheetHasZoneRelations(zones) {
		in.Spacing = out.Spacing
		pages, err := planSchematicRelatedSheets(in, zones)
		if err != nil {
			return nil, err
		}
		out.Pages = pages
		for i := range out.Pages {
			out.Pages[i].Title = fmt.Sprintf("%s · %d/%d", in.Title, i+1, len(out.Pages))
		}
		return out, nil
	}
	for order := 0; order < 4; order++ {
		candidates := 0
		work := append([]SchematicRenderZone(nil), zones...)
		metric := func(z SchematicRenderZone) float64 {
			r := z.Frame.Rect
			switch order {
			case 1:
				return r.MaxY - r.MinY
			case 2:
				return r.MaxX - r.MinX
			default:
				return (r.MaxX - r.MinX) * (r.MaxY - r.MinY)
			}
		}
		if order > 0 {
			sort.SliceStable(work, func(i, j int) bool { return metric(work[i]) > metric(work[j]) })
		}
		pages := []SchematicRenderInput{}
		for _, z := range work {
			found := false
			for p := 0; p <= len(pages); p++ {
				fresh := p == len(pages)
				page := SchematicRenderInput{SchemaVersion: 1, Title: in.Title, Sheet: in.Sheet, Spacing: out.Spacing, Diagnostic: in.Diagnostic}
				if !fresh {
					page = pages[p]
				}
				placed := make([]SchematicBox, 0, len(page.Zones))
				for _, placedZone := range page.Zones {
					r, e := sheetPreviewRect(placedZone)
					if e != nil {
						return nil, e
					}
					placed = append(placed, r)
				}
				w, h := z.Frame.Rect.MaxX-z.Frame.Rect.MinX, z.Frame.Rect.MaxY-z.Frame.Rect.MinY
				for y := u.MaxY; y-h >= u.MinY && !found; y -= 5 {
					for x := u.MinX; x+w <= u.MaxX && !found; x += 5 {
						candidates++
						if candidates > 2000000 {
							return nil, fmt.Errorf("sheet preview search budget exhausted; no capacity conclusion")
						}
						r := SchematicBox{MinX: x, MinY: y - h, MaxX: x + w, MaxY: y}
						if sheetPreviewPlacementError(z.ID, r, u, *page.Sheet, placed) == nil {
							candidate := z
							candidate.SheetPosition = &SchematicSheetPosition{X: x, Y: y}
							page.Zones = append(append([]SchematicRenderZone(nil), page.Zones...), candidate)
							found = true
						}
					}
				}
				if found {
					if fresh {
						pages = append(pages, page)
					} else {
						pages[p] = page
					}
					break
				}
				if fresh {
					return nil, fmt.Errorf("zone %s cannot fit empty sheet at requested padding (no scaling)", z.ID)
				}
			}
		}
		if out.Pages == nil || len(pages) < len(out.Pages) {
			out.Pages = pages
		}
	}
	for i := range out.Pages {
		out.Pages[i].Title = fmt.Sprintf("%s · %d/%d", in.Title, i+1, len(out.Pages))
	}
	return out, nil
}
