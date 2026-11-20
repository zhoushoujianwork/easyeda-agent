package app

// cmd_sch_status.go — `easyeda sch status`:原理图侧的**进度权威**,S1–S6 逐条从
// 活体推导。
//
// 立项背景(2026-08-16,esp32Mini E2E 复盘):`workflow status` 把 imported 与
// placement_ready 双双打成实心圆,而那块 PCB 上**一个器件都没有** —— 它记的是
// 「某个动作被调用过」,不是「结果还在画布上」。活体复验的能力其实早就有
// (reconcileWorkflow),却藏在可选的 --reconcile 后面,于是默认输出连 next 都是错的
// (workflowNext 的 `f.Reachable && f.Components == 0` 分支在无事实时整条被跳过,
// 空板照样建议 layout-lint)。四页全绿的 `sch gate` 加两个实心圆,读起来像「快完事了」。
//
// 原理图侧有个结构性优势,让这里可以做得比 PCB 侧更彻底:**S1–S6 全部是机械可判的**
// (页/图纸/组/框/器件/导线都能读回来),不像 PCB 的 placement_confirmed 是人的签字、
// 无法从画布推导。所以本命令**不持久化任何东西** —— 没有记录,就没有可撒的谎。
//
// 三条铁律:
//  1. **判不了就说判不了**(state=unknown / `?`),绝不默认打勾。S6 存盘正是这一类:
//     平台不暴露脏标记,那就写明白,而不是拿「跑过 save 命令」冒充。
//  2. **status 报进度,gate 报质量**。这里不跑五关(那要切页+DRC,慢且是另一件事),
//     S5 一律显示为「未验」并指向 `sch gate`;`--gate` 才真跑。
//  3. 判据与 `sch check` / `zone-plan` 共用同一批读取函数,不另立一套尺子。

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// schPageFacts 是一页的活体事实。全部只读;读不到的字段留零值并把 Err 填上,
// 绝不用零值冒充「没有」。
type schPageFacts struct {
	Name      string `json:"name"`
	DocUUID   string `json:"docUuid"`
	Reachable bool   `json:"reachable"` // 几何读到了没有(false 时下面的计数全部无意义)
	HasSheet  bool   `json:"hasSheet"`
	NamedWell bool   `json:"namedWell"` // 页名有功能语义
	Parts     int    `json:"parts"`
	Wires     int    `json:"wires"`
	Groups    int    `json:"groups"`
	Frames    int    `json:"frames"`

	Err string `json:"error,omitempty"`
	// WiresErr 单独记:导线读失败与「真的没有导线」是两件事,合成 0 就会把
	// 读取故障渲染成「还没连线」——本命令刚因为吞掉这个错误报过一次假缺陷。
	WiresErr string `json:"wiresError,omitempty"`
}

// schStageState 是一格的判定。**unknown 是一等公民**:它不是 todo 的委婉说法,
// 而是「这一步本工具判不了」——把它折成 ○ 或 ● 都是撒谎。
const (
	schStageDone    = "done"
	schStagePartial = "partial"
	schStageTodo    = "todo"
	schStageUnknown = "unknown"
)

func schStageMark(state string) string {
	switch state {
	case schStageDone:
		return "✓"
	case schStagePartial:
		return "◐"
	case schStageTodo:
		return "○"
	default:
		return "?"
	}
}

// schStageVerdict 是 S1–S6 中的一条。
type schStageVerdict struct {
	Stage  string `json:"stage"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// schPlaceholderPageName 判页名是不是平台默认/无语义的占位名。S1 要求
// 「页集合 = 模块计划」,`P1` / `Schematic1` / `page2` 这类名字等于没分页。
func schPlaceholderPageName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return true
	}
	// 去掉**尾部**页号再看剩下什么:"p1"→"p"、"schematic1"→"schematic"、"3"→"" 都是
	// 占位;"p1_power"→"p1_power"、"mcu"→"mcu" 带着功能语义,是好名字。
	base := strings.TrimRight(n, "0123456789")
	if base == "" {
		return true // 纯页号
	}
	for _, prefix := range []string{"p", "page", "sch", "schematic", "sheet", "doc", "untitled"} {
		if base == prefix {
			return true
		}
	}
	return false
}

// schGateSummary 是 --gate 真跑过之后的结果。**Fails 带原因**:只报「0/4 页通过」
// 等于让人再跑一遍才知道修什么 —— 判定必须自带可行动的归因(同 gate 报告本身的约定)。
type schGateSummary struct {
	Ran    bool     `json:"ran"`
	Passed int      `json:"passed"`
	Total  int      `json:"total"`
	Fails  []string `json:"fails,omitempty"` // "P1: clusters: 1 处组间过近 (--strict)"
}

// schStageVerdicts 是纯核:把一组页事实折成 S1–S6 的判定。无 I/O,可表驱动测试。
//
// gate 只在 --gate 真跑过时有意义;没跑就是 unknown —— 见铁律 1。
func schStageVerdicts(pages []schPageFacts, gate schGateSummary) []schStageVerdict {
	var reachable, sheeted, named, withGroups, withFrames, placed, wired int
	var parts, wires int
	var badNames []string
	for _, p := range pages {
		if !p.Reachable {
			continue
		}
		reachable++
		if p.HasSheet {
			sheeted++
		}
		if p.NamedWell {
			named++
		} else {
			badNames = append(badNames, p.Name)
		}
		if p.Groups > 0 {
			withGroups++
		}
		if p.Frames > 0 {
			withFrames++
		}
		if p.Parts > 0 {
			placed++
			parts += p.Parts
		}
		if p.Wires > 0 {
			wired++
			wires += p.Wires
		}
	}
	out := make([]schStageVerdict, 0, 6)

	// S1 图纸 / 分页
	s1 := schStageVerdict{Stage: "S1", Title: "图纸/分页"}
	switch {
	case reachable == 0:
		s1.State, s1.Detail = schStageUnknown, "读不到任何页的几何 —— 先 `easyeda health` / `doc switch`"
	case sheeted < reachable:
		s1.State = schStagePartial
		s1.Detail = fmt.Sprintf("%d/%d 页有可读图纸 —— 无 sheet 的页不能开始 place", sheeted, reachable)
	case len(badNames) > 0:
		s1.State = schStagePartial
		s1.Detail = fmt.Sprintf("%d/%d 页图纸就绪,但 %s 页名无功能语义(`sch page-rename`)",
			sheeted, reachable, strings.Join(quoteAll(badNames), "/"))
	default:
		s1.State = schStageDone
		s1.Detail = fmt.Sprintf("%d 页,页名皆有功能语义", reachable)
	}
	out = append(out, s1)

	// S2 编组 / 分区:模块框与标题,不要求独立 Notes。
	s2 := schStageVerdict{Stage: "S2", Title: "编组/分区"}
	switch {
	case withGroups == 0:
		s2.State, s2.Detail = schStageTodo, "没有虚拟组 —— `sch block-apply` 落块自动归组,手工页 `sch group create`"
	case withGroups < reachable || withFrames < withGroups:
		s2.State = schStagePartial
		s2.Detail = fmt.Sprintf("%d/%d 页有组、%d 页有分区框",
			withGroups, reachable, withFrames)
	default:
		s2.State = schStageDone
		s2.Detail = fmt.Sprintf("%d 页各有虚拟组 + 分区框", withGroups)
	}
	out = append(out, s2)

	// S3 摆放
	s3 := schStageVerdict{Stage: "S3", Title: "摆放"}
	switch {
	case placed == 0:
		s3.State, s3.Detail = schStageTodo, "画布上没有器件"
	case placed < reachable:
		s3.State = schStagePartial
		s3.Detail = fmt.Sprintf("%d 件落在 %d/%d 页 —— 有空页", parts, placed, reachable)
	default:
		s3.State, s3.Detail = schStageDone, fmt.Sprintf("%d 件已落位", parts)
	}
	out = append(out, s3)

	// S4 布线(只判「有没有线」;接得对不对是 S5 的事)
	s4 := schStageVerdict{Stage: "S4", Title: "布线"}
	var wireBlind []string
	for _, p := range pages {
		if p.Reachable && p.WiresErr != "" {
			wireBlind = append(wireBlind, p.Name)
		}
	}
	switch {
	case len(wireBlind) > 0:
		// 读不到导线 ≠ 没有导线。合成 0 会把读取故障报成「还没连线」——
		// 真机上正是这样把 3 页有线的工程判成「没连」。
		s4.State = schStageUnknown
		s4.Detail = fmt.Sprintf("%s 页的导线读不到 —— 判不了「连没连」,重跑或 `sch check` 复验",
			strings.Join(quoteAll(wireBlind), "/"))
	case placed == 0:
		s4.State, s4.Detail = schStageTodo, "还没放件"
	case wired == 0:
		s4.State, s4.Detail = schStageTodo, "没有导线 —— `sch autoconnect` / `sch block-apply`"
	case wired < placed:
		s4.State = schStagePartial
		s4.Detail = fmt.Sprintf("%d 段导线,但 %d/%d 有件的页还没连线", wires, placed-wired, placed)
	default:
		s4.State = schStageDone
		s4.Detail = fmt.Sprintf("%d 段导线(接得对不对由 S5 判)", wires)
	}
	out = append(out, s4)

	// S5 校验门 —— **本命令不判质量**。跑了才有话说,没跑就 unknown(铁律 2)。
	s5 := schStageVerdict{Stage: "S5", Title: "校验门"}
	switch {
	case !gate.Ran:
		s5.State = schStageUnknown
		s5.Detail = "未验 —— status 只报进度;逐页 `sch gate --strict --doc <页>`(或本命令加 --gate)"
	case gate.Total == 0:
		s5.State, s5.Detail = schStageUnknown, "没有页跑成 gate —— 判不了"
	case gate.Passed == gate.Total:
		s5.State, s5.Detail = schStageDone, fmt.Sprintf("%d/%d 页 gate 通过(--strict 档)", gate.Passed, gate.Total)
	default:
		s5.State = schStagePartial
		// 带上阻塞原因:strict 档比手跑的默认档严(实测默认档四页全 PASS、strict 下
		// clusters 的「组间过近」全数拦下),不写明原因会被当成本命令的 bug。
		s5.Detail = fmt.Sprintf("%d/%d 页过 --strict 档 —— %s",
			gate.Passed, gate.Total, strings.Join(gate.Fails, ";"))
	}
	out = append(out, s5)

	// S6 存盘 —— 平台不暴露脏标记,判不了就说判不了(铁律 1)。
	out = append(out, schStageVerdict{
		Stage: "S6", Title: "存盘",
		State:  schStageUnknown,
		Detail: "平台不暴露脏标记,无法从活体判定 —— daemon 防抖 autosave 已开(默认 3s),交付前显式 `sch save --doc <页>` 并确认 saved:true",
	})

	// **有页读不到,整张判定就是不完整的** —— 已达成的一律降级为 unknown。
	//
	// 这一条是真机当场打脸补上的:首跑时 4 页里 3 页切不过去(参数名写错),
	// 命令拿剩下那 1 页宣布「S1–S4 已就绪,下一步进 PCB」—— 与 `workflow status`
	// 拿记录冒充事实是同一种病,只是换了个地方犯。把读不到的页排除出分母,
	// 等于让**环境故障自动伪装成全绿**,而且页越读不到、结论越乐观。
	//
	// 语义同 `sch gate` 的第三态 blocked:检查器没跑完 ≠ 板子没问题。
	if unreachable := len(pages) - reachable; unreachable > 0 {
		for i := range out {
			if out[i].State != schStageDone {
				continue
			}
			out[i].State = schStageUnknown
			out[i].Detail = fmt.Sprintf("判定不完整:%d/%d 页读不到 —— 能读到的 %d 页:%s",
				unreachable, len(pages), reachable, out[i].Detail)
		}
	}
	return out
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// schStatusNext 给出唯一的下一步。判据顺序 = S1→S6,第一个没做完的就是下一步;
// 全做完了就指向 PCB 侧(P1)。
func schStatusNext(verdicts []schStageVerdict, pages []schPageFacts) (next, why string) {
	// 读不到的页优先于一切:进度无从谈起,先修环境。**别把它当成设计问题去改电路**
	// (同 gate 的 blocked 三态)。
	var unreadable []string
	for _, p := range pages {
		if !p.Reachable {
			unreadable = append(unreadable, p.Name)
		}
	}
	if len(unreadable) > 0 {
		return "easyeda health  →  easyeda doc switch <页>",
			fmt.Sprintf("%d 页读不到(%s)—— 判定不完整,先修环境;此时任何「已就绪」都不可信",
				len(unreadable), strings.Join(unreadable, "/"))
	}
	for _, v := range verdicts {
		switch {
		case v.State == schStageDone, v.State == schStageUnknown:
			continue // unknown 不阻断:它是「本工具判不了」,不是「没做」
		}
		switch v.Stage {
		case "S1":
			for _, p := range pages {
				if p.Reachable && !p.NamedWell {
					return fmt.Sprintf("easyeda sch page-rename --page %s --name <功能名>", p.DocUUID),
						fmt.Sprintf("S1 未完:%q 的页名要等于它的功能", p.Name)
				}
			}
			return "easyeda sch sheet-geometry --json", "S1 未完:" + v.Detail
		case "S2":
			// 空画布上没有框可画 —— 先落块(block-apply 同时完成 S2 归组 + S3 摆放
			// + S4 块内布线)。首跑真机时这里直接指 zone-plan,而画布上一个器件都
			// 没有:下一步必须是**当前状态下真能执行**的那条,否则它只是把流程图
			// 念了一遍。
			for _, p := range pages {
				if p.Reachable && p.Parts > 0 {
					return "easyeda sch zone-plan --json  →  easyeda sch zone-draw", "S2 未完:" + v.Detail
				}
			}
			return "easyeda blocks ls  →  easyeda sch block-apply <块> --instance <名>",
				"S2/S3 未开始:画布还是空的,先落块(block-apply 自动归组 + 摆放 + 块内布线)"
		case "S3":
			return "easyeda sch block-apply <块> --instance <名>", "S3 未完:" + v.Detail
		case "S4":
			return "easyeda sch autoconnect", "S4 未完:" + v.Detail
		case "S5":
			return "easyeda sch gate --strict --doc <页>", "S5 未过:" + v.Detail
		}
	}
	return "easyeda pcb import-changes", "原理图侧 S1–S4 已就绪(S5 请用 gate 验) —— 下一步进 PCB(P1)"
}

// collectSchPageFacts 拉一页的活体事实。几何只能读**激活页**,所以调用方负责切页;
// 组/框来自按 documentUuid 索引的持久状态,与激活页无关。
func collectSchPageFacts(cfg *appConfig, window, docUUID, name string, st *pcbStageState) schPageFacts {
	f := schPageFacts{Name: name, DocUUID: docUUID, NamedWell: !schPlaceholderPageName(name)}
	if st != nil {
		f.Groups = len(st.GroupsForPage(docUUID))
		if fr := st.SchZoneFrameIdsByPage[docUUID]; fr != nil {
			f.Frames = len(fr.Rects)
		}
		f.Frames += recordedModuleFrameCount(st, docUUID)
	}
	// includeWires 让导线跟几何**同一次调用、同一次页校验**回来。
	//
	// 原本走 fetchSchWirePolylines(debug.exec_js)读 polyline,但 exec_js 的响应
	// context 恒为进程启动时那一页 —— 切页后 components.list 的 context 已经是新页,
	// exec_js 的还是旧页,页漂移校验一票否决,三页导线全读不到。手工 `doc switch`
	// 看不出来,只因为两条命令之间隔了一次进程启动。status 只需要**根数**不需要
	// primitiveId,那就没必要走那条不可靠的路。(exec_js 的 context 一致性是连接器
	// 侧的问题,另计。)
	res, err := requestAutolayoutAction(cfg, "schematic.components.list", window,
		map[string]any{"includeBBox": true, "includeWires": true}, docUUID, "sch status: read page geometry")
	if err != nil {
		f.Err = err.Error()
		return f
	}
	comps, perr := parseLayoutComps(res.Result)
	if perr != nil {
		f.Err = perr.Error()
		return f
	}
	f.Reachable = true
	f.HasSheet = sheetBBoxOf(comps) != nil
	for _, c := range comps {
		if strings.EqualFold(c.ComponentType, "part") {
			f.Parts++
		}
	}
	f.Wires = len(buildWireSegments(res.Result))
	return f
}

// runSchStatus 是命令主体。默认只测**激活页**(快、不动前台);--all-pages 逐页切过去
// 读完再切回原页。
func runSchStatus(cfg *appConfig, window string, allPages, withGate, asJSON bool, stdout, stderr io.Writer) error {
	pinned, win, activeUUID, project, st, _, err := loadSchGroupsContext(cfg, window)
	if err != nil {
		return fmt.Errorf("sch status 需要一个已连接的原理图页: %w", err)
	}

	// openSchPage 切页,并**等页面数据沉降**。走 `document.open` 而不是
	// `schematic.page.open`:后者只打开页签,`debug.exec_js` 的上下文并不跟着走 ——
	// 实测 components.list 已经返回新页,而同一时刻 exec_js 的响应仍来自旧页
	// (page drift: response came from <旧页>),导线因此一根都读不到。
	// document.open + waitDocSettleFor 正是 `doc switch` 的做法,顺带避开懒加载
	// (issue #67:open 一返回页签就存在,但图元/网表还没加载完)。
	openSchPage := func(uuid string) error {
		if _, err := requestAction(pinned, "document.open", win, map[string]any{"uuid": uuid}); err != nil {
			return err
		}
		// **等窗口上下文自己承认切过去了**,再开始读。
		//
		// document.open 一返回,components.list 已经给出新页的数据,但连接器推给 daemon
		// 的窗口上下文还停在旧页 —— 于是 debug.exec_js 的响应带着旧 documentUuid,
		// 页漂移校验一票否决,导线一根都读不到(真机:三页全报 page drift,想当然地
		// 被 S4 渲染成「还没连线」)。手工 `doc switch` 之所以没事,只是因为两条命令
		// 之间隔着一次进程启动,上下文正好刷新完了 —— 时序上的巧合,不是保证。
		for i := 0; i < 15; i++ {
			cur, err := requestAction(pinned, "document.current", win, nil)
			if err == nil && cur != nil && cur.Context != nil && cur.Context.DocumentUUID == uuid {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		waitDocSettleFor(pinned, win, "schematic")
		return nil
	}

	type pageRef struct{ uuid, name string }
	refs := []pageRef{{activeUUID, ""}}
	if allPages {
		res, perr := requestAction(pinned, "schematic.pages.list", win, nil)
		if perr != nil {
			return fmt.Errorf("读不到页列表: %w", perr)
		}
		refs = refs[:0]
		if arr, ok := res.Result["pages"].([]any); ok {
			for _, it := range arr {
				m, _ := it.(map[string]any)
				if m == nil {
					continue
				}
				if u := asString(m["uuid"]); u != "" {
					refs = append(refs, pageRef{u, asString(m["name"])})
				}
			}
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].name < refs[j].name })
	}
	if len(refs) == 0 {
		return fmt.Errorf("工程 %q 没有可读的原理图页", stageProjectLabel(project))
	}

	var pages []schPageFacts
	for _, r := range refs {
		// **每页都切,不为「它就是初始激活页」省这一次**:循环到它时前台早已停在
		// 上一页,跳过切页读到的就是别人的数据(真机:P2_MCU 收到 P1_POWER 的几何,
		// 靠页漂移校验才没被当成自己的)。省一次切页换来一次串页,不划算。
		{
			if oerr := openSchPage(r.uuid); oerr != nil {
				pages = append(pages, schPageFacts{Name: r.name, DocUUID: r.uuid,
					Err: "切不过去: " + oerr.Error(), NamedWell: !schPlaceholderPageName(r.name)})
				continue
			}
		}
		name := r.name
		if name == "" {
			name = r.uuid
		}
		pages = append(pages, collectSchPageFacts(pinned, win, r.uuid, name, st))
	}
	// 读完切回来:status 是只读命令,不该改用户的前台页。
	if allPages && len(refs) > 1 {
		_ = openSchPage(activeUUID)
	}

	gate := schGateSummary{}
	if withGate {
		gate.Ran = true
		for _, p := range pages {
			if !p.Reachable {
				continue
			}
			gate.Total++
			if oerr := openSchPage(p.DocUUID); oerr != nil {
				gate.Total--
				fmt.Fprintf(stderr, "note: 切不到 %s 页(%v)—— 该页不计入 S5\n", p.Name, oerr)
				continue
			}
			// 复用 gate 的同一条管线,并且**只认 verdict**:blocked(检查器没跑起来)
			// 绝不能算通过,也不该被折成 fail —— 那正是 gate 三态存在的理由。
			rep, gerr := collectSchGate(pinned, win, false, true, false, "", "", p.DocUUID,
				gateDefaultMinGap, gateDefaultPinEps, gateDefaultOverlapEps, stderr)
			if gerr != nil || rep == nil {
				fmt.Fprintf(stderr, "note: %s 页的 gate 没能跑起来(%v)—— 该页不计入 S5\n", p.Name, gerr)
				gate.Total--
				continue
			}
			if rep.Verdict == "pass" {
				gate.Passed++
				continue
			}
			why := strings.Join(rep.Blockers, "; ")
			if why == "" {
				why = rep.Verdict
			}
			gate.Fails = append(gate.Fails, fmt.Sprintf("%s: %s", p.Name, why))
		}
		_ = openSchPage(activeUUID)
	}

	verdicts := schStageVerdicts(pages, gate)
	next, why := schStatusNext(verdicts, pages)

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"project": stageProjectLabel(project), "pages": pages,
			"stages": verdicts, "gate": gate, "next": next, "nextReason": why,
		})
	}

	scope := "激活页"
	if allPages {
		scope = fmt.Sprintf("%d 页", len(pages))
	}
	fmt.Fprintf(stdout, "sch status — 工程 %q · %s · **全部实测自活体,不落盘、不会过期**\n\n",
		stageProjectLabel(project), scope)
	fmt.Fprintf(stdout, "  %-18s %-6s %-6s %-6s %-6s %-6s %s\n", "页", "图纸", "页名", "组", "框", "器件", "导线")
	for _, p := range pages {
		if !p.Reachable {
			fmt.Fprintf(stdout, "  %-18s  读不到 —— %s\n", truncPageName(p.Name), p.Err)
			continue
		}
		fmt.Fprintf(stdout, "  %-18s %-6s %-6s %-6d %-6d %-6d %d\n",
			truncPageName(p.Name), boolMark(p.HasSheet), boolMark(p.NamedWell),
			p.Groups, p.Frames, p.Parts, p.Wires)
	}
	fmt.Fprintln(stdout)
	for _, v := range verdicts {
		fmt.Fprintf(stdout, "  %s %-3s %-10s %s\n", schStageMark(v.State), v.Stage, v.Title, v.Detail)
	}
	fmt.Fprintf(stdout, "\nnext: %s\n  (%s)\n", next, why)
	return nil
}

func boolMark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

// truncPageName 按 **rune** 截断 —— 页名和台账标签都可能是中文,按字节切会把
// 一个汉字劈成半个,渲染出 `原理�…`(cost ledger 首跑实见)。
func truncPageName(s string) string {
	r := []rune(s)
	if len(r) > 18 {
		return string(r[:17]) + "…"
	}
	return s
}

// newSchStatusCmd 注册 `sch status`。
func newSchStatusCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var allPages, withGate, asJSON bool
	c := &cobra.Command{
		Use:   "status",
		Short: "原理图进度(S1–S6):逐条从活体推导,不落盘,判不了的明说",
		Long: `原理图侧的进度权威 —— S1–S6 每一格都**当场从画布算出来**。

**为什么不落盘**:` + "`workflow status`" + ` 记的是「某个动作被调用过」,不是「结果还在
画布上」——实测它把 imported / placement_ready 打成实心圆,而那块 PCB 上一个器件
都没有。原理图侧的 S1–S6 全部机械可判(页/图纸/组/框/器件/导线都读得回来),
所以这里干脆不存任何状态:**没有记录,就没有可撒的谎**。

四种状态:
  ✓ done     判据满足
  ◐ partial  部分页满足(明细写在这一行)
  ○ todo     没做
  ? unknown  **本工具判不了** —— 不是委婉的「没做」,更不会替它打勾

判不了的两格是有意留白的:
  • S5 校验门 —— status 只报进度,不报质量。跑 ` + "`sch gate --strict --doc <页>`" + `
    (或本命令加 --gate 逐页跑)。
  • S6 存盘   —— 平台不暴露脏标记。daemon 有防抖 autosave,但那是安全网,
    交付前仍要显式 ` + "`sch save`" + ` 并确认 saved:true。

默认只测激活页(快、不动前台);--all-pages 逐页切过去读完再切回原页。`,
		Args: cobra.NoArgs,
		Example: `  easyeda sch status --project ceshi
  easyeda sch status --project ceshi --all-pages
  easyeda sch status --project ceshi --all-pages --gate`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchStatus(cfg, *window, allPages, withGate, asJSON, stdout, stderr)
		},
	}
	c.Flags().BoolVar(&allPages, "all-pages", false, "逐页测(切页读完再切回),默认只测激活页")
	c.Flags().BoolVar(&withGate, "gate", false, "顺带逐页跑 `sch gate` 填上 S5(慢:含 DRC,需要前台)")
	c.Flags().BoolVar(&asJSON, "json", false, "以 JSON 输出")
	return c
}
