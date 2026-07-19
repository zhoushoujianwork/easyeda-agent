package app

import (
	"strings"
	"testing"
)

// alignPart builds an alPart whose anchor sits at the bbox center (the common
// symbol shape) — keeps expected coordinates easy to reason about.
func alignPart(d string, cx, cy, w, h float64) alPart {
	return alPart{
		Designator: d, PrimitiveID: "id-" + d,
		AnchorX: cx, AnchorY: cy, HasBBox: true,
		BBox: layoutBBox{MinX: cx - w/2, MinY: cy - h/2, MaxX: cx + w/2, MaxY: cy + h/2},
	}
}

func TestPlanAlignCenterX(t *testing.T) {
	parts := []alPart{
		alignPart("U1", 400, 300, 100, 100),
		alignPart("C1", 447, 400, 40, 20), // 47 off; snaps to 45 (grid wins)
		alignPart("C2", 400, 500, 40, 20), // already aligned → no move
	}
	moves, err := planAlign(parts, "centerx", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 1 {
		t.Fatalf("moves = %+v, want only C1", moves)
	}
	m := moves[0]
	if m.Designator != "C1" || m.X != 400 || m.Y != 400 {
		t.Errorf("C1 move = %+v, want x=400 y=400", m)
	}
}

func TestPlanAlignTopIsYUp(t *testing.T) {
	// y-UP canvas: "top" aligns MaxY (the visually upper edge). C1 (top edge
	// y=410) must move to U1's top edge y=350 → anchor y 400-60=340.
	parts := []alPart{
		alignPart("U1", 400, 300, 100, 100), // MaxY 350
		alignPart("C1", 600, 400, 40, 20),   // MaxY 410
	}
	moves, err := planAlign(parts, "top", "U1")
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 1 || moves[0].Designator != "C1" || moves[0].Y != 340 || moves[0].X != 600 {
		t.Errorf("moves = %+v, want C1 → 600,340", moves)
	}
}

func TestPlanAlignRefMustBeMember(t *testing.T) {
	parts := []alPart{alignPart("A1", 0, 0, 10, 10), alignPart("A2", 50, 0, 10, 10)}
	if _, err := planAlign(parts, "left", "ZZ9"); err == nil || !strings.Contains(err.Error(), "not among") {
		t.Errorf("err = %v, want not-among-designators", err)
	}
	if _, err := planAlign(parts, "diagonal", ""); err == nil {
		t.Error("unknown mode accepted")
	}
}

func TestPlanDistributeSpan(t *testing.T) {
	// Three 40-wide parts across a 400..640 span: total span 240, widths 120,
	// gap = (240-120)/2 = 60. Middle part must land with its MinX at 480.
	parts := []alPart{
		alignPart("C1", 420, 300, 40, 20), // MinX 400
		alignPart("C2", 450, 300, 40, 20), // crowding the left
		alignPart("C3", 620, 300, 40, 20), // MinX 600, MaxX 640
	}
	moves, err := planDistribute(parts, "x", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(moves) != 1 || moves[0].Designator != "C2" {
		t.Fatalf("moves = %+v, want only C2 (ends stay put)", moves)
	}
	if moves[0].X != 520 { // MinX 480+anchor offset 20 → 500? no: target MinX = 400+40+60=500 → anchor 520
		t.Errorf("C2 x = %g, want 520", moves[0].X)
	}
}

func TestPlanDistributeExplicitGap(t *testing.T) {
	parts := []alPart{
		alignPart("C1", 400, 300, 40, 20),
		alignPart("C2", 445, 300, 40, 20),
	}
	moves, err := planDistribute(parts, "x", 20, true)
	if err != nil {
		t.Fatal(err)
	}
	// C1 MinX 380; C2 target MinX 380+40+20=440 → anchor 460.
	if len(moves) != 1 || moves[0].X != 460 {
		t.Errorf("moves = %+v, want C2 → x=460", moves)
	}
}

func TestPlanDistributeOverflowErrors(t *testing.T) {
	parts := []alPart{
		alignPart("C1", 400, 300, 100, 20),
		alignPart("C2", 430, 300, 100, 20),
		alignPart("C3", 460, 300, 100, 20), // span 160 < widths 300
	}
	if _, err := planDistribute(parts, "x", 0, false); err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Errorf("err = %v, want overflow", err)
	}
}

func TestPickPartsValidates(t *testing.T) {
	parts := []alPart{alignPart("U1", 0, 0, 10, 10), {Designator: "J1", PrimitiveID: "id-J1"}}
	if _, err := pickParts(parts, []string{"u1", "ZZ"}); err == nil {
		t.Error("missing designator accepted")
	}
	if _, err := pickParts(parts, []string{"U1", "J1"}); err == nil || !strings.Contains(err.Error(), "no rendered bbox") {
		t.Errorf("bbox-less part accepted: %v", err)
	}
	if _, err := pickParts(parts, []string{"U1", "u1"}); err == nil {
		t.Error("dedup should leave <2 parts and error")
	}
}
