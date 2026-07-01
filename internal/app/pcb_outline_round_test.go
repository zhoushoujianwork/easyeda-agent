package app

import (
	"math"
	"testing"
)

func TestRoundedRectPoints(t *testing.T) {
	// 100×80 rect at origin, radius 10, 4 segments/corner.
	pts := roundedRectPoints(0, 0, 100, 80, 10, 4)
	// 4 corners × (4+1) points.
	if len(pts) != 20 {
		t.Fatalf("point count=%d, want 20", len(pts))
	}
	// Every point stays within the rect and on/inside the rounded boundary; check the
	// extremes hit the straight edges (a point at x=100 exists on the right edge, etc).
	var maxX, minX, maxY, minY float64 = -1e9, 1e9, -1e9, 1e9
	for _, p := range pts {
		maxX = math.Max(maxX, p[0])
		minX = math.Min(minX, p[0])
		maxY = math.Max(maxY, p[1])
		minY = math.Min(minY, p[1])
	}
	if !(near(minX, 0) && near(maxX, 100) && near(minY, 0) && near(maxY, 80)) {
		t.Errorf("bbox=(%.1f,%.1f)-(%.1f,%.1f), want (0,0)-(100,80)", minX, minY, maxX, maxY)
	}
	// A corner center is inset by r; the arc points near a corner must be ≥ r from the
	// two far edges (i.e. no point sits exactly at the sharp corner (0,0)).
	for _, p := range pts {
		if near(p[0], 0) && near(p[1], 0) {
			t.Error("found a point at the sharp corner (0,0) — corner not rounded")
		}
	}
}

func TestRoundedRectPoints_RadiusClampedAndDefaults(t *testing.T) {
	// Radius larger than half the short side is clamped (100×80 → max r = 40).
	pts := roundedRectPoints(0, 0, 100, 80, 999, 3)
	for _, p := range pts {
		// with r=40 the corner arcs meet the mid of the short edges; no point exceeds the rect
		if p[0] < -0.01 || p[0] > 100.01 || p[1] < -0.01 || p[1] > 80.01 {
			t.Errorf("point %v outside clamped rect", p)
		}
	}
	// seg<1 falls back to a sane default (no panic, non-empty).
	if got := roundedRectPoints(0, 0, 10, 10, 2, 0); len(got) == 0 {
		t.Error("seg<1 should default, got empty polygon")
	}
}
