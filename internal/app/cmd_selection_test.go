package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// selectionFakeConnector answers /health and every action with the given
// envelope, recording the actions it received.
func selectionFakeConnector(t *testing.T, reply map[string]any, seen *[]string) *appConfig {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"w1","context":{"projectUuid":"project-1","projectName":"target"}}]}`))
			return
		}
		var req struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		*seen = append(*seen, req.Action)
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	host, port, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
	return &appConfig{host: host, ports: port + "-" + port, project: "target"}
}

func selectionContext(docType string) map[string]any {
	return map[string]any{
		"projectUuid": "project-1", "projectName": "target",
		"documentUuid": "pcb-doc-1", "documentType": docType, "tabId": "tab-1",
	}
}

func TestSelectionCmdPrintsTable(t *testing.T) {
	var seen []string
	cfg := selectionFakeConnector(t, map[string]any{
		"ok": true, "context": selectionContext("pcb"),
		"result": map[string]any{
			"documentType": "pcb", "count": 2, "detail": true,
			"items": []any{
				map[string]any{"primitiveId": "cmp1", "primitiveType": "Component", "designator": "R1", "layer": 1, "x": 1200.5, "y": -800, "locked": false},
				map[string]any{"primitiveId": "trk1", "primitiveType": "Line", "net": "GND"},
			},
		},
	}, &seen)

	var out, errOut bytes.Buffer
	cmd := newSelectionCmd(cfg, &out, &errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("selection: %v\n%s", err, out.String())
	}
	if len(seen) != 1 || seen[0] != "selection.get" {
		t.Fatalf("actions=%v, want exactly [selection.get]", seen)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want header line + column row + 2 items, got %d lines:\n%s", len(lines), out.String())
	}
	if lines[0] != "pcb pcb-doc-1: 2 selected" {
		t.Fatalf("first line = %q", lines[0])
	}
	if got := strings.Fields(lines[1]); strings.Join(got, " ") != "ID TYPE DESIGNATOR NET LAYER X Y" {
		t.Fatalf("columns = %q", lines[1])
	}
	if got := strings.Join(strings.Fields(lines[2]), " "); got != "cmp1 Component R1 - 1 1200.5 -800" {
		t.Fatalf("row 1 = %q", got)
	}
	if got := strings.Join(strings.Fields(lines[3]), " "); got != "trk1 Line - GND - - -" {
		t.Fatalf("row 2 (missing fields must print '-') = %q", got)
	}
}

func TestSelectionCmdJSON(t *testing.T) {
	var seen []string
	cfg := selectionFakeConnector(t, map[string]any{
		"ok": true, "context": selectionContext("schematic"),
		"result": map[string]any{
			"documentType": "schematic", "count": 1, "detail": true,
			"items": []any{map[string]any{"primitiveId": "c1", "primitiveType": "Component", "designator": "U1"}},
		},
	}, &seen)

	var out bytes.Buffer
	cmd := newSelectionCmd(cfg, &out, &out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("selection --json: %v\n%s", err, out.String())
	}
	var got struct {
		Context      map[string]any   `json:"context"`
		DocumentType string           `json:"documentType"`
		Count        int              `json:"count"`
		Items        []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if got.DocumentType != "schematic" || got.Count != 1 || len(got.Items) != 1 || got.Items[0]["primitiveId"] != "c1" {
		t.Fatalf("json = %+v", got)
	}
	if got.Context["documentUuid"] != "pcb-doc-1" || got.Context["projectUuid"] != "project-1" {
		t.Fatalf("context = %+v", got.Context)
	}
}

func TestSelectionCmdEmptySelectionIsNotAnError(t *testing.T) {
	var seen []string
	cfg := selectionFakeConnector(t, map[string]any{
		"ok": true, "context": selectionContext("pcb"),
		"result": map[string]any{"documentType": "pcb", "count": 0, "items": []any{}, "detail": true},
	}, &seen)

	var out bytes.Buffer
	cmd := newSelectionCmd(cfg, &out, &out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("empty selection must exit 0: %v", err)
	}
	if strings.TrimSpace(out.String()) != "no objects selected in pcb pcb-doc-1" {
		t.Fatalf("output = %q", out.String())
	}

	var jsonOut bytes.Buffer
	jsonCmd := newSelectionCmd(cfg, &jsonOut, &jsonOut)
	jsonCmd.SetArgs([]string{"--json"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatalf("empty selection --json: %v", err)
	}
	if !strings.Contains(jsonOut.String(), `"items": []`) || !strings.Contains(jsonOut.String(), `"count": 0`) {
		t.Fatalf("empty json must keep count 0 and an items array: %s", jsonOut.String())
	}
}

func TestSelectionCmdPassesActionErrorsThrough(t *testing.T) {
	var seen []string
	cfg := selectionFakeConnector(t, map[string]any{
		"ok": false, "context": selectionContext("pcb"),
		"error": map[string]any{
			"code":    "PRECONDITION_REFUSED",
			"message": "Selection read refused: page drift, the active project/document/tab changed while reading",
		},
	}, &seen)

	var out, errOut bytes.Buffer
	cmd := newSelectionCmd(cfg, &out, &errOut)
	cmd.SetArgs(nil)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("an ok=false selection.get must fail the command")
	}
	if !strings.Contains(err.Error(), "page drift") {
		t.Fatalf("connector error text was not passed through: %v", err)
	}
	if strings.Contains(out.String(), "selected") {
		t.Fatalf("no selection table may be printed on error: %q", out.String())
	}
}
