// Package schguard provides deterministic, I/O-free schematic execution guards.
package schguard

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

const epsilon = 1e-6

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type BBox struct {
	MinX float64 `json:"minX"`
	MinY float64 `json:"minY"`
	MaxX float64 `json:"maxX"`
	MaxY float64 `json:"maxY"`
}
type Finding struct {
	Type            string   `json:"type"`
	Level           string   `json:"level"`
	Designator      string   `json:"designator,omitempty"`
	PrimitiveId     string   `json:"primitiveId,omitempty"`
	WirePrimitiveId string   `json:"wirePrimitiveId,omitempty"`
	Pins            []string `json:"pins,omitempty"`
	Message         string   `json:"message"`
	At              *Point   `json:"at,omitempty"`
	BBox            *BBox    `json:"bbox,omitempty"`
	Segment         []Point  `json:"segment,omitempty"`
}

type segment struct {
	a, b  Point
	id    string
	index int
}

// WireCoverageError means the observed inventory was parseable but did not yet
// cover a proposed segment. It does not classify missing/malformed evidence as
// publication delay, and does not authorize another mutation.
type WireCoverageError struct{ Message string }

func (e *WireCoverageError) Error() string { return e.Message }

// VerifyWirePresent proves every proposed nonzero orthogonal segment is covered
// by actual wire geometry. EDA may reverse, split, merge or re-ID primitives;
// interval coverage, not primitive identity or equal endpoints, is authoritative.
// This verifies wire landing only, not electrical net naming or marker presence.
func VerifyWirePresent(result map[string]any, proposed map[string]any) error {
	if available, known := result["wiresAvailable"].(bool); known && !available {
		return fmt.Errorf("wire inventory unavailable")
	}
	raw, ok := array(result["wires"])
	if !ok {
		return fmt.Errorf("wire inventory missing")
	}
	points, ok := wirePoints(proposed)
	if !ok {
		return fmt.Errorf("invalid proposed wire geometry")
	}
	var actual []segment
	for i, v := range raw {
		w, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid actual wire %d", i)
		}
		p, ok := wirePoints(w)
		if !ok {
			return fmt.Errorf("invalid actual wire coordinates %d", i)
		}
		for j := 1; j < len(p); j++ {
			actual = append(actual, segment{a: p[j-1], b: p[j]})
		}
	}
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		if same(a, b) {
			return fmt.Errorf("proposed segment %d has zero length", i-1)
		}
		horizontal := math.Abs(a.Y-b.Y) <= epsilon
		if !horizontal && math.Abs(a.X-b.X) > epsilon {
			return fmt.Errorf("proposed segment %d is non-orthogonal", i-1)
		}
		coordinate, lo, hi := a.Y, math.Min(a.X, b.X), math.Max(a.X, b.X)
		if !horizontal {
			coordinate, lo, hi = a.X, math.Min(a.Y, b.Y), math.Max(a.Y, b.Y)
		}
		var intervals [][2]float64
		for _, s := range actual {
			c, d, x, y := s.a.Y, s.b.Y, s.a.X, s.b.X
			if !horizontal {
				c, d, x, y = s.a.X, s.b.X, s.a.Y, s.b.Y
			}
			if math.Abs(c-coordinate) <= epsilon && math.Abs(d-coordinate) <= epsilon {
				intervals = append(intervals, [2]float64{math.Min(x, y), math.Max(x, y)})
			}
		}
		sort.Slice(intervals, func(i, j int) bool { return intervals[i][0] < intervals[j][0] })
		covered := lo
		for _, iv := range intervals {
			if iv[1] < covered-epsilon {
				continue
			}
			if iv[0] > covered+epsilon {
				break
			}
			covered = math.Max(covered, iv[1])
			if covered >= hi-epsilon {
				break
			}
		}
		if covered < hi-epsilon {
			return &WireCoverageError{Message: fmt.Sprintf("proposed wire segment %d (%g,%g)→(%g,%g) is not fully present in readback (uncovered coordinate %g..%g)", i-1, a.X, a.Y, b.X, b.Y, covered, hi)}
		}
	}
	return nil
}

// AnalyzeWireGeometry consumes an official components.list snapshot requested
// with includePins/includeBBox/includeWires. Pin rotation is the measured WORLD
// outward direction (0=right, 90=up, 180=left, 270=down); component rotation and
// mirror must NOT be applied again. Missing evidence is an ERROR, never zero
// collisions. Net equality does not exempt a wire from visual safety constraints.
// Wires accept official x0/y0/x1/y1 segments, or points polylines (flat or XY pairs).
func AnalyzeWireGeometry(result map[string]any) []Finding {
	var out []Finding
	missing := func(ref, id, detail string) {
		out = append(out, Finding{Type: "wire-geometry-unverified", Level: "ERROR", Designator: ref, PrimitiveId: id, Message: detail})
	}
	components, ok := array(result["components"])
	if !ok {
		missing("", "", "components collection is missing or invalid")
		return out
	}
	if available, known := result["wiresAvailable"].(bool); known && !available {
		missing("", "", "wire collection read failed; wiresAvailable=false")
		return out
	}
	wires, ok := array(result["wires"])
	if !ok {
		missing("", "", "wire geometry collection is missing or invalid")
		return out
	}
	var segments []segment
	for i, value := range wires {
		w, ok := value.(map[string]any)
		if !ok {
			missing("", "", fmt.Sprintf("wire[%d] is invalid", i))
			continue
		}
		id, _ := w["primitiveId"].(string)
		if id == "" {
			id, _ = w["id"].(string)
		}
		if id == "" {
			id = fmt.Sprintf("wire[%d]", i)
		}
		points, valid := wirePoints(w)
		if !valid {
			missing("", id, "wire coordinates are missing, non-finite, or malformed")
			continue
		}
		for j := 1; j < len(points); j++ {
			a, b := points[j-1], points[j]
			if same(a, b) {
				out = append(out, Finding{Type: "zero-length-wire", Level: "ERROR", WirePrimitiveId: id, At: &a, Segment: []Point{a, b}, Message: "wire contains a zero-length segment"})
				continue
			}
			if math.Abs(a.X-b.X) > epsilon && math.Abs(a.Y-b.Y) > epsilon {
				out = append(out, Finding{Type: "non-orthogonal-wire", Level: "ERROR", WirePrimitiveId: id, At: &a, Segment: []Point{a, b}, Message: "wire segments must be orthogonal"})
			}
			segments = append(segments, segment{a, b, id, j - 1})
		}
	}
	for _, value := range components {
		c, ok := value.(map[string]any)
		if !ok {
			missing("", "", "invalid component record")
			continue
		}
		kind, _ := c["componentType"].(string)
		if kind == "" {
			missing("", "", "component type is missing; cannot determine body-check scope")
			continue
		}
		if kind != "part" {
			continue
		}
		ref, _ := c["designator"].(string)
		id, _ := c["primitiveId"].(string)
		box, valid := bbox(c["bbox"])
		if !valid {
			missing(ref, id, "component body bbox is missing or invalid")
		} else {
			for _, s := range segments {
				if throughInterior(s.a, s.b, box) && !componentPinHaloExit(c, s.a, s.b, box) {
					out = append(out, Finding{Type: "wire-through-body", Level: "ERROR", Designator: ref, PrimitiveId: id, WirePrimitiveId: s.id, BBox: &box, At: &s.a, Segment: []Point{s.a, s.b}, Message: fmt.Sprintf("wire segment %d (%g,%g)→(%g,%g) enters component body", s.index, s.a.X, s.a.Y, s.b.X, s.b.Y)})
				}
			}
		}
		pins, valid := array(c["pins"])
		available, hasAvailable := c["pinsAvailable"].(bool)
		if !valid || (hasAvailable && !available) {
			missing(ref, id, "component pins could not be measured")
			continue
		}
		for _, value := range pins {
			p, ok := value.(map[string]any)
			if !ok {
				missing(ref, id, "invalid pin record")
				continue
			}
			number, _ := p["pinNumber"].(string)
			if number == "" {
				number, _ = p["number"].(string)
			}
			x, xok := numeric(p["x"])
			y, yok := numeric(p["y"])
			if number == "" || !xok || !yok {
				missing(ref, id, "pin identity or coordinates missing")
				continue
			}
			at := Point{x, y}
			var incident []segment
			for _, s := range segments {
				if onSegment(at, s.a, s.b) {
					incident = append(incident, s)
				}
			}
			if len(incident) == 0 {
				continue
			} // no fanout exists to validate (NC/floating checked elsewhere)
			rotation, rok := numeric(p["rotation"])
			if !rok {
				missing(ref, id, "pin "+number+" has connected wire but no measured outward rotation")
				continue
			}
			rotation = math.Mod(math.Mod(rotation, 360)+360, 360)
			if math.Abs(rotation/90-math.Round(rotation/90)) > epsilon {
				missing(ref, id, "pin "+number+" has unsupported non-cardinal outward rotation")
				continue
			}
			rotation = math.Round(rotation/90) * 90
			for _, s := range incident {
				bad := false
				for _, end := range []Point{s.a, s.b} {
					if same(at, end) {
						continue
					}
					if !PinRayOutward(rotation, at, end) {
						bad = true
					}
				}
				if bad {
					out = append(out, Finding{Type: "pin-exit-direction", Level: "ERROR", Designator: ref, PrimitiveId: id, WirePrimitiveId: s.id, Pins: []string{number}, At: &at, Segment: []Point{s.a, s.b}, Message: fmt.Sprintf("pin %s outward rotation %g° requires its first wire segment to leave outward; segment %d (%g,%g)→(%g,%g) does not", number, rotation, s.index, s.a.X, s.a.Y, s.b.X, s.b.Y)})
				}
			}
		}
	}
	return out
}

func componentPinHaloExit(c map[string]any, a, b Point, box BBox) bool {
	pins, ok := array(c["pins"])
	if !ok {
		return false
	}
	for _, v := range pins {
		p, ok := v.(map[string]any)
		if !ok {
			continue
		}
		x, xo := numeric(p["x"])
		y, yo := numeric(p["y"])
		r, ro := numeric(p["rotation"])
		if !xo || !yo || !ro {
			continue
		}
		pin := Point{x, y}
		if (same(pin, a) && PinHaloExit(pin, b, box, r)) || (same(pin, b) && PinHaloExit(pin, a, box, r)) {
			return true
		}
	}
	return false
}

func numeric(v any) (float64, bool) {
	var n float64
	switch v := v.(type) {
	case float64:
		n = v
	case float32:
		n = float64(v)
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case json.Number:
		var err error
		n, err = v.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}
func array(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	if a, ok := v.([]any); ok {
		return a, true
	}
	if a, ok := v.([]map[string]any); ok {
		r := make([]any, len(a))
		for i, v := range a {
			r[i] = v
		}
		return r, true
	}
	return nil, false
}
func bbox(v any) (BBox, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return BBox{}, false
	}
	a, ao := numeric(m["minX"])
	b, bo := numeric(m["minY"])
	c, co := numeric(m["maxX"])
	d, do := numeric(m["maxY"])
	return BBox{a, b, c, d}, ao && bo && co && do && c > a && d > b
}
func wirePoints(w map[string]any) ([]Point, bool) {
	if raw, exists := w["points"]; exists {
		// Normalize typed slices from Go planners to the same JSON contract.
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, false
		}
		var a []any
		if json.Unmarshal(encoded, &a) != nil {
			return nil, false
		}
		var p []Point
		if len(a) > 0 {
			if _, nested := a[0].([]any); nested {
				for _, v := range a {
					pair, ok := v.([]any)
					if !ok || len(pair) != 2 {
						return nil, false
					}
					x, xo := numeric(pair[0])
					y, yo := numeric(pair[1])
					if !xo || !yo {
						return nil, false
					}
					p = append(p, Point{x, y})
				}
				return p, len(p) >= 2
			}
		}
		if len(a)%2 != 0 {
			return nil, false
		}
		for i := 0; i < len(a); i += 2 {
			x, xo := numeric(a[i])
			y, yo := numeric(a[i+1])
			if !xo || !yo {
				return nil, false
			}
			p = append(p, Point{x, y})
		}
		return p, len(p) >= 2
	}
	x, xo := numeric(w["x0"])
	y, yo := numeric(w["y0"])
	u, uo := numeric(w["x1"])
	v, vo := numeric(w["y1"])
	return []Point{{x, y}, {u, v}}, xo && yo && uo && vo
}
func same(a, b Point) bool { return math.Abs(a.X-b.X) <= epsilon && math.Abs(a.Y-b.Y) <= epsilon }
func onSegment(p, a, b Point) bool {
	dx, dy := b.X-a.X, b.Y-a.Y
	l := math.Hypot(dx, dy)
	return l > epsilon && math.Abs((p.X-a.X)*dy-(p.Y-a.Y)*dx) <= epsilon*l && p.X >= math.Min(a.X, b.X)-epsilon && p.X <= math.Max(a.X, b.X)+epsilon && p.Y >= math.Min(a.Y, b.Y)-epsilon && p.Y <= math.Max(a.Y, b.Y)+epsilon
}

// Slab clipping checks the OPEN body interior; boundary-only contact is not a
// penetration. Handles diagonal input too, so non-orthogonal wires cannot evade it.
func throughInterior(a, b Point, box BBox) bool {
	lo, hi := 0.0, 1.0
	for _, axis := range [][4]float64{{a.X, b.X - a.X, box.MinX + epsilon, box.MaxX - epsilon}, {a.Y, b.Y - a.Y, box.MinY + epsilon, box.MaxY - epsilon}} {
		if math.Abs(axis[1]) <= epsilon {
			if axis[0] <= axis[2] || axis[0] >= axis[3] {
				return false
			}
			continue
		}
		t, u := (axis[2]-axis[0])/axis[1], (axis[3]-axis[0])/axis[1]
		if t > u {
			t, u = u, t
		}
		lo = math.Max(lo, t)
		hi = math.Min(hi, u)
		if hi <= lo {
			return false
		}
	}
	return hi > lo
}
