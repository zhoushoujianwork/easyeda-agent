# EasyEDA 原理图

1.4 的工作对象是器件、引脚、网络和连接组成的数据图。先把电路和 Lib 的局部几何设计准确，
再编译为顺序执行的 SCH Apply。模块内部的核心器件与外围用短导线连成完整电路；电源、地及
跨模块信号按需要使用局部标记。遵守 [数据驱动架构基准](schematic-data.md#数据驱动架构基准)，
截图仅辅助发现采集/规则遗漏，不能替代数据驱动发现与修复。

按任务读取：

| 任务 | 参考 |
|---|---|
| IR、位号、Lib 合页与转换边界 | [schematic-data.md](schematic-data.md) |
| XY、引脚方向、紧凑标题、存量布局工具 | [schematic-placement.md](schematic-placement.md) |
| 计算 → Apply → 验证的操作顺序 | [auto-layout-sop.md](auto-layout-sop.md) |
| 局部接线、端口与断连 | [schematic-wiring.md](schematic-wiring.md) |
| 手写 Apply 或需要了解 action 返回值 | [actions.md](actions.md) |
| 原理图到 PCB 的整板阶段 | [design-flow.md](design-flow.md)、[pcb.md](pcb.md) |

## 目标与快照

```bash
easyeda health
easyeda doc ls --project <project> --json
easyeda sch connectivity --all-pages --project <project> > project-connectivity.json
easyeda sch list --project <project> --doc <page-uuid> \
  --include-device-identity --include-pins --include-bbox --include-wires > page-before.json
easyeda sch sheet-geometry --project <project> --doc <page-uuid> --json
```

- 每次读写绑定 `--project` 与 `--doc`，同名页使用 UUID。`--doc` 会确认并激活目标页；
  `windowId` 会随连接器重连改变，通常按工程路由更稳。
- `sch connectivity --all-pages` 逐页激活并恢复起始页。普通 `sch list --all-pages`
  可能只得到已加载页面的浅数据，不能用空 pins 证明无引脚或 NC；精确几何须逐页读取。
- `sch read` 是语义检查快照；`sch list` 加上述选项才是 compose 所需的完整实测基线。
  两者直接输出 JSON，没有 `--json` 参数。`--include-wires` 同时带活动页连接图元计数。
- 放置需要官方器件库 UUID；实例的 `primitiveId` 是修改句柄，`uniqueId` 是 sch↔PCB
  关联键，二者均不能当库 UUID。无法解析 device identity、pins 或网络时先补数据，不能猜测。
- 临时 JSON、计划和回读结果放入项目已忽略的临时目录。保留原始快照，在副本中设计目标。

## 器件与典型电路

先查 [standard-parts.json](standard-parts.json) 和 `easyeda blocks show <id>`。
已知 C 号用 `easyeda lib by-lcsc --lcsc C…` 精确解析；搜索结果仍须核对型号、封装与 C 号。
从官方器件记录的 Datasheet 地址查典型应用、引脚和外围参数，不凭外观或记忆补接线。
新选型可用 [parts-add.py](../scripts/parts-add.py) 写回标准器件库。

`sch resolve-lcsc` 与换件前的旧器件解析使用同一封装身份规则：双方有封装 UUID 时精确
匹配，双方同时提供封装库 UUID 时也须一致；缺失 UUID 才按去首尾空白、忽略大小写的
封装名称回退。只有 UUID、没有名称的实例仍要过滤封装。兼容库记录的嵌套 `footprint`
及旧版 `footprintName/footprintUuid` 字段。批量解析仅对完整查询条件相同的实例复用结果，
包括型号、项目库器件名、封装 UUID/库 UUID/名称，不能因型号与封装同名就跨身份复用。

`sch block-apply` 仍适用于已验证的电路块，支持相对关系模板和存量绝对偏移模板；它会读取
真实引脚、验证几何和连通，并登记功能子组。模板的功能引脚名必须与实际符号引脚表对应。
关系模板的 `--dry-run` 是两阶段预检：先按 anchor/flow/attach/pair 生成 `relation-seed`
估算包络并在已读图纸内选择原点；它不是实测贴脚布局，未放置时不会伪造引脚坐标。
`origin` 对应 anchor，`--at` 也是 anchor 的请求坐标。边界内无合法估算包络时拒绝落块，
不要接受图纸外的网格回退。正式执行先放 anchor、回读真实引脚，再求解其余成员；
未解出的关系仍是 pending，必须中止并按新建 ID 清理，不能将种子坐标写入现场或当通过证据。离线未读图纸时只能验证估算关系，
必须保留“未检查真实页面”的警告。
`compose` 消费已经设计好的 Lib 几何，不能代替任意器件的典型电路设计。

正常位号使用字母前缀加数字。库的 `U?` 等提供分配前缀，功能名称存 `role`。
历史非标准位号通过 `sch designators allocate/plan` 修复，稳定组件 ID 保持不变；
完整规则见 [schematic-data.md](schematic-data.md)。

<a id="easyeda-electrical-rules-load-bearing"></a>

## 必须保留的电气规则

- 器件引脚与 flag 重叠不构成连接。用“pin → 非零导线 → flag/port”；零长导线会被忽略。
- 同模块相邻外围优先真实正交线，尤其 VIN/VOUT 与电容。不要把连续外围拆成满页同名标签。
- GND/VCC 可就近重复放符号，减少环绕母线；普通信号用 netport，不伪装成电源 flag。
- 导线不能经过无关引脚或异网线。多脚连接应形成有实际锚点的连续线树，不依赖未验证的空中结点。
- 显式 NC 表示设计上不用的引脚。缺少网络信息不是 NC，也不能用删除器件引脚消除告警。
- `sch autoconnect` 会识别已连目标网；`sch connect` 不幂等，重发可能叠加导线和标记。
- `sch disconnect` 可能影响共享线树；返回的 `alsoDisconnectedPins[]` 必须逐个恢复。
  `partial`、`survivedIds`、`notApplied` 表示删除未完全生效，不能直接当作断开成功。
- 删除器件默认清理独占桩线/标记，保留其他器件共用的线树。与该器件无关的残线需另行识别。
  `bridge-check` 的 `orphan-tree` 才能发现“flag 连着线、整棵线树不接任何器件”的残留。

## 修改与恢复边界

布局修复先修改源数据/约束并重新求解、compose。已有连线的小范围移动可用 `sch group-move`
等带线工具执行已记录的源目标变更，之后同步回读及源数据；不能以现场补丁替代可重复生成链。
单独 `sch modify`、`align`、`distribute` 只动器件，不能视为带线移动。
换型号/符号/封装会重建实例，应重新取 primitive ID，检查 `pinDiff` 并验证网络。符号/封装
rebind 使用候选优先事务：先回读 Device association，候选创建且回读存在后才删除原件，最后
精确回读设备/符号或封装绑定、`uniqueId`、位姿和属性；失败时检查回执里的 `phase`、原件/候选
存在性和 `rollback.verified`。命令超时表示写入
仍可能晚到，禁止盲重试和 `pcb import-changes`，先做新鲜原理图回读并核对关联键。

`sch modify` 的 `otherProperty` 与兼容别名 `customAttributes` 二选一。连接器合并保留
现有属性，并回读检查；`partial`/非空 `notApplied` 是失败，`verified:false` 是未经确认。
回放 `propertiesBefore` 只能恢复覆盖值，不能靠 merge 删除本次新增的键。不要把库记录的
`Designator`、`Unique ID` 等投影字段整包写入实例。

清页先看 `sch clear --dry-run`，只在已授权的重建范围内执行；默认保留 sheet。
清后用 `sch clear --dry-run --expect-empty` 核对所有非保留图元，读取失败不是空页。
新 frame 和旧 zone-draw 分别拥有自己的图元，清旧标注用其对应命令，不按类型删除用户图形。

`sch prim-delete` 的有界 settle 后仍报 `partial`/`survived` 时，停止依赖步骤，保存命令、
原始回包与目标页 fresh list，核对指定 ID 的真实残留。残留本身不能证明 action 队列卡死；
不要按旧版 stderr 的重启或 UI 删除建议操作。只在原始失败与残留已冻结后，以明确的 typed
恢复方案重新核对基础调用链；恢复后的成功不能补签原始调用通过，也不能触发无限重试。
删除回读失败返回 `verified:false` 与 `unverified`，只表示结果未知，不计入已删除或真实残留；
不得据此重试删除、清理组成员或继续级联删除连线。只有回读证实消失的 ID 才能进入后续清理。
`prim-delete` 的 JSON 是最终复核结果；发生有界复核时，`attempts` 保留两轮原始结果。
Web 宿主导出网表后可能仍运行 `RealTimeSync`，此时官方删除 API 会拒绝执行。
连接器在导出结束，以及 `component.delete`、`prim-delete`、`disconnect` 每批删除前核对
原标签和文档实例，通过只读兼容适配器观察宿主事务，最多等待
5 秒同步结束，再调用官方删除 API；不结束宿主事务，就绪失败不触发删除重试。适配器不可用、状态未知、
其他事务占用、超时或身份漂移均报错；部分批次已经执行时仍须 fresh 回读核实。
其他复合动作中的旧清理分支尚未统一接入就绪检查，不据此宣称已覆盖。
这是有界就绪检查，不是保存证明；删除后仍核对对象，关键批次继续保存重载验证。

放置或修改超时不代表未落地。先按新鲜快照核实；`ACTION_ABANDONED` 或无法确认的回读
不得触发盲重试。`QUEUE_OVERFLOW` 表示该请求未执行。队列序号只证明 handler 顺序，不能
证明文档已保存。恢复后仍有 `partial`/`stillBroken` 就报告残余状态，再从实际数据规划。

变更先在 EasyEDA 内存中生效。daemon 的防抖 autosave 是兜底；通过阶段验证后仍显式
`sch save` 并确认 `saved:true`。若数据读回互相矛盾，先保存和检查连接器状态，必要时
`doc reload` 后再 `doc switch <uuid>`，重新取基线，不依据可疑读数删除电路。

## 验证与交付

- 原理图布局检查必须覆盖不同尺度：器件本体/位号、真实线树与标记、完整 zone 框和标题。
  这不是旧三层 tidy/move 架构；生成仍为区内与纸张两层算法。
  器件 `layout-lint` 为零或官方 DRC 通过，不代表 zone 之间无碰撞。
  规划阶段检查分区两两相交（包括包含、重复框），落地后还需回读实际框与标题并核对内容边界、
  框间间距和图纸边界。缺少现场几何时标记未验证，禁止宣称布局验收通过。
  `frame` 输入校验拒绝框间重叠；它不能代替现场回读，旧 `zone-draw` 与新 frame 的残留也须核对。

- 每页分别保存 `layout-lint`、`sch check`、`bridge-check` 与 SDK DRC 的实际结果；
  `sch gate` 可用于兼容旧脚本的聚合显示，但不作为写入许可。`fail` 表示发现问题，
  `blocked` 表示检查未完成；两者都要列出对象、证据和待修项。
- 上述检查不能证明设计意图。另将实际 connectivity 的组件 ID、pin→net 与 NC 对照目标图。
- `layout-lint --strict` 要求完整的单页本体、引脚和图纸几何，不能和 `--all-pages`
  合用。它不覆盖全部外置位号；需补位号与 marker 数据检查，非位号属性不参与布局判定。
  器件 tight-spacing 复用本页显式 zone/功能组 ownership：同一功能区内的紧贴不阻断，
  但同组真实 overlap 和跨组 tight 仍阻断；缺 ownership 时不得按同网或距离猜测豁免。
  `clusters` 对 owned wire 逐官方 flat segment 判成员相交，整条折线包络仅用于总体占地和
  页面边界，不能把 L 形空角算作碰撞。
  owned wire 的描边框相交只有在同一次 fresh 完整采集证明为无接点正交内部 X 时才可排除：
  保留原始段端，交点不得有任何导线端点、pin 或 marker；两侧物理线岛均需实际逐 pin
  网表与 marker 证据完整且各自网络唯一。网络同名、check 的 INFO 文本或缺测快照不是豁免
  依据；端点/T/共线接触及 part/marker 本体重叠继续拒绝。
  导线与 marker 本体/文字按可见 stroke 判碰撞；foreign wire 沿边、端点或 T 接不能因
  marker bbox 被统一内缩而漏检。只允许 marker 自身 lead 在自身 anchor 的精确收口。
- `sch check --json` 的逐条问题在 `result.findings`。SDK DRC 可能只返回布尔/聚合值，
  不能单凭它宣称官方 UI 所有警告已清除；未运行或缺数据的检查列为未验证。
- 用 `sch export-image` 导整页或指定 `--ids`；这是文档渲染，不依赖前台视口刷新。
  产物路径以响应 `artifacts[].path` 为准。BOM 和网表另用 `bom export`、`sch netlist`。

## 接口不足时

先查 `easyeda <command> --help` 和 `easyeda actions`。确无现成能力时，
`easyeda api search <query>` 可离线查询官方 API 索引；优先组合现有 typed actions。
必要的 `debug.exec_js` 只用于任务范围内的临时探测，输出须可 JSON 序列化。
重复使用的操作应落实为 typed action 与 CLI，再同步 Skill。网表读取用
`sch_ManufactureData.getNetlistFile()`，不要使用已废弃且可能挂起的 `sch_Netlist.getNetlist()`。

### Windows PowerShell JSON 补丁（#192）

`sch modify --id <pid> --patch-file patch.json` 从 UTF-8 文件读取 JSON 对象，支持 UTF-8 BOM，
绕过 PowerShell 5.1 传递原生程序参数时剥离 JSON 引号的问题。`--patch-file` 与 `--patch` 互斥。
PowerShell 可用 `'{"rotation":90}' | Set-Content -Encoding UTF8 patch.json` 创建补丁文件；
不要使用默认输出 UTF-16 的 `Out-File`。
原理图的显式 `--x/--y/--rotation/--designator` 仍覆盖文件中的同名键；
PCB 的 `--center` 仍不允许补丁包含 x/y/rotation。

### 引脚读取后的编辑失败（Web 4.1.60）

基础调用链现场观察到 `sch list --include-pins` 后紧接 `sch modify` 返回
`cmdKey` 异常；同样的读取后清除 NC 也曾未持久化。先 fresh 回读确认失败调用没有改变
目标，再 typed `doc open <原页UUID>`，核对工程/页身份后执行原操作，两个单项均恢复成功。
这只证明特定时序及恢复办法，尚未证明所有失败都由网表读取引起。不能盲重放可能部分
成功的写入，也不能把增加 reopen 的成功补签为原始连续调用通过；原失败证据仍保留。后续 16 次隔离修改及 NC 对照未复现，网表读取或保存本身
尚不能解释触发条件；未定位根因前不要加入无条件 reopen 或重试。

当前分别以 [bug #256](https://github.com/zhoushoujianwork/easyeda-agent/issues/256)（cmdKey）和
[bug #257](https://github.com/zhoushoujianwork/easyeda-agent/issues/257)（NC 清除）跟踪，不假定同源。
命令继续可用，但失败退出仍须停止依赖步骤；NC 清除后核对指定引脚 `noConnected:false`，
关键结果保存重载再读。dev.21 起，`sch modify` 与 `sch no-connect --clear` 在 stderr
提示对应 bug 和回读要求，stdout 保留原始 JSON；`partial`、`verified:false` 和未生效字段
仍非零退出。提示本身不说明本次失败，不触发自动重试，也不代表 bug 已修复。

### 原生 net_label 兼容性（#191）

`createNetLabel(x, y, net)` 是标注 EDA v4 起提供的 BETA API。仓库在
EasyEDA 3.2.186 的实测仍会挂起，交互界面存在该功能不代表扩展 API 可用。
不要通过改参数或连续重试处理此兼容性问题。超时后先回读实际连通和残留图元，
再按电气语义选择受支持的 netport/netflag；没有等价 typed 能力时标记 `unsupported`。
升级到支持该接口的宿主后仍须探测，不能只凭版本号宣称已修复。
daemon 在派发 `net_label` 创建/连接前检查宿主产品版本：V3 或版本不可确认时返回
`HOST_API_UNSUPPORTED`，不会创建桩线；`autoconnect --dry-run` 使用相同检查，批次含不支持的
label 时在任何写入前整体拒绝。不会自动替换为电气语义可能不同的 netport。
历史实测详见仓库 `docs/dev-environment.md` 的 Native net-label compatibility。
Web 4.1.60 会在创建原生标签后返回空对象。2026-09-24 命名/未命名导线的
调用前后属性差分均证明新增了指定位置的可见 `Name` 属性，保存重载后仍保留，
因此旧版快速失败回包不能说明未写入。dev.19 用新增属性、坐标、可见性与父导线网络
共同验证；无法唯一验证时保留部分写入证据并非零退出。不能盲重试或自动删除桩线。
具体契约见 [连接原语](schematic-wiring.md)，现场覆盖以对应版本的单项报告为准。

## 检查覆盖边界（原理图验收）

### typed 写入的正确性校验（安装并实测后才算现场覆盖）

daemon 对 `schematic.wire.create` / `schematic.power.connect_pin` 自动读取本页真实
引脚方向、本体与导线，拒绝逆向/垂直出脚、零长/斜线、穿本体及几何缺测等非法输入；
位姿修改、放件、换件、符号/封装重绑定、整组移动自动做几何前读和后检。这些校验保证
typed 路径不会写入已知非法几何，不承担阶段许可，也不能替代主动运行事实检查。
局部新增导线/连接允许保留未改变的历史几何问题，回执列出 `preexistingFindings` 与
`baselineFindings`；新路径仍完整校验，写后新增或改变的问题仍失败。缺测不作为历史问题豁免。
旧零长度段不参与新增导线的接触拓扑，但仍保留诊断；本次新增零长度线仍拒绝。
写后必须有同工程/页及 FIFO 顺序证据；线创建还要证明实际路径覆盖请求路径。
返回 `SCHEMATIC_GEOMETRY_INVALID`、`partial:true` 时，可能已落地，须回读修源后重新生成，
不盲重试或当作已撤销。几何输入校验不替代器件身份/位姿命中、电气连通或标记完整性的独立对账。
写入和会切页的读取共用窗口互斥；不能在另一条写入回读期间切页或叠加修改。

`sch check` 使用同一纯规则，逐条给出 `pin-exit-direction`、`wire-through-body` 等
ERROR，保留器件/引脚/线段证据。允许先向外再折线；不二次套用器件旋转/镜像改变官方
已经转换到页面坐标的引脚 rotation。原生手动编辑、任意 debug 脚本不是受保护的声明式
施工入口，仍需重新读回检查，不能把这种调试操作包装成完成验收。

严格内部 X 的无接点豁免按物理线岛取证：宿主 wire net 为空时，只有该岛上的逐 pin
官方网表和明确 marker 唯一证明同一非空网络才可记 INFO；缺证据或冲突仍为 ERROR，
且 X 不参与线岛合并。Designator 对导线的遮挡按官方原始编码解析；`flat-segments`
中的四坐标记录彼此独立，禁止把相邻记录尾首连接成不存在的斜线。

完整 compose 源与最终 Apply 守卫必须保留核心/外围 ownership。外围需沿真实导线和
必要的串联外围链连到本区核心；本区核心和外围共享的非电源/地信号脚还必须在同一导线岛。
跨区同名端口不建立这条实体连接，第三器件的存在不豁免本区专属支路。
电源/地可局部命名，但不能让去耦等外围所有端点均为独立标签而失去实体归属路径。
缺 owner、缺实际几何或只用期望标记伪装读回都不能通过。无源 ownership 的裸 `check`
不能证明外围语义归属；不得将它的零 findings 当作此项通过。

`sch check` 同时读取现场分区矩形和自由文字的真实 bbox：旧框重合报
`partition-overlap`；自由文字压器件/标记/其他自由文字报 `text-overlap`，
文字跨分区边界报 `text-frame-crossing`；它们作为 `sch check` finding 返回具体对象。
完全在框外且归属未知的文字不猜测归属。非空文字缺 bbox 或几何读取失败不能当作零碰撞。
分区包围的器件范围是本体和位号（如 Q1、Q2）；型号、参数、描述等非位号属性文字
不参与页面碰撞计算，不要求入框，重叠/越框均不报警，也不据此扩框或判验收失败。
位号通过逐器件 `sch_PrimitiveAttribute.getAll(parentId)` 读取，仅选 `Designator`，
再取真实 bbox。整页 `getAll()` 在 3.2.186 会返回空数组，不能据此判定无位号。
位号不可读、隐藏、身份不符、bbox 缺失均属未验证；不能用默认文字宽度代替。
位号与本体/标记/自由文字/其他位号/导线的遮挡及所属框越界进入 `sch check`。
标记文字带目前仍为估算；自由说明文字压导线尚未覆盖，必须列为未验证并补数据检查，
不能以官方导图代替机器检查。以下新增检查的现场通过须以安装版本和真实运行报告为证，
源码或离线回放通过不代表已部署。
`layout-score` 任一维未测时 verdict 为 `incomplete`；显式 `--min-score`
遇到缺测也失败，即使已测部分综合 100。frame-fit 只有部分文字归因时仍算未完成。


## 修改属性时保留供应商身份

在 EDA 3.2.149 的已验证现场，`schematic.component.modify` 仅改位号时会把既有
`Supplier Part`/`supplierId` 从真实 C 号重置成型号加 `.1`；重载后库身份解析因此失败。
对于已读回且本次不打算修改的供应商料号，把原 `supplierId` 放入同一个 patch，
不从型号推测、不硬编码某个电路的 C 号。值、位号或其他自定义属性修改也要检查这一不变量。
显式更换料号时以调用者的新值为准，不能保留旧值覆盖请求。

改前保存实例身份与料号，改后独立读回并比较；显式保存、typed 重载后再次运行
`includeDeviceIdentity=true`。供应商字段保留与库 UUID 解析是两个判据，不能相互替代。
该规则是已发布连接器的通用操作补偿，不表示其 handler 已自动保留所有未指定属性。
