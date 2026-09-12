package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Deliberately asymmetric, synthetic geometry: the closest legal R2 placement
// prevents R3's naming corridor. Releasing R2's checkpoint relocates it and
// recomputes the R2/R3 routes; array permutation alone cannot pass this test.
func schematicRepairFixture() SchematicLayoutInput {
	return SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: "core", MaxCandidates: 20000,
		NetPolicies: map[string]string{"G": "local_ground", "N0": "module_port", "N1": "module_port", "N2": "module_port"},
		Components: []SchematicLayoutComponent{
			{ID: "core", Measurement: SchematicPlacement{Designator: "U1", BBox: SchematicBox{-20, -40, 20, 40}, Pins: []SchematicPin{
				{Number: "4", Net: "G", X: 0, Y: -50}, {Number: "1", Net: "N0", X: 30, Y: 20}, {Number: "2", Net: "N1", X: 30, Y: 0}, {Number: "3", Net: "N2", X: 30, Y: -20}, {Number: "5", X: -30, Y: 20},
			}}, PinStates: map[string]string{"5": "nc"}},
			{ID: "R1", Measurement: SchematicPlacement{Designator: "R1", BBox: SchematicBox{-5, -20, 5, 20}, Pins: []SchematicPin{{Number: "1", Net: "N0", X: 0, Y: -30}, {Number: "2", Net: "G", X: 0, Y: 30}}}},
			{ID: "R2", Measurement: SchematicPlacement{Designator: "R2", Rotation: 180, Mirror: true, BBox: SchematicBox{-15, -15, 15, 15}, Pins: []SchematicPin{{Number: "1", Net: "N1", X: -25, Y: 0}, {Number: "2", Net: "G", X: 25, Y: 0}}}},
			{ID: "R3", Measurement: SchematicPlacement{Designator: "R3", BBox: SchematicBox{-10, -15, 10, 15}, Pins: []SchematicPin{{Number: "1", Net: "N2", X: 0, Y: 25}, {Number: "2", Net: "G", X: 0, Y: -25}}}},
		}}
}

func TestSchematicRepairMovesPreviouslyPlacedPeripheral(t *testing.T) {
	in := schematicRepairFixture()
	before, _ := json.Marshal(in)
	measured, members := map[string]powerLayoutPlacement{}, []string{}
	for _, c := range in.Components {
		measured[c.ID], members = c.Measurement, append(members, c.ID)
	}
	greedyBudget := in.MaxCandidates
	greedy := newSchematicRepairSearch(in, measured, members, nil, &greedyBudget)
	greedy.diagnostics.BranchLimit = 1 // First failing descendant; no relocation.
	if out, err := greedy.solve(powerLayoutPlan{Placements: []powerLayoutPlacement{measured["core"]}}, members[1:]); err == nil || out != nil {
		t.Fatal("fixture no longer exercises a failed greedy prefix", err)
	}
	if greedy.diagnostics.Backtracks > greedy.diagnostics.BranchLimit {
		t.Fatal("ancestor rollback incremented an exhausted branch limit")
	}
	out, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Search == nil || out.Search.Backtracks == 0 || out.Search.RepairAttempts == 0 || !strings.Contains(strings.Join(out.Search.MovedComponents, ","), "R2") {
		t.Fatalf("missing actual relocation diagnostics: %+v", out.Search)
	}
	for i, c := range out.Placements {
		m := measured[out.ComponentIDs[c.Designator]]
		if c.Rotation != m.Rotation || c.Mirror != m.Mirror || len(c.Pins) != len(m.Pins) {
			t.Fatal("changed measured pose/pin membership")
		}
		if c.Designator == "R2" && greedy.firstXY["R2"] == [2]float64{c.X, c.Y} {
			t.Fatal("previously placed R2 was not moved")
		}
		for j, q := range c.Pins {
			if q.Number != m.Pins[j].Number || q.Net != m.Pins[j].Net || q.X-c.X != m.Pins[j].X-m.X || q.Y-c.Y != m.Pins[j].Y-m.Y {
				t.Fatal("changed pin/net/relative geometry")
			}
		}
		if i == 0 && (c.Designator != "U1" || c.X != 0 || c.Y != 0) {
			t.Fatal("moved core")
		}
	}
	if out.PinStates["core"]["5"] != "nc" {
		t.Fatal("lost NC")
	}
	p := powerLayoutPlan{Placements: out.Placements, Wires: out.Wires, Flags: out.Flags}
	if err = validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if err = validateSchCompositionNets(&p); err != nil {
		t.Fatal("repaired suffix has invalid or stale routes", err)
	}
	again, err := PlanSchematicLayout(in)
	if err != nil || !reflect.DeepEqual(out, again) {
		t.Fatal("search is not deterministic", err)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated source evidence")
	}
	if out.CandidatesUsed > in.MaxCandidates || out.CandidatesUsed <= 0 {
		t.Fatal("invalid shared budget accounting")
	}
}

func TestRouteConflictIncludesWireOwnersAndKeepsUnknownOwnersSearchable(t *testing.T) {
	a := powerLayoutPlacement{Designator: "A", BBox: layoutBBox{-120, -20, -80, 20}, Pins: []powerLayoutPin{{Number: "1", Net: "N", X: -70, Y: 0}}}
	b := powerLayoutPlacement{Designator: "B", BBox: layoutBBox{80, -20, 120, 20}, Pins: []powerLayoutPin{{Number: "1", Net: "N", X: 70, Y: 0}}}
	owner := powerLayoutPlacement{Designator: "Q", BBox: layoutBBox{190, 190, 210, 210}, Pins: []powerLayoutPin{{Number: "1", Net: "BLOCK", X: 180, Y: 200}}}
	barrier := powerLayoutWire{Net: "BLOCK", Points: [][2]float64{{0, -300}, {0, 300}}}
	for _, known := range []bool{true, false} {
		p := powerLayoutPlan{Placements: []powerLayoutPlacement{a, b}, Wires: []powerLayoutWire{barrier}}
		if known {
			p.Placements = append(p.Placements, owner)
		}
		err := libJoinNetsMode(&p, map[string]string{"N": "direct", "BLOCK": "module_port"}, false, false)
		var conflict *schematicRouteConflict
		if !errors.As(err, &conflict) || conflict.net != "N" || conflict.ownersComplete != known {
			t.Fatal("missing structured route conflict/ownership", err)
		}
		if known && !conflict.blockers["Q"] {
			t.Fatal("ignored a component whose wire, but not body, blocks the route")
		}
		s := schematicRepairSearch{measured: map[string]powerLayoutPlacement{"owner": owner, "other": {Designator: "R", Pins: []powerLayoutPin{{Net: "OTHER"}}}}}
		if !s.participates("owner", conflict) || (!known && !s.participates("other", conflict)) {
			t.Fatal("unsafe pruning for real or unknown wire owners")
		}
		s.focused = true
		if !known && !s.participates("other", conflict) {
			t.Fatal("focused pass pruned unknown wire ownership")
		}
	}
}

func TestSchematicRepairFailureKeepsSharedBudgetAndNoPartialResult(t *testing.T) {
	in := schematicRepairFixture()
	before, _ := json.Marshal(in)
	for _, maximum := range []int{1, 7, 128, 512} {
		budget := maximum
		out, err := planSchematicLayoutWithBudget(in, &budget)
		if err == nil || out != nil || !errors.Is(err, errLibLayoutBudget) {
			t.Fatalf("max=%d: invalid failure result %+v, %v", maximum, out, err)
		}
		if budget < 0 || budget >= maximum || !strings.Contains(err.Error(), "no capacity proof") {
			t.Fatal("budget reset/underflow or ambiguous failure", budget, err)
		}
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("failed search changed evidence")
	}
}

func TestAttachmentPairsShareDistanceShellBudget(t *testing.T) {
	core := powerLayoutPlacement{Designator: "U1", BBox: layoutBBox{-20, -20, 20, 20}, Pins: []powerLayoutPin{{Number: "L", Net: "N", X: -30, Y: 0}, {Number: "R", Net: "N", X: 30, Y: 0}}}
	wall := powerLayoutPlacement{Designator: "X1", BBox: layoutBBox{-60, -300, -34, 300}, TextBBoxes: []layoutBBox{{-60, -10, -50, 10}}, Pins: []powerLayoutPin{{Number: "1", X: -70, Y: 0}}}
	part := powerLayoutPlacement{Designator: "R1", BBox: layoutBBox{-5, -5, 5, 5}, Pins: []powerLayoutPin{{Number: "1", Net: "N", X: -15, Y: 0}, {Number: "2", Net: "G", X: 15, Y: 0}}}
	current := powerLayoutPlan{Placements: []powerLayoutPlacement{core, wall}}
	bad := libAttachmentPair{host: core.Pins[0], own: part.Pins[0], side: "left"}
	good := libAttachmentPair{host: core.Pins[1], own: part.Pins[0], side: "right"}
	policies := map[string]string{"N": "local_power", "G": "local_ground"}
	budget := 1000
	if out, err := libPlacePeripheral(current, part, bad, policies, &budget); out != nil || err == nil {
		t.Fatal("first pair should be blocked by the fixed wall")
	}
	budget = 1000
	out, err := libPlacePeripheralPairs(current, part, []libAttachmentPair{bad, good}, policies, &budget, nil)
	if err != nil || out == nil || out.Placements[2].X <= 0 || budget <= 0 {
		t.Fatalf("first blocked pair monopolized the budget: %v; remaining=%d", err, budget)
	}
}
