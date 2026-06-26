---
name: easyeda-schematic
description: EasyEDA schematic automation skill. Use when working with EasyEDA schematic pages through the easyeda-agent CLI or daemon, including reading schematic context, listing components, placing or modifying components, creating wires and net flags, selecting primitives, running schematic DRC, saving schematic changes, and exporting BOM or netlist artifacts.
---

# EasyEDA Schematic

Use `easyeda-agent` typed actions. Do not write raw EasyEDA JavaScript unless a typed action is missing and the user explicitly accepts a debug path.

## Workflow

1. Run `easyeda health`.
2. Read active project and schematic context.
3. Inspect before mutating.
4. Prefer small additive operations.
5. Verify each mutation by readback, snapshot, or DRC.
6. Ask before destructive operations, multi-step mutation plans, or saving.
7. Summarize changed primitives, warnings, and artifacts.

## Drawing a schematic — library-first (default)

> **Design conventions live in the sibling [`easyeda-conventions`](../easyeda-conventions/SKILL.md) skill**
> (layout zones, spacing, wire/orientation rules, part-selection criteria, the
> canonical orientation table + standard-parts library). This operational skill
> **links** to it — single source, never copy the rules here.

Place **real parts from the EasyEDA / 立创(LCSC) library**, then wire them.
Hand-drawing a custom component symbol is the **fallback**, used only when the
part genuinely isn't in the library (a hand-built symbol loses the
footprint/supplier linkage and is error-prone — prefer a library part, even a
near-equivalent, first).

0. **Standard parts first.** Check [`standard-parts.json`](../easyeda-conventions/references/standard-parts.json)
   (in the easyeda-conventions skill) for the category you need (10k 0402, 100nF,
   ESP32-S3, AMS1117, USB-C, …). If it's there, place straight from its
   `{ libraryUuid, deviceUuid }` — deterministic, BOM-ready, with the real LCSC
   C-number. Only search when the category is missing, and ADD the chosen part back
   to `standard-parts.json` (with its C-number) so the next design is reproducible.
1. **Search** (fallback) `schematic.library.search` (free-text: an MPN, value+package,
   or a name like `ESP32-S3-WROOM-1`). Results are **reranked by relevance** (best
   category first; each carries a `score`), so the right part usually leads — but
   still sanity-check `value`/`footprintName`/`lcsc` before placing. Each candidate
   carries `uuid`, `libraryUuid`, `name`, `footprintName`, `lcsc`, `manufacturerId`.
2. **Place** `schematic.component.place` with the chosen `{libraryUuid, uuid}` at a
   coordinate → a manufacturable part with correct symbol + footprint + LCSC number.
3. **Read pins** (`schematic.components.list` / pin readback) for exact pin
   coordinates before wiring.
4. **Wire**: net flags for the power/ground rails; for a local signal net, route
   short wires that all meet at ONE junction point (star node) so EasyEDA junctions
   them — see the Electrical Rules below (pin → wire → flag, never flag-on-pin).
5. **Verify** with `schematic.drc.check` + the data linter
   (`scripts/lint.sh <project>`), and fix what it reports.

## Actions

Run `easyeda actions` for the current machine-readable action list.

### 导航 / Navigation

- `project.current` — 当前工程信息（uuid / name / teamUuid）
- `document.current` — 当前激活文档信息（uuid / tabId / documentType）
- `document.open` — 按 UUID 打开任意文档（原理图页或 PCB），通用版切换入口
- `schematic.pages.list` — 列出工程内所有原理图及页面
- `schematic.page.open` — 按 UUID 切换到指定原理图页（等同于 `document.open`，保留兼容）

多窗口说明：EasyEDA 每个窗口对应一个独立的 connector（windowId）。`system.health` 列出所有已连接窗口；在任意 action 中传 `--window <windowId>` 可指定操作哪个工程。

### 原理图编辑

- `schematic.components.list`
- `schematic.component.place`
- `schematic.component.modify`
- `schematic.component.delete`
- `schematic.wire.create`
- `schematic.netflag.create`
- `schematic.power.connect_pin`
- `schematic.select`
- `schematic.snapshot`
- `schematic.drc.check`
- `schematic.save`
- `schematic.export.netlist`
- `schematic.export.bom`
- `schematic.library.search`

### PCB（Phase 2）

坐标单位 = **mil**（原理图是 10 mil），**y-up**；元件绑定 TOP/BOTTOM 层，**无镜像只翻面**。布局动作默认作用于**当前选中**的元件，也可传 `primitiveIds`。

**读**
- `pcb.documents.list` — 列出工程内所有 PCB 文档（uuid + name），配合 `document.open` 切换到 PCB
- `pcb.components.list` — 列 PCB 器件；`includeBBox` 返回包围盒（判重叠/间距），`includePads` 返回焊盘 + net
- `pcb.layers.list` — 层信息（含 `copperLayerCount`，判 2 层 / 4+ 层）
- `pcb.nets.list` — 网络（net/length/color）
- `pcb.board.info` — 当前 Board（原理图↔PCB 关联），`import_changes` 前置

**同步 + 器件 CRUD**
- `pcb.import_changes` — 从原理图同步元件/网表到 PCB（主入口；ensureBoard→importChanges→刷飞线；**需确认**）
- `pcb.component.modify` — 移动/旋转/翻面/锁/位号
- `pcb.component.delete` — 删元件（**需确认**；返回布尔是"操作完成"非"确实删了"，别依赖）

**布局调整（确定性；EasyEDA 无原生对齐/网格 API，自实现）**
- `pcb.align` — 对齐 `left|right|top|bottom|centerX|centerY`（y-up：top = 大 y）
- `pcb.distribute` — 等间距 `axis=x|y`
- `pcb.grid_snap` — 坐标吸附到 `grid`（mil）
- `pcb.components.move` — 整组相对平移 `dx/dy`
- `pcb.components.arrange` — 粗布局种子：按共享局部 net 聚簇（`mode=cluster`）或网格打包（`mode=grid`），跳过 locked

## Bundled Scripts

| 脚本 | 用途 |
|---|---|
| `scripts/lint.sh <project>` | 原理图数据 lint（几何 + 连通性检查，无需截图）。有 baseline 时显示 DIFF |
| `scripts/lint.sh <project> --save` | 全量 lint 并记录 baseline |
| `scripts/bom-enrich.py <bom.tsv>` | 将导出的 BOM 里 `SupplierId` 从 MPN 补全为 LCSC C 号 |
| `scripts/parts-select.py` | 器件选型辅助工具 |

标准器件库（`standard-parts.json`）、flag 旋转真值表（`orientation.json`）、布局/选型约定都在
**[easyeda-conventions](../easyeda-conventions/SKILL.md)** skill（单一真源，勿在此复制）。
`bom-enrich.py` / `parts-select.py` / `orient.py` 会跨 skill 自动读取这些 canonical 文件。

## Guardrails

- Confirm before deleting primitives.
- Confirm before saving unless the user explicitly asked to save.
- Confirm before running a generated multi-step mutation plan.
- Do not claim completion after mutation until verification succeeds or the remaining risk is stated.
- Treat `File` and `Blob` outputs as artifacts.
- If DRC fails, report violations and propose the smallest repair step.

## Layout Conventions

### 原理图

When placing components, follow the easyeda-conventions skill — [schematic-layout-conventions.md](../easyeda-conventions/references/schematic-layout-conventions.md):
- Zone map (power left, MCU center, RF/sensors right, big modules in corners)
- Module spacing rules (80–500 units depending on size + pin count)
- Wire stub lengths (20–40 units for power, 20–60 for signals)
- Right-angle-only routing, decoupling caps within 30 units of VCC pins

### PCB 布局

PCB 自动布局/调整时遵循 easyeda-conventions skill 的 [pcb-layout-conventions.md](../easyeda-conventions/references/pcb-layout-conventions.md)（完整规则 + 检测方法）。要点:

**优先级裁决(冲突时高覆盖低)**:P0 机械/外壳锁定 > P1 安全间距/隔离 > P2 EMI 热回路 + 关键去耦贴近 > P3 参考平面/回流连续 > P4 热 keep-out > P5 功能分区 > P6 DFM > P7 网格/对齐/丝印(纯美化,**绝不覆盖功能位**)。

**执行步骤**:
1. 前置:`pcb.components.list`(`includeBBox`+`includePads`) + `pcb.layers.list`(取 `copperLayerCount`) + `pcb.nets.list`,按 net/designator 给每件打类(anchor/hot/sensitive/IC/passive)。
2. **P0**:连接器(J/USB)、安装孔(H/MH)按外壳坐标先放 + `lock`,作不可动障碍;板边连接器开口朝外。
3. **P6 粗聚簇**:`pcb.components.arrange mode=cluster` 得初始排布。
4. **P2/P4 就地覆盖**:去耦电容贴 IC 电源脚(≤2 层 ≤150 mil;4+ 层 ≤250 mil 但**留打孔空间**);晶振 + 两负载电容贴 MCU 振荡脚带 200 mil 守护环;开关输入回路 {Cin+开关+续流} bbox 最小;热源彼此 ≥400 mil,怕热件(电解/晶振/传感器)离热源 ≥200 mil。
5. **P7 收尾**:`pcb.align`/`pcb.distribute`/`pcb.grid_snap`(SMD 25 mil / THT 50 mil)对齐吸栅,不得破坏功能位。
6. 复跑 `pcb.drc.check`;改前重取 primitiveId(无 undo),破坏性操作需确认,before/after 进审计日志。

**关键纠偏**(评审结论):去耦有效性取决于电容自身**安装回路电感**(pad→via→平面),不是单纯"离 IC 多近";**默认单一完整地平面 + 摆位分区,不默认割地**;所有硬阈值条件化于叠层/工艺/外壳上下文。

## EasyEDA Electrical Rules (load-bearing — DRC will fatal if ignored)

EasyEDA's DRC does **not** treat two primitives sharing the same coordinate as electrically connected. Every connection needs a real `schematic.wire.create` between them. Two concrete consequences:

1. **`schematic.netflag.create` MUST NOT be placed on the same point as a pin.** Placing a +3V3/GND/IN/OUT flag at the exact pin coordinate produces a DRC fatal: *"端点重叠且未连接 / endpoints overlap but not connected"*. The flag sits on top of the pin visually but EasyEDA treats them as two disjoint endpoints.

   Correct pattern: pin → short wire → netflag at the wire's far end. Typical offset: 20 grid units (EasyEDA uses 0.01 inch / grid unit on schematics). Example for `+3V3` on `R1.pin1 @(265, 440)`:

   ```text
   schematic.wire.create     points = [265,440, 245,440]   # pin to a free point
   schematic.netflag.create  x = 245, y = 440, kind=power, net="+3V3"
   ```

2. **Wires must have non-zero length.** A wire of `[x,y, x,y]` is silently ignored; a wire of `[x,y, x+0,y+0]` will not register a connection.

3. **NC pins still need explicit marking.** A pin without any wire/flag triggers a "悬空 / floating" warning even if your design intends it unused. Use a Non-Connected flag for those.

Apply this rule when generating any power/ground/port connection — emit the wire first, then place the flag at the wire's free endpoint.

## Missing Actions

When a needed operation has no typed action:

1. Decompose it into existing actions if possible.
2. Otherwise state the missing action name and expected inputs/outputs.
3. Use `debug.exec_js` (raw `eda.*` JavaScript) only as a temporary, user-confirmed debug escape hatch. Its result must be JSON-serializable — base64-encode any `Blob`/`File` inside the snippet.
4. Recommend promoting repeated debug code into a typed action.
