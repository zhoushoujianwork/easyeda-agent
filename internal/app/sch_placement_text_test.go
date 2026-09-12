package app

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// Independent synthetic geometry: no source drawing, circuit or exam data.
func TestPlacementTextObstacles(t *testing.T) {
	for _, kind := range []string{"body", "wire", "marker", "label", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			p := powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "U_SYN", BBox: layoutBBox{-20, -20, 20, 20}, TextBBoxes: []layoutBBox{{50, -10, 100, 10}}}}}
			switch kind {
			case "body":
				p.Placements = append(p.Placements, powerLayoutPlacement{Designator: "R_SYN", X: 70, BBox: layoutBBox{60, -5, 80, 5}})
			case "wire":
				p.Wires = []powerLayoutWire{{Net: "SIGNAL", Points: [][2]float64{{40, 0}, {110, 0}}}}
			case "marker":
				p.Flags = []powerLayoutFlag{{Net: "SIGNAL", Kind: "power", PinX: 75, PinY: -50, Direction: "up", Offset: 40}}
			case "label":
				p.Placements = append(p.Placements, powerLayoutPlacement{Designator: "C_SYN", X: 200, BBox: layoutBBox{190, -5, 210, 5}, TextBBoxes: []layoutBBox{{60, -5, 90, 5}}})
			case "invalid":
				p.Placements[0].TextBBoxes[0].MaxX = math.NaN()
			}
			if err := validatePowerLayout(&p, layoutBBox{-1000, -1000, 1000, 1000}); err == nil || !strings.Contains(err.Error(), "text") {
				t.Fatalf("missing %s text obstacle: %v", kind, err)
			}
		})
	}
}

func TestPlacementTextTranslationAndMarkerSearch(t *testing.T) {
	c := powerLayoutPlacement{Designator: "U_SYN", BBox: layoutBBox{-20, -20, 20, 20}, TextBBoxes: []layoutBBox{{40, -10, 100, 15}}, Pins: []powerLayoutPin{{Number: "1", Net: "SIGNAL", X: 30, Y: 0}}}
	before := c.TextBBoxes[0]
	moved := plTranslate(c, 100, 50)
	if c.TextBBoxes[0] != before || moved.TextBBoxes[0] != (layoutBBox{140, 40, 200, 65}) {
		t.Fatal("text must translate without mutating source")
	}
	rotated := c
	for i := 0; i < 4; i++ {
		rotated = plRotate(rotated, 1)
	}
	if !reflect.DeepEqual(rotated.TextBBoxes, c.TextBBoxes) {
		t.Fatal("rotation lost text geometry")
	}
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{c}}
	if !libPlaceMarker(&p, c.Pins[0], "net_port_bi") {
		t.Fatal("marker search must find a position avoiding explicit text")
	}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	for _, b := range schTerminalMarkerBoxes(p.Flags[0]) {
		if boxesGapOverlap(b, before, 5) {
			t.Fatal("marker search returned overlap")
		}
	}
	found := false
	for _, b := range powerLayoutContentObstacles(&p) {
		found = found || b == before
	}
	if !found {
		t.Fatal("module envelope dropped explicit text")
	}
}

func TestLibLayoutPeripheralAvoidsCoreText(t *testing.T) {
	src := libLayoutFixture()
	// A deliberately tall explicit label obstacle across the outward attachment
	// corridor. The solver must translate the peripheral rather than drop text.
	src.Measurements[0].TextBBoxes = []layoutBBox{{145, 75, 205, 130}}
	out, err := planLibLayout(src)
	if err != nil {
		t.Fatal(err)
	}
	m := out.Modules[0]
	box := m.Placements[0].TextBBoxes[0]
	if boxesGapOverlap(box, m.Placements[1].BBox, 5) {
		t.Fatal("peripheral overlaps core text")
	}
	if len(m.Wires) == 0 {
		t.Fatal("lost direct peripheral connection")
	}
	if _, err := planSchComposition(*out); err != nil {
		t.Fatal(err)
	}
}
