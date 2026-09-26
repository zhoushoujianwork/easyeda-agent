# PCB 多边形焊盘投影详细记录

面向用户结论见[主记录](2026-09-27-pcb-polygon-projection-fix.md)。

## 来源与失败

输入保留原始 ESP32 需求的自主选型来源；目标 PCB 保存重载后有 51 唯一器件、203 焊盘、
33 网，2 层，无板框或铜。J1 TYPE-C-31-M-12 的 4 个 GND/VBUS 焊盘使用板上绝对
`POLYGON` 路径，12 个 RECT/OVAL 为相对尺寸对照。原生 footprint 与实例 Device/Footprint
绑定、逐顶点及普通形状对照保留在 supplement，不从截图推测几何。

`artifacts/release-v1.8.0-e2e-20260927/a02-dev12-layout-20260927/` 冻结 325 文件，
manifest `5a55f66bb4f3c0a92732a56184ca5d8d23470a3f37ba9b54140c6cec42eec455`。
单 J1 最小 CLI 平移 `(4193.6685, 6784.209) mil`，4 个中心移动而 shape 原字节不变。
全部局部/整板离线候选 `candidate-rejected`，无现场调用或写入。
最小 board 中 `projection.changes.includedRefs` 继承六件模块名单；实际 components 仅 J1，
这项来源注释不精确单独保留，不据其虚报 MRE 器件范围。

独立 red 用冻结 dev.12 二进制重放原命令，exit0、candidate JSON 完整内容相同。
114 项来源/身份/闭合路径/相对尺寸对照通过、4 项正确几何断言失败；
原生到实际路径误差不超过 `2.55e-13 mil`，错误投影最大顶点偏差 `7975.734903 mil`。
17 件独立清单 `e7b6b0b5fa74dcd01e182c5df6f905c7d4f2c2c0641ebb1249872607e24c33a3`，
报告 `070394023605e5e78da211830a4c83e05daf1f80935a4f55633f61aea257e374`。
复核器首轮错误使用精确跨表示锚点相等，后续仅修容差，原错误与 4 个 red 保留。

## 修复候选

Skill 先明确绝对路径与相对尺寸语义。公共 `transformBoardComp` 返回 error，rigid、edge、
pin follower、crystal guard、reflow 与独立 routing check 调用均拒绝不可投影几何；
reflow 搜索把此类移动判为不可用，不交出部分候选。
POLYGON 递归复制轮廓并按同一 anchor/delta/offset 变换所有 L 和 ARC 端点，保留有符号 sweep；
原路径仍由公共 contour 解析器核验，不以展平结果替换输入。普通尺寸只复制，不作坐标变换。
未知 shape、非空 specialPad、非法路径或非有限投影参数拒绝；旧缺 shape 快照仅保留 bbox
语义，不宣称精确铜几何可用。

脱敏真实 USB fixture 保留 4+12 焊盘，回归四种角度、原源不变、相对尺寸；另有手算
孔洞/ARC/inverse/无共享数组和非法路径/生成器拒绝。POLYGON `net-path` unsupported 负例保留。
本地完整包 dev.13、source/package 输入 27 件清单
`8e7832f16980d870fd19517c420ec57381c650e38af8952de8f241ec84e4fd14`。
全 Go 4129 pass/0 fail/1 skip、15 包，connector 638 pass、typecheck/build/Skill/diff 检查通过。

根任务核对独立导入材料 30 文件成功；核布局批 manifest 时首次误用 bytes 字段，实际字段
为 size，修核后 325 文件全部匹配；独立 red 17 文件同样匹配。此为核验脚本错误，非产品缺陷。
独立候选复核、runtime 升级、现场 fresh 投影与完整布局/布线/DRC/E2E 尚未完成。

## 独立额外字段反例与候选修订

首候选可正确投影真实 POLYGON，但独立 CLI 发现 RECT tuple 多一个未知几何字段仍被接受。
首候选未安装，标 `candidate-rejected`，27 输入完整复制并冻结：
`first-candidate-manifest.json` SHA `158d7cfcf5e520f17a22e6056153b15c5abc8dc4acdb6033b07c9b9d03fe9fe3`。
仅投影 helper 补 RECT4、OVAL/ELLIPSE/NGON3 精确字段数量及四项拒绝回归；
不修改 net-path 原 parser 契约。重新全量 Go 4129 pass/0 fail/1 skip、15 包，完整本地包重建通过。
27 件修订输入 `candidate-inputs-v2.json` SHA
`880f3caf8d588908b58cae3008799bcd32a185d92136507a6f1b2c8261b71185`；
首候选输入 SHA 仅对应已冻结副本，不再据当前可变 dist/source 读取首候选。独立修订复核仍待完成。

## 最终离线复核与运行态升级

修订 v2 独立 224 项有界复验通过，报告
`911b4ee7658c66a5a5b1a0b2d177b73cd35e1243c74082bd98a2aa71bfd6cf00`，
38 文件清单 `ec13cfde8e33c3f8b499736342764d2382ecabf9e20b9f461c16f239ab6d91b2`。
提交并推送 `faac8be` 后完整包重建，来源代码/fixture/Skill 不变；Go VCS 与 ZIP 时间变化
另存新输入。旧两批 27 输入均原字节复制保留。最终 adopted 独立 487 项通过：
报告 `5fb5838b485299d58d66751577e7ce5d7391141b41d405065eb239b68a47f579`，
19 文件清单 `bb540539ab15cf50482b77261441a564fde958af986685e0badd9687cbb1a142`。
上述三份独立清单均经根任务重新核对全部大小和哈希。

最终 dev.13 eext `f23672534ac3dd01001cfda936864b857e3322cdbb5426d1d223c9f7a04f33ab`，
bundle `fe5dff2cf78edbe1093a1eb514bb7d7caaed3c95965ee1bf805a62bc98810b65`，
CLI/daemon/PATH 与完整包相同：
`ac0c830deca61785612f97f591c22bcc7e944d9707360d74dc560821ff862c1b`。
Go VCS 为 `faac8be`、modified=false。早先两个未安装包和不同哈希保留，不混用。

升级材料 `artifacts/release-v1.8.0-dev13-upgrade-20260927/` 冻结 173 文件，
清单 `a8fb7b8382018f13e68182dec6a91e763d56c1961387108ee475bdfdb16e0fab`。
4 页逐页显式 saved:true，已有 connector UUID/权限/旧版本/bundle 与独立本地旧包相符。
使用提交的 extension-only hot-reload 脚本；新的窗口
`ec89ce47-091f-44f8-84ee-4ea323961c24` 上报精确 dev.13、Web 4.1.60 和目标 PCB。
local check READY，51 件/203 pad 的完整 typed result 升级前后相同，board 除 capturedAt 相同，
仍为 2 层、无板框/铜；板框不可用的 partial 原文保留，不误称完整设计已通过。

三页全局属性库存 1797/1093/568（含 pin-owned），其中 runtime ID 重铸 623/388/258。
三张 sheet 的 @Create Time、P1 的 @Create Date getter Value 共 4 项变化；
同值在组件 otherProperty 与 pagePrimitives 组件中重复为 8 个字段差分，均保留。
其余属性按 Parent+Key 全字段一致，其余非属性字段一致；不声称整份 raw 回包相等。
原生 122 sections 的全部非 DOCHEAD 行逐字相同，7 个其他 ZIP 条目逐字相同；
122 DOCHEAD 的会话元数据和 section 排序变化保留，不推断 getter 的 SDK 根因。
升级前备份与导入批备份的 8 个 ZIP 条目也逐字相同。

root 先缺 --window 导出被写前拒绝，fresh 精确窗口后成功；PCB 比较 helper 两次误读
capture/stdout wrapper、随后漏算 51 个 CLI bbox center，均明确修核。
第一次升级 proof 先错误要求所有属性、继而非属性全等，实际存在上述明确 sheet metadata
差分；改为输出全部差分、定位父属和原生内容后建立事实，原 raw 与 supervision notes 保留。
这些是 root 编排/核验错误，不包装成产品修复。升级独立复核已由只读 Agent 完成；
现场窗口现由原始需求执行员独占，重做候选并验证实际 POLYGON、四层布局/L1。
#270 仍 open，待现场保存重载验证；全局布线仍等待两轮布局与用户确认，v1.8.0 尚未发布。

## 升级独立结论与首次叠层 partial

独立升级复核 4689 项有效断言、657 件输入末次哈希重验通过，无阻塞 finding。
报告 `f97f2b2d46981011e5a20dee8881751087dab563559bde161fd7c70c97241e0c`，
32 文件 manifest `6bc66eb27e383ac2fa72c19f03a3a772292c5cf736a0e496c5896068fa8ef1e8`，
根任务逐文件大小/哈希核对通过。122 DOCHEAD 的唯一字段差分为 client，3 section 换位；
不签全部 raw 相同。独立核验器误将铜 availability 元数据也要求为空，原 exit1 保留，
修为逐铜集合空且 availability 可用后通过；未放松几何库存。

`a02-dev13-layout-20260927/` 冻结 244 文件，manifest
`d4c6a71cca2d0ce8e1d24989c49e7e5fcdece8e69636568cf08778fbdfff756e`，根任务全部核对通过。
唯一设计 mutation req87 为 `stackup set --layers 4 --plane 15 --signal 16`：
setCount/countVerified 为 true；内层15 requested PLANE、written:false、actual SIGNAL，
partial:true/verified:false/CLI exit1。编排即时停止，未创建板框、孔、器件布局或铜。
随后显式 saved:true，完整 51 件/203 pad、铜/丝印/config 不变。
native 97 原始差异保留，94 仅 ticket，实际 payload 为两个内层 use/show 和 DOCHEAD；
PCB thumbnail 改变，其余6个其他 ZIP 项相同，三页原生 section 不变。
本 partial 批未 reload，018/020/022 raw envelope 与021 dump stderr 仍报告 staleRisk；
这里只签即时 typed 观察及原生归档的有限保全，不借 save 或 fresh 文案补签权威重载数据。

这是将最终类型提前请求的编排错误，与公开 Skill 的 vias→SIGNAL 有网铜→PLANE→rebuild
顺序不符；不能因此断言 written:false 的唯一 SDK 根因。续批先真实 reload，确认4层及
完整件/pad/copper/native 后，用两内层 SIGNAL 中间态规划布局；最终 GND PLANE 要求保留。
布局确认后正确顺序仍失败，应登记未满足需求，不以 SIGNAL 成品代签。
续批执行员已报告保存重载保持4层；其完整布局/L1/投影现场材料仍待冻结与独立复核。
根任务一次误读 review manifest 文件名产生 FileNotFoundError，正确读取 manifest.json 后
32+244 文件核对通过，无 EDA 调用或写入，核验说明另存 progress-root 材料。

叠层 partial 批独立 470 项证据断言通过，仅签当时停止写入与限定保全，原请求仍 partial/failed。
21 件复核文件 manifest `de88e744076fbbd409950aa5600c2d386f63fb9b368f887c7b663719902b79a2`，
报告 `579341f584ac9a87785b163e1a53aca7d85aad40d7d24733ed0b80d2a83dfd1d`，根任务全部核对通过。
独立首轮漏算 CLI 新增 bbox center 的两项失败保留；v2逐件计算后457项及13组raw audit配对通过。
本结论不把 staleRisk 或缺 reload 改为通过，续批真实重载证据另验。

## USB 已保存里程碑与有限关闭

续批 `a02-dev13-layout-resume-20260927/usb-milestone-manifest.json` 冻结424文件，
SHA `a9e76a80b5ce4251509ced7df5d8116337cbf59f483597303edc95568636d409`，根任务逐大小/哈希匹配。
四层 SIGNAL 中间态真实 reload 后重新取源；板框80×65mm、2mm直倒角已创建，USB6件队列
完整dry-run/8步Apply、显式save→真实reload→完整件/pad/dump/native执行完成。
J1实际四个POLYGON仅L单外轮廓，没有孔/ARC现场前提；完整路径与候选严格相同，
中心最大差0.08mil、bbox最大2.2737e-13mil，原始读值和差分保留，不宣称raw中心全等或SDK唯一原因。
45范围外component完整typed记录相同；原生59差分为21payload与38header-only，
三页原生payload不变。独立USB复核已冻结47件，18731项有效断言通过、460输入绑定、158原始audit闭合。
报告 `7689ea36aa70edb9c5cc7f6f168ec24865a313a4bea503dc48c4df47a46c1980`，manifest
`15939503e44e462e795e9af9390d325d2ae563e9c37d2758c66a86f35b74eed2`，root全文件核验通过。
据此[#270有限关闭](https://github.com/zhoushoujianwork/easyeda-agent/issues/270#issuecomment-5851051573)。
独立核验的三次脚本假设错误（POLYGON无width、PAD_NET header更新、只读pages.list漏白名单）
原exit1保留，不算产品缺陷。该USB批L1仍not-run，后续专项另验。
首轮编译因候选save动作及空payload格式两次拒绝，随后无queue的dry-run本地拒绝；
编排改为每步非零立即停止，未手改候选或重复设计mutation，原错误保存。

后续45件已另队列完成摆位并保存重载；机器浮点尾差与270°/约−90°回读差按mod360几何等价
记录，不用pad中心容差放松anchor/bbox/path。4个M3孔随后完成保存重载；首次整板typed诊断图
只用于查漏，不计连续两轮，root只读观察到数个位号文字需要数据检查。RF、丝印、合法完成态
整体移动L1与两轮完整布局仍待完成，不据诊断截图签任何几何/电气事实。

后续L1专项54文件清单 `a97d37007d5bb9d7d06938aae735d06a7327ad7311cf14d765e67f494bf1f890`
root逐文件核对通过，点灯D4+R7合法完成态整体移动/从fresh恢复已执行，独立复核仍待补依赖材料。
RF禁布区及位号对齐保存重载后，执行员发现silk-align报告aligned51/unresolved0/details clean，
但真实Designator bbox仍有J1-C14、R3-Q1、U3-L1三对相交，J2文字与完整footprint包络也有重叠。
依赖写入暂停、失败材料正在冻结；源spacing还是位置变换/采集缺口尚未确定，不以手改坐标兜底。
