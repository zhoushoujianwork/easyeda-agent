package app

// pcb_board_snapshot.go — 板级快照：多维布局打分(#167)的统一数据层。
//
// 为什么需要它。在此之前每个消费者都自己发 `pcb.components.list` 再手工弱类型
// 解包：layout-lint 把解析内联在 runner 里(pcb_layoutlint.go)、place-constrained
// 有 parseCpComps、auto-place 有 parseApComps、check 的 antenna/zone 规则各拉一次
// ——全仓 21 处调用点，参数组合还各不相同。多维打分要同时看器件姿态 / 焊盘网表 /
// 丝印 / 板框 / 规则，逐维各拉一次会把一次打分放大成十几个往返，而精修环要反复
// 打分，这条会直接成为迭代瓶颈。
//
// 第二个理由更硬：**金标准好板回归**(#167 第五层)需要把真板落成离线 fixture。
// 现状 analyzePcbLayout 的输入解析在 runner 里，没有 `map[string]any → 结构体`
// 的纯函数缝，testdata 喂不进去。这里把「取数」和「解析」彻底分开：
//
//	fetchBoardSnapshot  = I/O（发 action）
//	parseBoard*         = 纯函数（可单测、可吃 testdata JSON）
//
// 快照本身带 json tag，`pcb snapshot --out board.json` 落盘的就是它，Go 单测
// os.ReadFile + json.Unmarshal 回来即可重放同一块板。
//
// 单位：全部 mil，y-UP（PCB 图元原生 mil；只有 DRC leaf 的 pos 是 mil/10，那条
// 在 pcb_drc_flat.go 里已归一，不流到这里）。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// 数据模型
// ---------------------------------------------------------------------------

// boardPad 是一个已放置焊盘。W/H 是连接器从焊盘形状元组抠出的真实铜箔尺寸，
// 0 = 未知（POLYGON 焊盘 padExtent 返回 null）——下游必须把 0 当"测不到"而不是
// "尺寸为零"。Layer 是焊盘的铜层：1=top / 2=bottom / 12=multi(通孔桶，每层导通)。
type boardPad struct {
	ID                string            `json:"primitiveId,omitempty"`
	Number            string            `json:"padNumber"`
	Net               string            `json:"net"`
	Layer             int               `json:"layer"`
	X                 float64           `json:"x"`
	Y                 float64           `json:"y"`
	W                 float64           `json:"width,omitempty"`
	H                 float64           `json:"height,omitempty"`
	Rotation          float64           `json:"rotation"`
	PadType           int               `json:"padType,omitempty"` // 0=NORMAL 1=TEST 2=MARK_POINT
	Shape             any               `json:"shape,omitempty"`
	SpecialPad        any               `json:"specialPad,omitempty"`
	Hole              any               `json:"hole,omitempty"` // raw drill tuple, 10 mil units; see enclosure.PadHoleMM
	Metallization     *bool             `json:"metallization,omitempty"`
	SpecialPadPresent *bool             `json:"specialPadPresent,omitempty"`
	Availability      map[string]string `json:"availability,omitempty"`
	CaptureErrors     map[string]any    `json:"captureErrors,omitempty"`
	GeometryStatus    string            `json:"geometryStatus,omitempty"`
	GeometryReason    string            `json:"geometryReason,omitempty"`
}

// isThroughHole 报告焊盘是否为通孔（连接器不给 hole 字段，只能用 MULTI 层反推）。
func (p boardPad) isThroughHole() bool { return p.Layer == pcbLayerMulti }

// boardComp 是一个已放置封装的完整姿态。相对既有的 pcbLComp（只有
// designator/layer/bbox）多出 #153 整齐度维度必需的 X/Y/Rotation/Locked ——
// 连接器 serializePcbComponent 一直在返回这些字段，只是过去被 Go 侧丢弃了。
//
// X/Y 是封装 anchor（原点），**不是 bbox 中心**：两者的偏移随旋转变，所以判位置
// 一律用 center()，写坐标才用 anchor（对齐 `pcb modify --center` 的既有约定）。
type boardComp struct {
	ID            string            `json:"primitiveId"`
	UniqueID      string            `json:"uniqueId,omitempty"`
	Footprint     any               `json:"footprint,omitempty"`
	Designator    string            `json:"designator"`
	Name          string            `json:"name,omitempty"`
	Device        string            `json:"device,omitempty"` // manufacturerId 优先，见 cpDeviceName
	Layer         int               `json:"layer"`            // 装配面 1=top 2=bottom 0=unknown
	X             float64           `json:"x"`                // anchor，非中心
	Y             float64           `json:"y"`
	Rotation      float64           `json:"rotation"`
	Locked        bool              `json:"locked,omitempty"`
	BBox          *layoutBBox       `json:"bbox,omitempty"` // 渲染包围盒（含丝印，非 IPC courtyard）
	Pads          []boardPad        `json:"pads"`
	Availability  map[string]string `json:"availability,omitempty"`
	CaptureErrors map[string]any    `json:"captureErrors,omitempty"`
	PadFailures   []map[string]any  `json:"padFailures,omitempty"`
}

// pcbGeometryCoverage separates observed geometry from missing inventories and
// unsupported shapes. Legacy extents remain usable for layout diagnostics, but
// do not certify a copper shape or a complete inspection.
type pcbGeometryCoverage struct {
	Complete                      bool               `json:"complete"`
	ComponentsInventoryKnown      bool               `json:"componentsInventoryKnown"`
	PadsInventoryKnown            bool               `json:"padsInventoryKnown"`
	ComponentCount                int                `json:"componentCount"`
	TotalPads                     int                `json:"totalPads"`
	KnownGeometryPads             int                `json:"knownGeometryPads"`
	UnsupportedGeometryPads       int                `json:"unsupportedGeometryPads"`
	UnknownGeometryPads           int                `json:"unknownGeometryPads"`
	LegacyGeometryPads            int                `json:"legacyGeometryPads"`
	UnverifiedPads                []pcbUnverifiedPad `json:"unverifiedPads,omitempty"`
	MissingPadInventoryComponents []string           `json:"missingPadInventoryComponents,omitempty"`
	MissingBBoxComponents         []string           `json:"missingBBoxComponents,omitempty"`
	Limitations                   []string           `json:"limitations,omitempty"`
}

type pcbUnverifiedPad struct {
	ComponentPrimitiveID string `json:"componentPrimitiveId,omitempty"`
	Designator           string `json:"designator"`
	PrimitiveID          string `json:"primitiveId,omitempty"`
	PadNumber            string `json:"padNumber"`
	Status               string `json:"status"`
	Reason               string `json:"reason"`
	CaptureFailed        bool   `json:"captureFailed,omitempty"`
}

func pcbStringMap(raw any) map[string]string {
	if m, ok := raw.(map[string]string); ok {
		return m
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for key, value := range m {
		out[key], _ = value.(string)
	}
	return out
}

func pcbCaptureErrors(raw any) map[string]any {
	m, _ := raw.(map[string]any)
	return m
}

// pcbPadCaptureIssue is shared by consumers which already parse native pads.
// A failed special-pad getter cannot be interpreted as a normal base pad.
func pcbPadCaptureIssue(pm map[string]any) string {
	a := pcbStringMap(pm["availability"])
	errors := pcbCaptureErrors(pm["captureErrors"])
	for _, key := range []string{"shape", "specialPad"} {
		if issue, exists := errors[key]; exists && issue != nil {
			return fmt.Sprintf("%s capture failed: %v", key, issue)
		}
	}
	for _, key := range []string{"shape", "specialPad"} {
		if state, exists := a[key]; exists && state != "available" {
			return fmt.Sprintf("%s capture is %s", key, state)
		}
	}
	return ""
}

func pcbMeasuredPad(p boardPad, ref string) (pcbPadP, string, string) {
	parsed := pcbPadP{ID: p.ID, Designator: ref, Number: p.Number, Net: p.Net, Layer: p.Layer,
		X: p.X, Y: p.Y, W: p.W, H: p.H, Rotation: p.Rotation}
	label := ref + "." + p.Number
	if issue := pcbPadCaptureIssue(map[string]any{"availability": p.Availability, "captureErrors": p.CaptureErrors}); issue != "" {
		parsed.ShapeIssue = issue
		parsed.W, parsed.H = 0, 0
		return parsed, "unknown", issue
	}
	if p.GeometryStatus == "unknown" && p.GeometryReason != "" {
		parsed.ShapeIssue = p.GeometryReason
		return parsed, "unknown", p.GeometryReason
	}
	if !allFinite(p.X, p.Y, p.Rotation) {
		parsed.ShapeIssue = "pad pose is non-finite"
		return parsed, "unknown", parsed.ShapeIssue
	}
	if !netPathCopperLayer(p.Layer) && p.Layer != pcbLayerMulti {
		parsed.ShapeIssue = "pad copper layer is unavailable or unsupported"
		return parsed, "unknown", parsed.ShapeIssue
	}
	if err := parseNetPathPadShape(map[string]any{"shape": p.Shape, "specialPad": p.SpecialPad}, &parsed, label); err != nil {
		parsed.ShapeIssue = err.Error()
		parsed.W, parsed.H = 0, 0
		return parsed, "unknown", parsed.ShapeIssue
	}
	if !parsed.ShapeOK {
		status := "unsupported"
		if p.Shape == nil {
			status = "unknown"
		}
		return parsed, status, parsed.ShapeIssue
	}
	return parsed, "available", ""
}

func (s *boardSnapshot) geometryCoverage(requireBBox bool) pcbGeometryCoverage {
	c := pcbGeometryCoverage{ComponentsInventoryKnown: s != nil && s.Components != nil, PadsInventoryKnown: true}
	if s != nil && s.ComponentsAvailability != "" && s.ComponentsAvailability != "available" {
		c.ComponentsInventoryKnown = false
	}
	if !c.ComponentsInventoryKnown {
		c.PadsInventoryKnown = false
		c.Limitations = append(c.Limitations, "components inventory is unavailable")
		if s == nil || s.Components == nil {
			return c
		}
	}
	c.ComponentCount = len(s.Components)
	for _, component := range s.Components {
		state, declared := component.Availability["pads"]
		known := declared && state == "available" && component.Pads != nil
		if !declared {
			known = len(component.Pads) > 0
		}
		if component.CaptureErrors["pads"] != nil || len(component.PadFailures) > 0 {
			known = false
		}
		if !known {
			c.PadsInventoryKnown = false
			c.MissingPadInventoryComponents = append(c.MissingPadInventoryComponents, component.Designator)
		}
		bboxState, bboxDeclared := component.Availability["bbox"]
		if requireBBox && (component.BBox == nil || bboxDeclared && bboxState != "available" || component.CaptureErrors["bbox"] != nil) {
			c.MissingBBoxComponents = append(c.MissingBBoxComponents, component.Designator)
		}
		for _, p := range component.Pads {
			c.TotalPads++
			_, status, reason := pcbLayoutPadGeometry(p, component.Designator)
			if status != "available" {
				c.UnverifiedPads = append(c.UnverifiedPads, pcbUnverifiedPad{ComponentPrimitiveID: component.ID,
					Designator: component.Designator, PrimitiveID: p.ID, PadNumber: p.Number, Status: status, Reason: reason,
					CaptureFailed: pcbPadCaptureIssue(map[string]any{"availability": p.Availability, "captureErrors": p.CaptureErrors}) != ""})
			}
			switch status {
			case "available":
				c.KnownGeometryPads++
			case "unsupported":
				c.UnsupportedGeometryPads++
			default:
				c.UnknownGeometryPads++
				if p.Shape == nil && p.W > 0 && p.H > 0 {
					c.LegacyGeometryPads++
				}
			}
		}
	}
	if len(c.MissingPadInventoryComponents) > 0 {
		c.Limitations = append(c.Limitations, "pad inventory unavailable/incomplete for "+joinCapped(c.MissingPadInventoryComponents, 12))
	}
	if len(c.MissingBBoxComponents) > 0 {
		c.Limitations = append(c.Limitations, "rendered bbox unavailable for "+joinCapped(c.MissingBBoxComponents, 12))
	}
	if c.UnsupportedGeometryPads > 0 {
		c.Limitations = append(c.Limitations, fmt.Sprintf("%d pad(s) have captured source shapes unsupported by exact pad-contact inspection", c.UnsupportedGeometryPads))
	}
	if c.UnknownGeometryPads > 0 {
		c.Limitations = append(c.Limitations, fmt.Sprintf("%d pad(s) have unknown source geometry (%d legacy extent-only)", c.UnknownGeometryPads, c.LegacyGeometryPads))
	}
	c.Complete = c.ComponentsInventoryKnown && c.PadsInventoryKnown && len(c.MissingBBoxComponents) == 0 && c.UnknownGeometryPads == 0 && c.UnsupportedGeometryPads == 0
	return c
}

func pcbReadbackCoverage(result map[string]any, requireBBox bool) pcbGeometryCoverage {
	components, ok := result["components"].([]any)
	if !ok {
		return (&boardSnapshot{}).geometryCoverage(requireBBox)
	}
	snap := &boardSnapshot{Components: parseBoardComponents(result)}
	c := snap.geometryCoverage(requireBBox)
	if len(snap.Components) != len(components) {
		c.ComponentsInventoryKnown, c.PadsInventoryKnown, c.Complete = false, false, false
		c.Limitations = append(c.Limitations, "components inventory contains malformed records")
	}
	return c
}

// center 返回判位置该用的坐标：有 bbox 用 bbox 几何中心，否则退回 anchor。
func (c boardComp) center() (float64, float64) {
	if c.BBox != nil {
		return (c.BBox.MinX + c.BBox.MaxX) / 2, (c.BBox.MinY + c.BBox.MaxY) / 2
	}
	return c.X, c.Y
}

func (c boardComp) width() float64 {
	if c.BBox == nil {
		return 0
	}
	return c.BBox.MaxX - c.BBox.MinX
}

func (c boardComp) height() float64 {
	if c.BBox == nil {
		return 0
	}
	return c.BBox.MaxY - c.BBox.MinY
}

// area 是渲染包围盒面积（mil²）。注意这是**渲染** bbox 不是 IPC-7351 courtyard：
// 它把丝印外框算进去，实测常比封装本体大 40%+。任何用它做分母的密度/紧凑度指标
// 都会系统性偏大，阈值必须用真板校准（#167 第五层），不能照搬 KiCad 的经验值。
func (c boardComp) area() float64 { return c.width() * c.height() }

// nets 返回该器件涉及的全部非空网名（去重、排序，保证确定性）。
func (c boardComp) nets() []string {
	seen := map[string]bool{}
	for _, p := range c.Pads {
		if p.Net != "" {
			seen[p.Net] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// signalNets 是 nets() 去掉电源/地（全局网）后的剩余——功能分区聚类只该看信号网，
// GND 连着所有东西，拿它聚类会把整块板并成一坨。
func (c boardComp) signalNets() []string {
	out := make([]string, 0, len(c.Pads))
	for _, n := range c.nets() {
		if !isGlobalNet(n) {
			out = append(out, n)
		}
	}
	return out
}

// 板框的边沿用 apEdge（pcb_autoplace.go:171）——auto-place / place-constrained
// 已经在用这套词汇选边，打分侧再造一套 left/right/top/bottom 只会让两边长期分叉。
// y-UP：edgeTop = 高 Y。

// boardOutline 是板框。Points 是真实闭合多边形（顺序点列，mil）；连接器旧版本
// 或读不到时为空，此时 Source=="bbox" 且所有几何退化成 AABB。
//
// 为什么要区分：#168 的 internal-on-edge 判据是「到板框外沿的距离」，异形板
// （Type-C 突出 / 切角 / 挖槽）用 AABB 算会系统性算错——突出部位的件明明贴边，
// AABB 看却离边很远。降级时下游必须把这件事标出来（outlineSource 进报告），
// 而不是假装算准了。
type boardOutline struct {
	BBox   layoutBBox   `json:"bbox"`
	Points [][2]float64 `json:"points,omitempty"`
	Source string       `json:"source"` // "polygon" | "bbox"
	// Format 是连接器解析板框时的自述（"polyline" / "unsupported-command:ARC" /
	// "ambiguous:3-polylines" / "" 表示旧连接器没这个字段）。降级时它就是原因，
	// 直接进报告的 partial[]，省得用户猜"为什么这块板只有 AABB"。
	Format string `json:"format,omitempty"`
}

func (o *boardOutline) width() float64  { return o.BBox.MaxX - o.BBox.MinX }
func (o *boardOutline) height() float64 { return o.BBox.MaxY - o.BBox.MinY }

// area 是板面积：有多边形用鞋带公式，否则 AABB 面积。
// sanitizeOutline 是多边形可信度护栏：闭合折线的鞋带面积远小于其 bbox 面积
// （<50%）说明这条折线不是外板框 —— 典型是板内开槽/切割的**内轮廓**被当成了
// 唯一边界（MIPI 屏扩展板真机实锤：polygon 面积 59k mil² vs 器件总面积 997k，
// utilization 1679%，compact 被打到谷底）。降级回 bbox 并如实记 partial。
// fetch 与 `--from` 回放两条入口都要过它 —— 坏 polygon 可能已经落在旧 dump 里。
func (s *boardSnapshot) sanitizeOutline() {
	o := s.Outline
	if o == nil || len(o.Points) < 3 {
		return
	}
	bbA := o.width() * o.height()
	if bbA <= 0 {
		return
	}
	var sum float64
	for i := range o.Points {
		j := (i + 1) % len(o.Points)
		sum += o.Points[i][0]*o.Points[j][1] - o.Points[j][0]*o.Points[i][1]
	}
	if polyA := math.Abs(sum) / 2; polyA < bbA*0.5 {
		o.Points = nil
		o.Source = "bbox"
		o.Format = "implausible-polygon-area"
		s.note("outline polygon area is <50%% of its bbox (likely an inner cutout, not the board edge) — demoted to the AABB")
	}
}

func (o *boardOutline) area() float64 {
	if len(o.Points) >= 3 {
		var s float64
		for i := range o.Points {
			j := (i + 1) % len(o.Points)
			s += o.Points[i][0]*o.Points[j][1] - o.Points[j][0]*o.Points[i][1]
		}
		if a := math.Abs(s) / 2; a > 0 {
			return a
		}
	}
	return o.width() * o.height()
}

// longAxis 返回板框较长的轴（"x" 或 "y"）——flow-order 维在 spec 没显式声明
// flowAxis 时的默认信号流向轴。
func (o *boardOutline) longAxis() string {
	if o.width() >= o.height() {
		return "x"
	}
	return "y"
}

// distToEdge 是点到板框边界的最短距离（板内为正，板外为负）。多边形可用时走真实
// 边界，否则退回 AABB 的四向轴距。
func (o *boardOutline) distToEdge(x, y float64) float64 {
	if len(o.Points) >= 3 {
		d := math.Inf(1)
		for i := range o.Points {
			j := (i + 1) % len(o.Points)
			d = math.Min(d, segPtDist(x, y, o.Points[i][0], o.Points[i][1], o.Points[j][0], o.Points[j][1]))
		}
		if !o.containsPoint(x, y) {
			return -d
		}
		return d
	}
	inside := x >= o.BBox.MinX && x <= o.BBox.MaxX && y >= o.BBox.MinY && y <= o.BBox.MaxY
	d := math.Min(
		math.Min(math.Abs(x-o.BBox.MinX), math.Abs(o.BBox.MaxX-x)),
		math.Min(math.Abs(y-o.BBox.MinY), math.Abs(o.BBox.MaxY-y)),
	)
	if !inside {
		return -rectPtDist(o.BBox.MinX, o.BBox.MinY, o.BBox.MaxX, o.BBox.MaxY, x, y)
	}
	return d
}

// nearestEdge 返回离点最近的那条 AABB 边及其轴向距离。即使有多边形也按 AABB 判
// 「哪条边」——边的**名字**（left/right/top/bottom）本来就是相对整板朝向说的，
// 用局部多边形边算出来的法向反而会把突出部位的件判到奇怪的边上。
func (o *boardOutline) nearestEdge(x, y float64) (apEdge, float64) {
	cands := []struct {
		e apEdge
		d float64
	}{
		{edgeLeft, x - o.BBox.MinX},
		{edgeRight, o.BBox.MaxX - x},
		{edgeBottom, y - o.BBox.MinY},
		{edgeTop, o.BBox.MaxY - y},
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.d < best.d {
			best = c
		}
	}
	return best.e, best.d
}

// containsPoint 是多边形内外判定（射线法）。仅在 Points 可用时有意义。
func (o *boardOutline) containsPoint(x, y float64) bool {
	if len(o.Points) < 3 {
		return x >= o.BBox.MinX && x <= o.BBox.MaxX && y >= o.BBox.MinY && y <= o.BBox.MaxY
	}
	in := false
	for i, j := 0, len(o.Points)-1; i < len(o.Points); j, i = i, i+1 {
		xi, yi := o.Points[i][0], o.Points[i][1]
		xj, yj := o.Points[j][0], o.Points[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

// boardSnapshot 是一次拉齐的板级只读视图。任何多维打分/规则都该消费它，而不是
// 各自再发一遍 action。Partial 记录哪些部分没取到（后台文档 / 旧连接器 / 平台
// 报错），下游据此降级并在报告里说明——**绝不能因为某段数据缺失就静默跳过一整维
// 却仍然给满分**，那会让"好板得高分"的校准判据失去意义。
type boardSnapshot struct {
	Components             []boardComp          `json:"components"`
	ComponentsAvailability string               `json:"componentsAvailability,omitempty"`
	Outline                *boardOutline        `json:"outline,omitempty"`
	Silk                   []pcbSilkText        `json:"silk,omitempty"`
	CopperLayers           int                  `json:"copperLayers,omitempty"`
	Rules                  *boardRules          `json:"rules,omitempty"`
	Copper                 *boardCopperSnapshot `json:"copper,omitempty"`
	// RoutedLines 是抓取时板上已布铜线段数（pcb.line.list 计数）。指针三态：
	// nil = 旧 dump/没读到（未知），0 = 真没布线。routable 维用它对**成品板**
	// 诚实 —— ratsnest 交叉不知道板子已经布完了，五块开源好板校准实锤该维在
	// 成品板上恒 26~40（布线自由度早已被真实走线兑现，量表系统性偏低）。
	RoutedLines    *int                `json:"routedLines,omitempty"`
	Partial        []string            `json:"partial,omitempty"`
	CapturedAt     string              `json:"capturedAt,omitempty"`
	Project        string              `json:"project,omitempty"`
	SemanticSHA256 string              `json:"semanticSha256,omitempty"`
	Capture        *pcbCaptureEvidence `json:"capture,omitempty"`
	Availability   map[string]string   `json:"availability,omitempty"`
}

// boardCopperSnapshot keeps the connector's exact JSON projections.  The list
// actions are already the public typed schema; retaining their objects avoids a
// second lossy parser and preserves polygon commands/holes for offline checks.
// Availability is per category so [] (known empty) never aliases a failed read.
type boardCopperSnapshot struct {
	Availability  map[string]string `json:"availability"`
	Lines         []any             `json:"lines"`
	Arcs          []any             `json:"arcs"`
	ArcsAvailable *bool             `json:"arcsAvailable,omitempty"`
	Vias          []any             `json:"vias"`
	Pours         []any             `json:"pours"`
	Poured        []any             `json:"poured"`
	Regions       []any             `json:"regions"`
	Fills         []any             `json:"fills"`
}

// boardRules 是 pcbRules 的可序列化投影（pcbRules 字段不导出，进不了 fixture）。
type boardRules struct {
	ClearanceMil           float64 `json:"clearanceMil"`
	ClearanceTrackTrackMil float64 `json:"clearanceTrackTrackMil,omitempty"`
	TrackWidthMil          float64 `json:"trackWidthMil"`
	PowerWidthMil          float64 `json:"powerWidthMil"`
	TrackWidthMinMil       float64 `json:"trackWidthMinMil"`
	ViaDrillMil            float64 `json:"viaDrillMil"`
	ViaDiameterMil         float64 `json:"viaDiameterMil"`
	CopperToEdgeMil        float64 `json:"copperToEdgeMil"`
	Source                 string  `json:"source"` // live | fallback
}

func rulesToBoard(r pcbRules) *boardRules {
	return &boardRules{
		ClearanceMil: r.clearanceMil, ClearanceTrackTrackMil: r.clearanceTrackTrackMil,
		TrackWidthMil: r.trackWidthMil,
		PowerWidthMil: r.powerWidthMil, TrackWidthMinMil: r.trackWidthMinMil,
		ViaDrillMil: r.viaDrillMil, ViaDiameterMil: r.viaDiameterMil,
		CopperToEdgeMil: r.copperToEdgeMil, Source: r.source,
	}
}

func (b *boardRules) toPcbRules() pcbRules {
	if b == nil {
		return defaultPcbRules()
	}
	trackTrack := b.ClearanceTrackTrackMil
	if trackTrack <= 0 {
		// Old snapshots predate pair-specific spacing. Preserve their former,
		// conservative single-scalar behavior instead of silently assuming 4mil.
		trackTrack = b.ClearanceMil
	}
	return pcbRules{
		clearanceMil: b.ClearanceMil, clearanceTrackTrackMil: trackTrack,
		trackWidthMil: b.TrackWidthMil,
		powerWidthMil: b.PowerWidthMil, trackWidthMinMil: b.TrackWidthMinMil,
		viaDrillMil: b.ViaDrillMil, viaDiameterMil: b.ViaDiameterMil,
		copperToEdgeMil: b.CopperToEdgeMil, source: b.Source,
	}
}

// ---------------------------------------------------------------------------
// 派生视图 —— 投影给既有纯核，避免把 overlap/short/ratsnest 再实现一遍
// ---------------------------------------------------------------------------

// toLayoutComps 把快照投影成 analyzePcbLayout 的入参。layout-score 的
// overlap / tight / off-board / short / ratsnest 交叉几维直接复用 layout-lint
// 的纯核，不重复实现（也就不会出现两套引擎长期给矛盾答案的老问题）。
func (s *boardSnapshot) toLayoutComps() ([]pcbLComp, []pcbLPad) {
	comps := make([]pcbLComp, 0, len(s.Components))
	var pads []pcbLPad
	for _, c := range s.Components {
		comps = append(comps, pcbLComp{ID: c.ID, Designator: c.Designator, Layer: c.Layer, BBox: c.BBox,
			Availability: c.Availability, CaptureErrors: c.CaptureErrors, PadFailures: c.PadFailures})
		for _, p := range c.Pads {
			pads = append(pads, p.toLayoutPad(c.Designator))
		}
	}
	return comps, pads
}

func (p boardPad) toLayoutPad(ref string) pcbLPad {
	return pcbLPad{ID: p.ID, Designator: ref, Number: p.Number, Net: p.Net,
		Layer: p.Layer, X: p.X, Y: p.Y, W: p.W, H: p.H,
		Rotation: p.Rotation, Shape: p.Shape, SpecialPad: p.SpecialPad,
		Availability: p.Availability, CaptureErrors: p.CaptureErrors,
		GeometryStatus: p.GeometryStatus, GeometryReason: p.GeometryReason}
}

// toCheckPads 投影成 pcb check 规则族的焊盘入参（decap-too-far / via-in-pad …）。
func (s *boardSnapshot) toCheckPads() []pcbPadP {
	var out []pcbPadP
	for _, c := range s.Components {
		for _, p := range c.Pads {
			// Preserve the failed raw record in the snapshot/coverage, but never
			// manufacture a diagnostic obstacle at a default (0,0) pose.
			if !allFinite(p.X, p.Y, p.Rotation) || p.GeometryStatus == "unknown" && p.GeometryReason == "pad pose x/y/rotation is missing or malformed" {
				continue
			}
			measured, _, _ := pcbMeasuredPad(p, c.Designator)
			out = append(out, measured)
		}
	}
	return out
}

// byDesignator 建位号索引（同名取第一个；平台不该产生重名，但真板上出现过）。
func (s *boardSnapshot) byDesignator() map[string]boardComp {
	m := make(map[string]boardComp, len(s.Components))
	for _, c := range s.Components {
		if _, dup := m[c.Designator]; !dup {
			m[c.Designator] = c
		}
	}
	return m
}

// netPads 按网名分桶所有焊盘（含全局网——调用方自己决定要不要滤）。
func (s *boardSnapshot) netPads() map[string][]boardPad {
	m := map[string][]boardPad{}
	for _, c := range s.Components {
		for _, p := range c.Pads {
			if p.Net == "" {
				continue
			}
			p.Number = c.Designator + "." + p.Number // 让桶里的焊盘可回指器件
			m[p.Net] = append(m[p.Net], p)
		}
	}
	return m
}

// netMembers 按网名给出参与该网的器件位号集合（去重排序）。功能分区聚类的原料。
func (s *boardSnapshot) netMembers() map[string][]string {
	m := map[string]map[string]bool{}
	for _, c := range s.Components {
		for _, n := range c.nets() {
			if m[n] == nil {
				m[n] = map[string]bool{}
			}
			m[n][c.Designator] = true
		}
	}
	out := make(map[string][]string, len(m))
	for n, set := range m {
		lst := make([]string, 0, len(set))
		for d := range set {
			lst = append(lst, d)
		}
		sort.Strings(lst)
		out[n] = lst
	}
	return out
}

// silkByComponent 按父器件 primitiveId 分组丝印文本（位号方位/字号一致性用）。
func (s *boardSnapshot) silkByComponent() map[string][]pcbSilkText {
	m := map[string][]pcbSilkText{}
	for _, t := range s.Silk {
		if t.CompID == "" {
			continue
		}
		m[t.CompID] = append(m[t.CompID], t)
	}
	return m
}

// note 追加一条降级说明（去重）。
func (s *boardSnapshot) note(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, e := range s.Partial {
		if e == msg {
			return
		}
	}
	s.Partial = append(s.Partial, msg)
}

// ---------------------------------------------------------------------------
// 纯解析器 —— 可离线单测，可吃 testdata JSON
// ---------------------------------------------------------------------------

// parseBoardComponents 把 `pcb.components.list {includeBBox,includePads}` 的
// result 解成 []boardComp。纯函数：喂 testdata 里的真板 dump 即可离线重放。
//
// 与既有 parseApComps/parseCpComps 的关系：那两个各自只取自己要的子集且丢弃
// rotation/locked（apComp 有 locked 但无 device，cpComp 有 device 但走两趟合并）。
// 这里一次取全，是它们的超集。
func parseBoardComponents(result map[string]any) []boardComp {
	raw, ok := result["components"].([]any)
	if !ok {
		return nil
	}
	out := make([]boardComp, 0, len(raw))
	for _, ri := range raw {
		cm, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		c := boardComp{
			ID:            asString(cm["primitiveId"]),
			UniqueID:      asString(cm["uniqueId"]),
			Footprint:     cm["footprint"],
			Designator:    asString(cm["designator"]),
			Name:          asString(cm["name"]),
			Device:        cpDeviceName(cm),
			Availability:  pcbStringMap(cm["availability"]),
			CaptureErrors: pcbCaptureErrors(cm["captureErrors"]),
		}
		if failures, ok := cm["padFailures"].([]any); ok {
			for _, failure := range failures {
				if item, ok := failure.(map[string]any); ok {
					c.PadFailures = append(c.PadFailures, item)
				} else {
					c.PadFailures = append(c.PadFailures, map[string]any{"error": "malformed pad failure record"})
				}
			}
		}
		if v, ok := asFloatOK(cm["layer"]); ok {
			c.Layer = int(v)
		}
		c.X, _ = asFloatOK(cm["x"])
		c.Y, _ = asFloatOK(cm["y"])
		c.Rotation, _ = asFloatOK(cm["rotation"])
		c.Locked, _ = cm["locked"].(bool)
		if bb, ok := cm["bbox"].(map[string]any); ok {
			minX, ok1 := asFloatOK(bb["minX"])
			minY, ok2 := asFloatOK(bb["minY"])
			maxX, ok3 := asFloatOK(bb["maxX"])
			maxY, ok4 := asFloatOK(bb["maxY"])
			if ok1 && ok2 && ok3 && ok4 && allFinite(minX, minY, maxX, maxY) && maxX >= minX && maxY >= minY {
				c.BBox = &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
			}
		}
		if rawPads, ok := cm["pads"].([]any); ok {
			c.Pads = make([]boardPad, 0, len(rawPads))
			for index, rp := range rawPads {
				pm, ok := rp.(map[string]any)
				if !ok {
					c.PadFailures = append(c.PadFailures, map[string]any{"index": index, "error": "malformed pad record"})
					continue
				}
				p := boardPad{
					ID:             asString(pm["primitiveId"]),
					Number:         asString(pm["padNumber"]),
					Net:            asString(pm["net"]),
					Availability:   pcbStringMap(pm["availability"]),
					CaptureErrors:  pcbCaptureErrors(pm["captureErrors"]),
					GeometryStatus: asString(pm["geometryStatus"]),
					GeometryReason: asString(pm["geometryReason"]),
				}
				if v, ok := asFloatOK(pm["layer"]); ok {
					p.Layer = int(v)
				}
				var xOK, yOK, rotationOK bool
				p.X, xOK = asFloatOK(pm["x"])
				p.Y, yOK = asFloatOK(pm["y"])
				// 0 = 连接器测不到（POLYGON 焊盘）。保持 0，下游用 halfExt 兜底，
				// 不在这里瞎猜一个尺寸——猜出来的短路判定比没有更危险。
				p.W, _ = asFloatOK(pm["width"])
				p.H, _ = asFloatOK(pm["height"])
				p.Rotation, rotationOK = asFloatOK(pm["rotation"])
				if !xOK || !yOK || !rotationOK {
					p.GeometryStatus, p.GeometryReason = "unknown", "pad pose x/y/rotation is missing or malformed"
				}
				if v, ok := asFloatOK(pm["padType"]); ok {
					p.PadType = int(v)
				}
				// Preserve the exact connector shape tuple and special-pad data for
				// offline fail-closed clearance checks. Width/height alone are only
				// an axis-aligned envelope and cannot prove complex pad clearance.
				p.Shape = pm["shape"]
				p.SpecialPad = pm["specialPad"]
				p.Hole = pm["hole"]
				if m, ok := pm["metallization"].(bool); ok {
					p.Metallization = &m
				}
				if present, known := pm["specialPadPresent"].(bool); known {
					p.SpecialPadPresent = &present
				}
				c.Pads = append(c.Pads, p)
			}
		}
		out = append(out, c)
	}
	return out
}

// parseBoardOutline 解析 `pcb.outline.get` 的 result。优先吃 points（连接器新增
// 的真实多边形，#167）；没有就退回 bbox 并把 Source 标成 "bbox"，让下游知道
// 自己算的是 AABB 近似而不是真板形状。
func parseBoardOutline(result map[string]any) *boardOutline {
	if result == nil {
		return nil
	}
	o := &boardOutline{Source: "bbox", Format: asString(result["outlineFormat"])}
	bbOK := false
	if bb, ok := mnav(result, "bbox").(map[string]any); ok {
		minX, ok1 := asFloatOK(bb["minX"])
		minY, ok2 := asFloatOK(bb["minY"])
		maxX, ok3 := asFloatOK(bb["maxX"])
		maxY, ok4 := asFloatOK(bb["maxY"])
		if ok1 && ok2 && ok3 && ok4 && maxX > minX && maxY > minY {
			o.BBox = layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
			bbOK = true
		}
	}
	if pts, ok := result["points"].([]any); ok {
		for _, pi := range pts {
			pair, ok := pi.([]any)
			if !ok || len(pair) < 2 {
				continue
			}
			x, xok := asFloatOK(pair[0])
			y, yok := asFloatOK(pair[1])
			if xok && yok {
				o.Points = append(o.Points, [2]float64{x, y})
			}
		}
	}
	if len(o.Points) >= 3 {
		o.Source = "polygon"
		if !bbOK { // 连接器只给了点列时自己算 AABB
			minX, minY := math.Inf(1), math.Inf(1)
			maxX, maxY := math.Inf(-1), math.Inf(-1)
			for _, p := range o.Points {
				minX, minY = math.Min(minX, p[0]), math.Min(minY, p[1])
				maxX, maxY = math.Max(maxX, p[0]), math.Max(maxY, p[1])
			}
			o.BBox = layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
			bbOK = true
		}
	} else {
		o.Points = nil // <3 点的退化点列没有几何意义，丢掉别让下游误用
	}
	if !bbOK {
		return nil
	}
	return o
}

// ---------------------------------------------------------------------------
// 取数
// ---------------------------------------------------------------------------

// boardSnapshotOpts 选择要拉哪些部分。默认（零值）只拉器件+板框，这是所有
// 布局维度的最小集；丝印/规则/层数按需开，省往返。
type boardSnapshotOpts struct {
	withSilk   bool
	withRules  bool
	withLayers bool
	withCopper bool
}

// fetchBoardSnapshot 一次拉齐板级只读视图。任何一段失败都只记进 Partial 并继续
// ——照 gatherPcbCheckReport 的既有容错口径（一段读不到不该让整份审计失败），
// 但与之不同的是这里把降级**显式记录**下来而不是只往 stderr 打一行 warning，
// 因为打分报告必须能说明"这一维为什么没算"。
func fetchBoardSnapshot(cfg *appConfig, window string, opts boardSnapshotOpts) (*boardSnapshot, error) {
	capture := newPCBCaptureEvidence()
	snap := &boardSnapshot{CapturedAt: capture.StartedAt, Capture: capture, Availability: map[string]string{}}

	res, err := capture.read(cfg, "pcb.components.list", window,
		map[string]any{"includeBBox": true, "includePads": true})
	if err != nil {
		return nil, fmt.Errorf("fetch PCB components: %w", err)
	}
	snap.Components = parseBoardComponents(res.Result)
	if raw, ok := res.Result["components"].([]any); !ok || len(raw) != len(snap.Components) {
		snap.ComponentsAvailability = "unavailable"
		snap.note("components inventory unavailable or contains malformed records")
	} else {
		snap.ComponentsAvailability = "available"
	}
	snap.Availability["components"] = snap.ComponentsAvailability
	if len(snap.Components) == 0 {
		snap.note("no components on the board")
	}
	missingBBox := 0
	for _, c := range snap.Components {
		if c.BBox == nil {
			missingBBox++
		}
	}
	if missingBBox > 0 {
		snap.note("%d/%d component(s) have no rendered bbox — geometric dimensions degrade to anchor points", missingBBox, len(snap.Components))
	}

	// 板框：pcb.outline.get 在 PCB 非前台时返 null（既有坑），此时 outline 为 nil，
	// 所有「到板边」维度必须降级而不是当成 0 距离。
	outlineRead, outlineEmpty := false, false
	if ores, oerr := capture.read(cfg, "pcb.outline.get", window, nil); oerr == nil && ores != nil {
		outlineRead = true
		snap.Outline = parseBoardOutline(ores.Result)
		// A successful read with zero segments and no bbox is "no outline drawn
		// yet", not a read failure (live 2026-10-13: the old note sent the user
		// hunting for a foreground problem on a board that simply had no outline).
		segs, _ := ores.Result["segments"].(float64)
		outlineEmpty = segs == 0 && ores.Result["bbox"] == nil
	}
	snap.Availability["outline"] = "unknown"
	if snap.Outline != nil {
		snap.Availability["outline"] = "available"
	}
	snap.sanitizeOutline()
	// 已布线段计数（best-effort）：routable 维靠它识别成品板。读不到保持 nil
	//（未知），与「真没布线」(0) 区分。
	if lres, lerr := capture.read(cfg, "pcb.line.list", window, nil); lerr == nil && lres != nil {
		if raw, ok := mnav(lres.Result, "lines").([]any); ok {
			n := len(raw)
			snap.RoutedLines = &n
		}
	}
	snap.Availability["routedLines"] = "unknown"
	if snap.RoutedLines != nil {
		snap.Availability["routedLines"] = "available"
	}
	if snap.Outline == nil {
		switch {
		case outlineRead && outlineEmpty:
			snap.note("no board outline drawn yet — edge/off-board dimensions skipped (draw one with `pcb outline-round` / `pcb outline-set`)")
		case outlineRead:
			snap.note("board outline read but not parseable — edge/off-board dimensions skipped")
		default:
			snap.note("board outline unavailable (read failed; is the PCB the foreground document?) — edge/off-board dimensions skipped")
		}
	} else if snap.Outline.Source != "polygon" {
		// 把连接器给的原因带上：是旧连接器没这个字段、还是板框用了我们解析不了的
		// 圆弧命令、还是外框层上有多条 polyline 分不清哪条是边界。三种成因的处置
		// 完全不同（升级 / 改板框 / 清理残留），笼统说一句"是近似"等于没说。
		why := "connector returned no polygon"
		switch f := snap.Outline.Format; {
		case f == "":
			why = "connector predates the polygon output — re-import the .eext to get exact edge distances"
		case strings.HasPrefix(f, "unsupported-command:"):
			why = fmt.Sprintf("outline uses a curve command this parser does not decode (%s)", f)
		case strings.HasPrefix(f, "ambiguous:"):
			why = fmt.Sprintf("several polylines on the outline layer (%s) — which one is the boundary is ambiguous; clear stale outline primitives", f)
		default:
			why = "connector reported outline format " + f
		}
		snap.note("board outline is an AABB approximation (%s) — edge distances on a non-rectangular board are approximate", why)
	}

	for _, name := range []string{"silk", "rules", "layers", "copper"} {
		snap.Availability[name] = "not-requested"
	}
	if opts.withSilk {
		snap.Availability["silk"] = "unknown"
		sres, serr := capture.read(cfg, "pcb.silk.list", window, nil)
		if serr == nil && sres != nil {
			snap.Silk, serr = parsePcbSilkTexts(sres.Result)
		}
		if serr != nil {
			snap.note("silkscreen unreadable (%v) — silk consistency dimensions skipped", serr)
		} else {
			snap.Availability["silk"] = "available"
			for _, text := range snap.Silk {
				if text.Visible == nil {
					snap.note("silkscreen visibility missing for %s", text.ID)
				}
				if text.LineWidth == nil {
					snap.note("silkscreen line width missing for %s", text.ID)
				}
			}
		}
	}
	if opts.withRules {
		snap.Availability["rules"] = "unknown"
		rres, rerr := capture.read(cfg, "pcb.drc.rules", window, nil)
		if rerr == nil && rres != nil {
			parsed, parseErr := parsePcbRules(rres.Result)
			if parseErr != nil {
				snap.note("live DRC rules unreadable (%v)", parseErr)
			}
			if parseErr == nil && parsed.source == "live" {
				snap.Rules = rulesToBoard(parsed)
				snap.Availability["rules"] = "available"
			}
		}
		if snap.Rules == nil {
			snap.note("live DRC rules unavailable — no default values recorded as measured rules")
		}
	}
	if opts.withLayers {
		snap.Availability["layers"] = "unknown"
		lres, lerr := capture.read(cfg, "pcb.layers.list", window, nil)
		if lerr == nil && lres != nil {
			if n, ok := asFloatOK(lres.Result["copperLayerCount"]); ok && n > 0 {
				snap.CopperLayers = int(n)
				snap.Availability["layers"] = "available"
			}
		}
		if snap.CopperLayers == 0 {
			snap.note("copper layer count unreadable")
		}
	}
	if opts.withCopper {
		snap.fetchCopper(cfg, window, capture)
		snap.Availability["copper"] = "available"
		for _, state := range snap.Copper.Availability {
			if state != "available" {
				snap.Availability["copper"] = "unknown"
			}
		}
	}
	if capture.mismatch != nil {
		return snap, capture.mismatch
	}
	for _, missing := range capture.Missing {
		snap.note("capture provenance: %s", missing)
	}
	semantic, serr := boardSnapshotSemanticSHA256(snap)
	if serr != nil {
		snap.note("semantic hash unavailable (%v)", serr)
	} else {
		snap.SemanticSHA256 = semantic
	}
	return snap, nil
}

func (s *boardSnapshot) fetchCopper(cfg *appConfig, window string, capture *pcbCaptureEvidence) {
	c := &boardCopperSnapshot{Availability: map[string]string{}}
	s.Copper = c
	read := func(name, action string, payload map[string]any, key string, dst *[]any) map[string]any {
		res, err := capture.read(cfg, action, window, payload)
		if err != nil || res == nil {
			c.Availability[name] = "unknown"
			s.note("copper %s unreadable (%v)", name, err)
			return nil
		}
		raw, ok := res.Result[key].([]any)
		if !ok {
			c.Availability[name] = "unknown"
			s.note("copper %s response has no %s array", name, key)
			return res.Result
		}
		*dst = raw
		c.Availability[name] = "available"
		return res.Result
	}
	lineResult := read("routing", "pcb.line.list", nil, "lines", &c.Lines)
	if lineResult != nil {
		if arcs, ok := lineResult["arcs"].([]any); ok {
			c.Arcs = arcs
		}
		if available, ok := lineResult["arcsAvailable"].(bool); ok {
			c.ArcsAvailable = &available
		} else {
			c.Availability["routing"] = "unknown"
			s.note("copper routing arc availability is unknown")
		}
	}
	read("vias", "pcb.via.list", nil, "vias", &c.Vias)
	read("pours", "pcb.pour.list", nil, "pours", &c.Pours)
	read("poured", "pcb.poured.list", nil, "poured", &c.Poured)
	read("regions", "pcb.region.list", nil, "regions", &c.Regions)
	read("fills", "pcb.fill.list", map[string]any{"includeBBox": true}, "fills", &c.Fills)
	for _, group := range [][]any{c.Pours, c.Regions, c.Fills} {
		for _, raw := range group {
			m, ok := raw.(map[string]any)
			if ok && m["geometryAvailable"] != true {
				s.note("one or more copper/region boundaries have unknown polygon geometry")
				break
			}
		}
	}
}

func boardSnapshotSemanticSHA256(s *boardSnapshot) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil board snapshot")
	}
	clone := *s
	clone.CapturedAt = ""
	clone.Capture = nil
	// Project is capture provenance, not board semantics. `pcb dump` resolves
	// and attaches the user-facing project label after fetchBoardSnapshot has
	// collected the geometry; hashing it made every --project dump invalidate
	// its own semanticSha256 as soon as it was written to disk.
	clone.Project = ""
	clone.Partial = nil
	for _, note := range s.Partial {
		if !strings.HasPrefix(note, "capture provenance:") {
			clone.Partial = append(clone.Partial, note)
		}
	}
	clone.SemanticSHA256 = ""
	raw, err := json.Marshal(clone)
	if err != nil {
		return "", err
	}
	// Host getters return the same geometry with float noise across a reload
	// (-200.30700000000001 vs -200.307; live 2026-10-13: identical board, two
	// hashes). Hash a canonical form with every number rounded to 0.001 mil so
	// the digest tracks design changes, not IEEE-754 jitter.
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(roundSemanticNumbers(tree))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// roundSemanticNumbers rounds every JSON number to 1e-3 (normalising -0 to 0).
func roundSemanticNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = roundSemanticNumbers(x)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = roundSemanticNumbers(x)
		}
		return t
	case float64:
		r := math.Round(t*1000) / 1000
		if r == 0 {
			return 0.0
		}
		return r
	default:
		return v
	}
}

// loadBoardSnapshotFile 从磁盘读回一份快照（金标准回归 / --from 离线打分）。
func loadBoardSnapshotFile(r io.Reader) (*boardSnapshot, error) {
	var snap boardSnapshot
	dec := json.NewDecoder(r)
	if err := dec.Decode(&snap); err != nil {
		return nil, fmt.Errorf("parse board snapshot: %w", err)
	}
	// 坏 polygon 可能已经落在旧 dump 里 —— 回放路径同样要过护栏。
	snap.sanitizeOutline()
	return &snap, nil
}

// writeBoardSnapshotFile preserves the exact dump bytes, then hashes the
// persisted file in a separate receipt. A digest inside its own JSON document
// cannot be the file's byte hash; semanticSha256 remains a different contract.
func writeBoardSnapshotFile(path string, data []byte) error {
	var snapshot boardSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("parse snapshot before writing: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read persisted snapshot: %w", err)
	}
	actualSHA := sha256Hex(persisted)
	if actualSHA != sha256Hex(data) {
		return fmt.Errorf("snapshot changed before file-hash receipt could be written")
	}
	receipt := map[string]any{
		"kind": "pcb-dump-file", "version": 1, "snapshot": filepath.Base(path),
		"sha256": actualSHA, "bytes": len(persisted), "capturedAt": snapshot.CapturedAt,
		"capture": snapshot.Capture, "availability": snapshot.Availability, "partial": snapshot.Partial,
	}
	if err := writeJSONFile(path+".capture.json", receipt); err != nil {
		return fmt.Errorf("write snapshot capture receipt: %w", err)
	}
	return nil
}
