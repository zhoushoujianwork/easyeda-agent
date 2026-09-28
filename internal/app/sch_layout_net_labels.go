package app

import (
	"fmt"
	"math"
)

// Explicit user-selected label connectivity: solve naming inside each measured
// component envelope, then pack complete envelopes. No inter-component router
// or physical-attachment search runs, and source nets/ownership remain intact.
func solveSchematicNetLabels(input SchematicLayoutInput, measured map[string]powerLayoutPlacement, members []string, budget *int) (*SchematicLayoutResult, error) {
	if err := validateSchematicExpandedAttachments(input, measured, members); err != nil {
		return nil, err
	}
	order := []string{input.CoreComponentID}
	for _, id := range members {
		if id != input.CoreComponentID {
			order = append(order, id)
		}
	}
	var cells []powerLayoutPlan
	var bounds []layoutBBox
	area, widest := 0.0, 0.0
	const gap = 30.0
	for _, id := range order {
		if *budget <= 0 {
			return nil, errLibLayoutBudget
		}
		*budget -= 1
		c := measured[id]
		cell := powerLayoutPlan{SchemaVersion: 1, Placements: []powerLayoutPlacement{plTranslate(c, -c.X, -c.Y)}}
		if err := libNameIslands(&cell, input.NetPolicies, budget); err != nil {
			return nil, fmt.Errorf("component %s (%s) net-label naming: %w", id, c.Designator, err)
		}
		if err := validateLibGeometry(&cell); err != nil {
			return nil, err
		}
		if err := validateSchCompositionNets(&cell); err != nil {
			return nil, err
		}
		box := powerLayoutContentBounds(&cell)
		cells = append(cells, cell)
		bounds = append(bounds, box)
		w, h := box.MaxX-box.MinX, box.MaxY-box.MinY
		area += (w + gap) * (h + gap)
		widest = math.Max(widest, w)
	}
	// A computed row target is a presentation preference, never a rejection
	// boundary. Oversized cells extend the row; there is no fixed sheet limit.
	rowTarget := plCeil(math.Max(widest, math.Sqrt(area)*1.4))
	p := powerLayoutPlan{SchemaVersion: 1}
	x, y, rowHeight := 0.0, 0.0, 0.0
	coreX, coreY := 0.0, 0.0
	for i, cell := range cells {
		box := bounds[i]
		w, h := box.MaxX-box.MinX, box.MaxY-box.MinY
		if x > 0 && x+w > rowTarget {
			x = 0
			y -= plCeil(rowHeight + gap)
			rowHeight = 0
		}
		dx, dy := plCeil(x-box.MinX), plFloor(y-box.MaxY)
		translatePowerLayout(&cell, dx, dy)
		if i == 0 {
			coreX, coreY = cell.Placements[0].X, cell.Placements[0].Y
		}
		p.Placements = append(p.Placements, cell.Placements...)
		p.Wires = append(p.Wires, cell.Wires...)
		p.Flags = append(p.Flags, cell.Flags...)
		x += plCeil(w + gap)
		rowHeight = math.Max(rowHeight, plCeil(h))
	}
	translatePowerLayout(&p, -coreX, -coreY)
	// Apply one rigid translation to source floats, avoiding compounded rounding
	// of measured fractional bboxes during local naming and row packing.
	for i, c := range p.Placements {
		original := measured[order[i]]
		p.Placements[i] = plTranslate(original, c.X-original.X, c.Y-original.Y)
	}
	if err := validateLibGeometry(&p); err != nil {
		return nil, fmt.Errorf("net-label packing: %w", err)
	}
	if err := validateSchCompositionNets(&p); err != nil {
		return nil, fmt.Errorf("net-label connectivity: %w", err)
	}
	return &SchematicLayoutResult{Placements: p.Placements, Wires: p.Wires, Flags: p.Flags, Score: libCandidateScore(&p),
		Search: &SchematicLayoutSearchDiagnostics{Strategy: "net-labels-v1"}}, nil
}
