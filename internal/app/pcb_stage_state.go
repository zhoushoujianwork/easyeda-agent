package app

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

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
	stageTierConfirm     = workflow.TierConfirm
)

// Tier ladder aliases (issue #125).
const workflowTierCount = workflow.PlacementTierCount

func workflowTierName(n int) string { return workflow.TierNames[n] }

func nowRFC3339() string { return time.Now().Format(time.RFC3339) }

func workflowHashLayout(poses []stageComponentPose) string { return workflow.HashLayout(poses) }

func workflowNewFingerprint(hash string, count int) *stageFingerprint {
	return workflow.NewFingerprint(hash, count)
}

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

func checkRouteGate(s *pcbStageState, force, forceUnsafe bool, reason string) routeGate {
	return workflow.CheckRouteGate(s, force, forceUnsafe, reason)
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
//
// The override is TIERED (issue #132, plan 1): --force <reason> bypasses SOFT
// gaps only — the mechanical skeleton (placement_confirmed / outline_confirmed)
// must be at least partly confirmed, and an UNKNOWN state (unresolvable
// project, unreadable state file) does not qualify (unknown = possibly
// zero-confirmed). --force-unsafe <reason> bypasses everything — the #116
// footgun now requires deliberately reaching for the sharper flag. Either
// override is per-run, audited in the state history, and propagated to the
// daemon (cfg.forceReason/forceUnsafe → every routing action request) so the
// daemon-side gate applies the same tier; nothing is confirmed, and the next
// un-forced run is gated again.
func gateRouteCommand(cfg *appConfig, window, cmdName, forceReason, forceUnsafeReason string, stderr io.Writer) error {
	unsafeReason := strings.TrimSpace(forceUnsafeReason)
	forceUnsafe := unsafeReason != ""
	reason := strings.TrimSpace(forceReason)
	if forceUnsafe && reason == "" {
		reason = unsafeReason
	}
	force := reason != ""
	if force {
		// Propagate to the daemon-side gate for the routing actions this command
		// is about to dispatch (pcb.line.create / pcb.via.create / …).
		cfg.forceReason = reason
		cfg.forceUnsafe = forceUnsafe
	}

	project, err := resolveStageProject(cfg, window)
	if err != nil {
		if forceUnsafe {
			fmt.Fprintf(stderr, "⚠️  %s: %v — proceeding on --force-unsafe (reason: %s)\n", cmdName, err, reason)
			return nil
		}
		if force {
			fmt.Fprintf(stderr, "❌ %s: %v — --force cannot vouch for an UNKNOWN stage state (it may be zero-confirmed, issue #132); pass --project, or escalate with --force-unsafe <reason>\n", cmdName, err)
			return errActionFailed
		}
		fmt.Fprintf(stderr, "❌ %s: %v (routing is stage-gated; pass --project or --force <reason>)\n", cmdName, err)
		return errActionFailed
	}
	st, err := loadPcbStageState(project)
	if err != nil {
		if forceUnsafe {
			fmt.Fprintf(stderr, "⚠️  %s: could not read workflow state: %v — proceeding on --force-unsafe (reason: %s)\n", cmdName, err, reason)
			return nil
		}
		if force {
			fmt.Fprintf(stderr, "❌ %s: workflow state unreadable (%v) — --force cannot vouch for an unknown state (issue #132); fix or delete %s, or escalate with --force-unsafe <reason>\n",
				cmdName, err, workflow.Path(project))
			return errActionFailed
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

	gate := checkRouteGate(st, force, forceUnsafe, reason)
	if gate.Audited {
		// Persist every audit event — a granted bypass AND a refused --force
		// attempt (#132) both belong in the history trail.
		if serr := savePcbStageState(st); serr != nil {
			fmt.Fprintf(stderr, "⚠️  %s: gate audit could not be persisted: %v\n", cmdName, serr)
		}
	}
	if !gate.Allowed {
		fmt.Fprintf(stderr, "❌ %s: %s\n", cmdName, gate.Message)
		return errActionFailed
	}
	if gate.Forced {
		// Authorization is per-run: routing_authorized is NOT set.
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

// ── placement tiers (issue #125) ────────────────────────────────────────────

// tierPoseHash hashes the poses of exactly the given designators (upper-case
// set), in HashLayout's canonical order. missing lists claimed designators no
// longer present on the board (deleted = drift).
func tierPoseHash(poses []stageComponentPose, designators []string) (hash string, missing []string) {
	want := map[string]bool{}
	for _, d := range designators {
		want[strings.ToUpper(d)] = true
	}
	var subset []stageComponentPose
	seen := map[string]bool{}
	for _, p := range poses {
		u := strings.ToUpper(p.Designator)
		if want[u] {
			subset = append(subset, p)
			seen[u] = true
		}
	}
	for _, d := range designators {
		if !seen[strings.ToUpper(d)] {
			missing = append(missing, d)
		}
	}
	return workflow.HashLayout(subset), missing
}

// verifyTierFingerprints re-derives each confirmed tier's pose hash from the
// live placement. A mismatch (moved / deleted part) invalidates that tier and
// every later one (and the placement_confirmed seal). Pure over the given
// poses; the caller persists. Returns human-readable drift notes.
func verifyTierFingerprints(st *pcbStageState, poses []stageComponentPose) []string {
	var drift []string
	for n := 1; n <= workflow.PlacementTierCount; n++ {
		tc := st.Tier(n)
		if tc == nil || tc.Empty {
			continue
		}
		hash, missing := tierPoseHash(poses, tc.Designators)
		if len(missing) > 0 {
			st.InvalidateTiersFrom(n, fmt.Sprintf("tier %d part(s) deleted: %s", n, strings.Join(missing, ",")))
			drift = append(drift, fmt.Sprintf(
				"tier %d (%s) part(s) no longer on the board: %s — re-run `pcb stage confirm-tier %d`",
				n, workflow.TierNames[n], strings.Join(missing, ","), n))
			break // later tiers were invalidated with it
		}
		if hash != tc.Hash {
			st.InvalidateTiersFrom(n, fmt.Sprintf("tier %d pose drift", n))
			drift = append(drift, fmt.Sprintf(
				"tier %d (%s) placement changed since its sign-off — re-run `pcb stage confirm-tier %d` (later tiers invalidated with it)",
				n, workflow.TierNames[n], n))
			break
		}
	}
	return drift
}

// resolveTierParts decides which designators tier n covers. Pure so it is unit
// testable: live is the board's designators (original case), claimed maps
// designator→owning tier.
//   - empty: declared empty tier — no parts, no hash.
//   - tiers 1–3: an explicit --parts list is required (the review IS per-part).
//   - tier 4: --parts optional; default = every live part no earlier tier claimed
//     (卫星件 = the rest, by definition).
//
// Errors: unknown designators, or parts already claimed by a DIFFERENT tier.
func resolveTierParts(n int, partsFlag []string, empty bool, live []string, claimed map[string]int) ([]string, error) {
	if empty {
		if len(partsFlag) > 0 {
			return nil, fmt.Errorf("--empty and --parts are mutually exclusive")
		}
		return nil, nil
	}
	liveSet := map[string]string{}
	for _, d := range live {
		liveSet[strings.ToUpper(d)] = d
	}
	var out []string
	if len(partsFlag) == 0 {
		if n != workflow.PlacementTierCount {
			return nil, fmt.Errorf("tier %d (%s) needs an explicit --parts list (or --empty) — the sign-off is per-part", n, workflow.TierNames[n])
		}
		for _, d := range live {
			u := strings.ToUpper(d)
			if t, ok := claimed[u]; ok && t != n {
				continue
			}
			out = append(out, u)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("tier 4 default (all unclaimed parts) resolved to nothing — every part is already claimed; pass --empty to record an empty tier")
		}
	} else {
		var unknown, conflict []string
		seen := map[string]bool{}
		for _, raw := range partsFlag {
			for _, d := range strings.Split(raw, ",") {
				d = strings.TrimSpace(d)
				if d == "" {
					continue
				}
				u := strings.ToUpper(d)
				if seen[u] {
					continue
				}
				seen[u] = true
				if _, ok := liveSet[u]; !ok {
					unknown = append(unknown, d)
					continue
				}
				if t, ok := claimed[u]; ok && t != n {
					conflict = append(conflict, fmt.Sprintf("%s(tier %d)", d, t))
					continue
				}
				out = append(out, u)
			}
		}
		if len(unknown) > 0 {
			return nil, fmt.Errorf("not on the board: %s (check `pcb list`)", strings.Join(unknown, ", "))
		}
		if len(conflict) > 0 {
			return nil, fmt.Errorf("already claimed by another tier: %s — a part belongs to exactly one tier (re-confirm that tier to change it)", strings.Join(conflict, ", "))
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("--parts resolved to no designators")
		}
	}
	sort.Strings(out)
	return out, nil
}

// unclaimedParts lists live designators no confirmed tier covers — a part added
// AFTER tier sign-offs would otherwise ride into placement_confirmed unreviewed.
func unclaimedParts(live []string, claimed map[string]int) []string {
	var out []string
	for _, d := range live {
		if _, ok := claimed[strings.ToUpper(d)]; !ok {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
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
