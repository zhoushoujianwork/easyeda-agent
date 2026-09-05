package app

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
	"io"
	"os"
)

func newSchConnectivityDiffCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{Use: "connectivity-diff <before.json> <after.json>", Short: "Compare two connectivity snapshots offline", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		var a, b connectivity.Document
		for i, p := range args {
			raw, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			if i == 0 {
				e = json.Unmarshal(raw, &a)
			} else {
				e = json.Unmarshal(raw, &b)
			}
			if e != nil {
				return fmt.Errorf("read %s: %w", p, e)
			}
		}
		if e := a.Validate(); e != nil {
			return fmt.Errorf("before: %w", e)
		}
		if e := b.Validate(); e != nil {
			return fmt.Errorf("after: %w", e)
		}
		out, _ := json.MarshalIndent(connectivity.Compare(a, b), "", "  ")
		_, e := fmt.Fprintln(stdout, string(out))
		return e
	}}
}
