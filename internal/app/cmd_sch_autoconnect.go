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

// acScene is the full geometric context one autoconnect run reasons against.
// Flags grows as connections are placed so later labels stagger off earlier ones.
type acScene struct {
	Parts                 []layoutBBox  // real part bboxes (componentType "part")
	Pins                  []acPin       // every pin across all parts
	Flags                 []layoutBBox  // existing netflag/netport/netlabel bboxes
	Components            []acComponent // every part seen (by designator), even pin-less off-page ones
	TitleBlock            *layoutBBox   // derived keep-out (nil if not applied)
	TitleBlockProvisional bool          // true when no sheet bbox was found (keep-out NOT geometrically applied)
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
	costPartOverlap   = 10000 // endpoint/label bbox overlaps a real part bbox
	costTitleBlock    = 10000 // endpoint/label bbox enters the title-block keep-out
	costPinCross      = 5000  // stub crosses a non-target pin
	costFlagCollision = 1000  // endpoint/label collides with an existing flag/port/label
	costThroughPart   = 500   // stub passes through another component bbox
	costFanoutChannel = 100   // too close to a preserved pin-fanout channel
	costOffsetPerUnit = 0.1   // +offset * 0.1 — prefer shorter stubs
	bonusOutwardSide  = -20   // direction matches the pin's outward side
	bonusKindDefault  = -10   // direction matches the kind default (GND down / power up / port outward)
	acLabelHalfExtent = 4.0   // nominal half-size of a placed flag/label marker box (schematic units)
	acCoordEps        = 0.01  // coordinate-equality tolerance
	acOverlapEps      = 1e-6  // positive-length threshold for interval/area overlap
)

// endpointFor computes where connect_pin will land the stub end for a given
// direction/offset. MUST match the connector's switch (extension/src/actions.ts,
// schematicPowerConnectPin): y-DOWN coords — 'up' decreases y, 'down' increases
// y — so the planned geometry equals the geometry connect_pin actually creates.
func endpointFor(pinX, pinY, offset float64, dir string) (x, y float64) {
	switch dir {
	case "up":
		return pinX, pinY - offset
	case "down":
		return pinX, pinY + offset
	case "left":
		return pinX - offset, pinY
	case "right":
		return pinX + offset, pinY
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

// labelBox is the nominal extent of the flag/label that will sit at the endpoint.
func labelBox(x, y float64) layoutBBox {
	return layoutBBox{
		MinX: x - acLabelHalfExtent, MinY: y - acLabelHalfExtent,
		MaxX: x + acLabelHalfExtent, MaxY: y + acLabelHalfExtent,
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

// scoreCandidate is the PURE deterministic core: given a pin, a direction, an
// offset, the canonical kind, the scene, and the rules, return the scored
// candidate with its signed cost breakdown. No I/O, no mutation — the whole
// reason this is unit-testable.
func scoreCandidate(pin acPin, dir string, offset float64, canonicalKind string, scene acScene, rules autoconnectRules) acCandidate {
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

	// +10000 endpoint/label enters the title-block keep-out (only when applied).
	if rules.AvoidTitleBlock && scene.TitleBlock != nil && boxesOverlap(lbl, *scene.TitleBlock) {
		reasons = append(reasons, acReason{costTitleBlock, "label enters title-block keep-out"})
	}

	// +5000 stub crosses a non-target pin.
	for _, op := range scene.Pins {
		if math.Abs(op.X-pin.X) < acCoordEps && math.Abs(op.Y-pin.Y) < acCoordEps {
			continue // the target pin itself
		}
		if pinOnSegment(pin.X, pin.Y, endX, endY, op.X, op.Y) {
			reasons = append(reasons, acReason{costPinCross, "stub crosses a non-target pin"})
			break
		}
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
func planConnection(pin acPin, canonicalKind string, scene acScene, rules autoconnectRules) []acCandidate {
	offsets := candidateOffsets(rules)
	all := make([]acCandidate, 0, len(acDirections)*len(offsets))
	for _, dir := range acDirections {
		for _, off := range offsets {
			all = append(all, scoreCandidate(pin, dir, off, canonicalKind, scene, rules))
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
