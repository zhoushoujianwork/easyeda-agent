[返回当前执行报告](test-report.md)。以下保存整理前的各批执行、失败、修复及有限复核记录；当前状态以主报告为准。

最新三页 F1/F2/E1、图签 L2 及过期 sourceScene N1 的有限独立结论与全部差分边界见
[原理图续测记录](../../../reviews/2026-09-27-cli-schematic-gate.md#最新有限独立结论)。
其后 R1 与四层 PCB 仍在续测；以下历史文本中的失败和当时状态不覆盖当前主报告，也不被改判。

# v1.8.0 执行报告（A01 修复续测）

[发布主文档](../../release-1.8.md) · [基准](baseline.md) · [用例](test-cases.md)

2026-09-27 启动发布准备。**用户已移除三项整体阻断；A01 初测失败后的通用修复及离线复测通过，现场续测中，result=in-progress，尚未发布。**
初测包为 `1.7.1-dev.11`；布局修复 CLI/daemon 为 `v1.7.0-31-g8c4d3e4`，截断修复后固定为
`v1.7.0-33-gd3409a0`；connector 仍为
`1.7.1-dev.11`。它们不是已经通过完整验收的 v1.8.0 正式版本。

## 现场回读

最初执行串行只读预检：

```bash
python3 scripts/cli-live-smoke.py --expected-version v1.7.1-dev.11 \
  --window be65ae0d-4680-41f1-b94d-681fd7eb653b \
  --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b \
  --type schematic --out artifacts/release-v1.8.0-preflight-20260927/readonly-smoke
```

结果 pass、exit 0：版本、前后 health、action 目录、工程信息、页面路由、原理图列表均成功，
前后仍为同一窗口/工程/文档。七条命令的 UTC、耗时、stdout/stderr 和退出码已保存。
`summary.json` SHA-256：`62bed01ede7caae9698e15fcfe1ec81eaa454b8661574069d4fc8c0c7dff7ed3`。
此项不写设计对象，不执行保存重载，不覆盖 PCB，也不等于 B00–B10 或完整用例通过。

## 原始需求与 A00/S0 进展

新上下文执行员仅接收客户第一节、公开 Skill、目标环境及“复用＋A”与三项范围决定；
未读取第二节 runbook、旧 BOM/网表/布局/候选。冻结的 996 字节输入哈希与基准完全相同。
本轮独立形成简版 S0、详细依据、器件候选和测量计划，均为 candidate-unverified；
具体器件身份、参数、引脚和封装仍须在当前宿主核验，不能将候选当定版 BOM。

双页只读原始基线、空板/无板框诊断及覆盖缺口见[基准](baseline.md#环境与起点)。
本地 `artifacts/release-v1.8.0-e2e-20260927/a00-s0-executor/` 的 39 份清单已冻结；
下一批使用独立 `a00-selection-measurement/` 目录：双页 typed save 与原生导出已成功，
102,943 字节归档通过 ZIP 6 条目校验，哈希见基准；`restoreVerified=false`。
WCH V3C 完整手册已获取；六件关键器件已完成当前库身份、真实 pins/bbox/临时位号与绑定封装测量，
逐件精确删除，最终空页保存重载恢复；PCB 184 条原生记录不变。该批 161 份证据及清理闭环经独立复核。
临时位号改变后仍须测量最终文字 bbox；绑定封装不是 PCB 实例测量，原生归档未重导入。
这些不证明完整设计 Apply、DRC 或全部 BOM 测量已通过；其余选型、测量和 canonical 源数据随后在独立批次生成，结果见后文。

`4816e57` 将本轮实测的 ESP32-WROOM-32E-N4、具名 HRO USB-C 及 ST C7519 身份沉淀到公共器件库，
与 UMW C2687116 分开维护；块库测试、1,028 引脚审计、Skill 检查和冻结身份对账通过。
六件早期 uniqueId 与后续 fresh 值的时间差、两次包装器事故均保留，不补签首次全链成功或 R1。

本轮选型核对发现公共 `block.esp32_autodownload` 的说明真值表将两个非对称输入态写反，
实际 `internal_nets` 拓扑与官方一致。`55284b5` 修正 notes、测试文案与公开 Skill，保留原始回包；
验证为全套 `go test ./...`、`make blocks-audit`（1,028 引脚引用）、`make skill-check`、diff 检查通过。
这是知识说明修复；现场 dev.11 二进制未换，不能称为新版本已验收。

独立候选复核又发现公共 ESD 条目按同名 ST 手册说明 C2687116，实际该 C 号厂商为 UMW。
`702b6bb` 补准确制造商与对应 UMW 手册，并按测试条件列明典型/最大电容，修正块的来源说明；
器件 UUID、C 号和连接拓扑未变。块库测试、Skill 检查及 1,028 引脚审计通过。
原候选与 ST 文档仍冻结保留；执行员在下一批自主选择与 ST 手册匹配的 C7519，另行实测身份。

## 前置失败与当前判定

- #55：实际页面改名仍被官方接口拒绝；失败退出修复已通过，实际改名没有通过证据。
- #260：区域/铺铜边界请求 1 mil 读回 0.2 mil；#261：铺铜 priority 2 读回 1。
  dev.11 正确检测两次不符，成功创建分支仍未覆盖，实际字段问题未解决。
- 当前源码相对安装实现没有这三项的代码变化，没有重新写入来重复取得同一失败；上述是沿用的未解决问题，
  不是本轮新复现。详见[已知问题](../../../cli-known-bugs.md)与其原始证据。
- 最近完整基础 dev.9 是 9 pass / 2 fail；dev.11 完整基础未运行。新的最终候选尚未冻结，
  本轮 B00–B10 全集仍为 not-run；dev.11 初测 A01 failed、A02–A06 not-run。旧 A00 blocked 记录继续保留，不补签或覆盖；修复续测进展见后文。
- 用户随后明确移除相关限制，已在[基准范围](baseline.md#2026-09-27-用户接受的范围)记录。
  三项不再阻断高级准入或发布；采用已有基础证据及当前目标/完整基线核对继续，
  不要求先修三项或再跑基础全集。完整用例和独立复核尚未完成，不能把准入决定当验收通过。

| ID | 结果 | 证据与未执行原因 |
|---|---|---|
| M1 | pass | 原 50 件与新 NT L1 的身份/引脚/bbox/最终位号、精确清理和保存重载均通过独立复核，最终 51 位号测量齐；旧 MT 及 744 文件批保持不变，不签 PCB 实例或电气设计 |
| F1 | in-progress | 十区规划、三页真实 Compose/dry-run 通过；P1 前 272 步后因 #263 回读截断失败，已保存部分候选，修复恢复中 |
| F2 | failed | 原 P1 截断失败保留；d3409a0 后 P1 保存重载/电气通过但图签越框；P2 strict gate 因 clusters 误报与 SDK 2 warn 失败，P3 未运行 |
| E1 | not-run | 本轮尚无实际完整电路，无法按 fresh 器件与导线判定供电、下载、按键和点灯路径 |
| L1 | not-run | 尚无本轮通过并落盘的完整基线，未执行核心及专属外围整体移动与范围外不变验收 |
| L2 | not-run | 尚未执行本轮完整设计 Apply 和现场局部检查，不能提前声称没有适用缺陷或判为 not-applicable |
| N1 | not-run | 已有参数源但尚无本轮完整设计的持久化基线，未执行过期状态、缺归属及局部受阻负例和完整前后回读 |
| R1 | not-run | 尚无本轮合法候选和局部批次，未测试同源可重现性、受控中断、重算恢复与清理持久化 |
| E2E | in-progress | 已继续完整源离线与现场准备，实际电路 Apply、四层 PCB、布局布线、DRC 和成品保存重载尚未完成；#262 保持 open，不作为本次三项例外 |

## 独立复核

独立 Agent 已核对准备材料、七条原始命令、目标 context、精确版本、证据清单与材料哈希、
schema 1 必需用例及“复用＋A”的范围。最初发现原始需求截取规则的末尾 LF 描述有歧义，
已明确字节切片及 996 字节长度，重新计算后与原哈希一致；无未关闭 finding。

独立报告保存在本地 `artifacts/release-v1.8.0-preflight-20260927/independent-review.md`，
SHA-256 为 `7366e9b80f8f63dbecd8bf9f4af25aaaaf30ca20572e3ea9c4c8536a451fe7fa`。
其初始输入哈希及修订后补核都保留；本段在报告冻结后追加，随后更新本目录 manifest 哈希。
**该复核只证明当时准备材料与只读记录准确，不是完整现场验收**；它发生于用户接受三项限制之前。
范围决定后整体改为 in-progress，independentReview 仍为 not-run；不得将旧文档复核外推为新范围或现场通过。

范围变更另经独立只读复核，无待修正 finding：确认只移除三项整体阻断与先修/重跑全集前置，
保留原 fail/not-run、实际铜/电气/几何/持久化及完整用例要求，运行时代码和 release-check 未改。
报告 `artifacts/release-v1.8.0-preflight-20260927/accepted-limitations-review.md`，SHA-256
`98c95bfda4d7acd7b2f58edbe7e2ce8e0af5725c4582e1816ba05e6060ae378d`。
该报告绑定本段追加前的 20 文件 diff；本段追加后同步 manifest 哈希，不作为完整现场独立复核通过。

A00/S0 另经独立只读复核：39 份材料长度/哈希、原始输入、typed 回包与候选边界一致，
可继续选型测量；C2687116 与 ST 手册错配 finding 已由 `702b6bb` 纠正，原始候选不改写。
报告 `artifacts/release-v1.8.0-e2e-20260927/a00-s0-review.md`，SHA-256
`7fe87104ebca3a9876681889600b3868ef7a6da0ee7c8f490fa259755ec15bc2`。
该复核覆盖 S0 和两项知识修正，未复核后续测量/备份闭环，不是完整现场验收；
manifest 的 independentReview 仍为 not-run。本段在报告冻结后追加并同步材料哈希。

六件测量另经独立只读复核，结论仅为 M1 subset 证据通过：161 份材料哈希匹配，
身份、原始几何、六次精确清理及最终完整基线比较一致；856 项为离线证据检查数，不是新增现场用例。
报告 `artifacts/release-v1.8.0-e2e-20260927/a00-measurement-review.md`，SHA-256
`9d316245a8a028e5bd90dfbfd0229de5b8e09b3f15d3c6cdd6a126c34f3b9c8d`；
独立输入/重算记录 `a00-measurement-review-inputs.json`，SHA-256
`f21c708ac1a55231794ee2ce049855c58128c88e2efd08253da1711e57cad137`。
完整 M1、最终电气方案、R1 与最终验收未签署；manifest 的 independentReview 继续为 not-run。

后续 51 个最终位号的静态身份链另经独立核对，29 种器件身份一致；报告
`a00-final-selection-review.md` SHA-256 `5eb7611079753d6a7066e60762364fb6fd2df35d7b60b6f88879c0e6d32196e6`。
完整测量子批 744 文件哈希匹配，51 件真实 pins/bbox/Designator、精确删除和最终完整基线恢复通过，
51 份测量归档及最终归档的目标 PCB 184 原生记录全部与测前相等，`restoreVerified=false` 保留。
报告 `a00-final-measurement-review.md` SHA-256 `bed719ad47cd3c107568a2ee69b8bfbc81975e8b677e703852312996a0c7633e`；
输入/独立检查记录 SHA-256 `33bdd9305ca8529858b9423f596d8d332124ef42459d2fc2d75de683ddf45e01`。
这些报告位于 `artifacts/release-v1.8.0-e2e-20260927/`，只签相应身份/测量，不签完整设计或发布。

L1 因 MT/NT 手册冲突修订为 `SWPA4030S2R2NT / C279948`，另行完成真实测量及精确清理；
旧 MT 的测量与失败布局输入保持不变。独立核查确认新 L1 两引脚/两原生焊盘、最终位号及清理链一致，
最终 112 全页只差原图框更新时间，113 归档的 PCB 184 原生记录与起点逐条相等。
最终 51 位号 M1 已齐；`restoreVerified=false`、电气未验及 A01 失败仍保留。
`450e40f` 将精确 NT 身份与 MT 资料缺口沉淀到公共库和 Skill，块库测试、Skill 检查通过。
报告 `artifacts/release-v1.8.0-e2e-20260927/a00-inductor-revision-review.md` SHA-256
`b9d8bef1a454a336e2c19ddef503747ed87f6997aa5b5fbd0ae9efb1149839a4`；
输入/独立检查记录 SHA-256 `a8209e1c86e87b28c23e97e8af157eefc3ae8ebf12cc9a1f90ddd8799af7c3c2`。
最终 924 件清单逐件核对通过；这项复核只补齐 M1，不改变完整发布复核的 not-run。

## A01 失败与已登记问题

本轮完整源固定 0°/20,000 候选及允许 R/C/L 正交旋转/100,000 候选均失败。
十区独立重放复现 usb/mux/uart/boot/mcu 五区 failed、其余五区 planned；
后者仅代表离线候选生成，不代表电气或现场通过。根侧按引脚朝向计算的候选姿态亦未解开 USB 区。
未证明无解或单一根因，没有修改求解器、网策略、几何检测、失败退出或用 GUI 试摆。
可复现输入和完整范围见[布局回归记录](../../../reviews/2026-09-27-cli-layout-regression.md)，
已登记 [#262](https://github.com/zhoushoujianwork/easyeda-agent/issues/262) 保持 open 后续修复。

失败材料另经独立复核，十份公开夹具与原输入逐字节相等，重放状态与命令退出码一致；
报告 `artifacts/release-v1.8.0-e2e-20260927/layout-failure-review.md` SHA-256
`0abfb10fbd0d731f3ec9bc31423b5be0afa52c629ad83e1edea96de7086ee4fe`。
该复核允许提交真实失败材料，不代表问题已修复或完整发布复核通过；本段在报告冻结后补入。

## 下一步

`8c4d3e4` 已修复命名出口、姿态窗口和指定 attachment 物理连线，十区冻结输入全部离线 planned；
全量 Go 测试、Skill 检查及独立代码/十区复核通过，原始失败保留。完整 NT 源另用本轮自主来源
生成三张 A4 页及固定 Compose 离线预检，158 connected / 37 NC、15 direct 网与 41 对 attachment
保持；12 份设计产物重复字节一致（不包含含耗时字段的诊断报告）。
本地 `a01-offline-continuation-20260927/artifact-manifest.json` 冻结 75 文件，SHA-256
`69f9277bb1eed70f0fcfa89328299552c586d84985f25bbcaed6eeb8e08836af`。
完整源 SHA-256 `eec7459db935da9e04548bd84770f042e26d4006a04f5870d08f06564efb649f`，
离线二进制 SHA-256 `b9bb3b94b48c49b252ce5f7e3d4f059f20363ba99eb755b0b75ac7917305db17`。
该批页2/3为显式占位，不是实际UUID，无 EDA 调用或可执行队列；完整离线链另经独立复核通过。
独立报告 `artifacts/release-v1.8.0-e2e-20260927/a01-offline-full-source-review.md` SHA-256
`8d9583ac5f030e932c272fb9da86ebfd57ec42ecaed5d1ed53763441a25634b5`；
输入清单 SHA-256 `ea83f4797f0549a0d37d79a8e9fb53f11adc4b724365265e67b88b8d92debc74`。
这只签离线范围，manifest 的完整现场 independentReview 仍为 not-run。

现场续测已用提交版本重放相同完整源，并补测 R5/R6 270°、C9 90° 的 body/pins/Designator。
符号几何一致，位号却由宿主保持横向并迁移；三件已精确删除、save→reload→fresh read 清理。
新源必须采用实际姿态文字再重算，不能将旧刚体文字推算签为现场通过。详情见[布局回归记录](../../../reviews/2026-09-27-cli-layout-regression.md)。
随后接续真实页身份、受保护 Compose/Apply 和完整 PCB 用例。
实际续测在 P1 前 272 步写入后因 [#263](https://github.com/zhoushoujianwork/easyeda-agent/issues/263) 响应静默截断失败；
部分候选已 typed save/原生导出，P2/P3 仅图框，PCB 未改，157 文件批次冻结。
公共读取修复后，完整现场只读采集为 1,388,244 字节、exit 0；全量 Go 与 Skill 检查通过。
这不是整页网络或持久化验收，需重新采集/编译受保护计划恢复；F2 保留 failed，其他未运行项不补签。
实际版本、错误和哈希见[截断记录](../../../reviews/2026-09-27-cli-response-truncation.md)。
`d3409a0` 响应修复另经独立离线代码/测试及单次完整只读回包复核，无阻塞 finding；
报告 SHA-256 `418a75479ab7849db6272e8a8ba16ffb9687ecf6360e1b154828e20c827edab9`，
输入记录 SHA-256 `9c38b973aa4fd3807787f91fc43a262890a7c991c825036e212095594406fa2c`。
其范围不含现场恢复、整页网络或保存重载，manifest 的完整 independentReview 仍为 not-run。
随后 P1 完成 15/15 恢复步骤、保存重载与完整回读/电气对账，但官方图中存在 #264 图签文字越框；
P2 前 161 步后 strict gate 又因 #265 clusters 误报和 SDK 2 warn 失败，已保存/导出部分候选，P3/PCB 停止。
本批冻结 87 文件，清单 SHA-256 `e27141e9cd730372fb133a7334ebed4d95bc07b15ca669f0d0e3ef69e37996c2`。
独立电气源预审通过仅为静态一致性，不签 E1；细节及哈希见[新增严格检查失败](../../../reviews/2026-09-27-cli-schematic-gate.md)。
F2 继续 failed，新问题不在三项例外内，完整现场 independentReview 仍 not-run。
响应读取 #263 的有限恢复独立复核随后通过 658 项，87 文件哈希全部匹配；只签明确保存重载后
P1 对象/pin/线树符合目标。宿主属性 ID 及隐藏 marker pin 属性变化保留，不宣称全 result 相等。
报告 SHA-256 `ee18a61c34fc068c6d65ba5aba552f944e13ec2728710111915e4575ac000489`，
输入 SHA-256 `cb1daea98948564100119930b164f9c5f499d07c0f64855123f00da9efe92d79`。
#263 以采用提交 d3409a0 关闭，F2/E1/SDK DRC/E2E 与完整独立复核状态不因此改变。
原始各批冻结材料保持不变，三项已接受问题继续按原范围跟进；本轮不创建发布 tag 或 Release。
新请求失败时保留实际证据，停止依赖步骤，不静默改参、用 GUI 兜底或冒充发布验收通过。
本轮以“A01 blocked #262”的失败尝试登记成本台账，没有标为 E2E 通过。
UTC 2026-09-26 请求统计区间为 16:32:57–17:38:09；审计实际首末动作 16:33:09–17:34:04，
该动作窗口约 60.91 分钟，daemon 聚合 5.35 分钟、差值 55.57 分钟；差值含思考、离线求解与资料核对，
不当作纯模型耗时。1,937 个 daemon 调用的 failure=0 不包含 CLI 本地拒绝或离线布局失败。
token 未记录，JSON 中 tokens=0 不代表无消耗；原包见本地 `artifacts/release-v1.8.0-e2e-20260927/cost-attempt.json`。

合并修复后固定运行 `v1.7.0-41-g2d041b5`，4027 项 Go 测试通过、0 失败、1 跳过，Skill 与独立代码复核通过。
P2 真实 strict clusters 定向复测 exit 0；全工程网表仅 UART 两网单脚，P1/P2 SDK strict 仍各报两条无明细警告。
P3 已声明的图签显隐保存重载通过；默认 null 显隐的 text-only 分支另发现宿主异常，保留为 #264 未关闭边界。
三页恢复顺序调整为 P3→P1→P2，未降低 strict；本段不改变 F2/E1/E2E 与完整独立复核的未通过状态。
细节见[定向复测](../../../reviews/2026-09-27-cli-schematic-gate.md#已提交版本的现场定向复测)。

P3 第 21 步又因 [#266](https://github.com/zhoushoujianwork/easyeda-agent/issues/266) 导线写后暂不可读停止，
后续 fresh 已看到同一实际线段；六件/一条线已保存，PCB 不变，不重复写线或补签失败。
新批 44 文件清单 SHA-256 `818e955feec36f15bc27c50d1e39ac51501b082da4ac2033b80a0b243507766d`。
`37449f2` 图签未知显隐写前拒绝、`8877942` 有界只读发布等待已通过独立离线复核，分别 83/58 项；
最终全量 Go 4075 通过、0 失败、1 跳过，Skill/diff 通过。P3 官方图仍有作者额外属性值，需修源后重新生成计划验证。
现场恢复、三页原理图、四层 PCB 和完整独立复核仍待执行；F2/E1/E2E 及 manifest 未通过状态不变。
修订边界、实际失败、最终源和独立哈希见[新修订](../../../reviews/2026-09-27-cli-schematic-gate.md#p3-导线发布失败与新修订)。

## 已知问题页整理前的历史描述

以下为 f29717f 中已知问题页的原段落，保留当时各次失败/等待状态；当前状态见主报告与 CLI Status，不由本段覆盖。

## 高级流程新增问题

[#262：完整用例五个功能区无法生成布局候选](https://github.com/zhoushoujianwork/easyeda-agent/issues/262)。
2026-09-27 从原始需求生成的 51 件电路，在 USB、数据隔离、串口、BOOT/RESET 与 MCU 区失败；
另外五区只通过离线规划。原始几何、网络和归属已冻结，十区输入重放复现相同结果，
详见[失败报告与可复现输入](reviews/2026-09-27-cli-layout-regression.md)。

同日通用修复已使十个冻结区域离线 `planned`，原失败记录保留；详情见上方回归记录。
完整现场链路尚未复测，保持 open；有界搜索失败不证明布局无解。
受影响的是高级自动设计候选生成，基础单动作仍按各自实测范围使用。
A01 失败，A02–A06 未运行；不移除几何/直连检查、不用手工坐标或 GUI 绕过，也不签完整发布通过。
这项新失败不在用户已接受的 #55/#260/#261 范围内。

[#263：超过 1 MiB 的动作响应被 CLI 静默截断](https://github.com/zhoushoujianwork/easyeda-agent/issues/263)。
P1 前 272 步写入后最终完整页回读失败，已保存并导出部分候选；后续页与 PCB 没有继续写入。
公共读取已改为动作 32 MiB、health 1 MiB，并在超限时明确报错；离线正负例和全量检查通过，
现场完整 JSON 已可读取；其后的 P1 定向恢复与关闭范围见下，不等于完整用例通过。
详见[截断修复记录](reviews/2026-09-27-cli-response-truncation.md)。
回读失败不证明对象缺失，不得盲重放或删减必要字段；先完整回读并从当前状态重新编译受保护计划。
该问题也不在三项已接受限制内，影响依赖完整快照的批次验证。

响应截断 #263 已修复并关闭：`d3409a0` 的完整读取及 P1 save→reload→fresh 对象/逐脚/线树恢复
经过独立子集复核，658 项通过；宿主隐藏属性的 ID/坐标归一化变化另行保留，不宣称完整 result 全等。
该子集不签整页图面或完整 E2E。

恢复后又发现 [#264 图签文本更新强制显示](https://github.com/zhoushoujianwork/easyeda-agent/issues/264)
与 [#265 clusters 无接点交叉误报](https://github.com/zhoushoujianwork/easyeda-agent/issues/265)，已保存原始输入/回读。
两项修复通过离线回归，现场尚未签署；#265 初版的库存摘要请求遗漏与漏原始线段边界已于 `5ab4b40` 修复，同一反例独立复验通过。
#264 的 Compose 源显隐合同已于 `61d0173` 补齐并通过定向离线回归，等待现场，不手改生成队列兜底。
P2 另有 SDK strict DRC 聚合 2 warn、无明细，根因未知，不声明已定位或豁免。
P1 的保存重载和电气通过不等于图面通过；三页完整验收仍未完成。
#265 在提交版本的 P2 真实 strict clusters 定向复测已 exit 0，保存重载后完整 gate 仍待完成。
#264 另发现新页字段显隐为 null 时，仅改文字触发宿主 TypeError，回读确认该次无变化；
本轮源明确声明的布尔显隐已写入并保存重载通过。未知显隐不能猜值，默认状态边界继续保持 open。
`37449f2` 已让 CLI 在写文字前拒绝未知显隐并列出缺项；显式布尔仍可执行。
独立离线 83 项通过，现场写前拒绝与三页完整图面仍待复测。此预检查覆盖 CLI/Compose 路径，
不声称原始 typed HTTP 已具有同一图签预检查。官方 P3 图中仍有额外作者属性值，下一轮修源显隐并重新生成计划。
详见[原理图恢复与严格检查记录](reviews/2026-09-27-cli-schematic-gate.md)。

[#266：导线创建返回后立即回读暂为空](https://github.com/zhoushoujianwork/easyeda-agent/issues/266)。
P3 第 21 步已返回实际线段 ID，但即时完整读取的 wires 为空，稍后 fresh 才出现同一条线；
失败保存后六件/一条线，未重放队列。`8877942` 只对合法库存的覆盖不足追加有界读取，
不重复写入、不放宽身份、几何或拓扑判定；取消/期限到达仍失败。独立 58 项和最终全量 Go
4075 项通过，仍待真实宿主、保存重载及完整原理图验证，保持 open。该新问题不在三项已接受限制内。

本轮最新构建完成三页受保护 Apply 及 save→reload→fresh，195 脚/41 对外围/15 个 direct 树对账通过；
全工程 33 网、UART 两网端点完整，三页同参数 SDK strict 均 0 fatal / 0 error / 0 warn。
#266 的 wire-000/015 实际均首读为空、第二读覆盖齐全后通过，每动作只写一次；
#264 三页图签黑色表格正文保留且额外蓝字消失，#265 P2 完整严格检查通过。
这些最新现场结果已完成有限独立复核，#262/#264/#265/#266 按各自范围关闭；
不补签旧失败，不代完整 E2E、L1/N1/R1 或 PCB。复核保留隐藏属性读值差分及原始审计的 context/seq 缺项，
没有用全文全等或源码推定掩盖它们，详见[续测记录](reviews/2026-09-27-cli-schematic-gate.md)。

[#267：保留器件清页遗漏引脚子级属性](https://github.com/zhoushoujianwork/easyeda-agent/issues/267)。
隔离真实 handler 的完整 global 投影 dry-run，把 153 条真实 pin-owned 属性列入 orphan 删除计划，
而返回 instancesPreserved:true；global 为空对照仅显示保护覆盖不足。两种复现均零删除，未发生现场损坏。
原始合成库存不证明宿主某次 unscoped getAll 必然全量。后续须补官方引脚父属闭包、完整属性核对及正负例，
当前不使用该广义清理路径。本批更窄的单导线 typed 删除及六件/51 脚/NC/属性语义保存重载已单独核对。
有限独立报告 SHA-256 `66d2aa66588a6e333d2fde1e7d20aaa9d729a93264962c86ea8fe4fd4b864cc5`，
输入 SHA-256 `c76e4c9415310d5ec7f1085066d79e338e52c5f5ce5b28a02e39027d9883c58d`。保持 open，后续修复和现场复测。

历史段落的等待与失败判断仅适用于各自原批；本轮 R1 监督失败另行冻结，不从正常完成的批次补签中断。


# CLI 修复发布范围决定前的完整设计报告（b2c0500）

以下原文保留当时状态，不作为完整设计通过结论。

# v1.8.0 执行报告

[发布准备](../../release-1.8.md) · [基准](baseline.md) · [用例](test-cases.md) · [详细执行记录](test-report-detail.md)

**完整用例进行中，尚未发布。** 三页、51 件电路及新单次 PCB 导入保存重载和有限独立复核通过；旧101件重复导入已精确清理。新R1、投影修复/升级、两件无铜L1、位号及14标记有限独立通过；四层 SIGNAL 中间态、六电容修正后的两轮布局及独立复核通过，当前等待用户布局确认，最终 GND PLANE 及完整成品未通过。

用户已接受 #55 页面改名、#260 区域/铺铜边界线宽、#261 铺铜优先级为本次已知限制，详见[基准范围](baseline.md#2026-09-27-用户接受的范围)。它们的历史失败保留，不整体阻断本次高级准入；其他需求、几何、电气、持久化和独立复核要求继续执行。

## 现场回读

目标工程 `0f46d4361c4741a9ab7a9ed62164ca9b`（ceshi-cli-basic-20260924），P1 `e453c0063919726b`、P2 `5c21ec9b670a7bb5`、P3 `b137c508566450c2`，PCB `3037fc1dfa4965d2`。全程使用内置浏览器 Web EasyEDA Pro 4.1.60 和 typed CLI。

原理图批 CLI/daemon `v1.7.0-45-g2c6cd35`，connector `1.7.1-dev.11`；窗口 `8f2f5dfa-b722-46f6-8f58-c993546c2671`，该批最后现场调用结束于 2026-09-26 UTC 20:46:08。bin/PATH SHA-256 均为 `8f49eac5e699482a6102895ec891f88f998ff163a844b8ebdbed8a5936f0a0a0`。当前续测已为dev.13，详见下文；开发戳不代表正式v1.8.0。

最新 146 件冻结材料清单 SHA-256 `1410c197c426231d34f8638146d516aa1d0ccbed9ca77dc3d37a0fee78567ed8`，根任务重算全部文件大小与哈希一致。三页 195 脚（158 connected / 37 NC）、33 网、41 条外围归属和 15 个 direct 物理树与自主源及官方原生网表一致。每页 save→真实 reload→fresh 后布局、连接和 SDK strict 均通过，SDK 各为 0 fatal / 0 error / 0 warn；原 PCB 184 条原生记录未变。原生备份 ZIP 有效，重新导入恢复仍未验证。

仅将 `esp32MiniRequire.md` 第一节的 996 字节原始需求交给无历史设计上下文的执行员，输入 SHA-256 `e62ebb05ecedb82e103d81b31da2bbf26aefe0669e580961c3aae87fef7d74d0`；未提供预制 BOM、网表或布局答案。本轮自主选型、真实测量和源数据的各批出处保留在[详细记录](test-report-detail.md)。

| ID | 结果 | 当前证据和仍缺的验证 |
|---|---|---|
| M1 | pass | 最终 51 位号的真实身份、物理脚、bbox、位号几何及精确清理保存重载已独立复核；924 件冻结清单有效。NT L1 修订单独复核；不签 PCB 实例或原生归档重新导入 |
| F1 | pass | 仅签本轮三页参数源、连接/NC/外围归属及真实直连，独立复核通过 |
| F2 | pass | 仅签三页 15/13/73 步受保护执行、保存重载、位号/图签/图面及 strict 检查；属性差分按下文保留，旧失败不改判 |
| E1 | pass | 仅签实际三页静态电气及全部 195 脚与源/原生网表对账；不宣称实板上电、烧录或动态供电保证 |
| L1 | pass | 仅签D4核心+R7专属外围两件无铜整体移动/恢复；save/reload/fresh与987项有限独立复核通过。原生13条ticket/DOCHEAD及缩略图差分保留，不签带铜或其他模块 |
| L2 | pass | 仅签真实图签错误的参数修正：三页黑色表格作者保留、额外蓝字及越框属性消失，独立复核无新增问题；不外推通用局部修复 |
| N1 | pass | 仅签自然过期 sourceScene 子例：第 2 步写前拒绝、0 设计 mutation，三页完整数据和 8 个原生条目内容未变；辅助预检失败/编排未短路保留，不补签该预检或其他负例 |
| R1 | pass | 仅签新 dev.12 两件非铜 region 受控中断/同源恢复，完成态 save→reload→fresh/native 先于精确清理及再次保存重载，独立复核通过；旧80失败/126partial保留，不外推整板布线恢复 |
| E2E | in-progress | 原理图、新单次PCB导入及升级有限独立通过；旧失败保留。四层SIGNAL中间态、两件无铜L1、位号及14标记已核，六电容修正后两轮布局及独立复核通过；用户确认、最终GND PLANE、整板布线、铜/DRC和最终保存重载导出未完成 |

## 失败与修复

- #262 区内规划：`8c4d3e4` 通用修复，十区离线 planned、三页现场完成并独立复核通过，按本轮原理图范围关闭。
- #263 大响应截断：`d3409a0` 完整读取及 P1 保存重载后的有限恢复独立复核通过，已关闭；不签完整图面/E2E。
- #264 图签显隐：`61d0173` 补源到队列合同，`37449f2` 在 CLI/Compose 文字写入前拒绝未知显隐。新源显式 false,false 经三页保存重载及官方图验证，表格正文保留、额外作者消失；未知显隐现场负例无真实适用字段，保持 not-run，离线 83 项已过。
- #265 无接点交叉：`5ab4b40` 补完整库存请求及原始线段对账；P2 保存重载后的完整 strict 与 SDK 检查通过，合法交叉仍报告为 info。
- #266 导线延迟发布：`8877942` 只追加有界读取、不重复写入。新 P3 的 58 次唯一 wire/connect 写入中两次真实命中第二次读取，其余首读通过；全量几何及持久化验证通过，原失败与回退证据保留。
- #267 引脚子级属性保护：隔离真实清页 handler dry-run 复现保护遗漏，未执行现场清页、未造成损坏。保持 open，公开 Skill 暂停受影响的保留器件清页分支；本批使用单线精确删除并核对六件/51 脚与 394 个属性语义，171 个属性运行期 ID 重载时重铸，不宣称原始 record 全等。
- #268 后台保存：两批在显式 PCB 保存与 reload 内保存均成功、随后读取原理图时，后台 `pcb.save` 外层 ok:true 却 saved:false。保持 open，不推断精确根因或设计丢失；两批清理均另有保存重载及完整数据/原生对账，后台失败不计保存成功，也不免除最终持久化验收。
- #269 PCB 导入：一次 typed import 报成功，实际 51→101 个实体，50 对重复身份。确认轮询重复点击及超时误签可隔离复现，不证明现场唯一根因；dev.12 候选补单次确认、身份重复/不可读检测与 CLI/raw/Apply 失败传播，已提交 `698980e` 并升级目标运行态，导入保存重载和5934项独立复核通过，按本批有限范围关闭。全部 101 件已 fresh 精确删除并保存重载，三页与范围外数据不变；PCB 原生新增 464 条空 payload 历史记录，不称原字节恢复。

#262/#264/#265/#266 的修复与本轮三页复验已独立通过，按各自有限范围关闭；旧 SDK 警告不凭数量推定精确根因。#267 保持 open。具体命令、原回包、冻结清单及修订范围见[原理图续测记录](../../../reviews/2026-09-27-cli-schematic-gate.md)和[历史详细记录](test-report-detail.md)。不覆盖旧失败，不盲重放，不用 GUI 恢复。

## 独立复核

本批原理图独立报告 SHA-256 `93bb42397521ea5c4693771550160472a25c58ea1c6659cf7180ad2d7f54c264`，输入 SHA-256 `40e95ab24b36826b9cdb2ddc43b652e617dffe7bb21828a4c31bc4d36738a3c8`，清单 SHA-256 `398b4fad74205960d9efbf9172b68bcf731ee58d4db64f2b622ac4bdb567aaeb`；4535 项是独立离线证据断言，不是产品现场测试数。

复核逐项保留 P2 42 条隐藏引脚属性的 AlignMode/X/Y 读取差分、属性 runtime ID 重铸及 R5 的 `5.684341886080802e-14 raw` 尾差；官方图面和原生页数据无对应电气/可见几何变更，不称原始属性全等。#266 的审计不含逐次完整 context/seq，seq 证据来自结果 observations；不补造缺失回包。此前修复及源的有限报告见详细记录。

N1 另批 45 文件清单 SHA-256 `e4d5f09dccb8e9bb13c0d2d18b4860995b2b8ff3d6f43bba411fdc17702b684e`。独立报告 SHA-256 `920b26c7e01774e099a8fdaa9ced1f0dc76ad82a654de4a823e30c7c34b81c51`，输入 SHA-256 `c0e85ce2174fd9376264e715f38def26fb19ddb334de1f8afb6fcc3fccdfe1bc`；302 项证据断言通过，原 146 文件不变。实际拒绝为新增 marker 导致 source-drift，不冒称触发 wires=0 断言；57 条原始审计均无设计 mutation。

导入修复 dev.12 批全量 Go 4108 通过、0 失败、1 跳过，15 个包通过；连接器 638 通过、typecheck、Skill 与 diff 检查通过。当前dev.13结果另见下文。剩余必测项和最终发布独立复核未完成，manifest 保持 `result=in-progress`、`independentReview=not-run`。

R1 新批 126 文件 manifest `f3dc0396d1a045ef37a5bd8d1ca587aee4f33d6b2fc97fa5c9520f6fb2a3c290`；
独立 236 原证据断言及 15 重算检查通过，整项仍 partial，报告 SHA-256
`b282cbc69a57fc77492a8f2445c9d0c38aa12a6c724587079a10d1bb69e752ca`，
输入 `b0f1ed3aed32aff2d2bd8b0a1cad2c11e159734c1ca6fb3d39152c008f7c4bb7`。
最终空板 reload 不反向证明恢复态持久化；新 runtime 上须补完整链，再清理。

PCB 失败 53 文件 manifest `09f176be28b6144a3ec4eb949e2ef93bf72e924a79d615d06bdabffd7ada3329`、
清理 46 文件 manifest `63f3e0365f672b71e4dd5c3b9937d0b188ac9c2e753e4caeacc81efca937dba1` 均经根任务重算匹配。
原副本证据独立 446 项通过；最后显式现场调用结束于 2026-09-26T21:54:33.989776Z，
之后后台 req_1460 在 21:54:35.998273Z 返回 saved:false，分别统计。dev.12 离线全量 Go 4108 pass/0 fail/1 skip，
连接器 638 pass/0 fail，typecheck、Skill 与构建通过；独立候选离线复核通过，报告 SHA-256 `1a7dad21ec2a41ae31b24af457a74af9b51e8aea210a30a4a18c7f208470f2bf`；新包已升级到精确 dev.12 runtime，导入复验待完成。
精确清理独立复核 680 项通过，报告 SHA-256 `5e484779804d352db23dae2b747bc7cc0e7ad6c793f8a09a1e8cc17ddbb7b24a`；
不签原生逐字恢复。完整 audit 138 条、成本区间 137 次（不含更晚后台保存），原始差分保留。
新包 SHA-256 `1bdff918840db41390ceb34146e540f80d9e7eacc3ebb3f5f7cae5d1aaae48f2`，
范围与原始差分见[PCB 记录](../../../reviews/2026-09-27-pcb-import-confirm-fix.md)。

每次实际端到端尝试分别记录成本，区分墙钟/daemon/差值，未提供 token 记未记录；失败也记录，不当作通过。各区间与证据哈希见[详细记录](test-report-detail.md)。完成全部现场验证后才统一正式版本、检查本地发布资产并执行已批准的 v1.8.0 发布；当前没有创建该 tag 或 Release。

运行态升级 190 文件清单 SHA-256 `1f4536894405594015de50898a1e461e06c5325a279e186afc9e47a9c002c97d`；
四页升级前 saved:true、新注册/目标/版本、安装 check READY 及 fresh 基线核对通过。
三页非属性全等，global属性全部字段相同，仅623/388/258 runtime IDs重铸和排序变化；
原生122 sections除DOCHEAD全等，DOCHEAD/顺序差分保留，7其他ZIP条目全等。
最终本地包 `.eext` SHA `a836acc5d7990073bb67966deb658b80bd06a5a594e2243255be94da7098eab3`，
bundle与第一包同字节；升级的有限独立复核通过，4468项为证据断言，报告SHA
`ec39ce6bfd8a877d2f2850d5acc309eec6f06c8bc7c666bf7816f8f680ee9e78`。执行员现从保留自主源继续R1和单次导入，
不据升级补签PCB或完整用例。

新完整 R1 136 文件清单 `62e7ba7d751af19c4b7f077703e8e9afb6324126b9f6f1f3aec641c63f258efd`，
根任务全部匹配。恢复 A+B 的048a–e显式保存/真实reload/完整fresh/native均先于050/051清理，
最终又精确清理并保存重载；8次saved:true。本次有限 R1 独立3770项通过，
报告SHA `cafcd2ca95c0e871d62b16068daed1294570709ca668cd55a89e2879709cf33d`，
输入 `416f483cc54115170a43a4b158a891fe73436d6545975cc0b4033211ba9adadc`；旧126/80结论不变。

新dev.12单次导入46文件清单 `f0b0a4e721d9bfb52e6ed0cf95a3a83742ae325cbaaa653e1016f9f7cf5bcacf`，
根任务全部匹配。51唯一器件/195源脚/203实际pad/33网双向核对与保存真实reload通过；
U1.39的9个同号GND散热pad逐一检查，多8个非遗漏；37NC对应37无网pad。
三页whole result不变，当前仍2层无板框/铜；独立导入复核已通过。

2026-09-27 单次导入独立复核：5934 项有效断言通过，352 输入哈希末次重验通过，
30 文件 manifest `251056e8ba63104ffd1f7a485ccee751bdf869b24914b6fb0e1a3b1b490e8f52`，
报告 `0be96f1fed67367d6802cbe349e919b43f23be2a41e7de41298bfb432c93c188`。
即时与持久化 238 原始几何差分保留，其中 R12 两脚 y 差 0.1 mil；
不签两批坐标原包全等，布局绑定 014/015 fresh 版本。#269 有限关闭，不签完整 E2E。

离线布局又发现 #270：4 个 USB POLYGON 中心投影而绝对路径未移动。325 文件清单
`5a55f66bb4f3c0a92732a56184ca5d8d23470a3f37ba9b54140c6cec42eec455`，
全批 candidate-rejected、无现场调用/写入；独立 red 114 对照通过、4 正确几何期望失败，
17 文件清单 `e7b6b0b5fa74dcd01e182c5df6f905c7d4f2c2c0641ebb1249872607e24c33a3`。
dev.13 修复候选全 Go 4129 pass/0 fail/1 skip、15 包，connector 638、typecheck/build/Skill 通过；
候选独立复核、升级及现场布局仍待完成。详见[投影修复](../../../reviews/2026-09-27-pcb-polygon-projection-fix.md)。

投影修复已提交推送 `faac8be`，独立修订复核及提交后包 487 项核验通过；
报告 `5fb5838b485299d58d66751577e7ce5d7391141b41d405065eb239b68a47f579`，
19 文件清单 `bb540539ab15cf50482b77261441a564fde958af986685e0badd9687cbb1a142`。
CLI/daemon/connector 已升级 dev.13，新窗口/fresh/本地 READY 与 bin/PATH/package byte 对账通过。
173 文件升级清单 `a8fb7b8382018f13e68182dec6a91e763d56c1961387108ee475bdfdb16e0fab`；
PCB 51 件/203 pad result 前后完全一致，board 除采集时间一致，仍 2 层无板框/铜。
三页仅 runtime 属性 ID、三张 sheet Create Time 及 P1 Create Date getter 差分（4逻辑值/8重复字段）
明确保留；原生 122 sections 非 DOCHEAD 行和 7 其他 ZIP 条目逐字相同，不称全部 raw 属性等。
升级独立复核通过；原执行员已接独占窗口续现场布局，#270实际投影、L1/完整四层布局/两轮布局/用户确认/
布线铜DRC和最终验收尚未通过，manifest 总结论仍 in-progress/not-run。

升级独立 4689 项有效断言及 657 输入哈希末次复验通过，报告
`f97f2b2d46981011e5a20dee8881751087dab563559bde161fd7c70c97241e0c`，32 文件清单
`6bc66eb27e383ac2fa72c19f03a3a772292c5cf736a0e496c5896068fa8ef1e8`，根任务全部核对通过。
首次叠层 partial 批冻结244文件，清单
`d4c6a71cca2d0ce8e1d24989c49e7e5fcdece8e69636568cf08778fbdfff756e`，根任务全部匹配。
层数2→4成功；提前请求PLANE written:false、partial/CLI1，不继续依赖步骤；随后saved:true，
51件/203pad、铜/丝印/config即时观察保持，staleRisk仍报告；不签权威重载数据。
原生97 raw差异及精确范围保留，批内未reload。
续批已真实reload确认4层，按SIGNAL中间态布局；最终GND PLANE仍待正确顺序验证。
类型拒绝唯一SDK原因未确定，不据编排错误补签或豁免要求。完整依据见[投影详细记录](../../../reviews/2026-09-27-pcb-polygon-projection-fix-detail.md)。

叠层partial批470项独立证据断言通过，报告
`579341f584ac9a87785b163e1a53aca7d85aad40d7d24733ed0b80d2a83dfd1d`，21件清单
`de88e744076fbbd409950aa5600c2d386f63fb9b368f887c7b663719902b79a2`，根任务全匹配。
仅签失败后保全，原叠层请求仍partial/failed，不签该批持久化。
USB里程碑424文件清单 `a9e76a80b5ce4251509ced7df5d8116337cbf59f483597303edc95568636d409`
根任务全匹配；四层真实reload及6件Apply/save/reload/fresh/native已完成，独立18731项断言通过。
实际4个L单环全部路径匹配，中心最大0.08mil差保留；无孔/ARC现场前提、L1仍not-run。
后续45件与四M3孔已保存重载，RF/丝印及连续两轮布局尚未完成，首张诊断图不算正式自检。
47件USB独立manifest `15939503e44e462e795e9af9390d325d2ae563e9c37d2758c66a86f35b74eed2`
根任务全核对，报告 `7689ea36aa70edb9c5cc7f6f168ec24865a313a4bea503dc48c4df47a46c1980`。
#270按实际四个L单环平移有限关闭，不签孔/ARC/非零旋转现场、POLYGON net-path或整板E2E。
后续L1点灯两件无铜整体移动/恢复54原件及28依赖/3版本补充完成987项有限独立复核；不签带铜移动。
RF已保存重载；silk-align假clean的三对相交及自身bbox漏检已修复，`83a98b2`及dev.14实际51位号保存重载与独立复核通过，#271按实测范围关闭。14标记有限独立通过；六电容修正后两轮自检及整板独立复核通过，用户确认尚未完成。
原始失败、独立红灯与L1哈希见[位号修复详细记录](../../../reviews/2026-09-27-pcb-silk-align-fix-detail.md)。

dev.14提交后开发包581项独立核验通过、346输入哈希复验；120文件清单
`385f30bceca8bfed0c8718e185bec2a46e40e55a60e4894cce925ac1a59913b2`，报告
`959c7be7d84db5ee43e78095f17345af38aa546ca6fb559f70a4eaad4b8258ee`。
升级前四页saved:true，采用已安装连接器的同UUID更新并保持权限，新注册dev.14/目标PCB及READY通过。
root全量差分证明三页非属性相同、属性仅1269个runtime ID重铸，51组件/203pad相同、board仅采集时间不同；
原生122section设计行及7其他ZIP条目相同，122个DOCHEAD client token及section顺序变化保留。
262文件升级清单 `fdafb56b77686bf41d24317ba1c1f3dbb32c392418fed7a83579102ef32bbbd7`，
升级独立核查已通过，不签位号修复、Layout或E2E。

实际51位号37件清单 `e3f17751fa663e9f16599c5425b65c2fce1678c73d51d93aa9677c96ab7ce7c5`，
独立15,868项有效检查/45输入最终哈希通过，15件清单
`755280cc3d3276a169dc127eecb2d9005e6a02bee85b8ec0a309a609eeebb04b`，报告
`324e0d30f81e0b53ce98abcb4d82388f86a2f921c1f871758de044bba580f1f1`。
51targets/50几何变化/52native raw行、16末位差及U1.x/D3.y持久化约+0.00005mil差保留，
所有组件/203pad及范围外数据保持；不补签14标记、Layout、DRC、最终PLANE或E2E。

后续14标记独立5,341项/91输入哈希通过，六电容目标与布局已修正，正式两轮自检无再次修正。
162件修正/90件两轮材料根全量哈希核对通过，整板独立4,764项/329输入复验通过；具体顺序、差异、限制及独立结论见
[PCB布局复核](../../../reviews/2026-09-27-esp32-pcb-layout.md)。166未连接、最终PLANE和真实降压回流仍待后续，不签E2E或发布。
