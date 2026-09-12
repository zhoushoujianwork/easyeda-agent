package app

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// Optimization is opt-in. The original solver remains the baseline authority;
// a failed refinement never discards its complete legal result. Rotations are
// absolute stored angles, not pin swaps, mirror operations, or renderer edits.
func schematicOptimizationSettings(in SchematicLayoutInput) (*SchematicLayoutOptimization, map[string][]float64, error) {
	allowed := map[string][]float64{}
	for _, c := range in.Components {
		source := c.Measurement.Rotation
		if in.Optimization != nil && (source < 0 || source >= 360) {
			return nil, nil, fmt.Errorf("component %s optimization requires source rotation 0, 90, 180 or 270", c.ID)
		}
		angles := append([]float64(nil), c.AllowedRotations...)
		if len(angles) == 0 {
			angles = []float64{source}
		}
		seen, hasSource := map[float64]bool{}, false
		for _, angle := range angles {
			if !plFinite(angle) || angle < 0 || angle >= 360 || math.Mod(angle, 90) != 0 || seen[angle] {
				// Omitted permissions preserve legacy measured-angle acceptance.
				if len(c.AllowedRotations) == 0 && in.Optimization == nil {
					continue
				}
				return nil, nil, fmt.Errorf("component %s allowedRotations requires unique angles from 0, 90, 180, 270", c.ID)
			}
			seen[angle], hasSource = true, hasSource || angle == source
		}
		if len(c.AllowedRotations) > 0 && !hasSource {
			return nil, nil, fmt.Errorf("component %s allowedRotations must include source rotation", c.ID)
		}
		if c.ID == in.CoreComponentID && (len(angles) != 1 || angles[0] != source) {
			return nil, nil, fmt.Errorf("core component %s rotation is locked to source", c.ID)
		}
		sort.Float64s(angles)
		allowed[c.ID] = angles
	}
	if in.Optimization == nil {
		return nil, allowed, nil
	}
	opts := *in.Optimization
	if opts.MaxVariants == 0 {
		opts.MaxVariants = 4
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 24
	}
	if opts.MaxVariants < 1 || opts.MaxVariants > 4 || opts.MaxAttempts < 1 || opts.MaxAttempts > 64 {
		return nil, nil, fmt.Errorf("optimization requires maxVariants 1..4 and maxAttempts 1..64")
	}
	return &opts, allowed, nil
}

func cloneSchematicOptimizationResult(r *SchematicLayoutResult) *SchematicLayoutResult {
	copy := *r
	copy.Variants = nil
	raw, _ := json.Marshal(copy)
	var detached SchematicLayoutResult
	_ = json.Unmarshal(raw, &detached)
	return &detached
}

func schematicOptimizationDimensions(r *SchematicLayoutResult) [3]float64 {
	p := powerLayoutPlan{Placements: r.Placements, Wires: r.Wires, Flags: r.Flags}
	b := powerLayoutContentBounds(&p)
	w, h := b.MaxX-b.MinX, b.MaxY-b.MinY
	return [3]float64{w * h, w, h}
}

func schematicOptimizationLess(a, b *SchematicLayoutResult, dimension int) bool {
	x, y := schematicOptimizationDimensions(a), schematicOptimizationDimensions(b)
	order := []int{dimension, 0, 1, 2}
	for _, i := range order {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	for _, i := range []int{0, 1, 2} {
		if a.Score[i] != b.Score[i] {
			return a.Score[i] < b.Score[i]
		}
	}
	return false
}

func schematicOptimizationGeometryKey(r *SchematicLayoutResult) string {
	raw, _ := json.Marshal(struct {
		Placements []SchematicPlacement
		Wires      []SchematicWire
		Flags      []SchematicMarker
	}{r.Placements, r.Wires, r.Flags})
	return string(raw)
}

func schematicOptimizationProposalQuota(baselineCost, remaining, reserved int) int {
	available := remaining - reserved
	if available <= 0 {
		return 0
	}
	quota := baselineCost * 2
	if quota < 512 {
		quota = 512
	}
	if quota > available {
		quota = available
	}
	return quota
}

// Validate rigid provenance independently of the solver: source pin identity,
// net, NC, mirror, text reservations and body shape may only undergo an allowed
// quarter-turn plus translation. No electrical or drawing mutation is accepted
// merely because the proposed rectangle became smaller.
func validateSchematicOptimizationEvidence(in SchematicLayoutInput, r *SchematicLayoutResult, allowed map[string][]float64) error {
	if len(r.Placements) != len(in.Components) || len(r.ComponentIDs) != len(in.Components) {
		return fmt.Errorf("optimized result changed component coverage")
	}
	byRef := map[string]SchematicPlacement{}
	for _, c := range r.Placements {
		if _, exists := byRef[c.Designator]; exists {
			return fmt.Errorf("optimized result duplicated component")
		}
		byRef[c.Designator] = c
	}
	for _, c := range in.Components {
		got, ok := byRef[c.Measurement.Designator]
		if !ok || r.ComponentIDs[got.Designator] != c.ID || !reflect.DeepEqual(r.PinStates[c.ID], c.PinStates) {
			return fmt.Errorf("optimized result changed identity or NC for %s", c.ID)
		}
		permitted := false
		for _, angle := range allowed[c.ID] {
			permitted = permitted || angle == got.Rotation
		}
		if !permitted || got.Mirror != c.Measurement.Mirror {
			return fmt.Errorf("optimized result changed unapproved pose for %s", c.ID)
		}
		quarters := int(math.Mod(got.Rotation-c.Measurement.Rotation+360, 360) / 90)
		expected := plRotate(c.Measurement, quarters)
		expected = plTranslate(expected, got.X-expected.X, got.Y-expected.Y)
		if !reflect.DeepEqual(got, expected) {
			return fmt.Errorf("optimized result is not a rigid source transform for %s", c.ID)
		}
		if c.ID == in.CoreComponentID && (got.X != 0 || got.Y != 0 || got.Rotation != c.Measurement.Rotation) {
			return fmt.Errorf("optimized result moved the core")
		}
	}
	p := powerLayoutPlan{Placements: r.Placements, Wires: r.Wires, Flags: r.Flags}
	if err := validateLibGeometry(&p); err != nil {
		return err
	}
	return validateSchCompositionNets(&p)
}

func optimizeSchematicLayout(in SchematicLayoutInput, baseline *SchematicLayoutResult, measured map[string]powerLayoutPlacement, members []string, hints map[string]SchematicLayoutPeripheral, opts SchematicLayoutOptimization, allowed map[string][]float64, budget *int) *SchematicLayoutResult {
	baseline.AllowedRotations = allowed
	baseline = cloneSchematicOptimizationResult(baseline)
	pool := []*SchematicLayoutResult{baseline}
	best := baseline
	seen := map[string]bool{schematicOptimizationGeometryKey(baseline): true}
	initialRemaining, attempts := *budget, 0
	if opts.MaxVariants == 1 {
		out := cloneSchematicOptimizationResult(baseline)
		out.Variants = []SchematicLayoutVariant{{ID: "baseline", Layout: cloneSchematicOptimizationResult(baseline)}}
		out.OptimizationReport = &SchematicOptimizationReport{RemainingCandidates: *budget, StopReason: "baseline-only"}
		return out
	}
	// A full pose rebuild must receive a budget proportional to this zone's
	// demonstrated baseline cost. A fixed 4096 cap silently made every pose of
	// harder zones fail even when the caller had ample shared budget remaining.
	// spend() still clamps every proposal to the one remaining total allowance.
	// Keep a small wire-length tradeoff bounded. Area wins are not permission
	// to trade readable short connections for arbitrarily long detours.
	lengthCap := baseline.Score[0] * 1.2
	accept := func(candidate *SchematicLayoutResult) {
		if candidate == nil {
			return
		}
		candidate.ComponentIDs, candidate.PinStates, candidate.AllowedRotations = baseline.ComponentIDs, baseline.PinStates, allowed
		p := powerLayoutPlan{Placements: candidate.Placements, Wires: candidate.Wires, Flags: candidate.Flags}
		candidate.Score = libCandidateScore(&p)
		if candidate.Score[0] > lengthCap || validateSchematicOptimizationEvidence(in, candidate, allowed) != nil || validateSchematicVariantConnectivityPreserved(baseline, candidate) != nil {
			return
		}
		key := schematicOptimizationGeometryKey(candidate)
		if seen[key] {
			return
		}
		seen[key] = true
		candidate.SchemaVersion = 1
		candidate = cloneSchematicOptimizationResult(candidate)
		pool = append(pool, candidate)
		if schematicOptimizationLess(candidate, best, 0) {
			best = candidate
		}
	}
	spend := func(run func(*int) (*SchematicLayoutResult, error), reserveArg ...int) {
		if attempts >= opts.MaxAttempts || *budget <= 0 {
			return
		}
		reserved := 0
		if len(reserveArg) > 0 {
			reserved = reserveArg[0]
		}
		limit := schematicOptimizationProposalQuota(baseline.CandidatesUsed, *budget, reserved)
		if limit <= 0 {
			return
		}
		attempts++
		before := limit
		limit-- // Every proposal, including an immediate collision, costs a unit.
		candidate, err := run(&limit)
		*budget -= before - limit
		if candidate != nil {
			candidate.CandidatesUsed = before - limit
		}
		if err == nil {
			accept(candidate)
		}
	}
	// At most half the attempts are pose rebuilds; leave a bounded allocation
	// for success-after-placement inward improvement. A second pass starts from
	// the best valid incumbent, enabling useful two-component combinations without
	// enumerating the Cartesian product of all permitted orientations.
	rotationLimit := (opts.MaxAttempts + 1) / 2
	for pass := 0; pass < 2 && attempts < rotationLimit && *budget > initialRemaining/4; pass++ {
		seed := best
		seedPoses := map[string]float64{}
		for _, c := range seed.Placements {
			seedPoses[c.Designator] = c.Rotation
		}
		for choice := 0; choice < 4 && attempts < rotationLimit; choice++ {
			for _, id := range members {
				angles := allowed[id]
				if id == in.CoreComponentID || choice >= len(angles) || angles[choice] == seedPoses[measured[id].Designator] || attempts >= rotationLimit || *budget <= initialRemaining/4 {
					continue
				}
				angle := angles[choice]
				spend(func(limit *int) (*SchematicLayoutResult, error) {
					// First exploit space already present in the successful layout.
					// A rigid turn at the incumbent anchor can shorten its envelope
					// without requiring a second expensive placement search.
					fixed := append([]powerLayoutPlacement(nil), seed.Placements...)
					for i, c := range fixed {
						if c.Designator == measured[id].Designator {
							fixed[i] = plRotate(c, int(math.Mod(angle-c.Rotation+360, 360)/90))
						}
					}
					if finished, err := finishSchematicOptimizationPlacements(fixed, in.NetPolicies, limit, baseline); err == nil {
						p := powerLayoutPlan{Placements: finished.Placements, Wires: finished.Wires, Flags: finished.Flags}
						if libCandidateScore(&p)[0] <= lengthCap {
							return finished, nil
						}
					}
					posed := make(map[string]powerLayoutPlacement, len(measured))
					for _, member := range members {
						original := measured[member]
						target := seedPoses[original.Designator]
						if member == id {
							target = angle
						}
						posed[member] = plRotate(original, int(math.Mod(target-original.Rotation+360, 360)/90))
					}
					return solveSchematicLayout(in, posed, members, hints, limit)
				}, initialRemaining/4)
			}
		}
	}
	// A genuine successful-layout refinement: move one peripheral rigidly inward
	// on the shared grid, throw away ALL old wires/flags, then reconnect and name
	// from its new measured pins. Failed candidates cannot leave stale routes.
	// Step/component/axis round-robin bounds work and avoids one peripheral using
	// every attempt before another receives its first inward proposal.
	for _, step := range []float64{5, 10, 20} {
		for _, id := range members {
			if id == in.CoreComponentID {
				continue
			}
			for axis := 0; axis < 2; axis++ {
				if attempts >= opts.MaxAttempts || *budget <= 0 {
					break
				}
				seed := best
				index := -1
				for i, c := range seed.Placements {
					if c.Designator == measured[id].Designator {
						index = i
					}
				}
				if index < 0 {
					continue
				}
				c := seed.Placements[index]
				value := c.X
				if axis == 1 {
					value = c.Y
				}
				if value == 0 || math.Abs(value) < step {
					continue
				}
				delta := -math.Copysign(step, value)
				spend(func(limit *int) (*SchematicLayoutResult, error) {
					p := powerLayoutPlan{Placements: append([]powerLayoutPlacement(nil), seed.Placements...)}
					dx, dy := delta, 0.0
					if axis == 1 {
						dx, dy = 0, delta
					}
					p.Placements[index] = plTranslate(c, dx, dy)
					return finishSchematicOptimizationPlacements(p.Placements, in.NetPolicies, limit, baseline)
				})
			}
		}
	}
	// Preserve baseline plus the best area/width/height representatives. Each
	// variant is a complete immutable layout and never contains nested variants.
	selected := []*SchematicLayoutResult{baseline}
	selectedKeys := map[string]bool{schematicOptimizationGeometryKey(baseline): true}
	for _, dimension := range []int{0, 1, 2} {
		if len(selected) >= opts.MaxVariants {
			break
		}
		winner := baseline
		for _, candidate := range pool[1:] {
			if schematicOptimizationLess(candidate, winner, dimension) {
				winner = candidate
			}
		}
		key := schematicOptimizationGeometryKey(winner)
		if !selectedKeys[key] {
			selectedKeys[key] = true
			selected = append(selected, winner)
		}
	}
	// maxVariants=1 is a real baseline-only contract, not a hidden alternative.
	main := baseline
	for _, candidate := range selected[1:] {
		if schematicOptimizationLess(candidate, main, 0) {
			main = candidate
		}
	}
	out := cloneSchematicOptimizationResult(main)
	for i, candidate := range selected {
		id := fmt.Sprintf("variant-%d", i)
		if i == 0 {
			id = "baseline"
		}
		out.Variants = append(out.Variants, SchematicLayoutVariant{ID: id, Layout: cloneSchematicOptimizationResult(candidate)})
	}
	stopReason := "candidate-set-exhausted"
	if attempts >= opts.MaxAttempts {
		stopReason = "max-attempts"
	}
	if *budget <= 0 {
		stopReason = "budget-exhausted"
	}
	out.OptimizationReport = &SchematicOptimizationReport{AttemptsUsed: attempts, AcceptedCandidates: len(pool) - 1, RemainingCandidates: *budget, StopReason: stopReason}
	return out
}

func finishSchematicOptimizationPlacements(placements []powerLayoutPlacement, policies map[string]string, budget *int, baselineArg ...*SchematicLayoutResult) (*SchematicLayoutResult, error) {
	p := powerLayoutPlan{Placements: placements}
	if err := validateLibGeometry(&p); err != nil {
		return nil, err
	}
	if len(baselineArg) > 0 && baselineArg[0] != nil {
		if err := restoreSchematicOptimizationConnections(&p, baselineArg[0], policies, budget); err != nil {
			return nil, err
		}
	}
	finished, err := libFinishSchematicLayout(p, policies, budget)
	if err != nil {
		return nil, err
	}
	return &SchematicLayoutResult{Placements: finished.Placements, Wires: finished.Wires, Flags: finished.Flags}, nil
}

// Preserve the incumbent's physical wire islands before optional rail/port
// policies run. A formerly mandatory connection must not disappear merely
// because a new pin pair is farther than the optional 80-raw rail join radius.
// We regenerate routes, never translate stale wire vertices.
func restoreSchematicOptimizationConnections(p *powerLayoutPlan, baseline *SchematicLayoutResult, policies map[string]string, budget *int) error {
	islands, err := schematicVariantPhysicalIslands(baseline)
	if err != nil {
		return err
	}
	sort.SliceStable(islands, func(i, j int) bool {
		return libNetPriority(policies[islands[i].Net]) < libNetPriority(policies[islands[j].Net])
	})
	pins := map[schematicVariantPhysicalPin]powerLayoutPin{}
	for _, c := range p.Placements {
		for _, q := range c.Pins {
			pins[schematicVariantPhysicalPin{c.Designator, q.Number}] = q
		}
	}
	physical := func(plan *powerLayoutPlan) (map[schematicVariantPinIdentity]schematicVariantPinIsland, error) {
		return schematicVariantPhysicalPinIslands(&SchematicLayoutResult{ComponentIDs: baseline.ComponentIDs, Placements: plan.Placements, Wires: plan.Wires, Flags: plan.Flags})
	}
	identity := func(pin schematicVariantPhysicalPin) schematicVariantPinIdentity {
		return schematicVariantPinIdentity{baseline.ComponentIDs[pin.Designator], pin.Number}
	}
	for _, island := range islands {
		for len(island.Pins) > 1 {
			current, err := physical(p)
			if err != nil {
				return err
			}
			type edge struct {
				a, b   schematicVariantPhysicalPin
				length float64
			}
			edges := []edge{}
			for i, a := range island.Pins {
				qa, exists := pins[a]
				if !exists || qa.Net != island.Net {
					return fmt.Errorf("required physical island pin %s.%s changed", a.Designator, a.Number)
				}
				for _, b := range island.Pins[:i] {
					if current[identity(a)] == current[identity(b)] {
						continue
					}
					qb := pins[b]
					edges = append(edges, edge{a, b, math.Abs(qa.X-qb.X) + math.Abs(qa.Y-qb.Y)})
				}
			}
			if len(edges) == 0 {
				break
			}
			sort.SliceStable(edges, func(i, j int) bool { return edges[i].length < edges[j].length })
			joined := false
			for _, e := range edges {
				qa, qb := pins[e.a], pins[e.b]
				for _, route := range append(libRoutes(qa, qb), libDetourRoutes(qa, qb)...) {
					if *budget <= 0 {
						return errLibLayoutBudget
					}
					*budget--
					trial := *p
					trial.Wires = libAppendRoute(p.Wires, route)
					if validateLibGeometry(&trial) != nil {
						continue
					}
					after, err := physical(&trial)
					if err != nil || after[identity(e.a)] != after[identity(e.b)] {
						continue
					}
					*p, joined = trial, true
					break
				}
				if joined {
					break
				}
			}
			if !joined {
				return fmt.Errorf("cannot rebuild required direct wire island for net %s; label-only replacement is forbidden", island.Net)
			}
		}
	}
	return nil
}
