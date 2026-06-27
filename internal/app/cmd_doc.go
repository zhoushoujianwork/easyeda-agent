package app

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

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
			docs, active, err := discoverDocs(cfg, window)
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
			docs, _, err := discoverDocs(cfg, window)
			if err != nil {
				return err
			}
			match, err := resolveDoc(docs, target)
			if err != nil {
				return err
			}
			if _, err := requestAction(cfg, "document.open", window,
				map[string]any{"uuid": match.UUID}); err != nil {
				return err
			}
			// Re-read live context to confirm the switch took effect.
			cur, err := requestAction(cfg, "document.current", window, nil)
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

	doc.AddCommand(lsCmd, switchCmd)
	return doc
}

// discoverDocs aggregates schematic.pages.list + pcb.documents.list into a
// single openable-document list and marks the one matching the live active
// document (document.current). Failures of the PCB listing are tolerated (a
// project may have no PCB).
func discoverDocs(cfg *appConfig, window string) (docs []openableDoc, activeUUID string, err error) {
	cur, err := requestAction(cfg, "document.current", window, nil)
	if err != nil {
		return nil, "", err
	}
	if cur.Context != nil {
		activeUUID = cur.Context.DocumentUUID
	}

	pages, err := requestAction(cfg, "schematic.pages.list", window, nil)
	if err != nil {
		return nil, "", err
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
	if pcbs, perr := requestAction(cfg, "pcb.documents.list", window, nil); perr == nil {
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
	return docs, activeUUID, nil
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
