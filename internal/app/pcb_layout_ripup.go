package app

// pcb_layout_ripup.go — the routing a `pcb layout simulate` result must clear.
//
// A moved part leaves its copper behind: tracks, arcs and vias that ended on its
// pads now end on nothing. The Desk app deletes exactly the ids listed here with
// the typed `pcb.route.delete` before it moves the parts, so the list has to be
// right and conservative: stubs and paths from a moved pad up to the point where
// the copper still links parts that stay are cleared; GND / 3V3 webs between
// unmoved parts, locked copper and copper of other nets are never listed.
//
// The rule is a pure function over the dump and the set of moved component ids
// (ripupClear / ripupClearNet), per net:
//
//  1. Contacts. Line/arc ends touch a line/arc end of the same layer, or a via
//     centre (a via spans every layer), within max(1 mil, half the narrower
//     width). A line/arc end touches a pad when it lies in the pad's copper
//     grown by half the track width, on the pad's layer (through-hole pads: any
//     layer); a via centre touches a pad that contains it. Only same-net
//     contacts count.
//  2. Anchors are pads of parts that stay, locked copper and copper embedded in
//     a footprint that stays. Pads (and embedded copper) of moved parts leave the
//     graph: they are where the copper loses contact.
//  3. Prune: repeatedly remove a non-anchor element with at most one live
//     attachment left, then remove every remaining group that touches no anchor.
//  4. Cleared = removed in step 3 AND in a group that touches a moved pad.
//
// What "attachment" and "group" mean here, where this is deliberately stricter
// than a plain neighbour count (each choice deletes less, never more):
//   - A line/arc has three attachment places: its two ends and its body. It is
//     a stub when at most one of them still has live copper or an unmoved pad.
//     So a stub ending on a junction shared by two other tracks is still a stub,
//     and a track with a branch on its body is not one.
//   - A line/arc end that lies on another line/arc's body (a T-junction without
//     a split) or a via centre on a track's body is a contact too. Ignoring it
//     would delete the path a branch hangs on.
//   - Groups are formed over unlocked copper only: an unmoved pad or a locked
//     element ends a group. Copper that hangs off an unmoved pad stays, even if
//     the same net also has a moved pad somewhere else.
//
// Footprint-embedded copper (a QFN's thermal vias: id = component id + "e<n>")
// travels with its part and the host refuses to delete it, so it is never
// listed; it counts as a pad of its part.

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// lsRipupNet is the copper of one net the result clears.
type lsRipupNet struct {
	Net    string `json:"net"`
	Tracks int    `json:"tracks"`
	Arcs   int    `json:"arcs"`
	Vias   int    `json:"vias"`
}

// lsRipup lists the routing a result's moves must clear (primitive ids, sorted,
// unique). Availability "unknown" (the copper could not be read) always comes
// with empty lists: a missing read is never "nothing to clear". PourNets are the
// nets of the cleared copper that also have a pour, which then needs a refill.
type lsRipup struct {
	Availability string       `json:"availability"`
	Tracks       []string     `json:"tracks"`
	Arcs         []string     `json:"arcs"`
	Vias         []string     `json:"vias"`
	Nets         []lsRipupNet `json:"nets"`
	PourNets     []string     `json:"pourNets"`
}

func newLsRipup(availability string) lsRipup {
	return lsRipup{Availability: availability, Tracks: []string{}, Arcs: []string{}, Vias: []string{}, Nets: []lsRipupNet{}, PourNets: []string{}}
}

// ripupElem is one piece of copper. A track/arc has two end points (P[0], P[1]);
// a via has its centre in P[0] and its diameter as Width. Curve is the centreline
// polyline (two points for a track, the flattened arc for an arc).
type ripupElem struct {
	ID     string
	Net    string
	Kind   string // "track" | "arc" | "via"
	Layer  int    // copper layer of a track/arc; 0 for a via (every layer)
	P      [2][2]float64
	Width  float64
	Locked bool
	Owner  string // id of the component whose footprint embeds this copper
	Curve  []netPathPoint
}

// ripupPad is one placed pad with the component it belongs to. A pad whose
// shape the dump carries is tested with the net-path pad reader (rotation
// aware); any other pad by the axis-aligned width × height envelope the
// connector reports, or a nominal box when the dump has no size at all.
type ripupPad struct {
	Comp     string
	Net      string
	X, Y     float64
	Layer    int
	AnyLayer bool // through-hole (MULTI layer or a drill) or a layer we cannot read
	W, H     float64
	shaped   bool
	node     pcbNetPathNode
}

// touches reports whether copper at (x, y) with the given half width lies on the
// pad's copper. The layer is checked by the caller.
func (p ripupPad) touches(x, y, radius float64) bool {
	if p.shaped {
		return pointTouchesPad(x, y, p.node, radius)
	}
	return rectPtDist(p.X-p.W/2, p.Y-p.H/2, p.X+p.W/2, p.Y+p.H/2, x, y) <= radius+netPathGeomEps
}

func (p ripupPad) onLayer(layer int) bool { return p.AnyLayer || layer == 0 || p.Layer == layer }

// simRipup computes the ripup of a simulate result: routingAvailable is
// routing.availability == "available" and moved the primitive ids of the parts
// the result moves. A copper or pad inventory that cannot be read as a whole
// gives "unknown" with empty lists.
func simRipup(snap *boardSnapshot, routingAvailable bool, moved map[string]bool) lsRipup {
	if !routingAvailable {
		return newLsRipup("unknown")
	}
	if len(moved) == 0 {
		return newLsRipup("available")
	}
	elems, pads, err := ripupInputs(snap)
	if err != nil {
		return newLsRipup("unknown")
	}
	out := newLsRipup("available")
	cleared := ripupClear(elems, pads, moved)
	if len(cleared) == 0 {
		return out
	}
	pour := ripupPourNets(snap)
	counts := map[string]*lsRipupNet{}
	seen := map[string]bool{}
	for _, e := range cleared {
		if seen[e.Kind+":"+e.ID] {
			continue
		}
		seen[e.Kind+":"+e.ID] = true
		n := counts[e.Net]
		if n == nil {
			n = &lsRipupNet{Net: e.Net}
			counts[e.Net] = n
		}
		switch e.Kind {
		case "track":
			out.Tracks = append(out.Tracks, e.ID)
			n.Tracks++
		case "arc":
			out.Arcs = append(out.Arcs, e.ID)
			n.Arcs++
		default:
			out.Vias = append(out.Vias, e.ID)
			n.Vias++
		}
	}
	sort.Strings(out.Tracks)
	sort.Strings(out.Arcs)
	sort.Strings(out.Vias)
	for net, n := range counts {
		out.Nets = append(out.Nets, *n)
		if pour[net] {
			out.PourNets = append(out.PourNets, net)
		}
	}
	sort.Slice(out.Nets, func(i, j int) bool { return out.Nets[i].Net < out.Nets[j].Net })
	sort.Strings(out.PourNets)
	return out
}

// ── snapshot → elements and pads ─────────────────────────────────────────────

// ripupInputs reads the dump's copper and pads. It fails (→ "unknown") when the
// component inventory is not usable, a component's pads were not captured, or
// any copper-layer line/arc/via cannot be parsed: absence of a connection cannot
// be shown from data that was not read.
func ripupInputs(snap *boardSnapshot) ([]ripupElem, []ripupPad, error) {
	if snap == nil || snap.Copper == nil {
		return nil, nil, fmt.Errorf("copper snapshot is missing")
	}
	if snap.ComponentsAvailability != "" && snap.ComponentsAvailability != "available" {
		return nil, nil, fmt.Errorf("components inventory is %s", snap.ComponentsAvailability)
	}
	compIDs := map[string]bool{}
	var pads []ripupPad
	for _, c := range snap.Components {
		if state, declared := c.Availability["pads"]; (declared && state != "available") || c.CaptureErrors["pads"] != nil || len(c.PadFailures) > 0 {
			return nil, nil, fmt.Errorf("pads of %s were not captured", c.Designator)
		}
		if strings.TrimSpace(c.ID) == "" {
			continue
		}
		compIDs[c.ID] = true
		for _, p := range c.Pads {
			net := strings.TrimSpace(p.Net)
			if net == "" || !allFinite(p.X, p.Y, p.Rotation) {
				continue // netless pads touch nothing; an unplaced pad has no position
			}
			pads = append(pads, ripupPadOf(c, p, net))
		}
	}
	elems, err := ripupCopper(snap.Copper, compIDs)
	if err != nil {
		return nil, nil, err
	}
	return elems, pads, nil
}

// ripupPadOf measures a pad with the reader pcb check / net-path use. A pad with
// a supported shape is tested rotation aware; any other pad by its axis-aligned
// width × height envelope (the connector's own extent), and a pad whose size the
// dump does not carry by the nominal half extent pcb check assumes.
func ripupPadOf(c boardComp, p boardPad, net string) ripupPad {
	m, _, _ := pcbMeasuredPad(p, c.Designator)
	rp := ripupPad{Comp: c.ID, Net: net, X: p.X, Y: p.Y, Layer: p.Layer, W: p.W, H: p.H}
	rp.AnyLayer = p.Layer == pcbLayerMulti || p.Hole != nil || !netPathCopperLayer(p.Layer)
	if m.ShapeOK {
		rp.shaped = true
		rp.node = pcbNetPathNode{kind: "pad", net: net, layer: p.Layer, x: p.X, y: p.Y, w: m.ShapeW, h: m.ShapeH,
			rotation: p.Rotation, shape: m.Shape, shapeRound: m.ShapeRound, shapeSides: m.ShapeSides}
	}
	if rp.W <= 0 || rp.H <= 0 {
		rp.W, rp.H = 2*nominalPadHalf, 2*nominalPadHalf
	}
	return rp
}

// ripupCopper parses the dump's copper-layer lines, arcs and vias (outline, silk
// and other non-copper line primitives are not routing) with the net-path
// readers, keeping the locked flag and the footprint-embedded owner. Netless
// copper is returned too; the rule never touches it.
func ripupCopper(c *boardCopperSnapshot, compIDs map[string]bool) ([]ripupElem, error) {
	locked := map[string]bool{}
	lines, arcs, vias := []any{}, []any{}, []any{}
	pick := func(kind string, group []any, dst *[]any, layered bool) {
		for _, raw := range group {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if layered {
				layer, ok := finiteFloat(m["layer"])
				if !ok || layer != math.Trunc(layer) || !netPathCopperLayer(int(layer)) {
					continue
				}
			}
			*dst = append(*dst, m)
			locked[kind+":"+strings.TrimSpace(asString(m["primitiveId"]))] = asBool(m["locked"])
		}
	}
	pick("track", c.Lines, &lines, true)
	pick("arc", c.Arcs, &arcs, true)
	pick("via", c.Vias, &vias, false)
	// arcsAvailable is vouched for by the caller (routing.availability).
	tracks, arcP, err := parseNetPathLines(map[string]any{"lines": lines, "arcs": arcs, "arcsAvailable": true})
	if err != nil {
		return nil, err
	}
	viaP, err := parseNetPathVias(map[string]any{"vias": vias})
	if err != nil {
		return nil, err
	}
	var out []ripupElem
	for _, t := range tracks {
		out = append(out, ripupElem{ID: t.ID, Net: strings.TrimSpace(t.Net), Kind: "track", Layer: t.Layer,
			P: [2][2]float64{{t.X1, t.Y1}, {t.X2, t.Y2}}, Width: t.Width, Locked: locked["track:"+t.ID], Owner: ripupEmbeddedOwner(t.ID, compIDs),
			Curve: []netPathPoint{{x: t.X1, y: t.Y1}, {x: t.X2, y: t.Y2}}})
	}
	for _, a := range arcP {
		curve, _, err := flattenNetPathArc(a)
		if err != nil { // geometry of the bend unknown: its chord is the best centreline we have
			curve = []netPathPoint{{x: a.X1, y: a.Y1}, {x: a.X2, y: a.Y2}}
		}
		out = append(out, ripupElem{ID: a.ID, Net: strings.TrimSpace(a.Net), Kind: "arc", Layer: a.Layer,
			P: [2][2]float64{{a.X1, a.Y1}, {a.X2, a.Y2}}, Width: a.Width, Locked: locked["arc:"+a.ID], Owner: ripupEmbeddedOwner(a.ID, compIDs), Curve: curve})
	}
	for _, v := range viaP {
		out = append(out, ripupElem{ID: v.ID, Net: strings.TrimSpace(v.Net), Kind: "via",
			P: [2][2]float64{{v.X, v.Y}}, Width: v.Dia, Locked: locked["via:"+v.ID], Owner: ripupEmbeddedOwner(v.ID, compIDs)})
	}
	return out, nil
}

var ripupEmbeddedSuffix = regexp.MustCompile(`^e\d+$`)

// ripupEmbeddedOwner returns the component that embeds the primitive, or "": a
// footprint-embedded id is its parent's id plus its own `e<digits>` (the rule
// `pcb.route.delete` applies; a bare prefix match is not ownership, so top-level
// `e200` is independent of component `e2`). The suffix starts at the last 'e'.
func ripupEmbeddedOwner(id string, compIDs map[string]bool) string {
	k := strings.LastIndexByte(id, 'e')
	if k <= 0 || !ripupEmbeddedSuffix.MatchString(id[k:]) {
		return ""
	}
	if parent := id[:k]; compIDs[parent] {
		return parent
	}
	return ""
}

// ripupPourNets are the nets that own a pour boundary or a materialised pour.
func ripupPourNets(snap *boardSnapshot) map[string]bool {
	out := map[string]bool{}
	if snap == nil || snap.Copper == nil {
		return out
	}
	for _, group := range [][]any{snap.Copper.Pours, snap.Copper.Poured} {
		for _, raw := range group {
			if m, ok := raw.(map[string]any); ok {
				if net := strings.TrimSpace(asString(m["net"])); net != "" {
					out[net] = true
				}
			}
		}
	}
	return out
}

// ── the rule ─────────────────────────────────────────────────────────────────

// ripupState of an element in the graph.
const (
	ripupNormal = iota // unlocked routing: may be cleared
	ripupAnchor        // locked copper, or copper embedded in a footprint that stays
	ripupDead          // copper embedded in a footprint that moves: travels with it, never listed
)

// ripupLink is one contact of an element: another element (elem >= 0) or a pad
// (pad >= 0). Place groups contacts: 0 and 1 are a track/arc's ends, 2 its body;
// every contact of a via is a place of its own.
type ripupLink struct {
	place, elem, pad int
}

// ripupClear applies the rule to every net and returns the cleared elements.
func ripupClear(elems []ripupElem, pads []ripupPad, moved map[string]bool) []ripupElem {
	byNet := map[string][]int{}
	for i, e := range elems {
		if e.Net != "" {
			byNet[e.Net] = append(byNet[e.Net], i)
		}
	}
	padsByNet := map[string][]int{}
	for i, p := range pads {
		padsByNet[p.Net] = append(padsByNet[p.Net], i)
	}
	nets := make([]string, 0, len(byNet))
	for n := range byNet {
		nets = append(nets, n)
	}
	sort.Strings(nets)
	var out []ripupElem
	for _, net := range nets {
		netElems := make([]ripupElem, len(byNet[net]))
		for k, i := range byNet[net] {
			netElems[k] = elems[i]
		}
		netPads := make([]ripupPad, len(padsByNet[net]))
		for k, i := range padsByNet[net] {
			netPads[k] = pads[i]
		}
		for _, k := range ripupClearNet(netElems, netPads, moved) {
			out = append(out, netElems[k])
		}
	}
	return out
}

// ripupClearNet runs the rule on one net (all elements and pads share it) and
// returns the indexes of the cleared elements.
func ripupClearNet(elems []ripupElem, pads []ripupPad, moved map[string]bool) []int {
	n := len(elems)
	state := make([]int, n)
	for i, e := range elems {
		switch {
		case e.Owner != "" && moved[e.Owner]:
			state[i] = ripupDead
		case e.Owner != "" || e.Locked:
			state[i] = ripupAnchor
		}
	}
	links := make([][]ripupLink, n)
	addElem := func(i, place, j int) { links[i] = append(links[i], ripupLink{place: place, elem: j, pad: -1}) }
	viaPlace := func(i int) int { return len(links[i]) + 3 }

	// 1. contacts
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if pi, pj, ok := ripupTouch(elems[i], elems[j]); ok {
				if elems[i].Kind == "via" {
					pi = viaPlace(i)
				}
				if elems[j].Kind == "via" {
					pj = viaPlace(j)
				}
				addElem(i, pi, j)
				addElem(j, pj, i)
			}
		}
		for k, p := range pads {
			if place, ok := ripupTouchPad(elems[i], p); ok {
				if elems[i].Kind == "via" {
					place = viaPlace(i)
				}
				links[i] = append(links[i], ripupLink{place: place, elem: -1, pad: k})
			}
		}
	}
	padMoved := func(k int) bool { return moved[pads[k].Comp] }

	// 2–3a. prune stubs: a normal element with at most one place that still has
	// live copper or an unmoved pad on it.
	removed := make([]bool, n)
	live := func(l ripupLink) bool {
		if l.pad >= 0 {
			return !padMoved(l.pad)
		}
		return state[l.elem] != ripupDead && !removed[l.elem]
	}
	stub := func(i int) bool {
		places := map[int]bool{}
		for _, l := range links[i] {
			if live(l) {
				places[l.place] = true
			}
		}
		return len(places) <= 1
	}
	queue := []int{}
	for i := 0; i < n; i++ {
		if state[i] == ripupNormal {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		if removed[i] || !stub(i) {
			continue
		}
		removed[i] = true
		for _, l := range links[i] {
			if l.elem >= 0 && state[l.elem] == ripupNormal && !removed[l.elem] {
				queue = append(queue, l.elem)
			}
		}
	}

	// 3b. remove remaining normal groups that touch no anchor (an unmoved pad or
	// anchor copper). Groups join normal copper only.
	seen := make([]bool, n)
	for s := 0; s < n; s++ {
		if state[s] != ripupNormal || removed[s] || seen[s] {
			continue
		}
		group, anchored := []int{s}, false
		seen[s] = true
		for q := 0; q < len(group); q++ {
			for _, l := range links[group[q]] {
				switch {
				case l.pad >= 0:
					anchored = anchored || !padMoved(l.pad)
				case state[l.elem] == ripupAnchor:
					anchored = true
				case state[l.elem] == ripupNormal && !removed[l.elem] && !seen[l.elem]:
					seen[l.elem] = true
					group = append(group, l.elem)
				}
			}
		}
		if !anchored {
			for _, i := range group {
				removed[i] = true
			}
		}
	}

	// 4. cleared = removed and in a contact group (normal copper joined through
	// moved pads and dead copper, step-1 graph) that contains a moved part.
	parent := make([]int, n+len(pads))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	join := func(a, b int) { parent[find(a)] = find(b) }
	for i := 0; i < n; i++ {
		if state[i] == ripupAnchor {
			continue
		}
		for _, l := range links[i] {
			switch {
			case l.pad >= 0 && padMoved(l.pad):
				join(i, n+l.pad)
			case l.elem >= 0 && state[l.elem] != ripupAnchor:
				join(i, l.elem)
			}
		}
	}
	hit := map[int]bool{}
	for i := 0; i < n; i++ {
		if state[i] == ripupDead {
			hit[find(i)] = true
		}
	}
	for k := range pads {
		if padMoved(k) {
			hit[find(n+k)] = true
		}
	}
	var out []int
	for i := 0; i < n; i++ {
		if removed[i] && hit[find(i)] {
			out = append(out, i)
		}
	}
	return out
}

// ripupTol is the contact tolerance between two pieces of copper.
func ripupTol(a, b ripupElem) float64 { return math.Max(1, math.Min(a.Width, b.Width)/2) }

// ripupTouch reports a contact between two elements of the same net and the
// place on each (0/1 = end, 2 = body; ignored for a via).
func ripupTouch(a, b ripupElem) (int, int, bool) {
	if a.Kind != "via" && b.Kind != "via" && a.Layer != b.Layer {
		return 0, 0, false
	}
	tol := ripupTol(a, b)
	near := func(p, q [2]float64) bool { return math.Hypot(p[0]-q[0], p[1]-q[1]) <= tol }
	ends := func(e ripupElem) [][2]float64 {
		if e.Kind == "via" {
			return [][2]float64{e.P[0]}
		}
		return [][2]float64{e.P[0], e.P[1]}
	}
	// end to end (a via centre counts as an end)
	for i, p := range ends(a) {
		for j, q := range ends(b) {
			if near(p, q) {
				return i, j, true
			}
		}
	}
	// an end (or via centre) on the other's body
	onBody := func(p [2]float64, e ripupElem) bool {
		return e.Kind != "via" && netPathPointCurveDistance(netPathPoint{x: p[0], y: p[1]}, netPathCopperCurve{points: e.Curve}) <= tol
	}
	for i, p := range ends(a) {
		if onBody(p, b) {
			return i, 2, true
		}
	}
	for j, q := range ends(b) {
		if onBody(q, a) {
			return 2, j, true
		}
	}
	return 0, 0, false
}

// ripupTouchPad reports whether an element touches a pad and at which place: a
// track/arc end inside the pad grown by half the track width, on the pad's
// layer; a via centre inside the pad.
func ripupTouchPad(e ripupElem, p ripupPad) (int, bool) {
	if e.Kind == "via" {
		return 0, p.touches(e.P[0][0], e.P[0][1], 0)
	}
	if !p.onLayer(e.Layer) {
		return 0, false
	}
	for i, q := range e.P {
		if p.touches(q[0], q[1], e.Width/2) {
			return i, true
		}
	}
	return 0, false
}
