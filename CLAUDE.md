# easyeda-agent

## 项目工作流记忆

布局验证交付遵循 [.claude/memory/workflow.md](.claude/memory/workflow.md)，固定编译布局图，当前不自动打印差异图解。

AI-native automation layer for **EasyEDA Pro (嘉立创EDA专业版)**. A skill drives a
Go daemon, which dispatches typed schematic actions to a connector extension
running inside EasyEDA, which calls the official `eda.*` API.

```
skill ──▶ Go CLI/daemon ──WebSocket──▶ connector .eext ──▶ eda.* API
          (typed actions)      60832      (in EasyEDA Pro)
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

**每次做端到端测试，都必须把 [`esp32MiniRequire.md`](esp32MiniRequire.md) 的
**「一、客户原始需求」那一节**（4 层板 + 点灯 + 5V 供电端子 + 降压到 3V3 + CH340 USB
烧录 + BOOT/RESET 按键 + 四角 M3 固定，**故意不含 BOM/UUID/网表**）当输入，让 agent 自己
选型 → 放置 → 编组 → 布线 → `sch layout-lint` → DRC → 转 PCB（4 层叠层 / GND 内电层 /
丝印极性 / 天线 keepout）→ save 完整跑一遍**——照 `skills/easyeda-agent/references/design-flow.md`
流程脊柱（S0–S6 + P0–P10），不是只测单点，**也绝不喂加工过的答案**（喂好 BOM/网表就不叫真实场景了）。
这是 agent 从需求到成品的回归基准：layout-lint / autosave / design-flow / 连接器 任何改动后都重跑此用例。
验收：需求条条落实（0 overlap、0 fatal、网络连通、丝印/极性正、4 层电源树、已落盘）。
测试工程用 `--project ceshi`，测完清理还原。

同一份文件的**「二、怎么跑完这个 Demo」是给人看的 runbook**（环境自举、分段验收表、
会被问到的决策题目、验收命令、已知坑、收尾），**不是喂给 agent 的输入** —— 它只写
「你会被问到哪些题」，不写答案，所以不构成加工过的答案；但跑回归时仍然只交第一节。

## Notes

reply as chiense! reply as chiense! reply as chiense!

**Commit directly on `main` — do NOT create feature branches.** Develop and commit
on `main` by default (user preference). Don't `git checkout -b`; just commit to
`main`. When the user asks to submit fixes, update GitHub progress, or handle PRs,
that authorizes committing and pushing the related verified changes without a
second push confirmation. For completed issue/PR fixes, publish a release using
the version policy below and close fixed issues or fully adopted PRs with links
to the release/adoption commit; do not wait for reporter revalidation or add tests solely to close them.
Run required automated/release checks as part of delivery. Keep unresolved issues
open and report remaining validation gaps accurately.

## Layout

| Path | What |
|---|---|
| `cmd/easyeda` + `internal/{app,daemon,protocol}` | Go CLI + daemon. `internal/protocol/actions.go` = the typed action catalog. Daemon: `/health`, `/eda` (connector WS), `/action`. |
| `extension/` | TypeScript connector → esbuild → `.eext`. `src/transport.ts` (fixed-port reconnect with backoff), `src/actions.ts` (eda.* handlers + `connect_pin`). |
| `skills/easyeda-agent/` | Merged public skill — short `SKILL.md` router plus `references/` for design flow, schematic, PCB, conventions, canonical data, and `scripts/` for lint/BOM/parts/calibration tools. |
| `docs/FEATURES.md` | Feature-status inventory (actions grouped by capability) + roadmap. |
| `docs/pcb-design-rules.md` | PCB 设计规范手册 — 线宽/间距/过孔/布局/走线/铺铜/Mark点/拼板/叠层/DRC 清单，基于 JLC 工艺能力 + IPC-2221。 |
| `skills/easyeda-agent/SKILL.md` | The user-facing skill. |

## Dev workflow

**Keep the daemon hot-reloading while you work** (rebuilds + restarts on any `.go`
change; the connector reconnects to the fixed default port 60832 with backoff):

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
make blocks-audit # 块引脚引用 vs 真实符号引脚表(离线;首审揪出 14 个块 41 处错)
make layout-calibrate # layout-score 金标准板回归(离线):参考板九维不该掉分 +
                  # 负对照九维必须还会响。改 pcb_score_*.go 的判据/阈值/权重后先跑它。
                  # fixture 与「怎么加一块真板」见 internal/app/testdata/boards/README.md
make actions      # print the typed action catalog
make eext         # bump PATCH + build importable .eext, STABLE uuid (update in place: uninstall old → import)
make eext-fresh   # fallback: bump PATCH + FRESH uuid (imports as a new entry; delete the old one) — for when the installed one won't uninstall
make connector    # build .eext at the current version/uuid (no bump — same-version dev only)

skills/easyeda-agent/scripts/lint.sh <project>          # live lint (DIFF if a baseline exists)
skills/easyeda-agent/scripts/lint.sh <project> --save   # full lint + record baseline
```

## Release workflow

### Version and retention policy

- If the connector runtime (`extension/src/**`, its build/configuration, manifest capabilities,
  or action contract) does not change, increment patch: `1.4.4` → `1.4.5`.
- If users must update/re-import the connector to obtain the behavior, increment minor and reset
  patch: `1.4.x` → `1.5.0`. Breaking public contracts still require a major increment.
- CLI, daemon, connector asset and Skill keep one full release version even when the connector
  runtime is unchanged. The version gate treats patch-only connector drift as a warning and
  minor-or-larger drift as blocking.
- Only the newest patch in each minor line is maintained and presented as current. Never delete
  published Git tags, GitHub Releases or assets merely because a newer patch exists: they are
  rollback, checksum and audit records, and fixed-version install links may still depend on them.
  GitHub's `Latest` pointer and hub `latest` tags move to the newest release; older patches are
  historical/superseded and receive no further fixes.

发布分为本地准备与外部发布。准备阶段先显式同步
`extension/extension.json`、`extension/package.json`、`extension/package-lock.json`
的版本（含 lock 的 `packages[""].version`），补齐 `extension/CHANGELOG.md` 对应条目，
再同步 Skill。下面的版本号须替换为本次完整 `vX.Y.Z` 版本：

```bash
python3 scripts/sync-skill-version.py X.Y.Z   # 准备时显式写 metadata.version
make skill-check                            # 离线检查公共 Skill 文件及安装后链接
make release-check VERSION=vX.Y.Z           # 校验版本、Changelog、打包输入；不修改源码
make release-build VERSION=vX.Y.Z           # 本地构建并核对全部资产；不提交、打 tag 或上传
```

`release-check` 要求 connector manifest、npm/lock 和 Skill 版本全部匹配，拒绝缺失的
Changelog。`release-build` 生成五平台 CLI、准确版本/UUID 的连接器、`skills.tar.gz`、
安装脚本和 `checksums.txt`，并验证资产与本机 CLI 的版本。它不会自动 bump 或提交源码。
`make eext` 仍是开发期升 patch 的快捷入口，不代替发布准备所需的完整版本同步。

Skill 包只包含 Git 已跟踪/已暂存文件的当前内容；新公共参考须先审阅并暂存，本地
草稿不入包。`make skill-check` 验证链接在仅安装 Skill 的目录中仍然成立。
GitHub、ClawHub 和 SkillHub 共用此受控打包器，不直接上传夹带草稿的工作目录。

完成验收并提交已审阅源码后，只有得到发布指令才运行：

```bash
make release VERSION=vX.Y.Z
```

`release` 要求已跟踪源码没有未提交改动、tag 不存在；它重新执行 `release-build`，
然后创建并推送 tag、发布 GitHub Release，最后 best-effort 发布到 ClawHub。
它不再修改版本或自动提交。不能覆盖已发布版本；只做准备的任务停在本地资产验收。

用户安装和升级：

```bash
curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | bash
easyeda update            # CLI + 已安装 Skill → latest
easyeda update --check    # 只读 CLI / Skill / connector 版本表
```

安装变量要传给执行脚本的 `bash`，例如管道右侧 `EASYEDA_INSTALL_SKILLS=codex,claude bash`，
不要只设置在 `curl` 一侧。连接器侧载包仍需卸载旧项后导入新包，保存并重开编辑器加载新运行时。

**版本与自更新契约**：CLI、connector、Skill 发布版本一致。
`scripts/sync-skill-version.py --check` 只核验不写入，`release-check` 负责检查准备结果。
`metadata.version` 保持两空格缩进的 `  version:` 格式；安装态 `.version` 是自更新器
写入的运行时标记，和包内声明不是同一个文件。`checksums.txt` 使用裸资产文件名；
改资产名时同步 `scripts/release-check.py`、Makefile 与 `internal/selfupdate.AssetName`。
自更新遇到没有校验和的旧 release，会通过执行下载二进制比对版本作兼容检查。

### 外部发布平台

- **ClawHub**：`release` 尾部 best-effort 发布；失败可在已有发布授权下用
  `make publish-skill VERSION=vX.Y.Z` 重试，需要 `clawhub login`。
  同版本不可覆盖。发布使用临时包的绝对路径，避免全局 workdir 导向另一份 Skill；
  `CLAWHUB_TAGS` 必须保留 `latest`，否则最新安装指针不会更新。
- **skillhub.cn**：GitHub `release: published` 触发 `.github/workflows/publish-skill.yml`，
  也可手动触发或用 `make publish-skill-hub VERSION=vX.Y.Z` 补发。
  `SKILLHUB_DRY_RUN=1` 只做打包和平台预检。版本来自 release tag/手动输入的 SemVer，
  同 slug 同版本不能覆盖，发布后还需平台审核。
- **SkillHub 身份与凭据**：只使用官方 CLI 安装器
  `curl -fsSL https://skillhub.cn/install/install.sh | bash -s -- --cli-only`。
  同名 CLI 可能属于其他服务，`make skillhub-check` 按实际 `publish` 参数校验身份，
  必要时用 `SKILLHUB_BIN` 指定。仓库 secret 为 `SKILLHUB_TOKEN`；CI 的 publish
  直接读取同名环境变量并完成鉴权，不运行 login/whoami，不回显 token 或写入凭据文件。
- **SkillHub 包格式**：其 `slug/displayName` 只注入临时 staging 副本；仓库 `SKILL.md`
  保持 Agent Skills 格式。不要为了平台字段破坏公共包的 frontmatter。
- **立创连接器市场 jlc-ext**：仍需人工通过网页提交，没有发布 CLI/API。
  市场可自动更新已安装连接器，但可能落后于 GitHub Release；不能把仓库发布成功
  当成市场已更新。更多候选验收范围见 [docs/release-1.4.md](docs/release-1.4.md)。

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

# 块引脚引用审计 —— 块按功能名引用引脚,此前无人对过真实符号,导致块标着
# verified 却静默错接(ch340c 的 USB 口根本没供电)。离线判定,非零退出可 gate。
skills/easyeda-agent/scripts/blocks-pin-audit.py            # 审全库(离线,用引脚表快照)
skills/easyeda-agent/scripts/blocks-pin-audit.py --probe --project <scratch> --doc <page> --allow-clear
# 仅清空并使用明确指定的专用测量页；无需补测时不写画布。

# 暴露面健康度体检 —— 读 ~/.easyeda-agent/audit/*.jsonl,离线,不需要连编辑器。
# 出「调用分布+失败率 / 错路回退 / 逐日多样性」三张表。判读法:长尾失败率显著
# 高于头部 = 有「用得少所以坏了没人知道」的角落;失败率 100% 的行 = 从未工作过
# 的命令(首测抓到 titleblock.modify 32 次调用 0 次成功)。收敛验收基线见
# docs/design-sch-surface-convergence.md。
skills/easyeda-agent/scripts/audit-baseline.py              # 全部历史
skills/easyeda-agent/scripts/audit-baseline.py 2026-08      # 只看某月/某天

# 成本画像 —— **每跑完一场端到端都要记一笔**(用户要求,用以改善)。
# 三个耗时指标分开:墙钟 / daemon 侧(机器真在算)/ 两者之差(agent 思考+编译)——
# 改法完全不同。动作榜**按耗时排**:首版按次数排,把「探测占 65% 调用」顶到榜首,
# 而它只花 22 秒(机器时间 1.4%);真正吃掉 86% 的是 components.list(41%)/
# connect_pin(34%)/ document.open(11%,单次 4.24s)。次数的价值在别处 —— 它是
# 「跑了多少条 CLI 命令」的代理(每条固定 2~3 发探测)。
# token 不在审计日志里(那是 agent 侧的账),用 --tokens 自报,不给就记「未记录」。
easyeda audit cost --day 2026-08-15 --since 14:12 --until 15:50 --label "…" --tokens N --record
easyeda audit cost --ledger                                 # 跨批次对比台账
```

`skills/easyeda-agent/references/standard-parts.json` — 标准器件库（libraryUuid + deviceUuid + LCSC C 号）。放置前先查这里；新选型后写回。

For a connected window, EasyEDA must be open with the project AND have **"允许外部
交互 / Allow external interaction"** enabled, or the connector's WebSocket never
reaches the daemon.

## Load-bearing gotchas

- **Connector upgrade and marketplace boundaries.** EasyEDA deduplicates installed
  extensions by UUID: sideload upgrades use the stable UUID, uninstall the old entry,
  then import the new `.eext`. `make eext-fresh` is a fallback that creates a separate
  entry; remove the stale one afterward. Save documents and fully quit/relaunch
  EasyEDA to stop old connector code in already-open windows.
  The marketplace listing is https://jlc-ext.com/item/zhoushoujian/easyeda-agent-connector.
  Keep its approved internal `name` and UUID stable; the approved display name is
  "EDA Agent Connector" (marketplace names must not contain "easyeda"). Marketplace
  installs can auto-update, but publishing still requires the web portal and may lag
  GitHub releases. Sideloads do not auto-update; use the matching release package and
  inspect `health` rather than assuming the marketplace version is current.
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
