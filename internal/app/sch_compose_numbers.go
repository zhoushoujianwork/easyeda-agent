package app

import "github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"

// Match the design-diff/revision numeric contract at the compile boundary.
// This removes API arithmetic tails at 1e-9 raw, not at the 5-raw drawing grid.
// The caller owns this generated drawing; source measurements and live before
// snapshots must never be passed here or rewritten to manufacture a match.
func normalizeSchCompositionGeometry(p *powerLayoutPlan) {
	n := connectivity.NormalizeDesignNumber
	box := func(b layoutBBox) layoutBBox {
		return layoutBBox{MinX: n(b.MinX), MinY: n(b.MinY), MaxX: n(b.MaxX), MaxY: n(b.MaxY)}
	}
	for i := range p.Placements {
		c := &p.Placements[i]
		c.X, c.Y, c.Rotation = n(c.X), n(c.Y), n(c.Rotation)
		c.BBox = box(c.BBox)
		for j := range c.TextBBoxes {
			c.TextBBoxes[j] = box(c.TextBBoxes[j])
		}
		for j := range c.Pins {
			c.Pins[j].X, c.Pins[j].Y = n(c.Pins[j].X), n(c.Pins[j].Y)
		}
	}
	for i := range p.Wires {
		for j := range p.Wires[i].Points {
			point := &p.Wires[i].Points[j]
			point[0], point[1] = n(point[0]), n(point[1])
		}
	}
	for i := range p.Flags {
		f := &p.Flags[i]
		f.PinX, f.PinY, f.Offset = n(f.PinX), n(f.PinY), n(f.Offset)
	}
	for i := range p.Frames {
		f := &p.Frames[i]
		f.Rect = box(f.Rect)
		f.TitleX, f.TitleY, f.FontSize = n(f.TitleX), n(f.TitleY), n(f.FontSize)
		if l := f.TitleLayout; l != nil {
			l.Width, l.Height, l.Clearance = n(l.Width), n(l.Height), n(l.Clearance)
			for j := range l.Obstacles {
				l.Obstacles[j] = box(l.Obstacles[j])
			}
		}
	}
}
