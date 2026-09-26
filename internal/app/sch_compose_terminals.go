package app

import (
	"fmt"
	"github.com/zhoushoujianwork/easyeda-agent/internal/schguard"
	"strings"
)

// A terminal names an existing physical pin. Its net and coordinates always
// come from the validated component model, never from a second connection map.
type schCompositionTerminal struct {
	Designator string `json:"designator"`
	Pin        string `json:"pin"`
	Direction  string `json:"direction"`
	Kind       string `json:"kind,omitempty"`
}

// planSchCompositionTerminals tries only straight, outward stubs. Input order
// breaks ties; each terminal gets its shortest legal 5-grid length. Work on a
// private flag slice so a later failure cannot leave a partially planned input.
func planSchCompositionTerminals(p *powerLayoutPlan, terminals []schCompositionTerminal) error {
	if p == nil {
		return fmt.Errorf("terminal plan is nil")
	}
	if len(terminals) == 0 {
		return nil
	}
	type pinKey struct{ ref, pin string }
	type measuredPin struct {
		part powerLayoutPlacement
		pin  powerLayoutPin
	}
	pins := map[pinKey]measuredPin{}
	refs := map[string]bool{}
	for _, c := range p.Placements {
		if c.Designator == "" || refs[c.Designator] || !plBoxValid(c.BBox) {
			return fmt.Errorf("terminal plan has invalid/duplicate component %q", c.Designator)
		}
		refs[c.Designator] = true
		for _, q := range c.Pins {
			k := pinKey{c.Designator, q.Number}
			if _, duplicate := pins[k]; duplicate || q.Number == "" || !plGrid(q.X) || !plGrid(q.Y) {
				return fmt.Errorf("terminal plan has invalid/duplicate pin %s.%s", c.Designator, q.Number)
			}
			pins[k] = measuredPin{c, q}
		}
	}
	work := *p
	work.Flags = append([]powerLayoutFlag(nil), p.Flags...)
	seen := map[pinKey]bool{}
	for _, terminal := range terminals {
		key := pinKey{terminal.Designator, terminal.Pin}
		measured, ok := pins[key]
		if !ok || seen[key] {
			return fmt.Errorf("terminal %s.%s is unknown or duplicated", key.ref, key.pin)
		}
		seen[key] = true
		q := measured.pin
		if strings.TrimSpace(q.Net) == "" {
			return fmt.Errorf("terminal %s.%s has NC/unknown net", key.ref, key.pin)
		}
		kind := terminal.Kind
		if kind == "" {
			kind = "net_port_bi"
		}
		switch kind {
		case "net_port_in", "net_port_out", "net_port_bi", "power", "ground":
		default:
			return fmt.Errorf("terminal %s.%s has unsupported kind %q", key.ref, key.pin, kind)
		}
		if !schTerminalPointsOutward(q, measured.part.BBox, terminal.Direction) {
			return fmt.Errorf("terminal %s.%s direction %q is not outward from its measured body", key.ref, key.pin, terminal.Direction)
		}
		segments, err := schTerminalSegments(&work)
		if err != nil {
			return err
		}
		start := [2]float64{q.X, q.Y}
		for _, s := range segments {
			if plOnSegment(start, s.Points[0], s.Points[1]) {
				return fmt.Errorf("terminal %s.%s already touches an existing wire/marker lead", key.ref, key.pin)
			}
		}
		var lastErr error
		placed := false
		for offset := 10.0; offset <= 300; offset += 5 {
			f := powerLayoutFlag{Net: q.Net, Kind: kind, PinX: q.X, PinY: q.Y, Direction: terminal.Direction, Offset: offset}
			if lastErr = schTerminalCandidate(&work, f, segments); lastErr != nil {
				continue
			}
			work.Flags = append(work.Flags, f)
			placed = true
			break
		}
		if !placed {
			return fmt.Errorf("terminal %s.%s has no legal straight %s stub at 10..300 raw: %w", key.ref, key.pin, terminal.Direction, lastErr)
		}
	}
	p.Flags = work.Flags
	return nil
}

// Measured pins outside the body directly establish their side. For a pin just
// inside the measurement halo, accept only its nearest edge (and outward half
// of the body). This classifies direction only: body collision stays strict.
func schTerminalPointsOutward(q powerLayoutPin, b layoutBBox, direction string) bool {
	if q.Rotation != nil {
		x, y, ok := schguard.CardinalOutward(*q.Rotation)
		if !ok {
			return false
		}
		return (direction == "right" && x > 0) || (direction == "left" && x < 0) || (direction == "up" && y > 0) || (direction == "down" && y < 0)
	}
	distances := map[string]float64{"left": q.X - b.MinX, "right": b.MaxX - q.X, "up": b.MaxY - q.Y, "down": q.Y - b.MinY}
	distance, ok := distances[direction]
	if !ok {
		return false
	}
	if distance <= 0 {
		return true
	}
	if distance > 0.5 {
		return false
	}
	for _, other := range distances {
		if other < distance-1e-7 {
			return false
		}
	}
	switch direction {
	case "left":
		return q.X < (b.MinX+b.MaxX)/2
	case "right":
		return q.X > (b.MinX+b.MaxX)/2
	case "up":
		return q.Y > (b.MinY+b.MaxY)/2
	case "down":
		return q.Y < (b.MinY+b.MaxY)/2
	}
	return false
}

func schTerminalSegments(p *powerLayoutPlan) ([]powerLayoutWire, error) {
	segments := append([]powerLayoutWire(nil), p.Wires...)
	for _, f := range p.Flags {
		switch f.Kind {
		case "net_port_in", "net_port_out", "net_port_bi", "power", "ground":
		default:
			return nil, fmt.Errorf("terminal plan contains unsupported marker kind %q", f.Kind)
		}
		if oppositeDirection(f.Direction) == "" || !plGrid(f.Offset) || f.Offset <= 0 {
			return nil, fmt.Errorf("terminal plan contains invalid marker direction/offset")
		}
		x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
		segments = append(segments, powerLayoutWire{Net: f.Net, Points: [][2]float64{{f.PinX, f.PinY}, {x, y}}})
	}
	for _, s := range segments {
		if len(s.Points) != 2 || strings.TrimSpace(s.Net) == "" {
			return nil, fmt.Errorf("terminal plan requires named single wire segments")
		}
		a, b := s.Points[0], s.Points[1]
		if a == b || (a[0] != b[0] && a[1] != b[1]) || !plGrid(a[0]) || !plGrid(a[1]) || !plGrid(b[0]) || !plGrid(b[1]) {
			return nil, fmt.Errorf("terminal plan contains nonorthogonal/off-grid wire")
		}
	}
	return segments, nil
}

func schTerminalMarkerBoxes(f powerLayoutFlag) []layoutBBox {
	x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
	body := predictedMarkerBody(x, y, f.Kind, f.Direction, f.Net)
	boxes := []layoutBBox{body}
	if text := predictedFlagTextBand(x, y, body, f.Kind, f.Direction, f.Net); text != nil {
		boxes = append(boxes, *text)
	}
	return boxes
}

func schTerminalCandidate(p *powerLayoutPlan, f powerLayoutFlag, segments []powerLayoutWire) error {
	x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
	a, b := [2]float64{f.PinX, f.PinY}, [2]float64{x, y}
	boxes := schTerminalMarkerBoxes(f)
	for _, c := range p.Placements {
		if plSegmentBox(a, b, c.BBox) {
			libRecordNamingBlocker(p, c.Designator)
			return fmt.Errorf("lead crosses %s body", c.Designator)
		}
		for _, box := range boxes {
			if boxesGapOverlap(box, c.BBox, 5) {
				libRecordNamingBlocker(p, c.Designator)
				return fmt.Errorf("marker body/text is too close to %s", c.Designator)
			}
		}
		for _, pin := range c.Pins {
			if pin.Net != f.Net && plOnSegment([2]float64{pin.X, pin.Y}, a, b) {
				libRecordNamingBlocker(p, c.Designator)
				return fmt.Errorf("lead touches NC/foreign pin %s.%s", c.Designator, pin.Number)
			}
			point := layoutBBox{MinX: pin.X, MaxX: pin.X, MinY: pin.Y, MaxY: pin.Y}
			for _, box := range boxes {
				if boxesGapOverlap(box, point, 5) {
					libRecordNamingBlocker(p, c.Designator)
					return fmt.Errorf("marker body/text is too close to pin %s.%s", c.Designator, pin.Number)
				}
			}
		}
	}
	for _, s := range segments {
		if s.Net != f.Net && plSegmentsContact(a, b, s.Points[0], s.Points[1]) {
			libRecordNamingWireBlockers(p, s)
			return fmt.Errorf("lead crosses foreign wire %s", s.Net)
		}
	}
	for _, existing := range p.Flags {
		for _, box := range boxes {
			for _, other := range schTerminalMarkerBoxes(existing) {
				if boxesGapOverlap(box, other, 5) {
					return fmt.Errorf("marker body/text is too close to %s", existing.Net)
				}
			}
		}
	}
	// Wire/marker checks use actual segments, without a 5-unit wire envelope:
	// the adjacent pin's straight lead can pass beside an 11-high text band.
	candidate := *p
	candidate.Flags = append(append([]powerLayoutFlag(nil), p.Flags...), f)
	if _, err := compositionMarkerGeometry(&candidate); err != nil {
		return err
	}
	// Retain finite bounds even when the marker's name is exceptionally long.
	for _, box := range boxes {
		if !plBoxValid(box) {
			return fmt.Errorf("invalid predicted marker geometry")
		}
	}
	return nil
}

// Probe-local evidence records actual rejected marker/body or pin geometry.
// It does not claim that these objects block every possible lead.
func libRecordNamingBlocker(p *powerLayoutPlan, ref string) {
	if p.namingBlockers != nil && ref != "" {
		p.namingBlockers[ref] = true
	}
}

// Attribute the specific rejected wire to its real physical island, rather
// than treating every component carrying the same net text as a blocker.
func libRecordNamingWireBlockers(p *powerLayoutPlan, wire powerLayoutWire) {
	if p.namingBlockers == nil || len(wire.Points) != 2 {
		return
	}
	for _, island := range libIslands(p) {
		if island.net != wire.Net {
			continue
		}
		contact := false
		for _, pin := range island.pins {
			contact = contact || plOnSegment([2]float64{pin.X, pin.Y}, wire.Points[0], wire.Points[1])
		}
		for _, index := range island.wireIndices {
			actual := p.Wires[index]
			contact = contact || plSegmentsContact(actual.Points[0], actual.Points[1], wire.Points[0], wire.Points[1])
		}
		if contact {
			for _, pin := range island.pins {
				libRecordNamingBlocker(p, libExactPinOwner(p, pin))
			}
		}
	}
}
