
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

- `board.list` / `board.current` — all boards (name + bound schematic + pcb) / the current one.
- `board.create` — bind a schematic and/or PCB into a new board (`--schematic` / `--pcb`). The fix for a floating/unlinked PCB before `import_changes`.
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
- `pcb.drc.rules` — read the active PCB's **DRC rule configuration** (clearances, track widths, via sizes, …) **without running a check**. Use to feed real rule values into layout reasoning / gates, or to see what `pcb.drc.check` enforces. The daemon parses the (deeply-nested, untyped) result into `{clearance, trackWidth, trackWidthMin, viaDrill, viaDiameter}` in mil (`internal/app/pcb_rules.go`); `route-short`/`auto-place` consume it so they conform to the board's spec.
  > **Fab-rule baseline: [`fab-rules-jlcpcb.json`](fab-rules-jlcpcb.json)** — the canonical JLCPCB fabrication capabilities (min trace/space, via drill+pad, annular ring, copper-to-edge, silk, by layer count + copper weight), captured from JLCPCB's published capabilities. JLCPCB is the fab behind EasyEDA Pro, so a live board's `pcb.drc.rules` converges with this file's **recommended** column (verified on ceshi: clear 6mil / width 10mil / via 0.3–0.6mm). **Always prefer the live rule; use this JSON as the fallback seed + as clamp floors** (never emit a track/via/gap below the `manufacturingMin`). `pourToEdgeMil`(8) is the pour-inset floor; controlled impedance is intentionally omitted (not derivable from platform data — see task #27).

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
  copper layer, bound to a `net`. `fill = solid` (default) `| grid | grid45`. Size it to
  the board outline; verify `poured:true` + `pcb.drc.check`.
- `pcb.pour.list` / `pcb.pour.delete` — inspect / remove pours.
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

- `pcb region create` (`pcb.region.create`) — region from a closed `--points` polygon
  on a `--layer`. `--rule` (repeatable, name or enum number): `no-components(2)` /
  `no-wires(5)` / `no-fills(6)` / `no-pours(7)` / `no-inner-electrical(8)` /
  `follow-rule(9)`. **Default** (no `--rule`) is a hard keep-out
  `[no-components, no-wires, no-pours]` — the antenna / board-edge case. `--locked`
  pins it. Verify with `pcb region list` + `pcb drc`.
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

- `pcb fill create` (`pcb.fill.create`) — fill from a closed `--points` polygon on a
  `--layer`, bound to `--net`. `--fill-mode solid` (default) `| mesh | inner`. `--locked`.
  Verify with `pcb fill list` + `pcb drc`.
- `pcb fill list` / `pcb fill delete` — inspect / remove (filter list by `--layer`/`--net`).

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
- `easyeda pcb route-short` — **short-trace self-router** (daemon-side, the heuristic tier — NOT `pcb autoroute`/Freerouting). Per net: MST over pads, then a track per hop ≤ `--max-len` (Manhattan) on the pads' shared layer. Skips GND (poured; `--route-gnd` to include), already-routed nets, cross-layer hops (need a via), over-long hops (maze tier). **Widths are rule-aware**: by default signal + power widths are seeded from the board's live DRC track-width spec (`pcb.drc.rules`, clamped ≥ the rule minimum) so tracks conform instead of the old hardcoded 10/20 mil; `--width-signal`/`--width-power`/`--width` still override. **Corner style** via `--corner`: `90` (Manhattan L, default), `45` (chamfer — avoids acid traps/reflections), `round` (chord-approximated fillet, `--round-radius`; native arcs don't commit on this build so it's segmented). No obstacle avoidance in v1 — **run after `auto-place`** so hops are short/clear, then `pcb drc`. `--dry-run` previews. Long/congested/any-distance routing → `pcb autoroute` (external Freerouting).

#### 待支持 — 布线/覆铜质量 (roadmap, not yet implemented)

v1 (`route-short` / `pour`) is mechanically correct but coarse. Planned quality upgrades:

- ✅ **填充区域 / 轮廓对象 (net-bound filled region, 异形大块铜)** (task #17, done) — `pcb fill create`
  (`eda.pcb_PrimitiveFill`, net-bound static copper). See the "Net-bound filled region" section above.
- ✅ **DSN keep-out injection** (task #17, done) — `pcb export-dsn` re-injects `pcb_PrimitiveRegion`
  keep-out as `(keepout (polygon …))` into the DSN `(structure)` (getDsnFile drops them). Default on;
  `--raw` skips. End-to-end Freerouting *honor* check is part of the #5 maze-tier toolchain.

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
