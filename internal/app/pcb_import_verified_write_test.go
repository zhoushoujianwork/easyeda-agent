package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPCBImportEntryPointsKeepUnverifiedResultAndStop(t *testing.T) {
	for _, result := range []string{
		`{"imported":true,"confirm":"dialog-open","verified":false,"partial":true}`,
		`{"imported":true,"confirm":"no-button","verified":false}`,
		`{"imported":true,"confirm":"no-dialog","verified":false}`,
		`{"imported":false,"confirm":"applied","verified":true}`,
		`{"imported":true,"confirm":"applied","verified":false,"partial":true,"duplicateUniqueIds":[{"uniqueId":"gge1","primitiveIds":["first","second"]}]}`,
		`{"imported":true,"confirm":"applied"}`,
	} {
		for _, entry := range []string{"command", "dispatch", "request"} {
			t.Run(entry+result, func(t *testing.T) {
				reply := `{"ok":true,"result":` + result + `}`
				cfg, state, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return reply })
				defer cleanup()
				var out, stderr bytes.Buffer
				var err error
				switch entry {
				case "command":
					cmd := newPcbCmd(cfg, &out, &stderr)
					cmd.SilenceErrors, cmd.SilenceUsage = true, true
					cmd.SetArgs([]string{"import-changes", "--window", "w1"})
					err = cmd.Execute()
				case "dispatch":
					err = dispatch(cfg, "pcb.import_changes", "w1", nil, &out, &stderr)
				case "request":
					var res *actionResult
					res, err = requestAction(cfg, "pcb.import_changes", "w1", nil)
					if res == nil || res.Result["confirm"] == nil {
						t.Fatalf("lost uncertain import: %+v", res)
					}
				}
				if err == nil || !strings.Contains(err.Error(), "import not verified") {
					t.Fatalf("accepted import: err=%v output=%s", err, out.String())
				}
				if entry != "request" && strings.TrimSpace(out.String()) != reply {
					t.Fatalf("original response lost: %s", out.String())
				}
				if calls := state.snapshot(); len(calls) != 1 || calls[0].Action != "pcb.import_changes" {
					t.Fatalf("must not backfill, repair, or repeat: %+v", calls)
				}
			})
		}
	}
}

func TestPCBImportApplyCannotVerifyRetryContinueOrPromptPastPartial(t *testing.T) {
	for _, entry := range []string{"action", "command"} {
		for _, mode := range []string{"default", "verify", "retry", "continue", "default-continue", "prompt"} {
			t.Run(entry+"/"+mode, func(t *testing.T) {
				cfg, state, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
					if call.Action == "pcb.import_changes" {
						return `{"ok":true,"result":{"imported":true,"confirm":"applied","verified":false,"partial":true,"duplicateUniqueIds":[{"uniqueId":"gge1","primitiveIds":["first","second"]}]}}`
					}
					return `{"ok":true,"result":{}}`
				})
				defer cleanup()
				step := playbookStep{ID: "uncertain-import", Action: "pcb.import_changes"}
				if entry == "command" {
					step.Action, step.Run = "", "pcb import-changes"
				}
				pb := &playbook{Version: 1}
				switch mode {
				case "verify":
					step.Verify = &verifyBlock{Action: "pcb.components.list"}
				case "retry":
					n := 1
					step.Retry = &n
				case "continue", "prompt":
					step.OnFail = mode
				case "default-continue":
					cont := true
					pb.Defaults.ContinueOnError = &cont
				}
				pb.Steps = []playbookStep{step, {ID: "dependent", Action: "pcb.line.create"}}
				var out, stderr bytes.Buffer
				journal := filepath.Join(t.TempDir(), "journal.jsonl")
				r := applyRunner{cfg: cfg, pb: pb, stdout: &out, stderr: &stderr, vars: map[string]string{}, window: "w1", yes: true, journalPath: journal, toIdx: 1}
				if err := r.execute(); err == nil || !strings.Contains(err.Error(), "import not verified") {
					t.Fatalf("bypassed partial import: %v", err)
				}
				calls := state.snapshot()
				if len(calls) != 1 || calls[0].Action != "pcb.import_changes" {
					t.Fatalf("unexpected verify, replay, or dependent action: %+v", calls)
				}
				if strings.Contains(stderr.String(), "continue anyway?") {
					t.Fatal("unverified import must not prompt to bypass")
				}
				data, err := os.ReadFile(journal)
				if err != nil || !bytes.Contains(data, []byte(`"status":"fail"`)) || bytes.Contains(data, []byte(`"status":"ok"`)) {
					t.Fatalf("lost failed journal: %s, %v", data, err)
				}
			})
		}
	}
}

func TestPCBImportAcceptsConfirmedVerifiedResult(t *testing.T) {
	cfg, _, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string {
		return `{"ok":true,"result":{"imported":true,"confirm":"applied","verified":true,"partial":false}}`
	})
	defer cleanup()
	var out, stderr bytes.Buffer
	cmd := newPcbCmd(cfg, &out, &stderr)
	cmd.SetArgs([]string{"import-changes", "--window", "w1", "--no-sync-attrs", "--no-sync-designators"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"verified":true`)) {
		t.Fatalf("lost success result: %s", out.String())
	}
}
