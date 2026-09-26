# 功能状态与路线图

本文件记录当前可用能力；typed action 的权威来源是 `make actions`，实现映射见 `internal/protocol/actions.go` 与 `extension/src/actions.ts`。相关领域的待办与边界在本页及 [CLI 索引](cli/README.md) 维护，生态调研见 [`ecosystem-survey.md`](ecosystem-survey.md)。

日常使用范围、集中检查是否收尾及待修复问题的当前总览，统一见 [CLI Status](cli-STATUS.md)。

本页按功能领域盘点能力，不表示测试层级。单个 CLI 动作的真实链路按[基础测试](cli-live-test.md)验收；选型、规划、求解、Apply 和整板设计结果按[高级测试](cli-advanced-test.md)验收。基础门禁全部通过后才进入高级现场验收。

> 主线宿主已切换到 EasyEDA Pro V4；推荐 V4.1.60+。当前 V4 状态、P0 门禁和现场验证边界见
> [`v4-development.md`](v4-development.md)。V3 历史实测记录仅作回归参考，不再代表当前主线。

## 当前基线

- PCB 配置 CLI：`pcb config get/clearance/track/via/bind/net-color`，覆盖考试中的安全间距、线宽规则（含复制新建 PWR）、过孔尺寸、现有网络类绑定和网络 RGB 颜色；支持单位换算、dry-run、保留其余配置及严格写后回读。2026-09-20 已在 Web 3.2.203 的考试 PCB `PCB1_1` 完成实际写入、保存、重载、幂等重放和完整恢复，规则、网络及 69 个组件最终与基线一致。固定 ESP32 回归已验证配置与四层/铜面持久化，但整板 DRC 因启发式走线穿越天线禁区/机械槽、连接错误及内层 PLANE 类型重载回退而未通过，不能记作完整 E2E；网格/吸附等全局偏好仍 unsupported。

- 私有器件库现场佐证：[AS07-M1101D-SMA](examples/as07-m1101d-sma/README.md)。从用户尺寸/引脚图创建 Symbol、Footprint、Device，再按反馈修正符号和框外丝印；保留最终规格、官方渲染和回读数据。额外文字及修正使用官方 API 调试路径，不代表单条 build 已覆盖；未完成实例接线、PCB DRC 或实物装配验证。
- 原理图统一架构：[数据驱动架构基准](../.agents/skills/easyeda-agent/references/schematic-data.md#数据驱动架构基准)。原始快照保留，源数据驱动计算、检查和修复；不是现场逐件试摆后看图兜底。
- 通用两层布局：`layout-plan --zones` 消费明确核心/外围归属和约束，`layout-sheet-plan` 只选择/平移完整候选；固定 `layout-render` 与 `compose --layout-page` 保留同一目标。任一区失败不能拼半成品。
- 局部数据编辑：`sch layout-edit` 按稳定 ID 将核心及其唯一归属 zone 作为一个相对坐标系平移；刚体目标碰撞时固定核心目标并仅重算本区。单脚标签修复只沿官方引脚外向轴生成候选，并通过 daemon 作用域 action 逐对象核对、串行替换和回读；普通写线仍按目标连接表和实际回读核对。
- 检查范围：位号参与遮挡/入框，其他器件属性文字排除页面碰撞和框包络；当前实现/安装版是否覆盖须按真实报告举证，不以规范代替验证。
- 原理图：以 Connectivity IR（器件、引脚、网络、pin-to-net）为电气事实，布局与 Lib 模块复用不得改变连接核心。
- 本地设计比较：`sch design-diff` 按稳定ID核对完整canonical字段与两份compose计划的图形数据，报告内容哈希和未验证范围。
- Lib 内部计算：`sch lib-layout` 根据实测姿态/引脚与canonical网络，计算核心及串联外围的局部位置、短线和局部电源地，再输出compose源。搜索有界，不推断缺失电路或擅自旋转。
- 固定 LDO 样例：`sch power-layout` 根据实测 pin/bbox 离线计算四器件位置、直连导线及 `sch apply` 队列；`expectSchematic` 校验移动前后几何与完整引脚网表。[验收及范围](reviews/power-layout-validation.md)。
- 模块呈现：`sch frame apply/check` 将 JSON 转换成粉色虚线框和 0.2 inch 标题,回读样式/实际文字边界并保持重复执行幂等。标题按分项占位选择上下空档压缩框高度,可用实测文字尺寸规划、携带预测包络与障碍物核验。各模块压缩后由共享 Z 字行规划器从左上起排、同行顶齐、各框保留自身高度；相对实测sheetBorder保留最小10 raw净距。[转换契约](schematic-frame-conversion.md)。
- 单页组合：`sch compose` 以完整 IR 和实测 Lib 几何生成同页位置及严格 Apply 队列；校验实际 bbox、全部 pin/net/NC、导线路径和标记方向。跨页位号须唯一，不自动删除源页。[组合契约](schematic-page-composition.md)。
- 位号：`sch designators allocate/plan/verify` 按官方库前缀修复非标准名称，保留合法编号与稳定 ID；原地队列核对位置、引脚/网络/NC、导线与全工程位号。[使用合同](../.agents/skills/easyeda-agent/references/schematic-data.md)。
- PCB：`layout-lint`、`layout-score`、`pcb check` 与 DRC 分别报告布局、质量、制造和电气事实；它们不授权或拒绝普通 action。
- PCB 独立求解内核：公开 Go 包 `pkg/pcbrouting` 被晶振规划和 `pcb route solve/check` 共用，
  无额外 CLI 安装；当前为单层零过孔、直线/45°有界寻路与独立路径校验（离线能力）。
  快照适配层处理真实几何/规则，未知数据、圆弧铜及有限搜索失败保持 incomplete；
  每次运行同时生成整板 SVG，覆盖目标层焊盘、铜、开槽、禁线区、搜索边界、候选线宽/净距
  和失败原因。`pkg/pcbmodel`、`pkg/pcblayout`、`pkg/pcbsolve` 进一步提供固定装配面的二层
  布局/共同路由闭环；`pcb layout solve/check/render` 读取完整快照，依据实际阻挡让完整组
  让位，支持 TOP/BOTTOM 与有预算的两过孔路径，并按事实向量保留最多三个候选。solve 同时
  输出基线、各次尝试和最终候选的整板/自动聚焦局部 SVG。该能力已通过文件案例、独立复验及
  `ceshi` fresh dump 的离线重放。后续新增 `translationSearch`，从实测冲突动态生成位置，
  支持机械冲突递归让位并输出可复验的通道预留；自动移动要求完整外部连接图。
  旧 ceshi 直线小案例曾通过两轮 typed 回读，但用户随后指出通道预留与块间调整仍不足，
  该记录不作为当前方案 Layout 完成或确认依据。新双网窄口竞争案例已在合成数据离线验证，
  并在 `ceshi/PCB1` 完成测试通道的自动让位、layout-only Apply 和连续两轮保存重载回读。
  这不证明整板所有连接可布通；现场 DRC 仍有连接/网表错误，规划路径未写铜；四层仅建模并
  明确拒绝联合求解。
  [输入与边界](../.agents/skills/easyeda-agent/references/pcb-routing.md#离线单层寻路与独立复验)。
- PCB 模块候选：`pcb layout-plan` 纯本地读取 `pcb dump` 与显式模块/pad 所有权，有限枚举
  `edge`、`pin-satellites`、`rigid` 及 schema-v2 `crystal-guard` 候选，输出事实、局部/整板/
  前后对比 SVG 和 typed Apply；不访问编辑器、不合成总分。带铜候选用
  `pcb dump --include-copper` 的语义哈希绑定 fresh 基线，执行后以 `pcb module-check` 对账
  journal、器件、铜、双层禁铺区、实际铺铜和非目标差异；`affectedBaselinePours` 显式限定
  pour-rebuild 可在参数化影响包络内修改的既有材料化铺铜，包络外和未声明对象严格保持。
  晶振验收逐个检查 fence/anchor via 的 TOP/BOTTOM 实际 GND 同岛连接，并证明 OSC ordered
  path、fresh pad geometry、实际长度/转折、capture PID 与 polygon/holes/ARC。LED/MCU/LDO 的 260919 样例已离线复算，
  晶振策略已自动测试、尚待本轮现场保存重载后升级为 live-verified。
- PCB Layout 复核图：`pcb snapshot/stage-snapshot --fit-mode board` 用公开板框适配加视口 PNG，记录实际 fit API、降级和 `objectLevelExport:false`；右键菜单的对象级 PNG/SVG 导出尚无公开 `eda.*`，未接入内部 message bus。[能力边界](pcb-image-export.md)。
- 样例驱动：Agent 选择相近样例，理解理由并替换参数，执行后根据实际回读修正。260919 AT32F415 考试资料已整理为 69 个器件、233 个端子、13 处明确 NC、46 个网络、15 个功能区和 36 个技术点；代表性原理图、机械、LDO 与模块 Layout 已有 `live-verified` 证据，其余项目继续保留 `source-only` / `offline-verified` 边界，不能外推成整板完成。
- typed actions 的精确清单始终以 `make actions` 为准，不单独维护数量。
- 真实回归输入、事实检查和运行步骤见 [`e2e-automation-acceptance.md`](e2e-automation-acceptance.md)。

## 260919 Demo 驱动的新接口（离线验证）

以下接口已通过 Go / Connector 自动测试；在 Web 版 EasyEDA 完成保存、重载和回读前，状态仍是
`offline-verified`：

| 能力 | Action / CLI | 当前语义 |
|---|---|---|
| 工程建立 | `project.create` / `project create` | 创建工程并区分“创建成功但未打开”的部分结果，不改变 `project open`。 |
| 真圆角板框 | `pcb.outline.round` / `pcb outline-round` | 一个闭合、锁定的 polyline，四角使用原生 90° ARC；`outline-get` 回读中心线尺寸、半径、线宽、锁定和弧段来源。 |
| 显示原点 | `pcb.origin.get/set` / `pcb origin get/set` | 读写显示坐标 offset，不移动板框、器件或铜。 |
| 完整 DRC 规则 | `pcb.drc.rules.set` / `pcb drc-rules-set --from` | 读取完整规则副本后写入；支持 dry-run、部分失败回滚和最终回读。 |
| 原生网络类 | `pcb.netclass.list/create` / `pcb net-class list/create` | 创建并回读真实 EasyEDA 网络类、网成员和规则关联；与启发式 `pcb net-classes` 区分。 |
| 字体 | `pcb.silk.create/modify` / `pcb silk-add/set --font-family` | 写入后回读实际字体；modify 静默失败会被识别。 |
| 封装区域 | `footprint.region.create` / `lib footprint region` | 在已可写封装中创建区域并核对 layer、rule、线宽、锁定和 polygon；宿主忽略可选 name 时保留已验证区域并报警，材料差异或保存失败才回滚。系统库封装无损复制到**当前工程库**后再写 region 的组合路径在 EasyEDA 4.1.60 两次现场调用均失败，当前标 `unsupported`；个人库试验不能外推为当前工程 U3 已完成。 |
| 模块候选布局 | `pcb layout-plan --from --board --module --candidates --out` | footprint anchor 为写坐标；bbox/pads/板框中心线用于变换、避让与事实测量；报告最小间隙及对应对象对，输出目录整体替换，候选绑定原始输入 SHA256。 |
| 二层布局/布线协同 | `pcb layout solve/check/render` | 纯离线读取 fresh `pcb dump --include-copper`；完整移动组与内部铜刚体投影，多网共同路径、路由反馈让位、共享状态预算和独立复验；输出基线/失败尝试/最终候选的整板与局部 SVG。四层联合求解 planned。 |
| 铜完整快照 | `pcb dump --include-copper` | 串行采集 tracks/arcs、vias、pour 边界、实际 poured fills、regions、静态 fills；每类保留 available/unknown，并生成忽略采集时间、项目显示标签和哈希自身的语义 SHA256。 |
| 实际铺铜读取 | `pcb.poured.list` / `pcb poured-list` | 将宿主材料化 fill 的 0.1mil 坐标/线宽归一化为 mil，保留 nested contours 与 degree ARC sweep；`fill:false` 标为带线宽的 thermal-spoke path。只有完整 inventory 的真实 `[]` 是 known-empty，fill/boundary/net/layer/polygon 任一缺测均为 unknown/error。 |
| 带铜模块验收 | `pcb module-check --candidate --before --after --journal` | 核对 fresh 语义基线、OSC track/arc replacement 精确全集、严格 PID journal、成员与 pad 几何、ordered path、含实际 signal-main stroke 的双层 no-pours、逐段 guard 到 anchor 的列出铜路径、逐个 GND via 的双层实际铜同岛连接及所有非目标差异；`affectedBaselinePours` 只放行影响包络内的声明铺铜重算，缺测返回 incomplete/fail。 |

执行许可已从版本、workflow stage、布局 tier 和 stale-read 状态中移除。旧接口继续返回
`compatibilityOnly` 或 `staleRisk` 供诊断；权威批次使用 save → reload → readback。

## 已知不支持：Altium Designer 工程自动导入

- `.SchDoc` / `.PcbDoc` 当前没有 typed action 或 CLI 导入入口；使用 EasyEDA Pro 的
  **文件 → 导入 → Altium Designer** GUI，再由现有读取、检查与保存命令核验结果。
- 官方 beta `sys_FileManager.importProjectByProjectFile` 虽声明支持 AD，但 issue #203
  在桌面端 3.2.149 本地工作区实测为 `undefined` 且没有文档/工程副作用，不能视为可用。
- `sys_FormatConversion` 只覆盖 Altium 库文件，不覆盖工程文档。未来封装必须把无返回、
  无变化和部分导入明确判失败，并核对连接图、板框、层叠和机械层。
- 面向 Agent 的操作与验收说明见
  [`project-import.md`](../.agents/skills/easyeda-agent/references/project-import.md)。

## Completed

### Absorbed from the official extension ecosystem (A1/A2/A3/A5)

Shipped from the [`ecosystem-survey.md`](ecosystem-survey.md) absorb-list — features
mined from open-source `eext-*` extensions' real `eda.*` usage:

| Action | CLI | What | absorb # |
|---|---|---|---|
| `schematic.library.get_by_lcsc` | `lib by-lcsc --lcsc C…` | Deterministically resolve LCSC C-numbers → `{libraryUuid, uuid}` (no free-text rank); `notFound` for misses. Companion script `scripts/parts-add.py` writes results back into `standard-parts.json`. | A1 |
| offline preflight | `lib device validate --spec …` | Validate datasheet identity/page evidence, land-pattern provenance, geometry, duplicate numbers, and exact Symbol-pin ↔ Footprint-pad mapping before `lib device build` performs any write. | library-builder |
| `pcb.line.create` | `pcb track` | Create a copper track (导线) on a layer between two points (mil, y-up). **Mutates.** | A2 |
| `pcb.via.create` | `pcb via` | Place a via (过孔) with hole + outer diameter. **Mutates.** | A2 |
| `pcb.report` | `pcb report` | Read-only design report: per-net length, net-class totals, differential-pair skew, equal-length spread. | A3 |
| `pcb.drc.rules` | `pcb drc-rules` | Read the DRC rule configuration without running a check. | A5 |
| `pcb.save` | `pcb save` | Save the active PCB to disk; also the action the daemon's debounced autosave now fires for PCB windows. | gap fix |

All five absorb-items are **live-verified on a real board (PCB1, connector 0.5.15):**
A1 resolved C6186→AMS1117-3.3 identity, A5 returned the full rule config, A3 reported
4 nets with length/net-class/diff/equal-length, A2 created a GND track (net length read
back 0→500 — bound to the right net), and `pcb drc` + save passed. The live run surfaced
a gap — **no `pcb.save` + PCB not covered by autosave** — now fixed (`pcb.save` action +
`saveActionForDocType` maps `pcb`→`pcb.save`, so PCB edits autosave like schematic edits).
No one-call PCB autorouter exists on this build (A4 blocked — see survey §6).

### Read context and typed document navigation (8 actions)

| Action | What |
|---|---|
| `system.health` | Daemon + connector availability, connected/active windows. Daemon-answered. |
| `project.current` | Current project uuid / name / team context. |
| `document.current` | Active editor document + schematic page context. |
| `document.close` | Close the fresh UUID + tabId matched active document through the official editor API and return its pre-close split ID for typed reload; mismatch fails before close. |
| `schematic.pages.list` | Schematic documents and pages in the project. |
| `schematic.page.open` | Open/activate a page by uuid. |
| `schematic.components.list` | Read components with optional pins/bbox/wires. Replay requires `includeDeviceIdentity:true` to resolve real 32-character library UUIDs; the default placed-instance UUID is not a library identity. `allPages:true, tagPages:true` loads all existing pages and rejects incomplete inventories or a failed return to the original page. |
| `schematic.text.list` | Read-only list of ALL text primitives on the ACTIVE page (`primitiveId/content/x/y/rotation/fontSize/color/…`) — pairs with `schematic.primitives.delete` to clean orphaned zone-draw labels without `debug.exec_js` (#156). Page-lazy-load law: active page only; sweep pages via `--page`/`doc switch`. CLI `sch text-list`. |
| `pcb.component.attrs_backfill` | Backfill PCB components' EMPTY otherProperty values from their DEVICE-LIBRARY records (resolved per-part by LCSC C-number via `getByLcscIds`). Repairs the platform's sch→PCB import (creates attribute KEYS with empty VALUES — blanking the 器件标准化 panel's PCB columns); the schematic instance is NOT a usable source (its values are empty after save/reload too). Empty-only merge by default (`--overwrite` forces); parts without a C-number skipped + reported. PROJECTED-STATE keys (`Designator`/`Name`/`Manufacturer*`/`Supplier*`/`Add into BOM`/`Unique ID`) are excluded from the merge — the library's own `Designator:"C?"` placeholder used to get merged in and the platform synced it into the primitive designator, wiping 166/166 on a real board (root-caused + fixed 2026-08-09). CLI `pcb sync-attrs`; auto-runs after `pcb import-changes` (`--no-sync-attrs` opts out). Mutates. |
| `pcb sync-designators` (CLI orchestration; no new action) | Repair placeholder designators (`U?`/`C?`) from the schematic, matched by `uniqueId` (minted by the platform at first sch→PCB import; ONE namespace across both documents — primitiveId is per-document). Placeholder-only (hand-set designators never overwritten); every write read-back-verified; `pcb.save` checkpoint after repair; schematic-side placeholders classified separately ("annotate the schematic first"). `--dry-run`/`--json`, non-zero exit on any failed write. Auto-runs rear-guard after `pcb import-changes` (after attrs, `--no-sync-designators` opts out). Mutates. |
| `schematic.select` | Select primitives by id, return the active selection. |

**Discover + switch/open loop (CLI, no new actions):** `easyeda doc ls [--project X]`
aggregates `schematic.pages.list` + `pcb.documents.list` + `document.current`
into one ★-active document list; `easyeda doc switch <name|uuid> [--project X]`
(or `easyeda doc open <name|uuid>` for more intuitive naming) resolves a page/PCB name
→ `document.open` → readback (cross-type PCB↔schematic). With 2+ windows connected,
`--project`/`--window` is required.

**Live window context:** each window's context in `system.health` stays fresh two
ways — the daemon refreshes it from every action response, and the connector
(≥ v0.5.7) pushes it on each heartbeat (~3s) when the active document changed, so
health tracks a UI tab-switch with no command run. `health` also reports
`connectorVersionOk` to flag a stale connector left in an open window.

### View / navigation (4 actions, `document` domain — schematic + PCB)

Editor canvas view shortcuts via `eda.dmt_EditorControl.*`; act on the focused
canvas, so they apply to whichever document (schematic or PCB) is active. CLI: `easyeda view …`.

| Action | What |
|---|---|
| `view.fit` | Zoom to fit all primitives — 适应全部, the `K` shortcut (`zoomToAllPrimitives`). |
| `view.fit_selection` | Zoom to fit the current selection — 适应选中 (`zoomToSelectedPrimitives`). |
| `view.zoom` | Pan/zoom to a center `x/y` and/or `scale` percent (`zoomTo`); omitted fields keep current. |
| `view.region` | Zoom to a rectangular region `left/right/top/bottom` (`zoomToRegion`). |

### Sheet / page management + 明细表 (6 actions, `schematic` domain)

Map to `eda.dmt_Schematic.*`. **No set-paper-size (A4/A3) API exists** in EasyEDA
Pro; the title block (明细表) is the editable "图纸" surface. CLI: `easyeda sch …`.

| Action | What |
|---|---|
| `schematic.titleblock.get` | Read a page's 明细表 — `showTitleBlock` + per-field `titleBlockData` (read first to learn the field keys). |
| `schematic.titleblock.modify` | Toggle title-block visibility and/or patch fields; only the passed items change, unknown keys ignored. Mutates. |
| `schematic.page.create` | Create a new page under a schematic document. Mutates. |
| `schematic.page.rename` | Rename a page. Mutates. |
| `schematic.page.delete` | Delete a page (confirmation-gated, no undo). Mutates. |
| `schematic.rename` | Rename a schematic document (whole sheet; may also rename a linked reuse-module symbol + PCB). Mutates. |

### Board / 组合 — schematic↔PCB binding (7 actions, `board` domain)

A **Board groups one schematic + one PCB** (识别符是 name, not uuid) — the structural
unit that keeps the two together and that `import_changes` follows. Project tree:
Workspace → Project → **Board** → schematic + PCB. Map to `eda.dmt_Board.*`. CLI: `easyeda board …`.

| Action | What |
|---|---|
| `board.list` | All boards in the project — name + bound schematic + pcb. |
| `board.current` | The current board (its bound schematic + PCB). |
| `board.create` | Bind a schematic and/or PCB into a new board. Fixes a floating PCB before `import_changes`. Mutates. |
| `board.rename` | Rename a board by its current name. Mutates. |
| `board.copy` | Duplicate a board (schematic + PCB). Mutates. |
| `board.delete` | Delete a board by name (confirmation-gated, no undo). Mutates. |
| `board.rebind` | Repair a stale/orphaned board binding after a PCB rebuild: delete the board (by `--name`, else current) and re-create it bound to `--schematic` (+ `--pcb`), rolling back on failure. Clears the false DRC Netlist Error left when the binding points at a deleted schematic UUID. `--force` moves a schematic already bound elsewhere. Mutates. |

### Draw / edit (11 actions, all mutate)

| Action | What |
|---|---|
| `schematic.component.place` | Place a device by library identity (`libraryUuid` + `uuid`) at `x,y` with optional rotation/mirror/BOM flags. |
| `schematic.rebind.footprint` | Swap a placed component's footprint with a **candidate-first transaction**: update and freshly verify the device association, create/read back the replacement while the original still exists, then delete the original and restore/verify stable `uniqueId`, pose, BOM and supplier properties. Missing stable identity is refused before mutation. Failures report phase, both instance presences and verified rollback facts; timeout forbids blind retry and PCB `import-changes` until a fresh read reconciles identity. System-library devices use the personal-library clone fallback. A successful replacement still mints a NEW primitiveId, so run `sch drc`/`sch check`. Mutates. |
| `schematic.rebind.symbol` | Swap a placed component's symbol via the same candidate-first transaction, association/identity readback and clone fallback. Same rollback and timeout caveats as `rebind.footprint`. Mutates. |
| `schematic.component.replace` | Replace a placed component with a **different** device (换型号 — the API equivalent of the 器件标准化 panel's 使用推荐器件, which itself has no extension API). No rebind-device primitive exists, so: capture state + pin table → delete → create the new device at the same pose → restore designator + uniqueId (kept so sch→PCB `import-changes` UPDATEs instead of delete+add). Part-identity fields (name/manufacturer/supplier/LCSC) deliberately follow the NEW device; `--keep-properties` also carries old custom attrs. Target: `--lcsc` (unique) / `--device-uuid`+`--device-lib` / `--query` (unique). Rolls back to the original device (full identity) on failure after delete. Returns a `pinDiff` (removed/added/moved by pinNumber at identical pose) — non-empty ⇒ re-wire, then `sch drc`/`sch check`. Mutates. |
| `schematic.component.modify` | Patch position, designator, name, BOM flags, or custom properties (components only — not flags). |
| `schematic.component.delete` | Delete component primitives (confirmation-gated). **Only removes components** — wires/buses/graphics survive; use `schematic.page.clear` for a full page reset. |
| `schematic.primitives.delete` | Delete primitives of **any** type by id (components, flags, wires, buses, graphics) — routes each id to its owning class. Omit ids to delete the current selection (select-all → delete). Confirmation-gated, no undo. |
| `schematic.page.clear` | Clear the **active page**: delete every page-level primitive (components, net flags/ports/labels, wires, buses, graphics), optionally keeping the sheet/title block (`preserveSheet`, default true). `dryRun` reports per-type counts without deleting. Returns `{deleted:{...}, total, deletedIds}`. Confirmation-gated, no undo. |
| `schematic.wire.create` | Create a wire polyline (optional net/color/width/lineType). |
| `schematic.netflag.create` | Power / ground / analog-ground / protective-ground / net-port (IN/OUT/BI) / short-circuit flag. |
| `schematic.power.connect_pin` | Composite: draw a stub wire out of a pin **and** place a netflag/netport at its far end in one call. Structurally prevents the "netflag overlaps pin" DRC fatal and orients the flag body outward along the stub (顺着导线方向). Default direction inferred from kind, default offset 30u. |
| `schematic.pin.set_no_connect` | Mark (or clear) a pin's no-connect flag (非连接标识, the X marker) so DRC stops reporting intentionally-floating pins as "un-connected pin". Targets pins by designator + pin number(s); `noConnected=false` clears. A pin state, not a standalone primitive: the connector resolves the live component, uses `component.getAllPins()`, commits each `pin.setState_NoConnected(...)` with `pin.done()`, then verifies by fresh readback. |

### Library search (1 action)

| Action | What |
|---|---|
| `schematic.library.search` | Free-text search of the EasyEDA device library (`eda.lib_Device.search`); returns `libraryUuid` + `uuid` ready for `schematic.component.place`, plus name/value/footprint/lcsc/description. Replaces ad-hoc `debug.exec_js` lookups. **See the search caveat under Roadmap.** |

### Verify (3 actions)

| Action | What |
|---|---|
| `schematic.drc.check` | Run the official schematic DRC SDK gate; current EasyEDA builds may return only boolean/aggregate detail. Use `schematic.check` for reconstructed per-item warnings. |
| `schematic.check` | Reconstructed schematic design check from primitives + official netlist JSON: net-marker mismatch, multi-net wire, floating pins, wire crossings, and wire-over-pin hazards. |
| `sch destagger` (CLI, Go-side planner) | **Fix side of `marker-overlap`** (issue #171; detection landed in #148). Plans a safe batch de-stagger: for every marker caught in a visual overlap, pick a new stub direction + length and **move the stub wire with it** (`disconnect` → `connect_pin`), leaving the host (pin-side) endpoint untouched so the electrical topology cannot change. Only markers sitting on a **two-point straight short stub** are moved — polyline/trunk/diagonal carriers are skipped with a reason (`not-a-stub`/`stub-too-long`/`diagonal-stub`), and a boxed-in marker is left alone (`no-free-slot`) rather than forced into another collision. Stub-length candidates are **measured** (they step by the flag's `flagTextBand` size) and snapped to the connector's 5-unit `SCH_GRID`; direction preference follows the 电上地下 convention and rotation comes from the same `flagBodyRotation` truth table the `reversed-net-flag` rule checks against. `--apply` re-runs the real `sch check` after each round and **rolls the whole batch back** if any electrical counter (floating-pin / dangling-wire / net-marker-mismatch / multi-net-wire / …) got worse. Default dry-run; `--json`; `--max-rounds`. Single page (stub geometry is active-page only). |
| `schematic.bridgeCheck` | **Tree-granularity** net-vs-copper consistency check (`sch bridge-check`). Groups every page wire into trees by shared vertices (union-find), then aggregates the netflag/netport net names anchored on each tree: `len(set(nets)) > 1` → **BRIDGE** (共线合并短路, real short, ERROR/gate); empty nets + touches a pin → **ORPHAN** (孤儿桩, WARN). Catches the盲区 `schematic.check`'s per-single-wire `multi-net-wire` rule under-reports when one merge spans several wires. Reports wire ids / flag ids / touched `designator:pin` per problem tree. Read-only. |
| `schematic.snapshot` | Capture the current rendered area as a PNG artifact. |

### Export (2 actions)

| Action | What |
|---|---|
| `schematic.export.netlist` | Export the netlist as an artifact. |
| `schematic.export.bom` | Export BOM as csv or xlsx artifact. |

### Save (1 action)

| Action | What |
|---|---|
| `schematic.save` | Save the active schematic document. |

### Escape hatch (1 action)

| Action | What |
|---|---|
| `debug.exec_js` | Run raw `eda.*` JavaScript in the connector. Confirmation-gated; for operations without a typed action yet. Repeated snippets should graduate to typed actions. |

### Tooling layer

- **Go-side CLI planners (pure geometry over real bboxes)** — deterministic,
  unit-testable analysis/placement that runs in the daemon's Go process on a
  single `schematic.components.list` pull, no per-step screenshots:
  - **`easyeda sch gate`** — **the S5 verification gate, one command**: runs
    `layout-lint → check → bridge-check → drc` in a fixed order and returns one
    report. Motivated by the surface-convergence audit
    ([2026-08 审计与验证记录](reviews/2026-08-sch-surface-audit.md)):
    with four separate checkers, *which ones, in what order, whose exit code
    counts* was re-decided every run with no data to decide it on — the audit log
    shows agents answering it four different ways for the same failure. Order,
    blocking rules and exit code now live in code. Blocking: layout-lint
    overlap/pin-coincidence · check fatal+error · bridge-check `wire-bridge` ·
    drc fatal (tight spacing, orphan stubs and non-fatal DRC are advisory;
    `--strict` promotes them). **Three-state verdict** — `pass` / `fail` (the
    board has blocking problems) / **`blocked`** (a checker could not RUN, so the
    schematic was never judged; remaining stages are skipped instead of running
    into the same wall, and the report points at `health`/`doc switch` rather
    than at the circuit). Each failing stage carries its prescribed next step.
    `--json` nests every stage's full native report under `stages[].detail` (a
    superset of the four single commands' JSON); `--only`/`--skip` select a
    subset and reject misspelled stage names rather than silently gating on
    fewer checks; `--fail-fast`. The four single commands stay for spot checks.
  - **`easyeda sch layout-lint`** — pairwise bbox overlap/pin coincidence
    (ERROR), tight spacing/off-grid/zone violation/**out-of-sheet** (WARN), with
    corrected mm↔0.01-inch conversion and schema-v2 unit metadata. `out-of-sheet`
    (issue #180) catches parts whose **bbox** (not anchor — a body can stick out
    while the anchor sits inside) leaves the sheet frame inset by 12 units:
    nothing caught this before, because an off-page part still wires up and still
    reconciles against the netlist — it simply does not print. `sheetCheckStatus`
    mirrors the zone check's honest disclosure (`unavailable` + reason when the
    sheet bbox is unreadable or under `--all-pages`). `--strict` also fails
    warnings, missing/malformed/unproven anchor/bbox/pin geometry, and an
    unavailable configured zone **or sheet** check, so `0 overlap` can no longer
    stand in for a proven layout. Strict proof is active-page/real-part only and rejects
    `--all-pages` or `--include-non-parts`.
  - **`easyeda sch autoconnect`** — pin-aware connect planner: score every
    (direction × offset) candidate against real geometry, pick the lowest cost,
    delegate the mutation to `connect_pin` (issue #24).
  - **`easyeda sch autolayout`** — legacy module-aware **placement** planner (issue #25),
    not the current data-driven generation workflow; local maintenance must reconcile source data:
    reads a `--spec` (page, sheet, modules with zone/core/parts, rules),
    partitions the canvas into named zones (`left-top`/`center`/`right`/…), places
    each module's core IC near its zone center, fans peripherals around it with
    collision retry, and preserves each core pin's fanout channel + the A4
    title-block keep-out. Same pure-scorer style as autoconnect: identical spec +
    input → identical coordinates that pass `layout-lint`. `--dry-run` plans
    without mutating; template `--apply` pins `--doc`/`spec.page`, refuses any
    existing wire/bus/net marker（含 `short_symbol`）both before planning and immediately before
    mutation, rejects `--all-pages`, and requires proven bbox/pin geometry. It
    validates every moved anchor, grid/spacing/overlap/pin/title-block rule by
    readback and proves `saved:true`. Any failure triggers reverse-order restoration, verified
    by another anchor readback, then saves the rollback.
    There is no template force/rewire override because v1 only **moves
    already-placed parts** (it neither carries wires nor creates missing parts).
- **`.agents/skills/easyeda-agent/scripts`** — a data-only schematic checker (no screenshots): one
  `getAll` + `wire.getAll` pull returns the full layout, then a geometry/union-find
  pass finds connectivity and orientation problems with exact coordinates (13
  checks: `flag_on_pin`, `dangling_wire`, `floating_pin`, `orientation`,
  `bbox_overlap`, `dup_designator`, … ). Ships with:
  - a **rule-trust harness** (`make lint-test`) — orientation-consistency guard
    (`orientation.json` is the single source of truth for the body-rotation table,
    derived identically by the linter's `orient.py` and the connector's
    `connect_pin`, so they can't drift) + fixture goldens;
  - a **diff baseline** — `lint.sh <project> --save` records a snapshot, later runs
    show only NEW / FIXED / PRE-EXISTING findings plus the changed primitives.
- **🧩 Standard circuit-block library (电路块库) — topology-template capability.** A
  community-built, credited library of KNOWN-GOOD peripheral subcircuits
  (`.agents/skills/easyeda-agent/references/blocks/*.json`, one block per file): CH340 USB-serial, ESP32
  auto-download, button de-bounce, USB-hub, buck… Their internal topology is fixed
  and copy-verbatim; reuse only rebinds the boundary nets (`ports`) and reallocates
  RefDes. It is the **topology tier** above `standard-parts.json` (part tier) and
  below `design-flow.md` (flow tier). Design invariants:
  - **Pins referenced by FUNCTIONAL NAME** (`CH340.TXD`), never pin numbers → reuse
    needs zero pin-renumbering.
  - **`parts` point back into `standard-parts.json`** by role key → BOM/LCSC stays
    single-sourced; `alt[]` gives interchangeable substitutes.
  - **Three knowledge dimensions per block**: parts (with alternatives) +
    `schematic_notes` (wiring gotchas) + `pcb_layout` (structured electrical
    constraints with `severity`, future-feedable to `pcb check`).
  - **Validation gate**: draft topology is still structurally complete and explicitly
    unverified; `internal_nets:"pending"` is no longer an accepted placeholder. Production
    readiness requires the staged evidence described by the contribution contract.
  - **Attribution**: `author`/`contributors` (GitHub @handles, never removed) +
    `added`/`updated` versions — *contribute once, benefit forever*. Contribution
    standard + PR gate: `references/standard-blocks-contributing.md`.
  - **Tooling**: `easyeda blocks ls/search/show` browses the embedded templates;
    `make blocks-audit` checks their real symbol pin references. `sch block-apply`
    is the write path. Public instance-level Lib candidates and compose assets live
    separately under the Skill `library/modules/` and are checked by `make modules-audit`.
- **Connector self-healing reconnect** — the connector port-scans 60832-60841,
  validates a handshake, and reconnects on liveness loss. It **never permanently
  gives up**: after 5 fast retries it drops to a quiet 10s background poll, so a
  daemon started/restarted later auto-reconnects with no manual action. A
  low-volume `log` frame surfaces connection-lifecycle diagnostics in the daemon
  log (`connector LOG: …`).
- **`make eext` release flow** — bumps the PATCH version and builds an importable
  `.eext`. `make eext` keeps the uuid **stable** (update-in-place: uninstall old →
  import); `make eext-fresh` mints a **fresh uuid** (imports as a separate entry,
  no uninstall needed) as the fallback when the installed one won't uninstall.
- **`easyeda update` (alias `upgrade`) — in-place self-update** for the two pieces
  that *can* be updated programmatically: the **CLI binary** (downloads this
  platform's release asset, verifies sha256 against the release `checksums.txt`
  when present, runs the download once to confirm it reports the expected
  version, then swaps it in with a same-dir rename) and the **skill dirs** (same
  machinery as `easyeda skill sync`). The **connector `.eext` is reported, never
  touched** — sideloads have no in-place update, so `update` prints the version
  it found in each open window plus the re-import URL. `--check` is read-only and
  `--check --exit-code` exits **10** unless the installed CLI/Skill and live
  daemon/Connector are all verifiably equal to the exact target Release. This is
  an explicit installation-accounting command; ordinary actions continue and
  report version differences as diagnostics. A **dev build is never overwritten** without `--force`
  (air rebuilds it anyway; silently replacing it would make the dev loop lie).

---

## Verified end-to-end (this session)

The board was drawn **entirely from real LCSC / 立创 library parts** (search →
place by uuid → wire → flag), and lint-clean:

- a minimal **ESP32-S3-WROOM-1** system board.

This proves the library-first workflow (place real parts, then wire) end to end,
not just hand-drawn custom symbols.

---

## Roadmap (NOT yet built)

These are planned and **not implemented** today.

- **🧩 `easyeda sch block apply` — one-shot circuit-block instantiation (phase-2 write path).**
  The block library's read/browse layer ships today (`references/blocks/*.json` +
  `scripts/blocks.py`); the **write path** — materializing a block into the live
  schematic — is the next milestone. Interface designed first (per the CLI-design
  首要准则), implementation to follow:

  ```
  easyeda sch block apply --id block.ch340c_usb_serial \
      --bind TXD=MCU_RX,RXD=MCU_TX,VBUS_5V=5V,GND=GND \
      [--prefix U2,R7,...] [--at X,Y] [--page <uuid>] [--dry-run]
  ```

  Semantics: (1) resolve the block's `parts` → place each role from
  `standard-parts.json` (`schematic.component.place`), allocating fresh RefDes
  (respecting `--prefix`/next-free); (2) wire every `internal_nets` entry with
  real wires (`connect_pin` — honoring the netflag-needs-real-wire rule); (3) for
  each `ports` entry, either bind to the `--bind`-supplied host net or emit the
  `default_net`; (4) refuse to apply a **draft** block (`validated:null` /
  `internal_nets:"pending"`) unless `--force`; (5) `--dry-run` prints the place +
  wire plan (like `sch autolayout --dry-run`) without mutating. This is a typed
  action (mutation) → a Cobra subcommand, not a script. It reuses the existing
  `place` + `connect_pin` engines, so the new logic is just topology expansion +
  RefDes/port binding. Ships the block library from "agent reads & hand-copies" to
  "agent instantiates in one call".
- **器件标准化 / standard parts library** — a curated `.agents/skills/easyeda-agent/references/standard-parts.json`
  mapping category → `{MPN, LCSC C-number, libraryUuid, deviceUuid}` that the
  agent places from **first**, with `schematic.library.search` as the fallback. The
  goal is deterministic, repeatable part choices instead of re-searching every time.
- **优化搜索 / optimized search** — `schematic.library.search` today simply slices
  the **first N** of EasyEDA's raw `lib_Device.search` results. Its action
  description claims a "ranked list", but the implementation does **not** rerank —
  it preserves EasyEDA's native order and truncates. Planned: rerank/filter by
  query relevance, package, JLC-basic-part status, and stock.
- **立创商城比对选型 / LCSC mall comparison selection** — compare candidate parts by
  price / stock / specs to pick the optimal one. Not built.
- **🧲 组内布局计算 / `sch group tidy`(未建,判据已实战校准 2026-08-12).**
  基于持久化编组做**组内自动整理**:`--pattern power-updown` 把组内双电源旗电容
  竖放成"上电源/下地"行业画法(ceshi POWER/MCU 组手工验证,96.0 excellent)。
  实战趟出的判据(实现即规则):① pin 半距按**实测**不按假设(0402/0805 符号
  ±20,且同规格不同库件存在镜像——C1/C6 需 rot90 而 C2/C3/C4 需 rot270,必须
  rot 后重读 pin 实位);② **带信号 netport 的件保持横放**(长条标竖放即折叠,
  按旗类型分流);③ **标签文字朝外**(电源旗文字在符号上方、GND 在下方——当前
  connect 产出的文字"内折"在旗与器件之间,需按 orientation 真值表甩向外侧,用户
  点名);④ mutation 后必须 fresh 读再连(rot 后立即 connect 吃 stale pin 位曾致
  两根 stub 同点起步被平台共线合并成贯穿桥=真短路,gate 兜住;显式坐标绕开)——
  这也指向 **schematic 侧 mutation-后-stale 的统一修复**(zone-draw / group-move
  / connect 三处已实证,应做 settle/double-read 通用防线)。组内 layout-lint 兜底
  不重叠;组间由分区框隔离 ⇒ 全局无重叠。
- **🔖 接插件逐脚丝印 / connector per-pin silk — `easyeda pcb silk-pins`(P9,未建).**
  端子 / 排针 / 接插件应**逐脚自动标注**电气特性,让用户拿到板一眼知每脚是什么(电源/地/TX/RX…)
  以辅助接线。今天**没有 CLI 自动做**——手工丝印踩过坑:把多脚写成一整行长句
  (`LCD 1:GND 2:3V3…`)、不与焊盘逐一对齐、长句远离焊盘只能读不能辅助接线、字号密度挤器件。
  设计标准(实现即校验门):① 接口名只留短标题(`LCD`/`PROG`/`MIC`…);② **每脚单独短标签、严格按焊盘物理顺序**;
  ③ 优先用**网络简称**(`G`/`3V3`/`5V`/`TX`/`RX`/`SCL`/`SDA`/`RST`/`BL`,从连到该脚的网名取);
  ④ 不写脚号冒号(除非编号有装配意义);⑤ 横向排针→横向逐脚、纵向→纵向,顺序与实物观察方向一致;
  ⑥ 标签对准各自焊盘、在器件外壳遮挡区之外;⑦ 普通器件只留位号;⑧ 不压焊盘/器件/出框、装配后可见(铁律 11)。
  **实现落点**:新子命令 `easyeda pcb silk-pins`(或扩展 `pcb silk`)从 netlist 取每脚网名简称 + 焊盘坐标/排布方向逐脚落字,
  复用块的 `silk` map(块数据已有逐脚标注,如 LED 阴极 K)。**根本教训**:校验门要**同时查几何(没压焊盘)+ 语义
  (标签数=引脚数、逐脚对齐、无长句)**——上一版只验了几何、漏了语义可用性。归属 design-flow **P9**。
- **✅ PCB 布局智能补完 — `place-constrained` 4 真缺陷全部 DONE(2026-07-11).** 复评官方
  「PCB自动化工具」v2.5.1 确认其「模块化布局」是 netlist 连通性聚类、解决不了角色感知 floorplan
  的 5 条痛点(板框/类型优先级/朝向/板边距/天线)——都是我们自己代码补的。ceshi 真机逐条验证:
  1. ✅ **`classifyCP` CONSUME 块数据(方案 A)** —— 位号前缀查块 `placement`,regex 降级 fallback;
     显式 `anchor` 字段治过度锚定;死的 role-id `ByDevice` 移除(`697efc2`,issue #95)。**附带**:
     分类改用 `manufacturerId` 而非 `"={Manufacturer Part}"` 模板 → U1 WROOM `main`→`edge`(`81576fb`)。
  2. ✅ **planner 读真板框** —— `outline-fit` 后 `pcb.outline.get` 接进 planner;`boardEdges` 上报
     board-outline vs part-cloud;ceshi J1 吸到真左边 -925(`0d8859e`)。
  3. ✅ **Tier-4 net 聚类** —— 须移位的卫星按共网最近固定脚做种子聚到芯片;良placed 件不动;user-facing
     不被拽走(`a4e9a2d`)。
  4. ✅ **天线 keepout 自动生成** —— 新 `pcb antenna-keepout`(方案 A,块声明 `keepout.end_frac`);
     只盖无焊盘天线端不孤立地脚;MULTI 层全铜层;幂等;`pcb check` 天线检测同步用 manufacturerId。
     ceshi loop 验证 present→0/deleted→1/regen→0(`ce04deb`)。

  仍可从官方插件吸收(未做):**器件布局导出/导入(布局复用)**(块库 PCB 侧对应物)、模块级
  fanout-with-vias、几条 DFM 检查(REF方向/两脚线宽/时钟3W/冗余过孔·线段)。详见 memory
  `pcb-automatic-tool-v251-reeval-and-layout-defects`。

### 验收用例 roadmap (acceptance regressions, NOT yet run end-to-end)

两块 ESP32 最小系统板作为**端到端检查验收基准**——跑通即证明放置→布线→`pcb check`
(含新的丝印正反 / 走线压焊盘 / 非正交走线规则)→DRC 全流程闭环。

- **task #34 — ESP32 **模组**开发板 (module dev board).** 拿原始需求
  [`esp32MiniRequire.md`](../esp32MiniRequire.md)(4 层板 + 点灯 + 5V 供电端子 + 降压 3V3 +
  CH340 USB 烧录 + BOOT/RESET 按键 + 四角 M3 固定,**不含 BOM/网表**)从零跑:agent 自己选型 →
  放置 → 编组 → 布线 → 转 PCB,照 `.agents/skills/easyeda-agent/references/design-flow.md` 的 S0–S6 + P0–P10
  脊柱,**收尾必须 `pcb check` 0 ERROR**(含丝印正反、走线压焊盘)。WROOM-1 模组自带天线/晶振/flash,
  keep-out 只需盖模组天线区。
- **task #35 — ESP32 **芯片级** N8R8 最小系统板 (bare-chip minimal system, no module
  template).** 用裸 **ESP32-S3** 芯片(不是 WROOM 模组),自己搭最小系统:**PCB 板载
  天线 + π 型匹配网络**、**N8R8 = 8MB flash + 8MB PSRAM**、40MHz 晶振、EN/boot straps、
  多路去耦。规格见 [`docs/test-case-esp32-chip-n8r8.md`](test-case-esp32-chip-n8r8.md)。
  这是比模组板更硬的验收:天线 keep-out + 阻抗、晶振布局、flash/PSRAM 高速走线,压满
  `pcb check` 的走线/丝印规则。**先补 `standard-parts.json` 芯片级选型**(ESP32-S3 裸片 /
  flash / PSRAM / 天线器件)再跑。

### LCSC C-number lost on placed parts → fixed by BOM enrichment

A placed component's `getState_SupplierId()` returns `MPN.1` (e.g.
`GRM21BR61H106KE43L.1`), not the LCSC C-number (`C440198`) — confirmed by reading
the exported BOM, whose "Supplier Part" column is the MPN.1. The component can't be
fixed at the source: `setState_SupplierId('C440198')` does **not** persist (the
field is device-bound and reverts on re-pull). So the fix is post-export:
**`.agents/skills/easyeda-agent/scripts/bom-enrich.py`** joins the C-number in by matching each row's Manufacturer
Part against `standard-parts.json` (MPN → LCSC) and rewriting "Supplier Part" to the
real C-number (and filling an empty Value). Verified: 5/5 rows of the ESP32-S3 BOM
enriched to orderable C-numbers; unmatched MPNs are reported as candidates to add to
`standard-parts.json`. Follow-ups: (1) wire the enrichment into the daemon's
`schematic.export.bom` so exports are orderable by default; (2) for non-standard
parts, resolve MPN → C-number via `lib_Device.search` instead of only the curated
list.

---

## Connector quirks (load-bearing)

- **`createNetFlag` / `createNetPort` STORE rotation negated on the 2026-06 build.**
  Despite the earlier "identity" assumption (commit `8aace7e` reverted a negation as
  a misdiagnosis), a live test settled it: `connect_pin(direction=left)` passed `90`,
  the flag stored `270` and rendered pointing **right**. (0/180 up/down are symmetric,
  so only horizontal flags exposed it.) `connect_pin` now **auto-detects** the
  behavior at runtime (`detectRotationNegation` — a one-shot probe flag, re-pulled)
  and compensates, so its output is correct whether the build negates or not. The
  orientation table (`orientation.json`, the **stored-rotation** truth) is still the
  single source, derived in one place and asserted equal between linter and connector
  by `make lint-test`; `calibrate.js` validates it read-only against real flags.
- **Coordinates are y-UP** — `+y` renders **upward**. `connect_pin` honors this:
  `direction: up` increases `y`, `down` decreases it.
- **No programmatic undo** in `eda.*`. `modify` only works on components, not
  flags — to change a flag you delete and recreate it. Pull fresh primitive ids
  right before mutating.
- **Re-importing the `.eext` does NOT reload already-open EasyEDA windows.** An
  open window keeps running the **old** connector code; the stale window then
  fights the freshly-imported one over the daemon socket → instability. **Fully
  quit and relaunch EasyEDA** to load new connector code.
- **`getCurrentRenderedAreaImage` could return a stale cached frame** (it didn't
  follow zoom or reflect just-made edits) — historically a trap for "confirm with
  a screenshot" workflows. Fixed in recent connector versions; still prefer
  data-driven verification (`schematic-lint`, `drc.check`) over screenshots.
</content>
</invoke>
