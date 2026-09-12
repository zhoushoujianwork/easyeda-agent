# 原理图数据与 SCH Apply（1.4）

适用于从本地 JSON 绘图、整理已有原理图、修正位号和验证转换结果。
CLI 参数以 `easyeda sch <command> --help` 为准；电路选型依据具体器件的数据手册，
通过 `lib device get` 的属性可取得 Datasheet 地址。转换器不会推断缺失的外围电路。

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

完整效果必须由整份 `sch layout-plan --zones` 成功结果生成，再交纸张规划和固定渲染器。
逐区捕获错误的诊断输出不能拼装为完整候选；保留诊断供修算法，但不以原测量占位替代失败区域。
发布本地效果时记录输入/输出哈希、源码提交、命令与覆盖检查；这不是 Git tag 或安装包发布。

两层统一间距模式在 zones 输入顶层提供 `spacing:P`，P 为 >=10 的 5 raw 网格数。
`layout-plan --zones` 生成具有该内边距的完整功能框，并将 spacing 带入输出；
随后附加 sheet 给 `layout-sheet-plan`，省略 sheet.padding/gap 时二者由 P 派生，
显式提供则必须等于 P，不能悄悄覆盖。相邻框只加一次 P，笔画半宽与网格取整另计，
实际净距可略大于 P。spacing 不改变电气间距或引脚 pitch，也不自动修改旧 compose 契约。
统一模式下 maxCandidates 是**每个 zone** 的独立搜索预算，candidatesUsed 为各区之和，
防止改变一区的计算耗费影响其他区结果；无 spacing 的旧输入仍沿用整份共享预算。

局部规划失败时执行有限 checkpoint 回退：撤回本区已放外围位置及受影响后缀线路，
尝试另一个真实 XY 再重新计算；核心与实测姿态不变，不通过修改网络或 NC 取得通过。
每个 checkpoint 最多尝试 3 个不同 XY，总回退上限 32 次；先用至多 16 次回退定向调整
冲突网成员，失败再扩展到包括异网导线所有者的完整阻挡集合，两阶段不重置预算。
阻挡归属未知时禁用剪枝。每轮使用共享预算分片（初始预算的 1/8，夹至 512..16384，
且不超过剩余预算），为回退保留搜索机会；片额不足也可能促使回退，不代表几何无解。
搜索返回 search.strategy/backtracks/repairAttempts/movedComponents/branchLimit/conflictPasses
诊断和总候选计数；失败/预算耗尽不返回完整 layout。
这不是无限搜索或全局最优保证，也不能替代现场文字与引脚回读。

合页预览使用 `sch layout-sheet-plan --from render.json --out pages.json`。
输入沿用 render zones，增加 `sheet:{bounds,border,keepouts,padding,gap}`，坐标单位 raw。
bounds 是原纸张，border 是内框，不能为放下内容而篡改；padding 指虚线笔画到内框的净距，
gap 是功能框之间的净距。用户未给具体值时可先提出更宽的预览值，不改变默认 compose 契约。
规划器保持区域内部全部几何与连接不变，只计算各区 `sheetPosition:{x,y}`（框左上角，y-UP）。
以数种确定顺序尝试无旋转、5 raw 网格装箱，选择页数较少的结果；不是最优性证明。
输出 `pages[]` 各项交 `sch layout-render`，绘制纸张、内框、padding 线和图签禁放区。
此模式渲染器严格使用给定位置、不重排；拒绝越界、相互重叠或侵入 keepout 的框。
输入各区均有合法 sheetPosition 且当前完整框仍满足纸张约束时，原位复用，返回
placementMode:reused；否则只重排矩形，返回 repacked，所有区内 layout 保持不变。
这是一页既有位置的复用，不是跨页迁移事务。已有 frame 不满足新的统一内边距时明确拒绝，
须先重新生成该区的框；整页层不暗中改大框或缩放内容。
红色 blocked 区仍未完成，不能把没有导线的占位区域当成电路验收或最终容量证明。
合页预览不迁移 EDA 页面，不合并网、不生成 Apply；确认后仍需完整连接/身份/纸张守卫。

固定离线渲染入口：`sch layout-render --from render.json --out layout.svg`，可选 `--zone <id>`。
输入 `schemaVersion:1,zones:[{id,title,status,layout}]`；layout 是局部布局输出，status 为
`planned` 或 `blocked`。也可直接读取 `layout-plan --zones` 的结果（缺省 status 为 planned）。
blocked 区只能显式提供原测量几何，不生成假导线。只转译数据，不重新求解、不调用模型或 EDA，
不输出差异图解；SVG 中区域整体平移仅用于展示，不代表纸张/电气验收。简化符号和字形不等同官方导图。
`layout-render` 和 `layout-sheet-plan` 默认校验完整 ID/引脚状态、几何和命名连通；
blocked 或只有器件没有必需连线的占位数据会失败且不覆盖已有输出。
需要诊断时显式加 `--diagnostic`，仍不允许缺失几何坐标、非法线段或伪造已完成状态。

标记引线按长度从短到长搜索，同长度才采用电源/地方向偏好；同一线树的各个引脚之间也比较
最短可行引线。两个二端器件同网面对面、共轴且已有直线连接时，优先尝试网格上的精确中点
垂直分支，用于对称取线；不能落网格或存在碰撞时保留原命名搜索，不移动连接点伪造对称。

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
输入 `schemaVersion:1/components/netPolicies/zones`，可选 `attachments/maxCandidates`；
每个 zone 为 `{id,title,coreComponentId,componentIds}`。components 与单区入口相同。
每个独立芯片/接口明确声明为一个核心；所有器件恰好归属一区，公共电源/地不用于猜归属。
共享外围先明确归属；跨区信号必须声明 `module_port`，不能用 `direct` 跨区后偷偷改标签。
输出每区的局部 `layout` 与包含器件、线路、标记及文字占位的 `contentBounds`；
contentBounds 不含标题；`frame` 使用既有标题/净距规则计算独立粉色虚线框预案。
标题宽度目前是保守估算，非官方实测。每区分别交 compose 模块整页排版；
补齐身份和纸张证据前不可 Apply。任一区失败整份不输出半成品。
这是显式归属的通用计算入口，尚不自动识别芯片功能或推断共享器件的归属。

外围在首个可行距离层优先比较连接端点到宿主出线轴的偏差，再比较线长等紧凑指标。
直连线树合并在直线/L 形无解时尝试向两侧最多 80 raw、步进 5 raw 的双折点绕行。
仍逐条检查异网引脚、器件、文字和已有线路；不放宽交叉判据，不保证交错多网全局可解。

普通器件集合不需要建 Lib：使用 `sch layout-plan --from input.json --out geometry.json`。
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
这是有界、保持实测姿态的求解器，失败不证明电路在任意朝向下都无解。改变朝向须重新提供
对应可信几何；不能放宽碰撞检查或修改网表来取得通过。已有手工设计好的 Lib 仍可直接 compose。

先安排仅连电源/地的外围，再安排信号相关外围，显式 attachTo 依赖仍须满足。
首个可行距离层内比较局部实线、轴线对齐误差、可见包络面积，不直接采用第一个可行位置。
部分放置候选只检查当前本体、引脚与线路，完整放置后才统一计算命名和最终连接/几何门禁；
最终评分包含标记与逃逸引线总长。命名失败同样触发本区回退，而不是把缺失标记当成成功。
同模块电源/地优先尝试 80 raw 内的安全直线/L 形合并，
不可短接时才保留独立命名点；不改变电气网名或跨模块引用。
密集引脚若没有合法直出标记，尝试有界正交逃逸：先沿实测引脚外向出线，再横移，最后接真实标记引线。
所有新增线段仍检查本体、异网引脚、已有导线和标记占位，不在渲染端假接或放宽短路判据。
`module_port` 优先直连；无合法直连的分离线树可分别接同名跨区端口，因该网已声明跨区引用。
`direct` 仍必须合为一棵线树，不得自动改为标签连接。
当前命名引线首先按 GND → 电源 → 信号搜索，失败再尝试两种空间顺序；同一线树比较
包含逃逸线的总线长，而非只比较最后一段标记 offset。符合条件的二端器件分支优先对称中点。
命名与逃逸候选消耗同一 maxCandidates 预算，失败的顺序不污染下一次候选。
这仍不是任意标记联合回溯；相同连接图与姿态可能因数组顺序产生不同结果。保留失败输入和最终计算参数，
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
实测 `sheet`、`keepouts` 和按阅读顺序排列的几何 `modules`。

| 字段 | 格式 |
|---|---|
| `sheet` | `{minX,minY,maxX,maxY}`，目标纸张实际 bbox。坐标单位 raw = 0.01 inch，y 向上。 |
| `sheetBorder` | 可选同格式 bbox，实际图纸内边框；模块虚线笔画在其内最少留 10 raw，考虑半线宽后向内取 5 raw 网格。缺少时输出 `sheet-bbox-fallback`，不能据此声称已验证红框净距。 |
| `keepouts` | bbox 数组，例如图签；从 `sch sheet-geometry --json` 取得，保留其来源与警告。空数组表示已确认没有禁放区。 |
| `modules[]` | `id/title/placements/wires/flags`，可附 `terminals/titleMetrics`；与 connectivity 的 Lib 成员逐项对应，每件只归属一个模块。 |
| `placements[]` | `designator/value/x/y/rotation/mirror/bbox/pins`；bbox 与引脚位置来自官方实测，器件和引脚坐标落在 5 raw 网格。 |
| `placements[].textBboxes` | 可选外置位号/型号 bbox 数组，格式同 sheet；也用于 measurements。坐标与当前实测姿态一致，随平移进入碰撞检查和框包络。普通 list 不自动提供，须另存测量来源；不控制实际文字位置，Apply 后须导图复核。缺失不代表文字为空。 |
| `pins[]` | 完整 `{number,name,net,x,y}`；NC 的 `net` 为空，与连接核心的 NC 状态一致。 |
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

## 从计算到现场

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
- 官方器件 bbox 不完整包含外置位号/型号文字；用官方 `sch export-image` 复核可读性，
  将必要净距写回源数据。短位号可能缩小文字预留并改变重新 compose 的紧凑排版；
  单独修位号不应附带重排。
- 报告未解决的 WARN、未知数据与未运行的验证。失败队列不会自动执行末尾 save；
  如需保留已核实的局部成果，先回读确认后另行保存，不把局部完成称为整板通过。
