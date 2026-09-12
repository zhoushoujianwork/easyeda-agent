package app

import (
	"errors"
	"fmt"
	"sort"
)

// SchematicLayoutSearchDiagnostics describes bounded search, not a proof that
// no placement exists outside the explored coordinates/branches.
type SchematicLayoutSearchDiagnostics struct {
	Strategy        string   `json:"strategy"`
	Backtracks      int      `json:"backtracks"`
	RepairAttempts  int      `json:"repairAttempts"`
	MovedComponents []string `json:"movedComponents"`
	BranchLimit     int      `json:"branchLimit"`
	ConflictPasses  int      `json:"conflictPasses"`
}

var errSchematicRepairBranches = errors.New("checkpoint branch limit exhausted")
var errSchematicRepairFocus = errors.New("focused conflict pass exhausted")

// A failed direct route identifies the net and the bodies/stems which actually
// obstructed its candidate paths. Checkpoints unrelated to that conflict are
// rolled back without wasting their complete position-alternative allowance.
type schematicRouteConflict struct {
	net            string
	blockers       map[string]bool
	ownersComplete bool
}

func (e *schematicRouteConflict) Error() string {
	return fmt.Sprintf("cannot route direct net %s between measured pins without crossing obstacles", e.net)
}

type schematicRepairSearch struct {
	input       SchematicLayoutInput
	measured    map[string]powerLayoutPlacement
	members     []string
	hints       map[string]SchematicLayoutPeripheral
	budget      *int
	initial     int
	diagnostics SchematicLayoutSearchDiagnostics
	firstXY     map[string][2]float64
	lastErr     error
	focused     bool
}

func newSchematicRepairSearch(input SchematicLayoutInput, measured map[string]powerLayoutPlacement, members []string, hints map[string]SchematicLayoutPeripheral, budget *int) *schematicRepairSearch {
	return &schematicRepairSearch{input: input, measured: measured, members: members, hints: hints, budget: budget, initial: *budget,
		diagnostics: SchematicLayoutSearchDiagnostics{Strategy: "checkpoint-local-repair-v1", BranchLimit: 32, ConflictPasses: 1, MovedComponents: []string{}}, firstXY: map[string][2]float64{}, focused: true}
}

// A checkpoint owns immutable placement/wire slices. On a descendant conflict,
// a different XY is selected at this checkpoint and the entire affected suffix
// is recomputed. Neither a translated stale wire nor a partial result escapes.
func (s *schematicRepairSearch) solve(p powerLayoutPlan, pending []string) (*SchematicLayoutResult, error) {
	finished, err := s.search(p, pending)
	if err != nil && *s.budget > 0 && s.diagnostics.Backtracks < s.diagnostics.BranchLimit {
		// Net endpoints are the first relocation priority, not an exclusion of
		// other nets' obstacles. A failed focused pass retries with the complete
		// body+wire owner set, sharing the SAME total budget and branch limit.
		s.focused = false
		s.diagnostics.ConflictPasses++
		finished, err = s.search(p, pending)
	}
	if err != nil {
		if errors.Is(err, errSchematicRepairBranches) && s.lastErr != nil {
			err = fmt.Errorf("%w: %v", err, s.lastErr)
		}
		return nil, fmt.Errorf("local search failed after %d candidates, %d backtracks, %d relocation attempts (branch limit %d; no capacity proof): %w", s.initial-*s.budget, s.diagnostics.Backtracks, s.diagnostics.RepairAttempts, s.diagnostics.BranchLimit, err)
	}
	for _, id := range s.members {
		before, ok := s.firstXY[id]
		if !ok {
			continue
		}
		for _, c := range finished.Placements {
			if c.Designator == s.measured[id].Designator && before != [2]float64{c.X, c.Y} {
				s.diagnostics.MovedComponents = append(s.diagnostics.MovedComponents, id)
			}
		}
	}
	return &SchematicLayoutResult{Placements: finished.Placements, Wires: finished.Wires, Flags: finished.Flags, Score: libCandidateScore(finished), Search: &s.diagnostics}, nil
}

func (s *schematicRepairSearch) search(p powerLayoutPlan, pending []string) (*powerLayoutPlan, error) {
	if *s.budget <= 0 {
		return nil, errLibLayoutBudget
	}
	if s.diagnostics.Backtracks >= s.diagnostics.BranchLimit {
		return nil, errSchematicRepairBranches
	}
	if s.focused && s.diagnostics.Backtracks >= s.diagnostics.BranchLimit/2 && s.diagnostics.Backtracks > 0 {
		return nil, errSchematicRepairFocus
	}
	if len(pending) == 0 {
		limit := s.sliceBudget()
		before := limit
		out, err := libFinishSchematicLayout(p, s.input.NetPolicies, &limit)
		*s.budget -= before - limit
		return out, err
	}
	pending = append([]string(nil), pending...)
	sort.SliceStable(pending, func(i, j int) bool {
		return libPeripheralPriority(s.measured[pending[i]], s.input.NetPolicies) < libPeripheralPriority(s.measured[pending[j]], s.input.NetPolicies)
	})
	placed := map[string]powerLayoutPlacement{}
	for _, c := range p.Placements {
		for _, id := range s.members {
			if c.Designator == s.measured[id].Designator {
				placed[id] = c
			}
		}
	}
	var lastErr error
	for i, id := range pending {
		pairs, err := libAttachmentPairs(id, s.measured[id], s.hints[id], placed, s.members, s.input.NetPolicies)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		if len(pairs) == 0 {
			continue
		}
		rejected := map[[2]float64]bool{}
		cursor := 5.0
		for alternate := 0; alternate < 3; alternate++ {
			if *s.budget <= 0 {
				return nil, errLibLayoutBudget
			}
			if s.diagnostics.Backtracks >= s.diagnostics.BranchLimit {
				return nil, errSchematicRepairBranches
			}
			limit := s.sliceBudget()
			before := limit
			next, placeErr := libPlacePeripheralPairs(p, s.measured[id], pairs, s.input.NetPolicies, &limit, rejected, &cursor)
			*s.budget -= before - limit
			if next == nil {
				lastErr = fmt.Errorf("component %s: %w", id, placeErr)
				break
			}
			c := next.Placements[len(next.Placements)-1]
			xy := [2]float64{c.X, c.Y}
			rejected[xy] = true
			if _, ok := s.firstXY[id]; !ok {
				s.firstXY[id] = xy
			}
			if alternate > 0 {
				s.diagnostics.RepairAttempts++
			}
			rest := append(append([]string{}, pending[:i]...), pending[i+1:]...)
			out, childErr := s.search(*next, rest)
			if childErr == nil {
				return out, nil
			}
			if errors.Is(childErr, errSchematicRepairBranches) || errors.Is(childErr, errSchematicRepairFocus) {
				return nil, childErr
			}
			lastErr, s.lastErr = childErr, childErr
			s.diagnostics.Backtracks++
			if s.diagnostics.Backtracks >= s.diagnostics.BranchLimit {
				return nil, errSchematicRepairBranches
			}
			if s.focused && s.diagnostics.Backtracks >= s.diagnostics.BranchLimit/2 {
				return nil, errSchematicRepairFocus
			}
			var conflict *schematicRouteConflict
			if errors.As(childErr, &conflict) && !s.participates(id, conflict) {
				return nil, childErr
			}
			// Exhaustion of a local slice requests rollback; exhaustion of the
			// shared budget is terminal and never receives a fresh allowance.
			if errors.Is(childErr, errLibLayoutBudget) && *s.budget == 0 {
				return nil, childErr
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("disconnected or cyclic attachment for %v", pending)
	}
	return nil, lastErr
}

func (s *schematicRepairSearch) participates(id string, conflict *schematicRouteConflict) bool {
	if !conflict.ownersComplete {
		return true // Unknown ownership disables pruning in either pass.
	}
	c := s.measured[id]
	for _, p := range c.Pins {
		if p.Net == conflict.net {
			return true
		}
	}
	if s.focused {
		return false // Only priority; a full-owner pass follows if this fails.
	}
	return conflict.blockers[c.Designator]
}

// A failed greedy branch must leave budget for relocation. All nested naming,
// escape and compaction attempts debit this slice, then the same shared budget.
func (s *schematicRepairSearch) sliceBudget() int {
	quota := s.initial / 8
	if quota < 512 {
		quota = 512
	}
	if quota > 16384 {
		quota = 16384
	}
	if quota > *s.budget {
		quota = *s.budget
	}
	return quota
}
