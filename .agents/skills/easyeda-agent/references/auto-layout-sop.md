# 原理图数据计算与 Apply 验证流程

新设计与整页重建使用 1.4 数据路径。数据契约见 [schematic-data.md](schematic-data.md)，
坐标、紧凑标题与存量工具边界见 [schematic-placement.md](schematic-placement.md)。
本流程遵守 [数据驱动架构基准](schematic-data.md#数据驱动架构基准)，
不要求先运行 `autolayout` 或按固定分区拆页。检查失败回改源数据/采集/算法后重算，
不是转为现场逐件试摆；每次恢复先找到源输入和生成记录，不能仅从上次截图继续。

## 1. 准备电路与测量数据

确认工程和目标页，保留完整工程 connectivity 与每页的几何快照：

```bash
easyeda doc ls --project <project> --json
easyeda sch connectivity --all-pages --project <project> > project-connectivity.json
easyeda sch list --project <project> --page <page> --stay \
  --include-device-identity --include-pins --include-bbox --include-wires \
  --include-page-primitives > page-before.json
easyeda sch designator-geometry --project <project> --doc <page> --out designators.json
easyeda sch sheet-geometry --project <project> --json
```

纸张门禁需从 typed 官方读取得到**红色绘图区内框**及图签真实占位；只有纸张外尺寸
或图签 keepout 时，保守内缩矩形只能用于离线探索，不能记为内框入页验收通过。
缺少精确内框 getter 时保留原响应并标 `unsupported`，先补采集能力再做现场写前门禁。
如需调查内置图框的原始符号，可由 `sch list` 的 sheet 组件取 `symbol.uuid/libraryUuid`，
使用 `lib symbol export-source` 保存官方 `.elibz2` 原包；该导出目前仅为 source-only 证据，
不得将其或 A4 纸张外框直接填成 `sheetBorder`。命令边界见 [actions.md](actions.md#图纸与明细表)。
若内置符号文件不可导出，可用 `project export-source --uuid <current-project-uuid>`
保存官方 `.epro2` 原包，离线检查当前 `SCH_PAGE` 与关联 `SYMBOL` 的真实记录；仅在实际
图元可识别并与现场只读显示核对后，才可建立精确内框和图签占位。

在副本中依据官方典型电路补齐器件、引脚和网络；修复非标准位号后再布局。
外围要围绕核心引脚并直接接线。已有网络与显式 NC 保持可追溯，不能把缺数据当作悬空或 NC。
按导出结果的 parent ID 把可见位号 bbox 放入相应源测量的 `textBboxes`；导出失败先补
采集能力或数据，不把空数组当作没有位号，也不以文字宽度估计通过最终数据门。
临时输入、计算结果和回读证据保存在项目忽略的目录，原快照保留不覆盖。

## 2. 离线计算模块与单页组合

普通 zones 的本地效果先走固定链路：
`layout-plan --zones → layout-sheet-plan → layout-render`，所有区完整通过才出效果。
输入顶层 spacing 统一内边距、框间距和页边距；区内回退只影响本区，整页仅平移区框。
纸张默认 `--flow z`：输入功能顺序从左到右、同行顶齐，下一行按该行最高框推进；
不补短框下空洞，不回填旧页。同页集合按最早成员聚拢、成员顺序不变，整体试放或换页。
旧自由装箱需显式 `--flow compact`，不能为减少页数悄悄改变用户要求的 Z 型阅读流。
修改阅读流后重新生成 pages，不能直接沿用仅通过碰撞检测的旧 sheetPosition。
用户只授权预览时止于离线结果，不执行下文 Apply。诊断模式不能替代完整候选；
保留源数据、参数、源码提交和输出哈希，使相同输入能重现同一图面。
复杂直连网络在源输入顶层使用可选
`routing:{"maxExpandedNodes":200000,"maxReroutes":4}`；省略即采用这两个默认值。
该预算按 zone 隔离，5 raw 方向网格的 40/80/160/320 raw 包络扩展、全部 direct 网络、
撤线重布和允许姿态尝试共同消费，不能在失败后重置。先保存 `--report`：它必须能重放
失败局部布局、未连接的指定物理线岛、候选路径摘要与逐边拒绝证据，但诊断数据不能交给
compose/Apply。报告为预算耗尽或限定范围无路径只表示有界失败；修算法/源约束后从本阶段
重算。失败命令不得生成或覆盖几何输出。
每次重算都把原始输入另存为不可覆盖的候选文件，报告也使用对应的独立文件名；
先核对报告 `sourceSha256` 与该输入文件**原始字节**的 SHA-256 相等，再解释冲突或
进入下游。修改通用的 `zones.json` 后，旧报告只能保留为失败记录，不能继续作为
新候选的参数归因或写前证据。对所有成功页也执行同样配对核验。
候选预算报告若给出 `terminal-conflict`，它是最后一次已观察到的具体终端冲突，
并不一定来自耗尽预算的那次尝试；其 `preRegenerationLayout` 是撤销临时线/标记前的
搜索检查点，不是命名失败时的完整终局几何。若局部命名候选刚好耗尽，只报告资源停止，
不凭空断言无安全引线。`candidate-budget-exhausted` 仅表示有界搜索停止。只在已回读到完整归因、且当前候选中
确有可移动阻挡器件时，为定向迁移保留剩余候选；命名或未知归因失败继续共享预算内的
保守回溯，不把未使用的预留额度当成布局无解，也不放松实测位号的闭区碰撞。
`naming-conflict.namingLayout` 保留该次真实命名失败的导线/标记检查状态，区别于放置阶段的
`preRegenerationLayout`；它可能只有部分标记，只用于诊断，不能作为成功候选交给 Apply。
`observedMarkerBlockerRefs` 是实际候选引线/标记与本体、文字、引脚或导线发生拒绝时的对象归属；
导线归属来自被拒绝线段所在的真实物理线岛，不把其他同名但未连接的器件并入阻挡对象。
它不证明这些对象封死全部路径。求解器可在同一预算内试移这些已观察对象并重算完整线树，
不能按距离或同网猜阻挡器件，也不能把试移成功当作此前候选合法。
命名迁移的完整重算若只因 4,096 候选分片耗尽，后续探测窗口可依次增加到 8,192/16,384；
实际耗用仍从同一目标额度和总余量扣除，报告记录重算额度与耗用，不把截断结果当作几何失败。
明确 `attachTo` 与自身 `pinNumber` 的非地同网引脚对，在撤线重算后仍须有真实物理路径；
局部电源允许分岛命名不取消这项附属连接。求解器只补该指定引脚对，用同一布线内核和
共享额度，不把其他同名电源岛强制并入，也不改源网策略。
命名引线失败时，若具名物理岛的测量端点可归属，求解器先找岛内非核心外围，或
显式附着在该岛同网引脚上的外围；沿离核心更远的第一个 5 raw 网格试移该外围及其
附属子组，撤销临时导线和标记后全量重算。此探测只用共享预算中的小额额度；失败
继续正常回溯，不能把同网但无显式所有权的器件当成阻挡对象。
direct 放置前沿、整网撤线重布和阻挡器件/attachment 刚体迁移都由同一内核执行；迁移先试
主轴向外 5/10 raw，再按 5 raw 扩展到 40 raw。已合并线树可从真实中段/T/端点垂直接出
命名，但命名成功不能反向证明 direct 已连接。检查报告中的指定线岛合并证据仍是进入 Apply 前
必须核对的连接不变量。
所有具名 attachment 的临时线岛都要保留可继续接出的真实网格出口，包括局部电源、地和
模块接口；两个相向引脚之间只有 5 raw 的连线虽能连通，却没有可用中段 T 点，不能因此
接受会封死命名出口的外围位置。这个局部检查不代替最终引线、文字净距和完整连通检查。
允许姿态菜单先保留原测量姿态，再按显式 attachment 引脚计算一次面向宿主的协调姿态；
仅使用源中授权的旋转，子件随父件的计算姿态重算。若原有均分窗口不足，该优先候选的窗口
提升到剩余额度的一半与 20,000 的较小值；原有均分窗口更大时保留它。后续候选继续共享
余量，不重置预算或改写原测量。
完整候选的命名检查在余量足够时保留至少 4,096 个候选的单次窗口，避免较小的姿态额度
被再次切碎而始终无法完成一次检查；不足时只用实际剩余量，不增加总额度。
用户确认拆出完整功能子电路时，先仅修改成员归属与边界绘图策略，保留 pin→net/NC；
需要相邻阅读时声明 placement.samePageAs 与 preferAdjacent，再走相同完整出图链路。
若拆分使核心接口同侧留下多个同名信号 `module_port` 引脚，区内求解必须先把它们合成真实
线树，再在边界命名；不能用逐引脚同名标签代替。源数据仍保留 `module_port`，这一临时提升
只发生在求解副本，回读时同时核对区内物理线岛合并和跨区网络不变。
比较拆前/拆后的主区及子区框面积、整页总框面积、总线长、页数与其他区几何不变量。
拆区成功不代表对称、对齐等软目标已经达成；图面未达到的目标单列，不手填坐标掩盖算法结果。

用户要求方向选择/面积压缩时，在源输入声明 optimization 与外围 allowedRotations，
由程序计算最多 4 个完整区内候选，再由 Z 型纸张层选择；详见数据契约的有限形态候选。
每个候选都保持原直连引脚组，不因同名标签仍能联网而接受拆线瘦身。保留原合法基线，
同时报告尝试数/停止原因、各区宽高面积、线长、最终页数与未改善区域，不只展示最好局部。
候选几何、框和连接必须整套选择；禁止在渲染脚本里旋转符号、缩框或用其他方案导线拼接。
每页完整固定渲染与重复计算一致性验证通过后才交付本地效果，不证明 EDA 已 Apply。

已确认 `layout-sheet-plan` 页时，将该页选中几何原样对应为 composition 的 modules，
补齐同页 canonical 连接核心与新鲜身份/纸张证据；使用下列固定转换入口，不再次求解。
page.json 是 pages[] 中的一页，不含候选包；间距、框、标题、位置均必须与预览一致。
新鲜纸张或现场连接改变时先处理差异，不能改快照来匹配旧预览。
每页还应从该页 `sch titleblock-get` 取得可写字段名，把图签文本放入本页
composition.json 的 `titleBlock`；转换器在 strict gate 前生成 typed 写入和回读步骤，
不在生成后的 apply.json 里手插图签命令。

```bash
easyeda sch compose --from composition.json --layout-page page.json --out plan.json \
  --before page-before.json --replace --playbook apply.json
```

该入口仅离线验证与刚体平移，仍复用可检查的完整 Apply 队列；不自动创建/合并/删除页面。
转换功能仅在新源码中存在时，可离线编译但不能据此声称安装版已支持；实际执行前须用
当前版本 CLI 完成队列 dry-run，并以 `--help` 核对安装态命令签名。需要安装对账时显式运行
`easyeda update --check`；版本状态不许可或拒绝普通 Apply，也不强制新开会话。

尚未确认纸张位置的 Lib 可用 `sch lib-layout` 计算局部几何，再用默认 compose 组合；框按各自内容压缩上下空档，
按功能顺序排 Z 字行，同行顶齐，下一行按上一行最高框推进，不统一拉高。
提供实测 `sheetBorder` 后，虚线笔画到红色图纸内框最少留 10 raw。
标题使用粉色 0.2 inch，方框使用粉色虚线；当前不生成 Notes。

```bash
easyeda sch compose --from composition.json --out plan.json \
  --before page-before.json --playbook apply.json
```

目标页与计划不同且任务已授权重建时，加 `--replace` 生成带清页守卫的队列；不要先自行
清空页面来绕过差异检查。已完全匹配时复用电路；器件匹配但尚未布线时由生成器核验是否
满足复用条件。装不下应修改模块几何或按功能拆页，compose 不自动迁页或删除源页。
整页替换的 `--before` 必须含新鲜的完整页图元清单。生成器和执行队列核对元件、引脚网络、
导线几何及网络、标记和其余图形的 ID/状态；`sch clear` 删除前再次核对图元清单。
任一枚举失败、缺项或现场变化都停止，重新采集快照和生成队列，不能只凭相同器件集合继续清页。
普通整页 clear 遇到独立嵌入对象或孤儿属性会在写前拒绝；属性全局枚举与逐父枚举
对不上、嵌入文件内容无法可靠读取时也拒绝。此时先补 typed 能力，不把空清单当作完整证据。
某些宿主的属性全量枚举会返回 `KeyVisible` 等状态为 `undefined`。采集器须按属性 ID
通过官方 typed `sch_PrimitiveAttribute.get(id)` 复读；复读仍非官方允许的值时拒绝快照和清页，
不能把 `undefined` 改写成 `null`、省略可见性或按默认值猜测。

## 3. 执行与回读

```bash
easyeda sch apply apply.json --dry-run
easyeda sch apply apply.json --yes
```

预览应显示正确的工程/页面、预计操作与全部守卫；`--yes` 仅用于已获授权的动作范围。
生成的保护队列必须完整执行，不能改目标、`--resume` 或 `--from/--to` 跳过验证。
失败时保留 journal，读取实际结果后重生成计划；已成功的写不会自动回滚。

Apply 负责清页残留检查、放置后 ID/Role 绑定、接线前实测 pin/bbox 检查，以及电气与图形
回读。超时或 `partial` 先核实实际状态，不能盲目重复 place/connect。若只补框标题，
用 `sch frame apply/check`；它只操作自己登记的图元。

进入 Apply 前检查 layout 报告的末态分类必须为成功，并确认所有 direct 网络的指定源/目标
线岛已真实合并、无剩余失败线岛；`data-missing`、`expanded-node-budget-exhausted`、
`no-path-within-bounds`、`final-validation-failed` 任一存在都停止。dry-run 也不能消费
diagnostic/blocked/partial 布局；先修复源数据、采集或算法，再重新生成完整受保护队列。

## 4. 验证代码转换效果

1. 对照目标 IR 与实际 connectivity：组件身份、pin→net、NC 必须一致。多页逐页读取，
   检查迁移后的页面归属和全工程位号；离线 diff 通过不能替代实际写入证明。
2. 逐页保存 `layout-lint`、`sch check`、`bridge-check` 和 SDK DRC 结果；`sch gate` 可作为旧脚本
   的聚合显示。`blocked` 表示检查未完成，未执行的项目明确列为待验证。
3. `sch frame check` 核验矩形、标题、颜色、虚线及必检文字净距；另对实际数据检查
   核心/外围归属、直连保持、位号入框和遮挡。型号/参数等非位号属性不参与布局检查。
   `sch export-image` 仅辅助审阅；若发现漏检，先补原始数据采集、规则和回归再重算，
   不能用人工看图补签缺测项。覆盖不足不得称完整通过。
4. `sch save` 返回 `saved:true`。保留输入、生成队列、回读和验证报告，报告仍未覆盖的限制。

只整理已有连线的小范围区域时，可按 [schematic-placement.md](schematic-placement.md)
选带连接的移动工具；先记录源目标与变更，完成后同步源数据并保存前后 topology/NC/几何对照。
未闭合可重复生成链不能记为算法验收通过。不要用只移动器件的工具替代连接迁移。
