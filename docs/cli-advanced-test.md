# 高级 CLI 业务验收

高级 CLI 用参数和真实工程数据完成选型、规划、求解、Compose/Apply、设计检查、PCB 布局布线
及从需求到成品的闭环。它回答“设计结果是否正确”，与
[基础 CLI 真实链路测试](cli-live-test.md)回答的“单个命令动作是否可靠”分开判定。

当前续测补充：dev.8 新开页面在 B00 发现 daemon 将同文档连接互踢。
[dev.9 连接身份修复](reviews/2026-09-26-window-identity-fix.md)已完成独立复核、安装对账和
定向现场恢复；[完整基础门禁](reviews/2026-09-26-v1.7.1-dev9-basic-cli.md)仍按新包逐项重跑，不沿用历史通过项。

## 进入条件

同一安装包、daemon、连接器和测试工程的基础用例 B00–B10 必须全部 `pass`。
基础层仍有 `fail`、`blocked` 或 `not-run` 时，不启动高级 CLI 的现场验收；
已有求解或 DRC 回包只作为历史观察，不能补签基础门禁，也不能称高级验收通过。

## 排期与范围

2026-09-25 用户决定：当前版本先发布已通过基础 CLI 的改动，高级 CLI A00–A06
留到下一版本验证。本轮高级结论为 `not-run`；历史失败和缺口继续保留。
下一版本从同版基础门禁检查开始，再按下述顺序验收。

2026-09-26 已按用户要求开始续测，见[本轮记录](reviews/2026-09-26-advanced-cli.md)。
本轮离线回归与目标预检已运行，用户已确认复用基础测试工程及叠层 A。现场器件测量期间
删除残留两次复现，A00 为 `blocked`、A01–A06 为 `not-run`；夹具已精确清理并保存重载。
不更改上轮发布材料中的 `not-run`，也不以 typed 恢复后的成功补签原失败。
删除故障已完成[定向修复回归](reviews/2026-09-26-sch-delete-fix.md)，现先重跑最终
`1.7.1-dev.4` 安装包的 B00–B10；[本批](reviews/2026-09-26-v1.7.1-dev4-basic-cli.md)
B00/B01 通过、B02 重连受阻。重连修复后，[dev.7 同包复跑](reviews/2026-09-26-v1.7.1-dev7-basic-cli.md)
B00–B03 通过、B04 改名失败。后续 [dev.8](reviews/2026-09-26-page-rename-fix.md) 已验证
失败传播修复并恢复测试基线，但宿主改名仍失败；基础门禁未放行，高级现场用例仍受阻。

通过基础门禁后，按[高级用例与前置条件](cli-advanced-test-detail.md)检查：
原理图数据驱动选型与布局、保护 Apply、严格质量门；PCB 同步、规则、布局、用户确认、
布线与铜、保存重载和独立复核。完整端到端只把
[`esp32MiniRequire.md` 第一节](../esp32MiniRequire.md)的客户原始需求交给执行 Agent，
按[现行验收标准](e2e-automation-acceptance.md)及公开 Skill 的 S0–S6/P0–P10 流程执行。

高级结论按每个设计事实给出 `pass`、`fail`、`blocked` 或 `not-run`，保留输入哈希、
候选报告、Apply journal、DRC 和 save→reload→fresh readback。离线候选、单页 gate
或截图均不等于完整设计通过。
