
# EasyEDA Schematic

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
6. Ask before destructive operations, multi-step mutation plans, or saving.
7. Summarize changed primitives, warnings, and artifacts.
8. If an official EasyEDA API is missing, undocumented, or differs from runtime behavior, record the evidence and workaround; when it affects correctness or maintainability, prepare a minimal repro and file an issue with the relevant official EasyEDA repository.

## Production preflight gates

- **Sheet first, default A4.** Before any whole-board placement/routing, run `easyeda doc ls`, switch to the target schematic page, then run `easyeda sch sheet-geometry --json`. If no `componentType:"sheet"` bbox is available, stop and ask the user to select/create the default A4 sheet in EasyEDA; do not place parts, wire nets, or run `sch autolayout --apply` on a sheetless page.
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
> 块若带 `schematic_layout` 模板,`sch block-apply` 直接按模板相对偏移+朝向落件(否则退回网格);
> 原点自动避开已有器件真实 bbox(显式 `--at` 优先),落完回读 overlap 写进 manifest——优先用
> `block-apply` 而不是逐件手放。
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
   the 嘉立创 ESP32-S3 standard project is **flags only on power/ground rails, every
   signal a real local wire**):
   - **Signals = real local orthogonal wires** (pin→wire→pin). Endpoint on a pin coord
     = connected; non-aligned pins → L-route `[x1,y1, x2,y1, x2,y2]`.
   - ⚠️ **Never run a wire through another pin** — EasyEDA trims+connects it there.
     Route in pin-free channels.
   - ⚠️ **Multi-pin nets: chain pin→pin** (each segment anchored on a pin), NOT a star
     to a free junction (EasyEDA drops the un-anchored junction on merge).
   - **Flags ONLY for power/ground rails** (`connect_pin direction=`, never blanket rot 0).
5. **Verify** with `easyeda sch layout-lint`(布局:覆盖/间距)+ `schematic.drc.check`(电气)
   + the data linter (`scripts/lint.sh <project>`). ⚠️ After API edits the **EasyEDA canvas may not
   auto-redraw** → `schematic.snapshot` / `getCurrentRenderedAreaImage` return a STALE
   frame (even `view fit` framing is stale). **Judge STATE by data (`sch list`/`getAll`),
   use the screenshot for visual layout only**, and touch the page in EasyEDA (scroll/
   click) to force a redraw before trusting a snapshot. `schematic.snapshot` now returns
   `primitiveCount` + `capturedAt` alongside the artifact — **compare `primitiveCount`
   across two adjacent snapshots: if it changed but the image bytes/sha did not, the
   frame is stale** and must not be trusted for verification.

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
> `--project`; batch many primitives per `debug.exec_js`; chunk each batch to <~20s
> (long calls die to the heartbeat); heavy-retry + incremental `sch save` per chunk;
> re-pull fresh pids each chunk.
>
> ⚠ **exec_js 建线勿走 create+modify 两步**(#133 Bug 2 实录,Windows 桌面端):批量
> `sch_PrimitiveWire.create()` 后再 `modify(id,{line,net})`、紧跟 `sch save`,触发过**不可逆
> 画布状态损坏**(net 全丢、floatingPinCount 爆表)。`create(line, net)` **一步带 net** 创建,
> 或直接用 typed action(`sch connect`/`sch autoconnect`);批量 exec_js 落线后先 `sch read`
> 逐网验证再 save。另:查 API 真名用 `easyeda api search`——索引已按**运行时可调用名**归属
> (0.15.1 修复:此前 57 个带 implements 的类方法被错归到 `sch_Netlist`/`pcb_Net`,照抄会 undefined)。

## Pin-aware autoconnect — let the planner pick direction/offset

`connect_pin` (`sch connect`) keeps the connection **safe** (pin → short wire →
flag/netport, never a netflag on a bare pin), but it still makes YOU pick
`--direction` and `--offset`, so layout quality depends on judgment. **`sch
autoconnect` removes that judgment**: it pulls the real geometry (part bboxes,
pin coords, existing flag/port/label bboxes, title-block keep-out), scores every
`up/down/left/right × offset` candidate with a deterministic cost function
(flag-collision / through-part penalties, shortest-offset + outward-side +
kind-default bonuses), picks the lowest-cost one, and delegates the mutation to
`connect_pin`. Same schematic state + spec → same selection (deterministic).

**批次内互斥 (issue #138):** 同一批(--spec / 多 --pin)里**已规划的短桩会当作
既存导线注册回 scene**,后续连接对它做同样的异网硬拒——同器件相邻异网引脚
(隔离 DC-DC 的 B0512S 类四域脚)不再出现短桩共线相触被 EasyEDA 合并成隐性
短路;规划器会自动换方向/offset 错开,四向全堵时按 #64 语义响亮报
"no safe candidate" 拒绝落笔。多域脚器件仍建议 power 上/gnd 下方向分治,给
规划器留出错开空间。

**Hard rejects (issue #64):** two hazards are never soft penalties — they make a
candidate *unusable* no matter the offset, because EasyEDA would silently merge
nets and the post-hoc DRC can't see it: (1) a stub whose endpoint or path touches
an existing **foreign-net wire** (endpoint-on-wire = junction = net merge), and
(2) a stub **crossing a non-target pin** (EasyEDA trims+connects there, and the
wire-over-pin rule exempts pin endpoints). autoconnect now pulls existing wire
geometry into the scene automatically; a wire already on the target net is fine
(that's the connection point). If EVERY direction/offset is a
hard reject, autoconnect refuses to place the stub and reports the connection as
failed — resolve the layout (move the part / clear the wire) and retry.

```bash
# single pin by designator:pin (number OR name)
easyeda sch autoconnect --pin U1:41 --kind gnd --net GND
easyeda sch autoconnect --pin U1:3V3 --kind power --net +3V3

# explicit coordinates (compat with existing flows)
easyeda sch autoconnect --x 720 --y 670 --kind gnd --net GND

# preview the plan + rejected options WITHOUT mutating
easyeda sch autoconnect --pin U1:41 --kind gnd --net GND --dry-run --json

# batch spec — clustered pins auto-stagger so labels don't stack
easyeda sch autoconnect --spec p1-connect.json

# re-run the SAME spec safely — pins already on the target net are skipped
easyeda sch autoconnect --spec p1-connect.json          # idempotent, no growth

# re-route pins currently on the WRONG net (delete old flag+wire, reconnect)
easyeda sch autoconnect --spec p1-connect.json --replace
```

**Idempotent (issue #50):** before connecting, autoconnect reads each pin's
current net (via `sch list --include-pins`, which now carries `net`) and classifies
every connection into three states — `new` (floating → connect), `already-connected`
(already on the target net → **skip**, no duplicate flag+wire), and `conflict` (on a
different net). A conflict is an error by default; pass `--replace` to delete the old
flag+wire (deleted **together**, so no orphan stub — see issue #51) and reconnect.
Re-running the same spec is therefore safe and never stacks duplicates. `--dry-run`
reports the three states without mutating.

Spec JSON (`--spec`): `{"connections":[{"pin":"U1:41","kind":"gnd","net":"GND"},
{"pin":"U1:3V3","kind":"power","net":"+3V3"}], "rules":{"avoidTitleBlock":true,
"avoidPinFanout":true,"staggerLabels":true,"offsetRange":[18,80],"offsetStep":6,
"minLabelGap":12}}`. Each result reports the `selected` candidate (direction /
offset / endPoint / score), the `rejected` alternatives with reasons, and the
`wirePrimitiveId` / `flagPrimitiveId`. The title-block keep-out comes from the
shared `sch sheet-geometry` derivation (issue #26) — when the sheet bbox isn't
exposed it is reported as **provisional** and not geometrically enforced (so a
guessed box can't corrupt scoring). **Prefer `sch autoconnect`
over hand-picking `sch connect --direction/--offset`** for power/ground/netport
stubs; `sch connect` stays for when you deliberately override the geometry.

## Module-aware autolayout — place parts by module zone

Where `autoconnect` is pin-level, **`sch autolayout` is module-level placement**:
it reads a `--spec` (page, sheet, modules with `zone`/`core`/`parts`, rules),
pulls the real geometry (anchors + bboxes + core pins + sheet bbox), partitions
the usable canvas into named zones (`left-top` / `left-bottom` / `center` /
`right` / `right-top` / `right-bottom` / …), places each module's **core IC near
its zone center**, fans the **peripherals around the core** with collision retry,
and keeps each core pin's **fanout channel** and the **A4 title-block** clear.
Same pure-scorer style as autoconnect: identical spec + input → identical
coordinates that pass `sch layout-lint`.

```bash
# preview proposed coordinates + warnings, mutate nothing (default)
easyeda sch autolayout --spec p1-layout.json --dry-run

# move parts via schematic.component.modify, then self-check overlaps
easyeda sch autolayout --spec p1-layout.json --apply

# structured report
easyeda sch autolayout --spec p1-layout.json --json

# platform FALLBACK engine (no spec): the official eda.sch_Document.autoLayout()
# @beta — a LONG op (~2min), rearranges the WHOLE active (foreground) page,
# connectivity-clustered/radial → messier than a template. For un-templated
# pages only; refine with `sch align`/`distribute` afterward.
easyeda sch autolayout --engine official --apply
```

**Engine priority (iron rule):** block hit → `sch block-apply` template; else a
`--spec` → `--engine template` (default); only when neither exists → `--engine
official` fallback. The official engine graduated `@alpha→@beta` on 3.2.148 and
now runs, but it produces the scattered generic-algorithm layout our research
predicted — never prefer it over a template for a known block.

Spec JSON (`--spec`):

```json
{
  "page": "P1_MCU_USB_STORAGE", "sheet": "A4",
  "modules": [
    {"name":"USB_HUB","zone":"left-top","core":"U10","parts":["J2","U10","X1","C30","R15"]},
    {"name":"MCU","zone":"center","core":"U1","parts":["U1","C18","C19","R6"]},
    {"name":"SD_NAND","zone":"right","core":"U8","parts":["U8","C28","R10"]}
  ],
  "rules": {"avoidTitleBlock":true,"preservePinFanout":true,
            "moduleGap":80,"routeChannelGap":40,
            "preferVerticalPeripheralPlacement":true}
}
```

The result reports each `placement` (designator / x / y / rotation / module), any
`warnings` (e.g. a peripheral forced into a fanout lane, or a spec part not yet
placed), and a `validation` summary (`partOverlaps` / `titleBlockHits` /
`fanoutKeepoutHits`). Notes:

- **v1 moves already-placed parts only** — it does NOT create missing parts; a
  spec part absent from the page is warned + skipped. Place the parts first
  (library-first), then `autolayout` arranges them.
- A **missing core** is a hard error for that module (clear diagnostic).
- When the **sheet bbox isn't exposed**, the title-block keep-out is reported as
  **provisional** and not geometrically enforced.
- `autolayout` solves **module placement, not routing** — follow it with
  `sch autoconnect` (power/ground/netport) + wiring, then `sch layout-lint` /
  `sch drc` to gate.

## Zone-less packing — `sch autoplace-free`

Where `autolayout` needs you to name zones, **`sch autoplace-free` finds the sheet's
blank space for you** and drops movable parts in, collision-free — the "把这些件塞进
纸面空白" case. Parts only (never wires/flags — that's `sch group-move`), so it's pure
CLI-side (reuses `components.list --include-bbox` + `component.modify`, no connector
handler). Deterministic top-left first-fit, anchors snapped to the 5-grid.

```bash
easyeda sch autoplace-free --dry-run                 # auto-pick messy parts, preview
easyeda sch autoplace-free --designators C1,C2,R4 --apply
easyeda sch autoplace-free --all --apply             # repack the whole page (tidy mode)
```

Move-set: **default** auto-selects parts currently OUTSIDE the usable area or
OVERLAPPING another (clean in-bounds parts stay put); `--designators A,B` targets
explicit parts; `--all` repacks everything. Fixed (non-moved) parts + the
title-block keep-out are obstacles it dodges. `--margin` (sheet-edge inset),
`--gap` (min edge-to-edge), `--grid-step`, `--no-avoid-titleblock`. `--apply`
moves via `component.modify` then self-checks with layout-lint. A big part on an
already-full page honestly reports **"no free slot"** rather than overlapping — use
`--all` so it gets first pick, or free up room. Verified live: 3 stacked parts →
`--apply` → 0 overlap.

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

- `schematic.components.list` — `--include-bbox` 附带每个元件渲染范围 `{minX,minY,maxX,maxY}`(供布局推理);`--include-pins` 附带每脚 `{pinName,pinNumber,x,y,noConnected}`(布线/连通性判断的数据面,布线前读引脚功能名→坐标用它,**不要**再用 `easyeda call schematic.components.list --payload '{"includePins":true}'` 绕过)。两个 flag 可与 `--all-pages` 叠加(输出会显著变大)。
- **`easyeda sch layout-lint`** — **布局自检**(治覆盖的机械真值)。拉 `components.list --include-bbox`,Go 侧两两几何检查:**bbox 重叠 = ERROR**(命令非零退出,可当门禁)、**间距 < `--min-gap`(默认 2.54mm)= WARN**。`--all-pages`、`--json`。**默认只检真实器件(`componentType == "part"`)**:图框/标题栏(sheet)铺满整页、netflag/netport/netlabel 等非器件原语都会被自动排除,否则它们会与几乎每个器件误报重叠(见 issue #13)。需要把这些也纳入检查时加 `--include-non-parts`;被排除的数量会在报告里以 `skippedNonParts` 透明列出。摆放后跑它判覆盖/间距,比肉眼/截图可靠(截图可能 stale)。是 place→verify→adjust 闭环的输入。
- **`easyeda sch sheet-geometry`** — **图纸边界 + 标题栏 keep-out**(放置/布线规划器的统一几何源,issue #26)。读 `components.list --include-bbox` 里 `componentType == "sheet"` 的实测 bbox,按**长宽比**匹配已知模板(A 系列横/纵向 ≈ √2),在**右下角**按归一化比例切出标题栏(图框/明细表)子矩形;`schematic.titleblock.get` 的 `showTitleBlock` 隐藏时不输出 keep-out。返回 `{sheet, titleBlock, keepouts[], warnings[]}`,每项带 **provenance**(`known-template-ratio` / `fallback-ratio` / `none`),无法确定时只给 warning、不输出虚假精度。`--json`。规划器消费 `keepouts[]`(`{name,bbox,hard}`)即可,**不要再各处硬编码 A4 坐标**。比例表见 `references/sheet-templates.json`。
- `schematic.component.place`
- `schematic.component.modify`
- `schematic.component.delete` — ⚠️ **只删组件,不删导线/总线/图形**。删完 `schematic.components.list` 只剩 A4 sheet 会让你误以为页面已干净,实际残留导线还在(DRC 仍会报)。要真正清页用 `schematic.page.clear`。
- `schematic.page.clear` — **一键清空当前页**:删除所有页级 primitive(组件、网络标志/端口/标签、导线、总线、图形),默认保留图框 sheet(`--no-preserve-sheet` 连图框一起删)。`--dry-run` 只统计不删。返回各类型删除计数 `{deleted:{...}, total, deletedIds}`。**无 undo**,确认门控。生成→检测→清页→重试闭环用这个。生产流程必须先 dry-run、报告、等用户确认;清完再读回确认 sheet 仍在。CLI:`easyeda sch clear [--dry-run] [--no-preserve-sheet]`。
- `schematic.primitives.delete` — 按 id **跨类型**删除(组件/标志/导线/总线/图形都行),省略 `--ids` 则删当前选区(配合 `schematic.select` 做"全选→删除")。无 undo,确认门控。CLI:`easyeda sch prim-delete [--ids '[...]']`。
- `schematic.wire.create`
- **`schematic.group.move`**(`easyeda sch group-move --ids '[...]' --dx <mil> --dy <mil>`)——把一个器件和它周边的 stub 导线/flag **当一个整体刚性平移**,内部相对布局不变,只挪外框。⚠️ **不对接 EasyEDA 原生"组合"UI 字段**(2026-07-07 查证:该字段在 `ESCH_PrimitiveType` 里没有对应类型、`sch_PrimitiveComponent` 的 47 个方法里没有任何 getter/setter 碰它、也没藏在 `OtherProperty` 里——纯 UI 内部状态,扩展 API 完全读不到写不了)。这是**无状态虚拟分组**:每次调用都要传完整成员 id 列表,不记忆跨调用状态。器件走普通 `x/y` modify(id 不变);导线没有原地 modify,走删除重建(net/color/width/lineType 保留,**id 会变**,后续操作要重新拉 id)。`--ids` 解析走 `getAll()` 本地过滤而非逐个 `.get(id)`——刚创建的图元直接 `.get(id)` 可能瞬时 404(实测踩过),同批次 `getAll()` 能看到。用于「摆放一个模块后想整体挪位置微调」的场景,S3 布局调整阶段可用。
- `schematic.netflag.create`
- `schematic.power.connect_pin`
- `schematic.pin.set_no_connect` — 打/清「非连接标识」(NC, X 标记),让 DRC 不再对故意悬空的引脚报"未连接"。按位号+引脚号定位:`easyeda sch no-connect --designator U1 --pin 23,24[,…]`(`--clear` 清除)。实现必须从器件实例 `getAllPins()` 取引脚,`setState_NoConnected(...)` 后逐脚 `await pin.done()` 应用到画布,再重新获取器件实例回读;只调 setter 会得到当前句柄假 `true`、实际画布不变。
- `schematic.select`
- `schematic.snapshot` — 截图。**产物保存在 CLI 运行目录下的隐藏目录 `<cwd>/.easyeda/artifacts/`,文件名带本地时间戳**(`<YYYYMMDD-HHMMSS>-<kind>-<短id>.png`,便于排序/查找);响应里的 `artifacts[].path` 是绝对路径。netlist/BOM 等其他产物同此规则。
- `schematic.drc.check` — 用 `easyeda sch drc` 跑 EasyEDA SDK 的 `sch_Drc.check`。**注意:当前 EasyEDA build 可能只返回布尔/聚合结果,不会暴露 UI DRC 面板里的逐条 warning**(例如网络标识与导线名不一致、悬空脚明细)。所以它只能作为 SDK DRC 门,不能单独宣称“官方 DRC 干净”。
- `schematic.check` — 用 `easyeda sch check` 跑的**重建式逐条设计检查**(补 SDK DRC 暴露不全)。**每条 finding 带 kebab-case 规则类型名 `type`(与 `pcb check` 同约定,可按类型统计/gate),summary 每类一个计数字段**。规则清单(全部 WARN):**floating-pin**(引脚悬空)、**geom-net-mismatch**(导线触碰引脚但网表未归入任何 net——疑漏报)、**net-marker-mismatch**(网络标识/端口/标签名与所连导线 net 名不一致)、**multi-net-wire**(同一导线多个网络名)、**wire-crossing**(导线交叉)、**wire-over-pin**(导线穿过引脚)、**zero-length-wire**(零长度残线)、**dangling-wire**(悬挂导线/孤儿 stub)。`floating-pin` 现在带 `primitiveId` 与 `pinDetails[]`(每个悬空脚的 `number`/`name`/`x`/`y`),文本报告逐脚打印脚名+坐标、designator 为空时回退打印 `primitiveId`,可直接喂给 `sch no-connect`。`wire-over-pin` 会**排除落在导线端点或 netflag/netport/netlabel 锚点上的引脚**——那是 `sch connect` 短 stub 的合法终点(EasyEDA 把共线相邻 stub 自动合并成一条长导线时,内部引脚会落进合并后导线的内部,但官方 DRC 视为合法,故不再误报)。`--json`、`--strict`(有 finding 即非零退出)、`--all-pages`。
- `schematic.bridgeCheck` — 用 `easyeda sch bridge-check` 跑的**树粒度网络-铜皮一致性门**(补 `sch check` 逐 wire 检查的盲区:EasyEDA 把共线相邻异网 stub 合并成一条 wire 树后,单条 wire 不再同时带两个网名)。按共享顶点把 wire 并成树(union-find),聚合树上锚定的 netflag/netport 网名——**锚定按点到线段距离**(0.15.1/#135 修复:合并会把被吞 flag 留在线段**中段**,旧的顶点邻近判定永远锚不上,一树双网真短路曾漏报为 0)。规则类型(kebab-case,同 `sch check`/`pcb check` 约定):**wire-bridge**(一棵 wire 树带 ≥2 个网名 = 真实短路,ERROR,非零退出可 gate)、**orphan-stub**(树触碰引脚但无任何网络标识,WARN)、**orphan-flag**(netflag/netport 不挨任何导线,WARN——删合并线留下的孤儿,新画的线穿过该点会静默继承其网名,发现即 `sch prim-delete` 清掉)。JSON 里每棵问题树带 `type`/`level`(`kind` 大写枚举保留兼容),summary 的 `bridges`/`orphans`/`orphanFlags` 即按类型计数。`--json`、`--all-pages`。**注意:即便 check+bridge-check 双绿,布线后的最终判据仍是 netlist 逐网对账**(`sch read` 对拓扑,`sch block-apply` 已内建此对账门,不一致非零退出)。
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
- Confirm before saving unless the user explicitly asked to save.
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
