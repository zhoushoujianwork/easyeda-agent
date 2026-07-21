package app

import (
	"io"
	"testing"
)

// TestBuildAutolayoutZoneClaims (issue #142): spec modules → schematic zone
// claims. Modules without a zone or parts are skipped; an unknown zone name is
// skipped (never aborts) so a successful --apply still draws the valid zones.
func TestBuildAutolayoutZoneClaims(t *testing.T) {
	mods := []alSpecModule{
		{Name: "POWER", Zone: "left-top", Parts: []string{"U1", "C1"}},
		{Name: "MCU", Zone: "CENTER", Parts: []string{"U2"}}, // case-insensitive
		{Name: "NOZONE", Zone: "", Parts: []string{"R1"}},     // skipped: no zone
		{Name: "NOPARTS", Zone: "right", Parts: nil},          // skipped: no parts
		{Name: "BADZONE", Zone: "middle-of-nowhere", Parts: []string{"D1"}}, // skipped: unknown zone
	}
	claims := buildAutolayoutZoneClaims(mods, io.Discard)
	if len(claims) != 2 {
		t.Fatalf("got %d claims, want 2 (POWER + MCU): %+v", len(claims), claims)
	}
	if claims["POWER"] == nil || claims["POWER"].Zone != "left-top" {
		t.Errorf("POWER claim wrong: %+v", claims["POWER"])
	}
	if claims["MCU"] == nil || claims["MCU"].Zone != "center" {
		t.Errorf("MCU zone not lowercased: %+v", claims["MCU"])
	}
	if claims["MCU"].Note != "autolayout" {
		t.Errorf("MCU note = %q, want autolayout", claims["MCU"].Note)
	}
	for _, bad := range []string{"NOZONE", "NOPARTS", "BADZONE"} {
		if _, ok := claims[bad]; ok {
			t.Errorf("%s should have been skipped", bad)
		}
	}
}
