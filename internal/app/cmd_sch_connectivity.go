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
	c := &cobra.Command{Use: "connectivity", Short: "Export layout-independent schematic connectivity IR", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		payload := map[string]any{"includeCheck": false}
		if allPages {
			payload["allPages"] = true
		}
		raw, err := requestAction(cfg, "schematic.read", *window, payload)
		if err != nil {
			return err
		}
		doc, err := connectivity.FromRead(raw.Result)
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
	return c
}
func stringVal(v any) string { s, _ := v.(string); return s }
