package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestKnownBugAdvisoriesPreserveResultsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, domain string
		args         []string
		bug          string
	}{
		{"modify", "sch", []string{"modify", "--id", "c1", "--x", "320"}, "#256"},
		{"nc-clear", "sch", []string{"no-connect", "--designator", "R1", "--pin", "1", "--clear"}, "#257"},
		{"config", "pcb", []string{"config", "track", "--name", "rule", "--min", "7"}, "#258"},
	} {
		for _, response := range []string{
			`{"ok":true,"result":{"verified":true}}`,
			`{"ok":false,"error":{"code":"HOST_ERROR","message":"cmdKey"}}`,
			`{"ok":true,"result":{"verified":false}}`,
			`{"ok":true,"result":{"verified":true,"partial":true}}`,
			`{"ok":true,"result":{"verified":true,"partial":true,"notApplied":["1"]}}`,
		} {
			t.Run(tc.name+response, func(t *testing.T) {
				cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
				defer cleanup()
				var out, stderr bytes.Buffer
				cmd := newSchCmd(cfg, &out, &stderr)
				if tc.domain == "pcb" {
					cmd = newPcbCmd(cfg, &out, &stderr)
				}
				cmd.SetOut(&out)
				cmd.SetErr(&stderr)
				cmd.SilenceUsage = true // Match the real root command's JSON contract.
				cmd.SilenceErrors = true
				cmd.SetArgs(tc.args)
				err := cmd.Execute()
				wantFailure := response != `{"ok":true,"result":{"verified":true}}`
				if (err != nil) != wantFailure {
					t.Fatalf("err=%v; want failure=%v", err, wantFailure)
				}
				if strings.TrimSpace(out.String()) != response {
					t.Fatalf("raw JSON changed: %s", out.String())
				}
				if !strings.Contains(stderr.String(), "warning [bug "+tc.bug+"]") {
					t.Fatalf("missing advisory: %s", stderr.String())
				}
				if len(calls.snapshot()) != 1 {
					t.Fatalf("unexpected retry/extra action: %+v", calls.snapshot())
				}
			})
		}
	}
}

func TestKnownBugAdvisoriesDoNotAffectReadDryRunOrNCSet(t *testing.T) {
	for _, args := range [][]string{
		{"sch", "no-connect", "--designator", "R1", "--pin", "1"},
		{"sch", "modify", "--help"},
		{"pcb", "config", "get"},
		{"pcb", "config", "track", "--name", "rule", "--min", "7", "--dry-run"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cfg, _, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string {
				return `{"ok":true,"result":{"verified":true,"dryRun":true}}`
			})
			defer cleanup()
			var out, stderr bytes.Buffer
			cmd := newSchCmd(cfg, &out, &stderr)
			if args[0] == "pcb" {
				cmd = newPcbCmd(cfg, &out, &stderr)
			}
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs(args[1:])
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stderr.String(), "warning [bug") {
				t.Fatalf("unrelated warning: %s", stderr.String())
			}
		})
	}
}
