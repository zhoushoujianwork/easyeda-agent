package app

import (
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
			c.pads = append(c.pads, apPad{num: string(rune('1' + i)), net: n, x: x, y: y + float64(i)*20, layer: 1})
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
