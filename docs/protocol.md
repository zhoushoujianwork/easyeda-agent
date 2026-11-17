# Protocol

The protocol is the contract between the Go daemon and the EasyEDA connector extension. It is intentionally action-oriented rather than a raw mirror of every `eda.*` method.

## Request

```json
{
  "id": "req_01",
  "type": "request",
  "version": "v1",
  "windowId": "optional-target-window",
  "createdAt": "2026-06-25T09:00:00Z",
  "action": "schematic.components.list",
  "payload": {
    "allPages": false
  }
}
```

## Window routing

Each registered `windowId` identifies one live transport. Two connections may
report the same project, document and tab without being interchangeable. The
daemon keeps both; unqualified requests return `AMBIGUOUS_WINDOW`, and a project
hint that still matches multiple windows returns `AMBIGUOUS_PROJECT`. Pass an
explicit, verified `windowId` to choose one. Connector version and connection
arrival time do not select a target.

A disconnected explicit ID is never redirected by matching document/project.
The daemon returns `STALE_WINDOW` with current candidates when other windows are
online, or `NO_CONNECTOR` when none are online. An in-flight request that loses
its connection fails; it is not transferred or replayed on another connection.

## Response

```json
{
  "id": "req_01",
  "type": "response",
  "version": "v1",
  "windowId": "active-window",
  "createdAt": "2026-06-25T09:00:01Z",
  "ok": true,
  "context": {
    "projectUuid": "project-id",
    "projectName": "demo",
    "documentUuid": "schematic-page-id",
    "documentType": "schematic",
    "tabId": "tab-id",
    "unit": "mil"
  },
  "result": {},
  "artifacts": [],
  "warnings": []
}
```

## Mutation and save identity

Successful handler responses also carry independent document identity evidence:

| Field | Meaning |
| --- | --- |
| `context` | Best-effort post-action context or the handler's explicit readback; diagnostic only. |
| `mutationContext` | Pre-handler project/document/type/tab sample. A false or absent availability flag makes this diagnostic only. |
| `mutationContextAvailable` | Explicit `true` only when project/document/type are nonempty and equal before and after the handler; optional tab must be absent on both sides or equal on both. |
| `mutationContextError` | Reason for a false certification, including missing identity or observed drift. |

For example, a handler may complete while the active page changes:

```json
{
  "type": "response",
  "id": "req_02",
  "version": "v1",
  "ok": true,
  "result": { "saved": true },
  "context": { "projectUuid": "project-id", "documentUuid": "other-page", "documentType": "pcb" },
  "mutationContext": { "projectUuid": "project-id", "documentUuid": "original-page", "documentType": "pcb" },
  "mutationContextAvailable": false,
  "mutationContextError": "handler-boundary documentUuid changed"
}
```

The native result stays unchanged. The daemon does not arm autosave or cancel an
existing timer from an uncertified identity, including legacy connectors that
omit the availability flag. It retains the reason in response warnings,
`health.autosave`, and audit. Explicit saves cancel only timers older than the
request for the certified document. These samples cannot detect an ABA page
switch between the two observations and do not prove persistence; explicit
save/reload/readback remains required. The typed `document.source.roundtrip`
and `document.source.restore` writers use the certified document type to select its autosave action; dry-run
and other generic-domain actions do not infer a save target.

## Error

```json
{
  "id": "req_01",
  "type": "response",
  "version": "v1",
  "ok": false,
  "error": {
    "code": "SCHEMATIC_PAGE_NOT_ACTIVE",
    "message": "No active schematic page is open.",
    "detail": "Open a schematic page before placing components."
  }
}
```

## Artifact

Artifacts are files produced by EasyEDA or the connector (snapshots, netlists, BOM files). The connector runs in a webview and cannot write to the daemon's disk, so it returns the bytes inline as base64. The daemon decodes them, writes the file under its artifact directory, fills `path`/`size`/`sha256`, and strips `inlineBase64` before returning to the caller.

Connector → daemon (on the WebSocket):

```json
{
  "id": "art_01",
  "kind": "schematic_snapshot",
  "fileName": "snapshot.png",
  "mimeType": "image/png",
  "inlineBase64": "iVBORw0KGgo..."
}
```

Daemon → caller (after persistence):

```json
{
  "id": "art_01",
  "kind": "schematic_snapshot",
  "path": "/absolute/path/to/artifacts/art_01.png",
  "fileName": "snapshot.png",
  "mimeType": "image/png",
  "size": 123456,
  "sha256": "..."
}
```

## Raw JavaScript Escape Hatch

Raw JavaScript may exist as a debug action:

```text
debug.exec_js
```

It must be disabled or confirmation-gated in normal Skill workflows. Typed actions are the default public surface.
