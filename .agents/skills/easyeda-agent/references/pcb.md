
# EasyEDA PCB

Drive `easyeda-agent` typed actions. Run `easyeda actions` for the live machine-readable
list. 工程写入只允许参数化数据经 Cobra 子命令、typed action 或 `easyeda apply` 执行；
缺接口就补工具并验证，禁止用 `debug.exec_js`、GUI、CUA 或手工编辑兜底。

> **PCB design rules live in this skill's references** — especially
> [`pcb-layout-conventions.md`](./pcb-layout-conventions.md)
> (placement priority P0–P7, stackup-conditioned decoupling, thermal/SI/DFM/grid rules,
> each with a data-detectable check). This operational skill **links** to it — single
> source, never copy the rules here.

> **本文导航**:块的 PCB 约束(先查)· 坐标系与模型 · Workflow · Actions(Navigation / Board /
> View / Read·inspect / Routing / Copper pour / Keep-out regions / Filled region / Sch→PCB sync /
> Layout adjust)· Board outline(板框)· Auto-layout · Guardrails。

## 块的 PCB 约束(先查)

板上任何来自**电路块**的模块,其 PCB 约束在块里——`easyeda blocks show <id>` 读四张 map。做 PCB
前先把本板用到的块 show 一遍,把 `severity=must` 的约束抄进对应阶段:

- `placement` → **P2** 板边 / 朝向(edge/side/orientation;非对称连接器 USB/SD/IPEX 朝外,须用户确认)
- `pcb_layout` → **P2** 去耦/晶振贴脚距离(`*-adjacency`)· **P8** EP 热过孔/接地缝合(`ep-*`)·
  **P4** RF keepout / 巴伦镜像(`rf-*` / `balun-mirror`)
- `signals` → **P7.0** 差分 / 阻抗 / 等长
- `silk` → **P9** 逐脚标注

通用启发式布局会漏掉 CC1101 巴伦镜像、ESP32 模组 EP 热过孔、去耦 ≤2mm 贴脚这类块专属约束——design-flow
的 P 阶段会逐个引用,这里是提醒:**做 PCB 前先 show 一遍本板的块**。

## Coordinate system & model (load-bearing)

- **Data unit = `1 mil`** (schematics are `10 mil` / 0.01in — different). **y-UP**: +y renders upward.
- **Component `x`/`y` = the ANCHOR (footprint origin), usually NOT the bbox center** — and the anchor-to-center offset rotates with the part, so the mismatch is worst on rotated parts (#105). **Plan in bbox centers, write with center semantics**: `pcb list --include-bbox` returns each part's `center` `{x,y}` (bbox geometric center, CLI-computed) alongside `bbox`; `pcb modify --id <pid> --center --x <cx> --y <cy>` takes the DESIRED center and converts to anchor via the live bbox. `--center` refuses a same-call rotation change (rotating alters the offset the conversion reads) — rotate first, then `--center` in a second call. Raw `--patch '{"x":…,"y":…}'` stays anchor semantics.
- **显示原点不移动几何**：`easyeda pcb origin get/set --x <mil> --y <mil>` 读写画布显示 offset。
  设置前后用 `outline-get` 和一个器件坐标核对几何完全不变；迁移已有板时根据实际 offset 语义计算，
  不通过平移整板伪造左下原点。
- Every component is bound to a **layer** (`TOP` / `BOTTOM`). **No left/right mirror — only flip** (change layer via `pcb.component.modify`).
- **No programmatic undo.** Snapshot before/after into the audit log; pull a **fresh `primitiveId`** right before mutating.
- `pcb.component.delete` returns a boolean meaning *"operation completed"*, **not** *"actually deleted something"* — don't rely on it; verify with `pcb.components.list`.
- Layout actions (`align` / `distribute` / `grid_snap` / `components.move` / `components.arrange`) act on the **current selection** by default; pass `primitiveIds` to target a specific set. With nothing selected and no `primitiveIds`, they error (0 targets).

## Workflow

1. `easyeda daemon health` → confirm a connected window (route by `--project <name>`; `--window <windowId>` only for fine control). Context is live — refreshed on every action AND, with connector ≥ v0.5.7, pushed by the heartbeat within ~3s of a UI tab-switch (so health follows the UI even with no command run). `connectorVersionOk: false` flags a stale connector loaded in an open window (fully quit + relaunch EasyEDA).
2. `easyeda doc ls --project <name>` → see every openable doc (★=active). If the active doc isn't the target PCB, `easyeda doc switch <PCB-name|uuid> --project <name>` (cross-type PCB↔schematic works). **With 2+ windows open, `--project`/`--window` is REQUIRED** — without it the command only auto-targets when exactly one window is connected, else errors `no EasyEDA connector is available` (a momentary connector reconnect can also trigger this — just retry). (Low-level equivalent: `document.current` → `pcb.documents.list` → `document.open <pcbUuid>`.)
3. **Inspect before mutating**: `pcb.components.list` (`includeBBox`+`includePads`), `pcb.layers.list` (read `copperLayerCount`), `pcb.nets.list`, `pcb.board.info`.
4. 模块布局先以 `pcb dump` 保存实测 anchor/bbox/pads，再用 `pcb layout-plan` 离线生成候选；
   选择后经 `easyeda apply` 串行写入，禁止在现场逐件试摆。
5. 小批执行后回读对象与差异；稳定检查点显式保存，需要持久化证据时再做有界 reload/readback。
6. 删除、清板等不可逆动作只在用户已授权范围内执行并保留前快照；普通布局、保存和只读检查
   不依赖 workflow/stage、评分或人工签字。
7. Summarize moved/changed primitives, warnings, and artifacts.

## Actions

### Navigation

- `pcb.documents.list` — all PCB documents in the project (uuid + name); pair with `document.open`.
- `document.open` — open any document (schematic page or PCB) by uuid; the cross-type switch entry. `doc open/switch`
  now require both active-UUID match and a settled object inventory; a tab ID or URL change with unreadable objects returns
  failure instead of a false success.
  打开前通过当前 tab 查询官方分屏 ID，存在时显式传给宿主，避免关闭 PCB 后默认分屏
  定位挂起；查不到时保留宿主默认行为，不猜 ID。返回 `ready:true` 至少要求当前 UUID
  与目标一致；PCB 身份确认不代表器件数据已完成加载，后续仍需读取验证。
- `pcb.board.info` — current Board (schematic↔PCB linkage) + current PCB; the prerequisite context for `import_changes`.

### Board (板子/组合 — the schematic↔PCB binding)

A **Board groups exactly one schematic + one PCB** — that is how the two are kept
together, and what `import_changes` follows. Boards are identified by **name**, not
uuid. CLI: `easyeda board …`. Maps to `eda.dmt_Board.*`.

- `board.list` / `board.current` — all boards (name + bound schematic + pcb) / the current one. A board can hold only a PCB or only a schematic — the missing side is reported as `null`.
- `board.create` — bind a schematic and/or PCB into a new board (`--schematic` / `--pcb`). The fix for a floating/unlinked PCB before `import_changes`.
- `board.rebind` — repair a **stale/orphaned** Board binding (e.g. a rebuild-from-empty PCB left the Board pointing at a deleted schematic uuid, crashing `board list` and faking a DRC Netlist Error): deletes the old Board (by `--name`, else current) and re-creates it bound to `--schematic` (+ `--pcb`), rolling back on failure; `--force` to move a schematic already bound elsewhere. 曾被 daemon 挡成 `UNKNOWN_ACTION`(protocol 目录漏登记),现已可用——不必再走 `board delete` + `board create` 手工两步。CLI: `easyeda board rebind --schematic <schUuid> --pcb <pcbUuid>`.
- `easyeda pcb new-board` (`board.new_pcb`) — new board + fresh empty PCB page bound to a schematic. **A schematic belongs to only ONE board**, so this refuses if the target schematic is already bound (it would MOVE it out, orphaning the old board's PCB — the "原理图没了" trap). Work inside the existing board instead; pass `--force` only to move it deliberately.
- `board.rename` — rename a board (`--name` → `--new`).
- `board.copy` — duplicate a board (its schematic + PCB).
- `board.delete` — delete a board by name (**confirm** — no undo).

### View (canvas — shared with the schematic editor)

Act on the focused canvas; the editor view shortcuts. CLI: `easyeda view …`.

- `view.fit` — zoom to fit all primitives (适应全部, the `K` shortcut) → `easyeda view fit`.
- `view.fit_selection` — zoom to fit the current selection → `easyeda view fit-selection`.
- `view.zoom` — pan/zoom to a center coordinate and/or scale percent (`--x/--y/--scale`; omitted keeps current).
- `view.region` — zoom to a rectangular region (`--left/--right/--top/--bottom`, mil).

### PCB 文本参数

`silk-add` / `silk-set` 支持 `--font-family`（例如 Arial），`silk-list` 回传实际字体；
省略时 `silk-add`、`silk-netnames`、`silk-label-pads` 使用内置 `default` 字体和
`LEFT_TOP`（1）对齐，坐标为文字左上锚点。官方文本 API 会拒绝空字体名；
对齐值合法范围为 1–9，不能传 0。宿主常把这些参数错误统一包装成
“无法创建文本图元”，这不等同于连接断开或 PCB 未加载。

### Read / inspect

- `pcb.components.list` — placed footprints. `includeBBox` → per-component rendered extent (for overlap/spacing reasoning); via the CLI (`pcb list --include-bbox`) each bbox'd part also carries `center` `{x,y}` — the bbox geometric center, CLI-computed — use it (not the anchor `x`/`y`) when planning positions; `includePads` → pads + net、原始 `shape` / `rotation` / `specialPad`，以及支持形状的旋转后轴对齐 `width`/`height` 包络。`POLYGON` 和特殊焊盘不伪造尺寸。Connector ≥0.12.1；需要连通证据时必须按原始 shape 判断，不能把 bbox 当铜面积。
- `pcb.layers.list` — layers (id/name/type), `currentLayer`, and `copperLayerCount` (2-layer vs 4+-layer — gates the decoupling rules).
- `pcb.nets.list` — nets (`net` / `length` / `color`).
- `easyeda pcb net-path --from REF.PAD [--through REF.PAD] --to REF.PAD [--layer 1]`
  — 只读按支持的原始焊盘 shape 与 track/arc/via 构图，证明指定焊盘间的连续铜路径并回报层、线宽、
  过孔数；`--through` 是有序必经焊盘，`--layer` 是求路约束且会排除物理过孔。同网名不
  等于连通；未知焊盘几何、缺失圆弧回读或有序证明遇到重叠铜时返回 unknown/error。铺铜、填充和 PLANE 固定列入 JSON `excludedCopper`。完整范围与例子见
  [pcb-routing.md](pcb-routing.md)。
- `easyeda pcb outline-get` — 读取真实板框。名义尺寸用 `centerlineBBox` / `width` / `height`，
  不用包含描边的 rendered `bbox`。圆角 polyline 用 `sourceArcs` / `nativeArcs` 计数；
  `arcs` / `legacyArcs` 只统计旧式独立 Arc 图元。结果还返回 `radius`、`lineWidth`、`locked`。
- `pcb.report` — **read-only design report** driven by per-net copper length: every net's routed length, each **net class**'s aggregate length, **differential-pair** P/N lengths + `skew` (`|lenP−lenN|`), and **equal-length-group** per-net lengths + `spread` (`max−min`). No DRC run — the quantitative companion to `pcb.drc.check` for routing-quality gates (diff skew / length matching). Pure read.
- `easyeda pcb check` — **reconstructed DFM (design-for-manufacture) audit** — the PCB sibling of `sch check`, and the quality checks the native `pcb drc` (rule clearance) does NOT flag. Copper rules compute **purely Go-side** from placed copper (`pcb.line.list` + `pcb.via.list` + `pcb.components.list --include-pads`) and never mutate; the silkscreen rule reads `pcb.silk.list` (text layer + mirror + **reverse + rotation + fontSize**), the antenna rule reads `pcb.region.list` (region bbox + rule types) + component bboxes. Rules: **dangling-end** (a track end anchored to no pad/via/track → floating copper), **acute-angle** (两条同网同层线段在精确同网焊盘铜外形成 <90° 夹角 → acid trap；焊盘内的分支/终端连接豁免，未知形状不豁免), **non-orthogonal** (a single track off the 0/45/90/135° grid → free-angle routing, WARN — catches lazy pad-to-pad diagonals), **track-over-pad** (a track body crosses a pad center it doesn't terminate on, same layer: cross-net = **ERROR** short, same-net = WARN), **silkscreen-flipped** (a silkscreen text 放反 — three modes: a designator on the opposite silk layer from its component **ERROR**; a text whose **reverse** flag is set on either silk layer, or whose **mirror** flag is set on TOP silk, reads backwards **ERROR** — bottom-side `mirror` is deliberately NOT judged, its platform semantics are unverified and the in-repo sources disagree (the vendored reference boards ship `mirror=false`, `pcb silk-align` writes `mirror=true`), so either polarity would misreport; a reference designator (`key=="Designator"`) not reading **upright** — 180° upside-down / 90°·270° sideways — **WARN**), **overlapping-via** (two vias stacked), **single-layer-via** (a *signal* via that changes no layer — power/GND stitch vias are skipped, they connect to a pour not a track), **width-mismatch** (a 2-pin part with asymmetric neck-down → INFO), **duplicate-segment** (collinear overlapping redundant copper), **antenna-keepout** (an antenna component — ESP WROOM/WROVER module, an `ANT*` part, or a **discrete chip antenna** matched by device name `2450AT`/`ANT-SMD` (#123: auto-designators like AE1 defeated the ANT* test) — whose footprint lacks a no-copper keep-out region on **every** copper layer → WARN, naming the missing layer; copper under an antenna detunes it. Requires top (L1) + bottom (L2) no-copper regions, plus the inner planes via `no-inner-electrical` on 4+-layer boards — a top-only keep-out still lets the bottom pour fill under the antenna), **netless-pour** (a copper pour bound to **no net** — dead copper that occupies board area but connects nothing, issue #34; arises from `pcb pour` without `--net`, or pouring directly on a flipped PLANE layer → WARN, remove with `pcb pour-clean --netless`), **via-crosses-plane** (a via whose net differs from an inner **PLANE/内电层**'s net, issue #30 — official bug [easyeda/pro-api-sdk#32](https://github.com/easyeda/pro-api-sdk/issues/32): a via created **after** the plane exists gets **no anti-pad** cut into the negative plane, DRC reports Plane Zone to Via / Hole to Plane Zone and `pour-rebuild` alone doesn't repair it → WARN with fix guidance: prefer removing the via and routing on outer layers, or `easyeda doc reload` then `pcb pour-rebuild`, then confirm with `pcb drc`. Reads the stackup via `pcb.layers.list` (`type=="PLANE"`) + plane nets from `pcb.pour.list`. **Best-effort**: the API exposes no anti-pad/creation-order data, so a via placed *before* the plane flip — proper anti-pad, clean DRC — is flagged too; treat `pcb drc` as the arbiter of which flagged vias are actually broken. A PLANE layer with **no net-bound pour visible** gets its own **INFO** (not WARN, not `--strict`-gated — issue #110: after `doc reload` a PLANE-layer pour is loaded into the negative-plane store and becomes **invisible to `pcb.pour.list`**, with no extension-API read path, so "plane net unknown" is usually a reload artifact, not a defect; treat `pcb drc` Connection=0 as the arbiter before adding any pour — blindly re-pouring stacks duplicates. If the plane is genuinely empty: pour while the layer is SIGNAL, then flip), dangling-end anchors a track endpoint by **via area** too (a same-net endpoint anywhere inside the via copper counts as anchored — track↔via conducts on its own; the former **via-bond** ERROR rule that flagged bare track↔via junctions was removed after [pro-api-sdk#31](https://github.com/easyeda/pro-api-sdk/issues/31) proved to be our misdiagnosis — the "floating" symptom was stale pour connectivity, fixed by `pcb pour-rebuild`, not by fills), **floating-track-island** (a connected **group** of ≥2 tracks/vias in which no endpoint anchors to any pad — dangling-end's blind spot, members anchor each other → WARN listing all member ids for `pcb track-delete`; islands under a same-net pour are exempt), **power-not-poured** (a power/GND net with ≥2 pads that has **no same-net pour and is bound to no PLANE** → WARN — power should be delivered by copper area, not thin tracks, the #1 DRC source; fix `pcb pour-fit --net N` on 2-layer / `pcb power-planes` on 4-layer; single-pad nets and already-poured nets are exempt. **#117 nuance**: when the board carries an inner **PLANE layer with unknown net** — its pour is platform-invisible after `doc reload`, #110 — a GND-class finding degrades to **INFO** (non-blocking, not `--strict`-gated): that plane almost certainly IS the GND pour, so verify with `pcb drc` Connection=0 instead of re-running `power-planes`), **width-under-spec** (a routed **power** track thinner than its net-class spec width — 公制圆整阶梯 branch 0.25mm / trunk 0.4mm / high-current 0.5mm (≈9.84/15.75/19.69mil, 规范 §1.2), see `pcb net-classes` → WARN, one aggregated finding per net with the thinnest offender; **fine-pitch narrowing and via-stitch stubs are exempt**, and signal nets are not checked since their spec is the live default and fine-pitch narrowing is legitimate), **silk-over-pad** (silk text whose estimated extent covers a same-side pad — fab clips silk on exposed copper → WARN; fix with `pcb silk-align`/`pcb silk-set`; text extent from string length × the REAL `fontSize` (40mil fallback), pads tested against their real width/height, 规范 §11.2), **decap-too-far** (a 2-pad C\* with one pad on a power rail + one on GND sitting >100mil/2.5mm from the nearest same-rail U\* pin → WARN — a decap must hug its IC ≤2mm; rails with no IC pad (bulk/input caps) and signal-signal caps are exempt, 规范 §3.1), **via-in-pad** (a **same-net** via ON a pad center → WARN — solder wicks down the barrel AND this project proved via-on-pad ≠ connected; offset with a dog-bone stub; cross-net via↔pad stays the clearance rule's ERROR, 规范 §2.3), **copper-near-edge** (routed track/via copper within the live copper-to-edge rule of the board-outline bbox — fallback 8mil routed edge → WARN, aggregated per net with the worst offender, 规范 §5.1; needs `pcb.outline.get`, skipped without an outline), **fiducial-missing** (an SMT-scale board — ≥30 top pads — with <3 `FID*`/`MARK*` fiducial parts → **INFO** only, since JLC panel rails add their own marks; local marks matter for fine-pitch, 规范 §9), **zone-violation** (#126: a part claimed by a `pcb zones set` functional-zone module whose bbox center sits **outside its zone's board sub-rectangle** → WARN with the module/zone named, 规范 §3.3 模拟/数字分区 — the S0 spec's partitioning decision finally verified at P2; only runs when the project has zone claims, and an edge-bound part on the wrong side keeps getting flagged until the claim or the edge assignment is fixed). 规范 §refs point into `docs/pcb-design-rules.md` (the fact-standard手册 the check messages cite). `--json` for the full list; `--strict` exits non-zero on any WARN/ERROR (gate-able). Complements `pcb layout-lint` (placement/routability) + `pcb drc` (rule clearance). Arcs are out of scope for v1 (line/via/pad only; auto/short-routed copper is line segments); through-hole cross-layer track-over-pad shorts are a known blind spot (pad layer reported per side). Core + tests in `internal/app/pcb_check.go`.
- `easyeda pcb drc` (`pcb.drc.check`) — native rule-clearance DRC, normalized to `{passed, violations}`. **`--json` flattens** the panel's nested tree into one row per violation `{rule, objType, ruleName, net, x, y, layer, objs, message}` with **x/y in real mil** (raw leaves store mil/10 — the flattener owns the ×10) — pipe to `jq`, feed `objs` ids straight into `pcb via-delete`/`track-delete`. **`--timeout <s>`** (default 60) bounds the wait AND is forwarded to the daemon, which answers with a structured error *before* the HTTP client gives up. ⚠️ **Foreground constraint**: a background/occluded EasyEDA window **never finishes** the DRC canvas recompute — on timeout, bring the window to the FOREGROUND and run **once**; do **not** retry in a loop (each retry piles another recompute onto the webview). The daemon enforces this: a second `pcb drc` on a window whose first hasn't settled is rejected immediately (`ACTION_BUSY`).
- `pcb.drc.rules` — read the active PCB's **DRC rule configuration** (clearances, track widths, via sizes, …) **without running a check**. Use to feed real rule values into layout reasoning / gates, or to see what `pcb.drc.check` enforces. Safe spacing is an **object-pair matrix**, not one scalar: the daemon preserves Track↔Track separately from Track↔Pad/Via, so `pcb check` judges each pair by its own live rule (the standard double-layer table is 4mil vs 6mil). Routing still uses the binding Track↔Pad value. The normalized rule set also carries track widths and via sizes in mil (`internal/app/pcb_rules.go`).
- `easyeda pcb net-classes [--json]` — print the daemon's **heuristic role→spec-width guide**: `signal` / `power-branch` / `power-trunk` / `high-current` / `gnd`. It classifies by net name/voltage and helps `route-short` / `pcb check`; it does **not** prove an EasyEDA native class exists.
- `easyeda pcb net-class list/create` — read or create the **real persisted EasyEDA net classes** and their net/rule associations. Use singular `net-class` for requirements such as `PWR_Class`; create, save/reload, then list and compare members. Do not substitute the heuristic plural report.
- `easyeda pcb config get/clearance/track/via/bind` — 参数化修改真实设计规则，支持 mil/mm、dry-run、保留未指定字段与写后回读；考试数值、PWR 绑定及能力边界见 [pcb-config.md](pcb-config.md)。创建网络类后仍需显式绑定规则。
- `easyeda pcb drc-rules-set --from <rules.json>` — write a complete edited copy of the current rule configuration, then re-read it. `--from` accepts the full JSON envelope emitted by `easyeda pcb drc-rules`, a top-level `{name,config}` wrapper, a bare host config, or an explicit `{ruleConfiguration,netRules}` spec; the connector writes only the bare `config` required by Web 3.2.203. This is the path for track clearance/default/minimum width, via outer/hole limits, per-class rules and other full-rule changes. `--dry-run` compares without writing. The compatible `--pour-clearance <mil>` shortcut still patches only pour/plane clearance and remains raise-only; follow copper-clearance changes with `pcb pour-rebuild`. A write on an immutable system preset (`JLCPCB Capability(...)`) creates a per-board `自定义配置` copy — expected.
  > **Fresh-PCB trap — the rules snapshot**: a PCB document **created in the current session and never reloaded** computes pour reflow from a **creation-time rules snapshot** — rule writes (readback shows them!), `pour-rebuild`, and tab-switching away/back all have NO effect on the reflow. Only a real close+reopen (`easyeda doc reload` — saves first, no edits lost) refreshes it; after the reload, `pcb pour-rebuild` reflows under the live rules (clearance AND thermal spokes). Already-reloaded documents (e.g. any board that survived an EasyEDA restart) honor rule writes immediately. The esp32-mini playbook encodes the full recipe: `rules-pour-margin` → pours → `reload-pcb` (`doc reload`) → `pour-rebuild-2`; verified on a fresh board: DRC 55 → **1** (remainder = the known add-component netlist false positive).
  > **Raw-API trap** (if scripting rules via `debug exec` instead): `eda.pcb_Drc.overwriteCurrentRuleConfiguration()` takes the **BARE config content** — `getCurrentRuleConfiguration()` returns `{name, config}`, and passing that whole wrapper **silently no-ops** (resolves `undefined`, readback unchanged). Pass `cfg.config` → returns `true`.
  > **Fab-rule baseline: [`fab-rules-jlcpcb.json`](fab-rules-jlcpcb.json)** — the canonical JLCPCB fabrication capabilities (min trace/space, via drill+pad, annular ring, copper-to-edge, silk, by layer count + copper weight), captured from JLCPCB's published capabilities. JLCPCB is the fab behind EasyEDA Pro, so a live board's `pcb.drc.rules` converges with this file's **recommended** column (verified on ceshi: clear 6mil / width 10mil / via 0.3–0.6mm). **Always prefer the live rule; use this JSON as the fallback seed + as clamp floors** (never emit a track/via/gap below the `manufacturingMin`). The **`boardTypeRulesLive`** section holds the AUTHORITATIVE real per-board-type rules exported from JLCEDA (single / double / multi-layer / metal-core), fingerprint-classified + confirmed against named exports — `defaultPcbRules` uses the **doubleLayer** row (clear 6 / width 10 / min 5 / via 0.3–0.6mm / copper-to-edge 10). Controlled impedance is intentionally omitted (not derivable from platform data — see task #27).

### Routing (copper tracks + vias)

**已移至 [`pcb-routing.md`](pcb-routing.md)** —— 本节内容整体搬出，减少每次调用的上下文成本（RFC #178）。需要时读那个文件。

### Schematic → PCB sync + component CRUD

**已移至 [`pcb-layout.md`](pcb-layout.md)** —— 本节内容整体搬出，减少每次调用的上下文成本（RFC #178）。需要时读那个文件。

## PCB mutation 后的读取与 `staleRisk`

首次把系统规则转为自定义配置时，Web 4.1.60 曾出现未请求孔间距 `0.3 → 0.2999994 mm`
变化，见 [bug #258](https://github.com/zhoushoujianwork/easyeda-agent/issues/258)。该差值很小，
但仍保留完整差分和 `verified:false`，不能自动放宽容差；已冻结自定义配置的修改/恢复需独立核对。
当前保留规则命令，首次初始化路径仍待定位。

Web 4.1.60 的区域名称仅适用于 `follow-rule`。dev.18 曾给 `no-pours` 传名称并
回显输入，误把它当作已保存；dev.19 起拒绝该组合，创建后回读真实名称、层、规则和
几何。无名称禁布区与具名规则区均已保存重载验证，详见 [区域契约](pcb-routing.md#keep-out--rule-regions-禁止区域)。

dev.18 对过期 PCB 器件 ID 的修改曾触发宿主 `isAsync` 异常；dev.19 起先读当前板
器件清单并在 ID 缺失时写前拒绝。下方“过期器件 ID”说明当前处理方式。

改完铜再读,读到的是**旧引擎状态**:每个 PCB 文档有自己的枚举缓存,
rip-up / route / delete / via / track / pour 这类 mutation 之后,
`pcb list` / `line.list` / `via.list` / `pour.list` / `nets.list` / `drc.check` / `report`
都可能返回 mutation 之前的画面,直到文档被真正关闭重开。

daemon 不再拒绝这种读取，而是在响应和 CLI stderr 中附加非阻塞 `staleRisk`。即时结果可用于
定位明显差异，但不能单独作为持久化或最终验收证据。例如：

```
⚠ staleRisk: pcb.components.list 发生在 pcb.line.create 之后、文档尚未 reload；宿主枚举可能仍是旧状态。
```

- 最终可信读取：`pcb save → easyeda doc reload ... → readback`。确定性复位示例为
  `rip-up → save → reload → track-list/check/drc`。
- 铜面或 via 变化后按需要先 `pcb pour-rebuild`，再读取连通并运行 DRC；它修复的是铺铜连接，
  不能代替保存重开对持久化的证明。
- `pcb.documents.list` / `pcb.board.info` 只读文档身份和 Board 绑定，可随时用于定位目标。
- `pcb save`、`pcb pour-rebuild`、`--dry-run` 和视图操作不产生需要枚举刷新的几何写入。
- `pcb snapshot` 也可能带 `staleRisk`；截图发白或视口未刷新时先把窗口切到前台。截图只作辅助。
- `--force-stale-read` 作为旧脚本兼容参数保留；正常流程无需使用，它也不会让缓存数据变成权威结果。

## Guardrails

- 删除、导入或批量重排只在当前任务已授权的范围内执行；已有授权不重复询问。
- 每个稳定检查点显式保存并验证 `saved:true`；用户明确要求逐步确认时再按其节奏暂停。
- Do not claim completion after a mutation until readback / DRC verifies it (or state the remaining risk).
- No undo — record before/after into the audit log so a move can be reversed by re-applying the old coordinates.
- Treat `File`/`Blob` outputs (gerber/pick-and-place/3D) as artifacts.

### Windows PowerShell JSON 补丁（#192）

`pcb modify --id <pid> --patch-file patch.json` 从 UTF-8 文件读取 JSON 对象，支持 UTF-8 BOM，
绕过 PowerShell 5.1 传递原生程序参数时剥离 JSON 引号的问题。`--patch-file` 与 `--patch` 互斥。
PowerShell 可用 `'{"rotation":90}' | Set-Content -Encoding UTF8 patch.json` 创建补丁文件；
不要使用默认输出 UTF-16 的 `Out-File`。
原理图的显式 `--x/--y/--rotation/--designator` 仍覆盖文件中的同名键；
PCB 的 `--center` 仍不允许补丁包含 x/y/rotation。

### 过期器件 ID

`pcb modify` 在写入前读取当前板器件清单，ID 不存在时返回 `PRECONDITION_REFUSED`，
清单不可读则停止；不要把上次回包的 ID 当作永久有效。拒绝后重新 `pcb list`，
确认工程、页面与位号，再根据当前 ID 决定是否仍需要修改。

`pcb add-component` 在绑定位号/uniqueId 后重新读取器件清单，返回实际位号与链接键；
`bindingVerified:false` 或 `partial:true` 表示绑定未验证，CLI 非零退出并保留已创建 ID。
`bindingVerified` 只核对明确请求的身份字段，不代表焊盘网络已验收；未请求的字段只报告回读值。
不要因回包失败重复放置，先核对该 ID。
