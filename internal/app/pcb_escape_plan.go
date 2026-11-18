package app

// Escape reservations are geometric witnesses, not editor copper and not a
// whole-board routing claim. Every owner pad is accounted for independently.
import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
)

type pcbRoutingIntent struct {
	Owners          []string          `json:"owners"`
	Demands         []pcbEscapeDemand `json:"demands"`
	RequiredTopNets []string          `json:"requiredTopNets,omitempty"`
	StepMil         float64           `json:"stepMil"`
	MaxDetourMil    float64           `json:"maxDetourMil"`
	MaxStates       int               `json:"maxStates"`
}

type pcbEscapeDemand struct {
	ID                      string            `json:"id"`
	Kind                    string            `json:"kind,omitempty"` // signal (default) | power | ground
	From                    string            `json:"from"`
	Net                     string            `json:"net,omitempty"`
	To                      string            `json:"to,omitempty"`
	Exits                   [][2]float64      `json:"exits,omitempty"`
	Toward                  string            `json:"toward,omitempty"`
	Congested               *layoutBBox       `json:"congested,omitempty"`
	WidthMil                float64           `json:"widthMil,omitempty"`
	Layer                   int               `json:"layer,omitempty"`
	MaxVias                 int               `json:"maxVias"`
	MaxAwayMil              float64           `json:"maxAwayMil,omitempty"`
	NC                      bool              `json:"nc,omitempty"`
	NCReason                string            `json:"ncReason,omitempty"`
	ExistingViasOnly        bool              `json:"existingViasOnly,omitempty"`
	ExistingViaIDs          []string          `json:"existingViaIds,omitempty"`
	ExistingViaInPadAnchors map[string]string `json:"existingViaInPadAnchors,omitempty"`
}

type pcbEscapeBudget struct {
	MaxStates int  `json:"maxStates"`
	Used      int  `json:"used"`
	Exhausted bool `json:"exhausted"`
}

type pcbEscapeReport struct {
	SchemaVersion       int                `json:"schemaVersion"`
	Status              string             `json:"status"` // pass | fail | incomplete
	BoardSemanticSHA256 string             `json:"boardSemanticSha256"`
	IntentSHA256        string             `json:"intentSha256"`
	BoardSHA256         string             `json:"boardSha256,omitempty"`
	LayoutSHA256        string             `json:"layoutSha256,omitempty"`
	Routes              []pcbModuleRoute   `json:"routes"`
	Vias                []pcbModuleVia     `json:"vias"`
	Witnesses           []pcbEscapeWitness `json:"witnesses,omitempty"`
	ExistingViaCounts   map[string]int     `json:"existingViaCounts,omitempty"`
	Budget              pcbEscapeBudget    `json:"budget"`
	Reasons             []string           `json:"reasons,omitempty"`
	Limitations         []string           `json:"limitations,omitempty"`
}

type pcbEscapeWitness struct {
	DemandID       string   `json:"demandId"`
	RouteIDs       []string `json:"routeIds"`
	ViaIDs         []string `json:"viaIds,omitempty"`
	ExistingViaIDs []string `json:"existingViaIds,omitempty"`
}

type pcbEscapeChoice struct {
	routes  []pcbModuleRoute
	vias    []pcbModuleVia
	witness pcbEscapeWitness
}

type pcbEscapeContext struct {
	snap      *boardSnapshot
	pads      map[string]pcbPadP
	ambiguous map[string]bool
	allPads   []pcbPadP
	tracks    []pcbTrack
	arcs      []pcbArc
	vias      []pcbViaP
	areas     []pcbCopperArea
	bans      []pcbCopperArea
	// slots and strokes are filled only by callers that load
	// pcbBoardCopperExtras (routability); escape-plan leaves them empty.
	slots   []pcbCopperArea
	strokes []pcbStrokedArea
}

func pcbEscapeIntentHash(in pcbRoutingIntent) string {
	b, _ := json.Marshal(in)
	return sha256String(b)
}

func preparePCBEscape(in pcbRoutingIntent, snap *boardSnapshot) (*pcbEscapeContext, string, error) {
	fail := func(msg string) (*pcbEscapeContext, string, error) { return nil, "fail", fmt.Errorf("%s", msg) }
	unknown := func(msg string) (*pcbEscapeContext, string, error) { return nil, "incomplete", fmt.Errorf("%s", msg) }
	if !allFinite(in.StepMil, in.MaxDetourMil) || in.StepMil <= 0 || in.MaxDetourMil < 0 || in.MaxDetourMil/in.StepMil > 100 || in.MaxStates < 1 || in.MaxStates > 2000000 {
		return fail("routing requires finite stepMil > 0, 0 <= maxDetourMil <= 100*stepMil, and 1 <= maxStates <= 2000000")
	}
	if len(in.Owners) == 0 || len(in.Owners) > 128 || len(in.Demands) > 1024 {
		return fail("routing requires 1..128 owners and at most 1024 pad demands")
	}
	ctx, status, err := preparePCBRouteGeometry(snap)
	if err != nil {
		return nil, status, err
	}
	r := snap.Rules
	owners, required := map[string]bool{}, map[string]bool{}
	for _, owner := range in.Owners {
		if owners[owner] || strings.TrimSpace(owner) == "" {
			return fail("empty/duplicate routing owner " + owner)
		}
		owners[owner] = true
		count := 0
		for key, p := range ctx.pads {
			if p.Designator == owner {
				if ctx.ambiguous[key] {
					return unknown("owner pad needs unambiguous per-primitive identity: " + key)
				}
				required[key] = true
				count++
			}
		}
		if count == 0 {
			return unknown("routing owner has no measured pads: " + owner)
		}
	}
	ids, covered := map[string]bool{}, map[string]bool{}
	strictNets := map[string]bool{}
	for _, n := range in.RequiredTopNets {
		if strings.TrimSpace(n) == "" || strictNets[n] {
			return fail("empty/duplicate requiredTopNets entry")
		}
		strictNets[n] = true
	}
	for _, d := range in.Demands {
		if d.ID == "" || ids[d.ID] {
			return fail("empty/duplicate demand id " + d.ID)
		}
		ids[d.ID] = true
		if !required[d.From] || covered[d.From] {
			return fail("demand from must cover a unique owner pad: " + d.From)
		}
		covered[d.From] = true
		from := ctx.pads[d.From]
		if d.NC {
			if strings.TrimSpace(d.NCReason) == "" || d.To != "" || len(d.Exits) != 0 || d.Toward != "" || d.ExistingViasOnly || len(d.ExistingViaIDs) != 0 || len(d.ExistingViaInPadAnchors) != 0 {
				return fail("NC needs a reason and no route: " + d.From)
			}
			continue
		}
		if d.NCReason != "" || strings.TrimSpace(d.Net) == "" || from.Net != d.Net {
			return fail("demand net/NC disagrees with measured pad " + d.From)
		}
		if d.Kind != "" && d.Kind != "signal" && d.Kind != "power" && d.Kind != "ground" {
			return fail("routing demand kind must be signal, power or ground: " + d.ID)
		}
		if len(d.ExistingViaIDs) > 8 || len(d.ExistingViaIDs) != len(d.ExistingViaInPadAnchors) {
			return fail("existing via reuse requires at most eight explicit via-in-pad anchor bindings: " + d.ID)
		}
		if len(d.ExistingViaIDs) > 0 {
			if d.Kind != "ground" || d.Layer != 1 || strictNets[d.Net] {
				return fail("existing in-pad entrance is supported only for explicitly declared TOP ground demand")
			}
			seen := map[string]bool{}
			for _, id := range d.ExistingViaIDs {
				if seen[id] || d.ExistingViaInPadAnchors[id] != d.From {
					return fail("existing via anchor must uniquely name the demand source pad: " + id)
				}
				seen[id] = true
				if _, err := ctx.existingGroundVia(d, id); err != nil {
					return unknown(err.Error())
				}
			}
		}
		if !allFinite(d.WidthMil, d.MaxAwayMil) || d.WidthMil < r.TrackWidthMinMil || d.MaxVias < 0 || d.MaxVias > 2 || d.MaxAwayMil < 0 || d.MaxAwayMil > in.MaxDetourMil || d.Layer != 1 && d.Layer != 2 {
			return fail("invalid width/layer/maxVias for " + d.ID)
		}
		if strictNets[d.Net] && (d.Layer != 1 || d.MaxVias != 0) {
			return fail("strict signal must be TOP, 0 via: " + d.Net)
		}
		if strictNets[d.Net] && pcbEscapeViaCount(ctx, d.Net, nil) > 0 {
			return fail("strict TOP network has existing via: " + d.Net)
		}
		if pcbEscapeDemandViaCount(ctx, d, nil) > d.MaxVias {
			return fail("existing whole-net via count exceeds declared maxVias: " + d.Net)
		}
		if strictNets[d.Net] {
			for _, t := range ctx.tracks {
				if t.Net == d.Net && t.Layer != 1 {
					return fail("strict TOP network has existing off-TOP track: " + d.Net)
				}
			}
			for _, a := range ctx.arcs {
				if a.Net == d.Net && a.Layer != 1 {
					return fail("strict TOP network has existing off-TOP arc: " + d.Net)
				}
			}
		}
		if !padLayerMatches(from.Layer, d.Layer) {
			return unknown("source requires a layer transition not yet implemented: " + d.From)
		}
		if d.Layer == 2 && snap.CopperLayers < 2 {
			return unknown("BOTTOM route requires measured stackup with at least two copper layers")
		}
		if d.ExistingViasOnly {
			if d.Kind != "ground" || d.Layer != 1 || len(d.ExistingViaIDs) == 0 || d.MaxVias != 0 || d.MaxAwayMil != 0 || d.To != "" || d.Toward != "" || len(d.Exits) != 0 || d.Congested != nil {
				return fail("existingViasOnly requires TOP ground, one or more bound in-pad vias, maxVias 0, and no route fields: " + d.ID)
			}
			continue
		}
		if d.To != "" {
			if d.Toward != "" || d.To == d.From || len(d.Exits) > 32 {
				return fail("full demand uses To with optional forced Exits+Congested: " + d.ID)
			}
			to, ok := ctx.pads[d.To]
			if !ok || ctx.ambiguous[d.To] {
				return unknown("destination pad is unknown: " + d.To)
			}
			if to.Net != d.Net {
				return fail("destination net mismatch: " + d.To)
			}
			if !padLayerMatches(to.Layer, d.Layer) {
				return unknown("destination requires a layer transition not yet implemented: " + d.To)
			}
			if len(d.Exits) > 0 {
				b := d.Congested
				if b == nil || !allFinite(b.MinX, b.MinY, b.MaxX, b.MaxY) || b.MinX >= b.MaxX || b.MinY >= b.MaxY || from.X < b.MinX || from.X > b.MaxX || from.Y < b.MinY || from.Y > b.MaxY {
					return fail("forced full-route exits require congested bounds containing source: " + d.ID)
				}
				for _, p := range d.Exits {
					if !allFinite(p[0], p[1]) || rectPtDist(b.MinX, b.MinY, b.MaxX, b.MaxY, p[0], p[1]) < d.WidthMil/2+r.ClearanceMil || !pcbEscapeExitWithinAway(from, to, p, d.MaxAwayMil) {
						return fail("forced full-route exit must clear congested envelope within maxAwayMil: " + d.ID)
					}
				}
			} else if d.Congested != nil {
				return fail("full demand congested bounds require forced exits: " + d.ID)
			}
		} else {
			if len(d.Exits) < 1 || len(d.Exits) > 32 || d.Congested == nil {
				return fail("local escape requires 1..32 exits and congested polygon bounds: " + d.ID)
			}
			to, ok := ctx.pads[d.Toward]
			if !ok || ctx.ambiguous[d.Toward] {
				return unknown("toward target pad is unknown: " + d.Toward)
			}
			if to.Net != d.Net {
				return fail("toward target net mismatch: " + d.Toward)
			}
			b := d.Congested
			if !allFinite(b.MinX, b.MinY, b.MaxX, b.MaxY) || b.MinX >= b.MaxX || b.MinY >= b.MaxY || from.X < b.MinX || from.X > b.MaxX || from.Y < b.MinY || from.Y > b.MaxY {
				return fail("congested bounds must contain source: " + d.ID)
			}
			for _, p := range d.Exits {
				if !allFinite(p[0], p[1]) || rectPtDist(b.MinX, b.MinY, b.MaxX, b.MaxY, p[0], p[1]) < d.WidthMil/2+r.ClearanceMil || !pcbEscapeExitWithinAway(from, to, p, d.MaxAwayMil) {
					return fail("exit must clear congested envelope and advance toward target: " + d.ID)
				}
			}
		}
	}
	var missing []string
	for key := range required {
		if !covered[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return unknown("owner pads lack routing/explicit NC demands: " + strings.Join(missing, ", "))
	}
	return ctx, "", nil
}

func pcbEscapeExitWithinAway(from, to pcbPadP, exit [2]float64, maxAway float64) bool {
	base := math.Hypot(from.X-to.X, from.Y-to.Y)
	if base <= netPathGeomEps || math.Hypot(exit[0]-to.X, exit[1]-to.Y) > base+maxAway+netPathGeomEps {
		return false
	}
	// Bound retreat along the source→target axis as well as radial distance.
	// maxAway=0 retains the old monotonic rule but permits a perpendicular
	// first escape (zero projection), which is essential at fine-pitch rows.
	projection := ((exit[0]-from.X)*(to.X-from.X) + (exit[1]-from.Y)*(to.Y-from.Y)) / base
	return projection >= -maxAway-netPathGeomEps
}

func (b *pcbEscapeBudget) take() bool {
	if b.Used >= b.MaxStates {
		b.Exhausted = true
		return false
	}
	b.Used++
	return true
}

func (ctx *pcbEscapeContext) clear(d pcbEscapeDemand, routes []pcbModuleRoute, plannedVias ...pcbModuleVia) (func([2]float64, [2]float64) bool, error) {
	tracks := append([]pcbTrack(nil), ctx.tracks...)
	for _, route := range routes {
		for j := 1; j < len(route.Points); j++ {
			a, z := route.Points[j-1], route.Points[j]
			tracks = append(tracks, pcbTrack{Net: route.Net, Layer: route.Layer, Width: route.WidthMil, X1: a[0], Y1: a[1], X2: z[0], Y2: z[1]})
		}
	}
	vias := append([]pcbViaP(nil), ctx.vias...)
	for _, v := range plannedVias {
		vias = append(vias, pcbViaP{ID: v.ID, Net: v.Net, X: v.X, Y: v.Y, Dia: v.DiameterMil, Hole: v.HoleMil})
	}
	// Keep the exact pad check outside crystalRouteClear's historical nominal
	// extent broad phase: a rotated asymmetric pad may extend beyond W/H.
	clear, err := crystalRouteClear(d.Net, d.Layer, d.WidthMil, ctx.snap, nil, tracks, ctx.arcs, vias, ctx.areas)
	if err != nil {
		return nil, err
	}
	return func(a, z [2]float64) bool {
		if !clear(a, z) {
			return false
		}
		for _, pad := range ctx.allPads {
			if pad.Net == d.Net || !padLayerMatches(pad.Layer, d.Layer) {
				continue
			}
			gap, err := pcbExactPadSegmentGap(pad, a, z)
			if err != nil || gap < d.WidthMil/2+ctx.snap.Rules.ClearanceMil-netPathGeomEps {
				return false
			}
		}
		for _, ban := range ctx.bans {
			if padLayerMatches(ban.Layer, d.Layer) && copperAreaSegmentDistance(ban, a, z) < d.WidthMil/2+ctx.snap.Rules.ClearanceMil-netPathGeomEps {
				return false
			}
		}
		for _, slot := range ctx.slots {
			if copperAreaSegmentDistance(slot, a, z) < d.WidthMil/2+pcbSlotClearanceMil(ctx.snap.Rules)-netPathGeomEps {
				return false
			}
		}
		for _, stroke := range ctx.strokes {
			if stroke.area.Net != d.Net && stroke.area.Layer == d.Layer && stroke.segmentDistance(a, z) < d.WidthMil/2+ctx.snap.Rules.ClearanceMil-netPathGeomEps {
				return false
			}
		}
		return true
	}, nil
}

func pcbEscapeViaCount(ctx *pcbEscapeContext, net string, planned []pcbModuleVia) int {
	count := 0
	for _, v := range ctx.vias {
		if v.Net == net {
			count++
		}
	}
	for _, v := range planned {
		if v.Net == net {
			count++
		}
	}
	return count
}

func pcbEscapeDemandViaCount(ctx *pcbEscapeContext, d pcbEscapeDemand, planned []pcbModuleVia) int {
	if d.Kind == "ground" || d.Kind == "power" {
		count := 0
		for _, v := range planned {
			if v.Net == d.Net {
				count++
			}
		}
		return count
	}
	return pcbEscapeViaCount(ctx, d.Net, planned)
}

// Reusing a ground EP via is an explicit identity-bound exception, never a
// general GND via-in-pad permission. A disk wholly inside the measured pad is
// a conservative real-copper contact proof for every supported pad shape.
func (ctx *pcbEscapeContext) existingGroundVia(d pcbEscapeDemand, id string) (pcbModuleVia, error) {
	if d.Kind != "ground" || !containsString(d.ExistingViaIDs, id) || d.ExistingViaInPadAnchors[id] != d.From {
		return pcbModuleVia{}, fmt.Errorf("existing via %s lacks an explicit source EP ground binding", id)
	}
	var found *pcbViaP
	for i, v := range ctx.vias {
		if v.ID == id {
			if found != nil {
				return pcbModuleVia{}, fmt.Errorf("ambiguous existing via %s", id)
			}
			found = &ctx.vias[i]
		}
	}
	if found == nil {
		return pcbModuleVia{}, fmt.Errorf("declared existing ground via is missing: %s", id)
	}
	p, ok := ctx.pads[d.From]
	if !ok || ctx.ambiguous[d.From] || p.Net != d.Net || found.Net != d.Net || !padLayerMatches(p.Layer, 1) {
		return pcbModuleVia{}, fmt.Errorf("existing via/pad net or layer mismatch: %s", id)
	}
	if math.Hypot(found.X-p.X, found.Y-p.Y)+found.Dia/2 > math.Min(p.ShapeW, p.ShapeH)/2-netPathGeomEps {
		return pcbModuleVia{}, fmt.Errorf("existing ground via %s is not proven fully bonded inside source pad %s", id, d.From)
	}
	return pcbModuleVia{ID: id, Net: found.Net, X: found.X, Y: found.Y, HoleMil: found.Hole, DiameterMil: found.Dia, Role: "existing-ground-ep-entry"}, nil
}

func pcbEscapeEntryInsideMeasuredPad(ctx *pcbEscapeContext, d pcbEscapeDemand, points [][2]float64) bool {
	p := ctx.pads[d.From]
	radius := math.Min(p.ShapeW, p.ShapeH)/2 - d.WidthMil/2
	if !pcbEscapePath45(points) || radius <= 0 {
		return false
	}
	for _, point := range points {
		if math.Hypot(point[0]-p.X, point[1]-p.Y) > radius-netPathGeomEps {
			return false
		}
	}
	return true
}

func (ctx *pcbEscapeContext) viaClear(v pcbModuleVia, routes []pcbModuleRoute, planned []pcbModuleVia) bool {
	if !allFinite(v.X, v.Y, v.HoleMil, v.DiameterMil) || v.HoleMil <= 0 || v.DiameterMil <= v.HoleMil {
		return false
	}
	var others []pcbModuleVia
	for _, via := range planned {
		if via.ID != v.ID {
			others = append(others, via)
		}
	}
	clear, err := ctx.viaSiteChecker(v.Net, v.DiameterMil, routes, others)
	return err == nil && clear(v.X, v.Y)
}

// viaCopperLayers is TOP, BOTTOM and every other copper layer with measured
// tracks, arcs, areas or keepouts.
func (ctx *pcbEscapeContext) viaCopperLayers() []int {
	seen := map[int]bool{1: true, 2: true}
	layers := []int{1, 2}
	add := func(layer int) {
		if netPathCopperLayer(layer) && !seen[layer] {
			seen[layer] = true
			layers = append(layers, layer)
		}
	}
	for _, t := range ctx.tracks {
		add(t.Layer)
	}
	for _, a := range ctx.arcs {
		add(a.Layer)
	}
	for _, a := range ctx.areas {
		add(a.Layer)
	}
	for _, b := range ctx.bans {
		add(b.Layer)
	}
	for _, st := range ctx.strokes {
		add(st.area.Layer)
	}
	sort.Ints(layers)
	return layers
}

// viaSiteChecker builds the exact through-via site test once so callers that
// probe many sites (routability flood) do not rebuild per-layer clearance
// closures for every point. A through via crosses every copper layer, so each
// layer that carries copper is checked.
func (ctx *pcbEscapeContext) viaSiteChecker(net string, diameter float64, routes []pcbModuleRoute, planned []pcbModuleVia) (func(x, y float64) bool, error) {
	radius := diameter / 2
	rules := ctx.snap.Rules
	var layers []func([2]float64, [2]float64) bool
	for _, layer := range ctx.viaCopperLayers() {
		clear, err := ctx.clear(pcbEscapeDemand{Net: net, Layer: layer, WidthMil: diameter}, routes)
		if err != nil {
			return nil, err
		}
		layers = append(layers, clear)
	}
	return func(x, y float64) bool {
		p := [2]float64{x, y}
		if !allFinite(x, y) || !ctx.snap.Outline.containsPoint(x, y) || ctx.snap.Outline.distToEdge(x, y) < radius+rules.CopperToEdgeMil-netPathGeomEps {
			return false
		}
		// Explicitly includes same-net pads: ordinary escape vias are never in-pad.
		for _, pad := range ctx.allPads {
			distance, err := pcbExactPadSegmentGap(pad, p, p)
			if err != nil || distance < radius+rules.ClearanceMil-netPathGeomEps {
				return false
			}
		}
		for _, via := range ctx.vias {
			if math.Hypot(via.X-x, via.Y-y) < radius+via.Dia/2+rules.ClearanceMil-netPathGeomEps {
				return false
			}
		}
		for _, via := range planned {
			if math.Hypot(via.X-x, via.Y-y) < radius+via.DiameterMil/2+rules.ClearanceMil-netPathGeomEps {
				return false
			}
		}
		for _, clear := range layers {
			if !clear(p, p) {
				return false
			}
		}
		return true
	}, nil
}

// Try only measured TOP -> BOTTOM -> TOP witnesses. Both vias must clear all
// pads/copper and count together with existing vias on the entire network.
func pcbEscapeViaChoices(ctx *pcbEscapeContext, in pcbRoutingIntent, d pcbEscapeDemand, routes []pcbModuleRoute, planned []pcbModuleVia, budget *pcbEscapeBudget) []pcbEscapeChoice {
	if d.To != "" && len(d.Exits) > 0 {
		// Forced TOP breakout followed by a layer transition needs a separately
		// modelled waypoint/layer contract; do not silently discard the waypoint.
		return nil
	}
	newVias := 2
	if len(d.ExistingViaIDs) > 0 {
		newVias = 1
	}
	if d.Layer != 1 || d.MaxVias < newVias || pcbEscapeDemandViaCount(ctx, d, planned)+newVias > d.MaxVias || ctx.snap.CopperLayers != 2 {
		return nil
	}
	rules := ctx.snap.Rules
	if !allFinite(rules.ViaDrillMil, rules.ViaDiameterMil) || rules.ViaDrillMil <= 0 || rules.ViaDiameterMil <= rules.ViaDrillMil {
		return nil
	}
	from := ctx.pads[d.From]
	a := [2]float64{from.X, from.Y}
	exits := d.Exits
	if d.To != "" {
		to := ctx.pads[d.To]
		exits = [][2]float64{{to.X, to.Y}}
	}
	top, err := ctx.clear(d, routes, planned...)
	if err != nil {
		return nil
	}
	bottomDemand := d
	bottomDemand.Layer = 2
	bottom, err := ctx.clear(bottomDemand, routes, planned...)
	if err != nil {
		return nil
	}
	makeSites := func(endpoint, toward [2]float64) []pcbModuleVia {
		var out []pcbModuleVia
		for dist := in.StepMil; dist <= in.MaxDetourMil+1e-7 && len(out) < 16; dist += in.StepMil {
			for _, dir := range [][2]float64{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}} {
				if !budget.take() {
					return out
				}
				p := [2]float64{endpoint[0] + dir[0]*dist, endpoint[1] + dir[1]*dist}
				v := pcbModuleVia{Net: d.Net, X: p[0], Y: p[1], HoleMil: rules.ViaDrillMil, DiameterMil: rules.ViaDiameterMil, Role: "escape-layer-transition"}
				if ctx.viaClear(v, routes, planned) {
					out = append(out, v)
				}
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			return math.Hypot(out[i].X-toward[0], out[i].Y-toward[1]) < math.Hypot(out[j].X-toward[0], out[j].Y-toward[1])
		})
		return out
	}
	var out []pcbEscapeChoice
	for _, z := range exits {
		var left []pcbModuleVia
		if len(d.ExistingViaIDs) > 0 {
			for _, id := range d.ExistingViaIDs {
				v, err := ctx.existingGroundVia(d, id)
				if err == nil {
					left = append(left, v)
				}
			}
		} else {
			left = makeSites(a, z)
		}
		right := makeSites(z, a)
		for _, v1 := range left {
			for _, v2 := range right {
				if !budget.take() {
					return out
				}
				existing := containsString(d.ExistingViaIDs, v1.ID)
				if !existing {
					v1.ID = d.ID + "/via-1"
				}
				v2.ID = d.ID + "/via-2"
				if !ctx.viaClear(v2, routes, append(append([]pcbModuleVia(nil), planned...), v1)) {
					continue
				}
				p, q := [2]float64{v1.X, v1.Y}, [2]float64{v2.X, v2.Y}
				entry := pcbEscapeGrid(a, p, in.StepMil, in.MaxDetourMil, top, budget)
				if len(entry) < 2 {
					continue
				}
				if existing && !pcbEscapeEntryInsideMeasuredPad(ctx, d, entry) {
					continue
				}
				exit := pcbEscapeGrid(q, z, in.StepMil, in.MaxDetourMil, top, budget)
				if len(exit) < 2 {
					continue
				}
				middle := pcbEscapeGrid(p, q, in.StepMil, in.MaxDetourMil, bottom, budget)
				if len(middle) < 2 {
					continue
				}
				if d.To == "" {
					to := ctx.pads[d.Toward]
					prev := exit[len(exit)-2]
					if d.MaxAwayMil == 0 && (z[0]-prev[0])*(to.X-z[0])+(z[1]-prev[1])*(to.Y-z[1]) < -netPathGeomEps {
						continue
					}
				}
				if !pcbEscapePath45(entry) || !pcbEscapePath45(exit) || !pcbEscapePath45(middle) {
					continue
				}
				segments := []pcbModuleRoute{{ID: d.ID + "/top-entry", Net: d.Net, Layer: 1, WidthMil: d.WidthMil, Points: entry, Role: "escape-reservation", From: d.From}, {ID: d.ID + "/bottom", Net: d.Net, Layer: 2, WidthMil: d.WidthMil, Points: middle, Role: "escape-reservation"}, {ID: d.ID + "/top-exit", Net: d.Net, Layer: 1, WidthMil: d.WidthMil, Points: exit, Role: "escape-reservation", To: d.To}}
				choice := pcbEscapeChoice{routes: segments, vias: []pcbModuleVia{v1, v2}, witness: pcbEscapeWitness{DemandID: d.ID, RouteIDs: []string{segments[0].ID, segments[1].ID, segments[2].ID}, ViaIDs: []string{v1.ID, v2.ID}}}
				if existing {
					choice.vias = []pcbModuleVia{v2}
					choice.witness.ExistingViaIDs = []string{v1.ID}
				}
				out = append(out, choice)
				if len(out) >= 4 {
					return out
				}
			}
		}
	}
	return out
}

// Grid search delegates to the public routing kernel. The adapter only accounts
// for the escape planner's shared budget and converts its legacy callback shape;
// there is no second A* implementation here.
func pcbEscapeGrid(a, z [2]float64, step, detour float64, clear func([2]float64, [2]float64) bool, budget *pcbEscapeBudget) [][2]float64 {
	remaining := budget.MaxStates - budget.Used
	if remaining <= 0 {
		budget.Exhausted = true
		return nil
	}
	// Preserve the former per-attempt bound: one impossible segment must not
	// consume the complete joint-search budget before alternate demand orders.
	maxStates := remaining
	if maxStates > 8192 {
		maxStates = 8192
	}
	result, err := pcbrouting.Solve(context.Background(), pcbrouting.Request{
		From: a, To: z, Step: step, MaxDetour: detour, MaxStates: maxStates,
	}, clear)
	budget.Used += result.States
	if budget.Used >= budget.MaxStates {
		budget.Exhausted = true
	}
	if err != nil || result.Status != pcbrouting.Found {
		return nil
	}
	return result.Points
}

func pcbEscapeCandidates(ctx *pcbEscapeContext, in pcbRoutingIntent, d pcbEscapeDemand, routes []pcbModuleRoute, budget *pcbEscapeBudget, plannedVias ...pcbModuleVia) []pcbModuleRoute {
	clear, err := ctx.clear(d, routes, plannedVias...)
	if err != nil {
		return nil
	}
	from := ctx.pads[d.From]
	a := [2]float64{from.X, from.Y}
	exits := d.Exits
	if d.To != "" {
		p := ctx.pads[d.To]
		exits = [][2]float64{{p.X, p.Y}}
	}
	var out []pcbModuleRoute
	seen := map[string]bool{}
	add := func(points [][2]float64) {
		if len(points) < 2 {
			return
		}
		var ok bool
		points, ok = crystalBevelRoute(crystalCompressRoute(points), clear)
		if !ok {
			return
		}
		if !pcbEscapePath45(points) {
			return
		}
		for i := 1; i < len(points); i++ {
			if !clear(points[i-1], points[i]) {
				return
			}
		}
		if d.To == "" {
			to := ctx.pads[d.Toward]
			z, p := points[len(points)-1], points[len(points)-2]
			if d.MaxAwayMil == 0 && (z[0]-p[0])*(to.X-z[0])+(z[1]-p[1])*(to.Y-z[1]) < -netPathGeomEps {
				return
			}
		} else {
			to := ctx.pads[d.To]
			last := points[len(points)-1]
			if math.Hypot(last[0]-to.X, last[1]-to.Y) > netPathGeomEps {
				return
			}
			if len(d.Exits) > 0 {
				forced := false
				for _, exit := range d.Exits {
					forced = forced || pcbEscapeRouteContainsPoint(points, exit)
				}
				if !forced {
					return
				}
			}
		}
		raw, _ := json.Marshal(points)
		key := string(raw)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, pcbModuleRoute{ID: d.ID, Net: d.Net, Layer: d.Layer, WidthMil: d.WidthMil, Points: points, Role: "escape-reservation", From: d.From, To: d.To})
	}
	if d.To != "" && len(d.Exits) > 0 {
		to := ctx.pads[d.To]
		target := [2]float64{to.X, to.Y}
		for _, entry := range d.Exits {
			if !budget.take() {
				break
			}
			// A balanced 45-degree path can approach a forced waypoint with a
			// heading that makes the following segment a 90-degree turn. The
			// generic bevel then correctly removes that sharp corner, but doing so
			// also removes the declared waypoint from the proof. Enumerate the two
			// equally short orthogonal/diagonal orders on both sides so a compatible
			// <=45-degree join can retain the exact forced breakout point.
			prefixes := pcbEscape45PathVariants(a, entry)
			prefixes = append(prefixes, pcbEscapeGrid(a, entry, in.StepMil, in.MaxDetourMil, clear, budget))
			for _, prefix := range prefixes {
				suffixes := pcbEscape45PathVariants(entry, target)
				suffixes = append(suffixes, pcbEscapeGrid(entry, target, in.StepMil, in.MaxDetourMil, clear, budget))
				suffixes = append(suffixes, pcbEscapeForcedContinuationSuffixes(prefix, entry, target, in, clear, budget)...)
				for _, suffix := range suffixes {
					if len(prefix) < 2 || len(suffix) < 2 {
						continue
					}
					add(append(append([][2]float64(nil), prefix...), suffix[1:]...))
				}
			}
		}
		return out
	}
	for _, z := range exits {
		if !budget.take() {
			break
		}
		add(crystal45Path(a, z))
		reverse := crystal45Path(z, a)
		for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
			reverse[i], reverse[j] = reverse[j], reverse[i]
		}
		add(reverse)
		add(pcbEscapeGrid(a, z, in.StepMil, in.MaxDetourMil, clear, budget))
		// Alternative corridors are generated before committing a net. A later
		// demand may force backtracking to a longer route for an earlier demand.
		for delta := in.StepMil; delta <= in.MaxDetourMil+1e-7 && len(out) < 12 && !budget.Exhausted; delta += in.StepMil {
			for _, offset := range [][2]float64{{0, delta}, {0, -delta}, {delta, 0}, {-delta, 0}} {
				if !budget.take() {
					break
				}
				mid := [2]float64{(a[0]+z[0])/2 + offset[0], (a[1]+z[1])/2 + offset[1]}
				p := crystal45Path(a, mid)
				tail := crystal45Path(mid, z)
				add(append(p, tail[1:]...))
			}
		}
	}
	return out
}

// pcbEscapeForcedContinuationSuffixes keeps a forced waypoint on a straight or
// 45-degree continuation before the path turns around nearby pad copper. This
// is needed when the shortest departure from the waypoint is blocked and a
// generic corner bevel would otherwise erase the waypoint from the proof.
func pcbEscapeForcedContinuationSuffixes(prefix [][2]float64, entry, target [2]float64, in pcbRoutingIntent, clear func([2]float64, [2]float64) bool, budget *pcbEscapeBudget) [][][2]float64 {
	if len(prefix) < 2 {
		return nil
	}
	h, ok := crystal45Heading(prefix[len(prefix)-2], entry)
	if !ok {
		return nil
	}
	dirs := [][2]float64{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
	var out [][][2]float64
	for _, delta := range []int{0, -1, 1} {
		if !budget.take() {
			break
		}
		dir := dirs[(h+delta+8)%8]
		lead := [2]float64{entry[0] + dir[0]*in.StepMil, entry[1] + dir[1]*in.StepMil}
		if !clear(entry, lead) {
			continue
		}
		tails := pcbEscape45PathVariants(lead, target)
		tails = append(tails, pcbEscapeGrid(lead, target, in.StepMil, in.MaxDetourMil, clear, budget))
		for _, tail := range tails {
			if len(tail) < 2 {
				continue
			}
			points := [][2]float64{entry, lead}
			points = append(points, tail[1:]...)
			out = append(out, points)
		}
	}
	return out
}

// pcbEscape45PathVariants returns the shortest straight/45-degree paths while
// varying whether the diagonal portion occurs before or after the orthogonal
// portion. The balanced variant remains first for stable existing output.
func pcbEscape45PathVariants(from, to [2]float64) [][][2]float64 {
	variants := [][][2]float64{crystal45Path(from, to)}
	dx, dy := to[0]-from[0], to[1]-from[1]
	ax, ay := math.Abs(dx), math.Abs(dy)
	if ax <= netPathGeomEps || ay <= netPathGeomEps || math.Abs(ax-ay) <= netPathGeomEps {
		return variants
	}
	sx, sy := math.Copysign(1, dx), math.Copysign(1, dy)
	if ax > ay {
		variants = append(variants,
			[][2]float64{from, {to[0] - sx*ay, from[1]}, to},
			[][2]float64{from, {from[0] + sx*ay, to[1]}, to},
		)
	} else {
		variants = append(variants,
			[][2]float64{from, {from[0], to[1] - sy*ax}, to},
			[][2]float64{from, {to[0], from[1] + sy*ax}, to},
		)
	}
	return variants
}

func pcbEscapeRouteContainsPoint(points [][2]float64, point [2]float64) bool {
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		if segPtDist(point[0], point[1], a[0], a[1], b[0], b[1]) <= netPathGeomEps {
			return true
		}
	}
	return false
}

func pcbEscapePath45(points [][2]float64) bool {
	if len(points) < 2 {
		return false
	}
	for i, p := range points {
		if !allFinite(p[0], p[1]) {
			return false
		}
		if i == 0 {
			continue
		}
		a := points[i-1]
		dx, dy := p[0]-a[0], p[1]-a[1]
		if math.Hypot(dx, dy) < 1e-7 || math.Abs(dx) > 1e-6 && math.Abs(dy) > 1e-6 && math.Abs(math.Abs(dx)-math.Abs(dy)) > 1e-6 {
			return false
		}
		if i > 1 {
			prev := points[i-2]
			ux, uy := a[0]-prev[0], a[1]-prev[1]
			if (ux*dx+uy*dy)/(math.Hypot(ux, uy)*math.Hypot(dx, dy)) < math.Sqrt(.5)-1e-6 {
				return false
			}
		}
	}
	return true
}

func planPCBEscape(in pcbRoutingIntent, snap *boardSnapshot) (pcbEscapeReport, error) {
	rep := pcbEscapeReport{SchemaVersion: 1, Status: "incomplete", IntentSHA256: pcbEscapeIntentHash(in), Budget: pcbEscapeBudget{MaxStates: in.MaxStates}, Routes: []pcbModuleRoute{}, Vias: []pcbModuleVia{}, Limitations: []string{"Reservations prove declared local/full pad paths only; they are not editor copper or whole-net completion.", "Bounded TOP/BOTTOM/TOP transitions require a measured two-layer board and two spare whole-net vias; other transitions remain incomplete."}}
	rep.BoardSemanticSHA256, _ = moduleCheckSemanticSHA256(snap)
	ctx, status, err := preparePCBEscape(in, snap)
	if err != nil {
		rep.Status = status
		rep.Reasons = []string{err.Error()}
		return rep, err
	}
	var demands []pcbEscapeDemand
	var fixedWitnesses []pcbEscapeWitness
	rep.ExistingViaCounts = map[string]int{}
	for _, d := range in.Demands {
		if !d.NC {
			rep.ExistingViaCounts[d.Net] = pcbEscapeViaCount(ctx, d.Net, nil)
			if d.ExistingViasOnly {
				fixedWitnesses = append(fixedWitnesses, pcbEscapeWitness{DemandID: d.ID, ExistingViaIDs: append([]string(nil), d.ExistingViaIDs...)})
				continue
			}
			demands = append(demands, d)
		}
	}
	visited := map[string]bool{}
	var search func([]pcbModuleRoute, []pcbModuleVia, []pcbEscapeWitness, map[string]bool) bool
	search = func(chosen []pcbModuleRoute, chosenVias []pcbModuleVia, witnesses []pcbEscapeWitness, done map[string]bool) bool {
		if !rep.Budget.take() {
			return false
		}
		if len(done) == len(demands) {
			rep.Routes = append([]pcbModuleRoute{}, chosen...)
			rep.Vias = append([]pcbModuleVia{}, chosenVias...)
			rep.Witnesses = append(append([]pcbEscapeWitness{}, fixedWitnesses...), witnesses...)
			return true
		}
		keys := make([]string, 0, len(chosen))
		for _, r := range chosen {
			b, _ := json.Marshal(r)
			keys = append(keys, string(b))
		}
		sort.Strings(keys)
		key := strings.Join(keys, "|")
		if visited[key] {
			return false
		}
		visited[key] = true
		// Every unfinished demand must eventually coexist with the same chosen
		// routes. Adding more copper can never create a path that is currently
		// absent, so a zero-choice demand kills this state immediately. Select
		// the demand with the fewest current choices (MRV) and branch only over
		// its geometry. The previous loop also branched over every permutation
		// of demand order; a 33-pad MCU could exhaust millions of states on
		// equivalent orders before reaching the actually blocked pad.
		type nextDemand struct {
			direct []pcbModuleRoute
			vias   []pcbEscapeChoice
			demand pcbEscapeDemand
		}
		var next *nextDemand
		// Resolve forced full routes before local corridor reservations. They are
		// the longest and most expensive constraints, and repeatedly rebuilding
		// them at every local-fanout depth can consume the shared budget without
		// exploring a new joint state. Backtracking still tries their alternative
		// geometries if later local reservations cannot coexist.
		prioritizeFull := false
		for _, d := range demands {
			if !done[d.ID] && d.To != "" && len(d.Exits) > 0 {
				prioritizeFull = true
				break
			}
		}
		for _, d := range demands {
			if done[d.ID] {
				continue
			}
			if prioritizeFull && (d.To == "" || len(d.Exits) == 0) {
				continue
			}
			direct := pcbEscapeCandidates(ctx, in, d, chosen, &rep.Budget, chosenVias...)
			var viaChoices []pcbEscapeChoice
			if len(direct) == 0 && !rep.Budget.Exhausted {
				viaChoices = pcbEscapeViaChoices(ctx, in, d, chosen, chosenVias, &rep.Budget)
			}
			if rep.Budget.Exhausted {
				return false
			}
			if len(direct)+len(viaChoices) == 0 {
				return false
			}
			if next == nil || len(direct)+len(viaChoices) < len(next.direct)+len(next.vias) {
				next = &nextDemand{direct: direct, vias: viaChoices, demand: d}
			}
		}
		if next == nil {
			return false
		}
		for _, route := range next.direct {
			done[next.demand.ID] = true
			w := pcbEscapeWitness{DemandID: next.demand.ID, RouteIDs: []string{route.ID}}
			if search(append(chosen, route), chosenVias, append(witnesses, w), done) {
				return true
			}
			delete(done, next.demand.ID)
			if rep.Budget.Exhausted {
				return false
			}
		}
		// Layer transitions are much more expensive to enumerate. Generate them
		// lazily only after all direct witnesses for the selected demand fail;
		// eager generation used to consume the whole board budget even when six
		// parallel straight QFN fanouts were already a complete solution.
		if len(next.direct) > 0 {
			next.vias = pcbEscapeViaChoices(ctx, in, next.demand, chosen, chosenVias, &rep.Budget)
			if rep.Budget.Exhausted {
				return false
			}
		}
		for _, choice := range next.vias {
			done[next.demand.ID] = true
			if search(append(chosen, choice.routes...), append(chosenVias, choice.vias...), append(witnesses, choice.witness), done) {
				return true
			}
			delete(done, next.demand.ID)
			if rep.Budget.Exhausted {
				return false
			}
		}
		return false
	}
	if !search(nil, nil, nil, map[string]bool{}) {
		reason := "finite route/corridor candidate search exhausted; this is not proof that the board is unroutable"
		if rep.Budget.Exhausted {
			reason = "joint escape search state budget exhausted"
		}
		rep.Reasons = []string{reason}
		return rep, fmt.Errorf("%s", reason)
	}
	sort.Slice(rep.Routes, func(i, j int) bool { return rep.Routes[i].ID < rep.Routes[j].ID })
	rep.Status = "pass"
	if err := verifyPCBEscape(in, rep, snap); err != nil {
		rep.Status = "incomplete"
		rep.Reasons = []string{err.Error()}
		return rep, err
	}
	return rep, nil
}

func verifyPCBEscape(in pcbRoutingIntent, rep pcbEscapeReport, snap *boardSnapshot) error {
	semantic, err := moduleCheckSemanticSHA256(snap)
	if err != nil {
		return err
	}
	if rep.Status != "pass" || rep.BoardSemanticSHA256 != semantic || rep.IntentSHA256 != pcbEscapeIntentHash(in) {
		return fmt.Errorf("escape proof is not pass or has stale board/intent hash")
	}
	return verifyPCBEscapeGeometry(in, rep, snap)
}

// After a journalled write, new primitive IDs legitimately change the snapshot
// hash. The verifier still binds independent intent and checks the exact paths
// against fresh geometry, never relabels a stale report as having a new hash.
func verifyPCBEscapeGeometry(in pcbRoutingIntent, rep pcbEscapeReport, snap *boardSnapshot) error {
	ctx, _, err := preparePCBEscape(in, snap)
	if err != nil {
		return err
	}
	if rep.Status != "pass" || rep.IntentSHA256 != pcbEscapeIntentHash(in) {
		return fmt.Errorf("escape proof status/intent mismatch")
	}
	demands := map[string]pcbEscapeDemand{}
	for _, d := range in.Demands {
		if !d.NC {
			demands[d.ID] = d
		}
	}
	if len(rep.Witnesses) != len(demands) {
		return fmt.Errorf("escape witnesses do not cover every routed pad")
	}
	routes := map[string]pcbModuleRoute{}
	vias := map[string]pcbModuleVia{}
	for _, r := range rep.Routes {
		if r.ID == "" {
			return fmt.Errorf("empty route id")
		}
		if _, ok := routes[r.ID]; ok {
			return fmt.Errorf("duplicate route id %s", r.ID)
		}
		routes[r.ID] = r
	}
	for _, v := range rep.Vias {
		if v.ID == "" {
			return fmt.Errorf("empty via id")
		}
		if _, ok := vias[v.ID]; ok {
			return fmt.Errorf("duplicate via id %s", v.ID)
		}
		vias[v.ID] = v
	}
	seenDemand, seenRoute, seenVia := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, w := range rep.Witnesses {
		d, ok := demands[w.DemandID]
		if !ok || seenDemand[w.DemandID] {
			return fmt.Errorf("unknown/duplicate demand witness %s", w.DemandID)
		}
		seenDemand[w.DemandID] = true
		if d.ExistingViasOnly {
			if len(w.RouteIDs) != 0 || len(w.ViaIDs) != 0 || len(w.ExistingViaIDs) != len(d.ExistingViaIDs) {
				return fmt.Errorf("existing-vias-only witness inventory mismatch %s", d.ID)
			}
			seenExisting := map[string]bool{}
			for _, id := range w.ExistingViaIDs {
				if seenExisting[id] || !containsString(d.ExistingViaIDs, id) {
					return fmt.Errorf("existing-vias-only witness contains an unauthorized via: %s", id)
				}
				seenExisting[id] = true
				if _, err := ctx.existingGroundVia(d, id); err != nil {
					return err
				}
				if _, recreated := vias[id]; recreated {
					return fmt.Errorf("existing via must not be recreated: %s", id)
				}
			}
			continue
		}
		if len(w.ViaIDs) != 0 && len(w.ViaIDs) != 2 {
			return fmt.Errorf("unsupported escape transition witness %s", d.ID)
		}
		if len(w.ViaIDs) == 0 && len(w.RouteIDs) != 1 || len(w.ViaIDs) == 2 && len(w.RouteIDs) != 3 {
			return fmt.Errorf("escape path segment inventory mismatch %s", d.ID)
		}
		if len(w.ExistingViaIDs) > 1 {
			return fmt.Errorf("unsupported existing-via transition inventory for %s", d.ID)
		}
		for _, id := range w.ExistingViaIDs {
			if len(w.ViaIDs) != 2 || id != w.ViaIDs[0] || !containsString(d.ExistingViaIDs, id) {
				return fmt.Errorf("existing via is not an authorized ground entry: %s", id)
			}
		}
		if len(w.ViaIDs)-len(w.ExistingViaIDs) > d.MaxVias || pcbEscapeDemandViaCount(ctx, d, rep.Vias) > d.MaxVias {
			return fmt.Errorf("whole-net maxVias exceeded %s", d.Net)
		}
		var parts []pcbModuleRoute
		for i, id := range w.RouteIDs {
			r, ok := routes[id]
			if !ok || seenRoute[id] || r.Net != d.Net || r.WidthMil != d.WidthMil || !pcbEscapePath45(r.Points) {
				return fmt.Errorf("escape route contract/geometry mismatch: %s", id)
			}
			seenRoute[id] = true
			expectedLayer := d.Layer
			if len(w.ViaIDs) == 2 {
				if d.Layer != 1 || ctx.snap.CopperLayers != 2 {
					return fmt.Errorf("two-via escape requires TOP on a two-layer board")
				}
				if i == 1 {
					expectedLayer = 2
				}
			}
			if r.Layer != expectedLayer {
				return fmt.Errorf("escape layer mismatch %s", id)
			}
			if i == 0 {
				if r.From != d.From {
					return fmt.Errorf("escape source identity mismatch %s", id)
				}
			} else if r.From != "" {
				return fmt.Errorf("unexpected intermediate source %s", id)
			}
			if i == len(w.RouteIDs)-1 {
				if r.To != d.To {
					return fmt.Errorf("escape target identity mismatch %s", id)
				}
			} else if r.To != "" {
				return fmt.Errorf("unexpected intermediate target %s", id)
			}
			layerDemand := d
			layerDemand.Layer = r.Layer
			clear, e := ctx.clear(layerDemand, rep.Routes, rep.Vias...)
			if e != nil {
				return e
			}
			for j := 1; j < len(r.Points); j++ {
				if !clear(r.Points[j-1], r.Points[j]) {
					return fmt.Errorf("escape clearance/board/region conflict: %s segment %d", id, j)
				}
			}
			parts = append(parts, r)
		}
		for i, id := range w.ViaIDs {
			var v pcbModuleVia
			if containsString(w.ExistingViaIDs, id) {
				var err error
				v, err = ctx.existingGroundVia(d, id)
				if err != nil {
					return err
				}
				if i != 0 || !pcbEscapeEntryInsideMeasuredPad(ctx, d, parts[0].Points) {
					return fmt.Errorf("existing via entry does not follow measured source pad copper %s", id)
				}
				if _, newAlso := vias[id]; newAlso {
					return fmt.Errorf("existing via must not be recreated: %s", id)
				}
			} else {
				var ok bool
				v, ok = vias[id]
				if !ok || seenVia[id] || v.Net != d.Net {
					return fmt.Errorf("escape via inventory mismatch %s", id)
				}
				seenVia[id] = true
				if v.HoleMil != ctx.snap.Rules.ViaDrillMil || v.DiameterMil != ctx.snap.Rules.ViaDiameterMil || !ctx.viaClear(v, rep.Routes, rep.Vias) {
					return fmt.Errorf("escape via geometry/clearance conflict %s", id)
				}
			}
			a, b := parts[i].Points[len(parts[i].Points)-1], parts[i+1].Points[0]
			if math.Hypot(a[0]-v.X, a[1]-v.Y) > netPathGeomEps || math.Hypot(b[0]-v.X, b[1]-v.Y) > netPathGeomEps {
				return fmt.Errorf("escape via does not join consecutive layers: %s", id)
			}
		}
		first := parts[0].Points[0]
		lastPart := parts[len(parts)-1]
		last := lastPart.Points[len(lastPart.Points)-1]
		from := ctx.pads[d.From]
		if math.Hypot(first[0]-from.X, first[1]-from.Y) > netPathGeomEps {
			return fmt.Errorf("escape route source mismatch: %s", d.ID)
		}
		if d.To != "" {
			p := ctx.pads[d.To]
			if math.Hypot(last[0]-p.X, last[1]-p.Y) > netPathGeomEps {
				return fmt.Errorf("escape destination mismatch: %s", d.ID)
			}
			if len(d.Exits) > 0 {
				forced := false
				for _, exit := range d.Exits {
					for _, part := range parts {
						forced = forced || pcbEscapeRouteContainsPoint(part.Points, exit)
					}
				}
				if !forced {
					return fmt.Errorf("escape path skips every forced breakout exit: %s", d.ID)
				}
			}
		} else {
			found := false
			for _, p := range d.Exits {
				if math.Hypot(last[0]-p[0], last[1]-p[1]) <= netPathGeomEps {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("undeclared escape exit: %s", d.ID)
			}
			to := ctx.pads[d.Toward]
			prev := lastPart.Points[len(lastPart.Points)-2]
			if d.MaxAwayMil == 0 && (last[0]-prev[0])*(to.X-last[0])+(last[1]-prev[1])*(to.Y-last[1]) < -netPathGeomEps {
				return fmt.Errorf("escape exit heads away from target: %s", d.ID)
			}
		}
	}
	if len(seenRoute) != len(routes) || len(seenVia) != len(vias) {
		return fmt.Errorf("escape report has undeclared route/via objects")
	}
	return nil
}
