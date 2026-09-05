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
		doc := connectivity.Document{SchemaVersion: "1.4"}
		m := raw.Result
		if cs, ok := m["components"].([]any); ok {
			netIDs := map[string]string{}
			for i, v := range cs {
				x, _ := v.(map[string]any)
				ref, _ := x["designator"].(string)
				if ref == "" || stringVal(x["componentType"]) != "part" {
					continue
				}
				id := fmt.Sprintf("cmp-%s", ref)
				if ref == "" {
					id = fmt.Sprintf("cmp-%d", i)
				}
				c := connectivity.Component{ID: id, Ref: ref}
				if ps, ok := x["pins"].([]any); ok {
					for _, pv := range ps {
						p, _ := pv.(map[string]any)
						n, _ := p["number"].(string)
						if n == "" {
							if f, ok := p["number"].(float64); ok {
								n = fmt.Sprintf("%.0f", f)
							}
						}
						c.Pins = append(c.Pins, connectivity.Pin{Number: n, Name: stringVal(p["name"]), Type: stringVal(p["type"])})
						if netName := stringVal(p["net"]); netName != "" {
							if _, exists := netIDs[netName]; !exists {
								netIDs[netName] = fmt.Sprintf("net-%d", len(netIDs))
							}
							doc.Connections = append(doc.Connections, connectivity.Connection{ComponentID: id, PinNumber: n, NetID: netIDs[netName], Kind: "netlist"})
						}
					}
				}
				doc.Components = append(doc.Components, c)
			}
		}
		if ns, ok := m["nets"].([]any); ok {
			for i, v := range ns {
				x, _ := v.(map[string]any)
				name := stringVal(x["net"])
				if name == "" {
					name = stringVal(x["name"])
				}
				doc.Nets = append(doc.Nets, connectivity.Net{ID: fmt.Sprintf("net-%d", i), Name: name})
			}
		}
		for name, id := range netIDs {
			found := false
			for i := range doc.Nets {
				if doc.Nets[i].ID == id {
					found = true
					break
				}
			}
			if !found {
				doc.Nets = append(doc.Nets, connectivity.Net{ID: id, Name: name})
			}
		}
		if err := doc.Validate(); err != nil {
			return err
		}
		b, _ := json.MarshalIndent(doc, "", "  ")
		_, err = fmt.Fprintln(stdout, string(b))
		return err
	}}
	c.Flags().BoolVar(&allPages, "all-pages", false, "include all schematic pages")
	return c
}
func stringVal(v any) string { s, _ := v.(string); return s }
