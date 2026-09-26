# 原理图数据与 SCH Apply（1.4）

适用于从本地 JSON 绘图、整理已有原理图、修正位号和验证转换结果。
CLI 参数以 `easyeda sch <command> --help` 为准；电路选型依据具体器件的数据手册，
通过 `lib device get` 的属性可取得 Datasheet 地址。转换器不会推断缺失的外围电路。

## 数据驱动架构基准

这是原理图设计、布局、检查和修复共同遵守的现行基准（2026-09-14 固定，延续 1.4
连接模型），不是新增一种可选工作流。字段定义见下文，执行步骤见
[auto-layout-sop.md](auto-layout-sop.md)。旧九宫格、三层 tidy/move 和逐件现场试摆不作为
新设计、整页重建或布局验收的主流程。本文随 Skill 发布，仓库架构页和历史文档引用本节，
不各自维护另一套规范。

### 权威来源与单向生成

```text
官方原始快照（保留，不覆盖） + 需求/手册证据
  → 目标源数据副本（连接事实 + 核心/外围归属 + 绘制策略 + 实测几何/约束）
  → layout-plan --zones（区内完整求解）
  → layout-sheet-plan（完整区候选选择与平移）
  → 数据校验 + layout-render（只转译同一份目标）
  → compose --layout-page（固定转换） → 受保护 Apply
  → 官方 API 原始数据回读 → 目标/实况对账 + 逐页严格门禁 → 显式保存
```

任一阶段发现问题，保留 finding 和失败输入，回到源数据、采集器或算法修复，再从受影响
阶段重算并重新验证下游结果。不能直接改 SVG、队列坐标或现场图面来掩盖失败。
已有完整 Lib 可走 `lib-layout → compose` 适配入口，但也遵守同一数据闭环。

- **原始观测不改写**：保存来源、工程/页、采集时间和原响应；新增测量另存，不能修改旧快照
  使其“匹配”目标。属性原文保留，规划时按下文规则筛选，不删除原始证据。
- **目标数据是设计权威**：canonical 连接核心决定 component/pin/net/NC，显式 zone 成员与
  attachments/netPolicies 决定功能归属和画法；只有网表或旧 S0 spec 不足以证明布局意图完整。
- **几何是可重算结果**：坐标、导线、标记、框和标题由测量与约束生成；Apply 不做第二次设计。
  现场数据是执行结果的权威证据，不能反向覆盖目标而掩盖偏差。

### 核心与外围不变量

每个器件唯一归属一个明确功能核心的 zone；共享器件显式决定归属，不按共同 GND/VCC、
画面距离或虚拟组名称猜测。必要时以 attachments/attachTo 指定已同网的参考引脚。
去耦、上下拉、滤波、反馈等专属外围随核心求解和迁移；串联支路保留依赖关系。
执行层必须用数据强制这些约束，不依赖 Agent 阅读提醒或主动运行检查。核心/外围之间未
引入第三器件的连接不得改成同名标签；第三器件须有完整连接数据证明，不凭本页网名推断。
显式专属外围仍须经真实导线及必要的串联外围链接到核心，同框、共同电源/地标签不建立这种关系。
写线前校验引脚外向与本体穿越，写后回读；完整组合的末态同时核验外围真实线树。
原理图导线的无节点内部 X 交叉与实体接点不同，不能将一切几何交叉都判短路。
已测 EasyEDA Pro 3.2.186 中，严格内部 X 不导通，T 端点接触会合网并切段；同网 X
仍是不同物理线岛，不能满足外围直连。API 连续 polyline 的冗余共线 waypoint 会被删除，
不得用它伪装实体接点；官方原始独立段记录不按连续 polyline 解读。dev.8 统一规划、
线岛、渲染和执行回读的接触语义；缺少对应版本的现场回读时不能称现场已覆盖。异网端点/T/重叠及穿引脚、
器件、位号、标记仍须拒绝；线的几何覆盖相同也须另核对物理接触分区。
违规或必需数据缺失必须非零失败，不能以 API 成功或省略 `--strict` 放行。
部分宿主的 `wire.getState_Net()` 即使导线已实际联网仍返回空字符串。严格内部 X 的现场检查
只能在两侧物理线岛分别由逐 pin 官方网表和明确 marker 得到唯一、非空且一致的网名时，
补足该岛的网络证据；歧义、缺 witness 或冲突仍报 ERROR，且 X 本身绝不能用于合并线岛。
位号/导线碰撞同样按官方 `flat-segments` 的每个四坐标独立段计算，不得把上一段尾点与
下一段起点虚构成对角线；连续 polyline 仅在原始编码明确表示连续顶点时使用。
导线与网络标记本体/文字的碰撞须使用包含可见 stroke 的真实占位；异网导线沿 marker
边界共线、端点或 T 接仍是碰撞，不能靠全局收缩 marker bbox 放行。只有该 marker 自身
引线在它自己的 anchor 精确终止时可按对象身份局部豁免，不按同网、同 zone 或 ownership
猜测。规划器、最终几何门和现场 clusters 必须复用这一接触语义。
规划候选也必须检查引脚首段沿外向方向出线，不能只等落图时由执行守卫拒绝。
官方世界坐标 `pin.rotation` 应保留并随刚体旋转同步变换；器件旋转不得再次重复应用。
兼容旧量测时的 bbox 方向推断须唯一且显式，不能用缺失方向冒充官方测量。
部分执行失败不表示自动撤销，保留实际结果并重新读数修复；检查覆盖以实际安装版本为准。
`direct` 必须是真实线树，原直连引脚组不得退化为同名标签岛。“同网”“同框”都不等于
外围完整跟随。拆区必须显式改源成员和边界策略，迁移完整功能子电路，不单独剥离专属外围。

区内只修改本区，纸张层只能整体选择并平移完整合法候选，不能混搭器件/导线、拆散同页集合。
碰撞、所有权、直连与纸张约束须分别验证；无重叠不证明功能完整，分数高不替代硬约束。
有界搜索失败/预算耗尽不证明无解，也不允许输出半成品冒充成功；优化无改进可保留合法基线。

### 页面碰撞与入框的统一范围

参与项：器件本体、完整引脚、真实导线、网络标记本体与网络文字、器件位号（Designator）、
自由文字、模块框和标题，以及纸张/图签边界。按对象关系判断合法连接与遮挡，不将所有接触
一概认作碰撞。器件本体与位号须完整落在所属框内，位号不得被遮挡。
实测 Designator bbox 是闭合障碍：导线进入内部、沿边界重合或只触及角点都必须报错；
不得沿用器件本体允许合法端点落边的开区间判据。

器件本体的 `tight-spacing` 使用源数据已经显式登记的功能 ownership：同一 zone、模块认领或
持久功能组内的核心/专属外围允许小于通用间距，但真实面积重叠仍是错误；跨 ownership 的
过近仍由 strict 门禁阻断。归属缺失时不按同网、画面距离或相似位号猜测豁免。
导线成员的碰撞按官方 `flat-segments` 逐条真实线段判断；L 形或多段线的整体包络只用于
功能组占地与页面边界，包络空角不得产生图元相交。真实线段或 marker 与其他器件相交仍报错。

排除项：型号、参数、描述、供应商信息等**非位号器件属性文字**。它们不进入页面碰撞、
入框、净距和扩框包络；重叠/越框不报警。不靠隐藏属性、缩短真实型号或器件标准化来过布局门。
这只排除属性的呈现占位，不豁免器件身份、参数正确性或官方电气 DRC 的独立检查。
自由文字和网络名称不是器件属性，不随此规则排除。

测量须保留对象类型、所属器件/zone、可见性和来源。旧 `textBboxes` 仅含矩形、不含属性类型，
生产者须从有类型的原始属性筛出位号后填入，并保留对应来源；不能混入型号后让消费者猜测。
缺少位号或其他必检几何不能当作空集合/零碰撞；估计可用于规划，不能冒充现场完整验证。

布局前对目标当前页运行 `easyeda sch designator-geometry --project <project> --doc <page> --out designators.json`。
此只读命令逐个读取 part 的 `Designator` 属性，要求每件恰有一个可见、值与位号一致且
拥有有效官方 bbox 的属性；输出属性 ID、parent ID、值、bbox 与测量来源。把对应 bbox
按 parent ID 写入源测量的 `measurement.textBboxes`，同时保留原始导出文件；型号、参数等
其他属性不得混入。命令对缺失、重复、隐藏、身份不符或 bbox 缺测非零失败，不用预测文字
尺寸冒充实测。它只覆盖当前页，不替代导线、框或电气回读。

### 数据发现、修复与验收证据

问题的主判据必须来自原始数据与可重复检查。官方导图仅辅助审阅：如果看图发现漏检，先把
问题还原为对象 ID、所属关系、bbox/线段或连接差异，补采集/规则及正反回归用例，再重算。
截图不能作为缺测项的替代证据，也不能把“人工看过”登记为机器检查通过。

每轮保留源输入、测量来源、算法参数/版本（含源码提交及未提交差异）、输入/输出哈希、
完整候选与选中页、队列/journal、实际回读、检查明细和 `saved:true`。失败至少能追到
规则、对象及原始证据；采集失败与设计违规分开报告。源数据或实况变化后，旧下游结论失效。
小范围存量移动仍可使用已有带线工具，但先记录源目标与变更，再回读同步和验证；
未能从保留输入重现的现场修补只能标为未闭环，不能交付为算法验收通过。
dev.8 对旧 `group-move`/`disconnect` 增加写前安全拒绝：不能可靠处理的多段导线、
相关严格内部 X、未知线形或缺少必要接点几何返回 `PRECONDITION_REFUSED`，不写入。
简单单段路径仍保留；此限制不是复杂网络移动已获完整支持。遇到拒绝须重新采集原始数据，
走数据驱动规划及 `compose --preserve-instances`，不能把原始段数组强当连续折线来绕过。

通用算法的回归以明确约束和多组可重复正反例为准：不透明 ID/位号/网名重命名、源坐标
平移、完整旋转与镜像、NC 保持、直连被标签替代的负例，以及有界失败/依赖回退。
具体工程仅是反例来源；不得加入按其型号、位号或坐标生效的补丁来换取页面通过。
离线算法验收与宿主安装/现场验收分别记录，不能相互替代。

以上是验收要求，**不是所有已安装命令均已覆盖这些检查的声明**。以当前版本实际输出和
覆盖范围为准：外围语义归属尚须显式输入，proximity 评分不等于所有权事实，单一聚合检查
不证明源数据溯源完整；未覆盖的必检项必须列为未验证并补足机器验证，不能用文档更新冒充实现。
只验原理图时止于原理图保存，不扩展为 PCB，也不称整板或发布验收通过。

## 数据分层

| 对象 | 权威数据与约束 |
|---|---|
| `component` | `id` 为稳定、不透明实例 ID；`ref` 为显示位号；可选 `role` 保存功能名。`device.libraryUuid/deviceUuid` 是器件库身份，不能用 16 位放置实例 UUID 替代 32 位库 UUID。 |
| `pin` | `number` 是完整物理引脚编号，`name` 是符号脚名；明确 NC 用 `noConnected:true`；已确认悬空用 `connectionState:"unconnected"`。保留器件所有物理引脚。 |
| `net` | 稳定 `id`、名称 `name`、可选 `scope/role`。电源与地通常为 `global/power|ground`，信号通常为 `local/signal`；作用域是规划提示，不替代连接记录。 |
| `connections` | 每条为 `{componentId,pinNumber,netId,kind}`；导出使用 `kind:"netlist"`，它不表示导线几何。通过稳定 ID 引用对象。 |
| `modules` | Lib 的 `id/name/coreComponents/peripheralComponents/internalNets/ports`。核心和外围列表引用组件 ID，不引用 primitiveId 或从 ID 截取位号。 |
| 几何 | 器件位置、朝向、实测 bbox/引脚位置、导线、标记、框与文字。允许重算，但不能暗改连接核心。 |

Connectivity JSON 顶层为 `schemaVersion:"1.4"`、`projectId/documentId`、
`components/nets/connections`，可附 `modules/issues`。一个参与绘图的引脚应恰好对应
一个网络、明确 NC 或显式 `connectionState:"unconnected"`，三者互斥。
悬空状态仅在官方快照明确返回 `net:""` 与 `noConnected:false` 时自动导出，
用于原样重建未完成的图，仍保留 `unconnected-pin` 警告，不代表设计通过。
未知、缺失证据需要修复，不能自动填 NC 或悬空。多个同功能脚也要逐脚连接。

实例通过 `otherProperty["EasyEDA Agent Component ID"]` 保留 canonical ID，
`otherProperty["EasyEDA Agent Role"]` 保留可选功能角色。重新导出优先读取绑定；
未绑定旧图兼容用 `cmp-<ref>` 初始化一次。改名之后保留旧 ID，不重新造 ID，
不覆盖原生 `uniqueId`（它可能关联 PCB）。

旧连接器可能把 16 位放置实例的 `device/footprint.uuid` 与 32 位库资产 UUID 直接比较，
导致同名封装全部报 `package-variant mismatch`。`sch list --include-device-identity` 与
Apply 写前/写后回读共用 CLI 兼容解析：用官方 `getDocumentFootprintSources()` 的
FOOTPRINT `DOCHEAD` 和唯一 `META.source` 证明实例封装到库资产的出处。部分版本在
原理图页返回空数组，此时从官方 `getProjectFile(..., 'epro2')` 的当次工程导出中只读提取
同样的出处；前后工程/页面必须一致，源码必须唯一包含当前页面，ZIP 和解压大小受限。
不读取历史备份替代当前状态，也不在编辑器内保存临时工程。随后请求全部
精确 LCSC 候选，并用 `lib_Device.get` 核对型号/稳定名称及关联封装 UUID、库来源。
唯一匹配才恢复器件库身份；输出 `deviceResolution.via=lcsc-footprint-source` 和原连接器错误。
这证明库资产出处，仍不代替引脚、XY、连线及最终图面回读；不把相同封装名作为重建依据。
两个 32 位资产 UUID 冲突、缺原生出处、来源不符、多候选或查询/证据缺失均拒绝，
不能手改快照清除错误。旧连接器的兼容查询只运行固定官方读取脚本；dry-run 不派发
该 debug 查询，也不降低身份门禁。

## 选择转换入口

| 需求 | CLI 与边界 |
|---|---|
| 读取连接图 | `sch connectivity [--page <page> | --all-pages]`；跨页导出逐页激活读取，避免只取得浅层引脚信息。 |
| 本地连接差异 | `sch connectivity-diff before.json after.json`；检查组件/网络增删、连接及 NC 差异，不代替器件库身份与几何校验。 |
| 本地设计版本差异 | `sch design-diff expected.json actual.json --exit-code`；比较 canonical 或完整 compose 计划，输出稳定 ID 差异、修订哈希和证据覆盖范围。 |
| 既有框/标题的差异 Apply | `sch design-diff before-plan.json after-plan.json --before fresh.json --playbook frame-diff-apply.json`；第一份是已落地基线，第二份是期望目标，只编译变化的既有 owned frames。 |
| 从实测引脚计算 Lib 内部 | `sch lib-layout --from layout-input.json --out composition.json`；核心与外围的连接图、实测姿态及网络绘制策略 → 局部器件位置/短线/标记，再交给 compose。 |
| 非标准位号修复 | `sch designators allocate` 分配，`plan` 编译原地修改队列，`verify` 执行前后校验。 |
| 完整 Lib 图面 | `sch compose`：完整连接核心与局部几何 → 单页布局与受保护 Apply。 |
| 已确认的 Zone 单页 | `sch compose --layout-page page.json`：保持选中页的完整几何、框/标题、spacing 与 sheetPosition，仅转换坐标并生成相同守卫。 |
| 基础放置 | `sch materialize`：已知库身份和 placement → 仅放件队列。它不是完整模块绘图器；`--with-connectivity` 已停用。 |
| 明确的标记增量 | `sch plan before.json after.json`：仅新增 `power/ground/net_port_in/net_port_out/net_port_bi` 连接；对应脚原为 `unconnected` 时，目标移除此声明；原为 NC 时，目标须同时清 NC 并新增明确标记连接。其他器件/引脚/NC 变更、删网或重接均拒绝。 |
| 只画框和标题 | `sch frame apply/check --from frames.json`；字段见 `sch frame --help` 与 [actions.md](actions.md)。 |
| 执行队列 | `sch apply plan.json`，顺序等待 WebSocket 响应并记录 journal。 |

`sch materialize --with-connectivity` 已停止生成队列：旧路径逐脚接线未规划碰撞，可能把相邻
引脚短接，也不能替代模块内部短导线。完整重建走 `lib-layout → compose → apply`，保留
接线前实测几何守卫。materialize 创建时使用零旋转、无镜像，再通过 modify 写入实测
存储旋转和镜像，避免把创建接口的旋转语义误当成存储语义。

`sch plan` 的 NC→连接转换逐脚执行：初始完整连接守卫 → `no_connect off` →
明确空网/非 NC 的中间守卫 → autoconnect → 目标守卫 → 保存及最终守卫。
这不删除器件引脚，不清理其他 NC；单独清 NC、同一脚 NC 与连接并存均拒绝。
队列必须完整执行，失败后重新回读并生成，不能 `--resume/--from/--to` 跳步。
新队列显式指定引线常规搜索 10～80 raw、步进 5 raw，扩展错长的硬上限 300 raw
（`sch autoconnect --offset-cap`）；保持 5 raw 网格并严格拒绝碰撞，找不到合法位置时停止。

### 本地版本与 EDA 回读对账

先按稳定组件 ID/引脚号匹配同一工程与页，再分别检查拓扑、ref/库身份、placement/bbox/pins。
`connectivity-diff` 返回 `{}` 不涵盖后两类；使用 `design-diff` 补查字段。完整 compose
计划还可比较导线、标记、框与标题；只提供 canonical 数据时这些图形必须列为未验证，
不能把导出中未包含的字段当作“相同”。新命令属于后续源码，原发布版 1.4.2 不含此能力。
`status` 为 `synced` / `different` / `wrong-target` / `incomplete`。
单页顶层 `documentId` 可确定省略的器件 `pageId`；跨页缺归属或显式冲突不能据此补齐。
对账与修订哈希统一按小数点后 9 位规范化数值，只去除 API 浮点尾差，不按 5 raw 网格吸附；
真实亚网格位移仍会报告。计划与 canonical 回读比较时，未导出的模块/顺序及仅由 `netlist`
证明连接、未证明绘制方式的 `kind` 列入 `coverage.unverified`，结果为 `incomplete`，不当作模块删除或全量同步。
两份完整计划仍比较模块、顺序和连接绘制方式。
`expectedRevision/actualRevision` 是 `coverage.scope` 内规范化内容的哈希；运行态 primitiveId、
库存数组顺序和整条导线的正反遍历不计差异，模块阅读顺序及实际折线路径会比较。
退出码：0 表示比较执行完；有 `--exit-code` 且内容不同为 2；目标不符或 canonical 证据不完整为 3；
非法输入/读文件失败为 1。新建连接图尚未含 placement/bbox/pin XY 时，即使两份图相同也会返回
`incomplete`（3）；这不等于网表错误，电气不变量可用 `connectivity-diff` 比较，几何须经计算或回读补齐。始终检查 `coverage.unverified`，不能只以退出码 0 声称现场完整同步。

```bash
easyeda sch design-diff target-plan.json observed-connectivity.json --exit-code
easyeda sch design-diff previous-plan.json next-plan.json --exit-code
```

仅修改已由本工具登记的模块框/标题时，可编译有界增量队列：

```bash
easyeda sch list --project <project> --page <page> --stay \
  --include-device-identity --include-bbox --include-pins --include-wires > fresh.json
easyeda sch design-diff before-plan.json after-plan.json \
  --before fresh.json --playbook frame-diff-apply.json > frame-diff-report.json
easyeda sch apply frame-diff-apply.json --dry-run
easyeda sch apply frame-diff-apply.json --yes
```

两份输入必须是完整 compose 计划，`fresh.json` 必须与第一份基线的器件、库身份、
引脚、NC、导线及标记一致。队列再次核对实际电气图和基线框的所有权/外观，
只更新有变化的框，再检查目标框和未变的电气图，最后保存。相同计划只生成只读检查，
不调用 frame apply 或 save。框仍使用虚线；标题修改必须保留有效的内容占位和净距。
本入口不新增、删除、重排框，不修改电气/器件/导线/标记，也不接受规划诊断或电路
占位数据的变更；这些差异明确拒绝编译，不能作为通用 patch 使用。未知所有权或现场
已偏离基线时停止；部分执行后须回读并重新确定基线，不能跳过前置检查重试旧队列。

模块局部坐标与整页坐标不同，现场应对照 `compose` 输出的目标坐标，不能直接比较源局部坐标。

保留本地目标和新导出两份文件，记录来源及采集时间。文件较新或连接相同不代表已经同步；
按用户已确认的修改意图确定合并方向，只读比较任务不覆盖任一方。坐标与 bbox 等证据互相
矛盾时先补读；缺少的字段列为未验证，不填默认值制造一致。拓扑、身份、几何、官方导图和
保存状态分别报告，离线比较不能确认导出之后的现场状态。

## 位号修复

正常位号使用英文字母前缀加数字。保留已有合法编号的大小写、前导零和声明顺序；
`U_RF`、`J_AUDIO_MOD` 属于功能名，放入 role，不当成原始合法编号继续重放。
官方库默认前缀可能是 `U?`、`CN?` 等，不能假定端子必定是 `J`。

V4 若启用了自定义位号格式，在 canonical Document 顶层显式声明：

```json
{"designatorPolicy":{"mode":"custom","pattern":"^CTRL-[A-Z]+-[0-9]{3}$"}}
```

pattern 必须是锚定的 RE2 表达式。此模式只验证和原样保留现有位号；`designators allocate`
不会猜 V4 编辑器中的递增规则。缺失/非法 pattern 或任一 ref 不匹配时在生成写队列前拒绝。
省略该字段仍执行下面的 classic 字母前缀+数字分配流程。

1. 用 `lib device get --uuid <deviceUuid> --library <libraryUuid>` 查询官方记录，保存响应。
2. 将 `result.device.property.designator` 汇成 `prefixes.json`，格式为
   `{"<libraryUuid>/<deviceUuid>":"CN?"}`。不能将库的 `Designator:"CN?"` 占位属性写回已有实例。
3. 在全工程连接图上分配编号，再逐页编译修改计划：

```bash
easyeda sch designators allocate project.json --prefixes prefixes.json \
  --out numbered.json --changes changes.json

easyeda sch list --project <project> --page <page> --stay \
  --include-pins --include-bbox --include-wires --include-device-identity > before.json

easyeda sch designators plan numbered.json --before before.json --out rename.json
easyeda sch apply rename.json --dry-run
easyeda sch apply rename.json --yes
```

分配只改非标准项，按输入顺序跳过全工程已占用数字。plan 固定源文件 SHA，核对库身份和
原始引脚/网络，只对原有实例写 ref 与 ID/role 绑定；不清页、不摆件、不画线。
全工程守卫逐页加载；页面枚举、读取或原页恢复失败时停止，不能把不完整清单当成全量证据。
前后检查保护 primitiveId、uniqueId、属性、引脚/网络/NC、位置和导线，成功后同步本页
组成员及 role 引用并保存。再次生成计划时，已匹配的绑定不再写入。

源 composition 的 `placements[].designator`、`terminals[].designator`、组和规格书中的
ref 引用也要按组件 ID 同步；不要对 JSON 做全局字符串替换，网络名和稳定 ID 不随之改名。
`compose/materialize` 在生成放置队列前拒绝非标准 ref，防止错误源数据写回画布。

## 由引脚计算 Lib 内部

### Zone 归属复核提醒

`easyeda sch zone-review --from zones.json --report review.json` 是离线、只读的规则检查，
输入与 `layout-plan --zones` 相同，省略 `--report` 时 JSON 输出到 stdout。报告绑定源 SHA-256。
`layout-plan --zones` 在求解前自动向 stderr 提醒，并在可选报告的 `zoneReview` 中保存相同
规则结果；求解失败仍保留复核报告。输出布局格式和输入/几何正确性校验不变。

- `multiple-multipin-members`：同区存在至少两个具有 >=4 个物理引脚的器件。只是多核心的
  弱线索，连接器和保护阵列可能合理同区；不把引脚数量等同芯片类别或强制拆区。
- `non-rail-subgraph-detached`：去除 `local_power/local_ground` 网络后，存在不含核心、
  但有至少两个成员通过其余网络关联的子图。提示复核完整子电路；孤立去耦不按本规则拆出。
- `rail-only-attachment`：显式 attachment 的两端器件仅共享 `local_power/local_ground`。
  去耦可以合法命中，需核对功能依据；共同电源/地本身不证明专属归属。

报告含稳定组件 ID、显示位号、涉及网络策略、逐脚证据和建议。算法只使用显式网络策略，
`direct/module_port` 仍可能是电源支路，不能把它们自动认作真实信号；报告明确此限制。
这些是源网表关系，不是现场物理线岛证明。`status:review-required` 仅提醒，退出 0；
`status:no-findings` 只表示规则未命中。输入不合法/硬归属错误返回 `invalid` 和非零退出。
AI 可以直接修改源数据副本，逐条解释保留或调整的理由；成员迁移须一起处理 attachment、
marker 的归属和跨区策略，保持 pin→net/NC 与原本必需的真实直连，随后重新求解及回读。
本检查不自动修改 JSON、生成执行队列、认可 AI 猜测或替代布局/电气验收。

完整效果必须由整份 `sch layout-plan --zones` 成功结果生成，再交纸张规划和固定渲染器。
逐区捕获错误的诊断输出不能拼装为完整候选；保留诊断供修算法，但不以原测量占位替代失败区域。
发布本地效果时记录输入/输出哈希、源码提交、命令与覆盖检查；这不是 Git tag 或安装包发布。

两层统一间距模式在 zones 输入顶层提供 `spacing:P`，P 为 >=10 的 5 raw 网格数。
`layout-plan --zones` 生成具有该内边距的完整功能框，并将 spacing 带入输出；
随后附加 sheet 给 `layout-sheet-plan`，省略 sheet.padding/gap 时二者由 P 派生，
显式提供则必须等于 P，不能悄悄覆盖。相邻框只加一次 P，笔画半宽与网格取整另计，
实际净距可略大于 P。spacing 不改变电气间距或引脚 pitch；已确认页通过 compose 的
显式 `--layout-page` 保留 P，省略该参数的旧 compose 仍使用原 10 raw 契约。
统一模式下 maxCandidates 是**每个 zone** 的独立搜索预算，candidatesUsed 为各区之和，
防止改变一区的计算耗费影响其他区结果；显式 optimization 也启用每区独立预算。
两者均省略的旧输入仍沿用整份共享预算。

局部规划失败时执行有限 checkpoint 回退：撤回本区已放外围位置及受影响后缀线路，
尝试另一个真实 XY 再重新计算；核心与实测姿态不变，不通过修改网络或 NC 取得通过。
每次进入 checkpoint 最多尝试 3 个不同 XY；同一 checkpoint 可因上层候选改变而再次进入，
每次回退已经消耗一个真实候选，因此分支上限与本次共享候选额度一致
（小于 128 的合成预算仍保留 128 的次级递归守卫），不再用成员数推导的 1024 次硬截断提前丢弃
已授权的候选预算。先用分支额度前半段定向调整
真实阻挡器件，失败再扩展到物理端点所有者，两阶段不重置预算。
显式 `attachTo` 子件在坐标窗口或当前预算分片内未找到合法位置时，父 checkpoint
下一次可按子件实测本体跨度改试较远网格距离层；仍受同一 3 个 XY 和共享预算约束。
预算分片耗尽时尤其不能把近距离层判为无解；跨距只是搜索启发式。
阻挡归属未知时禁用剪枝。每轮使用共享预算分片（初始预算的 1/8，夹至 512..16384，
且不超过剩余预算），为回退保留搜索机会；片额不足也可能促使回退，不代表几何无解。
候选预算末尾另保留 5%（夹至 512..10000），仅供最近完整终局的阻挡器件/依赖外围
刚体迁移；沿相对核心的主轴优先从 5/10 raw 开始，按 5 raw 有界扩展到 40 raw，
每次撤回本轮全部线树后重算。该阶段仍消费原候选预算和节点预算，不产生新额度。
完整放置后的 `direct` 路由若给出归属完整的可移动阻挡器件，先对该阻挡组做一次最近
5 raw 刚体迁移并重算整区；失败再走普通 checkpoint 回退，后续保留额度仍可继续扩距。
搜索返回 search.strategy/backtracks/repairAttempts/movedComponents/branchLimit/conflictPasses
诊断和总候选计数；失败/预算耗尽不返回完整 layout。
这不是无限搜索或全局最优保证，也不能替代现场文字与引脚回读。

区内接线可在输入顶层显式提供
`routing:{maxExpandedNodes,maxReroutes}`。两项均可省略；默认每个 zone 共用
`maxExpandedNodes:200000` 个方向网格节点展开、最多 `maxReroutes:4` 轮撤线重布。
单区入口同样按一个 zone 计。节点搜索固定使用 5 raw 网格，先尝试直线和简单正交折线，
失败后在当前完整内容包络外依次扩展 40、80、160、320 raw 做方向感知搜索；所有边界、
全部 direct 网络、允许姿态及 checkpoint 尝试共享同一节点预算，不能因换边界或重试而重置。
单个岛对最多使用总节点预算的 1/4；默认再保留最后 1/4 给经真实阻挡证据触发的
器件/attachment 刚体迁移（小于 1024 的测试预算不预留），避免早期失败吃光修复阶段。
搜索状态包含进入方向，按短线、少折弯、少合法 X 交叉的顺序确定性选择。引脚第一段必须
沿官方外向方向；目标是指定的另一物理线岛，可接该岛引脚或已有线树的合法网格点。
加入候选后必须证明原指定两岛已合并，不能只以全局岛数下降或同名标签放行。
放置候选还会用同一边验证器检查未完成 direct 线岛是否能到达另一线岛或 40 raw 局部前沿；
同器件同侧重复 direct 引脚保留 10 raw fanout corridor，普通 direct 引脚保持 5 raw 转弯自由。
拆区后，同一器件、同一侧、同一信号 `module_port` 的重复引脚也必须先在区内组成一棵真实
线树；求解器只在隔离的计算副本中将该网提升为 `direct` 完成 fanout，不修改源 zone 的
`module_port` 跨区契约。这样跨区仍由端口表达，但不能退化为每个密集引脚各放一个同名标签。
电源/地不适用这项信号规则，单个 module_port 也不因此改变策略。
只有新器件实际进入拒绝证据或属于该岛时，`direct-island-enclosed` 才能归因并剪枝。

严格内部 X 仍不导通。跨越异网线只能作为不中断的直线段穿过，不能在交叉点生成 waypoint
或拆成两个独立 wire action；异网端点、T、共线重叠仍拒绝。路由边的拒绝证据直接记录
阻挡对象稳定 ID/位号、网络和共享几何规则原因；归属不完整时禁止据此跳过候选或 checkpoint。
后续 direct 网络被本轮较早生成的线树阻挡时，从本轮接线前的不可变基线撤回这些生成线，
调整网络顺序后整轮重算；达到 maxReroutes 后才把完整冲突交给器件及依赖外围的 checkpoint
回退。显式 attachments 与 allowedRotations 的授权含义不变，路由器不能自动改归属或放宽姿态。

`--report` 对成功和失败均记录路由耗时、展开节点、边界尝试、重布次数、指定失败线岛，
以及逐类拒绝边/阻挡证据。失败分类至少区分 `data-missing`、
`expanded-node-budget-exhausted`、`no-path-within-bounds` 和 `final-validation-failed`。
诊断可保存完整局部布局、未连接线岛和候选路径摘要用于离线重放，但其 status 必须是
`diagnostic`，不能进入 layout 输出、compose 或 Apply。任一必需网络失败时命令非零退出，
不创建/覆盖 `--out`；已有同名旧输出也不代表本轮成功。

dev.8 的放置失败从几何检查处携带结构化归属，记录失败器件稳定 ID、参考宿主、阻挡
所有者、各类失败次数、候选消耗及耗尽类型；不解析最后一条人类错误字符串来决定回退。
已封闭的宿主候选集失败直接回传；仅在阻挡归属完整且没有待放新宿主时，跳过无关的
后缀 checkpoint，回退相关宿主/阻挡器件。未知归属、预算未搜完或未来可能增加同网
宿主时保守保留探索机会。原候选预算和分支上限不重置，也不因新回退增加。

成功布局也可继续优化：普通/多区 `layout-plan` 输入显式提供
`optimization:{maxVariants:4,maxAttempts:24}`，外围 `components[].allowedRotations`
列出许可的**绝对 stored rotation**（0/90/180/270，含源角度，不允许重复）。
默认锁定实测姿态；核心锚点/姿态、镜像、器件身份、pin→net/NC 不变。不根据位号猜授权。
器件经归属复核改为核心时可以保留源数据已有的 `allowedRotations`；该列表仍须合法且包含
实测角度，求解器只把核心的有效许可收窄为实测角度，不要求为改归属而篡改组件证据。
先保留完整合法 baseline，再尝试许可的刚体旋转和 5/10/20 raw 内移；新姿态先在现位置
重建接线，失败可在该区有限重排。所有候选必须重新检查器件/文字/引脚/线/标记碰撞与
命名连通；**基线同一物理导线岛内的引脚不允许退化为多个同名标签岛**。
线长相对基线最多增加 20%，不改变线宽、符号尺寸或 padding 取得小面积。
旋转后的实测文本 bbox 随刚体变换，无文本测量时仍使用保守估算；Apply 前须重新实测
旋转后的引脚、bbox 与文字，离线通过不证明官方符号/自动文字排布完全一致。

若原姿态 baseline 求解失败，应在同一有界预算内先尝试显式允许的外围朝向，寻找首个
完整合法基线，再进入可选瘦身优化；不能因优化只在成功后运行而忽略已允许的姿态。
原始测量保持不变，记录可行性搜索的朝向提案、预算与失败原因。仍不得改网、移核心、
省略直连或跳过碰撞检查。dev.5 在原姿态外最多尝试 16 份确定性候选（先协调组合、
再单件及双件），每次记录配额、消耗和失败原因，不重置总预算。原姿态是唯一有新鲜页面测量证据的姿态：
总额度 <=40000 时保留原 20000 窗口（总额度更小则使用全部）；更大预算使用 3/4，且最多 150000，
其余仍留给显式允许的旋转姿态。未用配额立即归还共享总预算，不产生新额度。
预算不足时不保证全部朝向会被尝试；失败不是任意姿态下无解的证明。现场能力须经
完整安装版与受保护 Apply 验证，离线成功不等于已修复页面。

maxVariants 范围 1..4（默认 4，1 表示仅基线、不搜索），maxAttempts 范围 1..64（默认 24）；
每提案及路线探测共享本区剩余 maxCandidates，失败不污染已验证结果。分别保留基线与
面积/宽/高代表方案（重复形态去重，所以可能少于 4 个），主 layout 选较小内容面积。
每提案额度按基线实际求解成本的 2 倍分配（至少 512、不得超剩余额度），而非所有区套用
固定小额度。方向阶段最多用半数提案，并硬保留优化开始时剩余额度的 1/4 给后续内移；
复杂区可能只探索少数角度，不能把提案限额内失败解释为该朝向几何无解。
单区输出 layout-level variants；多区输出 `zones[].variants:[{id,layout,contentBounds,frame}]`，
variants[0] 恒为完整原基线，主几何须精确匹配某项，`selectedVariantId` 标识匹配项。
候选内不嵌套候选。`optimizationReport` 报告提案数、接纳数、剩余预算和停止原因；
主 candidatesUsed 是整次本区成本，候选上的 cost/score 仅是诊断，不是合法性证明。
这是小候选启发式，不是笛卡尔积穷举或最优性承诺；无改进保留基线也算正常结果。

合页预览使用 `sch layout-sheet-plan --from render.json --out pages.json`。
输入沿用 render zones，增加 `sheet:{bounds,border,keepouts,padding,gap,flow?}`，坐标单位 raw。
bounds 是原纸张，border 是内框，不能为放下内容而篡改；padding 指虚线笔画到内框的净距，
gap 是功能框之间的净距。用户未给具体值时可先提出更宽的预览值，不改变默认 compose 契约。
无 variants 时规划器保持区域内部全部几何与连接不变，只计算各区
`sheetPosition:{x,y}`（框左上角，y-UP）；有 variants 时只能整体选取已验证候选再平移。
普通合页输入的 `sheet.flow` 可选 `z`（规划器默认）或 `compact`；CLI
`--flow z|compact` 显式覆盖输入选择。`fixed` 仅是 `sch layout-edit` 输出的已选页面契约：
它要求每个区已有唯一形态和位置，只复核边界/碰撞，不重新装箱，也不能通过 `--flow` 手工选择。
Z 型从左上按功能顺序从左到右，同行顶齐，按该行最高框加 gap 换行，框高度不拉齐。
不回填前行短框下的空白，也不回填已结束的页。图签避让仍须满足固定净距与 5 raw 网格；
功能顺序不会因框面积而重排。同页集合按最早成员聚拢，组内维持输入顺序，整体试放或换页。
相邻软偏好不能破坏 Z 型顺序；结果可能比自由装箱多页，不宣称页数最优。
Z 型整次多页计算最多 200000 次矩形候选检查；图签阻挡时横向推进至障碍右边缘，
无器件的受阻行才按 5 raw 下移。有界失败不返回部分页面，也不证明换行策略之外无解。
显式 compact 保留旧算法：数种确定顺序尝试无旋转装箱，选择页数较少的结果；不是最优性证明。
Z 型候选选择保留最多 16 个中间分支，每组在当前页或新页整体试放，仍不回填或重排内部。
先比较完整页数，再比较末页占高/行高、总框面积、真实线长和关联距离；分支可被有界剪枝。
另外独立计算全主方案和全原基线作为安全退路，不因新搜索耗尽丢失它们的合法完整结果。
两个安全基线与候选搜索分别最多 200000 次矩形检查；`variantSearch.candidatesUsed` 只计
候选阶段，另报两条基线可行性/页数、选中页数与 budgetLimited，不能当成三阶段总成本。
输出页只携带选中的 layout/frame/contentBounds、selectedVariantId 和 sheetPosition，清除
variants；渲染器不再优化或混搭。当前 `flow:compact` 不接受候选包，需明确先选定形态。
输出 `pages[]` 各项交 `sch layout-render`，绘制纸张、内框、padding 线和图签禁放区。
此模式渲染器严格使用给定位置、不重排；拒绝越界、相互重叠或侵入 keepout 的框。
输入各区均有合法 sheetPosition 且当前完整框仍满足所选流及纸张约束时，原位复用，返回
placementMode:reused；否则只重排矩形，返回 repacked，所有区内 layout 保持不变。
Z 型复用要求旧坐标与本次确定计算的位置完全一致，不只是无碰撞或近似 Z 型；
显式标注 flow:z 的页面渲染会复核此契约，拒绝错误位置但不修改它。旧 render 输入未声明
flow 时只保持原有几何校验；规划器则默认生成显式 flow:z，不能混称两种验证覆盖。
这是一页既有位置的复用，不是跨页迁移事务。已有 frame 不满足新的统一内边距时明确拒绝，
须先重新生成该区的框；整页层不暗中改大框或缩放内容。
红色 blocked 区仍未完成，不能把没有导线的占位区域当成电路验收或最终容量证明。
合页预览不迁移 EDA 页面，不合并网、不生成 Apply；确认后用 `compose --layout-page`
转换选中的单页，仍需完整连接/身份/纸张守卫。

固定离线渲染入口：`sch layout-render --from render.json --out layout.svg`，可选 `--zone <id>`。
输入 `schemaVersion:1,zones:[{id,title,status,layout}]`；layout 是局部布局输出，status 为
`planned` 或 `blocked`。也可直接读取 `layout-plan --zones` 的结果（缺省 status 为 planned）。
blocked 区只能显式提供原测量几何，不生成假导线。只转译数据，不重新求解、不调用模型或 EDA，
不输出差异图解；SVG 中区域整体平移仅用于展示，不代表纸张/电气验收。简化符号和字形不等同官方导图。
`layout-render` 和 `layout-sheet-plan` 默认校验完整 ID/引脚状态、几何和命名连通；
blocked 或只有器件没有必需连线的占位数据会失败且不覆盖已有输出。
需要诊断时显式加 `--diagnostic`，仍不允许缺失几何坐标、非法线段或伪造已完成状态。
`--zone` 先验证完整输入的排版关联引用与所有候选（包括未选中候选），再仅输出所选区细节，去掉纸张和同页约束；
它不验证整页容量，完整交付仍须渲染规划器返回的所有 pages。

标记引线按长度从短到长搜索，同长度才采用电源/地方向偏好；同一线树的各个引脚之间也比较
最短可行引线。两个二端器件同网面对面、共轴且已有直线连接时，优先尝试网格上的精确中点
垂直分支，用于对称取线；不能落网格或存在碰撞时保留原命名搜索，不移动连接点伪造对称。
对已真实合并的 `direct/module_port` 线岛，若专用中点规则不适用，还会从该岛真实线段的
5 raw 中点、既有接触节点或端点尝试垂直接出命名引线：水平段只先试上/下，垂直段只先试
左/右。该分支仍经过同网正长度重走、异网端点/T/重叠、标记包络与完整连接门禁；它只给
已连接线树命名，绝不替代 direct 的物理合并。

通用 layout-plan 的网络端口错长预算以端口本体加文字的轴向占位计算：
`min(300, ceilGrid(10 + 2 × 最大端口占位))`；最大占位取当前端口及已放置端口。
这是有界搜索的初始工程约束，不是官方电气规则；不能为满足长短比而把短线故意拉长，
也不能把超限候选截短后冒充合法结果。无解应调整姿态/布局；当前仍不是联合全局优化。
命名引线不得与已有同网导线正长度重走，共享端点和垂直 T 接允许。
同一线树只命名一次；同网外围直接连接可保留一个必要的跨区端口，不因有标签而拆网。
渲染端口须采用与避碰相同的本体和文字占位，显示真实 T 接点；不能用小圆点代替整支端口
后据图判断可压缩程度。框包络使用标记的实际方向，不向无符号的一侧镜像预留。
完整线树求解后增加一次有界端口瘦身：固定器件、电源/地和对称中点，只在原线树的已知引脚
尝试换命名点/方向；总线长至多增加 20%，每次须使内容包络面积至少减少 5%，且全部几何及
命名连通检查通过。最多 2048 次且计入剩余 maxCandidates；这一可选优化耗尽预算时保留已
验证的候选，不影响此前必需布局无解/耗尽时的失败规则。该启发式不保证 A4 排版全局最优。

逐芯片独立功能区使用 `sch layout-plan --zones --from zones.json --out zones-geometry.json`。
输入 `schemaVersion:1/components/netPolicies/zones`，可选 `attachments/maxCandidates/optimization/spacing/routing`；
每个 zone 为 `{id,title,coreComponentId,componentIds}`。components 与单区入口相同。
每个独立芯片/接口明确声明为一个核心；所有器件恰好归属一区，公共电源/地不用于猜归属。
共享外围先明确归属；跨区信号必须声明 `module_port`，不能用 `direct` 跨区后偷偷改标签。
输出每区的局部 `layout` 与包含器件、线路、标记及文字占位的 `contentBounds`；
contentBounds 不含标题；`frame` 使用既有标题/净距规则计算独立粉色虚线框预案。
标题宽度目前是保守估算，非官方实测。每区分别交 compose 模块整页排版；
补齐身份和纸张证据前不可 Apply。任一区失败整份不输出半成品。
这是显式归属的通用计算入口，尚不自动识别芯片功能或推断共享器件的归属。

可选 `zones[].placement:{samePageAs:"host-zone",preferAdjacent:true}` 会原样传递至局部结果、
sheet planner 和各页 render 输入。samePageAs 必须引用另一现有 zone；同页关系可传递，
不是父子所有权，不能重复组件。preferAdjacent 默认为 false，为同页目标的相邻软偏好。
当前没有单独的跨页 near 语义；缺目标、自引用、未知字段或不合法类型必须拒绝。
同页集合整体找页，不得为减少页数拆散；区内布局不变。Z 型按稳定阅读流放置同页集合，
不为 preferAdjacent 在已结束的行回填。下面的自由搜索预算仅用于显式 compact 流：
有关系时按同页连通集合做有界矩形搜索，候选取纸边、障碍边及对齐事件点，不是全网格完备搜索。
每组/每候选页最多 200000 次矩形检查、4096 个状态、64 个完整候选；按方向/对齐分桶轮询，
保留最多 8 个完整组方案，整页保留最多 8 个候选分支，并尝试 4 种组顺序。
整页先比较页数，再比较软关联边缘距离、组包络面积和中心距；每分支仍 first-fit 选页，
并非全局最优。失败排序不会丢弃其他排序已有的完整结果。合法旧页可原位复用，
不会仅为软相邻偏好强制移动既有区框。
未找到合法同页排版时返回搜索/容量诊断，不生成半页效果或偷偷取消约束。
完整子电路拆分必须显式修改成员归属和跨区 module_port 策略，但不能改 pin→net/NC。
例如晶振连同负载电容形成一组，主控去耦保留原区；该规则不按位号或型号硬编码。
验证需同时报告主控框、拆出框、总框面积、线长和页数；原理图分框不指导 PCB 拉远器件。

外围在首个可行距离层优先比较连接端点到宿主出线轴的偏差，再比较线长等紧凑指标。
直连线树合并先尝试直线/L 形和小范围折线快速路径；失败后进入上述共享预算的方向感知
5 raw 网格路由，并在需要时撤回本轮生成线树改变网络顺序。仍逐条检查异网引脚、器件、
位号和已有线路；按上述实体接触语义区分内部 X 与真实短接。有界预算不提供全局最优或
无解证明，但失败必须带明确终止类型且不能输出半成品。

普通器件集合不需要建 Lib：使用 `sch layout-plan --from input.json --out geometry.json`。
dev.8 的通用失败诊断入口为 `sch layout-plan ... --report report.json`（也适用于
`--zones`）：报告记录源 SHA-256、运行算法版本、decode/solve/emit 阶段、成功或失败，
以及可取得的结构化冲突对象、阻挡归属、路由耗时、展开节点、重布次数和搜索预算。
报告路径必须与输入/目标输出不同。
失败仍非零退出且不输出半成品布局；机器应消费结构化报告回改约束/算法，而不是从最后
一个报错位号猜根因。有限预算、坐标窗口与回退分支耗尽不是全局无解证明。
嵌套或多重包装的失败保留结构化诊断；放置冲突按稳定 ID、直连路由冲突按网名和
`blockerRefs` 定位，允许姿态搜索的各次失败也保留诊断，不只保留最后一次错误。
报告读取有界，异常错误树截断会明确列为缺测。
报告写入失败也非零退出，保留原失败原因；不能将旧几何文件的存在当成本轮成功。
输入 `schemaVersion:1/coreComponentId/components/netPolicies`，components 每项含稳定 `id`、
`measurement`（下表 placements 格式），无网络的物理脚还须在 `pinStates` 中按脚号声明
`nc` 或 `unconnected`。netPolicies 按**网络名称**索引；可选 `attachments` 沿用
`componentId/pinNumber/attachTo` 格式，可选 `maxCandidates`。输出是核心归零的局部几何、
`score`（线长/线段数/末件轴线误差/面积）和 `candidatesUsed`，不是可直接 Apply 的队列。
这条入口不校验库身份和纸张；交给 compose/执行适配器前仍须补齐这些证据。
Lib 入口仍使用稳定 net ID 映射策略，由适配层转换后调用同一 `PlanSchematicLayout` 内核。

`sch lib-layout --from layout-input.json --out composition.json` 全程离线，输出直接供
`sch compose` 使用，不生成或派发 EDA 操作。输入：

- `schemaVersion:1`、完整 `connectivity`、实测 `sheet`、明确的 `keepouts` 数组。
  可附 `sheetBorder` 指定实测图纸内边框；它不改变原纸张 bbox。
- `measurements[]` 使用下表 `placements` 的实测格式。必须显式含 x/y、rotation/mirror、
  bbox 四边、完整 pins（含 number/net/x/y）；NC 的 net 为空，朝向固定，不能将未知值填 0。
- `layoutModules[]` 含 `id/title/coreComponentId/netPolicies`，覆盖所有 canonical Lib。
  `coreComponentId` 必须是该模块的核心成员之一；其余核心与外围沿已知电气连接展开。
  `netPolicies` 以**稳定 net ID**为键：`direct` 必须连成真实线树，`module_port` 优先直连，
  无法安全合并时允许分别命名的跨区线树，
  `local_power`/`local_ground` 为每个独立线树就近放电源/地，已直连的外围不再各放一个标记。

可选 `layoutModules[].peripherals[]` 为 `{componentId,pinNumber?,attachTo:{componentId,pinNumber}}`。
前一个 pinNumber 选择外围脚，attachTo 选择同模块核心或外围的几何参考脚；两脚必须已经同网。
例如两个 VOUT 都存在时可指定右侧那一脚摆电容，另一个 VOUT 仍按网络策略保留连接。
没有提示时按已连接网络选择参考，优先内部信号，再考虑电源；纯地关系不推断功能搭档。
串联支路按依赖顺序放置；环形提示、断开的模块或无法避碰的测量姿态返回具体未解决对象。

核心归零后沿参考脚方向搜索，外围可正对或垂直于参考脚，全部 bbox/pin 随器件平移。
候选保持 5 raw 网格，参考脚连接距离从 5 raw 递增，检查直线和正交折点；当前外向搜索至 400 raw、横向至 200 raw。
可选 `maxCandidates` 限制整份输入的搜索次数（默认 20000，范围 1..1000000）；耗尽时明确报错，不写出半成品。
默认是有界、保持实测姿态的求解器，失败不证明电路在任意朝向下都无解。
普通/多区 layout-plan 的显式方向优化见上文；lib-layout 适配器仍保持原姿态契约。
不能放宽碰撞检查或修改网表来取得通过。已有手工设计好的 Lib 仍可直接 compose。

先安排仅连电源/地的外围，再安排信号相关外围；同一类别与连脚复杂度下，宿主就位后优先完成其显式
`attachTo` 子支路；同级子支路先试最近放好的宿主，尽早暴露需要回退宿主位置的冲突，
避免无关核心外围占去子支路出线空间。
候选接入 `direct` 网时先检查实际并岛及后续命名所需的合法网格出口；物理可短接但封死
出口的最短候选须改试更远位置，不等整区终局才发现。所有候选仍经过原几何与连通门禁。
`direct` 路由候选还要保留已完整汇合线岛的真实标记出口；A* 找到的首条几何合法路径若被
命名前沿证明封口，继续在原节点额度内寻找其他可达目标状态。当前 A* 对每个
`(x,y,direction)` 只保留一条最优路径，不能枚举到同一状态的更长替代路径；回调拒绝后若
未找到路径或用尽节点额度，只能报告本次搜索未定，不能缓存为几何无路。
只有一个区内物理脚的 `module_port` 必须能从该脚放出真实标记；新器件候选若在**撤去临时
导线后的放置几何**中封死原本可用的标记出口，放置期即拒绝该候选。此必要检查复用终局
命名器的同一套标记候选与碰撞门。已找到的标记引线须在每个新器件上重放完整文字、引脚和
导线间距检查；只按固定半径或普通本体几何复核会漏掉远端封口。探测预算耗尽只能报告缺测，
不能宣称出口不存在。
首个可行距离层内比较局部实线、轴线对齐误差、可见包络面积，不直接采用第一个可行位置。
下游专属外围若被已选位置挡住，同一 checkpoint 可在已拒绝 XY 集合下重试该距离层
的其他合法位置，再扩到下一层；已拒绝坐标不重复计候选，真实新候选仍消耗共享预算。
每个 checkpoint 至多三次选择，耗尽只报告有界失败，不输出部分布局。
部分放置候选只检查当前本体、引脚与线路，完整放置后才统一计算命名和最终连接/几何门禁；
最终评分包含标记与逃逸引线总长。命名失败同样触发本区回退，而不是把缺失标记当成成功。
同模块电源/地优先尝试 80 raw 内的安全直线/L 形合并，
不可短接时才保留独立命名点；不改变电气网名或跨模块引用。
密集引脚若没有合法直出标记，尝试有界正交逃逸：先沿实测引脚外向出线，再横移，最后接真实标记引线。
所有新增线段仍检查本体、异网引脚、已有导线和标记占位，不在渲染端假接或放宽短路判据。
`module_port` 优先直连；无合法直连的分离线树可分别接同名跨区端口，因该网已声明跨区引用。
`direct` 仍必须合为一棵线树，不得自动改为标签连接。
当前命名引线首先按 GND → 电源 → 信号搜索，失败再尝试两种纵向空间顺序、横向顺序与
稳定网名顺序；五种顺序都失败且仍有共享预算时，再对每个线岛最多八个经完整几何门
验证的引线候选做有界联合回溯，使后续线岛受阻时可重试前一线岛的合法引线。
它不能扩大 `maxCandidates`；快速路径已耗尽预算时不会额外搜索，也不发布部分标记。
终局联合命名前逐岛独立复用同一命名出口检查（单脚跨区端口与已接通的多脚线树均适用）；完整探测
失败可报告具体线岛，探测额度不足只能返回预算停止。已归因的命名岛冲突可将相关器件与显式子件整体从近到远
平移，每档撤销旧线并完整重布、重命名，所有候选与路由节点仍计入原有共享额度。
同一线树比较
包含逃逸线的总线长，而非只比较最后一段标记 offset。符合条件的二端器件分支优先对称中点。
命名与逃逸候选消耗同一 maxCandidates 预算，失败的顺序不污染下一次候选。
每岛八候选与总预算仍可能错过更深的解；相同连接图与姿态可能因数组顺序产生不同结果。
保留失败输入和最终计算参数，
不把一次排序成功当作通用布局规则。若从官方实测姿态推导 90 度刚体变换，须同步变换锚点、
完整引脚、bbox 四角和 stored rotation，并记录原始测量与变换；Apply 的写线前引脚回读必须通过。
模块单独求解成功后仍须通过整页 compose 的边距与图签检查。

```bash
easyeda sch lib-layout --from layout-input.json --out composition.json
easyeda sch compose --from composition.json --out plan.json
# 完成现场取证后，按下文加入 --before/--playbook，最后 Apply。
```

## Lib 组合输入

`compose` 输入顶层使用 `schemaVersion:1`，内含上述 `connectivity`（版本仍是字符串 `"1.4"`）、
实测 `sheet`、`keepouts`、本页图签文本和按阅读顺序排列的几何 `modules`。

| 字段 | 格式 |
|---|---|
| `sheet` | `{minX,minY,maxX,maxY}`，目标纸张实际 bbox。坐标单位 raw = 0.01 inch，y 向上。 |
| `sheetBorder` | 可选同格式 bbox，实际图纸内边框；模块虚线笔画在其内最少留 10 raw，考虑半线宽后向内取 5 raw 网格。缺少时输出 `sheet-bbox-fallback`，不能据此声称已验证红框净距。 |
| `keepouts` | bbox 数组，例如图签；从 `sch sheet-geometry --json` 取得，保留其来源与警告。需要精确内框/图签时先导出本页官方 SVG，再用 `sch sheet-geometry --from-svg <全页.svg> --json`（[来源与限制](schematic-layout-conventions.md#11-图纸边界与标题栏-keep-out-sheet--title-block-keep-out)）。空数组表示已确认没有禁放区。 |
| `titleBlock` | 可选的**本页**图签字段映射。旧字符串值（如 `{"Drawed":"设计者"}`）仅更新文本；对象值支持非空字符串 `value` 和可选布尔 `showTitle` / `showValue`。例如 `{"Name":{"value":"电源与接口","showTitle":false,"showValue":false},"Drawed":{"value":"设计者","showTitle":false,"showValue":true}}`。字段名先从目标页 `sch titleblock-get` 回读；源、计划和受保护队列保留同一显隐声明，strict gate 前通过 `sch titleblock --data` 写入并回读。省略显隐表示保持原状态，不能以 `null` 或字符串布尔代替；拒绝空更新、未知对象项、`@` 派生项及图纸结构项。字段属性显隐不等于图签整体显示；保留表格并用官方整页导出验证正文和附加属性。没有此字段的旧源不修改图签。 |
| `modules[]` | `id/title/placements/wires/flags`，可附 `terminals/titleMetrics`；与 connectivity 的 Lib 成员逐项对应，每件只归属一个模块。 |
| `placements[]` | `designator/value/x/y/rotation/mirror/bbox/pins`；bbox 与引脚位置来自官方实测，器件和引脚坐标落在 5 raw 网格。 |
| `placements[].textBboxes` | 可选外置位号 bbox 数组，格式同 sheet；也用于 measurements。仅填入按原始属性类型筛出的 Designator，非位号属性不进入此数组。与当前实测姿态一致，随平移进入碰撞和框包络。普通 list 不自动提供，须另存类型、parent 与测量来源；不控制实际文字位置，Apply 后须回读验证。缺失不代表位号为空或已验证。 |
| `pins[]` | 完整 `{number,name,net,x,y}`；官方量测提供时保留 `rotation`（世界坐标外向角：0 右、90 上、180 左、270 下），刚体旋转同步更新，不重复应用父器件旋转。NC 的 `net` 为空，与连接核心的 NC 状态一致。旧输入缺角度时仅允许唯一 bbox 侧推断，不得伪造为官方角度；候选不能删除或篡改已提供的角度。 |
| `flags[].anchor` | 必须可归因为 `{type:"pin",componentId,pinNumber}` 或 `{type:"wire_tree",zoneId?,net,x,y}`。pin 锚定的 `pinX/pinY/direction` 必须等于目标引脚坐标与官方外向方向；wire_tree 接入点必须落在指定网的唯一真实线岛上。旧结果只在几何能唯一归因时迁移，歧义非零失败。 |

### 核心相对坐标与局部编辑

区内结果以核心锚点为局部原点；外围、引脚、位号、内部导线、标记和框只应用一次区级
平移：`page = corePage + local`。attachment 决定布局依赖，不叠加为第二次坐标变换。
`sch layout-edit --source zones.json --page selected-page.json --snapshot fresh.json` 提供两个
互斥操作：`--move-core <stable-id> --to x,y`，或 `--repair-pin <stable-id>:<pin>`。
前者输出新的 selected page；若刚体平移碰撞，固定核心目标，从源数据重算本区候选并选择
首个完整合法形态，其他区保持不变。后者输出目标数据和 `--playbook`，只沿引脚外向轴搜索
合法标签错长。命令不写 EDA；执行使用 `easyeda sch apply`，写后回读、显式保存和官方导图。

局部 repair playbook 使用 `schematic.pin.repair_marker`。它在同一个页面互斥区间内验证旧线、
旧标记、目标引脚与完整基线，删除旧支路并创建新支路，再比较前后 finding 和范围外对象。
范围外既有错误允许保留但必须逐条相同；任何新增/改变、目标错误残留、陈旧快照、部分删除、
超时或不完整回读均失败。普通 `wire.create/connect_pin` 继续要求整页几何基线为零。
| `wires[]` | `{net,points:[[x,y],...]}`，正交、非零、落网格。JSON 网名本身不能给实际线树命名，须连接对应引脚/标记。 |
| `flags[]` | `{net,kind,pinX,pinY,direction,offset}`；kind 为 `power/ground/net_port_in/net_port_out/net_port_bi`，方向 `up/down/left/right`，offset 为正的网格长度。转换生成真实引线。 |
| `terminals[]` | 可选 `{designator,pin,direction,kind?}`，默认 `net_port_bi`；引用本模块已测引脚，选择向外直出方向，不能与已有接线重复。 |
| `titleMetrics` | 可选 `{title,fontSize,width,height}`，须与标题、字高相符的实测值；没有时采用保守预测，Apply 仍检查实际文字边界。 |

局部外围沿核心引脚方向设计，电容/电阻用短线直接连接。电源/地就近重复放置可减少环绕线；
跨模块信号使用网络端口。端子直出按 10～300 raw、5 raw 步进寻找最短合法直线，
邻标签通过错落长短避让。无法直出就修源几何，不自动回退折线。

组合器从模块上下空档选择标题位置，再从左上以 Z 字排列。每个框按自己的内容保持紧凑高度，
同行顶齐，下一行按上一行最高框推进；`rowHeights` 为各行推进高度，`rowHeight` 仅是最大值诊断。
页边、框内最小边距、模块间距及标题内缩固定 10 raw，标题净距 5 raw；
框为粉色 `#AA00AA` 虚线、无填充，标题为 20 raw。本版本无独立 Notes。
它平移器件、引脚和线路，但不推断器件朝向或缩放符号。

### 已确认页的固定转换

`sch compose --from composition.json --layout-page page.json --out plan.json`
消费 `layout-sheet-plan` 返回的单个 pages[] 对象，或 `sch layout-edit` 返回的固定页。
必须显式声明统一 spacing、`flow:z` 或受保护的 `flow:fixed`、
完整纸张/内框/禁放区，包含已选 layout/contentBounds/frame/sheetPosition，不能含 variants、
blocked 或 diagnostic 数据。composition 的有序模块身份、标题、核心/成员、全部局部
placements/wires/flags 必须与该页一致，不能有尚未求解的 terminals。
source 与 page 的纸张、内框和禁放区须精确一致；实际 sheet bbox 另由 --before 守卫核实。
`titleBlock` 绑定 `connectivity.documentId` 指向的单页，在分页选定后加入该页 composition 源；
区内几何源和纸张排布页不代填图签。生成后不手改 Apply 队列补图签。
内框、图签或文字若来自估计仍须保留来源，重复相同估计不等于官方测量。

先复核原始必填坐标、canonical 引脚网络/NC、完整局部几何和 Z 型纸张约束，
再按 `dx = sheetPosition.x - frame.rect.minX`、
`dy = sheetPosition.y - frame.rect.maxY` 作唯一刚体平移（y-UP）。
器件、引脚、文本 bbox、导线、标记、框、标题及标题障碍同步转换，不再选方向、缩框或重排。
编译输出的坐标使用统一小数点后 9 位精度消除 API 浮点尾差，保留原始测量快照；
这不是吸附到 5 raw 网格或放宽现场差异守卫，真实位移仍须报告。
加入 `--before fresh.json --replace --playbook apply.json` 后仍使用原完整 Apply 生成器；
拒绝输出覆盖输入或两个输出指向同一文件。此入口不创建页面，也不处理跨页迁移事务。
若现场电路或纸张与预览不符，先重新核对/计算并展示改变后的效果，不手改队列或伪造回读。

## 从计算到现场

### 已有器件的实例保全门禁（dev.8，现场验收未完成）

仅重排已有电路时，必须保留原器件实例，而不是删件后恢复一个相同位号。
原生 `uniqueId` 可能关联 PCB；`primitiveId`、真实参数、供应商及自定义属性也不能因
布局被替换。非位号属性不参与页面碰撞，不代表这些属性可以丢弃或恢复为库默认值。
dev.5 的整页 `compose --replace` 重建没有此保全契约，不可用于声称无损重排。

dev.8 开发入口为 `compose --replace --preserve-instances`：只接受同页、相同完整器件
集合及可核验的稳定 ID/位号/库身份/引脚连接与 NC；源不一致或字段缺测时写前拒绝。
生成器须保留实例，只清除已授权重建范围内的线路、标记和页面图形，再按同一份算法
目标移动原器件、接线与画框。纸张和器件所属属性不清除，不手改队列绕过保护。
前检、清理后的检查点、移动后的检查点及末态都须核对实例和属性；布局可变字段与
明确的 Agent ID/role 绑定单独列明，不能用忽略全部属性取得通过。
全工程位号唯一性前检只读取 `allPages+tagPages` 的最小器件清单，核对已有位号、实例句柄
和待创建位号的不存在性；不得在该跨页步骤重复请求器件库身份、bbox 或引脚。目标页随后
单独读取完整身份、几何、引脚和导线基线。两个守卫职责不同，不能因精简跨页读取而删掉
目标页的完整实例保全检查。
此段是实现及验收契约；开发包安装不等于现场已通过，以实际 Apply、保存重开和回读为准。

```bash
# 离线检查输入并计算布局
easyeda sch compose --from composition.json --out composition-plan.json

# 读取将要操作的已有页
easyeda sch list --project <project> --page <page> --stay \
  --include-pins --include-bbox --include-wires --include-device-identity > before.json

# 已授权重建不同图面时加 --replace；已匹配图面不需要该参数
easyeda sch compose --from composition.json --out composition-plan.json \
  --before before.json --replace --playbook apply.json
easyeda sch apply apply.json --dry-run
easyeda sch apply apply.json --yes
```

生成队列已固定工程与页 UUID；执行时沿用其目标，不用名称覆盖 `--project/--doc`。
目标已有图面与计划不同且未加 `--replace` 时拒绝生成队列。
完全匹配时保留电路，仅执行验证、组登记、模块框与保存；几何匹配但未接线时，
只有明确的空导线/空标记/无冲突 NC 证据才能复用器件接线。其他重建路径清目标页、
保留纸张，再枚举全部图元确认无残余。

新建器件先零旋转 place，再绝对 modify 写入实测朝向、位置及 canonical 绑定。
当前平台 create 与存储旋转存在符号差异，不应直接将测量角度传给 create。
回读全部引脚几何通过后才恢复 NC、导线和标记；最终核对 pin→net/NC、线树、模块框，
通过 `sch gate --strict` 后显式保存。`buses/shortSymbols` 计数缺失或非零会阻止图面匹配。

所有页使用各自的完整器件子集、不同 `documentId`，跨页位号不重复；组合器不自动创建、
合并或删除页面。默认先做一页，放不下时根据功能及用户已有授权拆页。
跨页迁入前还需安排源页处置：源页若仍有相同 ref，全工程位号守卫会拒绝目标重建。
先保存完整源数据并列出器件的目标页及源页处置步骤，再在已授权范围内执行迁移；
当前没有跨页迁移事务，不能只执行目标 compose，或为消除冲突盲清其余页面。

## 验证与恢复

- `sch apply --dry-run` 只校验队列，不证明现场状态与电路正确。
- `requireFullExecution:true` 或含连接守卫的队列须完整执行，不能续跑/跳步或更换目标。
  前检失败、写入超时或部分成功后，保留日志、读取实际状态，再生成新队列。
- Apply 不提供事务回滚。`checkpoint` 只是日志标记，只有 save 动作才保存。
- `sch connectivity-diff` 以稳定 ID 对账；位号修复还须核对 ref 映射，重新布局还须核对
  库 UUID、完整引脚、真实导线和框。DRC 单项结果不替代这些检查。
- 官方器件 bbox 不完整包含外置位号；另读 Designator 的真实 bbox 核验遮挡与入框，
  将必要净距写回源数据。非位号属性排除；官方导图只辅助发现采集/规则遗漏，不代替数据检查。
  短位号可能缩小文字预留并改变重新 compose 的紧凑排版；
  单独修位号不应附带重排。
- 报告未解决的 WARN、未知数据与未运行的验证。失败队列不会自动执行末尾 save；
  如需保留已核实的局部成果，先回读确认后另行保存，不把局部完成称为整板通过。
