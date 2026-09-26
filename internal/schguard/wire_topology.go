package schguard

import "fmt"

// VerifyWireTopology compares physical contact partitions, separately from
// geometric coverage and electrical net names. An interior X is not a junction,
// even on the same named net. This catches an editor turning a proposed X into a
// T/connected X without changing its occupied geometric edges. It does not claim
// that a deliberately requested junction has the correct electrical net name.
func VerifyWireTopology(before, after, proposed map[string]any) error {
	old, err := topologySegmentsAllowDegenerate(before, true)
	if err != nil {
		return fmt.Errorf("before topology: %w", err)
	}
	actual, err := topologySegmentsAllowDegenerate(after, true)
	if err != nil {
		return fmt.Errorf("after topology: %w", err)
	}
	points, ok := wirePoints(proposed)
	if !ok {
		return fmt.Errorf("proposed topology: invalid wire points")
	}
	// Official create removes redundant collinear points inside ONE action.
	// Existing observed segment ends, including real T nodes, must not be removed.
	points = NormalizePolylinePoints(points)
	expected := append([]segment(nil), old...)
	for i := 1; i < len(points); i++ {
		expected = append(expected, segment{a: points[i-1], b: points[i]})
	}
	return compareTopologySegments(expected, actual)
}

// CompareWireTopology compares two already normalized collections of physical
// segments. Do not supply arbitrary rendering/grid subdivisions as endpoints:
// those are not junction evidence. Pair this with geometric edge coverage and
// authoritative pin/net checks for complete Apply validation.
func CompareWireTopology(expected, actual map[string]any) error {
	want, err := topologySegments(expected)
	if err != nil {
		return fmt.Errorf("expected topology: %w", err)
	}
	have, err := topologySegments(actual)
	if err != nil {
		return fmt.Errorf("actual topology: %w", err)
	}
	return compareTopologySegments(want, have)
}

func compareTopologySegments(expected, actual []segment) error {
	anchors := make([]Point, 0, 2*len(expected))
	for _, s := range expected {
		anchors = append(anchors, s.a, s.b)
	}
	// Include actual endpoints as witnesses too. Their existence is not itself a
	// connection in expected geometry; partition roots come from original segments.
	for _, s := range actual {
		anchors = append(anchors, s.a, s.b)
	}
	want, err := topologyAnchorRoots(expected, anchors)
	if err != nil {
		return fmt.Errorf("expected topology: %w", err)
	}
	have, err := topologyAnchorRoots(actual, anchors)
	if err != nil {
		return fmt.Errorf("observed topology: %w", err)
	}
	forward, reverse := map[int]int{}, map[int]int{}
	for i := range anchors {
		w, h := want[i], have[i]
		if w == -2 || h == -2 {
			continue // Bare crossing witnesses do not manufacture a junction.
		}
		if prev, exists := forward[w]; exists && prev != h {
			return fmt.Errorf("wire-contact-topology: expected physical tree split near (%g,%g)", anchors[i].X, anchors[i].Y)
		}
		if prev, exists := reverse[h]; exists && prev != w {
			return fmt.Errorf("wire-contact-topology: separate physical trees merged near (%g,%g); geometric crossing is not a junction", anchors[i].X, anchors[i].Y)
		}
		forward[w], reverse[h] = h, w
	}
	return nil
}

func topologySegments(result map[string]any) ([]segment, error) {
	return topologySegmentsAllowDegenerate(result, false)
}

// Local additions may preserve old zero-length records. They span nothing and
// create no contacts; the geometry delta still rejects any NEW such record.
// Complete-drawing comparisons retain their strict validation.
func topologySegmentsAllowDegenerate(result map[string]any, allowDegenerate bool) ([]segment, error) {
	if available, known := result["wiresAvailable"].(bool); known && !available {
		return nil, fmt.Errorf("wire inventory unavailable")
	}
	values, ok := array(result["wires"])
	if !ok {
		return nil, fmt.Errorf("wire inventory missing")
	}
	var out []segment
	for _, value := range values {
		wire, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("malformed observed wire")
		}
		points, ok := wirePoints(wire)
		if !ok {
			return nil, fmt.Errorf("malformed observed wire coordinates")
		}
		for i := 1; i < len(points); i++ {
			if same(points[i-1], points[i]) {
				if allowDegenerate {
					continue
				}
				return nil, fmt.Errorf("zero-length observed wire")
			}
			out = append(out, segment{a: points[i-1], b: points[i]})
		}
	}
	return out, nil
}

func topologyAnchorRoots(segments []segment, anchors []Point) ([]int, error) {
	roots := physicalSegmentRoots(segments, nil)
	// An anchor at a bare X can lie on two independent trees. It is only a
	// witness, never a union operation. Such ambiguous interior witnesses must
	// be excluded; the endpoints of each original segment still prove both trees.
	out := make([]int, len(anchors))
	for i, p := range anchors {
		found := -1
		for j, s := range segments {
			if !onSegment(p, s.a, s.b) {
				continue
			}
			r := roots[j]
			if found >= 0 && found != r {
				// A true new split endpoint at a former bare X is not represented
				// by one old island. Let endpoint witnesses detect any actual merge.
				found = -2
				break
			}
			found = r
		}
		if found == -1 {
			return nil, fmt.Errorf("wire coverage missing at (%g,%g)", p.X, p.Y)
		}
		out[i] = found
	}
	return out, nil
}
