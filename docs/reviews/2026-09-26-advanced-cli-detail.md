# 高级 CLI 续测证据与实现细节

[返回主报告](2026-09-26-advanced-cli.md)。原始数据留在本机忽略目录
`artifacts/cli-advanced-dev21-20260926/`；命令记录包含 UTC、完整 stdout/stderr、退出码和耗时。
本文不替代 A00–A06 的现场设计验收，也不更新 v1.7.0 已冻结发布证据。

## 运行身份与基础门禁

现场三方为 dev.21、宿主 4.1.60；产品实现归档于 `1414784`，当前工作树基于 `884a875`。
两提交间产品 Go/TypeScript 源码未变；发布脚本、测试、版本和文档有变化。

| 文件 | SHA-256 |
|---|---|
| 已安装 CLI | `6a26c240dcb6838843836e313ea0dd747c0dfd43ddf548d685628f1cdf0251c1` |
| dev.21 连接器安装包 | `e8fd7e6758211409e628f988c24b67cb18ea66902c6fdc21141a52f37f7a4880` |
| 该安装包内 JS | `511add6882444ec7a80c5369fc9f59c8792de61bbd8c1c37f3632ec96e673193` |

`preflight/basic-evidence-integrity.json` 保存 11 项基础清单与 252 件文件的逐项检查。
独立复核同时检查已提交的 11 份单项报告哈希及 B02/B10 的完整 PCB 清理差分。
当前 `extension/dist/index.js` 与 dev.21 包内 JS 仅有两处版本文字变为 1.7.0；不能用构建目录
判断宿主安装状态。本轮没有重新采集宿主内存 JS 哈希，运行连接器版本由 health 回包确认。

原基础测试使用的是明确的夹具集，B03 本身创建新工程，并非所有动作都在单一 UUID。
本轮不能把这组历史证据套给任意新工程；选定目标后仍须检查目标页、当前基线及缺失基础能力。

## 目标与现场保护

- 原工程 `4037dd22434c412fa75d7f2d855a2ab4` 的三页原理图和 PCB 已逐页读取并 typed 保存；
  四个保存回包均 `saved:true`。记录为 `preflight/original-*-read/save.json`。
- `project find ceshi` 完整枚举 54/54，命中 `475cc0f773ed4a6fb7a02336c8a6a67f`。
  它是仓库记录的 AT32F415 基线，本轮未打开或写入该工程。
- 基础夹具 `0f46d4361c4741a9ab7a9ed62164ca9b`，名称 `ceshi-cli-basic-20260924`；
  P1 `e453c0063919726b` 只有图框、无导线；PCB `3037fc1dfa4965d2` 无器件、无板框。
- `preflight/fixture-pcb.json` 与历史 `B10/pcb-clean.json` 仅 `capturedAt` 不同，语义哈希为
  `41b23a1b3e7c54f623efa678dea430033717968444fefee90e1ccbfebce0d527`。
  空 PCB 的 `partial` 明确报告无器件/无板框，不能当作高级几何输入完整。
- 第一轮预检没有新建/删除页或电路对象。续测中的专用页补测见下文；现场安装二进制、连接器
  与 daemon 保持原状态，新加的 SVG 解析仅用源码 CLI 离线执行。

## 预检误拦的复现与修复

`preflight/basic-fixture/01-health-before.json` 同时出现：目标工程的 dev.21 窗口，以及另一工程
的 1.2.8 窗口。全局 `versionGate.verdict` 为 block，旧脚本因此提前退出；目标本身仍精确同版。
旧失败证据保留，之后的两次目标快照均为只读诊断，不能写成旧预检通过。

修复 `scripts/cli-live-smoke.py`：CLI 与 daemon 精确版本仍逐项核对；按 project/doc/type
唯一匹配窗口，再检查该 connector 的精确版本和该 windowId 唯一的兼容宿主 finding。
完整 health 保留其他窗口的警告，不改变 daemon 或普通动作的执行规则。
`preflight/basic-fixture-targeted/summary.json` 记录修复后的真实目标 PCB 预检 pass。

回归覆盖：单目标、无关旧连接器/旧宿主、目标旧连接器、CLI/daemon 变化、缺失/重复目标、
错误工程/页/类型、缺失/重复/失败宿主 finding、缺失 windowId、daemon 不可用。
`offline/05-smoke-target-tests.json` 记录 9 项通过；`offline/06-skill-check.json` 记录公开 Skill 检查通过。

## 离线回归

| 命令 | 原始记录 | 结果 |
|---|---|---|
| `go test ./...` | `offline/01-go-tests.json` | pass；部分包使用 Go 缓存 |
| `make layout-calibrate` | `offline/02-layout-calibrate.json` | pass，7 个好板/负对照基准 |
| `make lint-test` | `offline/03-lint-test.json` | pass，朝向、连接规则和正负夹具 |
| `make blocks-audit` | `offline/04-blocks-audit.json` | 1028 unique；missing/unknown/fanout 均 0 |

这些测试不调用现场设计写入，不能补签原理图 gate、四层配置、实际铜、DRC 或最终保存回读。

另用**已安装 CLI**运行纯离线合成输入，记录位于 `offline-cli/`。输入来自源码单元测试的
最小模型，明确标为 synthetic，没有真实库身份或编辑器测量，不交给 ESP32 执行 Agent 当答案。

| 专项 | 实际检查 |
|---|---|
| 原理图分区与区内求解 | `zone-review`、`layout-plan --zones` 成功；源字节哈希、唯一归属和引脚状态保留 |
| 纸张与固定转换 | `layout-sheet-plan`、选定 `pages[0]` 后 `layout-render`、20 raw 间距的 `compose --layout-page` 成功 |
| Compose | 两模块正例通过；缺失 NC/未连接意图、引脚未到命名导线树、缺失 titleX 均拒绝 |
| 原理图其他负例 | 缺 mirror、双重归属、搜索预算耗尽、页面越界均明确失败；已有合法输出未覆盖 |
| PCB 二层联合布局 | `solve/check/render` 成功，1 个候选、18/200000 搜索状态；组内成员共同移动 60 mil |
| PCB 负例 | 篡改路由端点、错误 request provenance、过期 semanticSha256 均拒绝；错误渲染未覆盖原 SVG |
| 四层能力边界 | 联合求解返回 incomplete、exit 1，明确四层尚不支持；不能据二层成功签四层通过 |

保留两次测试输入错误：第一次将整份 sheet 包装报告传给 renderer，按 help 改为选中页后成功；
固定 Compose 的首次转换遗漏源数据已声明的 power/ground 角色，工具正确拒绝不完整连接。
仅在新的源副本补齐已声明角色后重算成功，没有修改产品或放宽检查。
独立复核还重跑 9 项 smoke 测试，并将保存的多窗口 health 按各自明确目标重放验证。
最终共 34 条命令记录：12 个正例、11 个预期拒绝负例、9 条版本/帮助检查，以及上述 2 次
输入转换错误。原理图源哈希、刚体几何、网络、归属和 NC 对账通过；17 个 SVG 可解析。
`offline-cli/README.md` 是命令表，`verification.json` 是数据校验结果，
`smoke-independent-review.json` 是预检独立复核。106 件文件的 `evidence-sha256.json` 哈希为
`942b4c00587c776129c91db3c7fabc6bac379cd0ce2534c7ee1b34d8ce80889d`。

`cost/preparation.json` 已记录本次准备/只读预检成本，不是完整 E2E 成本：daemon 审计
84 次调用、31.0 秒；首次至末次审计动作跨度约 9.36 分钟。报告整理和之后的纯离线命令不在
该动作跨度内；token 未记录，不能把审计中的 0 写成真实 token 消耗为零。

## 需求输入与未决项

执行 Agent 未继承对话历史，只读取 `esp32MiniRequire.md` 第一节及通用 Skill/器件官方资料。
提取的需求 SHA-256 为 `a7b3a16850d393e8d122b13b2c54b7ceebc3ed99ae4a1c594d0b83fb6678cd90`。
`A00/proposal.md` 与 `proposal-detail.md` 保存用户方案及来源，器件仍为候选，未声称已完成库核验。

第一轮准备曾停在工程选择和叠层含义；用户随后明确回复“复用＋A”，决定已冻结在
`run02/user-decision.json`，其中绑定原方案文件哈希。原始需求、B00–B10 的范围和
PCB Layout 用户确认要求均保持，不把 S0 确认当成尚未生成的 PCB Layout 确认。

续测前的 `run02/preflight/` 共 21 件证据已冻结：原理图与 PCB 的目标预检均 pass；
P1 完整 `result` 与第一轮快照精确一致，PCB 完整 dump 仍仅 `capturedAt` 不同。
规则配置回读仍是既有的“自定义配置11”。执行 Agent 独占目标窗口，主 Agent 与复核员
同时只做离线工作。后续每个 Axx 只按本轮实际证据给结论。

## 续测：专用页补测与精确纸张边界

`run02/A00/p1-supplement/report.json` 记录 P1 的定向基础补测，14 条命令保留输入及完整回包：
从真实库放置测试电阻，读取身份/引脚/bbox，分别以真实 30 raw 导线连接命名网络与 GND；
save → reload 后逐脚网表一致。随后仅删除这次产生的两组件与两导线，再保存重载，
恢复 0 设计元件、0 网络；相对原始完整 result 仅图签自动更新时间变化。

默认 `sch sheet-geometry` 只提供图框 bbox 和比例估计标题栏，缺少精确内框。执行员通过
已有 typed `project export-source` 和全页 `sch export-image --format svg --scope page`
补齐原始数据。单独选择 sheet primitive 的 SVG 导出被宿主拒绝为 `Nothing is selected`，
失败回包保留；成功的全页 SVG 同时含补测电阻，不据电阻或 viewBox 推断纸张大小。

新增离线 Cobra 入口 `sch sheet-geometry --from-svg <全页.svg> --json`，读取官方导出的
sheet 组矩形及标题表单元格，转换为 raw、y-UP，绑定原文件 SHA-256；内框或表格缺失、
歧义及不支持的变换/可见性时拒绝。线宽不进入中心线边界，布局参数另留安全边距。
不调用 EDA、不更新现有 daemon；该新增能力的源码验证与 dev.21 已安装现场基础证据分开记录。

本页外框为 `(0,0)–(1170,825)`，内框为 `(10,10)–(1160,815)`，标题表为
`(460,10)–(1160,190)`，表格范围与原生归档 TABLE 一致。来源 SVG SHA-256 为
`dbacac9468b75c11e8aef347c9cdc3f9cd38ff062e47f3dfbedc483656176729`；原生 `.epro2` 为
`349ccf5cf26ac2054bcc3f7e25355779a59358d0b4ae0a07df2139bffcc42afb`。
最终命令及源代码哈希位于 `run02/sheet-geometry/parse-final.command.json`，几何结果为同目录
`geometry-final.json`。早期解析记录与复核发现的反例均保留，不覆盖失败证据。

独立复核发现 CSS 几何属性覆盖、嵌套 viewport/未显示 defs、重复标题表容器及空输入路径
回落现场查询的问题，均已补明确拒绝和负例。最终 26 项专项测试、10 个独立 CLI 正负例通过，
真实 SVG 的 26 个表格单元格与原生 TABLE 对齐；全量 Go 回归通过（3815 项，空路径新增
用例另在专项测试覆盖），`make skill-check` 及 diff 检查通过。
复验报告 `run02/review/sheet-svg-code-review-retest.md` 的 SHA-256 为
`4206e3ff54e8c4bf53800e834a71bef847cc648cdffc2c6861aabccbf60943aa`。
P1 清理后的完整对象结果也另行离线复算，见 `run02/review/p1-cleanup-independent.json`。

`run02/review/A00-candidate-review.md` 独立列出七项选型证据检查点，包括型号/物理脚、
电源域、防倒灌后最低输入、降压外围、USB-C、启动下载和四层天线避让。它是候选复核，
尚不能签 A00 通过；执行员继续用真实库身份及手册关闭这些检查点。

## 续测：删除残留与有界恢复

20 个器件实例采集到真实几何，其中前 19 个测量后精确清理成功；第 20 个 U4 ESP32 模组
`3826725d627082d4` 在 `sch prim-delete` 后仍存活，CLI 的 400 ms settle 内重试仍返回
`partial:true`、exit 1。执行员停写，独立 fresh list 确认同页只有 sheet 与 U4；不能称作
瞬时回读误报。原始证据为 `run02/A00/measurements/U4/04-delete.command.json` 和
`05-failure-fresh-list.command.json`。

主 Agent 接管窗口，先保存已知故障状态，再按已知 typed 恢复路径 `doc open <同页UUID>`，
fresh list 核对同一工程/页/对象，精确删除该残留成功。之后保存重载恢复空白基线，再原样
复跑 U4 放置→完整身份/引脚/bbox 读取→位号几何→删除，两次不中断序列均通过。
最后再次 save→reload→fresh 完整 list，与最初基线仅图签自动日期/时间不同。
`run02/delete-investigation/report.json` 保留恢复顺序和判定，原失败仍为 fail，根因未关闭。

首次复跑脚本把 `designator-geometry --out` 的文本 stdout 当作 JSON，导出命令已成功但
脚本解析中止；该中断及后续精确清理独立记录为 trial1，不计作连续时序复测。trial2/3 才是
两个完整复跑。本轮没有刷新浏览器、重启宿主/daemon、替换运行包或用 GUI 改工程。

修复仅涉及 CLI 残留提示：不凭幸存对象断言队列卡死；不再建议重启/GUI 删除；复核成功也
不推断首轮一定读到了旧快照。删除、400 ms 等待及已有一次重试的行为均未改变。提示修改
不是现场删除根因修复。窗口交回执行员后从空白基线继续剩余实例采集；再次失败须继续停写留证。

恢复后的 C7 完整链路成功；C8 使用与此前 C3/C6 相同的 100 nF 器件，却再次返回
`partial:true`、exit 1，400 ms 复核后仍存活。fresh list 确认 P1 只有 sheet 与 C8
`8fc4eb081c5417aa`。证据在 `run02/resumed/A00/measurements/C8/`；累计实测 22/35，
其余 13 件未测，A00 最终为 blocked，A01–A06 为 not-run，不再逐件 reopen 继续刷测。
第一轮 33 实例经电容偏压资料复核补为 35；旧参数保留，新方案只作为待完成的源数据。

`u4-audit.json`/`c8-audit.json` 按窗口冻结失败时段的原始审计。C8 失败前无自动保存插入，
前置 list 结束至 delete 的时间也与成功件相近；不据现有样本归因于 autosave、固定等待不足
或某个器件型号。两次原序列复跑中的 trial3 首轮 stdout 为 partial，CLI 既有有界复核后
exit 0、fresh list 干净，因此是整条命令通过，不是首删即成功。

最终仅清理残留：`run02/delete-investigation/final-cleanup/` 保存 typed 同页打开、精确删除
C8、save→reload→fresh 全量结果。只剩原 sheet，0 设计元件、0 导线，与最初基线仅自动
日期/时间不同；未操作 PCB 或原 AT32F415 工程。清理通过不能消除两次删除故障。
CLI 提示修复经 3 项定向测试、全量 Go 测试（3816 项）、Skill 检查和独立复核验证。
独立最终复核 `run02/review/delete-final-blocker-review.md` 确认两个原始失败、夹具清理及
未执行范围；SHA-256 为 `a6a82033020377a71f1a8fce9b1892e7b6d97b00ece58a97ede7649b89ac7672`。
执行员的 `run02/final/README.md` 汇总 17 条需求映射及 22/35 实测清单；371 件执行资料
通过独立离线校验，`manifest.json` SHA-256 为
`9f81448fea1327e11ae81f51b3c4cea22d1391229cb872ddd78705ff02bd1768`。
该清单不包含主 Agent 另外维护的审查、审计归因和成本文件，不能把它称为所有证据的总哈希。

## 续测成本

`run02/cost/` 仅筛目标窗口 18:55:31–19:28:28 UTC 的审计，过滤来源和 SHA-256 在
`scope.json`。已追加成本台账：845 次调用，daemon 累计 156.63 秒；首末动作跨度约
32.91 分钟，不包含前一轮准备、之后的文档整理，也不称完整 E2E 墙钟。token 未记录。
`audit cost` 的原始失败计数只看响应 `ok:false`，没有统计 `ok:true/partial:true` 的效果失败；
`effect-failure-note.json` 另列删除 partial 回包及两个最终失败的 CLI 命令，不能将成本表中的
删除 failures=0 写成删除全部成功。
