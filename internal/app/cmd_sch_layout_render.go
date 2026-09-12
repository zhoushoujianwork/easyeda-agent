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
	var diagnostic bool
	c := &cobra.Command{Use: "layout-render", Short: "Compile layout JSON to SVG offline (no AI, EDA or diff panels)", Long: `Render schemaVersion:1, optional title, zones:[{id,title,layout,status?}].
layout is a SchematicLayoutResult. Complete named connectivity is checked by default.
--diagnostic explicitly permits incomplete/blocked layouts, never for final delivery.
Also accepts layout-plan --zones output. Optional zone frame is preserved.
Only translates supplied geometry; never solves or fabricates missing wires.
Simplified symbols/text are not official EasyEDA graphics or electrical checks.
Without sheet: display-only zone packing. With sheet: honor exact sheetPosition
and check padding, zone gaps, keepouts and explicit sheet.flow:z; no reflow. Output is SVG.
All zone-level variants are checked, including unselected ones and in diagnostic mode.
--zone validates references and variants in the full input, then renders standalone detail
without sheet placement constraints (not an entire-page validation).

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
		if e = validateRenderSheetJSON(raw); e != nil {
			return e
		}
		if e = validateRenderMeasurementsJSON(raw); e != nil {
			return e
		}
		if e = validateSchematicRenderPlacements(input.Zones); e != nil {
			return e
		}
		if e = validateSchematicZoneVariants(input); e != nil {
			return e
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
			input.Sheet = nil
			input.Zones[0].SheetPosition = nil
			input.Zones[0].Placement = nil
		}
		if !diagnostic {
			if e = validateCompleteLayoutPreview(input); e != nil {
				return e
			}
		}
		input.Diagnostic = input.Diagnostic || diagnostic
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
	c.Flags().BoolVar(&diagnostic, "diagnostic", false, "explicitly render incomplete diagnostics, not a completed layout")
	return c
}

func newSchLayoutSheetPlanCmd(stdout io.Writer) *cobra.Command {
	var from, out, flow string
	var diagnostic bool
	c := &cobra.Command{Use: "layout-sheet-plan", Short: "Pack existing zone geometry into sheet previews offline (no Apply)", Long: `Input: layout-render JSON plus sheet:{bounds,border,keepouts,padding,gap,flow?}.
Units are raw (0.01 inch); padding/gap >= 10. Keeps symbol scale and internal
connections unchanged. Default flow:z preserves functional order, left-to-right,
top-aligned rows, then down by the tallest frame plus gap. Never backfills holes
under shorter frames or earlier pages. Same-page groups gather at their earliest
input member, preserve member order, and move to a new page atomically.
Explicit flow:compact retains the legacy free-packing search. --flow overrides
sheet.flow. Neither mode guarantees minimum page count.
Optional top-level spacing unifies zone inner padding, sheet padding and zone gap.
Optional zone placement:{samePageAs:<zone ID>,preferAdjacent?:true} keeps related
zones on one page; adjacency preference cannot override the selected reading flow.
Outputs pages[] accepted by layout-render. Incomplete zones require --diagnostic.
Optional zone variants (at most 4 complete local shapes, including baseline) are
validated before bounded Z-flow selection. The output materializes one shape per
zone, records selectedVariantId and removes unselected variants. No local edits.
Variants require Z flow; compact rejects them. variantSearch reports the bounded
selection effort; page count is not a global minimum proof.
Does not edit EDA, merge nets, or generate an Apply queue.`, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if from == "" || out == "" {
			return fmt.Errorf("--from and --out required")
		}
		a, _ := filepath.Abs(from)
		b, _ := filepath.Abs(out)
		fi, _ := os.Stat(from)
		fo, _ := os.Stat(out)
		if a == b || (fi != nil && fo != nil && os.SameFile(fi, fo)) {
			return fmt.Errorf("output must not overwrite input")
		}
		raw, e := os.ReadFile(from)
		if e != nil {
			return e
		}
		var in SchematicRenderInput
		if e = connectivity.DecodeStrictDesignJSON(raw, &in); e != nil {
			return e
		}
		if e = validateRenderSheetJSON(raw); e != nil {
			return e
		}
		if e = validateRenderMeasurementsJSON(raw); e != nil {
			return e
		}
		if cmd.Flags().Changed("flow") {
			if flow != "z" && flow != "compact" {
				return fmt.Errorf("--flow must be z or compact")
			}
			if in.Sheet == nil {
				return fmt.Errorf("sheet required for --flow")
			}
			in.Sheet.Flow = flow
		}
		if !diagnostic {
			if e = validateCompleteLayoutPreview(in); e != nil {
				return e
			}
		}
		in.Diagnostic = in.Diagnostic || diagnostic
		plan, e := PlanSchematicSheets(in)
		if e != nil {
			return e
		}
		data, e := json.MarshalIndent(plan, "", "  ")
		if e != nil {
			return e
		}
		return os.WriteFile(out, append(data, '\n'), 0644)
	}}
	c.Flags().StringVar(&from, "from", "", "zone layouts with sheet constraints JSON")
	c.Flags().StringVar(&out, "out", "", "page plan JSON output")
	c.Flags().StringVar(&flow, "flow", "z", "reading flow: z or compact; overrides sheet.flow only when explicitly supplied")
	c.Flags().BoolVar(&diagnostic, "diagnostic", false, "explicitly pack incomplete diagnostics, not a completed layout")
	return c
}

func validateRenderSheetJSON(raw []byte) error {
	var top map[string]json.RawMessage
	if e := json.Unmarshal(raw, &top); e != nil {
		return e
	}
	b, ok := top["sheet"]
	if !ok {
		return nil
	}
	var sheet map[string]json.RawMessage
	if e := json.Unmarshal(b, &sheet); e != nil {
		return e
	}
	if rawFlow, present := sheet["flow"]; present {
		var flow string
		if json.Unmarshal(rawFlow, &flow) != nil || (flow != "z" && flow != "compact") {
			return fmt.Errorf("explicit sheet.flow must be z or compact")
		}
	}
	if len(top["spacing"]) != 0 && string(top["spacing"]) != "null" {
		var spacing float64
		if e := json.Unmarshal(top["spacing"], &spacing); e != nil {
			return e
		}
		if e := validateSchematicSpacing(&spacing); e != nil {
			return e
		}
		for _, key := range []string{"padding", "gap"} {
			if value, supplied := sheet[key]; supplied {
				var n float64
				if string(value) == "null" || json.Unmarshal(value, &n) != nil || n != spacing {
					return fmt.Errorf("explicit sheet.%s must equal spacing", key)
				}
			}
		}
	}
	required := []string{"bounds", "border", "keepouts"}
	if len(top["spacing"]) == 0 || string(top["spacing"]) == "null" {
		required = append(required, "padding", "gap")
	}
	for _, key := range required {
		if len(sheet[key]) == 0 || string(sheet[key]) == "null" {
			return fmt.Errorf("sheet requires explicit %s", key)
		}
	}
	for _, key := range []string{"bounds", "border"} {
		var box map[string]json.RawMessage
		if e := json.Unmarshal(sheet[key], &box); e != nil {
			return e
		}
		for _, coord := range []string{"minX", "minY", "maxX", "maxY"} {
			if len(box[coord]) == 0 || string(box[coord]) == "null" {
				return fmt.Errorf("sheet.%s requires %s", key, coord)
			}
		}
	}
	var keepouts []map[string]json.RawMessage
	if e := json.Unmarshal(sheet["keepouts"], &keepouts); e != nil {
		return e
	}
	for _, box := range keepouts {
		for _, coord := range []string{"minX", "minY", "maxX", "maxY"} {
			if len(box[coord]) == 0 || string(box[coord]) == "null" {
				return fmt.Errorf("keepout requires %s", coord)
			}
		}
	}
	return nil
}

// Reject absent coordinates rather than letting JSON zero values invent them.
func validateRenderMeasurementsJSON(raw []byte) error {
	if err := validateRenderVariantsJSON(raw); err != nil {
		return err
	}
	if err := validateSchematicZonePlacementsJSON(raw); err != nil {
		return err
	}
	var top struct {
		Zones []struct {
			SheetPosition map[string]json.RawMessage `json:"sheetPosition"`
			Layout        struct {
				Placements []map[string]json.RawMessage `json:"placements"`
				Flags      []map[string]json.RawMessage `json:"flags"`
				Wires      []map[string]json.RawMessage `json:"wires"`
			} `json:"layout"`
		} `json:"zones"`
	}
	if e := json.Unmarshal(raw, &top); e != nil {
		return e
	}
	require := func(m map[string]json.RawMessage, keys ...string) error {
		for _, k := range keys {
			if len(m[k]) == 0 || string(m[k]) == "null" {
				return fmt.Errorf("explicit geometry field required: %s", k)
			}
		}
		return nil
	}
	for _, z := range top.Zones {
		for _, w := range z.Layout.Wires {
			if e := require(w, "net", "points"); e != nil {
				return e
			}
			var points [][]json.RawMessage
			if e := json.Unmarshal(w["points"], &points); e != nil {
				return e
			}
			if len(points) < 2 {
				return fmt.Errorf("wire requires at least two explicit points")
			}
			for _, p := range points {
				if len(p) != 2 {
					return fmt.Errorf("wire point requires exactly two coordinates")
				}
				for _, v := range p {
					var n float64
					if string(v) == "null" || json.Unmarshal(v, &n) != nil {
						return fmt.Errorf("wire requires explicit numeric coordinates")
					}
				}
			}
		}
		for _, f := range z.Layout.Flags {
			if e := require(f, "net", "kind", "pinX", "pinY", "direction", "offset"); e != nil {
				return e
			}
		}
		if z.SheetPosition != nil {
			if e := require(z.SheetPosition, "x", "y"); e != nil {
				return e
			}
		}
		for _, c := range z.Layout.Placements {
			if e := require(c, "x", "y", "rotation", "mirror", "bbox", "pins"); e != nil {
				return e
			}
			var box map[string]json.RawMessage
			if e := json.Unmarshal(c["bbox"], &box); e != nil {
				return e
			}
			if e := require(box, "minX", "minY", "maxX", "maxY"); e != nil {
				return e
			}
			if rawBoxes, supplied := c["textBboxes"]; supplied {
				var textBoxes []map[string]json.RawMessage
				if string(rawBoxes) == "null" || json.Unmarshal(rawBoxes, &textBoxes) != nil {
					return fmt.Errorf("textBboxes requires explicit measured boxes")
				}
				for _, textBox := range textBoxes {
					if e := require(textBox, "minX", "minY", "maxX", "maxY"); e != nil {
						return e
					}
				}
			}
			var pins []map[string]json.RawMessage
			if e := json.Unmarshal(c["pins"], &pins); e != nil {
				return e
			}
			for _, p := range pins {
				if e := require(p, "number", "net", "x", "y"); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
