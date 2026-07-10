package app

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// pcb_autoplace.go — module-aware PCB auto-placement (heuristic, daemon-side).
//
// The premise (proven by the community "PCB自动化工具" extension, see
// docs/ecosystem-survey.md §8.5): real PCB auto-layout does NOT need the @alpha
// autoLayout() API — it is a heuristic over the standard primitive reads/writes.
// We run that heuristic HERE in the daemon (Go), fed by
// `pcb.components.list --include-pads --include-bbox`, and emit moves as
// `pcb.component.modify` patches. Living in the daemon means `make dev` (air)
// hot-reloads algorithm tweaks with no connector re-import.
//
// v1 = "hug the chip": main chips (>= mainPins distinct pins) are anchors that
// stay put; every small satellite (caps, resistors, LEDs) is pulled to the chip
// edge nearest the pad it actually connects to, then packed along that edge so
// nothing overlaps. Decoupling caps land by their power pin; signal R's by their
// signal pin; an LED chains next to its series resistor.

// apPad is one component pad: its net (by name), rendered center, and copper layer.
type apPad struct {
	num   string
	net   string
	x     float64
	y     float64
	layer int
}

// apComp is a placed component with the geometry the planner reasons over.
type apComp struct {
	id         string
	designator string
	x, y       float64 // component anchor (what modify sets)
	rotation   float64 // current rotation (deg, y-up CCW) — needed to re-orient
	locked     bool
	hasBBox    bool
	minX, minY float64
	maxX, maxY float64
	pads       []apPad
}

func (c apComp) bboxCenter() (float64, float64) {
	return (c.minX + c.maxX) / 2, (c.minY + c.maxY) / 2
}
func (c apComp) width() float64  { return c.maxX - c.minX }
func (c apComp) height() float64 { return c.maxY - c.minY }

// distinctPins counts unique pad numbers (U1 reports the same GND pad many
// times; a 2-pad cap reports 2). This is the "is it a chip?" signal.
func (c apComp) distinctPins() int {
	seen := map[string]struct{}{}
	for _, p := range c.pads {
		seen[p.num] = struct{}{}
	}
	return len(seen)
}

// localNets returns the component's non-global nets (the signal nets that tie it
// to a specific chip pin), deduped.
func (c apComp) localNets() []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, p := range c.pads {
		n := strings.TrimSpace(p.net)
		if n == "" || isGlobalNet(n) {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// powerNets returns the component's global (power/ground) nets, deduped, with
// non-ground rails first (a decoupling cap wants to sit by its 3V3/VCC pin, not
// the ground pin).
func (c apComp) powerNets() []string {
	var nonGnd, gnd []string
	seen := map[string]struct{}{}
	for _, p := range c.pads {
		n := strings.TrimSpace(p.net)
		if n == "" || !isGlobalNet(n) {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		if strings.Contains(strings.ToLower(n), "gnd") {
			gnd = append(gnd, n)
		} else {
			nonGnd = append(nonGnd, n)
		}
	}
	return append(nonGnd, gnd...)
}

// Global-net classifier — ported verbatim from the connector's isGlobalNetName
// (extension/src/actions.ts) so daemon and connector agree on what "ties
// everything together" (GND / power rails / +N / Nv).
var (
	reGlobalNet1 = regexp.MustCompile(`(?i)^(?:[adp])?gnd$|^v(?:cc|dd|ss|in|out|bus|bat|sys|ref)\b|^[+-]?\d+v\d*$|^[+-]`)
	reGlobalNet2 = regexp.MustCompile(`(?i)gnd|vcc|vdd|vss`)
)

func isGlobalNet(net string) bool {
	n := strings.TrimSpace(net)
	if n == "" {
		return false
	}
	return reGlobalNet1.MatchString(n) || reGlobalNet2.MatchString(n)
}

// connectorDesRe matches board-edge connector designators (J1, CN2, CON3, USB1,
// SIM1, BAT1…) so the planner can leave them for `place-constrained` instead of
// dragging them toward a chip by their global GND pad. Prefixes are the EDA-wide
// convention for connectors/plugs; USB/SIM/BAT cover the common edge parts.
var connectorDesRe = regexp.MustCompile(`(?i)^(?:J|CN|CON|USB|SIM|BAT)\d`)

// edgeConnectorMinDim is the min bbox extent (mil) for the "relatively large
// footprint" half of the connector heuristic — real board-edge connectors dwarf a
// decoupling cap/resistor (which top out ~180mil), so 200mil safely separates them.
const edgeConnectorMinDim = 200.0

// isEdgeConnector flags a low-pin, connector-designated, relatively large part
// (e.g. a 3P J_VEH power terminal) that belongs on the board edge — auto-place
// must NOT pull it toward a chip by its global GND pad; `place-constrained` owns
// it. mainPins-or-more-pin parts are chips (handled elsewhere) and never match.
func isEdgeConnector(c apComp, mainPins int) bool {
	if !connectorDesRe.MatchString(strings.TrimSpace(c.designator)) {
		return false
	}
	if c.distinctPins() >= mainPins {
		return false
	}
	return math.Max(c.width(), c.height()) >= edgeConnectorMinDim
}

// mainsAre2D reports whether the anchored main chips already form an intentional
// 2D floorplan (≥2 columns with vertical stacking) rather than a single row. It
// looks for a pair whose X-projections overlap (spaceMains would judge them "too
// close" and push one right) yet whose Y-projections do NOT overlap (they are
// stacked, not colliding). Flattening such a layout into a row is exactly the bug
// in #91, so when detected the caller preserves the anchors.
func mainsAre2D(comps []apComp, mains []int) bool {
	for i := 0; i < len(mains); i++ {
		a := comps[mains[i]]
		for j := i + 1; j < len(mains); j++ {
			b := comps[mains[j]]
			xOverlap := a.minX < b.maxX && b.minX < a.maxX
			yOverlap := a.minY < b.maxY && b.minY < a.maxY
			if xOverlap && !yOverlap {
				return true
			}
		}
	}
	return false
}

type apEdge int

const (
	edgeLeft apEdge = iota
	edgeRight
	edgeTop
	edgeBottom
)

func (e apEdge) String() string {
	switch e {
	case edgeLeft:
		return "left"
	case edgeRight:
		return "right"
	case edgeTop:
		return "top"
	default:
		return "bottom"
	}
}

func (e apEdge) vertical() bool { return e == edgeLeft || e == edgeRight }

// apMove is one planned placement: where a satellite should go and why.
type apMove struct {
	ID         string  `json:"id"`
	Designator string  `json:"designator"`
	NewX       float64 `json:"newX"`
	NewY       float64 `json:"newY"`
	NewRot     float64 `json:"newRot"`           // target rotation (deg); == current when SetRot is false
	SetRot     bool    `json:"setRot"`           // whether to apply NewRot (only re-oriented 2-pin satellites)
	Main       string  `json:"main"`
	Edge       string  `json:"edge"`
	TargetNet  string  `json:"targetNet"`
	Via        string  `json:"via,omitempty"` // "local" | "power" | "chain:<designator>"
}

// apDiag records a satellite the planner could not place (and why), so the CLI
// can report honestly instead of silently leaving parts scattered.
type apDiag struct {
	Designator string `json:"designator"`
	Reason     string `json:"reason"`
}

type apOptions struct {
	mainPins int     // distinct-pin threshold to count as a "main chip" anchor
	gap      float64 // clearance from chip bbox edge to the nearest satellite edge
	pitch    float64 // spacing between two satellites packed on the same edge
	rotate   bool    // re-orient 2-pin satellites so their connecting pad faces the chip
	multiGap float64 // min bbox gap between adjacent main chips (0 = don't space them)
}

func defaultApOptions() apOptions {
	return apOptions{mainPins: 8, gap: 40, pitch: 30, rotate: true, multiGap: 150}
}

// assignment is the planner's per-satellite decision before packing: which chip,
// which edge, and where along that edge it wants to sit (the connecting pad's
// coordinate along the edge axis).
type assignment struct {
	sat       int // index into satellites
	mainIdx   int // index into mains
	edge      apEdge
	along     float64 // target coordinate along the edge axis (y for L/R, x for T/B)
	targetNet string
	via       string
}

// planAutoPlace is the pure planner: given the placed components, decide where
// every movable satellite goes. Main chips are anchors (never moved in v1).
// Returns the moves plus diagnostics for anything left unplaced.
func planAutoPlace(comps []apComp, opt apOptions) ([]apMove, []apDiag) {
	var mains, sats []int
	var diags []apDiag
	for i, c := range comps {
		if !c.hasBBox {
			continue
		}
		if c.distinctPins() >= opt.mainPins {
			mains = append(mains, i)
		} else if c.locked {
			continue
		} else if isEdgeConnector(c, opt.mainPins) {
			// Board-edge connector (J*/CN*/…): leave it where the user (or
			// place-constrained) put it — don't drag it toward a chip by its GND pad.
			diags = append(diags, apDiag{c.designator, "board-edge connector — skipped, use `place-constrained` to seat it on the edge"})
		} else {
			sats = append(sats, i)
		}
	}

	if len(mains) == 0 {
		for _, s := range sats {
			diags = append(diags, apDiag{comps[s].designator, "no main chip on board (>= mainPins distinct pins) to anchor against"})
		}
		return nil, diags
	}

	var moves []apMove

	// Multi-chip spacing: with ≥2 main chips, spread any that overlap / sit closer
	// than multiGap into a left-to-right row (leftmost stays put). Satellites are
	// then placed against the chips' NEW positions, so we run the rest of the
	// planner on a working copy whose mains are shifted, and emit a move per shifted
	// main. Single-chip boards (and multiGap=0) skip this entirely → unchanged v1.1.
	if opt.multiGap > 0 && len(mains) > 1 && !mainsAre2D(comps, mains) {
		shifts := spaceMains(comps, mains, opt.multiGap)
		work := make([]apComp, len(comps))
		copy(work, comps)
		for mi, m := range mains {
			dx := shifts[mi]
			if dx == 0 {
				continue
			}
			work[m] = shiftComp(comps[m], dx)
			moves = append(moves, apMove{
				ID:         comps[m].id,
				Designator: comps[m].designator,
				NewX:       round2(work[m].x),
				NewY:       round2(work[m].y),
				Via:        "chip-spacing",
			})
		}
		comps = work
	}

	// Per-main net→pads index: ALL pads on each net (a chip repeats GND/VCC many
	// times), so a satellite can hug the NEAREST same-net pad, not a fixed first one.
	mainPadsByNet := make([]map[string][]apPad, len(mains))
	for mi, m := range mains {
		mainPadsByNet[mi] = map[string][]apPad{}
		for _, p := range comps[m].pads {
			n := strings.TrimSpace(p.net)
			if n == "" {
				continue
			}
			mainPadsByNet[mi][n] = append(mainPadsByNet[mi][n], p)
		}
	}

	assigned := make(map[int]*assignment) // sat index → decision

	// Pass 1 — direct: a satellite sharing a LOCAL (signal) net with a main pad
	// hugs that pad. Prefer the nearest (main, same-net pad) when several match.
	for _, s := range sats {
		scx, scy := comps[s].bboxCenter()
		best := -1
		var bestPad apPad
		var bestNet string
		bestDist := math.MaxFloat64
		for _, ln := range comps[s].localNets() {
			for mi := range mains {
				if pad, ok := nearestPad(mainPadsByNet[mi][ln], scx, scy); ok {
					d := math.Hypot(pad.x-scx, pad.y-scy)
					if d < bestDist {
						bestDist, best, bestPad, bestNet = d, mi, pad, ln
					}
				}
			}
		}
		if best >= 0 {
			e, along := edgeFor(comps[mains[best]], bestPad)
			assigned[s] = &assignment{s, best, e, along, bestNet, "local"}
		}
	}

	// Pass 2 — chain: a satellite with a local net but no chip pad (e.g. an LED
	// whose LED_A net only reaches its series resistor) inherits a placed
	// neighbour's edge + target, so the packer drops it right alongside. Runs to
	// a fixpoint so multi-hop chains (A→B→C) resolve.
	for changed := true; changed; {
		changed = false
		for _, s := range sats {
			if assigned[s] != nil {
				continue
			}
			for _, ln := range comps[s].localNets() {
				parent := -1
				for _, o := range sats {
					if o == s || assigned[o] == nil {
						continue
					}
					if compHasLocalNet(comps[o], ln) {
						parent = o
						break
					}
				}
				if parent >= 0 {
					a := assigned[parent]
					assigned[s] = &assignment{s, a.mainIdx, a.edge, a.along, ln, "chain:" + comps[parent].designator}
					changed = true
					break
				}
			}
		}
	}

	// Pass 3 — power-only fallback: whatever is still unplaced (a decoupling cap
	// whose pads are all 3V3/GND, or a part that couldn't chain) hugs the nearest
	// main's matching power pad, non-ground rail preferred.
	for _, s := range sats {
		if assigned[s] != nil {
			continue
		}
		pnets := comps[s].powerNets()
		if len(pnets) == 0 {
			continue
		}
		scx, scy := comps[s].bboxCenter()
		best := -1
		var bestPad apPad
		var bestNet string
		bestDist := math.MaxFloat64
		for _, pn := range pnets {
			for mi := range mains {
				if pad, ok := nearestPad(mainPadsByNet[mi][pn], scx, scy); ok {
					d := math.Hypot(pad.x-scx, pad.y-scy)
					if d < bestDist {
						bestDist, best, bestPad, bestNet = d, mi, pad, pn
					}
				}
			}
			if best >= 0 {
				break // honor power-net preference order (non-GND first)
			}
		}
		if best >= 0 {
			e, along := edgeFor(comps[mains[best]], bestPad)
			assigned[s] = &assignment{s, best, e, along, bestNet, "power"}
		}
	}

	// Diagnostics for the genuinely unplaceable (no net path to any chip).
	for _, s := range sats {
		if assigned[s] == nil {
			diags = append(diags, apDiag{comps[s].designator, "no shared net path to a main chip"})
		}
	}

	// Pack per (main, edge): sort by target-along, then lay out in a line just
	// outside the edge, pushing later items along so none overlap.
	type key struct {
		main int
		edge apEdge
	}
	groups := map[key][]*assignment{}
	for _, s := range sats {
		if a := assigned[s]; a != nil {
			k := key{a.mainIdx, a.edge}
			groups[k] = append(groups[k], a)
		}
	}

	// Deterministic group order.
	var keys []key
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].main != keys[j].main {
			return keys[i].main < keys[j].main
		}
		return keys[i].edge < keys[j].edge
	})

	for _, k := range keys {
		group := groups[k]
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].along != group[j].along {
				return group[i].along < group[j].along
			}
			return comps[group[i].sat].designator < comps[group[j].sat].designator
		})
		chip := comps[mains[k.main]]
		cursor := math.Inf(-1)
		for _, a := range group {
			sat := comps[a.sat]
			// Re-orient (2-pin parts) so the connecting pad faces the chip; pack with
			// the EFFECTIVE (post-rotation) bbox so rotated parts still don't overlap.
			targetRot, effW, effH, oriented := orientSatellite(sat, k.edge, a.targetNet, opt.rotate)
			var alongExtent, perpCenter float64
			if k.edge.vertical() {
				alongExtent = effH
				half := effW / 2
				if k.edge == edgeLeft {
					perpCenter = chip.minX - opt.gap - half
				} else {
					perpCenter = chip.maxX + opt.gap + half
				}
			} else {
				alongExtent = effW
				half := effH / 2
				if k.edge == edgeTop {
					perpCenter = chip.maxY + opt.gap + half
				} else {
					perpCenter = chip.minY - opt.gap - half
				}
			}
			half := alongExtent / 2
			center := a.along
			if min := cursor + opt.pitch + half; center < min {
				center = min
			}
			cursor = center + half

			var cx, cy float64
			if k.edge.vertical() {
				cx, cy = perpCenter, center
			} else {
				cx, cy = center, perpCenter
			}
			// Place the bbox center at (cx,cy). The anchor→center offset is fixed on
			// the part, so it rotates with it (Δ = targetRot − current).
			bcx, bcy := sat.bboxCenter()
			odx, ody := rotateVec(bcx-sat.x, bcy-sat.y, targetRot-sat.rotation)
			moves = append(moves, apMove{
				ID:         sat.id,
				Designator: sat.designator,
				NewX:       round2(cx - odx),
				NewY:       round2(cy - ody),
				NewRot:     targetRot,
				SetRot:     oriented,
				Main:       chip.designator,
				Edge:       k.edge.String(),
				TargetNet:  a.targetNet,
				Via:        a.via,
			})
		}
	}
	return moves, diags
}

// edgeFor returns the chip bbox edge nearest a target pad, plus the pad's
// coordinate along that edge's axis (the value the packer aligns to).
func edgeFor(chip apComp, pad apPad) (apEdge, float64) {
	dl := pad.x - chip.minX
	dr := chip.maxX - pad.x
	db := pad.y - chip.minY
	dt := chip.maxY - pad.y
	best, edge := dl, edgeLeft
	if dr < best {
		best, edge = dr, edgeRight
	}
	if dt < best {
		best, edge = dt, edgeTop
	}
	if db < best {
		best, edge = db, edgeBottom
	}
	if edge.vertical() {
		return edge, pad.y
	}
	return edge, pad.x
}

func compHasLocalNet(c apComp, net string) bool {
	return slices.Contains(c.localNets(), net)
}

// spaceMains returns a per-main horizontal shift (dx) that lays the chips out
// left-to-right with at least multiGap between adjacent bboxes. The leftmost chip
// stays put; each subsequent chip is pushed right only if it overlaps / is closer
// than multiGap to the previous one (already-roomy chips get dx=0). Returned slice
// is indexed by position in `mains`.
func spaceMains(comps []apComp, mains []int, multiGap float64) []float64 {
	shifts := make([]float64, len(mains))
	order := make([]int, len(mains))
	for i := range mains {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		return comps[mains[order[a]]].minX < comps[mains[order[b]]].minX
	})
	prevRight := math.Inf(-1)
	for _, mi := range order {
		c := comps[mains[mi]]
		left := c.minX
		if prevRight > math.Inf(-1) { // every chip after the leftmost
			if required := prevRight + multiGap; left < required {
				shifts[mi] = required - left
				left = required
			}
		}
		prevRight = left + c.width()
	}
	return shifts
}

// shiftComp returns a copy of c translated by dx (anchor, bbox, and every pad),
// so the planner can reason/route against a chip's spaced-out position.
func shiftComp(c apComp, dx float64) apComp {
	out := c
	out.x += dx
	out.minX += dx
	out.maxX += dx
	out.pads = make([]apPad, len(c.pads))
	for i, p := range c.pads {
		p.x += dx
		out.pads[i] = p
	}
	return out
}

// nearestPad returns the pad in pads closest to (px,py).
func nearestPad(pads []apPad, px, py float64) (apPad, bool) {
	best := -1
	bestD := math.MaxFloat64
	for i, p := range pads {
		if d := math.Hypot(p.x-px, p.y-py); d < bestD {
			bestD, best = d, i
		}
	}
	if best < 0 {
		return apPad{}, false
	}
	return pads[best], true
}

// rotateVec rotates (x,y) by deg (y-up CCW).
func rotateVec(x, y, deg float64) (float64, float64) {
	r := deg * math.Pi / 180
	cs, sn := math.Cos(r), math.Sin(r)
	return x*cs - y*sn, x*sn + y*cs
}

// satPadOnNet returns a 2-pin satellite's pad on the given net.
func satPadOnNet(c apComp, net string) (apPad, bool) {
	for _, p := range c.pads {
		if strings.TrimSpace(p.net) == net {
			return p, true
		}
	}
	return apPad{}, false
}

// edgeFacing is the unit direction from a satellite on `edge` toward the chip it
// hugs (left edge → satellite sits left of chip → faces +x).
func edgeFacing(e apEdge) (float64, float64) {
	switch e {
	case edgeLeft:
		return 1, 0
	case edgeRight:
		return -1, 0
	case edgeTop:
		return 0, -1
	default: // bottom
		return 0, 1
	}
}

// orientSatellite decides the target rotation for a 2-pin satellite so its
// connecting pad faces the chip, and returns the effective (post-rotation) bbox
// width/height. For parts we don't re-orient it returns the current rotation and
// bbox unchanged. The candidate rotations keep the pad axis perpendicular to the
// edge (horizontal for L/R, vertical for T/B); we pick the one (of the 2) whose
// connecting pad points most toward the chip.
func orientSatellite(sat apComp, e apEdge, targetNet string, rotate bool) (targetRot, effW, effH float64, oriented bool) {
	curW, curH := sat.width(), sat.height()
	if !rotate || sat.distinctPins() != 2 {
		return sat.rotation, curW, curH, false
	}
	pad, ok := satPadOnNet(sat, targetNet)
	if !ok {
		return sat.rotation, curW, curH, false
	}
	// Native dims (footprint at rotation 0): current bbox is swapped when the part
	// currently sits at an odd 90°.
	natW, natH := curW, curH
	if oddQuarter(sat.rotation) {
		natW, natH = curH, curW
	}
	// Pad offset from the anchor, in the native (rotation-0) frame.
	ndx, ndy := rotateVec(pad.x-sat.x, pad.y-sat.y, -sat.rotation)
	fx, fy := edgeFacing(e)
	candidates := []float64{0, 180} // vertical edge → pad axis horizontal
	if !e.vertical() {
		candidates = []float64{90, 270} // horizontal edge → pad axis vertical
	}
	bestRot, bestScore := candidates[0], math.Inf(-1)
	for _, r := range candidates {
		rx, ry := rotateVec(ndx, ndy, r)
		if score := rx*fx + ry*fy; score > bestScore {
			bestScore, bestRot = score, r
		}
	}
	// Effective bbox after applying bestRot to the native footprint.
	if oddQuarter(bestRot) {
		return bestRot, natH, natW, true
	}
	return bestRot, natW, natH, true
}

func oddQuarter(deg float64) bool {
	q := int(math.Round(deg/90)) % 2
	return q == 1 || q == -1
}
