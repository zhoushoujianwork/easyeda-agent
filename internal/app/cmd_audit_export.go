package app

// audit export --playbook: turn a recorded session (the daemon's JSONL audit
// log) into a replayable playbook (docs/design-apply-playbook.md).
//
// The killer loop: run an exploratory session once → export the clean step
// list → commit it as a regression case for `easyeda apply`.
//
// Capture wiring: the audit log stores each action's payload AND result. When
// a later payload string equals an earlier result's primitiveId, the exporter
// rewrites it to ${VAR} and adds `capture` to the producing step — those ids
// churn per session, so raw values would never replay. Ids that were born
// BEFORE the exported window stay raw and are flagged in the step name so a
// human reviews them (known v1 boundary).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type auditRow struct {
	Ts       time.Time      `json:"ts"`
	WindowID string         `json:"windowId"`
	Action   string         `json:"action"`
	Payload  map[string]any `json:"payload"`
	OK       bool           `json:"ok"`
	Result   map[string]any `json:"result"`
}

func newAuditExportCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		dir, day, since, until   string
		window, name, project    string
		out                      string
		includeReads, includeErr bool
		noSquashSaves            bool
	)

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export a recorded session from the audit log as a replayable playbook",
		Example: `  easyeda audit export --playbook --day 2026-07-03 > replay.json
  easyeda audit export --playbook --since 14:20 --until 14:40 -o moves.playbook.json
  easyeda audit export --playbook --window <id> --name esp32-moves --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil || home == "" {
					home = os.Getenv("HOME")
				}
				dir = filepath.Join(home, ".easyeda-agent", "audit")
			}
			if day == "" {
				day = time.Now().UTC().Format("2006-01-02")
			}
			rows, err := readAuditRows(filepath.Join(dir, day+".jsonl"))
			if err != nil {
				return err
			}
			sortRowsByTs(rows)
			fromTs, toTs, err := parseAuditRange(day, since, until)
			if err != nil {
				return err
			}

			opts := exportOptions{
				window: window, from: fromTs, to: toTs,
				includeReads: includeReads, includeFailed: includeErr,
				squashSaves: !noSquashSaves,
			}
			pb, stats := exportPlaybook(rows, opts)
			if name == "" {
				name = "audit-export-" + day
			}
			pb.Meta.Name = name
			pb.Meta.Project = project
			pb.Meta.Description = fmt.Sprintf(
				"exported from audit %s (%d rows → %d steps, %d capture(s), %d raw id(s) need review)",
				day, stats.rowsScanned, len(pb.Steps), stats.captures, stats.rawIDs)

			buf, err := json.MarshalIndent(pb, "", "  ")
			if err != nil {
				return err
			}
			if out != "" {
				if err := os.WriteFile(out, append(buf, '\n'), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(stdout, "✓ %s: %d step(s), %d capture(s) wired", out, len(pb.Steps), stats.captures)
			} else {
				fmt.Fprintln(stdout, string(buf))
			}
			if stats.rawIDs > 0 {
				fmt.Fprintf(stderr, "⚠ %d step(s) reference primitive ids born OUTSIDE the exported window — they will only replay against the same board state (steps are marked \"raw-id\" in name). Widen --since or review before replay.\n", stats.rawIDs)
			}
			if out != "" {
				fmt.Fprintln(stdout)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "audit log directory (default ~/.easyeda-agent/audit)")
	cmd.Flags().StringVar(&day, "day", "", "audit day file YYYY-MM-DD, UTC (default today)")
	cmd.Flags().StringVar(&since, "since", "", "start time: RFC3339 or HH:MM[:SS] (UTC, on --day)")
	cmd.Flags().StringVar(&until, "until", "", "end time: RFC3339 or HH:MM[:SS] (UTC, on --day)")
	cmd.Flags().StringVar(&window, "window", "", "only entries from this windowId")
	cmd.Flags().StringVar(&name, "name", "", "playbook meta.name (default audit-export-<day>)")
	cmd.Flags().StringVar(&project, "project", "", "stamp meta.project (replay target)")
	cmd.Flags().StringVarP(&out, "out", "o", "", "write to file instead of stdout")
	cmd.Flags().BoolVar(&includeReads, "include-reads", false, "also export read-only actions")
	cmd.Flags().BoolVar(&includeErr, "include-failed", false, "also export failed (ok=false) actions")
	cmd.Flags().BoolVar(&noSquashSaves, "no-squash-saves", false, "keep every save (autosave storm) instead of collapsing runs")
	// --playbook is the only format today; the flag documents intent and
	// reserves room for future formats (csv/markdown timeline...).
	cmd.Flags().Bool("playbook", true, "export as an `easyeda apply` playbook (default and only format)")
	return cmd
}

func readAuditRows(path string) ([]auditRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()
	var rows []auditRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4<<20), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r auditRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // tolerate torn lines
		}
		rows = append(rows, r)
	}
	return rows, sc.Err()
}

func parseAuditRange(day, since, until string) (time.Time, time.Time, error) {
	parse := func(s string, def time.Time) (time.Time, error) {
		if s == "" {
			return def, nil
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, nil
		}
		for _, layout := range []string{"15:04:05", "15:04"} {
			if t, err := time.Parse(layout, s); err == nil {
				d, derr := time.Parse("2006-01-02", day)
				if derr != nil {
					return time.Time{}, derr
				}
				return time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC), nil
			}
		}
		return time.Time{}, fmt.Errorf("invalid time %q (want RFC3339 or HH:MM[:SS])", s)
	}
	from, err := parse(since, time.Time{})
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := parse(until, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return from, to, nil
}

type exportOptions struct {
	window        string
	from, to      time.Time
	includeReads  bool
	includeFailed bool
	squashSaves   bool
}

type exportStats struct {
	rowsScanned int
	captures    int
	rawIDs      int
}

// exportPlaybook converts filtered audit rows into playbook steps with
// capture wiring. Pure function — unit-testable.
func exportPlaybook(rows []auditRow, opts exportOptions) (*playbook, exportStats) {
	catalog := actionCatalog()
	var stats exportStats

	// skip list: session/nav noise that never belongs in a replay
	skip := map[string]bool{
		"system.health": true, "document.current": true, "project.current": true,
	}
	isSave := func(a string) bool { return a == "schematic.save" || a == "pcb.save" }

	pb := &playbook{Version: 1}
	// idIndex: result primitiveId value -> exported step index (producer)
	idIndex := map[string]int{}
	varOf := map[string]string{} // id value -> assigned var name

	for _, row := range rows {
		stats.rowsScanned++
		if opts.window != "" && row.WindowID != opts.window {
			continue
		}
		if row.Ts.Before(opts.from) || row.Ts.After(opts.to) {
			continue
		}
		if !row.OK && !opts.includeFailed {
			continue
		}
		if skip[row.Action] {
			continue
		}
		spec, known := catalog[row.Action]
		mutates := known && spec.Mutates
		// document.open / doc switches are idempotent context steps — keep them.
		contextStep := row.Action == "document.open" || row.Action == "schematic.page.open"
		if !mutates && !isSave(row.Action) && !contextStep && !opts.includeReads {
			continue
		}
		// squash save runs (debounced autosave storms)
		if isSave(row.Action) && opts.squashSaves && len(pb.Steps) > 0 {
			last := &pb.Steps[len(pb.Steps)-1]
			if last.Action == row.Action {
				continue
			}
		}

		step := playbookStep{
			ID:      fmt.Sprintf("s%d-%s", len(pb.Steps)+1, shortAction(row.Action)),
			Action:  row.Action,
			Payload: row.Payload,
		}
		if isSave(row.Action) {
			step.Checkpoint = true
		}

		// rewrite raw ids -> ${VAR} for ids produced by an earlier exported step
		rawSeen := false
		if step.Payload != nil {
			rewritten := walkStrings(step.Payload, func(s string) string {
				if prodIdx, ok := idIndex[s]; ok {
					v, has := varOf[s]
					if !has {
						v = fmt.Sprintf("ID%d", prodIdx+1)
						varOf[s] = v
						if pb.Steps[prodIdx].Capture == nil {
							pb.Steps[prodIdx].Capture = map[string]string{}
						}
						pb.Steps[prodIdx].Capture[v] = "$.primitiveId"
						stats.captures++
					}
					return "${" + v + "}"
				}
				if looksLikePrimitiveID(s) {
					rawSeen = true
				}
				return s
			})
			step.Payload = rewritten.(map[string]any)
		}
		if rawSeen {
			stats.rawIDs++
			step.Name = "raw-id: references a primitive created outside this export — review before replay"
		}

		pb.Steps = append(pb.Steps, step)

		// index this step's produced id(s) for later references
		if row.Result != nil {
			if id, ok := row.Result["primitiveId"].(string); ok && id != "" {
				idIndex[id] = len(pb.Steps) - 1
			}
			if ids, ok := row.Result["primitiveIds"].([]any); ok {
				for _, v := range ids {
					if id, ok := v.(string); ok && id != "" {
						idIndex[id] = len(pb.Steps) - 1
					}
				}
			}
		}
	}
	return pb, stats
}

// shortAction turns "schematic.component.place" into "place" for step ids.
func shortAction(a string) string {
	parts := strings.Split(a, ".")
	return parts[len(parts)-1]
}

// looksLikePrimitiveID heuristically flags EasyEDA primitive-id strings
// (16-hex like "be7fdb3c8c24fe36", or "gge" unique ids) inside payloads so the
// exporter can warn about non-replayable raw references.
func looksLikePrimitiveID(s string) bool {
	if strings.HasPrefix(s, "gge") && len(s) <= 12 {
		return true
	}
	if len(s) != 16 {
		return false
	}
	hexDigits := 0
	letters := false
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			hexDigits++
		case c >= 'a' && c <= 'f':
			hexDigits++
			letters = true
		default:
			return false
		}
	}
	return letters && hexDigits == 16
}

// sortRowsByTs keeps exported steps in wall-clock order even if the log has
// interleaved writers. Stable to preserve same-timestamp ordering.
func sortRowsByTs(rows []auditRow) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Ts.Before(rows[j].Ts) })
}
