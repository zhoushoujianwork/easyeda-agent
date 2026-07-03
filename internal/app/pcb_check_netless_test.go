package app

import "testing"

func TestFindNetlessPours(t *testing.T) {
	pours := []pcbPourP{
		{ID: "a", Net: "GND", Layer: 1},
		{ID: "b", Net: "", Layer: 1},   // netless → flag
		{ID: "c", Net: "  ", Layer: 2}, // whitespace-only → flag
		{ID: "d", Net: "+5V", Layer: 16},
	}
	got := findNetlessPours(pours)
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(got), got)
	}
	for _, f := range got {
		if f.Type != "netless-pour" || f.Level != "WARN" {
			t.Errorf("bad finding: %+v", f)
		}
		if len(f.Primitives) != 1 {
			t.Errorf("expected primitive id attached: %+v", f)
		}
	}
	// all-bound → no findings
	if n := len(findNetlessPours([]pcbPourP{{ID: "x", Net: "GND"}})); n != 0 {
		t.Errorf("all-bound pours: got %d findings, want 0", n)
	}
}
