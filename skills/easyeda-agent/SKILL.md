---
name: easyeda-agent
description: "Community EasyEDA Agent automation skill for EasyEDA Pro schematic and PCB work through the local easyeda-agent CLI/daemon/connector. Use when designing a board from scratch, editing or inspecting schematics, placing/wiring real LCSC/JLC library parts, syncing schematic changes into PCB, laying out PCB components, running EasyEDA DRC/check/layout-lint, exporting BOM/netlists/artifacts, querying the embedded circuit-block library (电路块库, `easyeda blocks ls/show/search`) for proven peripheral subcircuits (CH340/USB-serial, ESP32 auto-download, buck/buck-boost/LDO/charger, RS-485, RF 433M/GNSS, USB-hub, microSD), or using the bundled EasyEDA scripts and design conventions. 覆盖嘉立创EDA专业版原理图/PCB、布线、铺铜、板框、DRC/check。This is the merged public skill replacing easyeda-schematic, easyeda-pcb, easyeda-design-flow, and easyeda-conventions."
---

# EasyEDA Agent

Use the local `easyeda` CLI and daemon to operate EasyEDA Pro through typed,
observable actions. This is the community `easyeda-agent` workflow, not an official
EasyEDA skill; the suffix is intentional so users can distinguish it from upstream
EasyEDA tooling.

> **Source & docs:** https://github.com/zhoushoujianwork/easyeda-agent · Plugin
> marketplace listing coming soon. Install the CLI + connector per the repo README.

> **本 SKILL.md 顶部是「抗遗忘扫读区」——执行任何板级任务前先扫这几屏,别凭记忆走。**
> 顺序:① **铁律**(不可违反)→ ② **流程停点 / 档位默认 / 块地图** 三张速查 → ③ **顺序硬约束**。
> 具体细节**不堆在这里**——按下方 **What To Read** 的加载触发表按需读 reference(渐进式披露)。

## ① 铁律(不可违反)—— 抗遗忘扫读区

扫读式硬约束,任何模式都不问用户、不商量。违反 = 返工或坏板。每条带 `→` 指到细节文件。

1. **窗口操作前先 `easyeda health`** — 否则打到错窗口 / 无连接器。→ environment-setup.md
2. **只用 typed `easyeda` action** — 只有无对应 typed action **且**用户明确接受 debug 路径时才 `debug.exec_js`。
3. **mutate 前先 inspect** — 放/移/连/同步/存之前先读 doc/页/器件/引脚/板层/网络/规则,别盲改;破坏性操作(clear/delete/bulk import)先确认。
4. **无图纸不摆放/布线** — 找不到 sheet 立即停,让用户建/批准 A4(默认 A4)。→ design-flow S1
5. **PCB mutation(rip-up/route/delete/via/track)后先 `easyeda doc reload` 再读/判/DRC** — 否则 list/DRC 读 stale;同网 Connection Error 暴增多是 pour 连通性 stale(先 `pour-rebuild`),不是真断。→ pcb.md
6. **判对错只看 `list/check/drc/layout-lint`,不看截图** — 截图会 stale/blank;data 有内容但截图空 = 窗口没渲染(切前台),不是设计错。`pcb drc/check` 这类重画布计算**需 PCB 在前台**,超时=切前台**单发一次、绝不循环重试**(重发被 `ACTION_BUSY` 拒)。**录制/演示模式例外**:截图变交付物 → design-flow 录制/演示模式。
7. **每过一个阶段门显式 `save`(sch/PCB)** — place/wire/modify 只改内存,autosave 只兜底;整板每 ~10 件 save 一次。→ design-flow S 段 💾
8. **手工连任何已知外围前先查块库 `easyeda blocks`**(离线,无需 daemon/窗口)— 20 块/11 类目,照抄验证过的块只重绑端口。→ ② 块地图速查
9. **netflag 必须经真 wire 连、离 pin 非零距** — 重叠坐标 EasyEDA 不认作连接;禁零长 wire;多脚同名 pin 要全连(如多 GND、AMS1117 双 VOUT)。→ schematic.md
10. **RF/天线 keepout 覆盖每一层** — top+bottom no-copper + 内层 no-inner-electrical;top-only 会被底层 pour 灌到失谐。→ pcb.md
11. **丝印每个标记落在器件本体/courtyard 之外、装配后不被遮** — 端子塑料罩/卡座壳/按键帽会盖住 footprint 内的丝印 = 等于没标。→ design-flow P9
12. **禁用 `eda.sch_Netlist.getNetlist()`**(已废弃、悬空脚挂死)— 网表走 `sch read/check/netlist/export`;raw 路径不得已才 `getNetlistFile()` 读 `File.text()`。→ schematic.md / actions.md

## ② 流程停点 + 档位默认 + 块地图速查

**执行前先定位自己在哪个阶段、这一步是不是停点、走哪个档、这阶段要不要先查块。** 完整流程 S0–S6 / P0–P10
见 [`references/design-flow.md`](./references/design-flow.md);非平凡板(>~10 件或要交付/排 PCB)一律走它的 gated flow。
这里是执行时扫读用的顶层速查。

### 何时必须停手交回用户(里程碑档 = 真实用户默认)

| 停点 | 触发 | 要点 |
|---|---|---|
| ① S0 方案书 | 进 S1 前 | 架构/叠层/地策略/接口取向每条摊选项+坑+推荐让用户拍板;**必须落成磁盘文件**才算过门,不能停在对话里 |
| ② sch→PCB 前 | 原理图完成 | 网表逐条对齐 + **`sch drc` 与 `sch check` 都跑且都清零**(两引擎规则不重叠,只跑一个必漏规则)→ design-flow S5 |
| ③ 发板/交付前 | 导出制造 | 交付摘要说清偏差(降级决策/遗留 WARN) |
| P2 摆放前 | 布局起手 | 先问两决策:单/双面布局 + 焊接工艺(定封装下限) |
| P2 边缘接口件 | 端子/USB/SD/排针/按键/IPEX | 朝向 + 边序 = 装配体验,agent 猜不了,**必须用户确认**;先 `blocks show` 读块 placement 摊给用户 |
| P7 稠密板布线 | 见下档位 + P7 迷你清单 | **停下请用户在 EasyEDA 菜单点「布线→自动布线」**;交出去前必做两步见下方 P7 迷你清单,跑完再接手 |
| 破坏性操作 / 门禁失败 | clear/delete/bulk;`pcb new-board --force`(已绑板会搬走原理图=旧板原理图丢失);layout-lint ERROR / DRC fatal | 停在失败数据,不带病往下 |

里程碑档**只有这几处停**,不是每步都停(逐步档才每步停);全自动仅用于回归/CI/operator/录制。

### 档位默认(别自作主张改)

| 维度 | 默认 | 备注 |
|---|---|---|
| 交互模式 | **milestone(里程碑)** | 非逐步、非全自动 |
| 布线档 | 按 layout-lint ratsnest 密度选 | 稀疏(交叉<100)→ `route-short`;**稠密 → 请用户点原生自动布线(默认)**;全 headless 才 Freerouting(`pcb autoroute`,兜底,**不顶替默认**)。交出去前先跑 ↓P7 迷你清单 |
| 摆放优先级 | 孔 → 边缘件 → 主芯片+RF → 卫星件 | 只有卫星件交 auto-place;孔最先放 + 锁定 |
| 图纸 / 板框 | A4 / compact | 无尺寸信息时 compact;compact 时主芯片按**紧凑网格**播种(模块中心距≈包络+300~400mil,别撒 2000mil 外),摆位/判尺寸**只信 `pcb list --include-bbox` 实测 bbox**(含 courtyard,常比封装大 40%+),不猜标称 → design-flow P1/P2 |
| GND 内层 | `power-planes --gnd-plane` → 终态 PLANE | SIGNAL 铺→翻 PLANE→rebuild,不停在 SIGNAL |
| `pour-fit --replace` | **true(会清跨层同网 pour)** | 顶/底 GND pour 要显式 `--replace=false` |

### P7 交自动布线前必做两步(常被遗忘,已实测踩坑)

稠密板停手交用户点原生自动布线**之前**,这两步不做完就交出去 = 关键网被路由器冲掉或整个交给它不擅长的活:

1. **关键网先自布并锁定** — 电源先铜(`pcb fill` / `power-planes`,4 层走内电层)、差分/等长成对短布 → **`pcb track-lock`**
   锁死(否则自动布线器 / `pour-rebuild` 会把手布的关键线冲掉)。
2. **停手时必念「自动布线对话框」4 条**(漏一条毁掉第 1 步):① 「已有导线/过孔」选**保留**、绝不选「移除」
   ② 「布线图层」只勾**顶层+底层**、取消内层 1/2 ③ 「忽略网络」加**已在平面的电源网**(GND、3V3/VDD)
   ④ 其余默认。→ 细节 design-flow P7.0 + 自动布线对话框清单

### 块地图速查(块携带多维知识,按阶段读对应 map)

命中块后,不同阶段读块里不同的 map;每行统一**先 `easyeda blocks show <id>` 读对应 map**:

| 阶段 | 读块的 map | 内容 |
|---|---|---|
| S0 / S3 | `internal_nets` · `ports` · `parts` | 照抄拓扑(引脚用功能名零改号)/ 重绑边界网络 / 选型免做(parts 指回 standard-parts) |
| P2 | `placement` | 板边 / 朝向(edge/side/orientation,**须用户确认**) |
| P2 / P8 | `pcb_layout`(`*-adjacency` / `ep-*`) | 去耦·晶振贴脚距离(P2)/ EP 热过孔·接地缝合(P8) |
| P4 | `pcb_layout`(`rf-keepout` / `balun-mirror`) | RF 禁布 / 巴伦镜像(severity=must) |
| P7.0 | `signals` | 差分对 / 阻抗(`impedance_ohm`+`impedance_kind`)/ 等长(`length_match_mm`) |
| P9 | `silk` | 逐脚标注(`pins`/`label`/`note`) |

**搜索策略**:`blocks search` 命中 id/desc/category/port/part——按**功能**(rs485/buck/gnss)、**芯片**(ch340/cc1101)、
**端口网**(5V/USB_DP)三维轮换搜;一词没中换维度别急着手接;或 `blocks ls --category <power|usb|usb-serial|rf|comms|storage|sensing|mcu|mcu-support|indicator|button>` 浏览整类(11 类,`blocks ls` 看全 20 块)。

## ③ 顺序硬约束(反了必返工)

每条带 `→` design-flow 锚点;这些是**同级铁律级**的强顺序约束,散在深处易漏,汇总于此:

1. **摆放过 layout-lint 前不布线**(S3 摆放 → S5 layout-lint 门 → S4/P7 布线)→ design-flow S3/S5
2. **P6 可布性门在 P7 布线之前**(≥目标分、0 overlap、ratsnest 可控)→ design-flow P6
3. **P7.0 电源/差分先布并 `track-lock`,再交自动布线**(见上方 P7 迷你清单)→ design-flow P7.0
4. **禁布区 / 丝印(P4/P5)在布线 P7 之前**(布完再加会逼返工重绕)→ design-flow P4/P5
5. **改层数 / `outline-fit` 在铺铜布线之前** → design-flow P8
6. **PLANE 先铺 SIGNAL 再翻;PLANE 翻好后禁打异网 via**(官方缺陷 #32 不挖 anti-pad、`pour-rebuild` 不补救;换层先删 via 走外层,`pcb check via-crosses-plane` 会标出)→ design-flow P8

## What To Read(加载触发索引 —— load-more)

按走到的场景/阶段**按需**读对应 reference(渐进式披露),别预加载全部:

- `health` 显示 `windows: []` / `NO_CONNECTOR`,或改了连接器(`extension/`):读
  `references/environment-setup.md`。web 编辑器(`pro.lceda.cn`)+ chrome-devtools MCP 时
  agent 可自举全环境;**桌面客户端 chrome-devtools 够不到窗口,需用户手动开/切工程**(连接器照常附着,typed action 一样)。
- **整板 / 从零 / >~10 件,或走到某阶段拿不准**:先读 `references/design-flow.md`(流程脊柱 S0–S6 / P0–P10,顶部有阶段 TOC)。含 S0 事前摸底子步 `references/design-pre-analysis.md`(轻量摸底,可选、非门禁)。
- **布线阶段(P7)选档 / 关键网先行 / 自动布线对话框清单**:读 `references/design-flow.md` **P7 三档阶梯**——别停在 `pcb.md` 的命令手册(那里只给命令,布线档默认在 design-flow)。
- 架构权衡坑(真选择,非唯一答案——叠层、地策略、接口取向、成本档、单/双面、焊接工艺):读
  `references/design-decisions.md`;S0 从中产出方案书让用户确认。(RF/天线 keepout 是 guardrail 铁律 10,不进这张决策表。)
- **Schematic work**:读 `references/schematic.md`。
- **PCB work**:读 `references/pcb.md`(顶部有「块的 PCB 约束(先查)」+ 命令目录)。
- 查任一 typed action 签名、或 >5 步批量操作要用 playbook(`easyeda apply`):读 `references/actions.md`。
- **DRC / 制造规则地板与 fallback**:读 `references/fab-rules-jlcpcb.json`(live `pcb.drc.rules` 优先,此表作 fallback seed + clamp floors,**永不发出低于 manufacturingMin 的 track/via/gap**)。
- New/uncertain raw `eda.*` API:先 `easyeda api search/show`,再查官方 prodocs 参考页(方法为 `@alpha`/`@beta`/`@deprecated` 或有已知 upstream issue 时),把 caveat 记进 references 再固化成工作流。
- Schematic 布局规则:读 `references/schematic-layout-conventions.md`。
- PCB 摆放/布线规则:读 `references/pcb-layout-conventions.md`。
- CLI 摆放/布线硬坑 + auto-layout/autoconnect SOP:读 `references/auto-layout-sop.md`。
- 器件选型、JLC/LCSC 排名与标准化:读 `references/part-selection.md`(选型前**先查块**,块 `parts` 已固定标准外围选型)+ `references/standard-parts.json`。
- **电路块库**:`easyeda blocks ls/show/search`(离线,详见铁律 8 + 块地图速查);贡献一个新块见
  `references/standard-blocks-contributing.md`(验证过的外围回流入库,署名 + `validated` 门)。
- Netflag/netport 旋转真值:用 `references/orientation.json`;never hand-edit 派生的旋转表。
- 图纸/标题栏几何约定:读 `references/sheet-templates.json`。

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

**收尾回流(块库共建)**:若本板含**手工搭建且已跑通 `sch check` + DRC=0 / 网表逐网核实**的标准外围(库里没有的),
按 `references/standard-blocks-contributing.md` 顺手回流一个块(署名 + `validated` = 本次证据)——验证刚过正是入库时机,
**一次设计同时是一次贡献**。
