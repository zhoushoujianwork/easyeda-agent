package app

import (
	"errors"
	"fmt"
	"sort"
)

var errLibLayoutBudget = errors.New("candidate search budget exhausted")

func solveSchematicLayout(input SchematicLayoutInput, measured map[string]powerLayoutPlacement, members []string, hints map[string]SchematicLayoutPeripheral, budget *int) (*SchematicLayoutResult, error) {
	netPolicies := input.NetPolicies
	var err error
	core := measured[input.CoreComponentID]
	core = plTranslate(core, -core.X, -core.Y)
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{core}}
	placed := map[string]powerLayoutPlacement{input.CoreComponentID: core}
	pending := []string{}
	for _, id := range members {
		if id != input.CoreComponentID {
			pending = append(pending, id)
		}
	}
	for len(pending) > 0 {
		sort.SliceStable(pending, func(i, j int) bool {
			return libPeripheralPriority(measured[pending[i]], netPolicies) < libPeripheralPriority(measured[pending[j]], netPolicies)
		})
		progress := false
		var lastErr error
		for i, id := range pending {
			pairs, e := libAttachmentPairs(id, measured[id], hints[id], placed, members, netPolicies)
			if e != nil {
				return nil, fmt.Errorf("%s: %w", id, e)
			}
			if len(pairs) == 0 {
				continue
			}
			var next *powerLayoutPlan
			for _, pair := range pairs {
				next, lastErr = libPlacePeripheral(p, measured[id], pair, netPolicies, budget)
				if errors.Is(lastErr, errLibLayoutBudget) {
					return nil, fmt.Errorf("component %s: %w; revise constraints or explicitly increase maxCandidates", id, lastErr)
				}
				if next != nil {
					break
				}
			}
			if next == nil {
				continue
			}
			p = *next
			placed[id] = p.Placements[len(p.Placements)-1]
			pending = append(pending[:i], pending[i+1:]...)
			progress = true
			break
		}
		if !progress {
			return nil, fmt.Errorf("cannot place %v in 400 raw outward / 200 raw lateral search (disconnected/cyclic attachment or collision): %v", pending, lastErr)
		}
	}
	p.Flags = nil
	if err = libJoinNearbyRails(&p, netPolicies); err != nil {
		return nil, err
	}
	if err = libJoinDirectNets(&p, netPolicies); err != nil {
		return nil, err
	}
	if err = libNameIslands(&p, netPolicies); err != nil {
		return nil, err
	}
	libCompactMarkerEnvelope(&p, budget)
	if err = validateLibGeometry(&p); err != nil {
		return nil, err
	}

	return &SchematicLayoutResult{Placements: p.Placements, Wires: p.Wires, Flags: p.Flags, Score: libCandidateScore(&p)}, nil
}
