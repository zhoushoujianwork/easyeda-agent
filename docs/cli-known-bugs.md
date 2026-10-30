# CLI 已知问题与使用建议

更新于 2026-10-05。本轮问题按各自证据修复或继续跟进；使用方式与受影响分支见下表。
此前基础 CLI 集中检查已收尾；日常可用范围和新发布验收进度见 [CLI Status](cli-STATUS.md)，本页维护问题细节。
各轮现场依据见[历史证据索引](reviews/README.md)及对应 issue。

## 连续 PCB 修改后宿主白屏 / connector 离线

[#286](https://github.com/zhoushoujianwork/easyeda-agent/issues/286)：macOS 宿主连续修改后白屏，
每批 5 件、间隔 1 秒仍复现；根因未确定。源码已确认并修正每次 `doc switch` 对活动目标
重复调用 `openDocument` 的路径，保留新鲜身份复核与 CLI 数据就绪检查；显式指定 split 时仍
按指定位置打开，缺少有效 tab ID 时不复用。150 次活动 PCB 检查及 split、身份漂移/不可读、
无效 tab 对照由离线回归覆盖。这不是白屏现场修复证明，也不是真实 reload，不能清除
staleRisk 或代替 `save → doc reload → fresh readback`。没有已验证的安全批次大小、等待
秒数或宿主版本处方。

连接器离线、`StatusGoingAway` 或主进程存活都不能单独判定 renderer 崩溃。失败后停止写入，
不重放不确定步骤；私有证据中保存精确构建/宿主版本、新鲜 health、最后请求、成功保存点及
断线前后 daemon 日志。daemon 日志只证明传输断开；页面退出需宿主的导航/关闭事件；renderer
失效需宿主记录的 renderer 退出/无响应事件或崩溃转储。缺少对应事件时原因保持 unknown，
不要把重连或限速签为根因修复。

候选加载与现场验收仍 pending。另行预约独占可丢弃 fixture 和写入范围后，分别对照只读空闲、
单步 modify、modify+save、modify+save+真实 reload，一次只变一个因素，记录身份、耗时及
持久化对账。出错即保留部分状态；新鲜绑定目标后再评估恢复，不自动重开或重试。
仍需独立验收者在精确候选 HEAD 上复验白屏场景，本项不因离线测试通过而关闭。

## 封装构建与重新绑定

本地 dev.9 候选补齐 `library.footprint.build` 的逐侧阻焊/钢网扩展字段，保留零值；
普通 SDK `get()` 将开窗舍入到 0.1 mil，逐 pad 改用公开 `toAsync()` / `reset()` 精确回读，
明确保存后再次核对。无法独立证明规则策略时拒绝 `null`，无数值约束的空对象也拒绝。CLI/Apply 对 `verified` 缺失、false 或 `partial:true`
强制停止，保留响应，不允许 retry、verify 或 continue 掩盖写入失败。

原理图重新绑定时区分实例 UUID 与库资产 UUID；原生 DEVICE 的来源记录可精确锁定库器件，
再用 Device.get 校验型号、供应商料号及原生 FOOTPRINT 来源关联，避免搜索影子候选干扰身份。
没有 DEVICE 来源时，稀疏搜索结果须经 Device.get 补齐，结合原库限定搜索和同文档原生来源证明身份。冲突、缺证或上下文漂移时拒绝写入；
回退也需验证原库身份。自动测试与现场保存重载分别记录，不能由工具成功推断制造资格。

## 工程缺陷分诊

2026-10-05 用已有 Web 工程作真实回读 fixture，评估的是 easyeda-agent 的采集、
模型与判定链，不是该电路的设计质量。以下未指定 issue 的条目尚未登记远端；
本地修复不代表正式版本已包含，也不把代码负例称为现场破坏性复现。

| 工程问题 | 当前证据与影响 | 状态与后续 |
|---|---|---|
| `doc reload` 没有确认保存成功 | 原实现只检查请求错误；`ok:true/saved:false` 后仍可能关闭、重开。 | 本地候选已修为必须布尔 `saved:true`；SCH/PCB false、missing、null、字符串 true 负例通过。现场状态按本节验证边界记录。 |
| `pcb check --strict` 漏拦仅 live clearance 的 ERROR | live 注入只增加 errors，严格模式只看 warnings；其它 WARN 会掩盖该缺陷。 | 本地已修计数及 ERROR 守卫；孤立错误、普通诊断、安全间距对照通过。 |
| 请求的 PCB pads/bbox 读取失败被吞掉 | connector 捕获异常后仍返回组件数量；pads getter 返回 undefined 又转换成 `[]`，没有 availability/error。离线真实 handler 已复现两分支；specialPad getter 异常也变成 null，仍可能根据基础 shape 发布宽高。 | 本地候选已修；availability/error、失败 pad 身份与成功部分保留，传递到 snapshot 和检查 coverage；真实 handler 异常注入正负例通过。 |
| 未知焊盘几何没有贯穿布局判定 | 真实回读有 8 个 POLYGON 源数组；net-path 明确 unsupported，但部分投影丢 shape/rotation，layout-lint 跳过无尺寸焊盘且仍只按发现的问题计算 ok。 | 本地候选已修覆盖传播：保留源几何、解析原因与逐 pad 未验证列表，显式门槛/strict 拒绝 incomplete；POLYGON 的精确铜几何仍 unsupported，未证明样例实际存在短路。 |
| 方形焊盘角部铜重叠被布局纯核漏报 | 最小离线输入：TOP 件的 TOP 20×20 mil RECT 与 BOTTOM 件的 MULTI 20×20 mil RECT，中心相差 (18,18) mil，异网在 TOP 实际重叠 2×2 mil。投影丢 shape 后，纯核按宽高相近当圆，漏报角部接触。 | 本地候选已修、P1：使用真实 shape 及共用几何内核，跨装配面/共同铜层的矩形重叠和真圆对照通过。旧包 ok:true/100 的失败保留；这不是现场电路短路。 |
| PCB DRC 的诊断结果与进程码不一致 | 现场 `passed:false`、有违规的 `pcb drc --json` 仍 exit 0；当前 CLI 成功打印报告即返回 nil。 | 本地候选已修：原生 passed:false 或结论缺失非零退出，JSON 保留 nativeResult，raw 保留完整回包；SDK 本身统一严格模式，不将 `--strict` 无差异另记为宿主 bug。 |
| SCH check 的 INFO 被合并器改判失败 | connector 对 INFO-only 返回 passed:true；CLI 合并 marker 几何发现后用 findings 非空覆盖成 false，现场 INFO-only 的 passed:false 与 strict exit 0 相矛盾。 | 本地候选已修：按阻断 severity 计算最终 passed 并对齐文字，INFO-only/fatal/error/warn 的最终报告与退出状态正负例通过。 |
| 快照缺少跨文档身份与封装引用 | 原始 list 保留 uniqueId，board dump 与 connectivity 投影不保留完整关联键；connectivity 转换还把原生 footprint 对象按字符串读取，实测封装引用丢失。只用这些 IR 无法可靠对账。 | 本地候选已补 native uniqueId、封装原生对象和快照来源；身份正负例通过。保留机械/NC 例外，不按同位号猜 join。 |

另有 14 个 ELLIPSE getter 元组的尺寸疑点。原始数字由宿主 getter 返回，我们的
width/height 推导假定同一单位；文档源码不含封装 PAD 几何，无法判定根因在宿主、
库记录还是适配。保持待定位，不自动乘换算系数，不称已证明制造数据损坏。

保存目标与后台确认继续按 #268 跟踪。候选 autosave 从修改回执冻结 project/doc/type，
按文档防抖，连接器 FIFO 执行前核对 expectedContext；切页或缺身份时不改存其他文档。
独立 save、typed HTTP/apply 和后台 save 均按布尔 saved:true 确认，失败进入 health/audit。
旧 timer 不得清除较新修改，已确认显式保存只取消比该请求旧的 timer。模拟连接器的同窗
切页、SCH/PCB、timer 竞态、false/缺身份/断连负例与 race 检查通过；现场持久化状态单独记录。
这些修复不证明历史 false 的宿主根因已解决，也不证明此前发生过写错文档或数据丢失。

本地 `1.9.1-dev.4` 已在 Web 4.1.60 完成定向复验，CLI、daemon、Skill 与两个运行窗口
精确对账通过。原生 PCB DRC 的 false 与完整明细保留且 exit 1；SCH INFO-only 为
passed:true、strict exit 0。真实焊盘采集完整，但 8 个 POLYGON 与 14 个 ELLIPSE 的
精确检查仍 unsupported；lint/score 明示 incomplete、ok:false，strict check 拒绝通过。
PCB UID 及 SCH connectivity 的 UID/原生封装引用保留；PCB list 本身未提供封装库引用，
不能把投影保留能力写成已补齐该宿主采集接口。

专用测试页的参数化孤立线获得目标绑定的 autosave saved:true；随后显式保存、真实重载
与 fresh 回读通过。同窗立即切到 PCB 的负例将后台 SCH 保存拒绝为 PRECONDITION_REFUSED，
health 保留 failed/saved:false；错误目标的 typed Apply 保存同样拒绝。两条临时线按 fresh ID
删除后，保存重载恢复原有器件、引脚、导线与其他绘图库存。后台保存与显式保存的证据分别
记录；宿主图签更新时间及部分衍生属性 PID 可变，不称原始快照逐字恢复。
不把含显式保存的 reload 当作独立证明后台落盘的试验。异步写入期间漂移、旧回执及
timer 竞态另有离线真实 handler/daemon race 负例；前后身份采样仍无法排除中途切页再切回的 ABA。
本轮为工程缺陷定向验证，完整 ESP32 设计 E2E 与严格 B00–B10 全集未补签；该开发包未正式发布。

源码 getter 成功但缺封装库闭包属于尚未完成的文件后端范围，不是已发布导入功能的
回归；继续按[源码验证边界](../.agents/skills/easyeda-agent/references/document-source.md)研究。

基础单动作复验另发现：首次引脚导线创建、精确删除均成功，随后按相同输入再次创建
返回宿主 `create failed!`，CLI 非零退出；完整回读、保存重载后连接库存为空。
根因未知，未加写入重试；新空页首次导线创建成功不补签这一生命周期失败。
操作边界见[连线参考](../.agents/skills/easyeda-agent/references/schematic-wiring.md)。
页面改名 #55、边界线宽 #260、铺铜优先级 #261 在本地 `1.9.1-dev.4` 仍复现，
NC 清除本次成功也不关闭 #257 的间歇问题；严格基础准入仍未通过，完整设计 E2E 未开始。

持久化另有一项语义缺证：PCB 空网的原生 `RULE_SELECTOR` 在保存重载后新增
`NET_LENGTH_TOLERANCE=["default",null]`。实际组件、焊盘、板框与铜的原生数据保持，
但 SDK 的 `getNetRules` 没有返回空网选择器，现有默认规则不足以证明其有效规则等价。
暂记覆盖缺口，既不判无害也不登记为已确认产品故障。隐藏属性 getter 坐标变化须与
原生父属、键值、显隐、坐标以及真实焊盘/可见位号逐项核对，不能只忽略坐标或比较哈希。

## 当前跟踪

| Bug | 影响判断 | 当前使用方式 |
|---|---|---|
| [#258：规则首次初始化出现微小差异](https://github.com/zhoushoujianwork/easyeda-agent/issues/258) | 暂定 P3：已观察差值 0.0000006 mm 很小，但非请求字段变化与校验失败仍需定位。 | 展示完整差分，保留 verified:false；已冻结自定义规则的后续操作单独验证。 |
| [#55：页面改名被拒绝](https://github.com/zhoushoujianwork/easyeda-agent/issues/55) | 暂定 P2：dev.9 B04 官方返回 false，fresh 名称仍 P2，基础验收失败。 | 重新打开原 issue；失败传播已修复，实际改名仍待定位。保留非零退出，不用无关写入或重复改名恢复。 |
| [#260：区域/铺铜边界线宽 1→0.2 mil](https://github.com/zhoushoujianwork/easyeda-agent/issues/260) | 暂定 P2：显式参数未兑现，B08 失败；未证明电气损坏程度或三个对象类型同源。 | dev.11 已能 fresh 检出并停止依赖；宽度本身未修复。保留 ID，精确清理并保存重载核对。 |
| [#261：铺铜优先级 2→1](https://github.com/zhoushoujianwork/easyeda-agent/issues/261) | 暂定 P2：请求与回读不符；单个 pour 的排序语义未确定，不直接归因为宿主 bug。 | 当前默认宽度正向测试失败。先明确合法范围及排序契约，再修适配或写前拒绝；不得静默改参。 |
| [#267：保留器件清页的引脚属性保护](https://github.com/zhoushoujianwork/easyeda-agent/issues/267) | 完整 global 属性库存下可能误计划删除 pin-owned 属性；仅隔离 dry-run 复现，未执行现场删除。 | 暂停 `sch clear --preserve-parts` 及包含它的队列；只有完整范围外保护证明成立才可精确局部回退。 |
| [#268：切页后后台 PCB 保存返回 false](https://github.com/zhoushoujianwork/easyeda-agent/issues/268) | 两批后台 `pcb.save` 外层 ok:true、saved:false；精确原因尚未确定，未观察到设计丢失。 | 外层 ok 不代表保存成功；稳定检查点核对显式 saved:true，再真实重载与 fresh 回读。 |

本轮剩余八项保持 open 并加 `bug` 标签；优先级是当前分诊判断，不是已证明的损坏程度或发生概率。
#271 位号误报 clean 已由`83a98b2`修复，dev.14 实际51位号保存重载及独立复核通过，按该范围关闭；
不签完整布局或E2E，旧失败保留，见[修复记录](reviews/2026-09-27-pcb-silk-align-fix.md)。
#256 关联已关闭的 #210，但不继承其已撤回的根因主张。#256 与 #257 也尚未证明同源。

2026-09-26 按用户要求，将本轮尚无可靠修法的三项登记为待后续修复：复用并重新打开 #55，
新建 #260、#261。每项包含参数化复现、实际回读、已修复边界、证据与独立复核哈希、
后续排查和关闭条件。历史 #55 的缓存猜测、任意写入或重试处方已明确不再作为当前建议。
这表示当前尚不能验证根治，不表示永久无法修复；单纯登记 bug 不改变验收范围。

2026-09-27 用户另行明确接受 #55、#260、#261 为 v1.8.0 的已知限制，
移除它们对高级用例与发布的整体阻断，见[版本范围决定](releases/release-1.8.md#已接受的已知限制)。
不等待三项修复或因此重复基础全集；issue、实际失败和错误退出仍保留，其他功能按回读继续。

## 高级流程修复与边界

三页 F1/F2/E1 和真实图签 L2 子例已完成有限独立复核；以下修复已采用并关闭，
原失败与各阶段诊断保留在[续测记录](reviews/2026-09-27-cli-schematic-gate.md)及
[详细执行记录](releases/evidence/v1.8.0/test-report-detail.md)。不签 PCB 或完整 E2E。

| Bug | 当前结论 | 验证边界 |
|---|---|---|
| [#262：部分功能区无法生成布局候选](https://github.com/zhoushoujianwork/easyeda-agent/issues/262) | `8c4d3e4` 通用修复，十区离线及三页现场复验独立通过，closed | 只签本轮参数源与原理图；有界搜索失败仍不证明数学上无解。 |
| [#263：超过 1 MiB 响应静默截断](https://github.com/zhoushoujianwork/easyeda-agent/issues/263) | `d3409a0` 完整读取、超限报错及 P1 保存重载恢复独立通过，closed | 动作响应 32 MiB、health 1 MiB；超限明确失败，不能删减必要字段或盲重放。 |
| [#264：图签文本强制显示](https://github.com/zhoushoujianwork/easyeda-agent/issues/264) | `61d0173` 源显隐合同与 `37449f2` 写前检查，三页图面/持久化独立通过，closed | CLI/Compose 保持已知布尔、拒绝未知显隐；未知显隐现场负例仍 not-run，raw typed HTTP 不外推。 |
| [#265：无接点交叉误报](https://github.com/zhoushoujianwork/easyeda-agent/issues/265) | `5ab4b40` 完整库存与线段对账，P2 完整 strict/SDK 独立通过，closed | 只允许有完整证据的内部 X；端点/T/共线/本体/缺测仍拒绝，合法交叉保留为 info。 |
| [#266：导线写后即时库存暂空](https://github.com/zhoushoujianwork/easyeda-agent/issues/266) | `8877942` 有界只读回查，两次真实第二读及三页保存重载独立通过，closed | 每动作只写一次；过期、取消、漂移、非法几何仍失败，不承诺所有宿主延迟小于 2s。 |
| [#269：PCB 导入重复点击/实例](https://github.com/zhoushoujianwork/easyeda-agent/issues/269) | `698980e` 单次确认/身份检测及失败传播，dev.12 单次 0→51 保存重载和独立复核通过，closed | 5934 项有限证据断言；195 源脚/203 焊盘/33 网闭合。保留旧 101 实例与 238 几何差分，不证明旧副本的唯一根因，不签完整 E2E。 |
| [#270：多边形焊盘绝对路径未投影](https://github.com/zhoushoujianwork/easyeda-agent/issues/270) | `faac8be` dev.13 修复及实际4个J1单环L焊盘平移/save/reload独立通过，有限closed | 18731项证据断言；完整路径逐值相同，中心最大0.08mil差保留。孔/ARC/非零旋转只离线覆盖，net-path POLYGON仍unsupported，不签整板。 |

本轮三页 195 脚/33 网/41 条外围/15 个 direct 树对账通过，SDK strict 各 0 fatal/error/warn。
隐藏 pin 属性的 getter 差分、runtime ID 重铸和浮点尾差已按父属、显隐、官方图面与原生数据逐项核对；
不称属性原回包全等。#266 原始审计缺逐次完整 context/seq，seq 证据来自结果 observations，不补造缺项。

[#267：保留器件清页遗漏引脚子级属性](https://github.com/zhoushoujianwork/easyeda-agent/issues/267) 保持 open。
隔离真实 handler 的完整 global 投影 dry-run 将 153 条 pin-owned 属性列入 orphan 删除计划，却返回
instancesPreserved:true；global 为空对照仅显示保护覆盖不足。两种复现均零删除，未发生现场损坏，
也不证明宿主 unscoped getAll 必然完整。后续需补官方引脚父属闭包、属性核对、正负例及现场保存重载。
本批精确单线回退另有完整保护证明；不能把它外推为广义清页安全。
有限独立报告 SHA-256 `66d2aa66588a6e333d2fde1e7d20aaa9d729a93264962c86ea8fe4fd4b864cc5`，
输入 SHA-256 `c76e4c9415310d5ec7f1085066d79e338e52c5f5ce5b28a02e39027d9883c58d`。

[#268](https://github.com/zhoushoujianwork/easyeda-agent/issues/268) 保留实际失败与后台保存归属的排查要求。
修复前 autosave 按窗口计时，未携带 mutation 的文档身份，显式保存成功也未取消已有 timer；
这些实现事实不证明它就是 false 的根因或保存了错误文档。两批均已通过显式保存、真实重载和
完整数据/原生差分核对清理结果；不能将后台失败改成成功，也不能推定发生了数据丢失。
后续补切页时序、timer 竞态和目标身份的正负例及现场持久化复验。

[#269](https://github.com/zhoushoujianwork/easyeda-agent/issues/269) 的重复实体已按 101 个 fresh ID 精确删除并保存重载，
三页源和其他工程数据未变；PCB 原生比原始多 464 条空 payload 历史记录，不称逐字恢复。
候选与失败证据见[导入确认记录](reviews/2026-09-27-pcb-import-confirm-fix.md)。

## 本轮仍缺的验证

1. 三项 bug 的实际行为修复及保存重载复测转为后续跟进，不作为本次启动完整用例的前置条件。
   本次新设计实际使用的铺铜 `verified:true` 正向分支仍需验证，不能用错误检测代替。
   对 #261 若确认输入超出官方合法排序范围，应在写入前拒绝，并另测合法正例，不能把原失败改判通过。
2. 若要声称严格基础全通过，仍需同包 B00–B10 全集及独立复核；本次准入使用已接受范围，
   不因此强制重跑全集。各版本状态见 [CLI Status](cli-STATUS.md)，不混用旧通过记录。
3. 历史 A00 blocked（原选型测量 22/35）记录保留。本轮另从原始需求开始，已完成最终 51 位号测量，
   原 A01 布局失败保留，最新三页原理图已现场通过并完成有限独立复核；PCB 同步/布局、用户布局确认、
   布线/DRC、最终落盘复核及 L1 尚缺；旧 R1 仍 partial；新 dev.12 批已补恢复完成态真实 reload 和最终清理，有限场景独立通过；自然过期 sourceScene N1 子例已独立通过。
   范围和顺序见[高级验收](cli-advanced-test.md)，已确认的复用工程与叠层 A 保留。

上述是覆盖缺口，不另建成“已确认产品 bug”，也不拿离线检查或错误检测通过代签设计完成。

## 警告还是下线

采用**受影响操作的定向提示 + 失败退出 + fresh 回读**，暂不整体下线。
dev.21 已在 `sch modify`、`sch no-connect --clear` 和 `pcb config` 规则写入前输出
stderr warning，包含 bug 编号、影响及回读方法；stdout 的原始 JSON 保持不变。
读取、帮助、规则 `--dry-run` 与 NC 设置不输出这些专项提示。
原理图写入的 `partial` / `verified:false` 也会非零退出，保留回包及已写入部分的自动保存。
提示不代表本次失败或 bug 已修复；#256、#257、#258 继续跟踪。

遇到实际失败、partial、verified:false 或回读不可用时，停止依赖该结果的后续步骤。
警告不能把失败改成成功，不能替代保存重载，也不自动重试不确定的写入。

若后续发现写错文档、未被检测的部分写入或持久化结果无法可靠核对，再暂停受影响的
具体命令/分支，修复并复测后恢复；其他独立 CLI 功能继续按各自结果使用。

## 已修复路径与验证边界

| 路径 | 已验证的修复 | 仍需区分的边界 |
|---|---|---|
| 导出后删除残留 | [有界只读就绪检查与删除三态](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-26-sch-delete-fix.md)，dev.4 定向回归及保存重载通过 | 仅覆盖已复现触发路径，不代表所有复合清理已验证；此前失败候选保留在报告中。 |
| 后台重连 | [沙箱时钟、注册等待与 context 身份修复](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-26-cli-reconnect-fix.md)，dev.7 B02 通过 | 连接恢复不证明页面写入或设计正确。 |
| 同文档连接互踢 | [连接身份修复](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-26-window-identity-fix.md)，dev.9 复测通过 | 更换连接不能修复或补签 #55。 |
| 页面改名失败漏报 | [dev.8 定向失败传播验证](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-26-page-rename-fix.md) | 实际改名仍失败，见 #55 与 [dev.9 B04](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-26-v1.7.1-dev9-B04.md)。 |
| 铺铜参数不符漏报与 Apply 继续写入 | [边界回读及失败传播修复](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-26-pour-readback-fix.md)，[dev.11 错值检测与清理复测](reviews/2026-09-26-v1.7.1-dev11-pour-live.md) | #260、#261 的实际字段未修复，正向成功路径未通过；`rebuildAttempted:false` 不保证无铜，失败库存带 staleRisk，不能据此验收铜持久化。 |

## 观察项与覆盖缺口

R8 首次回读 uniqueId 从空变为 gge1 暂记观察项，赋值时机未知；第二次完整器件记录一致。
尚未证明这是产品 bug，详见[刷新报告](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-25-v1.6.0-dev20-reload-repair.md)。
真实团队创建等属于覆盖缺口，也不混作产品 bug。历史某次成功不关闭间歇问题；
各 issue 的采集要求和关闭条件继续适用。
