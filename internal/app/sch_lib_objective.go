package app

import "math"

func libNetPriority(policy string) int {
	switch policy {
	case "local_ground":
		return 0
	case "local_power":
		return 1
	default:
		return 2
	}
}

func libPeripheralPriority(c powerLayoutPlacement, policies map[string]string) int {
	connected := false
	for _, p := range c.Pins {
		if p.Net == "" {
			continue
		}
		connected = true
		if libNetPriority(policies[p.Net]) == 2 {
			return 1
		}
	}
	if connected {
		return 0
	}
	return 2
}

// Lexicographic objective, with no part-name or reference-design coordinates.
// Evaluated only for fully collision-checked candidates at equal attachment cost.
func libCandidateScore(p *powerLayoutPlan) [4]float64 {
	var s [4]float64
	for _, w := range p.Wires {
		for i := 1; i < len(w.Points); i++ {
			s[0] += math.Abs(w.Points[i][0]-w.Points[i-1][0]) + math.Abs(w.Points[i][1]-w.Points[i-1][1])
			s[1]++
		}
	}
	for _, f := range p.Flags {
		s[0] += f.Offset
		s[1]++
	}
	// Align the new peripheral with its nearest existing component axis.
	if n := len(p.Placements); n > 1 {
		c := p.Placements[n-1]
		s[2] = math.Inf(1)
		for _, other := range p.Placements[:n-1] {
			s[2] = math.Min(s[2], math.Min(math.Abs(c.X-other.X), math.Abs(c.Y-other.Y)))
		}
	}
	boxes := powerLayoutContentObstacles(p)
	if len(boxes) > 0 {
		b := boxes[0]
		for _, a := range boxes[1:] {
			b.MinX = math.Min(b.MinX, a.MinX)
			b.MinY = math.Min(b.MinY, a.MinY)
			b.MaxX = math.Max(b.MaxX, a.MaxX)
			b.MaxY = math.Max(b.MaxY, a.MaxY)
		}
		s[3] = (b.MaxX - b.MinX) * (b.MaxY - b.MinY)
	}
	return s
}

func libCandidateLess(a, b *powerLayoutPlan) bool {
	x, y := libCandidateScore(a), libCandidateScore(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

// Compare attachment pin axes only within the same compactness shell.
func libAlignedCandidateLess(a, b *powerLayoutPlan, pair libAttachmentPair) bool {
	errorFor := func(p *powerLayoutPlan) float64 {
		q, ok := libPin(p.Placements[len(p.Placements)-1], pair.own.Number)
		if !ok {
			return math.Inf(1)
		}
		if pair.side == "left" || pair.side == "right" {
			return math.Abs(q.Y - pair.host.Y)
		}
		return math.Abs(q.X - pair.host.X)
	}
	x, y := errorFor(a), errorFor(b)
	if x != y {
		return x < y
	}
	// Parallel two-terminal peers should share either their column or their
	// terminal row. Use measured pin centers, not symbol anchor conventions.
	peerError := func(p *powerLayoutPlan) float64 {
		n := len(p.Placements)
		if n < 2 {
			return 0
		}
		c := p.Placements[n-1]
		if len(c.Pins) != 2 {
			return 0
		}
		vertical := c.Pins[0].X == c.Pins[1].X
		horizontal := c.Pins[0].Y == c.Pins[1].Y
		if !vertical && !horizontal {
			return 0
		}
		cx, cy := (c.Pins[0].X+c.Pins[1].X)/2, (c.Pins[0].Y+c.Pins[1].Y)/2
		best := math.Inf(1)
		for _, other := range p.Placements[:n-1] {
			if len(other.Pins) != 2 {
				continue
			}
			if vertical != (other.Pins[0].X == other.Pins[1].X) || horizontal != (other.Pins[0].Y == other.Pins[1].Y) {
				continue
			}
			shared := false
			for _, a := range c.Pins {
				for _, b := range other.Pins {
					shared = shared || (a.Net != "" && a.Net == b.Net)
				}
			}
			if !shared {
				continue
			}
			ox, oy := (other.Pins[0].X+other.Pins[1].X)/2, (other.Pins[0].Y+other.Pins[1].Y)/2
			best = math.Min(best, math.Min(math.Abs(cx-ox), math.Abs(cy-oy)))
		}
		if math.IsInf(best, 1) {
			return 0
		}
		return best
	}
	x, y = peerError(a), peerError(b)
	if x != y {
		return x < y
	}
	return libCandidateLess(a, b)
}
