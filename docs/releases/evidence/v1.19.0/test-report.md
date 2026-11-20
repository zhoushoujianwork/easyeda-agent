# v1.19.0 定向修复验收报告

日期：2026-11-20。范围由本版用户明确决定，详见 baseline.md。结论只覆盖下列实际完成的自动回归、整页原理图链路与 keepProperties 替换的 DEV 现场验收、v1.18.2 负对照及所引用的步骤级回退记录；完整设计 E2E 未执行。

## 用例结果

| ID | 结果 | 实际证据与边界 |
| --- | --- | --- |
| M1 | pass | 在 6d4f1947 上运行：Go 原始 JSON 日志统计 6197 项通过、1 项跳过、0 失败（16 个测试包通过，2 个包无测试）；go vet 通过。连接器 1407 项全部通过，tsc typecheck 无错误。make agent-check 13 项、skill-check（192 个已跟踪文件，链接不越界）、release-script-test 136 项全部通过，退出码均为 0。 |
| F1 | pass | H1 换件后缺稳定 ID，先执行绑定计划 ec0439e1：verify-before、bind-001、verify-after、save 四步全部 ok，写回 cmp-H1。随后最终整页计划 f20955e5 的受保护队列 45 步全部 ok，包括 preflight-lib-000/001、verify-all-pins-nets-nc、electrical-check、strict-schematic-gate、save-composition、verify-saved-instance-preservation。Desk 返回 applied、saved:true、verified:true。保存重载后新回读：VCC = H1:1、R10:1；LED_A = R10:2、U4:2；GND = H1:2、U4:1、R102:2；匿名网 R101:2—R102:1 只用导线（平台先后重编号为 $2N14/$2N19/$2N24，分组核对均通过）；R101:1 为 NC。另行只读运行 sch gate --strict --drc-page，结论为 pass。 |
| F2 | pass | dev.12 上带 keepProperties 依次把 H1 换成 TS-102-T-J（C20381557）和 TS-102-G-L-2（C20381547），两次都保留 cmp-H1，LCSC Part Name 与 Datasheet 均为目标器件自己的值。修复前对照：dev.10 报 property otherProperty was not restored 并回滚；dev.11 能替换，但保留了旧器件的 Datasheet 与 LCSC Part Name。第一次替换后旧导线穿过新器件本体，Desk 报出 SCHEMATIC_GEOMETRY_INVALID 及导线明细，不再报"结束状态未知"。 |
| E1 | pass | 用 v1.18.2 标签构建的 CLI 对同一页只读运行 sch gate --strict：被 P1 的 6 条 warn 级原生 DRC 挡住（verdict fail）；新版加 --drc-page 后为 pass。用 v1.18.2 对 F1 最终执行的 composition 运行 sch compose：报 R101.2 does not reach a named wire tree，退出码 1。新版在同一输入上生成的 playbook（e1-new-playbook.json）与现场执行的 queue.json 逐字节相同，SHA256 均为 e0bdbe91…。 |
| L1 | pass | 现场每次写原理图页前，stderr 均出现 checkpoint 记录（delta 或 same）。步骤级回退引用 edadesk#17 的 2026-11-18 DEV 现场验证：Auto 下 checkpoint:restore 返回 restored/verified，dry-run 返回 alreadyAtCheckpoint，重复回退不写入。该记录当时的 CLI 为 v1.18.1-dev.2，检查点代码（32949f68、6f44d961）自那以后未改动；本轮未新增回退写入。 |
| N1 | pass | 只读运行 sch group create --check：不带 --replace-overlapping 时报 H1 already belongs to group g3，退出码 1；带该参数时报告 would release g3 并注明 nothing saved，退出码 0；前后分组表一致。现场两次中途失败的队列（electrical-check、strict-schematic-gate）都在 save-composition 之前停止，Desk 按 journal 报告"失败前没有保存"，与 journal 一致。 |
| R1 | pass | build-local-snapshot.py 从 842d3c6c、3f6d4fe0、07794bc6、eea36e96、63f6d662、47bfcc7b、6d4f1947 分别构建 dev.6–dev.12，sourceDirty 均为 false。每个都经 edadesk Dev 内人工确认的升级弹窗安装。health 显示 CLI、daemon、连接器均为 1.18.3-dev.12；dev.12 的 sourceRevision 即本版基准 6d4f1947。 |
| E2E | not-run | 本版用户在被告知完整点灯板 E2E 未执行后，选择定向修复范围；完整设计及其 DRC、保存重载、新回读延期至 follow-up，不沿用旧版本的现场结论。 |

Go 的 1 项跳过是本机不具备 Windows PowerShell 5.1 的平台限定测试，不计为通过。EDAD-7 的 writeStall 与 pcb rip-up 两项修复只有自动测试，本版没有现场复验。

## 可追溯证据

源码基准 `6d4f1947a48fc6ded68a354fc46f2d5b083cdb47`。原始记录留在本地忽略的 `artifacts/v1.19.0-logs/`，公开包不含个人路径、账号或私有原始数据。

| 原始记录 | SHA256 |
| --- | --- |
| go-test.jsonl | 34c50ab1e91bd0ca53fd4aebdaf2465c463661c1e38ad5a7cc87d5df4cf49eb0 |
| connector-test.log | d49b2463c8efd4bae06f0b2e7338a9d445de292b201e8271f3af373b05c1040d |
| repo-checks.log | 70151602a85237cffabb1a8c4b66161e099f0258d471ebc701d764fe67773237 |
| tsc.log | 846868eca584eb84bc0ca893a4f49461560645a8a2805b5f845f7dc5d33e0aea |
| summary.txt | 9a4dcacd308aa7420c4b60e7fd95cba46192ad837dbac407c75a84eb5694e950 |
| f1-final-journal.jsonl | 4bdb7cf62388f199796fef1ff39b8f813869216ac6b3b0abcc61cad3ebe8e89d |
| f1-final-queue.json | e0bdbe91616504660315de966a95d5bf54cd4bc0e2fc2f1f2e699bd2fba05c69 |
| f1-bind-journal.jsonl | 6ad2cbfb31b6e2b774295e854601bd4664f63dc73a0935b9d0b7ecae68f149eb |
| f1-groups-journal.jsonl | 050aa0839b3c1025ef1c15cd0700b72e2527444e56890bab2641e1a366b95f8d |
| f1-precheck-gate-journal.jsonl | 89ca66f81a3e4f4ac3bba2dc426f61e034e5c37a2864dd4dca3c069753f416a7 |
| e1-old-gate.json | 8efcdc5694014fb9f583c4a38dcad9f399bfff346426ad84727beec4551769f8 |
| e1-new-gate.json | 735fb27c91024b6478aa55f89ba313006c316f586f67ace5696d5229831db767 |
| e1-old-gate.err | 6534f829565d2dddf0268b6253e473457b17dc398bcf1c4e2cf6ce1a94a7c11f |
| e1-new-playbook.json | e0bdbe91616504660315de966a95d5bf54cd4bc0e2fc2f1f2e699bd2fba05c69 |
| e1-old-compose.log | 8459f885abbad296c4b0ea97481c534202409823dc8d023fb7c6f7c8ffa7615a |
| e1-new-compose.log | 26a1040d995129820aeca1e52648c2d5265b2ec745fff3a3e81fce74279c5a95 |
| f2-before-fix-dev10-turn.jsonl | 29482a501e1d47baf2d4f991827a058d808c2bed0e9bc7b1bded2e45e96e6049 |
| f2-dev11-turn.jsonl | 542eaf12bb105dd4a86084695aa119954dbb84c6e61e110b5390b1a7f0641b9e |
| f2-dev12-turn.jsonl | 6b696dd432aed4c85f4b33512df6e888fa3997eeb45532f14922bf4a13e550ba |
| n1-group-check.log | 50c42577f9d7aa7493f7bf3af55dec3fd5609e2499e3f578ebc94515627bf4a1 |
| r1-health-dev12.json | 1655cb3634b07f3560ff32dd02cca8e4d1583d5a8b9e8a113271f2354b252b1a |
| r1-local-dev12.json | adcbf2355e33349fb378b226c6d7aced89bc376a42f0be11c35a54a6cf5f35e7 |
| r1-checksums-dev12.txt | ae2ecf91d72acce44e7dbb73dc661c00521580f1c626ea4c275d1dae93168153 |

## 现场回读

宿主为 edadesk DEV 中的 EasyEDA Pro，测试工程 `ceshi`，页面 `8fb55bd61d0986a9`。设计写入只经 Desk 内置 AI（Codex，Auto 模式）走受保护流程；只读核对用 DEV CLI，端口限 60833。

- F1 的最终回读取自 save-composition 之后的 verify-saved-instance-preservation，以及 Desk 保存重载后的独立 inspect。我方另行只读运行严格门禁（--drc-page）与 components.list 复核，结论一致。
- 页名由 P2 改为“LED 指示”：用户同意后用 DEV CLI 直接执行 `sch page-rename`（Desk 未向内置 AI 开放改页名，见 edadesk#88）。图签 Name、Description 越界经 `schematic.attribute.move`（ownerType:sheet）移回纸内，官方导出图确认文字在格内。这两步属于测试前置，不是本版判定项。
- 过程中保留的失败（不改判）：
  - inspect 在升级和改页名后各有一次报"文档源不可用"，重试恢复；edadesk 217cc9e 已让该报错写明原因。
  - 一次 Codex 到 Desk MCP 的传输错误，请求没有到达，journal 为空。
  - 一次未加载本轮规范被 Desk 拒绝。
  - 替换 210S-1*2P 前，因库记录缺封装 UUID 与引脚而主动放弃。
- H1 新封装 HDR-TH_2P-P2.54-V-M-3 的焊盘数与间距未能从库读取，按封装描述（直插单排 2P、2.54 mm）记为未核实。

## 独立复核

独立 Agent 在 2026-11-20 只读复核了本版基准、用例、报告与 `artifacts/v1.19.0-logs/` 的原始记录：未改文件，未访问 EDA 或 daemon，未重跑测试。复核内容与结论如下。

- 逐一核对报告表中 20 份原始记录的 SHA256，全部一致。
- 从原始日志重新统计 M1：Go 6197 通过、1 跳过、0 失败（16 个包通过）；连接器 1407 项全部通过；agent-check 13 项、skill-check 192 个文件、release-script-test 136 项全部通过；各退出码均为 0。
- F1 最终 journal 共 45 步全部 ok，其 playbookSha256 等于 queue.json 的 SHA，步骤 ID 与队列逐一对应。
- F2 三个阶段的现象均在对应日志中找到：dev.10 回滚、dev.11 保留旧字段、dev.12 字段归属正确。
- E1 旧版门禁的 6 条告警全部在另一页 6abf3fbf13dc52f0；旧版 compose 拒绝匿名网。
- N1 拒绝发生在写入前，分组表不变；两个中途失败的队列都停在 save-composition 之前。
- R1 中 dev.12 的 sourceRevision 等于本版基准，sourceDirty 为 false。
- 源码：v1.18.2..6d4f1947 共 37 个提交；extension/src 有改动（keepProperties、doc source restore、pcb rip-up），须升中版本。EDAD-7 与 rip-up 无现场日志，"仅自动测试"的表述属实。检查点相关文件自 6f44d961 起没有提交，也没有差异；edadesk#17 当时 CLI（v1.18.1-dev.2）的确切构建修订无法从 git 确认，复核只能确认"6f44d961 之后未变"。
- 版本元数据（extension.json、package.json、lock 的根与 packages[""]、Skill）均为 1.19.0。

复核提出的非阻塞问题已处理：
- 补入绑定计划 journal（f1-bind-journal.jsonl）；
- 补入新版 playbook（e1-new-playbook.json，与执行队列逐字节相同）；
- 补入 e1-old-gate.err 的哈希。

另有两点限制如实保留：dev.6–dev.11 只留存了提交记录，构建是否干净以本报告的记载为准；health 只显示 daemon 与连接器版本，不单列 CLI。

独立复核结论为 pass，仅适用于 v1.19.0 声明的 cli-fixes 定向范围。完整设计 E2E 仍为 not-run。
