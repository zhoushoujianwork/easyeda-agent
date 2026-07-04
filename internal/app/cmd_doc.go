package app

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// openableDoc is one document a window can switch to (a schematic page or a PCB),
// unified across the schematic.pages.list and pcb.documents.list actions.
type openableDoc struct {
	UUID   string `json:"uuid"`
	Type   string `json:"type"` // "schematic" | "pcb"
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"` // owning schematic/project uuid
	Active bool   `json:"active"`
}

// newDocCmd returns the "doc" subcommand group — the self-service discover +
// switch loop. `doc ls` enumerates every openable document in the targeted
// window and marks the active one; `doc switch` resolves a name or uuid and
// brings that document to the front. Both route by the shared --project/--window
// flags, so an agent can drive a window without knowing its windowId or port.
func newDocCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	doc := &cobra.Command{
		Use:   "doc",
		Short: "Discover and switch the active EasyEDA document (schematic page / PCB)",
		Long: "Discover every openable document in a window and switch between them.\n\n" +
			"  easyeda doc ls --project <name>            list all schematic pages + PCBs, ★=active\n" +
			"  easyeda doc switch <name|uuid> --project <name>   bring a document to the front\n\n" +
			"Context is read live (not the connect-time snapshot), so the active marker\nand `daemon health` reflect the real foreground document.",
	}
	doc.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID (usually prefer --project)")

	var jsonOut bool

	lsCmd := &cobra.Command{
		Use:   "ls",
		Short: "List all openable documents in the window (★ = active)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			docs, active, _, err := discoverDocs(cfg, window)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(stdout, map[string]any{
					"activeUuid": active,
					"documents":  docs,
				})
			}
			printDocTable(stdout, docs)
			return nil
		},
	}
	lsCmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a table")

	switchCmd := &cobra.Command{
		Use:   "switch <name|uuid>",
		Short: "Switch the foreground document by page name, PCB name, or uuid",
		Args:  cobra.ExactArgs(1),
		Example: "  easyeda doc switch P2 --project motobox2026\n" +
			"  easyeda doc switch ESP32-S3-V1_0_1 --project motobox2026\n" +
			"  easyeda doc switch 6b3a2f01-... --project motobox2026",
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			docs, _, win, err := discoverDocs(cfg, window)
			if err != nil {
				return err
			}
			match, err := resolveDoc(docs, target)
			if err != nil {
				return err
			}
			// Pin the open + readback to the SAME window discoverDocs resolved.
			if _, err := requestAction(cfg, "document.open", win,
				map[string]any{"uuid": match.UUID}); err != nil {
				return err
			}
			// Re-read live context to confirm the switch took effect.
			cur, err := requestAction(cfg, "document.current", win, nil)
			if err != nil {
				return err
			}
			out := map[string]any{
				"switchedTo": match,
			}
			if cur.Context != nil {
				out["active"] = cur.Context
			}
			if jsonOut {
				return writeJSON(stdout, out)
			}
			fmt.Fprintf(stdout, "✓ switched to %s %q (%s)\n", match.Type, match.Name, match.UUID)
			return nil
		},
	}
	switchCmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a line")

	// doc reload — save + close + reopen a document. Exists because some
	// per-document engine state only refreshes on a real close/reopen: a
	// freshly CREATED PCB's pour reflow keeps using a creation-time rules
	// snapshot — rule writes and pour-rebuilds are ignored until the document
	// is reloaded (tab-switching away and back does NOT reload). The esp32-mini
	// playbook relies on this after its pour sequence.
	reloadCmd := &cobra.Command{
		Use:   "reload [name|uuid]",
		Short: "Save + close + reopen a document (default: the active one) — refreshes per-doc engine state",
		Long: `Save a document, close its tab, and reopen it — a real reload, unlike
"doc switch" which only changes the foreground tab.

Why: a freshly CREATED PCB document's copper-pour reflow keeps using the rules
snapshot taken at creation — writing rules (pcb drc-rules-set) and re-pouring
(pcb pour-rebuild) have NO effect until the document is closed and reopened.
After a reload the reflow honors the current rule configuration (clearance AND
thermal-spoke generation). Run "pcb pour-rebuild" after reloading a PCB.

The target document is saved first (schematic.save / pcb.save by type), so no
edits are lost. Defaults to the active document; pass a name/uuid to reload
another (it is brought to the front first).`,
		Args: cobra.MaximumNArgs(1),
		Example: `  easyeda doc reload                      # reload the active document
  easyeda doc reload PCB3 --project ceshi # reload a specific PCB
  easyeda pcb pour-rebuild                # then re-pour under the refreshed rules`,
		RunE: func(cmd *cobra.Command, args []string) error {
			docs, activeUUID, win, err := discoverDocs(cfg, window)
			if err != nil {
				return err
			}
			target := activeUUID
			if len(args) == 1 {
				match, err := resolveDoc(docs, args[0])
				if err != nil {
					return err
				}
				target = match.UUID
			}
			if target == "" {
				return fmt.Errorf("no active document to reload (run `easyeda doc ls`)")
			}
			// Bring the target to the front so the typed save + close hit it.
			if target != activeUUID {
				if _, err := requestAction(cfg, "document.open", win,
					map[string]any{"uuid": target}); err != nil {
					return err
				}
			}
			cur, err := requestAction(cfg, "document.current", win, nil)
			if err != nil {
				return err
			}
			if cur.Context == nil || cur.Context.DocumentUUID != target || cur.Context.TabID == "" {
				return fmt.Errorf("could not activate document %s before reload (active=%v)", target, cur.Context)
			}
			docType := cur.Context.DocumentType
			saveAction := "schematic.save"
			if docType == "pcb" {
				saveAction = "pcb.save"
			}
			if _, err := requestAction(cfg, saveAction, win, nil); err != nil {
				return fmt.Errorf("save before reload failed: %w", err)
			}
			closeJS := fmt.Sprintf("return await eda.dmt_EditorControl.closeDocument(%q)", cur.Context.TabID)
			if _, err := requestAction(cfg, "debug.exec_js", win, map[string]any{"code": closeJS}); err != nil {
				return fmt.Errorf("close document failed: %w", err)
			}
			time.Sleep(1 * time.Second)
			if _, err := requestAction(cfg, "document.open", win,
				map[string]any{"uuid": target}); err != nil {
				return fmt.Errorf("reopen after close failed: %w", err)
			}
			// Poll until the reopened document is the live active one.
			deadline := time.Now().Add(10 * time.Second)
			for {
				cur, err = requestAction(cfg, "document.current", win, nil)
				if err == nil && cur.Context != nil && cur.Context.DocumentUUID == target {
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("document %s did not become active within 10s after reopen", target)
				}
				time.Sleep(500 * time.Millisecond)
			}
			out := map[string]any{"reloaded": target, "documentType": docType, "saved": true}
			if jsonOut {
				return writeJSON(stdout, out)
			}
			fmt.Fprintf(stdout, "✓ reloaded %s %s (saved → closed → reopened)\n", docType, target)
			return nil
		},
	}
	reloadCmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a line")

	doc.AddCommand(lsCmd, switchCmd, reloadCmd)
	return doc
}

// discoverDocs resolves the target window ONCE, then aggregates
// schematic.pages.list + pcb.documents.list into a single openable-document list
// and marks the one matching the live active document (document.current). Every
// sub-call is pinned to the resolved windowId, so a second window appearing or a
// single-window auto-target racing mid-command can't break it. Returns the
// resolved windowId so a caller (e.g. `doc switch`) can pin its own follow-ups.
// PCB-listing failures are tolerated (a project may have no PCB).
func discoverDocs(cfg *appConfig, window string) (docs []openableDoc, activeUUID, resolvedWindow string, err error) {
	resolvedWindow, err = resolveTargetWindow(cfg, window)
	if err != nil {
		return nil, "", "", err
	}

	cur, err := requestAction(cfg, "document.current", resolvedWindow, nil)
	if err != nil {
		return nil, "", "", err
	}
	if cur.Context != nil {
		activeUUID = cur.Context.DocumentUUID
	}

	pages, err := requestAction(cfg, "schematic.pages.list", resolvedWindow, nil)
	if err != nil {
		return nil, "", "", err
	}
	for _, p := range mapsField(pages.Result, "pages") {
		docs = append(docs, openableDoc{
			UUID:   strField(p, "uuid"),
			Type:   "schematic",
			Name:   strField(p, "name"),
			Parent: strField(p, "parentSchematicUuid"),
		})
	}

	// PCBs are optional — a schematic-only project legitimately has none.
	if pcbs, perr := requestAction(cfg, "pcb.documents.list", resolvedWindow, nil); perr == nil {
		for _, p := range mapsField(pcbs.Result, "pcbs") {
			docs = append(docs, openableDoc{
				UUID:   strField(p, "uuid"),
				Type:   "pcb",
				Name:   strField(p, "name"),
				Parent: strField(p, "parentProjectUuid"),
			})
		}
	}

	for i := range docs {
		if docs[i].UUID != "" && docs[i].UUID == activeUUID {
			docs[i].Active = true
		}
	}
	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].Type != docs[j].Type {
			return docs[i].Type < docs[j].Type
		}
		return docs[i].Name < docs[j].Name
	})
	return docs, activeUUID, resolvedWindow, nil
}

// resolveDoc maps a user-supplied name or uuid to exactly one openable doc.
// An exact uuid match wins; otherwise a case-insensitive name match is used.
// Ambiguous name matches return an error listing the candidates.
func resolveDoc(docs []openableDoc, target string) (openableDoc, error) {
	for _, d := range docs {
		if d.UUID == target {
			return d, nil
		}
	}
	var hits []openableDoc
	lt := strings.ToLower(target)
	for _, d := range docs {
		if strings.ToLower(d.Name) == lt {
			hits = append(hits, d)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return openableDoc{}, fmt.Errorf("no document named or with uuid %q (run `easyeda doc ls` to see options)", target)
	default:
		var names []string
		for _, h := range hits {
			names = append(names, fmt.Sprintf("%s/%s", h.Type, h.UUID))
		}
		return openableDoc{}, fmt.Errorf("%q is ambiguous: %s — pass a uuid", target, strings.Join(names, ", "))
	}
}

func printDocTable(w io.Writer, docs []openableDoc) {
	if len(docs) == 0 {
		fmt.Fprintln(w, "(no openable documents — is a project open in this window?)")
		return
	}
	fmt.Fprintf(w, "%-2s  %-9s  %-24s  %s\n", "", "TYPE", "NAME", "UUID")
	for _, d := range docs {
		marker := " "
		if d.Active {
			marker = "★"
		}
		fmt.Fprintf(w, "%-2s  %-9s  %-24s  %s\n", marker, d.Type, d.Name, d.UUID)
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// mapsField returns result[key] as a slice of string-keyed maps, tolerating the
// any-typed shape that survives JSON round-tripping.
func mapsField(result map[string]any, key string) []map[string]any {
	if result == nil {
		return nil
	}
	raw, ok := result[key].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func strField(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}
