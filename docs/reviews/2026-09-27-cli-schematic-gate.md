# 完整原理图恢复与新增严格检查失败

2026-09-27 修复响应截断后，以 `v1.7.0-33-gd3409a0`、connector `1.7.1-dev.11`、Web 4.1.60
继续原始 ESP32 用例。P1 完成 15/15 恢复步骤、save→typed reload→完整 fresh read→原生导出，
29 件/88 脚、实际网络/线树和位号几何一致，layout-lint、电气检查与 SDK DRC 通过。
**整套原理图仍未通过：P1 图签文字越框，P2 strict gate 失败，P3 未执行，PCB 未改。**

## 真实失败与跟进

| 问题 | 原始事实 | 跟进 |
|---|---|---|
| [#264 图签文本更新强制显示](https://github.com/zhoushoujianwork/easyeda-agent/issues/264) | Name/Description 文字显示到外框下方；Drawed 额外字段名重复显示。CLI 把所有更新的 showTitle/showValue 强制 true，覆盖模板状态 | 文本显隐、完整 payload 回读与 Compose 元数据已修复并离线复核；尚未现场复测，不签图面通过 |
| [#265 clusters 无接点交叉误报](https://github.com/zhoushoujianwork/easyeda-agent/issues/265) | P2 SW2 GND 与 R5 3V3 在 (260,430) 内部正交交叉；同一 fresh pin/net 与原始线段已证明无接点，check 为 info，clusters 却按加粗 bbox 报 1×1 overlap | 完整同源库存、物理线岛和调用方修复已离线复核；端点/T/共线、未知数据和真实器件/marker重叠不豁免，等待现场复测 |
| P2 SDK strict DRC 两条警告 | `nativePassed:false`、0 fatal / 0 error / 2 warn、`detailsAvailable:false`，仅聚合数量 | 根因未确定，尚不声明为产品 bug；不猜两条内容、不归因于上述误报或图签，不移除 strict 或补签 |

P2 前 161/163 步成功，16 件/56 脚（50 连接/6 NC）和 101 实际线段/30 标记与计划一致。
第 162 步停止，随后只 typed save、完整 fresh read、原生导出/官方 SVG 冻结，未 reload 或改几何。
P3 仅图框，测试 PCB 184 原生记录逐条不变；原生包 ZIP 完整，`restoreVerified=false`。

## 证据边界

本地 `artifacts/release-v1.8.0-e2e-20260927/a01-schematic-recovery-20260927/` 冻结 87 文件；
清单 SHA-256 `e27141e9cd730372fb133a7334ebed4d95bc07b15ca669f0d0e3ef69e37996c2`，
末次原生包 SHA-256 `0114ca152b809d92fafbafcce51c79e9c1e19d88aca7625f34e6c32db023909b`。
strict-gate-raw、原始线段/逐脚/组 bbox、原图签回包及官方 SVG/PNG 均保留；旧 39/161/924/75/157 批次不变。
本轮新的实际失败不自动纳入已接受的 #55/#260/#261 范围。

## 修复候选与验证

#264 已改为文本更新保留已知显隐、未知/null 省略；显式布尔可以更新字段显隐，失败回退同时
核对字段与整体显示目标，提交 `83e6c65`。空更新和未知整体状态不提供成功证明。独立复核通过 5 项定向测试及
9 条实际 Cobra HTTP 用例（30 项断言）；报告 SHA-256
`2c8a5a2131716e877063d5874724b563a33dbbb97ddc7e0a8e7e927229450a8c`，
输入 SHA-256 `8677edbccf2b1da592a83b08ee14dcb5d16561d23ede673967d86930e8bf6b88`。

#265 提交 `591b681`；同一真实 P2 fixture 已复现旧 1×1 overlap 并在候选中通过。只对同一次完整快照中由
原始线段、逐脚网络、全量 marker 和物理线岛共同证明的无接点内部 X，豁免 wire-wire 成员对。
端点/T/共线、交点 pin/marker、真实本体碰撞与缺测仍拒绝；strict 缺少同源导线 inventory 直接阻断。
候选 21 主测试/57 子测试通过；冻结清单 SHA-256
`07a0527ecf60ae1ea2771a86a91a93cba8157a0eb1bdfdd55ea17e6131c58053`。

独立复核随后发现该初版采集遗漏 `includeConnectivitySummary:true`；官方连接器只有显式请求
才回库存摘要，测试夹具无条件返回摘要掩盖了缺项。该候选因此不能签实际调用路径通过。
初版材料保留，不覆写。revision2 提交 `5ab4b40` 修正三处 caller、快照参数超集复用和 faithful HTTP 回归；
另补原始线段完整对账：即使 wire PID 数量正确，漏任一 rawLine 的 segmentIndex 也非零拒绝。
独立复验 7 次真实 Cobra 本地 HTTP、98 项定向测试通过，34 件证据/11 份源码匹配；
mock 缺 ownership 的 gate 仍因 13 个 tight 非零退出，不把交叉误报消失写成整 gate 通过。
报告 SHA-256 `678952fedbb2acc9aa7c09ac31c19dcc548c9e8ab72cf827d178b07deaa47c37`，
输入 SHA-256 `3544ac0ad437fa3c169f7ce2a12f0314e07309cf3cfd2ed1d64529f9e9a7c8ee`。

revision2 与 Compose 元数据修复合并后全量 Go 测试 **4027 通过、0 失败、1 跳过**，15 个包通过；
公开 Skill 和 diff 检查通过。仍只签离线修复，现场 DRC、保存重载和完整案例需重新执行。

两项合并候选全量 Go 测试 3982 通过、0 失败、1 个 Windows 专用测试在 macOS 跳过；
相关测试、公开 Skill 与 diff 格式检查通过。这些是离线结果，尚不签 V4 图面/保存重载、P2
strict gate 或完整 E2E；SDK 两条警告仍未知。该恢复尝试已独立记成本：UTC 19:02:10–19:09:39，
不与较早的 P1 截断尝试重复计时，token 未记录。

后续恢复准备另复现 Compose 源合同缺口：`titleBlock` 原先仅接受字符串，声明显隐对象在纯离线
JSON 解析阶段即拒绝。该缺口随 #264 修复于 `61d0173`：限定字符串或 value+可选布尔对象，
元数据贯通源、计划和受保护队列并保留旧字符串序列化。拒绝未知/结构字段、null/错误类型与空文本。
219 项定向测试、Skill 和本轮真实 P1 源→15 步受保护恢复队列的离线重算通过；现场尚未执行。
冻结清单 SHA-256 `5be169431ab29765f502a72641e0e8d8a65de8253e2171d45b74740cdaed58d0`。
独立复核确认清单/源码哈希匹配，并另做真实源重复重算、显隐变化改变保护队列哈希且几何/连接
不变、null 显隐拒绝且无输出三项实际离线 CLI 重放。报告 SHA-256
`c72854a241007d28a7480d55980005b5e0be34c8eb65505bc2cc2cd22e2b81ad`，
输入 SHA-256 `6f4061475b45effa9821bffb727b563537106416ac21fdc0723a64eae611efe8`。
不以手改生成队列或绕开固定转换验收图签。

响应截断 #263 的 P1 有限恢复独立复核已通过 658 项，原 87 文件哈希全部匹配，明确
save→reload→fresh 后 29 件/88 脚/191 段/42 树与计划一致。组件及原始导线不变；宿主重铸
623 个属性 ID，另有隐藏 marker pin 属性的 44 AlignMode/15 Y/7 X 变化，未宣称完整 result 全等。
子集报告 SHA-256 `ee18a61c34fc068c6d65ba5aba552f944e13ec2728710111915e4575ac000489`，
输入 SHA-256 `cb1daea98948564100119930b164f9c5f499d07c0f64855123f00da9efe92d79`。
该证据用于关闭 d3409a0 修复的读取问题，不签图面、F2、E1、SDK DRC 或 E2E。

另有独立电气源预审 869 项静态断言通过，51 件/195 脚/33 网，未发现明确脚号或下载逻辑接错；
只签源与本轮真实测量一致性，不签现场 E1 或上电/烧录。USB 来源能力与启动瞬态、CH340C 电压余量、
PCB 两层 GND、天线/孔/丝印/热仍待实际阶段验证。
主报告 SHA-256 `eee9e5dfb19848bde08ae2be2133bd93977d1ef157949ce993ae9a779caccac9`；
详细稿 SHA-256 `9dd79a71d0bff5118435355cbc37889580fc35fcd57f5122668bbaffba5438cc`，
输入 SHA-256 `dda3a1aa638f562466d6c9f19274823c570e4b3d757cd1a7fb4c060b18cb730e`。

## 已提交版本的现场定向复测

固定运行版本 `v1.7.0-41-g2d041b5`（CLI/daemon 相同），connector `1.7.1-dev.11`、
Web `4.1.60`，唯一窗口 `fd5c2e25-edde-46d2-a67f-3cbfe1505253`。
新记录位于 `artifacts/release-v1.8.0-schematic-fix-live-20260927/`；不覆盖旧失败批次。

- P2 诊断前后完整 result 逐字段相同。真实 `sch clusters --strict --json --members` exit 0，
  旧 SW2/R5 交叉误报消失；P2 完整保存重载后的 gate 仍待执行。
- 全工程 `sch nets --all --strict --json` 非零报告仅 UART_RX、UART_TX 各 U2 一脚，
  其余跨页 USB 网络有完整端点。P1 与 P2 同参数 SDK strict DRC 均为 0 fatal / 0 error / 2 warn，
  无明细；尚不能把数量相同视为 SDK 根因已确定。先独立完成 P3，再对照同参数检查。
- P3 默认 Name 的 showTitle/showValue 都为 null。仅请求文字的调用返回宿主 TypeError
  `Cannot set properties of undefined (setting 'value')`，前后 titleblock result 相同。
  这是 #264 新发现的默认状态边界，不能宣称 text-only 全覆盖；未知显隐不默认 false。
- 本轮参数源明确要求的 Name/Description=false,false、Drawed=false,true 写入成功；
  typed save→真实 reload→fresh getter 后三项正文和布尔精确匹配，整体图签可见状态仍 true。
  这只签显式字段持久化，官方完整图面与三页电路仍在续测。

## P3 导线发布失败与新修订

P3 重新 Compose 的 88 步队列在前 20 步成功后，第 21 步 `(185,685)→(205,685)` 导线写后校验失败。
SDK 已返回实际 PID `bdee3961676b43e4` 与反向等几何线段；即时 `wires=[]`，稍后完整 fresh 才包含同 PID。
这不是反向线段判定问题，也无法从稍后的读取推定最早发布时间。已停止队列、保存、完整回读和导出；
实际六件/51 引脚/27 NC/一条线，PCB 184 条记录不变，未继续 P1/P2 或重放旧队列。
44 件材料清单 SHA-256 `818e955feec36f15bc27c50d1e39ac51501b082da4ac2033b80a0b243507766d`；
原生包 SHA-256 `3e27f0106b1bbdfd8536202b3b5613dadb2b1eebd3f21c525920c01ad657b2b5`，ZIP 有效但未验证恢复。
新增 [#266](https://github.com/zhoushoujianwork/easyeda-agent/issues/266)，不补签该 partial 失败。

`8877942` 只在合法完整库存的线段覆盖不足时追加只读：含首读最多五次、250ms 间隔、共用 2s 追加预算，
服从更短调用方期限；mutation 仅一次。每次检查工程/页身份、递增 FIFO、abandoned、库存及新几何，
完整覆盖后仍比较物理树拓扑；缺测、非法几何或迟到响应立即失败。原始审计与可迁移几何 fixture 保留。
独立发现的取消/成功响应同时到达漏洞也已按原反例修复。独立 58 项通过，报告 SHA-256
`e93d4240bf2a6883dbe7130e206a14a26e106db0233caed1ebb2ed16438f790a`，输入 SHA-256
`60ea6d033de24bb9827b07b5dde384c570f4cfc5f4c91e3cb7a07b61f378f6f9`；
十份最终源清单 SHA-256 `a54e15ac20b2185ffa21660ec571d7cb983d1187a250cfb9f64ef7fa0ce69e6e`。
证据在本地 `artifacts/release-v1.8.0-wire-settle-fix-20260927/`，未签实际宿主的等待效果或 E2E。

图签 `37449f2` 新修订让文字更新的未知显隐在第一次写入前拒绝，用户源必须明确给出所缺布尔值；
已知布尔精确保留。独立 83 项通过，报告 SHA-256
`518f17bd9c772cd5cb3b1708d6f54fce5c37dff9998296762f67908883ab1276`。
该前置检查位于 CLI/Compose，不能外推原始 typed HTTP。P3 官方 PNG 中 Drawed=false,true 仍有
重复蓝色作者属性值；下一轮在新源副本明确 false,false，重新 Compose，并验证表格正文保留。

两项最新修订合并后的全量 Go **4075 通过、0 失败、1 跳过**，15 个包通过；Skill 与 diff 检查通过。
运行时已暂停，待干净提交版本恢复 P3→P1→P2；图面、电气、保存重载、SDK strict 和完整用例仍未通过。
