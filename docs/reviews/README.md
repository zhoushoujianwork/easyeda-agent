# 历史验证与调研证据

这里保存特定日期、版本和工程上的结果。旧命令、状态与通过记录均不作为当前操作指南；
现行入口见 [文档导航](../README.md)、[CLI Status](../cli-STATUS.md)、[架构](../architecture.md) 和
[功能清单](../FEATURES.md)。原始截图和回读附件保持与各报告相邻或通过相对链接访问。

| 证据范围 | 记录 |
|---|---|
| 2026-09-27 完整 CLI 用例 | [原理图离线布局未通过与十区可复现输入](2026-09-27-cli-layout-regression.md) |
| 2026-09-27 完整页回读中断 | [CLI 大响应截断修复与受保护恢复范围](2026-09-27-cli-response-truncation.md) |
| 2026-09-26 dev.11 铺铜定向复测 | [错值检测、清理恢复与成功分支缺口](2026-09-26-v1.7.1-dev11-pour-live.md)、[边界回读及 Apply 修复](2026-09-26-pour-readback-fix.md) |
| 2026-09-26 dev.9 基础 CLI 同包复跑 | [逐项执行与独立复核状态](2026-09-26-v1.7.1-dev9-basic-cli.md) |
| 2026-09-26 同文档连接互踢 | [dev.8 B00 停止](2026-09-26-v1.7.1-dev8-B00.md)、[dev.9 连接身份修复](2026-09-26-window-identity-fix.md) |
| 2026-09-26 页面改名失败传播 | [dev.8 定向验证通过，实际改名仍失败](2026-09-26-page-rename-fix.md) |
| 2026-09-26 后台重连与基础复跑 | [dev.4 阻断](2026-09-26-v1.7.1-dev4-basic-cli.md)、[重连修复](2026-09-26-cli-reconnect-fix.md)、[dev.7 基础复跑](2026-09-26-v1.7.1-dev7-basic-cli.md) |
| 2026-09-26 原理图删除定向修复 | [网表导出隔离、删除三态与 dev.4 现场回归通过](2026-09-26-sch-delete-fix.md)；不泛化为所有复合清理已验证 |
| 2026-09-26 高级 CLI 续测 | [复用工程与叠层 A、预检修复、A00 测量中断及恢复](2026-09-26-advanced-cli.md) |
| 2026-09-25 dev.21 基础 CLI 门禁通过 | [个人空间 B00–B10、定向警告、独立复核与清理](2026-09-25-v1.6.0-dev21-basic-cli.md) |
| 2026-06/07 官方 API 与市场覆盖快照 | [PCB API 探测](2026-06-pcb-api-discovery.md)、[市场覆盖](2026-07-marketplace-coverage.md) |
| 2026-07 真实需求探针与交互纠偏 | [ESP32 Mini 复测](2026-07-esp32mini-findings.md)、[里程碑走查](2026-07-milestone-walkthrough.md) |
| 2026-08 工具调用分布与 gate 现场验证 | [审计与验证记录](2026-08-sch-surface-audit.md) |
| 2026-09-23 局部改版与 daemon 恢复 | [代码修复及验证边界](2026-09-23-local-edit-reliability.md) |
| 2026-09-23 1.6.0 原理图专项 | [测试 prompt、场景和现场记录](2026-09-23-v1.6.0-schematic-acceptance.md) |
| 2026-09-24 dev.14 CLI 测试重分类 | [基础门禁缺口与高级能力观察](2026-09-24-v1.6.0-dev14-cli-gates.md) |
| 2026-09-24 dev.14 基础 CLI 现场复测 | [B00–B10 写读、重载、清理与门禁结论](2026-09-24-v1.6.0-dev14-basic-cli-live.md) |
| 2026-09-24 dev.15 Web 刷新与快速复测 | [刷新失败、诊断探针与基础故障复现](2026-09-24-v1.6.0-dev15-web-reload-retest.md) |
| 2026-09-24 dev.17 Codex 连接器轮换 | [自动卸载导入、刷新耗时与基础故障复测](2026-09-24-v1.6.0-dev17-connector-rotation.md) |
| 2026-09-25 dev.19/20 基础失败修复 | [官方调研、单项修复、现场回读与剩余门禁](2026-09-25-v1.6.0-basic-cli-repairs.md) |
| 2026-09-24 dev.18 基础 CLI 续测 | [B00–B10 自动续测总表与故障定位](2026-09-24-v1.6.0-dev18-basic-cli-continuation.md) |
| 2026-09-24 dev.18 B03 单项 | [工程容器查找：reload 后延长超时，两次完整查找通过](2026-09-24-v1.6.0-dev18-B03-project-container.md) |
| 2026-09-24 dev.18 B03 创建子项 | [个人工程创建通过；个人归属 UUID 与团队创建参数的差异](2026-09-24-v1.6.0-dev18-B03-project-create.md) |
| 2026-09-24 dev.18 B03 打开/导出 | [工程与页面身份核对、原生归档和 CRC 验证通过](2026-09-24-v1.6.0-dev18-B03-project-open-export.md) |
| 2026-09-24 dev.18 B01 单项 | [帮助、Skill 签名与零调度审计](2026-09-24-v1.6.0-dev18-B01-auto.md) |
| 2026-09-24 dev.18 B04 单项 | [页面创建、改名与保存重载](2026-09-24-v1.6.0-dev18-B04-auto.md) |
| 2026-09-24 dev.18 B05 单项 | [库/器件写读与编辑上下文失败](2026-09-24-v1.6.0-dev18-B05-auto.md) |
| 2026-09-24 dev.18 B06 单项 | [导线/标记/NC 与原生标签失败](2026-09-24-v1.6.0-dev18-B06-auto.md) |
| 2026-09-24 dev.18 B07 单项 | [PCB 器件增删改查](2026-09-24-v1.6.0-dev18-B07-auto.md) |
| 2026-09-24 dev.18 B08 单项 | [配置/几何回读与区域名称差异](2026-09-24-v1.6.0-dev18-B08-auto.md) |
| 2026-09-24 dev.18 B09 单项 | [持久化、属性身份映射和导出](2026-09-24-v1.6.0-dev18-B09-auto.md) |
| 2026-09-24 dev.18 B10 单项 | [保护负例、精确清理和保留状态](2026-09-24-v1.6.0-dev18-B10-auto.md) |
| 2026-08 回归与端到端缺陷 | [08-16](regression-2026-08-16.md)、[08-19 第一轮](e2e-report-esp32mini-2026-08-19.md)、[08-19 第二轮](e2e-report-esp32mini-round2-2026-08-19.md)、[08-25](e2e-round-2026-08-25-findings.md)、[08-26](regression-findings-2026-08-26.md) |
| 原理图与模块算法验证 | [算法验证](schematic-algorithm-validation.md)、[Lib 组合](schematic-composition-validation.md)、[LDO 布局](power-layout-validation.md) |
| PR、issue 与现场复核 | [09-08 分流](2026-09-08-pr-issue-triage.md)、[09-09 电阻证据](2026-09-09-issue-202-resistance-evidence.md)、[09-12 分类](2026-09-12-open-issue-classification.md) |

版本说明另存于 [1.4](../releases/release-1.4.md)、[1.5](../releases/release-1.5.md)、
[1.7](../releases/release-1.7.md)；
实际发布版本以 Git tag 与 GitHub Release 为准，草案不是发布证据。
