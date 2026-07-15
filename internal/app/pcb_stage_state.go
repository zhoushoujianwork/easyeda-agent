package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/workflow"
)

// pcb_stage_state.go — CLI adapter over the shared internal/workflow stage
// machine (issue #97).
//
// The state machine itself (stages, persistence, route gate, fingerprints)
// lives in internal/workflow so the DAEMON enforces the same gates at the
// /action dispatch choke point (see internal/daemon/stagegate.go). State is
// GLOBAL per project (~/.easyeda-agent/workflow/<key>.json) — running the CLI
// from a different cwd can no longer blind the gate; the old cwd-relative
// .easyeda/pcb-stage/ file is still read as a legacy fallback.
//
// On top of the marker checks, the CLI-side route gate verifies the DOCUMENT
// FINGERPRINTS stored at confirm time (placement poses / outline geometry): a
// GUI drag, a debug.exec_js edit or another agent's move changes the hash, the
// gate auto-invalidates the stale confirmation and blocks until re-confirmed.

// Aliases keep the app-side names stable over the shared workflow types.
type (
	pcbStage             = workflow.Stage
	pcbStageEvent        = workflow.Event
	pcbStageState        = workflow.State
	pcbAssemblyProfile   = workflow.AssemblyProfile
	pcbLayoutGateSummary = workflow.GateSummary
	pcbCheckGateSummary  = workflow.CheckGateSummary
	routeGate            = workflow.Gate
	stageFingerprint     = workflow.Fingerprint
	stageComponentPose   = workflow.ComponentPose
)

const (
	stageImported           = workflow.StageImported
	stagePlacementReady     = workflow.StagePlacementReady
	stagePlacementConfirmed = workflow.StagePlacementConfirmed
	stageOutlineConfirmed   = workflow.StageOutlineConfirmed
	stagePreRoutePassed     = workflow.StagePreRoutePassed
	stageRoutingAuthorized  = workflow.StageRoutingAuthorized
	stagePostRouteChecked   = workflow.StagePostRouteChecked
)

var pcbStageOrder = workflow.Order

func pcbStageRank(s pcbStage) int { return workflow.Rank(s) }

func loadPcbStageState(project string) (*pcbStageState, error) { return workflow.Load(project) }
func savePcbStageState(s *pcbStageState) error                 { return workflow.Save(s) }

func checkRouteGate(s *pcbStageState, force bool, reason string) routeGate {
	return workflow.CheckRouteGate(s, force, reason)
}

// resolveStageProject yields the project key the workflow state is filed under:
// the explicit --project when given, else the live window's project identity
// (friendlyName || name — the same value the connector reports as projectName,
// so the CLI and the daemon-side gate agree on the key).
func resolveStageProject(cfg *appConfig, window string) (string, error) {
	if strings.TrimSpace(cfg.project) != "" {
		return cfg.project, nil
	}
	res, err := requestAction(cfg, "project.current", window, nil)
	if err != nil {
		return "", fmt.Errorf("resolve project for workflow state: %w", err)
	}
	if name := asString(res.Result["friendlyName"]); name != "" {
		return name, nil
	}
	if name := asString(res.Result["name"]); name != "" {
		return name, nil
	}
	if uuid := asString(res.Result["uuid"]); uuid != "" {
		return uuid, nil
	}
	return "", fmt.Errorf("resolve project for workflow state: window reports no project identity")
}

// pullLayoutPoses reads the live placement poses (designator/x/y/rotation/layer)
// the layout fingerprint is derived from.
func pullLayoutPoses(cfg *appConfig, window string) ([]stageComponentPose, error) {
	res, err := requestAction(cfg, "pcb.components.list", window, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch placement for fingerprint: %w", err)
	}
	raw, _ := res.Result["components"].([]any)
	poses := make([]stageComponentPose, 0, len(raw))
	for _, ri := range raw {
		cm, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		// layer is a NUMBER in the connector payload — asString() would coerce
		// it to "" and make a TOP↔BOTTOM flip invisible to the fingerprint
		// (issue #100 review). Accept number or string.
		layer := asString(cm["layer"])
		if layer == "" {
			if n, ok := asFloatOK(cm["layer"]); ok && n != 0 {
				layer = fmt.Sprintf("%.0f", n)
			}
		}
		poses = append(poses, stageComponentPose{
			Designator: asString(cm["designator"]),
			X:          asFloat(cm["x"]),
			Y:          asFloat(cm["y"]),
			Rotation:   asFloat(cm["rotation"]),
			Layer:      layer,
		})
	}
	return poses, nil
}

// pullLayoutFingerprint hashes the live placement.
func pullLayoutFingerprint(cfg *appConfig, window string) (*stageFingerprint, error) {
	poses, err := pullLayoutPoses(cfg, window)
	if err != nil {
		return nil, err
	}
	return workflow.NewFingerprint(workflow.HashLayout(poses), len(poses)), nil
}

// pullOutlineFingerprint hashes the live board outline snapshot (counts + bbox).
func pullOutlineFingerprint(cfg *appConfig, window string) (*stageFingerprint, error) {
	res, err := requestAction(cfg, "pcb.outline.get", window, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch outline for fingerprint: %w", err)
	}
	snapshot := map[string]any{
		"outline":  res.Result["outline"],
		"segments": res.Result["segments"],
		"arcs":     res.Result["arcs"],
		"bbox":     res.Result["bbox"],
	}
	hash, err := workflow.HashJSON(snapshot)
	if err != nil {
		return nil, err
	}
	count := int(asFloat(res.Result["outline"])) + int(asFloat(res.Result["segments"])) + int(asFloat(res.Result["arcs"]))
	return workflow.NewFingerprint(hash, count), nil
}

// verifyStageFingerprints re-derives the placement/outline fingerprints from the
// live document and compares them to the ones stored at confirm time. A
// mismatch means the document changed OUTSIDE the gated flow (GUI drag,
// debug.exec_js, another agent) — the stale confirmation is invalidated so the
// flow re-enters at the right stage. Returns human-readable drift notes (empty
// = no drift). The caller persists the state.
func verifyStageFingerprints(cfg *appConfig, window string, st *pcbStageState) ([]string, error) {
	var drift []string
	if st.LayoutFP != nil && st.Has(stagePlacementConfirmed) {
		live, err := pullLayoutFingerprint(cfg, window)
		if err != nil {
			return nil, err
		}
		if live.Hash != st.LayoutFP.Hash {
			st.InvalidateFrom(stagePlacementConfirmed,
				fmt.Sprintf("placement fingerprint drift (confirmed %d parts @ %s, live %d parts)",
					st.LayoutFP.Count, st.LayoutFP.At, live.Count))
			drift = append(drift, fmt.Sprintf(
				"placement changed since confirm-layout (%d parts @ %s) — re-run `pcb stage confirm-layout`",
				live.Count, st.UpdatedAt))
		}
	}
	if st.OutlineFP != nil && st.Has(stageOutlineConfirmed) {
		live, err := pullOutlineFingerprint(cfg, window)
		if err != nil {
			return nil, err
		}
		if live.Hash != st.OutlineFP.Hash {
			st.InvalidateFrom(stageOutlineConfirmed, "outline fingerprint drift (board edge changed since confirm-outline)")
			drift = append(drift, "board outline changed since confirm-outline — re-run `pcb stage confirm-outline`")
		}
	}
	return drift, nil
}

// gateRouteCommand enforces the route gate for a composite CLI route command.
// It returns errActionFailed (already-explained) when routing is blocked, nil
// when allowed. FAIL-CLOSED: an unreadable state or an unverifiable fingerprint
// blocks routing (the pre-#97 behavior treated a read error as "un-gated").
// --force <reason> overrides for THIS run only — the reason is recorded in the
// state history AND propagated to the daemon (cfg.forceReason → every routing
// action request), so the daemon-side gate honors the same audited override;
// nothing is confirmed, and the next un-forced run is gated again.
func gateRouteCommand(cfg *appConfig, window, cmdName, forceReason string, stderr io.Writer) error {
	reason := strings.TrimSpace(forceReason)
	force := reason != ""
	if force {
		// Propagate to the daemon-side gate for the routing actions this command
		// is about to dispatch (pcb.line.create / pcb.via.create / …).
		cfg.forceReason = reason
	}

	project, err := resolveStageProject(cfg, window)
	if err != nil {
		if force {
			fmt.Fprintf(stderr, "⚠️  %s: %v — proceeding on --force (reason: %s)\n", cmdName, err, reason)
			return nil
		}
		fmt.Fprintf(stderr, "❌ %s: %v (routing is stage-gated; pass --project or --force <reason>)\n", cmdName, err)
		return errActionFailed
	}
	st, err := loadPcbStageState(project)
	if err != nil {
		if force {
			fmt.Fprintf(stderr, "⚠️  %s: could not read workflow state: %v — proceeding on --force (reason: %s)\n", cmdName, err, reason)
			return nil
		}
		fmt.Fprintf(stderr, "❌ %s: workflow state unreadable (%v) — refusing to route (fail-closed); fix or delete %s, or --force <reason>\n",
			cmdName, err, workflow.Path(project))
		return errActionFailed
	}

	// Fingerprint drift check: catches edits the gated flow never saw.
	if !force {
		drift, derr := verifyStageFingerprints(cfg, window, st)
		if derr != nil {
			fmt.Fprintf(stderr, "❌ %s: cannot verify document fingerprints (%v) — refusing to route (fail-closed)\n", cmdName, derr)
			return errActionFailed
		}
		if len(drift) > 0 {
			if serr := savePcbStageState(st); serr != nil {
				fmt.Fprintf(stderr, "⚠️  %s: could not persist drift invalidation: %v\n", cmdName, serr)
			}
			for _, d := range drift {
				fmt.Fprintf(stderr, "⚠️  %s: %s\n", cmdName, d)
			}
		}
	}

	gate := checkRouteGate(st, force, reason)
	if !gate.Allowed {
		fmt.Fprintf(stderr, "❌ %s: %s\n", cmdName, gate.Message)
		return errActionFailed
	}
	if gate.Forced {
		// The force audit event is in the history; persist it. Authorization is
		// per-run: routing_authorized is NOT set.
		if serr := savePcbStageState(st); serr != nil {
			fmt.Fprintf(stderr, "⚠️  %s: gate override could not be persisted: %v\n", cmdName, serr)
		}
		fmt.Fprintf(stderr, "⚠️  %s: %s\n", cmdName, gate.Message)
		return nil
	}
	// Normal pass: record routing_authorized (informational — invalidated by any
	// later placement/outline mutation like every other downstream stage).
	if !st.Has(stageRoutingAuthorized) {
		st.Confirm(stageRoutingAuthorized, "gate-pass", cmdName)
		if serr := savePcbStageState(st); serr != nil {
			fmt.Fprintf(stderr, "⚠️  %s: could not persist routing_authorized: %v\n", cmdName, serr)
		}
	}
	return nil
}

// invalidatePcbStageFrom loads the project state, clears the given stage and
// everything downstream, and persists. Returns the cleared stages (as strings)
// so a command can surface what its mutation invalidated. Best-effort: a load /
// save failure is non-fatal to the caller (the mutation itself succeeded).
// The daemon performs the same catalog-driven invalidation at dispatch, so this
// CLI-side hook mostly reports what already happened for composite commands.
func invalidatePcbStageFrom(cfg *appConfig, from pcbStage, cause string) []string {
	st, err := loadPcbStageState(cfg.project)
	if err != nil {
		return nil
	}
	cleared := st.InvalidateFrom(from, cause)
	if len(cleared) == 0 {
		return nil
	}
	_ = savePcbStageState(st)
	out := make([]string, len(cleared))
	for i, c := range cleared {
		out[i] = string(c)
	}
	return out
}
