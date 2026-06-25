# EasyEDA Agent Connector

A real, buildable **EasyEDA Pro extension** (嘉立创EDA Pro / 立创EDA Pro / EasyEDA Pro)
that bridges the **easyeda-agent Go daemon** to the official `eda.*` API over a
local WebSocket. It is the only component allowed to call `eda.*`.

```text
Skill workflow -> Go CLI/daemon (typed actions) -> THIS connector -> official eda.* API
```

Business logic, confirmation, verification, unit handling, and multi-step
orchestration live in the **Go layer and Skills**, not here. This connector only
does: transport (port-scan + handshake + register + context + heartbeat),
typed-action dispatch (one `eda.*` cluster per action), result serialization,
binary artifact transfer, and structured errors.

This extension **adapts the proven transport** from
[`eext-run-api-gateway`](https://github.com/easyeda/eext-run-api-gateway) and
replaces its raw-JS `execute` path with our typed-action dispatcher. It uses
`eda.sys_WebSocket` (NOT browser `WebSocket`/`fetch`) and type-checks against
[`@jlceda/pro-api-types`](https://www.npmjs.com/package/@jlceda/pro-api-types).

---

## File layout

```
extension/
  src/
    index.ts        # Entry point. Exports activate/deactivate + menu fns.
    transport.ts    # eda.sys_WebSocket port-scan, handshake, register, heartbeat, dispatch loop.
    actions.ts      # typed action handlers → eda.* calls + JSON serialization + artifacts.
    eda-context.ts  # Reads project/document context; document-type label mapping; editor version.
    protocol.ts     # Wire-frame types, error codes, ActionError/ActionResult.
    util.ts         # Uint8Array→base64 (no Node Buffer), Blob→base64, payload field helpers.
  config/
    esbuild.common.ts   # Shared esbuild config (format: 'iife', bundle: true, platform: 'browser').
    esbuild.prod.ts     # Build runner (supports --watch).
  build/
    packaged.ts     # Zips the extension into build/dist/<name>_v<version>.eext.
  extension.json    # EasyEDA manifest (uuid, engines.eda, activationEvents, headerMenus).
  tsconfig.json     # Strict TS, targets the @jlceda/pro-api-types ambient globals.
  package.json      # Scripts + devDependencies.
  .edaignore        # Files excluded from the packaged .eext.
```

Source is modular but esbuild **bundles every `src/*.ts` into a single IIFE**
`dist/index.js` (entry `src/index.ts`, manifest `entry: "./dist/index"`).
`dist/` is gitignored and produced by the build.

---

## Build / typecheck / package

```bash
cd extension
npm install            # esbuild, typescript, @jlceda/pro-api-types, fs-extra, jszip, ignore, ts-node, ...
npm run typecheck      # tsc --noEmit against @jlceda/pro-api-types (proves eda.* call shapes)
npm run compile        # esbuild → dist/index.js (IIFE)
npm run build          # compile + package → build/dist/easyeda-agent-connector_v<version>.eext
```

Node >= 20.17.0 is required.

### Versioning — required to re-import

Bump the version before handing over a new `.eext` (a same-version re-import may
be a no-op). The **reliable** path is: **uninstall the old version in EasyEDA's
Extension manager, then import the new `.eext`** — that always works regardless
of patch/minor.

```bash
# from the repo root — bump patch + typecheck + build a fresh importable .eext:
make eext

# or, from extension/, with explicit level:
npm run release         # bump patch + typecheck + build  (use this to ship)
npm run bump minor      # 0.4.x -> 0.5.0
npm run bump            # 0.4.0 -> 0.4.1
```

`scripts/bump.mjs` keeps `extension.json` and `package.json` in lock-step.

> An earlier note here claimed "patch bumps don't trigger an update, use minor."
> That was a misdiagnosis: `0.3.0` failing to install was a **packaging** defect
> (logo shipped as PNG; the proven upstream uses `images/logo.jpg`), not the
> version scheme. Keep the package clean (see `.edaignore`) and the logo as JPG.

---

## Sideloading into EasyEDA Pro

1. Run `npm run build` to produce `build/dist/easyeda-agent-connector_v0.1.0.eext`.
2. In EasyEDA Pro: open the **Extension manager** and load/import the `.eext`
   file (or point it at this `extension/` directory in a dev install).
3. **Enable required permissions** for the extension:
   - **允许外部交互 / Allow external interaction** — **REQUIRED.**
     `eda.sys_WebSocket.register/send/close` throw if this is off, so the
     connector cannot reach the daemon without it.
   - **Show in top menu** — so the `EasyEDA Agent` header menu (Reconnect / Stop /
     Toggle Auto-Connect / About) is visible.
4. Start the easyeda-agent Go daemon (it listens on one of ports 49620-49629).
5. The extension auto-connects on startup (`onStartupFinished`) when
   auto-connect is enabled; otherwise use **EasyEDA Agent → Reconnect**.

---

## WebSocket wire protocol

Transport: `eda.sys_WebSocket.register("easyeda-agent", "ws://127.0.0.1:<port>/eda", onMessage, onConnected)`.
Ports 49620-49629 are scanned; for each, the connector registers and waits
~1500ms for the daemon's `handshake`. On success the connection is kept;
otherwise it is closed and the next port is tried. All frames are JSON text
(`event.data` is a raw JSON string that we `JSON.parse` ourselves).

Frame sequence:

1. **Daemon → connector (on connect):**
   `{"type":"handshake","service":"easyeda-agent","version":"<daemon ver>"}`
   — validated: `service` must equal `"easyeda-agent"`.
2. **Connector → daemon (after valid handshake):**
   `{"type":"register","windowId":"<uuid>","connectorVersion":"0.1.0","easyedaVersion":"<eda>","capabilities":["schematic.v1"]}`
   (`windowId` via `crypto.randomUUID()`).
3. **Connector → daemon (best-effort):**
   `{"type":"context","windowId":"...","projectUuid":"...","projectName":"...","documentUuid":"...","documentType":"schematic","tabId":"..."}`
   — empty fields are omitted.
4. **Heartbeat:** connector sends `{"type":"ping","id":"hb-<n>"}` every 15s; a
   missing `pong` within 5s is treated as dead → reconnect. A daemon-initiated
   `{"type":"ping","id":...}` is answered with `{"type":"pong","id":...}`.
5. **Daemon → connector:**
   `{"type":"request","id":"req_N","version":"v1","action":"<action>","payload":{...},"windowId":"..."}`.
6. **Connector → daemon (echoing `id`):**
   `{"type":"response","id":"req_N","version":"v1","ok":true,"result":{...},"context":{...},"artifacts":[...],"warnings":[...]}`
   or on failure
   `{"type":"response","id":"req_N","version":"v1","ok":false,"error":{"code":"...","message":"...","detail":"<original eda error>"}}`.

Auto-reconnect: up to 5 retries, 3s apart, before giving up (re-armed by
**Reconnect**).

---

## Action → `eda.*` mapping (16 Phase-1 actions)

All `eda.*` calls are `await`ed. Component fields are read via `getState_*()`
accessors. Coordinates are passed through from the payload unchanged (unit
handling is the daemon/skill's concern).

| Action | `eda.*` call(s) |
| --- | --- |
| `project.current` | `dmt_Project.getCurrentProjectInfo()` → `{uuid,name,friendlyName,teamUuid,description}` |
| `document.current` | `dmt_SelectControl.getCurrentDocumentInfo()` → maps numeric `documentType` → label |
| `schematic.pages.list` | `dmt_Schematic.getAllSchematicsInfo()` + `getAllSchematicPagesInfo()` |
| `schematic.page.open` | `dmt_EditorControl.openDocument(schematicPageUuid)` → `{tabId}` |
| `schematic.components.list` | `sch_PrimitiveComponent.getAll(undefined, allPages)`; optional `getAllPinsByPrimitiveId(id)` when `includePins:true` |
| `schematic.component.place` | `sch_PrimitiveComponent.create({libraryUuid,uuid}, x, y, subPartName?, rotation?, mirror?, addIntoBom?, addIntoPcb?)` |
| `schematic.component.modify` | `sch_PrimitiveComponent.modify(primitiveId, patch)` |
| `schematic.component.delete` | `sch_PrimitiveComponent.delete(primitiveIds)` → `{deleted}` |
| `schematic.wire.create` | `sch_PrimitiveWire.create(points, net?, color?, lineWidth?, lineType?)` |
| `schematic.netflag.create` | branches on `kind` (see below) |
| `schematic.select` | `sch_SelectControl.doSelectPrimitives(primitiveIds)` then `getAllSelectedPrimitives_PrimitiveId()` |
| `schematic.snapshot` | `dmt_EditorControl.getCurrentRenderedAreaImage(tabId?)` → Blob → artifact |
| `schematic.drc.check` | `sch_Drc.check(strict, false, true)` → `{passed: arr.length===0, violations: arr}` |
| `schematic.save` | `sch_Document.save()` → `{saved}` |
| `schematic.export.netlist` | `sch_ManufactureData.getNetlistFile(fileName?, netlistType?)` → File → artifact |
| `schematic.export.bom` | `sch_ManufactureData.getBomFile(fileName?, fileType, template?, filterOptions?, statistics?, property?, columns?)` → File → artifact |

> Note: `system.health` (action #1 in `Phase1Actions()`) is handled by the Go
> daemon itself (daemon/connector liveness) and is not dispatched to the
> connector, so it is not in the table above.

### `schematic.netflag.create` — payload `kind` → API mapping

| `payload.kind` | API call | identification / direction |
| --- | --- | --- |
| `power` | `createNetFlag` | `'Power'` |
| `ground` | `createNetFlag` | `'Ground'` |
| `analog_ground` | `createNetFlag` | `'AnalogGround'` |
| `protective_ground` / `protect_ground` | `createNetFlag` | `'ProtectGround'` |
| `net_port_in` | `createNetPort` | `'IN'` |
| `net_port_out` | `createNetPort` | `'OUT'` |
| `net_port_bi` | `createNetPort` | `'BI'` |
| `short_circuit` | `createShortCircuitFlag` | — (no net) |

`net` is required for `createNetFlag` and `createNetPort`; `short_circuit` takes
only `x, y, rotation?, mirror?`.

---

## Artifact transfer (snapshot / netlist / bom)

`getCurrentRenderedAreaImage` returns a **Blob**; `getNetlistFile`/`getBomFile`
return a **File**. The connector cannot write to the daemon's disk, so it reads
the bytes, base64-encodes them (a manual `Uint8Array → base64` helper in
`util.ts`; no Node `Buffer`, no `btoa`), and returns them inline:

```json
{
  "id": "art_<uuid>",
  "kind": "schematic_snapshot | schematic_netlist | schematic_bom",
  "mimeType": "<blob.type or inferred>",
  "fileName": "<name.ext>",
  "inlineBase64": "<base64 bytes>"
}
```

The **daemon** decodes `inlineBase64`, writes the file, and fills
`path`/`size`/`sha256`. The connector only produces
`{id, kind, mimeType, fileName, inlineBase64}`.

---

## Error handling

Handlers throw `ActionError(code, message, detail)`; the original `eda.*` error
message is preserved in `error.detail`. Stable codes:

- `UNKNOWN_ACTION` — no handler for the action name.
- `MISSING_PAYLOAD_FIELD` — a required payload field is missing/invalid.
- `EDA_API_UNAVAILABLE` — the global `eda` object is not present.
- `EDA_CALL_FAILED` — an `eda.*` call threw or returned no result.
- `INTERNAL_ERROR` — an unexpected non-ActionError was thrown.

---

## Menu actions (`extension.json` → exported fns)

| Menu item | Exported fn |
| --- | --- |
| Reconnect | `reconnect` |
| Stop | `stopConnection` |
| Toggle Auto-Connect | `toggleAutoConnect` |
| About... | `about` |

`activate()` (auto-start on `onStartupFinished`) and `deactivate()` (cleanup)
are also exported. Auto-connect preference is stored via
`eda.sys_Storage.get/setExtensionUserConfig("autoConnectEnabled")`.

---

## What remains uncertain

- **Artifact transfer is an assumption.** The protocol carries bytes inline as
  base64 because the connector has no filesystem access to the daemon's machine.
  This works but is memory-heavy for large BOM/netlist/snapshot files; a future
  chunked/streamed transfer may be warranted. The daemon must implement the
  `inlineBase64` → file decode side.
- **Coordinate units.** Coordinates are passed through verbatim. Per the SDK,
  schematic canvas units span `0.01 inch`. Unit interpretation/conversion is the
  daemon/skill's responsibility, not the connector's.
- **DRC violation shape.** `sch_Drc.check(..., true)` returns `Array<any>` — the
  SDK does not type the violation objects. They are passed through untouched as
  `result.violations`; an empty array means DRC passed.
- **`easyedaVersion`** is read from `eda.sys_Environment.getEditorCurrentVersion()`
  (best-effort; falls back to `""`).
- **`eda.sch_PrimitiveComponent` is a union type** (`SCH_PrimitiveComponent |
  SCH_PrimitiveComponent3`) in the SDK; both members expose identical method
  shapes, so calls type-check cleanly. Component/pin primitive types are derived
  from the API return types (`Awaited<ReturnType<...>>`) rather than the
  internal `$1`-suffixed class names.
- **Net-flag library devices.** `createNetFlag`/`createNetPort` rely on the
  EasyEDA defaults; if a project needs custom flag symbols, the
  `setNetFlagComponentUuid_*` / `setNetPortComponentUuid_*` setters would need
  wiring (not exposed as actions in Phase 1).
