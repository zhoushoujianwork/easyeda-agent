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
