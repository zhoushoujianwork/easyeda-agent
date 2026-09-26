# 原理图布局约定 (Schematic Layout Conventions)

When an AI agent (via `easyeda-agent`) generates or modifies a schematic, it must follow these conventions. They are derived from EE best practices plus empirical study of real LCEDA / EasyEDA Pro reference designs (see §7、§8).

> **自动批量实现** 一整张网表时,这些规则的执行**次序**见 [`auto-layout-sop.md`](./auto-layout-sop.md)
> (源数据 → 区内求解 → 整页求解 → 固定转换/Apply → 数据回读)。
> [数据驱动架构基准](schematic-data.md#数据驱动架构基准)优先；下文旧参考图的九宫格、
> 距离统计和历史观察不是生成坐标或功能归属的答案，不覆盖显式 zone/attachments/spacing。

> **本文导航(§)**:0 坐标系/单位 · 1 分区(Zone Map)· 2 模块间距 · 3 Wire 长度/走线约定 ·
> 4 命名约定 · 5 Designator 前缀 · 6 去耦电容规则 · 7 真实参考 motobox2026 · 8 真实参考 ESP32S3R8N8 ·
> 9 自动化布局执行步骤 · 10 边界/开放问题 · 11 图纸边界/标题栏 keep-out。

## 0. 坐标系与单位

- EasyEDA Pro 原理图原生坐标单位（raw/canvas unit）= `0.01 inch` = `0.254 mm`；
  常用 100 mil 网格的 1 grid step = 10 raw units。CLI 的坐标参数使用 raw unit；
  明确标注为 `mm` 的距离参数（例如 `sch layout-lint --min-gap`）会在边界换算，
  `2.54 mm = 10 raw units`。
- 器件 anchor 的硬连接网格为 **5 raw units**：`x % 5 == 0 && y % 5 == 0`；
  `connect_pin` / netflag 会吸附到该网格，偏离会制造斜短桩或悬空。整页排版优先使用
  10 raw units（100 mil）节拍获得更整齐的视觉，但不得把合法的 odd-5 引脚强拉到 10 格。
- 生产级布局必须先有可读 sheet primitive;默认选择/保留 A4。无图纸时不得用坐标外扩或已有器件 union bbox 代替图纸。
- A 系列图纸尺寸以 `easyeda sch sheet-geometry` 的实测 bbox 为准;不同 EasyEDA build 的 A4 可能约 `1170 × 825` / `1188 × 840` 等同类比例。不要硬编码单一尺寸。
- 元件中心 `(x, y)` = 元件参考点；元件 pin 在中心周围。

## 1. 分区 (Zone Map)

以下九宫格是历史参考的阅读示意，不是当前自动布局算法。当前以明确核心及专属外围
组成 zone，经数据计算按 Z 型排纸；不按型号模糊分类或公共电源猜归属，不硬套以下位置：

```
+-----------------------+-----------------------+-----------------------+
| (TL) 输入电源 / 接口   | (TC) 时钟 / 复位       | (TR) 状态 LED / 调试    |
| Type-C, DC IN, 充电   | 晶振, RC, MOSFET      | LED, 串口 header       |
|                       |                       |                       |
+-----------------------+-----------------------+-----------------------+
| (ML) DC-DC / LDO      | (MC) 主 MCU + 去耦    | (MR) 射频 / 传感器       |
| 降压, 升压, LDO, 滤波  | ESP32 / STM32 等       | Wi-Fi, BLE, IMU, GNSS  |
|                       |                       |                       |
+-----------------------+-----------------------+-----------------------+
| (BL) 电池 / 接地       | (BC) 外设 IC          | (BR) I/O 连接器 / 大模块  |
| Bat connector, GND   | EEPROM, RTC, USB Hub  | 4G/LTE 模块, FPC, FFC  |
+-----------------------+-----------------------+-----------------------+
```

**Rules of placement**（理想 / 软约束）：

- **电源向左**（TL/ML/BL 列）—— 电流从左向右流，符合阅读习惯
- **MCU 居中**（MC）—— 它是信号的"枢纽"，所有外设朝它收敛
- **射频/传感器/I/O 向右**（TR/MR/BR 列）—— 时序/数据从 MCU 发散
- **大模块（pin > 50 或 bbox 一边 > 200）放在角落**（BL/BR/TR）—— 给中部留布线空间
- **同一功能簇相邻**：晶振紧贴 MCU；去耦电容紧贴 IC 电源 pin（见 §6）；上拉电阻紧贴拉的那个 pin

**现实偏移 (real-world overrides)**——下面几种情况优先于 3×3 理想：

- **MCU 内集成 RF（ESP32 / nRF / CC 系列）时，MCU 迁移到角落（BL / BR / TL），把 RF pi-network + 天线引到板边**；此时 MC 区只剩 strap 网络（EN / IO0 上拉、RST / BOOT 按键、PSRAM 占位等）。参见 §8 ESP32S3R8N8 案例。
- **电源链可走横向或纵向**——纵向（沿 TL → ML → BL 左列下行，如 §7 motobox）适合 portrait sheet；横向（沿 TL → TC → TR 顶行右行，如 §8 ESP32 开发板）适合 MCU 占据图纸下半的 landscape sheet。**单一轴向**即可，不要两者混排。
- **支配性功能簇优先**——先按 bbox 总面积评估每个功能簇，最大簇先占用它最需要的边（如 RF 簇要板边、4G 模块要角落），再把 3×3 套到剩余空间。
- **自动复位 BJT 对**（CH340 DTR/RTS → Q1/Q2 → EN/IO0）固定坐落于 USB-UART 与 MCU strap pin 之间的过渡带，常跨 MR/BC 边界，不归类为 sensor 也不归类为外设 IC。

## 1.5 功能框与标题

默认先完成一页,每个 Lib 表示核心器件及其外围电路。模块方框与标题是布局数据的一部分,
由相同的转换器生成并通过 Apply 落图,不能靠任务结束前临时补画。

- 方框用粉色虚线,标题用粉色文字,字高 **0.2 inch = 20 原理图坐标单位**。
- 方框包络包含器件、引脚、导线、电源符号、位号及其他必检文字；排除非位号器件属性。
  按分项占位比较上、下空档,
  容得下就将标题内嵌,不够才最小扩边;先最小化框高度,再比较面积,平局依次优先左对齐、顶部。
  不固定预留顶部标题带,也不缩小标题字号。
- 先计算 XY、方向和紧凑包络,再从左上按功能 Z 字顺序排模块,各框保留内容高度、同行顶齐，按行最高框换行并整体平移；距实测图纸内边框至少10 raw。
  转换、Apply 后回读样式与真实文字 bbox,验证预测包络及占位净距;文字估算不能代替实测。
  重复执行不得叠加框或标题。字段契约见[模块框与标题](schematic-data.md)。
- 1.4 不提供独立 Notes 创建功能,不预设“每模块 1~3 行说明”的交付要求。
  既有文字仍可用 `sch text-list` 读取,需要清理时按真实 ID 用 `sch prim-delete` 删除。
- 分页是功能设计决策,不是框尺寸不足时的自动退路。当前一页放不下时,先报告数据中的边界冲突。

旧分区工具 `sch zone-plan` / `sch zone-draw` 仍服务已归组页面;手工页用 `sch zones set`
登记模块。存在性门仍检查缺失分区框和图签,不检查 Notes 缺失。

## 2. 模块间距

以下为历史样例统计，不是现行硬阈值，也不能替代统一 spacing 与真实 bbox/引脚避碰。
不得以统计距离强行拉远专属外围或扩大 zone；实际求解遵守数据手册中的约束。

> Goal：既不松散（图纸利用率低）也不拥挤（线路相互干扰）。

设模块 A 中心 `(xa, ya)`、bbox 宽 `wa`、高 `ha`；B 同。

**最小中心距**：
```
min_dx = (wa + wb) / 2 + buffer
buffer:
  - 小元件 (bbox 边 ≤ 50): 80
  - 中等 IC (bbox 边 60–150): 120
  - 大模块 (bbox 边 > 150): 200
```

**推荐中心距**（典型布局）：
| 邻居类型 | 中心距 (units) | 备注 |
|---|---|---|
| R / C / L 离散件之间 | 80–120 | 0603 元件 bbox ≈ 40 |
| 小 IC 之间 (8–16 pin) | 200–280 | 留出 designator 标签空间 |
| 中 IC 之间 (16–48 pin) | 280–400 | |
| MCU 邻射频/sensor | 400–600 | 大模块需要旁路空间 |
| 主 MCU 邻晶振 | 60–120 (SHOULD) | **理想紧贴**；MCU 含 RF 时可放宽到 ≤ 500 |
| IC 邻去耦电容 | 见 §6 分级阈值 | 高速 ≤ 30；一般 ≤ 60 |

> 实测：motobox X1 距 U1 ≈ 384，ESP32S3R8N8 开发板 X1 距 U3 ≈ 437——两份参考都已超出 120 这条线。把 60–120 视为「理想紧贴」，把 ≤ 500 当作「集成 RF 时的退让上限」。

**行间距**（top row → middle row → bottom row）：
- 短模块（h ≤ 80）间距 200–300
- 高模块（h > 200）间距 300–500
- 跨行长 wire 走外围（不穿过模块中央）

## 3. Wire 长度与走线约定

### 3.1 短桩 (pin lead-out)

每个**需要接入 wire/netflag/netport/netlabel 的 pin** 都必须经非零长度 wire 引出
（EasyEDA DRC 不认标识与 pin 坐标重叠为连接，见
[`schematic.md` 的 EasyEDA Electrical Rules](./schematic.md#easyeda-electrical-rules-load-bearing)）。
故意未使用的 pin 应显式 `sch no-connect`，不应为了满足短桩规则制造假连接。

> **重要**：自动生成时严禁产出**零长度 wire 占位记录**（如 `{line:[620,60,620,60]}`）——实测 ESP32 reference 中 204 个 wire 里 149 个是这种零长占位，DRC 会逐个报错。Agent 在 emit wire 前必须 assert `(x1,y1) != (x2,y2)`。

下面的「推荐长度」是**经验区间** (median–p90)，不是硬范围。唯一硬规则只有「非零」。

| 用途 | 推荐长度 (units) | 方向 |
|---|---|---|
| pin → Power netflag (`+3V3` / `+5V` / `VBAT` …) | 20–40 (median 30) | 顺着 pin 朝**上** |
| pin → GND netflag | 10–40 (median 20) | 顺着 pin 朝**下** |
| pin → NetPort (IN/OUT/BI) | 20–60 | 朝外侧 |
| pin → 共享网络 net label（信号） | 10–90 (median 30, p90 85) | 朝标签方向 |
| pin → 同行邻 pin（IC pin-to-pin 直连） | 80–100 | 直线，y 共线 |
| pin → 去耦电容 | 10–20 | 极短，越紧越好 |
| 任意 wire 段 > 100 | 紧凑性诊断，回源优化几何；不得因此拆开必需的直连线树 | |

> 实测分布 (ESP32 reference，151 个非零 segment)：median 30、p90 90、p95 100、p99 195、max 215。信号段在 10 (短跳) 和 85 (IC pin → 邻 label) 两处出现明显聚类——这些都是结构性长度，不要强压到 20–60。

### 3.2 直角约定 (right-angle routing)

- **所有 wire 走水平或竖直**，不出现斜线（45°、任意角度均不允许）
- 拐弯用**一段水平 + 一段竖直**两段 wire（或单 wire 多 endpoint：`[x1,y1, x2,y1, x2,y2]`）
- 不允许"T 形" 三线交点未显式标 junction
- 长线/多折点先修源约束并重算；只有已声明的跨模块/跨页边界可使用端口。
  `direct` 和原直连引脚组不得因线长而改为同名标签岛。

### 3.3 电源/接地特殊约定

| | 方向 | 推荐长度 | netflag kind |
|---|---|---|---|
| `+3V3` / `3V3` / `+5V` / `VBAT` / `VDD_*` | 优先向上出线，stored rotation 按 §3.5 校准表 | pin → netflag 20–40 | `power` |
| `GND` / `AGND` | 优先向下出线，stored rotation 按 §3.5 校准表 | pin → netflag 10–40 | `ground` |
| `IN/OUT` 端口 | 向外出线，朝向按真实引脚/标记几何 | pin → netflag 20–60 | `net_port_in/out/bi` |

电源/地的 netflag **绝不**与 pin 同坐标——必须用 wire 引出一段（即使只有 10 units 也行）。

#### 3.3.1 电源符号复用与最少导线算法（1.4）

- `GND`、`VCC`、`+3V3`、`+5V` 等全局网允许**多个局部符号**。每个符号都必须由一
  段真实短桩连接，不能用一个符号加一根线绕过整页所有器件。
- 先从 1.4 `pin_net` 生成同模块 terminal 集合，再在同模块内寻找近邻 pin 对。短的
  外围连接（LDO 的 VIN/VOUT 与电容、上拉电阻与目标 pin）优先生成真实水平/竖直
  wire；其余连通分量各自就近挂一个同名电源/地符号。
- 连接树采用无环最小森林：候选边按线长、拐点和障碍惩罚排序，禁止跨页大环、长距离
  环绕母线和为了“看起来同网”重复画线。一个网络可以有多个局部树，只要它们都通过
  同名全局符号或明确的模块端口归并。
- 普通信号也先尝试模块内直连；只有明确的跨模块/跨页策略才使用 netport/netlabel。
  标签是边界表达，不是外围电路的替代导线。
- Apply 的最小验收集固定为 `sch read`、`sch check`、`sch bridge-check`、DRC；任何
  wire-crossing、multi-net-wire、dangling-wire、orphan-stub 或拓扑变化都阻止后续队列。

### 3.4 线宽

EasyEDA 默认 lineWidth = 1。约定：
- **信号线**：1（默认）
- **电源线**（VCC/GND 主干道）：2
- **总线** (`BUS_xxx`)：3

通过 `schematic.wire.create` 的 `lineWidth` 参数指定。

### 3.5 朝向约定 (orientation — 顺着导线方向)

> **一句话**：netflag / netport / 元件的本体必须**顺着导线引出的方向朝外**，连接 pin 朝向电路（导线来的那侧）。朝向错了会让符号本体压在导线/电路上，布局立刻显乱。

**netflag / netport 的 rotation 规则**（已编码进 `schematic.power.connect_pin`）：

引脚先用一小段 wire 引出到某个方向 `direction`，flag 放在 wire 末端，body 朝 `direction` 继续朝外。EasyEDA 的 stored rotation 把 body 按 **up → right → down → left** 每 +90° 循环。各类型 rot=0 时的 body 朝向：power=下、ground=上、net_port=右；这些是 `orientation.json` 的已校准视觉真值，与端点坐标的正负号独立。

> **整张表只由 4 个事实决定，单一真源不会漂移**：上面的循环顺序 + 三个 rot=0 锚点（power=下 / ground=上 / port=右）。这 4 个事实存放在本 skill 的 [`orientation.json`](./orientation.json)，由它**推导**出 12 项表——`connect_pin`（`extension/src/actions.ts` 的 `deriveBodyRotation()`）与 linter（`scripts/orient.py`）**推导同一张表**，二者不可能各写各的。校验由 `make lint-test`（`tests/run.py`）保证：① 结构上 `orientation.json` 必须推回自己的 `frozenTable`、循环律成立；② 锚点的活体 ground truth 由 [`calibrate.js`](../scripts/calibrate.js) 对 `getPrimitivesBBox` 中心偏移实测复核（导入新 .eext 后跑一次）。**永远不要手改那 12 个数字**——改锚点 / 循环后重跑 `tests/run.py --update`。

> ⚠️ **createNetFlag / createNetPort 存储时取反**（2026-06 build）：传 `R` → 存储/渲染是 `(360-R)`。**坑**：建完**立即** `getState_Rotation()` 会回显 `R`（看着像恒等），**重新拉取**（`getAll`）才看到真正的取反值。`connect_pin` 已**运行时自探测并补偿**（`detectRotationNegation`），所以**经 connect_pin 传下表的值就能得到正确朝向**，对调用者透明;若直接调 raw `eda.createNetFlag`（debug.exec_js），需自己传取反值 `(360-表值)`。
>
> **坐标契约：原理图统一使用 y-UP。** `+y` 在画布上向上；`direction='up'` 使用 `endY = pinY + offset`，`direction='down'` 使用 `endY = pinY - offset`。`--direction` 表示视觉方向，并与坐标符号保持这一固定对应。2026-07-19 在 3.2.148(web) 用 `eda.sch_PrimitiveText.create` 双探针复核：y=700 位于 y=100 上方，且连接器 bbox 与原生 `getPrimitivesBBox` 完全同空间。
>
> 历史 3.2.121 曾有相反坐标轴报告，但当前 CLI、连接器、块模板和约定均以已复测的 y-UP 为单一真值；不能为兼容未复现的旧构建再次局部翻转符号。若未来确认仍需兼容相反坐标轴的构建，应像 `detectRotationNegation` 一样在连接器边界加入运行时探测和统一坐标转换。**flag rotation 表不随本端点修正修改**：它按视觉方向索引，并由 `orientation.json` / `calibrate.js` 独立校准。
>
> ⛔ **走过的弯路（勿重蹈）**：取反是**真的**——实测 `connect_pin(direction=left)` 传 `90` → 存 `270` → 渲染**朝右**（0/180 上下对称，所以只有横向 flag 才暴露,藏了很久）。曾把这个取反当"误判"、撤掉 connect_pin 的补偿(commit `8aace7e`)，那次 **revert 才是 bug**;现已用运行时自探测重新锁死。**不要再据"恒等"撤补偿,除非先用 `connect_pin` 放个 left flag 肉眼确认朝向。** 校准方法：对 flag 调 `sch_Primitive.getPrimitivesBBox([pid])`，bbox 中心相对放置点 (x,y) 的偏移方向 = body 真实朝向（纯数据，不靠截图）。

| kind | body 朝 `up` | `left` | `down` | `right` |
|---|---|---|---|---|
| power (`+3V3`/`+5V`/`VDD_*`) | **180°** | 90° | 0° | 270° |
| ground (`GND`/`AGND`) | 0° | 270° | **180°** | 90° |
| net_port (`IN`/`OUT`/`BI`) | 270° | 180° | 90° | **0°** |

> 加粗的是各类型的**默认/最常见**朝向（power 朝上、ground 朝下、port 朝右）。**power/ground** 由 `calibrate.js` 对活体 bbox 实测验证（ceshi 10/10 通过）。**net_port 是箭头符号，bbox 中心读不出它的指向**——已用 **connect_pin 放置 + 肉眼确认**：`direction=right` 的 port 渲染出来确实朝右（朝外），所以 port 行也是对的；`calibrate.js` 对 port 报的 WARN 是 bbox 读不准导致的，**属正常、不是表的 bug**。其余未观测方向由同一条循环律从已验证锚点推导，构造上一致。必要时用 `schematic.power.connect_pin` 的 `rotation` 参数显式覆盖。

**普通元件的朝向**（信号流原则）：
- 元件按信号流向摆——**输入 pin 朝信号来的方向，输出 pin 朝信号去的方向**。一个串在 A→B 横向链路上的元件（电阻、电感、磁珠）应水平摆放，pin1 朝 A、pin2 朝 B，不要竖着插在横向链路里。
- 极性元件（LED、二极管、电解电容、稳压管）：阳极/正极朝电流来的方向（通常朝电源/左/上），阴极/负极朝电流去的方向（通常朝地/右/下）。
- 多 pin IC：让**电源 pin 朝上/下、输入信号在左、输出信号在右**（配合 §1 的「信号从 MCU 向右发散」），减少导线交叉。

> 实现提示：自动布局时，先决定该 pin 的导线走向（依据它要连到的目标在左还是右、上还是下），再据此设元件/flag 的 rotation——**永远是导线方向决定朝向，不是先定朝向再硬拉线**。

## 4. 命名约定

| 类型 | 风格 | 例 |
|---|---|---|
| 电源 net（带极性 / 多电压） | `+大写带正负号` | `+5V`, `+12V`, `-5V` |
| 主数字 3.3 V 轨 | 可省 `+` | `+3V3` **或** `3V3`（见下） |
| 外设域电源 | `VDD_<peripheral>` | `VDD_SPI`, `VDDA`, `VDD3P3_RTC` |
| 模拟地 / 数字地 | 大写 | `GND`（默认单一地），仅在确有需要时拆 `AGND`/`DGND`/`PGND` |
| 数字信号（功能命名） | `MODULE_FUNC` 大写下划线 | `UART_TX`, `SPI_MOSI`, `I2C_SDA`, `LED_R` |
| 数字信号（直出芯片 pin） | 沿用 datasheet pin name | `SPIHD`, `SPIWP`, `SPICLK`, `SPICS0`, `CHIP_PU`, `XTAL_P`, `XTAL_N` |
| MCU GPIO 头排引出 | `GPIO<N>` | `GPIO0`, `GPIO48` |
| USB 差分对 | 后缀 `+/-` | `USB_D+`, `USB_D-`；Hub 下游口 `D3+`, `D3-`, `D4+`, `D4-` |
| 总线 | `BASE[N..0]` | `DATA[7..0]`, `ADDR[15..0]` |
| 复位 / 中断（低有效） | `n` 前缀 | `nRESET`, `nINT_IMU`（注：ESP32 的 `CHIP_PU` 是高有效，**不**加 `n`） |

**电源命名细则**：
- `+5V` / `+12V` / `-5V` **保留 `+`/`-`**（极性 / 区分必需）。
- `+3V3` 与 `3V3` 在 Espressif 参考设计里混用——主数字 3.3 V 轨可任选其一。**新设计推荐 `+3V3` 保持一致**；从 Espressif block 导入时保留原 `3V3` 即可，不强制改名。
- 单板优先使用单一 `GND`；只有真有模拟前端 / 开关电源回流时才拆 `AGND`/`PGND`。不要仅为「命名整洁」引入 `DGND`。
- 直接把芯片 pin 引出到 header / 测试点而**无功能重命名**时，net 名沿用 datasheet pin name（`SPIHD`、`GPIO0` 等），**优先于**再造一个 `SPI_HOLD` 别名。

## 5. Designator 前缀

| 前缀 | 元件类 |
|---|---|
| `R` | 电阻 |
| `C` | 电容 |
| `L` | 电感 |
| `D` | 二极管 (含 LED) |
| `Q` | 晶体管 / MOSFET |
| `U` | IC（一般、芯片、模块） |
| `J` | 连接器 |
| `X` | 晶振 |
| `SW` / `K` | 开关 |
| `TP` | 测试点 |
| `H` / `MH` | 安装孔 |

LED 也可用 `LED1` 这种语义化命名（兼容 `D1`），EasyEDA 不强制 `D` 前缀。

读取历史参考时保留原始 `PWR/BOOT/RST` 等命名作为证据；进入目标重建前用
`sch designators` 按官方库前缀修复非标准位号，功能名存 role，稳定 ID 不变。
本表仅举例，不能覆盖真实库前缀或改写已有合法位号。

## 6. 去耦电容 (decoupling) 规则

每个数字 IC、模拟 IC、模块的 **VCC pin** 都应有去耦电容旁路到 GND。距离按**分级阈值**给出（pin XY → cap 中心 manhattan 距离）：

| 电源 pin 类别 | SHOULD ≤ | MUST ≤ |
|---|---|---|
| 高速 / RF / ADC 电源（`VDD_SPI`, `VDDA`, RF 模块 `VDD3P3`） | **30 units** | 60 units |
| 一般数字电源（`VCC`, `VDD3P3_CPU`, 普通 `VDD`） | **60 units** | 120 units |
| 储能 / bulk（10 μF 钽 / 陶） | 200 units | — |

**电容选型**：
- 高频去耦 = **0.1 μF (100 nF) 陶瓷**，**每个 VCC pin 一个**。
- 模块电流 > 50 mA 时并联 **10 μF 钽 / 陶**（低频 / 储能），按 IC 而非按 pin 配置即可。
- 多 VCC pin 的大芯片（ESP32-S3 有 VDDA×2 + VDD3P3_CPU + VDD_SPI + VDD3P3_RTC + VDD3P3×2 = 7 路）：**每路一只 0.1 μF**——实测 §8 reference 只配齐了 2 个，属于**已知欠去耦**。

去耦需求首先按具体器件手册写入源连接与核心/外围归属，再由算法随核心求解；
不在现场 place 核心后临时补电容。以上数值是历史经验，不替代具体器件的典型电路。

> 阈值依据：ESP32 reference 9 个 big-IC VCC pin 的最近 cap 距离排序为 `[30, 50, 95, 105, 105, 165, 200, 215, 225]`，median 105。旧规则「≤30 units」对应 11% 达成率，明显不合实际；新分级让一般数字电源 SHOULD（≤60）达成率提升到 22%，MUST（≤120）覆盖 56%，同时保留高速 pin 的严格要求。

## 7. 真实参考 A：motobox2026 (motorcycle tracker)

15 个 part，2400 × 1600 grid 范围（采集自 connector 实测）。**这是「贴近 3×3 理想」的代表**：MCU 在 MC 区附近、电源链纵向、4G 大模块占 BR 角。

| Designator | 元件 | 类别 | 坐标 (x, y) | bbox (W × H) | pin |
|---|---|---|---|---|---|
| **U1** | ESP32-S3-WROOM-1U-N8R8 | 主 MCU (MC 区) | (1385, 210) | 190 × 220 | 41 |
| U2 | TPS54360 | 降压 DC-DC (TL/ML) | (150, 115) | 100 × 50 | 9 |
| U3 | BQ24074 | 电池充电管理 (ML) | (420, 175) | 120 × 150 | 17 |
| U4 | JW5033S | DC-DC (TL) | (675, 110) | 70 × 20 | 6 |
| U5 | SY8089 | DC-DC (TL) | (905, 110) | 70 × 20 | 5 |
| U6 | LC29H | GNSS 模块 (TR/MR) | (1740, 155) | 200 × 110 | 24 |
| U7 | LSM6DSV | IMU (TR) | (2085, 130) | 170 × 60 | 14 |
| U8 | SD NAND | 存储 (BL/BC) | (120, 500) | 40 × 40 | 9 |
| U9 | Air780EG | 4G LTE 模块 (BR 角) | (1590, 750) | 180 × 540 | 109 |
| U10 | CH334F | USB Hub (BL/BC) | (370, 540) | 140 × 130 | 25 |
| U11 | CH340K | USB-UART (BC) | (645, 500) | 90 × 60 | 11 |
| J1 | Wafer 2P | 电池接口 (BL/MC) | (1120, 125) | 30 × 50 | 4 |
| J2 | Type-C 12P | USB (BC) | (885, 535) | 70 × 110 | 16 |
| X1 | 32.768 kHz xtal | 晶振 (邻 MCU) | (1110, 490) | 60 × 20 | 4 |
| LED1 | 0603 White | 状态指示 (TR/MR) | (1320, 485) | 40 × ? | 2 |

**观察结论**：
- **电源链 (TPS54360 → BQ24074 → JW5033 → SY8089)** 从左到右排在上排 y=110–175。符合"电源向左+电流向右流"。
- **主 MCU U1 在中右** (1385, 210)，靠近右排射频/sensor（U6 GNSS, U7 IMU）。
- **大模块 U9 Air780EG (180×540, 109 pin)** 占 BR 角，独立成块。
- **USB-C 接口 J2 + USB Hub U10 + USB-UART U11** 形成 USB 子系统集中在 BC/BL 中下区。
- **晶振 X1 (1110, 490) 距 U1 中心 ≈ 384 units**——稍远，可优化到 200 内。
- 行间距 = top 行 (y~115–175) → 下行 (y~485–540) ≈ 320–360 units。

## 8. 真实参考 B：ESP32S3R8N8 开发板

53 个 part，~1400 × 1100 grid 范围。**这是「3×3 让位给 RF 与开发板形态」的代表**：MCU 在 BL 角而非 MC、电源链横向而非纵向、TR 区完全空缺、MR 区被 2×20 pin header + auto-reset BJT 占据。

| 区 | 矩形 (x..x × y..y) | 主要模块 | 与 §1 对比 |
|---|---|---|---|
| **TL** | 60..435 × 60..255 | U2 USB-C + R18/R19 CC 5.1k + C46/C52 + TP1/TP2 | 符合「输入电源」 |
| **TC** | 435..910 × 60..465 | U6 ME6217 3V3 LDO + U5 BY25Q64 SPI flash + BOOT 键 + PWR LED + C51/C53 | LDO 上移到 TC，§1 期望在 ML |
| **TR** | x>957, y<397 | **空** | §1 期望状态 LED / debug header；此处状态 LED 跑到了 ML |
| **MC** | 850..985 × 340..480 | U4 PSRAM 占位 + RST 按键 + R14/R15 EN/IO0 上拉 + 启动 strap | **不含 MCU**——只有 strap 网络 |
| **ML** | 60..260 × 415..640 | X1 40 MHz 主晶振 + LED1 状态灯 + C33/C34 10 pF 负载 | §1 期望 LDO；这里成了「晶振 + 状态灯」拼区 |
| **MR** | 930..1405 × 420..760 | J1, J2 (1×20 pin header) + Q1, Q2 (MMBT3904 自动复位) | §1 期望 RF/sensor；开发板用 header + auto-reset 替代 |
| **BL** | 60..655 × 705..1070 | **U3 ESP32-S3 (170×290, 57 pin)** + U25 天线 + L4/L6/L7/L8 RF pi-network + C19~C23/C47/C50 去耦 | **MCU + RF 占据整个 BL，并外溢到 BC**——§1 期望 MCU 在 MC |
| **BC** | (与 BL 接壤) | RF pi-network 与天线延伸 | — |
| **BR** | 930..1405 × 770..1070 | U24 CH340K USB-UART + U16 CH334F USB Hub + X2 24 MHz | 符合「I/O 连接器 / 大模块」 |

**关键观察**：

1. **MCU 不在 MC**：U3 ESP32-S3 (170×290 bbox，最大 IC) 与 RF pi-network + 天线一起占据 BL 角，把天线 pad 推到板边。MC 区只剩 strap 网络（EN/IO0 上拉、RST/BOOT 键、PSRAM 占位）——印证 §1「MCU 含 RF 时迁移到角落」。
2. **电源链横向而非纵向**：USB-C 在 TL → 3.3 V LDO 在 TC → 调压输出沿顶行 (y=60–465) 横向扩散，**完全不走左列**。配合 MCU 占用整个板下半，这是 landscape sheet 的典型选择。
3. **晶振远离 MCU**：X1 (185, 445) 距 U3 (365, 855) 中心距 ≈ 437 units，**违反 §2 「60–120」理想紧贴**——但在工作的 reference 上稳定存在，因此 §2 已把 60–120 标为 SHOULD，并允许集成 RF 时放宽到 ≤ 500。
4. **TR 区完全空（0 part）**：本应放的状态 LED 跑到 ML，调试用的 BOOT/RST 键塞在 TC/MC 边界。开发板形态下 TR 容易整片闲置，不要硬填。
5. **MR 不是 RF 也不是 sensor**：2×20 pin 排针 (J1/J2) + 自动复位 BJT 对 (Q1/Q2 + R16/R17 base 限流) 占据 MR。CH340 DTR/RTS → Q1/Q2 → EN/IO0 的过渡电路天然横跨 USB-UART (BR) 与 MCU strap (MC)，不归 sensor 也不归外设 IC。
6. **去耦欠配**：U3 有 7 个 VCC pin，但只有 C23 (距 VDD_SPI 30) 与 C54 在「local」范围内，其余 5 路 VCC 距最近 cap ≥ 95 units。这是 reference 自身的设计弱点，**不要把它当 baseline 抄**——按 §6 分级阈值生成时仍应给每个 VCC pin 配 0.1 μF。

## 9. 自动化布局的执行步骤

统一按 [auto-layout-sop.md](auto-layout-sop.md) 执行：保留原始数据，明确核心/外围所有权与
绘制策略，区内完整求解后整页计算，固定转换并 Apply，再做数据对账、严格检查与保存。
不按本节旧版“模糊分类 → 九宫格现场摆件 → 逐脚标记”执行。
发现问题回改源输入、测量或算法再重算；导图发现的漏检须补机器规则和回归。

## 10. 边界与开放问题

- 这是 **schematic** 约定，不是 PCB 约定。PCB layout 另有独立约定（trace 宽度、layer 用途、impedance）。
- 模块容量以真实几何、纸张约束和求解结果判断，不能只按引脚数推断；分页迁移完整功能区。
- 多页之间通过 `net_port` (`createNetPort('IN/OUT/BI')`) 在页间建立电气连接，net 名称相同视为同网。
- `getCurrentRenderedAreaImage` 在后台可能返回旧缓存。验收始终读取原始数据，
  展示用 `sch export-image`；不要为了检查截图刷新而修改电路，也不要以肉眼观察替代数据检查。
- **`schematic.page.rename` 必须看宿主返回值与 fresh 页名（[#55](https://github.com/zhoushoujianwork/easyeda-agent/issues/55)，已重新打开跟踪）**：宿主 `false` 表示未确认成功，不能写成“已提交”或自动归因为缓存。仅官方返回 `true` 且新鲜页面列表确认目标 UUID 的名称，才能称已验证；读取失败、页面缺失或仍旧名均不通过。保留原始返回值、旧/新页名和文档身份，停止依赖改名结果的后续写入。不要用无关写操作刷新元数据，也不要在状态未知时自动重试改名。历史 dev.21 的成功不替代当前构建回读。
- 目前两份 reference（§7 motobox、§8 ESP32S3R8N8）覆盖了「贴近 3×3 理想」与「RF MCU 占角 + 横向电源链」两种典型。若再采集到第三种（例如纯模拟前端、或多电源域工控板），应继续补充以避免 agent 过拟合到单一案例。

## 11. 图纸边界与标题栏 keep-out (sheet / title-block keep-out)

放置 / 布线规划器（`sch autoconnect`、`sch autolayout`）**绝不能**把 net flag / net port /
器件压在图纸的 **图框/明细表（title block）** 上。但 EasyEDA Pro 既没有 set-paper-size API，
也不单独暴露标题栏的 bbox。需要精确内框时读取官方 SVG 的矢量几何；不要在各工具里散落硬编码 A4 坐标，
统一走 **`easyeda sch sheet-geometry`**（实现见 `internal/app/cmd_sch_sheet.go`，运行时权威）。

精确离线路径：先用 `sch export-image --format svg --scope page --out sheet.svg --project <project> --doc <page>`
导出全页，再运行 `sch sheet-geometry --from-svg sheet.svg --json`。保存原文件、导出命令和页面身份；
输出 `sourceSha256` 绑定输入字节，`sheet.innerBBox` 是内框中心线边界，`titleBlock.bbox` 是表格单元格
外包络，统一为原理图 raw、y-UP 坐标。布局参数另留线宽及安全边距。当前仅支持官方导出的单个
`c_partid="sheet"`、两个未变换矩形框和一个矩形单元格标题表；缺失、歧义、变换或隐藏几何时
报 unsupported，不能退回比例估计后称精确通过。它不访问 EDA，也不证明文件属于当前页面，须与
同批 fresh 对象/原生归档交叉核对。不从 PNG 或 SVG 的 viewBox 猜纸张大小。

不传 `--from-svg` 时保留原有估算查询（issue #26，Option D 混合），不提供精确内框：

1. **sheet bbox**（实测）：`schematic.components.list --include-bbox` 里 `componentType == "sheet"` 的图元。
2. **模板识别**：用 sheet bbox 的**长宽比**匹配已知模板（A 系列横/纵向 ≈ √2）。公共 API 不暴露
   可靠的模板 id（deviceUuid / 符号名都拿不到），所以长宽比是识别键。
3. **标题栏矩形**：按匹配模板的**归一化比例**在 sheet bbox 的**右下角**（y-UP 坐标空间中靠
   **MaxX / MinY** 的角）切出子矩形。绘制矩形时 API 的左上锚点必须传 `(MinX, MaxY)`，高度向
   `-y` 延伸；固定 zone 与 partition 共用这一语义。比例表见 [`sheet-templates.json`](./sheet-templates.json)（Go 表 `sheetTemplates` 为运行时权威，
   此 JSON 为人/skill 可读镜像，二者须保持同步）。
4. **可见性**：`schematic.titleblock.get` 的 `showTitleBlock`；隐藏时**不**输出 keep-out。

返回的每条结果都带 **provenance**（`known-template-ratio` / `fallback-ratio` / `none`）与 `warnings`，
无法确定时只给警告、**绝不输出虚假精度**。规划器消费 `keepouts[]`（`{name, bbox, hard}`）即可，
不必关心几何怎么算出来的。
