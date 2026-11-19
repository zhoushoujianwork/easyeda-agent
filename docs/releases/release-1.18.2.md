# v1.18.2 已发布

本版本经用户于 2026-11-19 批准具体版本后，从固定干净的 main 提交执行 `make release VERSION=v1.18.2` 发布。
没有重启或替换用户正在运行的 CLI、daemon 或连接器，未涉及 Desk 安装镜像。

## 来源与范围

- 开发提交：`dev` 上的 `7c428c4da8cec820b0feef8761c9120975e66459`，
  新增 `pcb plan-apply`、其测试和公开 Skill 布线参考中的对应一节。
- 正式候选从 `main` 基线`7ad3d7d13447ec1699b3d6385ab370c105585092`（v1.18.1 之后）用 `git cherry-pick -x` 移植这一个提交，无冲突。
- 不带入 dev 的原理图源码恢复、自动检查点、`rip-up` 锁定走线修复或其他开发提交。
  连接器运行时无变化，按既定规则递增 patch 至 1.18.2；各组件版本元数据保持一致。
- 关联 Issue：#312（按 ID 删线后残留，`track-delete` 自身未改，仍开着）、#310（批量写入，未动）、#307。

## 合入与检查

- 发布 [PR #315](https://github.com/zhoushoujianwork/easyeda-agent/pull/315) 的 head 为
  `8048aea02b484e01fbc060413d6f44488ff05f05`，合入 main 的 merge SHA 为
  `e3ede55fd38d11412e5a5389eda19792c8e17e27`；合并提交与 head 的文件树一致。
- PR 的五项 CI 最终全部通过。其中 Windows 的"原生安装表面检查"第一次失败：DSH 打包用例里的 `npm install`
  超过 60 秒时限；该用例与本次改动无关（改动未触及测试脚本、连接器源码或 CI 配置），仅重跑该失败任务后通过。
  [main CI](https://github.com/zhoushoujianwork/easyeda-agent/actions) 在合并提交上五项通过。
- 本机（macOS arm64）准备阶段：`go test ./internal/app -count=1`、`-run PlanApply -race`、`go vet ./internal/app`、
  `make agent-check skill-check release-check VERSION=v1.18.2`、`make release-script-test`（136 项）、
  `make release-build`、`make release-smoke` 均通过。
- **未做独立评审**：本版只有开发 Agent 自己的检查与 CI，没有独立复核者的 PASS。

## 正式发布及验证结果

- [v1.18.2](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.18.2) 于 2026-11-19 02:30:12 UTC 发布，
  非草稿、非预发布，GitHub Latest 已指向本版。附注标签对象 `6f52fe4680db9406dfd5d115d26a5bc269f3d34d`
  绑定上述 main merge SHA。
- 发布后匿名下载全部十个附件（九项软件资产与 `checksums.txt`）：`checksums.txt` 逐项校验通过，
  每个文件与最终本地构建逐字节一致。macOS arm64 为 21,057,954 字节，
  SHA256 `a5555856c4d2ba9e834e764145cb4e37ce4fac3f77570f8dc129b1086e4bb0f1`。
- 下载包通过 `release-smoke`；`pcb plan-apply --help` 入口有效。仅本机 macOS arm64 发布二进制做了运行验证，
  其他平台为交叉编译及 CI 的原生安装表面检查，不冒充实机运行验证。
- 隔离目录（独立 HOME 与安装目录，不装 Skill）用已发布的安装脚本完成：全新安装 v1.18.2，CLI 与公开附件逐字节一致；
  先装 v1.18.1 再执行 `update --cli-only --version 1.18.2 --restart-daemon=false`，完成真实下载、校验和替换，
  新二进制与公开附件逐字节一致。Skill 的安装与升级路径本次没有单独验证。

## 外部市场状态

- SkillHub 自动发布任务成功（版本 1.18.2）；前台是否已更新未核对。
- ClawHub 返回 `Published easyeda-agent@1.18.2`，回执 `k9798602yh7bcq4j9nykfxnnsn8jgvje`；
  随后公开查询 latest 仍为 1.18.1。只记已提交，不声称公开可安装。
- 立创连接器市场未更新；连接器运行时无变化，无需更新。

## 现场验证范围

宿主为 EdaDesk 正式版，端点 127.0.0.1:60832，daemon v1.18.0，连接器 1.18.0，嘉立创EDA 专业版 Web 4.1.105；
CLI 为含该提交的开发构建；对象是一块 4 层板的一次性测试副本。

- `--dry-run` 对已与方案一致的板报告一致，不写入。
- `--project-uuid` 与窗口工程不符时拒绝，并说明实际工程。
- 被替换网络的铜被锁定时拒绝，提示解锁命令。
- 解锁后替换 42 个网络（删 553 条线、58 个过孔，写 557 条线、63 个过孔），一轮对账一致，其他网络未变；
  `pour-rebuild` → `save` → `doc reload` 后再预演仍一致；原生 DRC 无新增违例。
- 故意留下一条"分支删除后合并出来"的残线：预演报出该网络该层线长期望 105 mil、实际 205 mil；执行后一轮清除。
- 由外部串联流程调用：从 38 条未连接（16 个网络）的状态出发，写回 30 个网络后原生 DRC 未连接为 0。

## 未覆盖

- 命令内部"删除后再回读清扫"的分支现场未触发，仅有单测（模拟的合并行为）。
- 圆弧走线只有拒绝路径。写入仍是逐个对象调用，没有提速。
- 现场验证用的是开发构建而非发布二进制；发布二进制只做了本机冒烟与安装升级验证，没有连真实 EDA 重跑。
- 完整设计 E2E、B00–B10 / A00–A06 未执行，按小版本范围保持原状态。

## 当前正式版与回退

发布前 GitHub Latest 为 [v1.18.1](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.18.1)，保留该标签、Release 和全部历史资产。
如需回退，可用 `EASYEDA_VERSION=v1.18.1 bash ./install.sh`；连接器同 minor，无需更换。
不删除已发布版本，不修改历史证据。
