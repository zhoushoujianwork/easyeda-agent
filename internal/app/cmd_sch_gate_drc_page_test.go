package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func drcPageFixture(onP2Level string) drcReport {
	v := func(level, rule string, sheets ...string) drcViolation {
		prims := []map[string]any{}
		for _, s := range sheets {
			if s == "-" {
				prims = append(prims, map[string]any{"name": "x"})
				continue
			}
			prims = append(prims, map[string]any{"name": "x", "sheet": s})
		}
		raw, _ := json.Marshal(map[string]any{"rule": rule, "primitives": prims})
		return drcViolation{Level: level, Rule: rule, Raw: raw}
	}
	// Live ceshi shape: six items on P1 (floating pins, dangling net), one on P2.
	vs := []drcViolation{v("warn", "SHOWDANDLING", "P1"), v(onP2Level, "ATTRMATCHCODE", "P2")}
	for i := 0; i < 5; i++ {
		vs = append(vs, v("warn", "CHECKFLOATPIN", "P1", "P1"))
	}
	return drcReport{Passed: false, Strict: true, DetailsAvailable: true, Violations: vs,
		Summary: &drcSummary{Warn: 7, Total: 7}}
}

func TestDrcPageScopeBlocksOnlyThisPage(t *testing.T) {
	rep := drcPageFixture("warn")
	if got := drcBlockingReasons(rep, true, "P2"); len(got) != 1 || !strings.Contains(got[0], "1 warn-level DRC violation on this page") {
		t.Fatalf("P2 must block on its own item only: %v", got)
	}
	if got := drcBlockingReasons(rep, false, "P2"); len(got) != 0 {
		t.Fatalf("non-strict warn on page must not block: %v", got)
	}
	if got := drcBlockingReasons(rep, true, "P3"); len(got) != 0 {
		t.Fatalf("page with no items must pass despite other pages: %v", got)
	}
	if got := drcBlockingReasons(rep, true, ""); len(got) < 2 {
		t.Fatalf("unscoped keeps the native project verdict: %v", got)
	}
	// Incomplete detail or unknown severity: fall back to the native verdict.
	short := rep
	short.Violations = rep.Violations[:3]
	if got := drcBlockingReasons(short, true, "P3"); len(got) == 0 {
		t.Fatal("incomplete detail must not pass")
	}
	odd := drcPageFixture("mystery")
	if got := drcBlockingReasons(odd, true, "P2"); len(got) == 0 {
		t.Fatal("unknown severity must not pass")
	}
	// An item without sheet evidence counts as on the page.
	noSheet := drcPageFixture("warn")
	noSheet.Violations[0] = drcViolation{Level: "warn", Raw: json.RawMessage(`{"primitives":[{"name":"x"}]}`)}
	if got := drcBlockingReasons(noSheet, true, "P3"); len(got) != 1 {
		t.Fatalf("unknown sheet must block: %v", got)
	}
}
