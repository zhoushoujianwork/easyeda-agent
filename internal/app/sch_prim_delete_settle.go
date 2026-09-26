package app

// A bounded second deletion is issued only for confirmed survivors. Unknown
// readback never authorizes another mutation. The final result retains both
// attempts and only explicitly confirmed removals may leave the group registry.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// primDeleteSettleRecheck 对首轮 delete 报出的幸存者做一次 settle 复核,返回
// **用于定案**的 result(复核成功时是第二轮的回执,否则退回首轮回执)。
//
// 调用方输出合并后的最终结果；原始尝试保留在 attempts 中。
func primDeleteSettleRecheck(cfg *appConfig, window string, res *actionResult, stderr io.Writer) *actionResult {
	if res == nil || res.Result == nil {
		return res
	}
	if partial, _ := res.Result["partial"].(bool); !partial || res.Result["verified"] == false {
		return res
	}
	survivors := survivedIDSet(res.Result)
	if len(survivors) == 0 {
		return res
	}
	ids := make([]string, 0, len(survivors))
	for id := range survivors {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	fmt.Fprintf(stderr, "⚠ 首轮回读报 %d 个图元存活(%s)—— 等 %v settle 后复核一轮再定案\n",
		len(ids), strings.Join(ids, ", "), settleDelay)
	time.Sleep(settleDelay)

	second, err := requestAction(cfg, "schematic.primitives.delete", window,
		map[string]any{"primitiveIds": ids})
	if err != nil {
		fmt.Fprintf(stderr, "  复核失败(%v)—— 按首轮回执定案\n", err)
		return res
	}
	if second == nil || second.Result == nil {
		return res
	}
	merged := mergePrimDeleteResults(res.Result, second.Result)
	if partial, _ := merged["partial"].(bool); !partial {
		fmt.Fprintln(stderr, "✓ 复核后这些图元已不在页上")
	}
	return &actionResult{OK: true, Result: merged}
}

// Only explicit, positively verified IDs can leave the group registry.
func confirmedDeletedIDSet(result map[string]any) map[string]bool {
	if result == nil {
		return map[string]bool{}
	}
	return survivedIDSet(map[string]any{"survived": result["deletedIds"]})
}

// Retrying only survivors must preserve the first attempt's confirmed removals.
// A retry's notFound confirms absence of that previously observed survivor.
func mergePrimDeleteResults(first, second map[string]any) map[string]any {
	out := make(map[string]any, len(second)+3)
	for k, v := range second {
		out[k] = v
	}
	groups := map[string][]any{}
	addGroups := func(value any) {
		if m, ok := value.(map[string]any); ok {
			for kind, value := range m {
				seen := map[string]bool{}
				for _, v := range groups[kind] {
					seen[asString(v)] = true
				}
				for id := range survivedIDSet(map[string]any{"survived": value}) {
					if !seen[id] {
						groups[kind] = append(groups[kind], id)
					}
				}
			}
		}
	}
	addGroups(first["deletedIds"])
	absent := survivedIDSet(map[string]any{"survived": second["notFound"]})
	removed := confirmedDeletedIDSet(second)
	remaining := survivedIDSet(second)
	unknown := survivedIDSet(map[string]any{"survived": second["unverified"]})
	var unresolved []any
	if m, ok := first["survived"].(map[string]any); ok {
		for kind, values := range m {
			for id := range survivedIDSet(map[string]any{"survived": values}) {
				if !remaining[id] && !unknown[id] && (absent[id] || removed[id]) {
					groups[kind] = append(groups[kind], id)
				} else if !remaining[id] {
					unresolved = append(unresolved, id)
				}
			}
		}
	}
	if len(unresolved) > 0 {
		out["partial"], out["verified"] = true, false
		out["unverified"] = map[string]any{"settle": unresolved}
	}
	if len(remaining) > 0 {
		out["partial"] = true
	}
	counts, ids := map[string]any{}, map[string]any{}
	total := 0
	for kind, group := range groups {
		sort.Slice(group, func(i, j int) bool { return asString(group[i]) < asString(group[j]) })
		counts[kind], ids[kind] = len(group), group
		total += len(group)
	}
	out["deleted"], out["deletedIds"], out["total"] = counts, ids, total
	if requested, ok := first["requested"]; ok {
		out["requested"] = requested
	}
	delete(out, "notFound")
	if original, ok := first["notFound"]; ok {
		out["notFound"] = original
	}
	out["attempts"] = []any{first, second}
	return out
}

// primDeleteResidueGuidance reports residual facts and preserves diagnostic
// evidence. A survivor alone does not prove a queue failure or justify GUI edits.
func primDeleteResidueGuidance(w io.Writer, res *actionResult) {
	ids := survivedIDSet(nil)
	if res != nil {
		ids = survivedIDSet(res.Result)
	}
	if len(ids) > 0 {
		list := make([]string, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		sort.Strings(list)
		fmt.Fprintf(w, "  still on the page: %s\n", strings.Join(list, ", "))
	}
	fmt.Fprintln(w, "  删除尚未完整生效，停止依赖步骤；仅凭残留不能判断是宿主状态、删除接口还是队列问题。")
	fmt.Fprintln(w, "  保存原始命令/回包；绑定同一 --project/--doc 做 fresh `easyeda sch list` 并核对上述 ID。")
	fmt.Fprintln(w, "  按回读事实诊断 typed 接口；不要盲重试、刷新浏览器、重启宿主或用 GUI 删除兜底。")
}
