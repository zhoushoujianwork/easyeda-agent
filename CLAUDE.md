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
make eext         # bump version + build an importable connector .eext
make connector    # build .eext at the current version (no bump)

tools/schematic-lint/lint.sh <project>          # live lint (DIFF if a baseline exists)
tools/schematic-lint/lint.sh <project> --save   # full lint + record baseline
```

For a connected window, EasyEDA must be open with the project AND have **"允许外部
交互 / Allow external interaction"** enabled, or the connector's WebSocket never
reaches the daemon.

## Load-bearing gotchas

- **Re-importing the connector is install-only per UUID.** Once a UUID is
  installed you can't re-import a newer build with the same UUID (and a stuck one
  may not uninstall). **Most changes don't need a re-import — use the
  `debug.exec_js` escape hatch.** For manifest/handler changes: stop the daemon,
  uninstall, re-import; if it won't uninstall, ship a **fresh UUID** (`node -e
  "console.log(crypto.randomUUID().replaceAll('-',''))"`) + restart EasyEDA.
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
