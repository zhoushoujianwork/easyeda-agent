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
		Short: "Discover and open/switch EasyEDA documents (schematic pages / PCBs)",
		Long: "Discover every openable document in a window and open or switch between them.\n\n" +
			"  easyeda doc ls --project <name>                      list all schematic pages + PCBs, ★=active\n" +
			"  easyeda doc open <name|uuid> --project <name>        open a document (schematic page or PCB)\n" +
			"  easyeda doc switch <name|uuid> --project <name>      switch to a document (same as open)\n" +
			"  easyeda doc reload [name|uuid] --project <name>      save, close, and reopen a document\n\n" +
			"Context is read live (not the connect-time snapshot), so the active marker\nand `daemon health` reflect the real foreground document. If the project has\nno active editor tab, `doc ls` still uses project inventories and `doc open`\ncan recover by UUID, then confirms the new active document with a fresh read.",
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
			if cur.Context == nil || cur.Context.DocumentUUID != match.UUID {
				return fmt.Errorf("document.open returned but active document is not %s; the page may still be loading", match.UUID)
			}
			// document.open returns as soon as the tab exists — BEFORE the page's
			// primitives/netlist finish loading. Wait for the page data to settle
			// so a read fired right after the switch doesn't sample a half-loaded
			// page (issue #67). The probe follows the document type — a PCB is
			// polled with pcb.components.list rather than skipped (issue #161).
			ready := waitDocSettleFor(cfg, win, match.Type)
			if !ready {
				return fmt.Errorf("document %s became active but its %s data did not settle; refresh the editor or open it once from the project tree, then read back actual objects", match.UUID, match.Type)
			}
			out := map[string]any{
				"switchedTo": match,
				"ready":      ready,
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

	// open is a semantically clearer alias for switch — "open a document" vs "switch to a document"
	openCmd := &cobra.Command{
		Use:   "open <name|uuid>",
		Short: "Open a document (schematic page or PCB) by name or uuid",
		Args:  cobra.ExactArgs(1),
		Example: "  easyeda doc open PCB1 --project ceshi\n" +
			"  easyeda doc open P1 --project ceshi\n" +
			"  easyeda doc open ESP32-mini-v2 --project hardware",
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
			if _, err := requestAction(cfg, "document.open", win,
				map[string]any{"uuid": match.UUID}); err != nil {
				return err
			}
			cur, err := requestAction(cfg, "document.current", win, nil)
			if err != nil {
				return err
			}
			if cur.Context == nil || cur.Context.DocumentUUID != match.UUID {
				return fmt.Errorf("document.open returned but active document is not %s; the page may still be loading", match.UUID)
			}
			if !waitDocSettleFor(cfg, win, match.Type) {
				return fmt.Errorf("document %s became active but its %s data did not settle; refresh the editor or open it once from the project tree, then read back actual objects", match.UUID, match.Type)
			}
			out := map[string]any{
				"opened": match,
				"ready":  true,
			}
			if cur.Context != nil {
				out["active"] = cur.Context
			}
			if jsonOut {
				return writeJSON(stdout, out)
			}
			fmt.Fprintf(stdout, "✓ opened %s %q (%s)\n", match.Type, match.Name, match.UUID)
			return nil
		},
	}
	openCmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a line")

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
another (it is brought to the front first).

The reopen leaves the reloaded document foreground, so reloading a NON-active
page would move the active tab. This command restores the pre-reload active
document afterward and reports it as "activeRestored", so the ★ does not drift
(issue #67).`,
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
			docType, err := reloadDocumentByUUID(cfg, win, target)
			if err != nil {
				return err
			}
			// Restore the pre-reload active document. reopen leaves the target
			// foreground even when the caller reloaded a NON-active page, so
			// without this the ★ silently drifts and later commands land on the
			// wrong page (issue #67). Best-effort: a restore failure is reported
			// but the reload itself already succeeded.
			restored := activeUUID
			if activeUUID != "" && activeUUID != target {
				if _, rerr := requestAction(cfg, "document.open", win,
					map[string]any{"uuid": activeUUID}); rerr != nil {
					restored = ""
				}
			}
			out := map[string]any{
				"reloaded":       target,
				"documentType":   docType,
				"saved":          true,
				"activeRestored": restored,
			}
			if jsonOut {
				return writeJSON(stdout, out)
			}
			fmt.Fprintf(stdout, "✓ reloaded %s %s (saved → closed → reopened)\n", docType, target)
			if activeUUID != "" && activeUUID != target {
				if restored != "" {
					fmt.Fprintf(stdout, "  ↩ restored active document to %s\n", restored)
				} else {
					fmt.Fprintf(stdout, "  ⚠ could not restore active document %s — active is now %s\n", activeUUID, target)
				}
			}
			return nil
		},
	}
	reloadCmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a line")

	doc.AddCommand(lsCmd, switchCmd, openCmd, reloadCmd, newDocSourceCmd(cfg, &window, stdout))
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
	if err != nil && !isNoActiveDocument(cur) {
		return nil, "", "", err
	}
	if err == nil && cur.Context != nil {
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

	// #190: the host may temporarily omit the active PCB from getAllPcbsInfo.
	// Recover its name from the independent current-PCB API, never from a cached
	// name or the user's selector. Both reads must identify the same document and
	// project; otherwise the user may have switched tabs during enumeration.
	if cur.Context != nil && cur.Context.DocumentType == "pcb" && activeUUID != "" {
		found := false
		for _, d := range docs {
			if d.UUID == activeUUID && d.Type == "pcb" {
				found = true
				break
			}
		}
		if !found {
			probeCfg := *cfg
			probeCfg.doc = "" // discovery must not recurse through the --doc guard
			info, ierr := requestAction(&probeCfg, "pcb.board.info", resolvedWindow, nil)
			if ierr != nil {
				return nil, "", "", fmt.Errorf("PCB document enumeration incomplete: active PCB %s is missing; current-PCB lookup failed: %w", activeUUID, ierr)
			}
			pcb, _ := info.Result["pcb"].(map[string]any)
			if info.Context == nil || cur.Context.ProjectUUID == "" ||
				info.Context.ProjectUUID != cur.Context.ProjectUUID ||
				info.Context.DocumentUUID != activeUUID || info.Context.DocumentType != "pcb" ||
				strField(pcb, "uuid") != activeUUID || strField(pcb, "name") == "" {
				return nil, "", "", fmt.Errorf("PCB document enumeration incomplete: current-PCB lookup does not confirm active PCB %s in the same project; refusing to guess the target", activeUUID)
			}
			docs = append(docs, openableDoc{UUID: activeUUID, Type: "pcb", Name: strField(pcb, "name"), Parent: cur.Context.ProjectUUID})
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

// isNoActiveDocument recognizes the connector's explicit "the project is open,
// but no editor tab is active" result. That state is recoverable: the
// project-scoped schematic/PCB inventories remain readable and document.open
// can activate an inventory UUID. Keep the exception narrow so transport,
// project-context, and arbitrary document.current failures still fail closed.
//
// The current connector reports this condition with the generic
// EDA_CALL_FAILED code, so the stable message is also checked. A future
// dedicated error code can be added here without changing the CLI flow.
func isNoActiveDocument(res *actionResult) bool {
	if res == nil || res.errorCode != "EDA_CALL_FAILED" {
		return false
	}
	message := strings.TrimSpace(res.errorMsg)
	message = strings.TrimSuffix(message, ".")
	return strings.EqualFold(message, "No active document")
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

// reloadDocumentByUUID saves + closes + reopens document `target` in window
// `win` and waits for it to become the active document again — the extracted
// core of `doc reload`, shared with `pcb clear`'s verify pass (#121: some
// primitives are only enumerable after a real close/reopen, so a clear must
// reload before it can trust "the board is empty"). Brings the target to the
// foreground first when it isn't already. Returns the document's type
// ("pcb"/"schematic") so callers can branch.
func reloadDocumentByUUID(cfg *appConfig, win, target string) (string, error) {
	cur, err := requestAction(cfg, "document.current", win, nil)
	if err != nil {
		return "", err
	}
	if cur.Context == nil || cur.Context.DocumentUUID != target || cur.Context.TabID == "" {
		if _, err := requestAction(cfg, "document.open", win, map[string]any{"uuid": target}); err != nil {
			return "", err
		}
		cur, err = requestAction(cfg, "document.current", win, nil)
		if err != nil {
			return "", err
		}
		if cur.Context == nil || cur.Context.DocumentUUID != target || cur.Context.TabID == "" {
			return "", fmt.Errorf("could not activate document %s before reload (active=%v)", target, cur.Context)
		}
		if !waitDocSettleFor(cfg, win, cur.Context.DocumentType) {
			return cur.Context.DocumentType, fmt.Errorf("document %s became active before reload but its data did not settle", target)
		}
	}
	docType := cur.Context.DocumentType
	saveAction := "schematic.save"
	if docType == "pcb" {
		saveAction = "pcb.save"
	}
	if _, err := requestAction(cfg, saveAction, win, nil); err != nil {
		return docType, fmt.Errorf("save before reload failed: %w", err)
	}
	// The typed close action verifies BOTH live identities and captures the
	// target split before closing. Looking the split up after close is racy: the
	// host can expose a transient blank tab or a tab from another split.
	closeRes, err := requestAction(cfg, "document.close", win, map[string]any{
		"uuid": target, "tabId": cur.Context.TabID,
	})
	if err != nil {
		return docType, fmt.Errorf("close document failed: %w", err)
	}
	closed, _ := closeRes.Result["closed"].(bool)
	if !closed {
		return docType, fmt.Errorf("close document returned no success for %s; reopen was not attempted", target)
	}
	splitScreenID, _ := closeRes.Result["splitScreenId"].(string)
	splitScreenID = strings.TrimSpace(splitScreenID)

	// closeDocument's promise can resolve before the editor has removed the old
	// active tab. Opening the same UUID during that interval is the second path
	// to the permanent loading spinner. Prove the old document is inactive; on
	// timeout, stop without issuing openDocument at all.
	closeDeadline := time.Now().Add(10 * time.Second)
	for {
		cur, err = requestActionTimed(cfg, "document.current", win, nil, 3*time.Second)
		noActiveDocument := isNoActiveDocument(cur)
		if noActiveDocument || (err == nil && (cur.Context == nil || cur.Context.DocumentUUID != target)) {
			break
		}
		if time.Now().After(closeDeadline) {
			return docType, fmt.Errorf("document %s close did not settle within 10s; reopen was not attempted to avoid a duplicate open — check the existing tab, then refresh the editor if it is still loading", target)
		}
		time.Sleep(250 * time.Millisecond)
	}

	openPayload := map[string]any{"uuid": target}
	if splitScreenID != "" {
		openPayload["splitScreenId"] = splitScreenID
	}
	if _, err := requestActionTimed(cfg, "document.open", win, openPayload, 15*time.Second); err != nil {
		return docType, fmt.Errorf("reopen after close failed for %s (the target tab is closed; automatic retry was suppressed because the first open may still complete): %w — recover by refreshing the editor or opening the document once from the project tree", target, err)
	}
	// Poll until the reopened document is the live active one.
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur, err = requestAction(cfg, "document.current", win, nil)
		if err == nil && cur.Context != nil && cur.Context.DocumentUUID == target {
			if !waitDocSettleFor(cfg, win, docType) {
				return docType, fmt.Errorf("document %s became active after reopen but its %s objects did not settle; refresh the editor or open it once from the project tree, then read back actual objects", target, docType)
			}
			return docType, nil
		}
		if time.Now().After(deadline) {
			return docType, fmt.Errorf("document %s did not become active within 10s after reopen", target)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
