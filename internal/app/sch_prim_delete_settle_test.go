package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPrimDeleteCommandEmitsFinalResultAndAttempts(t *testing.T) {
	deletes := 0
	cfg, _, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		switch call.Action {
		case "schematic.components.list":
			return `{"ok":true,"result":{"components":[{"primitiveId":"gone","componentType":"part","designator":"C1"},{"primitiveId":"retry","componentType":"part","designator":"C2"}]}}`
		case "schematic.primitives.delete":
			deletes++
			if deletes == 1 {
				return `{"ok":true,"result":{"requested":2,"total":1,"deletedIds":{"components":["gone"]},"partial":true,"survivedTotal":1,"survived":{"components":["retry"]}},"warnings":["initial survivor"]}`
			}
			return `{"ok":true,"result":{"requested":1,"total":1,"deletedIds":{"components":["retry"]}}}`
		default:
			return `{"ok":true,"result":{}}`
		}
	})
	defer cleanup()
	var stdout, stderr bytes.Buffer
	cmd := newSchCmd(cfg, &stdout, &stderr)
	cmd.SetArgs([]string{"prim-delete", "--ids", "gone,retry", "--window", "w1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("one final JSON required: %v: %s", err, stdout.String())
	}
	r := got["result"].(map[string]any)
	if deletes != 2 || r["partial"] == true || r["total"] != float64(2) {
		t.Fatalf("stdout contradicts exit success: %v", got)
	}
	if got["warnings"] != nil {
		t.Fatalf("stale top-level warning: %v", got)
	}
	if len(r["attempts"].([]any)) != 2 {
		t.Fatalf("lost original attempts: %v", r)
	}
}

func primDeletePartialResult(ids ...string) *actionResult {
	list := make([]any, 0, len(ids))
	for _, id := range ids {
		list = append(list, id)
	}
	return &actionResult{OK: true, Result: map[string]any{
		"partial":       true,
		"survivedTotal": float64(len(ids)),
		"survived":      map[string]any{"components": list},
	}}
}

// 连接器删完**立刻** getAll 判存活,那一读可能采到尚未落定的快照 → 误报 survived。
// settle 复核必须把它翻过来,否则上层非零退出、人再删一遍,一轮轮空转。
func TestPrimDeleteSettleRecheckClearsAStaleSurvivorReport(t *testing.T) {
	var deleteCalls int
	cfg, _, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		if call.Action != "schematic.primitives.delete" {
			t.Errorf("unexpected action %q", call.Action)
			return `{"ok":true,"result":{}}`
		}
		deleteCalls++
		// 复核时那些 id 已经不在页上 → 连接器把它们归 notFound,不再 partial。
		return `{"ok":true,"result":{"deleted":{},"total":0,"requested":1,"notFound":["pid-1"]}}`
	})
	defer cleanup()

	var stderr bytes.Buffer
	out := primDeleteSettleRecheck(cfg, "w1", primDeletePartialResult("pid-1"), &stderr)
	if deleteCalls != 1 {
		t.Fatalf("recheck issued %d delete(s), want exactly one", deleteCalls)
	}
	if partial, _ := out.Result["partial"].(bool); partial {
		t.Fatalf("stale survivor report was not cleared: %+v", out.Result)
	}
	if err := failOnSurvivingPrimitives(out, &stderr); err != nil {
		t.Fatalf("settled recheck must exit clean, got %v", err)
	}
	if !strings.Contains(stderr.String(), "复核") {
		t.Fatalf("recheck must be explained on stderr:\n%s", stderr.String())
	}
}

// A real survivor must fail without inventing a root cause or a GUI fallback.
func TestPrimDeleteSettleRecheckKeepsFailingOnRealSurvivors(t *testing.T) {
	cfg, _, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		return `{"ok":true,"result":{"partial":true,"survivedTotal":1,"survived":{"components":["pid-1"]}}}`
	})
	defer cleanup()

	var stderr bytes.Buffer
	out := primDeleteSettleRecheck(cfg, "w1", primDeletePartialResult("pid-1"), &stderr)
	if partial, _ := out.Result["partial"].(bool); !partial {
		t.Fatalf("a real survivor must stay partial: %+v", out.Result)
	}
	if err := failOnSurvivingPrimitives(out, &stderr); err == nil {
		t.Fatal("a real survivor must still fail the command")
	}
	msg := stderr.String()
	if !strings.Contains(msg, "pid-1") || !strings.Contains(msg, "停止依赖步骤") ||
		!strings.Contains(msg, "easyeda sch list") || strings.Contains(msg, "几乎总是") {
		t.Fatalf("guidance must name the id and give a runnable next step:\n%s", msg)
	}
}

func TestPrimDeleteSettleRecheckIsANoOpOnACleanDelete(t *testing.T) {
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		t.Errorf("clean delete must not trigger a recheck round-trip (%q)", call.Action)
		return `{"ok":true,"result":{}}`
	})
	defer cleanup()

	clean := &actionResult{OK: true, Result: map[string]any{"total": float64(2)}}
	var stderr bytes.Buffer
	if out := primDeleteSettleRecheck(cfg, "w1", clean, &stderr); out != clean {
		t.Fatalf("clean result must pass through unchanged: %+v", out)
	}
	if len(daemon.snapshot()) != 0 {
		t.Fatalf("unexpected round-trips: %+v", daemon.snapshot())
	}
	if stderr.Len() != 0 {
		t.Fatalf("no survivors → no noise: %q", stderr.String())
	}
	if primDeleteSettleRecheck(cfg, "w1", nil, &stderr) != nil {
		t.Fatal("nil result must pass through")
	}
}

func TestPrimDeleteUnknownNeverRetriesOrUnregisters(t *testing.T) {
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		t.Errorf("unknown deletion must not be retried: %s", call.Action)
		return `{"ok":true,"result":{}}`
	})
	defer cleanup()
	res := primDeletePartialResult("kept")
	res.Result["verified"] = false
	res.Result["unverified"] = map[string]any{"components": []any{"unknown"}}
	res.Result["deletedIds"] = map[string]any{"components": []any{"gone"}}
	var stderr bytes.Buffer
	out := primDeleteSettleRecheck(cfg, "w1", res, &stderr)
	if len(daemon.snapshot()) != 0 || out != res {
		t.Fatal("unknown state was retried")
	}
	if err := failOnSurvivingPrimitives(out, &stderr); err == nil {
		t.Fatal("unknown state passed")
	}
	confirmed := confirmedDeletedIDSet(out.Result)
	if !confirmed["gone"] || confirmed["unknown"] || confirmed["kept"] {
		t.Fatalf("unsafe registry removal: %v", confirmed)
	}
}

func TestPrimDeleteMergePreservesKnownIDsAndRejectsIncompleteRetry(t *testing.T) {
	first := primDeletePartialResult("retry").Result
	first["requested"] = 2.0
	first["deletedIds"] = map[string]any{"components": []any{"gone"}}
	for _, second := range []map[string]any{{}, {"deletedIds": map[string]any{"components": []any{"unrelated"}}}} {
		out := mergePrimDeleteResults(first, second)
		if out["partial"] != true || out["verified"] != false {
			t.Fatalf("incomplete retry passed: %v", out)
		}
		ids := confirmedDeletedIDSet(out)
		if !ids["gone"] || ids["retry"] || ids["unrelated"] {
			t.Fatalf("bad confirmed IDs: %v", ids)
		}
	}
	for _, second := range []map[string]any{
		{"deletedIds": map[string]any{"components": []any{"retry"}}},
		{"notFound": []any{"retry"}},
	} {
		out := mergePrimDeleteResults(first, second)
		if out["partial"] == true || out["total"] != 2 {
			t.Fatalf("valid retry not merged: %v", out)
		}
		if ids := confirmedDeletedIDSet(out); len(ids) != 2 || !ids["gone"] || !ids["retry"] {
			t.Fatalf("lost removals: %v", ids)
		}
	}
}
