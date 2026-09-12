package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// The electrical graph and measured poses are immutable. Only translations,
// routes and naming markers are searched, on a bounded five-raw grid.
func planLibLayout(input libLayoutSource) (*schCompositionSource, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var src libLayoutSource
	if err = json.Unmarshal(raw, &src); err != nil {
		return nil, err
	}
	fail := func(format string, args ...any) (*schCompositionSource, error) {
		return nil, fmt.Errorf("unresolved: "+format, args...)
	}
	if src.SchemaVersion != 1 || src.Connectivity.ProjectID == "" || src.Connectivity.DocumentID == "" || !plBoxValid(src.Sheet) {
		return fail("schemaVersion:1, target project/page and measured sheet are required")
	}
	if _, _, err := schCompositionUsableBounds(src.Sheet, src.SheetBorder); err != nil {
		return fail("%v", err)
	}
	budget := src.MaxCandidates
	if budget == 0 {
		budget = 20000
	}
	if budget < 1 || budget > 1000000 {
		return fail("maxCandidates must be 1..1000000 (default 20000)")
	}
	for _, k := range src.Keepouts {
		if !plBoxValid(k) || !boxInside(k, src.Sheet) {
			return fail("invalid keepout")
		}
	}
	d := src.Connectivity
	if err = d.Validate(); err != nil {
		return fail("%v", err)
	}
	byID, byRef := map[string]connectivity.Component{}, map[string]connectivity.Component{}
	netNames, names := map[string]string{}, map[string]bool{}
	pinNet := map[string]map[string]string{}
	for _, n := range d.Nets {
		if strings.TrimSpace(n.Name) == "" || names[n.Name] {
			return fail("net %s needs a unique nonempty name", n.ID)
		}
		netNames[n.ID], names[n.Name] = n.Name, true
	}
	for _, c := range d.Components {
		if c.Device.LibraryUUID == "" || !isDeviceLibraryUUID(c.Device.UUID) || len(c.Pins) == 0 {
			return fail("%s needs library identity and complete physical pins", c.Ref)
		}
		byID[c.ID], byRef[c.Ref], pinNet[c.ID] = c, c, map[string]string{}
	}
	for _, edge := range d.Connections {
		pinNet[edge.ComponentID][edge.PinNumber] = edge.NetID
	}
	for _, c := range d.Components {
		for _, q := range c.Pins {
			states := 0
			if pinNet[c.ID][q.Number] != "" {
				states++
			}
			if q.NoConnected {
				states++
			}
			if q.ConnectionState == "unconnected" {
				states++
			}
			if states != 1 {
				return fail("%s.%s must have exactly one net or explicit NC or unconnected intent", c.Ref, q.Number)
			}
		}
	}
	measured := map[string]powerLayoutPlacement{}
	for _, m := range src.Measurements {
		c, ok := byRef[m.Designator]
		if !ok || measured[c.ID].Designator != "" {
			return fail("unknown/duplicate measurement %s", m.Designator)
		}
		if !plBoxValid(m.BBox) || !plGrid(m.X) || !plGrid(m.Y) || !plFinite(m.Rotation) || math.Mod(m.Rotation, 90) != 0 || len(m.Pins) != len(c.Pins) {
			return fail("%s incomplete measured geometry", c.Ref)
		}
		pins := map[string]connectivity.Pin{}
		for _, q := range c.Pins {
			pins[q.Number] = q
		}
		seen := map[string]bool{}
		for _, q := range m.Pins {
			cp, exists := pins[q.Number]
			if !exists || seen[q.Number] || cp.Name != q.Name || !plGrid(q.X) || !plGrid(q.Y) || q.Net != netNames[pinNet[c.ID][q.Number]] {
				return fail("%s.%s measured pin differs from canonical IR", c.Ref, q.Number)
			}
			if _, err := libPinSide(q, m.BBox); err != nil {
				return fail("%s: %v", c.Ref, err)
			}
			seen[q.Number] = true
		}
		measured[c.ID] = m
	}
	if len(measured) != len(d.Components) {
		return fail("measurements must cover every component")
	}
	modules, owners := map[string]connectivity.Module{}, map[string]string{}
	for _, m := range d.Modules {
		if m.ID == "" || modules[m.ID].ID != "" {
			return fail("unknown/duplicate canonical module %s", m.ID)
		}
		for _, id := range append(append([]string{}, m.CoreComponents...), m.PeripheralComponents...) {
			if byID[id].ID == "" || owners[id] != "" {
				return fail("unknown/repeated module member %s", id)
			}
			owners[id] = m.ID
		}
		modules[m.ID] = m
	}
	if len(owners) != len(d.Components) {
		return fail("every component must belong to one module")
	}
	result := &schCompositionSource{SchemaVersion: 1, Connectivity: d, Sheet: src.Sheet, SheetBorder: src.SheetBorder, Keepouts: src.Keepouts}
	seenModules := map[string]bool{}
	for _, intent := range src.LayoutModules {
		cm, ok := modules[intent.ID]
		if !ok || seenModules[intent.ID] {
			return fail("unknown/duplicate layout module %s", intent.ID)
		}
		seenModules[intent.ID] = true
		rootOK := false
		for _, id := range cm.CoreComponents {
			rootOK = rootOK || id == intent.CoreComponentID
		}
		if !rootOK {
			return fail("module %s coreComponentId must name a canonical core", intent.ID)
		}
		members := append(append([]string{}, cm.CoreComponents...), cm.PeripheralComponents...)
		netPolicies := map[string]string{}
		usedNets := map[string]bool{}
		for _, id := range members {
			for _, n := range pinNet[id] {
				usedNets[n] = true
			}
		}
		for n := range usedNets {
			switch intent.NetPolicies[n] {
			case "direct", "local_power", "local_ground", "module_port":
				netPolicies[netNames[n]] = intent.NetPolicies[n]
			default:
				return fail("module %s net %s needs an explicit supported policy", intent.ID, n)
			}
		}
		for n := range intent.NetPolicies {
			if !usedNets[n] {
				return fail("module %s unknown/unused policy %s", intent.ID, n)
			}
		}
		hints := map[string]libLayoutPeripheral{}
		for _, h := range intent.Peripherals {
			if owners[h.ComponentID] != intent.ID || h.ComponentID == intent.CoreComponentID || hints[h.ComponentID].ComponentID != "" {
				return fail("invalid/duplicate peripheral hint %s", h.ComponentID)
			}
			if h.AttachTo != nil && (owners[h.AttachTo.ComponentID] != intent.ID || h.AttachTo.ComponentID == h.ComponentID) {
				return fail("%s attachment target must be another module member", h.ComponentID)
			}
			hints[h.ComponentID] = h
		}
		input := SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: intent.CoreComponentID, NetPolicies: netPolicies, Attachments: intent.Peripherals}
		for _, id := range members {
			states := map[string]string{}
			for _, pin := range byID[id].Pins {
				if pin.NoConnected {
					states[pin.Number] = "nc"
				} else if pin.ConnectionState == "unconnected" {
					states[pin.Number] = "unconnected"
				}
			}
			input.Components = append(input.Components, SchematicLayoutComponent{ID: id, Measurement: measured[id], PinStates: states})
		}
		local, e := planSchematicLayoutWithBudget(input, &budget)
		if e != nil {
			return fail("module %s: %v", intent.ID, e)
		}
		p := powerLayoutPlan{Placements: local.Placements, Wires: local.Wires, Flags: local.Flags}
		result.Modules = append(result.Modules, schCompositionModule{ID: intent.ID, Title: intent.Title, Placements: p.Placements, Wires: p.Wires, Flags: p.Flags})
	}
	if len(seenModules) != len(modules) {
		return fail("layoutModules must cover every canonical module")
	}
	if _, err = planSchComposition(*result); err != nil {
		return fail("composition validation: %v", err)
	}
	return result, nil
}
