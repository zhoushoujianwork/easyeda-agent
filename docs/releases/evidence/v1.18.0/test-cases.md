# v1.18.0 定向修复用例

本表复用 schema 3 的用例 ID，并在本版明确其含义，不等同于历史版本同 ID 的整板现场流程。
现场用例只在一次性测试工程 `ceshi` 中执行，只清理本轮创建的对象。完整设计 E2E 按本次用户决定延期。

| ID | 用例 | 输入、步骤和判据 |
| --- | --- | --- |
| M1 | 全量自动回归 | 在源码基准上运行 `go test ./...`、`go vet ./...`、连接器 `npm test` 与 `tsc --noEmit`；不得有失败，跳过项如实保留。 |
| F1 | #299 wire-contact 现场触发条件与修复后回读 | 新页放两个 1206 电阻（引脚 80/120、130/170），建 10 单位连接导线及三条互距 80 单位的 NET_A/B/C 拐角导线；GUI 把右侧电阻左移 10 单位压出零长度导线；CLI 保存、重载后回读几何与 `sch check` / `sch read` / `bridge-check`。判据：零长度导线 1 条、`wire-contact` 0、无 wire-bridge；加 NET_D 后仍为 0。 |
| F2 | #303 规则单位现场复现 | PCB1 导入器件后，在 `mm` 存储规则下记录 `route-short --dry-run`；在设计规则对话框把单位切为 `mil` 并确认；CLI 保存、重载后回读 `pcb drc-rules`，再跑 `route-short --dry-run`、实际布 1 段线及 `pcb check`。判据：存储单位为 mil，读到的净距/线宽与数值等价的 mm 存储一致，能规划走线，无净距/线宽误报。 |
| E1 | 旧版负对照 | 用 `v1.17.0` 标签源码构建的 CLI，对 F2 同一块 `mil` 板运行只读 `route-short --dry-run` 与 `pcb check`。判据：`route-short` 必须复现约 39.37 倍放大的误读，证明 F2 的板确实触发原缺陷。 |
| L1 | 替换查询预算与版本哈希 | 运行 `replace-lookup-timeout` 连接器测试与 `semanticSha256` Go 回归：查询共用 7 秒总预算并保留各阶段失败原因；铺铜填充变化不改变板版本号。 |
| N1 | daemon、Skill 与发布脚本 | 运行 daemon 自动替换身份保护回归、`make agent-check skill-check release-script-test`；Skill 打包 192 文件链接不越界，发布脚本全部通过。 |
| R1 | 开发候选制品与热重载 | `build-local-snapshot.py` 从固定提交构建 `1.18.0-dev.1` 并通过 local-dev release-check；热重载注入核对旧/新 bundle 哈希，`health` 出现新 windowId 与 `connectorVersion=1.18.0-dev.1`。 |
| E2E | 完整设计与现场验收 | 未执行；后续按最小点灯板原始需求完成完整设计、DRC、保存重载与新回读。不能用本版定向用例或自动测试补签通过。 |

复现入口：上述命令，以及 `artifacts/` 下按用例命名的现场原始回读（不入库，哈希记于报告）。
正式 v1.18.0 制品另用 `make release-check release-build VERSION=v1.18.0` 核验，发布后还须核验真实 GitHub 资产下载与安装。
