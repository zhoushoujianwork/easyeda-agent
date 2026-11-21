package app

import (
	"context"
	"fmt"
	"math"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
)

// Exact supported pad geometry; unknown geometry is never treated as free space.
func pcbExactPadSegmentGap(p pcbPadP, a, z [2]float64) (float64, error) {
	if p.Shape == "POLYGON" && p.exactClearanceKnown() {
		return copperAreaSegmentDistance(pcbCopperArea{Contours: p.Contours}, a, z), nil
	}
	if !p.ShapeOK {
		return 0, fmt.Errorf("pad %s.%s geometry unknown", p.Designator, p.Number)
	}
	n := pcbNetPathNode{x: p.X, y: p.Y, w: p.ShapeW, h: p.ShapeH, rotation: p.Rotation, shape: p.Shape, shapeRound: p.ShapeRound, shapeSides: p.ShapeSides}
	x, y := netPathPadLocal(n, a[0], a[1])
	u, v := netPathPadLocal(n, z[0], z[1])
	switch p.Shape {
	case "RECT":
		r := p.ShapeRound
		return math.Max(0, rectSegDist(-p.ShapeW/2+r, -p.ShapeH/2+r, p.ShapeW/2-r, p.ShapeH/2-r, x, y, u, v)-r), nil
	case "OVAL":
		i, j, k, l, r := netPathOvalSpine(n)
		return math.Max(0, segSegDist(x, y, u, v, i, j, k, l)-r), nil
	case "ELLIPSE":
		if math.Abs(p.ShapeW-p.ShapeH) < 1e-6 {
			return math.Max(0, segPtDist(0, 0, x, y, u, v)-p.ShapeW/2), nil
		}
	}
	return 0, fmt.Errorf("pad %s.%s exact clearance for %s is unsupported", p.Designator, p.Number, p.Shape)
}

func crystalRouteClear(net string, layer int, width float64, snap *boardSnapshot, pads []pcbPadP, tracks []pcbTrack, arcs []pcbArc, vias []pcbViaP, areas []pcbCopperArea) (func([2]float64, [2]float64) bool, error) {
	var ps []pcbPadP
	for _, p := range pads {
		if p.Net == net || !padLayerMatches(p.Layer, layer) {
			continue
		}
		if _, err := pcbExactPadSegmentGap(p, [2]float64{p.X, p.Y}, [2]float64{p.X, p.Y}); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	ts := append([]pcbTrack(nil), tracks...)
	for _, arc := range arcs {
		if arc.Net == net || arc.Layer != layer {
			continue
		}
		curve, _, err := flattenNetPathArc(arc)
		if err != nil {
			return nil, err
		}
		for i := 0; i+1 < len(curve); i++ {
			ts = append(ts, pcbTrack{Net: arc.Net, Layer: arc.Layer, Width: arc.Width, X1: curve[i].x, Y1: curve[i].y, X2: curve[i+1].x, Y2: curve[i+1].y})
		}
	}
	return func(a, z [2]float64) bool {
		margin := width/2 + snap.Rules.ClearanceMil
		for _, p := range ps {
			if math.Max(a[0], z[0])+margin < p.X-p.W/2 || math.Min(a[0], z[0])-margin > p.X+p.W/2 || math.Max(a[1], z[1])+margin < p.Y-p.H/2 || math.Min(a[1], z[1])-margin > p.Y+p.H/2 {
				continue
			}
			d, _ := pcbExactPadSegmentGap(p, a, z)
			if d < margin-netPathGeomEps {
				return false
			}
		}
		for _, t := range ts {
			if t.Net == net || t.Layer != layer {
				continue
			}
			if segSegDist(a[0], a[1], z[0], z[1], t.X1, t.Y1, t.X2, t.Y2) < width/2+t.Width/2+math.Max(snap.Rules.ClearanceMil, snap.Rules.ClearanceTrackTrackMil)-netPathGeomEps {
				return false
			}
		}
		for _, v := range vias {
			if v.Net == net {
				continue
			}
			if segPtDist(v.X, v.Y, a[0], a[1], z[0], z[1]) < width/2+v.Dia/2+snap.Rules.ClearanceMil-netPathGeomEps {
				return false
			}
		}
		for _, area := range areas {
			if area.Net != net && area.Layer == layer && copperAreaSegmentDistance(area, a, z) < margin-netPathGeomEps {
				return false
			}
		}
		if snap.Outline == nil || !snap.Outline.containsPoint(a[0], a[1]) || !snap.Outline.containsPoint(z[0], z[1]) {
			return false
		}
		for i, p := range snap.Outline.Points {
			q := snap.Outline.Points[(i+1)%len(snap.Outline.Points)]
			if segSegDist(a[0], a[1], z[0], z[1], p[0], p[1], q[0], q[1]) < width/2+snap.Rules.CopperToEdgeMil-netPathGeomEps {
				return false
			}
		}
		return true
	}, nil
}

// Keep board-specific callers stable while sharing the public pure Go kernel.
func crystalGridRoute(a, z [2]float64, step, detour float64, clear func([2]float64, [2]float64) bool) ([][2]float64, error) {
	result, err := pcbrouting.Solve(context.Background(), pcbrouting.Request{
		From: a, To: z, Step: step, MaxDetour: detour, MaxStates: pcbrouting.MaxSearchStates,
	}, clear)
	if err != nil {
		return nil, err
	}
	if result.Status != pcbrouting.Found {
		return nil, fmt.Errorf("no single-layer zero-via path within %.3fmil detour: %s (%d states)", detour, result.Reason, result.States)
	}
	return result.Points, nil
}

func crystalCompressRoute(p [][2]float64) [][2]float64 {
	return pcbrouting.Compress(p)
}

func solveCrystalMeasuredSignals(b *pcbLayoutModuleBundle, g *pcbCrystalGuardSpec, snap *boardSnapshot, pads []pcbPadP, tracks []pcbTrack, arcs []pcbArc, vias []pcbViaP, areas []pcbCopperArea) error {
	// Enumerate both net orders; no irreversible sequential preference.
	original := append([]pcbModuleRoute(nil), b.SignalRoutes...)
	var failures []string
	for _, order := range [][]int{{0, 1, 2, 3}, {2, 3, 0, 1}} {
		routes := append([]pcbModuleRoute(nil), original...)
		obstacles := append([]pcbTrack(nil), tracks...)
		good := true
		for _, index := range order {
			r := &routes[index]
			clear, err := crystalRouteClear(r.Net, r.Layer, r.WidthMil, snap, pads, obstacles, arcs, vias, areas)
			if err != nil {
				return err
			}
			waypoints := [][2]float64{r.Points[0], r.Points[len(r.Points)-1]}
			if r.Role == "signal-main" {
				// Keep the two entry corridors together at their measured MCU
				// pad spacing. Independent shortest routes may split the entry
				// window across half the fence and leave an unfillable gap.
				var memberTop float64 = -math.MaxFloat64
				for _, regionPad := range pads {
					owned := false
					for _, ref := range b.OwnedRefs {
						if regionPad.Designator == ref {
							owned = true
						}
					}
					if owned {
						memberTop = math.Max(memberTop, regionPad.Y+regionPad.H/2)
					}
				}
				start, end := waypoints[0], waypoints[1]
				entryY := memberTop + g.GuardGapMil + g.GuardWidthMil/2
				if entryY < start[1] {
					waypoints = [][2]float64{start, {start[0], entryY}, end}
				}
			}
			var points [][2]float64
			for i := 0; i+1 < len(waypoints); i++ {
				segment, e := crystalGridRoute(waypoints[i], waypoints[i+1], g.ProtectionSearch.StepMil, g.ProtectionSearch.MaxDetourMil, clear)
				if e != nil {
					err = e
					break
				}
				if len(points) == 0 {
					points = segment
				} else {
					points = append(points, segment[1:]...)
				}
			}
			if err == nil {
				var ok bool
				points, ok = crystalBevelRoute(crystalCompressRoute(points), clear)
				if !ok {
					err = fmt.Errorf("entry join cannot maintain 45-degree turns")
				}
			}
			if err != nil {
				failures = append(failures, r.ID+": "+err.Error())
				good = false
				break
			}
			r.Points = crystalStringPull45(points, g.ProtectionSearch.MaxDetourMil, clear)
			points = r.Points
			for i := 0; i+1 < len(points); i++ {
				a, z := points[i], points[i+1]
				obstacles = append(obstacles, pcbTrack{ID: r.ID, Net: r.Net, Layer: r.Layer, Width: r.WidthMil, X1: a[0], Y1: a[1], X2: z[0], Y2: z[1]})
			}
		}
		if good {
			b.SignalRoutes = routes
			return nil
		}
	}
	return fmt.Errorf("measured signal route search rejected: %v", failures)
}

// Shared chamfer implementation; board-specific clearance is supplied by caller.
func crystalBevelRoute(path [][2]float64, clear func([2]float64, [2]float64) bool) ([][2]float64, bool) {
	return pcbrouting.Bevel45(path, clear)
}
