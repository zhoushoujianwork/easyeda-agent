package app

// pcb_layout_spread_search.go — `--spread` v2: a scored search over the poses
// (anchor and rotation) of the spreadable main chips.
//
// Every candidate is judged by the real result: the unchanged posed pipeline runs
// with the candidate as main poses, and the score adds the ratsnest length,
// crossings, congestion, P1/P2 issues (RF rules included), unplaced satellites
// and the RF chain length. Seeds are the v1 shelf pack, the current places and a
// force-directed layout, each legalised; simulated annealing then nudges, rotates
// and swaps chips. A cheap check (footprints inside the board, clear of
// blockers, keep-outs, antenna zones and each other; RF modules at the edge)
// runs before any evaluation. Deterministic: a fixed-seed generator and an
// evaluation budget, never the clock. The answer is never worse than the board
// as it is: without a better score the chips stay.

import (
	"fmt"
	"math"
	"strings"
)

const (
	spreadScoreCrossing   = 150.0
	spreadScoreCongestion = 20.0
	spreadScoreP2         = 400.0
	spreadScoreP1         = 5000.0
	spreadScoreUnplaced   = 20000.0
	spreadScoreRfChain    = 3.0
	spreadBudgetParts     = 7e4 // evaluations = this / parts^1.5, clamped (one evaluation costs ~parts^1.5)
	spreadBudgetMin       = 20
	spreadBudgetMax       = 400
	spreadSeed            = 0x5eed5eed
	spreadForceSteps      = 150
	spreadGridDiv         = 60.0 // legaliser grid: board min side / this
	spreadMinStepMil      = 10.0
	spreadStall           = 30 // annealing steps without a new best before the step halves
	spreadAttemptsFactor  = 20 // illegal candidates allowed per evaluation of budget
	spreadNoBetterMessage = "spread: no better placement found; left in place"
)

type spreadPose struct{ x, y, rot float64 }

// lsSpreadScore / lsSpreadSummary: what a --spread run reports about itself.
type lsSpreadScore struct {
	Score       float64 `json:"score"`
	RatsnestMil float64 `json:"ratsnestMil"`
	Crossings   int     `json:"crossings"`
	RfIssues    int     `json:"rfIssues"`
}

type lsSpreadSummary struct {
	Improved    bool          `json:"improved"`
	Evaluations int           `json:"evaluations"`
	Before      lsSpreadScore `json:"before"`
	After       lsSpreadScore `json:"after"`
}

type spreadSearch struct {
	p      *spreadPlan
	ap     []apComp // unposed board
	rf     rfCtx
	zones  []layoutBBox // antenna zones of antennas (they never move)
	rots   [][]float64  // allowed rotations per main
	eval   func([]spreadPose) (*lsResult, error)
	budget int
	used   int
	cache  map[string]spreadEval
	rng    uint64
	found  bool // a legal configuration other than the current one was evaluated
}

type spreadEval struct {
	res   *lsResult
	score float64
}

// spreadRNG is splitmix64: tiny, fast and identical everywhere.
func (s *spreadSearch) next() uint64 {
	s.rng += 0x9e3779b97f4a7c15
	z := s.rng
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
func (s *spreadSearch) float() float64 { return float64(s.next()>>11) / float64(1<<53) }
func (s *spreadSearch) intn(n int) int { return int(s.next() % uint64(n)) }

// spreadScore is the search objective of one result (lower is better).
func spreadScore(res *lsResult, rf rfCtx) (float64, lsSpreadScore) {
	p1, p2, rfn := 0, 0, 0
	for _, is := range res.Issues {
		if rfIssueMessage(is.Message) {
			rfn++
		}
		switch is.Severity {
		case "P1":
			p1++
		case "P2":
			p2++
		}
	}
	// Main chips on top of each other (a board drawn that way, or a bad candidate)
	// cost like a P1 each pair: the pipeline itself does not flag them.
	p1 += spreadMainOverlaps(res)
	m := res.Metrics
	score := m.RatsnestMil + spreadScoreCrossing*float64(m.Crossings) + spreadScoreCongestion*float64(m.CongestionPeak) +
		spreadScoreP2*float64(p2) + spreadScoreP1*float64(p1) + spreadScoreUnplaced*float64(m.Unplaced) +
		spreadScoreRfChain*rf.chainMil(res.Components)
	return score, lsSpreadScore{Score: round2(score), RatsnestMil: m.RatsnestMil, Crossings: m.Crossings, RfIssues: rfn}
}

// spreadMainOverlaps counts the pairs of main chips whose bboxes overlap on a
// shared side.
func spreadMainOverlaps(res *lsResult) int {
	var mains []lsComponent
	for _, c := range res.Components {
		if c.Role == "main" && c.BBox != nil {
			mains = append(mains, c)
		}
	}
	n := 0
	for a := range mains {
		for b := a + 1; b < len(mains); b++ {
			if (mains[a].Layer == mains[b].Layer || mains[a].Layer == 0 || mains[b].Layer == 0) && apOverlapArea(*mains[a].BBox, *mains[b].BBox) > apEps {
				n++
			}
		}
	}
	return n
}

func rfIssueMessage(msg string) bool {
	for _, pre := range []string{"inside the antenna clearance zone", "inductor ", "crystal ", "antenna ", rfIssueEdgeMessage} {
		if strings.HasPrefix(msg, pre) {
			return true
		}
	}
	return false
}

func spreadBudget(parts int) int {
	b := int(math.Round(spreadBudgetParts / math.Pow(math.Max(1, float64(parts)), 1.5)))
	return max(spreadBudgetMin, min(spreadBudgetMax, b))
}

// comp is main k at pose q.
func (s *spreadSearch) comp(k int, q spreadPose) apComp {
	return simTransform(s.ap[s.p.mains[k].idx], q.x, q.y, simNormDeg(q.rot))
}

func compBox(c apComp) layoutBBox {
	return layoutBBox{MinX: c.minX, MinY: c.minY, MaxX: c.maxX, MaxY: c.maxY}
}

// footprint is the bbox grown by the planner gap; an RF module keeps none on its
// antenna end (it sits on the board edge). zone is the module's antenna zone, or
// empty. The satellite ring only shapes the seeds: whether the satellites fit is
// for the real pipeline to say (unplaced and distance issues cost score).
func (s *spreadSearch) footprint(k int, c apComp) (fp layoutBBox, zone layoutBBox, hasZone, ok bool) {
	b := compBox(c)
	fp = apInflate(b, s.p.gap/2)
	if !s.rf.modules[rfKey(c.designator)] {
		return fp, zone, false, true
	}
	_, z, side, isMod := rfModuleZone(b, apPadsRect(c))
	if !isMod {
		return fp, zone, false, true
	}
	if !rfModuleAtEdge(b, side, s.p.board) {
		return fp, zone, false, false
	}
	switch side {
	case 0:
		fp.MinX = b.MinX
	case 1:
		fp.MaxX = b.MaxX
	case 2:
		fp.MinY = b.MinY
	default:
		fp.MaxY = b.MaxY
	}
	return fp, z, true, true
}

// legalOne checks main k at q against the board, the blockers and the fixed
// antenna zones (not against the other mains).
func (s *spreadSearch) legalOne(k int, q spreadPose) (layoutBBox, layoutBBox, layoutBBox, bool, bool) {
	c := s.comp(k, q)
	fp, zone, hasZone, ok := s.footprint(k, c)
	if !ok || !s.p.fits(fp, s.p.mains[k].side) {
		return fp, zone, layoutBBox{}, false, false
	}
	b := compBox(c)
	for _, z := range s.zones {
		if apOverlapArea(b, z) > apEps {
			return fp, zone, b, hasZone, false
		}
	}
	return fp, zone, b, hasZone, true
}

type spreadPlaced struct {
	fp, zone, box layoutBBox
	hasZone       bool
	side          int
}

func (s *spreadSearch) clash(a, b spreadPlaced) bool {
	if sameSide(a.side, b.side) && apOverlaps(a.fp, b.fp) {
		return true
	}
	return a.hasZone && apOverlapArea(b.box, a.zone) > apEps || b.hasZone && apOverlapArea(a.box, b.zone) > apEps
}

func (s *spreadSearch) placed(k int, q spreadPose) (spreadPlaced, bool) {
	fp, zone, box, hasZone, ok := s.legalOne(k, q)
	return spreadPlaced{fp: fp, zone: zone, box: box, hasZone: hasZone, side: s.p.mains[k].side}, ok
}

// legal checks a whole configuration.
func (s *spreadSearch) legal(cfg []spreadPose) bool {
	ps := make([]spreadPlaced, len(cfg))
	for k, q := range cfg {
		pl, ok := s.placed(k, q)
		if !ok {
			return false
		}
		for j := 0; j < k; j++ {
			if s.clash(ps[j], pl) {
				return false
			}
		}
		ps[k] = pl
	}
	return true
}

// legalMoved checks only the mains in moved, against the board, blockers, zones
// and every other main: a crowded board as drawn can still be improved one chip
// at a time.
func (s *spreadSearch) legalMoved(cfg []spreadPose, moved ...int) bool {
	for _, k := range moved {
		pk, ok := s.placed(k, cfg[k])
		if !ok {
			return false
		}
		for j := range cfg {
			if j == k {
				continue
			}
			pj, _ := s.placed(j, cfg[j])
			if s.clash(pk, pj) {
				return false
			}
		}
	}
	return true
}

// centreOf is the bbox centre of main k at q; poseAt is the pose whose bbox centre
// is (cx, cy) at rotation rot.
func (s *spreadSearch) centreOf(k int, q spreadPose) (float64, float64) {
	return compBox(s.comp(k, q)).centre()
}

func (s *spreadSearch) poseAt(k int, cx, cy, rot float64) spreadPose {
	ox, oy := s.centreOf(k, spreadPose{0, 0, rot})
	return spreadPose{round4(cx - ox), round4(cy - oy), rot}
}

func (b layoutBBox) centre() (float64, float64) { return (b.MinX + b.MaxX) / 2, (b.MinY + b.MaxY) / 2 }

// legalise keeps every legal main and moves each illegal one to the nearest legal
// grid place (any allowed rotation, its own first). Larger footprints go first.
func (s *spreadSearch) legalise(cfg []spreadPose) ([]spreadPose, bool) {
	n := len(cfg)
	out := append([]spreadPose(nil), cfg...)
	order := make([]int, n)
	for k := range order {
		order[k] = k
	}
	area := func(k int) float64 { return s.p.W[k] * s.p.H[k] }
	for i := 1; i < n; i++ {
		for j := i; j > 0 && area(order[j]) > area(order[j-1]); j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	var done []spreadPlaced
	fitsWith := func(k int, q spreadPose) (spreadPlaced, bool) {
		pl, ok := s.placed(k, q)
		if !ok {
			return pl, false
		}
		for _, d := range done {
			if s.clash(d, pl) {
				return pl, false
			}
		}
		return pl, true
	}
	bd := s.p.board
	step := math.Max(spreadMinStepMil, math.Min(bd.MaxX-bd.MinX, bd.MaxY-bd.MinY)/spreadGridDiv)
	for _, k := range order {
		if pl, ok := fitsWith(k, out[k]); ok {
			done = append(done, pl)
			continue
		}
		tx, ty := s.centreOf(k, out[k])
		bestD := math.Inf(1)
		var best spreadPose
		var bestPl spreadPlaced
		rots := append([]float64{out[k].rot}, s.rots[k]...)
		for ri, rot := range rots {
			if ri > 0 && simAngleDiff(rot, out[k].rot) < simAngEps {
				continue
			}
			for gy := bd.MinY + step/2; gy < bd.MaxY; gy += step {
				for gx := bd.MinX + step/2; gx < bd.MaxX; gx += step {
					d := math.Hypot(gx-tx, gy-ty)
					if d >= bestD-1e-9 {
						continue
					}
					q := s.poseAt(k, gx, gy, rot)
					if pl, ok := fitsWith(k, q); ok {
						bestD, best, bestPl = d, q, pl
					}
				}
			}
		}
		if math.IsInf(bestD, 1) {
			return nil, false
		}
		out[k] = best
		done = append(done, bestPl)
	}
	return out, true
}

// forceSeed pulls each main toward the parts it connects to (its RF chip toward
// the antenna), pushes overlapping footprints apart and keeps them on the board.
func (s *spreadSearch) forceSeed(cur []spreadPose, antPull [][2]float64) []spreadPose {
	n := len(cur)
	cx, cy := make([]float64, n), make([]float64, n)
	for k, q := range cur {
		cx[k], cy[k] = s.centreOf(k, q)
	}
	bd := s.p.board
	maxW := 1.0
	for k := 0; k < n; k++ {
		maxW = math.Max(maxW, s.p.total(k))
	}
	for it := 0; it < spreadForceSteps; it++ {
		nx, ny := append([]float64(nil), cx...), append([]float64(nil), cy...)
		for k := 0; k < n; k++ {
			fx, fy, wt := 0.0, 0.0, 0.0
			for j := 0; j < n; j++ {
				if w := s.p.wMM[k][j]; w > 0 && j != k {
					fx, fy, wt = fx+w*(cx[j]-cx[k]), fy+w*(cy[j]-cy[k]), wt+w
				}
			}
			for o, w := range s.p.wMO[k] {
				if w > 0 {
					fx, fy, wt = fx+w*(s.p.obst[o][0]-cx[k]), fy+w*(s.p.obst[o][1]-cy[k]), wt+w
				}
			}
			if !math.IsNaN(antPull[k][0]) {
				w := 2 * maxW
				fx, fy, wt = fx+w*(antPull[k][0]-cx[k]), fy+w*(antPull[k][1]-cy[k]), wt+w
			}
			if wt > 0 {
				nx[k] += 0.3 * fx / wt
				ny[k] += 0.3 * fy / wt
			}
		}
		for a := 0; a < n; a++ {
			for b := a + 1; b < n; b++ {
				ox := (s.p.W[a]+s.p.W[b])/2 - math.Abs(nx[a]-nx[b])
				oy := (s.p.H[a]+s.p.H[b])/2 - math.Abs(ny[a]-ny[b])
				if ox <= 0 || oy <= 0 {
					continue
				}
				if ox < oy {
					d := ox / 2
					if nx[a] < nx[b] || nx[a] == nx[b] && a < b {
						d = -d
					}
					nx[a], nx[b] = nx[a]+d, nx[b]-d
				} else {
					d := oy / 2
					if ny[a] < ny[b] || ny[a] == ny[b] && a < b {
						d = -d
					}
					ny[a], ny[b] = ny[a]+d, ny[b]-d
				}
			}
		}
		for k := 0; k < n; k++ {
			hw, hh := math.Min(s.p.W[k], bd.MaxX-bd.MinX)/2, math.Min(s.p.H[k], bd.MaxY-bd.MinY)/2
			nx[k] = math.Max(bd.MinX+hw, math.Min(bd.MaxX-hw, nx[k]))
			ny[k] = math.Max(bd.MinY+hh, math.Min(bd.MaxY-hh, ny[k]))
		}
		cx, cy = nx, ny
	}
	out := make([]spreadPose, n)
	for k := range out {
		out[k] = s.poseAt(k, cx[k], cy[k], cur[k].rot)
	}
	return out
}

func spreadKey(cfg []spreadPose) string {
	var b strings.Builder
	for _, q := range cfg {
		fmt.Fprintf(&b, "%.3f,%.3f,%.0f;", q.x, q.y, simNormDeg(q.rot))
	}
	return b.String()
}

// score evaluates a legal configuration once (cached); ok is false when the
// budget is spent or the pipeline refused it.
func (s *spreadSearch) score(cfg []spreadPose) (spreadEval, bool) {
	key := spreadKey(cfg)
	if e, hit := s.cache[key]; hit {
		return e, true
	}
	if s.used >= s.budget {
		return spreadEval{}, false
	}
	s.used++
	res, err := s.eval(cfg)
	if err != nil {
		return spreadEval{}, false
	}
	sc, _ := spreadScore(res, s.rf)
	e := spreadEval{res: res, score: sc}
	s.cache[key] = e
	return e, true
}

// run searches from the current configuration cur (whose result is base) and
// returns the best configuration and its evaluation.
//
// An illegal current configuration (overlapping chips, a module off the edge) is
// no candidate: any legal one beats it.
func (s *spreadSearch) run(cur []spreadPose, base spreadEval, seed1 []spreadPose, antPull [][2]float64) ([]spreadPose, spreadEval) {
	best, bestE := cur, base
	type start struct {
		cfg []spreadPose
		e   spreadEval
	}
	starts := []start{{cur, base}}
	for _, sd := range [][]spreadPose{seed1, cur, s.forceSeed(cur, antPull)} {
		if sd == nil {
			continue
		}
		l, ok := s.legalise(sd)
		if !ok || !s.legal(l) {
			continue
		}
		e, ok := s.score(l)
		if !ok {
			continue
		}
		s.found = true
		dup := false
		for _, st := range starts {
			dup = dup || spreadKey(st.cfg) == spreadKey(l)
		}
		if !dup {
			starts = append(starts, start{l, e})
		}
		if e.score < bestE.score-1e-9 {
			best, bestE = l, e
		}
	}
	// Anneal from every start in turn (best first), sharing what is left of the budget.
	for a := 1; a < len(starts); a++ {
		for b := a; b > 0 && starts[b].e.score < starts[b-1].e.score; b-- {
			starts[b], starts[b-1] = starts[b-1], starts[b]
		}
	}
	for i, st := range starts {
		limit := s.used + (s.budget-s.used)/(len(starts)-i)
		if c, e := s.anneal(st.cfg, st.e, limit); e.score < bestE.score-1e-9 {
			best, bestE = c, e
		}
	}
	return best, bestE
}

// anneal improves one start until s.used reaches limit and returns the best it saw.
func (s *spreadSearch) anneal(start []spreadPose, startE spreadEval, limit int) ([]spreadPose, spreadEval) {
	n := len(start)
	bd := s.p.board
	step0 := math.Min(bd.MaxX-bd.MinX, bd.MaxY-bd.MinY) / 8
	step := step0
	best, bestE := start, startE
	state, stateE := append([]spreadPose(nil), start...), startE
	t0 := math.Max(1, 0.02*startE.score)
	first := s.used
	attempts, stall := 0, 0
	maxAttempts := spreadAttemptsFactor * max(1, limit-first)
	for s.used < limit && attempts < maxAttempts {
		attempts++
		cand := append([]spreadPose(nil), state...)
		k := s.intn(n)
		moved := []int{k}
		switch r := s.float(); {
		case r < 0.5 || n == 1 && r < 0.75:
			a, l := 2*math.Pi*s.float(), step*(0.25+s.float())
			cand[k].x = round4(cand[k].x + l*math.Cos(a))
			cand[k].y = round4(cand[k].y + l*math.Sin(a))
		case r < 0.75 || n == 1:
			if len(s.rots[k]) < 2 {
				continue
			}
			cx, cy := s.centreOf(k, cand[k])
			rot := s.rots[k][s.intn(len(s.rots[k]))]
			if simAngleDiff(rot, cand[k].rot) < simAngEps {
				continue
			}
			cand[k] = s.poseAt(k, cx, cy, rot) // turn in place
		default:
			j := s.intn(n - 1)
			if j >= k {
				j++
			}
			ax, ay := s.centreOf(k, cand[k])
			bx, by := s.centreOf(j, cand[j])
			cand[k] = s.poseAt(k, bx, by, cand[k].rot)
			cand[j] = s.poseAt(j, ax, ay, cand[j].rot)
			moved = append(moved, j)
		}
		narrow := func() {
			// Narrow the step; at the floor, restart wide from the best (a new basin).
			step, stall = step/2, 0
			if step < spreadMinStepMil {
				step = step0
			}
			state, stateE = append([]spreadPose(nil), best...), bestE
		}
		if !s.legalMoved(cand, moved...) {
			if stall++; stall >= 4*spreadStall {
				narrow()
			}
			continue
		}
		e, ok := s.score(cand)
		if !ok {
			break
		}
		s.found = s.found || s.legal(cand)
		temp := t0 * math.Pow(1e-4, float64(s.used-first)/float64(max(1, limit-first)))
		if e.score < stateE.score || s.float() < math.Exp(-(e.score-stateE.score)/temp) {
			state, stateE = cand, e
		}
		if e.score < bestE.score-1e-9 {
			best, bestE, stall = cand, e, 0
		} else if stall++; stall >= spreadStall {
			narrow()
		}
	}
	return best, bestE
}

// simSpreadSearch is the --spread entry: the poses of the spreadable mains keyed
// by posed index (nil: they stay), the summary and any issue to report.
func simSpreadSearch(ap, posed []apComp, opt apOptions, pre apPlanInfo, movable map[int]bool, lockedRoute []bool, poly [][2]float64,
	rf rfCtx, base *lsResult, eval func(map[int]spreadPose) (*lsResult, error)) (map[int]spreadPose, *lsSpreadSummary, []lsIssue) {
	baseScore, baseSum := spreadScore(base, rf)
	sum := &lsSpreadSummary{Before: baseSum, After: baseSum, Evaluations: 1}
	p, extra := spreadSetup(posed, opt, pre, movable, lockedRoute, poly)
	if p == nil {
		return nil, sum, extra
	}
	seedPos, _, packed := p.packSeed()
	s := &spreadSearch{p: p, ap: ap, rf: rf, budget: spreadBudget(len(ap)), used: 1, cache: map[string]spreadEval{}, rng: spreadSeed}
	n := len(p.mains)
	s.rots = make([][]float64, n)
	cur := make([]spreadPose, n)
	var seed1 []spreadPose
	if packed {
		seed1 = make([]spreadPose, n)
	}
	antPull := make([][2]float64, n)
	for k, m := range p.mains {
		c := posed[m.idx]
		r0 := simNormDeg(c.rotation)
		cur[k] = spreadPose{c.x, c.y, r0}
		if packed {
			seed1[k] = spreadPose{seedPos[k][0], seedPos[k][1], r0}
		}
		for d := 0.0; d < 360; d += 90 {
			s.rots[k] = append(s.rots[k], simNormDeg(r0+d))
		}
		antPull[k] = [2]float64{math.NaN(), math.NaN()}
	}
	// Fixed antennas: their zones block the mains, and the chip of a chain is pulled to its antenna.
	for i, c := range posed {
		if !isAntennaComp(c) || !c.hasBBox {
			continue
		}
		box := compBox(c)
		chip, has := pre.AntChip[i]
		tx, ty := box.centre()
		if has {
			tx, ty = posed[chip].bboxCenter()
			for k, m := range p.mains {
				if m.idx == chip {
					antPull[k][0], antPull[k][1] = box.centre()
				}
			}
		} else if opt.board != nil {
			tx, ty = opt.board.centre()
		}
		s.zones = append(s.zones, rfAntennaZone(box, tx, ty))
	}
	s.eval = func(cfg []spreadPose) (*lsResult, error) {
		m := make(map[int]spreadPose, len(cfg))
		for k, q := range cfg {
			m[p.mains[k].idx] = q
		}
		return eval(m)
	}
	baseE := spreadEval{res: base, score: baseScore}
	s.cache[spreadKey(cur)] = baseE
	best, bestE := s.run(cur, baseE, seed1, antPull)
	sum.Evaluations = s.used
	if !s.found && spreadMainOverlaps(base) > 0 {
		return nil, sum, spreadNoFit() // overlapping chips and no legal place for them
	}
	if bestE.score >= baseScore-1e-6 || spreadKey(best) == spreadKey(cur) {
		return nil, sum, []lsIssue{{Severity: "P3", Message: spreadNoBetterMessage, Fix: "the chips are already placed better than any spread found; move them by hand if needed"}}
	}
	_, sum.After = spreadScore(bestE.res, rf)
	sum.Improved = true
	out := make(map[int]spreadPose, n)
	for k, m := range p.mains {
		out[m.idx] = best[k]
	}
	return out, sum, nil
}
