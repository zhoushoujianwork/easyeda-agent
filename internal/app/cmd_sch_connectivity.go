package app

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
	"io"
)

func newSchConnectivityCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var allPages bool
	var page string
	c := &cobra.Command{Use: "connectivity", Short: "Export layout-independent schematic connectivity IR", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		// EasyEDA lazily hydrates the manufacture netlist for the active page
		// only. For an all-pages export, read each schematic page while it is
		// active and merge the 1.4 IR locally; this keeps pins and net evidence
		// coherent instead of accepting shallow cross-page records.
		if allPages && page == "" {
			docs, _, win, err := discoverDocs(cfg, *window)
			if err != nil {
				return err
			}
			var merged connectivity.Document
			initialized := false
			haveMerged := false
			var firstScope pageScope
			for _, doc := range docs {
				if doc.Type != "schematic" {
					continue
				}
				scope, err := switchToPage(cfg, win, doc.UUID)
				if err != nil {
					return err
				}
				if !initialized {
					firstScope = scope
					initialized = true
				}
				raw, err := requestAction(cfg, "schematic.read", win, map[string]any{"includeCheck": false})
				if err != nil {
					return err
				}
				pinRaw, err := requestAction(cfg, "schematic.components.list", win, map[string]any{"includePins": true, "includeBBox": true, "includeDeviceIdentity": true})
				if err != nil {
					return err
				}
				if parts, ok := pinRaw.Result["components"]; ok {
					raw.Result["components"] = parts
				}
				part, err := connectivity.FromRead(raw.Result)
				if err != nil {
					return fmt.Errorf("page %s: %w", doc.Name, err)
				}
				for i := range part.Components {
					part.Components[i].PageID = doc.UUID
					part.Components[i].PageName = doc.Name
				}
				if !haveMerged {
					merged = part
					haveMerged = true
				} else {
					merged.Components = append(merged.Components, part.Components...)
					merged.Nets = append(merged.Nets, part.Nets...)
					merged.Connections = append(merged.Connections, part.Connections...)
				}
			}
			if initialized {
				_ = firstScope.restore(cfg)
			}
			if !initialized {
				return fmt.Errorf("no schematic pages found")
			}
			merged.SchemaVersion = "1.4"
			if err := merged.Validate(); err != nil {
				return err
			}
			b, _ := json.MarshalIndent(merged, "", "  ")
			_, err = fmt.Fprintln(stdout, string(b))
			return err
		}
		payload := map[string]any{"includeCheck": false}
		if page != "" {
			scope, err := switchToPage(cfg, *window, page)
			if err != nil {
				return err
			}
			defer func() { _ = scope.restore(cfg) }()
			*window = scope.window
		}
		if allPages {
			payload["allPages"] = true
			// EasyEDA lazily loads non-active pages; force the connector to tag
			// and hydrate every page before collecting the authoritative snapshot.
			payload["tagPages"] = true
		}
		raw, err := requestAction(cfg, "schematic.read", *window, payload)
		if err != nil {
			return err
		}
		// components.list is the pin-intent source: unlike schematic.read it
		// carries noConnected, distinguishing intentional NC from a missing wire.
		pinPayload := map[string]any{"includePins": true, "includeBBox": true, "includeDeviceIdentity": true}
		if allPages {
			pinPayload["allPages"] = true
			pinPayload["tagPages"] = true
		}
		pinRaw, err := requestAction(cfg, "schematic.components.list", *window, pinPayload)
		if err != nil {
			return err
		}
		readResult := raw.Result
		if parts, ok := pinRaw.Result["components"]; ok {
			readResult["components"] = parts
		}
		doc, err := connectivity.FromRead(readResult)
		if err != nil {
			return err
		}
		if raw.Context != nil {
			doc.ProjectID = raw.Context.ProjectUUID
			doc.DocumentID = raw.Context.DocumentUUID
		}
		b, _ := json.MarshalIndent(doc, "", "  ")
		_, err = fmt.Fprintln(stdout, string(b))
		return err
	}}
	c.Flags().BoolVar(&allPages, "all-pages", false, "include all schematic pages")
	c.Flags().StringVar(&page, "page", "", "switch to and read one page by name or UUID")
	return c
}
func stringVal(v any) string { s, _ := v.(string); return s }
