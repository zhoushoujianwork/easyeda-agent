# v1.22.0 已发布

本版本经用户于 2026-11-28 批准具体版本（v1.22.0）后，从固定干净的 `main` 提交执行
`make release VERSION=v1.22.0` 发布。现场验收使用 EdaDesk DEV 的独立运行时（见证据），没有重启或替换
用户正在运行的正式版 daemon（60832）或连接器。

## 来源与范围

- 开发集成分支 `dev` 的 `8b2bd82f`（第二轮发布准备 PR [#373](https://github.com/zhoushoujianwork/easyeda-agent/pull/373)
  合入后，源码与验收候选 `8594aa0e` 相同、只多验收材料与 Changelog）冻结为 `release/v1.22.0`，经发布 PR
  [#374](https://github.com/zhoushoujianwork/easyeda-agent/pull/374) 合入 `main`，合并提交
  `d5056dbc267fd5a4b6387d10d8fb68c11cb58209`（树与 `8b2bd82f` 相同）。
- 相对 v1.21.0 为 dev 上的 61 个提交，变更见 `extension/CHANGELOG.md` 的 `[1.22.0]`：`easyeda selection`（#360）、
  多单元器件位号与稳定绑定（#362、#363、#365）、布线流水线（`pcb fanout`、`route-diagnose`、`autoroute --net`）、
  `pcb layout simulate` 与一键布局（含射频检查、导出顺序无关，#368、#371、#372）、auto-place 卫星 v2 与射频匹配链、
  检查与 DRC 口径、`pcb silk-list`、`skill status`、Skill 写入确认改由宿主审批、playbook 断言契约（#370）。
- 连接器运行时有变化（`selection.get`、autoroute `ignoreNets`、锁定铜提示），用户需更新连接器，按中版本发布。

## 合入与检查

- #369：版本统一为 1.22.0、Changelog 与第一轮验收材料（候选 `51e483c8` / 运行时 `1.22.0-dev.7`）。
- #373：Changelog 补入 #368、#370–#372，第二轮验收（源码候选 `8594aa0e` / 运行时 `1.22.0-dev.10`）。
- #373、#374 的 CI 全部通过。
- 本机（macOS arm64）候选 `8594aa0e`：`go vet`、`go test`（6454 个测试 pass 含子测试 / 0 失败 / 1 平台限定 skip）、
  连接器 1432 项、`tsc`、`make agent-check skill-check release-script-test` 通过；`8b2bd82f` 上 `make release-check` 与
  `make release-build VERSION=v1.22.0` 通过。
- **独立复核**：两轮都由独立 Agent 只读复核证据与原始记录。第二轮首次 `fail`（L1 引用了未留存日志中的封装 id），
  修正后复核 `pass`。

## 正式发布及验证结果

- [v1.22.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.22.0) 于 2026-11-28T10:32:30Z 发布，
  非草稿、非预发布，GitHub Latest 指向本版。附注标签对象 `322416dbc070cebe0e8ee5e243f813751fa4ada2` 绑定
  main 合并提交 `d5056dbc`。
- 12 个资产（五平台 CLI、连接器 `.eext`、`skills.tar.gz`、`easyeda-agent-dsh.tgz`、安装脚本、`checksums.txt`、
  `test-evidence.zip`）。发布后重新下载全部资产，`shasum -c checksums.txt` 11 项通过，远端 `checksums.txt` 与本地构建一致：

| 资产 | SHA256 |
| --- | --- |
| easyeda_darwin_amd64 | 7ad7a9d565c57996b8eb02a18d501f471bae0cba82ec9f224ee3a78ba66ac481 |
| easyeda_darwin_arm64 | 9a6b96e4cc6e284a49d8185103430dad15e4216cdf36dffc1ce163df08a4b06c |
| easyeda_linux_amd64 | 60749362d52a3ff3a9facfaa5911d4de8b7d9a0db20a79ef0f1115a9494da772 |
| easyeda_linux_arm64 | 82ccd968d0f086a4143706362955551974a46a0daea4bca73ae683b3164ca2f5 |
| easyeda_windows_amd64.exe | 23b8dde1c752c5029d3f86f3940641d2e72939c6f26a3fb824f60076506c97ff |
| easyeda-agent-connector.eext | 0607ea70c8c441bc837d96c4008e037a09564fd03585b6279ea5cb13ccb20271 |
| skills.tar.gz | e9033be52e998f29e81f92eaf517ba4a0a19607bd7fa8a6dab66e9301a8e8ccb |
| easyeda-agent-dsh.tgz | f3ad215a6b89260b5b93e36cb5c9abfadee4ad96e1037a1d4d03b9a387b8b6ad |
| install.sh | ffa54418416b1f93351c8eb30917ebc05338a99e8a2199b130e7d2d4342ac21e |
| install.ps1 | 58b408b01e4fe90d8350e75c7d364e4f9252bac7fb8c0b7cd95635770b3d21bc |
| test-evidence.zip | 4893edc88cd831ecbee0da062a8913532b900c795f570ebb7a55122487a94d58 |

- 下载的 macOS arm64 CLI 报 `easyeda-agent v1.22.0`；本机 `easyeda update --check` 识别最新版为 v1.22.0。
- 安装冒烟与升级路径（`install.sh`、`easyeda update`）本轮未执行：本机全局 CLI 是开发构建、全局 Skill 为 1.17.0，
  正式版 EdaDesk 内置 v1.21.0，未擅自替换。

## 外部市场状态

- ClawHub：返回 `Published easyeda-agent@1.22.0`，回执 `k979qqe7wafj0vj8fdvgxw78dh8k2h3x`；公开 latest 是否已切换未核对。
- SkillHub：`Publish skill to skillhub.cn` workflow 成功（`easyeda-agent v1.22.0`），前台审核状态未核对。
- DSH 插件市场与立创连接器市场本轮未更新；连接器运行时有变化，需要时另行人工投递。
- EdaDesk 正式版仍内置 v1.21.0，需另行发布 EdaDesk 才会带上本版。

## 现场验证范围

宿主为 EdaDesk DEV 0.2.3 中的 EasyEDA Pro 4.1.105，`ceshi` 工程，daemon `127.0.0.1:60833`；验收范围由用户选择
schema 3 `cli-fixes`。

- 已通过（第二轮，`1.22.0-dev.10`）：全量自动回归；多单元器件绑定、保存重载回读与写前拒绝；playbook 断言预检与运行时
  路径提示及 v1.21.0 负对照；一键布局（无改进路径）；`auto-place --dry-run`；清理后 PCB 语义哈希回到测试前。
- 沿用第一轮（`1.22.0-dev.7`，相关代码此后未改）：`autoroute --net` 只布指定网络、`selection` 读取 PCB 真实选区、#365 的 v1.21.0 负对照。
- 仅自动测试：原理图页选区、`page drift`、跨页单元拒绝、越界新铜失败、一键布局改进路径、射频规则、导出顺序无关、auto-place 写入等。
- 完整设计 E2E 未执行，延期 follow-up。详细用例、原始记录哈希与边界见 [v1.22.0 证据](evidence/v1.22.0/test-report.md)。
