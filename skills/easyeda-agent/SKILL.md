---
name: easyeda-agent
description: "Community EasyEDA Agent automation skill for EasyEDA Pro schematic and PCB work through the local easyeda-agent CLI/daemon/connector. Use when designing a board from scratch, editing or inspecting schematics, placing/wiring real LCSC/JLC library parts, syncing schematic changes into PCB, laying out PCB components, running EasyEDA DRC/check/layout-lint, exporting BOM/netlists/artifacts, or using the bundled EasyEDA scripts and design conventions. This is the merged public skill replacing easyeda-schematic, easyeda-pcb, easyeda-design-flow, and easyeda-conventions."
---

# EasyEDA Agent

Use the local `easyeda` CLI and daemon to operate EasyEDA Pro through typed,
observable actions. This is the community `easyeda-agent` workflow, not an official
EasyEDA skill; the suffix is intentional so users can distinguish it from upstream
EasyEDA tooling.

> **Source & docs:** https://github.com/zhoushoujianwork/easyeda-agent · Plugin
> marketplace listing coming soon. Install the CLI + connector per the repo README.

## 铁律(不可违反)

扫读式硬约束,任何模式都不问用户、不商量。违反 = 返工或坏板。

1. **窗口操作前先 `easyeda health`** — 否则打到错窗口 / 无连接器。
2. **无图纸不摆放/布线** — 找不到 sheet 立即停,让用户建/批准 A4(默认 A4)。→ design-flow S1
3. **PCB mutation(rip-up/route/delete/via/track)后先 `easyeda doc reload` 再读/判/DRC** — 否则 list/DRC 读 stale;同网 Connection Error 暴增多是 pour 连通性 stale(先 `pour-rebuild`),不是真断。→ pcb.md
4. **判对错只看 `list/check/drc/layout-lint`,不看截图** — 截图会 stale/blank;data 有内容但截图空 = 窗口没渲染(切前台),不是设计错。→ Core Rules 7
5. **每过一个阶段门显式 `save`(sch/PCB)** — place/wire/modify 只改内存,autosave 只兜底;整板每 ~10 件 save 一次。
6. **手工连任何已知外围前先 `easyeda blocks search`** — CH340/自动下载/去抖/USB-hub/buck 照抄验证过的块,只重绑端口。→ Core Rules 4b
7. **netflag 必须经真 wire 连、离 pin 非零距** — 重叠坐标 EasyEDA 不认作连接;禁零长 wire;多脚同名 pin 要全连(如多 GND、AMS1117 双 VOUT)。→ schematic.md
8. **RF/天线 keepout 覆盖每一层** — top+bottom no-copper + 内层 no-inner-electrical;top-only 会被底层 pour 灌到失谐。→ pcb.md
9. **禁用 `eda.sch_Netlist.getNetlist()`**(已废弃、悬空脚挂死)— 网表走 `sch read/check/netlist`。→ Core Rules 3a

## 流程停点 + 档位默认速查

**执行前先定位自己在哪个阶段、这一步是不是停点、走哪个档。** 完整流程 S0–S6 / P0–P10 见
[`references/design-flow.md`](./references/design-flow.md);这里是执行时扫读用的顶层速查。

### 何时必须停手交回用户(里程碑档 = 真实用户默认)

| 停点 | 触发 | 要点 |
|---|---|---|
| ① S0 方案书 | 进 S1 前 | 架构/叠层/地策略/接口取向每条摊选项+坑+推荐让用户拍板;**必须落成磁盘文件**才算过门,不能停在对话里 |
| ② sch→PCB 前 | 原理图完成 | 转板前给出「网表全对/门禁全过」的可判断证据 |
| ③ 发板/交付前 | 导出制造 | 交付摘要说清偏差(降级决策/遗留 WARN) |
| P2 摆放前 | 布局起手 | 先问两决策:单/双面布局 + 焊接工艺(定封装下限) |
| P2 边缘接口件 | 端子/USB/SD/排针/按键/IPEX | 朝向 + 边序 = 装配体验,agent 猜不了,**必须用户确认** |
| P7 稠密板布线 | 见下档位 | **停下请用户在 EasyEDA 菜单点「布线→自动布线」**,跑完再接手 |
| 破坏性操作 / 门禁失败 | clear/delete/bulk;layout-lint ERROR / DRC fatal | 停在失败数据,不带病往下 |

里程碑档**只有这几处停**,不是每步都停(逐步档才每步停);全自动仅用于回归/CI/operator/录制。

### 档位默认(别自作主张改)

| 维度 | 默认 | 备注 |
|---|---|---|
| 交互模式 | **milestone(里程碑)** | 非逐步、非全自动 |
| 布线档 | 按 layout-lint ratsnest 密度选 | 稀疏(交叉<100)→ `route-short`;**稠密 → 请用户点原生自动布线(默认)**;全 headless 才 Freerouting(`pcb autoroute`,兜底,**不顶替默认**) |
| 摆放优先级 | 孔 → 边缘件 → 主芯片+RF → 卫星件 | 只有卫星件交 auto-place;孔最先放 + 锁定 |
| 图纸 / 板框 | A4 / compact | 无尺寸信息时 compact,不摊大饼 |
| GND 内层 | `power-planes --gnd-plane` → 终态 PLANE | SIGNAL 铺→翻 PLANE→rebuild,不停在 SIGNAL |
| `pour-fit --replace` | **true(会清跨层同网 pour)** | 顶/底 GND pour 要显式 `--replace=false` |

**顺序硬约束**(反了必返工):禁布区/丝印(P4/P5)必须在布线 P7 **之前**;改层数/`outline-fit` 在铺铜布线之前;PLANE 先铺 SIGNAL 再翻。

## Core Rules

1. Run `easyeda health` before any window-scoped action.
2. Use typed `easyeda` actions. Use raw `debug.exec_js` only when a typed action is
   missing and the user explicitly accepts a debug path.
3. Inspect before mutating: read docs/pages, components, pins, board/layers/nets, and
   relevant rules before placing, moving, wiring, syncing, or saving.
3a. **禁 `eda.sch_Netlist.getNetlist()`**(已废弃、悬空脚可无限挂死,easyeda/pro-api-sdk#30)。
    网表走 `easyeda sch read/check/netlist/export`;raw 路径不得已才 `eda.sch_ManufactureData.getNetlistFile()` 读 `File.text()`。
4. Confirm before destructive operations such as clear/delete/import bulk changes.
4a. **`sch autoconnect` 幂等**(重跑同 spec 安全,已连脚 skip,改网要 `--replace`);
    **`sch connect` 非幂等** — 重发前先 `sch read` 核对。机制见 `references/schematic.md`。
4b. **Block-first(电路块库):** 手工连已知外围前先查块库 —— `easyeda blocks ls / show <id> /
    search <query>`(离线,无需 daemon/窗口/skill 文件)。命中就照抄拓扑 + 重绑端口(ports)+
    重分 RefDes(引脚按功能名,零改号);无命中才手工连。验证过的新外围按
    `references/standard-blocks-contributing.md` 回流(署名 + `validated` gate)。
5. 非平凡板走 gated flow(pre-analysis → 分页 → 编组 → 组放置 → 通道布线 →
   DRC/check/layout-lint → 调整 → save 检查点)。停点与档位默认见上方速查;完整定义
   `references/design-flow.md`。
6. 显式 `sch save` / PCB save 落检查点;debounced autosave 只兜底(见铁律 5)。
7. **判对错只看 `list/check/drc/layout-lint`,不看截图。** data 有内容却截图空白/不变 =
   窗口没渲染(需切前台),不是设计错 —— 无 API 能重绘隐藏窗口。**录制/演示模式例外:**
   图变交付物,用 `easyeda pcb stage-snapshot --stage …`(自动 gate blank/stale/错文档,
   非零退出),绝不拿 data-rendered 图冒充实拍。细节见 `references/design-flow.md` → 录制模式。

## What To Read

- `health` 显示 `windows: []` / `NO_CONNECTOR`,或改了连接器(`extension/`):读
  `references/environment-setup.md`。web 编辑器(`pro.lceda.cn`)+ chrome-devtools MCP 时
  agent 可自举全环境(开编辑器→开工程→验附着→IndexedDB 热重载);**桌面客户端 chrome-devtools
  够不到窗口,需用户手动开/切工程**(连接器照常附着,typed action 完全一样)。
- Whole board, from scratch, or >~10 parts: read `references/design-flow.md` first.
- Architecture trade-off pitfalls (genuine choices, not one right answer — stackup,
  ground strategy, connector orientation, part cost tier, single/double-side, solder
  process): read `references/design-decisions.md`; the S0 design-proposal stage produces a
  proposal from these for the user to confirm. (RF/antenna keepout is a guardrail — 铁律 8 —
  not a Decision; it stays out of this list.)
- Schematic work: read `references/schematic.md` and `references/actions.md`.
- PCB work: read `references/pcb.md`.
- New/uncertain raw `eda.*` API use: first run `easyeda api search/show`, then check
  the matching official prodocs reference page when the method is `@alpha`, `@beta`,
  `@deprecated`, or has a known upstream issue. Record the caveat in references before
  turning it into an agent workflow.
- Schematic layout rules: read `references/schematic-layout-conventions.md`.
- PCB placement/routing rules: read `references/pcb-layout-conventions.md`.
- CLI placement/routing hard pits and auto-layout/autoconnect SOP: read
  `references/auto-layout-sop.md`.
- Part selection, JLC/LCSC ranking, and standardization: read
  `references/part-selection.md` and use `references/standard-parts.json`.
- **电路块库:** `easyeda blocks ls/show/search`(离线,详见 Core Rules 4b);贡献一个新块见
  `references/standard-blocks-contributing.md`。
- Netflag/netport rotation truth: use `references/orientation.json`; never hand-edit
  derived rotation tables.
- Sheet/title-block geometry conventions: read `references/sheet-templates.json`.

## Bundled Scripts

Scripts live in `scripts/` and are intended to be run directly when useful:

- `scripts/lint.sh <project>`: live schematic lint with optional diff baseline.
- `scripts/tests/run.py`: linter rule-trust harness; run after changes to
  `orientation.json`, linter rules, fixtures, or connector orientation facts.
- `scripts/bom-enrich.py <bom.tsv/csv>`: fill EasyEDA BOM Supplier Part values from
  `standard-parts.json`.
- `scripts/parts-add.py`: append resolved library parts into `standard-parts.json`.
- `scripts/parts-select.py`: deterministic part-selection helper.
- `scripts/calibrate.js`: live bbox calibration for netflag/netport orientation after
  importing a new connector build.

(电路块库的浏览/查找是离线 CLI `easyeda blocks`,不是 `scripts/` 脚本;块校验是 Go 测试
`go test ./internal/blocks/`,跟 `make test`/CI 跑。)

## Deliverables

Summarize changed primitives, commands run, DRC/check/lint status, saved checkpoints,
and artifact paths. If a gate cannot pass, stop at the failing data, explain the next
repair step, and do not claim the design is complete. 录制/演示模式下,额外列出每张阶段图并
标注 **native EasyEDA 截图** 或 **data-rendered 图**,显式报告任何 stale/替换帧。
