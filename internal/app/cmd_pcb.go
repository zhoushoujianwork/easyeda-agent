package app

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newPcbCmd returns the "pcb" subcommand group with all PCB actions.
// --window is a persistent flag on the group so every subcommand inherits it.
//
// Switching the active document to a PCB is done with the generic `easyeda doc
// switch <name|uuid>` (or `pcb docs` to list boards first) — there is no
// pcb-specific open. PCB design rules live in the easyeda-pcb / easyeda-conventions
// skills.
func newPcbCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	pcb := &cobra.Command{
		Use:   "pcb",
		Short: "PCB operations",
	}
	pcb.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")

	// ── drc ───────────────────────────────────────────────────────────────
	// pcb.drc.check — the PCB counterpart to `sch drc`. Routing is automatic:
	// pcb.* actions target the project's PCB window (domain→documentType), so
	// `pcb drc` and `sch drc` never cross-fire.
	{
		var strict bool
		c := &cobra.Command{
			Use:   "drc",
			Short: "Run PCB DRC and return normalized violations",
			Long: `Run PCB DRC on the active PCB and return normalized {passed, violations}.

This is the PCB counterpart to ` + "`easyeda sch drc`" + ` (schematic DRC). The two are
distinct subcommands and route to different documents automatically — pcb.* targets
the project's PCB window, schematic.* targets the schematic window — so they never
cross-fire. The PCB must be the active/foreground document.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb drc
  easyeda pcb drc --strict`,
			RunE: func(cmd *cobra.Command, args []string) error {
				var payload map[string]any
				if strict {
					payload = map[string]any{"strict": true}
				}
				return dispatch(cfg, "pcb.drc.check", window, payload, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors")
		pcb.AddCommand(c)
	}

	// ── docs ──────────────────────────────────────────────────────────────
	// pcb.documents.list
	pcb.AddCommand(&cobra.Command{
		Use:   "docs",
		Short: "List PCB documents in the current project (uuid + name)",
		Args:  cobra.NoArgs,
		Example: `  easyeda pcb docs
  easyeda doc switch <uuid>   # then switch to one`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.documents.list", window, nil, stdout, stderr)
		},
	})

	// ── list ──────────────────────────────────────────────────────────────
	// pcb.components.list
	{
		var layer string
		var includeBBox, includePads bool
		c := &cobra.Command{
			Use:   "list",
			Short: "List placed components/footprints on the active PCB",
			Args:  cobra.NoArgs,
			Example: `  easyeda pcb list
  easyeda pcb list --include-bbox
  easyeda pcb list --layer TOP --include-pads`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if layer != "" {
					payload["layer"] = layer
				}
				if includeBBox {
					payload["includeBBox"] = true
				}
				if includePads {
					payload["includePads"] = true
				}
				if len(payload) == 0 {
					return dispatch(cfg, "pcb.components.list", window, nil, stdout, stderr)
				}
				return dispatch(cfg, "pcb.components.list", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&layer, "layer", "", "filter by layer (e.g. TOP, BOTTOM)")
		c.Flags().BoolVar(&includeBBox, "include-bbox", false, "attach each component's rendered extent {minX,minY,maxX,maxY}")
		c.Flags().BoolVar(&includePads, "include-pads", false, "attach each component's pads (net-by-name surface)")
		pcb.AddCommand(c)
	}

	// ── layers ────────────────────────────────────────────────────────────
	// pcb.layers.list
	pcb.AddCommand(&cobra.Command{
		Use:   "layers",
		Short: "List layers of the active PCB (+ current layer, copper count)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.layers.list", window, nil, stdout, stderr)
		},
	})

	// ── nets ──────────────────────────────────────────────────────────────
	// pcb.nets.list
	pcb.AddCommand(&cobra.Command{
		Use:   "nets",
		Short: "List nets on the active PCB (name, length, color)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.nets.list", window, nil, stdout, stderr)
		},
	})

	// ── board-info ────────────────────────────────────────────────────────
	// pcb.board.info
	pcb.AddCommand(&cobra.Command{
		Use:   "board-info",
		Short: "Read the current Board (schematic↔PCB linkage) + PCB info",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.board.info", window, nil, stdout, stderr)
		},
	})

	// ── import-changes ────────────────────────────────────────────────────
	// pcb.import_changes — the schematic→PCB bridge (components arrive here).
	{
		var schematicUUID string
		var noEnsureBoard, noRecompute bool
		c := &cobra.Command{
			Use:   "import-changes",
			Short: "Sync the schematic netlist/components into the active PCB",
			Long: `Sync the schematic netlist/components into the active PCB (从原理图导入变更).

This is the primary way components arrive on the board. It ensures a Board links the
schematic and PCB first, then recomputes ratlines.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb import-changes
  easyeda pcb import-changes --schematic <uuid>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if schematicUUID != "" {
					payload["schematicUuid"] = schematicUUID
				}
				if noEnsureBoard {
					payload["ensureBoard"] = false
				}
				if noRecompute {
					payload["recomputeRatline"] = false
				}
				if len(payload) == 0 {
					return dispatch(cfg, "pcb.import_changes", window, nil, stdout, stderr)
				}
				return dispatch(cfg, "pcb.import_changes", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&schematicUUID, "schematic", "", "source schematic UUID (default: the linked one)")
		c.Flags().BoolVar(&noEnsureBoard, "no-ensure-board", false, "do not auto-create a Board link if missing")
		c.Flags().BoolVar(&noRecompute, "no-recompute-ratline", false, "skip ratline recomputation")
		pcb.AddCommand(c)
	}

	// ── modify ────────────────────────────────────────────────────────────
	// pcb.component.modify
	{
		var id, patchJSON string
		c := &cobra.Command{
			Use:   "modify",
			Short: "Lay out a PCB component: move/rotate/flip layer/lock/designator",
			Args:  cobra.NoArgs,
			Example: `  easyeda pcb modify --id <pid> --patch '{"x":1000,"y":2000}'
  easyeda pcb modify --id <pid> --patch '{"rotation":90,"layer":"BOTTOM"}'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if id == "" {
					return fmt.Errorf("--id is required")
				}
				if patchJSON == "" {
					return fmt.Errorf("--patch is required")
				}
				var patch map[string]any
				if err := json.Unmarshal([]byte(patchJSON), &patch); err != nil {
					return fmt.Errorf("invalid --patch json (expected object): %w", err)
				}
				return dispatch(cfg, "pcb.component.modify", window,
					map[string]any{"primitiveId": id, "patch": patch}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&id, "id", "", "component primitiveId (required)")
		c.Flags().StringVar(&patchJSON, "patch", "", "JSON patch object (required), e.g. '{\"x\":1000,\"y\":2000}'")
		pcb.AddCommand(c)
	}

	// ── delete ────────────────────────────────────────────────────────────
	// pcb.component.delete
	{
		var idsJSON string
		c := &cobra.Command{
			Use:     "delete",
			Short:   "Delete PCB component primitives by id",
			Args:    cobra.NoArgs,
			Example: `  easyeda pcb delete --ids '["id1","id2"]'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if idsJSON == "" {
					return fmt.Errorf("--ids is required")
				}
				var ids []any
				if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
					return fmt.Errorf("invalid --ids json (expected array): %w", err)
				}
				return dispatch(cfg, "pcb.component.delete", window,
					map[string]any{"primitiveIds": ids}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&idsJSON, "ids", "", `JSON array of primitive IDs to delete (required)`)
		pcb.AddCommand(c)
	}

	// ── align / distribute / grid-snap / move / arrange ───────────────────
	// All operate on the current selection unless --ids is given.
	addLayoutOp := func(use, short, action, flagName, flagDesc string, withValue bool) {
		var idsJSON, val string
		c := &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if withValue {
					if val == "" {
						return fmt.Errorf("--%s is required", flagName)
					}
					payload[flagName] = val
				}
				if idsJSON != "" {
					var ids []any
					if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
						return fmt.Errorf("invalid --ids json (expected array): %w", err)
					}
					payload["primitiveIds"] = ids
				}
				return dispatch(cfg, action, window, payload, stdout, stderr)
			},
		}
		if withValue {
			c.Flags().StringVar(&val, flagName, "", flagDesc)
		}
		c.Flags().StringVar(&idsJSON, "ids", "", "JSON array of primitive IDs (omit = current selection)")
		pcb.AddCommand(c)
	}
	addLayoutOp("align", "Align components by edge/center (left|right|top|bottom|centerX|centerY)",
		"pcb.align", "mode", "alignment mode: left|right|top|bottom|centerX|centerY (required)", true)
	addLayoutOp("distribute", "Evenly space component centers along an axis (x|y)",
		"pcb.distribute", "axis", "distribution axis: x|y (required)", true)

	// grid-snap (numeric grid), move (dx/dy), arrange (mode + tuning) need their
	// own flag shapes, so they are written out rather than via addLayoutOp.
	{
		var idsJSON string
		var grid float64
		c := &cobra.Command{
			Use:   "grid-snap",
			Short: "Snap component anchors to a grid (PCB data units, mil-scale)",
			Args:  cobra.NoArgs,
			Example: `  easyeda pcb grid-snap --grid 100
  easyeda pcb grid-snap --grid 100 --ids '["id1","id2"]'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if !cmd.Flags().Changed("grid") {
					return fmt.Errorf("--grid is required")
				}
				payload := map[string]any{"grid": grid}
				if idsJSON != "" {
					var ids []any
					if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
						return fmt.Errorf("invalid --ids json (expected array): %w", err)
					}
					payload["primitiveIds"] = ids
				}
				return dispatch(cfg, "pcb.grid_snap", window, payload, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&grid, "grid", 0, "grid step in PCB data units (required)")
		c.Flags().StringVar(&idsJSON, "ids", "", "JSON array of primitive IDs (omit = current selection)")
		pcb.AddCommand(c)
	}
	{
		var idsJSON string
		var dx, dy float64
		c := &cobra.Command{
			Use:   "move",
			Short: "Translate components by a relative (dx, dy) offset",
			Args:  cobra.NoArgs,
			Example: `  easyeda pcb move --dx 100 --dy 0
  easyeda pcb move --dx 100 --dy 50 --ids '["id1"]'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if !cmd.Flags().Changed("dx") && !cmd.Flags().Changed("dy") {
					return fmt.Errorf("at least one of --dx / --dy is required")
				}
				payload := map[string]any{"dx": dx, "dy": dy}
				if idsJSON != "" {
					var ids []any
					if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
						return fmt.Errorf("invalid --ids json (expected array): %w", err)
					}
					payload["primitiveIds"] = ids
				}
				return dispatch(cfg, "pcb.components.move", window, payload, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&dx, "dx", 0, "X offset")
		c.Flags().Float64Var(&dy, "dy", 0, "Y offset")
		c.Flags().StringVar(&idsJSON, "ids", "", "JSON array of primitive IDs (omit = current selection)")
		pcb.AddCommand(c)
	}
	{
		var idsJSON, mode string
		var pitch, gutter float64
		var cols int
		c := &cobra.Command{
			Use:   "arrange",
			Short: "Coarse auto-layout SEED: cluster by shared nets, or pack a grid",
			Long: `Coarse auto-layout SEED (mechanical first pass only).

mode=cluster groups by shared local nets; mode=grid packs a flat grid. Each cluster
is grid-packed into a tidy block with gutters; locked components are skipped. Apply
the placement priorities in pcb-layout-conventions.md (easyeda-conventions) afterward.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb arrange
  easyeda pcb arrange --mode grid --cols 8 --pitch 200`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if mode != "" {
					payload["mode"] = mode
				}
				if cmd.Flags().Changed("pitch") {
					payload["pitch"] = pitch
				}
				if cmd.Flags().Changed("gutter") {
					payload["gutter"] = gutter
				}
				if cmd.Flags().Changed("cols") {
					payload["cols"] = cols
				}
				if idsJSON != "" {
					var ids []any
					if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
						return fmt.Errorf("invalid --ids json (expected array): %w", err)
					}
					payload["primitiveIds"] = ids
				}
				return dispatch(cfg, "pcb.components.arrange", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&mode, "mode", "", "cluster (default) | grid")
		c.Flags().Float64Var(&pitch, "pitch", 0, "component pitch within a block")
		c.Flags().Float64Var(&gutter, "gutter", 0, "gutter between blocks")
		c.Flags().IntVar(&cols, "cols", 0, "columns for grid mode")
		c.Flags().StringVar(&idsJSON, "ids", "", "JSON array of primitive IDs (omit = current selection)")
		pcb.AddCommand(c)
	}

	// ── outline-get / outline-set / outline-clear (板框) ───────────────────
	pcb.AddCommand(&cobra.Command{
		Use:   "outline-get",
		Short: "Read the current board outline (segment/arc counts + bbox)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.outline.get", window, nil, stdout, stderr)
		},
	})
	{
		var pointsJSON string
		var lineWidth float64
		var noReplace bool
		c := &cobra.Command{
			Use:   "outline-set",
			Short: "Set the board outline from a closed polygon of points (mil, y-up)",
			Long: `Set the board outline from a closed polygon of points (mil, y-up).

Replaces any existing outline by default. The agent generates the points for the
desired shape (rectangle/rounded-rect/circle/instrument) — see the easyeda-pcb skill;
curves are approximated by line segments. Reports whether all components fall inside.`,
			Args:    cobra.NoArgs,
			Example: `  easyeda pcb outline-set --points '[[0,0],[2000,0],[2000,1500],[0,1500]]'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if pointsJSON == "" {
					return fmt.Errorf("--points is required")
				}
				var points any
				if err := json.Unmarshal([]byte(pointsJSON), &points); err != nil {
					return fmt.Errorf("invalid --points json (expected array): %w", err)
				}
				payload := map[string]any{"points": points}
				if noReplace {
					payload["replace"] = false
				}
				if cmd.Flags().Changed("line-width") {
					payload["lineWidth"] = lineWidth
				}
				return dispatch(cfg, "pcb.outline.set", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&pointsJSON, "points", "", `JSON array of [x,y] points in mil (required), e.g. '[[0,0],[2000,0],[2000,1500],[0,1500]]'`)
		c.Flags().Float64Var(&lineWidth, "line-width", 0, "outline line width")
		c.Flags().BoolVar(&noReplace, "no-replace", false, "append instead of replacing the existing outline")
		pcb.AddCommand(c)
	}
	pcb.AddCommand(&cobra.Command{
		Use:   "outline-clear",
		Short: "Remove the current board outline (BOARD_OUTLINE layer)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.outline.clear", window, nil, stdout, stderr)
		},
	})

	// ── report / drc-rules (read-only PCB analysis) ────────────────────────
	// pcb.report (per-net length + net-class/diff-pair/equal-length views),
	// pcb.drc.rules (the design-rule config without running a check).
	pcb.AddCommand(&cobra.Command{
		Use:   "report",
		Short: "Read-only design report: per-net length, net-class totals, diff-pair skew, equal-length spread",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.report", window, nil, stdout, stderr)
		},
	})
	pcb.AddCommand(&cobra.Command{
		Use:   "drc-rules",
		Short: "Read the active PCB's DRC rule configuration without running a check",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.drc.rules", window, nil, stdout, stderr)
		},
	})

	// ── track / via (copper routing) ───────────────────────────────────────
	// pcb.line.create / pcb.via.create — real routing primitives. Bind to a net
	// by NAME (pull from `pcb nets`); layer ids from `pcb layers`. No PCB autosave
	// yet, so save explicitly after routing.
	{
		var net string
		var layer int
		var x1, y1, x2, y2, width float64
		c := &cobra.Command{
			Use:   "track",
			Short: "Create a copper track (导线) on a layer between two points (mil, y-up)",
			Args:  cobra.NoArgs,
			Example: `  easyeda pcb track --x1 1000 --y1 1000 --x2 1500 --y2 1000 --net GND
  easyeda pcb track --x1 0 --y1 0 --x2 500 --y2 0 --layer 2 --width 10`,
			RunE: func(cmd *cobra.Command, args []string) error {
				for _, f := range []string{"x1", "y1", "x2", "y2"} {
					if !cmd.Flags().Changed(f) {
						return fmt.Errorf("--x1 --y1 --x2 --y2 are all required")
					}
				}
				payload := map[string]any{"startX": x1, "startY": y1, "endX": x2, "endY": y2}
				if net != "" {
					payload["net"] = net
				}
				if cmd.Flags().Changed("layer") {
					payload["layer"] = layer
				}
				if cmd.Flags().Changed("width") {
					payload["lineWidth"] = width
				}
				return dispatch(cfg, "pcb.line.create", window, payload, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&x1, "x1", 0, "start X (mil, required)")
		c.Flags().Float64Var(&y1, "y1", 0, "start Y (mil, required)")
		c.Flags().Float64Var(&x2, "x2", 0, "end X (mil, required)")
		c.Flags().Float64Var(&y2, "y2", 0, "end Y (mil, required)")
		c.Flags().IntVar(&layer, "layer", 1, "copper layer id: TOP=1, BOTTOM=2; inner ids via 'easyeda pcb layers'")
		c.Flags().Float64Var(&width, "width", 0, "track width (mil; default 6)")
		c.Flags().StringVar(&net, "net", "", "net name to bind the track to")
		pcb.AddCommand(c)
	}
	{
		var net string
		var x, y, hole, diameter float64
		c := &cobra.Command{
			Use:   "via",
			Short: "Place a via (过孔) at (x,y) with hole + outer diameter (mil, y-up)",
			Args:  cobra.NoArgs,
			Example: `  easyeda pcb via --x 1200 --y 1000 --net GND
  easyeda pcb via --x 1200 --y 1000 --hole 12 --diameter 24 --net GND`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if !cmd.Flags().Changed("x") || !cmd.Flags().Changed("y") {
					return fmt.Errorf("--x and --y are required")
				}
				payload := map[string]any{"x": x, "y": y}
				if net != "" {
					payload["net"] = net
				}
				if cmd.Flags().Changed("hole") {
					payload["holeDiameter"] = hole
				}
				if cmd.Flags().Changed("diameter") {
					payload["diameter"] = diameter
				}
				return dispatch(cfg, "pcb.via.create", window, payload, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&x, "x", 0, "via center X (mil, required)")
		c.Flags().Float64Var(&y, "y", 0, "via center Y (mil, required)")
		c.Flags().Float64Var(&hole, "hole", 0, "hole (drill) diameter (mil; default 12)")
		c.Flags().Float64Var(&diameter, "diameter", 0, "outer pad diameter (mil; default 24)")
		c.Flags().StringVar(&net, "net", "", "net name to bind the via to")
		pcb.AddCommand(c)
	}

	return pcb
}
