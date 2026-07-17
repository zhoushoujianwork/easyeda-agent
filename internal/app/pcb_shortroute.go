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
	signalWidth float64 // class width for signal nets (legacy two-bucket fallback)
	powerWidth  float64 // class width for power/ground nets (legacy two-bucket fallback)
	// netClassWidths is the role→width (mil) ladder (pcb_netclass.go); when present
	// widthFor consults it (signal / power-branch / power-trunk / high-current / gnd)
	// instead of the two-bucket signalWidth/powerWidth split.
	netClassWidths map[string]float64
	skipPower      bool // skip power+ground nets (isGlobalNet) — they belong in a pour, not thin tracks
	corner      string  // corner style: "90" (L), "45" (chamfer), "round" (chord fillet)
	roundRadius float64 // max fillet radius for corner=="round" (mil)
	avoid       bool    // obstacle-aware L-orientation selection (#23)
	clearance   float64 // safe-spacing clearance (mil) — a track must stay this far from other-net pads
	multilayer  bool    // route the hops a single-layer L can't clear (too long / cross-layer) with a via detour on the alternate copper layer instead of deferring to the maze tier
	stub        float64 // via setback from each pad on a multilayer hop (mil) — keeps vias OFF pads
	viaDia      float64 // multilayer-hop via outer diameter (mil)
	viaHole     float64 // multilayer-hop via hole/drill diameter (mil)

	// Pre-existing board copper the plan must stay clear of (routing on a board
	// that already carries tracks/vias — without these the planner only avoids
	// its OWN segments and lands new copper inside old copper's clearance band).
	existing     []rtSeg
	existingVias []obVia
	// Board cutouts / slots (M3 holes …) — the mill removes every layer, so all
	// planned copper keeps ≥ max(clearance, 8mil) from these rects.
	slots []pcbSlotP

	// minWidth is the fab's legal minimum track width (mil). A hop whose endpoint
	// sits in a FINE-PITCH pad field (an other-net pad within finePitch of it —
	// USB-C 16P is 20mil pitch, 0402 is 40) narrows to this: a 10/20mil track
	// terminating on a 20mil-pitch pad cannot clear the 6mil spacing rule to the
	// neighbor pad no matter how it is routed — width is the only lever.
	minWidth  float64
	finePitch float64
}

func defaultRtOptions() rtOptions {
	return rtOptions{
		maxLen: 1000, width: 0, signalWidth: 10, powerWidth: 20,
		netClassWidths: netClassWidthTable(defaultPcbRules()),
		skipPower:      true, corner: "90", roundRadius: 20,
		avoid: true, clearance: 6,
		multilayer: true, stub: 30, viaDia: 24, viaHole: 12,
		minWidth: 6, finePitch: 26,
	}
}

// widthFor picks a track width by net class: an explicit --width overrides
// everything; otherwise the role→width ladder (netClassWidths) gives a spec width
// per role (signal / power-branch / power-trunk / high-current / gnd). Falls back to
// the legacy two-bucket split (isGlobalNet ? powerWidth : signalWidth) when the
// ladder is absent. Returns 0 only if every width is 0 (connector default).
func (o rtOptions) widthFor(net string) float64 {
	if o.width > 0 {
		return o.width
	}
	if o.netClassWidths != nil {
		if w, ok := o.netClassWidths[netRole(net)]; ok && w > 0 {
			return w
		}
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
			obPads = append(obPads, obPad{net: net, x: pd.x, y: pd.y, layer: pd.layer, half: math.Max(pd.w, pd.h) / 2, w: pd.w, h: pd.h})
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
	obstacleSegs := append([]rtSeg(nil), opt.existing...)     // pre-existing + planned copper
	obVias := append([]obVia(nil), opt.existingVias...)       // pre-existing + planned vias
	var diags []rtNetDiag
	clr := opt.clearance + nominalPadHalf
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
		classW := opt.widthFor(net)
		for _, e := range mstEdges(pads) {
			a, b := pads[e.u], pads[e.v]
			hop := fmt.Sprintf("%s.%s↔%s.%s", a.comp, a.pin, b.comp, b.pin)
			crossLayer := a.layer != b.layer
			mlen := math.Abs(a.x-b.x) + math.Abs(a.y-b.y)
			mustDetour := crossLayer || mlen > opt.maxLen // can't run as one same-layer L

			// Fine-pitch endpoint → the single-layer hop narrows to the legal
			// minimum (see rtOptions.minWidth: at 20mil pitch no wider track can
			// clear). A multilayer detour gets classW and applies the same
			// narrowing PER SUB-SEGMENT instead (#107) — see routeMultilayerHop.
			w := applyFinePitch(classW, net, opt, obPads, [2]float64{a.x, a.y}, [2]float64{b.x, b.y})

			// Without multilayer, a hop that needs a detour defers to the maze tier.
			if mustDetour && !opt.multilayer {
				if crossLayer {
					diags = append(diags, rtNetDiag{net, fmt.Sprintf("%s needs a via (layers %d/%d) — enable --multilayer", hop, a.layer, b.layer)})
				} else {
					diags = append(diags, rtNetDiag{net, fmt.Sprintf("%s too long (%.0f > %.0f mil) — maze tier or --multilayer", hop, mlen, opt.maxLen)})
				}
				continue
			}

			// Same-layer L candidate (obstacle-aware orientation) + its clearance cost.
			// singleOK is the HARD gate (#111): false ⇒ every orientation of this L
			// runs through another net's pad copper, so it is not drawable at all.
			single := []rtSeg(nil)
			singleOK := false
			singleCost := 1 << 30
			if !mustDetour {
				single, singleOK = routeWithAvoid(net, a, b, w, opt, obstacleSegs, obPads, obVias)
				if singleOK {
					singleCost = hopCost(single, net, a, b, obstacleSegs, obPads, obVias, clr)
				}
			}

			// Detour onto the emptier copper layer when the hop needs it, when the
			// same-layer L can't clear the pad field at all, or when it would merely
			// run close and a bottom-layer trunk is cleaner (all SMD pads sit on top,
			// so the bottom trunk clears the pad field).
			if opt.multilayer && (mustDetour || !singleOK || singleCost > 0) {
				// Pass the un-narrowed class width: the detour narrows each
				// sub-segment independently (#107), so a bottom-layer trunk far
				// from a fine-pitch field keeps the full net-class width.
				ml, mv, mlOK := routeMultilayerHop(net, a, b, classW, opt, obPads, obVias, obstacleSegs)
				// The detour's cost includes its own vias landing near other nets —
				// otherwise it would trade a track-over-pad short for a worse via-over-pad.
				mlCost := hopCost(ml, net, a, b, obstacleSegs, obPads, obVias, clr)
				for _, vv := range mv {
					mlCost += viaClearanceCost(vv, obPads, obVias, clr, opt.viaDia/2)
				}
				if mlOK && (mustDetour || !singleOK || mlCost < singleCost) {
					segs = append(segs, ml...)
					obstacleSegs = append(obstacleSegs, ml...)
					vias = append(vias, mv...)
					for _, vv := range mv {
						obVias = append(obVias, obVia{net: vv.Net, x: vv.X, y: vv.Y, r: opt.viaDia / 2})
					}
					continue
				}
			}
			// Neither a same-layer L nor a detour clears the other-net pads: report
			// the hop instead of shorting the board (#111). The maze tier / a human
			// gets a real corridor; route-short's job here is to do no harm.
			if !singleOK {
				diags = append(diags, rtNetDiag{net, fmt.Sprintf("%s no clearance-safe path (other-net pad field) — maze tier / manual", hop)})
				continue
			}
			segs = append(segs, single...)
			obstacleSegs = append(obstacleSegs, single...)
		}
	}
	return segs, vias, diags
}

// viaSpotClear reports whether a via at (x,y) sits clear of every other-net pad,
// every via (any net — the drill web), and every other-net segment already down.
// A via is through-hole, so pads count on EVERY layer. The pad test is the rect
// model of the real extent (obPad.edgeDistPt — the same judge as `pcb check`'s
// via↔pad rule): a 60mil EPAD scored as a nominal 12mil circle is how SPIWP's via
// ended up drilled through U1's thermal pad (#111).
func viaSpotClear(x, y float64, net string, obPads []obPad, obVias []obVia, segs []rtSeg, opt rtOptions) bool {
	viaR := opt.viaDia / 2
	for _, p := range obPads {
		if p.net == net {
			continue
		}
		if p.edgeDistPt(x, y)-viaR < opt.clearance {
			return false
		}
	}
	for _, v := range obVias {
		if math.Hypot(x-v.x, y-v.y) < viaR+v.r+math.Max(opt.clearance, 4) {
			return false
		}
	}
	for _, s := range segs {
		if s.Net == net {
			continue
		}
		if segPointDist(s.X1, s.Y1, s.X2, s.Y2, x, y) < opt.clearance+viaR+s.Width/2 {
			return false
		}
	}
	for _, sl := range opt.slots {
		if rectPtDist(sl.MinX, sl.MinY, sl.MaxX, sl.MaxY, x, y)-viaR < math.Max(opt.clearance, 8) {
			return false
		}
	}
	return true
}

// findViaSpot picks a clearance-clean via position near (px,py), searching a ring
// of candidates at growing offsets, preferring the ones toward (tx,ty). ok=false
// when NO candidate is clean — the hop then has no drillable layer change and the
// caller must give it up. (It used to fall back to a fixed stub offset and let the
// cost model "judge" it, which is how a via got drilled into an EPAD: a cost is not
// a veto — #111.)
func findViaSpot(px, py, tx, ty float64, net string, opt rtOptions, obPads []obPad, obVias []obVia, segs []rtSeg) (float64, float64, bool) {
	// Try the directions pointing at (tx,ty) first, so the layer change happens on
	// the target's side and the trunk stays short.
	dirs := append([][2]float64(nil), stitchDirs[:]...)
	dx, dy := tx-px, ty-py
	sort.SliceStable(dirs, func(i, j int) bool {
		return dirs[i][0]*dx+dirs[i][1]*dy > dirs[j][0]*dx+dirs[j][1]*dy
	})
	for _, off := range []float64{opt.stub, opt.stub * 2, opt.stub * 3} {
		for _, d := range dirs {
			x, y := px+d[0]*off, py+d[1]*off
			if viaSpotClear(x, y, net, obPads, obVias, segs, opt) {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

// routeMultilayerHop routes a→b when a single-layer L won't do — either the pads
// sit on different layers, or the hop can't run clean on its own layer. The via
// positions are clearance-searched (findViaSpot), not fixed: in a fine-pitch pad
// field the vias walk OUT of the field instead of landing between pads. Pad-side
// stubs stay on the pad's own layer; the trunk rides the other copper layer.
//
// classW is the net-class width (widthFor(net), NOT hop-narrowed): every
// sub-segment carries it, narrowed to opt.minWidth only where ITS OWN endpoints
// sit in a fine-pitch pad field (#107). So a detour trunk keeps the full
// power-trunk width instead of inheriting a narrow-down forced by a far-away
// endpoint — and a stub that terminates between 20mil-pitch pads still narrows
// exactly like a single-layer hop would.
//
// The bool is the HARD clearance verdict (#111): false ⇒ some piece of this detour
// (a stub, the trunk, or a via with nowhere clean to land) would sit inside another
// net's pad copper. The segments are then NOT drawable — the caller must report the
// hop, not weigh it against a cost.
func routeMultilayerHop(net string, a, b rtPad, classW float64, opt rtOptions, obPads []obPad, obVias []obVia, obstacleSegs []rtSeg) ([]rtSeg, []rtVia, bool) {
	fp := func(x1, y1, x2, y2 float64) float64 {
		return applyFinePitch(classW, net, opt, obPads, [2]float64{x1, y1}, [2]float64{x2, y2})
	}
	isTH := func(l int) bool { return l != 1 && l != 2 }
	if a.layer != b.layer {
		// A through-hole pad (multi-layer, id outside 1/2) reaches every copper
		// layer by itself — route the whole L on the SMD side's layer, no via.
		if isTH(a.layer) && !isTH(b.layer) {
			s, ok := bestL(net, rtPad{x: a.x, y: a.y, layer: b.layer}, b, fp(a.x, a.y, b.x, b.y), opt, obstacleSegs, obPads, obVias)
			return s, nil, ok
		}
		if isTH(b.layer) && !isTH(a.layer) {
			s, ok := bestL(net, a, rtPad{x: b.x, y: b.y, layer: a.layer}, fp(a.x, a.y, b.x, b.y), opt, obstacleSegs, obPads, obVias)
			return s, nil, ok
		}
		if isTH(a.layer) && isTH(b.layer) {
			s, ok := bestL(net, rtPad{x: a.x, y: a.y, layer: 1}, rtPad{x: b.x, y: b.y, layer: 1}, fp(a.x, a.y, b.x, b.y), opt, obstacleSegs, obPads, obVias)
			return s, nil, ok
		}
		// True SMD top↔bottom: L on a.layer to a clear via, then b.layer to b.
		vx, vy, ok := findViaSpot(b.x, a.y, a.x, a.y, net, opt, obPads, obVias, obstacleSegs)
		if !ok {
			return nil, nil, false // no clean spot to change layer on
		}
		segs, ok1 := bestL(net, a, rtPad{x: vx, y: vy, layer: a.layer}, fp(a.x, a.y, vx, vy), opt, obstacleSegs, obPads, obVias)
		tail, ok2 := bestL(net, rtPad{x: vx, y: vy, layer: b.layer}, b, fp(vx, vy, b.x, b.y), opt, obstacleSegs, obPads, obVias)
		return append(segs, tail...), []rtVia{{net, vx, vy}}, ok1 && ok2
	}

	other := 2
	if a.layer == 2 {
		other = 1
	}
	v1x, v1y, ok1 := findViaSpot(a.x, a.y, b.x, b.y, net, opt, obPads, obVias, obstacleSegs)
	v2x, v2y, ok2 := findViaSpot(b.x, b.y, a.x, a.y, net, opt, obPads, obVias, obstacleSegs)
	if !ok1 || !ok2 {
		return nil, nil, false
	}
	stubA, okA := bestL(net, a, rtPad{x: v1x, y: v1y, layer: a.layer}, fp(a.x, a.y, v1x, v1y), opt, obstacleSegs, obPads, obVias)                                 // pad-side stub (a.layer)
	trunk, okT := bestL(net, rtPad{x: v1x, y: v1y, layer: other}, rtPad{x: v2x, y: v2y, layer: other}, fp(v1x, v1y, v2x, v2y), opt, obstacleSegs, obPads, obVias) // trunk
	stubB, okB := bestL(net, rtPad{x: v2x, y: v2y, layer: b.layer}, b, fp(v2x, v2y, b.x, b.y), opt, obstacleSegs, obPads, obVias)                                 // stub into b
	segs := append(stubA, trunk...)
	segs = append(segs, stubB...)
	return segs, []rtVia{{net, v1x, v1y}, {net, v2x, v2y}}, okA && okT && okB
}

// bestL picks the cheaper *feasible* L orientation between two points on the FROM
// point's layer — the multilayer hop's sub-segments get the same obstacle-aware
// orientation choice (and the same hard other-net-pad gate, #111) a plain hop gets,
// instead of a hardcoded corner. ok=false ⇒ neither orientation clears the pad
// field; the segments are the least-bad candidate, not something to draw.
func bestL(net string, from, to rtPad, w float64, opt rtOptions, obstacleSegs []rtSeg, obPads []obPad, obVias []obVia) ([]rtSeg, bool) {
	return pickL(net, from, to,
		lShape90(net, from, to, w, true),
		lShape90(net, from, to, w, false),
		opt, obstacleSegs, obPads, obVias)
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

// applyFinePitch narrows a net-class width to the fab's legal minimum when any of
// the given points sits inside a fine-pitch pad field (see rtOptions.minWidth: a
// wide track terminating between 20mil-pitch pads cannot clear the spacing rule no
// matter how it is routed — width is the only lever). The single-layer path applies
// it once per hop (both pad endpoints); a multilayer detour applies it per
// sub-segment (#107), so only the pieces actually inside the field narrow.
func applyFinePitch(w float64, net string, opt rtOptions, obPads []obPad, pts ...[2]float64) float64 {
	if opt.minWidth <= 0 || opt.minWidth >= w {
		return w
	}
	for _, p := range pts {
		if finePitchAt(p[0], p[1], net, obPads, opt.finePitch) {
			return opt.minWidth
		}
	}
	return w
}

// finePitchAt reports whether an other-net pad sits within `pitch` mil of (x,y) —
// i.e. the point is inside a fine-pitch pad field (USB-C 16P: 20mil pitch).
func finePitchAt(x, y float64, net string, obPads []obPad, pitch float64) bool {
	if pitch <= 0 {
		return false
	}
	for _, p := range obPads {
		if p.net == net {
			continue
		}
		if math.Hypot(p.x-x, p.y-y) <= pitch {
			return true
		}
	}
	return false
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
