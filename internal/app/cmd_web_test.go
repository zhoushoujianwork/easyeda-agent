package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebReloadSavesAndRequiresNewReadableRegistration(t *testing.T) {
	var actions []string
	reloadScheduled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			id := "old-window"
			if reloadScheduled {
				id = "new-window"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"service": "easyeda-agent",
				"windows": []any{map[string]any{
					"windowId": id,
					"context":  map[string]any{"projectUuid": "project-1", "documentUuid": "doc-1", "documentType": "schematic", "tabId": "tab-1"},
				}},
			})
			return
		}
		var req struct {
			Action  string         `json:"action"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		actions = append(actions, req.Action)
		result := map[string]any{}
		switch req.Action {
		case "project.current":
			result["uuid"] = "project-1"
		case "document.current":
			result["uuid"], result["documentType"] = "doc-1", "schematic"
		case "schematic.save":
			result["saved"] = true
		case "system.page_reload":
			if req.Payload["projectUuid"] != "project-1" || req.Payload["documentUuid"] != "doc-1" {
				t.Errorf("reload payload=%v", req.Payload)
			}
			result["scheduled"] = true
			reloadScheduled = true
		case "schematic.components.list":
			result["count"] = 13
		default:
			t.Errorf("unexpected action %q", req.Action)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result,
			"context": map[string]any{"projectUuid": "project-1", "documentUuid": "doc-1", "documentType": "schematic"}})
	}))
	defer server.Close()
	host, port, _ := strings.Cut(strings.TrimPrefix(server.URL, "http://"), ":")
	cfg := &appConfig{host: host, ports: port + "-" + port, project: "project-1", doc: "doc-1"}
	report, err := reloadWebPage(cfg, "", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if report["oldWindowId"] != "old-window" || report["newWindowId"] != "new-window" || report["ready"] != true {
		t.Fatalf("unexpected report: %v", report)
	}
	want := "project.current,document.current,schematic.save,system.page_reload,document.current,schematic.components.list,schematic.components.list"
	if strings.Join(actions, ",") != want {
		t.Fatalf("actions=%v, want %s", actions, want)
	}
}

func TestWebReloadRefusesWrongDocumentBeforeSave(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"old-window","context":{"projectUuid":"project-1","documentUuid":"doc-2"}}]}`))
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Action {
		case "project.current":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"uuid":"project-1"}}`))
		case "document.current":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"uuid":"doc-2","documentType":"schematic"}}`))
		default:
			t.Errorf("unexpected write/action %q", req.Action)
		}
	}))
	defer server.Close()
	host, port, _ := strings.Cut(strings.TrimPrefix(server.URL, "http://"), ":")
	cfg := &appConfig{host: host, ports: port + "-" + port, project: "project-1", doc: "doc-1"}
	if _, err := reloadWebPage(cfg, "", time.Second); err == nil || !strings.Contains(err.Error(), "exact active UUID") {
		t.Fatalf("expected pre-save document refusal, got %v", err)
	}
}
