# 官方扩展市场覆盖度报告 — 2026-07-01

> 方法:workflow fan-out 18 个官方 EasyEDA/JLC PCB 自动化扩展(PCB自动化工具 + 17 个
> eext-*),每个抓仓库/市场页 + 对照 easyeda-agent 当前能力评「full/partial/none/wall」,
> 再合成。生成脚本见 workflows/,原始逐项评估见本仓 issue/PR 讨论。
> 这是 [`ecosystem-survey.md`](ecosystem-survey.md) 的 2026-07-01 覆盖度快照(survey 本身
> 是更早的 API 盲区调研)。

# EasyEDA Pro 市场插件覆盖度报告 (easyeda-agent)

## 1. 一句话结论

在 18 个官方/市场 PCB 自动化插件里，easyeda-agent 已 **full/partial 覆盖 8 个 (2 full + 6 partial)**，且覆盖的正是设计主干——放置→布线→自动布局→短线布线→铺铜→netlist/BOM→DRC 重构→外部布线桥 (Freerouting/DSN)——以及底层传输层 (`run-api-gateway` 就是我们连接器架构的「撞型」)。**真正的空白集中在主干之外的四类**:(a) **建库/建符号建封装** authoring 三件套 (我们只会放现成 LCSC 件,零 authoring);(b) **专用铺铜/图形** (net-less 均衡铺铜/thieving、丝印挖孔填充、QR/logo/轮廓导入);(c) **高级布线/信号完整性** (差分对、等长绕线、fanout、线圈、net-length/timing 分析);(d) **3D/STEP 导出 与 BOM 版本 diff**。关键校正:调研中几乎所有「平台墙」都被这些插件**证伪**——diff-pair/fanout/length-match/net-length/teardrop 都可通过「外部算几何 + 写 raw primitive」达成,墙只在交互式菜单 API,不在结果。

## 2. 覆盖度总表 (按 category)

| Extension | 做什么 | 覆盖 | 备注 |
|---|---|---|---|
| **dfm-check** | | | |
| PCB自动化工具 (PCB Automatic Tool) | 模块化自动布局+短线布线+fanout+局部铺铜+~10 条 DFM 审查 | 🟡partial | 布局/短线/铺铜三腿=我们的 `auto-place`/`route-short`/`pour`;它本就是我们 survey §8.5 的可行性证据。缺:~6 条 DFM 检查 + fanout-with-vias |
| **analysis-report** | | | |
| eext-export-design-report | 只读 PCB 统计报表 (net 长度/net-class/差分/等长/pad 坐标) CSV | 🟡partial | `pcb report` 已做 4/6 (还多算 skew/spread);缺 pad-pair groups + 逐 pad 坐标 dump。mm/CSV 只是格式 |
| eext-netlist-explorer | netlist 表格+统计仪表盘+拓扑图+BOM (交互 UI) | ✅full | 数据源 `getNetlistFile()` 与我们 `sch read`/`check`/`netlist` 完全同源;剩下全是 web 可视化,不吸 |
| eext-timing-analysis | 两器件间共网 → net 长度 → Tpd → setup/hold 时序余量 SVG | ⬜none | 零时序/长度分析。**证伪 length-match 墙**:`getNetLength` 真能读实布线长度 |
| eext-bom-compare | 两份 BOM 按位号 diff (added/missing/changed) 导出 | ⬜none | 会导 BOM 但不会 diff 版本;纯文件处理,零 eda.* API |
| **copper-plane** | | | |
| eext-balance-copper | net-less 网格铜块铺满空白区做电镀均衡 (copper thieving),避障+DRC | ⬜none | 我们的铜全是 net-bound 单区;这是密度均衡 tiling。**顺带解锁 teardrop** (同一 source-injection 路径) |
| eext-dynamic-fill-region-for-silkscreen | 丝印层填充多边形,自动对障碍布尔挖孔 (复杂多边形带洞) | 🟡partial | `fill` 已调同一 `PrimitiveFill.create`;缺障碍收集+布尔挖孔 (需 polygon-clipping 库) |
| **routing** | | | |
| eext-coil-creator | 参数化 PCB 线圈 (螺旋/方/六/八边),NFC/电机/无线充 | ⬜none | `route-short` 是 MST 布线,目的不同;线圈=纯数学循环 emit tracks |
| eext-kirouting-integration | 外部 Rust A* 布线器桥:差分/等长/阻抗/fanout/Voronoi 电源过孔 | 🟡partial | 外部布线桥模式我们有 (Freerouting)。缺 diff-pair/等长绕线/阻抗/fanout;Rust 引擎太重不吸 |
| **import-graphics** | | | |
| eext-qrcode-generator | 文本/URL→QR/条码→丝印多边形/图像,放顶层丝印 | ⬜none | 零图像/图形导入;`convertImageToComplexPolygon`+`PrimitiveImage.create` 也解锁通用 logo 丝印 |
| eext-image-contour-to-pcb | 位图/SVG 轮廓→板框/铜/阻焊窗/艺术丝印 | ⬜none | 零 raster 摄入。写侧 API 我们已有,但轮廓矢量化算法重、离功能设计主干远、人在环 |
| **ai-assist** | | | |
| eext-datasheet-helper | PDF.js 解析 datasheet + LLM 问答面板 | ⬜none | 无 eda.* 可吸;我们本身就是 agent,Claude 原生能读 PDF |
| eext-chat-with-ai-kimi | 应用内 Kimi 聊天:设计问答/选中件详情/替代料/netlist 分析 | 🟡partial | 数据 API 全有;唯一缺口=「读选中件→找 pin-兼容替代料」流程,低价低耗 |
| **library-part** | | | |
| eext-ai-device-standardization | 元件/BOM 匹配 JLC 标准库并重绑符号/封装 | 🟡partial | search/get_by_lcsc 已包;缺**已放置件 rebind 符号/封装** (`modify` 改不了符号引用) |
| eext-ai-library-builder | Vision-LLM 读 datasheet→建符号+封装 (BGA/QFN/QFP…) | ⬜none | authoring 盲区 (survey 已标)。`lib_*.create` API 存在;视觉交给 agent |
| eext-ai-symbol-builder | 芯片照片→Qwen2.5-VL 提取引脚→画符号到画布 | ⬜none | 同上但只 emit 松散 primitive (无 lib device 包装),不可复用 |
| **manufacturing-output** | | | |
| eext-mcad-integration | 板 3D STEP 导出 + 与 FreeCAD/Fusion/SW 双向 live-sync | ⬜none | 零 3D/STEP。live-sync 是交互胶水不吸;但 `get3DFile` 单调用可做 `pcb export-3d` |
| **infra** | | | |
| eext-run-api-gateway | 官方 WS 桥,让外部 AI 工具在 EDA 内跑 eda.* | ✅full | 与我们 `transport.ts` 端口扫描 49620-49629 + daemon 逐字撞型;我们是其严格超集 |

> 注:表中无 🧱wall——各插件评估**逐一证伪**了原调研的平台墙 (diff-pair/fanout/length-match/teardrop/net-length 均可达),需回写 KNOWN PLATFORM WALLS 笔记。

## 3. 优先吸收清单 (high / medium)

### High
| # | 来源 | 吸什么 | eda.* API | 难度 |
|---|---|---|---|---|
| 1 | PCB自动化工具 | **DFM 审查补全** (~6 条纯读几何检查):90°/锐角走线、冗余/重叠过孔、悬空单层过孔、两脚器件线宽一致、3W 时钟间距、电源网铜层覆盖、阻焊窗、冗余线段、pad 圆角角度。直接扩我们薄的 dfm-check 面,mirror `sch check` 哲学 | `pcb_Primitive{Line,Via,Pad,Component,Attribute}.getAll` (全只读) | 中 (读几何易) |
| 1b | PCB自动化工具 | **pin/module fanout-with-vias** (密集引脚逃逸到过孔),证明可用启发式路径 | `pcb_PrimitiveLine.create` + `pcb_PrimitiveVia.create` | 中 |
| 2 | eext-balance-copper | **net-less 均衡铺铜/thieving** (`pcb balance`/`pcb thieving`):障碍收集器+每类型 DRC clearance 引擎+FILL source-injection;**同路径顺带破 teardrop 墙** | `sys_FileManager.getDocumentSource/setDocumentSource` (载重), `pcb_Drc.check` | 中-高 (tiling+注入) |

### Medium (按性价比排序)
| # | 来源 | 吸什么 | eda.* API | 难度 |
|---|---|---|---|---|
| 3 | eext-mcad-integration | `pcb export-3d`/`export-step`:单调用写 STEP File 到盘 (chunk-base64 只是它 WS 传输需要,CLI 不需)。补齐 manufacturing-output (今有 DSN/BOM/netlist,缺 3D) | `pcb_ManufactureData.get3DFile('pcbModel','step',...)` | 低 |
| 4 | eext-timing-analysis | `pcb net-length`/`length-match` 报告:逐 net 读长度、按器件 pad 网交集、flag bus skew;可选 Tsu/Th/clock 时序余量。**并修正 length-match 平台墙笔记** | `pcb_Net.getNetLength` + `pcb_PrimitiveComponent.getAll→getState_Pads` | 低-中 |
| 5 | eext-bom-compare | `bom-compare.py`:双 BOM 按位号 diff (added/removed/changed + 计数),TSV/CSV 出。落进 `scripts/` 家族,零 eda.* | 无 (纯文件) | 低 |
| 6 | eext-ai-device-standardization | **已放置件 rebind 符号/封装到标准库**——`modify` 改不了符号引用,须 delete-then-create | `lib_Device.modify` + `sch_PrimitiveComponent.delete/create` | 中 |
| 7 | eext-coil-creator | `pcb coil` 子命令:参数化螺旋/多边形线圈,纯数学循环 emit tracks | `pcb_PrimitiveLine.create` (循环;弧形版可选 `pcb_PrimitiveArc`) | 低 |
| 8 | eext-qrcode-generator | `pcb qrcode`/`silk-graphic`:文本/图 → 丝印图形;同 API 解锁通用 logo/artwork 导入 | `pcb_MathPolygon.convertImageToComplexPolygon` + `pcb_PrimitiveImage.create`/`PrimitiveObject.create` (TOP_SILKSCREEN;核对层 id 3 映射) | 中 |
| 9 | eext-kirouting-integration | **diff-pair / 等长绕线 / fanout 的「输出模式」**——自研启发式算几何写 raw tracks/vias (引擎/Voronoi/Hungarian 太重,不吸,继续外包 Freerouting) | `pcb_PrimitiveLine.create` + `pcb_PrimitiveVia.create` | 高 (但仅结果模式) |
| 10 | eext-ai-library-builder + eext-ai-symbol-builder | **建库三件套** authoring:两侧引脚自动布局 + QFN/QFP/BGA/DIP/SOP pad 数学;视觉/datasheet 交给 Claude 原生 | `lib_Symbol.create`/`lib_Footprint.create`/`lib_Device.create` + `sch_PrimitivePin.create`/`sch_PrimitivePolygon.create` (均在 api-index.json) | 高 (新 authoring 域) |

## 4. 已覆盖 — 验证方向正确

- **eext-netlist-explorer** (✅full):其核心数据源 `sch_ManufactureData.getNetlistFile()` 与我们 `sch read`/`sch check`/`sch netlist` 完全同一权威 API——证明我们的 netlist 语义快照选对了源头,剩余差异全是我们刻意不做的 web 可视化。
- **eext-run-api-gateway** (✅full):官方 WS 桥端口扫描 49620-49629 + `/health` 握手 + 心跳重连,与我们 `transport.ts` + Go daemon (`/health`/`/eda`/`/action`) **逐字撞型**。我们当年刻意自建 (connector-architecture-decision),并在其上叠 20 typed actions + Cobra CLI + skill——是它面向 agent 的严格超集。这两个 full 项证明:**架构选型与权威数据源都押对了**。
