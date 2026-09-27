# CLI 已知问题与使用建议

更新于 2026-09-27。本轮问题按各自证据修复或继续跟进；使用方式与受影响分支见下表。
此前基础 CLI 集中检查已收尾；日常可用范围和新发布验收进度见 [CLI Status](cli-STATUS.md)，本页维护问题细节。
各轮现场依据见[历史证据索引](reviews/README.md)及对应 issue。

## 当前跟踪

| Bug | 影响判断 | 当前使用方式 |
|---|---|---|
| [#256：原理图 modify 间歇 cmdKey](https://github.com/zhoushoujianwork/easyeda-agent/issues/256) | 暂定 P2：可能中断单次修改或批次；本轮对照未复现，根因未知，不能估计发生率。 | 保留命令和失败退出；失败后核对目标与实际字段，再决定后续操作。 |
| [#257：NC 清除间歇未生效](https://github.com/zhoushoujianwork/easyeda-agent/issues/257) | 暂定 P2：涉及引脚电气属性；现有 CLI 已通过回读检测本次未生效并非零退出。 | 保留命令；清除后检查 noConnected，关键结果保存重载复核。 |
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
autosave 按窗口计时，未携带 mutation 的文档身份，显式保存成功也未取消已有 timer；
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
| 导出后删除残留 | [有界只读就绪检查与删除三态](reviews/2026-09-26-sch-delete-fix.md)，dev.4 定向回归及保存重载通过 | 仅覆盖已复现触发路径，不代表所有复合清理已验证；此前失败候选保留在报告中。 |
| 后台重连 | [沙箱时钟、注册等待与 context 身份修复](reviews/2026-09-26-cli-reconnect-fix.md)，dev.7 B02 通过 | 连接恢复不证明页面写入或设计正确。 |
| 同文档连接互踢 | [连接身份修复](reviews/2026-09-26-window-identity-fix.md)，dev.9 复测通过 | 更换连接不能修复或补签 #55。 |
| 页面改名失败漏报 | [dev.8 定向失败传播验证](reviews/2026-09-26-page-rename-fix.md) | 实际改名仍失败，见 #55 与 [dev.9 B04](reviews/2026-09-26-v1.7.1-dev9-B04.md)。 |
| 铺铜参数不符漏报与 Apply 继续写入 | [边界回读及失败传播修复](reviews/2026-09-26-pour-readback-fix.md)，[dev.11 错值检测与清理复测](reviews/2026-09-26-v1.7.1-dev11-pour-live.md) | #260、#261 的实际字段未修复，正向成功路径未通过；`rebuildAttempted:false` 不保证无铜，失败库存带 staleRisk，不能据此验收铜持久化。 |

## 观察项与覆盖缺口

R8 首次回读 uniqueId 从空变为 gge1 暂记观察项，赋值时机未知；第二次完整器件记录一致。
尚未证明这是产品 bug，详见[刷新报告](reviews/2026-09-25-v1.6.0-dev20-reload-repair.md)。
真实团队创建等属于覆盖缺口，也不混作产品 bug。历史某次成功不关闭间歇问题；
各 issue 的采集要求和关闭条件继续适用。
