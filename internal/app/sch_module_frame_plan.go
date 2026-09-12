package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// A saved native measurement is optional. Matching text and font size prevent
// accidentally reusing a width from a different title. Coordinates are 0.01 inch.
type schTitleMetrics struct {
	Title    string  `json:"title"`
	FontSize float64 `json:"fontSize"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
}

func planSchModuleFrame(id, title string, content, sheet layoutBBox) (schFrameSpec, error) {
	return measureSchModuleFrameObstacles(id, title, []layoutBBox{content}, nil, &sheet)
}

func measureSchModuleFrame(id, title string, content layoutBBox) (schFrameSpec, error) {
	return measureSchModuleFrameObstacles(id, title, []layoutBBox{content}, nil, nil)
}

// A conservative fallback for the current plain sans-serif font, not a native
// measurement. Narrow Latin glyphs need less space than a CJK/full-em glyph.
// Apply must verify the rendered bbox against this envelope and the obstacles.
func schModuleTitleWidth(title string, size float64) float64 {
	width := 0.0
	for _, r := range title {
		em := 1.05
		switch {
		case strings.ContainsRune(" ilI.,:;!|'", r):
			em = .35
		case strings.ContainsRune("MWmw@%", r):
			em = 1.0
		case r >= 'A' && r <= 'Z':
			em = .8
		case r >= 32 && r <= 126:
			em = .65
		}
		width += em * size
	}
	return plCeil(width)
}

func schBoundsUnion(boxes []layoutBBox) layoutBBox {
	b := boxes[0]
	for _, v := range boxes[1:] {
		b.MinX, b.MinY = math.Min(b.MinX, v.MinX), math.Min(b.MinY, v.MinY)
		b.MaxX, b.MaxY = math.Max(b.MaxX, v.MaxX), math.Max(b.MaxY, v.MaxY)
	}
	return b
}

// Search the upper/lower silhouette at every relevant horizontal gap boundary.
// No fixed title band: first reuse free space inside the padded circuit bounds,
// otherwise grow only enough to fit. Height wins, then area, leftmost X, top.
// Restrict horizontal expansion to that required by the title itself.
func measureSchModuleFrameObstacles(id, title string, obstacles []layoutBBox, metrics *schTitleMetrics, sheet *layoutBBox) (schFrameSpec, error) {
	return measureSchModuleFrameObstaclesSpacing(id, title, obstacles, metrics, sheet, nil)
}

// Optional spacing is the minimum visible clearance, not a distance between
// stroke centerlines. The legacy path is unchanged; unified spacing reserves a
// half-unit frame stroke and rounds outwards to the schematic's 5-unit grid.
func measureSchModuleFrameObstaclesSpacing(id, title string, obstacles []layoutBBox, metrics *schTitleMetrics, sheet *layoutBBox, spacing *float64) (schFrameSpec, error) {
	if err := validateSchematicSpacing(spacing); err != nil {
		return schFrameSpec{}, err
	}
	padding, inset := schModuleFramePadding, schModuleTitleInset
	if spacing != nil {
		padding = *spacing + .5
		inset = plCeil(padding)
	}
	const fontSize, clearance = schModuleTitleFontSize, schModuleTitleClearance
	if strings.TrimSpace(id) == "" || strings.TrimSpace(title) == "" || strings.ContainsAny(title, "\r\n") || len(obstacles) == 0 {
		return schFrameSpec{}, fmt.Errorf("module frame requires an id, single-line title and finite content bounds")
	}
	for _, b := range obstacles {
		if !plBoxValid(b) {
			return schFrameSpec{}, fmt.Errorf("module %s has invalid occupied bounds", id)
		}
	}
	if sheet != nil && !plBoxValid(*sheet) {
		return schFrameSpec{}, fmt.Errorf("invalid sheet bounds")
	}
	width, height := schModuleTitleWidth(title, fontSize), fontSize
	if metrics != nil {
		if metrics.Title != title || metrics.FontSize != fontSize || !plFinite(metrics.Width) || !plFinite(metrics.Height) || metrics.Width <= 0 || metrics.Height <= 0 {
			return schFrameSpec{}, fmt.Errorf("titleMetrics must match the title/fontSize and contain positive finite dimensions")
		}
		width, height = plCeil(metrics.Width), math.Max(fontSize, plCeil(metrics.Height))
	}
	content := schBoundsUnion(obstacles)
	base := layoutBBox{MinX: plFloor(content.MinX - padding), MinY: plFloor(content.MinY - padding), MaxX: plCeil(content.MaxX + padding), MaxY: plCeil(content.MaxY + padding)}
	base.MaxX = math.Max(base.MaxX, plCeil(base.MinX+width+2*inset))
	if sheet != nil && base.MaxX > sheet.MaxX && content.MaxX+padding <= sheet.MaxX {
		shift := plCeil(base.MaxX - sheet.MaxX)
		base.MinX -= shift
		base.MaxX -= shift
	}
	xmin, xmax := base.MinX+inset, plFloor(base.MaxX-inset-width)
	xs := []float64{xmin, xmax}
	for _, o := range obstacles {
		xs = append(xs, plCeil(o.MaxX+clearance), plFloor(o.MinX-clearance-width))
	}
	sort.Float64s(xs)
	var best schFrameSpec
	found := false
	for i, x := range xs {
		if x < xmin || x > xmax || (i > 0 && x == xs[i-1]) {
			continue
		}
		top, bottom := base.MaxY-inset, base.MinY+inset+height
		for _, o := range obstacles {
			if x < o.MaxX+clearance && x+width > o.MinX-clearance {
				top = math.Max(top, plCeil(o.MaxY+clearance+height))
				bottom = math.Min(bottom, plFloor(o.MinY-clearance))
			}
		}
		for _, y := range []float64{top, bottom} {
			f := schFrameSpec{ID: id, Title: title, FontSize: fontSize, Color: "#AA00AA", LineType: 1, Rect: base, TitleX: x, TitleY: y,
				TitleLayout: &schFrameTitleLayout{Width: width, Height: height, Clearance: clearance, Obstacles: append([]layoutBBox(nil), obstacles...)}}
			f.Rect.MaxY = math.Max(base.MaxY, plCeil(y+inset))
			f.Rect.MinY = math.Min(base.MinY, plFloor(y-height-inset))
			if sheet != nil && !boxInside(f.Rect, *sheet) {
				continue
			}
			if !found || schFrameMoreCompact(f, best) {
				best, found = f, true
			}
		}
	}
	if !found {
		return schFrameSpec{}, fmt.Errorf("module %s frame/title outside sheet; revise the input layout (no automatic pagination)", id)
	}
	return best, nil
}

func validateSchematicSpacing(spacing *float64) error {
	if spacing != nil && (!plGrid(*spacing) || *spacing < 10) {
		return fmt.Errorf("spacing must be finite, >= 10 raw and on the 5-raw grid")
	}
	return nil
}

// Supplied frames are immutable sheet inputs. A different page padding must
// never silently rebuild a too-small local frame around unchanged content.
func validateSchematicFrameSpacing(f schFrameSpec, obstacles []layoutBBox, spacing *float64) error {
	if err := validateSchematicSpacing(spacing); err != nil || spacing == nil {
		return err
	}
	if !plBoxValid(f.Rect) || !plFinite(f.TitleX) || !plFinite(f.TitleY) || !plFinite(f.FontSize) || f.FontSize <= 0 || len(obstacles) == 0 {
		return fmt.Errorf("frame %s requires complete geometry for unified spacing", f.ID)
	}
	n := *spacing + .5
	inside := layoutBBox{MinX: f.Rect.MinX + n, MinY: f.Rect.MinY + n, MaxX: f.Rect.MaxX - n, MaxY: f.Rect.MaxY - n}
	if !plBoxValid(inside) {
		return fmt.Errorf("frame %s cannot contain spacing %g", f.ID, *spacing)
	}
	for _, b := range obstacles {
		if !plBoxValid(b) || !boxInside(b, inside) {
			return fmt.Errorf("frame %s violates zone inner spacing %g; replan this zone before sheet packing", f.ID, *spacing)
		}
	}
	title := layoutBBox{MinX: f.TitleX, MinY: f.TitleY - f.FontSize, MaxX: f.TitleX + schModuleTitleWidth(f.Title, f.FontSize), MaxY: f.TitleY}
	if f.TitleLayout != nil {
		if err := checkSchFrameTitleOccupancy(f, f.titleBounds()); err != nil {
			return err
		}
		title = f.titleBounds()
	}
	if !boxInside(title, inside) {
		return fmt.Errorf("frame %s title violates zone inner spacing %g", f.ID, *spacing)
	}
	clearance := schModuleTitleClearance
	if f.TitleLayout != nil {
		clearance = f.TitleLayout.Clearance
	}
	// A retained frame may accompany newly solved local geometry. Its saved
	// title obstacles describe the old result, so check current occupancy too.
	for _, b := range obstacles {
		if title.MinX < b.MaxX+clearance-1e-6 && title.MaxX > b.MinX-clearance+1e-6 && title.MinY < b.MaxY+clearance-1e-6 && title.MaxY > b.MinY-clearance+1e-6 {
			return fmt.Errorf("frame %s title collides with current zone content; replan this zone before sheet packing", f.ID)
		}
	}
	return nil
}

func schFrameMoreCompact(a, b schFrameSpec) bool {
	ah, bh := a.Rect.MaxY-a.Rect.MinY, b.Rect.MaxY-b.Rect.MinY
	if ah != bh {
		return ah < bh
	}
	aa, ba := ah*(a.Rect.MaxX-a.Rect.MinX), bh*(b.Rect.MaxX-b.Rect.MinX)
	if aa != ba {
		return aa < ba
	}
	if a.TitleX != b.TitleX {
		return a.TitleX < b.TitleX
	}
	return a.TitleY > b.TitleY
}

// Occupancy retains local gaps instead of flattening the whole module into one
// rectangle. Label/marker bounds remain conservative until measured upstream.
func powerLayoutContentObstacles(plan *powerLayoutPlan) []layoutBBox {
	var boxes []layoutBBox
	segment := func(x1, y1, x2, y2 float64) {
		boxes = append(boxes, layoutBBox{MinX: math.Min(x1, x2) - .5, MinY: math.Min(y1, y2) - .5, MaxX: math.Max(x1, x2) + .5, MaxY: math.Max(y1, y2) + .5})
	}
	for _, c := range plan.Placements {
		boxes = append(boxes, layoutBBox{MinX: c.BBox.MinX, MinY: c.BBox.MinY - 20, MaxX: c.BBox.MaxX, MaxY: c.BBox.MaxY + 15})
		boxes = append(boxes, libPartLabelBoxes(c)...)
		for _, p := range c.Pins {
			// Include the complete pin stem, not just the outer connection point.
			bx := math.Max(c.BBox.MinX, math.Min(c.BBox.MaxX, p.X))
			by := math.Max(c.BBox.MinY, math.Min(c.BBox.MaxY, p.Y))
			segment(p.X, p.Y, bx, by)
		}
	}
	for _, w := range plan.Wires {
		for i := 1; i < len(w.Points); i++ {
			a, b := w.Points[i-1], w.Points[i]
			segment(a[0], a[1], b[0], b[1])
		}
	}
	for _, f := range plan.Flags {
		x, y := f.PinX, f.PinY
		switch f.Direction {
		case "left":
			x -= f.Offset
		case "right":
			x += f.Offset
		case "up":
			y += f.Offset
		case "down":
			y -= f.Offset
		}
		segment(f.PinX, f.PinY, x, y)
		// Share the collision model: symbols occupy the outward side only.
		// A symmetric radius incorrectly doubles the port's reserved length.
		boxes = append(boxes, schTerminalMarkerBoxes(f)...)
	}
	return boxes
}

func powerLayoutContentBounds(plan *powerLayoutPlan) layoutBBox {
	return schBoundsUnion(powerLayoutContentObstacles(plan))
}
