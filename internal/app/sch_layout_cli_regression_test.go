package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// These frozen inputs came from the raw-requirement executor's measured symbols,
// not a completed layout. All formerly failing zones must pass the same budget,
// while retaining the source pins, NC declarations and rotation permissions.
func TestMeasuredCLIRegressionZones(t *testing.T) {
	for _, name := range []string{"usb", "mux", "uart", "boot", "mcu", "input", "slew", "buck", "detect", "led"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("../../docs/reviews/fixtures/2026-09-27-cli-layout", name+"-input.json"))
			if err != nil {
				t.Fatal(err)
			}
			in, err := decodeSchematicZonesInput(raw)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(in)
			out, err := PlanSchematicZones(in)
			if err != nil || out == nil || len(out.Zones) != 1 {
				t.Fatalf("incomplete zone: %v", err)
			}
			layout := out.Zones[0].Layout
			if layout.CandidatesUsed <= 0 || layout.CandidatesUsed > in.MaxCandidates {
				t.Fatal("candidate budget violated")
			}
			if layout.Search != nil {
				for _, attempt := range layout.Search.RelocationAttempts {
					if attempt.RegenerationCandidatesUsed < 0 || attempt.RegenerationCandidatesUsed > attempt.RegenerationCandidateQuota {
						t.Fatal("relocation regenerated outside its declared quota")
					}
				}
			}
			p := powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires, Flags: layout.Flags}
			if err := validateLibGeometry(&p); err != nil {
				t.Fatal(err)
			}
			if err := validateSchCompositionNets(&p); err != nil {
				t.Fatal(err)
			}
			for _, c := range in.Components {
				if len(layout.PinStates[c.ID]) != len(c.PinStates) {
					t.Fatal("NC contract changed", c.ID)
				}
				for number, state := range c.PinStates {
					if layout.PinStates[c.ID][number] != state {
						t.Fatal("NC contract changed", c.ID, number)
					}
				}
				actual := libPlacementByDesignator(&p, c.Measurement.Designator)
				if actual == nil || len(actual.Pins) != len(c.Measurement.Pins) {
					t.Fatal("lost measured component/pins")
				}
				for _, pin := range c.Measurement.Pins {
					got, ok := libPin(*actual, pin.Number)
					if !ok || got.Net != pin.Net {
						t.Fatal("pin net changed", c.ID, pin.Number)
					}
				}
			}
			for _, pair := range schematicRequiredAttachments(SchematicLayoutInput{Components: in.Components, Attachments: in.Attachments, NetPolicies: in.NetPolicies}) {
				a, b, ok := pair.pins(&p)
				if !ok || !libPinsShareIsland(&p, a, b) {
					t.Fatal("explicit attachment lost physical connection", pair)
				}
			}
			after, _ := json.Marshal(in)
			if !bytes.Equal(before, after) {
				t.Fatal("source geometry, permissions or connectivity mutated")
			}
			in.MaxCandidates = 1
			if partial, err := PlanSchematicZones(in); err == nil || partial != nil {
				t.Fatal("budget exhaustion returned an applicable partial layout")
			}
		})
	}
}

func TestRequiredAttachmentKeepsOtherNamedPowerIslandsSeparate(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{
		{Designator: "U1", BBox: layoutBBox{-10, -10, 10, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "SUPPLY", X: 20, Y: 0}}},
		{Designator: "C1", X: 120, BBox: layoutBBox{110, -10, 130, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "SUPPLY", X: 100, Y: 0}}},
		{Designator: "J1", X: 500, BBox: layoutBBox{490, -10, 510, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "SUPPLY", X: 480, Y: 0}}},
	}}
	ctx, _ := newSchematicRoutingContext(nil, nil)
	policies := map[string]string{"SUPPLY": "local_power"}
	ctx.policies = policies
	ctx.requiredConnections = []schematicRequiredConnection{{"U1", "1", "C1", "1", "SUPPLY"}}
	budget := 10000
	ctx.candidateBudget = &budget
	if err := libJoinRequiredAttachments(&p, policies, ctx); err != nil {
		t.Fatal(err)
	}
	if !libPinsShareIsland(&p, p.Placements[0].Pins[0], p.Placements[1].Pins[0]) {
		t.Fatal("lost explicit supply path")
	}
	if libPinsShareIsland(&p, p.Placements[0].Pins[0], p.Placements[2].Pins[0]) {
		t.Fatal("joined an unrelated same-name island")
	}
	if policies["SUPPLY"] != "local_power" || ctx.policies["SUPPLY"] != "local_power" || ctx.requiredJoin != nil {
		t.Fatal("temporary routing policy leaked")
	}
}

func TestNamingWireBlockerUsesPhysicalIslandAndFrozenFailure(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{
		{Designator: "A", Pins: []powerLayoutPin{{Number: "1", Net: "N", X: 0, Y: 0}}},
		{Designator: "B", Pins: []powerLayoutPin{{Number: "1", Net: "N", X: 100, Y: 0}}},
	}, Wires: []powerLayoutWire{{Net: "N", Points: [][2]float64{{0, 0}, {20, 0}}}, {Net: "N", Points: [][2]float64{{100, 0}, {120, 0}}}}, namingBlockers: map[string]bool{}}
	libRecordNamingWireBlockers(&p, p.Wires[0])
	if !p.namingBlockers["A"] || p.namingBlockers["B"] {
		t.Fatal("same-name disconnected island was attributed")
	}
	conflict := libNamingConflict(&p, libIslands(&p)[0])
	before, _ := json.Marshal(conflict.FailureDetails())
	p.Placements[0].Pins[0].Net = "CHANGED"
	p.Wires[0].Points[0][0] = 50
	after, _ := json.Marshal(conflict.FailureDetails())
	if !bytes.Equal(before, after) {
		t.Fatal("failure report aliases later candidate geometry")
	}
}

func TestAttachmentFacingPoseResolvesMixedParentAndChildRotations(t *testing.T) {
	in := SchematicLayoutInput{CoreComponentID: "core", Components: []SchematicLayoutComponent{
		{ID: "core", Measurement: powerLayoutPlacement{Designator: "U1", BBox: layoutBBox{-10, -10, 10, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "P", X: 0, Y: 20}}}},
		{ID: "parent", Measurement: powerLayoutPlacement{Designator: "R1", BBox: layoutBBox{-10, -10, 10, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "P", X: 20, Y: 0}, {Number: "2", Net: "Q", X: -20, Y: 0}}}, AllowedRotations: []float64{0, 90, 180, 270}},
		{ID: "child", Measurement: powerLayoutPlacement{Designator: "C1", BBox: layoutBBox{-10, -10, 10, 10}, Pins: []powerLayoutPin{{Number: "1", Net: "Q", X: 0, Y: 20}}}, AllowedRotations: []float64{0, 90, 180, 270}},
	}, Attachments: []SchematicLayoutPeripheral{
		{ComponentID: "child", PinNumber: "1", AttachTo: &SchematicLayoutAttach{ComponentID: "parent", PinNumber: "2"}},
		{ComponentID: "parent", PinNumber: "1", AttachTo: &SchematicLayoutAttach{ComponentID: "core", PinNumber: "1"}},
	}}
	allowed := map[string][]float64{"core": {0}, "parent": {0, 90, 180, 270}, "child": {0, 90, 180, 270}}
	before, _ := json.Marshal(in)
	pose := schematicAttachmentFacingPose(in, allowed)
	if !reflect.DeepEqual(pose, map[string]float64{"parent": 270, "child": 180}) {
		t.Fatalf("child did not follow transformed parent: %v", pose)
	}
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("source pose mutated")
	}
	measured := map[string]powerLayoutPlacement{}
	for _, c := range in.Components {
		measured[c.ID] = c.Measurement
	}
	budget, calls := 100000, 0
	_, report, err := runSchematicLayoutFeasibility(in, measured, allowed, &budget, func(candidate map[string]powerLayoutPlacement, quota *int) (*SchematicLayoutResult, error) {
		calls++
		if calls == 1 {
			if *quota != 75000 {
				t.Fatal("measured pose no longer received its original window")
			}
			*quota = 0
			return nil, errLibLayoutBudget
		}
		if calls != 2 || *quota != 12500 || candidate["parent"].Rotation != 270 || candidate["child"].Rotation != 180 {
			t.Fatal("coherent pose was starved or changed")
		}
		*quota -= 37
		return &SchematicLayoutResult{}, nil
	})
	if err != nil || calls != 2 || budget != 24963 || report.CandidatesUsed != 75037 {
		t.Fatal("shared fallback accounting", budget, report, err)
	}
	in.Components[2].AllowedRotations = nil
	if _, changed := schematicAttachmentFacingPose(in, allowed)["child"]; changed {
		t.Fatal("unapproved child rotation")
	}
}
