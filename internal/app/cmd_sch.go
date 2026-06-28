package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

// placeTimeout fails `sch place` fast instead of waiting out the full default
// window. A successful placement returns near-instantly; a hang almost always
// means the EasyEDA API never settled on a bad {libraryUuid, uuid} — most often
// because --uuid is a placed-instance id (from `sch list`) rather than a device
// library uuid (from `lib search`). See placeUUIDHint.
const placeTimeout = 8 * time.Second

// placeUUIDHint translates a bare deadline-exceeded into an actionable message:
// the most common cause of a hung placement is replaying an instance uuid that
// `sch list` exposes (component/symbol/footprint/uniqueId) instead of the
// device-library uuid that `lib search` returns.
func placeUUIDHint(timeout time.Duration) error {
	return fmt.Errorf(
		"placement timed out after %s — the EasyEDA API never returned for this {libraryUuid, uuid}.\n"+
			"This usually means --uuid is NOT a device-library uuid. The component/symbol/footprint/uniqueId\n"+
			"fields from `easyeda sch list` are placed-INSTANCE ids and cannot be replayed into `sch place`.\n"+
			"Get a replayable device uuid first: `easyeda lib search --query \"<part>\"` → use its `uuid` + `libraryUuid`.",
		timeout,
	)
}

// netflagKindAliases maps user-friendly CLI shorthands to the canonical kind
// enum the connector (extension/src/actions.ts NET_FLAG_KINDS / NET_PORT_KINDS)
// actually accepts. Canonical names also pass through unchanged so both
// `--kind gnd` and `--kind ground` work. Keep this list in sync with the
// connector's accepted set to avoid CLI↔connector drift.
var netflagKindAliases = map[string]string{
	// shorthands
	"gnd":     "ground",
	"agnd":    "analog_ground",
	"pgnd":    "protective_ground",
	"netport": "net_port_bi", // bidirectional port is the most general default
	// canonical passthrough (connector-native names)
	"power":             "power",
	"ground":            "ground",
	"analog_ground":     "analog_ground",
	"protective_ground": "protective_ground",
	"protect_ground":    "protect_ground",
	"net_port_in":       "net_port_in",
	"net_port_out":      "net_port_out",
	"net_port_bi":       "net_port_bi",
}

// netflagKindHelp is the single source of truth for the --kind help text so the
// listed values stay in sync with what resolveNetflagKind actually accepts.
const netflagKindHelp = "flag kind (required). Shorthands: gnd→ground, agnd→analog_ground, " +
	"pgnd→protective_ground, netport→net_port_bi. Canonical: power, ground, analog_ground, " +
	"protective_ground, protect_ground, net_port_in, net_port_out, net_port_bi"

// resolveNetflagKind translates a CLI --kind value (shorthand or canonical) to
// the canonical kind the connector accepts. Unknown values get a friendly CLI
// error listing every valid value, instead of leaking the raw connector error.
func resolveNetflagKind(kind string) (string, error) {
	if canonical, ok := netflagKindAliases[kind]; ok {
		return canonical, nil
	}
	valid := []string{
		"gnd", "agnd", "pgnd", "netport",
		"power", "ground", "analog_ground", "protective_ground", "protect_ground",
		"net_port_in", "net_port_out", "net_port_bi",
	}
	return "", fmt.Errorf("unknown --kind %q; expected one of: %v", kind, valid)
}

// newSchCmd returns the "sch" subcommand group with all schematic actions.
// --window is a persistent flag on the group so every subcommand inherits it.
func newSchCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	sch := &cobra.Command{
		Use:   "sch",
		Short: "Schematic operations",
	}
	sch.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")

	// ── pages ────────────────────────────────────────────────────────────
	// schematic.pages.list
	sch.AddCommand(&cobra.Command{
		Use:   "pages",
		Short: "List schematic documents and pages in the current project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "schematic.pages.list", window, nil, stdout, stderr)
		},
	})

	// ── open ─────────────────────────────────────────────────────────────
	// schematic.page.open
	{
		var page string
		c := &cobra.Command{
			Use:     "open",
			Short:   "Open or activate a schematic page by UUID",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch open --page 6b3a2f01-...`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page == "" {
					return fmt.Errorf("--page is required")
				}
				return dispatch(cfg, "schematic.page.open", window,
					map[string]any{"schematicPageUuid": page}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&page, "page", "", "schematic page UUID (required)")
		sch.AddCommand(c)
	}

	// ── titleblock get ─────────────────────────────────────────────────────
	// schematic.titleblock.get — 明细表读取（含可编辑字段 key）
	{
		var page string
		c := &cobra.Command{
			Use:   "titleblock-get",
			Short: "Read a page's 明细表 (title block): show flag + field keys/values",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch titleblock-get
  easyeda sch titleblock-get --page <pageUuid>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				var payload map[string]any
				if page != "" {
					payload = map[string]any{"pageUuid": page}
				}
				return dispatch(cfg, "schematic.titleblock.get", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&page, "page", "", "schematic page UUID (default: focused page)")
		sch.AddCommand(c)
	}

	// ── titleblock modify ──────────────────────────────────────────────────
	// schematic.titleblock.modify — 明细表调整（显隐 + 字段值）
	{
		var dataJSON string
		var show, hide bool
		c := &cobra.Command{
			Use:   "titleblock",
			Short: "Adjust the focused page's 明细表 (title block): visibility and/or fields",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch titleblock --show
  easyeda sch titleblock --hide
  easyeda sch titleblock --data '{"Title":{"value":"电源模块"},"Designer":{"value":"Mika"}}'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if show && hide {
					return fmt.Errorf("--show and --hide are mutually exclusive")
				}
				payload := map[string]any{}
				if show {
					payload["showTitleBlock"] = true
				}
				if hide {
					payload["showTitleBlock"] = false
				}
				if dataJSON != "" {
					var data map[string]any
					if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
						return fmt.Errorf("invalid --data json: %w", err)
					}
					payload["titleBlockData"] = data
				}
				if len(payload) == 0 {
					return fmt.Errorf("pass at least one of --show / --hide / --data")
				}
				return dispatch(cfg, "schematic.titleblock.modify", window, payload, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&show, "show", false, "show the title block")
		c.Flags().BoolVar(&hide, "hide", false, "hide the title block")
		c.Flags().StringVar(&dataJSON, "data", "", `JSON of fields to patch, e.g. '{"Title":{"value":"..."}}'`)
		sch.AddCommand(c)
	}

	// ── page-new ───────────────────────────────────────────────────────────
	// schematic.page.create
	{
		var schUuid string
		c := &cobra.Command{
			Use:     "page-new",
			Short:   "Create a new schematic page under a schematic document",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch page-new --schematic <schematicUuid>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if schUuid == "" {
					return fmt.Errorf("--schematic is required")
				}
				return dispatch(cfg, "schematic.page.create", window,
					map[string]any{"schematicUuid": schUuid}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&schUuid, "schematic", "", "parent schematic document UUID (required)")
		sch.AddCommand(c)
	}

	// ── page-rename ────────────────────────────────────────────────────────
	// schematic.page.rename
	{
		var page, name string
		c := &cobra.Command{
			Use:     "page-rename",
			Short:   "Rename a schematic page",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch page-rename --page <pageUuid> --name "电源"`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page == "" {
					return fmt.Errorf("--page is required")
				}
				if name == "" {
					return fmt.Errorf("--name is required")
				}
				return dispatch(cfg, "schematic.page.rename", window,
					map[string]any{"pageUuid": page, "name": name}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&page, "page", "", "schematic page UUID (required)")
		c.Flags().StringVar(&name, "name", "", "new page name (required)")
		sch.AddCommand(c)
	}

	// ── page-delete ────────────────────────────────────────────────────────
	// schematic.page.delete
	{
		var page string
		c := &cobra.Command{
			Use:     "page-delete",
			Short:   "Delete a schematic page (no undo)",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch page-delete --page <pageUuid>`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if page == "" {
					return fmt.Errorf("--page is required")
				}
				return dispatch(cfg, "schematic.page.delete", window,
					map[string]any{"pageUuid": page}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&page, "page", "", "schematic page UUID (required)")
		sch.AddCommand(c)
	}

	// ── clear ──────────────────────────────────────────────────────────────
	// schematic.page.clear
	{
		var noPreserveSheet, dryRun bool
		c := &cobra.Command{
			Use:   "clear",
			Short: "Clear the active schematic page (delete all page primitives: components, flags, wires, buses, graphics)",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch clear                      # clear the page, keep the sheet/title block (图框)
  easyeda sch clear --dry-run            # report what would be deleted, delete nothing
  easyeda sch clear --no-preserve-sheet  # also delete the sheet/title block`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return dispatch(cfg, "schematic.page.clear", window, map[string]any{
					"preserveSheet": !noPreserveSheet,
					"dryRun":        dryRun,
				}, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&dryRun, "dry-run", false, "report counts without deleting anything")
		c.Flags().BoolVar(&noPreserveSheet, "no-preserve-sheet", false, "also delete the sheet/title block (图框); by default it is kept")
		sch.AddCommand(c)
	}

	// ── rename (whole schematic document) ──────────────────────────────────
	// schematic.rename
	{
		var schUuid, name string
		c := &cobra.Command{
			Use:     "rename",
			Short:   "Rename a schematic document (the whole sheet, not a single page)",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch rename --schematic <schematicUuid> --name "主原理图"`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if schUuid == "" {
					return fmt.Errorf("--schematic is required")
				}
				if name == "" {
					return fmt.Errorf("--name is required")
				}
				return dispatch(cfg, "schematic.rename", window,
					map[string]any{"schematicUuid": schUuid, "name": name}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&schUuid, "schematic", "", "schematic document UUID (required)")
		c.Flags().StringVar(&name, "name", "", "new schematic name (required)")
		sch.AddCommand(c)
	}

	// ── list ─────────────────────────────────────────────────────────────
	// schematic.components.list
	{
		var allPages, includeBBox, includePins bool
		c := &cobra.Command{
			Use:   "list",
			Short: "List components on the active (or all) schematic page(s)",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch list
  easyeda sch list --all-pages
  easyeda sch list --include-bbox
  easyeda sch list --include-pins`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if allPages {
					payload["allPages"] = true
				}
				if includeBBox {
					payload["includeBBox"] = true
				}
				if includePins {
					payload["includePins"] = true
				}
				if len(payload) == 0 {
					return dispatch(cfg, "schematic.components.list", window, nil, stdout, stderr)
				}
				return dispatch(cfg, "schematic.components.list", window, payload, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&allPages, "all-pages", false, "list components across all schematic pages")
		c.Flags().BoolVar(&includeBBox, "include-bbox", false, "attach each component's rendered extent {minX,minY,maxX,maxY}")
		c.Flags().BoolVar(&includePins, "include-pins", false, "attach each pin's {pinName,pinNumber,x,y,noConnected} — the data plane for routing/connectivity checks (output grows, esp. with --all-pages)")
		sch.AddCommand(c)
	}

	// ── place ─────────────────────────────────────────────────────────────
	// schematic.component.place
	{
		var lib, uuid string
		var x, y, rotation float64
		var mirror bool
		c := &cobra.Command{
			Use:   "place",
			Short: "Place a component from the device library at coordinates",
			Args:  cobra.NoArgs,
			Long: `Place a device/component from the EasyEDA device library at coordinates.

--uuid MUST be a device-library uuid (from ` + "`easyeda lib search`" + `), NOT one of
the uuid-looking fields ` + "`component`/`symbol`/`footprint`/`uniqueId`" + ` that
` + "`easyeda sch list`" + ` reports — those are placed-INSTANCE ids and are not valid
` + "`sch place`" + ` inputs. Passing an instance uuid makes the EasyEDA API hang; this
command fails fast after a short timeout with a hint instead of stalling.`,
			Example: `  easyeda sch place --lib <libraryUuid> --uuid <deviceUuid> --x 100 --y 200
  easyeda sch place --lib <l> --uuid <u> --x 100 --y 200 --rotation 90 --mirror`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if lib == "" {
					return fmt.Errorf("--lib is required")
				}
				if uuid == "" {
					return fmt.Errorf("--uuid is required")
				}
				payload := map[string]any{
					"libraryUuid": lib,
					"uuid":        uuid,
					"x":           x,
					"y":           y,
				}
				if cmd.Flags().Changed("rotation") {
					payload["rotation"] = rotation
				}
				if cmd.Flags().Changed("mirror") {
					payload["mirror"] = mirror
				}
				err := dispatchTimed(cfg, "schematic.component.place", window, payload, placeTimeout, stdout, stderr)
				if err != nil && errors.Is(err, context.DeadlineExceeded) {
					return placeUUIDHint(placeTimeout)
				}
				return err
			},
		}
		c.Flags().StringVar(&lib, "lib", "", "library UUID (required)")
		c.Flags().StringVar(&uuid, "uuid", "", "device UUID within the library (required)")
		c.Flags().Float64Var(&x, "x", 0, "X coordinate")
		c.Flags().Float64Var(&y, "y", 0, "Y coordinate")
		c.Flags().Float64Var(&rotation, "rotation", 0, "rotation in degrees (0/90/180/270)")
		c.Flags().BoolVar(&mirror, "mirror", false, "mirror the component")
		sch.AddCommand(c)
	}

	// ── modify ────────────────────────────────────────────────────────────
	// schematic.component.modify
	{
		var id, patchJSON string
		c := &cobra.Command{
			Use:   "modify",
			Short: "Modify component position, designator, BOM flags, or custom properties",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch modify --id <primitiveId> --patch '{"x":150,"y":200}'
  easyeda sch modify --id <id> --patch '{"customAttributes":{"Value":"10k"}}'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if id == "" {
					return fmt.Errorf("--id is required")
				}
				if patchJSON == "" {
					return fmt.Errorf("--patch is required")
				}
				var patch map[string]any
				if err := json.Unmarshal([]byte(patchJSON), &patch); err != nil {
					return fmt.Errorf("invalid --patch json: %w", err)
				}
				return dispatch(cfg, "schematic.component.modify", window,
					map[string]any{"primitiveId": id, "patch": patch}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&id, "id", "", "primitive ID to modify (required)")
		c.Flags().StringVar(&patchJSON, "patch", "", "JSON object with fields to update (required)")
		sch.AddCommand(c)
	}

	// ── delete ────────────────────────────────────────────────────────────
	// schematic.component.delete
	{
		var idsJSON string
		c := &cobra.Command{
			Use:     "delete",
			Short:   "Delete schematic component primitives",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch delete --ids '["id1","id2"]'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if idsJSON == "" {
					return fmt.Errorf("--ids is required")
				}
				var ids []any
				if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
					return fmt.Errorf("invalid --ids json (expected array): %w", err)
				}
				return dispatch(cfg, "schematic.component.delete", window,
					map[string]any{"primitiveIds": ids}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&idsJSON, "ids", "", `JSON array of primitive IDs to delete (required), e.g. '["id1","id2"]'`)
		sch.AddCommand(c)
	}

	// ── prim-delete ─────────────────────────────────────────────────────────
	// schematic.primitives.delete
	{
		var idsJSON string
		c := &cobra.Command{
			Use:   "prim-delete",
			Short: "Delete schematic primitives of ANY type by id (or the current selection if --ids omitted)",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch prim-delete --ids '["id1","id2"]'   # delete these (any primitive type)
  easyeda sch prim-delete                         # delete the current selection`,
			RunE: func(cmd *cobra.Command, args []string) error {
				payload := map[string]any{}
				if idsJSON != "" {
					var ids []any
					if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
						return fmt.Errorf("invalid --ids json (expected array): %w", err)
					}
					payload["primitiveIds"] = ids
				}
				return dispatch(cfg, "schematic.primitives.delete", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&idsJSON, "ids", "", `JSON array of primitive IDs to delete (any type); omit to delete the current selection`)
		sch.AddCommand(c)
	}

	// ── wire ──────────────────────────────────────────────────────────────
	// schematic.wire.create
	{
		var pointsJSON, net, styleJSON string
		c := &cobra.Command{
			Use:   "wire",
			Short: "Create a schematic wire polyline",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch wire --points '[[100,200],[100,300]]'        # nested pairs
  easyeda sch wire --points '[100,200,100,300]'            # flat (also accepted)
  easyeda sch wire --points '[[100,200],[100,300]]' --net VCC`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if pointsJSON == "" {
					return fmt.Errorf("--points is required")
				}
				var points []any
				if err := json.Unmarshal([]byte(pointsJSON), &points); err != nil {
					return fmt.Errorf("invalid --points json (expected array): %w", err)
				}
				payload := map[string]any{"points": points}
				if net != "" {
					payload["net"] = net
				}
				if cmd.Flags().Changed("style") {
					var style map[string]any
					if err := json.Unmarshal([]byte(styleJSON), &style); err != nil {
						return fmt.Errorf("invalid --style json: %w", err)
					}
					payload["style"] = style
				}
				return dispatch(cfg, "schematic.wire.create", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&pointsJSON, "points", "", `JSON coordinate list, nested '[[x,y],...]' or flat '[x1,y1,x2,y2,...]' (connector normalizes; required)`)
		c.Flags().StringVar(&net, "net", "", "net name to assign to the wire")
		c.Flags().StringVar(&styleJSON, "style", "", "JSON object with wire style overrides")
		sch.AddCommand(c)
	}

	// ── netflag ───────────────────────────────────────────────────────────
	// schematic.netflag.create
	{
		var kind, net string
		var x, y, rotation float64
		c := &cobra.Command{
			Use:   "netflag",
			Short: "Create a power/ground/net flag or port",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch netflag --kind power --net VCC --x 100 --y 200
  easyeda sch netflag --kind gnd --net GND --x 100 --y 100 --rotation 180`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if kind == "" {
					return fmt.Errorf("--kind is required")
				}
				if net == "" {
					return fmt.Errorf("--net is required")
				}
				canonicalKind, err := resolveNetflagKind(kind)
				if err != nil {
					return err
				}
				payload := map[string]any{
					"kind": canonicalKind,
					"net":  net,
					"x":    x,
					"y":    y,
				}
				if cmd.Flags().Changed("rotation") {
					payload["rotation"] = rotation
				}
				return dispatch(cfg, "schematic.netflag.create", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&kind, "kind", "", netflagKindHelp)
		c.Flags().StringVar(&net, "net", "", "net name (required)")
		c.Flags().Float64Var(&x, "x", 0, "X coordinate")
		c.Flags().Float64Var(&y, "y", 0, "Y coordinate")
		c.Flags().Float64Var(&rotation, "rotation", 0, "rotation in degrees")
		sch.AddCommand(c)
	}

	// ── connect ───────────────────────────────────────────────────────────
	// schematic.power.connect_pin
	{
		var kind, net, direction string
		var x, y, offset, rotation float64
		c := &cobra.Command{
			Use:   "connect",
			Short: "Stub a wire out of a pin and place a netflag/netport at its far end",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch connect --x 100 --y 200 --kind power --net VCC
  easyeda sch connect --x 100 --y 200 --kind gnd --net GND --direction down --offset 40`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if kind == "" {
					return fmt.Errorf("--kind is required")
				}
				if net == "" {
					return fmt.Errorf("--net is required")
				}
				canonicalKind, err := resolveNetflagKind(kind)
				if err != nil {
					return err
				}
				payload := map[string]any{
					"pinX": x,
					"pinY": y,
					"kind": canonicalKind,
					"net":  net,
				}
				if cmd.Flags().Changed("direction") {
					payload["direction"] = direction
				}
				if cmd.Flags().Changed("offset") {
					payload["offset"] = offset
				}
				if cmd.Flags().Changed("rotation") {
					payload["rotation"] = rotation
				}
				return dispatch(cfg, "schematic.power.connect_pin", window, payload, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&x, "x", 0, "pin X coordinate")
		c.Flags().Float64Var(&y, "y", 0, "pin Y coordinate")
		c.Flags().StringVar(&kind, "kind", "", netflagKindHelp)
		c.Flags().StringVar(&net, "net", "", "net name (required)")
		c.Flags().StringVar(&direction, "direction", "", "visual stub direction (up=higher on canvas, down=lower): up, down, left, right")
		c.Flags().Float64Var(&offset, "offset", 0, "wire length in schematic units")
		c.Flags().Float64Var(&rotation, "rotation", 0, "flag rotation override in degrees")
		sch.AddCommand(c)
	}

	// ── no-connect ──────────────────────────────────────────────────────────
	// schematic.pin.set_no_connect
	{
		var designator string
		var pins []string
		var clear bool
		c := &cobra.Command{
			Use:   "no-connect",
			Short: "Mark (or clear) a pin's no-connect flag (非连接标识)",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch no-connect --designator U1 --pin 23
  easyeda sch no-connect --designator U1 --pin 23,24,25
  easyeda sch no-connect --designator U1 --pin 23 --clear`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if designator == "" {
					return fmt.Errorf("--designator is required")
				}
				if len(pins) == 0 {
					return fmt.Errorf("--pin is required (one or more pin numbers)")
				}
				anyPins := make([]any, len(pins))
				for i, p := range pins {
					anyPins[i] = p
				}
				payload := map[string]any{
					"designator": designator,
					"pins":       anyPins,
				}
				if clear {
					payload["noConnected"] = false
				}
				return dispatch(cfg, "schematic.pin.set_no_connect", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&designator, "designator", "", "component designator, e.g. U1 (required)")
		c.Flags().StringSliceVar(&pins, "pin", nil, "pin number(s); repeat the flag or comma-separate (required)")
		c.Flags().BoolVar(&clear, "clear", false, "clear the no-connect flag instead of setting it")
		sch.AddCommand(c)
	}

	// ── select ────────────────────────────────────────────────────────────
	// schematic.select
	{
		var idsJSON string
		c := &cobra.Command{
			Use:     "select",
			Short:   "Select schematic primitives by ID",
			Args:    cobra.NoArgs,
			Example: `  easyeda sch select --ids '["id1","id2"]'`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if idsJSON == "" {
					return fmt.Errorf("--ids is required")
				}
				var ids []any
				if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
					return fmt.Errorf("invalid --ids json (expected array): %w", err)
				}
				return dispatch(cfg, "schematic.select", window,
					map[string]any{"primitiveIds": ids}, stdout, stderr)
			},
		}
		c.Flags().StringVar(&idsJSON, "ids", "", `JSON array of primitive IDs to select (required)`)
		sch.AddCommand(c)
	}

	// ── snapshot ──────────────────────────────────────────────────────────
	// schematic.snapshot
	{
		var noFit bool
		var previousSha string
		c := &cobra.Command{
			Use:   "snapshot",
			Short: "Capture the current schematic view as an image artifact",
			Long: `Capture the current schematic view as an image artifact.

Zooms to fit all primitives (适应全部) before capturing BY DEFAULT, so the whole
sheet lands in frame without a separate view.fit — pass --no-fit to keep the
current viewport.

For a PARTIAL / zoomed-in shot, frame the area first with "easyeda view region
--left --right --top --bottom" (or "view zoom --x --y --scale"), then capture
with --no-fit so the snapshot keeps that viewport instead of zooming back out.
The snapshot now waits for the canvas to repaint before grabbing the frame, so
"view region && sch snapshot --no-fit" reliably captures the requested region
(issue #20).

STALE FRAMES: EasyEDA does NOT auto-redraw after API edits, so a capture can be
byte-identical to a previous one even though the page changed. The result now
includes a frame "sha256" — pass it back via --previous-sha256 on the next
snapshot and the connector will detect a byte-identical (stale) frame, retry
once after another redraw, and report stale=true if it is still identical. Also
compare primitiveCount and judge STATE by data (sch list/getAll), not the pixels.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch snapshot           # auto fit-to-all, then capture
  easyeda sch snapshot --no-fit  # keep the current viewport (partial shot)
  easyeda view region --left 100 --right 400 --top 500 --bottom 200 && easyeda sch snapshot --no-fit`,
			RunE: func(cmd *cobra.Command, args []string) error {
				// Auto-fit is built into the schematic.snapshot action (default on);
				// the CLI just forwards the opt-out so a single round-trip both fits
				// and captures.
				payload := map[string]any{"fit": !noFit}
				if previousSha != "" {
					payload["previousSha256"] = previousSha
				}
				return dispatch(cfg, "schematic.snapshot", window, payload, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&noFit, "no-fit", false, "do NOT zoom to fit before capturing (keep current viewport)")
		c.Flags().StringVar(&previousSha, "previous-sha256", "", "sha256 of the previous snapshot; enables stale-frame detection + auto-retry")
		sch.AddCommand(c)
	}

	// ── drc ───────────────────────────────────────────────────────────────
	// schematic.drc.check
	{
		var strict, verbose, asJSON bool
		c := &cobra.Command{
			Use:   "drc",
			Short: "Run the official schematic DRC SDK gate (may be boolean/aggregate only)",
			Long: `Run the official schematic DRC SDK gate.

Current EasyEDA builds may return only boolean/aggregate data even when the SDK
type declares verbose per-item detail. The connector normalizes whatever the SDK
returns, but 'sch drc' must not be treated as the full UI DRC warning list.

Use 'easyeda sch check' for reconstructed per-item warnings such as floating pins
and net-marker/wire-name mismatches.

Exit code: non-zero ONLY when the fatal count (error + fatal severities) is > 0.
Warnings alone exit 0, so the design-flow S5 gate can demand "0 fatal" while
still surfacing warnings for review.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch drc
  easyeda sch drc --strict          # treat warnings as errors (SDK strict mode)
  easyeda sch drc --json            # normalized SDK result
  easyeda sch check --json          # reconstructed per-item warnings`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSchDrc(cfg, window, strict, verbose, asJSON, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&strict, "strict", false, "treat warnings as errors (SDK strict mode)")
		c.Flags().BoolVar(&verbose, "verbose", false, "also print each violation's raw EDA object")
		c.Flags().BoolVar(&asJSON, "json", false, "emit the normalized report as JSON")
		sch.AddCommand(c)
	}

	// ── check ─────────────────────────────────────────────────────────────
	// schematic.check — reconstructed per-item design check (floating pins, …).
	// Fills the gap the SDK schematic DRC API can't: eda.sch_Drc.check returns
	// only an aggregate, so the itemized findings the UI panel shows are computed
	// here from primitives. Output (designator + pin numbers) feeds `sch no-connect`.
	{
		var allPages, strict, asJSON bool
		c := &cobra.Command{
			Use:   "check",
			Short: "Reconstructed per-item design check the SDK DRC can't itemize",
			Long: `Reconstructed per-item design check — the detail the EDA schematic DRC API can't expose.

eda.sch_Drc.check (what 'sch drc' uses) may return only a boolean/aggregate result;
the itemized findings the UI DRC panel shows are not in any public API. 'sch check'
recomputes them from primitives and the official manufacture netlist JSON.

Covered rules include net-marker/wire-name mismatches, duplicate/multiple net names
on a wire, floating pins (netlist-confirmed and geometric), wire crossings, and
wire-over-pin hazards.

The floating-pin output is the exact input 'sch no-connect' takes, so the loop is:
sch check → wire the real ones / sch no-connect the intentional ones → sch check.

Exit code: 0 by default (floating IO pins are normal until NC-marked); --strict
exits non-zero when there are any findings, to use it as a gate.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch check
  easyeda sch check --json
  easyeda sch check --strict      # non-zero exit if any findings`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSchCheck(cfg, window, allPages, strict, asJSON, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&allPages, "all-pages", false, "check components across all schematic pages")
		c.Flags().BoolVar(&strict, "strict", false, "exit non-zero when there are findings (gate mode)")
		c.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
		sch.AddCommand(c)
	}

	// ── save ──────────────────────────────────────────────────────────────
	// schematic.save
	sch.AddCommand(&cobra.Command{
		Use:   "save",
		Short: "Save the active schematic document",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "schematic.save", window, nil, stdout, stderr)
		},
	})

	// ── layout-lint ───────────────────────────────────────────────────────
	// Go-side placement check on schematic.components.list(includeBBox). The
	// "teeth" of the verify→adjust loop: detect bbox overlaps (ERROR) and
	// too-tight spacing (WARN) so layout overlap is mechanically caught, not
	// eyeballed. Exits non-zero when overlaps exist → usable as a gate.
	{
		var minGap float64
		var asJSON, allPages, includeNonParts bool
		c := &cobra.Command{
			Use:   "layout-lint",
			Short: "Check component placement for bbox overlaps and tight spacing",
			Long: `Check component placement on the schematic for overlaps and tight spacing.

Pulls every component's rendered extent (schematic.components.list --include-bbox)
and runs two pairwise checks in Go:

  • overlap  — two component bounding boxes intersect            → ERROR
  • spacing  — bbox gap is below --min-gap (default 2.54mm)      → WARN

Only real parts (componentType "part") are checked by default. The drawing
sheet / title block (图框) spans the whole page, so including it would false-flag
an overlap against nearly every component; netflag/netport/netlabel and other
non-part primitives are likewise excluded. Pass --include-non-parts to score them
too (e.g. to inspect the sheet bbox).

This is the mechanical ground truth for the place→verify→adjust loop: run it
after each placement stage, fix every ERROR (move/align/distribute), then re-run.
Exits non-zero when any overlap is found, so it can gate a workflow.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch layout-lint
  easyeda sch layout-lint --min-gap 5.08
  easyeda sch layout-lint --all-pages --json`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runLayoutLint(cfg, window, minGap, allPages, asJSON, includeNonParts, stdout, stderr)
			},
		}
		c.Flags().Float64Var(&minGap, "min-gap", 2.54, "minimum gap between component bboxes in mm (closer = WARN)")
		c.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
		c.Flags().BoolVar(&allPages, "all-pages", false, "lint components across all schematic pages")
		c.Flags().BoolVar(&includeNonParts, "include-non-parts", false, "also lint non-part primitives (sheet/title-frame, netflag/netport/…); excluded by default")
		sch.AddCommand(c)
	}

	// ── sheet-geometry ────────────────────────────────────────────────────
	// Normalized sheet bounds + title-block keep-out (issue #26). The single
	// source placement/routing planners (autoconnect/autolayout) consume, so A4
	// coordinates aren't re-hardcoded per tool. Derives the keep-out from the
	// live sheet bbox + a known-template ratio, with explicit provenance +
	// warnings (never false precision). Pure core in cmd_sch_sheet.go.
	{
		var asJSON bool
		c := &cobra.Command{
			Use:   "sheet-geometry",
			Short: "Report sheet bounds + title-block keep-out geometry (provenance-tagged)",
			Long: `Report the schematic sheet's bounds and the title-block (图框/明细表) keep-out.

Placement/routing planners must avoid dropping flags or parts on top of the
title block. EasyEDA Pro exposes no set-paper-size API and no separate bbox for
the title block, so the geometry is DERIVED:

  • sheet bbox  — live, from the componentType "sheet" primitive
  • template    — matched best-effort by the sheet's aspect ratio (A-series ≈ √2)
  • title block — a corner sub-rect from the matched template's normalized ratio
  • visibility  — schematic.titleblock.get → showTitleBlock (hidden ⇒ no keep-out)

The result tags provenance (known-template-ratio / fallback-ratio / none) and
emits warnings instead of false precision when geometry can't be determined.
The keepouts[] format is what sch autoconnect / autolayout consume.`,
			Args: cobra.NoArgs,
			Example: `  easyeda sch sheet-geometry
  easyeda sch sheet-geometry --json`,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runSheetGeometry(cfg, window, asJSON, stdout, stderr)
			},
		}
		c.Flags().BoolVar(&asJSON, "json", false, "emit the geometry as JSON")
		sch.AddCommand(c)
	}

	// ── autoconnect ───────────────────────────────────────────────────────
	// Pin-aware deterministic connect planner: pick direction/offset by scoring
	// real geometry, then delegate to schematic.power.connect_pin. Scorer is pure
	// (cmd_sch_autoconnect.go); orchestration in cmd_sch_autoconnect_run.go.
	sch.AddCommand(newAutoconnectCmd(cfg, &window, stdout, stderr))

	// ── autolayout ──────────────────────────────────────────────────────────
	// Module-aware deterministic placement planner: partition the canvas into
	// named zones, place each module's core IC + peripherals with collision
	// retry, preserve pin-fanout channels + the title-block keep-out, and emit
	// lint-clean coordinates. Pure planner in cmd_sch_autolayout.go; I/O +
	// --apply in cmd_sch_autolayout_run.go. See issue #25.
	sch.AddCommand(newAutolayoutCmd(cfg, &window, stdout, stderr))

	// ── netlist ───────────────────────────────────────────────────────────
	// schematic.export.netlist
	{
		var netlistType string
		c := &cobra.Command{
			Use:   "netlist",
			Short: "Export schematic netlist as an artifact",
			Args:  cobra.NoArgs,
			Example: `  easyeda sch netlist
  easyeda sch netlist --type kicad`,
			RunE: func(cmd *cobra.Command, args []string) error {
				var payload map[string]any
				if netlistType != "" {
					payload = map[string]any{"netlistType": netlistType}
				}
				return dispatch(cfg, "schematic.export.netlist", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&netlistType, "type", "", "netlist format (e.g. kicad, spice, protel)")
		sch.AddCommand(c)
	}

	return sch
}
