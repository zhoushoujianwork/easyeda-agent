package app

// pcb layout-lint — PCB placement quality + ROUTABILITY prediction.
//
// `sch layout-lint` catches component overlap on the schematic; this is its PCB
// sibling, plus the thing that actually predicts routing pain BEFORE you route:
// the ratsnest. It computes, over signal nets only (power/GND are poured, not
// routed as tracks — so they'd swamp the metric), a per-net minimum spanning tree
// and counts how many cross-net ratline segments GEOMETRICALLY CROSS. Crossings are
// the classic single-layer routability killer — two nets whose shortest links cross
// can't both stay on one layer without a via/detour. Combined with overlap (fatal)
// and outside-outline, that yields a 0-100 score to gate/compare placements.
//
// Pure core here (unit-testable, no I/O); the CLI command + live fetch/render is in
// cmd_pcb.go / the runner below. Reuses overlapExtent/rectGap/round2 from
// cmd_sch_layout.go and isGlobalNet from pcb_autoplace.go.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// pcbLPad is a placed pad with its net and center (mil).
type pcbLPad struct {
	Designator string
	Net        string
	X, Y       float64
}

// pcbLComp is a placed footprint's identity + rendered extent.
type pcbLComp struct {
	Designator string
	BBox       *layoutBBox
}

// ratLink is one ratsnest (unrouted) link between two same-net pads.
type ratLink struct {
	Net    string
	Ax, Ay float64
	Bx, By float64
	Len    float64
}

// pcbLFinding is one mechanical placement issue.
type pcbLFinding struct {
	Type string  `json:"type"` // "overlap" | "outside-outline" | "spacing"
	A    string  `json:"a"`
	B    string  `json:"b,omitempty"`
	OvX  float64 `json:"overlapX,omitempty"`
	OvY  float64 `json:"overlapY,omitempty"`
	Gap  float64 `json:"gap,omitempty"`
}

// crossFinding is a cross-net ratline crossing (a routability hotspot).
type crossFinding struct {
	NetA string  `json:"netA"`
	NetB string  `json:"netB"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

// pcbLAccessFinding is a component boxed in on ALL four sides below the
// hand-solder iron-access corridor (issue #99): there is no direction to bring
// an iron tip / solder / rework tools in from.
type pcbLAccessFinding struct {
	Designator string             `json:"designator"`
	BestGap    float64            `json:"bestGapMil"` // widest of the four side gaps
	Sides      map[string]float64 `json:"sides"`      // left/right/top/bottom → gap to nearest blocker (mil)
}

// pcbLayoutReport is the full normalized result.
type pcbLayoutReport struct {
	OK             bool          `json:"ok"`
	Score          int           `json:"score"`   // 0-100 routability
	Verdict        string        `json:"verdict"` // easy | moderate | hard | very-hard | overlap
	ComponentCount int           `json:"componentCount"`
	MinGapMil      float64       `json:"minGapMil"`
	Overlaps       []pcbLFinding `json:"overlaps"`
	OutsideOutline []pcbLFinding `json:"outsideOutline"`
	TightPairs     []pcbLFinding `json:"tightSpacing"`
	// AccessMil / AccessBlocked: hand-solder iron-access check (issue #99),
	// populated only when the gate runs with a hand-solder assembly profile.
	AccessMil     float64             `json:"accessMil,omitempty"`
	AccessBlocked []pcbLAccessFinding `json:"accessBlocked,omitempty"`

	SignalNets     int            `json:"signalNets"`
	RatsnestLenMil float64        `json:"ratsnestLenMil"`
	CrossingCount  int            `json:"crossingCount"`
	Crossings      []crossFinding `json:"crossings,omitempty"`
	Summary        string         `json:"summary"`
}

// analyzeSolderAccess flags components with NO accessible side: for each of the
// four bbox directions the corridor (the component's own side span, extended
// outward) must stay clear of other components for at least accessMil before
// it counts as an iron-entry direction. One clear side is enough — a decap may
// sit tight against its IC as long as its other flank stays workable, which is
// exactly the issue-#99 rule ("去耦可贴近,但至少保留一侧可操作"). The board
// edge never blocks (open air is reachable). Pad-size-aware "large pad"
// classification needs pad width/height the connector does not expose yet, so
// v1 applies the corridor rule to every component uniformly.
func analyzeSolderAccess(comps []pcbLComp, accessMil float64) []pcbLAccessFinding {
	withBBox := make([]pcbLComp, 0, len(comps))
	for _, c := range comps {
		if c.BBox != nil {
			withBBox = append(withBBox, c)
		}
	}
	// A gap this large means "no blocker in that direction" (open to the edge).
	const openGap = 1e9
	var out []pcbLAccessFinding
	for i, c := range withBBox {
		a := *c.BBox
		sides := map[string]float64{"left": openGap, "right": openGap, "top": openGap, "bottom": openGap}
		for j, o := range withBBox {
			if i == j {
				continue
			}
			b := *o.BBox
			overlapY := b.MinY < a.MaxY && b.MaxY > a.MinY
			overlapX := b.MinX < a.MaxX && b.MaxX > a.MinX
			if overlapY {
				if b.MinX >= a.MaxX { // blocker to the right
					sides["right"] = math.Min(sides["right"], b.MinX-a.MaxX)
				} else if b.MaxX <= a.MinX { // blocker to the left
					sides["left"] = math.Min(sides["left"], a.MinX-b.MaxX)
				}
			}
			if overlapX {
				if b.MinY >= a.MaxY { // blocker above (y-up)
					sides["top"] = math.Min(sides["top"], b.MinY-a.MaxY)
				} else if b.MaxY <= a.MinY { // blocker below
					sides["bottom"] = math.Min(sides["bottom"], a.MinY-b.MaxY)
				}
			}
			// Overlapping bboxes are reported by the overlap check; for access
			// purposes an overlapped side is simply gap 0 on both axes it blocks.
			if overlapX && overlapY {
				for k := range sides {
					sides[k] = 0
				}
				break
			}
		}
		best := 0.0
		for _, g := range sides {
			best = math.Max(best, g)
		}
		if best < accessMil {
			rounded := map[string]float64{}
			for k, g := range sides {
				rounded[k] = round2(g)
			}
			out = append(out, pcbLAccessFinding{Designator: c.Designator, BestGap: round2(best), Sides: rounded})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Designator < out[j].Designator })
	return out
}

// analyzePcbLayout is the pure core. minGapMil flags too-tight pairs; outline (may
// be nil) drives the outside-outline check.
func analyzePcbLayout(comps []pcbLComp, pads []pcbLPad, outline *layoutBBox, minGapMil float64) pcbLayoutReport {
	rep := pcbLayoutReport{MinGapMil: minGapMil, ComponentCount: len(comps)}

	withBBox := make([]pcbLComp, 0, len(comps))
	for _, c := range comps {
		if c.BBox != nil {
			withBBox = append(withBBox, c)
		}
	}

	// 1. Overlap + tight spacing (pairwise).
	for i := 0; i < len(withBBox); i++ {
		for j := i + 1; j < len(withBBox); j++ {
			a, b := withBBox[i], withBBox[j]
			la, lb := a.Designator, b.Designator
			if lb < la {
				la, lb = lb, la
			}
			if ox, oy, ov := overlapExtent(*a.BBox, *b.BBox); ov {
				rep.Overlaps = append(rep.Overlaps, pcbLFinding{Type: "overlap", A: la, B: lb, OvX: round2(ox), OvY: round2(oy)})
				continue
			}
			if gap := rectGap(*a.BBox, *b.BBox); gap < minGapMil {
				rep.TightPairs = append(rep.TightPairs, pcbLFinding{Type: "spacing", A: la, B: lb, Gap: round2(gap)})
			}
		}
	}

	// 2. Outside board outline. A part is off-board only when one of its PADS lands
	//    outside the outline — a connector whose body/courtyard protrudes past the
	//    edge (Type-C, card slot, screw terminal) with every pad inside is an
	//    INTENTIONAL edge-mount (the mating face overhangs on purpose), not a
	//    misplacement. Fall back to the bbox for parts with no pads (mechanical /
	//    graphic). Pad centers are used (pad-edge-to-outline clearance is DRC's job).
	if outline != nil {
		padsByRef := map[string][]pcbLPad{}
		for _, p := range pads {
			padsByRef[p.Designator] = append(padsByRef[p.Designator], p)
		}
		for _, c := range withBBox {
			var outside bool
			if cps := padsByRef[c.Designator]; len(cps) > 0 {
				for _, p := range cps {
					if p.X < outline.MinX || p.X > outline.MaxX ||
						p.Y < outline.MinY || p.Y > outline.MaxY {
						outside = true
						break
					}
				}
			} else {
				outside = c.BBox.MinX < outline.MinX || c.BBox.MinY < outline.MinY ||
					c.BBox.MaxX > outline.MaxX || c.BBox.MaxY > outline.MaxY
			}
			if outside {
				rep.OutsideOutline = append(rep.OutsideOutline, pcbLFinding{Type: "outside-outline", A: c.Designator})
			}
		}
	}

	// 3. Ratsnest over SIGNAL nets (power/GND poured → excluded so they don't swamp
	//    the metric). Per net: MST length; then count cross-net segment crossings.
	byNet := map[string][]pcbLPad{}
	for _, p := range pads {
		if p.Net == "" || isGlobalNet(p.Net) {
			continue
		}
		byNet[p.Net] = append(byNet[p.Net], p)
	}
	nets := make([]string, 0, len(byNet))
	for n := range byNet {
		nets = append(nets, n)
	}
	sort.Strings(nets)

	var edges []ratLink
	for _, n := range nets {
		np := dedupPadPoints(byNet[n])
		if len(np) < 2 {
			continue
		}
		rep.SignalNets++
		for _, e := range netMST(n, np) {
			rep.RatsnestLenMil += e.Len
			edges = append(edges, e)
		}
	}
	rep.RatsnestLenMil = round2(rep.RatsnestLenMil)

	// Cross-net crossings (same-net crossings are fine — one net can touch itself).
	for i := 0; i < len(edges); i++ {
		for j := i + 1; j < len(edges); j++ {
			if edges[i].Net == edges[j].Net {
				continue
			}
			if x, y, ok := segCross(edges[i], edges[j]); ok {
				na, nb := edges[i].Net, edges[j].Net
				if nb < na {
					na, nb = nb, na
				}
				rep.Crossings = append(rep.Crossings, crossFinding{NetA: na, NetB: nb, X: round2(x), Y: round2(y)})
			}
		}
	}
	sort.Slice(rep.Crossings, func(i, j int) bool {
		if rep.Crossings[i].NetA != rep.Crossings[j].NetA {
			return rep.Crossings[i].NetA < rep.Crossings[j].NetA
		}
		return rep.Crossings[i].NetB < rep.Crossings[j].NetB
	})
	rep.CrossingCount = len(rep.Crossings)

	// 4. Score + verdict. Overlaps are fatal; crossings/outside dominate routability.
	rep.OK = len(rep.Overlaps) == 0 && len(rep.OutsideOutline) == 0
	score := 100
	score -= 100 * len(rep.Overlaps)      // any overlap ⇒ 0
	score -= 20 * len(rep.OutsideOutline) // off-board is nearly as bad
	score -= 4 * rep.CrossingCount        // each cross-net crossing = a via/detour
	score -= 1 * len(rep.TightPairs)      // minor
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	rep.Score = score
	switch {
	case len(rep.Overlaps) > 0:
		rep.Verdict = "overlap"
	case score >= 85:
		rep.Verdict = "easy"
	case score >= 60:
		rep.Verdict = "moderate"
	case score >= 30:
		rep.Verdict = "hard"
	default:
		rep.Verdict = "very-hard"
	}

	rep.Summary = fmt.Sprintf("score %d/100 (%s): %d comps, %d overlap, %d off-board, %d tight; %d signal nets, ratsnest %.0fmil, %d crossings",
		rep.Score, rep.Verdict, rep.ComponentCount, len(rep.Overlaps), len(rep.OutsideOutline),
		len(rep.TightPairs), rep.SignalNets, rep.RatsnestLenMil, rep.CrossingCount)
	return rep
}

// dedupPadPoints collapses pads sharing a coordinate (a multi-pad net can have
// stacked pads) so the MST doesn't emit zero-length edges.
func dedupPadPoints(pads []pcbLPad) []pcbLPad {
	seen := map[[2]int64]bool{}
	out := make([]pcbLPad, 0, len(pads))
	for _, p := range pads {
		k := [2]int64{int64(math.Round(p.X * 100)), int64(math.Round(p.Y * 100))}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return out
}

// netMST builds a minimum spanning tree (Prim, complete Euclidean graph) over a
// net's pads — the shortest set of links that connect every pad, i.e. the ratsnest.
func netMST(net string, pads []pcbLPad) []ratLink {
	n := len(pads)
	if n < 2 {
		return nil
	}
	inTree := make([]bool, n)
	dist := make([]float64, n)
	from := make([]int, n)
	for i := range dist {
		dist[i] = math.Inf(1)
		from[i] = -1
	}
	dist[0] = 0
	var edges []ratLink
	for k := 0; k < n; k++ {
		u := -1
		best := math.Inf(1)
		for v := 0; v < n; v++ {
			if !inTree[v] && dist[v] < best {
				best, u = dist[v], v
			}
		}
		if u == -1 {
			break
		}
		inTree[u] = true
		if from[u] >= 0 {
			a, b := pads[from[u]], pads[u]
			edges = append(edges, ratLink{Net: net, Ax: a.X, Ay: a.Y, Bx: b.X, By: b.Y, Len: math.Hypot(a.X-b.X, a.Y-b.Y)})
		}
		for v := 0; v < n; v++ {
			if inTree[v] {
				continue
			}
			if d := math.Hypot(pads[u].X-pads[v].X, pads[u].Y-pads[v].Y); d < dist[v] {
				dist[v], from[v] = d, u
			}
		}
	}
	return edges
}

// segCross reports whether two ratsnest segments properly cross (interior
// intersection), and where. Shared endpoints do NOT count as a crossing.
func segCross(e, f ratLink) (x, y float64, ok bool) {
	p1x, p1y, p2x, p2y := e.Ax, e.Ay, e.Bx, e.By
	p3x, p3y, p4x, p4y := f.Ax, f.Ay, f.Bx, f.By
	d := (p2x-p1x)*(p4y-p3y) - (p2y-p1y)*(p4x-p3x)
	if math.Abs(d) < 1e-9 {
		return 0, 0, false // parallel / collinear
	}
	t := ((p3x-p1x)*(p4y-p3y) - (p3y-p1y)*(p4x-p3x)) / d
	u := ((p3x-p1x)*(p2y-p1y) - (p3y-p1y)*(p2x-p1x)) / d
	const eps = 1e-6
	if t <= eps || t >= 1-eps || u <= eps || u >= 1-eps {
		return 0, 0, false // touch at/near an endpoint, or outside → not a proper crossing
	}
	return p1x + t*(p2x-p1x), p1y + t*(p2y-p1y), true
}

// runPcbLayoutLint fetches the live placement (bbox + pads), the board outline, and
// the DRC clearance, analyzes, renders, and returns a non-nil error when the layout
// is not OK (overlap / off-board) so the command exits non-zero (gate-able).
// pcbLayoutGateOpts configures the routability gate that layout-lint applies on
// top of the overlap/off-board checks (issue #97): a minimum score and a maximum
// cross-net ratline crossing count. When gate is enabled and the layout passes,
// the project's pre_route_passed stage is confirmed and a gate summary is
// persisted for the route commands to consult.
type pcbLayoutGateOpts struct {
	gate         bool
	project      string
	minScore     int
	maxCrossings int
}

func runPcbLayoutLint(cfg *appConfig, window string, minGapMil float64, asJSON bool, gate pcbLayoutGateOpts, stdout, stderr io.Writer) error {
	var assembly *pcbAssemblyProfile
	if gate.gate {
		// Key the persisted gate state by the real project identity (matches the
		// daemon-side gate and the confirm commands) — not the raw --project flag,
		// which may be empty when routing by --window.
		if resolved, rerr := resolveStageProject(cfg, window); rerr == nil {
			gate.project = resolved
		}
		st, serr := loadPcbStageState(gate.project)
		if serr != nil {
			return fmt.Errorf("load assembly profile: %w", serr)
		}
		if st.Assembly == nil {
			return fmt.Errorf("assembly profile is required for --gate; run `pcb stage set-assembly --profile hand-solder|reflow`")
		}
		assembly = st.Assembly
		if assembly.MinGapMil > minGapMil {
			minGapMil = assembly.MinGapMil
		}
	}
	res, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includeBBox": true, "includePads": true})
	if err != nil {
		return fmt.Errorf("fetch PCB components: %w", err)
	}
	rawComps, _ := mnav(res.Result, "components").([]any)

	var comps []pcbLComp
	var pads []pcbLPad
	for _, rc := range rawComps {
		cm, ok := rc.(map[string]any)
		if !ok {
			continue
		}
		desig, _ := cm["designator"].(string)
		lc := pcbLComp{Designator: desig}
		if bb, ok := cm["bbox"].(map[string]any); ok {
			minX, _ := asFloatOK(bb["minX"])
			minY, _ := asFloatOK(bb["minY"])
			maxX, _ := asFloatOK(bb["maxX"])
			maxY, _ := asFloatOK(bb["maxY"])
			lc.BBox = &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
		}
		comps = append(comps, lc)
		if rawPads, ok := cm["pads"].([]any); ok {
			for _, rp := range rawPads {
				pm, ok := rp.(map[string]any)
				if !ok {
					continue
				}
				net, _ := pm["net"].(string)
				x, _ := asFloatOK(pm["x"])
				y, _ := asFloatOK(pm["y"])
				pads = append(pads, pcbLPad{Designator: desig, Net: net, X: x, Y: y})
			}
		}
	}

	// Board outline bbox (best-effort; nil → skip the off-board check).
	var outline *layoutBBox
	if ores, oerr := requestAction(cfg, "pcb.outline.get", window, nil); oerr == nil && ores != nil {
		if bb, ok := mnav(ores.Result, "bbox").(map[string]any); ok {
			minX, ok1 := asFloatOK(bb["minX"])
			minY, ok2 := asFloatOK(bb["minY"])
			maxX, ok3 := asFloatOK(bb["maxX"])
			maxY, ok4 := asFloatOK(bb["maxY"])
			if ok1 && ok2 && ok3 && ok4 {
				outline = &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
			}
		}
	}

	// Default min-gap = the board's track-to-pad clearance (live rule) if not set.
	if minGapMil <= 0 {
		minGapMil = fetchPcbRules(cfg, window).clearanceMil
	}

	rep := analyzePcbLayout(comps, pads, outline, minGapMil)

	// Hand-solder iron-access check (issue #99): with a hand-solder profile the
	// gate also requires every component to keep at least one clear entry side.
	if gate.gate && assembly != nil && assembly.Profile == "hand-solder" && assembly.LargePadAccessMil > 0 {
		rep.AccessMil = assembly.LargePadAccessMil
		rep.AccessBlocked = analyzeSolderAccess(comps, assembly.LargePadAccessMil)
	}

	// Routability gate (issue #97): the base report already flags overlap /
	// off-board; the gate adds score + crossings thresholds and — on a pass —
	// confirms the project's pre_route_passed stage so route commands unlock.
	var gateVerdict *routeGateVerdict
	if gate.gate {
		gv := evalLayoutGate(rep, gate)
		gateVerdict = &gv
		if gv.Pass {
			if perr := recordLayoutGatePass(gate.project, rep, assembly); perr != nil {
				fmt.Fprintf(stderr, "⚠️  gate passed but could not persist pre_route_passed: %v\n", perr)
			}
		}
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		payload := map[string]any{"report": rep}
		if gateVerdict != nil {
			payload["gate"] = gateVerdict
		}
		if err := enc.Encode(payload); err != nil {
			return err
		}
	} else {
		renderPcbLayoutReport(rep, stdout)
		if gateVerdict != nil {
			renderLayoutGate(*gateVerdict, stdout)
		}
	}
	if !rep.OK {
		return fmt.Errorf("layout not routable-ready: %d overlap, %d off-board", len(rep.Overlaps), len(rep.OutsideOutline))
	}
	if gateVerdict != nil && !gateVerdict.Pass {
		return fmt.Errorf("routability gate FAILED: %s", strings.Join(gateVerdict.Reasons, "; "))
	}
	return nil
}

// routeGateVerdict is the machine-readable result of the layout-lint routability
// gate (emitted in --json, stored on pass).
type routeGateVerdict struct {
	Pass          bool     `json:"pass"`
	Score         int      `json:"score"`
	MinScore      int      `json:"minScore"`
	CrossingCount int      `json:"crossingCount"`
	MaxCrossings  int      `json:"maxCrossings"`
	Reasons       []string `json:"reasons,omitempty"`
}

// evalLayoutGate applies the score / crossings / overlap / off-board thresholds.
func evalLayoutGate(rep pcbLayoutReport, opt pcbLayoutGateOpts) routeGateVerdict {
	v := routeGateVerdict{
		Score: rep.Score, MinScore: opt.minScore,
		CrossingCount: rep.CrossingCount, MaxCrossings: opt.maxCrossings,
	}
	if len(rep.Overlaps) > 0 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d overlap", len(rep.Overlaps)))
	}
	if len(rep.OutsideOutline) > 0 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d off-board", len(rep.OutsideOutline)))
	}
	if len(rep.TightPairs) > 0 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d tight pair(s) below %.1fmil assembly gap", len(rep.TightPairs), rep.MinGapMil))
	}
	if len(rep.AccessBlocked) > 0 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d component(s) boxed in below the %.1fmil iron-access corridor", len(rep.AccessBlocked), rep.AccessMil))
	}
	if rep.Score < opt.minScore {
		v.Reasons = append(v.Reasons, fmt.Sprintf("score %d < min %d", rep.Score, opt.minScore))
	}
	if opt.maxCrossings >= 0 && rep.CrossingCount > opt.maxCrossings {
		v.Reasons = append(v.Reasons, fmt.Sprintf("crossings %d > max %d", rep.CrossingCount, opt.maxCrossings))
	}
	v.Pass = len(v.Reasons) == 0
	return v
}

// recordLayoutGatePass persists pre_route_passed + the gate snapshot.
func recordLayoutGatePass(project string, rep pcbLayoutReport, assembly *pcbAssemblyProfile) error {
	st, err := loadPcbStageState(project)
	if err != nil {
		return err
	}
	profile := ""
	if assembly != nil {
		profile = assembly.Profile
	}
	st.Layout = &pcbLayoutGateSummary{
		Score: rep.Score, Verdict: rep.Verdict,
		Overlaps: len(rep.Overlaps), OffBoard: len(rep.OutsideOutline),
		CrossingCount: rep.CrossingCount, MinGapMil: rep.MinGapMil,
		TightPairs: len(rep.TightPairs),
		AccessMil:  rep.AccessMil, AccessBlocked: len(rep.AccessBlocked),
		Assembly: profile, At: time.Now().Format(time.RFC3339),
	}
	st.Confirm(stagePreRoutePassed, "gate-pass",
		fmt.Sprintf("layout-lint score=%d crossings=%d", rep.Score, rep.CrossingCount))
	return savePcbStageState(st)
}

// renderLayoutGate prints the human-readable gate verdict.
func renderLayoutGate(v routeGateVerdict, w io.Writer) {
	if v.Pass {
		fmt.Fprintf(w, "\nroutability gate: ✅ PASS (score %d ≥ %d, crossings %d ≤ %d) → pre_route_passed confirmed\n",
			v.Score, v.MinScore, v.CrossingCount, v.MaxCrossings)
		return
	}
	fmt.Fprintf(w, "\nroutability gate: ❌ FAIL — %s\n", strings.Join(v.Reasons, "; "))
}

func renderPcbLayoutReport(rep pcbLayoutReport, w io.Writer) {
	fmt.Fprintf(w, "PCB layout-lint: %s\n", rep.Summary)
	for _, o := range rep.Overlaps {
		fmt.Fprintf(w, "  ERROR overlap    %s ↔ %s  (%.1f×%.1f mil)\n", o.A, o.B, o.OvX, o.OvY)
	}
	for _, o := range rep.OutsideOutline {
		fmt.Fprintf(w, "  ERROR off-board  %s extends outside the board outline\n", o.A)
	}
	for _, c := range rep.Crossings {
		fmt.Fprintf(w, "  WARN  crossing   %s × %s @ (%.0f, %.0f)\n", c.NetA, c.NetB, c.X, c.Y)
	}
	for _, t := range rep.TightPairs {
		fmt.Fprintf(w, "  WARN  tight      %s ↔ %s  gap %.1f mil (< %.1f)\n", t.A, t.B, t.Gap, rep.MinGapMil)
	}
	for _, a := range rep.AccessBlocked {
		fmt.Fprintf(w, "  WARN  no-access  %s boxed in on all sides (best %.1f mil < %.1f iron corridor: L%.0f R%.0f T%.0f B%.0f)\n",
			a.Designator, a.BestGap, rep.AccessMil,
			a.Sides["left"], a.Sides["right"], a.Sides["top"], a.Sides["bottom"])
	}
}
