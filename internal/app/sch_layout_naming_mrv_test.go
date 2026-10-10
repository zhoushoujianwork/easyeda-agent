package app

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestJointNamingLargeBudgetRetainsIncumbentAndSharedDebit(t *testing.T) {
	policies := map[string]string{"A": "module_port", "B": "module_port"}
	incumbent, islands, _ := jointNamingCompetitionFixture()
	fast := 4096
	if err := libNameIslandsJointWithMode(&incumbent, policies, islands, &fast, libJointNamingOptions, true); err != nil {
		t.Fatal(err)
	}
	actual, islands, _ := jointNamingCompetitionFixture()
	budget := 20000
	if err := libNameIslandsJoint(&actual, policies, islands, &budget); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, incumbent) || 20000-budget != 4096-fast {
		t.Fatalf("larger allowance changed a successful incumbent or debit: actual=%d incumbent=%d", 20000-budget, 4096-fast)
	}
}

func TestJointNamingFailedFallbackSharesBudgetAndPreservesInput(t *testing.T) {
	p, input, _ := namingBudgetBoundaryFixture()
	before := p
	budget := 20000
	err := libNameIslandsJoint(&p, input.NetPolicies, libIslands(&p), &budget)
	if err == nil || budget < 0 || budget >= 20000-4096 || !reflect.DeepEqual(p, before) {
		t.Fatalf("failed fallback escaped shared allowance or input: err=%v remaining=%d", err, budget)
	}
}

func TestTerminalNamingRetryBudgetBoundary(t *testing.T) {
	wrapped := fmt.Errorf("observed conflict: %w", errLibLayoutBudget)
	for _, test := range []struct {
		name               string
		initial, remaining int
		previous, unused   int
		err                error
		want               int
	}{
		{"double", 100000, 90000, 16384, 0, errLibLayoutBudget, 32768},
		{"repair-reserve-cap", 100000, 45000, 20000, 0, errLibLayoutBudget, 32500},
		{"last-remainder-cap", 100000, 20000, 16384, 0, errLibLayoutBudget, 20000},
		{"no-larger-slice", 100000, 16384, 16384, 0, errLibLayoutBudget, 0},
		{"unused-allowance", 100000, 90000, 16384, 1, errLibLayoutBudget, 0},
		{"observed-cause", 100000, 90000, 16384, 0, wrapped, 0},
		{"routing-stop", 100000, 90000, 16384, 0, errSchematicExpandedBudget, 0},
		{"success", 100000, 90000, 16384, 0, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			remaining := test.remaining
			s := schematicRepairSearch{budget: &remaining, initial: test.initial}
			if got := s.nextTerminalSliceBudget(test.previous, test.unused, test.err); got != test.want || remaining != test.remaining {
				t.Fatalf("unsafe retry quota: got=%d want=%d remaining=%d", got, test.want, remaining)
			}
		})
	}
	remaining := 139
	s := schematicRepairSearch{budget: &remaining, initial: 149}
	if s.terminalSliceBudget() != 139 {
		t.Fatal("tiny allowance was split before a complete terminal pass")
	}
}

func TestMRVNamingRealMarkersResolveSharedCorridor(t *testing.T) {
	p, islands, _ := jointNamingCompetitionFixture()
	before := p
	budget := 20000
	if err := libNameIslandsMRV(&p, map[string]string{"A": "module_port", "B": "module_port"}, islands, &budget); err != nil {
		t.Fatal(err)
	}
	if len(p.Flags) != 2 || budget < 0 || budget >= 20000 {
		t.Fatalf("incomplete/unbudgeted result: flags=%d budget=%d", len(p.Flags), budget)
	}
	if !reflect.DeepEqual(p.Placements, before.Placements) {
		t.Fatal("naming changed measured geometry")
	}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if err := validateSchCompositionNets(&p); err != nil {
		t.Fatal(err)
	}
}

func TestMRVNamingBudgetStopKeepsInputUntouched(t *testing.T) {
	p, islands, _ := jointNamingCompetitionFixture()
	before := p
	budget := 1
	err := libNameIslandsMRV(&p, map[string]string{"A": "module_port", "B": "module_port"}, islands, &budget)
	if !errors.Is(err, errLibLayoutBudget) || budget != 0 || !reflect.DeepEqual(p, before) {
		t.Fatalf("partial naming escaped: err=%v budget=%d", err, budget)
	}
}

func TestMRVNamingKeepsPhysicalTreeAfterWireNormalization(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{
		{Designator: "U1", BBox: layoutBBox{-20, -20, 20, 20}, Pins: []powerLayoutPin{{Number: "1", Net: "GND", X: 30, Y: 0, Rotation: directionNumber(0)}}},
		{Designator: "J1", X: 100, BBox: layoutBBox{95, -5, 105, 5}, Pins: []powerLayoutPin{{Number: "1", Net: "GND", X: 80, Y: 0, Rotation: directionNumber(180)}}},
	}, Wires: []powerLayoutWire{
		{Net: "GND", Points: [][2]float64{{30, 0}, {50, 0}}},
		{Net: "GND", Points: [][2]float64{{50, 0}, {80, 0}}},
	}}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	before := p
	first, second := p.Placements[0].Pins[0], p.Placements[1].Pins[0]
	budget := 20000
	if err := libNameIslandsMRV(&p, map[string]string{"GND": "local_ground"}, libIslands(&p), &budget); err != nil {
		t.Fatal(err)
	}
	if len(p.Flags) != 1 || !libPinsShareIsland(&p, first, second) {
		t.Fatal("naming split the real attachment tree")
	}
	if reflect.DeepEqual(p.Wires, before.Wires) {
		t.Fatal("fixture did not exercise a canonicalized elbow branch")
	}
	for _, w := range before.Wires {
		for _, point := range w.Points {
			found := false
			for _, actual := range p.Wires {
				if actual.Net == w.Net && plOnSegment(point, actual.Points[0], actual.Points[1]) {
					found = true
				}
			}
			if !found {
				t.Fatalf("lost original wire point %v", point)
			}
		}
	}
	if !reflect.DeepEqual(p.Placements, before.Placements) {
		t.Fatal("naming changed original parts")
	}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if err := validateSchCompositionNets(&p); err != nil {
		t.Fatal(err)
	}
}

func TestMRVNamingOptionsExposeWideAndElbowAlternatives(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "J1", BBox: layoutBBox{-20, -20, 20, 20}, Pins: []powerLayoutPin{{Number: "1", Net: "LONG_SIGNAL_NAME", X: 30, Y: 0, Rotation: directionNumber(0)}}}}}
	before := p
	budget := 10000
	count, elbows := 0, 0
	low, high := 1000.0, -1000.0
	libMRVNamingOptions(&p, libIslands(&p)[0], "net_port_bi", func(candidate *powerLayoutPlan) bool {
		if err := validateLibGeometry(candidate); err != nil {
			t.Fatal(err)
		}
		count++
		if len(candidate.Wires) > len(p.Wires) {
			elbows++
		}
		f := candidate.Flags[len(candidate.Flags)-1]
		x, _ := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
		if x < low {
			low = x
		}
		if x > high {
			high = x
		}
		return false
	}, &budget)
	if count < 8 || count > 32 || elbows == 0 || high-low < 80 {
		t.Fatalf("candidate pool repeats local offsets: count=%d elbows=%d xspan=%g", count, elbows, high-low)
	}
	if budget < 0 || budget >= 10000 || !reflect.DeepEqual(p, before) {
		t.Fatal("option generator escaped its budget or modified the input")
	}
}
