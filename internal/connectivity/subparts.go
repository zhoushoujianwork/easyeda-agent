package connectivity

import (
	"fmt"
	"sort"
	"strings"
)

// SubPart is one placed unit of a multi-unit device (EasyEDA Pro places each
// unit as its own schematic primitive; all units share one designator and one
// uniqueId and become a single footprint on the PCB). A component carries
// SubParts only when more than one unit was read back.
type SubPart struct {
	Name        string     `json:"name"`
	PrimitiveID string     `json:"primitiveId,omitempty"`
	PageID      string     `json:"pageId,omitempty"`
	PageName    string     `json:"pageName,omitempty"`
	Placement   *Placement `json:"placement,omitempty"`
}

// CheckMultiUnitParts decides whether raw part records that share a designator
// are the sub-parts of ONE physical device. It accepts only read-back evidence:
// every record must carry the same nonempty uniqueId, a distinct nonempty
// "<base>.<unit>" subPartName with a common base, agreeing agent component
// bindings and, where hydrated, the same 32-character device uuid. Anything
// missing or conflicting is rejected so distinct parts are never merged.
func CheckMultiUnitParts(ref string, records []map[string]any) error {
	if len(records) < 2 {
		return nil
	}
	uniqueID := stringField(records[0], "uniqueId")
	if uniqueID == "" {
		return fmt.Errorf("%s: instances carry no uniqueId; cannot prove they are sub-parts of one device", ref)
	}
	base, binding, device := "", "", ""
	names := map[string]bool{}
	for i, r := range records {
		if id := stringField(r, "uniqueId"); id != uniqueID {
			return fmt.Errorf("%s: instances have different uniqueId (%q vs %q); distinct devices share the designator", ref, uniqueID, id)
		}
		name := stringField(r, "subPartName")
		b, ok := subPartBase(name)
		if !ok {
			return fmt.Errorf("%s: instance subPartName %q is not a <device>.<unit> name; cannot prove a multi-unit device", ref, name)
		}
		if names[name] {
			return fmt.Errorf("%s: sub-part %q is placed more than once", ref, name)
		}
		names[name] = true
		var bound string
		if properties, ok := r["otherProperty"].(map[string]any); ok {
			value, _, err := componentBinding(properties, ComponentIDProperty)
			if err != nil {
				return fmt.Errorf("%s: %w", ref, err)
			}
			bound = value
		}
		dev := ""
		if d, ok := r["device"].(map[string]any); ok {
			if uuid := stringField(d, "uuid"); len(uuid) == 32 {
				dev = uuid
			}
		}
		if i == 0 {
			base, binding, device = b, bound, dev
			continue
		}
		if b != base {
			return fmt.Errorf("%s: sub-parts belong to different devices (%q vs %q)", ref, base, b)
		}
		if bound != binding {
			return fmt.Errorf("%s: sub-parts carry conflicting %s bindings", ref, ComponentIDProperty)
		}
		if dev != "" && device != "" && dev != device {
			return fmt.Errorf("%s: sub-parts resolve to different devices (%s vs %s)", ref, device, dev)
		}
		if device == "" {
			device = dev
		}
	}
	return nil
}

func subPartBase(name string) (string, bool) {
	i := strings.LastIndex(name, ".")
	if i <= 0 || i == len(name)-1 {
		return "", false
	}
	return name[:i], true
}

// GroupPartsByDesignator returns part records keyed by designator, in input
// order. A designator held by several records is accepted only when
// CheckMultiUnitParts proves they are units of one device.
func GroupPartsByDesignator(records []map[string]any) (map[string][]map[string]any, []string, error) {
	groups := map[string][]map[string]any{}
	order := []string{}
	for _, r := range records {
		ref, _ := r["designator"].(string)
		if _, ok := groups[ref]; !ok {
			order = append(order, ref)
		}
		groups[ref] = append(groups[ref], r)
	}
	for _, ref := range order {
		if err := CheckMultiUnitParts(ref, groups[ref]); err != nil {
			return nil, nil, err
		}
		sort.SliceStable(groups[ref], func(i, j int) bool {
			return stringField(groups[ref][i], "subPartName") < stringField(groups[ref][j], "subPartName")
		})
	}
	return groups, order, nil
}
