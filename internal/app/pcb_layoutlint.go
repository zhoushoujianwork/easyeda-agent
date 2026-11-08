package app

// pcb layout-lint — PCB placement quality + ROUTABILITY prediction.
//
// `sch layout-lint` catches component overlap on the schematic; this is its PCB
// sibling, plus the thing that actually predicts routing pain BEFORE you route:
// the ratsnest. It computes, over signal nets only (power/GND are poured, not
// routed as tracks — so they'd swamp the metric), a per-net minimum spanning tree
// and counts how many cross-net ratline segments GEOMETRICALLY CROSS. Crossings are
// the classic single-layer routability killer — two nets whose shortest links cross
// can't both stay on one layer without a via/detour. Combined with overlap (fatal)
// and outside-outline, that yields a 0-100 score for comparing placements.
//
// Pure core here (unit-testable, no I/O); the CLI command + live fetch/render is in
// cmd_pcb.go / the runner below. Reuses overlapExtent/rectGap/round2 from
// cmd_sch_layout.go and isGlobalNet from pcb_autoplace.go.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
)

// pcbLPad is a placed pad with its net and center (mil). Layer is the pad's
// copper layer id (1=top, 2=bottom, 12=multi ⇒ a through-hole barrel that
// conducts on EVERY layer); W/H are the real copper extent when the connector
// could derive it from the pad shape (0 = unknown, e.g. a polygon pad).
type pcbLPad struct {
	ID                             string
	Designator                     string
	Number                         string
	Net                            string
	Layer                          int
	X, Y                           float64
	W, H                           float64
	Rotation                       float64
	Shape, SpecialPad              any
	Availability                   map[string]string
	CaptureErrors                  map[string]any
	GeometryStatus, GeometryReason string
}

// pcbLComp is a placed footprint's identity + rendered extent. Layer is the
// board SIDE the footprint is assembled on (1=top, 2=bottom, 0=unknown) — the
// axis overlap is judged along, because a top part and a bottom part sharing an
// XY is a legal top/bottom pass-through, not a collision.
type pcbLComp struct {
	ID            string
	Designator    string
	Layer         int
	BBox          *layoutBBox
	Availability  map[string]string
	CaptureErrors map[string]any
	PadFailures   []map[string]any
}

// sameAssemblySide reports whether two footprints sit on the same physical side
// of the board. Layer 0 means "the API did not report a side" (older connector);
// an unknown side compares against BOTH sides so a missing field can never
// silently suppress a real same-side overlap.
func sameAssemblySide(a, b int) bool { return a == 0 || b == 0 || a == b }

// sideName renders a component layer id for human/JSON output.
func sideName(layer int) string {
	switch layer {
	case pcbSideTop:
		return "top"
	case pcbSideBottom:
		return "bottom"
	case 0:
		return "unknown"
	default:
		return fmt.Sprintf("layer-%d", layer)
	}
}

// ratLink is one ratsnest (unrouted) link between two same-net pads.
type ratLink struct {
	Net    string
	Ax, Ay float64
	Bx, By float64
	Len    float64
}

// pcbLFinding is one mechanical placement issue. Side names the board side the
// pair was judged on ("top"/"bottom"/"unknown") — overlap and spacing are
// per-side, so the side is part of the finding's meaning.
type pcbLFinding struct {
	Type string  `json:"type"` // "overlap" | "outside-outline" | "spacing"
	A    string  `json:"a"`
	B    string  `json:"b,omitempty"`
	Side string  `json:"side,omitempty"`
	OvX  float64 `json:"overlapX,omitempty"`
	OvY  float64 `json:"overlapY,omitempty"`
	Gap  float64 `json:"gap,omitempty"`
}

// pcbLShort is copper from two DIFFERENT nets physically overlapping: two pads
// whose real copper rects intersect on a shared layer. That is not "too close",
// it is a short — the qualitative jump KiCad makes and we used to miss
// (docs/ecosystem-survey.md §9.2).
type pcbLShort struct {
	A     string  `json:"a"` // "U1.3"
	NetA  string  `json:"netA"`
	B     string  `json:"b"` // "C2.1"
	NetB  string  `json:"netB"`
	Layer string  `json:"layer"` // "top" | "bottom" | "multi" | …
	OvX   float64 `json:"overlapX"`
	OvY   float64 `json:"overlapY"`
}

// crossFinding is a cross-net ratline crossing (a routability hotspot).
type crossFinding struct {
	NetA string  `json:"netA"`
	NetB string  `json:"netB"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

// pcbLAccessFinding is a component boxed in on ALL four sides below the
// hand-solder iron-access corridor (issue #99): there is no direction to bring
// an iron tip / solder / rework tools in from.
type pcbLAccessFinding struct {
	Designator string             `json:"designator"`
	BestGap    float64            `json:"bestGapMil"` // widest of the four side gaps
	Sides      map[string]float64 `json:"sides"`      // left/right/top/bottom → gap to nearest blocker (mil)
}

// pcbLayoutReport is the full normalized result.
type pcbLayoutReport struct {
	OK             bool                `json:"ok"`
	Incomplete     bool                `json:"incomplete"`
	Coverage       pcbGeometryCoverage `json:"coverage"`
	Score          int                 `json:"score"`   // 0-100 routability
	Verdict        string              `json:"verdict"` // easy | moderate | hard | very-hard | overlap | short
	ComponentCount int                 `json:"componentCount"`
	MinGapMil      float64             `json:"minGapMil"`
	Overlaps       []pcbLFinding       `json:"overlaps"`
	OutsideOutline []pcbLFinding       `json:"outsideOutline"`
	TightPairs     []pcbLFinding       `json:"tightSpacing"`
	// Silk is pcb check's own silk rules (silk-over-pad, silk-off-board) run on
	// the same capture: designator placement is part of layout, and lint used to
	// report 0 issues while labels sat on neighbours' pads or off the board
	// (live 2026-10-13). WARN-level; never changes score or exit code.
	Silk []pcbCheckFinding `json:"silk,omitempty"`
	// SilkUnchecked names why the silk rules could not run (empty = they ran).
	SilkUnchecked string `json:"silkUnchecked,omitempty"`
	// AltFitStacks 是同网集堆叠对：两件焊盘数相同、非空网名多重集完全一致且
	// 本体相交/贴住 —— 官方板的装配选项惯例（同位放两个 fit 选项只焊一个）或
	// 刻意并联。同网集意味着**不可能**造成跨网短路，装配上也是有意为之，所以
	// 单列 INFO 而不是 overlap/tight（五块嘉立创开源板校准实锤:MIPI 的
	// R1↔R3/R2↔R4、RK3568 的并联电容对全是这类,把好板压成 [blocked]）。
	// 盘级跨网接触仍由 Shorts 兜底(同网集不代表逐盘对齐,交叉贴装照样抓)。
	AltFitStacks []pcbLFinding `json:"altFitStacks,omitempty"`
	// UnderShellPairs 是「连接器壳下垫件」对：一方位号是连接器/卡座
	// (J/CN/CON/USB/CARD/SIM/SD/TF/DC),双方**焊盘互不接触**,只有焊盘并集
	// (含壳体投影内的空腔)相交 —— 卡座/USB 壳体抬高、下方腔体里放小被动件是
	// 专业惯例(五板校准实锤:K230 的 CARD1↔R57/L6、实战派S3 的 C13↔J1)。
	// 单列 INFO(需人工核对壳下净高),不算 blocking。焊盘真接触仍是 overlap。
	UnderShellPairs []pcbLFinding `json:"underShellPairs,omitempty"`
	// A possible shell overlap with unknown pad geometry remains an explicit
	// unresolved diagnostic. It is never certified as safe under-shell placement.
	UnverifiedOverlaps []pcbLFinding `json:"unverifiedOverlaps,omitempty"`
	// Shorts is cross-net copper contact (pad↔pad), a strictly worse finding
	// than a geometric overlap: the board is electrically wrong, not just tight.
	Shorts []pcbLShort `json:"shorts,omitempty"`
	// Sides counts components per board side — the evidence that overlap was
	// judged PER SIDE (a double-sided board shows both entries).
	Sides map[string]int `json:"sides,omitempty"`
	// AccessMil / AccessBlocked: hand-solder iron-access diagnostic (issue #99),
	// populated when compatibility --gate finds a hand-solder assembly profile.
	AccessMil     float64             `json:"accessMil,omitempty"`
	AccessBlocked []pcbLAccessFinding `json:"accessBlocked,omitempty"`

	SignalNets     int            `json:"signalNets"`
	RatsnestLenMil float64        `json:"ratsnestLenMil"`
	CrossingCount  int            `json:"crossingCount"`
	Crossings      []crossFinding `json:"crossings,omitempty"`
	Summary        string         `json:"summary"`
}

// analyzeSolderAccess flags components with NO accessible side: for each of the
// four bbox directions the corridor (the component's own side span, extended
// outward) must stay clear of other components for at least accessMil before
// it counts as an iron-entry direction. One clear side is enough — a decap may
// sit tight against its IC as long as its other flank stays workable, which is
// exactly the issue-#99 rule ("去耦可贴近,但至少保留一侧可操作"). The board
// edge never blocks (open air is reachable). Pad-size-aware "large pad"
// classification needs pad width/height the connector does not expose yet, so
// v1 applies the corridor rule to every component uniformly.
//
// Blockers are SAME-SIDE only: the iron comes in from the side the part is
// assembled on, so a part on the opposite side of the board never obstructs it.
func analyzeSolderAccess(comps []pcbLComp, accessMil float64) []pcbLAccessFinding {
	withBBox := make([]pcbLComp, 0, len(comps))
	for _, c := range comps {
		if c.BBox != nil {
			withBBox = append(withBBox, c)
		}
	}
	// A gap this large means "no blocker in that direction" (open to the edge).
	const openGap = 1e9
	var out []pcbLAccessFinding
	for i, c := range withBBox {
		a := *c.BBox
		sides := map[string]float64{"left": openGap, "right": openGap, "top": openGap, "bottom": openGap}
		for j, o := range withBBox {
			if i == j || !sameAssemblySide(c.Layer, o.Layer) {
				continue
			}
			b := *o.BBox
			overlapY := b.MinY < a.MaxY && b.MaxY > a.MinY
			overlapX := b.MinX < a.MaxX && b.MaxX > a.MinX
			if overlapY {
				if b.MinX >= a.MaxX { // blocker to the right
					sides["right"] = math.Min(sides["right"], b.MinX-a.MaxX)
				} else if b.MaxX <= a.MinX { // blocker to the left
					sides["left"] = math.Min(sides["left"], a.MinX-b.MaxX)
				}
			}
			if overlapX {
				if b.MinY >= a.MaxY { // blocker above (y-up)
					sides["top"] = math.Min(sides["top"], b.MinY-a.MaxY)
				} else if b.MaxY <= a.MinY { // blocker below
					sides["bottom"] = math.Min(sides["bottom"], a.MinY-b.MaxY)
				}
			}
			// Overlapping bboxes are reported by the overlap check; for access
			// purposes an overlapped side is simply gap 0 on both axes it blocks.
			if overlapX && overlapY {
				for k := range sides {
					sides[k] = 0
				}
				break
			}
		}
		best := 0.0
		for _, g := range sides {
			best = math.Max(best, g)
		}
		if best < accessMil {
			rounded := map[string]float64{}
			for k, g := range sides {
				rounded[k] = round2(g)
			}
			out = append(out, pcbLAccessFinding{Designator: c.Designator, BestGap: round2(best), Sides: rounded})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Designator < out[j].Designator })
	return out
}

func (p pcbLPad) boardPad() boardPad {
	return boardPad{ID: p.ID, Number: p.Number, Net: p.Net, Layer: p.Layer, X: p.X, Y: p.Y, W: p.W, H: p.H,
		Rotation: p.Rotation, Shape: p.Shape, SpecialPad: p.SpecialPad,
		Availability: p.Availability, CaptureErrors: p.CaptureErrors,
		GeometryStatus: p.GeometryStatus, GeometryReason: p.GeometryReason}
}

func (p pcbLPad) positionKnown() bool {
	return allFinite(p.X, p.Y, p.Rotation) && !(p.GeometryStatus == "unknown" && p.GeometryReason == "pad pose x/y/rotation is missing or malformed")
}

// Contact requires native source geometry supported by the common exact kernel.
// A legacy W/H envelope can describe placement, but cannot prove copper contact.
func pcbLayoutPadGeometry(p boardPad, ref string) (pcbPadP, string, string) {
	native, status, reason := pcbMeasuredPad(p, ref)
	if status == "available" {
		center := [2]float64{native.X, native.Y}
		if _, err := pcbExactPadSegmentGap(native, center, center); err != nil {
			return native, "unsupported", err.Error()
		}
	}
	return native, status, reason
}

func layoutPadsMeasured(ps []pcbLPad) bool {
	if len(ps) == 0 {
		return false
	}
	for _, p := range ps {
		if _, status, _ := pcbLayoutPadGeometry(p.boardPad(), p.Designator); status != "available" {
			return false
		}
	}
	return true
}

func pcbLayoutCoverage(comps []pcbLComp, pads []pcbLPad) pcbGeometryCoverage {
	s := &boardSnapshot{}
	if comps != nil {
		s.Components = make([]boardComp, 0, len(comps))
	}
	for _, c := range comps {
		component := boardComp{ID: c.ID, Designator: c.Designator, BBox: c.BBox,
			Availability: c.Availability, CaptureErrors: c.CaptureErrors, PadFailures: c.PadFailures}
		if c.Availability["pads"] == "available" {
			component.Pads = []boardPad{}
		}
		for _, p := range pads {
			if p.Designator == c.Designator {
				component.Pads = append(component.Pads, p.boardPad())
			}
		}
		s.Components = append(s.Components, component)
	}
	return s.geometryCoverage(true)
}

// connectorishDes 判定位号是不是连接器/卡座类 —— UnderShellPairs 的"壳"方判据。
// 只看位号前缀(数据里最稳的信号,器件名常是未解析模板)。
var connectorishDesRe = regexp.MustCompile(`(?i)^(?:J|CN|CON|USB|CARD|SIM|SD|TF|DC|FPC)[\d_]`)

func connectorishDes(des string) bool {
	return connectorishDesRe.MatchString(strings.TrimSpace(des))
}

// anyPadPairTouch 判两件是否存在任意一对焊盘铜相交（不看网名——这里判的是
// 物理接触，不是短路）。与 padShorts 同用原生形状与精确几何内核；未知几何
// 不产生接触结论，调用方必须另查 layoutPadsMeasured，不能以 false 证明无铜。
func anyPadPairTouch(as, bs []pcbLPad) bool {
	for _, pa := range as {
		for _, pb := range bs {
			if !padsShareCopperLayer(pa, pb) {
				continue
			}
			if _, _, contact := pcbLayoutPadContact(pa, pb); contact {
				return true
			}
		}
	}
	return false
}

// sameNetSetStack 判定两件是否「同网集堆叠/贴邻」：焊盘数相同、双方全部焊盘都
// 有网名、且网名多重集完全一致。这是装配选项(fit-option)与刻意并联的网表指纹
// —— 见 AltFitStacks 字段注释。任一侧有无网焊盘(机械件/散热盘)不豁免。
func sameNetSetStack(as, bs []pcbLPad) bool {
	if len(as) == 0 || len(as) != len(bs) {
		return false
	}
	na := map[string]int{}
	for _, p := range as {
		n := strings.TrimSpace(p.Net)
		if n == "" {
			return false
		}
		na[n]++
	}
	for _, p := range bs {
		n := strings.TrimSpace(p.Net)
		if n == "" {
			return false
		}
		na[n]--
	}
	for _, v := range na {
		if v != 0 {
			return false
		}
	}
	return true
}

func padCopperRect(p pcbLPad) (layoutBBox, bool) {
	if !p.positionKnown() {
		return layoutBBox{}, false
	}
	w, h := p.W, p.H
	if native, status, _ := pcbLayoutPadGeometry(p.boardPad(), p.Designator); status == "available" {
		// Compute the shape's enclosing box for the broad phase. Contact below
		// still uses the common exact kernel rather than this envelope.
		angle := native.Rotation * math.Pi / 180
		c, s := math.Abs(math.Cos(angle)), math.Abs(math.Sin(angle))
		switch native.Shape {
		case "RECT":
			r := native.ShapeRound
			w, h = (native.ShapeW-2*r)*c+(native.ShapeH-2*r)*s+2*r, (native.ShapeW-2*r)*s+(native.ShapeH-2*r)*c+2*r
		case "OVAL":
			r := math.Min(native.ShapeW, native.ShapeH) / 2
			w, h = (native.ShapeW-2*r)*c+(native.ShapeH-2*r)*s+2*r, (native.ShapeW-2*r)*s+(native.ShapeH-2*r)*c+2*r
		case "ELLIPSE":
			w, h = native.ShapeW, native.ShapeH // only circles are supported here
		}
	}
	if !allFinite(p.X, p.Y, w, h) || w <= 0 || h <= 0 {
		return layoutBBox{}, false
	}
	return layoutBBox{
		MinX: p.X - w/2, MaxX: p.X + w/2,
		MinY: p.Y - h/2, MaxY: p.Y + h/2,
	}, true
}

func pcbLayoutPadContact(a, b pcbLPad) (float64, float64, bool) {
	pa, sa, _ := pcbLayoutPadGeometry(a.boardPad(), a.Designator)
	pb, sb, _ := pcbLayoutPadGeometry(b.boardPad(), b.Designator)
	if sa != "available" || sb != "available" {
		return 0, 0, false
	}
	ra, oka := padCopperRect(a)
	rb, okb := padCopperRect(b)
	if !oka || !okb {
		return 0, 0, false
	}
	ox := math.Min(ra.MaxX, rb.MaxX) - math.Max(ra.MinX, rb.MinX)
	oy := math.Min(ra.MaxY, rb.MaxY) - math.Max(ra.MinY, rb.MinY)
	if ox < -netPathGeomEps || oy < -netPathGeomEps {
		return 0, 0, false
	}
	gap, err := pcbReflowPadGap(pa, pb)
	return math.Max(0, ox), math.Max(0, oy), err == nil && gap <= netPathGeomEps
}

// padsShareCopperLayer reports whether two pads have copper on a common layer.
// A multi-layer pad (12) is a plated through-hole barrel — it conducts on every
// layer, so it shares with anything. Layer 0 = unknown → assume shared (a
// missing field must not hide a short).
func padsShareCopperLayer(a, b pcbLPad) bool {
	if a.Layer == 0 || b.Layer == 0 || a.Layer == pcbLayerMulti || b.Layer == pcbLayerMulti {
		return true
	}
	return a.Layer == b.Layer
}

// padShortLayer names the layer a contact happens on for the finding.
func padShortLayer(a, b pcbLPad) string {
	if a.Layer == pcbLayerMulti || b.Layer == pcbLayerMulti {
		return "multi"
	}
	if a.Layer != 0 {
		return sideName(a.Layer)
	}
	return sideName(b.Layer)
}

// padShorts finds cross-net copper contact between two footprints' pads: pads
// on a shared layer, from two DIFFERENT named nets, whose copper rects
// intersect. Unnamed pads (no net) are skipped — an unconnected pad touching
// something is a footprint/placement problem the overlap finding already
// covers, and treating "" as a net would short every mounting hole together.
func padShorts(as, bs []pcbLPad) []pcbLShort {
	var out []pcbLShort
	for _, pa := range as {
		if pa.Net == "" {
			continue
		}
		if _, status, _ := pcbLayoutPadGeometry(pa.boardPad(), pa.Designator); status != "available" {
			continue
		}
		for _, pb := range bs {
			if pb.Net == "" || pb.Net == pa.Net || !padsShareCopperLayer(pa, pb) {
				continue
			}
			ox, oy, contact := pcbLayoutPadContact(pa, pb)
			if !contact {
				continue
			}
			out = append(out, pcbLShort{
				A: padLabel(pa), NetA: pa.Net,
				B: padLabel(pb), NetB: pb.Net,
				Layer: padShortLayer(pa, pb),
				OvX:   round2(ox), OvY: round2(oy),
			})
		}
	}
	return out
}

// padLabel renders "U1.3" (designator.padNumber), falling back to the bare
// designator when the pad number is unknown.
func padLabel(p pcbLPad) string {
	if p.Number == "" {
		return p.Designator
	}
	return p.Designator + "." + p.Number
}

// analyzePcbLayout is the pure core. minGapMil flags too-tight pairs; outline (may
// be nil) drives the outside-outline check.
func analyzePcbLayout(comps []pcbLComp, pads []pcbLPad, outline *layoutBBox, minGapMil float64) pcbLayoutReport {
	rep := pcbLayoutReport{MinGapMil: minGapMil, ComponentCount: len(comps)}
	rep.Coverage = pcbLayoutCoverage(comps, pads)
	rep.Incomplete = !rep.Coverage.Complete

	withBBox := make([]pcbLComp, 0, len(comps))
	for _, c := range comps {
		if c.BBox != nil {
			withBBox = append(withBBox, c)
		}
	}
	rep.Sides = map[string]int{}
	for _, c := range comps {
		rep.Sides[sideName(c.Layer)]++
	}

	padsByRef := map[string][]pcbLPad{}
	for _, p := range pads {
		padsByRef[p.Designator] = append(padsByRef[p.Designator], p)
	}
	// Copper contact is independent of rendered bbox availability and assembly
	// face. A missing bbox must not suppress a known pad short.
	refs := make([]string, 0, len(padsByRef))
	for ref := range padsByRef {
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	for i, a := range refs {
		for _, b := range refs[i+1:] {
			rep.Shorts = append(rep.Shorts, padShorts(padsByRef[a], padsByRef[b])...)
		}
	}

	// 本体代理（courtyard proxy）：重叠/间距判定用**焊盘并集**，退化才用渲染 bbox。
	//
	// 渲染 bbox 含丝印和位号文字，实测比本体大 40%+ —— 拿它当 courtyard 在专业
	// 密板上是误报风暴：五块公认好板校准（2026-08-10），实战派S3 被报 104 处
	// "component-overlap"（C83↔C84 1.9×83mil 这种丝印级贴边）、RK3568 报 143 处、
	// 三块板 clearance 维直接归零 —— 好板掉分优先怀疑度量，这就是那个度量错误。
	// 焊盘并集是从下方逼近本体（引脚式封装的盘略伸出本体、BGA/QFN 略缩），
	// 判「装配冲突」宁可错放不错杀 —— 真正的铜接触另有 padShorts 兜底，真正的
	// 本体大幅相撞焊盘并集同样会相交。
	bodyOf := func(c pcbLComp) layoutBBox {
		ps := padsByRef[c.Designator]
		if len(ps) == 0 {
			return *c.BBox
		}
		bb := layoutBBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
		any := false
		for _, p := range ps {
			r, ok := padCopperRect(p)
			if !ok {
				continue
			}
			any = true
			bb.MinX = math.Min(bb.MinX, r.MinX)
			bb.MinY = math.Min(bb.MinY, r.MinY)
			bb.MaxX = math.Max(bb.MaxX, r.MaxX)
			bb.MaxY = math.Max(bb.MaxY, r.MaxY)
		}
		if !any {
			return *c.BBox
		}
		return bb
	}
	bodyByRef := make(map[string]layoutBBox, len(withBBox))
	for _, c := range withBBox {
		bodyByRef[c.Designator] = bodyOf(c)
	}

	// 1. Overlap + tight spacing (pairwise) — LAYER-AWARE. Bodies only collide
	//    when they are assembled on the SAME side; a top part and a bottom part
	//    sharing an XY is an ordinary top/bottom pass-through and used to be the
	//    dominant false positive on double-sided boards (box-v2 rev-a: 100+
	//    "overlaps", real same-side count 0 — docs/ecosystem-survey.md §9.3).
	//    KiCad gets this for free by comparing per-side courtyards (F.CrtYd /
	//    B.CrtYd); we group by the footprint's layer instead.
	//
	//    The SHORT check deliberately does NOT take the same-side shortcut: a
	//    through-hole barrel (pad layer 12 = multi) conducts on every layer, so
	//    it can genuinely short against a pad on the opposite side. Copper
	//    contact is judged per PAD layer, not per assembly side.
	for i := 0; i < len(withBBox); i++ {
		for j := i + 1; j < len(withBBox); j++ {
			a, b := withBBox[i], withBBox[j]
			la, lb := a.Designator, b.Designator
			if lb < la {
				la, lb = lb, la
			}
			// The pair's side: whichever of the two the API actually reported.
			side := a.Layer
			if side == 0 {
				side = b.Layer
			}
			ox, oy, ov := overlapExtent(bodyByRef[a.Designator], bodyByRef[b.Designator])
			if !sameAssemblySide(a.Layer, b.Layer) {
				continue // opposite sides: bodies pass through each other legally
			}
			altFit := ov && sameNetSetStack(padsByRef[la], padsByRef[lb])
			if altFit {
				rep.AltFitStacks = append(rep.AltFitStacks, pcbLFinding{
					Type: "alt-fit-stack", A: la, B: lb, Side: sideName(side),
					OvX: round2(ox), OvY: round2(oy)})
				continue
			}
			if ov {
				if (connectorishDes(la) || connectorishDes(lb)) && !anyPadPairTouch(padsByRef[la], padsByRef[lb]) {
					measured := layoutPadsMeasured(padsByRef[la]) && layoutPadsMeasured(padsByRef[lb]) &&
						len(a.PadFailures) == 0 && len(b.PadFailures) == 0 &&
						a.CaptureErrors["pads"] == nil && b.CaptureErrors["pads"] == nil &&
						(a.Availability["pads"] == "" || a.Availability["pads"] == "available") &&
						(b.Availability["pads"] == "" || b.Availability["pads"] == "available")
					finding := pcbLFinding{Type: "under-shell", A: la, B: lb, Side: sideName(side), OvX: round2(ox), OvY: round2(oy)}
					if measured {
						rep.UnderShellPairs = append(rep.UnderShellPairs, finding)
					} else {
						finding.Type = "unverified-overlap"
						rep.UnverifiedOverlaps = append(rep.UnverifiedOverlaps, finding)
					}
					continue
				}
				rep.Overlaps = append(rep.Overlaps, pcbLFinding{
					Type: "overlap", A: la, B: lb, Side: sideName(side),
					OvX: round2(ox), OvY: round2(oy)})
				continue
			}
			if gap := rectGap(bodyByRef[a.Designator], bodyByRef[b.Designator]); gap < minGapMil {
				if sameNetSetStack(padsByRef[la], padsByRef[lb]) {
					continue // 同网集贴邻(并联电容排):有意为之,不算装配过近
				}
				rep.TightPairs = append(rep.TightPairs, pcbLFinding{
					Type: "spacing", A: la, B: lb, Side: sideName(side), Gap: round2(gap)})
			}
		}
	}
	sort.Slice(rep.Shorts, func(i, j int) bool {
		if rep.Shorts[i].A != rep.Shorts[j].A {
			return rep.Shorts[i].A < rep.Shorts[j].A
		}
		return rep.Shorts[i].B < rep.Shorts[j].B
	})

	// 2. Outside board outline. A part is off-board only when one of its PADS lands
	//    outside the outline — a connector whose body/courtyard protrudes past the
	//    edge (Type-C, card slot, screw terminal) with every pad inside is an
	//    INTENTIONAL edge-mount (the mating face overhangs on purpose), not a
	//    misplacement. Fall back to the bbox for parts with no pads (mechanical /
	//    graphic). Pad centers are used (pad-edge-to-outline clearance is DRC's job).
	if outline != nil {
		for _, c := range withBBox {
			var outside bool
			knownPositions := 0
			if cps := padsByRef[c.Designator]; len(cps) > 0 {
				for _, p := range cps {
					if !p.positionKnown() {
						continue
					}
					knownPositions++
					if p.X < outline.MinX || p.X > outline.MaxX ||
						p.Y < outline.MinY || p.Y > outline.MaxY {
						outside = true
						break
					}
				}
			}
			if knownPositions == 0 {
				outside = c.BBox.MinX < outline.MinX || c.BBox.MinY < outline.MinY ||
					c.BBox.MaxX > outline.MaxX || c.BBox.MaxY > outline.MaxY
			}
			if outside {
				rep.OutsideOutline = append(rep.OutsideOutline, pcbLFinding{Type: "outside-outline", A: c.Designator})
			}
		}
	}

	// 3. Ratsnest over SIGNAL nets (power/GND poured → excluded so they don't swamp
	//    the metric). Per net: MST length; then count cross-net segment crossings.
	byNet := map[string][]pcbLPad{}
	for _, p := range pads {
		if !p.positionKnown() || p.Net == "" || isGlobalNet(p.Net) {
			continue
		}
		byNet[p.Net] = append(byNet[p.Net], p)
	}
	nets := make([]string, 0, len(byNet))
	for n := range byNet {
		nets = append(nets, n)
	}
	sort.Strings(nets)

	var edges []ratLink
	for _, n := range nets {
		np := dedupPadPoints(byNet[n])
		if len(np) < 2 {
			continue
		}
		rep.SignalNets++
		for _, e := range netMST(n, np) {
			rep.RatsnestLenMil += e.Len
			edges = append(edges, e)
		}
	}
	rep.RatsnestLenMil = round2(rep.RatsnestLenMil)

	// Cross-net crossings (same-net crossings are fine — one net can touch itself).
	for i := 0; i < len(edges); i++ {
		for j := i + 1; j < len(edges); j++ {
			if edges[i].Net == edges[j].Net {
				continue
			}
			if x, y, ok := segCross(edges[i], edges[j]); ok {
				na, nb := edges[i].Net, edges[j].Net
				if nb < na {
					na, nb = nb, na
				}
				rep.Crossings = append(rep.Crossings, crossFinding{NetA: na, NetB: nb, X: round2(x), Y: round2(y)})
			}
		}
	}
	sort.Slice(rep.Crossings, func(i, j int) bool {
		if rep.Crossings[i].NetA != rep.Crossings[j].NetA {
			return rep.Crossings[i].NetA < rep.Crossings[j].NetA
		}
		return rep.Crossings[i].NetB < rep.Crossings[j].NetB
	})
	rep.CrossingCount = len(rep.Crossings)

	finalizePcbLayoutReport(&rep)
	return rep
}

// One finalizer keeps the pure and live reports consistent after live coverage
// restores information (such as malformed records) omitted by parsed views.
func finalizePcbLayoutReport(rep *pcbLayoutReport) {
	// 4. Score + verdict. Shorts and overlaps are fatal; crossings/outside
	//    dominate routability.
	rep.OK = !rep.Incomplete && len(rep.Overlaps) == 0 && len(rep.OutsideOutline) == 0 && len(rep.Shorts) == 0
	score := 100
	score -= 100 * len(rep.Shorts)        // cross-net copper contact ⇒ 0
	score -= 100 * len(rep.Overlaps)      // any overlap ⇒ 0
	score -= 20 * len(rep.OutsideOutline) // off-board is nearly as bad
	score -= 4 * rep.CrossingCount        // each cross-net crossing = a via/detour
	score -= 1 * len(rep.TightPairs)      // minor
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	rep.Score = score
	switch {
	case len(rep.Shorts) > 0:
		rep.Verdict = "short"
	case len(rep.Overlaps) > 0:
		rep.Verdict = "overlap"
	case rep.Incomplete:
		rep.Verdict = "incomplete"
	case score >= 85:
		rep.Verdict = "easy"
	case score >= 60:
		rep.Verdict = "moderate"
	case score >= 30:
		rep.Verdict = "hard"
	default:
		rep.Verdict = "very-hard"
	}

	rep.Summary = fmt.Sprintf("score %d/100 (%s): %d comps%s, %d short, %d overlap, %d off-board, %d tight; %d signal nets, ratsnest %.0fmil, %d crossings",
		rep.Score, rep.Verdict, rep.ComponentCount, sidesSuffix(rep.Sides), len(rep.Shorts),
		len(rep.Overlaps), len(rep.OutsideOutline),
		len(rep.TightPairs), rep.SignalNets, rep.RatsnestLenMil, rep.CrossingCount)
	if rep.Incomplete {
		rep.Summary += "; geometry inspection incomplete"
	}
}

// sidesSuffix spells out the per-side split on a double-sided board (" [top 90
// / bottom 76]") so the overlap count is read as per-side by construction. A
// single-sided board (or a board with no side data) gets no suffix.
func sidesSuffix(sides map[string]int) string {
	if len(sides) < 2 {
		return ""
	}
	keys := make([]string, 0, len(sides))
	for k := range sides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, sides[k]))
	}
	return " [" + strings.Join(parts, " / ") + "]"
}

// dedupPadPoints collapses pads sharing a coordinate (a multi-pad net can have
// stacked pads) so the MST doesn't emit zero-length edges.
func dedupPadPoints(pads []pcbLPad) []pcbLPad {
	seen := map[[2]int64]bool{}
	out := make([]pcbLPad, 0, len(pads))
	for _, p := range pads {
		k := [2]int64{int64(math.Round(p.X * 100)), int64(math.Round(p.Y * 100))}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return out
}

// netMST builds a minimum spanning tree (Prim, complete Euclidean graph) over a
// net's pads — the shortest set of links that connect every pad, i.e. the ratsnest.
func netMST(net string, pads []pcbLPad) []ratLink {
	n := len(pads)
	if n < 2 {
		return nil
	}
	inTree := make([]bool, n)
	dist := make([]float64, n)
	from := make([]int, n)
	for i := range dist {
		dist[i] = math.Inf(1)
		from[i] = -1
	}
	dist[0] = 0
	var edges []ratLink
	for k := 0; k < n; k++ {
		u := -1
		best := math.Inf(1)
		for v := 0; v < n; v++ {
			if !inTree[v] && dist[v] < best {
				best, u = dist[v], v
			}
		}
		if u == -1 {
			break
		}
		inTree[u] = true
		if from[u] >= 0 {
			a, b := pads[from[u]], pads[u]
			edges = append(edges, ratLink{Net: net, Ax: a.X, Ay: a.Y, Bx: b.X, By: b.Y, Len: math.Hypot(a.X-b.X, a.Y-b.Y)})
		}
		for v := 0; v < n; v++ {
			if inTree[v] {
				continue
			}
			if d := math.Hypot(pads[u].X-pads[v].X, pads[u].Y-pads[v].Y); d < dist[v] {
				dist[v], from[v] = d, u
			}
		}
	}
	return edges
}

// segCross reports whether two ratsnest segments properly cross (interior
// intersection), and where. Shared endpoints do NOT count as a crossing.
func segCross(e, f ratLink) (x, y float64, ok bool) {
	p1x, p1y, p2x, p2y := e.Ax, e.Ay, e.Bx, e.By
	p3x, p3y, p4x, p4y := f.Ax, f.Ay, f.Bx, f.By
	d := (p2x-p1x)*(p4y-p3y) - (p2y-p1y)*(p4x-p3x)
	if math.Abs(d) < 1e-9 {
		return 0, 0, false // parallel / collinear
	}
	t := ((p3x-p1x)*(p4y-p3y) - (p3y-p1y)*(p4x-p3x)) / d
	u := ((p3x-p1x)*(p2y-p1y) - (p3y-p1y)*(p2x-p1x)) / d
	const eps = 1e-6
	if t <= eps || t >= 1-eps || u <= eps || u >= 1-eps {
		return 0, 0, false // touch at/near an endpoint, or outside → not a proper crossing
	}
	return p1x + t*(p2x-p1x), p1y + t*(p2y-p1y), true
}

// runPcbLayoutLint fetches the live placement (bbox + pads), the board outline, and
// the DRC clearance, analyzes, renders, and returns a non-nil error when the layout
// is not OK (short / overlap / off-board) so the command exits non-zero.
// pcbLayoutGateOpts is the legacy --gate compatibility view: it computes and
// reports historical thresholds, but does not authorize routing, persist a
// workflow stage, or turn a score/crossing threshold into a refusal.
type pcbLayoutGateOpts struct {
	gate         bool
	project      string
	minScore     int
	maxCrossings int
}

func runPcbLayoutLint(cfg *appConfig, window string, minGapMil float64, asJSON bool, gate pcbLayoutGateOpts, stdout, stderr io.Writer) error {
	var assembly *pcbAssemblyProfile
	if gate.gate {
		// Reuse an existing assembly profile when available so old invocations
		// retain their useful spacing/access diagnostics. Missing, corrupt, or
		// unresolvable workflow state is non-blocking historical metadata.
		if resolved, rerr := resolveStageProject(cfg, window); rerr == nil {
			gate.project = resolved
		}
		if st, serr := loadPcbStageState(gate.project); serr == nil && st.Assembly != nil {
			assembly = st.Assembly
			if assembly.MinGapMil > minGapMil {
				minGapMil = assembly.MinGapMil
			}
		}
	}
	res, err := requestAction(cfg, "pcb.components.list", window, map[string]any{"includeBBox": true, "includePads": true})
	if err != nil {
		return fmt.Errorf("fetch PCB components: %w", err)
	}
	snapshot := &boardSnapshot{Components: parseBoardComponents(res.Result)}
	comps, pads := snapshot.toLayoutComps()

	// Board outline bbox (best-effort; nil → skip the off-board check).
	var outline *layoutBBox
	if ores, oerr := requestAction(cfg, "pcb.outline.get", window, nil); oerr == nil && ores != nil {
		if bb, ok := mnav(ores.Result, "bbox").(map[string]any); ok {
			minX, ok1 := asFloatOK(bb["minX"])
			minY, ok2 := asFloatOK(bb["minY"])
			maxX, ok3 := asFloatOK(bb["maxX"])
			maxY, ok4 := asFloatOK(bb["maxY"])
			if ok1 && ok2 && ok3 && ok4 {
				outline = &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
			}
		}
	}

	// Default min-gap = the board's track-to-pad clearance (live rule) if not set.
	if minGapMil <= 0 {
		rules, ruleErr := fetchPcbRules(cfg, window)
		if ruleErr != nil {
			return ruleErr
		}
		minGapMil = rules.clearanceMil
	}

	rep := analyzePcbLayout(comps, pads, outline, minGapMil)
	rep.Coverage = pcbReadbackCoverage(res.Result, true)
	rep.Incomplete = !rep.Coverage.Complete
	finalizePcbLayoutReport(&rep)
	if silk, serr := fetchPcbSilk(cfg, window); serr != nil {
		rep.SilkUnchecked = serr.Error()
	} else {
		rep.Silk = findSilkOverPad(silk, snapshot.toCheckPads())
		if outline != nil {
			rep.Silk = append(rep.Silk, findSilkOffBoard(silk, pcbRect{MinX: outline.MinX, MinY: outline.MinY, MaxX: outline.MaxX, MaxY: outline.MaxY})...)
		} else {
			rep.SilkUnchecked = "silk-off-board not judged: board outline unavailable"
		}
	}

	// Hand-solder iron-access check (issue #99): with a hand-solder profile the
	// gate also requires every component to keep at least one clear entry side.
	if gate.gate && assembly != nil && assembly.Profile == "hand-solder" && assembly.LargePadAccessMil > 0 {
		rep.AccessMil = assembly.LargePadAccessMil
		rep.AccessBlocked = analyzeSolderAccess(comps, assembly.LargePadAccessMil)
	}

	// Legacy threshold diagnostic: the base report remains the source of factual
	// geometry errors; score and crossing thresholds are explanatory only.
	var gateVerdict *routeGateVerdict
	if gate.gate {
		gv := evalLayoutGate(rep, gate)
		gateVerdict = &gv
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		payload := map[string]any{"report": rep}
		if gateVerdict != nil {
			payload["gate"] = gateVerdict
		}
		if err := enc.Encode(payload); err != nil {
			return err
		}
	} else {
		renderPcbLayoutReport(rep, stdout)
		if gateVerdict != nil {
			renderLayoutGate(*gateVerdict, stdout)
		}
	}
	// Incomplete evidence is reported as not OK, while ordinary diagnostics
	// remain available. Only observed geometric errors affect this exit code.
	if len(rep.Shorts) > 0 || len(rep.Overlaps) > 0 || len(rep.OutsideOutline) > 0 {
		return fmt.Errorf("layout not routable-ready: %d cross-net short, %d overlap, %d off-board",
			len(rep.Shorts), len(rep.Overlaps), len(rep.OutsideOutline))
	}
	return nil
}

// routeGateVerdict is the legacy-compatible machine-readable threshold result
// emitted by layout-lint --gate. It is diagnostic and is never persisted.
type routeGateVerdict struct {
	Pass          bool     `json:"pass"`
	Score         int      `json:"score"`
	MinScore      int      `json:"minScore"`
	CrossingCount int      `json:"crossingCount"`
	MaxCrossings  int      `json:"maxCrossings"`
	Reasons       []string `json:"reasons,omitempty"`
}

// evalLayoutGate applies the score / crossings / overlap / off-board thresholds.
func evalLayoutGate(rep pcbLayoutReport, opt pcbLayoutGateOpts) routeGateVerdict {
	v := routeGateVerdict{
		Score: rep.Score, MinScore: opt.minScore,
		CrossingCount: rep.CrossingCount, MaxCrossings: opt.maxCrossings,
	}
	if rep.Incomplete {
		v.Reasons = append(v.Reasons, "geometry inspection is incomplete")
	}
	if len(rep.Shorts) > 0 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d cross-net pad short", len(rep.Shorts)))
	}
	if len(rep.Overlaps) > 0 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d overlap", len(rep.Overlaps)))
	}
	if len(rep.OutsideOutline) > 0 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d off-board", len(rep.OutsideOutline)))
	}
	if len(rep.TightPairs) > 0 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d tight pair(s) below %.1fmil assembly gap", len(rep.TightPairs), rep.MinGapMil))
	}
	if len(rep.AccessBlocked) > 0 {
		v.Reasons = append(v.Reasons, fmt.Sprintf("%d component(s) boxed in below the %.1fmil iron-access corridor", len(rep.AccessBlocked), rep.AccessMil))
	}
	if rep.Score < opt.minScore {
		v.Reasons = append(v.Reasons, fmt.Sprintf("score %d < min %d", rep.Score, opt.minScore))
	}
	if opt.maxCrossings >= 0 && rep.CrossingCount > opt.maxCrossings {
		v.Reasons = append(v.Reasons, fmt.Sprintf("crossings %d > max %d", rep.CrossingCount, opt.maxCrossings))
	}
	v.Pass = len(v.Reasons) == 0
	return v
}

// renderLayoutGate prints the human-readable legacy threshold diagnostic.
func renderLayoutGate(v routeGateVerdict, w io.Writer) {
	if v.Pass {
		fmt.Fprintf(w, "\nroutability diagnostic: thresholds met (score %d ≥ %d, crossings %d ≤ %d); no workflow state written\n",
			v.Score, v.MinScore, v.CrossingCount, v.MaxCrossings)
		return
	}
	fmt.Fprintf(w, "\nroutability diagnostic: thresholds missed — %s (informational; routing remains available)\n", strings.Join(v.Reasons, "; "))
}

func renderPcbLayoutReport(rep pcbLayoutReport, w io.Writer) {
	fmt.Fprintf(w, "PCB layout-lint: %s\n", rep.Summary)
	for _, f := range rep.Silk {
		fmt.Fprintf(w, "  WARN  %-12s %s\n", f.Type, f.Message)
	}
	if rep.SilkUnchecked != "" {
		fmt.Fprintf(w, "  INFO  silk rules incomplete: %s\n", rep.SilkUnchecked)
	}
	if rep.Incomplete {
		fmt.Fprintf(w, "  INCOMPLETE geometry: %s\n", strings.Join(rep.Coverage.Limitations, "; "))
	}
	for _, pair := range rep.UnverifiedOverlaps {
		fmt.Fprintf(w, "  UNKNOWN overlap    %s ↔ %s (shell clearance cannot be verified from captured pads)\n", pair.A, pair.B)
	}
	for _, s := range rep.Shorts {
		fmt.Fprintf(w, "  ERROR short      %s[%s] ↔ %s[%s] on %s  (copper overlap %.1f×%.1f mil)\n",
			s.A, s.NetA, s.B, s.NetB, s.Layer, s.OvX, s.OvY)
	}
	for _, o := range rep.Overlaps {
		fmt.Fprintf(w, "  ERROR overlap    %s ↔ %s  (%s side, %.1f×%.1f mil)\n", o.A, o.B, o.Side, o.OvX, o.OvY)
	}
	for _, o := range rep.OutsideOutline {
		fmt.Fprintf(w, "  ERROR off-board  %s extends outside the board outline\n", o.A)
	}
	for _, c := range rep.Crossings {
		fmt.Fprintf(w, "  WARN  crossing   %s × %s @ (%.0f, %.0f)\n", c.NetA, c.NetB, c.X, c.Y)
	}
	for _, t := range rep.TightPairs {
		fmt.Fprintf(w, "  WARN  tight      %s ↔ %s  (%s side) gap %.1f mil (< %.1f)\n", t.A, t.B, t.Side, t.Gap, rep.MinGapMil)
	}
	for _, a := range rep.AccessBlocked {
		fmt.Fprintf(w, "  WARN  no-access  %s boxed in on all sides (best %.1f mil < %.1f iron corridor: L%.0f R%.0f T%.0f B%.0f)\n",
			a.Designator, a.BestGap, rep.AccessMil,
			a.Sides["left"], a.Sides["right"], a.Sides["top"], a.Sides["bottom"])
	}
}
