package app

import (
	"encoding/json"
	"fmt"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// Authored ownership survives JSON queue serialization and is checked against
// actual geometry at the complete-design checkpoint, never the placement prefix.
type schematicOwnershipExpectation struct {
	ComponentIDs   map[string]string     `json:"componentIds"`
	Modules        []connectivity.Module `json:"modules"`
	NetRoles       map[string]string     `json:"netRoles"`
	ConnectionMode string                `json:"connectionMode,omitempty"`
}

func (e *schematicStateExpectation) validateOwnership() error {
	// Complete nonempty drawing assertions cannot silently lose the generated
	// semantic guard. Empty/unwired baselines and intermediate pin guards remain
	// legal so a rebuild can fix an invalid source rather than deadlock on it.
	completeDrawing := e.ExactParts && len(e.Parts) > 0 && e.Drawing != nil && (len(e.Drawing.Wires) > 0 || len(e.Drawing.Flags) > 0)
	if completeDrawing && e.Ownership == nil {
		return fmt.Errorf("peripheral-ownership-incomplete: complete drawing assertion requires ownership")
	}
	if e.Ownership == nil {
		return nil
	}
	if !e.ExactParts || e.Drawing == nil || len(e.Parts) == 0 || len(e.Ownership.ComponentIDs) != len(e.Parts) {
		return fmt.Errorf("peripheral-ownership-incomplete: ownership requires a complete exact-parts drawing assertion")
	}
	layout := &SchematicLayoutResult{ComponentIDs: e.Ownership.ComponentIDs, Wires: e.Drawing.Wires, Flags: e.Drawing.Flags}
	for _, ref := range sortedStateKeys(e.Parts) {
		part := SchematicPlacement{Designator: ref}
		for _, number := range sortedStateKeys(e.Parts[ref].Pins) {
			pin := e.Parts[ref].Pins[number]
			if pin.X == nil || pin.Y == nil || pin.Net == nil || pin.NC == nil {
				return fmt.Errorf("peripheral-direct-incomplete: %s.%s needs exhaustive geometry/net/NC evidence", ref, number)
			}
			if *pin.Net != "" && e.Ownership.NetRoles[*pin.Net] != "power" && e.Ownership.NetRoles[*pin.Net] != "ground" && e.Ownership.NetRoles[*pin.Net] != "signal" {
				return fmt.Errorf("peripheral-ownership-incomplete: net %s needs preserved power/ground/signal classification", *pin.Net)
			}
			part.Pins = append(part.Pins, SchematicPin{Number: number, X: *pin.X, Y: *pin.Y, Net: *pin.Net})
		}
		layout.Placements = append(layout.Placements, part)
	}
	switch e.Ownership.ConnectionMode {
	case "", "direct":
		return ValidateSchematicPeripheralDirect(layout, e.Ownership.Modules, e.Ownership.NetRoles)
	case "net-labels":
		if err := validateSchCompositionNets(&powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}); err != nil {
			return fmt.Errorf("peripheral-label-incomplete: %w", err)
		}
		return validateSchematicLabelledOwnership(layout, e.Ownership.Modules)
	default:
		return fmt.Errorf("connectionMode must be direct or net-labels")
	}
}

func (e *schematicOwnershipExpectation) check(result any) error {
	if e.ConnectionMode != "" && e.ConnectionMode != "direct" && e.ConnectionMode != "net-labels" {
		return fmt.Errorf("connectionMode must be direct or net-labels")
	}
	layout, err := schematicObservedOwnershipLayout(result, e.ComponentIDs, e.ConnectionMode == "net-labels")
	if err != nil {
		return fmt.Errorf("peripheral-direct-incomplete: %w", err)
	}
	if e.ConnectionMode == "net-labels" {
		return validateSchematicLabelledOwnership(layout, e.Modules)
	}
	return ValidateSchematicPeripheralDirect(layout, e.Modules, e.NetRoles)
}

// Reconstruct wire roots from the fresh response. Target flags never manufacture
// observed marker leads; only actual returned line segments participate. Wire
// names are resolved from fresh touching pin/marker evidence, not from the plan.
func schematicObservedOwnershipLayout(result any, ids map[string]string, requireNamedMarkers ...bool) (*SchematicLayoutResult, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var live struct {
		Wires *[]struct {
			X0, Y0, X1, Y1 *float64
			Net            string
		} `json:"wires"`
		Components []struct {
			Kind          string `json:"componentType"`
			Ref           string `json:"designator"`
			PinsAvailable *bool  `json:"pinsAvailable"`
			NetAmbiguous  bool   `json:"netAmbiguous"`
			Net           string `json:"net"`
			X, Y          *float64
			Pins          []struct {
				Number string `json:"pinNumber"`
				Net    *string
				NC     *bool `json:"noConnected"`
				X, Y   *float64
			} `json:"pins"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &live); err != nil {
		return nil, err
	}
	if live.Wires == nil {
		return nil, fmt.Errorf("fresh wire geometry is unavailable")
	}
	layout := &SchematicLayoutResult{ComponentIDs: ids}
	for _, w := range *live.Wires {
		if w.X0 == nil || w.Y0 == nil || w.X1 == nil || w.Y1 == nil {
			return nil, fmt.Errorf("fresh wire has missing coordinates")
		}
		layout.Wires = append(layout.Wires, SchematicWire{Net: w.Net, Points: [][2]float64{{*w.X0, *w.Y0}, {*w.X1, *w.Y1}}})
	}
	if _, err := drawingEdges(layout.Wires); err != nil {
		return nil, err
	}
	parent := make([]int, len(layout.Wires))
	for i := range parent {
		parent[i] = i
	}
	var root func(int) int
	root = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i, a := range layout.Wires {
		for j, b := range layout.Wires[:i] {
			if plSegmentsContact(a.Points[0], a.Points[1], b.Points[0], b.Points[1]) {
				parent[root(i)] = root(j)
			}
		}
	}
	names := map[int]string{}
	markerNames := map[int]string{}
	requireMarkers := len(requireNamedMarkers) > 0 && requireNamedMarkers[0]
	nameRoot := func(index int, net string) error {
		if net == "" {
			return nil
		}
		r := root(index)
		if names[r] != "" && names[r] != net {
			return fmt.Errorf("fresh physical wire tree shorts nets %s/%s", names[r], net)
		}
		names[r] = net
		return nil
	}
	touch := func(x, y float64, net string) error {
		for i, w := range layout.Wires {
			if plOnSegment([2]float64{x, y}, w.Points[0], w.Points[1]) {
				if err := nameRoot(i, net); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, c := range live.Components {
		if c.Kind != "netport" && c.Kind != "netflag" {
			continue
		}
		if c.X == nil || c.Y == nil || c.Net == "" {
			return nil, fmt.Errorf("fresh marker geometry/net is incomplete")
		}
		marked := false
		for i, w := range layout.Wires {
			if plOnSegment([2]float64{*c.X, *c.Y}, w.Points[0], w.Points[1]) {
				r := root(i)
				if markerNames[r] != "" && markerNames[r] != c.Net {
					return nil, fmt.Errorf("fresh marker tree shorts nets %s/%s", markerNames[r], c.Net)
				}
				markerNames[r] = c.Net
				marked = true
			}
		}
		if requireMarkers && !marked {
			return nil, fmt.Errorf("fresh marker %s has no real lead", c.Net)
		}
		if err := touch(*c.X, *c.Y, c.Net); err != nil {
			return nil, err
		}
	}
	for _, c := range live.Components {
		if c.Kind != "part" {
			continue
		}
		if c.Ref == "" || ids[c.Ref] == "" || c.NetAmbiguous || len(c.Pins) == 0 || (c.PinsAvailable != nil && !*c.PinsAvailable) {
			return nil, fmt.Errorf("fresh part %s has incomplete identity/pins", c.Ref)
		}
		part := SchematicPlacement{Designator: c.Ref}
		for _, q := range c.Pins {
			if q.X == nil || q.Y == nil || q.Net == nil || q.NC == nil || q.Number == "" || (*q.NC && *q.Net != "") || !plGrid(*q.X) || !plGrid(*q.Y) {
				return nil, fmt.Errorf("fresh pin %s.%s has incomplete geometry/net/NC", c.Ref, q.Number)
			}
			part.Pins = append(part.Pins, SchematicPin{Number: q.Number, Net: *q.Net, X: *q.X, Y: *q.Y})
			if requireMarkers && *q.Net != "" {
				named := false
				for i, w := range layout.Wires {
					if plOnSegment([2]float64{*q.X, *q.Y}, w.Points[0], w.Points[1]) && markerNames[root(i)] == *q.Net {
						named = true
					}
				}
				if !named {
					return nil, fmt.Errorf("fresh pin %s.%s does not reach a same-name marker through a real lead", c.Ref, q.Number)
				}
			}
			if err := touch(*q.X, *q.Y, *q.Net); err != nil {
				return nil, err
			}
		}
		layout.Placements = append(layout.Placements, part)
	}
	for i := range layout.Wires {
		if err := nameRoot(i, layout.Wires[i].Net); err != nil {
			return nil, err
		}
	}
	for i := range layout.Wires {
		if names[root(i)] == "" {
			return nil, fmt.Errorf("fresh wire tree has no net evidence")
		}
		layout.Wires[i].Net = names[root(i)]
	}
	return layout, nil
}
