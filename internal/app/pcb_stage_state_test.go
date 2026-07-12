package app

import (
	"os"
	"testing"
)

// Issue #97 regression: the PCB flow must not let routing proceed without
// outline_confirmed + pre_route_passed, and any placement/outline mutation must
// invalidate downstream confirmations.

func newTestStageState() *pcbStageState {
	return &pcbStageState{Project: "test", Confirmed: map[pcbStage]bool{}}
}

func TestRouteGateBlocksUnconfirmed(t *testing.T) {
	st := newTestStageState()
	g := checkRouteGate(st, false, "")
	if g.Allowed {
		t.Fatal("route gate must block a fresh (unconfirmed) layout")
	}
	if len(g.Missing) != 2 {
		t.Fatalf("expected outline_confirmed + pre_route_passed missing, got %v", g.Missing)
	}
}

func TestRouteGateAllowsWhenConfirmed(t *testing.T) {
	st := newTestStageState()
	st.confirmStage(stageOutlineConfirmed, "confirm", "")
	st.confirmStage(stagePreRoutePassed, "gate-pass", "")
	g := checkRouteGate(st, false, "")
	if !g.Allowed {
		t.Fatalf("route gate should allow with both confirmations, missing=%v", g.Missing)
	}
}

func TestRouteGateForceRecordsAudit(t *testing.T) {
	st := newTestStageState()
	g := checkRouteGate(st, true, "prototype spin, DRC reviewed manually")
	if !g.Allowed {
		t.Fatal("force must allow routing past a missing gate")
	}
	if len(st.History) != 1 || st.History[0].Action != "force" {
		t.Fatalf("force must record one audit event, got %v", st.History)
	}
	if st.History[0].Reason == "" {
		t.Fatal("forced override must record a reason")
	}
}

func TestMutationInvalidatesDownstream(t *testing.T) {
	st := newTestStageState()
	for _, s := range []pcbStage{
		stagePlacementReady, stagePlacementConfirmed, stageOutlineConfirmed, stagePreRoutePassed,
	} {
		st.confirmStage(s, "confirm", "")
	}
	st.Layout = &pcbLayoutGateSummary{Score: 70}

	// Moving a part invalidates placement_confirmed and everything after it.
	cleared := st.invalidateFrom(stagePlacementConfirmed, "test move")
	if len(cleared) != 3 {
		t.Fatalf("expected 3 cleared (placement_confirmed, outline_confirmed, pre_route_passed), got %v", cleared)
	}
	if st.has(stagePlacementConfirmed) || st.has(stageOutlineConfirmed) || st.has(stagePreRoutePassed) {
		t.Fatal("downstream confirmations must be cleared")
	}
	if !st.has(stagePlacementReady) {
		t.Fatal("upstream stage (placement_ready) must survive")
	}
	if st.Layout != nil {
		t.Fatal("layout gate snapshot must drop when pre_route is invalidated")
	}
	// Gate now blocks again.
	if checkRouteGate(st, false, "").Allowed {
		t.Fatal("routing must be blocked again after invalidation")
	}
}

func TestOutlineMutationKeepsPlacement(t *testing.T) {
	st := newTestStageState()
	st.confirmStage(stagePlacementConfirmed, "confirm", "")
	st.confirmStage(stageOutlineConfirmed, "confirm", "")
	st.confirmStage(stagePreRoutePassed, "gate-pass", "")

	cleared := st.invalidateFrom(stageOutlineConfirmed, "outline resized")
	if len(cleared) != 2 {
		t.Fatalf("outline change should clear outline_confirmed + pre_route_passed, got %v", cleared)
	}
	if !st.has(stagePlacementConfirmed) {
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
}

func TestStageStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(cwd)

	st, err := loadPcbStageState("proj-x")
	if err != nil {
		t.Fatalf("load fresh: %v", err)
	}
	st.confirmStage(stageOutlineConfirmed, "confirm", "note")
	if err := savePcbStageState(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadPcbStageState("proj-x")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.has(stageOutlineConfirmed) {
		t.Fatal("persisted confirmation must survive a reload")
	}
}
