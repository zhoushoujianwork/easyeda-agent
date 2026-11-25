# v1.21.0 定向用例

本表复用 schema 3 的用例 ID，含义按本版重新定义，不等同于历史版本同 ID 的整板流程。现场用例只在一次性测试工程
`ceshi` 中执行；完整设计 E2E 按本版用户决定延期。

| ID | 用例 | 输入、步骤和判据 |
| --- | --- | --- |
| M1 | 全量自动回归 | 在候选提交上运行 `go vet ./...`、`go test -json ./...`、连接器 `npm test`、`tsc --noEmit`、`make agent-check`、`make skill-check`、`make release-script-test`；不得有失败，跳过项如实保留。 |
| F1 | 内置 AI 按 ID 删除自由丝印 | EdaDesk DEV Auto 会话中让内置 AI：在 PCB1 顶层丝印 (400,400) 添加 `DEL341` 并保存，再用 `pcb.silk.delete` 按返回的 primitiveId 删除并保存。判据：删除响应 `deleted:true`、`verified:true`；两个设计批次 `applied`、`saved:true`；删除后的源哈希回到测试前。 |
| F2 | 保存重载与新回读 | 判据：两次批次后的 `doc reload --json` 回执 `status:passed`、`saved/closed/reloaded` 成立；宿主重启并重新从磁盘打开 PCB1 后，独立 CLI `pcb.silk.list` 回读自由丝印 0 条、不含 `DEL341`。 |
| E1 | v1.20.0 负对照 | 用 Release `v1.20.0` CLI 列出动作目录与 `pcb silk-delete`。判据：目录中没有 `pcb.silk.delete`，也没有 `silk-delete` 子命令。 |
| L1 | 写入检查点与回退 | 判据：PCB 写入经过自动检查点钩子时不报 `⚠ no checkpoint`（PCB 文档按设计不存检查点，如实记录）；回退能力以现场“添加 → 按 ID 删除”使源哈希回到起点证明，不宣称存在 PCB 检查点。 |
| N1 | 写前拒绝 | 对已删除的 ID 再执行 `easyeda pcb silk-delete --id b193a423578a9597`。判据：退出码非 0、`PRECONDITION_REFUSED` 且消息含 “no delete was attempted”；随后回读仍为 0 条。锁定、非丝印层、属性 ID 的拒绝只由自动测试覆盖并如实标注。 |
| R1 | 候选制品与运行时等价 | 判据：现场快照 `local-dev.json` 的 `sourceRevision=1516cc8c…`、`sourceDirty=false`，`checksums.txt` 全部通过；`1516cc8c..候选` 在 `extension/src`、`internal`、`cmd`、`pkg` 无差异；daemon 与连接器回读版本一致。 |
| E2E | 完整设计与现场验收 | 未执行；后续按最小点灯板原始需求完成完整设计、DRC、保存重载与新回读。不能用本版定向用例或自动测试补签通过。 |

复现入口：上述命令，以及本地忽略的 `artifacts/v1.21.0-logs/` 下按用例命名的原始记录（不入库，哈希记于报告）。
正式 v1.21.0 制品另用 `make release-check release-build VERSION=v1.21.0` 核验，发布后还须核验真实 GitHub 资产。
