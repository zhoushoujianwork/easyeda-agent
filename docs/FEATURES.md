# Feature status & roadmap

What `easyeda-agent` can do today, what's been driven end-to-end, and what's
planned. Ground truth for the action catalog is `make actions`
(`internal/protocol/actions.go`); the connector's handler map is
`extension/src/actions.ts`.

**20 typed actions** total — 14 in the `schematic` domain, 2 in `artifact`
(netlist/BOM export), and one each in `system`, `project`, `document`, `debug`.
19 are dispatched to the connector; `system.health` is answered by the daemon
itself (daemon/connector liveness, no window required).

---

## Completed

### Read context (7 actions)

| Action | What |
|---|---|
| `system.health` | Daemon + connector availability, connected/active windows. Daemon-answered. |
| `project.current` | Current project uuid / name / team context. |
| `document.current` | Active editor document + schematic page context. |
| `schematic.pages.list` | Schematic documents and pages in the project. |
| `schematic.page.open` | Open/activate a page by uuid. |
| `schematic.components.list` | Components on the active page (optional `allPages`, `includePins`) with designator, name, coords, and `getState_*` fields. |
| `schematic.select` | Select primitives by id, return the active selection. |

**Discover + switch loop (CLI, no new actions):** `easyeda doc ls [--project X]`
aggregates `schematic.pages.list` + `pcb.documents.list` + `document.current`
into one ★-active document list; `easyeda doc switch <name|uuid> [--project X]`
resolves a page/PCB name → `document.open` → readback (cross-type PCB↔schematic).
With 2+ windows connected, `--project`/`--window` is required.

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

### Board / 组合 — schematic↔PCB binding (6 actions, `board` domain)

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

### Draw / edit (6 actions, all mutate)

| Action | What |
|---|---|
| `schematic.component.place` | Place a device by library identity (`libraryUuid` + `uuid`) at `x,y` with optional rotation/mirror/BOM flags. |
| `schematic.component.modify` | Patch position, designator, name, BOM flags, or custom properties (components only — not flags). |
| `schematic.component.delete` | Delete component primitives (confirmation-gated). |
| `schematic.wire.create` | Create a wire polyline (optional net/color/width/lineType). |
| `schematic.netflag.create` | Power / ground / analog-ground / protective-ground / net-port (IN/OUT/BI) / short-circuit flag. |
| `schematic.power.connect_pin` | Composite: draw a stub wire out of a pin **and** place a netflag/netport at its far end in one call. Structurally prevents the "netflag overlaps pin" DRC fatal and orients the flag body outward along the stub (顺着导线方向). Default direction inferred from kind, default offset 30u. |

### Library search (1 action)

| Action | What |
|---|---|
| `schematic.library.search` | Free-text search of the EasyEDA device library (`eda.lib_Device.search`); returns `libraryUuid` + `uuid` ready for `schematic.component.place`, plus name/value/footprint/lcsc/description. Replaces ad-hoc `debug.exec_js` lookups. **See the search caveat under Roadmap.** |

### Verify (2 actions)

| Action | What |
|---|---|
| `schematic.drc.check` | Run schematic DRC, normalized to `{passed, violations}`. |
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

- **`skills/easyeda-schematic/scripts`** — a data-only schematic checker (no screenshots): one
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
- **Connector self-healing reconnect** — the connector port-scans 49620-49629,
  validates a handshake, and reconnects on liveness loss. It **never permanently
  gives up**: after 5 fast retries it drops to a quiet 10s background poll, so a
  daemon started/restarted later auto-reconnects with no manual action. A
  low-volume `log` frame surfaces connection-lifecycle diagnostics in the daemon
  log (`connector LOG: …`).
- **`make eext` release flow** — bumps the PATCH version and builds an importable
  `.eext`. `make eext` keeps the uuid **stable** (update-in-place: uninstall old →
  import); `make eext-fresh` mints a **fresh uuid** (imports as a separate entry,
  no uninstall needed) as the fallback when the installed one won't uninstall.

---

## Verified end-to-end (this session)

Both boards were drawn **entirely from real LCSC / 立创 library parts** (search →
place by uuid → wire → flag), and lint-clean:

- a minimal **ESP32-S3-WROOM-1** system board, and
- a **USB-C + AMS1117-3.3** power board.

This proves the library-first workflow (place real parts, then wire) end to end,
not just hand-drawn custom symbols.

---

## Roadmap (NOT yet built)

These are planned and **not implemented** today.

- **器件标准化 / standard parts library** — a curated `skills/easyeda-conventions/references/standard-parts.json`
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

### LCSC C-number lost on placed parts → fixed by BOM enrichment

A placed component's `getState_SupplierId()` returns `MPN.1` (e.g.
`GRM21BR61H106KE43L.1`), not the LCSC C-number (`C440198`) — confirmed by reading
the exported BOM, whose "Supplier Part" column is the MPN.1. The component can't be
fixed at the source: `setState_SupplierId('C440198')` does **not** persist (the
field is device-bound and reverts on re-pull). So the fix is post-export:
**`skills/easyeda-schematic/scripts/bom-enrich.py`** joins the C-number in by matching each row's Manufacturer
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
