package app

import (
	"fmt"
	"math"
	"strings"
)

// pcb_pad_geometry.go — derived, unit-labelled pad facts for `pcb dump` and the
// offline route solver (#309). Raw connector fields stay authoritative; these
// projections are recomputed from them and never assume a missing value is 0.

// pcbHoleGeometry is one pad drill in absolute board mil.
//
// The connector reports the drill tuple in 10 mil units (live: a 2.54 mm header
// reads ["ROUND", 4.3308] = 1.1 mm). Hole offsets have no verified unit, so only
// an offset that is 0 up to float noise (|v| <= pcbHoleOffsetNoise, live USB-C
// slots read ±0.0003–0.0007) yields an exact position. Hole rotation is read in
// radians (live: a vertical USB-C slot reports exactly π/2) and accepted only at
// whole multiples of π/2; any other value has no verified unit and stays unknown.
// SLOT length semantics (overall vs. center distance) are not documented; the
// obstacle model uses a spine of the full length, a superset of both readings.
type pcbHoleGeometry struct {
	Status      string  `json:"status"` // available | unknown
	Reason      string  `json:"reason,omitempty"`
	Kind        string  `json:"kind,omitempty"` // ROUND | SLOT
	Units       string  `json:"units"`
	Plated      *bool   `json:"plated,omitempty"`
	DiameterMil float64 `json:"diameterMil,omitempty"`
	LengthMil   float64 `json:"lengthMil,omitempty"`
	CenterX     float64 `json:"centerX,omitempty"`
	CenterY     float64 `json:"centerY,omitempty"`
	RotationDeg float64 `json:"rotationDeg,omitempty"` // absolute: pad rotation + hole rotation
	Model       string  `json:"model,omitempty"`
}

const pcbHoleTupleUnitMil = 10

// pcbHoleOffsetNoise is the largest hole offset still read as 0. In the drill
// tuple's 10 mil unit it is 0.01 mil (0.04 mil even if the unit were mm) — far
// below any clearance rule, while real footprints carry ±0.0007 export noise.
const pcbHoleOffsetNoise = 1e-3

// pcbHoleRotationDeg converts a hole rotation to degrees when it is a whole
// multiple of π/2 radians. Other values (including degree-looking 90) are not
// accepted: the unit is only verified for cardinal slots.
func pcbHoleRotationDeg(r float64) (float64, bool) {
	k := math.Round(r / (math.Pi / 2))
	if !allFinite(r) || math.Abs(r-k*math.Pi/2) > 1e-6 {
		return 0, false
	}
	return normalizeDeg(k * 90), true
}

// pcbPadHoleGeometry derives the drill shape of p, or nil when the pad has no
// hole. Any missing or unverifiable input yields Status "unknown" with a reason.
func pcbPadHoleGeometry(p boardPad) *pcbHoleGeometry {
	if p.Hole == nil {
		if state, declared := p.Availability["hole"]; declared && state != "available" || p.CaptureErrors["hole"] != nil {
			return &pcbHoleGeometry{Status: "unknown", Units: "mil", Reason: "hole capture failed"}
		}
		return nil
	}
	g := &pcbHoleGeometry{Status: "unknown", Units: "mil", Plated: p.Metallization}
	tuple, ok := p.Hole.([]any)
	if !ok || len(tuple) < 2 {
		g.Reason = "hole tuple is malformed"
		return g
	}
	kind, _ := tuple[0].(string)
	g.Kind = strings.ToUpper(strings.TrimSpace(kind))
	d, ok := netPathOptionalFinite(tuple[1])
	if !ok || d <= 0 {
		g.Reason = "hole diameter is missing or not positive"
		return g
	}
	g.DiameterMil = round4(d * pcbHoleTupleUnitMil)
	switch g.Kind {
	case "ROUND":
		g.Model = "circle"
	case "SLOT":
		if len(tuple) < 3 {
			g.Reason = "SLOT hole length is missing"
			return g
		}
		l, ok := netPathOptionalFinite(tuple[2])
		if !ok || l <= 0 {
			g.Reason = "SLOT hole length is not positive"
			return g
		}
		g.LengthMil = round4(l * pcbHoleTupleUnitMil)
		g.Model = "conservative stadium: spine = lengthMil, radius = diameterMil/2"
	default:
		g.Reason = fmt.Sprintf("hole type %q is unsupported", kind)
		return g
	}
	if !allFinite(p.X, p.Y, p.Rotation) {
		g.Reason = "pad pose is unknown"
		return g
	}
	for _, field := range []string{"holeOffsetX", "holeOffsetY"} {
		v := p.HoleOffsetX
		if field == "holeOffsetY" {
			v = p.HoleOffsetY
		}
		if v == nil || p.Availability[field] != "" && p.Availability[field] != "available" {
			g.Reason = field + " was not captured (connector predates hole placement capture; re-dump)"
			return g
		}
		if math.Abs(*v) > pcbHoleOffsetNoise {
			g.Reason = field + " is nonzero and its unit is unverified"
			return g
		}
	}
	g.CenterX, g.CenterY = p.X, p.Y
	g.RotationDeg = p.Rotation
	if g.Kind == "SLOT" {
		if p.HoleRotation == nil || p.Availability["holeRotation"] != "" && p.Availability["holeRotation"] != "available" {
			g.Reason = "holeRotation was not captured (connector predates hole placement capture; re-dump)"
			return g
		}
		holeDeg, ok := pcbHoleRotationDeg(*p.HoleRotation)
		if !ok {
			g.Reason = fmt.Sprintf("holeRotation %v is not a multiple of π/2 radians; its unit is unverified", *p.HoleRotation)
			return g
		}
		g.RotationDeg = normalizeDeg(p.Rotation + holeDeg)
	}
	g.Status = "available"
	return g
}

// segmentGap is the distance from segment a–z to the drill wall (0 if they
// intersect). Only valid when Status is "available".
func (g pcbHoleGeometry) segmentGap(a, z [2]float64) float64 {
	r := g.DiameterMil / 2
	if g.Kind != "SLOT" {
		return math.Max(0, segPtDist(g.CenterX, g.CenterY, a[0], a[1], z[0], z[1])-r)
	}
	hx, hy := rotateVec(g.LengthMil/2, 0, g.RotationDeg)
	return math.Max(0, segSegDist(a[0], a[1], z[0], z[1], g.CenterX-hx, g.CenterY-hy, g.CenterX+hx, g.CenterY+hy)-r)
}

// enrichPolygonPadExtent fills a POLYGON pad's missing width/height with the
// centered envelope of its exact contour and records the exact bbox, so
// external consumers never see an empty size for a measurable pad.
func enrichPolygonPadExtent(p *boardPad) {
	parsed := pcbPadP{X: p.X, Y: p.Y}
	if err := parseNetPathPadShape(map[string]any{"shape": p.Shape, "specialPad": p.SpecialPad}, &parsed, "pad"); err != nil ||
		parsed.Shape != "POLYGON" || !parsed.exactClearanceKnown() || !allFinite(p.X, p.Y) {
		return
	}
	bb := layoutBBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	for _, contour := range parsed.Contours {
		for _, pt := range contour {
			bb.MinX, bb.MinY = math.Min(bb.MinX, pt[0]), math.Min(bb.MinY, pt[1])
			bb.MaxX, bb.MaxY = math.Max(bb.MaxX, pt[0]), math.Max(bb.MaxY, pt[1])
		}
	}
	p.ShapeBBox = &bb
	if p.W <= 0 || p.H <= 0 {
		p.W, p.H = round4(parsed.W), round4(parsed.H)
		p.ExtentSource = "polygon-centered-envelope"
	}
}
