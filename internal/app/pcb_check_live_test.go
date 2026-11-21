package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The foreign-net pad is near the track, but outside its centerline/body. This
// isolates the live rule-based clearance finding from the pure track-over-pad
// rule and from WARN-level findings that previously masked the exit-code bug.
func newClearanceCheckFixture(t *testing.T, foreignPadY float64, mutate ...func(map[string]map[string]any)) *appConfig {
	t.Helper()
	pad := func(id, number, net string, x, y float64) map[string]any {
		return map[string]any{"primitiveId": id, "padNumber": number, "net": net, "layer": 1, "x": x, "y": y, "shape": []any{"ELLIPSE", 10, 10}, "rotation": 0, "width": 10, "height": 10}
	}
	components := []any{
		map[string]any{"primitiveId": "r1", "designator": "R1", "name": "resistor", "layer": 1, "x": 50, "y": 0, "rotation": 0, "pads": []any{pad("p1", "1", "N1", 0, 0), pad("p2", "2", "N1", 100, 0)}},
		map[string]any{"primitiveId": "r2", "designator": "R2", "name": "resistor", "layer": 1, "x": 50, "y": foreignPadY, "rotation": 0, "pads": []any{pad("p3", "1", "OTHER", 50, foreignPadY)}},
	}
	results := map[string]map[string]any{
		"pcb.components.list": {"components": components},
		"pcb.line.list":       {"lines": []any{map[string]any{"primitiveId": "track", "net": "N1", "layer": 1, "startX": 0, "startY": 0, "endX": 100, "endY": 0, "lineWidth": 8}}, "arcs": []any{}, "arcsAvailable": true},
		"pcb.via.list":        {"vias": []any{}},
		"pcb.silk.list":       {"texts": []any{}},
		"pcb.constraint.list": {"differentialPairs": []any{}},
		"pcb.drc.rules":       ceshiRuleResult(),
		"pcb.fill.list":       {"fills": []any{}},
		"pcb.outline.get":     {"bbox": map[string]any{"minX": -1000, "minY": -1000, "maxX": 1000, "maxY": 1000}},
		"pcb.layers.list":     {"copperLayerCount": 2, "layers": []any{}},
		"pcb.region.list":     {"regions": []any{}},
		"pcb.pour.list":       {"pours": []any{}},
		// No authored zone claims are involved in this copper-only fixture.
		"project.current": {},
	}
	for _, change := range mutate {
		change(results)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"w1"}]}`))
			return
		}
		var request struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		result, exists := results[request.Action]
		if !exists {
			t.Errorf("unexpected action %s", request.Action)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	return &appConfig{host: host, ports: port + "-" + port}
}

func TestPcbCheckLiveClearanceStrictExit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		foreignPadY float64
		strict      bool
		wantError   bool
		wantCount   int
	}{
		{"clearance error blocks strict", 10, true, true, 1},
		{"clearance error remains informational", 10, false, false, 1},
		{"safe spacing passes strict", 20, true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newClearanceCheckFixture(t, tc.foreignPadY)
			var stdout, stderr bytes.Buffer
			err := runPcbCheck(cfg, "w1", 3, nil, tc.strict, true, &stdout, &stderr)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v stderr=%s", err, stderr.String())
			}
			var report pcbCheckReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("report decode=%v stdout=%s", err, stdout.String())
			}
			if report.Summary.Errors != tc.wantCount || report.Summary.Clearance != tc.wantCount || report.Summary.Warnings != tc.wantCount || report.Summary.Total != tc.wantCount {
				t.Fatalf("summary=%+v findings=%+v", report.Summary, report.Findings)
			}
			if report.Passed != (tc.wantCount == 0) {
				t.Fatalf("passed=%t summary=%+v", report.Passed, report.Summary)
			}
			for _, finding := range report.Findings {
				if finding.Type != "clearance" || finding.Level != "ERROR" {
					t.Fatalf("fixture did not isolate live clearance: %+v", finding)
				}
			}
		})
	}
}

func TestPcbCheckCoverageStrictExit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		result   map[string]any
		complete bool
	}{
		{"known empty", map[string]any{"components": []any{}}, true},
		{"missing component inventory", map[string]any{}, false},
		{"missing pad inventory", map[string]any{"components": []any{map[string]any{"primitiveId": "c1", "designator": "J1"}}}, false},
		{"explicit failed inventory with retained pad", map[string]any{"components": []any{map[string]any{"primitiveId": "c1", "designator": "J1", "pads": []any{}, "availability": map[string]any{"pads": "unavailable"}, "captureErrors": map[string]any{"pads": map[string]any{"code": "getter-threw", "message": "injected inventory failure"}}}}}, false},
		{"unsupported source shape", map[string]any{"components": []any{map[string]any{"primitiveId": "c1", "designator": "J1", "pads": []any{map[string]any{"primitiveId": "p1", "padNumber": "1", "net": "N1", "layer": 1, "x": 0, "y": 0, "rotation": 0, "shape": []any{"POLYGON", []any{0, 0, 10, 0, 10, 10}}, "specialPad": nil}}}}}, false},
		{"legacy extent only", map[string]any{"components": []any{map[string]any{"primitiveId": "c1", "designator": "J1", "pads": []any{map[string]any{"primitiveId": "p1", "padNumber": "1", "net": "N1", "layer": 1, "x": 0, "y": 0, "width": 10, "height": 10}}}}}, false},
		{"special getter unavailable cannot use base shape", map[string]any{"components": []any{map[string]any{"primitiveId": "c1", "designator": "J1", "pads": []any{map[string]any{"primitiveId": "p1", "padNumber": "1", "net": "N1", "layer": 1, "x": 0, "y": 0, "shape": []any{"RECT", 10, 10, 0}, "width": 10, "height": 10, "availability": map[string]any{"shape": "available", "specialPad": "unavailable"}, "captureErrors": map[string]any{"specialPad": map[string]any{"code": "getter-threw", "message": "injected special failure"}}}}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newClearanceCheckFixture(t, 20, func(results map[string]map[string]any) {
				results["pcb.components.list"] = tc.result
				results["pcb.line.list"] = map[string]any{"lines": []any{}, "arcs": []any{}, "arcsAvailable": true}
			})
			var out, diagnostic bytes.Buffer
			err := runPcbCheck(cfg, "w1", 3, nil, true, true, &out, &diagnostic)
			if (err == nil) != tc.complete {
				t.Fatalf("complete=%v err=%v output=%s diagnostics=%s", tc.complete, err, out.String(), diagnostic.String())
			}
			var report pcbCheckReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Incomplete == tc.complete || report.Coverage.Complete != tc.complete {
				t.Fatalf("coverage=%+v incomplete=%v", report.Coverage, report.Incomplete)
			}
			if !tc.complete && report.Passed {
				t.Fatal("incomplete report advertised passed")
			}
			if !tc.complete && len(report.Limitations) == 0 {
				t.Fatal("incomplete report lost its reasons")
			}
			out.Reset()
			if err := runPcbCheck(cfg, "w1", 3, nil, false, false, &out, &diagnostic); err != nil {
				t.Fatalf("ordinary diagnostic was gated: %v", err)
			}
			if !tc.complete && strings.Contains(out.String(), "✓ no DFM issues") {
				t.Fatalf("unknown inventory rendered as clean: %s", out.String())
			}
		})
	}
}

func TestPcbCheckDoesNotCertifyMissingCopperInventory(t *testing.T) {
	for _, tc := range []struct {
		name, action, field string
		value               any
		wantReport          bool
	}{
		{"tracks null", "pcb.line.list", "lines", nil, false},
		{"vias null", "pcb.via.list", "vias", nil, false},
		{"tracks malformed", "pcb.line.list", "lines", []any{nil}, false},
		{"arcs null", "pcb.line.list", "arcs", nil, true},
		{"arcs getter unavailable", "pcb.line.list", "arcsAvailable", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newClearanceCheckFixture(t, 20, func(results map[string]map[string]any) {
				results["pcb.components.list"] = map[string]any{"components": []any{}}
				results["pcb.line.list"] = map[string]any{"lines": []any{}, "arcs": []any{}, "arcsAvailable": true}
				results[tc.action][tc.field] = tc.value
			})
			var out, diagnostic bytes.Buffer
			if err := runPcbCheck(cfg, "w1", 3, nil, true, true, &out, &diagnostic); err == nil {
				t.Fatalf("strict check certified missing copper: %s", out.String())
			}
			if tc.wantReport {
				var report pcbCheckReport
				if err := json.Unmarshal(out.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if report.Passed || !report.Incomplete || len(report.Limitations) == 0 {
					t.Fatalf("incomplete copper report=%+v", report)
				}
			}
		})
	}
}

func TestPcbPlanningPadsRejectPartialInventoryAndPose(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"missing pose", func(c, p map[string]any) { delete(p, "rotation") }},
		{"partial inventory", func(c, p map[string]any) { c["availability"] = map[string]any{"pads": "unavailable"} }},
		{"capture failed", func(c, p map[string]any) {
			p["availability"] = map[string]any{"specialPad": "unavailable"}
			p["captureErrors"] = map[string]any{"specialPad": map[string]any{"code": "getter-threw", "message": "cannot read special geometry"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newClearanceCheckFixture(t, 20, func(results map[string]map[string]any) {
				pad := map[string]any{"primitiveId": "p1", "padNumber": "1", "net": "GND", "layer": 1, "x": 10, "y": 20, "rotation": 0, "shape": []any{"RECT", 20, 10, 0}, "width": 20, "height": 10}
				component := map[string]any{"primitiveId": "c1", "designator": "J1", "pads": []any{pad}, "availability": map[string]any{"pads": "available"}}
				tc.mutate(component, pad)
				results["pcb.components.list"] = map[string]any{"components": []any{component}}
			})
			if pads, err := fetchPcbPads(cfg, "w1"); err == nil {
				t.Fatalf("planning accepted partial geometry/inventory: %+v", pads)
			}
			if _, coverage, err := fetchPcbPadsWithCoverage(cfg, "w1"); err != nil || coverage.Complete {
				t.Fatalf("read-only observation incorrectly gated or certified: coverage=%+v err=%v", coverage, err)
			}
		})
	}
}

// Live ceshi 2026-11-21 (EDA 4.1.105): native DRC flagged a track 11 mil from
// the outline center line ("should be >= 11.8mil") while pcb check passed it,
// because it measured to the rendered bbox, which adds half the 10 mil outline
// stroke. The edge check must use centerlineBBox like native DRC.
func TestPcbCheckCopperNearEdgeUsesOutlineCenterline(t *testing.T) {
	// Track N1 runs x 0..100 at y 0, width 8 → copper edge at x=104.
	outline := func(results map[string]map[string]any) {
		results["pcb.outline.get"] = map[string]any{
			"bbox":           map[string]any{"minX": -1005, "minY": -1005, "maxX": 120, "maxY": 1005},
			"centerlineBBox": map[string]any{"minX": -1000, "minY": -1000, "maxX": 115, "maxY": 1000},
			"lineWidth":      10,
		}
	}
	cfg := newClearanceCheckFixture(t, 20, outline)
	var stdout, stderr bytes.Buffer
	if err := runPcbCheck(cfg, "w1", 3, nil, false, true, &stdout, &stderr); err != nil {
		t.Fatalf("check: %v stderr=%s", err, stderr.String())
	}
	var report pcbCheckReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.Summary.CopperNearEdge != 1 {
		t.Fatalf("11 mil gap to the center line must be flagged under the 11.8 mil rule: summary=%+v findings=%+v", report.Summary, report.Findings)
	}
}
