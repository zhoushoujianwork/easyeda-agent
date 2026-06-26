# Phase 2 调研:为 easyeda-agent 增加 PCB 布局布线能力

> 结论先行:**可行(yes-with-caveats)**。PCB 的核心操作(布局 / 手动布线 / 铺铜 / 过孔 / 层 / DRC / 原理图→PCB 同步 / 制造导出)都有原生 `eda.pcb_*` API,且与现有 `eda.sch_*` 的 CRUD/ID 契约**逐一镜像**,传输层 / 守护进程 / 协议层 100% 复用。真正的"缺口"集中在三处:**对齐 / 网格 / 吸附无 API(manual-only)**、**内置自动布线 @alpha 不可依赖**、**无编程级 undo**。这些都有明确的工程化绕过方案,不构成阻塞。

本报告所有 API 行号均针对 vendored `extension/node_modules/@jlceda/pro-api-types/index.d.ts`(v0.2.63,共 21302 行),并已对照真实代码核对(经多代理逐行对抗式验证)。

---

## 1. 实现原理:与原理图特性完全一致

现有原理图链路:

```
skill/CLI ──HTTP /action──▶ Go daemon (dispatch.go 按动作名路由)
        ──WebSocket──▶ connector HANDLERS[action] ──▶ 一次 eda.sch_* 调用 ──▶ getState_* 序列化结果
```

PCB **沿用同一条链路,原则不变**:typed actions(新增 `pcb` Domain)→ Go daemon(除目录外**零改动**)→ connector(`HANDLERS` map 追加 `pcb.*`)→ `eda.pcb_*` 命名空间。

关键证据 —— `pcb_PrimitiveComponent` 与 `sch_PrimitiveComponent` 同形:

| 契约 | schematic | PCB | 行号 |
|---|---|---|---|
| create | `sch_PrimitiveComponent.create` | `pcb_PrimitiveComponent.create({libraryUuid,uuid}\|footprint, layer, x, y, rotation?, lock?)` | 10517 |
| modify | ✓ | `modify(id, {x,y,rotation,layer,lock,designator,addIntoBom,...})` | 10553 |
| delete/get/getAll | ✓ | `delete`/`get`/`getAll`/`getAllPrimitiveId` | 10532/10578/10605/10596 |
| 引脚 | `getAllPinsByPrimitiveId` | `getAllPinsByPrimitiveId` | 10613 |
| 句柄 | `getState_PrimitiveId()` | `getState_PrimitiveId()` | 4217 |
| 两种编辑风格 | one-shot modify / setState_*→done | 同 | 11013-11048 |

因此 handler 主体(一次 `eda.pcb_*` 调用 + `edaError→ActionError(detail)` 包装 + `getState_*` 序列化 + primitiveId 作句柄 + 改前重取 ID)**机械式移植**即可。Daemon 的 `persistArtifacts` 已能处理任意 base64 blob(gerber/drill/pnp 直接接入),路由/分发**无需任何改动**。

**已确认连接器天然识别 PCB 文档**:`EDMT_EditorDocumentType.PCB = 3`(index.d.ts:51),且 `document.current` 已返回 `documentType`/`documentTypeCode`(actions.ts:167-168)。

### PCB 与原理图的本质差异(必须重新设计的地方)

1. **单位 / 坐标**:PCB 数据单位 = **1 mil**(原理图为 10 mil / 0.01in)。仍然 **y-UP**(一致)。但 PCB 引入**数据原点 vs 画布原点**的分裂(原理图无此概念)——需在会话开始 `setCanvasOrigin(0,0)`(@4486),并用 `convertDataOriginToCanvasOrigin`(@4445)换算;`sys_Unit` 的 mil/mm 换算是**同步**函数(@1742-1787)。
2. **层**:每个走线 / 铺铜 / 填充 / 区域图元都**绑定层**;元件放置**必须带层参数**(TOP/BOTTOM,无"无层放置")。这是原理图完全没有的新维度。
3. **连接模型**:PCB **没有网络标志(net flag)**。电源/地是"铜上的网络名 + 飞线",所以 `connect_pin`、`orientation.json`、`deriveBodyRotation`、12 数字 frozenTable **全部不迁移**;布线是 pad 之间的 track 段。
4. **设计规则 / DRC**:规则配置与违规对象是**不透明 `{[key:string]:any}` / `Array<any>`**(`check` @4687),不像原理图 DRC 有结构化归一。
5. **图元体量**:一块板的图元数量比一页原理图高一到两个数量级,冲击 linter 的"单次 exec_js 整拉"策略。
6. **新桥接概念**:原理图→PCB 同步(`importChanges` @4366 + `dmt_Board.createBoard` @792),原理图侧无对应物。
7. **新自动化面**:`autoRouting`/`autoLayout`(@4590/@4597,@alpha)。
8. **镜像**:元件**不支持左右镜像**,只有 TOP/BOTTOM 翻面(`setState_Layer` @10900)。

---

## 2. 操作 → API 映射表

| 操作 | 关键 `pcb_*` 方法(行号) | 支持级别 | 说明 |
|---|---|---|---|
| **布局 / placement** | `pcb_PrimitiveComponent.create`(10517) / `modify`(10553) / `delete`(10532) / `getAll`(10605) / `getAllPinsByPrimitiveId`(10613);`lib_Footprint.search`(2593);`dmt_Pcb.createPcb`(155) | **native-api** | 直接镜像原理图。footprint 搜索结果可直接喂 create()。**必须带 TOP/BOTTOM 层**;无元件镜像,仅翻面。改前重取 ID(无 undo)。|
| **布线 / routing(手动 track/arc)** | `pcb_PrimitiveLine.create`(6859,分段、共端点自动合并) / `pcb_PrimitivePolyline.create`(9248,一次成多段) / `pcb_PrimitiveArc.create`(7191);走线遍历 `getEntireTrack`(7146)/`getAdjacentPrimitives`(7138);`pcb_Layer.selectLayer`(3569) | **native-api** | 两种风格:逐段 Line(类似 `sch_PrimitiveWire`)或一次性 Polyline。网络是 create() 的**每图元参数**——**没有"当前布线网络"模式 setter**(不像层有 selectLayer),由连接器组合。|
| **排线 / 总线·并行·扇出·等长** | 组合 `Line/Polyline.create` + `Via.create`(6541);`pcb_Drc.createEqualLengthNetGroup`(4995);`createDifferentialPair`(4937) | **scriptable** | **无**专用总线/扇出/推挤/蛇形生成器。等长组、差分对的**约束定义**是原生的;但蛇形/手风琴的几何**须自算**后发普通 Line/Polyline。诚实止点:自动等长调节 = manual-only。|
| **自动布线 / autorouting** | 内置:`pcb_Document.autoRouting`(4590)/`autoLayout`(4597)/`clearRouting`(4567)(均 @alpha);外部环:`getDsnFile`(6150)→Freerouting→`importAutoRouteSesFile`(4384)/`importAutoRouteJsonFile`(4375)(@beta、已发布文档) | **scriptable** | 稳定路径是 **DSN 出 / SES 入** 外部环。内置一键 autoRouting 为 @alpha、不在公开文档、官方称"效果一般",可能被生产构建屏蔽——视为 exec_js 探测的实验功能。|
| **铺铜 / pour & fill** | `pcb_PrimitivePour.create`(8905) + 实例 `rebuildCopperRegion`(9207,真正灌铜);`getCopperRegion`(9200);`pcb_MathPolygon.createPolygon`(3906)/`createComplexPolygon`(3914);`pcb_PrimitiveFill.create`(9520);`pcb_PrimitiveRegion.create`(8609,禁布区) | **native-api** | create() 只建**轮廓**,必须 `rebuildCopperRegion()` 才灌铜。Poured 只读(`PrimitivePoured.create/modify` 是 no-op @8404/8420)。**无全局重灌**——遍历 `getAll`(8969) 逐个 rebuild(封装成 composite)。|
| **via / 过孔** | `pcb_PrimitiveVia.create`(6541) / `modify`(6558) / `delete`(6549) / `getAll`(6603) / `get`(6576);`getAdjacentPrimitives`(6835) | **native-api** | viaType = VIA/BLIND/SUTURE(@6512)。盲埋孔跨层由**命名设计规则** `designRuleBlindViaName` 决定,**非**显式层参数。注意 `getAllPrimitiveId` 无 layer 参数。|
| **对齐 / align & distribute** | **无 API**。自算:`pcb_Primitive.getPrimitivesBBox`(4179) + `getState_X/Y`(10784) + `modify({x,y})`;选择 `doSelectPrimitives`(12169) | **manual-only** | 诚实止点:**无元件对齐/分布方法**,`EPCB_PrimitiveStringAlignMode` 仅文本锚点。建议做成**确定性 typed 复合动作**(读 bbox→算→写绝对 x/y),可单测,不需 exec_js。|
| **网格 / grid & snap** | **无 API**。`SYS_Setting` 仅 `restoreDefault`(20690)。自算:坐标取整到选定网格;`setCanvasOrigin(0,0)`(4486) | **manual-only** | 诚实止点:v0.2.63 中无 `setGrid/gridSize/snapToGrid`。编辑器交互网格不可触达。实践答案:**agent 自己掌控坐标精度**,在放置/布线 handler 内置网格取整(如 5/25 mil)。|
| **layer / 层 + 叠层** | `selectLayer`(3569)、`getCurrentLayer`(3561,**同步**)、`getAllLayers`(3689)、`setLayerVisible/Invisible`(3578/3587)、`lockLayer/unlockLayer`(3595/3603)、`setTheNumberOfCopperLayers`(3612)、`addCustomLayer/removeLayer/modifyLayer`(3658/3667/3677)、`setPcbType`(3651)、物理叠层 get/save/overwrite(3711-3786) | **native-api** | 层 / 铜层数 / 自定义层 / 叠层全可控。叠层(阻抗)是不透明 `{[key]:any}`(alpha),按名读写。|
| **DRC** | `pcb_Drc.check`(4687,verbose→`Array<any>`);实时 DRC `start/stop/getStatus`(4712/4720/4704,同步);规则 get/save/overwrite(4734-4813,不透明);`createNetClass`(4885)/`createDifferentialPair`(4937)/`createEqualLengthNetGroup`(4995);事件 `addRealTimeDrcResultEventListener`(5245) | **native-api** | 跑 DRC + 网络类/差分/等长 CRUD 原生。但规则与违规是**不透明 blob**——连接器须把 `Array<any>` **归一化**(原理图 `schematic.drc.check` 已做同样的事)。|
| **原理图→PCB 同步** | `pcb_Document.importChanges`(4366,**@public 稳定**);前置 `dmt_Board.createBoard`(792)/`getCurrentBoardInfo`(832);`startCalculatingRatline`(4415)/`stop`(4422) 刷新飞线 | **native-api** | 唯一全新概念,但稳定。floating PCB 上 importChanges 返回 false——须先用 Board 关联 SCH+PCB。封装成 `pcb.import_changes`,先确保 Board 再导入并 readback 验证。|
| **制造导出 / mfg-export** | `getGerberFile`(5698,钻孔内嵌)、`getPickAndPlaceFile`(5805)、`getBomFile`(5944)、`get3DFile`(5757)、`getNetlistFile`(5998)、`getDxfFile`(6009)、`getOpenDatabaseDoublePlusFile`(ODB++ 6115)、`getIpc2581CFile`(6085)、`getDsnFile`(6150)、`placePcbOrder`(6316) | **native-api** | 与原理图导出同套 plumbing(返回 `Promise<File>`,daemon 已写盘)。诚实止点:**无独立 NC/Excellon 钻孔**——钻孔仅内嵌于 Gerber/ODB++ 的 `other.*` 标志。|

---

## 3. 架构改动清单(具体)

- **Go 目录**(`internal/protocol/actions.go:5-12`):枚举加 `DomainPcb Domain = "pcb"`,`Phase1Actions()` 追加约 20 条 `pcb.*` spec。`dispatch.go` 的 `knownActions` 自动派生——**daemon 零路由改动**。复用 `Mutates/NeedsWindow/NeedsConfirm/Phase/VerifyWith`(如 `pcb.component.delete`、`pcb.clear_routing`、`pcb.pour.delete` 都置 `NeedsConfirm=true`)。
- **连接器能力标签**(`extension/src/protocol.ts:10`):`CAPABILITIES = ['schematic.v1']` → 加 `'pcb.v1'`。transport(端口扫描 49620-49629、握手、心跳重连)与帧协议**无原理图语义,原样复用**。
- **连接器 handlers**(`extension/src/actions.ts:817`):向 `HANDLERS: Record<string,Handler>` 追加 `pcb.*`;`runAction` 已按 `HANDLERS[action]` 派发。新增 PCB 版 `serializeComponent/serializePrimitive`(镜像 actions.ts:59-83)。
- **PCB 文档守卫**:`document.current` 已暴露 `documentType`——`NeedsWindow` 守卫可断言"当前文档是 PCB",小 helper,无新 transport。
- **新单位/坐标模块**(原理图无对应):统一 PCB 数据单位 = mil;会话开始 `setCanvasOrigin(0,0)`;暴露 `sys_Unit.mmToMil/milToMm`(同步);约定所有 `pcb.*` 的 x/y 为 mil、y-up、已网格取整。
- **新约定文档** `skills/easyeda-conventions/references/pcb-layout-conventions.md`:默认线宽/孔径(按 net-class)、层分配、网格步距、禁布区策略、丝印朝向。**无 netflag/orientation.json 对应物**。
- **新数据型 linter** `skills/easyeda-schematic/scripts/pcb-lint/`:原样复用 schematic-lint 架构(单次 exec_js 整拉 → Python 确定性检查 → 稳定 `{rule,msg}` → git 版本化 baseline/diff),但规则换成 PCB:未布线飞线、间距/clearance、via-in-pad、courtyard 重叠、off-grid、悬空/零长 track、酸角、铜渣。probe **必须按 net+layer 分页**(`getAllPrimitiveId` 过滤),因图元体量可能超过 1MB Request 上限 / 60s dispatch 超时。
- **制造导出**:`persistArtifacts`(dispatch.go:120-166)对 gerber/drill/pnp/ODB++/STEP/DSN 原样复用。
- **SKILL 脚手架**复用(health → 读上下文 → 巡查 → 小步增量 → 每次改动 readback/snapshot/DRC 验证 → 破坏性/保存需确认 → 护栏);并强化审计日志 before/after 规则——在 PCB 上更关键(`clearRouting('all')`、`autoRouting(REMOVE)`)。

---

## 4. 风险与缓解(按严重度)

| 风险 | 级别 | 缓解 |
|---|---|---|
| 内置自动布线 @alpha、不在公开文档、可能被生产屏蔽;客户端 ^0.2.21 与 vendored 0.2.63 可能不一致 | **高** | v1 不依赖内置自动布线;先做确定性手动/辅助布线 + 稳定的 DSN/SES 外部环;`autoRouting()` 仅在 exec_js 运行探测成功后才 feature-gate 开放。|
| **无编程 undo**(全 API 0 处 undo/transaction)——PCB 上更危险(clearRouting/autoRouting-REMOVE/删铜/批量删元件不可逆) | **高** | 所有破坏性 `pcb.*` 置 `NeedsConfirm=true`;批量操作前把 `getAll()` 状态快照进审计日志(before/after);恢复靠 delete+recreate;改前重取 ID。|
| 对齐/分布/网格/吸附**无 API** | **中** | 做成**确定性 typed 复合动作**:`getPrimitivesBBox` + `getState_X/Y` 计算 → `modify({x,y})`。可单测、无需 exec_js。编辑器交互网格视为范围外,由 agent 掌控坐标。|
| DRC 规则配置与违规是不透明 blob | **中** | 连接器内做违规**归一化器**(原理图已有同款);规则编辑靠 round-trip 捕获的 blob,不臆测字段;DRC 看不到的交给 pcb-lint。|
| 图元体量冲击单次 exec_js 整拉 + 1MB Request 上限 + 60s 超时 | **中** | probe 按 net/layer 分页;返回摘要而非全几何;大读分块多动作;针对密板尽早压测、必要时调上限/超时。|
| importChanges 需 Board 关联(floating PCB 返回 false);"同时更新走线网络"无文档 | **中** | 封装 `pcb.import_changes`:先 `createBoard`/`getCurrentBoardInfo` 确保关联,暴露 false 原因,readback + 飞线重算验证。importChanges 本身 @public 稳定。|
| @alpha/@beta 漂移、SDK 版本差 | **中** | 非 @public 方法升级为 typed 动作前先 exec_js 运行探测;握手上报客户端 API 版本;以真实构建为准而非 .d.ts。|
| 元件无镜像 / 无全局重灌 / 无相对移动 | **低** | 翻面用 `setState_Layer`;`pcb.pour.rebuild_all` 复合枚举;移动增量自算(读 x/y→写绝对)。真镜像极少用,exec_js 兜底。|

---

## 5. 自动布线裁决

**可行但不具契约可靠性——先发手动布线,自动布线作为实验/外部能力。** 内置 `autoRouting(props?)`(@4590)与 `autoLayout()`(@4597)存在于类型,但 @alpha、不在公开文档、官方"效果一般"、可能被构建屏蔽,且运行客户端可能与 vendored 类型不一致。**生产安全路径是外部布线环(今天即可脚本化)**:`getDsnFile`(6150)/`getAutoRouteJsonFile`(6166) → 外部 Freerouting/JRouter/ELECTRA → `importAutoRouteSesFile`(4384)/`importAutoRouteJsonFile`(4375)(均 @beta 且已发布),`clearRouting`(4567) 清场。**建议**:v1 提供确定性手动/辅助布线(`pcb.track.create`、`pcb.via.create`、`pcb.route_net` 复合)+ DSN/SES 桥作为"自动"选项;内置 autoRouting/autoLayout 用运行探测 gate 住、标注实验。自动布线**不是 v1 阻塞项也不是主价值**——布局 + 手动布线 + 铺铜 + DRC + 制造导出全是 native-api,应聚焦于此。

---

## 6. 推荐分阶段计划

- **Phase 0 基础(无变更)**:加 `pcb` Domain + `pcb.v1` 能力;pcb-coords helper(`setCanvasOrigin(0,0)` @4486、mil 约定、y-up、`sys_Unit` 换算);只读动作 `pcb.document.current`/`pcb.components.list`(getAll @10605 + getAllPinsByPrimitiveId @10613)/`pcb.nets.list`(`pcb_Net.getAllNets` @6382 / `getAllNetsName` @6397)/`pcb.layers.list`(getAllLayers @3689)/`pcb.primitive.get`(@4163)/`pcb.snapshot`。未 typed 的先走 `debug.exec_js`。
- **Phase 1 巡查 + linter**:`skills/easyeda-schematic/scripts/pcb-lint/`(复用 schematic-lint 架构,按 net/layer 分页防图元爆量);规则:未布线飞线、clearance、via-in-pad、courtyard 重叠、off-grid、悬空/零长 track。写 `skills/easyeda-conventions/references/pcb-layout-conventions.md`(按 net-class 的线宽/孔径默认、层分配、网格步距、禁布区策略)。
- **Phase 2 布局(增量改动)**:`pcb.component.place`(create @10517,带 layer/x/y/rotation,网格取整)、`pcb.component.modify`(移动/旋转/翻面/锁 @10553)、`pcb.component.delete`(需确认)、`pcb.select` / cross-probe(`doSelectPrimitives` @12169 / `doCrossProbeSelect` @12181);确定性 `pcb.align` + `pcb.distribute`(`getPrimitivesBBox` @4179 + modify x/y);`lib_Footprint.search` @2593 找封装。
- **Phase 3 原理图→PCB 桥**:`pcb.board.ensure`(`dmt_Board.createBoard` @792 / `getCurrentBoardInfo` @832)、`pcb.import_changes`(`importChanges` @4366 + `startCalculatingRatline` @4415,readback 验证)。产出"已布局未布线"的板,是稳定的 @public 同步路径。
- **Phase 4 手动布线 + 过孔**:`pcb.track.create`(`pcb_PrimitiveLine.create` @6859 逐段 或 `pcb_PrimitivePolyline.create` @9248 一次成)、`pcb.via.create`(@6541)、`pcb.route_net` 复合(pad→段→换层过孔)、`pcb.layer.select`(@3569)、`pcb.clear_routing`(@4567,需确认);等长/差分组创建(@4995/@4937)。
- **Phase 5 铺铜 + 禁布**:`pcb.pour.create`(`createPolygon` @3906 → `PrimitivePour.create` @8905 → `rebuildCopperRegion` @9207)、`pcb.pour.rebuild_all`(遍历 getAll @8969)、`pcb.region.create` 禁布(@8609)、`pcb.fill.create` 静态(@9520)。
- **Phase 6 DRC**:`pcb.drc.check`(@4687,归一化 `Array<any>`)、实时 DRC 开关(@4712/4720)、net-class/diff-pair/equal-length 创建;结果回灌 linter baseline/diff。
- **Phase 7 制造导出**:`pcb.export.gerber`(@5698,钻孔内嵌)/`pnp`(@5805)/`bom`(@5944)/`3d`(@5757)/`netlist`(@5998)/`odb`(@6115)/`dsn`(@6150),复用 persistArtifacts。
- **Phase 8 实验自动布线(可选,最后)**:运行探测 `autoRouting`/`autoLayout`(debug.exec_js)并 feature-gate;DSN 出 / SES 入外部环(`getDsnFile` @6150 → Freerouting → `importAutoRouteSesFile` @4384)作可靠"自动"路径。

**贯穿全程**:任何未 typed 的操作先用 `debug.exec_js` 跑通,再把高频项升级为 typed 动作——与原理图特性同样的成熟路径。

---

## 7. 待澄清问题(开工前需实测确认)

1. 真实运行的 EasyEDA 客户端(pin `^0.2.21`,非分析所用 0.2.63)到底是否暴露 `autoRouting/autoLayout/clearRouting`,能否执行?须 exec_js 探测。
2. 密板上 daemon 的 1MB Request 上限 + 60s 超时的真实图元数天花板?是否需要流式/分页读路径供 linter probe?
3. DRC 违规对象(`check(...,true)→Array<any>`)与规则/物理叠层 blob 的真实结构?须从实板捕获以设计归一化器与规则编辑动作。
4. `importChanges` 是否更新**已存在 track 的网络**,还是只增删元件/网络?"同时更新走线网络"无 API 文档,须实测 readback。
5. `pcb-layout-conventions.md` 应规定的网格步距、按 net-class 的默认线宽/孔径?
6. 是否有可靠方式把飞线/未布线连接当作端点对象枚举?(`getAllPrimitivesByNet` @6439 给网络成员、autoRouting 给 failedNets,均非"剩余飞线")——linter 的未布线规则依赖于此。
7. 对齐/分布/网格应做成 typed 确定性动作(推荐)还是留给 exec_js?需 owner 确认是否升级为一等动作。

---

## 附:可靠性分级与来源

- **API 行号**:对照 vendored `extension/node_modules/@jlceda/pro-api-types/index.d.ts`(v0.2.63),经多代理逐行对抗式验证。
- **发布阶段标记**:`@public`(`importChanges`)= 生产就绪;`@beta`(`importAutoRouteSesFile`、create/modify/getAll、create-board)= 已发布文档、相对稳定;`@alpha`(`autoRouting`、`autoLayout`、`clearRouting`、`rebuildCopperRegion`、物理叠层)= 最新、未进公开文档、需运行探测。
- **官方文档**:[自动布线](https://prodocs.lceda.cn/cn/pcb/route-auto-routing/)、[自动布局](https://prodocs.lceda.cn/cn/pcb/layout-auto-layout/index.html)、[pcb_Document API 参考](https://prodocs.easyeda.com/en/api/reference/pro-api.pcb_document.html)、[导入变更](https://docs.easyeda.com/en/PCB/Import-Changes/index.html)、[pro-api-sdk](https://github.com/easyeda/pro-api-sdk)。
