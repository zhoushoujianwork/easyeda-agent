package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func applyOwnershipFixture(t *testing.T) (*schematicStateExpectation, map[string]any) {
	t.Helper()
	layout, modules := peripheralDirectFixture()
	layout.Wires = []SchematicWire{{Net: "EN", Points: [][2]float64{{0, 0}, {100, 0}, {200, 0}}}}
	e := &schematicStateExpectation{ExactParts: true, Parts: map[string]schematicPartExpectation{},
		Drawing:   &schematicDrawingExpectation{Wires: layout.Wires, Flags: layout.Flags},
		Ownership: &schematicOwnershipExpectation{ComponentIDs: layout.ComponentIDs, Modules: modules, NetRoles: map[string]string{"EN": "signal"}}}
	parts, wires := []any{}, []any{}
	wire := func(x0, y0, x1, y1 float64) map[string]any {
		return map[string]any{"x0": x0, "y0": y0, "x1": x1, "y1": y1}
	}
	for _, c := range layout.Placements {
		p := schematicPartExpectation{Pins: map[string]schematicPinExpectation{}}
		pins := []any{}
		for _, q := range c.Pins {
			q, nc := q, false
			p.Pins[q.Number] = schematicPinExpectation{X: &q.X, Y: &q.Y, Net: &q.Net, NC: &nc}
			pins = append(pins, map[string]any{"pinNumber": q.Number, "x": q.X, "y": q.Y, "net": q.Net, "noConnected": false})
		}
		e.Parts[c.Designator] = p
		parts = append(parts, map[string]any{"componentType": "part", "designator": c.Designator, "pinsAvailable": true, "pins": pins})
	}
	for _, w := range layout.Wires {
		for i := 1; i < len(w.Points); i++ {
			wires = append(wires, wire(w.Points[i-1][0], w.Points[i-1][1], w.Points[i][0], w.Points[i][1]))
		}
	}
	for _, f := range layout.Flags {
		x, y := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
		wires = append(wires, wire(f.PinX, f.PinY, x, y))
		parts = append(parts, map[string]any{"componentType": "netport", "net": f.Net, "x": x, "y": y, "rotation": flagBodyRotation["port"][f.Direction]})
	}
	return e, map[string]any{"components": parts, "wires": wires, "connectivitySummary": emptyDrawingSummary()}
}

func TestApplyOwnershipUsesFreshPhysicalWiresNotSameNamedPins(t *testing.T) {
	e, live := applyOwnershipFixture(t)
	if err := e.check(live, nil); err != nil {
		t.Fatal(err)
	}
	// Keep all EN names and real markers; only cut the core-to-peripheral edge.
	live["wires"] = live["wires"].([]any)[1:]
	if err := e.Ownership.check(live); err == nil || !strings.Contains(err.Error(), "peripheral-direct-missing") {
		t.Fatalf("raw disconnected data passed ownership check: %v", err)
	}
	if err := e.check(live, nil); err == nil {
		t.Fatal("final Apply assertion accepted disconnected raw drawing")
	}
}

func TestApplyExplicitNetLabelsRequiresFreshNamedLeads(t *testing.T) {
	e, live := applyOwnershipFixture(t)
	e.Ownership.ConnectionMode = "net-labels"
	e.Drawing.Wires = nil
	live["wires"] = live["wires"].([]any)[2:] // Keep only the three real marker leads.
	if err := e.validate(); err != nil {
		t.Fatal(err)
	}
	if err := e.check(live, nil); err != nil {
		t.Fatalf("complete named islands were rejected: %v", err)
	}
	wires := live["wires"].([]any)
	live["wires"] = wires[1:]
	if err := e.Ownership.check(live); err == nil || !strings.Contains(err.Error(), "no real lead") {
		t.Fatalf("pin with no named lead passed: %v", err)
	}
	live["wires"] = wires
	live["components"] = live["components"].([]any)[:len(live["components"].([]any))-1]
	if err := e.check(live, nil); err == nil {
		t.Fatal("missing marker primitive passed the complete drawing guard")
	}
	e.Ownership.ConnectionMode = ""
	if err := e.validate(); err == nil || !strings.Contains(err.Error(), "peripheral-direct-missing") {
		t.Fatalf("stripping explicit mode escaped the direct-connection guard: %v", err)
	}
}

func TestApplyOwnershipRoundTripAndRemovalFailsPreflight(t *testing.T) {
	e, _ := applyOwnershipFixture(t)
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip schematicStateExpectation
	if err := json.Unmarshal(raw, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if err := roundtrip.validate(); err != nil {
		t.Fatal(err)
	}
	delete(roundtrip.Ownership.NetRoles, "EN")
	if err := roundtrip.validate(); err == nil || !strings.Contains(err.Error(), "preserved") {
		t.Fatalf("stripping net classification escaped preflight: %v", err)
	}
	roundtrip.Ownership = nil
	if err := roundtrip.validate(); err == nil || !strings.Contains(err.Error(), "peripheral-ownership-incomplete") {
		t.Fatalf("stripping ownership escaped complete-drawing preflight: %v", err)
	}
	// The target itself cannot be label-only even when the intended netlist agrees.
	e.Drawing.Wires = nil
	if err := e.validate(); err == nil || !strings.Contains(err.Error(), "peripheral-direct-missing") {
		t.Fatalf("label-only target was accepted: %v", err)
	}
}

func TestApplyOwnershipMissingRawEvidenceFailsClosed(t *testing.T) {
	for _, mode := range []string{"wires", "pin-x", "pin-net", "pin-nc", "pins-unavailable", "pin-short", "wire-short", "no-real-leads"} {
		t.Run(mode, func(t *testing.T) {
			e, live := applyOwnershipFixture(t)
			part := live["components"].([]any)[0].(map[string]any)
			pin := part["pins"].([]any)[0].(map[string]any)
			switch mode {
			case "wires":
				delete(live, "wires")
			case "pin-x":
				delete(pin, "x")
			case "pin-net":
				delete(pin, "net")
			case "pin-nc":
				delete(pin, "noConnected")
			case "pins-unavailable":
				part["pinsAvailable"] = false
			case "pin-short":
				pin["net"] = "FOREIGN"
			case "wire-short":
				live["wires"].([]any)[0].(map[string]any)["net"] = "FOREIGN"
			case "no-real-leads":
				live["wires"] = []any{}
			}
			if err := e.Ownership.check(live); err == nil {
				t.Fatal("accepted missing/conflicting raw evidence or synthesized wires from target flags")
			}
		})
	}
}

func TestApplyCompositionPersistsOwnershipAndRejectsStrippedGuard(t *testing.T) {
	p, live := composeApplyFixture(t, true)
	pb, err := schCompositionPlaybook(p, composeApplyBytes(t, live), false)
	if err != nil {
		t.Fatal(err)
	}
	_, final := composeStep(t, pb, "verify-all-pins-nets-nc")
	if final.ExpectSchematic.Ownership == nil || len(final.ExpectSchematic.Ownership.Modules) != len(p.Connectivity.Modules) {
		t.Fatal("generated final guard lost canonical ownership")
	}
	final.ExpectSchematic.Ownership = nil
	if err := validateSchematicExpectationStep(final); err == nil {
		t.Fatal("guard with ownership removed passed Apply preflight")
	}
}
