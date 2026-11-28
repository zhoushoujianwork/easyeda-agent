package app

// pcb_layout_rf.go — RF placement rules of `pcb layout simulate`, adapted from the
// MIT layout planner of github.com/donghuixin/low-power-iot-hardware-design
// (assets/layout-planner/src/js/rules.js, distances there in mm):
//
//   - antenna zone (RF-01): an antenna, and the pad-free antenna end of an RF
//     module, keeps a 3 mm clearance on every side except the one facing its chip
//     (module: its body). Only the antenna's own RF matching chain may sit there.
//   - inductors stay 8 mm from antennas/RF modules (PI-03) and 4 mm from crystals;
//     crystals stay 4 mm from antennas/RF modules and switching regulators (SI-04);
//     antennas/RF modules stay 5 mm from connectors and batteries (RF-03).
//
// The checks run on the final layout of every simulate result. Spread also uses
// the zones and the module edge rule as hard limits for the main chips.

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

const (
	rfZoneMil          = 118.11 // 3 mm antenna clearance
	rfInductorAntMil   = 314.96 // 8 mm inductor ↔ antenna / RF module
	rfInductorXtalMil  = 157.48 // 4 mm inductor ↔ crystal
	rfXtalNoiseMil     = 157.48 // 4 mm crystal ↔ antenna / RF module / switching regulator
	rfMetalMil         = 196.85 // 5 mm antenna / RF module ↔ connector / battery
	rfPadlessDepth     = 0.15   // a module side is pad-free when no pad centre lies within this share of the depth
	rfModuleEdgeMil    = 20.0   // an RF module's antenna end must lie this close to the board edge
	rfIssueZoneMsg     = "inside the antenna clearance zone of %s"
	rfIssueIndAntMsg   = "inductor %.0fmil from %s (keep ≥ 315mil / 8mm from antennas)"
	rfIssueIndXtalMsg  = "inductor %.0fmil from crystal %s (keep ≥ 157mil / 4mm)"
	rfIssueXtalMsg     = "crystal %.0fmil from %s (keep ≥ 157mil / 4mm from antennas and switching regulators)"
	rfIssueMetalMsg    = "antenna %.0fmil from %s (keep ≥ 197mil / 5mm from connectors and batteries)"
	rfIssueEdgeMessage = "RF module antenna end is not at the board edge"
)

var (
	rfModuleRe   = regexp.MustCompile(`(?i)WROOM|WROVER|MINI-1|-MINI|ESP-?\d\d|MODULE|模组`)
	rfInductorRe = regexp.MustCompile(`(?i)^L\d+$`)
	rfCrystalRe  = regexp.MustCompile(`(?i)^(?:Y|X)\d+$`)
	rfBatteryRe  = regexp.MustCompile(`(?i)^(?:BT|BAT)\d*$`)
)

// rfCtx is the pose-independent RF classification of a board (designators,
// upper-cased and trimmed).
type rfCtx struct {
	antennas  map[string]bool
	antChip   map[string]string    // antenna → chip at the end of its matching chain
	antPads   map[string][2]string // antenna → feed pad num, chip RF pad num
	chain     map[string]bool      // RF matching chain members
	modules   map[string]bool      // main chips named like an RF module (edge rule only with a pad-free end)
	switchers map[string]bool      // mains that own an inductor
}

func rfKey(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func newRfCtx(ap []apComp, pre apPlanInfo) rfCtx {
	r := rfCtx{antennas: map[string]bool{}, antChip: map[string]string{}, antPads: map[string][2]string{}, chain: map[string]bool{},
		modules: map[string]bool{}, switchers: map[string]bool{}}
	for _, c := range ap {
		if isAntennaComp(c) {
			r.antennas[rfKey(c.designator)] = true
		}
	}
	for a, chip := range pre.AntChip {
		r.antChip[rfKey(ap[a].designator)] = rfKey(ap[chip].designator)
		if p, ok := pre.AntPads[a]; ok {
			r.antPads[rfKey(ap[a].designator)] = p
		}
	}
	for m := range pre.ChainAnt {
		r.chain[rfKey(ap[m].designator)] = true
	}
	for _, i := range pre.Mains {
		c := ap[i]
		if rfModuleRe.MatchString(c.device) || rfModuleRe.MatchString(c.name) {
			r.modules[rfKey(c.designator)] = true
		}
	}
	for s, mi := range pre.Owner {
		if rfInductorRe.MatchString(strings.TrimSpace(ap[s].designator)) {
			r.switchers[rfKey(ap[mi].designator)] = true
		}
	}
	return r
}

func (r rfCtx) empty() bool {
	return len(r.antennas) == 0 && len(r.modules) == 0 && len(r.switchers) == 0
}

// rfPadlessSide returns the side of b (0 left/minX, 1 right/maxX, 2 bottom/minY,
// 3 top/maxY) that has no pad centre within rfPadlessDepth of the depth while the
// opposite side has one, and the depth of the pad-free strip (edge to the nearest
// pad extent). Exactly one side must qualify; otherwise ok is false.
func rfPadlessSide(b layoutBBox, pads [][4]float64) (side int, depth float64, ok bool) {
	if len(pads) == 0 || b.MaxX <= b.MinX || b.MaxY <= b.MinY {
		return 0, 0, false
	}
	w, h := b.MaxX-b.MinX, b.MaxY-b.MinY
	near := func(s int, p [4]float64) bool {
		switch s {
		case 0:
			return p[0]-b.MinX < rfPadlessDepth*w
		case 1:
			return b.MaxX-p[0] < rfPadlessDepth*w
		case 2:
			return p[1]-b.MinY < rfPadlessDepth*h
		}
		return b.MaxY-p[1] < rfPadlessDepth*h
	}
	found := -1
	for s := 0; s < 4; s++ {
		opp := s ^ 1
		has, hasOpp := false, false
		for _, p := range pads {
			has = has || near(s, p)
			hasOpp = hasOpp || near(opp, p)
		}
		if !has && hasOpp {
			if found >= 0 {
				return 0, 0, false
			}
			found = s
		}
	}
	if found < 0 {
		return 0, 0, false
	}
	depth = math.Inf(1)
	for _, p := range pads {
		var d float64
		switch found {
		case 0:
			d = p[0] - p[2]/2 - b.MinX
		case 1:
			d = b.MaxX - (p[0] + p[2]/2)
		case 2:
			d = p[1] - p[3]/2 - b.MinY
		default:
			d = b.MaxY - (p[1] + p[3]/2)
		}
		depth = math.Min(depth, d)
	}
	return found, math.Max(depth, 0), true
}

// rfZone grows box by rfZoneMil on every side except open (0..3 as rfPadlessSide;
// -1 = none open).
func rfZone(box layoutBBox, open int) layoutBBox {
	z := apInflate(box, rfZoneMil)
	switch open {
	case 0:
		z.MinX = box.MinX
	case 1:
		z.MaxX = box.MaxX
	case 2:
		z.MinY = box.MinY
	case 3:
		z.MaxY = box.MaxY
	}
	return z
}

// rfModuleZone is the antenna strip of a module (its pad-free end) and the zone
// around it, open towards the module body. ok is false without a pad-free end.
func rfModuleZone(b layoutBBox, pads [][4]float64) (strip, zone layoutBBox, side int, ok bool) {
	side, depth, ok := rfPadlessSide(b, pads)
	if !ok {
		return layoutBBox{}, layoutBBox{}, 0, false
	}
	strip = b
	switch side {
	case 0:
		strip.MaxX = b.MinX + depth
	case 1:
		strip.MinX = b.MaxX - depth
	case 2:
		strip.MaxY = b.MinY + depth
	default:
		strip.MinY = b.MaxY - depth
	}
	return strip, rfZone(strip, side^1), side, true
}

// rfAntennaZone is an antenna's zone, open on the side facing (cx, cy): its chip,
// or the board centre when it has none.
func rfAntennaZone(b layoutBBox, cx, cy float64) layoutBBox {
	ax, ay := (b.MinX+b.MaxX)/2, (b.MinY+b.MaxY)/2
	dx, dy := cx-ax, cy-ay
	open := -1
	switch {
	case dx == 0 && dy == 0:
	case math.Abs(dx) >= math.Abs(dy) && dx < 0:
		open = 0
	case math.Abs(dx) >= math.Abs(dy):
		open = 1
	case dy < 0:
		open = 2
	default:
		open = 3
	}
	return rfZone(b, open)
}

// rfModuleAtEdge reports whether the module's antenna end (side) lies on the board
// box edge, facing out.
func rfModuleAtEdge(b layoutBBox, side int, board layoutBBox) bool {
	switch side {
	case 0:
		return b.MinX-board.MinX <= rfModuleEdgeMil
	case 1:
		return board.MaxX-b.MaxX <= rfModuleEdgeMil
	case 2:
		return b.MinY-board.MinY <= rfModuleEdgeMil
	}
	return board.MaxY-b.MaxY <= rfModuleEdgeMil
}

func rfGap(a, b layoutBBox) float64 {
	dx := math.Max(0, math.Max(a.MinX-b.MaxX, b.MinX-a.MaxX))
	dy := math.Max(0, math.Max(a.MinY-b.MaxY, b.MinY-a.MaxY))
	return math.Hypot(dx, dy)
}

func rfPadsOf(c lsComponent) [][4]float64 {
	out := make([][4]float64, 0, len(c.Pads))
	for _, p := range c.Pads {
		out = append(out, [4]float64{p.X, p.Y, p.W, p.H})
	}
	return out
}

func apPadsRect(c apComp) [][4]float64 {
	out := make([][4]float64, 0, len(c.pads))
	for _, p := range c.pads {
		out = append(out, [4]float64{p.x, p.y, p.w, p.h})
	}
	return out
}

// rfZones returns the antenna zones of a laid-out board, keyed by the antenna /
// module designator.
func (r rfCtx) zones(comps []lsComponent) map[string]layoutBBox {
	out := map[string]layoutBBox{}
	if r.empty() {
		return out
	}
	byDes := map[string]lsComponent{}
	for _, c := range comps {
		byDes[rfKey(c.Designator)] = c
	}
	var board *layoutBBox
	for _, c := range comps {
		k := rfKey(c.Designator)
		if c.BBox == nil {
			continue
		}
		switch {
		case r.antennas[k]:
			cx, cy := math.NaN(), math.NaN()
			if chip, ok := byDes[r.antChip[k]]; ok && chip.BBox != nil {
				cx, cy = (chip.BBox.MinX+chip.BBox.MaxX)/2, (chip.BBox.MinY+chip.BBox.MaxY)/2
			}
			if math.IsNaN(cx) {
				if board == nil {
					board = rfBoardOf(comps)
				}
				cx, cy = (board.MinX+board.MaxX)/2, (board.MinY+board.MaxY)/2
			}
			out[k] = rfAntennaZone(*c.BBox, cx, cy)
		case r.modules[k]:
			if _, zone, _, ok := rfModuleZone(*c.BBox, rfPadsOf(c)); ok {
				out[k] = zone
			}
		}
	}
	return out
}

func rfBoardOf(comps []lsComponent) *layoutBBox {
	b := layoutBBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	for _, c := range comps {
		if c.BBox != nil {
			b.MinX, b.MinY = math.Min(b.MinX, c.BBox.MinX), math.Min(b.MinY, c.BBox.MinY)
			b.MaxX, b.MaxY = math.Max(b.MaxX, c.BBox.MaxX), math.Max(b.MaxY, c.BBox.MaxY)
		}
	}
	return &b
}

// issues checks the RF rules on a laid-out board (components in designator
// order). board is the outline box used for the module edge rule (nil: skipped).
func (r rfCtx) issues(comps []lsComponent, board *layoutBBox) []lsIssue {
	if r.empty() {
		return nil
	}
	var out []lsIssue
	zones := r.zones(comps)
	var zoneKeys []string
	for k := range zones {
		zoneKeys = append(zoneKeys, k)
	}
	sortDes(zoneKeys)
	isAnt := func(k string) bool {
		if r.antennas[k] {
			return true
		}
		_, z := zones[k]
		return r.modules[k] && z
	}
	var ants, xtals, switchers, metal []lsComponent
	for _, c := range comps {
		k := rfKey(c.Designator)
		if c.BBox == nil {
			continue
		}
		if isAnt(k) {
			ants = append(ants, c)
		}
		if rfCrystalRe.MatchString(k) && !r.chain[k] {
			xtals = append(xtals, c)
		}
		if r.switchers[k] {
			switchers = append(switchers, c)
		}
		if connectorDesRe.MatchString(k) || rfBatteryRe.MatchString(k) {
			metal = append(metal, c)
		}
	}
	for _, c := range comps {
		k := rfKey(c.Designator)
		if c.BBox == nil {
			continue
		}
		b := *c.BBox
		if !r.chain[k] {
			for _, zk := range zoneKeys {
				if zk == k || r.antChip[zk] == k && r.antennas[zk] {
					continue // the antenna itself; its chip may touch the open side
				}
				if apOverlapArea(b, zones[zk]) > apEps {
					out = append(out, lsIssue{Severity: "P1", Designator: c.Designator, Message: fmt.Sprintf(rfIssueZoneMsg, zk),
						Fix: "move it out of the antenna clearance (3 mm on three sides, only the matching parts inside)"})
				}
			}
		}
		if rfInductorRe.MatchString(k) && !r.chain[k] {
			for _, a := range ants {
				if g := rfGap(b, *a.BBox); g < rfInductorAntMil {
					out = append(out, lsIssue{Severity: "P1", Designator: c.Designator, Message: fmt.Sprintf(rfIssueIndAntMsg, g, a.Designator),
						Fix: "move the inductor or its regulator away from the antenna"})
				}
			}
			for _, x := range xtals {
				if g := rfGap(b, *x.BBox); g < rfInductorXtalMil {
					out = append(out, lsIssue{Severity: "P2", Designator: c.Designator, Message: fmt.Sprintf(rfIssueIndXtalMsg, g, x.Designator),
						Fix: "keep inductors away from crystals"})
				}
			}
		}
		if rfCrystalRe.MatchString(k) && !r.chain[k] {
			for _, a := range append(append([]lsComponent{}, ants...), switchers...) {
				if g := rfGap(b, *a.BBox); g < rfXtalNoiseMil {
					out = append(out, lsIssue{Severity: "P2", Designator: c.Designator, Message: fmt.Sprintf(rfIssueXtalMsg, g, a.Designator),
						Fix: "keep the crystal away from antennas and switching regulators"})
				}
			}
		}
		if isAnt(k) {
			for _, m := range metal {
				if g := rfGap(b, *m.BBox); g < rfMetalMil {
					out = append(out, lsIssue{Severity: "P2", Designator: c.Designator, Message: fmt.Sprintf(rfIssueMetalMsg, g, m.Designator),
						Fix: "keep connectors and batteries away from the antenna"})
				}
			}
		}
		if r.modules[k] && board != nil {
			if _, _, side, ok := rfModuleZone(b, rfPadsOf(c)); ok && !rfModuleAtEdge(b, side, *board) {
				out = append(out, lsIssue{Severity: "P1", Designator: c.Designator, Message: rfIssueEdgeMessage,
					Fix: "put the module at the board edge with its antenna end facing out"})
			}
		}
	}
	return out
}

// chainMil is the straight length chip RF pad → antenna feed pad, summed over the
// antennas with a chain (0 without antennas).
func (r rfCtx) chainMil(comps []lsComponent) float64 {
	if len(r.antChip) == 0 {
		return 0
	}
	byDes := map[string]lsComponent{}
	for _, c := range comps {
		byDes[rfKey(c.Designator)] = c
	}
	var ants []string
	for a := range r.antChip {
		ants = append(ants, a)
	}
	sortDes(ants)
	total := 0.0
	for _, a := range ants {
		ant, ok1 := byDes[a]
		chip, ok2 := byDes[r.antChip[a]]
		if !ok1 || !ok2 {
			continue
		}
		pads := r.antPads[a]
		ax, ay, okA := rfPadAt(ant, pads[0])
		cx, cy, okC := rfPadAt(chip, pads[1])
		if !okA || !okC {
			continue
		}
		total += math.Hypot(ax-cx, ay-cy)
	}
	return total
}

// rfPadAt is the pad num's centre, or the bbox centre when it is not found.
func rfPadAt(c lsComponent, num string) (float64, float64, bool) {
	for _, p := range c.Pads {
		if num != "" && p.Num == num {
			return p.X, p.Y, true
		}
	}
	if c.BBox != nil {
		return (c.BBox.MinX + c.BBox.MaxX) / 2, (c.BBox.MinY + c.BBox.MaxY) / 2, true
	}
	return 0, 0, false
}

func sortDes(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && simDesLess(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
