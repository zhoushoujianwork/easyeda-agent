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
	current.Flags = nil // Marker reservations are recalculated for each candidate.
	var lastErr error
	// Search complete distance shells: don't accept the first legal coordinate.
	// All candidates in the first feasible shell compete on the full objective.
	for cost := 5.0; cost <= 600; cost += 5 {
		var best *powerLayoutPlan
		var bestRouting []powerLayoutWire
		for lateral := 0.0; lateral <= math.Min(200, cost-5); lateral += 5 {
			distance := cost - lateral
			if distance > 400 {
				continue
			}
			for _, sign := range []float64{1, -1} {
				if lateral == 0 && sign < 0 {
					continue
				}
				if *budget <= 0 {
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
					if lastErr = libNameIslands(&candidate, policies, budget); lastErr != nil {
						continue
					}
					if best == nil || libAlignedCandidateLess(&candidate, best, pair) {
						best = &candidate
						bestRouting = routing
					}
				}
			}
		}
		if best != nil {
			// Naming is a feasibility probe during placement. Its escape wires
			// must not survive after their temporary markers are discarded.
			best.Wires = bestRouting
			best.Flags = nil
			return best, nil
		}
	}
	return nil, lastErr
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
					return fmt.Errorf("cannot route direct net %s between measured pins without crossing obstacles; revise attachment or measured pose", e.a.Net)
				}
			}
			// An explicitly cross-zone net may retain separately named trees.
			// libNameIslands must still name every island; direct nets never fall back.
			return nil
		}
	}
}
