# easyeda-agent

AI-native automation layer for **EasyEDA Pro (嘉立创EDA专业版)**. A skill drives a
Go daemon, which dispatches typed schematic actions to a connector extension
running inside EasyEDA, which calls the official `eda.*` API.

```
skill ──▶ Go CLI/daemon ──WebSocket──▶ connector .eext ──▶ eda.* API
          (typed actions)   49620-49629   (in EasyEDA Pro)
```
## Notes

reply as chiense! reply as chiense! reply as chiense!

**Commit directly on `main` — do NOT create feature branches.** Develop and commit
on `main` by default (user preference). Don't `git checkout -b`; just commit to
`main`. Push only when explicitly asked.

## Layout

| Path | What |
|---|---|
| `cmd/easyeda` + `internal/{app,daemon,protocol}` | Go CLI + daemon. `internal/protocol/actions.go` = the 20 typed actions. Daemon: `/health`, `/eda` (connector WS), `/action`. |
| `extension/` | TypeScript connector → esbuild → `.eext`. `src/transport.ts` (port-scan + auto-reconnect), `src/actions.ts` (eda.* handlers + `connect_pin`). |
| `skills/easyeda-schematic/` | User-facing skill. `scripts/` = lint suite + BOM/parts tools. `references/` = standard-parts.json, orientation.json. |
| `docs/schematic-layout-conventions.md` | Layout/orientation conventions the agent must follow. |
| `docs/FEATURES.md` | Feature-status inventory (20 actions grouped by capability) + roadmap. |
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
make eext         # bump PATCH + build importable .eext, STABLE uuid (update in place: uninstall old → import)
make eext-fresh   # fallback: bump PATCH + FRESH uuid (imports as a new entry; delete the old one) — for when the installed one won't uninstall
make connector    # build .eext at the current version/uuid (no bump — same-version dev only)

skills/easyeda-schematic/scripts/lint.sh <project>          # live lint (DIFF if a baseline exists)
skills/easyeda-schematic/scripts/lint.sh <project> --save   # full lint + record baseline
```

For a connected window, EasyEDA must be open with the project AND have **"允许外部
交互 / Allow external interaction"** enabled, or the connector's WebSocket never
reaches the daemon.

## Load-bearing gotchas

- **Re-importing the connector: EasyEDA dedups installed extensions by UUID.**
  Importing a build whose uuid is already installed **silently fails** unless you
  first **uninstall the old one** in the 已安装 tab — a version bump alone is NOT
  enough (this bit us on v0.4.2). Two paths: **`make eext`** keeps the uuid stable
  → the normal update-in-place (uninstall old → import the printed `.eext`, one
  entry). **`make eext-fresh`** mints a new uuid → imports as a *separate* entry
  with no uninstall, but you must delete the stale one (two connectors fight over
  the daemon otherwise) — it's the fallback when the installed one won't
  uninstall. Our manifest is complete; there is no in-place auto-update for
  sideloaded `.eext` (that's a marketplace-only feature). **Most changes don't
  even need a re-import — use the `debug.exec_js` escape hatch** for scriptable
  behavior; only manifest/handler changes require a rebuild. **And re-importing
  does NOT reload already-open EasyEDA windows** — an open window keeps running the
  OLD connector code and fights the freshly-imported one over the daemon socket;
  **fully quit and relaunch EasyEDA** to load new connector code.
- **EasyEDA schematic coords are y-UP** (+y renders upward). The orientation table
  in `skills/easyeda-schematic/references/orientation.json` is the **stored-rotation** truth (the
  value `getState_Rotation` reads back for a correctly-oriented flag), validated
  read-only against real placed flags by `skills/easyeda-schematic/scripts/calibrate.js`. **`createNetFlag` /
  `createNetPort` STORE rotation negated** on the 2026-06 build — confirmed via
  `connect_pin(direction=left)`: it passed `90`, the flag stored `270` and rendered
  pointing **right** (up/down at 0/180 are symmetric, which is why it hid for so
  long). `connect_pin` now **auto-detects this at runtime** (`detectRotationNegation`,
  a one-shot probe flag) and compensates, so its output is correct whether the build
  negates or not. Two follow-ons: (1) if you create flags via **raw**
  `eda.createNetFlag` (`debug.exec_js`), YOU must pass the negated value — or just
  use `connect_pin`; (2) `getState_Rotation()` *immediately* after create can echo
  the input — a fresh **re-pull** (`getAll`) shows the real stored value.
- **A netflag must connect via a real wire** — overlapping the pin coordinate is
  NOT a connection (DRC won't see it).
- No programmatic undo in `eda.*`; `modify` only works on components (not flags —
  delete + recreate). Pull fresh primitive IDs right before mutating.

Deeper notes live in the per-fact memory under
`~/.claude/projects/-Users-mikas-github-easyeda-agent/memory/`.
