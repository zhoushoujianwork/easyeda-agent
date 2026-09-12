package app

import (
	"fmt"
	"math"
)

func libPinSide(p powerLayoutPin, b layoutBBox) (string, error) {
	var found []string
	for _, d := range []string{"left", "right", "up", "down"} {
		if schTerminalPointsOutward(p, b, d) {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("pin %s has ambiguous/unknown measured outward side", p.Number)
	}
	return found[0], nil
}
func libPin(c powerLayoutPlacement, n string) (powerLayoutPin, bool) {
	for _, p := range c.Pins {
		if p.Number == n {
			return p, true
		}
	}
	return powerLayoutPin{}, false
}

// Bounded route alternatives: straight, then the two one-bend Manhattan paths.
func libRoutes(a, b powerLayoutPin) [][]powerLayoutWire {
	if a.Net == "" || a.Net != b.Net {
		return nil
	}
	if a.X == b.X && a.Y == b.Y {
		return [][]powerLayoutWire{{}}
	}
	wire := func(x, y [2]float64) powerLayoutWire { return powerLayoutWire{Net: a.Net, Points: [][2]float64{x, y}} }
	s, t := [2]float64{a.X, a.Y}, [2]float64{b.X, b.Y}
	if a.X == b.X || a.Y == b.Y {
		return [][]powerLayoutWire{{wire(s, t)}}
	}
	m1, m2 := [2]float64{b.X, a.Y}, [2]float64{a.X, b.Y}
	return [][]powerLayoutWire{{wire(s, m1), wire(m1, t)}, {wire(s, m2), wire(m2, t)}}
}

// Bounded two-bend alternatives; the full geometry validator rejects inward paths.
func libDetourRoutes(a, b powerLayoutPin) [][]powerLayoutWire {
	if a.Net == "" || a.Net != b.Net || (a.X == b.X && a.Y == b.Y) {
		return nil
	}
	var routes [][]powerLayoutWire
	add := func(points ...[2]float64) {
		var route []powerLayoutWire
		for i := 1; i < len(points); i++ {
			if points[i-1] != points[i] {
				route = append(route, powerLayoutWire{Net: a.Net, Points: [][2]float64{points[i-1], points[i]}})
			}
		}
		routes = append(routes, route)
	}
	s, t := [2]float64{a.X, a.Y}, [2]float64{b.X, b.Y}
	for d := 5.0; d <= 80; d += 5 {
		for _, x := range []float64{math.Min(a.X, b.X) - d, math.Max(a.X, b.X) + d} {
			add(s, [2]float64{x, a.Y}, [2]float64{x, b.Y}, t)
		}
		for _, y := range []float64{math.Min(a.Y, b.Y) - d, math.Max(a.Y, b.Y) + d} {
			add(s, [2]float64{a.X, y}, [2]float64{b.X, y}, t)
		}
	}
	return routes
}

// Marker leads may branch off the already connected same-net tree.
func libPlaceMarker(p *powerLayoutPlan, q powerLayoutPin, kind string) bool {
	var body layoutBBox
	for _, c := range p.Placements {
		for _, cp := range c.Pins {
			if cp == q {
				body = c.BBox
			}
		}
	}
	side, e := libPinSide(q, body)
	if e != nil {
		return false
	}
	directions := []string{side, "up", "down", "left", "right"}
	if kind == "ground" {
		directions = []string{"down", side, "left", "right", "up"}
	}
	if kind == "power" {
		directions = []string{"up", side, "left", "right", "down"}
	}
	return libPlaceMarkerAt(p, q, kind, directions, libMarkerOffsetCap(p, q.Net, kind))
}

func libMarkerOffsetCap(p *powerLayoutPlan, net, kind string) float64 {
	cap := 300.0
	if isNetPortKind(kind) {
		span := acPortTotalLen(net)
		for _, f := range p.Flags {
			if isNetPortKind(f.Kind) {
				span = math.Max(span, acPortTotalLen(f.Net))
			}
		}
		cap = math.Min(cap, plCeil(10+2*span))
	}
	return cap
}

func libPlaceMarkerAt(p *powerLayoutPlan, q powerLayoutPin, kind string, directions []string, cap float64) bool {
	segments, e := schTerminalSegments(p)
	if e != nil {
		return false
	}
	for offset := 10.0; offset <= cap; offset += 5 {
		seen := map[string]bool{}
		for _, direction := range directions {
			if seen[direction] {
				continue
			}
			seen[direction] = true
			f := powerLayoutFlag{Net: q.Net, Kind: kind, PinX: q.X, PinY: q.Y, Direction: direction, Offset: offset}
			if libMarkerRetraces(f, segments) {
				continue
			}
			if schTerminalCandidate(p, f, segments) == nil {
				candidate := *p
				candidate.Flags = append(append([]powerLayoutFlag(nil), p.Flags...), f)
				if validateLibGeometry(&candidate) == nil {
					p.Flags = candidate.Flags
					return true
				}
			}
		}
	}
	return false
}

// A naming branch can meet a wire at one point, but must not redraw a
// positive-length part of it (otherwise the marker hides the real junction).
func libMarkerRetraces(f powerLayoutFlag, segments []powerLayoutWire) bool {
	x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
	a, b := [2]float64{f.PinX, f.PinY}, [2]float64{x, y}
	for _, w := range segments {
		if w.Net != f.Net {
			continue
		}
		for i := 1; i < len(w.Points); i++ {
			c, d := w.Points[i-1], w.Points[i]
			axis := -1
			if a[1] == b[1] && a[1] == c[1] && a[1] == d[1] {
				axis = 0
			}
			if a[0] == b[0] && a[0] == c[0] && a[0] == d[0] {
				axis = 1
			}
			if axis >= 0 && math.Max(math.Min(a[axis], b[axis]), math.Min(c[axis], d[axis])) < math.Min(math.Max(a[axis], b[axis]), math.Max(c[axis], d[axis])) {
				return true
			}
		}
	}
	return false
}

// Symmetric taps only on a proved straight connection between two facing
// two-terminal devices. It neither invents net membership nor extends the wire.
func libFacingTwoTerminalPins(p *powerLayoutPlan, a, b powerLayoutPin) bool {
	var ac, bc *powerLayoutPlacement
	for i := range p.Placements {
		c := &p.Placements[i]
		if len(c.Pins) != 2 {
			continue
		}
		for _, q := range c.Pins {
			if q == a {
				ac = c
			}
			if q == b {
				bc = c
			}
		}
	}
	if ac == nil || bc == nil || ac == bc || a.Net == "" || a.Net != b.Net {
		return false
	}
	as, _ := libPinSide(a, ac.BBox)
	bs, _ := libPinSide(b, bc.BBox)
	return (a.X == b.X && ((a.Y < b.Y && as == "up" && bs == "down") || (a.Y > b.Y && as == "down" && bs == "up"))) || (a.Y == b.Y && ((a.X < b.X && as == "right" && bs == "left") || (a.X > b.X && as == "left" && bs == "right")))
}

func libPlaceMidpointMarker(p *powerLayoutPlan, island libIsland, kind string) bool {
	if kind != "net_port_bi" {
		return false
	}
	for i, a := range p.Placements {
		if len(a.Pins) != 2 {
			continue
		}
		for _, b := range p.Placements[:i] {
			if len(b.Pins) != 2 {
				continue
			}
			for _, ap := range a.Pins {
				for _, bp := range b.Pins {
					if ap.Net != island.net || bp.Net != island.net {
						continue
					}
					memberA, memberB := false, false
					for _, q := range island.pins {
						memberA = memberA || q == ap
						memberB = memberB || q == bp
					}
					if !memberA || !memberB {
						continue
					}
					as, _ := libPinSide(ap, a.BBox)
					bs, _ := libPinSide(bp, b.BBox)
					vertical := ap.X == bp.X && ap.Y != bp.Y && ((ap.Y < bp.Y && as == "up" && bs == "down") || (ap.Y > bp.Y && as == "down" && bs == "up"))
					horizontal := ap.Y == bp.Y && ap.X != bp.X && ((ap.X < bp.X && as == "right" && bs == "left") || (ap.X > bp.X && as == "left" && bs == "right"))
					if !vertical && !horizontal {
						continue
					}
					q := powerLayoutPin{Net: island.net, X: (ap.X + bp.X) / 2, Y: (ap.Y + bp.Y) / 2}
					if !plGrid(q.X) || !plGrid(q.Y) {
						continue
					}
					connected := false
					for _, w := range p.Wires {
						if w.Net == island.net && plOnSegment([2]float64{ap.X, ap.Y}, w.Points[0], w.Points[1]) && plOnSegment([2]float64{bp.X, bp.Y}, w.Points[0], w.Points[1]) {
							connected = true
						}
					}
					if !connected {
						continue
					}
					dirs := []string{"right", "left"}
					if horizontal {
						dirs = []string{"up", "down"}
					}
					if libPlaceMarkerAt(p, q, kind, dirs, 80) {
						return true
					}
				}
			}
		}
	}
	return false
}

// Merge same-net collinear intervals, including a new segment that bridges two
// old ones. Rebuild slices so searching a candidate cannot mutate its parent.
func libAppendRoute(existing, route []powerLayoutWire) []powerLayoutWire {
	out := make([]powerLayoutWire, len(existing))
	for i, w := range existing {
		out[i] = w
		out[i].Points = append([][2]float64(nil), w.Points...)
	}
	for _, w := range route {
		w.Points = append([][2]float64(nil), w.Points...)
		for i := 0; i < len(out); {
			old := out[i]
			if old.Net == w.Net && len(w.Points) == 2 && len(old.Points) == 2 {
				a, b, c, d := w.Points[0], w.Points[1], old.Points[0], old.Points[1]
				horizontal := a[1] == b[1] && a[1] == c[1] && a[1] == d[1]
				vertical := a[0] == b[0] && a[0] == c[0] && a[0] == d[0]
				if (horizontal || vertical) && plSegmentsMeet(a, b, c, d) {
					axis := 0
					if vertical {
						axis = 1
					}
					lo, hi := a, a
					for _, point := range [][2]float64{b, c, d} {
						if point[axis] < lo[axis] {
							lo = point
						}
						if point[axis] > hi[axis] {
							hi = point
						}
					}
					w.Points = [][2]float64{lo, hi}
					out = append(out[:i], out[i+1:]...)
					i = 0 // The extended interval may now reach an earlier segment.
					continue
				}
			}
			i++
		}
		out = append(out, w)
	}
	return out
}
