# 基础 CLI 真实链路测试：前置条件与步骤

[返回简版](cli-live-test.md)。本表测试 CLI → daemon → connector → 官方 `eda.*` API →
CLI 回包，以及写入后的 save → reload → fresh readback。全部工程写入走 typed CLI 或
受保护 Apply；同一 Web 窗口一次只执行一条 typed 命令。`--help` 和离线单元测试只证明
命令存在，不计入现场通过。

## 全局前置条件

1. 用户已在 Codex 内置浏览器打开 Web EasyEDA Pro V4 测试工程，连接器已启用外部交互；
   不启动桌面版。CLI、daemon、运行中的 connector 精确同一 `dev.N`，测试源码/Skill
   与该版本对应。记录 Git commit、版本、宿主版本及 `health.windows` 的工程/页 UUID。
2. 使用独立测试工程和独立原理图/PCB 页，保留测试前完整快照与语义哈希。写入例的
   器件型号、网络、尺寸、位置、规则及单位来自参数文件和当前官方回读；不复制旧板绝对坐标。
   创建工程若返回空 UUID，先用 `project find` 完整查证，身份不明时停止。
3. 安排一名主执行员独占目标窗口；独立复核员在主执行员停止访问该窗口后只读核查。
   执行员只收到本用例、公开 Skill、专用工程和必要原始需求；不提供旧结果作为答案。
4. 每条命令保存原始 stdout/stderr、退出码、时间、适用的参数文件/快照 SHA-256、目标工程/页 UUID、
   返回 `context`、关键对象 ID 和前后完整对象差分。失败或超时先 fresh 回读，禁止盲重放。
   结论只用 `pass` / `fail` / `blocked` / `not-run`；`offline-only` 不计现场通过。
   单条命令可用 `python3 scripts/cli-live-record.py --out <新证据.json> -- easyeda ...` 执行；
   它保留命令、UTC 时间、原始回包和退出码，拒绝覆盖已有证据。同步保存 `health` 与
   输入文件副本，才能让独立复核员重算版本、上下文和哈希。
5. 写测试先看 `easyeda <domain> <command> --help` 确认当前签名。所有可写命令显式传
   `--project <UUID> --doc <UUID>`；创建尚不存在工程时只传 `--window`，不用假工程/页路由。
   现场检查和截图不替代对象回读。PCB Layout 完成仍需用户确认回读版本后才能测整板布线。

### Codex 执行与复核输入

执行 subagent 使用新上下文（`fork_turns: none`），只给本清单、公开 Skill、专用工程/页
UUID、原始需求和版本号：先跑 C00；前置条件不成立就停在该例并留原始响应。逐例从参数重算，
写前保存基线，写后读回，失败不盲重放。主 Agent 不同时访问该窗口。独立复核 subagent
也用新上下文，只给冻结的输入、命令回包、Apply journal、save/reload 与 fresh 回读；
它逐例独立判定，不以执行员结论为输入。需要现场只读复核时，主执行员先停止访问窗口。

## 测试步骤

| ID | 专项前置条件 | 串行操作 | 通过判据 |
|---|---|---|---|
| C00 发现与路由 | daemon 已启动、连接器已连接 | `--version`、`actions`、`health`；运行只读预检脚本；分别查 `project info`、`project doc`、`sch list` 或 `pcb list`，最后再查 `health` | CLI/daemon/connector 精确同版；两次窗口身份一致；CLI 回包 `ok:true`、`context` 精确指向测试页，读取到真实对象；任何版本漂移停止写入 |
| C00R 重连 | C00 通过，所有测试页已 typed 保存 | 按[本地运行步骤](codex-web-eda-runbook.md)重启同版 daemon，等待 connector 重连；重跑 C00 的 health、身份和对象读取 | 新连接只指向原工程/页；版本仍精确同版，原始对象语义不变；旧 `windowId` 不当持久身份 |
| C01 命令契约 | 当前源码和安装包一致 | 查 `project/doc/sch/pcb/lib/bom/apply/audit` 及本轮子命令 `--help`；与 `actions` 的 typed 动作对照 | 所选命令和参数存在，未知命令/坏参数非零退出且未调度设计写入；记录新增能力在 Skill 的入口 |
| C02 工程与页面 | 空白专用工程名、团队及父 schematic UUID 已确定 | `project create --window ... --name ... --open`；`project find --window ... --name ... --team ...`；`sch pages`、`sch page-new --schematic ...`、`doc ls/open`、`project export-source`；读新页完整对象 | 创建回包与 find、health、页清单 UUID 一致；空白页对象可读，工程导出 artifact 可验证。部分成功、重名或不完整枚举不重试 |
| C03 元件库与选型 | 原始需求给出器件规格；候选来自库查询 | `lib search` / `lib by-lcsc`；读取候选 device/符号/封装/引脚；选定后才 `sch place` | 放置回包的 `primitiveId`、位号、device 身份和 fresh `sch list --include-pins --include-bbox` 一致；无变体歧义、无重复位号 |
| C04 原理图写读 | C03 已得到实测引脚与参数化连接表 | `sch connect`/`no-connect`、必要的 `modify`；`sch list --include-wires --include-page-primitives`、`sch connectivity`、`sch read` | 逐物理脚对应预期 net 或 NC；标记由真实非零导线连接；写前/写后对象差分只含声明目标 |
| C05 规划与保护执行 | 原始快照、源数据及配对 SHA-256 已冻结 | `sch zone-review`、`layout-plan`、`layout-sheet-plan`、`compose`；`sch apply --dry-run` 后执行受保护队列 | 报告源哈希与输入原始字节一致；所有权、direct 线树、位号与边界检查通过；Apply journal 步骤全成功，现场对象与源逐项一致 |
| C06 原理图质量与持久化 | C04/C05 的写入已稳定 | `sch layout-lint`、`sch check`、`sch bridge-check`、`sch drc`、`sch gate --strict`；`sch save` → `doc reload` → fresh `sch list/read/check`；`sch export-image`、`sch netlist`、`bom export` | 0 overlap、0 fatal，网络/NC 对账；`saved:true`；重载后身份、对象、网络和图签保持；导出 artifact 路径、大小和 SHA-256 可读 |
| C07 板创建与同步 | 原理图已保存重载，目标 schematic 未被其他 board 绑定 | `pcb new-board --schematic ...`，切至新 PCB；`board-info`、`pcb docs/list/layers/nets`；按真实回读选择 `import-changes` 或 `add-component` | board、schematic、PCB UUID 链接正确；每个 footprint 的 uniqueId、焊盘编号和网络与原理图相符；空 UUID/部分创建先对账 |
| C08 PCB 规则与布局 | C07 的板、真实封装 bbox/pads、板框约束齐备 | `pcb stackup show/set`、`pcb config get` 与规则 dry-run/写入；`pcb outline-set/get`、参数化 `pcb layout-plan`/`pcb layout check`、候选 Apply；`pcb dump`、`pcb layout-lint`、`pcb layout-score`、`pcb stage-snapshot` | 层数/层类型、规则和板框经回读；器件无越界/重叠，封装与网络关联未变；整板预览两轮连续自检，第 2 轮 save→reload→fresh dump→fresh render。用户确认该版本前停在此阶段 |
| C09 PCB 铜与检查 | C08 的持久化 Layout 已得到用户确认；线宽、间距、网络和 keepout 参数已定 | typed `track`/`via`/`region`/`pour`，或参数化 `route` 候选 Apply；`track-list`、`via-list`、`pour-list`、`poured-list`、`net-path`、`pcb check`、`pcb drc` | 新鲜铜对象 ID、net、layer、几何与输入一致；没有声明外铜变化；连通、间距、禁布区及 DRC 满足原需求；缺测或路由失败按事实记录 |
| C10 PCB 落盘与导出 | C09 所需检查已通过 | `pcb save` → `doc reload` → fresh `pcb dump --include-copper`/`pcb drc`；`pcb snapshot`、`pcb export-dsn`、`pcb report` | 对象、铺铜材料化结果和规则持久化；导出 artifact 有实际大小及哈希；不能用截图证明连接、DRC 或落盘 |
| C11 保护负例 | C02/C06/C08 的稳定快照可对比 | 对错误 `--doc`、歧义窗口、过期对象 ID、同件数异导线、无效参数、缺失 connector/超时各执行一个非破坏性或写前拒绝例；每次 fresh 回读 | 首次设计写入前明确拒绝；前后完整对象与语义哈希相等；不以强制 flag、GUI 或任意 JS 绕过；超时后状态未知则标 blocked 并停写 |
| C12 清理与独立复核 | 所有记录已冻结，创建的临时页/对象 ID 已保存 | typed 删除临时对象/页，保存重载后核对；`audit` 核对本轮命令与失败记录，复核员重算输入/报告哈希、逐条核对 journal、fresh 回读与客户需求 | 测试工程最终状态可说明；复核结论逐例给出，不引用执行员自评替代原始证据；运行 `audit cost ... --record` |

## 覆盖判定

每轮必须有三层证据：离线测试（Go/connector）、安装态只读预检、专用工程现场写读。
C00–C12（含 C00R）中有 `fail`、`blocked` 或 `not-run`，整套基础 CLI 链路不得标通过。C02–C06
覆盖工程与原理图，C07–C10 覆盖 PCB，C11–C12 覆盖失败恢复与证据；
[固定 ESP32 原始需求 E2E](e2e-automation-acceptance.md)另跑完整 S0–S6/P0–P10。
当前测试工程若没有 PCB 或运行版落后于源码，可完成只读事实采集，但对应现场用例保持
`not-run` / `blocked`。不将历史报告的成功项迁入新版本结论。
