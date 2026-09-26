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

// ValidateWireCrossingInventory checks that the same single-page response
// accounts for every component, marker and wire primitive. It does not prove
// electrical connectivity or grant any geometry exemption by itself.
func ValidateWireCrossingInventory(result map[string]any) error {
	reject := func(why string) error {
		return fmt.Errorf("wire crossing inventory unavailable: %s", why)
	}
	if result["wiresAvailable"] != true || result["allPages"] == true {
		return reject("complete single-page wire inventory not proved")
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
		switch kind {
		case "part", "sheet", "netflag", "netport", "netlabel":
			kinds[kind]++
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
	wires, ok := array(result["wires"])
	if !ok {
		return reject("wires missing")
	}
	wireIDs := map[string]bool{}
	for _, v := range wires {
		w, ok := v.(map[string]any)
		if !ok {
			return reject("invalid wire")
		}
		id, _ := w["primitiveId"].(string)
		if id == "" || ids[id] {
			return reject("missing/conflicting wire ID")
		}
		wireIDs[id] = true
	}
	n, ok := numeric(summary["wires"])
	if !ok || n != float64(len(wireIDs)) {
		return reject("wire inventory mismatch")
	}
	_, _, err := completeRawWireSegments(wires)
	return err
}

// observedRawCoordinates mirrors the connector's parseObservedWireLine contract.
// Only its three explicit encodings are supported; never infer an authored
// polyline from the parity of an ambiguous flat coordinate array.
func observedRawCoordinates(w map[string]any) ([]float64, error) {
	bad := fmt.Errorf("invalid or unknown original wire encoding/line")
	line, ok := array(w["rawLine"])
	if !ok || len(line) == 0 {
		return nil, bad
	}
	encoding, _ := w["rawEncoding"].(string)
	if encoding == "flat-segments" {
		if len(line)%4 != 0 {
			return nil, bad
		}
		coords := make([]float64, len(line))
		for i, v := range line {
			n, ok := numeric(v)
			if !ok {
				return nil, bad
			}
			coords[i] = n
		}
		return coords, nil
	}
	width := 4
	if encoding == "nested-polyline" {
		width = 2
		if len(line) < 2 {
			return nil, bad
		}
	} else if encoding != "nested-segments" {
		return nil, bad
	}
	rows := make([][]float64, len(line))
	for i, value := range line {
		row, ok := array(value)
		if !ok || len(row) != width {
			return nil, bad
		}
		rows[i] = make([]float64, width)
		for j, value := range row {
			n, ok := numeric(value)
			if !ok {
				return nil, bad
			}
			rows[i][j] = n
		}
	}
	var coords []float64
	for i, row := range rows {
		if width == 4 {
			coords = append(coords, row...)
		} else if i+1 < len(rows) {
			coords = append(coords, row...)
			coords = append(coords, rows[i+1]...)
		}
	}
	return coords, nil
}

// completeRawWireSegments accounts for every original segment record.
// Known diagonal/zero-length geometry is complete inventory, but never proves
// a bare orthogonal X. Missing/duplicate indices or mismatched raw data do not.
func completeRawWireSegments(raw []any) ([]segment, []string, error) {
	reject := func(why string) ([]segment, []string, error) {
		return nil, nil, fmt.Errorf("wire crossing inventory unavailable: %s", why)
	}
	var segments []segment
	var nets []string
	rawByID := map[string][]float64{}
	encodingByID := map[string]string{}
	indices := map[string]map[int]bool{}
	for _, v := range raw {
		w, ok := v.(map[string]any)
		if !ok {
			return reject("invalid wire")
		}
		id, _ := w["primitiveId"].(string)
		if id == "" {
			return reject("original wire identity missing")
		}
		coords, err := observedRawCoordinates(w)
		if err != nil {
			return reject(err.Error())
		}
		encoding := w["rawEncoding"].(string)
		if prior, ok := rawByID[id]; ok {
			if encodingByID[id] != encoding || len(prior) != len(coords) {
				return reject("inconsistent raw line")
			}
			for i := range coords {
				if prior[i] != coords[i] {
					return reject("inconsistent raw line")
				}
			}
		} else {
			rawByID[id] = coords
			encodingByID[id] = encoding
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
		net, _ := w["net"].(string)
		segments = append(segments, segment{a, b, id, i})
		nets = append(nets, strings.TrimSpace(net))
	}
	for id, line := range rawByID {
		if len(indices[id]) != len(line)/4 {
			return reject("raw segments omitted")
		}
	}
	return segments, nets, nil
}

// VerifiedWireCrossings does not consume check findings or expected net names.
// It requires actual pin netlist provenance, all markers, and every original
// flat-segment record. Missing/conflicting evidence never grants an exception.
func VerifiedWireCrossings(result map[string]any) (*WireCrossingProof, error) {
	reject := func(why string) (*WireCrossingProof, error) {
		return nil, fmt.Errorf("wire crossing evidence unavailable: %s", why)
	}
	if err := ValidateWireCrossingInventory(result); err != nil {
		return nil, err
	}
	if result["pinNetsAvailable"] != true {
		return reject("complete pin netlist not proved")
	}
	comps, _ := array(result["components"])
	type witness struct {
		at  Point
		net string
		pin bool
	}
	var witnesses []witness
	var anchors []Point
	for _, v := range comps {
		c, ok := v.(map[string]any)
		if !ok {
			return reject("invalid component")
		}
		kind, _ := c["componentType"].(string)
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
	raw, ok := array(result["wires"])
	if !ok {
		return reject("wires missing")
	}
	for _, v := range raw {
		if v.(map[string]any)["rawEncoding"] != "flat-segments" {
			return reject("crossing proof requires original flat-segment encoding")
		}
	}
	segments, nets, err := completeRawWireSegments(raw)
	if err != nil {
		return nil, err
	}
	for _, s := range segments {
		if same(s.a, s.b) || (math.Abs(s.a.X-s.b.X) > epsilon && math.Abs(s.a.Y-s.b.Y) > epsilon) {
			return reject("zero/non-orthogonal segment")
		}
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
