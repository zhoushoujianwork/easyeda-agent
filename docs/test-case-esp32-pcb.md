# 固定测试用例 — ESP32-S3 最小系统板 **PCB 侧**

> **PCB 侧的回归基准**，与原理图侧 [`test-case-esp32-blink.md`](test-case-esp32-blink.md)
> 对应。每次做 PCB 端到端测试，都要把这个用例用 PCB 流程脊柱完整跑一遍——
> import → 布局 → **禁止区域(天线/板边)** → export-dsn → 自动布线 → **铺铜** → DRC。
> 这是 agent 画 PCB 能力的「冒烟 + 验收」基准；#5(Freerouting)/#11(keep-out) 任何改动后重跑。

## 为什么是它

承接原理图侧同一块 ESP32-S3 最小系统板（8 件、5 网络）。它足够小、可重复，又同时压到
PCB 的真实难点：**带集成天线的模块(U1)的 keep-out**、板框、布局、自动布线往返、铺铜与
禁止区域/板边的避让、DRC 归零。**小到能反复跑，大到是真约束，不是玩具。**

## 前置条件

- 原理图侧用例已建（8 件齐、连通、DRC 0 fatal）。
- 一个 **Board** 把该原理图与一块 PCB 绑定（`board.create` / `board list` 确认）。
- 测试工程用 `ceshi`（一次性，可清空重来）。

## 跑测流程（PCB 流程脊柱 + 约束）

| 阶段 | 动作 | 关键约束 |
|---|---|---|
| **P0 同步** | `pcb import-changes`（原理图→PCB，= 菜单「更新/转换原理图到PCB」） | 8 件落到 PCB，飞线生成 |
| **P1 板框+禁区** | 板框：客户规格优先，否则贴合器件（`pcb outline-set`）。**禁止区域**：U1 天线下 + 板边 clearance（`pcb region create`，ruleType=禁铜/禁布线/禁过孔 — **action 待补 #11**） | **天线 keep-out 必须就位** |
| **P2 布局** | 器件摆位（人/UI 主导；agent 给粗 seed `pcb arrange` + `move/align/grid-snap`） | 无重叠（DRC Safe Spacing=0）；去耦贴电源脚；天线区净空 |
| **P3 导出 DSN** | `pcb export-dsn` | ⚠️ **导出的 DSN 必须含 keepout**（天线/板边）——否则布线器会在天线下走线 |
| **P4 自动布线** | 外部 Freerouting 跑 DSN → SES（`pcb autoroute` 编排，或手动）→ `pcb import-autoroute route.ses` | 布线避开所有 keepout |
| **P5 铺铜** | `pcb pour`（GND 灌铜） | **避让禁止区域 + 板边间距**；`rebuildCopperRegion` 重灌 |
| **P6 校验门** | `pcb drc` | 0 fatal；无 Connection Error（全布通）；keep-out 被尊重 |

## 验收标准（全过才算通过）

- [ ] **P0** 8 件全部从原理图同步到 PCB（`pcb list` = 8，位号齐）
- [ ] **P1** 板框贴合（非默认巨框）；**U1 天线 keep-out 区域存在**；板边 clearance 禁区存在
- [ ] **P2** 布局无重叠（`pcb drc` 中 Safe Spacing/Clearance 误差 = 0）；天线区无器件/铜
- [ ] **P3** `pcb export-dsn` 产出 DSN，且 **keepout 条目 > 0**（天线/板边都进了 DSN）
- [ ] **P4** 5 个网络全部布通（`pcb drc` 中 **Connection Error = 0 / No Connection = 0**）；无走线穿越天线 keep-out
- [ ] **P5** GND 铺铜完成，且**避让天线禁区 + 板边**（铺铜不进 keep-out）
- [ ] **P6** `pcb drc` → **0 fatal**；keep-out / clearance / netlist 全过
- [ ] 跑测在干净工程上做；**测完清理还原**（除非要留存复核）

## 当前状态（2026-06-29）

实事求是记录哪些已通、哪些是缺口/手动：

- ✅ **P0 import-changes** 已验证（菜单「更新原理图到PCB」= `pcb.import_changes`，真机跑通）。
- ✅ **P3 export-dsn** 已验证（导出真 Specctra DSN）。
- ✅ **P4 写回原语**：`pcb.import_autoroute`（`importAutoRouteSesFile` @beta）+ `snapshot` 已封（connector 0.5.24，Phase A）。
- ✅ **P2 布局**：`pcb auto-place`（daemon 侧模块感知布局，2026-06-29）已验证——主芯片锚定，
  卫星(电容/电阻/LED)被拉到所连芯片 pad 那条边、按边打包。真机 ceshi：7 件全部贴到正确边/网、
  **0 器件重叠**、快照肉眼确认（去耦贴电源脚、LED1 链在 R3 旁）。`pcb arrange`(粗聚类种子)保留。
  v1 仅平移不转向；多芯片间距、转向(pad 朝芯片)为后续增量。
- 🔴 **P1/P3 keep-out（#11）**：禁止区域 action **未封**；且实测 **当前 DSN keepout = 0** —— 天线 keep-out 没进 DSN，**P4 布线会在天线下走线**。这是 #5 产出「能用的板」的**前提红灯**，必须先解决。
- ✅ **P4 短线自研布线**：`pcb route-short`（daemon 侧启发式·两档策略「启发式档」，2026-06-30）已验证——
  每网 pad MST + 短边走 L 形导线（跳 GND/已布线/跨层/过长）。真机 ceshi（auto-place 后）：14 段、
  5 网布通（EN/IO0/BLINK/LED_A/3V3），**No-Connection 130→106**，track-list=14。
  v1 无避障 → 密集区平行走线撞 Track-Clearance（6 条）；长跳 + GND 留给铺铜/迷宫档。
- 🟡 **P4 迷宫档（长线/拥塞/任意距离）**：走外部 Freerouting（`easyeda-pcb-router` 或标准 CLI）；
  一键 `pcb autoroute` 编排。短线档已被 `route-short` 覆盖，迷宫档补长线 + 避障。
- ✅ **P5 铺铜（GND）**：`pcb pour --net GND --layer 1`（贴板框内缩）真机验证——**No-Connection 106→8**
  （GND 焊盘全连通）。pour↔keepout 避让仍待验（#11）。残留可调项：①铺铜内缩需更大（撞 1 条
  Board-Outline-to-Copper）；②U1 中心散热焊盘未接铜皮（需散热孔/过孔）。
- 🎯 **端到端管线跑通（2026-06-30）**：`import-changes → auto-place → route-short → pour(GND) → drc`，
  ceshi ESP32 真机 **No-Connection 130→8**（剩 1 条 3V3 远跳留迷宫档 + 4 散热焊盘 + 间距）。
  纯 daemon 侧自研启发式，不依赖 @alpha / 外部引擎。

## 备注

- 关联任务：#5（Freerouting 往返主链）、#11（禁止区域/天线/铺铜约束 = 本用例 P1/P3/P5 的能力前提）。
- `ceshi` 一次性，可清空（见项目 memory）。
- 真机判据优先 `pcb list`/`pcb drc`/DSN 内容，`pcb snapshot` 仅供肉眼看布局（stale frame 坑）。

## 2026-06-29 调查：keep-out / DSN 的硬发现（#11）

用 `debug.exec_js` 实测（无重导）：

1. **ESP32-S3-WROOM-1 模块封装不带天线 keep-out**——`pcb_PrimitiveRegion.getAll()` 初始为 0。
   → P1 的天线 keep-out **必须我们自己建**。
2. **能建**：`eda.pcb_PrimitiveRegion.create(layer, poly, ruleType[])` 可用。
   `ruleType`：`NO_COMPONENTS=2 / NO_WIRES=5 / NO_FILLS=6 / NO_POURS=7 / NO_INNER=8 / FOLLOW_REGION_RULE=9`。
   天线 keep-out 用 `[2,5,7]`（禁器件/禁布线/禁覆铜）。`poly` 用 `pcb_MathPolygon.createPolygon([x0,y0,'L',x1,y1,…,x0,y0])`。
3. **🔴 红灯坐实**：建了禁止区域后 `getDsnFile` 导出的 DSN **仍 0 keepout**——
   **`getDsnFile` 丢弃禁止区域**（`(structure)` 段只有 boundary + clear/width rule + layer）。
   → 带天线 keepout 的板，DSN 无 keepout → Freerouting **会在天线下走线 → 报废**。

### 两条修法（P3 解锁）
- **(A) 我们注入**：`pcb export-dsn` 后读 `pcb_PrimitiveRegion.getAll()`，把 Specctra
  `(keepout (polygon …))` 注入 DSN 的 `(structure)` 段，再交布线器。不等官方。
- **(B) 提 issue**：让官方修 `getDsnFile` 导出时带上 keep-out（同 issue #28 模式）。

> 小 gap：`pcb delete` 只删器件不删 region（region 用 `pcb_PrimitiveRegion.delete`）；
> 禁止区域 typed action（`pcb.region.create/list/delete`）尚未封装。
