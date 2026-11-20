package app

import (
	"bytes"
	"strings"
	"testing"
)

// The gate's whole point is that "which checkers, in what order, whose exit code
// counts" stops being re-decided per run. These tests pin that contract.

func TestResolveGateStagesDefaultsToTheFullFixedPipeline(t *testing.T) {
	run, skipped, err := resolveGateStages("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"layout-lint", "clusters", "check", "bridge-check", "drc"}
	if strings.Join(run, ",") != strings.Join(want, ",") {
		t.Fatalf("pipeline order changed: got %v want %v", run, want)
	}
	if len(skipped) != 0 {
		t.Fatalf("nothing should be skipped by default, got %v", skipped)
	}
}

func TestResolveGateStagesOnlyKeepsPipelineOrderNotArgumentOrder(t *testing.T) {
	// Arguments deliberately reversed: the report must always read as the same
	// fixed pipeline, otherwise two runs of the same board look different.
	run, skipped, err := resolveGateStages("drc,layout-lint", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(run, ",") != "layout-lint,drc" {
		t.Fatalf("got %v, want pipeline order [layout-lint drc]", run)
	}
	if strings.Join(skipped, ",") != "clusters,check,bridge-check" {
		t.Fatalf("excluded stages wrong: %v", skipped)
	}
}

func TestResolveGateStagesSkip(t *testing.T) {
	run, skipped, err := resolveGateStages("", "drc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(run, ",") != "layout-lint,clusters,check,bridge-check" {
		t.Fatalf("got %v", run)
	}
	if strings.Join(skipped, ",") != "drc" {
		t.Fatalf("got %v", skipped)
	}
}

func TestResolveGateStagesRejectsUnknownName(t *testing.T) {
	// A typo'd --only must never silently gate on fewer checks than asked.
	if _, _, err := resolveGateStages("layout-lnt", ""); err == nil {
		t.Fatal("a misspelled stage must be an error, not a silent no-op")
	}
	if _, _, err := resolveGateStages("", "drcc"); err == nil {
		t.Fatal("a misspelled --skip stage must be an error")
	}
}

func TestResolveGateStagesRejectsOnlyPlusSkipAndEmptySelection(t *testing.T) {
	if _, _, err := resolveGateStages("check", "drc"); err == nil {
		t.Fatal("--only with --skip must be rejected")
	}
	if _, _, err := resolveGateStages("", "layout-lint,clusters,check,bridge-check,drc"); err == nil {
		t.Fatal("skipping every stage must be an error, not an empty pass")
	}
}

func TestGradeGateReportPassWhenEveryStagePasses(t *testing.T) {
	rep := gateReport{Stages: []gateStage{
		{Name: "layout-lint", Status: gateStatusPass},
		{Name: "check", Status: gateStatusPass},
	}}
	gradeGateReport(&rep)
	if !rep.OK || rep.Verdict != "pass" {
		t.Fatalf("got ok=%v verdict=%q", rep.OK, rep.Verdict)
	}
	if len(rep.Blockers) != 0 {
		t.Fatalf("unexpected blockers: %v", rep.Blockers)
	}
}

func TestGradeGateReportWarningsDoNotBlock(t *testing.T) {
	rep := gateReport{Stages: []gateStage{
		{Name: "layout-lint", Status: gateStatusPass, Warnings: 3, Summary: "3 tight"},
	}}
	gradeGateReport(&rep)
	if !rep.OK || rep.Verdict != "pass" {
		t.Fatalf("advisory findings must not gate: ok=%v verdict=%q", rep.OK, rep.Verdict)
	}
	if len(rep.Warnings) != 1 {
		t.Fatalf("warnings should still be surfaced: %v", rep.Warnings)
	}
}

func TestGradeGateReportFailWhenAStageHasBlockingFindings(t *testing.T) {
	rep := gateReport{Stages: []gateStage{
		{Name: "layout-lint", Status: gateStatusPass},
		{Name: "bridge-check", Status: gateStatusFail, Errors: 2, Summary: "2 bridge(short)"},
	}}
	gradeGateReport(&rep)
	if rep.OK || rep.Verdict != "fail" {
		t.Fatalf("got ok=%v verdict=%q", rep.OK, rep.Verdict)
	}
	if len(rep.Blockers) != 1 || !strings.Contains(rep.Blockers[0], "bridge-check") {
		t.Fatalf("blockers wrong: %v", rep.Blockers)
	}
}

func TestGradeGateReportBlockedOutranksFail(t *testing.T) {
	// This is the distinction the audit log said agents were missing: a checker
	// that could not RUN must not be reported as a broken board — otherwise the
	// agent "fixes" a schematic that was never judged.
	rep := gateReport{Stages: []gateStage{
		{Name: "layout-lint", Status: gateStatusFail, Errors: 1, Summary: "1 overlap"},
		{Name: "check", Status: gateStatusError, Error: "no EasyEDA connector is available"},
		{Name: "drc", Status: gateStatusSkipped},
	}}
	gradeGateReport(&rep)
	if rep.Verdict != "blocked" {
		t.Fatalf("a stage that could not run must win the verdict, got %q", rep.Verdict)
	}
	if rep.OK {
		t.Fatal("blocked must not be ok")
	}
	joined := strings.Join(rep.Blockers, " | ")
	if !strings.Contains(joined, "没能跑起来") || !strings.Contains(joined, "connector") {
		t.Fatalf("the infra cause must reach the blockers list: %v", rep.Blockers)
	}
}

func TestGradeGateReportErrorStageContributesNoWarnings(t *testing.T) {
	// A stage that never ran has no findings to report — counting its zeroed
	// fields as "clean" would understate what is unknown.
	rep := gateReport{Stages: []gateStage{
		{Name: "check", Status: gateStatusError, Error: "boom", Warnings: 7},
	}}
	gradeGateReport(&rep)
	if len(rep.Warnings) != 0 {
		t.Fatalf("a stage that could not run must not emit warnings: %v", rep.Warnings)
	}
}

func TestRenderGateReportShowsVerdictStagesAndPrescribedNextStep(t *testing.T) {
	rep := gateReport{Stages: []gateStage{
		{Name: "layout-lint", Status: gateStatusFail, Errors: 2, Summary: "2 overlap, 0 pin-coincidence",
			BlockingReasons: []string{"2 overlap"}},
		{Name: "check", Status: gateStatusPass, Summary: "0 finding(s)"},
		{Name: "drc", Status: gateStatusSkipped, Summary: "被 --only/--skip 排除"},
	}}
	gradeGateReport(&rep)
	var buf bytes.Buffer
	renderGateReport(rep, &buf)
	out := buf.String()
	for _, want := range []string{"FAIL", "layout-lint", "SKIP", "阻塞项", "下一步"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
	// The prescribed next action must travel with the failure — the whole reason
	// agents invented four different next steps was that nothing prescribed one.
	if !strings.Contains(out, "autolayout") {
		t.Fatalf("layout-lint failure must carry its fix path:\n%s", out)
	}
}

func TestRenderGateReportBlockedExplainsItIsNotTheBoard(t *testing.T) {
	rep := gateReport{Stages: []gateStage{
		{Name: "layout-lint", Status: gateStatusError, Error: "no connector"},
	}}
	gradeGateReport(&rep)
	var buf bytes.Buffer
	renderGateReport(rep, &buf)
	out := buf.String()
	if !strings.Contains(out, "BLOCKED") {
		t.Fatalf("verdict missing:\n%s", out)
	}
	// The 146 blind NO_CONNECTOR retries in the audit log are exactly what this
	// guidance is for: check health/doc first, do not retry other checkers.
	for _, want := range []string{"不是「板子有问题」", "health", "doc switch"} {
		if !strings.Contains(out, want) {
			t.Fatalf("blocked guidance missing %q:\n%s", want, out)
		}
	}
}

// ── advice must follow the REASON, never the stage name ──────────────────
//
// Real-machine regression (2026-08-04, 示例工程_快速入门 under --strict): the
// stage-keyed advice table told the agent to "拆掉真短路" on a page with 0
// bridges (it failed on --strict orphan stubs) and to "重排几何" with 0
// overlaps. Advice pointing at a problem the board does not have is worse than
// none — it is the phantom-chasing this gate exists to prevent.

func TestGateAdviceFollowsTheBlockingReasonNotTheStageName(t *testing.T) {
	st := gateStage{
		Name:            "bridge-check",
		Status:          gateStatusFail,
		BlockingReasons: []string{"11 orphan-stub (--strict)"},
	}
	advice := strings.Join(gateAdviceFor(st), " | ")
	if strings.Contains(advice, "真短路") {
		t.Fatalf("0 bridges must never yield short-circuit advice:\n%s", advice)
	}
	if !strings.Contains(advice, "孤儿桩") {
		t.Fatalf("orphan-stub advice missing:\n%s", advice)
	}
}

func TestGateAdviceForUnprovenPinsBlamesTheConnectorNotTheCircuit(t *testing.T) {
	// The real failure on a clean official example board: 34 unproven pins
	// because connector 0.17.3 does not send the pinsAvailable contract. Telling
	// the agent to move parts around here would be actively harmful.
	st := gateStage{
		Name:            "layout-lint",
		Status:          gateStatusFail,
		BlockingReasons: []string{"34 unproven pin geometry (--strict;连接器未给 pinsAvailable 契约)"},
	}
	advice := strings.Join(gateAdviceFor(st), " | ")
	if strings.Contains(advice, "重排") || strings.Contains(advice, "挪位") {
		t.Fatalf("a provenance failure must not be treated as a placement defect:\n%s", advice)
	}
	if !strings.Contains(advice, "升级连接器") {
		t.Fatalf("advice must point at the connector:\n%s", advice)
	}
}

func TestGateAdviceIsEmptyWhenNothingBlocked(t *testing.T) {
	if a := gateAdviceFor(gateStage{Name: "drc", Status: gateStatusPass}); len(a) != 0 {
		t.Fatalf("a passing stage must prescribe nothing, got %v", a)
	}
}

func TestGateAdviceRulesCoverEveryReasonTheStagesCanEmit(t *testing.T) {
	// Every phrase a stage builder can put into BlockingReasons must match a
	// rule, or that failure silently ships with no prescribed next step.
	emitted := []string{
		"3 overlap", "1 pin-coincidence",
		"2 tight-spacing (--strict)", "4 off-grid anchor (--strict)",
		"1 zone-violation (--strict)", "2 component without bbox (--strict)",
		"5 unchecked pin geometry (--strict)",
		"34 unproven pin geometry (--strict;连接器未给 pinsAvailable 契约)",
		"1 invalid geometry value (--strict)",
		"zone-check unavailable (--strict): no sheet",
		"7 个 error/fatal finding: floating-pin×7",
		"23 个 warn finding (--strict;info 不阻塞): floating-pin×23",
		"2 wire-bridge(真短路)", "11 orphan-stub (--strict)", "1 orphan-flag (--strict)",
		"2 orphan-tree (--strict)",
		"3 fatal DRC violation",
	}
	for _, reason := range emitted {
		st := gateStage{Status: gateStatusFail, BlockingReasons: []string{reason}}
		if len(gateAdviceFor(st)) == 0 {
			t.Fatalf("blocking reason %q has no prescribed next step", reason)
		}
	}
}

func TestGateBlockersNameWhatBlockedNotASeverityTally(t *testing.T) {
	// The 2026-08-04 run printed "layout-lint: 0 个阻塞项" next to FAIL, because
	// blockers were built from the error tally while --strict failed on
	// promoted advisories. The blocker line must state the actual cause.
	rep := gateReport{Stages: []gateStage{{
		Name:            "layout-lint",
		Status:          gateStatusFail,
		Errors:          0,
		Warnings:        0,
		Summary:         "0 overlap, 0 pin-coincidence, …",
		BlockingReasons: []string{"34 unproven pin geometry (--strict;连接器未给 pinsAvailable 契约)"},
	}}}
	gradeGateReport(&rep)
	if len(rep.Blockers) != 1 {
		t.Fatalf("expected one blocker, got %v", rep.Blockers)
	}
	if strings.Contains(rep.Blockers[0], "0 个阻塞项") {
		t.Fatalf("blocker must not report a zero tally next to FAIL: %q", rep.Blockers[0])
	}
	if !strings.Contains(rep.Blockers[0], "unproven pin geometry") {
		t.Fatalf("blocker must name the actual cause: %q", rep.Blockers[0])
	}
}

func TestFormatTypeTallyIsDeterministicAndMostFrequentFirst(t *testing.T) {
	got := formatTypeTally(map[string]int{"floating-pin": 3, "wire-crossing": 9, "zero-length-wire": 3})
	want := "wire-crossing×9, floating-pin×3, zero-length-wire×3"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// DRC 关的阻塞判据 —— 这一关的 strict 参数曾经**收了不用**,而 --help 承诺
// 「non-fatal DRC items are advisory,--strict promotes them to blocking」。
// 文档承诺了判据没做的事,方向还是「你以为管住了」,所以逐档钉死。
//
// 测的是 drcBlockingReasons 本身,不是在测试里把判据重写一遍 —— 那样只会测到
// 测试自己的逻辑,被测代码改坏了照样绿。
func TestDrcBlockingReasons_MatchesDocumentedContract(t *testing.T) {
	mk := func(fatal, errs, warns, infos int) drcReport {
		r := drcReport{
			Passed: true, NativePassed: true, Fatal: intp(fatal),
			Summary: &drcSummary{}, CountsAvailable: true,
		}
		r.Summary.Fatal, r.Summary.Error, r.Summary.Warn, r.Summary.Info = fatal, errs, warns, infos
		return r
	}
	cases := []struct {
		name      string
		rep       drcReport
		strict    bool
		wantBlock bool
		wantHint  string // 阻塞理由里必须出现的字样
	}{
		{"全清", mk(0, 0, 0, 0), false, false, ""},
		{"全清 + strict", mk(0, 0, 0, 0), true, false, ""},
		{"fatal 任何档位都阻塞", mk(1, 0, 0, 0), false, true, "fatal"},
		{"error 任何档位都阻塞(与 check 关同口径)", mk(0, 2, 0, 0), false, true, "error-level"},
		{"warn 默认不阻塞", mk(0, 0, 3, 0), false, false, ""},
		{"warn 在 strict 下阻塞(--help 的承诺)", mk(0, 0, 3, 0), true, true, "DRC 面板"},
		{"info 即便 strict 也不阻塞", mk(0, 0, 0, 5), true, false, ""},
		{"计数未知时宿主失败仍阻塞", drcReport{Passed: false, NativePassed: false, Strict: true}, true, true, "native DRC verdict failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := drcBlockingReasons(c.rep, c.strict, "")
			if blocked := len(got) > 0; blocked != c.wantBlock {
				t.Fatalf("blocking=%v want %v (reasons=%v)", blocked, c.wantBlock, got)
			}
			if c.wantHint == "" {
				return
			}
			joined := strings.Join(got, " | ")
			if !strings.Contains(joined, c.wantHint) {
				t.Errorf("理由里缺 %q:%s", c.wantHint, joined)
			}
		})
	}
}

func TestDrcBlockingReasons_WarnBlockSaysWhereToLook(t *testing.T) {
	// 平台只回聚合计数,逐条明细没有 API —— 所以这条阻塞如果不写明「去 EasyEDA 的
	// DRC 面板看」,它就是一条无法行动的阻塞,会被直接绕过,连它以后报的真问题
	// 一起绕过。
	rep := drcReport{Passed: true, NativePassed: true, Fatal: intp(0), Summary: &drcSummary{Warn: 1}, CountsAvailable: true}
	got := drcBlockingReasons(rep, true, "")
	if len(got) != 1 {
		t.Fatalf("want 1 reason, got %v", got)
	}
	for _, want := range []string{"--strict", "聚合", "DRC 面板"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("缺 %q:%s", want, got[0])
		}
	}
}

// The gate's check stage shares the checkLevelBlocks ruler (issue #172):
// info-level findings (titleblock-overlap against an ESTIMATED keep-out) never
// block, not even under --strict; warn still blocks under --strict; error always.
func TestGradeGateCheckFindings_InfoNeverBlocks(t *testing.T) {
	infoOnly := checkReport{Findings: []checkFinding{
		{Type: "titleblock-overlap", Level: "info"},
		{Type: "titleblock-overlap", Level: "info"},
	}}
	errs, warns, reasons := gradeGateCheckFindings(infoOnly, true)
	if errs != 0 || warns != 2 {
		t.Fatalf("tally = %d/%d, want 0 errors / 2 warn-info displays", errs, warns)
	}
	if len(reasons) != 0 {
		t.Fatalf("info-only findings must not block even under --strict, got %v", reasons)
	}

	mixed := checkReport{Findings: []checkFinding{
		{Type: "titleblock-overlap", Level: "info"},
		{Type: "wire-crossing", Level: "warn"},
	}}
	if _, _, r := gradeGateCheckFindings(mixed, false); len(r) != 0 {
		t.Fatalf("warn is advisory without --strict, got %v", r)
	}
	_, _, r := gradeGateCheckFindings(mixed, true)
	if len(r) != 1 || !strings.Contains(r[0], "1 个 warn finding") || !strings.Contains(r[0], "wire-crossing×1") {
		t.Fatalf("strict must promote ONLY the warn finding (count 1, typed), got %v", r)
	}
	if strings.Contains(r[0], "titleblock-overlap") {
		t.Fatalf("the info finding's type must not appear in the blocking tally: %v", r)
	}

	withErr := checkReport{Findings: []checkFinding{
		{Type: "titleblock-overlap", Level: "info"},
		{Type: "multi-net-wire", Level: "error"},
	}}
	_, _, r = gradeGateCheckFindings(withErr, false)
	if len(r) != 1 || !strings.Contains(r[0], "1 个 error/fatal finding") {
		t.Fatalf("error must always block, got %v", r)
	}
}
