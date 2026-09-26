package app

import (
	"container/heap"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/schguard"
)

const (
	defaultSchematicExpandedNodes = 200000
	defaultSchematicMaxReroutes   = 4
)

var schematicRoutingExpansions = [...]float64{40, 80, 160, 320}

var errSchematicExpandedBudget = errors.New("schematic routing expanded-node budget exhausted")

type SchematicRoutingRejection struct {
	ComponentID         string `json:"componentId,omitempty"`
	ComponentRef        string `json:"componentRef,omitempty"`
	Net                 string `json:"net,omitempty"`
	Reason              string `json:"reason"`
	Count               int    `json:"count"`
	AttributionComplete bool   `json:"attributionComplete"`
}

type SchematicRoutingBoundaryAttempt struct {
	Net           string  `json:"net"`
	SourceIsland  string  `json:"sourceIsland"`
	TargetIsland  string  `json:"targetIsland"`
	ExpansionRaw  float64 `json:"expansionRaw"`
	ExpandedNodes int     `json:"expandedNodes"`
	Result        string  `json:"result"`
}

type SchematicRoutingCandidateSummary struct {
	Net          string       `json:"net"`
	SourceIsland string       `json:"sourceIsland"`
	TargetIsland string       `json:"targetIsland"`
	Points       [][2]float64 `json:"points"`
	Result       string       `json:"result"`
}

type SchematicRoutingDiagnostics struct {
	Strategy           string                             `json:"strategy"`
	MaxExpandedNodes   int                                `json:"maxExpandedNodes"`
	MaxNodesPerAttempt int                                `json:"maxNodesPerAttempt"`
	ExpandedNodes      int                                `json:"expandedNodes"`
	MaxReroutes        int                                `json:"maxReroutes"`
	Reroutes           int                                `json:"reroutes"`
	CompletedJoins     int                                `json:"completedJoins"`
	TemplateCacheHits  int                                `json:"templateCacheHits"`
	BoundaryAttempts   []SchematicRoutingBoundaryAttempt  `json:"boundaryAttempts,omitempty"`
	RejectedEdges      []SchematicRoutingRejection        `json:"rejectedEdges,omitempty"`
	CandidateSummaries []SchematicRoutingCandidateSummary `json:"candidateSummaries,omitempty"`
	duration           time.Duration
}

type schematicRoutingContext struct {
	options             SchematicRoutingOptions
	expanded            int
	reroutes            int
	completed           int
	components          map[string]string
	netPins             map[string]int
	policies            map[string]string
	rejections          map[string]*SchematicRoutingRejection
	boundaries          []SchematicRoutingBoundaryAttempt
	candidates          []SchematicRoutingCandidateSummary
	cache               map[string]schematicMazeCacheEntry
	templates           map[string][][]powerLayoutWire
	templateHits        int
	duration            time.Duration
	relocation          int
	candidateBudget     *int
	namingReserve       int
	requiredConnections []schematicRequiredConnection
	requiredJoin        *schematicRequiredConnection
}

func (c *schematicRoutingContext) beginReroute() bool {
	if c == nil || c.reroutes >= c.options.MaxReroutes {
		return false
	}
	c.reroutes++
	return true
}

type schematicMazeCacheEntry struct {
	route          []powerLayoutWire
	kind           string
	message        string
	evidence       []SchematicRoutingRejection
	ownersComplete bool
}

func newSchematicRoutingContext(options *SchematicRoutingOptions, components []SchematicLayoutComponent) (*schematicRoutingContext, error) {
	value := SchematicRoutingOptions{MaxExpandedNodes: defaultSchematicExpandedNodes, MaxReroutes: defaultSchematicMaxReroutes}
	if options != nil {
		if options.MaxExpandedNodes != 0 {
			value.MaxExpandedNodes = options.MaxExpandedNodes
		}
		if options.MaxReroutes != 0 {
			value.MaxReroutes = options.MaxReroutes
		}
	}
	if value.MaxExpandedNodes < 1 || value.MaxExpandedNodes > 5000000 {
		return nil, fmt.Errorf("routing.maxExpandedNodes must be 1..5000000")
	}
	if value.MaxReroutes < 1 || value.MaxReroutes > 32 {
		return nil, fmt.Errorf("routing.maxReroutes must be 1..32")
	}
	refs := map[string]string{}
	netPins := map[string]int{}
	for _, component := range components {
		refs[component.Measurement.Designator] = component.ID
		for _, pin := range component.Measurement.Pins {
			if pin.Net != "" {
				netPins[pin.Net]++
			}
		}
	}
	return &schematicRoutingContext{options: value, components: refs, netPins: netPins, rejections: map[string]*SchematicRoutingRejection{}, cache: map[string]schematicMazeCacheEntry{}, templates: map[string][][]powerLayoutWire{}}, nil
}

func (c *schematicRoutingContext) snapshot() *SchematicRoutingDiagnostics {
	if c == nil {
		return nil
	}
	out := &SchematicRoutingDiagnostics{Strategy: "directional-grid-a-star-v1", MaxExpandedNodes: c.options.MaxExpandedNodes, MaxNodesPerAttempt: c.maxNodesPerAttempt(),
		ExpandedNodes: c.expanded, MaxReroutes: c.options.MaxReroutes, Reroutes: c.reroutes, CompletedJoins: c.completed, TemplateCacheHits: c.templateHits,
		BoundaryAttempts:   append([]SchematicRoutingBoundaryAttempt(nil), c.boundaries...),
		CandidateSummaries: append([]SchematicRoutingCandidateSummary(nil), c.candidates...), duration: c.duration}
	for _, value := range c.rejections {
		out.RejectedEdges = append(out.RejectedEdges, *value)
	}
	sort.Slice(out.RejectedEdges, func(i, j int) bool {
		a, b := out.RejectedEdges[i], out.RejectedEdges[j]
		return a.Reason+a.ComponentID+a.ComponentRef+a.Net < b.Reason+b.ComponentID+b.ComponentRef+b.Net
	})
	return out
}

func (c *schematicRoutingContext) maxNodesPerAttempt() int {
	if c == nil {
		return 0
	}
	value := c.options.MaxExpandedNodes / 4
	if value < 256 {
		value = c.options.MaxExpandedNodes
	}
	return value
}

func (c *schematicRoutingContext) usableExpandedNodes() int {
	if c == nil {
		return 0
	}
	if c.relocation > 0 || c.options.MaxExpandedNodes < 1024 {
		return c.options.MaxExpandedNodes
	}
	// Preserve one quarter of the declared zone budget for the required
	// blocker/dependency relocation stage. It is still part of the same global
	// accounting and becomes available only inside that stage.
	return c.options.MaxExpandedNodes - c.options.MaxExpandedNodes/4
}

func (c *schematicRoutingContext) observe(err error) {
	if c == nil || err == nil {
		return
	}
	var obstruction *schGeometryObstruction
	if !errors.As(err, &obstruction) {
		c.addRejection(SchematicRoutingRejection{Reason: "unattributed", AttributionComplete: false})
		return
	}
	refs := obstruction.blockers
	if len(refs) == 0 {
		refs = obstruction.owners
	}
	if len(refs) == 0 {
		refs = []string{""}
	}
	nets := obstruction.nets
	if len(nets) == 0 {
		nets = []string{""}
	}
	for _, ref := range refs {
		for _, net := range nets {
			id := c.components[ref]
			// For a rejected search edge, an explicitly hit body/pin/stem is
			// complete blocker evidence by itself. The broader net-owner set on
			// schGeometryObstruction remains relevant to legacy placement
			// dependency analysis, but a missing source-net owner must not erase
			// the exact object that A* actually hit.
			complete := ref != "" && id != ""
			if !obstruction.blockersExplicit {
				complete = complete && obstruction.complete
			}
			c.addRejection(SchematicRoutingRejection{ComponentID: id, ComponentRef: ref, Net: net, Reason: obstruction.kind, AttributionComplete: complete})
		}
	}
}

func (c *schematicRoutingContext) addRejection(value SchematicRoutingRejection) {
	key := strings.Join([]string{value.Reason, value.ComponentID, value.ComponentRef, value.Net}, "\x00")
	if existing := c.rejections[key]; existing != nil {
		existing.Count++
		existing.AttributionComplete = existing.AttributionComplete && value.AttributionComplete
		return
	}
	value.Count = 1
	c.rejections[key] = &value
}

func (c *schematicRoutingContext) rejectionCounts() map[string]int {
	out := make(map[string]int, len(c.rejections))
	for key, value := range c.rejections {
		out[key] = value.Count
	}
	return out
}

func (c *schematicRoutingContext) rejectionsSince(before map[string]int) ([]SchematicRoutingRejection, bool) {
	complete := true
	var out []SchematicRoutingRejection
	for key, value := range c.rejections {
		count := value.Count - before[key]
		if count <= 0 {
			continue
		}
		item := *value
		item.Count = count
		complete = complete && item.AttributionComplete
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Reason+a.ComponentID+a.ComponentRef+a.Net < b.Reason+b.ComponentID+b.ComponentRef+b.Net
	})
	if len(out) == 0 {
		complete = false
	}
	return out, complete
}

type schematicRoutingFailure struct {
	Kind             string                       `json:"kind"`
	Net              string                       `json:"net"`
	SourceIsland     string                       `json:"sourceIsland"`
	TargetIsland     string                       `json:"targetIsland"`
	SourcePins       []powerLayoutPin             `json:"sourcePins"`
	TargetPins       []powerLayoutPin             `json:"targetPins"`
	LocalLayout      *powerLayoutPlan             `json:"localLayout,omitempty"`
	Routing          *SchematicRoutingDiagnostics `json:"routing"`
	BlockingEvidence []SchematicRoutingRejection  `json:"blockingEvidence,omitempty"`
	OwnersComplete   bool                         `json:"ownersComplete"`
	cause            error
}

func (e *schematicRoutingFailure) Error() string {
	return fmt.Sprintf("routing %s for net %s from island %s to %s: %v", e.Kind, e.Net, e.SourceIsland, e.TargetIsland, e.cause)
}
func (e *schematicRoutingFailure) Unwrap() error { return e.cause }
func (e *schematicRoutingFailure) FailureDetails() any {
	return struct {
		Type                      string `json:"type"`
		GlobalInfeasibilityProven bool   `json:"globalInfeasibilityProven"`
		*schematicRoutingFailure
	}{"routing-failure", false, e}
}

type mazeState struct {
	X, Y int
	Dir  int
}

type mazeCost struct {
	Length, Bends, Crossings int
}

func mazeCostLess(a, b mazeCost) bool {
	if a.Length != b.Length {
		return a.Length < b.Length
	}
	if a.Bends != b.Bends {
		return a.Bends < b.Bends
	}
	return a.Crossings < b.Crossings
}

type mazeItem struct {
	state mazeState
	g, f  mazeCost
	seq   int
	index int
}
type mazeQueue []*mazeItem

func (q mazeQueue) Len() int { return len(q) }
func (q mazeQueue) Less(i, j int) bool {
	if mazeCostLess(q[i].f, q[j].f) {
		return true
	}
	if mazeCostLess(q[j].f, q[i].f) {
		return false
	}
	if q[i].state.Y != q[j].state.Y {
		return q[i].state.Y < q[j].state.Y
	}
	if q[i].state.X != q[j].state.X {
		return q[i].state.X < q[j].state.X
	}
	if q[i].state.Dir != q[j].state.Dir {
		return q[i].state.Dir < q[j].state.Dir
	}
	return q[i].seq < q[j].seq
}
func (q mazeQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i]; q[i].index, q[j].index = i, j }
func (q *mazeQueue) Push(x any)   { item := x.(*mazeItem); item.index = len(*q); *q = append(*q, item) }
func (q *mazeQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*q = old[:n-1]
	return item
}

var mazeDirections = [...]struct{ X, Y int }{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}

func mazePoint(s mazeState) [2]float64 {
	return [2]float64{float64(s.X) * schAnchorGrid, float64(s.Y) * schAnchorGrid}
}
func mazeGrid(point [2]float64) (mazeState, bool) {
	if !plGrid(point[0]) || !plGrid(point[1]) {
		return mazeState{}, false
	}
	return mazeState{X: int(math.Round(point[0] / schAnchorGrid)), Y: int(math.Round(point[1] / schAnchorGrid))}, true
}

func libIslandStableID(island libIsland) string {
	pins := append([]powerLayoutPin(nil), island.pins...)
	sort.Slice(pins, func(i, j int) bool {
		if pins[i].Y != pins[j].Y {
			return pins[i].Y < pins[j].Y
		}
		if pins[i].X != pins[j].X {
			return pins[i].X < pins[j].X
		}
		return pins[i].Number < pins[j].Number
	})
	parts := make([]string, 0, len(pins))
	for _, pin := range pins {
		parts = append(parts, fmt.Sprintf("%s@%g,%g", pin.Number, pin.X, pin.Y))
	}
	return island.net + ":" + strings.Join(parts, ";")
}

func libIslandAccessPoints(p *powerLayoutPlan, island libIsland) map[mazeState]bool {
	out := map[mazeState]bool{}
	for _, pin := range island.pins {
		if state, ok := mazeGrid([2]float64{pin.X, pin.Y}); ok {
			out[state] = true
		}
	}
	for _, index := range island.wireIndices {
		if index < 0 || index >= len(p.Wires) || len(p.Wires[index].Points) != 2 {
			continue
		}
		a, aok := mazeGrid(p.Wires[index].Points[0])
		b, bok := mazeGrid(p.Wires[index].Points[1])
		if !aok || !bok {
			continue
		}
		dx, dy := 0, 0
		if a.X < b.X {
			dx = 1
		} else if a.X > b.X {
			dx = -1
		}
		if a.Y < b.Y {
			dy = 1
		} else if a.Y > b.Y {
			dy = -1
		}
		for s := a; ; s.X, s.Y = s.X+dx, s.Y+dy {
			out[s] = true
			if s.X == b.X && s.Y == b.Y {
				break
			}
		}
	}
	return out
}

func libIslandPinDirection(p *powerLayoutPlan, island libIsland, state mazeState) (int, bool) {
	point := mazePoint(state)
	for _, pin := range island.pins {
		if math.Abs(pin.X-point[0]) > 1e-6 || math.Abs(pin.Y-point[1]) > 1e-6 {
			continue
		}
		for _, component := range p.Placements {
			for _, candidate := range component.Pins {
				if math.Abs(candidate.X-pin.X) > 1e-6 || math.Abs(candidate.Y-pin.Y) > 1e-6 || candidate.Number != pin.Number || candidate.Net != pin.Net {
					continue
				}
				side, err := libPinSide(candidate, component.BBox)
				if err != nil {
					return 0, false
				}
				return map[string]int{"right": 1, "up": 2, "left": 3, "down": 4}[side], true
			}
		}
	}
	return 0, false
}

func mazeHeuristic(state mazeState, bounds [4]int) int {
	dx, dy := 0, 0
	if state.X < bounds[0] {
		dx = bounds[0] - state.X
	} else if state.X > bounds[2] {
		dx = state.X - bounds[2]
	}
	if state.Y < bounds[1] {
		dy = bounds[1] - state.Y
	} else if state.Y > bounds[3] {
		dy = state.Y - bounds[3]
	}
	return dx + dy
}

func mazeTargetBounds(target map[mazeState]bool) [4]int {
	b := [4]int{math.MaxInt, math.MaxInt, math.MinInt, math.MinInt}
	for point := range target {
		if point.X < b[0] {
			b[0] = point.X
		}
		if point.Y < b[1] {
			b[1] = point.Y
		}
		if point.X > b[2] {
			b[2] = point.X
		}
		if point.Y > b[3] {
			b[3] = point.Y
		}
	}
	return b
}

func mazeSearchBounds(p *powerLayoutPlan, expansion float64) [4]int {
	b := powerLayoutContentBounds(p)
	return [4]int{int(math.Floor((b.MinX - expansion) / schAnchorGrid)), int(math.Floor((b.MinY - expansion) / schAnchorGrid)),
		int(math.Ceil((b.MaxX + expansion) / schAnchorGrid)), int(math.Ceil((b.MaxY + expansion) / schAnchorGrid))}
}

func mazePointStrictlyInsideForeignWire(p *powerLayoutPlan, net string, point [2]float64) bool {
	for _, wire := range p.Wires {
		if wire.Net == net || len(wire.Points) != 2 || !plOnSegment(point, wire.Points[0], wire.Points[1]) {
			continue
		}
		atA := math.Abs(point[0]-wire.Points[0][0]) <= 1e-6 && math.Abs(point[1]-wire.Points[0][1]) <= 1e-6
		atB := math.Abs(point[0]-wire.Points[1][0]) <= 1e-6 && math.Abs(point[1]-wire.Points[1][1]) <= 1e-6
		if !atA && !atB {
			return true
		}
	}
	return false
}

func mazeCrossings(p *powerLayoutPlan, net string, a, b [2]float64) int {
	n := 0
	for _, wire := range p.Wires {
		if wire.Net != net && len(wire.Points) == 2 && plSegmentsMeet(a, b, wire.Points[0], wire.Points[1]) && !plSegmentsContact(a, b, wire.Points[0], wire.Points[1]) {
			n++
		}
	}
	return n
}

func mazeAdvanceAcrossForeignX(p *powerLayoutPlan, net string, from mazeState, direction int, bounds [4]int) (mazeState, bool) {
	delta := mazeDirections[direction-1]
	next := mazeState{X: from.X + delta.X, Y: from.Y + delta.Y, Dir: direction}
	for mazePointStrictlyInsideForeignWire(p, net, mazePoint(next)) {
		next.X += delta.X
		next.Y += delta.Y
	}
	if next.X < bounds[0] || next.X > bounds[2] || next.Y < bounds[1] || next.Y > bounds[3] {
		return mazeState{}, false
	}
	return next, true
}

func mazeEdgeValid(p *powerLayoutPlan, net string, from, to mazeState, routing *schematicRoutingContext) bool {
	a, b := mazePoint(from), mazePoint(to)
	if err := validateLibRoutingEdge(p, net, a, b); err != nil {
		routing.observe(err)
		return false
	}
	return true
}

// Validate only the proposed edge against the already-valid immutable base.
// It uses the same shared body/contact/outward predicates as the full gate; the
// reconstructed complete path is still passed through validateLibGeometry.
func validateLibRoutingEdge(p *powerLayoutPlan, net string, a, b [2]float64) error {
	if a == b || (a[0] != b[0] && a[1] != b[1]) || !plGrid(a[0]) || !plGrid(a[1]) || !plGrid(b[0]) || !plGrid(b[1]) {
		return schObstruction("invalid-route-edge", fmt.Errorf("route edge is zero, off-grid or nonorthogonal"))
	}
	for _, component := range p.Placements {
		if plWireEntersBody(a, b, component) {
			return schWireObstruction(p, "wire-body", fmt.Errorf("%s wire passes through %s body", net, component.Designator), []string{net}, component.Designator)
		}
		for _, box := range component.TextBBoxes {
			if plSegmentTouchesBox(a, b, box) {
				return schWireObstruction(p, "wire-text", fmt.Errorf("%s wire crosses %s Designator", net, component.Designator), []string{net}, component.Designator)
			}
		}
		for _, pin := range component.Pins {
			at := [2]float64{pin.X, pin.Y}
			if plOnSegment(at, a, b) {
				if pin.Net != net {
					return schWireObstruction(p, "foreign-pin", fmt.Errorf("%s wire crosses foreign pin %s.%s", net, component.Designator, pin.Number), []string{net, pin.Net}, component.Designator)
				}
				rotation, err := libPinOutwardRotation(pin, component.BBox)
				if err != nil {
					return err
				}
				for _, end := range [][2]float64{a, b} {
					if math.Abs(end[0]-pin.X) <= 1e-6 && math.Abs(end[1]-pin.Y) <= 1e-6 {
						continue
					}
					if !schguard.PinRayOutward(rotation, schguard.Point{X: pin.X, Y: pin.Y}, schguard.Point{X: end[0], Y: end[1]}) {
						return schWireObstruction(p, "pin-exit-direction", fmt.Errorf("pin-exit-direction: %s.%s first wire edge must leave outward", component.Designator, pin.Number), []string{net}, component.Designator)
					}
				}
			}
			if pin.Net != "" && pin.Net != net {
				side, err := libPinSide(pin, component.BBox)
				if err != nil {
					return err
				}
				x, y := endpointFor(pin.X, pin.Y, schAnchorGrid, side)
				if plSegmentsContact(a, b, at, [2]float64{x, y}) {
					return schWireObstruction(p, "pin-exit-foreign-wire", fmt.Errorf("%s wire blocks minimum exit of %s.%s", net, component.Designator, pin.Number), []string{net, pin.Net}, component.Designator)
				}
			}
		}
	}
	stems, err := libMeasuredStems(p)
	if err != nil {
		return err
	}
	for _, stem := range stems {
		if stem.net != net && plSegmentsMeet(a, b, stem.a, stem.b) {
			return schWireObstruction(p, "foreign-stem", fmt.Errorf("%s wire touches foreign pin stem %s.%s", net, stem.component, stem.pin), []string{net, stem.net}, stem.component)
		}
	}
	segments, err := schTerminalSegments(p)
	if err != nil {
		return err
	}
	for _, wire := range segments {
		if wire.Net != net && plSegmentsContact(a, b, wire.Points[0], wire.Points[1]) {
			// The proposed edge has no persistent owner yet. Only the existing
			// foreign wire tree is a blocker; including the source net here made
			// every endpoint component look like an obstacle and disabled precise
			// conflict-directed backjumping.
			return schWireObstruction(p, "foreign-wire-contact", fmt.Errorf("wire edge joins %s and %s", net, wire.Net), []string{wire.Net})
		}
	}
	for _, marker := range p.Flags {
		for _, box := range schTerminalMarkerBoxes(marker) {
			if plSegmentBox(a, b, box) {
				return schWireObstruction(p, "wire-marker", fmt.Errorf("%s wire crosses %s marker", net, marker.Net), []string{net, marker.Net})
			}
		}
	}
	return nil
}

func mazeReconstruct(goal mazeState, parent map[mazeState]mazeState) [][2]float64 {
	states := []mazeState{goal}
	for {
		previous, ok := parent[states[len(states)-1]]
		if !ok {
			break
		}
		states = append(states, previous)
	}
	points := make([][2]float64, len(states))
	for i := range states {
		points[len(states)-1-i] = mazePoint(states[i])
	}
	return plNormalizeWirePoints(points)
}

func libPinsShareIsland(p *powerLayoutPlan, a, b powerLayoutPin) bool {
	for _, island := range libIslands(p) {
		hasA, hasB := false, false
		for _, pin := range island.pins {
			hasA = hasA || libSamePhysicalPin(pin, a)
			hasB = hasB || libSamePhysicalPin(pin, b)
		}
		if hasA && hasB {
			return true
		}
	}
	return false
}

func libSamePhysicalPin(a, b powerLayoutPin) bool {
	return a.Net == b.Net && a.Number == b.Number && math.Abs(a.X-b.X) <= 1e-6 && math.Abs(a.Y-b.Y) <= 1e-6
}

// A locally valid short join can still make a multi-island net impossible to
// finish. The canonical example is two opposed pins one grid step apart: their
// only outward edges become the same segment, leaving no legal T point for the
// remaining pins. Reject that partial tree while another physical island of the
// same net remains, so the net is grown through a branchable tree instead.
func libIslandMergeCanContinue(p *powerLayoutPlan, a, b powerLayoutPin) bool {
	islands := libIslands(p)
	var merged *libIsland
	for i := range islands {
		if islands[i].net != a.Net {
			continue
		}
		hasA, hasB := false, false
		for _, pin := range islands[i].pins {
			hasA = hasA || libSamePhysicalPin(pin, a)
			hasB = hasB || libSamePhysicalPin(pin, b)
		}
		if hasA && hasB {
			merged = &islands[i]
		}
	}
	if merged == nil {
		return false
	}
	// Even the final physical island needs one legal grid frontier for its
	// naming lead. Two opposed pins one grid step apart can be electrically
	// joined, but both endpoints then point into the same sealed segment and no
	// on-grid midpoint exists. Treat that as incomplete just like a tree that
	// still owes another island.
	access := libIslandAccessPoints(p, *merged)
	for state := range access {
		for direction := 1; direction <= len(mazeDirections); direction++ {
			if required, constrained := libIslandPinDirection(p, *merged, state); constrained && direction != required {
				continue
			}
			delta := mazeDirections[direction-1]
			next := mazeState{X: state.X + delta.X, Y: state.Y + delta.Y, Dir: direction}
			// A strict foreign X is traversable only as one uninterrupted edge.
			for steps := 0; steps < 1024 && mazePointStrictlyInsideForeignWire(p, a.Net, mazePoint(next)); steps++ {
				next.X += delta.X
				next.Y += delta.Y
			}
			accessState := next
			accessState.Dir = 0
			if access[accessState] {
				continue
			}
			if err := validateLibRoutingEdge(p, a.Net, mazePoint(state), mazePoint(next)); err == nil {
				return true
			}
		}
	}
	return false
}

func libMazeRoute(p *powerLayoutPlan, source, target libIsland, routing *schematicRoutingContext) ([]powerLayoutWire, error) {
	return libMazeRouteAccepted(p, source, target, routing, nil)
}

// accept can reject a geometrically valid route for a downstream obligation,
// such as preserving a completed island's marker frontier. The search then
// tries other reachable goal states within the same node allowance. A* keeps
// only one cost per (point, direction), so this is not an exhaustive search of
// all geometrically possible paths to the same goal state.
func libMazeRouteAccepted(p *powerLayoutPlan, source, target libIsland, routing *schematicRoutingContext, accept func(*powerLayoutPlan) bool) ([]powerLayoutWire, error) {
	if routing == nil {
		return nil, fmt.Errorf("routing context missing")
	}
	if policy, known := routing.policies[source.net]; known && policy != "direct" {
		return nil, fmt.Errorf("maze routing is reserved for direct nets; %s is %s", source.net, policy)
	}
	started := time.Now()
	defer func() { routing.duration += time.Since(started) }()
	callStart := routing.expanded
	attemptLimit := routing.maxNodesPerAttempt()
	rejectionsBefore := routing.rejectionCounts()
	sourceID, targetID := libIslandStableID(source), libIslandStableID(target)
	cacheSource, cacheTarget := sourceID, targetID
	if cacheSource > cacheTarget {
		cacheSource, cacheTarget = cacheTarget, cacheSource
	}
	cacheRaw, _ := json.Marshal(struct {
		Source, Target string
		Placements     []powerLayoutPlacement
		Wires          []powerLayoutWire
		Flags          []powerLayoutFlag
	}{cacheSource, cacheTarget, p.Placements, p.Wires, p.Flags})
	cacheKey := string(cacheRaw)
	templateKey := cacheSource + "\x00" + cacheTarget
	acceptRejected := false
	failure := func(kind string, cause error, cached ...schematicMazeCacheEntry) error {
		evidence, ownersComplete := routing.rejectionsSince(rejectionsBefore)
		if len(cached) > 0 {
			evidence = append([]SchematicRoutingRejection(nil), cached[0].evidence...)
			ownersComplete = cached[0].ownersComplete
		}
		// A callback checks complete terminal obligations, not just edges. Even
		// when every geometric rejection has an exact owner, those owners do
		// not explain why a joined route failed naming. This remains true if
		// A* later stops at its node limit rather than exhausting the queue.
		if acceptRejected {
			ownersComplete = false
		}
		if len(cached) == 0 && !acceptRejected && schematicMazeFailureCacheable(kind) {
			routing.cache[cacheKey] = schematicMazeCacheEntry{kind: kind, message: cause.Error(), evidence: append([]SchematicRoutingRejection(nil), evidence...), ownersComplete: ownersComplete}
		}
		layout := *p
		layout.Placements = append([]powerLayoutPlacement(nil), p.Placements...)
		layout.Wires = append([]powerLayoutWire(nil), p.Wires...)
		layout.Flags = append([]powerLayoutFlag(nil), p.Flags...)
		diagnostics := routing.snapshot()
		diagnostics.duration += time.Since(started)
		return &schematicRoutingFailure{Kind: kind, Net: source.net, SourceIsland: sourceID, TargetIsland: targetID,
			SourcePins: append([]powerLayoutPin(nil), source.pins...), TargetPins: append([]powerLayoutPin(nil), target.pins...),
			LocalLayout: &layout, Routing: diagnostics, BlockingEvidence: evidence, OwnersComplete: ownersComplete, cause: cause}
	}
	if cached, ok := routing.cache[cacheKey]; ok {
		if cached.kind != "" {
			return nil, failure(cached.kind, errors.New(cached.message), cached)
		}
		route := append([]powerLayoutWire(nil), cached.route...)
		for i := range route {
			route[i].Points = append([][2]float64(nil), route[i].Points...)
		}
		trial := *p
		trial.Wires = libAppendRoute(p.Wires, route)
		if accept == nil || accept(&trial) {
			return route, nil
		}
		acceptRejected = true
	}
	if source.net == "" || source.net != target.net || len(source.pins) == 0 || len(target.pins) == 0 {
		return nil, failure("data-missing", fmt.Errorf("source/target island evidence incomplete"))
	}
	for _, template := range routing.templates[templateKey] {
		route := clonePowerLayoutWires(template)
		trial := *p
		trial.Wires = libAppendRoute(p.Wires, route)
		err := validateLibGeometry(&trial)
		merged := err == nil && libPinsShareIsland(&trial, source.pins[0], target.pins[0])
		accepted := merged && (accept == nil || accept(&trial))
		if accepted {
			routing.completed++
			routing.templateHits++
			routing.candidates = append(routing.candidates, SchematicRoutingCandidateSummary{Net: source.net, SourceIsland: sourceID, TargetIsland: targetID, Result: "template-reused"})
			return route, nil
		} else if err != nil {
			routing.observe(err)
		} else if merged {
			acceptRejected = true
		}
	}
	searchSource, searchTarget := source, target
	starts, goals := libIslandAccessPoints(p, searchSource), libIslandAccessPoints(p, searchTarget)
	// Search from the smaller physical frontier. A trapped isolated pin then
	// proves its bounded component locally instead of flooding the whole open
	// envelope from a large line tree before placement repair can run.
	if len(goals) < len(starts) {
		searchSource, searchTarget = searchTarget, searchSource
		starts, goals = goals, starts
	}
	if len(starts) == 0 || len(goals) == 0 {
		return nil, failure("data-missing", fmt.Errorf("source/target island has no grid access point"))
	}
	var lastErr error
	for expansionIndex, expansion := range schematicRoutingExpansions {
		if routing.expanded >= routing.options.MaxExpandedNodes {
			return nil, failure("expanded-node-budget-exhausted", fmt.Errorf("%w: expanded %d of %d nodes", errSchematicExpandedBudget, routing.expanded, routing.options.MaxExpandedNodes))
		}
		if routing.expanded >= routing.usableExpandedNodes() {
			return nil, failure("relocation-budget-reserved", fmt.Errorf("expanded %d nodes; preserving the final %d nodes for blocker/dependency relocation", routing.expanded, routing.options.MaxExpandedNodes-routing.usableExpandedNodes()))
		}
		activeSource, activeTarget := searchSource, searchTarget
		if expansionIndex%2 == 1 {
			activeSource, activeTarget = activeTarget, activeSource
		}
		starts, goals = libIslandAccessPoints(p, activeSource), libIslandAccessPoints(p, activeTarget)
		goalBounds := mazeTargetBounds(goals)
		bounds := mazeSearchBounds(p, expansion)
		queue := &mazeQueue{}
		heap.Init(queue)
		best := map[mazeState]mazeCost{}
		parent := map[mazeState]mazeState{}
		seq, attemptStart := 0, routing.expanded
		for start := range starts {
			if start.X < bounds[0] || start.X > bounds[2] || start.Y < bounds[1] || start.Y > bounds[3] || mazePointStrictlyInsideForeignWire(p, source.net, mazePoint(start)) {
				continue
			}
			state := start
			state.Dir = 0
			cost := mazeCost{}
			best[state] = cost
			heap.Push(queue, &mazeItem{state: state, g: cost, f: mazeCost{Length: mazeHeuristic(state, goalBounds)}, seq: seq})
			seq++
		}
		result := "no-path"
		touchedBoundary := false
		for queue.Len() > 0 {
			if routing.expanded >= routing.options.MaxExpandedNodes {
				result = "expanded-node-budget-exhausted"
				break
			}
			if routing.expanded >= routing.usableExpandedNodes() {
				result = "relocation-budget-reserved"
				break
			}
			if routing.expanded-callStart >= attemptLimit {
				result = "route-attempt-node-limit"
				break
			}
			item := heap.Pop(queue).(*mazeItem)
			known, ok := best[item.state]
			if !ok || known != item.g {
				continue
			}
			routing.expanded++
			if item.state.X == bounds[0] || item.state.X == bounds[2] || item.state.Y == bounds[1] || item.state.Y == bounds[3] {
				touchedBoundary = true
				if expansionIndex < len(schematicRoutingExpansions)-1 {
					result = "boundary-reached"
					break
				}
			}
			goalState := item.state
			goalState.Dir = 0
			if goals[goalState] && item.state.Dir != 0 {
				points := mazeReconstruct(item.state, parent)
				route := libPointsRoute(source.net, points...)
				trial := *p
				trial.Wires = libAppendRoute(p.Wires, route)
				err := validateLibGeometry(&trial)
				merged := err == nil && libPinsShareIsland(&trial, source.pins[0], target.pins[0])
				accepted := merged && (accept == nil || accept(&trial))
				if merged && !accepted {
					acceptRejected = true
				}
				if accepted {
					routing.completed++
					routing.candidates = append(routing.candidates, SchematicRoutingCandidateSummary{Net: source.net, SourceIsland: sourceID, TargetIsland: targetID, Points: points, Result: "merged"})
					result = "merged"
					routing.boundaries = append(routing.boundaries, SchematicRoutingBoundaryAttempt{Net: source.net, SourceIsland: sourceID, TargetIsland: targetID, ExpansionRaw: expansion, ExpandedNodes: routing.expanded - attemptStart, Result: result})
					cachedRoute := append([]powerLayoutWire(nil), route...)
					for i := range cachedRoute {
						cachedRoute[i].Points = append([][2]float64(nil), cachedRoute[i].Points...)
					}
					routing.cache[cacheKey] = schematicMazeCacheEntry{route: cachedRoute}
					if len(routing.templates[templateKey]) < 8 {
						routing.templates[templateKey] = append(routing.templates[templateKey], clonePowerLayoutWires(route))
					}
					return route, nil
				} else if err != nil {
					routing.observe(err)
					lastErr = err
				} else if merged {
					lastErr = fmt.Errorf("route reaches the target but closes a required terminal frontier")
				} else {
					lastErr = fmt.Errorf("candidate did not merge the specified islands")
				}
				result := "final-validation-failed"
				if merged {
					result = "terminal-frontier-rejected"
				}
				routing.candidates = append(routing.candidates, SchematicRoutingCandidateSummary{Net: source.net, SourceIsland: sourceID, TargetIsland: targetID, Points: points, Result: result})
				continue
			}
			for direction := 1; direction <= 4; direction++ {
				if item.state.Dir == 0 {
					if required, constrained := libIslandPinDirection(p, activeSource, item.state); constrained && direction != required {
						continue
					}
				}
				next, ok := mazeAdvanceAcrossForeignX(p, source.net, item.state, direction, bounds)
				if !ok || !mazeEdgeValid(p, source.net, item.state, next, routing) {
					continue
				}
				stepLength := int(math.Abs(float64(next.X-item.state.X)) + math.Abs(float64(next.Y-item.state.Y)))
				cost := item.g
				cost.Length += stepLength
				if item.state.Dir != 0 && item.state.Dir != direction {
					cost.Bends++
				}
				cost.Crossings += mazeCrossings(p, source.net, mazePoint(item.state), mazePoint(next))
				next.Dir = direction
				if old, exists := best[next]; exists && !mazeCostLess(cost, old) {
					continue
				}
				best[next], parent[next] = cost, item.state
				priority := cost
				priority.Length += mazeHeuristic(next, goalBounds)
				heap.Push(queue, &mazeItem{state: next, g: cost, f: priority, seq: seq})
				seq++
			}
		}
		routing.boundaries = append(routing.boundaries, SchematicRoutingBoundaryAttempt{Net: source.net, SourceIsland: sourceID, TargetIsland: targetID, ExpansionRaw: expansion, ExpandedNodes: routing.expanded - attemptStart, Result: result})
		if result == "expanded-node-budget-exhausted" {
			return nil, failure(result, fmt.Errorf("%w: expanded %d of %d nodes", errSchematicExpandedBudget, routing.expanded, routing.options.MaxExpandedNodes))
		}
		if result == "relocation-budget-reserved" {
			return nil, failure(result, fmt.Errorf("expanded %d nodes; preserving the final %d nodes for blocker/dependency relocation", routing.expanded, routing.options.MaxExpandedNodes-routing.usableExpandedNodes()))
		}
		if result == "route-attempt-node-limit" {
			return nil, failure(result, fmt.Errorf("island-pair attempt expanded %d nodes; preserving the shared %d-node zone budget for reroute/relocation", routing.expanded-callStart, routing.options.MaxExpandedNodes))
		}
		if !touchedBoundary {
			if acceptRejected {
				return nil, failure("terminal-frontier-rejected", fmt.Errorf("bounded A* found a geometric join but no accepted alternative goal state"))
			}
			return nil, failure("no-path-within-bounds", fmt.Errorf("reachable component from %s is enclosed inside the %g raw envelope", libIslandStableID(activeSource), expansion))
		}
	}
	if acceptRejected {
		return nil, failure("terminal-frontier-rejected", fmt.Errorf("bounded A* found a geometric join but no accepted alternative goal state"))
	}
	if lastErr != nil {
		return nil, failure("final-validation-failed", lastErr)
	}
	return nil, failure("no-path-within-bounds", fmt.Errorf("no legal path in 40/80/160/320 raw expanded envelopes"))
}

func schematicMazeFailureCacheable(kind string) bool {
	switch kind {
	case "expanded-node-budget-exhausted", "relocation-budget-reserved", "route-attempt-node-limit", "terminal-frontier-rejected":
		return false
	default:
		return true
	}
}

func clonePowerLayoutWires(wires []powerLayoutWire) []powerLayoutWire {
	out := make([]powerLayoutWire, len(wires))
	for i, wire := range wires {
		out[i] = wire
		out[i].Points = append([][2]float64(nil), wire.Points...)
	}
	return out
}
