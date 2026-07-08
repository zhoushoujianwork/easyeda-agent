package app

import (
	"fmt"
	"math"
	"sort"
)

// ── sch autolayout: module-aware deterministic placement planner ─────────────
//
// `sch layout-lint` already gives the place→verify→adjust loop a quantified
// overlap input, and `sch autoconnect` (issue #24) showed how to turn a layout
// judgment call into a PURE deterministic geometry decision over real bboxes.
// `sch autolayout` is the larger sibling (issue #25): it solves MODULE-LEVEL
// placement — not electrical routing — by reading a spec (page, sheet, modules
// with zone/core/parts, rules), partitioning the usable canvas into named zones,
// placing each module's core IC near its zone center, fanning peripherals around
// the core while preserving pin-fanout channels and the A4 title-block keep-out,
// and retrying candidate positions on collision.
//
// The planner core here (planAutolayout + the geometry helpers) is PURE and
// deterministic: same modules + parts + sheet + rules always yields identical
// coordinates, so it is unit-testable with no connector. The I/O side (pull real
// geometry, --apply via schematic.component.modify) lives in
// cmd_sch_autolayout_run.go. It reuses layoutBBox / boxesOverlap / rectGap /
// titleBlockKeepout / analyzeLayout rather than re-deriving them.

// ── tunable rules ───────────────────────────────────────────────────────────

// autolayoutRules are the planner knobs. Mirrors the `rules` block of the spec
// JSON. Gaps are in schematic units (the same space the pin/bbox coordinates
// live in).
type autolayoutRules struct {
	AvoidTitleBlock   bool    // hard keep-out: never land a part in the title block
	PreservePinFanout bool    // keep peripherals out of a core pin's lead-out lane
	ModuleGap         float64 // nominal inter-module breathing room (informational/zone sizing)
	RouteChannelGap   float64 // length of a preserved pin-fanout channel
	PreferVertical    bool    // try above/below before left/right for dense peripherals
	PartGap           float64 // minimum edge-to-edge gap between two parts
}

// defaultAutolayoutRules matches the spec defaults documented in issue #25.
func defaultAutolayoutRules() autolayoutRules {
	return autolayoutRules{
		AvoidTitleBlock:   true,
		PreservePinFanout: true,
		ModuleGap:         80,
		RouteChannelGap:   40,
		PreferVertical:    true,
		PartGap:           20,
	}
}

// alMaxRing bounds how far out the candidate search spirals before a part is
// declared unplaceable. 12 rings × 8 directions = 96 candidates per part.
const alMaxRing = 12

// ── scene inputs ────────────────────────────────────────────────────────────

// alPinPt is one core pin coordinate (for fanout-lane derivation).
type alPinPt struct{ X, Y float64 }

// alPart is one placed device the planner can MOVE (v1 does not create parts).
// AnchorX/Y is the component's stored x/y (what schematic.component.modify sets);
// BBox is the rendered extent. Moving translates anchor and bbox by the same
// vector, so the planner reasons in bbox-center space and converts back to an
// anchor at the end (makePlacement).
type alPart struct {
	Designator  string
	PrimitiveID string
	AnchorX     float64
	AnchorY     float64
	Rotation    float64
	BBox        layoutBBox
	HasBBox     bool
	Pins        []alPinPt
}

// alModuleSpec is one module to place: a named zone, a core IC, and the parts
// (including the core) that belong to it.
type alModuleSpec struct {
	Name  string
	Zone  string
	Core  string
	Parts []string
}

// ── result shape (issue #25) ────────────────────────────────────────────────

type alPlacement struct {
	Designator  string  `json:"designator"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Rotation    float64 `json:"rotation"`
	Module      string  `json:"module"`
	PrimitiveID string  `json:"primitiveId,omitempty"`
	Retries     int     `json:"retries,omitempty"`
}

type alWarning struct {
	Module  string `json:"module"`
	Message string `json:"message"`
}

type alValidation struct {
	PartOverlaps      int `json:"partOverlaps"`
	TitleBlockHits    int `json:"titleBlockHits"`
	FanoutKeepoutHits int `json:"fanoutKeepoutHits"`
}

type alReport struct {
	OK                    bool          `json:"ok"`
	Page                  string        `json:"page,omitempty"`
	Placements            []alPlacement `json:"placements"`
	Warnings              []alWarning   `json:"warnings,omitempty"`
	Errors                []string      `json:"errors,omitempty"`
	Validation            alValidation  `json:"validation"`
	TitleBlockProvisional bool          `json:"titleBlockProvisional,omitempty"`
	Note                  string        `json:"note,omitempty"`
}

// ── geometry helpers ────────────────────────────────────────────────────────

func bboxSize(b layoutBBox) (w, h float64) { return b.MaxX - b.MinX, b.MaxY - b.MinY }

func bboxCenter(b layoutBBox) (x, y float64) { return (b.MinX + b.MaxX) / 2, (b.MinY + b.MaxY) / 2 }

// recenterBox returns b's same width/height translated so its center is (cx,cy).
func recenterBox(b layoutBBox, cx, cy float64) layoutBBox {
	w, h := bboxSize(b)
	return layoutBBox{MinX: cx - w/2, MinY: cy - h/2, MaxX: cx + w/2, MaxY: cy + h/2}
}

// boxInside reports whether inner is fully contained in outer.
func boxInside(inner, outer layoutBBox) bool {
	return inner.MinX >= outer.MinX && inner.MaxX <= outer.MaxX &&
		inner.MinY >= outer.MinY && inner.MaxY <= outer.MaxY
}

// unionBoxes is the fallback usable area when no sheet bbox is exposed: the
// extent of every placed part, padded out so there is room to spread them.
func unionBoxes(parts []alPart) layoutBBox {
	first := true
	var u layoutBBox
	for _, p := range parts {
		if !p.HasBBox {
			continue
		}
		if first {
			u = p.BBox
			first = false
			continue
		}
		u.MinX = math.Min(u.MinX, p.BBox.MinX)
		u.MinY = math.Min(u.MinY, p.BBox.MinY)
		u.MaxX = math.Max(u.MaxX, p.BBox.MaxX)
		u.MaxY = math.Max(u.MaxY, p.BBox.MaxY)
	}
	if first {
		return layoutBBox{MinX: 0, MinY: 0, MaxX: 1000, MaxY: 800}
	}
	padX := (u.MaxX-u.MinX)*0.25 + 60
	padY := (u.MaxY-u.MinY)*0.25 + 60
	return layoutBBox{MinX: u.MinX - padX, MinY: u.MinY - padY, MaxX: u.MaxX + padX, MaxY: u.MaxY + padY}
}

// zoneRect maps a named zone to its sub-rectangle of the usable area. Columns:
// left [0,1/3], center [1/3,2/3], right [2/3,1]. Rows (y-DOWN, top = smaller y):
// top [0,0.5], bottom [0.5,1]. Unknown/empty zone → center, full height.
func zoneRect(zone string, u layoutBBox) layoutBBox {
	w := u.MaxX - u.MinX
	h := u.MaxY - u.MinY
	colL := [2]float64{0, 1.0 / 3}
	colC := [2]float64{1.0 / 3, 2.0 / 3}
	colR := [2]float64{2.0 / 3, 1.0}
	rowT := [2]float64{0, 0.5}
	rowB := [2]float64{0.5, 1.0}
	full := [2]float64{0, 1.0}
	col, row := colC, full
	switch zone {
	case "left-top":
		col, row = colL, rowT
	case "left-bottom":
		col, row = colL, rowB
	case "left":
		col, row = colL, full
	case "center", "":
		col, row = colC, full
	case "center-top":
		col, row = colC, rowT
	case "center-bottom":
		col, row = colC, rowB
	case "right":
		col, row = colR, full
	case "right-top":
		col, row = colR, rowT
	case "right-bottom":
		col, row = colR, rowB
	case "top":
		col, row = full, rowT
	case "bottom":
		col, row = full, rowB
	}
	return layoutBBox{
		MinX: u.MinX + col[0]*w, MaxX: u.MinX + col[1]*w,
		MinY: u.MinY + row[0]*h, MaxY: u.MinY + row[1]*h,
	}
}

// alDir is a unit search direction (y-DOWN: dy=+1 is "below"/down on canvas).
type alDir struct{ dx, dy float64 }

// Vertical-first / horizontal-first orderings. Cardinals before diagonals so a
// dense column/row of peripherals stacks cleanly before spilling into corners.
var (
	alDirsVertical   = []alDir{{0, 1}, {0, -1}, {1, 0}, {-1, 0}, {1, 1}, {-1, 1}, {1, -1}, {-1, -1}}
	alDirsHorizontal = []alDir{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, 1}, {1, -1}, {-1, -1}}
)

// candidateCenters enumerates ring positions around (cx,cy) at radius r*step for
// r=1..maxRing, each ring sweeping the direction order. Deterministic.
func candidateCenters(cx, cy, step float64, preferVertical bool, maxRing int) []alPinPt {
	dirs := alDirsHorizontal
	if preferVertical {
		dirs = alDirsVertical
	}
	out := make([]alPinPt, 0, maxRing*len(dirs))
	for r := 1; r <= maxRing; r++ {
		for _, d := range dirs {
			out = append(out, alPinPt{X: cx + d.dx*step*float64(r), Y: cy + d.dy*step*float64(r)})
		}
	}
	return out
}

// alOutward is the direction a pin leads AWAY from its owning core's center.
func alOutward(px, py, cx, cy float64) string {
	dx, dy := px-cx, py-cy
	if math.Abs(dx) >= math.Abs(dy) {
		if dx >= 0 {
			return "right"
		}
		return "left"
	}
	if dy >= 0 {
		return "down"
	}
	return "up"
}

// coreFanoutLanes builds a thin keep-out rectangle extending outward from each
// core pin by channelLen — the channel a wire needs and a peripheral must not
// squat in. core.BBox/core.Pins are the ALREADY-PLACED (translated) geometry.
func coreFanoutLanes(core alPart, channelLen, halfWidth float64) []layoutBBox {
	cx, cy := bboxCenter(core.BBox)
	lanes := make([]layoutBBox, 0, len(core.Pins))
	for _, p := range core.Pins {
		switch alOutward(p.X, p.Y, cx, cy) {
		case "up":
			lanes = append(lanes, layoutBBox{MinX: p.X - halfWidth, MinY: p.Y - channelLen, MaxX: p.X + halfWidth, MaxY: p.Y})
		case "down":
			lanes = append(lanes, layoutBBox{MinX: p.X - halfWidth, MinY: p.Y, MaxX: p.X + halfWidth, MaxY: p.Y + channelLen})
		case "left":
			lanes = append(lanes, layoutBBox{MinX: p.X - channelLen, MinY: p.Y - halfWidth, MaxX: p.X, MaxY: p.Y + halfWidth})
		case "right":
			lanes = append(lanes, layoutBBox{MinX: p.X, MinY: p.Y - halfWidth, MaxX: p.X + channelLen, MaxY: p.Y + halfWidth})
		}
	}
	return lanes
}

// makePlacement converts a target bbox-CENTER into the part's new ANCHOR
// coordinate (modify sets the anchor). Moving translates anchor and bbox by the
// same vector, so newAnchor = oldAnchor + (targetCenter - oldBBoxCenter).
// schAnchorGrid is the schematic placement grid the final part ANCHOR must land
// on. EasyEDA snaps a netflag/netport's connection pin to a 5-unit grid, and
// symbol pins sit at multiples of 5 from the part anchor — so an off-grid
// anchor puts EVERY pin off-grid, and connect_pin stubs then fail outright
// (a horizontal stub's snapped endpoint goes diagonal → "Failed to create
// pin-stub wire") or leave the flag floating. Zone-centering math naturally
// produces fractional centers (825/4 = 206.25), which is how probe round #3
// hit this: 53/64 autoconnect failures on an autolayout-placed page.
const schAnchorGrid = 5

func snapAnchor(v float64) float64 { return math.Round(v/schAnchorGrid) * schAnchorGrid }

func makePlacement(p alPart, targetCX, targetCY float64, module string, retries int) alPlacement {
	ocx, ocy := bboxCenter(p.BBox)
	dx, dy := targetCX-ocx, targetCY-ocy
	return alPlacement{
		Designator:  p.Designator,
		X:           snapAnchor(p.AnchorX + dx),
		Y:           snapAnchor(p.AnchorY + dy),
		Rotation:    p.Rotation,
		Module:      module,
		PrimitiveID: p.PrimitiveID,
		Retries:     retries,
	}
}

// findSlot searches for the first collision-free center for box, starting at
// (cx,cy) then spiralling outward. Each predicate (nil = skip) vetoes a
// candidate; the first survivor wins. Returns the placed box, the retry index
// (0 = landed at the preferred center), and whether a slot was found.
func findSlot(box layoutBBox, cx, cy, step float64, preferVertical bool, collides, inBounds, hitsTitle, hitsLane func(layoutBBox) bool) (layoutBBox, int, bool) {
	tryAt := func(tx, ty float64) (layoutBBox, bool) {
		cand := recenterBox(box, tx, ty)
		if inBounds != nil && !inBounds(cand) {
			return cand, false
		}
		if hitsTitle != nil && hitsTitle(cand) {
			return cand, false
		}
		if hitsLane != nil && hitsLane(cand) {
			return cand, false
		}
		if collides != nil && collides(cand) {
			return cand, false
		}
		return cand, true
	}
	if cand, ok := tryAt(cx, cy); ok {
		return cand, 0, true
	}
	cands := candidateCenters(cx, cy, step, preferVertical, alMaxRing)
	for i, c := range cands {
		if cand, ok := tryAt(c.X, c.Y); ok {
			return cand, i + 1, true
		}
	}
	return box, len(cands), false
}

// planAutolayout is the PURE deterministic core. Given the modules (in spec
// order), the placed parts, the sheet bbox (nil → derive usable area + skip the
// title-block keep-out), and the rules, it returns the full placement report.
func planAutolayout(modules []alModuleSpec, parts []alPart, sheet *layoutBBox, rules autolayoutRules) alReport {
	rep := alReport{OK: true}

	byDes := make(map[string]alPart, len(parts))
	for _, p := range parts {
		byDes[p.Designator] = p
	}

	tb, prov := titleBlockKeepout(sheet)
	rep.TitleBlockProvisional = prov
	var usable layoutBBox
	if sheet != nil {
		usable = *sheet
	} else {
		usable = unionBoxes(parts)
		rep.Note = "no sheet bbox exposed — usable area derived from existing part extents; title-block keep-out NOT enforced"
	}

	var placed []layoutBBox      // obstacle boxes accumulated across all modules
	var placedComps []layoutComp // parallel, for the final overlap validation
	var coreLanes []layoutBBox   // preserved pin-fanout channels from placed cores

	collides := func(cand layoutBBox) bool {
		for _, o := range placed {
			if boxesOverlap(cand, o) || rectGap(cand, o) < rules.PartGap {
				return true
			}
		}
		return false
	}
	inBounds := func(cand layoutBBox) bool {
		if sheet == nil {
			return true
		}
		return boxInside(cand, usable)
	}
	hitsTitle := func(cand layoutBBox) bool {
		return rules.AvoidTitleBlock && tb != nil && boxesOverlap(cand, *tb)
	}
	hitsLane := func(cand layoutBBox) bool {
		if !rules.PreservePinFanout {
			return false
		}
		for _, l := range coreLanes {
			if boxesOverlap(cand, l) {
				return true
			}
		}
		return false
	}
	register := func(p alPart, box layoutBBox) {
		placed = append(placed, box)
		placedComps = append(placedComps, layoutComp{Designator: p.Designator, ID: p.PrimitiveID, BBox: &box})
	}

	for _, m := range modules {
		zone := zoneRect(m.Zone, usable)
		zcx, zcy := bboxCenter(zone)

		// ── core IC: anchor the module near its zone center ─────────────────
		if m.Core == "" {
			rep.Errors = append(rep.Errors, fmt.Sprintf("module %q has no core specified", m.Name))
			rep.OK = false
			continue
		}
		core, ok := byDes[m.Core]
		if !ok || !core.HasBBox {
			rep.Errors = append(rep.Errors, fmt.Sprintf("module %q core %q not found among placed parts (v1 moves existing parts only)", m.Name, m.Core))
			rep.OK = false
			continue
		}
		cw, ch := bboxSize(core.BBox)
		coreStep := math.Max(cw, ch) + rules.PartGap
		corePlaced, cRetries, cok := findSlot(core.BBox, zcx, zcy, coreStep, rules.PreferVertical, collides, inBounds, hitsTitle, nil)
		if !cok {
			rep.Errors = append(rep.Errors, fmt.Sprintf("module %q: could not place core %q without collision after %d candidate retries", m.Name, m.Core, alMaxRing*len(alDirsVertical)))
			rep.OK = false
			continue
		}
		ccx, ccy := bboxCenter(corePlaced)
		rep.Placements = append(rep.Placements, makePlacement(core, ccx, ccy, m.Name, cRetries))
		register(core, corePlaced)

		// Register this core's fanout lanes (pins translated to the new center)
		// so later peripherals (this module and beyond) keep them clear.
		if rules.PreservePinFanout && len(core.Pins) > 0 {
			ocx, ocy := bboxCenter(core.BBox)
			dx, dy := ccx-ocx, ccy-ocy
			moved := alPart{BBox: corePlaced}
			for _, p := range core.Pins {
				moved.Pins = append(moved.Pins, alPinPt{X: p.X + dx, Y: p.Y + dy})
			}
			coreLanes = append(coreLanes, coreFanoutLanes(moved, rules.RouteChannelGap, rules.PartGap/2)...)
		}

		// ── peripherals: deterministic order, ring out from the core ────────
		periphs := make([]string, 0, len(m.Parts))
		for _, d := range m.Parts {
			if d != m.Core {
				periphs = append(periphs, d)
			}
		}
		sort.Strings(periphs)
		for _, d := range periphs {
			pt, ok := byDes[d]
			if !ok || !pt.HasBBox {
				rep.Warnings = append(rep.Warnings, alWarning{Module: m.Name, Message: fmt.Sprintf("part %q not found among placed parts — skipped (v1 moves existing parts only)", d)})
				continue
			}
			pw, ph := bboxSize(pt.BBox)
			step := math.Max(cw, ch)/2 + math.Max(pw, ph)/2 + rules.PartGap
			box, retries, ok2 := findSlot(pt.BBox, ccx, ccy, step, rules.PreferVertical, collides, inBounds, hitsTitle, hitsLane)
			if !ok2 {
				// Last resort: relax the fanout-channel preference (still respect
				// collisions, bounds, and the title block) and warn the human.
				box, retries, ok2 = findSlot(pt.BBox, ccx, ccy, step, rules.PreferVertical, collides, inBounds, hitsTitle, nil)
				if ok2 {
					rep.Warnings = append(rep.Warnings, alWarning{Module: m.Name, Message: fmt.Sprintf("%s placed inside a pin-fanout lane (no clear slot otherwise)", d)})
					rep.Validation.FanoutKeepoutHits++
				}
			}
			if !ok2 {
				rep.Errors = append(rep.Errors, fmt.Sprintf("module %q: could not place peripheral %q after candidate retries", m.Name, d))
				rep.OK = false
				continue
			}
			pcx, pcy := bboxCenter(box)
			rep.Placements = append(rep.Placements, makePlacement(pt, pcx, pcy, m.Name, retries))
			register(pt, box)
		}
	}

	// ── validation summary ──────────────────────────────────────────────────
	overlapRep := analyzeLayout(placedComps, 0, -1) // minGap 0 → count true overlaps only
	rep.Validation.PartOverlaps = len(overlapRep.Overlaps)
	if rules.AvoidTitleBlock && tb != nil {
		for _, b := range placed {
			if boxesOverlap(b, *tb) {
				rep.Validation.TitleBlockHits++
			}
		}
	}
	if rep.Validation.PartOverlaps > 0 || rep.Validation.TitleBlockHits > 0 {
		rep.OK = false
	}
	return rep
}
