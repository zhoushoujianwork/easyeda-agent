package app

// pcb check — via-bond + floating-track-island (probe round #1 C-list).
//
// Both rules exist because of one platform truth (pro-api-sdk#31, verified live):
// on 4-layer / PLANE-bearing boards, a track touching a via does NOT register as
// electrically connected — not endpoint-on-center, not via-on-body. The only
// reliable bridge is a net-bound FILL (or same-net pour) overlapping the
// junction. `pcb via-hop` builds bonded hops automatically; these rules catch
// the LEGACY / hand-made junctions that never got a bond.
//
//   - via-bond: a same-net track↔via junction with no bond fill/pour on the
//     track's layer → ERROR (it looks routed but is open). Skipped entirely on
//     plain 2-layer boards, where the junction does register.
//   - floating-track-island: a connected GROUP of tracks/vias none of whose
//     endpoints anchors to a pad — dangling-end's blind spot (members anchor
//     each other, so per-endpoint checks stay silent) → WARN per island.
//
// Pure functions over list-shaped inputs; live fetches sit at the bottom.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// pcbFillP is one net-bound fill (pcb.fill.list --include-bbox). HasBBox is
// false on older connectors (no bbox support) — the bond check then degrades
// to "same-net fill exists on the layer" instead of point-in-box.
type pcbFillP struct {
	ID                     string
	Net                    string
	Layer                  int
	MinX, MinY, MaxX, MaxY float64
	HasBBox                bool
}

// viaBondEps pads the fill bbox / junction geometry tests: a bond fill only
// has to overlap the junction, not contain it with margin.
const viaBondEps = 1.0

// findViaBondIssues flags same-net track↔via junctions that have no bond on
// the track's layer. Active only when the board is 4+ copper layers or has a
// PLANE layer — the class where bare junctions do not conduct (#31); a 2-layer
// SIGNAL-only board registers them fine and gets no findings.
func findViaBondIssues(tracks []pcbTrack, vias []pcbViaP, fills []pcbFillP, pours []pcbPourP, copperLayers int, planeCount int) []pcbCheckFinding {
	if copperLayers < 4 && planeCount == 0 {
		return nil
	}

	// pour nets per layer — a same-net pour reflows onto the via and bonds it.
	type netLayer struct {
		net   string
		layer int
	}
	pourOn := map[netLayer]bool{}
	for _, p := range pours {
		if strings.TrimSpace(p.Net) != "" {
			pourOn[netLayer{p.Net, p.Layer}] = true
		}
	}

	bonded := func(net string, layer int, x, y float64) bool {
		if pourOn[netLayer{net, layer}] {
			return true
		}
		for _, f := range fills {
			if f.Net != net || f.Layer != layer {
				continue
			}
			if !f.HasBBox {
				return true // older connector: fill exists on net+layer, assume bonded
			}
			if x >= f.MinX-viaBondEps && x <= f.MaxX+viaBondEps &&
				y >= f.MinY-viaBondEps && y <= f.MaxY+viaBondEps {
				return true
			}
		}
		return false
	}

	var out []pcbCheckFinding
	seen := map[string]bool{} // one finding per via+layer
	for _, v := range vias {
		if strings.TrimSpace(v.Net) == "" {
			continue
		}
		r := v.Dia / 2
		for _, t := range tracks {
			if t.Net != v.Net {
				continue
			}
			// Junction: a track endpoint inside the via copper, or the track
			// centerline passing through it (via pressed onto the body).
			touches := math.Hypot(t.X1-v.X, t.Y1-v.Y) <= r+pcbCoincEps ||
				math.Hypot(t.X2-v.X, t.Y2-v.Y) <= r+pcbCoincEps ||
				segPtDist(v.X, v.Y, t.X1, t.Y1, t.X2, t.Y2) <= r
			if !touches {
				continue
			}
			key := fmt.Sprintf("%s|%d", v.ID, t.Layer)
			if seen[key] || bonded(v.Net, t.Layer, v.X, v.Y) {
				continue
			}
			seen[key] = true
			out = append(out, pcbCheckFinding{
				Type: "via-bond", Level: "ERROR", Net: v.Net, Layer: t.Layer,
				Primitives: []string{v.ID, t.ID}, At: &pcbXY{round2(v.X), round2(v.Y)},
				Message: fmt.Sprintf("bare track↔via junction (net %s) — on 4-layer/PLANE boards this does NOT register as connected (pro-api-sdk#31); bond it with a ~20×20mil net-bound fill over the junction on this layer (`pcb fill create`), or re-route the hop with `pcb via-hop` (bonds automatically)", v.Net),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Net != out[j].Net {
			return out[i].Net < out[j].Net
		}
		return out[i].Layer < out[j].Layer
	})
	return out
}

// findFloatingTrackIslands finds connected groups of tracks (linked by touching
// endpoints/bodies on the same layer, or bridged by a shared via) in which NO
// track endpoint anchors to a pad — an entire island of copper feeding nothing.
// dangling-end can't see these: every member endpoint is "anchored" by another
// member. Islands under a same-net pour are skipped (the pour bonds them);
// single tracks are left to dangling-end (they'd double-report).
func findFloatingTrackIslands(tracks []pcbTrack, vias []pcbViaP, pads []pcbPadP, pours []pcbPourP) []pcbCheckFinding {
	n := len(tracks)
	if n < 2 {
		return nil
	}

	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(a int) int {
		for parent[a] != a {
			parent[a] = parent[parent[a]]
			a = parent[a]
		}
		return a
	}
	union := func(a, b int) { parent[find(a)] = find(b) }

	touchesTrack := func(a, b pcbTrack) bool {
		if a.Layer != b.Layer {
			return false
		}
		for _, ep := range [][2]float64{{a.X1, a.Y1}, {a.X2, a.Y2}} {
			if segPtDist(ep[0], ep[1], b.X1, b.Y1, b.X2, b.Y2) <= pcbCoincEps {
				return true
			}
		}
		for _, ep := range [][2]float64{{b.X1, b.Y1}, {b.X2, b.Y2}} {
			if segPtDist(ep[0], ep[1], a.X1, a.Y1, a.X2, a.Y2) <= pcbCoincEps {
				return true
			}
		}
		return false
	}
	touchesVia := func(t pcbTrack, v pcbViaP) bool {
		r := v.Dia/2 + pcbCoincEps
		return math.Hypot(t.X1-v.X, t.Y1-v.Y) <= r ||
			math.Hypot(t.X2-v.X, t.Y2-v.Y) <= r ||
			segPtDist(v.X, v.Y, t.X1, t.Y1, t.X2, t.Y2) <= v.Dia/2
	}

	for i := range n {
		for j := i + 1; j < n; j++ {
			if touchesTrack(tracks[i], tracks[j]) {
				union(i, j)
			}
		}
	}
	// Via bridges: all tracks touching the same via join one component (that is
	// the geometric intent, even though the platform may not conduct it — the
	// via-bond rule owns THAT defect).
	for _, v := range vias {
		first := -1
		for i, t := range tracks {
			if !touchesVia(t, v) {
				continue
			}
			if first < 0 {
				first = i
			} else {
				union(first, i)
			}
		}
	}

	// pour nets per layer (same-net pour bonds the island to real copper).
	pourNetLayer := map[string]map[int]bool{}
	for _, p := range pours {
		net := strings.TrimSpace(p.Net)
		if net == "" {
			continue
		}
		if pourNetLayer[net] == nil {
			pourNetLayer[net] = map[int]bool{}
		}
		pourNetLayer[net][p.Layer] = true
	}

	groups := map[int][]int{}
	for i := range tracks {
		root := find(i)
		groups[root] = append(groups[root], i)
	}

	var out []pcbCheckFinding
	for _, idxs := range groups {
		if len(idxs) < 2 {
			continue // single floating track = dangling-end territory
		}
		anchoredToPad := false
		underPour := false
		var ids []string
		var sx, sy float64
		net := tracks[idxs[0]].Net
		for _, i := range idxs {
			t := tracks[i]
			ids = append(ids, t.ID)
			sx += (t.X1 + t.X2) / 2
			sy += (t.Y1 + t.Y2) / 2
			for _, ep := range [][2]float64{{t.X1, t.Y1}, {t.X2, t.Y2}} {
				for _, p := range pads {
					tol := pcbCoincEps
					if t.Net != "" && p.Net == t.Net {
						tol = padBodyAnchorTol
					}
					if math.Hypot(p.X-ep[0], p.Y-ep[1]) <= tol {
						anchoredToPad = true
					}
				}
			}
			if t.Net != "" && pourNetLayer[t.Net][t.Layer] {
				underPour = true
			}
		}
		if anchoredToPad || underPour {
			continue
		}
		sort.Strings(ids)
		out = append(out, pcbCheckFinding{
			Type: "floating-track-island", Level: "WARN", Net: net,
			Layer: tracks[idxs[0]].Layer, Primitives: ids,
			At: &pcbXY{round2(sx / float64(len(idxs))), round2(sy / float64(len(idxs)))},
			Message: fmt.Sprintf("island of %d connected track(s) anchors to NO pad — an entire floating copper group (dangling-end can't see it: members anchor each other); wire it to a pad or delete it (`pcb track-delete --ids …`)", len(idxs)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].Primitives) > len(out[j].Primitives) })
	return out
}

// fetchPcbFills reads net-bound fills with bboxes (pcb.fill.list includeBBox).
// Older connectors return no bbox field — HasBBox=false keeps the bond check
// usable in its degraded form.
func fetchPcbFills(cfg *appConfig, window string) ([]pcbFillP, error) {
	res, err := requestAction(cfg, "pcb.fill.list", window, map[string]any{"includeBBox": true})
	if err != nil {
		return nil, err
	}
	var fills []pcbFillP
	for _, rf := range mnavSlice(res.Result, "fills") {
		fm, ok := rf.(map[string]any)
		if !ok {
			continue
		}
		id, _ := fm["primitiveId"].(string)
		net, _ := fm["net"].(string)
		layerF, _ := asFloatOK(fm["layer"])
		f := pcbFillP{ID: id, Net: net, Layer: int(layerF)}
		if bb, ok := fm["bbox"].(map[string]any); ok {
			minX, ok1 := asFloatOK(bb["minX"])
			minY, ok2 := asFloatOK(bb["minY"])
			maxX, ok3 := asFloatOK(bb["maxX"])
			maxY, ok4 := asFloatOK(bb["maxY"])
			if ok1 && ok2 && ok3 && ok4 {
				f.MinX, f.MinY, f.MaxX, f.MaxY, f.HasBBox = minX, minY, maxX, maxY, true
			}
		}
		fills = append(fills, f)
	}
	return fills, nil
}

// fetchPcbCopperLayerCount reads copperLayerCount from pcb.layers.list
// (2 on failure — the conservative class where via-bond stays silent).
func fetchPcbCopperLayerCount(cfg *appConfig, window string) int {
	if lres, err := requestAction(cfg, "pcb.layers.list", window, nil); err == nil {
		if n, ok := asFloatOK(mnav(lres.Result, "copperLayerCount")); ok && n > 0 {
			return int(n)
		}
	}
	return 2
}
