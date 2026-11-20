package app

import (
	"strings"
	"testing"
)

// An anonymous net ("$2N10", drawn with wires only) is kept by its wire tree:
// no label, all pins on one continuous tree.
func TestSchCompositionAnonymousNetIsOneUnlabeledWireTree(t *testing.T) {
	plan := func(wires []powerLayoutWire, flags []powerLayoutFlag) *powerLayoutPlan {
		return &powerLayoutPlan{
			Placements: []powerLayoutPlacement{
				{Designator: "R101", Pins: []powerLayoutPin{{Number: "1", X: 0, Y: 0}, {Number: "2", Net: "$2N10", X: 20, Y: 0}}},
				{Designator: "R102", Pins: []powerLayoutPin{{Number: "1", Net: "$2N10", X: 60, Y: 0}, {Number: "2", Net: "", X: 80, Y: 0}}},
			},
			Wires: wires, Flags: flags,
		}
	}
	joined := []powerLayoutWire{{Net: "$2N10", Points: [][2]float64{{20, 0}, {60, 0}}}}
	if err := validateSchCompositionNets(plan(joined, nil)); err != nil {
		t.Fatalf("wired anonymous net rejected: %v", err)
	}
	split := []powerLayoutWire{{Net: "$2N10", Points: [][2]float64{{20, 0}, {30, 0}}}, {Net: "$2N10", Points: [][2]float64{{50, 0}, {60, 0}}}}
	for name, tc := range map[string]struct {
		p    *powerLayoutPlan
		want string
	}{
		"split":    {plan(split, nil), "is split"},
		"unwired":  {plan(nil, nil), "is not wired"},
		"labelled": {plan(joined, []powerLayoutFlag{{Net: "$2N10", Kind: "net_port_bi", PinX: 20, PinY: 0, Direction: "up", Offset: 10}}), "cannot carry a label"},
	} {
		if err := validateSchCompositionNets(tc.p); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: want %q, got %v", name, tc.want, err)
		}
	}
}
