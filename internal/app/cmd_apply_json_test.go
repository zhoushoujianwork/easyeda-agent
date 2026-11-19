package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --json gives a caller one structured result on stdout: a dry run that
// passed, or a refusal before any step ran. Progress stays on stderr.
func TestApplyJSONReportsDryRunAndRefusal(t *testing.T) {
	pb := playbook{Version: 1, RequireFullExecution: true, Meta: playbookMeta{Name: "guarded", Doc: "doc", Project: "project"}, Steps: []playbookStep{{ID: "read", Action: "schematic.components.list"}, {ID: "save", Action: "schematic.save"}}}
	raw, _ := json.Marshal(pb)
	path := filepath.Join(t.TempDir(), "apply.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := Run([]string{"sch", "apply", path, "--dry-run", "--json"}, &out, &errs); code != 0 {
		t.Fatalf("dry run failed: %d %s", code, errs.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON result: %q", out.String())
	}
	if got["ok"] != true || got["dryRun"] != true || got["steps"] != float64(2) || got["name"] != "guarded" {
		t.Fatalf("unexpected dry-run result: %v", got)
	}
	if !strings.Contains(errs.String(), "dry-run: preflight passed") {
		t.Fatalf("progress should move to stderr: %q", errs.String())
	}
	out.Reset()
	errs.Reset()
	if code := Run([]string{"sch", "apply", path, "--dry-run", "--json", "--resume"}, &out, &errs); code == 0 {
		t.Fatal("guarded resume must be refused")
	}
	got = nil
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("refusal is not one JSON result: %q", out.String())
	}
	e, _ := got["error"].(map[string]any)
	if got["ok"] != false || e["code"] != "PRECONDITION_REFUSED" || !strings.Contains(e["message"].(string), "full execution") {
		t.Fatalf("unexpected refusal: %v", got)
	}
	// Without --json the human output is unchanged.
	out.Reset()
	errs.Reset()
	if code := Run([]string{"sch", "apply", path, "--dry-run"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "dry-run: preflight passed") {
		t.Fatalf("plain dry run changed: %d %q %q", code, out.String(), errs.String())
	}
}
