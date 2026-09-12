package app

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLibPriorityUsesPoliciesNotNames(t *testing.T) {
	policies := map[string]string{"banana": "local_ground", "orange": "local_power", "VCC": "module_port"}
	rail := powerLayoutPlacement{Designator: "CONNECTOR", Pins: []powerLayoutPin{{Net: "banana"}, {Net: "orange"}}}
	signal := powerLayoutPlacement{Designator: "C1", Pins: []powerLayoutPin{{Net: "banana"}, {Net: "VCC"}}}
	if libPeripheralPriority(rail, policies) >= libPeripheralPriority(signal, policies) {
		t.Fatal("must use declared policies, not C prefix or VCC spelling")
	}
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{
		{Designator: "X1", BBox: layoutBBox{-10, -10, 10, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "VCC", X: -20, Y: 0}, {Number: "2", Net: "orange", X: 0, Y: 20}, {Number: "3", Net: "banana", X: 0, Y: -20}}},
	}}
	if err := libNameIslands(&p, policies); err != nil {
		t.Fatal(err)
	}
	if len(p.Flags) != 3 || p.Flags[0].Net != "banana" || p.Flags[1].Net != "orange" || p.Flags[2].Net != "VCC" {
		t.Fatalf("incorrect naming priority: %+v", p.Flags)
	}
}

func TestLibNearbyRailJoinAndObstacle(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		p := powerLayoutPlan{Placements: []powerLayoutPlacement{
			{Designator: "X1", BBox: layoutBBox{-10, -10, 10, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "RAIL", X: 0, Y: 20}}},
			{Designator: "X2", X: 70, BBox: layoutBBox{60, -10, 80, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "RAIL", X: 70, Y: 20}}},
		}}
		if blocked {
			p.Wires = []powerLayoutWire{{Net: "FOREIGN", Points: [][2]float64{{35, 10}, {35, 30}}}}
		}
		policies := map[string]string{"RAIL": "local_power"}
		before, _ := json.Marshal(p.Placements)
		if err := libJoinNearbyRails(&p, policies); err != nil {
			t.Fatal(err)
		}
		after, _ := json.Marshal(p.Placements)
		if string(before) != string(after) {
			t.Fatal("joining rewrote pins/poses")
		}
		want := 1
		if blocked {
			want = 2
		}
		if got := len(libIslands(&p)); got != want {
			t.Fatalf("blocked=%v: islands=%d want=%d", blocked, got, want)
		}
		if !blocked {
			if err := libNameIslands(&p, policies); err != nil {
				t.Fatal(err)
			}
			if len(p.Flags) != 1 {
				t.Fatal("direct rail tree should have only one naming marker")
			}
		}
		if err := validateLibGeometry(&p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLibNearbyRailJoinIsBounded(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{
		{Designator: "X1", BBox: layoutBBox{-10, -10, 10, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "RAIL", X: 0, Y: 20}}},
		{Designator: "X2", X: 100, BBox: layoutBBox{90, -10, 110, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "RAIL", X: 100, Y: 20}}},
	}}
	if err := libJoinNearbyRails(&p, map[string]string{"RAIL": "local_ground"}); err != nil {
		t.Fatal(err)
	}
	if len(p.Wires) != 0 {
		t.Fatal("must not force a long optional rail route")
	}
}

func TestLibCandidateObjectiveIncludesLeadsAndAlignment(t *testing.T) {
	a := powerLayoutPlan{Placements: []powerLayoutPlacement{{X: 0, Y: 0, BBox: layoutBBox{-10, -10, 10, 10}}, {X: 60, Y: 0, BBox: layoutBBox{50, -10, 70, 10}}}, Flags: []powerLayoutFlag{{Kind: "power", Net: "A", Direction: "up", Offset: 10}}}
	b := a
	b.Flags = append([]powerLayoutFlag(nil), a.Flags...)
	b.Flags[0].Offset = 50
	if !libCandidateLess(&a, &b) {
		t.Fatal("ignored full naming lead cost")
	}
	b = a
	b.Placements = append([]powerLayoutPlacement(nil), a.Placements...)
	b.Placements[1] = plTranslate(b.Placements[1], 0, 5)
	if !libCandidateLess(&a, &b) {
		t.Fatal("ignored axis alignment")
	}
	x := libCandidateScore(&a)
	translatePowerLayout(&a, 100, -200)
	if y := libCandidateScore(&a); !reflect.DeepEqual(x, y) {
		t.Fatalf("objective depends on page origin: %v / %v", x, y)
	}
}
