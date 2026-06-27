# PCB 工艺能力发现（API 探测）— 2026-06-28

> 目的：在做 PCB 布局/工艺自动化前，先摸清 EasyEDA Pro `eda.pcb_*` API 对目标能力的支持度。
> 本轮是**API 表面探测**（枚举方法 + 关键创建器存在性），**未做活板行为测试**（明天在真 PCB 上验证）。
> 探测环境：connector 0.5.14，EasyEDA Pro 3.2.148。

## 能力支持矩阵（你列的清单）

| 能力 | API | 支持度 | 说明 / 下一步 |
|---|---|---|---|
| **板框 布局** | `pcb.outline.set/get/clear`（已有 typed action + CLI）；`pcb_Document.zoomToBoardOutline` | ✅ 已支持 | 闭合多段线，曲线用线段逼近（native arc 当前 build 不提交）。CLI `pcb outline-set/get/clear` 已有。 |
| **布线（手工走线）** | `pcb_PrimitiveLine.create/modify/delete/get/getAll`（铜层上的线=走线） | ✅ API 有 | **缺 CLI 子命令**；需活板验证 create 的层/线宽/网络参数。 |
| **布线（自动布线）** | 类型声明 `pcb_Document.autoRouting(props?)` @alpha；文件式 `importAutoRouteJsonFile` / `importAutoRouteSesFile`；`pcb_ManufactureData.getAutoRouteJsonFile(ForJRouter)` | ⚠️ 受限（类型 vs 实测有出入） | 发布类型**声明了**可直调的 `pcb_Document.autoRouting(props?)` → `{routedNets, totalNets}`（@alpha），**但 2026-06-26 实测运行 build(锁 ^0.2.21)上 `autoRouting`/`autoLayout` 为 `undefined`**——运行 build 落后于发布类型。复测确认前，文件式仍是唯一可靠路径。详见 [`ecosystem-survey.md`](ecosystem-survey.md) §6。 |
| **铺铜** | `pcb_PrimitivePour`（铺铜区定义）+ `pcb_PrimitivePoured`（计算后的铜）+ `pcb_PrimitiveRegion` + `pcb_PrimitiveFill`，均 create/modify/delete/get/getAll | ✅ API 有 | **缺 CLI**；需验证 Pour.create 的多边形/net/层参数，以及 Pour→Poured 的重灌触发方式。 |
| **过孔** | `pcb_PrimitiveVia.create/modify/delete/get/getAll` | ✅ API 有 | **缺 CLI**；需验证孔径/焊盘/网络/盲埋孔参数。 |
| **4 层 / 2 层 设计** | `pcb_Layer.setTheNumberOfCopperLayers` / `getTheNumberOfCopperLayers` / `setPcbType` / `addCustomLayer` / `removeLayer`；物理叠层配置 `get/save/setDefault…PhysicalStackingConfiguration` | ✅ API 有 | **缺 CLI**；需验证 2↔4 层切换是否即时生效、叠层配置如何选。 |
| **泪滴处理** | （全 `eda.*` 命名空间搜索 teardrop/泪滴）**无** | ❌ **不支持** | **公开 API 没有泪滴创建/设置接口**。只能：① UI 手工「一键泪滴」；② 自己用 `pcb_PrimitiveLine`/`Region` 近似画（成本高、不等价于原生泪滴）；③ 向嘉立创反馈缺接口。**列为不支持，待平台或 workaround。** |
| **DRC 检测** | `pcb_Drc.check` + 实时 DRC（start/stop/status）+ 规则配置（net class / 差分对 / 等长组 / pad-pair）一整套 | ✅ 已支持(强) | `pcb drc` CLI 已有；比原理图 DRC 强很多（有逐条明细 + 规则配置）。规则配置面（net class 等）可后续包 CLI。 |

## 额外发现（有用的基础设施）

- **PCB 网络 API 是好的**：`pcb_Net.getAllNets / getNetlist / getAllPrimitivesByNet / getAllNetsName / highlightNet` 都在 —— 跟原理图 `sch_Net.getAllNets`（返回空、getNetlist 超时）形成对比。**布线/铺铜可以可靠地按网络操作。**
- **sch→PCB 桥**：`pcb_Document.importChanges`（已有 `pcb import-changes`）+ `importAutoLayoutJsonFile`（文件式自动布局）。
- **清布线**：`pcb_Document.clearRouting`（一键清所有走线，重布前用）。
- **定位/拾取**：`getPrimitiveAtPoint` / `getPrimitivesInRegion` / `navigateToCoordinates/Region` —— 交互/校验用。
- **制造输出齐全**：`pcb_ManufactureData` 有 Gerber / 钻孔 / 贴片(pick&place) / BOM / 3D / DXF / PDF / IPC-356A / IPC-2581 等几乎全套导出。

## 明天的计划（行为验证 + 落 CLI）

1. **建 PCB 并 import_changes**：从已修好的 ESP32 板（`ceshi`）建 PCB，`pcb import-changes` 同步器件 → 验证器件/网络/ratline 是否到位。
2. **逐项活板验证**（确认 API 真能用、参数形态）：
   - `pcb_Layer.setTheNumberOfCopperLayers(2/4)` 是否即时；
   - `pcb_PrimitiveLine.create` 画一段走线（层/宽/net）；
   - `pcb_PrimitiveVia.create` 放一个过孔；
   - `pcb_PrimitivePour.create` 铺一块铜 + 触发重灌；
   - `pcb drc` 在有走线/铺铜后跑，看逐条明细。
3. **落 CLI 子命令**（`pcb` 组已存在）：`pcb track`(走线) / `pcb via` / `pcb pour` / `pcb layers --set-copper N` 等；沿用「typed action → Cobra 子命令」闭环。
4. **泪滴**：确认无原生 API 后，决定走 UI 手工 还是 近似实现 还是 暂不做。
5. 像原理图那样,给 PCB 也补**几何质量检查**(`pcb check`?:走线/铜与板框/过孔间距等 —— 复用 layout-lint 思路)。

## 结论（一句话）

板框 ✅、过孔 ✅、铺铜 ✅、层叠(2/4) ✅、手工布线 ✅、DRC ✅(强) —— 都**有 API、可落 CLI**(明天验证 + 包命令)。
**自动布线 ⚠️ 仅文件交换式**(需外接布线器)。**泪滴 ❌ 无公开 API**(列为不支持)。

---

## 活板验证结果（2026-06-28，真 PCB「PCB1」/ ceshi，connector 0.5.14）

在已 import 的 ESP32 板（PCB1：2 层、8 器件、board linked）上逐项实测，纠正若干 API-表面推断：

| 能力 | 活板结果 | 实测签名 / 备注 |
|---|---|---|
| **层数 2↔4** | ✅ **可用** | `pcb_Layer.setTheNumberOfCopperLayers(4)` 即时生效，`getTheNumberOfCopperLayers` 回读 4；改回 2 正常。 |
| **手工走线** | ✅ **可用** | 真实签名 **`pcb_PrimitiveLine.create(net, layer, x1, y1, x2, y2, width, …)`**（net 是第 1 参!layer 第 2;width 在坐标之后)。回读 `{net,layer,startX/Y,endX/Y,lineWidth}` 正确。注意 create 很宽容、乱序也"成功"但参数错位——必须回读校验。 |
| **过孔** | ✅ **可用** | 签名 **`pcb_PrimitiveVia.create(net, x, y, holeDiameter, diameter, …)`**;回读 `{net,x,y,holeDiameter,diameter,viaType,...}` 正确。 |
| **PCB DRC** | ✅ **强,有逐条明细** | `pcb drc` 返回**嵌套明细**:`{count, list:[{errorObjType:"SMD Pad", errorType:"Connection Error", explanation:{errData:{net:"+3V3", obj1, obj1Suffix:"(+3V3): C2_2"}, str}, globalIndex}]}`。未布线时 28 条连接错误(ratline 未连),符合预期。**远强于原理图 DRC(仅聚合)**。 |
| **铺铜** | ⚠️ **create 签名未破** | `pcb_PrimitivePour.create`(arity 9)所有猜测(`(net,layer,pts)`/`(layer,net,pts)`/`(net,layer,pts,clear)`/带 name)都报 **「错误:无法创建覆铜边框图元,可能是传入的参数不正确」**。API 在,但参数形态未知 → 需查官方 doc 或抓 UI 调用。**暂列受阻。** |
| **自动布线(直调)** | ❌ **本 build 没有** | `eda.pcb_Document.autoRouting` 在 **3.2.148 实测 undefined**(与文档"@alpha 直调"不符——那可能来自更高版本/API 文档)。本 build **只有文件交换式**(`importAutoRouteJsonFile`/`importAutoRouteSesFile` + `getAutoRouteJsonFileForJRouter`)。 |
| **泪滴** | ❌ **无 API**(同前) | 全命名空间无 teardrop。 |

### 结论修正
- **可立即落 CLI(签名已确认)**:`pcb layers --set-copper N`、`pcb track`(走线)、`pcb via`、`pcb drc`(已有)。
- **受阻待解**:**铺铜**(create 签名)、**自动布线**(本 build 无直调,只能文件式或等平台)、**泪滴**(无 API)。
- **下一步**:铺铜签名——抓 UI「铺铜」实际调用 或 查 eda-api `pcb_PrimitivePour.create` 定义;自动布线——评估文件交换流程是否值得包。
