package app

import (
	"reflect"
	"sort"
	"testing"
)

// TestConnsFromLiveNets: a captured netlist flattens to one autoconnect
// connection per pin, kind inferred from the net name, "D.N" → "D:N", and
// single-pin nets are skipped (nothing to tie).
func TestConnsFromLiveNets(t *testing.T) {
	live := map[string]map[string]bool{
		"GND":    {"U1.57": true, "C1.2": true},
		"3V3":    {"U1.2": true, "C1.1": true},
		"SOLO":   {"R9.1": true}, // single pin → skipped
		"C1_N3":  {"U1.29": true, "U2.8": true},
	}
	got := connsFromLiveNets(live)

	// Expect 6 connections (2+2+2), SOLO dropped.
	if len(got) != 6 {
		t.Fatalf("got %d connections, want 6 (single-pin net dropped): %+v", len(got), got)
	}
	// Kind inference + pin-ref rewrite, checked per net.
	byNet := map[string][]acConnSpec{}
	for _, c := range got {
		byNet[c.Net] = append(byNet[c.Net], c)
	}
	if _, ok := byNet["SOLO"]; ok {
		t.Error("single-pin net SOLO must be skipped")
	}
	for _, c := range byNet["GND"] {
		if c.Kind != "gnd" {
			t.Errorf("GND pin kind = %q, want gnd", c.Kind)
		}
	}
	for _, c := range byNet["3V3"] {
		if c.Kind != "power" {
			t.Errorf("3V3 pin kind = %q, want power", c.Kind)
		}
	}
	for _, c := range byNet["C1_N3"] {
		if c.Kind != "netport" {
			t.Errorf("C1_N3 pin kind = %q, want netport", c.Kind)
		}
	}
	// "U1.57" → "U1:57"
	var gndRefs []string
	for _, c := range byNet["GND"] {
		gndRefs = append(gndRefs, c.PinRef)
	}
	sort.Strings(gndRefs)
	if !reflect.DeepEqual(gndRefs, []string{"C1:2", "U1:57"}) {
		t.Errorf("GND pin refs = %v, want [C1:2 U1:57]", gndRefs)
	}
}

func TestCountNets(t *testing.T) {
	live := map[string]map[string]bool{
		"A": {"U1.1": true, "U2.1": true},
		"B": {"U3.1": true}, // single pin
		"C": {"U4.1": true, "U5.1": true},
	}
	if n := countNets(live); n != 2 {
		t.Errorf("countNets = %d, want 2 (single-pin net not counted)", n)
	}
}
