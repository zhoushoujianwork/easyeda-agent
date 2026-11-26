package app

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// ── issue #127: diff-pair identification ────────────────────────────────────

func TestIdentifyDiffPairsByName(t *testing.T) {
	nets := []string{"USB_DP", "USB_DM", "RS485_P", "RS485_N", "CAN+", "CAN-", "GND", "3V3", "IO12", "HOST_DP", "HOST_DM"}
	pairs := identifyDiffPairsByName(nets)
	got := map[string]string{}
	for _, p := range pairs {
		got[p.NetP] = p.NetN
	}
	for _, want := range [][2]string{{"USB_DP", "USB_DM"}, {"RS485_P", "RS485_N"}, {"CAN+", "CAN-"}, {"HOST_DP", "HOST_DM"}} {
		if got[want[0]] != want[1] {
			t.Errorf("expected pair %s/%s, got map %v", want[0], want[1], got)
		}
	}
	if len(pairs) != 4 {
		t.Errorf("expected exactly 4 pairs, got %+v", pairs)
	}
	// GND/3V3/IO12 must never pair.
	for _, p := range pairs {
		if p.NetP == "GND" || p.NetP == "3V3" || strings.HasPrefix(p.NetP, "IO") {
			t.Errorf("false positive pair: %+v", p)
		}
	}
}

// TestIdentifyDiffPairsFromBlocks: live nets matching a block's diff_pair
// declaration get the block's impedance + skew budget (ch340c USB_D:
// length_match 0.15mm ≈ 5.9mil, 90Ω).
func TestIdentifyDiffPairsFromBlocks(t *testing.T) {
	nets := []string{"USB_DP", "USB_DM", "GND"}
	pairs := identifyDiffPairsFromBlocks(nets)
	var usb *rcDiffPair
	for i := range pairs {
		if strings.EqualFold(pairs[i].NetP, "USB_DP") || strings.EqualFold(pairs[i].NetN, "USB_DP") {
			usb = &pairs[i]
			break
		}
	}
	if usb == nil {
		t.Fatalf("block-informed USB pair not found: %+v", pairs)
	}
	if usb.ImpedanceOhm != 90 {
		t.Errorf("expected 90Ω from the block, got %+v", usb)
	}
	if usb.SkewLimitMil < 5.8 || usb.SkewLimitMil > 6.0 {
		t.Errorf("expected skew budget ≈5.9mil (0.15mm), got %.2f", usb.SkewLimitMil)
	}
	if !strings.HasPrefix(usb.Source, "block:") {
		t.Errorf("expected block source, got %q", usb.Source)
	}
}

// TestIdentifyDiffPairsMerge: block metadata wins over the name-pattern dup.
func TestIdentifyDiffPairsMerge(t *testing.T) {
	pairs := identifyDiffPairs([]string{"USB_DP", "USB_DM"})
	n := 0
	for _, p := range pairs {
		a, b := strings.ToUpper(p.NetP), strings.ToUpper(p.NetN)
		if (a == "USB_DP" && b == "USB_DM") || (a == "USB_DM" && b == "USB_DP") {
			n++
			if p.ImpedanceOhm != 90 {
				t.Errorf("merged pair lost block metadata: %+v", p)
			}
		}
	}
	if n != 1 {
		t.Errorf("expected the USB pair exactly once after merge, got %d (%+v)", n, pairs)
	}
}

// TestPlanPairRoute: two pads per net, same layer, short hops → both sides
// routed, lengths measured, skew within the default budget for a symmetric
// geometry; all other nets untouched.
func TestPlanPairRoute(t *testing.T) {
	mk := func(des string, x, y float64, nets ...string) apComp {
		c := apComp{id: des, designator: des, x: x, y: y, hasBBox: true,
			minX: x - 10, minY: y - 10, maxX: x + 10, maxY: y + 10}
		for i, n := range nets {
			// Real 10×10 pads: a sizeless pad is a nominal 24mil circle, which at
			// 20mil pitch leaves no copper-to-copper room for any track (#310).
			c.pads = append(c.pads, apPad{num: string(rune('1' + i)), net: n, x: x, y: y + float64(i)*20, layer: 1, w: 10, h: 10})
		}
		return c
	}
	comps := []apComp{
		mk("J1", 0, 0, "USB_DP", "USB_DM"),
		mk("U1", 300, 0, "USB_DP", "USB_DM"),
		mk("R1", 150, 200, "IO5"), mk("U2", 350, 200, "IO5"),
	}
	opt := defaultRtOptions()
	opt.corner = "45"
	opt.skipPower = true
	pair := rcDiffPair{Name: "USB_D", NetP: "USB_DP", NetN: "USB_DM", SkewLimitMil: rcDefaultSkewMil}
	res := planPairRoute(comps, pair, map[string]bool{}, opt)
	if res.Status != "routed" {
		t.Fatalf("expected routed, got %+v", res)
	}
	if res.LenPMil <= 0 || res.LenNMil <= 0 {
		t.Fatalf("lengths not measured: %+v", res)
	}
	if !res.WithinSkew {
		t.Errorf("symmetric geometry should be within skew: %+v", res)
	}
	for _, s := range res.Segs {
		if u := strings.ToUpper(s.Net); u != "USB_DP" && u != "USB_DM" {
			t.Errorf("planner touched a non-pair net: %+v", s)
		}
	}
	// Already-routed pair short-circuits.
	res2 := planPairRoute(comps, pair, map[string]bool{"USB_DP": true}, opt)
	if res2.Status != "already-routed" {
		t.Errorf("expected already-routed, got %+v", res2)
	}
}

// ── T-11: copper-layer counting on a 2-layer board ──────────────────────────

// layer 组一条 pcb.layers.list 里的 layer 项。
func layer(id int, name, typ string, status int) map[string]any {
	return map[string]any{"id": float64(id), "name": name, "type": typ, "layerStatus": float64(status)}
}

// twoLayerBoard 复刻实测(EasyEDA Pro 3.2.135)2 层板的 pcb.layers.list 形状:
// 真正的两层铜是 type=TOP / BOTTOM(layerStatus=1),而 Inner1..Inner32 全部存在、
// 全部 type=SIGNAL 且 layerStatus=0(EPCB_LayerStatus.NOT_USED,未启用)。
func twoLayerBoard() []any {
	out := []any{
		layer(1, "Top Layer", "TOP", 1),
		layer(2, "Bottom Layer", "BOTTOM", 1),
		layer(3, "Top Silkscreen Layer", "TOP_SILK", 1),
		layer(4, "Bottom Silkscreen Layer", "BOT_SILK", 1),
		layer(12, "Multi-Layer", "MULTI", 1),
	}
	for i := 1; i <= 32; i++ {
		out = append(out, layer(14+i, fmt.Sprintf("Inner%d", i), "SIGNAL", 0))
	}
	return out
}

func TestCopperLayerCountFromResult(t *testing.T) {
	fourLayer := append([]any{}, twoLayerBoard()...)
	for i, li := range fourLayer {
		m := li.(map[string]any)
		if m["name"] == "Inner1" || m["name"] == "Inner2" {
			m["layerStatus"] = float64(1)
			fourLayer[i] = m
		}
	}

	type countCase struct {
		name    string
		result  map[string]any
		want    int
		wantOK  bool
		wantSrc string
	}
	cases := []countCase{{
		name:    "platform copperLayerCount wins",
		result:  map[string]any{"copperLayerCount": float64(2), "layers": twoLayerBoard()},
		want:    2,
		wantOK:  true,
		wantSrc: "pcb.layers.list.copperLayerCount",
	}, {
		// T-11 的回归:没有平台字段时,32 个未启用的 Inner 层不得被算成铜层。
		name:    "2-layer board with 32 disabled inner layers counts 2",
		result:  map[string]any{"layers": twoLayerBoard()},
		want:    2,
		wantOK:  true,
		wantSrc: "pcb.layers.list.layers[] (enabled copper)",
	}, {
		name:    "4-layer board counts the two ENABLED inner layers",
		result:  map[string]any{"layers": fourLayer},
		want:    4,
		wantOK:  true,
		wantSrc: "pcb.layers.list.layers[] (enabled copper)",
	}, {
		name: "an enabled PLANE inner layer counts as copper",
		result: map[string]any{"layers": []any{
			layer(1, "Top Layer", "TOP", 1),
			layer(2, "Bottom Layer", "BOTTOM", 1),
			layer(15, "Inner1", "PLANE", 1),
			layer(16, "Inner2", "SIGNAL", 2), // HIDDEN is USED, just not shown
			layer(17, "Inner3", "SIGNAL", 0),
		}},
		want:    4,
		wantOK:  true,
		wantSrc: "pcb.layers.list.layers[] (enabled copper)",
	}, {
		name:   "a bogus platform count below 2 falls through to layers[]",
		result: map[string]any{"copperLayerCount": float64(0), "layers": twoLayerBoard()},
		want:   2,
		wantOK: true,
	}, {
		name:   "nothing readable is reported as no evidence, not as 32",
		result: map[string]any{},
		want:   0,
		wantOK: false,
	}, {
		name:   "only non-copper layers is no evidence",
		result: map[string]any{"layers": []any{layer(3, "Top Silkscreen Layer", "TOP_SILK", 1)}},
		want:   0,
		wantOK: false,
	}, {
		// 没有状态，不能证明铜层已经启用。
		name: "a layer without layerStatus is not reliable evidence",
		result: map[string]any{"layers": []any{
			map[string]any{"id": float64(1), "name": "Top Layer", "type": "TOP"},
			map[string]any{"id": float64(2), "name": "Bottom Layer", "type": "BOTTOM"},
		}},
		want:   0,
		wantOK: false,
	}}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 2.5, 3, 34} {
		cases = append(cases, countCase{name: fmt.Sprintf("invalid platform count %v", bad), result: map[string]any{"copperLayerCount": bad}})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, src, ok := copperLayerCountFromResult(tc.result)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (got %d from %q)", ok, tc.wantOK, got, src)
			}
			if got != tc.want {
				t.Errorf("count = %d, want %d (source %q)", got, tc.want, src)
			}
			if tc.wantSrc != "" && src != tc.wantSrc {
				t.Errorf("source = %q, want %q", src, tc.wantSrc)
			}
		})
	}
}

// TestCopperLayerCountNeverMistakesTwoForFour 是 T-11 的门:2 层板绝不能落进
// route-critical 的 power-planes 分支(该分支会 pcb.stackup.set{count:4} 改板)。
func TestCopperLayerCountNeverMistakesTwoForFour(t *testing.T) {
	for _, res := range []map[string]any{
		{"copperLayerCount": float64(2), "layers": twoLayerBoard()},
		{"layers": twoLayerBoard()},
	} {
		n, src, ok := copperLayerCountFromResult(res)
		if !ok || n >= 4 {
			t.Fatalf("2-layer board resolved to %d layers (ok=%v, source=%q) — power-planes would re-stack it", n, ok, src)
		}
	}
}
