package app

// pcb_powerplanes_stitch.go — clearance-aware stitch-via placement for power-planes.
//
// The old behavior dropped one via at EVERY power pad center, with no clearance
// check: vias landed ON pads (unreliable bond — the via-on-pad trap) and inside
// other nets' spacing bands, producing the ceshi DRC wall (Track-to-Via /
// SMD-Pad-to-Via / Hole-to-* ≈ 35 of 57 clearance errors came from stitch vias).
// This planner replaces it:
//   - through-hole pads are skipped (a TH pad already reaches the inner plane),
//   - each via sits OFF its pad at a candidate offset, joined by a short stub
//     track (0/45/90° so pcb check's non-orthogonal rule stays quiet),
//   - every candidate is scored against other-net pads, routed tracks, and ALL
//     vias (hole-to-hole applies regardless of net) before it is accepted,
//   - same-net pads within reach SHARE one via (an EPAD's 9 sub-pads fan their
//     stubs into one hole instead of drilling 9).
// A pad with no clear candidate is left unstitched (reported) rather than
// violated into place — the pour/DRC surfaces it for a human.

import "math"

// stitchCtx carries the geometry rules the planner scores against.
type stitchCtx struct {
	clearance float64    // copper-copper spacing rule (mil)
	viaDia    float64    // stitch via outer diameter (mil)
	stubW     float64    // pad→via stub track width (mil)
	offsets   []float64  // candidate via offsets from the pad center (mil)
	shareDist float64    // same-net pads within this of a planned via reuse it (mil)
	rect      [4]float64 // outline inset box vias must land inside (minX,minY,maxX,maxY)
}

func defaultStitchCtx(clearance float64, rect [4]float64) stitchCtx {
	if clearance <= 0 {
		clearance = 6
	}
	return stitchCtx{
		clearance: clearance, viaDia: 24, stubW: 20,
		offsets: []float64{30, 50}, shareDist: 80, rect: rect,
	}
}

// stitchResult is one net's stitch plan.
type stitchResult struct {
	Vias      []rtVia // new vias to create
	Stubs     []rtSeg // pad→via stub tracks (on the pad's layer)
	Unplaced  int     // pads with no clear via candidate (left for DRC)
	Shared    int     // pads that fanned into an earlier via
	SkippedTH int     // through-hole pads (already reach the plane)
}

// stitchDirs are the 8 candidate directions (unit vectors), cardinals first so a
// clean straight stub wins over a 45° one.
var stitchDirs = [8][2]float64{
	{1, 0}, {-1, 0}, {0, 1}, {0, -1},
	{0.7071, 0.7071}, {-0.7071, 0.7071}, {0.7071, -0.7071}, {-0.7071, -0.7071},
}

// planStitchViasForNet plans clearance-clean stitch vias for one power net.
// padsAll is EVERY pad on the board (own pads selected by net, the rest are
// obstacles); tracks/vias are the already-routed copper. priorVias are vias
// planned earlier in the same power-planes run (other nets) — they count as
// obstacles too, and same-net entries are shareable.
func planStitchViasForNet(net string, padsAll []pcbPadP, tracks []pcbTrack, vias []pcbViaP, priorVias []rtVia, ctx stitchCtx) stitchResult {
	var res stitchResult

	// A via is through-hole: it clashes with copper on every layer, so every
	// other-net pad and every via (any net) is an obstacle.
	viaR := ctx.viaDia / 2
	padBand := ctx.clearance + viaR + nominalPadHalf
	viaBand := ctx.viaDia + math.Max(ctx.clearance, 4) // via-to-via center distance (covers hole-to-hole web)

	type pt struct{ x, y float64 }
	var planned []pt // this net's vias planned in this call (shareable)

	viaClear := func(x, y float64) bool {
		if x < ctx.rect[0] || x > ctx.rect[2] || y < ctx.rect[1] || y > ctx.rect[3] {
			return false
		}
		for _, o := range padsAll {
			if o.Net == net {
				continue
			}
			if math.Hypot(x-o.X, y-o.Y) < padBand {
				return false
			}
		}
		for _, t := range tracks {
			if t.Net == net {
				continue
			}
			if segPtDist(x, y, t.X1, t.Y1, t.X2, t.Y2) < ctx.clearance+viaR+t.Width/2 {
				return false
			}
		}
		for _, v := range vias {
			if math.Hypot(x-v.X, y-v.Y) < viaBand {
				return false
			}
		}
		for _, pv := range priorVias {
			if math.Hypot(x-pv.X, y-pv.Y) < viaBand {
				return false
			}
		}
		for _, pv := range planned {
			if math.Hypot(x-pv.x, y-pv.y) < viaBand {
				return false
			}
		}
		return true
	}
	// The stub is short, but it can still graze an adjacent other-net pad —
	// reject a candidate whose stub does.
	stubClear := func(px, py, vx, vy float64) bool {
		band := ctx.clearance + nominalPadHalf + ctx.stubW/2
		for _, o := range padsAll {
			if o.Net == net {
				continue
			}
			if samePoint(o.X, o.Y, px, py) {
				continue
			}
			if segPtDist(o.X, o.Y, px, py, vx, vy) < band {
				return false
			}
		}
		return true
	}

	for _, p := range padsAll {
		if p.Net != net {
			continue
		}
		if p.Layer != 1 && p.Layer != 2 {
			res.SkippedTH++ // through-hole — already reaches the inner plane
			continue
		}

		// Share: a same-net via already planned (this run) within reach → fan in.
		shared := false
		for _, pv := range planned {
			if math.Hypot(p.X-pv.x, p.Y-pv.y) <= ctx.shareDist && stubClear(p.X, p.Y, pv.x, pv.y) {
				res.Stubs = append(res.Stubs, rtSeg{Net: net, X1: p.X, Y1: p.Y, X2: pv.x, Y2: pv.y, Layer: p.Layer, Width: ctx.stubW})
				res.Shared++
				shared = true
				break
			}
		}
		if !shared {
			// Or an existing same-net board via (a routing via is through-hole too).
			for _, v := range vias {
				if v.Net == net && math.Hypot(p.X-v.X, p.Y-v.Y) <= ctx.shareDist && stubClear(p.X, p.Y, v.X, v.Y) {
					res.Stubs = append(res.Stubs, rtSeg{Net: net, X1: p.X, Y1: p.Y, X2: v.X, Y2: v.Y, Layer: p.Layer, Width: ctx.stubW})
					res.Shared++
					shared = true
					break
				}
			}
		}
		if shared {
			continue
		}

		placedOne := false
		for _, off := range ctx.offsets {
			for _, d := range stitchDirs {
				vx, vy := p.X+d[0]*off, p.Y+d[1]*off
				if !viaClear(vx, vy) || !stubClear(p.X, p.Y, vx, vy) {
					continue
				}
				res.Vias = append(res.Vias, rtVia{Net: net, X: vx, Y: vy})
				res.Stubs = append(res.Stubs, rtSeg{Net: net, X1: p.X, Y1: p.Y, X2: vx, Y2: vy, Layer: p.Layer, Width: ctx.stubW})
				planned = append(planned, pt{vx, vy})
				placedOne = true
				break
			}
			if placedOne {
				break
			}
		}
		if !placedOne {
			res.Unplaced++
		}
	}
	return res
}
