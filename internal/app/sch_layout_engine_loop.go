package app

import (
	"errors"
)

var errLibLayoutBudget = errors.New("candidate search budget exhausted")

func solveSchematicLayout(input SchematicLayoutInput, measured map[string]powerLayoutPlacement, members []string, hints map[string]SchematicLayoutPeripheral, budget *int) (*SchematicLayoutResult, error) {
	core := measured[input.CoreComponentID]
	core = plTranslate(core, -core.X, -core.Y)
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{core}}
	pending := []string{}
	for _, id := range members {
		if id != input.CoreComponentID {
			pending = append(pending, id)
		}
	}
	s := newSchematicRepairSearch(input, measured, members, hints, budget)
	return s.solve(p, pending)
}

// The final wiring/naming gate is also inside the checkpoint search: a legal
// placement prefix is not sufficient if its finished routes trap a later net.
func libFinishSchematicLayout(p powerLayoutPlan, netPolicies map[string]string, budget *int) (*powerLayoutPlan, error) {
	var err error
	p.Flags = nil
	if err = libJoinNearbyRails(&p, netPolicies); err != nil {
		return nil, err
	}
	beforePortJoins := p
	err = libJoinDirectNets(&p, netPolicies)
	if err == nil {
		err = libNameIslands(&p, netPolicies, budget)
	}
	if err != nil {
		if errors.Is(err, errLibLayoutBudget) {
			return nil, err
		}
		// Joining interleaved cross-zone pins may trap another net's naming
		// corridor. Retry without optional port joins, retaining mandatory
		// peripheral routes and all direct-net joins.
		p = beforePortJoins
		if err = libJoinNetsMode(&p, netPolicies, false, false); err != nil {
			return nil, err
		}
		if err = libNameIslands(&p, netPolicies, budget); err != nil {
			return nil, err
		}
	}
	libCompactMarkerEnvelope(&p, budget)
	if err = validateLibGeometry(&p); err != nil {
		return nil, err
	}
	if err = validateSchCompositionNets(&p); err != nil {
		return nil, err
	}

	return &p, nil
}
