package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// runAutoplaceFree pulls real geometry, selects a move-set, plans a free-space
// packing, optionally applies it (schematic.component.modify per part), and
// renders. Parts-only: it never touches wires/flags (that is group-move's job),
// so it needs no new connector handler.
func runAutoplaceFree(cfg *appConfig, window string, designators []string, all bool, opts freePlaceOpts, apply, asJSON bool, stdout, stderr io.Writer) error {
	res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{"includeBBox": true})
	if err != nil {
		return err
	}
	parts, sheet := parseAutolayoutParts(res.Result)
	if sheet == nil {
		return fmt.Errorf("autoplace-free: no sheet bbox found; select/create a sheet and verify with 'easyeda sch sheet-geometry' first")
	}

	usable := freePlaceUsableArea(*sheet, opts.Margin)

	// Resolve the move-set.
	move := map[string]bool{}
	switch {
	case len(designators) > 0:
		want := map[string]bool{}
		for _, d := range designators {
			want[strings.TrimSpace(d)] = true
		}
		matched := map[string]bool{}
		for _, p := range parts {
			if p.HasBBox && want[p.Designator] {
				move[p.PrimitiveID] = true
				matched[p.Designator] = true
			}
		}
		var missing []string
		for d := range want {
			if d != "" && !matched[d] {
				missing = append(missing, d)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("autoplace-free: designator(s) not found on page (or lack a bbox): %s", strings.Join(missing, ", "))
		}
	case all:
		for _, p := range parts {
			if p.HasBBox {
				move[p.PrimitiveID] = true
			}
		}
	default:
		move = autoSelectMovable(parts, usable)
		if len(move) == 0 {
			fmt.Fprintln(stdout, "autoplace-free: nothing to do — every part is already inside the usable area and non-overlapping. Pass --all to repack anyway, or --designators to target specific parts.")
			return nil
		}
	}

	rep := planFreePlace(parts, move, sheet, opts)

	if apply && rep.OK {
		applyFreePlace(cfg, window, &rep, stderr)
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		renderFreePlaceReport(rep, usable, apply, stdout)
	}
	if !rep.OK {
		return fmt.Errorf("autoplace-free: %d part(s) found no free slot (%s) — free up sheet area, raise --margin room, or lower --gap",
			len(rep.Unplaced), strings.Join(rep.Unplaced, ", "))
	}
	return nil
}

// applyFreePlace moves each placed part, then re-pulls geometry and re-runs the
// overlap check as a self-gate (mirrors applyAutolayout).
func applyFreePlace(cfg *appConfig, window string, rep *freePlaceReport, stderr io.Writer) {
	applied := 0
	for _, pl := range rep.Placements {
		if pl.PrimitiveID == "" {
			continue
		}
		if _, aerr := requestAction(cfg, "schematic.component.modify", window, map[string]any{
			"primitiveId": pl.PrimitiveID,
			"patch":       map[string]any{"x": pl.X, "y": pl.Y},
		}); aerr != nil {
			rep.Note = strings.TrimSpace(rep.Note + fmt.Sprintf(" apply %s failed: %v.", pl.Designator, aerr))
			rep.OK = false
			return
		}
		applied++
	}
	rep.Note = strings.TrimSpace(rep.Note + fmt.Sprintf(" applied %d/%d placements.", applied, len(rep.Placements)))

	// Post-apply self-check: real bboxes, re-run layout-lint on real parts.
	lres, lerr := requestAction(cfg, "schematic.components.list", window, map[string]any{"includeBBox": true})
	if lerr != nil {
		fmt.Fprintf(stderr, "autoplace-free: post-apply layout-lint skipped: %v\n", lerr)
		return
	}
	comps, perr := parseLayoutComps(lres.Result)
	if perr != nil {
		fmt.Fprintf(stderr, "autoplace-free: post-apply layout-lint skipped: %v\n", perr)
		return
	}
	kept, _ := filterLayoutComps(comps, false)
	if lrep := analyzeLayout(kept, 0, -1); len(lrep.Overlaps) > 0 {
		rep.OK = false
		rep.Note = strings.TrimSpace(rep.Note + fmt.Sprintf(" ⚠ post-apply layout-lint still sees %d overlap(s).", len(lrep.Overlaps)))
	}
}

func renderFreePlaceReport(rep freePlaceReport, usable layoutBBox, apply bool, w io.Writer) {
	mode := "plan (dry-run)"
	if apply {
		mode = "apply"
	}
	fmt.Fprintf(w, "autoplace-free: %d placement(s), %d fixed obstacle(s), mode=%s\n",
		len(rep.Placements), rep.Fixed, mode)
	fmt.Fprintf(w, "  usable area: [%.0f,%.0f → %.0f,%.0f]\n", usable.MinX, usable.MinY, usable.MaxX, usable.MaxY)
	for _, pl := range rep.Placements {
		fmt.Fprintf(w, "  • %-8s (%.0f,%.0f) → (%.0f,%.0f)\n", pl.Designator, pl.FromX, pl.FromY, pl.X, pl.Y)
	}
	if len(rep.Unplaced) > 0 {
		fmt.Fprintf(w, "  ✗ no free slot for: %s\n", strings.Join(rep.Unplaced, ", "))
	}
	if rep.Note != "" {
		fmt.Fprintf(w, "  note:%s\n", rep.Note)
	}
	if rep.OK {
		fmt.Fprintln(w, "✓ all movable parts placed collision-free")
	}
}

// newAutoplaceFreeCmd builds the `sch autoplace-free` subcommand.
func newAutoplaceFreeCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		designators   []string
		all           bool
		dryRun, apply bool
		asJSON        bool
		margin, gap   float64
		gridStep      float64
		noAvoidTitle  bool
	)
	c := &cobra.Command{
		Use:   "autoplace-free",
		Short: "Pack parts into the sheet's blank space (zone-less; keeps other parts fixed)",
		Long: `Zone-less auto-placement: drop movable parts into the sheet's free space,
top-left first-fit, collision-free against the parts you're NOT moving plus the
title-block keep-out. Unlike 'sch autolayout' you don't name zones — it finds the
gaps for you. Parts only (never touches wires/flags — that's 'sch group-move').

Move-set selection:
  (default)        auto: parts currently OUTSIDE the usable area or OVERLAPPING
                   another part; clean in-bounds parts are left exactly where they are
  --designators A,B target these parts explicitly
  --all            repack every part (tidy up a whole messy page)

The planner is PURE/deterministic (same page + opts → same coordinates) and
snaps every anchor to the 5-unit grid. --dry-run (default) proposes coordinates;
--apply moves them via schematic.component.modify and self-checks with layout-lint.

A sheet bbox is required (verify with 'easyeda sch sheet-geometry').`,
		Args: cobra.NoArgs,
		Example: `  easyeda sch autoplace-free --dry-run              # auto-pick the messy parts, preview
  easyeda sch autoplace-free --designators C1,C2,R4 --apply
  easyeda sch autoplace-free --all --apply          # repack the whole page
  easyeda sch autoplace-free --margin 60 --gap 30 --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && apply {
				return fmt.Errorf("--dry-run and --apply are mutually exclusive")
			}
			if all && len(designators) > 0 {
				return fmt.Errorf("--all and --designators are mutually exclusive")
			}
			opts := defaultFreePlaceOpts()
			if cmd.Flags().Changed("margin") {
				opts.Margin = margin
			}
			if cmd.Flags().Changed("gap") {
				opts.Gap = gap
			}
			if cmd.Flags().Changed("grid-step") {
				opts.GridStep = gridStep
			}
			opts.AvoidTitleBlock = !noAvoidTitle
			return runAutoplaceFree(cfg, *window, designators, all, opts, apply, asJSON, stdout, stderr)
		},
	}
	c.Flags().StringSliceVar(&designators, "designators", nil, "explicit parts to place (comma-separate or repeat); default auto-picks overlapping/out-of-bounds parts")
	c.Flags().BoolVar(&all, "all", false, "repack every part on the page (tidy mode)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "propose coordinates without moving anything (default behavior)")
	c.Flags().BoolVar(&apply, "apply", false, "move parts via schematic.component.modify, then self-check overlaps")
	c.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	c.Flags().Float64Var(&margin, "margin", 40, "inset from the sheet edge (schematic units)")
	c.Flags().Float64Var(&gap, "gap", 20, "minimum edge-to-edge gap to every obstacle/placed part")
	c.Flags().Float64Var(&gridStep, "grid-step", 20, "scan resolution for candidate positions")
	c.Flags().BoolVar(&noAvoidTitle, "no-avoid-titleblock", false, "allow placing into the title-block area")
	return c
}
