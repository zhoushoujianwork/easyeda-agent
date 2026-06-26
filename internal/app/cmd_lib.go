package app

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newLibCmd returns the "lib" subcommand group.
func newLibCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	lib := &cobra.Command{
		Use:   "lib",
		Short: "EasyEDA device library operations",
	}
	lib.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")

	// ── lib search ────────────────────────────────────────────────────────
	// schematic.library.search
	{
		var query string
		var limit int
		c := &cobra.Command{
			Use:   "search",
			Short: "Search the EasyEDA device library by MPN, value+package, or name",
			Args:  cobra.NoArgs,
			Example: `  easyeda lib search --query "ESP32-S3-WROOM-1"
  easyeda lib search --query "100nF 0402" --limit 5`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if query == "" {
					return fmt.Errorf("--query is required")
				}
				payload := map[string]any{"query": query}
				if cmd.Flags().Changed("limit") {
					payload["limit"] = limit
				}
				return dispatch(cfg, "schematic.library.search", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&query, "query", "", "search query: MPN, value+package, or component name (required)")
		c.Flags().IntVar(&limit, "limit", 10, "maximum number of results to return")
		lib.AddCommand(c)
	}

	return lib
}
