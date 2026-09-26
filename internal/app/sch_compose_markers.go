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
	if findings := analyzeMarkerGeometry(comps, nil, sheetSourceNone, schMarkerOverlapEps); len(findings) > 0 {
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
	type ownedWire struct {
		wire        powerLayoutWire
		markerIndex int
	}
	wires := make([]ownedWire, 0, len(p.Wires)+len(p.Flags))
	for _, wire := range p.Wires {
		wires = append(wires, ownedWire{wire: wire, markerIndex: -1})
	}
	for i, f := range p.Flags {
		x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
		wires = append(wires, ownedWire{
			wire:        powerLayoutWire{Net: f.Net, Points: [][2]float64{{f.PinX, f.PinY}, {x, y}}},
			markerIndex: i,
		})
	}
	markers := make([]layoutComp, 0, len(p.Flags))
	for _, c := range comps {
		if isSchMarker(c.ComponentType) {
			markers = append(markers, c)
		}
	}
	if len(markers) != len(p.Flags) {
		return fmt.Errorf("wire-marker geometry has %d markers for %d flags", len(markers), len(p.Flags))
	}
	for markerIndex, c := range markers {
		if !isSchMarker(c.ComponentType) || c.BBox == nil {
			continue
		}
		box := markerJudgeBBox(c)
		flag := p.Flags[markerIndex]
		anchor := [2]float64{c.X, c.Y}
		for _, owned := range wires {
			w := owned.wire
			for i := 1; i < len(w.Points); i++ {
				a, b := w.Points[i-1], w.Points[i]
				// Only this marker's generated lead may terminate on its own anchor.
				// The exemption is by object identity plus exact geometry: a same-net
				// or same-owner wire with coincident coordinates is still foreign.
				if owned.markerIndex == markerIndex && i == 1 && len(w.Points) == 2 &&
					((a == [2]float64{flag.PinX, flag.PinY} && b == anchor) ||
						(b == [2]float64{flag.PinX, flag.PinY} && a == anchor)) {
					continue
				}
				if plOnSegment(anchor, a, b) {
					libRecordNamingWireBlockers(p, w)
					return fmt.Errorf("wire-marker overlap: foreign wire %s %v→%v touches %s anchor", w.Net, a, b, c.Net)
				}
				segment, ok := schVisibleWireSegmentBBox([4]float64{a[0], a[1], b[0], b[1]})
				if ok && boxesIntersect(segment, box) {
					libRecordNamingWireBlockers(p, w)
					return fmt.Errorf("wire-marker overlap: wire %s %v→%v crosses %s body/text", w.Net, a, b, c.Net)
				}
			}
		}
	}
	return nil
}
