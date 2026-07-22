package app

// cmd_sch_zone_plan.go — `sch zone-plan` + `sch zone-draw --mode partition`:
// a DATA-DRIVEN A4 functional-partition planner (issue #149).
//
// The legacy `zone-draw` (zones mode) resolves each claim to a FIXED 3×2 grid
// cell (zoneRect) — it can't express "carve the whole sheet into sensible
// functional regions and leave the bottom-right title block a gap". This planner
// instead derives partition rectangles from the LIVE geometry: usable sheet minus
// a margin, split into columns/rows at the NATURAL gaps between module clusters
// (not fixed fractions), each partition lifted clear of the title-block keep-out
// and reserving a big-font title band. Pure core (planPartitions) → unit-testable
// against the issue's real 6-module A4 page; the draw path goes through the same
// debug.exec_js graphics hatch `zone-draw` uses, persisted per-page (documentUuid).

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

// partitionModule is one functional module: a name + the union bbox of its parts.
type partitionModule struct {
	Name string     `json:"name"`
	BBox layoutBBox `json:"bbox"`
}

// partitionRect is one planned partition: the rectangle, its title band, and the
// modules assigned to it.
type partitionRect struct {
	Modules   []string   `json:"modules"`
	BBox      layoutBBox `json:"bbox"`
	TitleBBox layoutBBox `json:"titleBBox"`
}

// partitionValidation counts every way a plan can be wrong (all should be 0).
type partitionValidation struct {
	SheetOverflow     int `json:"sheetOverflow"`
	PartitionOverlap  int `json:"partitionOverlap"`
	TitleBlockHits    int `json:"titleBlockHits"`
	ModuleOutsideZone int `json:"moduleOutsideZone"`
	LabelCollisions   int `json:"labelCollisions"`
}

func (v partitionValidation) clean() bool {
	return v.SheetOverflow == 0 && v.PartitionOverlap == 0 && v.TitleBlockHits == 0 &&
		v.ModuleOutsideZone == 0 && v.LabelCollisions == 0
}

type partitionPlan struct {
	Sheet      layoutBBox          `json:"sheet"`
	Keepout    *layoutBBox         `json:"keepout,omitempty"`
	Partitions []partitionRect     `json:"partitions"`
	Validation partitionValidation `json:"validation"`
}

type partitionOpts struct {
	Margin    float64
	Gutter    float64
	TitleBand float64
	MaxCols   int
	MaxRows   int
}

func defaultPartitionOpts() partitionOpts {
	return partitionOpts{Margin: 20, Gutter: 12, TitleBand: 30, MaxCols: 3, MaxRows: 2}
}

// planPartitions is the pure planner: usable sheet (minus margin) carved into
// column/row bands at the natural gaps between module clusters, each partition
// lifted above the title-block keep-out and given a top title band. Deterministic.
func planPartitions(sheet layoutBBox, keepout *layoutBBox, modules []partitionModule, opts partitionOpts) partitionPlan {
	plan := partitionPlan{Sheet: sheet, Keepout: keepout}
	usable := layoutBBox{
		MinX: sheet.MinX + opts.Margin, MinY: sheet.MinY + opts.Margin,
		MaxX: sheet.MaxX - opts.Margin, MaxY: sheet.MaxY - opts.Margin,
	}
	if len(modules) == 0 || usable.MaxX <= usable.MinX || usable.MaxY <= usable.MinY {
		return plan
	}

	cx := make([]float64, len(modules))
	cy := make([]float64, len(modules))
	colIvs := make([]axisInterval, len(modules))
	rowIvs := make([]axisInterval, len(modules))
	for i, m := range modules {
		cx[i], cy[i] = bboxCenter(m.BBox)
		colIvs[i] = axisInterval{m.BBox.MinX, m.BBox.MaxX, cx[i]}
		rowIvs[i] = axisInterval{m.BBox.MinY, m.BBox.MaxY, cy[i]}
	}
	// Split at the natural EMPTY BAND between module bboxes (edge-to-edge), not the
	// midpoint of centers — a tall module (主MCU) whose bbox straddles a center-gap
	// split would end up outside its partition (issue #149). Require the gap to hold
	// the gutter so adjacent partitions don't collide.
	colBounds := boundsFrom(usable.MinX, usable.MaxX, clusterSplits(colIvs, opts.Gutter, opts.MaxCols))
	rowBounds := boundsFrom(usable.MinY, usable.MaxY, clusterSplits(rowIvs, opts.Gutter, opts.MaxRows))

	type cellKey struct{ c, r int }
	cells := map[cellKey][]int{}
	var order []cellKey
	for i := range modules {
		k := cellKey{bandIndex(cx[i], colBounds), bandIndex(cy[i], rowBounds)}
		if _, ok := cells[k]; !ok {
			order = append(order, k)
		}
		cells[k] = append(cells[k], i)
	}
	// Deterministic: visual top (large y, y-UP) first, then left→right.
	sort.Slice(order, func(i, j int) bool {
		if order[i].r != order[j].r {
			return order[i].r > order[j].r
		}
		return order[i].c < order[j].c
	})

	half := opts.Gutter / 2
	for _, k := range order {
		rect := layoutBBox{
			MinX: colBounds[k.c] + half, MinY: rowBounds[k.r] + half,
			MaxX: colBounds[k.c+1] - half, MaxY: rowBounds[k.r+1] - half,
		}
		// Lift the bottom above the title-block keep-out (a bottom-right band) so no
		// partition covers the 图签/明细表.
		if keepout != nil && boxesOverlap(rect, *keepout) {
			if lift := keepout.MaxY + half; lift < rect.MaxY {
				rect.MinY = lift
			}
		}
		band := opts.TitleBand
		if h := rect.MaxY - rect.MinY; band > h/2 {
			band = h / 2
		}
		names := make([]string, 0, len(cells[k]))
		for _, i := range cells[k] {
			names = append(names, modules[i].Name)
		}
		sort.Strings(names)
		plan.Partitions = append(plan.Partitions, partitionRect{
			Modules: names,
			BBox:    rect,
			// Title band at the visual TOP (large y).
			TitleBBox: layoutBBox{MinX: rect.MinX, MinY: rect.MaxY - band, MaxX: rect.MaxX, MaxY: rect.MaxY},
		})
	}
	plan.Validation = validatePartitions(plan, modules, keepout)
	return plan
}

// axisInterval is a module's extent on one axis (min, max) plus its center, used
// to place partition splits in the EMPTY BAND between modules rather than through
// a straddling module's body.
type axisInterval struct{ lo, hi, center float64 }

// clusterSplits returns the inner split coordinates (≤ maxK-1 of them) placed at
// the midpoints of the LARGEST empty bands between adjacent module intervals. A
// band smaller than minGap (the gutter) is skipped — there's no room for two
// partitions there — and overlapping intervals (negative band) never split (the
// modules are separable on the OTHER axis instead).
func clusterSplits(ivs []axisInterval, minGap float64, maxK int) []float64 {
	if len(ivs) <= 1 || maxK <= 1 {
		return nil
	}
	s := append([]axisInterval(nil), ivs...)
	sort.Slice(s, func(i, j int) bool { return s[i].center < s[j].center })
	type gap struct{ size, mid float64 }
	var gaps []gap
	for i := 1; i < len(s); i++ {
		band := s[i].lo - s[i-1].hi
		if band < minGap {
			continue
		}
		gaps = append(gaps, gap{band, (s[i].lo + s[i-1].hi) / 2})
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].size != gaps[j].size {
			return gaps[i].size > gaps[j].size
		}
		return gaps[i].mid < gaps[j].mid
	})
	var splits []float64
	for _, g := range gaps {
		if len(splits) >= maxK-1 {
			break
		}
		splits = append(splits, g.mid)
	}
	sort.Float64s(splits)
	return splits
}

func boundsFrom(lo, hi float64, splits []float64) []float64 {
	b := make([]float64, 0, len(splits)+2)
	b = append(b, lo)
	b = append(b, splits...)
	return append(b, hi)
}

// bandIndex returns the band [bounds[i],bounds[i+1]) that v falls into.
func bandIndex(v float64, bounds []float64) int {
	for i := 0; i+1 < len(bounds); i++ {
		if v < bounds[i+1] {
			return i
		}
	}
	return len(bounds) - 2
}

func bboxContains(outer, inner layoutBBox) bool {
	const eps = 0.01
	return inner.MinX >= outer.MinX-eps && inner.MinY >= outer.MinY-eps &&
		inner.MaxX <= outer.MaxX+eps && inner.MaxY <= outer.MaxY+eps
}

func validatePartitions(plan partitionPlan, modules []partitionModule, keepout *layoutBBox) partitionValidation {
	var v partitionValidation
	ps := plan.Partitions
	for _, p := range ps {
		if !bboxContains(plan.Sheet, p.BBox) {
			v.SheetOverflow++
		}
		if keepout != nil && boxesOverlap(p.BBox, *keepout) {
			v.TitleBlockHits++
		}
	}
	for i := 0; i < len(ps); i++ {
		for j := i + 1; j < len(ps); j++ {
			if boxesOverlap(ps[i].BBox, ps[j].BBox) {
				v.PartitionOverlap++
			}
		}
	}
	partOf := map[string]layoutBBox{}
	for _, p := range ps {
		for _, name := range p.Modules {
			partOf[name] = p.BBox
		}
	}
	for _, m := range modules {
		pb, ok := partOf[m.Name]
		if !ok || !bboxContains(pb, m.BBox) {
			v.ModuleOutsideZone++
		}
	}
	// A title band overlapping a module body would put the big title on top of a
	// symbol (label collision).
	for _, p := range ps {
		for _, m := range modules {
			if strInSlice(p.Modules, m.Name) && boxesOverlap(p.TitleBBox, m.BBox) {
				v.LabelCollisions++
			}
		}
	}
	return v
}

func strInSlice(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// modulesFromClaims builds the planner input from `sch zones` claims: each module's
// bbox is the union of its parts' live bboxes. Modules whose parts aren't on the
// active page (no bbox) are skipped.
func modulesFromClaims(zones map[string]*schZoneClaim, comps []layoutComp) []partitionModule {
	byDesig := map[string]layoutComp{}
	for _, c := range comps {
		if c.Designator != "" && c.BBox != nil {
			byDesig[strings.ToUpper(c.Designator)] = c
		}
	}
	var names []string
	for n := range zones {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []partitionModule
	for _, name := range names {
		zc := zones[name]
		if zc == nil {
			continue
		}
		var u *layoutBBox
		for _, d := range zc.Parts {
			c, ok := byDesig[strings.ToUpper(d)]
			if !ok {
				continue
			}
			if u == nil {
				b := *c.BBox
				u = &b
				continue
			}
			u.MinX = minF(u.MinX, c.BBox.MinX)
			u.MinY = minF(u.MinY, c.BBox.MinY)
			u.MaxX = maxF(u.MaxX, c.BBox.MaxX)
			u.MaxY = maxF(u.MaxY, c.BBox.MaxY)
		}
		if u != nil {
			out = append(out, partitionModule{Name: name, BBox: *u})
		}
	}
	return out
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// buildPartitionDrawJS renders the exec_js that draws every partition rect + its
// big-font title, returning their ids. Pure (unit-testable).
func buildPartitionDrawJS(plan partitionPlan, fontSize float64, color string) string {
	var b strings.Builder
	b.WriteString("const rects=[], texts=[];\n")
	colorJS, _ := json.Marshal(color)
	for _, p := range plan.Partitions {
		w := p.BBox.MaxX - p.BBox.MinX
		h := p.BBox.MaxY - p.BBox.MinY
		if w <= 0 || h <= 0 {
			continue
		}
		title, _ := json.Marshal(strings.Join(p.Modules, " / "))
		// sch_PrimitiveRectangle.create anchors at the TOP-LEFT corner (min x, MAX y
		// on the y-UP canvas) and extends toward -y by the height — passing MinY as
		// the anchor drops the whole frame one height down (confirmed by bbox
		// readback). Anchor at MaxY so the document bbox equals the planned rect.
		fmt.Fprintf(&b, "{ const rc = await eda.sch_PrimitiveRectangle.create(%g, %g, %g, %g, 0, 0, %s, null, 1, 1);\n",
			p.BBox.MinX, p.BBox.MaxY, w, h, colorJS)
		b.WriteString("  if (rc) rects.push(rc.getState_PrimitiveId());\n")
		// Title baseline sits fontSize below the band top (larger y = higher on the
		// y-up canvas) so the rendered glyph box stays inside the frame (issue #149:
		// a 22pt title anchored at the very top spilled ~6 units over the edge).
		tx := p.TitleBBox.MinX + 4
		ty := p.TitleBBox.MaxY - fontSize
		fmt.Fprintf(&b, "  const tt = await eda.sch_PrimitiveText.create(%g, %g, %s, 0, %s, null, %g);\n",
			tx, ty, title, colorJS, fontSize)
		b.WriteString("  if (tt) texts.push(tt.getState_PrimitiveId()); }\n")
	}
	b.WriteString("return {rects, texts};")
	return b.String()
}

// currentDocumentUUID returns the active document's uuid (for per-page frame keying).
func currentDocumentUUID(cfg *appConfig, window string) string {
	cur, err := requestAction(cfg, "document.current", window, nil)
	if err != nil || cur.Context == nil {
		return ""
	}
	return cur.Context.DocumentUUID
}

// newSchZonePlanCmd builds `sch zone-plan` — compute + print the partition plan
// (no mutation). --json emits the full plan + validation.
func newSchZonePlanCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var asJSON bool
	var margin, gutter, titleBand float64
	var maxCols, maxRows int
	c := &cobra.Command{
		Use:   "zone-plan",
		Short: "Plan data-driven A4 functional partitions from the live sheet + module bboxes (no mutation)",
		Long: `Compute a whole-sheet functional partition plan (issue #149) from the LIVE
geometry: usable sheet (minus margin) carved into columns/rows at the natural gaps
between module clusters, each partition lifted clear of the title-block keep-out and
given a big-font title band. Reads modules from ` + "`sch zones`" + ` claims (each
module's bbox = union of its parts). Pure计算 — prints the plan + validation
(sheetOverflow / partitionOverlap / titleBlockHits / moduleOutsideZone /
labelCollisions, all should be 0). Draw it with ` + "`sch zone-draw --mode partition`" + `.`,
		Example: `  easyeda sch zones set --spec s0.json --project ceshi
  easyeda sch zone-plan --project ceshi --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, _, err := computePartitionPlan(cfg, *window, partitionOptsFrom(margin, gutter, titleBand, maxCols, maxRows))
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(plan)
			}
			renderPartitionPlan(plan, stdout)
			if !plan.Validation.clean() {
				return fmt.Errorf("zone-plan: validation not clean (%+v)", plan.Validation)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit the full plan + validation as JSON")
	c.Flags().Float64Var(&margin, "margin", 20, "page margin inset from the sheet edge")
	c.Flags().Float64Var(&gutter, "gutter", 12, "gutter between adjacent partitions")
	c.Flags().Float64Var(&titleBand, "title-band", 30, "height of each partition's title band")
	c.Flags().IntVar(&maxCols, "max-cols", 3, "maximum partition columns")
	c.Flags().IntVar(&maxRows, "max-rows", 2, "maximum partition rows")
	return c
}

func partitionOptsFrom(margin, gutter, titleBand float64, maxCols, maxRows int) partitionOpts {
	o := defaultPartitionOpts()
	if margin > 0 {
		o.Margin = margin
	}
	if gutter > 0 {
		o.Gutter = gutter
	}
	if titleBand > 0 {
		o.TitleBand = titleBand
	}
	if maxCols > 0 {
		o.MaxCols = maxCols
	}
	if maxRows > 0 {
		o.MaxRows = maxRows
	}
	return o
}

// computePartitionPlan pulls claims + live geometry and runs the planner.
func computePartitionPlan(cfg *appConfig, window string, opts partitionOpts) (partitionPlan, map[string]*schZoneClaim, error) {
	zones, project, err := loadSchZoneClaims(cfg, window)
	if err != nil {
		return partitionPlan{}, nil, err
	}
	if len(zones) == 0 {
		return partitionPlan{}, nil, fmt.Errorf("no schematic zone claims for %q — run `sch zones set --spec <s0-spec.json>` first", project)
	}
	res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{"includeBBox": true})
	if err != nil {
		return partitionPlan{}, nil, err
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		return partitionPlan{}, nil, perr
	}
	sheet := sheetBBoxOf(comps)
	if sheet == nil {
		return partitionPlan{}, nil, fmt.Errorf("no sheet bbox on the active page — `easyeda doc switch` to the schematic page first")
	}
	keepout, _ := titleBlockKeepout(sheet)
	modules := modulesFromClaims(zones, comps)
	if len(modules) == 0 {
		return partitionPlan{}, nil, fmt.Errorf("no module bboxes resolved — the claimed parts aren't on this page (place them / `doc switch`)")
	}
	return planPartitions(*sheet, keepout, modules, opts), zones, nil
}

func renderPartitionPlan(plan partitionPlan, w io.Writer) {
	fmt.Fprintf(w, "zone-plan: %d partition(s) on sheet (%.0f,%.0f)..(%.0f,%.0f)\n",
		len(plan.Partitions), plan.Sheet.MinX, plan.Sheet.MinY, plan.Sheet.MaxX, plan.Sheet.MaxY)
	for _, p := range plan.Partitions {
		fmt.Fprintf(w, "  [%s]  (%.0f,%.0f)..(%.0f,%.0f)\n",
			strings.Join(p.Modules, " / "), p.BBox.MinX, p.BBox.MinY, p.BBox.MaxX, p.BBox.MaxY)
	}
	v := plan.Validation
	fmt.Fprintf(w, "validation: sheetOverflow=%d partitionOverlap=%d titleBlockHits=%d moduleOutsideZone=%d labelCollisions=%d\n",
		v.SheetOverflow, v.PartitionOverlap, v.TitleBlockHits, v.ModuleOutsideZone, v.LabelCollisions)
	if v.clean() {
		fmt.Fprintln(w, "✓ plan is clean")
	} else {
		fmt.Fprintln(w, "✗ plan has violations — adjust margins/gutter or the zone claims")
	}
}

// runPartitionDraw draws (or clears) the partition frames, persisted per-page.
func runPartitionDraw(cfg *appConfig, window string, opts partitionOpts, fontSize float64, color string, clear bool, stdout, stderr io.Writer) error {
	project, err := resolveStageProject(cfg, window)
	if err != nil {
		return err
	}
	st, err := loadPcbStageState(project)
	if err != nil {
		return err
	}
	docUUID := currentDocumentUUID(cfg, window)
	if st.SchZoneFrameIdsByPage == nil {
		st.SchZoneFrameIdsByPage = map[string]*workflow.SchZoneFrames{}
	}
	// Clear this page's previous partition frames first (for --clear and redraw).
	if prev := st.SchZoneFrameIdsByPage[docUUID]; prev != nil && (len(prev.Rects) > 0 || len(prev.Texts) > 0) {
		if v, derr := execZoneJS(cfg, window, buildZoneClearJS(prev)); derr != nil {
			fmt.Fprintf(stderr, "warn: clearing previous partition frames failed (%v)\n", derr)
		} else {
			fmt.Fprintf(stderr, "cleared %v previous partition frame primitive(s)\n", v["deleted"])
		}
		delete(st.SchZoneFrameIdsByPage, docUUID)
		if err := savePcbStageState(st); err != nil {
			return err
		}
	} else if clear {
		fmt.Fprintln(stdout, "no partition frames recorded for this page — nothing to clear")
		return nil
	}
	if clear {
		fmt.Fprintln(stdout, "partition frames cleared for this page")
		return nil
	}

	plan, _, err := computePartitionPlan(cfg, window, opts)
	if err != nil {
		return err
	}
	if !plan.Validation.clean() {
		fmt.Fprintf(stderr, "warn: partition plan has violations %+v — drawing anyway; fix claims/margins\n", plan.Validation)
	}
	v, err := execZoneJS(cfg, window, buildPartitionDrawJS(plan, fontSize, color))
	if err != nil {
		return err
	}
	frames := &workflow.SchZoneFrames{Rects: asStringSlice(v["rects"]), Texts: asStringSlice(v["texts"]), At: time.Now().Format(time.RFC3339)}
	st.SchZoneFrameIdsByPage[docUUID] = frames
	if err := savePcbStageState(st); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "drew %d partition frame(s) + %d title(s) — annotation only; `sch zone-draw --mode partition --clear` removes them (this page)\n",
		len(frames.Rects), len(frames.Texts))
	return nil
}
