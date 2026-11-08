package app

import (
	"reflect"
	"testing"
)

func TestSchWireGeometryAdapterPreservesEvidenceAndBlocks(t *testing.T) {
	result := map[string]any{"components": []any{map[string]any{
		"componentType": "part", "designator": "U1", "primitiveId": "u1",
		"bbox":          map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 20.0, "maxY": 20.0},
		"pinsAvailable": true, "pins": []any{map[string]any{"pinNumber": "1", "x": 30.0, "y": 10.0, "rotation": 0.0}},
	}}, "wires": "original observation must remain intact"}
	fs := schWireGeometryFindings(result, []schGroupWire{{ID: "stable-wire-id", Points: []float64{10, 10, 30, 10}}})
	if len(fs) != 2 {
		t.Fatalf("expected direction and penetration: %+v", fs)
	}
	for _, f := range fs {
		if f.WirePrimitiveId != "stable-wire-id" || f.Designator != "U1" || f.PrimitiveId != "u1" || f.Message == "" {
			t.Fatalf("lost evidence: %+v", f)
		}
		if !checkLevelBlocks(f.Level, false) {
			t.Fatalf("hard errors cannot depend on strict: %+v", f)
		}
	}
	if result["wires"] != "original observation must remain intact" {
		t.Fatal("adapter modified original raw snapshot")
	}
}

func TestSchWireGeometryAdapterDoesNotInventMergedTreeSegments(t *testing.T) {
	result := map[string]any{"components": []any{map[string]any{
		"componentType": "part", "designator": "U1", "primitiveId": "u1",
		"bbox":          map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 20.0, "maxY": 20.0},
		"pinsAvailable": true, "pins": []any{},
	}}}
	// Official merged-tree array has two independent horizontal segments,
	// not the fictitious (30,-10)→(-10,30) diagonal through U1.
	wires := []schGroupWire{{ID: "merged", Points: []float64{-10, -10, 30, -10, -10, 30, 30, 30}}}
	if fs := schWireGeometryFindings(result, wires); len(fs) != 0 {
		t.Fatalf("fabricated inter-segment edge: %+v", fs)
	}
}

// The connector and the Go-side geometry guard now share one severity vocabulary.
// Geometry findings were emitted as uppercase ERROR while connector rules use
// lowercase; callers filtering level=="error" (issue #299's report) missed them.
func TestSchWireGeometryAdapterNormalizesLevelCase(t *testing.T) {
	result := map[string]any{"components": []any{map[string]any{
		"componentType": "part", "designator": "U1", "primitiveId": "u1",
		"bbox":          map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 20.0, "maxY": 20.0},
		"pinsAvailable": true, "pins": []any{map[string]any{"pinNumber": "1", "x": 30.0, "y": 10.0, "rotation": 0.0}},
	}}}
	fs := schWireGeometryFindings(result, []schGroupWire{{ID: "w", Points: []float64{10, 10, 30, 10}}})
	if len(fs) != 2 {
		t.Fatalf("expected two geometry findings: %+v", fs)
	}
	for _, f := range fs {
		if f.Level != "error" {
			t.Fatalf("level %q must be normalized to lowercase error: %+v", f.Level, f)
		}
	}
}

func TestZeroLengthIdentity(t *testing.T) {
	at := &checkPoint{X: 1.5, Y: 2.25}
	for _, tc := range []struct {
		name string
		f    checkFinding
		want string
	}{
		{"wire id", checkFinding{Type: "zero-length-wire", WirePrimitiveId: "w1"}, "id:w1"},
		{"coordinate fallback", checkFinding{Type: "zero-length-wire", At: at}, "at:1.5000,2.2500"},
		{"other rule", checkFinding{Type: "wire-crossing", WirePrimitiveId: "w1"}, ""},
		{"no identity", checkFinding{Type: "zero-length-wire"}, ""},
	} {
		if got := zeroLengthIdentity(tc.f); got != tc.want {
			t.Errorf("%s: identity=%q, want %q", tc.name, got, tc.want)
		}
	}
}

// The connector reports one zero-length-wire WARN and the Go geometry guard adds
// its own ERROR for the same primitive. Only the duplicate is dropped, by primitive
// id or (when the id is missing) by anchor; other rules that share an id stay.
func TestDropDuplicateZeroLengthFindings(t *testing.T) {
	base := []checkFinding{
		{Type: "zero-length-wire", Level: "warn", WirePrimitiveId: "w1"},
		{Type: "zero-length-wire", Level: "warn", At: &checkPoint{X: 5, Y: 6}},
		{Type: "wire-crossing", Level: "error", At: &checkPoint{X: 5, Y: 6}},
	}
	extra := []checkFinding{
		{Type: "zero-length-wire", Level: "error", WirePrimitiveId: "w1"},       // same primitive
		{Type: "zero-length-wire", Level: "error", At: &checkPoint{X: 5, Y: 6}}, // same coordinate
		{Type: "zero-length-wire", Level: "error", WirePrimitiveId: "w2"},       // new primitive
		{Type: "wire-through-body", Level: "error", WirePrimitiveId: "w1"},      // other rule, same id
	}
	got := dropDuplicateZeroLengthFindings(base, extra)
	want := []checkFinding{extra[2], extra[3]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dedupe kept %+v, want %+v", got, want)
	}
	if all := dropDuplicateZeroLengthFindings(nil, extra); !reflect.DeepEqual(all, extra) {
		t.Fatalf("empty existing must keep every geometry finding: %+v", all)
	}
}
