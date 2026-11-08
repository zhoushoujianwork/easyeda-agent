package app

// pcb check — reconstructed DFM (design-for-manufacture) audit.
//
// The PCB sibling of `sch check`. EasyEDA's native `pcb drc` catches
// rule-clearance violations (track-to-pad, width, hole); it does NOT flag the
// manufacturability / reliability hazards that the official DFM tools look for:
// acid-trap acute angles, dangling copper stubs, free-angle traces, track-over-pad
// shorts, flipped/back-side silkscreen, pointless single-layer vias, stacked/
// redundant vias, asymmetric neck-down on 2-pin parts, or duplicated overlapping
// copper. Copper rules recompute purely from the placed primitives (tracks + vias
// + pads); the silkscreen-orientation rule reads text layer+mirror via the
// `pcb.silk.list` connector handler. Mirrors the Go-side geometry approach of
// `pcb layout-lint`.
//
// Pure core (analyzePcbCheck) is unit-tested; the live fetch/render is
// runPcbCheck below. Reuses isGlobalNet (pcb_autoplace.go) and round2
// (cmd_sch_layout.go). Arcs are out of scope for v1 (no pcb.arc.list action);
// auto-routed / short-routed copper is line segments, so coverage is high.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
	"github.com/zhoushoujianwork/easyeda-agent/internal/spec"
)

// Geometry thresholds, all in mil (PCB primitives are native mil — pcb.line.create
// passes coordinates straight to eda.* with no scaling).
const (
	pcbCoincEps    = 2.0  // endpoint↔endpoint / ↔via / ↔pad-center coincidence
	pcbOnSegEps    = 1.5  // point-lies-on-track-body (T-junction / collinear test)
	pcbAcuteDeg    = 90.0 // interior trace angle below this = acid-trap acute corner
	pcbAcuteMinDeg = 5.0  // …but at/below this it's collinear overlap, not a corner (dup-segment covers it)
	pcbViaDupEps   = 2.0  // two vias closer than this = redundant/stacked
	pcbWidthTolMil = 1.0  // absolute width tolerance for 2-pin neck-down symmetry
	pcbWidthTolRel = 0.25 // relative width tolerance (25%)
	pcbDupOverlap  = 2.0  // collinear same-net overlap longer than this = duplicate copper

	pcbCouplingW       = 3.0  // default 3W factor: center-to-center spacing < this×maxWidth = coupling risk
	pcbParallelDeg     = 15.0 // two segments within this of parallel/anti-parallel are "parallel"
	pcbCouplingMinOvlp = 20.0 // parallel overlap must exceed this (mil) to count (ignore incidental)

	pcbOrthoTolDeg = 1.0 // a track this far off the nearest 0/45/90/135° = free-angle routing
	pcbOverPadEps  = 2.0 // pad center within this of a track body (but not its endpoint) = track-over-pad
)

// pcbTrack is one copper line segment (pcb.line.list).
type pcbTrack struct {
	ID     string
	Net    string
	Layer  int
	X1, Y1 float64
	X2, Y2 float64
	Width  float64
}

// pcbArc is a copper ARC (pcb.line.list → arcs). beautify (走线美化) rounds a sharp
// corner into a track→arc→track chain, so a track terminating on an arc endpoint is
// electrically continued, not floating. Only the endpoints are carried — that is
// where a track joins the arc; Width/ArcAngle are carried for pcb net-path evidence,
// while the DFM anchoring rules below need only the endpoints.
type pcbArc struct {
	ID       string
	Net      string
	Layer    int
	X1, Y1   float64
	X2, Y2   float64
	Width    float64
	ArcAngle float64
}

// pcbViaP is one via (pcb.via.list).
type pcbViaP struct {
	ID   string
	Net  string
	X, Y float64
	Hole float64
	Dia  float64
}

// pcbPadP is one placed pad (pcb.components.list --include-pads). W/H are the
// real axis-aligned copper extents (mil) from the connector's pad-shape tuple;
// 0 = unknown (older connector, or a complex-polygon pad) → halfExt() falls back
// to the nominal estimate.
type pcbPadP struct {
	ID         string
	Designator string
	Number     string
	Net        string
	Layer      int
	X, Y       float64
	W, H       float64
	Rotation   float64
	Shape      string
	ShapeW     float64
	ShapeH     float64
	ShapeRound float64
	ShapeSides int
	ShapeOK    bool
	ShapeIssue string
	SpecialPad bool
}

// halfExt is the pad's half-extent (mil) for center-distance clearance math —
// the real max(W,H)/2 when the connector reported a size, else nominalPadHalf.
func (p pcbPadP) halfExt() float64 {
	if h := math.Max(p.W, p.H) / 2; h > 0 {
		return h
	}
	return nominalPadHalf
}

// pcbSlotP is one board cutout / slot (a MULTI-layer fill, layer 12 — e.g. an M3
// mounting hole). The mill removes material on EVERY layer, so all copper must
// keep the spacing rule from its bbox.
type pcbSlotP struct {
	ID                     string
	MinX, MinY, MaxX, MaxY float64
}

// rectPtDist is the distance from a point to an axis-aligned rect (0 inside).
func rectPtDist(minX, minY, maxX, maxY, x, y float64) float64 {
	dx := math.Max(math.Max(minX-x, 0), x-maxX)
	dy := math.Max(math.Max(minY-y, 0), y-maxY)
	return math.Hypot(dx, dy)
}

// rectSegDist is the shortest distance from a segment to an axis-aligned rect
// (0 if the segment touches or enters it).
func rectSegDist(minX, minY, maxX, maxY, x1, y1, x2, y2 float64) float64 {
	if rectPtDist(minX, minY, maxX, maxY, x1, y1) == 0 || rectPtDist(minX, minY, maxX, maxY, x2, y2) == 0 {
		return 0
	}
	d := math.Inf(1)
	edges := [4][4]float64{
		{minX, minY, maxX, minY}, {maxX, minY, maxX, maxY},
		{maxX, maxY, minX, maxY}, {minX, maxY, minX, minY},
	}
	for _, e := range edges {
		if segSegCross(x1, y1, x2, y2, e[0], e[1], e[2], e[3]) {
			return 0
		}
		d = math.Min(d, math.Min(
			math.Min(segPtDist(e[0], e[1], x1, y1, x2, y2), segPtDist(e[2], e[3], x1, y1, x2, y2)),
			math.Min(segPtDist(x1, y1, e[0], e[1], e[2], e[3]), segPtDist(x2, y2, e[0], e[1], e[2], e[3])),
		))
	}
	return d
}

// pcbSilkText is one silkscreen text primitive (pcb.silk.list) — a component
// designator/value attribute or a free string. Layer is the silk layer
// (silkTopLayer / silkBottomLayer); CompLayer is the parent component's side
// (pcbSideTop / pcbSideBottom, 0 = none/unknown) for attributes.
type pcbSilkText struct {
	ID        string
	Kind      string // "attribute" | "string"
	Key       string // attribute key: "Designator" | "Footprint" | "Device"
	Text      string
	Layer     int
	Mirror    bool
	Reverse   bool    // reversed reading (left-right flipped) — reads backwards
	Rotation  float64 // degrees; a designator should read upright (0°)
	FontSize  float64 // mil; 0 = unknown (older connector) → estimate at 40
	CompID    string
	CompLayer int
	X, Y      float64 // stored BOTTOM-LEFT anchor, NOT the text center (#155)
	// Real rendered extent from the connector (nil on older connectors). When
	// present it supersedes any anchor/char-width/rotation estimation.
	BBox                      *pcbRect
	FontFamily                string   `json:"fontFamily,omitempty"`
	LineWidth                 *float64 `json:"lineWidth,omitempty"`
	LineWidthAvailability     string   `json:"lineWidthAvailability,omitempty"`
	LineWidthError            string   `json:"lineWidthError,omitempty"`
	LineWidthSource           string   `json:"lineWidthSource,omitempty"`
	Visible                   *bool    `json:"visible,omitempty"`
	VisibilityAvailability    string   `json:"visibilityAvailability,omitempty"`
	VisibilitySource          string   `json:"visibilitySource,omitempty"`
	VisibilityError           string   `json:"visibilityError,omitempty"`
	KeyVisible                *bool    `json:"keyVisible,omitempty"`
	KeyVisibilityAvailability string   `json:"keyVisibilityAvailability,omitempty"`
	KeyVisibilitySource       string   `json:"keyVisibilitySource,omitempty"`
	KeyVisibilityError        string   `json:"keyVisibilityError,omitempty"`
}

// pcbRect is a plain min/max rectangle in board mils.
type pcbRect struct {
	MinX, MinY, MaxX, MaxY float64
}

// Silk / component side layer ids (EPCB_LayerId).
const (
	pcbSideTop      = 1
	pcbSideBottom   = 2
	pcbLayerMulti   = 12 // 多层 / MULTI — a region here spans every copper layer at once
	silkTopLayer    = 3
	silkBottomLayer = 4
)

// pcbXY is a coordinate on a finding.
type pcbXY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// pcbCheckFinding is one DFM issue.
type pcbCheckFinding struct {
	Type       string    `json:"type"`
	Level      string    `json:"level"` // ERROR | WARN | INFO
	Net        string    `json:"net,omitempty"`
	Nets       []string  `json:"nets,omitempty"`
	Layer      int       `json:"layer,omitempty"`
	Designator string    `json:"designator,omitempty"`
	Primitives []string  `json:"primitives,omitempty"`
	AngleDeg   float64   `json:"angleDeg,omitempty"`
	Widths     []float64 `json:"widths,omitempty"`
	Message    string    `json:"message"`
	At         *pcbXY    `json:"at,omitempty"`
}

type pcbCheckSummary struct {
	DanglingEnds      int `json:"danglingEnds"`
	AcuteAngles       int `json:"acuteAngles"`
	NonOrthogonal     int `json:"nonOrthogonal"`
	TrackOverPad      int `json:"trackOverPad"`
	Clearance         int `json:"clearance"`
	SilkscreenFlipped int `json:"silkscreenFlipped"`
	OverlappingVias   int `json:"overlappingVias"`
	SingleLayerVias   int `json:"singleLayerVias"`
	WidthMismatches   int `json:"widthMismatches"`
	DuplicateSegments int `json:"duplicateSegments"`
	ParallelCoupling  int `json:"parallelCoupling"`
	AntennaKeepout    int `json:"antennaKeepout"`
	NetlessPours      int `json:"netlessPours"`
	ViaCrossesPlane   int `json:"viaCrossesPlane"`
	FloatingIslands   int `json:"floatingIslands"`
	PowerNotPoured    int `json:"powerNotPoured"`
	NetlessViaInPad   int `json:"netlessViaInPad"`
	WidthUnderSpec    int `json:"widthUnderSpec"`
	SilkOverPad       int `json:"silkOverPad"`
	SilkOffBoard      int `json:"silkOffBoard"`
	DecapTooFar       int `json:"decapTooFar"`
	ViaInPad          int `json:"viaInPad"`
	CopperNearEdge    int `json:"copperNearEdge"`
	FiducialMissing   int `json:"fiducialMissing"`
	ZoneViolation     int `json:"zoneViolation"`
	ZoneClaimsStale   int `json:"zoneClaimsStale,omitempty"`
	// #168 连接器布局：内部件占板外沿 / 相邻对外口插头护套打架。
	InternalOnEdge         int `json:"internalOnEdge"`
	ConnectorPlugClearance int `json:"connectorPlugClearance"`
	// 插拔通道禁布：器件挡在卧贴插口的开口前方，插头进不来。
	ConnectorMatingBlocked int `json:"connectorMatingBlocked"`
	ConnectorOpeningInward int `json:"connectorOpeningInward"`
	Errors                 int `json:"errors"`
	Warnings               int `json:"warnings"`
	Total                  int `json:"total"`
}

type pcbCheckReport struct {
	Passed      bool                `json:"passed"`
	Summary     pcbCheckSummary     `json:"summary"`
	TrackCount  int                 `json:"trackCount"`
	ViaCount    int                 `json:"viaCount"`
	PadCount    int                 `json:"padCount"`
	Findings    []pcbCheckFinding   `json:"findings"`
	Limitations []string            `json:"limitations,omitempty"`
	Incomplete  bool                `json:"incomplete"`
	Coverage    pcbGeometryCoverage `json:"coverage"`
}

// analyzePcbCheck is the copper-only DFM core (no silkscreen). Thin wrapper over
// analyzePcbCheckFull; kept for the many unit tests that don't exercise silk.
func analyzePcbCheck(pads []pcbPadP, tracks []pcbTrack, vias []pcbViaP, couplingW float64) pcbCheckReport {
	return analyzePcbCheckFull(pads, tracks, vias, nil, nil, couplingW, nil)
}

// analyzePcbCheckFull is the pure DFM core over placed primitives. couplingW is the
// 3W-rule center-spacing factor (≤0 → default pcbCouplingW). silk feeds the
// silkscreen-orientation rule (flipped/back-side labels); arcs (beautify's rounded
// corners) anchor track endpoints so rounding doesn't fabricate dangling stubs.
func analyzePcbCheckFull(pads []pcbPadP, tracks []pcbTrack, vias []pcbViaP, arcs []pcbArc, silk []pcbSilkText, couplingW float64, diffPairs []pcbDiffPair) pcbCheckReport {
	rep := pcbCheckReport{TrackCount: len(tracks), ViaCount: len(vias), PadCount: len(pads)}
	legacyPadGeometry, unknownPadGeometry := 0, 0
	for _, p := range pads {
		if p.ShapeOK {
			continue
		}
		if p.ShapeIssue != "" {
			rep.Limitations = append(rep.Limitations, fmt.Sprintf("pad %s.%s: %s", p.Designator, p.Number, p.ShapeIssue))
		} else if p.W > 0 && p.H > 0 {
			legacyPadGeometry++
		} else {
			unknownPadGeometry++
		}
	}
	if legacyPadGeometry > 0 {
		rep.Limitations = append(rep.Limitations, fmt.Sprintf("%d legacy pad(s) have width/height only; endpoint anchoring uses a conservative legacy ellipse/cardinal or inscribed-circle approximation, never the full AABB", legacyPadGeometry))
	}
	if unknownPadGeometry > 0 {
		rep.Limitations = append(rep.Limitations, fmt.Sprintf("%d pad(s) have no readable geometry; only center coincidence can anchor them", unknownPadGeometry))
	}
	if couplingW <= 0 {
		couplingW = pcbCouplingW
	}

	// Drop degenerate zero-length tracks up front (they connect nothing and would
	// pollute angle/dangling math).
	real := tracks[:0:0]
	for _, t := range tracks {
		if math.Hypot(t.X2-t.X1, t.Y2-t.Y1) >= pcbCoincEps {
			real = append(real, t)
		}
	}
	tracks = real

	rep.Findings = append(rep.Findings, findDanglingEnds(tracks, vias, pads, arcs)...)
	// Teardrop lines (pcb teardrop-plan) meet their track at a shallow angle by
	// design; the corner and grid rules judge the routing without them.
	routed := withoutTeardrops(tracks, pads)
	rep.Findings = append(rep.Findings, findAcuteAngles(routed, pads)...)
	rep.Findings = append(rep.Findings, findNonOrthogonal(routed)...)
	rep.Findings = append(rep.Findings, findTrackOverPad(tracks, pads)...)
	rep.Findings = append(rep.Findings, findViaIssues(tracks, vias)...)
	rep.Findings = append(rep.Findings, findWidthMismatch(tracks, pads)...)
	rep.Findings = append(rep.Findings, findDuplicateSegments(tracks)...)
	rep.Findings = append(rep.Findings, findParallelCoupling(tracks, couplingW, diffPairs)...)
	rep.Findings = append(rep.Findings, findSilkscreenFlipped(silk)...)
	rep.Findings = append(rep.Findings, findSilkOverPad(silk, pads)...)
	rep.Findings = append(rep.Findings, findDecapTooFar(pads)...)
	rep.Findings = append(rep.Findings, findViaInPad(vias, pads)...)
	rep.Findings = append(rep.Findings, findFiducialMissing(pads)...)

	for _, f := range rep.Findings {
		switch f.Type {
		case "dangling-end":
			rep.Summary.DanglingEnds++
		case "acute-angle":
			rep.Summary.AcuteAngles++
		case "non-orthogonal":
			rep.Summary.NonOrthogonal++
		case "track-over-pad":
			rep.Summary.TrackOverPad++
		case "overlapping-via":
			rep.Summary.OverlappingVias++
		case "single-layer-via":
			rep.Summary.SingleLayerVias++
		case "width-mismatch":
			rep.Summary.WidthMismatches++
		case "duplicate-segment":
			rep.Summary.DuplicateSegments++
		case "parallel-coupling":
			rep.Summary.ParallelCoupling++
		case "silkscreen-flipped":
			rep.Summary.SilkscreenFlipped++
		case "silk-over-pad":
			rep.Summary.SilkOverPad++
		case "decap-too-far":
			rep.Summary.DecapTooFar++
		case "via-in-pad":
			rep.Summary.ViaInPad++
		case "fiducial-missing":
			rep.Summary.FiducialMissing++
		}
		switch f.Level {
		case "ERROR":
			rep.Summary.Errors++
			rep.Summary.Warnings++
		case "WARN":
			rep.Summary.Warnings++
		}
	}
	rep.Summary.Total = len(rep.Findings)
	rep.Passed = rep.Summary.Total == 0
	return rep
}

// ── R1: dangling copper stub ────────────────────────────────────────────────
// A track endpoint that anchors to nothing — no pad, no via, no other track —
// is an unfinished / floating copper stub (a routing artifact). Endpoints that
// join a pad center, a via, or any other track (endpoint OR mid-body T-junction)
// are anchored.
func findDanglingEnds(tracks []pcbTrack, vias []pcbViaP, pads []pcbPadP, arcs []pcbArc) []pcbCheckFinding {
	var out []pcbCheckFinding
	seen := map[[2]int64]bool{} // dedup by rounded point — one finding per free node
	for i, t := range tracks {
		for _, ep := range [][2]float64{{t.X1, t.Y1}, {t.X2, t.Y2}} {
			px, py := ep[0], ep[1]
			if anchored(px, py, i, t.Layer, t.Net, tracks, vias, pads, arcs) {
				continue
			}
			k := [2]int64{int64(math.Round(px * 100)), int64(math.Round(py * 100))}
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, pcbCheckFinding{
				Type: "dangling-end", Level: "WARN", Net: t.Net, Layer: t.Layer,
				Primitives: []string{t.ID}, At: &pcbXY{round2(px), round2(py)},
				Message: "track end connects to nothing (no pad/via/track) — unfinished or floating copper",
			})
		}
	}
	return out
}

// anchored reports whether a track endpoint at (px,py) on copper layer `layer`
// is electrically continued: a pad whose copper touches the endpoint/track stroke
// (foreign pads only at the exact center — a near-miss on a foreign pad is NOT a
// connection), a via, or ANOTHER track passing through it ON THE SAME
// LAYER. A different-layer track crossing the XY is NOT a connection without a
// via, so it must not anchor the stub.
func anchored(px, py float64, self, layer int, net string, tracks []pcbTrack, vias []pcbViaP, pads []pcbPadP, arcs []pcbArc) bool {
	trackRadius := pcbCoincEps
	if self >= 0 && self < len(tracks) && tracks[self].Width > 0 {
		trackRadius += tracks[self].Width / 2
	}
	for _, p := range pads {
		if pcbPadAnchorsPoint(p, px, py, layer, net, trackRadius) {
			return true
		}
	}
	for _, v := range vias {
		// Same-net vias anchor by AREA (endpoint anywhere inside the via copper),
		// not just the exact center — probe round #1 briefly false-flagged stubs
		// that EasyEDA's own DRC accepted because they ended inside the via pad
		// but off-center. Foreign vias keep the strict center epsilon (a track
		// ending on a foreign via is a short, not an anchor — but at the exact
		// center we still treat it as "not unfinished copper", the historical
		// behavior). A same-net track↔via junction DOES conduct (pro-api-sdk#31
		// was our misdiagnosis — verified live 2026-07-07); DRC reports any real
		// open once ratline/pour connectivity is recomputed.
		tol := pcbCoincEps
		if net != "" && v.Net == net {
			tol = v.Dia/2 + pcbCoincEps
		}
		if math.Hypot(v.X-px, v.Y-py) <= tol {
			return true
		}
	}
	for j, o := range tracks {
		if j == self || o.Layer != layer {
			continue
		}
		if segPtDist(px, py, o.X1, o.Y1, o.X2, o.Y2) <= pcbCoincEps {
			return true
		}
	}
	// A copper ARC on the same layer whose endpoint meets this point continues the
	// trace — beautify (走线美化) rounds a corner into track→arc→track, so the track
	// terminates exactly on the arc endpoint. Without this, every rounded corner
	// fabricates two false "dangling" stubs (EasyEDA's own DRC reports 0 opens).
	for _, a := range arcs {
		if a.Layer != layer {
			continue
		}
		if math.Hypot(a.X1-px, a.Y1-py) <= pcbCoincEps || math.Hypot(a.X2-px, a.Y2-py) <= pcbCoincEps {
			return true
		}
	}
	return false
}

// pcbPadAnchorsPoint is fail-closed for pad copper. New connectors expose the
// native shape, so rotated RECT/rounded RECT and OVAL use their real geometry and
// ELLIPSE/NGON use the net-path conservative geometry. Legacy connectors expose
// only width/height: at cardinal rotations an axis-aligned ellipse lies inside
// RECT/OVAL/ELLIPSE native pads; at other rotations an inscribed circle avoids
// treating an AABB corner as copper. With no size, only center coincidence counts.
func pcbPadAnchorsPoint(p pcbPadP, px, py float64, layer int, net string, contactRadius float64) bool {
	if !padLayerMatches(p.Layer, layer) {
		return false
	}
	if net == "" || p.Net != net {
		return math.Hypot(p.X-px, p.Y-py) <= pcbCoincEps
	}
	if p.ShapeOK {
		pad := pcbNetPathNode{
			kind: "pad", net: p.Net, layer: p.Layer, x: p.X, y: p.Y,
			w: p.ShapeW, h: p.ShapeH, rotation: p.Rotation, shape: p.Shape,
			shapeRound: p.ShapeRound, shapeSides: p.ShapeSides,
		}
		return pointTouchesPad(px, py, pad, contactRadius)
	}
	if p.W <= 0 || p.H <= 0 {
		return math.Hypot(p.X-px, p.Y-py) <= pcbCoincEps
	}
	dx, dy := px-p.X, py-p.Y
	if netPathCardinalRotation(p.Rotation) {
		return netPathEllipsePointDistance(math.Abs(dx), math.Abs(dy), p.W/2, p.H/2) <= contactRadius+netPathGeomEps
	}
	return math.Hypot(dx, dy) <= math.Min(p.W, p.H)/2+contactRadius+netPathGeomEps
}

func netPathCardinalRotation(rotation float64) bool {
	r := math.Mod(rotation, 90)
	if r < 0 {
		r += 90
	}
	return r <= netPathGeomEps || 90-r <= netPathGeomEps
}

// Exact Euclidean distance from a first-quadrant point to an axis-aligned
// ellipse. Zero means inside. The monotonic Lagrange multiplier solve is bounded
// and deterministic; it is used only for legacy pad anchoring.
func netPathEllipsePointDistance(x, y, a, b float64) float64 {
	if a <= 0 || b <= 0 {
		return math.Inf(1)
	}
	if x*x/(a*a)+y*y/(b*b) <= 1 {
		return 0
	}
	f := func(t float64) float64 {
		return (a*x/(t+a*a))*(a*x/(t+a*a)) + (b*y/(t+b*b))*(b*y/(t+b*b)) - 1
	}
	lo, hi := 0.0, math.Max(a*x, b*y)
	if hi <= 0 {
		return 0
	}
	for f(hi) > 0 {
		hi *= 2
	}
	for i := 0; i < 80; i++ {
		mid := (lo + hi) / 2
		if f(mid) > 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	t := hi
	qx := a * a * x / (t + a*a)
	qy := b * b * y / (t + b*b)
	return math.Hypot(x-qx, y-qy)
}

// ── R2: acute-angle (acid-trap) corner ──────────────────────────────────────
// Where two same-net, same-layer segments meet at a shared vertex, the interior
// angle between them in (pcbAcuteMinDeg, 90°) forms a sharp spike where etchant
// collects. A branch/terminal vertex inside exact same-net pad copper is exempt:
// the pad already fills the wedge, so there is no etched acute pocket. 90° and
// 45° (135° interior) corners are fine; ≤5° is collinear overlap
// (duplicate-segment), not a corner.
//
// Known scope limits (deliberate — low value / false-positive risk, tracked by the
// DFM audit wf_9afc4dbe-b08): only EXACT shared vertices are compared, so a branch
// teeing off mid-trunk (T-junction) and two endpoints coincident within pcbCoincEps
// but not exactly are not evaluated. Routed copper meets at exact vertices, so this
// covers the real cases.
func findAcuteAngles(tracks []pcbTrack, pads []pcbPadP) []pcbCheckFinding {
	type inc struct {
		dx, dy float64
		id     string
	}
	type vtx struct {
		net   string
		layer int
		x, y  float64
		segs  []inc
	}
	// bucket incident segment directions by net|layer|point (0.01 mil grid). The
	// key is only an identity handle — metadata lives on the vtx so we never parse
	// the key back (Go's fmt has no %[...] scanset).
	buckets := map[string]*vtx{}
	key := func(net string, layer int, x, y float64) string {
		return fmt.Sprintf("%s|%d|%d|%d", net, layer, int64(math.Round(x*100)), int64(math.Round(y*100)))
	}
	add := func(net string, layer int, x, y, dx, dy float64, id string) {
		k := key(net, layer, x, y)
		v := buckets[k]
		if v == nil {
			v = &vtx{net: net, layer: layer, x: x, y: y}
			buckets[k] = v
		}
		v.segs = append(v.segs, inc{dx, dy, id})
	}
	for _, t := range tracks {
		add(t.Net, t.Layer, t.X1, t.Y1, t.X2-t.X1, t.Y2-t.Y1, t.ID)
		add(t.Net, t.Layer, t.X2, t.Y2, t.X1-t.X2, t.Y1-t.Y2, t.ID)
	}
	// deterministic order
	ks := make([]string, 0, len(buckets))
	for k := range buckets {
		ks = append(ks, k)
	}
	sort.Strings(ks)

	var out []pcbCheckFinding
	for _, k := range ks {
		v := buckets[k]
		if len(v.segs) < 2 {
			continue
		}
		if acuteVertexInsideSameNetPad(v.x, v.y, v.net, v.layer, pads) {
			continue
		}
		minAng := 999.0
		var ids []string
		for a := 0; a < len(v.segs); a++ {
			for b := a + 1; b < len(v.segs); b++ {
				ang := angleBetween(v.segs[a].dx, v.segs[a].dy, v.segs[b].dx, v.segs[b].dy)
				if ang < minAng {
					minAng = ang
					ids = uniqStr([]string{v.segs[a].id, v.segs[b].id})
				}
			}
		}
		// (pcbAcuteMinDeg, 90°): a real acid-trap corner. ≤5° is collinear/overlapping
		// copper (no corner) — duplicate-segment covers that; 0.5° tol so a clean 90° never trips.
		if minAng > pcbAcuteMinDeg && minAng < pcbAcuteDeg-0.5 {
			out = append(out, pcbCheckFinding{
				Type: "acute-angle", Level: "WARN", Net: v.net, Layer: v.layer,
				Primitives: ids, AngleDeg: round2(minAng),
				At:      &pcbXY{round2(v.x), round2(v.y)},
				Message: fmt.Sprintf("trace bends %.0f° (<90°) — acid-trap acute angle", minAng) + docRule("4.1", "转角禁直角/锐角"),
			})
		}
	}
	return out
}

// acuteVertexInsideSameNetPad uses only source-backed pad geometry. An unknown
// shape, a legacy width/height approximation, or a point merely inside a rotated
// pad's axis-aligned bbox cannot suppress a DFM finding.
func acuteVertexInsideSameNetPad(x, y float64, net string, layer int, pads []pcbPadP) bool {
	if strings.TrimSpace(net) == "" {
		return false
	}
	for _, p := range pads {
		if !p.ShapeOK || p.Net != net || !padLayerMatches(p.Layer, layer) {
			continue
		}
		pad := pcbNetPathNode{
			kind: "pad", net: p.Net, layer: p.Layer, x: p.X, y: p.Y,
			w: p.ShapeW, h: p.ShapeH, rotation: p.Rotation, shape: p.Shape,
			shapeRound: p.ShapeRound, shapeSides: p.ShapeSides,
		}
		if pointTouchesPad(x, y, pad, 0) {
			return true
		}
	}
	return false
}

// ── R3: non-orthogonal (free-angle) trace ───────────────────────────────────
// Good routing runs on the 45° grid — segments at 0/45/90/135°. A track at an
// arbitrary angle (e.g. a lazy pad-to-pad diagonal) is a free-angle route: harder
// to inspect, and often a sign the router/hand-route skipped a proper corner. We
// flag any single segment whose heading is >1° off the nearest multiple of 45°.
// withoutTeardrops drops line-shaped teardrops (pcb teardrop-plan): a PAIR of
// short tracks that start on the same same-net pad and end within 2 mil of each
// other at shallow angles (a thin pad gives a pair only a few degrees apart). The host splits the reinforced track at the tips, so
// "ends on another track's interior" does not survive a create; the pair does.
func withoutTeardrops(tracks []pcbTrack, pads []pcbPadP) []pcbTrack {
	type entry struct {
		pad      int
		fx, fy   float64 // far end
		dx, dy   float64
		eligible bool
	}
	es := make([]entry, len(tracks))
	for i, t := range tracks {
		es[i].pad = -1
		if t.Net == "" {
			continue
		}
		L := math.Hypot(t.X2-t.X1, t.Y2-t.Y1)
		for j, p := range pads {
			if L == 0 || L > 3*math.Max(p.W, p.H) {
				continue
			}
			if pcbPadAnchorsPoint(p, t.X1, t.Y1, t.Layer, t.Net, 0) && !pcbPadAnchorsPoint(p, t.X2, t.Y2, t.Layer, t.Net, 0) {
				es[i] = entry{pad: j, fx: t.X2, fy: t.Y2, dx: t.X2 - t.X1, dy: t.Y2 - t.Y1, eligible: true}
				break
			}
			if pcbPadAnchorsPoint(p, t.X2, t.Y2, t.Layer, t.Net, 0) && !pcbPadAnchorsPoint(p, t.X1, t.Y1, t.Layer, t.Net, 0) {
				es[i] = entry{pad: j, fx: t.X1, fy: t.Y1, dx: t.X1 - t.X2, dy: t.Y1 - t.Y2, eligible: true}
				break
			}
		}
	}
	// A piece whose far end runs straight on into another same-net track is the
	// reinforced track itself (split by the host at a tip), not a teardrop.
	for i, e := range es {
		if !e.eligible {
			continue
		}
		for k, o := range tracks {
			if k == i || o.Net != tracks[i].Net || o.Layer != tracks[i].Layer {
				continue
			}
			var odx, ody float64
			switch {
			case math.Hypot(o.X1-e.fx, o.Y1-e.fy) <= pcbCoincEps:
				odx, ody = o.X2-o.X1, o.Y2-o.Y1
			case math.Hypot(o.X2-e.fx, o.Y2-e.fy) <= pcbCoincEps:
				odx, ody = o.X1-o.X2, o.Y1-o.Y2
			default:
				continue
			}
			if angleBetween(e.dx, e.dy, odx, ody) < 1 { // e points into the joint, o away: straight on = 0°
				es[i].eligible = false
				break
			}
		}
	}
	drop := make([]bool, len(tracks))
	for i := range tracks {
		for j := i + 1; j < len(tracks); j++ {
			a, b := es[i], es[j]
			if !a.eligible || !b.eligible || a.pad != b.pad || tracks[i].Net != tracks[j].Net || tracks[i].Layer != tracks[j].Layer {
				continue
			}
			if math.Hypot(a.fx-b.fx, a.fy-b.fy) > 2 {
				continue
			}
			if ang := angleBetween(a.dx, a.dy, b.dx, b.dy); ang > 1 && ang < 90 {
				drop[i], drop[j] = true, true
			}
		}
	}
	out := make([]pcbTrack, 0, len(tracks))
	for i, t := range tracks {
		if !drop[i] {
			out = append(out, t)
		}
	}
	return out
}

func findNonOrthogonal(tracks []pcbTrack) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, t := range tracks {
		ang := math.Atan2(t.Y2-t.Y1, t.X2-t.X1) * 180 / math.Pi
		if ang < 0 {
			ang += 180 // heading is mod 180 (a segment has no direction)
		}
		r := math.Mod(ang, 45) // distance to nearest 45° multiple
		off := math.Min(r, 45-r)
		if off > pcbOrthoTolDeg {
			out = append(out, pcbCheckFinding{
				Type: "non-orthogonal", Level: "WARN", Net: t.Net, Layer: t.Layer,
				Primitives: []string{t.ID}, AngleDeg: round2(ang),
				At:      &pcbXY{round2((t.X1 + t.X2) / 2), round2((t.Y1 + t.Y2) / 2)},
				Message: fmt.Sprintf("trace runs at %.1f° — not on the 0/45/90° grid (free-angle routing)", ang) + docRule("4.1", "推荐45°折线或圆弧"),
			})
		}
	}
	return out
}

// ── R4: track-over-pad (crossing a pad it doesn't terminate on) ──────────────
// A track whose body passes directly over a pad center that is NOT one of its own
// endpoints. On the SAME copper layer this is either a hard short (the pad is on a
// different net) or sloppy routing (same net — the track should have terminated on
// the pad, not run through it). Different-layer pads are ignored (a top track over
// a bottom SMD pad is fine); through-hole cross-layer shorts are a known blind spot
// (pad layer is reported as a single side here).
func findTrackOverPad(tracks []pcbTrack, pads []pcbPadP) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, t := range tracks {
		for _, p := range pads {
			if p.Layer != t.Layer {
				continue
			}
			// pad is an endpoint of this track → legitimate termination, skip.
			if math.Hypot(p.X-t.X1, p.Y-t.Y1) <= pcbCoincEps || math.Hypot(p.X-t.X2, p.Y-t.Y2) <= pcbCoincEps {
				continue
			}
			if segPtDist(p.X, p.Y, t.X1, t.Y1, t.X2, t.Y2) > pcbOverPadEps {
				continue
			}
			padRef := p.Designator
			if p.Number != "" {
				padRef += "." + p.Number
			}
			if p.Net != t.Net {
				out = append(out, pcbCheckFinding{
					Type: "track-over-pad", Level: "ERROR", Net: t.Net, Layer: t.Layer,
					Designator: p.Designator, Primitives: []string{t.ID},
					At:      &pcbXY{round2(p.X), round2(p.Y)},
					Message: fmt.Sprintf("track (net %s) crosses over pad %s (net %s) on the same layer — short circuit", t.Net, padRef, p.Net) + docRule("1", "线宽与间距"),
				})
			} else {
				out = append(out, pcbCheckFinding{
					Type: "track-over-pad", Level: "WARN", Net: t.Net, Layer: t.Layer,
					Designator: p.Designator, Primitives: []string{t.ID},
					At:      &pcbXY{round2(p.X), round2(p.Y)},
					Message: fmt.Sprintf("track passes through same-net pad %s instead of terminating on it", padRef) + docRule("1", "线宽与间距"),
				})
			}
		}
	}
	return out
}

// ── clearance: a track runs closer than the spacing rule to another net ──────
// The headless twin of EasyEDA DRC's 间距错误 (Safe Spacing) — a track passing
// within `clearance` of an OTHER net's pad, via, or track is a short-in-waiting.
// track-over-pad owns the exact overlap (a track crossing a pad center); this
// owns the "too close but not on top" band and the track↔via / track↔track cases
// that overlap-only checks miss, so `pcb check` can gate on shorts WITHOUT the
// foreground-only native DRC. Pads use their REAL half-extent when the connector
// reports width/height (halfExt(), nominal fallback for old connectors / polygon
// pads); vias use their real outer radius. Capped so a wall of violations can't
// drown the report — the count says how many were cut.
func findClearanceViolations(tracks []pcbTrack, pads []pcbPadP, vias []pcbViaP, slots []pcbSlotP, clearance, trackTrackClearance float64) []pcbCheckFinding {
	if trackTrackClearance <= 0 {
		trackTrackClearance = clearance // compatibility for callers without a pair-specific rule
	}
	const cap = 40
	var out []pcbCheckFinding
	dropped := 0
	add := func(f pcbCheckFinding) {
		if len(out) < cap {
			out = append(out, f)
		} else {
			dropped++
		}
	}
	isEnd := func(x, y, x1, y1, x2, y2 float64) bool {
		return math.Hypot(x-x1, y-y1) <= pcbCoincEps || math.Hypot(x-x2, y-y2) <= pcbCoincEps
	}
	for _, t := range tracks {
		if strings.TrimSpace(t.Net) == "" {
			continue
		}
		for _, p := range pads {
			if p.Layer != t.Layer || p.Net == t.Net || strings.TrimSpace(p.Net) == "" {
				continue
			}
			if isEnd(p.X, p.Y, t.X1, t.Y1, t.X2, t.Y2) {
				continue // legitimate termination
			}
			centerD := segPtDist(p.X, p.Y, t.X1, t.Y1, t.X2, t.Y2)
			if centerD <= pcbOverPadEps {
				continue // over-pad short is track-over-pad's
			}
			// Edge distance: with a real extent the pad is its axis-aligned RECT —
			// a radial max(W,H)/2 would false-flag a track running legitimately
			// beside an elongated pad (USB-C 1.0mm pads: half-length 19.7mil ≫
			// half-width 5.9mil, seen on ceshi). Nominal radius only as fallback.
			var edgeD float64
			if p.W > 0 && p.H > 0 {
				edgeD = rectSegDist(p.X-p.W/2, p.Y-p.H/2, p.X+p.W/2, p.Y+p.H/2, t.X1, t.Y1, t.X2, t.Y2)
			} else {
				edgeD = centerD - nominalPadHalf
			}
			if edgeD >= clearance {
				continue
			}
			ref := p.Designator
			if p.Number != "" {
				ref += "." + p.Number
			}
			add(pcbCheckFinding{
				Type: "clearance", Level: "ERROR", Net: t.Net, Layer: t.Layer,
				Designator: p.Designator, Primitives: []string{t.ID},
				At:      &pcbXY{round2(p.X), round2(p.Y)},
				Message: fmt.Sprintf("track (net %s) runs %.1fmil from pad %s (net %s) — under the %.0fmil spacing rule", t.Net, math.Max(edgeD, 0), ref, p.Net, clearance) + docRule("1.1", "最小间距"),
			})
		}
		for _, v := range vias {
			if v.Net == t.Net || strings.TrimSpace(v.Net) == "" {
				continue
			}
			if isEnd(v.X, v.Y, t.X1, t.Y1, t.X2, t.Y2) {
				continue
			}
			// EDGE distance (copper-to-copper): center distance minus the via's
			// outer radius AND the track's half-width. Printing the CENTER
			// distance made the message self-contradictory ("runs 16.9mil …
			// under the 6mil rule" — #43 regression); the gap a human/agent
			// must widen is the edge gap.
			edgeD := segPtDist(v.X, v.Y, t.X1, t.Y1, t.X2, t.Y2) - v.Dia/2 - t.Width/2
			if edgeD >= clearance {
				continue
			}
			add(pcbCheckFinding{
				Type: "clearance", Level: "ERROR", Net: t.Net, Layer: t.Layer,
				Primitives: []string{t.ID, v.ID}, At: &pcbXY{round2(v.X), round2(v.Y)},
				Message: fmt.Sprintf("track (net %s) runs %.1fmil from via (net %s) — under the %.0fmil spacing rule", t.Net, math.Max(edgeD, 0), v.Net, clearance) + docRule("1.1", "最小间距"),
			})
		}
	}
	// track ↔ track (different net, same layer)
	for i := 0; i < len(tracks); i++ {
		for j := i + 1; j < len(tracks); j++ {
			a, b := tracks[i], tracks[j]
			if a.Layer != b.Layer || a.Net == b.Net || strings.TrimSpace(a.Net) == "" || strings.TrimSpace(b.Net) == "" {
				continue
			}
			// EDGE gap: centerline distance minus BOTH half-widths. The old
			// centerline test under-reported (two 10mil tracks 8mil apart on
			// centerlines already OVERLAP, yet printed "8mil apart") — #43.
			//
			// NO pcbOverPadEps escape here (#43 R2): the track↔pad branch hands
			// its overlap band to the track-over-pad rule, but NOTHING owns
			// track↔track overlap — a `centerD > eps` guard let two tracks that
			// actually CROSS (centerD == 0, a dead short) skip the check while
			// flagging the near-misses beside them. Crossing copper is the worst
			// case, so it must always report.
			centerD := segSegDist(a.X1, a.Y1, a.X2, a.Y2, b.X1, b.Y1, b.X2, b.Y2)
			if edgeD := centerD - a.Width/2 - b.Width/2; edgeD < trackTrackClearance {
				mx, my := (a.X1+a.X2)/2, (a.Y1+a.Y2)/2
				add(pcbCheckFinding{
					Type: "clearance", Level: "ERROR", Nets: uniqStr([]string{a.Net, b.Net}), Layer: a.Layer,
					Primitives: []string{a.ID, b.ID}, At: &pcbXY{round2(mx), round2(my)},
					Message: fmt.Sprintf("tracks (net %s / %s) run %.1fmil apart — under the %.0fmil track-to-track spacing rule", a.Net, b.Net, math.Max(edgeD, 0), trackTrackClearance) + docRule("1.1", "最小间距"),
				})
			}
		}
	}
	// via ↔ pad (a via is through-hole — it clashes with a pad on ANY layer) and
	// via ↔ via (different nets; same-net stacking is overlapping-via's rule).
	for _, v := range vias {
		if strings.TrimSpace(v.Net) == "" {
			continue
		}
		for _, p := range pads {
			if p.Net == v.Net || strings.TrimSpace(p.Net) == "" {
				continue
			}
			// Rect model for real-extent pads (see track↔pad above) — a radial
			// max-half would false-flag vias beside elongated connector pads.
			var d float64
			if p.W > 0 && p.H > 0 {
				d = rectPtDist(p.X-p.W/2, p.Y-p.H/2, p.X+p.W/2, p.Y+p.H/2, v.X, v.Y) - v.Dia/2
			} else {
				d = math.Hypot(v.X-p.X, v.Y-p.Y) - v.Dia/2 - nominalPadHalf
			}
			if d < clearance {
				ref := p.Designator
				if p.Number != "" {
					ref += "." + p.Number
				}
				add(pcbCheckFinding{
					Type: "clearance", Level: "ERROR", Nets: uniqStr([]string{v.Net, p.Net}),
					Designator: p.Designator, Primitives: []string{v.ID},
					At:      &pcbXY{round2(v.X), round2(v.Y)},
					Message: fmt.Sprintf("via (net %s) sits ~%.1fmil from pad %s (net %s) — under the %.0fmil spacing rule", v.Net, math.Max(d, 0), ref, p.Net, clearance) + docRule("1.1", "最小间距"),
				})
			}
		}
	}
	for i := 0; i < len(vias); i++ {
		for j := i + 1; j < len(vias); j++ {
			a, b := vias[i], vias[j]
			if a.Net == b.Net {
				continue
			}
			if d := math.Hypot(a.X-b.X, a.Y-b.Y) - a.Dia/2 - b.Dia/2; d < clearance {
				add(pcbCheckFinding{
					Type: "clearance", Level: "ERROR", Nets: uniqStr([]string{a.Net, b.Net}),
					Primitives: []string{a.ID, b.ID}, At: &pcbXY{round2(a.X), round2(a.Y)},
					Message: fmt.Sprintf("vias (net %s / %s) sit ~%.1fmil apart — under the %.0fmil spacing rule", a.Net, b.Net, math.Max(d, 0), clearance) + docRule("1.1", "最小间距"),
				})
			}
		}
	}
	// slot (board cutout, e.g. an M3 hole) ↔ track / via — the mill removes
	// material on every layer; JLC wants ≥ max(rule, 8mil) copper-to-cutout.
	slotClr := math.Max(clearance, 8)
	for _, s := range slots {
		for _, t := range tracks {
			if strings.TrimSpace(t.Net) == "" {
				continue
			}
			if d := rectSegDist(s.MinX, s.MinY, s.MaxX, s.MaxY, t.X1, t.Y1, t.X2, t.Y2) - t.Width/2; d < slotClr {
				mx, my := (t.X1+t.X2)/2, (t.Y1+t.Y2)/2
				add(pcbCheckFinding{
					Type: "clearance", Level: "ERROR", Net: t.Net, Layer: t.Layer,
					Primitives: []string{s.ID, t.ID}, At: &pcbXY{round2(mx), round2(my)},
					Message: fmt.Sprintf("track (net %s) runs ~%.1fmil from a board cutout/slot — under the %.0fmil copper-to-cutout rule", t.Net, math.Max(d, 0), slotClr) + docRule("1.1", "最小间距"),
				})
			}
		}
		for _, v := range vias {
			if strings.TrimSpace(v.Net) == "" {
				continue
			}
			if d := rectPtDist(s.MinX, s.MinY, s.MaxX, s.MaxY, v.X, v.Y) - v.Dia/2; d < slotClr {
				add(pcbCheckFinding{
					Type: "clearance", Level: "ERROR", Net: v.Net,
					Primitives: []string{s.ID, v.ID}, At: &pcbXY{round2(v.X), round2(v.Y)},
					Message: fmt.Sprintf("via (net %s) sits ~%.1fmil from a board cutout/slot — under the %.0fmil copper-to-cutout rule", v.Net, math.Max(d, 0), slotClr) + docRule("1.1", "最小间距"),
				})
			}
		}
	}
	if dropped > 0 {
		out = append(out, pcbCheckFinding{
			Type: "clearance", Level: "ERROR",
			Message: fmt.Sprintf("+%d more clearance violation(s) not listed (capped at %d) — run `pcb drc` for the full set", dropped, cap),
		})
	}
	return out
}

// ── R5: via issues — stacked/redundant + pointless single-layer ─────────────
func findViaIssues(tracks []pcbTrack, vias []pcbViaP) []pcbCheckFinding {
	var out []pcbCheckFinding

	// stacked/overlapping vias
	for i := 0; i < len(vias); i++ {
		for j := i + 1; j < len(vias); j++ {
			if math.Hypot(vias[i].X-vias[j].X, vias[i].Y-vias[j].Y) <= pcbViaDupEps {
				nets := uniqStr([]string{vias[i].Net, vias[j].Net})
				out = append(out, pcbCheckFinding{
					Type: "overlapping-via", Level: "WARN", Nets: nets,
					Primitives: []string{vias[i].ID, vias[j].ID},
					At:         &pcbXY{round2(vias[i].X), round2(vias[i].Y)},
					Message:    "two vias occupy the same spot — stacked/redundant" + docRule("2", "过孔设计"),
				})
			}
		}
	}

	// single-layer / dangling via: a via exists to change layers. If the tracks
	// touching it live on fewer than 2 copper layers it serves no purpose. Skip
	// power/GND nets — those are stitching/plane vias that connect to a pour
	// (not a track), so a single touching layer is legitimate.
	for _, v := range vias {
		if isGlobalNet(v.Net) {
			continue
		}
		layers := map[int]bool{}
		for _, t := range tracks {
			if t.Net != v.Net { // a foreign net's track merely crossing the XY isn't served by this via
				continue
			}
			if segPtDist(v.X, v.Y, t.X1, t.Y1, t.X2, t.Y2) <= pcbCoincEps {
				layers[t.Layer] = true
			}
		}
		if len(layers) < 2 {
			out = append(out, pcbCheckFinding{
				Type: "single-layer-via", Level: "WARN", Net: v.Net,
				Primitives: []string{v.ID}, At: &pcbXY{round2(v.X), round2(v.Y)},
				Message: fmt.Sprintf("signal via touches tracks on %d layer(s) — no layer transition (pointless or dangling)", len(layers)) + docRule("2", "过孔设计"),
			})
		}
	}
	return out
}

// ── R6: 2-pin neck-down asymmetry ───────────────────────────────────────────
// A discrete 2-pad part (R/C/L/diode) whose two pads have noticeably different
// entering track widths — asymmetric neck-down, usually an oversight.
func findWidthMismatch(tracks []pcbTrack, pads []pcbPadP) []pcbCheckFinding {
	byDesig := map[string][]pcbPadP{}
	order := []string{}
	for _, p := range pads {
		if _, ok := byDesig[p.Designator]; !ok {
			order = append(order, p.Designator)
		}
		byDesig[p.Designator] = append(byDesig[p.Designator], p)
	}
	sort.Strings(order)

	var out []pcbCheckFinding
	for _, d := range order {
		ps := byDesig[d]
		if len(ps) != 2 {
			continue
		}
		w0, ok0 := maxTrackWidthAt(ps[0].X, ps[0].Y, ps[0].Net, tracks)
		w1, ok1 := maxTrackWidthAt(ps[1].X, ps[1].Y, ps[1].Net, tracks)
		if !ok0 || !ok1 {
			continue
		}
		diff := math.Abs(w0 - w1)
		tol := math.Max(pcbWidthTolMil, pcbWidthTolRel*math.Max(w0, w1))
		if diff > tol {
			out = append(out, pcbCheckFinding{
				Type: "width-mismatch", Level: "INFO", Designator: d, Net: ps[0].Net,
				Widths:  []float64{round2(w0), round2(w1)},
				Message: fmt.Sprintf("2-pin part %s: entering track widths differ (%.1f vs %.1f mil)", d, w0, w1) + docRule("1.2", "推荐线宽"),
			})
		}
	}
	return out
}

// maxTrackWidthAt is the widest track of net `net` whose endpoint lands on the pad
// at (px,py). Restricting to the pad's OWN net keeps an unrelated track that merely
// crosses the pad's XY (e.g. on another layer) from inflating the width — a track
// entering a pad necessarily carries that pad's net.
func maxTrackWidthAt(px, py float64, net string, tracks []pcbTrack) (float64, bool) {
	best := 0.0
	found := false
	for _, t := range tracks {
		if t.Net != net {
			continue
		}
		if math.Hypot(t.X1-px, t.Y1-py) <= pcbCoincEps || math.Hypot(t.X2-px, t.Y2-py) <= pcbCoincEps {
			if t.Width > best {
				best = t.Width
			}
			found = true
		}
	}
	return best, found
}

// ── R7: duplicated / overlapping copper ─────────────────────────────────────
// Two same-net, same-layer collinear segments whose overlap exceeds a tolerance
// — redundant double copper (a router artifact), mergeable.
func findDuplicateSegments(tracks []pcbTrack) []pcbCheckFinding {
	var out []pcbCheckFinding
	for i := 0; i < len(tracks); i++ {
		for j := i + 1; j < len(tracks); j++ {
			a, b := tracks[i], tracks[j]
			if a.Net != b.Net || a.Layer != b.Layer {
				continue
			}
			// collinearOverlap tests b's endpoints against a's line, so it's asymmetric:
			// a short segment sitting on a long slightly-angled one is only detected with
			// the LONGER segment as the reference. Order it so the result is stable.
			if segLen(b) > segLen(a) {
				a, b = b, a
			}
			if ov, ok := collinearOverlap(a, b); ok && ov > pcbDupOverlap {
				out = append(out, pcbCheckFinding{
					Type: "duplicate-segment", Level: "WARN", Net: a.Net, Layer: a.Layer,
					Primitives: []string{a.ID, b.ID},
					Message:    fmt.Sprintf("collinear overlapping copper (%.0f mil overlap) — redundant/mergeable", ov),
				})
			}
		}
	}
	return out
}

// collinearOverlap returns the overlap length of two segments if they are
// collinear (both of b's endpoints lie on a's infinite line), else ok=false.
func collinearOverlap(a, b pcbTrack) (float64, bool) {
	dx, dy := a.X2-a.X1, a.Y2-a.Y1
	la := math.Hypot(dx, dy)
	if la < 1e-9 {
		return 0, false
	}
	// b endpoints must be on a's line
	if pointLineDist(b.X1, b.Y1, a) > pcbOnSegEps || pointLineDist(b.X2, b.Y2, a) > pcbOnSegEps {
		return 0, false
	}
	// project all four endpoints onto a's unit direction
	ux, uy := dx/la, dy/la
	proj := func(x, y float64) float64 { return (x-a.X1)*ux + (y-a.Y1)*uy }
	a0, a1 := 0.0, la
	b0, b1 := proj(b.X1, b.Y1), proj(b.X2, b.Y2)
	if b0 > b1 {
		b0, b1 = b1, b0
	}
	lo := math.Max(math.Min(a0, a1), b0)
	hi := math.Min(math.Max(a0, a1), b1)
	if hi <= lo {
		return 0, false
	}
	return hi - lo, true
}

// ── R8: 3W parallel-coupling ────────────────────────────────────────────────
// Two DIFFERENT-net, same-layer segments running near-parallel with a
// center-to-center gap below couplingW×maxWidth, over a meaningful overlap, are
// a crosstalk / manufacturing-spacing risk (the classic 3W rule). Same-net pairs
// (intentional) and power/GND (poured, not coupled tracks) are skipped.
//
// So are the two members of a DECLARED differential pair: running them tightly
// coupled is the entire point of a pair, and the 3W rule is about a pair (or a
// single net) against OTHER nets — never about the gap inside one pair. Without
// this, a correctly routed pair is told to un-route exactly what the constraint
// asked for, and no legal layout can clear the warning. Pair members are still
// checked against every other net.
func findParallelCoupling(tracks []pcbTrack, couplingW float64, diffPairs []pcbDiffPair) []pcbCheckFinding {
	var out []pcbCheckFinding
	for i := 0; i < len(tracks); i++ {
		a := tracks[i]
		if isGlobalNet(a.Net) {
			continue
		}
		for j := i + 1; j < len(tracks); j++ {
			b := tracks[j]
			if a.Net == b.Net || a.Layer != b.Layer || isGlobalNet(b.Net) {
				continue
			}
			if isDeclaredDiffPair(a.Net, b.Net, diffPairs) {
				continue // intra-pair coupling is the design intent, not a defect
			}
			gap, ovlp, ok := parallelGap(a, b)
			if !ok || ovlp < pcbCouplingMinOvlp {
				continue
			}
			need := couplingW * math.Max(a.Width, b.Width)
			if gap < need {
				na, nb := a.Net, b.Net
				if nb < na {
					na, nb = nb, na
				}
				out = append(out, pcbCheckFinding{
					Type: "parallel-coupling", Level: "WARN", Nets: []string{na, nb}, Layer: a.Layer,
					Primitives: []string{a.ID, b.ID},
					Message: fmt.Sprintf("parallel traces %.1f mil apart over %.0f mil (< %.1f mil = %.0f×W) — crosstalk/3W risk",
						gap, ovlp, need, couplingW) + docRule("4.2", "减少串扰"),
				})
			}
		}
	}
	return out
}

// ── R9: silkscreen orientation (flipped / back-side text) ───────────────────
// A silkscreen text is "放反" (placed reversed) when it can't be read from the side
// it belongs to. Two independent failure modes, both ERROR (a mirrored designator
// is a real, ship-stopping artwork defect):
//
//  1. side mismatch — a component's designator sits on the OPPOSITE silk layer from
//     its footprint (component on TOP but its designator on BOTTOM_SILKSCREEN, or
//     vice-versa). The label ends up on the wrong side of the board.
//
//  2. mirror/reverse — the text renders backwards. Only the UNAMBIGUOUS half is
//     judged here:
//
//     - `reverse` (left/right reading) is always a defect, on either silk layer:
//     it mirrors glyph order regardless of which side the text is on.
//     - `mirror` on TOP silk is a defect — the top is the reference reading
//     orientation, so mirroring it can only render backwards.
//
//     `mirror` on BOTTOM silk is deliberately NOT judged. The platform's bottom
//     semantics are unverified and the in-repo sources disagree, so any rule here
//     would either misreport or contradict our own tooling:
//
//     - All seven vendored reference boards (testdata/boards/*.json — JLCEDA
//     open-source boards plus a shipped user design) carry mirror=false on every
//     bottom-silk text: 1814 of 1814, attributes and free strings alike. The
//     rule's original polarity (bottom must be mirrored) fired ~1800 false
//     ERRORs across them, yet bbclaw ships mirror=true on 10 bottom designators,
//     so the inverted polarity does not clear them all either.
//     - `pcb silk-align` (extension/src/actions.ts) deliberately sets
//     `mirror = (component side == bottom)` on the designators it writes, and
//     retries WITHOUT mirror/layer when modify rejects them. Judging bottom
//     mirror either way makes this audit flag the writer's own output.
//
//     A false ERROR on a ship gate is worse than a missed cosmetic one, and a text
//     that is merely mis-positioned is invisible to this check regardless (it only
//     reads the text's own flags). Re-enable a bottom rule once the platform
//     semantics are settled against a known-good board.
func findSilkscreenFlipped(silk []pcbSilkText) []pcbCheckFinding {
	sideName := func(l int) string {
		switch l {
		case pcbSideTop, silkTopLayer:
			return "top"
		case pcbSideBottom, silkBottomLayer:
			return "bottom"
		}
		return "?"
	}
	var out []pcbCheckFinding
	for _, s := range silk {
		if s.Layer != silkTopLayer && s.Layer != silkBottomLayer {
			continue // not a silkscreen text
		}
		label := s.Text
		if label == "" {
			label = s.ID
		}
		compOwned := s.Kind == "attribute" && (s.CompLayer == pcbSideTop || s.CompLayer == pcbSideBottom)
		// 1. designator on the wrong silk side vs its component.
		if compOwned {
			wantSilk := silkTopLayer
			if s.CompLayer == pcbSideBottom {
				wantSilk = silkBottomLayer
			}
			if s.Layer != wantSilk {
				out = append(out, pcbCheckFinding{
					Type: "silkscreen-flipped", Level: "ERROR", Layer: s.Layer, Designator: label,
					Primitives: []string{s.ID}, At: &pcbXY{round2(s.X), round2(s.Y)},
					Message: fmt.Sprintf("designator '%s' is on the %s silkscreen but its component sits on the %s side — silkscreen flipped to the wrong side",
						label, sideName(s.Layer), sideName(s.CompLayer)) + docRule("11.2", "位号清晰可辨"),
				})
				continue
			}
		}
		// 2. reverse is always backwards; a mirrored TOP text is too. Bottom-side
		//    mirror is not judged — see the doc comment above.
		if s.Reverse || (s.Mirror && s.Layer == silkTopLayer) {
			reason := "reversed"
			if s.Mirror {
				reason = "mirrored"
			}
			out = append(out, pcbCheckFinding{
				Type: "silkscreen-flipped", Level: "ERROR", Layer: s.Layer, Designator: label,
				Primitives: []string{s.ID}, At: &pcbXY{round2(s.X), round2(s.Y)},
				Message: fmt.Sprintf("silkscreen text '%s' on the %s silk is %s — it reads backwards (放反)",
					label, sideName(s.Layer), reason) + docRule("11.2", "丝印须清晰可辨"),
			})
			continue
		}
		// 3. designator ORIENTATION: a reference designator (位号) should read UPRIGHT
		//    (0°). Rotated = upside-down (180°) or sideways (90/270°) — awkward to read
		//    on the assembled board. WARN (readable, but non-standard); designators only.
		if s.Kind == "attribute" && s.Key == "Designator" {
			if rot := normDeg(s.Rotation); rot != 0 {
				ori := "sideways (侧向)"
				if rot == 180 {
					ori = "upside-down (上下颠倒)"
				}
				out = append(out, pcbCheckFinding{
					Type: "silkscreen-flipped", Level: "WARN", Layer: s.Layer, Designator: label,
					Primitives: []string{s.ID}, At: &pcbXY{round2(s.X), round2(s.Y)},
					Message: fmt.Sprintf("designator '%s' is rotated %g° (%s) — not upright (放反/朝向不正)", label, rot, ori) + docRule("11.2", "位号清晰可辨"),
				})
			}
		}
	}
	return out
}

// ── R11: netless copper pour ────────────────────────────────────────────────
// A copper pour with an empty net is DEAD copper — it connects to nothing yet
// occupies board area (issue #34: a net:"" layer-1 pour left by `pcb pour`
// without --net). It's confusing (looks like a real plane), and `pour-fit
// --replace` can't clear it because that only matches same-net pours. Flag every
// pour whose net is empty so it can be removed with `pcb pour-clean --netless`.

type pcbPourP struct {
	ID    string
	Net   string
	Layer int
}

func findNetlessPours(pours []pcbPourP) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, p := range pours {
		if strings.TrimSpace(p.Net) != "" {
			continue
		}
		f := pcbCheckFinding{
			Type: "netless-pour", Level: "WARN", Layer: p.Layer,
			Message: "netless copper pour (dead copper — bound to no net); remove with `pcb pour-clean --netless` or re-pour with a net",
		}
		if p.ID != "" {
			f.Primitives = []string{p.ID}
		}
		out = append(out, f)
	}
	return out
}

// ── R12: via crossing an inner PLANE (内电层) on a foreign net ──────────────
// Official defect easyeda/pro-api-sdk#32: once an inner layer is 内电层/PLANE, a
// via created AFTERWARDS on a different net does NOT get an anti-pad cut into
// the negative plane — DRC reports Plane Zone to Via + Hole to Plane Zone, and
// `pour-rebuild` alone does not repair it. The via list exposes no anti-pad or
// creation-order data, so this is a BEST-EFFORT guard: every via whose net
// differs from the plane's net is flagged WARN with the fix guidance. Known
// edges: a via placed BEFORE the plane flip (proper anti-pad, clean DRC) is
// flagged too — cross-check with `pcb drc`, only vias that also show Plane Zone
// errors are actually broken; blind/buried vias that don't reach the plane are
// indistinguishable from through vias (all flagged); a PLANE layer with no
// net-bound pour has an unknown net and gets its own INFO instead of via checks
// (INFO, not WARN — see the #110 note in findViaCrossesPlane).

// pcbPlaneLayer is one inner copper layer of type PLANE (内电层), plus the
// net(s) bound to it via its pour(s).
type pcbPlaneLayer struct {
	Layer int
	Name  string
	Nets  []string
}

// bindPlaneNets attaches each plane's net(s) from the pours on its layer
// (netless pours don't bind — R11 flags those separately).
func bindPlaneNets(planes []pcbPlaneLayer, pours []pcbPourP) []pcbPlaneLayer {
	for i := range planes {
		seen := map[string]bool{}
		for _, p := range pours {
			net := strings.TrimSpace(p.Net)
			if p.Layer != planes[i].Layer || net == "" || seen[net] {
				continue
			}
			seen[net] = true
			planes[i].Nets = append(planes[i].Nets, net)
		}
		sort.Strings(planes[i].Nets)
	}
	return planes
}

func findViaCrossesPlane(vias []pcbViaP, planes []pcbPlaneLayer) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, pl := range planes {
		name := pl.Name
		if name == "" {
			name = fmt.Sprintf("layer %d", pl.Layer)
		}
		if len(pl.Nets) == 0 {
			// #110: INFO, not WARN. After `doc reload`, a pour stored on a PLANE
			// (内电层) layer is loaded into the negative-plane store, NOT the Pour
			// primitive store — eda.pcb_PrimitivePour.getAll() (pcb.pour.list)
			// returns nothing for that layer, and no extension API exposes it
			// (probed live: getAll(net)/getAll(undefined,layer), PrimitiveFill,
			// PrimitiveRegion, cross-probe-select by net, physical stacking config
			// all come back empty; flipping the layer back to SIGNAL + reload makes
			// the same pours reappear, proving the data is intact but invisible).
			// So "no net-bound pour" cannot distinguish a genuinely empty plane
			// from a healthy plane read after reload → downgrade to INFO so
			// --strict doesn't gate on it, and point at native DRC as the arbiter.
			out = append(out, pcbCheckFinding{
				Type: "via-crosses-plane", Level: "INFO", Layer: pl.Layer,
				Message: fmt.Sprintf("inner PLANE %s has no net-bound pour visible — plane net unknown; NOTE (#110): after `doc reload` PLANE-layer pours are invisible to pcb.pour.list, so this may be a false positive — treat `pcb drc` Connection=0 as the arbiter before adding any pour; if the plane is genuinely empty, pour the net while the layer is still SIGNAL, then flip to PLANE and pour-rebuild (`pcb power-planes` does this)", name),
			})
			continue
		}
		netSet := map[string]bool{}
		for _, n := range pl.Nets {
			netSet[n] = true
		}
		for _, v := range vias {
			if netSet[v.Net] {
				continue
			}
			vnet := v.Net
			if strings.TrimSpace(vnet) == "" {
				vnet = "(no net)"
			}
			out = append(out, pcbCheckFinding{
				Type: "via-crosses-plane", Level: "WARN", Net: v.Net, Layer: pl.Layer,
				Primitives: []string{v.ID}, At: &pcbXY{round2(v.X), round2(v.Y)},
				Message: fmt.Sprintf("via (net %s) crosses inner PLANE %s (net %s) — a via created after the plane existed gets NO anti-pad (easyeda/pro-api-sdk#32; DRC: Plane Zone to Via / Hole to Plane Zone); prefer removing it and routing on outer layers, or `easyeda doc reload` then `pcb pour-rebuild`, then confirm with `pcb drc`",
					vnet, name, strings.Join(pl.Nets, ",")),
			})
		}
	}
	return out
}

// fetchPcbPlaneLayers reads the stackup (pcb.layers.list) and returns the
// copper layers whose type is PLANE (内电层). Net binding comes from the pours
// (bindPlaneNets) — the layer item itself carries no net.
func fetchPcbPlaneLayers(cfg *appConfig, window string) ([]pcbPlaneLayer, error) {
	res, err := requestAction(cfg, "pcb.layers.list", window, nil)
	if err != nil {
		return nil, err
	}
	var planes []pcbPlaneLayer
	for _, rl := range mnavSlice(res.Result, "layers") {
		lm, ok := rl.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := lm["type"].(string); typ != "PLANE" {
			continue
		}
		idf, _ := asFloatOK(lm["id"])
		name, _ := lm["name"].(string)
		planes = append(planes, pcbPlaneLayer{Layer: int(idf), Name: name})
	}
	return planes, nil
}

func fetchPcbPours(cfg *appConfig, window string) ([]pcbPourP, error) {
	res, err := requestAction(cfg, "pcb.pour.list", window, nil)
	if err != nil {
		return nil, err
	}
	rawPours, _ := mnav(res.Result, "pours").([]any)
	var pours []pcbPourP
	for _, rp := range rawPours {
		pm, ok := rp.(map[string]any)
		if !ok {
			continue
		}
		id, _ := pm["primitiveId"].(string)
		net, _ := pm["net"].(string)
		layer, _ := asFloatOK(pm["layer"])
		pours = append(pours, pcbPourP{ID: id, Net: net, Layer: int(layer)})
	}
	return pours, nil
}

// ── R13: power net not carried by copper pour/plane (能力 B / 电源走铺铜块) ──
// Power/ground nets should distribute current through a copper POUR or inner
// PLANE, not thin tracks (design-flow P7.0; thin power tracks are the #1 DRC
// source — design-decisions.md measured 18/27 Safe-Spacing violations from six
// thin 3V3 tracks). Flags every power net (isGlobalNet, incl. GND) with ≥2 pads
// that has NO same-net pour and is bound to NO PLANE — its current is carried by
// tracks alone (or is unrouted). Single-pad nets (test points) are skipped.
//
// blindPlanes is the count of inner PLANE layers whose net binding is UNKNOWN —
// after a doc reload the platform stores PLANE-layer pours in negative-image
// form with no extension-API read path (#110), so such a layer very likely
// carries the missing pour (the power-planes recipe puts GND there). When
// blindPlanes > 0 a GND-class net is downgraded to INFO with a message that
// points at `pcb drc` as the arbiter instead of suggesting `pcb power-planes` —
// the command that poured the invisible plane in the first place (#117's
// deadlock). Non-GND power nets keep WARN: the recipe leaves them on SIGNAL
// layers where their pours stay visible.
func findPowerNotPoured(pads []pcbPadP, pouredNets map[string]bool, blindPlanes int) []pcbCheckFinding {
	padCount := map[string]int{}
	for _, p := range pads {
		n := strings.TrimSpace(p.Net)
		if n == "" || !isGlobalNet(n) {
			continue
		}
		padCount[n]++
	}
	nets := make([]string, 0, len(padCount))
	for n := range padCount {
		nets = append(nets, n)
	}
	sort.Strings(nets)
	var out []pcbCheckFinding
	for _, n := range nets {
		if padCount[n] < 2 || pouredNets[n] {
			continue
		}
		if blindPlanes > 0 && isGndNetName(n) {
			out = append(out, pcbCheckFinding{
				Type: "power-not-poured", Level: "INFO", Net: n,
				Message: fmt.Sprintf("power net %s (%d pads) has no VISIBLE pour — but the board carries %d inner PLANE layer(s) with unknown net: PLANE-layer pours are invisible to pcb.pour.list after a doc reload (#110), so this is most likely that pour; treat `pcb drc` Connection=0 as the arbiter — do NOT re-run `pcb power-planes` just to clear this finding (#117)", n, padCount[n], blindPlanes),
			})
			continue
		}
		fix := fmt.Sprintf("`pcb pour-fit --net %s` (2-layer) or `pcb power-planes` (4-layer)", n)
		out = append(out, pcbCheckFinding{
			Type: "power-not-poured", Level: "WARN", Net: n,
			Message: fmt.Sprintf("power net %s (%d pads) has no copper pour/plane — power should be poured, not carried by thin tracks: %s", n, padCount[n], fix) + docRule("5.2", "铺铜策略"),
		})
	}
	return out
}

// ── R15: netless via inside a net-carrying pad (#118 / #120) ────────────────
// A footprint-embedded via (QFN EPAD thermal via) always re-materializes with
// net:"" after a doc reload — it then never bonds to the plane and native DRC
// fires one same-footprint "SMD Pad to Via" per via. It cannot be deleted
// either (#120). The repair is `pcb via-bond`, and this rule is the tripwire
// that says when to (re-)run it. Detection reuses the via-bond planner so the
// check and the fix agree exactly on what qualifies.
func findNetlessViaInPad(pads []pcbPadP, vias []pcbViaP) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, a := range planViaBond(pads, vias, "") {
		out = append(out, pcbCheckFinding{
			Type: "netless-via-in-pad", Level: "WARN", Net: a.Net,
			Designator: a.Designator, Primitives: []string{a.ViaID},
			At: &pcbXY{round2(a.X), round2(a.Y)},
			Message: fmt.Sprintf("netless via inside pad %s.%s (net %s) — an embedded (EPAD thermal) via that never bonds to its plane and trips native DRC; fix `easyeda pcb via-bond` (re-run after every doc reload — the platform re-materializes these netless, live-verified #118)",
				a.Designator, a.PadNumber, a.Net),
		})
	}
	return out
}

// pouredNetSet returns the set of nets whose current is delivered by copper area:
// any net bound to a same-net pour or to an inner PLANE. Pass planes already run
// through bindPlaneNets so their Nets are populated.
func pouredNetSet(pours []pcbPourP, planes []pcbPlaneLayer) map[string]bool {
	set := map[string]bool{}
	for _, p := range pours {
		if n := strings.TrimSpace(p.Net); n != "" {
			set[n] = true
		}
	}
	for _, pl := range planes {
		for _, n := range pl.Nets {
			set[n] = true
		}
	}
	return set
}

// ── R14: power track under its net-class spec width (能力 A / 规范线宽) ──────
// A routed power track thinner than its net-class spec (pcb_netclass.go: branch 10
// / trunk 15 / high-current 20 / gnd) is under-sized for the current it carries.
// One aggregated finding per net (thinnest offender + count) caps noise. Exempts
// (a) fine-pitch narrowing — a track pinned to a fine-pitch pad field, where
// narrowing to the legal minimum is the only way to clear spacing — and (b) short
// via-stitch escape stubs. Signal nets are NOT checked (their spec is the live
// default and fine-pitch narrowing is legitimate). `widths` is netClassWidthTable.
func findWidthUnderSpec(tracks []pcbTrack, pads []pcbPadP, vias []pcbViaP, widths map[string]float64) []pcbCheckFinding {
	if len(widths) == 0 {
		return nil
	}
	obPads := make([]obPad, 0, len(pads))
	for _, p := range pads {
		obPads = append(obPads, obPad{net: strings.TrimSpace(p.Net), x: p.X, y: p.Y, layer: p.Layer, half: math.Max(p.W, p.H) / 2})
	}
	type offender struct {
		count    int
		thinnest float64
		spec     float64
		at       pcbXY
		prim     string
	}
	byNet := map[string]*offender{}
	var order []string
	for _, t := range tracks {
		net := strings.TrimSpace(t.Net)
		role := netRole(net)
		if role == roleSignal { // signals exempt (spec == live default; fine-pitch legit)
			continue
		}
		spec, ok := widths[role]
		if !ok || spec <= 0 || t.Width >= spec-pcbWidthTolMil {
			continue
		}
		// Legitimately-narrow exemptions.
		if finePitchAt(t.X1, t.Y1, net, obPads, 26) || finePitchAt(t.X2, t.Y2, net, obPads, 26) {
			continue
		}
		if trackIsStitchStub(t, vias) {
			continue
		}
		o := byNet[net]
		if o == nil {
			o = &offender{thinnest: math.Inf(1), spec: spec}
			byNet[net] = o
			order = append(order, net)
		}
		o.count++
		if t.Width < o.thinnest {
			o.thinnest = t.Width
			o.at = pcbXY{round2((t.X1 + t.X2) / 2), round2((t.Y1 + t.Y2) / 2)}
			o.prim = t.ID
		}
	}
	sort.Strings(order)
	var out []pcbCheckFinding
	for _, n := range order {
		o := byNet[n]
		f := pcbCheckFinding{
			Type: "width-under-spec", Level: "WARN", Net: n,
			Widths: []float64{round2(o.thinnest), round2(o.spec)},
			At:     &pcbXY{o.at.X, o.at.Y},
			Message: fmt.Sprintf("power net %s: %d track(s) below the %s spec width %.3g mil (thinnest %.3g mil) — widen for current capacity (route-short sizes by role; pour it instead, or --width-power to override)",
				n, o.count, netRole(n), o.spec, o.thinnest) + docRule("1.2", "推荐线宽"),
		}
		if o.prim != "" {
			f.Primitives = []string{o.prim}
		}
		out = append(out, f)
	}
	return out
}

// trackIsStitchStub reports whether a short track is a via-connection escape stub
// (one endpoint on a same-net via, length ≤ a stub threshold) — legitimately
// narrow, so width-under-spec should not flag it.
func trackIsStitchStub(t pcbTrack, vias []pcbViaP) bool {
	const stubMaxLen = 60.0 // mil — route-short stub setback ≈30; stitch stubs are short
	if math.Hypot(t.X2-t.X1, t.Y2-t.Y1) > stubMaxLen {
		return false
	}
	for _, v := range vias {
		if v.Net != t.Net {
			continue
		}
		if math.Hypot(v.X-t.X1, v.Y-t.Y1) <= pcbCoincEps || math.Hypot(v.X-t.X2, v.Y-t.Y2) <= pcbCoincEps {
			return true
		}
	}
	return false
}

// ── R10: antenna keep-out ───────────────────────────────────────────────────
// A component that carries an RF antenna (an ESP WROOM/WROVER module, or a part
// named/designated as an antenna) needs a NO-COPPER keep-out under/around its
// antenna — copper (pour/plane/track) there detunes the antenna. We flag an
// antenna component whose computed radiator rectangle is not fully protected.
// A bbox intersection, one copper rule or a missing geometry read cannot clear it.

type pcbAntComp struct {
	Designator    string
	Device        string
	BBox          *layoutBBox
	RequiredRect  *[4]float64
	RequiredRules []int
	Incomplete    string
}

type pcbKeepRegion struct {
	PrimitiveID       string
	BBox              *layoutBBox
	Layer             int // TOP=1 / BOTTOM=2 / MULTI=12 / actual inner id
	RuleTypes         []int
	Source            any
	GeometryAvailable bool
	Name              string
}

// isAntennaDevice reports whether a device name / designator indicates an
// antenna-bearing part (integrated-antenna modules + discrete antennas).
func isAntennaDevice(device, designator string) bool {
	d := strings.ToUpper(device)
	// "2450AT" (Johanson 2.4GHz ceramic chip family) and "ANT-SMD" (the LCSC
	// footprint prefix for discrete SMD antennas) catch two-pad chip antennas
	// whose auto-assigned designator isn't ANT* (#123: AE1 on the live board).
	for _, kw := range []string{"WROOM", "WROVER", "ANTENNA", "ESP32-C3-MINI", "ESP8266", "2450AT", "ANT-SMD"} {
		if strings.Contains(d, kw) {
			return true
		}
	}
	u := strings.ToUpper(designator)
	return strings.HasPrefix(u, "ANT")
}

// findAntennaKeepout uses the same pad-free strip/chip rectangle as creation.
// Chip antennas keep rules 6/7 without rule 5: their RF feed must enter (#129).
func findAntennaKeepout(ants []pcbAntComp, regions []pcbKeepRegion, copperLayers int) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, a := range ants {
		if a.Incomplete != "" || a.RequiredRect == nil || !antennaValidRect(*a.RequiredRect) {
			reason := a.Incomplete
			if reason == "" {
				reason = "antenna rectangle unavailable"
			}
			out = append(out, pcbCheckFinding{Type: "antenna-keepout-incomplete", Level: "WARN", Designator: a.Designator,
				Message: fmt.Sprintf("antenna component %s (%s): incomplete keep-out check (%s)", a.Designator, a.Device, reason)})
			continue
		}
		missing := antennaCoverageMissing(*a.RequiredRect, a.RequiredRules, regions, copperLayers)
		if len(missing) > 0 {
			dev := a.Device
			if dev == "" {
				dev = "antenna part"
			}
			out = append(out, pcbCheckFinding{
				Type: "antenna-keepout", Level: "WARN", Designator: a.Designator,
				Message: fmt.Sprintf("antenna component %s (%s) lacks complete polygon/rule coverage on: %s — copper there detunes the antenna (每层都要禁铺铜)",
					a.Designator, dev, strings.Join(missing, ", ")) + docRule("5.3", "天线下禁止铺铜"),
			})
		}
	}
	return out
}

// normDeg normalizes an angle to [0,360), rounded to a whole degree (kills float
// noise like 449.99 → 90).
func normDeg(d float64) float64 {
	r := math.Mod(math.Round(d), 360)
	if r < 0 {
		r += 360
	}
	return r
}

// parallelGap reports, for two near-parallel segments, their center-to-center
// perpendicular gap and the length over which they run parallel (their overlap
// projected onto a's direction). ok=false when they are not parallel.
func parallelGap(a, b pcbTrack) (gap, overlap float64, ok bool) {
	adx, ady := a.X2-a.X1, a.Y2-a.Y1
	bdx, bdy := b.X2-b.X1, b.Y2-b.Y1
	la, lb := math.Hypot(adx, ady), math.Hypot(bdx, bdy)
	if la < 1e-9 || lb < 1e-9 {
		return 0, 0, false
	}
	ang := angleBetween(adx, ady, bdx, bdy)
	if ang > pcbParallelDeg && ang < 180-pcbParallelDeg {
		return 0, 0, false // not parallel nor anti-parallel
	}
	// unit direction of a; project b's endpoints + a's endpoints onto it.
	ux, uy := adx/la, ady/la
	proj := func(x, y float64) float64 { return (x-a.X1)*ux + (y-a.Y1)*uy }
	a0, a1 := 0.0, la
	b0, b1 := proj(b.X1, b.Y1), proj(b.X2, b.Y2)
	if b0 > b1 {
		b0, b1 = b1, b0
	}
	lo := math.Max(math.Min(a0, a1), b0)
	hi := math.Min(math.Max(a0, a1), b1)
	overlap = hi - lo
	if overlap <= 0 {
		return 0, 0, false // parallel but don't run alongside each other
	}
	// gap = the MINIMUM separation over the overlap, not the midpoint. Within the 15°
	// parallel window traces can DIVERGE (a wedge): nearly touching at one end, far at
	// the other. The midpoint would miss that — the tight end is the real coupling risk.
	// Sample a's line across the overlap and take the closest approach to b's segment.
	const samples = 16
	gap = math.Inf(1)
	for k := 0; k <= samples; k++ {
		t := lo + (hi-lo)*float64(k)/float64(samples)
		amx, amy := a.X1+ux*t, a.Y1+uy*t
		if d := segPtDist(amx, amy, b.X1, b.Y1, b.X2, b.Y2); d < gap {
			gap = d
		}
	}
	return gap, overlap, true
}

// segLen is a segment's length.
func segLen(t pcbTrack) float64 { return math.Hypot(t.X2-t.X1, t.Y2-t.Y1) }

// ── geometry helpers ────────────────────────────────────────────────────────

// segPtDist is the distance from (px,py) to segment (ax,ay)-(bx,by). Unlike the
// axis-aligned pointSegDist in cmd_sch_autoconnect.go, this handles arbitrary
// (e.g. 45°) segments, which routed copper needs.
func segPtDist(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	if l2 < 1e-12 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / l2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

// pointLineDist is the perpendicular distance from (px,py) to a's infinite line.
func pointLineDist(px, py float64, a pcbTrack) float64 {
	dx, dy := a.X2-a.X1, a.Y2-a.Y1
	l := math.Hypot(dx, dy)
	if l < 1e-9 {
		return math.Hypot(px-a.X1, py-a.Y1)
	}
	return math.Abs((px-a.X1)*dy-(py-a.Y1)*dx) / l
}

// angleBetween returns the angle in degrees [0,180] between two vectors.
func angleBetween(ax, ay, bx, by float64) float64 {
	la, lb := math.Hypot(ax, ay), math.Hypot(bx, by)
	if la < 1e-9 || lb < 1e-9 {
		return 180
	}
	c := (ax*bx + ay*by) / (la * lb)
	if c > 1 {
		c = 1
	} else if c < -1 {
		c = -1
	}
	return math.Acos(c) * 180 / math.Pi
}

func uniqStr(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ── live fetch / render ──────────────────────────────────────────────────────

// runPcbCheck pulls placed copper (tracks + vias + pads), runs the DFM audit,
// renders it, and (with strict) returns a non-zero exit when there are findings.
func runPcbCheck(cfg *appConfig, window string, couplingW float64, checkSpec *spec.Spec, strict, asJSON bool, stdout, stderr io.Writer) error {
	rep, err := gatherPcbCheckReport(cfg, window, couplingW, checkSpec, stderr)
	if err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		renderPcbCheckReport(*rep, stdout)
	}

	// Warnings historically includes ERROR findings. Check Errors as well so a
	// live-only rule cannot accidentally bypass strict mode via counter drift.
	blocking := rep.Summary.Warnings
	if rep.Summary.Errors > blocking {
		blocking = rep.Summary.Errors
	}
	if strict && blocking > 0 {
		return fmt.Errorf("pcb check: %d issue(s) (--strict)", blocking)
	}
	if strict && rep.Incomplete {
		return fmt.Errorf("pcb check incomplete: required pad inventory/geometry was not verified (--strict)")
	}
	return nil
}

// gatherPcbCheckReport runs the full DFM audit (core + every LIVE-only rule)
// and returns the report — the reusable seam `workflow advance` drives for the
// post_route_checked gate (布完必查), while `pcb check` keeps owning rendering
// and --strict semantics.
func gatherPcbCheckReport(cfg *appConfig, window string, couplingW float64, checkSpec *spec.Spec, stderr io.Writer) (*pcbCheckReport, error) {
	rules, ruleErr := fetchPcbRules(cfg, window)
	if ruleErr != nil {
		return nil, ruleErr
	}
	pads, coverage, err := fetchPcbPadsWithCoverage(cfg, window)
	if err != nil {
		return nil, fmt.Errorf("fetch PCB pads: %w", err)
	}
	tracks, err := fetchPcbTracks(cfg, window)
	if err != nil {
		return nil, fmt.Errorf("fetch PCB tracks: %w", err)
	}
	// Arcs anchor track endpoints (beautify's rounded corners). Best-effort: an older
	// connector omits them → empty slice, dangling-end just loses arc-awareness.
	arcs, aerr := fetchPcbArcs(cfg, window)
	if aerr != nil {
		fmt.Fprintf(stderr, "warning: arc fetch failed, dangling-end check runs without arc-awareness (%v)\n", aerr)
		arcs = nil
	}
	vias, err := fetchPcbVias(cfg, window)
	if err != nil {
		return nil, fmt.Errorf("fetch PCB vias: %w", err)
	}
	// Silkscreen is OPTIONAL: the silk rule needs the pcb.silk.list connector handler.
	// On an older connector (before it was added) this errors "Unknown action" — degrade
	// gracefully (run the copper rules, skip silk) instead of failing the whole audit.
	silk, err := fetchPcbSilk(cfg, window)
	if err != nil {
		fmt.Fprintf(stderr, "warning: silkscreen-flipped check skipped (%v) — update the connector to enable it\n", err)
		silk = nil
	}
	// Declared differential pairs (#176) tell the 3W rule which tight coupling is
	// intentional. Best-effort, like arcs and silk: an older connector without
	// pcb.constraint.list just loses pair-awareness — the previous behaviour.
	diffPairs, derr := fetchPcbDiffPairs(cfg, window)
	if derr != nil {
		fmt.Fprintf(stderr, "warning: differential-pair constraints unavailable (%v) — the 3W rule cannot tell an intentional pair from crosstalk\n", derr)
		diffPairs = nil
	}

	rep := analyzePcbCheckFull(pads, tracks, vias, arcs, silk, couplingW, diffPairs)
	rep.Coverage = coverage
	rep.Incomplete = !coverage.Complete
	rep.Limitations = append(rep.Limitations, coverage.Limitations...)
	if aerr != nil {
		rep.Incomplete = true
		rep.Limitations = append(rep.Limitations, "copper arc inventory unavailable: "+aerr.Error())
	}

	// Clearance is a LIVE rule — it needs the board's live spacing value. Flags
	// copper running under the spacing rule against another net's pad/via/track
	// and against board cutouts (slots): the headless twin of native DRC's
	// 间距错误, so `pcb check --strict` gates the low-level shorts WITHOUT the
	// foreground-only native DRC.
	clearance := rules.clearanceMil
	if clearance <= 0 {
		clearance = 6
	}
	slots, serr := fetchPcbSlots(cfg, window)
	if serr != nil {
		fmt.Fprintf(stderr, "warning: clearance check runs without slot/cutout data (%v)\n", serr)
		slots = nil
	}
	for _, f := range findClearanceViolations(tracks, pads, vias, slots, clearance, rules.clearanceTrackTrackMil) {
		rep.Findings = append(rep.Findings, f)
		rep.Summary.Clearance++
		rep.Summary.Errors++
		rep.Summary.Warnings++
		rep.Summary.Total++
	}
	// R15 (#118): netless embedded vias re-materialize on every reload, so this
	// tripwire fires whenever a re-bond is due (fix: `pcb via-bond`).
	for _, f := range findNetlessViaInPad(pads, vias) {
		rep.Findings = append(rep.Findings, f)
		rep.Summary.NetlessViaInPad++
		rep.Summary.Warnings++
		rep.Summary.Total++
	}
	rep.Passed = rep.Summary.Total == 0

	// Copper-near-edge is a LIVE-only rule (needs the board outline). The floor is
	// the live copper-to-edge rule (fallback: JLC routed-edge 8mil, doc §5.1
	// recommends ~20mil/0.5mm — we gate on the fab floor, the doc value is advice).
	if ores, oerr := requestAction(cfg, "pcb.outline.get", window, nil); oerr != nil {
		fmt.Fprintf(stderr, "warning: copper-near-edge check skipped (%v)\n", oerr)
	} else if bb, ok := mnav(ores.Result, "bbox").(map[string]any); ok {
		minX, ok1 := asFloatOK(bb["minX"])
		minY, ok2 := asFloatOK(bb["minY"])
		maxX, ok3 := asFloatOK(bb["maxX"])
		maxY, ok4 := asFloatOK(bb["maxY"])
		if ok1 && ok2 && ok3 && ok4 {
			outline := &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
			edgeClr := rules.copperToEdgeMil
			if edgeClr <= 0 {
				edgeClr = 8
			}
			for _, f := range findCopperNearEdge(tracks, vias, outline, edgeClr) {
				rep.Findings = append(rep.Findings, f)
				rep.Summary.CopperNearEdge++
				rep.Summary.Warnings++
				rep.Summary.Total++
			}
			rep.Passed = rep.Summary.Total == 0
		}
	}

	// Zone-violation is a LIVE-only rule (issue #126): claimed parts must sit in
	// their S0 functional zone's board sub-rect. Only runs when the project has
	// zone claims (`pcb zones set`); degrades gracefully on any fetch failure.
	if zones, _, zerr := loadZoneClaims(cfg, window); zerr == nil && len(zones) > 0 {
		if board, berr := fetchBoardRect(cfg, window); berr != nil {
			fmt.Fprintf(stderr, "warning: zone-violation check skipped (%v)\n", berr)
		} else if parts, perr := fetchZoneParts(cfg, window); perr != nil {
			fmt.Fprintf(stderr, "warning: zone-violation check skipped (%v)\n", perr)
		} else {
			for _, f := range findZoneViolations(zones, board, parts) {
				rep.Findings = append(rep.Findings, f)
				if f.Type == "zone-claims-stale" {
					rep.Summary.ZoneClaimsStale++
				} else {
					rep.Summary.ZoneViolation++
				}
				rep.Summary.Warnings++
				rep.Summary.Total++
			}
			rep.Passed = rep.Summary.Total == 0
		}
	}

	// 连接器布局规则（issue #168）：internal-on-edge + connector-plug-clearance。
	// 同为 LIVE-only —— 它们要的是**器件级**几何（bbox / 到板框距离 / 设备名），
	// 而纯核只吃焊盘和铜箔。两条共用一次板级快照，不各拉一遍。
	//
	// 意图来源：优先读 --spec 指定的 S0 spec（连接器 facing/internal 显式声明），
	// 没有就退回启发式并把 finding 降到 INFO —— 推定错了不该像显式标注那样理直气壮
	// 地拦人（docs/concepts.md 的三层置信度表）。
	if connSnap, cerr := fetchBoardSnapshot(cfg, window, boardSnapshotOpts{}); cerr != nil {
		fmt.Fprintf(stderr, "warning: connector layout checks skipped (%v)\n", cerr)
	} else {
		conns := collectBoardConnectors(connSnap, checkSpec)
		for _, f := range findInternalOnEdge(conns, connSnap.Outline) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.InternalOnEdge++
			if f.Level != "INFO" {
				rep.Summary.Warnings++
			}
			rep.Summary.Total++
		}
		for _, f := range findConnectorPlugClearance(conns, connSnap.Outline) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.ConnectorPlugClearance++
			if f.Level != "INFO" {
				rep.Summary.Warnings++
			}
			rep.Summary.Total++
		}
		// 插拔通道禁布（mating corridor）：与上两条共用同一次连接器判读。
		for _, f := range findConnectorMatingBlocked(conns, connSnap.Components, connSnap.Outline) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.ConnectorMatingBlocked++
			if f.Level != "INFO" {
				rep.Summary.Warnings++
			}
			rep.Summary.Total++
		}
		// 贴边插口开口朝内（#231）：同一次判读，与规则③互补（通道空着也要报）。
		for _, f := range findConnectorOpeningInward(conns) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.ConnectorOpeningInward++
			rep.Summary.Warnings++
			rep.Summary.Total++
		}
		rep.Passed = rep.Summary.Total == 0
	}

	// Missing antenna geometry/layers is an incomplete check, not an empty pass.
	if ants, regions, copperLayers, aerr := fetchAntennaContext(cfg, window, silk); aerr != nil {
		rep.Incomplete = true
		rep.Limitations = append(rep.Limitations, "antenna keep-out check incomplete: "+aerr.Error())
		fmt.Fprintf(stderr, "warning: antenna keep-out check incomplete (%v)\n", aerr)
	} else {
		for _, f := range findAntennaKeepout(ants, regions, copperLayers) {
			if f.Type == "antenna-keepout-incomplete" {
				rep.Incomplete = true
				rep.Limitations = append(rep.Limitations, f.Message)
			}
			rep.Findings = append(rep.Findings, f)
			rep.Summary.AntennaKeepout++
			rep.Summary.Warnings++
			rep.Summary.Total++
		}
		rep.Passed = rep.Summary.Total == 0
	}

	// Visible silk outside the board outline is not printed (live 2026-10-13:
	// U1's designator sat above the board edge while layout-lint reported 0
	// off-board, since it only judges pads). Unreadable outline → incomplete.
	if board, drawn, oerr := fetchPcbOutlineBBox(cfg, window); oerr != nil {
		rep.Incomplete = true
		rep.Limitations = append(rep.Limitations, "silk-off-board check incomplete: "+oerr.Error())
	} else if !drawn {
		rep.Limitations = append(rep.Limitations, "silk-off-board not judged: no board outline drawn yet")
	} else {
		for _, f := range findSilkOffBoard(silk, board) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.SilkOffBoard++
			rep.Summary.Warnings++
			rep.Summary.Total++
		}
		rep.Passed = rep.Summary.Total == 0
	}

	// Netless-pour + via-crosses-plane + floating-track-island + power-not-poured
	// + width-under-spec are LIVE-only rules (they need the pour lists / the
	// stackup / the live width ladder, which the pure copper core doesn't take).
	// Degrade gracefully if a fetch fails.
	if pours, perr := fetchPcbPours(cfg, window); perr != nil {
		fmt.Fprintf(stderr, "warning: pour/plane + power-distribution checks skipped (%v)\n", perr)
	} else {
		// Planes (bound to their nets) feed both via-crosses-plane and the
		// poured-net set that power-not-poured / width-under-spec share.
		planes, lerr := fetchPcbPlaneLayers(cfg, window)
		if lerr != nil {
			fmt.Fprintf(stderr, "warning: plane data unavailable (%v)\n", lerr)
			planes = nil
		}
		planes = bindPlaneNets(planes, pours)
		pouredNets := pouredNetSet(pours, planes)

		for _, f := range findNetlessPours(pours) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.NetlessPours++
			rep.Summary.Warnings++
			rep.Summary.Total++
		}
		if len(planes) > 0 {
			for _, f := range findViaCrossesPlane(vias, planes) {
				rep.Findings = append(rep.Findings, f)
				rep.Summary.ViaCrossesPlane++
				// #110: the plane-net-unknown finding is INFO (a reload artifact,
				// not a defect) — it stays in the summary/total but must not trip
				// --strict, which gates on Summary.Warnings.
				if f.Level != "INFO" {
					rep.Summary.Warnings++
				}
				rep.Summary.Total++
			}
		}
		for _, f := range findFloatingTrackIslands(tracks, vias, pads, pours, arcs) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.FloatingIslands++
			rep.Summary.Warnings++
			rep.Summary.Total++
		}
		// power-not-poured (能力 B) + width-under-spec (能力 A) — both WARN, both
		// gate under --strict. Share pouredNets so a thin stitch stub on a poured
		// net is exempt. blindPlanes = PLANE layers whose pour is platform-invisible
		// after reload (#110) — GND findings degrade to INFO then (#117), and like
		// the via-crosses-plane INFO they stay out of Summary.Warnings (--strict).
		blindPlanes := 0
		for _, pl := range planes {
			if len(pl.Nets) == 0 {
				blindPlanes++
			}
		}
		for _, f := range findPowerNotPoured(pads, pouredNets, blindPlanes) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.PowerNotPoured++
			if f.Level != "INFO" {
				rep.Summary.Warnings++
			}
			rep.Summary.Total++
		}
		for _, f := range findWidthUnderSpec(tracks, pads, vias, netClassWidthTable(rules)) {
			rep.Findings = append(rep.Findings, f)
			rep.Summary.WidthUnderSpec++
			rep.Summary.Warnings++
			rep.Summary.Total++
		}
		rep.Passed = rep.Summary.Total == 0
	}

	rep.Passed = rep.Summary.Total == 0 && !rep.Incomplete
	rep.Limitations = uniqueStrings(rep.Limitations)
	return &rep, nil
}

func fetchPcbPads(cfg *appConfig, window string) ([]pcbPadP, error) {
	pads, coverage, err := fetchPcbPadsWithCoverage(cfg, window)
	if err != nil {
		return nil, err
	}
	// Planning consumers must not turn a partial inventory into a complete set
	// of power nets or obstacles. The read-only check reports missing coverage.
	if !coverage.ComponentsInventoryKnown || !coverage.PadsInventoryKnown || len(pads) != coverage.TotalPads {
		return nil, fmt.Errorf("PCB pad inventory/pose incomplete: %s", strings.Join(coverage.Limitations, "; "))
	}
	for _, pad := range coverage.UnverifiedPads {
		if pad.CaptureFailed {
			return nil, fmt.Errorf("PCB pad %s.%s unavailable: %s", pad.Designator, pad.PadNumber, pad.Reason)
		}
	}
	return pads, nil
}

func fetchPcbPadsWithCoverage(cfg *appConfig, window string) ([]pcbPadP, pcbGeometryCoverage, error) {
	res, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includePads": true})
	if err != nil {
		return nil, pcbGeometryCoverage{}, err
	}
	coverage := pcbReadbackCoverage(res.Result, false)
	snapshot := &boardSnapshot{Components: parseBoardComponents(res.Result)}
	return snapshot.toCheckPads(), coverage, nil
}

func fetchPcbTracks(cfg *appConfig, window string) ([]pcbTrack, error) {
	res, err := requestAction(cfg, "pcb.line.list", window, nil)
	if err != nil {
		return nil, err
	}
	rawLines, err := netPathRequiredSlice(res.Result, "lines")
	if err != nil {
		return nil, err
	}
	var tracks []pcbTrack
	for _, rl := range rawLines {
		lm, ok := rl.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("track inventory contains malformed record")
		}
		id, _ := lm["primitiveId"].(string)
		net, _ := lm["net"].(string)
		layer, _ := asFloatOK(lm["layer"])
		x1, _ := asFloatOK(lm["startX"])
		y1, _ := asFloatOK(lm["startY"])
		x2, _ := asFloatOK(lm["endX"])
		y2, _ := asFloatOK(lm["endY"])
		w, _ := asFloatOK(lm["lineWidth"])
		tracks = append(tracks, pcbTrack{ID: id, Net: net, Layer: int(layer), X1: x1, Y1: y1, X2: x2, Y2: y2, Width: w})
	}
	return tracks, nil
}

// fetchPcbArcs reads copper arcs from the same pcb.line.list response (the connector
// returns them in an `arcs` field). An older connector that omits `arcs` yields an
// empty slice — the dangling-end check simply loses arc-awareness, it does not fail.
func fetchPcbArcs(cfg *appConfig, window string) ([]pcbArc, error) {
	res, err := requestAction(cfg, "pcb.line.list", window, nil)
	if err != nil {
		return nil, err
	}
	if available, declared := res.Result["arcsAvailable"].(bool); declared && !available {
		return nil, fmt.Errorf("copper arc inventory capture failed")
	}
	rawArcs, err := netPathRequiredSlice(res.Result, "arcs")
	if err != nil {
		return nil, err
	}
	var arcs []pcbArc
	for _, ra := range rawArcs {
		am, ok := ra.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("arc inventory contains malformed record")
		}
		id, _ := am["primitiveId"].(string)
		net, _ := am["net"].(string)
		layer, _ := asFloatOK(am["layer"])
		x1, _ := asFloatOK(am["startX"])
		y1, _ := asFloatOK(am["startY"])
		x2, _ := asFloatOK(am["endX"])
		y2, _ := asFloatOK(am["endY"])
		w, _ := asFloatOK(am["lineWidth"])
		angle, _ := asFloatOK(am["arcAngle"])
		arcs = append(arcs, pcbArc{ID: id, Net: net, Layer: int(layer), X1: x1, Y1: y1, X2: x2, Y2: y2, Width: w, ArcAngle: angle})
	}
	return arcs, nil
}

func fetchPcbVias(cfg *appConfig, window string) ([]pcbViaP, error) {
	res, err := requestAction(cfg, "pcb.via.list", window, nil)
	if err != nil {
		return nil, err
	}
	rawVias, err := netPathRequiredSlice(res.Result, "vias")
	if err != nil {
		return nil, err
	}
	var vias []pcbViaP
	for _, rv := range rawVias {
		vm, ok := rv.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("via inventory contains malformed record")
		}
		id, _ := vm["primitiveId"].(string)
		net, _ := vm["net"].(string)
		x, _ := asFloatOK(vm["x"])
		y, _ := asFloatOK(vm["y"])
		hole, _ := asFloatOK(vm["holeDiameter"])
		dia, _ := asFloatOK(vm["diameter"])
		vias = append(vias, pcbViaP{ID: id, Net: net, X: x, Y: y, Hole: hole, Dia: dia})
	}
	return vias, nil
}

// fetchPcbSlots reads board cutouts (MULTI-layer fills, layer 12) with bboxes —
// the clearance rule keeps copper off the milled edges.
func fetchPcbSlots(cfg *appConfig, window string) ([]pcbSlotP, error) {
	res, err := requestAction(cfg, "pcb.fill.list", window, map[string]any{"layer": 12, "includeBBox": true})
	if err != nil {
		return nil, err
	}
	var slots []pcbSlotP
	for _, rf := range mnavSlice(res.Result, "fills") {
		fm, ok := rf.(map[string]any)
		if !ok {
			continue
		}
		id, _ := fm["primitiveId"].(string)
		bb, ok := fm["bbox"].(map[string]any)
		if !ok {
			continue
		}
		minX, ok1 := asFloatOK(bb["minX"])
		minY, ok2 := asFloatOK(bb["minY"])
		maxX, ok3 := asFloatOK(bb["maxX"])
		maxY, ok4 := asFloatOK(bb["maxY"])
		if ok1 && ok2 && ok3 && ok4 {
			slots = append(slots, pcbSlotP{ID: id, MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY})
		}
	}
	return slots, nil
}

func fetchPcbSilk(cfg *appConfig, window string) ([]pcbSilkText, error) {
	res, err := requestAction(cfg, "pcb.silk.list", window, nil)
	if err != nil {
		return nil, err
	}
	return parsePcbSilkTexts(res.Result)
}

func parsePcbSilkTexts(result map[string]any) ([]pcbSilkText, error) {
	rawTexts, known := result["texts"].([]any)
	if !known {
		return nil, fmt.Errorf("silkscreen inventory unavailable: missing texts array")
	}
	silk := make([]pcbSilkText, 0, len(rawTexts))
	for _, rt := range rawTexts {
		tm, ok := rt.(map[string]any)
		if !ok {
			continue
		}
		id, _ := tm["primitiveId"].(string)
		kind, _ := tm["kind"].(string)
		key, _ := tm["key"].(string)
		text, _ := tm["text"].(string)
		layer, _ := asFloatOK(tm["layer"])
		mirror, _ := tm["mirror"].(bool)
		reverse, _ := tm["reverse"].(bool)
		rotation, _ := asFloatOK(tm["rotation"])
		fontSize, _ := asFloatOK(tm["fontSize"]) // 0 = old connector → estimated
		compID, _ := tm["componentId"].(string)
		compLayer, _ := asFloatOK(tm["componentLayer"])
		x, _ := asFloatOK(tm["x"])
		y, _ := asFloatOK(tm["y"])
		var bbox *pcbRect
		if bm, ok := tm["bbox"].(map[string]any); ok {
			minX, ok1 := asFloatOK(bm["minX"])
			minY, ok2 := asFloatOK(bm["minY"])
			maxX, ok3 := asFloatOK(bm["maxX"])
			maxY, ok4 := asFloatOK(bm["maxY"])
			if ok1 && ok2 && ok3 && ok4 && maxX > minX && maxY > minY {
				bbox = &pcbRect{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
			}
		}
		var lineWidth *float64
		lineWidthAvailability := asString(tm["lineWidthAvailability"])
		if width, ok := asFloatOK(tm["lineWidth"]); ok && allFinite(width) && width >= 0 && (lineWidthAvailability == "" || lineWidthAvailability == "available") {
			lineWidth = &width
			lineWidthAvailability = "available"
		}
		if lineWidth == nil {
			lineWidthAvailability = "unknown"
		}
		visible, visibility := parsePcbSilkVisibility(tm, "visible", "visibilityAvailability")
		keyVisible, keyVisibility := parsePcbSilkVisibility(tm, "keyVisible", "keyVisibilityAvailability")
		silk = append(silk, pcbSilkText{
			Visible: visible, VisibilityAvailability: visibility, VisibilitySource: asString(tm["visibilitySource"]), VisibilityError: asString(tm["visibilityError"]),
			KeyVisible: keyVisible, KeyVisibilityAvailability: keyVisibility, KeyVisibilitySource: asString(tm["keyVisibilitySource"]), KeyVisibilityError: asString(tm["keyVisibilityError"]),
			FontFamily: asString(tm["fontFamily"]), LineWidth: lineWidth, LineWidthAvailability: lineWidthAvailability, LineWidthSource: asString(tm["lineWidthSource"]), LineWidthError: asString(tm["lineWidthError"]),
			ID: id, Kind: kind, Key: key, Text: text, Layer: int(layer), Mirror: mirror,
			Reverse: reverse, Rotation: rotation, FontSize: fontSize, CompID: compID, CompLayer: int(compLayer), X: x, Y: y,
			BBox: bbox,
		})
	}
	if len(silk) != len(rawTexts) {
		return silk, fmt.Errorf("silkscreen inventory contains malformed records")
	}
	return silk, nil
}

// Unknown visibility never becomes a guessed visible/hidden attribute. Older
// dumps remain diagnostic input, but cannot assert rendered text membership.
func parsePcbSilkVisibility(raw map[string]any, field, availabilityField string) (*bool, string) {
	if raw[availabilityField] == "available" {
		if value, ok := raw[field].(bool); ok {
			return &value, "available"
		}
	}
	return nil, "unknown"
}

// fetchAntennaContext derives the same parameterized radiator rectangle as the
// generator. Missing bboxes/pads/source/layers remain explicit incomplete data.
func fetchAntennaContext(cfg *appConfig, window string, silk []pcbSilkText) ([]pcbAntComp, []pcbKeepRegion, int, error) {
	devByComp := map[string]string{}
	for _, s := range silk {
		if s.Kind == "attribute" && s.Key == "Device" && s.CompID != "" {
			devByComp[s.CompID] = s.Text
		}
	}
	cres, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includeBBox": true, "includePads": true})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("antenna component inventory unavailable: %w", err)
	}
	components, err := netPathRequiredSlice(cres.Result, "components")
	if err != nil {
		return nil, nil, 0, err
	}
	keepouts, err := blocks.LoadAntennaKeepouts()
	if err != nil {
		return nil, nil, 0, fmt.Errorf("antenna depth declarations unavailable: %w", err)
	}
	var ants []pcbAntComp
	var board *layoutBBox
	boardRead := false
	for _, raw := range components {
		cm, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, 0, fmt.Errorf("antenna component inventory contains an unreadable record")
		}
		id, desig := asString(cm["primitiveId"]), asString(cm["designator"])
		device := resolveAntennaDevice(devByComp[id], cm)
		if !isAntennaDevice(device, desig) {
			continue
		}
		ac := pcbAntComp{Designator: desig, Device: device, RequiredRules: []int{5, 6, 7}}
		for _, key := range []string{"bbox", "pads"} {
			if issue := antennaCommandCaptureIssue(cm, key); issue != "" {
				ac.Incomplete = issue
				break
			}
		}
		bb, bboxErr := antennaCommandBBox(cm)
		if bboxErr != nil && ac.Incomplete == "" {
			ac.Incomplete = bboxErr.Error()
		}
		if bboxErr == nil {
			ac.BBox = &bb
		}
		padRecords, padsOK := cm["pads"].([]any)
		if (!padsOK || len(padRecords) == 0) && ac.Incomplete == "" {
			ac.Incomplete = "pad inventory unavailable or empty"
		}
		var pads [][2]float64
		for _, rawPad := range padRecords {
			pm, ok := rawPad.(map[string]any)
			if !ok {
				ac.Incomplete = "unreadable pad record"
				break
			}
			x, xok := asFloatOK(pm["x"])
			y, yok := asFloatOK(pm["y"])
			if !xok || !yok || !antennaFinite(x) || !antennaFinite(y) {
				ac.Incomplete = "pad coordinates unavailable"
				break
			}
			pads = append(pads, [2]float64{x, y})
		}
		if ac.Incomplete == "" {
			var x0, y0, x1, y1 float64
			var ok bool
			chip := isChipAntennaPart(device, bb.MaxX-bb.MinX, bb.MaxY-bb.MinY)
			if chip {
				x0, y0, x1, y1, ok = chipAntennaKeepoutRect(bb.MinX, bb.MinY, bb.MaxX, bb.MaxY, 120)
				// #129: no-wires would prohibit the chip antenna's own RF feed.
				ac.RequiredRules = []int{6, 7}
			} else {
				x0, y0, x1, y1, ok = antennaKeepoutRect(bb.MinX, bb.MinY, bb.MaxX, bb.MaxY, pads,
					antennaKeepoutFrac(keepouts, device), 20, 40)
			}
			if ok {
				rect := [4]float64{x0, y0, x1, y1}
				// Same ruler as creation: edge-span a module antenna on the board
				// edge, then judge coverage only over the on-board part.
				if !boardRead {
					board, boardRead = fetchAntennaBoardBBox(cfg, window), true
				}
				if !chip {
					rect = antennaEdgeSpan(rect, bb, board)
				}
				rect = antennaOnBoardRect(rect, board)
				ac.RequiredRect = &rect
			} else {
				ac.Incomplete = "cannot derive a pad-free antenna strip"
			}
		}
		ants = append(ants, ac)
	}
	if len(ants) == 0 {
		return ants, nil, 0, nil
	}
	lres, err := requestAction(cfg, "pcb.layers.list", window, nil)
	if err != nil {
		return ants, nil, 0, fmt.Errorf("antenna copper layers unavailable: %w", err)
	}
	n, ok := asFloatOK(lres.Result["copperLayerCount"])
	if !ok || !antennaFinite(n) || n < 2 || n > 32 || n != math.Trunc(n) || int(n)%2 != 0 {
		return ants, nil, 0, fmt.Errorf("antenna copper layer count unavailable or invalid")
	}
	copperLayers := int(n)
	rres, err := requestAction(cfg, "pcb.region.list", window, nil)
	if err != nil {
		return ants, nil, copperLayers, fmt.Errorf("antenna region inventory unavailable: %w", err)
	}
	rawRegions, err := netPathRequiredSlice(rres.Result, "regions")
	if err != nil {
		return ants, nil, copperLayers, err
	}
	regions := parseAntennaKeepRegions(rres.Result)
	if len(regions) != len(rawRegions) {
		return ants, regions, copperLayers, fmt.Errorf("antenna region inventory contains unreadable records")
	}
	for _, region := range regions {
		if !region.GeometryAvailable {
			return ants, regions, copperLayers, fmt.Errorf("antenna region %s geometry unavailable (closed single linear polygon required)", region.PrimitiveID)
		}
	}
	return ants, regions, copperLayers, nil
}

// mnavSlice is mnav + a []any assertion (nil on miss).
func mnavSlice(result map[string]any, key string) []any {
	s, _ := mnav(result, key).([]any)
	return s
}

// toFloatSlice coerces a JSON array of numbers to []float64.
func toFloatSlice(v any) []float64 {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]float64, 0, len(arr))
	for _, x := range arr {
		if f, ok := asFloatOK(x); ok {
			out = append(out, f)
		}
	}
	return out
}

func renderPcbCheckReport(rep pcbCheckReport, w io.Writer) {
	s := rep.Summary
	fmt.Fprintf(w, "PCB check (DFM): %d track(s), %d via(s), %d pad(s) — %d issue(s)\n",
		rep.TrackCount, rep.ViaCount, rep.PadCount, s.Total)
	for _, limitation := range rep.Limitations {
		fmt.Fprintf(w, "  LIMIT %s\n", limitation)
	}
	if s.Total == 0 {
		if rep.Incomplete {
			fmt.Fprintln(w, "  incomplete: no findings in observed data; required inventory/geometry is unverified")
			return
		}
		fmt.Fprintln(w, "  ✓ no DFM issues found")
		return
	}
	fmt.Fprintf(w, "  ERROR=%d WARN=%d  |  dangling=%d acute=%d nonOrtho=%d overPad=%d clearance=%d silkFlipped=%d overlapVia=%d singleLayerVia=%d widthMismatch=%d dupSegment=%d coupling=%d antennaKeepout=%d netlessPour=%d viaCrossesPlane=%d floatingIsland=%d powerNotPoured=%d netlessViaInPad=%d widthUnderSpec=%d silkOverPad=%d silkOffBoard=%d decapTooFar=%d viaInPad=%d copperNearEdge=%d fiducialMissing=%d zoneViolation=%d zoneClaimsStale=%d internalOnEdge=%d plugClearance=%d matingBlocked=%d openingInward=%d\n",
		s.Errors, s.Warnings-s.Errors,
		s.DanglingEnds, s.AcuteAngles, s.NonOrthogonal, s.TrackOverPad, s.Clearance, s.SilkscreenFlipped, s.OverlappingVias, s.SingleLayerVias, s.WidthMismatches, s.DuplicateSegments, s.ParallelCoupling, s.AntennaKeepout, s.NetlessPours, s.ViaCrossesPlane, s.FloatingIslands, s.PowerNotPoured, s.NetlessViaInPad, s.WidthUnderSpec, s.SilkOverPad, s.SilkOffBoard, s.DecapTooFar, s.ViaInPad, s.CopperNearEdge, s.FiducialMissing, s.ZoneViolation, s.ZoneClaimsStale, s.InternalOnEdge, s.ConnectorPlugClearance, s.ConnectorMatingBlocked, s.ConnectorOpeningInward)
	for _, f := range rep.Findings {
		loc := ""
		if f.At != nil {
			loc = fmt.Sprintf(" @ (%.0f, %.0f)", f.At.X, f.At.Y)
		}
		net := f.Net
		if net == "" && len(f.Nets) > 0 {
			net = fmt.Sprintf("%v", f.Nets)
		}
		fmt.Fprintf(w, "  %-5s %-17s %s%s  [%s]\n", f.Level, f.Type, f.Message, loc, net)
	}
}

// ── differential pairs (#176 constraints) feeding the 3W rule ───────────────

// pcbDiffPair is one DECLARED differential pair, as `pcb diff-pair create`
// stores it on the board. The 3W rule needs it because the two members of a
// pair are *supposed* to run tightly coupled — that is what makes them a pair.
type pcbDiffPair struct{ Positive, Negative string }

// fetchPcbDiffPairs reads the board's declared length constraints. Best-effort,
// like arcs and silk: an older connector without pcb.constraint.list just means
// the coupling rule loses pair-awareness, which is the behaviour it had before.
func fetchPcbDiffPairs(cfg *appConfig, window string) ([]pcbDiffPair, error) {
	res, err := requestAction(cfg, "pcb.constraint.list", window, nil)
	if err != nil {
		return nil, err
	}
	raw, _ := mnav(res.Result, "differentialPairs").([]any)
	var out []pcbDiffPair
	for _, it := range raw {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		p, _ := m["positiveNet"].(string)
		n, _ := m["negativeNet"].(string)
		if p != "" && n != "" {
			out = append(out, pcbDiffPair{Positive: p, Negative: n})
		}
	}
	return out, nil
}

// isDeclaredDiffPair reports whether these two nets are the two members of one
// declared pair. Order-independent: the caller has already sorted the names for
// the finding message, so it must not depend on which is positive.
func isDeclaredDiffPair(a, b string, pairs []pcbDiffPair) bool {
	for _, p := range pairs {
		if (a == p.Positive && b == p.Negative) || (a == p.Negative && b == p.Positive) {
			return true
		}
	}
	return false
}

// fetchPcbOutlineBBox reads the board outline bbox (center-line, else the
// rendered bbox). drawn=false with a nil error means the read succeeded and the
// board has no outline yet; an error means the outline could not be read.
func fetchPcbOutlineBBox(cfg *appConfig, window string) (pcbRect, bool, error) {
	res, err := requestAction(cfg, "pcb.outline.get", window, nil)
	if err != nil {
		return pcbRect{}, false, err
	}
	bb, ok := res.Result["centerlineBBox"].(map[string]any)
	if !ok {
		bb, ok = res.Result["bbox"].(map[string]any)
	}
	if !ok {
		return pcbRect{}, false, nil
	}
	var r pcbRect
	var oks [4]bool
	r.MinX, oks[0] = asFloatOK(bb["minX"])
	r.MinY, oks[1] = asFloatOK(bb["minY"])
	r.MaxX, oks[2] = asFloatOK(bb["maxX"])
	r.MaxY, oks[3] = asFloatOK(bb["maxY"])
	if !oks[0] || !oks[1] || !oks[2] || !oks[3] || r.MaxX <= r.MinX || r.MaxY <= r.MinY {
		return pcbRect{}, false, fmt.Errorf("board outline bbox unreadable")
	}
	return r, true, nil
}

// findSilkOffBoard flags visible silkscreen texts whose rendered extent (or,
// without a bbox, anchor) leaves the board outline bbox. Hidden texts and texts
// of unknown visibility are not judged. Silk layers only.
func findSilkOffBoard(silk []pcbSilkText, board pcbRect) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, s := range silk {
		if (s.Layer != silkTopLayer && s.Layer != silkBottomLayer) || s.Visible == nil || !*s.Visible {
			continue
		}
		ext := pcbRect{MinX: s.X, MinY: s.Y, MaxX: s.X, MaxY: s.Y}
		if s.BBox != nil {
			ext = *s.BBox
		}
		if ext.MinX >= board.MinX && ext.MaxX <= board.MaxX && ext.MinY >= board.MinY && ext.MaxY <= board.MaxY {
			continue
		}
		label := s.Text
		if label == "" {
			label = s.ID
		}
		out = append(out, pcbCheckFinding{
			Type: "silk-off-board", Level: "WARN", Layer: s.Layer, Designator: label,
			Primitives: []string{s.ID}, At: &pcbXY{round2(s.X), round2(s.Y)},
			Message: fmt.Sprintf("silkscreen text '%s' extends outside the board outline (text %.0f..%.0f × %.0f..%.0f mil, board %.0f..%.0f × %.0f..%.0f) — it will not be printed; move it with `pcb silk-set --ids %s --x … --y …`",
				label, ext.MinX, ext.MaxX, ext.MinY, ext.MaxY, board.MinX, board.MaxX, board.MinY, board.MaxY, s.ID),
		})
	}
	return out
}
