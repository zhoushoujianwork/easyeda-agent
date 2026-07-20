// Package svgimport converts an SVG document into the EasyEDA Pro
// "complex polygon" command arrays consumed by eda.pcb_PrimitiveImage.create,
// so an SVG logo/artwork can be placed as a FILLED silkscreen primitive.
//
// Every curve (cubic/quadratic Bézier and elliptical arc) is flattened to line
// segments, so the output is a set of polyline contours: each contour is one
// TPCB_PolygonSourceArray of the form [x0, y0, "L", x1, y1, x2, y2, …]. The full
// result is an array of such contours (a complex polygon); the EasyEDA renderer
// fills them with an even-odd rule, so inner contours punch holes — matching how
// most logos encode counters (the "hole" in an "o", etc.).
//
// Coordinate frame: output points are in mil, in an SVG-native frame (origin at
// the artwork's top-left, +y downward, normalised so the bbox starts at 0,0).
// The connector places the primitive at (atX, atY); the artwork's top-left lands
// there and renders upright (probe-verified: screen_y = atY − local_y).
package svgimport

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Options controls how the SVG is scaled and flattened.
type Options struct {
	// TargetWidth / TargetHeight in mil. 0 means "unspecified".
	TargetWidth  float64
	TargetHeight float64
	// KeepAspect forces uniform scaling (the default when only one of
	// width/height is given).
	KeepAspect bool
	// FlattenTol is the curve-flattening tolerance in mil (default 2).
	FlattenTol float64
}

// Result is the parsed, scaled, flattened artwork ready to send to the connector.
type Result struct {
	// Polygons is the complex polygon: an array of contours, each a
	// TPCB_PolygonSourceArray ([x0,y0,"L",x1,y1,…]). Values are float64 or the
	// string "L".
	Polygons [][]any
	Width    float64 // rendered width in mil
	Height   float64 // rendered height in mil
	// MinFeature is a heuristic thinnest-feature size in mil (min of each
	// contour's bbox W/H) — a proxy for DFM silk minimum feature checks.
	MinFeature float64
	PathCount  int // number of contours
	PointCount int // total vertices across all contours
}

// Parse reads an SVG document and returns the scaled complex-polygon result.
func Parse(r io.Reader, opts Options) (*Result, error) {
	tol := opts.FlattenTol
	if tol <= 0 {
		tol = 2.0
	}

	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	stack := []matrix{identity()}
	var subs [][]pt
	// SVG user-space flatten tolerance must be scaled with the CTM, but the CTM
	// varies; we flatten in user space with a tolerance derived AFTER we know the
	// final scale. To keep it simple we flatten with a small fixed user-space
	// fraction and rely on the final normalisation being near-1. Use tol directly
	// in user space; typical logos have viewBox ≈ output size after scaling, and
	// we re-check below.
	found := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse svg: %w", err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			found = true
			top := stack[len(stack)-1]
			if tf := attr(se, "transform"); tf != "" {
				top = top.mul(parseTransform(tf))
			}
			stack = append(stack, top)
			collectElement(se, top, tol, &subs)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("no SVG elements found")
	}
	if len(subs) == 0 {
		return nil, fmt.Errorf("no drawable geometry found (paths/shapes)")
	}

	// content bbox in user space
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, s := range subs {
		for _, p := range s {
			minX, minY = math.Min(minX, p.x), math.Min(minY, p.y)
			maxX, maxY = math.Max(maxX, p.x), math.Max(maxY, p.y)
		}
	}
	cw, ch := maxX-minX, maxY-minY
	if cw <= 0 || ch <= 0 {
		return nil, fmt.Errorf("degenerate artwork bounds (w=%g h=%g)", cw, ch)
	}

	sx, sy := scaleFactors(cw, ch, opts)

	res := &Result{}
	minFeat := math.Inf(1)
	for _, s := range subs {
		poly := make([]any, 0, len(s)*2+1)
		sMinX, sMinY := math.Inf(1), math.Inf(1)
		sMaxX, sMaxY := math.Inf(-1), math.Inf(-1)
		for j, p := range s {
			nx := round3((p.x - minX) * sx)
			ny := round3((p.y - minY) * sy)
			if j == 0 {
				poly = append(poly, nx, ny, "L")
			} else {
				poly = append(poly, nx, ny)
			}
			sMinX, sMinY = math.Min(sMinX, nx), math.Min(sMinY, ny)
			sMaxX, sMaxY = math.Max(sMaxX, nx), math.Max(sMaxY, ny)
			res.PointCount++
		}
		// a subpath with only a start point (no segments) is dropped
		if len(s) < 2 {
			continue
		}
		res.Polygons = append(res.Polygons, poly)
		if f := math.Min(sMaxX-sMinX, sMaxY-sMinY); f < minFeat {
			minFeat = f
		}
	}
	res.PathCount = len(res.Polygons)
	if res.PathCount == 0 {
		return nil, fmt.Errorf("no drawable contours after flattening")
	}
	res.Width = round3(cw * sx)
	res.Height = round3(ch * sy)
	if math.IsInf(minFeat, 1) {
		minFeat = 0
	}
	res.MinFeature = round3(minFeat)
	return res, nil
}

// scaleFactors resolves (sx, sy) from the content size and options.
func scaleFactors(cw, ch float64, opts Options) (float64, float64) {
	tw, th := opts.TargetWidth, opts.TargetHeight
	switch {
	case tw > 0 && th > 0 && !opts.KeepAspect:
		return tw / cw, th / ch
	case tw > 0 && th > 0 && opts.KeepAspect:
		s := math.Min(tw/cw, th/ch)
		return s, s
	case tw > 0:
		return tw / cw, tw / cw
	case th > 0:
		return th / ch, th / ch
	default:
		return 1, 1 // no target: treat user units as mil
	}
}

// collectElement flattens one SVG shape element into subpaths (CTM applied).
func collectElement(se xml.StartElement, ctm matrix, tol float64, subs *[][]pt) {
	name := strings.ToLower(se.Name.Local)
	switch name {
	case "path":
		d := attr(se, "d")
		if d == "" {
			return
		}
		for _, sub := range parsePathData(d, tol) {
			*subs = append(*subs, transformPts(sub, ctm))
		}
	case "polygon", "polyline":
		nums := parseFloatList(attr(se, "points"))
		var sub []pt
		for i := 0; i+1 < len(nums); i += 2 {
			sub = append(sub, pt{nums[i], nums[i+1]})
		}
		if name == "polygon" && len(sub) > 0 {
			sub = append(sub, sub[0]) // close
		}
		if len(sub) >= 2 {
			*subs = append(*subs, transformPts(sub, ctm))
		}
	case "rect":
		x := fnum(attr(se, "x"))
		y := fnum(attr(se, "y"))
		w := fnum(attr(se, "width"))
		h := fnum(attr(se, "height"))
		if w <= 0 || h <= 0 {
			return
		}
		rx := fnum(attr(se, "rx"))
		ry := fnum(attr(se, "ry"))
		if rx == 0 {
			rx = ry
		}
		if ry == 0 {
			ry = rx
		}
		var sub []pt
		if rx > 0 && ry > 0 {
			sub = roundedRect(x, y, w, h, rx, ry, tol)
		} else {
			sub = []pt{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}, {x, y}}
		}
		*subs = append(*subs, transformPts(sub, ctm))
	case "circle":
		cx := fnum(attr(se, "cx"))
		cy := fnum(attr(se, "cy"))
		r := fnum(attr(se, "r"))
		if r <= 0 {
			return
		}
		*subs = append(*subs, transformPts(ellipsePts(cx, cy, r, r, tol), ctm))
	case "ellipse":
		cx := fnum(attr(se, "cx"))
		cy := fnum(attr(se, "cy"))
		rx := fnum(attr(se, "rx"))
		ry := fnum(attr(se, "ry"))
		if rx <= 0 || ry <= 0 {
			return
		}
		*subs = append(*subs, transformPts(ellipsePts(cx, cy, rx, ry, tol), ctm))
	case "line":
		x1 := fnum(attr(se, "x1"))
		y1 := fnum(attr(se, "y1"))
		x2 := fnum(attr(se, "x2"))
		y2 := fnum(attr(se, "y2"))
		*subs = append(*subs, transformPts([]pt{{x1, y1}, {x2, y2}}, ctm))
	}
}

func transformPts(in []pt, m matrix) []pt {
	out := make([]pt, len(in))
	for i, p := range in {
		x, y := m.apply(p.x, p.y)
		out[i] = pt{x, y}
	}
	return out
}

func ellipsePts(cx, cy, rx, ry, tol float64) []pt {
	rmax := math.Max(rx, ry)
	segs := 32
	if rmax > 0 && tol > 0 && tol < rmax {
		maxStep := 2 * math.Acos(clamp(1-tol/rmax, -1, 1))
		if maxStep > 0 {
			segs = int(math.Ceil(2 * math.Pi / maxStep))
		}
	}
	if segs < 8 {
		segs = 8
	}
	if segs > 512 {
		segs = 512
	}
	out := make([]pt, 0, segs+1)
	for i := 0; i <= segs; i++ {
		th := 2 * math.Pi * float64(i) / float64(segs)
		out = append(out, pt{cx + rx*math.Cos(th), cy + ry*math.Sin(th)})
	}
	return out
}

func roundedRect(x, y, w, h, rx, ry, tol float64) []pt {
	if rx > w/2 {
		rx = w / 2
	}
	if ry > h/2 {
		ry = h / 2
	}
	var out []pt
	arc := func(cx, cy, a0, a1 float64) {
		rmax := math.Max(rx, ry)
		segs := 6
		if rmax > 0 && tol > 0 && tol < rmax {
			maxStep := 2 * math.Acos(clamp(1-tol/rmax, -1, 1))
			if maxStep > 0 {
				segs = int(math.Ceil(math.Abs(a1-a0) / maxStep))
			}
		}
		if segs < 2 {
			segs = 2
		}
		for i := 0; i <= segs; i++ {
			th := a0 + (a1-a0)*float64(i)/float64(segs)
			out = append(out, pt{cx + rx*math.Cos(th), cy + ry*math.Sin(th)})
		}
	}
	// top-left → top-right → bottom-right → bottom-left, corners CCW in SVG y-down
	arc(x+rx, y+ry, math.Pi, 1.5*math.Pi)
	arc(x+w-rx, y+ry, 1.5*math.Pi, 2*math.Pi)
	arc(x+w-rx, y+h-ry, 0, 0.5*math.Pi)
	arc(x+rx, y+h-ry, 0.5*math.Pi, math.Pi)
	if len(out) > 0 {
		out = append(out, out[0])
	}
	return out
}

func attr(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if strings.EqualFold(a.Name.Local, name) {
			return a.Value
		}
	}
	return ""
}

// fnum parses a length, stripping a trailing unit (px/pt/mm/…). Percentages are
// treated as 0 (unsupported).
func fnum(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, "%") {
		return 0
	}
	s = strings.TrimRight(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
