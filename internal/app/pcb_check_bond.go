package app

// pcb check — floating-track-island (probe round #1 C-list).
//
// floating-track-island: a connected GROUP of tracks/vias none of whose
// endpoints anchors to a pad — dangling-end's blind spot (members anchor each
// other, so per-endpoint checks stay silent) → WARN per island.
//
// Historical note: this file also carried a `via-bond` ERROR rule built on
// pro-api-sdk#31 ("track↔via does not register as connected on 4-layer/PLANE
// boards"). That was our misdiagnosis — live retest on 2026-07-07 showed
// track↔via DOES register (a via bridge satisfies the ratline in every plane
// state); the original "floating" symptom was STALE pour connectivity, cured by
// `pcb pour-rebuild` + a ratline recompute, not by fill-bonding. The rule was a
// false-positive generator (it flagged junctions DRC reports as connected) and
// has been removed. DRC now owns the real open-net verdict.
//
// Pure functions over list-shaped inputs; live fetches sit at the bottom.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

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
	// Via bridges: all tracks touching the same via join one component — the via
	// conducts across layers (pro-api-sdk#31), so this is both the geometric
	// intent and the electrical reality.
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
