# EasyEDA PCB — 摆放 / 同步 / 板框

> 从 [`pcb.md`](pcb.md) 拆出(RFC #178):原理图→PCB 同步、器件 CRUD、布局调整(align/distribute/
grid-snap/分档摆放)、板框、自动布局。入口与 guardrails 仍在 `pcb.md` —— **先读它**,再按需读本文件。
布局的**规范判据**(为什么这么摆)在 [`pcb-layout-conventions.md`](pcb-layout-conventions.md),本文件是**命令**。

---

### Schematic → PCB sync + component CRUD

- `pcb.import_changes` — **sync components/netlist from the schematic** (从原理图导入变更). How parts first arrive on the board: ensures a Board links SCH+PCB, then `importChanges`, then recomputes ratlines. 只在当前任务授权的目标 Board 上执行，完成后回读并保存。Returns `imported:false` (with a reason) for a floating/unlinked PCB.
  > **✅ #20 误诊已订正(#124 破案,2026-07-17)**:`importChanges` 从来不是 no-op——它弹「确认导入信息」对话框等人点「应用修改」,API 返回 true 只代表**对话框弹出**(某些状态下 promise 甚至永不 resolve)。headless 没人点 = 看似 no-op。handler 现在**自动点「应用修改」**（英文界面识别 `Confirm Importing changes information` 对话框的 `Apply Changes`；`confirm:false` 保留人工审查）。其他语言未验证；若 `confirm:"no-dialog"` 且回读未发生预期变化，停止写入并记录对话框证据，先补 typed handler，禁止手动点击兜底并报 `{confirm, componentsBefore/After}` 计数差为真值;**clear→reimport 往返已打通**(真机:清空板 → import → 20 件全自动落板)。增量添加同样可行。`pcb add-component`(below)仍是逐件精确控制(--nets 赋网 + 内嵌 via 键合)的互补路径。导入会改变实例和飞线，只为同步真实原理图变更运行，并重新读取布局对象。
  > **✅ 「导入后位号全变 U?」误诊已订正(2026-08-09 真机二分破案)**:导入本身位号一直是对的(平台还在首次导入时给两侧铸造 `uniqueId` gge*);毁位号的是随后自动跑的 **attrs 同步**——器件库记录的 otherProperty 自带 `Designator: "C?"` 占位键,merge 灌进实例后平台把它同步成图元位号,一板位号当场全灭(166/166 事故真因;安静板单跑一次 sync-attrs 即 100% 复现)。连接器已根治(merge 剔除平台投影键 `Designator`/`Name`/`Manufacturer*`/`Supplier*`/`Add into BOM` 等——它们存在顶层图元状态,写 otherProperty 要么毁状态要么被静默丢弃);import-changes 现在 **attrs 在前、位号回填殿后**,且只在 `confirm=applied`(真落地)时才跑后续同步。被老版本毁过的板用 `easyeda pcb sync-designators` 修(见下)。
- `pcb sync-designators` (`pcb.components.list` + `pcb.component.modify` 编排) — **修占位位号**(`U?`/`C?`/`RF?`):按 `uniqueId`(平台首次 sch→PCB 导入时铸造、跨文档同一命名空间;primitiveId 每文档各 mint 一套对不上)从原理图回填。**只动占位符**——PCB 手工设过的真实位号是用户的决定,绝不覆盖;每笔写入**回读验证**(平台 modify 有静默 no-op 前科),修完立落 `pcb.save` 检查点;原理图侧也是占位符的件归类为「先标注原理图」而非 unmatched。`--dry-run` 先看会改多少;`--json` 出报告(Failed>0 时两种模式都非零退出,可 gate)。`import-changes` 之后自动殿后跑(`--no-sync-designators` 可关)。API 手放且从未导入过的原理图器件 `uniqueId` 为空——先跑一次 import-changes 让平台铸造。
- `pcb add-component` (`pcb.add_component`) — **the working way to add a part to an existing board.** Places the footprint (`--library` + `--uuid`, a device) at `--x/--y` on `--layer`, links it to its schematic twin (`--designator` + `--unique-id`), assigns each pad's net from `--nets` (a JSON `padNumber→net` map), and recomputes ratlines — directly wiring net→pad, which is what `importChanges` would normally do. **Get `--nets` and `--unique-id` from `sch read`** (the netlist is only readable while the schematic is the active doc, so you pass them in). Workflow: ① place + wire the part in the schematic → ② `sch read` (note its pin nets + `uniqueId`) → ③ `pcb add-component … --designator U2 --unique-id gge9 --nets '{"5":"3V3","3":"GND"}'`. Verify with `pcb list --include-pads` + `pcb drc`. **Embedded-via bonding (#118)**: footprints that EMBED vias (QFN EPAD thermal vias) used to land with `net:""` — the EPAD never bonded to the GND plane and DRC fired one "SMD Pad to Via" per via, with no repair path (embedded vias can't be deleted, #120). The handler now assigns every netless via inside a just-assigned pad's copper rect that pad's net via `pcb_PrimitiveVia.modify` (@beta) and readback-verifies it — the result's `embeddedVias {assigned, verified, failed}` reports the outcome. ⚠️ **The assignment does NOT survive a doc reload** (live-verified: the platform re-materializes embedded vias netless every time) — re-run `pcb via-bond` after any reload, before DRC/power-planes; `pcb check`'s **netless-via-in-pad** WARN is the tripwire.
- `pcb.component.modify` (`pcb modify`) — move (x/y), rotate, flip layer (top/bottom), lock, designator/BOM flags. Patch x/y = **anchor**; `pcb modify --center --x <cx> --y <cy>` writes by **bbox center** instead (CLI converts via the live bbox; mutually exclusive with a rotation change in the same call — rotate first, then center).
- `pcb.component.delete` (`pcb delete --ids`) — delete component primitives **by id** (`--ids` CSV or JSON array). 写前读取当前板器件 ID；任何 ID 已过期时返回 `PRECONDITION_REFUSED`，整批不派发删除。先 fresh `pcb list` 再重算 ID。只在当前任务已授权的删除范围内执行并保留删除前快照；没有程序化 undo。⚠️ **只删器件**,布线/铺铜/区域/丝印会残留 —— 要整版清板重来用 **`easyeda pcb clear`**(`pcb.page.clear`,见上「一键整版复位」)。

### 模块候选布局：AI 定关系，算法算坐标

对有明确功能归属的模块，优先使用离线候选入口，避免整板启发式把同网外围重新分给错误引脚：

已经完成组内相对布局时，先把核心及专属外围固化为完整成员和 pin owner 表，使用 `rigid`
或公共求解器的完整移动组。每次调整保持组内相对向量/角度、装配面和网络；外部连接另行
重算。不要再次调用逐脚外围排布改变已接受的组内关系。组合移动的起点、参数、独立要求与
回读见[五成员开发验证样例](examples/pcb-group-move/README.md)。按场景分别记录实现、自动
测试、离线复现和现场保存重载证据；只通过前两者不能称完整电路块现场移动已验证。

```bash
easyeda pcb dump --project ceshi --out board.json
easyeda pcb layout-plan \
  --from layout.json --board board.json --module rgb-led \
  --candidates 3 --out candidates/
easyeda apply candidates/candidate-01.apply.json
```

跨模块通道竞争使用二层协同入口：

```bash
easyeda pcb dump --include-copper --out board.json
easyeda pcb layout solve --board board.json --from solve-request.json --out solve-report.json
easyeda pcb layout check --board board.json --from solve-request.json \
  --candidate solve-report.candidate-01.json --out check.json
easyeda pcb layout render --board board.json --from solve-request.json \
  --candidate solve-report.candidate-01.json --out candidate.svg
```

该组命令没有 appConfig/window，不能写编辑器。Layout 只变换声明的完整组及其内部
track/via/region；Router 在每个固定投影上共同检查所有 demand，失败的实际阻挡对象用于优先
让位。布局、共同路由和单次寻路共享总状态预算；耗尽保留已通过独立复验的合法候选，同时
标记搜索未完成。最终按移动组数、总位移、旋转、新过孔、实际线长、转折排序，不合成总分。

布局完成前必须检查块间导线需要的空间。自动让位入口使用
`layout.groups[].translationSearch: {"step": 10, "maxDistance": 80}`（mil），允许在原始位置
附近按网格搜索；`offsets` 仍兼容旧的显式备选位置。先共同试布，记录实际被拒绝路径的
网络、层、对象和位置，再移动完整组并重算所有声明连接。
反馈搜索也探索 `allowedRotationDeltas` 声明的整体转角：原地旋转碰撞时，允许在平移后重新
尝试该朝向。全部成员和内部几何始终从原始 anchor 重建，不累计逐次旋转误差；`fixedAxes`
中的 `rotation` 仍禁止转动，移动半径与总预算保持。共同路由优先在固定布局中回溯路径和
排网顺序，让先前占用通道的网络按新的占用重新寻路；这些尝试也消耗共享预算。真实同层
替代路线的生成仍有限，未找到共同路径不能当全局无解，也不代表一定需要移动模块。
最终候选保留共同路径的铜宽、净距及过孔占用；预留只存在于计算结果，不加入 board 的
既有铜，也不由 layout Apply 写入。
只检查少数 demand 只能证明这些连接；没有完整块间连接需求时，不能称整板布局可布通。
自动让位会拒绝缺少完整外部连接图的输入；已有焊盘与铜的接触不能因移动而断开，外部旧铜
尚无显式替换模型时要保留该失败，不能以新路径预留替代旧铜重建。状态/网格资源耗尽不归因
于器件阻挡。该能力已通过合成案例离线验证，初始布局前的全局通道容量估计仍为 planned。
布局检查不得因原板已有同一对象对的问题，就豁免移动后的重叠或越界。

输入要求 fresh `pcb dump --include-copper` 的精确多边形板框、live 规则、全部铜分类及匹配的
`semanticSha256`。当前固定装配面，支持 TOP/BOTTOM、直线/45°和每网最多两个过孔；四层只在
公共模型表达，旧 dump 的层数不能推断内层用途。输出含基线、每次失败尝试和最终候选的
整板 SVG 与同名 `*.local.svg`；局部范围由声明端点、移动组、实际路径和过孔自动计算，仍与
整板图消费同一候选。solve/check 的预览还显示请求中的机械禁放区；移动原因完整换行显示。
单独 `render` 加 `--from` 可显示相同请求禁放区，并核对板/请求与候选来源；旧的无 `--from`
调用仍兼容，但只显示板中已有约束。SVG 不证明连通。提供 `--apply-project/--apply-doc` 时，solve 为不含内部铜
移动的候选输出绑定工程/文档的 layout-only playbook；执行前还须 fresh dump 对照候选的
`baseSemanticHash`，绑定本身不会自动完成这项哈希检查。playbook 只写器件位置并保存，不写规划路径。移动显式内部铜
的候选会输出 `unsupported` 说明，不能只移动器件。2026-09-23 的 `ceshi/PCB1` 双网测试
已完成“受阻 fresh dump → 自动反馈让位 → 独立 check → layout-only typed Apply → 连续两轮预览/回读”；
它只验证测试通道，规划铜未写入，整板 DRC 尚未通过，不升级为整板布线验收。
无论哪种情况都必须从同一 candidate 投影生成 typed 队列，不能从 SVG 反推。

`layout.json` 使用 mil、`footprint-anchor` 坐标语义，逐模块声明成员、策略、固定轴、允许的
0/90/180/270°角度、装配面、搜索间距和显式 `copperPolicy`。去耦等外围必须写
`member pad → owner pad`；同一电源网有多个物理供电脚时以真实 pad number/primitiveId 归属，
不能按最近的同网焊盘重分。AMS1117 VOUT/TAB 这类等电位物理脚可用 `ownerPads` 表达，但
等价组只用于刚体关系和距离测量，逐脚放置仍需要唯一 owner pad。

三种策略分别用于：

- `edge`：把指定 `edgeMember` 放到板框中心线给定间隙，再按 pad 归属重排内侧 follower；
  混合模式要求 `anchorRef == edgeMember`，其余成员必须是 follower 或明确锁定/三轴固定的 root。
- `pin-satellites`：核心保持，外围按所属焊盘、方位和 bbox 间隙逐个放置。
- `rigid`：模块整体平移/直角旋转，anchor、bbox、pads 和成员角度统一变换。

命令只做本地计算，不访问 EDA，也不替 AI 选择。每个候选输出完整坐标、pad 距离、
`polygon-centerline`/`outline-aabb` 板边测量、分开的 component/keepout gap，以及各最小间隙的
`from`/`to` 对象，避免只看到一个数却不知道是谁限制了空间；同时生成 SVG 和 typed apply。
没有综合分数。AI 用一句具体理由选择；都不合适就改关系、方位、间距或可用区域后重算。
候选带原始 board/layout 文件 SHA256；执行前重新 `pcb dump` 对照目标对象，primitiveId 或
几何缺失时不生成可执行候选。

`copperPolicy: require-board-empty` 会在板上已有走线时拒绝移动，适合已有局部铜的模块；
`ignore` 只表示本轮不以全板铜计数拒绝，并会明确报告“铜几何未证明”。写入前另用 typed
`track-list` 确认目标模块没有已有铜。执行后显式 `pcb save`，有界 `doc reload`，重新 dump
并核对坐标、角度、固定件、板框、模块成员和铜段数。完整可迁移例见
[260919 模块候选布局](examples/260919-at32f415/layout-candidates.md)。

Region/keepout 是附加约束，不是换模型的理由。既有器件已经正确绑定 device、footprint 与
3D model 时，先回读三项身份和 source library 类型。可写 source footprint 可原位增加区域，
保存后再次逐项核对关联和 PCB 实例；EasyEDA system library 必须在打开编辑器/创建几何前
拒绝。系统源不可写时只能使用已经现场证明保持绑定、且不会把 owner 本身判为违规的实例级/
工程级 typed region；该能力不存在就标 `incomplete`，不能复制封装后默认 rebind。
普通 top-level `no-components` region 没有 owner 语义时也不能冒充实例级区域：它会把承载该
区域的器件自身判成 `Device to Prohibited Region`。必须先用 DRC 负例证明 owner 豁免；出现
自违规就 typed 删除、保存重载并对账，不能通过忽略这条 DRC 来宣称完成。

### Layout 观察视图与连续两轮自检

元件属性有时会遮挡本体、焊盘出口和模块间空隙。可以为了观察临时隐藏，但这不是设计修改：

1. 先用 typed 读取当前属性可见性，把对象、字段和旧值保存到本轮 review manifest；
2. 只用 typed **view-state** 动作隐藏所需属性，不删除属性、不改文字/位号，也不把临时状态写成
   新的设计基线；
3. 无论观察图生成成功或失败，都恢复全部旧值，并再次 typed 回读，逐项证明恢复一致；恢复失败时
   Layout 保持 `incomplete`；
4. 当前 action catalog 没有可回读、可恢复的逐元件属性视图动作时标 `unsupported`，保持属性
   可见继续检查。`pcb layer-visibility` 只控制层，不能冒充属性显隐；禁止从属性面板或 GUI 兜底。

参数化布局全部写入后，必须通过 typed capture/export 生成一张包含板框和全部器件的整板集成图。
当前可用入口是 `pcb stage-snapshot --fit-mode board`：它先调用公开
`pcb_Document.zoomToBoardOutline()`，再由 `getCurrentRenderedAreaImage()` 保存
`board-fitted-viewport-png`，同时保存 components/tracks/vias/pours/nets/DRC 数据包。返回和
`stage.json` 必须记录 `fitMode`、`fitApi`、`captureKind` 及 `objectLevelExport:false`。编辑器右键
“复制为 SVG/PNG”使用的对象级整板导出尚未暴露为公开 `eda.*`，不能调用内部 message bus，也
不能把当前视口 PNG 称为该原生导出。`--fit-mode all` 用于需要容纳板外图元的诊断图，`none`
保留当前视口。截图为空、上下文不匹配或 typed 渲染不可用时，记录 `unsupported/incomplete` 并
停止完成声明，不能手工截图补齐。正式复核图必须在属性视图恢复后生成。

两轮自检是连续的“未修正通过”，不组成 workflow/stage 许可：

- **第 1 轮：集成视觉检查。** 对整板图检查空间利用、板边/插拔方向、模块关系、禁区、器件
  聚团或孤立、明显拥挤、丝印/属性遮挡和异常空洞，并用 dump/lint/测量定位具体对象。发现问题
  就修改关系或参数、重新计算和 typed apply，再重新生成整板图；本轮不计通过，连续计数归零。
- **第 2 轮：持久化复核。** 第 1 轮无修正后，严格执行 `pcb save` → `doc reload` → fresh
  `pcb dump` → fresh `pcb stage-snapshot`。以新 dump 核对器件 anchor/角度/层/锁定、板框、region、现存局部
  铜和机械事实，再检查新整板图。若发现并修复任何问题，同样清零并回到第 1 轮；即使修复很小，
  也不能把修复前的第 1 轮算作连续通过。

fresh render 是 reload 后的新 capture 调用和新 artifact，不要求未改设计的 PNG 字节必须变化；
记录每轮 dump/render 路径、SHA256、检查项、findings 和 `fixesApplied`。只有第 1 轮与紧接着的
第 2 轮都没有待修的明显布局/视觉 finding，且 `fixesApplied=false`，才可称整板 Layout 完成并
进入用户确认；纯观察项可以保留，但必须说明为什么不需要修改。

### Layout 完成、局部电源铜与用户确认

模块候选全部执行不等于整板 Layout 已完成。先逐项落实板框、固定件、板边方向、机械/封装
禁区、核心与专属外围、逐脚归属和预留通道；任何题目要求仍为 `incomplete` 时，都只能报告
“候选或局部布局已完成”。随后按上节生成整板图并连续通过两轮无修正自检，再形成给用户看的
布局复核包：

- 当前 board/layout 输入哈希、器件总数以及全部成员的 anchor、角度、层和锁定状态；
- 第 2 轮 fresh 整板预览，以及固定坐标、板内/禁区、重叠、间距和关键模块关系的事实结果；
- 两轮 review manifest；若曾修正，记录计数清零和重新开始的证据；
- 为后续走线保留的出口/通道和仍需用户取舍的项目，不用综合分数替用户作决定；
- 当前版本的未完成项。存在机械违规、缺测或保存重载失败时，不请求把它当成完成态确认。

展示复核包后停止在 Layout，等待用户明确回复“OK”“布局确认”或“可以布线”，也接受用户
描述继续调整。用户提出修改时更新参数、重新生成候选、typed 执行并再次复核。用户也可以
自己调整 EDA；这不是 Agent 使用 GUI 兜底。用户说调整完成并确认后，Agent 必须先重新 dump，
以现场 anchor/角度重建参数基线并重跑布局事实检查，避免用旧候选坐标开始走线。

LDO、DCDC 等电源模块是例外：输入电容到芯片、芯片到输出电容、局部 GND 回流及必要的
EP/地过孔直接决定模块布局，可以在整板确认前作为一个参数化整体完成。局部铜必须逐网回读、
保存，作为候选比较和布局复核包的一部分；以后移动或旋转模块时，器件与这些 track/via 一起
重算，不能只移动器件。已有局部铜而工具还不能整体变换时，候选应拒绝移动。该例外不包含
跨模块电源主干、普通信号、全局铺铜或缝合孔。

没有用户对**当前回读版本**的明确确认，不开始上述例外之外的 track/via/pour 写入，也不运行
会改变整板铜的自动布线命令。确认记录写明基线哈希、时间和用户原话；它是设计评审记录，
不是 `workflow/stage`、评分、版本许可或 CLI 解锁令牌。

### Layout adjustment (deterministic — EasyEDA exposes no align/grid API)

- `easyeda pcb refine` — **打分驱动的布局精修环(#167 #153)**。读 `pcb layout-score` 逐维归因,
  对最弱维下确定性变换,每步后复核,`pcb check` 新增 finding 或综合分下降即**回滚该步**。
  **默认 dry-run,`--apply` 才落笔**;不可动集合取锁定件和本轮参数中明确的固定件，
  `--max-shift` 超限剔除不截断、回读证实才算 restored。当前唯一变换器 grid-snap→tidy
  (`--grid` 默认 5mil);**blocking(重叠/短路/出板框)不归它管**——先
  `place-constrained` 等 typed 合法化动作清掉再精修；能力不足时报告 blocking 并补工具。真机环实测:65.2[blocked]→清 blocking→refine→78.6[good]。
- `pcb.align` — `mode = left | right | top | bottom | centerX | centerY` (y-up: `top` = larger y), aligned to the group extent.
- `pcb.distribute` — even center spacing, `axis = x | y`, extremes fixed.
- `pcb.grid_snap` — round component anchors to `grid` (mil; SMD 25, THT 50).
- `pcb.components.move` — translate a group by relative `dx` / `dy`.
- `pcb.components.arrange` — coarse auto-layout **seed** (priority P6): `mode=cluster` groups by shared local nets then grid-packs each cluster into a tidy non-overlapping block; `mode=grid` packs a flat grid. Skips locked parts.
- `easyeda pcb auto-place` — **module-aware** heuristic placement (daemon-side). Main chips (≥ `--main-pins`, default 8, distinct pins) are anchors that stay put — but a **connector-designated part (J*/CN*/USB*/SIM*/BAT*) never competes for main whatever its pin count (#131)**: a 16-pad USB-C out-pins a small IC, and calling it main made it steal the decoupling caps that belong to the regulator; high- and low-pin connectors alike are skipped with a diag for `place-constrained` to seat. `--anchor U1,U5` FORCES parts into the main set and `--exclude-main <des>` bars them (an excluded high-pin part stays put) — explicit beats every heuristic; every satellite (cap/R/LED) is pulled to the chip edge nearest the pad it connects to (the **nearest same-net pad** — a chip repeats GND/VCC many times), then packed along that edge with no overlap: decoupling caps land by their power pin (3V3/VCC), signal R's by their signal pin, an LED chains beside its series resistor. **v1.1 also re-orients** each 2-pin satellite so its connecting pad faces the chip (rotation 0/90/180/270, packed with the post-rotation bbox); `--no-rotate` keeps the v1 translate-only behavior. **With 2+ main chips**, any that overlap / sit closer than `--multi-gap` (default 150 mil) are spread into a left-to-right row (leftmost stays put) before satellites are placed; `--multi-gap 0` disables it. **Spacing is rule-aware**: `--gap`/`--pitch` default to values derived from the board's live DRC rule (clearance + track width, via `pcb.drc.rules`) instead of a fixed 40/30, so packing never creates sub-clearance corridors. `--dry-run` prints the plan without moving. It is a SEED: use typed `refine`/`place-constrained` and verify with `pcb drc`. Prefer over `arrange` when there is a clear main chip.
- `easyeda pcb outline-fit` — **tighten the board outline to the placed parts** (daemon-side). Reads every component's bbox, adds `--margin` (default 100 mil), and replaces the outline with that rectangle. Fixes low utilization (ceshi 17%→71%); reports util before/after. **Run AFTER `auto-place`, BEFORE pour/route** (changing the outline after copper exists can strand it). `--dry-run` previews.
- `easyeda pcb outline-round` — **rounded-rectangle board outline** (圆角板框, daemon-side). Rounds the current outline centerline bbox (or `--rect x0,y0,x1,y1`, `--margin` to expand) with corner `--radius` (default ≈12% of the shorter side, clamped to half). It writes one locked BOARD_OUTLINE polyline with four native 90° ARC commands and 10mil line width. Run BEFORE pour/route. `--dry-run` prints the native source; `pcb outline-get` reads centerline width/height/radius/lineWidth/locked. The old `--segments` chord approximation is obsolete.
- `easyeda pcb silk-align` — **POSITION-AWARE designator (位号) auto-placement** (v2, designed via a 3-lens workflow). Per part it ranks the 4 sides by **local free space** (corridor clearance to nearest obstacle) + **board position** (edge parts pulled inward, never off-board) + a **crowd-axis bonus** (a part in a tight stack gets its label pushed PERPENDICULAR to the stack — the ceshi C2/C1/R1/C3 fix), then places via a ladder (base offset → grow rings → diagonals) at the lowest-cost slot. **Core fix vs v1: the obstacle set now includes OTHER parts' PADS** (a label over exposed copper is fab-clipped — why C1's label used to land on C2's pad), component bodies, keep-out regions (mechanical=hard/copper=soft), the **board outline** (containment), and other/frozen labels. Most-constrained-first order. Rotation stays **0** (upright, keeps `pcb check` clean); **bottom parts → bottom silk + mirror** (retry-without-mirror fallback). A boxed-in part is **left + reported in `unresolved`**, never moved onto a pad. `--side` biases the default, `--offset` = base gap, `--refs` limits to specific parts (others frozen). Outputs `aligned`/`warned`/`unresolved`/`skipped`.
- `easyeda pcb silk-add` — **add a FREE silkscreen string** (board marking / credit / note) at `--x/--y` with config: `--layer` (3=top silk default, 4=bottom), `--font-size` (mil), `--line-width` (stroke mil), `--rotation`, `--font-family` (for example Arial). Legible JLCPCB-safe defaults (font 40 / stroke 6) — **a small font (<~32mil) with a thick stroke smears the glyphs (糊)**. Returns primitiveId + rendered bbox and actual font family (check it fits + clears parts). Then restyle/reposition with `pcb silk-set`.
- `easyeda pcb silk-set` — **batch-adjust existing silk** (designators + free strings): `--ids id1,id2` (CSV) + any of `--x/--y/--rotation/--font-size/--line-width/--font-family/--text` (only given keys change). **ALIGN shortcut**: `--align center|mid|centerx|centery|left|right|top|bottom` + `--ref <designator>|board|outline|fill` positions each silk relative to that reference bbox (e.g. `--ref board --align centerx` centers the board credit; `--ref U1 --align top` aligns a label to U1's top), computed from the silk's own bbox. Uses the reliable `.modify(id,props)` — mutation 后即时 screenshot/list 可能带 `staleRisk`，最终字体、旋转和位置用 save → reload → `silk-list` 回读。
- `easyeda pcb silk-import-svg` — **import an SVG (logo / brand mark / artwork) as a FILLED silkscreen graphic** (`pcb.silk.import_svg` → `eda.pcb_PrimitiveImage.create`) — the typed path for placing a vector graphic on a PCB **without `debug.exec_js`**. The CLI parses the SVG (path `M/L/H/V/C/S/Q/T/A/Z`, `polygon`/`polyline`/`rect`/`circle`/`ellipse`/`line`, nested `transform`, viewBox), **flattens every curve to line segments**, applies viewBox→mil scaling, and sends the resulting **complex polygon** (contours + **even-odd holes**, so a logo's counters — the hole in an "o" — punch through) to the connector, which creates **one** image primitive on the silk layer. `--file <path>`/`--svg <string>`; `--x/--y` (or `--at "x,y"`) = where the artwork's **top-left** lands (mil); `--width`/`--height` in mil (`--keep-aspect` for uniform scaling; only one given ⇒ aspect always preserved); `--layer` 3=top (default) / 4=bottom (auto-mirrors); `--rotation`/`--mirror`; `--flatten-tol` (curve tolerance mil). **`--dry-run` parses + scales WITHOUT touching the editor** and prints target bbox / contour count / vertex count / **min-feature** (a DFM proxy — warns when < `--min-line-width`, JLCPCB silk min ≈ 6 mil) — always dry-run first. **Fill rule is even-odd; stroke-only art is not stroked (all geometry is filled).** Returns `primitiveId` + rendered `bbox`. **Real-machine verified**: creates on top/bottom silk, holes punch, rotation/mirror honored, and it **persists across `doc reload` + `pcb save`** (same primitiveId/bbox). Note: the image is a distinct primitive type — it does **not** appear in `pcb silk-list` (that lists silk *text*); read it back via `pcb check` (runs clean) or a snapshot. After a real import follow reload → check → `pcb save`.
- **Teardrops (泪滴) — `unsupported`.** `eda.*` has no verified create/apply-teardrop API
  (teardrops appear only as a `getManufactureFile` object type, never as a constructable primitive)。禁止用
  交互菜单补做；实现 typed 创建、对象回读与持久化验证前，此项保持未完成。
- `easyeda pcb route-critical` — **P7.0 关键网络先行,一条命令(#127)**:自动布线器最不擅长的两类先确定性做掉再锁死。**① power**:铜层数 ≥4 → `power-planes`(内电层),2 层 → `power-pour`(双面 GND+轨局部 pour);**② diff**:差分对识别双源合并——**块库 `signals` map**(type=diff_pair,带 90Ω/120Ω 阻抗与 `length_match_mm` 预算;USB_D/RS485_AB/USB-hub 各下行对)+ 保守**名字模式**扫描实网(`X_DP/X_DM`、`X_P/X_N`、`X+/X−`),每对用 short-route 规划器 45° 角同层成对布线,**逐对实测两侧长度与 skew**,超预算(默认 5mil,块值优先)**响亮报告不静默接受**(v1 不做蛇形调谐——本项目的对都是连接器→芯片短对,"成对、尽量短"就是规格);**③ lock**:`pcb.track.lock` 锁住布好的对网。之后剩余普通信号交正常档(route-short/用户点原生自动布线)。层数须来自有效实时证据；`--spec` 的层数与实板冲突时拒绝，不默认按两层继续。执行前读取板框、布局、规则和实际网络，结果按网络回读；旧 stage 状态不决定能否执行。`--dry-run` 只识别+规划;`--skip-power`/`--skip-diff`/`--no-lock` 单独关某步。
- `easyeda pcb track-lock` — **锁定/解锁已布铜皮**(#127,typed action `pcb.track.lock`,已从 debug.exec_js 版毕业):track+**arc**(beautify 圆角,旧 JS 版漏)+via+net-bound fill(`--no-fills` 排除);`--net`(可重复/逗号)/`--ids`/`--all`(仍要求 net≠"",板框永不隐式锁)三选一,`--unlock` 反向。**pour 永不锁**(要 reflow)。幂等:已处于目标态只计数。P7.0 契约:关键铜锁死后,原生自动布线/rip-up/pour-rebuild 都动不了它(rip_up 明确跳过 locked)。
- `easyeda pcb zones` — **功能分区一等公民(#126)**:把 S0 方案书 spec 的 `modules[].zone`(MCU 区/电源区/RF 区…)落成可执行、可校验的 claim 表。`zones set --spec <s0-spec.json>`(或手动 `--module "RF=right-top:U2,ANT1"`,可重复)把 module→{九宫格 zone, 器件清单} 持久化为项目 zone claim 数据(旧实现与 workflow 文件同库存储,跨 cwd 生效);zone 词汇 = 原理图 autolayout 同一套九宫格(`left/center/right × top/bottom` 及全高/全宽形式,共享词汇表),矩形在**消费时**从实时板框 bbox 解析(改板框不用重设 claim)。消费方:① `pcb place-constrained` — 被 claim 的**主芯片**若在区外→迁入该区(spiral 找位,diag `main:zoned:<module>`),**卫星件**合法化限制在区内(区满则出区放置+`satellite:zone-overflow` 诊断,check 会继续曝光);**边缘件豁免**(出边是比分区更硬的约束,diag 标 `:zone-exempt`);② `pcb check` 的 **zone-violation** 规则(见上文规则清单)。`zones status` 显示 claim + 实时违规速览;`zones clear` 清除。claim 是 spec 契约:布局失效/重摆不清它,只有 clear/重 set 会动。真机验证:ceshi 4 违规 → place-constrained 落区后 1(剩余那条正是「claim 与贴边矛盾」的真问题)。
- `easyeda pcb layout-lint` — **placement and routability facts before routing**。`--min-gap` 默认使用电气 clearance；手焊样例可通过 `pcb stage set-assembly --profile hand-solder` 提供装配参数，并检查普通间距、tight pair 与 solder-access。每个器件 bbox 四侧至少一侧有 `largePadAccessMil`（默认 60mil）的净通道；四面被围报告 `no-access`。`--gate`、`pre_route_passed` 和 confirm 状态仅为旧脚本兼容，不解锁布线。v1 仍是器件 bbox 级近似，Type-C 外壳脚/SOT-223 的实际烙铁进入方向须结合焊盘和局部图复核。
  **overlap / tight / 烙铁通道 三项都已「层感知」(#141)**:器件按**装配面**(`pcb.components.list`
  的 `layer`,1=顶 2=底)分组后才两两比 —— 顶层件与底层件落在同一 XY 是合法的**顶底对穿**,不再报
  overlap,也不再算 tight,底面邻居也不再堵顶面的烙铁通道。JSON 里每条 overlap/tight 带 `side`,
  报告头带 `sides`(如 `[bottom 134 / top 32]`),**双面板的 overlap 数字现在可以直接信**。
  实测 box-v2 rev-a(166 器件 / 642 焊盘,双面贴片):**overlap 116 → 0、tight 7 → 3**,与人工按层
  重算的真值一致(同层重叠 0)。等价于 KiCad 的分层 courtyard(`F.CrtYd`/`B.CrtYd`)语义 ——
  见 [`docs/ecosystem-survey.md`](https://github.com/zhoushoujianwork/easyeda-agent/blob/main/docs/ecosystem-survey.md) §9.3。
  **同时补了网络感知 —— 新增 `short` ERROR**:两器件 bbox 相交时进一步比**焊盘铜皮**,
  若两块铜在**共享层**上真的压在一起且**分属不同网络**,报 `ERROR short  C2.1[VBAT_RAW] ↔ D2.2[SW1_NODE]`
  —— 定性从「靠太近」升级成「这两网短路」,与 KiCad 的 `shorting_items` 对齐。short 与 overlap 同级致命
  (`ok=false`、score 归零、verdict `short`、gate 直接失败)。**短路判定按焊盘层而不是装配面**:
  两个异面 SMD 焊盘永不短路,但**通孔焊盘(层 12=multi)贯穿所有层**,能跟对面焊盘真短 —— 这是唯一
  不吃「同面才比」规则的地方。焊盘形状取不到尺寸(多边形焊盘)或无网络时**跳过而不猜**。
- `easyeda pcb layout-score` — **九维布局质量表 + 逐器件归因(#167)**。`layout-lint` 给的是**单标量可布性分**,
  公式 `100 −100×short −100×overlap −20×offBoard −4×crossing −1×tight` —— **一处重叠就把分数打成 0**,
  其余维度的差异全被抹平,看不出布局到底好在哪差在哪。`layout-score` 把「好布局」拆成九个**各自 0-100**
  的维度 + 加权综合分 + **每维「是哪几个器件拉低了它」**:

  | id | 中文 | 量什么 | 默认权重 |
  |---|---|---|---|
  | `partition` | 功能分区 | 模块领地(成员 center 包络)两两交错度 60 + 模块内紧凑度 40。归属优先读 spec `modules[].parts`,没 spec 就按信号网并查集**推断**(整维标 degraded) | 1.2 |
  | `flow-order` | 信号流向 | 各 flow 阶段的**面积加权质心**投影到流向轴,与 spec `flow` 声明顺序算 **Kendall tau-b**;分数 `(tau+1)/2×100`。**方向不强制** —— 板上从右到左走 电源→天线 与从左到右同样好(正反都算取绝对值大者,`reversed` 记进 Metrics)。板上不存在的阶段剔除而不是当 (0,0) | 0.8 |
  | `edge-io` | 对外接口与板沿 | ① 对外口聚一条边 + 开口朝外 ② `internal-on-edge` ③ `connector-plug-clearance`(见下) | 1.2 |
  | `protection` | 保护件/去耦就近 | `protection-too-far`(F\*/TVS/ESD/RV\* → 同网端子最近中心距)+ 复用既有 `decap-too-far`;两族**等权**合成(去耦件数通常碾压保护件,按件数加权会把保护件淹掉) | 1.0 |
  | `tidy` | 齐整度 | #153 五条子规则:落格 / 朝向一致 / 位号同侧 / 字号统一 / 阵列步进。**纯 cosmetic,永不进 blocking** | 0.5 |
  | `compact` | 紧凑度 | 板面利用率,**双侧评分**(太空和太挤都扣)。⚠️ 恒为 `degraded`:本项目没有真 courtyard,分母是**渲染 bbox**(含丝印/位号,比本体大 40%+),绝对刻度不可信、相对量可用 | 0.8 |
  | `rf` | 射频 | 天线馈点 → RF 源的馈线长度(2.4G 板上 λ≈2000mil,λ/10≈200mil 是"电气短"门槛)。⚠️ 恒为 `degraded`:keepout 全层那半边这里测不了(归 `pcb check` 的 antenna-keepout) | 1.0 |
  | `routable` | 可布性 | ratsnest 跨网交叉**密度**(÷信号网数)+ 飞线长度密度(÷板对角线)。**用密度不用绝对数** —— 166 器件的板天然比 20 器件的板交叉多,同样水准不该被判"very-hard" | 1.5 |
  | `clearance` | 装配间距 | tight pair(**绝对计数**,0.2mm 是工艺硬约束、大板不许挤)+ 手焊烙铁通道(#99 `no-access`)。一处违规先扣固定 25 分**把分压到 good 档以下**,保证「分数低 ⟺ gate 会挂」不再打架 | 1.5 |

  **三条硬约定**(这套度量可信的前提,读报告前先记住):
  1. **「没测」≠「测了满分」** —— 数据/意图缺失的维 `status=skipped`,**不参与加权**且必须给原因
     (如「没给 `--spec` 所以没有 flow 目标序列」「PCB 不在前台导致板框读不到」)。报告摘要显式写
     `N skipped`,不会把「7 维 90 分」读成全面体检。`degraded` 是第三态:算出来了但输入是近似,参与加权但要说明。
  2. **硬错不抹平分数** —— 短路 / 器件重叠 / 出板框进独立 `blocking[]` **一票否决**(verdict=`blocked`、
     非零退出),**不进加权**;而且几何两维**绝不再扣它们的分**,否则同一个缺陷被罚三次。
  3. **计数与判定同源** —— `verdict` 只从 `blocking` 数和 `overall` 推(`verdictFor` 唯一产出点,
     单测钉死),不会再出现「0 个阻塞项却 FAIL」。verdict 档:`blocked` / `poor`<55 / `fair`<75 / `good`<90 / `excellent`≥90。

  flag:`--spec <s0.json>`(解锁意图类维度;spec 有 ERROR 直接拒,WARN 打 stderr)、
  `--from <dump.json>`(离线重放,不连编辑器)、`--json`、`--min-score N`(不达标非零退出;**不设则只有 blocking 才非零**)、
  `--only/--skip <id,…>`(**拼错维度名直接报错**,不静默变成"一维都没算")、`--weight dim=val`(可重复)、
  `--grid <mil>`(齐整度落格网格,默认 **5**)、`--min-gap <mil>`(默认取板载 live clearance 规则)、`--all`(列全部归因)、
  **`--part J2,U1`(器件聚焦视角)**——整体归因是「维→器件」,这个是反向的「器件→全维度」汇总:该件的
  直接扣分、**关联提及**(TVS 离 J2 太远扣的是 TVS 的分但提及 J2——动哪个由人定)、blocking、几何现状
  (坐标/装配面/离板边距离)。**推荐工作流 = 整体打分 → 用户点名要优化的器件 → `--part` 聚焦**。
  位号匹配带词边界(C1 不误配 C10);`--all`/`--part` 时各维保留全量归因(routable 默认数据层截前 12)。
  默认每维只列前 3 个归因、且 ≥90 分的维不展开。归因 `penalty` 是**可比的扣分量**(不是布尔标记),
  `Σ 归因 = 100 − 该维分数` 是恒等式 —— 先动哪个器件涨多少分是可预测的。
  **插接面贴边规则(器件特性)**:Type-C/USB/SD 类水平插拔件在 300mil 边带内但**缩在板内 >25mil** →
  edge-io 扣分点名(`plug-face-not-flush` WARN,按缩进深度线性);齐平与**外突板框都合法** ——
  off-board 判据用**焊盘**不用 bbox,正是为了放行插接面外突这种正常设计。
  **插拔通道禁布(mating corridor,器件特性)**:卧贴插口的开口面前方 250mil(v1 待校准)×本体宽的走廊里
  不得有同面器件 —— 器件挡道 = 插头物理进不来。方向只认块库 `openings` 声明(贴边无声明推定朝外,
  走廊在板外自动裁掉不报;判不出方向绝不猜)。`pcb check` 计数 `connectorMatingBlocked`(WARN),
  edge-io 维按遮挡件扣分(10/件封顶 30),**归因落在遮挡件**(连接器贴边定死,动遮挡件才是解法);
  `pcb place-constrained` T2 落位后把走廊记为占用,T4 卫星不会再被规划进去(只挡规划,不产生 move)。
  **`layout-lint` 与 `layout-score` 不互相取代**:前者列重叠、出板框、间距和装配可达等具体事实，
  后者是质量诊断表；二者都不许可或拒绝下一步。几何维**复用** layout-lint 的纯核
  (`analyzePcbLayout`),同一个量只准有一个算法。权重与阈值全是**待校准初值**,校准闭环 = `pcb dump` 出好板 fixture
  → `--from` 离线重放 → 好板某维掉分就回去改度量,不是改板子。
  > **#168 两条连接器规则两边都出**:`pcb check`(计数 `internalOnEdge` / `connectorPlugClearance`)
  > 与 `layout-score` 的 `edge-io` 维(`--json` 的 `dimensions[].findings[]`)。判据本体是
  > `pcb_check_connector.go` 里的纯函数,两边共用同一份实现,不会给出矛盾答案。
  > `pcb check --spec <s0.json>` 让 `internal-on-edge` 读 spec 声明的 facing 而不是靠猜:
  > - **`internal-on-edge`** — 被标 internal 的连接器占了板外沿(默认 300mil 外沿带)。
  >   **spec 显式声明 → WARN**(板级决定,可信);**启发式推定 → INFO**(线对板封装 + 无对外语义网,
  >   会把接箱外传感器的 XH 座误判,不该阻塞)。报文自带 `internal=spec|heuristic` 标明来源。
  > - **`connector-plug-clearance`** — 相邻**对外**连接器中心距 < 两者**插头护套包络宽**的均值 → WARN。
  >   护套宽三级取值:spec `interfaces[].plugWidthMm` > 块库查找表
  >   `internal/blocks/data/_plug_envelope.json`(13 条,每条带 `confidence`=datasheet/measured/estimated + 出处;
  >   排式连接器按 `pitch×(pins−1)+margin` 随脚数算)> **bbox+2mm 兜底并标 `plug-width=fallback`**。
  >   只比同装配面 + 同板边的配对。**这是 layout-lint/pcb check 结构性看不见的一类**:
  >   它们只看铜箔与渲染 bbox,而插头护套是板上根本不存在的三维实体(实测 box-v2 rev-a 底边三口
  >   中心距 12–13mm,按母座 ~9mm 判全过,按插头护套判才暴露)。
  >
  > 完整规范 → `pcb-design-rules.md` **§3.5 对外接口与板沿**(报文里的 `[规范 §3.5]` 即指该节)。
- `easyeda pcb floorplan --spec <s0.json>` — **从 S0 `flow` 推布局骨架(只读,#167)**。把
  `flow`(如 `["POWER","MCU","RF","ANT"]`)沿流向轴切成**有序**功能带,**带宽按各段器件面积分配**,
  并把 spec 里显式声明了 `ref`+`edge` 的连接器钉到目标边(边序是装配体验,工具不猜 —— 没写 `edge` 就不钉)。
  **与 `pcb zones` 并存不互相取代**:zones 是固定 3×2 九宫格,能表达「MCU 在中间」这种位置意图,
  但表达不了**顺序**(谁在谁之后)、**比例**(166 器件的域不该和 3 器件的域等宽)和**段数**(flow 可能 2 段也可能 6 段)。
  **⚠️ 只读 —— 本命令不搬器件**,落笔仍走 `pcb place-constrained`。之所以先只做规划:floorplan 决定的是
  「板子怎么分区」,这件事错了后面搬多少次件都是白搬,值得先让人看一眼。**方向不强制**:已有器件分布更接近
  反向时按反向切带(输出 `reversed=true`),不会把一块本来就摆对的板翻过来重排。flag:`--from <dump.json>`(离线)、
  `--json`、`--margin <mil>`(板边留白,默认 300)、`--min-band <mil>`(最小带宽,默认 400 —— 小段不许塌成零)。
  输出含 `bands[]`(功能域/面积/矩形/器件)、`pins[]`(钉边连接器目标点)、`unzoned[]`(未归属器件)、`warnings[]`。
  `--spec` 必填(骨架只能从 flow 来,没有别的东西可推);spec 有 ERROR 时拒绝执行。
- `easyeda pcb dump [--out board.json]` — **板级几何快照(只读)**。一次拉齐器件
  (anchor/rotation/locked/bbox/pads)+ 板框 + 丝印 + live DRC 规则 + 铜层数,写成**自包含 JSON**。
  存在理由是**金标准好板回归**(#167 第五层):好板必须能变成**离线 fixture**,否则每次改权重都得开着
  EasyEDA 手动重跑,回归形同虚设。喂回 `pcb layout-score --from board.json` / `pcb floorplan --from …`
  即可**不连编辑器**重放(CI 里也能跑)。⚠️ **板框需要 PCB 是前台文档**(否则平台返 null)——
  快照把这类降级如实记进 `partial[]`,不假装板子没有边。与 `pcb stage-snapshot` 区别:那个抓 PNG 给人看,
  这个抓结构化几何给 CLI/单测吃。flag:`--out`(默认 stdout)、`--label`、`--no-silk`/`--no-rules`/`--no-layers`(省往返)。
- `easyeda spec validate <s0.json>` / `easyeda spec show <s0.json>` — **S0 方案书的校验与归一化查看(#167)**。
  在此之前 S0 spec 写错是**完全静默**的(只要 `modules[].zone/parts` 对,`zones set` 就成功,其余字段无人看)。
  判定口径刻意宽松以兼容既有 spec:**ERROR** = 写了但写错(枚举外的 zone/kind/facing、flow 里重复或不存在的阶段、
  `internal:true` 与 `facing:"user-facing"` 自相矛盾);**WARN** = 缺了会让某维测不了(没有 flow、模块没归功能域、
  flow 阶段在板上没有对应模块);**INFO** = 能力降级(接口没写 `ref` → 连接器规则只能退回启发式 INFO 档)。
  默认只有 ERROR 非零退出,`--strict` 让 WARN 也失败(交付前用),`--json` 出 `{ok, issues, counts}`。
  `spec show` 打印**归一化后**的 spec(`board` 字符串写法折进 outline、`stackup.inner1/inner2` 折进 `innerLayers`、
  模块 `kind` 按 `name` 补全)—— 用来确认「工具**实际读到的**意图」与你写的是不是一回事。
  字段形状见 [`design-flow.md`](./design-flow.md) S0。**兼容性原则是只加不改**:既有 spec 全部继续能读,缺新字段只报 WARN/INFO。
- `easyeda pcb route-short` — **short-trace self-router** (daemon-side, the heuristic tier — NOT `pcb autoroute`/Freerouting). Per net: MST over pads, then a track per hop ≤ `--max-len` (Manhattan) on the pads' shared layer. **Skips power+ground nets by default** (VCC/3V3/GND/… via `isGlobalNet`) — they belong in a POUR, not thin tracks; `--route-power` forces routing them. (Measured on ceshi: routing 3V3 as thin tracks caused **18 of 27** Safe-Spacing violations — pouring power instead dropped Safe-Spacing 27→3. Do `pcb pour` GND + each power net after routing signal. Residual No-Connection on a 2-layer board = the pour can't reach every scattered power pad on a shared layer; that needs via-stitching / a dedicated plane layer.) Also skips already-routed nets, cross-layer hops (need a via), over-long hops (maze tier). **Widths are net-class rule-aware**: each net's width is picked by **role** (signal / power-branch 3V3·1V8 / power-trunk +5V / high-current VBUS·VIN — the §7.8 role split on the §1.2 metric grid: 0.25/0.4/0.5mm, `pcb_netclass.go`), seeded from the board's live DRC track-width spec (`pcb.drc.rules`, clamped ≥ the rule minimum) so a 3V3 branch gets 0.25mm (≈9.84mil) while a VBUS input gets 0.5mm (≈19.69mil), instead of the old flat power/signal 20/10 mil buckets. `pcb net-classes` prints the active ladder; `--width-signal` overrides the signal role, `--width-power` forces ONE width across all power roles (legacy), `--width` forces everything. **Corner style** via `--corner`: `90` (Manhattan L, default), `45` (chamfer — avoids acid traps/reflections), `round` (chord-approximated fillet, `--round-radius`; native arcs don't commit on this build so it's segmented). **Obstacle-aware (v2/v3)**: each hop picks the L orientation (horizontal- vs vertical-first) that crosses the fewest already-placed **other-net** tracks + other-net pads; `--no-avoid` restores the v1 naive horizontal-first. **Hard clearance gate (#111/#119/#122)**: other-net **pads**, **vias**, **same-layer tracks** (crossing OR under-clearance parallel run — the R2 SPIHD×SPIWP shorts) and **board cutouts/slots** (max(clearance,8mil) band, Slot Region to Track) are a **veto, not a cost** — a hop that cannot clear them detours (`--multilayer`) or lands in diagnostics unrouted; route-short never draws what `pcb check`/native DRC would flag (judges are shared with `findClearanceViolations`). Still NOT a maze router (no push-shove/vias/rip-up) — **run after `auto-place`** so hops are short/clear, then `pcb drc`. `--dry-run` previews. 布线方式见 [`design-flow.md`](./design-flow.md) P7：稀疏短连线可用本命令，稠密板可用 EasyEDA 原生自动布线或已配置的外部路由器。执行前读取最终板框、固定件、禁布区和实际规则；`--dry-run` 只出计划。旧 stage/force 参数为兼容保留，不决定命令能否运行。
- `easyeda pcb stackup` — **board stackup: copper layer count + inner-layer types** (`pcb.stackup.set` / read via `pcb layers`). `pcb stackup set --layers 4` sets the count (2|4|6|…|32, `eda.pcb_Layer.setTheNumberOfCopperLayers`); `--plane 15 --plane 16` / `--signal 15` set inner layers' type (SIGNAL↔PLANE/内电层, `modifyLayer` — only INNER layers accept a type change). Set the layer count BEFORE routing/pouring inner layers. **A net-bound 内电层 (PLANE) IS achievable via API** — verified recipe: pour the net on the inner layer **while it is still SIGNAL** (`pcb pour`/`power-planes`), THEN flip the type (`--plane 15`), THEN `pcb pour-rebuild`. The net-bound fill survives the flip and DRC stays clean (0 Plane-Zone/via clashes). Doing it in the other order (flip type first, then pour on a PLANE layer) is the path that breaks — the pour lands netless on L1. `power-planes` does this for you (`--gnd-plane`, on by default).
- `easyeda pcb power-planes` — **4-layer power distribution (the proper fix for the 2-layer pour conflict)**. Requires a verified ≥4-layer board by default; a confirmed 2→4 upgrade needs `--allow-stackup-change`, and unknown layer evidence always refuses. Existing 6+ layer boards retain their layer count. Assigns GND + power nets to inner layers, **via-stitches every power/ground pad DOWN to its plane** (the connection point the inner pour needs — without it the inner pour is all isolated islands and deposits nothing), then pours each net on its inner layer, then **flips the GND inner layer to 内电层/PLANE** (`--gnd-plane`, on by default) and rebuilds. **Order matters: vias BEFORE the pour** (empty otherwise), and the plane-flip AFTER the pour (the verified pour-while-SIGNAL → flip → rebuild recipe keeps the fill and DRC clean). The power layer stays 信号层 so its pour is an ordinary positive plane — matching the common customer stackup **GND=内电层 / VCC(3V3)=信号层** (e.g. `esp32MiniRequire.md`). `--gnd-layer 15 --power-layer 16` (defaults); `--gnd-plane=false` keeps GND a plain signal-layer pour. **Validated on ceshi: DRC 31 → 0, No-Connection → 0** — dedicated planes solve what a shared 2-layer pour can't (two power nets stranding each other's pads). Run AFTER auto-place + outline-fit + route-short (signals). Two power nets sharing one plane layer re-create the conflict (warned) — give each its own inner layer on 6+ layers. `--dry-run` prints the net→layer plan. **Reload evidence (#114/#117)**: the run records nets deliberately routed as tracks (`powerTracksNets`) and nets poured before a layer was flipped to PLANE (`planePouredNets`). This history explains why `pcb.pour.list` may not see a PLANE-layer pour after `doc reload` (#110); it does not authorize later actions. Standalone `pcb check` (no state) degrades a GND finding to **INFO** whenever the board carries a net-unknown PLANE layer — treat `pcb drc` Connection=0 as the arbiter, do NOT re-pour.
- `easyeda pcb power-pour` — **2-layer power distribution (the 2-layer analog of `power-planes`)**. Delivers every power net through copper **POUR area** instead of thin tracks: **GND** → a board-outline-fitted pour on `--gnd-layers` (default **both**, the reference plane); **each non-GND rail** (3V3/5V/VBUS… via `isGlobalNet`) → a **LOCAL pour** bounded to the bbox of ITS OWN pads (+`--margin`) on the **top** layer, so a small rail doesn't claim the whole board. Every region is a **DYNAMIC pour** (retreats from other-net copper by the clearance rule) — different-net regions never short, whereas a static `fill` would; **that's why it uses pours, not fills.** Rails with <2 pads are skipped; `--replace` clears same-net pours first (default on), `--rebuild` reflows after (default on), `--rails skip` pours only GND. Run AFTER auto-place + outline-fit + route-short (signals), then `pcb check` (**power-not-poured** should clear) + `pcb drc`. Use `power-planes` for 4-layer boards. Core in `pcb_powerpour.go`; `--dry-run` prints the nets→layers→rects plan.
- `easyeda pcb beautify` — **走线美化 (routing beautification, `pcb.beautify`)** — round sharp track corners into arcs once routing is final (the aesthetics/manufacturability post-process; design-flow **P7.9**). Chains connected same-net/same-layer segments into polylines and fillets each interior corner (radius = `max(track width) * --radius-ratio`, default 3), replacing the originals with trimmed lines + arcs. Because it deletes+recreates copper it **self-guards**: a DRC binary-search (`--drc-retry`, default 4) shrinks or straightens any corner that violates clearance, then it **rebuilds copper pours** (same-net bonding goes stale after track edits — the familiar `pour-rebuild` step, folded in). **Diff-pair / equal-length nets** get concentric-arc protection when the build exposes `pcb_Drc.getAllDifferentialPairs`/`getAllEqualLengthNetGroups`, else those corners stay straight. **Copper layers only** — never touches silkscreen/outline; skips locked copper. **Always `--dry-run` first** (reports paths/lines/arcs WITHOUT mutating — safe on any board, even one you don't want to change), then run for real and `pcb save`. Flags: `--selected` (only tracks selected in EasyEDA, default whole board), `--net` (**repeatable** — `--net USB_DP --net USB_DM` beautifies only those nets; the safest way to apply on a dense board — small blast radius, dry-run + DRC each net), `--layer` filter, `--force-arc` (round even too-short segments), `--merge-u` (fuse tight U-bends into one arc), `--no-protect`/`--no-drc`/`--no-pour-rebuild`. **On a dense, not-yet-DRC-clean board prefer per-net over a full-board pass** — a whole-board run both has a large blast radius and surfaces the board's pre-existing violations alongside its own. Absorbed from the open-source **Easy_EDA_PCB_Beautify** (m-RNA, Apache-2.0; see repo `NOTICE`). Line-width bezier smoothing is a documented follow-up. Advice from upstream: pad-to-track joints may need a manual look, exclude RF/high-speed nets from a global pass (do them per-`--net`), preview Gerber before fab.

#### 待支持 — 布线/覆铜质量 (roadmap, not yet implemented)

v1 (`route-short` / `pour`) is mechanically correct but coarse. Planned quality upgrades:

- ✅ **填充区域 / 轮廓对象 (net-bound filled region, 异形大块铜)** (task #17, done) — `pcb fill create`
  (`eda.pcb_PrimitiveFill`, net-bound static copper). See the "Net-bound filled region" section above.
- ✅ **DSN keep-out injection** (task #17, done) — `pcb export-dsn` re-injects `pcb_PrimitiveRegion`
  keep-out as `(keepout (polygon …))` into the DSN `(structure)` (getDsnFile drops them). Default on;
  `--raw` skips. End-to-end Freerouting *honor* check is part of the #5 maze-tier toolchain.
- ✅ **DFM 审查 (design-for-manufacture audit)** (task #33, done) — `pcb check`: acute-angle / dangling-end /
  non-orthogonal(自由角度走线)/ track-over-pad(走线压焊盘=短路)/ silkscreen-flipped(丝印正反/放反)/
  overlapping- & single-layer-via / 2-pin width-mismatch / duplicate-segment. Copper rules reconstructed
  Go-side from placed copper; the silkscreen rule reads `pcb.silk.list` (text layer+mirror). See the
  `pcb check` bullet in **Read / inspect**. Absorbs the official DFM tool's geometry checks
  (`docs/reviews/2026-07-marketplace-coverage.md`, HIGH item).
- ✅ **布局质量多维打分 (#167, done)** — `pcb layout-score` 九维 + 归因、`pcb floorplan` 有序功能带、
  `pcb dump` 离线 fixture、`easyeda spec validate/show` S0 契约化。见上方 **Layout adjustment** 三条。
  论点:**能自动逼近的前提是先能量化打分 —— 你没法优化一个你测不出来的东西**,所以建设顺序是先
  DETECT(打分)再 ACHIEVE(逼近)。
- ✅ **#168 两条连接器规则 (done)** — `internal-on-edge` / `connector-plug-clearance` 已接进
  `pcb check`(计数字段 `internalOnEdge` / `connectorPlugClearance`,因此也进 `--strict` 门)
  并同时喂 `layout-score` 的 edge-io 维。真板验收(车机V2,166 器件)两条都命中 issue 原文描述的问题:
  J1(PH2.0 电池座)离底边 119mil、J2↔J_VEH 中心距 13.00mm < 插头护套要求的 13.98mm。
  同一轮验收还修掉一个误判:`USBLC6-2SC6`(ESD 二极管,名含 "usb")与 `SMAJ5.0A`(TVS,SMA 是**封装**名)
  曾被器件名正则判成连接器,后者还查到了 SMA 射频接头的 14mm 护套宽 —— 现在**位号前缀优先于器件名**。
- 🚧 **权重/阈值的金标准校准 (#167 第五层)** — 回归**框架**已就位(`internal/app/testdata/boards/`
  正负对照 fixture 对 + `make layout-calibrate`,好板不掉分/坏板仍报警,离线可跑),但 fixture
  目前全是**合成板**(参考板满分是同义反复)——真板校准(oshwhub 公认好板 `pcb dump` 进
  fixture、人工核定期望值)尚未发生,九维权重与阈值仍是**待校准初值**。真板入库判据:
  开源/官方参考板可入 testdata,**商业设计一律走 `EASYEDA_BENCH_BOARD` 环境变量不入库**。
- ✅ **布局精修环 (ACHIEVE 侧,`pcb refine`,#167 #153) — 骨架已落地,变换器 1/9 维**。
  读 `layout-score` 逐维归因,对最弱维下**确定性**变换,每步后复核:任一步让 `pcb check`
  新增 finding 或综合分下降就**回滚该步**(逐步回滚,好步不被坏步连累)。**默认 dry-run,
  `--apply` 才落笔**(与本仓多数命令相反,刻意的——批量搬件,#153 实测一个"无害"对齐曾
  静默制造 3 条压焊盘丝印)。三条保护默认开:不可动集合(锁定件+本轮参数明确的固定件)、位移预算
  超限**剔除不截断**、回读证实才算 restored。**当前唯一变换器 = grid-snap → tidy 维**
  (#153 实测零副作用);其余八维照实报告「无对症 typed 手段,`layout-score --only <dim> --all`
  列出器件并标记工具缺口」——**blocking(重叠/短路/出板框)不归 refine 管**,先走
  `place-constrained` 等 typed 合法化动作；仍不能清除时停止并补工具。真机闭环实测(ceshi):65.2[blocked] →
  place-constrained 清 blocking → refine → 78.6[good]。

### Board outline (板框)

The board outline anchors edge keep-out, connectors-to-edge and mounting holes, so
`place-constrained`'s edge heuristic needs *some* outline to snap to. **Two legal
paths, by whether mechanical dimensions exist (issue #97 — these do NOT conflict):**

- **有机械尺寸/外壳约束**: build the specified outline FIRST (`outline.set` /
  `outline-round`), read back its centerline geometry, then place against those real edges.
- **无机械尺寸**: rough-place first with a **temporary oversize outline** (`outline-fit`
  with a generous `--margin` so `place-constrained` has an edge to snap to), then tighten
  the outline (`outline-fit`/`outline-round`) once placement is done.

Both paths end by reading the final outline, component bboxes/pads and keep-out regions,
then running layout and routability checks. If an outline edit occurs after placement or
copper work, recompute edge distances and inspect stranded copper. Legacy
`pcb stage confirm-*`, `workflow status/advance` and force flags remain callable for old
scripts, but their stored state is diagnostic history and does not authorize routing.

- `pcb.outline.set` — set the outline from a closed polygon `points` (`[[x,y],…]`, mil,
  y-up). Replaces any existing outline; reports `allInside`/`outside` (components out of
  the board). 在已授权的目标板上先保存原板框参数，写后回读，不依赖 stage 签字。
- `pcb.outline.get` — current outline (source/native arc count + bbox + **真多边形 `points`/`outlineFormat`**,#167)。
  新圆角 polyline 使用 `sourceArcs` / `nativeArcs`；兼容字段 `arcs` / `legacyArcs` 只统计独立旧式 Arc 图元。
  `points` 是板框折线**中心线**点集 = 铣刀走的真板边;`bbox` 是**渲染范围含线宽**(实测 10mil 线宽每边大 5mil)。
  异形板(Type-C 凸出/缺口/铣槽)上「到板边距离」必须用 `points`——AABB 会把贴着凸出部真边的件误判成离边很远。
  已实测的 `ARC,signedAngle,endX,endY` 按 ≤2° 采样;多条可解析闭环仅在唯一最大环包含其他全部环时选为外边。
  `CARC/C/R/CIRCLE`、并列外环、等面积环或无法证明包含关系时仍 fail closed，退化为 bbox 并标 `degraded`。
- `pcb.outline.clear` — remove the outline.

任意多边形仍由 Agent 生成闭合 `points`。标准圆角矩形使用 `outline-round` 的原生 ARC，
不要用折线近似，也禁止转到 GUI 手工补画。其他曲线形状只有在 typed 接口能够创建、回读并
保存时才执行；当前不能证明的能力标 `unsupported`。以下公式只用于离线计算点列（中心
`(cx,cy)`，单位 mil），不能把采样折线声明成真圆弧：

| Shape | Points |
|---|---|
| Rectangle `w×h` | the 4 corners |
| Rounded-rect | corners replaced by N-step quarter-circle fillets of radius `r` |
| Circle Ø`d` | `N≈72`: `[cx+r·cosθ, cy+r·sinθ]` for `θ=2πi/N`, `r=d/2` |
| Instrument / dashboard (异形) | squircle `x=a·sign(cosθ)·|cosθ|^(2/n)`, `y=b·sign(sinθ)·|sinθ|^(2/n)` (n≈3.6) + width taper `x·(1+k·y/b)` + top-centre arch — a wide rounded shield |

Size the outline to enclose the component extent (`pcb.components.list --includeBBox`)
with margin, then verify `allInside` from the response.

## Auto-layout — execute per the conventions

Follow the priority hierarchy in
[`pcb-layout-conventions.md`](./pcb-layout-conventions.md)
(**P0 mechanical/enclosure > P1 safety/isolation > P2 EMI hot-loop + critical decoupling >
P3 reference-plane/return > P4 thermal keep-out > P5 functional grouping > P6 DFM >
P7 grid/align/silkscreen** — P7 is cosmetic and never overrides a function-driven position).

Operational order:

1. **Read state** — `pcb.components.list` (`includeBBox`+`includePads`) + `pcb.layers.list` (`copperLayerCount`) + `pcb.nets.list`; classify each part by net/designator (anchor / hot / sensitive / IC / passive).
2. **P0** — place connectors (J/USB) and mounting holes (H/MH) at enclosure coords and **`lock`** them; treat as immovable obstacles; edge connectors open outward.
3. **P6 coarse seed** — when the board has a clear main chip, `easyeda pcb auto-place` (module-aware: satellites hug the chip pin they connect to); otherwise `pcb.components.arrange mode=cluster` for a net-clustered seed. Run `--dry-run` first to review the plan.
4. **P2/P4 local overrides** — decoupling caps tight to the IC power pin (≤2-layer ≤150 mil; 4+-layer ≤250 mil **but leave via room**); crystal + 2 load caps tight to the MCU osc pins inside a 200 mil guard; minimize the switcher input loop `{Cin + switch + catch-diode}` bbox; spread hot parts ≥400 mil; keep heat-sensitive parts (electrolytics/crystals/sensors) ≥200 mil from heat.
5. **P7 tidy-up** — `pcb.align` / `pcb.distribute` / `pcb.grid_snap`, **without breaking any function-driven position**.
6. **Verify** — `pcb.drc.check` (and the PCB linter once it lands); fix by rule number. Pull fresh primitiveIds before each mutation; confirm destructive ops; log before/after.

**Key corrections from review** (see the conventions doc): decoupling effectiveness is governed by the cap's **mounting-loop inductance** (pad→via→plane), not raw distance; **default a single solid ground plane** partitioned by placement (do *not* split-ground by default); all hard thresholds are **conditioned on stackup / fab / enclosure** context.
