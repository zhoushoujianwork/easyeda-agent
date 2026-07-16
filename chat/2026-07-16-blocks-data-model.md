# 2026-07-16 Blocks 数据模型改造决策

## 背景

当前电路块库用 `internal/blocks/data/*.json` 保存器件选择、内部拓扑、边界端口及 PCB 约束。
它已经适合 Agent 查询和人工复用，但还不足以充当确定性的 block 实例化 IR：loader 以核心字段投影加
`json.RawMessage` 保留扩展字段，`_schema.json` 主要是说明文档，测试只覆盖部分元数据和约束 map。

本次盘点了仓库内 25 个 block，并运行 `go test ./internal/blocks/`。现有测试通过，但发现“测试通过”
并不等于数据满足文档所宣称的“validated 后可直接照抄”。

## 主要发现

### 1. `validated` 的二值语义过载

`validated` 只要是非空字符串，`Ready()` 就返回 true；它实际上同时承载原理图拓扑、器件选型、PCB
DRC、实板 bring-up 和投产可用性。CH340C block 已标为 validated，但自身 note 明确指出 USB ESD
器件选型错误、投产前必须替换，说明“拓扑验证通过”不能推出“整个 block production-ready”。

### 2. `_schema.json` 不是可执行 schema

它使用示例字符串描述字段，没有 `required`、`type`、`enum`、引用闭合等机器约束。Go loader 又使用
`map[string]any` 和 Raw JSON，因此拼错字段、未知枚举、缺少核心字段可能直到具体消费者运行时才暴露。

### 3. 拓扑引用没有闭合校验

目前没有完整验证：

- `internal_nets` 的 role / port 是否存在；
- 同一 pin 是否误入多个网络；
- `ports.at` 是否为有效 role/pin；
- `signals.nets`、`pcb_layout.target` 是否引用已声明对象；
- pin 名是否属于所选标准器件的符号。

此外，边界端口存在 `PORT:<name>` 成员和 `ports.<name>.at` 两种表达。部分直连端口只写 `at`，没有
出现在 `internal_nets`，未来 block apply 必须猜测两者的优先级。

### 4. 可执行约束和人类说明混在自由文本中

`pcb_layout.value`、`target`、`placement.orientation`、`signals.route` 等字段很适合人和 LLM 阅读，
但无法让工具稳定执行。可量化的距离、方向、层、网络和严重级别应结构化，自由文本只保留为 `reason`。

### 5. PCB 阶段丢失 block 实例身份

现有 placement 消费器只能按位号前缀反推 role；单字母前缀被排除，冲突前缀会被丢弃。这不是可靠的
实例关联。正确方向是在 block 实例化时持久化 `block_id + block_instance_id + role` provenance。

### 6. `must` 约束可能静默失效

部分 loader 遇到 malformed map 会跳过单项或整个块。对于 `must` 约束，这种 best-effort 行为会形成
“命令成功但关键约束未执行”的假象。

## 决策

采用向后兼容改造，不一次性破坏现有 block。第一阶段的可信加载、结构化 verification 和核心拓扑
校验可以保留；后续大规模迁移和 schema 扩张须先通过下面的价值验证。

## 价值复盘（后续决策，优先于原三阶段路线）

进一步盘点发现，当前 block JSON 中约一半内容是 `source`、notes、reason、route、BOM 说明等
手册性文本，真正的 `internal_nets + ports` 只占较小部分。当前也没有完整的 `block apply`：主要消费
方式仍是 Agent 搜索、阅读 JSON、再手工 place/wire，只有少量 placement/opening/keepout 被工具消费。

因此 blocks 目前证明的是“结构化知识库有用”，尚未证明“复杂可执行 IR 有用”。结构化本身不是价值；
只有自动实例化、自动检查或稳定实例追踪实际消费某字段时，维护该字段才有回报。

### 当前正式定位

blocks 暂时定位为：

> 轻量可执行拓扑 manifest（parts/internal_nets/ports）+ Agent 可读的设计手册与验证提示。

不是完整电路 IR，也不承诺所有约束都能被工具执行。JSON 中存在自由文本并不意味着该文本已机械化。

### 暂停事项

在最小 `block-apply` 闭环证明价值前，暂停：

- 全量迁移 25 个 block 到复杂 verification；
- 为所有内部网络增加稳定 net ID；
- 建设完整 provenance 数据库；
- 把更多 PCB/手册知识继续拆字段、扩 schema；
- 因“未来可能消费”而提前结构化字段。

### 下一步价值实验

选择 `led_indicator_gpio`、`tactile_boot_reset` 或 `ams1117_ldo_3v3` 中一个简单块，实现最小
`easyeda sch block-apply` 垂直闭环：

1. 加载 block；
2. 分配器件位号并按 parts 放置；
3. 按 internal_nets 连线；
4. 将 ports 绑定到宿主网络；
5. 运行 schematic check；
6. 输出可追踪的 block instance manifest。

先不处理复杂 PCB constraints。用连续实例化成功率、操作数量下降、是否仍需解析自由文本、第二/第三个
block 的执行器复用程度决定是否继续投资。

### 继续投资的门槛

- 连续多次实例化不产生错误网络；
- 相比 Agent 手工 place/wire 明显减少动作和返工；
- 核心执行不依赖 LLM 阅读 notes 后猜行为；
- 第二、第三个 block 可复用同一执行器而非增加专用脚本；
- 实例能记录 block ID/revision/role，修改后可识别过期实例。

若实验失败，blocks 应退回“简洁 manifest + Markdown 手册 + 可复制示例工程”，不再追求完整 IR。

## 已完成的有限改造

以下工作解决了真实问题且保持兼容，因此保留：

- 强类型 part/port/verification 核心投影；
- role/port/internal_nets 引用和 pin 多网冲突校验；
- `ready` / `verified` / `draft` 状态区分；
- CH340C 选型错误不再被标成 production-ready；
- `_block.schema.json` 作为稳定核心字段的最小 schema。

以下原计划仅作为“价值实验成功后的候选路线”，不再视为已批准 roadmap。

### 已完成第一阶段：可信加载与核心校验

1. 引入结构化 `verification`，保留 legacy `validated` 读取兼容。
2. `Ready()` 改为由明确 verification gate 计算；旧数据在迁移完成前走兼容路径。
3. 为核心字段建立强类型结构：part、port、verification；Raw 只用于未知扩展维度。
4. 增加库级 `Validate`：必填字段、枚举、role/port 引用、pin 多网冲突。

尚未实现“未知 `must` 约束拒绝执行”；它应随具体自动执行器一起实现,而不是在尚无消费者时继续扩展
通用 schema。

### 候选第二阶段：迁移现有数据

1. 将现有 `validated` 证据迁入 `verification.schematic` / `verification.pcb_drc`。
2. 没有实板证据的一律设置 `bringup=not_tested`。
3. 有选型待修或 datasheet 待核的 block 设置 `component_selection=failed|pending`，不得
   `production_ready=true`。
4. 修正 category 枚举漂移，增加 `schema_version` 和 block `revision`。

### 候选第三阶段：可执行 IR

1. 为内部网络增加稳定 net ID，ports、signals、PCB rules 统一引用 net ID。
2. 把 adjacency、方向、线宽、阻抗、层和 keepout 等可执行值结构化。
3. block apply 生成持久化 provenance，PCB 消费器停止依赖位号前缀猜测。

## 兼容原则

- `blocks ls/show/search` 在数据迁移期间保持可用。
- 不删除原始验证证据；legacy `validated` 只在完成迁移后废弃。
- 未知扩展字段继续可展示，但不能被误报为已执行。
- “schematic verified” 与 “production ready” 必须在 CLI 输出中明确区分。

## 候选长期验收条件（仅在价值实验成功后启用）

- 每个 block 都通过统一的严格 validator。
- CI 能捕获 role/port 引用错误、pin 多网冲突、非法枚举和 malformed `must` 约束。
- CH340C 这类“拓扑已验证但选型有问题”的 block 不再显示为 production-ready。
- 自动化消费者能够报告哪些约束已消费、哪些未支持，而不是静默跳过。
- 最终 block 实例具有稳定 provenance，可从 PCB 器件反查 block instance 和 role。

---

## 追加(同日):价值实验结果 + 反馈闭环定案

### block-apply 垂直闭环实验 —— 门槛四过一待

按上文「下一步价值实验」在 `ceshi` 真机跑通了最小 `easyeda sch block-apply`
(加载 → 放置 → 连线 → 绑端口 → check → 实例 manifest;规划器纯函数 + 7 单测):

| 门槛判据 | 结果 |
|---|---|
| 连续实例化不产生错误网络 | ✅ LED 块 3 次实例化,netlist 逐网核实;GND 正确并入宿主网,内部网互不合并 |
| 第二/三块复用同一执行器 | ✅ ams1117_ldo_3v3 零代码改动直接规划成功(4 角色/power-gnd 分档/位号避让) |
| 核心执行不依赖读 notes | ✅ 只消费 parts/internal_nets/ports;未消费约束在 manifest 里显式列 NOT applied |
| 实例记录 block ID/revision | ✅ manifest 含 blockId+revision+role→designator 映射 |
| 比手工明显省动作 | ⏳ 直觉成立(1 条命令 vs 十几次调用),未量化 |

实验过程本身抓到并修复:**同名内部网静默合并 bug**(默认 instance 原从 block id 派生,
两次实例化会把两个实例的内部网合并短路;改为从首个已分配位号派生,LED1_N2/LED3_N2 天然隔离,
带回归测试)——这正是上文「PCB 阶段丢失 block 实例身份」担忧在原理图侧的具体形态。

顺带解决:role-id → deviceUuid 桥(placement.go 记载的缺口)通过复用 bom-enrich 的
六级探测(抽成 `internal/app/skill_asset.go` 共享)在 CLI 侧打通;是否嵌入二进制留待后议。

**结论:可执行 IR 的价值实验通过,块库从「给 AI 看的说明书」升级为「可机械执行的库」。**

### 反馈闭环:否决在线服务,走 GitHub Issue

实验通过后曾提议把库服务化(在线查询 + 验证结果自动回传 + 缺失自动登记,做成自有资产)。
**否决**,理由:

1. 自动回传验证结果 = **收集用户数据**,合规与信任成本不可接受;
2. 养服务是可用性/鉴权/版本兼容的长期运维承诺;
3. 私有数据面与「一块一 PR、署名永久」的社区共建模型冲突。

**定案:反馈面全部走 GitHub Issue —— AI 起草、用户确认后才提交,零被动数据收集。**

- 三类模板(`.github/ISSUE_TEMPLATE/`):`block-gap`(缺失登记,聚成需求地图)/
  `block-bug`(缺陷上报,必须带 manifest/netlist/DRC 证据)/
  `block-contribution`(好设计投稿,不会提 PR 也能投,维护者代落库,author 署名归投稿人)。
- Skill 三纪律(SKILL.md 铁律 8 + contributing §七):必查、报缺陷、登记缺口;
  **上报是外发动作,一律经用户确认,绝不自动**。
- 生命周期由 issue 驱动:draft → ready → **正常参考(默认态:无人反馈=没出问题,不是数据缺失)**
  → block-bug → 修订 bump updated + 上报人进 contributors。
- 维护侧:分诊三 label,机械修挂 `ready-for-agent` 交自动化(既有流程),
  真机 DRC 验收类留人工实时会话(operator 边界既有约定)。
- 器件数据漂移(deviceUuid/C号/basic,已实测存在)不归此闭环:parts-select/bom-enrich
  已走实时查询,发现漂移按 block-bug 报,不做后台刷新。

放弃的:反馈率天然低于遥测(刻意接受——「沉默=正常」是明文定义的健康默认态)。
观察后定:若起草 issue 的路径被真实使用,再加 `easyeda blocks report` 辅助命令
(从 manifest 自动拼 issue 草稿)——判据同 block-apply:看真实使用,不看想象。
