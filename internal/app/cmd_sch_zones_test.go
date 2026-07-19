package app

import (
	"testing"
)

func schZoneComps() []layoutComp {
	box := func(cx, cy float64) *layoutBBox {
		return &layoutBBox{MinX: cx - 20, MinY: cy - 10, MaxX: cx + 20, MaxY: cy + 10}
	}
	// Sheet 0..900 × 0..600 (y-down: top rows are y < 300).
	return []layoutComp{
		{Designator: "U1", ComponentType: "part", BBox: box(450, 300)},  // center
		{Designator: "U3", ComponentType: "part", BBox: box(100, 100)},  // left-top
		{Designator: "C5", ComponentType: "part", BBox: box(800, 500)},  // right-bottom — NOT left-top
		{Designator: "J1", ComponentType: "part", BBox: nil},            // no bbox → skipped
	}
}

// TestFindSchZoneViolations: only claimed parts outside their zone rect are
// flagged; absent/bbox-less designators are skipped, order is deterministic.
func TestFindSchZoneViolations(t *testing.T) {
	sheet := layoutBBox{MinX: 0, MinY: 0, MaxX: 900, MaxY: 600}
	zones := map[string]*schZoneClaim{
		"MCU":   {Zone: "center", Parts: []string{"U1"}},
		"POWER": {Zone: "left-top", Parts: []string{"U3", "C5", "J1", "C99"}},
	}
	v := findSchZoneViolations(zones, sheet, schZoneComps())
	if len(v) != 1 {
		t.Fatalf("violations = %+v, want exactly C5", v)
	}
	if v[0].A != "C5" || v[0].Type != "zone-violation" || v[0].B != "POWER→left-top" {
		t.Errorf("violation = %+v", v[0])
	}
}

// TestFindSchZoneViolationsYDown pins the row semantics: sheet coords are
// y-DOWN, so "top" is the SMALLER-y half. A part at small y must satisfy a
// -top claim and violate a -bottom claim.
func TestFindSchZoneViolationsYDown(t *testing.T) {
	sheet := layoutBBox{MinX: 0, MinY: 0, MaxX: 900, MaxY: 600}
	comps := schZoneComps()
	top := map[string]*schZoneClaim{"P": {Zone: "left-top", Parts: []string{"U3"}}}
	if v := findSchZoneViolations(top, sheet, comps); len(v) != 0 {
		t.Errorf("U3 at y=100 should satisfy left-top on a y-down sheet, got %+v", v)
	}
	bottom := map[string]*schZoneClaim{"P": {Zone: "left-bottom", Parts: []string{"U3"}}}
	if v := findSchZoneViolations(bottom, sheet, comps); len(v) != 1 {
		t.Errorf("U3 at y=100 should violate left-bottom, got %+v", v)
	}
}

func TestParseSchZoneSpec(t *testing.T) {
	raw := []byte(`{"modules":[
		{"name":"MCU","page":"P1","zone":"center","parts":["U1"]},
		{"name":"unzoned","parts":["X1"]},
		{"name":"POWER","zone":"Left-Top","parts":["u3","C5","u3"]}
	]}`)
	claims, err := parseSchZoneSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 {
		t.Fatalf("claims = %v, want MCU+POWER (unzoned skipped)", claims)
	}
	if claims["MCU"].Page != "P1" || claims["MCU"].Zone != "center" {
		t.Errorf("MCU claim = %+v", claims["MCU"])
	}
	p := claims["POWER"]
	if p.Zone != "left-top" || len(p.Parts) != 2 || p.Parts[0] != "C5" || p.Parts[1] != "U3" {
		t.Errorf("POWER claim = %+v, want normalized deduped sorted parts", p)
	}
	if _, err := parseSchZoneSpec([]byte(`{"modules":[{"name":"A","zone":"middle","parts":["U1"]}]}`)); err == nil {
		t.Error("unknown zone name accepted")
	}
	if _, err := parseSchZoneSpec([]byte(`{"modules":[{"name":"A","parts":["U1"]}]}`)); err == nil {
		t.Error("spec with no zoned modules accepted")
	}
}

func TestParseSchZoneModuleFlags(t *testing.T) {
	claims, err := parseSchZoneModuleFlags([]string{"POWER=left-top:U3,C5"})
	if err != nil {
		t.Fatal(err)
	}
	if claims["POWER"].Zone != "left-top" || len(claims["POWER"].Parts) != 2 {
		t.Errorf("claims = %+v", claims["POWER"])
	}
	for _, bad := range []string{"noequals", "A=nocolon", "A=badzone:U1"} {
		if _, err := parseSchZoneModuleFlags([]string{bad}); err == nil {
			t.Errorf("malformed --module %q accepted", bad)
		}
	}
}
