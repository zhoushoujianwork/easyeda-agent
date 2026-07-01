package app

// pcb outline-round — generate a rounded-rectangle board outline (#29). EasyEDA's
// pcb.outline.set takes a closed polygon and approximates curves with line segments,
// so a rounded rect = the 4 straight edges + a chord-approximated quarter arc at each
// corner. The board-outline layer renders, so this is snapshot-verifiable.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// roundedRectPoints returns the CCW polygon of a rounded rectangle (y-up). r is the
// corner radius (clamped to ≤ half the shorter side); seg = chord count per corner.
func roundedRectPoints(x0, y0, x1, y1, r float64, seg int) [][]float64 {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	if maxR := math.Min(x1-x0, y1-y0) / 2; r > maxR {
		r = maxR
	}
	if r < 0 {
		r = 0
	}
	if seg < 1 {
		seg = 6
	}
	// Four corners, each a 90° arc, walked CCW: BR → TR → TL → BL. The straight edges
	// fall out as the polygon segments between consecutive corner arcs.
	corners := []struct{ cx, cy, a0 float64 }{
		{x1 - r, y0 + r, -math.Pi / 2}, // bottom-right: -90°→0°
		{x1 - r, y1 - r, 0},            // top-right:     0°→90°
		{x0 + r, y1 - r, math.Pi / 2},  // top-left:     90°→180°
		{x0 + r, y0 + r, math.Pi},      // bottom-left: 180°→270°
	}
	var pts [][]float64
	for _, c := range corners {
		for i := 0; i <= seg; i++ {
			a := c.a0 + (math.Pi/2)*float64(i)/float64(seg)
			pts = append(pts, []float64{round2(c.cx + r*math.Cos(a)), round2(c.cy + r*math.Sin(a))})
		}
	}
	return pts
}

// runOutlineRound resolves the rectangle (explicit --rect, else the current outline
// bbox), expands by margin, rounds the corners, and replaces the outline.
func runOutlineRound(cfg *appConfig, window, rectSpec string, radius, margin float64, segments int, dryRun bool, stdout, stderr io.Writer) error {
	var x0, y0, x1, y1 float64
	if rectSpec != "" {
		var err error
		x0, y0, x1, y1, err = parseRectSpec(rectSpec)
		if err != nil {
			return err
		}
	} else {
		rect, err := outlineRect(cfg, window, 0)
		if err != nil {
			return fmt.Errorf("no --rect and no current outline to round (%v) — set one with `pcb outline-set`/`outline-fit` first", err)
		}
		x0, y0, x1, y1 = rect[0], rect[1], rect[2], rect[3]
	}
	// margin expands outward.
	x0 -= margin
	y0 -= margin
	x1 += margin
	y1 += margin

	if radius <= 0 {
		radius = math.Min(math.Abs(x1-x0), math.Abs(y1-y0)) * 0.12 // sensible default
	}
	pts := roundedRectPoints(x0, y0, x1, y1, radius, segments)

	if dryRun {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"dryRun": true, "radius": round2(radius), "segmentsPerCorner": segments, "rect": []float64{x0, y0, x1, y1}, "points": pts})
	}
	return dispatch(cfg, "pcb.outline.set", window, map[string]any{"points": pts}, stdout, stderr)
}
