package app

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

type libAttachmentPair struct {
	host, own powerLayoutPin
	side      string
	rank      int
}

// Naming can fail even after a complete physical island has been routed. Its
// measured endpoint owners are candidate placement dependencies, not claimed
// geometric blockers; a bounded trial must still regenerate and validate the
// entire terminal plan before it can be accepted.
type schematicNamingConflict struct {
	net            string
	pin            powerLayoutPin
	endpointOwners map[string]bool
	ownersComplete bool
	layout         *powerLayoutPlan
	markerBlockers map[string]bool
}

func (e *schematicNamingConflict) Error() string {
	return fmt.Sprintf("net %s has no safe naming lead for island at pin %s (%g,%g)", e.net, e.pin.Number, e.pin.X, e.pin.Y)
}
func (e *schematicNamingConflict) FailureDetails() any {
	refs := make([]string, 0, len(e.endpointOwners))
	for ref := range e.endpointOwners {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	blockers := []string{}
	for ref := range e.markerBlockers {
		blockers = append(blockers, ref)
	}
	sort.Strings(blockers)
	return struct {
		Kind                      string           `json:"kind"`
		Net                       string           `json:"net"`
		EndpointRefs              []string         `json:"endpointRefs"`
		OwnersComplete            bool             `json:"ownersComplete"`
		GlobalInfeasibilityProven bool             `json:"globalInfeasibilityProven"`
		NamingLayout              *powerLayoutPlan `json:"namingLayout,omitempty"`
		ObservedMarkerBlockerRefs []string         `json:"observedMarkerBlockerRefs,omitempty"`
	}{"naming-conflict", e.net, refs, e.ownersComplete, false, e.layout, blockers}
}

func libAttachmentPairs(id string, own powerLayoutPlacement, hint SchematicLayoutPeripheral, placed map[string]powerLayoutPlacement, order []string, policies map[string]string) ([]libAttachmentPair, error) {
	if hint.PinNumber != "" {
		if _, ok := libPin(own, hint.PinNumber); !ok {
			return nil, fmt.Errorf("unknown peripheral pin %s", hint.PinNumber)
		}
	}
	if hint.AttachTo != nil {
		if _, ok := placed[hint.AttachTo.ComponentID]; !ok {
			return nil, nil
		}
	}
	pairs := []libAttachmentPair{}
	for _, hostID := range order {
		host, ok := placed[hostID]
		if !ok {
			continue
		}
		if hint.AttachTo != nil && hint.AttachTo.ComponentID != hostID {
			continue
		}
		foundPin := hint.AttachTo == nil
		for _, hp := range host.Pins {
			if hint.AttachTo != nil && hp.Number != hint.AttachTo.PinNumber {
				continue
			}
			foundPin = true
			if hp.Net == "" {
				continue
			}
			if hint.AttachTo == nil && policies[hp.Net] == "local_ground" {
				continue
			}
			side, err := libPinSide(hp, host.BBox)
			if err != nil {
				return nil, err
			}
			for _, op := range own.Pins {
				if op.Net == "" || hp.Net != op.Net || (hint.PinNumber != "" && hint.PinNumber != op.Number) {
					continue
				}
				rank := 0
				if policies[hp.Net] == "local_power" {
					rank = 1
				}
				pairs = append(pairs, libAttachmentPair{hp, op, side, rank})
			}
		}
		if !foundPin {
			return nil, fmt.Errorf("unknown attachTo pin %s.%s", hostID, hint.AttachTo.PinNumber)
		}
	}
	if hint.AttachTo != nil && len(pairs) == 0 {
		return nil, fmt.Errorf("attachTo has no shared connected pin on %s", id)
	}
	// A reference chooses geometry, not electrical intent. Multiple already-
	// connected candidates are legal; authored member/pin order breaks ties.
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].rank < pairs[j].rank })
	return pairs, nil
}

func libPlacePeripheral(current powerLayoutPlan, measured powerLayoutPlacement, pair libAttachmentPair, policies map[string]string, budget *int) (*powerLayoutPlan, error) {
	return libPlacePeripheralPairs(current, measured, []libAttachmentPair{pair}, policies, budget, nil)
}

// Try every attachment pair at each distance shell before extending any pair.
// Previously one unsuitable pair could spend the entire global allowance while
// another pair had a legal short connection. Rejected XYs are checkpoint-local:
// they request an actual relocation, not merely a different processing order.
func libPlacePeripheralPairs(current powerLayoutPlan, measured powerLayoutPlacement, pairs []libAttachmentPair, policies map[string]string, budget *int, rejected map[[2]float64]bool, cursor ...*float64) (*powerLayoutPlan, error) {
	var nextCursor *float64
	if len(cursor) > 0 {
		nextCursor = cursor[0]
	}
	return libPlacePeripheralPairsWithRouting(current, measured, pairs, policies, budget, rejected, nil, nextCursor)
}

func libPlacePeripheralPairsWithRouting(current powerLayoutPlan, measured powerLayoutPlacement, pairs []libAttachmentPair, policies map[string]string, budget *int, rejected map[[2]float64]bool, routing *schematicRoutingContext, cursor *float64) (*powerLayoutPlan, error) {
	current.Flags = nil // Full marker placement is deferred to the final gate.
	existingContactNodes := libWireContactNodes(current.Wires)
	namingWitnesses := libNamingPlacementWitnesses{}
	var lastErr error
	conflict := newPlacementCandidateConflict()
	// Search complete distance shells: don't accept the first legal coordinate.
	// All candidates in the first feasible shell compete on the full objective.
	start := 5.0
	if cursor != nil && *cursor > start {
		start = *cursor
	}
	for cost := start; cost <= 600; cost += 5 {
		var best *powerLayoutPlan
		var bestRouting []powerLayoutWire
		var bestPair libAttachmentPair
		finishBest := func() *powerLayoutPlan {
			if best != nil {
				if cursor != nil {
					// A legal position in this shell can block a dependent part while
					// another position at the same cost leaves its exit open. The
					// checkpoint's rejected XY set prevents replay; its three-choice
					// limit and the shared candidate budget still bound this search.
					*cursor = cost
				}
				best.Wires = bestRouting
				best.Flags = nil
			}
			return best
		}
		for lateral := 0.0; lateral <= math.Min(200, cost-5); lateral += 5 {
			distance := cost - lateral
			if distance > 400 {
				continue
			}
			for _, sign := range []float64{1, -1} {
				if lateral == 0 && sign < 0 {
					continue
				}
				for _, pair := range pairs {
					x, y := endpointFor(pair.host.X, pair.host.Y, distance, pair.side)
					if pair.side == "left" || pair.side == "right" {
						y += sign * lateral
					} else {
						x += sign * lateral
					}
					c := plTranslate(measured, x-pair.own.X, y-pair.own.Y)
					if rejected[[2]float64{c.X, c.Y}] {
						continue
					}
					if *budget <= 0 {
						if best != nil {
							return finishBest(), nil
						}
						return nil, conflict.finish("candidate-budget", errLibLayoutBudget)
					}
					*budget -= 1
					conflict.candidates++
					trial := current
					trial.Placements = append(append([]powerLayoutPlacement{}, current.Placements...), c)
					if lastErr = validateLibGeometry(&trial); lastErr != nil {
						conflict.observe(lastErr)
						continue
					}
					if lastErr = libValidateMandatoryDirectPlacementFrontiers(&current, &trial, c, policies, routing); lastErr != nil {
						conflict.observe(lastErr)
						continue
					}
					if lastErr = libValidateSingletonNamingPlacement(&current, &trial, c, policies, routing, budget, namingWitnesses); lastErr != nil {
						conflict.observe(lastErr)
						continue
					}
					q, _ := libPin(c, pair.own.Number)
					// Facing pins on a net that still has another source-data pin need
					// an exact grid midpoint for a future T. This is about the whole net,
					// not whether both symbols happen to be two-terminal devices.
					netPinCount := libPlanNetPinCount(&trial, q.Net)
					if routing != nil && routing.netPins[q.Net] > netPinCount {
						netPinCount = routing.netPins[q.Net]
					}
					if libNetPriority(policies[q.Net]) == 2 && netPinCount > 2 && libFacingPins(&trial, pair.host, q) && (!plGrid((pair.host.X+q.X)/2) || !plGrid((pair.host.Y+q.Y)/2)) {
						conflict.reasons["tap-grid"]++
						continue
					}
					routes := libRoutes(pair.host, q, &trial)
					if len(routes) == 0 {
						conflict.reasons["no-directional-route"]++
					}
					for _, route := range routes {
						candidate := trial
						candidate.Wires = libAppendRouteWithNodes(current.Wires, route, existingContactNodes)
						if lastErr = validateLibGeometry(&candidate); lastErr != nil {
							conflict.observe(lastErr)
							continue
						}
						// Nearby rails are provisional checkpoint data: descendants may
						// legally attach to their real midspan, but the terminal regeneration
						// withdraws the whole forest and rebuilds it in a bounded order.
						if lastErr = libJoinNearbyRails(&candidate, policies); lastErr != nil {
							conflict.observe(lastErr)
							continue
						}
						// A short provisional named join can be electrically complete
						// while sealing both outward pin exits. Reject that position
						// here, while the distance shell can still try another XY,
						// instead of repeatedly failing the terminal regeneration.
						if q.Net != "" && libPinsShareIsland(&candidate, pair.host, q) && !libIslandMergeCanContinue(&candidate, pair.host, q) {
							conflict.reasons["named-frontier-sealed"]++
							continue
						}
						// This is a geometry/routing checkpoint, not a completed
						// schematic. Naming every unchanged core pin here made a
						// dense core cost O(proposals * all pins * escape routes).
						// The full naming/electrical gate runs at the final checkpoint;
						// any failure rolls back these provisional placements.
						if best == nil || libPairCandidateLess(&candidate, pair, best, bestPair) {
							best = &candidate
							bestRouting = candidate.Wires
							bestPair = pair
						}
					}
				}
			}
		}
		if best != nil {
			// Return provisional geometry; complete naming is the terminal gate.
			return finishBest(), nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no untried coordinate in 400 raw outward / 200 raw lateral search")
	}
	return nil, conflict.finish("coordinate-window", lastErr)
}

func libPairCandidateLess(a *powerLayoutPlan, ap libAttachmentPair, b *powerLayoutPlan, bp libAttachmentPair) bool {
	if ap == bp {
		return libAlignedCandidateLess(a, b, ap)
	}
	if ap.rank != bp.rank {
		return ap.rank < bp.rank
	}
	axisError := func(p *powerLayoutPlan, pair libAttachmentPair) float64 {
		q, _ := libPin(p.Placements[len(p.Placements)-1], pair.own.Number)
		if pair.side == "left" || pair.side == "right" {
			return math.Abs(q.Y - pair.host.Y)
		}
		return math.Abs(q.X - pair.host.X)
	}
	x, y := axisError(a, ap), axisError(b, bp)
	if x != y {
		return x < y
	}
	return libCandidateLess(a, b)
}

// Each island needs exactly one real naming lead. Local rail policies permit
// multiple islands; a direct net is joined before this function's final call.
type libIsland struct {
	net         string
	key         string
	pins        []powerLayoutPin
	wireIndices []int
}

func libIslands(p *powerLayoutPlan) []libIsland {
	parent := make([]int, len(p.Wires))
	for i := range parent {
		parent[i] = i
	}
	var root func(int) int
	root = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i, w := range p.Wires {
		for j, v := range p.Wires[:i] {
			if w.Net == v.Net && plSegmentsContact(w.Points[0], w.Points[1], v.Points[0], v.Points[1]) {
				parent[root(i)] = root(j)
			}
		}
	}
	indices := map[string]int{}
	wireRoots := map[int]int{}
	islands := []libIsland{}
	for _, c := range p.Placements {
		for _, q := range c.Pins {
			if q.Net == "" {
				continue
			}
			key := fmt.Sprintf("%q:pin:%g,%g", q.Net, q.X, q.Y)
			for i, w := range p.Wires {
				if w.Net == q.Net && plOnSegment([2]float64{q.X, q.Y}, w.Points[0], w.Points[1]) {
					key = fmt.Sprintf("%q:wire:%d", q.Net, root(i))
					break
				}
			}
			index, ok := indices[key]
			if !ok {
				index = len(islands)
				indices[key] = index
				islands = append(islands, libIsland{net: q.Net, key: key})
			}
			islands[index].pins = append(islands[index].pins, q)
			for i, w := range p.Wires {
				if w.Net == q.Net && plOnSegment([2]float64{q.X, q.Y}, w.Points[0], w.Points[1]) {
					wireRoots[root(i)] = index
					break
				}
			}
		}
	}
	for i := range p.Wires {
		if index, ok := wireRoots[root(i)]; ok {
			islands[index].wireIndices = append(islands[index].wireIndices, i)
		}
	}
	return islands
}

func libNameIslands(p *powerLayoutPlan, policies map[string]string, budget ...*int) error {
	base := *p
	base.Flags = nil
	// All route joins have finished. Every remaining physical island therefore
	// needs its own legal marker lead. Prove each independently before exploring
	// combinations of unrelated labels: one sealed direct tree or module port
	// would otherwise consume the entire joint-search slice and hide its exact
	// placement/routing conflict.
	for _, island := range libIslands(&base) {
		probe := 4096
		if len(budget) > 0 && *budget[0] < probe {
			probe = *budget[0]
		}
		if probe <= 0 {
			return errLibLayoutBudget
		}
		before := probe
		blockers := map[string]bool{}
		reachable, complete := libNamingFrontier(&base, island, policies[island.net], &probe, blockers)
		if len(budget) > 0 {
			*budget[0] -= before - probe
		}
		if !reachable && complete {
			conflict := libNamingConflict(&base, island)
			conflict.markerBlockers = blockers
			return conflict
		}
		if !complete && len(budget) > 0 && *budget[0] <= 0 {
			return errLibLayoutBudget
		}
	}
	var lastErr error
	var jointOrder []libIsland
	for order := 0; order < 5; order++ {
		trial := base
		islands := libIslands(&trial)
		sort.SliceStable(islands, func(i, j int) bool {
			a, b := islands[i].pins[0], islands[j].pins[0]
			switch order {
			case 0:
				return libNetPriority(policies[islands[i].net]) < libNetPriority(policies[islands[j].net])
			case 1, 2:
				if a.Y != b.Y {
					if order == 1 {
						return a.Y > b.Y
					}
					return a.Y < b.Y
				}
				return a.X < b.X
			case 3:
				if a.X != b.X {
					return a.X < b.X
				}
				return a.Y > b.Y
			default:
				if islands[i].net != islands[j].net {
					return islands[i].net < islands[j].net
				}
				return islands[i].key < islands[j].key
			}
		})
		if order == 0 {
			jointOrder = append([]libIsland(nil), islands...)
		}
		if lastErr = libNameOrderedIslands(&trial, policies, islands, budget...); lastErr == nil {
			*p = trial
			return nil
		}
		if len(budget) > 0 && *budget[0] <= 0 {
			return errLibLayoutBudget
		}
	}
	// Fixed order retries cannot revise an earlier legal lead that blocks a
	// later island. Explore a small set of complete lead alternatives per
	// island, debiting the same candidate allowance as the greedy passes.
	jointBudget := 4096
	if len(budget) == 0 {
		budget = []*int{&jointBudget}
	}
	if joint := libNameIslandsJoint(&base, policies, jointOrder, budget[0]); joint != nil {
		if *budget[0] <= 0 {
			return errLibLayoutBudget
		}
		// A bounded fallback cannot replace the fully observed greedy conflict
		// with a weaker diagnostic from a different, truncated branch.
		return lastErr
	}
	*p = base
	return nil
}

const libJointNamingChoicesPerIsland = 8

func libNameIslandsJoint(base *powerLayoutPlan, policies map[string]string, islands []libIsland, budget *int) error {
	return libNameIslandsJointWithMode(base, policies, islands, budget, libJointNamingOptions, true)
}

type libNamingOptionVisitor func(*powerLayoutPlan, libIsland, string, func(*powerLayoutPlan) bool, *int)

func libJointNamingOptions(current *powerLayoutPlan, island libIsland, kind string, accept func(*powerLayoutPlan) bool, budget *int) {
	choices := 0
	halt := false
	limited := func(candidate *powerLayoutPlan) bool {
		choices++
		halt = accept(candidate) || choices >= libJointNamingChoicesPerIsland || *budget <= 0
		return halt
	}
	// Give each naming family a chance before a long offset scan consumes all
	// eight choices on one anchor. The fast path above still keeps its original
	// shortest-first behavior.
	familyChoices := 0
	family := func(candidate *powerLayoutPlan) bool {
		familyChoices++
		return limited(candidate) || familyChoices >= 2
	}
	libVisitMidpointMarker(current, island, kind, family, budget)
	if halt || *budget <= 0 {
		return
	}
	familyChoices = 0
	libVisitWireTreeMarker(current, island, kind, family, budget)
	if halt || *budget <= 0 {
		return
	}
	for _, pin := range island.pins {
		if libVisitMarkerLimited(current, pin, kind, 2, limited, budget) || halt || *budget <= 0 {
			return
		}
	}
}

func libNameIslandsJointWith(base *powerLayoutPlan, policies map[string]string, islands []libIsland, budget *int, options libNamingOptionVisitor) error {
	return libNameIslandsJointWithMode(base, policies, islands, budget, options, false)
}

func libNameIslandsJointWithMode(base *powerLayoutPlan, policies map[string]string, islands []libIsland, budget *int, options libNamingOptionVisitor, checkRemaining bool) error {
	var lastErr error
	var visit func(powerLayoutPlan, int) (*powerLayoutPlan, bool)
	visit = func(current powerLayoutPlan, index int) (*powerLayoutPlan, bool) {
		if index == len(islands) {
			if err := validateLibGeometry(&current); err != nil {
				lastErr = err
				return nil, false
			}
			if err := validateSchCompositionNets(&current); err != nil {
				lastErr = err
				return nil, false
			}
			return &current, true
		}
		if *budget <= 0 {
			lastErr = errLibLayoutBudget
			return nil, false
		}
		island := islands[index]
		kind := "net_port_bi"
		switch policies[island.net] {
		case "local_ground":
			kind = "ground"
		case "local_power":
			kind = "power"
		}
		var solved *powerLayoutPlan
		accept := func(candidate *powerLayoutPlan) bool {
			if checkRemaining {
				for _, remaining := range islands[index+1:] {
					if policies[remaining.net] != "module_port" || libPlanNetPinCount(candidate, remaining.net) != 1 {
						continue
					}
					probe := 512
					if *budget < probe {
						probe = *budget
					}
					if probe <= 0 {
						lastErr = errLibLayoutBudget
						return true
					}
					before := probe
					reachable, complete := libNamingFrontier(candidate, remaining, "module_port", &probe)
					*budget -= before - probe
					if !reachable && complete {
						lastErr = libNamingConflict(candidate, remaining)
						return false
					}
					if *budget <= 0 {
						lastErr = errLibLayoutBudget
						return true
					}
				}
			}
			if result, ok := visit(*candidate, index+1); ok {
				solved = result
				return true
			}
			return *budget <= 0
		}
		options(&current, island, kind, accept, budget)
		if solved != nil {
			return solved, true
		}
		if *budget <= 0 {
			lastErr = errLibLayoutBudget
		} else if lastErr == nil {
			lastErr = libNamingConflict(&current, island)
		}
		return nil, false
	}
	if solved, ok := visit(*base, 0); ok {
		// The caller only observes a complete solution.
		*base = *solved
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("bounded joint naming found no complete lead assignment")
	}
	return lastErr
}

func libNamingConflict(p *powerLayoutPlan, island libIsland) *schematicNamingConflict {
	conflict := &schematicNamingConflict{net: island.net, pin: island.pins[0], endpointOwners: map[string]bool{}, ownersComplete: true}
	snapshot := *p
	snapshot.Placements = make([]powerLayoutPlacement, len(p.Placements))
	for i, c := range p.Placements {
		snapshot.Placements[i] = plTranslate(c, 0, 0)
		for j, pin := range snapshot.Placements[i].Pins {
			if pin.Rotation != nil {
				rotation := *pin.Rotation
				snapshot.Placements[i].Pins[j].Rotation = &rotation
			}
		}
	}
	snapshot.Wires = clonePowerLayoutWires(p.Wires)
	snapshot.Flags = append([]powerLayoutFlag(nil), p.Flags...)
	snapshot.namingBlockers = nil
	conflict.layout = &snapshot
	for _, pin := range island.pins {
		if ref := libExactPinOwner(p, pin); ref != "" {
			conflict.endpointOwners[ref] = true
		} else {
			conflict.ownersComplete = false
		}
	}
	if len(conflict.endpointOwners) == 0 {
		conflict.ownersComplete = false
	}
	return conflict
}

func libNameOrderedIslands(p *powerLayoutPlan, policies map[string]string, islands []libIsland, budget ...*int) error {
	// Ground constraints are tighter than signal naming at dense core pins.
	for _, island := range islands {
		kind := "net_port_bi"
		if policies[island.net] == "local_ground" {
			kind = "ground"
		}
		if policies[island.net] == "local_power" {
			kind = "power"
		}
		if libPlaceMidpointMarker(p, island, kind, budget...) {
			continue
		}
		// The direct/module tree is already a real physical island at this
		// point. Dense endpoints can have no remaining naming clearance even
		// though an ordinary segment in the same tree has a legal perpendicular
		// branch. Name that tree through an actual contact before retrying the
		// component pins; this never substitutes a label for libJoinDirectNets.
		if libPlaceWireTreeMarker(p, island, kind, budget...) {
			continue
		}
		var best *powerLayoutPlan
		for _, q := range island.pins {
			trial := *p
			if libPlaceMarker(&trial, q, kind, budget...) {
				if best == nil || libCandidateLess(&trial, best) {
					best = &trial
				}
			}
		}
		if best == nil {
			return libNamingConflict(p, island)
		}
		*p = *best
	}
	return nil
}

func libJoinDirectNets(p *powerLayoutPlan, policies map[string]string, routing ...*schematicRoutingContext) error {
	return libJoinNets(p, policies, false, routing...)
}

func libJoinNearbyRails(p *powerLayoutPlan, policies map[string]string) error {
	return libJoinNets(p, policies, true)
}

func libJoinNets(p *powerLayoutPlan, policies map[string]string, railsOnly bool, routing ...*schematicRoutingContext) error {
	return libJoinNetsMode(p, policies, railsOnly, true, routing...)
}

func libJoinNetsMode(p *powerLayoutPlan, policies map[string]string, railsOnly, joinPorts bool, routingArg ...*schematicRoutingContext) error {
	var routing *schematicRoutingContext
	if len(routingArg) > 0 {
		routing = routingArg[0]
	}
	if routing == nil && !railsOnly {
		components := make([]SchematicLayoutComponent, 0, len(p.Placements))
		for _, placement := range p.Placements {
			components = append(components, SchematicLayoutComponent{ID: placement.Designator, Measurement: placement})
		}
		routing, _ = newSchematicRoutingContext(nil, components)
	}
	if railsOnly {
		return libJoinNetsPass(p, policies, true, joinPorts, nil, nil, false)
	}
	baseline := *p
	baseline.Wires = append([]powerLayoutWire(nil), p.Wires...)
	baseline.Flags = append([]powerLayoutFlag(nil), p.Flags...)
	var lastErr error
	preferred := []string(nil)
	for round := 0; ; round++ {
		trial := baseline
		trial.Wires = append([]powerLayoutWire(nil), baseline.Wires...)
		trial.Flags = append([]powerLayoutFlag(nil), baseline.Flags...)
		err := libJoinNetsPass(&trial, policies, false, joinPorts, routing, preferred, round%2 == 1)
		if err == nil {
			*p = trial
			return nil
		}
		lastErr = err
		var failure *schematicRoutingFailure
		if errors.As(err, &failure) && failure.Kind == "expanded-node-budget-exhausted" {
			return err
		}
		if routing == nil || routing.reroutes >= routing.options.MaxReroutes {
			return lastErr
		}
		if len(libIslands(&trial)) >= len(libIslands(&baseline)) {
			// No wire generated by this pass merged an island before failure;
			// changing route order cannot remove a static body/pin obstruction.
			return lastErr
		}
		if !routing.beginReroute() {
			return lastErr
		}
		var conflict *schematicRouteConflict
		if errors.As(err, &conflict) && conflict.net != "" {
			preferred = schematicRerouteOrder(policies, conflict.net, round)
		} else {
			preferred = schematicRerouteOrder(policies, "", round)
		}
	}
}

func schematicRerouteOrder(policies map[string]string, failed string, round int) []string {
	var nets []string
	for net, policy := range policies {
		if policy == "direct" || policy == "module_port" {
			nets = append(nets, net)
		}
	}
	sort.Slice(nets, func(i, j int) bool {
		a, b := libNetPriority(policies[nets[i]]), libNetPriority(policies[nets[j]])
		if a != b {
			return a < b
		}
		a, b = libSignalPolicyPriority(policies[nets[i]]), libSignalPolicyPriority(policies[nets[j]])
		if a != b {
			return a < b
		}
		return nets[i] < nets[j]
	})
	if len(nets) == 0 {
		return nil
	}
	ordered := make([]string, 0, len(nets))
	if failed != "" {
		ordered = append(ordered, failed)
	}
	shift := round % len(nets)
	for i := range nets {
		net := nets[(i+shift)%len(nets)]
		if net != failed {
			ordered = append(ordered, net)
		}
	}
	return ordered
}

func libJoinNetsPass(p *powerLayoutPlan, policies map[string]string, railsOnly, joinPorts bool, routing *schematicRoutingContext, preferred []string, preferExternal bool) error {
	rank := map[string]int{}
	namingWitnesses := libRouteNamingWitnesses{}
	for i, net := range preferred {
		rank[net] = i + 1
	}
	for {
		islands := libIslands(p)
		type edge struct {
			a, b          powerLayoutPin
			aIsland       int
			bIsland       int
			length        float64
			sameComponent bool
			sameSide      bool
		}
		edges := []edge{}
		addEdge := func(a, b powerLayoutPin, ai, bi int) {
			if a.X == b.X && a.Y == b.Y {
				return
			}
			for _, existing := range edges {
				if existing.a.X == a.X && existing.a.Y == a.Y && existing.b.X == b.X && existing.b.Y == b.Y && existing.a.Net == a.Net && existing.aIsland == ai && existing.bIsland == bi {
					return
				}
			}
			aOwner, bOwner := libExactPinOwner(p, a), libExactPinOwner(p, b)
			sameComponent := aOwner != "" && aOwner == bOwner
			sameSide := false
			if sameComponent {
				if owner := libPlacementByDesignator(p, aOwner); owner != nil {
					aSide, aErr := libPinSide(a, owner.BBox)
					bSide, bErr := libPinSide(b, owner.BBox)
					sameSide = aErr == nil && bErr == nil && aSide == bSide
				}
			}
			edges = append(edges, edge{a: a, b: b, aIsland: ai, bIsland: bi, length: math.Abs(a.X-b.X) + math.Abs(a.Y-b.Y), sameComponent: sameComponent, sameSide: sameSide})
		}
		for i, a := range islands {
			if !joinPorts && policies[a.net] == "module_port" {
				continue
			}
			isRail := libNetPriority(policies[a.net]) < 2
			if isRail != railsOnly {
				continue
			}
			for bi, b := range islands[:i] {
				if a.net == b.net {
					if routing != nil && routing.requiredJoin != nil && !routing.requiredJoin.matchesIslands(p, a, b) {
						continue
					}
					for _, x := range a.pins {
						for _, y := range b.pins {
							length := math.Abs(x.X-y.X) + math.Abs(x.Y-y.Y)
							if !railsOnly || length <= 80 {
								addEdge(x, y, i, bi)
							}
						}
						// A physical tree is a valid routing target in its own right.
						// Restricting joins to another component pin can make a legal
						// endpoint/T junction unreachable behind dense multi-pin symbols.
						// Candidate taps are derived only from existing same-net segments;
						// the post-add island-count check below proves that a real tree was
						// joined instead of accepting a geometric near miss or an X crossing.
						if !railsOnly {
							for _, x := range a.pins {
								for _, tap := range libSameNetWireTapPins(p, x) {
									addEdge(x, tap, i, bi)
								}
							}
							for _, y := range b.pins {
								for _, tap := range libSameNetWireTapPins(p, y) {
									addEdge(y, tap, bi, i)
								}
							}
						}
					}
				}
			}
		}
		if len(edges) == 0 {
			return nil
		}
		sort.SliceStable(edges, func(i, j int) bool {
			ri, rj := rank[edges[i].a.Net], rank[edges[j].a.Net]
			if ri != rj {
				if ri == 0 {
					return false
				}
				if rj == 0 {
					return true
				}
				return ri < rj
			}
			a, b := libNetPriority(policies[edges[i].a.Net]), libNetPriority(policies[edges[j].a.Net])
			if a != b {
				return a < b
			}
			a, b = libSignalPolicyPriority(policies[edges[i].a.Net]), libSignalPolicyPriority(policies[edges[j].a.Net])
			if a != b {
				return a < b
			}
			// A reroute changes the spanning-tree topology as well as net order.
			// Dense interleaved connector pins sometimes cannot form a local bus
			// without touching a foreign mandatory exit; connect their external
			// island first, then grow the remaining same-side pin into that tree.
			if preferExternal && edges[i].sameComponent != edges[j].sameComponent {
				return !edges[i].sameComponent
			}
			// Same-net pins interleaved on one symbol side need a shared fanout
			// bus before an external tree claims one pin's escape corridor.
			if edges[i].sameSide != edges[j].sameSide {
				return edges[i].sameSide
			}
			// Equivalent pins on one symbol are normally best joined through the
			// surrounding real net tree. Routing a short external loop around the
			// same body first can fence off every later cross-component branch.
			if edges[i].sameComponent != edges[j].sameComponent {
				return !edges[i].sameComponent
			}
			if edges[i].sameComponent && edges[i].sameSide && edges[j].sameSide && edges[i].length != edges[j].length {
				// Interleaved fanout uses nested trunks. Reserve the outer span
				// first; choosing the shortest inner pair first can surround a
				// farther pin with foreign endpoint/T obligations.
				return edges[i].length > edges[j].length
			}
			return edges[i].length < edges[j].length
		})
		joined := false
		lockedNet := ""
		if len(edges) > 0 && policies[edges[0].a.Net] == "direct" {
			// A mandatory net is an atomic routing obligation. Once selected,
			// finish its physical tree (or fail and reroute the whole forest)
			// before another net can consume the remaining corridor.
			lockedNet = edges[0].a.Net
		}
		mazeTried := map[[2]int]bool{}
		var lastMazeErr error
		var failedMazeEdge *edge
		var failedRouting *schematicRoutingFailure
		sealedBlockers := map[string]bool{}
		for _, e := range edges {
			if lockedNet != "" && e.a.Net != lockedNet {
				continue
			}
			routes := libRoutes(e.a, e.b, p)
			if !railsOnly {
				routes = append(routes, libDetourRoutes(e.a, e.b, p)...)
			}
			sealedPair := false
			for _, route := range routes {
				trial := *p
				trial.Wires = libAppendRoute(p.Wires, route)
				if validateLibGeometry(&trial) == nil && libPinsShareIsland(&trial, islands[e.aIsland].pins[0], islands[e.bIsland].pins[0]) {
					if !libIslandMergeCanContinue(&trial, islands[e.aIsland].pins[0], islands[e.bIsland].pins[0]) {
						sealedPair = true
						continue
					}
					if libDirectRouteKeepsFrontiers(p, &trial, policies) {
						keepsNaming, updated := libRouteKeepsCompletedNamingFrontiers(p, &trial, policies, routing, namingWitnesses)
						if !keepsNaming {
							continue
						}
						*p = trial
						namingWitnesses = updated
						joined = true
						break
					}
				}
			}
			pair := [2]int{e.aIsland, e.bIsland}
			if pair[0] > pair[1] {
				pair[0], pair[1] = pair[1], pair[0]
			}
			if sealedPair && !joined {
				lastMazeErr = fmt.Errorf("joining islands %s and %s leaves no legal frontier for the remaining %s islands", libIslandStableID(islands[e.aIsland]), libIslandStableID(islands[e.bIsland]), e.a.Net)
				copy := e
				failedMazeEdge = &copy
				for _, pin := range []powerLayoutPin{e.a, e.b} {
					if owner := libExactPinOwner(p, pin); owner != "" {
						sealedBlockers[owner] = true
						if routing != nil {
							routing.addRejection(SchematicRoutingRejection{ComponentID: routing.components[owner], ComponentRef: owner, Net: e.a.Net, Reason: "future-frontier-sealed"})
						}
					}
				}
				// A* would rediscover the same electrically merged short path. The
				// remedy is a different island pair or placement, not more nodes.
				continue
			}
			// module_port explicitly permits separately named physical islands at
			// a zone boundary. Keep cheap local joins, but reserve obstacle-search
			// budget for direct nets whose islands must physically merge.
			if !joined && !railsOnly && routing != nil && policies[e.a.Net] == "direct" && !mazeTried[pair] {
				mazeTried[pair] = true
				var acceptedWitnesses libRouteNamingWitnesses
				route, err := libMazeRouteAccepted(p, islands[e.aIsland], islands[e.bIsland], routing, func(trial *powerLayoutPlan) bool {
					if !libIslandMergeCanContinue(trial, islands[e.aIsland].pins[0], islands[e.bIsland].pins[0]) || !libDirectRouteKeepsFrontiers(p, trial, policies) {
						return false
					}
					keepsNaming, updated := libRouteKeepsCompletedNamingFrontiers(p, trial, policies, routing, namingWitnesses)
					if keepsNaming {
						acceptedWitnesses = updated
					}
					return keepsNaming
				})
				if err == nil {
					trial := *p
					trial.Wires = libAppendRoute(p.Wires, route)
					if validateLibGeometry(&trial) == nil && acceptedWitnesses != nil {
						*p, joined = trial, true
						namingWitnesses = acceptedWitnesses
					}
				} else {
					lastMazeErr = err
					copy := e
					failedMazeEdge = &copy
					var failure *schematicRoutingFailure
					if errors.As(err, &failure) {
						failedRouting = failure
					}
					if errors.As(err, &failure) && failure.Kind == "expanded-node-budget-exhausted" {
						return err
					}
				}
			}
			if joined {
				break
			}
		}
		if !joined {
			if railsOnly {
				return nil
			}
			orderedFailures := edges
			if failedMazeEdge != nil {
				orderedFailures = append([]edge{*failedMazeEdge}, edges...)
			}
			for _, e := range orderedFailures {
				if policies[e.a.Net] == "direct" {
					conflict := &schematicRouteConflict{net: e.a.Net, sourceIsland: libIslandStableID(islands[e.aIsland]), targetIsland: libIslandStableID(islands[e.bIsland]), endpointOwners: map[string]bool{}, blockers: map[string]bool{}, ownersComplete: true, cause: lastMazeErr}
					for _, index := range []int{e.aIsland, e.bIsland} {
						for _, pin := range islands[index].pins {
							if ref := libExactPinOwner(p, pin); ref != "" {
								conflict.endpointOwners[ref] = true
							} else {
								conflict.ownersComplete = false
							}
						}
					}
					if len(conflict.endpointOwners) == 0 {
						conflict.ownersComplete = false
					}
					for ref := range sealedBlockers {
						conflict.blockers[ref] = true
					}
					if failedRouting != nil {
						conflict.ownersComplete = conflict.ownersComplete && failedRouting.OwnersComplete
						for _, evidence := range failedRouting.BlockingEvidence {
							if evidence.ComponentRef != "" && evidence.ComponentID != "" {
								conflict.blockers[evidence.ComponentRef] = true
							}
						}
					}
					// This diagnostic runs only after all real route candidates
					// failed. It changes search order, never electrical validity.
					for _, route := range append(libRoutes(e.a, e.b, p), libDetourRoutes(e.a, e.b, p)...) {
						for _, c := range p.Placements {
							probe := powerLayoutPlan{Placements: []powerLayoutPlacement{c}, Wires: route}
							if validateLibGeometry(&probe) != nil {
								conflict.blockers[c.Designator] = true
							}
						}
						// A body can be outside the corridor while one of its wires
						// blocks it. Conservatively include every member of that net;
						// missing ownership disables strong conflict pruning entirely.
						for _, existing := range p.Wires {
							if existing.Net == e.a.Net || !libRoutesIntersect(route, existing) {
								continue
							}
							owned := false
							for _, c := range p.Placements {
								for _, q := range c.Pins {
									if q.Net == existing.Net {
										conflict.blockers[c.Designator], owned = true, true
									}
								}
							}
							conflict.ownersComplete = conflict.ownersComplete && owned
						}
					}
					return conflict
				}
			}
			// An explicitly cross-zone net may retain separately named trees.
			// libNameIslands must still name every island; direct nets never fall back.
			return nil
		}
	}
}

func libExactPinOwner(p *powerLayoutPlan, pin powerLayoutPin) string {
	for _, component := range p.Placements {
		for _, candidate := range component.Pins {
			if candidate.Net == pin.Net && candidate.Number == pin.Number && math.Abs(candidate.X-pin.X) <= 1e-6 && math.Abs(candidate.Y-pin.Y) <= 1e-6 {
				return component.Designator
			}
		}
	}
	return ""
}

func libPlacementByDesignator(p *powerLayoutPlan, ref string) *powerLayoutPlacement {
	for i := range p.Placements {
		if p.Placements[i].Designator == ref {
			return &p.Placements[i]
		}
	}
	return nil
}

func libSameNetWireTapPins(p *powerLayoutPlan, pin powerLayoutPin) []powerLayoutPin {
	seen := map[[2]float64]bool{}
	var taps []powerLayoutPin
	add := func(point [2]float64) {
		if point == [2]float64{pin.X, pin.Y} || seen[point] || !plGrid(point[0]) || !plGrid(point[1]) {
			return
		}
		seen[point] = true
		taps = append(taps, powerLayoutPin{Net: pin.Net, X: point[0], Y: point[1]})
	}
	for _, wire := range p.Wires {
		if wire.Net != pin.Net {
			continue
		}
		for i := 1; i < len(wire.Points); i++ {
			a, b := wire.Points[i-1], wire.Points[i]
			add(a)
			add(b)
			if a[0] == b[0] {
				y := math.Max(math.Min(pin.Y, math.Max(a[1], b[1])), math.Min(a[1], b[1]))
				add([2]float64{a[0], y})
			} else if a[1] == b[1] {
				x := math.Max(math.Min(pin.X, math.Max(a[0], b[0])), math.Min(a[0], b[0]))
				add([2]float64{x, a[1]})
			}
		}
	}
	sort.SliceStable(taps, func(i, j int) bool {
		a := math.Abs(pin.X-taps[i].X) + math.Abs(pin.Y-taps[i].Y)
		b := math.Abs(pin.X-taps[j].X) + math.Abs(pin.Y-taps[j].Y)
		if a != b {
			return a < b
		}
		if taps[i].Y != taps[j].Y {
			return taps[i].Y < taps[j].Y
		}
		return taps[i].X < taps[j].X
	})
	return taps
}

func libRoutesIntersect(route []powerLayoutWire, wire powerLayoutWire) bool {
	for _, part := range route {
		for i := 1; i < len(part.Points); i++ {
			for j := 1; j < len(wire.Points); j++ {
				if plSegmentsContact(part.Points[i-1], part.Points[i], wire.Points[j-1], wire.Points[j]) {
					return true
				}
			}
		}
	}
	return false
}
