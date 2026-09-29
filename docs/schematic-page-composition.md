# 单页 Lib 组合与 SCH Apply

本页描述转换器；上游设计与修复遵守
[数据驱动架构基准](../.agents/skills/easyeda-agent/references/schematic-data.md#数据驱动架构基准)。
普通 zone 先走 `layout-plan --zones → layout-sheet-plan → layout-render`；确认的页用
`compose --layout-page page.json` 固定转换，不能再调用默认重排覆盖选中几何/spacing。

`easyeda sch compose` 把完整的 1.4 电气模型和各 Lib 已设计的局部几何合成一页原理图，
输出布局 JSON，并可编译顺序执行的 `sch apply` 队列。它负责模块组合和数据转换；
核心器件与外围电路的连接、局部摆放及朝向必须已在输入中确定。

## 输入契约

```text
schemaVersion: 1
connectivity: 完整的 1.4 IR
sheet: {minX, minY, maxX, maxY}
sheetBorder?: {minX, minY, maxX, maxY}
keepouts: [{minX, minY, maxX, maxY}, ...]
modules: [
  {id, title, titleMetrics?, placements, wires, flags, terminals?}, ...
]
```

| 字段 | 约束 |
|---|---|
| `connectivity` | 必须有目标 `projectId/documentId`、稳定器件/网络 ID、全部物理引脚及 `connections`。每个引脚恰好有网络、明确 NC 或有原始证据的 `connectionState:unconnected`；已知悬空保留电气警告，不能用 NC 或悬空掩盖未知数据。 |
| `components[].device` | `libraryUuid/deviceUuid` 必须来自已解析的器件库身份。放置实例 ID 不能当库 UUID 重放。 |
| `connectivity.modules` | 用 `coreComponents/peripheralComponents` 引用器件 ID；与几何模块成员逐项对应，每件只归属一个模块。 |
| `sheet` | 目标页实际纸张 bbox；Apply 前再次核对。坐标单位为 0.01 inch，y 向上。 |
| `paperless` | 可选 `true`，仅用于 typed 回读确认目标页 0 个 sheet 的情况。省略 `sheet/sheetBorder/titleBlock`，保留空 `keepouts:[]`；输出中的 `sheet` 只是从全部模块内容派生的虚拟校验包络。默认依次排成一行，仍检查真实导线和跨网接触；不能与 `--layout-page` 同用。 |
| `paperlessRowBreaks` | 仅在 `paperless:true` 时可用；例如 `[4]` 表示第 5 个模块开始第二行。索引从 0 起，必须严格递增且在模块列表内部。整块平移、同行顶齐，行间距 10 raw；不改变模块内部导线、标记或器件姿态。 |
| `sheetBorder` | 可选的实际图纸内边框 bbox，四个坐标须显式提供有限数值，不能缺省或为 null，且必须位于 `sheet` 内。模块虚线笔画与该边框至少保留 10 raw 净距；缺少整项时只沿纸张 bbox 排版，输出 `sheet-bbox-fallback`，不代表红框净距已经验证。 |
| `keepouts` | 纸内禁止占用的区域，例如图签。记录其来源和可见状态；空数组表示确实没有禁放区域。 |
| `modules` | 数组顺序就是功能阅读顺序，不按旧页面或旧 XY 排序。 |
| `placements[]` | `designator/value/x/y/rotation/mirror/bbox/pins`。器件与引脚坐标落在 5 raw 网格；bbox 使用官方实测几何。`pins` 为完整的 `{number,name,net,x,y}` 数组，NC 引脚的 `net` 为空。旧 `primitiveId` 不作为新实例身份。 |
| `wires[]` | `{net,points:[[x,y],...]}`；正交折线会拆成单段。网名用于本地校验，实际线树由相连的电源符号或网络端口命名。 |
| `flags[]` | `{net,kind,pinX,pinY,direction,offset}`；`kind` 使用 `power/ground/net_port_in/net_port_out/net_port_bi`，方向为 `up/down/left/right`，偏移为正的网格长度。转换会生成真实引线。 |
| `terminals[]` | 可选 `{designator,pin,direction,kind?}`；引用本模块的已测引脚，网名从连接核心读取，默认 `kind:net_port_bi`，也支持 `net_port_in/net_port_out/power/ground`。必须指定向器件外侧的方向，不能重复声明或与已有线/标记重复接线。 |
| `titleMetrics` | 可选 `{title,fontSize,width,height}`，仅接受同标题、同字高的实测数据。没有实测时使用保守字宽预测，Apply 仍检查实际文字 bbox。 |

连接模型见 [概念约定](concepts.md)，框与标题字段见
[模块框转换](schematic-frame-conversion.md)。局部电路应先用真实引脚几何设计：
外围电容、电阻直接连接核心器件，电源/地就近放标记，边界信号可用网络端口。
每个有网络的引脚都必须经真实线段到达同名标记；在 JSON 导线中填写网名本身不构成连接。

位号必须逐字匹配输入 `component.ref`，保持正常数字位号的大小写、前导零和原有风格；
功能名称或含下划线的非标准位号在写入前被拒绝，应按官方库前缀修复并将功能名保存在 `role`。
组合器不添加模块前缀、不重排正常编号。位号修复由 `sch designators` 独立处理。器件和 Lib 成员按
声明顺序传递；`group create --if-absent` 按精确位号集合判幂等，换序不重写已有登记。

无图框且完全未接线的既有页面使用 `--replace --preserve-instances`：完整快照证明
0 导线、0 标记、所有引脚均未赋网且没有 NC 后，队列跳过清页，保留每件原生实例，
移动并写入 NC/导线/标记。已有图元走原有受保护路径，不把部分接线冒充空白页。
这条路径的电气、bridge 与 DRC 门仍执行；严格纸张边界门因没有图框而不执行。
保存重载后需以同一 composition 再编译对账，不能只看 dry-run 或离线候选。

端子直出在框计算之前完成：从 10 raw 起按 5 raw 增长，选最短合法直线，最长
300 raw。标记本体和文字保留 5 raw 净距；检查全部器件、引脚、已有线段和标记。
邻脚标签采用错落线长避免重叠，不生成折线回退。不能直出时先调整源数据中的
器件位置/朝向；规划器不会擅自旋转器件、重连网络或删除已有线路。

## 排版与固定贴边尺寸

下述 10 raw 是未传 `--layout-page` 的默认 compose 契约；两层模式由源输入 spacing
统一控制，固定转换保留该值。源数据/已选页的身份、完整几何和纸张必须一致。

框内最小边距、模块间距、行间距、标题内缩及图签净距均为
**10 raw = 0.1 inch = 2.54 mm**；标题与内容净距为 5 raw。
这些尺寸由共享常量定义，当前 CLI 不提供逐模块调整参数。

纸张外沿与图纸内边框分别记录为 `sheet`、`sheetBorder`。显式提供内边框时，
规划先从它向内保留 10 raw 净距，再加模块虚线的 0.5 raw 半线宽，最后向内
取整到 5 raw 网格；输出 `usableBounds` 是可容纳模块矩形路径的范围。
每一完整模块框都必须落在此范围内。`placementBoundarySource` 为
`explicit-sheet-border`；没有内框数据则为 `sheet-bbox-fallback`，沿用纸张 bbox
内缩 10 raw 的兼容行为并提示边界未提供。`sheet` 始终保留原始纸张几何，
不会用较小的内框替换 Apply 的纸张身份校验。

先在模块上方、下方寻找标题空档，选择合法且总高度较小的包络；标题保持
20 raw（0.2 inch），粉色 `#AA00AA`，外框同色、虚线、无填充。再从左上按 Z 字排列，
右侧放不下才换行。每个框保留由自身内容计算出的紧凑高度，同行仅顶边对齐；
下一行按上一行最高框的高度加行间距推进。短模块不因相邻模块更高而扩框。
输出 `rowHeights` 记录各行最高框的高度，`rowHeight` 仅记录其中最大值用于诊断。
平移同时作用于器件、引脚、导线、标记与标题，不改变器件朝向及任何电气连接。
网格取整可以增加少量留白，但不再使用全页最大高度扩展各模块。

图签应通过 `sch sheet-geometry --json` 获取，并保留 `source/warnings`。
运行时规则在 `internal/app/cmd_sch_sheet.go`，Skill 的 `sheet-templates.json` 是镜像。
1170 × 825 的 A4 横向图纸、显示默认图签时，当前校准区域为
`{minX:468,minY:0,maxX:1170,maxY:198}`。这是基于实测纸张和已校准模板的推导，
不能把它称为独立图签图元的 API 实测 bbox，也不能把旧的 0.22 × 0.14 比例用于验收。
该查询目前也不提供独立内边框 bbox。历史官方字段 `Border`、`Blade Width` 不能
仅凭名称就转换成已验证的内框尺寸；取得可靠的边框几何后再写入 `sheetBorder`。
离线测试使用的合成边框必须另存并标明来源，不能冒充当前工程的官方测量。

## CLI 闭环

准备输入后，布局和队列生成均在本地执行：

```bash
easyeda sch compose --from composition.json --out composition-plan.json

# 读取目标页最新几何、全部引脚和导线；include-wires 同时读取 connectivitySummary。
easyeda sch list --project <project-uuid> --page <page-uuid> --stay \
  --include-pins --include-bbox --include-wires --include-device-identity > before.json

# 已批准重建目标页时，编译包含前置状态检查的队列。
easyeda sch compose --from composition.json --out composition-plan.json \
  --before before.json --replace --playbook composition-apply.json
easyeda sch apply composition-apply.json --dry-run
easyeda sch apply composition-apply.json --yes

# 使用新回读重新编译，可验证重复执行是否已无需重建。
easyeda sch list --project <project-uuid> --page <page-uuid> --stay \
  --include-pins --include-bbox --include-wires --include-device-identity > after.json
easyeda sch compose --from composition.json --out composition-plan.json \
  --before after.json --playbook verify-apply.json
easyeda sch apply verify-apply.json --yes
```

`--replace` 允许为不同的目标状态编译恢复/重建队列，执行发生在 `sch apply`。
已有同一批器件仅改布局时，dev.6 开发路径为 `--replace --preserve-instances`，
保留原实例及属性；旧破坏性 `--replace` 不作为无损重排入口。源身份/引脚/NC/属性缺失
或不一致时必须拒绝，不能手工补队列。详见 Skill 的
[数据架构与实例保全门禁](../.agents/skills/easyeda-agent/references/schematic-data.md)。
dev.6 安装与现场验证仍须单独完成，不能由本文推定已发布或已通过。
队列先核对项目/页面、工程内位号唯一性和目标旧状态；新增位号通过 `absentParts`
检查其他页也未占用。根据新鲜回读选择以下路径：

| 目标状态 | 执行方式 |
|---|---|
| 器件、引脚、net/NC、导线路径和标记完全匹配 | 保留现有电路，继续模块框及最终验收。仅同网但路径不同不算匹配。 |
| 器件及全部引脚几何匹配，尚无接线（`reuseUnwired`） | 保留器件，复核并保存放置检查点后恢复 NC、导线和标记。 |
| 同批实例的布局/线路变化，显式 `--preserve-instances` | 有界清理绘制内容但保留纸张、原器件和所属属性，按算法目标移动并重接，逐字段检查实例保全。 |
| 不同器件设计的明确整页重建 | 清除目标页并保留纸张；检查全部图元无残余，再重新放置和接线；不宣称原实例已保全。 |

`reuseUnwired` 必须有明确的空导线、空标记和 `connectivitySummary` 证据：
`scope:activePage`、`wires:0`、`buses:0`，已有 NC 也不能与目标冲突。
执行时重新读取并断言这些计数，缺失或变化即停止，不能仅凭编译时快照继续接线。

重新放置按库 UUID 以零旋转创建，再用绝对 `component.modify` 写入最终位置与朝向；
当前 EasyEDA 的 create 会把 90°/270°反向存储，不能将测量角度直接当 create 参数。
画线之前逐项回读 bbox、朝向与全部引脚位置，通过并保存后才恢复 NC、导线和标记。
所有路径均按 Lib 登记幂等虚拟组、应用/核对模块框，检查完整 pin→net/NC、
导线路径与标记、电气及线树，再通过 `sch gate --strict` 后保存。
框/标题的幂等处理及响应丢失恢复规则见
[模块框转换](schematic-frame-conversion.md)。

## 拒绝条件与失败恢复

最终绘图核对还要求当前页 `connectivitySummary` 中 `buses/shortSymbols` 均为零，缺失计数视为未知，拒绝匹配。

规划器复用运行时的标记本体与文字带模型，检查标记之间及标记与器件的重叠，
并逐段检查普通导线和标记引线是否穿过标记本体或文字；同网也不能穿字。
合法引线可以终止在标记锚点，不用折线的整体包络代替真实线段判断。

缺失引脚、未知 NC、错误网络、未命名线树、孤立导线/标记、触及异网或 NC 引脚、
穿越器件、重复或错误模块成员、无法容纳于单页、压住图签都会阻断规划。
局部坐标可以来自不同页面、甚至处于目标纸张之外；最终组合必须全部落在可用纸内。

官方器件 bbox 不完整包含外置位号，须单独采集 Designator 的类型、parent、可见性与 bbox。
型号/参数等非位号器件属性不参与页面碰撞与框包络；不能将混合属性矩形直接填进 textBboxes。
旋转后回读位号/引脚几何，缺测不得称完整通过。导图只辅助发现采集/规则遗漏，
遗漏应补成数据检查与回归，修源几何后重新求解，而不是直接挪现场文字。

生成队列使用 `requireFullExecution`，写操作不自动重试。失败后先保存日志并回读实际状态：

1. **规划失败**：修改局部几何、补齐缺失数据或纠正网络；重新运行 `compose`。不缩小图签区域绕过报错。
2. **清空前状态失配**：重新获取 `before.json`，确认目标与源页现状后重新编译。
3. **放置后几何失配**：检查库身份、符号变体、旋转/镜像和实测引脚；修正输入后从新鲜快照重建。
4. **写入中断或响应不明**：先回读已落地内容，再编译完整队列；不能用 `--resume/--from/--to` 跳过校验，也不能更换队列目标页面。

单页容纳不足时，可按功能拆成两页：分别准备完整的单页器件子集及不同的目标
`documentId`，各运行一次 `compose`。命令每次只组合一页，不自动分页或删除源页。
其他页可保留不同位号的器件；同位号跨页重复时前置检查会停止。旧页应由迁移流程
在数据保全和器件归属确认后处理；不能将复制后留有重复器件视为合页完成。
命令也不推断缺失外围电路、不缩放符号，不以框包络检查代替实际文字可读性检查。
软件验收应同时保存离线计划、Apply 日志、完整回读及官方导图；离线通过不能写成现场闭环已通过。
