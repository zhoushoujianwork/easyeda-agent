package app

import (
	"strings"
	"testing"
)

// ── issue #126: functional zones ────────────────────────────────────────────

func TestPcbZoneRectGrid(t *testing.T) {
	b := cpRect{x0: 0, y0: 0, x1: 300, y1: 200}
	cases := []struct {
		zone string
		want cpRect
	}{
		{"left", cpRect{0, 0, 100, 200}},
		{"right", cpRect{200, 0, 300, 200}},
		{"center", cpRect{100, 0, 200, 200}},
		{"top", cpRect{0, 100, 300, 200}},       // top = MAX-Y half (PCB canvas convention)
		{"bottom", cpRect{0, 0, 300, 100}},
		{"right-top", cpRect{200, 100, 300, 200}},
		{"left-bottom", cpRect{0, 0, 100, 100}},
	}
	for _, c := range cases {
		got, ok := pcbZoneRect(c.zone, b)
		if !ok {
			t.Fatalf("%s: not recognized", c.zone)
		}
		if got != c.want {
			t.Errorf("%s: got %+v want %+v", c.zone, got, c.want)
		}
	}
	if _, ok := pcbZoneRect("middle-earth", b); ok {
		t.Error("unknown zone name must be rejected")
	}
}

func TestParseZoneSpec(t *testing.T) {
	raw := []byte(`{"modules":[
		{"name":"MCU","zone":"center","parts":["U1","Y1"]},
		{"name":"RF","zone":"right-top","parts":["ANT1"]},
		{"name":"unzoned","parts":["J9"]},
		{"name":"empty-parts","zone":"left","parts":[]}
	]}`)
	claims, err := parseZoneSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 {
		t.Fatalf("expected 2 zoned modules, got %d", len(claims))
	}
	if claims["MCU"].Zone != "center" || strings.Join(claims["MCU"].Parts, ",") != "U1,Y1" {
		t.Errorf("MCU claim wrong: %+v", claims["MCU"])
	}
	if _, err := parseZoneSpec([]byte(`{"modules":[{"name":"x","zone":"nope","parts":["A1"]}]}`)); err == nil {
		t.Error("unknown zone in spec must error")
	}
	if _, err := parseZoneSpec([]byte(`{"modules":[{"name":"x","parts":["A1"]}]}`)); err == nil {
		t.Error("spec with no zoned modules must error")
	}
}

func TestFindZoneViolations(t *testing.T) {
	board := cpRect{x0: 0, y0: 0, x1: 300, y1: 200}
	zones := map[string]*stageZoneClaim{
		"RF":  {Zone: "right-top", Parts: []string{"ANT1", "U2"}},
		"PWR": {Zone: "left-bottom", Parts: []string{"U3"}},
	}
	parts := []zonePart{
		{Designator: "ANT1", CX: 280, CY: 180, HasBBox: true}, // inside right-top
		{Designator: "U2", CX: 50, CY: 50, HasBBox: true},     // OUTSIDE (in left-bottom)
		{Designator: "U3", CX: 50, CY: 50, HasBBox: true},     // inside left-bottom
		{Designator: "C9", CX: 0, CY: 0, HasBBox: true},       // unclaimed — ignored
	}
	got := findZoneViolations(zones, board, parts)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 violation, got %+v", got)
	}
	f := got[0]
	if f.Designator != "U2" || f.Type != "zone-violation" || f.Level != "WARN" {
		t.Errorf("wrong finding: %+v", f)
	}
	if !strings.Contains(f.Message, "规范 §3.3") {
		t.Errorf("finding must cite the design-rules section, got %q", f.Message)
	}
	// Claimed-but-absent part: skipped, not flagged.
	zones["GHOST"] = &stageZoneClaim{Zone: "left", Parts: []string{"NOPE9"}}
	if got := findZoneViolations(zones, board, parts); len(got) != 1 {
		t.Errorf("absent claimed part must be skipped, got %+v", got)
	}
}

// TestPlannerZoneConsumption: a zone-claimed main outside its zone is moved in;
// a claimed satellite legalizes inside the zone; edge parts stay edge-bound.
func TestPlannerZoneConsumption(t *testing.T) {
	board := cpRect{x0: 0, y0: 0, x1: 1000, y1: 600}
	rfRect, _ := pcbZoneRect("right-top", board)
	comps := []cpComp{
		// main chip with 10 distinct-net pads at left-bottom, claimed to right-top
		mkCpComp("U2", 100, 100, 60, 60, 10),
		// its satellite cap, also claimed
		mkCpComp("C5", 90, 90, 10, 10, 2),
	}
	opt := defaultCpOptions()
	opt.board = &board
	opt.zones = map[string]cpZoneClaim{
		"U2": {rect: rfRect, module: "RF", zone: "right-top"},
		"C5": {rect: rfRect, module: "RF", zone: "right-top"},
	}
	moves, diags := planConstrainedPlace(comps, nil, opt)
	movedTo := map[string][2]float64{}
	for _, mv := range moves {
		movedTo[mv.Designator] = [2]float64{mv.NewX, mv.NewY}
	}
	u2, ok := movedTo["U2"]
	if !ok {
		t.Fatalf("U2 must be moved into its zone; moves=%+v diags=%+v", moves, diags)
	}
	if u2[0] < rfRect.x0-20 || u2[0] > rfRect.x1+20 || u2[1] < rfRect.y0-20 || u2[1] > rfRect.y1+20 {
		t.Errorf("U2 landed outside right-top: %+v (zone %+v)", u2, rfRect)
	}
	c5, ok := movedTo["C5"]
	if !ok {
		t.Fatalf("C5 must be relocated into the zone; moves=%+v diags=%+v", moves, diags)
	}
	if c5[0] < rfRect.x0-20 || c5[1] < rfRect.y0-20 {
		t.Errorf("C5 landed outside right-top: %+v", c5)
	}
	var zonedDiags int
	for _, d := range diags {
		if strings.Contains(d.Reason, "zoned:RF") {
			zonedDiags++
		}
	}
	if zonedDiags < 2 {
		t.Errorf("expected zoned diags for U2+C5, got %+v", diags)
	}
}

// mkCpComp builds a minimal component with n distinct-net pads centred at (x,y).
func mkCpComp(des string, x, y, w, h float64, nets int) cpComp {
	c := cpComp{
		apComp: apComp{
			id: des + "-id", designator: des, x: x, y: y,
			minX: x - w/2, minY: y - h/2, maxX: x + w/2, maxY: y + h/2,
			hasBBox: true,
		},
		layer: 1,
	}
	for i := 0; i < nets; i++ {
		c.pads = append(c.pads, apPad{x: x, y: y, net: des + "-N" + string(rune('A'+i))})
	}
	return c
}
