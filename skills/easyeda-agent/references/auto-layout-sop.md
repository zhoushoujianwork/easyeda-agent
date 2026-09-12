# 原理图数据计算与 Apply 验证流程

新设计与整页重建使用 1.4 数据路径。数据契约见 [schematic-data.md](schematic-data.md)，
坐标、紧凑标题与存量工具边界见 [schematic-placement.md](schematic-placement.md)。
本流程不要求先运行 `autolayout` 或按固定分区拆页。

## 1. 准备电路与测量数据

确认工程和目标页，保留完整工程 connectivity 与每页的几何快照：

```bash
easyeda doc ls --project <project> --json
easyeda sch connectivity --all-pages --project <project> > project-connectivity.json
easyeda sch list --project <project> --doc <page> \
  --include-device-identity --include-pins --include-bbox --include-wires > page-before.json
easyeda sch sheet-geometry --project <project> --doc <page> --json
```

在副本中依据官方典型电路补齐器件、引脚和网络；修复非标准位号后再布局。
外围要围绕核心引脚并直接接线。已有网络与显式 NC 保持可追溯，不能把缺数据当作悬空或 NC。
临时输入、计算结果和回读证据保存在项目忽略的目录，原快照保留不覆盖。

## 2. 离线计算模块与单页组合

普通 zones 的本地效果先走固定链路：
`layout-plan --zones → layout-sheet-plan → layout-render`，所有区完整通过才出效果。
输入顶层 spacing 统一内边距、框间距和页边距；区内回退只影响本区，整页仅平移区框。
用户只授权预览时止于离线结果，不执行下文 Apply。诊断模式不能替代完整候选；
保留源数据、参数、源码提交和输出哈希，使相同输入能重现同一图面。

先用 `sch lib-layout` 计算每个 Lib 的局部几何，再组合到纸张；框按各自内容压缩上下空档，
按功能顺序排 Z 字行，同行顶齐，下一行按上一行最高框推进，不统一拉高。
提供实测 `sheetBorder` 后，虚线笔画到红色图纸内框最少留 10 raw。
标题使用粉色 0.2 inch，方框使用粉色虚线；当前不生成 Notes。

```bash
easyeda sch compose --from composition.json --out plan.json \
  --before page-before.json --playbook apply.json
```

目标页与计划不同且任务已授权重建时，加 `--replace` 生成带清页守卫的队列；不要先自行
清空页面来绕过差异检查。已完全匹配时复用电路；器件匹配但尚未布线时由生成器核验是否
满足复用条件。装不下应修改模块几何或按功能拆页，compose 不自动迁页或删除源页。

## 3. 执行与回读

```bash
easyeda sch apply apply.json --dry-run
easyeda sch apply apply.json --yes
```

预览应显示正确的工程/页面、预计操作与全部守卫；`--yes` 仅用于已获授权的动作范围。
生成的保护队列必须完整执行，不能改目标、`--resume` 或 `--from/--to` 跳过验证。
失败时保留 journal，读取实际结果后重生成计划；已成功的写不会自动回滚。

Apply 负责清页残留检查、放置后 ID/Role 绑定、接线前实测 pin/bbox 检查，以及电气与图形
回读。超时或 `partial` 先核实实际状态，不能盲目重复 place/connect。若只补框标题，
用 `sch frame apply/check`；它只操作自己登记的图元。

## 4. 验证代码转换效果

1. 对照目标 IR 与实际 connectivity：组件身份、pin→net、NC 必须一致。多页逐页读取，
   检查迁移后的页面归属和全工程位号；离线 diff 通过不能替代实际写入证明。
2. 逐页 `sch gate --strict --doc <page>`，确认所有阶段完成且 verdict 为 `pass`。
   `blocked` 先处理连接/页面；未执行的 DRC 等阶段必须补跑。
3. `sch frame check` 核验矩形、标题、颜色、虚线及实际文本净距，再用 `sch export-image`
   检查外置位号/型号、方向和阅读顺序。视觉问题应还原成源数据或算法规则修复。
4. `sch save` 返回 `saved:true`。保留输入、生成队列、回读和验证报告，报告仍未覆盖的限制。

只整理已有连线的小范围区域时，可按 [schematic-placement.md](schematic-placement.md)
选带连接的移动工具；仍须保存前后 topology/NC 对照。不要用只移动器件的工具替代连接迁移。
