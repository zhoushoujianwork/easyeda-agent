package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// #365: a multi-unit device is three placed records with one designator,
// uniqueId and device. The designator flow must treat it as one component.
func multiUnitDesignatorRaw(t *testing.T, edit func(units []map[string]any)) []byte {
	t.Helper()
	unit := func(pid, subPart string, pins ...[2]string) map[string]any {
		list := []any{}
		for i, p := range pins {
			list = append(list, map[string]any{"pinNumber": p[0], "pinName": "P" + p[0], "x": float64(10 * i), "y": 0.0, "noConnected": false, "net": p[1]})
		}
		return map[string]any{"primitiveId": pid, "componentType": "part", "designator": "U18", "uniqueId": "gge1_3", "subPartName": subPart,
			"x": 100.0, "y": 200.0, "rotation": 0.0, "mirror": false, "device": map[string]any{"libraryUuid": "official", "uuid": strings.Repeat("b", 32)},
			"pinsAvailable": true, "netlistAvailable": true, "otherProperty": map[string]any{}, "pins": list}
	}
	units := []map[string]any{
		unit("u18-c", "BRIDGE.3", [2]string{"C1", "GND"}),
		unit("u18-a", "BRIDGE.1", [2]string{"A1", "TXP"}),
		unit("u18-b", "BRIDGE.2", [2]string{"B1", "TXP"}, [2]string{"B2", "GND"}),
	}
	if edit != nil {
		edit(units)
	}
	components := []any{map[string]any{"primitiveId": "r1", "componentType": "part", "designator": "R1", "uniqueId": "gge9", "x": 0.0, "y": 0.0, "rotation": 0.0, "mirror": false,
		"device": map[string]any{"libraryUuid": "official", "uuid": strings.Repeat("c", 32)}, "pinsAvailable": true, "netlistAvailable": true, "otherProperty": map[string]any{},
		"pins": []any{map[string]any{"pinNumber": "1", "pinName": "1", "x": 0.0, "y": 0.0, "noConnected": false, "net": "GND"}}}}
	for _, u := range units {
		components = append(components, u)
	}
	raw, err := json.Marshal(map[string]any{"ok": true, "context": map[string]any{"projectUuid": "project", "documentUuid": "page1", "documentType": "schematic"},
		"result": map[string]any{"components": components, "wires": []any{},
			"connectivitySummary": map[string]any{"scope": "activePage", "wires": 0, "buses": 0, "netflags": 0, "netports": 0, "netlabels": 0, "shortSymbols": 0}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSchDesignatorsBindsEveryUnitOfMultiUnitDevice(t *testing.T) {
	before, err := parseSchDesignatorBaseline(multiUnitDesignatorRaw(t, nil))
	if err != nil {
		t.Fatalf("multi-unit device must parse as one component: %v", err)
	}
	if before.IDs["u18-a"] != "cmp-U18" || before.IDs["u18-c"] != "cmp-U18" {
		t.Fatalf("units must share the canonical ID: %+v", before.IDs)
	}
	target, err := schDesignatorsBindTarget(before)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := buildSchDesignatorsPlan(before, target, "/b", "/t", "bs", "ts")
	if err != nil {
		t.Fatalf("bind plan: %v", err)
	}
	var bound []string
	for _, step := range pb.Steps {
		if step.Action != "schematic.component.modify" {
			continue
		}
		patch := step.Payload["patch"].(map[string]any)
		attrs := patch["customAttributes"].(map[string]any)
		if pid := step.Payload["primitiveId"].(string); strings.HasPrefix(pid, "u18-") {
			if patch["designator"] != "U18" || attrs[connectivity.ComponentIDProperty] != "cmp-U18" {
				t.Fatalf("unit bound inconsistently: %+v", step.Payload)
			}
			bound = append(bound, pid)
		}
	}
	if strings.Join(bound, ",") != "u18-a,u18-b,u18-c" {
		t.Fatalf("every unit must be bound in sub-part order, got %v", bound)
	}
	// After the queue runs, all units carry the binding and the scene matches.
	after := designatorAfter(t, before, target)
	if err := verifySchDesignatorScene(before, target, after, "after"); err != nil {
		t.Fatalf("after verification: %v", err)
	}
	// The fully bound read still parses: the bindings agree across units.
	raw, _ := json.Marshal(actionResult{OK: true, Context: &after.Context, Result: after.Result})
	if _, err := parseSchDesignatorBaseline(raw); err != nil {
		t.Fatalf("bound page must reparse: %v", err)
	}
	// Full-project inventory counts the device once, not three times.
	global := &actionResult{OK: true, Context: &before.Context, Result: before.Result}
	if err := verifySchDesignatorGlobal(before, target, global, false); err != nil {
		t.Fatalf("global verification: %v", err)
	}
}

func TestSchDesignatorsStillRejectsDistinctPartsSharingAnID(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(units []map[string]any)
	}{
		{"distinct-uniqueId", func(u []map[string]any) { u[1]["uniqueId"] = "gge2" }},
		{"half-bound", func(u []map[string]any) {
			u[0]["otherProperty"] = map[string]any{connectivity.ComponentIDProperty: "cmp-U18"}
		}},
		{"same-binding-other-ref", func(u []map[string]any) {
			u[0]["designator"] = "U19"
			for _, x := range u {
				x["otherProperty"] = map[string]any{connectivity.ComponentIDProperty: "bridge"}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSchDesignatorBaseline(multiUnitDesignatorRaw(t, tc.edit))
			if err == nil || !strings.Contains(err.Error(), "duplicate component binding") {
				t.Fatalf("want duplicate binding refusal, got %v", err)
			}
		})
	}
}

func TestSchDesignatorsRefusesSplittingCrossPageUnits(t *testing.T) {
	before, err := parseSchDesignatorBaseline(multiUnitDesignatorRaw(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	target, err := schDesignatorsBindTarget(before)
	if err != nil {
		t.Fatal(err)
	}
	// One unit lives on another page of the project.
	for i := range target.Components {
		if target.Components[i].ID == "cmp-U18" {
			target.Components[i].SubParts = append(target.Components[i].SubParts, connectivity.SubPart{Name: "BRIDGE.4", PageID: "page2"})
		}
	}
	if _, err := schDesignatorTargets(before, target); err == nil || !strings.Contains(err.Error(), "3 of 4 units") {
		t.Fatalf("want cross-page refusal, got %v", err)
	}
}
