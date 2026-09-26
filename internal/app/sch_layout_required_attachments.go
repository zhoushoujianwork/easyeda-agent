package app

import "fmt"

// Explicit attachment pin pairs are physical obligations. Regenerating the
// forest must preserve them even when their named power rail permits separate
// islands elsewhere in the zone. References survive every permitted pose.
type schematicRequiredConnection struct {
	hostRef, hostPin, ownRef, ownPin, net string
}

func schematicRequiredAttachments(input SchematicLayoutInput) []schematicRequiredConnection {
	components := map[string]powerLayoutPlacement{}
	for _, c := range input.Components {
		components[c.ID] = c.Measurement
	}
	var pairs []schematicRequiredConnection
	for _, h := range input.Attachments {
		if h.AttachTo == nil || h.PinNumber == "" {
			continue
		}
		host, own := components[h.AttachTo.ComponentID], components[h.ComponentID]
		a, aOK := libPin(host, h.AttachTo.PinNumber)
		b, bOK := libPin(own, h.PinNumber)
		ground := input.NetPolicies[a.Net] == "local_ground" || (input.NetPolicies[a.Net] != "local_power" && schematicPeripheralNetRole(a.Net) == "ground")
		if !aOK || !bOK || a.Net == "" || a.Net != b.Net || ground {
			continue
		}
		pairs = append(pairs, schematicRequiredConnection{host.Designator, a.Number, own.Designator, b.Number, a.Net})
	}
	return pairs
}

func (pair schematicRequiredConnection) pins(p *powerLayoutPlan) (powerLayoutPin, powerLayoutPin, bool) {
	a, b := libPlacementByDesignator(p, pair.hostRef), libPlacementByDesignator(p, pair.ownRef)
	if a == nil || b == nil {
		return powerLayoutPin{}, powerLayoutPin{}, false
	}
	x, xOK := libPin(*a, pair.hostPin)
	y, yOK := libPin(*b, pair.ownPin)
	return x, y, xOK && yOK && x.Net == pair.net && y.Net == pair.net
}

func (pair schematicRequiredConnection) matchesIslands(p *powerLayoutPlan, a, b libIsland) bool {
	x, y, ok := pair.pins(p)
	return ok && ((libIslandContainsPhysicalPin(a, x) && libIslandContainsPhysicalPin(b, y)) ||
		(libIslandContainsPhysicalPin(a, y) && libIslandContainsPhysicalPin(b, x)))
}

func libJoinRequiredAttachments(p *powerLayoutPlan, policies map[string]string, routing *schematicRoutingContext) error {
	if routing == nil {
		return nil
	}
	oldPolicies, oldJoin := routing.policies, routing.requiredJoin
	defer func() { routing.policies, routing.requiredJoin = oldPolicies, oldJoin }()
	for _, pair := range routing.requiredConnections {
		a, b, ok := pair.pins(p)
		if !ok {
			return fmt.Errorf("required attachment %s.%s -> %s.%s no longer has its measured net", pair.ownRef, pair.ownPin, pair.hostRef, pair.hostPin)
		}
		if libPinsShareIsland(p, a, b) {
			continue
		}
		// Reuse the mandatory route kernel for only these two physical islands.
		// Do not promote all other same-name rail islands or alter source policy.
		effective := make(map[string]string, len(policies))
		for net, policy := range policies {
			effective[net] = policy
		}
		effective[pair.net] = "direct"
		routing.policies, routing.requiredJoin = effective, &pair
		if err := libJoinNetsMode(p, effective, false, false, routing); err != nil {
			return err
		}
		if !libPinsShareIsland(p, a, b) {
			return fmt.Errorf("required attachment %s.%s -> %s.%s has no physical wire path", pair.ownRef, pair.ownPin, pair.hostRef, pair.hostPin)
		}
	}
	return nil
}
