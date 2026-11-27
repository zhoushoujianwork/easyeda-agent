package connectivity

import "strings"

// EasyEDA Pro places one multi-unit device as several part records: one per
// unit symbol. Those records share the native uniqueId, repeat the reference
// designator, and end up as a single footprint on the PCB. The designator
// string alone therefore cannot identify a device instance, and treating the
// sub-parts as separate devices reports a duplicate for a correct design.
//
// The explicit agent binding wins because the designator flows write it; the
// native uniqueId is the platform's own instance key. A record that carries
// neither identity stays unique on its own, so two different devices wearing
// one designator are still reported.

// PartInstanceKeys returns the identities that attribute a placed part record
// to a device instance. An empty result means the record cannot be attributed
// to a known instance and must stay unique on its own.
func PartInstanceKeys(record map[string]any) []string {
	keys := []string{}
	if properties, ok := record["otherProperty"].(map[string]any); ok {
		if id, exists, err := componentBinding(properties, ComponentIDProperty); err == nil && exists {
			keys = append(keys, "bound:"+id)
		}
	}
	if uid := strings.TrimSpace(stringField(record, "uniqueId")); uid != "" {
		keys = append(keys, "uid:"+uid)
	}
	return keys
}

// GroupPartInstances groups placed part records into device instances. Records
// that share any instance identity belong to the same device, so a bound
// sub-part and an unbound one still merge through their native uniqueId.
// The group order follows the read order; each group keeps that order too.
func GroupPartInstances(records []map[string]any) [][]map[string]any {
	parent := make([]int, len(records))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	owner := map[string]int{}
	for i, record := range records {
		for _, key := range PartInstanceKeys(record) {
			first, exists := owner[key]
			if !exists {
				owner[key] = i
				continue
			}
			a, b := find(first), find(i)
			if a != b {
				parent[b] = a
			}
		}
	}
	groups := map[int][]map[string]any{}
	order := []int{}
	for i, record := range records {
		root := find(i)
		if _, exists := groups[root]; !exists {
			order = append(order, root)
		}
		groups[root] = append(groups[root], record)
	}
	out := make([][]map[string]any, 0, len(order))
	for _, root := range order {
		out = append(out, groups[root])
	}
	return out
}
