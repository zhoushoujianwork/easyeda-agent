# 基础 CLI 真实链路测试：前置条件与步骤

[返回简版](cli-live-test.md)。本表只测基础动作从 Cobra 命令到 daemon、连接器、官方 API
再回到 CLI 的真实链路。`sch`、`pcb` 是命令域，不是验收层级：规划、求解、Compose/Apply、
DRC 与整板质量按[高级 CLI 用例](cli-advanced-test-detail.md)另测。

## 全局前置条件

1. 用户已在 Codex 内置浏览器打开 Web EasyEDA Pro V4 的专用测试工程，允许外部交互；
   CLI、daemon、连接器精确同一 `dev.N`。记录 Git commit、安装包、宿主版本、窗口和
   工程/页 UUID。版本或页面身份不明时停止所有现场写入。
2. 准备可清理的空白原理图页与 PCB 页、已知真实器件/封装和实测引脚/焊盘、一个合法测试网络、
   参数文件及原始完整快照。只在这些测试页写入；PCB 几何使用量测值和明确单位。
   `project create` 返回空 UUID 时先 `project find` 完整查证，不盲重试。
3. 主执行员独占 Web 窗口，逐条串行调用。Codex subagent 可在新上下文执行，但主 Agent
   同时停止访问该窗口；独立复核员只读查看冻结证据。工程写入只经 typed CLI，
   不用 GUI 或 `debug.exec_js` 兜底。
4. 每次调用保存命令、UTC 时间、stdout/stderr、退出码、输入哈希、目标 UUID 和返回
   `context`。写入前后分别保存完整对象快照；超时或部分成功先 fresh 回读，状态不明就停写。
   可用 `python3 scripts/cli-live-record.py --out <新证据.json> -- easyeda ...` 记录；
   它拒绝覆盖旧证据。`--help` 与离线测试只算命令契约，不能替代现场回包。
   B01 可先运行 `python3 scripts/cli-basic-contract-check.py --out <新证据.json>`，
   检查本表使用的命令帮助、关键参数、typed action 目录和错误命令退出码；
   然后逐项核对 Skill 中实际调用的签名，报告未覆盖的命令。
5. 所有写命令显式传 `--project <UUID> --doc <UUID>`；仅创建尚不存在的工程时用
   `--window`。每次稳定检查点显式保存，确认 `saved:true` 后再 reload 和 fresh 读取。
   本表的通过判据只判断动作是否准确执行，不判断电路设计是否合理。

## 基础动作测试

| ID | 专项前置条件 | 串行操作 | 通过判据 |
|---|---|---|---|
| B00 版本与只读路由 | daemon 和连接器已启动，目标页已打开 | `--version`、`actions`、`health`、[只读预检](../scripts/cli-live-smoke.py)、`project info/doc`、`sch/pcb list`、再次 `health` | 三方同版，前后为唯一目标窗口，回包 `ok:true` 且 `context` 指向正确页；空列表只证明读取成功，不充当对象写读证据 |
| B01 命令契约 | 源码、安装包和 Skill 版本对应 | 查 `project/doc/sch/pcb/lib/bom/apply/audit` 与本轮基础子命令的 `--help`，对照 `actions` 和 Skill；执行未知子命令、无效 flag | 正常帮助退出 0，错误命令/参数非零退出且未调度设计写入；参数签名与 Skill 一致 |
| B02 daemon 重连 | B00 通过，所有测试页 `saved:true`，已冻结完整对象快照 | 记录 daemon 重启命令并重启同版进程；有界等待连接器，重查 `health`、工程/页和完整对象 | 同版连接器自动回到原工程/页，允许 `windowId` 变化；重启后 fresh 对象语义与原快照相同。未连接标 `blocked`，不以旧 smoke 补签 |
| B03 工程容器 | 确定团队、专用名称和可用窗口 | `project create/find/info/open`、`project export-source`；记录返回 UUID 后重查 | 创建/查找/打开返回同一工程身份，导出文件可读；空 UUID 或不完整枚举先查证，不能重复创建 |
| B04 页面容器 | B03 工程已核对，有可用 schematic 父文档 | `sch pages/page-new`、`doc ls/open`、必要的 `sch page-rename`；对新页 `list`，保存后 `doc reload` | 页 UUID、父文档、名称和当前窗口一致；空白页可读，保存重载后同一页仍可打开 |
| B05 库读取与原理图器件 | B04 专用页、实测库器件和位号可用 | `lib search/by-lcsc`，读取 device/符号/封装/引脚；`sch place`、`sch list --include-pins --include-bbox`、`sch modify`、fresh `list` | 单件对象 ID、位号、device、位置/属性与每次写入回包一致；只改变指定器件。这里不评价选型是否满足客户需求 |
| B06 原理图连接原语 | B05 器件引脚与目标网络已实测 | 在专用页用 `sch wire/netflag/no-connect` 或等价直接 typed 动作增删单个图元；netflag 经真实导线连接引脚；fresh `sch list --include-wires --include-page-primitives` | 指定导线、标记或 NC 的 ID、端点/引脚和网络按输入出现或消失，范围外对象不变；不以此宣称整页网表正确 |
| B07 PCB 容器与器件 | 专用 schematic/PCB、真实 footprint 和焊盘已确定 | `pcb new-board` 或打开专用板；`pcb docs/list/board-info`，`pcb add-component`、单件移动/删除与 fresh `list` | board/页/器件/焊盘身份和坐标回读一致，单件差分准确；不验证整板同步率或布局优劣 |
| B08 PCB 几何与配置原语 | 专用板、合法层/网络、实测尺寸和可清理区域已确定 | `pcb stackup/config` 的直接 get/set，`outline-set/get`；单条 `track`、单个 `via/region/pour` 的创建、list、精确删除 | 每种暴露的基础写入均有对象 ID、层/网/几何/配置回读；删除仅移除指定对象。铺铜连通和 DRC 留给高级测试 |
| B09 持久化与导出 | B05–B08 的对象差分已稳定 | `sch save`、`pcb save` → `doc reload` → fresh `sch list`/`pcb dump`；`sch export-image/netlist`、`bom export`、基础 PCB 导出 | `saved:true`，重载后的完整对象与预期一致；导出文件有路径、大小和 SHA-256。图片不替代对象回读 |
| B10 保护负例、清理与复核 | 已冻结 B05–B09 快照及临时对象 ID | 测错误 `--doc`、过期 ID、无效参数、缺失连接器的写前拒绝；连接恢复后 fresh 读回；typed 清理临时对象/页并保存重载；审计本轮命令，独立复核 | 每个负例在写前拒绝或明确标状态未知；连接恢复后确认完整对象不变。测试工程清理后可解释，复核员据原始回包独立判定 |

## 进入高级 CLI 的条件

B00–B10 **全部为 `pass`** 才放行[高级 CLI 业务验收](cli-advanced-test.md)。任一用例
`fail`、`blocked` 或 `not-run` 时，保留其证据并停在基础层；不能用求解器成功、
DRC 数值或其他高级结果抵消基础动作缺测。高级测试另按
[固定 ESP32 原始需求](../esp32MiniRequire.md)和[端到端验收标准](e2e-automation-acceptance.md)
执行。
