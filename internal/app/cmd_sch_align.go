package app

// cmd_sch_align.go — `sch align` + `sch distribute`: the micro-adjustment pair
// the S6 loop always claimed to have.
//
// design-flow.md has been telling agents "layout-lint 报覆盖 → sch align /
// distribute 挪开" since S6 was written, but the commands never existed — the
// only real tools were per-part `sch modify` (one coordinate at a time) and the
// heavyweight autolayout/autoplace-free planners. This file closes the drift
// with the two obvious pure-geometry verbs over REAL rendered bboxes:
//
//   align       — line up bbox edges/centers against a reference part
//   distribute  — equalize edge-to-edge gaps along one axis
//
// Same contract as the other planners: pure deterministic core (unit-testable,
// no connector), --apply via schematic.component.modify, final anchors snapped
// to the 5-unit grid (an off-grid anchor breaks every downstream connect_pin —
// see schAnchorGrid), post-apply overlap self-check.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// alignMove is one part's planned relocation.
type alignMove struct {
	Designator  string  `json:"designator"`
	PrimitiveID string  `json:"primitiveId,omitempty"`
	FromX       float64 `json:"fromX"`
	FromY       float64 `json:"fromY"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
}

// alignModes maps mode → which bbox feature lines up. The canvas is y-UP
// (+y renders higher), so "top" aligns the LARGER-y edges (MaxY).
var alignModes = map[string]bool{
	"left": true, "right": true, "top": true, "bottom": true,
	"centerx": true, "centery": true,
}

// pickParts resolves the requested designators (case-insensitive, order kept)
// against the live parts; every requested part must exist and carry a bbox.
func pickParts(parts []alPart, designators []string) ([]alPart, error) {
	byDes := make(map[string]alPart, len(parts))
	for _, p := range parts {
		byDes[strings.ToUpper(p.Designator)] = p
	}
	out := make([]alPart, 0, len(designators))
	seen := map[string]bool{}
	for _, d := range designators {
		u := strings.ToUpper(strings.TrimSpace(d))
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		p, ok := byDes[u]
		if !ok {
			return nil, fmt.Errorf("part %q not found on the active page (`sch list` to see designators)", d)
		}
		if !p.HasBBox {
			return nil, fmt.Errorf("part %q has no rendered bbox (non-active page shallow data? `doc switch` first)", d)
		}
		out = append(out, p)
	}
	if len(out) < 2 {
		return nil, fmt.Errorf("need at least 2 parts, got %d", len(out))
	}
	return out, nil
}

// planAlign lines the picked parts up against the reference part (default: the
// first designator). Alignment is computed on rendered bboxes, then converted
// to grid-snapped anchors — snapping can shift the perfect line-up by <5 units,
// which the grid law deliberately wins over pixel-perfect edges.
func planAlign(parts []alPart, mode, ref string) ([]alignMove, error) {
	if !alignModes[mode] {
		return nil, fmt.Errorf("unknown mode %q (left|right|top|bottom|centerx|centery)", mode)
	}
	refIdx := 0
	if ref != "" {
		refIdx = -1
		for i, p := range parts {
			if strings.EqualFold(p.Designator, ref) {
				refIdx = i
				break
			}
		}
		if refIdx < 0 {
			return nil, fmt.Errorf("--ref %q is not among the given designators", ref)
		}
	}
	r := parts[refIdx]
	rcx, rcy := bboxCenter(r.BBox)
	var moves []alignMove
	for _, p := range parts {
		cx, cy := bboxCenter(p.BBox)
		dx, dy := 0.0, 0.0
		switch mode {
		case "left":
			dx = r.BBox.MinX - p.BBox.MinX
		case "right":
			dx = r.BBox.MaxX - p.BBox.MaxX
		case "top":
			dy = r.BBox.MaxY - p.BBox.MaxY
		case "bottom":
			dy = r.BBox.MinY - p.BBox.MinY
		case "centerx":
			dx = rcx - cx
		case "centery":
			dy = rcy - cy
		}
		nx, ny := snapAnchor(p.AnchorX+dx), snapAnchor(p.AnchorY+dy)
		if nx == p.AnchorX && ny == p.AnchorY {
			continue
		}
		moves = append(moves, alignMove{
			Designator: p.Designator, PrimitiveID: p.PrimitiveID,
			FromX: round2(p.AnchorX), FromY: round2(p.AnchorY), X: nx, Y: ny,
		})
	}
	return moves, nil
}

// planDistribute equalizes edge-to-edge gaps along one axis. Parts are ordered
// by their current center on that axis; with --gap unset, the first and last
// stay put and the span is redistributed (negative computed gap = the span is
// too tight → error, widen it or use autoplace-free). With --gap set, parts
// pack sequentially from the first part at exactly that gap.
func planDistribute(parts []alPart, axis string, gap float64, gapSet bool) ([]alignMove, error) {
	if axis != "x" && axis != "y" {
		return nil, fmt.Errorf("unknown axis %q (x|y)", axis)
	}
	if len(parts) < 3 && !gapSet {
		return nil, fmt.Errorf("span redistribution needs at least 3 parts (2 ends + movable middle); pass --gap to pack 2+")
	}
	ordered := append([]alPart(nil), parts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ci, _ := bboxCenter(ordered[i].BBox)
		cj, _ := bboxCenter(ordered[j].BBox)
		if axis == "y" {
			_, ci = bboxCenter(ordered[i].BBox)
			_, cj = bboxCenter(ordered[j].BBox)
		}
		if ci != cj {
			return ci < cj
		}
		return ordered[i].Designator < ordered[j].Designator
	})

	size := func(p alPart) float64 {
		w, h := bboxSize(p.BBox)
		if axis == "x" {
			return w
		}
		return h
	}
	lo := func(p alPart) float64 {
		if axis == "x" {
			return p.BBox.MinX
		}
		return p.BBox.MinY
	}

	if !gapSet {
		first, last := ordered[0], ordered[len(ordered)-1]
		span := lo(last) + size(last) - lo(first)
		sum := 0.0
		for _, p := range ordered {
			sum += size(p)
		}
		gap = (span - sum) / float64(len(ordered)-1)
		if gap < 0 {
			return nil, fmt.Errorf("parts overflow the current span (computed gap %.1f < 0) — pass --gap to pack tighter/looser or widen the span first", gap)
		}
	}

	var moves []alignMove
	cursor := lo(ordered[0])
	for i, p := range ordered {
		target := cursor
		cursor += size(p) + gap
		if i == 0 {
			continue // the first part anchors the run
		}
		d := target - lo(p)
		dx, dy := d, 0.0
		if axis == "y" {
			dx, dy = 0, d
		}
		nx, ny := snapAnchor(p.AnchorX+dx), snapAnchor(p.AnchorY+dy)
		if nx == p.AnchorX && ny == p.AnchorY {
			continue
		}
		moves = append(moves, alignMove{
			Designator: p.Designator, PrimitiveID: p.PrimitiveID,
			FromX: round2(p.AnchorX), FromY: round2(p.AnchorY), X: nx, Y: ny,
		})
	}
	return moves, nil
}

// runAlignMoves shows the plan (default) or applies it via component.modify,
// then re-runs the overlap check as a self-gate — same contract as autolayout.
func runAlignMoves(cfg *appConfig, window string, moves []alignMove, apply, asJSON bool, stdout, stderr io.Writer) error {
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"applied": apply, "moves": moves}); err != nil {
			return err
		}
	} else {
		if len(moves) == 0 {
			fmt.Fprintln(stdout, "already in position — nothing to move")
		}
		for _, m := range moves {
			fmt.Fprintf(stdout, "%-6s %.0f,%.0f → %.0f,%.0f\n", m.Designator, m.FromX, m.FromY, m.X, m.Y)
		}
	}
	if !apply {
		if len(moves) > 0 && !asJSON {
			fmt.Fprintln(stdout, "(dry-run — pass --apply to move)")
		}
		return nil
	}
	for _, m := range moves {
		if _, err := requestAction(cfg, "schematic.component.modify", window, map[string]any{
			"primitiveId": m.PrimitiveID,
			"patch":       map[string]any{"x": m.X, "y": m.Y},
		}); err != nil {
			return fmt.Errorf("apply %s: %w", m.Designator, err)
		}
	}
	fmt.Fprintf(stderr, "applied %d move(s)\n", len(moves))
	// Post-apply self-check (best-effort): any overlap among the moved parts?
	res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{"includeBBox": true})
	if err == nil {
		if comps, perr := parseLayoutComps(res.Result); perr == nil {
			kept, _ := filterLayoutComps(comps, false)
			rep := analyzeLayout(kept, 0, -1)
			moved := map[string]bool{}
			for _, m := range moves {
				moved[strings.ToUpper(m.Designator)] = true
			}
			for _, f := range rep.Overlaps {
				if moved[strings.ToUpper(f.A)] || moved[strings.ToUpper(f.B)] {
					fmt.Fprintf(stderr, "layout ✗ overlap %s ↔ %s — run `sch layout-lint` and adjust\n", f.A, f.B)
				}
			}
		}
	}
	return nil
}

// fetchAlignParts pulls the live parts (real bboxes) for align/distribute.
func fetchAlignParts(cfg *appConfig, window string) ([]alPart, error) {
	res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{"includeBBox": true, "includePins": true})
	if err != nil {
		return nil, err
	}
	parts, _ := parseAutolayoutParts(res.Result)
	return parts, nil
}

func splitDesignators(s string) []string {
	var out []string
	for _, d := range strings.Split(s, ",") {
		if t := strings.TrimSpace(d); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// newSchAlignCmd builds `sch align`.
func newSchAlignCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var designators, mode, ref string
	var apply, asJSON bool
	c := &cobra.Command{
		Use:   "align",
		Short: "Align parts' rendered bboxes (left|right|top|bottom|centerx|centery) to a reference part",
		Long: `Line up two or more parts against a reference part (default: the first
designator), computed on REAL rendered bboxes and applied as grid-snapped
anchors (the 5-unit grid wins over pixel-perfect edges — off-grid anchors break
connect_pin stubs downstream).

The canvas is y-UP: top aligns the larger-y (visually upper) edges. centerx
lines up centers into a vertical column; centery into a horizontal row.

Dry-run by default; --apply moves via schematic.component.modify and re-checks
overlap among the moved parts.`,
		Example: `  easyeda sch align --designators C1,C2,C3 --mode centerx --project ceshi
  easyeda sch align --designators U1,C1 --mode top --ref U1 --apply`,
		RunE: func(cmd *cobra.Command, args []string) error {
			parts, err := fetchAlignParts(cfg, *window)
			if err != nil {
				return err
			}
			picked, err := pickParts(parts, splitDesignators(designators))
			if err != nil {
				return err
			}
			moves, err := planAlign(picked, mode, ref)
			if err != nil {
				return err
			}
			return runAlignMoves(cfg, *window, moves, apply, asJSON, stdout, stderr)
		},
	}
	c.Flags().StringVar(&designators, "designators", "", "comma-separated designators (required, ≥2)")
	c.Flags().StringVar(&mode, "mode", "centerx", "left|right|top|bottom|centerx|centery")
	c.Flags().StringVar(&ref, "ref", "", "reference designator (default: first in --designators)")
	c.Flags().BoolVar(&apply, "apply", false, "actually move (default: dry-run plan)")
	c.Flags().BoolVar(&asJSON, "json", false, "emit the plan as JSON")
	_ = c.MarkFlagRequired("designators")
	return c
}

// newSchDistributeCmd builds `sch distribute`.
func newSchDistributeCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var designators, axis string
	var gap float64
	var apply, asJSON bool
	c := &cobra.Command{
		Use:   "distribute",
		Short: "Equalize edge-to-edge gaps between parts along one axis (x|y)",
		Long: `Distribute parts evenly along an axis, ordered by their current position.
Without --gap the two outermost parts stay put and the middle ones spread to
equal edge-to-edge gaps (needs ≥3 parts; errors when the parts overflow the
span). With --gap every part packs sequentially from the first at exactly that
gap (≥2 parts). Anchors snap to the 5-unit grid.

Dry-run by default; --apply moves via schematic.component.modify and re-checks
overlap among the moved parts.`,
		Example: `  easyeda sch distribute --designators C1,C2,C3,C4 --axis x --project ceshi
  easyeda sch distribute --designators C1,C2,C3 --axis y --gap 20 --apply`,
		RunE: func(cmd *cobra.Command, args []string) error {
			parts, err := fetchAlignParts(cfg, *window)
			if err != nil {
				return err
			}
			picked, err := pickParts(parts, splitDesignators(designators))
			if err != nil {
				return err
			}
			moves, err := planDistribute(picked, axis, gap, cmd.Flags().Changed("gap"))
			if err != nil {
				return err
			}
			return runAlignMoves(cfg, *window, moves, apply, asJSON, stdout, stderr)
		},
	}
	c.Flags().StringVar(&designators, "designators", "", "comma-separated designators (required)")
	c.Flags().StringVar(&axis, "axis", "x", "x|y")
	c.Flags().Float64Var(&gap, "gap", 20, "edge-to-edge gap (omit to redistribute the current span)")
	c.Flags().BoolVar(&apply, "apply", false, "actually move (default: dry-run plan)")
	c.Flags().BoolVar(&asJSON, "json", false, "emit the plan as JSON")
	_ = c.MarkFlagRequired("designators")
	return c
}
