package app

import "fmt"

// This is an offline geometry/naming gate, not a library-identity or live DRC gate.
// A status word alone must never turn an empty-wire placeholder into a deliverable.
func validateCompleteLayoutPreview(in SchematicRenderInput) error {
	if err := validateSchematicZoneVariants(in); err != nil {
		return err
	}
	if len(in.Zones) == 0 {
		return fmt.Errorf("complete preview requires zones")
	}
	seenIDs := map[string]bool{}
	for _, z := range in.Zones {
		if z.Status == "blocked" || z.Layout == nil || len(z.Layout.Placements) == 0 {
			return fmt.Errorf("zone %s incomplete; solve the full layout first (diagnostics require --diagnostic)", z.ID)
		}
		p := powerLayoutPlan{Placements: z.Layout.Placements, Wires: z.Layout.Wires, Flags: z.Layout.Flags}
		for _, c := range p.Placements {
			id := z.Layout.ComponentIDs[c.Designator]
			if id == "" || seenIDs[id] {
				return fmt.Errorf("zone %s missing/duplicate component identity for %s", z.ID, c.Designator)
			}
			seenIDs[id] = true
			for _, q := range c.Pins {
				state := z.Layout.PinStates[id][q.Number]
				if q.Net == "" && state != "nc" && state != "unconnected" {
					return fmt.Errorf("%s.%s lacks explicit pin state", c.Designator, q.Number)
				}
				if q.Net != "" && state != "" {
					return fmt.Errorf("%s.%s conflicting pin state", c.Designator, q.Number)
				}
			}
		}
		if err := validateLibGeometry(&p); err != nil {
			return fmt.Errorf("zone %s geometry: %w", z.ID, err)
		}
		if err := validateSchCompositionNets(&p); err != nil {
			return fmt.Errorf("zone %s incomplete connectivity: %w", z.ID, err)
		}
	}
	return nil
}
