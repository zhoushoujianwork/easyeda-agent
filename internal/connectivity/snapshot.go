package connectivity

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// FromRead converts the active-page semantic read. It rejects shallow data.
// Records may carry pageUuid/pageName (tagged reads); sub-parts of one
// multi-unit device, proven by CheckMultiUnitParts, become one component.
func FromRead(m map[string]any) (Document, error) {
	d := Document{SchemaVersion: "1.4", Components: []Component{}, Nets: []Net{}, Connections: []Connection{}}
	cs, ok := m["components"].([]any)
	if !ok {
		return d, fmt.Errorf("missing components inventory")
	}
	var records []map[string]any
	for _, v := range cs {
		x, ok := v.(map[string]any)
		if !ok {
			return d, fmt.Errorf("invalid component")
		}
		if x["componentType"] != "part" {
			continue
		}
		if available, declared := m["pinNetsAvailable"].(bool); declared && !available {
			return d, fmt.Errorf("pin net evidence unavailable; cannot construct connectivity")
		}
		records = append(records, x)
	}
	groups, order, err := GroupPartsByDesignator(records)
	if err != nil {
		return d, fmt.Errorf("duplicate component designator: %w", err)
	}
	nets := map[string]string{}
	for _, ref := range order {
		var c Component
		var units []Component
		var conns [][]Connection
		for _, x := range groups[ref] {
			unit, unitConns, err := componentFromRecord(x, nets)
			if err != nil {
				return d, err
			}
			units = append(units, unit)
			conns = append(conns, unitConns)
		}
		if len(units) == 1 {
			c = units[0]
			d.Connections = append(d.Connections, conns[0]...)
		} else {
			c, err = mergeSubParts(ref, groups[ref], units)
			if err != nil {
				return d, err
			}
			for _, unitConns := range conns {
				d.Connections = append(d.Connections, unitConns...)
			}
		}
		d.Components = append(d.Components, c)
	}
	for name, id := range nets {
		scope, role := classifyNet(name)
		d.Nets = append(d.Nets, Net{ID: id, Name: name, Scope: scope, Role: role})
	}
	sort.Slice(d.Components, func(i, j int) bool { return d.Components[i].ID < d.Components[j].ID })
	sort.Slice(d.Nets, func(i, j int) bool { return d.Nets[i].ID < d.Nets[j].ID })
	sort.Slice(d.Connections, func(i, j int) bool {
		a, b := d.Connections[i], d.Connections[j]
		if a.ComponentID != b.ComponentID {
			return a.ComponentID < b.ComponentID
		}
		return a.PinNumber < b.PinNumber
	})
	return d, d.Validate()
}

// mergeSubParts folds the units of one device into a single component. Pin
// numbers must be disjoint across units; each pin records its owning unit.
func mergeSubParts(ref string, records []map[string]any, units []Component) (Component, error) {
	c := units[0]
	c.Placement, c.Pins, c.PageID, c.PageName = nil, nil, "", ""
	owner := map[string]string{}
	pages := map[string]bool{}
	for i, u := range units {
		name := stringField(records[i], "subPartName")
		if u.ID != c.ID {
			return c, fmt.Errorf("%s: sub-parts resolve to different component ids", ref)
		}
		if len(c.Device.UUID) != 32 && len(u.Device.UUID) == 32 {
			c.Device = u.Device
		}
		c.SubParts = append(c.SubParts, SubPart{Name: name, PrimitiveID: stringField(records[i], "primitiveId"), PageID: u.PageID, PageName: u.PageName, Placement: u.Placement})
		pages[u.PageID+"\x00"+u.PageName] = true
		for _, p := range u.Pins {
			if previous, dup := owner[p.Number]; dup {
				return c, fmt.Errorf("%s: pin %s appears on sub-parts %s and %s; cannot attribute pins unambiguously", ref, p.Number, previous, name)
			}
			owner[p.Number] = name
			p.SubPart = name
			c.Pins = append(c.Pins, p)
		}
	}
	if len(pages) == 1 {
		c.PageID, c.PageName = units[0].PageID, units[0].PageName
	}
	sort.Slice(c.Pins, func(i, j int) bool { return c.Pins[i].Number < c.Pins[j].Number })
	return c, nil
}

func componentFromRecord(x map[string]any, nets map[string]string) (Component, []Connection, error) {
	var conns []Connection
	ref, _ := x["designator"].(string)
	if ambiguous, _ := x["netAmbiguous"].(bool); ambiguous {
		return Component{}, nil, fmt.Errorf("%s: pin net attribution ambiguous; cannot construct connectivity", ref)
	}
	if unavailable, _ := x["pinNetsUnavailable"].(bool); unavailable {
		return Component{}, nil, fmt.Errorf("%s: pin net evidence unavailable; cannot construct connectivity", ref)
	}
	if available, declared := x["netlistAvailable"].(bool); declared && !available {
		return Component{}, nil, fmt.Errorf("%s: pin net evidence unavailable; cannot construct connectivity", ref)
	}
	c := Component{ID: "cmp-" + ref, Ref: ref, UniqueID: stringField(x, "uniqueId"), PageID: stringField(x, "pageUuid"), PageName: stringField(x, "pageName")}
	if properties, ok := x["otherProperty"].(map[string]any); ok {
		if id, exists, err := componentBinding(properties, ComponentIDProperty); err != nil {
			return Component{}, nil, fmt.Errorf("%s: %w", ref, err)
		} else if exists {
			c.ID = id
		}
		if role, _, err := componentBinding(properties, ComponentRoleProperty); err != nil {
			return Component{}, nil, fmt.Errorf("%s: %w", ref, err)
		} else {
			c.Role = role
		}
	}
	if dev, ok := x["device"].(map[string]any); ok {
		c.Device = Device{LibraryUUID: stringField(dev, "libraryUuid"), UUID: stringField(dev, "uuid"), Name: stringField(dev, "name")}
	}
	if c.Device.UUID == "" {
		if dev, ok := x["component"].(map[string]any); ok {
			c.Device = Device{LibraryUUID: stringField(dev, "libraryUuid"), UUID: stringField(dev, "uuid"), Name: stringField(dev, "name")}
		}
	}
	c.Footprint = stringField(x, "footprint")
	if footprint, ok := x["footprint"].(map[string]any); ok {
		c.Footprint = stringField(footprint, "uuid")
		c.FootprintRef = make(map[string]any, len(footprint))
		for key, value := range footprint {
			c.FootprintRef[key] = value
		}
	}
	if xv, xok := numberValue(x["x"]); xok {
		if yv, yok := numberValue(x["y"]); yok {
			c.Placement = &Placement{X: xv, Y: yv}
			if rv, rok := numberValue(x["rotation"]); rok {
				c.Placement.Rotation = rv
			}
			if mv, mok := x["mirror"].(bool); mok {
				c.Placement.Mirror = mv
			}
		}
	}
	if bb, ok := x["bbox"].(map[string]any); ok {
		if minx, a := numberValue(bb["minX"]); a {
			if miny, b := numberValue(bb["minY"]); b {
				if maxx, c1 := numberValue(bb["maxX"]); c1 {
					if maxy, d := numberValue(bb["maxY"]); d {
						if c.Placement == nil {
							c.Placement = &Placement{}
						}
						c.Placement.BBox = &BBox{MinX: minx, MinY: miny, MaxX: maxx, MaxY: maxy}
					}
				}
			}
		}
	}
	ps, ok := x["pins"].([]any)
	if !ok || len(ps) == 0 {
		return Component{}, nil, fmt.Errorf("%s: pin inventory unavailable; read each active page separately", ref)
	}
	for _, v := range ps {
		p, ok := v.(map[string]any)
		if !ok {
			return Component{}, nil, fmt.Errorf("%s: invalid pin", ref)
		}
		n, _ := p["number"].(string)
		if n == "" {
			n, _ = p["pinNumber"].(string)
		}
		name, _ := p["name"].(string)
		if name == "" {
			name, _ = p["pinName"].(string)
		}
		if _, ok := p["net"]; !ok {
			return Component{}, nil, fmt.Errorf("%s.%s: missing net evidence", ref, n)
		}
		nc, ncKnown := p["noConnected"].(bool)
		pin := Pin{Number: n, Name: name, NoConnected: nc}
		if net, known := p["net"].(string); known && net == "" && ncKnown && !nc {
			pin.ConnectionState = "unconnected"
		}
		pin.X, _ = numberValue(p["x"])
		pin.Y, _ = numberValue(p["y"])
		c.Pins = append(c.Pins, pin)
		if p["net"] != nil {
			net, ok := p["net"].(string)
			if !ok {
				return Component{}, nil, fmt.Errorf("invalid net value")
			}
			if net != "" {
				id := fmt.Sprintf("net-%x", sha256.Sum256([]byte(net)))
				nets[net] = id
				conns = append(conns, Connection{ComponentID: c.ID, PinNumber: n, NetID: id, Kind: "netlist"})
			}
		}
	}
	sort.Slice(c.Pins, func(i, j int) bool { return c.Pins[i].Number < c.Pins[j].Number })
	return c, conns, nil
}

// classifyNet keeps the connectivity IR useful to layout/materialization code
// without making geometry part of the electrical truth.  Power and ground are
// global semantic rails: they may be represented by repeated local symbols.
// Everything else is a local signal until a module/page boundary promotes it
// to a port or label during materialization.
func classifyNet(name string) (scope, role string) {
	n := strings.ToUpper(strings.TrimSpace(name))
	if n == "GND" || n == "AGND" || n == "DGND" || n == "PGND" || strings.HasSuffix(n, "_GND") {
		return "global", "ground"
	}
	if n == "VCC" || n == "VBUS" || n == "VDD" || n == "VSS" || n == "3V3" || n == "5V" || n == "12V" || n == "1V8" ||
		strings.HasPrefix(n, "+") || strings.HasPrefix(n, "VDD_") ||
		strings.HasPrefix(n, "VCC_") || strings.HasPrefix(n, "VBAT") ||
		strings.HasSuffix(n, "_VCC") || strings.HasSuffix(n, "_VDD") ||
		strings.HasSuffix(n, "_3V3") || strings.HasSuffix(n, "_5V") {
		return "global", "power"
	}
	return "local", "signal"
}

func numberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	default:
		return 0, false
	}
}

func stringField(m map[string]any, key string) string { v, _ := m[key].(string); return v }

// NamedPins compares observed connectivity using instance references and net names.
// Null is an observed unassigned pin, not an explicit NC declaration.
func (d Document) NamedPins() (map[string]*string, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	names := map[string]string{}
	refs := map[string]string{}
	out := map[string]*string{}
	for _, n := range d.Nets {
		if n.Name == "" {
			return nil, fmt.Errorf("net %s has no name", n.ID)
		}
		names[n.ID] = n.Name
	}
	for _, c := range d.Components {
		refs[c.ID] = c.Ref
		for _, p := range c.Pins {
			out[fmt.Sprintf("%q/%q", c.Ref, p.Number)] = nil
		}
	}
	for _, c := range d.Connections {
		n := names[c.NetID]
		out[fmt.Sprintf("%q/%q", refs[c.ComponentID], c.PinNumber)] = &n
	}
	return out, nil
}
