package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SchematicZonePlacement is a page-layout constraint, not ownership or PCB
// placement. Same-page edges are transitive; cycles express the same constraint.
type SchematicZonePlacement struct {
	SamePageAs     string `json:"samePageAs"`
	PreferAdjacent bool   `json:"preferAdjacent,omitempty"`
}

func copySchematicZonePlacement(p *SchematicZonePlacement) *SchematicZonePlacement {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

func validateSchematicZonePlacements(ids []string, placements []*SchematicZonePlacement) error {
	if len(ids) != len(placements) {
		return fmt.Errorf("zone placement identity count mismatch")
	}
	known := map[string]bool{}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" || known[id] {
			return fmt.Errorf("zone placement requires unique nonempty zone IDs")
		}
		known[id] = true
	}
	for i, p := range placements {
		if p == nil {
			continue
		}
		if strings.TrimSpace(p.SamePageAs) == "" || !known[p.SamePageAs] || p.SamePageAs == ids[i] {
			return fmt.Errorf("zone %s placement.samePageAs must reference another zone in this input", ids[i])
		}
	}
	return nil
}

func validateSchematicRenderPlacements(zones []SchematicRenderZone) error {
	ids := make([]string, len(zones))
	placements := make([]*SchematicZonePlacement, len(zones))
	for i, z := range zones {
		ids[i], placements[i] = z.ID, z.Placement
	}
	return validateSchematicZonePlacements(ids, placements)
}

// Raw evidence prevents JSON null from silently becoming an omitted constraint
// or false preference. DecodeStrictDesignJSON separately rejects unknown fields.
func validateSchematicZonePlacementsJSON(raw []byte) error {
	var top struct {
		Zones []map[string]json.RawMessage `json:"zones"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	for _, z := range top.Zones {
		b, exists := z["placement"]
		if !exists {
			continue
		}
		var p map[string]json.RawMessage
		if string(b) == "null" || json.Unmarshal(b, &p) != nil || len(p["samePageAs"]) == 0 || string(p["samePageAs"]) == "null" {
			return fmt.Errorf("placement requires an object and explicit samePageAs")
		}
		if b, exists := p["preferAdjacent"]; exists && string(b) == "null" {
			return fmt.Errorf("placement.preferAdjacent must be boolean, not null")
		}
	}
	return nil
}
