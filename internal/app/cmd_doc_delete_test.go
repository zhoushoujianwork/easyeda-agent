package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/spf13/cobra"
)

func runCapturedCmd(t *testing.T, build func(*appConfig) *cobra.Command, args ...string) (*capturedRequest, error) {
	t.Helper()
	cfg, cap, cleanup := newCapturingDaemon(t)
	t.Cleanup(cleanup)
	var stdout, stderr bytes.Buffer
	cmd := build(cfg)
	cmd.SetArgs(args)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	return cap, cmd.Execute()
}

// board delete forwards --dry-run / --keep-documents and omits them when unset,
// so a plain delete removes the Board AND its documents.
func TestBoardDelete_WiresDryRunAndKeepDocuments(t *testing.T) {
	board := func(cfg *appConfig) *cobra.Command { return newBoardCmd(cfg, &bytes.Buffer{}, &bytes.Buffer{}) }

	cap, err := runCapturedCmd(t, board, "delete", "--name", "Board1_1", "--dry-run", "--keep-documents")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cap.action != "board.delete" || cap.payload["name"] != "Board1_1" || cap.payload["dryRun"] != true || cap.payload["keepDocuments"] != true {
		t.Fatalf("got action=%q payload=%v", cap.action, cap.payload)
	}

	cap, err = runCapturedCmd(t, board, "delete", "--name", "Board1_1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := cap.payload["dryRun"]; ok {
		t.Error("dryRun must be omitted when --dry-run is unset")
	}
	if _, ok := cap.payload["keepDocuments"]; ok {
		t.Error("keepDocuments must be omitted when --keep-documents is unset")
	}
}

// pcb doc-delete needs the exact identity pair before any daemon call.
func TestPcbDocDelete_RequiresUUIDAndExpectedName(t *testing.T) {
	pcb := func(cfg *appConfig) *cobra.Command { return newPcbCmd(cfg, &bytes.Buffer{}, &bytes.Buffer{}) }
	for _, args := range [][]string{
		{"doc-delete", "--uuid", "c0d8d3b9d38f5d76"},
		{"doc-delete", "--expected-name", "PCB1_1"},
	} {
		cap, err := runCapturedCmd(t, pcb, args...)
		if err == nil {
			t.Fatalf("%v: expected an error", args)
		}
		if cap.action != "" {
			t.Fatalf("%v: dispatched %q despite missing identity", args, cap.action)
		}
	}

	cap, err := runCapturedCmd(t, pcb, "doc-delete", "--uuid", "c0d8d3b9d38f5d76", "--expected-name", "PCB1_1", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cap.action != "pcb.document.delete" || cap.payload["uuid"] != "c0d8d3b9d38f5d76" ||
		cap.payload["expectedName"] != "PCB1_1" || cap.payload["dryRun"] != true {
		t.Fatalf("got action=%q payload=%v", cap.action, cap.payload)
	}
}

func stubTreeRefresh(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	old := refreshProjectTree
	refreshProjectTree = func(*appConfig, string) (map[string]any, error) {
		calls++
		if err != nil {
			return nil, err
		}
		return map[string]any{"reloaded": true, "documentUuid": "44a50c47650bccd0"}, nil
	}
	t.Cleanup(func() { refreshProjectTree = old })
	return &calls
}

func runDeleteWithRefresh(t *testing.T, action, result string, payload map[string]any, refresh bool) (map[string]any, error) {
	t.Helper()
	cfg, _, cleanup := newAutolayoutTestDaemon(t, func(int, autolayoutTestCall) string {
		return `{"ok":true,"result":` + result + `}`
	})
	defer cleanup()
	var out, stderr bytes.Buffer
	err := dispatchDocumentDelete(cfg, action, "", payload, refresh, &out, &stderr)
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if jerr := json.Unmarshal(out.Bytes(), &envelope); jerr != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", jerr, out.String())
	}
	return envelope.Result, err
}

// A verified delete reloads the page once and reports it; a failed reload is
// reported separately and leaves the verified delete successful.
func TestDocumentDelete_RefreshesTreeOnlyAfterVerifiedDelete(t *testing.T) {
	verified := `{"uuid":"c0d8d3b9d38f5d76","deleted":true,"verified":true,"alsoDeleted":[]}`
	for _, action := range []string{"board.delete", "pcb.document.delete"} {
		calls := stubTreeRefresh(t, nil)
		res, err := runDeleteWithRefresh(t, action, verified, map[string]any{"uuid": "x"}, true)
		if err != nil || *calls != 1 || res["treeRefreshed"] != true || res["deleted"] != true {
			t.Fatalf("%s: err=%v calls=%d result=%v", action, err, *calls, res)
		}

		calls = stubTreeRefresh(t, errors.New("save refused"))
		res, err = runDeleteWithRefresh(t, action, verified, map[string]any{"uuid": "x"}, true)
		report, _ := res["treeRefresh"].(map[string]any)
		if err != nil || *calls != 1 || res["treeRefreshed"] != false || report["error"] != "save refused" {
			t.Fatalf("%s refresh failure: err=%v calls=%d result=%v", action, err, *calls, res)
		}

		calls = stubTreeRefresh(t, nil)
		res, err = runDeleteWithRefresh(t, action, verified, map[string]any{"uuid": "x"}, false)
		report, _ = res["treeRefresh"].(map[string]any)
		if err != nil || *calls != 0 || res["treeRefreshed"] != false || report["skipped"] == nil {
			t.Fatalf("%s --no-tree-refresh: err=%v calls=%d result=%v", action, err, *calls, res)
		}
	}
}

// Dry runs, unbind-only and partial deletes never reload the editor.
func TestDocumentDelete_NoRefreshForDryRunKeepOrPartial(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		payload      map[string]any
		wantErr      bool
	}{
		{"dry-run", `{"dryRun":true,"wouldDelete":[]}`, map[string]any{"dryRun": true}, false},
		{"keep-documents", `{"boardDeleted":true,"verified":true}`, map[string]any{"keepDocuments": true}, false},
		{"partial", `{"boardDeleted":true,"partial":true,"verified":false}`, map[string]any{}, true},
	} {
		calls := stubTreeRefresh(t, nil)
		res, err := runDeleteWithRefresh(t, "board.delete", tc.result, tc.payload, true)
		if (err != nil) != tc.wantErr || *calls != 0 || res["treeRefresh"] != nil {
			t.Fatalf("%s: err=%v calls=%d result=%v", tc.name, err, *calls, res)
		}
	}
}

// board new needs the exact project and forwards it with the optional name.
func TestBoardNew_RequiresProjectAndWiresPayload(t *testing.T) {
	build := func(cfg *appConfig) *cobra.Command { return newBoardCmd(cfg, &bytes.Buffer{}, &bytes.Buffer{}) }
	cap, err := runCapturedCmd(t, build, "new")
	if err == nil || cap.action != "" {
		t.Fatalf("expected a refusal without --project, got err=%v action=%q", err, cap.action)
	}

	cfg, cap, cleanup := newCapturingDaemon(t)
	defer cleanup()
	cfg.project = "72880e75d3be4601a20dc1ec3764227b"
	cmd := newBoardCmd(cfg, &bytes.Buffer{}, &bytes.Buffer{})
	cmd.SetArgs([]string{"new", "--name", "主板"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cap.action != "board.new" || cap.payload["projectUuid"] != cfg.project || cap.payload["name"] != "主板" {
		t.Fatalf("got action=%q payload=%v", cap.action, cap.payload)
	}
}

func TestBoardRenameHostRefusalFailsDispatch(t *testing.T) {
	for _, tc := range []struct {
		result  string
		failure bool
	}{{`{"ok":false}`, true}, {`{}`, true}, {`{"ok":true}`, false}} {
		response := `{"ok":true,"result":` + tc.result + `}`
		cfg, _, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
		err := dispatch(cfg, "board.rename", "", map[string]any{}, io.Discard, io.Discard)
		cleanup()
		if (err != nil) != tc.failure {
			t.Fatalf("%s: err=%v", tc.result, err)
		}
	}
}

// pcb rip-up: only an absent --net means "all routing"; an empty one is refused
// before any daemon call instead of widening into a whole-board rip-up.
func TestPcbRipUp_EmptyNetRefusedNotWidenedToAll(t *testing.T) {
	pcb := func(cfg *appConfig) *cobra.Command { return newPcbCmd(cfg, &bytes.Buffer{}, &bytes.Buffer{}) }
	for _, args := range [][]string{
		{"rip-up", "--net", ""},
		{"rip-up", "--net", ","},
		{"rip-up", "--net", "GND", "--net", " "},
	} {
		cap, err := runCapturedCmd(t, pcb, args...)
		if err == nil {
			t.Fatalf("%q: expected an error", args)
		}
		if cap.action != "" {
			t.Fatalf("%q: dispatched %q despite an empty net", args, cap.action)
		}
	}
	cap, err := runCapturedCmd(t, pcb, "rip-up", "--net", "GND,VBAT")
	if err != nil || cap.action != "pcb.route.rip_up" {
		t.Fatalf("scoped rip-up: action=%q err=%v", cap.action, err)
	}
	if got, ok := cap.payload["net"].([]any); !ok || len(got) != 2 || got[0] != "GND" || got[1] != "VBAT" {
		t.Fatalf("scoped rip-up payload: %v", cap.payload)
	}
	cap, err = runCapturedCmd(t, pcb, "rip-up")
	if err != nil || cap.action != "pcb.route.rip_up" {
		t.Fatalf("all rip-up: action=%q err=%v", cap.action, err)
	}
	if _, ok := cap.payload["net"]; ok {
		t.Fatalf("all rip-up must not send a net filter: %v", cap.payload)
	}
}
