package app

import (
	"encoding/json"
	"fmt"
)

// Check original bytes, including nonselected alternatives. Re-marshalling a
// typed candidate first would turn missing coordinates into invented zeros.
func validateRenderVariantsJSON(raw []byte) error {
	var top struct {
		Zones []map[string]json.RawMessage `json:"zones"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	for _, zone := range top.Zones {
		if b, ok := zone["layout"]; ok {
			var layout map[string]json.RawMessage
			if err := json.Unmarshal(b, &layout); err != nil {
				return err
			}
			if _, nested := layout["variants"]; nested {
				return fmt.Errorf("render requires zone-level variants, not nested layout variants")
			}
			if b, supplied := layout["allowedRotations"]; supplied {
				var allowed map[string]json.RawMessage
				if string(b) == "null" || json.Unmarshal(b, &allowed) != nil {
					return fmt.Errorf("allowedRotations requires an explicit component map")
				}
				for id, rawAngles := range allowed {
					var angles []json.RawMessage
					if string(rawAngles) == "null" || json.Unmarshal(rawAngles, &angles) != nil || len(angles) == 0 || len(angles) > 4 {
						return fmt.Errorf("allowedRotations for %s requires 1..4 explicit angles", id)
					}
					seen := map[float64]bool{}
					for _, rawAngle := range angles {
						var angle float64
						if string(rawAngle) == "null" || json.Unmarshal(rawAngle, &angle) != nil || (angle != 0 && angle != 90 && angle != 180 && angle != 270) || seen[angle] {
							return fmt.Errorf("allowedRotations for %s requires unique explicit 0/90/180/270 angles", id)
						}
						seen[angle] = true
					}
				}
			}
		}
		b, ok := zone["variants"]
		if !ok {
			continue
		}
		var variants []map[string]json.RawMessage
		if string(b) == "null" || json.Unmarshal(b, &variants) != nil || len(variants) < 1 || len(variants) > 4 {
			return fmt.Errorf("zone variants requires 1..4 complete alternatives")
		}
		// Main geometry is equally untrusted: missing zero-valued main bounds
		// must not be invented by typed decode just because a variant matches.
		for _, v := range append([]map[string]json.RawMessage{zone}, variants...) {
			for _, key := range []string{"id", "layout", "frame", "contentBounds"} {
				if len(v[key]) == 0 || string(v[key]) == "null" {
					return fmt.Errorf("variant requires explicit %s", key)
				}
			}
			var layout map[string]json.RawMessage
			if err := json.Unmarshal(v["layout"], &layout); err != nil {
				return err
			}
			if _, nested := layout["variants"]; nested {
				return fmt.Errorf("nested variants are not allowed")
			}
			var frame map[string]json.RawMessage
			if err := json.Unmarshal(v["frame"], &frame); err != nil {
				return err
			}
			for _, key := range []string{"titleX", "titleY", "fontSize"} {
				if len(frame[key]) == 0 || string(frame[key]) == "null" {
					return fmt.Errorf("variant frame requires explicit %s", key)
				}
			}
			for _, b := range []json.RawMessage{v["contentBounds"], frame["rect"]} {
				var box map[string]json.RawMessage
				if json.Unmarshal(b, &box) != nil {
					return fmt.Errorf("variant requires explicit bounds")
				}
				for _, key := range []string{"minX", "minY", "maxX", "maxY"} {
					if len(box[key]) == 0 || string(box[key]) == "null" {
						return fmt.Errorf("variant bounds requires explicit %s", key)
					}
				}
			}
			wrapped, err := json.Marshal(map[string]any{"zones": []any{map[string]json.RawMessage{"layout": v["layout"]}}})
			if err != nil {
				return err
			}
			if err = validateRenderMeasurementsJSON(wrapped); err != nil {
				return fmt.Errorf("variant geometry: %w", err)
			}
		}
	}
	return nil
}
