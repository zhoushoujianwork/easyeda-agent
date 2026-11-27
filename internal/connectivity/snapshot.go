package connectivity

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// FromRead converts the active-page semantic read. It rejects shallow data.
func FromRead(m map[string]any) (Document, error) {
	d := Document{SchemaVersion: "1.4", Components: []Component{}, Nets: []Net{}, Connections: []Connection{}}
	cs, ok := m["components"].([]any)
	if !ok {
		return d, fmt.Errorf("missing components inventory")
	}
	records := []map[string]any{}
	for _, v := range cs {
		x, ok := v.(map[string]any)
		if !ok {
			return d, fmt.Errorf("invalid component")
		}
		if x["componentType"] != "part" {
			continue
		}
		records = append(records, x)
	}
	nets := map[string]string{}
	// One multi-unit device is placed as several records that share the
	// native uniqueId and repeat the reference designator. They form one
	// device instance, so they are read as one canonical component.
	for _, group := range GroupPartInstances(records) {
		c, connections, err := readDeviceInstance(m, group, nets)
		if err != nil {
			return d, err
		}
		d.Components = append(d.Components, c)
		d.Connections = append(d.Connections, connections...)
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

// readDeviceInstance reads the placed records of one device instance into a
// canonical component. A multi-unit device contributes one record per unit;
// their pins form one physical inventory, so a pin number may only appear
// once across the whole instance.
func readDeviceInstance(m map[string]any, records []map[string]any, nets map[string]string) (Component, []Connection, error) {
	var c Component
	connections := []Connection{}
	ref, unique, bound := "", "", ""
	hasBound := false
	designators := []string{}
	seenRef := map[string]bool{}
	for _, x := range records {
		r, _ := x["designator"].(string)
		if !seenRef[r] {
			seenRef[r] = true
			designators = append(designators, r)
		}
		if ref == "" {
			ref = r
		}
		if uid := stringField(x, "uniqueId"); uid != "" {
			if unique == "" {
				unique = uid
			} else if unique != uid {
				return c, connections, fmt.Errorf("%s: device instance carries conflicting uniqueIds %s and %s", ref, unique, uid)
			}
		}
		if properties, ok := x["otherProperty"].(map[string]any); ok {
			id, exists, err := componentBinding(properties, ComponentIDProperty)
			if err != nil {
				return c, connections, fmt.Errorf("%s: %w", ref, err)
			} else if exists {
				if hasBound && bound != id {
					return c, connections, fmt.Errorf("%s: conflicting canonical component bindings %s and %s", ref, bound, id)
				}
				bound, hasBound = id, true
			}
			if role, _, err := componentBinding(properties, ComponentRoleProperty); err != nil {
				return c, connections, fmt.Errorf("%s: %w", ref, err)
			} else if role != "" {
				c.Role = role
			}
		}
	}
	// One device identity must name one device. Records that claim the same
	// identity under different designators are two devices, not a multi-unit
	// device, and the identity stays unusable for both.
	if len(designators) > 1 {
		identity := "cmp-" + ref
		if hasBound {
			identity = bound
		}
		sort.Strings(designators)
		return c, connections, fmt.Errorf("duplicate component %s; one device identity carries the designators %s", identity, strings.Join(designators, ", "))
	}
	c.Ref, c.UniqueID = ref, unique
	c.ID = "cmp-" + ref
	if hasBound {
		c.ID = bound
	}
	for _, x := range records {
		if available, declared := m["pinNetsAvailable"].(bool); declared && !available {
			return c, connections, fmt.Errorf("pin net evidence unavailable; cannot construct connectivity")
		}
		if ambiguous, _ := x["netAmbiguous"].(bool); ambiguous {
			return c, connections, fmt.Errorf("%s: pin net attribution ambiguous; cannot construct connectivity", ref)
		}
		if unavailable, _ := x["pinNetsUnavailable"].(bool); unavailable {
			return c, connections, fmt.Errorf("%s: pin net evidence unavailable; cannot construct connectivity", ref)
		}
		if available, declared := x["netlistAvailable"].(bool); declared && !available {
			return c, connections, fmt.Errorf("%s: pin net evidence unavailable; cannot construct connectivity", ref)
		}
	}
	primary := records[0]
	if dev, ok := primary["device"].(map[string]any); ok {
		c.Device = Device{LibraryUUID: stringField(dev, "libraryUuid"), UUID: stringField(dev, "uuid"), Name: stringField(dev, "name")}
	}
	if c.Device.UUID == "" {
		if dev, ok := primary["component"].(map[string]any); ok {
			c.Device = Device{LibraryUUID: stringField(dev, "libraryUuid"), UUID: stringField(dev, "uuid"), Name: stringField(dev, "name")}
		}
	}
	c.Footprint = stringField(primary, "footprint")
	if footprint, ok := primary["footprint"].(map[string]any); ok {
		c.Footprint = stringField(footprint, "uuid")
		c.FootprintRef = make(map[string]any, len(footprint))
		for key, value := range footprint {
			c.FootprintRef[key] = value
		}
	}
	c.Placement = readPlacement(primary)
	if len(records) > 1 {
		for _, x := range records {
			c.SubParts = append(c.SubParts, SubPart{
				PrimitiveID: stringField(x, "primitiveId"),
				SubPartName: stringField(x, "subPartName"),
				UniqueID:    stringField(x, "uniqueId"),
				Placement:   readPlacement(x),
			})
		}
	}
	seen := map[string]bool{}
	for _, x := range records {
		ps, ok := x["pins"].([]any)
		if !ok || len(ps) == 0 {
			return c, connections, fmt.Errorf("%s: pin inventory unavailable; read each active page separately", ref)
		}
		for _, v := range ps {
			p, ok := v.(map[string]any)
			if !ok {
				return c, connections, fmt.Errorf("%s: invalid pin", ref)
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
				return c, connections, fmt.Errorf("%s.%s: missing net evidence", ref, n)
			}
			nc, ncKnown := p["noConnected"].(bool)
			pin := Pin{Number: n, Name: name, NoConnected: nc}
			if net, known := p["net"].(string); known && net == "" && ncKnown && !nc {
				pin.ConnectionState = "unconnected"
			}
			pin.X, _ = numberValue(p["x"])
			pin.Y, _ = numberValue(p["y"])
			if seen[n] {
				return c, connections, fmt.Errorf("%s: duplicate pin number %s across the device instance", ref, n)
			}
			seen[n] = true
			c.Pins = append(c.Pins, pin)
			if p["net"] != nil {
				net, ok := p["net"].(string)
				if !ok {
					return c, connections, fmt.Errorf("invalid net value")
				}
				if net != "" {
					id := fmt.Sprintf("net-%x", sha256.Sum256([]byte(net)))
					nets[net] = id
					connections = append(connections, Connection{ComponentID: c.ID, PinNumber: n, NetID: id, Kind: "netlist"})
				}
			}
		}
	}
	sort.Slice(c.Pins, func(i, j int) bool { return c.Pins[i].Number < c.Pins[j].Number })
	return c, connections, nil
}

// readPlacement reads the measured placement of one placed record. A
// multi-unit device places each unit separately, so the device-level
// placement is the first placed unit and every unit keeps its own geometry
// in the component sub-parts.
func readPlacement(record map[string]any) *Placement {
	var placement *Placement
	if xv, xok := numberValue(record["x"]); xok {
		if yv, yok := numberValue(record["y"]); yok {
			placement = &Placement{X: xv, Y: yv}
			if rv, rok := numberValue(record["rotation"]); rok {
				placement.Rotation = rv
			}
			if mv, mok := record["mirror"].(bool); mok {
				placement.Mirror = mv
			}
		}
	}
	if bb, ok := record["bbox"].(map[string]any); ok {
		if minx, a := numberValue(bb["minX"]); a {
			if miny, b := numberValue(bb["minY"]); b {
				if maxx, c1 := numberValue(bb["maxX"]); c1 {
					if maxy, d := numberValue(bb["maxY"]); d {
						if placement == nil {
							placement = &Placement{}
						}
						placement.BBox = &BBox{MinX: minx, MinY: miny, MaxX: maxx, MaxY: maxy}
					}
				}
			}
		}
	}
	return placement
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
