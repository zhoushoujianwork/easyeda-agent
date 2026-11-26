package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// loadPCBFanoutPlan reads the escape-plan report and the dump it was computed
// on, and converts them into a verified plan-apply target.
func loadPCBFanoutPlan(escapesPath, boardPath string) (*pcbFanoutPlan, error) {
	escRaw, err := os.ReadFile(escapesPath)
	if err != nil {
		return nil, err
	}
	var esc pcbEscapeReport
	if err := json.Unmarshal(escRaw, &esc); err != nil {
		return nil, fmt.Errorf("parse escape report %s: %w", escapesPath, err)
	}
	boardRaw, err := os.ReadFile(boardPath)
	if err != nil {
		return nil, err
	}
	snap, err := loadBoardSnapshotFile(bytes.NewReader(boardRaw))
	if err != nil {
		return nil, err
	}
	return buildPCBFanoutPlan(&esc, snap)
}

func newPcbFanoutCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var escapesPath, boardPath, projectUUID string
	var passes int
	var dryRun, lock bool
	c := &cobra.Command{
		Use:   "fanout",
		Short: "Write a passing escape-plan as real tracks and vias, prove them by readback and lock them",
		Long: `Turn the reservations of a passing 'pcb escape-plan' report into copper, ahead
of signal routing, so the official router ('pcb autoroute') continues from the
fanout end points.

Each reserved polyline becomes consecutive straight tracks (zero-length pieces
skipped) with the route's width on its EasyEDA layer; each via is created at
its position with the hole/diameter from the escape report, or the board's
measured via rules when the report has none. The plan is written through the
same path as 'pcb plan-apply' (typed create, readback, per net/layer
reconcile of track length, width-weighted length and via positions, at most
--passes clear-and-rewrite passes). Via hole and diameter are compared after
readback as well.

Refused before any write:
  - the escape report status is not "pass", or its boardSemanticSha256 differs
    from --board (use the dump the escape plan was computed on);
  - the report reuses an existing via (fanout writes only new copper);
  - any fanout net already carries tracks, arcs or vias, in the dump or on the
    live board. plan-apply replaces the copper of its nets, so existing routing
    would be deleted: run 'easyeda pcb rip-up --net <net>' first, then save,
    reload, dump and re-run escape-plan.

After a proven match and with --lock (default on) exactly the created
primitives are locked ('pcb.track.lock' by primitive id), so the router keeps
them. The report lists fanoutNets, segments, vias and locked. A lock failure
keeps the report and exits non-zero. --dry-run reads and compares only.

The command does not save, rebuild pours or run DRC. Save → 'easyeda doc reload'
→ a fresh 'pcb dump --include-copper' must follow before autoroute or
'pcb routability'. A non-zero exit means the board was not proven to match; do
not save.`,
		Example: `  easyeda pcb dump --include-copper --out board.json
  easyeda pcb escape-plan --from layout.json --board board.json --out escapes.json
  easyeda pcb fanout --escapes escapes.json --board board.json --project-uuid <uuid> --dry-run
  easyeda pcb fanout --escapes escapes.json --board board.json --project-uuid <uuid>
  easyeda pcb save && easyeda doc reload`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if escapesPath == "" || boardPath == "" || strings.TrimSpace(projectUUID) == "" {
				return fmt.Errorf("--escapes, --board and --project-uuid are required")
			}
			if passes < 1 || passes > 5 {
				return fmt.Errorf("--passes must be 1..5")
			}
			plan, err := loadPCBFanoutPlan(escapesPath, boardPath)
			if err != nil {
				return err
			}
			live := &planApplyLive{cfg: cfg, window: *window, projectUUID: strings.TrimSpace(projectUUID)}
			rep, runErr := runPCBFanout(live, plan, passes, dryRun, lock)
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(rep); err != nil {
				return err
			}
			if runErr != nil {
				fmt.Fprintf(stderr, "pcb fanout: %v\n", runErr)
			}
			return runErr
		},
	}
	c.Flags().StringVar(&escapesPath, "escapes", "", "pcb escape-plan report JSON, status must be pass (required)")
	c.Flags().StringVar(&boardPath, "board", "", "the pcb dump --include-copper the escape plan was computed on (required)")
	c.Flags().StringVar(&projectUUID, "project-uuid", "", "expected project UUID of the target window (required); a mismatch stops the run")
	c.Flags().BoolVar(&lock, "lock", true, "lock exactly the created tracks and vias after the readback proves them")
	c.Flags().IntVar(&passes, "passes", 3, "maximum clear-and-rewrite passes (1..5)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "read and compare only; report what would be created, write and lock nothing")
	return c
}
