# easyeda-agent

AI-native automation layer for **EasyEDA Pro (嘉立创EDA专业版)**. A skill drives a
Go daemon, which dispatches typed schematic actions to a connector extension
running inside EasyEDA, which calls the official `eda.*` API.

```
skill ──▶ Go CLI/daemon ──WebSocket──▶ connector .eext ──▶ eda.* API
          (typed actions)   60832-60841   (in EasyEDA Pro)
```

## 官方插件库调研参考
文章：docs/ecosystem-survey.md，遇到什么不确认的情况可以来这里参考分析，并更新认知到相应文档；

## 核心概念拉通认知
[`docs/concepts.md`](docs/concepts.md) = 布局/布线域的**共享词汇表**(网 / 网感知 vs 几何 /
布局分档 T1–T4 / edge 语义 / 块数据模型 / 可信判据)。**引入或讨论新概念对象先落这里再引用**,
让后续会话、贡献者、Skill 用同一套心智模型。验收判据见 [`docs/e2e-automation-acceptance.md`](docs/e2e-automation-acceptance.md)。

## 首要准则 — Skill 优先

> **本项目是「边开发、边更新 Agent Skill」的联合开发模式。**
>
> - **开发和测试的主要对象是 Skill**（唯一对外入口 `skills/easyeda-agent/`）。
> - Go CLI/daemon（`cmd/easyeda` + `internal/`）和连接器插件（`extension/`）是**为 Skill 服务的基础设施**，而非最终目的。
> - 每次改动首先问：「Skill 里的工作流、知识、或 guardrail 需要同步更新吗？」——如果需要，先改 Skill，再改底层实现。
> - 修改底层 action / daemon / 插件后，必须同步更新 Skill 里对应的工具描述、示例、或注意事项。

## 首要准则 — CLI 子命令设计

详见 [`docs/cli-design.md`](docs/cli-design.md)。核心约束：所有明确的功能模块必须以 **Cobra 子命令**方式暴露（`easyeda sch`、`easyeda pcb`、`easyeda bom` …），`--help` 自描述，新功能先设计命令接口再写实现，Skill 描述与子命令签名保持同步。开发闭环：`debug.exec_js` → typed action → Cobra 子命令。

## 首要准则 — 固定测试用例（端到端验收）

**每次做端到端测试，都必须把 [`esp32MiniRequire.md`](esp32MiniRequire.md)
（客户口吻的**原始需求**：4 层板 + 点灯 + 5V 供电端子 + 降压到 3V3 + CH340 USB 烧录 +
BOOT/RESET 按键 + 四角 M3 固定，**故意不含 BOM/UUID/网表**）当输入，让 agent 自己
选型 → 放置 → 编组 → 布线 → `sch layout-lint` → DRC → 转 PCB（4 层叠层 / GND 内电层 /
丝印极性 / 天线 keepout）→ save 完整跑一遍**——照 `skills/easyeda-agent/references/design-flow.md`
流程脊柱（S0–S6 + P0–P10），不是只测单点，**也绝不喂加工过的答案**（喂好 BOM/网表就不叫真实场景了）。
这是 agent 从需求到成品的回归基准：layout-lint / autosave / design-flow / 连接器 任何改动后都重跑此用例。
验收：需求条条落实（0 overlap、0 fatal、网络连通、丝印/极性正、4 层电源树、已落盘）。
测试工程用 `--project ceshi`，测完清理还原。

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
| `skills/easyeda-agent/` | Merged public skill — short `SKILL.md` router plus `references/` for design flow, schematic, PCB, conventions, canonical data, and `scripts/` for lint/BOM/parts/calibration tools. |
| `docs/FEATURES.md` | Feature-status inventory (20 actions grouped by capability) + roadmap. |
| `docs/pcb-design-rules.md` | PCB 设计规范手册 — 线宽/间距/过孔/布局/走线/铺铜/Mark点/拼板/叠层/DRC 清单，基于 JLC 工艺能力 + IPC-2221。 |
| `skills/easyeda-agent/SKILL.md` | The user-facing skill. |

## Dev workflow

**Keep the daemon hot-reloading while you work** (rebuilds + restarts on any `.go`
change; the connector auto-reconnects because it port-scans 60832-60841 in the
background):

```bash
make dev          # air live-reload of `easyeda daemon` — leave running in a terminal
```

Requires [air](https://github.com/air-verse/air): `go install github.com/air-verse/air@latest`.
Config is `.air.toml`: on any `.go` change it runs `make dev-build` (version-stamped
build → `./bin/easyeda` **and** a best-effort copy to `$PREFIX/bin/easyeda`), then
runs the daemon from that same `./bin/easyeda`. **So the `easyeda` CLI on your PATH
is refreshed on every rebuild — daemon and CLI never drift.** (Before this, air only
rebuilt the daemon; the PATH CLI stayed frozen at the last `make install`, so a new
subcommand like `easyeda doc` was missing until you reinstalled.) If `$PREFIX/bin`
isn't writable, air prints a warning and you run `make install` once with sudo to fix
perms. The dev binary is git-describe-stamped (e.g. `v0.5.1-19-g…-dirty`); a
non-clean stamp is treated as "dev" by the `health` connector-version check, so it
never false-flags a connector as stale against a dev daemon.

Other targets:

```bash
make build        # bin/easyeda (version-stamped via git describe)
make install      # build + install to /usr/local/bin (PREFIX overridable; sudo only if needed)
make daemon       # one-shot daemon (no reload) — prefer `make dev`
make test         # go test ./...
make lint-test    # linter rule-trust harness (orientation consistency + fixtures)
make actions      # print the typed action catalog
make eext         # bump PATCH + build importable .eext, STABLE uuid (update in place: uninstall old → import)
make eext-fresh   # fallback: bump PATCH + FRESH uuid (imports as a new entry; delete the old one) — for when the installed one won't uninstall
make connector    # build .eext at the current version/uuid (no bump — same-version dev only)

skills/easyeda-agent/scripts/lint.sh <project>          # live lint (DIFF if a baseline exists)
skills/easyeda-agent/scripts/lint.sh <project> --save   # full lint + record baseline
```

## Release workflow

```bash
# 一条命令发版：自动把 connector + CLI 统一到同一版本，交叉编译 5 平台，
# 打包 skills.tar.gz，创建 GitHub Release 并上传所有 assets。
make release VERSION=v0.5.1

# 用户一行安装
curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | sh
```

**版本号约定**：CLI 和 connector 始终用同一版本号（`make release` 负责把 `extension.json` 同步到 VERSION，不需要提前跑 `make eext`）。`make release` 会自动打 git tag、push 并创建 GitHub Release，**并把 skill 同版本发布到 ClawHub**（best-effort，失败不阻断；重试 `make publish-skill VERSION=…`，需已 `clawhub login`）。ClawHub 版本号不可覆盖；`publish-skill` 必须用绝对路径——clawhub 的 workdir 会被全局配置劫持到 `~/clawd`，相对路径会把旧副本发上去（0.8.1 踩过）。skillhub.cn 无 CLI API（纯网页社区），不集成。

**Changelog 门禁**：`extension/CHANGELOG.md` 必须有对应版本的 `## [x.y.z]` 条目。`make release` 会**硬校验**（缺条目直接报错退出，发版前先补 changelog）；`make eext`（dev 循环）只**警告**不阻断。校验逻辑在 `extension/scripts/bump.mjs`（`--require-changelog`）。

## Skill scripts usage

All tools live in `skills/easyeda-agent/scripts/`.

```bash
# 原理图 lint
skills/easyeda-agent/scripts/lint.sh <project>           # 实时 lint；有 baseline 时只显示 DIFF
skills/easyeda-agent/scripts/lint.sh <project> --save    # 全量 lint + 记录 baseline

# BOM 补全 LCSC C 号（导出后运行）
skills/easyeda-agent/scripts/bom-enrich.py <bom.tsv>             # 输出到 stdout
skills/easyeda-agent/scripts/bom-enrich.py <bom.tsv> --out <out> # 写入文件

# 器件选型
skills/easyeda-agent/scripts/parts-select.py --help

# flag 旋转真值表校准（导入新 .eext 后跑一次，需要已连接的 EasyEDA 窗口）
# 在 EasyEDA 的 debug.exec_js 里粘贴 calibrate.js 内容
skills/easyeda-agent/scripts/calibrate.js   # 读 getPrimitivesBBox 实测锚点

# lint 规则信任测试
make lint-test    # = python3 skills/easyeda-agent/scripts/tests/run.py
```

`skills/easyeda-agent/references/standard-parts.json` — 标准器件库（libraryUuid + deviceUuid + LCSC C 号）。放置前先查这里；新选型后写回。

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
  uninstall. Our manifest is complete. **The connector is now LIVE on the official
  立创EDA (jlc-ext) marketplace** — https://jlc-ext.com/item/zhoushoujian/easyeda-agent-connector
  — so there are now two install channels: (1) a **sideloaded `.eext`** (the
  `make eext` / GitHub-Release path above) has **no in-place auto-update** (manual
  uninstall→import) but is **strictly version-locked to the CLI**, so it stays the
  source of truth for dev/regression; (2) a **marketplace-installed** copy the
  platform **can auto-update in place** — but the listing **lags** (currently
  v0.9.0 vs repo 0.11.x; there is no publish CLI/API for jlc-ext — each release is
  a manual web-portal re-submit), so a marketplace connector can be **older** than
  your CLI and flag `connectorVersionOk:false`. **Most changes don't
  even need a re-import — use the `debug.exec_js` escape hatch** for scriptable
  behavior; only manifest/handler changes require a rebuild. **And re-importing
  does NOT reload already-open EasyEDA windows** — an open window keeps running the
  OLD connector code and fights the freshly-imported one over the daemon socket;
  **fully quit and relaunch EasyEDA** to load new connector code.
- **EasyEDA schematic coords are y-UP** (+y renders upward). The orientation table
  in `skills/easyeda-agent/references/orientation.json` is the **stored-rotation** truth (the
  value `getState_Rotation` reads back for a correctly-oriented flag), validated
  read-only against real placed flags by `skills/easyeda-agent/scripts/calibrate.js`. **`createNetFlag` /
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
- **Edits are in-memory until saved.** `place`/`wire`/`modify` only change the
  EasyEDA document in memory; a window reload / daemon restart / crash loses
  unsaved work (bit us: placed parts vanished after an air hot-reload). The daemon
  now runs **debounced autosave** (`daemon start --autosave-debounce`, default
  **3s**, `0` disables) — after any successful *mutating* action it fires the
  matching typed save once edits quiesce (`schematic.save` for a schematic edit,
  `pcb.save` for a PCB edit; excludes the save action itself, so no recursion).
  It's a safety net,
  not a substitute for an explicit save at a known-good checkpoint (a process death
  within the debounce window still loses the last edits). Catalog `Mutates` flag
  drives which actions arm it; see `internal/daemon/autosave.go`.
- **Placement overlap is now mechanically checkable.** `easyeda sch layout-lint`
  pulls real rendered bboxes (`schematic.components.list --include-bbox` →
  `eda.sch_Primitive.getPrimitivesBBox`) and flags overlaps (ERROR, non-zero exit
  → gate-able) + tight spacing (WARN). More accurate than the old python
  `bbox_overlap`, which used a pin-extent approximation that underreported.

Deeper notes live in the per-fact memory under
`~/.claude/projects/-Users-mikas-github-easyeda-agent/memory/`.
