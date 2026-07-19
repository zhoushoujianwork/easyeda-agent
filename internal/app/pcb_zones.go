package app

// pcb_zones.go — functional zones as a first-class PCB object (issue #126).
//
// The S0 spec's `modules[].zone` (MCU 区 / 电源区 / RF 区 …) was only consumed by
// the SCHEMATIC autolayout; PCB placement never read it, so the architectural
// partitioning decided at S0 simply did not exist at P2 — nothing placed by
// zone, and nothing could say "you put an RF part inside the digital zone".
//
// This file makes the claim table executable:
//   - `pcb zones set --spec <s0-spec.json>` (or --module NAME=ZONE:D1,D2 …)
//     persists module → {grid zone, designators} into the project workflow
//     state (~/.easyeda-agent/workflow/<project>.json — same store the stage
//     gates use, so the daemon and every CLI cwd agree);
//   - `pcb place-constrained` consumes it (mains anchored into their zone,
//     satellites legalized within it — edge parts exempt, the board edge is a
//     harder constraint than the zone);
//   - `pcb check` gains a zone-violation rule (WARN): a claimed part whose bbox
//     center sits outside its zone's sub-rectangle, with the 规范 §3.3
//     (模拟/数字分区) reference.
//
// Zone names reuse the schematic autolayout's grid vocabulary (docs/concepts.md
// 共享词汇表): columns left/center/right × rows top/bottom, plus full-height /
// full-width forms. The rectangle is resolved from the LIVE board outline at
// consumption time, so the same claim keeps working when the outline changes.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/workflow"
)

type stageZoneClaim = workflow.ZoneClaim

// pcbZoneNames is the accepted grid vocabulary (mirrors sch autolayout zoneRect).
var pcbZoneNames = map[string]bool{
	"left": true, "center": true, "right": true,
	"top": true, "bottom": true,
	"left-top": true, "left-bottom": true,
	"center-top": true, "center-bottom": true,
	"right-top": true, "right-bottom": true,
}

// pcbZoneRect maps a grid zone name to its sub-rectangle of the board rect.
// Columns: left [0,1/3), center [1/3,2/3), right [2/3,1]. Rows: this codebase's
// PCB canvas convention is top = MAX Y (see nearestEdge/placeEdgePart in
// pcb_place_constrained.go), so "top" is the upper-Y half — the OPPOSITE row
// mapping of the schematic zoneRect (sheet coords are y-down).
func pcbZoneRect(zone string, b cpRect) (cpRect, bool) {
	if !pcbZoneNames[zone] {
		return cpRect{}, false
	}
	w := b.x1 - b.x0
	h := b.y1 - b.y0
	col := [2]float64{0, 1} // x fractions
	row := [2]float64{0, 1} // y fractions, 0 = y0 (bottom), 1 = y1 (top)
	switch {
	case strings.HasPrefix(zone, "left"):
		col = [2]float64{0, 1.0 / 3}
	case strings.HasPrefix(zone, "center"):
		col = [2]float64{1.0 / 3, 2.0 / 3}
	case strings.HasPrefix(zone, "right"):
		col = [2]float64{2.0 / 3, 1}
	}
	switch {
	case strings.HasSuffix(zone, "top"):
		row = [2]float64{0.5, 1}
	case strings.HasSuffix(zone, "bottom"):
		row = [2]float64{0, 0.5}
	}
	return cpRect{
		x0: b.x0 + w*col[0], y0: b.y0 + h*row[0],
		x1: b.x0 + w*col[1], y1: b.y0 + h*row[1],
	}, true
}

// parseZoneSpec reads an S0 spec file's modules[] into zone claims. Modules
// without a zone or without parts are skipped (not every module is zoned).
func parseZoneSpec(raw []byte) (map[string]*stageZoneClaim, error) {
	var doc struct {
		Modules []struct {
			Name  string   `json:"name"`
			Zone  string   `json:"zone"`
			Parts []string `json:"parts"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	out := map[string]*stageZoneClaim{}
	for i, m := range doc.Modules {
		zone := strings.ToLower(strings.TrimSpace(m.Zone))
		if zone == "" || len(m.Parts) == 0 {
			continue
		}
		if !pcbZoneNames[zone] {
			return nil, fmt.Errorf("modules[%d] %q: unknown zone %q (grid vocabulary: %s)",
				i, m.Name, m.Zone, strings.Join(sortedZoneNames(), ", "))
		}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = fmt.Sprintf("module%d", i+1)
		}
		out[name] = &stageZoneClaim{Zone: zone, Parts: normalizeDesignators(m.Parts), Note: "spec"}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("spec has no zoned modules (modules[].zone + modules[].parts)")
	}
	return out, nil
}

// parseZoneModuleFlags parses repeatable --module NAME=ZONE:D1,D2 flags.
func parseZoneModuleFlags(items []string) (map[string]*stageZoneClaim, error) {
	out := map[string]*stageZoneClaim{}
	for _, it := range items {
		name, rest, ok := strings.Cut(it, "=")
		if !ok {
			return nil, fmt.Errorf("--module %q: expected NAME=ZONE:D1,D2", it)
		}
		zone, parts, ok := strings.Cut(rest, ":")
		zone = strings.ToLower(strings.TrimSpace(zone))
		if !ok || strings.TrimSpace(parts) == "" {
			return nil, fmt.Errorf("--module %q: expected NAME=ZONE:D1,D2", it)
		}
		if !pcbZoneNames[zone] {
			return nil, fmt.Errorf("--module %q: unknown zone %q (grid vocabulary: %s)", it, zone, strings.Join(sortedZoneNames(), ", "))
		}
		out[strings.TrimSpace(name)] = &stageZoneClaim{
			Zone: zone, Parts: normalizeDesignators(strings.Split(parts, ",")), Note: "manual",
		}
	}
	return out, nil
}

func normalizeDesignators(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range in {
		d = strings.ToUpper(strings.TrimSpace(d))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func sortedZoneNames() []string {
	var names []string
	for n := range pcbZoneNames {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// zonePart is the minimal component view the violation rule needs.
type zonePart struct {
	Designator string
	CX, CY     float64 // bbox center
	HasBBox    bool
}

// findZoneViolations flags claimed parts whose bbox center is outside their
// zone's sub-rectangle of the board. Pure (unit-testable). Claimed-but-absent
// designators are skipped — presence is the tier/netlist layers' concern.
func findZoneViolations(zones map[string]*stageZoneClaim, board cpRect, parts []zonePart) []pcbCheckFinding {
	byDesig := map[string]zonePart{}
	for _, p := range parts {
		byDesig[strings.ToUpper(p.Designator)] = p
	}
	var out []pcbCheckFinding
	var names []string
	for n := range zones {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic finding order
	for _, name := range names {
		zc := zones[name]
		if zc == nil {
			continue
		}
		rect, ok := pcbZoneRect(zc.Zone, board)
		if !ok {
			continue
		}
		for _, d := range zc.Parts {
			p, present := byDesig[d]
			if !present || !p.HasBBox {
				continue
			}
			if p.CX >= rect.x0 && p.CX <= rect.x1 && p.CY >= rect.y0 && p.CY <= rect.y1 {
				continue
			}
			out = append(out, pcbCheckFinding{
				Type: "zone-violation", Level: "WARN", Designator: p.Designator,
				At: &pcbXY{X: round1(p.CX), Y: round1(p.CY)},
				Message: fmt.Sprintf("器件 %s 在其功能分区之外: module %q → zone %q (板面 %s 子矩形) — S0 拍板的分区在布局里没有落实 [规范 §3.3 模拟/数字分区]",
					p.Designator, name, zc.Zone, zc.Zone),
			})
		}
	}
	return out
}

// fetchZoneParts pulls the live component bbox centers for the violation rule.
func fetchZoneParts(cfg *appConfig, window string) ([]zonePart, error) {
	res, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includeBBox": true})
	if err != nil {
		return nil, err
	}
	raw, _ := res.Result["components"].([]any)
	out := make([]zonePart, 0, len(raw))
	for _, ri := range raw {
		cm, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		p := zonePart{Designator: asString(cm["designator"])}
		if bb, ok := cm["bbox"].(map[string]any); ok {
			x0, ok1 := asFloatOK(bb["minX"])
			y0, ok2 := asFloatOK(bb["minY"])
			x1, ok3 := asFloatOK(bb["maxX"])
			y1, ok4 := asFloatOK(bb["maxY"])
			if ok1 && ok2 && ok3 && ok4 {
				p.CX, p.CY, p.HasBBox = (x0+x1)/2, (y0+y1)/2, true
			}
		}
		if !p.HasBBox {
			// Fall back to the anchor: better a slightly-off center than no check.
			if x, ok1 := asFloatOK(cm["x"]); ok1 {
				if y, ok2 := asFloatOK(cm["y"]); ok2 {
					p.CX, p.CY, p.HasBBox = x, y, true
				}
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// fetchBoardRect pulls the live outline bbox as a cpRect.
func fetchBoardRect(cfg *appConfig, window string) (cpRect, error) {
	res, err := requestAction(cfg, "pcb.outline.get", window, nil)
	if err != nil {
		return cpRect{}, err
	}
	bb, ok := mnav(res.Result, "bbox").(map[string]any)
	if !ok {
		return cpRect{}, fmt.Errorf("outline has no bbox (draw a board outline first)")
	}
	x0, ok1 := asFloatOK(bb["minX"])
	y0, ok2 := asFloatOK(bb["minY"])
	x1, ok3 := asFloatOK(bb["maxX"])
	y1, ok4 := asFloatOK(bb["maxY"])
	if !ok1 || !ok2 || !ok3 || !ok4 || x1 <= x0 || y1 <= y0 {
		return cpRect{}, fmt.Errorf("outline bbox malformed")
	}
	return cpRect{x0: x0, y0: y0, x1: x1, y1: y1}, nil
}

// loadZoneClaims loads the project's zone table (nil when none set).
func loadZoneClaims(cfg *appConfig, window string) (map[string]*stageZoneClaim, string, error) {
	project, err := resolveStageProject(cfg, window)
	if err != nil {
		return nil, "", err
	}
	st, err := loadPcbStageState(project)
	if err != nil {
		return nil, project, err
	}
	return st.Zones, project, nil
}

// newPcbZonesCmd builds the `pcb zones` command group.
func newPcbZonesCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	zones := &cobra.Command{
		Use:   "zones",
		Short: "Functional zone claims (S0 modules[].zone → PCB, issue #126): set / status / clear",
		Long: `Persist the S0 spec's functional-zone partitioning (modules[].zone) so the
PCB side can execute and verify it:

  - ` + "`pcb place-constrained`" + ` places claimed mains/satellites INTO their zone's
    board sub-rectangle (edge parts exempt — the board edge is harder);
  - ` + "`pcb check`" + ` flags zone-violation (WARN): a claimed part outside its zone
    [规范 §3.3 模拟/数字分区].

Zone names are the shared grid vocabulary (same as schematic autolayout):
left / center / right × top / bottom (e.g. right-top), or full-height/width
left / right / top / bottom / center. The rectangle is resolved from the LIVE
board outline at consumption time. Claims live in the project workflow state
and survive placement invalidations (they are a spec contract).`,
	}
	zones.AddCommand(newPcbZonesSetCmd(cfg, window, stdout, stderr))
	zones.AddCommand(newPcbZonesStatusCmd(cfg, window, stdout))
	zones.AddCommand(newPcbZonesClearCmd(cfg, window, stdout))
	return zones
}

func newPcbZonesSetCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var specPath string
	var modules []string
	c := &cobra.Command{
		Use:   "set",
		Short: "Set zone claims from an S0 spec file (--spec) or manually (--module)",
		Example: `  easyeda pcb zones set --spec s0-esp32mini.json --project ceshi
  easyeda pcb zones set --module "RF=right-top:U2,ANT1" --module "POWER=left-bottom:U3,C5,C6" --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var claims map[string]*stageZoneClaim
			var err error
			switch {
			case specPath != "" && len(modules) > 0:
				return fmt.Errorf("--spec and --module are mutually exclusive")
			case specPath != "":
				raw, rerr := os.ReadFile(specPath)
				if rerr != nil {
					return rerr
				}
				claims, err = parseZoneSpec(raw)
			case len(modules) > 0:
				claims, err = parseZoneModuleFlags(modules)
			default:
				return fmt.Errorf("pass --spec <s0-spec.json> or --module NAME=ZONE:D1,D2")
			}
			if err != nil {
				return err
			}
			project, perr := resolveStageProject(cfg, *window)
			if perr != nil {
				return perr
			}
			st, serr := loadPcbStageState(project)
			if serr != nil {
				return serr
			}
			for name, zc := range claims {
				zc.At = nowRFC3339()
				_ = name
			}
			st.SetZones(claims)
			if err := savePcbStageState(st); err != nil {
				return err
			}
			total := 0
			for name, zc := range claims {
				total += len(zc.Parts)
				fmt.Fprintf(stderr, "✓ %s → %s (%d part(s): %s)\n", name, zc.Zone, len(zc.Parts), strings.Join(zc.Parts, ","))
			}
			fmt.Fprintf(stderr, "zone claims persisted for %q — %d module(s), %d part(s); consumed by place-constrained + pcb check (zone-violation)\n",
				project, len(claims), total)
			return nil
		},
	}
	c.Flags().StringVar(&specPath, "spec", "", "S0 spec JSON with modules[].zone + modules[].parts")
	c.Flags().StringArrayVar(&modules, "module", nil, `manual claim: NAME=ZONE:D1,D2 (repeatable)`)
	return c
}

func newPcbZonesStatusCmd(cfg *appConfig, window *string, stdout io.Writer) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Show the persisted zone claims (and live violations when a window is connected)",
		RunE: func(cmd *cobra.Command, args []string) error {
			zones, project, err := loadZoneClaims(cfg, *window)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"project": project, "zones": zones})
			}
			if len(zones) == 0 {
				fmt.Fprintf(stdout, "no zone claims for %q — `pcb zones set --spec <s0-spec.json>`\n", project)
				return nil
			}
			fmt.Fprintf(stdout, "zone claims — project %q\n", project)
			var names []string
			for n := range zones {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				zc := zones[n]
				fmt.Fprintf(stdout, "  %-12s %-13s %d part(s): %s\n", n, zc.Zone, len(zc.Parts), strings.Join(zc.Parts, ","))
			}
			// Live violation quick-look (best-effort: needs window + outline).
			if board, berr := fetchBoardRect(cfg, *window); berr == nil {
				if parts, perr := fetchZoneParts(cfg, *window); perr == nil {
					v := findZoneViolations(zones, board, parts)
					if len(v) == 0 {
						fmt.Fprintln(stdout, "  ✓ live check: all claimed parts inside their zones")
					} else {
						for _, f := range v {
							fmt.Fprintf(stdout, "  ✗ %s\n", f.Message)
						}
					}
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit claims as JSON")
	return c
}

func newPcbZonesClearCmd(cfg *appConfig, window *string, stdout io.Writer) *cobra.Command {
	c := &cobra.Command{
		Use:   "clear",
		Short: "Remove all zone claims",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := resolveStageProject(cfg, *window)
			if err != nil {
				return err
			}
			st, err := loadPcbStageState(project)
			if err != nil {
				return err
			}
			n := len(st.Zones)
			st.SetZones(nil)
			if err := savePcbStageState(st); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "cleared %d zone claim(s) for %q\n", n, project)
			return nil
		},
	}
	return c
}
