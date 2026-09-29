package app

import (
	"fmt"
	"sort"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// ValidateSchematicPeripheralDirect validates declared functional ownership
// against physical wire islands, never net names. Edges mean two components
// share an actual wire tree, not that current may flow through their bodies.
// Traversing peripheral edges permits authored series/feedback chains. A common
// rail name or a third component elsewhere cannot waive this ownership contract.
// This is a complete-design gate; partially placed Apply steps are not designs.
func ValidateSchematicPeripheralDirect(layout *SchematicLayoutResult, modules []connectivity.Module, roles ...map[string]string) error {
	if layout == nil || len(layout.Placements) == 0 || len(modules) == 0 {
		return fmt.Errorf("peripheral-ownership-incomplete: layout and explicit core/peripheral ownership required")
	}
	// Public composition drawing expectations may retain polyline records; the
	// physical island kernel consumes segments. Normalize a detached wrapper.
	copyLayout := *layout
	copyLayout.Wires = nil
	if _, err := drawingEdges(layout.Wires); err != nil {
		return fmt.Errorf("peripheral-direct-incomplete: %w", err)
	}
	for _, w := range layout.Wires {
		w.Points = plNormalizeWirePoints(w.Points)
		for i := 1; i < len(w.Points); i++ {
			copyLayout.Wires = append(copyLayout.Wires, SchematicWire{Net: w.Net, Points: [][2]float64{w.Points[i-1], w.Points[i]}})
		}
	}
	layout = &copyLayout
	islands, err := schematicVariantPhysicalPinIslands(layout)
	if err != nil {
		return fmt.Errorf("peripheral-direct-incomplete: %w", err)
	}
	refs := map[string]string{}
	for _, c := range layout.Placements {
		id := layout.ComponentIDs[c.Designator]
		if id == "" || refs[id] != "" {
			return fmt.Errorf("peripheral-ownership-incomplete: missing/duplicate identity for %s", c.Designator)
		}
		refs[id] = c.Designator
	}
	owners := map[string]string{}
	moduleIDs := map[string]bool{}
	for _, m := range modules {
		if m.ID == "" || moduleIDs[m.ID] || len(m.CoreComponents) == 0 {
			return fmt.Errorf("peripheral-ownership-incomplete: module %s needs unique identity and an explicit core", m.ID)
		}
		moduleIDs[m.ID] = true
		for _, id := range append(append([]string{}, m.CoreComponents...), m.PeripheralComponents...) {
			if refs[id] == "" || owners[id] != "" {
				return fmt.Errorf("peripheral-ownership-incomplete: module %s has unknown/multiply owned component %s", m.ID, id)
			}
			owners[id] = m.ID
		}
	}
	if len(owners) != len(refs) {
		return fmt.Errorf("peripheral-ownership-incomplete: every component needs explicit core/peripheral ownership")
	}
	// Real wire roots join pins; marker names never join otherwise separate roots.
	rootMembers := map[int]map[string]bool{}
	for pin, island := range islands {
		// Common ground must not stand in for a decoupler's supply attachment
		// or a filter's signal branch. Ground may remain locally marked.
		if schematicPeripheralNetRole(island.net, roles...) == "ground" {
			continue
		}
		if rootMembers[island.root] == nil {
			rootMembers[island.root] = map[string]bool{}
		}
		rootMembers[island.root][pin.component] = true
	}
	edges := map[string]map[string]bool{}
	for _, members := range rootMembers {
		for a := range members {
			if edges[a] == nil {
				edges[a] = map[string]bool{}
			}
			for b := range members {
				if a != b && owners[a] == owners[b] {
					edges[a][b] = true
				}
			}
		}
	}
	for _, m := range modules {
		cores, peripherals := map[string]bool{}, map[string]bool{}
		for _, id := range m.CoreComponents {
			cores[id] = true
		}
		for _, id := range m.PeripheralComponents {
			peripherals[id] = true
		}
		// A peripheral already attached via a supply/ground path must not hide a
		// severed EN/feedback/etc branch. Every module-local pin on a signal shared
		// by a core and its peripherals belongs to one mandatory physical tree.
		netPins := map[string][]schematicVariantPinIdentity{}
		coreNet, peripheralNet := map[string]bool{}, map[string]bool{}
		for _, c := range layout.Placements {
			id := layout.ComponentIDs[c.Designator]
			if owners[id] != m.ID {
				continue
			}
			for _, pin := range c.Pins {
				if pin.Net == "" || schematicPeripheralNetRole(pin.Net, roles...) != "signal" {
					continue
				}
				coreNet[pin.Net] = coreNet[pin.Net] || cores[id]
				peripheralNet[pin.Net] = peripheralNet[pin.Net] || peripherals[id]
				netPins[pin.Net] = append(netPins[pin.Net], schematicVariantPinIdentity{id, pin.Number})
			}
		}
		for _, net := range sortedStateKeys(netPins) {
			if !coreNet[net] || !peripheralNet[net] {
				continue
			}
			pins := netPins[net]
			first := islands[pins[0]]
			labels := make([]string, 0, len(pins))
			for _, pin := range pins {
				labels = append(labels, refs[pin.component]+"."+pin.number)
			}
			for _, pin := range pins[1:] {
				if islands[pin] != first {
					return fmt.Errorf("peripheral-direct-missing: module %s dedicated signal %s pins %v are split into physical wire islands (at %s.%s); supply/ground connectivity and same-name labels cannot replace this branch", m.ID, net, labels, refs[pin.component], pin.number)
				}
			}
		}
		reached := map[string]bool{}
		queue := append([]string{}, m.CoreComponents...)
		for _, id := range queue {
			reached[id] = true
		}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			for neighbor := range edges[id] {
				if !reached[neighbor] {
					reached[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}
		var missing []string
		for _, id := range m.PeripheralComponents {
			if !reached[id] {
				missing = append(missing, refs[id]+" ("+id+")")
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return fmt.Errorf("peripheral-direct-missing: module %s peripherals %v have no non-ground physical wire path to their declared core; common ground and same-name labels cannot replace dedicated peripheral connections", m.ID, missing)
		}
	}
	return nil
}

func validateSchCompositionPeripheralDirect(p *powerLayoutPlan, d connectivity.Document, moduleID string) error {
	layout := &SchematicLayoutResult{ComponentIDs: map[string]string{}, Placements: p.Placements, Wires: p.Wires, Flags: p.Flags}
	for _, c := range d.Components {
		layout.ComponentIDs[c.Ref] = c.ID
	}
	var modules []connectivity.Module
	for _, m := range d.Modules {
		if moduleID == "" || m.ID == moduleID {
			modules = append(modules, m)
		}
	}
	return ValidateSchematicPeripheralDirect(layout, modules, schematicCanonicalNetRoles(d))
}

// Label mode is an explicit design choice: ownership remains exhaustive, but
// physical islands may join through same-name markers. The named-lead check is
// separate from membership so the fresh reader can prove marker coverage from
// actual primitives instead of accepting source flags as observed evidence.
func validateSchematicLabelledOwnership(layout *SchematicLayoutResult, modules []connectivity.Module) error {
	if layout == nil || len(layout.Placements) == 0 || len(modules) == 0 {
		return fmt.Errorf("peripheral-ownership-incomplete: layout and explicit core/peripheral ownership required")
	}
	refs := map[string]string{}
	for _, c := range layout.Placements {
		id := layout.ComponentIDs[c.Designator]
		if id == "" || refs[id] != "" {
			return fmt.Errorf("peripheral-ownership-incomplete: missing/duplicate identity for %s", c.Designator)
		}
		refs[id] = c.Designator
	}
	owners := map[string]string{}
	moduleIDs := map[string]bool{}
	for _, m := range modules {
		if m.ID == "" || moduleIDs[m.ID] || len(m.CoreComponents) == 0 {
			return fmt.Errorf("peripheral-ownership-incomplete: module %s needs unique identity and an explicit core", m.ID)
		}
		moduleIDs[m.ID] = true
		for _, id := range append(append([]string{}, m.CoreComponents...), m.PeripheralComponents...) {
			if refs[id] == "" || owners[id] != "" {
				return fmt.Errorf("peripheral-ownership-incomplete: module %s has unknown/multiply owned component %s", m.ID, id)
			}
			owners[id] = m.ID
		}
	}
	if len(owners) != len(refs) {
		return fmt.Errorf("peripheral-ownership-incomplete: every component needs explicit core/peripheral ownership")
	}
	return nil
}

func validateSchCompositionPeripheralLabels(p *powerLayoutPlan, d connectivity.Document, moduleID string) error {
	if err := validateSchCompositionNets(p); err != nil {
		return fmt.Errorf("peripheral-label-incomplete: %w", err)
	}
	layout := &SchematicLayoutResult{ComponentIDs: map[string]string{}, Placements: p.Placements, Wires: p.Wires, Flags: p.Flags}
	for _, c := range d.Components {
		layout.ComponentIDs[c.Ref] = c.ID
	}
	var modules []connectivity.Module
	for _, m := range d.Modules {
		if moduleID == "" || m.ID == moduleID {
			modules = append(modules, m)
		}
	}
	return validateSchematicLabelledOwnership(layout, modules)
}

func validateSchematicLayoutPeripheralDirect(layout *SchematicLayoutResult, coreID string, roles map[string]string) error {
	m := connectivity.Module{ID: "layout", CoreComponents: []string{coreID}}
	for _, c := range layout.Placements {
		id := layout.ComponentIDs[c.Designator]
		if id != coreID {
			m.PeripheralComponents = append(m.PeripheralComponents, id)
		}
	}
	return ValidateSchematicPeripheralDirect(layout, []connectivity.Module{m}, roles)
}

// Existing conventional rail classification is a compatibility fallback only.
// Explicit canonical role takes priority, including explicit signal on a name
// that looks like a voltage. Scope/global alone never grants a rail exception.
func schematicPeripheralNetRole(net string, roles ...map[string]string) string {
	if len(roles) > 0 {
		switch roles[0][net] {
		case "power", "ground", "signal":
			return roles[0][net]
		}
	}
	return tidyNetClass(net)
}

func schematicCanonicalNetRoles(d connectivity.Document) map[string]string {
	roles := map[string]string{}
	for _, n := range d.Nets {
		roles[n.Name] = schematicPeripheralNetRole(n.Name, map[string]string{n.Name: n.Role})
	}
	return roles
}

func schematicMandatoryPeripheralSignalPolicies(input *SchematicLayoutInput) map[string]string {
	roles, coreNets, peripheralNets := map[string]string{}, map[string]bool{}, map[string]bool{}
	for net, policy := range input.NetPolicies {
		role := tidyNetClass(net)
		if policy == "local_power" {
			role = "power"
		} else if policy == "local_ground" {
			role = "ground"
		}
		roles[net] = role
	}
	for _, c := range input.Components {
		for _, pin := range c.Measurement.Pins {
			if pin.Net != "" {
				if c.ID == input.CoreComponentID {
					coreNets[pin.Net] = true
				} else {
					peripheralNets[pin.Net] = true
				}
			}
		}
	}
	for net := range coreNets {
		if peripheralNets[net] && roles[net] == "signal" {
			input.NetPolicies[net] = "direct"
		}
	}
	// A module_port remains the external zone-boundary contract, but repeated
	// signal pins on the same symbol side need one physical in-zone fanout tree.
	// Otherwise a dense connector can fall back to one label per 10-raw pin and
	// become geometrically impossible after its downstream protection device is
	// split into a separate zone.  Promote only the detached local input copy;
	// the source zones and their cross-zone policy remain module_port.
	for _, c := range input.Components {
		counts := map[string]map[string]int{}
		for _, pin := range c.Measurement.Pins {
			if pin.Net == "" || input.NetPolicies[pin.Net] != "module_port" || roles[pin.Net] != "signal" {
				continue
			}
			side, err := libPinSide(pin, c.Measurement.BBox)
			if err != nil {
				continue
			}
			if counts[pin.Net] == nil {
				counts[pin.Net] = map[string]int{}
			}
			counts[pin.Net][side]++
			if counts[pin.Net][side] > 1 {
				input.NetPolicies[pin.Net] = "direct"
			}
		}
	}
	return roles
}
