# CLI 与 Action 使用参考

`easyeda <command> --help` 是命令签名，`easyeda actions` 是机器可读 typed action 目录。
本文件只保留调用边界；不复制全部命令和历史修复。原理图主流程见
[schematic-data.md](schematic-data.md) 与 [auto-layout-sop.md](auto-layout-sop.md)。

## 1.4 原理图入口

| 目的 | CLI |
|---|---|
| 获取 canonical 图 | `sch connectivity`；全工程用 `--all-pages` 逐页读取 |
| 离线比较连接 | `sch connectivity-diff <before.json> <after.json>` |
| 修复功能式位号 | `sch designators allocate` → `plan` → `sch apply` |
| 完整本地版本比较 | `sch design-diff expected.json actual.json --exit-code`；检查 coverage/unverified |
| 由测量计算 Lib 内部 | `sch lib-layout --from layout-input.json --out composition.json`；纯离线 |
| 核心相对移动/单脚标签修复 | `sch layout-edit --source zones.json --page page.json --snapshot fresh.json (--move-core ID --to X,Y \| --repair-pin ID:PIN) --out target.json [--report report.json] [--playbook repair.json]`；纯离线生成，修复 playbook 使用作用域 action |
| 合并已设计的 Lib 几何 | `sch compose --from … --out … --before … --playbook …` |
| 放置固定 IR 中的器件 | `sch materialize <connectivity.json> --out …`；不是完整布局/布线器 |
| 少量显式标记连接增量 | `sch plan <before.json> <after.json>`；不支持任意器件或导线 diff |
| 转换/核验模块方框与标题 | `sch frame apply/check --from …` |
| 执行计划 | `sch apply <playbook.json>` |

生成器的输入、支持范围与位号/身份规则集中在 [schematic-data.md](schematic-data.md)。
数据层校验用于发现结构问题，执行时的实时回读用于证明变更确已生效。

compose 生成的全工程位号唯一性步骤使用 `schematic.components.list` 的
`allPages:true,tagPages:true` 最小清单，只检查位号冲突、既有 primitiveId 和待建位号不存在；
它不请求慢速 device identity、bbox 或 pins。紧随其后的目标页守卫仍读取完整
device identity、bbox、pins、wires 与连接摘要，不能用前者替代后者。
整页 `--replace` 的源快照还要用 `sch list --include-page-primitives` 读取清页涉及的全部
图元身份与原生状态（标记/端口、导线、总线、文本及其他图形）；`verify-source-before-reset`
检查完整场景，生成队列的 `sch clear --expect-page-primitives-b64` 在删除前再次核对
（编码可保留属性中的字面 `${...}`）。缺测或变化时重新采集，
旧队列不能继续执行。sheet 自动更新时间不参与此比较。普通 clear 遇到孤儿属性或嵌入对象
会拒绝；官方属性全局枚举漏掉逐父可见对象、嵌入文件内容不可读时也拒绝，不报告为零对象。
重载后若官方 `getAll` 和 `get(id)` 的属性可见性 getter 都为 `undefined`，连接器只在
官方当前工程 `.epro2` 源的目标 `SCH_PAGE` 中找到同 ID、同 key、同 parent、同 value 的
`ATTR`，且源明确含 `keyVisible`/`valueVisible` 时补齐这两个字段。先后核对工程、文档和
标签身份；源缺失、字段缺失或状态不符仍拒绝完整快照和清页，不把 `undefined` 猜成 `null`。
宿主可能暴露未写入工程源的自动生成属性；其字段全由 SDK 正常读取时继续保留在 SDK 清单中，
不要求这些可读属性出现在 `.epro2` 源里。

## SCH Apply

```bash
easyeda sch apply steps.json --dry-run
easyeda sch apply steps.json --yes
```

`--dry-run` 只预检并打印，不执行步骤；`--yes` 对当前任务已授权的计划跳过交互提示。
不以文档中的示例替代用户授权，也不为已经授权的每个步骤重复请求确认。

Playbook 使用 `version:1`、`meta` 和有序 `steps`。每步只选一种执行方式：

- `action` + `payload`：typed action；输入按目录 schema 校验。
- `run` + `flags`/`args`：Cobra 子命令，例如 `sch frame apply`。
- `notify`：编辑器提示。

`capture:{"part":"$.primitiveId"}` 捕获新实例 ID，后续 payload 用 `${part}`；
不要把旧文件中的 primitive ID 当作新建结果。`assert` 的路径相对 action 的 `result`，
支持 `exists`、`true/false`、`==/!=`、数值比较及 `len` 比较。内部 run 继承目标工程/页。

执行默认失败即停，只读步骤可重试；变更超时不自动重发。`partial:true` 或非空
`notApplied` 会使 typed 步骤失败。成功回执不等于保存；计划应包含读回与显式 save。
可选 `verify` 是失败后的落地检查，不是事务回滚。已生效的前序步骤保留在画布和 journal。

CLI 完整读取单次 action 的 HTTP 响应，最多 32 MiB；health 清单另限 1 MiB。
超限必须报错且不输出截断内容。整页含属性、pins、bbox 与 wires 的快照可能超过 1 MiB；
旧 CLI 会静默截断并导致 JSON 解析失败，不能据此判断宿主对象缺失。遇回读失败先停止依赖写入，
保留 journal、输入和原始输出；已写入状态可 typed save/原生导出冻结，但不算验证通过。
修复读取后重新采集完整快照、核对已生效步骤，并从当前状态重算受保护队列；不要盲重放或删减必要字段。

**生成的保护计划**（如 compose、designators、connectivity plan）禁止改目标、
`--resume` 和 `--from/--to`。失败后重新读取当前图，修正输入，再完整编译/执行。
普通手写 playbook 支持这些选项，但恢复依赖原文件 SHA 和有效的捕获变量；修改文件后
不能继续使用旧 journal。只有明确验证过的独立片段才适合区间执行。

`audit export --playbook` 可从审计生成复现用队列；它可能包含 clear/delete 等操作，
也可能引用在录制区间外创建的 ID。先检查生成内容和 raw-id 警告，不能将录制物当作幂等模板。

## 常用原理图适配器边界

| CLI / action | 必要边界 |
|---|---|
| `doc ls/switch/open/reload`，`document.current/open/close` | 使用工程和页面目标；同名页用 UUID。工程仍在线但没有活动标签时，`doc ls --project` 继续读取工程级原理图页/PCB 清单，`doc open <uuid> --project` 用 typed `document.open` 恢复并以 fresh `document.current` 确认；其他 current/清单错误仍失败关闭。`doc reload` 保存后把 fresh current 的 UUID + tabId 一起交给 typed `document.close`，由官方 API 在关闭前回读身份和 splitScreenId，再用 `document.open` 恢复；禁止以 `debug.exec_js` 关闭标签。CLI 同时核对活动 UUID 与对象枚举 settle；只出现目标标签、但对象仍不可读时失败并要求停止写入、修复 typed reload/open 后复测 |
| `web reload`，`system.page_reload` | 仅在用户明确要求刷新整个 Web 编辑器时使用。传精确 `--project <UUID> --doc <UUID>`；CLI 先保存并记录目标页组件 ID 基线，连接器在回执后刷新顶层页面。新 windowId 必须属于同工程；若宿主恢复另一文档，CLI 最多一次 typed `document.open` 恢复原文档。成功要求连续同 context 的 fresh 组件清单与基线 ID 一致、对象状态稳定、末次 `document.current` 核对；空基线需额外稳定采样，仅证明空页路由可读。超时、再次漂移或读失败均不能称重载完成。其他打开的文档须事先保存；不能用它自动兜底对象读取故障 |
| `sch list`，`schematic.components.list` | `includeDeviceIdentity` 为重放解析真正库 UUID；`includePins/BBox/Wires` 取得几何基线。V4 `pins[].otherProperty` 保留引脚文本属性；字段缺失不能当空对象。非激活页可能是浅数据 |
| `sch attribute-inspect --id <primitiveId>`，`schematic.attribute.inspect` | 只读诊断当前页指定属性的 `KeyVisible`/`ValueVisible`：分别记录全量枚举、按 ID 读取、按 ID 读取后 `toAsync().reset()` 的原值与类型，并核对前后文档身份及图元 ID。`undefined` 为不可读；诊断结果不补默认值，不放宽整页快照或清页守卫。仅调用官方读取接口，不调用 `done`/`modify`。|
| `sch place`，`schematic.component.place` | 使用库 UUID；自动回填可确定的 C 号与空属性是 best-effort，须检查警告。没有 place 自定义属性输入契约；V4 复数 symbol/device/footprint association 在 canonical selector 完成前写前拒绝，不能取第一项 |
| `sch modify`，`schematic.component.modify` | `otherProperty`/`customAttributes` 二选一，合并保留原属性。`verified:false` 需要再回读，不能当已验证 |
| `sch prim-delete/clear` | 删除后按 ID 或完整图元清单验证；默认保护 sheet。未知枚举或幸存图元不能报告清空 |
| `sch connect/autoconnect`，`schematic.power.connect_pin` | 必须生成非零短线，flag 不能与 pin 重叠；connect 非幂等，autoconnect 可跳过已连接目标网 |
| `sch disconnect`，`schematic.pin.disconnect` | 检查共享树的 `alsoDisconnectedPins` 和删除残留；逐个恢复受影响引脚 |
| `sch no-connect` | 显式设置/清除 NC，不创建零长线，不推断缺失数据为 NC |
| `sch replace/rebind-symbol/rebind-footprint` | rebind 先回读 Device association，再创建并回读候选，之后才删除原件；恢复后逐字段核对设备/符号或封装绑定、`uniqueId`、位姿和属性。失败回执含 phase、原件/候选存在性和 rollback 事实。超时后禁止盲重试及 `pcb import-changes`，先新鲜回读。换器件另查看 pinDiff，按引脚差异重连和验收 |
| `sch export-image` | 文档渲染 SVG/PNG/PDF；`--ids` 导局部，不依赖视口截图 |
| `sch read/check/bridge-check/drc/gate` | 用法与判读见 [schematic.md](schematic.md)；SDK DRC 聚合值不代表 UI 所有警告消失 |
| `sch save` | 通过阶段验证后保存并确认 `saved:true`，不能只依赖防抖 autosave |

`replace` 保留 sch↔PCB 的 `uniqueId`，器件型号/供应商字段随新 device；`--keep-properties`
才保留旧自定义属性。`rebind` 对不可写系统库可克隆到个人库；失败恢复仍需看实际回读。
修改属性时不要整包带入库的 `Designator` 等投影键。回放 `propertiesBefore` 只能恢复旧值，
无法通过 merge 删除新加的键。网络文件用 `sch_ManufactureData.getNetlistFile()`，
不用已废弃的 `sch_Netlist.getNetlist()`。

产物路径从 `artifacts[].path` 获取。`sch read/list` 本身直接输出 JSON；
`sch check --json` 使用 `{ok,result}` 信封，问题在 `result.findings`。
`bom export --type csv` 默认 best-effort 补 LCSC C 号，`--enrich=false` 可关闭，xlsx 不补。
需显式指定脚本时用 `--script`，安装态也可设置 `EASYEDA_SKILLS_DIR` 指向 Skill 的父目录。
补号解释器按 `python3` → `python` → `py -3` 依次探测（Windows 上会真正运行一次候选，
所以微软商店那个只会退出 9009 的 `python3.exe` 假入口会被跳过），需要钉死某个解释器
（venv、指定小版本）时设 `EASYEDA_PYTHON=/abs/path/to/python`；设了但不可执行直接报错，
不会退回其它解释器。找不到任何 Python 3 时只是补号失败并打 warning，导出的 BOM 仍然成立。

## 图纸与明细表

`project export-source --uuid <current-project-uuid> [--window <window-id>] [--out project.epro2]`
经官方 `sys_FileManager.getProjectFile(..., 'epro2')` 导出当前工程原包。`--uuid` 必须等于
导出前后的活动工程 UUID；CLI 核对官方大小、daemon 落盘大小及 SHA-256。超过 8 MiB、
权限不足、工程切换或超时均失败。此命令只保存原始证据；`epro2` 中是否含当前图框 `SYMBOL`
及可区分红色内框与图签的图元，须逐份验证，不能直接当成几何实测。

`lib symbol export-source --uuid <sheet.symbol.uuid> --library <sheet.symbol.libraryUuid>
[--out source.elibz2]` 经官方 `sys_FileManager.getSymbolFileBySymbolUuid` 导出原始符号包。
从 `sch list` 的 `componentType:"sheet"` 记录取 **symbol** UUID，不要误用 `component`
中的器件 UUID。CLI 对导出物大小、落盘路径及 SHA-256 做核对；超过 8 MiB 或权限不足即失败。
此命令只保留未改写的原始证据，尚无已验证的 `.elibz2` 图框解析器，不能把符号包、纸张
外 bbox 或图签比例估计称为红色绘图区内框实测。下载库权限和当前宿主是否能导出内置图框
符号须现场只读验证。

`sch titleblock-get` 先取得实际字段名；`sch titleblock --data` 只传要改的明细项，按
`--doc` 钉住聚焦页。不要把 get 返回的整包字段写回，尤其 Device/Symbol、几何与 `@` 投影项。
连接器按字段回读：unknownKeys 应修正键名，partial/notApplied 应检查实际状态，不能盲重试。
只更新文本时保留字段已知的 `showTitle`/`showValue`；任一显隐未知、缺失或 null 时，
必须在该字段显式给出对应布尔值，否则 CLI 在首次写入前拒绝并列出缺失项，不猜宿主默认值。
只改显隐而不改 `value` 时，仍需已知或显式的 `showTitle` 才能安全初始化字段；
未请求且未知的 `showValue` 可以省略。Compose 通过同一 CLI 执行，遇拒绝先修源参数再重算。
需要改变属性显隐时在对应字段显式给布尔值，例如
`{"Name":{"value":"电源页","showTitle":false,"showValue":false}}`；字段属性显隐与图签整体显示是两回事。
显示字段名可能生成图签表格以外的属性文字；写后必须用官方整页导出核对，不能仅凭值已写入验收图面。
失败后的幂等回读要同时核对请求的文本、字段显隐及同次整体 `--show/--hide`，不能以相同文本掩盖未生效的可见性。
空的 `--data` 拒绝执行；整体显示未知时不自动打开，只有明确 false 才沿用文本更新时打开图签的行为。
明细表接口不能设置纸张尺寸；换图框是独立的器件替换工作，不能用 Width/Height 伪装。

`page-new/rename/delete` 管单页，`sch rename` 管原理图文档。compose 不隐式删除源页；
删页应先确认目标器件/网络已经迁移，平台无程序化 undo。

## 器件库与自建资产

优先标准器件或 `lib by-lcsc` 的精确 C 号匹配。搜索结果须核对型号与封装，不能默认取第一条。
`sch resolve-lcsc` 只在型号和封装精确匹配时写回，unresolved 必须继续处理。

需要自建时按 `lib libraries` 找目标库，再用 `lib device build --spec device.json`
编排 Symbol、Footprint、可选 3D Model 与 Device；也可分步 create/build/get。
完整规格先运行 `lib device validate --spec device.json`，它离线核对 PDF 证据、几何字段、
重复编号以及 symbol pin ↔ footprint pad 集合；`device build` 会再次执行同一输入校验，防止写入非法资产。
PDF 通读、封装变体消歧和规格格式见 [library-authoring.md](library-authoring.md)。
资产使用可复用的 `EA_AGENT__<ASSET>` 命名，项目来源写属性或描述。create/build 的
`verified/partial/rollback` 必须核对；删除要求 UUID、library 和 expected-name 精确匹配。
Symbol/Footprint build 仅允许写入可证明为空的刚创建资产：Connector 在任何 create 前回读
目标 editor 的完整受支持图元 inventory，非空或读取不完整都以 `PRECONDITION_REFUSED`
零写入拒绝。build 不是追加或替换接口，不要重放同一 UUID；当前没有 `--replace`。

- Footprint JSON 的单位是 mil，pad/hole 使用官方 tuple；复杂弧线/区域优先用
  `lib footprint copy` 保留几何。层与制造规则见 [pcb.md](pcb.md)。
- `lib symbol build` 从轮廓、引脚与可选圆形生成符号；引脚编号、Pin-1 和极性需验证。
- 当前没有 Device rename typed action；实测官方 `lib_Device.modify` 改名返回 false 且不落地，
  不要用 `debug exec` 反复试探。需要新名称时新建并重新绑定 Device。
- `lib model3d search/copy/create` 获取模型；`lib device model3d` 绑定或清除，须回读
  模型 UUID 与 library UUID。`device create` 也支持模型绑定参数。
- 库 API 有 beta 能力；错误或结果不明时先 get，不能因即时读回缺失重复创建。

需要底层方法时先 `easyeda api search <query>`。typed action 尚缺的行为可临时探测，
验证后再实现 CLI；不把重复 debug 脚本积累成生产流程。

`debug exec` 的脚本编译失败返回 `PRECONDITION_REFUSED`，说明代码未执行：修正语法与
命令行转义后再提交，不原样重试。执行阶段抛错仍按 `EDA_CALL_FAILED` 处理，即使异常名为
SyntaxError；执行可能已经产生修改，必须回读。语法拒绝不计入连接器健康度。

队列拒绝只有在入队探针仍未返回、且近期旁路 `document.current` 成功时才使用
`CONNECTOR_QUEUE_BLOCKED`，CLI 可有界等待。旁路结果未知、过期或失败时返回
`CONNECTOR_HEALTH_UNVERIFIED`，停止自动等待，先切前台并检查旁路读取；持续不响应时
按恢复流程重启并回读。两种拒绝均未派发当前动作，不代表此前超时的写入没有落地。

布局路径的 `connect_pin` 与 `sch connect/autoconnect` 共用 35 秒请求预算，包含 daemon 的
2 秒回执余量。该预算不保证宿主一定完成；超时仍须回读，不能自动认定创建失败并重发。

`sch place` 为 daemon 等待连接器保留 8 秒，另外预留 2 秒传回结构化错误（请求共10秒）。
超时提示同时覆盖 HTTP 超时和 daemon 返回的 deadline 错误；先回读是否已经放置，再检查
库 UUID、窗口状态。超时不能单独证明 UUID 错误，也不能作为再次放置的依据。

## 外部工程导入边界

Altium Designer `.SchDoc` / `.PcbDoc` 当前没有可用的 typed action。官方 beta
`sys_FileManager.importProjectByProjectFile` 在已报告的 3.2.149 本地工作区会静默返回
`undefined` 且不产生工程副作用，不能包装后当成功。`sys_FormatConversion` 的 Altium
入口只适用于 `.SchLib` / `.PcbLib` 库转换。工程迁移当前标为 `unsupported`；不得通过
EasyEDA 交互界面兜底。能力边界与未来 typed 验收见 [project-import.md](project-import.md)。

## PCB 基础上下文（非穷举）

- `pcb.config.get` / `pcb.config.set` — 当前 PCB 的配置读取与参数化局部修改。CLI 为
  `pcb config get/clearance/track/via/bind`；参数、mil/mm、dry-run、部分成功及回读契约见
  [pcb-config.md](pcb-config.md)。`get` 导出可交给 `pcb drc-rules-set --from` 完整恢复。

- `pcb.documents.list` — 工程内所有 PCB 文档（uuid + name）
- `pcb.components.list` — PCB 上的封装/器件；`includePads:true` 回传 pad 的原始
  `shape` / `rotation` / `specialPad`，支持形状另带旋转后 bbox `width/height`
- `pcb.line.list` — 铜线与圆弧；`arcsAvailable:true` 才能证明空 `arcs` 确实表示没有圆弧
- `pcb net-path` — 用 fresh pads/tracks/arcs/vias 证明有序焊盘拓扑、层与过孔；长度累计实际
  经过的 track 子段和 arc 子弧，分叉落在图元中段时不把整图元或圆弧弦长计入结果。
- `pcb.layers.list` — PCB 层列表 + 当前层 + 铜层数（会先激活 PCB tab 保证 `currentLayer` 可读回；无当前层时附带 `visibleLayers` 作为显示状态证据）→ `easyeda pcb layers`
- `pcb.layers.set_current` — 切换当前编辑层（`--layer` 接受 id|层名|top|bottom|inner1）→ `easyeda pcb layer-set --layer bottom`
- `pcb.layers.visibility` — 显示/隐藏/聚焦层做视觉 QA：`--preset top-only|bottom-only|copper-only|silk-only`，或 `--show/--hide`（可加 `--exclusive` 只留所选）→ `easyeda pcb layer-visibility --preset bottom-only`
- `pcb.view.side` — 切到顶面/底面视图（选该面铜层为当前层 + 聚焦该面铜+丝印），随后 `pcb snapshot` 即反映该面。注意：EasyEDA 无原生画布翻面 API，这是「层聚焦」近似而非物理翻板 → `easyeda pcb view-side --side bottom`
- `pcb.view.filter.get` — 只读返回当前 PCB 画布过滤配置 → `easyeda pcb view-filter`。当前官方 SDK 只有 getter，没有“元件属性”显隐 setter；因此自动隐藏/恢复保持 `unsupported`，不能用 `pcb_PrimitiveAttribute.modify` 改持久属性，也不能点击 GUI 兜底。
- `pcb.snapshot` — `--fit-mode board|all|none`；默认 `board` 先执行公开 `zoomToBoardOutline()` 再抓取当前渲染区，返回实际 `fitModeApplied` / `fitApi` / `captureKind`。它是 board-fitted viewport PNG，`objectLevelExport=false`；不能冒充编辑器菜单的对象级“复制为 SVG/PNG”，后者当前没有公开 `eda.*` 包装。旧 `--fit=true|false` 仅兼容映射为 `all|none`。
- `pcb.nets.list` — PCB 全部网络
- `pcb dump --include-copper --out board.json` — 生成自包含快照；焊盘保留原始 shape、旋转和
  specialPad，铜按 routing/vias/pours/poured/regions/fills 分别标记 available/unknown，
  `semanticSha256` 排除采集时间与自身哈希后用于执行前 stale 检查。
- `pcb.poured.list` / `pcb poured-list` — 读取 `pour-rebuild` 后的实际铜岛，不等同于
  `pcb.pour.list` 的可编辑边界；complex polygon 的孔洞与已验证 ARC 原样保留，任一 fill
  几何读取失败则整个 action 失败。宿主 poured fill 的坐标和 `lineWidth` 为 0.1mil，typed
  action 按 polygon 命令角色归一化到 mil；`ARC/CARC` sweep 和 `R` rotation 保持 degree，
  nested contours 递归保留。每个 fill 返回单位字段和 `geometryKind`；`fill:false` 保留为带
  线宽的 `stroked-thermal-spoke-path`，不能按填充面解释。只有完整 inventory 返回真实 `[]` 才是 known-empty；fill、
  boundary、net、layer、polygon 或关联 ID 任一缺测均为 unknown/error。
- `pcb layout-plan` — schemaVersion 1 做纯布局；schemaVersion 2 保留历史模块；schemaVersion 3 的 `crystal-guard` 要求 `groundImplementation=tracks-vias`，在局部坐标完成器件、OSC、GND 护环/导线、双层 no-pours 和接地孔后整体平移，输出 `candidate-XX.svg`（整板）、`.local.svg`（局部组装）和
  `.compare.svg`（前后对比），三者与 apply 共用候选几何。`crystal-guard` 的 no-pours 包络
  包含最终 signal-main 的“线宽一半 + live 净距”stroke bbox，并保留 owner 侧信号入口；
  `replacePrimitiveIds` 必须精确覆盖 fresh baseline 两条 OSC 网的全部 track/arc ID。
- `pcb module-check` — 离线比较 before/after fresh dump、候选与 apply journal；检查遗漏/
  额外对象、非目标变化、no-pours 内实际铺铜与静态 fill、OSC ordered path、fresh pad 几何、
  capture PID 一一对应，以及 polygon/holes/ARC 等价。schema-v2 的 `affectedBaselinePours` 把
  可局部重建的既有材料化铺铜绑定到 boundary/materialized ID 和 `impactEnvelope`：只允许声明
  对象在包络内变化，区外及未声明对象严格保持。晶振 GND 会逐段证明 `role=guard` 实际 track
  经列出铜连接到 ground anchor，并逐个验证每个 fence/anchor via 在
  TOP/BOTTOM 实际 GND 铜上与 ground-anchor 同岛，而不是只验任意 via 或两条入口。官方 DRC
  仍须单独运行并按对象/错误类型保存证据。

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

这些是 `Mutates` 动作。即时读取可能带 `staleRisk`，可用于诊断；最终约束证据使用
`pcb save → doc reload → list/report`。

## Board（板子/组合 — 原理图↔PCB 绑定）

一个 **Board = 1 张原理图 + 1 块 PCB**，原理图与 PCB 就是通过它「组合」在一起（`import_changes` 也沿此链接同步）。Board 以**名称**标识。CLI：`easyeda board …`。

- `board.list` / `board.current` — 列出全部组合（名称 + 原理图 + PCB）/ 当前组合
- `board.create` — 把原理图和/或 PCB 绑成新组合（`--schematic` / `--pcb`）；游离 PCB 在 `import_changes` 前的修复手段
- `board.rename` — 重命名组合（`--name` → `--new`）
- `board.copy` — 复制组合（连同原理图 + PCB）
- `board.delete` — 删除组合（**需确认**，无 undo）


## PCB 属性同步（现有契约）

- `pcb.component.attrs_backfill` — **PCB 器件属性回填（器件标准化 PCB 侧）**。平台 sch→PCB 导入把 otherProperty 建成**键在值空**（Value/耐压/精度/Datasheet 全 ""），且原理图实例属性值 save/reload 后同样为空（不可作源）——唯一稳定源是 **device 库记录**：按实例 C 号 `getByLcscIds` 解析，只填 PCB 侧空值键（手改值优先，`--overwrite` 强制），全程 PCB 前台。无 C 号器件跳过并报告。`pcb import-changes` 成功后**自动跑**（`--no-sync-attrs` 关）。⚠️ **平台投影键绝不参与 merge**（`Designator`/`Unique ID`/`Name`/`Add into BOM`/`Manufacturer*`/`Supplier*`——它们存在顶层图元状态；库记录的 `Designator:"C?"` 占位键灌进实例会被平台同步成图元位号,一板位号全灭 = 166/166 U? 事故真因,2026-08-09 根治）。CLI：`easyeda pcb sync-attrs [--overwrite]`
- `pcb sync-designators`（`pcb.components.list` + `pcb.component.modify` 编排,无新 action）— **修占位位号**（`U?`/`C?`）：按 `uniqueId`（平台首次导入铸造、跨文档同一命名空间）从原理图回填。只动占位符（手设真实位号绝不覆盖）；每笔回读验证；修完立落 `pcb.save` 检查点；原理图侧同为占位符的件归类「先标注原理图」。`--dry-run`/`--json`（Failed>0 非零退出）。`import-changes` 后自动**殿后**跑（在 attrs 之后,`--no-sync-designators` 关）。CLI：`easyeda pcb sync-designators`

3D 模型导入的 `/action` 请求体上限为 **32 MiB**（#199），计算的是包含 base64、
文件名和其他字段的整个 JSON，不是原始模型大小。base64 约膨胀 4/3，
因此原始模型必须小于约 24 MiB，并给 JSON 字段留出余量。超过上限会在 daemon
入口拒绝，不会交给连接器；请压缩/简化模型或使用库中已有模型。
### `schematic.pin.repair_marker`

受保护的单脚标记支路替换。输入含页面身份、稳定组件/脚、旧 wire+marker 的完整坐标/ID、
目标 kind/net/direction/offset 和源快照哈希。daemon 在同一互斥区间内读取基线、验证旧对象，
删除旧支路、创建新支路并回读；目标 finding 必须消失，范围外对象与旧 finding 必须不变。
部分写入如实返回，不能重试或声称回滚。只由 `sch layout-edit --playbook` 生成；普通修线不手写。

## 工程打开与原生导出

| Action | 输入 | 结果与约束 |
|---|---|---|
| `project.open` | `projectUuid`、`allowDiscardUnsaved:true`，可选 `pageUuid` | 官方打开后核对工程；指定原理图页时等待树就绪并核对页面；先保存所有文档 |
| `project.export` | `projectUuid` | 仅导出当前匹配工程，前后核对身份；返回 `uuid/format/size/base64`，最大 16 MiB |

CLI `project open --project-uuid` 与 `project export` 封装上述 action；导出 CLI 负责 ZIP/CRC 校验、禁止覆盖与 SHA-256。MCP 使用 `easyeda_project_transfer`。需要包含 handler 的连接器，无调试脚本回退；具体参数与恢复验证边界见 [project-import.md](project-import.md)。

## 原生原理图 DRC 的判定与覆盖

`schematic.drc.check` 的 `passed` / `nativePassed` 采用宿主布尔重载在指定 `strict` 下的判定。详细模式另取统计，两次 SDK 读取不是原子快照；检查期间不要并发修改工程。非严格通过并不代表零告警。
`countsAvailable` / `detailsAvailable` 区分统计和逐项明细；仅布尔结果的 `summary` / `fatal` 为 null，不能把未知填成零。聚合 count/type 不能用来猜规则或对象，`schematic.check` 不替代原生规则。调用失败不能作为通过。
