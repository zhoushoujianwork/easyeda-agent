package app

import (
	"fmt"
	"reflect"
)

// libNamingFrontier asks the same marker constructors used by terminal naming
// whether this physical island has at least one legal lead in the supplied
// geometry. Callers decide whether a failed probe is a necessary placement
// constraint or only a provisional routing conflict. A spent probe budget is
// inconclusive and must never be used to prune a placement or route.
func libNamingFrontier(p *powerLayoutPlan, island libIsland, policy string, budget *int, blockers ...map[string]bool) (reachable, complete bool) {
	witness, complete := libNamingFrontierWitness(p, island, policy, budget, blockers...)
	return witness != nil, complete
}

func libNamingFrontierWitness(p *powerLayoutPlan, island libIsland, policy string, budget *int, blockers ...map[string]bool) (*powerLayoutPlan, bool) {
	if p == nil || len(island.pins) == 0 {
		return nil, false
	}
	if budget == nil {
		local := 1_000_000
		budget = &local
	}
	if *budget <= 0 {
		return nil, false
	}
	trial := *p
	trial.Placements = append([]powerLayoutPlacement(nil), p.Placements...)
	trial.Wires = clonePowerLayoutWires(p.Wires)
	trial.Flags = append([]powerLayoutFlag(nil), p.Flags...)
	trial.namingBlockers = nil
	if len(blockers) > 0 {
		trial.namingBlockers = blockers[0]
	}
	kind := "net_port_bi"
	switch policy {
	case "local_ground":
		kind = "ground"
	case "local_power":
		kind = "power"
	}
	if libPlaceMidpointMarker(&trial, island, kind, budget) || libPlaceWireTreeMarker(&trial, island, kind, budget) {
		return &trial, true
	}
	for _, pin := range island.pins {
		if libPlaceMarker(&trial, pin, kind, budget) {
			return &trial, true
		}
		if *budget <= 0 {
			return nil, false
		}
	}
	return nil, true
}

type libNamingPlacementWitnesses map[string]*powerLayoutPlan

// A cached marker must pass the same terminal construction checks after a
// placement or wire route changes. The witness is a complete before-plan plus
// its naming wires/flag; only that naming suffix is projected onto after.
// validateLibGeometry alone does not cover predicted marker body/text and
// lead-to-pin clearance, so replay each marker through schTerminalCandidate.
func libNamingWitnessStillLegal(before, after, witness *powerLayoutPlan) bool {
	if before == nil || after == nil || witness == nil || len(witness.Wires) < len(before.Wires) || len(witness.Flags) < len(before.Flags) {
		return false
	}
	for i, wire := range before.Wires {
		if !reflect.DeepEqual(wire, witness.Wires[i]) {
			return false
		}
	}
	for i, flag := range before.Flags {
		if !reflect.DeepEqual(flag, witness.Flags[i]) {
			return false
		}
	}
	trial := *after
	trial.Wires = append(clonePowerLayoutWires(after.Wires), witness.Wires[len(before.Wires):]...)
	trial.Flags = append([]powerLayoutFlag(nil), after.Flags...)
	if validateLibGeometry(&trial) != nil {
		return false
	}
	for _, marker := range witness.Flags[len(before.Flags):] {
		segments, err := schTerminalSegments(&trial)
		if err != nil || libMarkerRetraces(marker, segments) || schTerminalCandidate(&trial, marker, segments) != nil {
			return false
		}
		trial.Flags = append(trial.Flags, marker)
	}
	return validateLibGeometry(&trial) == nil
}

// A singleton module_port cannot gain another same-net wire island inside the
// zone. If the newly placed component alone removes its final marker lead,
// descendants and terminal route ordering cannot repair that geometry.
func libValidateSingletonNamingPlacement(before, after *powerLayoutPlan, placed powerLayoutPlacement, policies map[string]string, routing *schematicRoutingContext, budget *int, witnesses ...libNamingPlacementWitnesses) error {
	if before == nil || after == nil || routing == nil {
		return nil
	}
	geometryAfter := *after
	geometryAfter.Wires, geometryAfter.Flags = nil, nil
	geometryBefore := *before
	geometryBefore.Wires, geometryBefore.Flags = nil, nil
	var cache libNamingPlacementWitnesses
	if len(witnesses) > 0 {
		cache = witnesses[0]
	}
	for _, component := range geometryAfter.Placements {
		for _, pin := range component.Pins {
			if pin.Net == "" || policies[pin.Net] != "module_port" || routing.netPins[pin.Net] != 1 {
				continue
			}
			island := libIsland{net: pin.Net, pins: []powerLayoutPin{pin}}
			// A naming lead may extend far beyond its pin before it finds a
			// clear grid point. A body outside a fixed local radius can still
			// close that lead, so validate the cached witness against every new
			// placement instead of using a distance-only skip.
			if component.Designator != placed.Designator {
				witness, known := cache[pin.Net]
				if !known {
					var complete bool
					witness, complete = libNamingFrontierWitness(&geometryBefore, island, "module_port", budget)
					if !complete {
						return errLibLayoutBudget
					}
					if cache != nil {
						cache[pin.Net] = witness
					}
				}
				if witness == nil {
					continue // Pre-existing closure is not caused by this component.
				}
				if libNamingWitnessStillLegal(&geometryBefore, &geometryAfter, witness) {
					continue // Reuse a complete, previously validated naming witness.
				}
			}
			if ok, complete := libNamingFrontier(&geometryAfter, island, "module_port", budget); ok {
				continue
			} else if !complete {
				return errLibLayoutBudget
			}
			return &schGeometryObstruction{kind: "marker-frontier-sealed", owners: []string{placed.Designator}, blockers: []string{placed.Designator}, blockersExplicit: true,
				nets: []string{pin.Net}, complete: true, cause: fmt.Errorf("placing %s closes the only legal %s marker lead", placed.Designator, pin.Net)}
		}
	}
	return nil
}
