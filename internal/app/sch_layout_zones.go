package app

import (
	"fmt"
	"math"
	"strings"
)

// Explicit ownership avoids inventing functional relationships from shared rails.
type SchematicZone struct {
	ID              string                  `json:"id"`
	Title           string                  `json:"title"`
	CoreComponentID string                  `json:"coreComponentId"`
	ComponentIDs    []string                `json:"componentIds"`
	Placement       *SchematicZonePlacement `json:"placement,omitempty"`
}
type SchematicZonesInput struct {
	SchemaVersion int                         `json:"schemaVersion"`
	Spacing       *float64                    `json:"spacing,omitempty"`
	Components    []SchematicLayoutComponent  `json:"components"`
	NetPolicies   map[string]string           `json:"netPolicies"`
	Attachments   []SchematicLayoutPeripheral `json:"attachments,omitempty"`
	MaxCandidates int                         `json:"maxCandidates,omitempty"`
	Zones         []SchematicZone             `json:"zones"`
}
type SchematicZoneResult struct {
	ID              string                  `json:"id"`
	Title           string                  `json:"title"`
	CoreComponentID string                  `json:"coreComponentId"`
	ContentBounds   SchematicBox            `json:"contentBounds"`
	Frame           schFrameSpec            `json:"frame"`
	Layout          *SchematicLayoutResult  `json:"layout"`
	Placement       *SchematicZonePlacement `json:"placement,omitempty"`
}
type SchematicZonesResult struct {
	SchemaVersion  int                   `json:"schemaVersion"`
	Spacing        *float64              `json:"spacing,omitempty"`
	Zones          []SchematicZoneResult `json:"zones"`
	CandidatesUsed int                   `json:"candidatesUsed"`
}

// PlanSchematicZones computes independent, core-normalized zones, not sheet
// positions or rendered frames. No partial result escapes on any zone failure.
func PlanSchematicZones(in SchematicZonesInput) (*SchematicZonesResult, error) {
	if in.SchemaVersion != 1 || len(in.Zones) == 0 || len(in.Components) == 0 {
		return nil, fmt.Errorf("schemaVersion:1, zones and components required")
	}
	if err := validateSchematicSpacing(in.Spacing); err != nil {
		return nil, err
	}
	budget := in.MaxCandidates
	if budget == 0 {
		budget = 20000
	}
	if budget < 1 || budget > 1000000 {
		return nil, fmt.Errorf("maxCandidates must be 1..1000000")
	}
	initial := budget
	components := map[string]SchematicLayoutComponent{}
	refs := map[string]bool{}
	for _, c := range in.Components {
		if strings.TrimSpace(c.ID) == "" || components[c.ID].ID != "" || refs[c.Measurement.Designator] {
			return nil, fmt.Errorf("duplicate/empty component or designator %s", c.ID)
		}
		components[c.ID] = c
		refs[c.Measurement.Designator] = true
	}
	owners, zoneIDs := map[string]string{}, map[string]bool{}
	ids, placements := []string{}, []*SchematicZonePlacement{}
	for _, z := range in.Zones {
		ids, placements = append(ids, z.ID), append(placements, z.Placement)
		if strings.TrimSpace(z.ID) == "" || strings.TrimSpace(z.Title) == "" || zoneIDs[z.ID] {
			return nil, fmt.Errorf("duplicate/empty zone ID or title %s", z.ID)
		}
		zoneIDs[z.ID] = true
		for _, id := range z.ComponentIDs {
			if components[id].ID == "" || owners[id] != "" {
				return nil, fmt.Errorf("zone %s: unknown or multiply owned component %s", z.ID, id)
			}
			owners[id] = z.ID
		}
		if owners[z.CoreComponentID] != z.ID {
			return nil, fmt.Errorf("zone %s: core must be a member", z.ID)
		}
	}
	if err := validateSchematicZonePlacements(ids, placements); err != nil {
		return nil, err
	}
	netOwners := map[string]map[string]bool{}
	for _, c := range in.Components {
		if owners[c.ID] == "" {
			return nil, fmt.Errorf("component %s needs explicit zone ownership", c.ID)
		}
		for _, p := range c.Measurement.Pins {
			if p.Net == "" {
				continue
			}
			if netOwners[p.Net] == nil {
				netOwners[p.Net] = map[string]bool{}
			}
			netOwners[p.Net][owners[c.ID]] = true
		}
	}
	for net, zones := range netOwners {
		switch in.NetPolicies[net] {
		case "local_ground", "local_power", "module_port":
		case "direct":
			if len(zones) > 1 {
				return nil, fmt.Errorf("cross-zone net %s requires module_port policy", net)
			}
		default:
			return nil, fmt.Errorf("net %s needs explicit policy", net)
		}
	}
	for net := range in.NetPolicies {
		if netOwners[net] == nil {
			return nil, fmt.Errorf("unused policy %s", net)
		}
	}
	for _, h := range in.Attachments {
		if owners[h.ComponentID] == "" || (h.AttachTo != nil && owners[h.AttachTo.ComponentID] != owners[h.ComponentID]) {
			return nil, fmt.Errorf("unknown/cross-zone attachment %s", h.ComponentID)
		}
	}
	out := &SchematicZonesResult{SchemaVersion: 1}
	if in.Spacing != nil {
		spacing := *in.Spacing
		out.Spacing = &spacing
	}
	for _, z := range in.Zones {
		local := SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: z.CoreComponentID, NetPolicies: map[string]string{}}
		for _, id := range z.ComponentIDs {
			c := components[id]
			local.Components = append(local.Components, c)
			for _, p := range c.Measurement.Pins {
				if p.Net != "" {
					local.NetPolicies[p.Net] = in.NetPolicies[p.Net]
				}
			}
		}
		for _, h := range in.Attachments {
			if owners[h.ComponentID] == z.ID {
				local.Attachments = append(local.Attachments, h)
			}
		}
		zoneBudget := &budget
		if in.Spacing != nil {
			// Unified two-level mode is isolated: a harder earlier zone cannot
			// consume a later zone's search/compaction allowance.
			isolatedBudget := initial
			zoneBudget = &isolatedBudget
		}
		before := *zoneBudget
		layout, err := planSchematicLayoutWithBudget(local, zoneBudget)
		if err != nil {
			return nil, fmt.Errorf("zone %s (%s): %w", z.ID, z.Title, err)
		}
		out.CandidatesUsed += before - *zoneBudget
		p := powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}
		boxes := powerLayoutContentObstacles(&p)
		if len(boxes) == 0 {
			return nil, fmt.Errorf("zone %s has no content geometry", z.ID)
		}
		b := boxes[0]
		for _, a := range boxes[1:] {
			b.MinX = math.Min(b.MinX, a.MinX)
			b.MinY = math.Min(b.MinY, a.MinY)
			b.MaxX = math.Max(b.MaxX, a.MaxX)
			b.MaxY = math.Max(b.MaxY, a.MaxY)
		}
		frame, err := measureSchModuleFrameObstaclesSpacing(z.ID, z.Title, boxes, nil, nil, in.Spacing)
		if err != nil {
			return nil, fmt.Errorf("zone %s frame: %w", z.ID, err)
		}
		out.Zones = append(out.Zones, SchematicZoneResult{ID: z.ID, Title: z.Title, CoreComponentID: z.CoreComponentID, ContentBounds: b, Frame: frame, Layout: layout, Placement: copySchematicZonePlacement(z.Placement)})
	}
	return out, nil
}
