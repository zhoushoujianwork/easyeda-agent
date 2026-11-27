package connectivity

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func multiUnitReadFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/multiunit-subpart-read.json")
	if err != nil {
		t.Fatal(err)
	}
	var read map[string]any
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	return read
}

// A multi-unit device is placed as one record per unit symbol. The records
// share the native uniqueId, repeat the reference designator and end up as a
// single footprint, so they are one canonical component with the union of
// the unit pins.
func TestFromReadMergesMultiUnitDeviceSubParts(t *testing.T) {
	read := multiUnitReadFixture(t)
	doc, err := FromRead(read)
	if err != nil {
		t.Fatalf("multi-unit device rejected: %v", err)
	}
	if len(doc.Components) != 1 {
		t.Fatalf("expected one canonical component, got %d", len(doc.Components))
	}
	c := doc.Components[0]
	if c.ID != "cmp-U18" || c.Ref != "U18" || c.UniqueID != "gge1_3" {
		t.Fatalf("device identity drifted: %+v", c)
	}
	if len(c.Pins) != 9 {
		t.Fatalf("expected the union of the unit pins, got %d", len(c.Pins))
	}
	if len(c.SubParts) != 3 {
		t.Fatalf("expected three placed units, got %d", len(c.SubParts))
	}
	units := map[string]bool{}
	for _, sub := range c.SubParts {
		if sub.PrimitiveID == "" || sub.SubPartName == "" || sub.Placement == nil {
			t.Fatalf("placed unit evidence lost: %+v", sub)
		}
		units[sub.SubPartName] = true
	}
	if len(units) != 3 {
		t.Fatalf("expected three distinct unit names, got %v", units)
	}
	if len(doc.Connections) != 9 {
		t.Fatalf("expected one connection per pin, got %d", len(doc.Connections))
	}
	for _, conn := range doc.Connections {
		if conn.ComponentID != "cmp-U18" {
			t.Fatalf("connection left the canonical component: %+v", conn)
		}
	}
	if len(doc.Nets) != 7 {
		t.Fatalf("expected the seven observed nets, got %d", len(doc.Nets))
	}
}

// Two different devices wearing one designator stay a duplicate. The merge
// must not turn a real conflict into a silently merged component.
func TestFromReadKeepsGenuineDuplicateDesignatorsRejected(t *testing.T) {
	read := multiUnitReadFixture(t)
	components := read["components"].([]any)
	clone := map[string]any{}
	raw, _ := json.Marshal(components[1])
	_ = json.Unmarshal(raw, &clone)
	clone["primitiveId"] = "another-device"
	clone["uniqueId"] = "gge9_1"
	clone["subPartName"] = "OTHER_C5116335.1"
	read["components"] = append(components, clone)
	if _, err := FromRead(read); err == nil || !strings.Contains(err.Error(), "duplicate component") {
		t.Fatalf("two devices with one designator were accepted: %v", err)
	}
}

// One device identity must name one device: a shared canonical binding under
// two designators is a duplicate binding, not a multi-unit device.
func TestFromReadRejectsOneBindingUnderTwoDesignators(t *testing.T) {
	read := multiUnitReadFixture(t)
	components := read["components"].([]any)
	clone := map[string]any{}
	raw, _ := json.Marshal(components[1])
	_ = json.Unmarshal(raw, &clone)
	clone["primitiveId"] = "another-device"
	clone["uniqueId"] = "gge9_1"
	clone["designator"] = "U19"
	clone["otherProperty"] = map[string]any{ComponentIDProperty: "cmp-U18"}
	read["components"] = append(components, clone)
	if _, err := FromRead(read); err == nil || !strings.Contains(err.Error(), "duplicate component") {
		t.Fatalf("one binding under two designators was accepted: %v", err)
	}
}

// The bound canonical ID wins over the designator fallback, and every unit of
// the device keeps that binding.
func TestFromReadKeepsBoundCanonicalIDForMultiUnitDevice(t *testing.T) {
	read := multiUnitReadFixture(t)
	for _, item := range read["components"].([]any) {
		part := item.(map[string]any)
		part["otherProperty"] = map[string]any{ComponentIDProperty: "cmp-switch-1"}
	}
	doc, err := FromRead(read)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Components) != 1 || doc.Components[0].ID != "cmp-switch-1" {
		t.Fatalf("bound identity lost: %+v", doc.Components)
	}
}

// A record without any instance identity stays unique on its own, so two
// unbound parts wearing one designator are still reported.
func TestFromReadKeepsUnboundDuplicateDesignatorsRejected(t *testing.T) {
	read := multiUnitReadFixture(t)
	components := read["components"].([]any)
	clone := map[string]any{}
	raw, _ := json.Marshal(components[1])
	_ = json.Unmarshal(raw, &clone)
	clone["primitiveId"] = "another-device"
	clone["uniqueId"] = ""
	clone["subPartName"] = "OTHER_C5116335.1"
	read["components"] = append(components, clone)
	if _, err := FromRead(read); err == nil || !strings.Contains(err.Error(), "duplicate component") {
		t.Fatalf("two unbound parts with one designator were accepted: %v", err)
	}
}

func TestGroupPartInstancesMergesThroughSharedUniqueID(t *testing.T) {
	bound := map[string]any{
		"componentType": "part", "designator": "U1", "primitiveId": "p1", "uniqueId": "gge1",
		"otherProperty": map[string]any{ComponentIDProperty: "cmp-U1"},
	}
	unbound := map[string]any{"componentType": "part", "designator": "U1", "primitiveId": "p2", "uniqueId": "gge1"}
	other := map[string]any{"componentType": "part", "designator": "U2", "primitiveId": "p3", "uniqueId": "gge2"}
	groups := GroupPartInstances([]map[string]any{bound, unbound, other})
	if len(groups) != 2 {
		t.Fatalf("expected two device instances, got %d", len(groups))
	}
	if len(groups[0]) != 2 || groups[0][0]["primitiveId"] != "p1" || groups[0][1]["primitiveId"] != "p2" {
		t.Fatalf("bound and unbound units of one device were not merged: %+v", groups[0])
	}
	if len(groups[1]) != 1 || groups[1][0]["primitiveId"] != "p3" {
		t.Fatalf("separate device was merged: %+v", groups[1])
	}
}
