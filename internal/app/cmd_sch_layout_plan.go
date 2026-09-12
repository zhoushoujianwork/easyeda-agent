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
coreComponentId, components:[{id,measurement,pinStates?}], netPolicies keyed by
net NAME, optional attachments and maxCandidates. measurement contains explicit
designator,x,y,rotation,mirror,bbox,pins (number,name,net,x,y), optional textBboxes.
Every empty-net pin requires pinStates[number] = nc or unconnected.
Policies: direct, module_port, local_power, local_ground.
Attachments: {componentId,pinNumber?,attachTo?:{componentId,pinNumber}}.
Core is normalized to 0,0. Output preserves pin states and component IDs.
No library UUID, Lib membership, project, sheet or daemon required. No Apply.
With --zones: input schemaVersion, components, netPolicies, zones, optional
attachments/maxCandidates. Each zone: {id,title,coreComponentId,componentIds}.
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
	c.Flags().BoolVar(&zones, "zones", false, "plan explicitly owned per-core zones with a shared search budget")
	c.Flags().StringVar(&out, "out", "", "write local geometry only after validation; defaults to stdout")
	return c
}

func decodeSchematicZonesInput(raw []byte) (SchematicZonesInput, error) {
	var input SchematicZonesInput
	if err := connectivity.DecodeStrictDesignJSON(raw, &input); err != nil {
		return input, err
	}
	// Reuse explicit measurement-field checks on original JSON, not re-marshaled
	// structs whose zero values would hide missing rotation/mirror/coordinates.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return input, err
	}
	delete(fields, "zones")
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
