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
