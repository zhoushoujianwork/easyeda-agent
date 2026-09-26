# 完整原理图恢复与新增严格检查失败

2026-09-27 修复响应截断后，以 `v1.7.0-33-gd3409a0`、connector `1.7.1-dev.11`、Web 4.1.60
继续原始 ESP32 用例。P1 完成 15/15 恢复步骤、save→typed reload→完整 fresh read→原生导出，
29 件/88 脚、实际网络/线树和位号几何一致，layout-lint、电气检查与 SDK DRC 通过。
**整套原理图仍未通过：P1 图签文字越框，P2 strict gate 失败，P3 未执行，PCB 未改。**

## 真实失败与跟进

| 问题 | 原始事实 | 跟进 |
|---|---|---|
| [#264 图签文本更新强制显示](https://github.com/zhoushoujianwork/easyeda-agent/issues/264) | Name/Description 文字显示到外框下方；Drawed 额外字段名重复显示。CLI 把所有更新的 showTitle/showValue 强制 true，覆盖模板状态 | 正修复文本更新的显隐保留、显式布尔及完整 payload 幂等回读；尚未现场复测，不签图面通过 |
| [#265 clusters 无接点交叉误报](https://github.com/zhoushoujianwork/easyeda-agent/issues/265) | P2 SW2 GND 与 R5 3V3 在 (260,430) 内部正交交叉；同一 fresh pin/net 与原始线段已证明无接点，check 为 info，clusters 却按加粗 bbox 报 1×1 overlap | 正补同源物理线岛/接触证据，端点/T/共线、未知数据和真实器件/marker重叠不豁免；不按 check 文案放行 |
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

两项合并候选全量 Go 测试 3982 通过、0 失败、1 个 Windows 专用测试在 macOS 跳过；
相关测试、公开 Skill 与 diff 格式检查通过。这些是离线结果，尚不签 V4 图面/保存重载、P2
strict gate 或完整 E2E；SDK 两条警告仍未知。该恢复尝试已独立记成本：UTC 19:02:10–19:09:39，
不与较早的 P1 截断尝试重复计时，token 未记录。

后续恢复准备另复现 Compose 源合同缺口：`titleBlock` 仅接受字符串，声明显隐对象在纯离线
JSON 解析阶段即拒绝。该缺口随 #264 跟进，需把元数据贯通源、计划和受保护队列，并保留旧文本
源兼容；尚不以手改生成队列或绕开固定转换验收图签。

另有独立电气源预审 869 项静态断言通过，51 件/195 脚/33 网，未发现明确脚号或下载逻辑接错；
只签源与本轮真实测量一致性，不签现场 E1 或上电/烧录。USB 来源能力与启动瞬态、CH340C 电压余量、
PCB 两层 GND、天线/孔/丝印/热仍待实际阶段验证。
主报告 SHA-256 `eee9e5dfb19848bde08ae2be2133bd93977d1ef157949ce993ae9a779caccac9`；
详细稿 SHA-256 `9dd79a71d0bff5118435355cbc37889580fc35fcd57f5122668bbaffba5438cc`，
输入 SHA-256 `dda3a1aa638f562466d6c9f19274823c570e4b3d757cd1a7fb4c060b18cb730e`。
