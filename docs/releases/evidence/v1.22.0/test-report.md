# v1.22.0 定向验收报告

日期：2026-11-27。范围由本版用户明确决定，详见 baseline.md。结论只覆盖下列实际完成的自动回归、EdaDesk DEV 现场写入与
回读、v1.21.0 负对照和候选制品身份；完整设计 E2E 未执行。

## 用例结果

| ID | 结果 | 实际证据与边界 |
| --- | --- | --- |
| M1 | pass | 候选 `51e483c8`：`go vet ./...` 退出 0（日志为空）；`go test -json ./...` 退出 0，事件统计 6422 个测试 pass（顶层 3187、子测试 3235）、16 个包 pass、0 失败；1 个测试 skip（平台限定）与 2 个无测试文件的包 skip，不计为通过。连接器 `npm test` 1432 项通过、0 失败，`tsc --noEmit` 退出 0。`make agent-check`、`skill-check`（192 个已跟踪文件）、`release-script-test` 退出 0。首轮连接器测试与 `tsc` 因任务 worktree 缺 `extension/node_modules`（`ts-node` 找不到）退出 1，链接主检出的同版本依赖后重跑通过，日志为重跑输出（`m1-exits.log`）。 |
| F1 | pass | `LED 指示` 页经 `schematic.component.place` 放 PI7C9X2G608GPBNJEX（库 `0819f05c…`/`05552086…`，`lib search` 现场解析）的单元 .1/.2/.3（primitiveId `d82c2803…`、`53ed2841…`、`6c739f7f…`），`sch modify` 赋 `U90`；`pcb import-changes` 后三者 uniqueId 均为 `gge18`。基线 `f1-before.json` 三单元引脚 106+43+47=196，`netlistAvailable`/`pinsAvailable` 为真。候选 `sch designators bind` 退出 0，`bindings:["U90","U90","U90"]`，队列为 verify-before、bind-001..003（每个单元一条 `designator:U90` + `EasyEDA Agent Component ID:cmp-U90`）、verify-after、save（`f1-bind.log`、`f1-bind.json`）。`sch apply --json`：`ok:true`、`okCount:6`、`skipped:0`，verify-after 回执 `components:3`、`ok:true`（`f1-apply.json`、`f1-apply.err`、journal）。 |
| F2 | pass | `doc reload --json`：F1 后 `status:passed`、`saved:true`、`closed:true`、`reloaded:8fb55bd61d0986a9`（`f2-reload.json`）；F3 后（目标 PCB1）与清理后（原理图页 `c1-cleanup-sch.log`、PCB1 `c2-pcb-reload-readback.log`）同为 `passed`/`saved:true`（`f3-reload.json`）。F1 重载后新回读 `f2-after.json`：三个单元均为 `U90`、uniqueId `gge18`、`cmp-U90`；对其再次 bind 报 “every part already carries its stable component binding; nothing to bind” 且不写队列（`n1-refusal.log`）。 |
| F3 | pass | `pcb autoroute --net OSC_IN --timeout 4m`：作业 `autoroute-1` completed，`created` tracks 8 / vias 0 / arcs 0，`createdByNet:{OSC_IN:8}`；请求 props `RoutingNets:["OSC_IN"]`、`ignoreNets` 为其余 6 个网络（+3V3、+5V、BOOT0、GND、NRST、OSC_OUT），`existingPrimitiveMode:keep`；`outOfScopeNets:[]`、`lockedLost:[]`（`f3-autoroute.json`、`f3-autoroute.err`）。保存重载后新回读：OSC_IN 344.709 mil，其余 6 个网络 0（`f3-nets-after.log`）。没有 v1.21.0 现场负对照；越界新铜时失败只有自动测试。 |
| F4 | pass | 用户在 PCB1 画布上手动选择后，`easyeda selection` 退出 0：`pcb f708451540690854: 11 selected`，含 7 条 OSC_IN `Line`、1 个 `Pad`（`286c2401b788c33ce5`，网络 OSC_IN）与 C7/C11/C10 三个 `Component`；`--json` 上下文为 ceshi / `f708451540690854` / pcb（`f4-selection-pcb.txt`、`f4-selection-pcb.json`）。独立回读：选区后立即读取的 `pcb.line.list` 共 8 条 OSC_IN 线，选区 7 条全部在内（`f4-line-list.json`）；器件对应由清理并保存重载后的 `pcb list` 原始输出核对（`c2-pcb-reload-readback.log`，primitiveId 跨重载不变）：C7 `0fa68ab08f38fbb2`、C10 `f442f29b50665cb3`、C11 `13b06e4c57eaf66f` 与选区一致，Y1 为 `286c2401b788c33c`，选区中的 Pad 以其为前缀。用户原定选 Y1、C11 与一段走线，实际选区如上，如实记录。读取后没有单独回读选区或工程，“只读、不改变选区”只由代码与自动测试支持。原理图页选区、读取中切页的 `page drift`、非原理图/PCB 拒绝、空选区只有自动测试。 |
| E1 | pass | v1.21.0 CLI（SHA256 `17be2b20…`，与 v1.21.0 Release 记录一致）：动作目录中 `selection.get` 0 次，`selection --help` 报 `unknown command "selection"`；候选 dev.7 目录中 1 次（`e1-negative.log`）。对 F1 同一份现场基线 `f1-before.json` 离线运行 `sch designators bind`：`duplicate component binding cmp-U90`，退出 1（`e2-v121-bind.log`），即 #365 的原始故障。 |
| L1 | pass | 原理图写入经自动检查点钩子记录：三次放置 `20261127T070253…-680e`、`…070256…-6872`、`…070257…-68a0`（delta），apply `…070517…-72f5`（same），清理删除 `…072913…-1e76`（same），随后两次原理图 `doc reload` 产生 `…072914…-2899`（delta）、`…072916…-3023`（same）；放置检查点见 `f1-place-1..3.json`；stderr 无 `⚠ no checkpoint`。PCB 文档按设计不存检查点。本轮未执行 `checkpoint restore`；回退以清理后保存重载的新回读回到起点证明：`doc reload f708451540690854` 回执 `status:passed`、`saved:true`、`target:f708451540690854`，之后 `pcb list` 17 个器件、无 U90，`pcb.line.list` 0 条，7 个网络长度 0，`LED 指示` 只剩图框（`c2-pcb-reload-readback.log`，含原始 JSON 与 UTC 时间）。注意：`c1-cleanup-sch.log` 中标为 “pcb reload” 的一次与 `f1-reload-pcb.json` 实际重载的是当时活动的原理图页——执行者判断原因是 `doc reload` 以位置参数选目标、未给时默认活动文档（`internal/app/cmd_doc.go` 帮助文字），全局 `--doc` 对它无效，原始记录只能证明两次重载实际落在原理图上；`c1-final-readback.log` 因此只是未重载的实时回读，以 `c2` 为准。起点的位号集合只有 `f1-import-changes.json` 的导入前 17 个器件与 F3 的 `before.tracks:0` 间接支持，属弱证明。 |
| N1 | pass | 对 guarded 队列 `f1-bind.json`：`--project ceshi`（工程名，队列锁定 UUID）与 `--doc 8fb55bd61d0986a9` 两种覆盖均在写入前退出 1，`PRECONDITION_REFUSED`/“cannot override guarded plan target”；已全部绑定的页再次 bind 报 “nothing to bind”，未写队列；随后回读页面不变（`n1-refusal.log`，为清理后重放：首次现场拒绝发生在 F1 执行前，其输出被后续成功运行覆盖）。 |
| R1 | pass | 快照 `local-dev.json`：`version=1.22.0-dev.7`、`sourceRevision=d6efaf0c…`、`sourceBranch=dev`、`sourceDirty=false`、`versionInjected=true`；`shasum -c checksums.txt` 全部 OK。EdaDesk DEV `active.json` 的 CLI SHA256 `2c802520…` 与快照一致；`git diff d6efaf0c..51e483c8` 只有 7 个文件（版本字符串、Skill 元数据、Changelog），`extension/src`、`internal`、`cmd`、`pkg` 无差异（`r1-candidate-identity.log`）。`r1-health.json` 记录 daemon `v1.22.0-dev.7` 与连接器 `1.22.0-dev.7`。 |
| E2E | not-run | 本版用户在被告知需要现场验收后选择“定向验收，E2E 延期”；完整设计及其 DRC、保存重载、新回读延期至 follow-up，不沿用旧版本的现场结论。 |

以下行为本轮**只有自动测试**，不写成现场通过：`selection` 在原理图页上的读取、读取期间活动文档变化的 `page drift`、
非原理图/PCB 文档的拒绝、空选区；多单元器件单元分在多页时页级修复的拒绝、不同器件同号/只绑一半的拒绝；
`autoroute --net` 出现越界新铜时失败；compose `--preserve-instances` 对多单元器件的拒绝。

## 可追溯证据

源码候选 `51e483c80aa5f695190a45947867ff9294186427`；现场等价运行时 `d6efaf0c…`（`1.22.0-dev.7`）；负对照 `v1.21.0`。
原始记录留在本地忽略的 `artifacts/v1.22.0-logs/`，不入库；其中 `f1-bind.log`、`f1-bind.json` 含本机绝对路径，公开包只引用哈希。

| 原始记录 | SHA256 |
| --- | --- |
| m1-exits.log | ec67f986c7e903a50951c418321f3e97c6e14b1bda7864d9430c104ca7bf0c14 |
| m1-go-vet.log | e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 |
| m1-go-test.jsonl | c838eb759f2c55220474386d5ec05ef8fcc83443d84fdab957360c6d1cbae048 |
| m1-go-test.exit | 9a271f2a916b0b6ee6cecb2426f0b3206ef074578be55d9bc94f6f3fe3ab86aa |
| m1-connector-test.log | 391055f7660c96e0f6efb4e3fd3afa5f9e0d8cd19977193d4609a2882aafc85c |
| m1-tsc.log | 846868eca584eb84bc0ca893a4f49461560645a8a2805b5f845f7dc5d33e0aea |
| m1-agent-check.log | 7b634053acc01e554ebdd060c7a143b7e088f569ab63d6350e88eb5e6d190a5d |
| m1-skill-check.log | f3fc1c86dfdbc82e50438c685ba1794aa03f071d4826292e64342ac994c79ea7 |
| m1-release-script-test.log | d285d6335046777f42807ad1869acb0c3b97002427c0b2f61241f44146519a68 |
| e1-negative.log | bc331dfdc986dbeb725373cc0d47f688ec07312c71feebc45f62b0fed3d9c792 |
| e2-v121-bind.log | 44eb51308f22cc1933577c71e72b0ecedb3f395eaa303563cce480bb7244913d |
| f1-before.json | 3c80495224df2f516a8d4ad5d8c1a6d5b9b3799149e739302752c71364699eff |
| f1-bind.log | f75cbeb93555c1048512ffb7260f9cda4d1cb6cdd369d8b2cafedcacec12bcce |
| f1-bind.json | 0e04570dd0aa9e8b31384c5c1d1c89ee21f4b0b79aad31db64cbee0367df4dd5 |
| f1-bind.json.journal.jsonl | 237fa0aae6160cd3f8ad46a3fd1e1c3613f664c9e0cbbfd7fc2d7532a3bbdd87 |
| f1-apply.json | 85dd20f957597cf467e8559ce29dc04c086e8dc391be58e37b8e1359bbd01bc9 |
| f1-apply.err | a36c24288b55222e3208ef7a42fd02339b7dfa8b649d0b38987cdac0f4cac52c |
| f2-reload.json | 87c6fe085d7d56d56027a81137b2b2246dbe4b457d8002987f393089c2a3927c |
| f2-after.json | cb462aaf23177eecd28bece3435c02012c225ca6b07419c8d831a18818869336 |
| f3-autoroute.json | e0045f4858e23c803260eef9641e7799e29db5f0e9a58cb463aff4c91c38d89c |
| f3-autoroute.err | 2c1f3aaa7ccb1bf9144c0a085e58579f6e3125dcdba9d39fe8b941d1d13880cd |
| f3-reload.json | 6f14a5ac0e6dca3bbf5b080e66488d41d92b0ae1ea2f6a7e36eb8eb060f74bcf |
| f3-nets-after.log | 33ed5664c274d8d4ab34788d72d1fa8731ad3c47afc371fd423ca9e8f600bf19 |
| f4-selection-pcb.txt | b86c661b2acb11d3594f8573ded9901ad43197c830cb9bafba5ec5a9fb781888 |
| f4-selection-pcb.json | e384f1e8d4c463af573ed98cbb7d2218647ed463ee1fe794e03c40869ba113af |
| f4-line-list.json | d1f1d48b6b59facdaf13f409ff7c6ca7c1c35050d14ec2da7f91b02541fba8c6 |
| n1-refusal.log | 89fb9b3e1ae050722cbda0ae0390590f3f56fad02fd698db8e99d09b7d6e8124 |
| f1-place-1.json | ddc4e9ac2d45a9c71dcd8413584b814c2fd6c61f26a9f8b260712ebbaa8419df |
| f1-place-2.json | 86e8ca238f860990972847dc8ca5694219921ef4122fe4de4ca14734da7c3cc3 |
| f1-place-3.json | 6deb079f90b3af62195a354cbe0bd93860e77383415856def4600c0174568da9 |
| f1-import-changes.json | 3f34a6afaecfca9752a5c92c75f2e5f2ccd12d48726a32b21f8002cb51097fb7 |
| f1-reload-pcb.json | 9d70139bf8c8a93807434d6d4887f803f3542c9ed79e31976d2ac9a79aa4508b |
| c2-pcb-reload-readback.log | 22d14afc8d9949e41bc797d620ceec5aeb63f5ac0a9433b2c2f2e450c7284bc2 |
| c1-cleanup-pcb.log | 69dcea76279591d85c349167f2d00369fe5a56289de94ea1d84f4b0ac26863c5 |
| c1-cleanup-sch.log | 57f8a824113b3d98f91eecc3061fc6e4a85a7bf3e0a23633af26699821b40b29 |
| c1-final-readback.log | a1429e0b331f2a57b434090e974016bcca4e81401918945f79aa930c3142fee2 |
| r1-candidate-identity.log | bb4a334c256ecdbfde733410b3050e061a3d4a03c0bdf8330d00c5ad61c28b79 |
| r1-health.json | 00b6330b7f0b0f006cfdc5e455135cfb3f3d9a08e2ac819813759abbc8840f6e |
| r1-active.json | a284445cd0606d800da59f0ed086caf1a3f63832ddcb44d3a4edd72b736dcf54 |

## 现场回读

宿主为 EdaDesk DEV 中的 EasyEDA Pro 4.1.105，工程 `ceshi`，daemon 端点 `127.0.0.1:60833`，daemon 与连接器 `1.22.0-dev.7`，
窗口 `24ee8c55…`。全部写入由执行 Agent 用运行时 CLI 经 Cobra 子命令或 `easyeda call` 的类型化动作下发；原始记录只有各用例的输出，没有完整命令流水，
“没有 `exec_js`、没有 GUI 编辑”是执行者观察。F4 的选区由用户在画布上手动选择（执行者只读取）。

- F1/F2 以 apply 回执、verify-after 结果、`doc reload` 回执与重载后的 `sch list` 新回读为准。
- F3 以作业回执、请求 props 与重载后的 `pcb nets` 新回读为准；PCB 起点无走线，因此“其余网络长度 0”能直接证明没有越界铜。
- 中途两次参数错误均在写入前被 CLI 拒绝（`call` 位置参数格式，该次输出未留存；guarded 队列的工程名覆盖，见 N1 重放），没有落地改动。
- 测试对象（U90 三个单元与其 PCB 封装、8 条 OSC_IN 线）在验收结束时删除；原理图页与 PCB1 分别按目标保存重载，新回读回到起点（L1、`c2-pcb-reload-readback.log`）。
- `f3-nets-after.log`、`c1-final-readback.log`、`n1-refusal.log` 末段是脚本对原始响应的摘要，无单独时间戳；`f2-reload.json` 首行混有一行 stderr 检查点信息，不是纯 JSON。
- `e2-v121-bind.log` 的输入为 `f1-before.json`（与 `f1-bind.log` 同一时刻、同一命令行），器件库身份由 `lib search` 现场解析，这两点日志未直接记录命令行。

## 独立复核

独立 Agent 于 2026-11-27 只读复核了本版基准、用例、报告与 `artifacts/v1.22.0-logs/` 的原始记录：未改仓库文件，未访问 daemon 或 EDA，
未重跑测试，最终结论 **pass**。已独立复算并核对：报告 SHA256 表 39 条原始记录全部一致；M1 的 go test 事件（顶层 3187、子测试 3235，
共 6422 pass；16 个包 pass；1 个平台限定 skip `TestModifyPatchWindowsPowerShell51` 与 2 个无测试文件的包；0 失败）、连接器 1432 项与其余
M1 退出码；F1 基线三单元（.1/.2/.3、引脚 106/43/47、uniqueId gge18）、bind 队列 6 步与 payload、apply `okCount:6` 与 verify-after；
F2 重载回执与新回读的 `cmp-U90`；F3 作业回执、props、`ignoreNets` 与重载后网络长度；F4 选区与 `pcb.line.list`、`c2` 中 `pcb list` 的 id 对应；
E1 的 v1.21.0 负对照及其 SHA256 与 v1.21.0 Release 记录一致；L1 检查点行在原始记录中的归属；N1 拒绝记录；R1 的快照字段、
`shasum -c checksums.txt` 11 项、`active.json` 与已安装运行时 CLI 哈希 `2c802520…`、`d6efaf0c..51e483c8` 仅 7 个版本/文档文件且
`extension/src`、`internal`、`cmd`、`pkg` 无差异；用例与报告 ID 集合一致、E2E not-run；公开文件不含本机路径或账号。

首轮复核结论 `fail`，三个阻塞项已在冻结前修正并经复核确认：清理后 PCB 未按目标保存重载却写成已回到起点（补做
`doc reload f708451540690854` 与新回读，见 `c2-pcb-reload-readback.log`，并说明 `c1` 中的“pcb reload”实际重载了原理图）；L1 检查点
归属错误（改为删除 `-1e76`、两次重载 `-2899`/`-3023`）；F4 中 `pcb list` 独立回读缺原始记录（改由 `c2` 核对）。第二轮指出 `-1e76`
应为 same，已修正。同时采纳的非阻塞意见：哈希表补放置、导入与重载记录；起点状态标为弱证明；`exec_js`/GUI 改为执行者观察；
注明摘要文件、`f2-reload.json` 首行 stderr、E1 输入与 `lib search` 未直接记录；`doc reload` 原因标为执行者判断并附源码出处。

未能独立确认（如实保留）：用户范围决定的原始记录；F4 选区由用户手动选择；无 GUI 编辑、无 `exec_js`；N1 的首次现场拒绝（报告用
清理后的重放）；起点状态（弱证明）；`lib search` 解析过程；GitHub 远端 v1.21.0 资产（只比对了仓库文档与本机二进制）。
`release-check`、`release-build` 与发布后的远端资产核验属于发布阶段，不在本证据内。
