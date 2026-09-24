# 安装、连接与恢复

仅在首次使用、升级或连接异常时读取。本地 IR 检查和离线规划不需要打开 EasyEDA；
实际读写、DRC 和原生导图需要已连接的编辑器。

## EasyEDA Pro V4 主线要求

项目主线宿主为 EasyEDA Pro V4，最低识别基线 4.0.0，推荐使用已验证的 4.1.60 或更新 V4。
运行 `easyeda health` 后检查 `hostCompatibility`：V3 的 `block` 表示停止现场写入并升级；较老
V4 的 `warn` 表示可读但应优先升级，未经 save→reload→readback 不能宣称写入兼容。宿主产品
版本不参与 CLI/daemon/Connector 版本对齐；`extension.json` 的 `engines.eda ~3.2.0` 是扩展
API 引擎版本，官方 V4 SDK 仍使用该 API 线，不得机械改成 4.x。开发状态见
[`docs/v4-development.md`](https://github.com/zhoushoujianwork/easyeda-agent/blob/main/docs/v4-development.md)。

## 安装与升级

### 源码 checkout 中的 Skill

源码规范源是 `.agents/skills/easyeda-agent/`。在源码仓库运行
`python3 scripts/install-agent-skills.py --scope design --dry-run` 预览，去掉 `--dry-run`
即可把当前 checkout 链接到用户级发现目录；`--scope all` 同时安装仓库查询与维护入口。
安装器只迁移能够确认属于同一 checkout 的旧 `skills/easyeda-agent` 链接，保留发布版目录和
其他仓库链接。发布包仍以 `easyeda-agent/` 为根，正式安装和自更新不依赖源码目录层级。
源码链接随 checkout 内容变化，不是固定发布版；变更后重新加载客户端。

### 本地开发版（用户明确选择时）

仓库中显式同步 connector/npm/lock 和 Skill 元数据到独立 `X.Y.Z-dev.N`，补 changelog，
再运行 `make local-build VERSION=vX.Y.Z-dev.N`。构建真实 CLI、连接器与 tracked-only Skill 包，
生成 checksums；不打 tag、不 push、不上传。每次源码改变递增 N，不复用同版不同内容。
正式版 `make release` 仍只接受纯 `vX.Y.Z`。

```bash
# 首次用刚构建的本机二进制调用；--binary 指向实际 PATH 安装位置，不是 dist 内二进制。
dist/easyeda_darwin_arm64 update --local-dir dist --binary /usr/local/bin/easyeda
# 安装后显式核对本地包；不访问 GitHub
easyeda update --local-dir /absolute/path/to/dist --check --exit-code
```

本地安装替换 CLI 和已安装客户端的完整 Skill（备份路径输出），不自动重启进程或导入插件。
保存文档，用安装后的 CLI 重启 daemon；卸载旧侧载连接器、导入输出的 `.eext`，完全退出并重开
EasyEDA。开发版精确同版便于定位源码与运行态差异，不能套用正式版的 patch 兼容规则。
检查比对包 SHA-256、实际 CLI 字节、Skill 全部文件（含 metadata 和 `.version`）及实时版本。
checksum 只防意外损坏，不是签名：只使用自己构建或可信来源的本地包。
无连接、旧 daemon、混合 Skill 或任一旧 Connector 均会在对账中显示差异；它们不阻止离线工作，
但涉及对应运行态能力时必须如实报告版本证据。未安装前可以用 dist 二进制进行离线测试，
不能据此声称现场已验证。

### 正式版

CLI/daemon、`easyeda-agent` Skill 和 EDA Agent Connector 是三个配套组成部分；CLI、daemon
与 Skill 必须精确同版，Connector 按 major.minor 兼容线对齐。EasyEDA Pro 是宿主，不参与
项目版本号对齐。

发布版安装 CLI 和 Skill：

```bash
curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | bash
easyeda update --check
easyeda update
```

原生 Windows 用 PowerShell（5.1 或 7 均可）执行同源的 `install.ps1`；`install.sh`
只支持 macOS/Linux，在 Windows 上直接报错并指向该脚本：

```powershell
irm https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.ps1 | iex
```

`update --check` 是显式、只读的安装对账工具，不是每次 EDA 操作的前置许可。
`--check --exit-code` 保留现有自动化退出码：满足所选对账条件返回 0，组件差异返回 10，查询
本身失败返回 1。正式版对账可比较 GitHub latest 与 Connector major.minor 兼容线；本地开发版
使用 `--local-dir` 比较指定构建。发现差异时根据当前任务是否依赖该运行态能力决定升级或只记录，
不强制追 latest，也不要求另开 Agent 会话。latest 查询会使用 `GH_TOKEN` / `GITHUB_TOKEN`，API 匿名额度耗尽时回退
到公开 Release 重定向。普通 `update` 更新 CLI 与已安装的 Skill，不能安装或替换编辑器里的连接器。需要安装缺失的客户端
Skill 时用 `--create-missing`，保留本地 Skill 修改用 `--preserve`，固定发布版用
`--version <version>`。更新二进制后还需让 daemon 使用新二进制启动。

GitHub Release 大资产连续三次失败时，CLI/安装器默认尝试 `https://gh-proxy.com/`；只有
先从 GitHub 主源取得该 Release 的 `checksums.txt` 才允许镜像回退，下载后仍按主源
SHA-256 校验。`EASYEDA_GITHUB_PROXY=https://mirror.example/{url}` 可替换传输镜像，设为
`off` 可禁用。不要把镜像提供的 checksum 当信任依据。

需要升级时按以下顺序恢复安装态：

1. 运行 `easyeda update` 更新到所选版本；需要精确 Release 时显式传 `--version`。
   `--preserve` 会形成混合内容，不能作为纯 Release 一致性的证据。
2. 停止旧 daemon，用升级后的 `easyeda daemon start` 重启。
3. 纯 patch 更新时保留现有 Connector，不升级插件市场版本，也不重开 EasyEDA。仅当
   Connector 与 latest 跨 minor/major 不兼容时，从 `update` 输出的 GitHub Release 地址取得
   对应 `.eext`；在扩展管理器卸载旧侧载项、导入新包，然后完全退出并重开 EasyEDA。
4. 重新运行 `easyeda update --check` 记录实际版本。若当前运行时不能热加载新 Skill，后续步骤按
   已加载说明和当前 `--help` 执行，并明确文档/二进制差异；无需把重开会话当作执行许可。

在另一台机器或新的终端验证时，固定 Release 版本并使用独立目录，先检查
`easyeda --version`、`easyeda sch compose --help`、`easyeda blocks ls --json`。
这些命令无需 daemon；命令存在且离线规划成功后，再检查连接器与真实页面。
带 `-dirty` 或 git describe 后缀的版本是开发构建，不能作为正式 Release 安装验证的证据。

安装链的后续修复支持 `EASYEDA_INSTALL_DIR` 指定二进制目录，并遵循客户端的
`CODEX_HOME` / `CLAUDE_CONFIG_DIR`；未设置时仍用默认目录。需确认 `command -v easyeda`
指向刚安装的文件，必要时刷新 shell 命令缓存。Windows 首选 `install.ps1`：它遵循
同一套 `EASYEDA_INSTALL_DIR` / `CODEX_HOME` / `CLAUDE_CONFIG_DIR`，默认装到
`%USERPROFILE%\.local\bin`，全部资产先校验 SHA-256 再替换；目录不在用户 PATH 上时
只打印添加命令，`-AddToPath` 或 `EASYEDA_ADD_TO_PATH=1` 才写入用户 PATH，机器级
PATH 不动；`easyeda.exe` 被运行中的 daemon 占用时改名旧文件后换入新文件，随后需
重启 daemon。回退的手工步骤仍然有效：下载 `easyeda_windows_amd64.exe` 并命名为
`easyeda.exe`，把所在目录加入 PATH，再运行
`easyeda update --skill-only --create-missing --version <version>` 安装 Skill。
Git Bash/WSL 与原生 Windows 是不同运行环境，选择相应的二进制。

DSH bundle 在 Windows 启动时报 `C:\C:\... MODULE_NOT_FOUND` 时，升级
`easyeda-agent-dsh` bundle 并重启 DSH；这是 MCP/Skill 的文件 URL 路径转换问题。
bundle 使用 Node 内置 `fileURLToPath` 同时解析 MCP server 与 Skill 目录，保留
盘符、UNC、中文和空格；不要手工拼盘符或把 URL 的 `pathname` 当成本机文件路径。
Node 版本遵循 bundle 的要求（至少 20.17）。

安装/升级失败须保留非零退出码，不能只依据最后一行提示判定成功。普通 Skill 更新
应替换完整发布目录，清理已删除的旧参考；`--preserve` 是混合本地内容，保留旧版本标记，
不能宣称全部文件已升级。daemon 启动时只同步自身版本的 Skill，版本升级由显式
`easyeda update` 完成。

仓库开发使用 `make build` 构建 CLI，`make install` 安装，`make dev` 保持 daemon
随 Go 代码热重建。`make dev` 会刷新仓库二进制和可写的安装路径；先用 `command -v easyeda`
核对实际 CLI。不要再启动一个后台 daemon 与开发进程交替接管端口。

连接器有两种安装渠道，同一编辑器 profile 保留一种：

| 渠道 | 安装/升级方法 |
|---|---|
| GitHub Release `.eext` 侧载 | 跨 minor/major 时下载与 CLI 兼容线对应的包，在 EasyEDA 扩展管理器卸载旧项，再导入新包。平台按 UUID 去重，侧载没有自动更新。 |
| [立创插件市场](https://jlc-ext.com/item/zhoushoujian/easyeda-agent-connector) | 在市场安装，平台支持原地自动更新；市场版本可落后 patch，只要 major.minor 相同就无需处理。 |

开发连接器：`make connector` 按当前版本/UUID 构建，`make eext` 升 patch 后构建同 UUID
安装包。用户要求轮换侧载包时，Codex 可在用户已打开的 Web EDA 中管理扩展：先对每个
已打开文档 typed 保存，再核对 UUID/旧版本、卸载旧项、导入新包，并在新项配置中重新启用
“允许外部交互”。只重新导入包不保证已打开页面执行新代码；Web 端可用 typed `web reload`
刷新并等待新连接与同一工程/文档回读，桌面端完全退出后重开。不要用 IndexedDB 覆写或清空站点数据作为
常规升级方式；它们绕过安装流程且可能破坏扩展或登录状态。

用户明确授权的仓库开发验证可按仓库 `docs/dev-environment.md §5` 对**已安装的同一
连接器**做有界热更新，不要求用户重复手动卸载导入。这不是常规升级或权限绕过：先保存
文档、核实唯一目标数据库/连接器 UUID/旧版本和现有外部交互权限；只原子更新该连接器
的索引与 bundle，校验新包版本/哈希，保留原权限，不清空数据库或站点。通过正常
`debug exec` 运行已审阅的专用更新脚本，不能借此绕过设计写操作守卫。重载用户选定的
Web 编辑器并核对新窗口/运行版本。用户指定 Web 时绝不改开桌面客户端。

## 确认连接和目标文档

桌面版和网页版使用同一连接器。打开用户指定的宿主、账号和工程，在扩展设置启用
“允许外部交互”。可使用现有浏览器或桌面工具完成已授权的打开操作；只有登录、权限
或界面操作确实无法代办时才请用户介入，不因连接失败擅自换到另一个宿主。

V4 的权限入口仍从**高级 → 扩展管理器 → 已安装 → 选中连接器**进入。旧 V3.2 的状态按钮也使用
`Enabled` / `Disabled` 表示**当前状态**（点击切换），不是动作；只有处于 `Enabled` 时才显示
`Config` 页签，“允许外部交互 / Allow interactive with external”和“Show at header menu”
都在该页签。未开启外部交互时平台的 `sys_WebSocket.register()` 直接抛错，连接器侧只表现为
“Daemon not found”，daemon 看不到任何连接尝试；此时先核对权限，不要重启 daemon。
Online 与 Half Offline 模式的扩展存储互不共用，切换运行模式后需在新模式下重新导入并授权。

非开发环境在单独终端运行：

```bash
easyeda daemon start
```

当前默认固定监听 **60832**，连接器重试该端口。`daemon start` 会接管同端口旧的
EasyEDA daemon；端口被其他程序占用时按报错处理，不向后寻找另一个 daemon 端口。
自定义 `--ports` 时还须同步连接器 `daemonPorts` 配置。

```bash
easyeda health --project "<project>"
easyeda doc ls --project "<project>"
easyeda doc switch "<doc-name-or-uuid>" --project "<project>"
```

- 没有 daemon：检查当前安装路径与启动日志；开发环境恢复现有 `make dev`。
- daemon 正常但 `windows` 为空：检查编辑器、登录态、扩展启用和外部交互权限。
- 已连接：核对目标工程/文档、连接器版本及 `versionGate`。`health` 提供当前 CLI 与
  连接器的兼容证据；GitHub latest 只用于显式安装对账，不决定本次操作能否继续。按 findings
  评估当前步骤是否依赖缺失能力，并记录实际运行版本。
- 写操作使用 `--project` 和 `--doc`，由 CLI 在派发前实时确认目标文档。没有独立的
  `easyeda context` 命令；`health` 显示连接状态，`doc ls/switch` 读取/切换实时文档。

### 扩展已启用、权限已开，但始终没有连接尝试

以下两种情况 daemon 侧都完全不可见（`windows` 为空、无 `connector connected` 日志），
`health` 无法区分，需要在编辑器一侧判断：

- **跨大版本导入残留**：在 2.2.x 客户端导入过本连接器（`engines.eda` 为 `~3.2.0`）后再升级到
  3.2.x，可能留下只有扩展索引记录、没有文件内容的安装：扩展列表里可见、状态也能切换，但
  永远不加载。在扩展管理器卸载该项，完全退出并重开 EasyEDA，再重新导入 `.eext`。
- **重启后不自启（#221，根因未明）**：国际版桌面客户端 3.2.149（Half Offline 与 Full Online
  均复现）上，侧载的连接器只在“导入当次”的运行期间工作；EasyEDA 重启后不再 activate，
  顶部菜单栏里也看不到 `EDA Agent`。当前只有规避手段：每次启动 EasyEDA 后重新导入同一个
  `.eext`。覆盖导入会保留外部交互设置，但状态可能变为 `Disabled`，需点回 `Enabled`；
  当次运行内即可注册，`easyeda update --check --exit-code` 返回 `READY`。这不是修复，
  其他客户端版本是否受影响未验证。

### 连接正常，但 `block-apply` 的第一个 place 就 “connector did not respond”

这不是连接故障，不要去重启 daemon 或重装连接器：`health` 正常、其他读命令也正常时，
多半是器件 uuid 不属于当前站点。国际版（easyeda.com）与国内版（lceda.cn）系统库
libraryUuid 相同但器件 uuid 不同，平台对未知 uuid 不回执，表现成超时。处理办法见
[part-selection.md 的「站点差异：deviceUuid 必须按当前版本重解析」](part-selection.md#站点差异deviceuuid-必须按当前版本重解析)。

## 上下文与缓存

`windowId` 会随重连变化，不作为项目或文档的持久身份。优先用项目和文档 UUID 路由。
daemon 接收心跳、context 和动作响应来更新窗口信息，过期连接会退休，同一
project/document/tab 的重复连接会去重：可比较的完整语义版本优先保留较高版（包括
`-dev.N`），同版或版本无法比较时保留较晚连接。缓存清理不需要手工删历史 windowId。
去重只处理 daemon 中的连接；升级后仍用 `health` 核对目标页实际运行的 Connector 版本。

`health` 中的连接上下文不能代替目标页的数据快照。切页、重连或 Apply 后，需要
读取相应文档的新数据；离线文件须记录其来源和采样阶段。要刷新编辑器文档状态时：

```bash
easyeda doc reload "<doc-name-or-uuid>" --project "<project>"
```

它先保存，再关闭并重开文档。PCB 若刷新了铜形或规则，之后运行 `pcb pour-rebuild`
再验证；`doc switch` 只切前台，不等于 reload。文档重载也不等于停止旧连接器运行时。

用户明确要求刷新**整个 Web 页面**时，先保存同一页面里其他打开的文档，再执行：

```bash
easyeda web reload --project <project-uuid> --doc <active-doc-uuid> --timeout 30s
```

该命令保存当前文档，调用 typed `system.page_reload`，等新的 connector windowId 和同一
工程/文档对象可读后才成功，并输出实际毫秒耗时。旧连接器没有该 action 时须先更新扩展；
`doc reload` 与整个 Web 页面刷新不能混为一谈。

Web 编辑器若在重开后持续显示加载动画，停止自动重试和现场写入：第一次 `openDocument` 可能
仍在宿主内部执行，重复重开会叠加空白标签。保留错误、当前标签状态和 typed read 结果；只有
UUID 变成目标值、但对象仍不可读时，仍视为加载未完成。当前 `doc reload` 保存目标分屏、等待
旧文档退出活动态，并只做一次有界重开；失败时报告数据不可用，修复 typed reload/open 后复测。
不得通过 GUI 刷新浏览器、工程树、属性面板或 CUA 兜底；上面的 typed `web reload`
只用于用户明确要求的整页刷新，不替代读取故障的诊断。

## 单连接恢复

daemon 生命周期命令：`easyeda daemon stop` 停止本机指定端口的已识别 daemon；
`easyeda daemon restart --auto-update-skill=false` 停止后以前台方式启动当前 CLI 版本，
支持与 `start` 相同的启动参数。先显式保存可读工程；重启 daemon 不能取消宿主尚未结束的写入。
进程身份不可确认或 `/health` 不可读时拒绝结束进程，并报告端口、PID 和可获取的进程路径。
Windows 使用系统端口查询和进程终止接口，不依赖 `lsof`。PID 文件不可写时仍可通过
`health.pid` 和端口所有者管理；按提示修正 `.easyeda-agent` 目录权限，不以管理员启动作为默认修法。
`make dev` 管理的进程由其终端停止，否则热重载管理器可能再次启动它。

同一目标页出现多个版本或 windowId、反复注册或写请求超时时，先暂停 Apply，并保留
health、journal 和日志。多个真实工程/窗口可以同时存在；要排除的是同一目标的旧运行时。

1. 先用回读确认最后一条写是否落地；能保存时保存。响应失败不一定代表内容未改变，
   不要直接重放整队列。
2. 用版本、连接和运行日志定位重复连接器或旧运行时；Agent 不通过扩展管理器、浏览器标签或
   其他 GUI 修复。需要宿主侧重新安装或重启时停止现场操作并报告该外部前置条件。
3. 只有 daemon 本身版本或状态异常时才重启它；`make dev` 管理的进程通过其终端恢复。
4. 用 `health` 确认目标只剩预期连接和版本，再读取目标页，例如
   `sch list --page <uuid> --include-pins`。读回稳定且未完成步骤已核清后，再继续 Apply。

恢复后仍有同一错误就根据新日志定位，不循环刷新、批量杀浏览器进程、重发写操作或
清空 IndexedDB。离线数据准备可以继续，原生验证仍未完成时如实标明。

基础 `sch list` 成功但 `--include-device-identity` 超时时，连接不等于丢失：完整身份解析
包含工程来源导出、LCSC 候选和器件详情查询。身份读取默认使用 60 秒请求预算（其他普通
动作仍为 20 秒）；同次读取复用完全相同的解析输入，不跨请求/页面缓存来源证明。
查看错误所指的具体 API 阶段；超时或缺证据仍不得重建，不能把 16 位实例 ID 当库 UUID。

### PCB 文档枚举暂时缺失（#190）

`doc ls` / `--doc` 解析时，如果当前活动 PCB 不在 `pcb.documents.list` 中，CLI 会用
官方 `pcb.board.info` 补读当前 PCB 的 UUID 与名称。仅当当前文档与该补读的
项目 UUID、文档 UUID、类型均一致时才接受，不从历史缓存或用户输入猜名称。
补读失败或上下文不一致时报告枚举不完整并停止；不要去掉 `--doc` 来绕过目标保护。

如果总表与当前 PCB 元数据都不可读，但 `document.current` 的结果与响应上下文
一致确认同一工程、同一图页 UUID 和类型，可用这个精确 UUID 作为 `--doc`；
CLI 直接验证该实时身份，不依赖名称枚举。不能将未经回读确认的 UUID 或旧 health 缓存当证据。

### 读取预算与未知写入状态

CLI 在统一派发入口为 `document.open` / `schematic.page.open` 提供至少 30 秒的 daemon 等待窗口，
为 `schematic.components.list` 的 `includePins:true` 提供至少 150 秒；HTTP 预算另含现有 2 秒响应宽限。
这些预算覆盖普通命令、布局与 Apply 调用，不改变明确小于默认 20 秒的诊断请求。
预算增加不代表解决宿主节流，也不保证迟到写入取消。打开文档报错后，`--doc` 会只读核实目标 UUID；
不能确认目标时仍失败。zone-arrange 修复连接失败后立即停止，不因超时或回滚文案重发写入；
先回读连接、图元和保存状态，再从参数化源数据重新计划。
