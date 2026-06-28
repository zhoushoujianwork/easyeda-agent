package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// ── autoconnect orchestration (I/O side; the scorer in cmd_sch_autoconnect.go is pure) ──

// acSpec is the batch `--spec` JSON shape (issue #24).
type acSpec struct {
	Connections []acSpecConn `json:"connections"`
	Rules       *acSpecRules `json:"rules"`
}

type acSpecConn struct {
	Pin  string   `json:"pin"`
	X    *float64 `json:"x"`
	Y    *float64 `json:"y"`
	Kind string   `json:"kind"`
	Net  string   `json:"net"`
}

// acSpecRules mirrors the rules block; pointer fields so an omitted key keeps the
// default instead of zeroing it.
type acSpecRules struct {
	AvoidTitleBlock *bool     `json:"avoidTitleBlock"`
	AvoidPinFanout  *bool     `json:"avoidPinFanout"`
	StaggerLabels   *bool     `json:"staggerLabels"`
	OffsetRange     []float64 `json:"offsetRange"`
	OffsetStep      *float64  `json:"offsetStep"`
	MinLabelGap     *float64  `json:"minLabelGap"`
}

// applyTo overlays the spec's rules onto a base ruleset.
func (r *acSpecRules) applyTo(base autoconnectRules) autoconnectRules {
	if r == nil {
		return base
	}
	if r.AvoidTitleBlock != nil {
		base.AvoidTitleBlock = *r.AvoidTitleBlock
	}
	if r.AvoidPinFanout != nil {
		base.AvoidPinFanout = *r.AvoidPinFanout
	}
	if r.StaggerLabels != nil {
		base.StaggerLabels = *r.StaggerLabels
	}
	if len(r.OffsetRange) == 2 {
		base.OffsetMin, base.OffsetMax = r.OffsetRange[0], r.OffsetRange[1]
	}
	if r.OffsetStep != nil {
		base.OffsetStep = *r.OffsetStep
	}
	if r.MinLabelGap != nil {
		base.MinLabelGap = *r.MinLabelGap
	}
	return base
}

// acConnSpec is the normalized form of one connection to plan, after merging CLI
// flags / spec entries.
type acConnSpec struct {
	PinRef string   // "U1:41" (for reporting / coordinate resolution); empty when explicit coords given
	X, Y   *float64 // explicit coordinate override
	Kind   string   // raw CLI/spec kind ("gnd", "power", "netport", …)
	Net    string
}

// acConnResult is the per-connection output (issue #24 result shape).
type acConnResult struct {
	Pin             string       `json:"pin,omitempty"`
	Net             string       `json:"net"`
	Kind            string       `json:"kind"`
	PinX            float64      `json:"pinX"`
	PinY            float64      `json:"pinY"`
	Selected        *acCandidate `json:"selected,omitempty"`
	Rejected        []acRejected `json:"rejected,omitempty"`
	WirePrimitiveID string       `json:"wirePrimitiveId,omitempty"`
	FlagPrimitiveID string       `json:"flagPrimitiveId,omitempty"`
	DryRun          bool         `json:"dryRun,omitempty"`
	Error           string       `json:"error,omitempty"`
}

type acRejected struct {
	Direction string  `json:"direction"`
	Offset    float64 `json:"offset"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
}

// acReport is the whole autoconnect run.
type acReport struct {
	OK                    bool           `json:"ok"`
	Connections           []acConnResult `json:"connections"`
	TitleBlockProvisional bool           `json:"titleBlockProvisional,omitempty"`
	Note                  string         `json:"note,omitempty"`
}

// buildScene pulls real geometry from schematic.components.list and assembles the
// scoring scene: part bboxes, every pin (tagged with owner), existing flag/port/
// label bboxes, and a title-block keep-out derived from the sheet bbox (or a
// reported provisional fallback when no sheet bbox is exposed).
func buildScene(result map[string]any) acScene {
	scene := acScene{}
	raw, _ := result["components"].([]any)
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
		case "part", "":
			if box != nil {
				scene.Parts = append(scene.Parts, *box)
			}
			designator := asString(m["designator"])
			if pins, ok := m["pins"].([]any); ok {
				for _, pp := range pins {
					pm, ok := pp.(map[string]any)
					if !ok {
						continue
					}
					scene.Pins = append(scene.Pins, acPin{
						X:          asFloat(pm["x"]),
						Y:          asFloat(pm["y"]),
						Designator: designator,
						PinNumber:  asString(pm["pinNumber"]),
						PinName:    asString(pm["pinName"]),
						OwnerBBox:  box,
					})
				}
			}
		case "netflag", "netport", "netlabel":
			if box != nil {
				scene.Flags = append(scene.Flags, *box)
			}
		case "sheet":
			if box != nil {
				sheet = box
			}
		}
	}
	scene.TitleBlock, scene.TitleBlockProvisional = titleBlockKeepout(sheet)
	return scene
}

// titleBlockKeepout derives the title-block keep-out for the autoconnect scorer.
// It delegates to deriveSheetGeometry (the issue #26 single source of the keep-out
// ratio) so the geometry is computed in exactly one place. When the sheet bbox is
// NOT exposed it reports a provisional fallback and applies NO geometric penalty
// (returning nil), so a guessed absolute box can't corrupt scoring — the caller
// still surfaces `titleBlockProvisional` so a human knows it was not enforced.
func titleBlockKeepout(sheet *layoutBBox) (*layoutBBox, bool) {
	if sheet == nil {
		return nil, true // provisional: not applied
	}
	g := deriveSheetGeometry(sheet, nil)
	if g.TitleBlock.BBox == nil {
		return nil, true // could not derive (e.g. degenerate bbox) → not enforced
	}
	return g.TitleBlock.BBox, false
}

// resolvePinCoord finds a pin's coordinate from a "designator:pinNumberOrName"
// reference against the scene's pins.
func resolvePinCoord(scene acScene, ref string) (acPin, error) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return acPin{}, fmt.Errorf("invalid --pin %q; expected DESIGNATOR:PIN, e.g. U1:41 or U1:3V3", ref)
	}
	desig, token := parts[0], parts[1]
	var matches []acPin
	for _, p := range scene.Pins {
		if p.Designator == desig && (p.PinNumber == token || p.PinName == token) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return acPin{}, fmt.Errorf("no pin %q found (component %q not placed, or pin number/name mismatch — check `easyeda sch list --include-pins`)", ref, desig)
	default:
		return acPin{}, fmt.Errorf("pin reference %q is ambiguous (%d matches); use the pin NUMBER instead of name", ref, len(matches))
	}
}

// runAutoconnect is the command core: build the scene once, then plan → (dispatch)
// each connection sequentially, staggering later labels off earlier placements.
func runAutoconnect(cfg *appConfig, window string, conns []acConnSpec, rules autoconnectRules, allPages, dryRun, asJSON bool, stdout, stderr io.Writer) error {
	res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{
		"includeBBox": true,
		"includePins": true,
		"allPages":    allPages,
	})
	if err != nil {
		return err
	}
	scene := buildScene(res.Result)

	report := acReport{OK: true, TitleBlockProvisional: scene.TitleBlockProvisional}
	if scene.TitleBlockProvisional && rules.AvoidTitleBlock {
		report.Note = "no sheet bbox exposed — title-block keep-out is provisional and was NOT geometrically enforced"
	}

	for _, c := range conns {
		cr := acConnResult{Net: c.Net, Kind: c.Kind, DryRun: dryRun}

		canonicalKind, kerr := resolveNetflagKind(c.Kind)
		if kerr != nil {
			cr.Error = kerr.Error()
			report.OK = false
			report.Connections = append(report.Connections, cr)
			continue
		}

		// Resolve pin coordinate: explicit --x/--y wins; else designator:pin.
		var pin acPin
		if c.X != nil && c.Y != nil {
			pin = acPin{X: *c.X, Y: *c.Y}
		} else if c.PinRef != "" {
			cr.Pin = c.PinRef
			p, perr := resolvePinCoord(scene, c.PinRef)
			if perr != nil {
				cr.Error = perr.Error()
				report.OK = false
				report.Connections = append(report.Connections, cr)
				continue
			}
			pin = p
		} else {
			cr.Error = "no pin coordinate: pass --pin DESIGNATOR:PIN or both --x and --y"
			report.OK = false
			report.Connections = append(report.Connections, cr)
			continue
		}
		cr.PinX, cr.PinY = pin.X, pin.Y

		all := planConnection(pin, canonicalKind, scene, rules)
		selected := all[0]
		cr.Selected = &selected
		cr.Rejected = summarizeRejected(all, selected)

		if !dryRun {
			payload := map[string]any{
				"pinX":      pin.X,
				"pinY":      pin.Y,
				"kind":      canonicalKind,
				"net":       c.Net,
				"direction": selected.Direction,
				"offset":    selected.Offset,
			}
			cres, cerr := requestAction(cfg, "schematic.power.connect_pin", window, payload)
			if cerr != nil {
				cr.Error = cerr.Error()
				report.OK = false
				report.Connections = append(report.Connections, cr)
				continue
			}
			cr.WirePrimitiveID = asString(cres.Result["wirePrimitiveId"])
			cr.FlagPrimitiveID = asString(cres.Result["flagPrimitiveId"])
		}

		// Stagger: register the just-placed label so later connections in this
		// batch avoid stacking on it (clustered-pin staggering).
		if rules.StaggerLabels {
			scene.Flags = append(scene.Flags, labelBox(selected.EndPoint.X, selected.EndPoint.Y))
		}

		report.Connections = append(report.Connections, cr)
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		renderAutoconnectReport(report, dryRun, stdout)
	}
	if !report.OK {
		return fmt.Errorf("autoconnect: %d connection(s) failed", countFailed(report))
	}
	return nil
}

// summarizeRejected returns the best-scoring candidate of each direction OTHER
// than the selected one, with its dominant reason — compact but representative.
func summarizeRejected(all []acCandidate, selected acCandidate) []acRejected {
	seen := map[string]bool{selected.Direction: true}
	var out []acRejected
	for _, c := range all {
		if seen[c.Direction] {
			continue
		}
		seen[c.Direction] = true
		out = append(out, acRejected{
			Direction: c.Direction,
			Offset:    c.Offset,
			Score:     c.Score,
			Reason:    dominantReason(c),
		})
	}
	return out
}

func countFailed(r acReport) int {
	n := 0
	for _, c := range r.Connections {
		if c.Error != "" {
			n++
		}
	}
	return n
}

func renderAutoconnectReport(r acReport, dryRun bool, w io.Writer) {
	mode := "connect"
	if dryRun {
		mode = "plan (dry-run)"
	}
	fmt.Fprintf(w, "autoconnect: %d connection(s), mode=%s\n", len(r.Connections), mode)
	if r.Note != "" {
		fmt.Fprintf(w, "  note: %s\n", r.Note)
	}
	for _, c := range r.Connections {
		id := c.Pin
		if id == "" {
			id = fmt.Sprintf("(%.2f,%.2f)", c.PinX, c.PinY)
		}
		if c.Error != "" {
			fmt.Fprintf(w, "  ✗ %s → %s [%s]: %s\n", id, c.Net, c.Kind, c.Error)
			continue
		}
		s := c.Selected
		fmt.Fprintf(w, "  ✓ %s → %s [%s]: %s offset=%.0f end=(%.2f,%.2f) score=%.2f\n",
			id, c.Net, c.Kind, s.Direction, s.Offset, s.EndPoint.X, s.EndPoint.Y, s.Score)
		if !dryRun {
			fmt.Fprintf(w, "      wire=%s flag=%s\n", c.WirePrimitiveID, c.FlagPrimitiveID)
		}
		for _, rj := range c.Rejected {
			fmt.Fprintf(w, "      rejected %-5s offset=%.0f score=%.2f — %s\n", rj.Direction, rj.Offset, rj.Score, rj.Reason)
		}
	}
}

// newAutoconnectCmd builds the `sch autoconnect` subcommand.
func newAutoconnectCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		pin, kind, net, spec         string
		x, y                         float64
		avoidTitleBlock, avoidFanout bool
		offsetMin, offsetMax, step   float64
		allPages, dryRun, asJSON     bool
	)
	c := &cobra.Command{
		Use:   "autoconnect",
		Short: "Pin-aware planner: auto-pick direction/offset and connect a pin (or batch) to a flag/netport",
		Long: `Pin-aware autoconnect planner.

connect_pin already guarantees the structural safety (pin → short wire →
flag/netport, so a netflag never sits on a bare pin and trips DRC). autoconnect
removes the remaining judgment call — which direction and offset — by turning it
into a deterministic geometry decision:

  1. Resolve the pin coordinate (DESIGNATOR:PIN, or explicit --x/--y).
  2. Enumerate every direction (up/down/left/right) × offset candidate.
  3. Score each against real geometry: part bboxes, pin coordinates, existing
     flag/port/label bboxes, and the title-block keep-out.
  4. Pick the lowest-cost candidate (deterministic tie-break) and delegate the
     actual mutation to connect_pin.

The scorer is pure and deterministic: the same schematic state + spec always
yields the same selection. Use --dry-run to see the plan (and rejected options)
without mutating.`,
		Args: cobra.NoArgs,
		Example: `  easyeda sch autoconnect --pin U1:41 --kind gnd --net GND
  easyeda sch autoconnect --x 720 --y 670 --kind gnd --net GND
  easyeda sch autoconnect --pin U1:3V3 --kind power --net +3V3 --dry-run
  easyeda sch autoconnect --spec p1-connect.json --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rules := defaultAutoconnectRules()
			if cmd.Flags().Changed("avoid-titleblock") {
				rules.AvoidTitleBlock = avoidTitleBlock
			}
			if cmd.Flags().Changed("avoid-pin-fanout") {
				rules.AvoidPinFanout = avoidFanout
			}
			if cmd.Flags().Changed("offset-min") {
				rules.OffsetMin = offsetMin
			}
			if cmd.Flags().Changed("offset-max") {
				rules.OffsetMax = offsetMax
			}
			if cmd.Flags().Changed("offset-step") {
				rules.OffsetStep = step
			}

			var conns []acConnSpec
			if spec != "" {
				raw, err := os.ReadFile(spec)
				if err != nil {
					return fmt.Errorf("read --spec: %w", err)
				}
				var s acSpec
				if err := json.Unmarshal(raw, &s); err != nil {
					return fmt.Errorf("invalid --spec json: %w", err)
				}
				if len(s.Connections) == 0 {
					return fmt.Errorf("--spec has no connections")
				}
				rules = s.Rules.applyTo(rules)
				for _, sc := range s.Connections {
					if sc.Kind == "" || sc.Net == "" {
						return fmt.Errorf("each spec connection needs kind and net (got pin=%q)", sc.Pin)
					}
					conns = append(conns, acConnSpec{PinRef: sc.Pin, X: sc.X, Y: sc.Y, Kind: sc.Kind, Net: sc.Net})
				}
			} else {
				if kind == "" || net == "" {
					return fmt.Errorf("--kind and --net are required (or use --spec)")
				}
				cs := acConnSpec{Kind: kind, Net: net}
				if pin != "" {
					cs.PinRef = pin
				} else if cmd.Flags().Changed("x") && cmd.Flags().Changed("y") {
					cs.X, cs.Y = &x, &y
				} else {
					return fmt.Errorf("pass --pin DESIGNATOR:PIN or both --x and --y")
				}
				conns = append(conns, cs)
			}

			return runAutoconnect(cfg, *window, conns, rules, allPages, dryRun, asJSON, stdout, stderr)
		},
	}
	c.Flags().StringVar(&pin, "pin", "", "pin reference DESIGNATOR:PIN (number or name), e.g. U1:41 or U1:3V3")
	c.Flags().Float64Var(&x, "x", 0, "explicit pin X coordinate (use with --y instead of --pin)")
	c.Flags().Float64Var(&y, "y", 0, "explicit pin Y coordinate (use with --x instead of --pin)")
	c.Flags().StringVar(&kind, "kind", "", netflagKindHelp)
	c.Flags().StringVar(&net, "net", "", "net name")
	c.Flags().StringVar(&spec, "spec", "", "batch spec JSON file ({connections:[...], rules:{...}})")
	c.Flags().BoolVar(&avoidTitleBlock, "avoid-titleblock", true, "penalize candidates whose label enters the title-block keep-out")
	c.Flags().BoolVar(&avoidFanout, "avoid-pin-fanout", true, "penalize candidates that run close to a pin fanout channel")
	c.Flags().Float64Var(&offsetMin, "offset-min", 18, "minimum stub offset to consider")
	c.Flags().Float64Var(&offsetMax, "offset-max", 80, "maximum stub offset to consider")
	c.Flags().Float64Var(&step, "offset-step", 6, "offset increment")
	c.Flags().BoolVar(&allPages, "all-pages", false, "build the scene from all schematic pages")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "plan and print the selection without mutating")
	c.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return c
}
