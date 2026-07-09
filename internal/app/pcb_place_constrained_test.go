package app

import "testing"

func mkCP(des, name string, layer int, x, y, w, h float64, pins int) cpComp {
	c := cpComp{footprint: name, layer: layer}
	c.id = des
	c.designator = des
	c.x, c.y = x, y
	c.hasBBox = true
	c.minX, c.minY, c.maxX, c.maxY = x-w/2, y-h/2, x+w/2, y+h/2
	for i := range pins {
		c.pads = append(c.pads, apPad{num: string(rune('0' + i))})
	}
	return c
}

func TestClassifyCP(t *testing.T) {
	cases := []struct {
		des, name string
		pins      int
		want      cpClass
	}{
		{"J2", "conn.usb_c", 16, cpEdgeMust},
		{"U1", "esp32-s3-wroom-1u-n8", 41, cpEdgeMust}, // module by name, not main
		{"U3", "ch340c", 16, cpMainChip},
		{"SW1", "sw.tact", 2, cpUserFacing},
		{"LED1", "led.red", 2, cpUserFacing},
		{"X2", "xtal.26mhz_3225", 4, cpMainChip}, // crystal folds into main
		{"C1", "cap.100nf", 2, cpSatellite},
		{"R1", "res.10k", 2, cpSatellite},
	}
	for _, c := range cases {
		got := classifyCP(mkCP(c.des, c.name, 1, 0, 0, 100, 100, c.pins), 8)
		if got != c.want {
			t.Errorf("%s(%s): got %v, want %v", c.des, c.name, got, c.want)
		}
	}
}

func TestConstrainedPlaceEdgeSnapAndNoOverlap(t *testing.T) {
	// A USB connector parked 300mil inside the board must snap to the nearest edge;
	// satellites must not overlap it or each other.
	comps := []cpComp{
		mkCP("J2", "conn.usb_c", 1, 400, 400, 300, 200, 16),    // near left, but 250mil in
		mkCP("U1", "esp32-wroom", 1, 1500, 1500, 700, 700, 41), // module, gets edge-snapped
		mkCP("U3", "ch340c", 1, 1500, 600, 300, 250, 16),       // main, fixed
		mkCP("C1", "cap", 1, 1500, 600, 60, 40, 2),             // satellite ON TOP of U3 → must move
		mkCP("C2", "cap", 1, 1510, 610, 60, 40, 2),             // satellite overlapping C1 → must move
	}
	// Board extent from the parts: x[50,1850] y[50,1850] roughly.
	moves, _ := planConstrainedPlace(comps, nil, defaultCpOptions())
	byDes := map[string]apMove{}
	for _, m := range moves {
		byDes[m.Designator] = m
	}
	// J2 should have moved toward the left edge (its new minX ≈ board minX + margin).
	if _, ok := byDes["J2"]; !ok {
		t.Error("J2 (edge-must, 250mil inside) should have been snapped to an edge")
	}
	// Both satellites should have been relocated off the U3 pile.
	for _, d := range []string{"C1", "C2"} {
		if _, ok := byDes[d]; !ok {
			t.Errorf("%s (overlapping) should have been legalized", d)
		}
	}
	// Verify the resulting satellite positions don't overlap U3's fixed rect.
	u3 := comps[2]
	u3r := cpRect{u3.minX, u3.minY, u3.maxX, u3.maxY}
	for _, d := range []string{"C1", "C2"} {
		m := byDes[d]
		// reconstruct new bbox center from the anchor move
		var c cpComp
		for _, cc := range comps {
			if cc.designator == d {
				c = cc
			}
		}
		ncx := m.NewX + (c.minX+c.maxX)/2 - c.x
		ncy := m.NewY + (c.minY+c.maxY)/2 - c.y
		nr := cpRect{ncx - c.width()/2, ncy - c.height()/2, ncx + c.width()/2, ncy + c.height()/2}
		if nr.overlaps(u3r) {
			t.Errorf("%s still overlaps U3 after placement", d)
		}
	}
}

func TestConnOrientation(t *testing.T) {
	// A terminal on the RIGHT edge whose pads point RIGHT (toward the edge = opening
	// faces interior = WRONG) must be rotated so pads face LEFT (interior).
	c := mkCP("J1", "conn.terminal", 1, 1700, 900, 200, 300, 2)
	// put pads to the RIGHT of bbox center (wrong: opening faces interior on a right edge)
	c.pads = []apPad{{num: "1", x: 1780, y: 850}, {num: "2", x: 1780, y: 950}}
	// board so J1 is near the right edge
	others := []cpComp{
		mkCP("U1", "esp32-wroom", 1, 400, 900, 400, 400, 41),
		c,
	}
	delta, score := bestConnDelta(others[1], edgeRight)
	if delta == 0 && score < 0 {
		t.Errorf("right-edge terminal with pads facing OUT should get a non-zero orient delta; got delta=%v score=%v", delta, score)
	}
	// After the best delta, pads should face interior (left, -x): score > 0.
	if score <= 0 {
		t.Errorf("best orientation should put pads on the interior side (score>0), got %v", score)
	}
}
