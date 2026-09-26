package app

// cmd_sch_clusters.go — 原理图的 L1 虚拟组(cluster)判据。
//
// **定义(用户 2026-08-15 拍板,见 docs/concepts.md)**:
// 一个有立创编号的器件 + **只挂在它自己引脚上**的 marker / 桩线 / 文字 = 一个虚拟组。
// 跨器件的连线**不计入任何一组的体积** —— 它是两组之间的走线通道,本来就该穿过空白。
//
// 铁律:**组的体积 = 它全部元素的并集,组与组之间不许重叠。**
//
// 为什么必须单独成一个判据:`layout-lint` 默认只看器件本体(非 part 图元全部排除),
// 于是一张 marker 互相压、去耦被标签罩住、簇左沿探出图纸的页,它照样报
// `✓ 0 overlap`。这是「判定与生成同一把尺」在组这一层的显形 —— 尺子看不见的问题,
// 改进也无法验收。

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/schguard"
)

// schCluster 是一个 L1 虚拟组。
//
// Box 是包络(给布局留位用),Parts 是**组成它的每一个图元的实测 box**(判重叠用):
// 一个件的 marker 是四面星形展开的,包络矩形里大半是空的 —— 拿包络判"组间重叠"会
// 把「J1 的上方 marker 和 D1 的下方本体各占一半、谁也没压谁」也报成 ERROR。
// 判据必须落在**真实图元**上,否则它会逼着布局去躲根本不存在的碰撞。
type schCluster struct {
	Designator  string     `json:"designator"`
	PrimitiveID string     `json:"primitiveId,omitempty"`
	Device      string     `json:"device,omitempty"`
	Body        layoutBBox `json:"body"`    // 器件本体
	Box         layoutBBox `json:"box"`     // 体积:本体 ∪ 归属 marker ∪ 归属桩线
	Markers     int        `json:"markers"` // 归属的 marker 数
	Wires       int        `json:"wires"`   // 归属的桩线数(跨组的不算)
	// Members 是组成它的每一个图元的实测 box —— 判组间重叠用它,不用包络。
	Members []layoutBBox `json:"-"`
	// Detail 是每个成员的可读描述(类型/网名/bbox),--members 时打印给人核对。
	Detail []string `json:"members,omitempty"`
	// Typed 是同一份归属信息的**机器口径**(kind/net/bbox)—— phase A 收敛规划器
	// (sch_zone_follow.go)从这里折出类型化端子。Detail 只给人看;机器路径解析
	// 字符串是第二把尺,禁止。
	Typed []schClusterTyped `json:"-"`
}

// schClusterTyped 是一个归属成员的类型化记录。
type schClusterTyped struct {
	Kind    string // part | wire | netflag | netport | netlabel …
	Net     string
	BBox    layoutBBox
	WireID  string
	Segment *[4]float64 // exact observed segment; nil for bodies/markers/legacy members
}

// Model a schematic centreline with a 1-raw topological stroke for positive-area
// member collision checks; the cluster's aggregate Box still uses the exact
// polyline envelope for page occupancy.
const schVisibleWireHalfWidth = 0.5

// schClusterFinding 是一条判定结果。
type schClusterFinding struct {
	Type  string  `json:"type"` // overlap | out-of-sheet | tight
	A     string  `json:"a"`
	B     string  `json:"b,omitempty"`
	OvX   float64 `json:"ovX,omitempty"`
	OvY   float64 `json:"ovY,omitempty"`
	Gap   float64 `json:"gap,omitempty"`
	Note  string  `json:"note,omitempty"`
	Level string  `json:"level"` // ERROR | WARN
}

// schClusterReport 是命令的完整输出。
type schClusterReport struct {
	Clusters []schCluster        `json:"clusters"`
	Findings []schClusterFinding `json:"findings"`
	Sheet    *layoutBBox         `json:"sheetUsable,omitempty"`
	Unowned  int                 `json:"unownedMarkers,omitempty"`
	// TooBig 是**本身就比这一页大**的组(sch_page_fit.go)。与 findings 里的
	// out-of-sheet 是两种病:那个挪一挪能解,这个挪多少次都不会变(#181 第三份
	// 复盘 8+ 轮的根源)。分开一个字段,读的人才不会把两者当同一件事。
	TooBig []schPageFit `json:"pageTooSmall,omitempty"`
}

// schClustersTooBig 挑出「组本身比这一页还大」的那几个。
//
// keepout 传**未膨胀**的图签矩形:虚拟组只是「器件 ∪ 它自己的 marker」,不带
// 区名带/说明带,膨胀会让它凭空严格一档(把"挪一挪能解"误判成"这一页放不下")。
func schClustersTooBig(cs []schCluster, usable, keepout *layoutBBox) []schPageFit {
	if usable == nil {
		return nil // 量不出可用区就不下结论(猜出来的 fits 会掩盖掉真问题)
	}
	var out []schPageFit
	for _, c := range cs {
		if f := judgeSchPageFit(c.Designator, c.Box, *usable, keepout); f.TooBig() {
			out = append(out, f)
		}
	}
	return out
}

// buildSchClusters 是纯函数核心:从实测几何算出每个器件的虚拟组体积。
//
// 归属走**导线本身**,不靠距离:marker 是由一根桩线连到某只引脚上的,顺着线走就知道
// 它挂在谁身上。第一版按「最近的引脚」判,lane 错开把 marker 推到 248 远时它直接判成
// 无主 —— 而那几支恰恰是惹事的那几支,于是体积算小了、判据当场失明。
//
//   - 桩线连通块只触到**一个**器件 → 这块线和挂在上面的 marker 都归它;
//   - 触到**多个**器件 → 这是两组之间的走线,**不计入任何组的体积**;线上的 marker
//     按最近引脚归给其中一个(它物理上就贴着那只脚);
//   - 完全不沾线的 marker(平台丢了线)→ 退回最近引脚,并计入 unowned 统计如果太远。
func buildSchClusters(comps []layoutComp, wires []schGroupWire) ([]schCluster, int) {
	type pinRef struct {
		owner string
		x, y  float64
	}
	var pins []pinRef
	body := map[string]layoutBBox{}
	order := []string{}
	idOf := map[string]string{}
	devOf := map[string]string{}
	for _, c := range comps {
		if c.ComponentType != "part" || c.BBox == nil {
			continue
		}
		d := label(c)
		if _, seen := body[d]; !seen {
			order = append(order, d)
		}
		body[d] = *c.BBox
		idOf[d] = c.ID
		for _, p := range c.Pins {
			pins = append(pins, pinRef{owner: d, x: p.X, y: p.Y})
		}
	}
	box := map[string]layoutBBox{}
	markers := map[string]int{}
	wireCount := map[string]int{}
	members := map[string][]layoutBBox{}
	for d, b := range body {
		box[d] = b
		members[d] = []layoutBBox{b}
	}
	growBox := func(d string, b layoutBBox) {
		cur := box[d]
		box[d] = layoutBBox{
			MinX: math.Min(cur.MinX, b.MinX), MinY: math.Min(cur.MinY, b.MinY),
			MaxX: math.Max(cur.MaxX, b.MaxX), MaxY: math.Max(cur.MaxY, b.MaxY),
		}
	}
	addMember := func(d string, b layoutBBox) {
		members[d] = append(members[d], b)
	}
	grow := func(d string, b layoutBBox) {
		growBox(d, b)
		addMember(d, b)
	}
	detail := map[string][]string{}
	typed := map[string][]schClusterTyped{}
	note := func(d, kind, net string, b layoutBBox) {
		detail[d] = append(detail[d], fmt.Sprintf("%-8s %-8s x=[%.0f,%.0f] y=[%.0f,%.0f]", kind, net, b.MinX, b.MaxX, b.MinY, b.MaxY))
		typed[d] = append(typed[d], schClusterTyped{Kind: kind, Net: net, BBox: b})
	}
	for d, b := range body {
		note(d, "part", d, b)
	}
	quant := func(x, y float64) [2]int64 {
		return [2]int64{int64(math.Round(x)), int64(math.Round(y))}
	}
	pinAt := map[[2]int64]string{}
	for _, p := range pins {
		pinAt[quant(p.x, p.y)] = p.owner
	}

	// ① 把导线按共享端点并成连通块(一根 marker 的桩线可能已被平台合并进长线)。
	uf := map[int]int{}
	var find func(int) int
	find = func(a int) int {
		if _, ok := uf[a]; !ok {
			uf[a] = a
		}
		for uf[a] != a {
			uf[a] = uf[uf[a]]
			a = uf[a]
		}
		return a
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			uf[ra] = rb
		}
	}
	at := map[[2]int64][]int{}
	wireBox := make([]layoutBBox, len(wires))
	wireBoxValid := make([]bool, len(wires))
	wireMembers := make([][]schClusterTyped, len(wires))
	for wi, w := range wires {
		if len(w.Points) < 4 {
			continue
		}
		find(wi)
		// Collision members must preserve the observed flat-segment encoding.
		// One bbox for the whole L/polyline contains empty corners and caused the
		// dev.12 U2↔C7 false overlap. Box below remains the union envelope for
		// cluster occupancy/page bounds; Members receives each real segment.
		for _, seg := range schDesignatorWireSegments(w) {
			b, ok := schVisibleWireSegmentBBox(seg)
			if !ok {
				continue
			}
			wireMembers[wi] = append(wireMembers[wi], schClusterTyped{Kind: "wire", BBox: b, WireID: w.ID, Segment: &seg})
			rawBox := layoutBBox{
				MinX: math.Min(seg[0], seg[2]), MinY: math.Min(seg[1], seg[3]),
				MaxX: math.Max(seg[0], seg[2]), MaxY: math.Max(seg[1], seg[3]),
			}
			if !wireBoxValid[wi] {
				wireBox[wi], wireBoxValid[wi] = rawBox, true
			} else {
				wireBox[wi] = schUnionBBox(wireBox[wi], rawBox)
			}
		}
		for i := 0; i+1 < len(w.Points); i += 2 {
			k := quant(w.Points[i], w.Points[i+1])
			for _, other := range at[k] {
				union(wi, other)
			}
			at[k] = append(at[k], wi)
		}
	}
	// ② 每个导线连通块触到哪些器件。
	touch := map[int]map[string]bool{}
	for wi := range wires {
		if len(wires[wi].Points) < 4 || !wireBoxValid[wi] {
			continue
		}
		r := find(wi)
		if touch[r] == nil {
			touch[r] = map[string]bool{}
		}
		for i := 0; i+1 < len(wires[wi].Points); i += 2 {
			if o := pinAt[quant(wires[wi].Points[i], wires[wi].Points[i+1])]; o != "" {
				touch[r][o] = true
			}
		}
	}
	// 锚点 → 它所在的导线连通块(marker 顺着自己的桩线找宿主)。
	compAt := map[[2]int64]int{}
	for k, ws := range at {
		if len(ws) > 0 {
			compAt[k] = find(ws[0])
		}
	}
	// ③ 只沾一个器件的连通块 = 该组的桩线,计入体积。
	for wi := range wires {
		if len(wires[wi].Points) < 4 {
			continue
		}
		r := find(wi)
		if len(touch[r]) != 1 {
			continue // 跨组的走线通道,不属于任何一组
		}
		for o := range touch[r] {
			growBox(o, wireBox[wi])
			for _, member := range wireMembers[wi] {
				addMember(o, member.BBox)
				note(o, "wire", "", member.BBox)
				typed[o][len(typed[o])-1] = member
			}
			wireCount[o]++
		}
	}

	nearestPin := func(x, y float64, only map[string]bool) (string, float64) {
		best, bestD := "", math.Inf(1)
		for _, p := range pins {
			if only != nil && !only[p.owner] {
				continue
			}
			if d := math.Hypot(x-p.x, y-p.y); d < bestD {
				best, bestD = p.owner, d
			}
		}
		return best, bestD
	}
	unowned := 0
	for _, c := range comps {
		if !isSchMarker(c.ComponentType) || c.BBox == nil {
			continue
		}
		owner := ""
		if r, ok := compAt[quant(c.X, c.Y)]; ok {
			switch len(touch[r]) {
			case 1:
				for o := range touch[r] {
					owner = o
				}
			case 0: // 悬空的线:退回最近引脚
			default: // 跨组走线上的 marker:归给它物理上贴着的那只脚
				owner, _ = nearestPin(c.X, c.Y, touch[r])
			}
		}
		if owner == "" {
			o, d := nearestPin(c.X, c.Y, nil)
			if o == "" || d > 6*schStubLen {
				unowned++ // 既不沾线、离谁都远 —— 不硬塞,塞错了体积就是假的
				continue
			}
			owner = o
		}
		jb := markerJudgeBBox(c)
		grow(owner, jb)
		note(owner, c.ComponentType, c.Net, jb)
		markers[owner]++
	}

	out := make([]schCluster, 0, len(order))
	for _, d := range order {
		out = append(out, schCluster{
			Designator: d, PrimitiveID: idOf[d], Device: devOf[d],
			Body: body[d], Box: box[d], Markers: markers[d], Wires: wireCount[d],
			Members: members[d], Detail: detail[d], Typed: typed[d],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Box.MinX != out[j].Box.MinX {
			return out[i].Box.MinX < out[j].Box.MinX
		}
		return out[i].Designator < out[j].Designator
	})
	return out, unowned
}

// schVisibleWireSegmentBBox turns one real orthogonal wire segment into a
// positive-area member box. The epsilon is only a topological stroke: it lets a
// centreline that enters another body count as an intersection without turning
// the empty corner of a multi-segment polyline into occupied area. Planner and
// live cluster validation share this visible-stroke model.
func schVisibleWireSegmentBBox(seg [4]float64) (layoutBBox, bool) {
	x0, y0, x1, y1 := seg[0], seg[1], seg[2], seg[3]
	if math.Hypot(x1-x0, y1-y0) <= acOverlapEps {
		return layoutBBox{}, false
	}
	return layoutBBox{
		MinX: math.Min(x0, x1) - schVisibleWireHalfWidth,
		MinY: math.Min(y0, y1) - schVisibleWireHalfWidth,
		MaxX: math.Max(x0, x1) + schVisibleWireHalfWidth,
		MaxY: math.Max(y0, y1) + schVisibleWireHalfWidth,
	}, true
}

// membersOf 退化保护:老调用方(或手搓的 fixture)没填 Members 时用包络顶上。
func membersOf(c schCluster) []layoutBBox {
	if len(c.Members) > 0 {
		return c.Members
	}
	return []layoutBBox{c.Box}
}

// boxesIntersect / boxGapAlongAxes 是包络快筛用的两把小尺。
func boxesIntersect(a, b layoutBBox) bool {
	return math.Min(a.MaxX, b.MaxX)-math.Max(a.MinX, b.MinX) > 0 &&
		math.Min(a.MaxY, b.MaxY)-math.Max(a.MinY, b.MinY) > 0
}

func boxGapAlongAxes(a, b layoutBBox) float64 {
	ox := math.Min(a.MaxX, b.MaxX) - math.Max(a.MinX, b.MinX)
	oy := math.Min(a.MaxY, b.MaxY) - math.Max(a.MinY, b.MinY)
	return math.Max(-ox, -oy)
}

// judgeSchClusters 出判定:组间重叠(ERROR)、组出图纸(ERROR)、组间过近(WARN)。
// schSameGroupFn 判两个位号是不是同一个**功能子群**(L2 虚拟组)的成员。
// nil = 不知道分组,一律当成不同组(保持旧行为)。
type schSameGroupFn func(a, b string) bool

// judgeSchClusters 判 L1 组之间的重叠与过近。
//
// **tight 对同组成员豁免**(2026-08-16 E2E #2):`sch block-apply` 落块时按功能
// 子群归组,而「去耦紧贴电源脚」「上下拉紧贴 strapping 脚」正是设计要求 ——
// 实测 P3 报的 6 处过近里有 5 处是块内的 attach 件(D1↔U3、U3↔C8、Q1↔R5…),
// 那不是缺陷,是电路该有的样子;真正该报的只有跨块那一处(Q2↔R3)。
//
// 求解器那边是同一笔账的另一半:它件间只按本体判、marker 之间靠 lane 排布错开
// (见 bslPartBox 的注释),所以组体积贴在一起本就在预期内。判据要是照旧把它们
// 全报出来,6 条里 5 条是噪音 —— 而噪音会让人把整条规则关掉。
//
// **重叠(ERROR)不豁免**:同组也不许压在一起,那是真几何缺陷。
func judgeSchClustersWith(cs []schCluster, usable *layoutBBox, minGap float64, sameGroup schSameGroupFn) []schClusterFinding {
	return judgeSchClustersWithCrossings(cs, usable, minGap, sameGroup, nil)
}

func judgeSchClustersWithCrossings(cs []schCluster, usable *layoutBBox, minGap float64, sameGroup schSameGroupFn, proof *schguard.WireCrossingProof) []schClusterFinding {
	var out []schClusterFinding
	for i := 0; i < len(cs); i++ {
		for j := i + 1; j < len(cs); j++ {
			// 先用包络快筛(包络不相交,成员一定不相交),再逐图元判 —— 判定必须落在
			// 真实图元上,包络之间的空白不是碰撞。
			ox, oy, hit := 0.0, 0.0, false
			gap := math.Inf(1)
			ea, eb := cs[i].Box, cs[j].Box
			envelopeGap := boxGapAlongAxes(ea, eb)
			if envelopeGap < minGap || envelopeGap <= schVisibleWireHalfWidth || boxesIntersect(ea, eb) {
				for ai, a := range membersOf(cs[i]) {
					for bi, b := range membersOf(cs[j]) {
						if schClusterMembersProvedCrossing(cs[i], ai, cs[j], bi, proof) {
							continue
						}
						x := math.Min(a.MaxX, b.MaxX) - math.Max(a.MinX, b.MinX)
						y := math.Min(a.MaxY, b.MaxY) - math.Max(a.MinY, b.MinY)
						if x > 0 && y > 0 {
							if !hit || x*y > ox*oy {
								ox, oy, hit = x, y, true
							}
							continue
						}
						if g := math.Max(-x, -y); g < gap {
							gap = g
						}
					}
				}
			}
			if hit {
				out = append(out, schClusterFinding{Type: "overlap", Level: "ERROR",
					A: cs[i].Designator, B: cs[j].Designator, OvX: ox, OvY: oy})
				continue
			}
			if minGap > 0 && gap < minGap {
				if sameGroup != nil && sameGroup(cs[i].Designator, cs[j].Designator) {
					continue // 同一功能子群内紧贴是设计要求,不是缺陷
				}
				if gap == 0 {
					gap = 0 // 抹掉 −0,别让"贴着"打印成 "-0"
				}
				out = append(out, schClusterFinding{Type: "tight", Level: "WARN",
					A: cs[i].Designator, B: cs[j].Designator, Gap: gap})
			}
		}
	}
	if usable != nil {
		for _, c := range cs {
			var why []string
			if c.Box.MinX < usable.MinX {
				why = append(why, fmt.Sprintf("左沿 %.0f < %.0f", c.Box.MinX, usable.MinX))
			}
			if c.Box.MaxX > usable.MaxX {
				why = append(why, fmt.Sprintf("右沿 %.0f > %.0f", c.Box.MaxX, usable.MaxX))
			}
			if c.Box.MinY < usable.MinY {
				why = append(why, fmt.Sprintf("下沿 %.0f < %.0f", c.Box.MinY, usable.MinY))
			}
			if c.Box.MaxY > usable.MaxY {
				why = append(why, fmt.Sprintf("上沿 %.0f > %.0f", c.Box.MaxY, usable.MaxY))
			}
			if len(why) > 0 {
				out = append(out, schClusterFinding{Type: "out-of-sheet", Level: "ERROR",
					A: c.Designator, Note: strings.Join(why, "、")})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level < out[j].Level }) // ERROR 在前
	return out
}

// runSchClusters 读实测几何 → 建组 → 判定 → 打印。只读,不改画布。
func runSchClusters(cfg *appConfig, window string, minGap float64, asJSON, strict, showMembers bool,
	stdout, stderr io.Writer) error {

	res, err := requestAction(cfg, "schematic.components.list", window,
		map[string]any{"includeBBox": true, "includePins": true, "includeWires": true})
	if err != nil {
		return fmt.Errorf("read components with real bbox/pin geometry: %w", err)
	}
	comps, err := parseLayoutComps(res.Result)
	if err != nil {
		return fmt.Errorf("parse components: %w", err)
	}
	wires, werr := schClusterSnapshotWires(res.Result)
	if werr != nil && strict {
		return fmt.Errorf("strict clusters require wire geometry from the same complete snapshot: %w", werr)
	}
	if werr != nil {
		wires, werr = fetchSchWirePolylines(cfg, window, "")
	}
	if werr != nil {
		fmt.Fprintf(stderr, "warn: 读不到导线(%v)—— 桩线不计入组体积,marker 仍按最近引脚归属\n", werr)
	}
	clusters, unowned := buildSchClusters(comps, wires)
	var usable, keepout *layoutBBox
	if sheet := sheetBBoxOf(comps); sheet != nil {
		u := schUsableArea(*sheet)
		usable = &u
		keepout, _ = titleBlockKeepout(sheet)
	}
	// 逐组的装配判决:哪几个组**本身就比这一页大**。
	//
	// 为什么在 clusters 这条命令上做:out-of-sheet 只说「探出图纸可用区」,读起来
	// 像"挪一挪";而 #181 第三份复盘那 8+ 轮里,真正的病是**挪不动**。这条命令是
	// page-too-small 建议里指过来的那一条(「看组高 vs 本体高」),它必须能自己
	// 回答"到底是摆得不好还是根本装不下"。
	oversized := schClustersTooBig(clusters, usable, keepout)
	// 带上功能子群信息:块内「去耦贴电源脚」这类紧贴是设计要求,不该报 tight。
	var same schSameGroupFn
	if _, _, docUUID, _, st, _, gerr := loadSchGroupsContext(cfg, window); gerr == nil {
		same = schSameLayoutOwnerFromState(st, docUUID)
	}
	proof, _ := schguard.VerifiedWireCrossings(res.Result)
	findings := judgeSchClustersWithCrossings(clusters, usable, minGap, same, proof)
	report := schClusterReport{Clusters: clusters, Findings: findings, Sheet: usable, Unowned: unowned,
		TooBig: oversized}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "clusters — %d 个虚拟组(器件 + 它自己的 marker/桩线;跨器件的连线不计入体积)\n",
			len(clusters))
		for _, c := range clusters {
			fmt.Fprintf(stdout, "  %-6s 体积 x=[%.0f,%.0f] y=[%.0f,%.0f]  %.0f×%.0f   本体 %.0f×%.0f   marker %d / 桩线 %d\n",
				c.Designator, c.Box.MinX, c.Box.MaxX, c.Box.MinY, c.Box.MaxY,
				c.Box.MaxX-c.Box.MinX, c.Box.MaxY-c.Box.MinY,
				c.Body.MaxX-c.Body.MinX, c.Body.MaxY-c.Body.MinY, c.Markers, c.Wires)
			if showMembers {
				for _, m := range c.Detail {
					fmt.Fprintf(stdout, "         %s\n", m)
				}
			}
		}
		if unowned > 0 {
			fmt.Fprintf(stdout, "  note: %d 支 marker 既不沾任何导线、离谁都远,未计入任何组\n", unowned)
		}
		for _, f := range findings {
			switch f.Type {
			case "overlap":
				fmt.Fprintf(stdout, "  ERROR  overlap       %s ↔ %s   重叠 %.0f×%.0f\n", f.A, f.B, f.OvX, f.OvY)
			case "out-of-sheet":
				fmt.Fprintf(stdout, "  ERROR  out-of-sheet  %s   %s\n", f.A, f.Note)
			case "tight":
				fmt.Fprintf(stdout, "  WARN   tight         %s ↔ %s   间隙 %.0f < %.0f\n", f.A, f.B, f.Gap, minGap)
			}
		}
		for _, f := range oversized {
			// 与 out-of-sheet 分开成一行、措辞完全不同:那一条是"挪一挪",这一条是
			// "挪不动"。混在一起就等于没报。
			fmt.Fprintf(stdout, "  BLOCKED page-too-small  %s\n", f.Advice)
		}
	}

	errs, warns := 0, 0
	for _, f := range findings {
		if f.Level == "ERROR" {
			errs++
		} else {
			warns++
		}
	}
	if !asJSON {
		if errs == 0 && (warns == 0 || !strict) {
			fmt.Fprintf(stdout, "✓ %d 个组:0 重叠 / 0 出图纸%s\n", len(clusters),
				map[bool]string{true: fmt.Sprintf(",%d 处过近", warns), false: ""}[warns > 0])
		} else {
			fmt.Fprintf(stdout, "✗ %d 处 ERROR / %d 处 WARN\n", errs, warns)
		}
	}
	if errs > 0 || (strict && warns > 0) {
		return fmt.Errorf("cluster check failed: %d error(s), %d warning(s)", errs, warns)
	}
	return nil
}

// newSchClustersCmd 注册 `sch clusters`。
func newSchClustersCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var minGap float64
	var asJSON, strict, showMembers bool
	c := &cobra.Command{
		Use:   "clusters",
		Short: "列出 L1 虚拟组(器件 + 它自己的 marker/桩线)并判组间重叠 / 出图纸",
		Long: `列出这一页的 L1 虚拟组,并按「组的体积 = 全部元素的并集,组间不许重叠」判定。

**虚拟组 = 一个器件 + 只挂在它自己引脚上的 marker / 桩线 / 文字。**
跨器件的连线不计入任何一组的体积 —— 它是两组之间的走线通道,本来就该穿过空白。

为什么需要它:` + "`sch layout-lint`" + ` 默认只看器件**本体**(netflag/netport 等非 part
图元全部排除),于是一张 marker 互相压、去耦被标签罩住、簇左沿探出图纸的页,它照样
报 0 overlap。判据看不见的问题,改进也无法验收。

判定:
  • overlap       两个组的体积相交                   → ERROR
  • out-of-sheet  组的体积探出图纸可用区(图框内缩)  → ERROR
  • tight         组间间隙 < --min-gap               → WARN

有 ERROR 时非零退出,可以直接当门禁;--strict 连 WARN 一起算失败。`,
		Args: cobra.NoArgs,
		Example: `  easyeda sch clusters
  easyeda sch clusters --strict
  easyeda sch clusters --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchClusters(cfg, *window, minGap, asJSON, strict, showMembers, stdout, stderr)
		},
	}
	c.Flags().Float64Var(&minGap, "min-gap", 20, "组与组之间的最小间隙(原理图单位;默认 bslPartGap=20)")
	c.Flags().BoolVar(&asJSON, "json", false, "以 JSON 输出")
	c.Flags().BoolVar(&strict, "strict", false, "过近(WARN)也算失败")
	c.Flags().BoolVar(&showMembers, "members", false, "逐条列出每个组的成员图元(类型/网名/bbox)")
	return c
}

// bapReportClusters 在 block-apply 收尾处做一次虚拟组体检(L1 口径:器件 + 它自己的
// marker/桩线)。**只报不拦**:器件与连线此刻都已落地,版面问题是可后修的,把它变成
// 整单失败反而会诱导重跑一遍(而 apply 不幂等)。但它必须出现 —— 这一类问题
// `layout-lint` 默认根本看不见(非 part 图元全被排除),不报就是假绿。
func bapReportClusters(cfg *appConfig, window string, man *bapManifest, stderr io.Writer) *schPageFit {
	res, err := requestAction(cfg, "schematic.components.list", window,
		map[string]any{"includeBBox": true, "includePins": true})
	if err != nil {
		fmt.Fprintf(stderr, "warn: 虚拟组体检读不到几何(%v)—— 请手动跑 `easyeda sch clusters`\n", err)
		return nil
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		fmt.Fprintf(stderr, "warn: 虚拟组体检解析失败(%v)—— 请手动跑 `easyeda sch clusters`\n", perr)
		return nil
	}
	wires, _ := fetchSchWirePolylines(cfg, window, "") // 读不到就只按 marker 归属算
	clusters, _ := buildSchClusters(comps, wires)
	var usable *layoutBBox
	var keepout *layoutBBox
	sheet := sheetBBoxOf(comps)
	if sheet != nil {
		u := schUsableArea(*sheet)
		usable = &u
		// 图签 keep-out 传**未膨胀**的:膨胀带(titleBlockSafety)是分区框的区名带/
		// 说明带留白,虚拟组不带任何带 —— 用它会凭空严格一档,把"挪一挪能解"误判成
		// "这一页放不下"(那正是本判据要防的反向错误)。
		keepout, _ = titleBlockKeepout(sheet)
	}
	// minGap 0:这一步只报硬伤(重叠 / 出图纸),过近留给 `sch clusters --strict`。
	findings := judgeSchClusters(clusters, usable, 0)
	// **本次落的这个块,整组量下来到底装不装得进这一页** —— 这一句是 #181 第三份
	// 复盘的最大卡点(legacy 块真实高 700–840,反复 8+ 轮手工收敛)缺的那句话。
	// 此前这里最多报「探出图纸可用区」,读起来像"挪一挪",于是人就真的去挪了 8 轮;
	// 而高度比整页可用高还大时,**挪多少次都没用**。判定用实测 bbox(铁律)。
	fit := bapBlockPageFit(clusters, man, usable, keepout)
	if fit != nil && fit.TooBig() {
		man.Warnings = append(man.Warnings, fit.Advice)
		fmt.Fprintf(stderr, "clusters ✗ %s\n", fit.Advice)
	}
	if len(findings) == 0 {
		fmt.Fprintf(stderr, "clusters ✓ %d 个虚拟组:0 重叠 / 0 出图纸(器件 + 它自己的 marker/桩线)\n", len(clusters))
		return fit
	}
	for _, f := range findings {
		var w string
		switch f.Type {
		case "overlap":
			w = fmt.Sprintf("虚拟组 %s ↔ %s 的图元重叠 %.0f×%.0f —— 两组各自的 marker/桩线压在一起了",
				f.A, f.B, f.OvX, f.OvY)
		case "out-of-sheet":
			w = fmt.Sprintf("虚拟组 %s 探出图纸可用区(%s)—— 器件本体可能还在框内,但它的 marker 印不出来", f.A, f.Note)
		default:
			continue
		}
		man.Warnings = append(man.Warnings, w)
		fmt.Fprintf(stderr, "clusters ✗ %s\n", w)
	}
	fmt.Fprintf(stderr, "clusters: %d 个组、%d 处硬伤 —— 详情与门禁跑 `easyeda sch clusters --strict`\n",
		len(clusters), len(findings))
	return fit
}

// bapBlockPageFit 量**本次落的这个块**在这一页的处境。
//
// 口径:取本实例全部位号所触及的虚拟组(cluster = 器件 ∪ 它自己的 marker/桩线)的
// **并集框** —— 这才是这块真正占的地。只看单个器件 bbox 会系统性低估:复盘里那些
// 700–840 高的块,一半的高是竖排 marker 撑出来的。
//
// 返回 nil 的两种情形都必须是"不下结论",而不是"没问题":页面没有图框(量不出
// 可用区),或本实例的位号一个都没匹配上 cluster(读回来的几何与 manifest 对不上)。
// 装配判据宁可沉默也不许猜 —— 猜出来的 fits 会直接掩盖掉这次要报的那句话。
func bapBlockPageFit(clusters []schCluster, man *bapManifest, usable, keepout *layoutBBox) *schPageFit {
	if usable == nil || man == nil {
		return nil
	}
	mine := map[string]bool{}
	for _, p := range man.Placed {
		if d := strings.TrimSpace(p.Designator); d != "" {
			mine[strings.ToUpper(d)] = true
		}
	}
	if len(mine) == 0 {
		return nil
	}
	var box layoutBBox
	first := true
	for _, c := range clusters {
		if !mine[strings.ToUpper(strings.TrimSpace(c.Designator))] {
			continue
		}
		// c.Box 是「本体 ∪ 归属 marker ∪ 归属桩线」—— 组真正占的地,不是器件 bbox。
		if first {
			box, first = c.Box, false
			continue
		}
		box = schUnionBBox(box, c.Box)
	}
	if first {
		return nil
	}
	name := strings.TrimSpace(man.GroupName)
	if name == "" {
		name = strings.TrimPrefix(strings.TrimSpace(man.BlockID), "block.")
	}
	f := judgeSchPageFit(name, box, *usable, keepout)
	return &f
}

// schUnionBBox 是两框的包络。
func schUnionBBox(a, b layoutBBox) layoutBBox {
	return layoutBBox{
		MinX: math.Min(a.MinX, b.MinX), MinY: math.Min(a.MinY, b.MinY),
		MaxX: math.Max(a.MaxX, b.MaxX), MaxY: math.Max(a.MaxY, b.MaxY),
	}
}

// judgeSchClusters 是不带分组信息的旧签名(纯几何,同组不豁免)。
func judgeSchClusters(cs []schCluster, usable *layoutBBox, minGap float64) []schClusterFinding {
	return judgeSchClustersWith(cs, usable, minGap, nil)
}
