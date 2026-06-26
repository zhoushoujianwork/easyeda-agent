# 事前分析 SOP — 原理图设计前分析 (Pre-execution Design Analysis)

> **定位:在 AI 动手布局之前的「分析阶段」。** 像资深设计师那样先做事前分析 (pre-analysis),把需求 / BOM / 网表读成一份**结构化「布局计划」(layout plan)**——**然后才**落坐标。
> **顺序 = 分析 (analyze) → 计划 (plan) → 执行 (execute)。不出计划,不落坐标。**
> 本文是执行 SOP [`auto-layout-sop.md`](./auto-layout-sop.md) 的**上游**:这里产出的「布局计划」正是它 **Step 0** 的输入。**本文只产计划,不重写执行细节**——计划里的每一段都对应喂给下游某个 Step(`functions/kind`→Step 0/1、`host`→Step 2、`refs/nets`→Step 3),并同时喂给 PCB 侧 [`pcb-layout-conventions.md`](./pcb-layout-conventions.md) 的 §0.3 分类器与 P0–P7。
> 规则定义全部引用 [`schematic-layout-conventions.md`](./schematic-layout-conventions.md):§1 分区 / §2 间距 / §4 命名 / §5 Designator 前缀 / §6 去耦;选型走 [`part-selection.md`](./part-selection.md);标准库查表 [`standard-parts.json`](./standard-parts.json)。

## 总原则 + 铁律(反模式)

**先看懂全图,再动一个坐标。** 散乱 / 返工 / 采购翻车,几乎都来自「没分析就开摆」。

> ⛔ **不出计划不落坐标 (no plan, no coords)**:布局计划的各段未填满前,**禁止** `schematic.component.place`。Gate 未全绿不得进 `auto-layout-sop`。
> ⛔ **只读不写 (read-only)**:分析阶段**零 mutation**——只跑 `schematic.components.list` / `schematic.export.bom` / `schematic.pages.list` / `schematic.titleblock.get` + 查 `standard-parts.json`。**绝不** `place` / `wire` / `modify`。
> ⛔ **不靠记忆,全部实读**:重器件 bbox、pin、网络分类、BOM 状态**全部来自实读**(`components.list` / BOM enrich),不靠估、不靠 designator 前缀猜。
> ⛔ **不准边摆边想分组**:功能分组 / 信号流 / 电源树**必须在放置前一次性定死**,放置只是查表落坐标。
> ⭐ **锚点驱动 (anchor-first)**:先钉**重器件锚点**,辅助件后挂;**最大簇先占板边**。锚点漏判 → 后面所有相对坐标错位。
> ⭐ **早标早省 + net 命名是唯一跨域信道**:PCB 的 P0–P7 约束**约 80% 在原理图阶段就能确定**(分区 / 机械 / 热 / 隔离 / net 命名 / 电源树)。PCB 看不到原理图意图,只能从 **net 名 + 线宽 + designator** 反推(全靠正则);原理图不标 = PCB 降级成 advisory + 人工返工。命名是**零成本的 PCB 让路**。
> ⛔ **RED 红旗清零 = 入口门禁**:带着无 C 号件 / 孤儿封装 / 漏去耦去布局 = 白排,采购阶段必返工。

---

## 分析阶段读什么 → 产出什么 (data → artifact)

| 做什么 | 读哪个数据 / action | 产出(写进计划) |
|---|---|---|
| 盘点全部器件 | `schematic.components.list`(designator/name/pins) | 器件清单 + pin 数 → 功能清单、锚点表、网络分类 |
| 量重器件 bbox | `components.list` 的 bbox(或符号尺寸) | 锚点表 `w×h` → 喂 Step 1 间距 / keep-out |
| 数页 / 分图现状 | `schematic.pages.list`(`allPages`) | 现有页结构 → 幅面 + 分页决策 |
| 读当前纸张 | `schematic.titleblock.get`(Width/Height/Size) | 幅面基线(默认 A4 1170×825) |
| 出可下单 BOM | `schematic.export.bom` + `scripts/bom-enrich.py` | BOM 归一表 + 孤儿件清单(无 C#)→ 红旗 |
| 选型 / 补料号 | `schematic.library.search`、`scripts/parts-select.py`、查 `standard-parts.json` | 每件 `libraryUuid + deviceUuid + LCSC C#`;替换建议(贵 / 缺货 / 非 basic)→ 红旗 |
| 反查器件类别 / 功能 | `standard-parts.json`(`key → kind/footprint/basic`) | `kind` 分类键 → 喂 part→zone |

> ⚠️ `export.bom` 需要器件**已 place**;事前阶段没有真 BOM —— greenfield 用**纸面 BOM 计划**代替,这正是「事前分析」存在的理由。首版 place 完**立刻**导一次真 BOM 做第一次核对(C# 命中率 / 未匹配 MPN),大改后重导,交付前归档。

---

# 事前分析步骤 (A1–A8)

> 各步只读;输出各填「布局计划」的一段。步间有反馈(高速差分对会反向拉近 A6 分区;新增 / 删件回 A1 全链重判),整体是「一遍跑通 + 局部回炉」,不是死直线。

## A1 — 读懂设计意图 → 功能清单 (intent → function list)

- **做什么**:把需求文字 / 已有件 / 目标 BOM 解析成**功能模块清单**(power-in, charge, dc-dc, ldo, mcu-core, clock-reset, rf/antenna, usb, sensor, storage, io, debug…)。每块记:`id` / 用途 / 关键器件 designator / 关键网络 / 是否对外接口 `external`。
- **读哪个**:用户需求文字(第一手意图);`schematic.components.list` 的 `designator/name/pins`(已有件);`export.bom` + `bom-enrich.py` 补 C# 看清真实型号;`standard-parts.json` 反查 `key → 类别/desc`。空图时从需求 + 目标 BOM **推**功能块,不依赖坐标。
- **产出**:`布局计划.functions[]`。
> 教训:**功能由「它在哪条信号链上」定义,不由 designator 前缀定义。** 只按前缀分类 → 把「自动复位 BJT 对」(Q1/Q2)当普通三极管归错块(它属 usb-uart→mcu strap 的过渡链,§1)。

## A2 — BOM 计划 + 标准件复用 + 可制造性红旗 (BOM / DFM plan)

- **做什么**:在落任何坐标前,把「要买什么、买不买得到、怎么归一、位号怎么编」算清楚,产出**结构化 BOM 计划表 + 可制造性红旗表**。
- **读哪个 / action**:
  - **查表优先**:每个待选件先查 [`standard-parts.json`](./standard-parts.json),命中即复用(确定性、可下单、basic 优先,直接带齐 `lcsc/footprint/deviceUuid`)。
  - **缺类目才选型**:未命中 → `parts-select.py "<value> <fp> <spec>" --qty <build qty>` → `schematic.library.search` by C#/MPN 拿 `{libraryUuid, deviceUuid}` → **回写 `standard-parts.json`(同 PR)**。排序见 [`part-selection.md`](./part-selection.md):relevance→buildable→**basic**→preferred→cheapest。
  - **同值同封装合并**:key = `(归一value, footprint)`,合并成一 BOM 行,`refs` 收全部位号,`qty`=成员数。值归一借 `parts-select` 规则(`10kohm/10kΩ`→`10k`,`µ`→`u`)。
  - **Designator 规划**(§5 前缀:R/C/L/D/Q/U/J/X/SW/K/TP/H/MH):新建件按 §5;语义化位号(`PWR/BOOT/RST`)容忍保留;同前缀连续不跳号、不重号;`set(designator)` 长度核对去重。
  - **关键参数确认**(阻容感 / 二极管 / IC 逐行核对,缺项 = RED):电容 **Vrating ≥ 1.5–2× 实际轨压**、去耦用 X7R/X5R(禁 Y5V/Z5U);电感 **Isat/额定电流 ≥ 峰值**;LDO 压差 + 电流余量。
- **产出**:`布局计划.bom_plan[]`(字段见模板)+ `red_flags[]`。
> ⛔ **合并是 BOM 行级,位号不合并。** `100nF×7` = 1 个 BOM 行(qty=7、refs=`C12…C18`)+ 7 个独立唯一位号。重号 / 空号 = RED。
> 📌 **教训(标准化闭环)**:每次新选型必须写回 `standard-parts.json`(`value/mpn/lcsc/manufacturer/deviceUuid/footprint/basic`),否则下一张图又退回 `library.search` first-match 的猜测,标准化白做。

**可制造性红旗表(DFM — pre-flight;RED 未清 → 不进 Step 0)**:

| 红旗 | 检测(读哪个) | 严重度 | 处置 |
|---|---|---|---|
| 无 LCSC C 号 (no-C#) | `bom-enrich` 未匹配 / `lcsc` 空 | **RED** | `parts-select` 选 basic 替代;或确认手工采购并标注 |
| 孤儿封装 (orphan footprint) | `footprint` 空 / 非标 / JLC 无 feeder | **RED** | 换可采购标准封装,回写 `standard-parts.json` |
| 缺去耦 (missing decap) | 每 IC VCC pin 对照 §6,无 100nF 绑定 | **RED**(RF/ADC)/ YELLOW(一般数字) | 进 `bom_plan` 补 0.1µF(每 VCC 一只)+ 设 `host`,喂 Step 2 |
| 容差 / 额定缺失 | A2 关键参数未确认 | **RED** | 补 spec 再选型 |
| 重号 / 空位号 | 去重断言失败 | **RED** | 重排位号 |
| extended-only(无 basic 替代) | `parts-select` 只剩 expand | YELLOW | 接受 feeder fee 或换 basic |
| 同值多封装碎片化 | 多个 0402/0603 10k 混用 | YELLOW | 归一到一种封装,减 feeder |
| 库存不足 (stock < build qty) | `parts-select` buildable 失败 / `stockCount` 不够 | YELLOW | 换在库件 |
| 极性件方向未定 / 钽电容未降额 | BOM 行无 polarity / 降额标注 | YELLOW | 标方向 + 钽电容额定 ≥ 2× |

## A3 — 重元器件识别(锚点)(anchor / heavy-part ID)

- **做什么**:选出**锚点件 (anchors)** = 布局的「钉子」。逐件记 `bbox(W×H)` / `pins` / 所属功能块 / 放置约束 `constraint`。
- **锚点判据**(命中任一):`pins ≥ ~20`,或 `bbox` 任一边 `> ~150`(§1「大模块 pin>50 或 bbox 边>200 放角落」);连接器 (J*)、电源模块、RF/天线、晶振 (X*)、MCU/大 IC;有特殊放置约束(板边 / 角落 / 热源 / keep-out)。
- **读哪个**:`components.list` 的 `pins[]`(→ pin 数 + 估 bbox);`standard-parts.json` 的 `footprint/desc`(判 RF/连接器/模块);§1 大模块规则。
- **产出**:`布局计划.anchors[]`,**按 bbox 面积降序**(最大簇先占边);bbox 边 >150 的件标独立 keep-out 列。
> 铁律:**锚点先定位,辅助件后挂。** 写进 `constraint`(呼应 §1 real-world override + PCB P0):发热件(DC-DC/LDO/4G PA)→ 远离敏感模拟与晶振;RF/天线 → 靠板边角落、天线 pad 朝外;MCU 含 RF(ESP32/nRF/CC)→ 标 `corner`;连接器 → 标 `board-edge`(对外朝板缘)。

## A4 — 电源树梳理 (power tree)

- **做什么**:把每条轨理成一棵树 **源 → 压差 → 电流 → 负载**。这棵树是 A2 去耦预算、A5 大电流 net、A7 稳压器=HOT 的**共同上游**——先画树,再派生其余四项。
- **读哪个**:`components.list` 找稳压器(pad 同时接输入轨 + 输出轨)、连接器电源 pin;BOM + `bom-enrich` 查稳压器型号 / 电流;`standard-parts.json`。
- **产出**:`布局计划.power_tree[]`,每行一条轨:`轨 / 源(上游轨或连接器) / 稳压器 / Vout / 估算 I / 负载簇 / 去耦预算`。
- **从树派生**:① 去耦——每条轨每个 VCC pin 配 100nF(§6),>50mA 加 bulk,轨越敏感(VDDA/VREF/RF)距离越严;② 布局优先级——主干高电流路径 `input→转换→负载` 单调成线,安静轨(VDDA/VREF/RF)在公共节点**星形分支**,绝不挂在噪声负载(开关器 / MCU)下游同一段供电铜;③ 哪条是大电流主干(=A5 的 HI-I);④ 哪个稳压器是 HOT(=A7)。
> 教训:不画树就布局 = 不知道哪条轨大电流、哪条要星形 → PCB 上电源回流乱窜、bulk 远离入口。**漏一个 VCC 焊盘 = 红旗。**

## A5 — 网络分类 + 关键网络标注 (net classification + critical-net annotation)

- **做什么**:把网络按电气性质分类——分类**决定** auto-layout Step 3 的接线策略与 PCB 布线优先级。原理图是**唯一**能把电气意图写进 net 的地方,用**命名 + 线宽 + 标签**显式标好。
- **读哪个**:`components.list` 的网名 + §4 命名约定;`schematic.wire.create` 的 `lineWidth`(§3.4);BOM 看模块类型(判 RF/电机/PA)。
- **产出**:`布局计划.nets{}`(net → class)+ 每类策略提示。

| class | 命中(§4 命名) | 原理图怎么标 | 布线策略(下游 auto-layout / PCB) |
|---|---|---|---|
| `rail` 电源轨 | `+3V3/+5V/GND/VDD_*/AGND` | 每 pin 按名 flag | PCB 加宽 / 铜皮 |
| `hs-diff` 高速 / 差分 | `USB_D±/SDIO/SPI_CLK/*_P,*_N` | **成对命名** `XXX_P/XXX_N` | 成对、同区、短;两端拉近(反向影响 A6);镜像对称等距 §7.3 |
| `length` 等长总线 | `DDR_DQ*/RGMII_*/SDIO_*` | 共前缀 | packed 等长组 §7.4 |
| `high-current` 大电流 | `VBAT/VBUS/电机/PA 供电` | **线宽 2/3** + 命名 | 加宽 trace / 铜面、bulk 就近、星形地、靠源 §5.5 |
| `analog` 模拟 / 敏感 | `ADC*/VREF/XTAL_*/SENSE/ISNS`、回 `AGND` | 命名区分 + 就近本地线 | 离开关 / 晶振 ≥300–500mil §7.6 |
| `sw` 高 dV/dt 开关 | `SW/LX/BST/BOOST` | 命名 | 铜面最小、敏感件躲开 §5.4 |
| `signal` 普通信号 | 其余功能网 | ≤3pin 本地线 / 跨区 net label | — |
| 隔离 / 电位 | `*_PRI/*_SEC/ISO_*/HV_*/MAINS_*` | **命名区分 primary/secondary** | PCB 据名推电位、爬电 / 间隙检查 §3 |

> ⛔ 铁律:**成对 / 成组的 net 必须成对 / 成组命名**——差分对漏 `_N`、等长组前缀不齐、隔离网不区分 → PCB 认不出 → SI / 安规全靠人工。**网络分类一次定死,执行与 PCB 直接查表。**
> 自检:`flag数/pin数` 预算应 ≪0.6;若预算就 ≈1,说明分类没做(全按名 flag fallback)。

## A6 — 功能分组 + 信号流 + PCB 分区映射 (grouping + signal flow + zone-tag)

- **做什么**:把 A1 功能块映射到 §1 九宫格 `zone`,定**主信号流轴**(左→右 = 输入→处理→输出:电源左 / MCU 中 / 外设右),并给每簇打一个 **zone-tag**∈{power/digital/analog/RF/mixed},让它在 PCB 上落成一块**不重叠瓦片**(PCB §4.2)。原理图九宫格分区**就是 PCB 分区的草图**。
- **读哪个**:§1 Zone Map + real-world overrides;A3 锚点簇面积(**最大簇先占它最需要的边**,再把 3×3 套进剩余空间);PCB §0.3 正则给每件打 PWR/GND/HS/ANA/RF 标。
- **产出**:`布局计划.zones{}`(块→区)+ `signal_flow{}`(`axis` + 上游→下游 `chain[]` DAG)+ 分区映射表(功能簇 → zone-tag → 主导 net → 板位偏好)。
- **每块标**:`zone / upstream / downstream` + 输入 pin 朝来向、输出 pin 朝去向(执行器直接喂 `connect_pin direction`,不再现猜)。电源链定**单一轴向**(纵向 TL→ML→BL **或** 横向 TL→TC→TR,**不混排**)。

| 功能簇 | zone-tag | 主导 net | 板位偏好 |
|---|---|---|---|
| 输入电源 / 转换 | power | PWR/GND | 板边 / 角(靠输入连接器) |
| MCU + 数字 | digital | 信号 net | 板中 |
| ADC / 采样 / 基准 | analog | ANA/AGND | 与数字隔 ≥100mil |
| 天线 / 收发 | RF | RF/ANT | **贴板边** |
| ADC/DAC/codec | **mixed** | 跨域 | 跨边界:模拟 pin 朝模拟、数字 pin 朝数字 |

> ⛔ **四域互斥**:RF / 模拟 / 数字 / 电源在原理图就**不许交织**(同簇件相邻、不跨区,§1)。交织 → PCB 必打架(回流横穿别人的块)。
> 教训(呼应 §3.5):信号流方向在这一步**定死**,执行时直接喂朝向,避免 flag 体压回器件。高速差分对必须成对同区短 → **反向影响 A5/A6 分区**(把两端块拉近)。

## A7 — 机械 / 热 / EMI / 隔离预判 (mechanical + thermal pre-registration → PCB P0/P1/P4)

- **做什么**:PCB 的 P0(机械锁定,最高优先级、immovable)、P1(安规隔离)、P4(热)所需输入**都是电气 / 机械属性,原理图阶段就能全部记下**,别等开 PCB 再翻外壳图。
- **读哪个**:`components.list` 找 `J/USB/DC/RJ/CN`(连接器)、`H/MH`(安装孔)、`SW/LED`(面板件);外壳 / 结构图(人给);`standard-parts.json` 核连接器 footprint / pin1 朝向;BOM + `bom-enrich` 查功率 / 电流 / 封装;判据用 PCB §0.3 的 HOT/SENSITIVE。
- **产出 → 机械约束清单 (mech-notes)**:连接器(朝外、贴哪条板边、插拔走廊 ≥5mm);安装孔(XY + 紧固件 → keep-out 半径);面板件(开孔坐标锁定);**板框 outline**(客户 / 外壳给的板尺寸 → PCB `pcb.outline.set` 的输入,板框是布局前置)。
- **产出 → 热 / EMI / 隔离标注**:① 发热件 **HOT**(稳压器、功率管 Q、功率电感 / 电阻、大电流 LED);② 怕热件 **SENSITIVE**(电解 / 钽电容、晶振 X/Y、传感器、基准、sense 电阻);③ 隔离 / 电位(跨光耦 / 变压器 / 数字隔离器的 primary/secondary net 命名区分,见 A5);④ 热环路——开关电源输入回路 `Cin→开关→续流→回 Cin` 在原理图就把 **Cin 紧贴稳压器 VIN/GND pin**(§6),PCB 上天然成簇。
> 铁律:**机械定电气,不反向**(PCB §2.1)。连接器**按信号流摆在图纸对应边**(电源连接器在电源簇侧、USB 在数字侧),PCB importChanges 后天然靠近该贴的板边。
> 教训:电解电容不标 SENSITIVE → PCB 可能把它摆到稳压器旁(每 +10℃ 寿命减半,要求 ≥5mm)。

## A8 — 幅面与分页预判 (sheet & paging pre-decision)

- **做什么**:基于器件数 + 锚点 `Σbbox` + 功能块数,**预判** A4/A3/A2/A1 或**多页分图**,并给每页的块归属。
- **读哪个**:A3 `anchors` 的 `Σbbox`;A1 `functions` 数量;`schematic.titleblock.get` 读纸张基线;`schematic.pages.list` 现有页。
- **面积估**:`Σ(主器件 bbox) + 辅助件 × ~80×80 + 走线余量`,留 **~20%**。件数阈值(auto-layout Step 0):**≤30→A4(1170×825)/ 30–80→A3(1654×1170)/ 80–160→A2(2340×1650)/ >160→A1 或多页**(电源 / MCU+数字 / RF+4G)。
- **产出**:`布局计划.sheet{}` = `size` +(多页时)`pages[]`(每页装哪些块);明细表 keep-out(右下角 `Title Block Position=3`,约 600×450);边界 `[40, W-40]×[40, H-40]`、对齐 10。
> ⚠️ 这是**预判**;真正「设纸张 + 放完断言全在界内」在 auto-layout **Step 0** 执行,预判错了 Step 0 会断言失败回炉。多页之间用 `net_port` 连页。
> ⚠️ **术语澄清**:`titleblock` 的 `Size/Width/Height` = **纸张**(改它 = 改纸张,**归 Step 0**);`title_block_plan` 只填 **Title/Author/Date/Rev** 等文字字段(经 `titleblock.modify`),**本步不动纸张**,避免两处抢着改。
> 教训(box-v2):面积要**算**(Σbbox + 辅助 + 20%),别拍脑袋——曾布到 2220×1500 却没核对纸张 = A4 → 一半在纸外。

---

# 产出物模板 — 设计分析报告 / 布局计划 (the deliverable)

**照下面骨架逐节填满,每节空着就是 Gate 红灯。** 直接复制这段 markdown 当模板:

````markdown
# 布局计划 — <项目名> (Layout Plan)
> 状态:[ ] 草拟  [ ] 已审  [ ] 已批准放置   生成:<date>   数据快照:components.list=<ts>,BOM=<ts>(过期需重读)

## 1. 需求摘要 (A1)
- 板子用途 / 一句话:<…>
- 器件总数:<N>(主 <n> + 辅 <m>)   页数:<p>
- 硬约束:<尺寸上限 / 接口位置 / 散热 / 特殊器件>

## 2. 功能清单 (A1) ← Step 1
| id | 用途 | 关键器件 | 关键网络 | external |
|---|---|---|---|---|
| power-in | USB-C 输入+保护 | U2,R18,R19 | VBUS,+5V,CC1 | ✅ |

## 3. BOM 计划表 (A2) ← Step 0 件数 / Step 1 kind / Step 2 host
| line | value | footprint | lcsc | mpn | basic | qty | refs[] | deviceUuid | kind | host(ic.vccPin) | tol/vrating/irating | redflags |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 100nF | 0402 | C1525 | 0402B104K500CT | ✅ | 7 | C12..C18 | … | decap | U3.VDD3P3_CPU | 10%/50V/— | [] |
> 合并只折叠 BOM 行,位号唯一无空号。孤儿件(无 C#/无库)= 0,否则进 §10 红旗。

## 4. 锚点 / 重器件表 (A3) ← Step 1
| Designator | 器件/型号 | type | bbox w×h | pins | primary-net | 预判 zone | constraint / keep-out |
|---|---|---|---|---|---|---|---|
| U3 | ESP32-S3 | MCU | 190×220 | 56 | +3V3 | BL | corner+antenna@board-edge |
> 按 bbox 面积降序;边 >150 → 独立 keep-out 列;一列 ≤4 件。

## 5. 电源树 (A4) ← Step 1 排序 + Step 2 去耦预算
```
VBUS(5V,J-USB) ─▶ U2 AMS1117 ─▶ +3V3 ─┬─▶ U3 ESP32 (7×VCC)
                                        └─▶ U7 Air780
```
| 轨 | 源 | 稳压器 | Vout | 估 I | 负载簇 | 去耦预算(§6) |
|---|---|---|---|---|---|---|
| +3V3 | +5V | U2 | 3.3V | (估) | U3,U7,上拉 | U3:7×100nF+2×10µF |
> 每条轨列全负载;每 VCC 焊盘 1×100nF(最近)+ 体电容(更外)。漏一个 VCC = 红旗。

## 6. 网络分类 + 关键网络 (A5) ← Step 3 接线决策表
| 网络 | class | pin数 | 跨度 | 标注手段(命名/线宽/标签) | 接线策略 |
|---|---|---|---|---|---|
| GND | rail | many | 全图 | 按名 | 每 pin 按名 flag |
| USB_DP/DM | hs-diff | 2 | — | 成对命名 | 本地等长贴近,两端拉近 |
> flag/pin 预算 ≪0.6;成对/成组 net 必须成对/成组命名。

## 7. 分组 + 信号流 + 分区映射 (A6) ← Step 1a/b
- 信号流主轴:<horizontal/vertical> chain=[power-in→dc-dc→mcu-core→io];逆流标理由。电源链单一轴向不混排。
| 组 | 成员 | zone | zone-tag | 输入←/输出→ |
|---|---|---|---|---|
| G3 MCU | U3+去耦 | BL | digital | UART↔U7 |
- 过渡簇:<复位 BJT 对 / ADC 分压> → 落两端 IC 间过渡带,不归任一栅格。

## 8. 机械 / 热 / 隔离标注 (A7) ← PCB P0/P1/P4 + pcb.outline.set
- mech-notes:连接器朝外/贴边/走廊;安装孔 XY+keep-out;面板件开孔;板框 outline=<尺寸>。
- HOT:<稳压器/功率管…>   SENSITIVE:<电解/钽/晶振/基准…>   隔离网:<*_PRI/*_SEC/HV_*>

## 9. 幅面 + 分页 (A8) ← Step 0
- 估面积:Σ(主bbox)+辅×80×80+走线,留~20% = <…>
- 选定幅面:<A4/A3/A2/A1>(titleblock.get 基线=<…>)
- 分页(多页):<电源 | MCU+数字 | RF+4G>;跨页 net_port,同页 net label
- 边界 [40,W-40]×[40,H-40];明细表 keep-out 右下 ~600×450
- title_block_plan(仅文字,经 titleblock.modify;Size/纸张归 Step 0):{Title,Author,Date,Rev,Sheet}

## 10. 风险 / 可制造性红旗 (跨 A2–A9)
| # | 红旗 | 严重度 | 影响 | 处置 / 回到哪节 |
|---|---|---|---|---|
| R1 | 孤儿件 C12 无 C# | RED | BOM 不可下单 | parts-select → 写回 standard-parts.json(A2) |
| R2 | +1V8 轨无去耦预算 | RED | 噪声/复位异常 | 回 A4 补预算 |
````

**机器可读交接结构**(同一份计划的 JSON 视图,喂下游):

```jsonc
layout_plan = {
  "functions": [ {"id":"power-in","parts":["U2","R18"],"nets":["VBUS","+5V"],"external":true} ],
  "anchors":   [ {"ref":"U3","fn":"mcu-core","bbox":[190,220],"pins":56,"constraint":"corner+antenna@board-edge"} ],
  "zones":     {"power-in":"TL","mcu-core":"BL","rf":"BL","io":"BR"},
  "signal_flow": {"axis":"horizontal","chain":["power-in","dc-dc","mcu-core","io"]},
  "nets":      {"+3V3":"rail","USB_D+":"hs-diff","XTAL_P":"analog","VBAT":"high-current"},
  "power_tree":[ {"rail":"+3V3","src":"+5V","reg":"U2","vout":"3.3","loads":["U3","U7"]} ],
  "mech_notes":[ {"ref":"J1","edge":"left","face":"out","clearance":"5mm"} ],
  "sheet":     {"size":"A3","pages":[{"name":"Page1","fns":["power-in","mcu-core","io"]}]}
}
bom_plan = [ {"line":1,"value":"100nF","footprint":"0402","lcsc":"C1525","basic":true,"qty":7,
              "refs":["C12","C13","C14","C15","C16","C17","C18"],"deviceUuid":"…","kind":"decap",
              "host":{"ic":"U3","vccPin":"VDD3P3_CPU"},"vrating":"50V","redflags":[]} ]
title_block_plan = {"Title":"…","Author":"…","Date":"…","Rev":"v0.1","Sheet":"1/1"}
red_flags = [ {"flag":"missing-decap","target":"U3.VDDA","severity":"RED","fix":"add 0.1µF @host"} ]
```

> 教训(box-v2):没有这份计划 → 94 件按固定栅格平铺、去耦不贴 IC、327 个 flag。**计划在前,执行只是「查计划落坐标」**,散乱在分析阶段就被堵住。

---

# 进入执行前自检清单 (Gate)

**全部勾选后**才允许进入 [`auto-layout-sop.md`](./auto-layout-sop.md) 的 Step 0→3。**任一项 ❌ → 不准 place。**

- [ ] **G1 器件已实读**:`components.list` 跑过,主 / 辅件已分,无「记忆估数」。
- [ ] **G2 BOM 可下单**:`bom_plan` 每行带 `lcsc + footprint + deviceUuid + basic`;孤儿件 = 0(或登记进红旗并给处置);新选型已写回 `standard-parts.json`。
- [ ] **G3 关键参数已确认**:阻容感 / 二极管 / IC 的容差 / 额定电压 / 额定电流齐全;电容 Vrating ≥ 1.5–2× 轨压。
- [ ] **G4 命名 / 前缀合规**:designator 前缀符合 §5,无重号 / 空号;网名符合 §4(全局轨用标准名;差分 / 等长 / 隔离成对成组命名)。
- [ ] **G5 锚点已定**:每个 anchor 的 `w×h` + pin 数在表;按面积降序;边 >150 已标 keep-out 列;特殊约束(板边 / 角 / 热)已写 `constraint`。
- [ ] **G6 电源树完整**:每条轨列全下游负载;无悬空轨;源 IC 已定;HOT 稳压器已识别。
- [ ] **G7 去耦已规划**:每个 IC 每个 VCC 焊盘配 100nF(+ 体电容)写进电源树,**无漏焊盘**(§6),`host` 已设。
- [ ] **G8 网络已分类**:每个网络标 class + 接线策略;`flag/pin` 预算 ≪0.6;高速 / 差分 / RF / 模拟 / 晶振 / 隔离已单列标注。
- [ ] **G9 已分组 + 定信号流 + zone-tag**:每件归入功能组;信号流主轴写定(逆流有理由);四域不交织;电源链单一轴向。
- [ ] **G10 机械 / 热 / 隔离已登记**:连接器朝外 / 贴边、安装孔 keep-out、板框 outline 已记;HOT / SENSITIVE / 隔离网已标。
- [ ] **G11 幅面 / 分页已定**:估面积 → 选定幅面;`titleblock.get` 确认基线;多页方案明确;明细表 keep-out 已记;预判坐标都落 `[40,W-40]×[40,H-40]` 且无器件出界。
- [ ] **G12 红旗已闭环**:每个红旗有处置 + 回退节;无「未决高危」(出界 / 漏去耦 / 不可下单 / 孤儿封装)。

> 铁律:**Gate 是入口门禁,不是事后 lint。** lint(`decap_far / flag_density / zone-adherence`)是执行后的安全网,抓手抖漏网的;Gate 是执行前的准入,拦「没想清楚」。两者都过才算交付。

---

# 何时回到分析 (when to re-analyze)

出现下列任一情况,**停止执行,回相应分析步重做计划**(改计划 → 重过 Gate → 才继续):

| 触发 | 回到哪步 |
|---|---|
| 幅面放不下 / 器件预判或实放出界、压明细表 | A8 幅面 + 分页(重选尺寸或多页) |
| 新增 / 删除器件,或解析出隐藏件 | A1 功能 + A3 锚点 + A5 网络(全链重判) |
| 选型变更(换料 / 缺货 / 非 basic) | A2 BOM + 红旗(并写回 `standard-parts.json`) |
| 发现漏标的电源轨 / 漏配去耦的 VCC 焊盘 | A4 电源树 + 去耦预算 |
| 网络分类错(本地网被当 flag、跨区网没给 label、差分漏 `_N`) | A5 网络分类(`flag/pin` 预算重算) |
| 功能流被打断(过渡簇无归属、逆流无理由、四域交织) | A6 功能分组 + 信号流 |
| 机械 / 外壳约束变更 | A7 机械 + 热(并更新 `pcb.outline.set` 输入) |
| 执行后 lint 成片报警(zone 不符 / flag 密度 >0.6 / decap_far 成片) | **不是微调能救** → 回 A4/A5/A6 重规划,而非逐件挪坐标 |

> 教训:lint 成片报警 ≠ 微调能修。**成片 = 计划错**,必须回分析重做,别在执行层逐件挪坐标硬刚。

---

# 与下游 / 兄弟文档的衔接 (handoff)

```
analyze()  # 本文 A1–A8,只读,产出「布局计划 + bom_plan + red_flags + title_block_plan」
  A1 functions   ← components.list + bom-enrich + standard-parts.json
  A2 bom_plan    ← 查表优先 / parts-select / §5 前缀 / 关键参数 → DFM 红旗
  A3 anchors     ← pins/bbox + footprint;按面积降序
  A4 power_tree  ← 稳压器 + 负载;派生去耦/大电流/HOT
  A5 nets        ← 网名 + §4;分类决定布线/PCB;成对成组命名
  A6 zones+flow  ← §1 + 最大簇先占边;zone-tag 四域不交织
  A7 mech/thermal← 连接器/安装孔/板框 + HOT/SENSITIVE/隔离
  A8 sheet       ← Σbbox 阈值;A4/A3/A2 或多页
→ Gate 12 项全绿 → 进入 auto-layout-sop.md Step 0 落坐标
```

**谁喂下游哪步**:

| 产出 | 喂给 | 用途 |
|---|---|---|
| `bom_plan.length` + `kind` 分布、`sheet` | **auto-layout Step 0** | 估件数 → A4/A3/A2/多页;设纸张 |
| `kind`、`anchors`、`zones`、`signal_flow` | **Step 1** | 确定性 part→zone;锚点先放;主轴朝向 |
| `host`(IC + VCC 焊盘)、去耦预算 | **Step 2** | 去耦 pin-relative 绑定(§6 阈值) |
| `nets`、`refs` / designator、信号流朝向 | **Step 3** | 接线决策表(按名 flag / 本地线 / net label + `connect_pin direction`) |
| `title_block_plan` | `schematic.titleblock.modify` | 只填文字字段;**Size/纸张由 Step 0 改** |
| 分区映射(zone-tag)、`mech_notes`、HOT/隔离、net-class | **PCB [`pcb-layout-conventions.md`](./pcb-layout-conventions.md)** | §4 瓦片/P5、§2 P0 锁定 + `pcb.outline.set`、§3 P1/§6 P4、§5/§7 P2/P3 |

规则依据:[`schematic-layout-conventions.md`](./schematic-layout-conventions.md)(§1 分区 / §2 间距 / §4 命名 / §5 前缀 / §6 去耦);选型:[`part-selection.md`](./part-selection.md);标准库:[`standard-parts.json`](./standard-parts.json);PCB 侧:[`pcb-layout-conventions.md`](./pcb-layout-conventions.md)。

---

> 状态:**建议稿**。本文是 [`auto-layout-sop.md`](./auto-layout-sop.md) 的**上游分析层**;二者配套——本文出「布局计划 + Gate」,下游照计划执行 Step 0–3。后续按更多板子细化阈值(锚点 `pins≥20 / bbox 边>150`、面积余量系数、`flag/pin` 预算线、去耦覆盖率、孤儿件容忍度、net 分类关键词),每补一类网 / 一种约束 / 一条红旗,回写本文与 §1/§4/§6,并把 Gate 关键项接成 CLI 预放置校验。
