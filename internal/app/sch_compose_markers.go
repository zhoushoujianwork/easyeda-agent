package app

import (
	"fmt"
	"math"
)

// Reuse the same calibrated marker bodies and text bands as the live checker.
// A module's overall envelope alone cannot reveal two labels inside it colliding.
func compositionMarkerGeometry(p *powerLayoutPlan) ([]layoutBBox, error) {
	if err := validatePlacementText(p, layoutBBox{-math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64, math.MaxFloat64}); err != nil {
		return nil, err
	}
	comps := []layoutComp{}
	for _, c := range p.Placements {
		c := c
		comps = append(comps, layoutComp{ID: c.Designator, Designator: c.Designator, ComponentType: "part", BBox: &c.BBox})
	}
	withoutFlags := *p
	withoutFlags.Flags = nil
	obstacles := powerLayoutContentObstacles(&withoutFlags)
	for i, f := range p.Flags {
		x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
		body := predictedMarkerBody(x, y, f.Kind, f.Direction, f.Net)
		family, kind := f.Kind, "netflag"
		if isNetPortKind(f.Kind) {
			family, kind = "port", "netport"
		}
		rotation := flagBodyRotation[family][f.Direction]
		c := layoutComp{ID: fmt.Sprintf("marker-%03d-%s", i, f.Net), ComponentType: kind, Net: f.Net, X: x, Y: y, AnchorAvailable: true, Rotation: &rotation, BBox: &body}
		comps = append(comps, c)
		obstacles = append(obstacles, body)
		if band := flagTextBand(c); band != nil {
			obstacles = append(obstacles, *band)
		}
		obstacles = append(obstacles, layoutBBox{MinX: math.Min(f.PinX, x) - 0.5, MinY: math.Min(f.PinY, y) - 0.5, MaxX: math.Max(f.PinX, x) + 0.5, MaxY: math.Max(f.PinY, y) + 0.5})
	}
	if findings := analyzeMarkerGeometry(comps, nil, sheetSourceNone, 1); len(findings) > 0 {
		return nil, fmt.Errorf("marker geometry: %d overlap(s); %s", len(findings), findings[0].Message)
	}
	if err := compositionWireMarkerGeometry(p, comps); err != nil {
		return nil, err
	}
	return obstacles, nil
}

// Check actual segments, not the envelope of a bent wire. Electrical topology
// can be correct even when a wire runs through another marker's body or name.
func compositionWireMarkerGeometry(p *powerLayoutPlan, comps []layoutComp) error {
	wires := append([]powerLayoutWire(nil), p.Wires...)
	for _, f := range p.Flags {
		x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
		wires = append(wires, powerLayoutWire{Net: f.Net, Points: [][2]float64{{f.PinX, f.PinY}, {x, y}}})
	}
	for _, c := range comps {
		if !isSchMarker(c.ComponentType) || c.BBox == nil {
			continue
		}
		// The measured body includes a half-unit stroke halo. Removing that
		// halo allows a normal lead to terminate at the marker's anchor.
		body := *c.BBox
		body.MinX += 0.5
		body.MinY += 0.5
		body.MaxX -= 0.5
		body.MaxY -= 0.5
		boxes := []layoutBBox{body}
		if band := flagTextBand(c); band != nil {
			boxes = append(boxes, *band)
		}
		for _, w := range wires {
			for i := 1; i < len(w.Points); i++ {
				for _, box := range boxes {
					if plSegmentBox(w.Points[i-1], w.Points[i], box) {
						return fmt.Errorf("wire-marker overlap: wire %s %v→%v crosses %s body/text", w.Net, w.Points[i-1], w.Points[i], c.Net)
					}
				}
			}
		}
	}
	return nil
}
