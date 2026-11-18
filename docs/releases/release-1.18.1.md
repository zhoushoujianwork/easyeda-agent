# v1.18.1 已发布（ALCY-7）

本版本按项目已授权的自动评审、合并与发布流程完成 GitHub Release 分发，
使用固定干净 main 提交和 `make release VERSION=v1.18.1`。
既有工作流已提交 SkillHub，ClawHub 也返回提交回执；公开市场版本仍需分别核对。
未涉及腾讯云主机服务或 Desk 安装镜像。

## 来源与范围

- 开发 PR：[#311](https://github.com/zhoushoujianwork/easyeda-agent/pull/311)，
  独立评审 PASS 的 head 为 `cde909b3e46d25923786e8fe9e6c176565c86ea5`，五项 CI 通过。
- 已合入 `dev`，merge SHA `0240566fbbc9b9f528e98fb76973f54e5f06097c`；合并不代表正式发布。
- 正式发布 [PR #313](https://github.com/zhoushoujianwork/easyeda-agent/pull/313) 的独立 PASS head 为
  `b730a12bb4580b9dd29f2ad3adde97ef38a37264`，五项 CI 通过，合入 main 的 merge SHA 为
  `dff86156cbde28e65d83dfe893f1b53f87d967b6`；合并提交与评审 head 的文件树一致。
- 正式候选从 `main` 基线 `af76cd7ea73cd9789a68782cb4ad18d921fd0074` 移植
  `a6026583`、`8727b9ae`、`7033a6f0`、`0b5595dc`、`cde909b3` 五个提交。
  产品与 Skill 差异的 stable patch-id 和原 PR 均为 `8b8fbfde3950113dd9f95b43d6d3720b4ee43799`。
- 仅新增离线可布性检查与相应 Skill 要求，不带入原理图源码恢复、自动检查点或其他 dev 改动。
  连接器运行时无变化，按既定规则递增 patch 至 1.18.1；各组件版本元数据保持一致。

## 正式发布及验证结果

本候选的 `go test ./internal/app -run 'Routability|Escape' -race -count=1`、
`go vet ./internal/app`、`make agent-check skill-check release-check VERSION=v1.18.1`
及 `make release-script-test` 已通过；发布脚本回归共 136 项，Skill 包含 192 个文件。

- [v1.18.1](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.18.1)
  于 2026-11-18 16:28:05 UTC 发布，非草稿、非预发布，GitHub Latest 已指向本版。
  附注标签对象 `7c6697c33a9eca3679987aad9fea060620b44e62` 绑定上述 main merge SHA；
  [main CI](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/44346642275) 五项任务全部通过。
- 从该提交的独立干净 clone 完成 `release-check`、`release-build`、本地安装/升级预检，
  再由标准 `make release` 重建并上传。五个平台二进制 build info 均绑定该提交、`vcs.modified=false`。
  五平台为 macOS amd64/arm64、Linux amd64/arm64、Windows amd64；仅本机 macOS arm64 发布二进制完成运行验证，
  其他平台的交叉编译及 CI 原生安装表面检查不冒充这些发布文件的实机运行验证。
- 发布后匿名下载全部十个附件（九项软件资产与 `checksums.txt`），文件名、大小、SHA256 与最终本地构建逐项一致；
  [校验清单](https://github.com/zhoushoujianwork/easyeda-agent/releases/download/v1.18.1/checksums.txt) 保留完整资产哈希。
  macOS arm64 为 21,039,330 字节，SHA256 `10924738df4b4b1b49593620b3bfccd8d06c71370d7c3ff36cee0e1d709b82dd`。
  重建连接器归档的容器元数据变化，解压后的全部 payload 与预检候选逐字节一致。
- 本地候选和实际下载包均通过 `release-smoke`，各含十五项本机 CLI 冒烟；新包的 `pcb routability --help` 入口有效。
  隔离目录中使用原样安装脚本完成真实 GitHub 首次安装、v1.18.0 基线及升级到 v1.18.1：CLI 实字节、
  192 个 Skill 文件和 `.version` 均匹配，旧文件哨兵被移除。发布前候选同样完成上述安装路径，
  其下载通道使用仅映射精确 Release URL 的本地替身，不与正式联网验证混淆。
- 官方 v1.18.0 CLI 的隔离副本执行 `update --cli-only --version 1.18.1 --restart-daemon=false`，
  完成真实下载、SHA256 校验和替换，新二进制与公开附件逐字节一致。没有重启或替换用户现有 daemon、CLI 或连接器。

## 外部市场状态

- SkillHub [自动发布任务](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/44347574697)
  成功，版本 1.18.1，回执 `skillId=163177 status=None`；发布后通过官方 CLI 在隔离目录下载的
  `easyeda-agent-connector` 社区包仍为 1.18.0，不能把提交成功记为前台已经更新。
- ClawHub 返回 `Published easyeda-agent@1.18.1`，回执 `k97a7x6axx7tsbnwp7cv7w49658jfz06`；
  随后公开查询 1.18.1 返回 `Version not found`，latest 仍为 1.18.0。只记已提交，不声称公开可安装。
  立创连接器市场未在本轮更新。

## 未完成的设计复验

原 PR 的离线用例覆盖 0.4 mm 间距夹具的阻挡、移除阻挡、只读及不完整输入拒绝。
真实含 0.4 mm 间距器件的四层板仍缺完整几何、实测规则的新 dump，正式复验未执行；
四层既有过孔缺少孔型/起止层回读证据。网格采样与逐焊盘局部检查不分配共享通道容量。
这些限制随发布披露，不把离线结果写成真实板通过或完整 E2E 通过。

## 当前正式版与回退

发布前 GitHub Latest 为 [v1.18.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.18.0)，
标签绑定 `af76cd7ea73cd9789a68782cb4ad18d921fd0074`；保留该标签、Release 和全部历史资产。
本次不替换用户正在运行的 CLI、daemon 或连接器。
若新版本分发或安装验证失败，停止扩大发布，保留失败证据；用户可使用旧版固定资产和
`EASYEDA_VERSION=v1.18.0 bash ./install.sh` 回退 CLI/Skill，连接器仍可保留同 minor 的旧运行时。
不删除已发布版本，不修改历史证据，不以上传成功替代下载验证。
