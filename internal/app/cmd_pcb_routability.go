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
(measured rules, exact pad/copper/keepout geometry, board edge) out to --radius
beyond the pad's copper edge, and sample legal through-via sites (clearance to
every pad, every via, board slots and the copper/keepouts of every copper
layer the via crosses, pour strokes included, unfilled pour paths only as their
stroked lines; never in a pad). A reachable
same-net via counts as an existing layer change only on a two-layer board (the
via readback has no type or layer span); a reachable same-net through-hole pad
needs metallization=true and a valid drill (ROUND/SLOT, positive sizes, smaller
than the pad copper). A pad with no via site, no track
reaching the radius and no proven layer change is "trapped" and is listed with
the pads, tracks, vias, copper, slots, keepouts or board edge bounding its
reachable region; one whose only way out is an unproven transition is
"unverified" and makes the result incomplete.
Straight-edged POLYGON pads are exact copper here (the flood seeds on the
pad's own copper, which need not contain its origin); arc-edged POLYGON,
specialPad and unknown shapes still make the result incomplete.
Passing pads report via-site count, nearest site, escape directions and
existing transitions.

Exit code is non-zero for fail (any trapped pad) or incomplete (component or pad
capture incomplete, outline or copper inventory unknown, or a used rule field not
measured from the board — never treated as pass). Pass is a
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
	cmd.Flags().Float64Var(&opt.RadiusMil, "radius", pcbRoutabilityDefaultRadiusMil, "escape radius in mil beyond the pad copper edge: a track reaching it counts as an escape")
	cmd.Flags().StringArrayVar(&opt.Components, "component", nil, "only check pads of this designator (repeatable); obstacles still include the whole board")
	return cmd
}
