package app

import (
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

func TestDesignatorAlphaPrefix(t *testing.T) {
	cases := map[string]string{
		"LED2": "LED", "U1": "U", "C10": "C", "R100": "R",
		"led2": "LED", // upper-cased
		"123":  "",    // no alpha prefix
		"":     "",
	}
	for in, want := range cases {
		if got := designatorAlphaPrefix(in); got != want {
			t.Errorf("designatorAlphaPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeRotation(t *testing.T) {
	cases := map[float64]float64{
		0: 0, 90: 90, 180: 180, 270: 270,
		360: 0, -90: 270, 450: 90,
		89.9: 90, 269: 270, 44: 0, 46: 90,
	}
	for in, want := range cases {
		if got := normalizeRotation(in); got != want {
			t.Errorf("normalizeRotation(%g) = %g, want %g", in, got, want)
		}
	}
}

func TestExtractRolePrefixes_RealBlock(t *testing.T) {
	b, ok, err := blocks.Get("led_indicator_gpio")
	if err != nil || !ok {
		t.Fatalf("load block: ok=%v err=%v", ok, err)
	}
	pfx, err := extractRolePrefixes(b)
	if err != nil {
		t.Fatal(err)
	}
	if pfx["LED"] != "LED" || pfx["R"] != "R" {
		t.Errorf("prefixes = %+v, want LED→LED, R→R", pfx)
	}
	// No "U" role → anchor is the first sorted role, "LED".
	if a := extractAnchorRole(pfx); a != "LED" {
		t.Errorf("anchor role = %q, want LED (first sorted, no chip)", a)
	}
}

func TestExtractAnchorRolePrefersChip(t *testing.T) {
	// A "U" role always wins the anchor regardless of sort order.
	pfx := map[string]string{"aaa": "R", "zzz": "U", "mmm": "C"}
	if a := extractAnchorRole(pfx); a != "zzz" {
		t.Errorf("anchor = %q, want zzz (the chip)", a)
	}
}

func TestResolveExtractRoleMap_FromUniquePrefix(t *testing.T) {
	b, _, _ := blocks.Get("led_indicator_gpio")
	pfx := map[string]string{"LED": "LED", "R": "R"}
	got, err := resolveExtractRoleMap(b, pfx, nil, []string{"LED1", "R1"})
	if err != nil {
		t.Fatal(err)
	}
	if got["LED"] != "LED1" || got["R"] != "R1" {
		t.Errorf("roleDesig = %+v, want LED→LED1, R→R1", got)
	}
}

func TestResolveExtractRoleMap_ExplicitWinsAndCompletes(t *testing.T) {
	b, _, _ := blocks.Get("led_indicator_gpio")
	pfx := map[string]string{"LED": "LED", "R": "R"}
	// Explicit pins R; --from supplies the LED.
	got, err := resolveExtractRoleMap(b, pfx, map[string]string{"R": "R7"}, []string{"LED3"})
	if err != nil {
		t.Fatal(err)
	}
	if got["R"] != "R7" || got["LED"] != "LED3" {
		t.Errorf("roleDesig = %+v, want R→R7, LED→LED3", got)
	}
}

func TestResolveExtractRoleMap_AmbiguousPrefixErrors(t *testing.T) {
	// Two roles share prefix "C" → a bare --from cannot disambiguate.
	b := blocks.Block{ID: "t", Parts: map[string]blocks.Part{"a": {Qty: 1}, "b": {Qty: 1}}}
	pfx := map[string]string{"a": "C", "b": "C"}
	if _, err := resolveExtractRoleMap(b, pfx, nil, []string{"C1"}); err == nil {
		t.Error("expected ambiguity error for shared prefix, got nil")
	}
}

func TestResolveExtractRoleMap_MissingRoleErrors(t *testing.T) {
	b, _, _ := blocks.Get("led_indicator_gpio")
	pfx := map[string]string{"LED": "LED", "R": "R"}
	// Only map LED; R is left unmapped.
	if _, err := resolveExtractRoleMap(b, pfx, map[string]string{"LED": "LED1"}, nil); err == nil {
		t.Error("expected missing-role error, got nil")
	}
}

func TestBuildExtractedLayout_RelativeOffsetsSnapped(t *testing.T) {
	roleDesig := map[string]string{"LED": "LED1", "R": "R1"}
	geom := map[string]extractRoleGeom{
		"LED": {X: 200, Y: 300, Rotation: 0},
		"R":   {X: 322, Y: 300, Rotation: 88}, // 322→snap 120, 88→90
	}
	layout, err := buildExtractedLayout(roleDesig, geom, "LED", "test")
	if err != nil {
		t.Fatal(err)
	}
	if layout.Roles["LED"].DX != 0 || layout.Roles["LED"].DY != 0 {
		t.Errorf("anchor role LED must be (0,0), got %+v", layout.Roles["LED"])
	}
	r := layout.Roles["R"]
	if r.DX != 120 || r.DY != 0 || r.Rotation != 90 {
		t.Errorf("R hint = %+v, want dx=120 dy=0 rot=90", r)
	}
	if layout.Note != "test" {
		t.Errorf("note = %q, want test", layout.Note)
	}
}

func TestBuildExtractedLayout_MissingGeomErrors(t *testing.T) {
	roleDesig := map[string]string{"LED": "LED1", "R": "R1"}
	geom := map[string]extractRoleGeom{"LED": {X: 0, Y: 0}} // R absent
	if _, err := buildExtractedLayout(roleDesig, geom, "LED", ""); err == nil {
		t.Error("expected missing-geometry error for role R, got nil")
	}
}
