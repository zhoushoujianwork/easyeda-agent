package schguard

import (
	"encoding/json"
	"math"
	"testing"
)

func crossingTestWire(id string, s [4]float64) map[string]any {
	return map[string]any{"primitiveId": id, "net": "", "rawEncoding": "flat-segments", "rawLine": []any{s[0], s[1], s[2], s[3]}, "segmentIndex": 0,
		"x0": s[0], "y0": s[1], "x1": s[2], "y1": s[3]}
}
func crossingTestSnapshot() map[string]any {
	part := func(id, net string, x, y float64) map[string]any {
		return map[string]any{"primitiveId": id, "designator": id, "componentType": "part", "pinsAvailable": true, "netlistAvailable": true,
			"bbox": map[string]any{"minX": x - 2, "maxX": x + 2, "minY": y - 2, "maxY": y + 2},
			"pins": []any{map[string]any{"pinNumber": "1", "x": x, "y": y, "net": net}}}
	}
	return map[string]any{"wiresAvailable": true, "pinNetsAvailable": true, "count": 2,
		"components":          []any{part("A", "GND", -10, 0), part("B", "3V3", 0, -10)},
		"connectivitySummary": map[string]any{"scope": "activePage", "wires": 2, "netflags": 0, "netports": 0, "netlabels": 0, "buses": 0, "shortSymbols": 0},
		"wires":               []any{crossingTestWire("h", [4]float64{-10, 0, 10, 0}), crossingTestWire("v", [4]float64{0, -10, 0, 10})}}
}
func crossingTestAllows(r map[string]any) bool {
	p, _ := VerifiedWireCrossings(r)
	w := r["wires"].([]any)
	a, _ := wirePoints(w[0].(map[string]any))
	b, _ := wirePoints(w[1].(map[string]any))
	return len(a) == 2 && len(b) == 2 && p.Allows("h", a[0], a[1], "v", b[0], b[1])
}
func TestVerifiedWireCrossingsEvidence(t *testing.T) {
	part := func(r map[string]any) map[string]any { return r["components"].([]any)[0].(map[string]any) }
	pin := func(r map[string]any) map[string]any { return part(r)["pins"].([]any)[0].(map[string]any) }
	wire := func(r map[string]any) map[string]any { return r["wires"].([]any)[0].(map[string]any) }
	addMarker := func(r map[string]any, x, y float64, net string) {
		r["components"] = append(r["components"].([]any), map[string]any{"primitiveId": "m", "componentType": "netflag", "x": x, "y": y, "net": net,
			"bbox": map[string]any{"minX": x - 1, "maxX": x + 1, "minY": y - 1, "maxY": y + 1}})
		r["count"] = 3
		r["connectivitySummary"].(map[string]any)["netflags"] = 1
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   bool
	}{
		{"bare X different proved nets", func(map[string]any) {}, true},
		{"bare X same proved nets", func(r map[string]any) { pin(r)["net"] = "3V3" }, true},
		{"consistent marker on island", func(r map[string]any) { addMarker(r, 10, 0, "GND") }, true},
		{"wire names alone", func(r map[string]any) { pin(r)["net"] = ""; wire(r)["net"] = "GND" }, false},
		{"unknown netlist", func(r map[string]any) { delete(r, "pinNetsAvailable") }, false},
		{"failed netlist", func(r map[string]any) { r["pinNetsAvailable"] = false }, false},
		{"per part netlist absent", func(r map[string]any) { delete(part(r), "netlistAvailable") }, false},
		{"unread pins", func(r map[string]any) { part(r)["pinsAvailable"] = false }, false},
		{"missing pin coordinate", func(r map[string]any) { delete(pin(r), "x") }, false},
		{"nonfinite pin", func(r map[string]any) { pin(r)["x"] = math.NaN() }, false},
		{"no pin witness", func(r map[string]any) { pin(r)["x"] = -20.0 }, false},
		{"unknown pin net", func(r map[string]any) { pin(r)["net"] = nil }, false},
		{"conflicting raw net", func(r map[string]any) { wire(r)["net"] = "OTHER" }, false},
		{"conflicting marker", func(r map[string]any) { addMarker(r, 10, 0, "OTHER") }, false},
		{"empty marker net", func(r map[string]any) { addMarker(r, 10, 0, "") }, false},
		{"marker at X", func(r map[string]any) { addMarker(r, 0, 0, "GND") }, false},
		{"pin at X", func(r map[string]any) { pin(r)["x"] = 0.0 }, false},
		{"third endpoint at X", func(r map[string]any) {
			r["wires"] = append(r["wires"].([]any), crossingTestWire("third", [4]float64{0, 0, 3, 0}))
			r["connectivitySummary"].(map[string]any)["wires"] = 3
		}, false},
		{"T endpoint", func(r map[string]any) { r["wires"].([]any)[1] = crossingTestWire("v", [4]float64{0, -10, 0, 0}) }, false},
		{"shared endpoint", func(r map[string]any) { r["wires"].([]any)[1] = crossingTestWire("v", [4]float64{10, -10, 10, 0}) }, false},
		{"collinear overlap", func(r map[string]any) { r["wires"].([]any)[1] = crossingTestWire("v", [4]float64{-5, 0, 15, 0}) }, false},
		{"diagonal", func(r map[string]any) { r["wires"].([]any)[1] = crossingTestWire("v", [4]float64{-5, -5, 5, 5}) }, false},
		{"zero segment", func(r map[string]any) { r["wires"].([]any)[1] = crossingTestWire("v", [4]float64{0, 0, 0, 0}) }, false},
		{"no raw geometry", func(r map[string]any) { delete(wire(r), "rawLine") }, false},
		{"unknown encoding", func(r map[string]any) { wire(r)["rawEncoding"] = "polyline" }, false},
		{"raw disagreement", func(r map[string]any) { wire(r)["rawLine"].([]any)[0] = -11.0 }, false},
		{"hidden raw endpoint", func(r map[string]any) { wire(r)["rawLine"] = []any{-10.0, 0.0, 0.0, 0.0, 0.0, 0.0, 10.0, 0.0} }, false},
		{"missing inventory", func(r map[string]any) { delete(r, "connectivitySummary") }, false},
		{"truncated components", func(r map[string]any) { r["count"] = 3 }, false},
		{"truncated wire inventory", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["wires"] = 3 }, false},
		{"truncated marker inventory", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["netflags"] = 1 }, false},
		{"unsupported bus", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["buses"] = 1 }, false},
		{"multiple pages", func(r map[string]any) { r["allPages"] = true }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := crossingTestSnapshot()
			tc.mutate(r)
			if got := crossingTestAllows(r); got != tc.want {
				t.Fatalf("allowed=%v want %v", got, tc.want)
			}
		})
	}
}

func TestVerifiedWireCrossingsTransformAndRawRecordOrder(t *testing.T) {
	for _, transform := range []func(float64, float64) (float64, float64){
		func(x, y float64) (float64, float64) { return x + 260, y + 430 },
		func(x, y float64) (float64, float64) { return -y, x },
		func(x, y float64) (float64, float64) { return -x, -y },
	} {
		r := crossingTestSnapshot()
		for _, v := range r["components"].([]any) {
			c := v.(map[string]any)
			p := c["pins"].([]any)[0].(map[string]any)
			p["x"], p["y"] = transform(p["x"].(float64), p["y"].(float64))
		}
		for _, v := range r["wires"].([]any) {
			w := v.(map[string]any)
			a, _ := wirePoints(w)
			x, y := transform(a[0].X, a[0].Y)
			u, v := transform(a[1].X, a[1].Y)
			w = crossingTestWire(w["primitiveId"].(string), [4]float64{u, v, x, y})
			if w["primitiveId"] == "h" {
				r["wires"].([]any)[0] = w
			} else {
				r["wires"].([]any)[1] = w
			}
		}
		// Exercise the actual JSON shape, independent of Go map insertion order.
		b, _ := json.Marshal(r)
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		if !crossingTestAllows(r) {
			t.Fatal("translation/rotation/endpoint reversal changed bare X proof")
		}
	}
}

func TestVerifiedWireCrossingsPreservesOriginalFlatEndpoints(t *testing.T) {
	r := crossingTestSnapshot()
	left := crossingTestWire("h", [4]float64{-10, 0, 0, 0})
	right := crossingTestWire("h", [4]float64{0, 0, 10, 0})
	line := []any{-10.0, 0.0, 0.0, 0.0, 0.0, 0.0, 10.0, 0.0}
	left["rawLine"], right["rawLine"] = line, line
	right["segmentIndex"] = 1
	vertical := r["wires"].([]any)[1]
	// Reverse inventory order: original segment indices, not list position,
	// establish the observed end at (0,0). Do not simplify these into a bare X.
	r["wires"] = []any{right, vertical, left}
	p, err := VerifiedWireCrossings(r)
	if err != nil || len(p.pairs) != 0 {
		t.Fatalf("observed split X is a contact, not an exemption: %+v %v", p, err)
	}
	r["wires"] = []any{right, vertical}
	if _, err := VerifiedWireCrossings(r); err == nil {
		t.Fatal("omitting a raw segment must invalidate completeness")
	}
	r["wires"] = []any{right, vertical, left, left}
	if _, err := VerifiedWireCrossings(r); err == nil {
		t.Fatal("duplicate segment must not stand in for complete original geometry")
	}
}
