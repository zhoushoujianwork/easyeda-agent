package app

// Reflow is a bounded, offline placement search. Rendered boxes are used only
// for component occupancy; routed copper uses the measured pad/track geometry.
// The caller must still jointly solve escape channels and regenerated module
// protection, and apply Copper together with Placements, never placements alone.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type pcbReflowSpec struct {
	Groups                    []pcbReflowGroupSpec   `json:"groups"`
	FixedRefs                 []string               `json:"fixedRefs,omitempty"`
	FixedAxes                 map[string][]string    `json:"fixedAxes,omitempty"`
	MaxStates                 int                    `json:"maxStates"`
	MaxShiftXMil              float64                `json:"maxShiftXMil"`
	MaxShiftYMil              float64                `json:"maxShiftYMil"`
	StepMil                   float64                `json:"stepMil"`
	DirectionPriority         []string               `json:"directionPriority,omitempty"`
	MaxCandidates             int                    `json:"maxCandidates,omitempty"`
	TargetReplacePrimitiveIDs []string               `json:"targetReplacePrimitiveIds,omitempty"`
	Reservations              []pcbReflowReservation `json:"reservations,omitempty"`
}

// Reservations are planning geometry only. They do not become copper actions.
// Distinct nets must fit simultaneously; common-net intersections are allowed.
type pcbReflowReservation struct {
	ID           string       `json:"id"`
	Net          string       `json:"net"`
	Layer        int          `json:"layer"`
	WidthMil     float64      `json:"widthMil"`
	ClearanceMil float64      `json:"clearanceMil"`
	Points       [][2]float64 `json:"points"`
}

type pcbReflowGroupSpec struct {
	ID                   string                        `json:"id"`
	Refs                 []string                      `json:"refs"`
	AnchorRef            string                        `json:"anchorRef"`
	FixedAxes            []string                      `json:"fixedAxes,omitempty"`
	AllowedRotationsDeg  []float64                     `json:"allowedRotationsDeg,omitempty"`
	InternalPrimitiveIDs []string                      `json:"internalPrimitiveIds,omitempty"`
	ExternalConnections  []pcbReflowExternalConnection `json:"externalConnections,omitempty"`
}

// An external connection names electrical endpoints, not precomputed positions.
// From is a group member pad; To is a fixed outside pad. Unlisted copper touching
// a member/internal primitive makes the group immovable. This does not infer
// module ownership from a common net or from a rendered bounding box.
type pcbReflowExternalConnection struct {
	ID                  string   `json:"id"`
	From                string   `json:"from"`
	To                  string   `json:"to"`
	Net                 string   `json:"net"`
	Layer               int      `json:"layer"`
	WidthMil            float64  `json:"widthMil"`
	ReplacePrimitiveIDs []string `json:"replacePrimitiveIds,omitempty"`
}

type pcbReflowReport struct {
	Status      string               `json:"status"`
	States      int                  `json:"states"`
	Budget      int                  `json:"budget"`
	Exhausted   bool                 `json:"exhausted"`
	Candidates  []pcbReflowCandidate `json:"candidates,omitempty"`
	FixedGroups map[string]string    `json:"fixedGroups,omitempty"`
	Rejected    []string             `json:"rejected,omitempty"`
	Limitations []string             `json:"limitations"`
}

type pcbReflowCandidate struct {
	Variant string              `json:"variant"`
	Moves   []pcbReflowMove     `json:"moves"`
	Copper  pcbReflowCopperPlan `json:"copper"`
}

type pcbReflowMove struct {
	Group            string   `json:"group"`
	Refs             []string `json:"refs"`
	Reason           string   `json:"reason"`
	PivotXMil        float64  `json:"pivotXMil"`
	PivotYMil        float64  `json:"pivotYMil"`
	DXMil            float64  `json:"dxMil"`
	DYMil            float64  `json:"dyMil"`
	RotationDeltaDeg float64  `json:"rotationDeltaDeg"`
}

type pcbReflowCopperPlan struct {
	ReplacePrimitiveIDs []string         `json:"replacePrimitiveIds,omitempty"`
	Routes              []pcbModuleRoute `json:"routes,omitempty"`
	Vias                []pcbModuleVia   `json:"vias,omitempty"`
}

type pcbReflowState struct {
	comps map[string]boardComp
	moves map[string]pcbReflowMove
}

type pcbReflowContext struct {
	spec              pcbReflowSpec
	snap              *boardSnapshot
	base              map[string]boardComp
	owner             map[string]string
	groups            map[string]pcbReflowGroupSpec
	target            map[string]bool
	fixed             map[string]bool
	tracks            []pcbTrack
	arcs              []pcbArc
	vias              []pcbViaP
	areas             []pcbCopperArea
	strokes           []pcbTrack
	pads              []pcbPadP
	minGap            float64
	copperErr         string
	unsupportedGroups map[string]bool
	blockedUnknown    bool
	regions           []pcbReflowRegion
	report            pcbReflowReport
}

type pcbReflowRegion struct {
	area         pcbCopperArea
	noComponents bool
	noWires      bool
}

// resolvePCBReflow treats target as a fixed desired placement seed. The outer
// planner enumerates target offsets/orientations; this inner search recursively
// moves declared blocking modules and backtracks on fixed obstacles/board edges.
// It never mutates target, snap, or spec, and never visits the editor.
func resolvePCBReflow(spec pcbReflowSpec, target pcbLayoutVariant, snap *boardSnapshot, minGap float64) ([]pcbLayoutVariant, pcbReflowReport, error) {
	ctx, err := newPCBReflowContext(spec, target, snap, minGap)
	if err != nil {
		return nil, pcbReflowReport{Status: "invalid"}, err
	}
	state := pcbReflowState{comps: map[string]boardComp{}, moves: map[string]pcbReflowMove{}}
	for ref, c := range target.comps {
		state.comps[ref] = c
	}
	var variants []pcbLayoutVariant
	visited := map[string]bool{}
	var search func(pcbReflowState)
	search = func(s pcbReflowState) {
		if len(variants) >= ctx.spec.MaxCandidates {
			return
		}
		key := pcbReflowStateKey(s)
		if visited[key] {
			return
		}
		if ctx.report.States >= ctx.spec.MaxStates {
			ctx.report.Exhausted = true
			return
		}
		visited[key] = true
		ctx.report.States++
		groups, reason := ctx.nextCollision(s)
		if reason != "" && len(groups) == 0 {
			ctx.reject(reason)
			return
		}
		if len(groups) == 0 {
			copper, err := ctx.copperPlan(s)
			if err != nil {
				ctx.reject(err.Error())
				return
			}
			label := fmt.Sprintf("%s-reflow-%d", target.label, len(variants)+1)
			variants = append(variants, pcbLayoutVariant{label: label, comps: s.comps})
			evidence := pcbReflowCandidate{Variant: label, Copper: copper}
			for _, groupID := range pcbReflowSortedMoveKeys(s.moves) {
				evidence.Moves = append(evidence.Moves, s.moves[groupID])
			}
			ctx.report.Candidates = append(ctx.report.Candidates, evidence)
			return
		}
		for _, groupID := range groups {
			// Each recursion level adds a whole previously unchosen group.
			// Repositioning a group already in this branch is handled by its
			// caller's next transform, bounding stack depth by group count and
			// avoiding long oscillating chains within the same group.
			if _, chosen := s.moves[groupID]; chosen {
				continue
			}
			if fixedReason, fixed := ctx.report.FixedGroups[groupID]; fixed {
				ctx.reject(groupID + ": " + fixedReason)
				ctx.blockedUnknown = ctx.blockedUnknown || ctx.unsupportedGroups[groupID]
				continue
			}
			group := ctx.groups[groupID]
			for _, move := range ctx.groupTransforms(group, reason) {
				next, ok := ctx.movedState(s, group, move)
				if !ok || pcbReflowStateKey(next) == key {
					continue
				}
				search(next)
				if ctx.report.Exhausted || len(variants) >= ctx.spec.MaxCandidates {
					return
				}
			}
		}
	}
	search(state)
	ctx.report.Status = "no-candidate-in-search"
	if len(variants) > 0 {
		ctx.report.Status = "candidates"
	}
	if ctx.report.Exhausted || ctx.copperErr != "" || len(variants) == 0 && ctx.blockedUnknown {
		ctx.report.Status = "incomplete"
	}
	return variants, ctx.report, nil
}

func newPCBReflowContext(spec pcbReflowSpec, target pcbLayoutVariant, snap *boardSnapshot, minGap float64) (*pcbReflowContext, error) {
	if snap == nil || snap.Outline == nil || snap.Outline.Source != "polygon" || len(snap.Outline.Points) < 3 {
		return nil, fmt.Errorf("reflow requires a measured polygon board outline")
	}
	for _, point := range snap.Outline.Points {
		if !allFinite(point[0], point[1]) {
			return nil, fmt.Errorf("reflow board outline has nonfinite coordinates")
		}
	}
	if spec.MaxCandidates == 0 {
		spec.MaxCandidates = 3
	}
	if spec.MaxStates < 1 || spec.MaxStates > 100000 || spec.MaxCandidates < 1 || spec.MaxCandidates > 3 || !allFinite(spec.StepMil, spec.MaxShiftXMil, spec.MaxShiftYMil, minGap) || spec.StepMil <= 0 || spec.MaxShiftXMil < 0 || spec.MaxShiftYMil < 0 || minGap < 0 {
		return nil, fmt.Errorf("reflow requires finite nonnegative ranges/gap, positive step, maxStates 1..100000 and maxCandidates 1..3")
	}
	// Reject a huge transform lattice before allocation, independently of DFS's
	// state budget. This is an input guard, not silent truncation of the search.
	if (2*math.Floor(spec.MaxShiftXMil/spec.StepMil)+1)*(2*math.Floor(spec.MaxShiftYMil/spec.StepMil)+1) > 10000 {
		return nil, fmt.Errorf("reflow offset lattice exceeds 10000 points; increase step or reduce ranges")
	}
	if len(spec.DirectionPriority) == 0 {
		spec.DirectionPriority = []string{"down", "left", "right", "up"}
	}
	directions := map[string]bool{}
	for _, direction := range spec.DirectionPriority {
		if direction != "down" && direction != "left" && direction != "right" && direction != "up" || directions[direction] {
			return nil, fmt.Errorf("directionPriority must contain distinct down/left/right/up values")
		}
		directions[direction] = true
	}
	if len(directions) != 4 {
		return nil, fmt.Errorf("directionPriority must order all four directions")
	}
	reservationIDs := map[string]bool{}
	for _, r := range spec.Reservations {
		if r.ID == "" || r.Net == "" || reservationIDs[r.ID] || !netPathCopperLayer(r.Layer) || !allFinite(r.WidthMil, r.ClearanceMil) || r.WidthMil <= 0 || r.ClearanceMil < 0 || len(r.Points) < 2 || len(r.Points) > 4096 {
			return nil, fmt.Errorf("reflow reservations require unique id, net, layer, positive width, nonnegative clearance and 2..4096 points")
		}
		reservationIDs[r.ID] = true
		for _, p := range r.Points {
			if !allFinite(p[0], p[1]) {
				return nil, fmt.Errorf("reservation %s has nonfinite point", r.ID)
			}
		}
	}
	ctx := &pcbReflowContext{spec: spec, snap: snap, minGap: minGap, base: map[string]boardComp{}, owner: map[string]string{}, groups: map[string]pcbReflowGroupSpec{}, target: map[string]bool{}, fixed: map[string]bool{}, unsupportedGroups: map[string]bool{}}
	ctx.report = pcbReflowReport{Budget: spec.MaxStates, FixedGroups: map[string]string{}, Limitations: []string{"Rendered bboxes prove component occupancy only. Final escape-channel, protection, rebuilt-pour and live persistence checks remain required."}}
	for _, c := range snap.Components {
		if c.Designator == "" || ctx.base[c.Designator].ID != "" || c.ID == "" || c.BBox == nil || !allFinite(c.X, c.Y, c.Rotation, c.BBox.MinX, c.BBox.MinY, c.BBox.MaxX, c.BBox.MaxY) {
			return nil, fmt.Errorf("reflow component %q has missing/duplicate identity or unknown geometry", c.Designator)
		}
		ctx.base[c.Designator] = c
	}
	if len(target.comps) == 0 {
		return nil, fmt.Errorf("reflow target is empty")
	}
	for ref, c := range target.comps {
		base, ok := ctx.base[ref]
		if !ok || c.ID != base.ID || c.Designator != ref || c.BBox == nil || !allFinite(c.X, c.Y, c.Rotation) {
			return nil, fmt.Errorf("reflow target %s is not a measured board component", ref)
		}
		ctx.target[ref] = true
	}
	for _, ref := range spec.FixedRefs {
		if _, ok := ctx.base[ref]; !ok {
			return nil, fmt.Errorf("fixed ref %s is absent", ref)
		}
		ctx.fixed[ref] = true
	}
	for ref, axes := range spec.FixedAxes {
		if _, ok := ctx.base[ref]; !ok {
			return nil, fmt.Errorf("fixed-axis ref %s is absent", ref)
		}
		if err := pcbReflowValidateAxes(axes); err != nil {
			return nil, err
		}
	}
	primitiveOwner := map[string]string{}
	for _, group := range spec.Groups {
		if group.ID == "" || len(group.Refs) == 0 || ctx.groups[group.ID].ID != "" {
			return nil, fmt.Errorf("reflow groups require distinct ids and nonempty refs")
		}
		if err := pcbReflowValidateAxes(group.FixedAxes); err != nil {
			return nil, err
		}
		anchorFound := false
		for _, ref := range group.Refs {
			if _, ok := ctx.base[ref]; !ok || ctx.owner[ref] != "" || ctx.target[ref] {
				return nil, fmt.Errorf("group %s ref %s is absent, duplicated, or also a target member", group.ID, ref)
			}
			ctx.owner[ref] = group.ID
			anchorFound = anchorFound || ref == group.AnchorRef
		}
		if !anchorFound {
			return nil, fmt.Errorf("group %s anchorRef must be a member", group.ID)
		}
		for _, rotation := range group.AllowedRotationsDeg {
			if !isFinite(rotation) || math.Abs(rotation/90-math.Round(rotation/90)) > 1e-8 {
				return nil, fmt.Errorf("group %s allows only finite orthogonal rotations", group.ID)
			}
		}
		if len(group.AllowedRotationsDeg) > 4 {
			return nil, fmt.Errorf("group %s allows at most four orthogonal rotations", group.ID)
		}
		ids := append([]string(nil), group.InternalPrimitiveIDs...)
		for _, external := range group.ExternalConnections {
			if external.ID == "" || external.Net == "" || !netPathCopperLayer(external.Layer) || !isFinite(external.WidthMil) || external.WidthMil <= 0 {
				return nil, fmt.Errorf("group %s has incomplete external connection", group.ID)
			}
			fromRef, _, e1 := splitPadRef(external.From)
			toRef, _, e2 := splitPadRef(external.To)
			if e1 != nil || e2 != nil || !pcbReflowContains(group.Refs, fromRef) || pcbReflowContains(group.Refs, toRef) {
				return nil, fmt.Errorf("group %s external connection must join a member pad to an outside pad", group.ID)
			}
			ids = append(ids, external.ReplacePrimitiveIDs...)
		}
		for _, id := range ids {
			if id == "" || primitiveOwner[id] != "" || pcbReflowContains(spec.TargetReplacePrimitiveIDs, id) {
				return nil, fmt.Errorf("primitive %s has duplicate or missing reflow ownership", id)
			}
			primitiveOwner[id] = group.ID
		}
		ctx.groups[group.ID] = group
	}
	for _, group := range spec.Groups {
		for _, external := range group.ExternalConnections {
			toRef, _, _ := splitPadRef(external.To)
			if ctx.owner[toRef] != "" || ctx.target[toRef] {
				return nil, fmt.Errorf("group %s external endpoint %s must stay fixed; intermoving-group reconnection is unsupported", group.ID, external.To)
			}
		}
	}
	ctx.readCopper()
	ctx.readRegions()
	for _, group := range spec.Groups {
		if err := ctx.validateOwnership(group); err != nil {
			ctx.report.FixedGroups[group.ID] = err.Error()
			ctx.unsupportedGroups[group.ID] = true
			for _, ref := range group.Refs {
				if ctx.fixed[ref] || ctx.base[ref].Locked {
					ctx.unsupportedGroups[group.ID] = false
				}
			}
		}
	}
	return ctx, nil
}

func (ctx *pcbReflowContext) readCopper() {
	copper := ctx.snap.Copper
	if copper == nil || ctx.snap.Rules == nil || ctx.snap.Rules.Source != "live" {
		ctx.copperErr = "measured copper and live rules are required"
		return
	}
	rules := ctx.snap.Rules
	if !allFinite(rules.ClearanceMil, rules.ClearanceTrackTrackMil, rules.CopperToEdgeMil, rules.TrackWidthMinMil, rules.ViaDrillMil, rules.ViaDiameterMil) || rules.ClearanceMil <= 0 || rules.ClearanceTrackTrackMil < 0 || rules.CopperToEdgeMil < 0 || rules.TrackWidthMinMil < 0 || rules.ViaDrillMil < 0 || rules.ViaDiameterMil < 0 {
		ctx.copperErr = "live copper rules contain missing/invalid clearance or dimensions"
		return
	}
	for _, category := range []string{"routing", "vias", "regions", "fills", "pours", "poured"} {
		if copper.Availability[category] != "available" {
			ctx.copperErr = category + " copper availability is unknown"
			return
		}
	}
	var err error
	ctx.tracks, ctx.arcs, ctx.vias, err = crystalSnapshotRouting(ctx.snap)
	if err == nil {
		ctx.areas, ctx.strokes, err = parseModuleCopperAreasAndStrokes(copper.Fills, copper.Pours, copper.Poured)
	}
	if err == nil {
		ctx.pads, err = boardSnapshotNetPathPads(ctx.snap)
	}
	if err == nil {
		for _, p := range ctx.pads {
			if _, err = pcbExactPadSegmentGap(p, [2]float64{p.X, p.Y}, [2]float64{p.X, p.Y}); err != nil {
				break
			}
		}
	}
	if err != nil {
		ctx.copperErr = "exact copper geometry unavailable: " + err.Error()
	}
}

func (ctx *pcbReflowContext) validateOwnership(group pcbReflowGroupSpec) error {
	for _, ref := range group.Refs {
		if ctx.fixed[ref] || ctx.base[ref].Locked {
			return fmt.Errorf("member %s is fixed or locked", ref)
		}
	}
	if ctx.copperErr != "" {
		return fmt.Errorf("%s", ctx.copperErr)
	}
	owned := map[string]bool{}
	internal := map[string]bool{}
	for _, id := range group.InternalPrimitiveIDs {
		owned[id], internal[id] = true, true
	}
	for _, external := range group.ExternalConnections {
		for _, id := range external.ReplacePrimitiveIDs {
			owned[id] = true
		}
		for _, endpoint := range []string{external.From, external.To} {
			p, err := pcbReflowPad(ctx.base, endpoint)
			if err != nil || p.Net != external.Net || !padLayerMatches(p.Layer, external.Layer) {
				return fmt.Errorf("external connection %s endpoint %s has unknown/mismatched net or layer", external.ID, endpoint)
			}
		}
	}
	known := map[string]bool{}
	for _, track := range ctx.tracks {
		known[track.ID] = true
		if owned[track.ID] && track.Net == "" {
			return fmt.Errorf("owned track %s has unknown net", track.ID)
		}
	}
	for _, via := range ctx.vias {
		known[via.ID] = true
		if owned[via.ID] && via.Net == "" {
			return fmt.Errorf("owned via %s has unknown net", via.ID)
		}
	}
	for id := range owned {
		if !known[id] {
			return fmt.Errorf("owned primitive %s is missing or not a reconstructible track/via; arc/fill/region/pour movement requires its own rebuild model", id)
		}
	}
	for _, external := range group.ExternalConnections {
		var oldTracks []pcbTrack
		var oldVias []pcbViaP
		for _, id := range external.ReplacePrimitiveIDs {
			for _, track := range ctx.tracks {
				if track.ID == id && (track.Net != external.Net || track.Layer != external.Layer) {
					return fmt.Errorf("external replacement %s has different net/layer from connection %s", id, external.ID)
				}
				if track.ID == id {
					oldTracks = append(oldTracks, track)
				}
			}
			for _, via := range ctx.vias {
				if via.ID == id && via.Net != external.Net {
					return fmt.Errorf("external replacement via %s has different net", id)
				}
				if via.ID == id {
					oldVias = append(oldVias, via)
				}
			}
		}
		if len(external.ReplacePrimitiveIDs) > 0 {
			proof, err := analyzePcbNetPath(ctx.pads, oldTracks, nil, oldVias, pcbNetPathOptions{From: external.From, To: external.To, Net: external.Net})
			if err != nil || !proof.Connected {
				return fmt.Errorf("external connection %s replacement copper does not prove its declared endpoints", external.ID)
			}
			used := map[string]bool{}
			for _, step := range proof.Path {
				used[step.PrimitiveID] = true
			}
			for _, id := range external.ReplacePrimitiveIDs {
				if !used[id] {
					return fmt.Errorf("external connection %s includes branch/unrelated primitive %s; declare each branch separately", external.ID, id)
				}
			}
		}
	}
	// An explicit pad node map catches copper attached to the complete module,
	// including branches joined halfway along a declared internal track.
	var nodes []pcbNetPathNode
	nets := map[string]bool{}
	for _, ref := range group.Refs {
		for _, net := range ctx.base[ref].nets() {
			nets[net] = true
		}
	}
	for net := range nets {
		netNodes, _ := buildNetPathNodes(ctx.pads, ctx.tracks, ctx.arcs, ctx.vias, net, nil)
		nodes = append(nodes, netNodes...)
	}
	for _, a := range nodes {
		ownedA := internal[a.id] || a.kind == "pad" && pcbReflowContains(group.Refs, strings.SplitN(a.ref, ".", 2)[0])
		if !ownedA {
			continue
		}
		for _, b := range nodes {
			if a.id == b.id || owned[b.id] || b.kind == "pad" && pcbReflowContains(group.Refs, strings.SplitN(b.ref, ".", 2)[0]) {
				continue
			}
			touches := netPathNodesTouch(a, b, nil)
			if a.kind == "pad" && b.kind == "via" {
				touches = touches || pointTouchesPad(b.x, b.y, a, b.dia/2)
			}
			if touches {
				return fmt.Errorf("unowned attached %s %s requires explicit reconnection or ownership", b.kind, b.id)
			}
		}
	}
	for _, external := range group.ExternalConnections {
		var toNode pcbNetPathNode
		for _, node := range nodes {
			if node.kind == "pad" && strings.EqualFold(node.ref, external.To) {
				toNode = node
				break
			}
		}
		for _, a := range nodes {
			if !pcbReflowContains(external.ReplacePrimitiveIDs, a.id) {
				continue
			}
			for _, b := range nodes {
				if a.id == b.id || owned[b.id] || b.kind == "pad" && pcbReflowContains(group.Refs, strings.SplitN(b.ref, ".", 2)[0]) || b.id == toNode.id {
					continue
				}
				if netPathNodesTouch(a, b, nil) && !netPathNodesTouch(b, toNode, nil) {
					return fmt.Errorf("external connection %s has undeclared branch at %s; explicit reconnection required", external.ID, b.id)
				}
			}
		}
	}
	// Materialized/static areas cannot be rigidly copied. A move of a pad tied
	// directly to a plane needs an explicit plane/rebuild contract, not same-net
	// inference. Keep that module fixed in this kernel version.
	for _, ref := range group.Refs {
		for _, pad := range ctx.base[ref].Pads {
			for _, area := range ctx.areas {
				if padLayerMatches(pad.Layer, area.Layer) && copperAreaPointDistance(area, [2]float64{pad.X, pad.Y}) <= math.Hypot(pad.W, pad.H)/2+netPathGeomEps {
					return fmt.Errorf("member %s.%s touches or approaches static/materialized copper %s; plane rebuild ownership is required", ref, pad.Number, area.ID)
				}
			}
		}
	}
	return nil
}

func (ctx *pcbReflowContext) nextCollision(s pcbReflowState) ([]string, string) {
	if groups, reason := ctx.reservationCollision(s); reason != "" {
		return groups, reason
	}
	refs := sortedBoardCompKeys(s.comps)
	for _, ref := range refs {
		c := s.comps[ref]
		if !ctx.poseAllowed(ref, c) {
			return nil, ref + " violates fixed/locked/axis constraints"
		}
		if !bboxInsideBoardOutline(ctx.snap.Outline, *c.BBox) {
			return nil, ref + " leaves board outline"
		}
		for _, region := range ctx.regions {
			if !region.noComponents || !padLayerMatches(c.Layer, region.area.Layer) && region.area.Layer != pcbLayerMulti {
				continue
			}
			points := rectPoints(*c.BBox)
			for i, a := range points {
				b := points[(i+1)%len(points)]
				if copperAreaSegmentDistance(region.area, a, b) <= ctx.minGap+netPathGeomEps {
					return nil, ref + " intersects no-components region " + region.area.ID
				}
			}
			for _, contour := range region.area.Contours {
				for _, p := range contour {
					if rectPtDist(c.BBox.MinX, c.BBox.MinY, c.BBox.MaxX, c.BBox.MaxY, p[0], p[1]) == 0 {
						return nil, ref + " encloses no-components region " + region.area.ID
					}
				}
			}
		}
		for _, otherRef := range sortedBoardCompKeys(ctx.base) {
			if ref == otherRef || ctx.owner[ref] != "" && ctx.owner[ref] == ctx.owner[otherRef] {
				continue
			}
			other := ctx.base[otherRef]
			if moved, ok := s.comps[otherRef]; ok {
				other = moved
			}
			if !boardComponentsSharePlacementLayer(c, other) || layoutRectGap(*c.BBox, *other.BBox) >= ctx.minGap && !pcbReflowRectOverlap(*c.BBox, *other.BBox) {
				continue
			}
			var alternatives []string
			// Push the blocker first, then backtrack by changing the previously
			// moved group. Target members are never implicitly pushed.
			for _, blockedRef := range []string{otherRef, ref} {
				id := ctx.owner[blockedRef]
				if id != "" && !pcbReflowContains(alternatives, id) {
					alternatives = append(alternatives, id)
				}
			}
			return alternatives, ref + " blocks " + otherRef
		}
	}
	return nil, ""
}

func (ctx *pcbReflowContext) reservationCollision(s pcbReflowState) ([]string, string) {
	if len(ctx.spec.Reservations) == 0 {
		return nil, ""
	}
	if ctx.copperErr != "" {
		return nil, "reservation geometry unknown: " + ctx.copperErr
	}
	pads, err := crystalCandidatePads(ctx.snap, s.comps)
	if err != nil {
		return nil, err.Error()
	}
	blocked := func(ref, reason string) ([]string, string) {
		if id := ctx.owner[ref]; id != "" {
			return []string{id}, reason
		}
		return nil, reason
	}
	for i, r := range ctx.spec.Reservations {
		clearance := math.Max(r.ClearanceMil, ctx.snap.Rules.ClearanceMil)
		for k := 1; k < len(r.Points); k++ {
			a, b := r.Points[k-1], r.Points[k]
			if !pcbReflowSegmentInsideOutline(ctx.snap.Outline, a, b, r.WidthMil/2+ctx.snap.Rules.CopperToEdgeMil) {
				return nil, "reservation " + r.ID + " leaves board copper boundary"
			}
			for _, q := range ctx.spec.Reservations[i+1:] {
				if q.Layer != r.Layer || q.Net == r.Net {
					continue
				}
				gap := math.Max(clearance, q.ClearanceMil)
				for j := 1; j < len(q.Points); j++ {
					c, d := q.Points[j-1], q.Points[j]
					if segSegDist(a[0], a[1], b[0], b[1], c[0], c[1], d[0], d[1]) < r.WidthMil/2+q.WidthMil/2+gap-netPathGeomEps {
						return nil, "reservations " + r.ID + " and " + q.ID + " conflict jointly"
					}
				}
			}
			for _, pad := range pads {
				if pad.Net == r.Net || !padLayerMatches(pad.Layer, r.Layer) {
					continue
				}
				gap, e := pcbExactPadSegmentGap(pad, a, b)
				if e != nil {
					return nil, e.Error()
				}
				if gap < r.WidthMil/2+clearance-netPathGeomEps {
					return blocked(pad.Designator, "reservation "+r.ID+" blocked by pad "+pad.Designator+"."+pad.Number)
				}
			}
			for _, track := range ctx.tracks {
				if pcbReflowContains(ctx.spec.TargetReplacePrimitiveIDs, track.ID) || track.Net == r.Net || track.Layer != r.Layer {
					continue
				}
				owner, internal := ctx.primitiveGroup(track.ID)
				if move, ok := s.moves[owner]; ok {
					if !internal {
						continue
					} // rebuilt fixed-endpoint routes checked at leaf
					c, d := pcbReflowTransformPoint([2]float64{track.X1, track.Y1}, move), pcbReflowTransformPoint([2]float64{track.X2, track.Y2}, move)
					track.X1, track.Y1, track.X2, track.Y2 = c[0], c[1], d[0], d[1]
				}
				if segSegDist(a[0], a[1], b[0], b[1], track.X1, track.Y1, track.X2, track.Y2) < r.WidthMil/2+track.Width/2+clearance-netPathGeomEps {
					reason := "reservation " + r.ID + " blocked by track " + track.ID
					if owner != "" {
						return []string{owner}, reason
					}
					return nil, reason
				}
			}
			for _, via := range ctx.vias {
				if pcbReflowContains(ctx.spec.TargetReplacePrimitiveIDs, via.ID) || via.Net == r.Net {
					continue
				}
				owner, internal := ctx.primitiveGroup(via.ID)
				if move, ok := s.moves[owner]; ok {
					if !internal {
						continue
					}
					p := pcbReflowTransformPoint([2]float64{via.X, via.Y}, move)
					via.X, via.Y = p[0], p[1]
				}
				if segPtDist(via.X, via.Y, a[0], a[1], b[0], b[1]) < r.WidthMil/2+via.Dia/2+clearance-netPathGeomEps {
					reason := "reservation " + r.ID + " blocked by via " + via.ID
					if owner != "" {
						return []string{owner}, reason
					}
					return nil, reason
				}
			}
		}
	}
	return nil, ""
}

func (ctx *pcbReflowContext) primitiveGroup(id string) (string, bool) {
	for _, group := range ctx.spec.Groups {
		if pcbReflowContains(group.InternalPrimitiveIDs, id) {
			return group.ID, true
		}
		for _, e := range group.ExternalConnections {
			if pcbReflowContains(e.ReplacePrimitiveIDs, id) {
				return group.ID, false
			}
		}
	}
	return "", false
}

func (ctx *pcbReflowContext) groupTransforms(group pcbReflowGroupSpec, reason string) []pcbReflowMove {
	anchor := ctx.base[group.AnchorRef]
	rotations := group.AllowedRotationsDeg
	if len(rotations) == 0 {
		rotations = []float64{anchor.Rotation}
	}
	offsets := []pcbLayoutOffset{}
	nx, ny := int(math.Floor(ctx.spec.MaxShiftXMil/ctx.spec.StepMil)), int(math.Floor(ctx.spec.MaxShiftYMil/ctx.spec.StepMil))
	for iy := -ny; iy <= ny; iy++ {
		for ix := -nx; ix <= nx; ix++ {
			offsets = append(offsets, pcbLayoutOffset{XMil: float64(ix) * ctx.spec.StepMil, YMil: float64(iy) * ctx.spec.StepMil})
		}
	}
	rank := func(o pcbLayoutOffset) int {
		direction := "down"
		if o.XMil == 0 && o.YMil == 0 {
			return -1
		}
		if math.Abs(o.XMil) > math.Abs(o.YMil) {
			direction = "left"
			if o.XMil > 0 {
				direction = "right"
			}
		} else if o.YMil > 0 {
			direction = "up"
		}
		for i, d := range ctx.spec.DirectionPriority {
			if d == direction {
				return i
			}
		}
		return 4
	}
	sort.SliceStable(offsets, func(i, j int) bool {
		a, b := offsets[i], offsets[j]
		da, db := math.Abs(a.XMil)+math.Abs(a.YMil), math.Abs(b.XMil)+math.Abs(b.YMil)
		if da != db {
			return da < db
		}
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if a.YMil != b.YMil {
			return a.YMil < b.YMil
		}
		return a.XMil < b.XMil
	})
	var out []pcbReflowMove
	for _, rotation := range rotations {
		for _, offset := range offsets {
			out = append(out, pcbReflowMove{Group: group.ID, Refs: append([]string(nil), group.Refs...), Reason: reason, PivotXMil: anchor.X, PivotYMil: anchor.Y, DXMil: offset.XMil, DYMil: offset.YMil, RotationDeltaDeg: normalizeDeg(rotation - anchor.Rotation)})
		}
	}
	return out
}

func (ctx *pcbReflowContext) movedState(s pcbReflowState, group pcbReflowGroupSpec, move pcbReflowMove) (pcbReflowState, bool) {
	next := pcbReflowState{comps: map[string]boardComp{}, moves: map[string]pcbReflowMove{}}
	for ref, c := range s.comps {
		next.comps[ref] = c
	}
	for id, prior := range s.moves {
		next.moves[id] = prior
	}
	changed := false
	for _, ref := range group.Refs {
		c, err := transformBoardComp(ctx.base[ref], move.PivotXMil, move.PivotYMil, move.DXMil, move.DYMil, move.RotationDeltaDeg)
		if err != nil {
			return pcbReflowState{}, false
		}
		if !ctx.poseAllowed(ref, c) || !bboxInsideBoardOutline(ctx.snap.Outline, *c.BBox) {
			return pcbReflowState{}, false
		}
		for _, axis := range group.FixedAxes {
			if !pcbReflowAxisUnchanged(axis, ctx.base[ref], c) {
				return pcbReflowState{}, false
			}
		}
		changed = changed || poseChanged(ctx.base[ref], c)
		next.comps[ref] = c
	}
	if changed {
		next.moves[group.ID] = move
	} else {
		delete(next.moves, group.ID)
		for _, ref := range group.Refs {
			delete(next.comps, ref)
		}
	}
	return next, true
}

func (ctx *pcbReflowContext) poseAllowed(ref string, c boardComp) bool {
	base := ctx.base[ref]
	if (ctx.fixed[ref] || base.Locked) && poseChanged(base, c) {
		return false
	}
	if c.Layer != base.Layer {
		return false
	}
	for _, axis := range ctx.spec.FixedAxes[ref] {
		if !pcbReflowAxisUnchanged(axis, base, c) {
			return false
		}
	}
	return true
}

func (ctx *pcbReflowContext) copperPlan(s pcbReflowState) (pcbReflowCopperPlan, error) {
	plan := pcbReflowCopperPlan{}
	if ctx.copperErr != "" {
		return plan, fmt.Errorf("reflow cannot validate copper: %s", ctx.copperErr)
	}
	all := map[string]boardComp{}
	for ref, c := range ctx.base {
		all[ref] = c
	}
	for ref, c := range s.comps {
		all[ref] = c
	}
	for _, id := range pcbReflowSortedMoveKeys(s.moves) {
		group, move := ctx.groups[id], s.moves[id]
		for _, primitiveID := range group.InternalPrimitiveIDs {
			plan.ReplacePrimitiveIDs = append(plan.ReplacePrimitiveIDs, primitiveID)
			for _, track := range ctx.tracks {
				if track.ID == primitiveID {
					a := pcbReflowTransformPoint([2]float64{track.X1, track.Y1}, move)
					b := pcbReflowTransformPoint([2]float64{track.X2, track.Y2}, move)
					plan.Routes = append(plan.Routes, pcbModuleRoute{ID: "reflow-" + id + "-" + primitiveID, Net: track.Net, Layer: track.Layer, WidthMil: track.Width, Points: [][2]float64{a, b}, Role: "reflow-internal"})
				}
			}
			for _, via := range ctx.vias {
				if via.ID == primitiveID {
					p := pcbReflowTransformPoint([2]float64{via.X, via.Y}, move)
					plan.Vias = append(plan.Vias, pcbModuleVia{ID: "reflow-" + id + "-" + primitiveID, Net: via.Net, X: p[0], Y: p[1], HoleMil: via.Hole, DiameterMil: via.Dia, Role: "reflow-internal"})
				}
			}
		}
		for _, external := range group.ExternalConnections {
			plan.ReplacePrimitiveIDs = append(plan.ReplacePrimitiveIDs, external.ReplacePrimitiveIDs...)
		}
	}
	// Jointly backtrack the two orthogonal 45-degree choices for each external
	// connection. The global state budget accounts for these alternatives too.
	var pending []pcbReflowExternalConnection
	for _, id := range pcbReflowSortedMoveKeys(s.moves) {
		for _, external := range ctx.groups[id].ExternalConnections {
			external.ID = "reflow-" + id + "-" + external.ID
			pending = append(pending, external)
		}
	}
	var solved pcbReflowCopperPlan
	var route func(int, pcbReflowCopperPlan) bool
	var lastErr error
	route = func(index int, p pcbReflowCopperPlan) bool {
		if index == len(pending) {
			lastErr = ctx.validateCopperPlan(s, p)
			if lastErr == nil {
				solved = p
			}
			return lastErr == nil
		}
		e := pending[index]
		from, err := pcbReflowPad(all, e.From)
		if err != nil {
			lastErr = err
			return false
		}
		to, err := pcbReflowPad(all, e.To)
		if err != nil {
			lastErr = err
			return false
		}
		a, b := [2]float64{from.X, from.Y}, [2]float64{to.X, to.Y}
		paths := [][][2]float64{crystal45Path(a, b), pcbReflowReversePoints(crystal45Path(b, a))}
		for _, points := range paths {
			if ctx.report.States >= ctx.spec.MaxStates {
				ctx.report.Exhausted = true
				lastErr = fmt.Errorf("external reconnection search exhausted maxStates")
				return false
			}
			ctx.report.States++
			next := p
			next.Routes = append(append([]pcbModuleRoute(nil), p.Routes...), pcbModuleRoute{ID: e.ID, Net: e.Net, Layer: e.Layer, WidthMil: e.WidthMil, Points: points, Role: "reflow-external", From: e.From, To: e.To})
			if route(index+1, next) {
				return true
			}
		}
		return false
	}
	if !route(0, plan) {
		return plan, lastErr
	}
	return solved, nil
}

func (ctx *pcbReflowContext) validateCopperPlan(s pcbReflowState, plan pcbReflowCopperPlan) error {
	excluded := map[string]bool{}
	for _, id := range append(append([]string(nil), plan.ReplacePrimitiveIDs...), ctx.spec.TargetReplacePrimitiveIDs...) {
		excluded[id] = true
	}
	var existing []pcbTrack
	var arcs []pcbArc
	var vias []pcbViaP
	for _, t := range ctx.tracks {
		if !excluded[t.ID] {
			existing = append(existing, t)
		}
	}
	for _, a := range ctx.arcs {
		if !excluded[a.ID] {
			arcs = append(arcs, a)
		}
	}
	for _, v := range ctx.vias {
		if !excluded[v.ID] {
			vias = append(vias, v)
		}
	}
	existing = append(existing, ctx.strokes...)
	pads, err := crystalCandidatePads(ctx.snap, s.comps)
	if err != nil {
		return err
	}
	// Label moved pads as planned so the exact clearance checker does not
	// accidentally filter violations in an otherwise copper-empty move.
	for i := range pads {
		if _, moved := s.comps[pads[i].Designator]; moved {
			pads[i].ID = "planned:" + pads[i].ID
		}
	}
	for _, v := range plan.Vias {
		if ctx.snap.Outline.distToEdge(v.X, v.Y) < v.DiameterMil/2+ctx.snap.Rules.CopperToEdgeMil-netPathGeomEps {
			return fmt.Errorf("reflow via %s violates board-edge clearance", v.ID)
		}
		vias = append(vias, pcbViaP{ID: "planned:" + v.ID, Net: v.Net, X: v.X, Y: v.Y, Hole: v.HoleMil, Dia: v.DiameterMil})
	}
	for _, r := range plan.Routes {
		if r.WidthMil < ctx.snap.Rules.TrackWidthMinMil-netPathGeomEps {
			return fmt.Errorf("reflow route %s violates minimum track width", r.ID)
		}
		for i := 1; i < len(r.Points); i++ {
			for _, region := range ctx.regions {
				if region.noWires && (region.area.Layer == r.Layer || region.area.Layer == pcbLayerMulti) && copperAreaSegmentDistance(region.area, r.Points[i-1], r.Points[i]) < r.WidthMil/2+ctx.snap.Rules.ClearanceMil-netPathGeomEps {
					return fmt.Errorf("reflow route %s conflicts with no-wires region %s", r.ID, region.area.ID)
				}
			}
			if !pcbReflowSegmentInsideOutline(ctx.snap.Outline, r.Points[i-1], r.Points[i], r.WidthMil/2+ctx.snap.Rules.CopperToEdgeMil) {
				return fmt.Errorf("reflow route %s violates board-edge clearance", r.ID)
			}
		}
	}
	// The generic pcb-check report is intentionally capped and delegates some
	// shorts to other rules; a candidate validator must not inherit those skips.
	// Use the exact routing predicate directly for every planned segment.
	combined := append([]pcbTrack(nil), existing...)
	for _, r := range plan.Routes {
		for i := 1; i < len(r.Points); i++ {
			a, b := r.Points[i-1], r.Points[i]
			combined = append(combined, pcbTrack{ID: "planned:" + r.ID, Net: r.Net, Layer: r.Layer, Width: r.WidthMil, X1: a[0], Y1: a[1], X2: b[0], Y2: b[1]})
		}
	}
	for _, reservation := range ctx.spec.Reservations {
		reservationSnap := *ctx.snap
		rules := *ctx.snap.Rules
		rules.ClearanceMil = math.Max(rules.ClearanceMil, reservation.ClearanceMil)
		reservationSnap.Rules = &rules
		clear, err := crystalRouteClear(reservation.Net, reservation.Layer, reservation.WidthMil, &reservationSnap, pads, combined, arcs, vias, ctx.areas)
		if err != nil {
			return err
		}
		for i := 1; i < len(reservation.Points); i++ {
			for _, region := range ctx.regions {
				if region.noWires && (region.area.Layer == reservation.Layer || region.area.Layer == pcbLayerMulti) && copperAreaSegmentDistance(region.area, reservation.Points[i-1], reservation.Points[i]) < reservation.WidthMil/2+rules.ClearanceMil-netPathGeomEps {
					return fmt.Errorf("reservation %s conflicts with no-wires region %s", reservation.ID, region.area.ID)
				}
			}
			if !clear(reservation.Points[i-1], reservation.Points[i]) {
				return fmt.Errorf("reservation %s conflicts with final exact copper", reservation.ID)
			}
		}
	}
	for _, r := range plan.Routes {
		clear, err := crystalRouteClear(r.Net, r.Layer, r.WidthMil, ctx.snap, pads, combined, arcs, vias, ctx.areas)
		if err != nil {
			return err
		}
		for i := 1; i < len(r.Points); i++ {
			if !clear(r.Points[i-1], r.Points[i]) {
				return fmt.Errorf("reflow route %s violates exact copper clearance", r.ID)
			}
		}
	}
	for _, v := range plan.Vias {
		if v.HoleMil < ctx.snap.Rules.ViaDrillMil-netPathGeomEps || v.DiameterMil < ctx.snap.Rules.ViaDiameterMil-netPathGeomEps {
			return fmt.Errorf("reflow via %s violates current drill/diameter rules", v.ID)
		}
		for _, region := range ctx.regions {
			if region.noWires && copperAreaPointDistance(region.area, [2]float64{v.X, v.Y}) < v.DiameterMil/2+ctx.snap.Rules.ClearanceMil-netPathGeomEps {
				return fmt.Errorf("reflow via %s conflicts with no-wires region %s", v.ID, region.area.ID)
			}
		}
		others := []pcbViaP{}
		for _, existingVia := range vias {
			if existingVia.ID != "planned:"+v.ID {
				others = append(others, existingVia)
			}
		}
		check := preflightViaFence([][2]float64{{v.X, v.Y}}, v.Net, v.HoleMil, v.DiameterMil, ctx.snap.Rules.ClearanceMil, ctx.snap.Rules.CopperToEdgeMil, ctx.snap.Outline, pads, combined, arcs, others, ctx.areas)
		if len(check.Problems) > 0 {
			return fmt.Errorf("reflow via %s: %s", v.ID, strings.Join(check.Problems, "; "))
		}
	}
	for _, arc := range arcs {
		curve, _, err := flattenNetPathArc(arc)
		if err != nil {
			return err
		}
		for i := 1; i < len(curve); i++ {
			combined = append(combined, pcbTrack{ID: arc.ID, Net: arc.Net, Layer: arc.Layer, Width: arc.Width, X1: curve[i-1].x, Y1: curve[i-1].y, X2: curve[i].x, Y2: curve[i].y})
		}
	}
	for _, p := range pads {
		if _, moved := s.comps[p.Designator]; !moved {
			continue
		}
		for _, t := range combined {
			if p.Net == t.Net && p.Net != "" || !padLayerMatches(p.Layer, t.Layer) {
				continue
			}
			gap, err := pcbExactPadSegmentGap(p, [2]float64{t.X1, t.Y1}, [2]float64{t.X2, t.Y2})
			if err != nil {
				return err
			}
			if gap < t.Width/2+ctx.snap.Rules.ClearanceMil-netPathGeomEps {
				return fmt.Errorf("moved pad %s.%s conflicts with track %s", p.Designator, p.Number, t.ID)
			}
		}
		for _, via := range vias {
			if p.Net == via.Net && p.Net != "" {
				continue
			}
			gap, err := pcbExactPadSegmentGap(p, [2]float64{via.X, via.Y}, [2]float64{via.X, via.Y})
			if err != nil {
				return err
			}
			if gap < via.Dia/2+ctx.snap.Rules.ClearanceMil-netPathGeomEps {
				return fmt.Errorf("moved pad %s.%s conflicts with via %s", p.Designator, p.Number, via.ID)
			}
		}
		for _, q := range pads {
			if p.ID == q.ID || p.Net == q.Net && p.Net != "" || !padLayersCompatible(p.Layer, q.Layer) {
				continue
			}
			gap, err := pcbReflowPadGap(p, q)
			if err != nil {
				return err
			}
			if gap < ctx.snap.Rules.ClearanceMil-netPathGeomEps {
				return fmt.Errorf("moved pad %s.%s conflicts with pad %s.%s", p.Designator, p.Number, q.Designator, q.Number)
			}
		}
		for _, area := range ctx.areas {
			if !padLayerMatches(p.Layer, area.Layer) || p.Net == area.Net && p.Net != "" {
				continue
			}
			gap, err := pcbReflowPadAreaGap(p, area)
			if err != nil {
				return err
			}
			if gap < ctx.snap.Rules.ClearanceMil-netPathGeomEps {
				return fmt.Errorf("moved pad %s.%s conflicts with %s %s", p.Designator, p.Number, area.Kind, area.ID)
			}
		}
	}
	return nil
}

// rebuildPCBReflowCandidate is the independent-checker entry point. Copper in
// evidence is deliberately ignored. The caller compares both returned poses
// and regenerated copper against the submitted candidate/journal/live objects.
func rebuildPCBReflowCandidate(spec pcbReflowSpec, target pcbLayoutVariant, evidence pcbReflowCandidate, snap *boardSnapshot, minGap float64) (pcbLayoutVariant, pcbReflowCopperPlan, error) {
	ctx, err := newPCBReflowContext(spec, target, snap, minGap)
	if err != nil {
		return pcbLayoutVariant{}, pcbReflowCopperPlan{}, err
	}
	s := pcbReflowState{comps: map[string]boardComp{}, moves: map[string]pcbReflowMove{}}
	for ref, c := range target.comps {
		s.comps[ref] = c
	}
	for _, submitted := range evidence.Moves {
		group, ok := ctx.groups[submitted.Group]
		if !ok || s.moves[submitted.Group].Group != "" {
			return pcbLayoutVariant{}, pcbReflowCopperPlan{}, fmt.Errorf("unknown/duplicate reflow group %s", submitted.Group)
		}
		if reason := ctx.report.FixedGroups[group.ID]; reason != "" {
			return pcbLayoutVariant{}, pcbReflowCopperPlan{}, fmt.Errorf("reflow group %s cannot move: %s", group.ID, reason)
		}
		refs := append([]string(nil), submitted.Refs...)
		want := append([]string(nil), group.Refs...)
		sort.Strings(refs)
		sort.Strings(want)
		if strings.Join(refs, "\x00") != strings.Join(want, "\x00") {
			return pcbLayoutVariant{}, pcbReflowCopperPlan{}, fmt.Errorf("reflow group %s is not whole", group.ID)
		}
		anchor := ctx.base[group.AnchorRef]
		if !allFinite(submitted.PivotXMil, submitted.PivotYMil, submitted.DXMil, submitted.DYMil, submitted.RotationDeltaDeg) || !layoutAlmostEqual(submitted.PivotXMil, anchor.X) || !layoutAlmostEqual(submitted.PivotYMil, anchor.Y) || math.Abs(submitted.DXMil) > spec.MaxShiftXMil+netPathGeomEps || math.Abs(submitted.DYMil) > spec.MaxShiftYMil+netPathGeomEps || math.Abs(submitted.DXMil/spec.StepMil-math.Round(submitted.DXMil/spec.StepMil)) > 1e-6 || math.Abs(submitted.DYMil/spec.StepMil-math.Round(submitted.DYMil/spec.StepMil)) > 1e-6 {
			return pcbLayoutVariant{}, pcbReflowCopperPlan{}, fmt.Errorf("reflow group %s transform is outside declared search", group.ID)
		}
		rotations := group.AllowedRotationsDeg
		if len(rotations) == 0 {
			rotations = []float64{anchor.Rotation}
		}
		if !rotationAllowed(normalizeDeg(anchor.Rotation+submitted.RotationDeltaDeg), rotations) {
			return pcbLayoutVariant{}, pcbReflowCopperPlan{}, fmt.Errorf("reflow group %s rotation is not allowed", group.ID)
		}
		var moved bool
		s, moved = ctx.movedState(s, group, submitted)
		if !moved || s.moves[group.ID].Group == "" {
			return pcbLayoutVariant{}, pcbReflowCopperPlan{}, fmt.Errorf("reflow group %s transform violates fixed constraints or is a no-op", group.ID)
		}
	}
	if _, reason := ctx.nextCollision(s); reason != "" {
		return pcbLayoutVariant{}, pcbReflowCopperPlan{}, fmt.Errorf("reflow candidate placement: %s", reason)
	}
	copper, err := ctx.copperPlan(s)
	if err != nil {
		return pcbLayoutVariant{}, pcbReflowCopperPlan{}, err
	}
	return pcbLayoutVariant{label: evidence.Variant, comps: s.comps}, copper, nil
}

func (ctx *pcbReflowContext) readRegions() {
	if ctx.copperErr != "" {
		return
	}
	for _, raw := range ctx.snap.Copper.Regions {
		m, ok := raw.(map[string]any)
		if !ok {
			ctx.copperErr = "region object is unavailable"
			return
		}
		id := asString(m["primitiveId"])
		if pcbReflowContains(ctx.spec.TargetReplacePrimitiveIDs, id) {
			continue
		}
		var names []string
		switch x := m["ruleTypeNames"].(type) {
		case []any:
			for _, name := range x {
				s, ok := name.(string)
				if !ok {
					ctx.copperErr = "region rule types unavailable"
					return
				}
				names = append(names, s)
			}
		case []string:
			names = x
		}
		if len(names) == 0 {
			ctx.copperErr = "region " + id + " has unknown rule types"
			return
		}
		r := pcbReflowRegion{area: pcbCopperArea{ID: id, Kind: "rule region", Layer: int(asFloat(m["layer"]))}}
		for _, name := range names {
			switch name {
			case "no-components":
				r.noComponents = true
			case "no-wires":
				r.noWires = true
			case "no-pours", "no-fills", "no-inner-electrical":
			default:
				ctx.copperErr = "region " + id + " rule " + name + " unsupported"
				return
			}
		}
		if !r.noComponents && !r.noWires {
			continue
		}
		if id == "" || m["geometryAvailable"] != true || !netPathCopperLayer(r.area.Layer) && r.area.Layer != pcbLayerMulti {
			ctx.copperErr = "region " + id + " has unknown layer/geometry"
			return
		}
		var err error
		r.area.Contours, err = polygonSourceContours(m["source"])
		if err != nil {
			ctx.copperErr = "region " + id + " geometry: " + err.Error()
			return
		}
		ctx.regions = append(ctx.regions, r)
	}
}

// A rounded rectangle or capsule is a line/rectangle core Minkowski-summed
// with a disk. Measuring against that core and subtracting its radius preserves
// exact shapes and rotations, without treating a rendered box as copper.
func pcbReflowPadGap(a, b pcbPadP) (float64, error) {
	if !a.ShapeOK || !b.ShapeOK {
		return 0, fmt.Errorf("pad geometry unknown")
	}
	center := [2]float64{a.X, a.Y}
	inside, err := pcbExactPadSegmentGap(b, center, center)
	if err != nil {
		return 0, err
	}
	if inside <= netPathGeomEps {
		return 0, nil
	}
	n := pcbNetPathNode{x: b.X, y: b.Y, w: b.ShapeW, h: b.ShapeH, rotation: b.Rotation, shape: b.Shape}
	var core [][2]float64
	r := 0.0
	switch b.Shape {
	case "RECT":
		r = b.ShapeRound
		w, h := b.ShapeW/2-r, b.ShapeH/2-r
		core = [][2]float64{{-w, -h}, {w, -h}, {w, h}, {-w, h}, {-w, -h}}
	case "OVAL":
		x1, y1, x2, y2, radius := netPathOvalSpine(n)
		r = radius
		core = [][2]float64{{x1, y1}, {x2, y2}}
	case "ELLIPSE":
		if math.Abs(b.ShapeW-b.ShapeH) > netPathGeomEps {
			return 0, fmt.Errorf("noncircular ellipse clearance unsupported")
		}
		r = b.ShapeW / 2
		core = [][2]float64{{0, 0}, {0, 0}}
	default:
		return 0, fmt.Errorf("pad shape %s clearance unsupported", b.Shape)
	}
	for i, p := range core {
		x, y := rotateVec(p[0], p[1], b.Rotation)
		core[i] = [2]float64{x + b.X, y + b.Y}
	}
	gap := math.Inf(1)
	for i := 1; i < len(core); i++ {
		d, err := pcbExactPadSegmentGap(a, core[i-1], core[i])
		if err != nil {
			return 0, err
		}
		gap = math.Min(gap, d-r)
	}
	return math.Max(0, gap), nil
}

func pcbReflowPadAreaGap(p pcbPadP, area pcbCopperArea) (float64, error) {
	center := [2]float64{p.X, p.Y}
	if compoundWinding(area.Contours, center) != 0 {
		return 0, nil
	}
	gap := math.Inf(1)
	for _, contour := range area.Contours {
		for i, a := range contour {
			b := contour[(i+1)%len(contour)]
			d, err := pcbExactPadSegmentGap(p, a, b)
			if err != nil {
				return 0, err
			}
			gap = math.Min(gap, d)
		}
	}
	return gap, nil
}

func pcbReflowTransformPoint(p [2]float64, move pcbReflowMove) [2]float64 {
	x, y := rotateVec(p[0]-move.PivotXMil, p[1]-move.PivotYMil, move.RotationDeltaDeg)
	return [2]float64{round4(x + move.PivotXMil + move.DXMil), round4(y + move.PivotYMil + move.DYMil)}
}

func pcbReflowSegmentInsideOutline(outline *boardOutline, a, b [2]float64, margin float64) bool {
	if !outline.containsPoint(a[0], a[1]) || !outline.containsPoint(b[0], b[1]) {
		return false
	}
	for i, p := range outline.Points {
		q := outline.Points[(i+1)%len(outline.Points)]
		if segSegDist(a[0], a[1], b[0], b[1], p[0], p[1], q[0], q[1]) < margin-netPathGeomEps {
			return false
		}
	}
	return true
}

func pcbReflowPad(all map[string]boardComp, endpoint string) (boardPad, error) {
	ref, number, err := splitPadRef(endpoint)
	if err != nil {
		return boardPad{}, err
	}
	c, ok := all[ref]
	if !ok {
		return boardPad{}, fmt.Errorf("missing reflow endpoint %s", endpoint)
	}
	return findBoardPadExact(c, number, "")
}

func pcbReflowReversePoints(points [][2]float64) [][2]float64 {
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}
	return points
}

func pcbReflowAxisUnchanged(axis string, a, b boardComp) bool {
	switch axis {
	case "x":
		return layoutAlmostEqual(a.X, b.X)
	case "y":
		return layoutAlmostEqual(a.Y, b.Y)
	case "rotation":
		return sameRotation(a.Rotation, b.Rotation)
	}
	return false
}

func pcbReflowValidateAxes(axes []string) error {
	seen := map[string]bool{}
	for _, axis := range axes {
		if axis != "x" && axis != "y" && axis != "rotation" || seen[axis] {
			return fmt.Errorf("fixedAxes must contain distinct x/y/rotation values")
		}
		seen[axis] = true
	}
	return nil
}

func pcbReflowContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func pcbReflowSortedMoveKeys(moves map[string]pcbReflowMove) []string {
	out := make([]string, 0, len(moves))
	for id := range moves {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func pcbReflowStateKey(s pcbReflowState) string {
	var b strings.Builder
	for _, ref := range sortedBoardCompKeys(s.comps) {
		c := s.comps[ref]
		fmt.Fprintf(&b, "%s:%.4f,%.4f,%.4f;", ref, c.X, c.Y, normalizeDeg(c.Rotation))
	}
	return b.String()
}

func pcbReflowRectOverlap(a, b layoutBBox) bool {
	return a.MinX < b.MaxX && a.MaxX > b.MinX && a.MinY < b.MaxY && a.MaxY > b.MinY
}

func (ctx *pcbReflowContext) reject(reason string) {
	if len(ctx.report.Rejected) < 50 && !pcbReflowContains(ctx.report.Rejected, reason) {
		ctx.report.Rejected = append(ctx.report.Rejected, reason)
	}
}
