package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// ── layout-lint: mechanical placement check ─────────────────────────────────
//
// The overlap problem reported from real use ("元件覆盖在一起") is fundamentally a
// missing-feedback problem: the agent placed components but had no ground truth
// for whether they collided. `sch layout-lint` is that ground truth — it pulls
// every component's rendered bbox and runs cheap pairwise geometry in Go, so the
// place→verify→adjust loop has a quantified input instead of an eyeball.

// layoutBBox is a component's rendered extent in schematic mm (y-up).
type layoutBBox struct {
	MinX float64 `json:"minX"`
	MinY float64 `json:"minY"`
	MaxX float64 `json:"maxX"`
	MaxY float64 `json:"maxY"`
}

// layoutPin is one pin's number + coordinate in schematic mm (y-up). Used by the
// pin-coincidence check: two pins from DIFFERENT components landing on the same
// point is an implicit short (any wire/stub through that point ties the two nets)
// that bbox-only overlap detection cannot see. See issue #63.
type layoutPin struct {
	Number string
	X      float64
	Y      float64
}

// layoutComp is the minimal per-component shape layout-lint reasons about.
type layoutComp struct {
	ID            string
	Designator    string
	ComponentType string // "part" | "sheet" | "netflag" | "netport" | … (from the connector)
	BBox          *layoutBBox
	Pins          []layoutPin
}

// schLayoutPartType is the componentType of a real placed device. Only these
// participate in placement geometry by default. The drawing sheet / title block
// (componentType "sheet") spans the whole page, so including it false-flags an
// overlap against nearly every component; the various flag/label/port primitives
// (netflag/netport/netlabel/…) are likewise not physical parts. See issue #13.
const schLayoutPartType = "part"

// filterLayoutComps keeps only real parts unless includeNonParts is set. It
// returns the kept slice plus the count of excluded non-part primitives (sheet,
// netflag, netport, …) so the report can disclose what was skipped. Components
// with an empty componentType are KEPT — an older connector build that doesn't
// emit the field must not have every component silently dropped.
func filterLayoutComps(comps []layoutComp, includeNonParts bool) (kept []layoutComp, skipped int) {
	if includeNonParts {
		return comps, 0
	}
	kept = make([]layoutComp, 0, len(comps))
	for _, c := range comps {
		if c.ComponentType == "" || c.ComponentType == schLayoutPartType {
			kept = append(kept, c)
			continue
		}
		skipped++
	}
	return kept, skipped
}

// layoutFinding is one pairwise issue (overlap, tight spacing, or pin coincidence).
type layoutFinding struct {
	Type string  `json:"type"` // "overlap" | "spacing" | "pin-coincidence"
	A    string  `json:"a"`    // designator (or id)
	B    string  `json:"b"`
	OvX  float64 `json:"overlapX,omitempty"` // overlap extent (mm), overlap only
	OvY  float64 `json:"overlapY,omitempty"`
	Gap  float64 `json:"gap,omitempty"` // edge-to-edge gap (mm), spacing only
	// pin-coincidence only: the two colliding pins and their shared point.
	APin string  `json:"aPin,omitempty"`
	BPin string  `json:"bPin,omitempty"`
	X    float64 `json:"x,omitempty"`
	Y    float64 `json:"y,omitempty"`
	Dist float64 `json:"dist,omitempty"` // pin-to-pin distance (mm), pin-coincidence only
}

// layoutReport is the full normalized result of a layout-lint run.
type layoutReport struct {
	OK              bool            `json:"ok"`
	MinGap          float64         `json:"minGap"`
	Total           int             `json:"componentCount"`
	WithBBox        int             `json:"withBBox"`
	SkippedNonParts int             `json:"skippedNonParts,omitempty"`
	NoBBox          []string        `json:"noBBox,omitempty"`
	Overlaps        []layoutFinding `json:"overlaps"`
	TightPairs      []layoutFinding `json:"tightSpacing"`
	PinCoincidences []layoutFinding `json:"pinCoincidences"`
	PinEps          float64         `json:"pinEps"`
	Summary         string          `json:"summary"`
}

// analyzeLayout is the pure core: given components and a min-gap threshold,
// return every overlapping and too-close pair. Deterministic ordering so output
// and tests are stable. Kept free of I/O for unit-testing.
func analyzeLayout(comps []layoutComp, minGap, pinEps float64) layoutReport {
	rep := layoutReport{MinGap: minGap, PinEps: pinEps, Total: len(comps)}

	withBBox := make([]layoutComp, 0, len(comps))
	for _, c := range comps {
		if c.BBox != nil {
			withBBox = append(withBBox, c)
		} else {
			rep.NoBBox = append(rep.NoBBox, label(c))
		}
	}
	rep.WithBBox = len(withBBox)
	sort.Strings(rep.NoBBox)

	for i := 0; i < len(withBBox); i++ {
		for j := i + 1; j < len(withBBox); j++ {
			a, b := withBBox[i], withBBox[j]
			ox, oy, overlap := overlapExtent(*a.BBox, *b.BBox)
			la, lb := label(a), label(b)
			// Order the pair labels for a stable, readable A↔B.
			if lb < la {
				la, lb = lb, la
			}
			if overlap {
				rep.Overlaps = append(rep.Overlaps, layoutFinding{Type: "overlap", A: la, B: lb, OvX: round2(ox), OvY: round2(oy)})
				continue
			}
			if gap := rectGap(*a.BBox, *b.BBox); gap < minGap {
				rep.TightPairs = append(rep.TightPairs, layoutFinding{Type: "spacing", A: la, B: lb, Gap: round2(gap)})
			}
		}
	}

	rep.PinCoincidences = detectPinCoincidence(comps, pinEps)

	sortFindings(rep.Overlaps)
	sortFindings(rep.TightPairs)
	sortFindings(rep.PinCoincidences)

	// Both bbox overlaps and pin coincidences are hard errors: a coincident pin
	// pair is an implicit short even when the bboxes never touch.
	rep.OK = len(rep.Overlaps) == 0 && len(rep.PinCoincidences) == 0
	rep.Summary = fmt.Sprintf("%d components (%d with bbox): %d overlap, %d tight (<%.2fmm), %d pin-coincidence",
		rep.Total, rep.WithBBox, len(rep.Overlaps), len(rep.TightPairs), minGap, len(rep.PinCoincidences))
	return rep
}

// detectPinCoincidence finds pins from DIFFERENT components that land on the same
// point (distance <= eps). It buckets pins by quantized coordinate so only pins in
// the same/adjacent cell are compared — avoiding a full O(n²) scan over every pin
// pair on the page. Same-component pins never collide with each other (a symbol's
// own pins are expected to sit at fixed offsets). See issue #63.
func detectPinCoincidence(comps []layoutComp, eps float64) []layoutFinding {
	if eps < 0 {
		return nil // negative eps disables the check (internal bbox-only callers)
	}
	// Cell size: eps>0 buckets by eps so neighbors fall within ±1 cell; eps==0
	// (strict equality) buckets on an exact grid so identical points collide.
	cell := eps
	if cell <= 0 {
		cell = 1e-6
	}
	type keyed struct {
		comp int
		pin  layoutPin
	}
	buckets := make(map[[2]int64][]keyed)
	key := func(x, y float64) [2]int64 {
		return [2]int64{int64(math.Floor(x / cell)), int64(math.Floor(y / cell))}
	}
	var out []layoutFinding
	seen := make(map[[2]int]bool) // dedupe pair by (compA,compB) — one finding per part pair
	for ci := range comps {
		for _, p := range comps[ci].Pins {
			k := key(p.X, p.Y)
			// Compare against pins already placed in this and neighbouring cells.
			for dx := int64(-1); dx <= 1; dx++ {
				for dy := int64(-1); dy <= 1; dy++ {
					for _, other := range buckets[[2]int64{k[0] + dx, k[1] + dy}] {
						if other.comp == ci {
							continue // same component: skip
						}
						if math.Hypot(p.X-other.pin.X, p.Y-other.pin.Y) > eps {
							continue
						}
						lo, hi := other.comp, ci
						if lo > hi {
							lo, hi = hi, lo
						}
						if seen[[2]int{lo, hi}] {
							continue
						}
						seen[[2]int{lo, hi}] = true
						la, lb := label(comps[other.comp]), label(comps[ci])
						pa, pb := other.pin, p
						if lb < la {
							la, lb = lb, la
							pa, pb = p, other.pin
						}
						out = append(out, layoutFinding{
							Type: "pin-coincidence", A: la, B: lb,
							APin: pa.Number, BPin: pb.Number,
							X: round2(pa.X), Y: round2(pa.Y),
							Dist: round2(math.Hypot(p.X-other.pin.X, p.Y-other.pin.Y)),
						})
					}
				}
			}
			buckets[k] = append(buckets[k], keyed{comp: ci, pin: p})
		}
	}
	return out
}

// overlapExtent reports the intersection extent of two bboxes and whether they
// actually overlap (positive area on both axes). Touching edges do NOT count.
func overlapExtent(a, b layoutBBox) (ox, oy float64, overlap bool) {
	ox = math.Min(a.MaxX, b.MaxX) - math.Max(a.MinX, b.MinX)
	oy = math.Min(a.MaxY, b.MaxY) - math.Max(a.MinY, b.MinY)
	return ox, oy, ox > 0 && oy > 0
}

// rectGap is the edge-to-edge separation between two non-overlapping bboxes.
func rectGap(a, b layoutBBox) float64 {
	dx := math.Max(0, math.Max(a.MinX-b.MaxX, b.MinX-a.MaxX))
	dy := math.Max(0, math.Max(a.MinY-b.MaxY, b.MinY-a.MaxY))
	return math.Hypot(dx, dy)
}

// label picks the most identifying name. A freshly placed part carries an
// UNASSIGNED designator ("C?", "R?", or empty) — useless for telling two apart —
// so fall back to the primitiveId in that case.
func label(c layoutComp) string {
	d := c.Designator
	if d != "" && !strings.HasSuffix(d, "?") {
		return d
	}
	if c.ID != "" {
		if d != "" {
			return d + "@" + c.ID // e.g. "C?@129274a01919b064"
		}
		return c.ID
	}
	return d
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func sortFindings(f []layoutFinding) {
	sort.Slice(f, func(i, j int) bool {
		if f[i].A != f[j].A {
			return f[i].A < f[j].A
		}
		return f[i].B < f[j].B
	})
}

// runLayoutLint fetches components with bbox, analyzes, renders, and returns a
// non-nil error when overlaps exist so the command exits non-zero (gate-able).
func runLayoutLint(cfg *appConfig, window string, minGap, pinEps float64, allPages, asJSON, includeNonParts bool, stdout, stderr io.Writer) error {
	payload := map[string]any{"includeBBox": true, "includePins": true}
	if allPages {
		payload["allPages"] = true
	}
	res, err := requestAction(cfg, "schematic.components.list", window, payload)
	if err != nil {
		return err
	}

	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		return perr
	}
	parts, skipped := filterLayoutComps(comps, includeNonParts)
	rep := analyzeLayout(parts, minGap, pinEps)
	rep.SkippedNonParts = skipped

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		renderLayoutReport(rep, stdout)
	}

	if !rep.OK {
		return fmt.Errorf("layout-lint: %d overlap(s), %d pin-coincidence(s) found",
			len(rep.Overlaps), len(rep.PinCoincidences))
	}
	return nil
}

// parseLayoutComps extracts the minimal layoutComp slice from a components.list
// result map (components: [{primitiveId, designator, bbox:{minX..}}...]).
func parseLayoutComps(result map[string]any) ([]layoutComp, error) {
	raw, ok := result["components"].([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected components.list result: missing components array")
	}
	out := make([]layoutComp, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		c := layoutComp{
			ID:            asString(m["primitiveId"]),
			Designator:    asString(m["designator"]),
			ComponentType: asString(m["componentType"]),
		}
		if bm, ok := m["bbox"].(map[string]any); ok {
			c.BBox = &layoutBBox{
				MinX: asFloat(bm["minX"]),
				MinY: asFloat(bm["minY"]),
				MaxX: asFloat(bm["maxX"]),
				MaxY: asFloat(bm["maxY"]),
			}
		}
		if pins, ok := m["pins"].([]any); ok {
			for _, pp := range pins {
				pm, ok := pp.(map[string]any)
				if !ok {
					continue
				}
				c.Pins = append(c.Pins, layoutPin{
					Number: asString(pm["pinNumber"]),
					X:      asFloat(pm["x"]),
					Y:      asFloat(pm["y"]),
				})
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

// renderLayoutReport prints a compact human summary.
func renderLayoutReport(rep layoutReport, w io.Writer) {
	fmt.Fprintf(w, "layout-lint: %d components (%d with bbox), min-gap %.2fmm\n", rep.Total, rep.WithBBox, rep.MinGap)
	for _, f := range rep.Overlaps {
		fmt.Fprintf(w, "  ERROR  overlap  %s ↔ %s   (overlap %.2f × %.2f mm)\n", f.A, f.B, f.OvX, f.OvY)
	}
	for _, f := range rep.PinCoincidences {
		fmt.Fprintf(w, "  ERROR  pin-coincidence  %s:%s ↔ %s:%s   (both at %.2f,%.2f — implicit short)\n",
			f.A, f.APin, f.B, f.BPin, f.X, f.Y)
	}
	for _, f := range rep.TightPairs {
		fmt.Fprintf(w, "  WARN   spacing  %s ↔ %s   (gap %.2fmm < %.2fmm)\n", f.A, f.B, f.Gap, rep.MinGap)
	}
	if rep.SkippedNonParts > 0 {
		fmt.Fprintf(w, "  note: %d non-part primitive(s) excluded (sheet/title-frame, netflag/netport/…); pass --include-non-parts to include\n", rep.SkippedNonParts)
	}
	if len(rep.NoBBox) > 0 {
		fmt.Fprintf(w, "  WARN   no-bbox  %d component(s) NOT CHECKED (no bbox — likely non-active-page shallow data under --all-pages; `doc switch` to that page to lint it): %v\n", len(rep.NoBBox), rep.NoBBox)
	}
	skipCaveat := ""
	if len(rep.NoBBox) > 0 {
		skipCaveat = fmt.Sprintf("; %d component(s) NOT checked (skipped ≠ confirmed clear)", len(rep.NoBBox))
	}
	if rep.OK {
		fmt.Fprintf(w, "✓ no overlaps or pin coincidences among checked components; %d tight pair(s)%s\n", len(rep.TightPairs), skipCaveat)
	} else {
		fmt.Fprintf(w, "✗ %d overlap(s), %d pin-coincidence(s), %d tight pair(s)%s\n",
			len(rep.Overlaps), len(rep.PinCoincidences), len(rep.TightPairs), skipCaveat)
	}
}
