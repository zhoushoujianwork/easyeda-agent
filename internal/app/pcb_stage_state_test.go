package app

import (
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/workflow"
)

// Issue #97 regression: the PCB flow must not let routing proceed without
// outline_confirmed + pre_route_passed, and any placement/outline mutation must
// invalidate downstream confirmations.

func newTestStageState() *pcbStageState {
	return &pcbStageState{Project: "test", Confirmed: map[pcbStage]bool{}}
}

func TestRouteGateBlocksUnconfirmed(t *testing.T) {
	st := newTestStageState()
	g := checkRouteGate(st, false, false, "")
	if g.Allowed {
		t.Fatal("route gate must block a fresh (unconfirmed) layout")
	}
	if len(g.Missing) != 2 {
		t.Fatalf("expected outline_confirmed + pre_route_passed missing, got %v", g.Missing)
	}
}

func TestRouteGateAllowsWhenConfirmed(t *testing.T) {
	st := newTestStageState()
	st.Confirm(stageOutlineConfirmed, "confirm", "")
	st.Confirm(stagePreRoutePassed, "gate-pass", "")
	g := checkRouteGate(st, false, false, "")
	if !g.Allowed {
		t.Fatalf("route gate should allow with both confirmations, missing=%v", g.Missing)
	}
}

// TestRouteGateForceTiers pins the #132 plan-1 semantics: plain --force only
// bypasses SOFT gaps (mechanical skeleton at least partly confirmed); a
// zero-confirmation board refuses it and demands --force-unsafe, which bypasses
// everything. Every path leaves an audit event, including the refusal.
func TestRouteGateForceTiers(t *testing.T) {
	// HARD: nothing confirmed → plain force is refused, audited as force-refused.
	st := newTestStageState()
	g := checkRouteGate(st, true, false, "prototype spin")
	if g.Allowed {
		t.Fatal("plain --force must be refused on a zero-confirmation board (#132)")
	}
	if !g.Audited || len(st.History) != 1 || st.History[0].Action != "force-refused" {
		t.Fatalf("refused force must record a force-refused audit event, got %v", st.History)
	}

	// HARD + unsafe → allowed, audited as force-unsafe.
	st = newTestStageState()
	g = checkRouteGate(st, true, true, "prototype spin, DRC reviewed manually")
	if !g.Allowed || !g.Forced {
		t.Fatal("--force-unsafe must allow routing past a zero-confirmation board")
	}
	if len(st.History) != 1 || st.History[0].Action != "force-unsafe" || st.History[0].Reason == "" {
		t.Fatalf("unsafe override must record a force-unsafe audit event with reason, got %v", st.History)
	}

	// SOFT: placement confirmed, outline+preRoute missing → plain force passes.
	st = newTestStageState()
	st.Confirm(stagePlacementConfirmed, "confirm", "")
	g = checkRouteGate(st, true, false, "outline pending, prototype")
	if !g.Allowed || !g.Forced {
		t.Fatalf("plain --force must bypass soft gaps once the skeleton is partly confirmed, got %+v", g)
	}
	if len(st.History) != 2 || st.History[1].Action != "force" {
		t.Fatalf("soft force must record a force audit event, got %v", st.History)
	}

	// SOFT variant: outline confirmed, only pre_route missing → plain force passes.
	st = newTestStageState()
	st.Confirm(stageOutlineConfirmed, "confirm", "")
	g = checkRouteGate(st, true, false, "lint gate stale, re-running after")
	if !g.Allowed {
		t.Fatalf("plain --force must bypass a missing pre_route_passed, got %+v", g)
	}
}

func TestMutationInvalidatesDownstream(t *testing.T) {
	st := newTestStageState()
	for _, s := range []pcbStage{
		stagePlacementReady, stagePlacementConfirmed, stageOutlineConfirmed, stagePreRoutePassed,
	} {
		st.Confirm(s, "confirm", "")
	}
	st.Layout = &pcbLayoutGateSummary{Score: 70}

	// Moving a part invalidates placement_confirmed and everything after it.
	cleared := st.InvalidateFrom(stagePlacementConfirmed, "test move")
	if len(cleared) != 3 {
		t.Fatalf("expected 3 cleared (placement_confirmed, outline_confirmed, pre_route_passed), got %v", cleared)
	}
	if st.Has(stagePlacementConfirmed) || st.Has(stageOutlineConfirmed) || st.Has(stagePreRoutePassed) {
		t.Fatal("downstream confirmations must be cleared")
	}
	if !st.Has(stagePlacementReady) {
		t.Fatal("upstream stage (placement_ready) must survive")
	}
	if st.Layout != nil {
		t.Fatal("layout gate snapshot must drop when pre_route is invalidated")
	}
	// Gate now blocks again.
	if checkRouteGate(st, false, false, "").Allowed {
		t.Fatal("routing must be blocked again after invalidation")
	}
}

func TestOutlineMutationKeepsPlacement(t *testing.T) {
	st := newTestStageState()
	st.Confirm(stagePlacementConfirmed, "confirm", "")
	st.Confirm(stageOutlineConfirmed, "confirm", "")
	st.Confirm(stagePreRoutePassed, "gate-pass", "")

	cleared := st.InvalidateFrom(stageOutlineConfirmed, "outline resized")
	if len(cleared) != 2 {
		t.Fatalf("outline change should clear outline_confirmed + pre_route_passed, got %v", cleared)
	}
	if !st.Has(stagePlacementConfirmed) {
		t.Fatal("placement_confirmed must survive an outline-only change")
	}
}

func TestEvalLayoutGate(t *testing.T) {
	opt := pcbLayoutGateOpts{gate: true, minScore: 60, maxCrossings: 8}

	// The issue's reproduced layout: score 32, 17 crossings → must fail.
	bad := pcbLayoutReport{Score: 32, CrossingCount: 17}
	if v := evalLayoutGate(bad, opt); v.Pass {
		t.Fatalf("score 32 / 17 crossings must FAIL the gate, got %+v", v)
	}

	// A clean layout passes.
	good := pcbLayoutReport{Score: 80, CrossingCount: 2}
	if v := evalLayoutGate(good, opt); !v.Pass {
		t.Fatalf("score 80 / 2 crossings should pass, reasons=%v", v.Reasons)
	}

	// Overlap alone fails regardless of score.
	ovl := pcbLayoutReport{Score: 95, CrossingCount: 0, Overlaps: []pcbLFinding{{A: "U1", B: "U2"}}}
	if v := evalLayoutGate(ovl, opt); v.Pass {
		t.Fatal("any overlap must fail the gate")
	}

	// Issue #99: electrical clearance is not a hand-solder assembly gate.
	tight := pcbLayoutReport{Score: 90, CrossingCount: 0, MinGapMil: 40,
		TightPairs: []pcbLFinding{{A: "C1", B: "U1", Gap: 16.4}}}
	if v := evalLayoutGate(tight, opt); v.Pass {
		t.Fatal("any pair below the selected assembly gap must fail the gate")
	}
}

func TestAssemblyProfileRoundTrip(t *testing.T) {
	t.Setenv(workflow.EnvDir, t.TempDir())

	st := newTestStageState()
	st.Assembly = &pcbAssemblyProfile{Profile: "hand-solder", MinGapMil: 40, LargePadAccessMil: 60}
	if err := savePcbStageState(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadPcbStageState("test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Assembly == nil || got.Assembly.Profile != "hand-solder" || got.Assembly.MinGapMil != 40 {
		t.Fatalf("assembly profile did not round-trip: %+v", got.Assembly)
	}
}

func TestStageStateRoundTrip(t *testing.T) {
	t.Setenv(workflow.EnvDir, t.TempDir())

	st, err := loadPcbStageState("proj-x")
	if err != nil {
		t.Fatalf("load fresh: %v", err)
	}
	st.Confirm(stageOutlineConfirmed, "confirm", "note")
	if err := savePcbStageState(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadPcbStageState("proj-x")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.Has(stageOutlineConfirmed) {
		t.Fatal("persisted confirmation must survive a reload")
	}
}
