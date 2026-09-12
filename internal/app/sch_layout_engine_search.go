package app

import (
	"fmt"
	"math"
	"sort"
)

type libAttachmentPair struct {
	host, own powerLayoutPin
	side      string
	rank      int
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
	current.Flags = nil // Full marker placement is deferred to the final gate.
	var lastErr error
	// Search complete distance shells: don't accept the first legal coordinate.
	// All candidates in the first feasible shell compete on the full objective.
	start := 5.0
	if len(cursor) > 0 && *cursor[0] > start {
		start = *cursor[0]
	}
	for cost := start; cost <= 600; cost += 5 {
		var best *powerLayoutPlan
		var bestRouting []powerLayoutWire
		var bestPair libAttachmentPair
		finishBest := func() *powerLayoutPlan {
			if best != nil {
				if len(cursor) > 0 {
					*cursor[0] = cost
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
					if *budget <= 0 {
						if best != nil {
							return finishBest(), nil
						}
						return nil, errLibLayoutBudget
					}
					*budget -= 1
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
					trial := current
					trial.Placements = append(append([]powerLayoutPlacement{}, current.Placements...), c)
					if lastErr = validateLibGeometry(&trial); lastErr != nil {
						continue
					}
					q, _ := libPin(c, pair.own.Number)
					// A facing two-terminal signal branch needs an exact grid midpoint
					// for symmetric tapping; reserve it during placement, not by bending
					// or shifting a finished wire in the renderer.
					if libNetPriority(policies[q.Net]) == 2 && libFacingTwoTerminalPins(&trial, pair.host, q) && (!plGrid((pair.host.X+q.X)/2) || !plGrid((pair.host.Y+q.Y)/2)) {
						continue
					}
					for _, route := range libRoutes(pair.host, q) {
						candidate := trial
						candidate.Wires = libAppendRoute(current.Wires, route)
						if lastErr = validateLibGeometry(&candidate); lastErr != nil {
							continue
						}
						if lastErr = libJoinNearbyRails(&candidate, policies); lastErr != nil {
							continue
						}
						routing := candidate.Wires
						// This is a geometry/routing checkpoint, not a completed
						// schematic. Naming every unchanged core pin here made a
						// dense core cost O(proposals * all pins * escape routes).
						// The full naming/electrical gate runs at the final checkpoint;
						// any failure rolls back these provisional placements.
						if best == nil || libPairCandidateLess(&candidate, pair, best, bestPair) {
							best = &candidate
							bestRouting = routing
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
	return nil, lastErr
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
	net  string
	pins []powerLayoutPin
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
			if w.Net == v.Net && plSegmentsMeet(w.Points[0], w.Points[1], v.Points[0], v.Points[1]) {
				parent[root(i)] = root(j)
			}
		}
	}
	indices := map[string]int{}
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
				islands = append(islands, libIsland{net: q.Net})
			}
			islands[index].pins = append(islands[index].pins, q)
		}
	}
	return islands
}

func libNameIslands(p *powerLayoutPlan, policies map[string]string, budget ...*int) error {
	base := *p
	base.Flags = nil
	var lastErr error
	for order := 0; order < 3; order++ {
		trial := base
		islands := libIslands(&trial)
		sort.SliceStable(islands, func(i, j int) bool {
			if order == 0 {
				return libNetPriority(policies[islands[i].net]) < libNetPriority(policies[islands[j].net])
			}
			a, b := islands[i].pins[0], islands[j].pins[0]
			if a.Y != b.Y {
				if order == 1 {
					return a.Y > b.Y
				}
				return a.Y < b.Y
			}
			return a.X < b.X
		})
		if lastErr = libNameOrderedIslands(&trial, policies, islands, budget...); lastErr == nil {
			*p = trial
			return nil
		}
		if len(budget) > 0 && *budget[0] <= 0 {
			return errLibLayoutBudget
		}
	}
	return lastErr
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
			return fmt.Errorf("net %s has no safe naming lead for island at pin %s (%g,%g)", island.net, island.pins[0].Number, island.pins[0].X, island.pins[0].Y)
		}
		*p = *best
	}
	return nil
}

func libJoinDirectNets(p *powerLayoutPlan, policies map[string]string) error {
	return libJoinNets(p, policies, false)
}

func libJoinNearbyRails(p *powerLayoutPlan, policies map[string]string) error {
	return libJoinNets(p, policies, true)
}

func libJoinNets(p *powerLayoutPlan, policies map[string]string, railsOnly bool) error {
	return libJoinNetsMode(p, policies, railsOnly, true)
}

func libJoinNetsMode(p *powerLayoutPlan, policies map[string]string, railsOnly, joinPorts bool) error {
	for {
		islands := libIslands(p)
		type edge struct {
			a, b   powerLayoutPin
			length float64
		}
		edges := []edge{}
		for i, a := range islands {
			if !joinPorts && policies[a.net] == "module_port" {
				continue
			}
			isRail := libNetPriority(policies[a.net]) < 2
			if isRail != railsOnly {
				continue
			}
			for _, b := range islands[:i] {
				if a.net == b.net {
					for _, x := range a.pins {
						for _, y := range b.pins {
							length := math.Abs(x.X-y.X) + math.Abs(x.Y-y.Y)
							if !railsOnly || length <= 80 {
								edges = append(edges, edge{x, y, length})
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
			a, b := libNetPriority(policies[edges[i].a.Net]), libNetPriority(policies[edges[j].a.Net])
			if a != b {
				return a < b
			}
			return edges[i].length < edges[j].length
		})
		joined := false
		for _, e := range edges {
			routes := libRoutes(e.a, e.b)
			if !railsOnly {
				routes = append(routes, libDetourRoutes(e.a, e.b)...)
			}
			for _, route := range routes {
				trial := *p
				trial.Wires = libAppendRoute(p.Wires, route)
				if validateLibGeometry(&trial) == nil && len(libIslands(&trial)) < len(islands) {
					*p = trial
					joined = true
					break
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
			for _, e := range edges {
				if policies[e.a.Net] == "direct" {
					conflict := &schematicRouteConflict{net: e.a.Net, blockers: map[string]bool{}, ownersComplete: true}
					// This diagnostic runs only after all real route candidates
					// failed. It changes search order, never electrical validity.
					for _, route := range append(libRoutes(e.a, e.b), libDetourRoutes(e.a, e.b)...) {
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

func libRoutesIntersect(route []powerLayoutWire, wire powerLayoutWire) bool {
	for _, part := range route {
		for i := 1; i < len(part.Points); i++ {
			for j := 1; j < len(wire.Points); j++ {
				if plSegmentsMeet(part.Points[i-1], part.Points[i], wire.Points[j-1], wire.Points[j]) {
					return true
				}
			}
		}
	}
	return false
}
