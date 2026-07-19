package app

// cmd_sch_zone_draw.go — `sch zone-draw`: make the claimed functional zones
// VISIBLE on the schematic sheet (行业规范的「先看区、再看线」分区框).
//
// `sch zones set` persists the S0 partitioning and layout-lint verifies it
// mechanically; this command draws it for humans: one dashed rectangle + a
// "module (zone)" label per claim, resolved from the LIVE sheet bbox with the
// same zoneRect() geometry the violation rule uses — what you see IS what the
// gate checks.
//
// Implementation note: the schematic graphics API (eda.sch_PrimitiveRectangle /
// sch_PrimitiveText — full CRUD, probed live 2026-07-19 on ceshi) has no typed
// action yet, so this goes through the debug.exec_js hatch, the documented path
// for scriptable behavior that doesn't warrant a connector re-import. Created
// primitive ids are recorded in the project workflow state so redraw/--clear
// removes exactly what this tool created and never touches user graphics.

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/workflow"
)

// schZoneFrameInset shrinks each zone rectangle so adjacent frames don't sit
// on the exact same boundary line (schematic units).
const schZoneFrameInset = 4

// buildZoneDrawJS renders the one-shot exec_js script: create every frame
// rect + label, return their ids. Pure (unit-testable).
func buildZoneDrawJS(zones map[string]*schZoneClaim, sheet layoutBBox, color string) string {
	var names []string
	for n := range zones {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("const rects=[], texts=[];\n")
	for _, name := range names {
		zc := zones[name]
		if zc == nil || !pcbZoneNames[zc.Zone] {
			continue
		}
		r := zoneRect(zc.Zone, sheet)
		x := r.MinX + schZoneFrameInset
		y := r.MinY + schZoneFrameInset
		w := (r.MaxX - r.MinX) - 2*schZoneFrameInset
		h := (r.MaxY - r.MinY) - 2*schZoneFrameInset
		if w <= 0 || h <= 0 {
			continue
		}
		label, _ := json.Marshal(fmt.Sprintf("%s (%s)", name, zc.Zone))
		colorJS, _ := json.Marshal(color)
		// The canvas is y-UP, so the frame's visual TOP edge is MaxY; the label
		// sits just inside the top-left corner. The rectangle API's topLeftY is
		// document-space y of the anchor corner — passing MinY with the height
		// spans MinY..MaxY either way. lineType 1 = DASHED (ESCH_PrimitiveLineType).
		fmt.Fprintf(&b, "{ const rc = await eda.sch_PrimitiveRectangle.create(%g, %g, %g, %g, 0, 0, %s, null, 1, 1);\n",
			x, y, w, h, colorJS)
		fmt.Fprintf(&b, "  if (rc) rects.push(rc.getState_PrimitiveId());\n")
		fmt.Fprintf(&b, "  const tx = await eda.sch_PrimitiveText.create(%g, %g, %s, 0, %s, null, 9);\n",
			x+4, y+h-6, label, colorJS)
		fmt.Fprintf(&b, "  if (tx) texts.push(tx.getState_PrimitiveId()); }\n")
	}
	b.WriteString("return {rects, texts};")
	return b.String()
}

// buildZoneClearJS renders the script deleting previously drawn frames.
func buildZoneClearJS(f *workflow.SchZoneFrames) string {
	rects, _ := json.Marshal(f.Rects)
	texts, _ := json.Marshal(f.Texts)
	return fmt.Sprintf(`const rects=%s, texts=%s;
let deleted = 0;
if (rects.length) { if (await eda.sch_PrimitiveRectangle.delete(rects)) deleted += rects.length; }
if (texts.length) { if (await eda.sch_PrimitiveText.delete(texts)) deleted += texts.length; }
return {deleted};`, rects, texts)
}

// execZoneJS runs a zone-draw script through debug.exec_js and returns the
// result value map.
func execZoneJS(cfg *appConfig, window, code string) (map[string]any, error) {
	res, err := requestActionTimed(cfg, "debug.exec_js", window, map[string]any{"code": code}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	v, _ := res.Result["value"].(map[string]any)
	if v == nil {
		return nil, fmt.Errorf("exec_js returned no value (result: %v)", res.Result)
	}
	return v, nil
}

func asStringSlice(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, it := range raw {
		if s := asString(it); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// newSchZoneDrawCmd builds `sch zone-draw`.
func newSchZoneDrawCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var color string
	var clear bool
	c := &cobra.Command{
		Use:   "zone-draw",
		Short: "Draw the claimed functional zones as dashed frames + labels on the sheet (--clear removes them)",
		Long: `Visualize the ` + "`sch zones set`" + ` claims: one dashed rectangle + "module (zone)"
label per claim, resolved from the LIVE sheet bbox with the same geometry the
layout-lint zone-violation rule uses — what you see is exactly what the gate
checks (行业规范「先看区、再看线」的分区框标注).

Frames are annotation graphics, not electrical objects. Their primitive ids are
recorded in the project workflow state; re-running redraws (old frames removed
first) and --clear deletes them without touching any user graphics. Requires
the schematic page to be the ACTIVE document.`,
		Example: `  easyeda sch zones set --spec s0.json --project ceshi
  easyeda sch zone-draw --project ceshi
  easyeda sch zone-draw --clear --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			zones, project, err := loadSchZoneClaims(cfg, *window)
			if err != nil {
				return err
			}
			st, err := loadPcbStageState(project)
			if err != nil {
				return err
			}

			// Remove previously drawn frames first (both for --clear and redraw).
			if st.SchZoneFrameIds != nil && (len(st.SchZoneFrameIds.Rects) > 0 || len(st.SchZoneFrameIds.Texts) > 0) {
				v, derr := execZoneJS(cfg, *window, buildZoneClearJS(st.SchZoneFrameIds))
				if derr != nil {
					fmt.Fprintf(stderr, "warn: clearing previous frames failed (%v) — they may already be gone (reload loses in-memory graphics)\n", derr)
				} else {
					fmt.Fprintf(stderr, "cleared %v previous frame primitive(s)\n", v["deleted"])
				}
				st.SchZoneFrameIds = nil
				if err := savePcbStageState(st); err != nil {
					return err
				}
			} else if clear {
				fmt.Fprintln(stdout, "no zone frames recorded — nothing to clear")
				return nil
			}
			if clear {
				fmt.Fprintln(stdout, "zone frames cleared")
				return nil
			}

			if len(zones) == 0 {
				return fmt.Errorf("no schematic zone claims for %q — run `sch zones set --spec <s0-spec.json>` first", project)
			}
			// Live sheet bbox — same frame of reference as the violation rule.
			res, err := requestAction(cfg, "schematic.components.list", *window, map[string]any{"includeBBox": true})
			if err != nil {
				return err
			}
			comps, perr := parseLayoutComps(res.Result)
			if perr != nil {
				return perr
			}
			sheet := sheetBBoxOf(comps)
			if sheet == nil {
				return fmt.Errorf("no sheet bbox on the active page — switch to the schematic page (`easyeda doc switch`) or place a drawing sheet first")
			}

			v, err := execZoneJS(cfg, *window, buildZoneDrawJS(zones, *sheet, color))
			if err != nil {
				return err
			}
			frames := &workflow.SchZoneFrames{
				Rects: asStringSlice(v["rects"]),
				Texts: asStringSlice(v["texts"]),
				At:    nowRFC3339(),
			}
			st.SchZoneFrameIds = frames
			if err := savePcbStageState(st); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "drew %d zone frame(s) + %d label(s) for %d claim(s) — annotation only; `sch zone-draw --clear` removes them\n",
				len(frames.Rects), len(frames.Texts), len(zones))
			return nil
		},
	}
	c.Flags().StringVar(&color, "color", "#AA00AA", "frame + label color")
	c.Flags().BoolVar(&clear, "clear", false, "remove the frames drawn by the last zone-draw")
	return c
}
