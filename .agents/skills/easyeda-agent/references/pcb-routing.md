# EasyEDA PCB — 布线 / 铺铜 / 禁布区 / 填充区域

> 从 [`pcb.md`](pcb.md) 拆出(RFC #178):这几节只在**动铜**时才需要,不该压在每次 PCB 调用的上下文里。
入口、坐标系、正确性检查与写后回读语义仍在 `pcb.md` —— **先读它**,再按需读本文件。

---

### 关键网与叠层

`pcb route-critical --spec <S0.json> --dry-run` 先检查真实铜层数与关键网方案。
执行时沿用同一份 spec；`stackup.layers` 与活板不一致，或读不到可靠铜层证据，
命令会因缺少可靠叠层输入而拒绝非法计划。先用 `pcb layers` 检查；即时读取若带 `staleRisk`，
可用于诊断，最终叠层证据在 save/reload 后重读。
不把未启用的内层、图层总数或默认两层当成真实叠层。

- 两层板的电源步走 `pcb power-pour`；四层及以上走 `pcb power-planes`。
  `route-critical --allow-stackup-change` 不会把正常两层板改走内电层。
- 独立 `power-planes` 要求至少四层；已有六层等叠层保持原层数。
  只有用户已明确授权将已确认的两层板升级为四层，才使用
  `pcb power-planes --allow-stackup-change`。未知层数不能被此开关绕过。
- `power-planes --dry-run` 运行相同叠层预检；允许升级时，结果中的 `stackup`
  明确列出当前层数、目标层数、证据来源和是否需要修改，不写入板子。
  需要改变已确定的设计叠层时，应先单独确认需求并更新 S0，不能为方便布线擅改。

### Routing (copper tracks + vias)

### 离线单层寻路与独立复验

`easyeda pcb route solve --board board.json --from request.json --out plan.json`
读取 `pcb dump --include-copper` 快照，使用共用 Go 内核计算单层零过孔路径。
`easyeda pcb route check --board board.json --from request.json --plan plan.json --out check.json`
按独立需求重查端点、网、层、线宽、45°方向/转向、绕行边界和整段净距，不重新求解。
两条命令均纯离线，不连接或写入编辑器；用户只需现有 easyeda CLI。
每次命令默认在 JSON 旁生成同名 `.svg`：整板显示目标层焊盘、过孔、已有走线、静态/铺铜、
开槽、禁线区、搜索边界、端点和候选路径。候选同时绘制真实线宽和净距光晕；检查失败时
保留待检路径并标红，有限搜索失败也绘制端点、搜索范围和原因。SVG 是人审预览，JSON 与
独立几何检查仍是机器证据；两者都不写编辑器。

请求示例（mil、y-UP，端点使用唯一 `位号.焊盘号`）：

```json
{"schemaVersion":1,"units":"mil","net":"SIG","layer":1,"widthMil":6,
 "from":"U1.1","to":"J1.1","stepMil":5,"maxDetourMil":100,"maxStates":100000}
```

板级规则必须来自真实读取；铜清单和精确几何必须完整。当前 CLI 只支持 TOP/BOTTOM，
RECT/圆角 RECT/OVAL/圆焊盘、直线、实心多边形铜区、禁线区和多边形开槽。铜区描边宽度
计入净距；圆弧铜/轮廓、非实心填充和热焊盘辐条暂拒绝，避免用未补偿的近似证明净距。
缺测或未知形状输出 incomplete，不退化为 bbox/空障碍。
结果包含原始板文件与需求 SHA256、候选路径、搜索状态和 `preview` 路径；复验要求同一输入文件版本。
`pass` 仅证明该端点对在声明规则下的离线路径，不能代替整板连通、差分/阻抗、回流或官方 DRC。
有限搜索失败与预算耗尽均输出 incomplete 和非零退出码；报告不包含可直接执行的写入队列。
应用候选后仍须 typed 写入、save → reload → fresh dump → 原生 DRC；整板布线遵守布局确认约定。

### 联合逃线与模块联动（offline-verified）

布局候选必须同时预留所选核心四侧逐脚出线、供电和 GND 空间；不能用固定器件间隙、
单网各自可达、零碰撞或当前 DRC 无短路代替多网共同可走。明确 NC 才可排除；未知不作 NC。
普通信号 TOP 优先、全网最多两个过孔；本例 OSC、USB、CAN、PA9–PA14 为 TOP/0 via。

规划接口：`pcb escape-plan --from layout.json --board board.json --out escapes.json`；
`pcb layout-plan` 将同一内核用于候选及多模块联动，`pcb module-check --requirements layout.json`
使用独立需求和新鲜回读检查预留。schemaVersion 3 声明 routing 与 reflow，旧输入兼容但不
自动获得逃线通过结论。预留路径是规划数据，不写成工程铜；预览、apply 与验收共用几何。

职责边界固定为：Layout 求解器枚举模块位置、旋转、移动组与外部连接重算；每个布局候选
投影为临时板状态后，调用 PCB Router 公共内核验证固定几何下的端点路径和共同通道。
Layout 不复制通用 Router。Router 单网通过也不批准器件位置，必须由 Layout 汇总同一候选中
所有需求的占用冲突。Router 接口新增能力时保持这一适配层，不让晶振策略直接依赖其内部队列。

公共实现位于 `pkg/pcbmodel`、`pkg/pcblayout`、`pkg/pcbrouting`、`pkg/pcbsolve`。板级入口为
`pcb layout solve/check/render`：`SolveJoint` 让后续网络看到已选路径并有限回溯，`pcbsolve.Check`
从原始板和 move 重建投影后独立复验。典型文件案例覆盖路由反馈让位、TOP/BOTTOM/TOP 两过孔、
两网各自可达但共同失败；公共包文件案例为 offline-verified，`ceshi/PCB1` 双网测试通道的
自动让位与 layout-only Apply 已完成保存重载现场复验，规划路径仍未写成铜。

先调整路线/组内布局，再移动目标模块，再递归纳入挡路的可动组，平移失败才换位置/允许
朝向。每组声明成员、固定轴、内部铜归属和外部连接；整体变换内部对象，重算外部引线，
重建铺铜后验证。未知归属作为固定障碍。检查全部受影响模块，有限搜索返回原因与预算。
输出最多三个候选，分别比较移动范围、位移、过孔、关键路径长度和转折，不给综合评分。

AT32F415 旧晶振对象已按身份清理并保存重载。首个 schemaVersion 3 候选虽离线生成 33 个
焊盘的共同逃线并完整执行 90 步，但第一轮 fresh `module-check` 发现护环 PID 替换、OSC_OUT
非端点铜重叠、U6.3 逃线终点不一致和旧对象声明不一致，已标 `candidate-rejected` 并精确回退到
干净语义哈希。U6.33 的 `existingViasOnly` 两个 EP 地孔在回退后保持。
U6/固定孔/CARD1 固定、CN1 固定 y=42mm 和 180°。允许必要的 LDO/USB 带铜模块联动，
但不得只移动 C5/C6/C10 而丢失核心外围关系。护环、外围孔、双层禁铺和局部铜必须避让
共同逃线通道。完成 CLI/Skill、负例测试、现场两轮保存重载验证及独立验收后才 live-verified。

`routing.demands[].existingViasOnly=true` 表示该需求只允许列出的既有过孔充当换层/接地
witness。每个 `existingViaIds` 必须在 fresh 快照存在，网络、位置及
`existingViaInPadAnchors` 的焊盘归属全部匹配；它不会创建新过孔。`maxVias=0` 与这一模式并不
矛盾，因为计数约束针对候选新增过孔。缺孔、错网、错焊盘或身份过期都必须失败。

干净 OSC 基线可以声明空 `existingObjects` / `replacePrimitiveIds`，但 planner 必须证明 fresh
快照上的 OSC track/arc 集合也严格为空。若输入多一个不存在 PID、漏掉任一实际 OSC 铜或只列
子集，均失败。空集合通过不代表跳过旧对象核对。

Real routing primitives — **additive creates**, like the schematic
`wire.create`. Bind to a net **by name** (pull from `pcb.nets.list`); layer ids from
`pcb.layers.list`. EasyEDA's `create()` is **lenient** — it can return no primitive on a
bad layer/coords without throwing, so each action verifies a primitive came back and
fails honestly otherwise. **PCB autosave is on** (debounced) — still **save explicitly**
at checkpoints. There is **no one-call autorouter** on this build
(`pcb_Document.autoRouting` is undefined — see `docs/ecosystem-survey.md` §6/§7); route
segment-by-segment, or use the file-exchange autoroute flow. 布线方式见
[`design-flow.md`](./design-flow.md) P7：稀疏短线可逐段或 `route-short`，稠密板使用题目允许的
EasyEDA 原生自动布线或已配置的外部路由器，完成后都按网回读并运行 DRC。

- `pcb.line.create` — a copper **track** (导线): line segment on a copper layer
  (`TOP=1`, `BOTTOM=2`; **inner-copper ids are higher** — `id 3` is silkscreen, not
  copper, so read real ids from `pcb.layers.list`) between `(startX,startY)` and
  `(endX,endY)` (mil, y-up), `lineWidth` (default 6 mil), optional `net`. Verify with
  `pcb.drc.check`.
- `pcb.via.create` — a **via** (过孔) at `(x,y)` with `holeDiameter` (drill, default 12
  mil) + `diameter` (outer pad, default 24 mil), optional `net`.
- `pcb.line.list` / `pcb.via.list` — read what's routed (filter by net/layer) before
  rip-up or reroute.
- `easyeda pcb net-path --from C3.1 --through C4.1 --to U2.3 --layer 1 [--net +5V] [--json]`
  — **只读的逐焊盘铜路径举证**。它读取焊盘原始 `shape` / `rotation` / `specialPad`、
  线段/圆弧、层、线宽和过孔，在 Go
  侧构图；`--through` 是一条不重复使用铜图元的路径中按给定顺序出现的**必经焊盘**，
  不是物理过孔。只因各焊盘分别可达、但必须沿分支回头的网络不会冒充顺序通过。输出每一段的
  primitive 路径、层序列、线宽集合/最小值、唯一过孔数、实际中心线长度和转折数，因此可以分别证明
  `C3.1 → C4.1`、`C4.1 → U2.3`，也能证明晶振路径是否确实 TOP-only。
  长度按实际选中路径的 track 子段和 arc 子弧累计；焊盘或分叉落在线/弧中段时只计真实经过的
  部分，不把整条 primitive 或圆弧弦长冒充实际路径长度。
  测量优先使用唯一真实中心线接点构成的完整路径，输出 `measurementPathKind=exact-centerline`；
  若只有铜宽接触可证明连接则回落 `copper-contact-fallback`，歧义仍返回 unknown。
  相邻短于线宽的 45° 段以唯一中心线交点测量，不把铜边缘投影产生的第二个点当作另一接点；
  正长度重叠、多重交点及仅靠平行铜边缘搭接仍不冒充唯一长度。
  `--layer 1` 在受限图中求路，直接排除其它层和物理过孔；不是先任取一条跨层路径再检查
  结果。因此 TOP-only 报告的 `requestedLayer=1`、`layers=[1]`、`viaCount=0`。
  同网名本身不算连通，异层同坐标也不算连通，必须有实际接触的铜或过孔。旋转 RECT（含
  圆角）和 OVAL 按实际几何判定；ELLIPSE、NGON 与 pad-to-pad 接触使用完全包含在真实铜内的
  保守几何，允许漏掉边缘落点但不制造假连通。`POLYGON`、`specialPad`、缺失/畸形 shape
  不使用 bbox 或中心点猜测：必经焊盘直接返回 unknown/error；其它同网未知焊盘若可能改变
  FAIL 也返回 unknown/error。结果的 `excludedCopper` 固定列出
  pours / filled regions / PLANE layers：第一版**不把铺铜或内电层推断成已验证路径**，所以
  FAIL 只表示“没有被 listed routed copper 证明”，整板开路结论仍由 `pcb drc` 给出。
  圆弧作为连续铜参与路径，但其它图元只在圆弧回读端点处与它建立接触；`arcsAvailable`
  缺失/为假时不把空数组当成“没有圆弧”。圆弧中段分叉也会明确列为限制。对 ordered proof，
  工具按实际线宽检查同层铜：平行近邻、正长度叠线、内部交叉、
  track↔arc、arc↔arc 或 via↔via 的非规范重叠都返回 unknown/error；一个普通 endpoint 或
  T junction 仍可使用。圆弧以有误差上界的中心线离散参与检查，无法在预算内可靠规范化时
  同样返回 unknown，避免借不同 primitive ID 在同一物理铜上回走。直接 via-on-pad 重叠不算连接（平台实测需有 track/arc stub），避免制造假
  连通。该命令适合每组关键网布完后的局部核查；保存重开后的最终批次仍应重跑。
- `pcb check` 的 `dangling-end` 也复用 pad shape/rotation：同网 track 端部或其铜宽实际接触
  pad 铜才算 anchor。旧 connector 只有 `width/height` 时使用保守 ellipse/cardinal 或内切圆，
  不用完整 AABB；JSON/human report 的 `limitations` 会说明 legacy/unknown pad 数量。这样
  AMS1117 的大 TAB/窄脚端部落铜不会被固定 30mil 半径误报，旋转焊盘的 AABB 空角也不会假通过。
- `pcb.route.rip_up` — **reliable rip-up**: delete tracks+arcs+vias, `--net` to scope
  (string or list) or omit for ALL. **Copper layers only** — never deletes the board
  outline, silkscreen/assembly/mechanical artwork, or **locked** primitives. The
  iteration primitive: `rip_up → re-route`. (Reports `{requested, ok}` per type, since
  `delete()` is a batch boolean.)
- `easyeda pcb clear` (`pcb.page.clear`) — **一键整版复位**,`sch clear` 的 PCB 对称版。
  一次删掉所有**板级内容** primitive:器件 + 布线(轨/弧/过孔)+ 铺铜/填充(pour/fill)+
  keep-out/规则区域 + 自由丝印(**丝印层 3/4** 的字符串 + 线/弧图形,不碰铜层/文档层的自由文字或
  机械/装配线弧)。`pcb delete`(`pcb.component.delete`)**只删器件**,
  布线/铺铜/区域/丝印会静默残留(`components.list` 看着空了、铜其实还在)——要真正清板重来
  用这个。**默认保留锁定图元 + 板框(layer 11)**(板框是布局前提,和 `sch clear` 保留图框对称)。
  收窄:`--only components,routing,copper,regions,silk`(逗号子集,省略 = 全部);`--no-preserve-outline`
  连板框一起删;`--include-locked` 连锁定图元一起删(危险)。**无 undo**,确认门控。
  **默认自带 verify 复合流程(#121)**:清 → save → `doc reload` → 二遍清 → 最终 dry-run 计数——
  部分图元只在 save/reload 时被引擎物化,单次 handler 调用内任何枚举(含 #112 的循环)都看不到
  (R2 实测 reload 后冒出 3 条轨);返回 `{pass1, pass2, remainingAfterVerify, verified}`,
  `remainingAfterVerify` 非零 = 锁定/保留件或更深的引擎问题,绝不假报干净。`--no-verify` 回到
  单遍(快,但你要自己 reload 后 `--dry-run` 复查)。
  ⚠️ **破坏性**:生产流程必须**先 `--dry-run` 报告删除计数、等用户确认**,再执行。
  生成→检测→清板→重试闭环用这个。
- `easyeda pcb via-delete --ids …` / `pcb track-delete --ids …` (`pcb.route.delete`) —
  **surgical delete by primitiveId**: one bad via no longer costs re-routing the whole
  net (rip-up is net-scoped). Ids come from `pcb via-list` / `pcb track-list` / `pcb drc
  --json` `objs`; **pull them fresh — ids churn after edits**. `--ids` takes **CSV
  (`id1,id2`) or a JSON array (`'["id1","id2"]'`) — both work**; all delete-by-id
  commands (`pcb delete` / `pour-delete` / `region delete` / `fill delete` /
  `track-delete` / `via-delete`) now accept both formats (issue #109), so `pcb drc
  --json` `objs` arrays paste straight in. Each subcommand guards its
  kind (pasting track ids into `via-delete` errors out); locked primitives are skipped,
  stale ids reported as `notFound`. The result's `removed[]` echoes each primitive's full
  before-state (net/layer/geometry) so the audit log can recreate it. **Embedded-primitive
  pre-check + readback (#120, live-verified)**: a footprint-embedded via's id is its
  parent component's primitiveId + a suffix (`ba45…f3` + `e184`); deleting one lies
  TWICE — the SDK returns true AND an immediate getAll shows it gone, but the next
  save/reload re-materializes it from the footprint. The handler refuses these UPFRONT
  (`notDeletable[]` with the parent component + `ok:false`; use `pcb via-bond` to net
  them, or delete the whole component) and additionally readback-verifies the rest
  (`removed`/`count` only count what actually vanished; unattributable survivors land
  in `notDeleted`). ⚠️ **After surgical
  edits (delete/via-hop/fill changes), a burst of same-net (usually GND) Connection
  Errors in DRC is pour-mediated connectivity gone stale, not real breaks — run
  `pcb pour-rebuild` first, then re-judge** (verified live: 11→1 baseline).
- `easyeda pcb via-bond [--component U1] [--dry-run]` — **bond netless footprint-embedded
  vias (EPAD thermal vias) to the net of the pad they sit in** (#118). Scans every net:""
  via whose center sits inside a net-carrying pad's copper rect and assigns that pad's
  net via raw `eda.pcb_PrimitiveVia.modify` (debug-exec backed — works on every deployed
  connector, no re-import). Idempotent, readback-verified (`{planned, assigned, verified}`).
  ⚠️ **Platform limit (live-verified)**: the assignment does NOT survive a doc reload —
  embedded vias re-materialize netless every time; re-run after any reload, before
  DRC / power-planes. `pcb check`'s **netless-via-in-pad** WARN fires whenever a re-bond
  is due, with this command as the fix.
- `easyeda pcb via-hop --net N --from-x … --from-y … --to-x … --to-y …`
  (`pcb.route.via_hop`) — **composite layer hop**: entry stub → via → hop-layer track →
  via → exit stub. **track↔via registers as connected on its own** — no bond fill needed
  (see the truth table below). Vias sit `--stub` (default 20mil) inside the endpoints so
  they stay **off pads** (via-on-pad ≠ connected). `--layer` (default 1=TOP) /
  `--hop-layer` (default 2=BOTTOM), `--width`. `--bond-fill` (default **off**) adds
  optional extra copper over the vias for thermal/current — not for connectivity. Rolls
  back everything it created on mid-sequence failure. Verify with `pcb drc`.
- `pcb.clear_routing` — native `clearRouting` (`@alpha`, may be undefined on this build,
  and does NOT protect unlocked outline) — prefer `pcb.route.rip_up`.

#### 连通性键合真值表 (what actually registers as CONNECTED)

⚠️ **Corrected 2026-07-07 (跟进 pro-api-sdk#31).** The earlier claim — "track↔via does
not register on 4-layer / ex-PLANE boards, a bond fill is the only reliable bridge" —
was **our misdiagnosis** and has been retracted (official confirmed live; we reproduced
the correction on real hardware). What actually happened: DRC Connection Errors are
driven by netlist **ratlines**; a `track(L1)→via→track(L2)→via→track(L1)` bridge between
two same-net pads **satisfies the ratline and clears the error** in every plane state
(clean 4-layer / Inner=PLANE / flipped SIGNAL↔PLANE — all tested). The original
"+5V/U0TXD floating" symptom was **stale pour-mediated GND connectivity**, cured by
`pcb pour-rebuild` (same phenomenon as the ⚠️ note under `via-delete` above) — the fills
that "fixed" it were a red herring; the re-pour/recompute did the work.

| junction | registers? |
|---|---|
| track endpoint on a via (center or inside via copper) | ✅ (needs a fresh ratline recompute) |
| via on a track's body (mid-segment) | ✅ |
| pad ↔ track endpoint at pad center | ✅ |
| net-bound FILL overlapping via + track | ✅ (works, but **not** required) |
| pour (same net) flowing over via | ✅ (but pour reflow has its own traps — see pour section) |
| via ON a pad | ⚠️ offset + stub anyway (a via centered on a pad is redundant, not a bond failure) |

**Via-bridge SOP**: just route the hop with `pcb via-hop` — no bond fill needed. If DRC
shows same-net (usually GND) Connection Errors after routing surgery, that's **stale
pour connectivity**: run `pcb pour-rebuild`, let ratlines recompute, then re-judge — do
**not** paper over it with fills.

### Length constraints — differential pairs / equal-length groups (#176)

**布线前声明,布线后量。** 差分对与等长组是**约束对象**,不是走线:建了它们,EasyEDA 的 DRC 才把
两条网当一对查,`easyeda pcb report` 的 `skew`(|lenP−lenN|)/`spread`(max−min)才有东西可量。
不建 → 那两个数组恒空,报告里的测量能力空转。

```bash
# 差分对(USB / 以太网 / HDMI 这类)
easyeda pcb diff-pair create --name USB0 --positive USB_DP --negative USB_DM
easyeda pcb diff-pair list                       # 约束清单(≠ pcb report 的测量值)
easyeda pcb diff-pair rename --name USB0 --to USB
easyeda pcb diff-pair delete --name USB

# 等长网络组(并行总线 / 地址线),至少 2 条网
easyeda pcb eq-group create --name DDR_ADDR --nets A0,A1,A2
easyeda pcb eq-group add    --name DDR_ADDR --nets A3,A4     # 已是成员的自动跳过
easyeda pcb eq-group delete --name DDR_ADDR
```

**要点**(真机验过):网名**前置校验**,指向板上没有的网 = 零写入拒绝并点名(网名大小写敏感、来自
原理图,用 `easyeda pcb nets` 取准);回执的 `verified` 是连接器**重读比对**出来的,不是平台返回值;
同名同内容重建 = `alreadyExists`(可重放),同名不同内容 = 拒绝并给下一步;差分对**只能改名**,
要换绑定得删了重建。改完可即时读取诊断；最终证据使用 `pcb save → doc reload → list/report`。

**在流程里的位置**:P7 布线之前建好 → `route-critical` / typed track action → `easyeda pcb report` 回读
skew/spread 验收。

### Copper pour (铺铜)

A pour is a net-bound copper region (usually GND/power plane). **The agent passes raw
points** — the connector builds the `IPCB_Polygon` (`pcb_MathPolygon.createPolygon`)
and re-pours; passing raw points to the bare `eda.*` create fails ("无法创建覆铜边框图元").

- `pcb.pour.create` — pour from a closed polygon `points` (`[[x,y],…]`, mil, y-up) on a
  copper layer, bound to a `net` (**required — a netless pour is dead copper; `pcb pour`
  now refuses an empty `--net`, issue #34**). `fill = solid` (default) `| grid | grid45`.
  创建后按精确 ID fresh 回读边界的网、层、填充方式、几何和明确提供的名称、优先级、线宽。
  `verified:true` 只证明边界参数兑现；`poured` 独立报告重建返回值，实际铜须再读
  `pcb poured-list`，连通与制造质量另做 DRC。`poured:false` 或空铜本身不证明算法故障。
  `rebuildAttempted:false` 也不代表没有实际铜：Web 4.1.60 的 dev.11 定向测试中，
  边界校验失败、连接器未调用重建，当时的库存仍读到铜（带 staleRisk，未对失败铜保存重载，
  不作持久化验收）。清理必须按 fresh parent pour ID
  对账边界与派生铜，保存重载后再核实完整基线，不能只删边界就宣称恢复。
  边界不符或回读未知时返回 `partial:true/verified:false`、保留已创建 ID，CLI/Apply 和
  pour-fit、power-pour、power-planes 失败退出并停止依赖步骤；先按 ID 核对和清理，不重试创建。
  Apply 的 action/run 两入口都保留此失败；通用 verify、显式 retry、continue 或 prompt
  不能将已创建但未验证的边界改判成功或继续写入。先核对现场，再编新的修复计划。
  已发现 Web 宿主把显式 1 mil 边界线宽读回为 0.2 mil 的情况；不得换成宿主值补签请求，
  也不从 create 返回对象的参数回显推断已写入。新包只修正检测与传播，不宣称宿主宽度已修复。
  另一次预声明测试未传线宽，但优先级 2 读回为 1；原因未确定，同样失败退出。
  这两项只现场验证了错值检测，不能称成功创建路径或高级 CLI 已验收。
- `pcb.pour.list` / `pcb.pour.delete` — inspect / remove pours.
- `easyeda pcb poured-list [--net GND]`（typed action `pcb.poured.list`）读取**重建后的实际
  铺铜图元**，按 pour primitiveId 返回每个 fill 的 complex polygon source、线宽、填充标志和
  fill id。宿主这里使用 0.1mil 坐标/线宽，action 已逐命令归一化为 mil：坐标、半径、圆角与
  宽高乘 10，`ARC/CARC` sweep 和矩形 rotation 仍为 degree；孔洞/多轮廓递归保留。
  `geometryKind=filled-complex-polygon` 表示铜岛，`fill:false` 则保留为带 `lineWidth` 的
  `stroked-thermal-spoke-path`，检查器必须按线段膨胀宽度判断连接。只有完整 inventory 成功且真实返回 `[]` 才是 known-empty；对象清单、fill、pour
  boundary、net、layer 或 polygon 任一缺测都返回 unknown/error，或在
  `pcb dump --include-copper` 的 `partial[]` 中记为 unknown，不能把缺字段或读取失败当作空铜。
- `pcb pour-clean --netless` (daemon-side) — remove pours bound to **no net** (net:"" dead
  copper that `pour-fit --replace` can't clear — it only matches same-net pours). `--dry-run`
  lists them first. Detected by `pcb check` (netless-pour rule).
- `pcb.pour.rebuild` — re-pour all (or by net) after moving components/routing so the
  copper reflows around new obstacles.
- `pcb pour-fit` (daemon-side) — **auto-size a pour to the board**: reads the outline
  and insets its bbox by `--inset` (mil, default 20) so copper keeps edge clearance
  (fixes Board-Outline-to-Copper), then pours `--net`/`--layer`. `--replace` (default)
  clears the net's existing pours first so they don't stack. v1 pours a RECTANGLE within
  the bbox; for an odd outline draw a custom polygon with `pcb pour`. `--dry-run` previews.
- `pcb via-stitch` (daemon-side) — fill a `--rect "x0,y0,x1,y1"` with a `--pitch`-spaced
  grid of `--net` vias: **thermal vias** under a power-IC center pad (tie it to the GND
  plane) or **GND stitching** between top & bottom pours. Run `pcb pour-rebuild` after so
  the planes reflow onto the new vias. `--margin` insets from the rect edges. `--dry-run`.
- `pcb via-fence` (daemon-side) — place vias only on the perimeter of a derived rectangle.
  Use it for a crystal/RF guard boundary where the center must remain free of copper and vias;
  do not substitute `via-stitch`, whose full grid would populate the protected interior.
  `--pitch` is the maximum edge spacing; corners are included once, short final gaps are
  redistributed along each edge, and `--margin` expands outward from the protected rectangle.
  Hole/diameter default to the live rule。生成器必须用真实板框、焊盘、异网铜和已有过孔避障；
  非有限参数、外径不大于孔径、点数超限或无法保持围栏要求时拒绝执行，不能静默漏孔。
  Dry-run first, then read back the GND vias and run `pcb pour-rebuild` + official DRC.

### 带铜模块的离线组装、定位与验收

`pcb layout-plan` 的 schemaVersion 2 把器件身份、局部铜、外部端口、regions、pours 和 vias
放入同一模块契约。局部坐标先生成完整模块，再对候选做旋转/平移；器件位号和 footprint
身份保持不变。连接固定 owner 的外部引线、GND 接线和受影响包络在最终位置重新求解，不能把
局部刚体变换直接套到 owner 端点。
schemaVersion 3 在此基础上增加 `routing` 与 `reflow`，用共同逃线和移动组验证候选；旧输入仍
可生成旧范围候选，但不会自动取得新增的逃线结论。

1. `easyeda pcb dump --include-copper --out before.json` 串行采集器件、精确焊盘、tracks/arcs、
   vias、pour 边界、实际 poured fills、regions 和静态 fills。每类数据都保留 available/unknown；
   原始文件 SHA256 用于溯源，执行保护使用去掉时间戳后的语义哈希。
2. `easyeda pcb layout-plan ...` 输出最多三个候选、局部 SVG、板内 SVG、typed apply 和事实测量。
   候选先满足拓扑、净距、层和过孔约束，再比较长度、转折和占地；不输出综合分数。
3. `easyeda apply candidate.apply.json --dry-run` 通过后才能写入。journal 记录每步返回的实际
   primitiveId；部分成功、超时或 stale 状态先回读，不直接重放。
4. 保存、重载、重建铺铜并 fresh dump 后运行
   `easyeda pcb module-check --candidate candidate.json --before before.json --after after.json
   --journal candidate.apply.json.journal.jsonl --out check.json`。缺 journal、未知铜几何、旧语义
   哈希、漏对象、非目标对象变化或实际铺铜无法读取均为 incomplete/fail。

schema-v2 bundle 用 `affectedBaselinePours[]` 显式记录可能被本模块 `pour-rebuild` 局部重算的
既有 pour boundary primitiveId、重建前 materialized primitiveId、net、layer 和参数化
`impactEnvelope`。该清单由 planner 根据 fresh before 中的同网同层实际铜，与器件移动前后
包络、routes、regions、pours 和 via 环相交关系计算；不能人工泛化成“所有 GND 铺铜都可变”。
editable boundary 及其 name/填充方式/priority/线宽/锁定等语义仍必须保持。声明对象只允许
`impactEnvelope` 内的材料化铜重算，包络外的 polygon、孔洞和 ARC 必须等价；未声明对象在全域
严格保持。缺 inventory、边界关联或几何时 fail-closed。

晶振 `crystal-guard` 起源于 schemaVersion 2；当前现场要求使用 schemaVersion 3 与联合逃线：X1、负载电容、两条 OSC 铜、顶层 GND
护环、TOP/BOTTOM no-pours regions、外围 GND 过孔和显式 GND 导线一起计算。本板晶振区禁止创建局部或环形铺铜；`groundImplementation=tracks-vias` 时 bundle 的 `pours`、`unreservedPours`、`affectedBaselinePours` 必须为空，apply 也不得含 `pcb.pour.create`/`pcb.pour.rebuild`。双层 no-pours、护环和过孔围栏来自题目与用户复核，不推广成所有晶振的固定模板。no-pours 只阻止
自动铺铜；静态 fills、tracks 和其它显式铜必须另查。GND 同名不证明回流，验收需证明晶振
GND 焊盘、护环、分离接地点、过孔及声明的 GND anchor 通过真实铜连通。敏感包络必须联合
X1/负载电容实测 bbox 与最终两条 `signal-main` 的实际中心线路径；每条路径按“线宽一半 + live
铜净距”扩成 stroke bbox，再叠加 `keepoutMarginMil`，不能只框三个器件。护环在 owner 方向为
两条信号保留合并入口，不能在入口间留下不接地的护环孤段。`replacePrimitiveIds` 默认严格等于
fresh baseline 中两条 OSC 网的全部 track/arc primitiveId；子集、额外 ID、重复 ID 或缺失 arc
均 fail-closed，删除时由 `pcb.route.delete` 按 fresh ID 分类，不能套 track-only kind guard。
护环不能把计划中的 T junction 留在一条长 primitive 的内部。EasyEDA 在后创建的同网支路
端点落到既有 track 中段时，会自动拆分该 track 并更换其 primitiveId；这样 apply 较早捕获的
PID 在同一批次内就会失效。planner 必须在每个同层 GND spoke、ground-entry 等连接点预先给
`role=guard` 路径增加顶点，apply 为顶点间每段分别创建并捕获 primitive；支路只能落在这些
端点。支路从自身线段中部穿越护环或与护环共线重叠时 fail-closed。修改任何保护路径后须重新
生成 candidate、apply 和 journal，旧 capture 不能复用。
`module-check` 还会
逐个证明每段 `role=guard` 的实际 track 经列出的 GND 铜连到声明 ground anchor，并
逐个核对每个声明的 fence/ground-anchor GND via。`groundImplementation=tracks-vias` 时，
TOP 与 BOTTOM 必须分别由 fresh 显式 GND track 接触其环形铜并经实际路径连到 ground-anchor；
只有候选明确声明铺铜时才检查 materialized GND 铜岛。只证明两条 ground-entry、任意一个 via
或同名 GND 不能通过。OSC 两网按 `电容 → X1 → MCU` 做 ordered path proof，fresh pad 身份、坐标、
shape/specialPad、实际长度/转折及 TOP/0-via 一起核对；journal 的每个 capture PID 必须一一对应
before 不存在、after 唯一且与候选网/层/几何相同的对象。region/pour/poured 比较规范化闭合、
方向、共线重分段、holes 和 ARC，未知曲线或缺几何不退化为 bbox 判断。

晶振定位搜索可选 `search.crystalOffsets`（`maxXMil`、`maxAwayMil`、`stepMil`，单位 mil；
当前 `offline-verified`，尚未现场验收）：以 owner 信号焊盘中点为零横移，向远离 owner 的方向增加距离；
显式 `search.offsetsMil` 同样相对此基准，y 为板坐标（bottom 侧只允许 y≤0）。
有界网格结合实测障碍边界和对应信号焊盘对齐事件产生候选，每个位置重新求解外线及完整保护结构。
搜索参数不降低 live 净距、不移动非成员；超过搜索数量上限报错，零合法候选只证明此有限搜索集失败，
不代表整个布局问题无解。先保留更小离开距离、再更小横移的合法候选，无综合分数；通过仍需现场两轮验收。

### Keep-out / rule regions (禁止区域)

A region (`eda.pcb_PrimitiveRegion`) is a polygon carrying **rule types** that keep
things OUT of an area — antenna clearance, board-edge inset, mechanical exclusion.
It is **NOT net-bound copper** (that's a pour) — `create` takes no net. EasyEDA's own
DRC + copper pour respect it (a pour avoids a `no-pours` region). Same raw-points
convention as pour (connector builds the polygon).

`pcb region create --name` 用于 `--rule follow-rule` 的 DRC 规则区域；
`no-pours` 等禁布区不保存名称，禁止给它们传名称后把输入回显当成保存成功。
创建后核对 `verified`、真实层/规则/几何/名称；`partial:true` 时先回读已创建 ID。
Web 4.1.60 实测拒绝 `no-pours` 与 `follow-rule` 混合组合，不把两者合并用于具名禁布区。

- `pcb region create` (`pcb.region.create`) — specify the area **three ways** (pick one):
  `--points '[[x,y],…]'` (explicit polygon), `--rect x0,y0,x1,y1` (rectangular
  shorthand), or **`--ref <designator>`** (the placed component's bbox — e.g. the
  antenna module). `--margin <mil>` expands the `--rect`/`--ref` box outward (antenna
  clearance). `--rule` (repeatable, name or enum number): `no-components(2)` /
  `no-wires(5)` / `no-fills(6)` / `no-pours(7)` / `no-inner-electrical(8)` /
  `follow-rule(9)`. **Default** (no `--rule`) is a hard keep-out
  `[no-components, no-wires, no-pours]` — the antenna / board-edge case. `--locked`
  pins it. Verify with `pcb region list` + `pcb drc`.
  E.g. antenna keep-out under U1: `pcb region create --ref U1 --margin 40 --rule no-pours`.
- `pcb region list` / `pcb region delete` — inspect / remove (note `pcb delete`
  removes components, NOT regions — use `region delete`). `--ids` takes CSV or a
  JSON array.

> **历史结论更正（2026-09-25）：** #18 的“区域名称永远不可回读”仅来自禁布区，不能推广到
> `follow-rule`；后者名称可真实回读且持久化。DSN 中无名称禁布区仍使用 `region_keepout_N`。
> 独立的 `pcb fill` 模式限制未在本轮复测：旧现场只读到 `solid`，不能用区域修复补签 fill 模式。

> **ESP32-S3-WROOM-1 ships with NO antenna keep-out** — you must create it (test-case
> P1). **`getDsnFile` drops regions**, but `pcb export-dsn` now **re-injects** them as
> Specctra `(keepout (polygon …))` by default (reports `keepouts=N`; `--raw` to skip),
> so external Freerouting no longer routes under the antenna. Transform is a verified
> pure translation (1:1 mil, no flip).

### Net-bound filled region (填充区域 / 异形大块铜)

`eda.pcb_PrimitiveFill` — a **STATIC filled polygon bound to a net** (a 3V3/RF-ground
patch, thermal copper, an odd-shaped plane). Three net-copper primitives, don't confuse:
**fill** (static, no reflow), **pour** (`覆铜`, reflows around obstacles), **region**
(keep-out, no net). Same raw-points convention.

- `pcb fill create` (`pcb.fill.create`) — area via `--points` | `--rect x0,y0,x1,y1` |
  `--at x,y --size w,h` | `--ref <designator>` (+ `--margin`), on a `--layer`, bound to
  `--net`. `--fill-mode solid` (default) `| mesh | inner`. `--locked`. Verify with
  `pcb fill list`. ⚠️ **`--rect` 的四个数是两个对角点 `x0,y0,x1,y1`,不是 `x,y,宽,高`**
  (issue #109 实踩:按 x,y,w,h 传参生成盖住 USB-C 区的巨型 fill,原生 DRC 爆 ~50 条)——
  想按「角点 + 宽高」表达就用 **`--at x,y --size w,h`**(与 `--rect` 互斥,`--size` 从
  `--at` 向 +x/+y 延伸)。**防呆护栏**:fill bbox 面积 > 板框 bbox 的 **25%**(板框可读时;
  读不到板框则 > 4,000,000 mil² ≈ 50×50mm)直接拒绝,报错教两角点语义;确属故意的超大 fill
  加 `--force-large` 放行。
- `pcb fill list` / `pcb fill delete` — inspect / remove (filter list by `--layer`/`--net`);
  `delete --ids` takes CSV or a JSON array.

**Board cutout / slot (挖槽) — `pcb slot`.** A fill on the **MULTI layer (12)** IS a
board cutout (per the eda API: *"填充所属层为 MULTI 时代表挖槽区域"*; manufacturing
emits it as a `BoardCutout`). `pcb slot --rect … | --ref ANT1 --margin 20` mills a
hole — antenna isolation / mechanical opening. No net. It's a `pcb_PrimitiveFill` on
layer 12, so list/delete via `pcb fill list --layer 12` / `pcb fill delete`.

**M3 安装孔 — `pcb mount-holes`** (issue #102). Places corner mounting holes
**automatically and collision-checked** — never hand-place M3 holes at guessed
coordinates (#102: a blind hole landed on C1). Reads the real board outline
(errors without one — run `pcb outline-fit` first), computes each corner center
at `--inset` (default 197mil ≈ 5mm) from both edges, and mills a near-circular
MULTI-layer cutout (`--dia` default 126mil = M3 Ø3.2mm) — the same primitive as
`pcb slot`, so `pcb place-constrained` avoids it as a **Tier-1 obstacle** and
`pcb check` keeps copper off the milled edge. Each corner is checked against
every component's rendered bbox with the fastener keep-out radius
`max(hole R+40mil, M3 washer R118mil)` (conventions §2.3): a conflicting corner
is **warned + skipped**, never force-placed (`--clearance` overrides the radius
for a smaller fastener head you knowingly accept); a corner that already has a
cutout reports `exists` (idempotent rerun). `--corners tl,tr,bl,br` picks a
subset; `--dry-run` prints the per-corner plan. Save after placing; delete via
`pcb fill list --layer 12` + `pcb fill delete`.

  easyeda pcb mount-holes --dry-run          # plan only
  easyeda pcb mount-holes                    # 4 corners, M3 defaults
  easyeda pcb mount-holes --corners tl,tr --inset 250
> **Snapshot can't confirm it visually** — `pcb snapshot` (`getCurrentRenderedAreaImage`)
> does NOT auto-redraw after API edits and does not render filled copper/cutouts, so a
> fresh snapshot shows a **stale frame**. Verify slots/fills/pours by **data** (`pcb fill
> list`, DRC, manufacture export), not screenshot — the snapshot is for component layout only.
>
> **Stale-frame detection (issue #31).** `pcb snapshot` carries the anti-stale machinery (the schematic-side snapshot was removed — sch renders via `sch export-image`):
> the result exposes a frame `sha256`, and `--previous-sha256 <sha>` lets the connector
> detect a byte-identical (stale) frame, force a redraw (ratline recompute + zoom-to-all)
> and retry once, reporting `stale:true` if it still cannot refresh. **Reliable recording
> workflow** for user-facing videos/tutorials where the visual artifact is required:
> 1. `easyeda view region --left … --right … --top … --bottom …`（或 `easyeda view fit`）框住目标视口。
> 2. `easyeda pcb snapshot --fit=false --previous-sha256 <上一次的 sha256>`。
> 3. 若结果 `stale:true`，说明画布未刷新 — 告警/失败，不要用该帧。
> 4. 用 `pcb list` / `pcb drc` / `pcb check` / `pcb layout-lint` 做**权威**正确性校验（截图只作视觉终检）。
>
> **底面视觉 QA（issue #40）** — 不再需要人工点 UI 切层。`easyeda pcb view-side --side bottom`
> 会选底铜为当前层并聚焦底面铜+丝印层，随后 `easyeda pcb snapshot`（thread `--previous-sha256`
> 防陈帧）即反映底面（底丝印/底铜/背面装配标记）。更细的显隐用 `easyeda pcb layer-visibility
> --preset bottom-only|top-only|copper-only|silk-only` 或 `--show/--hide`。切当前编辑层用
> `easyeda pcb layer-set --layer bottom|Inner1|<id>`。**注意**：EasyEDA 无原生画布翻面/镜像视图
> API，`view-side` 是「层聚焦」近似（切当前层 + 只显示该面层），不是物理翻板；丝印极性仍以
> `pcb check` 的 silkscreen-flipped 规则做数据级判定为准——该规则判**位号与元件不同层**、
> **`reverse`（任一层）**、以及**顶层 `mirror`**。底层 `mirror` 平台语义未确证（仓库 fixture
> 板多为 `mirror=false`，而 `pcb silk-align` 写 `mirror=true`），两种极性都会误报，故不判。

> **Routing boundary (load-bearing — see `docs/ecosystem-survey.md` §7):** EasyEDA's
> interactive 布线 menu (single/multi/differential **routing**, stretch, optimize,
> length-tuning/serpentine, fanout, remove-loops) has **NO `eda.*` API** — the agent
> cannot do smart/avoiding/push-and-shove routing. Programmatic routing is limited to:
> create tracks/vias/pours by coordinate (above), rip-up, the `@alpha` `autoRouting`
> (undefined on 3.2.148), or read-primitives → external engine → write (the official
> kirouting pattern). So route segment-by-segment, pour planes, and leave smart routing
> to the human/UI. **Shipped: copper pour + rip-up (R1/R2).** **net-class WIDTHS
> are shipped daemon-side** (R3-width): `pcb net-classes` prints the role→spec-width
> ladder, `route-short` sizes each net by role (signal / power-branch / power-trunk /
> high-current — `pcb_netclass.go`), and `pcb check` **width-under-spec** reports
> under-sized power tracks. Still pending: writing those roles into EasyEDA's NATIVE
> net-class rules (`createNetClass`/`overwriteNetRules`, @beta — so the native DRC
> enforces per-class width) + diff-pair/equal-length **definitions** (read side is
> in `pcb.report`).

### 晶振障碍感知保护搜索（开发中）

`crystalGuard.protectionSearch` 显式声明 `stepMil` 和 `maxDetourMil`，启用局部成员包络
周围的护环/孔围栏搜索。双层禁铺区仍覆盖成员与完整信号 stroke；护环包围目标为三个
局部成员，MCU 引线从必要入口穿出，不把 MCU 邻脚包进刚性矩形护环。轮廓、接地支线及
孔位均按新鲜焊盘、铜及板边重新验证。孔位可沿轮廓移动，但必须报告最大实际节距，
不能静默漏孔；无法满足最大节距、必需接地点或真实净距时拒绝整候选。
本能力尚未现场验证，失败样例不得更新为 live-verified。

`maxDetourMil` 是严格边界，不会自动扩大；`stepMil` 定义8方向搜索分辨率。两条 OSC
依 MCU 信号焊盘间距保留紧凑入口，并尝试两网求解顺序，保持 TOP、0 via。RECT、
圆角 RECT、OVAL 和圆焊盘使用真实旋转几何；一般椭圆、NGON 的精确净距尚为
unsupported，不能用 bbox 证明通过。

双层 no-pours 使用成员包络与分段信号 stroke 保守矩形的并集外轮廓，内部孔洞仍禁铺，
不连通或边界分叉时拒绝。护环入口依据真实信号 stroke 切开相交侧，每段护环及四个局部
GND 焊盘均需有实际铜路径到 anchor。`bundle.fenceContour` 和 `fencePitchMil` 记录闭合
孔围栏及沿周长的最大节距，module-check 核对不自交、孔位在轮廓上及末首间隔。围栏
不得进入成员敏感包络；MCU 外部信号禁铺区域可伸出局部围栏，孔仍不得在禁铺区内。
无法放孔时报告最长无合法孔区间的两端坐标与长度，不静默漏孔。

`protectionSearch.guardOffsetsMil` 可显式枚举护环的初始膨胀量，调试时用 `[0]` 固定一次
轮廓尝试，不必缩减实际路径的 `maxDetourMil`。被障碍挡住的初始角点先迁移到合法种子，
再求闭合 `guardContour`、核对不自交与包围成员，最后从最终轮廓切开信号入口。
路径 A* 状态包含到达方向，只允许45°方向变化；绕行边界也约束对角偏移，不自动乘以 √2。
信号路径随后执行有净距验证的 45° visibility/string-pull：每段候选重新调用精确障碍检查，
并保持原绕行边界和最多 45° 的转向；优先最少真实转折，其次最短中心线长度，不使用综合分数。
输出主线和电容支路各自的实际路径；不能把 A* 找到第一条合法但转折繁多的路线直接当作最优。

孔围栏轮廓是非铜参考线，不能按24mil铜线要求它的每一毫米都可放孔。轮廓本身只硬性
避让成员敏感区域和板边；实际孔逐点检查真实焊盘与异网铜。若筛孔形成超节距区间，使用
合法孔位组成的有界路径重算该区间，每个候选孔合法、相邻参考线段不穿成员敏感区域且
不超过最大节距。禁铺区只约束自动铺铜；外围孔中心在区外时，孔盘跨过禁铺边界本身
不构成铜净距违规，但实际铺铜仍必须证实每个地孔与地锚点连通。

### 旧模块保护对象替换（offline-verified）

详见 [旧模块对象归属恢复](module-owned-objects.md)。

`modules[].existingObjects` 显式列出旧模块对象，每项为 `kind`（track/arc/via/region/pour）、
`primitiveId`、`expected`（新鲜快照中的完整原始对象）以及 `source`（归属的原候选/journal 依据）。
这不是按网名删除；GND 同网不能证明归属。输入必须先和原候选及 journal 对账，再由规划器逐对象
比较身份与完整几何，任一缺测、变更或重复都失败。仅声明 pour 的材料化子对象可一并撤出规划障碍，
它们不整体平移，执行后重新铺铜。未知对象保持固定；新候选和独立 requirements 的声明必须一致。
替换动作出现在移动与创建之前，写后验证所有声明旧对象已消失、未声明对象保持。重复执行或部分
成功后必须新鲜回读重新生成声明，不能重放旧 apply。

已有 EP 地过孔复用（offline-verified）：`crystalGuard.reuseGroundAnchorViaIds` 只能指向已存在且与声明
MCU GND anchor 精确相接、尺寸/位置符合模块设计的过孔。候选 via 的 `existingPrimitiveId`
表示验证保留，apply 不创建第二个孔；before/after 都必须存在同一真实对象。普通器件不因此获得
via-in-pad 许可。`routing.demands[].existingViasOnly` 还要求列出的 PID、网络、位置及
`existingViaInPadAnchors` 焊盘归属与 fresh 快照一致；这些孔只作为既有 witness，不计入
候选新增过孔，不能仅靠同名网络代替实际入口铜路径。
