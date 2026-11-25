package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const verifiedSilkDeleteResult = `{"primitiveId":"s1","text":"REV A","layer":3,"deleted":true,"verified":true}`

func TestSilkDeleteCLIDispatchesOnlyPrimitiveID(t *testing.T) {
	cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string {
		return `{"ok":true,"result":` + verifiedSilkDeleteResult + `}`
	})
	defer cleanup()
	var out, stderr bytes.Buffer
	cmd := newPcbCmd(cfg, &out, &stderr)
	cmd.SetArgs([]string{"silk-delete", "--id", "s1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := calls.snapshot()
	if len(got) != 1 || got[0].Action != "pcb.silk.delete" || len(got[0].Payload) != 1 || got[0].Payload["primitiveId"] != "s1" {
		t.Fatalf("calls=%+v", got)
	}
}

func TestSilkDeleteCLIRequiresID(t *testing.T) {
	for _, args := range [][]string{{"silk-delete"}, {"silk-delete", "--id", ""}, {"silk-delete", "--id", "  "}} {
		cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string {
			return `{"ok":true,"result":` + verifiedSilkDeleteResult + `}`
		})
		var out, stderr bytes.Buffer
		cmd := newPcbCmd(cfg, &out, &stderr)
		cmd.SetArgs(args)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		err := cmd.Execute()
		cleanup()
		if err == nil || !strings.Contains(err.Error(), "--id is required") {
			t.Fatalf("args=%v err=%v", args, err)
		}
		if len(calls.snapshot()) != 0 {
			t.Fatalf("dispatched without an id: %+v", calls.snapshot())
		}
	}
}

func TestSilkDeleteUnverifiedResultFailsAcrossEntryPoints(t *testing.T) {
	results := []string{
		verifiedSilkDeleteResult,
		`{"primitiveId":"s1","text":"REV A","layer":3,"deleted":true,"verified":false,"partial":true}`,
		`{"primitiveId":"s1","text":"REV A","layer":3,"deleted":true,"verified":true,"partial":true}`,
		`{"primitiveId":"s1","text":"REV A","layer":3,"deleted":false,"verified":true}`,
		`{"primitiveId":"s1","text":"REV A","layer":3,"deleted":true}`,
		`{"text":"REV A","layer":3,"deleted":true,"verified":true}`,
		`{}`,
	}
	for _, result := range results {
		for _, entry := range []string{"dispatch", "pcb silk-delete", "capture", "request", "apply"} {
			t.Run(entry+result, func(t *testing.T) {
				response := `{"ok":true,"result":` + result + `}`
				cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
				defer cleanup()
				var out, stderr bytes.Buffer
				var err error
				payload := map[string]any{"primitiveId": "s1"}
				switch entry {
				case "dispatch":
					err = dispatch(cfg, "pcb.silk.delete", "w1", payload, &out, &stderr)
				case "pcb silk-delete":
					cmd := newPcbCmd(cfg, &out, &stderr)
					cmd.SetArgs([]string{"silk-delete", "--id", "s1"})
					cmd.SilenceUsage, cmd.SilenceErrors = true, true
					err = cmd.Execute()
				case "capture":
					_, err = dispatchCapture(cfg, "pcb.silk.delete", "w1", payload, &out)
				case "request":
					var res *actionResult
					res, err = requestAction(cfg, "pcb.silk.delete", "w1", payload)
					if res == nil || res.Result == nil {
						t.Fatal("lost result")
					}
				case "apply":
					r := applyRunner{cfg: cfg, window: "w1", stdout: &out}
					_, err = r.runAction("pcb.silk.delete", payload, time.Second)
				}
				wantFailure := result != verifiedSilkDeleteResult
				if (err != nil) != wantFailure {
					t.Fatalf("err=%v result=%s", err, result)
				}
				if wantFailure {
					var typed *unverifiedSilkDeleteError
					if !errors.As(err, &typed) || typed.rawResponse != response {
						t.Fatalf("missing typed error/raw evidence: %v", err)
					}
				}
				if got := calls.snapshot(); len(got) != 1 || got[0].Action != "pcb.silk.delete" {
					t.Fatalf("blind replay or wrong action: %+v", got)
				}
			})
		}
	}
}

func TestSilkDeleteApplyCannotRetryVerifyOrContinueAndJournalsRawEvidence(t *testing.T) {
	response := `{"ok":true,"result":{"primitiveId":"s1","text":"REV A","layer":3,"deleted":true,"verified":false,"partial":true}}`
	for _, entry := range []string{"action", "pcb silk-delete"} {
		for _, mode := range []string{"default", "verify", "retry", "continue"} {
			t.Run(entry+"/"+mode, func(t *testing.T) {
				deletes := 0
				cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
					if call.Action == "pcb.silk.delete" {
						deletes++
						return response
					}
					return `{"ok":true,"result":{}}`
				})
				defer cleanup()
				step := playbookStep{ID: "bad-delete", Action: "pcb.silk.delete", Payload: map[string]any{"primitiveId": "s1"}}
				if entry != "action" {
					step.Action, step.Run = "", entry
					step.Flags = map[string]any{"id": "s1"}
				}
				switch mode {
				case "verify":
					step.Verify = &verifyBlock{Action: "pcb.silk.list"}
				case "retry":
					retries := 1
					step.Retry = &retries
				case "continue":
					step.OnFail = mode
				}
				pb := &playbook{Version: 1, Steps: []playbookStep{step, {ID: "dependent", Action: "pcb.line.create", Payload: map[string]any{}}}}
				var out, stderr bytes.Buffer
				journal := filepath.Join(t.TempDir(), "journal.jsonl")
				r := applyRunner{cfg: cfg, stdout: &out, stderr: &stderr, pb: pb, vars: map[string]string{}, window: "w1", yes: true, journalPath: journal, toIdx: 1, quiet: true}
				err := r.execute()
				var typed *unverifiedSilkDeleteError
				if !errors.As(err, &typed) || deletes != 1 {
					t.Fatalf("failure bypassed: %v; deletes=%d", err, deletes)
				}
				for _, call := range calls.snapshot() {
					if call.Action == "pcb.line.create" || call.Action == "pcb.silk.list" {
						t.Fatalf("dependent/verify action ran after unverified delete: %+v", calls.snapshot())
					}
				}
				log, err := os.ReadFile(journal)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, line := range bytes.Split(log, []byte("\n")) {
					var item journalEntry
					if json.Unmarshal(line, &item) == nil && item.Status == "fail" {
						found = item.RawResponse == response
					}
				}
				if !found || bytes.Contains(log, []byte(`"status":"ok"`)) {
					t.Fatalf("lost exact failed response: %s", log)
				}
			})
		}
	}
}

func TestSilkDeleteRawCallFailsOnUnverifiedResult(t *testing.T) {
	for _, tc := range []struct {
		result  string
		wantErr bool
	}{
		{verifiedSilkDeleteResult, false},
		{`{"primitiveId":"s1","text":"REV A","layer":3,"deleted":true,"verified":false,"partial":true}`, true},
	} {
		cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return `{"ok":true,"result":` + tc.result + `}` })
		var out, stderr bytes.Buffer
		cmd := newCallCmd(cfg, &out, &stderr)
		cmd.SetArgs([]string{"pcb.silk.delete", "--payload", `{"primitiveId":"s1"}`})
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		err := cmd.Execute()
		cleanup()
		if (err != nil) != tc.wantErr {
			t.Fatalf("result=%s err=%v", tc.result, err)
		}
		if got := calls.snapshot(); len(got) != 1 || got[0].Action != "pcb.silk.delete" || got[0].Payload["primitiveId"] != "s1" {
			t.Fatalf("calls=%+v", got)
		}
		if !strings.Contains(out.String(), tc.result) {
			t.Fatalf("raw response not echoed: %s", out.String())
		}
	}
}
