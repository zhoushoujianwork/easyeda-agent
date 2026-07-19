package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// ── autolayout orchestration (I/O side; the planner in cmd_sch_autolayout.go is pure) ──

// alSpec is the `--spec` JSON shape (issue #25).
type alSpec struct {
	Page    string         `json:"page"`
	Sheet   string         `json:"sheet"`
	Modules []alSpecModule `json:"modules"`
	Rules   *alSpecRules   `json:"rules"`
}

type alSpecModule struct {
	Name  string   `json:"name"`
	Zone  string   `json:"zone"`
	Core  string   `json:"core"`
	Parts []string `json:"parts"`
}

// alSpecRules mirrors the rules block; pointer fields so an omitted key keeps the
// default instead of zeroing it.
type alSpecRules struct {
	AvoidTitleBlock                   *bool    `json:"avoidTitleBlock"`
	PreservePinFanout                 *bool    `json:"preservePinFanout"`
	ModuleGap                         *float64 `json:"moduleGap"`
	RouteChannelGap                   *float64 `json:"routeChannelGap"`
	PreferVerticalPeripheralPlacement *bool    `json:"preferVerticalPeripheralPlacement"`
	PartGap                           *float64 `json:"partGap"`
}

// applyTo overlays the spec's rules onto a base ruleset.
func (r *alSpecRules) applyTo(base autolayoutRules) autolayoutRules {
	if r == nil {
		return base
	}
	if r.AvoidTitleBlock != nil {
		base.AvoidTitleBlock = *r.AvoidTitleBlock
	}
	if r.PreservePinFanout != nil {
		base.PreservePinFanout = *r.PreservePinFanout
	}
	if r.ModuleGap != nil {
		base.ModuleGap = *r.ModuleGap
	}
	if r.RouteChannelGap != nil {
		base.RouteChannelGap = *r.RouteChannelGap
	}
	if r.PreferVerticalPeripheralPlacement != nil {
		base.PreferVertical = *r.PreferVerticalPeripheralPlacement
	}
	if r.PartGap != nil {
		base.PartGap = *r.PartGap
	}
	return base
}

// parseAutolayoutParts extracts the placed parts (anchor + bbox + pins) and the
// sheet bbox from a components.list result. Non-part primitives other than the
// sheet are ignored — the planner only moves real parts.
func parseAutolayoutParts(result map[string]any) ([]alPart, *layoutBBox) {
	raw, _ := result["components"].([]any)
	var parts []alPart
	var sheet *layoutBBox
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ctype := asString(m["componentType"])
		var box *layoutBBox
		if bm, ok := m["bbox"].(map[string]any); ok {
			box = &layoutBBox{
				MinX: asFloat(bm["minX"]), MinY: asFloat(bm["minY"]),
				MaxX: asFloat(bm["maxX"]), MaxY: asFloat(bm["maxY"]),
			}
		}
		switch ctype {
		case "sheet":
			if box != nil {
				sheet = box
			}
		case "part", "":
			p := alPart{
				Designator:  asString(m["designator"]),
				PrimitiveID: asString(m["primitiveId"]),
				AnchorX:     asFloat(m["x"]),
				AnchorY:     asFloat(m["y"]),
				Rotation:    asFloat(m["rotation"]),
			}
			if box != nil {
				p.BBox = *box
				p.HasBBox = true
			}
			if pins, ok := m["pins"].([]any); ok {
				for _, pp := range pins {
					if pm, ok := pp.(map[string]any); ok {
						p.Pins = append(p.Pins, alPinPt{X: asFloat(pm["x"]), Y: asFloat(pm["y"])})
					}
				}
			}
			parts = append(parts, p)
		}
	}
	return parts, sheet
}

// runAutolayout pulls real geometry, plans, optionally applies, and renders.
func runAutolayout(cfg *appConfig, window string, spec alSpec, rules autolayoutRules, apply, allPages, asJSON bool, stdout, stderr io.Writer) error {
	res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{
		"includeBBox": true,
		"includePins": true,
		"allPages":    allPages,
	})
	if err != nil {
		return err
	}
	parts, sheet := parseAutolayoutParts(res.Result)
	if apply && rules.AvoidTitleBlock && sheet == nil {
		return fmt.Errorf("autolayout: no sheet bbox found; select/create an A4 sheet and verify with 'easyeda sch sheet-geometry' before --apply")
	}

	modules := make([]alModuleSpec, 0, len(spec.Modules))
	for _, m := range spec.Modules {
		modules = append(modules, alModuleSpec{Name: m.Name, Zone: m.Zone, Core: m.Core, Parts: m.Parts})
	}
	rep := planAutolayout(modules, parts, sheet, rules)
	rep.Page = spec.Page

	if apply {
		applyAutolayout(cfg, window, &rep, stderr)
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		renderAutolayoutReport(rep, apply, stdout)
	}
	if !rep.OK {
		return fmt.Errorf("autolayout: incomplete (%d error(s), %d overlap(s))", len(rep.Errors), rep.Validation.PartOverlaps)
	}
	return nil
}

// applyAutolayout mutates the page (schematic.component.modify per placement),
// then re-pulls geometry and re-runs the overlap check as a self-gate. It mutates
// only when the plan itself is clean — a planning error means nothing is moved.
func applyAutolayout(cfg *appConfig, window string, rep *alReport, stderr io.Writer) {
	if !rep.OK {
		rep.Note = strings.TrimSpace(rep.Note + " plan has errors — nothing applied.")
		return
	}
	applied := 0
	for i := range rep.Placements {
		pl := &rep.Placements[i]
		if pl.PrimitiveID == "" {
			continue
		}
		_, aerr := requestAction(cfg, "schematic.component.modify", window, map[string]any{
			"primitiveId": pl.PrimitiveID,
			"patch":       map[string]any{"x": pl.X, "y": pl.Y},
		})
		if aerr != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("apply %s: %v", pl.Designator, aerr))
			rep.OK = false
			break
		}
		applied++
	}
	rep.Note = strings.TrimSpace(rep.Note + fmt.Sprintf(" applied %d/%d placements.", applied, len(rep.Placements)))

	if !rep.OK {
		return
	}
	// Post-apply self-check: pull the real rendered bboxes and re-run layout-lint.
	lres, lerr := requestAction(cfg, "schematic.components.list", window, map[string]any{"includeBBox": true})
	if lerr != nil {
		fmt.Fprintf(stderr, "autolayout: post-apply layout-lint skipped: %v\n", lerr)
		return
	}
	comps, perr := parseLayoutComps(lres.Result)
	if perr != nil {
		fmt.Fprintf(stderr, "autolayout: post-apply layout-lint skipped: %v\n", perr)
		return
	}
	kept, _ := filterLayoutComps(comps, false)
	lrep := analyzeLayout(kept, 0, -1)
	rep.Validation.PartOverlaps = len(lrep.Overlaps)
	if len(lrep.Overlaps) > 0 {
		rep.OK = false
	}
}

// renderAutolayoutReport prints a compact human summary.
func renderAutolayoutReport(rep alReport, apply bool, w io.Writer) {
	mode := "plan (dry-run)"
	if apply {
		mode = "apply"
	}
	fmt.Fprintf(w, "autolayout: %d placement(s), mode=%s", len(rep.Placements), mode)
	if rep.Page != "" {
		fmt.Fprintf(w, ", page=%s", rep.Page)
	}
	fmt.Fprintln(w)
	if rep.Note != "" {
		fmt.Fprintf(w, "  note: %s\n", rep.Note)
	}
	if rep.TitleBlockProvisional {
		fmt.Fprintf(w, "  note: title-block keep-out is provisional (no sheet bbox) — NOT enforced\n")
	}
	for _, p := range rep.Placements {
		retry := ""
		if p.Retries > 0 {
			retry = fmt.Sprintf(" (retry %d)", p.Retries)
		}
		fmt.Fprintf(w, "  • %-6s [%s] → (%.2f, %.2f) rot=%.0f%s\n", p.Designator, p.Module, p.X, p.Y, p.Rotation, retry)
	}
	for _, wn := range rep.Warnings {
		fmt.Fprintf(w, "  WARN  [%s] %s\n", wn.Module, wn.Message)
	}
	for _, e := range rep.Errors {
		fmt.Fprintf(w, "  ERROR %s\n", e)
	}
	v := rep.Validation
	fmt.Fprintf(w, "  validation: partOverlaps=%d titleBlockHits=%d fanoutKeepoutHits=%d\n", v.PartOverlaps, v.TitleBlockHits, v.FanoutKeepoutHits)
	if rep.OK {
		fmt.Fprintf(w, "✓ layout plan OK\n")
	} else {
		fmt.Fprintf(w, "✗ layout incomplete\n")
	}
}

// newAutolayoutCmd builds the `sch autolayout` subcommand.
func newAutolayoutCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		spec                                            string
		engine                                          string
		dryRun, apply                                   bool
		allPages, asJSON                                bool
		avoidTitleBlock, preserveFanout, preferVertical bool
		moduleGap, channelGap, partGap                  float64
	)
	c := &cobra.Command{
		Use:   "autolayout",
		Short: "Module-aware planner: place parts by module zone with deterministic, lint-clean coordinates",
		Long: `Module-aware schematic placement planner.

autolayout solves MODULE-LEVEL placement (not routing): it reads a --spec (page,
sheet, modules with zone/core/parts, rules), partitions the usable canvas into
named zones (left-top / left-bottom / center / right / right-top / right-bottom /
…), places each module's core IC near its zone center, fans peripherals around
the core, and retries candidate positions on collision — all while preserving
each core pin's fanout channel and the A4 title-block keep-out.

The planner is PURE and deterministic: the same spec on the same input always
yields identical coordinates that pass 'sch layout-lint'. v1 only MOVES parts
that are already placed (it does not create missing parts).

TWO ENGINES (--engine):
  template  (default) our spec-driven functional-group planner above — clean,
            deterministic, needs --spec. Best for KNOWN blocks/modules.
  official  the platform's own eda.sch_Document.autoLayout() (@beta) — a generic
            connectivity-clustered FALLBACK for un-templated pages. No spec, but
            it is a LONG op (~2min), rearranges the WHOLE active schematic page,
            and is messier than a template. Needs the target page foreground.

  --dry-run  return proposed coordinates + warnings, mutate nothing (default)
  --apply    move parts via schematic.component.modify, then self-check overlaps
             (requires a real sheet bbox when title-block avoidance is enabled)
  --json     emit the structured report

Spec shape:
  {
    "page": "P1_MCU_USB_STORAGE", "sheet": "A4",
    "modules": [
      {"name":"MCU","zone":"center","core":"U1","parts":["U1","C18","R6"]}
    ],
    "rules": {"avoidTitleBlock":true,"preservePinFanout":true,
              "moduleGap":80,"routeChannelGap":40,
              "preferVerticalPeripheralPlacement":true}
  }`,
		Args: cobra.NoArgs,
		Example: `  easyeda sch autolayout --spec p1-layout.json --dry-run
  easyeda sch autolayout --spec p1-layout.json --apply
  easyeda sch autolayout --spec p1-layout.json --json
  easyeda sch autolayout --engine official --apply   # platform fallback, whole active page`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && apply {
				return fmt.Errorf("--dry-run and --apply are mutually exclusive")
			}
			// The official engine has a totally different interface (no spec): it
			// wraps the platform's own long-running autoLayout over the active page.
			switch engine {
			case "official":
				if spec != "" {
					fmt.Fprintln(stderr, "note: --spec is ignored by --engine official (the platform lays out the whole active page)")
				}
				return runOfficialAutolayout(cfg, *window, apply, stdout, stderr)
			case "", "template":
				// fall through to the spec-driven planner below
			default:
				return fmt.Errorf("unknown --engine %q (template|official)", engine)
			}
			if spec == "" {
				return fmt.Errorf("--spec is required for --engine template (a layout spec JSON file); or use --engine official for the platform fallback")
			}
			raw, err := os.ReadFile(spec)
			if err != nil {
				return fmt.Errorf("read --spec: %w", err)
			}
			var s alSpec
			if err := json.Unmarshal(raw, &s); err != nil {
				return fmt.Errorf("invalid --spec json: %w", err)
			}
			if len(s.Modules) == 0 {
				return fmt.Errorf("--spec has no modules")
			}
			for _, m := range s.Modules {
				if m.Name == "" {
					return fmt.Errorf("every module needs a name")
				}
				if len(m.Parts) == 0 {
					return fmt.Errorf("module %q has no parts", m.Name)
				}
			}

			rules := defaultAutolayoutRules()
			rules = s.Rules.applyTo(rules)
			// Explicit CLI flags win over the spec's rules block.
			if cmd.Flags().Changed("avoid-titleblock") {
				rules.AvoidTitleBlock = avoidTitleBlock
			}
			if cmd.Flags().Changed("preserve-pin-fanout") {
				rules.PreservePinFanout = preserveFanout
			}
			if cmd.Flags().Changed("prefer-vertical") {
				rules.PreferVertical = preferVertical
			}
			if cmd.Flags().Changed("module-gap") {
				rules.ModuleGap = moduleGap
			}
			if cmd.Flags().Changed("route-channel-gap") {
				rules.RouteChannelGap = channelGap
			}
			if cmd.Flags().Changed("part-gap") {
				rules.PartGap = partGap
			}

			return runAutolayout(cfg, *window, s, rules, apply, allPages, asJSON, stdout, stderr)
		},
	}
	c.Flags().StringVar(&spec, "spec", "", "layout spec JSON file (required for --engine template)")
	c.Flags().StringVar(&engine, "engine", "template", "placement engine: template (our spec-driven planner) | official (platform eda.sch_Document.autoLayout fallback)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "plan and print proposed coordinates without mutating (default behavior)")
	c.Flags().BoolVar(&apply, "apply", false, "move parts via schematic.component.modify, then self-check overlaps")
	c.Flags().BoolVar(&allPages, "all-pages", false, "build the scene from all schematic pages")
	c.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	c.Flags().BoolVar(&avoidTitleBlock, "avoid-titleblock", true, "treat the title block as a hard keep-out")
	c.Flags().BoolVar(&preserveFanout, "preserve-pin-fanout", true, "keep peripherals out of core pin lead-out lanes")
	c.Flags().BoolVar(&preferVertical, "prefer-vertical", true, "try vertical peripheral placement before horizontal")
	c.Flags().Float64Var(&moduleGap, "module-gap", 80, "nominal inter-module breathing room")
	c.Flags().Float64Var(&channelGap, "route-channel-gap", 40, "length of a preserved pin-fanout channel")
	c.Flags().Float64Var(&partGap, "part-gap", 20, "minimum edge-to-edge gap between two parts")
	return c
}
