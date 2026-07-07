package app

import (
	"math"
	"testing"
)

func stitchCtxForTest() stitchCtx {
	return defaultStitchCtx(6, [4]float64{-1000, -1000, 1000, 1000})
}

// An SMD power pad gets a via OFF the pad (offset, not on center) plus a stub
// track from the pad to the via on the pad's layer.
func TestStitchOffsetsViaOffPad(t *testing.T) {
	pads := []pcbPadP{{Designator: "C1", Number: "1", Net: "GND", Layer: 1, X: 0, Y: 0}}
	res := planStitchViasForNet("GND", pads, nil, nil, nil, stitchCtxForTest())
	if len(res.Vias) != 1 || len(res.Stubs) != 1 {
		t.Fatalf("want 1 via + 1 stub, got %d/%d", len(res.Vias), len(res.Stubs))
	}
	v := res.Vias[0]
	if d := math.Hypot(v.X, v.Y); d < 25 {
		t.Errorf("via must sit OFF the pad (≥ offset), center distance %.1f", d)
	}
	s := res.Stubs[0]
	if !(s.X1 == 0 && s.Y1 == 0 && samePoint(s.X2, s.Y2, v.X, v.Y)) {
		t.Errorf("stub must run pad→via, got (%.1f,%.1f)→(%.1f,%.1f)", s.X1, s.Y1, s.X2, s.Y2)
	}
	if s.Layer != 1 {
		t.Errorf("stub layer = %d, want the pad's layer 1", s.Layer)
	}
}

// A through-hole pad already reaches the inner plane — no via, no stub.
func TestStitchSkipsThroughHolePads(t *testing.T) {
	pads := []pcbPadP{{Designator: "J2", Number: "1", Net: "+5V", Layer: 11, X: 0, Y: 0}}
	res := planStitchViasForNet("+5V", pads, nil, nil, nil, stitchCtxForTest())
	if len(res.Vias) != 0 || len(res.Stubs) != 0 || res.SkippedTH != 1 {
		t.Fatalf("TH pad must be skipped: vias=%d stubs=%d skippedTH=%d", len(res.Vias), len(res.Stubs), res.SkippedTH)
	}
}

// Same-net pads within shareDist fan into ONE via (EPAD case): 1 via, 2 stubs.
func TestStitchSharesViaAmongClosePads(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "U1", Number: "41", Net: "GND", Layer: 1, X: 0, Y: 0},
		{Designator: "U1", Number: "41", Net: "GND", Layer: 1, X: 35, Y: 0},
	}
	res := planStitchViasForNet("GND", pads, nil, nil, nil, stitchCtxForTest())
	if len(res.Vias) != 1 {
		t.Fatalf("close same-net pads must share one via, got %d", len(res.Vias))
	}
	if len(res.Stubs) != 2 || res.Shared != 1 {
		t.Errorf("want 2 stubs (one per pad) + shared=1, got stubs=%d shared=%d", len(res.Stubs), res.Shared)
	}
}

// A candidate spot inside another net's clearance band is rejected — the via
// lands in a different direction, still clear.
func TestStitchAvoidsOtherNetPad(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "C1", Number: "1", Net: "GND", Layer: 1, X: 0, Y: 0},
		// Other-net pad due EAST at 30mil — the first candidate direction is blocked.
		{Designator: "C1", Number: "2", Net: "+3V3", Layer: 1, X: 30, Y: 0},
	}
	ctx := stitchCtxForTest()
	res := planStitchViasForNet("GND", pads, nil, nil, nil, ctx)
	if len(res.Vias) != 1 {
		t.Fatalf("want 1 via, got %d", len(res.Vias))
	}
	v := res.Vias[0]
	band := ctx.clearance + ctx.viaDia/2 + nominalPadHalf
	if d := math.Hypot(v.X-30, v.Y-0); d < band {
		t.Errorf("via lands %.1fmil from the +3V3 pad — inside the %.1f clearance band", d, band)
	}
}

// Vias keep hole-to-hole distance from existing board vias regardless of net.
func TestStitchAvoidsExistingVias(t *testing.T) {
	pads := []pcbPadP{{Designator: "C1", Number: "1", Net: "GND", Layer: 1, X: 0, Y: 0}}
	// Existing same-net via NOT within shareDist... put a foreign via at the first
	// candidate spot (E, offset 30) so the planner must dodge it.
	vias := []pcbViaP{{ID: "v1", Net: "U0TXD", X: 30, Y: 0, Dia: 24}}
	ctx := stitchCtxForTest()
	res := planStitchViasForNet("GND", pads, nil, vias, nil, ctx)
	if len(res.Vias) != 1 {
		t.Fatalf("want 1 via, got %d", len(res.Vias))
	}
	v := res.Vias[0]
	if d := math.Hypot(v.X-30, v.Y-0); d < ctx.viaDia+ctx.clearance {
		t.Errorf("via lands %.1fmil from an existing via — under the via-to-via band", d)
	}
}

// A pad boxed in on all candidates is left unstitched (reported), never violated
// into place.
func TestStitchLeavesBoxedInPadUnplaced(t *testing.T) {
	pads := []pcbPadP{{Designator: "U9", Number: "1", Net: "GND", Layer: 1, X: 0, Y: 0}}
	// Fence of other-net pads at every candidate ring.
	for _, off := range []float64{30, 50} {
		for _, d := range stitchDirs {
			pads = append(pads, pcbPadP{Designator: "X", Net: "SIG", Layer: 1, X: d[0] * off, Y: d[1] * off})
		}
	}
	res := planStitchViasForNet("GND", pads, nil, nil, nil, stitchCtxForTest())
	if len(res.Vias) != 0 || res.Unplaced != 1 {
		t.Fatalf("boxed-in pad must be left unplaced: vias=%d unplaced=%d", len(res.Vias), res.Unplaced)
	}
}
