# v1.8.0 执行报告（A01 修复续测）

[发布主文档](../../release-1.8.md) · [基准](baseline.md) · [用例](test-cases.md)

2026-09-27 启动发布准备。**用户已移除三项整体阻断；A01 初测失败后的通用修复及离线复测通过，现场续测中，result=in-progress，尚未发布。**
初测包为 `1.7.1-dev.11`；后续 CLI/daemon 为 `v1.7.0-31-g8c4d3e4`，connector 仍为
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
| F1 | in-progress | 初次布局 failed 保留；8c4d3e4 修复后最终 NT 完整源十区规划、三页组合与固定 Compose 离线预检通过，现场实测旋转位号差异后正更新新源，真实页面/受保护队列待完成 |
| F2 | not-run | 尚未执行完整原理图受保护队列、strict gate、DRC 或成品保存重载验收；临时单件测量清理不是该项通过 |
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
原始各批冻结材料保持不变，三项已接受问题继续按原范围跟进；本轮不创建发布 tag 或 Release。
新请求失败时保留实际证据，停止依赖步骤，不静默改参、用 GUI 兜底或冒充发布验收通过。
本轮以“A01 blocked #262”的失败尝试登记成本台账，没有标为 E2E 通过。
UTC 2026-09-26 请求统计区间为 16:32:57–17:38:09；审计实际首末动作 16:33:09–17:34:04，
该动作窗口约 60.91 分钟，daemon 聚合 5.35 分钟、差值 55.57 分钟；差值含思考、离线求解与资料核对，
不当作纯模型耗时。1,937 个 daemon 调用的 failure=0 不包含 CLI 本地拒绝或离线布局失败。
token 未记录，JSON 中 tokens=0 不代表无消耗；原包见本地 `artifacts/release-v1.8.0-e2e-20260927/cost-attempt.json`。
