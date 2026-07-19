package app

// cmd_sch_block_apply_run.go — the I/O side of `sch block-apply` (the planner in
// cmd_sch_block_apply.go is pure).
//
// Pipeline, matching the vertical slice in chat/2026-07-16-blocks-data-model.md:
//   1. load the block (go:embed, offline)
//   2. resolve parts → standard-parts.json (the role-id → deviceUuid bridge)
//   3. read the page's existing designators so a second instance never collides
//   4. plan (pure)
//   5. place each role via schematic.component.place --designator (atomic)
//   6. wire internal_nets by delegating to the autoconnect planner, which already
//      owns the geometry safety (pin → stub wire → flag, never a flag on a bare pin)
//   7. schematic.check
//   8. emit the instance manifest

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

// bapManifest is the command's output: what was built, from which block
// revision, and — deliberately — what was NOT honoured.
type bapManifest struct {
	OK         string         `json:"ok"`
	BlockID    string         `json:"blockId"`
	Revision   int            `json:"revision,omitempty"`
	BlockState string         `json:"blockState"` // ready | verified | draft — never let a draft look production-ready
	Instance   string         `json:"instance"`
	Placed     []bapPlacement `json:"placed"`
	Nets       []bapNet       `json:"nets"`
	Unconsumed []string       `json:"unconsumedConstraints,omitempty"`
	Note       string         `json:"note,omitempty"`
	// Reconciled is true when the post-apply netlist read-back matched every
	// planned net (issue #135); Diffs carries the mismatches when it did not.
	Reconciled bool         `json:"reconciled,omitempty"`
	Diffs      []bapNetDiff `json:"diffs,omitempty"`
}

// loadStandardParts reads the parts library into the role-id → device bridge.
func loadStandardParts(path string) (map[string]bapDevice, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		LibraryUUID string `json:"libraryUuid"`
		Parts       map[string]struct {
			MPN        string `json:"mpn"`
			LCSC       string `json:"lcsc"`
			Value      any    `json:"value"`
			DeviceUUID string `json:"deviceUuid"`
			Basic      bool   `json:"basic"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make(map[string]bapDevice, len(doc.Parts))
	for k, p := range doc.Parts {
		out[k] = bapDevice{
			LibraryUUID: doc.LibraryUUID,
			DeviceUUID:  p.DeviceUUID,
			MPN:         p.MPN,
			LCSC:        p.LCSC,
			Value:       asString(p.Value),
			Basic:       p.Basic,
		}
	}
	return out, nil
}

// blockTopology pulls internal_nets out of the block's raw JSON. The typed
// projection deliberately keeps it in Raw (unknown maps stay forward-compatible),
// so the executable core parses it here.
func blockTopology(b blocks.Block) ([][]string, error) {
	var doc struct {
		InternalNets [][]string `json:"internal_nets"`
	}
	if err := json.Unmarshal(b.Raw, &doc); err != nil {
		return nil, fmt.Errorf("parse internal_nets: %w", err)
	}
	return doc.InternalNets, nil
}

// parseKV splits repeatable KEY=VALUE flags.
func parseKV(items []string, flag string) (map[string]string, error) {
	out := map[string]string{}
	for _, it := range items {
		k, v, ok := strings.Cut(it, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("%s %q: expected KEY=VALUE", flag, it)
		}
		out[k] = v
	}
	return out, nil
}

// existingDesignators reads the WHOLE document (all schematic pages) so
// allocation can skip taken designators. Active-page-only scanning caused issue
// #136: an instance on a fresh page allocated C1/R1/U1 colliding with another
// page's parts, and the document-wide netlist (keyed by designator.pin) then
// mis-attributed every collided pin's net. Non-active pages return shallow data
// but the designator field is always present, which is all this needs.
func existingDesignators(cfg *appConfig, window string) (map[string]bool, error) {
	res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{"allPages": true})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	raw, _ := res.Result["components"].([]any)
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if d := asString(m["designator"]); d != "" {
			out[strings.ToUpper(d)] = true
		}
	}
	return out, nil
}

// runBlockApply is the command core.
func runBlockApply(cfg *appConfig, window, blockID string, in bapInput, partsPath string,
	dryRun, asJSON bool, stdout, stderr io.Writer) error {

	b, ok, err := blocks.Get(blockID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no such block %q — `easyeda blocks ls` to list", blockID)
	}
	in.Block = b

	if in.Topology, err = blockTopology(b); err != nil {
		return err
	}
	if len(in.Topology) == 0 {
		return fmt.Errorf("block %s has no internal_nets — nothing to wire", b.ID)
	}

	path, err := resolveStandardParts(partsPath)
	if err != nil {
		return err
	}
	if in.Devices, err = loadStandardParts(path); err != nil {
		return err
	}

	// A dry run must not need a window: the point is to inspect the plan. Only
	// the designator scan needs the page, so fall back to an empty page.
	if !dryRun || window != "" || cfg.project != "" {
		if in.Existing, err = existingDesignators(cfg, window); err != nil {
			if !dryRun {
				return err
			}
			fmt.Fprintf(stderr, "warn: could not read the page (%v) — planning against an empty page\n", err)
			in.Existing = map[string]bool{}
		}
	}

	plan, err := planBlockApply(in)
	if err != nil {
		return err
	}

	man := bapManifest{
		OK: "planned", BlockID: plan.BlockID, Revision: plan.Revision,
		BlockState: b.Status(), Instance: plan.Instance,
		Placed: plan.Placements, Nets: plan.Nets, Unconsumed: plan.Unconsumed,
	}
	if len(plan.Unconsumed) > 0 {
		man.Note = "block-apply v1 executes parts/internal_nets/ports only; the listed constraint maps were NOT applied"
	}

	if dryRun {
		man.OK = "dry-run"
		return emitBapManifest(man, asJSON, stdout)
	}

	// A draft block's pin names are, by definition, not verified against the real
	// symbols. Placing it is legitimate (that is how a draft gets validated) but it
	// must be said out loud, not discovered when the wiring silently misses.
	if b.Status() == "draft" {
		fmt.Fprintf(stderr, "warn: block %s is a DRAFT — its pin names are unverified; expect autoconnect to fail on any wrong name\n", b.ID)
	}

	// 5. place
	for _, p := range plan.Placements {
		payload := map[string]any{
			"libraryUuid": p.LibraryUUID,
			"uuid":        p.DeviceUUID,
			"x":           p.X,
			"y":           p.Y,
			"designator":  p.Designator,
		}
		if _, err := requestActionTimed(cfg, "schematic.component.place", window, payload, placeTimeout); err != nil {
			return fmt.Errorf("place %s (%s): %w", p.Designator, p.PartKey, err)
		}
		fmt.Fprintf(stderr, "placed %-6s %-18s @ %.0f,%.0f\n", p.Designator, p.PartKey, p.X, p.Y)
	}

	// 6. wire — delegate to autoconnect, which owns the stub geometry + idempotency.
	var conns []acConnSpec
	for _, n := range plan.Nets {
		for _, m := range n.Members {
			conns = append(conns, acConnSpec{PinRef: m, Kind: n.Kind, Net: n.Net})
		}
	}
	// autoconnect's per-connection report goes to STDERR, not io.Discard: when a
	// connection fails, that report IS the diagnosis (which pin, which candidates
	// were rejected and why). Discarding it leaves a bare "1 connection(s) failed"
	// and forces the caller to re-run each pin by hand to find out anything.
	// stdout stays clean for the manifest.
	if err := runAutoconnect(cfg, window, conns, defaultAutoconnectRules(), false, false, false, false, stderr, stderr); err != nil {
		return fmt.Errorf("wire: %w", err)
	}

	// 7. check
	man.OK = "applied"
	if _, err := requestAction(cfg, "schematic.check", window, map[string]any{}); err != nil {
		fmt.Fprintf(stderr, "warn: schematic.check failed to run: %v\n", err)
	}

	// 8. reconcile the live netlist against the plan (issue #135). Per-stub wiring
	// success is not topology success: EasyEDA merges touching wires, and a merged
	// short has slipped past BOTH check and bridge-check before. The netlist is
	// the authority; a mismatch fails the command instead of hiding behind the
	// green per-stub report.
	liveNets, pinNumbers, rerr := readLiveNets(cfg, window)
	if rerr != nil {
		fmt.Fprintf(stderr, "warn: could not read back the netlist to reconcile (%v) — verify with `easyeda sch read` manually\n", rerr)
	} else {
		diffs := reconcileBlockNets(plan, liveNets, pinNumbers)
		man.Diffs = diffs
		man.Reconciled = len(diffs) == 0
		if len(diffs) > 0 {
			man.OK = "applied-mismatch"
			for _, d := range diffs {
				fmt.Fprintf(stderr, "reconcile ✗ net %s: missing %s", d.Net, strings.Join(d.Missing, ", "))
				for pin, other := range d.FoundIn {
					fmt.Fprintf(stderr, " (%s landed in %q — likely a merged-wire short)", pin, other)
				}
				fmt.Fprintln(stderr)
			}
			if err := emitBapManifest(man, asJSON, stdout); err != nil {
				return err
			}
			return fmt.Errorf("block-apply: %d net(s) do not match the plan — run `easyeda sch bridge-check` and fix before trusting this instance", len(diffs))
		}
		fmt.Fprintf(stderr, "reconcile ✓ %d net(s) match the live netlist\n", len(plan.Nets))
	}

	return emitBapManifest(man, asJSON, stdout)
}

// readLiveNets pulls the post-wiring truth via schematic.read: live net → set of
// "DESIGNATOR.NUMBER" members, plus each component's pin name/number → number
// map (plan members reference pins by NAME; the netlist speaks numbers).
func readLiveNets(cfg *appConfig, window string) (map[string]map[string]bool, map[string]map[string]string, error) {
	res, err := requestAction(cfg, "schematic.read", window, map[string]any{"includeCheck": false})
	if err != nil {
		return nil, nil, err
	}
	liveNets := map[string]map[string]bool{}
	if nets, ok := res.Result["nets"].([]any); ok {
		for _, n := range nets {
			m, ok := n.(map[string]any)
			if !ok {
				continue
			}
			name := asString(m["net"])
			if name == "" {
				continue
			}
			set := map[string]bool{}
			if pins, ok := m["pins"].([]any); ok {
				for _, p := range pins {
					if s := asString(p); s != "" {
						set[s] = true
					}
				}
			}
			liveNets[name] = set
		}
	}
	pinNumbers := map[string]map[string]string{}
	if comps, ok := res.Result["components"].([]any); ok {
		for _, c := range comps {
			m, ok := c.(map[string]any)
			if !ok {
				continue
			}
			desig := strings.ToUpper(asString(m["designator"]))
			if desig == "" {
				continue
			}
			byRef := pinNumbers[desig]
			if byRef == nil {
				byRef = map[string]string{}
				pinNumbers[desig] = byRef
			}
			if pins, ok := m["pins"].([]any); ok {
				for _, p := range pins {
					pm, ok := p.(map[string]any)
					if !ok {
						continue
					}
					num := asString(pm["number"])
					if num == "" {
						num = asString(pm["pinNumber"])
					}
					if num == "" {
						continue
					}
					byRef[strings.ToUpper(num)] = num
					if name := asString(pm["name"]); name != "" {
						byRef[strings.ToUpper(name)] = num
					} else if name := asString(pm["pinName"]); name != "" {
						byRef[strings.ToUpper(name)] = num
					}
				}
			}
		}
	}
	return liveNets, pinNumbers, nil
}

func emitBapManifest(m bapManifest, asJSON bool, stdout io.Writer) error {
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	}
	fmt.Fprintf(stdout, "%s  %s", m.OK, m.BlockID)
	if m.Revision > 0 {
		fmt.Fprintf(stdout, " rev%d", m.Revision)
	}
	fmt.Fprintf(stdout, "  [%s]  instance=%s\n", m.BlockState, m.Instance)
	fmt.Fprintf(stdout, "\n%-6s %-8s %-20s %s\n", "REF", "ROLE", "PART", "AT")
	for _, p := range m.Placed {
		fmt.Fprintf(stdout, "%-6s %-8s %-20s %.0f,%.0f\n", p.Designator, p.Role, p.PartKey, p.X, p.Y)
	}
	fmt.Fprintf(stdout, "\n%-14s %-9s %s\n", "NET", "KIND", "MEMBERS")
	for _, n := range m.Nets {
		tag := ""
		if n.Port != "" {
			tag = " (port " + n.Port + ")"
			if n.Bound {
				tag = " (port " + n.Port + ", bound)"
			}
		}
		fmt.Fprintf(stdout, "%-14s %-9s %s%s\n", n.Net, n.Kind, strings.Join(n.Members, " "), tag)
	}
	if len(m.Unconsumed) > 0 {
		fmt.Fprintf(stdout, "\nNOT applied (v1 scope): %s\n", strings.Join(m.Unconsumed, ", "))
	}
	return nil
}

// newSchBlockApplyCmd builds the cobra command.
func newSchBlockApplyCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		at, instance, partsPath string
		binds, kinds            []string
		spacing                 float64
		perRow                  int
		dryRun, asJSON          bool
	)
	c := &cobra.Command{
		Use:   "block-apply <block-id>",
		Short: "Instantiate a circuit block: place its parts, wire internal_nets, bind ports",
		Long: `Instantiate a standard circuit block onto the active schematic page.

Loads the block from the embedded library, resolves each role to a real device via
standard-parts.json, places the parts with allocated designators, wires the block's
internal_nets, binds its boundary ports to host nets, and prints a traceable
instance manifest.

SCOPE (v1): parts / internal_nets / ports only. A block's pcb_layout, placement,
signals and silk maps are NOT applied — the manifest lists them under
"NOT applied" so a green exit never reads as "the whole block was honoured".

EACH RUN CREATES A NEW INSTANCE — this command is NOT idempotent, by design: two
LEDs means running it twice. Designators are allocated around whatever is already
in the DOCUMENT — all schematic pages, not just the active one, because the
netlist is keyed by designator.pin document-wide and a cross-page collision
poisons every net attribution (issue #136). Each instance's PORT-less internal nets are
named after its own first designator (LED1_N2 vs LED2_N2) so instances never
merge. Re-running after a partial failure therefore does NOT repair that instance,
it builds another one; fix a half-built instance with ` + "`sch autoconnect`" + ` on the
pins that are missing, or delete it and start over.

Wiring itself is delegated to the ` + "`sch autoconnect`" + ` planner, which IS
idempotent per pin — an already-connected pin is skipped rather than re-flagged.`,
		Args: cobra.ExactArgs(1),
		Example: `  easyeda sch block-apply led_indicator_gpio --dry-run
  easyeda sch block-apply led_indicator_gpio --at 400,300 --bind CTRL=IO2 --bind GND=GND
  easyeda sch block-apply block.led_indicator_gpio --instance led2 --at 400,500 --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			bind, err := parseKV(binds, "--bind")
			if err != nil {
				return err
			}
			kindRaw, err := parseKV(kinds, "--kind")
			if err != nil {
				return err
			}
			kindOver := map[string]string{}
			for net, k := range kindRaw {
				if _, err := resolveNetflagKind(k); err != nil {
					return err
				}
				kindOver[strings.ToUpper(net)] = k
			}
			x, y, err := parseXY(at)
			if err != nil {
				return err
			}
			in := bapInput{
				Instance: instance, OriginX: x, OriginY: y,
				Spacing: spacing, PerRow: perRow, Bind: bind, KindOver: kindOver,
			}
			return runBlockApply(cfg, *window, args[0], in, partsPath, dryRun, asJSON, stdout, stderr)
		},
	}
	c.Flags().StringVar(&at, "at", "400,300", "origin coordinate x,y for the first part")
	c.Flags().Float64Var(&spacing, "spacing", 100, "grid spacing between placed parts")
	c.Flags().IntVar(&perRow, "per-row", 4, "parts per row before wrapping")
	c.Flags().StringArrayVar(&binds, "bind", nil, "bind a block PORT to a host net: --bind CTRL=IO2 (repeatable)")
	c.Flags().StringArrayVar(&kinds, "kind", nil, "override a net's flag kind: --kind LED_CTRL=netport (repeatable)")
	c.Flags().StringVar(&instance, "instance", "", "instance id used to name internal nets (default: the first allocated designator, e.g. LED1 → LED1_N2)")
	c.Flags().StringVar(&partsPath, "parts", "", "path to standard-parts.json (auto-detected if omitted)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "plan and print without placing or wiring")
	c.Flags().BoolVar(&asJSON, "json", false, "emit the instance manifest as JSON")
	return c
}

// parseXY parses an "x,y" flag.
func parseXY(s string) (float64, float64, error) {
	xs, ys, ok := strings.Cut(s, ",")
	if !ok {
		return 0, 0, fmt.Errorf("--at %q: expected x,y", s)
	}
	var x, y float64
	if _, err := fmt.Sscanf(strings.TrimSpace(xs), "%g", &x); err != nil {
		return 0, 0, fmt.Errorf("--at %q: bad x", s)
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(ys), "%g", &y); err != nil {
		return 0, 0, fmt.Errorf("--at %q: bad y", s)
	}
	return x, y, nil
}

