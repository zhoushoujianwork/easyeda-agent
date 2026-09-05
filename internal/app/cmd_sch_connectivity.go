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
		pinPayload := map[string]any{"includePins": true}
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
