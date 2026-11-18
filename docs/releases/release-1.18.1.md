# v1.18.1 发布准备（ALCY-7，未发布）

本版本按项目已授权的自动评审、合并与发布流程准备；不是要求重复人工批准。
目标沿用 `main` 上的 `make release VERSION=v1.18.1` 和 GitHub Release 分发，
正式 Release 发布后由既有工作流提交 SkillHub，ClawHub 沿用 best-effort 提交。
未涉及腾讯云主机服务或 Desk 安装镜像。

## 来源与范围

- 开发 PR：[#311](https://github.com/zhoushoujianwork/easyeda-agent/pull/311)，
  独立评审 PASS 的 head 为 `cde909b3e46d25923786e8fe9e6c176565c86ea5`，五项 CI 通过。
- 已合入 `dev`，merge SHA `0240566fbbc9b9f528e98fb76973f54e5f06097c`；合并不代表正式发布。
- 正式候选从 `main` 基线 `af76cd7ea73cd9789a68782cb4ad18d921fd0074` 移植
  `a6026583`、`8727b9ae`、`7033a6f0`、`0b5595dc`、`cde909b3` 五个提交。
  产品与 Skill 差异的 stable patch-id 和原 PR 均为 `8b8fbfde3950113dd9f95b43d6d3720b4ee43799`。
- 仅新增离线可布性检查与相应 Skill 要求，不带入原理图源码恢复、自动检查点或其他 dev 改动。
  连接器运行时无变化，按既定规则递增 patch 至 1.18.1；各组件版本元数据保持一致。

## 验证及剩余工作

本候选的 `go test ./internal/app -run 'Routability|Escape' -race -count=1`、
`go vet ./internal/app`、`make agent-check skill-check release-check VERSION=v1.18.1`
及 `make release-script-test` 已通过；发布脚本回归共 136 项，Skill 包含 192 个文件。

发布 PR 须对自己的完整 head 完成独立复核与必要检查，再合入 main。
随后从固定干净提交执行 `make release-check release-build VERSION=v1.18.1`，
核对五平台二进制 build info、资产 SHA256、安装/升级冒烟，再执行标准 `make release`。
发布后核对附注标签、非草稿 Release、真实下载与安装，记录实际结果；当前尚未创建标签或资产。

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
