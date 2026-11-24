# v1.20.0 已发布

本版本经用户于 2026-11-24 批准具体版本后，从固定干净的 `main` 提交执行
`make release VERSION=v1.20.0` 发布。没有重启或替换用户正在运行的 CLI、daemon 或连接器
（现场验收使用 edadesk Dev 的独立运行时，见证据）。

## 来源与范围

- 开发集成分支 `dev` 的 `789279d4`（发布准备 PR #337 合入后）经发布 PR
  [#338](https://github.com/zhoushoujianwork/easyeda-agent/pull/338) 合入 `main`，
  合并提交 `3b31af47229bf5f153587adaace34629a851c3a0`。
- 相对 v1.19.0 的改动：PCB 孔/槽几何与板边中心线测量（EDAD-8）；原理图页改名恢复与宿主诊断
  （EDAD-50、edadesk#88）；图签压线检查与只读图签几何（EDAD-41）；track/via 部分删除非零退出
  （EDAD-6）；原生自动布线可取消作业（EDAD-10）；`/health` 写入活动（edadesk#59）；DSH 分发
  补齐 npm 清单、预构建 `easyeda-agent-dsh.tgz` 与根包版本同步（#334/#335/#336）。
- 连接器运行时自 v1.19.0 起有改动（页面改名恢复与诊断、孔/槽采集、自动布线作业），按版本策略
  升中版本。

## 合入与检查

- 发布准备 PR [#337](https://github.com/zhoushoujianwork/easyeda-agent/pull/337)：版本号统一为
  1.20.0（extension、两处 lock、根 DSH bundle、Skill）、Changelog `[1.20.0]`、
  `docs/releases/evidence/v1.20.0/`（schema 3、`cli-fixes`、完整设计 E2E 延期 follow-up）。
- 两个 PR 的五项 CI 最终全部通过；发布提交上的 main CI 通过。
- 本机（macOS arm64）准备阶段：`go vet ./...`、`go test ./...`（6256 通过 / 0 失败 /
  3 平台或无测试文件 skip）、连接器 1416 项、`tsc`、`make agent-check skill-check release-script-test`、
  `make release-check VERSION=v1.20.0`、`make release-build VERSION=v1.20.0` 均通过。
- **独立复核**：独立 Agent 只读复核证据与原始记录，结论 `pass`、无阻塞项；非阻塞意见
  （哈希表、字段表述、`hostStates` 归属、退出码与 M1 原始日志）已在冻结前处理。

## 正式发布及验证结果

- [v1.20.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.20.0) 于
  2026-11-24T10:32:08Z 发布，非草稿、非预发布。附注标签对象
  `baca02f82e9a15a2b47e899d5d6a9af737ab3a0a` 绑定 main 合并提交 `3b31af47`。
- 发布包含 12 个资产：五平台 CLI、连接器 `.eext`、`skills.tar.gz`、安装脚本、`checksums.txt`、
  中版本证据 `test-evidence.zip`，以及**新增的预构建 DSH bundle `easyeda-agent-dsh.tgz`**
  （652,733 字节，SHA256 `890cf134ebc3bb353776b2e0d881659ed6e3c5859ca5166115a27fe240b792b5`，
  已在 `checksums.txt` 中）。`publish-release.py` 对草稿与正式两次下载并校验了资产摘要。
- macOS arm64 CLI SHA256 `5ab32d282820735e993564d03af093a00032c26a27e7bbe8856a8b6fede49777`。
- 发布后 `curl -I` 确认 tarball URL 返回 200 且 Content-Length 为 652,733。
- 隔离安装/升级冒烟本轮未做；只用本地 `dist/` 的 `release-check --artifacts` 与发布器的下载校验
  验证资产一致性，不把它写成安装通过。

## 外部市场状态

- ClawHub：返回 `Published easyeda-agent@1.20.0`，回执 `k978167ndd16egny6beryqqvjx8jt5eh`；
  公开 latest 是否已切换未核对，不声称公开可安装。
- SkillHub：`Publish skill to skillhub.cn` workflow 成功（`easyeda-agent v1.20.0`），前台审核状态未核对。
- DSH 插件市场：目录条目 PR
  [awesome-dsh-plugin#18440](https://github.com/awesome-dsh-plugin/awesome-dsh-plugin/pull/18440)
  已提交，并已补 `tarball:` 指向上面的 v1.20.0 资产；截至发布时仍在等待该仓库 CI/审核。
- 立创连接器市场未在本轮更新；连接器运行时变化，需要时另行重投。

## 现场验证范围

宿主为 edadesk Dev 0.2.0 中的 EasyEDA Pro 4.1.105，`ceshi` 工程，daemon `127.0.0.1:60833`，
连接器 `1.19.1-dev.4`；验收范围由用户选择 schema 3 `cli-fixes`。

- 已通过：全量自动回归；页面改名成功与重名拒绝（带两阶段宿主状态）；PCB 槽孔弧度旋转与偏移
  噪声内可测、`copperToEdgeMil` 来源、只读图签几何与 `pcb check`；v1.19.0 负对照；保存/检查
  的 checkpoint；未知 ID 删除的写前拒绝与删除后 33 器件。
- 仅自动测试、未现场制造：自动布线取消作业与进度、track/via 部分删除路径、图签压线检出、
  板边中心线测量（测试板无近边铜）。
- 完整设计 E2E、DRC 修复、保存重载与新回读未执行，延期 follow-up，不沿用旧版本结论。
- 详细用例、原始记录哈希与边界见 [v1.20.0 证据](evidence/v1.20.0/test-report.md)。
