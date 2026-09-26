package app

import (
	"fmt"
	"github.com/zhoushoujianwork/easyeda-agent/internal/schguard"
)

// schClusterSnapshotWires preserves each official flat segment from the same
// snapshot as pins and markers. It never links the tail/head of adjacent records.
// Raw provenance is separately validated by schguard before any exemption.
func schClusterSnapshotWires(result map[string]any) ([]schGroupWire, error) {
	if result["wiresAvailable"] != true {
		return nil, fmt.Errorf("wire inventory unavailable")
	}
	raw, ok := result["wires"].([]any)
	if !ok {
		return nil, fmt.Errorf("wire inventory missing")
	}
	var out []schGroupWire
	indices := map[string]int{}
	for _, v := range raw {
		w, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid wire")
		}
		id := asString(w["primitiveId"])
		if id == "" {
			return nil, fmt.Errorf("wire identity missing")
		}
		var seg [4]float64
		for i, key := range []string{"x0", "y0", "x1", "y1"} {
			n, ok := finiteFloat(w[key])
			if !ok {
				return nil, fmt.Errorf("wire coordinate missing")
			}
			seg[i] = n
		}
		i, ok := indices[id]
		if !ok {
			i = len(out)
			indices[id] = i
			out = append(out, schGroupWire{ID: id})
		}
		out[i].Points = append(out[i].Points, seg[:]...)
		out[i].ObservedSegments = append(out[i].ObservedSegments, seg)
	}
	return out, nil
}

func schClusterMembersProvedCrossing(a schCluster, ai int, b schCluster, bi int, proof *schguard.WireCrossingProof) bool {
	// Older planners/fixtures may provide only Members. Never infer wire kind
	// from a narrow box or silently use a misaligned Typed collection.
	if proof == nil || len(a.Typed) != len(a.Members) || len(b.Typed) != len(b.Members) || ai >= len(a.Typed) || bi >= len(b.Typed) {
		return false
	}
	x, y := a.Typed[ai], b.Typed[bi]
	if x.Kind != "wire" || y.Kind != "wire" || x.Segment == nil || y.Segment == nil || x.BBox != a.Members[ai] || y.BBox != b.Members[bi] {
		return false
	}
	u, v := *x.Segment, *y.Segment
	return proof.Allows(x.WireID, schguard.Point{X: u[0], Y: u[1]}, schguard.Point{X: u[2], Y: u[3]},
		y.WireID, schguard.Point{X: v[0], Y: v[1]}, schguard.Point{X: v[2], Y: v[3]})
}
