package app

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestInterleavedPortsMayKeepNamedIslandsButDirectMustJoin(t *testing.T) {
	// Synthetic four-pin connector, independent of any vendor/training circuit.
	in := SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: "connector", NetPolicies: map[string]string{"A": "module_port", "B": "module_port"}, Components: []SchematicLayoutComponent{{ID: "connector", Measurement: SchematicPlacement{Designator: "J1", BBox: SchematicBox{-10, -30, 10, 30}, Pins: []SchematicPin{{Number: "1", Net: "A", X: 30, Y: 15}, {Number: "2", Net: "B", X: 30, Y: 5}, {Number: "3", Net: "A", X: 30, Y: -5}, {Number: "4", Net: "B", X: 30, Y: -15}}}}}}
	before, _ := json.Marshal(in)
	out, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	p := powerLayoutPlan{Placements: out.Placements, Wires: out.Wires, Flags: out.Flags}
	if err = validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if err = validateSchCompositionNets(&p); err != nil {
		t.Fatal(err)
	}
	again, err := PlanSchematicLayout(in)
	if err != nil || !reflect.DeepEqual(out, again) {
		t.Fatal("nondeterministic", err)
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("modified evidence")
	}
	in.NetPolicies["A"] = "direct"
	in.NetPolicies["B"] = "direct"
	if direct, err := PlanSchematicLayout(in); err == nil || direct != nil {
		t.Fatal("silently split mandatory direct nets")
	}
}

func TestNamingBudgetExhaustionDoesNotPublishPartial(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "U1", BBox: layoutBBox{-20, -45, 20, 45}, Pins: []powerLayoutPin{{Number: "1", Net: "R", X: 35, Y: 0}, {Number: "2", Net: "S", X: 35, Y: 10}}}}}
	before, _ := json.Marshal(p)
	budget := 1
	err := libNameIslands(&p, map[string]string{"R": "local_ground", "S": "local_power"}, &budget)
	if !errors.Is(err, errLibLayoutBudget) || budget != 0 {
		t.Fatal(err, budget)
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("partial naming escaped on exhaustion")
	}
}

func TestDenseMixedNamingRetriesPreserveGeometry(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "U1", BBox: layoutBBox{-20, -45, 20, 45}, Pins: []powerLayoutPin{{Number: "1", Net: "RETURN", X: 35, Y: 0}, {Number: "2", Net: "SUPPLY", X: 35, Y: 10}, {Number: "3", Net: "CONTROL", X: 35, Y: 20}}}}}
	original := p.Placements
	if err := libNameIslands(&p, map[string]string{"RETURN": "local_ground", "SUPPLY": "local_power", "CONTROL": "module_port"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, p.Placements) {
		t.Fatal("naming moved component")
	}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if err := validateSchCompositionNets(&p); err != nil {
		t.Fatal(err)
	}
	if len(p.Flags) != 3 {
		t.Fatal("incomplete naming")
	}
}
