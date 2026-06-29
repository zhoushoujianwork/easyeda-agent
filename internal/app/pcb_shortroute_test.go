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
	segs, diags := planShortRoutes(routeBoard(), map[string]bool{}, defaultRtOptions())

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

// Already-routed nets are left alone.
func TestPlanShortRoutes_SkipAlreadyRouted(t *testing.T) {
	board := routeBoard()
	segs, _ := planShortRoutes(board, map[string]bool{"EN": true}, defaultRtOptions())
	for _, s := range segs {
		if s.Net == "EN" {
			t.Fatal("EN was marked already-routed; must not be re-routed")
		}
	}
}
