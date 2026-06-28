---
name: easyeda-design-flow
description: End-to-end EasyEDA board design orchestrator — the chief-EDA-engineer process spine. Use when the task is a whole board or a non-trivial design from scratch ("帮我画一个 …", "设计这块板", "原理图到 PCB 全流程", multi-module / >~10 parts / deliverable). It enforces a staged, gated workflow — pre-analysis → paginate → group by module → place by group (chip + peripherals together) → route in channels → DRC + layout-lint → adjust loop — and delegates concrete actions to the easyeda-schematic / easyeda-pcb skills and design rules to easyeda-conventions. For a one-off small edit (place a few parts, tweak one wire), use easyeda-schematic / easyeda-pcb directly instead.
---

# EasyEDA Design Flow — 首席EDA工程师流程脊柱

你是**首席EDA工程师**。整板/非平凡设计**不允许**「边想边随手摆」——那正是覆盖、外围乱飞、线压元件的根因。
按下面的**分阶段 + 硬门禁**走:每个阶段有明确产出和**过门条件**,**门不过不进下一阶段**。

> 这是**编排层**,不重复规则或动作细节:
> - 具体动作(place/wire/modify/move/align/…) → [`easyeda-schematic`](../easyeda-schematic/SKILL.md) / [`easyeda-pcb`](../easyeda-pcb/SKILL.md)
> - 设计规则(分区/间距/朝向/选型) → [`easyeda-conventions`](../easyeda-conventions/SKILL.md)
> - 本 skill 只负责**顺序、分组、门禁、自调闭环**。

## 核心原则

1. **先规划,后落子。** 没分页、没编组之前,一个元件都不放。
2. **按组摆,不按件摆。** 芯片和它的外围(去耦/晶振/上下拉/接口)是**一个整体**,一起定位、一起移动,组与组之间留出布线通道。
3. **每步可验证。** 摆放后用 `easyeda sch layout-lint` 拿**机械真值**判覆盖/间距,而不是靠肉眼或截图(截图可能 stale)。
4. **门不过就回退。** layout-lint 有 ERROR、DRC 有 fatal → 立刻调整,不带病往下走。
5. **过一阶段就存盘(硬规则)。** `place`/`wire`/`modify` 只改 EasyEDA **内存**,不 save 就**不落盘**——窗口重载、daemon 重启、EasyEDA 崩溃都会**丢未保存的工作**。daemon 默认开了**防抖 autosave(3s)** 兜底(变更停 3s 自动 `schematic.save`),但它是安全网不是替代:① 防抖窗口内进程挂掉仍丢最后几笔;② autosave 可能被 `--autosave-debounce 0` 关。所以**每个阶段门通过后仍显式 `easyeda sch save` 一次**(见各阶段 💾),即时落到已知良好点。本流程里 save 是既定步骤,无需逐次确认。

## 阶段流水线(原理图)

```
S0 预分析 → S1 分页💾 → S2 模块编组 → S3 按组摆放💾 → S4 通道布线💾 → S5 校验门 → S6 调整闭环💾
                                            ↑___________________________________|
```
> 💾 = 该阶段通过后 `easyeda sch save` 存盘检查点(见原则 5)。整板放置时,S3 每放完几组(或每 ~10 件)就 save 一次,别等全放完——崩一次就白干。

### S0 — 预分析(摸底)
- **做什么**:读懂设计——器件清单、电源树、功能模块划分、目标幅面。
- **怎么做**:见 conventions 的 `design-pre-analysis.md`(轻量摸底)。`easyeda health` 确认已连接。
- **产出**:模块清单(如 MCU、电源、USB、传感器、调试口…)+ 每个模块的器件归属。
- **过门条件**:每个器件都归到了某个模块;知道有几页、每页放哪些模块。

### S1 — 分页(先分页!)
- **做什么**:按模块/功能把设计**先分到几页**(电源一页、主控一页、接口一页…),别全堆一页。
- **怎么做**:`easyeda sch page-new` / `page-rename`;复杂模块独立成页。
- **💾 过门条件**:页结构建好,每页职责明确 → `easyeda sch save`。

### S2 — 模块编组
- **做什么**:在每页内,把「芯片 + 其外围电路」定义为一个**组**,并规划各组在页面上的**分区位置**(谁在左、谁在右、信号流向)。
- **怎么做**:分区/信号流向规则查 conventions 的 `schematic-layout-conventions.md`。此阶段只规划坐标分区,先不落子。
- **过门条件**:每个组有明确的目标矩形区域,组间预留了通道(不重叠的分区)。

### S3 — 按组摆放(芯片 + 外围一起)
- **做什么**:**逐组**放置——先放该组核心芯片,再把它的外围**就近**放在芯片周围(去耦贴电源脚、晶振贴时钟脚…),放完一组再下一组。
- **怎么做**:`easyeda sch place` + `sch modify`(设位号);坐标按 S2 的分区。库优先、选型规则见 easyeda-schematic / conventions。
- **💾 过门条件**:进入 S4 前**必须先过 S5 的 layout-lint**——本组无覆盖、组内外围紧凑、组间不挤。**有 ERROR 先回 S3 调整**(`sch move`/`align`/`distribute`)。过了就 `easyeda sch save`(整板放置每 ~10 件存一次,别等全放完)。

### S4 — 通道布线(留距离,别压元件)
- **做什么**:在组**摆放并通过 layout-lint 之后**再布线——信号走元件间的**空通道**,不要让导线压在元件或外围上。
- **怎么做**:布线/flag/去耦规则见 conventions 的 `auto-layout-sop.md`(信号=本地正交线、flag 仅电源地、绝不穿引脚)。
- **💾 过门后**:`easyeda sch save` 存盘,再进入 S5。

### S5 — 校验门(机械真值,不是肉眼)
**两个门必须都过**,否则回 S3/S4:
1. **布局门** `easyeda sch layout-lint`(可加 `--min-gap`、`--all-pages`)
   - **任何 `overlap` ERROR = 必须修**(命令非零退出,可直接当 gate)。
   - `spacing` WARN = 评估是否太挤,外围贴芯片可接受、模块间过近要拉开。
   - **默认只检真实器件**:图框/标题栏(sheet)与 netflag/netport 等非器件原语已自动排除,不会再误报"器件压图框"(issue #13);要连这些一起检查才加 `--include-non-parts`。
2. **电气门** `easyeda sch drc`(+ `scripts/lint.sh <project>` 数据 lint)
   - **逐条**输出 `LEVEL <rule> <message> @(x,y)`;命令**仅在 `fatal>0`(error/fatal)时非零退出**,可直接当 gate(`0 fatal` = 过门)。
   - fatal / 未连接网络 = 必须修;`summary.warn` 警告(如未用 IO 悬空)逐条复核、可接受则放行。
- ⚠️ **判状态看数据(`sch list` / layout-lint / drc),不看截图**(API 改动后画布可能不重绘 → 截图 stale)。

### S6 — 调整闭环(立刻调,再验)
- layout-lint 报覆盖 → `sch move`/`align`/`distribute` 把冲突元件挪开 → **重跑 layout-lint**。
- DRC 报错 → 补线/补 flag → **重跑 drc**。
- **💾 循环直到两个门都干净,再 `easyeda sch save` 收尾**。这就是「DRC 后立刻调整」要的闭环。

## 切到 PCB
原理图过门(DRC 干净 + 已保存)后,转 [`easyeda-pcb`](../easyeda-pcb/SKILL.md):`import_changes` 同步器件 → 板框优先 → 同样的「按组摆放 → 校验 → 调整」节奏(PCB 有 align/distribute/cluster-arrange)。

## 反模式(实测踩过的坑)
- ❌ 全堆一页、不分页 → S1 强制分页。
- ❌ 一件一件随手摆、芯片和外围分家 → S2/S3 按组摆。
- ❌ 摆完不验就布线 → 元件覆盖、线压外围。S5 layout-lint 门拦住。
- ❌ 靠截图判断有没有覆盖 → 截图 stale。看 `layout-lint` 数据。
- ❌ DRC 报错放着不管 → S6 闭环必须清零再 save。
- ❌ **放/连一大堆都不 save** → 窗口重载或 daemon 重启全丢(实测踩过)。每阶段门后存盘,整板放置每 ~10 件存一次。
