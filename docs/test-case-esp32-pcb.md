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
- ✅ **P2 布局 v1.1（卫星转向 + 去耦贴最近同网pad，#14，2026-06-30）** 真机验证：
  ① 转向——2 脚卫星按所在边重定向让连接 pad 朝芯片；真机 ceshi 7 件全 `setRot`，
  重拉确认 rotation 真落上去(LED1/C1=180、其余 0，与 plan 一致，**无回显陷阱**)，
  **0 器件重叠**。② 去耦贴最近同网 pad——芯片 GND/VCC 重复多焊盘时取最近的。
  `--no-rotate` 可回退 v1 纯平移。多芯片间距(b)留 #19（ceshi 单芯片验不了，需多芯片 fixture）。
- ✅ **P1 禁止区域 action（#11，2026-06-30）** 已封 + 真机验证：`pcb region create/list/delete`
  （`eda.pcb_PrimitiveRegion.*`，connector 0.5.25）。ruleType 友好名/枚举号，默认硬 keep-out
  `[no-components,no-wires,no-pours]`。真机 ceshi：建天线 keep-out + 命名规则区→list 读回规则正确→
  **铺铜避让验证**（在 U1 GND 焊盘上扣一块 no-pours 区，pour-rebuild 后铜皮在该区缺口、U1 GND
  失连 No-Connection 6→11 = 铺铜确实尊重 keep-out）→delete 还原。**EasyEDA 自家 DRC + 铺铜引擎
  都认这个 region**。小缺口：`regionName` 读回为 null（仅名字，规则/几何正常，不影响 keep-out）。
  region 无 net 参数 = 纯 keep-out；net-bound 填充区域是另一条路（→ #17）。
- ✅ **P3 DSN keepout 注入（#17，2026-06-30）** 已实现 + 真机验证：`pcb export-dsn` 默认把 region
  拼回 DSN（connector 0.5.26）。真机 ceshi：建天线 keep-out（no-wires/no-pours）→ export-dsn
  得 **keepouts=1**、`--raw` 得 0；导出 DSN 内出现 `(keepout "…" (polygon TopLayer 0 …))`，
  坐标落在 `(boundary)` 框内、压在 U1 天线端。**坐标变换实测为纯平移**（1:1 mil 无翻转，
  pad 对应标定 dsn=easyeda+(0,2600)）；注入用 bbox min 标定 → +5mil 系统偏移（=半线宽，
  对带余量 keep-out 可忽略，已记录）。小缺口：keepout 名落到 `region_keepout_N`（regionName
  读回为 null）。Freerouting 真去尊重它的端到端核验属 #5 迷宫档工具链。
- ✅ **净绑定填充区域 action（#17，2026-06-30）** 已封 + 真机验证：`pcb fill create/list/delete`
  （`eda.pcb_PrimitiveFill.*`，connector 0.5.27）。净绑定静态填充铜（3V3/RF-ground/散热/异形 plane），
  区别于 pour（覆铜,绕障重流）与 region（keep-out,无 net）。真机 ceshi：建 3V3 fill→list 读回 net/layer
  正确→drc 无 fatal→delete 归零。小缺口：`fillMode` 读回恒为 solid（创建即时回显输入、fresh getAll
  读 solid——与 rotation/regionName 同类回显陷阱；net+几何正常持久，solid 本就是常用默认，低影响）。
- ✅ **P4 短线自研布线**：`pcb route-short`（daemon 侧启发式·两档策略「启发式档」，2026-06-30）已验证——
  每网 pad MST + 短边走 L 形导线（跳 GND/已布线/跨层/过长）。真机 ceshi（auto-place 后）：14 段、
  5 网布通（EN/IO0/BLINK/LED_A/3V3），**No-Connection 130→106**，track-list=14。
  v1 无避障 → 密集区平行走线撞 Track-Clearance（6 条）；长跳 + GND 留给铺铜/迷宫档。
- ✅ **P4 布线质量 v1.1（线宽按网类 + 拐角风格，2026-06-30）** 真机验证（#16）——`--corner 45`
  画出 6 段对角切角、0 失败；线宽分级生效：3V3(power)=20mil、信号(EN/IO0/BLINK/LED_A)=10mil。
  **可调实证**：默认 20/10mil 在这块极密板 → Safe-Spacing 32 条；`--width 6` 细线 → 15 条
  （胖线在密集区更易撞 = v1 无避障的预期代价，非 bug）。No-Connection 恒 6（3V3 远跳 + U1
  散热焊盘，与线宽无关）。`--corner round` 为弦逼近圆角（本版原生 arc 不提交）。
- 🟡 **P4 迷宫档（长线/拥塞/任意距离）**：走外部 Freerouting（`easyeda-pcb-router` 或标准 CLI）；
  一键 `pcb autoroute` 编排。短线档已被 `route-short` 覆盖，迷宫档补长线 + 避障。
- ✅ **P5 铺铜（GND）**：`pcb pour --net GND --layer 1`（贴板框内缩）真机验证——**No-Connection 106→8**
  （GND 焊盘全连通）。pour↔keepout 避让仍待验（#11）。
- ✅ **P5 铺铜调优（#17，2026-06-30）** 已实现 + 真机验证，两条 daemon 侧命令（复用既有 action，无需重导）：
  - **`pcb pour-fit`**（内缩铺铜）：读板框 bbox → 按 `--inset`(默认20mil)内缩 → 铺 net/layer，`--replace`
    先清同网旧铺铜防叠加。真机：把 3 块叠加 GND 铺铜合并成 1 块干净内缩铺铜（cleared=3, poured=true），
    **无 Board-Outline-to-Copper 违规**。v1 在 bbox 内铺矩形（异形板用 `pcb pour` 画多边形）。
  - **`pcb via-stitch`**（散热孔/GND 缝合）：`--rect` 内按 `--pitch` 铺一网过孔。真机：U1 中心区铺
    16 颗 GND 过孔（0 失败）+ 顶底双层 GND 平面 + pour-rebuild → **No-Connection 4→2**（缝合把原本
    悬空的 GND 通过过孔接到双面平面）。剩 2 条为其它网（3V3 远跳等），Safe-Spacing 4 为 45° 走线间距。
  - 平台小缺口（已知、低影响）：`regionName`/`fillMode` 写入后读回不准（回显陷阱）；DSN 注入 offset
    用 bbox 有 ≤半线宽系统偏移。详见各自验收条目。
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
