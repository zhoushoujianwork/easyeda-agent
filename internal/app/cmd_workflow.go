package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/workflow"
)

// cmd_workflow.go — the `easyeda workflow` group (issue #97 follow-up): the
// project-level fixed workflow over the persisted stage state.
//
// The point (from the issue): stage state and acceptance results must be
// PERSISTED per project and CONSUMED by later commands — never dependent on an
// agent remembering the skill. A user (or another agent) may also cut in at ANY
// stage and edit directly; `workflow status --reconcile` re-derives where the
// document actually is (live facts + fingerprint drift), auto-invalidates stale
// confirmations, and `workflow advance` idempotently walks the flow forward,
// always printing the exact next command — so after any out-of-band edit the
// flow simply continues from the right stage.

// newWorkflowCmd builds the `workflow` group.
func newWorkflowCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string
	wf := &cobra.Command{
		Use:   "workflow",
		Short: "Project design-flow state machine: init / status / advance / confirm / reset (issue #97)",
		Long: `Per-project persisted workflow over the PCB design flow.

Stages (rank order):
  imported → placement_ready → placement_confirmed → outline_confirmed
           → pre_route_passed → routing_authorized

Hard rules (enforced by the CLI route commands AND the daemon at /action):
  • routing (route-short / autoroute / pcb.line.create / pcb.via.create /
    pcb.import_autoroute) refuses without outline_confirmed + pre_route_passed;
  • any placement/outline mutation invalidates the downstream confirmations;
  • confirmations are pinned to document fingerprints — an edit the flow never
    saw (GUI drag, debug.exec_js, another agent) is detected and invalidates;
  • --force <reason> on a route command is a per-run, audited override.

State lives at ~/.easyeda-agent/workflow/<project>.json (EASYEDA_WORKFLOW_DIR
to override). Cut in at any stage: run 'workflow status --reconcile' to re-sync
the marker with the real document, then 'workflow advance' to continue.`,
	}
	wf.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID (else use --project)")

	wf.AddCommand(newWorkflowInitCmd(cfg, &window, stdout, stderr))
	wf.AddCommand(newWorkflowStatusCmd(cfg, &window, stdout, stderr))
	wf.AddCommand(newWorkflowAdvanceCmd(cfg, &window, stdout, stderr))
	wf.AddCommand(newWorkflowConfirmCmd(cfg, &window, stdout, stderr))
	wf.AddCommand(newWorkflowResetCmd(cfg, &window, stdout))
	return wf
}

// workflowFacts is what the live document actually contains — the ground truth
// the marker is reconciled against.
type workflowFacts struct {
	Components  int  `json:"components"`
	OutlineSegs int  `json:"outlineSegments"`
	RoutedLines int  `json:"routedLines"`
	Reachable   bool `json:"reachable"`
}

// pullWorkflowFacts reads the live document counts (best-effort per field).
func pullWorkflowFacts(cfg *appConfig, window string) (workflowFacts, error) {
	var f workflowFacts
	res, err := requestAction(cfg, "pcb.components.list", window, nil)
	if err != nil {
		return f, err
	}
	f.Reachable = true
	if comps, ok := res.Result["components"].([]any); ok {
		f.Components = len(comps)
	}
	if ores, oerr := requestAction(cfg, "pcb.outline.get", window, nil); oerr == nil {
		f.OutlineSegs = int(asFloat(ores.Result["outline"])) + int(asFloat(ores.Result["segments"])) + int(asFloat(ores.Result["arcs"]))
	}
	if lres, lerr := requestAction(cfg, "pcb.line.list", window, nil); lerr == nil {
		if lines, ok := lres.Result["lines"].([]any); ok {
			f.RoutedLines = len(lines)
		} else {
			f.RoutedLines = int(asFloat(lres.Result["count"]))
		}
	}
	return f, nil
}

// workflowNext computes the single next command for the current state + facts.
// This is the "default continue" the issue asks for: whatever stage the user
// cut in at, the flow tells them (and the agent) exactly what comes next.
func workflowNext(st *pcbStageState, f workflowFacts) (next, why string) {
	switch {
	case st.Assembly == nil:
		return "easyeda pcb stage set-assembly --profile hand-solder|reflow",
			"assembly profile not set (P2 precondition, issue #99)"
	case f.Reachable && f.Components == 0:
		return "easyeda pcb import-changes",
			"the PCB has no components yet (P1)"
	case !st.Has(stagePlacementConfirmed):
		if st.Layout == nil || st.Layout.TightPairs != 0 || st.Layout.AccessBlocked != 0 {
			return "easyeda pcb layout-lint --gate",
				"placement not lint-gated yet (P2: fix overlaps/tight pairs/iron access, then gate)"
		}
		return "easyeda pcb stage confirm-layout --note \"...\"",
			"placement awaits the P2 human sign-off"
	case f.Reachable && f.OutlineSegs == 0:
		return "easyeda pcb outline-fit",
			"no board outline yet (P3)"
	case !st.Has(stageOutlineConfirmed):
		return "easyeda pcb stage confirm-outline --note \"...\"",
			"outline awaits the P3 human sign-off"
	case !st.Has(stagePreRoutePassed):
		return "easyeda pcb layout-lint --gate",
			"routability gate not passed since the last change (P6)"
	default:
		return "easyeda pcb route-short   (or autoroute)",
			"routing is authorized (P7)"
	}
}

// reconcileWorkflow re-syncs the marker with the live document: fingerprint
// drift auto-invalidates stale confirmations; obvious inconsistencies are
// reported. It never auto-CONFIRMS anything — human sign-offs stay human.
func reconcileWorkflow(cfg *appConfig, window string, st *pcbStageState, f workflowFacts) (notes []string) {
	drift, derr := verifyStageFingerprints(cfg, window, st)
	if derr != nil {
		notes = append(notes, fmt.Sprintf("fingerprint verify failed: %v", derr))
		return notes
	}
	if len(drift) > 0 {
		_ = savePcbStageState(st)
		notes = append(notes, drift...)
	}
	// placement_ready is mechanical (parts exist) — safe to auto-sync both ways.
	if f.Components > 0 && !st.Has(stagePlacementReady) {
		st.Confirm(stagePlacementReady, "reconcile", fmt.Sprintf("%d components on board", f.Components))
		_ = savePcbStageState(st)
		notes = append(notes, fmt.Sprintf("placement_ready auto-set (%d components on board)", f.Components))
	}
	if f.Reachable && f.Components == 0 {
		if cleared := st.InvalidateFrom(stagePlacementReady, "reconcile: board has no components"); len(cleared) > 0 {
			_ = savePcbStageState(st)
			notes = append(notes, "board is empty — cleared "+strings.Join(stageNames(cleared), ", "))
		}
	}
	if f.Reachable && f.OutlineSegs == 0 && st.Has(stageOutlineConfirmed) {
		if cleared := st.InvalidateFrom(stageOutlineConfirmed, "reconcile: board outline is gone"); len(cleared) > 0 {
			_ = savePcbStageState(st)
			notes = append(notes, "outline is gone — cleared "+strings.Join(stageNames(cleared), ", "))
		}
	}
	// Routing that predates authorization: report, never bless.
	if f.RoutedLines > 0 && !st.Has(stageRoutingAuthorized) {
		notes = append(notes, fmt.Sprintf(
			"%d routed line(s) exist but routing was never authorized — rip up (`pcb clear-routing`) or complete the gates before continuing",
			f.RoutedLines))
	}
	return notes
}

func stageNames(stages []pcbStage) []string {
	out := make([]string, len(stages))
	for i, s := range stages {
		out[i] = string(s)
	}
	return out
}

// ── init ────────────────────────────────────────────────────────────────────

func newWorkflowInitCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize (or restart) the project's workflow marker",
		Long: `Create a fresh workflow record for the project (or wipe an existing one back
to 'imported'). Run once when starting a board; then follow 'workflow advance'.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda workflow init --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			project := stageKeyBestEffort(cfg, *window)
			if strings.TrimSpace(project) == "" {
				return fmt.Errorf("workflow init needs a project identity: pass --project or connect a window")
			}
			st, err := loadPcbStageState(project)
			if err != nil {
				// init may recover from a corrupt state file: start fresh.
				fmt.Fprintf(stderr, "⚠️  existing state unreadable (%v) — starting fresh\n", err)
				st = &pcbStageState{Project: project}
			}
			st.InvalidateFrom(stagePlacementReady, "workflow init")
			st.Confirm(stageImported, "init", "workflow init")
			if err := savePcbStageState(st); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "✓ workflow initialized for %q → %s\n", project, workflow.Path(project))
			facts, ferr := pullWorkflowFacts(cfg, *window)
			if ferr == nil {
				next, why := workflowNext(st, facts)
				fmt.Fprintf(stdout, "next: %s\n  (%s)\n", next, why)
			}
			return nil
		},
	}
}

// ── status ──────────────────────────────────────────────────────────────────

func newWorkflowStatusCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var asJSON, reconcile bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Show the workflow state; --reconcile re-syncs it with the live document",
		Long: `Print the persisted stage state and the computed next step.

--reconcile additionally pulls the LIVE document (component count, outline,
routed lines), re-verifies the confirmation fingerprints, auto-invalidates
anything that drifted, and reports inconsistencies (e.g. routing that predates
authorization). Use it whenever you (or anyone) edited outside the flow.`,
		Args: cobra.NoArgs,
		Example: `  easyeda workflow status --project ceshi
  easyeda workflow status --project ceshi --reconcile`,
		RunE: func(cmd *cobra.Command, args []string) error {
			project := stageKeyBestEffort(cfg, *window)
			st, err := loadPcbStageState(project)
			if err != nil {
				return err
			}
			var facts workflowFacts
			var notes []string
			if reconcile {
				facts, err = pullWorkflowFacts(cfg, *window)
				if err != nil {
					return fmt.Errorf("reconcile needs a connected window: %w", err)
				}
				notes = reconcileWorkflow(cfg, *window, st, facts)
			}
			gate := checkRouteGate(st, false, "")
			next, why := workflowNext(st, facts)
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				out := map[string]any{
					"project":      st.Project,
					"confirmed":    st.Confirmed,
					"assembly":     st.Assembly,
					"layoutGate":   st.Layout,
					"routeAllowed": gate.Allowed,
					"missing":      gate.Missing,
					"next":         next,
					"nextReason":   why,
				}
				if reconcile {
					out["facts"] = facts
					out["reconcileNotes"] = notes
				}
				return enc.Encode(out)
			}
			fmt.Fprintf(stdout, "workflow — project %q\n", stageProjectLabel(project))
			for _, s := range pcbStageOrder {
				mark := "○"
				if st.Has(s) {
					mark = "●"
				}
				fmt.Fprintf(stdout, "  %s %s\n", mark, s)
			}
			if reconcile {
				fmt.Fprintf(stdout, "  document: %d components, %d outline segment(s), %d routed line(s)\n",
					facts.Components, facts.OutlineSegs, facts.RoutedLines)
				for _, n := range notes {
					fmt.Fprintf(stdout, "  ⚠️  %s\n", n)
				}
			}
			if gate.Allowed {
				fmt.Fprintln(stdout, "  routing: ✅ authorized")
			} else {
				fmt.Fprintf(stdout, "  routing: ❌ blocked — missing %s\n", strings.Join(gate.Missing, ", "))
			}
			fmt.Fprintf(stdout, "next: %s\n  (%s)\n", next, why)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit as JSON")
	c.Flags().BoolVar(&reconcile, "reconcile", false, "pull the live document, verify fingerprints, auto-invalidate drift")
	return c
}

// ── advance ─────────────────────────────────────────────────────────────────

func newWorkflowAdvanceCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var minScore, maxCrossings int
	c := &cobra.Command{
		Use:   "advance",
		Short: "Reconcile, run the next MECHANICAL acceptance, and say what comes next",
		Long: `Idempotently walk the workflow forward:

  1. reconcile the marker with the live document (fingerprints, counts);
  2. if the next step is MECHANICAL (the layout-lint routability gate), run it;
  3. print the exact next command.

Human sign-offs (confirm-layout / confirm-outline) are never auto-performed —
advance exits non-zero when it is blocked on one, so scripted loops stop at
exactly the points a human must approve.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda workflow advance --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := resolveStageProject(cfg, *window)
			if err != nil {
				return fmt.Errorf("workflow advance needs a connected window: %w", err)
			}
			st, err := loadPcbStageState(project)
			if err != nil {
				return err
			}
			facts, err := pullWorkflowFacts(cfg, *window)
			if err != nil {
				return err
			}
			for _, n := range reconcileWorkflow(cfg, *window, st, facts) {
				fmt.Fprintf(stderr, "⚠️  %s\n", n)
			}

			// Mechanical acceptance: the routability gate can be run here. It
			// persists pre_route_passed on pass (see runPcbLayoutLint).
			runnableGate := st.Assembly != nil && facts.Components > 0 &&
				(!st.Has(stagePreRoutePassed) || st.Layout == nil)
			if runnableGate {
				fmt.Fprintf(stderr, "→ running `pcb layout-lint --gate` (min-score %d, max-crossings %d)\n", minScore, maxCrossings)
				if lerr := runPcbLayoutLint(cfg, *window, 0, false, pcbLayoutGateOpts{
					gate: true, project: project, minScore: minScore, maxCrossings: maxCrossings,
				}, stdout, stderr); lerr != nil {
					fmt.Fprintf(stderr, "❌ gate failed: %v\n", lerr)
					// fall through — next below will point at fixing the layout
				}
				// reload: the gate may have confirmed pre_route_passed
				if reloaded, rerr := loadPcbStageState(project); rerr == nil {
					st = reloaded
				}
			}

			next, why := workflowNext(st, facts)
			fmt.Fprintf(stdout, "next: %s\n  (%s)\n", next, why)
			// Non-zero when blocked on a human sign-off, so scripted advance loops
			// stop exactly at the approval points.
			if strings.Contains(next, "confirm-layout") || strings.Contains(next, "confirm-outline") ||
				strings.Contains(next, "set-assembly") {
				return errActionFailed
			}
			return nil
		},
	}
	c.Flags().IntVar(&minScore, "min-score", 60, "minimum routability score for the gate")
	c.Flags().IntVar(&maxCrossings, "max-crossings", 8, "maximum ratline crossings for the gate (-1 = unlimited)")
	return c
}

// ── confirm ─────────────────────────────────────────────────────────────────

func newWorkflowConfirmCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var note string
	c := &cobra.Command{
		Use:   "confirm <layout|outline>",
		Short: "Record a human sign-off (same as `pcb stage confirm-layout/-outline`)",
		Args:  cobra.ExactArgs(1),
		Example: `  easyeda workflow confirm layout --project ceshi --note "USB-C out, antenna top"
  easyeda workflow confirm outline --project ceshi --note "40×25mm"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch strings.ToLower(strings.TrimSpace(args[0])) {
			case "layout":
				return runStageConfirmLayout(cfg, *window, note, stderr)
			case "outline":
				return runStageConfirmOutline(cfg, *window, note, stderr)
			default:
				return fmt.Errorf("confirm target must be layout or outline (got %q)", args[0])
			}
		},
	}
	c.Flags().StringVar(&note, "note", "", "what was reviewed/confirmed (recorded in the audit trail)")
	return c
}

// ── reset ───────────────────────────────────────────────────────────────────

func newWorkflowResetCmd(cfg *appConfig, window *string, stdout io.Writer) *cobra.Command {
	var from string
	var all bool
	c := &cobra.Command{
		Use:     "reset",
		Short:   "Clear a confirmation and everything downstream (or --all)",
		Args:    cobra.NoArgs,
		Example: `  easyeda workflow reset --all --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadPcbStageState(stageKeyBestEffort(cfg, *window))
			if err != nil {
				return err
			}
			var cleared []pcbStage
			if all {
				cleared = st.InvalidateFrom(stagePlacementReady, "workflow reset --all")
			} else {
				target := pcbStage(strings.TrimSpace(from))
				if pcbStageRank(target) < 0 {
					return fmt.Errorf("--from must be one of %v (or use --all)", pcbStageOrder)
				}
				cleared = st.InvalidateFrom(target, "workflow reset --from "+from)
			}
			if err := savePcbStageState(st); err != nil {
				return err
			}
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{"ok": true, "cleared": stageNames(cleared)})
		},
	}
	c.Flags().StringVar(&from, "from", "", "stage to clear from (inclusive)")
	c.Flags().BoolVar(&all, "all", false, "clear all confirmations")
	return c
}
