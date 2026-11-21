package app

import "testing"

// The unwired checkpoints run before any wire exists; an anonymous-net pin
// must not demand a net there (live: "R101.2 net is unknown or ambiguous").
func TestSchDropPinNetsClearsNamedAndAnonymousNets(t *testing.T) {
	named, anon, nc := "GND", "$2N10", false
	e := &schematicStateExpectation{Parts: map[string]schematicPartExpectation{
		"R101": {Pins: map[string]schematicPinExpectation{"2": {AutoNet: &anon, NC: &nc}}},
		"R102": {Pins: map[string]schematicPinExpectation{"1": {AutoNet: &anon, NC: &nc}, "2": {Net: &named, NC: &nc}}},
	}}
	schDropPinNets(e)
	for ref, part := range e.Parts {
		for number, pin := range part.Pins {
			if pin.Net != nil || pin.AutoNet != nil || pin.NC == nil {
				t.Fatalf("%s.%s kept a net assertion or lost NC: %+v", ref, number, pin)
			}
		}
	}
	result := map[string]any{"components": []any{
		map[string]any{"componentType": "part", "designator": "R101", "pinsAvailable": true, "pins": []any{map[string]any{"pinNumber": "2", "net": "", "noConnected": false}}},
		map[string]any{"componentType": "part", "designator": "R102", "pinsAvailable": true, "pins": []any{map[string]any{"pinNumber": "1", "net": "", "noConnected": false}, map[string]any{"pinNumber": "2", "net": "", "noConnected": false}}},
	}}
	if err := e.check(result, nil); err != nil {
		t.Fatalf("unwired readback rejected after dropping nets: %v", err)
	}
}
