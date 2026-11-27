package connectivity

import (
	"strings"
	"testing"
)

func multiUnitRecord(subPart string, pins ...[2]string) map[string]any {
	list := []any{}
	for _, p := range pins {
		list = append(list, map[string]any{"number": p[0], "net": p[1], "noConnected": false})
	}
	return map[string]any{
		"componentType": "part", "designator": "U18", "primitiveId": "p-" + subPart, "uniqueId": "gge1_3", "subPartName": subPart,
		"x": 100.0, "y": 200.0, "device": map[string]any{"libraryUuid": strings.Repeat("a", 32), "uuid": strings.Repeat("b", 32)}, "pins": list,
	}
}

func TestFromReadFoldsSamePageMultiUnitDevice(t *testing.T) {
	d, err := FromRead(map[string]any{"components": []any{
		multiUnitRecord("BRIDGE.3", [2]string{"C1", "GND"}),
		multiUnitRecord("BRIDGE.1", [2]string{"A1", "PCIE_TXP"}),
		multiUnitRecord("BRIDGE.2", [2]string{"B1", "PCIE_TXP"}, [2]string{"B2", ""}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Components) != 1 {
		t.Fatalf("want one component, got %+v", d.Components)
	}
	c := d.Components[0]
	if c.ID != "cmp-U18" || c.Placement != nil || len(c.SubParts) != 3 || c.SubParts[0].Name != "BRIDGE.1" || c.SubParts[2].Placement == nil {
		t.Fatalf("units not folded: %+v", c)
	}
	if len(c.Pins) != 4 || c.Pins[0].Number != "A1" || c.Pins[0].SubPart != "BRIDGE.1" || c.Pins[3].SubPart != "BRIDGE.3" {
		t.Fatalf("pin attribution: %+v", c.Pins)
	}
	if len(d.Connections) != 3 || len(d.Nets) != 2 {
		t.Fatalf("connections/nets: %+v %+v", d.Connections, d.Nets)
	}
	if err := ValidatePlacementDesignators(d); err == nil || !strings.Contains(err.Error(), "multi-unit") {
		t.Fatalf("placement must refuse a folded multi-unit device: %v", err)
	}
}

func TestFromReadRejectsUnprovenMultiUnitIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(a, b map[string]any)
		want string
	}{
		{"distinct-uniqueId", func(_, b map[string]any) { b["uniqueId"] = "gge2" }, "different uniqueId"},
		{"missing-uniqueId", func(a, b map[string]any) { delete(a, "uniqueId"); delete(b, "uniqueId") }, "no uniqueId"},
		{"missing-subPartName", func(_, b map[string]any) { b["subPartName"] = "" }, "subPartName"},
		{"same-unit", func(_, b map[string]any) { b["subPartName"] = "BRIDGE.1" }, "more than once"},
		{"other-device", func(_, b map[string]any) { b["device"] = map[string]any{"uuid": strings.Repeat("c", 32)} }, "different devices"},
		{"binding-conflict", func(a, _ map[string]any) {
			a["otherProperty"] = map[string]any{ComponentIDProperty: "bridge"}
		}, "conflicting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := multiUnitRecord("BRIDGE.1", [2]string{"A1", "GND"}), multiUnitRecord("BRIDGE.2", [2]string{"B1", "GND"})
			tc.edit(a, b)
			if _, err := FromRead(map[string]any{"components": []any{a, b}}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFromReadSingleUnitPartUnchanged(t *testing.T) {
	d, err := FromRead(map[string]any{"components": []any{multiUnitRecord("BRIDGE.1", [2]string{"A1", "GND"})}})
	if err != nil {
		t.Fatal(err)
	}
	c := d.Components[0]
	if c.SubParts != nil || c.Placement == nil || c.Pins[0].SubPart != "" {
		t.Fatalf("single-unit part changed shape: %+v", c)
	}
}
