---
name: easyeda-pcb
description: EasyEDA PCB automation skill. Use when working with EasyEDA PCB documents through the easyeda-agent CLI or daemon — switching to a PCB, reading components/layers/nets/board context, syncing components from the schematic (import changes), and laying out components (move, rotate, flip, align, distribute, grid-snap, and net-cluster auto-arrange). PCB design rules live in the sibling easyeda-conventions skill.
---

# EasyEDA PCB

Drive `easyeda-agent` typed actions. Run `easyeda actions` for the live machine-readable
list. Prefer typed actions; only fall back to `debug.exec_js` when a typed action is
missing **and** the user explicitly accepts a debug path.

> **PCB design rules live in the sibling [`easyeda-conventions`](../easyeda-conventions/SKILL.md)
> skill** — especially [`pcb-layout-conventions.md`](../easyeda-conventions/references/pcb-layout-conventions.md)
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
- `pcb.drc.rules` — read the active PCB's **DRC rule configuration** (clearances, track widths, via sizes, …) **without running a check**. Use to feed real rule values into layout reasoning / gates, or to see what `pcb.drc.check` enforces.

### Routing (copper tracks + vias)

Real routing primitives — **additive creates** (no confirm), like the schematic
`wire.create`. Bind to a net **by name** (pull from `pcb.nets.list`); layer ids from
`pcb.layers.list`. EasyEDA's `create()` is **lenient** — it can return no primitive on a
bad layer/coords without throwing, so each action verifies a primitive came back and
fails honestly otherwise. **No PCB autosave yet** (autosave is schematic-only) → **save
explicitly** after routing. There is **no one-call autorouter** on this build
(`pcb_Document.autoRouting` is undefined — see `docs/ecosystem-survey.md` §6); route
segment-by-segment, or use the file-exchange autoroute flow.

- `pcb.line.create` — a copper **track** (导线): line segment on a copper layer
  (`TOP=1`, `BOTTOM=2`; **inner-copper ids are higher** — `id 3` is silkscreen, not
  copper, so read real ids from `pcb.layers.list`) between `(startX,startY)` and
  `(endX,endY)` (mil, y-up), `lineWidth` (default 6 mil), optional `net`. Verify with
  `pcb.drc.check`.
- `pcb.via.create` — a **via** (过孔) at `(x,y)` with `holeDiameter` (drill, default 12
  mil) + `diameter` (outer pad, default 24 mil), optional `net`.

### Schematic → PCB sync + component CRUD

- `pcb.import_changes` — **sync components/netlist from the schematic** (从原理图导入变更). The primary way parts arrive on the board: ensures a Board links SCH+PCB, then `importChanges`, then recomputes ratlines. **Mutates the board; confirm first.** Returns `imported:false` (with a reason) for a floating/unlinked PCB.
- `pcb.component.modify` — move (x/y), rotate, flip layer (top/bottom), lock, designator/BOM flags.
- `pcb.component.delete` — delete component primitives. **Confirm first** (no undo).

### Layout adjustment (deterministic — EasyEDA exposes no align/grid API)

- `pcb.align` — `mode = left | right | top | bottom | centerX | centerY` (y-up: `top` = larger y), aligned to the group extent.
- `pcb.distribute` — even center spacing, `axis = x | y`, extremes fixed.
- `pcb.grid_snap` — round component anchors to `grid` (mil; SMD 25, THT 50).
- `pcb.components.move` — translate a group by relative `dx` / `dy`.
- `pcb.components.arrange` — coarse auto-layout **seed** (priority P6): `mode=cluster` groups by shared local nets then grid-packs each cluster into a tidy non-overlapping block; `mode=grid` packs a flat grid. Skips locked parts.

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
[`pcb-layout-conventions.md`](../easyeda-conventions/references/pcb-layout-conventions.md)
(**P0 mechanical/enclosure > P1 safety/isolation > P2 EMI hot-loop + critical decoupling >
P3 reference-plane/return > P4 thermal keep-out > P5 functional grouping > P6 DFM >
P7 grid/align/silkscreen** — P7 is cosmetic and never overrides a function-driven position).

Operational order:

1. **Read state** — `pcb.components.list` (`includeBBox`+`includePads`) + `pcb.layers.list` (`copperLayerCount`) + `pcb.nets.list`; classify each part by net/designator (anchor / hot / sensitive / IC / passive).
2. **P0** — place connectors (J/USB) and mounting holes (H/MH) at enclosure coords and **`lock`** them; treat as immovable obstacles; edge connectors open outward.
3. **P6 coarse seed** — `pcb.components.arrange mode=cluster` for an initial net-clustered layout.
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
