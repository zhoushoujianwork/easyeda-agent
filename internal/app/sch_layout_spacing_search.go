package app

import "math"

type SchematicSpacingTrial struct {
	Factor         int    `json:"factor"`
	CandidateQuota int    `json:"candidateQuota"`
	CandidatesUsed int    `json:"candidatesUsed"`
	ExpandedNodes  int    `json:"expandedNodes"`
	Result         string `json:"result"`
}

// Dense attachment shells optimize wire length before terminal routing and
// marker geometry are known. Once the compact fast path fails, propose one
// wider checkpoint before spending the whole allowance on the same geometry.
// Only symbol translations change: the core, rotations, measured shapes,
// physical pin identities, net policies and ownership remain intact. Every
// provisional wire/marker is withdrawn and regenerated through the same gates.
func (s *schematicRepairSearch) tryCheckpointSpacing(p powerLayoutPlan) (*powerLayoutPlan, bool) {
	if *s.budget <= 0 {
		return nil, false
	}
	*s.budget-- // One whole-checkpoint pose candidate; downstream probes pay too.
	attempt := SchematicSpacingTrial{Factor: 3, CandidatesUsed: 1, Result: "geometry-rejected"}
	expandedBefore := 0
	if s.routing != nil {
		expandedBefore = s.routing.expanded
	}
	defer func() {
		if s.routing != nil {
			attempt.ExpandedNodes = s.routing.expanded - expandedBefore
		}
		s.diagnostics.SpacingTrials = append(s.diagnostics.SpacingTrials, attempt)
	}()
	trial := p
	trial.Placements = append([]powerLayoutPlacement(nil), p.Placements...)
	trial.Wires, trial.Flags = nil, nil
	core := p.Placements[0]
	for i, c := range trial.Placements {
		// Triple the center spacing on the existing five-raw search grid. This is
		// a bounded heuristic proposal, not an inferred constraint or impossibility
		// claim about the compact checkpoint. The original remains a fallback.
		dx := math.Round(2*(c.X-core.X)/5) * 5
		dy := math.Round(2*(c.Y-core.Y)/5) * 5
		trial.Placements[i] = plTranslate(c, dx, dy)
	}
	if validateLibGeometry(&trial) != nil {
		return nil, false
	}
	quota := 4096
	if quota > *s.budget {
		quota = *s.budget
	}
	before := quota
	if s.routing != nil {
		s.routing.relocation++
	}
	finished, err := libFinishSchematicLayoutRegenerate(trial, s.input.NetPolicies, &quota, s.routing)
	if s.routing != nil {
		s.routing.relocation--
	}
	*s.budget -= before - quota
	attempt.CandidateQuota = before
	attempt.CandidatesUsed += before - quota
	attempt.Result = "accepted"
	if err == nil {
		layout := &SchematicLayoutResult{Placements: finished.Placements, Wires: finished.Wires, Flags: finished.Flags, ComponentIDs: map[string]string{}}
		for id, c := range s.measured {
			layout.ComponentIDs[c.Designator] = id
		}
		roles := map[string]string{}
		for net, policy := range s.input.NetPolicies {
			roles[net] = schematicPeripheralNetRole(net)
			if policy == "local_power" {
				roles[net] = "power"
			}
			if policy == "local_ground" {
				roles[net] = "ground"
			}
		}
		err = validateSchematicLayoutPeripheralDirect(layout, s.input.CoreComponentID, roles)
		if err == nil {
			err = validateMinimumAttachmentDistances(layout, s.measured, s.hints)
		}
	}
	if err != nil {
		attempt.Result = err.Error()
	}
	return finished, err == nil
}
