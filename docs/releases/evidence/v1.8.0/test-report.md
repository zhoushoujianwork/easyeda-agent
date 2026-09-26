# v1.8.0 执行报告

[发布准备](../../release-1.8.md) · [基准](baseline.md) · [用例](test-cases.md) · [详细执行记录](test-report-detail.md)

**完整用例进行中，尚未发布。** 三页、51 件电路及新单次 PCB 导入保存重载和有限独立复核通过；旧101件重复导入已精确清理。新R1、dev.13投影修复/包/升级有限独立通过；四层 SIGNAL 中间态已保存重载，布局/L1续测中，最终 GND PLANE 及完整成品未通过。

用户已接受 #55 页面改名、#260 区域/铺铜边界线宽、#261 铺铜优先级为本次已知限制，详见[基准范围](baseline.md#2026-09-27-用户接受的范围)。它们的历史失败保留，不整体阻断本次高级准入；其他需求、几何、电气、持久化和独立复核要求继续执行。

## 现场回读

目标工程 `0f46d4361c4741a9ab7a9ed62164ca9b`（ceshi-cli-basic-20260924），P1 `e453c0063919726b`、P2 `5c21ec9b670a7bb5`、P3 `b137c508566450c2`，PCB `3037fc1dfa4965d2`。全程使用内置浏览器 Web EasyEDA Pro 4.1.60 和 typed CLI。

本批 CLI/daemon `v1.7.0-45-g2c6cd35`，connector `1.7.1-dev.11`；窗口 `8f2f5dfa-b722-46f6-8f58-c993546c2671`，最后现场调用结束于 2026-09-26 UTC 20:46:08。bin/PATH SHA-256 均为 `8f49eac5e699482a6102895ec891f88f998ff163a844b8ebdbed8a5936f0a0a0`；开发戳不代表正式 v1.8.0。

最新 146 件冻结材料清单 SHA-256 `1410c197c426231d34f8638146d516aa1d0ccbed9ca77dc3d37a0fee78567ed8`，根任务重算全部文件大小与哈希一致。三页 195 脚（158 connected / 37 NC）、33 网、41 条外围归属和 15 个 direct 物理树与自主源及官方原生网表一致。每页 save→真实 reload→fresh 后布局、连接和 SDK strict 均通过，SDK 各为 0 fatal / 0 error / 0 warn；原 PCB 184 条原生记录未变。原生备份 ZIP 有效，重新导入恢复仍未验证。

仅将 `esp32MiniRequire.md` 第一节的 996 字节原始需求交给无历史设计上下文的执行员，输入 SHA-256 `e62ebb05ecedb82e103d81b31da2bbf26aefe0669e580961c3aae87fef7d74d0`；未提供预制 BOM、网表或布局答案。本轮自主选型、真实测量和源数据的各批出处保留在[详细记录](test-report-detail.md)。

| ID | 结果 | 当前证据和仍缺的验证 |
|---|---|---|
| M1 | pass | 最终 51 位号的真实身份、物理脚、bbox、位号几何及精确清理保存重载已独立复核；924 件冻结清单有效。NT L1 修订单独复核；不签 PCB 实例或原生归档重新导入 |
| F1 | pass | 仅签本轮三页参数源、连接/NC/外围归属及真实直连，独立复核通过 |
| F2 | pass | 仅签三页 15/13/73 步受保护执行、保存重载、位号/图签/图面及 strict 检查；属性差分按下文保留，旧失败不改判 |
| E1 | pass | 仅签实际三页静态电气及全部 195 脚与源/原生网表对账；不宣称实板上电、烧录或动态供电保证 |
| L1 | not-run | 待合法未布线 PCB 基线后执行核心及专属外围整体移动，验证连接/约束与模块外状态 |
| L2 | pass | 仅签真实图签错误的参数修正：三页黑色表格作者保留、额外蓝字及越框属性消失，独立复核无新增问题；不外推通用局部修复 |
| N1 | pass | 仅签自然过期 sourceScene 子例：第 2 步写前拒绝、0 设计 mutation，三页完整数据和 8 个原生条目内容未变；辅助预检失败/编排未短路保留，不补签该预检或其他负例 |
| R1 | pass | 仅签新 dev.12 两件非铜 region 受控中断/同源恢复，完成态 save→reload→fresh/native 先于精确清理及再次保存重载，独立复核通过；旧80失败/126partial保留，不外推整板布线恢复 |
| E2E | in-progress | 原理图、新单次PCB导入及dev.13升级有限独立通过；旧#270候选拒绝保留。四层SIGNAL中间态已保存重载，布局/L1续测中；最终GND PLANE、两轮布局及用户确认、整板布线、铜/DRC和最终保存重载导出未完成 |

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

最新 dev.12 修订全量 Go 4108 通过、0 失败、1 跳过，15 个包通过；连接器 638 通过、typecheck、Skill 与 diff 检查通过。剩余必测项和最终发布独立复核未完成，manifest 保持 `result=in-progress`、`independentReview=not-run`。

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
