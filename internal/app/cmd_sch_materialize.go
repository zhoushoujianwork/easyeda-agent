package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// newSchMaterializeCmd turns a fixed 1.4 connectivity snapshot into a
// deterministic schematic playbook. It is deliberately an offline planner:
// placement and device identity come from the snapshot, while wires remain a
// later derived apply step. No existing canvas data is overwritten here.
func newSchMaterializeCmd(stdout, stderr io.Writer) *cobra.Command {
	var out string
	c := &cobra.Command{
		Use:   "materialize <connectivity.json>",
		Short: "将 1.4 原理图数据转换为器件放置 Apply 队列",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			var d connectivity.Document
			if err := json.Unmarshal(raw, &d); err != nil {
				return fmt.Errorf("读取 connectivity JSON: %w", err)
			}
			if err := d.Validate(); err != nil {
				return fmt.Errorf("数据结构校验失败: %w", err)
			}
			pb := playbook{Version: 1, Meta: playbookMeta{Name: "sch-materialize", Description: "Materialize 1.4 connectivity components", Project: d.ProjectID, Doc: d.DocumentID}, Defaults: stepPolicy{}, Steps: []playbookStep{}}
			for _, c := range d.Components {
				if c.Device.UUID == "" || c.Device.LibraryUUID == "" {
					return fmt.Errorf("%s 缺少 libraryUuid/deviceUuid，不能安全放置", c.Ref)
				}
				if c.Placement == nil {
					return fmt.Errorf("%s 缺少 placement 坐标", c.Ref)
				}
				pb.Steps = append(pb.Steps, playbookStep{ID: "place-" + c.Ref, Name: "place " + c.Ref, Action: "schematic.component.place", Payload: map[string]any{"libraryUuid": c.Device.LibraryUUID, "uuid": c.Device.UUID, "x": c.Placement.X, "y": c.Placement.Y}, Capture: map[string]string{c.Ref: "$.primitiveId"}})
			}
			pb.Steps = append(pb.Steps, playbookStep{ID: "save", Action: "schematic.save", Checkpoint: true})
			encoded, _ := json.MarshalIndent(pb, "", "  ")
			encoded = append(encoded, '\n')
			if out != "" {
				if err := os.WriteFile(out, encoded, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(stdout, "materialize plan written: %s (%d placement steps)\n", out, len(d.Components))
				return nil
			}
			_, err = stdout.Write(encoded)
			return err
		},
	}
	c.Flags().StringVar(&out, "out", "", "write playbook to a file instead of stdout")
	return c
}
