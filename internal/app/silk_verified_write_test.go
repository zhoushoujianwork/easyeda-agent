package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSilkAlignmentRequiresFreshVerifiedResultAcrossEntryPoints(t *testing.T) {
	for _, result := range []string{
		`{"aligned":51,"unresolved":0,"skipped":0}`, // legacy false-success shape
		`{"verified":false,"partial":true,"appliedIds":["kept-label"],"unresolved":0,"skipped":0}`,
		`{"verified":true,"partial":true,"unresolved":0,"skipped":0}`,
		`{"verified":true,"unresolved":1,"skipped":0}`,
		`{"verified":true,"unresolved":0,"skipped":1}`,
		`{"verified":true,"unresolved":0,"skipped":0}`,
	} {
		for _, entry := range []string{"dispatch", "capture", "request", "apply"} {
			t.Run(entry+result, func(t *testing.T) {
				response := `{"ok":true,"result":` + result + `}`
				cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
				defer cleanup()
				var out, stderr bytes.Buffer
				var err error
				switch entry {
				case "dispatch":
					err = dispatch(cfg, "pcb.silk.align", "w1", nil, &out, &stderr)
				case "capture":
					_, err = dispatchCapture(cfg, "pcb.silk.align", "w1", nil, &out)
				case "request":
					var res *actionResult
					res, err = requestAction(cfg, "pcb.silk.align", "w1", nil)
					if res == nil || res.Result == nil {
						t.Fatal("lost partial result")
					}
				case "apply":
					r := applyRunner{cfg: cfg, window: "w1"}
					var res any
					res, err = r.runAction("pcb.silk.align", nil, time.Second)
					if res == nil {
						t.Fatal("lost partial result")
					}
				}
				wantFailure := result != `{"verified":true,"unresolved":0,"skipped":0}`
				if (err != nil) != wantFailure {
					t.Fatalf("err=%v; result=%s", err, result)
				}
				if wantFailure {
					var typed *unverifiedSilkAlignmentError
					if !errors.As(err, &typed) {
						t.Fatalf("lost typed error: %v", err)
					}
				}
				if entry == "dispatch" || entry == "capture" {
					if strings.TrimSpace(out.String()) != response {
						t.Fatalf("lost exact response: %s", out.String())
					}
				}
				if len(calls.snapshot()) != 1 {
					t.Fatalf("blind replay: %+v", calls.snapshot())
				}
			})
		}
	}
}

func TestSilkApplyCannotRetryVerifyOrContinueAfterUnverifiedPlacement(t *testing.T) {
	for _, entry := range []string{"action", "pcb silk-align"} {
		for _, mode := range []string{"default", "verify", "retry", "continue", "default-continue", "prompt"} {
			t.Run(entry+"/"+mode, func(t *testing.T) {
				aligns := 0
				cfg, state, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
					if call.Action == "pcb.silk.align" {
						aligns++
						return `{"ok":true,"result":{"verified":false,"partial":true,"appliedIds":["kept-label"],"unresolved":0,"skipped":1}}`
					}
					return `{"ok":true,"result":{}}`
				})
				defer cleanup()
				step := playbookStep{ID: "bad-silk", Action: "pcb.silk.align", Payload: map[string]any{}}
				if entry != "action" {
					step.Action, step.Run = "", entry
				}
				pb := &playbook{Version: 1}
				switch mode {
				case "verify":
					step.Verify = &verifyBlock{Action: "pcb.silk.list"}
				case "retry":
					retries := 1
					step.Retry = &retries
				case "continue", "prompt":
					step.OnFail = mode
				case "default-continue":
					cont := true
					pb.Defaults.ContinueOnError = &cont
				}
				pb.Steps = []playbookStep{step, {ID: "dependent", Action: "pcb.line.create", Payload: map[string]any{}}}
				var out, stderr bytes.Buffer
				journal := filepath.Join(t.TempDir(), "journal.jsonl")
				r := applyRunner{cfg: cfg, stdout: &out, stderr: &stderr, pb: pb, vars: map[string]string{}, window: "w1", yes: true, journalPath: journal, toIdx: 1}
				err := r.execute()
				if err == nil || aligns != 1 || !strings.Contains(err.Error(), "not verified") {
					t.Fatalf("failure bypassed: %v; aligns=%d", err, aligns)
				}
				calls := state.snapshot()
				for i, call := range calls {
					if call.Action == "pcb.silk.align" && i != len(calls)-1 {
						t.Fatalf("dependent action: %+v", calls[i+1:])
					}
				}
				if strings.Contains(stderr.String(), "continue anyway?") {
					t.Fatalf("unexpected continuation: %s", stderr.String())
				}
				log, err := os.ReadFile(journal)
				if err != nil || !bytes.Contains(log, []byte(`"status":"fail"`)) || bytes.Contains(log, []byte(`"status":"ok"`)) {
					t.Fatalf("lost failure: %s (%v)", log, err)
				}
			})
		}
	}
}
