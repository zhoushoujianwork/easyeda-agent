---
name: easyeda-agent
description: "通过本地 easyeda CLI、daemon 和连接器操作嘉立创EDA专业版（EasyEDA Pro）：设计或修复原理图、核对器件与引脚网表、从 JSON 组合功能电路并 Apply、布局布线 PCB、运行检查和导出制造文件。适用于已有 EDA 工程操作及数据驱动电路设计。"
license: MIT
compatibility: "Requires the local easyeda CLI/daemon and EasyEDA Agent Connector with Allow external interaction enabled. Python 3 is used by bundled helpers; online library lookup and updates need network access."
metadata:
  author: zhoushoujianwork
  version: "1.4.9-dev.1"
  homepage: "https://github.com/zhoushoujianwork/easyeda-agent"
---

# EasyEDA Agent

用 typed CLI 经 WebSocket 调用 EasyEDA Pro 官方 `eda.*` API。CLI/daemon、此 Skill 和
连接器是配套组成部分；CLI/daemon 与 Skill 必须同版，连接器按 major.minor 兼容线对齐。
EasyEDA Pro 是宿主。安装、升级或连接异常时读
[environment-setup.md](references/environment-setup.md)。

## 开始工作

### 强制会话版本门禁

用户明确选择本地开发验证时，第一条命令改为
`easyeda update --local-dir <已构建目录> --check --exit-code`，不查询 GitHub。
该模式要求本地包校验、CLI 文件及完整 Skill 内容一致，daemon 和每个 Connector 精确同开发版
（含 `-dev.N`）；失败不允许 Apply。构建/安装见 environment-setup；不是改 `.version` 或跳过检查。
离线开发、构建和单元测试可在运行时尚未安装时进行，不能称为现场验证通过。
以下 latest 规则用于默认正式版模式；升级后的新会话要求两种模式都适用。

每个新 Agent 会话必须先运行 `easyeda update --check --exit-code`。这是本 Skill 的第一条
命令，先于项目读取、离线规划、`health` 和任何 EDA action。该命令查询 GitHub latest
Release；当前 CLI、已安装的当前客户端 Skill、正在运行的 daemon 必须可验证且**精确等于
latest**，所有已连接 EasyEDA 窗口里的 Connector 必须与 latest 共享 `major.minor` 兼容线，
才返回 0。Connector 仅有 patch 差异属于正常兼容，不要求升级插件市场版本。`ahead`、开发
构建、版本未知、daemon 未运行、没有连接器窗口，以及 Connector 跨 minor/major 都不是通过。

门禁非 0 时立即停止当前 EDA 任务，按 [environment-setup.md](references/environment-setup.md)
完成升级：CLI 或 Skill 不符先运行 `easyeda update`；daemon 不符则用新 CLI 重启；仅当
Connector 跨 minor/major 不兼容时，安装命令打印的同一兼容线 `.eext`，完全退出并重开
EasyEDA。纯 patch 更新不升级 Connector，也不要求重开 EasyEDA。不得用固定旧
`--version`、`--preserve`、`--skip-version-check` 或仅看 `health` 绕过 latest 门禁。

**只要 CLI、Skill、daemon 或 Connector 发生过升级/替换，本会话不得继续，也不得在本会话
内把重新检查当作放行。明确要求用户关闭当前 Agent 会话并新开会话，从本节第一条命令重新
开始。** 这是因为当前会话已经载入旧 Skill；重启 daemon 或 EasyEDA 不能刷新 Agent 指令。

门禁返回 0 后：

1. 按用户任务选择下表中的流程，只加载相关参考。已有项目的小修复沿用已确认的需求和授权。
2. 运行 `easyeda health` 确认工程、活动页和连接器。
3. 手动命令用 `--project <project>` 指定工程；变更带 `--doc <page>`，操作已有页面。
   已生成的受保护 Apply 队列沿用其固定目标，不再用名称覆盖。
   先读取将要修改的器件、引脚、网络及几何；位号或 primitiveId 不明确时不能盲写。
4. 以 `easyeda <domain> <command> --help` 和 `easyeda actions` 为参数真值。
   MCP 若可用，只是同一套 CLI/typed action 的入口。

| 任务 | 先读 |
|---|---|
| 本地原理图数据、版本对账、Lib 组合、修复位号、Apply | [schematic-data.md](references/schematic-data.md) |
| 已有原理图检查或器件/连线小修 | [schematic.md](references/schematic.md)；具体接线见 [schematic-wiring.md](references/schematic-wiring.md) |
| 原理图排版、已有连线的移动/整理 | [schematic-placement.md](references/schematic-placement.md)、[auto-layout-sop.md](references/auto-layout-sop.md) |
| 从需求到整板、原理图转 PCB | [design-flow.md](references/design-flow.md)；未确定的设计选项见 [design-decisions.md](references/design-decisions.md) |
| PCB 放置/布线/检查 | [pcb.md](references/pcb.md)，再按任务读 [pcb-layout.md](references/pcb-layout.md) 或 [pcb-routing.md](references/pcb-routing.md) |
| 选型、库器件、手册与标准电路 | [part-selection.md](references/part-selection.md)、[standard-parts.json](references/standard-parts.json)；先 `easyeda blocks search` 查可复用电路 |
| Altium Designer / 外部工程导入 | [project-import.md](references/project-import.md)；当前由 GUI 导入，再用 typed 读取与门禁核验 |
| 原理图/PCB 绘图规范 | [schematic-layout-conventions.md](references/schematic-layout-conventions.md)、[pcb-layout-conventions.md](references/pcb-layout-conventions.md) |
| 制造规则 | [pcb-design-rules.md](references/pcb-design-rules.md)、[fab-rules-jlcpcb.json](references/fab-rules-jlcpcb.json) |
| action 或队列字段 | [actions.md](references/actions.md)；未知官方接口先 `easyeda api search/show` |
| 查找或贡献公共复用数据 | [reusable-module-library.md](references/reusable-module-library.md)；拓扑模板再读 [standard-blocks-contributing.md](references/standard-blocks-contributing.md) |

## 1.4 原理图主流程

逐芯片分区：每个独立功能核心及其专属外围独立 zone；普通数据不必注册为 Lib。
`sch layout-plan --zones` 离线计算显式分区，各区分别交 compose 生成独立框；
字段与尚未覆盖的自动归属边界见 [schematic-data.md](references/schematic-data.md)。

**先确定连接数据，再计算几何，最后转换与回读。** 新设计依据具体型号的数据手册和典型电路；
已有图先导出 `sch connectivity`，未知引脚或网不能靠截图推断。
器件参数按 [part-selection.md](references/part-selection.md) 留存来源原文和单位换算；
不从料号数字猜阻值，区分 `mΩ` 与 `MΩ`，参数未核实或相互冲突时不能据此落图。

- `component.id` 是不透明稳定 ID，`ref` 是显示位号，功能名存 `role`。
  保留正常位号的拼写、前导零与顺序；错误名称用 `sch designators` 按官方库前缀修复，
  端子也不强制改为 `J`。不从 ID 反推 ref，不覆盖原生 `uniqueId`。
- 按功能组织 Lib：核心器件加外围，以真实短线连接。VCC/GND 可局部重复放置；
  标签用于电源或模块边界，不替代连接图。多引脚同功能（例如 AMS1117 双 VOUT）逐脚核对。
- 普通器件集合可用 `sch layout-plan` 直接计算局部布局，无需先建 Lib；它与 `lib-layout`
  共用纯计算内核。局部结果不包含身份/纸张/现场验收，不能直接作为 Apply 队列。
- 复用前先查 `library/modules/catalog.json`。`draft` 只表示已有脱敏功能证据，不能直接绘图；
  `topology_ready` 可转成实例连接核心；只有 `compose_ready` 且通过目录审计的资产才能直接交给
  `sch compose`。Block 是可参数化的拓扑配方，不是 Lib 实例或运行时布局层。
- 从官方接口读取器件与引脚几何，保留测量源；用 `sch lib-layout` 从连接图与测量计算 Lib 内部位置与导线。
  自编计算须保留脚本和参数，让输入能够重现目标 JSON。`sch compose` 消费已设计的模块几何，计算端子直线错长、
  紧凑标题和左上起排的 Z 字布局。每框保留自身紧凑高度，同行顶齐，按该行最大高度换行。默认 A4 一页，容量不足按已确认的功能拆页。
  它不自动补电路、旋转器件、缩放符号或创建页面。
- 每个 Lib 带粉色虚线框和 **0.2 inch = 20 raw** 标题。固定贴边尺寸为 **10 raw**，
  有实测 `sheetBorder` 时保证虚线笔画到图纸内边框至少 10 raw；缺少时注明边界回退。
  标题与内容净距 **5 raw**。标题可放上下空档；本版本不生成独立 Notes。
- 生成 `sch apply` 队列前读取目标页新鲜快照。覆盖不同图面用 `compose --replace`，
  该路径会清目标页并保留纸张，必须在用户已授权重建的范围内使用。
- 完整执行队列，回读全部 pin→net/NC、器件身份、线段及模块框；运行检查并显式保存。
  连接正确与布局可读都要验证，不能以截图或单个 DRC 数字代替数据对账。

数据字段、可运行命令及失败恢复集中在 [schematic-data.md](references/schematic-data.md)。
`sch design-diff` 对账位号、库身份、引脚及几何；两份完整计划还比较导线/框/标题。
覆盖范围和未验证项随结果报告，不能把 canonical 一致当作实际图面已同步。
`sch plan` 只支持明确的标记连接增量；目标同时取消该脚 NC 并新增明确标记连接时，
队列先清该脚 NC、核对中间状态，再连接并回读；禁止单独清 NC 或跳步执行。
`materialize` 只负责基础放置，不能代替完整 Lib 组合。

布局拥挤先判断是局部几何问题还是纸张容量不足，再明确选择分页或放大纸张；
具体决策与恢复步骤见 [容量不足处理](references/schematic-placement.md#容量不足处理)。
已授权分页可直接执行。当前没有可靠的纸张尺寸修改 API；选择放大纸张时，告知用户
目标页和所需尺寸，请用户在编辑器修改，随后重新读取纸张几何再继续。
原理图 `sch autolayout` 与 PCB 自动布线是不同功能，按各自参考使用。

## 执行与验证约束

- typed action 已有对应能力时使用它；无对应能力且用户接受调试路径时，才用 `debug.exec_js`。
- `.SchDoc` / `.PcbDoc` 当前没有可用的程序化工程导入入口；按
  [project-import.md](references/project-import.md) 走 EasyEDA GUI，导入后再由 typed 命令核验。
- 使用真实非零导线连接 netflag 与 pin，坐标重合不算连接。原理图坐标 **y 向上**，网格 5 raw。
  符号方向以 [orientation.json](references/orientation.json) 和实际回读为准。
- 保留明确 NC，不删除器件物理引脚，也不将缺失连接自动改为 NC。
  原样重建未完成图时，官方快照明确返回 `net:""` 和 `noConnected:false` 的脚可用
  `connectionState:"unconnected"` 保留；字段缺失或未声明状态仍属未知。悬空仍保留电气警告，
  数据完整与绘图成功不代表电气设计合格，严格门禁仍须执行。
  网表使用 `sch read/check/netlist`；不调用已废弃、可能挂起的 `sch_Netlist.getNetlist()`。
- 写入超时或部分成功后先回读，不盲重试。受保护队列不能用 `--resume/--from/--to` 跳过守卫；
  从实际状态重新生成。Apply 不提供事务撤销；autosave 仅兜底，检查点须显式 `sch save` / `pcb save`。
- 已有用户授权持续有效，不因流程表重复索取许可。新出现的破坏性范围、未决电气/机械要求才需澄清。
  门禁失败时先区分“检查没运行”与“设计不合格”，不靠关闭检查取得通过。
- PCB 变更后按命令提示 `doc reload` 再检查。保留分档放置、手焊可达性、关键网、RF 全层 keepout、
  丝印极性和制造规则要求；详见 PCB 流程。阻塞错误、未评估的 WARN 或未运行的项目不能记为通过。

## 验证交付

本地预览使用固定 `sch layout-render --from render.json --out layout.svg`，不再临时生成
绘图脚本。当前只输出布局图，不打印差异图解；输入和能力边界见 schematic-data.md。

说明修改范围、源数据与实际图面的差异、验证结果、已保存页面及尚未解决的问题。
`layout-lint` 检查几何，pin→net 黄金表检查接对与否，`sch gate --strict` 汇总原理图门禁。
官方 DRC 可能只返回聚合数；INFO/WARN 应单列，不能把“0 fatal”称为全部通过。
`layout-score` 的逐维结果、`skipped/degraded` 是诊断，不代替硬门。

用 `sch export-image` 生成官方导图辅助确认文字与可读性；原生视口截图可能未刷新。
PCB 制造交付还须确认层叠、GND、电源、丝印与导出文件。离线单元测试或一个图页验证，
均不等于从客户需求到 PCB 的全流程验收。

常用辅助脚本：`scripts/lint.sh`、`bom-enrich.py`、`parts-select.py`、`parts-add.py`、
`blocks-pin-audit.py`、`modules-audit.py`、`tests/run.py`。按对应参考使用；具体参数先看脚本 `--help`。
