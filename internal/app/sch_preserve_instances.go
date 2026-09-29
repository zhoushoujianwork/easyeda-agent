package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

var schInstanceFields = []string{"uniqueId", "name", "subPartName", "addIntoBom", "addIntoPcb", "manufacturer", "manufacturerId", "supplier", "supplierId", "otherProperty", "component", "symbol", "footprint"}

// This is instance evidence, not device-library defaults. Preserve JSON types,
// explicit empty values, and native link keys; never derive them from a ref.
func schPreservedInstance(part map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, key := range schInstanceFields {
		value, ok := part[key]
		if !ok {
			return nil, fmt.Errorf("instance field %s unavailable", key)
		}
		out[key] = value
	}
	if err := validateSchPreservedInstance(out); err != nil {
		return nil, err
	}
	return out, nil
}

func validateSchPreservedInstance(instance map[string]any) error {
	if len(instance) != len(schInstanceFields) {
		return fmt.Errorf("complete persistent instance fields required")
	}
	for _, key := range schInstanceFields {
		if _, ok := instance[key]; !ok {
			return fmt.Errorf("missing %s", key)
		}
	}
	if id, ok := instance["uniqueId"].(string); !ok || strings.TrimSpace(id) == "" {
		return fmt.Errorf("nonempty native uniqueId required")
	}
	for _, key := range []string{"name", "subPartName", "manufacturer", "manufacturerId", "supplier", "supplierId"} {
		if instance[key] != nil {
			if _, ok := instance[key].(string); !ok {
				return fmt.Errorf("%s must be string or explicit null", key)
			}
		}
	}
	for _, key := range []string{"addIntoBom", "addIntoPcb"} {
		if _, ok := instance[key].(bool); !ok {
			return fmt.Errorf("%s must be boolean", key)
		}
	}
	props, ok := instance["otherProperty"].(map[string]any)
	if !ok {
		return fmt.Errorf("otherProperty must be an explicit object")
	}
	for key, v := range props {
		switch v.(type) {
		case string, bool:
		case float64:
			if !finiteStateNumber(v.(float64)) {
				return fmt.Errorf("nonfinite property %s", key)
			}
		default:
			return fmt.Errorf("otherProperty.%s requires string/number/boolean; cannot silently discard unsupported data", key)
		}
	}
	for _, key := range []string{"component", "symbol", "footprint"} {
		if _, ok := instance[key].(map[string]any); !ok {
			return fmt.Errorf("%s native identity unavailable", key)
		}
	}
	return nil
}

func checkSchPreservedInstance(ref string, want, have map[string]any) error {
	actual, err := schPreservedInstance(have)
	if err != nil {
		return fmt.Errorf("%s: %w", ref, err)
	}
	if !reflect.DeepEqual(want, actual) {
		return fmt.Errorf("instance-preservation: %s", schDesignatorFirstDifference(ref, want, actual))
	}
	return nil
}

func checkSchPreservedSource(want, live map[string]any) error {
	actual, err := schDesignatorScene(live)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(want, actual) {
		return fmt.Errorf("source-drift: %s", schDesignatorFirstDifference("scene", want, actual))
	}
	return nil
}

type schPreservedParts struct {
	Parts     map[string]map[string]any
	Instances map[string]map[string]any
	IDs       []string
	Scene     map[string]any
}

// Preserve mode is a layout/route change only, not a device or connectivity
// replacement. It must prove complete same-page identity before any queue exists.
func prepareSchPreservedParts(p *schCompositionPlan, before []byte, allowUnwired bool) (*schPreservedParts, error) {
	baseline, err := parseSchDesignatorBaseline(before)
	if err != nil {
		return nil, err
	}
	if baseline.Context.ProjectUUID != p.Connectivity.ProjectID || baseline.Context.DocumentUUID != p.Connectivity.DocumentID {
		return nil, fmt.Errorf("preserve-instances requires identical project and page")
	}
	if len(baseline.Parts) != len(p.Connectivity.Components) {
		return nil, fmt.Errorf("preserve-instances requires the same complete component set")
	}
	out := &schPreservedParts{Parts: map[string]map[string]any{}, Instances: map[string]map[string]any{}}
	byID := map[string]connectivity.Component{}
	for _, c := range p.Connectivity.Components {
		byID[c.ID] = c
	}
	nets := map[string]string{}
	for _, n := range p.Connectivity.Nets {
		nets[n.ID] = n.Name
	}
	desired := map[string]map[string]string{}
	for _, c := range p.Connectivity.Components {
		desired[c.ID] = map[string]string{}
	}
	for _, e := range p.Connectivity.Connections {
		desired[e.ComponentID][e.PinNumber] = nets[e.NetID]
	}
	unique := map[string]bool{}
	for pid, raw := range baseline.Parts {
		id := baseline.IDs[pid]
		c, ok := byID[id]
		if !ok || raw["designator"] != c.Ref {
			return nil, fmt.Errorf("preserve-instances: stable ID/ref mismatch for %s", pid)
		}
		device, err := measuredSchematicDevice(c.Ref, raw)
		if err != nil {
			return nil, err
		}
		if device.LibraryUUID != c.Device.LibraryUUID || device.UUID != c.Device.UUID {
			return nil, fmt.Errorf("preserve-instances: device identity differs for %s", c.Ref)
		}
		instance, err := schPreservedInstance(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Ref, err)
		}
		uid := instance["uniqueId"].(string)
		if unique[uid] {
			return nil, fmt.Errorf("duplicate native uniqueId %s", uid)
		}
		unique[uid] = true
		props := instance["otherProperty"].(map[string]any)
		if props[connectivity.ComponentIDProperty] != c.ID {
			return nil, fmt.Errorf("%s explicit stable ID binding required", c.Ref)
		}
		if c.Role != "" && props[connectivity.ComponentRoleProperty] != c.Role {
			return nil, fmt.Errorf("%s role differs; preservation cannot rewrite attributes", c.Ref)
		}
		pins := raw["pins"].([]any)
		if len(pins) != len(c.Pins) {
			return nil, fmt.Errorf("%s physical pin set differs", c.Ref)
		}
		want := map[string]connectivity.Pin{}
		for _, q := range c.Pins {
			want[q.Number] = q
		}
		seen := map[string]bool{}
		for _, v := range pins {
			q := v.(map[string]any)
			number := schDesignatorPinNumber(q)
			expected, ok := want[number]
			if !ok || seen[number] {
				return nil, fmt.Errorf("%s unknown/duplicate physical pin %s", c.Ref, number)
			}
			seen[number] = true
			if name, known := q["pinName"].(string); !known || name != expected.Name || (!allowUnwired && (q["noConnected"] != expected.NoConnected || q["net"] != desired[id][number])) {
				return nil, fmt.Errorf("%s.%s source name/net/NC differs from target; preserve-instances cannot change circuit", c.Ref, number)
			}
		}
		out.Parts[c.Ref], out.Instances[c.Ref] = raw, instance
		out.IDs = append(out.IDs, pid)
	}
	sort.Strings(out.IDs)
	out.Scene, err = schDesignatorScene(baseline.Result)
	return out, err
}

// An unwired page may be laid out and wired while retaining its native part
// instances. This exception requires a complete active-page zero-drawing read
// and every pin explicitly open; a partial netlist or one existing net/NC
// falls back to the exact-preservation contract above.
func schComposeFullyUnwired(result map[string]any) bool {
	if err := (&schematicDrawingExpectation{}).check(result); err != nil {
		return false
	}
	summary, ok := result["connectivitySummary"].(map[string]any)
	if !ok || summary["scope"] != "activePage" {
		return false
	}
	for _, key := range []string{"wires", "buses", "netflags", "netports", "netlabels", "shortSymbols"} {
		if value, known := finiteFloat(summary[key]); !known || value != 0 {
			return false
		}
	}
	parts, ok := result["components"].([]any)
	if !ok || result["pinNetsAvailable"] != true || result["wiresAvailable"] != true {
		return false
	}
	for _, item := range parts {
		part, ok := item.(map[string]any)
		if !ok {
			return false
		}
		if part["componentType"] != "part" {
			continue
		}
		pins, ok := part["pins"].([]any)
		if !ok || part["pinsAvailable"] != true {
			return false
		}
		for _, item := range pins {
			pin, ok := item.(map[string]any)
			if !ok || pin["net"] != "" || pin["noConnected"] != false {
				return false
			}
		}
	}
	return true
}

func (p *schPreservedParts) protect(e *schematicStateExpectation) {
	for ref, part := range e.Parts {
		part.PrimitiveID = stringVal(p.Parts[ref]["primitiveId"])
		part.Instance = p.Instances[ref]
		for _, v := range p.Parts[ref]["pins"].([]any) {
			q := v.(map[string]any)
			number := schDesignatorPinNumber(q)
			name := q["pinName"].(string)
			pin := part.Pins[number]
			pin.Name = &name
			part.Pins[number] = pin
		}
		e.Parts[ref] = part
	}
}

func (p *schPreservedParts) posePatch(c powerLayoutPlacement) map[string]any {
	return map[string]any{"x": c.X, "y": c.Y, "rotation": c.Rotation, "mirror": c.Mirror}
}

// A matching bound instance set is recognizably a relayout, not authorization
// to discard native IDs. Older unbound/different-device rebuilds keep their
// existing contract; malformed bound snapshots fail the preserve compiler.
func schSameBoundInstanceSet(p *schCompositionPlan, result map[string]any) bool {
	rows, ok := result["components"].([]any)
	if !ok {
		return false
	}
	targets := map[string]connectivity.Component{}
	for _, c := range p.Connectivity.Components {
		targets[c.Ref] = c
	}
	count := 0
	for _, v := range rows {
		r, ok := v.(map[string]any)
		if !ok || r["componentType"] != "part" {
			continue
		}
		count++
		c, ok := targets[stringVal(r["designator"])]
		if !ok {
			return false
		}
		props, ok := r["otherProperty"].(map[string]any)
		if !ok || props[connectivity.ComponentIDProperty] != c.ID {
			return false
		}
		device, err := measuredSchematicDevice(c.Ref, r)
		if err != nil || device.LibraryUUID != c.Device.LibraryUUID || device.UUID != c.Device.UUID {
			return false
		}
	}
	return count > 0 && count == len(targets)
}

// Detached expectations prevent adding phase-specific facts to another step.
func cloneSchExpectation(e *schematicStateExpectation) *schematicStateExpectation {
	raw, _ := json.Marshal(e)
	var out schematicStateExpectation
	_ = json.Unmarshal(raw, &out)
	return &out
}
