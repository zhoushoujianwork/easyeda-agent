package app

// cmd_sch_titleblock_merge.go — 图签写入的**读改写**外壳。
//
// 当前只传点名的文本项，不回写图框结构。早期 V3 整包/显隐经验不能覆盖
// V4 的当前模板：强制 showTitle/showValue=true 会显示额外属性文字并探出外框。
// 文本更新保留已知显隐，未知/null 必须显式给布尔值；不猜宿主默认状态。

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// tbPreserve 原样回传一个**没被修改**的明细项,但把数字型字符串还原成数字。
//
// **这条是数据损坏修复**:`Title Block` / `Border` 这类结构开关读回来是字符串 `"1"`,
// 整体回传时平台把它解析成 0 —— 实测一次写图签就把**图框和明细表整个关掉**,
// sheet 图元的 bbox 塌成全零,`zone-plan`/`layout-lint` 的 sheet-check 当场不可用。
// 页面看上去还在,判据却瞎了,而且没有任何报错。
func tbPreserve(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for k, vv := range m {
		out[k] = vv
	}
	if sv, isStr := out["value"].(string); isStr {
		if n, err := strconv.ParseFloat(strings.TrimSpace(sv), 64); err == nil && sv != "" {
			out["value"] = n
		}
	}
	return out
}

// 这里曾有 tbStructuralKeys / tbKeepStructural:整包回传时把 `Title Block`/`Border`
// 两个结构开关按住原值,因为读回来的 "1" 被平台解析成 0 会把图框整个关掉
// (2026-08-15 esp32Mini E2E:四页图纸静默丢失)。
//
// **2026-08-26 连同整包回传一起删除。** 那套保护是在给一个本就不该做的动作打补丁:
// 整包回传才是图框损毁的成因(#186 —— `Device`/`Symbol` 的符号名被灌进 sheet 的
// UUID 引用位)。现在只传用户点名的文本项,结构键根本不出现在 payload 里,也就
// 不需要"按住"它们;连接器侧还有一道过滤兜底。**别再把它加回来** —— 要加回来
// 就意味着又在整包回传了。
// tbBoolOr 把读回来的 null / 非布尔折成一个确定的布尔。
func tbBoolOr(v any, fallback bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return fallback
}

// schTitleBlockMerge 读回当前页的全量明细项,把用户的 patch 合并进去。
//
// patch 接受两种写法:`{"Name":{"value":"X"}}`(与读回来的形状一致)与
// `{"Name":"X"}`(顺手写)。文本更新保留已知显隐，缺失显隐须显式补齐再写。
func schTitleBlockMerge(cfg *appConfig, window string, patch map[string]any) (map[string]any, bool, error) {
	if len(patch) == 0 {
		return nil, false, fmt.Errorf("图签 --data 必须包含至少一个字段更新")
	}
	res, err := requestAction(cfg, "schematic.titleblock.get", window, map[string]any{})
	if err != nil {
		return nil, false, fmt.Errorf("图签写入前要先读回全量明细项(平台传子集会崩): %w", err)
	}
	shown, showKnown := res.Result["showTitleBlock"].(bool)
	full, _ := res.Result["titleBlockData"].(map[string]any)
	if full == nil {
		return nil, false, fmt.Errorf("读不到当前页的明细项 —— 无法安全写入(平台传子集会崩)")
	}
	// **只回传用户点名要改的项** —— 不再整包回传(2026-08-26 改)。
	//
	// 原来这里读回全量明细项再整包下发,理由是「平台传子集会崩」。那条结论已被
	// 平台版本淘汰:3.2.149 实测单传一个 `Drawed` 完全写得进去。而整包回传恰恰
	// 是**图框损毁的成因** —— 社区 issue #186 复现:整包里的 `Device`/`Symbol`
	// 的 value 是符号**名字**,被平台灌进了 sheet 的 UUID 引用位,保存后重启拒载。
	// 连接器现在会在下发前过滤结构键(见 actions.ts 的 TITLE_BLOCK_STRUCTURAL_FIELDS),
	// 所以这里整包回传既无必要、又会撞上那道拒绝 —— 直接做减法:传子集。
	out, needShow, err := buildTitleBlockTextPatch(full, patch, shown)
	return out, showKnown && needShow, err
}

func buildTitleBlockTextPatch(full, patch map[string]any, shown bool) (map[string]any, bool, error) {
	if len(patch) == 0 {
		return nil, false, fmt.Errorf("图签 --data 必须包含至少一个字段更新")
	}
	out := make(map[string]any, len(patch))
	var unknown []string
	for k, v := range patch {
		if _, ok := full[k]; !ok {
			unknown = append(unknown, k)
		}
		item := map[string]any{}
		current, _ := full[k].(map[string]any)
		for _, flag := range []string{"showTitle", "showValue"} {
			if b, ok := current[flag].(bool); ok {
				item[flag] = b
			}
		}
		if m, isMap := v.(map[string]any); isMap {
			if len(m) == 0 {
				return nil, false, fmt.Errorf("图签字段 %s 的更新为空", k)
			}
			for field, value := range m {
				switch field {
				case "value":
					item[field] = value
				case "showTitle", "showValue":
					b, ok := value.(bool)
					if !ok {
						return nil, false, fmt.Errorf("图签字段 %s.%s 必须为布尔值", k, field)
					}
					item[field] = b
				default:
					return nil, false, fmt.Errorf("未知图签更新字段 %s.%s", k, field)
				}
			}
		} else {
			item["value"] = v
		}
		out[k] = item
	}
	if len(unknown) > 0 {
		return nil, false, fmt.Errorf("这些明细项当前页没有:%s —— 先跑 `easyeda sch titleblock-get` 看可用 key(平台对不认识的项会崩或静默忽略)",
			strings.Join(unknown, ", "))
	}
	// A text write must not silently choose host defaults for either unknown
	// visibility flag. For visibility-only updates, showTitle still initializes
	// the field in the official setter; an unrequested showValue may be omitted.
	var missing []string
	for _, key := range tbRequestedKeys(out) {
		item := out[key].(map[string]any)
		flags := []string{"showTitle"}
		if _, updatesValue := item["value"]; updatesValue {
			flags = append(flags, "showValue")
		}
		for _, flag := range flags {
			if _, known := item[flag].(bool); !known {
				missing = append(missing, key+"."+flag)
			}
		}
	}
	if len(missing) > 0 {
		return nil, false, fmt.Errorf("图签显隐未知，已在写入前停止；缺少 %s —— 请在对应 --data 字段显式提供 true 或 false，不能从 null、缺失或非布尔值推断宿主默认状态", strings.Join(missing, ", "))
	}
	// 只在**当前没显示**时才带 showTitleBlock:图签已经显示还传一次,连接器的
	// 前后对比会把「本来就是 true」判成「没应用」,于是写成功了却报失败(实测)。
	return out, !shown, nil
}

// warnIfSheetLost 在写图签后回读一次图纸几何:sheet 图元没了就当场报错。
//
// 判据不是「命令返回了什么」而是「画布还剩什么」—— 平台会在返回 true 的同时
// 把图框关掉,只有回读能发现。返回非 nil 表示画布已损坏,调用方应当作失败。
func warnIfSheetLost(cfg *appConfig, window string, stderr io.Writer) error {
	// 走 settleRead:写图签会让平台重建图框,紧接着那一读常常拿不到 sheet 图元。
	// 这条误报的代价特别大 —— 它会让人照提示去执行「整包回传写回 Border/Title
	// Block」,而那才是唯一真能把图框写坏的操作:判据把好板子推向危险操作。
	_, ok, rerr := settleRead(func() (bool, bool, error) {
		res, err := requestAction(cfg, "schematic.components.list", window, map[string]any{"includeBBox": true})
		if err != nil {
			return false, false, err
		}
		comps, perr := parseLayoutComps(res.Result)
		if perr != nil {
			return false, false, perr
		}
		for _, c := range comps {
			if c.ComponentType == "sheet" && c.BBox != nil {
				return true, true, nil
			}
		}
		return false, false, nil
	})
	if ok {
		return nil
	}
	// 回读根本没跑成 ≠ 图框没了。把真因带出来,否则「读失败」会被渲染成
	// 「图框被写坏了」,而那条提示指向的恰恰是唯一真能写坏图框的操作。
	if rerr != nil {
		return fmt.Errorf("写图签后无法回读本页图纸几何(%w)—— **未证实**图框是否还在,不要据此执行整包回传;"+
			"先用 `easyeda sch sheet-geometry` 单独确认", rerr)
	}
	return fmt.Errorf("写图签后本页找不到图纸边框(sheet 图元的 bbox)—— 停止依赖写入并冻结回读;" +
		"用 `easyeda sch sheet-geometry` 核对当前状态，不通过 titleblock 回写结构字段恢复图框")
}

// tbRequestedKeys 从用户的 patch 里取出**他真正要写的**明细项名。
//
// 与整包回传里的 key 集合不是一回事:后者包含全量明细项(平台要求整体回传)以及
// 我们主动按住的结构开关(Title Block / Border)。判「这次写成功没有」只该看前者。
func tbRequestedKeys(patch map[string]any) []string {
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// tbPatchLanded 回读图签,判**用户请求的每一项**是不是都已经是目标值。
//
// 为什么需要它:连接器的写后校验按「本次调用改变了什么」判定,于是
//  1. 我们主动按住的结构开关(值本来就对,写进去也不会变)永远落在 notApplied;
//  2. 重复写同一个值(幂等重跑、批量脚本重放)时,所有项都「没变化」,
//     校验判定 nothingProven 并抛错。
//
// 实测四页图签**内容全部写对了**(Name/Drawed/Description 都是目标值,
// Border/Title Block 都是 1),命令却报 ok:false —— 一次彻头彻尾的假失败。
// 而假失败比假成功更难缠:调用方会去重试、回滚,或者干脆认定这条路不通。
//
// 判据换成**画布的最终状态**:用户要的内容在不在图签上。这与「落地即判定」
// 是同一条原则 —— 平台的 applied 计数是过程量,画布才是结果。
func tbPatchLanded(cfg *appConfig, window string, patch map[string]any, expectedShow ...bool) (bool, []string) {
	if len(patch) == 0 {
		return false, []string{"empty patch"}
	}
	// 走 settleRead:写图签会让平台重建图签对象,首读常常还是旧值 —— 真机实测,
	// 首次写入(值真的变了)复核不过,而幂等重写(平台不重建)一路通过,症状正好反着。
	missing, ok, _ := settleRead(func() ([]string, bool, error) {
		res, err := requestAction(cfg, "schematic.titleblock.get", window, map[string]any{})
		if err != nil {
			return nil, false, err
		}
		full, _ := res.Result["titleBlockData"].(map[string]any)
		if full == nil {
			return nil, false, nil
		}
		var miss []string
		if len(expectedShow) > 0 {
			shown, known := res.Result["showTitleBlock"].(bool)
			if !known || shown != expectedShow[0] {
				miss = append(miss, "showTitleBlock")
			}
		}
		for _, k := range tbRequestedKeys(patch) {
			cur, _ := full[k].(map[string]any)
			if !tbItemMatches(cur, patch[k]) {
				miss = append(miss, k)
			}
		}
		return miss, len(miss) == 0, nil
	})
	return ok, missing
}

func tbItemMatches(current map[string]any, want any) bool {
	if current == nil {
		return false
	}
	if fields, ok := want.(map[string]any); ok {
		if len(fields) == 0 {
			return false
		}
		for k, v := range fields {
			actual, present := current[k]
			if !present {
				return false
			}
			switch k {
			case "value":
				if fmt.Sprint(actual) != fmt.Sprint(v) {
					return false
				}
			case "showTitle", "showValue":
				b, valid := v.(bool)
				got, known := actual.(bool)
				if !valid || !known || b != got {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	actual, present := current["value"]
	return present && fmt.Sprint(actual) == fmt.Sprint(want)
}
