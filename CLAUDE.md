# easyeda-agent

AI-native automation layer for **EasyEDA Pro (嘉立创EDA专业版)**. A skill drives a
Go daemon, which dispatches typed schematic actions to a connector extension
running inside EasyEDA, which calls the official `eda.*` API.

```
skill ──▶ Go CLI/daemon ──WebSocket──▶ connector .eext ──▶ eda.* API
          (typed actions)   49620-49629   (in EasyEDA Pro)
```

## Layout

| Path | What |
|---|---|
| `cmd/easyeda` + `internal/{app,daemon,protocol}` | Go CLI + daemon. `internal/protocol/actions.go` = the 20 typed actions. Daemon: `/health`, `/eda` (connector WS), `/action`. |
| `extension/` | TypeScript connector → esbuild → `.eext`. `src/transport.ts` (port-scan + auto-reconnect), `src/actions.ts` (eda.* handlers + `connect_pin`). |
| `tools/schematic-lint/` | Data-only schematic linter (no screenshots) + rule-trust harness + diff baseline. |
| `docs/schematic-layout-conventions.md` | Layout/orientation conventions the agent must follow. |
| `skills/easyeda-schematic/SKILL.md` | The user-facing skill. |

## Dev workflow

**Keep the daemon hot-reloading while you work** (rebuilds + restarts on any `.go`
change; the connector auto-reconnects because it port-scans 49620-49629 in the
background):

```bash
make dev          # air live-reload of `easyeda daemon` — leave running in a terminal
```

Requires [air](https://github.com/air-verse/air): `go install github.com/air-verse/air@latest`.
Config is `.air.toml` (builds to `./tmp/easyeda`, runs `daemon`, watches `cmd/`+`internal/`).

Other targets:

```bash
make build        # bin/easyeda
make daemon       # one-shot daemon (no reload) — prefer `make dev`
make test         # go test ./...
make lint-test    # linter rule-trust harness (orientation consistency + fixtures)
make actions      # print the typed action catalog
make eext         # bump PATCH + mint a FRESH UUID + build importable .eext (prints import path)
make connector    # build .eext at the current version/uuid (no bump — same-version dev only)

tools/schematic-lint/lint.sh <project>          # live lint (DIFF if a baseline exists)
tools/schematic-lint/lint.sh <project> --save   # full lint + record baseline
```

For a connected window, EasyEDA must be open with the project AND have **"允许外部
交互 / Allow external interaction"** enabled, or the connector's WebSocket never
reaches the daemon.

## Load-bearing gotchas

- **Re-importing the connector is install-only per UUID.** EasyEDA dedups by
  UUID: re-importing a build whose uuid is already installed **silently fails** (a
  version bump alone is NOT enough — this bit us on v0.4.2). **`make eext` now
  mints a fresh uuid every build** (`scripts/bump.mjs`), so the printed `.eext`
  always imports as a clean install — just import it, then remove the older
  "EasyEDA Agent" entry (each fresh uuid is a separate extension). **Most changes
  don't even need a re-import — use the `debug.exec_js` escape hatch** for
  behavior you can script; only manifest/handler changes require `make eext`.
- **EasyEDA schematic coords are y-UP** (+y renders upward); `createNetFlag` /
  `createNetPort` rotation is **identity** (no negation). Orientation table lives
  in `tools/schematic-lint/orientation.json` (single source of truth, derived by
  both the linter and `connect_pin`).
- **A netflag must connect via a real wire** — overlapping the pin coordinate is
  NOT a connection (DRC won't see it).
- No programmatic undo in `eda.*`; `modify` only works on components (not flags —
  delete + recreate). Pull fresh primitive IDs right before mutating.

Deeper notes live in the per-fact memory under
`~/.claude/projects/-Users-mikas-github-easyeda-agent/memory/`.
