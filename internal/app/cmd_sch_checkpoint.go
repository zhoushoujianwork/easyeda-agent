package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// sourceDifference compares two page sources by what they describe rather than
// byte for byte. Rewriting a page makes the editor re-expand library attributes
// of the parts it re-creates and renumber tickets, so equal bytes are not a
// usable success test for a rollback; identity, geometry and attribute values are.
type sourceDifference struct {
	Missing           []string `json:"missing"`           // in the checkpoint, not on the page
	Extra             []string `json:"extra"`             // on the page, not in the checkpoint
	Changed           []string `json:"changed"`           // same primitive, different content
	AttributesChanged []string `json:"attributesChanged"` // parent/key whose value differs or is gone
	AttributesAdded   int      `json:"attributesAdded"`   // attributes the editor materialised
	// DeviceAttributeReset counts re-created parts whose "Device" attribute the
	// editor emptied. Observed live on every restored part; the part's device
	// binding in the COMPONENT record is unaffected, so it does not fail the check.
	DeviceAttributeReset int `json:"deviceAttributeReset"`
}

func (d sourceDifference) equivalent() bool {
	return len(d.Missing)+len(d.Extra)+len(d.Changed)+len(d.AttributesChanged) == 0
}

type sourceRecords struct {
	primitives map[string][]string // type/id → canonical bodies
	attributes map[string]string   // parent/key → canonical value
}

func parseSourceRecords(source string) (sourceRecords, error) {
	out := sourceRecords{primitives: map[string][]string{}, attributes: map[string]string{}}
	for n, line := range strings.Split(sourceBody(source), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		headText, bodyText, ok := strings.Cut(line, "||")
		var head struct{ Type, ID string }
		var body map[string]any
		if !ok || json.Unmarshal([]byte(headText), &head) != nil || head.Type == "" ||
			json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimRight(bodyText, "\r"), "|")), &body) != nil {
			return out, fmt.Errorf("record %d does not use the known head||body| form", n+2)
		}
		if head.Type == "ATTR" {
			value, _ := json.Marshal(body["value"])
			out.attributes[fmt.Sprint(body["parentId"])+"/"+fmt.Sprint(body["key"])] = string(value)
			continue
		}
		// The nested attrs object is library metadata the editor rewrites on
		// re-creation; placement and wiring live in the other fields.
		delete(body, "attrs")
		canonical, _ := json.Marshal(body)
		key := head.Type + "/" + head.ID
		out.primitives[key] = append(out.primitives[key], string(canonical))
	}
	return out, nil
}

func compareSources(checkpoint, page string) (sourceDifference, error) {
	diff := sourceDifference{Missing: []string{}, Extra: []string{}, Changed: []string{}, AttributesChanged: []string{}}
	want, err := parseSourceRecords(checkpoint)
	if err != nil {
		return diff, fmt.Errorf("checkpoint: %w", err)
	}
	got, err := parseSourceRecords(page)
	if err != nil {
		return diff, fmt.Errorf("page: %w", err)
	}
	for key, bodies := range want.primitives {
		other, ok := got.primitives[key]
		switch {
		case !ok:
			diff.Missing = append(diff.Missing, key)
		default:
			sort.Strings(bodies)
			sort.Strings(other)
			if strings.Join(bodies, "\n") != strings.Join(other, "\n") {
				diff.Changed = append(diff.Changed, key)
			}
		}
	}
	for key := range got.primitives {
		if _, ok := want.primitives[key]; !ok {
			diff.Extra = append(diff.Extra, key)
		}
	}
	for key, value := range want.attributes {
		other, ok := got.attributes[key]
		switch {
		case ok && other == value:
		case ok && other == `""` && strings.HasSuffix(key, "/Device"):
			diff.DeviceAttributeReset++
		default:
			diff.AttributesChanged = append(diff.AttributesChanged, key)
		}
	}
	for key := range got.attributes {
		if _, ok := want.attributes[key]; !ok {
			diff.AttributesAdded++
		}
	}
	for _, list := range []*[]string{&diff.Missing, &diff.Extra, &diff.Changed, &diff.AttributesChanged} {
		sort.Strings(*list)
	}
	return diff, nil
}

// activeSchematicPage is the page a checkpoint command acts on: the one the
// window shows right now, read live.
func activeSchematicPage(cfg *appConfig, window string) (project, document string, err error) {
	cur, err := requestAction(cfg, "document.current", window, nil)
	if err != nil {
		return "", "", err
	}
	if cur.Context == nil || cur.Context.ProjectUUID == "" || cur.Context.DocumentUUID == "" || cur.Context.DocumentType != "schematic" {
		return "", "", fmt.Errorf("the active document is not a schematic page with readable identity; open the page first")
	}
	return cur.Context.ProjectUUID, cur.Context.DocumentUUID, nil
}

func newSchCheckpointCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	group := &cobra.Command{Use: "checkpoint", Short: "List and roll back to the page states recorded before each write command",
		Long: "Every command that writes a schematic page first records that page (EASYEDA_CHECKPOINT=off disables it).\n" +
			"A checkpoint is the page BEFORE its command: restoring it undoes that command and every later one.\n" +
			"Restoring records the current page first, so a restore can itself be undone.\n" +
			"Storage: ~/.easyeda-agent/checkpoints or EASYEDA_CHECKPOINT_DIR; EASYEDA_CHECKPOINT_LABEL stores a caller id with each entry.\n" +
			"Schematic pages only. Pages added or deleted by a command are not restored."}
	var project, document string
	list := &cobra.Command{Use: "list", Short: "List the checkpoints of the active page, oldest first", Args: cobra.NoArgs,
		Example: "  easyeda sch checkpoint list --window W\n  easyeda sch checkpoint list --project-uuid P --document-uuid D",
		RunE: func(cmd *cobra.Command, args []string) error {
			if (project == "") != (document == "") {
				return fmt.Errorf("--project-uuid and --document-uuid go together")
			}
			if project == "" {
				w, err := resolveTargetWindow(cfg, *window)
				if err != nil {
					return err
				}
				if project, document, err = activeSchematicPage(cfg, w); err != nil {
					return err
				}
			}
			store, err := openCheckpointStore(defaultCheckpointDir(), project, document)
			if err != nil {
				return err
			}
			entries, err := store.entries()
			if err != nil {
				return err
			}
			if entries == nil {
				entries = []checkpointEntry{}
			}
			return writeJSON(stdout, map[string]any{"project": project, "document": document, "count": len(entries), "checkpoints": entries})
		}}
	list.Flags().StringVar(&project, "project-uuid", "", "read a page's history without a connected editor")
	list.Flags().StringVar(&document, "document-uuid", "", "schematic page UUID (with --project-uuid)")

	var dryRun bool
	restore := &cobra.Command{Use: "restore <checkpoint-id>", Short: "Roll the active page back to the state before a recorded command", Args: cobra.ExactArgs(1),
		Long: "Write the checkpoint's page source back once and compare the result with the checkpoint by primitive identity,\n" +
			"geometry and attribute values. The page as it is now is recorded first; its id is returned as undoCheckpoint.\n" +
			"Primitive IDs read before the restore may be stale: read the page again before any further write.\n" +
			"Nothing is saved. Save, reload and read back to prove persistence. --dry-run only reports what would change.",
		Example: "  easyeda sch checkpoint restore 20261117T141500123456789-0abc --window W --dry-run",
		RunE: func(cmd *cobra.Command, args []string) error {
			w, err := resolveTargetWindow(cfg, *window)
			if err != nil {
				return err
			}
			project, document, err := activeSchematicPage(cfg, w)
			if err != nil {
				return err
			}
			store, err := openCheckpointStore(defaultCheckpointDir(), project, document)
			if err != nil {
				return err
			}
			entries, err := store.entries()
			if err != nil {
				return err
			}
			target, err := store.source(entries, args[0])
			if err != nil {
				return err
			}
			current, err := readSourceSnapshot(cfg, w, project, document)
			if err != nil {
				return err
			}
			pending, err := compareSources(target, current.Source)
			if err != nil {
				return err
			}
			report := map[string]any{"checkpoint": args[0], "project": project, "document": document, "dryRun": dryRun,
				"writeAttempted": false, "written": false, "pending": pending, "persistenceVerified": false}
			// After an earlier restore the page describes the same design in
			// different bytes; writing it again would only churn the editor.
			if pending.equivalent() || dryRun {
				report["alreadyAtCheckpoint"] = pending.equivalent()
				report["verified"] = true
				return writeJSON(stdout, report)
			}
			// The way back must exist before the page is overwritten.
			undo, err := store.add(checkpointEntry{Project: project, Document: document, Command: cfg.command,
				Action: "document.source.restore", Label: os.Getenv(envCheckpointLabel)}, current.Source)
			if err != nil {
				return fmt.Errorf("could not record the current page, so nothing was restored: %w", err)
			}
			checkpointProcess.mu.Lock()
			checkpointProcess.covered[w+"\x00"+document] = true
			checkpointProcess.mu.Unlock()
			report["undoCheckpoint"] = undo.ID
			wire := func(source string) map[string]any {
				return map[string]any{"schemaVersion": 1, "projectUuid": project, "documentUuid": document, "documentType": "schematic", "source": source}
			}
			// One dispatch, no retries: a late setter must never run twice.
			res, callErr := requestActionOnce(cfg, "document.source.restore", w, map[string]any{"projectUuid": project, "documentUuid": document,
				"snapshot": wire(target), "expected": wire(current.Source)}, 60*time.Second)
			report["writeAttempted"] = true
			if callErr != nil {
				report["error"], report["verified"] = callErr.Error(), false
				_ = writeJSON(stdout, report)
				return fmt.Errorf("restore did not complete (%w); the page before it is checkpoint %s — read the page before any further write", callErr, undo.ID)
			}
			after, _ := res.Result["afterSource"].(string)
			report["written"] = res.Result["written"] == true
			report["sourceEqual"] = after != "" && sourceBody(after) == sourceBody(target)
			difference, diffErr := compareSources(target, after)
			report["difference"] = difference
			verified := res.Result["written"] == true && diffErr == nil && difference.equivalent()
			report["verified"] = verified
			if diffErr != nil {
				report["error"] = diffErr.Error()
			}
			if err = writeJSON(stdout, report); err != nil {
				return err
			}
			if !verified {
				return fmt.Errorf("the page does not match checkpoint %s after the restore; the page before it is checkpoint %s — read the page before any further write", args[0], undo.ID)
			}
			return nil
		}}
	restore.Flags().BoolVar(&dryRun, "dry-run", false, "report what a restore would change; write nothing and record nothing")
	group.AddCommand(list, restore)
	return group
}
