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

	// ── lib by-lcsc ─────────────────────────────────────────────────────────
	// schematic.library.get_by_lcsc — deterministic resolve of LCSC C-numbers to
	// { libraryUuid, uuid } ready for schematic.component.place (the standard-
	// parts.json / BOM path; no free-text ranking).
	{
		var lcsc []string
		c := &cobra.Command{
			Use:   "by-lcsc",
			Short: "Resolve LCSC C-numbers directly to device-library identity (libraryUuid + uuid)",
			Args:  cobra.NoArgs,
			Example: `  easyeda lib by-lcsc --lcsc C6186
  easyeda lib by-lcsc --lcsc C6186 --lcsc C9900163599
  easyeda lib by-lcsc --lcsc C6186,C9900163599`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(lcsc) == 0 {
					return fmt.Errorf("--lcsc is required (one or more LCSC C-numbers)")
				}
				return dispatch(cfg, "schematic.library.get_by_lcsc", window,
					map[string]any{"lcscIds": lcsc}, stdout, stderr)
			},
		}
		c.Flags().StringSliceVar(&lcsc, "lcsc", nil, "LCSC C-number(s); repeat the flag or comma-separate (required)")
		lib.AddCommand(c)
	}

	return lib
}
