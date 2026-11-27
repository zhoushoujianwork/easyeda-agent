package app

// pcb_layout_spread.go — `pcb layout simulate --spread`: whole-board spread of
// the main chips.
//
// Spread only computes (x, y) poses for the spreadable mains. The caller writes
// them into the posed board exactly like user poses, so everything after it (the
// planner, --keep-unmoved, ripup) is the unchanged posed pipeline and a later run
// that is given these poses without --spread yields the same layout.
//
// A spreadable main is a planner main (after --anchor / --exclude-main) that is
// not locked, has no locked routing, is not a virtual-group member and has a
// bbox. Every other part with a bbox and every keep-out is an obstacle, except the
// satellites owned by a spreadable main: those are re-planned around it. Each main gets a footprint: its bbox grown by
// a ring that leaves room for the satellites it owns. Footprints are shelf-packed
// row by row (strongest connection first), improved by pairwise swaps, then the
// whole set is shifted toward the board centre as far as it stays legal.
//
// The board is the outline's bbox and, when the dump carries the outline points,
// its polygon (checked on the footprint's corners, centre and the polygon's
// vertices, see simplePolygonContainsBBox). Everything is deterministic: ties go
// to the lower designator, nothing iterates an unsorted map.

import (
	"math"
	"sort"
	"strings"
)

const (
	spreadSatAreaFactor = 2.5  // ring area reserved per unit of satellite bbox area
	spreadSwapPasses    = 8    // pairwise swap passes over the order
	spreadBisectSteps   = 30   // centring bisection steps
	spreadPolyStepMil   = 10.0 // x step when the board polygon rejects a slot
	spreadRowStepMil    = 25.0 // largest y step over a row nothing fits in
	spreadMinAdvanceMil = 0.01 // smallest x advance past a blocker
	spreadMaxRows       = 4000 // guard against a pathological scan
	spreadWeightEps     = 1e-9 // ties in connection weight
	spreadCostEps       = 1e-9 // a swap must drop the cost by more than this
	spreadNoFitMessage  = "spread: the main chips do not fit on the board; left in place"
	spreadNoMainMessage = "spread: no main chip to spread"
)

type spreadMain struct {
	idx        int
	des        string
	w, h       float64 // posed bbox size
	offX, offY float64 // anchor minus bbox centre
	sat        float64 // total bbox area of the satellites it owns
	satSide    float64 // largest satellite bbox side
	side       int
}

type spreadBlock struct {
	box  layoutBBox
	side int // 0 = every side (keep-outs)
}

type spreadPlan struct {
	board    layoutBBox
	poly     [][2]float64
	blockers []spreadBlock
	obst     [][2]float64 // obstacle centres, aligned with the columns of wMO
	mains    []spreadMain
	W, H     []float64 // footprint size
	wMM      [][]float64
	wMO      [][]float64
	obstIdx  []int // posed index of each obstacle column
	gap      float64
}

// simSpreadMains returns the anchor pose (x, y) of every spreadable main, keyed by
// its index in posed, or nil with an issue when it moves nothing. posed already
// carries the user poses; a posed main only contributes its rotation (its bbox).
func simSpreadMains(posed []apComp, opt apOptions, pre apPlanInfo, movable map[int]bool, lockedRoute []bool, poly [][2]float64) (map[int][2]float64, []lsIssue) {
	p, extra := spreadSetup(posed, opt, pre, movable, lockedRoute, poly)
	if p == nil {
		return nil, extra
	}
	pos, _, ok := p.packSeed()
	if !ok {
		return nil, spreadNoFit()
	}
	out := make(map[int][2]float64, len(p.mains))
	for k, m := range p.mains {
		out[m.idx] = pos[k]
	}
	return out, nil
}

func spreadNoFit() []lsIssue {
	return []lsIssue{{Severity: "P1", Message: spreadNoFitMessage, Fix: "enlarge the board, move connectors/antennas or keep-outs away, or demote a chip with --exclude-main"}}
}

// spreadSetup collects the spreadable mains, the packing area, the blockers and
// the connection weights. A nil plan comes with the issue that explains it.
func spreadSetup(posed []apComp, opt apOptions, pre apPlanInfo, movable map[int]bool, lockedRoute []bool, poly [][2]float64) (*spreadPlan, []lsIssue) {
	noFit := spreadNoFit()
	var mains []spreadMain
	isMain := map[int]int{}
	for _, i := range pre.Mains {
		c := posed[i]
		if !c.hasBBox || c.locked || (i < len(lockedRoute) && lockedRoute[i]) || opt.fixed[strings.ToUpper(strings.TrimSpace(c.designator))] {
			continue
		}
		if r := simNormDeg(c.rotation); math.Abs(r-math.Round(r/90)*90) > 1e-6 {
			continue // a pose must be a multiple of 90°: an odd angle stays where it is
		}
		cx, cy := c.bboxCenter()
		mains = append(mains, spreadMain{idx: i, des: strings.TrimSpace(c.designator), w: c.width(), h: c.height(), offX: c.x - cx, offY: c.y - cy, side: c.side()})
	}
	if len(mains) == 0 {
		return nil, []lsIssue{{Severity: "P3", Message: spreadNoMainMessage, Fix: "pin a chip with --anchor, or unlock the chip to spread"}}
	}
	sort.SliceStable(mains, func(a, b int) bool {
		if mains[a].des != mains[b].des {
			return simDesLess(mains[a].des, mains[b].des)
		}
		return mains[a].idx < mains[b].idx
	})
	for k, m := range mains {
		isMain[m.idx] = k
	}
	if opt.board == nil {
		return nil, []lsIssue{{Severity: "P1", Message: "spread: the dump has no usable board outline; the main chips are left in place", Fix: "take a fresh `easyeda pcb dump --include-copper` with the board outline set"}}
	}

	// Satellites each main owns: they size its footprint ring.
	for _, s := range pre.Movable {
		k, ok := isMain[pre.Owner[s]]
		if _, owned := pre.Owner[s]; !owned || !ok || !posed[s].hasBBox {
			continue
		}
		w, h := posed[s].width(), posed[s].height()
		mains[k].sat += w * h
		mains[k].satSide = math.Max(mains[k].satSide, math.Max(w, h))
	}

	// The packing area is the outline bbox, narrowed to the polygon's own bbox
	// (a dump's outline bbox includes the stroke width of the board line).
	board := *opt.board
	if len(poly) >= 3 {
		pb := layoutBBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
		for _, v := range poly {
			pb.MinX, pb.MaxX = math.Min(pb.MinX, v[0]), math.Max(pb.MaxX, v[0])
			pb.MinY, pb.MaxY = math.Min(pb.MinY, v[1]), math.Max(pb.MaxY, v[1])
		}
		board = layoutBBox{MinX: math.Max(board.MinX, pb.MinX), MinY: math.Max(board.MinY, pb.MinY), MaxX: math.Min(board.MaxX, pb.MaxX), MaxY: math.Min(board.MaxY, pb.MaxY)}
	}
	if board.MaxX <= board.MinX || board.MaxY <= board.MinY {
		return nil, noFit
	}
	p := &spreadPlan{board: board, poly: poly, mains: mains, gap: opt.gap}
	for _, ko := range opt.keepouts {
		p.blockers = append(p.blockers, spreadBlock{box: ko})
	}
	var obstIdx []int
	for i, c := range posed {
		if _, spread := isMain[i]; spread || !c.hasBBox {
			continue
		}
		if _, owned := pre.Owner[i]; movable[i] && owned {
			if _, follows := isMain[pre.Owner[i]]; follows {
				continue // re-planned around its spread chip
			}
		}
		obstIdx = append(obstIdx, i)
		box := layoutBBox{MinX: c.minX, MinY: c.minY, MaxX: c.maxX, MaxY: c.maxY}
		p.blockers = append(p.blockers, spreadBlock{box: apInflate(box, opt.gap), side: c.side()})
		cx, cy := c.bboxCenter()
		p.obst = append(p.obst, [2]float64{cx, cy})
	}
	// Obstacle columns in designator order: every sum over them is then independent
	// of the dump order.
	sort.SliceStable(obstIdx, func(a, b int) bool {
		da, db := strings.TrimSpace(posed[obstIdx[a]].designator), strings.TrimSpace(posed[obstIdx[b]].designator)
		if da != db {
			return simDesLess(da, db)
		}
		return posed[obstIdx[a]].id < posed[obstIdx[b]].id
	})
	p.obst = p.obst[:0]
	for _, i := range obstIdx {
		cx, cy := posed[i].bboxCenter()
		p.obst = append(p.obst, [2]float64{cx, cy})
	}
	p.wMM, p.wMO = spreadWeights(posed, mains, isMain, obstIdx)
	p.obstIdx = obstIdx
	return p, nil
}

// packSeed is the v1 layout: footprints with the full satellite ring, or the
// plain bbox + gap when those do not fit, shelf-packed. It returns the anchor
// (x, y) of each main (rotation unchanged) and the ring used.
func (p *spreadPlan) packSeed() ([][2]float64, []float64, bool) {
	for _, full := range []bool{true, false} {
		ring := p.rings(full)
		if pos, ok := p.solve(); ok {
			out := make([][2]float64, len(p.mains))
			for k, m := range p.mains {
				out[k] = [2]float64{round4(pos[k][0] + p.W[k]/2 + m.offX), round4(pos[k][1] + p.H[k]/2 + m.offY)}
			}
			return out, ring, true
		}
	}
	return nil, p.rings(false), false
}

// rings sets the footprint sizes p.W × p.H for the full satellite ring (or the
// plain gap) and returns the ring of each main.
func (p *spreadPlan) rings(full bool) []float64 {
	ring := make([]float64, len(p.mains))
	p.W, p.H = make([]float64, len(p.mains)), make([]float64, len(p.mains))
	for k, m := range p.mains {
		r := p.gap // fallback: plain bbox + 2*gap between neighbours
		if full {
			r = spreadRing(m.w, m.h, m.sat, m.satSide, p.gap)
		}
		ring[k] = r
		p.W[k], p.H[k] = m.w+2*r, m.h+2*r
	}
	return ring
}

// spreadRing is the satellite ring r around a main of w×h: the larger of the
// largest satellite side plus gap, and the r that gives the ring
// (w+2r)(h+2r) − w·h the area factor × the satellites' total bbox area.
func spreadRing(w, h, satArea, satSide, gap float64) float64 {
	r := satSide + gap
	if satArea > 0 {
		s := w + h
		r = math.Max(r, (-s+math.Sqrt(s*s+4*spreadSatAreaFactor*satArea))/4)
	}
	return math.Max(r, gap)
}

// spreadWeights returns the connection weights between mains and from each main to
// each obstacle: Σ over nets they share of 1/(k−1), k = the number of parts on the
// net, so a rail shared by dozens of parts counts little.
func spreadWeights(posed []apComp, mains []spreadMain, isMain map[int]int, obstIdx []int) ([][]float64, [][]float64) {
	byNet := map[string][]int{}
	for i, c := range posed {
		seen := map[string]bool{}
		for _, pad := range c.pads {
			net := strings.TrimSpace(pad.net)
			if net == "" || seen[net] {
				continue
			}
			seen[net] = true
			byNet[net] = append(byNet[net], i)
		}
	}
	nets := make([]string, 0, len(byNet))
	for n := range byNet {
		nets = append(nets, n)
	}
	sort.Strings(nets)
	obst := make(map[int]int, len(obstIdx))
	for o, i := range obstIdx {
		obst[i] = o
	}
	wMM := make([][]float64, len(mains))
	wMO := make([][]float64, len(mains))
	for k := range mains {
		wMM[k] = make([]float64, len(mains))
		wMO[k] = make([]float64, len(obstIdx))
	}
	for _, net := range nets {
		parts := byNet[net]
		if len(parts) < 2 {
			continue
		}
		w := 1 / float64(len(parts)-1)
		var ms, os []int
		for _, i := range parts {
			if k, ok := isMain[i]; ok {
				ms = append(ms, k)
			}
			if o, ok := obst[i]; ok {
				os = append(os, o)
			}
		}
		for a, ka := range ms {
			for _, kb := range ms[a+1:] {
				wMM[ka][kb] += w
				wMM[kb][ka] += w
			}
			for _, o := range os {
				wMO[ka][o] += w
			}
		}
	}
	return wMM, wMO
}

// solve orders, packs, improves and centres the footprints of p.W × p.H.
func (p *spreadPlan) solve() ([][2]float64, bool) {
	n := len(p.mains)
	order := p.greedyOrder()
	if _, ok := p.pack(order); !ok {
		// The connection order does not fit; a tall-first order often does.
		order = p.heightOrder()
		if _, ok := p.pack(order); !ok {
			return nil, false
		}
	}
	final := func(o []int) ([][2]float64, bool) {
		pos, ok := p.pack(o)
		if !ok {
			return nil, false
		}
		return p.centre(pos), true
	}
	best, _ := final(order)
	bestCost := p.cost(best)
	for pass := 0; pass < spreadSwapPasses; pass++ {
		improved := false
		for a := 0; a < n-1; a++ {
			for b := a + 1; b < n; b++ {
				order[a], order[b] = order[b], order[a]
				if pos, ok := final(order); ok {
					if c := p.cost(pos); c < bestCost-spreadCostEps {
						best, bestCost, improved = pos, c, true
						continue
					}
				}
				order[a], order[b] = order[b], order[a]
			}
		}
		if !improved {
			break
		}
	}
	if !p.valid(best) {
		return nil, false
	}
	return best, true
}

func (p *spreadPlan) total(k int) float64 {
	t := 0.0
	for _, w := range p.wMM[k] {
		t += w
	}
	for _, w := range p.wMO[k] {
		t += w
	}
	return t
}

// greedyOrder starts with the most connected main (ties: larger footprint, lower
// designator) and keeps appending the one most connected to those already
// ordered (ties: more connected overall, larger footprint, lower designator).
func (p *spreadPlan) greedyOrder() []int {
	n := len(p.mains)
	area := func(k int) float64 { return p.W[k] * p.H[k] }
	placed := make([]bool, n)
	order := make([]int, 0, n)
	for len(order) < n {
		pick := -1
		var pickConn, pickTotal float64
		for k := 0; k < n; k++ {
			if placed[k] {
				continue
			}
			conn := 0.0
			for _, j := range order {
				conn += p.wMM[k][j]
			}
			tot := p.total(k)
			better := pick < 0
			if !better {
				switch {
				case math.Abs(conn-pickConn) > spreadWeightEps:
					better = conn > pickConn
				case math.Abs(tot-pickTotal) > spreadWeightEps:
					better = tot > pickTotal
				case area(k) != area(pick):
					better = area(k) > area(pick)
				}
			}
			if better {
				pick, pickConn, pickTotal = k, conn, tot
			}
		}
		placed[pick] = true
		order = append(order, pick)
	}
	return order
}

// heightOrder is the classic tall-first shelf order (ties: larger area, lower designator).
func (p *spreadPlan) heightOrder() []int {
	order := make([]int, len(p.mains))
	for k := range order {
		order[k] = k
	}
	sort.SliceStable(order, func(a, b int) bool {
		i, j := order[a], order[b]
		if p.H[i] != p.H[j] {
			return p.H[i] > p.H[j]
		}
		return p.W[i]*p.H[i] > p.W[j]*p.H[j]
	})
	return order
}

// cost is Σ weight · centre distance over main pairs and main–obstacle pairs.
func (p *spreadPlan) cost(pos [][2]float64) float64 {
	c := 0.0
	cen := func(k int) (float64, float64) { return pos[k][0] + p.W[k]/2, pos[k][1] + p.H[k]/2 }
	for a := range p.mains {
		ax, ay := cen(a)
		for b := a + 1; b < len(p.mains); b++ {
			if w := p.wMM[a][b]; w > 0 {
				bx, by := cen(b)
				c += w * math.Hypot(ax-bx, ay-by)
			}
		}
		for o, w := range p.wMO[a] {
			if w > 0 {
				c += w * math.Hypot(ax-p.obst[o][0], ay-p.obst[o][1])
			}
		}
	}
	return c
}

// blockedUntil returns the right-most MaxX among the blockers that overlap b.
func (p *spreadPlan) blockedUntil(b layoutBBox, side int) (float64, bool) {
	until, hit := 0.0, false
	for _, bl := range p.blockers {
		if sameSide(side, bl.side) && apOverlaps(b, bl.box) {
			if !hit || bl.box.MaxX > until {
				until = bl.box.MaxX
			}
			hit = true
		}
	}
	return until, hit
}

func spreadPolyOK(poly [][2]float64, b layoutBBox) bool {
	return len(poly) < 3 || simplePolygonContainsBBox(poly, b)
}

// fits reports whether the footprint box is inside the board and clear of every blocker.
func (p *spreadPlan) fits(b layoutBBox, side int) bool {
	if b.MinX < p.board.MinX-apEps || b.MinY < p.board.MinY-apEps || b.MaxX > p.board.MaxX+apEps || b.MaxY > p.board.MaxY+apEps {
		return false
	}
	if _, hit := p.blockedUntil(b, side); hit {
		return false
	}
	return spreadPolyOK(p.poly, b)
}

func (p *spreadPlan) box(k int, pos [][2]float64) layoutBBox {
	return layoutBBox{MinX: pos[k][0], MinY: pos[k][1], MaxX: pos[k][0] + p.W[k], MaxY: pos[k][1] + p.H[k]}
}

// valid re-checks a result: every footprint fits and no two overlap.
func (p *spreadPlan) valid(pos [][2]float64) bool {
	for a := range p.mains {
		if !p.fits(p.box(a, pos), p.mains[a].side) {
			return false
		}
		for b := a + 1; b < len(p.mains); b++ {
			if apOverlaps(p.box(a, pos), p.box(b, pos)) {
				return false
			}
		}
	}
	return true
}

// pack shelf-packs the footprints in order, rows from the board's lower-left
// corner. A slot that collides with a blocker is skipped to the right of it; a
// row that cannot take the next footprint closes (its height is its tallest
// footprint) and the next row starts above.
func (p *spreadPlan) pack(order []int) ([][2]float64, bool) {
	pos := make([][2]float64, len(p.mains))
	rowY, x, rowH, items := p.board.MinY, p.board.MinX, 0.0, 0
	for _, k := range order {
		w, h, side := p.W[k], p.H[k], p.mains[k].side
		placed := false
		for rows := 0; !placed && rows < spreadMaxRows; rows++ {
			if rowY+h > p.board.MaxY+apEps {
				return nil, false
			}
			for x+w <= p.board.MaxX+apEps {
				b := layoutBBox{MinX: x, MinY: rowY, MaxX: x + w, MaxY: rowY + h}
				if until, hit := p.blockedUntil(b, side); hit {
					x = math.Max(until, x+spreadMinAdvanceMil)
					continue
				}
				if !spreadPolyOK(p.poly, b) {
					x += spreadPolyStepMil
					continue
				}
				pos[k] = [2]float64{x, rowY}
				x += w
				rowH = math.Max(rowH, h)
				items++
				placed = true
				break
			}
			if placed {
				break
			}
			if items > 0 {
				rowY += rowH
			} else {
				rowY = p.nextRowY(rowY)
			}
			x, rowH, items = p.board.MinX, 0, 0
		}
		if !placed {
			return nil, false
		}
	}
	return pos, true
}

// nextRowY lifts an empty row to the nearest blocker top above it (at most
// spreadRowStepMil), so a row no footprint fits in does not stall the scan.
func (p *spreadPlan) nextRowY(y float64) float64 {
	next := y + spreadRowStepMil
	for _, bl := range p.blockers {
		if bl.box.MaxY > y+1 && bl.box.MaxY < next {
			next = bl.box.MaxY
		}
	}
	return next
}

// centre shifts the whole packed set toward the board centre: the full centring
// offset when that stays legal, otherwise the largest fraction found by bisection.
func (p *spreadPlan) centre(pos [][2]float64) [][2]float64 {
	if len(pos) == 0 {
		return pos
	}
	u := p.box(0, pos)
	for k := 1; k < len(pos); k++ {
		u = simUnion(u, p.box(k, pos))
	}
	dx := (p.board.MinX+p.board.MaxX)/2 - (u.MinX+u.MaxX)/2
	dy := (p.board.MinY+p.board.MaxY)/2 - (u.MinY+u.MaxY)/2
	shifted := func(t float64) ([][2]float64, bool) {
		out := make([][2]float64, len(pos))
		for k := range pos {
			out[k] = [2]float64{pos[k][0] + t*dx, pos[k][1] + t*dy}
			if !p.fits(p.box(k, out), p.mains[k].side) {
				return nil, false
			}
		}
		return out, true
	}
	if out, ok := shifted(1); ok {
		return out
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < spreadBisectSteps; i++ {
		mid := (lo + hi) / 2
		if _, ok := shifted(mid); ok {
			lo = mid
		} else {
			hi = mid
		}
	}
	if out, ok := shifted(lo); ok {
		return out
	}
	return pos
}
