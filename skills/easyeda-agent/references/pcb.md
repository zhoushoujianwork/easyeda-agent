
# EasyEDA PCB

Drive `easyeda-agent` typed actions. Run `easyeda actions` for the live machine-readable
list. Prefer typed actions; only fall back to `debug.exec_js` when a typed action is
missing **and** the user explicitly accepts a debug path.

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
- Every component is bound to a **layer** (`TOP` / `BOTTOM`). **No left/right mirror — only flip** (change layer via `pcb.component.modify`).
- **No programmatic undo.** Snapshot before/after into the audit log; pull a **fresh `primitiveId`** right before mutating.
- `pcb.component.delete` returns a boolean meaning *"operation completed"*, **not** *"actually deleted something"* — don't rely on it; verify with `pcb.components.list`.
- Layout actions (`align` / `distribute` / `grid_snap` / `components.move` / `components.arrange`) act on the **current selection** by default; pass `primitiveIds` to target a specific set. With nothing selected and no `primitiveIds`, they error (0 targets).

## Workflow

1. `easyeda daemon health` → confirm a connected window (route by `--project <name>`; `--window <windowId>` only for fine control). Context is live — refreshed on every action AND, with connector ≥ v0.5.7, pushed by the heartbeat within ~3s of a UI tab-switch (so health follows the UI even with no command run). `connectorVersionOk: false` flags a stale connector loaded in an open window (fully quit + relaunch EasyEDA).
2. `easyeda doc ls --project <name>` → see every openable doc (★=active). If the active doc isn't the target PCB, `easyeda doc switch <PCB-name|uuid> --project <name>` (cross-type PCB↔schematic works). **With 2+ windows open, `--project`/`--window` is REQUIRED** — without it the command only auto-targets when exactly one window is connected, else errors `no EasyEDA connector is available` (a momentary connector reconnect can also trigger this — just retry). (Low-level equivalent: `document.current` → `pcb.documents.list` → `document.open <pcbUuid>`.)
3. **Inspect before mutating**: `pcb.components.list` (`includeBBox`+`includePads`), `pcb.layers.list` (read `copperLayerCount`), `pcb.nets.list`, `pcb.board.info`.
4. Small additive operations; **verify each** by readback + `pcb.drc.check`.
5. **Confirm** before destructive ops (`delete`, `import_changes`, bulk `arrange`) and before saving.
6. Summarize moved/changed primitives, warnings, and artifacts.

## Actions

### Navigation

- `pcb.documents.list` — all PCB documents in the project (uuid + name); pair with `document.open`.
- `document.open` — open any document (schematic page or PCB) by uuid; the cross-type switch entry.
- `pcb.board.info` — current Board (schematic↔PCB linkage) + current PCB; the prerequisite context for `import_changes`.

### Board (板子/组合 — the schematic↔PCB binding)

A **Board groups exactly one schematic + one PCB** — that is how the two are kept
together, and what `import_changes` follows. Boards are identified by **name**, not
uuid. CLI: `easyeda board …`. Maps to `eda.dmt_Board.*`.

- `board.list` / `board.current` — all boards (name + bound schematic + pcb) / the current one. A board can hold only a PCB or only a schematic — the missing side is reported as `null`.
- `board.create` — bind a schematic and/or PCB into a new board (`--schematic` / `--pcb`). The fix for a floating/unlinked PCB before `import_changes`.
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

### Read / inspect

- `pcb.components.list` — placed footprints. `includeBBox` → per-component rendered extent (for overlap/spacing reasoning); via the CLI (`pcb list --include-bbox`) each bbox'd part also carries `center` `{x,y}` — the bbox geometric center, CLI-computed — use it (not the anchor `x`/`y`) when planning positions; `includePads` → pads + net (the net-by-name connectivity) + **real copper `width`/`height`** (mil, axis-aligned after pad rotation; omitted for complex-polygon pads → consumers fall back to a nominal size). Connector ≥0.12.1; check/route clearance math uses these real extents.
- `pcb.layers.list` — layers (id/name/type), `currentLayer`, and `copperLayerCount` (2-layer vs 4+-layer — gates the decoupling rules).
- `pcb.nets.list` — nets (`net` / `length` / `color`).
- `pcb.report` — **read-only design report** driven by per-net copper length: every net's routed length, each **net class**'s aggregate length, **differential-pair** P/N lengths + `skew` (`|lenP−lenN|`), and **equal-length-group** per-net lengths + `spread` (`max−min`). No DRC run — the quantitative companion to `pcb.drc.check` for routing-quality gates (diff skew / length matching). Pure read.
- `easyeda pcb check` — **reconstructed DFM (design-for-manufacture) audit** — the PCB sibling of `sch check`, and the quality checks the native `pcb drc` (rule clearance) does NOT flag. Copper rules compute **purely Go-side** from placed copper (`pcb.line.list` + `pcb.via.list` + `pcb.components.list --include-pads`) and never mutate; the silkscreen rule reads `pcb.silk.list` (text layer + mirror + **reverse + rotation + fontSize**), the antenna rule reads `pcb.region.list` (region bbox + rule types) + component bboxes. Rules: **dangling-end** (a track end anchored to no pad/via/track → floating copper), **acute-angle** (two same-net same-layer segments bend <90° → acid trap), **non-orthogonal** (a single track off the 0/45/90/135° grid → free-angle routing, WARN — catches lazy pad-to-pad diagonals), **track-over-pad** (a track body crosses a pad center it doesn't terminate on, same layer: cross-net = **ERROR** short, same-net = WARN), **silkscreen-flipped** (a silkscreen text 放反 — three modes: a designator on the opposite silk layer from its component **ERROR**; a top/bottom text whose **mirror OR reverse** flag reads backwards **ERROR**; a reference designator (`key=="Designator"`) not reading **upright** — 180° upside-down / 90°·270° sideways — **WARN**), **overlapping-via** (two vias stacked), **single-layer-via** (a *signal* via that changes no layer — power/GND stitch vias are skipped, they connect to a pour not a track), **width-mismatch** (a 2-pin part with asymmetric neck-down → INFO), **duplicate-segment** (collinear overlapping redundant copper), **antenna-keepout** (an antenna component — ESP WROOM/WROVER module, an `ANT*` part, or a **discrete chip antenna** matched by device name `2450AT`/`ANT-SMD` (#123: auto-designators like AE1 defeated the ANT* test) — whose footprint lacks a no-copper keep-out region on **every** copper layer → WARN, naming the missing layer; copper under an antenna detunes it. Requires top (L1) + bottom (L2) no-copper regions, plus the inner planes via `no-inner-electrical` on 4+-layer boards — a top-only keep-out still lets the bottom pour fill under the antenna), **netless-pour** (a copper pour bound to **no net** — dead copper that occupies board area but connects nothing, issue #34; arises from `pcb pour` without `--net`, or pouring directly on a flipped PLANE layer → WARN, remove with `pcb pour-clean --netless`), **via-crosses-plane** (a via whose net differs from an inner **PLANE/内电层**'s net, issue #30 — official bug [easyeda/pro-api-sdk#32](https://github.com/easyeda/pro-api-sdk/issues/32): a via created **after** the plane exists gets **no anti-pad** cut into the negative plane, DRC reports Plane Zone to Via / Hole to Plane Zone and `pour-rebuild` alone doesn't repair it → WARN with fix guidance: prefer removing the via and routing on outer layers, or `easyeda doc reload` then `pcb pour-rebuild`, then confirm with `pcb drc`. Reads the stackup via `pcb.layers.list` (`type=="PLANE"`) + plane nets from `pcb.pour.list`. **Best-effort**: the API exposes no anti-pad/creation-order data, so a via placed *before* the plane flip — proper anti-pad, clean DRC — is flagged too; treat `pcb drc` as the arbiter of which flagged vias are actually broken. A PLANE layer with **no net-bound pour visible** gets its own **INFO** (not WARN, not `--strict`-gated — issue #110: after `doc reload` a PLANE-layer pour is loaded into the negative-plane store and becomes **invisible to `pcb.pour.list`**, with no extension-API read path, so "plane net unknown" is usually a reload artifact, not a defect; treat `pcb drc` Connection=0 as the arbiter before adding any pour — blindly re-pouring stacks duplicates. If the plane is genuinely empty: pour while the layer is SIGNAL, then flip), dangling-end anchors a track endpoint by **via area** too (a same-net endpoint anywhere inside the via copper counts as anchored — track↔via conducts on its own; the former **via-bond** ERROR rule that flagged bare track↔via junctions was removed after [pro-api-sdk#31](https://github.com/easyeda/pro-api-sdk/issues/31) proved to be our misdiagnosis — the "floating" symptom was stale pour connectivity, fixed by `pcb pour-rebuild`, not by fills), **floating-track-island** (a connected **group** of ≥2 tracks/vias in which no endpoint anchors to any pad — dangling-end's blind spot, members anchor each other → WARN listing all member ids for `pcb track-delete`; islands under a same-net pour are exempt), **power-not-poured** (a power/GND net with ≥2 pads that has **no same-net pour and is bound to no PLANE** → WARN — power should be delivered by copper area, not thin tracks, the #1 DRC source; fix `pcb pour-fit --net N` on 2-layer / `pcb power-planes` on 4-layer; single-pad nets and already-poured nets are exempt. **#117 nuance**: when the board carries an inner **PLANE layer with unknown net** — its pour is platform-invisible after `doc reload`, #110 — a GND-class finding degrades to **INFO** (non-blocking, not `--strict`-gated): that plane almost certainly IS the GND pour, so verify with `pcb drc` Connection=0 instead of re-running `power-planes`), **width-under-spec** (a routed **power** track thinner than its net-class spec width — 公制圆整阶梯 branch 0.25mm / trunk 0.4mm / high-current 0.5mm (≈9.84/15.75/19.69mil, 规范 §1.2), see `pcb net-classes` → WARN, one aggregated finding per net with the thinnest offender; **fine-pitch narrowing and via-stitch stubs are exempt**, and signal nets are not checked since their spec is the live default and fine-pitch narrowing is legitimate), **silk-over-pad** (silk text whose estimated extent covers a same-side pad — fab clips silk on exposed copper → WARN; fix with `pcb silk-align`/`pcb silk-set`; text extent from string length × the REAL `fontSize` (40mil fallback), pads tested against their real width/height, 规范 §11.2), **decap-too-far** (a 2-pad C\* with one pad on a power rail + one on GND sitting >100mil/2.5mm from the nearest same-rail U\* pin → WARN — a decap must hug its IC ≤2mm; rails with no IC pad (bulk/input caps) and signal-signal caps are exempt, 规范 §3.1), **via-in-pad** (a **same-net** via ON a pad center → WARN — solder wicks down the barrel AND this project proved via-on-pad ≠ connected; offset with a dog-bone stub; cross-net via↔pad stays the clearance rule's ERROR, 规范 §2.3), **copper-near-edge** (routed track/via copper within the live copper-to-edge rule of the board-outline bbox — fallback 8mil routed edge → WARN, aggregated per net with the worst offender, 规范 §5.1; needs `pcb.outline.get`, skipped without an outline), **fiducial-missing** (an SMT-scale board — ≥30 top pads — with <3 `FID*`/`MARK*` fiducial parts → **INFO** only, since JLC panel rails add their own marks; local marks matter for fine-pitch, 规范 §9). 规范 §refs point into `docs/pcb-design-rules.md` (the fact-standard手册 the check messages cite). `--json` for the full list; `--strict` exits non-zero on any WARN/ERROR (gate-able). Complements `pcb layout-lint` (placement/routability) + `pcb drc` (rule clearance). Arcs are out of scope for v1 (line/via/pad only; auto/short-routed copper is line segments); through-hole cross-layer track-over-pad shorts are a known blind spot (pad layer reported per side). Core + tests in `internal/app/pcb_check.go`.
- `easyeda pcb drc` (`pcb.drc.check`) — native rule-clearance DRC, normalized to `{passed, violations}`. **`--json` flattens** the panel's nested tree into one row per violation `{rule, objType, ruleName, net, x, y, layer, objs, message}` with **x/y in real mil** (raw leaves store mil/10 — the flattener owns the ×10) — pipe to `jq`, feed `objs` ids straight into `pcb via-delete`/`track-delete`. **`--timeout <s>`** (default 60) bounds the wait AND is forwarded to the daemon, which answers with a structured error *before* the HTTP client gives up. ⚠️ **Foreground constraint**: a background/occluded EasyEDA window **never finishes** the DRC canvas recompute — on timeout, bring the window to the FOREGROUND and run **once**; do **not** retry in a loop (each retry piles another recompute onto the webview). The daemon enforces this: a second `pcb drc` on a window whose first hasn't settled is rejected immediately (`ACTION_BUSY`).
- `pcb.drc.rules` — read the active PCB's **DRC rule configuration** (clearances, track widths, via sizes, …) **without running a check**. Use to feed real rule values into layout reasoning / gates, or to see what `pcb.drc.check` enforces. The daemon parses the (deeply-nested, untyped) result into `{clearance, trackWidth, trackWidthMin, viaDrill, viaDiameter}` in mil (`internal/app/pcb_rules.go`); `route-short`/`auto-place` consume it so they conform to the board's spec.
- `easyeda pcb net-classes [--json]` — print the **net-class → spec track-width ladder** (规范线宽) the daemon uses: `signal` (live default) / `power-branch` (3V3·1V8, 0.25mm≈9.84mil) / `power-trunk` (+5V, 0.4mm≈15.75mil) / `high-current` (VBUS·VIN·VBAT, 0.5mm≈19.69mil) / `gnd` (prefer pour). Roles are classified by net name/voltage (`pcb_netclass.go`); power-rung widths are **公制圆整** (0.05mm grid, 规范手册 §1.2 — not mil fragments like 10/15/20), seeded from the live rules and clamped ≥ the fab minimum (signal stays the raw live value, never rounded). `route-short` sizes each net by this table and `pcb check` width-under-spec gates under-sized power tracks. (A block's declared per-net `track_width_mil` overrides the heuristic — phase-2 consumption.)
- `easyeda pcb drc-rules-set --pour-clearance <mil>` — the **write side** of `drc-rules` (v1 knob: pour/plane copper clearance, **raise-only** — never loosens a stricter board). Patches `Plane` `lineClearance` in `copperRegion` (both pad models) + `innerPlane` of the current rule configuration, writes it back, verifies by re-read; follow with `pcb pour-rebuild` so existing pours reflow. A write on an immutable system preset (`JLCPCB Capability(...)`) turns it into a per-board `自定义配置` copy — expected. **Part of the solidified fix for the fresh-PCB pour-reflow divergence**: a newly created PCB reflows ~3% under the configured clearance (10mil → ~9.7mil) AND skips thermal spokes; `--pour-clearance 12` restores margin over the 10mil DRC floor.
  > **Fresh-PCB trap — the rules snapshot**: a PCB document **created in the current session and never reloaded** computes pour reflow from a **creation-time rules snapshot** — rule writes (readback shows them!), `pour-rebuild`, and tab-switching away/back all have NO effect on the reflow. Only a real close+reopen (`easyeda doc reload` — saves first, no edits lost) refreshes it; after the reload, `pcb pour-rebuild` reflows under the live rules (clearance AND thermal spokes). Already-reloaded documents (e.g. any board that survived an EasyEDA restart) honor rule writes immediately. The esp32-mini playbook encodes the full recipe: `rules-pour-margin` → pours → `reload-pcb` (`doc reload`) → `pour-rebuild-2`; verified on a fresh board: DRC 55 → **1** (remainder = the known add-component netlist false positive).
  > **Raw-API trap** (if scripting rules via `debug exec` instead): `eda.pcb_Drc.overwriteCurrentRuleConfiguration()` takes the **BARE config content** — `getCurrentRuleConfiguration()` returns `{name, config}`, and passing that whole wrapper **silently no-ops** (resolves `undefined`, readback unchanged). Pass `cfg.config` → returns `true`.
  > **Fab-rule baseline: [`fab-rules-jlcpcb.json`](fab-rules-jlcpcb.json)** — the canonical JLCPCB fabrication capabilities (min trace/space, via drill+pad, annular ring, copper-to-edge, silk, by layer count + copper weight), captured from JLCPCB's published capabilities. JLCPCB is the fab behind EasyEDA Pro, so a live board's `pcb.drc.rules` converges with this file's **recommended** column (verified on ceshi: clear 6mil / width 10mil / via 0.3–0.6mm). **Always prefer the live rule; use this JSON as the fallback seed + as clamp floors** (never emit a track/via/gap below the `manufacturingMin`). The **`boardTypeRulesLive`** section holds the AUTHORITATIVE real per-board-type rules exported from JLCEDA (single / double / multi-layer / metal-core), fingerprint-classified + confirmed against named exports — `defaultPcbRules` uses the **doubleLayer** row (clear 6 / width 10 / min 5 / via 0.3–0.6mm / copper-to-edge 10). Controlled impedance is intentionally omitted (not derivable from platform data — see task #27).

### Routing (copper tracks + vias)

Real routing primitives — **additive creates** (no confirm), like the schematic
`wire.create`. Bind to a net **by name** (pull from `pcb.nets.list`); layer ids from
`pcb.layers.list`. EasyEDA's `create()` is **lenient** — it can return no primitive on a
bad layer/coords without throwing, so each action verifies a primitive came back and
fails honestly otherwise. **PCB autosave is on** (debounced) — still **save explicitly**
at checkpoints. There is **no one-call autorouter** on this build
(`pcb_Document.autoRouting` is undefined — see `docs/ecosystem-survey.md` §6/§7); route
segment-by-segment, or use the file-exchange autoroute flow. **布线档如何选见
[`design-flow.md`](./design-flow.md) P7 三档阶梯——稠密板默认不是 file-exchange autoroute,而是
请用户点 EasyEDA 原生「布线→自动布线」(人机协作档);Freerouting 仅全 headless 无人可点时兜底。**

- `pcb.line.create` — a copper **track** (导线): line segment on a copper layer
  (`TOP=1`, `BOTTOM=2`; **inner-copper ids are higher** — `id 3` is silkscreen, not
  copper, so read real ids from `pcb.layers.list`) between `(startX,startY)` and
  `(endX,endY)` (mil, y-up), `lineWidth` (default 6 mil), optional `net`. Verify with
  `pcb.drc.check`.
- `pcb.via.create` — a **via** (过孔) at `(x,y)` with `holeDiameter` (drill, default 12
  mil) + `diameter` (outer pad, default 24 mil), optional `net`.
- `pcb.line.list` / `pcb.via.list` — read what's routed (filter by net/layer) before
  rip-up or reroute.
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

### Copper pour (铺铜)

A pour is a net-bound copper region (usually GND/power plane). **The agent passes raw
points** — the connector builds the `IPCB_Polygon` (`pcb_MathPolygon.createPolygon`)
and re-pours; passing raw points to the bare `eda.*` create fails ("无法创建覆铜边框图元").

- `pcb.pour.create` — pour from a closed polygon `points` (`[[x,y],…]`, mil, y-up) on a
  copper layer, bound to a `net` (**required — a netless pour is dead copper; `pcb pour`
  now refuses an empty `--net`, issue #34**). `fill = solid` (default) `| grid | grid45`.
  Size it to the board outline; verify `poured:true` + `pcb.drc.check`.
- `pcb.pour.list` / `pcb.pour.delete` — inspect / remove pours.
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

### Keep-out / rule regions (禁止区域)

A region (`eda.pcb_PrimitiveRegion`) is a polygon carrying **rule types** that keep
things OUT of an area — antenna clearance, board-edge inset, mechanical exclusion.
It is **NOT net-bound copper** (that's a pour) — `create` takes no net. EasyEDA's own
DRC + copper pour respect it (a pour avoids a `no-pours` region). Same raw-points
convention as pour (connector builds the polygon).

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

> **Read-back limit (verified #18):** `--name` on a region is fire-and-forget —
> `getState_RegionName` never reads it back, so `region list` shows `null` and the
> injected DSN keepout is named `region_keepout_N`. Likewise `pcb fill`'s `fillMode`
> always reads back `solid`. Geometry / layer / net / **ruleType** persist fine —
> just don't gate logic on reading a region's name or a fill's mode. Platform SDK
> quirk (same family as the netflag rotation echo trap), not fixable from here.

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
> **Stale-frame detection (issue #31).** `pcb snapshot` now has parity with `sch snapshot`:
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
> `pcb check` 的 silkscreen-flipped 规则（`layer=4` + `mirror=true`）做数据级判定为准。

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
> high-current — `pcb_netclass.go`), and `pcb check` **width-under-spec** gates
> under-sized power tracks. Still pending: writing those roles into EasyEDA's NATIVE
> net-class rules (`createNetClass`/`overwriteNetRules`, @beta — so the native DRC
> enforces per-class width) + diff-pair/equal-length **definitions** (read side is
> in `pcb.report`).

### Schematic → PCB sync + component CRUD

- `pcb.import_changes` — **sync components/netlist from the schematic** (从原理图导入变更). How parts first arrive on the board: ensures a Board links SCH+PCB, then `importChanges`, then recomputes ratlines. **Mutates the board; confirm first.** Returns `imported:false` (with a reason) for a floating/unlinked PCB.
  > **⚠️ Limitation (verified #20):** `importChanges` does **NOT** add a component placed via the API to an **existing** PCB — it returns `imported:true` but the PCB count is unchanged (the new part IS in the netlist, but the API `importChanges` is a no-op for incremental adds; no annotate/refresh/update-PCB API exists). It only populates the board the first time. **To add ONE part to an existing PCB, use `pcb add-component`** (below) — it places + connects the part directly.
- `pcb add-component` (`pcb.add_component`) — **the working way to add a part to an existing board.** Places the footprint (`--library` + `--uuid`, a device) at `--x/--y` on `--layer`, links it to its schematic twin (`--designator` + `--unique-id`), assigns each pad's net from `--nets` (a JSON `padNumber→net` map), and recomputes ratlines — directly wiring net→pad, which is what `importChanges` would normally do. **Get `--nets` and `--unique-id` from `sch read`** (the netlist is only readable while the schematic is the active doc, so you pass them in). Workflow: ① place + wire the part in the schematic → ② `sch read` (note its pin nets + `uniqueId`) → ③ `pcb add-component … --designator U2 --unique-id gge9 --nets '{"5":"3V3","3":"GND"}'`. Verify with `pcb list --include-pads` + `pcb drc`. **Embedded-via bonding (#118)**: footprints that EMBED vias (QFN EPAD thermal vias) used to land with `net:""` — the EPAD never bonded to the GND plane and DRC fired one "SMD Pad to Via" per via, with no repair path (embedded vias can't be deleted, #120). The handler now assigns every netless via inside a just-assigned pad's copper rect that pad's net via `pcb_PrimitiveVia.modify` (@beta) and readback-verifies it — the result's `embeddedVias {assigned, verified, failed}` reports the outcome. ⚠️ **The assignment does NOT survive a doc reload** (live-verified: the platform re-materializes embedded vias netless every time) — re-run `pcb via-bond` after any reload, before DRC/power-planes; `pcb check`'s **netless-via-in-pad** WARN is the tripwire.
- `pcb.component.modify` (`pcb modify`) — move (x/y), rotate, flip layer (top/bottom), lock, designator/BOM flags. Patch x/y = **anchor**; `pcb modify --center --x <cx> --y <cy>` writes by **bbox center** instead (CLI converts via the live bbox; mutually exclusive with a rotation change in the same call — rotate first, then center).
- `pcb.component.delete` (`pcb delete --ids`) — delete component primitives **by id** (`--ids` CSV or JSON array). **Confirm first** (no undo). ⚠️ **只删器件**,布线/铺铜/区域/丝印会残留 —— 要整版清板重来用 **`easyeda pcb clear`**(`pcb.page.clear`,见上「一键整版复位」)。

### Layout adjustment (deterministic — EasyEDA exposes no align/grid API)

- `pcb.align` — `mode = left | right | top | bottom | centerX | centerY` (y-up: `top` = larger y), aligned to the group extent.
- `pcb.distribute` — even center spacing, `axis = x | y`, extremes fixed.
- `pcb.grid_snap` — round component anchors to `grid` (mil; SMD 25, THT 50).
- `pcb.components.move` — translate a group by relative `dx` / `dy`.
- `pcb.components.arrange` — coarse auto-layout **seed** (priority P6): `mode=cluster` groups by shared local nets then grid-packs each cluster into a tidy non-overlapping block; `mode=grid` packs a flat grid. Skips locked parts.
- `easyeda pcb auto-place` — **module-aware** heuristic placement (daemon-side). Main chips (≥ `--main-pins`, default 8, distinct pins) are anchors that stay put — but a **connector-designated part (J*/CN*/USB*/SIM*/BAT*) never competes for main whatever its pin count (#131)**: a 16-pad USB-C out-pins a small IC, and calling it main made it steal the decoupling caps that belong to the regulator; high- and low-pin connectors alike are skipped with a diag for `place-constrained` to seat. `--anchor U1,U5` FORCES parts into the main set and `--exclude-main <des>` bars them (an excluded high-pin part stays put) — explicit beats every heuristic; every satellite (cap/R/LED) is pulled to the chip edge nearest the pad it connects to (the **nearest same-net pad** — a chip repeats GND/VCC many times), then packed along that edge with no overlap: decoupling caps land by their power pin (3V3/VCC), signal R's by their signal pin, an LED chains beside its series resistor. **v1.1 also re-orients** each 2-pin satellite so its connecting pad faces the chip (rotation 0/90/180/270, packed with the post-rotation bbox); `--no-rotate` keeps the v1 translate-only behavior. **With 2+ main chips**, any that overlap / sit closer than `--multi-gap` (default 150 mil) are spread into a left-to-right row (leftmost stays put) before satellites are placed; `--multi-gap 0` disables it. **Spacing is rule-aware**: `--gap`/`--pitch` default to values derived from the board's live DRC rule (clearance + track width, via `pcb.drc.rules`) instead of a fixed 40/30, so packing never creates sub-clearance corridors. `--dry-run` prints the plan without moving. A SEED — refine by hand + verify with `pcb drc`. Prefer over `arrange` when there is a clear main chip.
- `easyeda pcb outline-fit` — **tighten the board outline to the placed parts** (daemon-side). Reads every component's bbox, adds `--margin` (default 100 mil), and replaces the outline with that rectangle. Fixes low utilization (ceshi 17%→71%); reports util before/after. **Run AFTER `auto-place`, BEFORE pour/route** (changing the outline after copper exists can strand it). `--dry-run` previews.
- `easyeda pcb outline-round` — **rounded-rectangle board outline** (圆角板框, daemon-side). Rounds the current outline bbox (or `--rect x0,y0,x1,y1`, `--margin` to expand) with corner `--radius` (default ≈12% of the shorter side, clamped to half). Corners are chord-approximated (`--segments` per 90°, default 6) since `pcb.outline.set` takes a polygon — verified: the board-outline layer renders, snapshot shows curved corners. Run BEFORE pour/route. `--dry-run` prints the polygon.
- `easyeda pcb silk-align` — **POSITION-AWARE designator (位号) auto-placement** (v2, designed via a 3-lens workflow). Per part it ranks the 4 sides by **local free space** (corridor clearance to nearest obstacle) + **board position** (edge parts pulled inward, never off-board) + a **crowd-axis bonus** (a part in a tight stack gets its label pushed PERPENDICULAR to the stack — the ceshi C2/C1/R1/C3 fix), then places via a ladder (base offset → grow rings → diagonals) at the lowest-cost slot. **Core fix vs v1: the obstacle set now includes OTHER parts' PADS** (a label over exposed copper is fab-clipped — why C1's label used to land on C2's pad), component bodies, keep-out regions (mechanical=hard/copper=soft), the **board outline** (containment), and other/frozen labels. Most-constrained-first order. Rotation stays **0** (upright, keeps `pcb check` clean); **bottom parts → bottom silk + mirror** (retry-without-mirror fallback). A boxed-in part is **left + reported in `unresolved`**, never moved onto a pad. `--side` biases the default, `--offset` = base gap, `--refs` limits to specific parts (others frozen). Outputs `aligned`/`warned`/`unresolved`/`skipped`.
- `easyeda pcb silk-add` — **add a FREE silkscreen string** (board marking / credit / note) at `--x/--y` with config: `--layer` (3=top silk default, 4=bottom), `--font-size` (mil), `--line-width` (stroke mil), `--rotation`. Legible JLCPCB-safe defaults (font 40 / stroke 6) — **a small font (<~32mil) with a thick stroke smears the glyphs (糊)**. Returns primitiveId + rendered bbox (check it fits + clears parts). Then restyle/reposition with `pcb silk-set`.
- `easyeda pcb silk-set` — **batch-adjust existing silk** (designators + free strings): `--ids '[...]'` + any of `--x/--y/--rotation/--font-size/--line-width/--text` (only given keys change). **ALIGN shortcut**: `--align center|mid|centerx|centery|left|right|top|bottom` + `--ref <designator>|board|outline|fill` positions each silk relative to that reference bbox (e.g. `--ref board --align centerx` centers the board credit; `--ref U1 --align top` aligns a label to U1's top), computed from the silk's own bbox. Uses the reliable `.modify(id,props)` — **rotation persists but a `pcb snapshot` before a document reload shows the OLD orientation (stale render); judge by `pcb check`/silk list, not a screenshot**.
- **Teardrops (泪滴) — platform wall.** `eda.*` has NO create/apply-teardrop API (teardrops appear only as a `getManufactureFile` object type, never as a constructable primitive) — like the interactive routing menu, it's UI-only. Apply teardrops by hand in EasyEDA (右键 → 泪滴) before fabrication; the agent can't automate it.
- `easyeda pcb layout-lint` — **score placement quality + predict routability BEFORE routing**。Plain mode 的 `--min-gap` 默认仍是电气 clearance,仅供诊断。**Gate mode 已装配感知(#99)**:先 `pcb stage set-assembly --profile hand-solder|reflow`;`--gate` 读取该档案,手焊将间距地板钳到 ≥40mil,任何 tight pair 都失败,再执行 #97 的 `--min-score`(默认60)+`--max-crossings`(默认8)门。通过才持久化 `pre_route_passed`,与 `outline_confirmed` 一起解锁布线。因此“默认约6mil无告警”不再能冒充“适合手焊”。**烙铁进入通道已机械化**:hand-solder 下 gate 同时跑 solder-access 检查——每个器件的 bbox 四侧至少一侧要有 ≥ `largePadAccessMil`(默认60mil)的净通道(去耦可贴近 IC,但另一翼必须可操作;板边=天然可达),四面被围报 `no-access` 且 gate 失败、`confirm-layout` 拒绝。v1 是器件 bbox 级近似(pad 尺寸未从连接器暴露,按 pad 分类大焊盘留待后续);Type-C 外壳脚/SOT-223 的进入**方向**是否合理仍建议截图复核。
- `easyeda pcb route-short` — **short-trace self-router** (daemon-side, the heuristic tier — NOT `pcb autoroute`/Freerouting). Per net: MST over pads, then a track per hop ≤ `--max-len` (Manhattan) on the pads' shared layer. **Skips power+ground nets by default** (VCC/3V3/GND/… via `isGlobalNet`) — they belong in a POUR, not thin tracks; `--route-power` forces routing them. (Measured on ceshi: routing 3V3 as thin tracks caused **18 of 27** Safe-Spacing violations — pouring power instead dropped Safe-Spacing 27→3. Do `pcb pour` GND + each power net after routing signal. Residual No-Connection on a 2-layer board = the pour can't reach every scattered power pad on a shared layer; that needs via-stitching / a dedicated plane layer.) Also skips already-routed nets, cross-layer hops (need a via), over-long hops (maze tier). **Widths are net-class rule-aware**: each net's width is picked by **role** (signal / power-branch 3V3·1V8 / power-trunk +5V / high-current VBUS·VIN — the §7.8 role split on the §1.2 metric grid: 0.25/0.4/0.5mm, `pcb_netclass.go`), seeded from the board's live DRC track-width spec (`pcb.drc.rules`, clamped ≥ the rule minimum) so a 3V3 branch gets 0.25mm (≈9.84mil) while a VBUS input gets 0.5mm (≈19.69mil), instead of the old flat power/signal 20/10 mil buckets. `pcb net-classes` prints the active ladder; `--width-signal` overrides the signal role, `--width-power` forces ONE width across all power roles (legacy), `--width` forces everything. **Corner style** via `--corner`: `90` (Manhattan L, default), `45` (chamfer — avoids acid traps/reflections), `round` (chord-approximated fillet, `--round-radius`; native arcs don't commit on this build so it's segmented). **Obstacle-aware (v2/v3)**: each hop picks the L orientation (horizontal- vs vertical-first) that crosses the fewest already-placed **other-net** tracks + other-net pads; `--no-avoid` restores the v1 naive horizontal-first. **Hard clearance gate (#111/#119/#122)**: other-net **pads**, **vias**, **same-layer tracks** (crossing OR under-clearance parallel run — the R2 SPIHD×SPIWP shorts) and **board cutouts/slots** (max(clearance,8mil) band, Slot Region to Track) are a **veto, not a cost** — a hop that cannot clear them detours (`--multilayer`) or lands in diagnostics unrouted; route-short never draws what `pcb check`/native DRC would flag (judges are shared with `findClearanceViolations`). Still NOT a maze router (no push-shove/vias/rip-up) — **run after `auto-place`** so hops are short/clear, then `pcb drc`. `--dry-run` previews. **布线档选择见 [`design-flow.md`](./design-flow.md) P7 三档阶梯**:稀疏 → 本 `route-short`;**稠密默认 = ② 人机协作档(停手请用户点 EasyEDA 原生「布线→自动布线」)**;`pcb autoroute`(external Freerouting)仅全 headless 无人可点时兜底,**绝不顶替 ②**。**门禁(issue #97)**:`route-short`/`autoroute` 默认要求项目状态 `outline_confirmed` + `pre_route_passed`(经 `pcb stage confirm-outline` + `pcb layout-lint --gate`),否则拒绝执行(CLI 与 daemon 双层拦截,详见上方 Board outline 段的 stage-state 说明);**force 分级(#132)**:`--force <理由>` 只放行软缺口(机械骨架至少一项已确认;state 不可知=可能零确认,同样拒),零确认板需 `--force-unsafe <理由>`;CLI 与 daemon 同一分级(`forceUnsafe` 随 forceReason 传到 /action 层)。两者均仅本次执行有效、不落确认、入审计(被拒尝试记 force-refused),`--dry-run` 只出计划不触发门禁。
- `easyeda pcb stackup` — **board stackup: copper layer count + inner-layer types** (`pcb.stackup.set` / read via `pcb layers`). `pcb stackup set --layers 4` sets the count (2|4|6|…|32, `eda.pcb_Layer.setTheNumberOfCopperLayers`); `--plane 15 --plane 16` / `--signal 15` set inner layers' type (SIGNAL↔PLANE/内电层, `modifyLayer` — only INNER layers accept a type change). Set the layer count BEFORE routing/pouring inner layers. **A net-bound 内电层 (PLANE) IS achievable via API** — verified recipe: pour the net on the inner layer **while it is still SIGNAL** (`pcb pour`/`power-planes`), THEN flip the type (`--plane 15`), THEN `pcb pour-rebuild`. The net-bound fill survives the flip and DRC stays clean (0 Plane-Zone/via clashes). Doing it in the other order (flip type first, then pour on a PLANE layer) is the path that breaks — the pour lands netless on L1. `power-planes` does this for you (`--gnd-plane`, on by default).
- `easyeda pcb power-planes` — **4-layer power distribution (the proper fix for the 2-layer pour conflict)**. Ensures ≥4 copper layers, assigns GND + power nets to inner layers, **via-stitches every power/ground pad DOWN to its plane** (the connection point the inner pour needs — without it the inner pour is all isolated islands and deposits nothing), then pours each net on its inner layer, then **flips the GND inner layer to 内电层/PLANE** (`--gnd-plane`, on by default) and rebuilds. **Order matters: vias BEFORE the pour** (empty otherwise), and the plane-flip AFTER the pour (the verified pour-while-SIGNAL → flip → rebuild recipe keeps the fill and DRC clean). The power layer stays 信号层 so its pour is an ordinary positive plane — matching the common customer stackup **GND=内电层 / VCC(3V3)=信号层** (e.g. `esp32MiniRequire.md`). `--gnd-layer 15 --power-layer 16` (defaults); `--gnd-plane=false` keeps GND a plain signal-layer pour. **Validated on ceshi: DRC 31 → 0, No-Connection → 0** — dedicated planes solve what a shared 2-layer pour can't (two power nets stranding each other's pads). Run AFTER auto-place + outline-fit + route-short (signals). Two power nets sharing one plane layer re-create the conflict (warned) — give each its own inner layer on 6+ layers. `--dry-run` prints the net→layer plan. **State interop (#114/#117)**: the run records two verdicts into the workflow state — nets it deliberately ROUTED AS TRACKS (no plane left, `powerTracksNets`) and nets it poured onto a layer then flipped to PLANE (`planePouredNets`). The `post_route_checked` gate exempts both from `power-not-poured` blocking; the second matters because **PLANE-layer pours are invisible to `pcb.pour.list` after a `doc reload` (#110)** — without the record the gate would re-flag the GND the command just poured and suggest re-running it (deadlock, #117). Standalone `pcb check` (no state) degrades a GND finding to **INFO** whenever the board carries a net-unknown PLANE layer — treat `pcb drc` Connection=0 as the arbiter, do NOT re-pour.
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
  (`docs/marketplace-coverage.md`, HIGH item).

### Board outline (板框)

The board outline anchors edge keep-out, connectors-to-edge and mounting holes, so
`place-constrained`'s edge heuristic needs *some* outline to snap to. **Two legal
paths, by whether mechanical dimensions exist (issue #97 — these do NOT conflict):**

- **有机械尺寸/外壳约束**: build a rough outline from the spec FIRST (`outline.set` /
  `outline-round`), then place against those real edges, then let the user confirm and
  tighten it.
- **无机械尺寸**: rough-place first with a **temporary oversize outline** (`outline-fit`
  with a generous `--margin` so `place-constrained` has an edge to snap to), then tighten
  the outline (`outline-fit`/`outline-round`) once placement is done.

Both paths end with the user confirming placement (`pcb stage confirm-layout`) and the
outline (`pcb stage confirm-outline`) before the routability gate. Any outline edit
(`outline-fit`/`outline-round`) after a confirmation invalidates `outline_confirmed`
downstream, so it must be re-confirmed.

**Stage state is enforced, global, and fingerprinted (#97 follow-up):** state lives at
`~/.easyeda-agent/workflow/<project>.json` (not the cwd — `EASYEDA_WORKFLOW_DIR`
overrides); the daemon ALSO gates the raw routing actions (`pcb.line.create` /
`pcb.via.create` / `pcb.import_autoroute` → `STAGE_BLOCKED`) and auto-invalidates
downstream confirmations after any placement/outline mutation (response carries a
`workflow stage invalidated` warning). `confirm-layout`/`confirm-outline` pin the
sign-off to a **document fingerprint** (poses / outline geometry) — an out-of-band
edit (GUI drag, `debug.exec_js`, another agent) makes the next gate auto-invalidate
and point back to the right stage. Cut in at any stage / resume a session with
`easyeda workflow status --reconcile` (re-sync marker ↔ live document) then
`easyeda workflow advance` (idempotent: runs mechanical acceptance, stops with the
exact next command at human sign-off points). `--force <reason>` on route commands is
per-run and audited — nothing is confirmed by a force.

- `pcb.outline.set` — set the outline from a closed polygon `points` (`[[x,y],…]`, mil,
  y-up). Replaces any existing outline; reports `allInside`/`outside` (components out of
  the board). **Confirm first** (redraws the board edge).
- `pcb.outline.get` — current outline (segment/arc count + bbox).
- `pcb.outline.clear` — remove the outline.

**The agent generates the `points`** for the wanted shape. Curves are **line-segment
approximated** (~48–120 segments) — native arcs do not commit on this build, so a true
circle/arc needs the EasyEDA UI (圆形/圆弧 tool) or an SVG import. Recipes (centre `(cx,cy)`,
all mil):

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

## Guardrails

- Confirm before `pcb.component.delete`, `pcb.import_changes`, or a bulk `arrange`/auto-layout plan.
- Confirm before saving unless the user asked to save.
- Do not claim completion after a mutation until readback / DRC verifies it (or state the remaining risk).
- No undo — record before/after into the audit log so a move can be reversed by re-applying the old coordinates.
- Treat `File`/`Blob` outputs (gerber/pick-and-place/3D) as artifacts.
