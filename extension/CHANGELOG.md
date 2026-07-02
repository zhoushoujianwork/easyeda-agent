# Changelog

All notable changes to the **EasyEDA Agent Connector** are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/); versions
follow [SemVer](https://semver.org/).

## [Unreleased]

### Fixed
- **`pcb new-board` no longer silently steals the schematic.** A schematic can belong
  to only ONE Board in EasyEDA Pro, so `createBoard(schematicUuid)` on an already-bound
  schematic *moves* it into the new board — leaving the old board with just its PCB
  ("原理图没了"). `board.new_pcb` now detects an already-bound schematic and refuses with
  a clear error naming the owning board; pass `--force` (`force: true`) to move it
  deliberately.
- **`board list` / `pcb board-info` no longer crash on a PCB-only or schematic-only
  Board.** `serializeBoard` read `board.schematic.uuid` / `board.pcb.uuid`
  unconditionally, throwing `Cannot read properties of undefined (reading 'uuid')` for
  any board missing one side (exactly the orphaned boards the old `new-board` produced).
  It now emits `null` for the missing side.

## [0.7.0] - 2026-07-02

The market-ready PCB pass since v0.6.0 — a reconstructed **PCB DFM audit** (`pcb check`),
a full **silkscreen suite**, the verified **4-layer inner-plane** recipe, and a reconnect
UX fix. (Consolidates the dev-loop releases 0.6.1–0.6.7 below.)

### Added
- **`pcb check` — reconstructed DFM audit** (the PCB sibling of `sch check`; catches the
  design-for-manufacture problems the native `pcb drc` clearance check does NOT flag).
  Copper rules compute purely Go-side from placed copper and never mutate:
  **dangling-end** (a track end anchored to nothing → floating copper), **acute-angle**
  (same-net segments bending <90° → acid trap), **non-orthogonal** (a track off the
  0/45/90° grid → free-angle routing), **track-over-pad** (a track crossing a pad it
  doesn't terminate on: cross-net = ERROR short), **overlapping-via** / **single-layer-via**,
  **width-mismatch**, **duplicate-segment**, and **3W parallel-coupling**; plus
  **silkscreen-flipped** (a designator on the wrong silk layer / mirrored / non-upright)
  and per-layer **antenna-keepout** (an antenna module lacking a no-copper keep-out on
  every copper layer). `--strict` exits non-zero on any WARN/ERROR (gate-able).
- **Silkscreen suite** — `pcb silk-add` (a FREE silkscreen string: board credit / LED
  `+`/`−` polarity marks; configurable layer/font/stroke/rotation, JLCPCB-legible
  defaults), `pcb silk-set` (batch-adjust existing silk + an **align-to-reference**
  shortcut: center a board credit, align a label to a component/board/fill edge), and the
  read handlers `pcb.silk.list` (text layer/mirror/reverse/rotation) + `pcb.region.list`
  bbox that feed the DFM checks.
- **`easyeda pcb new-board` (`board.new_pcb`)** — create a brand-new board (板) with a
  fresh EMPTY PCB page bound to a schematic (CLI equivalent of the UI 新建PCB /
  原理图转PCB), then `pcb import-changes` to lay it out from scratch. Distinct from
  `board.create` (link-only). Runs the required 2-step SDK sequence that is otherwise a
  silent no-op — `createBoard(schematicUuid)` mints a board shell, then
  `createPcb(boardName)` adds the PCB INTO it — with shell rollback on failure.
  `--schematic` defaults to the current board's schematic.
- **`easyeda notify` (`system.notify`)** — show a non-blocking toast INSIDE the EasyEDA
  window so the design flow can announce each stage live ("完成 布线,下一步 铺铜").
  `--type info|success|warn|error|question`, `--duration`.

### Changed
- **`pcb silk-align` → position-aware (v2)** — ranks each designator's 4 sides by local
  free space + board position + a crowd-axis bonus, and avoids **other parts' pads**,
  bodies, keep-out regions, the board outline (now resolves rounded/polyline outlines),
  and other labels; keeps assembly clearance around each footprint (`--spacing`); a
  boxed-in part is reported (`unresolved`), never shoved onto a pad.
- **`pcb power-planes` flips the GND inner layer to 内电层/PLANE** after pouring (verified
  pour-while-SIGNAL → flip-type → rebuild recipe, DRC clean), matching the common customer
  stackup GND=内电层 / VCC=信号层. Drove the ESP32 regression board DRC 31→0, No-Connection→0.
- **`pcb auto-place --assembly-gap` (default 40 mil)** floors the chip-to-satellite gap at a
  hand-SOLDER clearance, not just the DRC routing clearance (~28 mil packed too tight to
  reach with an iron). **`pcb check` antenna-keepout now recognizes a single MULTI-layer(12)
  region** as covering every copper layer — one 多层 keep-out replaces the per-layer set.
  design-flow.md PCB pipeline reordered so keep-out regions + silk-align run BEFORE routing
  (post-hoc keep-outs forced re-routing).

### Fixed
- **Reconnect toast dedup** — one toast per daemon outage instead of one on every 3s retry
  (they were stacking and covering UI options during an outage).

### Docs
- README split into a Chinese homepage (`README.md`) + English (`README.en.md`); new demo
  recording storyboard `docs/demo-storyboard-esp32-mini.md`; FEATURES action count 85→88;
  official-marketplace coverage survey (`docs/marketplace-coverage.md`).

## [0.6.7] - 2026-07-02
### Fixed
- **silk-align: labels no longer crowd their OWN pads** — the body used for the offset
  is inflated by an assembly-clearance floor (Cassembly=10 mil) and own-pad overlap now
  carries a penalty, so a designator keeps solder-iron room around its footprint instead
  of touching the copper. New **`spacing`** coefficient (default 1.5, `--spacing`) scales
  the label drift for more/less assembly room; base offset default 12→15; other-pad
  margin Cpad 8→12.
- **`--ref board`/`outline` now resolves rounded/closed outlines** — the board outline is
  often a single `pcb_PrimitivePolyline` on layer 11, which the Line/Arc-only resolver
  missed (silk-set align + silk-align safeArea both use the new shared `boardOutlineIds`).

## [0.6.6] - 2026-07-02
### Changed
- **`pcb.silk.align` is now POSITION-AWARE** (designed via a 3-lens workflow). It ranks
  each designator's 4 sides by local free space + board position (edge parts pulled
  inward, never off-board) + a crowd-axis bonus (dense stacks pushed perpendicular),
  and — the core fix — avoids **other parts' PADS** (a label over exposed copper is
  fab-clipped; this is why C1's designator no longer lands on C2's pad), bodies,
  keep-out regions, the board outline, and other labels. Most-constrained-first order;
  bottom parts → bottom silk + mirror; a boxed-in part is left + reported (unresolved)
  rather than moved onto a pad. New outputs: warned / unresolved.
### Added
- **`pcb.silk.set` gains an ALIGN shortcut** — `align` (center|mid|centerx|centery|
  left|right|top|bottom) + `ref` (a component designator, "board"/"outline", or "fill")
  positions each silk relative to that reference bbox (e.g. center the board credit,
  align a label to a component edge), computed from the silk's own bbox.

## [0.6.5] - 2026-07-02
### Fixed
- **Reconnect toast spam / UI obscuring.** During a daemon outage the connector
  toasted "Daemon not found, retrying (n/5)" on EVERY fast retry (every 3s), so the
  toasts stacked and covered UI options ("one starts before the last ends"). Now it
  toasts **once per outage** (on the first failed scan) and retries **silently** in
  the background; the retry cadence (fast 3s → slow 10s) and reconnect speed are
  unchanged, and the eventual reconnect still announces once.

## [0.6.4] - 2026-07-02
### Added
- **`pcb.silk.add`** — create a FREE silkscreen STRING (board marking / credit / note)
  at (x,y) with config: layer (3=top / 4=bottom), fontSize, lineWidth, rotation.
  Legible JLCPCB-safe defaults (font 40 / stroke 6) — a small font with a thick stroke
  smears the glyphs. Returns primitiveId + rendered bbox.
- **`pcb.silk.set`** — batch-reconfigure existing silkscreen primitives (designator/
  value ATTRIBUTES + free STRINGS): primitiveIds[] + any of x/y/rotation/fontSize/
  lineWidth/text; only the given keys change. Uses the reliable `.modify(id,props)`
  (setState_Rotation alone does NOT persist). The batch position/orientation/size fixer
  behind correcting badly-placed or non-upright silk.

## [0.6.3] - 2026-07-02
### Changed
- **`pcb.region.list`** now emits each region's **`bbox`** (`getPrimitivesBBox`), so
  the daemon's new `pcb check` **antenna-keepout** rule can test whether a no-copper
  keep-out region actually overlaps an antenna module's footprint. Rule types are
  already reported, so the check reads no-copper = any of no-wires/no-fills/no-pours/
  no-inner-electrical.

## [0.6.2] - 2026-07-02
### Changed
- **`pcb.silk.list`** now also emits each text's **`reverse`** (`getState_Reverse` —
  left/right reversed reading) and **`rotation`** (`getState_Rotation`, degrees).
  `getState_Mirror` alone missed real "放反" cases: a designator rotated 180°
  (upside-down) or 90/270° (sideways) has `mirror=false` but doesn't read upright.
  The daemon's `pcb check` **silkscreen-flipped** rule now flags a reference
  designator (`key == "Designator"`) whose orientation isn't upright, and treats
  `mirror OR reverse` as "reads backwards". (`getState_HorizonMirror` does not exist
  on text primitives — confirmed via runtime probe.)

## [0.6.1] - 2026-07-02
### Added
- **`pcb.silk.list`** — read-only enumeration of every SILKSCREEN TEXT primitive:
  component designator/value ATTRIBUTES (`pcb_PrimitiveAttribute`) plus free STRINGS
  (`pcb_PrimitiveString`), each with its silk layer (3=TOP_SILKSCREEN /
  4=BOTTOM_SILKSCREEN), mirror flag, text, position, and (for attributes) the parent
  component's id + side (TOP/BOTTOM). Feeds the daemon's `pcb check`
  **silkscreen-flipped** rule — top silk must read un-mirrored, bottom silk must be
  mirrored, and a designator's silk side must match its component's side; a mismatch
  is a flipped/back-side silkscreen (丝印放反). The PCB component primitive itself has
  no `getState_Mirror`, so orientation is read from the text primitives, not the
  component.

## [0.6.0] - 2026-07-01
> PCB automation milestone (tasks #21–#32). Connector-side changes below; the bulk
> of the release is DAEMON-side (Go CLI) PCB automation, summarized under "Daemon".
### Added
- **`pcb.silk.align`** (task #30) — reposition each component's DESIGNATOR silkscreen
  with COLLISION AVOIDANCE: searches candidate slots around each footprint (preferred
  `side` first, then other directions at increasing distance) and takes the first that
  hits no other component body and no already-placed label — dense-cluster designators
  get pushed into open space instead of piling up. The designator is a component-bound
  attribute (pcb_PrimitiveString is empty), repositioned via
  `pcb_PrimitiveAttribute.getAllPrimitiveId(componentId)` + `.modify(id,{x,y})`.
  Reports `unresolvedCollisions`. CLI: `pcb silk-align`.
- **`pcb.stackup.set`** (task #26) — configure the board stackup: set the copper
  layer count (2/4/6/…/32 via `setTheNumberOfCopperLayers`) and/or set inner layers'
  type SIGNAL↔PLANE (内电层, via `modifyLayer`). A PLANE inner layer gives GND/power
  a dedicated plane on 4+ layer boards — the clean fix for the 2-layer pour conflict
  where two power nets can't both connect on one shared layer. Read via
  `pcb.layers.list`. CLI: `pcb stackup set --layers 4 --plane 15 --plane 16`.

### Fixed
- **Connector auto-reconnect wedge (需要重开窗口才恢复)** — after a daemon restart
  (dev hot-reload) or a long window-backgrounding, `isConnecting` could leak `true`
  and freeze EVERY reconnect path at once: the watchdog tick, the port scan, AND the
  focus/online/visibility wake listeners all early-returned on `isConnecting`, so
  only fully reopening the EasyEDA window recovered. Now (1) the watchdog
  force-resets a connect flow still unsettled after ~24s (`STUCK_CONNECTING_TICKS`),
  and (2) the foreground/online wake forces a clean reconnect *through* a stuck
  `isConnecting` (`cancelConnectionFlow()` first) instead of being blocked by it.

### Daemon (CLI) — PCB automation pass
All real-machine verified on the ESP32 regression board; each `easyeda pcb …` subcommand:
- **Rule-aware** `route-short` / `auto-place` / `pour` — read the board's live DRC rule
  (`pcb drc-rules`) and conform (widths/clearance/via/copper-to-edge) instead of hardcoding;
  fall back to a canonical **JLCPCB fab-rule reference** (real per-board-type exports). (#22/#32)
- `route-short` **v2**: obstacle-aware L-orientation, and **skips power/ground nets** by
  default (they belong in a pour — routing 3V3 as thin tracks was the #1 DRC source). (#23)
- `pcb outline-fit` (tighten to parts) / `pcb outline-round` (rounded-rect outline). (#21/#29)
- `pcb layout-lint` — placement quality + **routability score** (ratsnest MST + crossings). (#25)
- `pcb power-planes` — **4-layer** power distribution: GND + power on dedicated inner planes
  + via-stitch each pad (drove the regression board's No-Connection to 0). (#26)
- `pcb region` / `fill` / `slot` — antenna keep-out (禁铺铜) & board cutout (挖槽). (#28)
- Confirmed platform walls (no `eda.*` API): teardrops, controlled-impedance, interactive routing.

## [0.5.30] - 2026-06-30
### Added
- **`pcb.add_component`** (task #20) — add ONE part to an EXISTING PCB and wire it,
  the working alternative to `pcb.import_changes` (which is a no-op for API-added
  parts). Places the footprint (`pcb_PrimitiveComponent.create`), links it to its
  schematic twin (uniqueId + designator), assigns each pad's net from a caller-
  supplied `nets` map (`pcb_PrimitivePad.modify` — the step that actually wires it,
  since net→pad assignment is otherwise part of the broken import flow), and
  recomputes ratlines. CLI: `easyeda pcb add-component`. `schematic.read` now also
  returns each component's `uniqueId` (the sch↔PCB link key to pass in).
### Investigated
- `eda.pcb_Document.importChanges` does NOT sync API-added components to an existing
  PCB (returns true, count unchanged) — root-caused to incremental-add being a
  platform no-op; superseded by `pcb.add_component`.

## [0.5.29] - 2026-06-30
### Added
- **One-call circuit snapshot** (task #7): `schematic.read` returns a coherent
  semantic model in a single round-trip — components (each pin tagged with its
  JSON-authoritative net from `getNetlistFile`), nets (net → connected pins +
  degree + power/ground flag), floating pins, and the geometric design check
  (`includeCheck:false` to skip). Replaces the agent stitching `components.list` +
  `netlist` + `check`. CLI: `easyeda sch read` (`--all-pages`, `--no-check`).

## [0.5.28] - 2026-06-30
### Fixed
- **Auto-reconnect no longer needs a window "nudge".** The heartbeat/reconnect loop
  ran on a main-thread `setInterval`, which EasyEDA's webview freezes when the
  window is backgrounded — so after a daemon restart (e.g. `make dev` rebuild) the
  connector stayed dead until the user focused the window. A new **watchdog** drives
  both the heartbeat and reconnect from a **Web Worker** timer (which keeps firing
  while backgrounded); it falls back to a main-thread interval + `focus`/`online`
  listeners if the webview blocks workers. An explicit Stop now sets a `suspended`
  flag so the always-on watchdog doesn't reconnect behind the user's back.

## [0.5.27] - 2026-06-30
### Added
- **Net-bound filled region** (task #17): `pcb.fill.create` / `pcb.fill.list` /
  `pcb.fill.delete` (`eda.pcb_PrimitiveFill.*`) — a STATIC filled polygon bound to a
  net (3V3/RF-ground patch, thermal copper, odd-shaped plane). `fillMode = solid
  (default) | mesh | inner`. Distinct from `pcb.pour.create` (覆铜, reflows around
  obstacles) and `pcb.region.create` (keep-out, no net). CLI: `easyeda pcb fill
  create / list / delete`.

## [0.5.26] - 2026-06-30
### Added
- **DSN keep-out injection** (task #17): `pcb.export.dsn` now splices keep-out
  regions (禁止区域) back into the exported DSN by default — `getDsnFile` DROPS
  `pcb_PrimitiveRegion`, so a raw export had `keepout = 0` and Freerouting would
  route under the antenna. Each routing region (no-wires/no-fills/no-pours) becomes
  a Specctra `(keepout (polygon …))` in the `(structure)` section. Transform is a
  verified pure translation (1:1 mil, no flip; offset = DSN-boundary-min −
  outline-bbox-min). Result reports `keepouts = N`. CLI `easyeda pcb export-dsn`
  gains `--raw` for the unmodified export.

## [0.5.25] - 2026-06-30
### Added
- **PCB keep-out / rule regions** (task #11): `pcb.region.create` / `pcb.region.list`
  / `pcb.region.delete` (`eda.pcb_PrimitiveRegion.*`). A polygon carrying rule types
  — `no-components(2)` / `no-wires(5)` / `no-fills(6)` / `no-pours(7)` /
  `no-inner-electrical(8)` / `follow-rule(9)`; default is a hard keep-out
  `[no-components, no-wires, no-pours]` for antenna clearance / board-edge inset.
  NOT net-bound filled copper (that's `pcb.pour.create`). CLI: `easyeda pcb region
  create / list / delete`. (DSN keep-out injection for the Freerouting maze tier is a
  separate follow-up — `getDsnFile` drops regions.)

## [0.5.24] - 2026-06-29
### Added
- **Freerouting round-trip building blocks** (task #5): `pcb.export.dsn`
  (`getDsnFile` → Specctra DSN artifact, the autorouter input), `pcb.import_autoroute`
  (`importAutoRouteSesFile`/`importAutoRouteJsonFile`, base64 in, recomputes ratlines),
  and `pcb.snapshot` (`getCurrentRenderedAreaImage` for the PCB canvas — the PCB
  counterpart to `schematic.snapshot`). Enables the file-based autoroute workflow
  `pcb export-dsn` → run Freerouting → `pcb import-autoroute route.ses` without the
  @alpha `autoRouting()`. CLI: `easyeda pcb export-dsn / import-autoroute / snapshot`.

## [0.5.23] - 2026-06-29
### Added
- `schematic.check` now reports **stray wires** the SDK DRC and layout-lint both
  miss: `dangling-wire` (a segment whose vertices touch no pin, net-flag/port/label,
  or other wire — e.g. a stub left behind when its pin/flag was deleted) and
  `zero-length-wire`. Each finding carries the `wirePrimitiveId` so it can be
  removed with `sch prim-delete`. Summary gains `zeroLengthWires` / `danglingWires`.

## [0.5.22] - 2026-06-29
### Fixed
- Net-flag/net-port **vertical (up/down) body orientation** on the y-DOWN build:
  `connect_pin --direction down` ground (and `--direction up` power) flags rendered
  their body toward the pin instead of away. Root cause was the orientation table's
  up/down entries being derived in a y-UP frame; `ROTATION_CYCLE` is now
  `up→right→down→left` with power/ground anchors swapped (left/right unchanged).
  Verified via `getPrimitivesBBox` on real placed flags + `calibrate.js` (whose own
  y-frame was fixed). See `orientation.json` _doc.

## [0.5.20] - 2026-06-29
### Fixed
- `schematic.drc.check` now treats boolean SDK results as first-class normalized
  output instead of assuming the verbose overload always returns an array. This
  matches current EasyEDA runtime behavior for `SCH_Drc.check`.
- `schematic.check` now reconstructs additional UI-like warnings for schematic
  validation: net-marker/wire-name mismatches and multi-net wires.
- Floating-pin detection now cross-checks the official manufacture netlist JSON
  (`sch_ManufactureData.getNetlistFile`) before reporting a pin as floating.
- Net-marker checks now dedupe repeated wire/marker segment matches and only treat
  a marker as attached when it touches a wire vertex, reducing false positives from
  malformed merged polylines.

### Changed
- CLI and skill docs now distinguish the official SDK DRC gate (`sch drc`) from
  the reconstructed per-item checker (`sch check`).

## [0.5.18] - 2026-06-28
### Added
- **PCB routing roadmap R1 (copper pour) + R2 (rip-up/list)** from
  `docs/ecosystem-survey.md §7` — 8 new actions, d.ts-grounded + adversarially reviewed:
  - `pcb.pour.create` / `pcb.pour.list` / `pcb.pour.delete` / `pcb.pour.rebuild` —
    **copper pour (铺铜)**. create takes raw `points`; the connector builds the
    `IPCB_Polygon` via `pcb_MathPolygon.createPolygon` (the missing piece behind the old
    "无法创建覆铜边框图元" failures — you must pass a polygon object, not raw points),
    then `rebuildCopperRegion()` computes the fill. `fill = solid|grid|grid45`. CLI
    `easyeda pcb pour / pour-list / pour-delete / pour-rebuild`.
  - `pcb.route.rip_up` — **reliable rip-up** (getAll → filter → delete on stable
    primitive APIs, the official kirouting pattern). Deletes tracks+arcs+vias on
    **copper layers only** (TOP/BOTTOM/INNER) — never the board outline,
    silkscreen/assembly/mechanical artwork, or **locked** primitives. `--net` scopes;
    omit = all. CLI `easyeda pcb rip-up`.
  - `pcb.line.list` / `pcb.via.list` — read routed tracks/vias. CLI `pcb track-list` /
    `pcb via-list`.
  - `pcb.clear_routing` — wraps native `clearRouting` (`@alpha`, may be undefined;
    prefer `pcb.route.rip_up`). CLI `easyeda pcb clear-routing`.
  - Smart/interactive routing (single/multi/diff routing, stretch, optimize,
    length-tuning, fanout) has NO `eda.*` API — documented as a hard boundary (§7).
- **Five actions absorbed from the official open-source extension ecosystem**
  (see `docs/ecosystem-survey.md`), each grounded in `pro-api-types` signatures:
  - `schematic.library.get_by_lcsc` — resolve LCSC C-numbers directly to
    `{libraryUuid, uuid}` via `eda.lib_Device.getByLcscIds` (deterministic, no
    free-text ranking; reports `notFound`). CLI `easyeda lib by-lcsc --lcsc C…`.
  - `pcb.line.create` — create a copper track via `eda.pcb_PrimitiveLine.create`
    (mutating). CLI `easyeda pcb track`.
  - `pcb.via.create` — place a via via `eda.pcb_PrimitiveVia.create` (mutating).
    CLI `easyeda pcb via`.
  - `pcb.report` — read-only design report (per-net length, net-class totals,
    differential-pair skew, equal-length spread) over `eda.pcb_Net.getNetLength` +
    `eda.pcb_Drc.getAll{NetClasses,DifferentialPairs,EqualLengthNetGroups}`. CLI
    `easyeda pcb report`.
  - `pcb.drc.rules` — read `eda.pcb_Drc.getCurrentRuleConfiguration` without
    running a check. CLI `easyeda pcb drc-rules`.
  - **Live-verified on a real board (PCB1, connector 0.5.15):** A1 resolves
    C6186→AMS1117-3.3 identity; A5 returns the full rule config; A3 reports 4 nets
    with length/net-class/diff/equal-length; A2 creates a GND track (net length
    read back 0→500, confirming it bound to the right net); `pcb drc` + save pass.
- **`pcb.save` — save the active PCB to disk** (`eda.pcb_Document.save`), the PCB
  counterpart to `schematic.save`. CLI `easyeda pcb save`. **PCB autosave is now
  on:** the daemon's debounced autosave fires `pcb.save` after a PCB-mutating
  action, closing the in-memory-edit data-loss gap that previously only schematic
  edits were protected from (`saveActionForDocType` now maps `pcb`→`pcb.save`).

### Fixed
- **`pcb.outline.set` now creates the REAL board-outline object (类型=板框), not loose
  lines.** Root cause of "the outline vanished when I cleared routing" + "DRC doesn't
  flag out-of-board": the outline was drawn as N separate `pcb_PrimitiveLine`s on
  layer 11. A loose line on the board-outline layer is just a wire that happens to sit
  there — EasyEDA does NOT treat it as the board boundary (DRC ignores it for
  enclosure, the UI "清除布线 / clear routing" deletes it). Compared a UI-drawn 板框
  against ours: the real outline is ONE `pcb_PrimitivePolyline` whose `polygon` is an
  `IPCB_Polygon`. Fix: build the closed-polygon source `[x0,y0,'L',…,x0,y0]` →
  `eda.pcb_MathPolygon.createPolygon` → `eda.pcb_PrimitivePolyline.create('', 11,
  polygon, lineWidth, /*lock*/true)` — one locked polyline. `pcb.outline.get/clear`
  updated to read/delete the polyline (bbox from its rendered extent; legacy lines
  still handled). Default lineWidth 10mil. Returns `outlineId`. Create flow verified
  live (createPolygon + polyline produced a 类型=板框 object matching the UI's).
- **`view region` + `schematic.snapshot --no-fit` now reliably captures the
  requested local region (issue #20).** Three coordinated fixes: (1) the snapshot
  handler now waits for the canvas to repaint (two `requestAnimationFrame`s with a
  timeout fallback) BEFORE reading the frame, so a preceding `view region` viewport
  has actually landed — previously `--no-fit` grabbed the pre-region frame because
  EasyEDA does not synchronously repaint after `eda.*` view calls (the `--fit` path
  only "worked" by accident, since `zoomToAllPrimitives` nudged a redraw). (2)
  Built-in stale-frame detection: the snapshot result now exposes the frame
  `sha256`; thread it back via `sch snapshot --previous-sha256 <sha>` and the
  connector detects a byte-identical (stale) frame, retries once after another
  redraw, and reports `stale`/`staleRetry`. (3) `view.region` now normalizes the
  rectangle (sorts each axis to min/max) and rejects a zero-area box, so a
  reversed/degenerate bound no longer renders as a tiny sliver in a blank frame;
  `view region` CLI help documents the y-DOWN schematic axis semantics and units.
- **`schematic.power.connect_pin` (`sch connect`) `--direction up/down` no longer
  inverts the stub/netport endpoint.** EasyEDA Pro schematic coords are y-DOWN (a
  larger stored y renders LOWER on screen, verified on 3.2.121, issue #19), but the
  endpoint math assumed y-UP, so `--direction up` pushed a top-pin stub DOWN into the
  IC body and `--direction down` pushed a bottom-pin stub UP — visually wrong even
  when DRC was clean. `up` now decreases y (visually higher) and `down` increases y
  (visually lower). The flag-rotation table is unchanged: it is calibrated against
  real rendered bbox and already keyed to visual directions, so the corrected
  endpoint and the flag orientation now agree (callers no longer need the
  `--direction down --rotation 90` workaround to get a visually-upward netport).
- **`schematic.check` no longer false-flags merged-stub endpoints as `wire-over-pin`,
  and floating-pin findings now carry component-level detail.** A pin coincident with
  a wire endpoint or a netflag/netport/netlabel anchor is the legitimate terminus of
  its own `sch connect` stub; when EasyEDA auto-merges collinear touching stubs into
  one long wire an inner pin lands in that wire's interior and was wrongly reported as
  a through-pin short (the official DRC stays clean). Rule 3 now excludes pins that
  coincide with a wire vertex or a net-marker anchor. Floating-pin findings now include
  `primitiveId` and a `pinDetails[]` array (`number`, `name`, `x`, `y`) so the `--json`
  report identifies the component and pin without a second lookup; the text report
  prints the per-pin name + coordinates and falls back to `primitiveId` when the
  designator is empty.

## [0.5.14] - 2026-06-28
### Fixed
- **`schematic.pin.set_no_connect` no longer reports a false success.** On EasyEDA
  Pro 3.2.x, `pin.setState_NoConnected` is a **no-op** — the pin primitive has no
  `noConnected` field (verified by re-pull, DRC re-run, and a canvas snapshot: no
  非连接标识 is ever placed and DRC still treats the pin as floating). The setter is
  typed `@public`, so the prior implementation compiled and returned `ok` while
  silently doing nothing. The handler now **verifies** the write and fails with
  `EDA_CALL_FAILED`, naming it as an EasyEDA platform limitation (not a connector
  defect) and returning `notApplied[]`. It auto-passes if a future build makes the
  setter real. There is no public `eda.*` API to place a 非连接标识 on this version —
  use `schematic.check` to enumerate floating pins.
- **`schematic.wire.create` now normalizes nested `points` (issue #5).** EDA's
  `eda.sch_PrimitiveWire.create` only accepts a **flat** `number[]`
  (`[x1,y1,x2,y2,…]`); a nested `[[x,y],…]` payload failed with
  `EDA_CALL_FAILED / "create failed!"`. The connector now flattens nested points
  at a single source of truth (`normalizeWirePoints` in `util.ts`), so CLI /
  `call` / sch.py / `debug.exec_js` all accept either form. Also validates the
  list is an even-length (`≥4`) run of finite numbers. CLI `sch wire --help` and
  `auto-layout-sop.md` updated to document both forms.

### Added
- **`schematic.check` — reconstructed per-item design check + routing-quality
  rules.** The EDA schematic DRC API (`eda.sch_Drc.check`) returns only an aggregate
  `{count,type}` and `layout-lint` only sees component bbox overlap; this fills both
  gaps by computing findings geometrically from primitives. Rules: (1) **floating
  pins** — a pin is connected iff a wire touches its coordinate (NC-marked excluded),
  grouped by component as `{designator, pins[]}` (the exact input
  `schematic.pin.set_no_connect` takes); (2) **wire-crossing** — two wire segments
  cross in their interiors (a routing tangle; shared endpoints/junctions excluded),
  reported with the intersection point; (3) **wire-over-pin** — a pin sits in a
  wire's interior (EasyEDA trims+connects there → unintended short; enforces the SOP
  "chain pin→pin, don't run a wire through a pin"). Returns `{passed,
  summary{floatingPins,wireCrossings,wireOverPins,…}, findings[]}`. CLI:
  `easyeda sch check` (`--json`, `--strict`, `--all-pages`). Verified live via a
  detect→fix→re-check loop on an ESP32-S3: 2 wire-crossings found and driven to 0.
- **`schematic.drc.check` now returns per-violation detail.** Normalizes the SDK
  result into `{passed, fatal, summary, violations[]}` — each violation projects
  `{level, rule, message, primitiveIds, designators, x, y}` (raw kept) plus a
  severity summary and a `fatal` count for the design-flow S5 gate. CLI `sch drc`
  prints one line per violation and exits non-zero only when `fatal > 0`. NOTE: the
  schematic SDK only provides an aggregate, so detail degrades honestly to
  "N issue(s) — EDA returned no per-item detail" (use `schematic.check` for the
  itemized floating-pin findings).
- **`schematic.snapshot` anti-stale metadata (issue #2).** The snapshot result now
  carries `primitiveCount` (live components + page primitives on the current page),
  `capturedAt` (ISO timestamp), and a `stale` advisory string. EasyEDA does not
  auto-redraw after `eda.*` edits, so `getCurrentRenderedAreaImage` can return a
  byte-identical STALE frame; callers compare `primitiveCount` across two snapshots
  to detect when the image didn't change but the page did. Judge state by data, use
  the screenshot for layout only.
- **`schematic.page.clear` — one-shot page reset.** Deletes every page-level
  primitive on the active page (components, net flags/ports/labels, wires, buses,
  and graphics — arcs/circles/rectangles/polygons/text), not just components.
  `preserveSheet` (default true) keeps the sheet/title block; `dryRun` reports
  per-type counts without deleting. Returns `{deleted:{...}, total, deletedIds}`.
  Fixes the trap where `schematic.component.delete` left wires/buses behind while
  `components.list` reported a clean page, forcing a fall back to raw
  `debug.exec_js`.
- **`schematic.primitives.delete` — generalized, any-type delete.** Routes each
  requested id to its owning `sch_Primitive*` class so wires/buses/graphics/flags
  can be deleted alongside components; omit `primitiveIds` to delete the current
  selection (select-all → delete). Reports `notFound` ids.

## [0.5.8] - 2026-06-27
### Changed
- **Version bump to pair with the daemon/CLI artifact-path change.** No connector
  behavior change vs 0.5.7. The CLI now sends its working directory and the daemon
  writes artifacts (snapshots, netlist/BOM exports) under `<cwd>/.easyeda/artifacts`
  with sortable timestamped names (`<YYYYMMDD-HHMMSS>-<kind>-<short>.ext`) instead
  of a flat `artifacts/art_<uuid>` in the daemon's cwd. Released together to keep
  CLI and connector on the same version.

## [0.5.7] - 2026-06-27
### Added
- **Heartbeat-carried context.** The connector now re-reads the active
  project/document on each heartbeat (~3s) and pushes it to the daemon only when
  it changed. `easyeda daemon health` (and project routing) now reflect a UI
  tab-switch within one interval — previously context refreshed only on connect
  or as a side effect of running an action, so it lagged the UI until the next
  command. The initial post-connect push is unconditional; reconnects reset the
  change-detection signature so they always re-push.

## [0.5.6] - 2026-06-27
### Changed
- **Rebuild to pair with the daemon's live-context + `doc` work.** No connector
  behavior change vs 0.5.5 — this build exists so a window stuck on a stale
  connector can be re-imported to pick up real version reporting + port-scan
  (49620–49629). The daemon now refreshes each window's context from every action
  response (so `health` no longer reads `home` forever) and `easyeda doc ls/switch`
  drives the discover→switch loop on top of the existing `document.open` action.

## [0.5.5] - 2026-06-27
### Fixed
- **Handshake reports the real connector version.** `connectorVersion` was a
  hardcoded `0.1.0`, so `easyeda daemon health` could not reveal which build a
  window was actually running — useless for spotting a stale open window. esbuild
  now injects `extension.json`'s version at build time (`__CONNECTOR_VERSION__`).

## [0.5.4] - 2026-06-27
### Added
- **Board (板子/组合) management** — `board.list`, `board.current`, `board.create`,
  `board.rename`, `board.copy`, `board.delete`. A Board binds one schematic + one
  PCB; these expose `eda.dmt_Board.*` so the schematic↔PCB grouping is editable
  (and a floating PCB can be linked before `import_changes`).

## [0.5.3] - 2026-06-27
### Added
- **Schematic page management** — `schematic.page.create`, `schematic.page.rename`,
  `schematic.page.delete`, `schematic.rename` (`eda.dmt_Schematic.*`).
- **明细表 (title block)** — `schematic.titleblock.get` / `schematic.titleblock.modify`
  to read and adjust the drawing-sheet title block (the editable "图纸" surface;
  EasyEDA Pro exposes no set-paper-size API).

## [0.5.2] - 2026-06-27
### Added
- **Editor view shortcuts** — `view.fit` (适应全部 / `K`), `view.fit_selection`
  (适应选中), `view.zoom`, `view.region` via `eda.dmt_EditorControl.*`; act on the
  focused canvas, shared by schematic and PCB.

## [0.5.1] - 2026-06-26
### Added
- **PCB layout intelligence** — `pcb.components.arrange` (cluster / grid auto-layout
  seed) and rendered bounding boxes in `pcb.components.list`.
- **PCB layout adjustment** — `pcb.align`, `pcb.distribute`, `pcb.grid_snap`,
  `pcb.components.move`.
- **Board outline (板框)** — `pcb.outline.set` / `pcb.outline.get` / `pcb.outline.clear`.
- **PCB DRC** — `pcb.drc.check`, normalized to `{passed, violations}`.

## [0.4.10] - 2026-06-26
### Added
- `homepage` pointing at the GitHub repository (open-source link for the listing),
  as a plain URL (no `#readme` fragment).

## [0.4.9] - 2026-06-26
### Fixed
- Marketplace manifest finalized: `repository.type` is `github` (per the official
  `eext-extension-demo`); removed the optional `bugs`/`homepage` fields — the
  marketplace flagged the `bugs` content and neither field is required. No email
  or other private data ships in the `.eext`.

## [0.4.5] - 2026-06-26
### Added
- `repository` field in the manifest and this `CHANGELOG.md` (marketplace
  submission requirements).

## [0.4.4] - 2026-06-26
### Changed
- Release tooling keeps a **stable UUID** by default, so a new version updates in
  place (uninstall the old entry, then import); a fresh-UUID build is now an
  explicit fallback. No change to the connector's runtime behaviour.

## [0.4.2] - 2026-06-26
### Fixed
- **Self-healing reconnection.** The connector no longer permanently gives up
  after a few failed retries. After the initial fast attempts it falls back to a
  quiet background poll, so a daemon that is started or restarted *after* the
  editor auto-connects with no manual **Reconnect**. A connection lost to a daemon
  restart also recovers on its own.

## [0.4.0] - 2026-06-26
### Fixed
- `.eext` packaging so the extension installs reliably. Bundled a JPEG logo.

## [0.3.0] - 2026-06-26
### Fixed
- Netflag / netport **orientation**: corrected for EasyEDA's y-up coordinate
  system and fixed rotation handling in `connect_pin` (reverted a wrong rotation
  negation).

## [0.2.0] - 2026-06-25
### Added
- Initial connector: a WebSocket bridge (port-scans 49620–49629) to the
  easyeda-agent Go daemon, dispatching typed schematic actions to the official
  `eda.*` API, with auto-reconnect and a heartbeat.
- `connect_pin` composite action and the netflag/netport orientation convention.
- Header menu: **Reconnect**, **Stop**, **Toggle Auto-Connect**, **About**.
