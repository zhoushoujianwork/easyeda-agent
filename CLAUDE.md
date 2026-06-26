# easyeda-agent

AI-native automation layer for **EasyEDA Pro (嘉立创EDA专业版)**. A skill drives a
Go daemon, which dispatches typed schematic actions to a connector extension
running inside EasyEDA, which calls the official `eda.*` API.

```
skill ──▶ Go CLI/daemon ──WebSocket──▶ connector .eext ──▶ eda.* API
          (typed actions)   49620-49629   (in EasyEDA Pro)
```
## 首要准则 — Skill 优先

> **本项目是「边开发、边更新 Agent Skill」的联合开发模式。**
>
> - **开发和测试的主要对象是 Skill**（操作技能 `skills/easyeda-schematic/`、`skills/easyeda-pcb/`，参考技能 `skills/easyeda-conventions/`）。
> - Go CLI/daemon（`cmd/easyeda` + `internal/`）和连接器插件（`extension/`）是**为 Skill 服务的基础设施**，而非最终目的。
> - 每次改动首先问：「Skill 里的工作流、知识、或 guardrail 需要同步更新吗？」——如果需要，先改 Skill，再改底层实现。
> - 修改底层 action / daemon / 插件后，必须同步更新 Skill 里对应的工具描述、示例、或注意事项。

## 首要准则 — CLI 子命令设计

详见 [`docs/cli-design.md`](docs/cli-design.md)。核心约束：所有明确的功能模块必须以 **Cobra 子命令**方式暴露（`easyeda sch`、`easyeda pcb`、`easyeda bom` …），`--help` 自描述，新功能先设计命令接口再写实现，Skill 描述与子命令签名保持同步。开发闭环：`debug.exec_js` → typed action → Cobra 子命令。

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
| `skills/easyeda-schematic/` | Operational skill (schematic) — typed-action workflow, `scripts/` (lint suite + BOM/parts tools), guardrails. Links to the conventions skill for design rules. |
| `skills/easyeda-pcb/` | Operational skill (PCB) — switch to a PCB, read components/layers/nets/board, `import_changes` from the schematic, lay out (move/rotate/align/distribute/grid-snap/cluster-arrange). Links to the conventions skill. |
| `skills/easyeda-conventions/` | Reference skill (no actions) — the EE design truth + canonical data in `references/`: orientation.json, standard-parts.json, schematic/pcb layout conventions, part-selection. See `skills/README.md`. |
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

## Skill scripts usage

All tools live in `skills/easyeda-schematic/scripts/`.

```bash
# 原理图 lint
skills/easyeda-schematic/scripts/lint.sh <project>           # 实时 lint；有 baseline 时只显示 DIFF
skills/easyeda-schematic/scripts/lint.sh <project> --save    # 全量 lint + 记录 baseline

# BOM 补全 LCSC C 号（导出后运行）
skills/easyeda-schematic/scripts/bom-enrich.py <bom.tsv>             # 输出到 stdout
skills/easyeda-schematic/scripts/bom-enrich.py <bom.tsv> --out <out> # 写入文件

# 器件选型
skills/easyeda-schematic/scripts/parts-select.py --help

# flag 旋转真值表校准（导入新 .eext 后跑一次，需要已连接的 EasyEDA 窗口）
# 在 EasyEDA 的 debug.exec_js 里粘贴 calibrate.js 内容
skills/easyeda-schematic/scripts/calibrate.js   # 读 getPrimitivesBBox 实测锚点

# lint 规则信任测试
make lint-test    # = python3 skills/easyeda-schematic/scripts/tests/run.py
```

`skills/easyeda-conventions/references/standard-parts.json` — 标准器件库（libraryUuid + deviceUuid + LCSC C 号）。放置前先查这里；新选型后写回。

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
  in `skills/easyeda-conventions/references/orientation.json` is the **stored-rotation** truth (the
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
