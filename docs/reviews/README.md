# 历史验证与调研证据

这里保存特定日期、版本和工程上的结果。旧命令、状态与通过记录均不作为当前操作指南；
现行入口见 [文档导航](../README.md)、[架构](../architecture.md) 和
[功能清单](../FEATURES.md)。原始截图和回读附件保持与各报告相邻或通过相对链接访问。

| 证据范围 | 记录 |
|---|---|
| 2026-06/07 官方 API 与市场覆盖快照 | [PCB API 探测](2026-06-pcb-api-discovery.md)、[市场覆盖](2026-07-marketplace-coverage.md) |
| 2026-07 真实需求探针与交互纠偏 | [ESP32 Mini 复测](2026-07-esp32mini-findings.md)、[里程碑走查](2026-07-milestone-walkthrough.md) |
| 2026-08 工具调用分布与 gate 现场验证 | [审计与验证记录](2026-08-sch-surface-audit.md) |
| 2026-09-23 局部改版与 daemon 恢复 | [代码修复及验证边界](2026-09-23-local-edit-reliability.md) |
| 2026-09-23 1.6.0 原理图专项 | [测试 prompt、场景和现场记录](2026-09-23-v1.6.0-schematic-acceptance.md) |
| 2026-09-24 dev.14 CLI 测试重分类 | [基础门禁缺口与高级能力观察](2026-09-24-v1.6.0-dev14-cli-gates.md) |
| 2026-09-24 dev.14 基础 CLI 现场复测 | [B00–B10 写读、重载、清理与门禁结论](2026-09-24-v1.6.0-dev14-basic-cli-live.md) |
| 2026-09-24 dev.15 Web 刷新与快速复测 | [刷新失败、诊断探针与基础故障复现](2026-09-24-v1.6.0-dev15-web-reload-retest.md) |
| 2026-08 回归与端到端缺陷 | [08-16](regression-2026-08-16.md)、[08-19 第一轮](e2e-report-esp32mini-2026-08-19.md)、[08-19 第二轮](e2e-report-esp32mini-round2-2026-08-19.md)、[08-25](e2e-round-2026-08-25-findings.md)、[08-26](regression-findings-2026-08-26.md) |
| 原理图与模块算法验证 | [算法验证](schematic-algorithm-validation.md)、[Lib 组合](schematic-composition-validation.md)、[LDO 布局](power-layout-validation.md) |
| PR、issue 与现场复核 | [09-08 分流](2026-09-08-pr-issue-triage.md)、[09-09 电阻证据](2026-09-09-issue-202-resistance-evidence.md)、[09-12 分类](2026-09-12-open-issue-classification.md) |

版本说明另存于 [1.4](../releases/release-1.4.md)、[1.5](../releases/release-1.5.md)；
实际发布版本以 Git tag 与 GitHub Release 为准，草案不是发布证据。
