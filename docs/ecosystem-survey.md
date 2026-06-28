# 官方扩展生态调研 & 可吸收能力清单 — 2026-06-28

> **目的**：本项目是 skill 驱动、agent **全自主**操作 EasyEDA Pro 的自动化层。
> 嘉立创官方在 `github.com/easyeda` 开源了一大批扩展(eext-*),它们调用的是和我们
> **完全相同**的 `eda.*` API。本调研系统性挖掘这些扩展的源码 + 官方类型定义,目的不是
> 抄它们的 web UI,而是吸收三样东西:**① 它们调了哪些 `eda.*`(暴露我们还没发现的 API)、
> ② 它们的算法(布线/铺铜/拓扑/选型)、③ 它们覆盖的真实工程场景。**
>
> **数据来源**:`@jlceda/pro-api-types@0.2.63`(`index.d.ts`,21302 行,权威定义,`eda` 暴露
> **86 个命名空间**)+ 6 个官方扩展源码逐行 grep + `prodocs.easyeda.com` API 文档。
> **方法**:四路并行源码挖矿(布线/铺铜、器件标准化、网表/DRC、命名空间全扫)。

---

## 0. 两个战略结论

1. **我们的架构被官方"撞型"验证。** 官方 `eext-run-api-gateway` 的桥接模型和我们**几乎一模一样**:
   扫描端口 `49620-49629`、`/health` 健康检查、WebSocket 握手、自动重连,连身份标识都叫
   `easyeda-bridge`。我们当初[自建 connector 而非用官方 gateway](../CLAUDE.md) 的决策方向正确,
   连官方都收敛到同一套模型。

2. **定位根本不同 → 吸收 API 与算法,不吸收 UI。** 所有官方扩展(包括它们的 AI agent)都是
   **web UI / 人在环里点按钮**;我们是 **skill 驱动、agent 跑完整流程**。而且所有扩展的 AI 全部走
   **外部 LLM**(`sys_ClientUrl.request` 发 OpenAI 兼容 / Qwen-VL / GLM)——**EasyEDA 本身没有内置 AI**,
   `eda.*` 只提供库搜索、渲染图、图元 CRUD 这些确定性能力。**这对我们是利好:AI 决策层我们本就是
   agent,缺的只是把那几个数据/写入 API 封装进 typed action。**

---

## 1. 官方开源扩展全景(`github.com/easyeda`,均开源)

| 扩展 | 能力 | 对我们的价值 |
|---|---|---|
| **eext-run-api-gateway** | AI 编程工具的 WebSocket API 网关 | 架构撞型参照(已验证我们方向) |
| **eext-kirouting-integration** | KiCad Rust A* 自动布线接入 | 🔥 真实布线原语 + 外部引擎接入范式 |
| **eext-balance-copper** | 空白区自动均衡铺铜 | 🔥 铺铜/填充的源码注入路径(也是泪滴潜在唯一路径) |
| **eext-ai-device-standardization** | AI 自动匹配器件信息/符号/封装 | 🔥 `lib_Device.search/getByLcscIds` + 五步绑定法 |
| **eext-ai-library-builder / eext-ai-symbol-builder** | AI 建库/建符号(视觉 LLM 识 PDF) | 程序化建符号/建焊盘 API |
| **eext-netlist-explorer** | 网表引脚表/连接表/拓扑图 | 网表数据源(getNetlistFile) |
| **eext-export-design-report** | 网络长度/差分对/等长统计报告 | 🔥 纯读 PCB API,可直接吸收 |
| **eext-timing-analysis** | path delay / Setup-Hold | 网络长度 + 过孔计数技巧 |
| **eext-bom-compare** | 多格式 BOM diff | BOM 工具增强 |
| eext-datasheet-helper / eext-chat-with-ai-kimi | OCR datasheet / Kimi 问答 | 长期路线 |
| eext-mcad-integration-*(SolidWorks/Fusion360/FreeCAD) | 3D MCAD 互通 | 暂不相关 |
| eext-coil-creator / eext-image-contour-to-pcb / eext-qrcode-generator | 线圈/图形/二维码生成 PCB | 暂不相关 |
| **pro-api-sdk / pro-api-types** | 官方 SDK + 权威类型定义 | 🔥 API 发现的权威源 |

---

## 2. `eda.*` 命名空间全景(86 个,按前缀归类)

| 前缀 | 命名空间(主要能力) |
|---|---|
| **dmt_**(文档树, 11) | `dmt_Project`(建/开工程)、`dmt_Schematic`(建图页/标题栏)、`dmt_Pcb`、`dmt_Panel`、`dmt_EditorControl`(开关文档/缩放)、`dmt_SelectControl`、`dmt_Event` |
| **sch_**(原理图, 21) | `sch_Document`(importChanges/save/autoRouting/autoLayout)、`sch_Drc`(**仅 check**)、`sch_Net`/`sch_Netlist`、`sch_SimulationEngine`(**电路仿真**)、`sch_ManufactureData`(BOM/网表)、`sch_Event`、`sch_Primitive*`(Wire/Pin/Component/Bus/Text…) |
| **pcb_**(PCB, 26) | `pcb_Document`(**autoRouting/autoLayout/clearRouting/ratline**)、`pcb_Drc`(check + **完整规则/网络类/差分对/等长组**, 43 方法)、`pcb_Layer`(**层叠管理**)、`pcb_Net`(长度/高亮/网表)、`pcb_ManufactureData`(**Gerber/贴片/3D/IPC/下单**)、`pcb_RayTracerEngine`(3D 光追)、`pcb_Event`(**实时 DRC 事件**)、`pcb_Primitive*`(Via/Pad/Line/Pour/Poured/Region…) |
| **lib_**(集成库, 9) | `lib_Device`(create/**search**/**getByLcscIds**)、`lib_Symbol`、`lib_Footprint`、`lib_3DModel`、`lib_Cbb`(**复用模块**)、`lib_PanelLibrary` |
| **pnl_**(拼板, 1) | `pnl_Document`(**panelize/拼板**) |
| **sys_**(系统, 25) | `sys_ClientUrl`(外发 HTTP)、`sys_FileManager`(**getDocumentSource/setDocumentSource — 整份文档源码读写**)、`sys_Storage`、`sys_FormatConversion`(**Altium/DSN 转换**)、`sys_IFrame`、`sys_MessageBus`、`sys_Timer` 等 |

---

## 3. 已覆盖 vs 盲区对照

| 能力域 | 我们(22 actions) | 官方 API | 状态 |
|---|---|---|---|
| 原理图读/放/改/连线/netflag/选择/DRC/BOM/网表 | ✅ 全套 | `sch_*` | **已覆盖** |
| PCB 读组件/层/网络/板框 + import_changes + 布局 | ✅ | `pcb_Document`/`pcb_Layer`/`pcb_Net` | **已覆盖**(只读 + 自实现布局) |
| **PCB 自动布线/布局** | 实测=仅文件式 | `pcb_Document.autoRouting/autoLayout/clearRouting`(**类型声明 @alpha,3.2.148 实测仍 undefined**) | **盲区(类型 vs 实测有出入,见 §6)** |
| **PCB 走线/过孔/铺铜图元** | ❌ | `pcb_PrimitiveLine/Via/Pour/Poured.create` | **盲区** |
| **PCB 设计规则/网络类/差分对/等长** | ❌ | `pcb_Drc.*`(43 方法) | **完全盲区** |
| **PCB 网络长度/飞线/高亮** | ❌ | `pcb_Net.getNetLength`、`pcb_Document.startCalculatingRatline` | **盲区** |
| **PCB 制造输出(Gerber/贴片/3D/下单)** | ❌ | `pcb_ManufactureData`(30+ 方法) | **完全盲区** |
| **库管理/器件创建/封装放置/复用模块** | 仅原理图 search | `lib_Device/Footprint/Cbb`(create/place) | **盲区** |
| **PCB 层叠管理(2/4 层)** | ❌(只 list) | `pcb_Layer.setTheNumberOfCopperLayers` 等 | **盲区** |
| **拼板 panelize / 电路仿真 / 事件订阅 / 3D 渲染 / 格式转换** | ❌ | `pnl_Document` / `sch_SimulationEngine` / `*_Event` / `pcb_RayTracerEngine` / `sys_FormatConversion` | **完全盲区** |

---

## 4. 四条线的 API 发现详情

### 4.1 PCB 布线/铺铜(kirouting-integration + balance-copper)

**真实布线原语(不依赖外部 server,这是我们缺的交互式布线底层):**
```
eda.pcb_PrimitiveLine.create(net, layer, sx,sy, ex,ey, width, false)   // 创建走线段
eda.pcb_PrimitiveVia.create(net, x,y, holeDiameter, diameter)          // 创建过孔
eda.pcb_PrimitiveLine.delete(id) / eda.pcb_PrimitiveVia.delete(id)     // ripup
eda.pcb_Drc.getCurrentRuleConfiguration()                              // 读真实 DRC 规则(线宽/孔径/间距/板边)
```
**外部引擎接入范式**:采集(`pcb_*.getAll` 序列化成 JSON)→ `sys_ClientUrl.request` POST 到本地
`localhost:8765` Python bridge → bridge 转 KiCad 格式调 **Rust A* 引擎** → 异步 job 轮询 → 回写时
`PrimitiveLine/Via.create` 并发批量(并发 50)落真实图元。**与我们的"文件式 autorouter"同代外挂思路,
但它的回写用真实 create API——正是我们可以直接吸收的部分。**

**铺铜/填充靠源码注入**(无 `createPour` 类 API):`getDocumentSource()` 取全文 → append 一行
`{"type":"FILL",...}||{layerId,fillStyle:"SOLID",path:[['CIRCLE',x,y,r] 或 [x,y,'L',...]],...}` →
`setDocumentSource()`。清除按记录的 id filter 掉对应行。**这也是泪滴的潜在唯一实现路径**(自己拼焊盘
根部水滴 FILL)。

**teardrop:确认无创建 API。** 全 d.ts 仅 1 处 `'TearDrop'`(`pcb_ManufactureData` 导 Gerber 的对象类型
过滤枚举)——UI 生成的泪滴能进 Gerber,但无编程式生成方法。

### 4.2 器件标准化/选型(ai-device-standardization + library/symbol-builder)

**官方在线立创库搜索 API(关键发现):**
```
eda.lib_Device.search(keyword, scope?)   // scope='project' 搜工程库,省略搜立创在线商城
eda.lib_Device.getByLcscIds(lcscId)      // 直接用 LCSC C 号精确查,返回数组
// 返回字段: name, supplierId(LCSC C号), manufacturerId(MPN), uuid, libraryUuid, symbolUuid, footprintUuid
eda.lib_Footprint.search / eda.lib_Symbol.search / eda.lib_Device.modify(改封装/符号关联)
eda.lib_*.getRenderImage(...)            // 渲染图 Blob(UI 预览用)
```
返回里**自带 `uuid + libraryUuid + LCSC号 + symbolUuid + footprintUuid`——正是我们 `standard-parts.json`
手工维护的全部字段**。意味着手查 JSON 可换成运行时 `getByLcscIds('C8734')` / `search('ESP32-S3')`,
实时拿到 placement-ready 的 uuid 对,`sch_PrimitiveComponent.create({libraryUuid, uuid}, ...)` 直接放置,闭环成立。

**五步绑定法(换封装/换符号的标准动作)**:`modify(库器件关联)→delete(旧图元)→create(新图元带新封装)
→modify(恢复 designator/位置/otherProperty)`。**重要坑**:导入器件(Altium 等)`libraryUuid` 常为空,
必须先 `lib_Device.search(name,'project')` 按 uuid 反查真实库 UUID,否则 `create` 卡死(`resolveDeviceLibrary`)。

**AI 选型工作流(可抄进我们的 parts-select)**:两段式——① 把器件信息喂 LLM 要 `{"keyword":"..."}`;
② `lib_Device.search` 取前 10 候选,再问 LLM `{"idx":n}` 选最匹配。带降级链 `keyword→MPN→去容差 core value
→value+package`。有 LCSC C 号则走 `getByLcscIds` 精确命中,跳过 AI。

**程序化建符号/建封装**:`sch_PrimitivePin.create / sch_PrimitivePolygon.create`(符号引脚/边框)、
`pcb_PrimitivePad.create`(焊盘)——builder 类纯几何算法铺 IPC 坐标,不查库。

### 4.3 网表/DRC(netlist-explorer + export-design-report + timing-analysis)

**网表数据源——大家都用这个,没有更细的拓扑 API:**
```
eda.sch_ManufactureData.getNetlistFile()   // 制造网表 File → .text() → JSON.parse(我们已在用)
```
官方网表生成器**不用** `sch_Netlist.getNetlist()`,引脚表/连接表/拓扑图全是 iframe 里 JS 从这份 JSON
自己算的。NC 引脚也是逐引脚 `getState_NoConnected()` 数出来。**坐实:无网表拓扑专用 API,都在 raw JSON 上重建。**

**DRC 详情分两侧看:**
- **原理图侧 = 聚合-only(坐实)**:`sch_Drc` 只有 `check`,三个官方报告扩展无一拿到逐条 violation,
  export-design-report 的 `pcb_Drc.*` 也只读 net-class/差分对/等长组**约束定义**。我们 `sch check`
  几何重建路线正确且唯一可行。
- **PCB 侧 = 有逐条明细(已活板确认,见 [`pcb-feature-discovery.md`](pcb-feature-discovery.md))**:
  `pcb_Drc.check` 在 3.2.148 真板上返回**嵌套明细** `{count, list:[{errorObjType, errorType,
  explanation:{errData:{net, obj1, ...}}, globalIndex}]}`(我们 `pcb drc` CLI 已在用)——远强于原理图侧。
  `pcb_Event.addRealTimeDrcResultEventListener` 是额外的实时逐条途径。

**设计报告——全是纯读 PCB API,零写入,可直接吸收:**
```
eda.pcb_Net.getAllNetName() / getNetLength(name)              // 每网走线长度(mil)
eda.pcb_Drc.getAllNetClasses()                                // 网络类→成员网络
eda.pcb_Drc.getAllDifferentialPairs()                         // 差分对 P/N(算 skew)
eda.pcb_Drc.getAllEqualLengthNetGroups()                      // 等长组
eda.pcb_Drc.getAllPadPairGroups() / getPadPairGroupMinWireLength()
eda.pcb_PrimitiveVia.getAll() + via.getState_Net()            // 每网过孔数
```
我们当前 PCB 侧只用了 `pcb_Net.getAllNets()`,这套长度/约束读取器**完全没用 → 一整块净新增能力**。

---

## 5. 可吸收功能清单(按优先级,映射 skill/action + 落地难度)

> **进度(2026-06-28)**:**A1 / A2 / A3 / A5 已落地并真机验证通过**(PCB1 / connector 0.5.15):
> A1 解析 C6186→AMS1117-3.3 身份、A5 返回完整规则配置、A3 报告 4 网络含网长/net-class/差分/等长、
> A2 建 GND 走线(网长回读 0→500,确认挂对网)、`pcb drc` + 存盘通过。真机验证另暴露一个 gap——
> **缺 `pcb.save` 且 PCB 不在 autosave 覆盖内**——已补(新增 `pcb.save` action + `saveActionForDocType`
> 加 `pcb`→`pcb.save`,PCB 编辑现与原理图一样自动落盘)。A4(直调自动布线)本 build 不可用,阻塞中。
> 详见 [`FEATURES.md`](FEATURES.md)。

| # | 吸收什么 | API | 落到哪 | 难度 |
|---|---|---|---|---|
| **A1** | **在线器件搜索**,把 standard-parts.json 从"唯一来源"降级为"缓存层":未命中则在线搜并自动写回 | `lib_Device.search` / `getByLcscIds` | `easyeda-schematic` 放置链 + 新 action `lib.device.search`/`lib.device.by-lcsc`(CLI `easyeda lib …`) + `easyeda-conventions` | **低** |
| **A2** | **真实走线/过孔图元**,补交互式布线缺口(可先做脚本化逐段布线,甚至自研简易布线器直接回写) | `pcb_PrimitiveLine.create` / `pcb_PrimitiveVia.create` / `.delete` | `easyeda-pcb` + 新 action `pcb.route_segment`/`pcb.place_via`/`pcb.rip_up`(标 `Mutates`) | **低** |
| **A3** | **PCB 设计报告**:每网长度 / 差分对 skew / 等长偏差(纯读、零风险,design-flow 的天然 DRC 补强) | `pcb_Net.getNetLength` + `pcb_Drc.getAll{NetClasses,DifferentialPairs,EqualLengthNetGroups}` | 新只读 action `pcb.report`(CLI) + `easyeda-design-flow` 门禁 | **低** |
| **A4** | **直调自动布线**(类型声明 @alpha,但 3.2.148 实测仍 undefined → 暂走文件式) | `pcb_Document.autoRouting(props?)` @alpha → `{routedNets, totalNets}` + `clearRouting` | `easyeda-pcb` 新 action `pcb.auto_route` | **高**(本 build 未暴露,等平台或用文件式) |
| **A5** | **真实 DRC 规则值**喂 layout-lint / DRC 门禁(用工程真实线宽/间距/板边判定,替代经验猜测) | `pcb_Drc.getCurrentRuleConfiguration()` | `easyeda-pcb` 读上下文 + `easyeda-design-flow` 门禁,只读 action `pcb.drc-rules` | **低-中**(规则 JSON 层级深,键名带空格) |
| **A6** | **换封装/换符号标准动作**,固化"五步绑定法 + resolveDeviceLibrary"(导入器件 libraryUuid 为空反查) | `lib_Device.modify` + 原始态保存/恢复 | `easyeda-schematic` 新 action `sch.rebind-footprint`/`sch.rebind-symbol` | **中**(需端到端验证,挂 ESP32 回归) |
| **A7** | **sch check 加 netlist-JSON 交叉校验**:JSON 权威 pin→net 归属 vs 几何"导线触碰引脚"判定对照,降误报漏报 | 复用 `sch_ManufactureData.getNetlistFile()` | `cmd_sch_check.go` floating-pin 规则加一路 JSON 源 | **中**(需摸清网表 JSON pin 命名→坐标映射) |
| **A8** | **AI 选型升级**:两段式 prompt(关键词→候选→选 idx)+ 降级链,从规则筛选升级为 LLM+库搜索 | (prompt + `lib_Device.search`) | `scripts/parts-select.py` + `easyeda-conventions` part-selection | **低-中** |
| **A9** | **铺铜/填充**(源码注入),也是泪滴的潜在唯一实现路径 | `sys_FileManager.getDocumentSource/setDocumentSource` + FILL 图元格式 | `easyeda-pcb` 新 action `pcb.copper-fill`;FILL schema 记进 conventions | **中**(绕过 typed API,需吃透源码拼接 + 失败回滚) |

**更长期盲区(列入 roadmap,暂不动手)**:制造输出 `pcb_ManufactureData`(Gerber/贴片/下单——流程脊柱的交付端)、
拼板 `pnl_Document`、电路仿真 `sch_SimulationEngine`、复用模块 `lib_Cbb`、层叠管理 `pcb_Layer.setTheNumberOfCopperLayers`、
事件订阅 `*_Event`。

---

## 6. 需要更正/确认的旧认知

- ⚠️ **"PCB 自动布线仅文件式" → 类型有出入,但实测已确认本 build 不可用。** 发布的类型定义
  (`pro-api-types@0.2.63`)**声明了** `pcb_Document.autoRouting(props?)` 这个 @alpha API(返回
  `{routedNets, totalNets}`,支持 `nets[]`/`ignoreNets[]`/`cornerStyle`/`optimization`)。**但活板实测两次
  都是 undefined**:2026-06-26(client 锁 `^0.2.21`)+ 2026-06-28(EasyEDA 3.2.148)——运行 build 落后于
  发布类型,直调 API 没暴露。**结论:声明存在(@alpha),当前 build(3.2.148)实测不可用;文件式
  (`importAutoRouteJsonFile`/SES)仍是唯一可靠路径,等平台 build 跟上类型再复测。**
- ✅ **泪滴无创建 API** —— 官方扩展源码 + d.ts 双重确认。要做只能源码注入造 FILL 图元(见 A9)。
- ✅ **原理图 DRC 仅聚合** —— `sch_Drc` 只有 `check`,官方报告扩展也拿不到逐条,确认成立。
- ✅ **PCB DRC 有逐条明细(已活板确认)** —— `pcb_Drc.check` 在 3.2.148 返回嵌套 `{count, list:[{errorObjType,
  errorType, explanation, globalIndex}]}`,我们 `pcb drc` 已在用;`pcb_Event.addRealTimeDrcResultEventListener`
  是额外的实时逐条途径。**PCB 侧不存在"聚合-only"问题,那是原理图侧专属。**
- ✅ **EasyEDA 无内置 AI** —— 所有官方 AI 扩展走外部 LLM,我们的 agent 决策层是优势而非缺口。

---

## 来源

- [EasyEDA 官方 GitHub 组织](https://github.com/easyeda) — 全部 eext-* 扩展开源
- [eext-run-api-gateway](https://github.com/easyeda/eext-run-api-gateway) · [pro-api-sdk](https://github.com/easyeda/pro-api-sdk) · [eext-kirouting-integration](https://github.com/easyeda/eext-kirouting-integration) · [eext-balance-copper](https://github.com/easyeda/eext-balance-copper) · [eext-ai-device-standardization](https://github.com/easyeda/eext-ai-device-standardization) · [eext-netlist-explorer](https://github.com/easyeda/eext-netlist-explorer) · [eext-export-design-report](https://github.com/easyeda/eext-export-design-report)
- [EasyEDA Pro API 文档](https://prodocs.easyeda.com/en/api/guide/index.html) · 权威类型定义 `@jlceda/pro-api-types@0.2.63`
- [嘉立创EDA扩展广场](https://extensions.oshwhub.com/)
