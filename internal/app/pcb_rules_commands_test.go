package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestPcbCheckRuleUnitErrorStopsBeforeGeometry(t *testing.T) {
	for _, strict := range []bool{false, true} {
		// This mock reports any unexpected request; an invalid unit must cause only
		// a rule read, never geometry checks, reports or mutation actions.
		cfg := captureFixture(t, func(action string, response map[string]any) {
			if action != "pcb.drc.rules" {
				t.Errorf("continued after ambiguous rule: %s", action)
			}
			result := ceshiRuleResult()
			delete(mnav(result, "rules", "config", "Physics", "Track", "copperThickness1oz").(map[string]any), "unit")
			response["result"] = result
		})
		var out, stderr bytes.Buffer
		err := runPcbCheck(cfg, "window", 3, nil, strict, true, &out, &stderr)
		if err == nil || !strings.Contains(err.Error(), "Track.copperThickness1oz.unit") {
			t.Fatalf("err=%v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("published report despite unreadable units: %s", out.String())
		}
	}
}
func TestBoardSnapshotRuleUnitErrorIsUnknown(t *testing.T) {
	cfg := captureFixture(t, func(action string, response map[string]any) {
		if action == "pcb.drc.rules" {
			result := ceshiRuleResult()
			mnav(result, "rules", "config", "Spacing", "Safe Spacing", "copperThickness1oz").(map[string]any)["unit"] = "um"
			response["result"] = result
		}
	})
	snap, err := fetchBoardSnapshot(cfg, "window", boardSnapshotOpts{withRules: true})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Rules != nil || snap.Availability["rules"] != "unknown" {
		t.Fatalf("rules published as measured: %+v", snap)
	}
	if !strings.Contains(strings.Join(snap.Partial, " "), "Safe Spacing.copperThickness1oz.unit") {
		t.Fatalf("missing precise error: %v", snap.Partial)
	}
}

func TestRouteShortRuleUnitErrorCannotBeBypassedByWidth(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		cfg := newClearanceCheckFixture(t, 20, func(results map[string]map[string]any) {
			mnav(results["pcb.drc.rules"], "rules", "config", "Spacing", "Safe Spacing", "copperThickness1oz").(map[string]any)["unit"] = "unknown"
		})
		var out, stderr bytes.Buffer
		cmd := newPcbCmd(cfg, &out, &stderr)
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		args := []string{"route-short", "--window", "w1", "--width", "10"}
		if dryRun {
			args = append(args, "--dry-run")
		}
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "Safe Spacing.copperThickness1oz.unit") {
			t.Fatalf("dryRun=%v err=%v", dryRun, err)
		}
		if out.Len() != 0 {
			t.Fatalf("plan published despite unit error: %s", out.String())
		}
	}
}
func TestFetchPcbRulesRequestErrorKeepsFallback(t *testing.T) {
	cfg := captureFixture(t, func(action string, response map[string]any) {
		if action != "pcb.drc.rules" {
			t.Errorf("unexpected action %s", action)
		}
		response["ok"] = false
		response["error"] = map[string]any{"code": "EDA_API_ERROR", "message": "injected rules request failure"}
	})
	got, err := fetchPcbRules(cfg, "window")
	if err != nil || got != defaultPcbRules() {
		t.Fatalf("request fallback changed: %+v err=%v", got, err)
	}
}
