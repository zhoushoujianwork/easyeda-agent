package app

// pcb_shortroute_avoid.go — clearance-aware routing for route-short (#23, #clearance).
//
// v1 route-short blindly drew a horizontal-first L for every hop, so tracks crossed
// each other and cut through other nets' pads. v2 picked, per hop, the L ORIENTATION
// (horizontal-first vs vertical-first) that hit the fewest OTHER-NET obstacles.
//
// v3 (this file) makes the cost CLEARANCE-aware and LAYER-aware, so the planner can
// tell when a hop simply cannot run clean on its pads' layer and should detour onto
// the emptier copper layer via a multilayer hop instead (all SMD pads sit on the top
// layer, so a bottom-layer trunk clears the whole pad field). It scores a candidate
// route by:
//   - proper crossings with an other-net segment on the SAME layer (a short-in-waiting),
//   - running within `clr` of an other-net SMD pad ON THE SAME LAYER (layer-aware — a
//     bottom-layer track does not clash with a top-layer SMD pad),
//   - running within `clr` of an other-net via (THT — counts on every layer),
//   - running parallel-close to an other-net segment on the same layer.
// Still NOT a maze router (no push-shove, no rip-up); the planner's multilayer fallback
// is what actually clears a congested hop.

import "math"

// obPad is a pad as an obstacle: its net (a same-net pad is never an obstacle), center
// (mil), copper layer, and half-extent. half is the REAL max-extent/2 when the
// connector reports pad width/height, else 0 → halfOr() falls back to the nominal.
type obPad struct {
	net   string
	x, y  float64
	layer int
	half  float64
}

// halfOr is the pad's half-extent for clearance math — the real value when known,
// else the nominal stand-in.
func (p obPad) halfOr() float64 {
	if p.half > 0 {
		return p.half
	}
	return nominalPadHalf
}

// obVia is a via as an obstacle. A via is through-hole, so it obstructs every copper
// layer; r is its outer radius (mil) added to the clearance.
type obVia struct {
	net  string
	x, y float64
	r    float64
}

// nominalPadHalf is a stand-in pad half-extent (mil) added to the clearance when
// judging "a track runs too close to a pad" — real pad sizes aren't in the router
// input, and a typical 0402/SMD pad is ~15-25mil across.
const nominalPadHalf = 12

// routeWithAvoid returns the hop's segments in whichever L orientation hits fewer
// other-net obstacles. With opt.avoid off, or a straight (aligned) hop, it's just
// the default horizontal-first route.
func routeWithAvoid(net string, a, b rtPad, w float64, opt rtOptions, placed []rtSeg, obstacles []obPad, vias []obVia) []rtSeg {
	if !opt.avoid || a.x == b.x || a.y == b.y {
		return routeHop(net, a, b, w, opt, true)
	}
	h := routeHop(net, a, b, w, opt, true)  // horizontal-first
	v := routeHop(net, a, b, w, opt, false) // vertical-first
	clr := opt.clearance + nominalPadHalf
	hc := hopCost(h, net, a, b, placed, obstacles, vias, clr) + hopSlotCost(h, opt.slots, opt.clearance)
	vc := hopCost(v, net, a, b, placed, obstacles, vias, clr) + hopSlotCost(v, opt.slots, opt.clearance)
	if vc < hc {
		return v
	}
	return h // ties keep the conventional horizontal-first
}

// hopSlotCost penalizes a candidate whose copper lands inside a board cutout's
// keep-away band (max(clearance, 8mil) off the milled edge, every layer).
func hopSlotCost(cand []rtSeg, slots []pcbSlotP, clearance float64) int {
	if len(slots) == 0 {
		return 0
	}
	band := math.Max(clearance, 8)
	cost := 0
	for _, s := range cand {
		for _, sl := range slots {
			if rectSegDist(sl.MinX, sl.MinY, sl.MaxX, sl.MaxY, s.X1, s.Y1, s.X2, s.Y2)-s.Width/2 < band {
				cost += 6
			}
		}
	}
	return cost
}

// hopCost scores a candidate route by how many other-net obstacles it hits. This
// hop's own endpoint pads (a, b) are never counted. Cost 0 means the route violates
// no clearance against anything planned/known so far. opt-independent obstacles
// (slots) are scored via hopSlotCost by the caller that has them.
func hopCost(cand []rtSeg, net string, a, b rtPad, placed []rtSeg, obstacles []obPad, vias []obVia, clr float64) int {
	cost := 0
	for _, s := range cand {
		for _, p := range placed {
			if p.Net == net {
				continue
			}
			if segSegCross(s.X1, s.Y1, s.X2, s.Y2, p.X1, p.Y1, p.X2, p.Y2) {
				cost += 10
			} else if p.Layer == s.Layer && segSegDist(s.X1, s.Y1, s.X2, s.Y2, p.X1, p.Y1, p.X2, p.Y2) < clr {
				cost += 5 // parallel-close on the same layer
			}
		}
		for _, pd := range obstacles {
			if pd.net == net || pd.net == "" || pd.layer != s.Layer {
				continue // layer-aware: a segment only clashes with a pad on its own layer
			}
			if samePoint(pd.x, pd.y, a.x, a.y) || samePoint(pd.x, pd.y, b.x, b.y) {
				continue // this hop's own endpoints
			}
			// clr bakes in the nominal pad half; swap it for the REAL half-extent
			// when the connector reported one (identical when unknown).
			if segPointDist(s.X1, s.Y1, s.X2, s.Y2, pd.x, pd.y) < clr-nominalPadHalf+pd.halfOr() {
				cost += 4
			}
		}
		for _, vp := range vias {
			if vp.net == net {
				continue // a same-net via is a legal touch, not an obstacle
			}
			if samePoint(vp.x, vp.y, a.x, a.y) || samePoint(vp.x, vp.y, b.x, b.y) {
				continue
			}
			if segPointDist(s.X1, s.Y1, s.X2, s.Y2, vp.x, vp.y) < clr+vp.r {
				cost += 4 // vias are through-hole → count on every layer
			}
		}
	}
	return cost
}

// viaClearanceCost scores one detour via by how close it lands to an other-net
// pad or via — a through-hole via obstructs every layer, so it clashes with a top
// SMD pad as readily as with another via. This is what stops a multilayer detour
// from trading a track-over-pad violation for a worse via-over-pad one.
func viaClearanceCost(v rtVia, obPads []obPad, obVias []obVia, clr, r float64) int {
	cost := 0
	for _, pd := range obPads {
		if pd.net == v.Net || pd.net == "" {
			continue
		}
		if math.Hypot(v.X-pd.x, v.Y-pd.y) < clr-nominalPadHalf+pd.halfOr()+r {
			cost += 4
		}
	}
	for _, ov := range obVias {
		if ov.net == v.Net {
			continue
		}
		if math.Hypot(v.X-ov.x, v.Y-ov.y) < clr+r+ov.r {
			cost += 4
		}
	}
	return cost
}

func samePoint(ax, ay, bx, by float64) bool {
	return math.Abs(ax-bx) < 0.01 && math.Abs(ay-by) < 0.01
}

// segSegCross reports whether two segments properly cross (interior intersection).
// Shared/near endpoints do NOT count.
func segSegCross(ax, ay, bx, by, cx, cy, dx, dy float64) bool {
	d := (bx-ax)*(dy-cy) - (by-ay)*(dx-cx)
	if math.Abs(d) < 1e-9 {
		return false // parallel / collinear
	}
	t := ((cx-ax)*(dy-cy) - (cy-ay)*(dx-cx)) / d
	u := ((cx-ax)*(by-ay) - (cy-ay)*(bx-ax)) / d
	const eps = 1e-6
	return t > eps && t < 1-eps && u > eps && u < 1-eps
}

// segPointDist is the shortest distance from point (px,py) to segment (ax,ay)-(bx,by).
func segPointDist(ax, ay, bx, by, px, py float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / l2
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

// segSegDist is the shortest distance between two segments (0 if they intersect).
func segSegDist(ax, ay, bx, by, cx, cy, dx, dy float64) float64 {
	if segSegCross(ax, ay, bx, by, cx, cy, dx, dy) {
		return 0
	}
	return math.Min(
		math.Min(segPointDist(ax, ay, bx, by, cx, cy), segPointDist(ax, ay, bx, by, dx, dy)),
		math.Min(segPointDist(cx, cy, dx, dy, ax, ay), segPointDist(cx, cy, dx, dy, bx, by)),
	)
}
