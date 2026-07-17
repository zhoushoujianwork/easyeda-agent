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
// (mil), copper layer, and extent. w/h are the REAL copper extents when the connector
// reports them (0 = unknown → the nominal circle model); half is max(w,h)/2, kept for
// the radial cost/fine-pitch math that doesn't need the rect.
type obPad struct {
	net   string
	x, y  float64
	layer int
	half  float64
	w, h  float64
}

// halfOr is the pad's half-extent for clearance math — the real value when known,
// else the nominal stand-in.
func (p obPad) halfOr() float64 {
	if p.half > 0 {
		return p.half
	}
	return nominalPadHalf
}

// hasExtent reports whether the connector gave this pad a real copper rect. Without
// one (old connector / polygon pad) every model falls back to the nominal circle, so
// a sizeless board keeps routing exactly as it did before real extents existed.
func (p obPad) hasExtent() bool { return p.w > 0 && p.h > 0 }

func (p obPad) rect() (float64, float64, float64, float64) {
	return p.x - p.w/2, p.y - p.h/2, p.x + p.w/2, p.y + p.h/2
}

// edgeDistSeg is the copper-edge gap between a segment and this pad — the SAME judge
// `pcb check`'s findClearanceViolations uses: the axis-aligned RECT of the real extent
// (a radial max(w,h)/2 would false-flag a track running legitimately beside an
// elongated pad, and would under-model a big square EPAD's corners), falling back to
// the nominal circle when the extent is unknown.
func (p obPad) edgeDistSeg(x1, y1, x2, y2 float64) float64 {
	if p.hasExtent() {
		minX, minY, maxX, maxY := p.rect()
		return rectSegDist(minX, minY, maxX, maxY, x1, y1, x2, y2)
	}
	return segPointDist(x1, y1, x2, y2, p.x, p.y) - p.halfOr()
}

// edgeDistPt is edgeDistSeg for a point (a via center) — same rect/circle split.
func (p obPad) edgeDistPt(x, y float64) float64 {
	if p.hasExtent() {
		minX, minY, maxX, maxY := p.rect()
		return rectPtDist(minX, minY, maxX, maxY, x, y)
	}
	return math.Hypot(x-p.x, y-p.y) - p.halfOr()
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

// hopFeasible is the HARD clearance gate on other-net pads (#111). hopCost prices a
// pad strike as mere cost (+4), so when BOTH L orientations hit copper the planner
// still drew the "cheaper" one — on a QFN-56 that meant a track ploughing through
// five other-net pads and a via landing on the EPAD. Copper-to-copper under the
// spacing rule is not a preference, it is a short: a candidate that fails this is
// unroutable at any cost, and the caller must detour or report it — never draw it.
//
// The judge is `pcb check`'s (obPad.edgeDistSeg → rectSegDist over the real extent),
// so the router can no longer emit what the checker will flag. Layer-aware exactly
// like the check (an SMD pad only obstructs copper on its own layer); the hop's own
// endpoint pads are not obstacles.
//
// UNNAMED pads are obstacles too — the one place this is deliberately stricter than
// findClearanceViolations, which skips net:"" pads. Copper is copper regardless of
// what the netlist calls it: on the #43 board U1.36-41 are unconnected QFN pins with
// no net and 26×8mil of real copper each, and SPID's MST hop ran straight down the
// column through all five — which the checker duly reported as five track-over-pad
// SHORTS (that rule does not skip net:""). A pad the router is free to ignore is a
// pad it will eventually drive a track through.
//
// Other-net VIAS are gated the same way (through-hole → every layer, check's edge
// formula: centre distance less the via's radius and the track's half-width). They
// were +4 cost as well, and the planner appends each detour's vias to obVias as it
// goes — so on ceshi SPIWP drew straight through two vias route-short itself had
// dropped for SPICLK seconds earlier.
//
// Other-net TRACKS are gated too (#119 — #111 only covered pads): a proper
// crossing on the same layer is a dead short (segSegDist returns 0, so the edge
// test catches it), and running under the spacing rule beside one is what native
// DRC flags as Track to Track. hopCost priced a crossing at +10, so when BOTH L
// orientations crossed something the planner still drew the "cheaper" short —
// R2's SPIHD×SPIWP / SPICS0×SPIWP came from exactly this, via detour entry stubs
// dropping through a same-layer foreign track. The judge is the check's
// (segSegDist − both half-widths, crossing ⇒ 0 — commit 33df2d9); same-net and
// cross-layer segments are never obstacles. Netless (net:"") tracks count, same
// stance as unnamed pads above: copper is copper.
//
// Board cutouts/SLOTS are a hard gate as well (#122 — same cost-not-veto bug):
// copper within max(clearance, 8mil) of a milled edge is native DRC's Slot
// Region to Track. Judge shared with `pcb check`'s slot branch (rectSegDist −
// half-width vs slotClr).
func hopFeasible(cand []rtSeg, net string, a, b rtPad, placed []rtSeg, obPads []obPad, obVias []obVia, slots []pcbSlotP, clearance float64) bool {
	slotClr := math.Max(clearance, 8)
	for _, s := range cand {
		for _, pd := range obPads {
			if pd.net == net || pd.layer != s.Layer {
				continue
			}
			if samePoint(pd.x, pd.y, a.x, a.y) || samePoint(pd.x, pd.y, b.x, b.y) {
				continue // this hop's own endpoints
			}
			if pd.edgeDistSeg(s.X1, s.Y1, s.X2, s.Y2) < clearance {
				return false
			}
		}
		for _, vp := range obVias {
			if vp.net == net {
				continue // a same-net via is a legal touch (that's what a detour joint IS)
			}
			if samePoint(vp.x, vp.y, a.x, a.y) || samePoint(vp.x, vp.y, b.x, b.y) {
				continue
			}
			if segPointDist(s.X1, s.Y1, s.X2, s.Y2, vp.x, vp.y)-vp.r-s.Width/2 < clearance {
				return false
			}
		}
		for _, p := range placed {
			if p.Net == net || p.Layer != s.Layer {
				continue
			}
			if segSegDist(s.X1, s.Y1, s.X2, s.Y2, p.X1, p.Y1, p.X2, p.Y2)-s.Width/2-p.Width/2 < clearance {
				return false
			}
		}
		for _, sl := range slots {
			if rectSegDist(sl.MinX, sl.MinY, sl.MaxX, sl.MaxY, s.X1, s.Y1, s.X2, s.Y2)-s.Width/2 < slotClr {
				return false
			}
		}
	}
	return true
}

// pickL chooses between the two L orientations: a FEASIBLE candidate always beats an
// infeasible one (hopFeasible is a gate, not a cost); among equals the cheaper cost
// wins and ties keep the conventional horizontal-first. ok=false means neither
// orientation clears the other-net pad field — the returned segments must NOT be
// drawn, they are only the caller's least-bad candidate for diagnostics.
func pickL(net string, a, b rtPad, h, v []rtSeg, opt rtOptions, placed []rtSeg, obstacles []obPad, vias []obVia) ([]rtSeg, bool) {
	hOK := hopFeasible(h, net, a, b, placed, obstacles, vias, opt.slots, opt.clearance)
	vOK := hopFeasible(v, net, a, b, placed, obstacles, vias, opt.slots, opt.clearance)
	clr := opt.clearance + nominalPadHalf
	hc := hopCost(h, net, a, b, placed, obstacles, vias, clr)
	vc := hopCost(v, net, a, b, placed, obstacles, vias, clr)
	if (vOK && !hOK) || (vOK == hOK && vc < hc) {
		return v, vOK
	}
	return h, hOK
}

// routeWithAvoid returns the hop's segments in whichever L orientation hits fewer
// other-net obstacles, plus whether that route is clearance-FEASIBLE at all (#111 —
// ok=false ⇒ the caller detours or reports; drawing it would short). With opt.avoid
// off it's the v1 naive horizontal-first route, ungated (an explicit opt-out).
func routeWithAvoid(net string, a, b rtPad, w float64, opt rtOptions, placed []rtSeg, obstacles []obPad, vias []obVia) ([]rtSeg, bool) {
	if !opt.avoid {
		return routeHop(net, a, b, w, opt, true), true
	}
	// A straight (aligned) hop has no orientation to choose — but it still has to
	// clear the pad field: an aligned pad row is exactly how SPID ran down x=793
	// through five of U1's pads.
	if a.x == b.x || a.y == b.y {
		cand := routeHop(net, a, b, w, opt, true)
		return cand, hopFeasible(cand, net, a, b, placed, obstacles, vias, opt.slots, opt.clearance)
	}
	return pickL(net, a, b,
		routeHop(net, a, b, w, opt, true),  // horizontal-first
		routeHop(net, a, b, w, opt, false), // vertical-first
		opt, placed, obstacles, vias)
}

// hopCost scores a candidate route by how many other-net obstacles it hits. This
// hop's own endpoint pads (a, b) are never counted. Cost 0 means the route violates
// no clearance against anything planned/known so far. Slots need no cost term:
// their keep-away band is a hard hopFeasible gate (#122) at the same threshold, so
// a feasible candidate can never incur one.
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
			// clr bakes in the nominal pad half, so clr-nominalPadHalf is the bare
			// spacing rule to compare the copper-edge gap against (rect model on a
			// real extent, nominal circle when unknown — same judge as hopFeasible).
			if pd.edgeDistSeg(s.X1, s.Y1, s.X2, s.Y2) < clr-nominalPadHalf {
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
		if pd.edgeDistPt(v.X, v.Y)-r < clr-nominalPadHalf {
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
