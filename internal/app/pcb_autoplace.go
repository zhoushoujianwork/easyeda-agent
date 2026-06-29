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

// apPad is one component pad: its net (by name) and rendered center.
type apPad struct {
	num string
	net string
	x   float64
	y   float64
}

// apComp is a placed component with the geometry the planner reasons over.
type apComp struct {
	id         string
	designator string
	x, y       float64 // component anchor (what modify sets)
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
	ID          string  `json:"id"`
	Designator  string  `json:"designator"`
	NewX        float64 `json:"newX"`
	NewY        float64 `json:"newY"`
	Main        string  `json:"main"`
	Edge        string  `json:"edge"`
	TargetNet   string  `json:"targetNet"`
	Via         string  `json:"via,omitempty"` // "local" | "power" | "chain:<designator>"
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
}

func defaultApOptions() apOptions { return apOptions{mainPins: 8, gap: 40, pitch: 30} }

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
	for i, c := range comps {
		if !c.hasBBox {
			continue
		}
		if c.distinctPins() >= opt.mainPins {
			mains = append(mains, i)
		} else if !c.locked {
			sats = append(sats, i)
		}
	}

	var diags []apDiag
	if len(mains) == 0 {
		for _, s := range sats {
			diags = append(diags, apDiag{comps[s].designator, "no main chip on board (>= mainPins distinct pins) to anchor against"})
		}
		return nil, diags
	}

	// Per-main net→pad index: first pad seen on each net (chips repeat GND pads).
	mainPadByNet := make([]map[string]apPad, len(mains))
	for mi, m := range mains {
		mainPadByNet[mi] = map[string]apPad{}
		for _, p := range comps[m].pads {
			n := strings.TrimSpace(p.net)
			if n == "" {
				continue
			}
			if _, ok := mainPadByNet[mi][n]; !ok {
				mainPadByNet[mi][n] = p
			}
		}
	}

	assigned := make(map[int]*assignment) // sat index → decision

	// Pass 1 — direct: a satellite sharing a LOCAL (signal) net with a main pad
	// hugs that pad. Prefer the nearest main when several expose the same net.
	for _, s := range sats {
		best := -1
		var bestPad apPad
		var bestNet string
		bestDist := math.MaxFloat64
		for _, ln := range comps[s].localNets() {
			for mi := range mains {
				if pad, ok := mainPadByNet[mi][ln]; ok {
					scx, scy := comps[s].bboxCenter()
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
		best := -1
		var bestPad apPad
		var bestNet string
		bestDist := math.MaxFloat64
		for _, pn := range pnets {
			for mi := range mains {
				if pad, ok := mainPadByNet[mi][pn]; ok {
					scx, scy := comps[s].bboxCenter()
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

	var moves []apMove
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
			var alongExtent, perpCenter float64
			if k.edge.vertical() {
				alongExtent = sat.height()
				half := sat.width() / 2
				if k.edge == edgeLeft {
					perpCenter = chip.minX - opt.gap - half
				} else {
					perpCenter = chip.maxX + opt.gap + half
				}
			} else {
				alongExtent = sat.width()
				half := sat.height() / 2
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
			bcx, bcy := sat.bboxCenter()
			moves = append(moves, apMove{
				ID:         sat.id,
				Designator: sat.designator,
				NewX:       round2(cx - bcx + sat.x),
				NewY:       round2(cy - bcy + sat.y),
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
