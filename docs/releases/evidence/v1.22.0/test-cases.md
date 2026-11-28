# v1.22.0 定向用例

本表复用 schema 3 的用例 ID，含义按本版重新定义，不等同于历史版本同 ID 的流程；F3、F4 为第一轮新增，E2、P1、S1、A1 为第二轮
新增。现场用例只在一次性测试工程 `ceshi` 中执行；完整设计 E2E 按本版用户决定延期。第二轮（候选 `8594aa0e`，运行时
`1.22.0-dev.10`）重跑 M1、F1、F2、L1、N1、R1 并执行新增用例；F3、F4、E1 沿用第一轮（`1.22.0-dev.7`），理由见 R1。

| ID | 用例 | 输入、步骤和判据 |
| --- | --- | --- |
| M1 | 全量自动回归 | 在源码候选上运行 `go vet ./...`、`go test -json ./...`、连接器 `npm test`、`tsc --noEmit`、`make agent-check`、`make skill-check`、`make release-script-test`；不得有失败，跳过项如实保留。 |
| F1 | 多单元器件位号绑定（#365） | 在空页 `LED 指示` 用 `schematic.component.place` 放 PI7C9X2G608GPBNJEX 的单元 .1/.2/.3，三者赋位号 `U90`，`pcb import-changes` 后三者共享 uniqueId；按 #365 复现命令抓 `sch list --stay --include-device-identity --include-pins --include-wires --include-bbox` 基线，运行 `sch designators bind` 与 `sch apply`。判据：bind 对每个单元各生成一条同位号、同 `cmp-U90` 绑定的修改；生成的队列通过 #370 新增的 `--dry-run` 预检；apply 全部步骤（含写前/写后 verify 与保存）成功。 |
| F2 | 保存重载与新回读 | 判据：F1、F3 与清理后的 `doc reload --json` 回执 `status:passed`、`saved:true`；F1 重载后新回读三个单元均为 `U90`、同一 uniqueId、`EasyEDA Agent Component ID=cmp-U90`；对该回读再次 bind 报 “nothing to bind”（已绑定的多单元器件可被解析）。 |
| F3 | `pcb autoroute --net` 只布指定网络 | 对无走线的 PCB1 运行 `pcb autoroute --net OSC_IN`。判据：作业 completed，`createdByNet` 只有 OSC_IN，`ignoreNets` 列出其余全部网络，`outOfScopeNets`、`lockedLost` 为空；保存重载后新回读只有 OSC_IN 长度非 0。 |
| F4 | `easyeda selection` 读真实选区（#360） | 用户在 PCB1 画布上手动选中对象后运行 `easyeda selection` 与 `--json`。判据：返回 `documentType=pcb` 与当前工程/文档上下文；条目的 primitiveId、类型、位号、网络与独立回读（`pcb list`、`pcb.line.list`）一致。只读性由代码与自动测试支持，现场不单独判定。 |
| E1 | v1.21.0 负对照（#365、#360） | 用 v1.21.0 CLI：动作目录与 `selection` 子命令；对 F1 的同一份现场基线离线运行 `sch designators bind`。判据：没有 `selection.get`，`selection` 是未知命令；bind 失败并报 `duplicate component binding`。 |
| E2 | v1.21.0 负对照（#370） | 用 v1.21.0 CLI 对 P1 的语法错误 playbook 运行 `sch apply --dry-run`。判据：旧版本预检通过（不能发现这些错误），即 #370 修复的原始问题。 |
| L1 | 写入检查点与回退 | 判据：原理图写入（放置、修改、apply、删除）经自动检查点钩子记录检查点、不报 `⚠ no checkpoint`；回退以清理测试对象后新回读回到起点证明，不宣称执行过 `checkpoint restore`。 |
| N1 | 写前拒绝 | 判据：guarded 位号队列在 `--project`/`--doc` 与队列锁定目标不一致时于写入前拒绝（`cannot override guarded plan target`），拒绝后工程不变；已全部绑定的页再次 bind 报 “nothing to bind” 且不生成队列。 |
| P1 | playbook 断言契约（#370） | 在 `LED 指示` 页：(a) 含 `count`、`result.count` 路径、未定义 `${UNDEFINED}` 判定式与路径内 `${I}` 的 playbook 运行 `--dry-run`；(b) 路径语法正确但字段不存在（`$.result.count`）的只读 playbook 正式运行；(c) `sch apply --help` 中示例的扩展版（建文字 → 用捕获变量与 `==${N}` 断言 → 删除 → 断言为空 → 保存）先 `--dry-run` 再 `--yes` 运行。判据：(a) 预检逐条报出 4 个问题、退出非 0、不执行；(b) 第 1 步失败并列出 `result` 顶层字段，不写入；(c) 6 步全部成功，之后 `schematic.text.list` 为空。 |
| S1 | 一键布局（#368、#371、#372） | 经 Desk 调试通道 `preview:layout` 读取 PCB1，再 `layout:simulate` 带 `spread: true`、不带位姿（页面“一键布局”的同一入口）；测试对象清理并重载后再做一次。判据：搜索完成并给出 `spread` 摘要（evaluations、前后得分与指标）；没有更好位置时主器件不动、只有 P3；两次结果一致；工程不写入（`layout:apply` 为人操作，不调用）。 |
| A1 | `pcb auto-place --dry-run` 现场规划 | 对 PCB1 运行 `pcb auto-place --dry-run`。判据：退出 0，给出规划（主器件、每个外围件的目标位姿、边与目标网络），不移动任何器件——以之后读取的 PCB1 语义哈希不变证明。 |
| R1 | 候选制品与运行时等价 | 判据：第二轮快照 `local-dev.json` 的 `sourceRevision=8594aa0e…`、`sourceDirty=false`，`checksums.txt` 全部通过；EdaDesk DEV `active.json` 的 CLI SHA256 与快照一致；daemon 与连接器回读版本一致。沿用第一轮的 F3、F4、E1：`d6efaf0c..8594aa0e` 在 `extension/src`、`cmd`、`pkg` 无差异，`internal` 的改动不含 autoroute、selection、位号与连接器相关文件。 |
| E2E | 完整设计与现场验收 | 未执行；后续按最小点灯板原始需求完成完整设计、DRC、保存重载与新回读。不能用本版定向用例或自动测试补签通过。 |

复现入口：上述命令，以及本地忽略的 `artifacts/v1.22.0-logs/`（第一轮）与 `artifacts/v1.22.0-r2-logs/`（第二轮）下按用例命名的原始记录（不入库，哈希记于报告）。
正式 v1.22.0 制品另用 `make release-check release-build VERSION=v1.22.0` 核验，发布后还须核验真实 GitHub 资产。
