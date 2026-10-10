package app

import (
	"encoding/json"
	"testing"
)

func minimumAttachmentFixture(minimum float64) SchematicLayoutInput {
	return SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: "core", MaxCandidates: 20000, NetPolicies: map[string]string{"BUS": "direct"}, Components: []SchematicLayoutComponent{
		{ID: "core", Measurement: SchematicPlacement{Designator: "U1", BBox: SchematicBox{-10, -10, 10, 10}, TextBBoxes: []SchematicBox{{-10, 15, 5, 23}}, Pins: []SchematicPin{{Number: "1", Net: "BUS", X: 20, Y: 0, Rotation: directionNumber(0)}}}},
		{ID: "probe", Measurement: SchematicPlacement{Designator: "TP1", X: 100, Y: 0, BBox: SchematicBox{94.5, -5.5, 105.5, 5.5}, TextBBoxes: []SchematicBox{{95, 10, 115, 18}}, Pins: []SchematicPin{{Number: "1", Net: "BUS", X: 80, Y: 0, Rotation: directionNumber(180)}}}},
	}, Attachments: []SchematicLayoutPeripheral{{ComponentID: "probe", PinNumber: "1", AttachTo: &SchematicLayoutAttach{ComponentID: "core", PinNumber: "1"}, MinimumAttachmentDistance: minimum}}}
}
func TestMinimumAttachmentDistanceProducesLongerPhysicalBridge(t *testing.T) {
	input := minimumAttachmentFixture(40)
	before, _ := json.Marshal(input)
	out, err := PlanSchematicLayout(input)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]powerLayoutPlacement{}
	for _, p := range out.Placements {
		refs[p.Designator] = p
	}
	own := refs["TP1"]
	host := refs["U1"]
	pin, _ := libPin(own, "1")
	source, _ := libPin(host, "1")
	if pin.X-source.X < 40 {
		t.Fatalf("physical pin gap was not kept: %g", pin.X-source.X)
	}
	p := powerLayoutPlan{Placements: out.Placements, Wires: out.Wires, Flags: out.Flags}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if err := validateSchCompositionNets(&p); err != nil {
		t.Fatal(err)
	}
	if !libPinsShareIsland(&p, source, pin) {
		t.Fatal("minimum distance substituted labels for the direct wire")
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("caller measurement evidence modified")
	}
	original := input.Components[1].Measurement
	if own.BBox.MinX-own.X != original.BBox.MinX-original.X || own.BBox.MaxY-own.Y != original.BBox.MaxY-original.Y || pin.X-own.X != original.Pins[0].X-original.X {
		t.Fatal("measured shape changed")
	}
	moved := *out
	moved.Placements = append([]powerLayoutPlacement(nil), out.Placements...)
	for i, c := range moved.Placements {
		if c.Designator == "TP1" {
			moved.Placements[i] = plTranslate(c, source.X+35-pin.X, 0)
		}
	}
	measured := map[string]powerLayoutPlacement{"core": host, "probe": own}
	hints := map[string]SchematicLayoutPeripheral{"probe": input.Attachments[0]}
	if validateMinimumAttachmentDistances(&moved, measured, hints) == nil {
		t.Fatal("relocation violated the declared minimum")
	}
}
func TestMinimumAttachmentDistanceFinalGuardAllOutwardSides(t *testing.T) {
	for _, sample := range []struct {
		rotation     float64
		ownX, ownY   float64
		hostX, hostY float64
	}{
		{0, 60, 0, 20, 0}, {180, -60, 0, -20, 0}, {90, 0, 60, 0, 20}, {270, 0, -60, 0, -20},
	} {
		own := powerLayoutPlacement{Designator: "TP1", Pins: []powerLayoutPin{{Number: "1", X: sample.ownX, Y: sample.ownY}}}
		host := powerLayoutPlacement{Designator: "U1", Pins: []powerLayoutPin{{Number: "1", X: sample.hostX, Y: sample.hostY, Rotation: directionNumber(sample.rotation)}}}
		result := &SchematicLayoutResult{Placements: []SchematicPlacement{host, own}}
		measured := map[string]powerLayoutPlacement{"core": host, "probe": own}
		hints := map[string]SchematicLayoutPeripheral{"probe": minimumAttachmentFixture(40).Attachments[0]}
		if err := validateMinimumAttachmentDistances(result, measured, hints); err != nil {
			t.Fatalf("rotation %g: %v", sample.rotation, err)
		}
		result.Placements[1] = plTranslate(own, (sample.hostX-sample.ownX)/8, (sample.hostY-sample.ownY)/8)
		if err := validateMinimumAttachmentDistances(result, measured, hints); err == nil {
			t.Fatalf("rotation %g accepted 35 raw after relocation", sample.rotation)
		}
	}
}
func TestMinimumAttachmentDistanceRejectsInvalidAndUnsupportedInput(t *testing.T) {
	for _, v := range []float64{-5, 3, 7.5, 405} {
		if out, err := PlanSchematicLayout(minimumAttachmentFixture(v)); err == nil || out != nil {
			t.Fatal(v, out, err)
		}
	}
	in := minimumAttachmentFixture(40)
	in.LayoutMode = "unbounded"
	if out, err := PlanSchematicLayout(in); err == nil || out != nil {
		t.Fatal("unbounded silently ignored minimum", out, err)
	}
	in = minimumAttachmentFixture(40)
	in.Optimization = &SchematicLayoutOptimization{}
	if out, err := PlanSchematicLayout(in); err == nil || out != nil {
		t.Fatal("optimization silently ignored minimum", out, err)
	}
	in = minimumAttachmentFixture(40)
	in.LayoutMode = "net-labels"
	if out, err := PlanSchematicLayout(in); err == nil || out != nil {
		t.Fatal("net-labels silently ignored minimum", out, err)
	}
	in = minimumAttachmentFixture(40)
	in.Attachments[0].AttachTo.PinNumber = ""
	if out, err := PlanSchematicLayout(in); err == nil || out != nil {
		t.Fatal("missing explicit target pin", out, err)
	}
	in = minimumAttachmentFixture(40)
	in.Attachments[0].PinNumber = ""
	if out, err := PlanSchematicLayout(in); err == nil || out != nil {
		t.Fatal("missing explicit own pin", out, err)
	}
	in = minimumAttachmentFixture(40)
	in.Attachments[0].AttachTo = nil
	if out, err := PlanSchematicLayout(in); err == nil || out != nil {
		t.Fatal("missing explicit target", out, err)
	}
}
