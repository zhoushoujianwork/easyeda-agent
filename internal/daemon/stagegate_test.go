package daemon

import (
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
	"github.com/zhoushoujianwork/easyeda-agent/internal/workflow"
)

// Issue #97 follow-up: the daemon is the choke point — routing actions must be
// gated at /action dispatch (catalog-driven), and placement/outline mutations
// must invalidate downstream confirmations regardless of which client sent them.

func TestStageGateCatalogWiring(t *testing.T) {
	for _, a := range []string{"pcb.line.create", "pcb.via.create", "pcb.import_autoroute"} {
		if gateForAction[a] != protocol.GateRouting {
			t.Fatalf("%s must require the routing gate", a)
		}
	}
	for a, want := range map[string]workflow.Stage{
		"pcb.component.modify":   workflow.StagePlacementConfirmed,
		"pcb.components.move":    workflow.StagePlacementConfirmed,
		"pcb.components.arrange": workflow.StagePlacementConfirmed,
		"pcb.add_component":      workflow.StagePlacementConfirmed,
		"pcb.import_changes":     workflow.StagePlacementConfirmed,
		"pcb.outline.set":        workflow.StageOutlineConfirmed,
		"pcb.outline.clear":      workflow.StageOutlineConfirmed,
	} {
		if invalidatesForAction[a] != want {
			t.Fatalf("%s must invalidate %s, got %q", a, want, invalidatesForAction[a])
		}
	}
	// Routing-class mutations invalidate the post-route check gate (布完必查):
	// any copper change makes the standing pcb-check audit stale. They stay
	// UNGATED (rip-up/repair must work on an unconfirmed board).
	for _, a := range []string{"pcb.route.rip_up", "pcb.clear_routing", "pcb.route.delete",
		"pcb.pour.create", "pcb.pour.rebuild", "pcb.beautify", "pcb.fill.create"} {
		if invalidatesForAction[a] != workflow.StagePostRouteChecked {
			t.Fatalf("%s must invalidate post_route_checked, got %q", a, invalidatesForAction[a])
		}
	}
	if gateForAction["pcb.route.rip_up"] != "" || gateForAction["pcb.clear_routing"] != "" {
		t.Fatal("rip-up/clear_routing must stay ungated (repair path)")
	}
	// Reads and save stay ungated and non-invalidating.
	for _, a := range []string{"pcb.components.list", "pcb.save"} {
		if gateForAction[a] != "" {
			t.Fatalf("%s must not be gated", a)
		}
		if invalidatesForAction[a] != "" {
			t.Fatalf("%s must not invalidate a stage", a)
		}
	}
}

func TestCheckStageGateBlocksThenAllows(t *testing.T) {
	t.Setenv(workflow.EnvDir, t.TempDir())
	s := New(Options{})

	req := &protocol.Request{Action: "pcb.line.create", Project: "gate-proj"}
	req.ID = "req_test"

	// Fresh project: blocked.
	resp := s.checkStageGate(req)
	if resp == nil {
		t.Fatal("routing on a fresh project must be blocked")
	}
	if resp.Error == nil || resp.Error.Code != "STAGE_BLOCKED" {
		t.Fatalf("want STAGE_BLOCKED, got %+v", resp.Error)
	}

	// Confirm the gates → allowed.
	st, _ := workflow.Load("gate-proj")
	st.Confirm(workflow.StageOutlineConfirmed, "confirm", "test")
	st.Confirm(workflow.StagePreRoutePassed, "gate-pass", "test")
	if err := workflow.Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	if resp := s.checkStageGate(req); resp != nil {
		t.Fatalf("confirmed project must pass the gate, got %+v", resp.Error)
	}

	// Ungated action never blocks.
	read := &protocol.Request{Action: "pcb.components.list", Project: "other"}
	if resp := s.checkStageGate(read); resp != nil {
		t.Fatal("reads must not be stage-gated")
	}
}

func TestCheckStageGateForceIsPerRequestAndAudited(t *testing.T) {
	t.Setenv(workflow.EnvDir, t.TempDir())
	s := New(Options{})

	// #132 tier check: on a zero-confirmation project a plain forceReason is
	// REFUSED (STAGE_BLOCKED pointing at --force-unsafe)…
	plain := &protocol.Request{Action: "pcb.via.create", Project: "force-proj", ForceReason: "prototype spin"}
	if resp := s.checkStageGate(plain); resp == nil || resp.Error == nil || resp.Error.Code != "STAGE_BLOCKED" {
		t.Fatalf("plain forceReason must be refused on a zero-confirmation project (#132), got %+v", resp)
	}
	// …and the refusal is audited.
	if st, err := workflow.Load("force-proj"); err != nil || len(st.History) == 0 || st.History[len(st.History)-1].Action != "force-refused" {
		t.Fatalf("refused force must be audited, err=%v", err)
	}

	// ForceUnsafe escalates past the hard tier.
	req := &protocol.Request{Action: "pcb.via.create", Project: "force-proj", ForceReason: "prototype spin", ForceUnsafe: true}
	if resp := s.checkStageGate(req); resp != nil {
		t.Fatalf("forceUnsafe must override the gate, got %+v", resp.Error)
	}
	// The override is audited in the state history…
	st, err := workflow.Load("force-proj")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	found := false
	for _, e := range st.History {
		if e.Action == "force-unsafe" && strings.Contains(e.Reason, "prototype spin") {
			found = true
		}
	}
	if !found {
		t.Fatalf("force must be audited in history, got %+v", st.History)
	}
	// …but nothing is confirmed: the next un-forced request is blocked again.
	unforced := &protocol.Request{Action: "pcb.via.create", Project: "force-proj"}
	if resp := s.checkStageGate(unforced); resp == nil {
		t.Fatal("force must authorize a single request, not persist authorization")
	}
}

func TestMaybeInvalidateStage(t *testing.T) {
	t.Setenv(workflow.EnvDir, t.TempDir())
	s := New(Options{})

	st, _ := workflow.Load("inv-proj")
	st.Confirm(workflow.StagePlacementConfirmed, "confirm", "")
	st.Confirm(workflow.StageOutlineConfirmed, "confirm", "")
	st.Confirm(workflow.StagePreRoutePassed, "gate-pass", "")
	if err := workflow.Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}

	req := &protocol.Request{Action: "pcb.components.move", Project: "inv-proj"}
	resp := &protocol.Response{OK: true}
	s.maybeInvalidateStage(req, resp)

	got, _ := workflow.Load("inv-proj")
	if got.Has(workflow.StagePlacementConfirmed) || got.Has(workflow.StagePreRoutePassed) {
		t.Fatalf("mutation must clear downstream confirmations, got %+v", got.Confirmed)
	}
	if len(resp.Warnings) == 0 || !strings.Contains(resp.Warnings[0], "workflow stage invalidated") {
		t.Fatalf("invalidation must surface as a response warning, got %v", resp.Warnings)
	}

	// A failed action must not invalidate.
	st2, _ := workflow.Load("inv-proj2")
	st2.Confirm(workflow.StagePlacementConfirmed, "confirm", "")
	_ = workflow.Save(st2)
	s.maybeInvalidateStage(&protocol.Request{Action: "pcb.components.move", Project: "inv-proj2"},
		&protocol.Response{OK: false})
	got2, _ := workflow.Load("inv-proj2")
	if !got2.Has(workflow.StagePlacementConfirmed) {
		t.Fatal("a failed action must not invalidate confirmations")
	}
}
