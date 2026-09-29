package app

import "fmt"

// Modules are consumed in logical order, never sorted by their previous XY.
// Frames in a row share their top edge, retaining their content-derived height.
// The next row advances by this row's tallest frame. Internal circuit geometry
// is translated as a unit; a small module never inherits another module's blank
// vertical space.
type schModuleRowPlacement struct {
	Frame  schFrameSpec
	Row    int
	DX, DY float64
}

func planSchModuleRows(frames []schFrameSpec, sheet layoutBBox, margin, gap float64) ([]schModuleRowPlacement, error) {
	if len(frames) == 0 || !plBoxValid(sheet) || !plGrid(margin) || !plGrid(gap) || margin < 0 || gap < 0 {
		return nil, fmt.Errorf("Z layout requires frames, a finite sheet and nonnegative grid spacing")
	}
	seen := map[string]bool{}
	for _, f := range frames {
		if !plBoxValid(f.Rect) || f.ID == "" || seen[f.ID] {
			return nil, fmt.Errorf("invalid/duplicate module frame %q", f.ID)
		}
		seen[f.ID] = true
	}
	x, top := sheet.MinX+margin, sheet.MaxY-margin
	right, bottom := sheet.MaxX-margin, sheet.MinY+margin
	row := 0
	rowHeight := 0.0
	out := make([]schModuleRowPlacement, 0, len(frames))
	for _, f := range frames {
		width := plCeil(f.Rect.MaxX - f.Rect.MinX)
		height := f.Rect.MaxY - f.Rect.MinY
		if width > right-(sheet.MinX+margin) {
			return nil, fmt.Errorf("module %s is wider than the usable sheet", f.ID)
		}
		if x+width > right {
			x = sheet.MinX + margin
			top -= plCeil(rowHeight) + gap
			row++
			rowHeight = 0
		}
		if top-height < bottom {
			return nil, fmt.Errorf("module %s would exceed the single sheet in row %d; reduce input extents (no automatic pagination)", f.ID, row+1)
		}
		dx, dy := x-f.Rect.MinX, top-f.Rect.MaxY
		f = translateSchFrame(f, dx, dy)
		f.Rect = layoutBBox{MinX: x, MinY: top - height, MaxX: x + width, MaxY: top}
		out = append(out, schModuleRowPlacement{Frame: f, Row: row, DX: dx, DY: dy})
		if height > rowHeight {
			rowHeight = height
		}
		x += width + gap
	}
	return out, nil
}

// A paperless composition uses a derived validation envelope only. Its width
// comes from complete module frames, so this does not impose a paper limit
// or silently turn the drawing back into an A-size sheet.
func schCompositionContentDerivedBounds(frames []schFrameSpec) layoutBBox {
	return schCompositionContentDerivedBoundsRows(frames, nil)
}

// A break gives the index of the first module in the next row. The caller
// chooses the reading order; this function never rotates or resizes a module.
func validateSchPaperlessRowBreaks(breaks []int, count int) error {
	previous := 0
	for _, at := range breaks {
		if at <= previous || at >= count {
			return fmt.Errorf("paperlessRowBreaks must be strictly increasing indices between 1 and %d", count-1)
		}
		previous = at
	}
	return nil
}

func schCompositionContentDerivedBoundsRows(frames []schFrameSpec, breaks []int) layoutBBox {
	width, height := 0.0, 0.0
	rowWidth, rowHeight, breakIndex := 0.0, 0.0, 0
	for i, frame := range frames {
		if breakIndex < len(breaks) && i == breaks[breakIndex] {
			width = max(width, rowWidth)
			height += rowHeight + schModuleGap
			rowWidth, rowHeight = 0, 0
			breakIndex++
		}
		if rowWidth > 0 {
			rowWidth += schModuleGap
		}
		rowWidth += plCeil(frame.Rect.MaxX - frame.Rect.MinX)
		rowHeight = max(rowHeight, plCeil(frame.Rect.MaxY-frame.Rect.MinY))
	}
	width = max(width, rowWidth)
	height += rowHeight
	return layoutBBox{MinX: 0, MinY: 0, MaxX: width, MaxY: height}
}

func planSchPaperlessRows(frames []schFrameSpec, bounds layoutBBox, breaks []int) ([]schModuleRowPlacement, error) {
	if len(frames) == 0 || !plBoxValid(bounds) {
		return nil, fmt.Errorf("paperless rows require valid frames and content bounds")
	}
	if err := validateSchPaperlessRowBreaks(breaks, len(frames)); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]schModuleRowPlacement, 0, len(frames))
	x, top, row, rowHeight, breakIndex := bounds.MinX, bounds.MaxY, 0, 0.0, 0
	for i, f := range frames {
		if !plBoxValid(f.Rect) || f.ID == "" || seen[f.ID] {
			return nil, fmt.Errorf("invalid/duplicate module frame %q", f.ID)
		}
		seen[f.ID] = true
		if breakIndex < len(breaks) && i == breaks[breakIndex] {
			x = bounds.MinX
			top -= rowHeight + schModuleGap
			rowHeight = 0
			row++
			breakIndex++
		}
		width := plCeil(f.Rect.MaxX - f.Rect.MinX)
		height := plCeil(f.Rect.MaxY - f.Rect.MinY)
		if x+width > bounds.MaxX || top-height < bounds.MinY {
			return nil, fmt.Errorf("module %s exceeds derived paperless bounds", f.ID)
		}
		dx, dy := x-f.Rect.MinX, top-f.Rect.MaxY
		f = translateSchFrame(f, dx, dy)
		f.Rect = layoutBBox{MinX: x, MinY: top - height, MaxX: x + width, MaxY: top}
		out = append(out, schModuleRowPlacement{Frame: f, Row: row, DX: dx, DY: dy})
		rowHeight = max(rowHeight, height)
		x += width + schModuleGap
	}
	return out, nil
}

func translatePowerLayout(p *powerLayoutPlan, dx, dy float64) {
	for i, c := range p.Placements {
		p.Placements[i] = plTranslate(c, dx, dy)
	}
	for i := range p.Wires {
		for j := range p.Wires[i].Points {
			p.Wires[i].Points[j][0] += dx
			p.Wires[i].Points[j][1] += dy
		}
	}
	for i := range p.Flags {
		p.Flags[i].PinX += dx
		p.Flags[i].PinY += dy
	}
	for i, f := range p.Frames {
		p.Frames[i] = translateSchFrame(f, dx, dy)
	}
}

func translateSchFrame(f schFrameSpec, dx, dy float64) schFrameSpec {
	shift := func(b layoutBBox) layoutBBox {
		return layoutBBox{MinX: b.MinX + dx, MinY: b.MinY + dy, MaxX: b.MaxX + dx, MaxY: b.MaxY + dy}
	}
	f.Rect = shift(f.Rect)
	f.TitleX += dx
	f.TitleY += dy
	if f.TitleLayout != nil {
		l := *f.TitleLayout
		l.Obstacles = make([]layoutBBox, len(f.TitleLayout.Obstacles))
		for i, o := range f.TitleLayout.Obstacles {
			l.Obstacles[i] = shift(o)
		}
		f.TitleLayout = &l
	}
	return f
}
