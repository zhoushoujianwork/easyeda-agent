# 功能状态与路线图

本文件记录当前可用能力；typed action 的权威来源是 `make actions`，实现映射见 `internal/protocol/actions.go` 与 `extension/src/actions.ts`。战略优先级见 [`ROADMAP.md`](ROADMAP.md)，生态调研见 [`ecosystem-survey.md`](ecosystem-survey.md)。

## 当前基线

- 原理图：以 Connectivity IR（器件、引脚、网络、pin-to-net）为电气事实，布局与 Lib 模块复用不得改变连接核心。
- PCB：`layout-lint` 负责硬门，`layout-score` 负责质量维度；`pcb check` 与 DRC 负责制造和电气约束。
- 94 个 typed actions 的精确清单始终以 `make actions` 为准，本文不重复维护易过时的数量和版本历史。
- 真实回归输入、验收门和运行步骤见 [`e2e-automation-acceptance.md`](e2e-automation-acceptance.md)。

## Completed

### Absorbed from the official extension ecosystem (A1/A2/A3/A5)

Shipped from the [`ecosystem-survey.md`](ecosystem-survey.md) absorb-list — features
mined from open-source `eext-*` extensions' real `eda.*` usage:

| Action | CLI | What | absorb # |
|---|---|---|---|
| `schematic.library.get_by_lcsc` | `lib by-lcsc --lcsc C…` | Deterministically resolve LCSC C-numbers → `{libraryUuid, uuid}` (no free-text rank); `notFound` for misses. Companion script `scripts/parts-add.py` writes results back into `standard-parts.json`. | A1 |
| `pcb.line.create` | `pcb track` | Create a copper track (导线) on a layer between two points (mil, y-up). **Mutates.** | A2 |
| `pcb.via.create` | `pcb via` | Place a via (过孔) with hole + outer diameter. **Mutates.** | A2 |
| `pcb.report` | `pcb report` | Read-only design report: per-net length, net-class totals, differential-pair skew, equal-length spread. | A3 |
| `pcb.drc.rules` | `pcb drc-rules` | Read the DRC rule configuration without running a check. | A5 |
| `pcb.save` | `pcb save` | Save the active PCB to disk; also the action the daemon's debounced autosave now fires for PCB windows. | gap fix |

All five absorb-items are **live-verified on a real board (PCB1, connector 0.5.15):**
A1 resolved C6186→AMS1117-3.3 identity, A5 returned the full rule config, A3 reported
4 nets with length/net-class/diff/equal-length, A2 created a GND track (net length read
back 0→500 — bound to the right net), and `pcb drc` + save passed. The live run surfaced
a gap — **no `pcb.save` + PCB not covered by autosave** — now fixed (`pcb.save` action +
`saveActionForDocType` maps `pcb`→`pcb.save`, so PCB edits autosave like schematic edits).
No one-call PCB autorouter exists on this build (A4 blocked — see survey §6).

### Read context (7 actions)

| Action | What |
|---|---|
| `system.health` | Daemon + connector availability, connected/active windows. Daemon-answered. |
| `project.current` | Current project uuid / name / team context. |
| `document.current` | Active editor document + schematic page context. |
| `schematic.pages.list` | Schematic documents and pages in the project. |
| `schematic.page.open` | Open/activate a page by uuid. |
| `schematic.components.list` | Components on the active page (optional `allPages`, `includePins`) with designator, name, coords, and `getState_*` fields. Each carries a structured `device:{libraryUuid,uuid,name}` — the device-library identity of the placed part (from `getState_Component()`, the same identity rebind resolves) — distinct from the placed-INSTANCE `component/symbol/footprint/uniqueId` ids. Use `device.uuid` to lock onto a golden design's exact symbol variant instead of re-searching by LCSC C-number; imported devices may report an empty `device.libraryUuid` (resolve via `lib search`/`lib by-lcsc` before `sch place`). |
| `schematic.text.list` | Read-only list of ALL text primitives on the ACTIVE page (`primitiveId/content/x/y/rotation/fontSize/color/…`) — pairs with `schematic.primitives.delete` to clean orphaned zone-draw labels without `debug.exec_js` (#156). Page-lazy-load law: active page only; sweep pages via `--page`/`doc switch`. CLI `sch text-list`. |
| `sch note` (CLI-internal exec_js, zone-draw precedent; no new action) | Place a circuit-description text note (电路说明) on the sheet — the third piece of the schematic-organization default (paging + zone frames + per-module notes; user feedback: agent schematics shipped without partitions or descriptions). `--text` (`\n` = line break), `--x/--y` (y-UP), `--font-size` (default 10, subordinate to zone labels), `--color` (default gray). Id read-back verified + explicit save; enumerate `sch text-list`, remove `sch prim-delete`. Mutates. |
| `pcb.component.attrs_backfill` | Backfill PCB components' EMPTY otherProperty values from their DEVICE-LIBRARY records (resolved per-part by LCSC C-number via `getByLcscIds`). Repairs the platform's sch→PCB import (creates attribute KEYS with empty VALUES — blanking the 器件标准化 panel's PCB columns); the schematic instance is NOT a usable source (its values are empty after save/reload too). Empty-only merge by default (`--overwrite` forces); parts without a C-number skipped + reported. PROJECTED-STATE keys (`Designator`/`Name`/`Manufacturer*`/`Supplier*`/`Add into BOM`/`Unique ID`) are excluded from the merge — the library's own `Designator:"C?"` placeholder used to get merged in and the platform synced it into the primitive designator, wiping 166/166 on a real board (root-caused + fixed 2026-08-09). CLI `pcb sync-attrs`; auto-runs after `pcb import-changes` (`--no-sync-attrs` opts out). Mutates. |
| `pcb sync-designators` (CLI orchestration; no new action) | Repair placeholder designators (`U?`/`C?`) from the schematic, matched by `uniqueId` (minted by the platform at first sch→PCB import; ONE namespace across both documents — primitiveId is per-document). Placeholder-only (hand-set designators never overwritten); every write read-back-verified; `pcb.save` checkpoint after repair; schematic-side placeholders classified separately ("annotate the schematic first"). `--dry-run`/`--json`, non-zero exit on any failed write. Auto-runs rear-guard after `pcb import-changes` (after attrs, `--no-sync-designators` opts out). Mutates. |
| `schematic.select` | Select primitives by id, return the active selection. |

**Discover + switch/open loop (CLI, no new actions):** `easyeda doc ls [--project X]`
aggregates `schematic.pages.list` + `pcb.documents.list` + `document.current`
into one ★-active document list; `easyeda doc switch <name|uuid> [--project X]`
(or `easyeda doc open <name|uuid>` for more intuitive naming) resolves a page/PCB name
→ `document.open` → readback (cross-type PCB↔schematic). With 2+ windows connected,
`--project`/`--window` is required.

**Live window context:** each window's context in `system.health` stays fresh two
ways — the daemon refreshes it from every action response, and the connector
(≥ v0.5.7) pushes it on each heartbeat (~3s) when the active document changed, so
health tracks a UI tab-switch with no command run. `health` also reports
`connectorVersionOk` to flag a stale connector left in an open window.

### View / navigation (4 actions, `document` domain — schematic + PCB)

Editor canvas view shortcuts via `eda.dmt_EditorControl.*`; act on the focused
canvas, so they apply to whichever document (schematic or PCB) is active. CLI: `easyeda view …`.

| Action | What |
|---|---|
| `view.fit` | Zoom to fit all primitives — 适应全部, the `K` shortcut (`zoomToAllPrimitives`). |
| `view.fit_selection` | Zoom to fit the current selection — 适应选中 (`zoomToSelectedPrimitives`). |
| `view.zoom` | Pan/zoom to a center `x/y` and/or `scale` percent (`zoomTo`); omitted fields keep current. |
| `view.region` | Zoom to a rectangular region `left/right/top/bottom` (`zoomToRegion`). |

### Sheet / page management + 明细表 (6 actions, `schematic` domain)

Map to `eda.dmt_Schematic.*`. **No set-paper-size (A4/A3) API exists** in EasyEDA
Pro; the title block (明细表) is the editable "图纸" surface. CLI: `easyeda sch …`.

| Action | What |
|---|---|
| `schematic.titleblock.get` | Read a page's 明细表 — `showTitleBlock` + per-field `titleBlockData` (read first to learn the field keys). |
| `schematic.titleblock.modify` | Toggle title-block visibility and/or patch fields; only the passed items change, unknown keys ignored. Mutates. |
| `schematic.page.create` | Create a new page under a schematic document. Mutates. |
| `schematic.page.rename` | Rename a page. Mutates. |
| `schematic.page.delete` | Delete a page (confirmation-gated, no undo). Mutates. |
| `schematic.rename` | Rename a schematic document (whole sheet; may also rename a linked reuse-module symbol + PCB). Mutates. |

### Board / 组合 — schematic↔PCB binding (7 actions, `board` domain)

A **Board groups one schematic + one PCB** (识别符是 name, not uuid) — the structural
unit that keeps the two together and that `import_changes` follows. Project tree:
Workspace → Project → **Board** → schematic + PCB. Map to `eda.dmt_Board.*`. CLI: `easyeda board …`.

| Action | What |
|---|---|
| `board.list` | All boards in the project — name + bound schematic + pcb. |
| `board.current` | The current board (its bound schematic + PCB). |
| `board.create` | Bind a schematic and/or PCB into a new board. Fixes a floating PCB before `import_changes`. Mutates. |
| `board.rename` | Rename a board by its current name. Mutates. |
| `board.copy` | Duplicate a board (schematic + PCB). Mutates. |
| `board.delete` | Delete a board by name (confirmation-gated, no undo). Mutates. |
| `board.rebind` | Repair a stale/orphaned board binding after a PCB rebuild: delete the board (by `--name`, else current) and re-create it bound to `--schematic` (+ `--pcb`), rolling back on failure. Clears the false DRC Netlist Error left when the binding points at a deleted schematic UUID. `--force` moves a schematic already bound elsewhere. Mutates. |

### Draw / edit (11 actions, all mutate)

| Action | What |
|---|---|
| `schematic.component.place` | Place a device by library identity (`libraryUuid` + `uuid`) at `x,y` with optional rotation/mirror/BOM flags. |
| `schematic.rebind.footprint` | Swap a placed component's footprint via the **five-step binding** (`lib_Device.modify → delete → create → restore`) — `modify` alone cannot change a placed instance's footprint reference. Resolves the placed part's REAL 32-char device uuid first (LCSC→MPN→project-name; `getState_Component().uuid` is a 16-char symbol id the library APIs reject). **System-library device records are read-only** → automatic personal-library **clone fallback** (copy/reuse → bind new footprint → re-place; `mode='cloned-to-personal-library'` + `clonedDevice`), with the 符号/封装另存为 conflict dialogs auto-confirmed via MutationObserver (timer polling is throttled in background tabs). Matches by footprint name (exact; pass `--footprint-uuid` to bind directly). Captures & restores designator/position/rotation/mirror/BOM flags/manufacturer/supplier/otherProperty; rolls back on any failure. **Re-placing mints a NEW primitiveId — wires may need re-drawing; run `sch drc`/`sch check` after.** Mutates. |
| `schematic.rebind.symbol` | Swap a placed component's symbol via the same five-step binding, incl. the clone fallback + dialog auto-confirm. Same matching/rollback/caveats as `rebind.footprint`. Mutates. |
| `schematic.component.replace` | Replace a placed component with a **different** device (换型号 — the API equivalent of the 器件标准化 panel's 使用推荐器件, which itself has no extension API). No rebind-device primitive exists, so: capture state + pin table → delete → create the new device at the same pose → restore designator + uniqueId (kept so sch→PCB `import-changes` UPDATEs instead of delete+add). Part-identity fields (name/manufacturer/supplier/LCSC) deliberately follow the NEW device; `--keep-properties` also carries old custom attrs. Target: `--lcsc` (unique) / `--device-uuid`+`--device-lib` / `--query` (unique). Rolls back to the original device (full identity) on failure after delete. Returns a `pinDiff` (removed/added/moved by pinNumber at identical pose) — non-empty ⇒ re-wire, then `sch drc`/`sch check`. Mutates. |
| `schematic.component.modify` | Patch position, designator, name, BOM flags, or custom properties (components only — not flags). |
| `schematic.component.delete` | Delete component primitives (confirmation-gated). **Only removes components** — wires/buses/graphics survive; use `schematic.page.clear` for a full page reset. |
| `schematic.primitives.delete` | Delete primitives of **any** type by id (components, flags, wires, buses, graphics) — routes each id to its owning class. Omit ids to delete the current selection (select-all → delete). Confirmation-gated, no undo. |
| `schematic.page.clear` | Clear the **active page**: delete every page-level primitive (components, net flags/ports/labels, wires, buses, graphics), optionally keeping the sheet/title block (`preserveSheet`, default true). `dryRun` reports per-type counts without deleting. Returns `{deleted:{...}, total, deletedIds}`. Confirmation-gated, no undo. |
| `schematic.wire.create` | Create a wire polyline (optional net/color/width/lineType). |
| `schematic.netflag.create` | Power / ground / analog-ground / protective-ground / net-port (IN/OUT/BI) / short-circuit flag. |
| `schematic.power.connect_pin` | Composite: draw a stub wire out of a pin **and** place a netflag/netport at its far end in one call. Structurally prevents the "netflag overlaps pin" DRC fatal and orients the flag body outward along the stub (顺着导线方向). Default direction inferred from kind, default offset 30u. |
| `schematic.pin.set_no_connect` | Mark (or clear) a pin's no-connect flag (非连接标识, the X marker) so DRC stops reporting intentionally-floating pins as "un-connected pin". Targets pins by designator + pin number(s); `noConnected=false` clears. A pin state, not a standalone primitive: the connector resolves the live component, uses `component.getAllPins()`, commits each `pin.setState_NoConnected(...)` with `pin.done()`, then verifies by fresh readback. |

### Library search (1 action)

| Action | What |
|---|---|
| `schematic.library.search` | Free-text search of the EasyEDA device library (`eda.lib_Device.search`); returns `libraryUuid` + `uuid` ready for `schematic.component.place`, plus name/value/footprint/lcsc/description. Replaces ad-hoc `debug.exec_js` lookups. **See the search caveat under Roadmap.** |

### Verify (3 actions)

| Action | What |
|---|---|
| `schematic.drc.check` | Run the official schematic DRC SDK gate; current EasyEDA builds may return only boolean/aggregate detail. Use `schematic.check` for reconstructed per-item warnings. |
| `schematic.check` | Reconstructed schematic design check from primitives + official netlist JSON: net-marker mismatch, multi-net wire, floating pins, wire crossings, and wire-over-pin hazards. |
| `sch destagger` (CLI, Go-side planner) | **Fix side of `marker-overlap`** (issue #171; detection landed in #148). Plans a safe batch de-stagger: for every marker caught in a visual overlap, pick a new stub direction + length and **move the stub wire with it** (`disconnect` → `connect_pin`), leaving the host (pin-side) endpoint untouched so the electrical topology cannot change. Only markers sitting on a **two-point straight short stub** are moved — polyline/trunk/diagonal carriers are skipped with a reason (`not-a-stub`/`stub-too-long`/`diagonal-stub`), and a boxed-in marker is left alone (`no-free-slot`) rather than forced into another collision. Stub-length candidates are **measured** (they step by the flag's `flagTextBand` size) and snapped to the connector's 5-unit `SCH_GRID`; direction preference follows the 电上地下 convention and rotation comes from the same `flagBodyRotation` truth table the `reversed-net-flag` rule checks against. `--apply` re-runs the real `sch check` after each round and **rolls the whole batch back** if any electrical counter (floating-pin / dangling-wire / net-marker-mismatch / multi-net-wire / …) got worse. Default dry-run; `--json`; `--max-rounds`. Single page (stub geometry is active-page only). |
| `schematic.bridgeCheck` | **Tree-granularity** net-vs-copper consistency check (`sch bridge-check`). Groups every page wire into trees by shared vertices (union-find), then aggregates the netflag/netport net names anchored on each tree: `len(set(nets)) > 1` → **BRIDGE** (共线合并短路, real short, ERROR/gate); empty nets + touches a pin → **ORPHAN** (孤儿桩, WARN). Catches the盲区 `schematic.check`'s per-single-wire `multi-net-wire` rule under-reports when one merge spans several wires. Reports wire ids / flag ids / touched `designator:pin` per problem tree. Read-only. |
| `schematic.snapshot` | Capture the current rendered area as a PNG artifact. |

### Export (2 actions)

| Action | What |
|---|---|
| `schematic.export.netlist` | Export the netlist as an artifact. |
| `schematic.export.bom` | Export BOM as csv or xlsx artifact. |

### Save (1 action)

| Action | What |
|---|---|
| `schematic.save` | Save the active schematic document. |

### Escape hatch (1 action)

| Action | What |
|---|---|
| `debug.exec_js` | Run raw `eda.*` JavaScript in the connector. Confirmation-gated; for operations without a typed action yet. Repeated snippets should graduate to typed actions. |

### Tooling layer

- **Go-side CLI planners (pure geometry over real bboxes)** — deterministic,
  unit-testable analysis/placement that runs in the daemon's Go process on a
  single `schematic.components.list` pull, no per-step screenshots:
  - **`easyeda sch gate`** — **the S5 verification gate, one command**: runs
    `layout-lint → check → bridge-check → drc` in a fixed order and returns one
    report. Motivated by the surface-convergence audit
    ([`design-sch-surface-convergence.md`](./design-sch-surface-convergence.md)):
    with four separate checkers, *which ones, in what order, whose exit code
    counts* was re-decided every run with no data to decide it on — the audit log
    shows agents answering it four different ways for the same failure. Order,
    blocking rules and exit code now live in code. Blocking: layout-lint
    overlap/pin-coincidence · check fatal+error · bridge-check `wire-bridge` ·
    drc fatal (tight spacing, orphan stubs and non-fatal DRC are advisory;
    `--strict` promotes them). **Three-state verdict** — `pass` / `fail` (the
    board has blocking problems) / **`blocked`** (a checker could not RUN, so the
    schematic was never judged; remaining stages are skipped instead of running
    into the same wall, and the report points at `health`/`doc switch` rather
    than at the circuit). Each failing stage carries its prescribed next step.
    `--json` nests every stage's full native report under `stages[].detail` (a
    superset of the four single commands' JSON); `--only`/`--skip` select a
    subset and reject misspelled stage names rather than silently gating on
    fewer checks; `--fail-fast`. The four single commands stay for spot checks.
  - **`easyeda sch layout-lint`** — pairwise bbox overlap/pin coincidence
    (ERROR), tight spacing/off-grid/zone violation/**out-of-sheet** (WARN), with
    corrected mm↔0.01-inch conversion and schema-v2 unit metadata. `out-of-sheet`
    (issue #180) catches parts whose **bbox** (not anchor — a body can stick out
    while the anchor sits inside) leaves the sheet frame inset by 12 units:
    nothing caught this before, because an off-page part still wires up and still
    reconciles against the netlist — it simply does not print. `sheetCheckStatus`
    mirrors the zone check's honest disclosure (`unavailable` + reason when the
    sheet bbox is unreadable or under `--all-pages`). `--strict` also fails
    warnings, missing/malformed/unproven anchor/bbox/pin geometry, and an
    unavailable configured zone **or sheet** check, so `0 overlap` can no longer
    stand in for a proven layout. Strict proof is active-page/real-part only and rejects
    `--all-pages` or `--include-non-parts`.
  - **`easyeda sch autoconnect`** — pin-aware connect planner: score every
    (direction × offset) candidate against real geometry, pick the lowest cost,
    delegate the mutation to `connect_pin` (issue #24).
  - **`easyeda sch autolayout`** — module-aware **placement** planner (issue #25):
    reads a `--spec` (page, sheet, modules with zone/core/parts, rules),
    partitions the canvas into named zones (`left-top`/`center`/`right`/…), places
    each module's core IC near its zone center, fans peripherals around it with
    collision retry, and preserves each core pin's fanout channel + the A4
    title-block keep-out. Same pure-scorer style as autoconnect: identical spec +
    input → identical coordinates that pass `layout-lint`. `--dry-run` plans
    without mutating; template `--apply` pins `--doc`/`spec.page`, refuses any
    existing wire/bus/net marker（含 `short_symbol`）both before planning and immediately before
    mutation, rejects `--all-pages`, and requires proven bbox/pin geometry. It
    validates every moved anchor, grid/spacing/overlap/pin/title-block rule by
    readback and proves `saved:true`. Any failure triggers reverse-order restoration, verified
    by another anchor readback, then saves the rollback.
    There is no template force/rewire override because v1 only **moves
    already-placed parts** (it neither carries wires nor creates missing parts).
- **`skills/easyeda-agent/scripts`** — a data-only schematic checker (no screenshots): one
  `getAll` + `wire.getAll` pull returns the full layout, then a geometry/union-find
  pass finds connectivity and orientation problems with exact coordinates (13
  checks: `flag_on_pin`, `dangling_wire`, `floating_pin`, `orientation`,
  `bbox_overlap`, `dup_designator`, … ). Ships with:
  - a **rule-trust harness** (`make lint-test`) — orientation-consistency guard
    (`orientation.json` is the single source of truth for the body-rotation table,
    derived identically by the linter's `orient.py` and the connector's
    `connect_pin`, so they can't drift) + fixture goldens;
  - a **diff baseline** — `lint.sh <project> --save` records a snapshot, later runs
    show only NEW / FIXED / PRE-EXISTING findings plus the changed primitives.
- **🧩 Standard circuit-block library (电路块库) — flagship capability.** A
  community-built, credited library of KNOWN-GOOD peripheral subcircuits
  (`skills/easyeda-agent/references/blocks/*.json`, one block per file): CH340 USB-serial, ESP32
  auto-download, button de-bounce, USB-hub, buck… Their internal topology is fixed
  and copy-verbatim; reuse only rebinds the boundary nets (`ports`) and reallocates
  RefDes. It is the **topology tier** above `standard-parts.json` (part tier) and
  below `design-flow.md` (flow tier). Design invariants:
  - **Pins referenced by FUNCTIONAL NAME** (`CH340.TXD`), never pin numbers → reuse
    needs zero pin-renumbering.
  - **`parts` point back into `standard-parts.json`** by role key → BOM/LCSC stays
    single-sourced; `alt[]` gives interchangeable substitutes.
  - **Three knowledge dimensions per block**: parts (with alternatives) +
    `schematic_notes` (wiring gotchas) + `pcb_layout` (structured electrical
    constraints with `severity`, future-feedable to `pcb check`).
  - **Validation gate**: a block only enters the library after one full-flow proof
    (`place → wire → sch check → DRC=0`); until then `validated:null` +
    `internal_nets:"pending"`. Topologies are harvested from validated oshwhub
    boards / official reference designs, never hand-written from memory.
  - **Attribution**: `author`/`contributors` (GitHub @handles, never removed) +
    `added`/`updated` versions — *contribute once, benefit forever*. Contribution
    standard + PR gate: `references/standard-blocks-contributing.md`.
  - **Tooling**: `scripts/blocks.py ls | show <id> | validate [--strict]` — browse
    blocks and lint the JSON against the schema + contribution rules (the PR gate;
    cross-checks every `parts` key against `standard-parts.json`). Network-free,
    daemon-free — the local companion to the JSON, like `parts-select.py` is for
    parts. Schematic instantiation is the phase-2 write path (see Roadmap →
    `sch block apply`).
- **Connector self-healing reconnect** — the connector port-scans 60832-60841,
  validates a handshake, and reconnects on liveness loss. It **never permanently
  gives up**: after 5 fast retries it drops to a quiet 10s background poll, so a
  daemon started/restarted later auto-reconnects with no manual action. A
  low-volume `log` frame surfaces connection-lifecycle diagnostics in the daemon
  log (`connector LOG: …`).
- **`make eext` release flow** — bumps the PATCH version and builds an importable
  `.eext`. `make eext` keeps the uuid **stable** (update-in-place: uninstall old →
  import); `make eext-fresh` mints a **fresh uuid** (imports as a separate entry,
  no uninstall needed) as the fallback when the installed one won't uninstall.
- **`easyeda update` (alias `upgrade`) — in-place self-update** for the two pieces
  that *can* be updated programmatically: the **CLI binary** (downloads this
  platform's release asset, verifies sha256 against the release `checksums.txt`
  when present, runs the download once to confirm it reports the expected
  version, then swaps it in with a same-dir rename) and the **skill dirs** (same
  machinery as `easyeda skill sync`). The **connector `.eext` is reported, never
  touched** — sideloads have no in-place update, so `update` prints the version
  it found in each open window plus the re-import URL. `--check` is read-only and
  `--check --exit-code` exits **10** when anything is behind, so agents/CI can
  gate on version drift. A **dev build is never overwritten** without `--force`
  (air rebuilds it anyway; silently replacing it would make the dev loop lie).

---

## Verified end-to-end (this session)

The board was drawn **entirely from real LCSC / 立创 library parts** (search →
place by uuid → wire → flag), and lint-clean:

- a minimal **ESP32-S3-WROOM-1** system board.

This proves the library-first workflow (place real parts, then wire) end to end,
not just hand-drawn custom symbols.

---

## Roadmap (NOT yet built)

These are planned and **not implemented** today.

- **🧩 `easyeda sch block apply` — one-shot circuit-block instantiation (phase-2 write path).**
  The block library's read/browse layer ships today (`references/blocks/*.json` +
  `scripts/blocks.py`); the **write path** — materializing a block into the live
  schematic — is the next milestone. Interface designed first (per the CLI-design
  首要准则), implementation to follow:

  ```
  easyeda sch block apply --id block.ch340c_usb_serial \
      --bind TXD=MCU_RX,RXD=MCU_TX,VBUS_5V=5V,GND=GND \
      [--prefix U2,R7,...] [--at X,Y] [--page <uuid>] [--dry-run]
  ```

  Semantics: (1) resolve the block's `parts` → place each role from
  `standard-parts.json` (`schematic.component.place`), allocating fresh RefDes
  (respecting `--prefix`/next-free); (2) wire every `internal_nets` entry with
  real wires (`connect_pin` — honoring the netflag-needs-real-wire rule); (3) for
  each `ports` entry, either bind to the `--bind`-supplied host net or emit the
  `default_net`; (4) refuse to apply a **draft** block (`validated:null` /
  `internal_nets:"pending"`) unless `--force`; (5) `--dry-run` prints the place +
  wire plan (like `sch autolayout --dry-run`) without mutating. This is a typed
  action (mutation) → a Cobra subcommand, not a script. It reuses the existing
  `place` + `connect_pin` engines, so the new logic is just topology expansion +
  RefDes/port binding. Ships the block library from "agent reads & hand-copies" to
  "agent instantiates in one call".
- **器件标准化 / standard parts library** — a curated `skills/easyeda-agent/references/standard-parts.json`
  mapping category → `{MPN, LCSC C-number, libraryUuid, deviceUuid}` that the
  agent places from **first**, with `schematic.library.search` as the fallback. The
  goal is deterministic, repeatable part choices instead of re-searching every time.
- **优化搜索 / optimized search** — `schematic.library.search` today simply slices
  the **first N** of EasyEDA's raw `lib_Device.search` results. Its action
  description claims a "ranked list", but the implementation does **not** rerank —
  it preserves EasyEDA's native order and truncates. Planned: rerank/filter by
  query relevance, package, JLC-basic-part status, and stock.
- **立创商城比对选型 / LCSC mall comparison selection** — compare candidate parts by
  price / stock / specs to pick the optimal one. Not built.
- **🧲 组内布局计算 / `sch group tidy`(未建,判据已实战校准 2026-08-12).**
  基于持久化编组做**组内自动整理**:`--pattern power-updown` 把组内双电源旗电容
  竖放成"上电源/下地"行业画法(ceshi POWER/MCU 组手工验证,96.0 excellent)。
  实战趟出的判据(实现即规则):① pin 半距按**实测**不按假设(0402/0805 符号
  ±20,且同规格不同库件存在镜像——C1/C6 需 rot90 而 C2/C3/C4 需 rot270,必须
  rot 后重读 pin 实位);② **带信号 netport 的件保持横放**(长条标竖放即折叠,
  按旗类型分流);③ **标签文字朝外**(电源旗文字在符号上方、GND 在下方——当前
  connect 产出的文字"内折"在旗与器件之间,需按 orientation 真值表甩向外侧,用户
  点名);④ mutation 后必须 fresh 读再连(rot 后立即 connect 吃 stale pin 位曾致
  两根 stub 同点起步被平台共线合并成贯穿桥=真短路,gate 兜住;显式坐标绕开)——
  这也指向 **schematic 侧 mutation-后-stale 的统一修复**(zone-draw / group-move
  / connect 三处已实证,应做 settle/double-read 通用防线)。组内 layout-lint 兜底
  不重叠;组间由分区框隔离 ⇒ 全局无重叠。
- **🔖 接插件逐脚丝印 / connector per-pin silk — `easyeda pcb silk-pins`(P9,未建).**
  端子 / 排针 / 接插件应**逐脚自动标注**电气特性,让用户拿到板一眼知每脚是什么(电源/地/TX/RX…)
  以辅助接线。今天**没有 CLI 自动做**——手工丝印踩过坑:把多脚写成一整行长句
  (`LCD 1:GND 2:3V3…`)、不与焊盘逐一对齐、长句远离焊盘只能读不能辅助接线、字号密度挤器件。
  设计标准(实现即校验门):① 接口名只留短标题(`LCD`/`PROG`/`MIC`…);② **每脚单独短标签、严格按焊盘物理顺序**;
  ③ 优先用**网络简称**(`G`/`3V3`/`5V`/`TX`/`RX`/`SCL`/`SDA`/`RST`/`BL`,从连到该脚的网名取);
  ④ 不写脚号冒号(除非编号有装配意义);⑤ 横向排针→横向逐脚、纵向→纵向,顺序与实物观察方向一致;
  ⑥ 标签对准各自焊盘、在器件外壳遮挡区之外;⑦ 普通器件只留位号;⑧ 不压焊盘/器件/出框、装配后可见(铁律 11)。
  **实现落点**:新子命令 `easyeda pcb silk-pins`(或扩展 `pcb silk`)从 netlist 取每脚网名简称 + 焊盘坐标/排布方向逐脚落字,
  复用块的 `silk` map(块数据已有逐脚标注,如 LED 阴极 K)。**根本教训**:校验门要**同时查几何(没压焊盘)+ 语义
  (标签数=引脚数、逐脚对齐、无长句)**——上一版只验了几何、漏了语义可用性。归属 design-flow **P9**。
- **✅ PCB 布局智能补完 — `place-constrained` 4 真缺陷全部 DONE(2026-07-11).** 复评官方
  「PCB自动化工具」v2.5.1 确认其「模块化布局」是 netlist 连通性聚类、解决不了角色感知 floorplan
  的 5 条痛点(板框/类型优先级/朝向/板边距/天线)——都是我们自己代码补的。ceshi 真机逐条验证:
  1. ✅ **`classifyCP` CONSUME 块数据(方案 A)** —— 位号前缀查块 `placement`,regex 降级 fallback;
     显式 `anchor` 字段治过度锚定;死的 role-id `ByDevice` 移除(`697efc2`,issue #95)。**附带**:
     分类改用 `manufacturerId` 而非 `"={Manufacturer Part}"` 模板 → U1 WROOM `main`→`edge`(`81576fb`)。
  2. ✅ **planner 读真板框** —— `outline-fit` 后 `pcb.outline.get` 接进 planner;`boardEdges` 上报
     board-outline vs part-cloud;ceshi J1 吸到真左边 -925(`0d8859e`)。
  3. ✅ **Tier-4 net 聚类** —— 须移位的卫星按共网最近固定脚做种子聚到芯片;良placed 件不动;user-facing
     不被拽走(`a4e9a2d`)。
  4. ✅ **天线 keepout 自动生成** —— 新 `pcb antenna-keepout`(方案 A,块声明 `keepout.end_frac`);
     只盖无焊盘天线端不孤立地脚;MULTI 层全铜层;幂等;`pcb check` 天线检测同步用 manufacturerId。
     ceshi loop 验证 present→0/deleted→1/regen→0(`ce04deb`)。

  仍可从官方插件吸收(未做):**器件布局导出/导入(布局复用)**(块库 PCB 侧对应物)、模块级
  fanout-with-vias、几条 DFM 检查(REF方向/两脚线宽/时钟3W/冗余过孔·线段)。详见 memory
  `pcb-automatic-tool-v251-reeval-and-layout-defects`。

### 验收用例 roadmap (acceptance regressions, NOT yet run end-to-end)

两块 ESP32 最小系统板作为**端到端检查验收基准**——跑通即证明放置→布线→`pcb check`
(含新的丝印正反 / 走线压焊盘 / 非正交走线规则)→DRC 全流程闭环。

- **task #34 — ESP32 **模组**开发板 (module dev board).** 拿原始需求
  [`esp32MiniRequire.md`](../esp32MiniRequire.md)(4 层板 + 点灯 + 5V 供电端子 + 降压 3V3 +
  CH340 USB 烧录 + BOOT/RESET 按键 + 四角 M3 固定,**不含 BOM/网表**)从零跑:agent 自己选型 →
  放置 → 编组 → 布线 → 转 PCB,照 `skills/easyeda-agent/references/design-flow.md` 的 S0–S6 + P0–P10
  脊柱,**收尾必须 `pcb check` 0 ERROR**(含丝印正反、走线压焊盘)。WROOM-1 模组自带天线/晶振/flash,
  keep-out 只需盖模组天线区。
- **task #35 — ESP32 **芯片级** N8R8 最小系统板 (bare-chip minimal system, no module
  template).** 用裸 **ESP32-S3** 芯片(不是 WROOM 模组),自己搭最小系统:**PCB 板载
  天线 + π 型匹配网络**、**N8R8 = 8MB flash + 8MB PSRAM**、40MHz 晶振、EN/boot straps、
  多路去耦。规格见 [`docs/test-case-esp32-chip-n8r8.md`](test-case-esp32-chip-n8r8.md)。
  这是比模组板更硬的验收:天线 keep-out + 阻抗、晶振布局、flash/PSRAM 高速走线,压满
  `pcb check` 的走线/丝印规则。**先补 `standard-parts.json` 芯片级选型**(ESP32-S3 裸片 /
  flash / PSRAM / 天线器件)再跑。

### LCSC C-number lost on placed parts → fixed by BOM enrichment

A placed component's `getState_SupplierId()` returns `MPN.1` (e.g.
`GRM21BR61H106KE43L.1`), not the LCSC C-number (`C440198`) — confirmed by reading
the exported BOM, whose "Supplier Part" column is the MPN.1. The component can't be
fixed at the source: `setState_SupplierId('C440198')` does **not** persist (the
field is device-bound and reverts on re-pull). So the fix is post-export:
**`skills/easyeda-agent/scripts/bom-enrich.py`** joins the C-number in by matching each row's Manufacturer
Part against `standard-parts.json` (MPN → LCSC) and rewriting "Supplier Part" to the
real C-number (and filling an empty Value). Verified: 5/5 rows of the ESP32-S3 BOM
enriched to orderable C-numbers; unmatched MPNs are reported as candidates to add to
`standard-parts.json`. Follow-ups: (1) wire the enrichment into the daemon's
`schematic.export.bom` so exports are orderable by default; (2) for non-standard
parts, resolve MPN → C-number via `lib_Device.search` instead of only the curated
list.

---

## Connector quirks (load-bearing)

- **`createNetFlag` / `createNetPort` STORE rotation negated on the 2026-06 build.**
  Despite the earlier "identity" assumption (commit `8aace7e` reverted a negation as
  a misdiagnosis), a live test settled it: `connect_pin(direction=left)` passed `90`,
  the flag stored `270` and rendered pointing **right**. (0/180 up/down are symmetric,
  so only horizontal flags exposed it.) `connect_pin` now **auto-detects** the
  behavior at runtime (`detectRotationNegation` — a one-shot probe flag, re-pulled)
  and compensates, so its output is correct whether the build negates or not. The
  orientation table (`orientation.json`, the **stored-rotation** truth) is still the
  single source, derived in one place and asserted equal between linter and connector
  by `make lint-test`; `calibrate.js` validates it read-only against real flags.
- **Coordinates are y-UP** — `+y` renders **upward**. `connect_pin` honors this:
  `direction: up` increases `y`, `down` decreases it.
- **No programmatic undo** in `eda.*`. `modify` only works on components, not
  flags — to change a flag you delete and recreate it. Pull fresh primitive ids
  right before mutating.
- **Re-importing the `.eext` does NOT reload already-open EasyEDA windows.** An
  open window keeps running the **old** connector code; the stale window then
  fights the freshly-imported one over the daemon socket → instability. **Fully
  quit and relaunch EasyEDA** to load new connector code.
- **`getCurrentRenderedAreaImage` could return a stale cached frame** (it didn't
  follow zoom or reflect just-made edits) — historically a trap for "confirm with
  a screenshot" workflows. Fixed in recent connector versions; still prefer
  data-driven verification (`schematic-lint`, `drc.check`) over screenshots.
</content>
</invoke>
