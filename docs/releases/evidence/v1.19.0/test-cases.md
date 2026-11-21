# v1.19.0 定向修复用例

本表复用 schema 3 的用例 ID，含义按本版重新定义，不等同于历史版本同 ID 的整板流程。现场用例只在一次性测试工程 `ceshi` 中执行。完整设计 E2E 按本次用户决定延期。

| ID | 用例 | 输入、步骤和判据 |
| --- | --- | --- |
| M1 | 全量自动回归 | 在源码基准上运行 `go test ./...`、`go vet ./...`、连接器 `npm test` 与 `tsc --noEmit`、`make agent-check skill-check release-script-test`；不得有失败，跳过项如实保留。 |
| F1 | 整页原理图链路现场 | 在 P2 上由 Desk 内置 AI 执行 inspect → plan（必要时先 bind）→ apply：匿名网 R101:2—R102:1 只用导线、R101:1 NC、图签字段在纸内、H1 为属性完整的器件。判据：受保护队列 journal 全部 ok（含 preflight-lib、verify-all-pins-nets-nc、electrical-check、strict-schematic-gate、save-composition），保存重载后回读符合测试意图，严格门禁（`--drc-page`）通过。 |
| F2 | keepProperties 替换现场 | 带 `keepProperties:true` 依次把 H1 换成 TS-102-T-J、TS-102-G-L-2。判据：每次替换都保留 `cmp-H1`；LCSC Part Name、Datasheet 为目标器件自己的值；写后几何失败时，Desk 报出 CLI 的几何问题，而不是"结束状态未知"。 |
| E1 | v1.18.2 负对照 | 用 v1.18.2 标签构建的 CLI 对同一页、同一输入只读运行 `sch gate --strict` 与 `sch compose`（F1 最终执行的 composition）。判据：旧版严格门禁被 P1 的 DRC 告警挡住，旧版 compose 拒绝匿名网；新版在同一输入上门禁通过，并生成与现场执行队列一致的 playbook。 |
| L1 | 检查点与步骤级回退 | 每次写原理图页前自动记录检查点（现场 stderr 可见）；步骤级回退引用 edadesk#17 在 2026-11-18 的 DEV 现场验证（Auto 下 `checkpoint:restore`、已回退的 dry-run、重复回退不写入）。判据：引用的验证记录存在且范围相符；本轮不新增回退写入。 |
| N1 | 写前拒绝 | 只读运行 `sch group create --check`：不带 `--replace-overlapping` 时，对已在其他组的成员拒绝；带该参数时报告将释放的组，且不保存。判据：前后分组表不变，拒绝发生在写入前；现场中途失败的队列由 Desk 按 journal 如实报告"失败前没有保存"。 |
| R1 | 开发候选制品 | `build-local-snapshot.py` 从固定提交构建 dev.6–dev.12，经升级弹窗安装。判据：`local-dev.json` 的 `sourceRevision` 与提交一致且 `sourceDirty=false`；health 显示 CLI、daemon、连接器均为 `1.18.3-dev.12`；dev.12 的源码即本版基准。 |
| E2E | 完整设计与现场验收 | 未执行；后续按最小点灯板原始需求完成完整设计、DRC、保存重载与新回读。不能用本版定向用例或自动测试补签通过。 |

复现入口：上述命令，以及本地忽略的 `artifacts/v1.19.0-logs/` 下按用例命名的原始记录（不入库，哈希记于报告）。正式 v1.19.0 制品另用 `make release-check release-build VERSION=v1.19.0` 核验，发布后还须核验真实 GitHub 资产的下载与安装。
