# v1.20.0 定向修复用例

本表复用 schema 3 的用例 ID，含义按本版重新定义，不等同于历史版本同 ID 的整板流程。现场用例只在
一次性测试工程 `ceshi` 中执行；完整设计 E2E 按本版用户决定延期。

| ID | 用例 | 输入、步骤和判据 |
| --- | --- | --- |
| M1 | 全量自动回归 | 在候选提交上运行 `go vet ./...`、`go test ./...`、连接器 `npm test`、`tsc --noEmit`、`make agent-check skill-check release-script-test`；不得有失败，跳过项如实保留。 |
| F1 | 页面改名与宿主诊断现场 | 在 `ceshi` 中让 PCB1 或另一页处于前台，只读外再执行 `sch page-rename --page 6abf3fbf13dc52f0 --name P1-v120`：宿主未加载兄弟页时应先加载再重试并成功；随后改回 `P1`。另把该页改成已存在的 `LED 指示`，判据：退出码非 0、报 `EDA_CALL_FAILED` 且带宿主状态（`hostStates`），页名不变；成功改名只需 `verified:true` 与 `doc ls` 回读，不要求每个成功日志都带 `hostStates`。 |
| F2 | PCB 几何与检查现场 | 对 `PCB1` 只读运行 `pcb dump --include-copper`、`pcb check`、`pcb drc` 与 `sch check --json`。判据：槽孔 `holeRotation` 以弧度给出且为 π/2 的整数倍，孔偏移在 ±0.001 噪声内仍可测；`rules.copperToEdgeMil` 来自原生 Safe Spacing；`sch check` 报告含图签越界/压线字段；所有命令不写入。 |
| E1 | v1.19.0 负对照 | 用 Release `v1.19.0` 的 CLI 对同一 `PCB1` 只读运行 `pcb dump` 与 `pcb check`。判据：旧版只有 `availability` 静态占位、没有孔偏移/旋转测量值（因此槽孔不可测），新版给出测量值；两版检查摘要的差异必须如实记录（本板无近边铜，不得因此宣称中心线改动已现场验证）。 |
| L1 | 写入检查点 | 现场执行页面改名与只读检查时，stderr 应有 `checkpoint …` 记录；判据：写前出现检查点、无新增回退写入，回退能力引用 edadesk#17 的既有现场验证（2026-11-18 DEV），本轮不重放。 |
| N1 | 写前拒绝 | 在 `PCB1` 上执行 `pcb delete --ids nonexistent-v120-id`。判据：退出码非 0（日志记录 `exit=1`）、报 `PRECONDITION_REFUSED`，删除后重跑 dump 仍为 33 个器件，未发生删除；部分失效路径只由自动测试覆盖并如实标注。 |
| R1 | 开发候选制品 | `build-local-snapshot.py --version 1.20.0-dev.1 --revision 529bd59d…` 构建；判据：`local-dev.json` 的 `sourceRevision` 与提交一致、`sourceDirty=false`，CLI/daemon/连接器版本一致，且与 Dev 宿主运行时 `1.19.1-dev.4` 之间无运行时相关差异。 |
| E2E | 完整设计与现场验收 | 未执行；后续按最小点灯板原始需求完成完整设计、DRC、保存重载与新回读。不能用本版定向用例或自动测试补签通过。 |

复现入口：上述命令，以及本地忽略的 `artifacts/v1.20.0-logs/` 下按用例命名的原始记录（不入库，
哈希记于报告）。正式 v1.20.0 制品另用 `make release-check release-build VERSION=v1.20.0` 核验，
发布后还须核验真实 GitHub 资产的下载与安装。
