
# EasyEDA Schematic

> 1.4 先建立并校验 component/pin/net/pin-to-net，再布局。真实 wire 是连接首选；网络标签只作跨模块、跨页和电源辅助。模块复用遵循 [`docs/schematic-connectivity-model.md`](../../../docs/schematic-connectivity-model.md)。

Use `easyeda-agent` typed actions. Do not write raw EasyEDA JavaScript unless a typed action is missing and the user explicitly accepts a debug path.

> **本文导航**:Workflow · Production preflight gates · library-first 绘图 · netlist 批量实现 ·
> pin-aware autoconnect · module-aware autolayout · zone-less packing · Actions · Bundled Scripts ·
> Guardrails · Layout Conventions · EasyEDA Electrical Rules(load-bearing)· Missing Actions。

## Workflow

1. Run `easyeda health`.
2. Read active project and schematic context.
3. Inspect before mutating.
4. Prefer small additive operations.
5. Verify each mutation by readback, snapshot, or DRC.
6. Ask before destructive operations or a multi-step mutation plan. A save at an already-defined
   passed stage is mandatory and does not need separate confirmation unless the user explicitly
   requested step-by-step approval.
7. Summarize changed primitives, warnings, and artifacts.
8. If an official EasyEDA API is missing, undocumented, or differs from runtime behavior, record the evidence and workaround; when it affects correctness or maintainability, prepare a minimal repro and file an issue with the relevant official EasyEDA repository.

## Production preflight gates

- **Sheet first, default A4.** Before any whole-board placement/routing, run `easyeda doc ls`, switch to the target schematic page, then run `easyeda sch sheet-geometry --json`. If no `componentType:"sheet"` bbox is available, stop and ask the user to select/create the default A4 sheet in EasyEDA; do not place parts, wire nets, or run `sch autolayout --apply` on a sheetless page. **图签 keep-out 只对 A4 横版标定过**(实测真实图签≈右下 60%宽×20%高 → `(468,0)..(1170,165)`,`source:known-template-ratio`;旧 0.22×0.14 只圈右半日期列、漏保护左半是已修的坑)。**非 A4 尺寸**(A3+)图签是定尺寸、占比更小,`sheet-geometry` 会**降级 `source:fallback-ratio` 并 warn「calibrated for A4 landscape only」**——那时 keep-out 是过估近似,别当硬门信,人工核对。所以**默认就用 A4**;要支持别的尺寸需按该尺寸补标定。
- **Page plan before coordinates.** For non-trivial designs, decide the page/module split from the A4 usable area before placing anything. If the modules do not fit with route channels and title-block keep-out, create/rename pages and split modules instead of expanding coordinates outside the sheet.
- **Clear is destructive.** Use `easyeda sch clear --dry-run` first, report the delete counts, and wait for explicit confirmation before `easyeda sch clear`. Preserve the sheet by default; only use `--no-preserve-sheet` when the user explicitly wants the drawing frame removed too. After clearing, read back the page and confirm only the intended sheet/template primitives remain.
- **Honor step confirmations.** If the user asks to confirm each step, stop after every stage report (preflight, clear dry-run, clear apply, page creation, placement dry-run, placement apply, wiring, verification, save) until they approve the next mutation.

## Drawing a schematic — library-first (default)

> **Design conventions live in this skill's references**
> (layout zones, spacing, wire/orientation rules, part-selection criteria, the
> canonical orientation table + standard-parts library). This operational skill
> **links** to it — single source, never copy the rules here.

> ⚠️ **整板 / 非平凡设计 → 先走 [`design-flow.md`](./design-flow.md) 流程脊柱。**
> 那里有分阶段 + 硬门禁(预分析 → 分页 → 模块编组 → 按组摆放 → 通道布线 → DRC + layout-lint → 调整闭环),
> 专治「随手摆导致覆盖、外围乱飞、线压元件」。本 skill 提供它每一步要调用的**具体动作**。
>
> ⚠️ **多器件 / 整板设计:先花几分钟摸底,再动手。** 非平凡板子(>~10 件,或要交付/排 PCB)
> place 前快速读懂设计(器件/电源树/功能分组/幅面)——见
> [`design-pre-analysis.md`](./design-pre-analysis.md)(轻量摸底,不是门禁)。
> 然后照 [`auto-layout-sop.md`](./auto-layout-sop.md)
> 的 CLI 能力 + 硬坑落坐标,布局**用数据 + 截图自调**(放→读回坐标→`sch layout-lint` 判覆盖/间距→挪→再验)。
> 小改 / 几个器件直接按下面放置。
>
> ⚠️ **标准外围先查块(铁律 8):** `easyeda blocks show <id>` 给 `internal_nets`(照抄拓扑,引脚用
> 功能名零改号)+ `ports`(重绑边界网络)+ `schematic_notes`(落线注意);命中就别手接。ESP32
> 自动下载(双三极管交叉耦合时序易接反)这类电路尤其照块抄,别凭记忆手连。
> **块的 `schematic_layout` 有两种形态**(#180)。**关系形态(推荐)**只声明意图 ——
> `flow`(信号流左→右)/ `attach`(角色→目标.引脚,去耦贴电源脚)/ `pair`(等距并列组),
> **一个坐标都不写**;`sch block-apply` 走**两阶段求解**:先落锚件(五级判据自动选:
> 被 attach 指向最多者 = 主芯片)→ 回读它的**实测引脚坐标** → 据此算其余件 → 逐个放。
> 避让是**受约束的**(只沿关系自己的轴推,另一轴钉死),所以 flow 永远共线、pair 躲让
> 也走整数倍 pitch、attach 永远待在目标引脚那一侧 —— 用环形推让会把关系语义当场
> 破坏(实测 flow 两件 y 差 220、pair 完全不成对)。
> **落地后、连线前还有一步「推让」**:数锚件每一侧要挂几个 marker,算出需要多深的通道,
> 通道带里的件不够远就整条链让开(被推的件挤到更外侧的件,那一件跟着让;pair 组整体平移,
> attach 的去耦**永不推**)。它解两遍 —— 落地前按估算尺寸(决定件创建在哪),**落地后按
> 实测 bbox 再补一次**(符号的锚点常常不在 bbox 中心,估算必然差一截),然后才过布线前硬门。
> 所以 **`--at` 给的坐标不是最终坐标**,以 manifest 里的 `AT` 为准。
> 日志把算术写全了,照着判断即可:`relational: left 侧 6 个 marker 需 276,与 D1 只有 120
> —— D1 让 155、J1 让 55(通道 → 274)`;推不动时会说被谁顶住(可用区边界 / 页面上已有图元),
> 那就是**该换更大图纸或拆页**的信号,不是重跑一次能解决的。
> **落完自动按「功能子群」登记虚拟组**(不是整块一个组):有 `flow` 的块按**信号流每一级**
> 一群(CH340C → `/J_USB`(Type-C + CC 下拉)/ `/D_ESD` / `/U`(桥芯片 + 去耦));
> **没有 flow 的块按 `attach` 的目标引脚**分群 —— **贴同一个脚的件就是一个功能单元**,
> 锚件自成一群(群名 = 块短名)、其余按 `ROLE_PIN` 命名(2026-08-20 修复:此前归属只
> 取 `role.pin` 的 role 那一半,`U.3V3`/`U.EN`/`U.IO0` 一律归约成 `U`,于是
> `esp32s3_wroom1_module` 6 件糊成一个 507×712 的区,**独占一整页也放不进 A4**,
> `zone-arrange` 四条边逐条报「被图签挡」;现在拆成
> `esp32s3_wroom1_module`(U)/ `U_3V3`(C_VDD+C_BULK)/ `U_EN`(R_EN+C_EN)/
> `U_IO0`(R_IO0)四群,离线判据实测同页排得下)。**件太少或 attach 全指同一个脚就不拆**
> (小块本来就是一个功能单元,硬拆只是多两个空框)。子群 = `sch group-move --group <id>`
> 的抓手,也是 `zone-plan` 的分区粒度 —— 组名末段就是区名。
> **legacy 形态**(`roles` 绝对偏移)
> 仍受支持但已废弃:块作者写模板时不知道实例会落在页面哪里、图纸多大,手算必踩
> 出界/顶标题带。原点自动避开已有器件真实 bbox 且**不出图纸**(显式 `--at` 优先);
> 螺旋搜索落空时还有一层网格扫描兜底(螺旋步长随块尺寸放大,中等块常常一个候选都
> 落不进可用区,那不等于放不下)。每次 place 都记录平台返回的
> `primitiveId`,落完回读真实 bbox + pins 作**布线前硬门**:读取/解析/几何不完整、bbox overlap
> 或异件引脚重合都会在 autoconnect 前失败。命令只按本次返回的 ID 补偿删除并再次读回;
> 能证明删净报 `failed-rolled-back`,否则报 `failed-partial` + `PARTIAL STATE`,绝不把独立 autosave
> 的多次变异伪装成事务。优先用 `block-apply` 而不是逐件手放。
>
> 🩹 **place 超时收编(假失败定律在 place 上的缺口,2026-08-19 修)**:`place` 报
> `connector did not respond` **不等于没落地** —— 连接器侧 `create` 通常已经建好了件,丢的
> 只是回执。以前 Go 侧拿不到 `primitiveId`,回滚无从下手,那个件就永远留在页上(真机一轮
> 留下 `U2`/`U2`/`U3` 三件,**每次重试再生一个**)。现在 block-apply 在放置前快照本页全部
> 器件 id;place 超时(或成功但没回 id)时做一次 **settle 回读**,把**同时满足「不在快照里」
> 「componentType == part」「落在下发坐标 ±5」**的那个件认回来(`rollback.adoptedPrimitiveIds`),
> 它随后走和正常件一样的逐个删+回读证实。**绝不凭空造 id**;页面上原有的同型同坐标器件
> 天生在快照里,永远不会被收编或误删;快照读不到就**整个关掉收编**并如实报 PARTIAL STATE。
> 命中 ≥2 个则不收编但**逐一点名**,并打印可直接跑的 `sch prim-delete --ids …`。
>
> 🩹 **命中 0 个要先证明「回读是新鲜的」才算数(2026-08-20 修)**:`adopt ✓ …确实没有落地`
> 这句话曾在**它唯一该起作用的场景里系统性说反话** —— 真机上 place C8 超时,收编回读报
> 「(440,535) 附近没有新器件」,而 C8 就在 (440,535)。根因不是判据错,是**回读本身不成立**:
> 让 place 超时的连接器 wedge 同时让这一读没反映当前页面(旧快照 / create 还堵在队列里),
> 两者都只是**读得太早**。现在多一道门⓪:**本命令此前已成功放置并拿到 id 的器件必须一个不缺
> 地出现在这次回读里**,否则这一读什么都不算,判定降级为 `adopt ?`(uncertain)并进
> `PARTIAL STATE`,同时打印可执行处方(`sch save` → 完全退出重启 EasyEDA → `sch list` 查坐标 →
> 有就 `prim-delete`)。**读它的口径**:
>
> | 报文 | 含义 | 你要做什么 |
> |---|---|---|
> | `adopt ✓ …按 id … 收编` | 件已落地,句柄认回来了 | 无 —— 回滚/后续照常 |
> | `adopt ✓ …确实没有落地(强证据:…)` | **顺序可证**:那里确实没有新器件 | 无 —— 可以直接重跑 |
> | `adopt ✓ …确实没有落地(**弱证据**:…)` | 只有探针启发式支撑(连接器旧) | 可以重跑,但先看一眼报文里的升级提示 |
> | `adopt ? …无法判断` | **回读不可信**,落没落地都有可能 | 照处方去页面上查一眼,别盲重跑(会造重复件) |
> | `adopt ✗ …` | 没快照 / 回读失败 | 同上 |
>
> 🔒 **两级判定:算术优先,探针兜底(2026-08-20,连接器 FIFO 上线后)**。上面那道门⓪
> 之所以只能是启发式,根因是**写和随后的读之间原本没有 happens-before 关系**:连接器把
> 每条消息交给各自的回调,`await` 不跨回调排队,两条动作可以同时在飞(用真 transport
> 跑的探针实测:写还没 settle,读的 handler 已经开跑,响应也先发了出去)。连接器现在把
> 动作串成**一条 FIFO 链**,并在每条响应上带 `seq` / `seqAbandoned` / `unordered`,于是:
>
> - **算术档(强证据)** —— 比较「失败那次 place 之前观测到的 `seqAbandoned`」与
>   「收编回读响应上的 `seqAbandoned`」。**没变** = 这中间没有动作被放弃 = FIFO 保证这次
>   回读的 handler 在那次 place 的 handler settle 之后才开跑 → 「那里没有新器件」是真的。
>   **变了** = 有 handler 被丢下且**仍在跑、效果可能稍后才落地** → 一律 uncertain。
>   报文打 `证据档:可证(连接器 FIFO 顺序序号)`。
> - **探针档(弱证据)** —— 连接器比 CLI 旧、响应不带这些字段时,退回原来的探针启发式,
>   报文打 `证据档:弱(探针启发式)` 并给出升级连接器的步骤。**绝不会因为缺字段就默认
>   「新鲜」**。
> - **`unordered: true` 的响应永远不算证据** —— 那是连接器旁路通道(wedge 期仍可观测的
>   纯诊断读)的响应,它的 seq 与 FIFO 无关。
>
> 边界移动了:**第一件(anchor)就超时、没有任何探针**的场景,探针档结构上无解(那一刻
> 「没落地」与「读得太早」在观测上完全等价),但**算术档能出结论** —— 这正是它最大的收益。
>
> ⚠️ **`seq` 证明的是 handler 边界,不是「文档已提交」**。`eda.*` 可能在 handler 返回之后
> 才把改动写进文档模型,那一层没有任何观测点。所以「确实没有落地」的准确含义始终是
> **「在可证的最新一刻,那里没有新器件」**——报文原文就是这么写的,别在转述时把它说满。
>
> ⚠️ **残件删不掉 ≠ 未提交 ≠ 回读 stale**:同一份失败报文里**有回执的**器件也会
> `survived`——那是**连接器 action 队列 wedge**(某个重调用的 promise 永不 resolve)。
> 连接器 FIFO 上线后这件事有两个变化:(1) wedge **会自愈** —— 队首超过它自己的
> `timeoutMs` 就被放弃,队列继续流动(而不是像以前那样静默吞掉接下来几分钟的
> `place`/`delete`/`document.open`);(2) 被放弃过会体现在后续响应的 `seqAbandoned` 上,
> 所以那段时间的写**判定为 uncertain 而不是失败**。收到 `ACTION_ABANDONED` 错误码 =
> 这次写**没有已知的完成时刻**,盲重发就是在造重复件。处方不变:`sch save` → **完全退出
> 并重启 EasyEDA** → 再删。另见 `QUEUE_OVERFLOW`(队列积压到上限):那个动作**根本没有
> 执行**,等积压消化后重发是安全的。
>
> 🔢 **多页工程的位号真相(#144):** EasyEDA **页数据懒加载**——`getAll(_, allPages)` 只返回
> **本会话打开过**的页,没访问过的页对我们隐形,却照样参与平台自己的位号避让。曾因此规划 `C1`
> 落地却成 `C11`,而 wiring 仍拿 `C1` 去解析 → **跨页连到另一页的 C1 上**(netlist 按
> designator.pin 全文档索引),13 条连线全废且报出本页不存在的网络。现已双层兜底:预扫描
> `tagPages` 强制遍历各页把数据加载进来;放置后再**回读平台实际赋予的位号**,不一致就把
> placements / net members / `<INSTANCE>_N<i>` 内部网名一并 remap(manifest 里
> `designatorRenames` + 警告)。**内部网名必须跟着位号走**,否则两个实例的 `C1_N3` 会跨页同名合并。
> ⚠️ 由此推论:**任何按位号引用图元的批量流程,都别信规划值,以放置回读为准**。

Place **real parts from the EasyEDA / 立创(LCSC) library**, then wire them.
Hand-drawing a custom component symbol is the **fallback**, used only when the
part genuinely isn't in the library (a hand-built symbol loses the
footprint/supplier linkage and is error-prone — prefer a library part, even a
near-equivalent, first).

0. **Standard parts first.** Check [`standard-parts.json`](./standard-parts.json)
   (in this skill's `references/`) for the category you need (10k 0402, 100nF,
   ESP32-S3, AMS1117, USB-C, …). If it's there, place straight from its
   `{ libraryUuid, deviceUuid }` — deterministic, BOM-ready, with the real LCSC
   C-number. Only search when the category is missing, and ADD the chosen part back
   to `standard-parts.json` (with its C-number) so the next design is reproducible.
   When you already know the **exact C-number** (from a BOM or standard-parts.json),
   resolve it deterministically with `lib by-lcsc --lcsc C…` (`schematic.library.get_by_lcsc`)
   → `{libraryUuid, uuid}` ready to place, skipping free-text ranking. After a new
   selection, `scripts/parts-add.py` appends the resolved part into `standard-parts.json`
   so the curated cache grows (it reads the JSON `lib by-lcsc` / `lib search` emits).
1. **Search** (fallback) `schematic.library.search` (free-text: an MPN, value+package,
   or a name like `ESP32-S3-WROOM-1`). Results are **reranked by relevance** (best
   category first; each carries a `score`), so the right part usually leads — but
   still sanity-check `value`/`footprintName`/`lcsc` before placing. Each candidate
   carries `uuid`, `libraryUuid`, `name`, `footprintName`, `lcsc`, `manufacturerId`.
2. **Place** `schematic.component.place` with the chosen `{libraryUuid, uuid}` at a
   coordinate → a manufacturable part with correct symbol + footprint + LCSC number.
   ⚠️ **`--uuid` must be a DEVICE-library uuid** (from `lib search` / `standard-parts.json`),
   **never** one of the uuid-looking fields `component`/`symbol`/`footprint`/`uniqueId`
   that `sch list` reports — those are placed-INSTANCE ids and **cannot be replayed**.
   Feeding an instance uuid hangs the EasyEDA API; `sch place` now fails fast (~8s) with
   a hint instead of stalling 20s on `context deadline exceeded`. To re-place an existing
   part, run `lib search` again to get its device uuid.
3. **Read pins** (`schematic.components.list` / pin readback) for exact pin
   coordinates before wiring.
4. **Wire** (reference-validated — see **画线 / flag / 去耦(CLI 级硬规则)** in
   [`auto-layout-sop.md`](./auto-layout-sop.md);
   the 嘉立创 ESP32-S3 standard project is **flags only on power/ground rails; module-local
   signals use real wires and long/cross-module signals use named netports**):
   - **Module-local signals = real orthogonal wires** (pin→wire→pin). Endpoint on a pin coord
     = connected; non-aligned pins → L-route `[x1,y1, x2,y1, x2,y2]`. Use named netport
     stubs for long, cross-module, or cross-page signals.
   - ⚠️ **Never run a wire through another pin** — EasyEDA trims+connects it there.
     Route in pin-free channels.
   - ⚠️ **Multi-pin nets: chain pin→pin** (each segment anchored on a pin), NOT a star
     to a free junction (EasyEDA drops the un-anchored junction on merge).
   - **Flags ONLY for power/ground rails** (`connect_pin direction=`, never blanket rot 0).
5. **Verify each page** with `easyeda sch gate --strict --doc <page>` — one command runs
   layout-lint → check → bridge-check → drc in a fixed order and returns one verdict
   (`pass` / `fail` / `blocked`; `blocked` means a checker could not RUN, so the page was
   never judged — fix `health`/`doc switch` and re-run rather than editing the circuit).
   Then do a `sch read` comparison against the design spec or saved pin→net golden map:
   the gate proves the page is *legal*, only that comparison proves it is *correct*.
   The single checkers stay available for spot re-checks; the data linter
   (`scripts/lint.sh <project>`) is an additional check, not a replacement. ⚠️ After API edits the **EasyEDA canvas may not
   auto-redraw** → `getCurrentRenderedAreaImage`-class viewport captures return a STALE
   frame (even `view fit` framing is stale). **Judge STATE by data (`sch list`/`getAll`),
   use the screenshot for visual layout only**, and touch the page in EasyEDA (scroll/
   click) to force a redraw before trusting a snapshot. Pass the previous frame's `sha256`
   by preferring `sch export-image` (renders document data, viewport-free); a stale frame
   must not be trusted for verification.

## Bulk realization from a netlist (automated)

For a whole board (place ~N parts + wire the full netlist at once), the manual flow
above doesn't scale. Pipeline (proven on box-v2/110 parts):

1. **PLACE-ALL** — for each part, resolve `{libraryUuid, deviceUuid}`
   (standard-parts.json first, `lib.search` fallback), place at coords, then assign
   the designator (`sch modify --patch '{"designator":...}'` — place leaves it `C?`).
2. **READ-PINS** — ONE `sch list` / pin pull AFTER all placement for real pin coords
   (don't trust pre-place maps; map IC functional names → physical pads first).
3. **WIRE** — per net, decide flag vs local wire vs label (see the decision table in
   the SOP); emit flags via `connect_pin direction=` (never blanket rot 0).
4. **DRC + lint**, then a **MANDATORY clustering/zone pass** before "done".

> ⚠️ **Layout is NOT optional.** Naive place-at-synthesis-coords + flag-every-pin is
> electrically valid but **visually scattered** (box-v2: 327 flags, decaps far from
> ICs). **Follow [`auto-layout-sop.md`](./auto-layout-sop.md)**
> (`auto-layout-sop.md`): fit sheet → mains by zone → auxiliaries pin-relative to their
> owner IC → fine-tune. And **write resolved parts back into `standard-parts.json`** in
> the same change (so the next board doesn't re-search non-deterministically).
>
> **Churn-resilience for >~50 mutations** (essential, see the SOP): route by
> `--project` + `--doc`; batch with typed actions, `easyeda apply`,
> `scripts/bulk-place.py`, or `scripts/bulk-connect.py`; incrementally `sch save`;
> re-pull fresh primitive IDs each chunk. `debug.exec_js` remains a user-approved
> temporary fallback only when no typed action exists.
>
> ⚠ **exec_js 建线勿走 create+modify 两步**(#133 Bug 2 实录,Windows 桌面端):批量
> `sch_PrimitiveWire.create()` 后再 `modify(id,{line,net})`、紧跟 `sch save`,触发过**不可逆
> 画布状态损坏**(net 全丢、floatingPinCount 爆表)。`create(line, net)` **一步带 net** 创建,
> 或直接用 typed action(`sch connect`/`sch autoconnect`);批量 exec_js 落线后先 `sch read`
> 逐网验证再 save。另:查 API 真名用 `easyeda api search`——索引已按**运行时可调用名**归属
> (0.15.1 修复:此前 57 个带 implements 的类方法被错归到 `sch_Netlist`/`pcb_Net`,照抄会 undefined)。

## Pin-aware autoconnect — let the planner pick direction/offset

**已移至 [`schematic-wiring.md`](schematic-wiring.md)** —— 本节内容整体搬出，减少每次调用的上下文成本（RFC #178）。需要时读那个文件。

## 三层布局体系 — Sheet → Zone → Group(tidy + move 各层齐备)

**已移至 [`schematic-placement.md`](schematic-placement.md)** —— 本节内容整体搬出，减少每次调用的上下文成本（RFC #178）。需要时读那个文件。

## Actions

Run `easyeda actions` for the current machine-readable action list.

### 导航 / Navigation

**自助「发现 + 切换」闭环（首选）** — 不要让用户手动开窗口/切页,Agent 自己发现并切换:

```bash
easyeda daemon health                         # 发现:有哪些已连接窗口 + 各自实时上下文
easyeda doc ls     --project <名字>            # 发现:列出该窗口所有可开文档(原理图页+PCB),★=当前前台
easyeda doc switch <P2|PCB1|uuid> --project <名字>   # 切换:按页名/PCB名/uuid 切到前台,自动回读确认
```

- `easyeda doc ls` 聚合了 `schematic.pages.list` + `pcb.documents.list` + `document.current`,一条命令看全貌;`--json` 给机器读。
- `easyeda doc switch` 按名字解析 → `document.open` → `document.current` 回读确认。**同名页(多个 P1)会报歧义并列出 uuid,改传 uuid**。跨类型也行(PCB ↔ 原理图)。
- **多窗口时必须 `--project`(或 `--window`)**:`doc ls`/`doc switch` 不带目标时,只有「恰好一个窗口」才能自动命中;两个及以上窗口会报 `no EasyEDA connector is available`。同理,某窗口连接器正在重连(churn)的瞬间也可能瞬时报这个,重试即可。
- **`doc reload` 后必须先 `doc switch <uuid>` 重钉 context 再写(2026-08-19 真机实锤)**:reload(saved→closed→reopened)后 exec_js 的 JS context 仍挂在**已关闭的旧 tab**上,紧接的写(`prim-delete`/create/modify)会打进旧文档——**静默 no-op 但回执照样 ok**(同 4 个 id 逐个删、回执全 ok、复检原样;插一条 `doc switch` 后同样命令立即生效)。这是「exec_js context 不跟切页走」的 reload 变体,`--doc` guard 拦不住(guard 核对的是 daemon 视角的 document.current,名义上已是新 tab)。同病还有**读侧 stale**:mutation 后 `bridge-check`/`clusters`/`check` 可能读到旧几何——orphan-tree 判据在 mutation 后不 reload 就跑,会把刚建的合法桩线误判成孤儿树**引导误删**(真机踩过:删掉了 C7:1 刚补的连接)。口诀:**写完要判,先 reload;reload 完要写,先 switch**。

底层 action(需要细控时再用):

- `project.current` — 当前工程信息（uuid / name / teamUuid）
- `document.current` — 当前激活文档信息（uuid / tabId / documentType）—— **实时读取**,不是连接快照
- `document.open` — 按 UUID 打开任意文档（原理图页或 PCB），通用版切换入口
- `schematic.pages.list` — 列出工程内所有原理图及页面
- `schematic.page.open` — 按 UUID 切换到指定原理图页（等同于 `document.open`，保留兼容）

多窗口说明：EasyEDA 每个窗口对应一个独立的 connector（windowId）。`easyeda daemon health` 列出所有已连接窗口;**优先用 `--project <名字>` 路由**(windowId 重连会变),细控时才用 `--window <windowId>`。

> **上下文是实时的,不会卡在 `home`。** 两条刷新路径:① daemon 用每次 action 响应里的实时上下文刷新缓存;② 连接器 **v0.5.7 起,心跳(~3s)会主动重读当前文档,变了就推**——所以用户在 EasyEDA 里**切了 tab、什么命令都没跑**,`daemon health` 也会在 ~3s 内自己跟上。若 health 显示某窗口是 `home`,说明它的前台 tab 停在开始页/欢迎页,或那个窗口跑的是旧连接器(< v0.5.7)没连上。
>
> **UI 切页要双击**:单击只选中 tab、不打开文档;双击才真正打开,`document.current` 读到的是「已打开」的那个文档。
>
> **`connectorVersionOk: false`** = 该窗口加载的连接器版本与 daemon 不符(典型:开着的窗口跑着旧连接器代码,或连接器版本落后 CLI)。处理:**侧载**的 `.eext` 需重导与 CLI 同版的 GitHub Release 包;从[立创插件市场](https://jlc-ext.com/item/zhoushoujian/easyeda-agent-connector)装的可能已原地自动更新(但市场版可能**滞后** CLI,严格同版仍取 Release `.eext`)。无论哪种,都要**完全退出并重启 EasyEDA** 才能把新连接器加载进已开窗口(re-import / 原地更新都不刷新已开窗口)。`null` 表示版本号非 semver(dev 构建)无法判定。

### 原理图编辑

- `schematic.components.list` — `--include-bbox` 附带每个元件渲染范围 `{minX,minY,maxX,maxY}`(供布局推理);`--include-pins` 附带每脚 `{pinName,pinNumber,x,y,noConnected}`，并明确返回 `pinsAvailable:true`；SDK 读脚失败会返回 `pinsAvailable:false + pinsError`，不再伪装成 `pins:[]`。connectivity 导出还请求 `includeDeviceIdentity`：把放置实例的 16 位 uuid 解析成 `schematic.component.place` 所需的 32 位器件库 uuid，无法唯一解析时返回 `deviceIdentityError`。内部布局写门还会请求 `includeConnectivitySummary`，得到 active-page 的 wires/buses/netflags/netports/netlabels/shortSymbols fail-closed 计数。两个常规 flag 可与 `--all-pages` 叠加(输出会显著变大)。
- **`easyeda sch layout-lint`** — **布局自检**(治覆盖的机械真值)。拉 `components.list --include-bbox --include-pins`,Go 侧两两几何检查:**bbox 重叠 / 异件引脚重合 = ERROR**、**间距 < `--min-gap`(默认 2.54mm) / 锚点 off-grid / out-of-sheet = WARN**。生产过门使用 `--strict`，会把这些 WARN、缺失或畸形 bbox/anchor/pin 几何、旧 connector 无法证明 pin 读取成功的状态，以及“无可读 sheet、无法执行 out-of-sheet 判定”的状态一并升级为非零退出，避免 `0 overlap` 冒充布局已完成。
**注意 `layout-lint` 只判器件本体** —— 标签之间、标签压器件、标签探出图纸它结构上看不见,
那一半在 `sch clusters`(已进 `sch gate` 第 2 关)。zone-violation 判据已废弃:分区框从活体
模块 bbox 反推后,「件在不在自己的框里」是同义反复。**`out-of-sheet`(#180)**:器件 bbox 越出图纸边框内缩 12 单位后的可用区。此前**没有任何判据抓这个** —— 出图纸的件照样连线、netlist 照样对得上,只是印不出来(实测 block-apply 曾把件放到 x=-20 / y=880 而图纸 0..825,当时 lint 全绿)。判的是 **bbox 不是锚点**(锚点在框内、body 探出框外一样印不出来)。`sheetCheckStatus` 与 zone 同态诚实披露:读不到图纸 bbox 或 `--all-pages` 下都报 `unavailable` 并附原因,**`--strict` 下 unavailable 本身即阻塞** —— 「没检查」绝不许显得像「查了干净」。`--strict` 必须逐页运行，拒绝与 `--all-pages` 或 `--include-non-parts` 组合：非激活页是浅数据，sheet/marker 也不是器件放置体。`--min-gap` / `--pin-eps` 是毫米参数，CLI 会换算到原理图原生 `0.01inch` 坐标（`2.54mm = 10 raw units`）；JSON schema v2 的距离值带 `measurementUnit: "mm"`，点坐标和 `anchorGridRaw` 使用 `coordinateUnit/anchorGridUnit: "0.01inch"`。注意 v1 曾误把 raw 数值标作 mm，依赖旧实际数值的脚本应按 `schemaVersion` 迁移。诊断模式仍支持 `--all-pages`、`--json`。**默认只检真实器件(`componentType == "part"`)**:图框/标题栏(sheet)铺满整页、netflag/netport/netlabel 等非器件原语都会被自动排除,否则它们会与几乎每个器件误报重叠(见 issue #13)。需要诊断这些 bbox/spacing 时可在非 strict 模式加 `--include-non-parts`;off-grid 与 pin 完整性仍只验真实器件。摆放后跑它判覆盖/间距,比肉眼/截图可靠(截图可能 stale)。是 place→verify→adjust 闭环的输入。
- **`easyeda sch sheet-geometry`** — **图纸边界 + 标题栏 keep-out**(放置/布线规划器的统一几何源,issue #26)。读 `components.list --include-bbox` 里 `componentType == "sheet"` 的实测 bbox,按**长宽比**匹配已知模板(A 系列横/纵向 ≈ √2),在**右下角**按归一化比例切出标题栏(图框/明细表)子矩形;`schematic.titleblock.get` 的 `showTitleBlock` 隐藏时不输出 keep-out。返回 `{sheet, titleBlock, keepouts[], warnings[]}`,每项带 **provenance**(`known-template-ratio` / `fallback-ratio` / `none`),无法确定时只给 warning、不输出虚假精度。`--json`。规划器消费 `keepouts[]`(`{name,bbox,hard}`)即可,**不要再各处硬编码 A4 坐标**。比例表见 `references/sheet-templates.json`。
- `schematic.component.place`
- `schematic.component.modify` — 位置/位号/BOM 标志/自定义属性。`customAttributes` 是 SDK `otherProperty` 的兼容别名(二选一,不能同传);属性补丁与现有值**合并**后写入(不清空其他元数据),未知顶层键**前置拒绝**(SDK 会静默丢弃)。写后用新句柄回读,**分级语义(#151)**:全部生效=ok;**部分生效=ok + `result.{partial,applied,alreadySet,notApplied,addedKeys,propertiesBefore}` + warnings**(已应用子集留画布并照常 autosave;`sch modify` 子命令与 playbook 重放此时**按失败处理**,但裸 `easyeda call` 只有 stderr 警告、退出码仍 0,需自查 `result.partial`)。恢复注意:**重放 `propertiesBefore` 只能恢复被覆盖键的原值,本次新增键(`addedKeys`)merge 语义下删不掉**,要删须编辑器手工操作;`applied` 只计回读可证明的写入,期望值与原值本就相同的键归 `alreadySet`(写入不可证)。纯属性补丁无一可证明写入=报错(画布未变)。回读通道本身失败会降级 `verified:false` + warning 而非报错(报错会让 daemon 跳过 autosave,丢已落画布的变更)。**merge 语义对只含顶层字段的补丁同样成立(#175)**:平台 modify 对 `otherProperty` 是**整体重写**语义,曾导致 `{"supplierId":...}` 这类顶层补丁把全部自定义属性静默清空;连接器现在会回读现有自定义属性并在同一次 modify 里原样写回 —— 全保住时 `result.propertiesPreserved` 列出被连带重写但保留的键(+`propertiesBefore` 快照),平台仍丢的键进 `result.notApplied`(`partial:true`,CLI 非零退出),没有静默面。
- `schematic.component.delete` — **级联清理独占桩线/flag(ADR-0004 Decision 5)**:只挂在被删件引脚上的桩线树连同其上的旗随件删净,**共享树只断不删**(树还触别的器件就留下),结果带 `cascaded` 字段(回读证实的 wire/flag id 明细);payload `cascade:false` 退回旧行为。不再留「删器件不清理桩线」的幽灵连接。⚠️ 级联只针对被删件的附着物——与该件无关的导线/总线/图形不动,要真正清页仍用 `schematic.page.clear`。
- `schematic.page.clear` — **一键清空当前页**:删除所有页级 primitive(组件、网络标志/端口/标签、导线、总线、图形),默认保留图框 sheet(`--no-preserve-sheet` 连图框一起删)。`--dry-run` 只统计不删。返回各类型删除计数 `{deleted:{...}, total, deletedIds}`。**无 undo**,确认门控。生成→检测→清页→重试闭环用这个。生产流程必须先 dry-run、报告、等用户确认;清完再读回确认 sheet 仍在。CLI:`easyeda sch clear [--dry-run] [--no-preserve-sheet]`。
- `schematic.primitives.delete` — 按 id **跨类型**删除(组件/标志/导线/总线/图形都行),省略 `--ids` 则删当前选区(配合 `schematic.select` 做"全选→删除")。无 undo,确认门控。CLI:`easyeda sch prim-delete [--ids id1,id2]`(CSV,重复 id 自动去重——平台对含重复 id 的批次整批静默拒)。**图框守卫(2026-08-17 误删实锤)**:sheet 图元在 `sch list` 里是「无位号 @(0,0)」——与 PARTIAL 残件同脸,曾被残件清理误删,而平台没有重建图框的 API(丢了只能人工 UI 重放)。`prim-delete` 发送前自动比对活画布,命中 sheet 即拒;确认要删图框加 `--allow-sheet`。**清理残件前先看 componentType,别只看「无位号 + 原点坐标」**。**计数是回读验证出来的,不是请求数(#164)**:删完重新枚举各类目,`deleted`/`total` 只计真正消失的;有图元活下来则 `result.partial:true` + `survived`(按类目列 id),CLI **非零退出**。此前它把请求数当删除数上报,于是「删旧+重画」的 zone-draw 标签每轮都报干净、实则只加不减(P5 累积到 56 个)。**批量删不可靠已在工具内兜底(缺陷 3 已修)**:平台对大批量 delete 会静默 no-op 仍返 true(真机:zone-draw 批删旧框 survived=4、block-apply 回滚 deleted=false,**逐个删 100% 成功**)——zone-draw 删旧框/绘制回滚与 block-apply 回滚现已统一为「逐个删+回读证实+幸存者重试一次」,判定只信回读;agent 手工清理大批 id 时也照此办理:分小批或逐个 `prim-delete`,非零退出(partial)就按 survived 列表重试。**删组成员自动级联清组注册(缺陷 2 已修)**:`prim-delete` 删掉的器件若登记在虚拟组里,回读证实后自动从组注册表摘除(组删净则删组),不再留陈旧注册吃掉复用位号。**删除走通用图元类(#164 已修)**:`eda.sch_PrimitiveText.delete()` 只从内存/渲染索引摘除、**从不进持久化模型** —— 删完立即读=0、`sch save` 后=0,`doc reload` 后**原 id 全部复活**(矩形/导线不受影响,只有文本;文本的 `modify` 同样被丢弃,等于一经创建就冻结)。现已统一改走 `eda.sch_PrimitiveObject.delete(ids)`(跨类型、真落盘),`sch prim-delete` 与 `sch zone-draw --clear` 都已真机验过 reload 后归零。**幸存者会先 settle 复核一轮再定案(2026-08-19)**:连接器的存活判定是删完**立刻** `getAll()` 的,那一读可能采到尚未落定的快照 → 误报 `survived`,而 CLI 据此非零退出、人再删一遍空转。现在首轮报 partial 时,CLI 等一拍(400ms)对**幸存 id**重发一次删除,用第二次回执定案:已经没了 → 归 `notFound`、命令绿;真没删掉 → 照样非零退出并给出「`sch save` + 完全重启 EasyEDA」的 wedge 处方(stdout 上留的仍是**首轮**原始回执,最终判定看 stderr 与退出码)。**留下的判据教训**:立即回读**证明不了持久化**,凡是判断"删干净没有",判据是 `doc reload` 后再 `sch text-list`——这条对任何自研的 fail-closed 校验都成立(`zone-draw --clear` 当初就是这么一路报"cleared 6"、实则标签全在的)。
- `schematic.wire.create`
- **`schematic.group.move`**(`easyeda sch group-move --ids id1,id2,... --dx <mil> --dy <mil>`)——把一个器件和它周边的 stub 导线/flag **当一个整体刚性平移**,内部相对布局不变,只挪外框。⚠️ **不对接 EasyEDA 原生"组合"UI 字段**(2026-07-07 查证:该字段在 `ESCH_PrimitiveType` 里没有对应类型、`sch_PrimitiveComponent` 的 47 个方法里没有任何 getter/setter 碰它、也没藏在 `OtherProperty` 里——纯 UI 内部状态,扩展 API 完全读不到写不了)。这是**无状态虚拟分组**:每次调用都要传完整成员 id 列表,不记忆跨调用状态。器件走普通 `x/y` modify(id 不变);导线没有原地 modify,走删除重建(net/color/width/lineType 保留,**id 会变**,后续操作要重新拉 id)。`--ids` 解析走 `getAll()` 本地过滤而非逐个 `.get(id)`——刚创建的图元直接 `.get(id)` 可能瞬时 404(实测踩过),同批次 `getAll()` 能看到。用于「摆放一个模块后想整体挪位置微调」的场景,S3 布局调整阶段可用。**持久编组已可用**:`easyeda sch group create --members R1,C5,U2 [--name mcu-core]` / `list [--all-pages]` / `add` / `remove` / `ungroup` —— 平台无编组 API(真机探测:`eda.*` 零编组面、组件实例零 group/parent 字段),easyeda-agent 按 documentUuid 把组关系存进 workflow state(`~/.easyeda-agent/workflow/<project>.json`,同 zones claims 模式)。成员存**位号**(页内稳定;primitiveId 会 churn),`sch group-move --group g1 --dx 100` 时解析当前 id 并**自动展开附着物**(成员 pin 上的桩线 + 远端 netflag/netport,线树粒度同 disconnect;触碰非成员脚的线树=真连线,留在原地并报告)。**`--groups g1,g2,…` 把同块的多个子组当一个刚体集合、一次内核调用整体移动**——逐子组 move 会撕裂组间共享导线的老坑已根治,同块多子组一律用它。执行走 ADR-0004 统一 move 内核(见「三层布局体系」):失败自动恢复到快照重连,结构化 `moveReport`,判据是电气对账。**边界钳位是可见的部分应用(#151)**:位移撞图纸边/图签 keepout 会被收拢(只减不反号),此时 stderr 逐轴 WARN(撞哪个边、被钳掉多少),stdout 给一行机器可读的 `partial: {"requestedDelta":…,"appliedDelta":…}` 且绿勾行同时印 requestedΔ/appliedΔ;**任一被请求轴被钳到接近 0(|applied| < |requested| 的 10% 且 |applied| ≤ 5)= 未执行**——命令在动画布之前就拒绝、非零退出,出路是先挪走挡路对象或减小位移;位移经 snap 5 网格后 0 件被移动时打「⚠ no-op」而非绿勾。足额位移的输出与旧版逐字节一致。**`--max-attempts`(默认 3,仅 `--group`/`--groups`)**:同一个组连续 N 次得到同一个失败结果(位移被钳到 0 等)就在动画布之前停手并给结论;组本身比整幅可用区还大时,拒绝消息换成 `page-too-small` 那句真话(独立成页/拆页),而不是走不通的「减小位移试试」。`0` = 不限。同一位号只属一个组;组空自动删;`list` 标 stale 成员。**删器件自动级联清组注册(缺陷 2 已修)**:`sch prim-delete` / block-apply 回滚在**回读证实删除成功后**,把该位号从组注册表摘除(指向死位号的 role 一并摘,组删净则删组)——位号复用不会再被陈旧组吃掉;级联 fail-soft,失败只警告,可 `sch group list` 审计补清。**块溯源可手工恢复(缺陷 4 已修)**:`group create` 新增 `--block-id <块id> [--instance <实例>] [--roles ROLE=位号,…]`,写入与 block-apply 自动登记相同的溯源字段;组注册损坏(如曾被陈旧组吃掉)后手工重登即可恢复 `sch reconcile` 机械对账——**reconcile 需要 --block-id 加 --roles 两者**,只给 --block-id 记录溯源但暂不可对账(命令会提示);--roles 的位号必须是本组成员。`sch align`/`distribute` 对**部分覆盖**某组的选集硬拒绝(`--break-group` 显式放行);autolayout/autoplace-free 检测到组时警告(不保组内相对几何)。
- `schematic.netflag.create`
- `schematic.power.connect_pin`
- **`easyeda sch destagger`** — **marker-overlap 的修复侧**(#171;检测侧是 `sch check` 的 marker-overlap,#148)。`sch check` 早能检测、一直没法修:直接 `sch modify` 挪 netflag/netport 坐标会把标识从导线端点上**挪脱→断网**,所以实测一块 4 页板报出的 101 条纯视觉重叠长期"修不动",只能手工一个个拆了重连(2026-08-12 真实会话:6 处重叠,AI 临场猜 offset 30/40/50/70 改了三轮才收敛,中途还引入一次 `multi-net-wire` 短路)。四条安全原语:①**只搬两点直线短桩**上的 marker,挂在多段折线/网络主干/斜线上的一律跳过并带原因(`not-a-stub`/`stub-too-long`/`diagonal-stub`);②**带桩线一起挪**——走 `disconnect`(旗+桩一起删)→ `connect_pin`(按新方向/桩长重拉),**宿主端(pin 侧)坐标一字不动**,电气拓扑天然不变;③桩长候选**量出来**(跟着该旗 `flagTextBand` 文字带尺寸递增)并吸附 5 单位连接网格(= 连接器 `SCH_GRID`,不吸附则实际落点与规划差半格),方向按「电上地下」偏好序分配、rotation 走与 `reversed-net-flag` 判据**同一张真值表**;④落地后跑**真实 `sch check` 复验**,电气项任何一项变差就回滚并如实上报回滚是否干净(PARTIAL STATE 绝不谎报复原)。**`--apply` 已解禁(ADR-0004)**——执行统一走安全 move 内核:宿主器件不动,把宿主的桩线/旗**整树删净(删证回读)**后按新方向/桩长一遍性重连,再做网表逐 pin 对账 + bridge 增量检查,任一步失败**自动恢复到快照重连**并如实上报。当年三次三败的死因是逐根 `disconnect` 删桩线触发相邻共线导线自动合并 → 串网(缺安全执行层,非规划错);整树删净后器件身上没有任何导线,重建零合并风险。**dry-run 预览仍推荐**:先看计划,满意再 `--apply`。**挤不下时**宁可不动**(记 `no-free-slot`),不硬塞一个还撞的位置。默认 dry-run;`--json` 出完整计划(每个 skip 带原因);`--max-rounds N` 迭代(marker-overlap 归零提前收敛)。**跑满 `--max-rounds` 未收敛的判词分三档,出路完全不同**(#181):有进展 → 建议加轮数(**不说停手**)/ 一个都搬不动 → 停手换手段 / 被逐条跳过 → 按 `skips` 的理由处理。另有跨调用的 **`--max-attempts`(默认 3)**:同一页连续 N 次得到同一个失败签名就在动画布之前停手并给结论(`0` = 不限)。**单页作用域**(桩线只能从激活页读),跨页逐页 `doc switch` 后各跑一次。判据是「电气项不许变差」而非「必须全 0」——真实板本来就带着未 NC 标的 floating IO。
- `schematic.pin.set_no_connect` — 打/清「非连接标识」(NC, X 标记),让 DRC 不再对故意悬空的引脚报"未连接"。按位号+引脚号定位:`easyeda sch no-connect --designator U1 --pin 23,24[,…]`(`--clear` 清除)。实现必须从器件实例 `getAllPins()` 取引脚,`setState_NoConnected(...)` 后逐脚 `await pin.done()` 应用到画布,再重新获取器件实例回读;只调 setter 会得到当前句柄假 `true`、实际画布不变。
- `schematic.select`
- ~~`schematic.snapshot`~~ — 已移除(2026-08-12,出图统一 `sch export-image`)。**产物保存在 CLI 运行目录下的隐藏目录 `<cwd>/.easyeda/artifacts/`,文件名带本地时间戳**(`<YYYYMMDD-HHMMSS>-<kind>-<短id>.png`);响应里的 `artifacts[].path` 是绝对路径。netlist/BOM 等其他产物同此规则。
- **`easyeda sch zone-plan` 的失败分两种,别混**:①**装不下** —— 报「这一页装不下:<模块> 的框要 W×H,而可用区只有 W×H」并建议拆到单独一页(`sch page-new`)。**A4-only,不建议换纸**(用户裁定 2026-08-16:算法域固定 A4,平台也没有改图纸尺寸的 API;旧版曾推荐 A3/A2,已删)。块/虚拟组这一侧的同一判决叫 **`page-too-small`**(`block-apply` / `sch clusters` 报),与本条**共用同一把尺** `fitsAroundCorner`;②**摆得不好** —— 报「容量是够的,是摆放/间距问题」并指向 `sch zone-arrange --apply` 整页重排 / `sch group-move` 挪件 / 拆页(**不再指向 `--gutter`**:归组是「一区一框」之后 gutter 不参与分区怎么分,调它治不了重叠)。此前两者共用一句「adjust margins/gutter or the zone claims」,而对一颗 421 高的 WROOM-1 模组来说那是**做不到的建议**(A4 扣掉图签安全带只剩 541 可用高,框要 605),照着调只会白试一轮然后把整条判据当噪音。判据的价值不在报错,在报出**能执行的下一步**。容量判定刻意保守:只问单个模块自己塞不塞得进可用区,完全不管模块之间怎么排 —— 绝不会把「两个组顶在一起」误判成「该换纸」。

- **`easyeda sch status`** — **原理图侧的进度权威**:S1–S6 每一格**当场从画布算**,`--all-pages` 逐页测(切页读完切回),`--gate` 顺带逐页跑 gate 填上 S5。**不落盘任何状态** —— 立项动机就是记录会撒谎:`workflow status` 把 imported/placement_ready 打成实心圆,而那块 PCB 上一个器件都没有(它记的是「某个动作被调用过」,不是「结果还在画布上」)。原理图的 S1–S6 全部机械可判,所以干脆不存,**没有记录就没有可撒的谎**。四态:`✓ done` / `◐ partial`(部分页满足) / `○ todo` / **`? unknown`——本工具判不了,不是委婉的「没做」,更不会替它打勾**。三条硬规矩:①**有页读不到 → 整张判定降级 unknown** 并指向 `health`/`doc switch`(同 gate 的 `blocked`:检查器没跑完 ≠ 板子没问题;首版正是拿读得到的 1/4 页宣布「已就绪、进 PCB」被真机打脸);②**读取失败绝不合成 0**(导线读不到 ≠ 没有导线,否则故障被渲染成「还没连线」);③S5/S6 是有意留白:S5 要跑 gate,S6 平台不暴露脏标记(只能显式 `sch save` 确认 `saved:true`)。`next` 永远给一条可照抄执行的命令(页名占位时直接带上该页 uuid)。

- **`easyeda sch gate`** — **S5 校验门的唯一入口**:按固定顺序跑 `layout-lint` → `check` → `bridge-check` → `drc`,出一张报告。四个单命令原样保留(局部复查),但**交付门走 gate**。收敛动机见 `docs/design-sch-surface-convergence.md`:四个检查器各自为政时,「跑哪几个、什么顺序、谁的退出码算数」每次都要现场决定,而这个决策没有数据判据 —— audit log 实测 agent 对同一个失败拼过四种不同的下一步。现在顺序、阻塞判据、退出码都固化在代码里。**阻塞判据**:layout-lint `overlap`/`pin-coincidence` · check fatal+error · bridge-check `wire-bridge`(真短路) · drc fatal;tight spacing / orphan stub / 非 fatal DRC 项是告警,`--strict` 提升为阻塞。**顺序不是随意的**:几何最便宜且解释力最强(重叠会连锁出一堆电气误报,先治几何省掉大半来回),DRC 最慢且需前台故垫底。**verdict 三态,`blocked` ≠ `fail`**:`pass` 全过 / `fail` **板子有阻塞问题** / `blocked` **检查器没跑起来**(连接器断、页没打开、返回结构异常)——此时原理图**从未被完整判定**,后续 stage 直接跳过而不是继续撞同一堵墙,报告会指向 `easyeda health` + `doc switch` 而不是让你去改电路(旧行为下 agent 曾在 NO_CONNECTOR 后盲目改调别的命令 146 次)。每个失败 stage 自带**规定的下一步**,不用自己发明。`--json` 带每个 stage 的完整原生报告(`stages[].detail`),是四个单命令 JSON 的超集;`--only`/`--skip` 选子集(拼错 stage 名直接报错,绝不静默少跑一关);`--fail-fast` 第一个阻塞失败就停;窗口不在前台时 `--skip drc` 先过前三关。
- `schematic.drc.check` — 用 `easyeda sch drc` 跑 EasyEDA SDK 的 `sch_Drc.check`。**注意:当前 EasyEDA build 可能只返回布尔/聚合结果,不会暴露 UI DRC 面板里的逐条 warning**(例如网络标识与导线名不一致、悬空脚明细)。所以它只能作为 SDK DRC 门,不能单独宣称“官方 DRC 干净”。
- `schematic.check` — 用 `easyeda sch check` 跑的**重建式逐条设计检查**(补 SDK DRC 暴露不全)。**每条 finding 带 kebab-case 规则类型名 `type`(与 `pcb check` 同约定,可按类型统计/gate),summary 每类一个计数字段**。规则清单(全部 WARN):**floating-pin**(引脚悬空)、**geom-net-mismatch**(导线触碰引脚但网表未归入任何 net——疑漏报)、**net-marker-mismatch**(网络标识/端口/标签名与所连导线 net 名不一致)、**multi-net-wire**(同一导线多个网络名)、**wire-crossing**(导线交叉)、**wire-over-pin**(导线穿过引脚)、**zero-length-wire**(零长度残线)、**dangling-wire**(悬挂导线/孤儿 stub)、**polarity-convention-outlier**(极性约定离群,#183:同页「一脚电源+一脚 GND」的两脚电容多数派对电源侧 pin 号有约定,违背者即报——钽/电解反接曾带 51 条 WARN 过 gate 直到热失控;候选口径 C+数字编号,CN 端子/CR 二极管不入票仓;判据不需要领域知识,但 MLCC 引脚号本无极性,故 WARN 级、message 明示可忽略;保守阈值:样本<3 / 多数派并列 / 占比<75% 均不报,串联电容天然排除;`--all-pages` 下跨页池化成一个统计而非逐页约定,gate 默认单页不受影响)。**几何 marker 规则(Go 侧,消费 `components.list` 的真实 bbox/锚点,电气引擎看不见的三类,#146/#147/#148)**:**duplicate-net-marker**(同类型+同网+同锚点的重合 netflag/netport ≥2 个——批量 autoconnect 中断重试留下的重复 GND/电源/端口标识,连接器会把同名重合旗合并掉网,故所有电气规则全绿而页面叠着一对;finding 带全部 `primitiveIds` + `suggestKeepId`/`suggestDeleteIds`,直接喂 `sch prim-delete`)、**titleblock-overlap**(part/marker 的 bbox 侵入 A4 标题栏图签 keep-out——autoconnect 会把 netport 落进明细表而 layout-lint 只检 part、电气检查几何盲)、**marker-overlap**(marker body 正面积压住 part 或另一 marker——电气正确但不可读;`--overlap-eps` 默认 0.5 调噪声下限,平行同侧端口的 ~1 unit 天然相交仍会报。**修复走 `easyeda sch destagger`**,别手工挪坐标——直接 modify 会把标识挪脱导线端点导致断网,见下方条目)。`floating-pin` 现在带 `primitiveId` 与 `pinDetails[]`(每个悬空脚的 `number`/`name`/`x`/`y`),文本报告逐脚打印脚名+坐标、designator 为空时回退打印 `primitiveId`,可直接喂给 `sch no-connect`。`wire-over-pin` 会**排除落在导线端点或 netflag/netport/netlabel 锚点上的引脚**——那是 `sch connect` 短 stub 的合法终点(EasyEDA 把共线相邻 stub 自动合并成一条长导线时,内部引脚会落进合并后导线的内部,但官方 DRC 视为合法,故不再误报)。`--json`、`--strict`(有 finding 即非零退出)、`--all-pages`。
- `schematic.bridgeCheck` — 用 `easyeda sch bridge-check` 跑的**树粒度网络-铜皮一致性门**(补 `sch check` 逐 wire 检查的盲区:EasyEDA 把共线相邻异网 stub 合并成一条 wire 树后,单条 wire 不再同时带两个网名)。按共享顶点把 wire 并成树(union-find),聚合树上锚定的 netflag/netport 网名——**锚定按点到线段距离**(0.15.1/#135 修复:合并会把被吞 flag 留在线段**中段**,旧的顶点邻近判定永远锚不上,一树双网真短路曾漏报为 0)。规则类型(kebab-case,同 `sch check`/`pcb check` 约定):**wire-bridge**(一棵 wire 树带 ≥2 个网名 = 真实短路,ERROR,非零退出可 gate)、**orphan-stub**(树触碰引脚但无任何网络标识,WARN)、**orphan-flag**(netflag/netport 不挨任何导线,WARN——删合并线留下的孤儿,新画的线穿过该点会静默继承其网名,发现即 `sch prim-delete` 清掉)、**orphan-tree**(wire 树**不触任何引脚**:挪件残留的 flag+桩线整树、或裸死线,WARN——修法 `sch prim-delete` 整树 wireIds+flagIds 删净;**需连接器 ≥0.26.1**,此前 orphan-stub 要求触脚、orphan-flag 要求 flag 无线,对这种形态**双双结构性盲区**,2026-08-18 真机 P2 两棵 GND 残留树报了全绿,靠渲染图人工数 flag 才抓到 —— 怀疑有悬空标识而 bridge-check 全绿时,可交叉验证「某网 flag 数 vs 该网 pin 数」)。JSON 里每棵问题树带 `type`/`level`(`kind` 大写枚举保留兼容),summary 的 `bridges`/`orphans`/`orphanFlags`/`orphanTrees` 即按类型计数。`--json`、`--all-pages`。**注意:即便 check+bridge-check 双绿,布线后的最终判据仍是 netlist 逐网对账**(`sch read` 对拓扑,`sch block-apply` 已内建此对账门,不一致非零退出)。
- `schematic.read` — **一次拿到整张电路的语义快照**(`easyeda sch read`),省得分别跑 `components.list`+`netlist`+`check` 再自己拼。返回:`components[]`(designator/type/name 值/footprint/supplierId=LCSC/坐标 + 每脚 `{number,name,net}`)、`nets[]`(net→所连 `designator.pin` 列表 + `degree` + `isGlobal` 电源地标志)、`floatingPins[]`(未连脚)、`check`(同 `sch check` 的几何检查)。**脚→net 取自官方网表 `sch_ManufactureData.getNetlistFile()` 的 JSON,权威非几何猜测**,与 `sch check` 同源。`--all-pages`;`--no-check` 跳过设计检查更快。读电路状态/做决策前优先用它。**不要改走 `sch_Netlist.getNetlist()`**:官方 prodocs 已标 obsolete 并要求改用 `SCH_ManufactureData.getNetlistFile()`,且 [easyeda/pro-api-sdk#30](https://github.com/easyeda/pro-api-sdk/issues/30) 记录了它在含悬空引脚原理图上无限卡死。
- `schematic.save`
- `schematic.export.netlist` — 导出网表 artifact,底层同样只走 `sch_ManufactureData.getNetlistFile(fileName, netlistType)`。raw debug 需要网表时用:
  `const f = await eda.sch_ManufactureData.getNetlistFile('netlist.json'); return f && await f.text();`
- `schematic.export.bom`
- `schematic.library.search`
- `schematic.library.get_by_lcsc` — 用 `easyeda lib by-lcsc --lcsc C…`(可重复或逗号分隔多个)把 LCSC C 号**确定性**解析成 `{libraryUuid, uuid}`(免 free-text 排序),返回里带 `notFound` 列出未解析的 C 号。已知确切器件(BOM / standard-parts.json)时优先用它。

### PCB

PCB 操作（切到 PCB、读器件/层/网络/Board、从原理图 `import_changes` 同步、布局摆位
move/rotate/align/distribute/grid_snap/cluster-arrange）在独立的 operational skill
**[`pcb.md`](./pcb.md)** —— 见那里(单一真源,勿在此复制)。

## Bundled Scripts

| 脚本 | 用途 |
|---|---|
| `scripts/sch.py` | **稳定执行器**（import 用）— 把核心 CLI 封成 churn-resilient API:`read()`/`place()`/`move()`/`wire()`(SOP-W 正交避引脚)/`rail_flag()`(SOP-F 定向)/`decouple()`(SOP-D)/`connectivity()`(union-find 真连通)/`snapshot()`(取 .easyeda/artifacts)。AI 数据自调闭环用:放→`read`→判→`move`→`connectivity` 验。 |
| `scripts/lint.sh <project>` | 原理图数据 lint（几何 + 连通性检查，无需截图）。有 baseline 时显示 DIFF |
| `scripts/lint.sh <project> --save` | 全量 lint 并记录 baseline |
| `scripts/bom-enrich.py <bom.tsv>` | 将导出的 BOM 里 `SupplierId` 从 MPN 补全为 LCSC C 号。**`easyeda bom export --type csv` 已默认自动调用它就地补全**（`--enrich=false` 关闭）；本脚本仅在手动后处理已有 BOM 时单独用 |
| `scripts/parts-select.py` | 器件选型辅助工具 |

标准器件库（`standard-parts.json`）、flag 旋转真值表（`orientation.json`）、布局/选型约定都在
**easyeda-agent references** skill（单一真源，勿在此复制）。
`bom-enrich.py` / `parts-select.py` / `orient.py` 会跨 skill 自动读取这些 canonical 文件。

## Guardrails

- Confirm before deleting primitives.
- Save automatically at an already-defined passed stage and verify `saved:true`; pause first
  only when the user explicitly requested step-by-step approval.
- **幂等性**:`sch autoconnect` 幂等(重跑同 spec 安全,已连脚 skip,改网加 `--replace`);`sch connect`
  **非幂等** —— 重发前先 `sch read` 核对,否则在同一脚叠加 flag。
- **持久化:`place`/`wire`/`modify` 只改 EasyEDA 内存,不 `schematic.save` 就不落盘** —— 窗口重载 / daemon 重启 / EasyEDA 崩溃会丢掉未保存的改动(实测踩过)。daemon 默认开**防抖 autosave(3s)** 兜底(`daemon start --autosave-debounce`,`0` 关),但防抖窗口内进程挂掉仍会丢最后几笔,所以多步改动仍**分批显式 `sch save`**,别只靠 autosave。整板流程的存盘节奏见 [`design-flow.md`](./design-flow.md) 的 💾 检查点。
- Confirm before running a generated multi-step mutation plan.
- Do not claim completion after mutation until verification succeeds or the remaining risk is stated.
- Treat `File` and `Blob` outputs as artifacts.
- If DRC fails, report violations and propose the smallest repair step.

## Layout Conventions

### 原理图

When placing components, follow [schematic-layout-conventions.md](./schematic-layout-conventions.md):
- Zone map (power left, MCU center, RF/sensors right, big modules in corners)
- Module spacing rules (80–500 units depending on size + pin count)
- Wire stub lengths (20–40 units for power, 20–60 for signals)
- Right-angle-only routing, decoupling caps within 30 units of VCC pins

> **PCB 布局**约定在 [pcb-layout-conventions.md](./pcb-layout-conventions.md)，操作流程在 [`pcb.md`](./pcb.md) skill。

## EasyEDA Electrical Rules (load-bearing — DRC will fatal if ignored)

EasyEDA's DRC does **not** treat two primitives sharing the same coordinate as electrically connected. Every connection needs a real `schematic.wire.create` between them. Two concrete consequences:

1. **`schematic.netflag.create` MUST NOT be placed on the same point as a pin.** Placing a +3V3/GND/IN/OUT flag at the exact pin coordinate produces a DRC fatal: *"端点重叠且未连接 / endpoints overlap but not connected"*. The flag sits on top of the pin visually but EasyEDA treats them as two disjoint endpoints.

   Correct pattern: pin → short wire → netflag at the wire's far end. Typical offset: 20 grid units (EasyEDA uses 0.01 inch / grid unit on schematics). Example for `+3V3` on `R1.pin1 @(265, 440)`:

   ```text
   schematic.wire.create     points = [265,440, 245,440]   # pin to a free point
   schematic.netflag.create  x = 245, y = 440, kind=power, net="+3V3"
   ```

2. **Wires must have non-zero length.** A wire of `[x,y, x,y]` is silently ignored; a wire of `[x,y, x+0,y+0]` will not register a connection.

3. **NC pins still need explicit marking.** A pin without any wire/flag triggers a "悬空 / floating" warning even if your design intends it unused. Use a Non-Connected flag for those.

Apply this rule when generating any power/ground/port connection — emit the wire first, then place the flag at the wire's free endpoint.

## Missing Actions

When a needed operation has no typed action:

0. **Discover the underlying `eda.*` method first** — `easyeda api search <kw>`
   (offline, no daemon) ranks methods of the official API by name/namespace/中文摘要,
   `easyeda api ls [filter]` lists namespaces, `easyeda api show <ns>` dumps one
   namespace. Index is embedded from `@jlceda/pro-api-types`. This is the front of
   the dev loop `api search → debug.exec_js → typed action → Cobra 子命令`.
1. Decompose it into existing actions if possible.
2. Otherwise state the missing action name and expected inputs/outputs.
3. Use `debug.exec_js` (raw `eda.*` JavaScript) only as a temporary, user-confirmed debug escape hatch. Its result must be JSON-serializable — base64-encode any `Blob`/`File` inside the snippet.
4. Recommend promoting repeated debug code into a typed action.
