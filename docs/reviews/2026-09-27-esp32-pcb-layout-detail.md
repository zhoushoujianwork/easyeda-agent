[返回布局复核](2026-09-27-esp32-pcb-layout.md)。以下只记录该未布线版本，不签整板 E2E。

## 环境和冻结输入

CLI/daemon/connector `1.7.1-dev.14`，采用代码 `83a98b2`，Web 4.1.60。
工程 `0f46d4361c4741a9ab7a9ed62164ca9b`，PCB `3037fc1dfa4965d2`。
原始需求仍为客户第一节，不以加工后的 BOM/网表作为新输入。

原始材料在测试机的 `artifacts/release-v1.8.0-e2e-20260927/`，不作为公开 Skill 的安装依赖：

| 范围 | 清单 | SHA256 |
|---|---|---|
| 六件电容修正，162 文件 | `a02-dev14-decap-adjust-20260927/power-refinement-manifest.json` | `760f3c7492e7668fd678fe46131393881046c94ce4937d087264608c750628ff` |
| 两轮布局，90 文件 | `a02-dev14-layout-rounds-20260927/layout-handoff-manifest.json` | `f66332b6d6ec91c93151e24d22f7c636927542cf7bc19f66db8e57b56b7ad7ba` |

根任务全量重算 252 文件大小与哈希一致，并查看第 2 轮整板 PNG。
两轮 PNG 同为 `c631507e637bcc9ff674d0a524c1872d0240702b82ec09ee760f28c78392c7c3`；
第 2 轮路径 `previews/layout-round2-persisted/snapshot.png`。这是新的 typed capture，
`captureKind=board-fitted-viewport-png`、`objectLevelExport=false`，不称菜单对象级导出。

## 修正及范围保持

原初轮因电容源关系问题不计通过，修正后计数清零。C3→U2.16、C4→U2.4、
C5/C15→U3.4、C11→U5.6、C14→D3.5，与原始归属对应。
C1 的真实矩形 pad 边缘距为 1.846mm；C2 是局部 bulk。保留二者并报告中心距离，
不以检查器 100mil 启发式单独判定厂商违规。C6/C7 归属 L1 输出，不按最近 U2 重排。

45 个未移动器件完整回包精确保持，包括 U1/J3。旧说明中的 43 是算术错误，保留原源字节并更正说明。
51 实例身份与 203 pad 网络身份保持，14 自由标记保持。位号允许集合是全部 51 个，实际变化 12 条；
另 8 条隐藏 Device/Footprint 属性随四个已声明电容旋转，角差逐项保留，内容和显隐不变。
三页原理图及非 PCB 原生 section 原文保持；第 2 轮与修正后最终板的 dump 除 capturedAt 精确相同，
完整器件列表和原生 section 相同。缩略图及 ZIP 差异单列，`restoreVerified:false` 保留。

## 两轮与剩余事实

正式第 1 轮为命令 013–017/032；第 2 轮为 020 saved:true → 021 返回准确 PCB UUID 的真实 reload →
022 dump → 023 list → 024 native → 025/026 check/lint → 027 fresh render → 028/033 离线事实核验。
两轮没有设计修正。覆盖 51 body、203 pad、65 可见文字、4 孔、4 RF、41 owner-pin，
195 源引脚反向核对 203 实际 pad：U1.39 对应九个实体 GND pad，额外数量有明确解释。

layout-lint 的 overlap/offboard/tight/short 为零；飞线交叉属于未布线诊断。
`pcb check` 仍 passed:false，保留 6 WARN/1 INFO；SDK 两轮均有 166 Connection Error。
四个 POLYGON 的实际 L 单环路径已核对，不外推孔、ARC 或该 DFM 内核完整支持。
C6/C7 GND→U3.2 的 10.885/11.193mm 是端点距离，尚无实际铜回流长度。
最终 GND PLANE 仍待布局确认后按 SIGNAL 上网绑定铜 → 切 PLANE → 重建的顺序验证。

250 条两轮审计（初始 78、正式 172）与六件修正的 129 条区间不重叠；两次 save 均 saved:true。
已记录 cost，token 未记录。初始短区间的 daemon 比例 208% 是首末请求窗口计算现象，
不作为并行 SDK 调用证据。helper 字段/返回类型误读均 fail-fast，原失败保留，未重复设计写入。

先前 14 标记检查点已独立复核 5,341 项、91 输入哈希通过，19 件清单
`7ba881aa3d91311816fecc006ea08a17324db5fd488bccf8080c46eaa5dd90fd`，报告
`105aa57b908bdc74bf82bb7ede26d90abfeb692f128a6044b1b236b90f01b378`。
旧 61 件空审计 finding 保留，另 7+2 件 addendum 闭合 179 原始记录；根任务均重算哈希。
标题的计划/持久化差与 native/getter 的 1ULP 差分别记录，不用后者概括所有差异。

## 独立布局结论

两轮完整包的独立复核已通过，支持展示当前保存的未布线版本，请求用户明确布局确认；
未发现必须先修改的新明显问题。4,764 项有效断言、329 输入末次哈希复验一致。
报告保存在 `artifacts/release-v1.8.0-layout-handoff-independent-review-20260927/`：

- 52 文件清单：`c75cda658ecc5020823ce3567f980a6746bee2f93c1c687068fc23955dcdb49f`；
- 报告：`2f8e518590e1377c853e4647b737e58b9f94db7f184de67dd1757d6740e1612b`；
- 详细稿：`da8093dd307d79c70c183ca06b2cd8ec90faaa38af664f2dec751b6259f87d3f`。

根任务重算全部 52 文件大小与哈希一致，并读取简版及详细结论。独立核验同时明确原 16 个
attachment 差异：六个错误目标已修正，九个为实际同网端点的物理摆放锚点，D3 .6/.1 是
官方说明的同一 I/O1 通道端点；逻辑 owner 仍保留，均不证明实际铜直连。
82 native raw 差异逐项保留：1 DOCHEAD、6 COMPONENT、63 ATTR、12 PAD_NET；
12 PAD_NET 仅 ticket 变化，网络 payload 保持。正式 172 audit 中无布局设计修改。
独立 helper 五次字段/范围假设失败及修正后的 exit0 都保存，零 EDA 调用。

用户尚未确认布局。未布线、最终叠层/铜、DRC 与完整发布均未通过。后续真实回流若暴露需要
布局修正，仍须重跑连续两轮并重新确认，不能沿用这次检查点的签署。
