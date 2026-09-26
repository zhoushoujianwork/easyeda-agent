package app

// crystal-guard is the first schema-v2 routed module.  It assembles X1 and its
// capacitors in local coordinates, locates that complete cluster against fixed
// MCU pads, then derives every copper/region/via object from the final geometry.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
)

func validateCrystalGuardSpec(mod pcbLayoutModuleSpec, members, all map[string]boardComp, snap *boardSnapshot) (*pcbCrystalGuardSpec, error) {
	g := mod.CrystalGuard
	if g == nil {
		return nil, fmt.Errorf("crystal-guard strategy requires crystalGuard")
	}
	if snap == nil || snap.Copper == nil {
		return nil, fmt.Errorf("crystal-guard requires a board from pcb dump --include-copper")
	}
	if snap.Rules == nil || snap.Outline == nil {
		return nil, fmt.Errorf("crystal-guard requires live rules and exact board outline in the snapshot")
	}
	for _, category := range []string{"routing", "vias", "pours", "poured", "regions", "fills"} {
		if snap.Copper.Availability[category] != "available" {
			return nil, fmt.Errorf("crystal-guard copper category %s is unknown", category)
		}
	}
	if snap.Copper.ArcsAvailable == nil || !*snap.Copper.ArcsAvailable {
		return nil, fmt.Errorf("crystal-guard cannot prove routing obstacles because arc readback is unavailable")
	}
	if mod.CopperPolicy != "module-owned" {
		return nil, fmt.Errorf("crystal-guard requires copperPolicy=module-owned")
	}
	if g.OwnerSide != "bottom" {
		return nil, fmt.Errorf("crystalGuard.ownerSide must be bottom in schema v2")
	}
	if _, ok := members[g.CrystalRef]; !ok || g.CrystalRef != mod.AnchorRef {
		return nil, fmt.Errorf("crystalGuard.crystalRef must name the module anchor")
	}
	owner, ok := all[g.OwnerRef]
	if !ok || owner.BBox == nil {
		return nil, fmt.Errorf("crystalGuard owner %q requires measured bbox geometry", g.OwnerRef)
	}
	if len(g.Ports) != 2 {
		return nil, fmt.Errorf("crystalGuard requires exactly two signal ports")
	}
	if strings.TrimSpace(g.GroundNet) == "" || len(g.CrystalGroundPads) == 0 || len(g.GroundAnchors) == 0 {
		return nil, fmt.Errorf("crystalGuard requires groundNet, crystalGroundPads, and groundAnchors")
	}
	if p := g.ProtectionSearch; p != nil {
		if !allFinite(p.StepMil, p.MaxDetourMil) || p.StepMil <= 0 || p.MaxDetourMil < p.StepMil || p.MaxDetourMil/p.StepMil > 100 {
			return nil, fmt.Errorf("crystalGuard.protectionSearch requires finite 0 < stepMil <= maxDetourMil and at most 100 steps")
		}
	}
	if p := g.ProtectionSearch; p != nil {
		if len(p.GuardOffsetsMil) > 101 {
			return nil, fmt.Errorf("too many guardOffsetsMil")
		}
		for _, offset := range p.GuardOffsetsMil {
			if !allFinite(offset) || offset < 0 || offset > p.MaxDetourMil {
				return nil, fmt.Errorf("guardOffsetsMil must be finite and within maxDetourMil")
			}
		}
	}
	if g.SignalLayer != 1 || g.GuardLayer != 1 {
		return nil, fmt.Errorf("crystalGuard signalLayer and guardLayer must be TOP(1) for this strategy")
	}
	if g.GroundImplementation == "" {
		g.GroundImplementation = "legacy-local-pours"
	}
	positive := map[string]float64{
		"signalWidthMil": g.SignalWidthMil, "componentGapMil": g.ComponentGapMil,
		"guardWidthMil": g.GuardWidthMil, "guardGapMil": g.GuardGapMil,
		"keepoutMarginMil": g.KeepoutMarginMil, "fencePitchMil": g.FencePitchMil,
		"fenceMarginMil": g.FenceMarginMil,
	}
	if g.GroundImplementation == "legacy-local-pours" {
		positive["localPourMarginMil"] = g.LocalPourMarginMil
	}
	for name, value := range positive {
		if !allFinite(value) || value <= 0 {
			return nil, fmt.Errorf("crystalGuard.%s must be a finite value > 0", name)
		}
	}
	seenNets := map[string]bool{}
	for i, p := range g.Ports {
		if strings.TrimSpace(p.Net) == "" || seenNets[p.Net] {
			return nil, fmt.Errorf("crystalGuard ports[%d] has empty or duplicate net", i)
		}
		seenNets[p.Net] = true
		if p.CapacitorSide != "left" && p.CapacitorSide != "right" {
			return nil, fmt.Errorf("crystalGuard ports[%d].capacitorSide must be left|right", i)
		}
		cap, ok := members[p.CapacitorRef]
		if !ok || cap.BBox == nil {
			return nil, fmt.Errorf("crystalGuard capacitor %q is not a measured module member", p.CapacitorRef)
		}
		for label, pair := range map[string]struct {
			comp boardComp
			pad  string
		}{
			"owner": {owner, p.OwnerPad}, "crystal": {members[g.CrystalRef], p.CrystalPad},
			"capacitor signal": {cap, p.CapacitorSignalPad}, "capacitor ground": {cap, p.CapacitorGroundPad},
		} {
			pad, err := findBoardPadExact(pair.comp, pair.pad, "")
			if err != nil {
				return nil, fmt.Errorf("crystalGuard ports[%d] %s pad %s: %w", i, label, pair.pad, err)
			}
			if label != "capacitor ground" && pad.Net != p.Net {
				return nil, fmt.Errorf("crystalGuard ports[%d] %s pad is net %q, want %q", i, label, pad.Net, p.Net)
			}
			if label == "capacitor ground" && pad.Net != g.GroundNet {
				return nil, fmt.Errorf("crystalGuard ports[%d] capacitor ground pad is net %q, want %q", i, pad.Net, g.GroundNet)
			}
		}
	}
	for _, number := range g.CrystalGroundPads {
		p, err := findBoardPadExact(members[g.CrystalRef], number, "")
		if err != nil || p.Net != g.GroundNet {
			return nil, fmt.Errorf("crystal ground pad %s is missing or not on %s", number, g.GroundNet)
		}
	}
	for _, ref := range g.GroundAnchors {
		cRef, pRef, err := splitPadRef(ref)
		if err != nil {
			return nil, fmt.Errorf("ground anchor %q: %w", ref, err)
		}
		c, ok := all[cRef]
		if !ok {
			return nil, fmt.Errorf("ground anchor component %q is absent", cRef)
		}
		p, err := findBoardPadExact(c, pRef, "")
		if err != nil || p.Net != g.GroundNet {
			return nil, fmt.Errorf("ground anchor %s is missing or not on %s", ref, g.GroundNet)
		}
	}
	return g, nil
}

func generateCrystalGuardVariants(mod pcbLayoutModuleSpec, members, all map[string]boardComp, snap *boardSnapshot) ([]pcbLayoutVariant, error) {
	g, err := validateCrystalGuardSpec(mod, members, all, snap)
	if err != nil {
		return nil, err
	}
	gaps := mod.Search.GapsMil
	if len(gaps) == 0 {
		return nil, fmt.Errorf("crystal-guard requires search.gapsMil")
	}
	rots := mod.Search.RotationDeltasDeg
	if len(rots) == 0 {
		rots = []float64{0}
	}
	owner := all[g.OwnerRef]
	// Assemble once in a local coordinate system rooted at the crystal anchor.
	// Candidate rotation is then a rigid transform of X1 and both capacitors;
	// no member is independently re-snapped in board coordinates after rotation.
	baseCrystal := members[g.CrystalRef]
	local := map[string]boardComp{g.CrystalRef: baseCrystal}
	for _, port := range g.Ports {
		cap0 := members[port.CapacitorRef]
		cap, err := transformBoardComp(cap0, cap0.X, cap0.Y, 0, 0, port.CapacitorRotationDeg-cap0.Rotation)
		if err != nil {
			return nil, err
		}
		crystalPad, _ := findBoardPadExact(baseCrystal, port.CrystalPad, "")
		capPad, _ := findBoardPadExact(cap, port.CapacitorSignalPad, "")
		capDX, capDY := 0.0, crystalPad.Y-capPad.Y
		if port.CapacitorSide == "left" {
			capDX = baseCrystal.BBox.MinX - g.ComponentGapMil - cap.BBox.MaxX
		} else {
			capDX = baseCrystal.BBox.MaxX + g.ComponentGapMil - cap.BBox.MinX
		}
		projected, err := translateBoardComp(cap, capDX, capDY)
		if err != nil {
			return nil, err
		}
		local[port.CapacitorRef] = projected
	}
	var out []pcbLayoutVariant
	for _, gap := range gaps {
		for _, delta := range rots {
			placed := map[string]boardComp{}
			for ref, comp := range local {
				projected, err := transformBoardComp(comp, baseCrystal.X, baseCrystal.Y, 0, 0, delta)
				if err != nil {
					return nil, err
				}
				placed[ref] = projected
			}
			crystal := placed[g.CrystalRef]
			var ownerX, crystalX float64
			for _, port := range g.Ports {
				op, _ := findBoardPadExact(owner, port.OwnerPad, "")
				cp, _ := findBoardPadExact(crystal, port.CrystalPad, "")
				ownerX += op.X
				crystalX += cp.X
			}
			ownerX /= float64(len(g.Ports))
			crystalX /= float64(len(g.Ports))
			dx := ownerX - crystalX
			var localBounds *layoutBBox
			for _, comp := range placed {
				localBounds = unionLayoutBBox(localBounds, comp.BBox)
			}
			if localBounds == nil {
				return nil, fmt.Errorf("crystal local assembly has no measured bounds")
			}
			dy := owner.BBox.MinY - gap - localBounds.MaxY
			for ref, comp := range placed {
				projected, err := translateBoardComp(comp, dx, dy)
				if err != nil {
					return nil, err
				}
				placed[ref] = projected
			}
			offsets, err := crystalPlacementOffsets(mod, placed, all, snap)
			if err != nil {
				return nil, err
			}
			for _, offset := range offsets {
				shifted := map[string]boardComp{}
				for ref, comp := range placed {
					projected, err := translateBoardComp(comp, offset.XMil, offset.YMil)
					if err != nil {
						return nil, err
					}
					shifted[ref] = projected
				}
				crystal = shifted[g.CrystalRef]
				out = append(out, pcbLayoutVariant{
					label: fmt.Sprintf("crystal-local-gap(%.2f)-rotate(%.0f)-offset(%.4f,%.4f)-%s", gap, normalizeRotation(crystal.Rotation), offset.XMil, offset.YMil, offset.Label),
					comps: shifted,
				})
			}
		}
	}
	return out, nil
}

// Search offsets are seeds only: exact routing/pad/area checks still run for
// every complete candidate. Bounding boxes never certify copper clearance.
func crystalPlacementOffsets(mod pcbLayoutModuleSpec, placed, all map[string]boardComp, snap *boardSnapshot) ([]pcbLayoutOffset, error) {
	if err := validatePCBLayoutInputNumbers(mod); err != nil {
		return nil, err
	}
	out := append([]pcbLayoutOffset(nil), mod.Search.OffsetsMil...)
	if len(out) == 0 {
		out = append(out, pcbLayoutOffset{Label: "owner-pad-midpoint"})
	}
	s := mod.Search.CrystalOffsets
	if s == nil {
		return out, nil
	}
	g := mod.CrystalGuard
	xs, ys := []float64{0, -s.MaxXMil, s.MaxXMil}, []float64{0, -s.MaxAwayMil}
	for x := s.StepMil; x < s.MaxXMil; x += s.StepMil {
		xs = append(xs, -x, x)
	}
	for y := s.StepMil; y < s.MaxAwayMil; y += s.StepMil {
		ys = append(ys, -y)
	}
	owner := all[g.OwnerRef]
	crystal := placed[g.CrystalRef]
	for _, port := range g.Ports {
		op, _ := findBoardPadExact(owner, port.OwnerPad, "")
		cp, _ := findBoardPadExact(crystal, port.CrystalPad, "")
		xs = append(xs, op.X-cp.X)
	}
	clearance := math.Max(snap.Rules.ClearanceMil, snap.Rules.ClearanceTrackTrackMil)
	viaDiameter := g.ViaDiameterMil
	if viaDiameter == 0 {
		viaDiameter = snap.Rules.ViaDiameterMil
	}
	protection := g.KeepoutMarginMil + g.GuardGapMil + g.GuardWidthMil/2 + g.FenceMarginMil + viaDiameter/2
	refs := make([]string, 0, len(all))
	for ref := range all {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		obstacle := all[ref]
		if _, member := placed[ref]; member || ref == g.OwnerRef || obstacle.BBox == nil {
			continue
		}
		for _, memberRef := range sortedCrystalMemberRefs(placed) {
			b := placed[memberRef].BBox
			if b == nil {
				continue
			}
			for _, margin := range []float64{clearance, clearance + protection} {
				if obstacle.BBox.MaxX < b.MinX-s.MaxXMil-margin || obstacle.BBox.MinX > b.MaxX+s.MaxXMil+margin || obstacle.BBox.MaxY < b.MinY-s.MaxAwayMil-margin || obstacle.BBox.MinY > b.MaxY+margin {
					continue
				}
				// Exact obstacle-edge events supplement the finite grid. The
				// complete bundle checker rejects routes/fences blocked elsewhere.
				xs = append(xs, obstacle.BBox.MinX-margin-b.MaxX, obstacle.BBox.MaxX+margin-b.MinX)
				ys = append(ys, obstacle.BBox.MinY-margin-b.MaxY)
			}
		}
	}
	axis := func(values []float64, min, max float64) []float64 {
		sort.Float64s(values)
		var unique []float64
		for _, v := range values {
			if v < min-netPathGeomEps || v > max+netPathGeomEps {
				continue
			}
			if len(unique) == 0 || math.Abs(v-unique[len(unique)-1]) > netPathGeomEps {
				unique = append(unique, v)
			}
		}
		return unique
	}
	xs, ys = axis(xs, -s.MaxXMil, s.MaxXMil), axis(ys, -s.MaxAwayMil, 0)
	if len(xs)*len(ys)+len(out) > 4096 {
		return nil, fmt.Errorf("crystalOffsets obstacle/grid events (%d x %d) exceed 4096 offset limit; narrow bounds or increase stepMil", len(xs), len(ys))
	}
	for _, y := range ys {
		for _, x := range xs {
			out = append(out, pcbLayoutOffset{XMil: x, YMil: y, Label: "bounded-obstacle-pad-search"})
		}
	}
	// Lexicographic preference, not an aggregate score: keep the MCU gap
	// small, then minimize lateral departure from the signal-pad midpoint.
	sort.SliceStable(out, func(i, j int) bool {
		if math.Abs(out[i].YMil-out[j].YMil) > netPathGeomEps {
			return out[i].YMil > out[j].YMil
		}
		if math.Abs(math.Abs(out[i].XMil)-math.Abs(out[j].XMil)) > netPathGeomEps {
			return math.Abs(out[i].XMil) < math.Abs(out[j].XMil)
		}
		return out[i].XMil < out[j].XMil
	})
	return out, nil
}

func sortedCrystalMemberRefs(placed map[string]boardComp) []string {
	refs := make([]string, 0, len(placed))
	for ref := range placed {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

func attachCrystalGuardBundle(candidate *pcbLayoutCandidate, mod pcbLayoutModuleSpec, variant pcbLayoutVariant, all map[string]boardComp, snap *boardSnapshot) error {
	g, err := validateCrystalGuardSpec(mod, variant.comps, all, snap)
	if err != nil {
		return err
	}
	tracks, arcs, vias, err := crystalSnapshotRouting(snap)
	if err != nil {
		return err
	}
	areas, err := parseCopperAreaObstacles(snap.Copper.Fills, snap.Copper.Poured)
	if err != nil {
		return fmt.Errorf("crystal-guard area copper is unknown: %w", err)
	}
	signalNets := map[string]bool{}
	for _, port := range g.Ports {
		signalNets[port.Net] = true
	}
	replacementIDs, err := validateCrystalReplacementSet(g.ReplacePrimitiveIDs, signalNets, tracks, arcs)
	if err != nil {
		return err
	}
	replace := map[string]bool{}
	for _, id := range replacementIDs {
		replace[id] = true
	}
	keptTracks := tracks[:0:0]
	for _, t := range tracks {
		if !replace[t.ID] {
			keptTracks = append(keptTracks, t)
		}
	}
	keptArcs := arcs[:0:0]
	for _, arc := range arcs {
		if !replace[arc.ID] {
			keptArcs = append(keptArcs, arc)
		}
	}

	bundle := &pcbLayoutModuleBundle{
		Kind: "crystal-guard", GroundImplementation: g.GroundImplementation, ReplacePrimitiveIDs: replacementIDs,
		GroundAnchors: append([]string(nil), g.GroundAnchors...), Metrics: map[string]float64{},
	}
	for ref := range variant.comps {
		bundle.OwnedRefs = append(bundle.OwnedRefs, ref)
	}
	sort.Strings(bundle.OwnedRefs)
	crystal := variant.comps[g.CrystalRef]
	mainRoutes := crystalMainSignalRoutes(g, all[g.OwnerRef], crystal, snap.Rules.ClearanceMil)
	if candidate.Escape != nil {
		for i, p := range g.Ports {
			found := false
			for _, r := range candidate.Escape.Routes {
				if r.From == g.OwnerRef+"."+p.OwnerPad && r.To == g.CrystalRef+"."+p.CrystalPad && r.Net == p.Net && r.Layer == 1 {
					mainRoutes[i] = append([][2]float64(nil), r.Points...)
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("joint escape witness must include TOP owner-to-crystal route for %s", p.Net)
			}
		}
	}
	for portIndex, port := range g.Ports {
		crystalPad, _ := findBoardPadExact(crystal, port.CrystalPad, "")
		capPad, _ := findBoardPadExact(variant.comps[port.CapacitorRef], port.CapacitorSignalPad, "")
		mainPoints := mainRoutes[portIndex]
		capPoints := [][2]float64{{capPad.X, capPad.Y}, {crystalPad.X, crystalPad.Y}}
		bundle.SignalRoutes = append(bundle.SignalRoutes,
			pcbModuleRoute{ID: strings.ToLower(strings.ReplaceAll(port.Net, "_", "-")) + "-main", Net: port.Net, Layer: g.SignalLayer, WidthMil: g.SignalWidthMil, Points: mainPoints, Role: "signal-main", From: g.OwnerRef + "." + port.OwnerPad, To: g.CrystalRef + "." + port.CrystalPad},
			pcbModuleRoute{ID: strings.ToLower(strings.ReplaceAll(port.Net, "_", "-")) + "-cap", Net: port.Net, Layer: g.SignalLayer, WidthMil: g.SignalWidthMil, Points: capPoints, Role: "signal-cap", From: port.CapacitorRef + "." + port.CapacitorSignalPad, To: g.CrystalRef + "." + port.CrystalPad},
		)
	}
	if g.ProtectionSearch != nil && candidate.Escape == nil {
		pads, err := crystalCandidatePads(snap, variant.comps)
		if err != nil {
			return err
		}
		if err := solveCrystalMeasuredSignals(bundle, g, snap, pads, keptTracks, keptArcs, vias, areas); err != nil {
			return err
		}
	}
	var sensitive *layoutBBox
	for _, c := range variant.comps {
		sensitive = unionLayoutBBox(sensitive, c.BBox)
	}
	// no-pours protects both the parts and the actual OSC main-route copper.
	// A route contributes its centerline bbox expanded by half its real width and
	// the live copper clearance. KeepoutMarginMil is then an additional design
	// margin around that already clearance-aware sensitive envelope.
	routeClearance := math.Max(snap.Rules.ClearanceMil, snap.Rules.ClearanceTrackTrackMil)
	if !allFinite(routeClearance) || routeClearance < 0 {
		return fmt.Errorf("crystal-guard signal-route clearance is unavailable")
	}
	strokeMargin := g.SignalWidthMil/2 + routeClearance
	for _, route := range bundle.SignalRoutes {
		if route.Role != "signal-main" || len(route.Points) == 0 {
			continue
		}
		stroke := expandLayoutBBox(pointsBBox(route.Points), strokeMargin)
		sensitive = unionLayoutBBox(sensitive, &stroke)
	}
	if sensitive == nil {
		return fmt.Errorf("crystal-guard sensitive envelope is unavailable")
	}
	keepout := expandLayoutBBox(*sensitive, g.KeepoutMarginMil)
	bundle.Metrics["sensitiveSignalStrokeMarginMil"] = round4(strokeMargin)
	bundle.Metrics["sensitiveKeepoutMarginMil"] = round4(g.KeepoutMarginMil)
	if g.ProtectionSearch != nil {
		if err := assembleCrystalObstacleProtection(bundle, g, variant.comps, all, snap, keptTracks, keptArcs, vias, areas, keepout); err != nil {
			return err
		}
	} else {
		guard := expandLayoutBBox(keepout, g.GuardGapMil+g.GuardWidthMil/2)
		bundle.Envelope = &guard
		bundle.GroundRoutes = append(bundle.GroundRoutes,
			pcbModuleRoute{ID: "guard-bottom", Net: g.GroundNet, Layer: g.GuardLayer, WidthMil: g.GuardWidthMil, Points: [][2]float64{{guard.MinX, guard.MinY}, {guard.MaxX, guard.MinY}}, Role: "guard"},
			pcbModuleRoute{ID: "guard-left", Net: g.GroundNet, Layer: g.GuardLayer, WidthMil: g.GuardWidthMil, Points: [][2]float64{{guard.MinX, guard.MinY}, {guard.MinX, guard.MaxY}}, Role: "guard"},
			pcbModuleRoute{ID: "guard-right", Net: g.GroundNet, Layer: g.GuardLayer, WidthMil: g.GuardWidthMil, Points: [][2]float64{{guard.MaxX, guard.MinY}, {guard.MaxX, guard.MaxY}}, Role: "guard"},
		)
		windows := crystalGuardSignalWindows(bundle.SignalRoutes, guard.MaxY, g.SignalWidthMil/2+g.GuardWidthMil/2+snap.Rules.ClearanceMil)
		cursor := guard.MinX
		segment := 0
		for _, window := range windows {
			start, end := math.Max(guard.MinX, window[0]), math.Min(guard.MaxX, window[1])
			if start > cursor+netPathGeomEps {
				segment++
				bundle.GroundRoutes = append(bundle.GroundRoutes, pcbModuleRoute{ID: fmt.Sprintf("guard-top-%02d", segment), Net: g.GroundNet, Layer: g.GuardLayer, WidthMil: g.GuardWidthMil, Points: [][2]float64{{cursor, guard.MaxY}, {start, guard.MaxY}}, Role: "guard"})
			}
			cursor = math.Max(cursor, end)
		}
		if cursor < guard.MaxX-netPathGeomEps {
			segment++
			bundle.GroundRoutes = append(bundle.GroundRoutes, pcbModuleRoute{ID: fmt.Sprintf("guard-top-%02d", segment), Net: g.GroundNet, Layer: g.GuardLayer, WidthMil: g.GuardWidthMil, Points: [][2]float64{{cursor, guard.MaxY}, {guard.MaxX, guard.MaxY}}, Role: "guard"})
		}
		for _, port := range g.Ports {
			capPad, _ := findBoardPadExact(variant.comps[port.CapacitorRef], port.CapacitorGroundPad, "")
			guardPoint, ok := nearestPointOnModuleRoutes(bundle.GroundRoutes, "guard", [2]float64{capPad.X, capPad.Y})
			if !ok {
				return fmt.Errorf("crystal-guard has no guard segment for %s ground", port.CapacitorRef)
			}
			bundle.GroundRoutes = append(bundle.GroundRoutes, pcbModuleRoute{ID: "ground-" + strings.ToLower(port.CapacitorRef), Net: g.GroundNet, Layer: g.GuardLayer, WidthMil: g.GuardWidthMil, Points: crystal45Path([2]float64{capPad.X, capPad.Y}, guardPoint), Role: "ground-spoke", From: port.CapacitorRef + "." + port.CapacitorGroundPad})
		}
		for _, number := range g.CrystalGroundPads {
			p, _ := findBoardPadExact(crystal, number, "")
			bundle.GroundRoutes = append(bundle.GroundRoutes, pcbModuleRoute{ID: "ground-" + strings.ToLower(g.CrystalRef) + "-" + number, Net: g.GroundNet, Layer: g.GuardLayer, WidthMil: g.GuardWidthMil, Points: [][2]float64{{p.X, p.Y}, {p.X, guard.MinY}}, Role: "ground-spoke", From: g.CrystalRef + "." + number})
		}
		bundle.Regions = []pcbModuleRegion{
			{ID: "crystal-no-pours-top", Layer: 1, RuleTypes: []string{"no-pours"}, Points: rectPoints(keepout)},
			{ID: "crystal-no-pours-bottom", Layer: 2, RuleTypes: []string{"no-pours"}, Points: rectPoints(keepout)},
		}
		fenceRect := expandLayoutBBox(guard, g.FenceMarginMil)
		fencePoints, err := rectViaFencePoints(fenceRect.MinX, fenceRect.MinY, fenceRect.MaxX, fenceRect.MaxY, g.FencePitchMil, 0)
		if err != nil {
			return err
		}
		hole, diameter := g.ViaHoleMil, g.ViaDiameterMil
		if hole == 0 {
			hole = snap.Rules.ViaDrillMil
		}
		if diameter == 0 {
			diameter = snap.Rules.ViaDiameterMil
		}
		if !allFinite(hole, diameter) || hole <= 0 || diameter <= hole {
			return fmt.Errorf("crystal-guard via dimensions are invalid: %.3f/%.3fmil", hole, diameter)
		}
		fenceWindows := crystalGuardSignalWindows(bundle.SignalRoutes, fenceRect.MaxY, diameter/2+g.SignalWidthMil/2+snap.Rules.ClearanceMil)
		filteredFence := make([][2]float64, 0, len(fencePoints))
		excludedSignalEntry := 0
		for _, point := range fencePoints {
			excluded := false
			if math.Abs(point[1]-fenceRect.MaxY) <= netPathGeomEps {
				for _, window := range fenceWindows {
					if point[0] >= window[0]-netPathGeomEps && point[0] <= window[1]+netPathGeomEps {
						excluded = true
						break
					}
				}
			}
			if excluded {
				excludedSignalEntry++
			} else {
				filteredFence = append(filteredFence, point)
			}
		}
		fencePoints = filteredFence
		bundle.Metrics["fenceExcludedSignalEntry"] = float64(excludedSignalEntry)
		candidatePads, err := crystalCandidatePads(snap, variant.comps)
		if err != nil {
			return err
		}
		preflight := preflightViaFence(fencePoints, g.GroundNet, hole, diameter, snap.Rules.ClearanceMil, snap.Rules.CopperToEdgeMil, snap.Outline, candidatePads, keptTracks, arcs, vias, areas)
		if len(preflight.Problems) > 0 {
			return fmt.Errorf("crystal-guard via fence conflicts: %s", strings.Join(preflight.Problems, "; "))
		}
		for i, point := range preflight.Create {
			bundle.Vias = append(bundle.Vias, pcbModuleVia{ID: fmt.Sprintf("fence-%02d", i+1), Net: g.GroundNet, X: point[0], Y: point[1], HoleMil: hole, DiameterMil: diameter, Role: "fence"})
		}
		var anchorViaPoints [][2]float64
		for i, anchor := range g.GroundAnchors {
			ref, padNum, _ := splitPadRef(anchor)
			pad, _ := findBoardPadExact(all[ref], padNum, "")
			usableHalf := (pad.W - diameter) / 2
			offset := math.Min(diameter*0.75, usableHalf)
			if offset <= netPathGeomEps || pad.H+netPathGeomEps < diameter {
				return fmt.Errorf("ground anchor %s cannot contain two separated %.3fmil vias", anchor, diameter)
			}
			for j, x := range []float64{pad.X - offset, pad.X + offset} {
				point := [2]float64{x, pad.Y}
				anchorViaPoints = append(anchorViaPoints, point)
				bundle.Vias = append(bundle.Vias, pcbModuleVia{ID: fmt.Sprintf("ground-anchor-%02d-%d", i+1, j+1), Net: g.GroundNet, X: x, Y: pad.Y, HoleMil: hole, DiameterMil: diameter, Role: "ground-anchor"})
				bundle.GroundRoutes = append(bundle.GroundRoutes, pcbModuleRoute{
					ID: fmt.Sprintf("ground-anchor-stub-%02d-%d", i+1, j+1), Net: g.GroundNet, Layer: 1,
					WidthMil: g.GuardWidthMil, Points: [][2]float64{{pad.X, pad.Y}, point}, Role: "ground-anchor-stub", From: anchor,
				})
			}
		}
		if len(preflight.Create) < 2 || len(anchorViaPoints) < 2 {
			return fmt.Errorf("crystal-guard needs at least two newly controlled fence vias")
		}
		// Pick two distinct fence vias close to the MCU ground anchor.  Each guard
		// entry lands on an existing guard segment on TOP, changes layer through the
		// fence via, then reaches a separated via inside the declared MCU GND pad on
		// BOTTOM.  The short TOP anchor stub makes the via-in-pad connection explicit
		// to the listed-copper path checker.
		usedFence := map[int]bool{}
		for i := 0; i < 2; i++ {
			anchorPoint := anchorViaPoints[i]
			fenceIndex := nearestUnusedPoint(preflight.Create, anchorPoint, usedFence)
			if fenceIndex < 0 {
				return fmt.Errorf("crystal-guard could not allocate two distinct fence ground entries")
			}
			usedFence[fenceIndex] = true
			fencePoint := preflight.Create[fenceIndex]
			guardPoint, ok := nearestPointOnModuleRoutes(bundle.GroundRoutes, "guard", fencePoint)
			if !ok {
				return fmt.Errorf("crystal-guard has no guard segment for ground entry")
			}
			bundle.GroundRoutes = append(bundle.GroundRoutes,
				pcbModuleRoute{ID: fmt.Sprintf("guard-ground-top-%d", i+1), Net: g.GroundNet, Layer: 1, WidthMil: g.GuardWidthMil, Points: crystal45Path(guardPoint, fencePoint), Role: "ground-entry-top"},
				pcbModuleRoute{ID: fmt.Sprintf("guard-ground-bottom-%d", i+1), Net: g.GroundNet, Layer: 2, WidthMil: g.GuardWidthMil, Points: crystal45Path(fencePoint, anchorPoint), Role: "ground-entry-bottom"},
			)
		}
		if g.GroundImplementation == "legacy-local-pours" {
			pourBounds := fenceRect
			pourBounds.MinX = math.Min(pourBounds.MinX, all[g.OwnerRef].BBox.MinX)
			pourBounds.MinY = math.Min(pourBounds.MinY, all[g.OwnerRef].BBox.MinY)
			pourBounds.MaxX = math.Max(pourBounds.MaxX, all[g.OwnerRef].BBox.MaxX)
			pourBounds.MaxY = math.Max(pourBounds.MaxY, all[g.OwnerRef].BBox.MaxY)
			pourBounds = expandLayoutBBox(pourBounds, g.LocalPourMarginMil)
			pourClearance := math.Max(snap.Rules.ClearanceMil, snap.Rules.ClearanceTrackTrackMil)
			bundle.Pours, err = crystalRingPours(pourBounds, rectPoints(keepout), pourClearance, g.GroundNet)
			if err != nil {
				return fmt.Errorf("crystal-guard local ring pours: %w", err)
			}
			bundle.Metrics["localPourKeepoutClearanceMil"] = pourClearance
		}
	}
	if err := bindCrystalExistingAnchorVias(bundle, g, snap); err != nil {
		return err
	}
	// EasyEDA splits an existing primitive when a later same-net branch lands in
	// its interior. Normalize those T junctions before preview/apply generation so
	// every captured guard PID remains the PID that fresh readback must contain.
	if err := splitCrystalGuardAtGroundJunctions(bundle); err != nil {
		return err
	}
	candidatePads, err := crystalCandidatePads(snap, variant.comps)
	if err != nil {
		return err
	}
	if g.GroundImplementation == "legacy-local-pours" {
		impactEnvelope, impactErr := crystalGuardPourImpactEnvelope(bundle, snap, variant.comps)
		if impactErr != nil {
			return impactErr
		}
		bundle.AffectedBaselinePours, err = crystalGuardAffectedBaselinePours(snap, g.GroundNet, impactEnvelope)
		if err != nil {
			return err
		}
	}
	plannedVias := append([]pcbViaP(nil), vias...)
	for _, v := range bundle.Vias {
		plannedVias = append(plannedVias, pcbViaP{ID: "planned:" + v.ID, Net: v.Net, X: v.X, Y: v.Y, Hole: v.HoleMil, Dia: v.DiameterMil})
	}
	if err := validateCrystalRoutes(bundle, snap, candidatePads, keptTracks, keptArcs, plannedVias, areas); err != nil {
		return err
	}
	length, turns := 0.0, 0.0
	for _, route := range bundle.SignalRoutes {
		length += moduleRouteLength(route.Points)
		turns += float64(moduleRouteTurns(route.Points))
	}
	bundle.Metrics["signalLengthMil"] = round4(length)
	bundle.Metrics["signalTurns"] = turns
	candidate.Bundle = bundle
	rebuildCrystalApply(candidate, g, bundle)
	return nil
}

// validateCrystalReplacementSet binds replacement to the exact fresh baseline
// inventory of routed track/arc copper on the two declared oscillator nets. A
// subset would leave stale owned copper behind; an extra id could delete another
// net. Both are unsafe, so the default contract is exact-set/fail-closed.
func validateCrystalReplacementSet(declared []string, signalNets map[string]bool, tracks []pcbTrack, arcs []pcbArc) ([]string, error) {
	owned := map[string]bool{}
	addOwned := func(id, net, kind string) error {
		if !signalNets[net] {
			return nil
		}
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("fresh %s on oscillator net %s has no primitiveId", kind, net)
		}
		if owned[id] {
			return fmt.Errorf("fresh oscillator routing repeats primitiveId %s", id)
		}
		owned[id] = true
		return nil
	}
	for _, track := range tracks {
		if err := addOwned(track.ID, track.Net, "track"); err != nil {
			return nil, err
		}
	}
	for _, arc := range arcs {
		if err := addOwned(arc.ID, arc.Net, "arc"); err != nil {
			return nil, err
		}
	}
	declaredSet := map[string]bool{}
	for _, id := range declared {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("crystalGuard.replacePrimitiveIds contains an empty primitiveId")
		}
		if declaredSet[id] {
			return nil, fmt.Errorf("crystalGuard.replacePrimitiveIds repeats primitiveId %s", id)
		}
		declaredSet[id] = true
	}
	var missing, extra []string
	for id := range owned {
		if !declaredSet[id] {
			missing = append(missing, id)
		}
	}
	for id := range declaredSet {
		if !owned[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		return nil, fmt.Errorf("crystalGuard.replacePrimitiveIds must exactly equal all fresh OSC track/arc primitiveIds; missing=%v extra=%v", missing, extra)
	}
	result := make([]string, 0, len(owned))
	for id := range owned {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func crystalGuardPourImpactEnvelope(bundle *pcbLayoutModuleBundle, snap *boardSnapshot, moved map[string]boardComp) (layoutBBox, error) {
	var impact *layoutBBox
	add := func(box layoutBBox) {
		if !allFinite(box.MinX, box.MinY, box.MaxX, box.MaxY) || box.MinX > box.MaxX || box.MinY > box.MaxY {
			return
		}
		impact = unionLayoutBBox(impact, &box)
	}
	if bundle.Envelope != nil {
		add(*bundle.Envelope)
	}
	for _, region := range bundle.Regions {
		add(pointsBBox(region.Points))
	}
	for _, pour := range bundle.Pours {
		add(pointsBBox(pour.Points))
	}
	for _, via := range bundle.Vias {
		r := via.DiameterMil / 2
		add(layoutBBox{MinX: via.X - r, MinY: via.Y - r, MaxX: via.X + r, MaxY: via.Y + r})
	}
	for _, route := range append(append([]pcbModuleRoute(nil), bundle.SignalRoutes...), bundle.GroundRoutes...) {
		if len(route.Points) == 0 {
			continue
		}
		box := pointsBBox(route.Points)
		add(expandLayoutBBox(box, route.WidthMil/2))
	}
	beforeByRef := snap.byDesignator()
	for _, ref := range bundle.OwnedRefs {
		if c, ok := beforeByRef[ref]; ok && c.BBox != nil {
			add(*c.BBox)
		}
		if c, ok := moved[ref]; ok && c.BBox != nil {
			add(*c.BBox)
		}
	}
	if impact == nil {
		return layoutBBox{}, fmt.Errorf("crystal-guard materialized-pour impact envelope is unavailable")
	}
	margin := math.Max(snap.Rules.ClearanceMil, snap.Rules.ClearanceTrackTrackMil)
	if !allFinite(margin) || margin < 0 {
		return layoutBBox{}, fmt.Errorf("crystal-guard materialized-pour impact clearance is unavailable")
	}
	return expandLayoutBBox(*impact, margin), nil
}

func crystalGuardAffectedBaselinePours(snap *boardSnapshot, net string, impact layoutBBox) ([]pcbModuleAffectedPour, error) {
	if snap == nil || snap.Copper == nil {
		return nil, fmt.Errorf("crystal-guard cannot classify affected baseline pours without fresh copper")
	}
	materialized := map[string]map[string]any{}
	for _, raw := range snap.Copper.Poured {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("baseline materialized pour is not an object")
		}
		boundaryID := asString(m["pourPrimitiveId"])
		if boundaryID == "" || asString(m["primitiveId"]) == "" {
			return nil, fmt.Errorf("baseline materialized pour identity is unavailable")
		}
		if _, duplicate := materialized[boundaryID]; duplicate {
			return nil, fmt.Errorf("baseline pour %s has multiple materialized objects", boundaryID)
		}
		materialized[boundaryID] = m
	}
	var affected []pcbModuleAffectedPour
	for _, raw := range snap.Copper.Pours {
		boundary, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("baseline pour boundary is not an object")
		}
		if asString(boundary["net"]) != net {
			continue
		}
		layer := int(asFloat(boundary["layer"]))
		if layer != 1 && layer != 2 {
			continue
		}
		boundaryID := asString(boundary["primitiveId"])
		if boundaryID == "" || boundary["geometryAvailable"] != true {
			return nil, fmt.Errorf("baseline %s pour boundary geometry/identity is unavailable", net)
		}
		poured, ok := materialized[boundaryID]
		if !ok {
			continue
		}
		if asString(poured["net"]) != net || int(asFloat(poured["layer"])) != layer {
			return nil, fmt.Errorf("baseline materialized pour %s metadata differs from boundary", boundaryID)
		}
		fills, ok := poured["fills"].([]any)
		if !ok {
			return nil, fmt.Errorf("baseline materialized pour %s fills are unavailable", boundaryID)
		}
		intersects := false
		for _, fillRaw := range fills {
			fill, ok := fillRaw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("baseline materialized pour %s fill is not an object", boundaryID)
			}
			hit, err := complexPolygonOccupiesRect(fill["source"], impact)
			if err != nil {
				return nil, fmt.Errorf("baseline materialized pour %s geometry is unknown: %w", boundaryID, err)
			}
			intersects = intersects || hit
		}
		if intersects {
			affected = append(affected, pcbModuleAffectedPour{
				BoundaryPrimitiveID: boundaryID, MaterializedPrimitiveID: asString(poured["primitiveId"]),
				Net: net, Layer: layer, ImpactEnvelope: impact,
			})
		}
	}
	sort.Slice(affected, func(i, j int) bool { return affected[i].BoundaryPrimitiveID < affected[j].BoundaryPrimitiveID })
	return affected, nil
}

func crystalMainSignalRoutes(g *pcbCrystalGuardSpec, owner, crystal boardComp, clearance float64) [][][2]float64 {
	routes := make([][][2]float64, len(g.Ports))
	type endpoint struct {
		owner, crystal [2]float64
	}
	ends := make([]endpoint, len(g.Ports))
	for i, port := range g.Ports {
		op, _ := findBoardPadExact(owner, port.OwnerPad, "")
		cp, _ := findBoardPadExact(crystal, port.CrystalPad, "")
		ends[i] = endpoint{owner: [2]float64{op.X, op.Y}, crystal: [2]float64{cp.X, cp.Y}}
		routes[i] = crystal45Path(ends[i].owner, ends[i].crystal)
	}
	if len(ends) != 2 {
		return routes
	}
	// A quarter-turned four-pad crystal commonly stacks the two signal pads on
	// one X coordinate. Independent shortest paths can then cross each other or
	// the opposite pad. Give each owner pin its own outside escape lane before
	// turning toward the crystal. The offset is derived from the live clearance,
	// signal width and declared local component gap.
	if math.Abs(ends[0].crystal[0]-ends[1].crystal[0]) >= g.SignalWidthMil+2*clearance {
		return routes
	}
	ownerMidX := (ends[0].owner[0] + ends[1].owner[0]) / 2
	maxPadHalf := 0.0
	for _, pad := range crystal.Pads {
		maxPadHalf = math.Max(maxPadHalf, math.Max(pad.W, pad.H)/2)
	}
	escape := math.Max(g.ComponentGapMil, maxPadHalf+g.SignalWidthMil/2+clearance+2)
	for i, end := range ends {
		sign := -1.0
		if end.owner[0] >= ownerMidX {
			sign = 1
		}
		first := [2]float64{end.owner[0] + sign*escape, end.owner[1] - escape}
		approach := [2]float64{end.crystal[0] + sign*escape, end.crystal[1] + escape}
		tail := crystal45Path(first, approach)
		routes[i] = append([][2]float64{end.owner, first}, tail[1:]...)
		routes[i] = append(routes[i], end.crystal)
	}
	return routes
}

func crystalSnapshotRouting(snap *boardSnapshot) ([]pcbTrack, []pcbArc, []pcbViaP, error) {
	if snap == nil || snap.Copper == nil {
		return nil, nil, nil, fmt.Errorf("copper snapshot is missing")
	}
	lines := map[string]any{"lines": snap.Copper.Lines, "arcs": snap.Copper.Arcs}
	if snap.Copper.ArcsAvailable != nil {
		lines["arcsAvailable"] = *snap.Copper.ArcsAvailable
	}
	tracks, arcs, err := parseNetPathLines(lines)
	if err != nil {
		return nil, nil, nil, err
	}
	vias, err := parseNetPathVias(map[string]any{"vias": snap.Copper.Vias})
	return tracks, arcs, vias, err
}

func validateCrystalRoutes(bundle *pcbLayoutModuleBundle, snap *boardSnapshot, pads []pcbPadP, existing []pcbTrack, arcs []pcbArc, vias []pcbViaP, areas []pcbCopperArea) error {
	planned := append([]pcbModuleRoute(nil), bundle.SignalRoutes...)
	planned = append(planned, bundle.GroundRoutes...)
	combined := append([]pcbTrack(nil), existing...)
	for _, arc := range arcs {
		curve, _, err := flattenNetPathArc(arc)
		if err != nil {
			return fmt.Errorf("crystal-guard existing arc %s geometry is unknown: %w", arc.ID, err)
		}
		for i := 0; i+1 < len(curve); i++ {
			combined = append(combined, pcbTrack{
				ID: fmt.Sprintf("arc:%s:%d", arc.ID, i), Net: arc.Net, Layer: arc.Layer,
				X1: curve[i].x, Y1: curve[i].y, X2: curve[i+1].x, Y2: curve[i+1].y, Width: arc.Width,
			})
		}
	}
	for _, route := range planned {
		for i := 0; i+1 < len(route.Points); i++ {
			a, b := route.Points[i], route.Points[i+1]
			combined = append(combined, pcbTrack{ID: fmt.Sprintf("planned:%s:%d", route.ID, i), Net: route.Net, Layer: route.Layer, X1: a[0], Y1: a[1], X2: b[0], Y2: b[1], Width: route.WidthMil})
			for _, area := range areas {
				if area.Layer != route.Layer || area.Net == route.Net {
					continue
				}
				if copperAreaSegmentDistance(area, a, b) < route.WidthMil/2+snap.Rules.ClearanceMil-netPathGeomEps {
					return fmt.Errorf("crystal-guard planned route %s conflicts with other-net %s %s (%s) on layer %d", route.ID, area.Kind, area.ID, area.Net, area.Layer)
				}
			}
		}
	}
	for _, via := range vias {
		if !strings.HasPrefix(via.ID, "planned:") {
			continue
		}
		for _, area := range areas {
			if area.Net == via.Net {
				continue
			}
			if copperAreaPointDistance(area, [2]float64{via.X, via.Y}) < via.Dia/2+snap.Rules.ClearanceMil-netPathGeomEps {
				return fmt.Errorf("crystal-guard planned via %s conflicts with other-net %s %s (%s) on layer %d", strings.TrimPrefix(via.ID, "planned:"), area.Kind, area.ID, area.Net, area.Layer)
			}
		}
	}
	findings := findClearanceViolations(combined, pads, vias, nil, snap.Rules.ClearanceMil, snap.Rules.ClearanceTrackTrackMil)
	var relevant []string
	for _, f := range findings {
		for _, id := range f.Primitives {
			if strings.HasPrefix(id, "planned:") {
				relevant = append(relevant, f.Message)
				break
			}
		}
	}
	if len(relevant) > 0 {
		return fmt.Errorf("crystal-guard planned copper violates clearance: %s", strings.Join(uniqueStrings(relevant), "; "))
	}
	return nil
}

func copperAreaSegmentDistance(area pcbCopperArea, a, b [2]float64) float64 {
	if compoundWinding(area.Contours, a) != 0 || compoundWinding(area.Contours, b) != 0 {
		return 0
	}
	best := math.Inf(1)
	for _, contour := range area.Contours {
		for i, p := range contour {
			q := contour[(i+1)%len(contour)]
			best = math.Min(best, segSegDist(a[0], a[1], b[0], b[1], p[0], p[1], q[0], q[1]))
		}
	}
	return best
}

func crystalCandidatePads(snap *boardSnapshot, moved map[string]boardComp) ([]pcbPadP, error) {
	var pads []pcbPadP
	for _, original := range snap.Components {
		c := original
		if replacement, ok := moved[original.Designator]; ok {
			c = replacement
		}
		for _, p := range c.Pads {
			parsed := pcbPadP{ID: p.ID, Designator: c.Designator, Number: p.Number, Net: p.Net, Layer: p.Layer, X: p.X, Y: p.Y, W: p.W, H: p.H, Rotation: p.Rotation}
			if err := parseNetPathPadShape(map[string]any{"shape": p.Shape, "specialPad": p.SpecialPad}, &parsed, c.Designator+"."+p.Number); err != nil {
				return nil, fmt.Errorf("crystal-guard pad %s.%s geometry is unknown: %w", c.Designator, p.Number, err)
			}
			if !parsed.ShapeOK {
				return nil, fmt.Errorf("crystal-guard pad %s.%s geometry is unknown/unsupported: %s", c.Designator, p.Number, parsed.ShapeIssue)
			}
			pads = append(pads, parsed)
		}
	}
	return pads, nil
}

func rebuildCrystalApply(candidate *pcbLayoutCandidate, g *pcbCrystalGuardSpec, bundle *pcbLayoutModuleBundle) {
	placements := append([]pcbLayoutTypedAction(nil), candidate.Actions[:maxInt(0, len(candidate.Actions)-1)]...)
	placementSteps := append([]playbookStep(nil), candidate.Apply.Steps[:maxInt(0, len(candidate.Apply.Steps)-1)]...)
	candidate.Actions = nil
	candidate.Apply.Steps = nil
	candidate.Apply.RequireFullExecution = true
	add := func(id, name, action string, payload map[string]any, capture bool) {
		candidate.Actions = append(candidate.Actions, pcbLayoutTypedAction{ID: id, Action: action, Payload: payload})
		step := playbookStep{ID: id, Name: name, Action: action, Payload: payload}
		if capture {
			step.Capture = map[string]string{strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_PID": "$.primitiveId"}
		}
		candidate.Apply.Steps = append(candidate.Apply.Steps, step)
	}
	if len(bundle.ReplacePrimitiveIDs) > 0 {
		add("unlock-old-osc", "unlock old OSC copper", "pcb.track.lock", map[string]any{"primitiveIds": bundle.ReplacePrimitiveIDs, "locked": false}, false)
		// No kind guard: the exact replacement set deliberately covers both tracks
		// and arcs, and pcb.route.delete classifies each fresh primitive by id.
		add("delete-old-osc", "delete exact fresh OSC track/arc set", "pcb.route.delete", map[string]any{"primitiveIds": bundle.ReplacePrimitiveIDs}, false)
	}
	candidate.Actions = append(candidate.Actions, placements...)
	candidate.Apply.Steps = append(candidate.Apply.Steps, placementSteps...)
	for _, route := range append(append([]pcbModuleRoute(nil), bundle.SignalRoutes...), bundle.GroundRoutes...) {
		for i := 0; i+1 < len(route.Points); i++ {
			a, b := route.Points[i], route.Points[i+1]
			id := fmt.Sprintf("route-%s-%02d", route.ID, i+1)
			add(id, "create "+route.ID, "pcb.line.create", map[string]any{"net": route.Net, "layer": route.Layer, "lineWidth": route.WidthMil, "startX": a[0], "startY": a[1], "endX": b[0], "endY": b[1]}, true)
		}
	}
	for _, region := range bundle.Regions {
		add("region-"+region.ID, "create "+region.ID, "pcb.region.create", map[string]any{"layer": region.Layer, "points": region.Points, "ruleType": region.RuleTypes, "locked": true}, true)
	}
	for _, via := range bundle.Vias {
		if via.ExistingPrimitiveID != "" {
			continue
		}
		add("via-"+via.ID, "create "+via.ID, "pcb.via.create", map[string]any{"net": via.Net, "x": via.X, "y": via.Y, "holeDiameter": via.HoleMil, "diameter": via.DiameterMil}, true)
	}
	for _, pour := range bundle.Pours {
		add("pour-"+pour.ID, "create "+pour.ID, "pcb.pour.create", map[string]any{"net": pour.Net, "layer": pour.Layer, "points": pour.Points, "fill": "solid"}, true)
	}
	if len(bundle.Pours) > 0 {
		add("rebuild-pours", "rebuild materialized copper", "pcb.pour.rebuild", map[string]any{"net": g.GroundNet}, false)
	}
	add("save", "save PCB checkpoint", "pcb.save", map[string]any{}, false)
}

func crystal45Path(from, to [2]float64) [][2]float64 {
	return pcbrouting.Direct45(from, to)
}

func unionLayoutBBox(a, b *layoutBBox) *layoutBBox {
	if b == nil {
		return a
	}
	if a == nil {
		c := *b
		return &c
	}
	a.MinX, a.MinY = math.Min(a.MinX, b.MinX), math.Min(a.MinY, b.MinY)
	a.MaxX, a.MaxY = math.Max(a.MaxX, b.MaxX), math.Max(a.MaxY, b.MaxY)
	return a
}

func expandLayoutBBox(b layoutBBox, margin float64) layoutBBox {
	return layoutBBox{MinX: b.MinX - margin, MinY: b.MinY - margin, MaxX: b.MaxX + margin, MaxY: b.MaxY + margin}
}

func rectPoints(b layoutBBox) [][2]float64 {
	return [][2]float64{{b.MinX, b.MinY}, {b.MaxX, b.MinY}, {b.MaxX, b.MaxY}, {b.MinX, b.MaxY}}
}

// crystalRingPours builds an explicit four-slab ring on each copper layer.
// A single pour boundary covering a concave no-pours contour proved unsafe in
// live rebuild: copper remained inside a lateral extension of the sensitive
// contour.  Keeping every boundary outside the expanded no-pours bbox makes
// the absence of local poured copper structural rather than dependent on the
// host's region clipping behavior.
func crystalRingPours(outer layoutBBox, noPourPoints [][2]float64, clearance float64, groundNet string) ([]pcbModulePour, error) {
	if !allFinite(outer.MinX, outer.MinY, outer.MaxX, outer.MaxY, clearance) || outer.MinX >= outer.MaxX || outer.MinY >= outer.MaxY {
		return nil, fmt.Errorf("outer boundary is invalid")
	}
	if len(noPourPoints) < 3 {
		return nil, fmt.Errorf("no-pours contour has fewer than three points")
	}
	if clearance < 0 {
		return nil, fmt.Errorf("clearance must be >= 0")
	}
	if strings.TrimSpace(groundNet) == "" {
		return nil, fmt.Errorf("ground net is empty")
	}
	for _, p := range noPourPoints {
		if !allFinite(p[0], p[1]) {
			return nil, fmt.Errorf("no-pours contour contains a non-finite point")
		}
	}
	inner := expandLayoutBBox(pointsBBox(noPourPoints), clearance)
	if outer.MinX >= inner.MinX-netPathGeomEps || outer.MinY >= inner.MinY-netPathGeomEps ||
		outer.MaxX <= inner.MaxX+netPathGeomEps || outer.MaxY <= inner.MaxY+netPathGeomEps {
		return nil, fmt.Errorf("outer boundary %+v does not strictly enclose no-pours bbox %+v plus %.3fmil clearance", outer, pointsBBox(noPourPoints), clearance)
	}
	parts := []struct {
		name string
		box  layoutBBox
	}{
		{name: "left", box: layoutBBox{MinX: outer.MinX, MinY: outer.MinY, MaxX: inner.MinX, MaxY: outer.MaxY}},
		{name: "right", box: layoutBBox{MinX: inner.MaxX, MinY: outer.MinY, MaxX: outer.MaxX, MaxY: outer.MaxY}},
		{name: "min-y", box: layoutBBox{MinX: inner.MinX, MinY: outer.MinY, MaxX: inner.MaxX, MaxY: inner.MinY}},
		{name: "max-y", box: layoutBBox{MinX: inner.MinX, MinY: inner.MaxY, MaxX: inner.MaxX, MaxY: outer.MaxY}},
	}
	result := make([]pcbModulePour, 0, 8)
	for _, layer := range []int{1, 2} {
		layerName := "top"
		if layer == 2 {
			layerName = "bottom"
		}
		for _, part := range parts {
			if part.box.MaxX-part.box.MinX <= netPathGeomEps || part.box.MaxY-part.box.MinY <= netPathGeomEps {
				return nil, fmt.Errorf("%s ring slab %s has no area", layerName, part.name)
			}
			result = append(result, pcbModulePour{
				ID: "crystal-ground-" + layerName + "-" + part.name, Net: groundNet, Layer: layer, Points: rectPoints(part.box),
			})
		}
	}
	return result, nil
}

func moduleRouteLength(points [][2]float64) float64 {
	total := 0.0
	for i := 0; i+1 < len(points); i++ {
		total += math.Hypot(points[i+1][0]-points[i][0], points[i+1][1]-points[i][1])
	}
	return total
}

func moduleRouteTurns(points [][2]float64) int {
	turns := 0
	for i := 1; i+1 < len(points); i++ {
		ax, ay := points[i][0]-points[i-1][0], points[i][1]-points[i-1][1]
		bx, by := points[i+1][0]-points[i][0], points[i+1][1]-points[i][1]
		if math.Abs(ax*by-ay*bx) > netPathGeomEps {
			turns++
		}
	}
	return turns
}

func nearestUnusedPoint(points [][2]float64, target [2]float64, used map[int]bool) int {
	best, bestDistance := -1, math.Inf(1)
	for i, point := range points {
		if used[i] {
			continue
		}
		if d := math.Hypot(point[0]-target[0], point[1]-target[1]); d < bestDistance {
			best, bestDistance = i, d
		}
	}
	return best
}

func nearestPointOnModuleRoutes(routes []pcbModuleRoute, role string, target [2]float64) ([2]float64, bool) {
	best, bestDistance, found := [2]float64{}, math.Inf(1), false
	for _, route := range routes {
		if route.Role != role {
			continue
		}
		for i := 0; i+1 < len(route.Points); i++ {
			p, q := route.Points[i], route.Points[i+1]
			dx, dy := q[0]-p[0], q[1]-p[1]
			den := dx*dx + dy*dy
			t := 0.0
			if den > 0 {
				t = ((target[0]-p[0])*dx + (target[1]-p[1])*dy) / den
				t = math.Max(0, math.Min(1, t))
			}
			point := [2]float64{p[0] + t*dx, p[1] + t*dy}
			if d := math.Hypot(point[0]-target[0], point[1]-target[1]); d < bestDistance {
				best, bestDistance, found = point, d, true
			}
		}
	}
	return best, found
}

func crystalGuardSignalWindows(routes []pcbModuleRoute, y, halfWidth float64) [][2]float64 {
	var windows [][2]float64
	for _, route := range routes {
		if route.Role != "signal-main" {
			continue
		}
		before := len(windows)
		for i := 0; i+1 < len(route.Points); i++ {
			p, q := route.Points[i], route.Points[i+1]
			if (y < math.Min(p[1], q[1])-netPathGeomEps) || (y > math.Max(p[1], q[1])+netPathGeomEps) {
				continue
			}
			x := p[0]
			if math.Abs(q[1]-p[1]) > netPathGeomEps {
				t := (y - p[1]) / (q[1] - p[1])
				x = p[0] + t*(q[0]-p[0])
			} else {
				windows = append(windows, [2]float64{math.Min(p[0], q[0]) - halfWidth, math.Max(p[0], q[0]) + halfWidth})
				continue
			}
			windows = append(windows, [2]float64{x - halfWidth, x + halfWidth})
		}
		// Once the sensitive envelope includes the complete signal-main stroke,
		// its outer guard can lie beyond the owner-pad endpoint. Preserve an entry
		// opening by projecting that declared owner-side endpoint onto the guard.
		if len(windows) == before && len(route.Points) > 0 {
			x := route.Points[0][0]
			windows = append(windows, [2]float64{x - halfWidth, x + halfWidth})
		}
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i][0] < windows[j][0] })
	merged := make([][2]float64, 0, len(windows))
	for _, window := range windows {
		if len(merged) == 0 || window[0] > merged[len(merged)-1][1]+netPathGeomEps {
			merged = append(merged, window)
			continue
		}
		merged[len(merged)-1][1] = math.Max(merged[len(merged)-1][1], window[1])
	}
	// Multiple close signal entries form one opening. Keeping copper between two
	// openings would create an isolated guard sliver with no route to GND.
	if len(merged) > 1 {
		return [][2]float64{{merged[0][0], merged[len(merged)-1][1]}}
	}
	return merged
}
