package app

import (
	"math"
	"sort"
)

// ── sch autoconnect: pin-aware deterministic connect planner ────────────────
//
// `schematic.power.connect_pin` already solves the low-level safety problem
// (pin → short wire → flag/netport, so a netflag never sits on a bare pin and
// trips DRC). But it still makes the CALLER pick `direction` and `offset`, which
// turns layout quality into AI/manual judgment.
//
// `sch autoconnect` removes that judgment: it pulls the real rendered geometry
// (part bboxes, pin coordinates, existing flag/port/label bboxes), enumerates
// every (direction × offset) candidate, scores each with a PURE deterministic
// cost function, picks the lowest-cost one, and delegates the actual mutation to
// the existing `connect_pin` primitive. Mirrors `analyzeLayout` in
// cmd_sch_layout.go — pure geometry in Go, unit-testable, no side effects in the
// scorer. See issue #24.

// ── tunable rules ───────────────────────────────────────────────────────────

// autoconnectRules are the planner knobs. Mirrors the `rules` block of the spec
// JSON. Offsets/gaps are in schematic units (the same space connect_pin's
// `offset` and the pin/bbox coordinates live in).
type autoconnectRules struct {
	AvoidTitleBlock bool    `json:"avoidTitleBlock"`
	AvoidPinFanout  bool    `json:"avoidPinFanout"`
	StaggerLabels   bool    `json:"staggerLabels"`
	OffsetMin       float64 `json:"offsetMin"`
	OffsetMax       float64 `json:"offsetMax"`
	OffsetStep      float64 `json:"offsetStep"`
	MinLabelGap     float64 `json:"minLabelGap"`
}

// defaultAutoconnectRules matches the spec defaults documented in issue #24.
func defaultAutoconnectRules() autoconnectRules {
	return autoconnectRules{
		AvoidTitleBlock: true,
		AvoidPinFanout:  true,
		StaggerLabels:   true,
		OffsetMin:       18,
		OffsetMax:       80,
		OffsetStep:      6,
		MinLabelGap:     12,
	}
}

// ── scene geometry ──────────────────────────────────────────────────────────

// acPin is a pin in the scene: its coordinate plus the owning part's identity
// and bbox (for outward-side reasoning). Pins with no owner bbox still
// participate in crossing checks.
type acPin struct {
	X, Y       float64
	Designator string
	PinNumber  string
	PinName    string
	OwnerBBox  *layoutBBox
	// Net is the pin's CURRENT authoritative net (from schematic.components.list
	// --include-pins). Empty means "floating" when NetKnown is true; NetKnown is
	// false when the netlist wasn't available, so idempotency checks can't run and
	// must fall back to unconditional connect. See issue #50.
	Net      string
	NetKnown bool
}

// acComponent is a part known to the scene, whether or not its pins made it in.
// When the scene is built with --all-pages, parts on non-active pages still appear
// here (by designator) but have HasPins=false because the EDA pin lookup only
// returns pins for the active page. PageUuid/PageName are populated when the
// extension supplies them; empty otherwise. This lets resolvePinCoord tell
// "placed on another page" apart from "truly not placed / pin typo".
type acComponent struct {
	Designator string
	HasPins    bool
	PageUuid   string
	PageName   string
}

// wireSegment is one existing schematic wire segment (a single polyline edge),
// tagged with its net when known. autoconnect uses these to HARD-REJECT any
// candidate stub that would touch a foreign-net wire — EasyEDA merges nets at an
// endpoint-on-wire junction, a silent short the post-hoc DRC can't catch. See #64.
type wireSegment struct {
	X0, Y0, X1, Y1 float64
	Net            string // "" when the wire carries no resolvable net name
}

// acScene is the full geometric context one autoconnect run reasons against.
// Flags grows as connections are placed so later labels stagger off earlier ones.
type acScene struct {
	Parts                 []layoutBBox  // real part bboxes (componentType "part")
	Pins                  []acPin       // every pin across all parts
	Flags                 []layoutBBox  // existing netflag/netport/netlabel bboxes
	Wires                 []wireSegment // existing wire segments (issue #64)
	Components            []acComponent // every part seen (by designator), even pin-less off-page ones
	TitleBlock            *layoutBBox   // derived keep-out (nil if not applied)
	TitleBlockProvisional bool          // true when no sheet bbox was found (keep-out NOT geometrically applied)
	// AmbiguousDesignators are designators the connector flagged as colliding
	// across pages (issue #136): their pin→net attribution is untrustworthy, so
	// their pins arrive with net=null (treated as new) and the report must say why.
	AmbiguousDesignators []string
}

// ── candidate + scoring ─────────────────────────────────────────────────────

type acPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// acReason is one signed cost contribution (penalty > 0, bonus < 0).
type acReason struct {
	Cost float64 `json:"cost"`
	Desc string  `json:"desc"`
}

// acCandidate is one scored (direction, offset) option.
type acCandidate struct {
	Direction string     `json:"direction"`
	Offset    float64    `json:"offset"`
	EndPoint  acPoint    `json:"endPoint"`
	Score     float64    `json:"score"`
	Reasons   []acReason `json:"reasons,omitempty"`
}

// Cost-table constants (issue #24). Kept as named constants so the scorer reads
// like the table and the unit tests can assert exact figures.
const (
	// costHardReject is a sentinel far above any reachable sum of penalties +
	// offset cost, so a candidate carrying it is effectively UNUSABLE — the planner
	// only falls back to it when EVERY option is a hard reject. Used for hazards
	// EasyEDA would silently turn into a wrong connection (endpoint/path on a
	// foreign-net wire, stub crossing a non-target pin). See issue #64.
	costHardReject  = 1e9
	costPartOverlap = 10000 // endpoint/label bbox overlaps a real part bbox
	// (title-block intrusion is now a HARD reject, not a soft cost — see scoreCandidate, issue #147)
	// costPinCross is a HARD reject (issue #64 rec 2): a stub crossing a non-target
	// pin gets trimmed+connected by EasyEDA, and the post-hoc wire-over-pin rule
	// exempts endpoints on pins, so the short goes unnoticed. Never a soft penalty.
	costPinCross = costHardReject
	// costWireTouch is a HARD reject (issue #64 rec 1): a candidate stub whose
	// endpoint or path touches an existing foreign-net wire merges the two nets at
	// the junction. Never a soft penalty.
	costWireTouch     = costHardReject
	costFlagCollision = 1000 // endpoint/label collides with an existing flag/port/label
	costThroughPart   = 500  // stub passes through another component bbox
	costFanoutChannel = 100  // too close to a preserved pin-fanout channel
	costOffsetPerUnit = 0.1  // +offset * 0.1 — prefer shorter stubs
	bonusOutwardSide  = -20  // direction matches the pin's outward side
	bonusKindDefault  = -10  // direction matches the kind default (GND down / power up / port outward)
	// Nominal marker box half-extents (issue #148 Phase-2): a placed netflag/netport
	// is ~11 units TALL and ~24 wide (real getPrimitivesBBox readback: netport 11×31,
	// netflag 10×21), NOT the old 8×8 square. The height is what makes the scorer
	// auto-stagger markers on tight (10-unit-pitch) parallel pins — an 8×8 box never
	// overlapped its neighbour at 10 pitch, so stagger never fired. Kept a shade
	// under the real width so the box doesn't reach back to a marker's OWN owner part
	// (endpoint sits ≥18 from the pin; 12 < 18) and over-reject valid placements.
	acLabelHalfW = 12.0
	acLabelHalfH = 5.5
	acCoordEps        = 0.01 // coordinate-equality tolerance
	acOverlapEps      = 1e-6 // positive-length threshold for interval/area overlap
)

// endpointFor computes where connect_pin will land the stub end for a given
// direction/offset. MUST match the connector's switch (extension/src/actions.ts,
// schematicPowerConnectPin): y-DOWN coords — 'up' decreases y, 'down' increases
// y — so the planned geometry equals the geometry connect_pin actually creates.
// acSchGrid mirrors the connector's SCH_GRID: EasyEDA Pro snaps a created
// netflag/netport's connection pin to a 5-unit grid, and connect_pin aligns the
// stub endpoint to the same grid so the two coincide. The planner MUST snap too —
// scoring an un-snapped endpoint means the geometry checks run on coordinates the
// board will never hold. That cost a real short: a stub planned to (545,272)
// scored "clear" of a foreign-net wire lying at y=270, then landed at (545,270)
// — ON that wire — merging USB_DP into the CC1 net. 5, not 10: many footprints
// have pins on the odd 5-grid, and a 10-snap would pull the endpoint off the pin
// axis into a diagonal stub.
const acSchGrid = 5

func acSnapGrid(v float64) float64 { return math.Round(v/acSchGrid) * acSchGrid }

// endpointFor returns the stub's far end. Only the coordinate ALONG the stub is
// snapped; the perpendicular one stays exactly on the pin, keeping the stub
// orthogonal (a diagonal stub fails to create).
func endpointFor(pinX, pinY, offset float64, dir string) (x, y float64) {
	switch dir {
	case "up":
		return pinX, acSnapGrid(pinY - offset)
	case "down":
		return pinX, acSnapGrid(pinY + offset)
	case "left":
		return acSnapGrid(pinX - offset), pinY
	case "right":
		return acSnapGrid(pinX + offset), pinY
	}
	return pinX, pinY
}

// kindDefaultDirection mirrors the connector's defaultDirection(kind): power up,
// grounds down, in-ports left, out/bi-ports right. Input is the CANONICAL kind.
func kindDefaultDirection(canonicalKind string) string {
	switch canonicalKind {
	case "power":
		return "up"
	case "net_port_in":
		return "left"
	case "net_port_out", "net_port_bi":
		return "right"
	default: // ground / analog_ground / protective_ground / protect_ground / unknown
		return "down"
	}
}

// outwardDirection is the direction that moves the endpoint AWAY from the owning
// part's bbox center — the natural side to route a pin's flag. Empty when the
// owner bbox is unknown.
func outwardDirection(pin acPin) string {
	if pin.OwnerBBox == nil {
		return ""
	}
	cx := (pin.OwnerBBox.MinX + pin.OwnerBBox.MaxX) / 2
	cy := (pin.OwnerBBox.MinY + pin.OwnerBBox.MaxY) / 2
	dx := pin.X - cx
	dy := pin.Y - cy
	if math.Abs(dx) >= math.Abs(dy) {
		if dx >= 0 {
			return "right"
		}
		return "left"
	}
	// y-DOWN: a pin BELOW center (larger y) routes outward as 'down' (y+offset).
	if dy >= 0 {
		return "down"
	}
	return "up"
}

// labelBox is the nominal extent of the flag/label that will sit at the endpoint
// (issue #148 Phase-2: a real marker box, ~24×11, so the scorer staggers markers
// on tight parallel pins instead of stacking them).
func labelBox(x, y float64) layoutBBox {
	return layoutBBox{
		MinX: x - acLabelHalfW, MinY: y - acLabelHalfH,
		MaxX: x + acLabelHalfW, MaxY: y + acLabelHalfH,
	}
}

// segHitsRect reports whether an axis-aligned segment from (x0,y0) to (x1,y1)
// passes through the INTERIOR of rect (positive-length overlap). A segment that
// merely runs along an edge (e.g. a stub leaving a pin on the part boundary)
// does not count — that's what lets an outward stub avoid flagging its own owner.
func segHitsRect(x0, y0, x1, y1 float64, rect layoutBBox) bool {
	if x0 == x1 { // vertical stub
		if !(rect.MinX < x0 && x0 < rect.MaxX) {
			return false
		}
		lo, hi := math.Min(y0, y1), math.Max(y0, y1)
		return math.Min(hi, rect.MaxY)-math.Max(lo, rect.MinY) > acOverlapEps
	}
	// horizontal stub
	if !(rect.MinY < y0 && y0 < rect.MaxY) {
		return false
	}
	lo, hi := math.Min(x0, x1), math.Max(x0, x1)
	return math.Min(hi, rect.MaxX)-math.Max(lo, rect.MinX) > acOverlapEps
}

// pinOnSegment reports whether point (px,py) lies strictly between the endpoints
// of an axis-aligned segment (i.e. the stub crosses that pin).
func pinOnSegment(x0, y0, x1, y1, px, py float64) bool {
	if x0 == x1 { // vertical
		if math.Abs(px-x0) > acCoordEps {
			return false
		}
		lo, hi := math.Min(y0, y1), math.Max(y0, y1)
		return py > lo+acCoordEps && py < hi-acCoordEps
	}
	// horizontal
	if math.Abs(py-y0) > acCoordEps {
		return false
	}
	lo, hi := math.Min(x0, x1), math.Max(x0, x1)
	return px > lo+acCoordEps && px < hi-acCoordEps
}

// pointSegDist is the distance from (px,py) to an axis-aligned segment.
func pointSegDist(x0, y0, x1, y1, px, py float64) float64 {
	if x0 == x1 { // vertical
		lo, hi := math.Min(y0, y1), math.Max(y0, y1)
		cy := math.Max(lo, math.Min(hi, py))
		return math.Hypot(px-x0, py-cy)
	}
	lo, hi := math.Min(x0, x1), math.Max(x0, x1)
	cx := math.Max(lo, math.Min(hi, px))
	return math.Hypot(px-cx, py-y0)
}

// boxesOverlap is true when two bboxes share positive area.
func boxesOverlap(a, b layoutBBox) bool {
	ox := math.Min(a.MaxX, b.MaxX) - math.Max(a.MinX, b.MinX)
	oy := math.Min(a.MaxY, b.MaxY) - math.Max(a.MinY, b.MinY)
	return ox > acOverlapEps && oy > acOverlapEps
}

// orient2D is the signed area (cross product) of (b-a)×(c-a): >0 left turn,
// <0 right turn, ~0 collinear. Used by the general segment-touch test so a stub
// can be checked against an arbitrarily-oriented existing wire.
func orient2D(ax, ay, bx, by, cx, cy float64) float64 {
	return (bx-ax)*(cy-ay) - (by-ay)*(cx-ax)
}

// pointOnSeg reports whether (px,py) lies on segment (x0,y0)-(x1,y1), endpoints
// INCLUDED. Any contact counts, because EasyEDA merges nets wherever a stub end
// or path meets a wire (junction), not just at a proper interior crossing.
func pointOnSeg(px, py, x0, y0, x1, y1 float64) bool {
	if math.Abs(orient2D(x0, y0, x1, y1, px, py)) > acCoordEps*math.Max(1, math.Hypot(x1-x0, y1-y0)) {
		return false
	}
	return px >= math.Min(x0, x1)-acCoordEps && px <= math.Max(x0, x1)+acCoordEps &&
		py >= math.Min(y0, y1)-acCoordEps && py <= math.Max(y0, y1)+acCoordEps
}

// segmentsTouch reports whether segments A(a0→a1) and B(b0→b1) share ANY point —
// a proper crossing, a shared/touching endpoint, or a collinear overlap. This is
// deliberately inclusive: for wire-merge hazard detection any contact is a short.
func segmentsTouch(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1 float64) bool {
	d1 := orient2D(bx0, by0, bx1, by1, ax0, ay0)
	d2 := orient2D(bx0, by0, bx1, by1, ax1, ay1)
	d3 := orient2D(ax0, ay0, ax1, ay1, bx0, by0)
	d4 := orient2D(ax0, ay0, ax1, ay1, bx1, by1)
	if ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
		((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0)) {
		return true // proper crossing
	}
	// Collinear / endpoint-touch cases: any endpoint lying on the other segment.
	return pointOnSeg(ax0, ay0, bx0, by0, bx1, by1) ||
		pointOnSeg(ax1, ay1, bx0, by0, bx1, by1) ||
		pointOnSeg(bx0, by0, ax0, ay0, ax1, ay1) ||
		pointOnSeg(bx1, by1, ax0, ay0, ax1, ay1)
}

// stubTouchesForeignWire reports whether a candidate stub (target pin → endpoint)
// touches any existing wire that would merge into a FOREIGN net. A wire already
// on the SAME target net is skipped — connecting to it is the whole point. Wires
// with no resolvable net ("") are treated as foreign (unknown → conservative hard
// reject), since a silent cross-net merge is the worse outcome. Degenerate
// zero-length wires are ignored. See issue #64.
func stubTouchesForeignWire(pinX, pinY, endX, endY float64, targetNet string, wires []wireSegment) bool {
	// Trim the stub start a hair away from the pin. A foreign wire that touches
	// ONLY at the pin coordinate is a PRE-EXISTING condition (the pin already sits
	// on that net) — the idempotency net check classifies that as a conflict, so it
	// must not hard-reject every direction here. We only care about contact along
	// the rest of the stub, including its far endpoint.
	dx, dy := endX-pinX, endY-pinY
	length := math.Hypot(dx, dy)
	if length < acCoordEps {
		return false // degenerate stub
	}
	trimX := pinX + dx/length*(acCoordEps*4)
	trimY := pinY + dy/length*(acCoordEps*4)
	for _, w := range wires {
		if w.Net != "" && w.Net == targetNet {
			continue // same net — legitimate connection target, not a hazard
		}
		if math.Abs(w.X1-w.X0) < acCoordEps && math.Abs(w.Y1-w.Y0) < acCoordEps {
			continue // degenerate zero-length wire
		}
		if segmentsTouch(trimX, trimY, endX, endY, w.X0, w.Y0, w.X1, w.Y1) {
			return true
		}
	}
	return false
}

// scoreCandidate is the PURE deterministic core: given a pin, a direction, an
// offset, the canonical kind, the scene, and the rules, return the scored
// candidate with its signed cost breakdown. No I/O, no mutation — the whole
// reason this is unit-testable.
func scoreCandidate(pin acPin, dir string, offset float64, canonicalKind, targetNet string, scene acScene, rules autoconnectRules) acCandidate {
	endX, endY := endpointFor(pin.X, pin.Y, offset, dir)
	lbl := labelBox(endX, endY)
	var reasons []acReason

	// +10000 endpoint/label overlaps a real part bbox.
	for _, p := range scene.Parts {
		if boxesOverlap(lbl, p) {
			reasons = append(reasons, acReason{costPartOverlap, "label overlaps a part bbox"})
			break
		}
	}

	// HARD REJECT: endpoint/label enters the title-block keep-out (issue #147). The
	// title block (图签/明细表) is a hard boundary, not a soft cost — a netport dropped
	// on it passes every existing gate (layout-lint is part-only, the electrical
	// check is geometry-blind). Scoring it like costPartOverlap let it WIN when every
	// other direction was itself hard-rejected (all pins toward the sheet corner). As
	// a hard reject it steers to a safe direction, or — when every candidate enters
	// the keep-out — makes runAutoconnect refuse to place rather than intrude it.
	if rules.AvoidTitleBlock && scene.TitleBlock != nil && boxesOverlap(lbl, *scene.TitleBlock) {
		reasons = append(reasons, acReason{costHardReject, "label enters title-block keep-out (hard reject)"})
	}

	// HARD REJECT: stub crosses a non-target pin (issue #64). EasyEDA trims and
	// connects the stub at that pin, and the wire-over-pin DRC exempts endpoints on
	// pins, so the short is invisible after the fact. Never a soft penalty.
	for _, op := range scene.Pins {
		if math.Abs(op.X-pin.X) < acCoordEps && math.Abs(op.Y-pin.Y) < acCoordEps {
			continue // the target pin itself
		}
		// pinOnSegment tests the segment's INTERIOR (endpoints excluded), so it misses
		// a stub that STOPS exactly on a neighbouring pin — which is just as shorted,
		// and which grid snapping makes common: pins sit on the grid, so a snapped
		// endpoint lands on one whenever the pin pitch is near the stub offset. Real
		// case: XL1509's pins 1-4 are 20 apart at x=645; pin2's "up" stub (offset 18 →
		// snapped to 390) ended ON pin3, whose own "up" stub ended ON pin4, chaining
		// three nets (C11_N3 + +5V + GND) into one wire tree.
		endsOnPin := math.Abs(op.X-endX) < acCoordEps && math.Abs(op.Y-endY) < acCoordEps
		if endsOnPin || pinOnSegment(pin.X, pin.Y, endX, endY, op.X, op.Y) {
			reasons = append(reasons, acReason{costPinCross, "stub crosses a non-target pin (hard reject)"})
			break
		}
	}

	// HARD REJECT: stub endpoint or path touches an existing foreign-net wire
	// (issue #64). EasyEDA merges the two nets at the junction — a silent short.
	if stubTouchesForeignWire(pin.X, pin.Y, endX, endY, targetNet, scene.Wires) {
		reasons = append(reasons, acReason{costWireTouch, "stub touches an existing (foreign-net) wire (hard reject)"})
	}

	// +1000 endpoint/label collides with an existing flag/port/label.
	for _, f := range scene.Flags {
		if boxesOverlap(lbl, f) {
			reasons = append(reasons, acReason{costFlagCollision, "label collides with an existing flag/port/label"})
			break
		}
	}

	// +500 stub passes through another component bbox.
	for _, p := range scene.Parts {
		if segHitsRect(pin.X, pin.Y, endX, endY, p) {
			reasons = append(reasons, acReason{costThroughPart, "stub passes through a component bbox"})
			break
		}
	}

	// +100 too close to a preserved fanout channel (a nearby non-target pin the
	// stub runs alongside without crossing).
	if rules.AvoidPinFanout {
		for _, op := range scene.Pins {
			if math.Abs(op.X-pin.X) < acCoordEps && math.Abs(op.Y-pin.Y) < acCoordEps {
				continue
			}
			if pinOnSegment(pin.X, pin.Y, endX, endY, op.X, op.Y) {
				continue // already counted as a crossing
			}
			if d := pointSegDist(pin.X, pin.Y, endX, endY, op.X, op.Y); d > acCoordEps && d < rules.MinLabelGap {
				reasons = append(reasons, acReason{costFanoutChannel, "stub runs close to a pin fanout channel"})
				break
			}
		}
	}

	// +offset * 0.1 — prefer shorter stubs.
	reasons = append(reasons, acReason{round2(offset * costOffsetPerUnit), "offset cost"})

	// -20 direction matches the pin's outward side.
	if outwardDirection(pin) == dir {
		reasons = append(reasons, acReason{bonusOutwardSide, "matches pin outward side"})
	}
	// -10 direction matches the kind default.
	if kindDefaultDirection(canonicalKind) == dir {
		reasons = append(reasons, acReason{bonusKindDefault, "matches kind default direction"})
	}

	var score float64
	for _, r := range reasons {
		score += r.Cost
	}
	return acCandidate{
		Direction: dir,
		Offset:    offset,
		EndPoint:  acPoint{X: round2(endX), Y: round2(endY)},
		Score:     round2(score),
		Reasons:   reasons,
	}
}

// candidateOffsets enumerates offsets from OffsetMin to OffsetMax stepping by
// OffsetStep (inclusive of OffsetMax). Deterministic, ascending.
func candidateOffsets(rules autoconnectRules) []float64 {
	min, max, step := rules.OffsetMin, rules.OffsetMax, rules.OffsetStep
	if step <= 0 {
		step = 6
	}
	if max < min {
		max = min
	}
	var out []float64
	for o := min; o <= max+acOverlapEps; o += step {
		out = append(out, round2(o))
	}
	if len(out) == 0 {
		out = []float64{min}
	}
	return out
}

var acDirections = []string{"up", "down", "left", "right"}

// planConnection enumerates every (direction × offset) candidate, scores them,
// and returns them sorted best-first. Deterministic tie-break: score asc, then
// direction lexical (down<left<right<up), then offset asc — so the same scene +
// spec always yields the same selection (acceptance: "deterministic result").
func planConnection(pin acPin, canonicalKind, targetNet string, scene acScene, rules autoconnectRules) []acCandidate {
	offsets := candidateOffsets(rules)
	all := make([]acCandidate, 0, len(acDirections)*len(offsets))
	for _, dir := range acDirections {
		for _, off := range offsets {
			all = append(all, scoreCandidate(pin, dir, off, canonicalKind, targetNet, scene, rules))
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score < all[j].Score
		}
		if all[i].Direction != all[j].Direction {
			return all[i].Direction < all[j].Direction
		}
		return all[i].Offset < all[j].Offset
	})
	return all
}

// candidateHardRejected reports whether a candidate carries a hard-reject cost
// (pin-cross or foreign-wire touch, issue #64). When the BEST candidate is still
// hard-rejected, every direction/offset was a hazard — the caller must refuse to
// mutate rather than place a stub it knows would short two nets.
func candidateHardRejected(c acCandidate) bool {
	for _, r := range c.Reasons {
		if r.Cost >= costHardReject {
			return true
		}
	}
	return false
}

// ── idempotency: three-state pin/net decision (issue #50) ───────────────────

// acConnState is the decision for one connection BEFORE any mutation, so a repeat
// run over the same spec is idempotent instead of stacking duplicate flags+wires.
type acConnState string

const (
	// acStateNew: the pin has no net yet (or we can't tell) → plan + connect.
	acStateNew acConnState = "new"
	// acStateAlreadyConnected: the pin is already on the spec's target net → skip.
	acStateAlreadyConnected acConnState = "already-connected"
	// acStateConflict: the pin is on a DIFFERENT net → error unless --replace.
	acStateConflict acConnState = "conflict"
)

// decideConnState is the PURE idempotency core: given the pin's current net
// (currentNet, only meaningful when netKnown is true) and the spec's target net,
// classify the connection. When the current net is unknown (netlist unavailable)
// we can't prove idempotency, so we fall back to "new" and let connect_pin run —
// preserving the pre-#50 behavior rather than silently skipping.
func decideConnState(currentNet string, netKnown bool, targetNet string) acConnState {
	if !netKnown || currentNet == "" {
		return acStateNew
	}
	if currentNet == targetNet {
		return acStateAlreadyConnected
	}
	return acStateConflict
}

// dominantReason picks the most expensive penalty for a rejected candidate's
// human summary; falls back to a generic note when nothing was penalized.
func dominantReason(c acCandidate) string {
	best := ""
	var bestCost float64
	for _, r := range c.Reasons {
		if r.Cost > bestCost {
			bestCost = r.Cost
			best = r.Desc
		}
	}
	if best == "" {
		return "higher total cost (longer offset / non-default direction)"
	}
	return best
}
