package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The project-wide designator guard runs on every page of the project, so a
// multi-unit device placed on another page must not block the page being
// composed. The units of one device legitimately repeat the designator.
func TestProjectDesignatorGuardAcceptsMultiUnitDeviceOnAnotherPage(t *testing.T) {
	raw, err := os.ReadFile("testdata/multiunit-project-guard.json")
	if err != nil {
		t.Fatal(err)
	}
	var read map[string]any
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	expected := &schematicStateExpectation{
		DesignatorsOnly: true,
		Parts: map[string]schematicPartExpectation{
			"U17": {PrimitiveID: "u17"},
			"U20": {PrimitiveID: "u20"},
			"CN1": {PrimitiveID: "cn1"},
		},
		AbsentParts: []string{"U21"},
	}
	if err := expected.check(read, nil); err != nil {
		t.Fatalf("project designator guard rejected a multi-unit device on another page: %v", err)
	}

	// Two different devices wearing one designator are still a duplicate.
	components := read["components"].([]any)
	clone := map[string]any{}
	encoded, _ := json.Marshal(components[0])
	_ = json.Unmarshal(encoded, &clone)
	clone["primitiveId"] = "other-u18"
	clone["uniqueId"] = "gge9_1"
	clone["pageUuid"] = "page-usb1"
	read["components"] = append(components, clone)
	if err := expected.check(read, nil); err == nil || !strings.Contains(err.Error(), "duplicate part designator U18") {
		t.Fatalf("a second device wearing U18 passed the project guard: %v", err)
	}
	read["components"] = components

	// One device identity may not span two pages either.
	clone = map[string]any{}
	encoded, _ = json.Marshal(components[0])
	_ = json.Unmarshal(encoded, &clone)
	clone["primitiveId"] = "other-u18"
	clone["pageUuid"] = "page-usb1"
	read["components"] = append(components, clone)
	if err := expected.check(read, nil); err == nil || !strings.Contains(err.Error(), "spans pages") {
		t.Fatalf("a device instance spanning two pages passed the project guard: %v", err)
	}
}

// The target-page guard reads the placed records of one device instance and
// checks the union of their pins, so a multi-unit device is attributed by its
// physical inventory instead of by one unit symbol.
func TestSchematicStateExpectationChecksMultiUnitDevicePins(t *testing.T) {
	components := []any{
		map[string]any{
			"componentType": "part", "designator": "U18", "primitiveId": "u18-a", "uniqueId": "gge1_3",
			"pinsAvailable": true,
			"pins": []any{
				map[string]any{"pinNumber": "A1", "net": "VCC"},
				map[string]any{"pinNumber": "A2", "net": "GND"},
			},
		},
		map[string]any{
			"componentType": "part", "designator": "U18", "primitiveId": "u18-b", "uniqueId": "gge1_3",
			"pinsAvailable": true,
			"pins": []any{
				map[string]any{"pinNumber": "B1", "net": "VCC"},
			},
		},
	}
	expected := &schematicStateExpectation{
		Parts: map[string]schematicPartExpectation{
			"U18": {
				PrimitiveID: "u18-a",
				Pins: map[string]schematicPinExpectation{
					"A1": {Net: stringPtr("VCC")},
					"A2": {Net: stringPtr("GND")},
					"B1": {Net: stringPtr("VCC")},
				},
			},
		},
	}
	if err := expected.check(map[string]any{"components": components}, nil); err != nil {
		t.Fatalf("multi-unit device pins were not attributed to one device: %v", err)
	}
	expected.Parts["U18"] = schematicPartExpectation{
		PrimitiveID: "u18-a",
		Pins: map[string]schematicPinExpectation{
			"A1": {Net: stringPtr("VCC")},
			"A2": {Net: stringPtr("GND")},
		},
	}
	if err := expected.check(map[string]any{"components": components}, nil); err == nil || !strings.Contains(err.Error(), "unexpected pin B1") {
		t.Fatalf("a pin of the second unit was not detected: %v", err)
	}
}

func stringPtr(v string) *string { return &v }
