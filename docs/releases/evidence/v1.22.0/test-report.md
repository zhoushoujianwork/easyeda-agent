# v1.22.0 定向验收报告

日期：第一轮 2026-11-27（候选 `51e483c8` / 运行时 `1.22.0-dev.7`），第二轮 2026-11-28（源码候选 `8594aa0e` / 运行时
`1.22.0-dev.10`，发布依据）。范围由本版用户明确决定，详见 baseline.md。结论只覆盖下列实际完成的自动回归、EdaDesk DEV
现场写入与回读、v1.21.0 负对照和候选制品身份；完整设计 E2E 未执行。下表每行注明证据所属轮次；第二轮原始记录在
`artifacts/v1.22.0-r2-logs/`，第一轮在 `artifacts/v1.22.0-logs/`。

## 用例结果

| ID | 结果 | 实际证据与边界 |
| --- | --- | --- |
| M1 | pass | 第二轮，源码候选 `8594aa0e`：`go vet ./...` 退出 0（日志为空）；`go test -json -count=1 ./...` 退出 0，事件统计 6454 个测试 pass（顶层 3219、子测试 3235）、16 个包 pass、0 失败；1 个平台限定 skip（`TestModifyPatchWindowsPowerShell51`）与 2 个无测试文件的包 skip，不计为通过。连接器 `npm test` 1432 项通过、0 失败，`tsc --noEmit` 退出 0；`make agent-check`、`skill-check`（192 个已跟踪文件）、`release-script-test` 退出 0（`m1-exits.log`）。任务 worktree 的 `extension/node_modules` 链接到主检出同一 lock 的依赖。第一轮在 `51e483c8` 上最终全部通过（6422 pass；其连接器测试与 `tsc` 首次因缺依赖失败，链接依赖后重跑通过）。 |
| F1 | pass | 第二轮。`LED 指示` 页经 `schematic.component.place` 放 PI7C9X2G608GPBNJEX（`lib search` 现场解析为库 `0819f05c…`/`05552086…`、C5116335，`f1-lib-search.json`）。首次三次放置未传 `subPartName`，三者都成了单元 .1（只留存 `f1-place-1.json`，另两次输出被覆盖）；按 primitiveId 删除多出的两个 .1（`f3239294…`、`6f548830…`，`f1-redo-delete.log`），再显式传 `subPartName` 放 .2、.3（`f1-place-2.json`、`f1-place-3.json`）。最终三单元 `96d8631e…`(.1)、`b0e17397…`(.2)、`dcb5e565…`(.3)，`sch modify --designator U90`（`f1-modify-*.json`）；`pcb import-changes` 17→18、`verified:true`、无重复 uniqueId，三者 uniqueId `gge18`（`f1-import-changes.json`、`f1-before.json`，引脚 106/43/47）。`sch designators bind` 退出 0，`bindings:["U90","U90","U90"]`（`f1-bind.log`）；生成的队列 `--dry-run` 预检通过，6 步为 verify-before、bind-001..003、verify-after、save（`f1-apply-dryrun.log`）。`sch apply --json`：`ok:true`、`okCount:6`、`skipped:0`，verify 前后均 `components:3`、`ok:true`（`f1-apply.json`、`f1-apply.err`、journal）。 |
| F2 | pass | 第二轮。`doc reload 8fb55bd61d0986a9 --json`：`status:passed`、`saved:true`、`closed:true`、`reloaded`/`target` 均为该页（`f2-reload.json`）。重载后新回读 `f2-after.json`：三个单元均为 `U90`、uniqueId `gge18`、`EasyEDA Agent Component ID=cmp-U90`；对其再次 bind 报 “every part already carries its stable component binding; nothing to bind”，退出 1，未写队列（`n1-refusal.log`）。清理后两份文档的 `doc reload` 同为 `passed`/`saved:true`（`c1-cleanup.log`）。 |
| F3 | pass | 第一轮（沿用，理由见 R1）。`pcb autoroute --net OSC_IN --timeout 4m`：作业 `autoroute-1` completed，`created` tracks 8 / vias 0 / arcs 0，`createdByNet:{OSC_IN:8}`；请求 props `RoutingNets:["OSC_IN"]`、`ignoreNets` 为其余 6 个网络（+3V3、+5V、BOOT0、GND、NRST、OSC_OUT），`existingPrimitiveMode:keep`；`outOfScopeNets:[]`、`lockedLost:[]`（`f3-autoroute.json`、`f3-autoroute.err`）。保存重载后新回读：OSC_IN 344.709 mil，其余 6 个网络 0（`f3-nets-after.log`）。没有 v1.21.0 现场负对照；越界新铜时失败只有自动测试。 |
| F4 | pass | 第一轮（沿用，理由见 R1）。用户在 PCB1 画布上手动选择后，`easyeda selection` 退出 0：`pcb f708451540690854: 11 selected`，含 7 条 OSC_IN `Line`、1 个 `Pad`（`286c2401b788c33ce5`，网络 OSC_IN）与 C7/C11/C10 三个 `Component`；`--json` 上下文为 ceshi / `f708451540690854` / pcb（`f4-selection-pcb.txt`、`f4-selection-pcb.json`）。独立回读：选区后立即读取的 `pcb.line.list` 共 8 条 OSC_IN 线，选区 7 条全部在内（`f4-line-list.json`）；器件对应由清理并保存重载后的 `pcb list` 原始输出核对（`c2-pcb-reload-readback.log`，primitiveId 跨重载不变）：C7 `0fa68ab08f38fbb2`、C10 `f442f29b50665cb3`、C11 `13b06e4c57eaf66f` 与选区一致，Y1 为 `286c2401b788c33c`，选区中的 Pad 以其为前缀。用户原定选 Y1、C11 与一段走线，实际选区如上，如实记录。读取后没有单独回读选区或工程，“只读、不改变选区”只由代码与自动测试支持。原理图页选区、读取中切页的 `page drift`、非原理图/PCB 拒绝、空选区只有自动测试。 |
| E1 | pass | 第一轮（沿用：被测对象是 v1.21.0 二进制本身）。v1.21.0 CLI（SHA256 `17be2b20…`，与 v1.21.0 Release 记录一致）：动作目录中 `selection.get` 0 次，`selection --help` 报 `unknown command "selection"`；候选 dev.7 目录中 1 次（`e1-negative.log`）。对 F1 同一份现场基线 `f1-before.json` 离线运行 `sch designators bind`：`duplicate component binding cmp-U90`，退出 1（`e2-v121-bind.log`），即 #365 的原始故障。 |
| E2 | pass | 第二轮。v1.21.0 CLI（SHA256 `17be2b20…`，与 v1.21.0 Release 记录一致）对 P1 的语法错误 playbook `p1-bad-syntax.json` 运行 `sch apply --dry-run`：“preflight passed, nothing executed”，退出 0（`e2-v121-playbook.log`），即 #370 之前预检发现不了这些错误；候选对同一文件报出 4 个问题（P1）。 |
| L1 | pass | 第二轮。原理图写入经自动检查点钩子记录：放置 `20261128T093026…-926f`（.1）、删除多余 .1 `…093055…-9467`、放置 `…093055…-946b`（.2）与 `…093056…-9485`（.3）、三次改号 `…093108…-9589`、`…093109…-958d`、`…093109…-9590`、apply `…093232…-994f`、F2 重载 `…093249…-9a1e`、P1 建文字 `…093402…-9d57`、清理删除 `…093432…-a3a6` 与重载 `…093435…-a476`；被覆盖的两次 .1 放置输出未留存其检查点行。日志中无 `⚠ no checkpoint`。PCB 文档按设计不存检查点。未执行 `checkpoint restore`；回退以清理后的新回读证明：U90 三单元与其 PCB 封装按 id 删除（封装 id 取自一次未留存的 `pcb list` 读取，`c1-cleanup.log` 只记录 `deleted:true`），两份文档按目标保存重载（`c1-cleanup.log`）；之后 `pcb list` 17 个器件、无 U90，`pcb.line.list` 0 条，7 个网络长度 0，`LED 指示` 只剩图框（`c2-readback.log`）；PCB1 语义哈希回到测试前的 `a84bfd08…88af`（`s2-preview-layout.json` 对 `s1-preview-layout.json`）。 |
| N1 | pass | 第二轮。执行者的命令包装默认带 `--project ceshi`（工程名，队列锁定 UUID），首次对 F1 队列 `--dry-run` 与正式 apply 均在写入前被拒绝（退出码只见于控制台，文件未记录）、`PRECONDITION_REFUSED`/“cannot override guarded plan target”（`n1-refusal-project-*`）；去掉 `--project` 后才执行 F1。F2 后重放：`--project ceshi` 与 `--doc 6abf3fbf13dc52f0`（P1 页）两种覆盖都在写入前拒绝；已全部绑定的页再次 bind 报 “nothing to bind”、未写队列；随后回读页面仍是图框加三个 U90 单元（`n1-refusal.log`）。 |
| P1 | pass | 第二轮，`LED 指示` 页。(a) `p1-bad-syntax.json` `--dry-run` 退出 1，逐条报出：`count` 与 `result.count` 路径须以 `$` 开头、`${UNDEFINED}` 未定义、路径内 `${...}` 不替换，共 4 个问题，未执行（`p1-dryrun.log`）。(b) `p1-bad-path.json` 预检通过；正式运行在第 1 步（只读的 `schematic.text.list`）重试 2 次后失败：“path not found; result fields: $.count, $.scope, $.texts”，0 步成功、无写入（`p1-run.log`）。(c) `p1-good.json` 预检通过；`--yes` 运行 6 步全部成功（建文字、捕获 `TXT`、断言 `$.count ==${N}` 与 `$.texts[0].primitiveId ==${TXT}`、删除、断言为空、保存），之后 `schematic.text.list` 返回 0 条（`p1-run.log`、journal）。 |
| S1 | pass | 第二轮，板子是 11-27 一键布局现场验证应用后的状态（已在局部最优，记录见 EdaDesk 验证记录，本证据目录未留存该次日志）。测试前（09:23）`preview:layout` 读 PCB1：语义哈希 `a84bfd08…`，U1 (1379.38, 701.64) 180°、U2 (791.42, 950.85) 180°，飞线 6068.23 mil、交叉 6、0 个问题；`layout:simulate`（`spread: true`、无位姿）：`spread` 摘要 `improved:false`、评估 400 次、前后得分均 7128.23（飞线 6068.23、交叉 6、射频问题 0），0 项移动，唯一提示 P3 “spread: no better placement found; left in place”（`s1-preview-layout.json`、`s1-spread.json`）。清理并重载后（09:35）重做一次，读取与结果完全相同（`s2-*`）。未调用 `layout:apply`，工程未写入。本轮只覆盖“没有更好位置”的路径；找到改进并移动主器件的路径、射频规则（ceshi 无天线）与导出顺序无关（#372）只有自动测试。 |
| A1 | pass | 第二轮。清理并重载后 `pcb auto-place --dry-run` 退出 0：`dryRun:true`、`applied` 为空，主器件 U1、U2，规划 15 个外围件（U2 10 个、U1 5 个；power 9、local 6），4 条距离诊断（C4/C5/C6/C8 距 U1 的 +3V3 焊盘 122～225 mil，超过 100 mil 上限）（`a1-autoplace-dryrun.log`）。随后读取的 PCB1 语义哈希仍为 `a84bfd08…`（`s2-preview-layout.json`），证明未移动任何器件。`auto-place` 的现场写入未执行。 |
| R1 | pass | 第二轮快照 `local-dev.json`：`version=1.22.0-dev.10`、`sourceRevision=8594aa0e…`、`sourceBranch=dev`、`sourceDirty=false`、`versionInjected=true`；`shasum -c checksums.txt` 11 项全部 OK；EdaDesk DEV `active.json` 指向 bundleSha `23de4cde…`，已安装 CLI SHA256 `53049dc2…` 与快照一致；health 报 daemon `v1.22.0-dev.10`（PID 25074）、连接器 `1.22.0-dev.10`（`r1-identity.log`）。沿用第一轮的依据：`git diff d6efaf0c..8594aa0e` 在 `extension/src`、`cmd`、`pkg` 无差异，`internal` 只改 11 个文件（`cmd_apply.go` 及测试、`pcb_autoplace*`、`pcb_layout_plan.go`、`pcb_layout_rf*`、`pcb_layout_sim.go`、`pcb_layout_spread*`），无 autoroute、selection、位号或连接器相关文件；间隔合入 #368–#372（`r1-candidate-identity.log`）。第一轮 R1（`d6efaf0c` 快照、CLI `2c802520…`）结论不变。 |
| E2E | not-run | 本版用户在被告知需要现场验收后选择“定向验收，E2E 延期”；完整设计及其 DRC、保存重载、新回读延期至 follow-up，不沿用旧版本的现场结论。 |

以下行为本轮**只有自动测试**，不写成现场通过：`selection` 在原理图页上的读取、读取期间活动文档变化的 `page drift`、
非原理图/PCB 文档的拒绝、空选区；多单元器件单元分在多页时页级修复的拒绝、不同器件同号/只绑一半的拒绝；
`autoroute --net` 出现越界新铜时失败；compose `--preserve-instances` 对多单元器件的拒绝；一键布局的射频规则与改进路径
（ceshi 已在局部最优，本轮没有移动）；卫星规划与导出顺序无关；`auto-place` 的现场写入。

## 可追溯证据

第二轮：源码候选 `8594aa0eb0963717fe9f75a83cecc56e50efba37`；现场等价运行时 `1.22.0-dev.10`；负对照 `v1.21.0`。原始记录留在本地
忽略的 `artifacts/v1.22.0-r2-logs/`，不入库；`f1-bind.json`、`f1-bind.log`、`r1-identity.log`、`s1/s2-preview-layout.json`
含本机绝对路径，公开包只引用哈希。命令包装脚本（`e.sh` 等）与 M1 运行脚本含本机路径，不计入。

### 第二轮原始记录

| 原始记录 | SHA256 |
| --- | --- |
| a1-autoplace-dryrun.log | cdf4ef6ffd200c8fa4ce506a4f3e667ccd6fc099ddb3e5310284a035203500b8 |
| c1-cleanup.log | 5c74ec0b421fd129df272f2345203f38c551090263141a67d642620789bfa7c3 |
| c2-readback.log | e1de500e028504cdb2ceaedf8e64bbdc26c853c237a2da2b9927c5d65eb50405 |
| e2-v121-playbook.log | 08b3998a20b4b6b6fee386f169a0a90e2ca60dfdfa8a8ce26f1c4ee44e89abbe |
| f1-apply-dryrun.log | 3572f25fe73ebe6365a80f9320ef36f4f1e2842d5b5f1b5f35d0efc4a8a7f04e |
| f1-apply.err | 282591648d97f382e7dd50480d5fa60f816085076de9eea54d8572f07a315b5c |
| f1-apply.json | 85dd20f957597cf467e8559ce29dc04c086e8dc391be58e37b8e1359bbd01bc9 |
| f1-before.err | e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 |
| f1-before.json | f0264aac366d60ba95ff3bfcd59dad4249996b18b1579ff086fe17c3680d1137 |
| f1-bind.json | c7c6470858ae9d73a64d545f90eb4e59d35bd9f15e135e0d2acbbd6c2d692c48 |
| f1-bind.json.journal.jsonl | 960ba0a6cb850cf08831bb9785ffa1e0348b99f5b40f53e683bc617167e92203 |
| f1-bind.log | 5eccfe23132daf6ab053f199504d0eee5acca72f4e66491d015d5d6fa6ffe28a |
| f1-import-changes.json | 3e0751fea71755a78883a2a51bffd3756917d7398a7d86c42b146382109937a3 |
| f1-lib-search.json | d5bf09ffdafd4491d5d086daff0f633a3b8de8f7914cd2eea3d782c50ccd741e |
| f1-modify-96d8631e3282ae30.json | 8f17448fdbecd06e44e95d8fdccab4f8816e9523d3c251fd207ac2e17d78db22 |
| f1-modify-b0e17397c946a228.json | 09dacc94b2370576cbbf7a0a0a75a294eda035c4e92917b7622085a0fa69157a |
| f1-modify-dcb5e565da4b3d32.json | b6d20343df4fa0da82efa9030f40451b829c0e89d1a74d50bfac952cd94af8e0 |
| f1-place-1.json | 0bdf8e198d75865e3a623a50f1143ebfff0b9a780e3a27ab832fc3bfae0a2b56 |
| f1-place-2.json | 3731909844102f4b4dfa5a193e0559c5d5e4bf38d6788a496defaf42f1040143 |
| f1-place-3.json | ea40ad9712ff1c5fbf4352f9ebf0717e39af9f9c412c1462ed61d9755ada0cf6 |
| f1-redo-delete.log | f4da38e760a7120c057ea3b975207fbb0157d1d09bb6b10e6c66d7539ef740d3 |
| f1-target.json | b28f1c189e202f2eea31b2df98883c571a62ebca9fc37edf2feee8039db7feaf |
| f2-after.json | b18e023dbfb7fbdab206d613f08dd6b5df2619c43221e3eb1176a9d969ceba0d |
| f2-reload.json | 9562a929be09b67072e04e4bc1b1ca8b5fb490ae70f3442a77d18d40b1f35d60 |
| m1-agent-check.log | 92141cc8857a7f8692f26f46c2b2787b2e364b3daa1c40afae08555f230b1e3e |
| m1-candidate.txt | 66937272059dad29243ad3cb6b3332fc2d98ee80beda19618430780a9a13e40c |
| m1-connector-test.log | 74e8b7b1299f923f05be86604acf3277151053b5e775293078b4f5bdcc059534 |
| m1-exits.log | 190241bfa0d60a513d3189f6772a598df93ed84f520853006441b6eb6d1e22cd |
| m1-go-test.exit | 9a271f2a916b0b6ee6cecb2426f0b3206ef074578be55d9bc94f6f3fe3ab86aa |
| m1-go-test.jsonl | f9be592eba0ce1f171e3030dee77eb1707ad9a7bad98ad51df7b02ff8eb05fff |
| m1-go-vet.log | e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 |
| m1-release-script-test.log | 88e3e7addeae925283136251c1c14a5bd2f149fbcc9000ebc46c9822825e66db |
| m1-skill-check.log | f3fc1c86dfdbc82e50438c685ba1794aa03f071d4826292e64342ac994c79ea7 |
| m1-tsc.log | 846868eca584eb84bc0ca893a4f49461560645a8a2805b5f845f7dc5d33e0aea |
| n1-refusal-project-apply.err | c87ad430583a4788c8bf5111fb8512908d016087792874b0a16b193fe5f7f24d |
| n1-refusal-project-apply.json | d3a1ddf2da02dec8a62e5643654d689b7b67d49ca8c376af32accfa6a5d90d08 |
| n1-refusal-project-dryrun.log | c87ad430583a4788c8bf5111fb8512908d016087792874b0a16b193fe5f7f24d |
| n1-refusal.log | 3ae2a211dfa7d8f373b08227cd9cd5b7ab0a8f620fcc2961468f97d76a615d0c |
| p1-bad-path.json | aab9a2669e6f10ff8180daa1e29ce0abe2e27c33c9451be9feca3ec84d7c62a0 |
| p1-bad-path.json.journal.jsonl | 4dd1cff9dafd7eb317f026d816c52e94866972102ec1cf50edb60adc165a7c39 |
| p1-bad-syntax.json | caa94640946fcbbab7cf2613a12f7b8e624b39cf0e159f248a741d9ebe6267e3 |
| p1-dryrun.log | 6306d0792188b61e40380b04c84af4635185629fb7c16b5734aa2221858ca0ba |
| p1-good.json | 2a6c01775027f8ca5cc128c8a94da9ba24b07e2e013d65b07bb2b641f98414f7 |
| p1-good.json.journal.jsonl | 9316b8b3d90a19b2bc5d541815986f63e6d8cf39e69c6afd242d1f4e3660cc6c |
| p1-run.log | 3c7da986b147349903b15f8e318a85f31ab0aa34cfe754f37f673164d32f45eb |
| r1-candidate-identity.log | d13aef23e117e8a5269452b7137720b1ab1509931e8e7fa187713dffcaca3430 |
| r1-identity.log | 55dddee9e32f7d5e5347080ebb6b3e5c2e2c9177a0169dfc549384e418f60b87 |
| s0-start.log | ec7ed161ef2d69077c1abb3fd10b60baa01591fce00c080e0bcd270f9a047f4e |
| s1-preview-layout.json | ef1b403439e9cb709e084afb8802c18aebd4f1e4e70d006df6c3292b8b72271c |
| s1-spread.json | 90d2983694497c357f1ad336fe50781af0b922fefcdaf8f471486314394fa6f2 |
| s2-preview-layout.json | 1396d9db42c49314c3c8d7d456bc481463b68239dcb2b3d42d75d4bb5c83a007 |
| s2-spread.json | 90d2983694497c357f1ad336fe50781af0b922fefcdaf8f471486314394fa6f2 |

### 第一轮原始记录

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

### 第二轮

宿主为 EdaDesk DEV 中的 EasyEDA Pro 4.1.105，工程 `ceshi`，daemon 端点 `127.0.0.1:60833`，daemon 与连接器 `1.22.0-dev.10`，
窗口 `1ad62c3a…`。全部写入由执行 Agent 用运行时 CLI 经 Cobra 子命令或 `easyeda call` 的类型化动作下发；一键布局由 Desk 调试通道
触发，没有 GUI 编辑，没有 `exec_js`（执行者观察；命令行写在各日志开头或可由报告复现，不是完整流水）。

- F1/F2 以 apply 回执、verify 结果、`doc reload` 回执与重载后的 `sch list` 新回读为准。
- 起点与终点：测试前 `s0-start.log`，清理并重载后 `c2-readback.log`；PCB1 语义哈希测试前后均为 `a84bfd08…`，是“回到起点”的直接证据。
- 首次放置三单元的偏差（都成了 .1）与修正如实记录在 F1；多出的两个 .1 在 import 之前按 id 删除，没有进入 PCB。
- 测试对象（U90 三单元及其 PCB 封装、P1 的临时文字）均已删除，两份文档按目标保存重载并新回读（L1）。
- `f1-before.json` 等读取由 CLI 直接输出 JSON；`f1-place-*`、`f1-modify-*`、`f2-reload.json` 首行是检查点提示，`f1-import-changes.json`
  末行是 `staleRisk` 提示，都不是纯 JSON；`n1-refusal.log` 末段与 `c2-readback.log` 的统计是脚本对原始响应的摘要。
- 首次放置的两个多余单元是 .1 来自执行者在控制台看到的输出（该两次输出被覆盖）；`f1-redo-delete.log` 只记录了被删的两个 id。
- 一键布局经 Desk 调试通道 `preview:layout`、`layout:simulate` 触发，不是 Cobra 子命令，只能在 EdaDesk DEV 中复现；它不是 `exec_js`，也不是 GUI 编辑。
- 两轮之间 PCB1 的 17 个器件位姿不同：11-27 的一键布局现场验证在第一轮之后应用了 17 项移动（EdaDesk 验证记录），本证据目录没有该次日志。

### 第一轮

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

### 第二轮

独立 Agent（offload 新会话 `046302f1`，Claude Sonnet 5.5）于 2026-11-28 只读复核第二轮：未改仓库文件，未访问 daemon 或 EDA，
未重跑测试，最终结论 **pass**。

- 已独立复算并核对：第二轮 SHA256 表 52 条原始记录全部一致（顺带复核第一轮 39 条哈希一致）；M1 的 go test 事件（顶层 3219、
  子测试 3235，共 6454 pass；16 个包 pass；1 个平台限定 skip 与 2 个无测试文件的包；0 失败）、连接器 1432 项与其余退出码，
  `node_modules` 链接目标与任务 worktree 的 `package.json`/lock 字节一致；F1/F2/N1/P1/E2/S1/A1/R1 各行的 id、计数、状态、错误文字、
  指标与检查点均与原始记录一致；`s1-spread.json` 与 `s2-spread.json` 字节相同；清理前后 17 个器件位姿一致；已安装运行时的 CLI、
  连接器与 `skills.tar.gz` 哈希与基准一致。
- 沿用第一轮 F3、F4、E1 的依据：`d6efaf0c..8594aa0e` 在 `extension/src`、`cmd`、`pkg`（及 `go.mod`、`Makefile`、`scripts`）无改动，
  `internal/app` 的 11 个改动文件不被 autoroute（`cmd_pcb_autoroute_official.go`）与 selection（`cmd_selection.go`）引用；这是静态论证，
  发布二进制本身没有重跑这三个用例，报告已如实写明。
- 用例 ID 两份文件一致，`release-check.py` 的 `basic_scope` 与 `check_case_results` 在当前文件上通过；公开证据文件不含本机路径或凭据。

首轮结论 `fail`，阻塞项一条：L1 写了一个原始记录中不存在的 PCB 封装 id。已改为说明该 id 取自未留存的读取，复核确认后结论改为 pass。
同时采纳的非阻塞意见：Changelog 验证段补全仅自动测试项（含一键布局的改进路径）；S1 注明板子已在局部最优、只覆盖无改进路径；
N1 首次拒绝的退出码只见于控制台；M1 第一轮的重跑经过；现场回读补充日志格式、调试通道入口与两轮间位姿差异；manifest 的
范围决定补记 2026-11-28；删除误建的 `extension/node_modules` 链接。

未能独立确认（如实保留）：PCB 封装 id 的来源；用户在原生确认中批准安装与调试通道校验本地包；Desk 修订 `d9d70b97`（只见
userAgent 中的 0.2.3）；无 GUI 编辑、无 `exec_js`；11-27 一键布局应用的日志；第一轮原始记录内容（只核哈希）；Changelog 中射频规则与
评估预算等功能描述超出 `--help` 的部分。`release-check`、`release-build` 与发布后的远端资产核验属于发布阶段，不在本证据内。

### 第一轮

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
