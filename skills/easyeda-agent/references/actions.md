# EasyEDA Action Reference

Run `easyeda actions` for the authoritative machine-readable list.

## Playbook 回放(`easyeda sch apply`)— 批量步骤的首选载体

> **多步批量操作(>~5 步)不要再写 shell/python 胶水脚本**——写成 playbook JSON,
> `easyeda sch apply` 按步执行,自带变量捕获、门禁、journal 断点续跑。
> 完整格式与错误处理语义见 `docs/design-apply-playbook.md`(单一真源)。

```bash
easyeda sch apply steps.json                    # 顺序执行(meta.project 定目标工程)
easyeda sch apply steps.json --dry-run          # 预检 + 打印计划,不执行
easyeda sch apply steps.json --project demo2    # CLI flag > 文件(同一份打到另一工程)
easyeda sch apply steps.json --var LIB=<uuid>   # 变量复写(参数化)
easyeda sch apply steps.json --resume           # 按 journal 跳过已完成步骤(恢复 captured 变量)
easyeda sch apply steps.json --from 12 --to 30  # 区间执行
easyeda sch apply steps.json --yes              # 放行确认门控(delete/clear/rip-up/import 类)
```

要点(实现与设计一致,已单测+真机验证):
- 每步 `action:`(typed action)或 `run:`(Cobra 子命令,如 `pcb auto-place`)二选一;
  `notify:` 弹 toast。`payload/flags/args` 内 `${VAR}` 替换。
- `capture: {"U1": "$.primitiveId"}` 把结果存变量给后步用(id 跨会话会变,这是复现的命门)。
- `assert: {"$.score": ">=95", "$.overlaps": "==0"}` = 门禁,不过即停(`onFail: stop` 默认)。
- **错误纪律**:失败即终止;只读步骤自动重试 2 次;**变更类步骤超时不自动重试**
  (mutation 可能已生效——先读回校验再 `--resume`);变更步骤可带 `verify:` 读回块自证。
- journal 头带 playbook sha,文件改动会拒绝 `--resume`(改用 `--from`)。
- **dry-run 纯计算铁律(ADR-0004)**:所有 `--dry-run` **机械保证零 mutation** ——
  dry-run 模式下派发层直接拒绝任何 Mutates 动作,预览绝不落件;可以放心把
  dry-run 当纯只读预演用。

**录制导出**:`easyeda audit export --playbook --day 2026-07-03 --since 15:17 --until 15:19
-o replay.json` 把真实会话(审计日志)提取成 playbook——只留变更步骤、自动挤压 autosave
风暴、**自动接线 capture/${var}**(后步引用前步 result.primitiveId 时);引用「窗外出生」
裸 id 的步骤会标 `raw-id` 警告(只能对同一板态回放,先 review)。⚠️ 提取物可能含
rip-up/clear 等破坏性步骤——整册回放前先 `--dry-run` 看计划,或用 `--from/--to` 只放安全区间
(已实证:esp32 移件段 18 步区间回放,幂等,lint 保持 100)。

## Navigation

- `system.health` — daemon + connector 可用性，已连接窗口列表
- `project.current` — 当前工程 uuid / name / teamUuid
- `document.current` — 当前激活文档 uuid / tabId / documentType
- `document.open` — 按 UUID 打开任意文档（原理图或 PCB）
- `schematic.pages.list` — 工程内全部原理图及页面
- `schematic.page.open` — 切换到指定原理图页（兼容旧用法）

## Sheet / 图页管理 + 明细表（title block）

均映射 `eda.dmt_Schematic.*`。**注意：EasyEDA Pro 无设置纸张尺寸(A4/A3)的公开 API**；可编辑的「图纸」属性就是明细表(title block)。CLI：`easyeda sch …`。

- `schematic.titleblock.get` — 读当前（或指定 `pageUuid`）图页的明细表：`showTitleBlock` + 各字段 `titleBlockData`。**改前先 get 拿到字段 key** → `easyeda sch titleblock-get`
- `schematic.titleblock.modify` — 调整明细表:显隐 + 字段值(只传要改的项)→ `easyeda sch titleblock --show` / `--data '{"Name":"电源模块","Drawed":"张三"}'`。**✅ 2026-08-26 解禁**(此前 2026-08-17 起禁用)。当时的两个理由都已消除:
  - **「写路径损毁图框」** —— 真因是**整包回传**:`titleblock.get` 返回的 `Device`/`Symbol` 的 value 是符号**名字**,整包传回 `modify` 会被平台灌进 sheet 的 component/device/symbol **UUID 引用位** → 报「器件/符号属性有误」→ 重启拒载(#186)。现在**只传你点名的项**,连接器侧还有一道结构键过滤兜底(图框身份/纸张几何/开关/`@`投影项一律不下发,真想改会被 `PRECONDITION_REFUSED` 零变异拒绝)。
  - **「写不进去」** —— 是**回读太早**的误报:平台提交明细表是异步的,写完立刻读拿到旧值,于是把成功报成 `nothing was applied`。现在回读轮询到落定。
  真机验收:一条 `sch titleblock --data` 三项全写入,`Border`/`Title Block` 保持 `"1"`,sheet UUID 完好。
  ⚠ 仍然**不要**用 exec_js 绕过连接器整包写明细表 —— 那条路依旧毁图框。换图框/改纸张不是明细项:走 `sch prim-delete --allow-sheet` + `sch place` 重放 `Drawing-Symbol_A4`。
  - **平台会对写不进去的字段返回成功**（官方 remarks 原文：「无法识别的明细项将被忽略」且「仍将返回 `true`」，与「删除 API 撒谎」同族）。handler 因此**改前快照 → 写 → 回读逐项比对**，产出 `applied`/`alreadySet`/`notApplied`/`unknownKeys`；全部落空即 ERROR，部分落空回 `partial:true` + warnings，CLI 非零退出（#151 三态约定）。
  - **`unknownKeys` = 这些根本不是本页的明细项**，修法是换 key 而不是重试 —— 先 `sch titleblock-get` 看可用 key。**明细表改不了纸张尺寸**：曾有 20 次调用拿 `Size`/`Width`/`Height`/`Page Size` 当纸张属性写，全部失败（audit 实测该 action 一度 32 次调用 0 次成功）。
  - **只能改当前聚焦页** —— 官方签名无 `pageUuid` 参数（`titleblock.get` 反而支持，两者不对称）。改之前先确认聚焦页就是目标页。
- `schematic.page.create` — 新建图页（`schematicUuid`）→ `easyeda sch page-new --schematic <uuid>`
- `schematic.page.rename` — 重命名图页 → `easyeda sch page-rename --page <uuid> --name ...`
- `schematic.page.delete` — 删除图页（**需确认**，无 undo）→ `easyeda sch page-delete --page <uuid>`
- `schematic.rename` — 重命名整张原理图文档（非单页；可能联动复用模块符号 + PCB）→ `easyeda sch rename --schematic <uuid> --name ...`

## View（画布视图快捷键，原理图 + PCB 通用）

作用于当前聚焦的画布，等价于编辑器工具栏/快捷键。CLI：`easyeda view …`。

- `view.fit` — 适应全部（`K` 快捷键）；缩放至显示全部图元 → `easyeda view fit`
- `view.fit_selection` — 适应选中；先 `schematic.select` 再缩放至选中图元 → `easyeda view fit-selection`
- `view.zoom` — 缩放到坐标/比例（x/y/scale，scale 为百分比，省略则保持当前值）→ `easyeda view zoom --scale 200`
- `view.region` — 缩放到矩形区域（left/right/top/bottom，单位：原理图 0.01inch、PCB mil）→ `easyeda view region --left 0 --right 1000 --top 1000 --bottom 0`

## Inspect Schematic

- `schematic.components.list` — 当前页（或全页）所有元件，可含 pins
- `schematic.select` — 按 primitiveId 选中图元
- `schematic.export.image` — **看局部原理图的首选**（#166）。把当前页、或**只把指定图元**渲染成 **SVG / PNG / PDF**：`easyeda sch export-image --ids id1,id2 --out block.svg`（省略 `--ids` 导整页；`--format` 默认 svg；`--scope selection|page|project`；`--page` 定位页）。给了 `--ids` 就自动选中并**把画布裁到选区**（实测 3 个器件 → 283×155，整页是 1191×846）。**比 `view region` + `snapshot` 可靠**：不走视口，所以不受「后台标签页不重绘 → 截回上一帧整页」的影响；SVG 还是矢量，放大看密集接线不糊。⚠️ 底层 `getExportDocumentFile` 的 `object` 字面量**官方类型定义是错的**，传错不报错、直接让 promise 永远悬着（编辑器卡在 1% 进度条）——真值已固化在 handler 里，**不要照 .d.ts "修正"**；handler 另有 30s 超时兜底。
- ~~`schematic.snapshot`~~ — **已移除(2026-08-12)**:视口截图易 stale,原理图出图统一走 `sch export-image`(整页或 `--ids` 选区,渲染文档数据、无视口依赖)。**落盘绝对路径**在 `result.artifactPath`(所有产 artifact 的动作统一如此,含 pcb snapshot/BOM/网表导出),stderr 另有 `📎 artifact saved: <path>` 一行 —— 读图直接用这个路径

## Mutate Schematic

- `schematic.component.place` — 从库放置元件（libraryUuid + uuid + x/y）。放置后自动把 `supplierId` 回填为 device 的**真立创 C 号**（平台 create 默认填成 `<MPN>.1` 的 subPartName，会让官方「器件标准化」面板全标红、BOM Supplier Part 不可下单，#157）；device 无 C 号（外采占位件）不动。**同时回填器件属性值**（`Value` / `Tolerance` / `Voltage Rating` / `Datasheet` / `Description` …，#186）：平台 create 只把 device 的属性**键**复制到实例、**值全是空的**，于是 BOM 值列和「器件标准化」面板一路空着，要等 PCB 侧 `sync-attrs` 才补 —— 而那份 device 记录在放置当刻就在手里。回执给 `otherPropertyBackfilled: [...]`。规则（与 PCB `sync-attrs` **同一把尺**）：只填实例上**已有且为空**的键（不新增键 ⇒ 结构上不可能把库占位漏进来）、不覆盖已有值、幂等；**投影键永不写**（库记录自带占位 `Designator: "C?"`，平台会把它同步进位号 —— 曾把 166/166 真位号洗成 `U?/C?`），且写入时**同一 call 重新断言 designator + supplierId**（整包 otherProperty 写会让平台重新投影顶层字段，不重新断言就会把 #157 刚填好的 C 号打回 `<MPN>.1`——真机回读抓到过，回执还谎称成功）。两项回填都是 best-effort：失败只出 warning，绝不让放置失败。⚠️ **超时不等于没落地(假失败定律)**:`connector did not respond` 时器件通常**已经建在画布上**,只是回执丢了 —— 直接重发会造重复件。`sch block-apply` 已自带**收编**(放置前快照 id → 失败后 settle 回读 → 认「不在快照里 + componentType==part + 落在下发坐标 ±5」的那个件,进 `rollback.adoptedPrimitiveIds` 并照常删除;绝不凭空造 id,也永不碰页面上原有的同型同坐标器件)。**「认不出」分两种,别混**(2026-08-20 修):回读被证明新鲜 → `adopt ✓ …确实没有落地`,可以直接重跑;回读**没被证明新鲜** = 不可信 → `adopt ? …无法判断` + PARTIAL STATE,**此时绝不能读成「没落地」**。旧版少了这道新鲜度门,真机上把「C8 明明就在 (440,535)」报成了「确实没有落地,页面上没有残件」。**新鲜度分两档**(连接器 FIFO 上线后):**算术档(强证据)**——连接器每条响应带 `seq`/`seqAbandoned`,回读期间 `seqAbandoned` 没变 = FIFO 保证这次回读的 handler 在那次 place 的 handler settle 之后才开跑,**连探针都不需要**(第一件就超时也能出结论);`seqAbandoned` 变了 = 有 handler 被放弃且仍在跑、效果可能稍后才落地 → 一律 uncertain。**探针档(弱证据)**——连接器比 CLI 旧、响应不带这些字段时退回旧启发式(本命令此前已落地的器件必须一个不缺地出现在回读里),报文会标 `证据档:弱(探针启发式)` 并给升级步骤;**绝不因为缺字段就默认新鲜**。⚠️ `seq` 证明的是 **handler 边界**的先后,**不是**「文档已提交」——「确实没有落地」的准确含义永远是「在可证的最新一刻那里没有新器件」。**手工调 place 时同样办法**:先 `sch list` 对比坐标确认落没落地,再决定重发还是删除,别盲重试。收到 `ACTION_ABANDONED` = 这次写没有已知完成时刻,盲重发会造重复件;收到 `QUEUE_OVERFLOW` = 该动作**根本没执行**,消化积压后重发是安全的。
- `schematic.text.list` — **只读**枚举当前激活页全部文本图元（id/content/x/y/rotation/fontSize/color），配 `sch prim-delete --ids` 清理孤儿 zone-draw 标签，免走 `debug exec` 逃生舱（#156）。页懒加载定律：只见激活页，多页工程用 `--page` 逐页扫。CLI：`easyeda sch text-list [--page P2]`
- `sch note`（CLI 内部 exec_js，同 zone-draw 惯例，无新 action）— **放电路说明文本**（分页分区+电路说明三件套的第三件，布局默认必做）：`--text`（`\n` 换行）`--x/--y`（y-UP）`--font-size`（默认 10）`--color`（默认 #5A5A5A）。创建后回读 primitiveId 验证 + 显式 save；枚举 `sch text-list`、清理 `sch prim-delete`。CLI：`easyeda sch note --text "LDO: 5V→3V3 1A\n输入/输出各 100nF" --x 700 --y 300 --doc P2`
- `schematic.component.resolve_lcsc` — **确定性「已放置器件→真 C 号」解析（#158）**。精确匹配链（实例 C 号 → MPN 严格相等 → 工程库名），**匹配的封装必须与实例一致**（唯一命中但封装不同 = 封装变体不符，照样进 unresolved）——绝不模糊兜底取 r[0]（真机事故：裸 search 把 U.FL 天线座解析成 C1017 磁珠）。默认 dry-run 报告；`--apply` 把解析出的 C 号写回 supplierId 非 C 号形状的实例（整板 supplierId 修复一条命令）。unresolved 附候选列表供人工确认。同 MPN+封装缓存；只扫激活页，多页 `--page` 逐页。CLI：`easyeda sch resolve-lcsc [--apply] [--page P2] [--id <pid>]`
- `pcb.component.attrs_backfill` — **PCB 器件属性回填（器件标准化 PCB 侧）**。平台 sch→PCB 导入把 otherProperty 建成**键在值空**（Value/耐压/精度/Datasheet 全 ""），且原理图实例属性值 save/reload 后同样为空（不可作源）——唯一稳定源是 **device 库记录**：按实例 C 号 `getByLcscIds` 解析，只填 PCB 侧空值键（手改值优先，`--overwrite` 强制），全程 PCB 前台。无 C 号器件跳过并报告。`pcb import-changes` 成功后**自动跑**（`--no-sync-attrs` 关）。⚠️ **平台投影键绝不参与 merge**（`Designator`/`Unique ID`/`Name`/`Add into BOM`/`Manufacturer*`/`Supplier*`——它们存在顶层图元状态；库记录的 `Designator:"C?"` 占位键灌进实例会被平台同步成图元位号,一板位号全灭 = 166/166 U? 事故真因,2026-08-09 根治）。CLI：`easyeda pcb sync-attrs [--overwrite]`
- `pcb sync-designators`（`pcb.components.list` + `pcb.component.modify` 编排,无新 action）— **修占位位号**（`U?`/`C?`）：按 `uniqueId`（平台首次导入铸造、跨文档同一命名空间）从原理图回填。只动占位符（手设真实位号绝不覆盖）；每笔回读验证；修完立落 `pcb.save` 检查点；原理图侧同为占位符的件归类「先标注原理图」。`--dry-run`/`--json`（Failed>0 非零退出）。`import-changes` 后自动**殿后**跑（在 attrs 之后,`--no-sync-designators` 关）。CLI：`easyeda pcb sync-designators`
- `schematic.component.modify` — 修改位置、位号、BOM 属性等
- `schematic.component.delete` — 删除元件（需确认）。**级联清理独占桩线/flag**（ADR-0004 Decision 5：只挂在被删件引脚上的桩线树+旗随件删净，**共享树只断不删**；回读证实，结果带 `cascaded` 字段列明细；payload `cascade:false` 退回旧行为）
- `schematic.wire.create` — 创建导线折线
- `schematic.netflag.create` — 创建电源/地/网络端口/短路 flag
- `schematic.power.connect_pin` — 复合操作：从 pin 拉导线 + 在末端放 flag（防止 flag-on-pin DRC fatal）
- `schematic.pin.disconnect` — `connect_pin` 的逆操作：把某 pin 的 stub 导线**连同**末端 netflag/netport 一并删除，避免只删 flag 留下孤儿 stub（EasyEDA 会给残留 wire 分配 `$3N…` 自动网名，`sch check` 现已能识别报 WARN）。按 `--pin U1:5`、`pinX`/`pinY` 坐标(`sch autoconnect --replace` 换网时用)或 `--flag-id`/`--wire-id` 定位。**合并树感知(0.15.1/#137)**：定位到的线可能是 EasyEDA 合并后的长树——flag 搜索覆盖**全折线（顶点+中段）**一并删除失宿 flag，且返回 `alsoDisconnectedPins[]` 列出因删线被连带断开的其它 pin——**该字段非空必须逐个重连**（`sch autoconnect`），否则邻居 pin 静默悬空。**删后回读验证（0.26.x）**：三条定位路径都收齐该脚**全部**触脚桩线（同脚多桩不再漏删），删除后回读画布——平台对并入共享树的 wire 会静默 no-op 仍返 true，此时按部分应用约定（#151）返回 `partial:true` + `survivedIds`/`notApplied` 并在 stderr 打警告，`deletedWires`/`deletedFlags` 只计证实消失的 id。**不再需要删后自己回读确认假成功——看 partial 字段/警告即可**；`partial` 非空说明 pin 可能仍连着，按 `survivedIds` 处理残留。CLI：`easyeda sch disconnect --pin U1:5`
- `schematic.pin.set_no_connect` — 给引脚打/清「非连接标识」(NC, X 标记)，告诉 DRC 该脚是故意悬空。按 `--designator` + `--pin`（可多个）定位；`--clear` 清除。底层必须走器件实例 `component.getAllPins()`，每脚 `setState_NoConnected(...)` 后 `await pin.done()` 才真正落到画布，再用新实例回读验证。CLI：`easyeda sch no-connect --designator U1 --pin 23,24`
- `schematic.rebind.footprint` — 换封装（五步绑定法）。`modify` 改不了已放置件的封装引用，故走 `lib_Device.modify → delete → create → 恢复位号/坐标/属性`。先以 LCSC C 号→MPN→工程库名解析已放置件的**真 32 位 device uuid**（`getState_Component().uuid` 是 16 位符号实例 id，库 API 一律拒收）。**系统库 device 记录不可写**（`lib_Device.modify` 恒 false）——此时自动走**个人库克隆回退**：复制/复用同名副本 → 副本绑新封装 → 重放置，结果 `mode='cloned-to-personal-library'` + `clonedDevice`；克隆触发的「符号/封装另存为」冲突弹框由 MutationObserver 自动点「确认」（后台 tab 定时器被 Chrome 节流，轮询点击不可用）。按封装名精确匹配（同名多命中或未命中会报错，可用 `--footprint-uuid` 直连）。**重建会换新 primitiveId，导线可能需重连——务必跑 `sch drc`/`sch check` 复核连通性。** CLI：`easyeda sch rebind-footprint --id <primitiveId> --footprint <name>`
- `schematic.rebind.symbol` — 换符号，机制同上（五步绑定法 + 同款克隆回退/弹框自动确认）。CLI：`easyeda sch rebind-symbol --id <primitiveId> --symbol <name>`
- `schematic.component.replace` — **整器件替换（换型号）**，官方「器件标准化」面板「使用推荐器件」的 API 等价物（该面板本身零 API）。官方无 rebind-device 原语（`modify` 改不了 device 绑定），走 delete → 同位姿 create 新 device → 恢复位号 + uniqueId（保留 uniqueId 使 sch→PCB `import-changes` 走 UPDATE 而非删加）。**器件身份字段（name/制造商/供应商/LCSC）故意不带过去**——跟新 device 走；`--keep-properties` 才连带旧自定义属性。目标三选一：`--lcsc <C号>`（须唯一解析）/ `--device-uuid <u> --device-lib <l>`（确定性）/ `--query <名称>`（须唯一命中）。delete 后失败自动回滚重建原器件（含完整身份）。返回 **pinDiff**（同位姿下按 pinNumber 对比 removed/added/moved）——非空说明旧导线对不上新引脚，**必须重接线再跑 `sch drc`/`sch check`**。CLI：`easyeda sch replace --id <primitiveId> --lcsc C14663`
- `schematic.save` — 保存原理图。既定流程中的阶段过门后必须显式保存并核对
  `saved:true`；只有用户要求逐步确认时才在保存前停下

## Library

3D 模型的选择与关联可完全通过 typed CLI/API 完成：

```bash
easyeda lib model3d search --query SDCARD --limit 20
easyeda lib model3d copy --uuid <model> --source-library <lib> --name SDCARD_18X18
easyeda lib device model3d --uuid <device> --library <lib> \
  --expected-name EA_AGENT__SDCARD_MODULE_18X18 \
  --model3d-uuid <model> --model3d-library <lib>
easyeda lib device model3d --uuid <device> --library <lib> \
  --expected-name EA_AGENT__SDCARD_MODULE_18X18 --clear
```

绑定动作会先确认 Device 和模型存在，再调用 `lib_Device.modify`；只有回读中模型
UUID 与 library UUID 均精确一致才报告成功。

- `schematic.library.search` — 自由文本搜索立创/EasyEDA 器件库，返回 libraryUuid + uuid。可传 `libraryUuid`（CLI `--library`）只搜个人库等指定库。当 `query` 为纯 LCSC C 号（`^C\d+$`）时自动切换为精确模式，仅保留 `lcsc`/`supplierId` 严格相等的条目；无精确命中则报错。传 `allowFuzzy`（CLI `--allow-fuzzy`）可保留原模糊排序结果
- `library.list` — 列出库并返回 `personalLibraryUuid` / `projectLibraryUuid` / `systemLibraryUuid`。CLI：`easyeda lib libraries`。
- `library.footprint.create/get` — 在个人库（默认）或工程库创建空封装并立即 `get` 回读验证；创建成功但即时回读缺失时返回 `partial:true`，不得盲目重试。CLI：`easyeda lib footprint create --name MY_FP [--scope project]` / `footprint get --uuid … [--library …]`。
- `library.footprint.build` — 打开指定封装并从 JSON spec 写入焊盘与线段。单位统一为 **mil**；`pads[].shape`/`hole` 使用官方 tuple（如 `['RECT',35,40,4]`、`['ELLIPSE',50,50]`、`hole:['ROUND',24]`），常用层：TOP=1、BOTTOM=2、TOP_SILKSCREEN=3、BOTTOM_SILKSCREEN=4、MULTI=12。整份 spec 先校验再 mutation；中途失败尝试删除本次图元并返回 `partial/rollback`。CLI：`easyeda lib footprint build --uuid <fp> --library <lib> --spec examples/library/footprint-r0603.json`。
- `library.footprint.copy` — 通过官方 `lib_Footprint.copy` 无损复制复杂封装，自动套用 `EA_AGENT__` 命名并回读验证。弧线/区域/文字/机械细节尚未进入 JSON builder 时优先用此路径。CLI：`easyeda lib footprint copy --uuid <src> --source-library <lib> --name SDCARD_V2`。
- `library.symbol.create/get` — 创建/读取符号库资产；`--symbol-type` 是官方 `ELIB_SymbolType` 数值。CLI：`easyeda lib symbol create|get …`。
- `library.device.create/get` — 创建单器件 Device 并绑定已有 Symbol、可选 Footprint，写入位号前缀/BOM/制造商/供应商属性后立即回读关联。默认 `addIntoBom/addIntoPcb=true`；`--properties '{"Value":"10k"}'` 写入官方 `otherProperty`。CLI：`easyeda lib device create --name … --symbol-uuid … --symbol-library … [--footprint-uuid … --footprint-library …] --designator U`。

以上三个 `create` 会强制使用跨项目可复用的 `EA_AGENT__<ASSET>` 命名（例如
`EA_AGENT__R0603`）。资产名统一规范化为大写标识；若调用方已传入完整前缀则不会
重复添加。项目来源应写入 Device `otherProperty`/描述，不放进个人库资产名。
三个 `delete` 均要求 `--uuid`、`--library` 和 `--expected-name` 精确匹配，并在删除后
回读验证不存在。

单器件资产的推荐顺序：`lib libraries` → `footprint create` → `footprint build` → `symbol create` → `symbol build` → 可选 `model3d create` → `device create`（绑定三者）→ `device get` → `sch place` 试放。Footprint/Device/Symbol/3D Model 官方库 API 均为 beta；输出中的 `verified` 与 `partial` 必须检查。

一站式推荐：`easyeda lib device build --spec device.json`。CLI 依序创建 Symbol、Footprint、可选 3D Model 并绑定 Device；任一步失败会按 3D→Footprint→Symbol 逆序尝试回滚，不留下可被误认为完成品的 Device。

- `library.symbol.build` / `easyeda lib symbol build --uuid ... --library ... --spec symbol.json`：从 `outline[]` + `pins[]` + 可选 `circles[]` 从零绘制符号并回读验证；圆图元用于 Pin-1/极性标记。
- `library.model3d.create|get|delete` / `easyeda lib model3d ...`：从本地 STEP/3D 文件导入，统一加 `EA_AGENT__` 命名并回读验证。
- `easyeda lib device create` 支持 `--model3d-uuid` + `--model3d-library`，与 Symbol、Footprint 一起绑定为完整 Device。

## Verify & Export

- `schematic.drc.check` — 调官方 `eda.sch_Drc.check` 作为 SDK DRC 门。当前 EasyEDA build 可能只返回 boolean/聚合结果,即使 `includeVerboseError=true` 也不保证有逐条 UI warning；CLI: `easyeda sch drc [--json]`。**不要单靠它宣称“官方 UI DRC 干净”**。
- `schematic.check` — 我们的逐条重建检查:从 primitives + 官方 `sch_ManufactureData.getNetlistFile()` 交叉校验，覆盖悬空脚、导线交叉/穿脚、网络名不一致、零长/悬挂线、极性约定离群(polarity-convention-outlier,#183)及 duplicate/titleblock/marker overlap。`--json` 是 `{id,type,version,ok,result}` 信封，findings 位于 `result.findings`。CLI: `easyeda sch check [--json] [--strict]`。
- `schematic.bridgeCheck` — 线树粒度检查 `wire-bridge`、orphan stub/flag，补 `sch check` 逐 wire 视角的盲区。CLI: `easyeda sch bridge-check [--json]`。
- `schematic.read` — 一次读取 components、pin→net、nets、floating pins 与 check；新设计对照 spec，既有原理图重构前后对照黄金 pin→net/NC 集合。components 每条含 **`primitiveId`（改动句柄：select/modify/delete/replace/rebind 都吃它，16 位 hex）**；⚠️ `uniqueId`（`gge…`）是 sch↔PCB 关联键**不是** primitiveId，喂给按 id 的 mutation 必 notFound（真机事故：read 曾漏输出 primitiveId，agent 抓了 uniqueId 全部落空）。CLI: `easyeda sch read [--page <page>]`。
- `schematic.export.netlist` — 导出网表为 artifact。底层必须走官方推荐的 `eda.sch_ManufactureData.getNetlistFile(fileName, netlistType)` 并读取返回的 `File`;不要使用已废弃的 `eda.sch_Netlist.getNetlist()`。官方文档标注 `getNetlist()` obsolete 且建议替代为 `getNetlistFile()`,并且 upstream issue [easyeda/pro-api-sdk#30](https://github.com/easyeda/pro-api-sdk/issues/30) 已复现它在含悬空引脚的原理图上可能无限卡死。CLI: `easyeda sch netlist`
- `schematic.export.bom` — 导出 BOM（csv 或 xlsx）为 artifact。CLI `easyeda bom export --type csv` **默认在导出后就地补全 LCSC C 号**（按 Manufacturer Part 关联 `standard-parts.json`，把 `Supplier Part` 从 `<MPN>.1` 改写为可下单的 C 号）；`--enrich=false` 关闭，xlsx 不补全（二进制）。补全是 best-effort（缺 python3/脚本只告警、导出仍成功）。脚本自动解析顺序：`--script` > `$EASYEDA_SKILLS_DIR/easyeda-agent/scripts/bom-enrich.py` > 二进制/工作目录向上找 `skills/` > PATH；安装版二进制在 `/usr/local/bin` 时设 `EASYEDA_SKILLS_DIR` 最稳。

## PCB 基础上下文（非穷举）

- `pcb.documents.list` — 工程内所有 PCB 文档（uuid + name）
- `pcb.components.list` — PCB 上的封装/器件（可含 pads）
- `pcb.layers.list` — PCB 层列表 + 当前层 + 铜层数（会先激活 PCB tab 保证 `currentLayer` 可读回；无当前层时附带 `visibleLayers` 作为显示状态证据）→ `easyeda pcb layers`
- `pcb.layers.set_current` — 切换当前编辑层（`--layer` 接受 id|层名|top|bottom|inner1）→ `easyeda pcb layer-set --layer bottom`
- `pcb.layers.visibility` — 显示/隐藏/聚焦层做视觉 QA：`--preset top-only|bottom-only|copper-only|silk-only`，或 `--show/--hide`（可加 `--exclusive` 只留所选）→ `easyeda pcb layer-visibility --preset bottom-only`
- `pcb.view.side` — 切到顶面/底面视图（选该面铜层为当前层 + 聚焦该面铜+丝印），随后 `pcb snapshot` 即反映该面。注意：EasyEDA 无原生画布翻面 API，这是「层聚焦」近似而非物理翻板 → `easyeda pcb view-side --side bottom`
- `pcb.nets.list` — PCB 全部网络

### 长度约束：差分对 / 等长网络组（#176）

**布线前（P7 之前）声明,布线后用 `pcb report` 量。** 约束是让 DRC 与布线器知道「这两条是一对 /
这组必须等长」的唯一途径,也是 `pcb report` 的 `skew`(|lenP−lenN|)与 `spread`(max−min)有意义的前提 ——
不建约束,那两个数组永远是空的,报告里的测量能力等于空转。

- `pcb.constraint.list` — 读回本板的**约束清单**(差分对 + 等长组)。注意与 `pcb.report` 分工:
  这条给「有哪些约束」,`pcb.report` 给「量出来多少」→ `easyeda pcb diff-pair list` / `eq-group list`
- `pcb.differential_pair.create|delete|rename` → `easyeda pcb diff-pair create --name USB0 --positive USB_DP --negative USB_DM`
- `pcb.equal_length_group.create|add_nets|delete` → `easyeda pcb eq-group create --name DDR_ADDR --nets A0,A1,A2`

四条行为约定(都已真机验过):

1. **网名前置校验**:约束指向板上没有的网,平台照收不误但等于没建 —— 我方在动手前比对
   `pcb nets`,对不上就**一个字节都不写**地拒绝并点名缺失网(网名大小写敏感,来自原理图);
2. **写后回读**:回执的 `verified` 是连接器自己重读 `getAll` 比对出来的,平台返回的 boolean 不算数;
3. **幂等**:同名同内容重建 = `alreadyExists`(可重放);同名**不同**内容 = 明确拒绝并给下一步
   (改名 / 先删 / 用 `eq-group add` 扩展),绝不静默覆盖;
4. **改绑定要删了重建**:平台对差分对只暴露「改名」,没有「改绑哪两条网」。

⚠ 这些是 `Mutates` 动作 → 改完再读会撞铁律 5 的 `STALE_READ` 门,先 `easyeda doc reload`(实测如此)。

## Board（板子/组合 — 原理图↔PCB 绑定）

一个 **Board = 1 张原理图 + 1 块 PCB**，原理图与 PCB 就是通过它「组合」在一起（`import_changes` 也沿此链接同步）。Board 以**名称**标识。CLI：`easyeda board …`。

- `board.list` / `board.current` — 列出全部组合（名称 + 原理图 + PCB）/ 当前组合
- `board.create` — 把原理图和/或 PCB 绑成新组合（`--schematic` / `--pcb`）；游离 PCB 在 `import_changes` 前的修复手段
- `board.rename` — 重命名组合（`--name` → `--new`）
- `board.copy` — 复制组合（连同原理图 + PCB）
- `board.delete` — 删除组合（**需确认**，无 undo）

## Confirmation Required

- `schematic.component.delete`
- `schematic.page.delete`（删除图页，无 undo）
- `board.delete`（删除组合/板子，无 undo）
- 生成的多步 mutation 计划
- `debug.exec_js`（任何情况）

### 1.4 Connectivity 快照边界

快照转换只使用同一张 pin→net 表生成网络和连接，避免枚举顺序不同造成错网。
`netId` 当前由网名确定性生成：重排不变，但改名会改变 ID，尚不是持久网络身份。
无引脚的浅数据拒绝导出；多页必须逐页激活读取，不能把空 pins 当作 NC。
离线 diff 包含新增、移除连接；结构校验拒绝不存在的引脚和同脚重复归网。

### Connectivity → plan → sch apply（当前支持范围）

```bash
easyeda sch connectivity --window <id> > .easyeda/tmp/before.json
# 在副本 after.json 中明确目标 connections；不修改 before.json
easyeda sch plan .easyeda/tmp/before.json .easyeda/tmp/after.json > .easyeda/tmp/plan.json
easyeda sch apply .easyeda/tmp/plan.json --dry-run
easyeda sch apply .easyeda/tmp/plan.json --window <id>
```

`plan` 输出实际 playbook `version:1 / meta / steps`，不再输出占位 operations。
当前仅支持已有引脚增加显式 `power`、`ground`、`net_port_in/out/bi` 连接；
新增器件、换网、断连、改名、模块变化、普通 wire 路由均拒绝生成计划，不会自动改成标签。
前后快照必须具有相同 projectId/documentId。无连接的引脚不自动推断为 NC。

生成队列先核对整页 pin-to-net 基线，每次写入后和保存后分别回读核对；
`expectedConnectivity` 是 playbook 的检查字段，验证失败即停。它不是事务回滚：
先前已落地的动作保留并记录 journal。保护计划禁止目标覆盖、`--resume`、`--from/--to`，
失败后重新导出实际快照再规划。内部 run 子命令继承目标页守卫。
离线结构检查用于规划；执行时回读用于证明写入，二者职责不同。
