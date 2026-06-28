package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// ── sheet-geometry: normalized sheet bounds + title-block keep-out (issue #26) ─
//
// Placement/routing planners (sch autoconnect #24, sch autolayout #25) must never
// drop flags or parts on top of the drawing sheet's 图框/明细表 (title block). They
// need a single, normalized keep-out rectangle instead of A4 coordinates
// re-hardcoded per tool. This is that source.
//
// EasyEDA Pro exposes no set-paper-size API and NO separate bbox for the title
// block itself, so the geometry is DERIVED (issue Option D — hybrid):
//
//  1. sheet bbox  — live, from schematic.components.list(includeBBox) where
//     componentType == "sheet" (Option A).
//  2. template    — best-effort match by the sheet's aspect ratio against the
//     known A-series ratio (≈√2). The public API exposes no reliable
//     template-id field (deviceUuid/symbol name are not surfaced), so the
//     aspect ratio is the detection key (Option C).
//  3. title block — a corner sub-rect computed by a normalized ratio from the
//     matched template (Option C/D).
//  4. visibility  — schematic.titleblock.get → showTitleBlock (Option B); a
//     hidden title block emits no keep-out.
//
// Every result carries provenance ("known-template-ratio" / "fallback-ratio" /
// "none") plus warnings, so consumers never get false precision. The ratio table
// here is the single source that sch autoconnect's titleBlockKeepout consumes.

// titleBlockRatio is a corner sub-rectangle of the sheet expressed as fractions
// of the sheet width/height.
type titleBlockRatio struct {
	WidthFrac  float64
	HeightFrac float64
}

// sheetTemplate maps a recognizable sheet (by aspect ratio) to its title-block
// footprint. A-series sheets all share the √2 aspect, so one landscape + one
// portrait entry covers A4/A3/A2/A1/A0 — the title block is proportionally the
// same fraction of the page.
type sheetTemplate struct {
	Name       string
	Aspect     float64 // sheet width / height
	AspectTol  float64 // match tolerance on aspect
	TitleBlock titleBlockRatio
}

// defaultTitleBlockRatio is applied when no template matches (fallback). The
// rightmost ~22% width × bottom ~14% height is the usual EasyEDA A-series
// title-block footprint — the same numbers sch autoconnect used to hardcode.
var defaultTitleBlockRatio = titleBlockRatio{WidthFrac: 0.22, HeightFrac: 0.14}

// sheetTemplates is the known sheet → title-block ratio table. Mirrored for
// humans/skills in skills/easyeda-conventions/references/sheet-templates.json;
// this Go table is the runtime authority (the CLI is the interface planners use).
var sheetTemplates = []sheetTemplate{
	{Name: "a-series-landscape", Aspect: 1.414, AspectTol: 0.06, TitleBlock: defaultTitleBlockRatio},
	{Name: "a-series-portrait", Aspect: 0.707, AspectTol: 0.04, TitleBlock: titleBlockRatio{WidthFrac: 0.31, HeightFrac: 0.10}},
}

// Provenance values for titleBlock.source / how the keep-out was derived.
const (
	sheetSourceKnownTemplate = "known-template-ratio" // sheet bbox live + aspect matched a template
	sheetSourceFallback      = "fallback-ratio"       // sheet bbox live, aspect unrecognized → generic ratio
	sheetSourceNone          = "none"                 // no sheet bbox, or title block hidden → no keep-out
)

// sheetInfo describes the drawing sheet itself.
type sheetInfo struct {
	Template string      `json:"template,omitempty"`
	BBox     *layoutBBox `json:"bbox"`
}

// titleBlockInfo describes the derived title-block rectangle and its provenance.
type titleBlockInfo struct {
	Visible *bool       `json:"visible,omitempty"`
	BBox    *layoutBBox `json:"bbox,omitempty"`
	Source  string      `json:"source"`
}

// keepout is one named, normalized exclusion rectangle planners must avoid.
type keepout struct {
	Name string      `json:"name"`
	BBox *layoutBBox `json:"bbox"`
	Hard bool        `json:"hard"`
}

// sheetGeometry is the full normalized result, shaped to the issue #26 contract.
type sheetGeometry struct {
	Sheet      sheetInfo      `json:"sheet"`
	TitleBlock titleBlockInfo `json:"titleBlock"`
	Keepouts   []keepout      `json:"keepouts"`
	Warnings   []string       `json:"warnings"`
}

// matchSheetTemplate picks the template whose aspect matches w/h within tolerance.
// Returns matched=false (and a generic template carrying the default ratio) when
// nothing matches, so the caller can downgrade provenance to fallback.
func matchSheetTemplate(w, h float64) (sheetTemplate, bool) {
	aspect := w / h
	for _, t := range sheetTemplates {
		if math.Abs(aspect-t.Aspect) <= t.AspectTol {
			return t, true
		}
	}
	return sheetTemplate{Name: "unknown", TitleBlock: defaultTitleBlockRatio}, false
}

// deriveSheetGeometry is the pure core: given the live sheet bbox (or nil) and
// the title block's visibility (or nil when unknown), produce the normalized
// geometry with provenance + warnings. Kept free of I/O for unit-testing.
//
// Coordinate note: EasyEDA's 图框/明细表 sits in the BOTTOM-RIGHT corner of the
// returned bbox space (larger y is lower), matching sch autoconnect's existing
// keep-out — so the carved rect is the high-x, high-y corner of the sheet bbox.
func deriveSheetGeometry(sheet *layoutBBox, showTitleBlock *bool) sheetGeometry {
	g := sheetGeometry{Keepouts: []keepout{}, Warnings: []string{}}
	g.TitleBlock.Visible = showTitleBlock
	g.TitleBlock.Source = sheetSourceNone

	if sheet == nil {
		g.Warnings = append(g.Warnings,
			"no sheet primitive found (componentType \"sheet\" with bbox); cannot derive sheet bounds or title-block keep-out")
		return g
	}
	g.Sheet.BBox = sheet

	w := sheet.MaxX - sheet.MinX
	h := sheet.MaxY - sheet.MinY
	if w <= 0 || h <= 0 {
		g.Warnings = append(g.Warnings,
			"sheet bbox has non-positive dimensions; title-block keep-out not derived")
		return g
	}

	tmpl, matched := matchSheetTemplate(w, h)
	g.Sheet.Template = tmpl.Name
	if matched {
		g.TitleBlock.Source = sheetSourceKnownTemplate
	} else {
		g.TitleBlock.Source = sheetSourceFallback
		g.Warnings = append(g.Warnings, fmt.Sprintf(
			"sheet aspect %.3f did not match a known template; title-block keep-out uses a generic fallback ratio, not template geometry",
			w/h))
	}

	// Respect an explicitly-hidden title block: no keep-out to enforce.
	if showTitleBlock != nil && !*showTitleBlock {
		g.TitleBlock.Source = sheetSourceNone
		g.Warnings = append(g.Warnings,
			"title block is hidden (showTitleBlock=false); no title-block keep-out emitted")
		return g
	}
	if showTitleBlock == nil {
		g.Warnings = append(g.Warnings,
			"title-block visibility unknown (showTitleBlock not reported); assuming visible")
	}

	ratio := tmpl.TitleBlock
	tb := &layoutBBox{
		MinX: round2(sheet.MaxX - ratio.WidthFrac*w),
		MinY: round2(sheet.MaxY - ratio.HeightFrac*h),
		MaxX: sheet.MaxX,
		MaxY: sheet.MaxY,
	}
	g.TitleBlock.BBox = tb
	g.Keepouts = append(g.Keepouts, keepout{Name: "titleBlock", BBox: tb, Hard: true})
	return g
}

// runSheetGeometry pulls the live sheet bbox + title-block visibility, derives the
// normalized geometry, and prints it. Read-only: it always exits zero (a missing
// sheet is reported as a warning, not an error) since it is a query, not a gate.
func runSheetGeometry(cfg *appConfig, window string, asJSON bool, stdout, stderr io.Writer) error {
	// 1. Sheet bbox (live) from components.list(includeBBox).
	res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{"includeBBox": true})
	if err != nil {
		return err
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		return perr
	}
	var sheet *layoutBBox
	for _, c := range comps {
		if c.ComponentType == "sheet" && c.BBox != nil {
			sheet = c.BBox
			break
		}
	}

	// 2. Title-block visibility (best effort; non-fatal if unavailable).
	var showTB *bool
	if tb, terr := requestAction(cfg, "schematic.titleblock.get", window, nil); terr == nil && tb.Result != nil {
		if v, ok := tb.Result["showTitleBlock"].(bool); ok {
			showTB = &v
		}
	}

	g := deriveSheetGeometry(sheet, showTB)

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(g)
	}
	renderSheetGeometry(g, stdout)
	return nil
}

// renderSheetGeometry prints a compact human summary.
func renderSheetGeometry(g sheetGeometry, w io.Writer) {
	if g.Sheet.BBox == nil {
		fmt.Fprintln(w, "sheet-geometry: no sheet bbox available")
	} else {
		b := g.Sheet.BBox
		fmt.Fprintf(w, "sheet-geometry: template %q, bbox [%.2f,%.2f → %.2f,%.2f]\n",
			g.Sheet.Template, b.MinX, b.MinY, b.MaxX, b.MaxY)
	}
	vis := "unknown"
	if g.TitleBlock.Visible != nil {
		if *g.TitleBlock.Visible {
			vis = "visible"
		} else {
			vis = "hidden"
		}
	}
	if g.TitleBlock.BBox != nil {
		b := g.TitleBlock.BBox
		fmt.Fprintf(w, "  titleBlock (%s, source=%s): bbox [%.2f,%.2f → %.2f,%.2f]\n",
			vis, g.TitleBlock.Source, b.MinX, b.MinY, b.MaxX, b.MaxY)
	} else {
		fmt.Fprintf(w, "  titleBlock (%s, source=%s): no keep-out\n", vis, g.TitleBlock.Source)
	}
	for _, k := range g.Keepouts {
		hard := "soft"
		if k.Hard {
			hard = "hard"
		}
		fmt.Fprintf(w, "  keepout %q (%s): [%.2f,%.2f → %.2f,%.2f]\n",
			k.Name, hard, k.BBox.MinX, k.BBox.MinY, k.BBox.MaxX, k.BBox.MaxY)
	}
	for _, msg := range g.Warnings {
		fmt.Fprintf(w, "  WARN  %s\n", msg)
	}
}
