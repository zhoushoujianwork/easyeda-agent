package app

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newSelectionCmd returns `easyeda selection`: a read-only report of the objects
// the user currently has selected on the active schematic or PCB canvas. It maps
// 1:1 to the selection.get action and never selects, clears or edits anything.
func newSelectionCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "selection",
		Short: "Read the objects currently selected on the active schematic/PCB canvas (read-only)",
		Long: "Read the objects the user currently has selected on the active schematic page or PCB.\n\n" +
			"Read-only: it never selects, clears or edits anything. The selection is only a way to\n" +
			"locate objects; write commands still need explicit ids, so take the ids from this output\n" +
			"and pass them to the existing command (for example `easyeda pcb delete --ids ...`).\n\n" +
			"Each row has the primitive id, type, designator, net, layer and x/y; fields the\n" +
			"object does not expose show as '-'. An empty selection is not an error. The read fails\n" +
			"with `page drift` when the active document changes while reading, and when the active\n" +
			"document is neither a schematic page nor a PCB.",
		Example: "  easyeda selection --project ceshi\n" +
			"  easyeda selection --project ceshi --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := requestAction(cfg, "selection.get", window, nil)
			if err != nil {
				return err
			}
			items := mapsField(res.Result, "items")
			docType := strField(res.Result, "documentType")
			if docType == "" && res.Context != nil {
				docType = res.Context.DocumentType
			}
			count := len(items)
			if n, ok := res.Result["count"].(float64); ok {
				count = int(n)
			}
			if jsonOut {
				if items == nil {
					items = []map[string]any{}
				}
				return writeJSON(stdout, map[string]any{
					"context":      res.Context,
					"documentType": docType,
					"count":        count,
					"items":        items,
				})
			}
			docID := "-"
			if res.Context != nil && res.Context.DocumentUUID != "" {
				docID = res.Context.DocumentUUID
			}
			printSelection(stdout, docType, docID, count, items)
			return nil
		},
	}
	cmd.Flags().StringVar(&window, "window", "", "EasyEDA window ID (usually prefer --project)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a table")
	return cmd
}

// printSelection renders the selection table; missing fields print as "-".
func printSelection(w io.Writer, docType, docID string, count int, items []map[string]any) {
	if docType == "" {
		docType = "document"
	}
	if count == 0 && len(items) == 0 {
		fmt.Fprintf(w, "no objects selected in %s %s\n", docType, docID)
		return
	}
	fmt.Fprintf(w, "%s %s: %d selected\n", docType, docID, count)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tDESIGNATOR\tNET\tLAYER\tX\tY")
	for _, it := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			selectionCell(it["primitiveId"]), selectionCell(it["primitiveType"]),
			selectionCell(it["designator"]), selectionCell(it["net"]), selectionCell(it["layer"]),
			selectionCell(it["x"]), selectionCell(it["y"]))
	}
	_ = tw.Flush()
}

// selectionCell formats one decoded JSON scalar for the table.
func selectionCell(v any) string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return "-"
		}
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return "-"
	}
}
