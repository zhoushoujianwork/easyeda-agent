package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// newPcbCmd returns the "pcb" subcommand group with all PCB actions.
// --window is a persistent flag on the group so every subcommand inherits it.
//
// Switching the active document to a PCB is done with the generic `easyeda doc
// switch <name|uuid>` (or `pcb docs` to list boards first) — there is no
// pcb-specific open. PCB design rules live in the easyeda-agent skill references
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

	// ── add-component ─────────────────────────────────────────────────────
	// pcb.add_component — the WORKING way to add ONE part to an existing PCB
	// (import-changes no-ops for API-added parts, #20): place footprint + link +
	// assign pad nets + ratline.
	{
		var libraryUUID, deviceUUID, designator, uniqueID, netsJSON string
		var x, y, rotation float64
		var layer int
		c := &cobra.Command{
			Use:   "add-component",
			Short: "Place + connect ONE footprint on the PCB (the working alternative to import-changes)",
			Long: `Add a single part to an EXISTING PCB and wire it — the working path, since
'import-changes' is a no-op for parts added to the schematic via the API (#20).

It places the footprint (--library + --uuid, a device), links it to its schematic
twin (--designator + --unique-id), assigns each pad's net from --nets
(a JSON padNumber→net map), and recomputes ratlines.

Get --nets and --unique-id from 'sch read' (the netlist is only readable while the
schematic is active, so you pass them). Workflow:
  1. place + wire the part in the schematic (sch place / connect)
  2. easyeda sch read   → note the part's pin nets + uniqueId
  3. easyeda pcb add-component --library … --uuid … --x … --y … \
       --designator U2 --unique-id gge9 --nets '{"5":"3V3","3":"GND"}'`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb add-component --library <lib> --uuid <dev> --x 3500 --y -1900 \
      --designator U2 --unique-id gge9 --nets '{"5":"3V3","3":"GND"}'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if libraryUUID == "" || deviceUUID == "" {
					return fmt.Errorf("--library and --uuid are required (a device {libraryUuid, uuid})")
				}
				payload := map[string]any{"libraryUuid": libraryUUID, "uuid": deviceUUID, "x": x, "y": y}
				if cmd.Flags().Changed("layer") {
					payload["layer"] = layer
				}
				if cmd.Flags().Changed("rotation") {
					payload["rotation"] = rotation
				}
				if designator != "" {
					payload["designator"] = designator
				}
				if uniqueID != "" {
					payload["uniqueId"] = uniqueID
				}
				if netsJSON != "" {
					var nets map[string]any
					if err := json.Unmarshal([]byte(netsJSON), &nets); err != nil {
						return fmt.Errorf("invalid --nets json (expected object padNumber→net): %w", err)
					}
					payload["nets"] = nets
				}
				return dispatch(cfg, "pcb.add_component", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&libraryUUID, "library", "", "device libraryUuid (required)")
		c.Flags().StringVar(&deviceUUID, "uuid", "", "device uuid (required)")
		c.Flags().Float64Var(&x, "x", 0, "x (mil)")
		c.Flags().Float64Var(&y, "y", 0, "y (mil)")
		c.Flags().IntVar(&layer, "layer", 1, "copper layer id (TOP=1, BOTTOM=2)")
		c.Flags().Float64Var(&rotation, "rotation", 0, "rotation (deg)")
		c.Flags().StringVar(&designator, "designator", "", "designator to set (match the schematic twin, e.g. U2)")
		c.Flags().StringVar(&uniqueID, "unique-id", "", "schematic twin's uniqueId (sch↔PCB link key; from 'sch read')")
		c.Flags().StringVar(&netsJSON, "nets", "", `JSON map padNumber→net, e.g. '{"5":"3V3","3":"GND"}' (from 'sch read')`)
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
the placement priorities in pcb-layout-conventions.md (easyeda-agent) afterward.`,
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
desired shape (rectangle/rounded-rect/circle/instrument) — see the easyeda-agent skill;
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

	// ── save ───────────────────────────────────────────────────────────────
	// pcb.save — PCB counterpart to `sch save`. PCB edits are in-memory until
	// saved; the daemon also autosaves (debounced) after PCB mutations.
	pcb.AddCommand(&cobra.Command{
		Use:   "save",
		Short: "Save the active PCB document to disk",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "pcb.save", window, nil, stdout, stderr)
		},
	})

	// ── track-list / via-list (read what's routed) ─────────────────────────
	{
		var net string
		var layer int
		c := &cobra.Command{
			Use:     "track-list",
			Short:   "List copper tracks (导线), optionally by net/layer",
			Args:    cobra.NoArgs,
			Example: `  easyeda pcb track-list --net GND`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if net != "" {
					payload["net"] = net
				}
				if cmd.Flags().Changed("layer") {
					payload["layer"] = layer
				}
				return dispatch(cfg, "pcb.line.list", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&net, "net", "", "filter by net name")
		c.Flags().IntVar(&layer, "layer", 0, "filter by copper layer id (TOP=1, BOTTOM=2)")
		pcb.AddCommand(c)
	}
	{
		var net string
		c := &cobra.Command{
			Use:     "via-list",
			Short:   "List vias (过孔), optionally by net",
			Args:    cobra.NoArgs,
			Example: `  easyeda pcb via-list --net GND`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if net != "" {
					payload["net"] = net
				}
				return dispatch(cfg, "pcb.via.list", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&net, "net", "", "filter by net name")
		pcb.AddCommand(c)
	}

	// ── rip-up / clear-routing ─────────────────────────────────────────────
	{
		var nets []string
		c := &cobra.Command{
			Use:   "rip-up",
			Short: "Rip up routing (delete tracks+vias); --net to scope, omit = all. Outline/locked are safe",
			Args:  cobra.NoArgs,
			Example: `  easyeda pcb rip-up --net GND
  easyeda pcb rip-up --net GND --net +3V3
  easyeda pcb rip-up            # rip up ALL routing (board outline + locked survive)`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if len(nets) > 0 {
					payload["net"] = nets
				}
				return dispatch(cfg, "pcb.route.rip_up", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringSliceVar(&nets, "net", nil, "net(s) to rip up; repeat or comma-separate; omit = all")
		pcb.AddCommand(c)
	}
	{
		var typ string
		c := &cobra.Command{
			Use:     "clear-routing",
			Short:   "Native clearRouting (@alpha — may be unavailable; prefer rip-up)",
			Args:    cobra.NoArgs,
			Example: `  easyeda pcb clear-routing --type all`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if typ != "" {
					payload["type"] = typ
				}
				return dispatch(cfg, "pcb.clear_routing", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&typ, "type", "all", "all | net | connection")
		pcb.AddCommand(c)
	}

	// ── pour (铺铜) ────────────────────────────────────────────────────────
	{
		var pointsJSON, net, fill, name string
		var layer, priority int
		var width float64
		c := &cobra.Command{
			Use:   "pour",
			Short: "Create a copper pour (铺铜) from a closed polygon, bound to a net (usually GND)",
			Long: `Create a copper pour (铺铜) from a closed polygon of [x,y] points (mil, y-up).

Builds the polygon internally — pass raw points, not a polygon object — then
rebuilds the poured copper. Size it to the board outline; bind to GND for a ground
plane. fill = solid (default) | grid | grid45.`,
			Args:    cobra.NoArgs,
			Example: `  easyeda pcb pour --points '[[0,0],[2000,0],[2000,1500],[0,1500]]' --net GND --layer 2`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if pointsJSON == "" {
					return fmt.Errorf("--points is required")
				}
				var points any
				if err := json.Unmarshal([]byte(pointsJSON), &points); err != nil {
					return fmt.Errorf("invalid --points json (expected array): %w", err)
				}
				payload := map[string]any{"points": points}
				if net != "" {
					payload["net"] = net
				}
				if cmd.Flags().Changed("layer") {
					payload["layer"] = layer
				}
				if fill != "" {
					payload["fill"] = fill
				}
				if name != "" {
					payload["name"] = name
				}
				if cmd.Flags().Changed("priority") {
					payload["priority"] = priority
				}
				if cmd.Flags().Changed("width") {
					payload["lineWidth"] = width
				}
				return dispatch(cfg, "pcb.pour.create", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&pointsJSON, "points", "", `JSON array of [x,y] points in mil (required)`)
		c.Flags().StringVar(&net, "net", "", "net to bind the pour to (e.g. GND)")
		c.Flags().IntVar(&layer, "layer", 1, "copper layer id (TOP=1, BOTTOM=2; inner via 'easyeda pcb layers')")
		c.Flags().StringVar(&fill, "fill", "", "fill style: solid (default) | grid | grid45")
		c.Flags().StringVar(&name, "name", "", "pour name")
		c.Flags().IntVar(&priority, "priority", 0, "pour priority (higher wins overlaps)")
		c.Flags().Float64Var(&width, "width", 0, "pour border/track width (mil)")
		pcb.AddCommand(c)
	}
	{
		var net string
		c := &cobra.Command{
			Use:     "pour-list",
			Short:   "List copper pours (铺铜), optionally by net",
			Args:    cobra.NoArgs,
			Example: `  easyeda pcb pour-list`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if net != "" {
					payload["net"] = net
				}
				return dispatch(cfg, "pcb.pour.list", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&net, "net", "", "filter by net name")
		pcb.AddCommand(c)
	}
	{
		var idsJSON string
		c := &cobra.Command{
			Use:     "pour-delete",
			Short:   "Delete copper pour regions by primitiveId",
			Args:    cobra.NoArgs,
			Example: `  easyeda pcb pour-delete --ids '["id1","id2"]'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if idsJSON == "" {
					return fmt.Errorf("--ids is required")
				}
				var ids []any
				if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
					return fmt.Errorf("invalid --ids json (expected array): %w", err)
				}
				return dispatch(cfg, "pcb.pour.delete", window,
					map[string]any{"primitiveIds": ids}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&idsJSON, "ids", "", `JSON array of pour primitiveIds to delete (required)`)
		pcb.AddCommand(c)
	}
	{
		var net string
		c := &cobra.Command{
			Use:     "pour-rebuild",
			Short:   "Re-pour (recompute) all pours after layout/routing changes",
			Args:    cobra.NoArgs,
			Example: `  easyeda pcb pour-rebuild`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if net != "" {
					payload["net"] = net
				}
				return dispatch(cfg, "pcb.pour.rebuild", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&net, "net", "", "filter by net name")
		pcb.AddCommand(c)
	}

	// ── pour-fit ──────────────────────────────────────────────────────────
	// Auto-size a copper pour to the board: read the outline, inset it by a
	// margin so copper never touches the edge (the Board-Outline-to-Copper
	// clearance), then pour. Pure daemon orchestration over outline.get + pour.*.
	{
		var net, fill string
		var layer int
		var inset float64
		var replace, dryRun bool
		c := &cobra.Command{
			Use:   "pour-fit",
			Short: "Auto-size a GND/power pour to the board outline, inset from the edge",
			Long: `Pour a net-bound plane sized to the board, inset from the edge by --inset (mil)
so copper keeps clearance to the board outline (fixes Board-Outline-to-Copper).
Reads the board outline (pcb.outline.get) and insets its bbox — v1 pours a
RECTANGLE within the bbox (an odd-shaped outline still gets a rectangular plane;
draw a custom polygon with 'pcb pour' for those). By default (--replace) it first
clears existing pours on the same net so you don't stack them.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb pour-fit --project ceshi --net GND --layer 1
  easyeda pcb pour-fit --net GND --layer 1 --inset 25 --dry-run`,
			RunE: func(cmd *cobra.Command, args []string) error {
				// Inset defaults to the board's copper-to-edge rule (JLCPCB fab floor
				// ~8mil; ceshi live ~10mil) instead of a fixed 20 — the real
				// Board-Outline-to-Copper clearance. --inset still overrides. (#32)
				if !cmd.Flags().Changed("inset") {
					inset = fetchPcbRules(cfg, window).copperToEdgeMil
				}
				// 1. Board outline bbox.
				ores, err := requestAction(cfg, "pcb.outline.get", window, nil)
				if err != nil {
					return err
				}
				bb, ok := ores.Result["bbox"].(map[string]any)
				if !ok || bb == nil {
					return fmt.Errorf("no board outline found — set one first with `pcb outline-set`")
				}
				minX, maxX := asFloat(bb["minX"]), asFloat(bb["maxX"])
				minY, maxY := asFloat(bb["minY"]), asFloat(bb["maxY"])
				if maxX-minX <= 2*inset || maxY-minY <= 2*inset {
					return fmt.Errorf("inset %.0f too large for board %0.f×%0.f mil", inset, maxX-minX, maxY-minY)
				}
				x0, y0, x1, y1 := minX+inset, minY+inset, maxX-inset, maxY-inset
				points := [][]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}

				// 2. Optionally clear existing pours on this net (avoid stacking).
				cleared := 0
				if replace && !dryRun {
					if lr, err := requestAction(cfg, "pcb.pour.list", window, nil); err == nil {
						var ids []any
						pours, _ := lr.Result["pours"].([]any)
						for _, pi := range pours {
							if pm, ok := pi.(map[string]any); ok && asString(pm["net"]) == net {
								if id := asString(pm["primitiveId"]); id != "" {
									ids = append(ids, id)
								}
							}
						}
						if len(ids) > 0 {
							if _, err := requestAction(cfg, "pcb.pour.delete", window, map[string]any{"primitiveIds": ids}); err == nil {
								cleared = len(ids)
							}
						}
					}
				}

				// 3. Pour (unless dry-run).
				payload := map[string]any{"points": points, "net": net, "layer": layer}
				if fill != "" {
					payload["fill"] = fill
				}
				if dryRun {
					out := map[string]any{"dryRun": true, "net": net, "layer": layer, "inset": inset, "points": points, "wouldClear": cleared}
					enc := json.NewEncoder(stdout)
					enc.SetIndent("", "  ")
					return enc.Encode(out)
				}
				res, err := requestAction(cfg, "pcb.pour.create", window, payload)
				if err != nil {
					return err
				}
				out := map[string]any{"ok": true, "net": net, "layer": layer, "inset": inset, "cleared": cleared, "points": points, "result": res.Result}
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			},
		}
		c.Flags().StringVar(&net, "net", "GND", "net to bind the pour to")
		c.Flags().IntVar(&layer, "layer", 1, "copper layer id (TOP=1, BOTTOM=2)")
		c.Flags().Float64Var(&inset, "inset", 20, "inset from the board outline (mil; default = board's copper-to-edge rule ~8–10)")
		c.Flags().StringVar(&fill, "fill", "", "fill style: solid (default) | grid | grid45")
		c.Flags().BoolVar(&replace, "replace", true, "clear existing pours on this net first (avoid stacking)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "print the computed pour polygon without drawing")
		pcb.AddCommand(c)
	}

	// ── outline-fit ─────────────────────────────────────────────────────────
	// Tighten the board outline to the placed-component cloud. Fixes the common
	// "outline far bigger than the parts" (low utilization) — run AFTER auto-place,
	// BEFORE pour. Pure daemon orchestration over components.list(bbox)+outline-set.
	{
		var margin float64
		var dryRun bool
		c := &cobra.Command{
			Use:   "outline-fit",
			Short: "Resize the board outline to hug the placed parts + a margin (fix low utilization)",
			Long: `Compute the union bbox of all placed components, add --margin on every side, and
replace the board outline with that rectangle. Run AFTER 'pcb auto-place' and
BEFORE pour/route so copper stays inside a tight frame. Reports the utilization
before/after. ⚠️ Changing the outline after routing/pouring can strand copper —
fit early. --dry-run previews the computed frame.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb outline-fit --project ceshi --margin 100
  easyeda pcb outline-fit --dry-run`,
			RunE: func(cmd *cobra.Command, args []string) error {
				res, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includeBBox": true})
				if err != nil {
					return err
				}
				comps := parseApComps(res.Result)
				minX, minY := math.Inf(1), math.Inf(1)
				maxX, maxY := math.Inf(-1), math.Inf(-1)
				n := 0
				for _, c := range comps {
					if !c.hasBBox {
						continue
					}
					minX, minY = math.Min(minX, c.minX), math.Min(minY, c.minY)
					maxX, maxY = math.Max(maxX, c.maxX), math.Max(maxY, c.maxY)
					n++
				}
				if n == 0 {
					return fmt.Errorf("no components with a bbox on the PCB (run `pcb import-changes` + `pcb auto-place` first)")
				}
				x0, y0, x1, y1 := minX-margin, minY-margin, maxX+margin, maxY+margin
				points := [][]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
				partArea := (maxX - minX) * (maxY - minY)
				newArea := (x1 - x0) * (y1 - y0)

				// Utilization vs the CURRENT outline (advisory).
				var oldUtil float64
				if og, err := requestAction(cfg, "pcb.outline.get", window, nil); err == nil {
					if bb, ok := og.Result["bbox"].(map[string]any); ok {
						ow := asFloat(bb["maxX"]) - asFloat(bb["minX"])
						oh := asFloat(bb["maxY"]) - asFloat(bb["minY"])
						if ow > 0 && oh > 0 {
							oldUtil = partArea / (ow * oh)
						}
					}
				}
				summary := map[string]any{
					"parts":          n,
					"partExtent":     map[string]float64{"w": round2(maxX - minX), "h": round2(maxY - minY)},
					"newOutline":     map[string]float64{"w": round2(x1 - x0), "h": round2(y1 - y0)},
					"utilBefore":     round2(oldUtil * 100),
					"utilAfterParts": round2(partArea / newArea * 100),
					"margin":         margin,
				}
				if dryRun {
					enc := json.NewEncoder(stdout)
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]any{"dryRun": true, "summary": summary, "points": points})
				}
				sr, err := requestAction(cfg, "pcb.outline.set", window, map[string]any{"points": points})
				if err != nil {
					return err
				}
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"ok": true, "summary": summary, "result": sr.Result})
			},
		}
		c.Flags().Float64Var(&margin, "margin", 100, "margin from the part cloud to the board edge (mil)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "print the computed outline without changing it")
		pcb.AddCommand(c)
	}

	// ── via-stitch ──────────────────────────────────────────────────────────
	// Place a pitch-spaced grid of net vias inside a rectangle — thermal vias
	// under a power-IC pad, or GND stitching that ties the top & bottom planes.
	// Pure daemon orchestration over pcb.via.create.
	{
		var net, rectCSV string
		var pitch, hole, diameter, margin float64
		var dryRun bool
		c := &cobra.Command{
			Use:   "via-stitch",
			Short: "Place a grid of net vias in a rectangle (thermal vias / GND stitching)",
			Long: `Fill a rectangle with a pitch-spaced grid of vias on a net — thermal vias under a
power-IC center pad (connect it down to the GND plane), or GND stitching that ties
top & bottom pours together. --rect is "x0,y0,x1,y1" (mil, y-up); vias are inset by
--margin from the rect edges. Run 'pcb pour-rebuild' afterwards so the planes reflow
onto the new vias.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb via-stitch --net GND --rect "2300,-1750,2500,-1550" --pitch 40
  easyeda pcb via-stitch --net GND --rect "0,-2600,3100,-400" --pitch 200 --dry-run`,
			RunE: func(cmd *cobra.Command, args []string) error {
				var x0, y0, x1, y1 float64
				if n, err := fmt.Sscanf(rectCSV, "%g,%g,%g,%g", &x0, &y0, &x1, &y1); err != nil || n != 4 {
					return fmt.Errorf("--rect must be \"x0,y0,x1,y1\" (mil), got %q", rectCSV)
				}
				if x1 < x0 {
					x0, x1 = x1, x0
				}
				if y1 < y0 {
					y0, y1 = y1, y0
				}
				if pitch <= 0 {
					return fmt.Errorf("--pitch must be > 0")
				}
				// Grid points, inset by margin, centered in the rect.
				var pts [][2]float64
				lx, hx := x0+margin, x1-margin
				ly, hy := y0+margin, y1-margin
				for y := ly; y <= hy+1e-6; y += pitch {
					for x := lx; x <= hx+1e-6; x += pitch {
						pts = append(pts, [2]float64{x, y})
					}
				}
				if len(pts) == 0 {
					return fmt.Errorf("rect too small for --margin %.0f / --pitch %.0f (no via fits)", margin, pitch)
				}
				if dryRun {
					enc := json.NewEncoder(stdout)
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]any{"dryRun": true, "net": net, "count": len(pts), "pitch": pitch, "points": pts})
				}
				// Via sizes default to the board's live rule (JLCPCB fab: 2L 0.4/0.8mm,
				// 4L 0.3/0.6mm); --hole/--diameter override. (#32)
				vsRules := fetchPcbRules(cfg, window)
				vHole, vDia := hole, diameter
				if vHole == 0 {
					vHole = vsRules.viaDrillMil
				}
				if vDia == 0 {
					vDia = vsRules.viaDiameterMil
				}
				placed, failed := 0, 0
				for _, p := range pts {
					payload := map[string]any{"x": p[0], "y": p[1], "net": net}
					if vHole > 0 {
						payload["holeDiameter"] = vHole
					}
					if vDia > 0 {
						payload["diameter"] = vDia
					}
					if _, err := requestAction(cfg, "pcb.via.create", window, payload); err != nil {
						failed++
						continue
					}
					placed++
				}
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"ok": true, "net": net, "placed": placed, "failed": failed, "pitch": pitch})
			},
		}
		c.Flags().StringVar(&net, "net", "GND", "net to bind the vias to")
		c.Flags().StringVar(&rectCSV, "rect", "", `rectangle "x0,y0,x1,y1" in mil (required)`)
		c.Flags().Float64Var(&pitch, "pitch", 40, "via center spacing (mil)")
		c.Flags().Float64Var(&margin, "margin", 0, "inset vias from the rect edges (mil)")
		c.Flags().Float64Var(&hole, "hole", 0, "via hole diameter (mil; 0 = connector default 12)")
		c.Flags().Float64Var(&diameter, "diameter", 0, "via outer diameter (mil; 0 = connector default 24)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "print the via grid without placing")
		_ = c.MarkFlagRequired("rect")
		pcb.AddCommand(c)
	}

	// ── Freerouting round-trip: export-dsn / import-autoroute / snapshot ──────
	// The file-based autoroute workflow (the paradigm EasyEDA's own routing
	// extensions use): `pcb export-dsn` → run Freerouting on the DSN → `pcb
	// import-autoroute route.ses`. No autoRouting() typed API (it is @alpha /
	// undefined this build); these wrap the @beta getDsnFile / importAutoRoute*.
	{
		var fileName string
		var noKeepout bool
		c := &cobra.Command{
			Use:   "export-dsn",
			Short: "Export the active PCB as a Specctra DSN (autorouter input)",
			Long: `Export the active PCB as a Specctra DSN (the external-autorouter input).

By default it splices keep-out regions (禁止区域) back into the DSN: EasyEDA's
getDsnFile DROPS pcb_PrimitiveRegion, so a raw export has zero keepout and an
external router (Freerouting) would route under the antenna. The result reports
` + "`keepouts`" + ` = how many were injected. Pass --raw for the unmodified EasyEDA export.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb export-dsn
  easyeda pcb export-dsn --name board.dsn
  easyeda pcb export-dsn --raw          # unmodified EasyEDA export (no keepout)`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if fileName != "" {
					payload["fileName"] = fileName
				}
				if noKeepout {
					payload["injectKeepout"] = false
				}
				return dispatch(cfg, "pcb.export.dsn", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&fileName, "name", "", "DSN file name (default design.dsn)")
		c.Flags().BoolVar(&noKeepout, "raw", false, "raw EasyEDA export — do NOT inject keep-out regions")
		pcb.AddCommand(c)
	}
	{
		var format string
		c := &cobra.Command{
			Use:   "import-autoroute <file>",
			Short: "Import a routed result (Specctra .ses / autoroute .json) into the active PCB",
			Args:  cobra.ExactArgs(1),
			Example: `  easyeda pcb import-autoroute design.ses
  easyeda pcb import-autoroute route.json --format json`,
			RunE: func(cmd *cobra.Command, args []string) error {
				data, err := os.ReadFile(args[0])
				if err != nil {
					return fmt.Errorf("read routed file: %w", err)
				}
				if format == "" {
					if strings.HasSuffix(strings.ToLower(args[0]), ".json") {
						format = "json"
					} else {
						format = "ses"
					}
				}
				payload := map[string]any{
					"fileBase64": base64.StdEncoding.EncodeToString(data),
					"format":     format,
					"fileName":   filepath.Base(args[0]),
				}
				return dispatch(cfg, "pcb.import_autoroute", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&format, "format", "", "ses | json (default: inferred from extension)")
		pcb.AddCommand(c)
	}
	{
		var fit bool
		c := &cobra.Command{
			Use:   "snapshot",
			Short: "Capture the active PCB canvas as a PNG artifact",
			Args:  cobra.NoArgs,
			Example: `  easyeda pcb snapshot
  easyeda pcb snapshot --fit=false`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return dispatch(cfg, "pcb.snapshot", window, map[string]any{"fit": fit}, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&fit, "fit", true, "zoom-to-fit before capture (nudges a redraw)")
		pcb.AddCommand(c)
	}
	// ── autoroute: one-command Freerouting round-trip ────────────────────────
	// export DSN → run an external Freerouting engine → import the routed SES → DRC.
	// The engine is external (Freerouting needs Java 17+); decoupled via a command
	// template so any router works. Without one configured it exports + stops
	// (graceful degradation → route manually, then `pcb import-autoroute`).
	{
		var routerCmd string
		var keep bool
		c := &cobra.Command{
			Use:   "autoroute",
			Short: "Auto-route the active PCB via an external Freerouting engine (DSN→route→SES→import→DRC)",
			Long: `Orchestrate a DSN→route→SES→import→DRC round-trip. The routing ENGINE is
external and pluggable (--router / FREEROUTING_CMD with {in}/{out}); we do NOT
bundle one.

NOTE — there is no built-in, no-popup, programmatically-callable autorouter today:
  • eda.pcb_Document.autoRouting() is declared but @alpha / undefined at runtime.
  • easyeda-pcb-router (official headless Freerouting) is a separate WS service you
    must run yourself.
  • the marketplace Freerouting extension can't be invoked from another extension.
So this command needs an external engine YOU provide, and is SUPERSEDED once a
native autoRouting() API ships. The building blocks (pcb export-dsn /
import-autoroute / snapshot) work regardless.

  easyeda pcb autoroute --router '<your-dsn→ses-router-cmd> {in} {out}'

Without a router configured, autoroute exports the DSN and stops — route it
externally, then run 'easyeda pcb import-autoroute <file.ses>'.

PREREQUISITE (see docs/test-case-esp32-pcb.md): keep-out zones (antenna / board
edge) MUST be in the DSN, else the router will route under the antenna. Verify the
exported DSN contains keepout entries before trusting the result.`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				// 1. Export DSN, capture the persisted file path.
				res, err := dispatchCapture(cfg, "pcb.export.dsn", window, map[string]any{}, stdout)
				if err != nil {
					return err
				}
				dsnPath := ""
				for _, a := range res.Artifacts {
					if a.Path != "" {
						dsnPath = a.Path
						break
					}
				}
				if dsnPath == "" {
					return fmt.Errorf("export-dsn returned no file (PCB empty or no nets? run `pcb import-changes` first)")
				}
				fmt.Fprintf(stderr, "DSN exported: %s\n", dsnPath)

				tmpl := routerCmd
				if tmpl == "" {
					tmpl = os.Getenv("FREEROUTING_CMD")
				}
				if tmpl == "" {
					fmt.Fprintf(stderr, "no --router / FREEROUTING_CMD set — DSN exported, stopping.\n"+
						"  route it externally (Freerouting), then: easyeda pcb import-autoroute <file.ses>\n")
					return nil
				}

				// 2. Run the external router: {in}=DSN, {out}=SES.
				sesPath := strings.TrimSuffix(dsnPath, ".dsn") + ".ses"
				runStr := strings.NewReplacer("{in}", dsnPath, "{out}", sesPath).Replace(tmpl)
				fmt.Fprintf(stderr, "routing: %s\n", runStr)
				rc := exec.Command("sh", "-c", runStr)
				rc.Stdout = stderr
				rc.Stderr = stderr
				if err := rc.Run(); err != nil {
					return fmt.Errorf("external router failed: %w", err)
				}
				if _, err := os.Stat(sesPath); err != nil {
					return fmt.Errorf("router produced no SES at %s (check the command's {out})", sesPath)
				}
				if !keep {
					defer func() { _ = os.Remove(sesPath) }()
				}

				// 3. Import the routed SES.
				data, err := os.ReadFile(sesPath)
				if err != nil {
					return fmt.Errorf("read SES: %w", err)
				}
				fmt.Fprintf(stderr, "importing SES (%d bytes) → tracks/vias\n", len(data))
				if err := dispatch(cfg, "pcb.import_autoroute", window, map[string]any{
					"fileBase64": base64.StdEncoding.EncodeToString(data),
					"format":     "ses",
					"fileName":   filepath.Base(sesPath),
				}, stdout, stderr); err != nil {
					return err
				}

				// 4. DRC the result.
				fmt.Fprintln(stderr, "--- DRC after routing ---")
				return dispatch(cfg, "pcb.drc.check", window, nil, stdout, stderr)
			},
		}
		c.Flags().StringVar(&routerCmd, "router", "", "external router command with {in}/{out} (or FREEROUTING_CMD env)")
		c.Flags().BoolVar(&keep, "keep", false, "keep the intermediate SES file")
		pcb.AddCommand(c)
	}

	// ── auto-place ────────────────────────────────────────────────────────
	// Module-aware heuristic placement (daemon-side; see pcb_autoplace.go).
	{
		var noRotate bool
		var multiGap float64
		var mainPins int
		var gap, pitch float64
		var dryRun bool
		c := &cobra.Command{
			Use:   "auto-place",
			Short: "Module-aware auto placement: pull each satellite (cap/R/LED) to the chip pin it connects to",
			Long: `Heuristic "hug the chip" placement, run in the daemon (not the connector, so
'make dev' hot-reloads tweaks with no re-import). Main chips (>= --main-pins
distinct pins) are anchors and stay put; every small satellite is moved to the
chip edge nearest the pad it actually connects to, then packed along that edge so
nothing overlaps:
  • decoupling caps land by their power pin (3V3/VCC), resistors by their signal pin
  • an LED chains next to its series resistor (shared signal net)
With 2+ main chips, any that overlap / sit closer than --multi-gap are spread into a
row (leftmost stays put) before satellites are placed; --multi-gap 0 disables that.
This is a SEED, not a final layout — verify with 'pcb drc'.

  easyeda pcb auto-place --project ceshi --dry-run   # print the plan, move nothing
  easyeda pcb auto-place --project ceshi             # apply it`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				// 1. Read placed components with pads (net surface) + rendered bbox.
				res, err := requestAction(cfg, "pcb.components.list", window,
					map[string]any{"includePads": true, "includeBBox": true})
				if err != nil {
					return err
				}
				comps := parseApComps(res.Result)
				if len(comps) == 0 {
					return fmt.Errorf("no components on the active PCB (run `pcb import-changes` first)")
				}

				// 2. Plan (pure, daemon-side).
				opt := defaultApOptions()
				// Clearance-aware spacing: derive gap/pitch from the board's live DRC
				// rule (room for a legal track + clearances between parts) instead of
				// the hardcoded 40/30, so packing never creates sub-clearance corridors
				// and packs as tight as the rule allows. (#22)
				apRules := fetchPcbRules(cfg, window)
				opt.gap = math.Max(12, apRules.clearanceMil*2+apRules.trackWidthMil+6)
				opt.pitch = math.Max(8, apRules.clearanceMil+apRules.trackWidthMil)
				if mainPins > 0 {
					opt.mainPins = mainPins
				}
				if gap > 0 {
					opt.gap = gap
				}
				if pitch > 0 {
					opt.pitch = pitch
				}
				if noRotate {
					opt.rotate = false
				}
				if cmd.Flags().Changed("multi-gap") {
					opt.multiGap = multiGap
				}
				moves, diags := planAutoPlace(comps, opt)

				// 3. Apply (unless --dry-run), one modify per satellite. Re-oriented
				// 2-pin parts also get a rotation patch.
				applied := 0
				var failures []map[string]any
				if !dryRun {
					for _, m := range moves {
						patch := map[string]any{"x": m.NewX, "y": m.NewY}
						if m.SetRot {
							patch["rotation"] = m.NewRot
						}
						if _, err := requestAction(cfg, "pcb.component.modify", window,
							map[string]any{"primitiveId": m.ID, "patch": patch}); err != nil {
							failures = append(failures, map[string]any{"designator": m.Designator, "error": err.Error()})
							continue
						}
						applied++
					}
				}

				// 4. Report.
				out := map[string]any{
					"ok":       true,
					"dryRun":   dryRun,
					"mains":    apMainDesignators(comps, opt),
					"planned":  len(moves),
					"applied":  applied,
					"moves":    moves,
					"diags":    diags,
					"failures": failures,
				}
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			},
		}
		c.Flags().IntVar(&mainPins, "main-pins", 0, "distinct-pin threshold to treat a component as a main chip (default 8)")
		c.Flags().Float64Var(&gap, "gap", 0, "clearance from chip edge to satellite (mil, default 40)")
		c.Flags().Float64Var(&pitch, "pitch", 0, "spacing between satellites packed on the same edge (mil, default 30)")
		c.Flags().BoolVar(&noRotate, "no-rotate", false, "do not re-orient satellites (v1 translate-only behavior)")
		c.Flags().Float64Var(&multiGap, "multi-gap", 0, "min bbox gap between multiple main chips (mil, default 150; 0 disables spacing)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "print the placement plan without moving anything")
		pcb.AddCommand(c)
	}

	// ── route-short ───────────────────────────────────────────────────────
	// Short-trace self-routing (daemon-side; see pcb_shortroute.go).
	{
		var maxLen, width, signalWidth, powerWidth, roundRadius float64
		var dryRun, routeGnd bool
		var corner string
		c := &cobra.Command{
			Use:   "route-short",
			Short: "Self-route the short, clear hops: per-net MST, L-shaped tracks on the pads' layer",
			Long: `Heuristic short-trace router, run in the daemon (the "heuristic tier" — NOT the
@alpha autoRouting() API, NOT an external Freerouting; that is 'pcb autoroute').
Per net it builds a minimum spanning tree over the pads and routes each hop that
is short enough (<= --max-len, Manhattan) as an L-shaped track on the pads'
shared layer. Skips: GND (poured, unless --route-gnd), already-routed nets,
cross-layer hops (need a via), and over-long hops (left for the maze tier).
No obstacle avoidance in v1 — run AFTER 'pcb auto-place' (hops are then short and
clear) and verify with 'pcb drc'.

Track width is by net class: power/ground nets (VCC/VDD/3V3/GND…) get --width-power
(default 20 mil), signals get --width-signal (default 10 mil). A single --width
overrides both. Corners default to 90° L; --corner 45 chamfers them, --corner round
emits a chord-approximated fillet (native arcs do not commit on this build).

  easyeda pcb route-short --project ceshi --dry-run            # print the plan, draw nothing
  easyeda pcb route-short --project ceshi                      # draw with class widths + 90° corners
  easyeda pcb route-short --project ceshi --corner 45          # chamfered corners
  easyeda pcb route-short --project ceshi --width-power 25     # fatter power tracks`,
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				// 1. Read pads (net + coords + layer) and which nets already have copper.
				res, err := requestAction(cfg, "pcb.components.list", window,
					map[string]any{"includePads": true})
				if err != nil {
					return err
				}
				comps := parseApComps(res.Result)
				if len(comps) == 0 {
					return fmt.Errorf("no components on the active PCB (run `pcb import-changes` first)")
				}
				routed := map[string]bool{}
				if lr, err := requestAction(cfg, "pcb.line.list", window, nil); err == nil {
					routed = parseRoutedNets(lr.Result)
				}

				// 2. Plan (pure, daemon-side).
				switch corner {
				case "90", "45", "round":
				default:
					return fmt.Errorf("--corner must be 90, 45, or round (got %q)", corner)
				}
				opt := defaultRtOptions()
				// Rule-aware widths: SIGNAL uses the board's live track-width default,
				// POWER stays wider (current capacity — the fab reference's recommended
				// power width, ≥ signal), both clamped ≥ the legal minimum. Overrides
				// via --width-signal/--width-power. (#22, power/signal split #corrected)
				rules := fetchPcbRules(cfg, window)
				opt.signalWidth = rules.clampWidth(rules.trackWidthMil)
				opt.powerWidth = rules.clampWidth(rules.powerWidthMil)
				if maxLen > 0 {
					opt.maxLen = maxLen
				}
				opt.width = width
				if signalWidth > 0 {
					opt.signalWidth = signalWidth
				}
				if powerWidth > 0 {
					opt.powerWidth = powerWidth
				}
				if roundRadius > 0 {
					opt.roundRadius = roundRadius
				}
				opt.corner = corner
				opt.skipGnd = !routeGnd
				segs, diags := planShortRoutes(comps, routed, opt)

				// 3. Draw (unless --dry-run), one line.create per segment.
				drawn := 0
				var failures []map[string]any
				if !dryRun {
					for _, s := range segs {
						payload := map[string]any{"startX": s.X1, "startY": s.Y1, "endX": s.X2, "endY": s.Y2, "net": s.Net, "layer": s.Layer}
						if s.Width > 0 {
							payload["lineWidth"] = s.Width
						}
						if _, err := requestAction(cfg, "pcb.line.create", window, payload); err != nil {
							failures = append(failures, map[string]any{"net": s.Net, "error": err.Error()})
							continue
						}
						drawn++
					}
				}

				// 4. Report.
				out := map[string]any{
					"ok":       true,
					"dryRun":   dryRun,
					"segments": len(segs),
					"drawn":    drawn,
					"rules":    map[string]any{"source": rules.source, "clearanceMil": rules.clearanceMil, "trackWidthMil": rules.trackWidthMil, "signalWidth": opt.signalWidth, "powerWidth": opt.powerWidth},
					"routes":   segs,
					"skipped":  diags,
					"failures": failures,
				}
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			},
		}
		c.Flags().Float64Var(&maxLen, "max-len", 0, "longest hop to route (Manhattan mil, default 1000)")
		c.Flags().Float64Var(&width, "width", 0, "force ALL tracks to this width (mil); overrides --width-signal/--width-power")
		c.Flags().Float64Var(&signalWidth, "width-signal", 0, "signal-net track width (mil, default 10)")
		c.Flags().Float64Var(&powerWidth, "width-power", 0, "power/ground-net track width (mil, default 20)")
		c.Flags().StringVar(&corner, "corner", "90", "corner style: 90 (L), 45 (chamfer), round (chord fillet)")
		c.Flags().Float64Var(&roundRadius, "round-radius", 0, "max fillet radius for --corner round (mil, default 20)")
		c.Flags().BoolVar(&routeGnd, "route-gnd", false, "also route GND (default skip — GND is poured)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "print the routing plan without drawing anything")
		pcb.AddCommand(c)
	}

	// ── region ────────────────────────────────────────────────────────────
	// pcb.region.* — keep-out / rule regions (禁止区域). NOT net-bound copper
	// (that's `pcb pour`). The #5 prerequisite: antenna / board-edge keep-out.
	{
		region := &cobra.Command{
			Use:   "region",
			Short: "Keep-out / rule regions (禁止区域): create / list / delete",
			Long: `Manage keep-out / rule regions (禁止区域) — a polygon that keeps components,
wires, and/or copper OUT of an area (antenna clearance, board-edge inset,
mechanical exclusion). This is NOT net-bound filled copper — use 'pcb pour' for a
ground/power plane.

ruleType (name or number, repeatable): no-components(2), no-wires(5), no-fills(6),
no-pours(7), no-inner-electrical(8), follow-rule(9). Default is a hard keep-out
[no-components, no-wires, no-pours].`,
		}

		{
			var pointsJSON, rectSpec, ref, name string
			var ruleTypes []string
			var layer int
			var width, margin float64
			var locked bool
			c := &cobra.Command{
				Use:   "create",
				Short: "Create a keep-out / rule region (area via --points | --rect | --ref)",
				Args:  cobra.NoArgs,
				Example: `  easyeda pcb region create --points '[[100,100],[400,100],[400,300],[100,300]]'   # default keep-out
  easyeda pcb region create --rect 2250,-2420,2700,-2180 --rule no-pours --name antenna
  easyeda pcb region create --ref U1 --margin 40 --rule no-pours --rule no-components   # keep-out under U1's antenna`,
				RunE: func(cmd *cobra.Command, args []string) error {
					points, err := areaPointsFrom(cfg, window, pointsJSON, rectSpec, ref, margin)
					if err != nil {
						return err
					}
					payload := map[string]any{"points": points}
					if cmd.Flags().Changed("layer") {
						payload["layer"] = layer
					}
					if len(ruleTypes) > 0 {
						payload["ruleType"] = ruleTypes
					}
					if name != "" {
						payload["name"] = name
					}
					if cmd.Flags().Changed("width") {
						payload["lineWidth"] = width
					}
					if locked {
						payload["locked"] = true
					}
					return dispatch(cfg, "pcb.region.create", window, payload, stdout, stderr)
				},
			}
			c.Flags().StringVar(&pointsJSON, "points", "", `JSON array of [x,y] points in mil (or use --rect / --ref)`)
			c.Flags().StringVar(&rectSpec, "rect", "", "axis-aligned rect 'x0,y0,x1,y1' (mil) — shorthand for a rectangular keep-out")
			c.Flags().StringVar(&ref, "ref", "", "designator of a placed component — keep-out over its bbox (e.g. an antenna module)")
			c.Flags().Float64Var(&margin, "margin", 0, "expand the --rect/--ref box outward by this many mil (antenna clearance)")
			c.Flags().StringArrayVar(&ruleTypes, "rule", nil, "rule type (repeatable): no-components|no-wires|no-fills|no-pours|no-inner-electrical|follow-rule (default keep-out)")
			c.Flags().IntVar(&layer, "layer", 1, "copper layer id (TOP=1, BOTTOM=2; inner via 'easyeda pcb layers')")
			c.Flags().StringVar(&name, "name", "", "region name")
			c.Flags().Float64Var(&width, "width", 0, "region border width (mil)")
			c.Flags().BoolVar(&locked, "locked", false, "create the region locked")
			region.AddCommand(c)
		}
		{
			var layer int
			c := &cobra.Command{
				Use:     "list",
				Short:   "List keep-out / rule regions, optionally by layer",
				Args:    cobra.NoArgs,
				Example: `  easyeda pcb region list`,
				RunE: func(cmd *cobra.Command, args []string) error {
					payload := map[string]any{}
					if cmd.Flags().Changed("layer") {
						payload["layer"] = layer
					}
					return dispatch(cfg, "pcb.region.list", window, payload, stdout, stderr)
				},
			}
			c.Flags().IntVar(&layer, "layer", 0, "filter by copper layer id")
			region.AddCommand(c)
		}
		{
			var idsJSON string
			c := &cobra.Command{
				Use:     "delete",
				Short:   "Delete keep-out / rule regions by primitiveId",
				Args:    cobra.NoArgs,
				Example: `  easyeda pcb region delete --ids '["id1","id2"]'`,
				RunE: func(cmd *cobra.Command, args []string) error {
					if idsJSON == "" {
						return fmt.Errorf("--ids is required")
					}
					var ids []any
					if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
						return fmt.Errorf("invalid --ids json (expected array): %w", err)
					}
					return dispatch(cfg, "pcb.region.delete", window,
						map[string]any{"primitiveIds": ids}, stdout, stderr)
				},
			}
			c.Flags().StringVar(&idsJSON, "ids", "", `JSON array of region primitiveIds to delete (required)`)
			region.AddCommand(c)
		}
		pcb.AddCommand(region)
	}

	// ── fill ──────────────────────────────────────────────────────────────
	// pcb.fill.* — net-bound filled region (填充区域 / 异形大块铜). STATIC copper
	// (no reflow), distinct from `pcb pour` (覆铜, reflows) and `pcb region` (keep-out).
	{
		fill := &cobra.Command{
			Use:   "fill",
			Short: "Net-bound filled region (填充区域 / 异形大块铜): create / list / delete",
			Long: `Manage net-bound filled regions (填充区域) — a STATIC filled polygon bound to a
net (a 3V3/RF-ground patch, thermal copper, odd-shaped plane). Unlike 'pcb pour'
(覆铜) it does NOT reflow around obstacles; unlike 'pcb region' (keep-out) it
carries a net. fillMode: solid (default) | mesh | inner.`,
		}
		{
			var pointsJSON, rectSpec, ref, net, fillMode string
			var layer int
			var width, margin float64
			var locked bool
			c := &cobra.Command{
				Use:   "create",
				Short: "Create a net-bound filled region (area via --points | --rect | --ref)",
				Args:  cobra.NoArgs,
				Example: `  easyeda pcb fill create --points '[[100,100],[400,100],[400,300],[100,300]]' --net 3V3 --layer 1
  easyeda pcb fill create --rect 2150,-1550,2400,-1400 --net GND --fill-mode mesh
  easyeda pcb fill create --ref U3 --margin 20 --net GND   # copper patch over U3`,
				RunE: func(cmd *cobra.Command, args []string) error {
					points, err := areaPointsFrom(cfg, window, pointsJSON, rectSpec, ref, margin)
					if err != nil {
						return err
					}
					payload := map[string]any{"points": points}
					if net != "" {
						payload["net"] = net
					}
					if cmd.Flags().Changed("layer") {
						payload["layer"] = layer
					}
					if fillMode != "" {
						payload["fillMode"] = fillMode
					}
					if cmd.Flags().Changed("width") {
						payload["lineWidth"] = width
					}
					if locked {
						payload["locked"] = true
					}
					return dispatch(cfg, "pcb.fill.create", window, payload, stdout, stderr)
				},
			}
			c.Flags().StringVar(&pointsJSON, "points", "", `JSON array of [x,y] points in mil (or use --rect / --ref)`)
			c.Flags().StringVar(&rectSpec, "rect", "", "axis-aligned rect 'x0,y0,x1,y1' (mil)")
			c.Flags().StringVar(&ref, "ref", "", "designator of a placed component — fill over its bbox")
			c.Flags().Float64Var(&margin, "margin", 0, "expand the --rect/--ref box outward by this many mil")
			c.Flags().StringVar(&net, "net", "", "net to bind the fill to (e.g. 3V3, GND)")
			c.Flags().IntVar(&layer, "layer", 1, "layer id (TOP=1, BOTTOM=2; inner via 'easyeda pcb layers')")
			c.Flags().StringVar(&fillMode, "fill-mode", "", "fill mode: solid (default) | mesh | inner")
			c.Flags().Float64Var(&width, "width", 0, "fill border width (mil)")
			c.Flags().BoolVar(&locked, "locked", false, "create the fill locked")
			fill.AddCommand(c)
		}
		{
			var layer int
			var net string
			c := &cobra.Command{
				Use:     "list",
				Short:   "List net-bound filled regions, optionally by layer/net",
				Args:    cobra.NoArgs,
				Example: `  easyeda pcb fill list --net 3V3`,
				RunE: func(cmd *cobra.Command, args []string) error {
					payload := map[string]any{}
					if cmd.Flags().Changed("layer") {
						payload["layer"] = layer
					}
					if net != "" {
						payload["net"] = net
					}
					return dispatch(cfg, "pcb.fill.list", window, payload, stdout, stderr)
				},
			}
			c.Flags().IntVar(&layer, "layer", 0, "filter by layer id")
			c.Flags().StringVar(&net, "net", "", "filter by net name")
			fill.AddCommand(c)
		}
		{
			var idsJSON string
			c := &cobra.Command{
				Use:     "delete",
				Short:   "Delete net-bound filled regions by primitiveId",
				Args:    cobra.NoArgs,
				Example: `  easyeda pcb fill delete --ids '["id1","id2"]'`,
				RunE: func(cmd *cobra.Command, args []string) error {
					if idsJSON == "" {
						return fmt.Errorf("--ids is required")
					}
					var ids []any
					if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
						return fmt.Errorf("invalid --ids json (expected array): %w", err)
					}
					return dispatch(cfg, "pcb.fill.delete", window,
						map[string]any{"primitiveIds": ids}, stdout, stderr)
				},
			}
			c.Flags().StringVar(&idsJSON, "ids", "", `JSON array of fill primitiveIds to delete (required)`)
			fill.AddCommand(c)
		}
		pcb.AddCommand(fill)
	}

	// ── slot (挖槽 / board cutout) ──────────────────────────────────────────
	// A slot is a pcb_PrimitiveFill on the MULTI layer (12): per the eda API types
	// (index.d.ts — "填充所属层为 EPCB_LayerId.MULTI 时代表挖槽区域"), a MULTI-layer
	// fill IS a board cutout, and the manufacturing output emits it as a BoardCutout
	// object. Same area shorthand as region/fill. Antenna isolation / mechanical
	// opening. List / delete via `pcb fill list --layer 12` / `pcb fill delete`.
	{
		var pointsJSON, rectSpec, ref string
		var margin float64
		var locked bool
		c := &cobra.Command{
			Use:   "slot",
			Short: "Board cutout / slot (挖槽) — a MULTI-layer fill that mills a hole",
			Long: `Create a board cutout / slot (挖槽) — physically removes board material (e.g.
under an antenna for isolation, or a mechanical opening). Implemented as a
pcb_PrimitiveFill on the MULTI layer (12), which the EasyEDA manufacturing output
treats as a BoardCutout. Specify the area three ways (pick one): --points, --rect
x0,y0,x1,y1, or --ref <designator> (+ --margin to expand). Inspect / remove with
'pcb fill list --layer 12' / 'pcb fill delete'.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb slot --rect 2450,-1550,2700,-1400
  easyeda pcb slot --ref ANT1 --margin 20     # cut a slot under the antenna`,
			RunE: func(cmd *cobra.Command, args []string) error {
				points, err := areaPointsFrom(cfg, window, pointsJSON, rectSpec, ref, margin)
				if err != nil {
					return err
				}
				payload := map[string]any{"points": points, "layer": 12}
				if locked {
					payload["locked"] = true
				}
				return dispatch(cfg, "pcb.fill.create", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&pointsJSON, "points", "", `JSON array of [x,y] points in mil (or use --rect / --ref)`)
		c.Flags().StringVar(&rectSpec, "rect", "", "axis-aligned rect 'x0,y0,x1,y1' (mil)")
		c.Flags().StringVar(&ref, "ref", "", "designator of a placed component — slot over its bbox (e.g. an antenna)")
		c.Flags().Float64Var(&margin, "margin", 0, "expand the --rect/--ref box outward by this many mil")
		c.Flags().BoolVar(&locked, "locked", false, "create the slot locked")
		pcb.AddCommand(c)
	}

	return pcb
}

// parseRoutedNets extracts the set of nets that already have copper tracks from a
// pcb.line.list result, so route-short skips them.
func parseRoutedNets(result map[string]any) map[string]bool {
	out := map[string]bool{}
	var arr []any
	for _, key := range []string{"tracks", "lines"} {
		if a, ok := result[key].([]any); ok {
			arr = a
			break
		}
	}
	for _, ri := range arr {
		if m, ok := ri.(map[string]any); ok {
			if net := asString(m["net"]); net != "" {
				out[net] = true
			}
		}
	}
	return out
}

// parseApComps converts a pcb.components.list result (with includePads +
// includeBBox) into the planner's component slice. Missing/odd fields degrade to
// zero values; a component with no bbox is flagged so the planner skips it.
func parseApComps(result map[string]any) []apComp {
	raw, _ := result["components"].([]any)
	out := make([]apComp, 0, len(raw))
	for _, ri := range raw {
		cm, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		c := apComp{
			id:         asString(cm["primitiveId"]),
			designator: asString(cm["designator"]),
			x:          asFloat(cm["x"]),
			y:          asFloat(cm["y"]),
			rotation:   asFloat(cm["rotation"]),
			locked:     asBool(cm["locked"]),
		}
		if bb, ok := cm["bbox"].(map[string]any); ok {
			c.hasBBox = true
			c.minX, c.minY = asFloat(bb["minX"]), asFloat(bb["minY"])
			c.maxX, c.maxY = asFloat(bb["maxX"]), asFloat(bb["maxY"])
		}
		if pads, ok := cm["pads"].([]any); ok {
			for _, pi := range pads {
				pm, ok := pi.(map[string]any)
				if !ok {
					continue
				}
				c.pads = append(c.pads, apPad{
					num:   asString(pm["padNumber"]),
					net:   asString(pm["net"]),
					x:     asFloat(pm["x"]),
					y:     asFloat(pm["y"]),
					layer: int(asFloat(pm["layer"])),
				})
			}
		}
		out = append(out, c)
	}
	return out
}

// apMainDesignators lists which components the planner treats as anchors, for the report.
func apMainDesignators(comps []apComp, opt apOptions) []string {
	var out []string
	for _, c := range comps {
		if c.hasBBox && c.distinctPins() >= opt.mainPins {
			out = append(out, c.designator)
		}
	}
	return out
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}
