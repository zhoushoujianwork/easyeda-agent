package app

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/internal/schguard"
)

// ── sch gate: the S5 校验门, one command ──────────────────────────────────
//
// 收敛动机(docs/reviews/2026-08-sch-surface-audit.md):原理图有 4 个独立检查命令
// (layout-lint / check / bridge-check / drc),agent 每次都要自己决定跑哪几个、
// 什么顺序、哪个的非零退出算数 —— 这个决策没有数据判据,只有散文描述,**猜错就是
// 不稳定**。audit log 实测 agent 在检查器失败后分别改调过 components.list / save /
// check / pages.list 四种不同的下一步,没有一种是被规定的。
//
// gate 把顺序固定下来、把判据写进代码、出一张报告:agent 只需要「跑 gate,读
// blockers」。四个单命令原样保留给专家和局部复查,但不再是主干路径。

// gateStageSpec declares one stage's identity and how to run it. Order in
// gateStages IS the execution order.
type gateStageSpec struct {
	Name string
	// Why this position: cheap-and-explanatory first. Geometry (layout-lint) is
	// one components.list read and explains downstream noise — overlapping parts
	// produce phantom electrical findings, so reporting them first tells the
	// agent what to fix before it chases symptoms. DRC is last: it is the
	// slowest, needs the window in the foreground, and its aggregate output is
	// the least actionable of the four.
	Why string
}

var gateStages = []gateStageSpec{
	{Name: "layout-lint", Why: "几何真值,一次 components.list,解释后面的连锁误报"},
	{Name: "clusters", Why: "虚拟组体积(器件+它自己的 marker/桩线)—— layout-lint **结构上看不见**的那一半"},
	{Name: "check", Why: "重建式逐条设计检查 + Go 侧几何 marker 规则"},
	{Name: "bridge-check", Why: "wire-tree 粒度的合并短路 / 孤儿桩"},
	{Name: "drc", Why: "官方 SDK DRC,最慢且需前台,放最后"},
}

// gateStage is one stage's outcome inside the aggregate report.
type gateStage struct {
	Name string `json:"name"`
	// Status separates the two failure kinds that agents kept conflating:
	//   pass    — ran, nothing blocking
	//   fail    — ran, found blocking problems (the BOARD has a problem)
	//   error   — could NOT run (connector down, page not open, bad shape).
	//             The board is not implicated; retrying other checkers is futile.
	//   skipped — excluded via --only/--skip, or not reached after a hard stop
	Status string `json:"status"`
	// Errors/Warnings are severity tallies for display. They are NOT the gate
	// decision — BlockingReasons is. Keeping them separate matters: under
	// --strict a stage can fail on promoted advisories while its error tally is
	// 0, and reporting "0 个阻塞项" next to FAIL is a self-contradiction that
	// tells the agent nothing (real-machine run 2026-08-04 hit exactly this).
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	// BlockingReasons names precisely what made this stage fail, in the same
	// vocabulary the underlying checker uses. Empty ⇔ the stage passed.
	BlockingReasons []string `json:"blockingReasons,omitempty"`
	Summary         string   `json:"summary"`
	Error           string   `json:"error,omitempty"`
	// Detail carries the stage's own full report under --json, so `sch gate --json`
	// is a superset of the four single commands' JSON and nothing needs a re-run.
	Detail any `json:"detail,omitempty"`
}

// gateReport is the one report the S5 gate produces.
type gateReport struct {
	OK bool `json:"ok"`
	// Verdict is the three-state outcome:
	//   pass    — every executed stage passed
	//   fail    — the schematic has blocking problems (see blockers)
	//   blocked — a checker could not run; the schematic was never fully judged
	// "blocked" exists because treating infra failure as a board failure is what
	// sent agents chasing phantom fixes.
	Verdict  string      `json:"verdict"`
	Stages   []gateStage `json:"stages"`
	Blockers []string    `json:"blockers"`
	Warnings []string    `json:"warnings,omitempty"`
}

// gate 三个判据阈值的默认值。**提成常量是为了让别的调用方用同一把尺**:
// `sch status --gate` 复用 collectSchGate 时若各自抄一份字面量,两条路就会在
// 某次调参后悄悄给出不同判定 —— 同一张画布两个答案是最难查的那种不一致。
const (
	gateDefaultMinGap = 2.54
	gateDefaultPinEps = 0.0
	// gateDefaultOverlapEps **必须**引用 schMarkerOverlapEps,不许抄字面量:
	// 2026-08-17 真机复验时 `sch check` 已按新容差报 0,`sch gate --strict` 的
	// check 段却还报 9 —— 就是这里的一份 0.5 手抄没跟着动。配对由
	// TestRuler_GateOverlapEpsMatchesCheck 钉死。
	gateDefaultOverlapEps = schMarkerOverlapEps
)

const (
	gateStatusPass    = "pass"
	gateStatusFail    = "fail"
	gateStatusError   = "error"
	gateStatusSkipped = "skipped"
)

// resolveGateStages applies --only/--skip and returns the ordered stage names to
// run plus the ones deliberately excluded. Unknown names are an error rather
// than a silent no-op — a typo'd --only must never look like a clean gate.
func resolveGateStages(only, skip string) (run []string, skipped []string, err error) {
	known := make(map[string]bool, len(gateStages))
	order := make([]string, 0, len(gateStages))
	for _, s := range gateStages {
		known[s.Name] = true
		order = append(order, s.Name)
	}
	parse := func(raw, flag string) (map[string]bool, error) {
		set := map[string]bool{}
		for _, part := range strings.Split(raw, ",") {
			name := strings.TrimSpace(strings.ToLower(part))
			if name == "" {
				continue
			}
			if !known[name] {
				return nil, fmt.Errorf("%s: unknown stage %q (known: %s)", flag, name, strings.Join(order, ", "))
			}
			set[name] = true
		}
		return set, nil
	}
	onlySet, err := parse(only, "--only")
	if err != nil {
		return nil, nil, err
	}
	skipSet, err := parse(skip, "--skip")
	if err != nil {
		return nil, nil, err
	}
	if len(onlySet) > 0 && len(skipSet) > 0 {
		return nil, nil, fmt.Errorf("--only and --skip are mutually exclusive")
	}
	for _, name := range order {
		switch {
		case len(onlySet) > 0 && !onlySet[name]:
			skipped = append(skipped, name)
		case skipSet[name]:
			skipped = append(skipped, name)
		default:
			run = append(run, name)
		}
	}
	if len(run) == 0 {
		return nil, nil, fmt.Errorf("no stages left to run")
	}
	return run, skipped, nil
}

// gateLayoutStage runs layout-lint and grades it. Overlaps and pin-coincidences
// are blocking (a real geometric defect); tight spacing is advisory.
func gateLayoutStage(cfg *appConfig, window string, minGap, pinEps float64, allPages, strict bool, geom *schGeomSnapshot) gateStage {
	st := gateStage{Name: "layout-lint"}
	rep, err := collectLayoutLintWith(cfg, window, minGap, pinEps, allPages, false, strict, geom)
	if err != nil {
		st.Status, st.Error = gateStatusError, err.Error()
		st.Summary = "layout-lint 没能跑起来"
		return st
	}
	st.Detail = rep
	st.Errors = len(rep.Overlaps) + len(rep.PinCoincidences)
	st.Warnings = len(rep.TightPairs) + len(rep.GridViolations) + len(rep.OutOfSheet)
	// The summary MUST mention the geometry-provenance counts, not just the
	// pairwise ones: under --strict those are what usually fails, and a summary
	// reading "0 overlap, 0 pin-coincidence…" beside a FAIL is unreadable.
	st.Summary = fmt.Sprintf("%d overlap, %d pin-coincidence, %d tight, %d off-grid, %d out-of-sheet, %d no-bbox, %d unchecked-pin, %d unproven-pin, %d nets-unproven, %d invalid-geometry (zone-check=%s sheet-check=%s)",
		len(rep.Overlaps), len(rep.PinCoincidences), len(rep.TightPairs),
		len(rep.GridViolations), len(rep.OutOfSheet), len(rep.NoBBox),
		len(rep.UncheckedPins), len(rep.UnprovenPins), len(rep.NetsUnproven), len(rep.InvalidGeometry),
		rep.ZoneCheckStatus, rep.SheetCheckStatus)

	add := func(n int, label string) {
		if n > 0 {
			st.BlockingReasons = append(st.BlockingReasons, fmt.Sprintf("%d %s", n, label))
		}
	}
	add(len(rep.Overlaps), "overlap")
	add(len(rep.PinCoincidences), "pin-coincidence")
	if strict {
		add(len(rep.TightPairs), "tight-spacing (--strict)")
		add(len(rep.GridViolations), "off-grid anchor (--strict)")
		add(len(rep.OutOfSheet), "out-of-sheet (--strict;件越出图纸可用区,印不出来)")
		add(len(rep.NoBBox), "component without bbox (--strict)")
		add(len(rep.UncheckedPins), "unchecked pin geometry (--strict)")
		add(len(rep.UnprovenPins), "unproven pin geometry (--strict;连接器未给 pinsAvailable 契约)")
		add(len(rep.NetsUnproven), "unproven pin nets (--strict;网表导出不可用,引脚 net 全为 null —— 是读取失败,不是电路悬空)")
		add(len(rep.InvalidGeometry), "invalid geometry value (--strict)")
		if rep.ZoneCheckStatus == "unavailable" {
			st.BlockingReasons = append(st.BlockingReasons,
				"zone-check unavailable (--strict): "+rep.ZoneCheckError)
		}
		if rep.SheetCheckStatus == "unavailable" {
			st.BlockingReasons = append(st.BlockingReasons,
				"sheet-check unavailable (--strict): "+rep.SheetCheckError)
		}
	}
	// rep.OK is the authority (it folds in the strict gate). If it says false but
	// we could not name a reason, say so plainly — a FAIL with no explanation is
	// worse than a crude one.
	if !rep.OK && len(st.BlockingReasons) == 0 {
		st.BlockingReasons = append(st.BlockingReasons,
			"layout-lint 判定不通过但未能归因,详见 `sch layout-lint --json` 的 ok/summary")
	}
	if rep.OK {
		st.Status = gateStatusPass
		st.BlockingReasons = nil
	} else {
		st.Status = gateStatusFail
	}
	return st
}

// gateCheckStage runs the reconstructed design check. fatal/error findings block;
// warn is advisory unless --strict; info never blocks (see checkLevelBlocks).
func gateCheckStage(cfg *appConfig, window string, allPages, strict bool, overlapEps float64, stderr io.Writer, geom *schGeomSnapshot) gateStage {
	st := gateStage{Name: "check"}
	payload := map[string]any{}
	if allPages {
		payload["allPages"] = true
	}
	res, err := requestAction(cfg, "schematic.check", window, payload)
	if err != nil {
		st.Status, st.Error = gateStatusError, err.Error()
		st.Summary = "sch check 没能跑起来"
		return st
	}
	rep, perr := parseCheckReport(res.Result)
	if perr != nil {
		st.Status, st.Error = gateStatusError, perr.Error()
		st.Summary = "sch check 返回了无法解析的结构"
		return st
	}
	mergeMarkerGeomFindingsWith(cfg, window, allPages, overlapEps, &rep, stderr, geom)
	st.Detail = rep
	st.Errors, st.Warnings, st.BlockingReasons = gradeGateCheckFindings(rep, strict)
	st.Summary = fmt.Sprintf("%d finding(s): %d error/fatal, %d warn/info", rep.Summary.Total, st.Errors, st.Warnings)
	if len(st.BlockingReasons) > 0 {
		st.Status = gateStatusFail
	} else {
		st.Status = gateStatusPass
	}
	return st
}

// gradeGateCheckFindings is the check stage's pure grading: severity tallies plus
// the blocking reasons. Blocking follows checkLevelBlocks — the ONE severity
// ruler shared with `sch check --strict` (issue #172): fatal/error always block,
// warn only under --strict, info NEVER (it marks hits against estimated geometry,
// e.g. a fallback-ratio titleblock keep-out, that need human confirmation).
// Reasons name the finding TYPES that block, not just a count — "3 个 error"
// sends the agent back to re-run the checker; "3 个 error: duplicate-net-marker,
// floating-pin" is already the fix list.
func gradeGateCheckFindings(rep checkReport, strict bool) (errors, warnings int, reasons []string) {
	blockingTypes := map[string]int{}
	promoted := 0 // warn-level findings promoted to blocking by --strict
	for _, f := range rep.Findings {
		isErr := false
		switch strings.ToLower(f.Level) {
		case "fatal", "error":
			errors++
			isErr = true
		default:
			warnings++
		}
		if checkLevelBlocks(f.Level, strict) {
			blockingTypes[f.Type]++
			if !isErr {
				promoted++
			}
		}
	}
	if errors > 0 {
		reasons = append(reasons,
			fmt.Sprintf("%d 个 error/fatal finding: %s", errors, formatTypeTally(blockingTypes)))
	} else if strict && promoted > 0 {
		reasons = append(reasons,
			fmt.Sprintf("%d 个 warn finding (--strict;info 不阻塞): %s", promoted, formatTypeTally(blockingTypes)))
	}
	return errors, warnings, reasons
}

// formatTypeTally renders a rule-type histogram as a stable, compact string.
func formatTypeTally(tally map[string]int) string {
	if len(tally) == 0 {
		return "(无类型信息)"
	}
	types := make([]string, 0, len(tally))
	for t := range tally {
		types = append(types, t)
	}
	// Most-frequent first, name as tiebreak, so the same board always renders
	// the same string.
	sort.Slice(types, func(i, j int) bool {
		if tally[types[i]] != tally[types[j]] {
			return tally[types[i]] > tally[types[j]]
		}
		return types[i] < types[j]
	})
	parts := make([]string, 0, len(types))
	for _, t := range types {
		name := t
		if name == "" {
			name = "(未命名规则)"
		}
		parts = append(parts, fmt.Sprintf("%s×%d", name, tally[t]))
	}
	return strings.Join(parts, ", ")
}

// gateBridgeStage runs bridge-check. A BRIDGE is a real short and blocks; an
// orphan stub/flag is advisory (it is cosmetic until it is wired).
// gateClustersStage 判 L1 虚拟组(器件 + 只挂在它自己引脚上的 marker/桩线/文字)。
//
// **为什么它必须在门里**:`layout-lint` 默认排除全部非 part 图元,而「标签压标签 /
// 标签压器件 / 标签探出图纸」恰恰只发生在这些图元上 —— 同一张画布上有 11 处标签
// 重叠时它照样报 `✓ placement gate passed`。判据结构上看不见的东西,只能由另一个
// 判据补上;补了还不进门,等于没补。
func gateClustersStage(cfg *appConfig, window string, strict bool, geom *schGeomSnapshot) gateStage {
	st := gateStage{Name: "clusters"}
	res, perr := geom.resultOr(cfg, window, map[string]any{"includeBBox": true, "includePins": true, "includeWires": true, "includeConnectivitySummary": true})
	var comps []layoutComp
	if perr == nil {
		comps, perr = parseLayoutComps(res.Result)
	}
	if perr != nil {
		st.Status, st.Error = gateStatusError, perr.Error()
		st.Summary = "sch clusters 没能读到几何"
		return st
	}
	wires, werr := schClusterSnapshotWires(res.Result)
	if werr != nil && strict {
		st.Status, st.Error = gateStatusError, werr.Error()
		st.Summary = "strict clusters 缺少同一快照的完整导线几何"
		return st
	}
	if strict {
		if err := schguard.ValidateWireCrossingInventory(res.Result); err != nil {
			st.Status, st.Error = gateStatusError, err.Error()
			st.Summary = "strict clusters 缺少同一快照的完整库存"
			return st
		}
	}
	if werr != nil {
		wires, _ = fetchSchWirePolylines(cfg, window, "")
	} // Legacy geometry never grants a crossing exemption.
	clusters, _ := buildSchClusters(comps, wires)
	var usable *layoutBBox
	if sheet := sheetBBoxOf(comps); sheet != nil {
		usable = &layoutBBox{
			MinX: sheet.MinX + sheetEdgeMinGap, MinY: sheet.MinY + sheetEdgeMinGap,
			MaxX: sheet.MaxX - sheetEdgeMinGap, MaxY: sheet.MaxY - sheetEdgeMinGap,
		}
	}
	minGap := 0.0
	if strict {
		minGap = bslPartGap // 非 strict 只判硬伤;组间"贴着但不压"留给 strict
	}
	var same schSameGroupFn
	if _, _, docUUID, _, gst, _, gerr := loadSchGroupsContext(cfg, window); gerr == nil {
		same = schSameLayoutOwnerFromState(gst, docUUID)
	}
	proof, _ := schguard.VerifiedWireCrossings(res.Result)
	findings := judgeSchClustersWithCrossings(clusters, usable, minGap, same, proof)
	st.Detail = schClusterReport{Clusters: clusters, Findings: findings, Sheet: usable}
	var overlaps, offSheet, tight int
	for _, f := range findings {
		switch f.Type {
		case "overlap":
			overlaps++
		case "out-of-sheet":
			offSheet++
		case "tight":
			tight++
		}
	}
	st.Errors, st.Warnings = overlaps+offSheet, tight
	st.Summary = fmt.Sprintf("%d 个虚拟组:%d 重叠 / %d 出图纸 / %d 过近",
		len(clusters), overlaps, offSheet, tight)
	if overlaps > 0 {
		st.BlockingReasons = append(st.BlockingReasons, fmt.Sprintf("%d 处组间图元重叠", overlaps))
	}
	if offSheet > 0 {
		st.BlockingReasons = append(st.BlockingReasons, fmt.Sprintf("%d 个组探出图纸可用区", offSheet))
	}
	if strict && tight > 0 {
		st.BlockingReasons = append(st.BlockingReasons, fmt.Sprintf("%d 处组间过近 (--strict)", tight))
	}
	if len(st.BlockingReasons) > 0 {
		st.Status = gateStatusFail
	} else {
		st.Status = gateStatusPass
	}
	return st
}

func gateBridgeStage(cfg *appConfig, window string, allPages, strict bool) gateStage {
	st := gateStage{Name: "bridge-check"}
	payload := map[string]any{}
	if allPages {
		payload["allPages"] = true
	}
	res, err := requestAction(cfg, "schematic.bridgeCheck", window, payload)
	if err != nil {
		st.Status, st.Error = gateStatusError, err.Error()
		st.Summary = "sch bridge-check 没能跑起来"
		return st
	}
	rep, perr := parseBridgeReport(res.Result)
	if perr != nil {
		st.Status, st.Error = gateStatusError, perr.Error()
		st.Summary = "sch bridge-check 返回了无法解析的结构"
		return st
	}
	st.Detail = rep
	st.Errors = rep.Summary.Bridges
	st.Warnings = rep.Summary.Orphans + rep.Summary.OrphanFlags + rep.Summary.OrphanTrees
	st.Summary = fmt.Sprintf("%d bridge(short), %d orphan-stub, %d orphan-flag, %d orphan-tree (%d wire tree(s))",
		rep.Summary.Bridges, rep.Summary.Orphans, rep.Summary.OrphanFlags, rep.Summary.OrphanTrees, rep.Summary.WireTreesTotal)
	if rep.Summary.Bridges > 0 {
		st.BlockingReasons = append(st.BlockingReasons,
			fmt.Sprintf("%d wire-bridge(真短路)", rep.Summary.Bridges))
	}
	if strict {
		if rep.Summary.Orphans > 0 {
			st.BlockingReasons = append(st.BlockingReasons,
				fmt.Sprintf("%d orphan-stub (--strict)", rep.Summary.Orphans))
		}
		if rep.Summary.OrphanFlags > 0 {
			st.BlockingReasons = append(st.BlockingReasons,
				fmt.Sprintf("%d orphan-flag (--strict)", rep.Summary.OrphanFlags))
		}
		if rep.Summary.OrphanTrees > 0 {
			st.BlockingReasons = append(st.BlockingReasons,
				fmt.Sprintf("%d orphan-tree (--strict)", rep.Summary.OrphanTrees))
		}
	}
	if len(st.BlockingReasons) > 0 {
		st.Status = gateStatusFail
	} else {
		st.Status = gateStatusPass
	}
	return st
}

// gateDrcStage runs the official SDK DRC. Only fatal blocks — matching the
// standalone `sch drc` contract, which real boards are calibrated against.
func gateDrcStage(cfg *appConfig, window string, strict bool) gateStage {
	st := gateStage{Name: "drc"}
	payload := map[string]any{"includeVerboseError": true}
	if strict {
		payload["strict"] = true
	}
	res, err := requestAction(cfg, "schematic.drc.check", window, payload)
	if err != nil {
		st.Status, st.Error = gateStatusError, err.Error()
		st.Summary = "sch drc 没能跑起来(DRC 需要窗口在前台)"
		return st
	}
	rep, perr := parseDrcReport(res.Result)
	if perr != nil {
		st.Status, st.Error = gateStatusError, perr.Error()
		st.Summary = "sch drc 返回了无法解析的结构"
		return st
	}
	st.Detail = rep
	if rep.Summary == nil {
		st.Summary = fmt.Sprintf("native verdict %s (strict=%t); counts unavailable",
			drcVerdictLabel(rep.Passed), rep.Strict)
	} else {
		st.Errors = rep.Summary.Fatal + rep.Summary.Error
		st.Warnings = rep.Summary.Warn
		st.Summary = fmt.Sprintf("%d fatal, %d error, %d warn, %d info (total %d); native verdict %s",
			rep.Summary.Fatal, rep.Summary.Error, rep.Summary.Warn, rep.Summary.Info, rep.Summary.Total,
			drcVerdictLabel(rep.Passed))
	}
	st.BlockingReasons = append(st.BlockingReasons, drcBlockingReasons(rep, strict)...)
	if len(st.BlockingReasons) > 0 {
		st.Status = gateStatusFail
	} else {
		st.Status = gateStatusPass
	}
	return st
}

// drcBlockingReasons 是 DRC 关的**阻塞判据**,抽成纯函数以便逐档钉死契约。
//
//	fatal  任何档位都阻塞
//	error  任何档位都阻塞 —— 与 check 关同口径(那边是 fatal||error||strict)
//	warn   仅 --strict 阻塞 —— 兑现 `--help` 的「non-fatal DRC items are advisory,
//	       --strict promotes them to blocking」
//	info   从不阻塞
//
// 这一关此前只看 rep.Fatal,**收了 strict 参数却不用**:官方 DRC 判定的 error 在
// 任何档位下都不阻塞,而文档写着 strict 会提升非 fatal。文档承诺了判据没做的事,
// 方向还是「你以为管住了」(2026-08-16 回归测试翻出)。
func drcBlockingReasons(rep drcReport, strict bool) []string {
	var out []string
	if !rep.Passed {
		out = append(out, fmt.Sprintf("native DRC verdict failed (strict=%t; inspect EasyEDA DRC 面板)", strict))
	}
	if rep.Summary == nil {
		return out
	}
	if rep.Fatal != nil && *rep.Fatal > 0 {
		out = append(out, fmt.Sprintf("%d fatal DRC violation", *rep.Fatal))
	}
	if rep.Summary.Error > 0 {
		out = append(out, fmt.Sprintf("%d error-level DRC violation", rep.Summary.Error))
	}
	// warn 的阻塞**必须带上「去哪看」**:平台的 sch_Drc.check 只回聚合计数,
	// 逐条明细没有 API(memory: schematic-drc-aggregate-only),我们能报的只有
	// 「有几条」。不写清楚就是一条无法行动的阻塞 —— 而无法行动的阻塞会被直接
	// 绕过,连它以后报的真问题一起绕过。
	if strict && rep.Summary.Warn > 0 {
		out = append(out, fmt.Sprintf(
			"%d warn-level DRC violation (--strict;平台只回聚合数,逐条明细请在 EasyEDA 的 DRC 面板查看)",
			rep.Summary.Warn))
	}
	return out
}

// gateAdviceRule maps a substring of a blocking reason to the prescribed fix.
// Keyed on the REASON, never on the stage name: a stage-keyed table told the
// 2026-08-04 real-machine run to "拆掉真短路" on a page with 0 bridges (it
// failed on --strict orphan stubs) and to "重排几何" with 0 overlaps. Advice
// that points at a problem the board does not have is worse than no advice —
// it is exactly the "agent chases a phantom" failure this gate exists to stop.
var gateAdviceRules = []struct{ match, advice string }{
	{"overlap", "几何重叠:`sch autolayout` 重排,或 `sch modify` 单件挪位。几何先修 —— 重叠会连锁出一堆电气误报"},
	{"pin-coincidence", "异件引脚重合 = 隐式短路:`sch modify` 把其中一件挪开(哪怕只挪一格)"},
	{"unproven pin geometry", "引脚几何未经证明:连接器太旧没给 pinsAvailable 契约 —— 升级连接器,或本轮去掉 `--strict`(不是电路问题)"},
	{"unchecked pin geometry", "引脚几何读不到:确认目标页在前台且已加载完(`doc switch`),再重跑"},
	{"component without bbox", "有器件读不到 bbox:多半是非激活页的浅数据 —— `doc switch` 到该页后重跑,别用 `--all-pages` 当证明"},
	{"invalid geometry", "几何值非法(NaN/Inf):该器件多半没落好,`sch list` 查坐标后 `sch modify` 重置"},
	{"tight-spacing", "间距偏紧(仅 --strict 阻塞):`sch distribute` 拉开,或确认 `--min-gap` 是否符合本板工艺"},
	{"off-grid anchor", "锚点不在网格上:`sch modify` 把坐标吸附到 5 网格 —— 离格会让 netflag 连不上"},
	{"zone-violation", "器件跑出认领分区:`sch zones status` 看认领,`sch autolayout` 按分区重排"},
	{"zone-check unavailable", "分区检查跑不了:先 `sch zones status` 确认认领与图纸可读,再重跑"},
	{"wire-bridge", "真短路:按 tree 的 primitiveIds 定位后 `sch prim-delete` 拆掉压线,再 `sch connect` 重连"},
	{"orphan-stub", "孤儿桩(仅 --strict 阻塞):要么 `sch connect` 补上网络标识,要么 `sch prim-delete` 清掉"},
	{"orphan-flag", "孤儿标识(仅 --strict 阻塞):flag 不挨任何导线,`sch prim-delete` 清掉 —— 新线穿过会静默继承其网名"},
	{"orphan-tree", "悬空树(仅 --strict 阻塞):flag+桩线成树却不触任何引脚(挪件残留)或纯裸死线,`sch prim-delete` 整树(wireIds+flagIds)清掉"},
	{"finding", "按 finding 类型分治:duplicate-net-marker 喂 `sch prim-delete`(带 suggestDeleteIds),floating-pin 用 `sch no-connect`,wire-* 用 `sch disconnect` 后重连"},
	{"fatal DRC", "跑 `sch drc --verbose` 看逐条明细(gate 只汇总);DRC 需要 EasyEDA 窗口在前台"},
}

// gateAdviceFor returns the prescribed next steps for one failed stage, derived
// from its actual blocking reasons. Deduped and order-stable.
func gateAdviceFor(st gateStage) []string {
	var out []string
	seen := map[string]bool{}
	for _, reason := range st.BlockingReasons {
		lower := strings.ToLower(reason)
		for _, rule := range gateAdviceRules {
			if !strings.Contains(lower, strings.ToLower(rule.match)) || seen[rule.advice] {
				continue
			}
			seen[rule.advice] = true
			out = append(out, rule.advice)
		}
	}
	return out
}

// runSchGate executes the fixed S5 gate pipeline and renders one report.
func runSchGate(cfg *appConfig, window string, allPages, strict, asJSON, failFast bool,
	only, skip string, minGap, pinEps, overlapEps float64, stdout, stderr io.Writer) error {
	rep, err := collectSchGate(cfg, window, allPages, strict, failFast, only, skip,
		minGap, pinEps, overlapEps, stderr)
	if err != nil {
		return err
	}

	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		renderGateReport(*rep, stdout)
	}

	switch rep.Verdict {
	case "blocked":
		return fmt.Errorf("sch gate: BLOCKED — 检查器没能跑完,原理图未被完整判定(不是板子的问题):%s",
			strings.Join(rep.Blockers, "; "))
	case "fail":
		return fmt.Errorf("sch gate: FAIL — %s", strings.Join(rep.Blockers, "; "))
	}
	return nil
}

// collectSchGate 跑固定管线并给出评级后的报告,**不渲染、不决定退出码**。
//
// 抽出来是为了让 `sch status --gate` 复用同一条管线:status 要的是三态判定本身
// (pass / fail / **blocked**),而不是打印出来的那段文字。如果它改用「调 runSchGate
// 看 error 非空」来判,blocked 就会被折成 fail —— 「检查器没跑起来」被当成「板子有病」,
// 正是 gate 三态存在的理由。判定只有一个来源,渲染是它的下游。
func collectSchGate(cfg *appConfig, window string, allPages, strict, failFast bool,
	only, skip string, minGap, pinEps, overlapEps float64, stderr io.Writer) (*gateReport, error) {
	if strict && allPages {
		return nil, fmt.Errorf("sch gate: --strict cannot be combined with --all-pages: inactive pages expose shallow geometry (see layout-lint), so gate each page after `easyeda doc switch <page>`")
	}
	run, skippedNames, err := resolveGateStages(only, skip)
	if err != nil {
		return nil, err
	}

	// **一次 gate 只读一次几何**。三关(layout-lint / clusters / check 的 marker 规则)
	// 判的是同一张画布的同一时刻,却各读各的 —— 实测一次 `gate --strict` 打 3 发
	// components.list,其中两发 payload 完全相同。6 个器件的页上这是 0.93s,可
	// includePins 的代价随引脚数涨(81 脚模组单次 18s),同一页就是 54s;整场 E2E 里
	// components.list 吃掉 41% 的 daemon 时间。
	//
	// 快照**显式传递**而不是在 dispatch 层做隐式缓存:试过后者,它既打破了靠「每次
	// 注入不同响应」工作的 fake-dispatcher 测试,又因为 debug.exec_js 被标记为写动作
	// 而在关与关之间全被清空 —— 一点没省。作用域限定在这里,失效问题根本不存在:
	// gate 全程只读,读完就用完。
	geom := gatePreloadGeometry(cfg, window, allPages)

	rep := gateReport{Stages: make([]gateStage, 0, len(gateStages))}
	byName := map[string]gateStage{}
	hardStopped := false
	for _, name := range run {
		if hardStopped {
			byName[name] = gateStage{Name: name, Status: gateStatusSkipped, Summary: "前一个 stage 没能跑起来,后续未执行"}
			continue
		}
		var st gateStage
		switch name {
		case "layout-lint":
			st = gateLayoutStage(cfg, window, minGap, pinEps, allPages, strict, geom)
		case "clusters":
			st = gateClustersStage(cfg, window, strict, geom)
		case "check":
			st = gateCheckStage(cfg, window, allPages, strict, overlapEps, stderr, geom)
		case "bridge-check":
			st = gateBridgeStage(cfg, window, allPages, strict)
		case "drc":
			st = gateDrcStage(cfg, window, strict)
		}
		byName[name] = st
		// A stage that could not RUN stops the pipeline: the remaining checkers
		// talk to the same connector/page and would fail the same way, and their
		// noise would bury the real cause. This is exactly the pattern the audit
		// log caught — 146 blind retries after a NO_CONNECTOR.
		if st.Status == gateStatusError {
			hardStopped = true
		}
		if failFast && st.Status == gateStatusFail {
			hardStopped = true
		}
	}
	for _, name := range skippedNames {
		byName[name] = gateStage{Name: name, Status: gateStatusSkipped, Summary: "被 --only/--skip 排除"}
	}
	// Emit in declared pipeline order regardless of --only/--skip, so the report
	// always reads as the same fixed pipeline.
	for _, spec := range gateStages {
		if st, ok := byName[spec.Name]; ok {
			rep.Stages = append(rep.Stages, st)
		}
	}

	gradeGateReport(&rep)
	return &rep, nil
}

// gradeGateReport derives blockers, warnings, verdict and ok from the stage
// outcomes. Pure so the verdict contract is unit-testable without a live
// connector — the gate's whole value is that this grading is fixed in code
// rather than re-decided per run.
//
// `blocked` outranks `fail`: if any checker could not run, the schematic was
// never fully judged, and reporting "fail" would send the agent fixing a board
// that may be fine (or, worse, declaring it fixed after the real checker never
// ran).
func gradeGateReport(rep *gateReport) {
	rep.Blockers = nil
	rep.Warnings = nil
	anyError, anyFail := false, false
	for _, st := range rep.Stages {
		switch st.Status {
		case gateStatusError:
			anyError = true
			rep.Blockers = append(rep.Blockers, fmt.Sprintf("%s 没能跑起来: %s", st.Name, st.Error))
		case gateStatusFail:
			anyFail = true
			// Report WHAT blocked, not a severity tally that can read 0 next to
			// a FAIL. Falls back to the summary only if a stage somehow failed
			// without naming a reason.
			why := strings.Join(st.BlockingReasons, "; ")
			if why == "" {
				why = st.Summary
			}
			rep.Blockers = append(rep.Blockers, fmt.Sprintf("%s: %s", st.Name, why))
		}
		if st.Status != gateStatusError && st.Warnings > 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: %d 条告警", st.Name, st.Warnings))
		}
	}
	switch {
	case anyError:
		rep.Verdict, rep.OK = "blocked", false
	case anyFail:
		rep.Verdict, rep.OK = "fail", false
	default:
		rep.Verdict, rep.OK = "pass", true
	}
}

func gateStatusTag(status string) string {
	switch status {
	case gateStatusPass:
		return "PASS "
	case gateStatusFail:
		return "FAIL "
	case gateStatusError:
		return "ERROR"
	case gateStatusSkipped:
		return "SKIP "
	}
	return "?????"
}

func renderGateReport(rep gateReport, w io.Writer) {
	verdict := strings.ToUpper(rep.Verdict)
	fmt.Fprintf(w, "sch gate: %s\n", verdict)
	for _, st := range rep.Stages {
		fmt.Fprintf(w, "  %s  %-13s  %s\n", gateStatusTag(st.Status), st.Name, st.Summary)
		if st.Error != "" {
			fmt.Fprintf(w, "         └─ %s\n", st.Error)
		}
	}
	if len(rep.Blockers) > 0 {
		fmt.Fprintln(w, "\n阻塞项:")
		for _, b := range rep.Blockers {
			fmt.Fprintf(w, "  • %s\n", b)
		}
		// Prescribed next steps, derived from what actually blocked — see
		// gateAdviceRules. Pipeline order is preserved (geometry first), which
		// is also the order they should be fixed in.
		var advice []string
		seen := map[string]bool{}
		for _, st := range rep.Stages {
			if st.Status != gateStatusFail {
				continue
			}
			for _, a := range gateAdviceFor(st) {
				if seen[a] {
					continue
				}
				seen[a] = true
				advice = append(advice, a)
			}
		}
		if len(advice) > 0 {
			fmt.Fprintln(w, "\n下一步:")
			for _, a := range advice {
				fmt.Fprintf(w, "  → %s\n", a)
			}
		}
		if rep.Verdict == "blocked" {
			fmt.Fprintln(w, "\n  注意:这是「检查器没跑成」,不是「板子有问题」——先 `easyeda health` 确认连接器,")
			fmt.Fprintln(w, "  再 `easyeda doc ls` / `doc switch <page>` 确认目标页已打开,然后重跑 gate。")
		}
	}
	if len(rep.Warnings) > 0 {
		fmt.Fprintf(w, "\n告警(不阻塞): %s\n", strings.Join(rep.Warnings, ", "))
	}
}

// newSchGateCmd builds the `easyeda sch gate` command.
func newSchGateCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var (
		allPages, strict, asJSON, failFast bool
		only, skip                         string
		minGap, pinEps, overlapEps         float64
	)
	c := &cobra.Command{
		Use:   "gate",
		Short: "S5 校验门:按固定顺序跑 layout-lint → check → bridge-check → drc,出一张报告",
		Long: `Run the schematic verification gate: every checker, in one fixed order, as one report.

Pipeline (order is fixed on purpose — cheap and explanatory first):

  1. layout-lint   几何真值,一次 components.list,解释后面的连锁误报
  2. check         重建式逐条设计检查 + Go 侧几何 marker 规则
  3. bridge-check  wire-tree 粒度的合并短路 / 孤儿桩
  4. drc           官方 SDK DRC,最慢且需前台,放最后

Why one command: the four checkers exist as separate commands, so every run
started with an undecidable question — which ones, in what order, whose exit
code counts. The audit log shows agents answering it four different ways for
the same failure. Here the order is fixed, the blocking rules live in code, and
the output is one verdict.

Verdict is three-state, because "the checker could not run" is NOT "the board
is broken":

  pass     every executed stage passed
  fail     the schematic has blocking problems — see 阻塞项 / blockers
  blocked  a checker could not run (connector down, page not open, bad shape);
           the schematic was never fully judged. Remaining stages are skipped
           rather than run into the same wall.

Blocking rules: layout-lint overlap/pin-coincidence · check fatal+error ·
bridge-check bridge(real short) · drc fatal. Tight spacing, orphan stubs, and
non-fatal DRC items are advisory — --strict promotes them to blocking.

--json emits every stage's full native report under stages[].detail, so it is a
superset of the four single commands' JSON: nothing needs a second run.`,
		Args: cobra.NoArgs,
		Example: `  easyeda sch gate
  easyeda sch gate --json
  easyeda sch gate --strict                 # 告警也阻塞
  easyeda sch gate --only layout-lint,check # 只跑便宜的两关
  easyeda sch gate --skip drc               # 窗口不在前台时跳过 DRC
  easyeda sch gate --fail-fast              # 第一个阻塞失败就停`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchGate(cfg, *window, allPages, strict, asJSON, failFast,
				only, skip, minGap, pinEps, overlapEps, stdout, stderr)
		},
	}
	c.Flags().BoolVar(&allPages, "all-pages", false, "gate every page (geometry on inactive pages is shallow — cannot combine with --strict)")
	c.Flags().BoolVar(&strict, "strict", false, "promote advisory findings (tight spacing, warn-level, orphan stubs) to blocking")
	c.Flags().BoolVar(&asJSON, "json", false, "emit the aggregate report as JSON, with each stage's full report under stages[].detail")
	c.Flags().BoolVar(&failFast, "fail-fast", false, "stop at the first blocking failure instead of collecting every stage")
	c.Flags().StringVar(&only, "only", "", "run only these stages (comma-separated: layout-lint,check,bridge-check,drc)")
	c.Flags().StringVar(&skip, "skip", "", "skip these stages (comma-separated)")
	// Defaults mirror `sch layout-lint` / `sch check` exactly — gating a page must
	// never disagree with linting it directly.
	c.Flags().Float64Var(&minGap, "min-gap", gateDefaultMinGap, "layout-lint stage: min edge-to-edge gap (mm) before a pair is flagged as tight")
	c.Flags().Float64Var(&pinEps, "pin-eps", gateDefaultPinEps, "layout-lint stage: max distance (mm) at which two pins of DIFFERENT components count as coincident; 0 = strict equality")
	c.Flags().Float64Var(&overlapEps, "overlap-eps", gateDefaultOverlapEps, "check stage: min positive-area extent (mm) for the marker-overlap/titleblock-overlap rules")
	return c
}
