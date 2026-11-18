package app

// Routability is a read-only, pre-routing local escape check. For each netted
// SMD pad it floods the pad's own copper layer with a minimum-width track and
// asks two questions using the same exact geometry/rule kernel as escape-plan:
// can a track leave the local neighbourhood, and can a legal through via be
// reached? A pad with neither is trapped; the report names the copper that
// bounds its reachable region. This is a local necessary condition only — it
// does not reserve shared channels or prove whole-net routing.
import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const (
	pcbRoutabilityDefaultStepMil   = 2.0
	pcbRoutabilityDefaultRadiusMil = 100.0
	// Legal via sites are probed in flood order (nearest track path first) and
	// counting stops here; pass/fail only needs one.
	pcbRoutabilityViaSiteCap = 32
)

type pcbRoutabilityOptions struct {
	StepMil    float64
	RadiusMil  float64
	Components []string // optional designator filter; empty = every component
}

type pcbRoutabilityParameters struct {
	StepMil        float64 `json:"stepMil"`
	RadiusMil      float64 `json:"radiusMil"`
	TrackWidthMil  float64 `json:"trackWidthMil"`
	ClearanceMil   float64 `json:"clearanceMil"`
	ViaDiameterMil float64 `json:"viaDiameterMil"`
	ViaDrillMil    float64 `json:"viaDrillMil"`
}

type pcbRoutabilitySummary struct {
	Checked     int `json:"checked"`
	Passed      int `json:"passed"`
	Trapped     int `json:"trapped"`
	ThroughHole int `json:"throughHole"`
	NoNet       int `json:"noNet"`
	// Unverified pads have no proven exit, only a same-net transition whose
	// layer span cannot be proven from the capture.
	Unverified int `json:"unverified"`
}

type pcbRoutabilityBlocker struct {
	Kind  string `json:"kind"` // pad | track | arc | via | copper | slot | keepout | board-edge
	ID    string `json:"id,omitempty"`
	Ref   string `json:"ref,omitempty"` // designator.pad for pads
	Net   string `json:"net,omitempty"`
	Layer int    `json:"layer,omitempty"`
}

type pcbRoutabilityPad struct {
	Pad               string      `json:"pad"`
	Net               string      `json:"net"`
	Layer             int         `json:"layer"`
	X                 float64     `json:"x"`
	Y                 float64     `json:"y"`
	Status            string      `json:"status"` // pass | trapped
	ViaSites          int         `json:"viaSites"`
	ViaSitesCapped    bool        `json:"viaSitesCapped,omitempty"`
	NearestViaSite    *[2]float64 `json:"nearestViaSite,omitempty"`
	NearestViaDistMil float64     `json:"nearestViaDistanceMil,omitempty"`
	EscapeDirections  []string    `json:"escapeDirections"`
	ReachableAreaMil2 float64     `json:"reachableAreaMil2"`
	// ExistingTransitions are same-net vias / through-hole pads the track flood
	// reaches; each is layer-change evidence without a new via.
	ExistingTransitions []pcbRoutabilityBlocker `json:"existingTransitions,omitempty"`
	// UnverifiedTransitions are reachable same-net objects without proof that
	// they are plated / span the source layer; they never make a pad pass.
	UnverifiedTransitions []pcbRoutabilityTransition `json:"unverifiedTransitions,omitempty"`
	Blockers              []pcbRoutabilityBlocker    `json:"blockers,omitempty"`
}

type pcbRoutabilityTransition struct {
	pcbRoutabilityBlocker
	Reason string `json:"reason"`
}

type pcbRoutabilityReport struct {
	SchemaVersion       int                      `json:"schemaVersion"`
	Status              string                   `json:"status"` // pass | fail | incomplete
	Reason              string                   `json:"reason,omitempty"`
	BoardSemanticSHA256 string                   `json:"boardSemanticSha256,omitempty"`
	BoardSHA256         string                   `json:"boardSha256,omitempty"`
	Parameters          pcbRoutabilityParameters `json:"parameters"`
	Summary             pcbRoutabilitySummary    `json:"summary"`
	Trapped             []string                 `json:"trapped"`
	Pads                []pcbRoutabilityPad      `json:"pads"`
	Limitations         []string                 `json:"limitations"`
}

var pcbRoutabilityDirections = []struct {
	name   string
	di, dj int
}{{"+X", 1, 0}, {"+X+Y", 1, 1}, {"+Y", 0, 1}, {"-X+Y", -1, 1}, {"-X", -1, 0}, {"-X-Y", -1, -1}, {"-Y", 0, -1}, {"+X-Y", 1, -1}}

func analyzePCBRoutability(snap *boardSnapshot, opt pcbRoutabilityOptions) (pcbRoutabilityReport, error) {
	rep := pcbRoutabilityReport{SchemaVersion: 1, Status: "incomplete", Trapped: []string{}, Pads: []pcbRoutabilityPad{}, Parameters: pcbRoutabilityParameters{StepMil: opt.StepMil, RadiusMil: opt.RadiusMil}, Limitations: []string{
		"Local necessary condition per pad: neighbouring pads may share one channel; a pass does not reserve capacity or prove whole-net routing.",
		"Via sites are counted up to 32 in flood order (nearest track path first); viaSitesCapped marks more.", "Track flood uses the minimum track width on the pad's own copper layer at a fixed grid; via sites are sampled on the same grid, so a fail means no witness at this resolution.",
		"Via sites are checked against every pad, every via and the copper/keepouts on every copper layer that carries any; negative plane-layer copper (automatic anti-pads) is not modelled.",
		"Escape radius is measured from the source pad's copper edge.",
		"A reachable same-net via proves a layer change only on a measured two-layer board (via readback has no type or layer span); a reachable same-net through-hole pad needs a captured drill and metallization=true. Otherwise it is reported as unverified and, if it is the pad's only way out, the check is incomplete.",
		"Board slots (MULTI-layer cutouts) keep max(8 mil, clearance, copper-to-edge) from copper and vias; stroked pour/fill outlines add half their stroke width.",
		"Through-hole pads already reach every layer and are not flooded.",
	}}
	rep.BoardSemanticSHA256, _ = moduleCheckSemanticSHA256(snap)
	incomplete := func(msg string) (pcbRoutabilityReport, error) {
		rep.Status, rep.Reason = "incomplete", msg
		return rep, fmt.Errorf("%s", msg)
	}
	if !allFinite(opt.StepMil, opt.RadiusMil) || opt.StepMil < 0.5 || opt.StepMil > 10 || opt.RadiusMil < 4*opt.StepMil || opt.RadiusMil/opt.StepMil > 400 {
		return incomplete("routability requires 0.5 <= step <= 10 mil and 4*step <= radius <= 400*step")
	}
	// Every component and pad must be captured: a missing pad list would
	// silently remove obstacles, and a component filter does not narrow that.
	if coverage := snap.geometryCoverage(false); !coverage.Complete {
		return incomplete("board geometry capture is incomplete: " + strings.Join(coverage.Limitations, "; "))
	}
	// Fill and poured copper come only from pcbBoardCopperExtras, which keeps
	// stroke widths and open/closed unfilled paths; the polygon-only area
	// parser would turn a hollow outline into a solid block.
	ctx, _, err := preparePCBRouteGeometryAreas(snap, false)
	if err != nil {
		return incomplete(err.Error())
	}
	extras, err := pcbBoardCopperExtras(snap, polygonSourceContours, true)
	if err != nil {
		return incomplete(err.Error())
	}
	ctx.slots, ctx.strokes = extras.slots, extras.strokes
	captured := map[string]boardPad{}
	for _, c := range snap.Components {
		for _, p := range c.Pads {
			captured[pcbRoutabilityPadKey(c.Designator, p.Number, p.ID)] = p
		}
	}
	r := snap.Rules
	// "live" only says some rule field was read; defaults for the fields used
	// here would make the check pass on invented numbers.
	var unmeasured []string
	for _, field := range []string{pcbRuleFieldClearance, pcbRuleFieldClearanceTrackTrack, pcbRuleFieldTrackWidthMin, pcbRuleFieldViaDrill, pcbRuleFieldViaDiameter, pcbRuleFieldCopperToEdge} {
		if !containsString(r.Measured, field) {
			unmeasured = append(unmeasured, field)
		}
	}
	if len(unmeasured) > 0 {
		return incomplete("routability requires rules measured from the board, missing: " + strings.Join(unmeasured, ", ") + " (re-capture with a current pcb dump)")
	}
	if !allFinite(r.ViaDrillMil, r.ViaDiameterMil) || r.ViaDrillMil <= 0 || r.ViaDiameterMil <= r.ViaDrillMil {
		return incomplete("routability requires valid via drill/diameter rules")
	}
	rep.Parameters.TrackWidthMil, rep.Parameters.ClearanceMil = r.TrackWidthMinMil, r.ClearanceMil
	rep.Parameters.ViaDiameterMil, rep.Parameters.ViaDrillMil = r.ViaDiameterMil, r.ViaDrillMil
	filter := map[string]bool{}
	for _, c := range opt.Components {
		filter[c] = true
	}
	found := map[string]bool{}
	var pads []pcbPadP
	for _, pad := range ctx.allPads {
		if len(filter) > 0 && !filter[pad.Designator] {
			continue
		}
		found[pad.Designator] = true
		switch {
		case strings.TrimSpace(pad.Net) == "":
			rep.Summary.NoNet++
			continue
		case pad.Layer == pcbLayerMulti:
			rep.Summary.ThroughHole++
			continue
		case pad.Layer != 1 && pad.Layer != 2:
			return incomplete(fmt.Sprintf("pad %s.%s is on unsupported layer %d", pad.Designator, pad.Number, pad.Layer))
		}
		pads = append(pads, pad)
	}
	// Pads are independent and the geometry context is read-only, so each
	// worker writes only its own result slot.
	var unverified []string
	results := make([]pcbRoutabilityPad, len(pads))
	errs := make([]error, len(pads))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i], errs[i] = ctx.routabilityPad(pads[i], opt.StepMil, opt.RadiusMil, captured)
			}
		}()
	}
	for i := range pads {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for i, result := range results {
		if errs[i] != nil {
			return incomplete(errs[i].Error())
		}
		rep.Pads = append(rep.Pads, result)
		rep.Summary.Checked++
		switch result.Status {
		case "trapped":
			rep.Summary.Trapped++
			rep.Trapped = append(rep.Trapped, result.Pad)
		case "unverified":
			rep.Summary.Unverified++
			unverified = append(unverified, result.Pad)
		default:
			rep.Summary.Passed++
		}
	}
	for c := range filter {
		if !found[c] {
			return incomplete("component has no measured pads: " + c)
		}
	}
	sort.Strings(rep.Trapped)
	sort.SliceStable(rep.Pads, func(i, j int) bool { return rep.Pads[i].Pad < rep.Pads[j].Pad })
	if rep.Summary.Trapped > 0 {
		rep.Status = "fail"
		rep.Reason = fmt.Sprintf("%d pad(s) have neither a legal via site nor a track escape: %s", rep.Summary.Trapped, strings.Join(rep.Trapped, ", "))
		return rep, fmt.Errorf("%s", rep.Reason)
	}
	if len(unverified) > 0 {
		sort.Strings(unverified)
		return incomplete(fmt.Sprintf("%d pad(s) escape only through a same-net transition whose layer span is unproven: %s", len(unverified), strings.Join(unverified, ", ")))
	}
	rep.Status = "pass"
	return rep, nil
}

// localize keeps only copper that can interact with a flood of the given
// radius around center. Pure performance: every dropped object is farther
// than any probed track or via could reach, so results are unchanged.
func (ctx *pcbEscapeContext) localize(cx, cy, reach float64) *pcbEscapeContext {
	near := func(d float64) bool { return d <= reach }
	out := &pcbEscapeContext{snap: ctx.snap, pads: ctx.pads, ambiguous: ctx.ambiguous}
	for _, p := range ctx.allPads {
		if near(math.Hypot(p.X-cx, p.Y-cy) - math.Max(math.Hypot(p.W, p.H), math.Hypot(p.ShapeW, p.ShapeH))/2) {
			out.allPads = append(out.allPads, p)
		}
	}
	for _, t := range ctx.tracks {
		if near(segPtDist(cx, cy, t.X1, t.Y1, t.X2, t.Y2) - t.Width/2) {
			out.tracks = append(out.tracks, t)
		}
	}
	for _, a := range ctx.arcs {
		// An arc stays within its chord length of either endpoint (|sweep| <= 360).
		chord := math.Hypot(a.X2-a.X1, a.Y2-a.Y1)
		if math.Abs(a.ArcAngle) > 180 || near(segPtDist(cx, cy, a.X1, a.Y1, a.X2, a.Y2)-chord-a.Width/2) {
			out.arcs = append(out.arcs, a)
		}
	}
	for _, v := range ctx.vias {
		if near(math.Hypot(v.X-cx, v.Y-cy) - v.Dia/2) {
			out.vias = append(out.vias, v)
		}
	}
	c := [2]float64{cx, cy}
	for _, a := range ctx.areas {
		if near(copperAreaSegmentDistance(a, c, c)) {
			out.areas = append(out.areas, a)
		}
	}
	for _, b := range ctx.bans {
		if near(copperAreaSegmentDistance(b, c, c)) {
			out.bans = append(out.bans, b)
		}
	}
	for _, slot := range ctx.slots {
		if near(copperAreaSegmentDistance(slot, c, c)) {
			out.slots = append(out.slots, slot)
		}
	}
	for _, st := range ctx.strokes {
		if near(st.segmentDistance(c, c)) {
			out.strokes = append(out.strokes, st)
		}
	}
	return out
}

// pcbRoutabilityPlatedDrillIssue explains why a captured pad drill does not
// prove a plated through connection ("" = proven). The connector returns the
// SDK tuple [kind, width, height] in 10 mil units; only shapes with a known
// meaning are accepted, both sizes must be finite and positive, and the drill
// must leave copper around it (smaller than the measured pad on both axes).
func pcbRoutabilityPlatedDrillIssue(raw any, pad pcbPadP) string {
	t, ok := raw.([]any)
	if !ok || len(t) != 3 {
		return "drill not captured as [kind, width, height]"
	}
	kind, _ := t[0].(string)
	if kind != "ROUND" && kind != "SLOT" {
		return fmt.Sprintf("drill kind %v is not a supported plated-hole shape", t[0])
	}
	w, wok := asFloatOK(t[1])
	h, hok := asFloatOK(t[2])
	if !wok || !hok || !allFinite(w, h) || w <= 0 || h <= 0 {
		return "drill size is missing, zero or not finite"
	}
	wMil, hMil := w*10, h*10
	if wMil >= pad.ShapeW-netPathGeomEps || hMil >= pad.ShapeH-netPathGeomEps {
		return fmt.Sprintf("drill %.2fx%.2f mil leaves no copper on the %.2fx%.2f mil pad", wMil, hMil, pad.ShapeW, pad.ShapeH)
	}
	return ""
}

func pcbRoutabilityPadKey(designator, number, id string) string {
	return designator + "." + number + "|" + id
}

func (ctx *pcbEscapeContext) routabilityPad(pad pcbPadP, step, radius float64, captured map[string]boardPad) (pcbRoutabilityPad, error) {
	rules := ctx.snap.Rules
	name := pad.Designator + "." + pad.Number
	out := pcbRoutabilityPad{Pad: name, Net: pad.Net, Layer: pad.Layer, X: pad.X, Y: pad.Y, EscapeDirections: []string{}}
	// The escape radius is measured from the pad's copper edge: wandering
	// across a large source pad is not an exit.
	halfDiag := math.Max(math.Hypot(pad.W, pad.H), math.Hypot(pad.ShapeW, pad.ShapeH)) / 2
	margin := math.Max(rules.ViaDiameterMil, rules.TrackWidthMinMil)/2 + math.Max(rules.ClearanceMil, rules.ClearanceTrackTrackMil) + pcbSlotClearanceMil(rules) + rules.CopperToEdgeMil + step
	local := ctx.localize(pad.X, pad.Y, halfDiag+radius+margin)
	demand := pcbEscapeDemand{Net: pad.Net, Layer: pad.Layer, WidthMil: rules.TrackWidthMinMil}
	track, err := local.clear(demand, nil)
	if err != nil {
		return out, fmt.Errorf("pad %s clearance model: %w", name, err)
	}
	via, err := local.viaSiteChecker(pad.Net, rules.ViaDiameterMil, nil, nil)
	if err != nil {
		return out, fmt.Errorf("pad %s via model: %w", name, err)
	}
	n := int(math.Ceil((halfDiag + radius) / step))
	point := func(i, j int) [2]float64 { return [2]float64{pad.X + float64(i)*step, pad.Y + float64(j)*step} }
	type node struct{ i, j int }
	gaps := map[node]float64{}
	gap := func(k node) float64 {
		if g, ok := gaps[k]; ok {
			return g
		}
		p := point(k.i, k.j)
		g, err := pcbExactPadSegmentGap(pad, p, p)
		if err != nil {
			g = math.Inf(1)
		}
		gaps[k] = g
		return g
	}
	inside := func(k node) bool {
		return k.i >= -n && k.i <= n && k.j >= -n && k.j <= n && gap(k) <= radius+netPathGeomEps
	}
	seen := map[node]bool{{0, 0}: true}
	queue := []node{{0, 0}}
	var reachable []node
	exits := map[string]bool{}
	if track(point(0, 0), point(0, 0)) {
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			reachable = append(reachable, cur)
			if gap(cur) > radius-step {
				exits[pcbRoutabilityOctant(cur.i, cur.j)] = true
			}
			for _, d := range pcbRoutabilityDirections {
				next := node{cur.i + d.di, cur.j + d.dj}
				if seen[next] || !inside(next) || !track(point(cur.i, cur.j), point(next.i, next.j)) {
					continue
				}
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	out.ReachableAreaMil2 = float64(len(reachable)) * step * step
	for _, d := range pcbRoutabilityDirections {
		if exits[d.name] {
			out.EscapeDirections = append(out.EscapeDirections, d.name)
		}
	}
	// Existing same-net through copper the flood reaches is a layer change
	// only with proof that it is plated and spans the source layer.
	touches := func(within func([2]float64) bool) bool {
		for _, cur := range reachable {
			if within(point(cur.i, cur.j)) {
				return true
			}
		}
		return false
	}
	for _, v := range local.vias {
		if v.Net != pad.Net || !touches(func(p [2]float64) bool { return math.Hypot(p[0]-v.X, p[1]-v.Y) <= v.Dia/2+netPathGeomEps }) {
			continue
		}
		t := pcbRoutabilityBlocker{Kind: "via", ID: v.ID, Net: v.Net}
		if ctx.snap.CopperLayers == 2 {
			out.ExistingTransitions = append(out.ExistingTransitions, t)
		} else {
			out.UnverifiedTransitions = append(out.UnverifiedTransitions, pcbRoutabilityTransition{t, fmt.Sprintf("via type/layer span not captured on a %d-copper-layer board", ctx.snap.CopperLayers)})
		}
	}
	for _, other := range local.allPads {
		if other.Net != pad.Net || other.Layer != pcbLayerMulti {
			continue
		}
		if !touches(func(p [2]float64) bool {
			g, err := pcbExactPadSegmentGap(other, p, p)
			return err == nil && g <= netPathGeomEps
		}) {
			continue
		}
		t := pcbRoutabilityBlocker{Kind: "pad", ID: other.ID, Ref: other.Designator + "." + other.Number, Net: other.Net, Layer: other.Layer}
		raw, known := captured[pcbRoutabilityPadKey(other.Designator, other.Number, other.ID)]
		switch {
		case known && raw.Metallization != nil && !*raw.Metallization:
			// A non-plated hole connects nothing; it is not a transition.
		case !known || raw.Metallization == nil:
			out.UnverifiedTransitions = append(out.UnverifiedTransitions, pcbRoutabilityTransition{t, "metallization not captured"})
		default:
			if reason := pcbRoutabilityPlatedDrillIssue(raw.Hole, other); reason != "" {
				out.UnverifiedTransitions = append(out.UnverifiedTransitions, pcbRoutabilityTransition{t, reason})
			} else {
				out.ExistingTransitions = append(out.ExistingTransitions, t)
			}
		}
	}
	// Probe via candidates nearest first (Euclidean from the pad centre), so
	// the cap never hides a closer legal site.
	candidates := append([]node(nil), reachable...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].i*candidates[i].i+candidates[i].j*candidates[i].j < candidates[j].i*candidates[j].i+candidates[j].j*candidates[j].j
	})
	for _, cur := range candidates {
		p := point(cur.i, cur.j)
		if !via(p[0], p[1]) {
			continue
		}
		if out.ViaSites == pcbRoutabilityViaSiteCap {
			out.ViaSitesCapped = true
			break
		}
		out.ViaSites++
		if out.NearestViaSite == nil {
			site := p
			out.NearestViaSite, out.NearestViaDistMil = &site, math.Hypot(p[0]-pad.X, p[1]-pad.Y)
		}
	}
	if out.ViaSites > 0 || len(out.EscapeDirections) > 0 || len(out.ExistingTransitions) > 0 {
		out.Status = "pass"
		return out, nil
	}
	if len(out.UnverifiedTransitions) > 0 {
		out.Status = "unverified"
		return out, nil
	}
	out.Status = "trapped"
	// Attribute the boundary: every object that rejects a track step out of the
	// reachable region or a via at a reachable point.
	blockers := map[string]pcbRoutabilityBlocker{}
	if len(reachable) == 0 {
		reachable = []node{{0, 0}}
	}
	for _, cur := range reachable {
		a := point(cur.i, cur.j)
		for _, d := range pcbRoutabilityDirections {
			next := node{cur.i + d.di, cur.j + d.dj}
			if seen[next] || !inside(next) {
				continue
			}
			local.routabilityBlockers(blockers, pad, pad.Layer, rules.TrackWidthMinMil, a, point(next.i, next.j), false)
		}
		local.routabilityBlockers(blockers, pad, 0, rules.ViaDiameterMil, a, a, true)
	}
	for _, b := range blockers {
		out.Blockers = append(out.Blockers, b)
	}
	sort.Slice(out.Blockers, func(i, j int) bool {
		x, y := out.Blockers[i], out.Blockers[j]
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		return x.Ref+x.ID < y.Ref+y.ID
	})
	return out, nil
}

func pcbRoutabilityOctant(i, j int) string {
	angle := math.Atan2(float64(j), float64(i))
	k := int(math.Round(angle/(math.Pi/4))+8) % 8
	return pcbRoutabilityDirections[k].name
}

// routabilityBlockers records objects closer than the rule margin to the probed
// track segment (layer > 0) or through-via disk (via == true, every copper
// layer). The source pad itself is never a
// blocker: a via-in-pad is excluded by policy, not by another object.
func (ctx *pcbEscapeContext) routabilityBlockers(into map[string]pcbRoutabilityBlocker, source pcbPadP, layer int, width float64, a, z [2]float64, via bool) {
	r := ctx.snap.Rules
	onLayer := func(l int) bool {
		if via {
			return netPathCopperLayer(l) || l == pcbLayerMulti
		}
		return l == layer || l == pcbLayerMulti
	}
	add := func(b pcbRoutabilityBlocker) {
		into[b.Kind+"|"+b.ID+"|"+b.Ref] = b
	}
	margin := width/2 + r.ClearanceMil - netPathGeomEps
	for _, p := range ctx.allPads {
		if p.ID == source.ID && p.Designator == source.Designator && p.Number == source.Number {
			continue
		}
		if !via && (p.Net == source.Net || !padLayerMatches(p.Layer, layer)) {
			continue
		}
		if gap, err := pcbExactPadSegmentGap(p, a, z); err == nil && gap < margin {
			add(pcbRoutabilityBlocker{Kind: "pad", ID: p.ID, Ref: p.Designator + "." + p.Number, Net: p.Net, Layer: p.Layer})
		}
	}
	for _, t := range ctx.tracks {
		if t.Net == source.Net || !onLayer(t.Layer) {
			continue
		}
		if segSegDist(a[0], a[1], z[0], z[1], t.X1, t.Y1, t.X2, t.Y2) < width/2+t.Width/2+math.Max(r.ClearanceMil, r.ClearanceTrackTrackMil)-netPathGeomEps {
			add(pcbRoutabilityBlocker{Kind: "track", ID: t.ID, Net: t.Net, Layer: t.Layer})
		}
	}
	for _, arc := range ctx.arcs {
		if arc.Net == source.Net || !onLayer(arc.Layer) {
			continue
		}
		curve, _, err := flattenNetPathArc(arc)
		if err != nil {
			continue
		}
		for i := 0; i+1 < len(curve); i++ {
			if segSegDist(a[0], a[1], z[0], z[1], curve[i].x, curve[i].y, curve[i+1].x, curve[i+1].y) < width/2+arc.Width/2+math.Max(r.ClearanceMil, r.ClearanceTrackTrackMil)-netPathGeomEps {
				add(pcbRoutabilityBlocker{Kind: "arc", ID: arc.ID, Net: arc.Net, Layer: arc.Layer})
				break
			}
		}
	}
	for _, v := range ctx.vias {
		if !via && v.Net == source.Net {
			continue
		}
		if segPtDist(v.X, v.Y, a[0], a[1], z[0], z[1]) < width/2+v.Dia/2+r.ClearanceMil-netPathGeomEps {
			add(pcbRoutabilityBlocker{Kind: "via", ID: v.ID, Net: v.Net})
		}
	}
	for _, area := range ctx.areas {
		if area.Net == source.Net || !onLayer(area.Layer) {
			continue
		}
		if copperAreaSegmentDistance(area, a, z) < margin {
			add(pcbRoutabilityBlocker{Kind: "copper", ID: area.ID, Net: area.Net, Layer: area.Layer})
		}
	}
	for _, ban := range ctx.bans {
		if !onLayer(ban.Layer) {
			continue
		}
		if copperAreaSegmentDistance(ban, a, z) < margin {
			add(pcbRoutabilityBlocker{Kind: "keepout", ID: ban.ID, Layer: ban.Layer})
		}
	}
	for _, slot := range ctx.slots {
		if copperAreaSegmentDistance(slot, a, z) < width/2+pcbSlotClearanceMil(r)-netPathGeomEps {
			add(pcbRoutabilityBlocker{Kind: "slot", ID: slot.ID, Layer: slot.Layer})
		}
	}
	for _, st := range ctx.strokes {
		if st.area.Net == source.Net || !onLayer(st.area.Layer) {
			continue
		}
		if st.segmentDistance(a, z) < margin {
			add(pcbRoutabilityBlocker{Kind: "copper", ID: st.area.ID, Net: st.area.Net, Layer: st.area.Layer})
		}
	}
	edge := r.CopperToEdgeMil + width/2 - netPathGeomEps
	if !ctx.snap.Outline.containsPoint(a[0], a[1]) || !ctx.snap.Outline.containsPoint(z[0], z[1]) || ctx.snap.Outline.distToEdge(a[0], a[1]) < edge || ctx.snap.Outline.distToEdge(z[0], z[1]) < edge {
		add(pcbRoutabilityBlocker{Kind: "board-edge"})
	}
}
