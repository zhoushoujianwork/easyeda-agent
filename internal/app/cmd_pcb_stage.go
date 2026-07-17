package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// cmd_pcb_stage.go — the `easyeda pcb stage` group (issue #97): human-in-the-loop
// confirmation + inspection for the persistable PCB flow gate. Confirming layout
// and outline is what unlocks the route commands, alongside the layout-lint
// routability gate (`pcb layout-lint --gate`). State is GLOBAL per project
// (~/.easyeda-agent/workflow/) so it survives cwd changes, and each confirm
// stores a DOCUMENT FINGERPRINT (placement poses / outline geometry) that the
// route gates re-verify — an edit the flow never saw invalidates the sign-off.

// newPcbStageCmd builds the `pcb stage` group.
func newPcbStageCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	stage := &cobra.Command{
		Use:   "stage",
		Short: "PCB flow stage gate: status / confirm-layout / confirm-outline / reset (issue #97)",
		Long: `Persistable PCB flow stage machine and its confirmation points.

The design-flow skill has P2 placement-confirm, P3 outline-confirm and a P6
routability gate; this makes them real: routing commands (route-short /
autoroute) refuse until BOTH outline_confirmed AND pre_route_passed are set —
enforced by the CLI AND by the daemon at /action dispatch, so raw callers are
gated too.

Progression:
  imported → placement_ready → placement_confirmed → outline_confirmed
           → pre_route_passed → routing_authorized

Confirm layout/outline HERE (after reviewing bbox, board size, edge-part
orientation, antenna keep-out and the layout-lint result); pass the routability
gate with 'pcb layout-lint --gate'. Any placement / outline mutation — a typed
action, a composite command, even a GUI drag — clears the affected confirmation:
mutating actions invalidate at the daemon, and each confirm stores a document
fingerprint the route gates re-verify, so out-of-band edits are caught too.`,
	}
	stage.AddCommand(newPcbStageStatusCmd(cfg, window, stdout))
	stage.AddCommand(newPcbStageSetAssemblyCmd(cfg, window, stdout, stderr))
	stage.AddCommand(newPcbStageConfirmLayoutCmd(cfg, window, stdout, stderr))
	stage.AddCommand(newPcbStageConfirmOutlineCmd(cfg, window, stdout, stderr))
	stage.AddCommand(newPcbStageResetCmd(cfg, window, stdout))
	return stage
}

// stageKeyBestEffort resolves the workflow state key: the live window's project
// identity when reachable, else the raw --project value. Read-only/offline
// commands (status / reset / set-assembly) use this so they still work without
// a connected window; the confirm commands require the live resolution (they
// need the window for fingerprints anyway).
func stageKeyBestEffort(cfg *appConfig, window string) string {
	if p, err := resolveStageProject(cfg, window); err == nil {
		return p
	}
	return cfg.project
}

// newPcbStageStatusCmd prints the current stage state (confirmed set + gate).
func newPcbStageStatusCmd(cfg *appConfig, window *string, stdout io.Writer) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "status",
		Short:   "Show the current PCB stage state (confirmations + routability gate)",
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb stage status --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			project := stageKeyBestEffort(cfg, *window)
			st, err := loadPcbStageState(project)
			if err != nil {
				return err
			}
			gate := checkRouteGate(st, false, false, "")
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"project":            st.Project,
					"confirmed":          st.Confirmed,
					"assembly":           st.Assembly,
					"layoutGate":         st.Layout,
					"layoutFingerprint":  st.LayoutFP,
					"outlineFingerprint": st.OutlineFP,
					"routeAllowed":       gate.Allowed,
					"missing":            gate.Missing,
				})
			}
			fmt.Fprintf(stdout, "PCB stage — project %q\n", stageProjectLabel(project))
			if st.Assembly != nil {
				fmt.Fprintf(stdout, "  assembly: %s, min gap %.1fmil, large-pad access %.1fmil\n",
					st.Assembly.Profile, st.Assembly.MinGapMil, st.Assembly.LargePadAccessMil)
			} else {
				fmt.Fprintln(stdout, "  assembly: ❌ not set (`pcb stage set-assembly`)")
			}
			for _, s := range pcbStageOrder {
				mark := "○"
				if st.Has(s) {
					mark = "●"
				}
				fmt.Fprintf(stdout, "  %s %s\n", mark, s)
			}
			if st.Layout != nil {
				fmt.Fprintf(stdout, "  layout gate: score %d (%s), %d crossings, %d tight, %d access-blocked @ %s\n",
					st.Layout.Score, st.Layout.Verdict, st.Layout.CrossingCount,
					st.Layout.TightPairs, st.Layout.AccessBlocked, st.Layout.At)
			}
			if st.LayoutFP != nil {
				fmt.Fprintf(stdout, "  layout fingerprint: %d parts @ %s\n", st.LayoutFP.Count, st.LayoutFP.At)
			}
			if st.OutlineFP != nil {
				fmt.Fprintf(stdout, "  outline fingerprint: recorded @ %s\n", st.OutlineFP.At)
			}
			if gate.Allowed {
				fmt.Fprintln(stdout, "  routing: ✅ authorized (outline_confirmed + pre_route_passed)")
			} else {
				fmt.Fprintf(stdout, "  routing: ❌ blocked — missing %s\n", strings.Join(gate.Missing, ", "))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit the state as JSON")
	return c
}

// newPcbStageSetAssemblyCmd persists the P2 assembly decision so all later
// layout gates use the same solder-access clearance instead of model memory.
func newPcbStageSetAssemblyCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var profile string
	var minGap, largePadGap float64
	c := &cobra.Command{
		Use:   "set-assembly",
		Short: "Persist assembly profile and solder-access clearances (issue #99)",
		Args:  cobra.NoArgs,
		Example: `  easyeda pcb stage set-assembly --profile hand-solder --min-gap 40 --large-pad-access 60 --project ceshi
  easyeda pcb stage set-assembly --profile reflow --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			profile = strings.ToLower(strings.TrimSpace(profile))
			if profile != "hand-solder" && profile != "reflow" {
				return fmt.Errorf("--profile must be hand-solder or reflow")
			}
			if profile == "hand-solder" {
				if minGap == 0 {
					minGap = 40
				}
				if minGap < 40 {
					return fmt.Errorf("hand-solder --min-gap must be >=40mil")
				}
				if largePadGap == 0 {
					largePadGap = 60
				}
				if largePadGap < minGap {
					return fmt.Errorf("--large-pad-access must be >= --min-gap")
				}
			}
			st, err := loadPcbStageState(stageKeyBestEffort(cfg, *window))
			if err != nil {
				return err
			}
			st.Assembly = &pcbAssemblyProfile{Profile: profile, MinGapMil: minGap,
				LargePadAccessMil: largePadGap, At: time.Now().Format(time.RFC3339)}
			st.InvalidateFrom(stagePlacementConfirmed, "assembly profile changed")
			if err := savePcbStageState(st); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "✓ assembly profile set: %s (min-gap %.1fmil, large-pad %.1fmil)\n",
				profile, minGap, largePadGap)
			return nil
		},
	}
	c.Flags().StringVar(&profile, "profile", "", "assembly process: hand-solder | reflow (required)")
	c.Flags().Float64Var(&minGap, "min-gap", 0, "general component gap in mil (hand-solder default/minimum 40)")
	c.Flags().Float64Var(&largePadGap, "large-pad-access", 0, "iron access corridor for large pads in mil (hand-solder default 60)")
	_ = c.MarkFlagRequired("profile")
	return c
}

// stageProjectLabel yields a display label when --project is empty.
func stageProjectLabel(p string) string {
	if strings.TrimSpace(p) == "" {
		return "(active window)"
	}
	return p
}

// newPcbStageConfirmLayoutCmd confirms placement_ready + placement_confirmed —
// the P2 human sign-off that the placement (bbox, edge-part orientation, antenna
// keep-out) is what the user wants. Requires a live window: the confirmation is
// pinned to the CURRENT placement by fingerprint, so a later out-of-band move
// (GUI drag / exec_js / another agent) is detected and invalidates it.
func newPcbStageConfirmLayoutCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var note string
	c := &cobra.Command{
		Use:   "confirm-layout",
		Short: "Confirm the placement (P2): sets placement_confirmed (pinned by fingerprint)",
		Long: `Record the user's sign-off on the component placement. Before confirming,
review the real bbox (` + "`pcb list --include-bbox`" + `), board size, edge-part
orientation (connector openings / antenna end facing out), antenna keep-out, and
the ` + "`pcb layout-lint`" + ` result. This sets placement_ready + placement_confirmed
and stores a fingerprint of the live placement (designator/x/y/rotation/layer):
route gates re-verify it, so any later move invalidates this confirmation.
It does NOT authorize routing on its own — the outline must also be confirmed and
the routability gate passed.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb stage confirm-layout --project ceshi --note "USB-C opening out, antenna at top edge"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStageConfirmLayout(cfg, *window, note, stderr)
		},
	}
	c.Flags().StringVar(&note, "note", "", "what was reviewed/confirmed (recorded in the audit trail)")
	return c
}

// runStageConfirmLayout is the P2 sign-off implementation, shared by
// `pcb stage confirm-layout` and `workflow confirm layout`.
func runStageConfirmLayout(cfg *appConfig, window, note string, stderr io.Writer) error {
	project, err := resolveStageProject(cfg, window)
	if err != nil {
		return fmt.Errorf("confirm-layout needs a connected window (the confirmation is fingerprinted against the live placement): %w", err)
	}
	st, err := loadPcbStageState(project)
	if err != nil {
		return err
	}
	if st.Assembly == nil {
		fmt.Fprintln(stderr, "❌ set the assembly profile first (`pcb stage set-assembly --profile hand-solder|reflow`)")
		return errActionFailed
	}
	if st.Layout == nil || st.Layout.TightPairs != 0 || st.Layout.MinGapMil < st.Assembly.MinGapMil {
		fmt.Fprintf(stderr, "❌ placement assembly gate not passed — run `pcb layout-lint --gate` using the persisted %s profile first\n", st.Assembly.Profile)
		return errActionFailed
	}
	if st.Layout.AccessBlocked != 0 {
		fmt.Fprintf(stderr, "❌ %d component(s) have no %.1fmil iron-access side — free at least one flank per part (`pcb layout-lint --gate` lists them), then re-gate\n",
			st.Layout.AccessBlocked, st.Layout.AccessMil)
		return errActionFailed
	}
	fp, err := pullLayoutFingerprint(cfg, window)
	if err != nil {
		return fmt.Errorf("confirm-layout: %w", err)
	}
	if fp.Count == 0 {
		fmt.Fprintln(stderr, "❌ the active PCB has no components — nothing to confirm (run `pcb import-changes` first)")
		return errActionFailed
	}
	st.Confirm(stagePlacementReady, "confirm", note)
	st.Confirm(stagePlacementConfirmed, "confirm", note)
	st.LayoutFP = fp
	if err := savePcbStageState(st); err != nil {
		return err
	}
	// The confirmation summary the user signs off on (issue #99 item 7).
	fmt.Fprintf(stderr, "✓ placement confirmed for %q — fingerprinted %d parts\n", project, fp.Count)
	fmt.Fprintf(stderr, "  assembly %s · min gap %.1fmil · tight pairs %d · iron-access blocked %d (corridor %.1fmil) · lint score %d\n",
		st.Assembly.Profile, st.Layout.MinGapMil, st.Layout.TightPairs,
		st.Layout.AccessBlocked, st.Layout.AccessMil, st.Layout.Score)
	return nil
}

// newPcbStageConfirmOutlineCmd confirms outline_confirmed — the P3 board-frame
// sign-off (board size, edge-part protrusion, mounting-hole clearance).
func newPcbStageConfirmOutlineCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var note string
	c := &cobra.Command{
		Use:   "confirm-outline",
		Short: "Confirm the board outline (P3): sets outline_confirmed (pinned by fingerprint)",
		Long: `Record the user's sign-off on the board outline / frame. Requires the
placement to be confirmed first (confirm-layout), because the outline is fit to
the placement — and the placement fingerprint is re-verified here, so a move
since confirm-layout sends you back to P2. Review board dimensions,
edge-connector protrusion (~0.5–1mm past the edge) and mounting-hole clearance
before confirming. Stores an outline fingerprint the route gates re-verify.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb stage confirm-outline --project ceshi --note "40×25mm, USB-C 0.8mm proud"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStageConfirmOutline(cfg, *window, note, stderr)
		},
	}
	c.Flags().StringVar(&note, "note", "", "what was reviewed/confirmed (recorded in the audit trail)")
	return c
}

// runStageConfirmOutline is the P3 sign-off implementation, shared by
// `pcb stage confirm-outline` and `workflow confirm outline`.
func runStageConfirmOutline(cfg *appConfig, window, note string, stderr io.Writer) error {
	project, err := resolveStageProject(cfg, window)
	if err != nil {
		return fmt.Errorf("confirm-outline needs a connected window (the confirmation is fingerprinted against the live outline): %w", err)
	}
	st, err := loadPcbStageState(project)
	if err != nil {
		return err
	}
	// Re-verify the placement fingerprint: an out-of-band move since
	// confirm-layout must send the flow back to P2, not ride into P3.
	drift, derr := verifyStageFingerprints(cfg, window, st)
	if derr != nil {
		return fmt.Errorf("confirm-outline: %w", derr)
	}
	if len(drift) > 0 {
		_ = savePcbStageState(st)
		for _, d := range drift {
			fmt.Fprintf(stderr, "⚠️  %s\n", d)
		}
	}
	if !st.Has(stagePlacementConfirmed) {
		fmt.Fprintf(stderr, "❌ confirm the placement first (`pcb stage confirm-layout`) — the outline is fit to it.\n")
		return errActionFailed
	}
	fp, err := pullOutlineFingerprint(cfg, window)
	if err != nil {
		return fmt.Errorf("confirm-outline: %w", err)
	}
	if fp.Count == 0 {
		fmt.Fprintln(stderr, "❌ the active PCB has no board outline — draw one first (`pcb outline-fit` / pcb.outline.set)")
		return errActionFailed
	}
	st.Confirm(stageOutlineConfirmed, "confirm", note)
	st.OutlineFP = fp
	if err := savePcbStageState(st); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "✓ outline confirmed for %q — fingerprint recorded\n", project)
	return nil
}

// newPcbStageResetCmd clears a stage (and everything downstream) — the manual
// invalidate for when the user changes their mind or restarts the flow.
func newPcbStageResetCmd(cfg *appConfig, window *string, stdout io.Writer) *cobra.Command {
	var from string
	var all bool
	c := &cobra.Command{
		Use:   "reset",
		Short: "Clear a confirmation and everything downstream (or --all)",
		Long: `Clear stage confirmations. --from <stage> clears that stage and every stage
after it; --all wipes the whole record back to imported. Use when restarting the
flow or after a manual edit the tool didn't see.`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb stage reset --all --project ceshi
  easyeda pcb stage reset --from placement_confirmed --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadPcbStageState(stageKeyBestEffort(cfg, *window))
			if err != nil {
				return err
			}
			var cleared []pcbStage
			if all {
				cleared = st.InvalidateFrom(stagePlacementReady, "manual reset --all")
			} else {
				target := pcbStage(strings.TrimSpace(from))
				if pcbStageRank(target) < 0 {
					return fmt.Errorf("--from must be one of %v (or use --all)", pcbStageOrder)
				}
				cleared = st.InvalidateFrom(target, "manual reset --from "+from)
			}
			if err := savePcbStageState(st); err != nil {
				return err
			}
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			out := make([]string, len(cleared))
			for i, c := range cleared {
				out[i] = string(c)
			}
			return enc.Encode(map[string]any{"ok": true, "cleared": out})
		},
	}
	c.Flags().StringVar(&from, "from", "", "stage to clear from (inclusive)")
	c.Flags().BoolVar(&all, "all", false, "clear all confirmations")
	return c
}
