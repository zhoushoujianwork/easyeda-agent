package app

import (
	"fmt"
	"math"
)

type libMeasuredStem struct {
	component string
	pin       string
	net       string
	a, b      [2]float64
}

// validateLibGeometry validates an incomplete local placement/routing candidate.
// Unlike the final composition net gate, it does not require every pin to have
// reached its final named tree yet.
func validateLibGeometry(p *powerLayoutPlan) error {
	if err := validatePowerLayout(p, layoutBBox{-math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64, math.MaxFloat64}); err != nil {
		return err
	}
	if _, err := compositionMarkerGeometry(p); err != nil {
		return err
	}
	stems, err := libMeasuredStems(p)
	if err != nil {
		return err
	}
	segments, err := schTerminalSegments(p)
	if err != nil {
		return err
	}
	for i, stem := range stems {
		for _, c := range p.Placements {
			if c.Designator != stem.component && plSegmentBox(stem.a, stem.b, c.BBox) {
				return fmt.Errorf("pin stem %s.%s crosses %s body", stem.component, stem.pin, c.Designator)
			}
		}
		for _, wire := range segments {
			if (stem.net == "" || stem.net != wire.Net) && plSegmentsMeet(stem.a, stem.b, wire.Points[0], wire.Points[1]) {
				return fmt.Errorf("wire/marker lead %s touches NC/foreign pin stem %s.%s", wire.Net, stem.component, stem.pin)
			}
		}
		for _, other := range stems[:i] {
			if (stem.net == "" || stem.net != other.net) && plSegmentsMeet(stem.a, stem.b, other.a, other.b) {
				return fmt.Errorf("NC/foreign pin stems intersect: %s.%s/%s.%s", stem.component, stem.pin, other.component, other.pin)
			}
		}
		for _, marker := range p.Flags {
			for _, box := range schTerminalMarkerBoxes(marker) {
				if plSegmentBox(stem.a, stem.b, box) {
					return fmt.Errorf("marker %s body/text crosses pin stem %s.%s", marker.Net, stem.component, stem.pin)
				}
			}
		}
	}
	for i, c := range p.Placements {
		for _, other := range p.Placements[:i] {
			for _, label := range libPartLabelBoxes(c) {
				for _, otherLabel := range libPartLabelBoxes(other) {
					if boxesGapOverlap(label, other.BBox, 5) || boxesGapOverlap(otherLabel, c.BBox, 5) || boxesGapOverlap(label, otherLabel, 5) {
						return fmt.Errorf("component label reservation collision: %s/%s", c.Designator, other.Designator)
					}
				}
			}
		}
	}
	return nil
}

// Only infer the visible axis-aligned stem between an outer measured connection
// point and its unique body edge. A point inside a 0.5-raw stroke halo has no
// positive outside stem; never extrapolate it into an invented long pin.
func libMeasuredStems(p *powerLayoutPlan) ([]libMeasuredStem, error) {
	var stems []libMeasuredStem
	for _, c := range p.Placements {
		if !plBoxValid(c.BBox) {
			return nil, fmt.Errorf("%s invalid measured body", c.Designator)
		}
		for _, q := range c.Pins {
			if !plGrid(q.X) || !plGrid(q.Y) {
				return nil, fmt.Errorf("%s.%s invalid measured pin coordinate", c.Designator, q.Number)
			}
			var side string
			for _, direction := range []string{"left", "right", "up", "down"} {
				if schTerminalPointsOutward(q, c.BBox, direction) {
					if side != "" {
						return nil, fmt.Errorf("%s.%s has ambiguous measured pin stem direction", c.Designator, q.Number)
					}
					side = direction
				}
			}
			if side == "" {
				return nil, fmt.Errorf("%s.%s has unknown measured pin stem direction", c.Designator, q.Number)
			}
			a := [2]float64{q.X, q.Y}
			b := a
			// For an outside pin the body edge lies inward, not further outward.
			switch side {
			case "left":
				if q.X < c.BBox.MinX {
					b[0] = c.BBox.MinX
				}
			case "right":
				if q.X > c.BBox.MaxX {
					b[0] = c.BBox.MaxX
				}
			case "up":
				if q.Y > c.BBox.MaxY {
					b[1] = c.BBox.MaxY
				}
			case "down":
				if q.Y < c.BBox.MinY {
					b[1] = c.BBox.MinY
				}
			}
			if a != b {
				stems = append(stems, libMeasuredStem{component: c.Designator, pin: q.Number, net: q.Net, a: a, b: b})
			}
		}
	}
	return stems, nil
}

// Reuse the existing composition frame's conservative right-side ref/value
// estimate. This is a reservation, not an official measured text bbox. Wires
// are not blocked by the entire reservation: the native label's exact vertical
// position is not represented by powerLayoutPlacement.
func libPartLabelReservation(c powerLayoutPlacement) layoutBBox {
	width := math.Max(plPowerTextWidth(c.Designator), plPowerTextWidth(c.Value))
	return layoutBBox{MinX: c.BBox.MaxX, MinY: c.BBox.MinY - 20, MaxX: c.BBox.MaxX + 10 + width, MaxY: c.BBox.MaxY + 15}
}

func libPartLabelBoxes(c powerLayoutPlacement) []layoutBBox {
	if len(c.TextBBoxes) > 0 {
		return c.TextBBoxes
	}
	return []layoutBBox{libPartLabelReservation(c)}
}
