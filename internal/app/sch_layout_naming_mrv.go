package app

import (
	"fmt"
	"math"
)

// libNameIslandsMRV explores only actual complete marker witnesses. It replays
// their naming geometry through every terminal/geometry gate. Canonical route
// normalization may reorder or split base wires; witness suffixes are not a
// reliable delta, so only differing actual segments are merged onto the intact
// current tree. Bounded pools cannot prove closure or prune a placement.
func libNameIslandsMRV(base *powerLayoutPlan, policies map[string]string, islands []libIsland, budget *int) error {
	type option struct {
		wires []powerLayoutWire
		flags []powerLayoutFlag
	}
	type domain struct {
		island  libIsland
		options []option
		degree  int
	}
	wireKey := func(w powerLayoutWire) string {
		a, b := w.Points[0], w.Points[1]
		if a[0] > b[0] || (a[0] == b[0] && a[1] > b[1]) {
			a, b = b, a
		}
		return fmt.Sprintf("%q:%g,%g:%g,%g", w.Net, a[0], a[1], b[0], b[1])
	}
	baseWires := map[string]bool{}
	for _, w := range base.Wires {
		if len(w.Points) != 2 {
			return fmt.Errorf("bounded MRV naming requires normalized two-point segments")
		}
		baseWires[wireKey(w)] = true
	}
	replay := func(current *powerLayoutPlan, proposal option) *powerLayoutPlan {
		result := *current
		result.Wires = libAppendRoute(current.Wires, proposal.wires)
		result.Flags = append([]powerLayoutFlag(nil), current.Flags...)
		if validateLibGeometry(&result) != nil {
			return nil
		}
		for _, f := range proposal.flags {
			segments, err := schTerminalSegments(&result)
			if err != nil || libMarkerRetraces(f, segments) || schTerminalCandidate(&result, f, segments) != nil {
				return nil
			}
			result.Flags = append(result.Flags, f)
		}
		if validateLibGeometry(&result) != nil {
			return nil
		}
		return &result
	}
	domains := make([]domain, len(islands))
	for i, island := range islands {
		domains[i].island = island
		kind := "net_port_bi"
		switch policies[island.net] {
		case "local_ground":
			kind = "ground"
		case "local_power":
			kind = "power"
		}
		libMRVNamingOptions(base, island, kind, func(candidate *powerLayoutPlan) bool {
			proposal := option{flags: append([]powerLayoutFlag(nil), candidate.Flags[len(base.Flags):]...)}
			for _, w := range candidate.Wires {
				if !baseWires[wireKey(w)] {
					proposal.wires = append(proposal.wires, w)
				}
			}
			domains[i].options = append(domains[i].options, proposal)
			return false
		}, budget)
		if *budget <= 0 {
			return errLibLayoutBudget
		}
		if len(domains[i].options) == 0 {
			return fmt.Errorf("bounded MRV naming pool has no candidates for %s", island.net)
		}
	}
	// Conflict degree is computed from complete witness pairs, not distances.
	for i := range domains {
		for j := 0; j < i; j++ {
			conflict := false
			for ai, a := range domains[i].options {
				if ai >= 4 {
					break
				}
				if *budget <= 0 {
					return errLibLayoutBudget
				}
				*budget--
				first := replay(base, a)
				if first == nil {
					continue
				}
				for bi, b := range domains[j].options {
					if bi >= 4 {
						break
					}
					if *budget <= 0 {
						return errLibLayoutBudget
					}
					*budget--
					if replay(first, b) == nil {
						conflict = true
						break
					}
				}
				if conflict {
					break
				}
			}
			if conflict {
				domains[i].degree++
				domains[j].degree++
			}
		}
	}
	visited := 0
	var visit func(*powerLayoutPlan, []int) *powerLayoutPlan
	visit = func(current *powerLayoutPlan, remaining []int) *powerLayoutPlan {
		visited++
		if len(remaining) == 0 {
			if validateLibGeometry(current) != nil || validateSchCompositionNets(current) != nil {
				return nil
			}
			return current
		}
		selected := -1
		var selectedLegal []*powerLayoutPlan
		for _, idx := range remaining {
			var legal []*powerLayoutPlan
			for _, proposal := range domains[idx].options {
				if *budget <= 0 {
					return nil
				}
				*budget--
				if candidate := replay(current, proposal); candidate != nil {
					legal = append(legal, candidate)
				}
			}
			if len(legal) == 0 {
				return nil
			}
			if selected < 0 || len(legal) < len(selectedLegal) || (len(legal) == len(selectedLegal) && domains[idx].degree > domains[selected].degree) {
				selected = idx
				selectedLegal = legal
			}
		}
		next := make([]int, 0, len(remaining)-1)
		for _, idx := range remaining {
			if idx != selected {
				next = append(next, idx)
			}
		}
		for _, candidate := range selectedLegal {
			if solved := visit(candidate, next); solved != nil {
				return solved
			}
			if *budget <= 0 {
				return nil
			}
		}
		return nil
	}
	remaining := make([]int, len(domains))
	for i := range remaining {
		remaining[i] = i
	}
	if solved := visit(base, remaining); solved != nil {
		*base = *solved
		return nil
	}
	if *budget <= 0 {
		return errLibLayoutBudget
	}
	return fmt.Errorf("bounded MRV naming exhausted candidate pools after %d nodes", visited)
}

// Sample complete witnesses across marker-anchor grid bands rather than filling
// a pool with eight adjacent five-raw offsets on the same side. Constructors,
// minimum lead, caps and guards are unchanged; sampling only guides bounded
// search and carries no infeasibility inference.
func libMRVNamingOptions(current *powerLayoutPlan, island libIsland, kind string, visit func(*powerLayoutPlan) bool, budget *int) {
	total := 0
	stopped := false
	seen := map[string]bool{}
	familyCount := 0
	accept := func(candidate *powerLayoutPlan) bool {
		if len(candidate.Flags) <= len(current.Flags) {
			return false
		}
		f := candidate.Flags[len(candidate.Flags)-1]
		x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
		key := fmt.Sprintf("%s:%g,%g", f.Direction, math.Floor(x/20), math.Floor(y/20))
		if seen[key] {
			return false
		}
		seen[key] = true
		total++
		familyCount++
		stopped = visit(candidate) || total >= 32 || *budget <= 0
		return stopped || familyCount >= 8
	}
	libVisitMidpointMarker(current, island, kind, accept, budget)
	if stopped || *budget <= 0 {
		return
	}
	familyCount = 0
	libVisitWireTreeMarker(current, island, kind, accept, budget)
	if stopped || *budget <= 0 {
		return
	}
	for _, pin := range island.pins {
		familyCount = 0
		var body layoutBBox
		for _, placement := range current.Placements {
			for _, p := range placement.Pins {
				if libSamePhysicalPin(p, pin) {
					body = placement.BBox
				}
			}
		}
		side, err := libPinSide(pin, body)
		if err != nil {
			continue
		}
		libVisitMarkerAt(current, pin, kind, []string{side}, libMarkerOffsetCap(current, pin.Net, kind), accept, budget)
		if stopped || *budget <= 0 {
			return
		}
		familyCount = 0
		libVisitMarkerLimited(current, pin, kind, 1, accept, budget)
		if stopped || *budget <= 0 {
			return
		}
	}
}
