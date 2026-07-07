package app

import (
	"encoding/json"
	"testing"
)

// Real leaves captured from ~/.easyeda-agent/audit (esp32MiniRequire probe
// rounds) — the panel's actual nested shape, one leaf per error class.
const drcResultFixture = `{
  "passed": false,
  "binding": {"boardName": "Board1", "hint": "Netlist Error is often a stale Board binding"},
  "violations": [
    {
      "count": 2, "name": "Connection Error", "title": ["Connection Error", "(2)"],
      "list": [
        {
          "count": 2, "name": "+3V3", "title": ["+3V3", "(2)"],
          "list": [
            {
              "errorObjType": "SMD Pad", "errorType": "Connection Error",
              "explanation": {
                "errData": {"errorType": "No Connection", "globalIndex": "err1", "name": "Common", "net": "+3V3", "obj1": "de130fef39e7b5efe15", "obj1Suffix": "(+3V3): C2_2", "obj1Type": "SMD Pad"},
                "param": {"obj1": "Object 1", "type": "ConnectError"},
                "str": "{obj1} is disconnected from other objects of the same network"
              },
              "globalIndex": "err1", "isFree": false, "net": "+3V3",
              "obj1": {"suffix": "(+3V3): C2_2", "typeName": "SMD Pad"},
              "objs": ["de130fef39e7b5efe15"],
              "parentId": "DRCTab|_|Errors|_|Connection Error|_|+3V3",
              "pos": {"x": 31.654, "y": -95},
              "ruleName": "Common", "visible": true
            },
            {
              "errorObjType": "Track", "errorType": "Connection Error",
              "explanation": {
                "errData": {"errorType": "No Connection", "globalIndex": "err2", "name": "Common", "net": "+3V3"},
                "param": {"obj1": "Object 1", "type": "ConnectError"},
                "str": "{obj1} is disconnected from other objects of the same network"
              },
              "globalIndex": "err2", "net": "+3V3",
              "obj1": {"suffix": "(+3V3): e12", "typeName": "Track"},
              "objs": ["aa0"], "pos": {"x": 10, "y": 20},
              "ruleName": "Common", "visible": true
            }
          ]
        }
      ]
    },
    {
      "count": 1, "name": "Clearance Error", "title": ["Clearance Error", "(1)"],
      "list": [
        {
          "count": 1, "name": "Track to Track",
          "list": [
            {
              "errorObjType": "Track to Track", "errorType": "Clearance Error",
              "explanation": {
                "errData": {"clearance": 0.40157, "globalIndex": "err28", "layerIds": [1], "minDistance": 0, "name": "copperThickness1oz", "obj1": "666de996beeb75f4", "obj1Suffix": "(+3V3): e48", "obj2": "1e9fb39bd07a08bc", "obj2Suffix": "(GND): e66", "position": {"x": 120.759, "y": -120.4719}},
                "param": {"minDistance": "0mil", "obj1": "Object 1", "obj2": "object 2", "shouldBe": ">= 4mil", "type": "ClearanceError"},
                "str": "{obj1} to {obj2} distance is {minDistance}, should be {shouldBe}"
              },
              "globalIndex": "err28", "layer": "Top Layer",
              "obj1": {"suffix": "(+3V3): e48", "typeName": "Track"},
              "obj2": {"suffix": "(GND): e66", "typeName": "Track"},
              "objs": ["666de996beeb75f4", "1e9fb39bd07a08bc"],
              "pos": {"x": 120.759, "y": -120.4719},
              "ruleName": "copperThickness1oz", "ruleTypeName": "Safe Spacing", "visible": true
            }
          ]
        }
      ]
    },
    {
      "count": 1, "name": "Netlist Error",
      "list": [
        {
          "count": 1, "name": "Netlist Error",
          "list": [
            {
              "errorObjType": "Netlist Error", "errorType": "Netlist Error",
              "explanation": {"param": {}, "str": "PCB and schematic netlist does not match."},
              "globalIndex": "err1",
              "obj1": {"suffix": "", "typeName": "Schematic Netlist"},
              "obj2": {"suffix": "", "typeName": "PCB Netlist"},
              "objs": ["err1"],
              "parentId": "DRCTab|_|Errors|_|Netlist Error|_|Netlist Error",
              "ruleName": "Import Changes", "ruleTypeName": "Import Changes", "visible": true
            }
          ]
        }
      ]
    }
  ]
}`

func loadDrcFixture(t *testing.T) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(drcResultFixture), &result); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return result
}

func TestFlattenDrcResult(t *testing.T) {
	report := flattenDrcResult(loadDrcFixture(t))

	if report.Passed {
		t.Fatal("passed should be false")
	}
	if report.Total != 4 {
		t.Fatalf("total = %d, want 4", report.Total)
	}
	wantCounts := map[string]int{"Connection Error": 2, "Clearance Error": 1, "Netlist Error": 1}
	for rule, n := range wantCounts {
		if report.Counts[rule] != n {
			t.Errorf("counts[%s] = %d, want %d", rule, report.Counts[rule], n)
		}
	}
	if report.Binding == nil || report.Binding["boardName"] != "Board1" {
		t.Errorf("binding not passed through: %v", report.Binding)
	}
}

func TestFlattenDrcLeafFields(t *testing.T) {
	report := flattenDrcResult(loadDrcFixture(t))

	// Sorted: Clearance < Connection < Netlist.
	clearance := report.Violations[0]
	if clearance.Rule != "Clearance Error" || clearance.ObjType != "Track to Track" {
		t.Fatalf("unexpected first row: %+v", clearance)
	}
	// mil/10 → mil (A5): 120.759 → 1207.59.
	if clearance.X == nil || *clearance.X != 1207.59 {
		t.Errorf("x = %v, want 1207.59", clearance.X)
	}
	if clearance.Y == nil || *clearance.Y != -1204.72 {
		t.Errorf("y = %v, want -1204.72 (rounded)", clearance.Y)
	}
	// Net recovered from the obj suffix "(+3V3): e48" (no leaf.net on clearance).
	if clearance.Net != "+3V3" {
		t.Errorf("net = %q, want +3V3 (from obj1 suffix)", clearance.Net)
	}
	if clearance.Layer != "Top Layer" || clearance.RuleName != "copperThickness1oz" {
		t.Errorf("layer/ruleName wrong: %+v", clearance)
	}
	if len(clearance.Objs) != 2 || clearance.Objs[0] != "666de996beeb75f4" {
		t.Errorf("objs = %v", clearance.Objs)
	}
	// Message: {obj1}/{obj2} bound to real suffixes, the rest from param.
	wantMsg := "(+3V3): e48 to (GND): e66 distance is 0mil, should be >= 4mil"
	if clearance.Message != wantMsg {
		t.Errorf("message = %q\n want %q", clearance.Message, wantMsg)
	}

	connection := report.Violations[1]
	if connection.Rule != "Connection Error" || connection.Net != "+3V3" {
		t.Fatalf("unexpected second row: %+v", connection)
	}
	if connection.X == nil || *connection.X != 316.54 || *connection.Y != -950 {
		t.Errorf("connection pos = %v,%v want 316.54,-950", connection.X, connection.Y)
	}
	if connection.Message != "(+3V3): C2_2 is disconnected from other objects of the same network" {
		t.Errorf("connection message = %q", connection.Message)
	}

	netlist := report.Violations[3]
	if netlist.Rule != "Netlist Error" {
		t.Fatalf("unexpected last row: %+v", netlist)
	}
	if netlist.X != nil || netlist.Y != nil {
		t.Errorf("netlist error should carry no coordinates: %+v", netlist)
	}
	if netlist.Net != "" {
		t.Errorf("netlist error net should be empty, got %q", netlist.Net)
	}
}

func TestFlattenDrcResultEmpty(t *testing.T) {
	report := flattenDrcResult(map[string]any{"passed": true, "violations": []any{}})
	if !report.Passed || report.Total != 0 {
		t.Fatalf("clean board: %+v", report)
	}
	// Violations must marshal as [] (not null) for downstream jq pipelines.
	out, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) == "" || !json.Valid(out) {
		t.Fatal("invalid JSON")
	}
	var round map[string]any
	_ = json.Unmarshal(out, &round)
	if _, ok := round["violations"].([]any); !ok {
		t.Fatalf("violations should be an array, got %T", round["violations"])
	}
}
