package schguard

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func scene(rotation float64, wires ...any) map[string]any {
	return map[string]any{"components": []any{map[string]any{
		"componentType": "part", "primitiveId": "core", "designator": "U1",
		"rotation": 270.0, "mirror": true, // world pin rotation is already transformed
		"bbox":          map[string]any{"minX": -10.0, "minY": -10.0, "maxX": 10.0, "maxY": 10.0},
		"pinsAvailable": true, "pins": []any{map[string]any{"pinNumber": "1", "x": 20.0, "y": 0.0, "rotation": rotation}},
	}}, "wires": wires}
}
func wire(points ...float64) any { return map[string]any{"primitiveId": "w1", "points": points} }
func count(fs []Finding, kind string) int {
	n := 0
	for _, f := range fs {
		if f.Type == kind {
			n++
		}
	}
	return n
}

func TestWireGeometryOutwardDoglegAndBranch(t *testing.T) {
	for _, wires := range [][]any{
		{wire(20, 0, 30, 0)},
		{wire(40, 20, 30, 20, 30, 0, 20, 0)},        // reversed traversal, legal dogleg
		{wire(20, 0, 30, 0), wire(30, -20, 30, 20)}, // T away from pin
	} {
		if f := AnalyzeWireGeometry(scene(0, wires...)); len(f) != 0 {
			t.Fatalf("legal fanout rejected: %+v", f)
		}
	}
}

func TestWireGeometryNeedsNoLibraryIdentityOrMarkerInventory(t *testing.T) {
	for _, w := range []any{wire(20, 0, 30, 0), wire(20, 0, 15, 0), wire(-20, 0, 20, 0)} {
		full := scene(0, w)
		part := full["components"].([]any)[0].(map[string]any)
		part["device"], part["symbol"], part["footprint"] = "library-device", "library-symbol", "library-footprint"
		full["components"] = append(full["components"].([]any), map[string]any{"componentType": "netflag", "primitiveId": "flag"})
		want := AnalyzeWireGeometry(full)
		lean := map[string]any{"wires": full["wires"], "components": []any{map[string]any{
			"primitiveId": part["primitiveId"], "componentType": "part", "designator": part["designator"],
			"bbox": part["bbox"], "pins": part["pins"], "pinsAvailable": true,
		}}}
		if got := AnalyzeWireGeometry(lean); !reflect.DeepEqual(got, want) {
			t.Fatalf("geometry-only evidence changed findings: got %+v want %+v", got, want)
		}
	}
}

func TestWireGeometryRejectsMalformedRoutingSegments(t *testing.T) {
	for _, tt := range []struct {
		w    any
		kind string
	}{{wire(20, 0, 20, 0), "zero-length-wire"}, {wire(20, 0, 20, 0, 30, 0), "zero-length-wire"}, {wire(30, 30, 40, 40), "non-orthogonal-wire"}} {
		if fs := AnalyzeWireGeometry(scene(0, tt.w)); count(fs, tt.kind) != 1 {
			t.Errorf("missing %s: %+v", tt.kind, fs)
		}
	}
}

func TestWireGeometryRotationTruthAndMirror(t *testing.T) {
	for _, tt := range []struct{ r, x, y float64 }{{0, 30, 0}, {90, 20, 10}, {180, 10, 0}, {270, 20, -10}, {-90, 20, -10}, {360, 30, 0}} {
		// Move the body away; test the measured direction, not component center inference.
		s := scene(tt.r, wire(20, 0, tt.x, tt.y))
		s["components"].([]any)[0].(map[string]any)["bbox"] = map[string]any{"minX": -50.0, "maxX": -40.0, "minY": -50.0, "maxY": -40.0}
		if f := AnalyzeWireGeometry(s); len(f) != 0 {
			t.Errorf("rotation %g: %+v", tt.r, f)
		}
	}
}

func TestWireGeometryRejectsInwardPerpendicularAndPinMidsegment(t *testing.T) {
	for _, w := range []any{wire(20, 0, 15, 0), wire(20, 0, 20, 10), wire(15, 0, 30, 0), wire(20, 0, 30, 10)} {
		fs := AnalyzeWireGeometry(scene(0, w))
		if count(fs, "pin-exit-direction") != 1 {
			t.Errorf("must reject bad first ray: %+v", fs)
		}
	}
}

func TestWireGeometryBodyIncludesUnrelatedAndSameNetWires(t *testing.T) {
	for _, w := range []any{wire(-20, 0, 20, 0), wire(0, -20, 0, 20), wire(-20, -20, 20, 20), wire(0, 0, 5, 5)} {
		if fs := AnalyzeWireGeometry(scene(0, w)); count(fs, "wire-through-body") != 1 {
			t.Errorf("body penetration not caught: %+v", fs)
		}
	}
	for _, w := range []any{wire(-20, 10, 20, 10), wire(10, -20, 10, 20), wire(-20, 20, 20, 20)} {
		if fs := AnalyzeWireGeometry(scene(0, w)); count(fs, "wire-through-body") != 0 {
			t.Errorf("boundary or exterior incorrectly rejected: %+v", fs)
		}
	}
}

func TestWireGeometryMissingEvidenceFailsClosed(t *testing.T) {
	mutations := []func(map[string]any){
		func(s map[string]any) { delete(s, "wires") },
		func(s map[string]any) { s["wiresAvailable"] = false },
		func(s map[string]any) { delete(s, "components") },
		func(s map[string]any) { delete(s["components"].([]any)[0].(map[string]any), "bbox") },
		func(s map[string]any) { s["components"].([]any)[0].(map[string]any)["pinsAvailable"] = false },
		func(s map[string]any) {
			delete(s["components"].([]any)[0].(map[string]any)["pins"].([]any)[0].(map[string]any), "rotation")
		},
		func(s map[string]any) {
			s["components"].([]any)[0].(map[string]any)["pins"].([]any)[0].(map[string]any)["rotation"] = 45.0
		},
		func(s map[string]any) { s["wires"] = []any{wire(math.NaN(), 0, 30, 0)} },
	}
	for i, mutate := range mutations {
		s := scene(0, wire(20, 0, 30, 0))
		mutate(s)
		if fs := AnalyzeWireGeometry(s); count(fs, "wire-geometry-unverified") == 0 {
			t.Errorf("case %d accepted missing evidence: %+v", i, fs)
		}
	}
}

func TestWireGeometryCeshiUSBC1NegativeRegression(t *testing.T) {
	// Verbatim geometric subset of the official 2026-09-14 P1 snapshot:
	// /tmp/easyeda-ceshi-p1-usbc1-geometry-dev2-20260914/list-raw.json.
	// The previous strict check returned zero while both wires crossed USBC1.
	const raw = `{"components":[{"componentType":"part","primitiveId":"0725fa240d4cac17","designator":"USBC1","rotation":180,"mirror":false,"bbox":{"maxX":580.4999999999999,"maxY":1080.5,"minX":529.4999999999999,"minY":949.5000000000001},"pinsAvailable":true,"pins":[{"pinNumber":"A7","x":589.9999999999999,"y":1020.0000000000001,"rotation":0},{"pinNumber":"B7","x":589.9999999999999,"y":1000.0000000000001,"rotation":0}]}],"wires":[{"primitiveId":"a7-wire","x0":535,"y0":1020,"x1":590,"y1":1020},{"primitiveId":"b7-wire","x0":535,"y0":1000,"x1":590,"y1":1000}]}`
	var s map[string]any
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	fs := AnalyzeWireGeometry(s)
	if count(fs, "pin-exit-direction") != 2 || count(fs, "wire-through-body") != 2 {
		t.Fatalf("lost live negative: %+v", fs)
	}
	for _, f := range fs {
		if f.Designator != "USBC1" || f.PrimitiveId == "" || f.WirePrimitiveId == "" {
			t.Fatalf("missing actionable identity: %+v", f)
		}
	}
	s["wires"] = []any{map[string]any{"points": [][2]float64{{590, 1020}, {620, 1020}, {620, 1040}}}, map[string]any{"points": [][2]float64{{590, 1000}, {630, 1000}}}}
	if fs := AnalyzeWireGeometry(s); len(fs) != 0 {
		t.Fatalf("legal source-data repair rejected: %+v", fs)
	}
}

func TestVerifyWirePresentSplitMergeReverseAndGap(t *testing.T) {
	proposed := wire(20, 0, 40, 0, 40, 20).(map[string]any)
	for _, tt := range []struct {
		name  string
		wires []any
		pass  bool
	}{
		{"same", []any{proposed}, true},
		{"split-and-reverse", []any{wire(30, 0, 20, 0), wire(30, 0, 40, 0), wire(40, 20, 40, 10), wire(40, 0, 40, 10)}, true},
		{"merged", []any{wire(0, 0, 50, 0), wire(40, -10, 40, 30)}, true},
		{"missing-all", []any{}, false},
		{"only-first-leg", []any{wire(20, 0, 40, 0)}, false},
		{"tiny-real-gap", []any{wire(20, 0, 30, 0), wire(30.01, 0, 40, 0), wire(40, 0, 40, 20)}, false},
		{"parallel-neighbor", []any{wire(20, 5, 40, 5), wire(40, 0, 40, 20)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyWirePresent(map[string]any{"wires": tt.wires, "wiresAvailable": true}, proposed)
			if (err == nil) != tt.pass {
				t.Fatalf("pass=%v err=%v", tt.pass, err)
			}
		})
	}
}

func TestCeshiHalfStrokeBodyBoundsDoNotRejectRealOutwardPins(t *testing.T) {
	// Official R3 and U2 measurements retain the 0.5raw outline stroke. Pin
	// anchors are 9.5raw outside it; do not shrink every bbox to manufacture room.
	const raw = `{"components":[{"componentType":"part","primitiveId":"r3","designator":"R3","bbox":{"minX":65.5,"maxX":74.5,"minY":914.5,"maxY":935.5},"pinsAvailable":true,"pins":[{"pinNumber":"2","x":70,"y":905,"rotation":270},{"pinNumber":"1","x":70,"y":945,"rotation":90}]},{"componentType":"part","primitiveId":"u2","designator":"U2","bbox":{"minX":624.5,"maxX":695.5,"minY":269.5,"maxY":360.5},"pinsAvailable":true,"pins":[{"pinNumber":"1","x":615,"y":350,"rotation":180},{"pinNumber":"16","x":705,"y":350,"rotation":0}]}],"wires":[{"x0":70,"y0":905,"x1":70,"y1":890},{"x0":70,"y0":945,"x1":70,"y1":960},{"x0":615,"y0":350,"x1":600,"y1":350},{"x0":705,"y0":350,"x1":720,"y1":350}]}`
	var s map[string]any
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if fs := AnalyzeWireGeometry(s); len(fs) != 0 {
		t.Fatalf("real stroke-aware measurements rejected: %+v", fs)
	}
}
