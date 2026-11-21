package app

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

// Title-block text vs. table strokes (issue #324). titleblock-overlap judges
// parts, markers and wires against the keep-out rectangle; it cannot see a
// shown title-block field (or free text) that sits ON a table line, because
// the table's rows and columns are not exposed by any object API. The official
// whole-page SVG export draws them as vectors, so the check reads that export
// only when some visible text actually reaches into the title block.

// exportSheetGeometry renders the active page through the official exporter
// and parses its sheet/title-table vectors. Read-only: no document changes.
func exportSheetGeometry(cfg *appConfig, window string) (sheetGeometry, error) {
	res, err := requestActionTimed(cfg, "schematic.export.image", window, map[string]any{"format": "svg", "scope": "page"}, 90*time.Second)
	if err != nil {
		return sheetGeometry{}, err
	}
	if res == nil || len(res.Artifacts) == 0 || res.Artifacts[0].Path == "" {
		return sheetGeometry{}, fmt.Errorf("official page export returned no SVG artifact")
	}
	data, err := os.ReadFile(res.Artifacts[0].Path)
	if err != nil {
		return sheetGeometry{}, fmt.Errorf("read exported SVG: %w", err)
	}
	g, err := parseSheetGeometrySVG(data)
	if err != nil {
		return sheetGeometry{}, err
	}
	g.Warnings = []string{"exported from the active page by the official exporter just now; bounds follow vector centerlines"}
	if len(g.TitleBlock.Lines) == 0 {
		g.Warnings = append(g.Warnings, "no title-table stroke lines in this export; text-on-line checks cannot run against it")
	}
	return g, nil
}

// textsReachingTitleBlock keeps the measured texts whose bbox positively
// overlaps the (possibly estimated) title-block rectangle grown by margin.
func textsReachingTitleBlock(items []visibleTextItem, titleBlock *layoutBBox, margin float64) []visibleTextItem {
	if titleBlock == nil {
		return nil
	}
	grown := layoutBBox{MinX: titleBlock.MinX - margin, MinY: titleBlock.MinY - margin, MaxX: titleBlock.MaxX + margin, MaxY: titleBlock.MaxY + margin}
	var out []visibleTextItem
	for _, it := range items {
		if it.BBox == nil {
			continue
		}
		if ox, oy, ok := overlapExtent(*it.BBox, grown); ok && ox > 0 && oy > 0 {
			out = append(out, it)
		}
	}
	return out
}

// titleBlockTextLineFindings reports each visible text whose measured bbox is
// crossed by a drawn title-table stroke. Only strokes passing through the
// text's interior (bbox shrunk by eps) count, so text resting inside a cell or
// touching a line by float noise is not reported. Pure, for table tests.
func titleBlockTextLineFindings(items []visibleTextItem, g sheetGeometry, eps float64) []checkFinding {
	table := g.TitleBlock.BBox
	if table == nil || len(g.TitleBlock.Lines) == 0 {
		return nil
	}
	var out []checkFinding
	for _, it := range items {
		b := it.BBox
		if b == nil {
			continue
		}
		if ox, oy, ok := overlapExtent(*b, *table); !ok || ox <= 0 || oy <= 0 {
			continue
		}
		inner := layoutBBox{MinX: b.MinX + eps, MinY: b.MinY + eps, MaxX: b.MaxX - eps, MaxY: b.MaxY - eps}
		if inner.MaxX <= inner.MinX || inner.MaxY <= inner.MinY {
			continue
		}
		var hit *titleBlockLine
		for i, l := range g.TitleBlock.Lines {
			if schSegmentCrossesBox(l.X1, l.Y1, l.X2, l.Y2, inner) {
				hit = &g.TitleBlock.Lines[i]
				break
			}
		}
		if hit == nil {
			continue
		}
		where := fmt.Sprintf("线段 (%.2f,%.2f)→(%.2f,%.2f)", hit.X1, hit.Y1, hit.X2, hit.Y2)
		switch {
		case hit.Y1 == hit.Y2:
			where = fmt.Sprintf("y=%.2f 横线", hit.Y1)
		case hit.X1 == hit.X2:
			where = fmt.Sprintf("x=%.2f 竖线", hit.X1)
		}
		cx, cy := (b.MinX+b.MaxX)/2, (b.MinY+b.MaxY)/2
		at := &checkPoint{X: round2(cx), Y: round2(cy)}
		if hit.Y1 == hit.Y2 {
			at.Y = round2(hit.Y1)
		} else if hit.X1 == hit.X2 {
			at.X = round2(hit.X1)
		}
		out = append(out, checkFinding{
			Type: "titleblock-text-on-line", Level: "warn", PrimitiveId: it.ID, Designator: it.Designator,
			BBox: b, At: at, Keepout: table,
			Message: fmt.Sprintf("%s 压在图签表格的 %s 上（文字 %.1f..%.1f × %.1f..%.1f，跨 %s）— 用 sch attribute-move 移进目标格内；各格 bbox 见 `easyeda sch sheet-geometry --exact`",
				visibleTextLabel(it), where, b.MinX, b.MaxX, b.MinY, b.MaxY, cellNamesOverlapping(g.TitleBlock.Cells, *b)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PrimitiveId < out[j].PrimitiveId })
	return out
}

func visibleTextLabel(it visibleTextItem) string {
	if it.Kind != "attribute" {
		return "文字「" + it.Content + "」"
	}
	owner := it.Designator
	if owner == "" {
		owner = it.ComponentType
	}
	return fmt.Sprintf("%s 的可见属性 %s", owner, it.Key)
}

// cellNamesOverlapping names the cells a text bbox covers, preferring the
// cell's own text, then "<row label>右格", then its id.
func cellNamesOverlapping(cells []titleBlockCell, b layoutBBox) string {
	var names []string
	for _, c := range cells {
		ox, oy, ok := overlapExtent(b, c.BBox)
		if !ok || ox <= 0 || oy <= 0 {
			continue
		}
		name := c.Text
		switch {
		case name != "":
		case c.LeftText != "":
			name = c.LeftText + "右格"
		default:
			name = fmt.Sprintf("空格[%.0f,%.0f→%.0f,%.0f]", c.BBox.MinX, c.BBox.MinY, c.BBox.MaxX, c.BBox.MaxY)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "未知单元格"
	}
	return strings.Join(names, " / ")
}

// liveTitleBlockTextLineFindings gates the export on texts that reach the
// estimated title block, so ordinary pages pay no export cost. A failed or
// unsupported export is surfaced as unverified, never treated as clean.
func liveTitleBlockTextLineFindings(cfg *appConfig, window string, items []visibleTextItem, titleBlock *layoutBBox, eps float64, stderr io.Writer) []checkFinding {
	near := textsReachingTitleBlock(items, titleBlock, 20)
	if len(near) == 0 {
		return nil
	}
	g, err := exportSheetGeometry(cfg, window)
	if err == nil && len(g.TitleBlock.Lines) == 0 {
		err = fmt.Errorf("export carries no title-table strokes")
	}
	if err != nil {
		fmt.Fprintf(stderr, "sch check: titleblock text-on-line unverified — %v\n", err)
		return []checkFinding{{Type: "titleblock-text-unverified", Level: "warn",
			Message: fmt.Sprintf("%d 个可见文字进入图签区，但无法从官方导出读取表格线，压线情况未核验: %v", len(near), err)}}
	}
	return titleBlockTextLineFindings(near, g, math.Max(eps, 0.5))
}
