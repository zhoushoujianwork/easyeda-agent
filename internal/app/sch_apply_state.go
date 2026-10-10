package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

// These expectations refer to physical part designators and pin numbers, never
// list indexes. Pins is exhaustive per part; optional values select the facts to
// check before placement, before wiring, or after the final topology readback.
type schematicStateExpectation struct {
	AbsentParts     []string                            `json:"absentParts,omitempty"`
	ExactParts      bool                                `json:"exactParts,omitempty"`
	DesignatorsOnly bool                                `json:"designatorsOnly,omitempty"`
	Parts           map[string]schematicPartExpectation `json:"parts"`
	Drawing         *schematicDrawingExpectation        `json:"drawing,omitempty"`
	Ownership       *schematicOwnershipExpectation      `json:"ownership,omitempty"`
	SourceScene     map[string]any                      `json:"sourceScene,omitempty"`
	PagePrimitives  map[string]any                      `json:"pagePrimitives,omitempty"`
	OwnedAttributes *schOwnedAttributesExpectation      `json:"ownedAttributes,omitempty"`
}

type schematicPartExpectation struct {
	PrimitiveID string                             `json:"primitiveId,omitempty"`
	Device      *schematicDeviceExpectation        `json:"device,omitempty"`
	X           *float64                           `json:"x,omitempty"`
	Y           *float64                           `json:"y,omitempty"`
	Rotation    *float64                           `json:"rotation,omitempty"`
	Mirror      *bool                              `json:"mirror,omitempty"`
	BBox        *layoutBBox                        `json:"bbox,omitempty"`
	Pins        map[string]schematicPinExpectation `json:"pins"`
	Instance    map[string]any                     `json:"instance,omitempty"`
}

// This is the hydrated library identity from components.list with
// includeDeviceIdentity:true, never the 16-character placed-symbol id.
type schematicDeviceExpectation struct {
	LibraryUUID string `json:"libraryUuid"`
	UUID        string `json:"uuid"`
}

func (p *schematicPartExpectation) UnmarshalJSON(data []byte) error {
	type plain schematicPartExpectation
	var value plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if device, exists := fields["device"]; exists && bytes.Equal(bytes.TrimSpace(device), []byte("null")) {
		return fmt.Errorf("expectSchematic device cannot be null; omit it only when identity is not checked")
	}
	if instance, exists := fields["instance"]; exists && bytes.Equal(bytes.TrimSpace(instance), []byte("null")) {
		return fmt.Errorf("expectSchematic instance cannot be null")
	}
	*p = schematicPartExpectation(value)
	return nil
}

func (d schematicDeviceExpectation) validate() error {
	if strings.TrimSpace(d.LibraryUUID) == "" || !isDeviceLibraryUUID(d.UUID) {
		return fmt.Errorf("device requires libraryUuid and a 32-character device-library uuid")
	}
	return nil
}

func measuredSchematicDevice(ref string, have map[string]any) (*schematicDeviceExpectation, error) {
	// The fresh independent proof failure explains why legacy recovery failed;
	// surface it before the original connector mismatch it was meant to adapt.
	for _, key := range []string{"deviceIdentityCompatibilityError", "deviceIdentityError"} {
		if problem, exists := have[key]; exists && problem != nil && problem != "" {
			return nil, fmt.Errorf("%s device identity is unresolved: %v", ref, problem)
		}
	}
	device, ok := have["device"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s device identity is unavailable; refresh sch list --include-device-identity", ref)
	}
	library, _ := device["libraryUuid"].(string)
	uuid, _ := device["uuid"].(string)
	d := &schematicDeviceExpectation{LibraryUUID: library, UUID: uuid}
	if err := d.validate(); err != nil {
		return nil, fmt.Errorf("%s device identity is incomplete or is a placed-instance id; refresh sch list --include-device-identity: %w", ref, err)
	}
	return d, nil
}

type schematicPinExpectation struct {
	Name       *string        `json:"name,omitempty"`
	X          *float64       `json:"x,omitempty"`
	Y          *float64       `json:"y,omitempty"`
	Net        *string        `json:"net,omitempty"`
	NC         *bool          `json:"noConnected,omitempty"`
	Persistent map[string]any `json:"persistent,omitempty"`
}

// Explicit null must not silently mean "do not check this fact". Omission is
// useful for geometry-only gates, while null denotes unavailable information.
func (p *schematicPinExpectation) UnmarshalJSON(data []byte) error {
	type plain schematicPinExpectation
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var value plain
	if err := dec.Decode(&value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"net", "noConnected", "name", "persistent"} {
		if value, exists := fields[key]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("expectSchematic pin %s cannot be null; omit it when this fact is not checked", key)
		}
	}
	*p = schematicPinExpectation(value)
	return nil
}

type schematicExpectationError struct{ cause error }

func (e *schematicExpectationError) Error() string { return "expectSchematic: " + e.cause.Error() }
func (e *schematicExpectationError) Unwrap() error { return e.cause }

func validateSchematicExpectationStep(s *playbookStep) error {
	if s.ExpectSchematic == nil {
		return nil
	}
	if s.Action != "schematic.components.list" {
		return fmt.Errorf("expectSchematic requires action schematic.components.list")
	}
	if !s.ExpectSchematic.DesignatorsOnly && s.Payload["includePins"] != true {
		return fmt.Errorf("expectSchematic requires action schematic.components.list with includePins:true")
	}
	for _, part := range s.ExpectSchematic.Parts {
		if part.Device != nil && s.Payload["includeDeviceIdentity"] != true {
			return fmt.Errorf("expectSchematic device requires includeDeviceIdentity:true")
		}
		if part.BBox != nil && s.Payload["includeBBox"] != true {
			return fmt.Errorf("expectSchematic bbox requires includeBBox:true")
		}
	}
	if s.ExpectSchematic.Drawing != nil && s.Payload["includeWires"] != true {
		return fmt.Errorf("expectSchematic drawing requires includeWires:true")
	}
	if s.ExpectSchematic.Drawing != nil && s.Payload["includeConnectivitySummary"] != true {
		return fmt.Errorf("expectSchematic drawing requires includeConnectivitySummary:true")
	}
	if s.ExpectSchematic.SourceScene != nil {
		if s.Payload["includeWires"] != true || s.Payload["includeConnectivitySummary"] != true || s.Payload["includePagePrimitives"] != true {
			return fmt.Errorf("expectSchematic sourceScene requires includeWires, includeConnectivitySummary and includePagePrimitives:true")
		}
	}
	if (s.ExpectSchematic.PagePrimitives != nil || s.ExpectSchematic.OwnedAttributes != nil) && s.Payload["includePagePrimitives"] != true {
		return fmt.Errorf("expectSchematic native inventory requires includePagePrimitives:true")
	}
	return s.ExpectSchematic.validate()
}

func (e *schematicStateExpectation) validate() error {
	if e.Parts == nil || (len(e.Parts) == 0 && !e.ExactParts && len(e.AbsentParts) == 0) {
		return fmt.Errorf("expectSchematic.parts must not be empty")
	}
	absent := map[string]bool{}
	for _, ref := range e.AbsentParts {
		_, present := e.Parts[ref]
		if strings.TrimSpace(ref) == "" || absent[ref] || present {
			return fmt.Errorf("invalid/conflicting absentParts ref %q", ref)
		}
		absent[ref] = true
	}
	for _, ref := range sortedStateKeys(e.Parts) {
		part := e.Parts[ref]
		if strings.TrimSpace(ref) == "" || (!e.DesignatorsOnly && part.Pins == nil) {
			return fmt.Errorf("expectSchematic part %q requires a designator and exhaustive pins map", ref)
		}
		if e.DesignatorsOnly {
			if part.Device != nil || part.X != nil || part.Y != nil || part.Rotation != nil || part.Mirror != nil || part.BBox != nil || part.Pins != nil || part.Instance != nil {
				return fmt.Errorf("designatorsOnly part %q may only check primitiveId", ref)
			}
			continue
		}
		if part.Instance != nil {
			if err := validateSchPreservedInstance(part.Instance); err != nil {
				return fmt.Errorf("%s instance: %w", ref, err)
			}
			if part.PrimitiveID == "" {
				return fmt.Errorf("%s instance guard requires original primitiveId", ref)
			}
		}
		if part.Device != nil {
			if err := part.Device.validate(); err != nil {
				return fmt.Errorf("%s: %w", ref, err)
			}
		}
		for field, value := range map[string]*float64{"x": part.X, "y": part.Y, "rotation": part.Rotation} {
			if value != nil && !finiteStateNumber(*value) {
				return fmt.Errorf("%s.%s must be finite", ref, field)
			}
		}
		if part.BBox != nil && !plBoxValid(*part.BBox) {
			return fmt.Errorf("%s.bbox must have finite coordinates and positive width/height", ref)
		}
		for _, number := range sortedStateKeys(part.Pins) {
			pin := part.Pins[number]
			if strings.TrimSpace(number) == "" {
				return fmt.Errorf("%s has an empty pin number", ref)
			}
			if pin.Persistent != nil {
				if err := validateSchPreservedPin(number, pin.Persistent); err != nil {
					return fmt.Errorf("%s.%s persistent pin: %w", ref, number, err)
				}
			}
			if pin.Net != nil && strings.TrimSpace(*pin.Net) == "" && (*pin.Net != "" || pin.NC == nil) {
				return fmt.Errorf("%s.%s empty expected net requires explicit noConnected:true/false; whitespace is not a net", ref, number)
			}
			if pin.Net != nil && *pin.Net != "" && pin.NC != nil && *pin.NC {
				return fmt.Errorf("%s.%s cannot expect both a net and noConnected:true", ref, number)
			}
			for field, value := range map[string]*float64{"x": pin.X, "y": pin.Y} {
				if value != nil && !finiteStateNumber(*value) {
					return fmt.Errorf("%s.%s.%s must be finite", ref, number, field)
				}
			}
		}
	}
	if e.Drawing != nil {
		if e.DesignatorsOnly {
			return fmt.Errorf("designatorsOnly cannot check drawing")
		}
		if err := e.Drawing.validate(); err != nil {
			return err
		}
	}
	return e.validateOwnership()
}

func (e *schematicStateExpectation) jsonValue() any {
	raw, _ := json.Marshal(e)
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}

func (e *schematicStateExpectation) substitutionValue() map[string]any {
	// User property strings (including literal ${...}) are data, not playbook
	// expressions. Only the existing geometry/locator expectation is substituted.
	base := e.jsonValue().(map[string]any)
	delete(base, "sourceScene")
	delete(base, "pagePrimitives")
	delete(base, "ownedAttributes")
	if parts, ok := base["parts"].(map[string]any); ok {
		for _, p := range parts {
			if part, ok := p.(map[string]any); ok {
				delete(part, "instance")
				if pins, ok := part["pins"].(map[string]any); ok {
					for _, value := range pins {
						if pin, ok := value.(map[string]any); ok {
							delete(pin, "persistent")
						}
					}
				}
			}
		}
	}
	return base
}

func (e *schematicStateExpectation) check(result any, vars map[string]string) error {
	value, err := substVars(e.substitutionValue(), vars)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var expected schematicStateExpectation
	if err := json.Unmarshal(raw, &expected); err != nil {
		return err
	}
	expected.SourceScene = e.SourceScene
	expected.PagePrimitives = e.PagePrimitives
	expected.OwnedAttributes = e.OwnedAttributes
	for ref, part := range expected.Parts {
		part.Instance = e.Parts[ref].Instance
		for number, pin := range part.Pins {
			pin.Persistent = e.Parts[ref].Pins[number].Persistent
			part.Pins[number] = pin
		}
		expected.Parts[ref] = part
	}
	if err := expected.validate(); err != nil {
		return err
	}
	root, ok := result.(map[string]any)
	if !ok {
		return fmt.Errorf("missing schematic components result")
	}
	if expected.SourceScene != nil {
		if err := checkSchPreservedSource(expected.SourceScene, root); err != nil {
			return err
		}
	}
	if expected.PagePrimitives != nil {
		if err := checkSchPageInventory(expected.PagePrimitives, root); err != nil {
			return err
		}
	}
	if expected.OwnedAttributes != nil {
		if err := expected.OwnedAttributes.check(root); err != nil {
			return err
		}
	}
	components, ok := root["components"].([]any)
	if !ok {
		return fmt.Errorf("components must be an available array")
	}
	parts := map[string]map[string]any{}
	for _, entry := range components {
		part, ok := entry.(map[string]any)
		if !ok {
			return fmt.Errorf("malformed component record")
		}
		kind, ok := part["componentType"].(string)
		if !ok || kind == "" {
			return fmt.Errorf("component %v has unknown componentType", part["primitiveId"])
		}
		if kind != "part" {
			continue
		}
		ref, ok := part["designator"].(string)
		if !ok || ref == "" {
			return fmt.Errorf("part %v has no designator", part["primitiveId"])
		}
		if _, exists := parts[ref]; exists {
			return fmt.Errorf("duplicate part designator %s; cannot attribute pins unambiguously", ref)
		}
		parts[ref] = part
	}
	for _, ref := range expected.AbsentParts {
		if _, exists := parts[ref]; exists {
			return fmt.Errorf("part %s must be absent before placement (possible cross-page duplicate)", ref)
		}
	}
	if expected.ExactParts {
		for _, ref := range sortedStateKeys(parts) {
			if _, exists := expected.Parts[ref]; !exists {
				return fmt.Errorf("unexpected part %s", ref)
			}
		}
	}
	for _, ref := range sortedStateKeys(expected.Parts) {
		want := expected.Parts[ref]
		have, exists := parts[ref]
		if !exists {
			return fmt.Errorf("missing part %s", ref)
		}
		if want.PrimitiveID != "" && have["primitiveId"] != want.PrimitiveID {
			return fmt.Errorf("%s primitiveId: got %v, want %s", ref, have["primitiveId"], want.PrimitiveID)
		}
		if expected.DesignatorsOnly {
			continue
		}
		if want.Instance != nil {
			if err := checkSchPreservedInstance(ref, want.Instance, have); err != nil {
				return err
			}
		}
		if want.Device != nil {
			device, err := measuredSchematicDevice(ref, have)
			if err != nil {
				return err
			}
			if *device != *want.Device {
				return fmt.Errorf("%s device identity: got %s/%s, want %s/%s", ref, device.LibraryUUID, device.UUID, want.Device.LibraryUUID, want.Device.UUID)
			}
		}
		for field, value := range map[string]*float64{"x": want.X, "y": want.Y, "rotation": want.Rotation} {
			if err := compareStateCoordinate(ref, field, have, value); err != nil {
				return err
			}
		}
		if want.Mirror != nil && have["mirror"] != *want.Mirror {
			return fmt.Errorf("%s mirror: got %v, want %v", ref, have["mirror"], *want.Mirror)
		}
		if want.BBox != nil {
			box, ok := have["bbox"].(map[string]any)
			if !ok {
				return fmt.Errorf("%s.bbox must be available measured geometry", ref)
			}
			for field, value := range map[string]*float64{"minX": &want.BBox.MinX, "minY": &want.BBox.MinY, "maxX": &want.BBox.MaxX, "maxY": &want.BBox.MaxY} {
				if err := compareStateCoordinate(ref+".bbox", field, box, value); err != nil {
					return err
				}
			}
		}
		if available, exists := have["pinsAvailable"]; exists && available != true {
			return fmt.Errorf("%s pins unavailable", ref)
		}
		pinList, ok := have["pins"].([]any)
		if !ok {
			return fmt.Errorf("%s pins must be an available array", ref)
		}
		pins := map[string]map[string]any{}
		for _, entry := range pinList {
			pin, ok := entry.(map[string]any)
			if !ok {
				return fmt.Errorf("%s has a malformed pin", ref)
			}
			number, ok := pin["pinNumber"].(string)
			if !ok || number == "" {
				return fmt.Errorf("%s has an unknown pin number", ref)
			}
			if _, exists := pins[number]; exists {
				return fmt.Errorf("%s has duplicate pin number %s", ref, number)
			}
			if _, exists := want.Pins[number]; !exists {
				return fmt.Errorf("%s has unexpected pin %s", ref, number)
			}
			pins[number] = pin
		}
		for _, number := range sortedStateKeys(want.Pins) {
			wantPin := want.Pins[number]
			pin, exists := pins[number]
			if !exists {
				return fmt.Errorf("%s missing pin %s", ref, number)
			}
			if wantPin.Name != nil && pin["pinName"] != *wantPin.Name {
				return fmt.Errorf("%s.%s pinName changed or unavailable", ref, number)
			}
			if wantPin.Persistent != nil {
				if err := checkSchPreservedPin(ref, number, wantPin.Persistent, pin); err != nil {
					return err
				}
			}
			for field, value := range map[string]*float64{"x": wantPin.X, "y": wantPin.Y} {
				if err := compareStateCoordinate(ref+"."+number, field, pin, value); err != nil {
					return err
				}
			}
			if wantPin.NC != nil {
				nc, ok := pin["noConnected"].(bool)
				if !ok || nc != *wantPin.NC {
					return fmt.Errorf("%s.%s noConnected: got %v, want %v", ref, number, pin["noConnected"], *wantPin.NC)
				}
			}
			if wantPin.Net != nil {
				net, known := pin["net"].(string)
				if have["netAmbiguous"] == true || !known {
					return fmt.Errorf("%s.%s net is unknown or ambiguous", ref, number)
				}
				if net != *wantPin.Net {
					return fmt.Errorf("%s.%s net: got %q, want %q", ref, number, net, *wantPin.Net)
				}
			}
		}
	}
	if expected.Drawing != nil {
		if err := expected.Drawing.check(result); err != nil {
			return err
		}
	}
	if expected.Ownership != nil {
		return expected.Ownership.check(result)
	}
	return nil
}

func compareStateCoordinate(ref, field string, have map[string]any, want *float64) error {
	if want == nil {
		return nil
	}
	actual, ok := toFloat(have[field])
	if !ok || !finiteStateNumber(actual) || math.Abs(actual-*want) > 1e-6 {
		return fmt.Errorf("%s.%s: got %v, want %g (tolerance 1e-6)", ref, field, have[field], *want)
	}
	return nil
}

func finiteStateNumber(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }

func sortedStateKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
