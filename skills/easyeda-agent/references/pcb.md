
# EasyEDA PCB

Drive `easyeda-agent` typed actions. Run `easyeda actions` for the live machine-readable
list. Prefer typed actions; only fall back to `debug.exec_js` when a typed action is
missing **and** the user explicitly accepts a debug path.

> **PCB design rules live in this skill's references** — especially
> [`pcb-layout-conventions.md`](./pcb-layout-conventions.md)
> (placement priority P0–P7, stackup-conditioned decoupling, thermal/SI/DFM/grid rules,
> each with a data-detectable check). This operational skill **links** to it — single
> source, never copy the rules here.

## Coordinate system & model (load-bearing)

- **Data unit = `1 mil`** (schematics are `10 mil` / 0.01in — different). **y-UP**: +y renders upward.
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

- `pcb.components.list` — placed footprints. `includeBBox` → per-component rendered extent (for overlap/spacing reasoning); `includePads` → pads + net (the net-by-name connectivity).
- `pcb.layers.list` — layers (id/name/type), `currentLayer`, and `copperLayerCount` (2-layer vs 4+-layer — gates the decoupling rules).
- `pcb.nets.list` — nets (`net` / `length` / `color`).
- `pcb.report` — **read-only design report** driven by per-net copper length: every net's routed length, each **net class**'s aggregate length, **differential-pair** P/N lengths + `skew` (`|lenP−lenN|`), and **equal-length-group** per-net lengths + `spread` (`max−min`). No DRC run — the quantitative companion to `pcb.drc.check` for routing-quality gates (diff skew / length matching). Pure read.
- `easyeda pcb check` — **reconstructed DFM (design-for-manufacture) audit** — the PCB sibling of `sch check`, and the quality checks the native `pcb drc` (rule clearance) does NOT flag. Copper rules compute **purely Go-side** from placed copper (`pcb.line.list` + `pcb.via.list` + `pcb.components.list --include-pads`) and never mutate; the silkscreen rule reads `pcb.silk.list` (text layer + mirror + **reverse + rotation**), the antenna rule reads `pcb.region.list` (region bbox + rule types) + component bboxes. Rules: **dangling-end** (a track end anchored to no pad/via/track → floating copper), **acute-angle** (two same-net same-layer segments bend <90° → acid trap), **non-orthogonal** (a single track off the 0/45/90/135° grid → free-angle routing, WARN — catches lazy pad-to-pad diagonals), **track-over-pad** (a track body crosses a pad center it doesn't terminate on, same layer: cross-net = **ERROR** short, same-net = WARN), **silkscreen-flipped** (a silkscreen text 放反 — three modes: a designator on the opposite silk layer from its component **ERROR**; a top/bottom text whose **mirror OR reverse** flag reads backwards **ERROR**; a reference designator (`key=="Designator"`) not reading **upright** — 180° upside-down / 90°·270° sideways — **WARN**), **overlapping-via** (two vias stacked), **single-layer-via** (a *signal* via that changes no layer — power/GND stitch vias are skipped, they connect to a pour not a track), **width-mismatch** (a 2-pin part with asymmetric neck-down → INFO), **duplicate-segment** (collinear overlapping redundant copper), **antenna-keepout** (an antenna component — ESP WROOM/WROVER module or an `ANT*` part — whose footprint lacks a no-copper keep-out region on **every** copper layer → WARN, naming the missing layer; copper under an antenna detunes it. Requires top (L1) + bottom (L2) no-copper regions, plus the inner planes via `no-inner-electrical` on 4+-layer boards — a top-only keep-out still lets the bottom pour fill under the antenna), **netless-pour** (a copper pour bound to **no net** — dead copper that occupies board area but connects nothing, issue #34; arises from `pcb pour` without `--net`, or pouring directly on a flipped PLANE layer → WARN, remove with `pcb pour-clean --netless`), **via-crosses-plane** (a via whose net differs from an inner **PLANE/内电层**'s net, issue #30 — official bug [easyeda/pro-api-sdk#32](https://github.com/easyeda/pro-api-sdk/issues/32): a via created **after** the plane exists gets **no anti-pad** cut into the negative plane, DRC reports Plane Zone to Via / Hole to Plane Zone and `pour-rebuild` alone doesn't repair it → WARN with fix guidance: prefer removing the via and routing on outer layers, or `easyeda doc reload` then `pcb pour-rebuild`, then confirm with `pcb drc`. Reads the stackup via `pcb.layers.list` (`type=="PLANE"`) + plane nets from `pcb.pour.list`. **Best-effort**: the API exposes no anti-pad/creation-order data, so a via placed *before* the plane flip — proper anti-pad, clean DRC — is flagged too; treat `pcb drc` as the arbiter of which flagged vias are actually broken. A PLANE layer with **no net-bound pour** gets its own WARN — its net is unknown; pour while the layer is SIGNAL, then flip). `--json` for the full list; `--strict` exits non-zero on any WARN/ERROR (gate-able). Complements `pcb layout-lint` (placement/routability) + `pcb drc` (rule clearance). Arcs are out of scope for v1 (line/via/pad only; auto/short-routed copper is line segments); through-hole cross-layer track-over-pad shorts are a known blind spot (pad layer reported per side). Core + tests in `internal/app/pcb_check.go`.
- `pcb.drc.rules` — read the active PCB's **DRC rule configuration** (clearances, track widths, via sizes, …) **without running a check**. Use to feed real rule values into layout reasoning / gates, or to see what `pcb.drc.check` enforces. The daemon parses the (deeply-nested, untyped) result into `{clearance, trackWidth, trackWidthMin, viaDrill, viaDiameter}` in mil (`internal/app/pcb_rules.go`); `route-short`/`auto-place` consume it so they conform to the board's spec.
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
segment-by-segment, or use the file-exchange autoroute flow.

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
- `pcb.clear_routing` — native `clearRouting` (`@alpha`, may be undefined on this build,
  and does NOT protect unlocked outline) — prefer `pcb.route.rip_up`.

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
  removes components, NOT regions — use `region delete`).

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
  `--ref <designator>` (+ `--margin`), on a `--layer`, bound to `--net`.
  `--fill-mode solid` (default) `| mesh | inner`. `--locked`. Verify with `pcb fill list`.
- `pcb fill list` / `pcb fill delete` — inspect / remove (filter list by `--layer`/`--net`).

**Board cutout / slot (挖槽) — `pcb slot`.** A fill on the **MULTI layer (12)** IS a
board cutout (per the eda API: *"填充所属层为 MULTI 时代表挖槽区域"*; manufacturing
emits it as a `BoardCutout`). `pcb slot --rect … | --ref ANT1 --margin 20` mills a
hole — antenna isolation / mechanical opening. No net. It's a `pcb_PrimitiveFill` on
layer 12, so list/delete via `pcb fill list --layer 12` / `pcb fill delete`.
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
> to the human/UI. **Shipped: copper pour + rip-up (R1/R2).** Still pending:
> net-class/diff-pair/equal-length **definitions** (R3 — read side is in `pcb.report`).

### Schematic → PCB sync + component CRUD

- `pcb.import_changes` — **sync components/netlist from the schematic** (从原理图导入变更). How parts first arrive on the board: ensures a Board links SCH+PCB, then `importChanges`, then recomputes ratlines. **Mutates the board; confirm first.** Returns `imported:false` (with a reason) for a floating/unlinked PCB.
  > **⚠️ Limitation (verified #20):** `importChanges` does **NOT** add a component placed via the API to an **existing** PCB — it returns `imported:true` but the PCB count is unchanged (the new part IS in the netlist, but the API `importChanges` is a no-op for incremental adds; no annotate/refresh/update-PCB API exists). It only populates the board the first time. **To add ONE part to an existing PCB, use `pcb add-component`** (below) — it places + connects the part directly.
- `pcb add-component` (`pcb.add_component`) — **the working way to add a part to an existing board.** Places the footprint (`--library` + `--uuid`, a device) at `--x/--y` on `--layer`, links it to its schematic twin (`--designator` + `--unique-id`), assigns each pad's net from `--nets` (a JSON `padNumber→net` map), and recomputes ratlines — directly wiring net→pad, which is what `importChanges` would normally do. **Get `--nets` and `--unique-id` from `sch read`** (the netlist is only readable while the schematic is the active doc, so you pass them in). Workflow: ① place + wire the part in the schematic → ② `sch read` (note its pin nets + `uniqueId`) → ③ `pcb add-component … --designator U2 --unique-id gge9 --nets '{"5":"3V3","3":"GND"}'`. Verify with `pcb list --include-pads` + `pcb drc`.
- `pcb.component.modify` — move (x/y), rotate, flip layer (top/bottom), lock, designator/BOM flags.
- `pcb.component.delete` — delete component primitives. **Confirm first** (no undo).

### Layout adjustment (deterministic — EasyEDA exposes no align/grid API)

- `pcb.align` — `mode = left | right | top | bottom | centerX | centerY` (y-up: `top` = larger y), aligned to the group extent.
- `pcb.distribute` — even center spacing, `axis = x | y`, extremes fixed.
- `pcb.grid_snap` — round component anchors to `grid` (mil; SMD 25, THT 50).
- `pcb.components.move` — translate a group by relative `dx` / `dy`.
- `pcb.components.arrange` — coarse auto-layout **seed** (priority P6): `mode=cluster` groups by shared local nets then grid-packs each cluster into a tidy non-overlapping block; `mode=grid` packs a flat grid. Skips locked parts.
- `easyeda pcb auto-place` — **module-aware** heuristic placement (daemon-side). Main chips (≥ `--main-pins`, default 8, distinct pins) are anchors that stay put; every satellite (cap/R/LED) is pulled to the chip edge nearest the pad it connects to (the **nearest same-net pad** — a chip repeats GND/VCC many times), then packed along that edge with no overlap: decoupling caps land by their power pin (3V3/VCC), signal R's by their signal pin, an LED chains beside its series resistor. **v1.1 also re-orients** each 2-pin satellite so its connecting pad faces the chip (rotation 0/90/180/270, packed with the post-rotation bbox); `--no-rotate` keeps the v1 translate-only behavior. **With 2+ main chips**, any that overlap / sit closer than `--multi-gap` (default 150 mil) are spread into a left-to-right row (leftmost stays put) before satellites are placed; `--multi-gap 0` disables it. **Spacing is rule-aware**: `--gap`/`--pitch` default to values derived from the board's live DRC rule (clearance + track width, via `pcb.drc.rules`) instead of a fixed 40/30, so packing never creates sub-clearance corridors. `--dry-run` prints the plan without moving. A SEED — refine by hand + verify with `pcb drc`. Prefer over `arrange` when there is a clear main chip.
- `easyeda pcb outline-fit` — **tighten the board outline to the placed parts** (daemon-side). Reads every component's bbox, adds `--margin` (default 100 mil), and replaces the outline with that rectangle. Fixes low utilization (ceshi 17%→71%); reports util before/after. **Run AFTER `auto-place`, BEFORE pour/route** (changing the outline after copper exists can strand it). `--dry-run` previews.
- `easyeda pcb outline-round` — **rounded-rectangle board outline** (圆角板框, daemon-side). Rounds the current outline bbox (or `--rect x0,y0,x1,y1`, `--margin` to expand) with corner `--radius` (default ≈12% of the shorter side, clamped to half). Corners are chord-approximated (`--segments` per 90°, default 6) since `pcb.outline.set` takes a polygon — verified: the board-outline layer renders, snapshot shows curved corners. Run BEFORE pour/route. `--dry-run` prints the polygon.
- `easyeda pcb silk-align` — **POSITION-AWARE designator (位号) auto-placement** (v2, designed via a 3-lens workflow). Per part it ranks the 4 sides by **local free space** (corridor clearance to nearest obstacle) + **board position** (edge parts pulled inward, never off-board) + a **crowd-axis bonus** (a part in a tight stack gets its label pushed PERPENDICULAR to the stack — the ceshi C2/C1/R1/C3 fix), then places via a ladder (base offset → grow rings → diagonals) at the lowest-cost slot. **Core fix vs v1: the obstacle set now includes OTHER parts' PADS** (a label over exposed copper is fab-clipped — why C1's label used to land on C2's pad), component bodies, keep-out regions (mechanical=hard/copper=soft), the **board outline** (containment), and other/frozen labels. Most-constrained-first order. Rotation stays **0** (upright, keeps `pcb check` clean); **bottom parts → bottom silk + mirror** (retry-without-mirror fallback). A boxed-in part is **left + reported in `unresolved`**, never moved onto a pad. `--side` biases the default, `--offset` = base gap, `--refs` limits to specific parts (others frozen). Outputs `aligned`/`warned`/`unresolved`/`skipped`.
- `easyeda pcb silk-add` — **add a FREE silkscreen string** (board marking / credit / note) at `--x/--y` with config: `--layer` (3=top silk default, 4=bottom), `--font-size` (mil), `--line-width` (stroke mil), `--rotation`. Legible JLCPCB-safe defaults (font 40 / stroke 6) — **a small font (<~32mil) with a thick stroke smears the glyphs (糊)**. Returns primitiveId + rendered bbox (check it fits + clears parts). Then restyle/reposition with `pcb silk-set`.
- `easyeda pcb silk-set` — **batch-adjust existing silk** (designators + free strings): `--ids '[...]'` + any of `--x/--y/--rotation/--font-size/--line-width/--text` (only given keys change). **ALIGN shortcut**: `--align center|mid|centerx|centery|left|right|top|bottom` + `--ref <designator>|board|outline|fill` positions each silk relative to that reference bbox (e.g. `--ref board --align centerx` centers the board credit; `--ref U1 --align top` aligns a label to U1's top), computed from the silk's own bbox. Uses the reliable `.modify(id,props)` — **rotation persists but a `pcb snapshot` before a document reload shows the OLD orientation (stale render); judge by `pcb check`/silk list, not a screenshot**.
- **Teardrops (泪滴) — platform wall.** `eda.*` has NO create/apply-teardrop API (teardrops appear only as a `getManufactureFile` object type, never as a constructable primitive) — like the interactive routing menu, it's UI-only. Apply teardrops by hand in EasyEDA (右键 → 泪滴) before fabrication; the agent can't automate it.
- `easyeda pcb layout-lint` — **score placement quality + predict routability BEFORE routing** (daemon-side; PCB sibling of `sch layout-lint`). Pulls every footprint's bbox + pads and reports: `overlap` (footprints intersect → ERROR, score 0), `off-board` (extends past the outline → ERROR), tight spacing (< `--min-gap`, default = board clearance → WARN), and the **ratsnest** — a per-signal-net minimum spanning tree (power/GND excluded, they're poured) whose cross-net segment **crossings** are the single-layer routability killer. Yields a **0-100 score + verdict** (easy/moderate/hard/very-hard); fewer crossings + shorter ratsnest = more routable. Run after `auto-place` to catch a bad layout before spending effort on routing; exits non-zero on overlap/off-board (gate-able). `--json` for the full finding list.
- `easyeda pcb route-short` — **short-trace self-router** (daemon-side, the heuristic tier — NOT `pcb autoroute`/Freerouting). Per net: MST over pads, then a track per hop ≤ `--max-len` (Manhattan) on the pads' shared layer. **Skips power+ground nets by default** (VCC/3V3/GND/… via `isGlobalNet`) — they belong in a POUR, not thin tracks; `--route-power` forces routing them. (Measured on ceshi: routing 3V3 as thin tracks caused **18 of 27** Safe-Spacing violations — pouring power instead dropped Safe-Spacing 27→3. Do `pcb pour` GND + each power net after routing signal. Residual No-Connection on a 2-layer board = the pour can't reach every scattered power pad on a shared layer; that needs via-stitching / a dedicated plane layer.) Also skips already-routed nets, cross-layer hops (need a via), over-long hops (maze tier). **Widths are rule-aware**: by default signal + power widths are seeded from the board's live DRC track-width spec (`pcb.drc.rules`, clamped ≥ the rule minimum) so tracks conform instead of the old hardcoded 10/20 mil; `--width-signal`/`--width-power`/`--width` still override. **Corner style** via `--corner`: `90` (Manhattan L, default), `45` (chamfer — avoids acid traps/reflections), `round` (chord-approximated fillet, `--round-radius`; native arcs don't commit on this build so it's segmented). **Obstacle-aware (v2)**: each hop picks the L orientation (horizontal- vs vertical-first) that crosses the fewest already-placed **other-net** tracks + other-net pads — kills most of the naive tangle at ~zero cost; `--no-avoid` restores the v1 naive horizontal-first. Still NOT a maze router (no push-shove/vias/rip-up) — **run after `auto-place`** so hops are short/clear, then `pcb drc`. `--dry-run` previews. Long/congested/any-distance routing → `pcb autoroute` (external Freerouting).
- `easyeda pcb stackup` — **board stackup: copper layer count + inner-layer types** (`pcb.stackup.set` / read via `pcb layers`). `pcb stackup set --layers 4` sets the count (2|4|6|…|32, `eda.pcb_Layer.setTheNumberOfCopperLayers`); `--plane 15 --plane 16` / `--signal 15` set inner layers' type (SIGNAL↔PLANE/内电层, `modifyLayer` — only INNER layers accept a type change). Set the layer count BEFORE routing/pouring inner layers. **A net-bound 内电层 (PLANE) IS achievable via API** — verified recipe: pour the net on the inner layer **while it is still SIGNAL** (`pcb pour`/`power-planes`), THEN flip the type (`--plane 15`), THEN `pcb pour-rebuild`. The net-bound fill survives the flip and DRC stays clean (0 Plane-Zone/via clashes). Doing it in the other order (flip type first, then pour on a PLANE layer) is the path that breaks — the pour lands netless on L1. `power-planes` does this for you (`--gnd-plane`, on by default).
- `easyeda pcb power-planes` — **4-layer power distribution (the proper fix for the 2-layer pour conflict)**. Ensures ≥4 copper layers, assigns GND + power nets to inner layers, **via-stitches every power/ground pad DOWN to its plane** (the connection point the inner pour needs — without it the inner pour is all isolated islands and deposits nothing), then pours each net on its inner layer, then **flips the GND inner layer to 内电层/PLANE** (`--gnd-plane`, on by default) and rebuilds. **Order matters: vias BEFORE the pour** (empty otherwise), and the plane-flip AFTER the pour (the verified pour-while-SIGNAL → flip → rebuild recipe keeps the fill and DRC clean). The power layer stays 信号层 so its pour is an ordinary positive plane — matching the common customer stackup **GND=内电层 / VCC(3V3)=信号层** (e.g. `esp32MiniRequire.md`). `--gnd-layer 15 --power-layer 16` (defaults); `--gnd-plane=false` keeps GND a plain signal-layer pour. **Validated on ceshi: DRC 31 → 0, No-Connection → 0** — dedicated planes solve what a shared 2-layer pour can't (two power nets stranding each other's pads). Run AFTER auto-place + outline-fit + route-short (signals). Two power nets sharing one plane layer re-create the conflict (warned) — give each its own inner layer on 6+ layers. `--dry-run` prints the net→layer plan.

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

The board outline is a **prerequisite** for layout (edge keep-out, connectors-to-edge,
mounting holes are all relative to it). If the customer has an outline spec, build it
first; otherwise draft a layout, then define an outline around it.

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
