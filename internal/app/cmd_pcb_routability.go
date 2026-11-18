package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// No appConfig/window is accepted: this constructor cannot dispatch an editor
// action. Fail and incomplete results still write the JSON report.
func newPcbRoutabilityCmd(stdout, stderr io.Writer) *cobra.Command {
	var boardPath, outPath string
	opt := pcbRoutabilityOptions{StepMil: pcbRoutabilityDefaultStepMil, RadiusMil: pcbRoutabilityDefaultRadiusMil}
	cmd := &cobra.Command{
		Use:   "routability",
		Short: "Find pads with no legal via site and no track escape before routing (offline, read-only)",
		Long: `Pre-routing escape check over a fresh 'pcb dump --include-copper' snapshot.
For every netted SMD pad, flood its own copper layer with a minimum-width track
(live rules, exact pad/copper/keepout geometry, board edge) out to --radius and
sample legal through-via sites (clearance to every pad, via and TOP/BOTTOM
copper; never in a pad). A pad with neither a via site nor a track reaching the
radius is "trapped" and is listed with the pads, tracks, vias, copper, keepouts
or board edge bounding its reachable region. Passing pads report via-site count,
nearest site and escape directions.

Exit code is non-zero for fail (any trapped pad) or incomplete (missing rules,
outline, copper inventory or pad geometry — never treated as pass). Pass is a
local necessary condition: it does not reserve shared channels or prove the
board routes. Run before layout confirmation and again after critical nets
are pre-routed; re-run after any geometry or rule change.`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb dump --include-copper --out board.json
  easyeda pcb routability --board board.json --out routability.json
  easyeda pcb routability --board board.json --out j1.json --component J1 --component U3`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if boardPath == "" || outPath == "" {
				return fmt.Errorf("--board and --out are required")
			}
			if err := rejectLayoutOutputAliases([]string{boardPath}, []string{outPath}); err != nil {
				return err
			}
			raw, err := os.ReadFile(boardPath)
			if err != nil {
				return err
			}
			snap, err := loadBoardSnapshotFile(bytes.NewReader(raw))
			if err != nil {
				return err
			}
			report, checkErr := analyzePCBRoutability(snap, opt)
			report.BoardSHA256 = sha256String(raw)
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			if err := writeJSONAtomic(outPath, report); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "pcb routability: %s; %d checked, %d trapped → %s\n", report.Status, report.Summary.Checked, report.Summary.Trapped, outPath)
			for _, pad := range report.Pads {
				if pad.Status != "trapped" {
					continue
				}
				fmt.Fprintf(stderr, "  trapped %s (%s, layer %d) blocked by:", pad.Pad, pad.Net, pad.Layer)
				for _, b := range pad.Blockers {
					label := b.Ref
					if label == "" {
						label = b.ID
					}
					fmt.Fprintf(stderr, " %s:%s", b.Kind, label)
				}
				fmt.Fprintln(stderr)
			}
			fmt.Fprintln(stdout, outPath)
			if checkErr != nil {
				return fmt.Errorf("routability %s: %w", report.Status, checkErr)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&boardPath, "board", "", "fresh pcb dump --include-copper JSON")
	cmd.Flags().StringVar(&outPath, "out", "", "routability report JSON path")
	cmd.Flags().Float64Var(&opt.StepMil, "step", pcbRoutabilityDefaultStepMil, "flood grid step in mil (0.5..10); finer finds narrower channels")
	cmd.Flags().Float64Var(&opt.RadiusMil, "radius", pcbRoutabilityDefaultRadiusMil, "escape radius in mil: a track reaching it counts as an escape")
	cmd.Flags().StringArrayVar(&opt.Components, "component", nil, "only check pads of this designator (repeatable); obstacles still include the whole board")
	return cmd
}
