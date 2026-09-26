package schguard

import (
	"fmt"
	"math"
	"strings"
)

// physicalSegmentRoots is the shared observed-wire island rule. Original
// endpoints/T/collinear contacts conduct. A bare interior X does not; an actual
// pin/marker at the X joins its incident segments. Never normalize observed ends.
func physicalSegmentRoots(segments []segment, anchors []Point) []int {
	parent := make([]int, len(segments))
	for i := range parent {
		parent[i] = i
	}
	var root func(int) int
	root = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i, s := range segments {
		for j, t := range segments[:i] {
			contact := SegmentsContact(s.a, s.b, t.a, t.b)
			if !contact {
				for _, p := range anchors {
					if onSegment(p, s.a, s.b) && onSegment(p, t.a, t.b) {
						contact = true
						break
					}
				}
			}
			if contact {
				parent[root(i)] = root(j)
			}
		}
	}
	for i := range parent {
		parent[i] = root(i)
	}
	return parent
}

type crossingSegmentKey struct {
	ID   string
	A, B Point
}

func crossingKey(id string, a, b Point) crossingSegmentKey {
	if a.X > b.X || (a.X == b.X && a.Y > b.Y) {
		a, b = b, a
	}
	return crossingSegmentKey{id, a, b}
}

// WireCrossingProof contains only segment pairs proved safe from one complete
// fresh official snapshot. It cannot exempt a body, marker or a bbox by itself.
type WireCrossingProof struct {
	pairs map[[2]crossingSegmentKey]bool
}

func (p *WireCrossingProof) Allows(aID string, a, b Point, bID string, c, d Point) bool {
	if p == nil {
		return false
	}
	x, y := crossingKey(aID, a, b), crossingKey(bID, c, d)
	return p.pairs[[2]crossingSegmentKey{x, y}] || p.pairs[[2]crossingSegmentKey{y, x}]
}

// VerifiedWireCrossings does not consume check findings or expected net names.
// It requires actual pin netlist provenance, all markers, and every original
// flat-segment record. Missing/conflicting evidence never grants an exception.
func VerifiedWireCrossings(result map[string]any) (*WireCrossingProof, error) {
	reject := func(why string) (*WireCrossingProof, error) {
		return nil, fmt.Errorf("wire crossing evidence unavailable: %s", why)
	}
	if result["wiresAvailable"] != true || result["pinNetsAvailable"] != true || result["allPages"] == true {
		return reject("complete single-page wires/pin netlist not proved")
	}
	comps, ok := array(result["components"])
	if !ok {
		return reject("components missing")
	}
	count, countOK := numeric(result["count"])
	if !countOK || count != float64(len(comps)) {
		return reject("component count mismatch")
	}
	summary, ok := result["connectivitySummary"].(map[string]any)
	if !ok || summary["scope"] != "activePage" {
		return reject("active page inventory not proved")
	}
	for _, k := range []string{"buses", "shortSymbols"} {
		n, ok := numeric(summary[k])
		if !ok || n != 0 {
			return reject("unsupported or unknown " + k)
		}
	}
	type witness struct {
		at  Point
		net string
		pin bool
	}
	var witnesses []witness
	var anchors []Point
	kinds := map[string]int{}
	ids := map[string]bool{}
	for _, v := range comps {
		c, ok := v.(map[string]any)
		if !ok {
			return reject("invalid component")
		}
		id, _ := c["primitiveId"].(string)
		if id == "" || ids[id] {
			return reject("missing/duplicate component ID")
		}
		ids[id] = true
		kind, _ := c["componentType"].(string)
		kinds[kind]++
		switch kind {
		case "sheet":
			continue
		case "part":
			if c["pinsAvailable"] != true || c["netlistAvailable"] != true {
				return reject("part pin/netlist provenance missing")
			}
			if msg, _ := c["pinsError"].(string); msg != "" {
				return reject("part pin read failed")
			}
			if errs, ok := array(c["geometryErrors"]); ok && len(errs) > 0 {
				return reject("part geometry errors")
			}
			if _, valid := bbox(c["bbox"]); !valid {
				return reject("part bbox unavailable")
			}
			pins, ok := array(c["pins"])
			if !ok {
				return reject("pins missing")
			}
			for _, v := range pins {
				p, ok := v.(map[string]any)
				if !ok {
					return reject("invalid pin")
				}
				x, xok := numeric(p["x"])
				y, yok := numeric(p["y"])
				if !xok || !yok {
					return reject("pin coordinate unavailable")
				}
				n, _ := p["pinNumber"].(string)
				if n == "" {
					return reject("pin identity unavailable")
				}
				net, _ := p["net"].(string)
				at := Point{x, y}
				anchors = append(anchors, at)
				witnesses = append(witnesses, witness{at, strings.TrimSpace(net), true})
			}
		case "netflag", "netport", "netlabel":
			x, xok := numeric(c["x"])
			y, yok := numeric(c["y"])
			if !xok || !yok {
				return reject("marker anchor unavailable")
			}
			if _, valid := bbox(c["bbox"]); !valid {
				return reject("marker bbox unavailable")
			}
			net, _ := c["net"].(string)
			at := Point{x, y}
			anchors = append(anchors, at)
			witnesses = append(witnesses, witness{at, strings.TrimSpace(net), false})
		default:
			return reject("unknown component type")
		}
	}
	for kind, key := range map[string]string{"netflag": "netflags", "netport": "netports", "netlabel": "netlabels"} {
		n, ok := numeric(summary[key])
		if !ok || n != float64(kinds[kind]) {
			return reject("marker inventory mismatch")
		}
	}
	raw, ok := array(result["wires"])
	if !ok {
		return reject("wires missing")
	}
	var segments []segment
	var nets []string
	rawByID := map[string][]float64{}
	indices := map[string]map[int]bool{}
	for _, v := range raw {
		w, ok := v.(map[string]any)
		if !ok {
			return reject("invalid wire")
		}
		id, _ := w["primitiveId"].(string)
		if id == "" || w["rawEncoding"] != "flat-segments" {
			return reject("original wire encoding missing")
		}
		line, ok := array(w["rawLine"])
		if !ok || len(line) < 4 || len(line)%4 != 0 {
			return reject("original wire line missing")
		}
		coords := make([]float64, len(line))
		for i, v := range line {
			n, ok := numeric(v)
			if !ok {
				return reject("invalid raw coordinate")
			}
			coords[i] = n
		}
		if prior, ok := rawByID[id]; ok {
			if len(prior) != len(coords) {
				return reject("inconsistent raw line")
			}
			for i := range coords {
				if prior[i] != coords[i] {
					return reject("inconsistent raw line")
				}
			}
		} else {
			rawByID[id] = coords
			indices[id] = map[int]bool{}
		}
		index, ok := numeric(w["segmentIndex"])
		if !ok || index != math.Trunc(index) || index < 0 || index >= float64(len(coords)/4) {
			return reject("invalid raw segment index")
		}
		i := int(index)
		if indices[id][i] {
			return reject("duplicate segment")
		}
		indices[id][i] = true
		points, valid := wirePoints(w)
		if !valid || len(points) != 2 {
			return reject("invalid segment geometry")
		}
		a, b := points[0], points[1]
		if a != (Point{coords[4*i], coords[4*i+1]}) || b != (Point{coords[4*i+2], coords[4*i+3]}) {
			return reject("segment disagrees with raw line")
		}
		if same(a, b) || (math.Abs(a.X-b.X) > epsilon && math.Abs(a.Y-b.Y) > epsilon) {
			return reject("zero/non-orthogonal segment")
		}
		net, _ := w["net"].(string)
		segments = append(segments, segment{a, b, id, i})
		nets = append(nets, strings.TrimSpace(net))
	}
	for id, line := range rawByID {
		if len(indices[id]) != len(line)/4 {
			return reject("raw segments omitted")
		}
	}
	n, ok := numeric(summary["wires"])
	if !ok || n != float64(len(rawByID)) {
		return reject("wire inventory mismatch")
	}
	roots := physicalSegmentRoots(segments, anchors)
	type evidence struct {
		nets     map[string]bool
		pins     int
		complete bool
	}
	islands := map[int]*evidence{}
	for i, s := range segments {
		root := roots[i]
		e := islands[root]
		if e == nil {
			e = &evidence{nets: map[string]bool{}, complete: true}
			islands[root] = e
		}
		if nets[i] != "" {
			e.nets[nets[i]] = true
		}
		for _, w := range witnesses {
			if onSegment(w.at, s.a, s.b) {
				if w.pin {
					e.pins++
				}
				if w.net == "" {
					e.complete = false
				} else {
					e.nets[w.net] = true
				}
			}
		}
	}
	proof := &WireCrossingProof{pairs: map[[2]crossingSegmentKey]bool{}}
	for i, s := range segments {
		for j, t := range segments[:i] {
			if !SegmentsProperlyCross(s.a, s.b, t.a, t.b) || SegmentsContact(s.a, s.b, t.a, t.b) {
				continue
			}
			blocked := false
			for _, a := range anchors {
				if onSegment(a, s.a, s.b) && onSegment(a, t.a, t.b) {
					blocked = true
					break
				}
			}
			// Every original endpoint counts, including a third wire's endpoint at the X.
			for _, u := range segments {
				for _, a := range []Point{u.a, u.b} {
					if onSegment(a, s.a, s.b) && onSegment(a, t.a, t.b) {
						blocked = true
					}
				}
			}
			if blocked {
				continue
			}
			a, b := islands[roots[i]], islands[roots[j]]
			if !a.complete || !b.complete || a.pins == 0 || b.pins == 0 || len(a.nets) != 1 || len(b.nets) != 1 {
				continue
			}
			proof.pairs[[2]crossingSegmentKey{crossingKey(s.id, s.a, s.b), crossingKey(t.id, t.a, t.b)}] = true
		}
	}
	return proof, nil
}
