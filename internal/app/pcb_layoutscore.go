package app

// pcb_layoutscore.go — `pcb layout-score` 的骨架：多维分数表 + 归因梯度。
//
// #167 的核心论点：**能自动逼近的前提是先能量化打分——你没法优化一个你测不出来
// 的东西。** 所以建设顺序是先 DETECT（打分）再 ACHIEVE（逼近），这个文件是 DETECT
// 的地基。
//
// 与既有 `pcb layout-lint` 的分工（**不是替换**）：
//
//	layout-lint  = 可布性单标量分 + 布线前的硬门（overlap/short/off-board/ratsnest）
//	               它的公式是 100 −100×short −100×overlap −20×offBoard −4×crossing
//	               −1×tight，一处重叠就把分数打成 0 —— 这正是 #167 说「太粗」的地方：
//	               板子只要有一个重叠，其它八个维度的差异全部被抹平，看不出布局到底
//	               好在哪差在哪。
//	layout-score = 九个维度各自 0-100 + 每维「是哪几个器件拉低了它」。硬错不再抹平
//	               分数，而是单独进 Blocking 列表。
//
// layout-score **复用** layout-lint 的纯核（analyzePcbLayout）算几何维度，不重新
// 实现一遍 overlap/short/ratsnest —— 这个项目吃过两套引擎长期给矛盾答案的亏
// （netlist 引擎被坏原语毒死返 0，check 几何引擎照常工作，两边矛盾了很久）。
//
// ── 两条硬约定 ──────────────────────────────────────────────────────────────
//
// 1. **「没测」和「测了满分」必须可区分。** 一维因为数据缺失（板框读不到 / spec 没
//    声明 flow / 连接器不给丝印）而算不了时，status 是 skipped，它**不参与加权**，
//    并且在报告里写明原因。绝不能默认给 100 —— 那会让「好板应该得高分」这条校准
//    判据失效：一块什么都没测的板会拿满分。
//
// 2. **计数与判定必须一致。** Verdict 的每一种取值都能从报告里的数字推出来：有
//    Blocking 就是 blocked，没有就按 Overall 分档。这个项目踩过「0 个阻塞项却
//    FAIL」的判读陷阱（聚合命令里计数和判定各算各的），这里用 verdictFor 一个函数
//    统一产出，并有单测钉住一致性。

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/spec"
)

// ---------------------------------------------------------------------------
// 维度身份
// ---------------------------------------------------------------------------

// 维度 id。这些字符串是对外契约（JSON key、--only/--skip 的取值、精修环按维派活
// 的路由键），改名等于破坏用户的 playbook assert。
const (
	dimPartition  = "partition"  // 功能分区不串：模块 bbox 互不交错、内部紧凑
	dimFlowOrder  = "flow-order" // 信号流向单调：模块质心沿轴序 vs spec flow 意图
	dimEdgeIO     = "edge-io"    // 对外口聚一条边、开口朝外；内部件不占外沿
	dimProtection = "protection" // 保护件贴端子 + 去耦贴 IC
	dimTidy       = "tidy"       // 齐整：落格 / 朝向一致 / 位号同侧 / 字号统一
	dimCompact    = "compact"    // 紧凑不真撞：板面利用率 + 无真实碰撞
	dimRF         = "rf"         // RF 馈线短 + keepout 全层
	dimRoutable   = "routable"   // 可布性：ratsnest 交叉密度（复用 layout-lint）
	dimClearance  = "clearance"  // 装配间距：tight pair / 手焊通道
)

// dimensionOrder 固定报告里维度的出现顺序（人读时从"意图"到"工艺"递进）。
var dimensionOrder = []string{
	dimPartition, dimFlowOrder, dimEdgeIO, dimProtection,
	dimTidy, dimCompact, dimRF, dimRoutable, dimClearance,
}

// dimensionTitles 是人读标题。
var dimensionTitles = map[string]string{
	dimPartition:  "功能分区",
	dimFlowOrder:  "信号流向",
	dimEdgeIO:     "对外接口与板沿",
	dimProtection: "保护件/去耦就近",
	dimTidy:       "齐整度",
	dimCompact:    "紧凑度",
	dimRF:         "射频",
	dimRoutable:   "可布性",
	dimClearance:  "装配间距",
}

// defaultDimensionWeights 是综合分的默认权重。
//
// 定权原则：**电气与工艺后果 > 意图符合度 > 观感**。可布性/装配间距直接决定板子
// 能不能造出来能不能焊，权重最高；分区/流向是"人类工程师会怎么摆"的意图层；齐整度
// 纯 cosmetic（#153 自己也说不该阻塞电气正确的板），权重最低但不为零——它正是
// 开源板与商业板观感差距最大的一块。
//
// 这些数字是**待校准的初值**，不是真理。#167 第五层要求拿一批公认的好板跑分来校
// 准：好板某一维得低分，就是度量或权重错了，回来改这张表。改之前先跑
// `go test ./internal/app/ -run TestLayoutScore_GoldenBoards`。
var defaultDimensionWeights = map[string]float64{
	dimRoutable:   1.5,
	dimClearance:  1.5,
	dimEdgeIO:     1.2,
	dimPartition:  1.2,
	dimProtection: 1.0,
	dimRF:         1.0,
	dimFlowOrder:  0.8,
	dimCompact:    0.8,
	dimTidy:       0.5,
}

// 维度状态。
const (
	dimScored   = "scored"   // 正常算出分，参与加权
	dimSkipped  = "skipped"  // 数据或意图缺失，不参与加权（必须给 Reason）
	dimDegraded = "degraded" // 算出来了但输入有近似（如板框只有 AABB），参与加权但要说明
)

// ---------------------------------------------------------------------------
// 报告结构
// ---------------------------------------------------------------------------

// scoreContributor 是「拉低这一维的那个器件」。Penalty 是它在本维扣掉的分数
// （0-100 尺度）—— 精修环靠这个值排序决定先动谁，所以它必须是可比的量而不是
// 一个布尔标记。
type scoreContributor struct {
	Designator string  `json:"designator"`
	Penalty    float64 `json:"penalty"`
	Detail     string  `json:"detail,omitempty"`
}

// scoreDimension 是一维的完整结果。
type scoreDimension struct {
	ID           string             `json:"id"`
	Title        string             `json:"title"`
	Status       string             `json:"status"` // scored | skipped | degraded
	Score        float64            `json:"score"`  // 0-100（skipped 时无意义）
	Weight       float64            `json:"weight"`
	Reason       string             `json:"reason,omitempty"` // skipped/degraded 的原因
	Contributors []scoreContributor `json:"contributors,omitempty"`
	Findings     []pcbCheckFinding  `json:"findings,omitempty"`
	// Metrics 是这一维的原始量（如落格率、Kendall-tau、面积比），给人判读和
	// 校准用 —— 只有分数没有原始量时，"这维为什么是 62 分"无法回答。
	Metrics map[string]float64 `json:"metrics,omitempty"`
}

// layoutScoreReport 是 `pcb layout-score` 的完整输出。
type layoutScoreReport struct {
	OK         bool                `json:"ok"`
	Incomplete bool                `json:"incomplete"`
	Coverage   pcbGeometryCoverage `json:"coverage"`
	Overall    float64             `json:"overall"` // 0-100 加权综合分（只算 scored/degraded 维）
	Verdict    string              `json:"verdict"` // blocked | incomplete | poor | fair | good | excellent
	MinScore   float64             `json:"minScore,omitempty"`

	// Blocking 是硬错：跨网短路 / 器件重叠 / 出板框。它们**不参与加权**，而是
	// 独立成一票否决 —— 一块有短路的板不该因为其它八维都漂亮就拿 85 分。
	Blocking           []pcbCheckFinding `json:"blocking,omitempty"`
	UnverifiedOverlaps []pcbLFinding     `json:"unverifiedOverlaps,omitempty"`

	Dimensions []scoreDimension `json:"dimensions"`

	// DimensionScores 是 id→分数的扁平映射，与 Dimensions 冗余，纯粹为了可断言：
	// playbook 的 assert 用的是 `$.` 点路径（actions.md 里已有
	// `{"$.score": ">=95"}` 的先例），点不进数组元素。有了它就能写
	// `{"$.report.dimensionScores.tidy": ">=80"}`。
	// **只收 scored/degraded 的维** —— skipped 的维不出现在这里，
	// 断言时"这维没测"会表现为路径缺失而不是 0 分。
	DimensionScores map[string]float64 `json:"dimensionScores,omitempty"`

	ComponentCount int      `json:"componentCount"`
	ScoredDims     int      `json:"scoredDims"`  // 参与加权的维数
	SkippedDims    int      `json:"skippedDims"` // 因数据/意图缺失跳过的维数
	Partial        []string `json:"partial,omitempty"`
	Summary        string   `json:"summary"`
}

// dimension 按 id 取一维（找不到返回 nil）。精修环靠它读梯度。
func (r *layoutScoreReport) dimension(id string) *scoreDimension {
	for i := range r.Dimensions {
		if r.Dimensions[i].ID == id {
			return &r.Dimensions[i]
		}
	}
	return nil
}

// weakest 返回参与加权的维里分数最低的前 n 个 —— 精修环的派活入口
// （「哪维低就对症下确定性变换」）。
func (r *layoutScoreReport) weakest(n int) []scoreDimension {
	var scored []scoreDimension
	for _, d := range r.Dimensions {
		if d.Status == dimSkipped {
			continue
		}
		scored = append(scored, d)
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score < scored[j].Score })
	if n > 0 && len(scored) > n {
		scored = scored[:n]
	}
	return scored
}

// ---------------------------------------------------------------------------
// 维度计算器契约
// ---------------------------------------------------------------------------

// scoreCtx 是所有维度共享的输入。一次拉齐、一次分析，九维共用 —— 这是 BoardSnapshot
// 存在的直接理由（否则逐维各发一遍 action，精修环反复打分时往返会爆炸）。
type scoreCtx struct {
	snap   *boardSnapshot
	spec   *spec.Spec       // 可能为 nil：没给 --spec 时意图类维度 skipped
	layout *pcbLayoutReport // layout-lint 纯核的结果（几何维复用它）
	rules  pcbRules
	opts   layoutScoreOpts
}

// hasSpec 报告是否有可用的 S0 意图。
func (c *scoreCtx) hasSpec() bool { return c.spec != nil }

// outline 是板框的便捷取用（可能为 nil）。
func (c *scoreCtx) outline() *boardOutline {
	if c.snap == nil {
		return nil
	}
	return c.snap.Outline
}

// dimScorer 是一维打分器。每维一个实现，注册进 allDimScorers。
//
// 契约：
//   - 输入不足时返回 skipped + Reason，**不要返回 100**。
//   - 输入是近似时（板框只有 AABB、bbox 当 courtyard 用）返回 degraded + Reason。
//   - Contributors 必须按 Penalty 降序，精修环直接取前几个。
type dimScorer interface {
	id() string
	score(ctx *scoreCtx) scoreDimension
}

// allDimScorers 是维度注册表。新增一维只需在这里加一行 + 写一个实现文件，
// 不用像 pcb check 那样改六处（Summary 字段 / switch case / renderer format 串 /
// 帮助文本 / skill 文档）——那种手写重复正是 #167 说「按现结构等于 30+ 个编辑点」
// 的原因。
var allDimScorers = []dimScorer{}

// registerDimScorer 供各维度实现文件在 init() 里自注册。
func registerDimScorer(s dimScorer) { allDimScorers = append(allDimScorers, s) }

// newDimension 是各维度实现的构造助手：填好身份和权重，只留分数/归因给实现填。
func newDimension(id string, opts layoutScoreOpts) scoreDimension {
	w := defaultDimensionWeights[id]
	if ow, ok := opts.weights[id]; ok {
		w = ow
	}
	return scoreDimension{
		ID:     id,
		Title:  dimensionTitles[id],
		Status: dimScored,
		Score:  100,
		Weight: w,
	}
}

// skipDimension 是「这一维测不了」的标准返回。**必须**给出人能看懂的原因，
// 因为报告要回答「为什么这维没分」。
func skipDimension(id string, opts layoutScoreOpts, format string, args ...any) scoreDimension {
	d := newDimension(id, opts)
	d.Status = dimSkipped
	d.Score = 0
	d.Reason = fmt.Sprintf(format, args...)
	return d
}

// sortContributors 按扣分降序排好归因（并列时按位号，保证输出确定）。
func sortContributors(cs []scoreContributor) []scoreContributor {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Penalty != cs[j].Penalty {
			return cs[i].Penalty > cs[j].Penalty
		}
		return cs[i].Designator < cs[j].Designator
	})
	return cs
}

// clampScore 把分数夹到 [0,100] 并保留一位小数（避免浮点噪声让 golden 抖动）。
func clampScore(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Round(math.Max(0, math.Min(100, v))*10) / 10
}

// ---------------------------------------------------------------------------
// 组装
// ---------------------------------------------------------------------------

// layoutScoreOpts 是打分的可调项。
type layoutScoreOpts struct {
	minScore float64            // 综合分门限（--min-score），0 = 不设门
	only     map[string]bool    // 只算这些维（--only）
	skip     map[string]bool    // 跳过这些维（--skip）
	weights  map[string]float64 // 覆盖默认权重（--weight dim=val）
	minGap   float64            // 传给 layout-lint 纯核的装配间距
	gridMil  float64            // 齐整度的落格网格（#153 实测建议 5mil）
	// keepAll 让各维保留**全量**归因不做数据层截断（--all / --part 时置位）。
	// 没有它,routable 这类在 scorer 内截到前 12 的维会让 --all 变成谎言、
	// --part 查不到榜外器件。
	keepAll bool
}

// analyzeLayoutScore 是纯核：吃快照 + 意图，出多维分数表。无 I/O，可离线单测，
// 可直接喂金标准板 fixture。
func analyzeLayoutScore(snap *boardSnapshot, s *spec.Spec, opts layoutScoreOpts) layoutScoreReport {
	rep := layoutScoreReport{MinScore: opts.minScore}
	rep.Coverage = snap.geometryCoverage(true)
	rep.Incomplete = !rep.Coverage.Complete
	if snap == nil {
		rep.Summary = "no board snapshot"
		rep.Verdict = "blocked"
		return rep
	}
	rep.ComponentCount = len(snap.Components)
	rep.Partial = append(rep.Partial, snap.Partial...)
	rep.Partial = append(rep.Partial, rep.Coverage.Limitations...)

	rules := snap.Rules.toPcbRules()
	minGap := opts.minGap
	if minGap <= 0 {
		minGap = rules.clearanceMil
		// 合理性上限：装配间距阈值取自 DRC clearance 规则。2026-08 五板校准时
		// K230/RK3568 曾读到 157.48mil、MIPI 236.22mil，当时按"拿错规则"退回
		// 装配下限；根因已查明是规则 `unit:"mil"` 的值被当成 mm 又乘 39.37
		// (#303，pcb_rules.go 已按每条规则自报单位换算)。此上限保留：真正声明
		// ≥20mil 的装配/禁布规则仍不适合当贴装间距，退回规范 §3.4 的 SMD-SMD
		// 装配下限。显式 --min-gap 不受此限(用户说了算)。
		if minGap > 20 {
			minGap = sgAssemblyGapFloorMil
		}
	}

	// 几何维度复用 layout-lint 的纯核 —— overlap/short/off-board/tight/ratsnest
	// 交叉全部由它算，这里只负责把结果翻译成分数和归因。
	comps, pads := snap.toLayoutComps()
	layout := analyzePcbLayout(comps, pads, outlineBBoxOf(snap), minGap)

	ctx := &scoreCtx{snap: snap, spec: s, layout: &layout, rules: rules, opts: opts}

	// 硬错单列，不进加权。
	rep.Blocking = blockingFindings(&layout)
	rep.UnverifiedOverlaps = layout.UnverifiedOverlaps

	for _, id := range dimensionOrder {
		if len(opts.only) > 0 && !opts.only[id] {
			continue
		}
		if opts.skip[id] {
			continue
		}
		sc := scorerFor(id)
		if sc == nil {
			// 该维尚未实现：明确标 skipped 而不是静默消失，否则报告里少一维
			// 没人会注意到。
			rep.Dimensions = append(rep.Dimensions,
				skipDimension(id, opts, "dimension not implemented yet"))
			continue
		}
		rep.Dimensions = append(rep.Dimensions, sc.score(ctx))
	}

	finalizeScoreReport(&rep)
	return rep
}

// scorerFor 按 id 找注册的计算器。
func scorerFor(id string) dimScorer {
	for _, s := range allDimScorers {
		if s.id() == id {
			return s
		}
	}
	return nil
}

// outlineBBoxOf 把快照的板框投影成 layout-lint 纯核要的 *layoutBBox（nil = 跳过
// off-board 检查，与 layout-lint 的既有语义一致）。
func outlineBBoxOf(snap *boardSnapshot) *layoutBBox {
	if snap.Outline == nil {
		return nil
	}
	bb := snap.Outline.BBox
	return &bb
}

// blockingFindings 把 layout-lint 报的硬错翻译成统一的 finding 结构。
//
// 为什么要翻译：layout-lint 的 findings 散在五个不同数组里，严重度只存在于文本
// 渲染器的字面量中（JSON 消费方只能靠"在哪个数组里"推严重度）。打分报告统一用
// pcbCheckFinding（有 Level 枚举），下游（精修环、门、playbook assert）就只需要
// 认一种形状。
func blockingFindings(l *pcbLayoutReport) []pcbCheckFinding {
	var out []pcbCheckFinding
	for _, s := range l.Shorts {
		out = append(out, pcbCheckFinding{
			Type: "cross-net-short", Level: "ERROR",
			Nets:    []string{s.NetA, s.NetB},
			Message: fmt.Sprintf("copper short: %s(%s) ↔ %s(%s) overlap %.1f×%.1f mil on %s", s.A, s.NetA, s.B, s.NetB, s.OvX, s.OvY, s.Layer),
		})
	}
	for _, f := range l.Overlaps {
		out = append(out, pcbCheckFinding{
			Type: "component-overlap", Level: "ERROR",
			Designator: f.A,
			Message:    fmt.Sprintf("footprint overlap: %s ↔ %s by %.1f×%.1f mil (%s side)", f.A, f.B, f.OvX, f.OvY, f.Side),
		})
	}
	for _, f := range l.OutsideOutline {
		out = append(out, pcbCheckFinding{
			Type: "off-board", Level: "ERROR",
			Designator: f.A,
			Message:    fmt.Sprintf("%s sits outside the board outline", f.A),
		})
	}
	return out
}

// finalizeScoreReport 算综合分、判定和摘要。
//
// 这是 verdict 的**唯一**产出点。记忆里那条教训——「计数与判定分离处必查一致性，
// 否则出现 0 个阻塞项却 FAIL」——就靠这里：Verdict 只从 Blocking 和 Overall 推，
// 没有第二条路径。
func finalizeScoreReport(rep *layoutScoreReport) {
	var sumW, sumWS float64
	for _, d := range rep.Dimensions {
		if d.Status == dimSkipped {
			rep.SkippedDims++
			continue
		}
		rep.ScoredDims++
		sumW += d.Weight
		sumWS += d.Weight * d.Score
		if rep.DimensionScores == nil {
			rep.DimensionScores = map[string]float64{}
		}
		rep.DimensionScores[d.ID] = d.Score
	}
	if sumW > 0 {
		rep.Overall = math.Round(sumWS/sumW*10) / 10
	}
	rep.Verdict = verdictFor(rep)
	rep.OK = !rep.Incomplete && len(rep.Blocking) == 0 && (rep.MinScore <= 0 || rep.Overall >= rep.MinScore)

	var b strings.Builder
	if rep.Incomplete {
		b.WriteString("incomplete geometry inspection; ")
	}
	if len(rep.Blocking) > 0 {
		fmt.Fprintf(&b, "%d blocking issue(s); ", len(rep.Blocking))
	}
	if rep.ScoredDims == 0 {
		b.WriteString("no dimension could be scored")
	} else {
		fmt.Fprintf(&b, "overall %.1f/100 over %d dimension(s)", rep.Overall, rep.ScoredDims)
		if rep.SkippedDims > 0 {
			// 跳过的维必须出现在摘要里 —— 否则「7 维都 90 分」读起来像全面体检，
			// 实际上有两维压根没测。
			fmt.Fprintf(&b, ", %d skipped (not scored — see reasons)", rep.SkippedDims)
		}
	}
	rep.Summary = b.String()
}

// verdictFor 是判定的唯一来源。
func verdictFor(rep *layoutScoreReport) string {
	if len(rep.Blocking) > 0 {
		return "blocked"
	}
	if rep.Incomplete {
		return "incomplete"
	}
	if rep.ScoredDims == 0 {
		return "unscored"
	}
	switch {
	case rep.Overall >= 90:
		return "excellent"
	case rep.Overall >= 75:
		return "good"
	case rep.Overall >= 55:
		return "fair"
	default:
		return "poor"
	}
}
