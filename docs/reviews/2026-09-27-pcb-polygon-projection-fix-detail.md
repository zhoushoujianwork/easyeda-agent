# PCB 多边形焊盘投影详细记录

> 历史结论，仅为发布材料兼容保留；当前能力见 [CLI Status](../cli-STATUS.md)。
> 逐步运行记录与原始标识见[固定版本原文](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-27-pcb-polygon-projection-fix-detail.md)。

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

## 升级与叠层验证边界

修订 v2 和采用提交的离线复核通过；运行态升级的基线保全已独立核对。
首次叠层提前设置 PLANE 返回 partial/failed，原因未确定；后续四层 SIGNAL 仅为
布局中间态，不能代替最终 GND PLANE 或整板验收。该批停止与保全通过不补签原请求。
逐次安装、原始差分和独立复核清单由本页顶部的固定版本原文追溯。

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
