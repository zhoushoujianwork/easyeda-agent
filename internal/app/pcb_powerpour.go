package app

// pcb_powerpour.go — 2-layer power-copper orchestration (能力 B / 电源走铺铜块).
//
// power-planes handles 4-layer boards (dedicated inner PLANEs). This is the 2-layer
// analog: instead of thin power tracks (the #1 DRC source — design-decisions.md),
// deliver each power net through copper POUR area:
//   - GND → a board-outline-fitted pour on the requested layer(s) (default both) —
//     the reference plane.
//   - each non-GND rail (3V3/5V/VBUS…) → a LOCAL pour bounded to the bbox of its own
//     pads (+ margin), on the top layer — copper where the rail is actually used.
//
// Every region is a DYNAMIC pour (retreats from other-net copper by the clearance
// rule), NOT a static fill — so different-net regions can overlap without shorting
// (a static fill would). Local rail pours keep a small rail from claiming the whole
// board (a full-outline pour per rail would fight GND for half the board).
//
// Reuses the pour-fit recipe (outline inset → rectangle → clear same-net → pour.create)
// and outlineRect/isGlobalNet/isGndNetName. planPowerPour is pure (unit-tested); the
// live fetch/draw is runPowerPour.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// powerPourPlan is one pour region the orchestrator will create.
type powerPourPlan struct {
	Net    string      `json:"net"`
	Layer  int         `json:"layer"`
	Kind   string      `json:"kind"` // "gnd-plane" | "rail-local"
	Pads   int         `json:"pads"`
	Points [][]float64 `json:"points"`
}

// railMargin is how far a rail's local pour extends past its pad bbox (mil) so the
// copper reaches slightly beyond the pads it feeds.
const railMargin = 60.0

// planPowerPour computes the pour regions for a 2-layer board. outline is the
// board's bbox ALREADY inset from the edge ([minX,minY,maxX,maxY]); gndLayers are
// the copper layer ids for the GND plane(s); railsMode is "pour" or "skip". Pure —
// no I/O. Nets that are not power (isGlobalNet) are ignored; a rail with <2 pads is
// skipped (nothing to distribute).
func planPowerPour(pads []pcbPadP, outline [4]float64, gndLayers []int, railsMode string, margin float64) []powerPourPlan {
	oMinX, oMinY, oMaxX, oMaxY := outline[0], outline[1], outline[2], outline[3]
	outRect := [][]float64{{oMinX, oMinY}, {oMaxX, oMinY}, {oMaxX, oMaxY}, {oMinX, oMaxY}}

	type acc struct {
		count                  int
		minX, minY, maxX, maxY float64
	}
	byNet := map[string]*acc{}
	var order []string
	for _, p := range pads {
		n := trimNet(p.Net)
		if n == "" || !isGlobalNet(n) {
			continue
		}
		a := byNet[n]
		if a == nil {
			a = &acc{minX: p.X, minY: p.Y, maxX: p.X, maxY: p.Y}
			byNet[n] = a
			order = append(order, n)
		}
		a.count++
		a.minX, a.maxX = minf(a.minX, p.X), maxf(a.maxX, p.X)
		a.minY, a.maxY = minf(a.minY, p.Y), maxf(a.maxY, p.Y)
	}
	// GND first (widest, poured full-board), then rails by pad count desc so the
	// heavier rail is created first (claims its area before lighter rails).
	sort.Slice(order, func(i, j int) bool {
		gi, gj := isGndNetName(order[i]), isGndNetName(order[j])
		if gi != gj {
			return gi
		}
		if byNet[order[i]].count != byNet[order[j]].count {
			return byNet[order[i]].count > byNet[order[j]].count
		}
		return order[i] < order[j]
	})

	var plans []powerPourPlan
	for _, n := range order {
		a := byNet[n]
		if isGndNetName(n) {
			for _, ly := range gndLayers {
				plans = append(plans, powerPourPlan{Net: n, Layer: ly, Kind: "gnd-plane", Pads: a.count, Points: cloneRect(outRect)})
			}
			continue
		}
		if railsMode == "skip" || a.count < 2 {
			continue
		}
		// Local rect = pad bbox + margin, clamped inside the inset outline.
		x0 := maxf(a.minX-margin, oMinX)
		y0 := maxf(a.minY-margin, oMinY)
		x1 := minf(a.maxX+margin, oMaxX)
		y1 := minf(a.maxY+margin, oMaxY)
		if x1-x0 < 2 || y1-y0 < 2 {
			continue // degenerate
		}
		plans = append(plans, powerPourPlan{
			Net: n, Layer: 1, Kind: "rail-local", Pads: a.count,
			Points: [][]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}},
		})
	}
	return plans
}

func cloneRect(r [][]float64) [][]float64 {
	out := make([][]float64, len(r))
	for i, p := range r {
		out[i] = []float64{p[0], p[1]}
	}
	return out
}

func trimNet(s string) string {
	// tiny local helper to avoid importing strings just for TrimSpace at call sites
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// parseGndLayers maps "both"/"top"/"bottom" to copper layer ids (TOP=1, BOTTOM=2).
func parseGndLayers(spec string) ([]int, error) {
	switch spec {
	case "both", "":
		return []int{1, 2}, nil
	case "top":
		return []int{1}, nil
	case "bottom":
		return []int{2}, nil
	default:
		return nil, fmt.Errorf("--gnd-layers must be both|top|bottom (got %q)", spec)
	}
}

// runPowerPour is the live orchestrator: read outline + pads, plan the pours, then
// (unless dry-run) clear same-net pours and create each region, optionally rebuilding.
func runPowerPour(cfg *appConfig, window, gndLayersSpec, railsMode string, margin, inset float64, replace, rebuild, dryRun bool, stdout, stderr io.Writer) error {
	// ADR-0004 Decision 4: dry-run 必须纯计算 —— 机械保证,Mutates 派发直接被拒。
	if dryRun {
		defer setDispatchDryRun(true)()
	}
	switch railsMode {
	case "pour", "skip":
	default:
		return fmt.Errorf("--rails must be pour|skip (got %q)", railsMode)
	}
	gndLayers, err := parseGndLayers(gndLayersSpec)
	if err != nil {
		return err
	}
	if inset <= 0 {
		inset = fetchPcbRules(cfg, window).copperToEdgeMil
	}
	outline, err := outlineRect(cfg, window, inset)
	if err != nil {
		return fmt.Errorf("%v — set a board outline first (`pcb outline-set`)", err)
	}
	if outline[2]-outline[0] <= 2 || outline[3]-outline[1] <= 2 {
		return fmt.Errorf("board too small for inset %.0f mil", inset)
	}
	pads, err := fetchPcbPads(cfg, window)
	if err != nil {
		return fmt.Errorf("read pads: %w", err)
	}
	plans := planPowerPour(pads, outline, gndLayers, railsMode, margin)
	if len(plans) == 0 {
		return fmt.Errorf("no power nets found to pour (nothing matches isGlobalNet: GND/VCC/3V3/…)")
	}

	if dryRun {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"dryRun": true, "inset": inset, "gndLayers": gndLayers, "railsMode": railsMode, "plans": plans})
	}

	// Clear existing same-net pours once per net (avoid stacking) before creating.
	if replace {
		cleared := map[string]bool{}
		for _, pl := range plans {
			if cleared[pl.Net] {
				continue
			}
			cleared[pl.Net] = true
			clearSameNetPours(cfg, window, pl.Net)
		}
	}

	var results []map[string]any
	var failureErr error
	created, failed := 0, 0
	for _, pl := range plans {
		payload := map[string]any{"points": pl.Points, "net": pl.Net, "layer": pl.Layer}
		res, err := requestAction(cfg, "pcb.pour.create", window, payload)
		if err != nil {
			failureErr = err
			failed++
			failure := map[string]any{"net": pl.Net, "layer": pl.Layer, "kind": pl.Kind, "error": err.Error()}
			if res != nil {
				failure["result"] = res.Result
			}
			results = append(results, failure)
			break // Do not build more copper after an unverified boundary.
		}
		created++
		results = append(results, map[string]any{"net": pl.Net, "layer": pl.Layer, "kind": pl.Kind, "pads": pl.Pads, "result": res.Result})
	}

	rebuilt := false
	if rebuild && created > 0 && failed == 0 {
		if _, err := requestAction(cfg, "pcb.pour.rebuild", window, nil); err != nil {
			fmt.Fprintf(stderr, "warning: pour-rebuild failed (%v) — run `pcb pour-rebuild` after `doc reload`\n", err)
		} else {
			rebuilt = true
		}
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{
		"ok": failed == 0, "created": created, "failed": failed,
		"gndLayers": gndLayers, "railsMode": railsMode, "inset": inset, "rebuilt": rebuilt,
		"pours": results,
	}); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("power-pour: stopped after an unverified or failed boundary; inspect returned results before retrying: %w", failureErr)
	}
	return nil
}

// clearSameNetPours deletes every pour bound to `net` (best-effort; ignores errors).
//
// 调用方按网逐条清理；每次都重新枚举刚被 delete 改过的 pour 表，避免旧铺铜与新铺铜叠加。
func clearSameNetPours(cfg *appConfig, window, net string) {
	lr, err := requestReadAfterWrite(cfg, "pcb.pour.list", window, nil,
		"power-pour 写后回读:逐网清理旧铺铜时枚举剩余 pour")
	if err != nil || lr == nil {
		return
	}
	pours, _ := lr.Result["pours"].([]any)
	var ids []any
	for _, pi := range pours {
		if pm, ok := pi.(map[string]any); ok && asString(pm["net"]) == net {
			if id := asString(pm["primitiveId"]); id != "" {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) > 0 {
		requestAction(cfg, "pcb.pour.delete", window, map[string]any{"primitiveIds": ids})
	}
}
