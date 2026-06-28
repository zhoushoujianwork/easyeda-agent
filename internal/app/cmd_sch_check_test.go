package app

import (
	"bytes"
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
	at := &struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}{X: 90, Y: 135}
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
	at := &struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}{X: 545, Y: 295}
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
				Nets:            []string{"EN", "EN"},
				Message:         "导线有多个网络名: EN、EN",
			},
		},
	}
	var buf bytes.Buffer
	renderCheckReport(rep, &buf)
	out := buf.String()
	for _, want := range []string{"net-marker mismatch", "multi-net wire", "marker=+3V3 wire=BOOT_IO0", "nets=[EN,EN]", "net names"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- output ---\n%s", want, out)
		}
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
