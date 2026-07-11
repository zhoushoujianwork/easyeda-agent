package app

import (
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/blocks"
)

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

// TestClassFromHint pins the block-hint → tier semantics: only an EXPLICIT
// signal decides a tier; an advisory-only hint (a side / orientation note) must
// fall through (ok=false) so an ordinary part isn't frozen in place.
func TestClassFromHint(t *testing.T) {
	if c, ok := classFromHint(blocks.PlacementHint{BoardEdge: true}); !ok || c != cpEdgeMust {
		t.Errorf("board_edge → (edge-must,true); got (%v,%v)", c, ok)
	}
	if c, ok := classFromHint(blocks.PlacementHint{Edge: "user-facing"}); !ok || c != cpUserFacing {
		t.Errorf("edge=user-facing → (user-facing,true); got (%v,%v)", c, ok)
	}
	if c, ok := classFromHint(blocks.PlacementHint{Anchor: true}); !ok || c != cpAnchored {
		t.Errorf("anchor → (anchored,true); got (%v,%v)", c, ok)
	}
	// Advisory-only: board_edge=false, no user-facing, no anchor → no tier forced.
	if c, ok := classFromHint(blocks.PlacementHint{Side: "top", Orientation: "长边贴板边"}); ok {
		t.Errorf("advisory-only hint must not force a tier; got (%v,true)", c)
	}
}

// TestClassifyCPFromBlockData is the acceptance gate for issue #95 defect #1:
// classifyCP must consult the block library's placement hints BEFORE the regex.
// Crucially it feeds REAL device names (what a placed part actually reports),
// NOT block role-ids — the reverse-map keys on the DISTINCTIVE designator prefix,
// which is how it works on a real board.
func TestClassifyCPFromBlockData(t *testing.T) {
	// JP701 on a real board reports its device name "SIP2-2.54mm单排针", never the
	// block role-id conn.sip2_254. It must still anchor — via the block-declared
	// "JP" prefix hint (board_edge=false, anchor=true) — NOT be caught by the old
	// J*-but-not-JP* rule and spiraled to a corner as a plain satellite.
	jp := classifyCP(mkCP("JP701", "SIP2-2.54mm单排针", 1, 0, 0, 60, 40, 2), 8)
	if jp != cpAnchored {
		t.Errorf("JP701 (block anchor=true, via JP prefix) should be cpAnchored; got %v", jp)
	}

	// The 3P screw terminal J4 is a board-edge part. Its "J" prefix is generic
	// (excluded from the index), so it correctly falls to the regex fallback
	// (terminal footprint + Jxx-not-JPxx) → edge-must.
	j4 := classifyCP(mkCP("J4", "KF301-5.08-3P 螺钉端子 terminal", 1, 0, 0, 300, 200, 3), 8)
	if j4 != cpEdgeMust {
		t.Errorf("J4 (screw terminal, board_edge) should be cpEdgeMust; got %v", j4)
	}

	// Over-anchor guard (defect #3): an ordinary decoupling cap must NOT be
	// anchored — no explicit anchor hint reaches it, so it stays a satellite to be
	// legalized, not frozen wherever it landed.
	if c := classifyCP(mkCP("C501", "CAP 470uF 35V 电解", 1, 0, 0, 40, 30, 2), 8); c == cpAnchored {
		t.Errorf("C501 (no explicit anchor) must not be cpAnchored; got %v", c)
	}

	// The old fiction is dead: feeding a block role-id as the device name is no
	// longer a match path (device-level precision is a future layer). With an
	// unknown designator prefix it must not anchor off the role-id string.
	if c := classifyCP(mkCP("XZ9", "conn.sip2_254", 1, 0, 0, 40, 30, 2), 8); c == cpAnchored {
		t.Errorf("role-id-as-device-name must not anchor (dead fiction path); got %v", c)
	}
}

// TestCpDeviceName guards the fix for the name-template blind spot found on the
// real ceshi board: a placed part's `name` is the unresolved "={Manufacturer
// Part}" template, so classification must key on the real manufacturerId.
func TestCpDeviceName(t *testing.T) {
	// manufacturerId present → use it (name is a useless template).
	if got := cpDeviceName(map[string]any{"manufacturerId": "ESP32-S3-WROOM-1", "name": "={Manufacturer Part}"}); got != "ESP32-S3-WROOM-1" {
		t.Errorf("should prefer manufacturerId; got %q", got)
	}
	// manufacturerId absent → fall back to a real name.
	if got := cpDeviceName(map[string]any{"name": "conn.usb_c"}); got != "conn.usb_c" {
		t.Errorf("should fall back to real name; got %q", got)
	}
	// manufacturerId absent AND name is a template → empty (nothing to match on).
	if got := cpDeviceName(map[string]any{"name": "={Manufacturer Part}"}); got != "" {
		t.Errorf("template-only should yield empty; got %q", got)
	}
}

// TestParseCpCompsUsesManufacturerId is the real-board regression: an
// ESP32-S3-WROOM-1 whose `name` is the "={Manufacturer Part}" template must still
// be recognised as a WROOM module (→ edge-must), not fall to the pin-count
// fallback and land as main/satellite. Before the fix it classified off the
// template name and the `wroom` regex never fired.
func TestParseCpCompsUsesManufacturerId(t *testing.T) {
	result := map[string]any{
		"components": []any{
			map[string]any{
				"primitiveId":    "p1",
				"designator":     "U1",
				"name":           "={Manufacturer Part}",
				"manufacturerId": "ESP32-S3-WROOM-1",
				"layer":          float64(1),
				"x":              float64(0),
				"y":              float64(250),
				"bbox":           map[string]any{"minX": float64(-374), "minY": float64(-130), "maxX": float64(374), "maxY": float64(900)},
				"pads":           []any{}, // 0 pins → without the fix this would be a satellite, not even main
			},
		},
	}
	comps := parseCpComps(result)
	if len(comps) != 1 {
		t.Fatalf("expected 1 comp, got %d", len(comps))
	}
	if comps[0].footprint != "ESP32-S3-WROOM-1" {
		t.Errorf("footprint should be the manufacturerId; got %q", comps[0].footprint)
	}
	if got := classifyCP(comps[0], 8); got != cpEdgeMust {
		t.Errorf("WROOM module must classify as edge-must; got %v", got)
	}
}

// TestConstrainedPlaceKeepsJP701 asserts the end-to-end #95 acceptance: with a
// JP701 sitting next to its 120R terminator and 3P terminal, place-constrained
// must NOT spiral it off to a board corner. A block-anchored part is fixed in
// place, so it produces no move (or at most a tiny legalizing nudge), unlike a
// satellite that gets flung outward.
func TestConstrainedPlaceKeepsJP701(t *testing.T) {
	comps := []cpComp{
		mkCP("U7", "SP3485EN-L SOP8", 1, 900, 900, 300, 200, 8),         // main chip, fixed
		mkCP("J4", "KF301-5.08-3P terminal", 1, 1500, 900, 300, 200, 3), // edge terminal
		mkCP("R703", "RES 120R 1206", 1, 1300, 900, 80, 50, 2),          // terminator
		mkCP("JP701", "SIP2-2.54mm单排针", 1, 1350, 950, 60, 40, 2),        // jumper beside R703/J4
		mkCP("C701", "CAP 100nF 0402", 1, 700, 900, 40, 30, 2),          // decap satellite
	}
	moves, diags := planConstrainedPlace(comps, nil, defaultCpOptions())

	byDes := map[string]apMove{}
	for _, m := range moves {
		byDes[m.Designator] = m
	}
	// JP701 must be recognised as block-anchored, not a satellite spiral.
	var jpDiag string
	for _, d := range diags {
		if d.Designator == "JP701" {
			jpDiag = d.Reason
		}
	}
	if jpDiag != "anchored:fixed" {
		t.Errorf("JP701 should be block-anchored (diag anchored:fixed); got %q", jpDiag)
	}
	// An anchored part stays put → no move emitted (satellites near JP701's old
	// class would have been flung to legalize around the pile).
	if mv, moved := byDes["JP701"]; moved {
		t.Errorf("JP701 (anchored) should not be relocated; got move to (%v,%v)", mv.NewX, mv.NewY)
	}
}

// TestConstrainedPlaceUsesRealOutline is the defect-#2 regression: when a real
// board outline is supplied, edge parts snap to the ACTUAL board edge, not to the
// tight part-cloud extent (the topmost/edge-most part must not define its own
// edge). Parts clustered near the origin on a much larger board must be flung to
// the real edge.
func TestConstrainedPlaceUsesRealOutline(t *testing.T) {
	comps := []cpComp{
		mkCP("U3", "CH340C", 1, 500, 500, 300, 200, 16),           // main
		mkCP("J1", "KF301-3P terminal", 1, 500, 300, 200, 150, 3), // edge, nearest the bottom
	}
	find := func(ms []apMove, des string) (apMove, bool) {
		for _, m := range ms {
			if m.Designator == des {
				return m, true
			}
		}
		return apMove{}, false
	}
	// No outline → J1 snaps to the tight part-cloud bottom (~y 300s).
	pc, _ := planConstrainedPlace(comps, nil, defaultCpOptions())
	j1pc, ok := find(pc, "J1")
	if !ok {
		t.Fatal("J1 should be edge-snapped (part-cloud fallback)")
	}
	// Real board is far larger → J1 snaps to the REAL bottom edge (y≈0), far lower.
	opt := defaultCpOptions()
	opt.board = &cpRect{0, 0, 4000, 4000}
	rb, _ := planConstrainedPlace(comps, nil, opt)
	j1rb, ok := find(rb, "J1")
	if !ok {
		t.Fatal("J1 should be edge-snapped (real outline)")
	}
	if !(j1rb.NewY < j1pc.NewY-100) {
		t.Errorf("with the real (larger) outline J1 must snap to the real bottom edge, "+
			"far below the part-cloud edge: part-cloud newY=%.0f real-board newY=%.0f", j1pc.NewY, j1rb.NewY)
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
