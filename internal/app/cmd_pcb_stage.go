package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// cmd_pcb_stage.go — the `easyeda pcb stage` group (issue #97): human-in-the-loop
// confirmation + inspection for the persistable PCB flow gate. Confirming layout
// and outline is what unlocks the route commands, alongside the layout-lint
// routability gate (`pcb layout-lint --gate`). State lives per project under
// <cwd>/.easyeda/pcb-stage/ — no daemon, no DB.

// newPcbStageCmd builds the `pcb stage` group.
func newPcbStageCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	stage := &cobra.Command{
		Use:   "stage",
		Short: "PCB flow stage gate: status / confirm-layout / confirm-outline / reset (issue #97)",
		Long: `Persistable PCB flow stage machine and its confirmation points.

The design-flow skill has P2 placement-confirm, P3 outline-confirm and a P6
routability gate; this makes them real: routing commands (route-short /
autoroute) refuse until BOTH outline_confirmed AND pre_route_passed are set.

Progression:
  imported → placement_ready → placement_confirmed → outline_confirmed
           → pre_route_passed → routing_authorized

Confirm layout/outline HERE (after reviewing bbox, board size, edge-part
orientation, antenna keep-out and the layout-lint result); pass the routability
gate with 'pcb layout-lint --gate'. Any placement / outline mutation
(place-constrained / outline-fit / outline-round) clears the affected
confirmation, so a moved part or resized board must be re-confirmed.`,
	}
	stage.AddCommand(newPcbStageStatusCmd(cfg, stdout))
	stage.AddCommand(newPcbStageConfirmLayoutCmd(cfg, stdout, stderr))
	stage.AddCommand(newPcbStageConfirmOutlineCmd(cfg, stdout, stderr))
	stage.AddCommand(newPcbStageResetCmd(cfg, stdout))
	return stage
}

// newPcbStageStatusCmd prints the current stage state (confirmed set + gate).
func newPcbStageStatusCmd(cfg *appConfig, stdout io.Writer) *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "status",
		Short:   "Show the current PCB stage state (confirmations + routability gate)",
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb stage status --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadPcbStageState(cfg.project)
			if err != nil {
				return err
			}
			gate := checkRouteGate(st, false, "")
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"project":      st.Project,
					"confirmed":    st.Confirmed,
					"layoutGate":   st.Layout,
					"routeAllowed": gate.Allowed,
					"missing":      gate.Missing,
				})
			}
			fmt.Fprintf(stdout, "PCB stage — project %q\n", stageProjectLabel(cfg.project))
			for _, s := range pcbStageOrder {
				mark := "○"
				if st.has(s) {
					mark = "●"
				}
				fmt.Fprintf(stdout, "  %s %s\n", mark, s)
			}
			if st.Layout != nil {
				fmt.Fprintf(stdout, "  layout gate: score %d (%s), %d crossings @ %s\n",
					st.Layout.Score, st.Layout.Verdict, st.Layout.CrossingCount, st.Layout.At)
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

// stageProjectLabel yields a display label when --project is empty.
func stageProjectLabel(p string) string {
	if strings.TrimSpace(p) == "" {
		return "(active window)"
	}
	return p
}

// newPcbStageConfirmLayoutCmd confirms placement_ready + placement_confirmed —
// the P2 human sign-off that the placement (bbox, edge-part orientation, antenna
// keep-out) is what the user wants.
func newPcbStageConfirmLayoutCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var note string
	c := &cobra.Command{
		Use:   "confirm-layout",
		Short: "Confirm the placement (P2): sets placement_confirmed",
		Long: `Record the user's sign-off on the component placement. Before confirming,
review the real bbox (` + "`pcb list --include-bbox`" + `), board size, edge-part
orientation (connector openings / antenna end facing out), antenna keep-out, and
the ` + "`pcb layout-lint`" + ` result. This sets placement_ready + placement_confirmed;
it does NOT authorize routing on its own — the outline must also be confirmed and
the routability gate passed.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb stage confirm-layout --project ceshi --note "USB-C opening out, antenna at top edge"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadPcbStageState(cfg.project)
			if err != nil {
				return err
			}
			st.confirmStage(stagePlacementReady, "confirm", note)
			st.confirmStage(stagePlacementConfirmed, "confirm", note)
			if err := savePcbStageState(st); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "✓ placement confirmed for %q\n", stageProjectLabel(cfg.project))
			return nil
		},
	}
	c.Flags().StringVar(&note, "note", "", "what was reviewed/confirmed (recorded in the audit trail)")
	return c
}

// newPcbStageConfirmOutlineCmd confirms outline_confirmed — the P3 board-frame
// sign-off (board size, edge-part protrusion, mounting-hole clearance).
func newPcbStageConfirmOutlineCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var note string
	c := &cobra.Command{
		Use:   "confirm-outline",
		Short: "Confirm the board outline (P3): sets outline_confirmed",
		Long: `Record the user's sign-off on the board outline / frame. Requires the
placement to be confirmed first (confirm-layout), because the outline is fit to
the placement. Review board dimensions, edge-connector protrusion (~0.5–1mm past
the edge) and mounting-hole clearance before confirming.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb stage confirm-outline --project ceshi --note "40×25mm, USB-C 0.8mm proud"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := loadPcbStageState(cfg.project)
			if err != nil {
				return err
			}
			if !st.has(stagePlacementConfirmed) {
				fmt.Fprintf(stderr, "❌ confirm the placement first (`pcb stage confirm-layout`) — the outline is fit to it.\n")
				return errActionFailed
			}
			st.confirmStage(stageOutlineConfirmed, "confirm", note)
			if err := savePcbStageState(st); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "✓ outline confirmed for %q\n", stageProjectLabel(cfg.project))
			return nil
		},
	}
	c.Flags().StringVar(&note, "note", "", "what was reviewed/confirmed (recorded in the audit trail)")
	return c
}

// newPcbStageResetCmd clears a stage (and everything downstream) — the manual
// invalidate for when the user changes their mind or restarts the flow.
func newPcbStageResetCmd(cfg *appConfig, stdout io.Writer) *cobra.Command {
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
			st, err := loadPcbStageState(cfg.project)
			if err != nil {
				return err
			}
			var cleared []pcbStage
			if all {
				cleared = st.invalidateFrom(stagePlacementReady, "manual reset --all")
			} else {
				target := pcbStage(strings.TrimSpace(from))
				if pcbStageRank(target) < 0 {
					return fmt.Errorf("--from must be one of %v (or use --all)", pcbStageOrder)
				}
				cleared = st.invalidateFrom(target, "manual reset --from "+from)
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
