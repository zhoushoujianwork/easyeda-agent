# v1.21.0 已发布

本版本经用户于 2026-11-25 批准具体版本（v1.21.0）后，从固定干净的 `main` 提交执行
`make release VERSION=v1.21.0` 发布。现场验收使用 EdaDesk DEV 的独立运行时（见证据），没有重启或替换
用户正在运行的 daemon 或连接器。

## 来源与范围

- 开发集成分支 `dev` 的 `3d08f744`（发布准备 PR [#343](https://github.com/zhoushoujianwork/easyeda-agent/pull/343)
  合入后）经发布 PR [#344](https://github.com/zhoushoujianwork/easyeda-agent/pull/344) 合入 `main`，
  合并提交 `381087c72fc4b954557837d56ae1fb9c1668ba06`（树与 `3d08f744` 相同）。
- 相对 v1.20.0 的改动只有 [#341](https://github.com/zhoushoujianwork/easyeda-agent/issues/341)：新增
  `pcb.silk.delete` / `easyeda pcb silk-delete --id`（PR [#342](https://github.com/zhoushoujianwork/easyeda-agent/pull/342)），
  按 ID 删除单条自由丝印并核实已消失。
- 新增连接器动作，需要更新连接器才能使用，按版本策略升中版本。用户最初要求 v1.20.1，被告知规则后选择 v1.21.0。

## 合入与检查

- #343：版本统一为 1.21.0（extension、两处 lock、根 DSH bundle、Skill）、Changelog `[1.21.0]`、
  `docs/releases/evidence/v1.21.0/`（schema 3、`cli-fixes`、完整设计 E2E 延期 follow-up）。
- #343、#344 的五项 CI 全部通过。
- 本机（macOS arm64）候选 `55fef8f4`：`go vet`、`go test`（6290 个测试 pass 含子测试 / 0 失败 / 1 平台限定 skip）、
  连接器 1420 项、`tsc`、`make agent-check skill-check release-script-test`、`make release-check` 与
  `make release-build VERSION=v1.21.0` 均通过。
- **独立复核**：独立 Agent 只读复核证据与原始记录。首轮 `fail`，两个阻塞项（路径替换措辞与日志不符、模型/Auto/
  修订号缺日志证据）在冻结前修正；复核改动处后结论 `pass`。

## 正式发布及验证结果

- [v1.21.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.21.0) 于 2026-11-25T07:15:35Z 发布，
  非草稿、非预发布，GitHub Latest 指向本版。附注标签对象 `cfda92cbf5140d1841ffc65752823038b6c43325` 绑定
  main 合并提交 `381087c7`。
- 12 个资产（五平台 CLI、连接器 `.eext`、`skills.tar.gz`、`easyeda-agent-dsh.tgz`、安装脚本、`checksums.txt`、
  `test-evidence.zip`）。发布后重新下载全部资产，`shasum -c checksums.txt` 11 项通过：

| 资产 | SHA256 |
| --- | --- |
| easyeda_darwin_amd64 | 60198002ca9cd23275b04b59305bc04c0bf6f0e87b12f45d0e4ee329e2807f90 |
| easyeda_darwin_arm64 | 17be2b20019b102142b4d1bdab3f690aa95673e629c4b0e38228ee08b4a2693c |
| easyeda_linux_amd64 | f753a8b0ba4fdcd44769346a14a5e73a4a932daaec51a799f5cc853d8c42725e |
| easyeda_linux_arm64 | 48821a8eceec15c0a16ca118ca4aeca07db52e1fe9b7f79273cb0890ee64b3a0 |
| easyeda_windows_amd64.exe | a91c4eb543c0b889622fb03a9ee0524580c6bc706a3422baf3a4174e6e31b39b |
| easyeda-agent-connector.eext | a11a593fce19c60c9d0a8175eda5e1daec1ad611a3bd0e94cf2cf9807469bc6f |
| skills.tar.gz | dabb8e6beaf56e61fd4dcac9c1710db3d2dbdf8c7e8e2f50877b5a067d6f6a8f |
| easyeda-agent-dsh.tgz | c8ed6820c2b41988e674b02cb487b47b210c139ce01c5bcc26ff5264b647a6c6 |
| install.sh | ffa54418416b1f93351c8eb30917ebc05338a99e8a2199b130e7d2d4342ac21e |
| install.ps1 | 58b408b01e4fe90d8350e75c7d364e4f9252bac7fb8c0b7cd95635770b3d21bc |
| test-evidence.zip | 08ac769b61880b41fccd39774402f9966a63e700df7e39e81945ff41e46268c5 |

- 下载的 macOS arm64 CLI 报 `easyeda-agent v1.21.0`，动作目录含 `pcb.silk.delete`，`pcb silk-delete --help` 可用。
- 安装冒烟：用已校验的发布版 `install.sh`（`EASYEDA_INSTALL_SKILLS=none`）安装成功，报 `easyeda-agent v1.21.0 installed`。
  安装器在 `/usr/local/bin` 可写时会忽略 `HOME`，因此这次装到了本机的 `/usr/local/bin/easyeda`，不是隔离目录；
  隔离安装应显式设置 `EASYEDA_INSTALL_DIR`。升级路径（`easyeda update`）本轮未单独冒烟。

## 外部市场状态

- ClawHub：返回 `Published easyeda-agent@1.21.0`，回执 `k970g2pv96ffs5gb9yq466cd3x8jxqfc`；公开 latest 是否已切换未核对。
- SkillHub：`Publish skill to skillhub.cn` workflow 成功（`easyeda-agent v1.21.0`），前台审核状态未核对。
- DSH 插件市场：目录条目仍指向 v1.20.0 的 tarball（`docs/dsh-market.md`），本轮未更新外部条目。
- 立创连接器市场未在本轮更新；连接器运行时有变化，需要时另行人工投递。

## 现场验证范围

宿主为 EdaDesk DEV 0.2.1 中的 EasyEDA Pro 4.1.105，`ceshi` 工程，daemon `127.0.0.1:60833`，等价运行时
`1.20.1-dev.2`（dev `1516cc8c`）；验收范围由用户选择 schema 3 `cli-fixes`。

- 已通过：全量自动回归；内置 AI 按 ID 删除自己添加的自由丝印（`deleted:true`、`verified:true`，保存重载后源哈希复原）；
  宿主重开后的新回读；不存在 ID 的写前拒绝；v1.20.0 负对照；候选与现场运行时等价。
- 仅自动测试：锁定、非丝印层、属性 ID 的拒绝；多余字段拒绝；清单读不到时的 `partial`；Apply 不重试。
- PCB 文档没有自动检查点（设计如此）；完整设计 E2E 未执行，延期 follow-up。
- 详细用例、原始记录哈希与边界见 [v1.21.0 证据](evidence/v1.21.0/test-report.md)。
