package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The connector shape: floating pins grouped by component (the ESP32 case —
// many unused IOs on U1).
func TestParseAndRenderCheck_Floating(t *testing.T) {
	result := map[string]any{
		"passed": false,
		"summary": map[string]any{
			"floatingPins":           float64(33),
			"componentsWithFloating": float64(1),
			"total":                  float64(1),
		},
		"findings": []any{
			map[string]any{
				"type":       "floating-pin",
				"level":      "warn",
				"designator": "U1",
				"pins":       []any{"4", "5", "15", "36", "37"},
				"count":      float64(5),
				"message":    "5 个引脚悬空(无导线连接,未打 NC 标识)",
			},
		},
	}
	rep, err := parseCheckReport(result)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rep.Passed {
		t.Error("expected passed=false")
	}
	if rep.Summary.FloatingPins != 33 || len(rep.Findings) != 1 {
		t.Errorf("unexpected summary/findings: %+v", rep)
	}

	var buf bytes.Buffer
	renderCheckReport(rep, &buf)
	out := buf.String()
	for _, want := range []string{"WARN", "floating-pin", "U1", "[4,5,15,36,37]", "floating pins"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// Wire-crossing + wire-over-pin: render shows the type, coords, and routing hint.
func TestRenderCheck_Routing(t *testing.T) {
	at := &checkPoint{X: 90, Y: 135}
	rep := checkReport{
		Passed:  false,
		Summary: checkSummary{WireCrossings: 1, WireOverPins: 1, Total: 2},
		Findings: []checkFinding{
			{Type: "wire-crossing", Level: "warn", Count: 1, Message: "两条导线交叉", At: at},
			{Type: "wire-over-pin", Level: "warn", Designator: "U1", Pins: []string{"5"}, Message: "导线穿过该引脚", At: at},
		},
	}
	var buf bytes.Buffer
	renderCheckReport(rep, &buf)
	out := buf.String()
	for _, want := range []string{"wire-crossing", "@(90.00,135.00)", "wire-over-pin", "U1", "routing"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderCheck_NetNames(t *testing.T) {
	at := &checkPoint{X: 545, Y: 295}
	rep := checkReport{
		Passed: false,
		Summary: checkSummary{
			NetMarkerMismatches: 1,
			MultiNetWires:       1,
			Total:               2,
		},
		Findings: []checkFinding{
			{
				Type:            "net-marker-mismatch",
				Level:           "warn",
				WirePrimitiveId: "w1",
				MarkerNet:       "+3V3",
				WireNet:         "BOOT_IO0",
				Message:         "网络标识 +3V3 与所连导线 BOOT_IO0 名称不一致",
				At:              at,
			},
			{
				Type:            "multi-net-wire",
				Level:           "warn",
				WirePrimitiveId: "w2",
				Nets:            []string{"EN", "GND"},
				Message:         "导线有多个网络名: EN、GND",
			},
		},
	}
	var buf bytes.Buffer
	renderCheckReport(rep, &buf)
	out := buf.String()
	for _, want := range []string{"net-marker mismatch", "multi-net wire", "marker=+3V3 wire=BOOT_IO0", "nets=[EN,GND]", "net names"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// #66: --json output must be wrapped in the {id,type,version,ok,result}
// envelope the transparent sch commands stream, so a uniform-envelope parser
// reading result.findings works here too (previously it emitted a bare
// {passed,summary,findings} and result.findings was silently empty).
func TestEncodeResultEnvelope_CheckReport(t *testing.T) {
	rep := checkReport{
		Passed:  false,
		Summary: checkSummary{FloatingPins: 2, Total: 1},
		Findings: []checkFinding{
			{Type: "floating-pin", Level: "warn", Designator: "U1", Pins: []string{"4", "5"}},
		},
	}
	res := &actionResult{ID: "req-1", Type: "response", Version: "1", OK: true}

	var buf bytes.Buffer
	if err := encodeResultEnvelope(res, rep, &buf); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var env struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Version string `json:"version"`
		OK      bool   `json:"ok"`
		Result  struct {
			Passed   bool           `json:"passed"`
			Findings []checkFinding `json:"findings"`
		} `json:"result"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, buf.String())
	}
	if env.ID != "req-1" || env.Type != "response" || env.Version != "1" || !env.OK {
		t.Errorf("envelope metadata lost: %+v", env)
	}
	// The whole point of #66: result.findings must be reachable and non-empty.
	if len(env.Result.Findings) != 1 || env.Result.Findings[0].Designator != "U1" {
		t.Errorf("result.findings not reachable via envelope: %+v", env.Result)
	}
	if env.Result.Passed {
		t.Error("expected result.passed=false")
	}
}

// Envelope metadata is optional: when the daemon response carries no id/type/
// version, those keys are omitted rather than emitted empty, but ok/result
// stay present.
func TestEncodeResultEnvelope_OmitsEmptyMeta(t *testing.T) {
	rep := checkReport{Passed: true}
	res := &actionResult{OK: true}

	var buf bytes.Buffer
	if err := encodeResultEnvelope(res, rep, &buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "\"id\"") || strings.Contains(out, "\"type\"") || strings.Contains(out, "\"version\"") {
		t.Errorf("expected empty meta omitted, got:\n%s", out)
	}
	for _, want := range []string{"\"ok\"", "\"result\""} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %s present, got:\n%s", want, out)
		}
	}
}

// Every connector-produced rule type must survive parse with its kebab-case
// Type intact, be counted in the per-type summary, and show its type name in
// the rendered report (the same per-rule gate/count convention as pcb check).
func TestCheckFindingTypes_AllRules(t *testing.T) {
	result := map[string]any{
		"passed": false,
		"summary": map[string]any{
			"floatingPins":           float64(2),
			"componentsWithFloating": float64(1),
			"geomNetMismatches":      float64(1),
			"netMarkerMismatches":    float64(1),
			"multiNetWires":          float64(1),
			"wireCrossings":          float64(1),
			"wireOverPins":           float64(1),
			"zeroLengthWires":        float64(1),
			"danglingWires":          float64(1),
			"total":                  float64(8),
		},
		"findings": []any{
			map[string]any{"type": "floating-pin", "level": "warn", "designator": "U1", "pins": []any{"4", "5"}},
			map[string]any{"type": "geom-net-mismatch", "level": "warn", "designator": "U1", "pins": []any{"7"}},
			map[string]any{"type": "net-marker-mismatch", "level": "warn", "wirePrimitiveId": "w1", "markerNet": "+3V3", "wireNet": "EN"},
			map[string]any{"type": "multi-net-wire", "level": "warn", "wirePrimitiveId": "w2", "nets": []any{"EN", "GND"}},
			map[string]any{"type": "wire-crossing", "level": "warn", "count": float64(1)},
			map[string]any{"type": "wire-over-pin", "level": "warn", "designator": "U1", "pins": []any{"5"}},
			map[string]any{"type": "zero-length-wire", "level": "warn", "wirePrimitiveId": "w3"},
			map[string]any{"type": "dangling-wire", "level": "warn", "wirePrimitiveId": "w4"},
		},
	}
	rep, err := parseCheckReport(result)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	wantTypes := []string{
		"floating-pin", "geom-net-mismatch", "net-marker-mismatch", "multi-net-wire",
		"wire-crossing", "wire-over-pin", "zero-length-wire", "dangling-wire",
	}
	if len(rep.Findings) != len(wantTypes) {
		t.Fatalf("expected %d findings, got %d: %+v", len(wantTypes), len(rep.Findings), rep.Findings)
	}
	for i, want := range wantTypes {
		if rep.Findings[i].Type != want {
			t.Errorf("finding %d: Type=%q, want %q", i, rep.Findings[i].Type, want)
		}
	}

	// Per-type summary counts (one field per rule, mirroring pcbCheckSummary).
	s := rep.Summary
	if s.FloatingPins != 2 || s.GeomNetMismatches != 1 || s.NetMarkerMismatches != 1 ||
		s.MultiNetWires != 1 || s.WireCrossings != 1 || s.WireOverPins != 1 ||
		s.ZeroLengthWires != 1 || s.DanglingWires != 1 || s.Total != 8 {
		t.Errorf("per-type summary counts wrong: %+v", s)
	}

	var buf bytes.Buffer
	renderCheckReport(rep, &buf)
	out := buf.String()
	for _, want := range wantTypes {
		if !strings.Contains(out, want) {
			t.Errorf("render missing rule type %q\n--- output ---\n%s", want, out)
		}
	}
	// zero-length/dangling wires carry the stray-wire fix hint.
	if !strings.Contains(out, "stray wires") {
		t.Errorf("render missing stray-wire hint\n--- output ---\n%s", out)
	}
}

// Clean board: no findings → passed, and the "no findings" line.
func TestRenderCheck_Clean(t *testing.T) {
	rep := checkReport{Passed: true, Summary: checkSummary{}}
	var buf bytes.Buffer
	renderCheckReport(rep, &buf)
	if !strings.Contains(buf.String(), "no findings") {
		t.Errorf("expected clean line, got:\n%s", buf.String())
	}
}

// geom-net-mismatch: geometry touches the pin but the JSON-authoritative netlist
// puts it on no net (the "补漏报" direction of the #45 cross-check). Render shows
// the type, designator, pin, coords, and the recompile/fix hint.
func TestParseAndRenderCheck_GeomNetMismatch(t *testing.T) {
	result := map[string]any{
		"passed": false,
		"summary": map[string]any{
			"geomNetMismatches": float64(1),
			"total":             float64(1),
		},
		"findings": []any{
			map[string]any{
				"type":        "geom-net-mismatch",
				"level":       "warn",
				"designator":  "U1",
				"primitiveId": "c1",
				"pins":        []any{"7"},
				"at":          map[string]any{"x": float64(120), "y": float64(-80)},
				"message":     "导线触碰该引脚但网表未将其归入任何 net",
			},
		},
	}
	rep, err := parseCheckReport(result)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rep.Summary.GeomNetMismatches != 1 || len(rep.Findings) != 1 {
		t.Errorf("unexpected summary/findings: %+v", rep)
	}

	var buf bytes.Buffer
	renderCheckReport(rep, &buf)
	out := buf.String()
	for _, want := range []string{"WARN", "geom-net-mismatch", "U1", "[7]", "@(120.00,-80.00)", "geom-net mismatch"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- output ---\n%s", want, out)
		}
	}
}
