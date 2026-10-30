package app

// Functional-frame evidence uses canvas titles and local frame records. Neither
// grouped parts nor arbitrary free text exempts a page from requiring frames.

import "strings"

// missingDeliverableHints gives a remedy for the observed type, in frame/titleblock order.
func missingDeliverableHints(findings []checkFinding) []string {
	seen := map[string]bool{}
	for _, f := range findings {
		seen[f.Type] = true
	}
	var out []string
	if seen["missing-partition"] {
		out = append(out, "→ missing-partition: 多器件页没画功能分区框(铁律#15) — `sch zones set`→`sch zone-draw`(整纸版式 --mode partition);"+
			"若 `sch zone-plan` 报 partitionOverlap,先 `sch zone-arrange --apply` 或拆页,再画")
	}
	if seen["missing-titleblock"] {
		// 文案与 titleBlockFinding 的明细逐字同源,免得提示行和明细行教两套写法。
		out = append(out, "→ missing-titleblock: 图签必填项空着,交付图必须能认领 — `sch titleblock --data '{\"Name\":\"…\",\"Drawed\":\"…\",\"Description\":\"…\"}'`")
	}
	if seen["hidden-titleblock"] {
		out = append(out, "→ hidden-titleblock: 必填图签需显示值；不能隐藏来规避越界。恢复 showValue:true 后，用 `sch attribute-move` 的 ownerType:sheet 位置修复，再保存重载回读")
	}
	return out
}

// schZoneTitleSep 是区标题里多个模块名之间的分隔符。zone-draw 的两条画框路径
// 都用 `strings.Join(p.Modules, " / ")`;这里把那个字面量收成一个名字,check
// 侧靠它反认。(两条画框路径在 cmd_sch_zone_* 里,本次不许改动,所以由
// TestSchZoneTitle_MatchesDrawnTitle 钉住「两边一个字不差」。)
const schZoneTitleSep = " / "

// schZoneTitleContent 生成一个分区框的标题文本 —— 与 zone-draw 落到画布上的内容
// 逐字相同。
func schZoneTitleContent(modules []string) string {
	return strings.Join(modules, schZoneTitleSep)
}

// schZoneNameKey 归一化一个区名用于比较:去空白 + 折大小写。区名来自虚拟组名末段
// 或认领表的 key,写进画布时原样;折大小写只是吸收人手敲认领时的大小写差异。
func schZoneNameKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// isSchZoneTitleText 判一条画布文本**是不是**本页某个分区框的标题:按 " / " 拆开
// 之后,每一段都必须是本页登记过的模块名(空段直接否决)。
//
// 为什么要求「每一段都是」而不是「含有」:电路说明里出现模块名是常事,而一条
// 内容恰好等于「模块名」或「模块名 / 模块名」的自由文本,只有 zone-draw 会写。
func isSchZoneTitleText(content string, zoneNames map[string]bool) bool {
	if len(zoneNames) == 0 {
		return false
	}
	segs := strings.Split(content, schZoneTitleSep)
	if len(segs) == 0 {
		return false
	}
	for _, s := range segs {
		key := schZoneNameKey(s)
		if key == "" || !zoneNames[key] {
			return false
		}
	}
	return true
}

// schZoneNameSet 把本页模块名列表折成查找集。
func schZoneNameSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		if k := schZoneNameKey(n); k != "" {
			out[k] = true
		}
	}
	return out
}

// countDrawnZoneTitles 数画布上有几条区标题 —— 即「这页实际画出了几个分区框」。
// 同一条内容出现多次照数多次:zone-draw 每个区写一条,重复内容意味着重复的框。
func countDrawnZoneTitles(texts []zoneMoveText, zoneNames []string) int {
	set := schZoneNameSet(zoneNames)
	if len(set) == 0 {
		return 0
	}
	n := 0
	for _, t := range texts {
		if isSchZoneTitleText(t.Content, set) {
			n++
		}
	}
	return n
}

// schPartitionEvidence 是一页「分区做到哪一步」的全部证据,两个证人分开留痕,
// 便于报文说清楚是谁作的证。
type schPartitionEvidence struct {
	// RecordedRects / RecordedLabels 来自本地记账(zone-draw 写、recordedZoneFrames 读)。
	RecordedRects  int
	RecordedLabels int
	// DrawnTitles 来自画布(text.list 里认出来的区标题数)。
	DrawnTitles int
	// Zones 是本页登记的功能模块数(虚拟组优先,没有组才是认领表)。
	// 它只决定**报文怎么写**,不决定报不报 —— 有组不等于画了框。
	Zones int
}

// Frames 是「这页有几个分区框」的最终口径:两个证人取大。
func (e schPartitionEvidence) Frames() int {
	if e.DrawnTitles > e.RecordedRects {
		return e.DrawnTitles
	}
	return e.RecordedRects
}

// Labels is the reconciled count of module title texts.
func (e schPartitionEvidence) Labels() int {
	if e.DrawnTitles > e.RecordedLabels {
		return e.DrawnTitles
	}
	return e.RecordedLabels
}

// schPartitionPageEvidence 是 I/O 外壳:一次 loadSchGroupsContext 取回记账 + 本页
// 模块表,再拿调用方已经拉好的 text.list 认画布上的区标题(零新增 I/O)。
// best-effort:读不到上下文就退回全 0 —— 等价于修复前的行为,绝不因此漏报。
func schPartitionPageEvidence(cfg *appConfig, window string, texts []zoneMoveText) schPartitionEvidence {
	_, _, docUUID, _, st, _, err := loadSchGroupsContext(cfg, window)
	if err != nil || st == nil {
		return schPartitionEvidence{}
	}
	// 模块归属的读法与 loadSchZoneModules 一致:虚拟组优先,没有组才回落认领。
	zones := schGroupModulesFromState(st, docUUID)
	if len(zones) == 0 {
		zones = st.SchZonesForPage(docUUID)
	}
	names := make([]string, 0, len(zones))
	for n := range zones {
		names = append(names, n)
	}
	ev := schPartitionEvidence{Zones: len(zones), DrawnTitles: countDrawnZoneTitles(texts, names)}
	// recordedZoneFrames 是 zone-draw 自己查记账的那个函数 —— 记账这一侧也共用一把尺
	// (旧的 schZoneFrameCounts 自己手写了一遍 page/legacy 两分支,legacy 的空记录
	// 判定与 zone-draw 不同)。
	if f, _ := recordedZoneFrames(st, docUUID); f != nil {
		ev.RecordedRects, ev.RecordedLabels = len(f.Rects), len(f.Texts)
	}
	moduleFrames := recordedModuleFrameCount(st, docUUID)
	ev.RecordedRects += moduleFrames
	ev.RecordedLabels += moduleFrames
	return ev
}
