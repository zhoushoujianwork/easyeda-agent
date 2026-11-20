# v1.19.0 定向修复验收基准

## 本版用户范围决定

2026-11-20，用户先批准发布 v1.18.3。随后得知以下事实：v1.18.2 之后连接器运行时有改动（`keepProperties` 替换、`doc source restore`、`pcb rip-up`），按版本策略必须升中版本；完整点灯板 E2E 尚未执行。用户在"以 v1.19.0 有限 CLI 修复范围发布"和"先跑完整 E2E"两个选项中，选择前者（"Yes, v1.19.0 limited"）。

本版因此采用 schema 3 `cli-fixes` 有限范围，包括：
- 全量自动回归；
- 整页原理图链路与 `keepProperties` 替换的 DEV 现场验收；
- 用 v1.18.2 做的负对照；
- 引用 edadesk#17 已有的步骤级回退现场验证。

完整设计 E2E 延期至 follow-up。此决定只绑定 v1.19.0。报告中的 `pass` 只表示本版明确定义的用例通过，不表示电路、制造、硬件或完整设计验收通过。

## 原始需求与起点

完整设计的默认原始需求仍为 `docs/test-case-esp32-blink.md` 第一节，本轮未执行该流程。现场没有输入预制 BOM、UUID、网表或坐标答案；用例中的电路目标（VCC/LED_A/GND、匿名网 R101:2—R102:1、R101:1 NC）是 edadesk#9 复验的测试意图，不是设计答案。

现场起点：一次性测试工程 `ceshi`，原理图页 P2（uuid `8fb55bd61d0986a9`，现场期间改名为“LED 指示”）。页上有前几轮测试留下的器件 H1、R10、U4、R101、R102。P1 为另一张未完成的页，有大量悬空引脚，本轮未改动。

## 源码与环境

- 软件源码基准：`6d4f1947a48fc6ded68a354fc46f2d5b083cdb47`（origin/dev）。正式元数据准备只修改版本号、Changelog、Skill 版本与本证据目录。
- 现场候选：`scripts/build-local-snapshot.py` 从固定提交构建的 `1.18.3-dev.6` 至 `1.18.3-dev.12`，每个都是干净提交（`sourceDirty=false`），经 edadesk Dev 内人工确认的升级弹窗安装。最终 `dev.12` 的 `sourceRevision` 即本版基准 `6d4f1947`，bundle SHA256 `2a23e46309e71a630f58f60861f628a9c60900ac2b7eefe6639a62d9807c2786`。
- 宿主：edadesk DEV（`7fb5bbf`、`149c437` 等 dev 构建）中的 EasyEDA Pro。daemon 端点为 127.0.0.1:60833（DEV），与正式 60832 隔离。设计写入由 Desk 内置 Codex（Auto 模式）经 edadesk 受保护流程执行。
- 负对照：从 `v1.18.2` 标签源码构建的 CLI（`easyeda-agent v1.18.2`）。
- macOS arm64；Go、Node、Python 版本见自动回归日志。

## 通过阈值与不豁免项

- 自动回归不得有失败，平台限定的跳过如实保留。
- 整页链路：受保护队列的 journal 全部步骤 ok，包括 `preflight-lib-*`、`verify-all-pins-nets-nc`、`electrical-check`、`strict-schematic-gate`、`save-composition`、`verify-saved-instance-preservation`；保存重载后的新回读中，四个网络、匿名网和 NC 必须符合测试意图。
- 替换：稳定 ID 保留，字段归属于新器件。
- 负对照：旧版必须在同一输入上复现被修复的拒绝或误判。

正式发布仍须完成 `release-check`、`release-build`、资产校验、安装冒烟和远端资产核验。本范围决定不允许伪造回读、放宽判据或删除失败记录。
