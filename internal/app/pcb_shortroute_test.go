package app

import (
	"strings"
	"testing"
)

// Post-auto-place geometry: satellites already hug U1's pins, so the per-net hops
// are short and clear — exactly what route-short v1 targets.
func routeBoard() []apComp {
	return []apComp{
		mkComp("u1", "U1", 2395, -1855, 748, 1030, []apPad{
			p("3", "EN", 2050, -1740), p("27", "IO0", 2740, -2290),
			p("1", "GND", 2050, -1640), p("2", "3V3", 2050, -1690),
		}),
		mkComp("c3", "C3", 1943, -1740, 75, 45, []apPad{p("2", "EN", 1943, -1740), p("1", "GND", 1943, -1700)}),
		mkComp("r1", "R1", 1940, -1664, 80, 45, []apPad{p("2", "EN", 1940, -1664), p("1", "3V3", 1940, -1624)}),
		mkComp("r2", "R2", 2849, -2290, 80, 45, []apPad{p("1", "IO0", 2849, -2290), p("2", "3V3", 2849, -2250)}),
		// A net whose two pads are far apart → too long for the short tier.
		mkComp("j1", "J1", 0, 0, 50, 50, []apPad{p("1", "FAR", 0, 0)}),
		mkComp("j2", "J2", 5000, 0, 50, 50, []apPad{p("1", "FAR", 5000, 0)}),
		// A net spanning two layers → needs a via, skip in v1.
		mkComp("u2", "U2", 100, 100, 50, 50, []apPad{p("1", "XL", 100, 100)}),
		mkComp("u3", "U3", 150, 100, 50, 50, []apPad{{num: "1", net: "XL", x: 150, y: 100, layer: 2}}),
	}
}

func TestPlanShortRoutes(t *testing.T) {
	// Base (single-layer) tier: too-long / cross-layer hops defer to diagnostics.
	opt := defaultRtOptions()
	opt.multilayer = false
	segs, _, diags := planShortRoutes(routeBoard(), map[string]bool{}, opt)

	routedNets := map[string]bool{}
	for _, s := range segs {
		routedNets[s.Net] = true
		if s.Layer != 1 {
			t.Errorf("net %s segment on layer %d, want 1", s.Net, s.Layer)
		}
	}
	if !routedNets["EN"] {
		t.Error("EN should be routed (3 short same-layer pads)")
	}
	if !routedNets["IO0"] {
		t.Error("IO0 should be routed (2 short pads)")
	}
	if routedNets["GND"] {
		t.Error("GND must be skipped by default (poured)")
	}
	if routedNets["FAR"] {
		t.Error("FAR is too long — must not be routed")
	}
	if routedNets["XL"] {
		t.Error("XL is cross-layer — must not be routed without a via")
	}

	// IO0's two pads share y → a single straight horizontal segment.
	var io0 []rtSeg
	for _, s := range segs {
		if s.Net == "IO0" {
			io0 = append(io0, s)
		}
	}
	if len(io0) != 1 || io0[0].Y1 != io0[0].Y2 {
		t.Errorf("IO0 want 1 straight horizontal seg, got %+v", io0)
	}

	// Diagnostics must explain GND / FAR / XL.
	joined := ""
	for _, d := range diags {
		joined += d.Net + ":" + d.Reason + "\n"
	}
	for _, want := range []string{"GND", "too long", "via"} {
		if !strings.Contains(joined, want) {
			t.Errorf("diagnostics missing %q; got:\n%s", want, joined)
		}
	}
}

// Multilayer tier (default): the hops the single-layer tier defers — too-long
// (FAR) and cross-layer (XL) — get routed with a via detour instead.
func TestPlanShortRoutes_Multilayer(t *testing.T) {
	segs, vias, diags := planShortRoutes(routeBoard(), map[string]bool{}, defaultRtOptions())

	routedNets := map[string]bool{}
	viaNets := map[string]int{}
	usesLayer2 := map[string]bool{}
	for _, s := range segs {
		routedNets[s.Net] = true
		if s.Layer == 2 {
			usesLayer2[s.Net] = true
		}
	}
	for _, v := range vias {
		viaNets[v.Net]++
	}

	// FAR (too long, same layer) is now routed with a 2-via detour on layer 2.
	if !routedNets["FAR"] {
		t.Error("FAR should be routed via multilayer detour")
	}
	if viaNets["FAR"] != 2 {
		t.Errorf("FAR wants 2 vias (down + up), got %d", viaNets["FAR"])
	}
	if !usesLayer2["FAR"] {
		t.Error("FAR's trunk should ride layer 2")
	}
	// XL (cross-layer) is routed with a single layer-change via.
	if !routedNets["XL"] {
		t.Error("XL cross-layer hop should be routed via one via")
	}
	if viaNets["XL"] != 1 {
		t.Errorf("XL wants 1 layer-change via, got %d", viaNets["XL"])
	}
	// Power/ground still deferred (poured), and no bogus "too long" diag survives.
	joined := ""
	for _, d := range diags {
		joined += d.Net + ":" + d.Reason + "\n"
	}
	if !strings.Contains(joined, "GND") {
		t.Errorf("GND should still be a diagnostic (poured); got:\n%s", joined)
	}
	if strings.Contains(joined, "too long") || strings.Contains(joined, "needs a via") {
		t.Errorf("multilayer routed the deferred hops; no maze/via diag should remain; got:\n%s", joined)
	}
}

// Already-routed nets are left alone.
func TestPlanShortRoutes_SkipAlreadyRouted(t *testing.T) {
	board := routeBoard()
	segs, _, _ := planShortRoutes(board, map[string]bool{"EN": true}, defaultRtOptions())
	for _, s := range segs {
		if s.Net == "EN" {
			t.Fatal("EN was marked already-routed; must not be re-routed")
		}
	}
}

// Track width follows net class: power/ground nets get the fatter powerWidth,
// signals get signalWidth, and an explicit --width overrides both.
func TestPlanShortRoutes_WidthByClass(t *testing.T) {
	segs, _, _ := planShortRoutes(routeBoard(), map[string]bool{}, defaultRtOptions())
	for _, s := range segs {
		want := 10.0 // signal default
		if s.Net == "3V3" {
			want = 20.0 // power default
		}
		if s.Width != want {
			t.Errorf("net %s width %.0f, want %.0f", s.Net, s.Width, want)
		}
	}

	opt := defaultRtOptions()
	opt.width = 8 // global override wins for every class
	forced, _, _ := planShortRoutes(routeBoard(), map[string]bool{}, opt)
	for _, s := range forced {
		if s.Width != 8 {
			t.Errorf("--width override: net %s width %.0f, want 8", s.Net, s.Width)
		}
	}
}

// A clean diagonal hop, one straight net across two parts, for corner-style tests.
func twoPadNet(net string, ax, ay, bx, by float64) []apComp {
	return []apComp{
		mkComp("a", "A", ax, ay, 50, 50, []apPad{p("1", net, ax, ay)}),
		mkComp("b", "B", bx, by, 50, 50, []apPad{p("1", net, bx, by)}),
	}
}

func TestRouteHop_CornerStyles(t *testing.T) {
	board := twoPadNet("SIG", 0, 0, 100, 60) // dx=100, dy=60 → a real corner

	// 90°: two axis-aligned segments, no diagonal.
	opt := defaultRtOptions()
	opt.corner = "90"
	segs, _, _ := planShortRoutes(board, map[string]bool{}, opt)
	if len(segs) != 2 {
		t.Fatalf("90° want 2 segs, got %d: %+v", len(segs), segs)
	}
	for _, s := range segs {
		if s.X1 != s.X2 && s.Y1 != s.Y2 {
			t.Errorf("90° segment is diagonal: %+v", s)
		}
	}

	// 45°: a chamfer — exactly one segment whose run is a true 45° (|dx|==|dy|).
	opt.corner = "45"
	segs45, _, _ := planShortRoutes(board, map[string]bool{}, opt)
	diag := 0
	for _, s := range segs45 {
		if dx, dy := absf(s.X2-s.X1), absf(s.Y2-s.Y1); dx != 0 && dy != 0 {
			diag++
			if dx != dy {
				t.Errorf("45° diagonal not at 45° (dx=%.0f dy=%.0f)", dx, dy)
			}
		}
	}
	if diag != 1 {
		t.Errorf("45° want exactly 1 diagonal segment, got %d", diag)
	}

	// round: a chord-approximated fillet → more segments than the bare L.
	opt.corner = "round"
	segsR, _, _ := planShortRoutes(board, map[string]bool{}, opt)
	if len(segsR) <= 2 {
		t.Errorf("round want >2 chord segments, got %d", len(segsR))
	}

	// Endpoints are preserved for every style (route still connects a→b).
	for _, segs := range [][]rtSeg{segs, segs45, segsR} {
		if !connectsEnds(segs, 0, 0, 100, 60) {
			t.Errorf("route does not span (0,0)→(100,60): %+v", segs)
		}
	}
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// connectsEnds checks the segment list starts at (ax,ay) and ends at (bx,by),
// i.e. the corner styling kept the pad endpoints intact.
func connectsEnds(segs []rtSeg, ax, ay, bx, by float64) bool {
	if len(segs) == 0 {
		return false
	}
	first, last := segs[0], segs[len(segs)-1]
	return first.X1 == ax && first.Y1 == ay && last.X2 == bx && last.Y2 == by
}
