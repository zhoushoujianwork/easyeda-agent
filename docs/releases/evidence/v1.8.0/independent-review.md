# v1.8.0 CLI 修复发布范围独立复核

结论：**pass，仅限用户批准的 `cli-fixes` 发布范围与本次材料核查；无阻塞 finding。**
完整 ESP32 成品 E2E 仍为 `in-progress`，本报告不将其改为通过，也不要求重做已冻结的有限现场用例。

2026-09-27，独立复核员以工作树基准 `b2c05009904446b7c88a4b6d56356b946ae55de6` 核对下列材料。
全程仅离线读取及哈希计算，未访问 EDA、health、debug 或 GUI，未重跑现场、全量测试或构建。

- [x] 用户本轮明确批准 v1.8.0 CLI 修复发布，完整成品留待后续；manifest 的 schema 3、`cli-fixes`、`full-design-e2e`、`follow-up` 和用户决定中的精确版本/日期一致。AGENTS、发布流程和主报告同步此范围，不把它用作其他版本的默认豁免。
- [x] 两个发布检查器对源码与验收 ZIP 使用同一范围合同：仍要求精确版本、发布范围 result/independentReview 均 pass、三材料完整 SHA、用例覆盖与具体回读说明。schema 1/2 原规则保留；schema 3 的 E2E 只能为 in-progress/not-run，不能填写 pass。没有放宽运行时守卫。
- [x] 新包装测试同时覆盖源码与资产路径，包含决定缺失/版本错配、旧 schema 冒用范围、独立复核未完成、E2E 误签、N1 未执行及遗漏用例等拒绝。主 Agent 提供的 122 项发布脚本测试结果作为其验证记录引用；本复核没有重复执行或声称独立重跑。
- [x] 用例与主报告均有 M1/F1/F2/E1/L1/L2/N1/R1/E2E 九行：前八项仅签已明确的有限场景，E2E 为 in-progress。已核主表六份冻结独立报告的实际 SHA，以及 M1 原 51 件测量与 NT 修订报告；结论范围与引用一致，不重新代签其原始现场。
- [x] 原客户第一节仍为 996 字节，SHA256 `e62ebb05ecedb82e103d81b31da2bbf26aefe0669e580961c3aae87fef7d74d0`。旧完整报告在 `b2c0500` 的 18,580 字节原文完整包含于详细稿，SHA256 `3224bb561103dd4e8c368700d24341cdc6865dea022143f535cd7106bc107fd5`；旧失败、partial、not-run 和原生差分没有被覆盖。三份主材料的 24 个非本页本地文件链接均存在。
- [x] `cmd/`、`internal/`、`extension/src/` 相对已采用 `83a98b287accf4f2c00d936063fb1f396ea27e30` 的差分为零。extension/package/lock（含根 packages 项）和公开 Skill 版本均为 1.8.0，连接器 UUID 保持 `4dae27407c1d43be98e8e210d45fe587`。这是开发包现场证据的源码承接，不是正式 1.8.0 现场全集复测。
- [x] `dist/v1.8.0-preparation` 的九项软件资产大小/哈希与其准备清单一致，eext 版本/UUID 匹配；该冻结清单中的旧“等待范围选择”文字仅记录准备时状态。它没有正式验收包，且当前 Skill 已补知识，不能冒充最终发布包。正式构建须重新打包，再完成 smoke 及远端十一项资产核验。

核定材料 SHA256：

| 文件 | SHA256 |
|---|---|
| test-report.md | `f8f7f3617e6b3a375b475960cc69a76e92507de6c20bcb188c05f5dce101c129` |
| baseline.md | `503d22a1509c15fa93d73d4a444bbeddfcefdbce30a4ea5739e4c915299e3f95` |
| test-cases.md | `497073fb82d7902f4d98ec97334a99d1aaab3b26358977682d6b618150ccf79b` |

本次合同源码 SHA256：`release-check.py` 为 `db7b00473b4380ba2a6134546fa2f830945668cdc259cb31e4053867ba75ea2b`；
`release-smoke.py` 为 `7dacc617c3857e3e1f2b27fdbfd3ecc60681b76eb9737f5a2002a5c16dd3ca98`；
`test_release_packaging.py` 为 `07044067385e82301bed9129238f4c0b2e8e1616e04778ee3c3db17b926d6bc6`。

复核时 manifest 的 `independentReview` 为 `in-progress`；本结论允许主 Agent 将其更新为当前声明范围的 pass。
正式构建、tag/Release 和远端资产尚不属于本次已完成结论。完整设计仍缺用户布局确认、最终 GND 内电层、
整板布线、铜/PCB DRC 和最终成品保存重载导出；历史 dev.9 基础 9 pass / 2 fail、已接受 #55/#260/#261
及其他已知问题均保持原判。本报告不签完整 E2E、实板硬件或未来设计状态。
