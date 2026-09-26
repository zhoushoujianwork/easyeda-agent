package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestPourApplyCannotVerifyRetryOrContinuePastUnverifiedBoundary(t *testing.T) {
	for _, entry := range []string{"action", "pcb pour", "pcb pour-fit", "pcb power-pour", "pcb power-planes"} {
		for _, mode := range []string{"default", "verify", "retry", "continue", "default-continue", "prompt"} {
			t.Run(entry+"/"+mode, func(t *testing.T) {
				creates := 0
				cfg, state, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
					if call.Action == "pcb.pour.create" {
						creates++
						if creates == 1 {
							return `{"ok":true,"result":{"primitiveId":"kept-pour","verified":false,"partial":true,"lineWidth":0.2}}`
						}
						return `{"ok":true,"result":{"primitiveId":"duplicate-pour","verified":true}}`
					}
					return stackupTestBoardResponse(call, `{"ok":true,"result":{"copperLayerCount":4}}`)
				})
				defer cleanup()
				step := playbookStep{ID: "bad-pour", Action: "pcb.pour.create", Payload: map[string]any{}}
				if entry != "action" {
					step.Action, step.Run = "", entry
					if entry == "pcb pour" {
						step.Flags = map[string]any{"net": "GND", "points": "[[0,0],[20,0],[20,20],[0,20]]"}
					}
				}
				pb := &playbook{Version: 1}
				switch mode {
				case "verify":
					step.Verify = &verifyBlock{Action: "pcb.pour.list"}
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
				if err == nil || !strings.Contains(err.Error(), "kept-pour") || creates != 1 {
					t.Fatalf("failure bypassed: err=%v creates=%d stdout=%s stderr=%s", err, creates, out.String(), stderr.String())
				}
				calls := state.snapshot()
				for i, call := range calls {
					if call.Action == "pcb.pour.create" && i != len(calls)-1 {
						t.Fatalf("action after failed boundary: %+v", calls[i+1:])
					}
				}
				if strings.Contains(stderr.String(), "continue anyway?") {
					t.Fatalf("must stop without asking to continue: %s", stderr.String())
				}
				log, err := os.ReadFile(journal)
				if err != nil || !bytes.Contains(log, []byte(`"status":"fail"`)) || bytes.Contains(log, []byte(`"status":"ok"`)) {
					t.Fatalf("journal lost failure: %s (%v)", log, err)
				}
			})
		}
	}
}

func TestPourCreationPathsRequireVerifiedBoundary(t *testing.T) {
	for _, tc := range []struct {
		result  string
		failure bool
	}{
		{`{"primitiveId":"kept-pour","poured":true}`, true},
		{`{"primitiveId":"kept-pour","verified":false,"lineWidth":0.2}`, true},
		{`{"primitiveId":"kept-pour","partial":true,"verified":true}`, true},
		{`{"primitiveId":"kept-pour","verified":true,"poured":false}`, false},
		{`{"primitiveId":"kept-pour","verified":true,"poured":true}`, false},
	} {
		for _, path := range []string{"dispatch", "request", "apply"} {
			t.Run(path+tc.result, func(t *testing.T) {
				response := `{"ok":true,"result":` + tc.result + `}`
				cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
				defer cleanup()
				var out, stderr bytes.Buffer
				var err error
				switch path {
				case "dispatch":
					err = dispatch(cfg, "pcb.pour.create", "w1", map[string]any{}, &out, &stderr)
					if strings.TrimSpace(out.String()) != response {
						t.Fatalf("lost original reply: %s", out.String())
					}
				case "request":
					var res *actionResult
					res, err = requestAction(cfg, "pcb.pour.create", "w1", map[string]any{})
					if res == nil || res.Result["primitiveId"] != "kept-pour" {
						t.Fatalf("lost created ID: %+v", res)
					}
				case "apply":
					r := applyRunner{cfg: cfg, window: "w1"}
					_, err = r.runAction("pcb.pour.create", map[string]any{}, time.Second)
				}
				if (err != nil) != tc.failure {
					t.Fatalf("err=%v; failure=%v", err, tc.failure)
				}
				if err != nil && !strings.Contains(err.Error(), "kept-pour") {
					t.Fatalf("failure must identify retained boundary: %v", err)
				}
				if len(calls.snapshot()) != 1 {
					t.Fatalf("unexpected replay: %+v", calls.snapshot())
				}
			})
		}
	}
}

func TestPourCompositeCommandsStopOnUnverifiedBoundary(t *testing.T) {
	for _, command := range []string{"pour-fit", "power-pour", "power-planes"} {
		t.Run(command, func(t *testing.T) {
			cfg, state, cleanup := newAutolayoutTestDaemon(t, func(_ int, c autolayoutTestCall) string {
				if c.Action == "pcb.pour.create" {
					return `{"ok":true,"result":{"primitiveId":"kept-pour","verified":false,"partial":true,"lineWidth":0.2,"differences":["lineWidth"]}}`
				}
				return stackupTestBoardResponse(c, `{"ok":true,"result":{"copperLayerCount":4}}`)
			})
			defer cleanup()
			var out, stderr bytes.Buffer
			cmd := newPcbCmd(cfg, &out, &stderr)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetArgs([]string{command, "--window", "w1"})
			if err := cmd.Execute(); err == nil {
				t.Fatalf("unverified pour accepted: %s", out.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("lost partial JSON: %s (%v)", out.String(), err)
			}
			if result["ok"] != false || !strings.Contains(out.String(), "kept-pour") || !strings.Contains(out.String(), "0.2") {
				t.Fatalf("lost failed boundary: %s", out.String())
			}
			mutates := map[string]bool{}
			for _, action := range protocol.AllActions() {
				mutates[action.Name] = action.Mutates
			}
			seen, count := false, 0
			for _, call := range state.snapshot() {
				if seen && mutates[call.Action] {
					t.Fatalf("dependent mutation after mismatch: %+v", call)
				}
				if call.Action == "pcb.pour.create" {
					seen = true
					count++
				}
			}
			if count != 1 {
				t.Fatalf("pour writes=%d; calls=%+v", count, state.snapshot())
			}
		})
	}
}
