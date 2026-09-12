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

func newSchLayoutPlanCmd(stdout io.Writer) *cobra.Command {
	var from, out string
	var zones bool
	c := &cobra.Command{Use: "layout-plan", Short: "Plan a measured component set offline without Lib or project metadata", Long: `Compute local placements, wires, markers and score from schemaVersion:1,
coreComponentId, components:[{id,measurement,pinStates?,allowedRotations?}], netPolicies keyed by
net NAME, optional attachments and maxCandidates. measurement contains explicit
designator,x,y,rotation,mirror,bbox,pins (number,name,net,x,y), optional textBboxes.
Every empty-net pin requires pinStates[number] = nc or unconnected.
Policies: direct, module_port, local_power, local_ground.
Attachments: {componentId,pinNumber?,attachTo?:{componentId,pinNumber}}.
Core is normalized to 0,0. Output preserves pin states and component IDs.
Optional optimization:{maxVariants?:4,maxAttempts?:24} enables bounded rotation
and geometry refinement after a complete baseline. maxVariants is 1..4 (including
baseline); maxAttempts is 1..64. Allowed rotations are absolute stored angles from
0,90,180,270 and must include the measured angle; omitted means locked. Core and
mirroring stay locked. Pin geometry and wires are recomputed and checked, not scaled.
Previously directly connected pin islands cannot be split into same-name labels.
Failed optional refinements keep a validated candidate; an unsolved baseline fails.
The same maxCandidates budget covers solving and refinement. In --zones mode an
optimization request isolates per-zone budgets, even without unified spacing.
Zone output includes variants:[{id,layout,contentBounds,frame}], selectedVariantId.
Pass the complete packet to layout-sheet-plan --flow z for bounded shape selection.
No library UUID, Lib membership, project, sheet or daemon required. No Apply.
With --zones: input schemaVersion, components, netPolicies, zones, optional
attachments/maxCandidates/spacing/optimization. Optional spacing is the shared zone inner,
page and inter-zone minimum clearance (>=10 raw, 5-raw grid), including stroke
clearance; forwarded unchanged to the sheet planner. Legacy defaults otherwise.
In unified spacing mode maxCandidates is a per-zone cap, so earlier zones cannot
consume another zone's optimization allowance. Legacy mode shares one cap.
Each zone: {id,title,coreComponentId,componentIds}.
Optional zone placement:{samePageAs:<zone ID>,preferAdjacent?:true} is forwarded
to sheet planning: hard same-page relation with an optional soft neighbor preference.
Every component belongs to exactly one zone. Cross-zone signals use module_port.
Output contains independent local layouts/contentBounds and compact frame plans,
not whole-page packing or rendered frames. Add identity/sheet evidence before compose/Apply.

Example:
  easyeda sch layout-plan --from measured-set.json --out local-geometry.json`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if from == "" {
			return fmt.Errorf("--from is required")
		}
		raw, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		var result any
		if zones {
			var input SchematicZonesInput
			input, err = decodeSchematicZonesInput(raw)
			if err == nil {
				result, err = PlanSchematicZones(input)
			}
		} else {
			var input SchematicLayoutInput
			input, err = decodeSchematicLayoutInput(raw)
			if err == nil {
				result, err = PlanSchematicLayout(input)
			}
		}
		if err != nil {
			return err
		}
		raw, err = json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		if out == "" {
			_, err = stdout.Write(raw)
			return err
		}
		a, _ := filepath.Abs(from)
		b, _ := filepath.Abs(out)
		fi, _ := os.Stat(from)
		fo, _ := os.Stat(out)
		if a == b || (fi != nil && fo != nil && os.SameFile(fi, fo)) {
			return fmt.Errorf("--out must not overwrite measured input")
		}
		return os.WriteFile(out, raw, 0644)
	}}
	c.Flags().StringVar(&from, "from", "", "measured component-set JSON, without Lib metadata")
	c.Flags().BoolVar(&zones, "zones", false, "plan explicitly owned per-core zones; unified spacing isolates per-zone budgets")
	c.Flags().StringVar(&out, "out", "", "write local geometry only after validation; defaults to stdout")
	return c
}

func decodeSchematicZonesInput(raw []byte) (SchematicZonesInput, error) {
	var input SchematicZonesInput
	if err := connectivity.DecodeStrictDesignJSON(raw, &input); err != nil {
		return input, err
	}
	if err := validateSchematicZonePlacementsJSON(raw); err != nil {
		return input, err
	}
	// Reuse explicit measurement-field checks on original JSON, not re-marshaled
	// structs whose zero values would hide missing rotation/mirror/coordinates.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return input, err
	}
	if b, ok := fields["spacing"]; ok && string(b) == "null" {
		return input, fmt.Errorf("spacing must be a number, not null")
	}
	if err := validateSchematicSpacing(input.Spacing); err != nil {
		return input, err
	}
	delete(fields, "zones")
	delete(fields, "spacing")
	fields["coreComponentId"] = json.RawMessage(`"zone-validation"`)
	measurementJSON, err := json.Marshal(fields)
	if err != nil {
		return input, err
	}
	_, err = decodeSchematicLayoutInput(measurementJSON)
	return input, err
}

func decodeSchematicLayoutInput(raw []byte) (SchematicLayoutInput, error) {
	var input SchematicLayoutInput
	if err := connectivity.DecodeStrictDesignJSON(raw, &input); err != nil {
		return input, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if err := validateLayoutOptimizationJSON(fields); err != nil {
		return input, err
	}
	require := func(raw json.RawMessage, where string, keys ...string) error {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		for _, key := range keys {
			if len(object[key]) == 0 || string(object[key]) == "null" {
				return fmt.Errorf("%s.%s requires explicit evidence", where, key)
			}
		}
		return nil
	}
	if err := require(raw, "input", "schemaVersion", "coreComponentId", "components", "netPolicies"); err != nil {
		return input, err
	}
	var components []map[string]json.RawMessage
	_ = json.Unmarshal(fields["components"], &components)
	for i, c := range components {
		if angles, ok := c["allowedRotations"]; ok {
			var values []json.RawMessage
			if string(angles) == "null" || json.Unmarshal(angles, &values) != nil || len(values) == 0 || len(values) > 4 {
				return input, fmt.Errorf("allowedRotations requires 1..4 explicit angles")
			}
			seen := map[float64]bool{}
			for _, value := range values {
				var angle float64
				if string(value) == "null" || json.Unmarshal(value, &angle) != nil || (angle != 0 && angle != 90 && angle != 180 && angle != 270) || seen[angle] {
					return input, fmt.Errorf("allowedRotations requires unique angles 0,90,180,270")
				}
				seen[angle] = true
			}
		}
		where := fmt.Sprintf("components[%d].measurement", i)
		if err := require(c["measurement"], where, "designator", "x", "y", "rotation", "mirror", "bbox", "pins"); err != nil {
			return input, err
		}
		var m map[string]json.RawMessage
		_ = json.Unmarshal(c["measurement"], &m)
		if err := require(m["bbox"], where+".bbox", "minX", "minY", "maxX", "maxY"); err != nil {
			return input, err
		}
		var boxes []json.RawMessage
		_ = json.Unmarshal(m["textBboxes"], &boxes)
		for _, box := range boxes {
			if err := require(box, where+".textBboxes", "minX", "minY", "maxX", "maxY"); err != nil {
				return input, err
			}
		}
		var pins []json.RawMessage
		_ = json.Unmarshal(m["pins"], &pins)
		for _, p := range pins {
			if err := require(p, where+".pins", "number", "net", "x", "y"); err != nil {
				return input, err
			}
		}
	}
	return input, nil
}

func validateLayoutOptimizationJSON(fields map[string]json.RawMessage) error {
	raw, ok := fields["optimization"]
	if !ok {
		return nil
	}
	var options map[string]json.RawMessage
	if string(raw) == "null" || json.Unmarshal(raw, &options) != nil {
		return fmt.Errorf("optimization requires an object")
	}
	for name, maximum := range map[string]int{"maxVariants": 4, "maxAttempts": 64} {
		if value, ok := options[name]; ok {
			var n int
			if string(value) == "null" || json.Unmarshal(value, &n) != nil || n < 1 || n > maximum {
				return fmt.Errorf("optimization.%s must be 1..%d", name, maximum)
			}
		}
	}
	return nil
}
