package app

// pcb power-planes — the 4-layer power-distribution heuristic (#26 increment 2).
//
// The 2-layer pour conflict (two power nets can't both connect on one shared layer —
// ceshi stranded 5 of the 3V3 pads) is solved properly by dedicated inner planes.
// This command, validated on ceshi (DRC 31 → 10 → 3, No-Connection → 0):
//   1. ensures the board has ≥4 copper layers (pcb.stackup.set),
//   2. assigns GND to one inner layer and the power nets to another,
//   3. via-stitches every power/ground pad DOWN to its plane (a through via on that
//      net — the connection point the inner pour needs; without it the inner pour is
//      all isolated islands and deposits nothing),
//   4. pours each net on its inner layer, then rebuilds.
// The pour MUST come after the vias (empty otherwise). Power/ground nets are the
// isGlobalNet set (GND/3V3/VCC/…); everything else stays a routed signal.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type ppNet struct {
	Net   string      `json:"net"`
	Layer int         `json:"layer"`
	Pads  [][2]float64 `json:"-"`
	Vias  int         `json:"viasPlaced"`
	Poured bool       `json:"poured"`
}

// runPowerPlanes orchestrates the 4-layer power-plane build. gndLayer/powerLayer are
// inner-layer ids (15=Inner1, 16=Inner2 on a 4-layer board). gndAsPlane flips the GND
// inner layer to 内电层/PLANE after pouring (the verified pour-while-SIGNAL → flip →
// rebuild recipe; DRC stays clean — see the pcb-inner-plane-fill memory).
func runPowerPlanes(cfg *appConfig, window string, gndLayer, powerLayer int, gndAsPlane, dryRun bool, stdout, stderr io.Writer) error {
	// 1. Read the board: pads (grouped by power net), routed tracks, existing vias,
	//    and the live spacing rule — the stitch planner scores against all of them.
	pads, err := fetchPcbPads(cfg, window)
	if err != nil {
		return fmt.Errorf("read pads: %w", err)
	}
	byNet := map[string][][2]float64{}
	for _, p := range pads {
		if isGlobalNet(p.Net) {
			byNet[p.Net] = append(byNet[p.Net], [2]float64{p.X, p.Y})
		}
	}
	if len(byNet) == 0 {
		return fmt.Errorf("no power/ground nets found (nothing matches isGlobalNet: GND/VCC/3V3/…)")
	}
	tracks, terr := fetchPcbTracks(cfg, window)
	if terr != nil {
		tracks = nil // clearance scoring degrades, placement still works
	}
	boardVias, verr := fetchPcbVias(cfg, window)
	if verr != nil {
		boardVias = nil
	}
	rules := fetchPcbRules(cfg, window)

	// 2. Assign layers: GND → gndLayer. Of the non-GND power nets, only the LARGEST
	//    (most pads) pours on powerLayer — two nets on one plane layer re-create the
	//    split conflict (the second pour is carved to islands → opens). The rest are
	//    ROUTED as fat tracks on the outer layers instead (they are small, local nets
	//    like a +5V entry).
	nets := make([]string, 0, len(byNet))
	for n := range byNet {
		nets = append(nets, n)
	}
	sort.Slice(nets, func(i, j int) bool { // GND first, then by pad count desc, name tiebreak
		gi, gj := isGndNetName(nets[i]), isGndNetName(nets[j])
		if gi != gj {
			return gi
		}
		if len(byNet[nets[i]]) != len(byNet[nets[j]]) {
			return len(byNet[nets[i]]) > len(byNet[nets[j]])
		}
		return nets[i] < nets[j]
	})
	var plan []ppNet
	var routeAsTracks []string
	var warnings []string
	powerAssigned := 0
	for _, n := range nets {
		switch {
		case isGndNetName(n):
			plan = append(plan, ppNet{Net: n, Layer: gndLayer, Pads: byNet[n]})
		case powerAssigned == 0:
			plan = append(plan, ppNet{Net: n, Layer: powerLayer, Pads: byNet[n]})
			powerAssigned++
		default:
			routeAsTracks = append(routeAsTracks, n)
			warnings = append(warnings, fmt.Sprintf("net %q not poured (plane layer %d already carries a power net) — routed as tracks instead", n, powerLayer))
		}
	}

	// Board outline → the pour rectangle (inset a hair so the plane sits inside).
	rect, rerr := outlineRect(cfg, window, 10)
	if rerr != nil {
		return fmt.Errorf("read board outline (needed for the plane pour): %w", rerr)
	}

	if dryRun {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"dryRun": true, "plan": plan, "routeAsTracks": routeAsTracks, "warnings": warnings, "pourRect": rect, "gndAsPlane": gndAsPlane, "gndLayer": gndLayer})
	}

	// 3. Ensure ≥4 copper layers.
	if _, err := requestAction(cfg, "pcb.stackup.set", window, map[string]any{"count": 4}); err != nil {
		return fmt.Errorf("set 4 copper layers: %w", err)
	}

	// 3a. The pour recipe is pour-while-SIGNAL → flip → rebuild. On a re-run the
	//     GND layer may ALREADY be 内电层/PLANE (a fresh pour directly on a PLANE
	//     layer silently lands netless on L1 — the known bad path), so flip it
	//     back to SIGNAL first.
	if planes, perr := fetchPcbPlaneLayers(cfg, window); perr == nil {
		for _, pl := range planes {
			if pl.Layer == gndLayer {
				if _, err := requestAction(cfg, "pcb.stackup.set", window, map[string]any{
					"layers": []map[string]any{{"id": gndLayer, "type": "signal"}},
				}); err != nil {
					fmt.Fprintf(stderr, "power-planes: could not flip layer %d back to SIGNAL before pouring: %v\n", gndLayer, err)
				}
				break
			}
		}
	}

	// 4. Per poured net: clearance-aware stitch (offset via + stub, shared among
	//    close pads, TH pads skipped), then pour on the net's inner layer.
	sctx := defaultStitchCtx(rules.clearanceMil, rect)
	var priorVias []rtVia
	stitchStats := map[string]map[string]int{}
	for i := range plan {
		p := &plan[i]
		st := planStitchViasForNet(p.Net, pads, tracks, boardVias, priorVias, sctx)
		for _, v := range st.Vias {
			if _, err := requestAction(cfg, "pcb.via.create", window, map[string]any{"x": v.X, "y": v.Y, "net": v.Net, "holeDiameter": 12, "diameter": sctx.viaDia}); err != nil {
				continue // best-effort; a failed via just leaves that pad for DRC
			}
			p.Vias++
		}
		for _, s := range st.Stubs {
			_, _ = requestAction(cfg, "pcb.line.create", window, map[string]any{"startX": s.X1, "startY": s.Y1, "endX": s.X2, "endY": s.Y2, "net": s.Net, "layer": s.Layer, "lineWidth": s.Width})
		}
		priorVias = append(priorVias, st.Vias...)
		stitchStats[p.Net] = map[string]int{"vias": len(st.Vias), "stubs": len(st.Stubs), "shared": st.Shared, "throughHole": st.SkippedTH, "unplaced": st.Unplaced}
		if st.Unplaced > 0 {
			warnings = append(warnings, fmt.Sprintf("net %q: %d pad(s) had no clearance-clean via spot — left unstitched (check DRC)", p.Net, st.Unplaced))
		}
		pres, perr := requestAction(cfg, "pcb.pour.create", window, map[string]any{
			"net": p.Net, "layer": p.Layer, "points": rectCorners(rect[0], rect[1], rect[2], rect[3]),
		})
		if perr == nil && pres != nil {
			if b, ok := mnav(pres.Result, "poured").(bool); ok {
				p.Poured = b
			}
		}
	}

	// 4a. Route the leftover small power nets as fat tracks (the multilayer
	//     short-route planner, power width) instead of fighting for the plane layer.
	routedTrackNets := map[string]any{}
	if len(routeAsTracks) > 0 {
		leftover := map[string]bool{}
		for _, n := range routeAsTracks {
			leftover[n] = true
		}
		// Everything NOT leftover is "already handled" for the planner. The
		// leftover nets are force-routed: parseRoutedNets counts ANY track on the
		// net as routed, and a stitch stub / stray segment would mask a net that
		// is not actually connected pad-to-pad.
		already := map[string]bool{}
		if lr, lerr := requestAction(cfg, "pcb.line.list", window, nil); lerr == nil {
			already = parseRoutedNets(lr.Result)
		}
		for _, pp := range plan {
			already[pp.Net] = true
		}
		for n := range leftover {
			delete(already, n)
		}
		comps := padsToComps(pads)
		opt := defaultRtOptions()
		opt.skipPower = false
		opt.clearance = rules.clearanceMil
		opt.powerWidth = rules.clampWidth(rules.powerWidthMil)
		// The board already carries signal routing + the stitch copper drawn above —
		// the fat power tracks must stay clear of all of it.
		for _, t := range tracks {
			opt.existing = append(opt.existing, rtSeg{Net: t.Net, X1: t.X1, Y1: t.Y1, X2: t.X2, Y2: t.Y2, Layer: t.Layer, Width: t.Width})
		}
		for _, v := range boardVias {
			opt.existingVias = append(opt.existingVias, obVia{net: v.Net, x: v.X, y: v.Y, r: v.Dia / 2})
		}
		for _, v := range priorVias {
			opt.existingVias = append(opt.existingVias, obVia{net: v.Net, x: v.X, y: v.Y, r: sctx.viaDia / 2})
		}
		segs, rvias, _ := planShortRoutes(comps, already, opt)
		drawn, viasDrawn := 0, 0
		for _, s := range segs {
			if !leftover[s.Net] {
				continue
			}
			if _, err := requestAction(cfg, "pcb.line.create", window, map[string]any{"startX": s.X1, "startY": s.Y1, "endX": s.X2, "endY": s.Y2, "net": s.Net, "layer": s.Layer, "lineWidth": s.Width}); err == nil {
				drawn++
			}
		}
		for _, v := range rvias {
			if !leftover[v.Net] {
				continue
			}
			if _, err := requestAction(cfg, "pcb.via.create", window, map[string]any{"x": v.X, "y": v.Y, "net": v.Net}); err == nil {
				viasDrawn++
			}
		}
		routedTrackNets = map[string]any{"nets": routeAsTracks, "tracks": drawn, "vias": viasDrawn}
	}

	// 4b. Flip the GND inner layer to 内电层/PLANE. The verified recipe is pour-while-
	//     SIGNAL (done above) → flip type → rebuild; the net-bound fill survives and
	//     DRC stays clean. The power layer stays SIGNAL so its net pour behaves as an
	//     ordinary positive plane (matches the customer stackup: GND=内电层, VCC=信号层).
	planeFlipped := false
	if gndAsPlane {
		if _, err := requestAction(cfg, "pcb.stackup.set", window, map[string]any{
			"layers": []map[string]any{{"id": gndLayer, "type": "plane"}},
		}); err != nil {
			fmt.Fprintf(stderr, "power-planes: could not set layer %d to 内电层/PLANE: %v\n", gndLayer, err)
		} else {
			planeFlipped = true
		}
	}

	// 5. Rebuild all pours so they reflow around the vias/obstacles (and settle the
	//    GND layer as a proper inner plane after the type flip).
	if _, err := requestAction(cfg, "pcb.pour.rebuild", window, nil); err != nil {
		fmt.Fprintf(stderr, "power-planes: pour rebuild failed: %v\n", err)
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"ok": true, "gndLayer": gndLayer, "powerLayer": powerLayer,
		"gndAsPlane": gndAsPlane, "gndPlaneFlipped": planeFlipped,
		"planes": plan, "stitch": stitchStats, "routedAsTracks": routedTrackNets,
		"warnings": warnings,
	})
}

// padsToComps groups flat pads back into minimal apComp containers — the shape
// planShortRoutes plans over (only .designator and .pads matter to it).
func padsToComps(pads []pcbPadP) []apComp {
	byDes := map[string][]apPad{}
	var order []string
	for _, p := range pads {
		if _, seen := byDes[p.Designator]; !seen {
			order = append(order, p.Designator)
		}
		byDes[p.Designator] = append(byDes[p.Designator], apPad{num: p.Number, net: p.Net, x: p.X, y: p.Y, layer: p.Layer})
	}
	comps := make([]apComp, 0, len(order))
	for _, d := range order {
		comps = append(comps, apComp{designator: d, pads: byDes[d]})
	}
	return comps
}

// isGndNetName reports whether a net is a ground net (for plane assignment).
func isGndNetName(net string) bool {
	return strings.Contains(strings.ToLower(net), "gnd")
}

// outlineRect returns the board outline bbox inset by `inset` mil as [minX,minY,maxX,maxY].
func outlineRect(cfg *appConfig, window string, inset float64) ([4]float64, error) {
	res, err := requestAction(cfg, "pcb.outline.get", window, nil)
	if err != nil || res == nil {
		return [4]float64{}, fmt.Errorf("outline.get failed: %v", err)
	}
	bb, ok := mnav(res.Result, "bbox").(map[string]any)
	if !ok {
		return [4]float64{}, fmt.Errorf("no board outline set — run `pcb outline-fit` first")
	}
	minX, _ := asFloatOK(bb["minX"])
	minY, _ := asFloatOK(bb["minY"])
	maxX, _ := asFloatOK(bb["maxX"])
	maxY, _ := asFloatOK(bb["maxY"])
	return [4]float64{minX + inset, minY + inset, maxX - inset, maxY - inset}, nil
}
