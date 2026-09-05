package app

import (
	"encoding/json"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
	"io"
	"os"
)

type schPlan struct {
	SchemaVersion string           `json:"schemaVersion"`
	Operations    []map[string]any `json:"operations"`
}

func newSchPlanCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{Use: "plan <before.json> <after.json>", Short: "Build an ordered schematic operation plan offline", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
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
			return e
		}
		if e := b.Validate(); e != nil {
			return e
		}
		d := connectivity.Compare(a, b)
		p := schPlan{SchemaVersion: "1.4"}
		for _, k := range d.ChangedConnections {
			p.Operations = append(p.Operations, map[string]any{"id": fmt.Sprintf("op-%03d", len(p.Operations)+1), "kind": "reconcile-connection", "target": k, "action": "schematic.read", "note": "连接变化需人工确认后再生成写操作"})
		}
		for _, id := range d.AddedComponents {
			p.Operations = append(p.Operations, map[string]any{"id": fmt.Sprintf("op-%03d", len(p.Operations)+1), "kind": "component-added", "target": id, "action": "schematic.read"})
		}
		out, _ := json.MarshalIndent(p, "", "  ")
		_, e := fmt.Fprintln(stdout, string(out))
		return e
	}}
}
