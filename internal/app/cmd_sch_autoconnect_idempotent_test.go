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

// fakeSchState is a tiny stateful model of the schematic the mock daemon mutates:
// the current net of one pin, and the running count of flags+wires. connect_pin
// bumps both counts and sets the pin net; disconnect clears them. It lets the
// idempotency regression assert "run the same spec twice → no growth" (issue #50).
type fakeSchState struct {
	pinNet     string // "" = floating
	flagCount  int
	wireCount  int
	connectHit int
}

// newFakeSchDaemon stands up a daemon that answers the three actions autoconnect
// uses (components.list, power.connect_pin, pin.disconnect) against a mutable
// fakeSchState. The single pin U1:1 reports its current net so the Go idempotency
// check has real data to reason about.
func newFakeSchDaemon(t *testing.T, st *fakeSchState) (*appConfig, func()) {
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
					"bbox": map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 20.0, "maxY": 20.0},
					"pins": []any{
						map[string]any{"pinNumber": "1", "pinName": "GND", "x": 10.0, "y": 25.0, "net": st.pinNet},
					},
				},
			}}
		case "schematic.power.connect_pin":
			st.connectHit++
			st.flagCount++
			st.wireCount++
			st.pinNet = asString(req.Payload["net"])
			result = map[string]any{"wirePrimitiveId": "w1", "flagPrimitiveId": "f1"}
		case "schematic.pin.disconnect":
			result = map[string]any{
				"deletedWireCount": st.wireCount, "deletedFlagCount": st.flagCount,
			}
			st.flagCount = 0
			st.wireCount = 0
			st.pinNet = ""
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

func readAllBody(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

// TestAutoconnect_RerunIsIdempotent is the acceptance regression: connecting the
// same spec twice must NOT grow flag/wire counts. The first run connects (1 flag +
// 1 wire); the second sees the pin already on GND and skips.
func TestAutoconnect_RerunIsIdempotent(t *testing.T) {
	st := &fakeSchState{}
	cfg, cleanup := newFakeSchDaemon(t, st)
	defer cleanup()

	conns := []acConnSpec{{PinRef: "U1:1", Kind: "gnd", Net: "GND"}}
	rules := defaultAutoconnectRules()

	var out bytes.Buffer
	if err := runAutoconnect(cfg, "", conns, rules, false, false, false, false, &out, &out); err != nil {
		t.Fatalf("first run failed: %v\n%s", err, out.String())
	}
	if st.flagCount != 1 || st.wireCount != 1 {
		t.Fatalf("after first run want 1 flag/1 wire, got %d/%d", st.flagCount, st.wireCount)
	}

	out.Reset()
	if err := runAutoconnect(cfg, "", conns, rules, false, false, false, false, &out, &out); err != nil {
		t.Fatalf("second run failed: %v\n%s", err, out.String())
	}
	// The core assertion: no growth.
	if st.flagCount != 1 || st.wireCount != 1 {
		t.Fatalf("after rerun want 1 flag/1 wire (no growth), got %d/%d", st.flagCount, st.wireCount)
	}
	if st.connectHit != 1 {
		t.Errorf("connect_pin should fire once across both runs, got %d", st.connectHit)
	}
	if !strings.Contains(out.String(), "already-connected") {
		t.Errorf("second run should report already-connected, got:\n%s", out.String())
	}
}

// TestAutoconnect_ConflictErrorsWithoutReplace: a pin already on a DIFFERENT net
// is an error by default and must NOT mutate.
func TestAutoconnect_ConflictErrorsWithoutReplace(t *testing.T) {
	st := &fakeSchState{pinNet: "+3V3", flagCount: 1, wireCount: 1}
	cfg, cleanup := newFakeSchDaemon(t, st)
	defer cleanup()

	conns := []acConnSpec{{PinRef: "U1:1", Kind: "gnd", Net: "GND"}}
	var out bytes.Buffer
	err := runAutoconnect(cfg, "", conns, defaultAutoconnectRules(), false, false, false, false, &out, &out)
	if err == nil {
		t.Fatalf("expected conflict error, got nil\n%s", out.String())
	}
	if st.connectHit != 0 {
		t.Errorf("conflict must not connect, connectHit=%d", st.connectHit)
	}
	if st.flagCount != 1 || st.wireCount != 1 {
		t.Errorf("conflict must not mutate, got %d/%d", st.flagCount, st.wireCount)
	}
}

// TestAutoconnect_ReplaceOverwritesConflict: --replace deletes the old flag+wire
// and reconnects, so the net ends on the target and counts stay at 1/1.
func TestAutoconnect_ReplaceOverwritesConflict(t *testing.T) {
	st := &fakeSchState{pinNet: "+3V3", flagCount: 1, wireCount: 1}
	cfg, cleanup := newFakeSchDaemon(t, st)
	defer cleanup()

	conns := []acConnSpec{{PinRef: "U1:1", Kind: "gnd", Net: "GND"}}
	var out bytes.Buffer
	if err := runAutoconnect(cfg, "", conns, defaultAutoconnectRules(), false, false, true, false, &out, &out); err != nil {
		t.Fatalf("replace run failed: %v\n%s", err, out.String())
	}
	if st.pinNet != "GND" {
		t.Errorf("after replace pin should be GND, got %q", st.pinNet)
	}
	if st.flagCount != 1 || st.wireCount != 1 {
		t.Errorf("after replace want 1 flag/1 wire, got %d/%d", st.flagCount, st.wireCount)
	}
	if !strings.Contains(out.String(), "replaced") {
		t.Errorf("replace run should report 'replaced', got:\n%s", out.String())
	}
}
