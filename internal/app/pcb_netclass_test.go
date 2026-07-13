package app

import (
	"math"
	"testing"
)

func TestNetRole(t *testing.T) {
	cases := []struct {
		net  string
		want string
	}{
		// Ground — always gnd, regardless of prefix.
		{"GND", roleGnd}, {"AGND", roleGnd}, {"DGND", roleGnd}, {"PGND", roleGnd}, {"GND1", roleGnd},
		// Connector-input / battery / bus rails → high-current (full board current).
		{"VBUS", roleHighCurrent}, {"VIN", roleHighCurrent}, {"VBAT", roleHighCurrent}, {"VSYS", roleHighCurrent},
		{"+VIN", roleHighCurrent},
		// High voltage → high-current.
		{"+12V", roleHighCurrent}, {"+9V", roleHighCurrent}, {"24V", roleHighCurrent},
		// Main rail 5–9V → trunk.
		{"+5V", rolePowerTrunk}, {"5V", rolePowerTrunk}, {"VCC5V0", rolePowerTrunk},
		// Regulated downstream rail <5V → branch.
		{"3V3", rolePowerBranch}, {"+3V3", rolePowerBranch}, {"1V8", rolePowerBranch}, {"1V2", rolePowerBranch},
		{"VDD_3V3", rolePowerBranch},
		// Voltage-less power names → branch.
		{"VCC", rolePowerBranch}, {"VDD", rolePowerBranch}, {"VREF", rolePowerBranch}, {"VOUT", rolePowerBranch},
		// Signals.
		{"USB_DP", roleSignal}, {"SDA", roleSignal}, {"MISO", roleSignal}, {"D0", roleSignal},
		{"5VUSB", roleSignal}, {"", roleSignal}, {"   ", roleSignal},
	}
	for _, c := range cases {
		if got := netRole(c.net); got != c.want {
			t.Errorf("netRole(%q) = %q, want %q", c.net, got, c.want)
		}
	}
}

func TestRailVoltage(t *testing.T) {
	cases := []struct {
		net string
		v   float64
		ok  bool
	}{
		{"+5V", 5, true}, {"5V", 5, true}, {"3V3", 3.3, true}, {"1V8", 1.8, true},
		{"+12V", 12, true}, {"5V0", 5, true}, {"VCC", 0, false}, {"GND", 0, false}, {"SDA", 0, false},
	}
	for _, c := range cases {
		v, ok := railVoltage(c.net)
		if ok != c.ok || (ok && math.Abs(v-c.v) > 1e-9) {
			t.Errorf("railVoltage(%q) = (%g, %v), want (%g, %v)", c.net, v, ok, c.v, c.ok)
		}
	}
}

func TestNetClassWidthTable(t *testing.T) {
	// Default rules (signal 10 / power 20 / min 5) → canonical §7.8 ladder.
	tbl := netClassWidthTable(defaultPcbRules())
	want := map[string]float64{
		roleSignal: 10, rolePowerBranch: 10, rolePowerTrunk: 15, roleHighCurrent: 20, roleGnd: 20,
	}
	for role, w := range want {
		if tbl[role] != w {
			t.Errorf("width[%s] = %g, want %g", role, tbl[role], w)
		}
	}

	// Ladder must stay monotonic (narrow→wide) even for a loose live default.
	loose := pcbRules{trackWidthMil: 12, powerWidthMil: 20, trackWidthMinMil: 5}
	lt := netClassWidthTable(loose)
	order := []string{roleSignal, rolePowerBranch, rolePowerTrunk, roleHighCurrent}
	for i := 1; i < len(order); i++ {
		if lt[order[i]] < lt[order[i-1]] {
			t.Errorf("non-monotonic ladder: %s(%g) < %s(%g)", order[i], lt[order[i]], order[i-1], lt[order[i-1]])
		}
	}

	// Every width floored at the legal minimum.
	tiny := pcbRules{trackWidthMil: 1, powerWidthMil: 1, trackWidthMinMil: 5}
	for role, w := range netClassWidthTable(tiny) {
		if w < 5 {
			t.Errorf("width[%s] = %g below clamp floor 5", role, w)
		}
	}
}
