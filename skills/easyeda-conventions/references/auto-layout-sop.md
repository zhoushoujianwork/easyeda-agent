# 自动布局 SOP — 原理图 (Automated Schematic Layout SOP)

> **缺口由 box-v2 实测暴露**:自动实现能"放器件 + 按网名接线",但**没有布局方法论**——94 个件按
> 综合的 **90×80 固定栅格平铺**、去耦不贴 IC、**每个 pin 都按名 flag**(327 个),结果电气对但视觉散。
> 本 SOP 把 [`schematic-layout-conventions.md`](./schematic-layout-conventions.md) 的规则
> (分区/间距/去耦/朝向)串成**自顶向下、机器可执行**的次序。**先布局,再接线,最后微调。**

## 总原则 + 三条铁律(反模式)
**自顶向下:图纸 → 主器件 → 辅助件 → 微调。** 散乱几乎都来自跳过 Step 0/1。

> ⛔ **不要 raster**:绝不把模块塞进固定 `90×80` 网格;辅助件相对**父引脚**放,不上传送带行。
> ⛔ **不要恒定 pitch**:间距按 `(wa+wb)/2 + buffer` 逐邻居算(§2),不用一个常数步进。
> ⛔ **不要 flag-every-pin**:按名 flag 是**快但乱的 fallback**;交付前必须收敛到本地线(见 Step 3)。

---

## Step 0 — 图纸尺寸自适应 (fit the sheet first)
1. 估面积 `Σ(主器件 bbox) + 辅助件数 × ~80×80`,留 ~20% 余量。
2. 选/设图纸:≤30 件 A4 `1700×1100`;30–80 件 A3 `2400×1600`;**>80 件(box-v2=110)A2 或多页分图**(电源页/MCU+数字页/RF+4G页)。
3. 记图纸边界 `(W,H)`,所有坐标落界内、对齐 10。
> 教训:综合把件铺到 x2300/y1560 没先定图纸 → 超视区,用户"看不到"。

## Step 1 — 主元器件按区布局 (mains by zone, deterministic)
**(a) 数值分区矩形表**(y-UP,低 y=底;以 box-v2 `100..2430 × 100..1560` 为例):

| | Left x[100,800] | Center x[850,1600] | Right x[1650,2430] |
|---|---|---|---|
| Top y[1080,1560] | 输入电源/保护 | 时钟/复位 | 状态/调试 |
| Middle y[580,1060] | DC-DC/LDO | **MCU + 去耦** | RF/传感器/蜂窝 |
| Bottom y[100,560] | 电池/USB | SD/外设 | I/O/大模块 |

**(b) 确定性 part→zone 分类器**(键 = `type + primary-net`,不靠模糊 symbolName):
电源 IC→Left(按轨排序)、MCU→Center、RF/传感器/蜂窝→Right、USB/SD/连接器→Bottom。
**过渡簇**(自动复位 BJT 对、ADC 采样分压)→**落在它两端 IC 之间的过渡带**,不归任一模块的栅格。
**(c) 间距**:`min_dx=(wa+wb)/2+buffer`(buffer 80/120/200 按尺寸,§2);bbox 边 >150 的件(ESP32 190×220、Air780 180×540)各占**独立 keep-out 列 buffer 200**;一列别堆 >4 件;一条功能链别铺满整张图高。
**此时只放主器件骨架,不放辅助件、不接线。**
> 教训:自动复位网络(Q1/Q2/R_Q1B…)落进 USB 块、离它服务的 U1 引脚 740u;ESP_EN/ESP_IO0 横跨 x460→1200。

## Step 2 — 辅助件就近(去耦绑定 IC 焊盘)(owner-bound, pin-relative)
**每个辅助件认领"宿主"主器件,贴它放。去耦是重点:**
1. **绑定**:每个去耦电容绑到**具体 (IC, VCC 焊盘)**,不是只绑到电源**网络**。
2. **坐标公式**:`cap = 父 VCC pin 坐标 ± (§6 SHOULD 半径内偏移)`,**不是** `module_origin + n*step`。
   每个 VCC 焊盘配 **1 个 100nF**(ESP32-S3 有 7 个);**高频 100nF 必须最靠焊盘**,体电容(10/22µF)放更外(~90u)。多侧径向分布,**不排成一行**。
3. **本地桩线**:去耦必须 `pin→wire→cap` 一段本地短线,再 `cap→GND flag`——**不能只靠网名丢到 +3V3/GND 上**(否则去耦环路对布局/DRC 不可见)。
4. FB 分压贴 FB pin;上拉贴被拉 pin;晶振+负载电容贴 osc pin(≤200u guard)。极性件按电流方向定向。
> 教训:~40 个去耦全被铺在均匀 90u 行,最近的也才 80–90u,**0 个达 SHOULD**;C34 离 U11 达 520u;U6 的体电容(C22 90u)竟比 100nF(C23 180u)更近——正是 §6 要防的。

## Step 3 — 微调:接线策略 + 朝向 + 对齐 (fine-tune)
**骨架+填充就位后才接线。按名 flag vs 本地线 vs 标号——决策表(可机器执行):**

| 网络情形 | 处理 |
|---|---|
| `kind∈{power,ground}` 或全局轨(GND/+3V3/+5V/VSYS/+3V8/VBAT*) | **每 pin 按名 flag**(§3.3) |
| 信号网 **≤3 pin 且 bbox 跨度 <~250u**(同功能簇) | **本地 `pin→wire→pin`/星点,不用 flag** |
| FB 分压 / 补偿 RC / 晶振负载 / 去耦 | **永远本地线**到宿主 IC pin |
| 信号网**跨区**(跨度 ≳500u) | **同页网络标号(net label)**;`net_port` 只留给**跨页** |

- **朝向**:每个 flag/port 经 `schematic.power.connect_pin` 传 `direction=`(=引脚引出侧),**绝不**统一 rot 0;`deriveBodyRotation`/§3.5 自动补偿取反。
- 直角走线、网格对齐、消重叠、清标签密度。
- **flag 密度自检**:若 `flag数 ≈ pin数`(>0.6),说明用了乱 fallback,**必须重过 Step 3**。
> 教训:327 flag / 341 pin 里只有 175 是真轨;44 个两脚网 + 16 个三脚星点(U2_FB/晶振等)本该一根直角线;全部 flag 还都 rot 0 → 体朝里压回器件。

---

## 执行次序(给自动实现器)+ 收尾
```
fit_sheet()                         # Step 0:估面积→定图纸/分页→记边界
classify_all_to_zone(type, net)     # Step 1a/b:先分类,再出坐标
place_anchor_ICs(zone_rects, §2)    # Step 1c:主器件落区中心、按 bbox 间距、keep-out 列
for ic in anchors:                  # Step 2:辅助件认领宿主→贴 VCC 焊盘(公式,非栅格)
    place_satellites_pin_relative(ic)
place_transition_clusters_in_gutter()   # 过渡簇落两端 IC 之间
wire():                             # Step 3:按决策表
    local_wire(≤3pin & FB/comp/xtal/decap); flag(rails, connect_pin direction); label(cross_zone)
post_pass(): zone_conformance + flag_density_selfcheck   # 强制:不达标重过
verify(): drc.check + lint(空间规则) + snapshot(zoom-to-all)
writeback(): 把新解析件 diff 进 standard-parts.json(与原理图同 PR)
```
**Step 0/1 不可跳过。** box-v2 散乱 = 跳过图纸自适应与主/辅分层、直接"放所有件+全按名 flag"。

## 大批量(>~50 mutation)抗 churn(实测必备)
(a) 显式传 `--project/--window`,中途重连不会打错窗口;(b) 用 `debug.exec_js` **批量**多图元/次,别一图元一 CLI;(c) 每个 exec_js 批**切到 <~20s**(place/wire 各分 N 块 ~20 op),长调用必被心跳杀;(d) 每块**重试 + 增量 `schematic.save`**(无 undo,半落未存=不可恢复);(e) 每块开头**重拉新 pid**。

> 状态:**建议稿**(box-v2 单板实测 + 5 维审计)。后续按更多板子细化:图纸尺寸阈值、去耦 Nu 阈值、本地线 vs flag 边界。lint 须加对应门禁(decap_far / zone-adherence / flag-density / local-net-as-flag / 预放置 min-distance,见 skill-iteration 计划 #15–20)。
