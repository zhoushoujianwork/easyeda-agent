# v1.20.0 定向修复验收报告

日期：2026-11-24。范围由本版用户明确决定，详见 baseline.md。结论只覆盖下列实际完成的自动回归、
页面改名与拒绝诊断的 DEV 现场验收、PCB 孔/槽与板边测量的只读现场核对、v1.19.0 负对照、检查点与
写前拒绝；完整设计 E2E 未执行。

## 用例结果

| ID | 结果 | 实际证据与边界 |
| --- | --- | --- |
| M1 | pass | 候选 `529bd59d`：`go vet ./...` 退出 0；`go test -json ./...` 统计 6256 项通过、0 失败、3 跳过（16 个测试包通过；跳过为平台限定项）。连接器 1416 项通过、0 失败，`tsc` typecheck 退出 0。`make agent-check` 13 项、`skill-check` 192 个已跟踪文件、`release-script-test` 144 项全部通过，退出码均为 0。原始日志与命令退出码见 `m1-*.log` 与 `m1-summary.txt`。 |
| F1 | pass | Dev 宿主（daemon 60833）上把 `P1`（`6abf3fbf13dc52f0`）改为 `P1-v120`：退出码 0，响应 `verified:true`，`doc ls` 回读显示 `P1-v120`；再改回 `P1` 退出码 0。`P1-v120` 的日志只有 1 个 `first-refusal` 阶段（前台为 PCB1、标签里只有 Start Page 与 PCB1），随后恢复加载两页并重试成功；改回 `P1` 的日志没有 `hostStates`。把该页改成已存在的 `LED 指示`：退出码 1、`EDA_CALL_FAILED`，detail 含 `first-refusal` 与 `after-recovery` 两阶段 `hostStates`、`observation.status=different`，页名保持不变。另：edadesk release 0.2.1 上的失败尝试含完整两阶段 `hostStates`（`host-refused-before-second-page-loaded`），说明宿主侧诊断路径在真实拒绝时可用。 |
| F2 | pass | 对 `PCB1` 只读 `pcb dump --include-copper`：33 个器件、outline=polygon；USB-C 槽孔 1–4 的 `holeRotation=1.5707963267948966`（π/2 弧度，竖向），孔偏移 ±0.0003–0.0007 mil（在 ±0.001 噪声界内仍可测）；`rules.copperToEdgeMil=11.8`、`zoneToEdgeMil=10`，`spacingSource` 指向原生 `Spacing.Safe Spacing` 单元格。`pcb check` 只读：`ERROR=0 WARN=16`（dangling=1、silkOverPad=11、powerNotPoured=2、antennaKeepout=1、matingBlocked=1；`fiducialMissing=1` 是 INFO 计数器，不计入 WARN）。`sch check --json` 含 `titleblockOverlaps=0`、`outOfSheet=0`；`sch sheet-geometry --exact` 输出图签可见区与单元格 bbox（原理图 / Schematic1 / 创建日期 / 2026-11-14 等，来源 official-svg-vectors）。`pcb drc` 返回原生失败（既有违规），未据此改板。 |
| E1 | pass | 用 Release `v1.19.0` CLI 对同一 `PCB1` 只读运行，运行身份记录在 `e1-old-identity.log`（二进制 SHA256 `919b059e…`、`easyeda-agent v1.19.0`、daemon `v1.19.1-dev.4`、窗口 `5ade41ed…`）。旧 dump 的 147 个焊盘**没有任何孔偏移/旋转测量值**——`holeOffsetX/holeOffsetY/holeRotation` 只作为 `availability` 静态占位出现（值为 available），没有数值；新版每个焊盘各写出 3 个测量字段（147×3=441）。两版 `pcb check` 输出逐字节相同（同一 SHA256），因为本板没有近边铜，**中心线测量改动无法在这块板上形成可区分差异**；该改动只由单元测试覆盖，不写成现场已验证。 |
| L1 | pass | `sch save` 与 `sch check` 的 stderr 出现 `checkpoint … (same, 0 bytes stored)`（保存 1 条、检查 2 条），说明写前检查点存在；两次成功页面改名没有打印 checkpoint 行（目标是后台页，stderr 提示 certified mutation context 未识别目标、autosave 未 arm），该边界如实保留。本轮没有新增回退写入；步骤级回退引用 edadesk#17 的 2026-11-18 DEV 现场验证（Auto 下 `checkpoint:restore` 返回 restored/verified，重复回退不写入），本轮不重放。 |
| N1 | pass | `pcb delete --ids nonexistent-v120-id`：日志记录 `exit=1`、`PRECONDITION_REFUSED`，消息为 “no deletion was dispatched”；随后重新 `pcb dump` 仍为 33 个器件（`n1-after-dump.json`），确认写入前拒绝且无删除。EDAD-6 的**部分失效**路径（一个有效 ID + 一个无效 ID）本轮没有现场制造，只有自动测试覆盖。 |
| R1 | pass | `build-local-snapshot.py --version 1.20.0-dev.1 --revision 529bd59d…` 构建成功；`local-dev.json` 的 `sourceRevision=529bd59d…`、`sourceDirty=false`、`versionInjected=true`；`checksums.txt` 全项校验通过（`shasum -c` 11 项 OK），CLI SHA256 `ac162668…`。`fb80cb36..529bd59d` 无运行时相关文件差异（15 个改动全在 docs/、package*.json、Makefile、scripts/、scripts/tests/），故 Dev 宿主运行时 `1.19.1-dev.4` 与候选行为一致。 |
| E2E | not-run | 本版用户在被告知完整点灯板 E2E 未执行后仍选择发布 v1.20.0 定向范围；完整设计及其 DRC、保存重载、新回读延期至 follow-up，不沿用旧版本的现场结论。 |

Go 的 3 项跳过为平台限定测试（Windows PowerShell 等），不计为通过。以下改动本轮**只有自动测试**，
不写成现场通过：原生自动布线可取消作业与进度（EDAD-10）、track/via 部分删除的非零退出路径
（EDAD-6）、图签文字压线的检出路径（EDAD-41；本轮只核对只读报告字段与图签几何）、板边中心线
测量（EDAD-8；本板无可区分样本）。

## 可追溯证据

源码候选 `529bd59d5f895e4e7f518f02a6756224911318ad`；Dev 宿主运行时 `fb80cb36…`；负对照
`v1.19.0`。原始记录留在本地忽略的 `artifacts/v1.20.0-logs/`，公开包不含个人路径、账号或私有原始
数据。带时间戳/版本头的记录（M1、E1、L1、N1、F2 图签几何）可在文件内直接确认运行身份。

| 原始记录 | SHA256 |
| --- | --- |
| f1-rename-p1-v120.log | aa1bd4f450210c87dd7aea790fa37bb0d426f1995ac345af0bdfe5b2a67da884 |
| f1-rename-back.log | 6b02ca4258390936e406c8f77c0323ed572ab9d84030d3c7da5fe2e9a683b767 |
| f1-duplicate-refusal.log | 4f0892ac1b4bc4e66047bacf89b4f91cc1552dc3e0552ad9c95fd45f96272c27 |
| f1-rename-attempt.log | a8392047c78e6ebf055afa678aab374536e565e7fa6ee37a1c153635dc67e810 |
| f2-pcb-dump.json | ea99940d30759fd1fcfaaf51ed799d7d2775620e56941f2c5699fa34913c1744 |
| f2-pcb-dump.json.capture.json | c0aaf6dd293c4b02d75c07b130f5c26023f5117a7fcbd93c675272ad98d2655c |
| f2-pcb-check.log | a4e4ccba6b2124572c186b85c47f2cedfa00b7b1f3a606476ada42eb9960b18a |
| f2-pcb-drc.log | f871c2c5ea78c53589673bd62f9d36ca239b962dd4d0d62ed126cf0d7c7d19cc |
| f2-sch-check.json | 4087ee2016df7be32df1c42acdd00870379d2486dbaaa2fc166870f617efa62f |
| f2-sheet-geometry.txt | 65bac56a12d74be50cb60452c87846e3852f251c91e90dba0d9effceb93c6bdc |
| e1-old-identity.log | bf063b112b20dc1deb02b33c27cabcd3b80c245933ab0a454e3240ecce543391 |
| e1-old-dump.json | 2b73a567aa183989c0c25fbb55c370cf0a75f8e7f9929f24ba2ece87ab6c79af |
| e1-old-dump.json.capture.json | d14800b9483b9142a0f9e2015f1ae11e92bc66deec12e69844837100c45b53a1 |
| e1-old-dump.log | d9800754a1a861dbd91ed795c5466a4ff15df33742165cf4337ccf9a76207a66 |
| e1-old-check.log | fc1ca42682953160bfc2e4fb2b2be3ea76a4492bab31fda7c7abbd23c15d8c12 |
| l1-save-checkpoint.log | 11c93e1bbae21658122b431887fff47e806dd5a7e69b862f8e269037d190d509 |
| n1-delete-bogus.log | 8efe51668b69d9bce63eccfed985a9336dabba8a19e635b3ae3beb505e76d068 |
| n1-after-dump.json | 29d2218bd82743876269304d7668ac3a09f6856b1fb5e028d4b652d838be6f99 |
| n1-after-dump.json.capture.json | 717bdc73f5a9c9525b623217ebae1a40fa78958dc5ce6f40bcf053f01c3eeeba |
| n1-after-dump.log | 9b894e6fa5747b7c2bf851a0bd2be6aa5b0e2ccf32815ef077ccdb97974e2ea1 |
| m1-go-vet.log | 84e47e457c5c8673c0c0aa772299ed3142fe237dea4a7202602426a5a78ac3a9 |
| m1-go-test.jsonl | 95c75574cc31204b64c3abe2cbbcd3c2dbe2fa521fe04147777643e7be21ec5e |
| m1-connector-test.log | 28dc8ba9ac9b67fdcfa566a375e756ba98752d84664e0385220a9ea6c73fb7c6 |
| m1-tsc.log | d8e6b26c19e5023f34e4593976dc65638a25f3864519a1f09290c6311fa30f7e |
| m1-agent-check.log | a8f67446b1c56868790675d2fbfb0ee193720bbff9679c517b31e6ad0163d02d |
| m1-skill-check.log | f3fc1c86dfdbc82e50438c685ba1794aa03f071d4826292e64342ac994c79ea7 |
| m1-release-script-test.log | 18f8243144baf37a91ca0441ab08526929da4d5f2ec15ea953bf50a573a8e00f |
| m1-summary.txt | 4a247531a6a6dad82e9c4236d9c9c6e2ad50d1fb77153387589425467583be0e |
| m1-health-dev4.json | a24aa4c100eba03bbb90858ef6f18cc95eb47c628dd0de710b223eb32286fd87 |

## 现场回读

宿主为 edadesk Dev 0.2.0 中的 EasyEDA Pro 4.1.105，工程 `ceshi`，daemon 端点 `127.0.0.1:60833`，
连接器 `1.19.1-dev.4`（窗口 `5ade41ed…`）。所有判定项都是类型化 CLI 动作或只读回读，没有 GUI
编辑、没有 `debug.exec_js` 改写工程数据。

- F1 的成功改名以 rename 响应的 `verified:true` + `doc ls` 新回读为准；拒绝用例以退出码、错误码、
  detail 中的 `hostStates` 与页名不变为准。后台页改名时 stderr 提示 autosave 未 arm，属既有边界，
  按实际记录保留。
- F2 全部只读：dump 的 `.capture.json` 记录持久化文件 SHA256 与 capture 身份；`pcb drc` 的原生
  失败是板子既有状态，不是本版回归，也没有为了让 DRC 变绿去改板。
- 失败的尝试与限制保留不改判：edadesk release 0.2.1 宿主拒绝页面改名（新诊断如实记录）；本板无
  近边铜，中心线测量无现场样本；部分删除、自动布线取消、图签压线检出仅有自动测试。

## 独立复核

独立 Agent 于 2026-11-24 只读复核了本版基准、候选制品、用例、报告与 `artifacts/v1.20.0-logs/`
的原始记录：未改仓库文件，未访问 daemon 或 EDA，未重跑测试，结论 **pass**、无阻塞项。复核提出的
非阻塞意见已在冻结前处理：

- `E1` 的字段表述改为“旧版只有 `availability` 静态占位、没有测量值”，并去掉不可复现的计数。
- `F1` 的 `hostStates` 归属按日志逐一改写（成功改名只有 1 个 `first-refusal`；两阶段来自失败尝试）。
- `L1` 改为仅引用实际出现 checkpoint 的 `sch save` / `sch check` 日志，并说明成功改名未 arm autosave。
- `N1` 补录 `exit=1` 与删除后重跑 dump（33 个器件）。
- 补录 M1 原始日志与退出码、v1.19.0 运行身份（二进制哈希/版本/窗口）、图签几何的时间戳与版本头。
- `F2` 把 `fiducialMissing` 标明为 INFO 计数器，不计入 WARN。

复核未能独立确认的项（如实保留）：Go/连接器的逐项计数依赖新补录的原始日志；`checkpoint:restore`
能力引用 edadesk#17 外部记录，本轮未重放；完整设计 E2E 未执行；`release-check`/`release-build` 与
发布后的远端资产核验属于发布阶段，不在本证据内。
