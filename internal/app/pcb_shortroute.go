package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// pcb_shortroute.go — short-trace self-routing (heuristic, daemon-side).
//
// The "heuristic tier" companion to auto-place (docs/ecosystem-survey.md §8.5):
// real short-trace auto-routing is a heuristic over the standard primitive
// reads/writes, NOT the @alpha autoRouting() API and NOT an external Freerouting
// (that is the separate "maze tier", task #5). The community "PCB自动化工具"
// extension proves it; we run the same idea HERE in the Go daemon.
//
// v1 = connect the SHORT, clear hops. Per net: build a minimum spanning tree over
// its pads (Manhattan), then route each tree edge that is short enough as an
// L-shaped (Manhattan) track on the pads' shared layer. GND is skipped (poured),
// cross-layer hops are skipped (would need a via), and over-long hops are left
// for the maze tier / a human. No push-shove or obstacle avoidance in v1 — run
// after auto-place (satellites already hug their chip pins, so the hops are
// short and clear) and let DRC flag anything that clashes.

// rtPad is a routable pad: which component/pin, where, and on which copper layer.
type rtPad struct {
	comp  string
	pin   string
	x, y  float64
	layer int
}

// rtSeg is one straight copper segment to create (pcb.line.create).
type rtSeg struct {
	Net   string  `json:"net"`
	X1    float64 `json:"x1"`
	Y1    float64 `json:"y1"`
	X2    float64 `json:"x2"`
	Y2    float64 `json:"y2"`
	Layer int     `json:"layer"`
}

// rtNetDiag explains why a net (or one hop of it) was not routed.
type rtNetDiag struct {
	Net    string `json:"net"`
	Reason string `json:"reason"`
}

type rtOptions struct {
	maxLen  float64 // longest single hop (Manhattan, mil) still considered "short"
	width   float64 // track width (mil); 0 → let the connector default apply
	skipGnd bool    // skip GND nets (normally a copper pour, not routed)
}

func defaultRtOptions() rtOptions { return rtOptions{maxLen: 1000, width: 0, skipGnd: true} }

// planShortRoutes is the pure planner: given placed components (with pads) and
// which nets are already routed, return the track segments to create plus
// diagnostics for every net/hop deliberately left unrouted.
func planShortRoutes(comps []apComp, alreadyRouted map[string]bool, opt rtOptions) ([]rtSeg, []rtNetDiag) {
	byNet := map[string][]rtPad{}
	for _, c := range comps {
		for _, pd := range c.pads {
			net := strings.TrimSpace(pd.net)
			if net == "" {
				continue
			}
			byNet[net] = append(byNet[net], rtPad{comp: c.designator, pin: pd.num, x: pd.x, y: pd.y, layer: pd.layer})
		}
	}

	nets := make([]string, 0, len(byNet))
	for n := range byNet {
		nets = append(nets, n)
	}
	sort.Strings(nets)

	var segs []rtSeg
	var diags []rtNetDiag
	for _, net := range nets {
		pads := byNet[net]
		switch {
		case alreadyRouted[net]:
			diags = append(diags, rtNetDiag{net, "already routed"})
			continue
		case opt.skipGnd && isGndNet(net):
			diags = append(diags, rtNetDiag{net, "skipped (GND — leave for pour)"})
			continue
		case len(pads) < 2:
			diags = append(diags, rtNetDiag{net, "single pad — nothing to route"})
			continue
		}
		for _, e := range mstEdges(pads) {
			a, b := pads[e.u], pads[e.v]
			hop := fmt.Sprintf("%s.%s↔%s.%s", a.comp, a.pin, b.comp, b.pin)
			if a.layer != b.layer {
				diags = append(diags, rtNetDiag{net, fmt.Sprintf("%s needs a via (layers %d/%d)", hop, a.layer, b.layer)})
				continue
			}
			if mlen := math.Abs(a.x-b.x) + math.Abs(a.y-b.y); mlen > opt.maxLen {
				diags = append(diags, rtNetDiag{net, fmt.Sprintf("%s too long (%.0f > %.0f mil) — maze tier", hop, mlen, opt.maxLen)})
				continue
			}
			segs = append(segs, lShape(net, a, b)...)
		}
	}
	return segs, diags
}

// lShape returns the 1–2 Manhattan segments connecting a→b: a straight run when
// aligned, else horizontal-first with a corner at (b.x, a.y). Zero-length pieces
// are dropped.
func lShape(net string, a, b rtPad) []rtSeg {
	if a.x == b.x || a.y == b.y {
		return []rtSeg{{Net: net, X1: a.x, Y1: a.y, X2: b.x, Y2: b.y, Layer: a.layer}}
	}
	cx, cy := b.x, a.y // corner: go horizontal first, then vertical
	var out []rtSeg
	if a.x != cx || a.y != cy {
		out = append(out, rtSeg{Net: net, X1: a.x, Y1: a.y, X2: cx, Y2: cy, Layer: a.layer})
	}
	if cx != b.x || cy != b.y {
		out = append(out, rtSeg{Net: net, X1: cx, Y1: cy, X2: b.x, Y2: b.y, Layer: a.layer})
	}
	return out
}

type rtEdge struct{ u, v int }

// mstEdges builds a minimum spanning tree over the pads using Manhattan distance
// (Prim's), so a multi-pad net is routed as the shortest-total tree of hops.
func mstEdges(pads []rtPad) []rtEdge {
	n := len(pads)
	if n < 2 {
		return nil
	}
	manhattan := func(i, j int) float64 {
		return math.Abs(pads[i].x-pads[j].x) + math.Abs(pads[i].y-pads[j].y)
	}
	inTree := make([]bool, n)
	parent := make([]int, n)
	dist := make([]float64, n)
	for i := range pads {
		dist[i] = manhattan(0, i)
	}
	inTree[0] = true
	var edges []rtEdge
	for k := 1; k < n; k++ {
		best, bd := -1, math.MaxFloat64
		for i := 0; i < n; i++ {
			if !inTree[i] && dist[i] < bd {
				bd, best = dist[i], i
			}
		}
		if best < 0 {
			break
		}
		inTree[best] = true
		edges = append(edges, rtEdge{parent[best], best})
		for i := 0; i < n; i++ {
			if !inTree[i] {
				if d := manhattan(best, i); d < dist[i] {
					dist[i], parent[i] = d, best
				}
			}
		}
	}
	return edges
}

func isGndNet(net string) bool {
	return strings.Contains(strings.ToLower(net), "gnd")
}
