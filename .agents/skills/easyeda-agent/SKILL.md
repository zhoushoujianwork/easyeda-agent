---
name: easyeda-agent
description: "通过本地 easyeda CLI、daemon 和连接器操作嘉立创EDA专业版（EasyEDA Pro）：依据需求与真实器件数据设计原理图、布局布线 PCB，并回读连接、几何、DRC 与保存结果。适用于已有工程操作及参数化电路设计。"
license: MIT
metadata:
  author: zhoushoujianwork
  version: "1.16.0"
  homepage: "https://github.com/zhoushoujianwork/easyeda-agent"
---

# EasyEDA Agent

用本地 `easyeda` CLI、daemon 和连接器调用 EasyEDA Pro 官方 `eda.*` 接口。
依据需求与器件资料独立设计；有合适样例时复用参数结构和已验证方法。
模型可选择器件、功能划分及几何生成方法；执行与验收遵守同一数据契约。

## 工作循环

1. 明确供电、接口、机械与装配约束；已有授权内自主完成常规取舍，只澄清影响目标的缺失信息。
2. 现场读写前用 `easyeda health` 确认用户已打开的内置浏览器 Web EDA、工程和文档。
   宿主要求 V4；连接或版本异常按[环境配置](references/environment-setup.md)处理，不擅自切换桌面版。
3. 保留原始快照，核实真实库身份、完整引脚、网络、NC、几何与规则；未知不能当作空或 NC。
4. 在源副本中明确连接、核心外围归属、单位与约束，生成候选并检查和预览。
   求解器、样例或模型生成的数据均须满足[数据契约](references/schematic-data.md#数据驱动架构基准)；不强制某个求解器。
5. 用当前 `--help` / `easyeda actions` 核对接口，从新鲜快照生成计划，支持 dry-run 时先检查。
   仅经 Cobra 子命令、typed action 或 `easyeda apply` 写入；同一窗口的调用串行执行。
6. 操作后检查宿主弹窗/进度阻塞，按[恢复流程](references/environment-setup.md#宿主弹窗与进度阻塞)处理；CLI 成功不等于界面无阻塞。
   回读身份、逐脚连接和实际几何，与目标对账。部分成功、超时或 stale ID 时先回读，再修源并重算。
7. 稳定检查点显式保存；最终完成 `save → doc reload → fresh readback/render`。
   必须返回布尔 `saved:true`；`saved:false`、缺失或其他类型均为失败，外层 `ok:true` 和 autosave 不替代保存证据。
8. 交付具体结果、证据与未覆盖项；检查、评分、版本或旧 `workflow/stage` 状态不作为普通动作许可。

## 设计与执行边界

- 工程编辑、保存、重载和验证使用 typed 接口，不用 GUI 或 `debug.exec_js` 绕过缺失设计接口；
  已有授权操作引起的宿主确认/取消弹窗可按恢复流程处理，不能扩展为画布或属性面板编辑。
  接口缺失或宿主持续加载、对象不可读时停止依赖写入，保留输入与错误，标 `unsupported/incomplete`，先修工具验证。
- 原始观测不可改写；改源数据后重新生成受保护队列，不手改队列、SVG 或回读来掩盖差异。
- 库中的相近型号须核对引脚和封装后复用；多变体未支持选择时拒绝，不默认取第一项。
- 原理图用 raw、y 向上、5 raw 网格；PCB 命令通常用 mil。明确原点、anchor 与 bbox 语义。
- 保留全部物理脚与明确 NC。`netAmbiguous:true` 表示歧义，不能转成未连接；标记通过非零导线接脚。
  同名网络、同框或高分不能代替要求的外围实际直连；功能归属与网名分别核验（`sch check` 按已登记模块报 `peripheral-not-direct`，严格门禁失败）。
- 位号参与碰撞与入框检查；其余属性保留原值，最终导图仍须检查全部可见文字，不能由零碰撞推断无叠字。
- PCB 按真实机械、回流与器件要求安排布局；采集缺测不等于空，检查须说明几何与铜的覆盖范围。
  完成前按[布局规则](references/pcb-layout.md)通过连续两轮自检，第二轮包含保存重载和新回读；修复后重来。
  展示最新回读版本并获用户确认后进入整板布线；确认前仅允许规则范围内的电源模块局部连接。
- 临时隐藏属性只能使用可恢复的 typed 视图接口，保存旧值、恢复并回读；不改属性内容来隐藏问题。
- DRC、连接检查、几何和图面分别说明覆盖事实；原生 `pcb drc` 的 `passed:false` 是失败，SCH 的 INFO 不等于错误。
  离线通过、导出成功或截图不能代替现场持久化、制造或硬件验证。

## 按任务读取

只加载当前任务需要的参考；命令参数以当前 CLI help 为准。

| 任务 | 参考 |
|---|---|
| 需求、选型与设计取舍 | [设计流程](references/design-flow.md)、[决策参考](references/design-decisions.md)、[器件选型](references/part-selection.md) |
| 原理图数据、布局与 Apply | [数据契约](references/schematic-data.md)、[执行步骤](references/auto-layout-sop.md) |
| 原理图检查或局部修复 | [原理图](references/schematic.md)、[放置](references/schematic-placement.md)、[连接](references/schematic-wiring.md) |
| 按考试评分细则出板（规则→布局→布线→泪滴→丝印） | [考试推荐流程](references/exam-pcb-workflow.md) |
| PCB 布局与机械 | [PCB](references/pcb.md)、[布局](references/pcb-layout.md) |
| PCB 布线、铺铜与禁布区 | [布线](references/pcb-routing.md) |
| 规则与网络类 | [配置](references/pcb-config.md) |
| 库器件或自建库 | [选型](references/part-selection.md)、[建库](references/library-authoring.md)、[候选表](references/standard-parts.json) |
| 新建、打开、导出或迁移工程 | [工程操作](references/project-import.md) |
| 交付原理图、PCB 正反面与图册 | [清晰设计图交付](references/design-drawings.md) |
| 由已有 PCB 生成 3D 打印外壳（上下壳/推盖、开孔、螺丝柱、卡扣） | [参数化外壳](references/pcb.md#参数化外壳离线生成-stl) |
| 连接、升级或保存异常 | [环境配置](references/environment-setup.md)、[Action 边界](references/actions.md) |
| 原始文档源码探针 | [源码读取与同源回写](references/document-source.md)；不接受任意新源码替代设计接口 |
| 复用案例与历史限制 | [样例索引](references/examples/index.md)；旧案例仅适用于其记录的范围 |

## 验证与维护

状态用 `source-only`、`offline-verified`、`live-verified`；后者要求真实写入并完成保存重载回读。
候选可标 `candidate-unverified` / `candidate-rejected`；未实现能力用 `planned` / `unsupported`。
新增可复用样例记录来源、开始状态、参数/单位、命令、实际结果、修法和验证范围。
历史发布验证不覆盖新任务。修改工具同步相应参考；修改 Skill 后运行
`python3 scripts/pack-skill.py --check`，包结构检查不代表现场通过。
