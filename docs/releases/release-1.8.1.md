# v1.8.1 原理图离线布局补丁

2026-09-29 用户明确确认后，[v1.8.1 已正式发布](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.8.1)。
发布时间为 2026-09-29 01:11:58（UTC+8）；annotated tag 固定指向
`3283c05f8e42cdf8694bedf58330b71b3f3c667c`，该提交已推送到 `dev` 和 `main`。
GitHub Release 为非草稿、非预发布，10 项远端资产的名称、大小和 SHA256 全部与本地构建匹配。

本版提供显式的网络标签布局、保留实体线树的无边界布局，以及原 Lib 输入的局部布局入口。
网络标签模式需用户明确同意，不自动替代原 direct/attachment 绘图要求。
原始网表、引脚、NC、功能归属和测量姿态保留，短接、遮挡和有限预算检查继续执行。

[#273 离线复测](../reviews/2026-09-29-issue-273-net-labels.md)的 20 件降压、13 件升压
在标签模式分别约 0.04 / 0.02 秒完成；默认搜索耗尽保留原判，不关闭其原始验收缺口。
现场 Apply、保存重载和完整 E2E 未在本轮运行。连接器运行逻辑及协议未改，发布包版本
统一到 1.8.1，不要求为新增离线能力重新导入连接器。

发布说明以 [Changelog](../../extension/CHANGELOG.md) 的 1.8.1 条目为准；
构建、校验和正式发布按[发布流程](../release-workflow.md)执行。

本地准备验证：全仓 Go 测试通过；Skill 检查、release-check 和五平台 release-build
通过；发布脚本测试 122 项通过。release-smoke 的 15 项本机 CLI 检查、201 文件 Skill
包、连接器版本/UUID 与全部九项载荷校验和通过。正式构建的本机 CLI 用 #273 原始输入复测，降压
0.040 s / 933 次消耗，升压 0.024 s / 337 次消耗，均完成局部布局及无纸张 SVG，
原输入哈希匹配，报告算法版本为 v1.8.1。非本机平台只做交叉编译与包完整性核验。
构建资产保存在忽略目录 `dist/v1.8.1/`，复测记录在 `artifacts/issue-273-release-1.8.1/`。

发布提交的两次 [CI](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/36456109920)
[检查](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/36456104873)均通过。
ClawHub 返回 `easyeda-agent@1.8.1` 发布成功；
[SkillHub 自动发布](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/36456442483)成功提交
`eda-agent-connector@1.8.1`（skillId `173258`），平台未返回审核状态，不据此宣称已完成平台审核。
远端资产核验明细保存在本地忽略的 `artifacts/issue-273-release-1.8.1/remote-verification.json`。
