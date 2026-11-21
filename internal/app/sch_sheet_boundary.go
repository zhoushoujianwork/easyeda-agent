package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// sheetBoundaryFindings is sch check's sheet-boundary ruler over EVERY drawn
// primitive — parts, markers AND wires. Live 2026-10-13: wires crossed the title
// block and labels left the frame, yet titleblock-overlap (parts/markers only),
// layout-lint (part bodies only) and the electrical rules all stayed at 0; only
// `sch clusters --strict` saw the off-sheet part, through a coarse group bbox.
//
// It reuses the page geometry the other rulers use — sheetBBoxOf, schUsableArea
// and the title-block keep-out — so "inside the sheet" means one thing.
//
//	out-of-sheet        error  part/marker bbox or wire segment leaves the usable area
//	titleblock-overlap  warn   a wire segment enters the title-block keep-out
//	                           (info when the keep-out is an estimate, as for parts)
//
// No sheet (paperless) → no findings: there is no boundary to judge.
func sheetBoundaryFindings(comps []layoutComp, wires []schGroupWire, sheet, titleBlock *layoutBBox, tbSource string, eps float64) []checkFinding {
	if sheet == nil {
		return nil
	}
	usable := schUsableArea(*sheet)
	inside := func(b layoutBBox) bool {
		return b.MinX >= usable.MinX-eps && b.MaxX <= usable.MaxX+eps && b.MinY >= usable.MinY-eps && b.MaxY <= usable.MaxY+eps
	}
	var out []checkFinding
	for _, c := range comps {
		if c.BBox == nil || (c.ComponentType != schLayoutPartType && c.ComponentType != "" && !isSchMarker(c.ComponentType)) {
			continue // the sheet itself and unmeasured primitives are not judged
		}
		if inside(*c.BBox) {
			continue
		}
		b := *c.BBox
		out = append(out, checkFinding{
			Type: "out-of-sheet", Level: "error", PrimitiveId: c.ID, ComponentType: c.ComponentType,
			Designator: c.Designator, MarkerNet: c.Net, BBox: c.BBox,
			Message: fmt.Sprintf("%s(%s) 超出图纸可用区（%.0f..%.0f × %.0f..%.0f，可用区 %.0f..%.0f × %.0f..%.0f）— 打印不出来，移回图内",
				markerLabel(c), c.ComponentType, b.MinX, b.MaxX, b.MinY, b.MaxY, usable.MinX, usable.MaxX, usable.MinY, usable.MaxY),
		})
	}
	var inner *layoutBBox
	if titleBlock != nil {
		// Only positive intrusion counts; a wire running along the keep-out edge does not.
		in := layoutBBox{MinX: titleBlock.MinX + eps, MinY: titleBlock.MinY + eps, MaxX: titleBlock.MaxX - eps, MaxY: titleBlock.MaxY - eps}
		if in.MaxX > in.MinX && in.MaxY > in.MinY {
			inner = &in
		}
	}
	confirmed := tbSource == sheetSourceKnownTemplate
	for _, w := range wires {
		offSheet, inTitle := false, false
		for _, s := range schDesignatorWireSegments(w) {
			seg := layoutBBox{MinX: math.Min(s[0], s[2]), MaxX: math.Max(s[0], s[2]), MinY: math.Min(s[1], s[3]), MaxY: math.Max(s[1], s[3])}
			if !offSheet && !inside(seg) {
				offSheet = true
				out = append(out, checkFinding{
					Type: "out-of-sheet", Level: "error", WirePrimitiveId: w.ID,
					At:      &checkPoint{X: round2(s[0]), Y: round2(s[1])},
					Message: fmt.Sprintf("导线 %s 的线段 (%.0f,%.0f)→(%.0f,%.0f) 超出图纸可用区 — 连同它挂的标签移回图内", w.ID, s[0], s[1], s[2], s[3]),
				})
			}
			if !inTitle && inner != nil && schSegmentCrossesBox(s[0], s[1], s[2], s[3], *inner) {
				inTitle = true
				level, msg := "warn", fmt.Sprintf("导线 %s 穿过标题栏 keep-out（线段 (%.0f,%.0f)→(%.0f,%.0f)）— 改走线或挪开所连器件", w.ID, s[0], s[1], s[2], s[3])
				if !confirmed {
					level = "info"
					msg = fmt.Sprintf("导线 %s 命中基于估计的图签区（source=%s）— keep-out 非 A4 标定，仅供参考", w.ID, tbSource)
				}
				out = append(out, checkFinding{
					Type: "titleblock-overlap", Level: level, WirePrimitiveId: w.ID, Keepout: titleBlock,
					At: &checkPoint{X: round2(s[0]), Y: round2(s[1])}, Message: msg,
				})
			}
		}
	}
	return out
}

// visibleTextItem is one measured entry of schematic.visible_text.bboxes.
type visibleTextItem struct {
	ID            string      `json:"id"`
	Kind          string      `json:"kind"` // text | attribute
	Content       string      `json:"content"`
	ParentID      string      `json:"parentId"`
	ComponentType string      `json:"componentType"`
	Designator    string      `json:"designator"`
	Key           string      `json:"key"`
	BBox          *layoutBBox `json:"bbox"`
}

// visibleTextBoundaryFindings extends the sheet-boundary ruler to every visible
// text: free text and shown attributes (parts' Name/Value…, the sheet's own
// title-block fields). Live 2026-10-13: showing the title-block Name and
// Description drew them below the frame and no rule saw it. Text is judged
// against the PAPER, not the 12-raw placement margin of schUsableArea: frame
// titles legitimately sit in that margin, text past the paper edge is lost.
func visibleTextBoundaryFindings(items []visibleTextItem, sheet *layoutBBox, eps float64) []checkFinding {
	if sheet == nil {
		return nil
	}
	paper := *sheet
	var out []checkFinding
	for _, it := range items {
		b := it.BBox
		if b == nil || (b.MinX >= paper.MinX-eps && b.MaxX <= paper.MaxX+eps && b.MinY >= paper.MinY-eps && b.MaxY <= paper.MaxY+eps) {
			continue
		}
		what := visibleTextLabel(it)
		out = append(out, checkFinding{
			Type: "out-of-sheet", Level: "warn", PrimitiveId: it.ID, Designator: it.Designator, BBox: b,
			Message: fmt.Sprintf("%s 超出纸张（%.0f..%.0f × %.0f..%.0f）— 打印不出来；移回图内，或不需要显示时关闭该属性的显示",
				what, b.MinX, b.MaxX, b.MinY, b.MaxY),
		})
	}
	return out
}

// visibleTextFindings reads the measured visible text and applies the paper
// ruler, then the title-table stroke ruler for texts reaching the title block.
// An older connector without the action is reported once on stderr (no
// finding: the rule simply does not exist there); a failed read or items the
// host could not measure are surfaced, never treated as "inside".
func visibleTextFindings(cfg *appConfig, window string, sheet, titleBlock *layoutBBox, eps float64, stderr io.Writer) []checkFinding {
	if sheet == nil {
		return nil
	}
	res, err := requestAction(cfg, "schematic.visible_text.bboxes", window, map[string]any{})
	if err != nil {
		if res != nil && res.errorCode == "UNKNOWN_ACTION" {
			fmt.Fprintln(stderr, "sch check: visible-text out-of-sheet skipped — connector predates schematic.visible_text.bboxes")
			return nil
		}
		return []checkFinding{{Type: "visible-text-unverified", Level: "warn", Message: "可见文字/属性是否越出图纸无法核验（读取失败）: " + err.Error()}}
	}
	var parsed struct {
		Items      []visibleTextItem `json:"items"`
		Unmeasured []visibleTextItem `json:"unmeasured"`
	}
	raw, _ := json.Marshal(res.Result)
	if json.Unmarshal(raw, &parsed) != nil {
		return []checkFinding{{Type: "visible-text-unverified", Level: "warn", Message: "可见文字/属性测量结果无法解析"}}
	}
	out := visibleTextBoundaryFindings(parsed.Items, sheet, eps)
	out = append(out, liveTitleBlockTextLineFindings(cfg, window, parsed.Items, titleBlock, eps, stderr)...)
	if n := len(parsed.Unmeasured); n > 0 {
		out = append(out, checkFinding{Type: "visible-text-unverified", Level: "info",
			Message: fmt.Sprintf("%d 个可见文字/属性宿主未给出 bbox，越界情况未核验", n)})
	}
	return out
}
