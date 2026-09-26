# 立创/JLC 比对选型 (mall comparison part selection)

Roadmap item ③. Turns "pick a part" from an arbitrary `library.search` first-match
into a **data-driven choice**: the cheapest, in-stock, JLC-**basic**, spec-matching
part — so the BOM is manufacturable without surprise feeder fees or stockouts.
排名只提供候选；是否满足电气要求仍须按下面的来源核验流程确认。

## 选型前先查块

**标准外围(RS-485 / buck / USB 串口 / GNSS / 充电 / microSD…)先 `easyeda blocks search <关键词>`** ——
命中后块的 `parts` map 直接给出 `standard-parts.json` 的 role，可复用候选身份；仍须核对
当前需求与器件参数，不能省略下面的参数核验。只有块里没有、或板级专有件才重新搜索候选。

**ESP32 自动下载块的电平语义**：`block.esp32_autodownload` 的交叉耦合拓扑按
`DTR,RTS → EN,IO0` 引脚电平核对：`11→11`、`00→11`、`10→01`（复位）、
`01→10`（BOOT 拉低）。旧版 notes 曾将后两行写反；不能据旧说明反接电路。
这里的 0/1 是引脚电平，串口 API 的 DTR/RTS asserted 为低电平；双低不持续复位。
进入下载还需要复位释放时采样 GPIO0，结合 EN 上拉、电容与控制时序核对，静态表不证明实板烧录。
来源：[Espressif DevKitC V4 原理图](https://dl.espressif.com/dl/schematics/esp32_devkitc_v4-sch.pdf)
与 [启动模式说明](https://docs.espressif.com/projects/esptool/en/latest/esp32/advanced-topics/boot-mode-selection.html)。

## 数据手册优先

标准器件条目应同时保存 `datasheetUrl`（立创型号页）和可直接读取的
`datasheetPdfUrl`。确定外围电路、引脚功能、典型应用、去耦值和布局约束时，先读取对应
数据手册；块库中的 `source`/`note` 只作为已验证摘要，不能替代型号手册。若页面型号、
PDF 或封装与当前库器件不一致，停止自动绘图并重新核对器件身份。

### 参数核验：保留原始单位，不从料号猜数值

1. 先锁定厂商、完整 MPN、LCSC C 号和封装。已有图面上的 value 是待核对的标注，
   搜索排名、料号中的数字和先前模型回答均不能单独证明电气参数。
2. 读取该 C 号目录中的具名参数（例如 `Resistance`）与对应手册。保留来源 URL、
   原始参数文字及手册页码/表名，再另列换算值；`330mΩ = 0.33Ω`，`33Ω` 相差 100 倍。
   SI 前缀区分大小写：`mΩ` 为毫欧、`MΩ` 为兆欧。不能先 lowercase、删除小数点或做
   数字子串匹配后判断数值相等。
3. 只有读到**该厂商、该系列、该编码位置**的规则，才解释料号。不要把完整 MPN 中的
   `330` 当成独立三位电阻标记，也不能把不同系列的 R 小数点记法、EIA 标记混用。
4. 目录、手册、库 value 不一致时，记录冲突并继续查证；来源无法读取或参数缺失时标为
   “未核实”。不得填入猜测值、写回标准库或用它完成选型/修改电路。
5. 交付时给出“器件身份 → 来源原文 → 单位换算 → 图面/BOM 对账”。阻值会影响采样、
   分压和功耗计算，原参数被纠正后须重新计算受影响部分；只改显示文字不足以完成验收。

**#202 回归事实**：UNI-ROYAL `0805W8F330LT5E` / **C52548** 的目录 Resistance 是
**330mΩ（0.33Ω），±1%**。对应厂家手册《Thick Film Chip Resistors》Version 3，
第 2 页（Page 2/9）§2.4.2–2.4.3 / §3 说明：≤2% 系列以前三位为有效数字，末位为倍率，
`L = 10⁻³`，故 `330L = 330 × 10⁻³ Ω = 0.33Ω`。该编码解释仅用于这份手册覆盖的系列。
来源：[LCSC 型号页](https://item.szlcsc.com/53562.html)、
[对应 PDF](https://datasheet.lcsc.com/datasheet/pdf/0a975aaa49b7c97f38a963127be4a823.pdf?productCode=C52548)。

**同名型号不是同一厂商**：`esd.usblc6_2sc6` 的 C2687116 是 UMW USBLC6-2SC6，
不能用 ST 同名器件的 DS4260 为它背书。对应 UMW 手册 Nov.2024 第 3 页给出
25°C、0Vdc、1MHz 下 I/O-GND 电容 typ 0.8/max 1pF、I/O-I/O typ 0.35/max 0.4pF，
不能把典型值或“<1pF”当所有条件下的上限。确切身份与手册链接保存在标准库条目中。

**颜色和厂家按当前身份核对**：C2296 的 KENTO `KT-0805Y` 是黄色 LED，原厂 A.0 手册 p3 在 25°C、IF=10mA 列 Vf 为 1.8–2.4V；
封装/3D 名称中的 RD 不能证明红色。新设计使用 `led.yellow_0805`，旧 `led.red_0805` 仅为同器件兼容键，
不满足指定红色的要求。C474881 的 `KF301-5.0-2P` 当前官方库制造商为 KEFA（科发），
不沿用旧厂家文字；原 C 号、device UUID 与引脚拓扑未换。型号页链接保存在标准库条目中。

## Data sources (live, no API key, browser User-Agent)

| Source | Endpoint | Gives |
|---|---|---|
| **JLCPCB SMT** (primary) | `POST jlcpcb.com/api/overseas-pcb-order/v1/shoppingCart/smtGood/selectSmtComponentList` | `componentLibraryType` (**base**/expand), `stockCount`, tiered `componentPrices`, `preferredComponentFlag`, `attributes`, MPN, LCSC C#, category |
| LCSC wmsc (optional) | `GET wmsc.lcsc.com/ftps/wm/product/detail?productCode=C…` | richer specs/catalog/datasheet |

Both verified reachable. The JLC SMT call gives the buy-side signals; that is the
backbone of 比对选型. **Basic parts only appear when you pass
`componentLibraryType:"base"`** — JLC's default search returns extended parts in the
top page, so the selector queries base + general and merges.

## Where it lives

Tool-side (`.agents/skills/easyeda-agent/scripts/parts-select.py`) or daemon-side — **NOT the connector**: the
EasyEDA webview can't make these cross-origin fetches; the daemon/tool can.

## Ranking (`.agents/skills/easyeda-agent/scripts/parts-select.py`)

Tuple sort — each tier breaks ties of the one above:

1. **Resistance gate** — for a query with an explicit `Ω`/`ohm` unit, require a numerically
   equal resistance from a named catalog attribute or an explicit value. Keep SI
   prefix case and units; `330mΩ` matches `0.33Ω`, never `33Ω` or `330MΩ`. MPNs,
   C-numbers and incidental prose numbers cannot supply the resistance. Missing,
   ambiguous or conflicting values are not a match. Remaining text relevance
   only ranks candidates that pass this gate; other specifications still require
   manual source verification.
2. **Buildable** — `stockCount ≥ build qty`, so the pick can actually be ordered. A
   basic part with too little stock yields to an in-stock one (marked `!` in the
   table); the build qty makes this stock-aware (10k basic wins at qty 100, yields at
   qty 5000 when its stock can't cover it).
3. **Basic** (`componentLibraryType=base`) — avoids the per-extended feeder fee.
4. **Preferred** flag — quality/availability signal.
5. **Cheapest** unit price at the build qty — final tiebreaker.

```
parts-select.py "100nF 0402 X7R" --qty 100        # → offline hit in standard-parts.json (default; zero network)
parts-select.py "10uF 0805" --json                 # → C440198 ; machine-readable for the agent
parts-select.py "esd array usb" --online           # explicit opt-in: JLC catalog compare, then converge via `easyeda lib by-lcsc`
parts-select.py "0.33Ω 0805" --online --json       # explicit resistance gate; may match a catalog Resistance of 330mΩ
```

阻值门禁支持 `Ω`/`Ω`/`ohm`/`ohms` 与 `m`、`k`/`K`、`M` 前缀；不能解析的带单位
表达式保留为无精确候选，不猜倍率。`10k` 等不带显式单位的查询仍为模糊搜索，严格核对
阻值时写 `10kΩ`。JSON 的 `resistance` 保留 `raw`、`ohms`、`source`，文本推荐也显示
该阻值证据；按 MPN/C号搜索时同样尽量保留该字段，但字段缺失不等于已核实。
显式阻值查询没有精确候选时退出码为 1（`--json` 输出 `[]`），不能当作成功选型继续放置。
普通模糊查询沿用原来的空结果行为。

## Integration — closes the standardization loop

```
need a part ──▶ parts-select (pick optimal LCSC C#)
            ──▶ library.search by that C#/MPN  →  EasyEDA {libraryUuid, deviceUuid}
            ──▶ component.place
            ──▶ add to standard-parts.json (now a DATA-DRIVEN, in-stock, basic choice)
            ──▶ export.bom + bom-enrich  →  orderable BOM with the C#
```

This makes [`standard-parts.json`](./standard-parts.json) selections justified
by live stock/price/basic data instead of a guess. Validated: 100nF→C1525,
10µF→C440198, AMS1117→C6186 — all matched the curated standard library.

**Already-placed parts** (standardizing an existing schematic — the 器件标准化
panel's use case): don't delete+re-place by hand. `easyeda sch replace --id
<primitiveId> --lcsc <C#>` swaps the placed component for the recommended device
in one call — keeps designator/uniqueId/pose, drops the OLD part's identity
fields (they follow the new device), rolls back on failure, and reports a
`pinDiff`; a non-empty pinDiff means re-wire, then `sch drc`/`sch check`.
Property-only assignment (writing a C# onto a part without changing the device)
stays `easyeda sch modify --patch '{"supplierId":"C…"}'`.

## 站点差异：deviceUuid 必须按当前版本重解析

**已验证事实**（Windows 11 + EasyEDA Pro 桌面版 3.2.149 **国际版**（easyeda.com）+
easyeda-agent v1.5.1）：国际版与国内版（lceda.cn）的系统库 **libraryUuid 相同**
（`0819f05c4eef4c71ace90d822a990e87`），但**器件 uuid 不同**。
[`standard-parts.json`](./standard-parts.json) 的 `deviceUuid` 采自国内版，在国际版上
**143 件全部对不上（0/143）**。

症状：`easyeda sch block-apply <block>` 在**第一个** place 就失败，报

```
schematic.component.place failed: connector did not respond
```

平台对未知器件 uuid 不给任何回执，所以表现成超时，不是 “not found”。证据：同一台机器上
`easyeda lib by-lcsc --lcsc C8678` 返回 `804240ef97df427480be2a5281ccea31`，而文件里写的是
`009407eaaa604eb9b6f73cc3868f316d`。`lib by-lcsc` 是按当前连接的编辑器解析的，是本地事实源。

### 工作流

```bash
# 1. 按当前连接的编辑器重解析，写到副本；--out 必填，绝不就地覆盖源文件
python3 scripts/parts-relocalize.py --out /tmp/parts.intl.json --project <project>
# 2. 用这份副本 apply
easyeda sch block-apply <block> --parts /tmp/parts.intl.json --project <project>
```

在 Skill 根目录运行（Windows 用 `python`）。脚本把所有 LCSC C 号按 ≤20 个一批送
`easyeda lib by-lcsc`，逐件比对：uuid 相同 → 原样；不同 → 写入解析到的 `deviceUuid`，
原值保留在 `deviceUuidOrigin`；没解析到 → 条目一个字段不动、加标记
`"_relocalize": "unresolved"`。返回的 `libraryUuid` 与文件不一致时只告警、**不改写**
（标 `"_relocalize": "libraryUuid-mismatch"`）。`--dry-run` 只查询不落盘，`--json` 输出
机器可读摘要，`--easyeda` / `--window` 分别指定二进制与目标窗口。需要已连接的编辑器。

### 限制

- **结果是该站点/该账号局部的**：不要提交回 `standard-parts.json`，仓库里的 canonical
  值仍是国内版身份；每台机器各自生成副本，换版本或换站点重跑。
- **会有未解析项**：实测 143 件解析出 140 件，3 件未解析（含 `lcsc` 写成占位符
  `(onboard)` 的 `ic.pc817_sop4`）。未解析件保留原 uuid，apply 到该件仍可能失败；按
  summary 打印的 key 逐个人工处理，不要让脚本猜。
- **同一 C 号解析到多个器件时不选**，标 unresolved 交人判定。
- **块的 `schematic_layout` 模板仍可能与国际版符号的引脚几何对不上** —— 那是另一个已知
  问题，重定位 uuid 不解决它；apply 成功后仍须 `sch layout-lint` / `sch check` 回读。

## Known limitations / refinements

- **Basic-search page depth** — *fixed*: the base-filtered query fetches a generous
  page (50), so a wanted basic that JLC ranks low (e.g. 10k C25744) still surfaces and
  the relevance gate picks it. A category filter would harden this further.
- **Other spec-attribute matching** — voltage / tolerance / temperature and other
  non-resistance specifications still use text relevance, not numerical gates.
  The selector does not read datasheets or validate a manufacturer's MPN encoding.
- **Caching & rate limits** — cache JLC responses; the APIs are unofficial and may
  change headers/shape.
- **Promote to a typed action** — `schematic.library.select` (daemon-side) so the
  agent gets a ranked pick directly instead of running a tool.
- **LCSC mall breadth** — JLC SMT covers assembly-stocked parts; for non-assembly
  catalog parts, add the LCSC wmsc search path.
