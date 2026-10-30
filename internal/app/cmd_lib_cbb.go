package app

import (
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"strings"
)

func newLibCbbCmd(cfg *appConfig, stdout, stderr io.Writer, window *string) *cobra.Command {
	group := &cobra.Command{Use: "cbb", Short: "Read native reuse-block library assets through official beta APIs"}
	{
		var query, library string
		var limit, page int
		c := &cobra.Command{Use: "search", Short: "Search CBB assets; does not open or modify their projects", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(query) == "" || limit < 1 || limit > 100 || page < 1 {
				return fmt.Errorf("--query is required; --limit must be 1..100 and --page positive")
			}
			p := map[string]any{"query": query, "limit": limit, "page": page}
			if library != "" {
				p["libraryUuid"] = library
			}
			return dispatch(cfg, "library.cbb.search", *window, p, stdout, stderr)
		}}
		c.Flags().StringVar(&query, "query", "", "non-empty CBB keyword (required)")
		c.Flags().StringVar(&library, "library", "", "library UUID (default: official system library)")
		c.Flags().IntVar(&limit, "limit", 20, "page size, 1..100")
		c.Flags().IntVar(&page, "page", 1, "positive search page")
		group.AddCommand(c)
	}
	{
		var uuid, library string
		c := &cobra.Command{Use: "get", Short: "Read exact CBB identity, boards and schematic symbol variants", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(uuid) == "" {
				return fmt.Errorf("--uuid is required")
			}
			p := map[string]any{"uuid": uuid}
			if library != "" {
				p["libraryUuid"] = library
			}
			return dispatch(cfg, "library.cbb.get", *window, p, stdout, stderr)
		}}
		c.Flags().StringVar(&uuid, "uuid", "", "CBB asset UUID (required)")
		c.Flags().StringVar(&library, "library", "", "exact library UUID")
		group.AddCommand(c)
	}
	return group
}
