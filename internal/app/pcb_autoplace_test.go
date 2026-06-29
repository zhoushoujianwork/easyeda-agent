package app

import (
	"math"
	"testing"
)

// Real geometry pulled from the fixed ESP32-S3 regression board (ceshi):
// `easyeda pcb list --include-pads --include-bbox`. U1 is the lone main chip;
// the 7 satellites must hug the U1 edge nearest the pad they connect to.

// mkComp builds a component whose bbox is centered on (cx,cy) with size w×h, so
// the anchor↔bbox-center offset is zero (the planner preserves it either way).
func mkComp(id, des string, cx, cy, w, h float64, pads []apPad) apComp {
	return apComp{
		id: id, designator: des, x: cx, y: cy, hasBBox: true,
		minX: cx - w/2, maxX: cx + w/2, minY: cy - h/2, maxY: cy + h/2,
		pads: pads,
	}
}

// p builds a TOP-layer pad (layer 1) — all fixture parts are single-sided.
func p(num, net string, x, y float64) apPad {
	return apPad{num: num, net: net, x: x, y: y, layer: 1}
}

func ceshiBoard() []apComp {
	// U1 key pads at their real coordinates (left edge: GND/3V3/EN; right edge:
	// IO0/BLINK/GND), plus a couple of center thermal GND pads to push the pin
	// count over the main-chip threshold.
	u1pads := []apPad{
		p("1", "GND", 2050, -1640), p("2", "3V3", 2050, -1690), p("3", "EN", 2050, -1740),
		p("27", "IO0", 2740, -2290), p("38", "BLINK", 2740, -1740), p("40", "GND", 2740, -1640),
		p("41", "GND", 2336, -1944), p("42", "", 2400, -2360), p("43", "", 2400, -1350),
		p("44", "", 2100, -2360),
	}
	return []apComp{
		mkComp("u1", "U1", 2395, -1855, 748, 1030, u1pads),
		mkComp("c3", "C3", 259, -1128, 74.8, 45.3, []apPad{p("2", "EN", 0, 0), p("1", "GND", 0, 0)}),
		mkComp("r1", "R1", 1855, -1128, 80.4, 45.3, []apPad{p("2", "EN", 0, 0), p("1", "3V3", 0, 0)}),
		mkComp("r2", "R2", 259, -2209, 80.3, 45.3, []apPad{p("2", "3V3", 0, 0), p("1", "IO0", 0, 0)}),
		mkComp("r3", "R3", 1057, -2209, 80.3, 45.3, []apPad{p("2", "LED_A", 0, 0), p("1", "BLINK", 0, 0)}),
		mkComp("c2", "C2", 2509, -652, 160.6, 77.2, []apPad{p("2", "3V3", 0, 0), p("1", "GND", 0, 0)}),
		mkComp("led1", "LED1", 1062, -1128, 176, 91, []apPad{p("2", "GND", 0, 0), p("1", "LED_A", 0, 0)}),
		mkComp("c1", "C1", 2827, -636, 74.8, 45.3, []apPad{p("2", "GND", 0, 0), p("1", "3V3", 0, 0)}),
	}
}

func TestPlanAutoPlace_Ceshi(t *testing.T) {
	comps := ceshiBoard()
	moves, diags := planAutoPlace(comps, defaultApOptions())

	if len(diags) != 0 {
		t.Fatalf("expected every satellite placed, got diags: %+v", diags)
	}
	if len(moves) != 7 {
		t.Fatalf("expected 7 satellites moved, got %d", len(moves))
	}

	byDes := map[string]apMove{}
	for _, m := range moves {
		byDes[m.Designator] = m
		if m.Main != "U1" {
			t.Errorf("%s anchored to %q, want U1", m.Designator, m.Main)
		}
	}

	// Expected edge + connecting net for each satellite.
	want := map[string]struct {
		edge string
		net  string
	}{
		"C3": {"left", "EN"}, "R1": {"left", "EN"},
		"C1": {"left", "3V3"}, "C2": {"left", "3V3"},
		"R2": {"right", "IO0"}, "R3": {"right", "BLINK"},
		"LED1": {"right", "LED_A"},
	}
	for des, w := range want {
		m, ok := byDes[des]
		if !ok {
			t.Errorf("%s not placed", des)
			continue
		}
		if m.Edge != w.edge {
			t.Errorf("%s edge=%s, want %s", des, m.Edge, w.edge)
		}
		if m.TargetNet != w.net {
			t.Errorf("%s targetNet=%s, want %s", des, m.TargetNet, w.net)
		}
	}
	// LED1 must chain off R3 (it shares LED_A with no chip pad).
	if v := byDes["LED1"].Via; v != "chain:R3" {
		t.Errorf("LED1 via=%q, want chain:R3", v)
	}

	// Every satellite must end OUTSIDE the U1 bbox, on the correct side.
	u1 := comps[0]
	for des, m := range byDes {
		switch want[des].edge {
		case "left":
			if m.NewX >= u1.minX {
				t.Errorf("%s newX=%.1f not left of U1 (minX=%.1f)", des, m.NewX, u1.minX)
			}
		case "right":
			if m.NewX <= u1.maxX {
				t.Errorf("%s newX=%.1f not right of U1 (maxX=%.1f)", des, m.NewX, u1.maxX)
			}
		}
	}

	// No two satellites may overlap after placement.
	sizes := map[string][2]float64{}
	for _, c := range comps {
		sizes[c.designator] = [2]float64{c.width(), c.height()}
	}
	ms := moves
	for i := 0; i < len(ms); i++ {
		for j := i + 1; j < len(ms); j++ {
			a, b := ms[i], ms[j]
			sa, sb := sizes[a.Designator], sizes[b.Designator]
			dx := math.Abs(a.NewX - b.NewX)
			dy := math.Abs(a.NewY - b.NewY)
			if dx < (sa[0]+sb[0])/2 && dy < (sa[1]+sb[1])/2 {
				t.Errorf("overlap: %s(%.0f,%.0f) vs %s(%.0f,%.0f)", a.Designator, a.NewX, a.NewY, b.Designator, b.NewX, b.NewY)
			}
		}
	}
}

// A board with no chip (only 2-pad parts) places nothing and says why.
func TestPlanAutoPlace_NoMainChip(t *testing.T) {
	comps := []apComp{
		mkComp("r1", "R1", 0, 0, 80, 45, []apPad{p("1", "A", 0, 0), p("2", "B", 0, 0)}),
		mkComp("c1", "C1", 100, 0, 75, 45, []apPad{p("1", "B", 0, 0), p("2", "GND", 0, 0)}),
	}
	moves, diags := planAutoPlace(comps, defaultApOptions())
	if len(moves) != 0 {
		t.Errorf("expected no moves without a main chip, got %d", len(moves))
	}
	if len(diags) != 2 {
		t.Errorf("expected a diag per satellite, got %d", len(diags))
	}
}
