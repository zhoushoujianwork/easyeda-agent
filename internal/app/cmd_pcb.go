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

	// ── new-board ─────────────────────────────────────────────────────────
	// board.new_pcb — create a NEW board (板) with a fresh PCB page from a schematic.
	{
		var schematic, name string
		var force bool
		c := &cobra.Command{
			Use:   "new-board",
			Short: "Create a NEW board (板) with a fresh empty PCB page, bound to an UNBOUND schematic",
			Long: `Create a brand-new board (板) that CONTAINS a fresh, empty PCB page, bound to a
schematic — the CLI equivalent of the UI's 新建PCB / 原理图转PCB. You get a clean
board to lay out from scratch, still driven by the schematic netlist (switch to it,
then 'easyeda pcb import-changes').

IMPORTANT: a schematic can belong to only ONE board in EasyEDA Pro. If the target
schematic is ALREADY bound to a board, this command refuses (it would otherwise MOVE
the schematic into the new board and leave the old board with just its PCB). To lay
out another PCB for an already-bound schematic, work inside its existing board. Pass
--force only if you deliberately want to move the schematic into the new board.

Under the hood it runs the required 2-step SDK sequence (createBoard shell →
createPcb into that board — a one-shot createPcb is a silent no-op), with rollback
if the PCB can't be created. --schematic defaults to the CURRENT board's schematic,
so in a single-design project you can just run 'easyeda pcb new-board'.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb new-board
  easyeda pcb new-board --name ESP32-rev2
  easyeda pcb new-board --schematic de2bc6678317009f --name Proto
  easyeda pcb new-board --schematic de2bc6678317009f --force   # move an already-bound schematic`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if schematic != "" {
					payload["schematicUuid"] = schematic
				}
				if name != "" {
					payload["name"] = name
				}
				if force {
					payload["force"] = true
				}
				return dispatch(cfg, "board.new_pcb", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&schematic, "schematic", "", "schematic UUID to bind (default = current board's schematic)")
		c.Flags().StringVar(&name, "name", "", "name for the new board (default = auto, e.g. Board1_1)")
		c.Flags().BoolVar(&force, "force", false, "move the schematic into the new board even if it is already bound to another board")
		pcb.AddCommand(c)
	}

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
		var previousSha string
		c := &cobra.Command{
			Use:   "snapshot",
			Short: "Capture the active PCB canvas as a PNG artifact",
			Args:  cobra.NoArgs,
			Example: `  easyeda pcb snapshot
  easyeda pcb snapshot --fit=false
  easyeda view region --left 500 --right 1550 --top -1500 --bottom -2260 && easyeda pcb snapshot --fit=false --previous-sha256 <sha>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{"fit": fit}
				if previousSha != "" {
					payload["previousSha256"] = previousSha
				}
				res, err := dispatchCapture(cfg, "pcb.snapshot", window, payload, stdout)
				if err != nil {
					return err
				}
				warnIfBlankSnapshot(res, stderr)
				return nil
			},
		}
		c.Flags().BoolVar(&fit, "fit", true, "zoom-to-fit before capture (nudges a redraw)")
		c.Flags().StringVar(&previousSha, "previous-sha256", "", "sha256 of the previous snapshot; enables stale-frame detection + auto-retry")
		pcb.AddCommand(c)
	}
	// ── stage-snapshot: recording/demo stage capture (snapshot + data bundle) ──
	pcb.AddCommand(newPcbStageSnapshotCmd(cfg, &window, stdout, stderr))
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
		var gap, pitch, assemblyGap float64
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
				// rule (room for a legal track + clearances between parts). (#22) BUT
				// floor it at --assembly-gap so parts also keep hand-SOLDER room around
				// their pads — a bare DRC-clearance gap (~28mil) routes fine but is too
				// cramped to reach with an iron tip; default 40mil leaves that room.
				apRules := fetchPcbRules(cfg, window)
				opt.gap = math.Max(assemblyGap, apRules.clearanceMil*2+apRules.trackWidthMil+6)
				opt.pitch = math.Max(assemblyGap*0.7, apRules.clearanceMil+apRules.trackWidthMil)
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
		c.Flags().Float64Var(&gap, "gap", 0, "override: fixed clearance from chip edge to satellite (mil); 0 = derive")
		c.Flags().Float64Var(&pitch, "pitch", 0, "override: fixed spacing between satellites on the same edge (mil); 0 = derive")
		c.Flags().Float64Var(&assemblyGap, "assembly-gap", 40, "min hand-SOLDER clearance around each part (mil floor for gap/pitch)")
		c.Flags().BoolVar(&noRotate, "no-rotate", false, "do not re-orient satellites (v1 translate-only behavior)")
		c.Flags().Float64Var(&multiGap, "multi-gap", 0, "min bbox gap between multiple main chips (mil, default 150; 0 disables spacing)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "print the placement plan without moving anything")
		pcb.AddCommand(c)
	}

	// ── route-short ───────────────────────────────────────────────────────
	// Short-trace self-routing (daemon-side; see pcb_shortroute.go).
	{
		var maxLen, width, signalWidth, powerWidth, roundRadius float64
		var dryRun, routePower, noAvoid bool
		var corner string
		c := &cobra.Command{
			Use:   "route-short",
			Short: "Self-route the short, clear hops: per-net MST, L-shaped tracks on the pads' layer",
			Long: `Heuristic short-trace router, run in the daemon (the "heuristic tier" — NOT the
@alpha autoRouting() API, NOT an external Freerouting; that is 'pcb autoroute').
Per net it builds a minimum spanning tree over the pads and routes each hop that
is short enough (<= --max-len, Manhattan) as an L-shaped track on the pads'
shared layer. Skips: power+ground nets (VCC/3V3/GND/… — POURED, not routed as thin
tracks; --route-power to force), already-routed nets, cross-layer hops (need a via),
and over-long hops (left for the maze tier).
Obstacle-aware (v2): each hop picks the L orientation (horizontal- vs vertical-
first) that crosses the fewest already-placed other-net tracks + other-net pads,
which removes most of the naive tangle; --no-avoid restores the v1 horizontal-
first behavior. Still NOT a maze router (no push-shove / vias / rip-up) — run
AFTER 'pcb auto-place' (hops are then short and clear) and verify with 'pcb drc'.

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
				opt.skipPower = !routePower
				opt.avoid = !noAvoid
				opt.clearance = rules.clearanceMil
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
					"avoid":    opt.avoid,
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
		c.Flags().BoolVar(&noAvoid, "no-avoid", false, "disable obstacle-aware L-orientation (v1 naive horizontal-first)")
		c.Flags().BoolVar(&routePower, "route-power", false, "also route power/ground nets as tracks (default skip — pour them instead; VCC/3V3/GND/… routed as thin tracks through pad fields is the #1 DRC source)")
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

	// ── layout-lint (布局质量 + 可布性预测) ──────────────────────────────────
	// PCB sibling of `sch layout-lint`: overlap / off-board / tight-spacing PLUS a
	// routability score from the ratsnest (signal-net MST length + cross-net
	// crossings). Run BEFORE routing to catch a placement that won't route; exits
	// non-zero on overlap/off-board so it can gate the flow. Core in pcb_layoutlint.go.
	{
		var minGap float64
		var asJSON bool
		c := &cobra.Command{
			Use:   "layout-lint",
			Short: "Score PCB placement quality + predict routability (ratsnest crossings)",
			Long: `Check the PCB placement and predict how hard it will be to route — run this
BEFORE routing (or after auto-place) to catch a bad layout early.

Pulls every footprint's rendered bbox + pads (pcb.components.list) and computes:

  • overlap          — two footprint bboxes intersect                → ERROR (score 0)
  • off-board        — a footprint extends outside the board outline → ERROR
  • tight spacing    — bbox gap below --min-gap                      → WARN
  • ratsnest         — per signal-net minimum spanning tree (power/GND
                       excluded — they're poured, not routed)
  • crossings        — cross-net ratline segments that geometrically
                       cross → the single-layer routability killer   → WARN

Yields a 0-100 routability score + verdict (easy/moderate/hard/very-hard). Fewer
crossings + shorter ratsnest = more routable. --min-gap defaults to the board's
live track-to-pad clearance. Exits non-zero on any overlap/off-board (gate-able).`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb layout-lint
  easyeda pcb layout-lint --json
  easyeda pcb layout-lint --min-gap 8`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runPcbLayoutLint(cfg, window, minGap, asJSON, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&minGap, "min-gap", 0, "min gap between footprint bboxes in mil (closer = WARN; default = board clearance)")
		c.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
		pcb.AddCommand(c)
	}

	// ── check (DFM 审查:制造性/可靠性几何隐患) ─────────────────────────────
	// PCB sibling of `sch check`. Native `pcb drc` catches rule-clearance; this
	// reconstructs the DFM hazards it doesn't — acute (acid-trap) angles, dangling
	// copper stubs, stacked / pointless single-layer vias, 2-pin neck-down
	// asymmetry, duplicated overlapping copper — purely from placed primitives
	// (tracks + vias + pads). Read-only. Core in pcb_check.go.
	{
		var strict, asJSON bool
		var couplingW float64
		c := &cobra.Command{
			Use:   "check",
			Short: "DFM audit: acute angles / dangling copper / bad vias / neck-down / 3W coupling (read-only)",
			Long: `Reconstructed DFM (design-for-manufacture) audit — the manufacturability and
reliability hazards the native 'pcb drc' does NOT flag. Computed purely from the
placed copper (pcb.line.list + pcb.via.list + pcb.components.list --include-pads),
so it needs no extra setup and never mutates the board.

Rules:
  • dangling-end      — a track end anchored to no pad/via/track  → WARN
  • acute-angle       — two same-net segments bend <90° (acid trap) → WARN
  • overlapping-via   — two vias stacked on the same spot          → WARN
  • single-layer-via  — a signal via that changes no layer         → WARN
  • width-mismatch    — a 2-pin part with asymmetric neck-down     → INFO
  • duplicate-segment — collinear overlapping (redundant) copper   → WARN
  • parallel-coupling — different-net traces closer than N×W (3W rule) → WARN

Complements 'pcb drc' (rule clearance) and 'pcb layout-lint' (placement/routability).
Exit code: 0 by default (informational). --strict exits non-zero on any WARN/ERROR
so it can gate the flow. Arcs are out of scope for v1 (line/via/pad only).`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb check
  easyeda pcb check --json
  easyeda pcb check --strict
  easyeda pcb check --coupling-w 2.5`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runPcbCheck(cfg, window, couplingW, strict, asJSON, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&strict, "strict", false, "exit non-zero when there are issues (gate mode)")
		c.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
		c.Flags().Float64Var(&couplingW, "coupling-w", 3.0, "3W-rule factor: flag different-net parallel traces closer than this × trace width")
		pcb.AddCommand(c)
	}

	// ── stackup (层叠:层数 + 内层类型) ──────────────────────────────────────
	// pcb.stackup.set — set copper layer count + inner-layer types (signal/plane).
	// The foundation for multi-layer designs: a PLANE inner layer gives GND/power a
	// dedicated plane, which is the clean fix for the 2-layer pour-conflict (two
	// power nets can't both connect on one shared layer). Read via `pcb layers`.
	{
		stackup := &cobra.Command{
			Use:   "stackup",
			Short: "Board stackup: copper layer count + inner-layer types (signal/plane)",
			Long: `Configure the board stackup — the count of copper layers and the type of each
inner layer. A PLANE (内电层) inner layer is a solid negative plane, the clean way
to distribute GND + power on 4+ layer boards: each gets a dedicated plane instead
of two power nets fighting over one layer (the 2-layer pour conflict). Read the
current stackup with 'pcb layers' (copperLayerCount + each layer's type).`,
		}
		{
			c := &cobra.Command{
				Use:     "show",
				Short:   "Show the current stackup (copper layer count + layers)",
				Args:    cobra.NoArgs,
				Example: `  easyeda pcb stackup show`,
				RunE: func(cmd *cobra.Command, args []string) error {
					return dispatch(cfg, "pcb.layers.list", window, nil, stdout, stderr)
				},
			}
			stackup.AddCommand(c)
		}
		{
			var layers int
			var planes, signals []int
			c := &cobra.Command{
				Use:   "set",
				Short: "Set copper layer count and/or inner-layer types",
				Args:  cobra.NoArgs,
				Example: `  easyeda pcb stackup set --layers 4
  easyeda pcb stackup set --layers 4 --plane 15 --plane 16   # Inner1+Inner2 = planes (GND / power)
  easyeda pcb stackup set --signal 15                        # Inner1 back to a signal layer`,
				RunE: func(cmd *cobra.Command, args []string) error {
					payload := map[string]any{}
					if cmd.Flags().Changed("layers") {
						payload["count"] = layers
					}
					var specs []map[string]any
					for _, id := range planes {
						specs = append(specs, map[string]any{"id": id, "type": "plane"})
					}
					for _, id := range signals {
						specs = append(specs, map[string]any{"id": id, "type": "signal"})
					}
					if len(specs) > 0 {
						payload["layers"] = specs
					}
					if len(payload) == 0 {
						return fmt.Errorf("nothing to set — use --layers and/or --plane/--signal (ids from `easyeda pcb layers`)")
					}
					return dispatch(cfg, "pcb.stackup.set", window, payload, stdout, stderr)
				},
			}
			c.Flags().IntVar(&layers, "layers", 0, "copper layer count (2|4|6|…|32)")
			c.Flags().IntSliceVar(&planes, "plane", nil, "inner layer id to set as PLANE/内电层 (repeatable; ids from 'pcb layers', e.g. 15=Inner1)")
			c.Flags().IntSliceVar(&signals, "signal", nil, "inner layer id to set as SIGNAL (repeatable)")
			stackup.AddCommand(c)
		}
		pcb.AddCommand(stackup)
	}

	// ── power-planes (4层电源平面启发式) ────────────────────────────────────
	// The proper fix for the 2-layer pour conflict: dedicated inner planes + via
	// stitching. Ensures 4 layers, assigns GND + power nets to inner layers,
	// via-stitches every power pad down to its plane, pours each plane. Validated on
	// ceshi: DRC No-Connection → 0. Core in pcb_powerplanes.go.
	{
		var gndLayer, powerLayer int
		var dryRun, gndPlane bool
		c := &cobra.Command{
			Use:   "power-planes",
			Short: "4-layer power distribution: GND 内电层 + power inner plane + via-stitch (fixes 2-layer pour conflict)",
			Long: `Distribute power/ground on dedicated INNER PLANES — the clean 4-layer fix for the
2-layer pour conflict (two power nets can't both connect on one shared layer, which
stranded 5 of ceshi's 3V3 pads). This:

  1. ensures the board has >=4 copper layers,
  2. assigns GND to an inner layer and power nets (VCC/3V3/… via isGlobalNet) to another,
  3. via-stitches every power/ground pad DOWN to its plane (the connection point the
     inner pour needs — without it the inner pour is all isolated islands),
  4. pours each net on its inner layer,
  5. flips the GND inner layer to 内电层/PLANE (--gnd-plane, default on), then rebuilds.

Step 5 uses the verified pour-while-SIGNAL → flip-type → rebuild recipe: the net-bound
GND fill survives the flip and DRC stays clean (0 Plane-Zone/via clashes). The power
layer stays 信号层 so its net pour is an ordinary positive plane — matching the common
customer stackup (GND=内电层, VCC/3V3=信号层). Pass --gnd-plane=false to keep GND as a
plain signal-layer pour.

Validated on ceshi: DRC 31 → 0, No-Connection → 0. Run AFTER auto-place + outline-fit
+ route-short (signals). Two power nets sharing one plane layer re-create the conflict
(warned) — give each its own inner layer on a 6+ layer board. --dry-run prints the plan.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb power-planes
  easyeda pcb power-planes --gnd-layer 15 --power-layer 16
  easyeda pcb power-planes --gnd-plane=false   # keep GND as a signal-layer pour
  easyeda pcb power-planes --dry-run`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runPowerPlanes(cfg, window, gndLayer, powerLayer, gndPlane, dryRun, stdout, stderr)
			},
		}
		c.Flags().IntVar(&gndLayer, "gnd-layer", 15, "inner layer id for the GND plane (15=Inner1)")
		c.Flags().IntVar(&powerLayer, "power-layer", 16, "inner layer id for the power plane (16=Inner2)")
		c.Flags().BoolVar(&gndPlane, "gnd-plane", true, "flip the GND inner layer to 内电层/PLANE after pouring (customer-stackup correct)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan (nets→layers, pad counts) without mutating")
		pcb.AddCommand(c)
	}

	// ── outline-round (圆角板框) ────────────────────────────────────────────
	// Replace the board outline with a rounded rectangle (#29). Curves are chord-
	// approximated (pcb.outline.set takes a polygon). Core in pcb_outline_round.go.
	{
		var rectSpec string
		var radius, margin float64
		var segments int
		var dryRun bool
		c := &cobra.Command{
			Use:   "outline-round",
			Short: "Set a rounded-rectangle board outline (圆角板框)",
			Long: `Replace the board outline with a rounded rectangle. The rect defaults to the
CURRENT outline's bbox (or pass --rect x0,y0,x1,y1); --margin expands it outward.
--radius is the corner radius (default ≈12% of the shorter side, clamped to half).
Corners are chord-approximated (--segments per corner, default 6) since
pcb.outline.set takes a polygon. The board-outline layer renders → verify with
'pcb snapshot'. Run BEFORE pour/route (changing the outline after copper can strand it).`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb outline-round --radius 80
  easyeda pcb outline-round --rect 0,0,2000,1500 --radius 100 --segments 8
  easyeda pcb outline-round --margin 100 --radius 60 --dry-run`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runOutlineRound(cfg, window, rectSpec, radius, margin, segments, dryRun, stdout, stderr)
			},
		}
		c.Flags().StringVar(&rectSpec, "rect", "", "axis-aligned rect 'x0,y0,x1,y1' (mil); default = current outline bbox")
		c.Flags().Float64Var(&radius, "radius", 0, "corner radius (mil); default ≈12% of the shorter side")
		c.Flags().Float64Var(&margin, "margin", 0, "expand the rect outward by this many mil before rounding")
		c.Flags().IntVar(&segments, "segments", 6, "chord segments per 90° corner (higher = smoother)")
		c.Flags().BoolVar(&dryRun, "dry-run", false, "print the generated polygon without setting the outline")
		pcb.AddCommand(c)
	}

	// ── silk-align (丝印/位号对齐) ──────────────────────────────────────────
	// pcb.silk.align — reposition each designator to a consistent spot above/below
	// its footprint. Designators are component-bound attributes (pcb_PrimitiveAttribute).
	{
		var offset, spacing float64
		var side string
		var refs []string
		c := &cobra.Command{
			Use:   "silk-align",
			Short: "Align component designators (位号) with collision avoidance (no overlaps)",
			Long: `Reposition every component's DESIGNATOR silkscreen with COLLISION AVOIDANCE: for
each label it searches candidate slots around the footprint (preferred --side first,
then the other directions, at increasing distance) and takes the first that hits no
other component body and no already-placed label — so dense-cluster designators get
pushed into open space instead of piling on top of each other. --side (top|bottom|
left|right) biases the search, --offset is the base gap, --refs limits to specific
parts. Reports unresolvedCollisions (still-overlapping labels ⇒ the layout is too
dense — loosen placement). Verify with 'pcb snapshot'.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb silk-align
  easyeda pcb silk-align --side bottom --offset 15
  easyeda pcb silk-align --refs U1 --refs LED1`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if cmd.Flags().Changed("offset") {
					payload["offset"] = offset
				}
				if side != "" {
					payload["side"] = side
				}
				if len(refs) > 0 {
					payload["refs"] = refs
				}
				if cmd.Flags().Changed("spacing") {
					payload["spacing"] = spacing
				}
				return dispatch(cfg, "pcb.silk.align", window, payload, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&offset, "offset", 15, "base distance from the footprint edge (mil); ×spacing")
		c.Flags().Float64Var(&spacing, "spacing", 1.5, "spacing coefficient — scales the label drift for assembly/solder room (bigger = further out)")
		c.Flags().StringVar(&side, "side", "", "bias which side of the footprint: top|bottom|left|right (soft hint)")
		c.Flags().StringArrayVar(&refs, "refs", nil, "limit to these designators (repeatable); default = all")
		pcb.AddCommand(c)
	}

	// pcb.silk.add — create a free silkscreen string (board marking / credit / note).
	{
		var text string
		var x, y, fontSize, lineWidth, rotation float64
		var layer int
		c := &cobra.Command{
			Use:   "silk-add",
			Short: "Add a free silkscreen string (board marking / credit / note) with config",
			Long: `Create a FREE silkscreen STRING at (x,y) — a board credit / label / note — with
full config: --layer (3=top silk default, 4=bottom), --font-size (mil), --line-width
(stroke mil), --rotation. The defaults (font 40 / stroke 6) are legible + JLCPCB-safe;
a small font with a thick stroke smears the glyphs together (糊). Returns the new
primitiveId + rendered bbox — check it fits the board and clears parts. Reposition or
restyle later with 'pcb silk-set'.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb silk-add --text "auto created by easyeda-agent" --x 1850 --y -2455
  easyeda pcb silk-add --text "REV A" --x 2400 --y -2455 --font-size 50 --line-width 6
  easyeda pcb silk-add --text "bottom mark" --x 2000 --y -2000 --layer 4 --rotation 90`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if text == "" {
					return fmt.Errorf("--text is required")
				}
				payload := map[string]any{"text": text, "x": x, "y": y, "layer": layer}
				if cmd.Flags().Changed("font-size") {
					payload["fontSize"] = fontSize
				}
				if cmd.Flags().Changed("line-width") {
					payload["lineWidth"] = lineWidth
				}
				if cmd.Flags().Changed("rotation") {
					payload["rotation"] = rotation
				}
				return dispatch(cfg, "pcb.silk.add", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&text, "text", "", "the silkscreen text (required)")
		c.Flags().Float64Var(&x, "x", 0, "X position (mil)")
		c.Flags().Float64Var(&y, "y", 0, "Y position (mil)")
		c.Flags().IntVar(&layer, "layer", 3, "silk layer: 3=TOP_SILKSCREEN, 4=BOTTOM_SILKSCREEN")
		c.Flags().Float64Var(&fontSize, "font-size", 40, "font height (mil; ≥~32 for JLCPCB legibility)")
		c.Flags().Float64Var(&lineWidth, "line-width", 6, "stroke width (mil; JLCPCB min ~6)")
		c.Flags().Float64Var(&rotation, "rotation", 0, "rotation (deg)")
		pcb.AddCommand(c)
	}

	// pcb.silk.set — batch reconfigure existing silk (position / rotation / size / text
	// / align-to-reference).
	{
		var ids string
		var x, y, rotation, fontSize, lineWidth float64
		var text, align, ref string
		c := &cobra.Command{
			Use:   "silk-set",
			Short: "Batch-adjust existing silk: position / rotation / size / text, or align to a reference",
			Long: `Reconfigure existing silkscreen primitive(s) in ONE batch — component designators
(位号) and free strings alike. --ids is a JSON array of primitiveIds (from 'pcb
check --json' or a silk list); set any of --x/--y/--rotation/--font-size/--line-width
/--text and ONLY those keys change.

ALIGN shortcut (--align + --ref): position each silk relative to a reference bbox —
--ref a component designator, "board"/"outline", or "fill" (default board). --align:
center|mid (both axes), centerx|centery, or left|right|top|bottom (edge-align). Each
silk is computed from ITS OWN bbox so the center/edge lands exactly on the reference.

NOTE: rotation via the reliable .modify persists, but a 'pcb snapshot' taken before a
document reload shows the OLD orientation (stale render) — judge success by 'pcb check'
/ silk list, not a screenshot.`,
			Args: cobra.NoArgs,
			Example: `  easyeda pcb silk-set --ids '["id1"]' --rotation 0
  easyeda pcb silk-set --ids '["credit"]' --ref board --align centerx   # center the board credit
  easyeda pcb silk-set --ids '["lbl"]' --ref U1 --align top             # align label to U1's top
  easyeda pcb silk-set --ids '["id1"]' --font-size 45 --line-width 6`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if ids == "" {
					return fmt.Errorf("--ids is required (JSON array of primitiveIds)")
				}
				var idList []string
				if err := json.Unmarshal([]byte(ids), &idList); err != nil {
					return fmt.Errorf("--ids must be a JSON array of strings: %w", err)
				}
				payload := map[string]any{"primitiveIds": idList}
				for flag, key := range map[string]string{"x": "x", "y": "y", "rotation": "rotation", "font-size": "fontSize", "line-width": "lineWidth"} {
					if cmd.Flags().Changed(flag) {
						switch key {
						case "x":
							payload[key] = x
						case "y":
							payload[key] = y
						case "rotation":
							payload[key] = rotation
						case "fontSize":
							payload[key] = fontSize
						case "lineWidth":
							payload[key] = lineWidth
						}
					}
				}
				if cmd.Flags().Changed("text") {
					payload["text"] = text
				}
				if align != "" {
					payload["align"] = align
					if ref != "" {
						payload["ref"] = ref
					}
				}
				return dispatch(cfg, "pcb.silk.set", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&ids, "ids", "", "JSON array of silk primitiveIds to adjust (required)")
		c.Flags().Float64Var(&x, "x", 0, "new X (mil)")
		c.Flags().Float64Var(&y, "y", 0, "new Y (mil)")
		c.Flags().Float64Var(&rotation, "rotation", 0, "new rotation (deg) — 0 = upright")
		c.Flags().Float64Var(&fontSize, "font-size", 0, "new font height (mil)")
		c.Flags().Float64Var(&lineWidth, "line-width", 0, "new stroke width (mil)")
		c.Flags().StringVar(&text, "text", "", "new text (designators: the value)")
		c.Flags().StringVar(&align, "align", "", "align to --ref: center|mid|centerx|centery|left|right|top|bottom")
		c.Flags().StringVar(&ref, "ref", "", "align reference: a designator, \"board\"/\"outline\", or \"fill\" (default board)")
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
