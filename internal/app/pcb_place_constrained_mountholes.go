package app

// pcb_place_constrained_mountholes.go — `pcb mount-holes` (issue #102): place
// M3 mounting holes at the board-outline corners, collision-checked against
// component bboxes. Each hole is a near-circular polygon fill on the MULTI
// layer (12) — the exact primitive `pcb slot` mills (manufacturing emits it as
// a BoardCutout) — so `readCpHoles` (#104) sees them as Tier-1 obstacles for
// `pcb place-constrained`, and `pcb check`'s clearance rule (fetchPcbSlots)
// keeps copper off the milled edge.
//
// Defaults (mil) follow pcb-layout-conventions §2.3 + issue #102:
//   - Ø3.2 mm ≈ 126 mil — the M3 clearance hole
//   - center inset ~5 mm ≈ 197 mil from each board edge
//   - keep-out radius = max(hole R + 40, washer/head R 118) — M3 head ≈ Ø6 mm

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

const (
	mhDefaultDiaMil   = 126.0 // M3 clearance hole Ø3.2mm
	mhDefaultInsetMil = 197.0 // hole CENTER ~5mm from each board edge
	mhWasherRadiusMil = 118.0 // M3 head/washer ≈ Ø6mm (conventions §2.3)
	mhDrillMarginMil  = 40.0  // hole radius + drill margin (conventions §2.3)
	mhPolygonSides    = 16    // near-circle N-gon (native arcs don't commit)
)

// mhClearanceRadius is the keep-out radius around a mounting-hole center:
// max(hole radius + drill margin, washer/head radius) per conventions §2.3.
func mhClearanceRadius(diaMil float64) float64 {
	return math.Max(diaMil/2+mhDrillMarginMil, mhWasherRadiusMil)
}

// mhCandidate is one corner's verdict. Status: "plan" (will place), "exists"
// (a cutout already sits there — idempotent skip), "skip-conflict" (a component
// bbox intrudes into the keep-out circle), then "placed"/"failed" after apply.
type mhCandidate struct {
	Corner string  `json:"corner"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Status string  `json:"status"`
	Reason string  `json:"reason,omitempty"`
}

// mhCirclePolygon renders the hole as a regular N-gon (near-circle) centered on
// (cx,cy) — manufacturing treats any MULTI-layer fill as a BoardCutout, and
// native arcs do not commit on this build, so a polygon is the reliable shape.
func mhCirclePolygon(cx, cy, diaMil float64, n int) [][]float64 {
	r := diaMil / 2
	pts := make([][]float64, 0, n)
	for i := 0; i < n; i++ {
		a := 2 * math.Pi * float64(i) / float64(n)
		x := math.Round((cx+r*math.Cos(a))*100) / 100
		y := math.Round((cy+r*math.Sin(a))*100) / 100
		pts = append(pts, []float64{x, y})
	}
	return pts
}

// mhCircleRectIntersects reports whether the circle (cx,cy,r) touches the
// axis-aligned rect [x0,y0 → x1,y1] (closest-point test).
func mhCircleRectIntersects(cx, cy, r, x0, y0, x1, y1 float64) bool {
	dx := cx - math.Max(x0, math.Min(cx, x1))
	dy := cy - math.Max(y0, math.Min(cy, y1))
	return dx*dx+dy*dy <= r*r
}

// planMountHoles is the pure planning half (no I/O, unit-tests): board-outline
// bbox + requested corners + component bboxes + existing cutouts + routed
// copper → per-corner verdicts. Coordinates are y-up (EasyEDA PCB): top = maxY.
// clearanceMil overrides the fastener keep-out radius (≤0 → auto: max(hole
// R+40, washer 118) per conventions §2.3) — for a smaller-head fastener the
// caller knowingly accepts.
//
// tracks/vias + copperBand are the #122 copper gate: a track or via whose
// copper edge lands within copperBand of the hole's milled edge would be native
// DRC's Slot Region to Track the moment the hole exists. The router hard-avoids
// existing slots (hopFeasible), so mount-holes placed AFTER routing must
// equally avoid existing copper — a corner that fails is skip-conflict, never
// force-milled. copperBand mirrors `pcb check`'s slot judge: max(clearance, 8).
func planMountHoles(board cpRect, corners []string, diaMil, insetMil, clearanceMil float64,
	comps []cpComp, existing []cpHole, tracks []pcbTrack, vias []pcbViaP, copperBand float64) ([]mhCandidate, error) {
	if diaMil <= 0 {
		return nil, fmt.Errorf("--dia must be > 0 (got %g)", diaMil)
	}
	if insetMil < diaMil/2 {
		return nil, fmt.Errorf("--inset %.0f < hole radius %.0f — the hole would cut through the board edge (need inset ≥ dia/2)", insetMil, diaMil/2)
	}
	w, h := board.x1-board.x0, board.y1-board.y0
	if need := 2*insetMil + diaMil; w < need || h < need {
		return nil, fmt.Errorf("board %.0f×%.0f mil too small for corner holes at inset %.0f (needs ≥ %.0f mil per axis — shrink --inset/--dia or grow the outline)", w, h, insetMil, need)
	}
	centers := map[string][2]float64{
		"tl": {board.x0 + insetMil, board.y1 - insetMil},
		"tr": {board.x1 - insetMil, board.y1 - insetMil},
		"bl": {board.x0 + insetMil, board.y0 + insetMil},
		"br": {board.x1 - insetMil, board.y0 + insetMil},
	}
	rClear := clearanceMil
	if rClear <= 0 {
		rClear = mhClearanceRadius(diaMil)
	} else if rClear < diaMil/2 {
		return nil, fmt.Errorf("--clearance %.0f < hole radius %.0f — the keep-out cannot be smaller than the hole itself", clearanceMil, diaMil/2)
	}
	seen := map[string]bool{}
	var out []mhCandidate
	for _, c := range corners {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" || seen[c] {
			continue
		}
		ctr, ok := centers[c]
		if !ok {
			return nil, fmt.Errorf("unknown corner %q (want a subset of: tl,tr,bl,br)", c)
		}
		seen[c] = true
		cand := mhCandidate{Corner: c, X: ctr[0], Y: ctr[1], Status: "plan"}
		// Idempotency: an existing cutout near this center = hole already there.
		for _, ex := range existing {
			if math.Hypot(ex.x-ctr[0], ex.y-ctr[1]) <= diaMil {
				cand.Status = "exists"
				cand.Reason = fmt.Sprintf("existing cutout at (%.0f,%.0f) — nothing to do", ex.x, ex.y)
				break
			}
		}
		// Collision: keep-out circle (hole + washer head) vs every footprint bbox.
		if cand.Status == "plan" {
			for _, cp := range comps {
				if !cp.hasBBox {
					continue
				}
				if mhCircleRectIntersects(ctr[0], ctr[1], rClear, cp.minX, cp.minY, cp.maxX, cp.maxY) {
					cand.Status = "skip-conflict"
					cand.Reason = fmt.Sprintf("keep-out circle R%.0f overlaps %s bbox [%.0f,%.0f → %.0f,%.0f] — move the part, adjust --inset, or knowingly accept a smaller fastener head via --clearance",
						rClear, cp.designator, cp.minX, cp.minY, cp.maxX, cp.maxY)
					break
				}
			}
		}
		// Copper collision (#122): routed tracks/vias vs the hole's milled edge.
		if cand.Status == "plan" && copperBand > 0 {
			for _, t := range tracks {
				if d := segPointDist(t.X1, t.Y1, t.X2, t.Y2, ctr[0], ctr[1]) - diaMil/2 - t.Width/2; d < copperBand {
					cand.Status = "skip-conflict"
					cand.Reason = fmt.Sprintf("track (net %s) runs %.1fmil from the hole's milled edge — under the %.0fmil copper-to-cutout rule (Slot Region to Track); rip/reroute it or pick another corner",
						t.Net, math.Max(d, 0), copperBand)
					break
				}
			}
		}
		if cand.Status == "plan" && copperBand > 0 {
			for _, v := range vias {
				if d := math.Hypot(v.X-ctr[0], v.Y-ctr[1]) - diaMil/2 - v.Dia/2; d < copperBand {
					cand.Status = "skip-conflict"
					cand.Reason = fmt.Sprintf("via (net %s) sits %.1fmil from the hole's milled edge — under the %.0fmil copper-to-cutout rule; remove it or pick another corner",
						v.Net, math.Max(d, 0), copperBand)
					break
				}
			}
		}
		out = append(out, cand)
	}
	return out, nil
}

// runPcbMountHoles is the live half: read the board outline + component bboxes
// + existing cutouts, plan, then mill each planned hole via pcb.fill.create
// (layer 12 — same as `pcb slot`). Conflicting corners are warned + skipped,
// never force-placed (issue #102: a blind hole landed on C1).
func runPcbMountHoles(cfg *appConfig, window string, dia, inset, clearance float64,
	cornersCSV string, dryRun bool, stdout, stderr io.Writer) error {
	r, err := outlineRect(cfg, window, 0)
	if err != nil {
		return fmt.Errorf("mount-holes needs a board outline: %v (run `easyeda pcb outline-fit` or `pcb outline set` first)", err)
	}
	board := cpRect{r[0], r[1], r[2], r[3]}

	res, cerr := requestAction(cfg, "pcb.components.list", window, map[string]any{"includeBBox": true})
	if cerr != nil {
		return fmt.Errorf("cannot read component bboxes for the collision check: %w", cerr)
	}
	comps := parseCpComps(res.Result)
	existing := readCpHoles(cfg, window)
	// #122: existing routed copper is a placement constraint too (best-effort —
	// an unrouted board simply has none). Band mirrors the check's slot judge.
	tracks, terr := fetchPcbTracks(cfg, window)
	if terr != nil {
		tracks = nil
	}
	vias, verr := fetchPcbVias(cfg, window)
	if verr != nil {
		vias = nil
	}
	copperBand := math.Max(fetchPcbRules(cfg, window).clearanceMil, 8)

	plan, err := planMountHoles(board, strings.Split(cornersCSV, ","), dia, inset, clearance, comps, existing, tracks, vias, copperBand)
	if err != nil {
		return err
	}

	placed := make([]map[string]any, 0)
	failed := 0
	for i := range plan {
		switch plan[i].Status {
		case "skip-conflict":
			fmt.Fprintf(stderr, "warning: corner %s skipped — %s\n", plan[i].Corner, plan[i].Reason)
			continue
		case "plan":
		default:
			continue
		}
		if dryRun {
			continue
		}
		pts := mhCirclePolygon(plan[i].X, plan[i].Y, dia, mhPolygonSides)
		cres, perr := requestAction(cfg, "pcb.fill.create", window,
			map[string]any{"points": pts, "layer": 12})
		if perr != nil {
			plan[i].Status = "failed"
			plan[i].Reason = perr.Error()
			failed++
			continue
		}
		plan[i].Status = "placed"
		placed = append(placed, map[string]any{
			"corner": plan[i].Corner, "x": plan[i].X, "y": plan[i].Y,
			"primitiveId": asString(mnav(cres.Result, "primitiveId")),
		})
	}

	out := map[string]any{
		"ok": failed == 0, "dryRun": dryRun,
		"board":              map[string]any{"x0": board.x0, "y0": board.y0, "x1": board.x1, "y1": board.y1},
		"diaMil":   dia,
		"insetMil": inset,
		"clearanceRadiusMil": func() float64 {
			if clearance > 0 {
				return clearance
			}
			return mhClearanceRadius(dia)
		}(),
		"holes":              plan,
		"placed":             len(placed),
		"placedHoles":        placed,
	}
	if !dryRun && len(placed) > 0 {
		out["note"] = "孔已放(MULTI 层挖槽,同 `pcb slot`)——`pcb place-constrained` 把它们当 Tier-1 障碍避让;" +
			"验证 `pcb fill list --layer 12`,删除 `pcb fill delete`,落盘 `pcb save`"
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
