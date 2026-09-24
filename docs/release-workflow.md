# 发布准备与分发契约

合并原 `CLAUDE.md` 中仍有效的发布知识，按当前 Makefile 核对。发布授权与版本选择遵守
[AGENTS.md](../AGENTS.md)；普通推送不会触发正式发版。

## Version and retention policy

- If the connector runtime (`extension/src/**`, its build/configuration, manifest capabilities,
  or action contract) does not change, increment patch: `1.4.4` → `1.4.5`.
- If users must update/re-import the connector to obtain the behavior, increment minor and reset
  patch: `1.4.x` → `1.5.0`. Breaking public contracts still require a major increment.
- CLI, daemon, connector asset and Skill keep one full release version even when the connector
  runtime is unchanged. Version differences are compatibility diagnostics; missing actions or
  incompatible protocols require the matching runtime, not a version-based design permission gate.
- Only the newest patch in each minor line is maintained and presented as current. Never delete
  published Git tags, GitHub Releases or assets merely because a newer patch exists: they are
  rollback, checksum and audit records, and fixed-version install links may still depend on them.
  GitHub's `Latest` pointer and hub `latest` tags move to the newest release; older patches are
  historical/superseded and receive no further fixes.

## 中版本验收材料（从 v1.6.0 起）

发布 `vX.Y.0`（`Y > 0`）前，按[验收材料格式](releases/evidence/README.md)提交
`docs/releases/evidence/vX.Y.0/` 的测试报告、基准、测试用例和 `manifest.json`。
测试报告必须记录现场完整流程及局部修改的实际结果、失败项、回读证据和独立 Agent 复核；
基准记录原始需求、起始状态、环境版本与验收阈值；测试用例记录输入、步骤和判据。
固定端到端回归只把 `esp32MiniRequire.md` 第一节原始需求交给执行 Agent，不能把预制
BOM、UUID 或网表当输入。现场未过时写失败或进行中报告，不能填 `pass`。

`make release-check` 对中版本要求 Git 已跟踪的四个文件、`manifest.json` 的
`result=pass` 与 `independentReview=pass`、精确版本和三份文件的 SHA256；缺失或
不一致立即失败，并核对各用例在报告中逐项通过及现场回读、独立复核段落。
结构检查不能代替独立复核者判断证据真伪。`make release-build` 自动生成 `test-evidence.zip`、纳入
`checksums.txt` 并对照仓库文件核对。`make release` 把该包作为 GitHub Release
资产上传，发布说明中注明。开发预览版 `-dev.N` 与普通 patch 版不触发该门禁。

用户在 2026-09-25 明确将高级 CLI 验证留到下一版本，本次按基础 CLI 范围准备
`v1.7.0`：使用 schema 2，显式声明 `acceptanceScope=basic-cli` 与高级验证延期；
B00–B10 必须全部现场通过，A00–A06 必须明确 `not-run`，独立复核仍必需。
发布说明、基准和报告同步披露个人空间及既有自定义规则夹具的边界。历史 schema 1
仍按完整 E2E 校验；没有用户范围决定时不得选择基础范围规避失败。
范围内 `pass` 不代表高级设计通过；具体版本批准仍是实际发布的前置条件。

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
Changelog；中版本还检查上述验收材料。`release-build` 生成五平台 CLI、准确版本/UUID
的连接器、`skills.tar.gz`、安装脚本和 `checksums.txt`，并验证资产与本机 CLI 的版本。
它不会自动 bump 或提交源码。
`make eext` 仍是开发期升 patch 的快捷入口，不代替发布准备所需的完整版本同步。

Skill 包只包含 Git 已跟踪/已暂存文件的当前内容；新公共参考须先审阅并暂存，本地
草稿不入包。`make skill-check` 验证链接在仅安装 Skill 的目录中仍然成立。
GitHub、ClawHub 和 SkillHub 共用此受控打包器，不直接上传夹带草稿的工作目录。

完成验收并提交已审阅源码后，只有得到发布指令才运行：

```bash
make release VERSION=vX.Y.Z
```

`release` 要求已跟踪源码没有未提交改动；它重新执行 `release-build`，再次核对资产，
创建并推送 annotated tag，以 GitHub 草稿上传全部资产，核对远端名称、大小和 SHA256
（GitHub 未提供摘要时重新下载核对）后才发布为非草稿，最后 best-effort 发布到 ClawHub。上传中断时草稿和同一提交
的 tag 可由相同命令继续，不会把仅有 tag 或未验完的草稿称为正式发布。已发布版本
不能覆盖；只做准备的任务停在本地资产验收。命令不会修改版本或自动提交源码。

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

## 外部发布平台

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
  当成市场已更新。更多候选验收范围见 [release-1.4.md](releases/release-1.4.md)。
