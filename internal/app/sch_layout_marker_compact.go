package app

import "math"

// Bounded final refinement of signal naming points on their existing wire tree.
// Keep placements/topology and rail reservations fixed. A modest length tradeoff
// is allowed only for a meaningful envelope saving; never just balance short/long
// leads by lengthening the short ones. Exhaustion retains the valid incumbent.
func libCompactMarkerEnvelope(p *powerLayoutPlan, budget *int) {
	initial := libCandidateScore(p)
	lengthCap := initial[0] * 1.2
	remaining := 2048
	islands := libIslands(p)
	for i, original := range p.Flags {
		if !isNetPortKind(original.Kind) {
			continue
		}
		// Keep explicitly chosen symmetric midpoint branches intact.
		var pins []powerLayoutPin
		for _, island := range islands {
			if island.net != original.Net {
				continue
			}
			for _, q := range island.pins {
				if q.X == original.PinX && q.Y == original.PinY {
					pins = island.pins
					break
				}
			}
		}
		if len(pins) == 0 {
			continue
		}
		base := *p
		base.Flags = append(append([]powerLayoutFlag{}, p.Flags[:i]...), p.Flags[i+1:]...)
		segments, e := schTerminalSegments(&base)
		if e != nil {
			return
		}
		best := *p
		score := libCandidateScore(&best)
		requiredArea := score[3] * .95
		cap := math.Min(libMarkerOffsetCap(p, original.Net, original.Kind), original.Offset+lengthCap-score[0])
		for offset := 10.; offset <= cap; offset += 5 {
			for _, q := range pins {
				for _, d := range []string{"right", "left", "up", "down"} {
					if remaining <= 0 || *budget <= 0 {
						*p = best
						return
					}
					remaining--
					*budget -= 1
					f := powerLayoutFlag{Net: original.Net, Kind: original.Kind, PinX: q.X, PinY: q.Y, Direction: d, Offset: offset}
					if libMarkerRetraces(f, segments) || schTerminalCandidate(&base, f, segments) != nil {
						continue
					}
					trial := *p
					trial.Flags = append([]powerLayoutFlag{}, p.Flags...)
					trial.Flags[i] = f
					s := libCandidateScore(&trial)
					if s[0] > lengthCap || s[3] > requiredArea || (s[3] > score[3] || s[3] == score[3] && s[0] >= score[0]) {
						continue
					}
					if validateLibGeometry(&trial) != nil || validateSchCompositionNets(&trial) != nil {
						continue
					}
					best, score = trial, s
				}
			}
		}
		*p = best
	}
}
