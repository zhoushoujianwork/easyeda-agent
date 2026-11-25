package app

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
//
// v2 = cost-based slot search (the assignment passes 1-3 are unchanged; only the
// packing stage changed). The idea of scoring candidate slots instead of taking
// the first free one comes from the open-source low-power-iot-hardware-design
// layout planner (MIT, github.com/donghuixin/low-power-iot-hardware-design).
//
//   - 2-D occupancy: one list of placed satellite bodies is shared by ALL
//     (main, edge) groups, so satellites on adjacent edges can no longer collide
//     at a chip corner. A slot is free iff it stays on the board, clear of every
//     keep-out, clear (zero clearance) of every non-moving part's bbox (mains,
//     locked parts, fixed group members, skipped connectors) and at least
//     opt.pitch from every placed satellite.
//   - Rings: each group may use up to apRings rows parallel to the edge. Row r
//     sits r*ringStep further out, ringStep = widest satellite of the group +
//     pitch, so a blocked first row degrades to a second row instead of failing.
//   - Cost: candidates are visited in ascending lowerBound = |along offset| +
//     ring*ringStep and the search stops once lowerBound exceeds the best cost.
//     cost = lowerBound + escape penalty (the body sits in front of a pad of a
//     DIFFERENT net on this chip edge, which blocks that pad's fan-out) + avoid
//     penalty (inductors keep away from keep-outs and crystals).
//   - Order inside a group: decoupling parts first, smallest capacitance nearest
//     the pin; then crystals; then everything else by target position.
//   - Distance limits: a decoupling part should sit within apLimitPowerMil of its
//     pin, a crystal within apLimitCrystalMil. A part that ends up farther is
//     still placed, but a diag names what blocks the nearer slots.

// apPad is one component pad: its net (by name), rendered center, and copper layer.
type apPad struct {
	num   string
	net   string
	x     float64
	y     float64
	layer int
	w, h  float64 // real copper extents (mil); 0 = unknown (old connector / polygon pad)
}

// apComp is a placed component with the geometry the planner reasons over.
type apComp struct {
	id         string
	designator string
	name       string  // component Name attribute (usually the value, e.g. "100nF"); "" if unknown
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
	NewRot     float64 `json:"newRot"` // target rotation (deg); == current when SetRot is false
	SetRot     bool    `json:"setRot"` // whether to apply NewRot (only re-oriented 2-pin satellites)
	Main       string  `json:"main"`
	Edge       string  `json:"edge"`
	TargetNet  string  `json:"targetNet"`
	Via        string  `json:"via,omitempty"` // "local" | "power" | "chain:<designator>"
	// FollowsID 记录跟随血缘：这个 move 的落点是跟着哪个件（primitiveId）的
	// 位移算出来的。合法化弃掉伙伴的 move 时必须连带弃掉跟随者 —— 否则跟随者
	// 停在「伙伴本要去而没去」的半路上（真板实锤：J2 的 move 被弃，TVS/ESD
	// 却已跟着幽灵位移搬走，protection 从贴身变成 800+mil）。
	FollowsID string `json:"followsId,omitempty"`
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
	// #131 explicit overrides (designators, upper-cased): anchors force a part
	// into the main set regardless of pin count / connector heuristics;
	// excludeMain bars one from it. Explicit beats every heuristic.
	anchors     map[string]bool
	excludeMain map[string]bool
	// keepouts are placement-forbidden rectangles (antenna/keep-out regions);
	// board, when known, bounds every satellite body. Live 2026-10-13: the old
	// one-way packer pushed C1/C3 up into the ESP32 antenna keep-out.
	keepouts []layoutBBox
	board    *layoutBBox
	// fixed are virtual PCB group members (#173, upper-cased designators): never
	// moved. A fixed satellite stays put and becomes a keep-out for the others;
	// a fixed main stays an anchor and vetoes multi-chip spacing.
	fixed map[string]bool
}

// Satellite slot search along an edge: step and how far from the target pad.
const (
	apSearchStepMil = 5.0
	apSearchSpanMil = 2000.0
)

func defaultApOptions() apOptions {
	return apOptions{mainPins: 8, gap: 40, pitch: 30, rotate: true, multiGap: 150}
}

// designatorSet parses a CSV of designators into an upper-cased membership set
// (nil for an empty flag, so map lookups stay cheap no-ops).
func designatorSet(csv string) map[string]bool {
	var set map[string]bool
	for _, d := range strings.Split(csv, ",") {
		d = strings.ToUpper(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if set == nil {
			set = map[string]bool{}
		}
		set[d] = true
	}
	return set
}

// isMainComp is THE main/anchor test (#131). Pin count alone is not enough: a
// 16-pad USB-C or a boxed pin header out-pins many a small IC, and calling it
// "main" makes it STEAL the satellites (decoupling caps grouped against a
// connector instead of the regulator they serve). So a connector-designated
// part (J*/CN*/USB*/… — connectorDesRe) NEVER competes for main, whatever its
// pin count: connectors are mechanical anchors for place-constrained, not
// electrical owners. Explicit --anchor / --exclude-main win over everything.
func isMainComp(c apComp, opt apOptions) bool {
	des := strings.ToUpper(strings.TrimSpace(c.designator))
	if opt.excludeMain[des] {
		return false
	}
	if opt.anchors[des] {
		return true
	}
	return c.distinctPins() >= opt.mainPins && !connectorDesRe.MatchString(des)
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
	// pad is the same-net chip pad the assignment was derived from (the point the
	// distance limits measure to). Chain assignments inherit their parent's pad.
	pad    apPad
	hasPad bool
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
		highPin := c.distinctPins() >= opt.mainPins
		switch {
		case isMainComp(c, opt):
			mains = append(mains, i)
		case c.locked:
			continue
		case opt.fixed[strings.ToUpper(strings.TrimSpace(c.designator))]:
			// 虚拟组成员(#173):原地不动。它不是卫星候选,所以和其他未动的件一样
			// 进入打包阶段的障碍物表 —— 否则卫星会被打包进它的位置。
			diags = append(diags, apDiag{c.designator, "group:fixed — virtual PCB group member, not moved (--break-group to override)"})
		case highPin && connectorDesRe.MatchString(strings.TrimSpace(c.designator)):
			// #131: a high-pin connector (USB-C, boxed header…) is a MECHANICAL
			// anchor, not an electrical main — it must neither move nor own
			// satellites. Leave it for place-constrained, like its low-pin kin.
			diags = append(diags, apDiag{c.designator, "high-pin connector — mechanical anchor, not a main (#131); `place-constrained` seats it, satellites go to the ICs they serve"})
		case highPin:
			// Only reachable via --exclude-main: a demoted big part stays put —
			// dragging an IC around as a "satellite" would be worse than the bug.
			diags = append(diags, apDiag{c.designator, "excluded from main by --exclude-main — left in place"})
		case isEdgeConnector(c, opt.mainPins):
			// Board-edge connector (J*/CN*/…): leave it where the user (or
			// place-constrained) put it — don't drag it toward a chip by its GND pad.
			diags = append(diags, apDiag{c.designator, "board-edge connector — skipped, use `place-constrained` to seat it on the edge"})
		default:
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
		for mi, m := range mains {
			if shifts[mi] != 0 && opt.fixed[strings.ToUpper(strings.TrimSpace(comps[m].designator))] {
				// 组成员主芯片不能被横向拉开;整步跳过,而不是只挪一部分主芯片。
				diags = append(diags, apDiag{comps[m].designator, "chip-spacing skipped — this main chip is a virtual PCB group member (--break-group to allow)"})
				shifts = make([]float64, len(shifts))
				break
			}
		}
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
			assigned[s] = &assignment{sat: s, mainIdx: best, edge: e, along: along, targetNet: bestNet, via: "local", pad: bestPad, hasPad: true}
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
					assigned[s] = &assignment{sat: s, mainIdx: a.mainIdx, edge: a.edge, along: a.along, targetNet: ln, via: "chain:" + comps[parent].designator, pad: a.pad, hasPad: a.hasPad}
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
			assigned[s] = &assignment{sat: s, mainIdx: best, edge: e, along: along, targetNet: bestNet, via: "power", pad: bestPad, hasPad: true}
		}
	}

	// Diagnostics for the genuinely unplaceable (no net path to any chip).
	for _, s := range sats {
		if assigned[s] == nil {
			diags = append(diags, apDiag{comps[s].designator, "no shared net path to a main chip"})
		}
	}

	// Obstacles: every placed part that stays where it is — mains (after the
	// chip-spacing shift), locked parts, fixed group members, skipped connectors,
	// excluded high-pin parts and satellites with no net path (they are not
	// moved either). Moving satellites are NOT obstacles: they vacate their spot.
	pk := &apPacker{opt: opt}
	for i, c := range comps {
		if !c.hasBBox || assigned[i] != nil {
			continue
		}
		pk.obstacles = append(pk.obstacles, apOccupant{
			box:     layoutBBox{MinX: c.minX, MinY: c.minY, MaxX: c.maxX, MaxY: c.maxY},
			des:     strings.TrimSpace(c.designator),
			crystal: apCrystalRe.MatchString(strings.TrimSpace(c.designator)),
		})
	}

	// Pack per (main, edge). All groups share one occupancy list (pk.placed), so
	// satellites on adjacent edges cannot collide at a chip corner.
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
		chip := comps[mains[k.main]]
		moves, diags = pk.packGroup(comps, chip, k.edge, groups[k], moves, diags)
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

// ── v2 packing: 2-D occupancy + cost-based slot search ───────────────────────

const (
	apRings                = 3     // rows parallel to a chip edge a satellite may use (0 = innermost)
	apEscapeDepthMil       = 50.0  // how far in front of a foreign pad its escape strip reaches
	apEscapeExtraMil       = 10.0  // extra along-width added to the pad extent
	apEscapePadFallbackMil = 20.0  // pad extent when the pad size is unknown
	apEscapePenaltyMil     = 25.0  // cost of a body lying completely inside escape strips
	apAvoidRadiusMil       = 300.0 // inductor distance to keep-outs / crystals below which it is penalised
	apAvoidWeight          = 0.5
	apLimitPowerMil        = 100.0 // decoupling part: max distance from its pin before a diag is raised
	apLimitCrystalMil      = 160.0 // crystal: same
	apEps                  = 1e-6  // overlap tolerance (floating point noise)
)

var (
	apCrystalRe  = regexp.MustCompile(`(?i)^(?:Y|X)\d`)
	apInductorRe = regexp.MustCompile(`(?i)^L\d`)
	// apCapValueRe accepts "100nF", "0.1uF", "0.1µF", "10u", "4.7uF/16V", "22 pF".
	// A letter right after the unit ("10uH", "100mΩ") is not a capacitance.
	apCapValueRe = regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*([pnuµμm])\s*(f?)(?:$|[^\p{L}\p{N}])`)
)

// parseCapacitanceFarads reads a capacitance such as "100nF" or "4.7uF/16V" from a
// component Name. Part numbers and anything else that does not start with
// <number><p|n|u|µ|m>[F] report ok=false.
func parseCapacitanceFarads(s string) (float64, bool) {
	m := apCapValueRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	if strings.EqualFold(m[2], "m") && m[3] == "" {
		return 0, false // a bare "10m" is too ambiguous (milli-what?) — require "mF"
	}
	switch strings.ToLower(m[2]) {
	case "p":
		return v * 1e-12, true
	case "n":
		return v * 1e-9, true
	case "u", "µ", "μ":
		return v * 1e-6, true
	case "m":
		return v * 1e-3, true
	}
	return 0, false
}

// apOccupant is a box the packer must not overlap: an obstacle (a part that
// stays put) or an already placed satellite.
type apOccupant struct {
	box     layoutBBox
	des     string
	crystal bool
}

// apPacker holds the occupancy shared by every (main, edge) group.
type apPacker struct {
	opt       apOptions
	obstacles []apOccupant // parts that do not move (zero clearance)
	placed    []apOccupant // satellites already placed (pitch clearance)
}

// apSatPrep is one satellite of a group with its post-rotation geometry.
type apSatPrep struct {
	a                 *assignment
	sat               apComp
	rot, effW, effH   float64
	oriented          bool
	perp, along       float64 // effective extent perpendicular / parallel to the edge
	rank              int     // 0 decoupling, 1 crystal, 2 other
	capKey            float64 // capacitance in femtofarads (rounded), valid when capOK
	capOK             bool
	crystal, inductor bool
}

// apSlot is one candidate position: ring and signed offset along the edge.
type apSlot struct {
	ring int
	sign int // -1 / 0 / +1
	d    float64
	lb   float64 // |d| + ring*ringStep, a lower bound of the cost
}

func apOverlapExtents(a, b layoutBBox) (float64, float64) {
	return math.Min(a.MaxX, b.MaxX) - math.Max(a.MinX, b.MinX),
		math.Min(a.MaxY, b.MaxY) - math.Max(a.MinY, b.MinY)
}

func apOverlaps(a, b layoutBBox) bool {
	ox, oy := apOverlapExtents(a, b)
	return ox > apEps && oy > apEps
}

func apOverlapArea(a, b layoutBBox) float64 {
	ox, oy := apOverlapExtents(a, b)
	if ox <= apEps || oy <= apEps {
		return 0
	}
	return ox * oy
}

func apInflate(b layoutBBox, d float64) layoutBBox {
	return layoutBBox{MinX: b.MinX - d, MinY: b.MinY - d, MaxX: b.MaxX + d, MaxY: b.MaxY + d}
}

// apPointRectDist is the distance from a point to a rectangle (0 inside).
func apPointRectDist(px, py float64, b layoutBBox) float64 {
	dx := math.Max(0, math.Max(b.MinX-px, px-b.MaxX))
	dy := math.Max(0, math.Max(b.MinY-py, py-b.MaxY))
	return math.Hypot(dx, dy)
}

func apAlongRange(b layoutBBox, e apEdge) (float64, float64) {
	if e.vertical() {
		return b.MinY, b.MaxY
	}
	return b.MinX, b.MaxX
}

// apPerpCenter is the perpendicular center of a body of perpendicular extent
// perp whose inner side sits `off` outward of the chip edge.
func apPerpCenter(chip apComp, e apEdge, off, perp float64) float64 {
	switch e {
	case edgeLeft:
		return chip.minX - off - perp/2
	case edgeRight:
		return chip.maxX + off + perp/2
	case edgeTop:
		return chip.maxY + off + perp/2
	default:
		return chip.minY - off - perp/2
	}
}

// apFrontRegion is the rectangle starting at the chip edge, `depth` deep
// outward, `halfSpan` either side of `along`.
func apFrontRegion(chip apComp, e apEdge, along, halfSpan, depth float64) layoutBBox {
	switch e {
	case edgeLeft:
		return layoutBBox{MinX: chip.minX - depth, MaxX: chip.minX, MinY: along - halfSpan, MaxY: along + halfSpan}
	case edgeRight:
		return layoutBBox{MinX: chip.maxX, MaxX: chip.maxX + depth, MinY: along - halfSpan, MaxY: along + halfSpan}
	case edgeTop:
		return layoutBBox{MinX: along - halfSpan, MaxX: along + halfSpan, MinY: chip.maxY, MaxY: chip.maxY + depth}
	default:
		return layoutBBox{MinX: along - halfSpan, MaxX: along + halfSpan, MinY: chip.minY - depth, MaxY: chip.minY}
	}
}

// apOwnNets is the set of non-empty nets on the satellite's pads.
func apOwnNets(c apComp) map[string]bool {
	out := map[string]bool{}
	for _, p := range c.pads {
		if n := strings.TrimSpace(p.net); n != "" {
			out[n] = true
		}
	}
	return out
}

// apEscapeStrips returns the fan-out strips in front of every pad of `chip` on
// `edge` whose net is not one of the satellite's own: a body parked there blocks
// the pad's escape. Identical strips (a pad repeated on one net) are deduped.
func apEscapeStrips(chip apComp, edge apEdge, own map[string]bool) []layoutBBox {
	var out []layoutBBox
	seen := map[layoutBBox]bool{}
	for _, p := range chip.pads {
		net := strings.TrimSpace(p.net)
		if net == "" || own[net] {
			continue
		}
		e, along := edgeFor(chip, p)
		if e != edge {
			continue
		}
		ext := p.w
		if edge.vertical() {
			ext = p.h
		}
		if ext <= 0 {
			ext = apEscapePadFallbackMil
		}
		s := apFrontRegion(chip, edge, along, (ext+apEscapeExtraMil)/2, apEscapeDepthMil)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// apEscapePenalty scales apEscapePenaltyMil by the share of the body covered by
// the strips (summed overlaps, capped at the body area).
func apEscapePenalty(body layoutBBox, strips []layoutBBox) float64 {
	area := (body.MaxX - body.MinX) * (body.MaxY - body.MinY)
	if area <= 0 || len(strips) == 0 {
		return 0
	}
	sum := 0.0
	for _, s := range strips {
		sum += apOverlapArea(body, s)
	}
	return apEscapePenaltyMil * math.Min(sum, area) / area
}

// apDistLimit is the farthest a satellite should sit from its target pad (0 = no limit).
func apDistLimit(via string, crystal bool) float64 {
	switch {
	case via == "power":
		return apLimitPowerMil
	case crystal:
		return apLimitCrystalMil
	}
	return 0
}

// free reports whether a body may be placed: on the board, outside keep-outs and
// every non-moving part (zero clearance), and pitch away from placed satellites.
func (pk *apPacker) free(b layoutBBox) bool {
	if bd := pk.opt.board; bd != nil && (b.MinX < bd.MinX-apEps || b.MaxX > bd.MaxX+apEps || b.MinY < bd.MinY-apEps || b.MaxY > bd.MaxY+apEps) {
		return false
	}
	for _, ko := range pk.opt.keepouts {
		if apOverlaps(b, ko) {
			return false
		}
	}
	for _, o := range pk.obstacles {
		if apOverlaps(b, o.box) {
			return false
		}
	}
	infl := apInflate(b, pk.opt.pitch)
	for _, o := range pk.placed {
		if apOverlaps(infl, o.box) {
			return false
		}
	}
	return true
}

// avoidPenalty keeps inductors away from keep-outs and crystals (noise): 0.5 per
// mil closer than apAvoidRadiusMil to each of them.
func (pk *apPacker) avoidPenalty(b layoutBBox, inductor bool) float64 {
	if !inductor {
		return 0
	}
	pen := 0.0
	add := func(r layoutBBox) {
		if d := rectGap(b, r); d < apAvoidRadiusMil {
			pen += apAvoidWeight * (apAvoidRadiusMil - d)
		}
	}
	for _, ko := range pk.opt.keepouts {
		add(ko)
	}
	for _, o := range pk.obstacles {
		if o.crystal {
			add(o.box)
		}
	}
	for _, o := range pk.placed {
		if o.crystal {
			add(o.box)
		}
	}
	return pen
}

// blockerReason explains why a satellite could not get a slot near its pad: it
// scans the region just outside the chip edge in front of the target pad
// (along ±limit, depth gap+limit). `slot` is the ideal (ring 0, on-target) body.
func (pk *apPacker) blockerReason(chip apComp, edge apEdge, p *apSatPrep, slot layoutBBox, limit float64) string {
	opt := pk.opt
	region := apFrontRegion(chip, edge, p.a.along, limit, opt.gap+limit)
	type blocker struct {
		name string
		area float64
		box  layoutBBox
	}
	var blks []blocker
	for _, o := range pk.obstacles {
		if ar := apOverlapArea(region, o.box); ar > 0 {
			name := o.des
			if name == "" {
				name = "part"
			}
			blks = append(blks, blocker{name, ar, o.box})
		}
	}
	for _, ko := range opt.keepouts {
		if ar := apOverlapArea(region, ko); ar > 0 {
			blks = append(blks, blocker{"keep-out", ar, ko})
		}
	}
	sib := 0.0
	for _, o := range pk.placed {
		sib += apOverlapArea(region, o.box)
	}
	sort.SliceStable(blks, func(i, j int) bool {
		a, b := blks[i], blks[j]
		if a.area != b.area {
			return a.area > b.area
		}
		if a.name != b.name {
			return a.name < b.name
		}
		if a.box.MinX != b.box.MinX {
			return a.box.MinX < b.box.MinX
		}
		return a.box.MinY < b.box.MinY
	})
	if len(blks) > 0 && blks[0].area >= sib {
		slo, shi := apAlongRange(slot, edge)
		for _, b := range blks {
			if !apOverlaps(slot, b.box) {
				continue // does not stand on the ideal slot
			}
			blo, bhi := apAlongRange(b.box, edge)
			n := math.Min(shi-blo, bhi-slo) // shortest shift along the edge that frees the slot
			if n <= 0 {
				continue
			}
			n = math.Ceil(n/5-1e-9) * 5
			return fmt.Sprintf("blocked by %s; move it ~%.0fmil away", b.name, n)
		}
	}
	if sib > 0 {
		return fmt.Sprintf("crowded: too many satellites on %s %s edge", chip.designator, edge)
	}
	return "no closer free slot"
}

// packGroup places one (main, edge) group. Decoupling parts claim slots first
// (smallest capacitance nearest the pin), then crystals, then the rest; each takes
// the minimum-cost free slot over rings × along offsets (both directions).
func (pk *apPacker) packGroup(comps []apComp, chip apComp, edge apEdge, group []*assignment, moves []apMove, diags []apDiag) ([]apMove, []apDiag) {
	opt := pk.opt
	preps := make([]*apSatPrep, 0, len(group))
	maxPerp := 0.0
	for _, a := range group {
		sat := comps[a.sat]
		// Re-orient (2-pin parts) so the connecting pad faces the chip; pack with
		// the EFFECTIVE (post-rotation) bbox so rotated parts still don't overlap.
		rot, effW, effH, oriented := orientSatellite(sat, edge, a.targetNet, opt.rotate)
		p := &apSatPrep{a: a, sat: sat, rot: rot, effW: effW, effH: effH, oriented: oriented}
		if edge.vertical() {
			p.perp, p.along = effW, effH
		} else {
			p.perp, p.along = effH, effW
		}
		des := strings.TrimSpace(sat.designator)
		p.crystal = apCrystalRe.MatchString(des)
		p.inductor = apInductorRe.MatchString(des)
		if f, ok := parseCapacitanceFarads(sat.name); ok {
			p.capKey, p.capOK = math.Round(f*1e15), true
		}
		switch {
		case a.via == "power":
			p.rank = 0
		case p.crystal:
			p.rank = 1
		default:
			p.rank = 2
		}
		maxPerp = math.Max(maxPerp, p.perp)
		preps = append(preps, p)
	}
	sort.SliceStable(preps, func(i, j int) bool {
		a, b := preps[i], preps[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.rank == 0 && a.capOK != b.capOK {
			return a.capOK // a known capacitance sorts before an unknown one
		}
		if a.rank == 0 && a.capOK && a.capKey != b.capKey {
			return a.capKey < b.capKey
		}
		if a.a.along != b.a.along {
			return a.a.along < b.a.along
		}
		return a.sat.designator < b.sat.designator
	})

	ringStep := maxPerp + opt.pitch
	body := func(p *apSatPrep, ring int, center float64) layoutBBox {
		pc := apPerpCenter(chip, edge, opt.gap+float64(ring)*ringStep, p.perp)
		if edge.vertical() {
			return layoutBBox{MinX: pc - p.effW/2, MaxX: pc + p.effW/2, MinY: center - p.effH/2, MaxY: center + p.effH/2}
		}
		return layoutBBox{MinX: center - p.effW/2, MaxX: center + p.effW/2, MinY: pc - p.effH/2, MaxY: pc + p.effH/2}
	}

	// Candidate template, visited in ascending lowerBound; ties: smaller ring, then
	// the -d side first (as the old nearest-free search did).
	var slots []apSlot
	for r := 0; r < apRings; r++ {
		for k := 0; float64(k)*apSearchStepMil <= apSearchSpanMil; k++ {
			d := float64(k) * apSearchStepMil
			lb := d + float64(r)*ringStep
			if k == 0 {
				slots = append(slots, apSlot{ring: r, sign: 0, d: 0, lb: lb})
				continue
			}
			slots = append(slots, apSlot{ring: r, sign: -1, d: d, lb: lb}, apSlot{ring: r, sign: 1, d: d, lb: lb})
		}
	}
	sort.SliceStable(slots, func(i, j int) bool {
		a, b := slots[i], slots[j]
		if a.lb != b.lb {
			return a.lb < b.lb
		}
		if a.ring != b.ring {
			return a.ring < b.ring
		}
		return a.sign < b.sign
	})

	for _, p := range preps {
		a, sat := p.a, p.sat
		strips := apEscapeStrips(chip, edge, apOwnNets(sat))
		limit := apDistLimit(a.via, p.crystal)

		var bestBody layoutBBox
		var bestCenter, bestCost float64
		found := false
		for _, sl := range slots {
			if found && sl.lb > bestCost {
				break
			}
			center := a.along + float64(sl.sign)*sl.d
			b := body(p, sl.ring, center)
			if !pk.free(b) {
				continue
			}
			cost := sl.lb + apEscapePenalty(b, strips) + pk.avoidPenalty(b, p.inductor)
			if !found || cost < bestCost {
				bestBody, bestCenter, bestCost, found = b, center, cost, true
			}
		}

		scan := limit
		if scan <= 0 {
			scan = apLimitCrystalMil
		}
		if !found {
			reason := pk.blockerReason(chip, edge, p, body(p, 0, a.along), scan)
			diags = append(diags, apDiag{sat.designator, fmt.Sprintf("no free slot on %s's %s edge within %.0fmil of its pin (keep-outs/board edge/other satellites) — %s", chip.designator, edge, apSearchSpanMil, reason)})
			continue
		}

		var cx, cy float64
		if edge.vertical() {
			cx, cy = (bestBody.MinX+bestBody.MaxX)/2, bestCenter
		} else {
			cx, cy = bestCenter, (bestBody.MinY+bestBody.MaxY)/2
		}
		if limit > 0 && a.hasPad {
			if dist := apPointRectDist(a.pad.x, a.pad.y, bestBody); dist > limit+apEps {
				reason := pk.blockerReason(chip, edge, p, body(p, 0, a.along), limit)
				diags = append(diags, apDiag{sat.designator, fmt.Sprintf("placed %.0fmil from %s.%s (limit %.0fmil) — %s", math.Ceil(dist-apEps), chip.designator, a.pad.net, limit, reason)})
			}
		}
		pk.placed = append(pk.placed, apOccupant{box: bestBody, des: strings.TrimSpace(sat.designator), crystal: p.crystal})

		// Place the bbox center at (cx,cy). The anchor→center offset is fixed on
		// the part, so it rotates with it (Δ = targetRot − current).
		bcx, bcy := sat.bboxCenter()
		odx, ody := rotateVec(bcx-sat.x, bcy-sat.y, p.rot-sat.rotation)
		moves = append(moves, apMove{
			ID:         sat.id,
			Designator: sat.designator,
			NewX:       round2(cx - odx),
			NewY:       round2(cy - ody),
			NewRot:     p.rot,
			SetRot:     p.oriented,
			Main:       chip.designator,
			Edge:       edge.String(),
			TargetNet:  a.targetNet,
			Via:        a.via,
		})
	}
	return moves, diags
}
