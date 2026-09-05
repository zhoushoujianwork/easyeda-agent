package connectivity

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// FromRead converts the active-page semantic read. It rejects shallow data.
func FromRead(m map[string]any) (Document, error) {
	d := Document{SchemaVersion: "1.4", Components: []Component{}, Nets: []Net{}, Connections: []Connection{}}
	cs, ok := m["components"].([]any)
	if !ok {
		return d, fmt.Errorf("missing components inventory")
	}
	nets := map[string]string{}
	for _, v := range cs {
		x, ok := v.(map[string]any)
		if !ok {
			return d, fmt.Errorf("invalid component")
		}
		if x["componentType"] != "part" {
			continue
		}
		ref, _ := x["designator"].(string)
		c := Component{ID: "cmp-" + ref, Ref: ref}
		if xv, xok := numberValue(x["x"]); xok {
			if yv, yok := numberValue(x["y"]); yok {
				c.Placement = &Placement{X: xv, Y: yv}
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
			return d, fmt.Errorf("%s: pin inventory unavailable; read each active page separately", ref)
		}
		for _, v := range ps {
			p, ok := v.(map[string]any)
			if !ok {
				return d, fmt.Errorf("%s: invalid pin", ref)
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
				return d, fmt.Errorf("%s.%s: missing net evidence", ref, n)
			}
			nc, _ := p["noConnected"].(bool)
			c.Pins = append(c.Pins, Pin{Number: n, Name: name, NoConnected: nc})
			if p["net"] != nil {
				net, ok := p["net"].(string)
				if !ok {
					return d, fmt.Errorf("invalid net value")
				}
				if net != "" {
					id := fmt.Sprintf("net-%x", sha256.Sum256([]byte(net)))
					nets[net] = id
					d.Connections = append(d.Connections, Connection{ComponentID: c.ID, PinNumber: n, NetID: id, Kind: "netlist"})
				}
			}
		}
		sort.Slice(c.Pins, func(i, j int) bool { return c.Pins[i].Number < c.Pins[j].Number })
		d.Components = append(d.Components, c)
	}
	for name, id := range nets {
		d.Nets = append(d.Nets, Net{ID: id, Name: name})
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
