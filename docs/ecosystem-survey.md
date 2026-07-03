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
>
> **项目记忆 / Issue 策略**:遇到官方 API 未暴露、类型声明与运行时不一致、或官方文档没有说明的能力时,
> 不把猜测实现当成稳定能力。先记录官方类型/文档证据、运行时探测结果、官方扩展源码用法和我们的替代路线。
> 若缺口影响正确性或可维护性,整理最小复现和期望行为,去官方 `easyeda/pro-api-sdk` 或相关官方库提 issue。

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
- **2026-06-29 复查(ceshi / EasyEDA 3.2.121 / connector 0.5.19)**:
  - 类型定义声明 `sch_Drc.check(strict,userInterface,includeVerboseError:true) -> Array<any>`,但运行时
    **8 组参数全返回 boolean**,不是数组:`strict=false -> true`,`strict=true -> false`;`userInterface`
    只影响是否打开 UI 面板,不改变返回形态。
  - `sys_Log.clear/sort/find` 在 `sch_Drc.check(true,true,true)` 后返回空日志;UI DRC 面板结果**不会进入**
    public log API。
  - `sys_MessageBus.pullAsyncPublic` 试探 `schDrcResult`/`drcResult`/`SCHEMATIC_DRC`/`drc` 等公共 topic
    均 timeout;未发现可订阅的 UI DRC 结果 topic。
  - `SCH_Event` 运行时只暴露 mouse / primitive / simulation 事件;**没有** schematic DRC result listener。
    `PCB_Event` 类型有 `addRealTimeDrcResultEventListener`,但 3.2.121 运行时 prototype 未暴露该方法。
  - `sch_Net.getAllNets*()` 在当前异常原理图上返回空数组,不能作为 DRC 明细源。
  - `sch_ManufactureData.getNetlistFile()` 能导出完整 JSON,包含 `components.*.pinInfoMap.*.net`
    和 `designRule.netRule`;这是比 `sch_Net` 更可靠的官方数据源,可用于 `sch check` 与几何连通结果交叉校验。
  - 结论保持:原理图 UI DRC 明细没有 public API;要对齐 UI warning,必须用 `sch check` 从 primitives +
    netlist JSON 重建。并且 connector 不能假设 verbose overload 一定返回数组。
  - 已在官方 issue 跟进:[easyeda/pro-api-sdk#27](https://github.com/easyeda/pro-api-sdk/issues/27)。在官方支持
    `includeVerboseError=true` 稳定返回结构化明细、或提供 DRC 面板读取 API 之前,生产流程暂以我们的
    `sch check` 作为逐条 warning/定位/自动修复依据,`sch_Drc.check` 只作为 boolean SDK 门禁。
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
| **A1** | **在线器件搜索**,把 standard-parts.json 从"唯一来源"降级为"缓存层":未命中则在线搜并自动写回 | `lib_Device.search` / `getByLcscIds` | `easyeda-agent` schematic flow + 新 action `lib.device.search`/`lib.device.by-lcsc`(CLI `easyeda lib …`) + `standard-parts.json` | **低** |
| **A2** | **真实走线/过孔图元**,补交互式布线缺口(可先做脚本化逐段布线,甚至自研简易布线器直接回写) | `pcb_PrimitiveLine.create` / `pcb_PrimitiveVia.create` / `.delete` | `easyeda-agent` PCB flow + 新 action `pcb.route_segment`/`pcb.place_via`/`pcb.rip_up`(标 `Mutates`) | **低** |
| **A3** | **PCB 设计报告**:每网长度 / 差分对 skew / 等长偏差(纯读、零风险,design-flow 的天然 DRC 补强) | `pcb_Net.getNetLength` + `pcb_Drc.getAll{NetClasses,DifferentialPairs,EqualLengthNetGroups}` | 新只读 action `pcb.report`(CLI) + `easyeda-agent` design-flow 门禁 | **低** |
| **A4** | **直调自动布线**(类型声明 @alpha,但 3.2.148 实测仍 undefined → 暂走文件式) | `pcb_Document.autoRouting(props?)` @alpha → `{routedNets, totalNets}` + `clearRouting` | `easyeda-agent` PCB flow + 新 action `pcb.auto_route` | **高**(本 build 未暴露,等平台或用文件式) |
| **A5** | **真实 DRC 规则值**喂 layout-lint / DRC 门禁(用工程真实线宽/间距/板边判定,替代经验猜测) | `pcb_Drc.getCurrentRuleConfiguration()` | `easyeda-agent` PCB context + design-flow 门禁,只读 action `pcb.drc-rules` | **低-中**(规则 JSON 层级深,键名带空格) |
| **A6** | **换封装/换符号标准动作**,固化"五步绑定法 + resolveDeviceLibrary"(导入器件 libraryUuid 为空反查) | `lib_Device.modify` + 原始态保存/恢复 | `easyeda-agent` schematic flow + 新 action `sch.rebind-footprint`/`sch.rebind-symbol` | **中**(需端到端验证,挂 ESP32 回归) |
| **A7** | **sch check 加 netlist-JSON 交叉校验**:JSON 权威 pin→net 归属 vs 几何"导线触碰引脚"判定对照,降误报漏报 | 复用 `sch_ManufactureData.getNetlistFile()` | `cmd_sch_check.go` floating-pin 规则加一路 JSON 源 | **中**(需摸清网表 JSON pin 命名→坐标映射) |
| **A8** | **AI 选型升级**:两段式 prompt(关键词→候选→选 idx)+ 降级链,从规则筛选升级为 LLM+库搜索 | (prompt + `lib_Device.search`) | `scripts/parts-select.py` + `easyeda-agent` part-selection | **低-中** |
| **A9** | ~~**铺铜/填充**(源码注入)~~ **已被 typed API 取代**——`pcb.fill.create`/`pcb.pour.create`(#17/#28)直接建 FILL/POUR,无需源码注入 | `pcb_PrimitiveFill.create` / `pcb_PrimitivePour.create` | ✅ 已落地 | 已完成 |
| **A10** | **丝印动态填充 + 障碍避让**([eext-dynamic-fill-region-for-silkscreen](https://github.com/easyeda/eext-dynamic-fill-region-for-silkscreen)):在丝印层(TOP/BOTTOM_SILKSCREEN)建填充区,**自动避开焊盘/位号/过孔/文字/挖槽**(每障碍扩 gap → 多边形布尔差集,带洞)。我们没有(silk-align 只挪位号)。核心=多边形布尔(它用 [polyclip-ts](https://github.com/luizbarboza/polyclip-ts) / Martinez-Rueda-Feito) | `pcb_PrimitiveFill.create`(**已确认支持丝印层**)+ 障碍收集(pad/attribute/via/string getAll)+ 多边形布尔差集 | `easyeda-agent` PCB flow + 新命令 `pcb silk-fill`(daemon 侧算几何 → fill.create) | **中-高**(fill+障碍收集易,布尔差集带洞是核心;Go 侧引入多边形裁剪库或自研) |

> **注**:`setTheNumberOfCopperLayers`(旧盲区)已在 #26 吸收(`pcb stackup`);泪滴已确认
> 平台墙(#31,无 create API)。survey 2026-06-28 版部分条目已过时,下次做市场全量扫描时更新。

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

## 7. PCB 布线能力支持矩阵(2026-06-28 深度调研)

> 起因:EasyEDA「布线(U)」菜单内置很多工具,排查我们到底支持哪些。方法:对权威类型定义
> `pro-api-types@0.2.63`(21302 行)**全命名空间**核实每个菜单项有无 `eda.*` API,并对抗式验证。
> 结论先行:**整张交互式布线菜单 0 个「逐线交互」API。** 不存在 `pcb_Routing`/`Route`/`Track`/`Trace`
> 命名空间;`pcb_SelectControl` 只有选择/取鼠标坐标,无布线方法。能驱动布线的只有**批量/文件式**。

### 7.1 「布线」菜单逐项对照

| 菜单项 | 有 `eda.*` API? | 我们支持? | 说明 |
|---|---|---|---|
| 单路布线(交互避障) | ❌ 无逐线 API | 〜 间接 | 只有底层 `pcb_PrimitiveLine.create`(我们已包 `pcb track`),给两端点画线,**无 push/避障** |
| 多路布线 | ❌ | ❌ | 无 |
| 差分对布线(布线动作) | ❌ | ❌ | 差分对只有**定义** API(见 7.3),无布线动作 |
| 拉伸导线 | ❌ | ❌ | 只有几何式 `pcb_PrimitiveLine.modify`(改端点),无交互拉伸 |
| 优化选中导线 | ❌ | ❌ | `optimization` 仅是 `autoRouting` 的参数枚举,非独立命令 |
| 等长调节(蛇形) | ❌ | 〜 读 | 无生成蛇形/调节 API;`getNetLength` 仅读长、`pcb report` 算 spread |
| 差分对等长调节 | ❌ | 〜 读 | 同上,只能读 skew(`pcb report`) |
| 扇出布线 | ❌ | ❌ | 全 d.ts 无 fanout/breakout/escape |
| 移除回路 | ❌ | ❌ | 无 removeLoop |
| 布线模式 / 布线拐角 | ❌ 交互无 | ❌ | `cornerStyle`(45/90)仅作 `autoRouting` 入参(枚举 line 4244) |
| 自动布线… | ⚠️ `autoRouting()` @alpha(line 4590) | ❌ | **3.2.148 实测 undefined**;可限定 `RoutingNets:'selected'|'selectedComponents'|string[]` |
| 清除布线 | ✅ `clearRouting('all'\|'net'\|'connection')` @alpha(4567) | ❌ 未包 | 🟢 可快速补 |

### 7.2 程序化布线的唯一可行范式(官方 kirouting 证实)

`eext-kirouting-integration` 没有任何「智能布线器」可调——它的做法,也是我们唯一能走的路:
**读图元(`*.getAll`)收集问题 → 外部引擎(KiCad 格式 + Rust A*,localhost:8765)→ `pcb_PrimitiveLine.create`/
`pcb_PrimitiveVia.create` 回写真实走线**;rip-up 是**手撸** `getAll` 按 net 过滤再 `delete`(连 `clearRouting`
都没用)。即:**智能布线没有 API,要做只能「读图元 + 自带引擎 + 写图元原语」。**

### 7.3 布线/约束 API 全景 vs 我们的覆盖

| 能力族 | API(行号) | 状态 | 我们 |
|---|---|---|---|
| **走线/过孔创建** | `pcb_PrimitiveLine.create`(6859)、`pcb_PrimitiveVia.create`(6541) | ✅ | **已包** `pcb track` / `pcb via` |
| **走线/过孔 改/删/列**(rip-up/reroute/list) | `Line.modify/delete/getAll`(6876/6867/6922)、`Via.*`(6558/6549/6603) | ✅ API 全 | ❌ **只 create,缺改/删/列** 🟢 |
| **清除布线** | `pcb_Document.clearRouting`(4567)@alpha | ✅ API | ❌ 未包 🟢 |
| **铺铜 / 填充 / 区域** | `pcb_PrimitivePour.create`(8905)、`Fill.create`(9520)、`Region.create`(8609 含 keepout) | ✅ API 全 | ❌ **未包——真实板最大缺口** 🟠 |
| **铜弧 / polyline 走线** | `Arc.create`(7191)、`Polyline.create`(9248) | ✅ API | ❌(弧仅板框用) |
| **网络类 定义** | `createNetClass`/`add/remove/modify/delete/getAll`(4885–4927) | ✅ CRUD 全 @beta | ❌ 写侧未包(读已在 `pcb report`) |
| **差分对 定义** | `createDifferentialPair`/`modify*/delete/getAll`(4937–4983) | ✅ CRUD 全 @beta | ❌ 写侧未包 |
| **等长组 定义** | `createEqualLengthNetGroup`/`add/remove/modify/delete/getAll`(4995–5037) | ✅ CRUD 全 @beta | ❌ 写侧未包 |
| **规则配置 CRUD** | `get/save/rename/delete/setAsDefault/overwriteCurrentRuleConfiguration` + `getNetRules`/`overwriteNetRules`(4727–4833) | ✅ 全 | ❌ 仅 `getCurrentRuleConfiguration`(`pcb drc-rules`) |
| **飞线/ratsnest** | `startCalculatingRatline`(4415)@public、`getCalculatingRatlineStatus`(4407)、stop(4422) | ✅ | 〜 `import_changes` 内部用 start |
| **自动布线/布局** | `autoRouting`(4590)/`autoLayout`(4597)@alpha;文件式 import(4375/4384)@beta;导出 `getDsnFile`(6144)/`getAutoRouteJsonFile*`(6166/6175) | ⚠️ @alpha 本 build 不可用 | ❌(A4 阻塞) |

### 7.4 据此可补的布线 roadmap(API 齐全、按价值)

- ✅ **R1 铺铜(已落地)** `pcb pour` / `pour-list` / `pour-delete` / `pour-rebuild`(`pcb_PrimitivePour.create` 需先 `pcb_MathPolygon.createPolygon` 建多边形 → `rebuildCopperRegion()` 重灌;连接器内部完成,调用方传裸点)。
- ✅ **R2 走线/过孔 rip-up + list(已落地)** `pcb track-list` / `via-list` / `rip-up` / `clear-routing`。rip-up 是可靠手撸(getAll→过滤→删),**只删铜层**(TOP/BOTTOM/INNER),绝不碰板框/丝印/装配/机械/锁定图元;含 arc。(`modify` 暂未包,delete+recreate 即可。)
- 🟢 **R3 布线约束定义** ——`pcb netclass` / `pcb diffpair` / `pcb eqlen` 写侧(create/add/remove/delete),与已有 `pcb report` 读侧配套;是自动布线/等长校验的前置。
- 🟡 **R4 规则配置写** ——`saveRuleConfiguration`/`setAsDefault` 等,让 agent 能设/切 DRC 规则集。
- 🔴 **R5 自动布线** ——`autoRouting` 本 build undefined,维持文件式/等平台。

> ⚠️ **重要边界(写进 conventions / design-flow)**:智能布线(单路避障、推挤、等长蛇形、扇出、优化)
> **无 API,agent 做不了**——这些仍需人在 EasyEDA UI 里完成,或自带外部布线引擎(如 kirouting)。
> 我们能给的是:铺铜、按坐标布线/过孔、rip-up、约束定义、长度/skew 报告与 DRC 门禁。

---

## 8. PCB 自动布线 + keep-out 深挖（2026-06-29，真机 + 官方文档扫描）

接 §7。这次把「能不能产出能用的板」挖到底，结论硬：

### 8.1 `autoRouting()` 被 @alpha「开发版本」门挡死，且解锁方法无文档

- 真机实测（connector 0.5.24 / EasyEDA 3.2.149）：`PCB_Document` 上 `@public`/`@beta`
  方法全可用（`importChanges`、`importAutoRouteSesFile`/`JsonFile`、`save`、
  `startCalculatingRatline`…），**唯独 `@alpha` 的 `autoRouting` / `autoLayout` /
  `clearRouting` 全 `undefined`**；另一个 `@beta` 的 `pcb_PrimitiveRegion.create` 也可用。
  → 规律就是 **@beta 能用、@alpha 不能用**。
- 官方 `stability` 文档（中英文都看了）只说：「开发阶段 API 仅当扩展被设置为
  **开发版本**时可用」，但 **`stability` / `how-to-start` / `extension-json` /
  `pro-api-sdk` 仓库全都没写「怎么把扩展设成开发版本」**。EasyEDA UI 里也没找到开关。
- **结论**：`autoRouting()` 对普通构建 / sideload 的扩展**不可达**，且**无任何文档化的
  解锁途径**。→ issue **#28**（请实装/GA autoRouting，**或把"开发版本"机制写进文档**）。

### 8.2 `getDsnFile` 丢弃禁止区域（keep-out）

- ESP32-S3-WROOM-1 模块封装**不带**天线 keep-out（`pcb_PrimitiveRegion.getAll()` 初始 0）。
- `pcb_PrimitiveRegion.create(layer, poly, ruleType[])` 可建（ruleType：NO_COMPONENTS=2 /
  NO_WIRES=5 / NO_FILLS=6 / NO_POURS=7 / NO_INNER=8 / FOLLOW_RULE=9；layer 可 TOP/BOTTOM/inner
  或 `MULTI=12` 全层）。
- **红灯**：在 TOP(1) 和 MULTI(12) 层都建了 keep-out，`getDsnFile` 导出的 DSN **仍 0 keepout**
  （`(structure)` 段只有 boundary + clear/width rule + layer）。→ 带天线的板，DSN 无 keepout
  → 外部布线器在天线下走线 → 报废。→ issue **#29**（getDsnFile 应把禁止区域翻成 DSN keepout）。

### 8.3 我方进度（管线已成、引擎/keepout 卡官方）

- Freerouting 文件式往返**已建成 + 真机证明**：`pcb export-dsn`（真 Specctra DSN）/
  `pcb import-autoroute`（`importAutoRouteSesFile` @beta）/ `pcb snapshot` / `pcb autoroute`
  编排（引擎可插拔），实测 rip-up→autoroute 出 **83 铜线**、Connection Error 27→2。
- 但「能用的板」两侧都卡官方：**引擎**（要么自备 Freerouting=GUI 弹窗+自装 Java，要么等
  #28 原生 API）+ **keepout**（#29）。我们不自备环境，故 autoroute 引擎留给原生 API / 用户自备。

### 8.4 副产物：`api search` 索引有 class 归属 bug

- `gen.py` 把部分方法挂错命名空间（实测 `rebuildCopperRegion` 实际在 `IPCB_PrimitivePour`，
  却被索引为 `pcb_Net`）。`api search` 个别结果 namespace 不准 → gen.py 的 class 跟踪待修。

> 关联：task #5（管线）/ #11（keep-out）/ issue #28 #29。

### 8.5 重大转向：自研启发式可行（社区扩展实证），推翻「不可行」结论

> ⚠️ **这条推翻了 §7 / §8.1 的「PCB 自动布线/布局不可行、卡官方 @alpha / 外部引擎」判断。**

第三方社区扩展 **「PCB自动化工具」**（V1.0，2025.9–2026.1，跑在 V2.2.42 / V3.2.x；
用户下载的说明书）提供 9 个工具：**短线自动布线 / 模块自动扇出打孔 / 整版全自动模块化布局
（主芯片+去耦电容聚类）/ 自动局部铺铜+45°·圆弧导角 / 群组草图布线 / 管脚扇出打孔 /
布局导出导入(plctxt.txt，含 ALLEGRO 互通) / 检查多余线段 / 检查90度·锐角**。

- **怎么实现的**：用**标准图元 API + 自研算法**（`pcb_PrimitiveLine.create` / `Via.create` /
  `PrimitivePour` / `PrimitiveComponent.modify`），**不靠 `autoRouting()`/`autoLayout()`**
  （那俩 @alpha/undefined 它们也用不了）。→ 用我们已有的 @beta/@public 原语就能做。
- 它们也撞到同样的 API 缺口（草图线无网络、读不到单网鼠线、DRC 推挤不可行）——印证我们的发现。

**两档策略（定调）**：
1. **启发式档**（短线直连、扇出、模块聚类布局、局部铺铜+导角、走线审核）——**自研更好**：
   无现成库、是 agent 强项、放 **Go daemon**（读状态→算几何→发 create/modify 动作；air 热重载、
   改算法不重导）。
2. **迷宫布线档**（任意距离/拥塞/推挤/等长）——**外包 Freerouting**（开源事实标准；现已有
   CLI + API + MCP，headless 可避 1.9 GUI 弹窗；我们文件式路径已对接）。别自研。

**外部库**：完整布线=Freerouting；布局/短线/扇出/铺铜=无现成库（bespoke 启发式，自研）；
OrthoRoute(GPU/FPGA 小众)、Quilter.ai(商业 RL 云,非库)。

**明天起点**：daemon 里做「模块聚类布局」第一版——`pcb arrange`(已有粗聚类) 的升级，
主芯片+去耦电容就近，纯几何启发式、真机可验、不依赖外部。见 task #13。

---

## 来源

- [EasyEDA 官方 GitHub 组织](https://github.com/easyeda) — 全部 eext-* 扩展开源
- [eext-run-api-gateway](https://github.com/easyeda/eext-run-api-gateway) · [pro-api-sdk](https://github.com/easyeda/pro-api-sdk) · [eext-kirouting-integration](https://github.com/easyeda/eext-kirouting-integration) · [eext-balance-copper](https://github.com/easyeda/eext-balance-copper) · [eext-ai-device-standardization](https://github.com/easyeda/eext-ai-device-standardization) · [eext-netlist-explorer](https://github.com/easyeda/eext-netlist-explorer) · [eext-export-design-report](https://github.com/easyeda/eext-export-design-report)
- [EasyEDA Pro API 文档](https://prodocs.easyeda.com/en/api/guide/index.html) · 权威类型定义 `@jlceda/pro-api-types@0.2.63`
- [嘉立创EDA扩展广场](https://extensions.oshwhub.com/)
