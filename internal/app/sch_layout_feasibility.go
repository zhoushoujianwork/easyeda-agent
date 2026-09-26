package app

import (
	"encoding/json"
	"fmt"
)

const schematicFeasibilityPoseLimit = 16

type SchematicFeasibilityAttempt struct {
	Rotations      map[string]float64 `json:"rotations"`
	CandidateQuota int                `json:"candidateQuota"`
	CandidatesUsed int                `json:"candidatesUsed"`
	Failure        string             `json:"failure,omitempty"`
	Diagnostics    []any              `json:"diagnostics,omitempty"`
}

type SchematicFeasibilityReport struct {
	Strategy            string                        `json:"strategy"`
	PoseLimit           int                           `json:"poseLimit"`
	ProposalsTruncated  bool                          `json:"proposalsTruncated"`
	Attempts            []SchematicFeasibilityAttempt `json:"attempts"`
	CandidatesUsed      int                           `json:"candidatesUsed"`
	RemainingCandidates int                           `json:"remainingCandidates"`
	SelectedRotations   map[string]float64            `json:"selectedRotations,omitempty"`
	StopReason          string                        `json:"stopReason"`
}

type schematicFeasibilityError struct {
	Report *SchematicFeasibilityReport
	Cause  error
}

func (e *schematicFeasibilityError) Error() string {
	report, _ := json.Marshal(e.Report)
	return fmt.Sprintf("allowed-pose feasibility failed: %d attempts, %d candidates used, %d remaining, stop=%s, bounded pose limit %d (no global feasibility proof): %v; feasibilityReport=%s", len(e.Report.Attempts), e.Report.CandidatesUsed, e.Report.RemainingCandidates, e.Report.StopReason, e.Report.PoseLimit, e.Cause, report)
}
func (e *schematicFeasibilityError) Unwrap() error { return e.Cause }

// Explicit permissions are authority, not inferred symmetry or permission to
// move a core. Coordinate choices remain the shared solver's responsibility.
// Coordinated proposals precede singles so jointly necessary flips (e.g. two
// adjacent decouplers) are not starved by unsuccessful one-at-a-time attempts.
// This bounded deterministic menu is intentionally not a Cartesian proof.
func schematicFeasibilityPoses(input SchematicLayoutInput, allowed map[string][]float64) ([]map[string]float64, bool) {
	type choices struct {
		id     string
		angles []float64
	}
	var components []choices
	for _, c := range input.Components {
		if c.ID == input.CoreComponentID || len(c.AllowedRotations) < 2 {
			continue
		}
		item := choices{id: c.ID}
		for _, angle := range allowed[c.ID] {
			if angle != c.Measurement.Rotation {
				item.angles = append(item.angles, angle)
			}
		}
		if len(item.angles) > 0 {
			components = append(components, item)
		}
	}
	var poses []map[string]float64
	seen := map[string]bool{}
	add := func(pose map[string]float64) bool {
		if len(pose) == 0 {
			return true
		}
		raw, _ := json.Marshal(pose)
		key := string(raw)
		if seen[key] {
			return true
		}
		if len(poses) >= schematicFeasibilityPoseLimit {
			return false
		}
		seen[key] = true
		poses = append(poses, pose)
		return true
	}
	// Explicit attachment pins can require different coordinated rotations;
	// rotating every peripheral by the same rank misses that useful first pose.
	add(schematicAttachmentFacingPose(input, allowed))
	for rank := 0; rank < 3; rank++ {
		pose := map[string]float64{}
		for _, c := range components {
			if rank < len(c.angles) {
				pose[c.id] = c.angles[rank]
			}
		}
		if !add(pose) {
			return poses, true
		}
	}
	for rank := 0; rank < 3; rank++ {
		for _, c := range components {
			if rank < len(c.angles) && !add(map[string]float64{c.id: c.angles[rank]}) {
				return poses, true
			}
		}
	}
	for i, a := range components {
		for _, b := range components[i+1:] {
			for _, x := range a.angles {
				for _, y := range b.angles {
					if !add(map[string]float64{a.id: x, b.id: y}) {
						return poses, true
					}
				}
			}
		}
	}
	return poses, false
}

// Preserve the original default 20k candidate window (or the whole smaller
// allowance) before reserving budget for poses. A known feasible small-budget
// source must not be lost merely because optional fallback was introduced. The
// measured source pose is the only pose backed by fresh page evidence, so give
// it three quarters of a larger allowance (capped at 150k) before exploring
// optional rotations. Each fallback still receives a fair share of the same
// remaining allowance. Unspent quota returns, never resets.
func runSchematicLayoutFeasibility(input SchematicLayoutInput, measured map[string]powerLayoutPlacement, allowed map[string][]float64, budget *int,
	run func(map[string]powerLayoutPlacement, *int) (*SchematicLayoutResult, error),
) (*SchematicLayoutResult, *SchematicFeasibilityReport, error) {
	poses, truncated := schematicFeasibilityPoses(input, allowed)
	if len(poses) == 0 {
		out, err := run(measured, budget)
		return out, nil, err // Preserve the established fixed-pose contract.
	}
	report := &SchematicFeasibilityReport{Strategy: "allowed-pose-baseline-v1", PoseLimit: schematicFeasibilityPoseLimit, ProposalsTruncated: truncated}
	initial := *budget
	proposals := append([]map[string]float64{{}}, poses...)
	var lastErr error
	for index, rotations := range proposals {
		if *budget <= 0 {
			break
		}
		quota := *budget / (len(proposals) - index)
		if index == 0 {
			quota = *budget * 3 / 4
			if *budget <= 40000 {
				quota = 20000
			}
			if quota < 20000 {
				quota = 20000
			}
			if quota > 150000 {
				quota = 150000
			}
			if quota > *budget {
				quota = *budget
			}
		}
		if index == 1 && len(schematicAttachmentFacingPose(input, allowed)) > 0 {
			// Preserve the measured first attempt, then let the coherent attachment
			// pose receive at least half the returned quota, capped at the default
			// 20k boost, without shrinking a larger fair share. Unused quota returns.
			preferred := *budget / 2
			if preferred > 20000 {
				preferred = 20000
			}
			if preferred > quota {
				quota = preferred
			}
		}
		if quota < 1 {
			quota = 1
		}
		pose := make(map[string]powerLayoutPlacement, len(measured))
		for id, c := range measured {
			pose[id] = c
		}
		for id, angle := range rotations {
			c := measured[id]
			quarters := int(schematicVariantRotation(angle-c.Rotation) / 90)
			pose[id] = plRotate(c, quarters)
		}
		remaining := quota
		out, err := run(pose, &remaining)
		if remaining < 0 || remaining > quota {
			return nil, report, fmt.Errorf("allowed-pose solver violated shared candidate budget")
		}
		used := quota - remaining
		*budget -= used
		attempt := SchematicFeasibilityAttempt{Rotations: rotations, CandidateQuota: quota, CandidatesUsed: used}
		if err != nil {
			attempt.Failure = err.Error()
			attempt.Diagnostics = schLayoutFailureDiagnostics(err)
		}
		report.Attempts = append(report.Attempts, attempt)
		report.CandidatesUsed, report.RemainingCandidates = initial-*budget, *budget
		if err == nil && out != nil {
			report.SelectedRotations = rotations
			report.StopReason = "source-pose-feasible"
			if index > 0 {
				report.StopReason = "allowed-pose-feasible"
			}
			return out, report, nil
		}
		if err == nil {
			err = fmt.Errorf("solver returned neither complete layout nor error")
			report.Attempts[len(report.Attempts)-1].Failure = err.Error()
		}
		lastErr = err
	}
	report.StopReason = "pose-menu-exhausted"
	if report.ProposalsTruncated {
		report.StopReason = "pose-limit-exhausted"
	}
	if *budget <= 0 {
		report.StopReason = "candidate-budget-exhausted"
	}
	return nil, report, &schematicFeasibilityError{Report: report, Cause: lastErr}
}

// Derive rotations only from explicit ownership and measured pin directions.
// No coordinates, inferred electrical ownership, or unapproved rotations enter
// this proposal. Child poses use the selected parent's measured rigid transform.
func schematicAttachmentFacingPose(input SchematicLayoutInput, allowed map[string][]float64) map[string]float64 {
	pose := map[string]float64{}
	geometry := map[string]powerLayoutPlacement{}
	components := map[string]SchematicLayoutComponent{}
	for _, c := range input.Components {
		geometry[c.ID], components[c.ID] = c.Measurement, c
	}
	ready := map[string]bool{input.CoreComponentID: true}
	for pass := 0; pass < len(input.Components); pass++ {
		progress := false
		for _, h := range input.Attachments {
			if ready[h.ComponentID] || h.AttachTo == nil || !ready[h.AttachTo.ComponentID] {
				continue
			}
			c, exists := components[h.ComponentID]
			if !exists || h.ComponentID == input.CoreComponentID {
				continue
			}
			host := geometry[h.AttachTo.ComponentID]
			hp, ok := libPin(host, h.AttachTo.PinNumber)
			if !ok {
				continue
			}
			hostSide, err := libPinSide(hp, host.BBox)
			if err != nil {
				continue
			}
			if len(c.AllowedRotations) > 1 && h.PinNumber != "" {
				for _, angle := range allowed[c.ID] {
					candidate := plRotate(c.Measurement, int(schematicVariantRotation(angle-c.Measurement.Rotation)/90))
					own, ok := libPin(candidate, h.PinNumber)
					if !ok || own.Net == "" || own.Net != hp.Net {
						continue
					}
					side, err := libPinSide(own, candidate.BBox)
					if err != nil || side != oppositeDirection(hostSide) {
						continue
					}
					geometry[c.ID] = candidate
					if angle != c.Measurement.Rotation {
						pose[c.ID] = angle
					}
					break
				}
			}
			ready[c.ID], progress = true, true
		}
		if !progress {
			break
		}
	}
	return pose
}
