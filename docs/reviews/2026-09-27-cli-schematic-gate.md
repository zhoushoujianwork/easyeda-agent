# 完整原理图恢复与新增严格检查失败

> 历史结论，仅为发布材料兼容保留；当前能力见 [CLI Status](../cli-STATUS.md)。
> 逐步运行记录与原始标识见[固定版本原文](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-27-cli-schematic-gate.md)。

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

| 问题 | 修复及必要边界 |
|---|---|
| #263 响应截断 | `d3409a0` 修复后 P1 完整读取和受保护恢复通过有限复核；隐藏属性 ID 重铸不等于原始回包全等。 |
| #264 图签显隐 | `83e6c65` 保留已知显隐；`61d0173` 让显隐对象贯通 Compose；未知显隐的现场边界另见后续修订。 |
| #265 X 交叉误报 | `591b681` 首版遗漏真实 caller 的库存摘要请求，不能签实际路径通过；`5ab4b40` 修正 caller 与 faithful HTTP 回归，缺库存及端点/T/共线接触仍拒绝。 |

这些候选的离线测试不替代现场持久化。独立电气源预审覆盖 51 件/195 脚/33 网，
仅证明源与测量的静态一致性；上电烧录、电源余量和 PCB 仍待后续阶段。
逐个候选的报告哈希与执行过程由本页顶部固定版本原文追溯，最后的现场结论和证据哈希保留如下。

## 已提交版本的现场定向复测

固定运行版本 `v1.7.0-41-g2d041b5`（CLI/daemon 相同），connector `1.7.1-dev.11`、
Web `4.1.60`，唯一窗口 `<现场身份已省略>`。
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

## 三页原理图完整现场候选

以上保留原失败和当时结论。新批次使用 CLI/daemon `v1.7.0-45-g2c6cd35`、
connector `1.7.1-dev.11` 和 Web 4.1.60，于 UTC 2026-09-26 20:46:08 完成最后现场调用。
冻结目录 `artifacts/release-v1.8.0-e2e-20260927/a01-schematic-final-recovery-20260927/`
清单覆盖 146 件文件，SHA-256 `1410c197c426231d34f8638146d516aa1d0ccbed9ca77dc3d37a0fee78567ed8`；
根任务逐件重算大小与哈希一致。本批候选待独立复核，不提前签完整 E2E。

P3 的部分电路不满足 preserve-instances，普通 replace 亦正确拒绝。发现
[#267](https://github.com/zhoushoujianwork/easyeda-agent/issues/267) 后未调用广义保留器件清页，
只以参数化 typed `sch prim-delete` 删除失败创建的唯一导线，再保存重载。
六件完整记录、51 脚及 NC 保留；394 个属性按唯一 parent+Key 配对后除运行期 primitiveId 外字段一致，
171 个属性运行期 ID 被宿主重铸。最初原始 ID 全等断言失败和完整映射保留，不把语义不变说成原始 record 全等。
真实 unwired 快照通过现有 reuseUnwired 编译出 73 步，无 clear/删件/place；未改守卫或历史队列。

| 页面 | 完整队列 | 件/物理脚 | connected/NC | 实际线段/命名 wire | 外围归属/direct 树 |
|---|---:|---:|---:|---:|---:|
| P1 | 15/15 | 29/88 | 84/4 | 191/42 | 24/8 |
| P2 | 13/13 | 16/56 | 50/6 | 101/30 | 13/4 |
| P3 | 73/73 | 6/51 | 24/27 | 55/15 | 4/3 |
| 合计 | 101/101 | 51/195 | 158/37 | 347/87 | 41/15 |

每页均实际 save→doc reload→完整 fresh，layout-lint、连接 strict 和 SDK strict 均通过，
SDK 各 0 fatal/0 error/0 warn。官方 Designator bbox 与测量计划匹配；15 个 direct 是真实同岛线树，
41 条归属有物理路径；原生全工程网表逐脚与源一致，共 33 网。
P3 完成后 UART_RX/TX 各有 U1+U2 两脚，同参数 P1/P2 SDK 告警归零；支持此前未完成跨页网络的解释，
但旧回包无明细，不能声称已知旧警告的精确根因。

三份新 composition 仅把 Drawed.showValue 从 true 改 false，其余电路/绑定/几何/预算未变；
重新完整 Compose/Apply 后，三页官方图各有一处黑色表格作者、无额外蓝色作者或越框 Name/Description。
Name/Description/Drawed 均 false,false，表格整体显示仍 true。未知显隐现场负例 not-run：
三页实际非空可编辑字段都已有布尔值，没有人为造错；该边界由既有离线 83 项复核覆盖。

#266 的新 P3 有 58 个唯一 wire/connect mutation 请求，56 个首读通过，两条线实际追加到第二读。
wire-000 的 req_60 首读 seq2739 无线且 coverageError，次读 seq2740 出现同一新 PID `b72b4d155bd2250e`，
完整几何检查通过。176 条对应原始审计、逐步 observations 与 readbackAttempts 均冻结；
只声明 typed mutation 未重复，不推断 SDK 内部调用次数。

最终原生包 SHA-256 `62641b2d555e5d98b86187ff34ec4f11ed425009442a0aaf9d8e6e65dd9be285`，
ZIP 有效、restoreVerified=false；官方原生网表 SHA-256
`4feb9a1c2f2f2d3a7f67d6f30a6ed17c0cbc29696dd241c21da5b1a44e17e3c6`。
PCB 184 条原生记录包含文档头均与本批起点相同，未做 PCB 调用。
本批成本已 record：墙钟 16.206 分钟、daemon 2.156 分钟、差值 14.050 分钟，756 调用；token 未记录。
M1 历史有限范围不扩签；F1/F2/E1/L2 交新独立复核，L1/N1/R1、PCB 与完整发布仍未运行。

### 最新有限独立结论

以上候选已独立通过 F1/F2/E1 本次三页子集，以及真实图签 L2 参数修正子例，无 blocking findings。
独立数据断言 4535 项通过；另逐张查看三页官方 PNG。174 份原始输入及 42 份派生材料哈希有效。
主报告 SHA-256 `93bb42397521ea5c4693771550160472a25c58ea1c6659cf7180ad2d7f54c264`，
详细稿 `ff7fbf2bb87f2d8e2c188860f08c9bdad00bf9e99017d32c0d469ffbce760af7`，
输入 `40e95ab24b36826b9cdb2ddc43b652e617dffe7bb21828a4c31bc4d36738a3c8`，
清单 `398b4fad74205960d9efbf9172b68bcf731ee58d4db64f2b622ac4bdb567aaeb`。
目录为 `artifacts/release-v1.8.0-e2e-20260927/a01-final-schematic-independent-review/`。
#262/#264/#265/#266 按各自源码修复和真实原理图范围关闭，不签完整 E2E 或发布。

P2 的 42 条隐藏 pin 属性（21 Pin Number、20 Pin Type、1 Pin Name）有 38 AlignMode、12 Y、11 X 差分，
前后显隐全部 false,false，内容和父属不变。P1/P2 有 623/388 个属性运行期 ID 重铸；P3 回退及后续
重载也保留 171 个属性 ID 的映射。官方 pin SVG 仅内部绘图 ID 变化，P2 的 R5.1 另有
`5.684341886080802e-14 raw` 浮点尾差；按 `1e-6 raw` 容差核对、原始差值不删。
P1/P2 原生记录无新增/删除，data 仅三个预期图签 ATTR 显隐及 DOCHEAD 时间/版本变化。
这些证明范围内持久化及可见几何未被新增改错，不证明隐藏属性原回包全等或所有宿主内部状态稳定。

#266 两次可观察追加读取间隔分别 257.330/253.717ms，首读开始到末读结束 383.330/292.717ms，
外层耗时 499/339ms；58 个 typed mutation 一一匹配队列、无重复。原 176 行审计不含逐次完整 context/seq，
seq2739→2740 和 seq2785→2786 出自 geometryGuard observations，不从源码补造缺失回包。
迟到/取消/漂移负例仍仅签原离线修复复核；不证明所有宿主延迟都小于 2s。

### N1 自然过期状态子例

后续另批目录 `artifacts/release-v1.8.0-e2e-20260927/n1-stale-rejection-20260927/`，
45 文件清单 SHA-256 `e4d5f09dccb8e9bb13c0d2d18b4860995b2b8ff3d6f43bba411fdc17702b684e`，
原 146 文件未改。实际运行 P3 旧 73 步队列，SHA-256
`e9407ad6b376ed6373c966165f89c2578190c8837e9a43a8b7ca9c91a0ab2ce9`，新 journal 独立。
CLI exit 1，在第 2 步 verify-source-before-reset 因新增 marker 的 sourceScene 漂移拒绝；
没有到第 4 步 save 或之后 NC/wire，不冒称是 wires=0 断言触发。

57 条原始审计均为只读动作，0 设计 mutation；三页全部 85294 个 result 叶值逐项原样一致，
两份原生备份的八个 ZIP entry 解压内容逐字节相同，PCB 184 条记录一致。本批未执行保存重载，
不把备份内容不变说成重新导入恢复已验证。最后现场调用 UTC 2026-09-26 21:09:33，窗口释放。

本地 readiness 辅助断言错误拒绝显式 Mutates:false，外层编排未依据该失败短路；两项瑕疵及原始
错误保留，不签失败预检通过。独立按完整队列、原始审计、前后数据重新检查 302 项均通过，
仅签真实自然过期 sourceScene 写前拒绝子例，无该子例 blocking finding；不覆盖其他 N1/R1/PCB 分支。
独立报告 SHA-256 `920b26c7e01774e099a8fdaa9ced1f0dc76ad82a654de4a823e30c7c34b81c51`，
输入 `c0e85ce2174fd9376264e715f38def26fb19ddb334de1f8afb6fcc3fccdfe1bc`，
八件独立清单 `47bbe3c5cc79816b2eb033c3ea6e1662a90090bfe33c964ea7d1e6176e5dabe7`。
