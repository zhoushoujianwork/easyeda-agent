package app

import (
	"strings"
	"testing"
)

func TestBindPlaneNets(t *testing.T) {
	planes := []pcbPlaneLayer{
		{Layer: 15, Name: "Inner1"},
		{Layer: 16, Name: "Inner2"},
	}
	pours := []pcbPourP{
		{ID: "p1", Net: "GND", Layer: 15},
		{ID: "p2", Net: "GND", Layer: 15},  // duplicate net → dedup
		{ID: "p3", Net: "", Layer: 15},     // netless → does not bind (R11 covers it)
		{ID: "p4", Net: "+3V3", Layer: 16}, // other layer
		{ID: "p5", Net: "GND", Layer: 1},   // top-layer pour — irrelevant to inner planes
	}
	got := bindPlaneNets(planes, pours)
	if len(got[0].Nets) != 1 || got[0].Nets[0] != "GND" {
		t.Errorf("layer 15 nets = %v, want [GND]", got[0].Nets)
	}
	if len(got[1].Nets) != 1 || got[1].Nets[0] != "+3V3" {
		t.Errorf("layer 16 nets = %v, want [+3V3]", got[1].Nets)
	}
}

func TestFindViaCrossesPlane(t *testing.T) {
	planes := []pcbPlaneLayer{{Layer: 15, Name: "Inner1", Nets: []string{"GND"}}}
	vias := []pcbViaP{
		{ID: "v1", Net: "GND", X: 100, Y: 100},  // matches plane net → OK
		{ID: "v2", Net: "+5V", X: 200, Y: 200},  // foreign net → flag (the issue #30 case)
		{ID: "v3", Net: "U0TXD", X: 300, Y: 50}, // foreign signal net → flag
		{ID: "v4", Net: "", X: 400, Y: 400},     // netless via → flag (foreign to GND)
	}
	got := findViaCrossesPlane(vias, planes)
	if len(got) != 3 {
		t.Fatalf("got %d findings, want 3: %+v", len(got), got)
	}
	flagged := map[string]bool{}
	for _, f := range got {
		if f.Type != "via-crosses-plane" || f.Level != "WARN" {
			t.Errorf("bad finding type/level: %+v", f)
		}
		if f.Layer != 15 {
			t.Errorf("finding should carry the plane layer: %+v", f)
		}
		if len(f.Primitives) != 1 {
			t.Errorf("expected via primitive id attached: %+v", f)
		} else {
			flagged[f.Primitives[0]] = true
		}
		if !strings.Contains(f.Message, "pro-api-sdk#32") || !strings.Contains(f.Message, "doc reload") {
			t.Errorf("message must cite the official bug + fix guidance: %q", f.Message)
		}
	}
	if flagged["v1"] || !flagged["v2"] || !flagged["v3"] || !flagged["v4"] {
		t.Errorf("wrong vias flagged: %v", flagged)
	}
}

func TestFindViaCrossesPlaneNetlessPlane(t *testing.T) {
	// A PLANE layer with no net-bound pour: net unknown → one WARN about the
	// plane itself, and NO per-via findings (we can't tell foreign from own).
	planes := []pcbPlaneLayer{{Layer: 15, Name: "Inner1"}}
	vias := []pcbViaP{{ID: "v1", Net: "+5V"}, {ID: "v2", Net: "GND"}}
	got := findViaCrossesPlane(vias, planes)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Type != "via-crosses-plane" || f.Level != "WARN" || f.Layer != 15 {
		t.Errorf("bad finding: %+v", f)
	}
	if !strings.Contains(f.Message, "no net-bound pour") {
		t.Errorf("message should explain the unknown plane net: %q", f.Message)
	}
}

func TestFindViaCrossesPlaneNoPlanes(t *testing.T) {
	// No PLANE layers (2-layer board / all-SIGNAL stackup) → rule is silent.
	vias := []pcbViaP{{ID: "v1", Net: "+5V"}}
	if got := findViaCrossesPlane(vias, nil); len(got) != 0 {
		t.Errorf("no planes: got %d findings, want 0: %+v", len(got), got)
	}
}

func TestFindViaCrossesPlaneMultiNetPlane(t *testing.T) {
	// A plane bound to two nets (unusual, but possible with two pours): a via on
	// EITHER net is fine, anything else is flagged.
	planes := []pcbPlaneLayer{{Layer: 16, Name: "Inner2", Nets: []string{"+3V3", "+5V"}}}
	vias := []pcbViaP{
		{ID: "v1", Net: "+3V3"},
		{ID: "v2", Net: "+5V"},
		{ID: "v3", Net: "GND"},
	}
	got := findViaCrossesPlane(vias, planes)
	if len(got) != 1 || got[0].Primitives[0] != "v3" {
		t.Fatalf("want exactly v3 flagged, got: %+v", got)
	}
}
