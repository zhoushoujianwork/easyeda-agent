package app

import (
	"fmt"
	"math"
	"sort"
)

func validateSchematicLayoutMode(mode string, optimization *SchematicLayoutOptimization) error {
	if mode != "" && mode != "search" && mode != "unbounded" && mode != "net-labels" {
		return fmt.Errorf("layoutMode must be search, unbounded or net-labels")
	}
	if (mode == "unbounded" || mode == "net-labels") && optimization != nil {
		return fmt.Errorf("%s layout preserves measured poses; omit optimization", mode)
	}
	return nil
}

// Construct an expanded orthogonal drawing instead of searching permutations.
// Each pin owns a vertical column, each net a horizontal channel. Their proper
// interior crossings are non-contacts under the shared schematic contact model.
// This is deliberately a readability/area tradeoff, not an always-solvable mode:
// measured pin exits and labels can still make the supplied pose impossible.
func solveSchematicUnbounded(input SchematicLayoutInput, measured map[string]powerLayoutPlacement, members []string, budget *int) (*SchematicLayoutResult, error) {
	if err := validateSchematicExpandedAttachments(input, measured, members); err != nil {
		return nil, err
	}
	return buildSchematicUnbounded(input, measured, members, budget)
}

func validateSchematicExpandedAttachments(input SchematicLayoutInput, measured map[string]powerLayoutPlacement, members []string) error {
	// Expanded placement must not turn a malformed explicit attachment into a
	// silently ignored positioning hint. Use the same pin/net rules as search.
	for _, hint := range input.Attachments {
		var others []string
		for _, id := range members {
			if id != hint.ComponentID {
				others = append(others, id)
			}
		}
		if _, err := libAttachmentPairs(hint.ComponentID, measured[hint.ComponentID], hint, measured, others, input.NetPolicies); err != nil {
			return err
		}
	}
	return nil
}

func buildSchematicUnbounded(input SchematicLayoutInput, measured map[string]powerLayoutPlacement, members []string, budget *int) (*SchematicLayoutResult, error) {
	spend := func() error {
		if *budget <= 0 {
			return errLibLayoutBudget
		}
		*budget -= 1
		return nil
	}
	const pitch = 10.0
	type escape struct {
		net  string
		x, y float64
	}
	var escapes []escape
	p := &powerLayoutPlan{SchemaVersion: 1}
	// Put the core first, then translate the complete drawing back to core 0,0.
	order := []string{input.CoreComponentID}
	for _, id := range members {
		if id != input.CoreComponentID {
			order = append(order, id)
		}
	}
	nextX, bottom := 0.0, 0.0
	var coreX, coreY float64
	appendSegment := func(net string, a, b [2]float64) {
		if a != b {
			p.Wires = append(p.Wires, powerLayoutWire{Net: net, Points: [][2]float64{a, b}})
		}
	}
	for _, id := range order {
		if err := spend(); err != nil {
			return nil, err
		}
		c := measured[id]
		box := c.BBox
		for _, b := range libPartLabelBoxes(c) {
			box.MinX, box.MinY = math.Min(box.MinX, b.MinX), math.Min(box.MinY, b.MinY)
			box.MaxX, box.MaxY = math.Max(box.MaxX, b.MaxX), math.Max(box.MaxY, b.MaxY)
		}
		leftCount := 0
		for _, q := range c.Pins {
			box.MinX, box.MinY = math.Min(box.MinX, q.X), math.Min(box.MinY, q.Y)
			box.MaxX, box.MaxY = math.Max(box.MaxX, q.X), math.Max(box.MaxY, q.Y)
			side, _ := libPinSide(q, c.BBox)
			if q.Net != "" && side == "left" {
				leftCount++
			}
		}
		dx, dy := plCeil(nextX+float64(leftCount+2)*pitch-box.MinX), -c.Y
		c = plTranslate(c, dx, dy)
		box.MinX, box.MaxX = box.MinX+dx, box.MaxX+dx
		box.MinY, box.MaxY = box.MinY+dy, box.MaxY+dy
		if id == input.CoreComponentID {
			coreX, coreY = c.X, c.Y
		}
		p.Placements = append(p.Placements, c)
		left, right, up, down := plFloor(box.MinX)-pitch, plCeil(box.MaxX)+pitch, plCeil(box.MaxY)+pitch, plFloor(box.MinY)-pitch
		for _, q := range c.Pins {
			if q.Net == "" {
				continue
			}
			if err := spend(); err != nil {
				return nil, err
			}
			a := [2]float64{q.X, q.Y}
			side, _ := libPinSide(q, c.BBox)
			x, y := right, q.Y
			switch side {
			case "left":
				x, left = left, left-pitch
			case "right":
				right += pitch
			case "up":
				y, up, right = up, up+pitch, right+pitch
				appendSegment(q.Net, a, [2]float64{q.X, y})
				a[1] = y
			case "down":
				y, down, right = down, down-pitch, right+pitch
				appendSegment(q.Net, a, [2]float64{q.X, y})
				a[1] = y
			}
			appendSegment(q.Net, a, [2]float64{x, y})
			escapes = append(escapes, escape{q.Net, x, y})
		}
		bottom = math.Min(bottom, down)
		nextX = right + 4*pitch
	}
	// Markers live beyond every component/escape column. Derive row pitch from
	// their actual geometry rather than a net-name length or fixed paper size.
	nets := make([]string, 0, len(input.NetPolicies))
	for net := range input.NetPolicies {
		nets = append(nets, net)
	}
	sort.Strings(nets)
	markers := make(map[string]powerLayoutFlag, len(nets))
	rowPitch := 40.0
	for _, net := range nets {
		kind, direction := "net_port_bi", "right"
		switch input.NetPolicies[net] {
		case "local_ground":
			kind, direction = "ground", "down"
		case "local_power":
			kind, direction = "power", "up"
		}
		f := powerLayoutFlag{Net: net, Kind: kind, Direction: direction, Offset: 10}
		for _, b := range schTerminalMarkerBoxes(f) {
			rowPitch = math.Max(rowPitch, 2*math.Max(math.Abs(b.MinY), math.Abs(b.MaxY))+20)
		}
		markers[net] = f
	}
	rowPitch = plCeil(rowPitch)
	for i, net := range nets {
		if err := spend(); err != nil {
			return nil, err
		}
		y, minX := plFloor(bottom)-float64(i+1)*rowPitch, math.Inf(1)
		for _, e := range escapes {
			if e.net == net {
				appendSegment(net, [2]float64{e.x, e.y}, [2]float64{e.x, y})
				minX = math.Min(minX, e.x)
			}
		}
		appendSegment(net, [2]float64{minX, y}, [2]float64{nextX, y})
		f := markers[net]
		f.PinX, f.PinY = nextX, y
		p.Flags = append(p.Flags, f)
	}
	// Normalize and preserve T nodes while combining collinear same-net pieces.
	p.Wires = libAppendRoute(nil, p.Wires)
	for i, c := range p.Placements {
		original := measured[order[i]]
		p.Placements[i] = plTranslate(original, c.X-coreX-original.X, c.Y-coreY-original.Y)
	}
	for i := range p.Wires {
		for j := range p.Wires[i].Points {
			p.Wires[i].Points[j][0] -= coreX
			p.Wires[i].Points[j][1] -= coreY
		}
	}
	for i := range p.Flags {
		p.Flags[i].PinX -= coreX
		p.Flags[i].PinY -= coreY
	}
	if err := validateLibGeometry(p); err != nil {
		return nil, fmt.Errorf("unbounded channel geometry: %w", err)
	}
	if err := validateSchCompositionNets(p); err != nil {
		return nil, fmt.Errorf("unbounded channel connectivity: %w", err)
	}
	for _, required := range schematicRequiredAttachments(input) {
		a, b, ok := required.pins(p)
		if !ok || !libPinsShareIsland(p, a, b) {
			return nil, fmt.Errorf("unbounded channel attachment lost physical connection")
		}
	}
	return &SchematicLayoutResult{Placements: p.Placements, Wires: p.Wires, Flags: p.Flags,
		Score: libCandidateScore(p), Search: &SchematicLayoutSearchDiagnostics{Strategy: "unbounded-channels-v1"}}, nil
}
