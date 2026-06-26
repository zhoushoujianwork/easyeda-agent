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

- **`tools/schematic-lint`** — a data-only schematic checker (no screenshots): one
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

- **器件标准化 / standard parts library** — a curated `tools/standard-parts.json`
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

### Known gap — LCSC C-number is lost on placed parts

A placed component's `getState_SupplierId()` returns `MPN.1` (e.g.
`GRM21BR61H106KE43L.1`) rather than the LCSC C-number (e.g. `C440198`). So
`schematic.library.search` can surface a C-number from search results, but once a
part is on the canvas the linkage back to a direct LCSC order is incomplete. A
robust fix (carry the C-number through placement, or resolve MPN → C-number) is
pending.

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
