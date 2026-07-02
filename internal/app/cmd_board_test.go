package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// newCapturingDaemon stands up a fake daemon that identifies as easyeda-agent
// and records the last /action request body, so a command test can assert the
// exact action + payload the CLI wired.
func newCapturingDaemon(t *testing.T) (*appConfig, *capturedRequest, func()) {
	t.Helper()
	cap := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"w1"}]}`))
		case "/action":
			var body struct {
				Action  string         `json:"action"`
				Payload map[string]any `json:"payload"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			cap.mu.Lock()
			cap.action = body.Action
			cap.payload = body.Payload
			cap.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))

	hostPort := strings.TrimPrefix(srv.URL, "http://")
	host, portStr, _ := strings.Cut(hostPort, ":")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}
	cfg := &appConfig{host: host, ports: fmt.Sprintf("%d-%d", port, port)}
	return cfg, cap, srv.Close
}

type capturedRequest struct {
	mu      sync.Mutex
	action  string
	payload map[string]any
}

// board rebind requires --schematic; without it the command errors before any
// daemon call.
func TestBoardRebind_RequiresSchematic(t *testing.T) {
	cfg, _, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	cmd := newBoardCmd(cfg, &stdout, &stderr)
	cmd.SetArgs([]string{"rebind", "--pcb", "pcb1"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when --schematic is missing")
	}
}

// board rebind maps to the board.rebind action and forwards the schematic/pcb/
// name/force fields into the payload.
func TestBoardRebind_WiresActionAndPayload(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	cmd := newBoardCmd(cfg, &stdout, &stderr)
	cmd.SetArgs([]string{"rebind", "--name", "Board1", "--schematic", "sch1", "--pcb", "pcb1", "--force"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.action != "board.rebind" {
		t.Fatalf("expected action board.rebind, got %q", cap.action)
	}
	if cap.payload["schematicUuid"] != "sch1" {
		t.Errorf("schematicUuid: got %v", cap.payload["schematicUuid"])
	}
	if cap.payload["pcbUuid"] != "pcb1" {
		t.Errorf("pcbUuid: got %v", cap.payload["pcbUuid"])
	}
	if cap.payload["name"] != "Board1" {
		t.Errorf("name: got %v", cap.payload["name"])
	}
	if cap.payload["force"] != true {
		t.Errorf("force: got %v", cap.payload["force"])
	}
}

// Without --pcb / --name / --force, only schematicUuid is sent (optional fields
// are omitted, not sent empty).
func TestBoardRebind_OmitsUnsetFields(t *testing.T) {
	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	cmd := newBoardCmd(cfg, &stdout, &stderr)
	cmd.SetArgs([]string{"rebind", "--schematic", "sch1"})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if _, ok := cap.payload["pcbUuid"]; ok {
		t.Error("pcbUuid should be omitted when --pcb is unset")
	}
	if _, ok := cap.payload["name"]; ok {
		t.Error("name should be omitted when --name is unset")
	}
	if _, ok := cap.payload["force"]; ok {
		t.Error("force should be omitted when --force is unset")
	}
}
