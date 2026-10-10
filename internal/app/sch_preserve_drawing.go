package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"sort"
)

// Pin attributes are persistent instance data. The pose and derived net change
// during a drawing rebuild; the native pin identity and custom data must not.
func schPreservedPin(pin map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, key := range []string{"primitiveId", "pinNumber", "otherProperty"} {
		value, ok := pin[key]
		if !ok {
			return nil, fmt.Errorf("pin field %s unavailable", key)
		}
		out[key] = value
	}
	return out, validateSchPreservedPin(stringVal(pin["pinNumber"]), out)
}

func validateSchPreservedPin(number string, pin map[string]any) error {
	if len(pin) != 3 || stringVal(pin["primitiveId"]) == "" || pin["pinNumber"] != number {
		return fmt.Errorf("complete native pin identity required")
	}
	if _, ok := pin["otherProperty"].(map[string]any); !ok {
		return fmt.Errorf("pin otherProperty must be an explicit object")
	}
	return nil
}

func checkSchPreservedPin(ref, number string, want, pin map[string]any) error {
	have, err := schPreservedPin(pin)
	if err != nil {
		return fmt.Errorf("%s.%s: %w", ref, number, err)
	}
	if !reflect.DeepEqual(want, have) {
		return fmt.Errorf("pin-preservation: %s", schDesignatorFirstDifference(ref+"."+number, want, have))
	}
	return nil
}

type schOwnedAttributesExpectation struct {
	Owners     []string       `json:"owners"`
	Attributes map[string]any `json:"attributes"`
}

// Attribute coordinates follow the owner during a move. All other native
// fields, including id, parent, content, visibility and style, remain exact.
func schOwnedAttributes(page map[string]any, owners []string) (map[string]any, error) {
	ownerSet := map[string]bool{}
	for _, id := range owners {
		ownerSet[id] = true
	}
	rows, ok := page["attributes"].([]any)
	if !ok {
		return nil, fmt.Errorf("native attribute inventory unavailable")
	}
	out := map[string]any{}
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("malformed native attribute")
		}
		if !ownerSet[stringVal(row["ParentPrimitiveId"])] {
			continue
		}
		id := stringVal(row["primitiveId"])
		if id == "" || out[id] != nil {
			return nil, fmt.Errorf("invalid/duplicate owned attribute")
		}
		copy := maps.Clone(row)
		for _, key := range []string{"X", "Y", "Rotation"} {
			delete(copy, key)
		}
		out[id] = copy
	}
	return out, nil
}

func (e *schOwnedAttributesExpectation) check(result map[string]any) error {
	page, ok := result["pagePrimitives"].(map[string]any)
	if !ok {
		return fmt.Errorf("native attribute inventory unavailable")
	}
	have, err := schOwnedAttributes(page, e.Owners)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(e.Attributes, have) {
		return fmt.Errorf("attribute-preservation: %s", schDesignatorFirstDifference("attributes", e.Attributes, have))
	}
	return nil
}

func schPageInventoryByID(page map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for kind, value := range page {
		rows, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("pagePrimitives.%s unavailable", kind)
		}
		items := map[string]any{}
		for _, item := range rows {
			row, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("malformed pagePrimitives.%s", kind)
			}
			id := stringVal(row["primitiveId"])
			if id == "" || items[id] != nil {
				return nil, fmt.Errorf("invalid/duplicate pagePrimitives.%s id", kind)
			}
			items[id] = row
		}
		out[kind] = items
	}
	return out, nil
}

func checkSchPageInventory(want map[string]any, result map[string]any) error {
	page, ok := result["pagePrimitives"].(map[string]any)
	if !ok {
		return fmt.Errorf("complete page primitive inventory unavailable")
	}
	expected, err := schPageInventoryByID(want)
	if err != nil {
		return err
	}
	have, err := schPageInventoryByID(page)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, have) {
		return fmt.Errorf("drawing-reset: %s", schDesignatorFirstDifference("pagePrimitives", expected, have))
	}
	return nil
}

// Enumerate an explicit drawing-only deletion set. No pin or attribute id is
// ever sent to the connector; unknown graphics and orphan attributes refuse
// compilation rather than widening the set. Parts and the physical sheet stay.
func schPreservedDrawingReset(page map[string]any, preserved *schPreservedParts) ([]string, map[string]any, error) {
	retained := map[string]bool{}
	parts := map[string]bool{}
	for _, id := range preserved.IDs {
		retained[id] = true
		parts[id] = true
	}
	for _, part := range preserved.Parts {
		for _, item := range part["pins"].([]any) {
			id := stringVal(item.(map[string]any)["primitiveId"])
			if id == "" || retained[id] {
				return nil, nil, fmt.Errorf("invalid/duplicate preserved pin id")
			}
			retained[id] = true
		}
	}
	deleted := map[string]bool{}
	removedOwners := map[string]bool{}
	all := maps.Clone(retained)
	for kind, value := range page {
		if kind == "attributes" {
			continue
		}
		for _, item := range value.([]any) {
			row := item.(map[string]any)
			id := stringVal(row["primitiveId"])
			if kind == "components" {
				switch row["componentType"] {
				case "part":
					if !parts[id] {
						return nil, nil, fmt.Errorf("unprotected native part %s", id)
					}
				case "sheet":
					if all[id] {
						return nil, nil, fmt.Errorf("sheet id collides with protected identity %s", id)
					}
					retained[id] = true
				case "netflag", "netport", "netlabel":
					if all[id] {
						return nil, nil, fmt.Errorf("drawing id collides with protected identity %s", id)
					}
					deleted[id] = true
					// Marker pins are children of an explicitly deleted drawing
					// component. Prove their attribute ownership from the complete
					// rich source read, never by guessing an id prefix.
					components, _ := preserved.Scene["components"].(map[string]any)
					source, _ := components[id].(map[string]any)
					pins, ok := source["pins"].([]any)
					if !ok {
						return nil, nil, fmt.Errorf("drawing marker %s pin inventory unavailable", id)
					}
					for _, item := range pins {
						pin, ok := item.(map[string]any)
						if !ok {
							return nil, nil, fmt.Errorf("drawing marker %s has malformed pin", id)
						}
						pid := stringVal(pin["primitiveId"])
						if pid == "" || all[pid] {
							return nil, nil, fmt.Errorf("drawing marker %s has invalid/duplicate pin id", id)
						}
						removedOwners[pid], all[pid] = true, true
					}
				default:
					return nil, nil, fmt.Errorf("drawing-only reset does not support component kind %v", row["componentType"])
				}
			} else {
				if all[id] {
					return nil, nil, fmt.Errorf("drawing id collides with preserved identity %s", id)
				}
				switch kind {
				case "wires", "rectangles", "texts":
					deleted[id] = true
				default:
					return nil, nil, fmt.Errorf("drawing-only reset does not support %s", kind)
				}
			}
			all[id] = true
		}
	}
	for id := range deleted {
		removedOwners[id] = true
	}
	for _, item := range page["attributes"].([]any) {
		row := item.(map[string]any)
		id, parent := stringVal(row["primitiveId"]), stringVal(row["ParentPrimitiveId"])
		if all[id] || (!retained[parent] && !removedOwners[parent]) {
			return nil, nil, fmt.Errorf("drawing-only reset cannot attribute native attribute %s", id)
		}
		all[id] = true
	}
	// Detach the predicted residual inventory so later phase guards cannot
	// accidentally mutate the complete source-before evidence.
	raw, err := json.Marshal(page)
	if err != nil {
		return nil, nil, err
	}
	remaining := map[string]any{}
	if err = json.Unmarshal(raw, &remaining); err != nil {
		return nil, nil, err
	}
	for kind, value := range remaining {
		rows := []any{}
		for _, item := range value.([]any) {
			row := item.(map[string]any)
			if deleted[stringVal(row["primitiveId"])] || (kind == "attributes" && removedOwners[stringVal(row["ParentPrimitiveId"])]) {
				continue
			}
			rows = append(rows, row)
		}
		remaining[kind] = rows
	}
	ids := make([]string, 0, len(deleted))
	for id := range deleted {
		if retained[id] {
			return nil, nil, fmt.Errorf("drawing deletion intersects protected identity %s", id)
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, remaining, nil
}
