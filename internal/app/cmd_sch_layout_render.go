package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func newSchLayoutRenderCmd(stdout io.Writer) *cobra.Command {
	var from, out, zone string
	c := &cobra.Command{Use: "layout-render", Short: "Compile layout JSON to SVG offline (no AI, EDA or diff panels)", Long: `Render schemaVersion:1, optional title, zones:[{id,title,layout,status?}].
layout is a SchematicLayoutResult. status: planned (default) or blocked.
Also accepts layout-plan --zones output. Optional zone frame is preserved.
Only translates supplied geometry; never solves or fabricates missing wires.
Simplified symbols/text are not official EasyEDA graphics or electrical checks.
Zone packing is display-only, not paper layout. Output is SVG, no external runtime.

  easyeda sch layout-render --from geometry.json --out layout.svg
  easyeda sch layout-render --from geometry.json --zone supply --out supply.svg`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if from == "" {
			return fmt.Errorf("--from required")
		}
		if out != "" {
			if filepath.Ext(out) != ".svg" {
				return fmt.Errorf("--out must use .svg")
			}
			a, _ := filepath.Abs(from)
			b, _ := filepath.Abs(out)
			fi, _ := os.Stat(from)
			fo, _ := os.Stat(out)
			if a == b || (fi != nil && fo != nil && os.SameFile(fi, fo)) {
				return fmt.Errorf("--out must not overwrite input")
			}
		}
		raw, e := os.ReadFile(from)
		if e != nil {
			return e
		}
		var input SchematicRenderInput
		if e = connectivity.DecodeStrictDesignJSON(raw, &input); e != nil {
			return e
		}
		// Missing geometry must not silently become coordinates at zero.
		var fields struct {
			Zones []struct {
				Layout struct {
					Placements []map[string]json.RawMessage `json:"placements"`
				} `json:"layout"`
			} `json:"zones"`
		}
		if e = json.Unmarshal(raw, &fields); e != nil {
			return e
		}
		for _, z := range fields.Zones {
			for _, m := range z.Layout.Placements {
				for _, key := range []string{"x", "y", "rotation", "mirror", "bbox", "pins"} {
					if len(m[key]) == 0 || string(m[key]) == "null" {
						return fmt.Errorf("measurement requires %s", key)
					}
				}
				var box map[string]json.RawMessage
				_ = json.Unmarshal(m["bbox"], &box)
				for _, key := range []string{"minX", "minY", "maxX", "maxY"} {
					if len(box[key]) == 0 || string(box[key]) == "null" {
						return fmt.Errorf("bbox requires %s", key)
					}
				}
				var pins []map[string]json.RawMessage
				_ = json.Unmarshal(m["pins"], &pins)
				for _, p := range pins {
					for _, key := range []string{"number", "net", "x", "y"} {
						if len(p[key]) == 0 || string(p[key]) == "null" {
							return fmt.Errorf("pin requires %s", key)
						}
					}
				}
			}
		}
		if zone != "" {
			var selected []SchematicRenderZone
			for _, z := range input.Zones {
				if z.ID == zone {
					selected = append(selected, z)
				}
			}
			if len(selected) != 1 {
				return fmt.Errorf("--zone must match exactly one zone")
			}
			input.Zones = selected
		}
		svg, e := RenderSchematicLayoutSVG(input)
		if e != nil {
			return e
		}
		if out == "" {
			_, e = stdout.Write(svg)
			return e
		}
		return os.WriteFile(out, svg, 0644)
	}}
	c.Flags().StringVar(&from, "from", "", "layout/diagnostic JSON")
	c.Flags().StringVar(&out, "out", "", "SVG output, defaults to stdout")
	c.Flags().StringVar(&zone, "zone", "", "render a single zone ID")
	return c
}
