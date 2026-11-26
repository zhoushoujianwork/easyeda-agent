package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// No appConfig/window is accepted: this constructor cannot dispatch an editor
// action. Fail and incomplete results still write the JSON report.
func newPcbRouteDiagnoseCmd(stdout, stderr io.Writer) *cobra.Command {
	var boardPath, drcPath, outPath string
	opt := pcbRouteDiagnoseOptions{StepMil: pcbRoutabilityDefaultStepMil, RadiusMil: pcbRoutabilityDefaultRadiusMil}
	cmd := &cobra.Command{
		Use:   "route-diagnose",
		Short: "Explain why nets stay unrouted after auto-routing: trapped pads and their blockers (offline, read-only)",
		Long: `Diagnose the nets the native DRC reports as unrouted. Inputs are a fresh
'pcb dump --include-copper' (--board) and the 'pcb drc --json' output of the same
saved and reloaded state (--drc). The command cannot verify that the two match:
run save, doc reload, pcb drc --json and pcb dump back to back, and re-run after
any write.

Unrouted nets are the distinct nets of the DRC rows whose rule is
"Connection Error" (--net narrows them). The command does not rebuild
whole-net connectivity. For each unrouted net it floods every netted SMD pad
(layer 1/2) with the same exact geometry, measured rules and coverage gate as
'pcb routability' (--step/--radius have the same meaning and limits); through-hole
pads are counted, not flooded. Per net:
  trapped              at least one pad has neither a track escape nor a legal via
                       site; the pads and the union of their blockers (pads,
                       tracks, vias, copper, keepouts, slots, board edge) are listed
  not-locally-trapped  every pad can leave; the break is in the channels between
                       pads. The report does not say which channel; drcObjs lists
                       the objects the DRC rows name. Go back to fanout or change
                       the layer plan; do not relax rules
  unknown              geometry or pad capture is incomplete, or a pad is unverified;
                       never treated as a pass
Every net also carries rows (Connection Error rows) and drcObjs (their primitive ids).

Report: {schemaVersion, status, boardSemanticSha256, parameters, nets[], limitations}.
status is incomplete when any net is unknown (or geometry is incomplete, or
Connection Error rows have no net name), else fail when any net is unrouted, else
pass (no Connection Error rows). Exit code is non-zero unless status is pass.
Without --out the report JSON goes to stdout.`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb save && easyeda doc reload
  easyeda pcb drc --json > drc.json
  easyeda pcb dump --include-copper --out board.json
  easyeda pcb route-diagnose --board board.json --drc drc.json --out diagnose.json
  easyeda pcb route-diagnose --board board.json --drc drc.json --net USB_DP --net USB_DM --out usb.json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if boardPath == "" || drcPath == "" {
				return fmt.Errorf("--board and --drc are required")
			}
			if outPath != "" {
				if err := rejectLayoutOutputAliases([]string{boardPath, drcPath}, []string{outPath}); err != nil {
					return err
				}
			}
			boardRaw, err := os.ReadFile(boardPath)
			if err != nil {
				return err
			}
			drcRaw, err := os.ReadFile(drcPath)
			if err != nil {
				return err
			}
			snap, err := loadBoardSnapshotFile(bytes.NewReader(boardRaw))
			if err != nil {
				return err
			}
			drc, err := loadPCBDrcFlatReport(drcRaw)
			if err != nil {
				return err
			}
			report, checkErr := diagnosePCBRoutes(snap, drc, opt)
			report.BoardSHA256, report.DRCSHA256 = sha256String(boardRaw), sha256String(drcRaw)
			if outPath != "" {
				if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
					return err
				}
				if err := writeJSONAtomic(outPath, report); err != nil {
					return err
				}
			}
			dest := outPath
			if dest == "" {
				dest = "stdout"
			}
			fmt.Fprintf(stderr, "pcb route-diagnose: %s; %d unrouted net(s), %d Connection Error row(s) → %s\n", report.Status, len(report.Nets), report.ConnectionRows, dest)
			for _, n := range report.Nets {
				fmt.Fprintf(stderr, "  %s: %s (%d row(s), %d pad(s) flooded)", n.Net, n.Reason, n.Rows, n.PadsChecked)
				if n.Reason == pcbRouteDiagnoseTrapped {
					fmt.Fprint(stderr, "; trapped")
					for _, p := range n.Pads {
						fmt.Fprintf(stderr, " %s", p.Pad)
					}
					fmt.Fprint(stderr, "; blocked by:")
					for _, b := range n.Blockers {
						label := b.Ref
						if label == "" {
							label = b.ID
						}
						fmt.Fprintf(stderr, " %s:%s", b.Kind, label)
					}
				}
				if n.Detail != "" {
					fmt.Fprintf(stderr, "; %s", n.Detail)
				}
				fmt.Fprintln(stderr)
			}
			if outPath != "" {
				fmt.Fprintln(stdout, outPath)
			} else {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			}
			if checkErr != nil {
				return fmt.Errorf("route-diagnose %s: %w", report.Status, checkErr)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&boardPath, "board", "", "fresh pcb dump --include-copper JSON (required)")
	cmd.Flags().StringVar(&drcPath, "drc", "", "pcb drc --json output of the same saved+reloaded state (required)")
	cmd.Flags().StringArrayVar(&opt.Nets, "net", nil, "only diagnose this net (repeatable); default: every net with a Connection Error")
	cmd.Flags().StringVar(&outPath, "out", "", "report JSON path (default: print the report to stdout)")
	cmd.Flags().Float64Var(&opt.StepMil, "step", pcbRoutabilityDefaultStepMil, "flood grid step in mil (0.5..10); finer finds narrower channels")
	cmd.Flags().Float64Var(&opt.RadiusMil, "radius", pcbRoutabilityDefaultRadiusMil, "escape radius in mil beyond the pad copper edge: a track reaching it counts as an escape")
	return cmd
}
