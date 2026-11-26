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
//
// v2.1 = RF matching chain. An antenna (designator ANT*/AE* or a Name containing
// "antenna") is a mechanical anchor: never moved, an obstacle. With no RF net
// class in the netlist the chain is found by topology: from the antenna feed net,
// breadth-first through 2-pin series parts (one pad on the current net, the other
// on another signal net) up to apRfMaxHops, until a net with a main-chip pad. The
// series parts, plus 2-pin shunts (path net to ground), are placed BEFORE any
// other group on the straight line between the chip RF pad and the antenna feed
// pad, antenna end first (placeRfChain, ported from the same planner). Keep-outs
// that touch the antenna zone are ignored for chain members; a member that cannot
// stay on the line falls back to the generic cost search and gets a diag.

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
	layer      int // assembly side from the readback: 1 top, 2 bottom, other = unknown
	hasBBox    bool
	minX, minY float64
	maxX, maxY float64
	pads       []apPad
}

func (c apComp) bboxCenter() (float64, float64) {
	return (c.minX + c.maxX) / 2, (c.minY + c.maxY) / 2
}
func (c apComp) width() float64 { return c.maxX - c.minX }

// side is the board side the part occupies for collision purposes: 1 top,
// 2 bottom, 0 both/unknown (unknown layer, or a through-hole part with a MULTI
// pad, which blocks both sides). Parts on opposite sides never collide — a
// double-sided board puts decoupling caps under the chip they serve.
func (c apComp) side() int {
	if c.layer != 1 && c.layer != 2 {
		return 0
	}
	for _, p := range c.pads {
		if p.layer == pcbLayerMulti {
			return 0
		}
	}
	return c.layer
}

// sameSide reports whether two sides can collide (0 = both sides).
func sameSide(a, b int) bool     { return a == 0 || b == 0 || a == b }
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
	// owners pins satellites to a main (upper-cased designator → upper-cased main
	// designator): a pinned satellite is only ever assigned to that main (pass 1
	// and 3 look at that main's pads only; pass 2 only inherits from a parent on
	// that main). A pin whose target is not a main on this board is ignored for
	// that part (the caller reports it). RF chain members are not affected: they
	// follow their antenna's chip.
	owners map[string]string
	// stay lists comps indices of satellites that keep their current pose. They
	// are still assigned (so pass-2 chains and owners resolve as without stay) but
	// are then not packed and become obstacles for everything else. An RF chain
	// stays only when ALL its members are in stay; otherwise it is placed whole.
	stay map[int]bool
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
//
// Small chips count too: an IC-style designator (apMainPrefixes: U, IC, VR, REG,
// PS) with at least apMainPrefixMinPins distinct pins is a main even below
// mainPins, so an LDO or DC-DC owns its own caps instead of being dragged to the
// MCU. Passive prefixes (R C L D LED FB Y X F TP Q…) are never promoted.
func isMainComp(c apComp, opt apOptions) bool {
	des := strings.ToUpper(strings.TrimSpace(c.designator))
	if opt.excludeMain[des] {
		return false
	}
	if opt.anchors[des] {
		return true
	}
	if isAntennaComp(c) {
		return false // mechanical anchor, never the owner of satellites
	}
	if connectorDesRe.MatchString(des) {
		return false
	}
	if c.distinctPins() >= opt.mainPins {
		return true
	}
	return apMainPrefixes[apDesPrefix(des)] && c.distinctPins() >= apMainPrefixMinPins
}

// apMainPrefixes are the designator prefixes of IC-style parts that are mains
// from apMainPrefixMinPins distinct pins on, whatever mainPins says.
var apMainPrefixes = map[string]bool{"U": true, "IC": true, "VR": true, "REG": true, "PS": true}

const apMainPrefixMinPins = 3

// apDesPrefix is the upper-cased run of letters before the first non-letter
// of a designator ("u12" → "U", "LED3" → "LED", "1U" → "").
func apDesPrefix(des string) string {
	des = strings.ToUpper(strings.TrimSpace(des))
	end := 0
	for end < len(des) && des[end] >= 'A' && des[end] <= 'Z' {
		end++
	}
	return des[:end]
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
	moves, diags, _ := planAutoPlaceDetailed(comps, opt)
	return moves, diags
}

// apPlanInfo is the planner's own classification of the parts (indices into the
// comps slice passed in), exposed so offline callers (pcb layout simulate) agree
// with what planAutoPlace actually moves instead of re-deriving the rules.
type apPlanInfo struct {
	Mains   []int       // anchors (chips)
	Movable []int       // every part the planner may move: satellites AND RF chain members
	AntChip map[int]int // antenna index → index of the chip its RF chain ends on
	// Owner maps every satellite the planner assigned (including stay ones and
	// RF chain members) to the index of the main it belongs to. A satellite with
	// no net path to a main has no entry.
	Owner map[int]int
	// ChainAnt maps an RF chain member to the index of the antenna it serves.
	ChainAnt map[int]int
	// Stayed holds the satellites (and chain members) left in place because of
	// opt.stay; they are neither moved nor unplaced.
	Stayed map[int]bool
}

// planAutoPlaceDetailed is planAutoPlace plus the classification of the parts.
func planAutoPlaceDetailed(comps []apComp, opt apOptions) ([]apMove, []apDiag, apPlanInfo) {
	var mains, sats, ants []int
	var diags []apDiag
	info := apPlanInfo{AntChip: map[int]int{}, Owner: map[int]int{}, ChainAnt: map[int]int{}, Stayed: map[int]bool{}}
	for i, c := range comps {
		if !c.hasBBox {
			continue
		}
		highPin := c.distinctPins() >= opt.mainPins
		if isAntennaComp(c) && !isMainComp(c, opt) {
			ants = append(ants, i) // locked / fixed antennas still anchor an RF chain
		}
		switch {
		case isMainComp(c, opt):
			mains = append(mains, i)
		case c.locked:
			continue
		case opt.fixed[strings.ToUpper(strings.TrimSpace(c.designator))]:
			// 虚拟组成员(#173):原地不动。它不是卫星候选,所以和其他未动的件一样
			// 进入打包阶段的障碍物表 —— 否则卫星会被打包进它的位置。
			diags = append(diags, apDiag{c.designator, "group:fixed — virtual PCB group member, not moved (--break-group to override)"})
		case isAntennaComp(c):
			// Antenna (ANT*/AE* or a part named "antenna"): a mechanical anchor like a
			// board-edge connector. It never moves and becomes an obstacle below; its
			// matching network is placed by the RF chain pass.
			diags = append(diags, apDiag{c.designator, "antenna — mechanical anchor, not moved; place it on the board edge"})
		case highPin && connectorDesRe.MatchString(strings.TrimSpace(c.designator)):
			// #131: a high-pin connector (USB-C, boxed header…) is a MECHANICAL
			// anchor, not an electrical main — it must neither move nor own
			// satellites. Leave it for place-constrained, like its low-pin kin.
			diags = append(diags, apDiag{c.designator, "high-pin connector — mechanical anchor, not a main (#131); `place-constrained` seats it, satellites go to the ICs they serve"})
		case highPin:
			// Only reachable via --exclude-main (isMainComp above already took every
			// promoted part, small ICs included): a demoted big part stays put —
			// dragging an IC around as a "satellite" would be worse than the bug. A
			// demoted small IC (below mainPins) is a satellite, like any peripheral.
			diags = append(diags, apDiag{c.designator, "excluded from main by --exclude-main — left in place"})
		case isEdgeConnector(c, opt.mainPins):
			// Board-edge connector (J*/CN*/…): leave it where the user (or
			// place-constrained) put it — don't drag it toward a chip by its GND pad.
			diags = append(diags, apDiag{c.designator, "board-edge connector — skipped, use `place-constrained` to seat it on the edge"})
		default:
			sats = append(sats, i)
		}
	}

	info.Mains = append([]int(nil), mains...)
	info.Movable = append([]int(nil), sats...)

	if len(mains) == 0 {
		for _, s := range sats {
			diags = append(diags, apDiag{comps[s].designator, "no main chip on board (>= mainPins distinct pins, or a U/IC/VR/REG/PS part with >= 3) to anchor against"})
		}
		return nil, diags, info
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

	// RF matching chains (antenna → series parts → chip RF pad). Chain members
	// leave the normal passes; they are placed first, below, so they own the feed path.
	var chains []*rfChain
	chainMoving := map[int]bool{}
	if len(ants) > 0 {
		var cd []apDiag
		chains, cd = discoverRfChains(comps, sats, mains, ants, mainPadsByNet)
		diags = append(diags, cd...)
		var kept []*rfChain
		chainOut := map[int]bool{} // members that leave the normal passes
		for _, ch := range chains {
			info.AntChip[ch.ant] = mains[ch.chip]
			members := ch.members()
			allStay := len(members) > 0
			for _, m := range members {
				allStay = allStay && opt.stay[m]
				info.Owner[m] = mains[ch.chip]
				info.ChainAnt[m] = ch.ant
				chainOut[m] = true
			}
			if allStay {
				for _, m := range members {
					info.Stayed[m] = true // stays put: an obstacle, not placed
				}
				continue
			}
			kept = append(kept, ch)
			for _, m := range members {
				chainMoving[m] = true
			}
		}
		chains = kept
		if len(chainOut) > 0 {
			keptSats := make([]int, 0, len(sats))
			for _, s := range sats {
				if !chainOut[s] {
					keptSats = append(keptSats, s)
				}
			}
			sats = keptSats
		}
	}

	assigned := make(map[int]*assignment) // sat index → decision

	// Pinned owners (opt.owners): the main a satellite must belong to, -1 = free.
	mainByDes := make(map[string]int, len(mains))
	for mi, m := range mains {
		mainByDes[strings.ToUpper(strings.TrimSpace(comps[m].designator))] = mi
	}
	pinned := func(s int) int {
		if len(opt.owners) == 0 {
			return -1
		}
		if want, ok := opt.owners[strings.ToUpper(strings.TrimSpace(comps[s].designator))]; ok {
			if mi, ok := mainByDes[want]; ok {
				return mi
			}
		}
		return -1
	}

	// Pass 1 — direct: a satellite sharing a LOCAL (signal) net with a main pad
	// hugs that pad. Prefer the nearest (main, same-net pad) when several match.
	for _, s := range sats {
		scx, scy := comps[s].bboxCenter()
		best := -1
		var bestPad apPad
		var bestNet string
		bestDist := math.MaxFloat64
		pin := pinned(s)
		for _, ln := range comps[s].localNets() {
			for mi := range mains {
				if pin >= 0 && mi != pin {
					continue
				}
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
				pin := pinned(s)
				for _, o := range sats {
					if o == s || assigned[o] == nil {
						continue
					}
					if pin >= 0 && assigned[o].mainIdx != pin {
						continue // a pinned part only inherits from a neighbour on its own main
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
		pin := pinned(s)
		for _, pn := range pnets {
			for mi := range mains {
				if pin >= 0 && mi != pin {
					continue
				}
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

	// Pass 3b — every chip on a rail keeps one cap of it: nearest-pad assignment
	// hands all +3V3 caps to the MCU and leaves the LDO that drives +3V3 with no
	// output cap. A main with a pad on a non-ground rail but no power-assigned cap
	// on it takes the nearest unpinned one from a main that has two or more.
	balanceRailCaps(comps, mains, mainPadsByNet, assigned, pinned)

	// Diagnostics for the genuinely unplaceable (no net path to any chip).
	for _, s := range sats {
		if assigned[s] == nil {
			reason := "no shared net path to a main chip"
			if pin := pinned(s); pin >= 0 {
				reason = fmt.Sprintf("no shared net path to its pinned owner %s", comps[mains[pin]].designator)
			}
			diags = append(diags, apDiag{comps[s].designator, reason})
		}
	}

	// Owners are final here. A stay satellite keeps its pose: it is not packed
	// below and acts as an obstacle instead.
	for _, s := range sats {
		if a := assigned[s]; a != nil {
			info.Owner[s] = mains[a.mainIdx]
			if opt.stay[s] {
				info.Stayed[s] = true
			}
		}
	}

	// Obstacles: every placed part that stays where it is — mains (after the
	// chip-spacing shift), locked parts, fixed group members, skipped connectors,
	// excluded high-pin parts, stay satellites (opt.stay) and satellites with no
	// net path (they are not moved either). Moving satellites are NOT obstacles:
	// they vacate their spot.
	pk := &apPacker{opt: opt}
	for i, c := range comps {
		if !c.hasBBox || (assigned[i] != nil && !info.Stayed[i]) || chainMoving[i] {
			continue
		}
		pk.obstacles = append(pk.obstacles, apOccupant{
			box:     layoutBBox{MinX: c.minX, MinY: c.minY, MaxX: c.maxX, MaxY: c.maxY},
			des:     strings.TrimSpace(c.designator),
			crystal: apCrystalRe.MatchString(strings.TrimSpace(c.designator)),
			side:    c.side(),
		})
	}

	// RF chains first: they own the straight path between antenna and chip.
	for _, ch := range chains {
		moves, diags = pk.placeRfChain(comps, mains, ch, moves, diags)
	}

	// Pack per (main, edge). All groups share one occupancy list (pk.placed), so
	// satellites on adjacent edges cannot collide at a chip corner.
	type key struct {
		main int
		edge apEdge
	}
	groups := map[key][]*assignment{}
	for _, s := range sats {
		if a := assigned[s]; a != nil && !info.Stayed[s] {
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
	return moves, diags, info
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
	fx, fy := edgeFacing(e)
	candidates := []float64{0, 180} // vertical edge → pad axis horizontal
	if !e.vertical() {
		candidates = []float64{90, 270} // horizontal edge → pad axis vertical
	}
	rot, w, h, ok := orientFacing(sat, targetNet, fx, fy, candidates)
	if !ok {
		return sat.rotation, curW, curH, false
	}
	return rot, w, h, true
}

// orientFacing picks, among candidate rotations, the one that turns the pad on
// `net` most toward the direction (fx,fy), and returns it with the effective
// (post-rotation) bbox. ok=false when the part has no pad on that net.
func orientFacing(sat apComp, net string, fx, fy float64, candidates []float64) (rot, effW, effH float64, ok bool) {
	curW, curH := sat.width(), sat.height()
	pad, found := satPadOnNet(sat, net)
	if !found {
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
	side    int // see apComp.side
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

// free reports whether a body on `side` may be placed: on the board, outside
// keep-outs (every side) and every non-moving part on the same side (zero
// clearance), and pitch away from placed satellites on the same side.
func (pk *apPacker) free(b layoutBBox, side int) bool { return pk.freeSkip(b, side, nil) }

// freeSkip is free() that ignores the keep-outs for which skipKO returns true
// (RF chain members may sit inside the keep-out that surrounds their antenna).
func (pk *apPacker) freeSkip(b layoutBBox, side int, skipKO func(layoutBBox) bool) bool {
	if bd := pk.opt.board; bd != nil && (b.MinX < bd.MinX-apEps || b.MaxX > bd.MaxX+apEps || b.MinY < bd.MinY-apEps || b.MaxY > bd.MaxY+apEps) {
		return false
	}
	for _, ko := range pk.opt.keepouts {
		if skipKO != nil && skipKO(ko) {
			continue
		}
		if apOverlaps(b, ko) {
			return false
		}
	}
	for _, o := range pk.obstacles {
		if sameSide(side, o.side) && apOverlaps(b, o.box) {
			return false
		}
	}
	infl := apInflate(b, pk.opt.pitch)
	for _, o := range pk.placed {
		if sameSide(side, o.side) && apOverlaps(infl, o.box) {
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
	side := p.sat.side()
	for _, o := range pk.obstacles {
		if !sameSide(side, o.side) {
			continue
		}
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
		if sameSide(side, o.side) {
			sib += apOverlapArea(region, o.box)
		}
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
		// A part on the other side of the board does not block this side's fan-out.
		var strips []layoutBBox
		if sameSide(chip.side(), sat.side()) {
			strips = apEscapeStrips(chip, edge, apOwnNets(sat))
		}
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
			if !pk.free(b, sat.side()) {
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
		pk.placed = append(pk.placed, apOccupant{box: bestBody, des: strings.TrimSpace(sat.designator), crystal: p.crystal, side: sat.side()})

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

// ── RF matching chain (antenna → series parts → chip RF pad) ─────────────────
//
// The netlist carries no RF net class, so the chain is found by topology: an
// antenna's feed net is walked through 2-pin series parts until a net that has a
// pad on a main chip. The parts on the way (and their shunts to ground) are laid
// on the straight line between the chip RF pad and the antenna feed pad, antenna
// end first. Port of placeRfChain from the low-pin-count IoT layout planner (see
// the file header).

const (
	apRfMaxHops        = 6     // series parts a chain may have
	apRfLineLimitMil   = 40.0  // max distance of a series part's centre from the feed line
	apRfStartMil       = 12.0  // first series part starts this far from the antenna feed pad
	apRfPartGapMil     = 12.0  // gap added between consecutive series parts
	apRfAntennaKeepMil = 100.0 // keep-outs touching the antenna bbox grown by this are ignored by chain members
)

var (
	apAntennaDesRe = regexp.MustCompile(`(?i)^(?:ANT|AE)\d`)
	apRfAlongTries = []float64{0, 12, 24, 40}
	apRfPerpTries  = []float64{0, 12, -12, 24, -24, 40, -40}
	apRfShuntTries = []float64{0, 12, -12, 24, -24}
)

// apAntennaMaxPins bounds the Name-based antenna test: a real antenna / antenna
// connector has a handful of pads, while a pin-rich module whose Name merely
// mentions its on-board antenna ("ESP32-WROOM-32 (PCB antenna)") is a main chip.
const apAntennaMaxPins = 4

// isAntennaComp reports a part that is the antenna itself: designator ANT*/AE*, or
// a Name that says "antenna" on a part with at most apAntennaMaxPins distinct pads.
// It is a mechanical anchor and never moves.
func isAntennaComp(c apComp) bool {
	if apAntennaDesRe.MatchString(strings.TrimSpace(c.designator)) {
		return true
	}
	return strings.Contains(strings.ToLower(c.name), "antenna") && c.distinctPins() <= apAntennaMaxPins
}

type rfShunt struct {
	part   int // comps index
	netIdx int // index into rfChain.nets of the path net it sits on
}

// rfChain is one discovered matching network.
type rfChain struct {
	ant    int      // comps index of the antenna
	chip   int      // index into mains
	feed   apPad    // antenna feed pad (A)
	rf     apPad    // chip RF pad (P)
	nets   []string // path nets, antenna feed net first, chip net last
	series []int    // comps indices antenna→chip; series[i] joins nets[i] and nets[i+1]
	shunts []rfShunt
}

func (ch *rfChain) members() []int {
	out := append([]int(nil), ch.series...)
	for _, s := range ch.shunts {
		out = append(out, s.part)
	}
	return out
}

func apPadNumLess(a, b string) bool {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	if aerr == nil && berr == nil {
		return ai < bi
	}
	return a < b
}

// antennaFeedPad is the antenna's pad on a non-global net (lowest pad number).
func antennaFeedPad(c apComp) (apPad, bool) {
	var best apPad
	found := false
	for _, p := range c.pads {
		n := strings.TrimSpace(p.net)
		if n == "" || isGlobalNet(n) {
			continue
		}
		if !found || apPadNumLess(p.num, best.num) {
			best, found = p, true
		}
	}
	return best, found
}

// twoPadNets returns the nets of a 2-pin part's two distinct pads.
func twoPadNets(c apComp) (n0, n1 string, ok bool) {
	if c.distinctPins() != 2 {
		return "", "", false
	}
	first := ""
	for _, p := range c.pads {
		if first == "" {
			first = p.num
			n0 = strings.TrimSpace(p.net)
		} else if p.num != first {
			return n0, strings.TrimSpace(p.net), true
		}
	}
	return "", "", false
}

// seriesOther returns the far net of a series part entered from `net`: one pad on
// `net`, the other on a different non-global, non-empty net.
func seriesOther(c apComp, net string) (string, bool) {
	n0, n1, ok := twoPadNets(c)
	if !ok {
		return "", false
	}
	pick := func(a, b string) (string, bool) {
		if a == net && b != "" && b != net && !isGlobalNet(b) {
			return b, true
		}
		return "", false
	}
	if o, ok := pick(n0, n1); ok {
		return o, true
	}
	return pick(n1, n0)
}

// discoverRfChains finds, per antenna, the matching chain to a main chip. Parts
// of a chain are claimed (a part serves one antenna). An antenna with no chain
// gets a diag and its would-be parts stay in normal packing.
func discoverRfChains(comps []apComp, sats, mains, ants []int, padsByNet []map[string][]apPad) ([]*rfChain, []apDiag) {
	var chains []*rfChain
	var diags []apDiag
	claimed := map[int]bool{}
	chipOf := func(net string, ax, ay float64) (int, apPad, bool) {
		bestMi, bestD := -1, math.MaxFloat64
		var bestPad apPad
		for mi := range mains {
			if pad, ok := nearestPad(padsByNet[mi][net], ax, ay); ok {
				if d := math.Hypot(pad.x-ax, pad.y-ay); d < bestD {
					bestMi, bestD, bestPad = mi, d, pad
				}
			}
		}
		return bestMi, bestPad, bestMi >= 0
	}
	for _, ai := range ants {
		ant := comps[ai]
		feed, ok := antennaFeedPad(ant)
		if !ok {
			diags = append(diags, apDiag{ant.designator, "rf: antenna has no pad on a signal net — no matching chain to place"})
			continue
		}
		feedNet := strings.TrimSpace(feed.net)
		if _, _, direct := chipOf(feedNet, feed.x, feed.y); direct {
			continue // wired straight to the chip: nothing to chain
		}
		type step struct {
			prev string
			part int
		}
		parent := map[string]step{feedNet: {part: -1}}
		frontier := []string{feedNet}
		goal := ""
		for hop := 0; hop < apRfMaxHops && goal == ""; hop++ {
			var next []string
			for _, net := range frontier {
				for _, s := range sats {
					if claimed[s] {
						continue
					}
					other, ok := seriesOther(comps[s], net)
					if !ok {
						continue
					}
					if _, seen := parent[other]; seen {
						continue
					}
					parent[other] = step{prev: net, part: s}
					next = append(next, other)
					if _, _, hit := chipOf(other, feed.x, feed.y); hit {
						goal = other
						break
					}
				}
				if goal != "" {
					break
				}
			}
			frontier = next
		}
		if goal == "" {
			diags = append(diags, apDiag{ant.designator, fmt.Sprintf("rf: no matching chain from net %s to a main chip within %d hops — parts on it fall back to normal packing", feedNet, apRfMaxHops)})
			continue
		}
		mi, rfPad, _ := chipOf(goal, feed.x, feed.y)
		ch := &rfChain{ant: ai, chip: mi, feed: feed, rf: rfPad}
		for net := goal; net != feedNet; net = parent[net].prev {
			ch.nets = append(ch.nets, net)
			ch.series = append(ch.series, parent[net].part)
		}
		ch.nets = append(ch.nets, feedNet)
		slices.Reverse(ch.nets) // antenna feed net … chip net
		slices.Reverse(ch.series)
		pathIdx := map[string]int{}
		for i, n := range ch.nets {
			pathIdx[n] = i
		}
		for _, s := range ch.series {
			claimed[s] = true
		}
		for _, s := range sats {
			if claimed[s] {
				continue
			}
			n0, n1, ok := twoPadNets(comps[s])
			if !ok {
				continue
			}
			if j, on := pathIdx[n0]; on && n1 != "" && isGlobalNet(n1) {
				ch.shunts = append(ch.shunts, rfShunt{part: s, netIdx: j})
			} else if j, on := pathIdx[n1]; on && n0 != "" && isGlobalNet(n0) {
				ch.shunts = append(ch.shunts, rfShunt{part: s, netIdx: j})
			}
		}
		sort.SliceStable(ch.shunts, func(i, j int) bool {
			a, b := ch.shunts[i], ch.shunts[j]
			if a.netIdx != b.netIdx {
				return a.netIdx < b.netIdx
			}
			return comps[a.part].designator < comps[b.part].designator
		})
		for _, sh := range ch.shunts {
			claimed[sh.part] = true
		}
		chains = append(chains, ch)
	}
	return chains, diags
}

func apPointSegDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 < apEps {
		return math.Hypot(px-ax, py-ay)
	}
	t := math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/l2))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

// rfOrient turns a 2-pin part so the pad on `net` points along (fx,fy), choosing
// from the two rotations that put the pad axis on the wanted axis. With --no-rotate
// (or a part that is not a 2-pin with a pad on the net) the part keeps its rotation.
func (pk *apPacker) rfOrient(sat apComp, net string, fx, fy float64, candidates []float64) (rot, effW, effH float64, oriented bool) {
	if pk.opt.rotate && sat.distinctPins() == 2 {
		if r, w, h, ok := orientFacing(sat, net, fx, fy, candidates); ok {
			return r, w, h, true
		}
	}
	return sat.rotation, sat.width(), sat.height(), false
}

// placeRfChain lays one chain on the straight line between the chip RF pad (P)
// and the antenna feed pad (A): series parts from the antenna end, each shunt
// beside the series part it hangs on. A part with no free slot on the line falls
// back to the generic cost search; every part ends up measured against the line.
func (pk *apPacker) placeRfChain(comps []apComp, mains []int, ch *rfChain, moves []apMove, diags []apDiag) ([]apMove, []apDiag) {
	opt := pk.opt
	chip, ant := comps[mains[ch.chip]], comps[ch.ant]
	ax, ay, px, py := ch.feed.x, ch.feed.y, ch.rf.x, ch.rf.y
	ux, uy := 1.0, 0.0
	if l := math.Hypot(ax-px, ay-py); l > apEps {
		ux, uy = (ax-px)/l, (ay-py)/l
	}
	vx, vy := -uy, ux // perpendicular to the feed line
	horiz := math.Abs(ux) >= math.Abs(uy)
	antZone := apInflate(layoutBBox{MinX: ant.minX, MinY: ant.minY, MaxX: ant.maxX, MaxY: ant.maxY}, apRfAntennaKeepMil)
	skipKO := func(ko layoutBBox) bool { return apOverlaps(ko, antZone) }
	via := "rf:" + ant.designator

	box := func(cx, cy, w, h float64) layoutBBox {
		return layoutBBox{MinX: cx - w/2, MaxX: cx + w/2, MinY: cy - h/2, MaxY: cy + h/2}
	}
	lineDist := func(b layoutBBox) float64 {
		return apPointSegDist((b.MinX+b.MaxX)/2, (b.MinY+b.MaxY)/2, px, py, ax, ay)
	}
	emit := func(sat apComp, rot float64, oriented bool, b layoutBBox, net string) {
		cx, cy := (b.MinX+b.MaxX)/2, (b.MinY+b.MaxY)/2
		bcx, bcy := sat.bboxCenter()
		odx, ody := rotateVec(bcx-sat.x, bcy-sat.y, rot-sat.rotation)
		moves = append(moves, apMove{
			ID: sat.id, Designator: sat.designator,
			NewX: round2(cx - odx), NewY: round2(cy - ody),
			NewRot: rot, SetRot: oriented,
			Main: chip.designator, Edge: "rf-chain", TargetNet: net, Via: via,
		})
		pk.placed = append(pk.placed, apOccupant{box: b, des: strings.TrimSpace(sat.designator), side: sat.side()})
	}
	offLine := func(sat apComp, b layoutBBox, limit float64) {
		if d := lineDist(b); d > limit+apEps {
			diags = append(diags, apDiag{sat.designator, fmt.Sprintf("%s sits %.0fmil off the RF feed line %s→%s (limit %.0fmil) — free the straight path between them",
				sat.designator, math.Ceil(d-apEps), chip.designator, ant.designator, limit)})
		}
	}
	// fallback: the generic cost search with the chip RF pad as the target. The part
	// has left the feed line, which is always reported (the distance is for context).
	fallback := func(si int) {
		sat := comps[si]
		e, along := edgeFor(chip, ch.rf)
		a := &assignment{sat: si, mainIdx: ch.chip, edge: e, along: along, targetNet: "", via: via, pad: ch.rf, hasPad: true}
		a.targetNet = rfFallbackNet(sat, ch)
		before, nMoves := len(pk.placed), len(moves)
		moves, diags = pk.packGroup(comps, chip, e, []*assignment{a}, moves, diags)
		if len(moves) > nMoves {
			moves[len(moves)-1].Edge = "rf-chain"
		}
		if len(pk.placed) > before {
			d := math.Ceil(lineDist(pk.placed[len(pk.placed)-1].box) - apEps)
			if d > apRfLineLimitMil {
				diags = append(diags, apDiag{sat.designator, fmt.Sprintf("%s sits %.0fmil off the RF feed line %s→%s (limit %.0fmil) — free the straight path between them; no free slot on the line, placed by the generic search",
					sat.designator, d, chip.designator, ant.designator, apRfLineLimitMil)})
			} else {
				diags = append(diags, apDiag{sat.designator, fmt.Sprintf("%s left the RF feed line %s→%s (no free slot on it; generic search placed it %.0fmil from it) — free the straight path between them",
					sat.designator, chip.designator, ant.designator, d)})
			}
		}
	}
	axisSeries := []float64{0, 180}
	axisShunt := []float64{90, 270}
	if !horiz {
		axisSeries, axisShunt = axisShunt, axisSeries
	}

	type seriesPos struct {
		ok     bool
		t      float64 // distance of the part's centre from A, towards the chip
		across float64 // its extent across the line
	}
	pos := make([]seriesPos, len(ch.series))
	cursor := apRfStartMil
	for i, si := range ch.series {
		sat := comps[si]
		// The pad on the antenna-side net faces the antenna (+u).
		rot, effW, effH, oriented := pk.rfOrient(sat, ch.nets[i], ux, uy, axisSeries)
		ext, across := effW, effH
		if !horiz {
			ext, across = effH, effW
		}
		var body layoutBBox
		var t float64
		found := false
	search:
		for _, al := range apRfAlongTries {
			for _, off := range apRfPerpTries {
				t = cursor + ext/2 + al
				b := box(ax-ux*t+vx*off, ay-uy*t+vy*off, effW, effH)
				if pk.freeSkip(b, sat.side(), skipKO) {
					body, found = b, true
					cursor += ext + al + apRfPartGapMil
					break search
				}
			}
		}
		if !found {
			fallback(si)
			continue
		}
		pos[i] = seriesPos{ok: true, t: t, across: across}
		emit(sat, rot, oriented, body, ch.nets[i])
		offLine(sat, body, apRfLineLimitMil)
	}

	for _, sh := range ch.shunts {
		sat := comps[sh.part]
		net := ch.nets[sh.netIdx]
		anchor := max(sh.netIdx-1, 0) // the series part that ends on this net (first part for the feed net)
		type sideRot struct {
			rot, w, h float64
			oriented  bool
		}
		var rots [2]sideRot
		sides := [2]float64{1, -1}
		for k, side := range sides {
			// The path-net pad faces the line, i.e. against the side it sits on.
			r, w, h, o := pk.rfOrient(sat, net, -side*vx, -side*vy, axisShunt)
			rots[k] = sideRot{r, w, h, o}
		}
		if !pos[anchor].ok {
			fallback(sh.part)
			continue
		}
		var body layoutBBox
		var chosen sideRot
		limit := apRfLineLimitMil
		found := false
	shuntSearch:
		for _, dl := range apRfShuntTries {
			for k, side := range sides {
				sr := rots[k]
				own := sr.h // extent across the line
				if !horiz {
					own = sr.w
				}
				offMag := pos[anchor].across/2 + opt.pitch + own/2
				t := pos[anchor].t + dl
				b := box(ax-ux*t+vx*side*offMag, ay-uy*t+vy*side*offMag, sr.w, sr.h)
				if pk.freeSkip(b, sat.side(), skipKO) {
					body, chosen, limit, found = b, sr, apRfLineLimitMil+offMag, true
					break shuntSearch
				}
			}
		}
		if !found {
			fallback(sh.part)
			continue
		}
		emit(sat, chosen.rot, chosen.oriented, body, net)
		offLine(sat, body, limit)
	}
	return moves, diags
}

// rfFallbackNet is the net a fallback-placed chain member is oriented by: its pad
// on a path net (the chip-ward one for a series part), else its first pad's net.
func rfFallbackNet(sat apComp, ch *rfChain) string {
	for i := len(ch.nets) - 1; i >= 0; i-- {
		if _, ok := satPadOnNet(sat, ch.nets[i]); ok {
			return ch.nets[i]
		}
	}
	for _, p := range sat.pads {
		if n := strings.TrimSpace(p.net); n != "" {
			return n
		}
	}
	return ""
}

// balanceRailCaps is pass 3b of planAutoPlaceDetailed: for each non-ground rail,
// a main with a pad on it but no power-assigned satellite of that rail takes the
// nearest one (to its rail pad) from a main that holds two or more. Pinned
// satellites (pin(s) >= 0) never move. Deterministic: rails and mains in order.
func balanceRailCaps(comps []apComp, mains []int, mainPadsByNet []map[string][]apPad, assigned map[int]*assignment, pin func(int) int) {
	rails := map[string]bool{}
	for _, a := range assigned {
		if a.via == "power" && !strings.Contains(strings.ToLower(a.targetNet), "gnd") {
			rails[a.targetNet] = true
		}
	}
	names := make([]string, 0, len(rails))
	for r := range rails {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, rail := range names {
		count := make([]int, len(mains))
		var sats []int
		for s, a := range assigned {
			if a.via == "power" && a.targetNet == rail {
				count[a.mainIdx]++
				sats = append(sats, s)
			}
		}
		sort.Ints(sats)
		for mi := range mains {
			pads := mainPadsByNet[mi][rail]
			if len(pads) == 0 || count[mi] > 0 {
				continue
			}
			best, bestDist := -1, math.MaxFloat64
			var bestPad apPad
			for _, s := range sats {
				a := assigned[s]
				if count[a.mainIdx] < 2 || pin(s) >= 0 {
					continue
				}
				scx, scy := comps[s].bboxCenter()
				if pad, ok := nearestPad(pads, scx, scy); ok {
					if d := math.Hypot(pad.x-scx, pad.y-scy); d < bestDist {
						best, bestDist, bestPad = s, d, pad
					}
				}
			}
			if best < 0 {
				continue
			}
			count[assigned[best].mainIdx]--
			count[mi]++
			e, along := edgeFor(comps[mains[mi]], bestPad)
			assigned[best] = &assignment{sat: best, mainIdx: mi, edge: e, along: along, targetNet: rail, via: "power", pad: bestPad, hasPad: true}
		}
	}
}
