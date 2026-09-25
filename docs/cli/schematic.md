# 原理图功能支持全景(CLI 视角)

`easyeda sch` 域的**当前能力清单 + 待支持路线**。定位:让 AI agent(或人)可以完全通过
typed CLI 操作嘉立创EDA专业版的原理图——每个动作可观测、可校验；写入不具备事务回滚。

设计、布局、检查和修复统一遵守
[数据驱动架构基准](../../.agents/skills/easyeda-agent/references/schematic-data.md#数据驱动架构基准)。
下面的存量命令是能力目录，不是要求逐个执行的主流程；fix 建议须回写源数据并重新求解，
不能照抄现场挪件后就称算法闭环通过。

> 动作目录的机器可读真值是 `make actions` / `easyeda actions`;本文是**人读的功能地图**,
> 按「AI 操作原理图需要什么」组织。设计流程(何时用哪个命令)见
> [`.agents/skills/easyeda-agent/references/design-flow.md`](../../.agents/skills/easyeda-agent/references/design-flow.md) S0–S6。

## 一、已支持(按功能域)

### 1. 器件与库

| 能力 | 命令 | 说明 |
|---|---|---|
| 库搜索 | `sch place` 配套(`schematic.library.search`) | 立创/LCSC 库自由搜索,返回 libraryUuid+deviceUuid |
| 放置真实器件 | `sch place` | 按 uuid 放置,`--designator` 原子分配位号(免 place→list→modify 往返) |
| 换型号 | `sch replace` | 换库器件,pinDiff 非空提示需重接线 |
| 符号/封装重绑 | `sch rebind-symbol` / `rebind-footprint` | 五步 rebind(modify→delete→create→restore) |
| C 号解析 | `sch resolve-lcsc` | 已放置器件 → 真实 LCSC C 号(确定性,绝不模糊兜底);dry-run 默认 |
| 属性修改 | `sch modify` | `--x/--y/--rotation/--designator` 快捷 flag,复杂属性走 `--patch`(两来源可并用,flag 覆盖同名键);**merge 语义**:只 patch 顶层字段(如 supplierId)时自动保留全部 otherProperty 并回报 `propertiesPreserved`(#175) |
| 位号修复 | `sch designators allocate / plan / verify` | 全工程 IR 按官方库前缀分配非标准位号；保留已有数字编号，功能名存 role。生成同页 modify 队列，文件 SHA 与现场前后数据守卫检查 ID、引脚、网、位置、导线并同步组成员；经 `sch apply` 执行 |
| 删除 | `sch prim-delete` / `sch clear` | 唯一删除入口:按 id 删**任意图元**(器件、文本、图形、导线)/ 整页清空(dry-run 可数)。旧 `sch delete`(仅器件)已移除 |

### 2. 连线与网络

| 能力 | 命令 | 说明 |
|---|---|---|
| 画线 | `sch wire` | 折线;netflag 必须经真 wire 连(重叠坐标不算连接,平台规则) |
| 引脚出线+标志 | `sch connect` | pin → 短 stub → netflag/netport,`--pin U1:5` 或 `--x/--y` 二选一定位,显式方向/offset;自动补偿平台「旋转存储取负」的坑 |
| 智能连接 | `sch autoconnect` | **打分器**自选方向/offset:碰撞/穿件/图签/fanout 通道全几何成本,含 **netport 竖排折叠惩罚**(密集引脚列不再把标签翻竖);幂等(已连跳过),`--replace` 换网 |
| 断开 | `sch disconnect` | connect 的逆操作:stub+flag 成对删(免孤儿桩) |
| NC 标记 | `sch no-connect` | 仅落实源设计中明确不用的物理脚；不能将 floating-pin 清单自动改为 NC 来消警 |
| 网表 | `sch netlist` / `sch read` | 导出网表 / 一次调用语义快照(器件+网络+检查) |

### 3. 布局与整理

| 能力 | 命令 | 说明 |
|---|---|---|
| 本地设计对账 | `sch design-diff expected.json actual.json --exit-code` | 按稳定ID核对器件、引脚、网、几何；两份完整compose计划还比较导线、框和标题，报告覆盖范围及未验证项 |
| 显式核心/外围区内求解 | `sch layout-plan --zones --from ... --out ...` | 连接/几何、zone 所有权、attachments 与策略 → 完整区内候选；任一区失败不输出完整结果 |
| 位号实测几何导出 | `sch designator-geometry --project ... --doc ... [--out ...]` | 只读当前页逐 part 读取唯一可见 Designator 的官方 bbox、parent、值及来源；缺失、重复、隐藏或无效数据即失败，可按 parent 写入源测量 `textBboxes` |
| 核心移动/单脚标签修复 | `sch layout-edit --source ... --page ... --snapshot ... (--move-core ID --to X,Y \| --repair-pin ID:PIN) --out ... [--playbook ...]` | 从保留源与新鲜快照生成目标；不直接写页面。核心固定目标后可重算本区，单脚修复生成作用域 playbook |
| 完整区域合页 | `sch layout-sheet-plan --from ... --out ...` | 只选择/平移合法完整候选，统一 spacing、Z 型流和同页约束，不拆外围 |
| 固定数据渲染 | `sch layout-render --from ... --out ...` | 校验并转译同一目标，不补线、不改坐标；不证明现场已 Apply |
| 已选页固定转换 | `sch compose --layout-page page.json --from ... --before ... --playbook ...` | 保持已确认的区内/整页几何及 spacing；本页源 `titleBlock` 文本经 typed 命令写入并回读，再过 strict gate |
| 通用求解诊断 | `sch layout-plan --from input.json --out layout.json --report report.json [--zones]` | 源哈希、算法版本、阶段及结构化冲突；失败非零、不输出半计划，有限搜索失败不称全局无解 |
| 已有实例无损重排（dev.6 开发验证） | `sch compose --replace --preserve-instances --layout-page page.json --from ... --before ... --playbook ...` | 保留原 primitiveId/uniqueId/参数；只重算绘制内容，源身份/属性与末态均检查；开发能力不等于现场验收通过 |
| Lib 内部计算 | `sch lib-layout --from ... --out ...` | 既定电路图与实测姿态→局部位置、短线和电源地；有界搜索，输出compose输入 |
| 单页 Lib 组合 | `sch compose --from ... --out ... --before ... --playbook ... [--replace]` | 保留原位号；完整IR与模块几何→端子直线错长、紧凑标题、Z字紧凑框排布、固定10 raw边距（实测sheetBorder可核验内框净距）；实际引脚/NC/bbox/导线路径回读。跨页位号唯一才许重建；[范围](../schematic-page-composition.md) |
| 固定 LDO 数据规划 | `sch power-layout --from ... --out ... --playbook ...` | 实测几何→器件/引脚/线/电源符号/模块框;标题择上下空档压缩包络后,默认左上 Z 字起排、同行顶齐、每框保持自身高度;输入可带实测 `titleMetrics`;`--frames-only` 只验证并补框 |
| 模块呈现转换 | `sch frame apply/check --from ...` | JSON→粉色虚线框+0.2 inch 标题;按页面和模块记录 ID,回读样式/实际文字边界;可选 `titleLayout` 核验预测包络和障碍物净距,重复 Apply 不增图元 |
| 模块感知自动布局 | `sch autolayout` | 双引擎:`template`(spec 驱动,核心放分区中心+外围环绕,确定性,布线前用)/ `official`(平台 @beta 兜底,破坏性,`--rewire` 网表重建) |
| 空隙打包 | `sch autoplace-free` | 无分区场景往空白处塞件 |
| 对齐/等距 | `sch align` / `sch distribute` | 按渲染 bbox 对齐(left/right/top/…)/ 单轴等距摊开;默认 dry-run;选集**部分覆盖**持久组时硬拒绝(`--break-group` 显式放行) |
| 刚体平移 | `sch group-move` | 器件+桩线+flag 一起搬:`--ids`(无状态,每次传全 id)或 `--group <id>`(持久组,成员桩线+远端 flag **自动展开**,触碰非成员脚的线树留在原地并报告) |
| 持久化编组 | `sch group create/list/add/remove/ungroup` | **virtual group**(平台墙真机坐实:EasyEDA Pro 3.2.121 的 `eda.*` 无编组 API,组件实例 70 个方法/属性零 group/parent 字段——UI 原生组对扩展完全不可见;后经用户 UI 实建 Group1 复核:44 图元全状态前后差分 0、selection 三种读法含私有属性零组字段,与 virtual group 并存不冲突)。`create --if-absent` 仅在名称、成员及来源均相同才保持不变。按 documentUuid 存 workflow state(同 zones claims 模式);成员存**位号**(netlist key,页内稳定;primitiveId 在 wire 重建/reload 时会变),move 时解析当前 id;同一位号只属一个组(入组查重报所在组);组空自动删;`list` 标 stale 成员;autolayout/autoplace-free 检测到组时警告(v1 不保组内相对几何) |
| 布局硬门 | `sch layout-lint` | 真实渲染 bbox 查重叠(ERROR 非零退出)/紧间距/off-grid/分区违规 |
| 组内布局计算 | `sch group tidy` | **三层体系 Group 层**:pattern auto/power-updown/signal-row——双电源旗电容自动竖放+上电下地+**文字朝外**(真机校准 rotation 表);实测 pin 二义消解、stale 双读、未建模第三连接拒绝、连带断开即错、自检红即逐步回滚 |
| 功能区刚移 | `sch zone move` | **Zone 层**:区内组+散件+桩+旗+note 整体平移;**全区一份展开**(区内直连线随行,跨区线才留守);出界/压图签硬拒、压他区警告;分区框自动重画(重画前指纹 settle) |
| 组间叠加布局 | `sch zone tidy` | **Zone 层**:区内组当刚体排布(锚组+上下堆叠,hGap 默认 117 可调);装不下给最小尺寸诊断不硬塞;双认领图元差集(正/回滚对称);自检红逆序回滚 |
| 布局质量分 | `sch layout-score` | 五维诊断与归因；proximity 不等于外围所有权硬门，缺测不算通过。fix 提示用于修改源约束，不照抄现场试摆；实际覆盖见 Skill |

### 4. 页面组织与分区

| 能力 | 命令 | 说明 |
|---|---|---|
| 分页管理 | `sch pages` / `page-new` / `page-rename` / `page-delete` | 页集合要 reconcile 到模块计划(改名成功能名/补页/删多余空页) |
| 分区认领 | `sch zones set/status/clear` | 模块→分区格的持久化认领,layout-lint 消费出 zone-violation |
| 数据驱动分区规划 | `sch zone-plan` | **一个虚拟组 / zone 认领 = 一个分区**(与 `zone-arrange` 的区一一对应,同一把尺;网格带合并那条路已删),框按成员真实 bbox 撑出来;校验六项(越界/重叠/**压图签**/模块出区/标签碰撞/**贴纸边**)全 0 才许画;抬升与校验共用同一安全余量常量(治「按 A 抬按 B 校」假绿) |
| 画分区框 | `sch zone-draw` | 虚线框+区名(`--mode partition` 整纸版式,框贴合模块内容不拉满页,给图签留缺口) |
| 图纸/图签 | `sch sheet-geometry` / `titleblock` / `titleblock-get` | 默认查询比例估算；`sheet-geometry --from-svg <全页.svg>` 离线解析精确内框/图签、绑定源 SHA-256 / 明细表读写 |

### 5. 校验与门禁

| 能力 | 命令 | 说明 |
|---|---|---|
| 逐项设计检查 | `sch check` | 平台 DRC 只给聚合数,check 从图元重建逐项 finding:悬空脚/几何-网表错配/导线交叉/压引脚/零长线/悬空线/重合标志/压图签/标志互压/**多器件页未分区**(missing-partition,铁律#15 机械兜底;**证人是画布**:认页上的区标题文本——与 `zone-draw` 生成标题同一个函数——绘制记账仍读,两者**取大**,所以换机器/清 state/`--project` 名字不一致导致记账丢失时不再恒报;口径未放宽,真没画框照报)/**netport 竖排折叠**(folded-net-label) |
| 桥接检测 | `sch bridge-check` | 树粒度:共线合并短路 BRIDGE / 孤儿桩 ORPHAN(单线视角看不全的盲区) |
| 官方 DRC | `sch drc` | SDK 门(可能仅聚合) |
| S5 一条龙门 | `sch gate` | 固定顺序 layout-lint→check→bridge-check→drc 出一张报告;`--strict` 全部 WARN 也拦;`verdict=blocked`=检查器没跑成≠板子有问题 |

### 6. 电路块库(拓扑复用)

| 能力 | 命令 | 说明 |
|---|---|---|
| 浏览/查找 | `easyeda blocks ls/show/search` | 离线,20 块/11 类目;块携带 internal_nets/ports/parts/pcb_layout/silk 多维知识 |
| 一键实例化 | `sch block-apply` | 放件+内部连线+端口绑定+落位避让+失败补偿回滚;带 `schematic_layout` 模板的块按人审过的几何落。`--spec <s0.json>` 落块后自动回填真实位号;`--max-attempts N`(默认 3)在同一失败签名重复 N 次时**动手之前**停手,组比整页还大时报 `page-too-small`(停手问用户,不自动分页) |
| 模板反推 | `sch extract-layout` | 真板摆好的实例 → 反向导出块模板 JSON(「摆好一次→固化」数据管线) |

### 7. 导出与视图

| 能力 | 命令 | 说明 |
|---|---|---|
| 页面导图 | `sch export-image` | SVG/PNG/PDF,选区或整页,不依赖前台视口(残留进度条已在导出后主动销毁) |
| BOM/网表 | `sch export`(bom/netlist) | BOM 自动补 LCSC C 号(`bom-enrich.py`) |

## 附:易混命令辨析(AI 选型速查)

同族命令的边界与参数差异——**每条都来自 agent 真实误用记录**,读这张表可以一次选对:

| 你想做 | 用这个 | 别用/别混 | 参数注意 |
|---|---|---|---|
| 给引脚接网络标志(常规) | `sch autoconnect --pin R1:1 --kind netport --net EN` | — | `--pin` 是 `位号:脚号` 一参式 |
| 给引脚接标志(指定方向) | `sch connect --pin U1:5 --direction right` | 也可 `--x <px> --y <py>`(裸坐标/无位号场景) | `--pin` 与 `--x/--y` 互斥二选一 |
| 断开引脚的 stub+flag | `sch disconnect --pin C4:1` | ⚠ 不是 `--designator C4 --pin 1` 两参式 | 也可 `--flag-id`/`--wire-id` |
| 挪器件/改属性 | `sch modify --id <pid> --x 100 --y 200` | 复杂属性(customAttributes 等)走 `--patch '{json}'` | flag 与 patch 可并用,flag 覆盖同名键 |
| 删图元(任意类型) | `sch prim-delete --ids id1,id2` | 旧 `sch delete`(仅器件)**已移除** | `--ids` 是 **CSV**(JSON 数组已不再接受) |
| 清整页 | `sch clear` | — | 残留或枚举警告非零退出；`--dry-run --expect-empty` 只读验证所有非图纸图元为空 |
| 列页/切页 | `sch pages` / `sch open` | `doc ls`/`doc switch` 是跨域老入口,功能重叠 | sch 域内优先用 sch 命令 |
| 出图给人看 | `sch export-image` | `snapshot` 是**视口截图**(需前台、会 stale) | export 不依赖前台 |
| 读电路状态 | `sch read`(=list+nets+check 聚合) | 只要器件清单用 `list`;合页快照加 `--include-bbox --include-pins --include-wires`;只要检查用 `check` | read 最贵但一次拿全 |
| 分区框(整纸版式) | `zones set` → `zone-plan`(校验)→ `zone-draw --mode partition` | ⚠ 固定九宫格 claim 对宽模组会误报 zone-violation——partition 画完后 `zones clear` | 三段链,顺序固定 |
| 整组挪动(免收集 id) | `sch group-move --group g1`(先 `sch group create --members …`) | `--ids` 是**无状态**老路:每次手工传全部 primitiveId | 两 flag 互斥;`--group` 存位号、move 时解析 id,并自动带上成员桩线+远端 flag。**移动目的地保持净空**:把组临时压到其他电路上再搬走(如 ±100 往返测试)会造成"树终止于异脚"的接触歧义(与真连线几何不可分),个别桩可能按真连线保留原地并报 note——真实用途(挪到空白区)无此问题,压他人电路本身就是 layout-lint 会拦的违规布局 |
| 建裸网络标志 | **尽量别用** `sch netflag` | 裸 flag 不经 wire = 假连接(铁律 9) | 用 connect/autoconnect |

**参数风格已收敛**:pin 定位统一 `--pin 位号:脚号`(connect/autoconnect/disconnect 同式,connect 另留
`--x/--y` 裸坐标能力)、`--ids` 统一 CSV、modify 快捷 flag 对齐 place。旧的 JSON 数组 `--ids` 与
`sch delete` 命令已移除(不留兼容)。

**现行架构是区内/纸张两层数据计算**，见 [架构](../architecture.md)。
存量 `group/zone/sheet tidy` 和 `zone move` 仍是维护工具，不构成新设计的布局主链。
维护时明确完整成员及附着导线/标志，旋转后重新读取真实引脚，移动目标预留净空；
回读不稳定时停止依赖旧坐标。局部零重叠不能证明外围归属、真实直连或整页正确，
失败也不意味着宿主提供事务回滚。移动安全经验见 [ADR-0004](../adr/0004-schematic-move-primitive.md)。

## 二、待支持 / 路线(按 AI 可操作性缺口排序)

> 持久化编组(原路线 §1)已落地,见上文「布局与整理」表的 `sch group` 行。
> 存储在 workflow state 的 `groupsByPage`(按 documentUuid),结构刻意**不 sch
> 专名化**——PCB 侧同需求(#173)落地时同一存储直接挂 PCB 文档的 uuid。
> v1 范围:group-move 自动展开附着物 + align/distribute 刚体保护;autolayout /
> autoplace-free 只警告不保组内几何(组感知重排是后续项)。

### 1. 检查覆盖与源数据闭环

`sch layout-score` 已有诊断能力，不等于完整自动精修。外围所有权/直连、位号与框的完整
原始几何覆盖，以及源输入到现场的溯源须分别举证；未覆盖必检项不能靠截图补签。
型号/参数等非位号属性文字排除页面碰撞和框包络，位号保留。

### 2. 显式归属与自动语义识别的边界

通用 `layout-plan` 已沿连接计算核心/外围位置；zone 所有权和共享外围归属仍需源输入
明确声明，不把局部网络启发式当作自动理解整个功能电路。旧 block fallback 不定义新主链。

### 3. 框与必检几何

当前 compose/frame 已消费器件、导线、标记、标题占位，不是只算器件 bbox。
必检范围与现场测量限制以 [Skill 检查覆盖](../../.agents/skills/easyeda-agent/references/schematic.md#检查覆盖边界原理图验收)
为准；不能以预测包络代替真实回读。

### 4. zone-draw 的 stale bbox

modify 挪件后**立即** zone-draw 会按旧 bbox 画框(隔几个命令重画就对)。
需要在 zone-draw 取数前强制 fresh 读(schematic 侧的 stale 时序与 PCB reload 同族)。

### 5. netlabel(实线中途标网名)

connect/autoconnect 只有端点挂 netflag/netport 一种表达;同模块内「实线直连 + 线上标网名」
的行业画法(如上拉电阻串进引线)需要 netlabel 创建 API 封装。

---

*本文与 [`docs/FEATURES.md`](../FEATURES.md)(全域 action 清单+roadmap)互补:那边是动作粒度,
这边是「AI 操作原理图」的功能域视角。改动原理图相关命令后请同步本文。*

1.4 不再提供独立 `sch note` 命令,也不以 Notes 的存在或归属阻塞检查。模块框标题由呈现数据转换生成,粉色字高 0.2 inch(20单位),方框使用虚线。标题放在上方或下方的合法空档,不固定保留顶部标题带。数据字段与转换验收见[模块框与标题](../schematic-frame-conversion.md)。普通文字读取仍使用 `sch text-list`。
