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

// rtSeg is one straight copper segment to create (pcb.line.create). Width is the
// per-net-class track width (mil); the connector default applies when it is 0.
type rtSeg struct {
	Net   string  `json:"net"`
	X1    float64 `json:"x1"`
	Y1    float64 `json:"y1"`
	X2    float64 `json:"x2"`
	Y2    float64 `json:"y2"`
	Layer int     `json:"layer"`
	Width float64 `json:"width"`
}

// rtVia is one through-hole via to create at (X,Y), bound to Net. A THT via
// connects every copper layer, so it is the layer-change joint for a multilayer
// hop (route-short's own answer to a hop a single-layer L can't clear).
type rtVia struct {
	Net string  `json:"net"`
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
}

// rtNetDiag explains why a net (or one hop of it) was not routed.
type rtNetDiag struct {
	Net    string `json:"net"`
	Reason string `json:"reason"`
}

type rtOptions struct {
	maxLen      float64 // longest single hop (Manhattan, mil) still considered "short"
	width       float64 // global override (mil); >0 forces ALL segments to this width
	signalWidth float64 // class width for signal nets (used when width==0)
	powerWidth  float64 // class width for power/ground nets (used when width==0)
	skipPower   bool    // skip power+ground nets (isGlobalNet) — they belong in a pour, not thin tracks
	corner      string  // corner style: "90" (L), "45" (chamfer), "round" (chord fillet)
	roundRadius float64 // max fillet radius for corner=="round" (mil)
	avoid       bool    // obstacle-aware L-orientation selection (#23)
	clearance   float64 // safe-spacing clearance (mil) — a track must stay this far from other-net pads
	multilayer  bool    // route the hops a single-layer L can't clear (too long / cross-layer) with a via detour on the alternate copper layer instead of deferring to the maze tier
	stub        float64 // via setback from each pad on a multilayer hop (mil) — keeps vias OFF pads
	viaDia      float64 // multilayer-hop via outer diameter (mil)
	viaHole     float64 // multilayer-hop via hole/drill diameter (mil)
}

func defaultRtOptions() rtOptions {
	return rtOptions{
		maxLen: 1000, width: 0, signalWidth: 10, powerWidth: 20,
		skipPower: true, corner: "90", roundRadius: 20,
		avoid: true, clearance: 6,
		multilayer: true, stub: 30, viaDia: 24, viaHole: 12,
	}
}

// widthFor picks a track width by net class: an explicit --width overrides
// everything; otherwise power/ground nets (isGlobalNet) get the fatter powerWidth
// and ordinary signals get signalWidth. Returns 0 only if every width is 0
// (connector default).
func (o rtOptions) widthFor(net string) float64 {
	if o.width > 0 {
		return o.width
	}
	if isGlobalNet(net) {
		return o.powerWidth
	}
	return o.signalWidth
}

// planShortRoutes is the pure planner: given placed components (with pads) and
// which nets are already routed, return the track segments to create plus
// diagnostics for every net/hop deliberately left unrouted.
func planShortRoutes(comps []apComp, alreadyRouted map[string]bool, opt rtOptions) ([]rtSeg, []rtVia, []rtNetDiag) {
	byNet := map[string][]rtPad{}
	var obPads []obPad // every pad (with net) = an obstacle for other nets' tracks
	for _, c := range comps {
		for _, pd := range c.pads {
			net := strings.TrimSpace(pd.net)
			obPads = append(obPads, obPad{net: net, x: pd.x, y: pd.y})
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
	var vias []rtVia
	var diags []rtNetDiag
	for _, net := range nets {
		pads := byNet[net]
		switch {
		case alreadyRouted[net]:
			diags = append(diags, rtNetDiag{net, "already routed"})
			continue
		case opt.skipPower && isGlobalNet(net):
			diags = append(diags, rtNetDiag{net, "skipped (power/ground — pour it, don't route)"})
			continue
		case len(pads) < 2:
			diags = append(diags, rtNetDiag{net, "single pad — nothing to route"})
			continue
		}
		w := opt.widthFor(net)
		for _, e := range mstEdges(pads) {
			a, b := pads[e.u], pads[e.v]
			hop := fmt.Sprintf("%s.%s↔%s.%s", a.comp, a.pin, b.comp, b.pin)
			crossLayer := a.layer != b.layer
			mlen := math.Abs(a.x-b.x) + math.Abs(a.y-b.y)
			// A hop a single-layer L can't clear: different pad layers, or too long
			// for one straight run. Multilayer routing detours it via the alternate
			// copper layer instead of deferring to the maze tier (#pcb-multilayer).
			if crossLayer || mlen > opt.maxLen {
				if opt.multilayer {
					hs, hv := routeMultilayerHop(net, a, b, w, opt)
					segs = append(segs, hs...)
					vias = append(vias, hv...)
					continue
				}
				if crossLayer {
					diags = append(diags, rtNetDiag{net, fmt.Sprintf("%s needs a via (layers %d/%d) — enable --multilayer", hop, a.layer, b.layer)})
				} else {
					diags = append(diags, rtNetDiag{net, fmt.Sprintf("%s too long (%.0f > %.0f mil) — maze tier or --multilayer", hop, mlen, opt.maxLen)})
				}
				continue
			}
			// Obstacle-aware: pick the L orientation that hits the fewest other-net
			// obstacles (already-placed segments + other-net pads). Falls back to the
			// naive H-first L when --no-avoid or when nothing is in the way.
			hopSegs := routeWithAvoid(net, a, b, w, opt, segs, obPads)
			segs = append(segs, hopSegs...)
		}
	}
	return segs, vias, diags
}

// routeMultilayerHop routes a→b when a single-layer L won't do — either the pads
// sit on different layers, or the hop is too long to run straight on one layer.
// Cross-layer: a single via at the corner joins a.layer→b.layer. Same-layer: the
// long L detours onto the OTHER copper layer (emptier), joined back to the pads
// by two THT vias set `stub` mil off each pad so a via never lands on a pad.
// Pad-side stubs stay on the pad's own layer; the trunk rides the other layer.
func routeMultilayerHop(net string, a, b rtPad, w float64, opt rtOptions) ([]rtSeg, []rtVia) {
	stub := opt.stub
	dx, dy := b.x-a.x, b.y-a.y
	sx, sy := sign(dx), sign(dy)

	if a.layer != b.layer {
		// Cross-layer: L on a.layer to the corner, one via, then b.layer to b.
		cx, cy := b.x, a.y
		segs := appendSeg(nil, net, a.x, a.y, cx, cy, a.layer, w)
		segs = appendSeg(segs, net, cx, cy, b.x, b.y, b.layer, w)
		return segs, []rtVia{{net, cx, cy}}
	}

	other := 2
	if a.layer == 2 {
		other = 1
	}
	// Nearly one-dimensional hops: a straight run on the other layer, a via at
	// each end. Otherwise an H-first dogbone with the corner on the other layer.
	if math.Abs(dx) < 2*stub {
		v1x, v1y := a.x, a.y+sy*stub
		v2x, v2y := b.x, b.y-sy*stub
		segs := appendSeg(nil, net, a.x, a.y, v1x, v1y, a.layer, w)
		segs = appendSeg(segs, net, v1x, v1y, v2x, v2y, other, w)
		segs = appendSeg(segs, net, v2x, v2y, b.x, b.y, a.layer, w)
		return segs, []rtVia{{net, v1x, v1y}, {net, v2x, v2y}}
	}
	if math.Abs(dy) < 2*stub {
		v1x, v1y := a.x+sx*stub, a.y
		v2x, v2y := b.x-sx*stub, b.y
		segs := appendSeg(nil, net, a.x, a.y, v1x, v1y, a.layer, w)
		segs = appendSeg(segs, net, v1x, v1y, v2x, v2y, other, w)
		segs = appendSeg(segs, net, v2x, v2y, b.x, b.y, a.layer, w)
		return segs, []rtVia{{net, v1x, v1y}, {net, v2x, v2y}}
	}
	v1x, v1y := a.x+sx*stub, a.y // via after a short horizontal stub off a
	v2x, v2y := b.x, b.y-sy*stub // via before a short vertical stub into b
	segs := appendSeg(nil, net, a.x, a.y, v1x, v1y, a.layer, w)
	segs = appendSeg(segs, net, v1x, v1y, b.x, a.y, other, w) // other-layer horizontal trunk
	segs = appendSeg(segs, net, b.x, a.y, v2x, v2y, other, w) // other-layer vertical trunk
	segs = appendSeg(segs, net, v2x, v2y, b.x, b.y, a.layer, w)
	return segs, []rtVia{{net, v1x, v1y}, {net, v2x, v2y}}
}

// routeHop connects a→b in the requested corner style, all on a.layer at width w.
// "90" = right-angle L (default), "45" = chamfered corner, "round" = a chord-
// approximated quarter-circle fillet (native arcs do not commit on this build, so
// the curve is emitted as short straight segments). Aligned pads always collapse
// to a single straight run regardless of style.
func routeHop(net string, a, b rtPad, w float64, opt rtOptions, hFirst bool) []rtSeg {
	if a.x == b.x || a.y == b.y {
		return appendSeg(nil, net, a.x, a.y, b.x, b.y, a.layer, w)
	}
	switch opt.corner {
	case "45":
		return lShape45(net, a, b, w, hFirst)
	case "round":
		return lShapeRound(net, a, b, w, opt.roundRadius, hFirst)
	default:
		return lShape90(net, a, b, w, hFirst)
	}
}

// appendSeg adds a→b to out, dropping zero-length pieces.
func appendSeg(out []rtSeg, net string, x1, y1, x2, y2 float64, layer int, w float64) []rtSeg {
	if x1 == x2 && y1 == y2 {
		return out
	}
	return append(out, rtSeg{Net: net, X1: x1, Y1: y1, X2: x2, Y2: y2, Layer: layer, Width: w})
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// lShape90 is the classic Manhattan L. hFirst → horizontal-first, 90° corner at
// (b.x, a.y); !hFirst → vertical-first, corner at (a.x, b.y). The orientation is
// what obstacle-aware routing picks between (routeWithAvoid).
func lShape90(net string, a, b rtPad, w float64, hFirst bool) []rtSeg {
	cx, cy := b.x, a.y
	if !hFirst {
		cx, cy = a.x, b.y
	}
	out := appendSeg(nil, net, a.x, a.y, cx, cy, a.layer, w)
	return appendSeg(out, net, cx, cy, b.x, b.y, a.layer, w)
}

// lShape45 cuts the corner with a 45° diagonal: a straight run, a diagonal that
// covers min(|dx|,|dy|) on each axis, then the other straight run. hFirst runs
// horizontal-first; !hFirst vertical-first. Straight runs collapse to zero (and
// drop) when |dx|==|dy| → a single clean diagonal.
func lShape45(net string, a, b rtPad, w float64, hFirst bool) []rtSeg {
	dx, dy := b.x-a.x, b.y-a.y
	d := math.Min(math.Abs(dx), math.Abs(dy))
	sx, sy := sign(dx), sign(dy)
	var p1x, p1y, p2x, p2y float64
	if hFirst {
		p1x, p1y = b.x-sx*d, a.y // end of the horizontal run
		p2x, p2y = b.x, a.y+sy*d // end of the 45° diagonal
	} else {
		p1x, p1y = a.x, b.y-sy*d // end of the vertical run
		p2x, p2y = a.x+sx*d, b.y // end of the 45° diagonal
	}
	out := appendSeg(nil, net, a.x, a.y, p1x, p1y, a.layer, w)
	out = appendSeg(out, net, p1x, p1y, p2x, p2y, a.layer, w)
	return appendSeg(out, net, p2x, p2y, b.x, b.y, a.layer, w)
}

// lShapeRound rounds the 90° corner with a quarter-circle fillet of radius
// min(|dx|, |dy|, maxR), approximated by roundChords straight chords (native arcs
// do not commit on this build). hFirst → corner at (b.x,a.y); !hFirst → (a.x,b.y).
const roundChords = 6

func lShapeRound(net string, a, b rtPad, w, maxR float64, hFirst bool) []rtSeg {
	dx, dy := b.x-a.x, b.y-a.y
	sx, sy := sign(dx), sign(dy)
	r := math.Min(math.Abs(dx), math.Abs(dy))
	if maxR > 0 && maxR < r {
		r = maxR
	}
	var cx, cy, t1x, t1y, t2x, t2y, ox, oy float64
	if hFirst {
		cx, cy = b.x, a.y          // the sharp corner we are rounding
		t1x, t1y = cx-sx*r, cy     // tangent on the horizontal (incoming) leg
		t2x, t2y = cx, cy+sy*r     // tangent on the vertical (outgoing) leg
		ox, oy = cx-sx*r, cy+sy*r  // arc center (equidistant r from both tangents)
	} else {
		cx, cy = a.x, b.y          // vertical-first corner
		t1x, t1y = cx, cy-sy*r     // tangent on the vertical (incoming) leg
		t2x, t2y = cx+sx*r, cy     // tangent on the horizontal (outgoing) leg
		ox, oy = cx+sx*r, cy-sy*r
	}

	out := appendSeg(nil, net, a.x, a.y, t1x, t1y, a.layer, w) // straight in
	ang1 := math.Atan2(t1y-oy, t1x-ox)
	ang2 := math.Atan2(t2y-oy, t2x-ox)
	da := ang2 - ang1
	for da > math.Pi {
		da -= 2 * math.Pi
	}
	for da < -math.Pi {
		da += 2 * math.Pi
	}
	px, py := t1x, t1y
	for i := 1; i <= roundChords; i++ {
		ang := ang1 + da*float64(i)/float64(roundChords)
		nx, ny := ox+r*math.Cos(ang), oy+r*math.Sin(ang)
		out = appendSeg(out, net, px, py, nx, ny, a.layer, w)
		px, py = nx, ny
	}
	return appendSeg(out, net, px, py, b.x, b.y, a.layer, w) // straight out
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
