package app

// sch_prim_delete_settle.go — `sch prim-delete` 的 settle 复核。
//
// 连接器侧 `survivingSchPrimitives` 在 delete 之后**立刻** `getAll()` 判存活。
// 那一读可能采到还没落定的快照,于是把「已经删掉」报成 survived —— 上层
// (failOnSurvivingPrimitives)据此非零退出,人再删一遍,一轮轮空转。
//
// 这里不去改连接器(改 extension 意味着重打包 .eext,真机验证成本高,而这条能在
// Go 侧闭合):首轮报幸存时,等一拍 settle 再对**幸存 id**重发一次删除。第二次
// 回执就是定案:
//
//   - 那些 id 其实早删掉了 → 第二次它们进 notFound,不再 partial → 判定成功;
//   - 真没删掉(平台大批量静默 no-op / 刚建的图元短暂拒删)→ 第二次顺手补删并
//     再回读一次;
//   - 删除仍未生效 → 保留 partial、如实失败；仅凭残留不推断根因。
//
// 与 deleteVerifiedOneByOne 是同一把尺:删一轮 → settle 回读 → 幸存者重删一次 →
// 再回读定案。重发 delete 是安全的:对已经不在页上的 id,连接器把它归 notFound
// 而不是再删一次别的东西。
//
// 这里同样**不**需要 sch_place_adopt.go 的新鲜度门(2026-08-20 复核):读得太早
// 只会把已删的报成 survived(偏保守,上层重删 + 给处方),不会把没删的报成删掉。
// 唯一的反向坏帧要求回读倒退到这些 id 出生之前,而 `sch prim-delete` 处理的是
// **用户手上已有的** id(不是本命令几秒前建的),那种倒退不在这条路径的风险面上。
// 完整推导见 sch_delete_verified.go 的 settleAliveSet 注释。

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
// stdout 上留下的始终是首轮的原始回执;最终判定看 stderr 与退出码。
func primDeleteSettleRecheck(cfg *appConfig, window string, res *actionResult, stderr io.Writer) *actionResult {
	if res == nil || res.Result == nil {
		return res
	}
	if partial, _ := res.Result["partial"].(bool); !partial {
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
	if partial, _ := second.Result["partial"].(bool); !partial {
		fmt.Fprintln(stderr, "✓ 复核后这些图元已不在页上")
		return second
	}
	return second
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
