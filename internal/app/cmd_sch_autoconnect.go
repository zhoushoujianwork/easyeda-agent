package app

import (
	"math"
	"sort"
	"strings"
)

// ── sch autoconnect: pin-aware deterministic connect planner ────────────────
//
// `schematic.power.connect_pin` already solves the low-level safety problem
// (pin → short wire → flag/netport, so a netflag never sits on a bare pin and
// trips DRC). But it still makes the CALLER pick `direction` and `offset`, which
// turns layout quality into AI/manual judgment.
//
// `sch autoconnect` removes that judgment: it pulls the real rendered geometry
// (part bboxes, pin coordinates, existing flag/port/label bboxes), enumerates
// every (direction × offset) candidate, scores each with a PURE deterministic
// cost function, picks the lowest-cost one, and delegates the actual mutation to
// the existing `connect_pin` primitive. Mirrors `analyzeLayout` in
// cmd_sch_layout.go — pure geometry in Go, unit-testable, no side effects in the
// scorer. See issue #24.

// ── tunable rules ───────────────────────────────────────────────────────────

// autoconnectRules are the planner knobs. Mirrors the `rules` block of the spec
// JSON. Offsets/gaps are in schematic units (the same space connect_pin's
// `offset` and the pin/bbox coordinates live in).
type autoconnectRules struct {
	AvoidTitleBlock bool    `json:"avoidTitleBlock"`
	AvoidPinFanout  bool    `json:"avoidPinFanout"`
	StaggerLabels   bool    `json:"staggerLabels"`
	OffsetMin       float64 `json:"offsetMin"`
	OffsetMax       float64 `json:"offsetMax"`
	OffsetStep      float64 `json:"offsetStep"`
	MinLabelGap     float64 `json:"minLabelGap"`
	// OffsetCap 是桩长的**硬上限**(0 = 不设限,保持既有行为)。
	//
	// 为什么需要它:OffsetMax 只封住细档(6 一跳那一串),而 candidateOffsets 还
	// **常驻**「标准档位」min+k·laneStepFor(netport 一档 ~89,三档铺到 ~285),
	// extendedOffsets 更能铺到 3×OffsetMax 甚至跟着 laneFloor 无上界。对**自由
	// 落点**的场景这是对的(躲不开就得走远);但对「刚体平移后把原样重连回去」
	// 和「按 phase A 的收敛计划落地」这两类场景,桩长是**被规划定死的量**,
	// 评分器多走一档就等于把区框撑胖一档 —— 真机实测:group-move 一次 --dx 40
	// 把 U 组框从 315×389 撑到 523×406(+208 ≈ 两个 netport 档),phase A 的
	// 区内收敛被一次「微调」撤销大半。
	//
	// 所以调用方能给出上界时就给:枚举一律不越过它(细档 / 标准档 / 扩展档三处
	// 都夹),评分器仍在剩下的档位里自由选方向和深浅。
	OffsetCap float64 `json:"offsetCap,omitempty"`
}

// defaultAutoconnectRules matches the spec defaults documented in issue #24.
func defaultAutoconnectRules() autoconnectRules {
	return autoconnectRules{
		AvoidTitleBlock: true,
		AvoidPinFanout:  true,
		StaggerLabels:   true,
		OffsetMin:       18,
		OffsetMax:       80,
		OffsetStep:      6,
		MinLabelGap:     12,
	}
}

// ── scene geometry ──────────────────────────────────────────────────────────

// acPin is a pin in the scene: its coordinate plus the owning part's identity
// and bbox (for outward-side reasoning). Pins with no owner bbox still
// participate in crossing checks.
type acPin struct {
	X, Y       float64
	Designator string
	PinNumber  string
	PinName    string
	OwnerBBox  *layoutBBox
	// PinRotation is the pin primitive's rendered outward direction.  It is
	// authoritative for asymmetric connector symbols; bbox-center inference is
	// only a fallback for older connectors that did not serialize rotation.
	PinRotation *float64
	// Net is the pin's CURRENT authoritative net (from schematic.components.list
	// --include-pins). Empty means "floating" when NetKnown is true; NetKnown is
	// false when the netlist wasn't available, so idempotency checks can't run and
	// must fall back to unconditional connect. See issue #50.
	Net      string
	NetKnown bool
}

// acComponent is a part known to the scene, whether or not its pins made it in.
// When the scene is built with --all-pages, parts on non-active pages still appear
// here (by designator) but have HasPins=false because the EDA pin lookup only
// returns pins for the active page. PageUuid/PageName are populated when the
// extension supplies them; empty otherwise. This lets resolvePinCoord tell
// "placed on another page" apart from "truly not placed / pin typo".
type acComponent struct {
	Designator string
	HasPins    bool
	PageUuid   string
	PageName   string
}

// wireSegment is one existing schematic wire segment (a single polyline edge),
// tagged with its net when known. autoconnect uses these to HARD-REJECT any
// candidate stub that would touch a foreign-net wire — EasyEDA merges nets at an
// endpoint-on-wire junction, a silent short the post-hoc DRC can't catch. See #64.
type wireSegment struct {
	X0, Y0, X1, Y1 float64
	Net            string // "" when the wire carries no resolvable net name
}

// acScene is the full geometric context one autoconnect run reasons against.
// Flags grows as connections are placed so later labels stagger off earlier ones.
type acScene struct {
	Parts []layoutBBox // real part bboxes (componentType "part")
	Pins  []acPin      // every pin across all parts
	Flags []layoutBBox // existing netflag/netport/netlabel bboxes
	// Texts are existing free text notes (电路说明) — schematic.text.list, sized by
	// noteSizeOf because the platform gives text primitives NO bbox. 它们过去完全
	// 不在 scene 里,于是 marker 可以正大光明压在电路说明上,只有事后 layout-score
	// 的 frame-fit 维度才隐约发现。注释是页面上的**同级占位对象**(ADR-0003),
	// 不是背景装饰。
	Texts                 []layoutBBox
	Wires                 []wireSegment // existing wire segments (issue #64)
	Components            []acComponent // every part seen (by designator), even pin-less off-page ones
	TitleBlock            *layoutBBox   // derived keep-out (nil if not applied)
	TitleBlockProvisional bool          // true when no sheet bbox was found (keep-out NOT geometrically applied)
	// AmbiguousDesignators are designators the connector flagged as colliding
	// across pages (issue #136): their pin→net attribution is untrustworthy, so
	// their pins arrive with net=null (treated as new) and the report must say why.
	AmbiguousDesignators []string
}

// ── candidate + scoring ─────────────────────────────────────────────────────

type acPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// acReason is one signed cost contribution (penalty > 0, bonus < 0).
type acReason struct {
	Cost float64 `json:"cost"`
	Desc string  `json:"desc"`
}

// acCandidate is one scored (direction, offset) option.
type acCandidate struct {
	Direction string     `json:"direction"`
	Offset    float64    `json:"offset"`
	EndPoint  acPoint    `json:"endPoint"`
	Score     float64    `json:"score"`
	Reasons   []acReason `json:"reasons,omitempty"`
}

// Cost-table constants (issue #24). Kept as named constants so the scorer reads
// like the table and the unit tests can assert exact figures.
const (
	// costHardReject is a sentinel far above any reachable sum of penalties +
	// offset cost, so a candidate carrying it is effectively UNUSABLE — the planner
	// only falls back to it when EVERY option is a hard reject. Used for hazards
	// EasyEDA would silently turn into a wrong connection (endpoint/path on a
	// foreign-net wire, stub crossing a non-target pin). See issue #64.
	costHardReject  = 1e9
	costPartOverlap = 10000 // endpoint/label bbox overlaps a real part bbox
	// (title-block intrusion is now a HARD reject, not a soft cost — see scoreCandidate, issue #147)
	// costPinCross is a HARD reject (issue #64 rec 2): a stub crossing a non-target
	// pin gets trimmed+connected by EasyEDA, and the post-hoc wire-over-pin rule
	// exempts endpoints on pins, so the short goes unnoticed. Never a soft penalty.
	costPinCross = costHardReject
	// costWireTouch is a HARD reject (issue #64 rec 1): a candidate stub whose
	// endpoint or path touches an existing foreign-net wire merges the two nets at
	// the junction. Never a soft penalty.
	costWireTouch     = costHardReject
	costFlagCollision = 1000 // endpoint/label collides with an existing flag/port/label
	costThroughPart   = 500  // stub passes through another component bbox
	// costFoldedPort penalizes standing a long-bodied netport vertical (up/down):
	// the whole 31-long body rotates 90/270 and its net name renders SIDEWAYS —
	// the "标签折起来" readability fail users flag on dense pin columns (live
	// 2026-08-11: 10-pitch MCU columns made every horizontal candidate eat
	// costFanoutChannel, so vertical won and labels folded). 150 sits between
	// costFanoutChannel (a folded label beats squeezing past a fanout channel is
	// FALSE — horizontal-with-channel ≈ 100+ still wins) and costThroughPart /
	// costFlagCollision (a folded label DOES beat overlapping a part or another
	// label). Ground/power markers are near-square and stay exempt.
	costFoldedPort    = 150
	costFanoutChannel = 100 // too close to a preserved pin-fanout channel
	costOffsetPerUnit = 0.1 // +offset * 0.1 — prefer shorter stubs
	bonusOutwardSide  = -20 // direction matches the pin's outward side
	// costOppositeSide:从引脚**背面**引出。比压器件(10000)还贵 —— 压盖是可见、可
	// 后修的,方向反了则整根线的走向都是错的。不做硬拒绝是为了留最后一条活路:
	// 真的四面楚歌时,一根难看的线仍好过一次失败的连接。
	costOppositeSide = 50000
	bonusKindDefault = -10  // direction matches the kind default (GND down / power up / port outward)
	acCoordEps       = 0.01 // coordinate-equality tolerance
	acOverlapEps     = 1e-6 // positive-length threshold for interval/area overlap
)

// acMarkerBBoxProfile is the measured rendered-body envelope of one marker
// family relative to its connection endpoint. Near/Far are positive distances
// along the body's outward axis; Cross is the half-extent across that axis.
//
// These are deliberately NOT centered on the endpoint. Live getPrimitivesBBox
// calibration (EasyEDA Pro 3.2.148) showed that the marker body starts several
// units beyond the connection pin:
//
//	netport: parallel 9.5..40.5, cross ±5.5  (31×11 / 11×31)
//	ground:  parallel 9.5..19.5, cross ±10.5 (10×21 / 21×10)
//	power:   parallel 4.5..10.5, cross ±5.5  (6×11 / 11×6)
//
// Using the old endpoint-centered 24×11 box both under-estimated long netports
// and invented body area behind the endpoint. That made the scorer miss real
// marker/part collisions while penalizing clear fanout space on the circuit side.
type acMarkerBBoxProfile struct {
	Near  float64
	Far   float64
	Cross float64
}

// isNetPortKind reports whether the canonical kind is the long-bodied netport
// family (the only marker family whose vertical placement folds its label —
// ground/power are near-square).
func isNetPortKind(canonicalKind string) bool {
	switch canonicalKind {
	case "net_port_in", "net_port_out", "net_port_bi", "netport":
		return true
	}
	return false
}

// markerBBoxProfile takes the net name because a **netport renders as wide as
// its label**: relayoutPortWidth = 6*len(net)+8, floor 31. The old fixed
// Far=40.5 hard-coded that floor, so every net name longer than 3 chars was
// under-predicted — "C7_N3" really spans 38, i.e. 7 units past the prediction.
// The scorer therefore computed "just clear" for positions that render
// touching, which is exactly the 1.00–11.00-unit grazing overlaps sch check
// kept reporting. ground/power are fixed-size symbols and ignore net.
func markerBBoxProfile(canonicalKind, net string) acMarkerBBoxProfile {
	switch canonicalKind {
	case "net_port_in", "net_port_out", "net_port_bi", "netport":
		return acMarkerBBoxProfile{Near: 9.5, Far: 9.5 + acPortBodyLen, Cross: 5.5}
	case "ground", "analog_ground", "protective_ground", "protect_ground", "gnd", "agnd", "pgnd":
		return acMarkerBBoxProfile{Near: 9.5, Far: 19.5, Cross: 10.5}
	default: // power and any future netflag family: conservative power envelope
		return acMarkerBBoxProfile{Near: 4.5, Far: 10.5, Cross: 5.5}
	}
}

// acPortBodyLen 是 netport 六边形本体的实测长度 —— 平台给的 bbox 恒为 31×11,
// 与网名无关(`C7_N3` 与 `USB_DTR` 一模一样),名字画在本体**外面**。
const acPortBodyLen = 31.0

// acPortTotalLen 是一支 netport 的**总占地**:六边形 + 渲染出来的名字。
//
// 这是全项目唯一的 netport 长度口径,三把尺(check 的 flagTextBand、评分器的
// markerBBoxProfile、布局的 bslReach)必须都问它要。此前各用各的:
// `relayoutPortWidth = max(31, 6×字数+8)` 本是**名字宽**的近似,却被当成总宽复用 ——
// 5 个字的 C7_N4 算出来 38,减掉六边形只剩 7,于是名字有 ~30 宽是判据看不见的,
// 用户一眼就能指出两处压着的标签,而 `sch check` 报 0。
func acPortTotalLen(net string) float64 {
	return acPortBodyLen + 6*float64(len(net)) + 8
}

// oppositeDirection 返回一个方向的正对面;传入空或未知方向时返回空。
func oppositeDirection(dir string) string {
	switch dir {
	case "left":
		return "right"
	case "right":
		return "left"
	case "up":
		return "down"
	case "down":
		return "up"
	}
	return ""
}

// endpointFor computes where connect_pin will land the stub end for a given
// direction/offset. MUST match the connector's switch (extension/src/actions.ts,
// schematicPowerConnectPin): y-UP coords — 'up' increases y, 'down' decreases
// y — so the planned geometry equals the geometry connect_pin actually creates.
// acSchGrid mirrors the connector's SCH_GRID: EasyEDA Pro snaps a created
// netflag/netport's connection pin to a 5-unit grid, and connect_pin aligns the
// stub endpoint to the same grid so the two coincide. The planner MUST snap too —
// scoring an un-snapped endpoint means the geometry checks run on coordinates the
// board will never hold. That cost a real short: a stub planned to (545,272)
// scored "clear" of a foreign-net wire lying at y=270, then landed at (545,270)
// — ON that wire — merging USB_DP into the CC1 net. 5, not 10: many footprints
// have pins on the odd 5-grid, and a 10-snap would pull the endpoint off the pin
// axis into a diagonal stub.
const acSchGrid = 5

func acSnapGrid(v float64) float64 { return math.Round(v/acSchGrid) * acSchGrid }

// endpointFor returns the stub's far end. Only the coordinate ALONG the stub is
// snapped; the perpendicular one stays exactly on the pin, keeping the stub
// orthogonal (a diagonal stub fails to create).
func endpointFor(pinX, pinY, offset float64, dir string) (x, y float64) {
	switch dir {
	case "up":
		return pinX, acSnapGrid(pinY + offset)
	case "down":
		return pinX, acSnapGrid(pinY - offset)
	case "left":
		return acSnapGrid(pinX - offset), pinY
	case "right":
		return acSnapGrid(pinX + offset), pinY
	}
	return pinX, pinY
}

// kindDefaultDirection mirrors the connector's defaultDirection(kind): power up,
// grounds down, in-ports left, out/bi-ports right. Input is the CANONICAL kind.
func kindDefaultDirection(canonicalKind string) string {
	switch canonicalKind {
	case "power":
		return "up"
	case "net_port_in":
		return "left"
	case "net_port_out", "net_port_bi":
		return "right"
	default: // ground / analog_ground / protective_ground / protect_ground / unknown
		return "down"
	}
}

// outwardDirection is the direction that moves the endpoint AWAY from the owning
// part's bbox center — the natural side to route a pin's flag. Empty when the
// owner bbox is unknown.
func outwardDirection(pin acPin) string {
	if pin.PinRotation != nil {
		r := math.Mod(*pin.PinRotation, 360)
		if r < 0 {
			r += 360
		}
		switch {
		case r < 45 || r >= 315:
			return "right"
		case r < 135:
			return "up"
		case r < 225:
			return "left"
		default:
			return "down"
		}
	}
	if pin.OwnerBBox == nil {
		return ""
	}
	cx := (pin.OwnerBBox.MinX + pin.OwnerBBox.MaxX) / 2
	cy := (pin.OwnerBBox.MinY + pin.OwnerBBox.MaxY) / 2
	dx := pin.X - cx
	dy := pin.Y - cy
	if math.Abs(dx) >= math.Abs(dy) {
		if dx >= 0 {
			return "right"
		}
		return "left"
	}
	// y-UP: a pin ABOVE center (larger y) routes outward as 'up' (y+offset).
	if dy >= 0 {
		return "up"
	}
	return "down"
}

// predictedMarkerBBox returns the conservative rendered body bbox for a marker
// placed at endpoint (x,y), keyed by its family and visual stub direction.
//
// **全部四个方向都按 y-UP 语义**:'up' 的 body 在端点上方(y 更大),'down' 在下方
// (y 更小),与 endpointFor / 连接器 schematicPowerConnectPin 同一口径。
//
// 这段注释以前写的是反的("up occupies y-Far..y-Near"),还警告不要改成语义符号
// —— 实测推翻了它:`sch connect --x 200 --y 200 --kind gnd --direction down
// --offset 40` 落到端点 (200,160),旗的真实 bbox 是 y 140.5..150.5,在端点**下方**
// (Near=9.5 / Far=19.5,与 ground profile 吻合)。原注释和当时的代码一起把竖直
// 碰撞检查指到了空位置。改这两支前请照上面那条命令实测,别照注释推理。
func predictedMarkerBBox(x, y float64, canonicalKind, direction, net string) layoutBBox {
	body := predictedMarkerBody(x, y, canonicalKind, direction, net)
	if band := predictedFlagTextBand(x, y, body, canonicalKind, direction, net); band != nil {
		return layoutBBox{
			MinX: math.Min(body.MinX, band.MinX), MinY: math.Min(body.MinY, band.MinY),
			MaxX: math.Max(body.MaxX, band.MaxX), MaxY: math.Max(body.MaxY, band.MaxY),
		}
	}
	return body
}

// predictedFlagTextBand mirrors flagTextBand (cmd_sch_marker_geom.go) — the net
// name rendered next to a power/ground flag. **判定与生成必须同一把尺**:
// `sch check` 的 marker-overlap 判的是「符号本体 ∪ 文字带」,评分器过去只预测
// 符号本体,于是它挑出的"干净"位置在 check 眼里照样重叠 —— 剩余那批
// 1.00×12.00 / 22.50×12.50 的重叠量,12 就是文字带高度本身。
// netport 的名字画在**六边形之外**,所以它和电源/地旗一样要有一条文字带:
// 本体(markerBBoxProfile)只到平台实测的 31,名字接在本体的背离锚点那一端。
// 与 check 侧的 flagTextBand 严格对称 —— 判定与生成同一把尺。
func predictedFlagTextBand(x, y float64, body layoutBBox, canonicalKind, direction, net string) *layoutBBox {
	if net == "" {
		return nil
	}
	switch canonicalKind {
	case "net_port_in", "net_port_out", "net_port_bi", "netport":
		l := acPortTotalLen(net) - acPortBodyLen
		const h = 11.0
		switch direction {
		case "left":
			return &layoutBBox{MinX: body.MinX - l, MinY: body.MinY, MaxX: body.MinX, MaxY: body.MinY + h}
		case "right":
			return &layoutBBox{MinX: body.MaxX, MinY: body.MinY, MaxX: body.MaxX + l, MaxY: body.MinY + h}
		case "up":
			return &layoutBBox{MinX: body.MinX, MinY: body.MaxY, MaxX: body.MinX + h, MaxY: body.MaxY + l}
		case "down":
			return &layoutBBox{MinX: body.MinX, MinY: body.MinY - l, MaxX: body.MinX + h, MaxY: body.MinY}
		}
		return nil
	}
	switch canonicalKind {
	case "ground", "analog_ground", "protective_ground", "protect_ground", "gnd", "agnd", "pgnd",
		"power", "vcc", "vdd":
	default:
		return nil
	}
	l := 6 * float64(len(net))
	const h = 12.0
	switch direction {
	case "right": // 符号在锚右,文字在锚左侧线上方
		return &layoutBBox{MinX: x - l, MinY: y, MaxX: x, MaxY: y + h}
	case "left":
		return &layoutBBox{MinX: x, MinY: y, MaxX: x + l, MaxY: y + h}
	case "up": // 符号在锚上方,文字在符号顶上居中
		return &layoutBBox{MinX: x - l/2, MinY: body.MaxY, MaxX: x + l/2, MaxY: body.MaxY + h}
	case "down":
		return &layoutBBox{MinX: x - l/2, MinY: body.MinY - h, MaxX: x + l/2, MaxY: body.MinY}
	}
	return nil
}

// predictedMarkerBody is the symbol body alone (no text band).
func predictedMarkerBody(x, y float64, canonicalKind, direction, net string) layoutBBox {
	p := markerBBoxProfile(canonicalKind, net)
	switch direction {
	case "left":
		return layoutBBox{
			MinX: x - p.Far, MinY: y - p.Cross,
			MaxX: x - p.Near, MaxY: y + p.Cross,
		}
	case "right":
		return layoutBBox{
			MinX: x + p.Near, MinY: y - p.Cross,
			MaxX: x + p.Far, MaxY: y + p.Cross,
		}
	// y-UP:'up' 让 y **增大**、'down' 让 y **减小**(与 endpointFor 的注释和连接器
	// 的 schematicPowerConnectPin 同一口径)。这两支曾经写反 —— 于是朝下的 GND 旗
	// 被预测在锚点**上方**,落点评分与 stagger 注册检查的都是一个空位置,真实碰撞
	// 全数漏检:实测同一片区域里两支 GND 旗重叠 1.00×12.00 而评分器毫无反应。
	// left/right 一直是对的,所以这个错误只在竖直方向显形(电源/地旗恰恰全是竖直的)。
	case "up":
		return layoutBBox{
			MinX: x - p.Cross, MinY: y + p.Near,
			MaxX: x + p.Cross, MaxY: y + p.Far,
		}
	case "down":
		return layoutBBox{
			MinX: x - p.Cross, MinY: y - p.Far,
			MaxX: x + p.Cross, MaxY: y - p.Near,
		}
	default:
		// planConnection only supplies the four directions above. Keep a safe
		// centered fallback for direct pure-function callers instead of emitting
		// an inverted/zero bbox for malformed input.
		return layoutBBox{
			MinX: x - p.Far, MinY: y - p.Far,
			MaxX: x + p.Far, MaxY: y + p.Far,
		}
	}
}

// segHitsRect reports whether an axis-aligned segment from (x0,y0) to (x1,y1)
// passes through the INTERIOR of rect (positive-length overlap). A segment that
// merely runs along an edge (e.g. a stub leaving a pin on the part boundary)
// does not count — that's what lets an outward stub avoid flagging its own owner.
func segHitsRect(x0, y0, x1, y1 float64, rect layoutBBox) bool {
	if x0 == x1 { // vertical stub
		if !(rect.MinX < x0 && x0 < rect.MaxX) {
			return false
		}
		lo, hi := math.Min(y0, y1), math.Max(y0, y1)
		return math.Min(hi, rect.MaxY)-math.Max(lo, rect.MinY) > acOverlapEps
	}
	// horizontal stub
	if !(rect.MinY < y0 && y0 < rect.MaxY) {
		return false
	}
	lo, hi := math.Min(x0, x1), math.Max(x0, x1)
	return math.Min(hi, rect.MaxX)-math.Max(lo, rect.MinX) > acOverlapEps
}

// pinOnSegment reports whether point (px,py) lies strictly between the endpoints
// of an axis-aligned segment (i.e. the stub crosses that pin).
func pinOnSegment(x0, y0, x1, y1, px, py float64) bool {
	if x0 == x1 { // vertical
		if math.Abs(px-x0) > acCoordEps {
			return false
		}
		lo, hi := math.Min(y0, y1), math.Max(y0, y1)
		return py > lo+acCoordEps && py < hi-acCoordEps
	}
	// horizontal
	if math.Abs(py-y0) > acCoordEps {
		return false
	}
	lo, hi := math.Min(x0, x1), math.Max(x0, x1)
	return px > lo+acCoordEps && px < hi-acCoordEps
}

// pointSegDist is the distance from (px,py) to an axis-aligned segment.
func pointSegDist(x0, y0, x1, y1, px, py float64) float64 {
	if x0 == x1 { // vertical
		lo, hi := math.Min(y0, y1), math.Max(y0, y1)
		cy := math.Max(lo, math.Min(hi, py))
		return math.Hypot(px-x0, py-cy)
	}
	lo, hi := math.Min(x0, x1), math.Max(x0, x1)
	cx := math.Max(lo, math.Min(hi, px))
	return math.Hypot(px-cx, py-y0)
}

// boxesOverlap is true when two bboxes share positive area.
func boxesOverlap(a, b layoutBBox) bool {
	ox := math.Min(a.MaxX, b.MaxX) - math.Max(a.MinX, b.MinX)
	oy := math.Min(a.MaxY, b.MaxY) - math.Max(a.MinY, b.MinY)
	return ox > acOverlapEps && oy > acOverlapEps
}

// orient2D is the signed area (cross product) of (b-a)×(c-a): >0 left turn,
// <0 right turn, ~0 collinear. Used by the general segment-touch test so a stub
// can be checked against an arbitrarily-oriented existing wire.
func orient2D(ax, ay, bx, by, cx, cy float64) float64 {
	return (bx-ax)*(cy-ay) - (by-ay)*(cx-ax)
}

// pointOnSeg reports whether (px,py) lies on segment (x0,y0)-(x1,y1), endpoints
// INCLUDED. Any contact counts, because EasyEDA merges nets wherever a stub end
// or path meets a wire (junction), not just at a proper interior crossing.
func pointOnSeg(px, py, x0, y0, x1, y1 float64) bool {
	if math.Abs(orient2D(x0, y0, x1, y1, px, py)) > acCoordEps*math.Max(1, math.Hypot(x1-x0, y1-y0)) {
		return false
	}
	return px >= math.Min(x0, x1)-acCoordEps && px <= math.Max(x0, x1)+acCoordEps &&
		py >= math.Min(y0, y1)-acCoordEps && py <= math.Max(y0, y1)+acCoordEps
}

// segmentsTouch reports whether segments A(a0→a1) and B(b0→b1) share ANY point —
// a proper crossing, a shared/touching endpoint, or a collinear overlap. This is
// deliberately inclusive: for wire-merge hazard detection any contact is a short.
func segmentsTouch(ax0, ay0, ax1, ay1, bx0, by0, bx1, by1 float64) bool {
	d1 := orient2D(bx0, by0, bx1, by1, ax0, ay0)
	d2 := orient2D(bx0, by0, bx1, by1, ax1, ay1)
	d3 := orient2D(ax0, ay0, ax1, ay1, bx0, by0)
	d4 := orient2D(ax0, ay0, ax1, ay1, bx1, by1)
	if ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
		((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0)) {
		return true // proper crossing
	}
	// Collinear / endpoint-touch cases: any endpoint lying on the other segment.
	return pointOnSeg(ax0, ay0, bx0, by0, bx1, by1) ||
		pointOnSeg(ax1, ay1, bx0, by0, bx1, by1) ||
		pointOnSeg(bx0, by0, ax0, ay0, ax1, ay1) ||
		pointOnSeg(bx1, by1, ax0, ay0, ax1, ay1)
}

// stubTouchesForeignWire reports whether a candidate stub (target pin → endpoint)
// touches any existing wire that would merge into a FOREIGN net. A wire already
// on the SAME target net is skipped — connecting to it is the whole point. Wires
// with no resolvable net ("") are treated as foreign (unknown → conservative hard
// reject), since a silent cross-net merge is the worse outcome. Degenerate
// zero-length wires are ignored. See issue #64.
func stubTouchesForeignWire(pinX, pinY, endX, endY float64, targetNet string, wires []wireSegment) bool {
	// Trim the stub start a hair away from the pin. A foreign wire that touches
	// ONLY at the pin coordinate is a PRE-EXISTING condition (the pin already sits
	// on that net) — the idempotency net check classifies that as a conflict, so it
	// must not hard-reject every direction here. We only care about contact along
	// the rest of the stub, including its far endpoint.
	dx, dy := endX-pinX, endY-pinY
	length := math.Hypot(dx, dy)
	if length < acCoordEps {
		return false // degenerate stub
	}
	trimX := pinX + dx/length*(acCoordEps*4)
	trimY := pinY + dy/length*(acCoordEps*4)
	for _, w := range wires {
		if w.Net != "" && w.Net == targetNet {
			continue // same net — legitimate connection target, not a hazard
		}
		if math.Abs(w.X1-w.X0) < acCoordEps && math.Abs(w.Y1-w.Y0) < acCoordEps {
			continue // degenerate zero-length wire
		}
		if segmentsTouch(trimX, trimY, endX, endY, w.X0, w.Y0, w.X1, w.Y1) {
			return true
		}
	}
	return false
}

// scoreCandidate is the PURE deterministic core: given a pin, a direction, an
// offset, the canonical kind, the scene, and the rules, return the scored
// candidate with its signed cost breakdown. No I/O, no mutation — the whole
// reason this is unit-testable.
func scoreCandidate(pin acPin, dir string, offset float64, canonicalKind, targetNet string, scene acScene, rules autoconnectRules) acCandidate {
	endX, endY := endpointFor(pin.X, pin.Y, offset, dir)
	lbl := predictedMarkerBBox(endX, endY, canonicalKind, dir, targetNet)
	var reasons []acReason

	// +10000 endpoint/label overlaps a real part bbox.
	for _, p := range scene.Parts {
		if boxesOverlap(lbl, p) {
			reasons = append(reasons, acReason{costPartOverlap, "label overlaps a part bbox"})
			break
		}
	}

	// +10000 endpoint/label overlaps an existing text note (电路说明). 与压器件同级:
	// 一个盖住说明文字的网络标签,和一个盖住器件的网络标签,对读图的人是同一种破坏。
	for _, t := range scene.Texts {
		if boxesOverlap(lbl, t) {
			reasons = append(reasons, acReason{costPartOverlap, "label overlaps a text note (电路说明)"})
			break
		}
	}

	// HARD REJECT: endpoint/label enters the title-block keep-out (issue #147). The
	// title block (图签/明细表) is a hard boundary, not a soft cost — a netport dropped
	// on it passes every existing gate (layout-lint is part-only, the electrical
	// check is geometry-blind). Scoring it like costPartOverlap let it WIN when every
	// other direction was itself hard-rejected (all pins toward the sheet corner). As
	// a hard reject it steers to a safe direction, or — when every candidate enters
	// the keep-out — makes runAutoconnect refuse to place rather than intrude it.
	if rules.AvoidTitleBlock && scene.TitleBlock != nil && boxesOverlap(lbl, *scene.TitleBlock) {
		reasons = append(reasons, acReason{costHardReject, "label enters title-block keep-out (hard reject)"})
	}

	// HARD REJECT: stub crosses a non-target pin (issue #64). EasyEDA trims and
	// connects the stub at that pin, and the wire-over-pin DRC exempts endpoints on
	// pins, so the short is invisible after the fact. Never a soft penalty.
	for _, op := range scene.Pins {
		if math.Abs(op.X-pin.X) < acCoordEps && math.Abs(op.Y-pin.Y) < acCoordEps {
			continue // the target pin itself
		}
		// pinOnSegment tests the segment's INTERIOR (endpoints excluded), so it misses
		// a stub that STOPS exactly on a neighbouring pin — which is just as shorted,
		// and which grid snapping makes common: pins sit on the grid, so a snapped
		// endpoint lands on one whenever the pin pitch is near the stub offset. Real
		// case: XL1509's pins 1-4 are 20 apart at x=645; pin2's "up" stub (offset 18 →
		// snapped to 390) ended ON pin3, whose own "up" stub ended ON pin4, chaining
		// three nets (C11_N3 + +5V + GND) into one wire tree.
		endsOnPin := math.Abs(op.X-endX) < acCoordEps && math.Abs(op.Y-endY) < acCoordEps
		if endsOnPin || pinOnSegment(pin.X, pin.Y, endX, endY, op.X, op.Y) {
			reasons = append(reasons, acReason{costPinCross, "stub crosses a non-target pin (hard reject)"})
			break
		}
	}

	// HARD REJECT: stub endpoint or path touches an existing foreign-net wire
	// (issue #64). EasyEDA merges the two nets at the junction — a silent short.
	if stubTouchesForeignWire(pin.X, pin.Y, endX, endY, targetNet, scene.Wires) {
		reasons = append(reasons, acReason{costWireTouch, "stub touches an existing (foreign-net) wire (hard reject)"})
	}

	// +1000 endpoint/label collides with an existing flag/port/label.
	for _, f := range scene.Flags {
		if boxesOverlap(lbl, f) {
			reasons = append(reasons, acReason{costFlagCollision, "label collides with an existing flag/port/label"})
			break
		}
	}

	// +500 stub passes through another component bbox.
	for _, p := range scene.Parts {
		if segHitsRect(pin.X, pin.Y, endX, endY, p) {
			reasons = append(reasons, acReason{costThroughPart, "stub passes through a component bbox"})
			break
		}
	}

	// +100 too close to a preserved fanout channel (a nearby non-target pin the
	// stub runs alongside without crossing).
	if rules.AvoidPinFanout {
		for _, op := range scene.Pins {
			if math.Abs(op.X-pin.X) < acCoordEps && math.Abs(op.Y-pin.Y) < acCoordEps {
				continue
			}
			if pinOnSegment(pin.X, pin.Y, endX, endY, op.X, op.Y) {
				continue // already counted as a crossing
			}
			if d := pointSegDist(pin.X, pin.Y, endX, endY, op.X, op.Y); d > acCoordEps && d < rules.MinLabelGap {
				reasons = append(reasons, acReason{costFanoutChannel, "stub runs close to a pin fanout channel"})
				break
			}
		}
	}

	// +150 a vertical netport folds its net name sideways (see costFoldedPort).
	if isNetPortKind(canonicalKind) && (dir == "up" || dir == "down") {
		reasons = append(reasons, acReason{costFoldedPort, "netport folded vertical — label reads sideways"})
	}

	// +offset * 0.1 — prefer shorter stubs.
	reasons = append(reasons, acReason{round2(offset * costOffsetPerUnit), "offset cost"})

	// -20 direction matches the pin's outward side.
	out := outwardDirection(pin)
	if out == dir {
		reasons = append(reasons, acReason{bonusOutwardSide, "matches pin outward side"})
	}
	// **背面引出是红线**。左侧引脚的 marker 从右边引出,桩线就要穿过或绕过器件本体
	// —— 读图的人根本追不到那根线,而 DRC 不管这个。
	//
	// 朝向过去只是 -20 的奖励,而撞一次标签是 +1000:评分器于是毫不犹豫地为了躲
	// 一次重叠把 marker 甩到引脚背面(实测 C7_N3 接 U3 左侧的 V3 脚,marker 却落在
	// 右边)。代价必须比任何一种软破坏都贵 —— 挤一点可以后修,方向反了整张图就读错了。
	// 避碰撞的正解是**挪器件**,不是把 marker 甩到反面。
	if opp := oppositeDirection(out); opp != "" && dir == opp {
		reasons = append(reasons, acReason{costOppositeSide, "引出方向与引脚朝外方向相反 —— 桩线要穿过/绕过器件本体"})
	}
	// -10 direction matches the kind default.
	if kindDefaultDirection(canonicalKind) == dir {
		reasons = append(reasons, acReason{bonusKindDefault, "matches kind default direction"})
	}

	var score float64
	for _, r := range reasons {
		score += r.Cost
	}
	return acCandidate{
		Direction: dir,
		Offset:    offset,
		EndPoint:  acPoint{X: round2(endX), Y: round2(endY)},
		Score:     round2(score),
		Reasons:   reasons,
	}
}

// candidateOffsets enumerates offsets from OffsetMin to OffsetMax stepping by
// OffsetStep (inclusive of OffsetMax), **加上「长短循环」的标准档位**。
//
// 细档(6 一跳)是给评分器躲零碎障碍用的:躲一根线、错开一个引脚。但同侧第二支
// marker 要让开的不是"一点点",是前一支的**整个占地**(body + 网名 + 间隙 =
// laneStepFor,netport 上 ~85)——而它超出了 OffsetMax(80),细档里根本没有这一档。
// 真机后果:第二支只能在细档里挑个"不够深"的(60),body 正好落进前一支的名字带,
// 同一侧连出 11 处 29×1 的重叠,而且怎么调细档都跳不出去。
//
// 所以标准档位必须常驻:浅档 = OffsetMin,之后每档一个完整 laneStepFor。这就是用户
// 要的「不要阶梯,长短循环」——一档浅一档深,两档都是标准长度,相邻脚交替用。
// 三档封顶(再深就该换方向或挪件了,extendedOffsets 仍是最后的兜底)。
func candidateOffsets(rules autoconnectRules, canonicalKind, net string) []float64 {
	min, max, step := rules.OffsetMin, rules.OffsetMax, rules.OffsetStep
	if step <= 0 {
		step = 6
	}
	if max < min {
		max = min
	}
	var out []float64
	for o := min; o <= max+acOverlapEps; o += step {
		out = append(out, round2(o))
	}
	if lane := laneStepFor(canonicalKind, net); lane > 0 {
		for k := 1; k <= 3; k++ {
			out = append(out, round2(min+float64(k)*lane))
		}
	}
	out = acCapOffsets(out, rules.OffsetCap)
	if len(out) == 0 {
		// 上界比 OffsetMin 还紧:仍要给一个可选档(夹到上界),否则这一脚**无候选**
		// 可评 —— 连不上比连得深还糟。
		if rules.OffsetCap > 0 && rules.OffsetCap < min {
			out = []float64{round2(rules.OffsetCap)}
		} else {
			out = []float64{min}
		}
	}
	sort.Float64s(out)
	dedup := out[:0]
	for i, o := range out {
		if i == 0 || o-dedup[len(dedup)-1] > acOverlapEps {
			dedup = append(dedup, o)
		}
	}
	return dedup
}

var acDirections = []string{"up", "down", "left", "right"}

// planConnection enumerates every (direction × offset) candidate, scores them,
// and returns them sorted best-first. Deterministic tie-break: score asc, then
// direction lexical (down<left<right<up), then offset asc — so the same scene +
// spec always yields the same selection (acceptance: "deterministic result").
// laneFloor 是同侧 lane 要求的最小 offset(0 = 这一侧还没人)。它决定候选枚举要
// 铺到多远 —— 布局腾出的空间只有被枚举到才用得上。
func planConnection(pin acPin, canonicalKind, targetNet string, scene acScene, rules autoconnectRules, laneFloor float64) []acCandidate {
	score := func(offsets []float64) []acCandidate {
		out := make([]acCandidate, 0, len(acDirections)*len(offsets))
		for _, dir := range acDirections {
			for _, off := range offsets {
				out = append(out, scoreCandidate(pin, dir, off, canonicalKind, targetNet, scene, rules))
			}
		}
		return out
	}
	all := score(candidateOffsets(rules, canonicalKind, targetNet))
	// **密集区兜底**:如果常规档位里**每一个**候选都与已有 marker 相撞,那不是
	// 「挑一个最不差的」的场合 —— 挑出来的就是一处真实的标签重叠。此时把桩线
	// 拉长继续找:人工画法本来就是这样(skill conventions:同侧密集旗用阶梯 offset
	// 错列 20/50/80)。扩展档位照样过全部判据(穿件/触异网线/折叠端口都还在),
	// 所以「更远」不等于「更差」,只有真正干净的位置才会赢。
	// 两种情况要铺更远的候选:常规档位里一个干净的都没有,**或者**同侧 lane 已经
	// 占到了常规范围之外(那时不铺,applyLaneStagger 手里根本没有够远的可选)。
	if noCleanCandidate(all) || laneFloor > rules.OffsetMax {
		all = append(all, score(extendedOffsets(rules, laneFloor))...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score < all[j].Score
		}
		if all[i].Direction != all[j].Direction {
			return all[i].Direction < all[j].Direction
		}
		return all[i].Offset < all[j].Offset
	})
	return all
}

// noCleanCandidate reports whether the regular offset range holds NO candidate
// that is both selectable (not hard-rejected) and free of marker collision.
//
// 判据有两处容易写窄:
//   - 「最优的撞了」不够 —— 最优候选可能因别的原因(穿件/折叠)排在后面;
//   - 「每一个都撞」也不对 —— 被 #64 硬拒绝的候选**根本不可选**,把它们算作
//     「还有干净位置」会让扩展在真正需要的时候不触发。实测:D1(SOT23 三脚)
//     相邻脚的 marker 一直挤在一起,就是因为存在「不撞但会短路」的候选。
//
// 所以判据是:有没有一个**既可选又干净**的候选。一个都没有才拉长桩线。
func noCleanCandidate(cands []acCandidate) bool {
	for _, c := range cands {
		if !candidateHardRejected(c) && !candidateCollidesWithMarker(c) {
			return false
		}
	}
	return true
}

// candidateCollidesWithMarker reports whether this候选 carries the flag/label
// collision penalty.
func candidateCollidesWithMarker(c acCandidate) bool {
	for _, r := range c.Reasons {
		if r.Cost == costFlagCollision {
			return true
		}
	}
	return false
}

// extendedOffsets are the longer stubs tried when the regular range has no
// collision-free slot, **或者同侧 lane 要求排到更远的地方**。
//
// 上界过去是拍出来的 3×OffsetMax(=240)。真机暴露了它的问题:布局把 D1 推开 146
// 之后,U3 左侧腾出 276 的通道,而第 6 个 marker 需要 offset 248 —— 248 > 240,
// **空间给了,落点算法却够不着**。上界必须跟着 lane 的实际需求走,不能是固定倍数:
// 腾出多少就要能排到多少,否则推开器件那一步白做。
//
// 仍保留 3×OffsetMax 作为下限:没有 lane 压力时,再长的桩线本身就是视觉噪声。
func extendedOffsets(rules autoconnectRules, laneFloor float64) []float64 {
	step := rules.OffsetStep
	if step <= 0 {
		step = 6
	}
	upper := 3 * rules.OffsetMax
	if laneFloor+step > upper {
		upper = laneFloor + step
	}
	var out []float64
	for o := rules.OffsetMax + step; o <= upper+acOverlapEps; o += step {
		out = append(out, round2(o))
	}
	return acCapOffsets(out, rules.OffsetCap)
}

// acCapOffsets 丢掉越过硬上限的档位(cap ≤ 0 = 不设限)。三处枚举共用一把闸,
// 不许各自 if —— 漏一处就等于没有上限(扩展档正是最容易漏的那处)。
func acCapOffsets(offs []float64, cap float64) []float64 {
	if cap <= 0 {
		return offs
	}
	out := offs[:0]
	for _, o := range offs {
		if o <= cap+acOverlapEps {
			out = append(out, o)
		}
	}
	return out
}

// candidateHardRejected reports whether a candidate carries a hard-reject cost
// (pin-cross or foreign-wire touch, issue #64). When the BEST candidate is still
// hard-rejected, every direction/offset was a hazard — the caller must refuse to
// mutate rather than place a stub it knows would short two nets.
func candidateHardRejected(c acCandidate) bool {
	for _, r := range c.Reasons {
		if r.Cost >= costHardReject {
			return true
		}
	}
	return false
}

// ── idempotency: three-state pin/net decision (issue #50) ───────────────────

// acConnState is the decision for one connection BEFORE any mutation, so a repeat
// run over the same spec is idempotent instead of stacking duplicate flags+wires.
type acConnState string

const (
	// acStateNew: the pin has no net yet (or we can't tell) → plan + connect.
	acStateNew acConnState = "new"
	// acStateAlreadyConnected: the pin is already on the spec's target net → skip.
	acStateAlreadyConnected acConnState = "already-connected"
	// acStateConflict: the pin is on a DIFFERENT net → error unless --replace.
	acStateConflict acConnState = "conflict"
)

// decideConnState is the PURE idempotency core: given the pin's current net
// (currentNet, only meaningful when netKnown is true) and the spec's target net,
// classify the connection. When the current net is unknown (netlist unavailable)
// we can't prove idempotency, so we fall back to "new" and let connect_pin run —
// preserving the pre-#50 behavior rather than silently skipping.
func decideConnState(currentNet string, netKnown bool, targetNet string) acConnState {
	if !netKnown || currentNet == "" {
		return acStateNew
	}
	if currentNet == targetNet {
		return acStateAlreadyConnected
	}
	return acStateConflict
}

// dominantReason picks the most expensive penalty for a rejected candidate's
// human summary; falls back to a generic note when nothing was penalized.
func dominantReason(c acCandidate) string {
	best := ""
	var bestCost float64
	for _, r := range c.Reasons {
		if r.Cost > bestCost {
			bestCost = r.Cost
			best = r.Desc
		}
	}
	if best == "" {
		return "higher total cost (longer offset / non-default direction)"
	}
	return best
}

// ── 同侧 lane 联合分配 ──────────────────────────────────────────────────────
//
// **为什么逐 pin 贪心在相邻脚上必然失败。** SOP/QFN 的引脚间距是 10,而横向
// netport 的 body 高 11 —— 两个相邻脚各挂一个同向 marker,y 范围**必然重叠 1 个
// 单位**。这不是判据错误,是几何必然:改 offset 只改变 marker 离引脚多远(x 方向),
// y 范围由引脚位置决定,一动不动。
//
// 人工画法是**同侧阶梯错列**:第 1 个 offset=18、第 2 个 60、第 3 个 102…,步长
// 大于 marker 自身的 body 长度,于是它们在 x 方向排成一条斜梯,彼此让开。
//
// 逐 pin 贪心做不到这件事:每个 pin 只知道自己的最优,挑完注册进 scene,下一个只能
// 在剩下的缝里找 —— 而这种密度下缝根本不存在。必须记住「这一侧已经落到哪儿了」。

// laneKeyOf 是 lane 台账的键:同一器件的同一侧算一条 lane。
func laneKeyOf(designator, direction string) string {
	return strings.ToUpper(designator) + "|" + direction
}

// laneStepFor 是同一条 lane 上两个 marker 的最小 offset 差:marker 的**整个占地**
// (body + 渲染出来的网名)+ 一个可见间隙。小于它,后一个会压在前一个身上。
//
// netport 的 profile 里 `Far-Near` 已经是「六边形 + 名字」的实宽(relayoutPortWidth),
// 所以这里不再另加名字 —— 加了就是翻倍,标准档位会一路铺到 228。
func laneStepFor(canonicalKind, net string) float64 {
	p := markerBBoxProfile(canonicalKind, net)
	step := (p.Far - p.Near) + acLaneGap
	switch canonicalKind {
	case "net_port_in", "net_port_out", "net_port_bi", "netport":
		step = acPortTotalLen(net) + acLaneGap // 本体只到六边形,名字要另算进步长
	}
	return step
}

// acLaneGap 是同侧相邻 marker 之间的可见间隙。
const acLaneGap = 8.0

// candidateHitsPartOrText reports whether this候选 covers a real part bbox or a
// text note —— 两者都是 costPartOverlap 级别的视觉破坏。
func candidateHitsPartOrText(c acCandidate) bool {
	for _, r := range c.Reasons {
		if r.Cost == costPartOverlap {
			return true
		}
	}
	return false
}

// candidateGoesOppositeSide reports whether this候选 引出方向与引脚朝外方向相反。
func candidateGoesOppositeSide(c acCandidate) bool {
	for _, r := range c.Reasons {
		if r.Cost == costOppositeSide {
			return true
		}
	}
	return false
}

// laneUnacceptable 汇总「为了错开也绝不能选」的候选:短路、压器件/说明、背面引出。
// **这三条必须一起判**。真机教训:第一版只挡短路,推远时压到了 D1;补上压器件之后,
// 「换一个没被占用的方向」那一步又把 C7_N3 甩到了 U3 背面(score 50597 竟然胜过
// 左侧的 1583 —— 不是排序错了,是 lane 在排序之外强行改选)。
// 错开是**优化**,它不能凌驾于任何一条正确性判据之上。
func laneUnacceptable(c acCandidate) bool {
	return candidateHardRejected(c) || candidateHitsPartOrText(c) || candidateGoesOppositeSide(c)
}

// applyLaneStagger 在候选里挑一个**尊重同侧 lane** 的:如果这一侧已经落过 marker,
// 新的必须比它远出一个 body 长度。找不到就退回原来的最优 —— 让位给 #64 那些硬
// 约束,宁可挤一点也不能短路。
func applyLaneStagger(all []acCandidate, lanes map[string]float64,
	designator, net, canonicalKind string) acCandidate {

	if len(all) == 0 {
		return acCandidate{}
	}
	best := all[0]
	// **只在真的会撞时才往深里挪。** 上一版是「同侧每多一个 marker 就再深一个 step」的
	// 阶梯:6 个脚 = 276 深的通道,而器件本体只有 71 宽 —— 簇被标签撑成本体的 6 倍,
	// 于是 D1 整个坐进了 J1 的 marker 场里(`sch clusters` 实测 J1 体积 486×292)。
	// 阶梯本来就是多余的:评分器已经把「撞已放标签 = costFlagCollision」算进总分,
	// 而 offset 每单位只要 0.1 —— 它自己就会为了不撞而走深,不撞时就该待在最浅那条。
	// 引脚在 y 上隔 16、标签才 11 高时,两支 marker 共用最浅 lane 根本不相撞,
	// 阶梯却硬把第二支推到 72。
	if !candidateCollidesWithMarker(best) {
		return best
	}
	// 首选确实撞了:同方向里找第一个不撞、又不破坏正确性判据的,**并且至少让开一个
	// 完整步长**(body + 网名 + 间隙)。只要"不撞"是不够的:候选档位是 6 一跳,
	// 挑出来的往往只比前一支深 42,而 netport 的名字画在 body 外 —— 深的那支 body
	// 正好落进浅的那支名字带里。这就是用户说的「不要阶梯,长短循环」:标准的两档,
	// 一档浅一档深,深的那档必须真的越过前一支的整个占地。
	need := 0.0
	if used, ok := lanes[laneKeyOf(designator, best.Direction)]; ok {
		need = used + laneStepFor(canonicalKind, net)
	}
	for _, c := range all {
		if c.Direction != best.Direction || c.Offset < need || candidateCollidesWithMarker(c) {
			continue
		}
		// **错开不能以别的破坏为代价**。只挡短路是不够的:第一版只过滤硬拒绝,
		// 于是把 marker 推远时推到了邻近器件身上 —— 真机当场多出两条
		// 「D1(part) 与 MCU_TX(netport) 重叠 26.00×11.00」。压器件和压标签一样
		// 是视觉破坏,换一种破坏不算解决。
		if laneUnacceptable(c) {
			continue
		}
		return c
	}
	// 同方向没有出路时,换个没被占用的方向往往比硬挤强。
	for _, c := range all {
		if laneUnacceptable(c) {
			continue
		}
		if _, taken := lanes[laneKeyOf(designator, c.Direction)]; !taken {
			return c
		}
	}
	// 哪条路都通不了 —— 退回原来的最优。挤一点是可见的、可后修的;短路和压器件
	// 不是。宁可留一条 marker-overlap 让 `sch check` 报出来。
	return best
}
