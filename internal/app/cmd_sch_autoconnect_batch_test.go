package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// newFakeBatchDaemon stands up a daemon with TWO parts whose pins sit on the
// same vertical line, spaced so that their naive kind-default stubs (GND down
// from y=30 reaching y=48, VCC up from y=62 reaching y=44 at the shortest
// offset) collinear-overlap on x=10 — the B0512S-class adjacent
// multi-domain-pin geometry from issue #133/#138. The pins sit 14 apart from
// each other's stub so the fanout-channel penalty (MinLabelGap 12) does not
// distort direction choice. connect_pin always succeeds.
func newFakeBatchDaemon(t *testing.T) (*appConfig, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[]}`))
			return
		}
		if r.URL.Path != "/action" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Action  string         `json:"action"`
			Payload map[string]any `json:"payload"`
		}
		body, _ := readAllBody(r)
		_ = json.Unmarshal(body, &req)

		var result map[string]any
		switch req.Action {
		case "schematic.components.list":
			result = map[string]any{"components": []any{
				map[string]any{
					"componentType": "part", "designator": "U1",
					"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 20.0, "maxY": 28.0},
					"pins": []any{
						map[string]any{"pinNumber": "1", "pinName": "GND", "x": 10.0, "y": 30.0, "net": ""},
					},
				},
				map[string]any{
					"componentType": "part", "designator": "U2",
					"bbox": map[string]any{"minX": 0.0, "minY": 64.0, "maxX": 20.0, "maxY": 92.0},
					"pins": []any{
						map[string]any{"pinNumber": "1", "pinName": "VCC", "x": 10.0, "y": 62.0, "net": ""},
					},
				},
			}}
		case "schematic.power.connect_pin":
			result = map[string]any{"wirePrimitiveId": "w", "flagPrimitiveId": "f"}
		default:
			result = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))

	hostPort := strings.TrimPrefix(srv.URL, "http://")
	host, portStr, _ := strings.Cut(hostPort, ":")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	cfg := &appConfig{host: host, ports: fmt.Sprintf("%d-%d", port, port)}
	return cfg, srv.Close
}

// TestAutoconnect_BatchStubsAreMutuallyExclusive is the issue #138 regression:
// two connections planned in ONE batch on different nets must never pick stubs
// that touch each other. Pre-fix, each candidate was scored against a scene
// that ignored its batch siblings, so U1:1's GND stub (down, ending at y=48)
// and U2:1's VCC stub (kind default up, ending at y=40) collinear-overlapped
// on x=10 — EasyEDA would merge them into one net (a silent GND/VCC short).
// Post-fix, the first planned stub is registered as a scene wire, so the
// second connection's "up" candidates are hard-rejected as foreign-wire
// touches and the planner steers to a clean direction.
func TestAutoconnect_BatchStubsAreMutuallyExclusive(t *testing.T) {
	cfg, cleanup := newFakeBatchDaemon(t)
	defer cleanup()

	conns := []acConnSpec{
		{PinRef: "U1:1", Kind: "gnd", Net: "GND"},
		{PinRef: "U2:1", Kind: "power", Net: "VCC"},
	}

	var out bytes.Buffer
	if err := runAutoconnect(cfg, "", conns, defaultAutoconnectRules(), false, false, false, true, &out, &out); err != nil {
		t.Fatalf("run failed: %v\n%s", err, out.String())
	}

	var report acReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("parse report: %v\n%s", err, out.String())
	}
	if len(report.Connections) != 2 {
		t.Fatalf("want 2 connections, got %d", len(report.Connections))
	}
	first, second := report.Connections[0], report.Connections[1]
	if first.Selected == nil || second.Selected == nil {
		t.Fatalf("both connections must select a candidate: %+v / %+v", first, second)
	}

	// Sanity: the first connection takes its unobstructed preferred direction.
	if first.Selected.Direction != "down" {
		t.Fatalf("U1:1 GND should go down (outward + kind default), got %s", first.Selected.Direction)
	}
	// The second connection's preferred "up" collides with the first stub and
	// must have been steered away.
	if second.Selected.Direction == "up" {
		t.Fatalf("U2:1 VCC picked 'up' — its stub overlaps U1:1's planned GND stub (no batch mutual exclusion)")
	}
	// The invariant that actually matters: the two placed stubs never touch.
	if segmentsTouch(
		first.PinX, first.PinY, first.Selected.EndPoint.X, first.Selected.EndPoint.Y,
		second.PinX, second.PinY, second.Selected.EndPoint.X, second.Selected.EndPoint.Y,
	) {
		t.Fatalf("batch stubs touch: %v→%v and %v→%v",
			acPoint{X: first.PinX, Y: first.PinY}, first.Selected.EndPoint,
			acPoint{X: second.PinX, Y: second.PinY}, second.Selected.EndPoint)
	}
	// And the rejection is attributed to the wire-touch hard reject, so the
	// report explains WHY "up" was refused.
	foundUpReject := false
	for _, rj := range second.Rejected {
		if rj.Direction == "up" && strings.Contains(rj.Reason, "foreign-net") {
			foundUpReject = true
		}
	}
	if !foundUpReject {
		t.Errorf("expected 'up' rejected with a foreign-net wire-touch reason, got %+v", second.Rejected)
	}
}

// TestAutoconnect_BatchStubAllBlockedFailsLoud is the other half of issue #138:
// when a batch sibling's stub blocks the LAST clean direction (existing
// foreign-net wires already box in left/right/down), the connection must fail
// loudly ("no safe candidate") instead of placing the up stub over the sibling
// — pre-fix the sibling stub was invisible, so "up" looked clean and the run
// silently merged GND into VCC.
func TestAutoconnect_BatchStubAllBlockedFailsLoud(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[]}`))
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		body, _ := readAllBody(r)
		_ = json.Unmarshal(body, &req)
		var result map[string]any
		switch req.Action {
		case "schematic.components.list":
			result = map[string]any{
				"components": []any{
					map[string]any{
						"componentType": "part", "designator": "U1",
						"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 20.0, "maxY": 28.0},
						"pins": []any{
							map[string]any{"pinNumber": "1", "pinName": "GND", "x": 10.0, "y": 30.0, "net": ""},
						},
					},
					map[string]any{
						"componentType": "part", "designator": "U2",
						"bbox": map[string]any{"minX": 0.0, "minY": 64.0, "maxX": 20.0, "maxY": 92.0},
						"pins": []any{
							map[string]any{"pinNumber": "1", "pinName": "VCC", "x": 10.0, "y": 62.0, "net": ""},
						},
					},
				},
				// Existing foreign-net wires box in U2:1's left, right and down
				// corridors; only "up" is geometrically clean — until U1:1's
				// batch stub claims it.
				"wires": []any{
					map[string]any{"x0": -2.0, "y0": 50.0, "x1": -2.0, "y1": 74.0, "net": "X"},
					map[string]any{"x0": 22.0, "y0": 50.0, "x1": 22.0, "y1": 74.0, "net": "X"},
					map[string]any{"x0": 0.0, "y0": 80.0, "x1": 20.0, "y1": 80.0, "net": "X"},
				},
			}
		case "schematic.power.connect_pin":
			result = map[string]any{"wirePrimitiveId": "w", "flagPrimitiveId": "f"}
		default:
			result = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer srv.Close()
	hostPort := strings.TrimPrefix(srv.URL, "http://")
	host, portStr, _ := strings.Cut(hostPort, ":")
	port, _ := strconv.Atoi(portStr)
	cfg := &appConfig{host: host, ports: fmt.Sprintf("%d-%d", port, port)}

	conns := []acConnSpec{
		{PinRef: "U1:1", Kind: "gnd", Net: "GND"},
		{PinRef: "U2:1", Kind: "power", Net: "VCC"},
	}
	var out bytes.Buffer
	err := runAutoconnect(cfg, "", conns, defaultAutoconnectRules(), false, false, false, true, &out, &out)
	if err == nil {
		t.Fatalf("expected the boxed-in connection to fail, got success:\n%s", out.String())
	}
	var report acReport
	if jerr := json.Unmarshal(out.Bytes(), &report); jerr != nil {
		t.Fatalf("parse report: %v\n%s", jerr, out.String())
	}
	if len(report.Connections) != 2 {
		t.Fatalf("want 2 connections, got %d", len(report.Connections))
	}
	if report.Connections[0].Error != "" {
		t.Fatalf("first connection should place cleanly, got error: %s", report.Connections[0].Error)
	}
	if !strings.Contains(report.Connections[1].Error, "no safe candidate") {
		t.Fatalf("second connection must refuse with 'no safe candidate', got: %+v", report.Connections[1])
	}
}
