package app

import (
	"sort"
	"testing"
)

func TestFindPowerNotPoured(t *testing.T) {
	pads := []pcbPadP{
		{Designator: "U1", Number: "1", Net: "GND"}, {Designator: "U1", Number: "2", Net: "GND"}, {Designator: "C1", Number: "1", Net: "GND"},
		{Designator: "U1", Number: "3", Net: "+5V"}, {Designator: "C1", Number: "2", Net: "+5V"},
		{Designator: "J1", Number: "1", Net: "VBUS"}, {Designator: "U1", Number: "4", Net: "VBUS"},
		{Designator: "U1", Number: "5", Net: "SDA"}, {Designator: "U2", Number: "1", Net: "SDA"}, // signal — never flagged
		{Designator: "U1", Number: "6", Net: "VREF"}, // single pad — skipped
	}
	poured := map[string]bool{"GND": true} // GND is poured, others are not

	out := findPowerNotPoured(pads, poured)
	got := map[string]bool{}
	for _, f := range out {
		if f.Type != "power-not-poured" || f.Level != "WARN" {
			t.Errorf("bad finding: %+v", f)
		}
		got[f.Net] = true
	}
	want := []string{"+5V", "VBUS"}
	if len(got) != len(want) {
		t.Fatalf("got %d findings (%v), want %v", len(got), keys(got), want)
	}
	for _, n := range want {
		if !got[n] {
			t.Errorf("expected power-not-poured for %s, got %v", n, keys(got))
		}
	}
	if got["GND"] {
		t.Error("GND is poured — must not be flagged")
	}
	if got["SDA"] {
		t.Error("SDA is a signal — must not be flagged")
	}
	if got["VREF"] {
		t.Error("VREF has a single pad — must not be flagged")
	}
}

func TestFindWidthUnderSpec(t *testing.T) {
	widths := netClassWidthTable(defaultPcbRules()) // signal10 branch10 trunk15 high20 gnd20
	tracks := []pcbTrack{
		{ID: "t1", Net: "+5V", Width: 8, X1: 0, Y1: 0, X2: 100, Y2: 0},     // trunk 15 → under
		{ID: "t2", Net: "+5V", Width: 20, X1: 100, Y1: 0, X2: 200, Y2: 0},  // ok
		{ID: "t3", Net: "VBUS", Width: 10, X1: 0, Y1: 50, X2: 100, Y2: 50}, // high 20 → under
		{ID: "t4", Net: "3V3", Width: 10, X1: 0, Y1: 100, X2: 100, Y2: 100}, // branch 10 → ok
		{ID: "t5", Net: "SDA", Width: 4, X1: 0, Y1: 150, X2: 100, Y2: 150},  // signal → exempt
		{ID: "t6", Net: "GND", Width: 8, X1: 0, Y1: 200, X2: 30, Y2: 200},   // short stub on a via → exempt
	}
	vias := []pcbViaP{{Net: "GND", X: 0, Y: 200}}

	out := findWidthUnderSpec(tracks, nil, vias, widths)
	byNet := map[string]pcbCheckFinding{}
	for _, f := range out {
		if f.Type != "width-under-spec" || f.Level != "WARN" {
			t.Errorf("bad finding: %+v", f)
		}
		byNet[f.Net] = f
	}
	if len(out) != 2 {
		t.Fatalf("got %d findings (%v), want 2 (+5V, VBUS)", len(out), keys2(byNet))
	}
	if f, ok := byNet["+5V"]; !ok || f.Widths[0] != 8 || f.Widths[1] != 15 {
		t.Errorf("+5V finding wrong: %+v", f)
	}
	if f, ok := byNet["VBUS"]; !ok || f.Widths[1] != 20 {
		t.Errorf("VBUS finding wrong: %+v", f)
	}
	if _, ok := byNet["3V3"]; ok {
		t.Error("3V3 at branch spec 10 must not be flagged")
	}
	if _, ok := byNet["SDA"]; ok {
		t.Error("SDA (signal) must not be flagged")
	}
	if _, ok := byNet["GND"]; ok {
		t.Error("GND short stub on a via must be exempt")
	}
}

func TestTrackIsStitchStub(t *testing.T) {
	vias := []pcbViaP{{Net: "GND", X: 0, Y: 0}}
	cases := []struct {
		name string
		t    pcbTrack
		want bool
	}{
		{"short stub on same-net via", pcbTrack{Net: "GND", X1: 0, Y1: 0, X2: 30, Y2: 0}, true},
		{"long track on via", pcbTrack{Net: "GND", X1: 0, Y1: 0, X2: 100, Y2: 0}, false},
		{"short track other-net via", pcbTrack{Net: "+5V", X1: 0, Y1: 0, X2: 30, Y2: 0}, false},
		{"short track no via nearby", pcbTrack{Net: "GND", X1: 500, Y1: 500, X2: 520, Y2: 500}, false},
	}
	for _, c := range cases {
		if got := trackIsStitchStub(c.t, vias); got != c.want {
			t.Errorf("%s: trackIsStitchStub = %v, want %v", c.name, got, c.want)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keys2(m map[string]pcbCheckFinding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
