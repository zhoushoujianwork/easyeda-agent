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
}
type SchematicSheetsPreview struct {
	SchemaVersion        int                    `json:"schemaVersion"`
	PreviewOnly          bool                   `json:"previewOnly"`
	ZoneCount            int                    `json:"zoneCount"`
	BlockedZones         []string               `json:"blockedZones"`
	FrameArea            float64                `json:"frameArea"`
	UsableAreaUpperBound float64                `json:"usableAreaUpperBound"`
	Pages                []SchematicRenderInput `json:"pages"`
}

func sheetPreviewUsable(s SchematicRenderSheet) SchematicBox {
	n := s.Padding + 0.5
	return SchematicBox{MinX: plCeil(s.Border.MinX + n), MinY: plCeil(s.Border.MinY + n), MaxX: plFloor(s.Border.MaxX - n), MaxY: plFloor(s.Border.MaxY - n)}
}
func sheetPreviewFrame(z SchematicRenderZone) (schFrameSpec, error) {
	if z.Frame != nil {
		return *z.Frame, nil
	}
	if z.Layout == nil {
		return schFrameSpec{}, fmt.Errorf("zone %s lacks layout", z.ID)
	}
	p := powerLayoutPlan{Placements: z.Layout.Placements, Wires: z.Layout.Wires, Flags: z.Layout.Flags}
	return measureSchModuleFrameObstacles(z.ID, z.Title, powerLayoutContentObstacles(&p), nil, nil)
}
func sheetPreviewRect(z SchematicRenderZone) (SchematicBox, error) {
	f, e := sheetPreviewFrame(z)
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
func validateSheetSpec(s *SchematicRenderSheet) error {
	if s == nil || !plBoxValid(s.Bounds) || !plBoxValid(s.Border) || !boxInside(s.Border, s.Bounds) || !plFinite(s.Padding) || s.Padding < 10 || !plFinite(s.Gap) || s.Gap < 10 || s.Keepouts == nil {
		return fmt.Errorf("sheet requires valid bounds/border, explicit keepouts, padding/gap >= 10 raw")
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
	if e := validateSheetSpec(in.Sheet); e != nil {
		return e
	}
	u := sheetPreviewUsable(*in.Sheet)
	placed := []SchematicBox{}
	for _, z := range in.Zones {
		r, e := sheetPreviewRect(z)
		if e != nil {
			return e
		}
		if !boxInside(r, u) {
			return fmt.Errorf("zone %s violates page padding", z.ID)
		}
		for _, k := range in.Sheet.Keepouts {
			if sheetPreviewConflict(r, k, in.Sheet.Gap+.5) {
				return fmt.Errorf("zone %s enters keepout clearance", z.ID)
			}
		}
		for _, b := range placed {
			if sheetPreviewConflict(r, b, in.Sheet.Gap+1) {
				return fmt.Errorf("zone %s overlaps another zone/clearance", z.ID)
			}
		}
		placed = append(placed, r)
	}
	return nil
}

// PlanSchematicSheets packs immutable local layouts. It does not merge nets,
// resize symbols, alter source pages or establish a live Apply baseline.
func PlanSchematicSheets(in SchematicRenderInput) (*SchematicSheetsPreview, error) {
	if e := validateSheetSpec(in.Sheet); e != nil {
		return nil, e
	}
	if len(in.Zones) > 64 {
		return nil, fmt.Errorf("at most 64 zones per preview")
	}
	validation := in
	validation.Sheet = nil
	if _, e := RenderSchematicLayoutSVG(validation); e != nil {
		return nil, e
	}
	zones := append([]SchematicRenderZone(nil), in.Zones...)
	out := &SchematicSheetsPreview{SchemaVersion: 1, PreviewOnly: true, ZoneCount: len(zones), BlockedZones: []string{}}
	u := sheetPreviewUsable(*in.Sheet)
	out.UsableAreaUpperBound = (u.MaxX - u.MinX) * (u.MaxY - u.MinY)
	// Subtract only non-overlapping intersections to keep this an upper bound.
	maxKeepout := 0.0
	for _, k := range in.Sheet.Keepouts {
		maxKeepout = math.Max(maxKeepout, math.Max(0, math.Min(u.MaxX, k.MaxX)-math.Max(u.MinX, k.MinX))*math.Max(0, math.Min(u.MaxY, k.MaxY)-math.Max(u.MinY, k.MinY)))
	}
	out.UsableAreaUpperBound -= maxKeepout
	for i, z := range zones {
		f, e := sheetPreviewFrame(z)
		if e != nil {
			return nil, e
		}
		zones[i].Frame = &f
		out.FrameArea += (f.Rect.MaxX - f.Rect.MinX) * (f.Rect.MaxY - f.Rect.MinY)
		if z.Status == "blocked" {
			out.BlockedZones = append(out.BlockedZones, z.ID)
		}
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
				page := SchematicRenderInput{SchemaVersion: 1, Title: in.Title, Sheet: in.Sheet}
				if !fresh {
					page = pages[p]
				}
				w, h := z.Frame.Rect.MaxX-z.Frame.Rect.MinX, z.Frame.Rect.MaxY-z.Frame.Rect.MinY
				for y := u.MaxY; y-h >= u.MinY && !found; y -= 5 {
					for x := u.MinX; x+w <= u.MaxX && !found; x += 5 {
						candidates++
						if candidates > 2000000 {
							return nil, fmt.Errorf("sheet preview search budget exhausted; no capacity conclusion")
						}
						candidate := z
						candidate.SheetPosition = &SchematicSheetPosition{X: x, Y: y}
						trial := page
						trial.Zones = append(append([]SchematicRenderZone(nil), page.Zones...), candidate)
						if validateSchematicSheet(trial) == nil {
							page = trial
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
