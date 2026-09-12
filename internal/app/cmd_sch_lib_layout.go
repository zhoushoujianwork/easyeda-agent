package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

type libLayoutAttach = SchematicLayoutAttach
type libLayoutPeripheral = SchematicLayoutPeripheral
type libLayoutModule struct {
	ID              string                `json:"id"`
	Title           string                `json:"title"`
	CoreComponentID string                `json:"coreComponentId"`
	Peripherals     []libLayoutPeripheral `json:"peripherals,omitempty"`
	NetPolicies     map[string]string     `json:"netPolicies"`
}
type libLayoutSource struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Connectivity  connectivity.Document  `json:"connectivity"`
	Sheet         layoutBBox             `json:"sheet"`
	SheetBorder   *layoutBBox            `json:"sheetBorder,omitempty"`
	Keepouts      []layoutBBox           `json:"keepouts"`
	Measurements  []powerLayoutPlacement `json:"measurements"`
	LayoutModules []libLayoutModule      `json:"layoutModules"`
	MaxCandidates int                    `json:"maxCandidates,omitempty"`
}

func newSchLibLayoutCmd(stdout, stderr io.Writer) *cobra.Command {
	var from, out string
	c := &cobra.Command{Use: "lib-layout", Short: "Calculate Lib placement and wiring offline from canonical nets and measured pins", Long: `Plan translation-only modules from a declared core on a 5-raw grid. Input contains
schemaVersion:1, connectivity, sheet, keepouts, measurements and layoutModules.
Each layoutModule declares id/title/coreComponentId and netPolicies keyed by netId:
direct, local_power, local_ground or module_port. Optional peripherals specify
componentId, optional pinNumber and attachTo:{componentId,pinNumber}. The target
may be a core or another module member; connection data must already agree.
Peripherals may face perpendicular to their reference pin (e.g. shunt capacitors).
Placement follows existing electrical branches; no connectivity or pose is invented. Search is bounded to
400 raw outward distance and 200 raw lateral distance, starting at 5 raw. Rail-only
peripherals are scheduled first. At the shortest feasible attachment distance,
compare total wire/lead length, segment count, axis alignment, then visible area.
Nearby power/ground islands try safe direct/L joins up to 80 raw before naming;
markers are assigned ground, power, then signals. Optional maxCandidates limits the total search (default
20000, range 1..1000000). Unresolved input fails
without writing output. Output is a validated source for sch compose. Naming markers are local for power
and ground islands; direct/module_port nets form an actual wire tree.

Examples:
  easyeda sch lib-layout --from layout-input.json --out composition.json
  easyeda sch compose --from composition.json --out plan.json`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if from == "" {
			return fmt.Errorf("--from is required")
		}
		b, e := os.ReadFile(from)
		if e != nil {
			return e
		}
		src, e := decodeLibLayout(b)
		if e != nil {
			return e
		}
		result, e := planLibLayout(src)
		if e != nil {
			return e
		}
		b, e = json.MarshalIndent(result, "", "  ")
		if e != nil {
			return e
		}
		b = append(b, '\n')
		if out != "" {
			a, _ := filepath.Abs(from)
			z, _ := filepath.Abs(out)
			fi, _ := os.Stat(from)
			fo, _ := os.Stat(out)
			if a == z || (fi != nil && fo != nil && os.SameFile(fi, fo)) {
				return fmt.Errorf("--out must not overwrite the measured input")
			}
			e = os.WriteFile(out, b, 0644)
		} else {
			_, e = stdout.Write(b)
		}
		if e != nil {
			return e
		}
		parts, wires, markers := 0, 0, 0
		for _, module := range result.Modules {
			parts += len(module.Placements)
			wires += len(module.Wires)
			markers += len(module.Flags)
		}
		fmt.Fprintf(stderr, "lib-layout: %d modules, %d parts, %d wires, %d markers; connectivity and measured poses preserved; compose source ready\n", len(result.Modules), parts, wires, markers)
		return nil
	}}
	c.Flags().StringVar(&from, "from", "", "canonical connectivity, measured geometry and module layout intent JSON")
	c.Flags().StringVar(&out, "out", "", "write compose source only after complete validation")
	return c
}

func decodeLibLayout(raw []byte) (libLayoutSource, error) {
	var src libLayoutSource
	if err := connectivity.DecodeStrictDesignJSON(raw, &src); err != nil {
		return src, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return src, err
	}
	if _, err := connectivity.DecodeDesign(fields["connectivity"]); err != nil {
		return src, err
	}
	require := func(object map[string]json.RawMessage, where string, keys ...string) error {
		for _, key := range keys {
			if len(object[key]) == 0 || string(object[key]) == "null" {
				return fmt.Errorf("%s.%s requires explicit measured evidence", where, key)
			}
		}
		return nil
	}
	if err := require(fields, "input", "keepouts", "measurements", "layoutModules", "sheet"); err != nil {
		return src, err
	}
	for _, key := range []string{"sheet", "sheetBorder"} {
		if len(fields[key]) == 0 {
			continue
		}
		var box map[string]json.RawMessage
		_ = json.Unmarshal(fields[key], &box)
		if err := require(box, key, "minX", "minY", "maxX", "maxY"); err != nil {
			return src, err
		}
	}
	var keepouts []map[string]json.RawMessage
	_ = json.Unmarshal(fields["keepouts"], &keepouts)
	for i, box := range keepouts {
		if err := require(box, fmt.Sprintf("keepouts[%d]", i), "minX", "minY", "maxX", "maxY"); err != nil {
			return src, err
		}
	}
	var measurements []map[string]json.RawMessage
	if err := json.Unmarshal(fields["measurements"], &measurements); err != nil {
		return src, err
	}
	for i, m := range measurements {
		where := fmt.Sprintf("measurements[%d]", i)
		if err := require(m, where, "designator", "x", "y", "rotation", "mirror", "bbox", "pins"); err != nil {
			return src, err
		}
		var box map[string]json.RawMessage
		_ = json.Unmarshal(m["bbox"], &box)
		if err := require(box, where+".bbox", "minX", "minY", "maxX", "maxY"); err != nil {
			return src, err
		}
		var pins []map[string]json.RawMessage
		_ = json.Unmarshal(m["pins"], &pins)
		for j, p := range pins {
			if err := require(p, fmt.Sprintf("%s.pins[%d]", where, j), "number", "net", "x", "y"); err != nil {
				return src, err
			}
		}
	}
	return src, nil
}
