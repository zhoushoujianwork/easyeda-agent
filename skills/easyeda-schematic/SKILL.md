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

> ⚠️ **多器件 / 整板设计:先花几分钟摸底,再动手。** 非平凡板子(>~10 件,或要交付/排 PCB)
> place 前快速读懂设计(器件/电源树/功能分组/幅面)——见
> [`design-pre-analysis.md`](../easyeda-conventions/references/design-pre-analysis.md)(轻量摸底,不是门禁)。
> 然后照 [`auto-layout-sop.md`](../easyeda-conventions/references/auto-layout-sop.md)
> 的 CLI 能力 + 硬坑落坐标,布局**用数据 + 截图自调**(放→读回坐标→判间距/线长→挪→再读)。
> 小改 / 几个器件直接按下面放置。

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
4. **Wire** (reference-validated — see **画线 / flag / 去耦(CLI 级硬规则)** in
   [`auto-layout-sop.md`](../easyeda-conventions/references/auto-layout-sop.md);
   the 嘉立创 ESP32-S3 standard project is **flags only on power/ground rails, every
   signal a real local wire**):
   - **Signals = real local orthogonal wires** (pin→wire→pin). Endpoint on a pin coord
     = connected; non-aligned pins → L-route `[x1,y1, x2,y1, x2,y2]`.
   - ⚠️ **Never run a wire through another pin** — EasyEDA trims+connects it there.
     Route in pin-free channels.
   - ⚠️ **Multi-pin nets: chain pin→pin** (each segment anchored on a pin), NOT a star
     to a free junction (EasyEDA drops the un-anchored junction on merge).
   - **Flags ONLY for power/ground rails** (`connect_pin direction=`, never blanket rot 0).
5. **Verify** with `schematic.drc.check` + the data linter
   (`scripts/lint.sh <project>`). ⚠️ After API edits the **EasyEDA canvas may not
   auto-redraw** → `schematic.snapshot` / `getCurrentRenderedAreaImage` return a STALE
   frame (even `view fit` framing is stale). **Judge STATE by data (`sch list`/`getAll`),
   use the screenshot for visual layout only**, and touch the page in EasyEDA (scroll/
   click) to force a redraw before trusting a snapshot.

## Bulk realization from a netlist (automated)

For a whole board (place ~N parts + wire the full netlist at once), the manual flow
above doesn't scale. Pipeline (proven on box-v2/110 parts):

1. **PLACE-ALL** — for each part, resolve `{libraryUuid, deviceUuid}`
   (standard-parts.json first, `lib.search` fallback), place at coords, then assign
   the designator (`sch modify --patch '{"designator":...}'` — place leaves it `C?`).
2. **READ-PINS** — ONE `sch list` / pin pull AFTER all placement for real pin coords
   (don't trust pre-place maps; map IC functional names → physical pads first).
3. **WIRE** — per net, decide flag vs local wire vs label (see the decision table in
   the SOP); emit flags via `connect_pin direction=` (never blanket rot 0).
4. **DRC + lint**, then a **MANDATORY clustering/zone pass** before "done".

> ⚠️ **Layout is NOT optional.** Naive place-at-synthesis-coords + flag-every-pin is
> electrically valid but **visually scattered** (box-v2: 327 flags, decaps far from
> ICs). **Follow [`auto-layout-sop.md`](../easyeda-conventions/references/auto-layout-sop.md)**
> (easyeda-conventions): fit sheet → mains by zone → auxiliaries pin-relative to their
> owner IC → fine-tune. And **write resolved parts back into `standard-parts.json`** in
> the same change (so the next board doesn't re-search non-deterministically).
>
> **Churn-resilience for >~50 mutations** (essential, see the SOP): route by
> `--project`; batch many primitives per `debug.exec_js`; chunk each batch to <~20s
> (long calls die to the heartbeat); heavy-retry + incremental `sch save` per chunk;
> re-pull fresh pids each chunk.

## Actions

Run `easyeda actions` for the current machine-readable action list.

### 导航 / Navigation

**自助「发现 + 切换」闭环（首选）** — 不要让用户手动开窗口/切页,Agent 自己发现并切换:

```bash
easyeda daemon health                         # 发现:有哪些已连接窗口 + 各自实时上下文
easyeda doc ls     --project <名字>            # 发现:列出该窗口所有可开文档(原理图页+PCB),★=当前前台
easyeda doc switch <P2|PCB1|uuid> --project <名字>   # 切换:按页名/PCB名/uuid 切到前台,自动回读确认
```

- `easyeda doc ls` 聚合了 `schematic.pages.list` + `pcb.documents.list` + `document.current`,一条命令看全貌;`--json` 给机器读。
- `easyeda doc switch` 按名字解析 → `document.open` → `document.current` 回读确认。**同名页(多个 P1)会报歧义并列出 uuid,改传 uuid**。跨类型也行(PCB ↔ 原理图)。
- **多窗口时必须 `--project`(或 `--window`)**:`doc ls`/`doc switch` 不带目标时,只有「恰好一个窗口」才能自动命中;两个及以上窗口会报 `no EasyEDA connector is available`。同理,某窗口连接器正在重连(churn)的瞬间也可能瞬时报这个,重试即可。

底层 action(需要细控时再用):

- `project.current` — 当前工程信息（uuid / name / teamUuid）
- `document.current` — 当前激活文档信息（uuid / tabId / documentType）—— **实时读取**,不是连接快照
- `document.open` — 按 UUID 打开任意文档（原理图页或 PCB），通用版切换入口
- `schematic.pages.list` — 列出工程内所有原理图及页面
- `schematic.page.open` — 按 UUID 切换到指定原理图页（等同于 `document.open`，保留兼容）

多窗口说明：EasyEDA 每个窗口对应一个独立的 connector（windowId）。`easyeda daemon health` 列出所有已连接窗口;**优先用 `--project <名字>` 路由**(windowId 重连会变),细控时才用 `--window <windowId>`。

> **上下文是实时的,不会卡在 `home`。** 两条刷新路径:① daemon 用每次 action 响应里的实时上下文刷新缓存;② 连接器 **v0.5.7 起,心跳(~3s)会主动重读当前文档,变了就推**——所以用户在 EasyEDA 里**切了 tab、什么命令都没跑**,`daemon health` 也会在 ~3s 内自己跟上。若 health 显示某窗口是 `home`,说明它的前台 tab 停在开始页/欢迎页,或那个窗口跑的是旧连接器(< v0.5.7)没连上。
>
> **UI 切页要双击**:单击只选中 tab、不打开文档;双击才真正打开,`document.current` 读到的是「已打开」的那个文档。
>
> **`connectorVersionOk: false`** = 该窗口加载的连接器版本与 daemon 不符(典型:开着的窗口跑着旧连接器代码)。处理:完全退出并重启 EasyEDA 重新加载连接器(re-import 不会刷新已开窗口)。`null` 表示版本号非 semver(dev 构建)无法判定。

### 原理图编辑

- `schematic.components.list`
- `schematic.component.place`
- `schematic.component.modify`
- `schematic.component.delete`
- `schematic.wire.create`
- `schematic.netflag.create`
- `schematic.power.connect_pin`
- `schematic.select`
- `schematic.snapshot` — 截图。**产物保存在 CLI 运行目录下的隐藏目录 `<cwd>/.easyeda/artifacts/`,文件名带本地时间戳**(`<YYYYMMDD-HHMMSS>-<kind>-<短id>.png`,便于排序/查找);响应里的 `artifacts[].path` 是绝对路径。netlist/BOM 等其他产物同此规则。
- `schematic.drc.check`
- `schematic.save`
- `schematic.export.netlist`
- `schematic.export.bom`
- `schematic.library.search`

### PCB

PCB 操作（切到 PCB、读器件/层/网络/Board、从原理图 `import_changes` 同步、布局摆位
move/rotate/align/distribute/grid_snap/cluster-arrange）在独立的 operational skill
**[`easyeda-pcb`](../easyeda-pcb/SKILL.md)** —— 见那里(单一真源,勿在此复制)。

## Bundled Scripts

| 脚本 | 用途 |
|---|---|
| `scripts/sch.py` | **稳定执行器**（import 用）— 把核心 CLI 封成 churn-resilient API:`read()`/`place()`/`move()`/`wire()`(SOP-W 正交避引脚)/`rail_flag()`(SOP-F 定向)/`decouple()`(SOP-D)/`connectivity()`(union-find 真连通)/`snapshot()`(取 .easyeda/artifacts)。AI 数据自调闭环用:放→`read`→判→`move`→`connectivity` 验。 |
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

> **PCB 布局**约定在 [pcb-layout-conventions.md](../easyeda-conventions/references/pcb-layout-conventions.md)，操作流程在 [`easyeda-pcb`](../easyeda-pcb/SKILL.md) skill。

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
