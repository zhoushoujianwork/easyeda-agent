# 自动布局 SOP — 原理图 (Automated Schematic Layout SOP)

> **缺口由 box-v2 实测暴露**:自动实现能"放器件 + 按网名接线",但**没有布局方法论**——94 个件按
> 综合的 **90×80 固定栅格平铺**、去耦不贴 IC、**每个 pin 都按名 flag**(327 个),结果电气对但视觉散。
> 本 SOP 把 [`schematic-layout-conventions.md`](./schematic-layout-conventions.md) 的规则
> (分区/间距/去耦/朝向)串成**自顶向下、机器可执行**的次序。**先布局,再接线,最后微调。**
>
> ⚠️ **前置 = 事前分析。** 本 SOP 是「执行层」,有上游:动手前必须先跑
> [`design-pre-analysis.md`](./design-pre-analysis.md)(事前分析)产出一份「布局计划」并过 12 项 Gate
> ——本文 **Step 0** 的输入(件数/kind 分布、锚点 bbox、电源树、功能分组+信号流、网络分类、幅面+分页)
> **全部来自那份计划**。**不出计划不落坐标。**

## 总原则 + 三条铁律(反模式)
**自顶向下:图纸 → 主器件 → 辅助件 → 微调。** 散乱几乎都来自跳过 Step 0/1。

> ⭐ **规则前置,lint 是安全网不是主纠正。** 朝向/间距/去耦距离这些**在放置/接线那一刻就按规则算好**
> ——flag 的 rotation 用 `BODY_ROT[类型][朝外方向]`(orientation.json)+ 取反补偿,桩线朝器件外;
> **绝不**"先随便摆(rot 0)再等 lint 事后报"。lint(decap_far/flag_density/朝向…)是**兜底安全网**,
> 用来抓漏网的,不是用来替代放置时的规则。(box-v2 教训:执行器图省事全用 rot 0 → 朝向全错,
> 等于把规则只当事后检查。)

> ⛔ **不要 raster**:绝不把模块塞进固定 `90×80` 网格;辅助件相对**父引脚**放,不上传送带行。
> ⛔ **不要恒定 pitch**:间距按 `(wa+wb)/2 + buffer` 逐邻居算(§2),不用一个常数步进。
> ⛔ **不要 flag-every-pin**:按名 flag 是**快但乱的 fallback**;交付前必须收敛到本地线(见 Step 3)。

---

## Step 0 — 图纸尺寸自适应 (fit the sheet first) ⚠️必做,不是估个数
EasyEDA Pro **无 set-paper-size API**;纸张大小由**明细表/图纸符号**决定,**改明细表 = 改纸张**。
真实 A 系列(landscape,EDA 单位,默认新建 = **A4 1170×825**):

| 尺寸 | W×H | 件数(经验) |
|---|---|---|
| A4 | **1170×825** | ≤30 |
| A3 | **1654×1170** | 30–80 |
| A2 | **2340×1650** | 80–160(box-v2=110 → A2) |
| A1 | **3300×2340** | >160(或多页分图:电源/MCU+数字/RF+4G) |

1. **读当前纸张**:`sch titleblock-get` → `Width/Height/Size`(新建默认 **A4 1170×825**)。
   ⚠️ **别信 `getCurrentSchematicInfo` 里的 Width/Height**——实测它会返回脏/不一致值(2338↔1655 乱跳);以 `titleblock-get` 为准,拿不准就**默认 A4**。
2. **估需求**:`Σ(主器件 bbox) + 辅助件 × ~80×80 + 走线余量`,留 ~20% → 查上表选尺寸。
3. **⛔ 已有页"无法可靠改大"**:`modifySchematicPageTitleBlock`(`sch titleblock --modify`)**实测写操作直接 EDA_CALL_FAILED**(连改文本字段都失败,读没问题)——**纸张尺寸字段改不动**。所以**两条可靠路**(不要赌"事后放大"):
   - **(默认页放不下就)多页分图**:`sch page-new` 建电源/MCU+数字/RF+4G 页,每页内坐标约束到该页 (W,H),跨页用 `net_port`;**这是 >A4 容量(box-v2=110)的正解**。
   - **或压缩到当前页**:把所有坐标缩放进默认 **A4 [40,1130]×[40,785]**(密但单页可靠)。
   > 待修(基础设施):`titleblock.modify` 这个 action 抛异常 = 连接器 bug;修好后才谈"单页放大到 A2"。
4. **记边界 + 明细表 keep-out**:明细表在**右下角**(`Title Block Position=3`,约占右下 ~600×450);坐标落 `[40,W-40]×[40,H-40]` 内、避开右下角、对齐 10。
5. **放置后断言**(硬性):任一器件 bbox 超出该页 `(W,H)` 或压明细表 → **Step 0 失败,改多页/压缩/重排**,不许交付。
> 教训(box-v2):① 布局铺到 2220×1500 却没核对纸张=A4 → 出界;② 想"改明细表放大纸张"但该 API 是坏的。
> **结论:纸张尺寸当成约束(默认 A4),坐标去适配它;放不下就多页——不要假设能改大单页。**

## Step 1 — 主元器件按区布局 (mains by zone, deterministic)
**(a) 数值分区矩形表**(y-UP,低 y=底;**按 Step 0 选定的 (W,H) 缩放**,下表以 **A2 2340×1650** 为例,留 40 边距):

| | Left x[40,780] | Center x[800,1540] | Right x[1560,2300] |
|---|---|---|---|
| Top y[1120,1610] | 输入电源/保护 | 时钟/复位 | 状态/调试 |
| Middle y[580,1100] | DC-DC/LDO | **MCU + 去耦** | RF/传感器/蜂窝 |
| Bottom y[40,560] | 电池/USB | SD/外设 | I/O/大模块 |

> ⚠️ **右下角明细表 keep-out**:`x>1750 且 y<480` 留给明细表(`Title Block Position=3`),**不放器件**;
> 该 cell(Bottom-Right)的件挤到它左侧/上方。分区矩形必须随纸张尺寸缩放,**不能硬编码 box-v2 的数**。

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

## 多页执行(>A4 容量,实测必备)
分页是 >A4 的正解,但 EasyEDA 的页 API 有两个致命坑(box-v2 实测):
- **`sch open` / `document.open` 不切换活动页**(实测:连开 3 个不同页,`getCurrentDocumentInfo` 始终停在最后一个);
  但 **`sch page-new` 会把新建页设为活动页**。`getAll()` 是**按活动页**取的。
- ⇒ **必须"交错"执行**:在**当前活动页**上 clear+place+wire(第 1 页),然后**每建一页就立刻在它上面 place+wire**
  (新页即活动页),**绝不回头切页**。先把所有页建好再统一布线 = 全堆到最后一页(踩过)。
- **清页前先保住预置件**:`_clear_active` 会删掉**所有** part 含预先放好的主 IC;这些 IC 的
  `{libraryUuid,deviceUuid}` 若没存过就**删了找不回**(`design.json` 里 onboard 件 `deviceUuid=null`)。
  动手前**先读 getState 存下它们的 lib/uuid**,或用 MPN 重新 `lib search` 解析(box-v2 14/14 靠 MPN 搜回)。
- 跨页信号用 **`net_port`(同名跨页连)**;每页内坐标各自约束到该页 (W,H)。
- `sch page-delete` 用 **`--page`**(不是 `--uuid`);删页能可靠清理误建页(zsh 下循环变量要显式分词,
  `for U in $VAR` 在 zsh 不自动分词——用字面量或 python 循环)。

## 大批量(>~50 mutation)抗 churn(实测必备)
(a) 显式传 `--project/--window`,中途重连不会打错窗口;(b) 用 `debug.exec_js` **批量**多图元/次,别一图元一 CLI;(c) 每个 exec_js 批**切到 <~20s**(place/wire 各分 N 块 ~20 op),长调用必被心跳杀;(d) 每块**重试 + 增量 `schematic.save`**(无 undo,半落未存=不可恢复);(e) 每块开头**重拉新 pid**。

> 状态:**建议稿**(box-v2 单板实测 + 5 维审计)。后续按更多板子细化:图纸尺寸阈值、去耦 Nu 阈值、本地线 vs flag 边界。lint 须加对应门禁(decap_far / zone-adherence / flag-density / local-net-as-flag / 预放置 min-distance,见 skill-iteration 计划 #15–20)。
