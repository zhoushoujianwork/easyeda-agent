package app

import (
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
// footprint. A-series sheets all share the √2 aspect. NOTE: the real title block
// is a FIXED-SIZE table, NOT a constant fraction of the page — so the ratio below
// is calibrated for A4 and OVER-estimates on larger A3+ sheets (deriveSheetGeometry
// warns + downgrades provenance there; A4 is supported first, see isA4LandscapeSize).
type sheetTemplate struct {
	Name       string
	Aspect     float64 // sheet width / height
	AspectTol  float64 // match tolerance on aspect
	TitleBlock titleBlockRatio
}

// defaultTitleBlockRatio is the bottom-right title-block table's footprint as a
// fraction of an A-series LANDSCAPE sheet (also the generic fallback). Calibrated
// against the real 立创EDA 3.2.148 A4 title block by overlay-measuring the rendered
// table on ceshi (2026-07-22): the earlier 0.22×0.14 covered only the RIGHT date
// columns, leaving the 原理图/Schematic1/Board1/ceshi left half UNPROTECTED — so
// autoconnect markers (#147) and partition frames (#149) could land on it while
// every keep-out check read "clear". The real table spans ~60% of the width.
// HeightFrac re-calibrated 0.2 → 0.24 (2026-08-11): at 0.2 the keep-out top sat
// at y=165 on A4 (825 high) while the RENDERED table top measures ≈ y≈190 — a
// partition frame lifted to 165+6 visibly crossed the 原理图/Schematic1 row while
// zone-plan validation still read titleBlockHits=0 (false green). 0.24 → 198
// keeps the whole table covered with the deliberate over-estimate bias this
// table documents above.
var defaultTitleBlockRatio = titleBlockRatio{WidthFrac: 0.6, HeightFrac: 0.24}

// a4SheetW/H is the A4 landscape sheet size (EasyEDA schematic units, overlay-
// measured on 3.2.148) that defaultTitleBlockRatio was calibrated against.
const (
	a4SheetW = 1170.0
	a4SheetH = 825.0
)

// titleBlockFixedW/H is the real title block's FIXED table size, derived from the
// A4 calibration (ratio × A4 sheet ≈ 702×198). Issue #172: on a non-A4 sheet the
// table does NOT scale with the page, so the honest estimate is this fixed size
// anchored to the bottom-right corner — scaling the A4 fraction up with an A3
// sheet over-reserved ~60% of the width and false-flagged mid-sheet parts.
var (
	titleBlockFixedW = defaultTitleBlockRatio.WidthFrac * a4SheetW
	titleBlockFixedH = defaultTitleBlockRatio.HeightFrac * a4SheetH
)

// sheetTemplates is the known sheet → title-block ratio table. Mirrored for
// humans/skills in .agents/skills/easyeda-agent/references/sheet-templates.json;
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
	Template  string      `json:"template,omitempty"`
	BBox      *layoutBBox `json:"bbox"`
	InnerBBox *layoutBBox `json:"innerBBox,omitempty"`
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
	SourceSha256     string         `json:"sourceSha256,omitempty"`
	CoordinateSystem string         `json:"coordinateSystem,omitempty"`
	Sheet            sheetInfo      `json:"sheet"`
	TitleBlock       titleBlockInfo `json:"titleBlock"`
	Keepouts         []keepout      `json:"keepouts"`
	Warnings         []string       `json:"warnings"`
}

// isA4LandscapeSize reports whether a landscape sheet is A4-sized — the size the
// title-block ratio was calibrated against (~1170×825 in EasyEDA schematic units,
// overlay-measured on 3.2.148). A ±20% band absorbs version/template variation
// without reaching A3 (×√2 ≈ 1654 wide) or A5 (÷√2 ≈ 827 wide), which share A4's
// aspect but carry the same fixed-size title block at a different fraction.
func isA4LandscapeSize(w, h float64) bool {
	const tol = 0.2
	return math.Abs(w-a4SheetW) <= a4SheetW*tol && math.Abs(h-a4SheetH) <= a4SheetH*tol
}

// titleBlockKeepoutWithSource derives the title-block keep-out plus its
// provenance (sheetSourceKnownTemplate / sheetSourceFallback / sheetSourceNone),
// so consumers (the titleblock-overlap check rule, issue #172) can grade how much
// to trust a hit: a fallback/estimated keep-out is advisory, not a hard gate.
func titleBlockKeepoutWithSource(sheet *layoutBBox) (*layoutBBox, string) {
	if sheet == nil {
		return nil, sheetSourceNone
	}
	g := deriveSheetGeometry(sheet, nil)
	if g.TitleBlock.BBox == nil {
		return nil, sheetSourceNone
	}
	return g.TitleBlock.BBox, g.TitleBlock.Source
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
// y-UP bbox space, so the carved rect is the high-x, LOW-y corner of the sheet.
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

	// fixedSize: derive the keep-out from the FIXED A4-calibrated table size
	// (titleBlockFixedW/H) anchored bottom-right, instead of scaling the A4
	// fraction with the sheet. Issue #172: the real title block is a fixed-size
	// table, so on a 1655×1170 sheet the ratio form reserved ~60% of the width
	// (left edge x=662) and titleblock-overlap false-flagged parts mid-sheet.
	fixedSize := false

	tmpl, matched := matchSheetTemplate(w, h)
	g.Sheet.Template = tmpl.Name
	if matched {
		g.TitleBlock.Source = sheetSourceKnownTemplate
	} else {
		g.TitleBlock.Source = sheetSourceFallback
		fixedSize = true
		g.Warnings = append(g.Warnings, fmt.Sprintf(
			"sheet aspect %.3f did not match a known template; title-block keep-out is an ESTIMATE — the fixed-size A4-calibrated table anchored bottom-right, not template geometry",
			w/h))
	}

	// A4 first: the landscape title-block ratio is calibrated ONLY for A4. The real
	// title block is a fixed-size table, so on any other A-series landscape size the
	// honest estimate is that same fixed size anchored bottom-right (the ratio form
	// would over-reserve on A3+ and under-reserve on A5). Provenance still downgrades
	// + warns, so a non-A4 keep-out is never silently trusted as calibrated truth.
	if matched && tmpl.Name == "a-series-landscape" && !isA4LandscapeSize(w, h) {
		g.TitleBlock.Source = sheetSourceFallback
		fixedSize = true
		g.Warnings = append(g.Warnings, fmt.Sprintf(
			"title-block keep-out is calibrated for A4 landscape only; this sheet (%.0f×%.0f) is a different A-series size, so the keep-out is an ESTIMATE (the fixed-size A4 table anchored bottom-right) — verify manually before trusting it as a hard gate",
			w, h))
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

	// The canvas is y-UP (proven live 2026-07-19: probe texts at y=100/700 on
	// ceshi render bottom/top respectively), so the visual bottom-right corner
	// the title block occupies is the MaxX/MIN-Y corner. The previous MaxY form
	// protected the visual TOP-right — a keep-out on the wrong corner.
	tbW, tbH := tmpl.TitleBlock.WidthFrac*w, tmpl.TitleBlock.HeightFrac*h
	if fixedSize {
		// Fixed-size estimate, clamped so a sheet smaller than the table never
		// yields a keep-out poking past the sheet's own bounds.
		tbW, tbH = math.Min(titleBlockFixedW, w), math.Min(titleBlockFixedH, h)
	}
	tb := &layoutBBox{
		MinX: round2(sheet.MaxX - tbW),
		MinY: sheet.MinY,
		MaxX: sheet.MaxX,
		MaxY: round2(sheet.MinY + tbH),
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
		// Wrap in the same {id,type,version,ok,result} envelope the rest of the
		// sch family emits (#66). Envelope metadata comes from the primary
		// components.list response (res).
		return encodeResultEnvelope(res, g, stdout)
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
	if b := g.Sheet.InnerBBox; b != nil {
		fmt.Fprintf(w, "  inner border: [%.2f,%.2f → %.2f,%.2f] (raw y-UP, vector centerlines)\n", b.MinX, b.MinY, b.MaxX, b.MaxY)
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
