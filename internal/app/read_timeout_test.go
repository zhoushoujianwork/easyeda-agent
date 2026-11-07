package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exercise the shared HTTP boundary, so both legacy explicit budgets and
// default callers are covered without enumerating dozens of call sites.
func TestReadBudgetOnWire(t *testing.T) {
	for _, tc := range []struct {
		action    string
		payload   any
		requested time.Duration
		want      int
	}{
		{"document.open", nil, 20 * time.Second, 32000},
		{"schematic.attribute.move", nil, 20 * time.Second, 47000},
		{"schematic.attribute.move", nil, 5 * time.Second, 5000},
		{"schematic.page.open", nil, 20 * time.Second, 32000},
		{"document.open", nil, 90 * time.Second, 90000},
		{"document.open", nil, 5 * time.Second, 5000},
		{"schematic.components.list", map[string]any{"includePins": true}, 20 * time.Second, 152000},
		{"schematic.components.list", map[string]any{"includePins": true, "includeWires": true}, 90 * time.Second, 152000},
		{"schematic.components.list", map[string]any{"includePins": true}, 180 * time.Second, 180000},
		{"schematic.components.list", map[string]any{"includePins": true}, 5 * time.Second, 5000},
		{"schematic.components.list", map[string]any{"includePins": false}, 20 * time.Second, 152000},
		{"schematic.components.list", nil, 20 * time.Second, 152000},
		{"schematic.geometry.read", nil, 20 * time.Second, 20000},
		{"document.current", nil, 20 * time.Second, 20000},
		{"pcb.pour.create", map[string]any{"priority": 0}, 20 * time.Second, 62000},
		{"pcb.pour.create", map[string]any{"priority": 2}, 90 * time.Second, 90000},
		{"pcb.pour.create", map[string]any{"priority": 0}, 5 * time.Second, 5000},
		{"pcb.pour.create", map[string]any{}, 20 * time.Second, 20000},
	} {
		t.Run(tc.action+tc.requested.String(), func(t *testing.T) {
			got := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					_, _ = w.Write([]byte(`{"service":"easyeda-agent"}`))
					return
				}
				var req struct {
					TimeoutMs int `json:"timeoutMs"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				got = req.TimeoutMs
				_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			}))
			defer srv.Close()
			host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
			cfg := &appConfig{host: host, ports: port + "-" + port}
			if _, err := postAction(cfg, tc.action, "w1", tc.payload, tc.requested); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("wire timeout = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDocGuardReadsBackAfterFailedOpen(t *testing.T) {
	for _, landed := range []bool{true, false} {
		t.Run(map[bool]string{true: "landed", false: "not-landed"}[landed], func(t *testing.T) {
			opens, readbacks := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"w1"}]}`))
					return
				}
				var req struct {
					Action string `json:"action"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				doc := "old"
				ok := true
				result := map[string]any{}
				switch req.Action {
				case "document.current":
					if opens > 0 {
						readbacks++
						if landed {
							doc = "target"
						}
					}
				case "schematic.pages.list":
					result["pages"] = []any{map[string]any{"uuid": "target", "name": "P2"}}
				case "pcb.documents.list":
					result["pcbs"] = []any{}
				case "document.open":
					opens++
					ok = false
				default:
					t.Errorf("unexpected action %s", req.Action)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "result": result, "context": map[string]any{"documentUuid": doc, "documentType": "schematic"}, "error": map[string]any{"code": "DISPATCH_FAILED", "message": "connector did not respond"}})
			}))
			defer srv.Close()
			host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
			cfg := &appConfig{host: host, ports: port + "-" + port, doc: "P2"}
			err := ensureActiveDoc(cfg, "w1")
			if (err == nil) != landed {
				t.Fatalf("landed=%v, error=%v", landed, err)
			}
			if opens != 1 || readbacks != 1 {
				t.Fatalf("opens=%d readbacks=%d; want one open and one verification", opens, readbacks)
			}
		})
	}
}
