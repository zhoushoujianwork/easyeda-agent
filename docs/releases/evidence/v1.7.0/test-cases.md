# v1.7.0 发布用例

本轮真实链路按下表 B00–B10 执行，固定输入与范围见 [baseline.md](baseline.md)，实际命令、
回读和独立复核见 [test-report.md](test-report.md)。B03 专项前置条件按用户选择为个人空间，
不要求团队目标；B08 使用既有自定义配置。单动作通过不能推导设计质量。

| ID | 前置条件 | 操作 | 判据 |
|---|---|---|---|
| B00 | daemon 和连接器已启动，目标页已打开 | `--version`、`actions`、`health`、[只读预检](https://github.com/zhoushoujianwork/easyeda-agent/blob/1414784/scripts/cli-live-smoke.py)、`project info/doc`、`sch/pcb list`、再次 `health` | 三方同版，前后为唯一目标窗口，回包 `ok:true` 且 `context` 指向正确页；空列表只证明读取成功，不充当对象写读证据 |
| B01 | 源码、安装包和 Skill 版本对应 | 查 `project/doc/sch/pcb/lib/bom/apply/audit` 与本轮基础子命令的 `--help`，对照 `actions` 和 Skill；执行未知子命令、无效 flag | 正常帮助退出 0，错误命令/参数非零退出且未调度设计写入；参数签名与 Skill 一致 |
| B02 | B00 通过，所有测试页 `saved:true`，已冻结完整对象快照 | 记录 daemon 重启命令并重启同版进程；有界等待连接器，重查 `health`、工程/页和完整对象 | 同版连接器自动回到原工程/页，允许 `windowId` 变化；重启后 fresh 对象语义与原快照相同。未连接标 `blocked`，不以旧 smoke 补签 |
| B03 | 个人空间、专用名称和可用窗口 | `project create/find/info/open`、`project export-source`；记录返回 UUID 后重查 | 创建/查找/打开返回同一工程身份，导出文件可读；空 UUID 或不完整枚举先查证，不能重复创建 |
| B04 | B03 工程已核对，有可用 schematic 父文档 | `sch pages/page-new`、`doc ls/open`、必要的 `sch page-rename`；对新页 `list`，保存后 `doc reload` | 页 UUID、父文档、名称和当前窗口一致；空白页可读，保存重载后同一页仍可打开 |
| B05 | B04 专用页、实测库器件和位号可用 | `lib search/by-lcsc`，读取 device/符号/封装/引脚；`sch place`、`sch list --include-pins --include-bbox`、`sch modify`、fresh `list` | 单件对象 ID、位号、device、位置/属性与每次写入回包一致；只改变指定器件。这里不评价选型是否满足客户需求 |
| B06 | B05 器件引脚与目标网络已实测 | 在专用页用 `sch wire/netflag/no-connect` 或等价直接 typed 动作增删单个图元；netflag 经真实导线连接引脚；fresh `sch list --include-wires --include-page-primitives` | 指定导线、标记或 NC 的 ID、端点/引脚和网络按输入出现或消失，范围外对象不变；不以此宣称整页网表正确 |
| B07 | 专用 schematic/PCB、真实 footprint 和焊盘已确定 | `pcb new-board` 或打开专用板；`pcb docs/list/board-info`，`pcb add-component`、单件移动/删除与 fresh `list` | board/页/器件/焊盘身份和坐标回读一致，单件差分准确；不验证整板同步率或布局优劣 |
| B08 | 专用板使用可恢复的自定义规则配置；合法层/网络、实测尺寸和可清理区域已确定 | 先存完整规则，再做 `pcb stackup/config` 的直接 get/set 并恢复原值；`outline-set/get`；单条 `track`、单个 `via/region/pour` 的创建、list、精确删除 | 每种暴露的基础写入均有对象 ID、层/网/几何/配置回读；删除仅移除指定对象，规则名称与数值恢复原快照。系统预设转自定义副本另记为宿主行为，不在原板做无法还原的试验。铺铜连通和 DRC 留给高级测试 |
| B09 | B05–B08 的对象差分已稳定 | `sch save`、`pcb save` → `doc reload` → fresh `sch list`/`pcb dump`；`sch export-image/netlist`、`bom export`、基础 PCB 导出 | `saved:true`；用户创建的器件、导线、铜等稳定对象身份与语义相符。宿主生成属性若重铸 ID，须逐项证明其父对象、键、值、可见性及渲染/电气效果不变，不能仅剔除 ID 就放行；PCB 隐藏属性坐标变为 0 也须验证实际位置语义。导出文件有路径、大小和 SHA-256；图片不替代对象回读 |
| B10 | 已冻结 B05–B09 快照及临时对象 ID | 测错误 `--doc`、过期 ID、无效参数、缺失连接器的写前拒绝；连接恢复后 fresh 读回；typed 清理临时对象/页并保存重载；审计本轮命令，独立复核 | 每个负例在写前拒绝或明确标状态未知；连接恢复后确认完整对象不变。测试工程清理后可解释，复核员据原始回包独立判定 |

## 延期用例

下表均为 not-run，下一版本按 docs/cli-advanced-test-detail.md 完整步骤执行。
历史求解失败和 DRC 缺口继续记录，不因延期变成通过。

| ID | 名称 | 排期 |
|---|---|---|
| A00 | 需求与选型 | 下一版本依基础门禁和原始需求执行；本轮不运行 |
| A01 | 原理图规划与求解 | 下一版本依基础门禁和原始需求执行；本轮不运行 |
| A02 | 保护执行与原理图质量 | 下一版本依基础门禁和原始需求执行；本轮不运行 |
| A03 | PCB 同步与规则 | 下一版本依基础门禁和原始需求执行；本轮不运行 |
| A04 | PCB 布局求解与确认 | 下一版本依基础门禁和原始需求执行；本轮不运行 |
| A05 | 路由、铜与设计检查 | 下一版本依基础门禁和原始需求执行；本轮不运行 |
| A06 | 最终落盘与独立复核 | 下一版本依基础门禁和原始需求执行；本轮不运行 |
