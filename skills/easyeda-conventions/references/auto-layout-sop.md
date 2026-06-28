# 原理图布局 — CLI 能力 + 硬约束(不是规则书)

> **布局智能交给 AI 自己**(看 `getAll` 坐标 + 截图判断间距/线长/对齐),本文不堆布局规则,
> 只写**工具怎么用 + 几条绕不过的硬坑**。需要分区/间距等设计约定时查
> [`schematic-layout-conventions.md`](./schematic-layout-conventions.md)(参考,不是必遵的规则书);
> 大板动手前可选过 [`design-pre-analysis.md`](./design-pre-analysis.md) 出个布局计划。

## 一条核心原则(嘉立创标准工程「实战派ESP32-S3」实测)
**flag 只给电源/地轨;信号全是真·本地正交线;去耦贴 IC;多页按功能分。**
(实测:flag 100% 是 GND/3V3/各电源域 ≈0.4/件;信号全是 `sch_PrimitiveWire`;去耦距 IC 90–230u。)
其余间距/分区/对齐 AI 按数据+截图自调。

## 自调闭环(数据驱动,现在就能用)
放 → `sch list`/`getAll` 读回坐标 + union-find 连通 → 判间距/线长/连通 → `sch modify` 挪 → 再读。
- **截图**:`sch snapshot` → 取 `<cwd>/.easyeda/artifacts/` 最新 png(给人看的视觉终检)。
- ⚠️ **API 改完 EDA 画布不自动重绘** → 截图 / `view fit` 都返回旧帧(连 fit 框选都是旧的)。
  **验证状态只信数据(`getAll`)**;要看图先在 EDA 里**碰一下那页**(滚动/点选)触发重绘再截。
- ⚠️ wire 的 `getState_Net` 不可靠(常空);连通用 union-find(`getState_Line` 按**每 4 数 = 1 段**解析,别当连续折线)。

## 画线 / flag / 去耦(CLI 级硬规则,实测)
- **信号 = 真本地正交线**:端点落引脚坐标 = 连通;不对齐走 L `[x1,y1, x2,y1, x2,y2]`(EDA 自动拆 2 正交段)。
  📐 **`--points` 两种写法都行**:嵌套 `[[x,y],[x,y],…]` 与扁平 `[x1,y1,x2,y2,…]` 均接受——连接器内部统一归一化成扁平 `number[]`(EDA 底层只认扁平)后再 `create`,不用再为格式 `call` 绕过(issue #5)。
  ⚠️ **线段不能穿过别的引脚**——EDA 会在穿过处**截断并连上它**(沿一排同 y/x 引脚走线必中招)→ 走无引脚通道。
  ⚠️ 多脚网用 **pin→pin 链式**(每段端点锚在引脚),别"星点连到空中 junction"(合并时丢无锚点 junction)。
- **flag 只给电源/地轨**:`connect_pin direction=`(自动定向 + 2026-06 build 取反补偿);信号一律走线,不 flag。
- **去耦**:读引脚 name 找 VCC/GND → 电容贴 VCC 焊盘 ~100u → `VCC→cap` 短线(无引脚通道)→ cap 另一脚 GND flag。

## 图纸 / 多页(工具坑,实测)
- **先有图纸,默认 A4**:`sch sheet-geometry --json` 必须能读到 `componentType:"sheet"` 的实测 bbox,再进入生产级 place/wire。若页面显示“无图纸”或 sheet bbox 缺失,这是**阻塞项**:让用户在 EasyEDA 选择/创建默认 A4 图纸,不要用 union bbox/provisional title block 继续落子。
- **纸张改不大**:`titleblock.modify` 写操作是坏的(EDA_CALL_FAILED,连文本字段都失败)→ 默认 **A4**(以 `sch sheet-geometry` 实测 bbox 为准;常见约 1170×825/1188×840 一类比例),
  放不下就**多页**,别赌放大单页。坐标落界内、避开右下角明细表;放完断言全在 sheet bbox 内。
- **分页先于摆件**:按模块粗估每页容量。经验门槛:一页 A4 优先放 1 个主 IC/模块簇 + 2–4 个小外围簇;若超过 ~20–30 个可见器件、或任意两组之间无法保留 40–80 units 通道,拆页。跨页用同名 net port 表达连接。
- **多页交错执行**:`doc switch` 能切页;`page-new` 把新页设为活动页;`getAll` 按活动页取。
  → 在当前页 place+wire,然后**每 `page-new` 一页就立刻**在它上面 place+wire(别先全建页再统一布线 = 全堆最后一页);跨页用 `net_port` 同名连。
- **清页前先存预置件的 `{libraryUuid,deviceUuid}`**(或用 MPN 重 `lib search` 搜回),否则删了找不回
  (`design.json` 里 onboard 件 `deviceUuid=null`)。
- `sch page-delete` 用 **`--page`**(不是 `--uuid`)。

## 抗 churn(>~50 mutation,实测必备)
显式 `--project`;`debug.exec_js` 批量多图元/次、每批切 <~20s(长调用被心跳杀);每批**重试 + 增量 `sch save`**(无 undo,半落未存=不可恢复);每批开头重拉新 pid。
