package daemon

import (
	"fmt"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
	"github.com/zhoushoujianwork/easyeda-agent/internal/workflow"
)

// Daemon-level workflow stage gate (issue #97 follow-up).
//
// The CLI gates its composite route commands, but the daemon is the choke point
// every typed action must pass — so the gate lives HERE too, driven by the
// action catalog (RequiresGate / InvalidatesStage) the same way autosave is
// driven by Mutates. A raw /action caller therefore cannot draw copper tracks
// past an unconfirmed layout, and any placement/outline mutation clears the
// stale downstream confirmations no matter which client performed it.
//
// debug.exec_js remains an un-gateable escape hatch by design; the document
// fingerprints stored at confirm time (verified by the CLI gates) catch what
// it changes after the fact.

// gateForAction maps action name → required workflow gate ("" = ungated).
var gateForAction = func() map[string]string {
	m := map[string]string{}
	for _, a := range protocol.AllActions() {
		if a.RequiresGate != "" {
			m[a.Name] = a.RequiresGate
		}
	}
	return m
}()

// invalidatesForAction maps action name → earliest workflow stage the action
// invalidates on success ("" = none).
var invalidatesForAction = func() map[string]workflow.Stage {
	m := map[string]workflow.Stage{}
	for _, a := range protocol.AllActions() {
		if a.InvalidatesStage != "" {
			m[a.Name] = workflow.Stage(a.InvalidatesStage)
		}
	}
	return m
}()

// stageKeyCandidates lists every identity the project's workflow state may be
// filed under: the caller's routing hint plus the target window's project name
// and uuid. The CLI usually writes by --project (a name); a raw caller may only
// know the windowId — the candidates make both find the same record.
func (s *Server) stageKeyCandidates(req *protocol.Request) []string {
	out := []string{}
	if strings.TrimSpace(req.Project) != "" {
		out = append(out, req.Project)
	}
	if c, ok := s.hub.get(req.WindowID); ok {
		snap := c.snapshot()
		if snap.Context.ProjectName != "" {
			out = append(out, snap.Context.ProjectName)
		}
		if snap.Context.ProjectUUID != "" {
			out = append(out, snap.Context.ProjectUUID)
		}
	}
	return out
}

// checkStageGate enforces a gated action's workflow preconditions. Returns a
// ready-to-send error response when the action must be refused, nil when it may
// proceed. Fail-closed: an unreadable state or an unresolvable project identity
// blocks the action (with an explicit forceReason as the audited override).
func (s *Server) checkStageGate(req *protocol.Request) *protocol.Response {
	gate := gateForAction[req.Action]
	if gate == "" {
		return nil
	}
	force := strings.TrimSpace(req.ForceReason) != ""
	candidates := s.stageKeyCandidates(req)
	if len(candidates) == 0 && !force {
		resp := errorResponse(req.ID, "STAGE_BLOCKED",
			fmt.Sprintf("%s requires the %q gate but the target window has no project identity", req.Action, gate),
			"pass --project (or a forceReason for an audited override)")
		return &resp
	}
	st, err := workflow.LoadAny(candidates...)
	if err != nil {
		// Fail-closed: a corrupt/unreadable state file must not read as "un-gated".
		resp := errorResponse(req.ID, "STAGE_BLOCKED",
			fmt.Sprintf("%s: workflow stage state unreadable — refusing gated action", req.Action), err.Error())
		return &resp
	}
	verdict := workflow.CheckRouteGate(st, force, req.ForceUnsafe, strings.TrimSpace(req.ForceReason))
	if verdict.Audited {
		// Persist every audit event — a granted bypass AND a refused --force
		// attempt (#132) both belong in the trail. Authorization stays
		// per-request (nothing is confirmed), so the next un-forced call is
		// gated again.
		if serr := workflow.Save(st); serr != nil {
			s.logf("stage gate: could not persist force audit for %s: %v", req.Action, serr)
		}
	}
	if !verdict.Allowed {
		resp := errorResponse(req.ID, "STAGE_BLOCKED",
			fmt.Sprintf("%s: %s", req.Action, verdict.Message),
			"see `easyeda workflow status` for the project's stage state")
		return &resp
	}
	if verdict.Forced {
		s.logf("stage gate: %s FORCED past %s (unsafe=%v, reason: %s)", req.Action, strings.Join(verdict.Missing, ", "), req.ForceUnsafe, req.ForceReason)
	}
	return nil
}

// maybeInvalidateStage clears downstream workflow confirmations after a
// successful placement/outline mutation, catalog-driven. The cleared stages are
// surfaced as a response warning so every client sees what its edit invalidated.
func (s *Server) maybeInvalidateStage(req *protocol.Request, resp *protocol.Response) {
	stg, ok := invalidatesForAction[req.Action]
	if !ok || resp == nil || !resp.OK {
		return
	}
	cleared := workflow.InvalidateAll(s.stageKeyCandidates(req), stg, "action "+req.Action)
	if len(cleared) > 0 {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf(
			"workflow stage invalidated: %s (cause: %s) — re-confirm layout/outline and re-run `pcb layout-lint --gate` before routing",
			strings.Join(cleared, ", "), req.Action))
	}
}
