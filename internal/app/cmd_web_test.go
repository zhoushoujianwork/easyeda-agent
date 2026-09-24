package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type webReloadFixture struct {
	mu             sync.Mutex
	scheduled      bool
	activeDoc      string
	reloadDoc      string
	healthProject  string
	openCalls      int
	readCalls      int
	postReads      int
	postCurrents   int
	currentDocs    []string
	failedReads    int
	baselineEmpty  bool
	emptyAfter     bool
	driftAfterRead int
	slowRead       bool
	actions        []string
}

func newWebReloadFixture(t *testing.T, fx *webReloadFixture) (*appConfig, func()) {
	t.Helper()
	fx.activeDoc = "doc-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		project := "project-1"
		if fx.scheduled && fx.healthProject != "" {
			project = fx.healthProject
		}
		window := "old-window"
		if fx.scheduled {
			window = "new-window"
		}
		docType := "schematic"
		if fx.activeDoc == "doc-2" {
			docType = "pcb"
		}
		ctx := map[string]any{"projectUuid": project, "documentUuid": fx.activeDoc, "documentType": docType, "tabId": fx.activeDoc + "-tab"}
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(map[string]any{"service": "easyeda-agent", "windows": []any{
				map[string]any{"windowId": window, "context": ctx},
			}})
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
		fx.actions = append(fx.actions, req.Action)
		result := map[string]any{}
		switch req.Action {
		case "project.current":
			result["uuid"] = project
		case "document.current":
			if fx.scheduled && fx.postCurrents < len(fx.currentDocs) {
				fx.activeDoc = fx.currentDocs[fx.postCurrents]
			} else if fx.scheduled && fx.driftAfterRead > 0 && fx.postReads >= fx.driftAfterRead {
				fx.activeDoc = "doc-2"
			}
			if fx.scheduled {
				fx.postCurrents++
			}
			if fx.activeDoc == "doc-2" {
				docType = "pcb"
				ctx["documentUuid"], ctx["documentType"], ctx["tabId"] = "doc-2", docType, "doc-2-tab"
			}
			result["uuid"], result["documentType"] = fx.activeDoc, docType
			result["tabId"], result["parentProjectUuid"] = fx.activeDoc+"-tab", project
		case "schematic.save":
			result["saved"] = true
		case "system.page_reload":
			if req.Payload["projectUuid"] != "project-1" || req.Payload["documentUuid"] != "doc-1" {
				t.Errorf("reload payload=%v", req.Payload)
			}
			result["scheduled"] = true
			fx.scheduled = true
			if fx.reloadDoc != "" {
				fx.activeDoc = fx.reloadDoc
			}
		case "document.open":
			fx.openCalls++
			if req.Payload["uuid"] != "doc-1" || project != "project-1" {
				t.Errorf("unsafe restore: project=%s payload=%v", project, req.Payload)
			}
			fx.activeDoc = "doc-1"
			result["tabId"], result["ready"] = "doc-1-tab", false
		case "schematic.components.list":
			fx.readCalls++
			if fx.scheduled {
				fx.postReads++
			}
			if fx.slowRead && fx.scheduled {
				fx.mu.Unlock()
				select {
				case <-r.Context().Done():
				case <-time.After(2 * time.Second):
				}
				fx.mu.Lock()
				return
			}
			if fx.scheduled && fx.postReads <= fx.failedReads {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": "EDA_CALL_FAILED", "message": "page still loading"}, "context": ctx})
				return
			}
			if (!fx.scheduled && fx.baselineEmpty) || (fx.scheduled && fx.emptyAfter) {
				result["count"], result["components"] = 0, []any{}
			} else {
				result["count"], result["components"] = 1, []any{map[string]any{"primitiveId": "part-1", "designator": "R1"}}
			}
		default:
			t.Errorf("unexpected action %q", req.Action)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result, "context": ctx})
	}))
	host, port, _ := strings.Cut(strings.TrimPrefix(server.URL, "http://"), ":")
	return &appConfig{host: host, ports: port + "-" + port, project: "project-1", doc: "doc-1"}, server.Close
}

func (fx *webReloadFixture) snapshot() (actions []string, opens, reads int) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return append([]string(nil), fx.actions...), fx.openCalls, fx.readCalls
}

func TestWebReloadSavesAndRequiresNewReadableRegistration(t *testing.T) {
	fx := &webReloadFixture{}
	cfg, closeServer := newWebReloadFixture(t, fx)
	defer closeServer()
	report, err := reloadWebPage(cfg, "", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if report["oldWindowId"] != "old-window" || report["newWindowId"] != "new-window" || report["ready"] != true {
		t.Fatalf("unexpected report: %v", report)
	}
	actions, opens, reads := fx.snapshot()
	if opens != 0 || reads != 3 {
		t.Fatalf("opens=%d reads=%d", opens, reads)
	}
	want := "project.current,document.current,schematic.save,schematic.components.list,system.page_reload,document.current,schematic.components.list,document.current,schematic.components.list,document.current"
	if strings.Join(actions, ",") != want {
		t.Fatalf("actions=%v, want %s", actions, want)
	}
}

func TestWebReloadRestoresTargetOnceAfterDriftAndReadFailure(t *testing.T) {
	fx := &webReloadFixture{reloadDoc: "doc-2", failedReads: 1}
	cfg, closeServer := newWebReloadFixture(t, fx)
	defer closeServer()
	report, err := reloadWebPage(cfg, "", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if report["ready"] != true || report["documentUuid"] != "doc-1" {
		t.Fatalf("unexpected report: %v", report)
	}
	_, opens, reads := fx.snapshot()
	if opens != 1 || reads != 4 {
		t.Fatalf("opens=%d reads=%d, want one restore and one transient failed read", opens, reads)
	}
}

func TestWebReloadRestoresAfterLateDriftAndTransientOldPage(t *testing.T) {
	fx := &webReloadFixture{currentDocs: []string{"doc-1", "doc-2", "doc-2", "doc-1", "doc-1", "doc-1"}}
	cfg, closeServer := newWebReloadFixture(t, fx)
	defer closeServer()
	report, err := reloadWebPage(cfg, "", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if report["ready"] != true {
		t.Fatalf("unexpected report: %v", report)
	}
	_, opens, reads := fx.snapshot()
	if opens != 1 || reads != 4 { // baseline, first target, two post-open reads
		t.Fatalf("opens=%d reads=%d; want one restore after a later drift", opens, reads)
	}
}

func TestWebReloadRefusesDriftAfterRestore(t *testing.T) {
	fx := &webReloadFixture{reloadDoc: "doc-2", driftAfterRead: 1}
	cfg, closeServer := newWebReloadFixture(t, fx)
	defer closeServer()
	if _, err := reloadWebPage(cfg, "", 2*time.Second); err == nil || !strings.Contains(err.Error(), "drifted") {
		t.Fatalf("expected post-restore drift failure, got %v", err)
	}
	_, opens, _ := fx.snapshot()
	if opens != 1 {
		t.Fatalf("document.open called %d times, want one", opens)
	}
}

func TestWebReloadFinalCurrentMustStillMatch(t *testing.T) {
	fx := &webReloadFixture{driftAfterRead: 2}
	cfg, closeServer := newWebReloadFixture(t, fx)
	defer closeServer()
	if _, err := reloadWebPage(cfg, "", 2*time.Second); err == nil || !strings.Contains(err.Error(), "final document.current drifted") {
		t.Fatalf("expected final-current drift failure, got %v", err)
	}
	_, opens, reads := fx.snapshot()
	if opens != 0 || reads != 3 { // one baseline and two post-refresh samples
		t.Fatalf("opens=%d reads=%d", opens, reads)
	}
}

func TestWebReloadUnreadyNeverBecomesSuccess(t *testing.T) {
	for _, name := range []string{"read-failure", "empty-inventory"} {
		t.Run(name, func(t *testing.T) {
			fx := &webReloadFixture{failedReads: 100, emptyAfter: name == "empty-inventory"}
			if name == "empty-inventory" {
				fx.failedReads = 0
			}
			cfg, closeServer := newWebReloadFixture(t, fx)
			defer closeServer()
			if _, err := reloadWebPage(cfg, "", 450*time.Millisecond); err == nil || !strings.Contains(err.Error(), "did not become stably readable") {
				t.Fatalf("expected bounded unreadable failure, got %v", err)
			}
			_, opens, _ := fx.snapshot()
			if opens != 0 {
				t.Fatalf("read failure opened document %d times", opens)
			}
		})
	}
}

func TestWebReloadVerifiedEmptyBaselineSettlesAsEmptyPage(t *testing.T) {
	fx := &webReloadFixture{baselineEmpty: true, emptyAfter: true}
	cfg, closeServer := newWebReloadFixture(t, fx)
	defer closeServer()
	started := time.Now()
	report, err := reloadWebPage(cfg, "", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, opens, reads := fx.snapshot()
	if report["ready"] != true || report["readbackScope"] != "stable-empty-page-routing" || opens != 0 || reads < 5 || time.Since(started) < 700*time.Millisecond {
		t.Fatalf("empty page accepted without grace: report=%v opens=%d reads=%d", report, opens, reads)
	}
}

func TestWebReloadInventoryIgnoresEnumerationOrder(t *testing.T) {
	component := func(id string) any { return map[string]any{"primitiveId": id, "designator": id} }
	makeResult := func(rows []any) *actionResult {
		return &actionResult{Context: &actionContext{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "pcb", TabID: "tab"},
			Result: map[string]any{"count": float64(2), "components": rows}}
	}
	a, idsA, err := webReloadInventory(makeResult([]any{component("a"), component("b")}), "p", "d", "pcb", "tab")
	if err != nil {
		t.Fatal(err)
	}
	b, idsB, err := webReloadInventory(makeResult([]any{component("b"), component("a")}), "p", "d", "pcb", "tab")
	if err != nil || a != b || !webReloadSameIDs(idsA, idsB) {
		t.Fatalf("same objects reordered: fingerprints %x/%x IDs %v/%v error %v", a, b, idsA, idsB, err)
	}
}

func TestWebReloadWrongProjectCannotRestore(t *testing.T) {
	fx := &webReloadFixture{reloadDoc: "doc-2", healthProject: "project-2"}
	cfg, closeServer := newWebReloadFixture(t, fx)
	defer closeServer()
	if _, err := reloadWebPage(cfg, "", 350*time.Millisecond); err == nil {
		t.Fatal("wrong project unexpectedly became ready")
	}
	_, opens, _ := fx.snapshot()
	if opens != 0 {
		t.Fatalf("wrong-project document.open called %d times", opens)
	}
}

func TestWebReloadReadConsumesOnlyRemainingBudget(t *testing.T) {
	fx := &webReloadFixture{slowRead: true}
	cfg, closeServer := newWebReloadFixture(t, fx)
	defer closeServer()
	started := time.Now()
	if _, err := reloadWebPage(cfg, "", 350*time.Millisecond); err == nil {
		t.Fatal("slow read unexpectedly became ready")
	}
	if elapsed := time.Since(started); elapsed > 900*time.Millisecond {
		t.Fatalf("350ms reload budget consumed %s", elapsed)
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
