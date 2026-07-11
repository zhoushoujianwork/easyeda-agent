package app

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

// pcb_place_constrained.go — constraint-driven TIERED placement (daemon-side).
//
// The fix for whack-a-mole layout: place POSITION-CONSTRAINED parts first and
// LOCK them, then legalize the rest around the locked set — so a satellite pass
// can never push an edge connector off its edge. Tiers (highest priority first):
//
//	Tier 1  mounting holes            — passed in as obstacles (placed via `pcb slot`), never moved.
//	Tier 2  edge-constrained parts    — connectors (USB / terminal / card socket / IPEX) + RF
//	                                     modules → snapped flush to their NEAREST board edge, fixed.
//	Tier 3  main chips + crystals     — anchors, kept where they are (fixed).
//	        + block-anchored parts    — a part the block pinned to a deliberate
//	                                     non-edge spot (e.g. a bus-terminal jumper).
//	Tier 4  satellites + user-facing  — legalized (spiral) around the fixed set, avoiding holes.
//
// Classification CONSUMES the circuit-block library's declarative placement hints
// (internal/blocks/data/*.json → placement.<REF>.{board_edge,edge,...}): a placed
// part is reverse-mapped to its block role by its distinctive designator prefix,
// and only falls back to the hardcoded footprint/designator regex when no block
// role matches. The block data is the single source-of-truth (see the
// improvements-sink-to-blocks rule) — the regex merely mirrors it as a safety net.
// A board that was block-assembled or built from the schematic both work: we read
// what's placed, not how it got there (placed parts carry no block link, hence the
// device-name / prefix reverse lookup).

type cpClass int

const (
	cpSatellite  cpClass = iota // tier 4 (default)
	cpUserFacing                // tier 4, but wants to stay near an edge / visible (LED, button)
	cpMainChip                  // tier 3
	cpEdgeMust                  // tier 2 — MUST sit at a board edge (connector / module / IPEX)
	cpAnchored                  // tier 3 — block declares a DELIBERATE non-edge position
	// (e.g. a bus-terminal jumper next to its resistor); keep it put, don't spiral.
)

func (k cpClass) String() string {
	switch k {
	case cpEdgeMust:
		return "edge"
	case cpMainChip:
		return "main"
	case cpAnchored:
		return "anchored"
	case cpUserFacing:
		return "user-facing"
	default:
		return "satellite"
	}
}

// cpPlacementIndex lazily loads (and caches) the block library's placement hints
// so classifyCP can consult the declarative source-of-truth before falling back
// to the hardcoded regex below. The block data is go:embed'd, so this never
// touches disk. Built once; the index is read-only after.
var (
	cpIdxOnce sync.Once
	cpIdx     blocks.PlacementIndex
)

func placementIndex() blocks.PlacementIndex {
	cpIdxOnce.Do(func() {
		// Errors leave cpIdx zero-valued (empty maps → classifyCP just uses the
		// regex fallback), so a broken block library degrades, never panics.
		if idx, err := blocks.LoadPlacementIndex(); err == nil {
			cpIdx = idx
		}
	})
	return cpIdx
}

// cpOpenOnce lazily loads the block library's connector-opening declarations
// (which local side a connector's opening faces), so Tier-2 can orient a symmetric
// terminal whose opening isn't in the pad geometry. Built once; read-only after.
var (
	cpOpenOnce sync.Once
	cpOpen     []blocks.ConnectorOpening
)

func connectorOpenings() []blocks.ConnectorOpening {
	cpOpenOnce.Do(func() {
		if o, err := blocks.LoadConnectorOpenings(); err == nil {
			cpOpen = o
		}
	})
	return cpOpen
}

// openingVec parses a local opening string ("+x"/"-x"/"+y"/"-y") into a unit vector.
func openingVec(local string) (float64, float64, bool) {
	switch strings.ToLower(strings.TrimSpace(local)) {
	case "+x", "x":
		return 1, 0, true
	case "-x":
		return -1, 0, true
	case "+y", "y":
		return 0, 1, true
	case "-y":
		return 0, -1, true
	}
	return 0, 0, false
}

// connOpeningFor returns the block-declared LOCAL opening vector for a device (by
// manufacturerId substring), if any block declares one.
func connOpeningFor(device string) (float64, float64, bool) {
	d := strings.ToLower(strings.TrimSpace(device))
	if d == "" {
		return 0, 0, false
	}
	for _, o := range connectorOpenings() {
		if strings.Contains(d, strings.ToLower(o.Match)) {
			if vx, vy, ok := openingVec(o.Local); ok {
				return vx, vy, true
			}
		}
	}
	return 0, 0, false
}

// rotate2d rotates (x,y) by deg CCW — the EasyEDA component-rotation convention
// (calibrated on a real KF301: its local -y opening points +x at rotation 90).
func rotate2d(x, y, deg float64) (float64, float64) {
	r := deg * math.Pi / 180
	c, s := math.Cos(r), math.Sin(r)
	return x*c - y*s, x*s + y*c
}

// openingTargetDelta returns the rotation delta ∈ {0,90,180,270} to ADD to curRot
// so the connector's local opening ends up facing OFF the assigned board edge.
func openingTargetDelta(curRot, lox, loy float64, edge apEdge) float64 {
	ix, iy := edgeInteriorDir(edge)
	obx, oby := -ix, -iy // off-board = away from the interior
	bestDelta, bestDot := 0.0, math.Inf(-1)
	for _, d := range []float64{0, 90, 180, 270} {
		ax, ay := rotate2d(lox, loy, curRot+d)
		if dot := ax*obx + ay*oby; dot > bestDot {
			bestDot, bestDelta = dot, d
		}
	}
	return bestDelta
}

// classFromHint maps a block placement hint to a placement tier. It decides ONLY
// on an EXPLICIT signal; an advisory hint (a side/orientation note with no
// board_edge / user-facing / anchor) returns ok=false so classifyCP falls through
// to the regex/pin-count heuristic — an ordinary decoupling cap that merely
// carries a "side: top" note must NOT be frozen in place.
//
//   - board_edge=true                  → edge-must (tier 2), snapped to an edge.
//   - edge="user-facing" (not edge)    → tier 4 user-facing (LED / button).
//   - anchor=true (deliberate non-edge)→ anchored (tier 3): the block pinned it to
//     a specific spot beside another part (e.g. JP701 by its 120R terminator), so
//     keep it put rather than spiral it to a board corner.
//   - otherwise                        → (_, false): advisory only, no tier forced.
//
// The hint only ever promotes a part to a stronger placement role; it never
// demotes a main chip (which the pin-count fallback still catches).
func classFromHint(h blocks.PlacementHint) (cpClass, bool) {
	switch {
	case h.BoardEdge:
		return cpEdgeMust, true
	case strings.EqualFold(strings.TrimSpace(h.Edge), "user-facing"):
		return cpUserFacing, true
	case h.Anchor:
		return cpAnchored, true
	default:
		return 0, false
	}
}

// Footprint / designator patterns for the position-constrained categories. These
// only MIRROR the block library's placement hints — they are the FALLBACK, used
// when classifyCP can't reverse-map a placed part to a block role by its
// designator prefix (see placementIndex). The block data is the source-of-truth.
var (
	cpReEdgeConn = regexp.MustCompile(`(?i)usb|type-?c|micro-?sd|tf[-_ ]?card|sd[-_]?card|push-?push|ipex|u\.?fl|sma|ufl|kf301|kf128|kf2edg|terminal|screw|hdr|header|pin-?header|conn`)
	cpReModule   = regexp.MustCompile(`(?i)wroom|wrover|esp32.*(module|wifi|smd)`)
	cpReSwitch   = regexp.MustCompile(`(?i)tact|switch|\bkey\b|button|sw-?smd`)
	cpReLED      = regexp.MustCompile(`(?i)\bled\b`)
	cpReCrystal  = regexp.MustCompile(`(?i)xtal|crystal|osc|3225|3215|2016|2520|smd-?4p`)
)

// cpComp carries a placed component's device NAME (PCB components expose no
// footprint-name string, so we pattern-match the device name — e.g.
// "esp32-s3-wroom-1u-n8" — for module detection; connectors are caught by the
// J* designator prefix anyway) + copper layer (TOP=1 / BOTTOM=2).
type cpComp struct {
	apComp
	footprint string // device name, used for module/connector pattern matching
	layer     int
}

// classify decides the placement tier from footprint + designator + pin count.
//
// Block-data FIRST: the declarative placement hints in internal/blocks/data/*.json
// are the source-of-truth (see improvements-sink-to-blocks). We reverse-map the
// placed part to a block role by its DISTINCTIVE designator prefix (a placed part
// carries no block link, so we can only match on what it exposes; device-level
// precision is a future layer — see blocks.PlacementIndex). Only when the prefix
// yields no explicit hint do we fall through to the hardcoded regex heuristic
// below — that regex is the fallback, not the primary path.
func classifyCP(c cpComp, mainPins int) cpClass {
	fp := c.footprint
	des := strings.ToUpper(c.designator)

	// Block data FIRST: reverse-map the placed part to a block role by DISTINCTIVE
	// designator prefix (JP/SW/LED/ANT…). The prefix is itself block-declared, so
	// this consumes the source-of-truth (per improvements-sink-to-blocks). Only an
	// EXPLICIT hint (board_edge/user-facing/anchor) decides a tier; an advisory
	// hint falls through to the regex heuristic below.
	idx := placementIndex()
	if h, ok := idx.ByRefPrefix[refPrefixCP(des)]; ok {
		if cls, decided := classFromHint(h); decided {
			return cls
		}
	}
	// A connector/module footprint, OR a Jxx designator that isn't a plain header
	// resistor — treat as edge-must.
	if cpReModule.MatchString(fp) {
		return cpEdgeMust
	}
	// Jxx connectors are edge-must, but NOT JPxx (a jumper/link — belongs by its net,
	// not at a board edge). A J-prefix with a connector-ish footprint also qualifies.
	if cpReEdgeConn.MatchString(fp) || (strings.HasPrefix(des, "J") && !strings.HasPrefix(des, "JP")) {
		return cpEdgeMust
	}
	if cpReSwitch.MatchString(fp) || strings.HasPrefix(des, "SW") {
		return cpUserFacing
	}
	if cpReLED.MatchString(fp) || strings.HasPrefix(des, "LED") {
		return cpUserFacing
	}
	// Main chip by distinct-pin count (crystals are few-pin but anchor near their IC
	// — fold them into main so they stay put in tier 3).
	if c.distinctPins() >= mainPins || cpReCrystal.MatchString(fp) {
		return cpMainChip
	}
	return cpSatellite
}

// refPrefixCP returns the leading alphabetic run of a designator, upper-cased
// ("JP701" → "JP", "J4" → "J"). Mirrors blocks.refPrefix so the app-side lookup
// key matches the index key.
func refPrefixCP(des string) string {
	des = strings.ToUpper(strings.TrimSpace(des))
	i := 0
	for i < len(des) && des[i] >= 'A' && des[i] <= 'Z' {
		i++
	}
	return des[:i]
}

// edgeInteriorDir is the unit vector pointing from a board edge toward the board
// interior. An edge connector should sit with its PADS on the interior side (traces
// route inward) and its OPENING facing out — so we orient it to maximize the
// pad-centroid projection along this direction.
func edgeInteriorDir(e apEdge) (float64, float64) {
	switch e {
	case edgeLeft:
		return 1, 0
	case edgeRight:
		return -1, 0
	case edgeBottom:
		return 0, 1
	default: // edgeTop
		return 0, -1
	}
}

// connGeom returns, for component c rotated by deltaDeg about its anchor, the pad
// centroid and rotated bbox (all absolute). Used to pick the orientation whose
// opening faces off-board.
func connGeom(c cpComp, deltaDeg float64) (pcx, pcy, bx0, by0, bx1, by1 float64) {
	var sx, sy float64
	n := 0
	for _, p := range c.pads {
		rx, ry := rotateVec(p.x-c.x, p.y-c.y, deltaDeg)
		sx, sy = sx+c.x+rx, sy+c.y+ry
		n++
	}
	if n > 0 {
		pcx, pcy = sx/float64(n), sy/float64(n)
	} else {
		pcx, pcy = c.x, c.y
	}
	bx0, by0 = math.Inf(1), math.Inf(1)
	bx1, by1 = math.Inf(-1), math.Inf(-1)
	for _, cn := range [4][2]float64{{c.minX, c.minY}, {c.maxX, c.minY}, {c.maxX, c.maxY}, {c.minX, c.maxY}} {
		rx, ry := rotateVec(cn[0]-c.x, cn[1]-c.y, deltaDeg)
		ax, ay := c.x+rx, c.y+ry
		bx0, by0 = math.Min(bx0, ax), math.Min(by0, ay)
		bx1, by1 = math.Max(bx1, ax), math.Max(by1, ay)
	}
	return
}

// bestConnDelta picks the rotation (0/90/180/270 relative to current) whose opening
// faces off the assigned edge — i.e. maximizes the pad-centroid projection toward
// the board interior — returning the delta and that projection score.
func bestConnDelta(c cpComp, edge apEdge) (delta, score float64) {
	ix, iy := edgeInteriorDir(edge)
	score = math.Inf(-1)
	for _, dd := range []float64{0, 90, 180, 270} {
		pcx, pcy, gx0, gy0, gx1, gy1 := connGeom(c, dd)
		s := (pcx-(gx0+gx1)/2)*ix + (pcy-(gy0+gy1)/2)*iy
		if s > score {
			score, delta = s, dd
		}
	}
	return
}

type cpHole struct{ x, y, r float64 }

type cpOptions struct {
	mainPins   int
	edgeMargin float64 // gap between an edge part's bbox and the board edge
	partGap    float64 // clearance between any two parts / part-to-hole
	board      *cpRect // REAL board-outline bbox; nil → fall back to the part-cloud union extent
}

func defaultCpOptions() cpOptions {
	return cpOptions{mainPins: 8, edgeMargin: 45, partGap: 14}
}

type cpRect struct{ x0, y0, x1, y1 float64 }

func (r cpRect) overlaps(o cpRect) bool {
	return !(r.x1 <= o.x0 || o.x1 <= r.x0 || r.y1 <= o.y0 || o.y1 <= r.y0)
}

// planConstrainedPlace runs the tiered placement over a snapshot of components +
// mounting holes, returning the anchor moves. Pure: no I/O, so it unit-tests.
func planConstrainedPlace(comps []cpComp, holes []cpHole, opt cpOptions) ([]apMove, []apDiag) {
	var moves []apMove
	var diags []apDiag
	if len(comps) == 0 {
		return moves, diags
	}
	// Board rect. Prefer the REAL board outline when the caller supplies it; the
	// part-cloud union below is only the fallback. Using the part cloud as "the
	// board" is wrong whenever the outline differs from the placed extent — the
	// topmost part would otherwise define its own "top edge", so an edge connector
	// snaps to the part pile instead of the actual board edge (the flaky U1 WROOM
	// edge:top on ceshi was exactly this).
	bx0, by0 := math.Inf(1), math.Inf(1)
	bx1, by1 := math.Inf(-1), math.Inf(-1)
	for _, c := range comps {
		if !c.hasBBox {
			continue
		}
		bx0, by0 = math.Min(bx0, c.minX), math.Min(by0, c.minY)
		bx1, by1 = math.Max(bx1, c.maxX), math.Max(by1, c.maxY)
	}
	if opt.board != nil {
		bx0, by0, bx1, by1 = opt.board.x0, opt.board.y0, opt.board.x1, opt.board.y1
	}
	m := opt.partGap
	// placed holds the FIXED rects (edge parts + mains + holes) satellites avoid.
	var placed []cpRect
	for _, h := range holes {
		placed = append(placed, cpRect{h.x - h.r, h.y - h.r, h.x + h.r, h.y + h.r})
	}
	// Layer-aware: a satellite only clashes with same-layer fixed parts. Track layer per rect.
	type lrect struct {
		cpRect
		layer int
	}
	var lplaced []lrect
	addFixed := func(r cpRect, layer int) {
		placed = append(placed, r)
		lplaced = append(lplaced, lrect{r, layer})
	}

	// Classify.
	kinds := make([]cpClass, len(comps))
	for i, c := range comps {
		kinds[i] = classifyCP(c, opt.mainPins)
	}

	// ── Tier 2: edge-must → snap to nearest board edge, fix. ──────────────────
	// Order: biggest first (big connectors claim edge space before small ones).
	edgeIdx := []int{}
	for i := range comps {
		if kinds[i] == cpEdgeMust {
			edgeIdx = append(edgeIdx, i)
		}
	}
	sort.Slice(edgeIdx, func(a, b int) bool {
		ca, cb := comps[edgeIdx[a]], comps[edgeIdx[b]]
		return ca.width()*ca.height() > cb.width()*cb.height()
	})
	for _, i := range edgeIdx {
		c := comps[i]
		if !c.hasBBox {
			continue
		}
		cx, cy := c.bboxCenter()
		// nearest edge by bbox-center distance
		dL, dR, dB, dT := cx-bx0, bx1-cx, cy-by0, by1-cy
		best := dL
		edge := edgeLeft
		if dR < best {
			best, edge = dR, edgeRight
		}
		if dB < best {
			best, edge = dB, edgeBottom
		}
		if dT < best {
			best, edge = dT, edgeTop
		}
		// Orientation: pick the rotation whose OPENING faces off the edge (pads face
		// the interior). Recognizes a part the user already oriented correctly and
		// leaves it — only rotates when the current opening points the wrong way.
		delta, score := bestConnDelta(c, edge)
		curScore := (func() float64 {
			pcx, pcy, gx0, gy0, gx1, gy1 := connGeom(c, 0)
			ix, iy := edgeInteriorDir(edge)
			return (pcx-(gx0+gx1)/2)*ix + (pcy-(gy0+gy1)/2)*iy
		})()
		// Orientation is only auto-corrected for ASYMMETRIC connectors where the pad
		// geometry actually reveals the opening direction (USB, SD, IPEX). Only rotate
		// when the CURRENT opening clearly faces the interior (wrong) AND a rotation
		// clearly fixes it. A symmetric 2-pin terminal / header has near-zero score
		// either way → the opening direction isn't in the pads, so PRESERVE the
		// current (user-set) rotation rather than guess. Also recognize an already
		// edge-flush, outward-facing part and leave it untouched.
		alreadyGood := best <= opt.edgeMargin+30 && curScore > 15
		clearlyWrong := curScore < -30 && score > 30
		blockOriented := false
		if lox, loy, ok := connOpeningFor(c.footprint); ok {
			// The block DECLARES which local side the opening faces → deterministic:
			// rotate so it faces off-board. This overrides the pad-geometry guess AND
			// the confirm flag — the opening isn't in the pads for a symmetric terminal.
			delta = openingTargetDelta(c.rotation, lox, loy, edge)
			blockOriented = true
		} else if alreadyGood || !clearlyWrong {
			delta = 0
		}
		// Geometry after the chosen rotation (about the anchor).
		_, _, gx0, gy0, gx1, gy1 := connGeom(c, delta)
		var shiftX, shiftY float64
		switch edge {
		case edgeLeft:
			shiftX = (bx0 + opt.edgeMargin) - gx0
		case edgeRight:
			shiftX = (bx1 - opt.edgeMargin) - gx1
		case edgeBottom:
			shiftY = (by0 + opt.edgeMargin) - gy0
		case edgeTop:
			shiftY = (by1 - opt.edgeMargin) - gy1
		}
		nx, ny := c.x+shiftX, c.y+shiftY
		nr := cpRect{gx0 + shiftX - m, gy0 + shiftY - m, gx1 + shiftX + m, gy1 + shiftY + m}
		addFixed(nr, c.layer)
		if math.Abs(shiftX) > 1 || math.Abs(shiftY) > 1 || delta != 0 {
			moves = append(moves, apMove{ID: c.id, Designator: c.designator,
				NewX: round1(nx), NewY: round1(ny), NewRot: c.rotation + delta, SetRot: delta != 0, Edge: edge.String()})
		}
		// If we could NEITHER confirm the opening already faces out (alreadyGood) NOR
		// confidently rotate it (clearlyWrong), then the opening direction isn't in the
		// pad geometry — a symmetric 2-pin terminal / header. Don't silently leave a
		// possibly-wrong-facing connector: flag it so the user confirms the opening
		// faces off-board or hand-places it (per the "对称件保留用户手调" rule).
		needsConfirm := !blockOriented && !alreadyGood && !clearlyWrong && curScore <= 15
		reason := "edge:" + edge.String()
		switch {
		case blockOriented && delta != 0:
			reason += ":oriented-by-block"
		case blockOriented:
			reason += ":block-ok"
		case alreadyGood:
			reason += ":recognized"
		case delta != 0:
			reason += ":oriented"
		case needsConfirm:
			reason += ":confirm-orientation"
		}
		diags = append(diags, apDiag{Designator: c.designator, Reason: reason})
	}

	// ── Tier 3: main chips + crystals + block-anchored parts → keep, fix. ─────
	// cpAnchored is a part the block deliberately pinned to a specific non-edge
	// spot (e.g. a bus-terminal jumper beside its resistor). Like a main chip we
	// leave it where it is and add it to the fixed set, so the Tier-4 spiral can
	// never fling it to a corner.
	for i, c := range comps {
		if (kinds[i] != cpMainChip && kinds[i] != cpAnchored) || !c.hasBBox {
			continue
		}
		addFixed(cpRect{c.minX - m, c.minY - m, c.maxX + m, c.maxY + m}, c.layer)
		reason := "main:fixed"
		if kinds[i] == cpAnchored {
			reason = "anchored:fixed"
		}
		diags = append(diags, apDiag{Designator: c.designator, Reason: reason})
	}

	// ── Tier 4: satellites + user-facing → legalize (spiral) around fixed. ────
	satIdx := []int{}
	for i := range comps {
		if kinds[i] == cpSatellite || kinds[i] == cpUserFacing {
			satIdx = append(satIdx, i)
		}
	}
	// Biggest satellites first (they need the most room).
	sort.Slice(satIdx, func(a, b int) bool {
		ca, cb := comps[satIdx[a]], comps[satIdx[b]]
		return ca.width()*ca.height() > cb.width()*cb.height()
	})
	clashFixed := func(r cpRect, layer int) bool {
		for _, h := range holes { // holes cut every layer
			if (cpRect{h.x - h.r, h.y - h.r, h.x + h.r, h.y + h.r}).overlaps(r) {
				return true
			}
		}
		for _, lr := range lplaced {
			if lr.layer == layer && lr.cpRect.overlaps(r) {
				return true
			}
		}
		return false
	}
	inside := func(r cpRect) bool {
		return !(r.x0 < bx0-20 || r.y0 < by0-20 || r.x1 > bx1+20 || r.y1 > by1+20)
	}
	// Net-aware seed source: pads of the FIXED, NON-MOVED parts (mains + anchored).
	// Tier-2 edge parts DID move, so their pad coords are stale — exclude them.
	// A satellite that must be relocated is seeded near its nearest electrically-
	// related fixed pad, so a decoupling cap clusters onto its chip instead of
	// landing at the first free slot near a bad import position.
	fixedNetPads := map[string][]apPad{}
	for i, c := range comps {
		if kinds[i] != cpMainChip && kinds[i] != cpAnchored {
			continue
		}
		for _, p := range c.pads {
			if n := strings.TrimSpace(p.net); n != "" {
				fixedNetPads[n] = append(fixedNetPads[n], p)
			}
		}
	}
	netSeed := func(c cpComp, fromX, fromY float64) (float64, float64, bool) {
		var cand []apPad
		seen := map[string]bool{}
		for _, p := range c.pads {
			n := strings.TrimSpace(p.net)
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			cand = append(cand, fixedNetPads[n]...)
		}
		if pad, ok := nearestPad(cand, fromX, fromY); ok {
			return pad.x, pad.y, true
		}
		return 0, 0, false
	}

	for _, i := range satIdx {
		c := comps[i]
		if !c.hasBBox {
			continue
		}
		cx0, cy0 := c.bboxCenter()
		hw, hh := c.width()/2, c.height()/2
		// Keep a well-placed satellite EXACTLY where it is (no gratuitous moves —
		// don't disturb a hand-placed layout). Only relocate one that clashes.
		cur := cpRect{cx0 - hw - m, cy0 - hh - m, cx0 + hw + m, cy0 + hh + m}
		if inside(cur) && !clashFixed(cur, c.layer) {
			addFixed(cur, c.layer)
			continue
		}
		// Must relocate. A pure satellite (decoupling cap / resistor) is seeded near
		// its chip (nearest shared-net fixed pad) so it clusters there; a USER-FACING
		// part (LED / button) is NOT net-hugged — it should stay where it is visible
		// / accessible, so it just spirals out from its current position.
		seedX, seedY := cx0, cy0
		if kinds[i] == cpSatellite {
			if sx, sy, ok := netSeed(c, cx0, cy0); ok {
				seedX, seedY = sx, sy
			}
		}
		var best *[2]float64
		for rad := 0.0; rad <= 2200 && best == nil; rad += 25 {
			steps := 1
			if rad > 0 {
				steps = 24
			}
			for s := 0; s < steps; s++ {
				ang := float64(s) * math.Pi / 12
				px, py := seedX+rad*math.Cos(ang), seedY+rad*math.Sin(ang)
				r := cpRect{px - hw - m, py - hh - m, px + hw + m, py + hh + m}
				if !inside(r) {
					continue
				}
				if !clashFixed(r, c.layer) {
					best = &[2]float64{px, py}
					break
				}
			}
		}
		if best == nil {
			diags = append(diags, apDiag{Designator: c.designator, Reason: "satellite:no-fit"})
			continue
		}
		px, py := best[0], best[1]
		addFixed(cpRect{px - hw - m, py - hh - m, px + hw + m, py + hh + m}, c.layer)
		dx, dy := px-cx0, py-cy0
		if math.Abs(dx) > 1 || math.Abs(dy) > 1 {
			moves = append(moves, apMove{ID: c.id, Designator: c.designator, NewX: round1(c.x + dx), NewY: round1(c.y + dy), Edge: kinds[i].String()})
		}
	}
	return moves, diags
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// cpDeviceName returns the string classifyCP pattern-matches on. A PLACED part's
// `name` is frequently the UNRESOLVED EasyEDA template "={Manufacturer Part}"
// (confirmed on the ceshi board — every part reported it), which makes the
// module/connector NAME regexes blind (e.g. an ESP32-S3-WROOM-1 module fails the
// `wroom` test and drops to the pin-count fallback → misclassified as a main chip
// instead of an edge part). So prefer the real manufacturerId; fall back to `name`
// only when manufacturerId is absent AND name isn't itself a "={…}" template.
func cpDeviceName(cm map[string]any) string {
	if mpn := strings.TrimSpace(asString(cm["manufacturerId"])); mpn != "" {
		return mpn
	}
	if n := strings.TrimSpace(asString(cm["name"])); !strings.HasPrefix(n, "={") {
		return n
	}
	return ""
}

// parseCpComps parses pcb.components.list into cpComps (apComp + device name +
// layer). A PCB component's identifying string is its device `name` (no footprint
// name is exposed); the layer is TOP=1 / BOTTOM=2.
func parseCpComps(result map[string]any) []cpComp {
	base := parseApComps(result)
	byID := map[string]cpComp{}
	raw, _ := result["components"].([]any)
	for _, ri := range raw {
		cm, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		id := asString(cm["primitiveId"])
		layer := int(asFloat(cm["layer"]))
		if layer == 0 {
			layer = 1
		}
		byID[id] = cpComp{footprint: cpDeviceName(cm), layer: layer}
	}
	out := make([]cpComp, 0, len(base))
	for _, b := range base {
		extra := byID[b.id]
		out = append(out, cpComp{apComp: b, footprint: extra.footprint, layer: extra.layer})
	}
	return out
}

// readCpHoles reads mounting-hole cutouts (fills on the MULTI layer, id 12 — where
// `pcb slot` puts board cutouts) and reduces each to a center + radius obstacle.
// Best-effort: a fill without readable points is skipped.
func readCpHoles(cfg *appConfig, window string) []cpHole {
	res, err := requestAction(cfg, "pcb.fill.list", window, nil)
	if err != nil || res == nil {
		return nil
	}
	fills, _ := res.Result["fills"].([]any)
	var out []cpHole
	for _, fi := range fills {
		fm, ok := fi.(map[string]any)
		if !ok || int(asFloat(fm["layer"])) != 12 {
			continue
		}
		pts, _ := fm["points"].([]any)
		if len(pts) < 3 {
			continue
		}
		minX, minY := math.Inf(1), math.Inf(1)
		maxX, maxY := math.Inf(-1), math.Inf(-1)
		for _, pi := range pts {
			p, ok := pi.([]any)
			if !ok || len(p) < 2 {
				continue
			}
			x, y := asFloat(p[0]), asFloat(p[1])
			minX, minY = math.Min(minX, x), math.Min(minY, y)
			maxX, maxY = math.Max(maxX, x), math.Max(maxY, y)
		}
		if math.IsInf(minX, 1) {
			continue
		}
		cx, cy := (minX+maxX)/2, (minY+maxY)/2
		// clearance radius = hole radius + washer margin (M3 head ≈ R118 mil)
		r := math.Max((maxX-minX)/2, (maxY-minY)/2) + 60
		out = append(out, cpHole{x: cx, y: cy, r: r})
	}
	return out
}
