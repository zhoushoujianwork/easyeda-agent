# v1.21.0 定向验收报告

日期：2026-11-25。范围由本版用户明确决定，详见 baseline.md。结论只覆盖下列实际完成的自动回归、`pcb.silk.delete`
的 DEV 现场写入与写前拒绝、v1.20.0 负对照和候选制品身份；完整设计 E2E 未执行。

## 用例结果

| ID | 结果 | 实际证据与边界 |
| --- | --- | --- |
| M1 | pass | 候选 `55fef8f4`：`go vet ./...` 退出 0；`go test -json ./...` 退出 0，事件统计 6290 个测试 pass（含子测试，顶层 3087）、16 个包 pass、0 失败；1 个测试 skip（平台限定）与 2 个无测试文件的包 skip，不计为通过。连接器 1420 项通过、0 失败，`tsc --noEmit` 退出 0。`make agent-check` 13 项、`skill-check` 192 个已跟踪文件、`release-script-test` 144 项全部通过，退出码均为 0。原始日志见 `m1-*.log`、`m1-go-test.jsonl` 与 `m1-go-test.exit`。 |
| F1 | pass | EdaDesk DEV 会话 `6b4ce467…`、turn `3b86618e-235d-4d22-a1f5-fb053346605e`（模型 `edadesk-gpt-6.1-sol`、提示含 Auto 块、宿主 CLI `1.20.1-dev.2` / `fba9e991…`，见 `f1-session-context.log`）：内置 AI 先 `pcb.silk.add` 添加 `DEL341`（primitiveId `b193a423578a9597`，层 3，(400,400)），批次 `applied`、`saved:true`，源哈希 `dd146105…` → `3be48aef…`；再 `pcb.silk.delete {"primitiveId":"b193a423578a9597"}`，连接器响应 `deleted:true`、`verified:true`、`text:DEL341`、`layer:3`，批次 `applied`、`saved:true`，源哈希回到 `dd146105…`（与测试前一致）。成功删除走的是 `easyeda call pcb.silk.delete`（Desk 类型化写入），`easyeda pcb silk-delete --id` 子命令现场只跑过 N1 的拒绝路径。turn 内另有三次 `pcb.silk.list`（05:56:10Z、05:56:54Z、05:57:32Z，退出 0）；AI 最终回复报告自由丝印 0 → 1 → 0，该计数出自 AI 回复，独立回读见 F2。记录见 `f1-ai-turn.jsonl`、`f1-cli-timeline.json`、`f1-session-context.log`。 |
| F2 | pass | 两次批次后的 `doc reload --json` 回执均为 `status:passed`、`saved:true`、`closed:true`、`reloaded=f708451540690854`（`f1-cli-timeline.json`）。之后 EdaDesk DEV 重装并重启，用户重新登录后 PCB1 从磁盘重新打开；06:22:31Z 用候选等价 CLI 独立 `call pcb.silk.list` 回读：`count:0`，不含 `DEL341`，`doc ls` 显示 PCB1 为活动文档（`f2-fresh-readback.log`，日志头记 CLI SHA256、daemon、连接器与窗口身份）。 |
| E1 | pass | Release `v1.20.0` 的 `easyeda_darwin_arm64`（SHA256 `5ab32d28…`，与该 Release `checksums.txt` 一致，`easyeda-agent v1.20.0`）：动作目录只有 7 个 `pcb.silk.*`（align/list/add/set/netnames/label_pads/import_svg），`pcb.silk.delete` 出现 0 次；`pcb silk-delete --help` 不是已知子命令，只打印 `pcb` 的总帮助；`pcb --help` 列出的 `silk-*` 子命令中 `silk-delete` 计数为 0（`e1-v120-negative.log`）。候选目录含 `pcb.silk.delete`，access 为 document/write、design 审批、save-reload 校验——此项来自候选源码 `internal/protocol/action_access.go` 与 DEV 运行时 `easyeda actions`，不在原始日志中。 |
| L1 | pass | 删除经 `easyeda call pcb.silk.delete` 下发，stderr 为空，没有 `⚠ no checkpoint` 警告；按 `checkpoint_auto.go` 的设计，自动检查点只记录原理图页，PCB 文档返回“无需记录”，因此本轮**没有** PCB 检查点，如实保留该边界（“PCB 无需记录”依据源码；日志未记录 `EASYEDA_CHECKPOINT` 未设置，stderr 为空在该变量为 off 时也会出现）。本轮没有验证 `checkpoint:restore`；现场只证明反向写入（添加后按 ID 删除）可使 PCB1 源哈希 `dd146105…` → `3be48aef…` → `dd146105…` 精确复原（`f1-ai-turn.jsonl` 的两个 design-result）。 |
| N1 | pass | 06:22:32Z 对已删除的 `b193a423578a9597` 执行 `easyeda pcb silk-delete --id …`：退出码 1，响应 `ok:false`、`PRECONDITION_REFUSED`，消息 “not found or ID readback mismatched … no delete was attempted”；随后回读 `count 0`、自由丝印为空（`n1-refusal.log`；该行是脚本对原始响应的摘要输出，不是原始 JSON）。锁定、非丝印层、属性 ID 的拒绝与“清单读不到 → verified:false/partial:true”只有自动测试覆盖。 |
| R1 | pass | 现场快照 `local-dev.json`：`version=1.20.1-dev.2`、`sourceRevision=1516cc8c…`、`sourceBranch=dev`、`sourceDirty=false`、`versionInjected=true`；`shasum -c checksums.txt` 11 项 OK。`git diff 1516cc8c..55fef8f4` 只有 7 个文件（版本字符串、Skill 元数据、Changelog），`extension/src`、`internal`、`cmd`、`pkg` 无差异（`r1-candidate-identity.log`）。`r1-health.json` 记录 daemon `v1.20.1-dev.2` 与连接器 `1.20.1-dev.2`。 |
| E2E | not-run | 本版用户在被告知需要现场验收后选择以定向范围发布 v1.21.0；完整设计及其 DRC、保存重载、新回读延期至 follow-up，不沿用旧版本的现场结论。 |

以下行为本轮**只有自动测试**，不写成现场通过：锁定文字、非丝印层字符串、器件位号/值属性 ID 的拒绝（属性 ID 的测试用模拟“找不到”ID，依赖“属性 ID 在 `pcb_PrimitiveString.get` 中解析为空”这一未现场验证的假设）；多余 payload
键的拒绝；删除后清单读不到时的 `verified:false`/`partial:true`；Apply 对未核实删除的停止与不重试。

## 可追溯证据

源码候选 `55fef8f4e0acbf0de7e1f24f40c349f5496ae3d8`；现场等价运行时 `1516cc8c…`（`1.20.1-dev.2`）；负对照
`v1.20.0`。原始记录留在本地忽略的 `artifacts/v1.21.0-logs/`，公开包不含个人路径、账号或私有原始数据
（EdaDesk 导出的命令记录已把本机路径写成 `[本机路径已隐藏]`，提取脚本未再引入本机路径）。

| 原始记录 | SHA256 |
| --- | --- |
| e1-v120-negative.log | 90cb9058ded142ada7279015d8b7b42e0b344b6fdb7762f4c1c3220ac5e072d3 |
| f1-ai-turn.jsonl | 419d904e52b1e1233a7aaf1584a1cb1bee77d5d8400c6841c4850e2da998aa64 |
| f1-session-context.log | 4a14ac07802592c4a951e590957526b55089e814becf33bc33097c93a70180ff |
| f1-cli-timeline.json | d1fcc506d1a83de5a02f82432bdb3a2a6a464fc2ad9ef5d8185ebed1344fccef |
| f2-fresh-readback.log | e53e5bb9cb14bdc76c3515ea1e90121d085707e5379ed9ce000a60829283394f |
| m1-agent-check.log | d2c28b7aef1e112207ccddee98fe1665fa29e7cd1c133f94b458833fc9a37f1b |
| m1-connector-test.log | c9050d2c818979a7bb24496456b471133412c57903e591cb3de8b092a8a82b84 |
| m1-go-test.exit | 0d8eca0af7f57f01ce9b0ee5a7b5965bfa1aafdaa22b6b2c87a8d8d092c31b38 |
| m1-go-test.jsonl | 65b779b0bb84fcdfcd2a1afd9d25030b95dfca6e02c2429d6cae224acfb48b0a |
| m1-go-vet.log | eadb1d3e83a92e84d152105cc960f235e61e0cdae55ca4dc57960ed9e81e9193 |
| m1-release-script-test.log | c410c8dc122dcad11f5668145ae78567649e3f3c5495c874e0437ab86f13c78e |
| m1-skill-check.log | 4a586623a26b092db9ff2b80599c68d8c9d6853f9d678d64c796c1307610a51f |
| m1-tsc.log | 74c1218ddf8d546bc2e44b22e44052369249be54939538064bd5878abd8c15bf |
| n1-refusal.log | 89696c191719006f1ad8a9b56192a7a6f6a4ba0c7e0944cd410eb79a84c676c9 |
| r1-candidate-identity.log | e964f027217405e0405d7fd0d0256565872cd10af6dba048e7c67ae2109f3eac |
| r1-health.json | 2d810f92d8de029cdefaa1884b37cd467e620e4edf210458cb4fdc79cf6c5dc2 |

## 现场回读

宿主为 EdaDesk DEV 中的 EasyEDA Pro 4.1.105，工程 `ceshi`，daemon 端点 `127.0.0.1:60833`，连接器 `1.20.1-dev.2`。
写入全部经 EdaDesk 内置 AI 的类型化设计批次（`design:propose` / `edadesk_design_propose`）下发，日志中没有 `exec_js`；
“没有 GUI 编辑”是执行者现场观察，原始日志未记录。

- F1 以连接器删除响应的 `deleted/verified`、设计批次的 `saved:true` 与前后源哈希为准。PCB1 是空板，源哈希回到测试前
  只能弱证明没有误删其他对象；F1 中间 `pcb.silk.list` 的输出未留存，0 → 1 → 0 的计数出自 AI 回复。
- F1 时刻的窗口为 `547ad8c2…`，连接器版本未在当时单独记录；运行时身份由宿主 CLI 事实（`local-c0976fb7…` 即快照
  bundle）与连接器实际执行了 `pcb.silk.delete` 共同推断。F2/N1 在重启后的窗口 `26008b66…` 上执行。
- F2 的新回读发生在宿主重装、重新登录并从磁盘重开 PCB1 之后，不依赖进程内缓存。重开后同一标签出现两个连接器会话
  （`26008b66…`、`ebd51c92…`），两者 `doc ls` 结果相同；读取使用前者，如实记录。
- 宿主侧观察（不属于本版 CLI 结论）：DEV 重装后嘉立创登录过期导致编辑器未连接，需人工重新登录后才能回读。

## 独立复核

独立 Agent 于 2026-11-25 只读复核了本版基准、用例、报告与 `artifacts/v1.21.0-logs/` 的 16 份原始记录：未改仓库文件，
未访问 daemon 或 EDA，未重跑测试，结论 **pass**。已独立复算并核对：原始日志 SHA256 表；M1 的 go test 事件（6290 个测试
pass、含子测试，顶层 3087；16 个包 pass；1 个平台限定 skip；0 失败）及其余 M1 退出码与计数；F1、F2、N1 的 ID、源哈希往返、
响应字段和时间；E1 的 v1.20.0 负对照；`1516cc8c..55fef8f4` 仅 7 个版本与文档文件，`extension/src`、`internal`、`cmd`、
`pkg` 无差异，现场快照 CLI 与连接器哈希与本机安装的快照一致；PCB 无自动检查点的依据（`checkpoint_auto.go`）。

首轮复核提出两个阻塞项，已在冻结前修正并经复核确认：报告中“本机路径替换为 `~`”与日志不符（改为 EdaDesk 的
`[本机路径已隐藏]`）；模型、Auto 审批与 EdaDesk 修订号缺少日志证据（补 `f1-session-context.log`，删去无日志的修订号）。
同时采纳的非阻塞意见：L1 不再声称回退能力已验证；注明空板起点与源哈希的弱证明；E1 补 `silk-*` 子命令列表并标注目录
access 来自源码；N1 标注摘要行；F1 注明成功路径走 `call`、子命令只跑过拒绝路径；注明属性 ID 拒绝依赖的未现场验证假设。

未能独立确认（如实保留）：用户范围决定的原始记录；宿主重装与重新登录经过及“无 GUI 编辑”（执行者观察，日志无记录）；
F1 中间 `pcb.silk.list` 输出与 0 → 1 → 0 计数（只有 AI 回复，独立回读见 F2）；R1 的 `checksums.txt` 复验（只做了安装
快照的间接核对）；v1.20.0 二进制与 GitHub Release 校验和（只比对了仓库文档中的哈希）；F1 时刻的连接器版本（由 CLI 身份
推断）；属性 ID、锁定、非丝印层的拒绝仅有自动测试。`release-check`、`release-build` 与发布后的远端资产核验属于发布阶段，
不在本证据内。
