# 安装、连接与恢复

仅在首次使用、升级或连接异常时读取。本地 IR 检查和离线规划不需要打开 EasyEDA；
实际读写、DRC 和原生导图需要已连接的编辑器。

## 安装与升级

CLI/daemon、`easyeda-agent` Skill 和 EDA Agent Connector 是三个配套组成部分；CLI、daemon
与 Skill 必须精确同版，Connector 按 major.minor 兼容线对齐。EasyEDA Pro 是宿主，不参与
项目版本号对齐。

发布版安装 CLI 和 Skill：

```bash
curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | bash
easyeda update --check
easyeda update
```

`update --check` 只读；`--check --exit-code` 是 Agent 会话硬门，只有 CLI、已安装的
客户端 Skill、运行中的 daemon 可验证且精确等于 GitHub latest，并且所有已连接 Connector
与 latest 共享 major.minor 兼容线时返回 0。Connector 仅差 patch 可直接通过；其余组件的
任何落后、超前、开发构建、未知或未连接状态，以及 Connector 跨 minor/major，都返回 10；查询 latest
本身失败返回 1。latest 查询会使用 `GH_TOKEN` / `GITHUB_TOKEN`，API 匿名额度耗尽时回退
到公开 Release 重定向。普通 `update` 更新 CLI 与已安装的 Skill，不能安装或替换编辑器里的连接器。需要安装缺失的客户端
Skill 时用 `--create-missing`，保留本地 Skill 修改用 `--preserve`，固定发布版用
`--version <version>`。更新二进制后还需让 daemon 使用新二进制启动。

GitHub Release 大资产连续三次失败时，CLI/安装器默认尝试 `https://gh-proxy.com/`；只有
先从 GitHub 主源取得该 Release 的 `checksums.txt` 才允许镜像回退，下载后仍按主源
SHA-256 校验。`EASYEDA_GITHUB_PROXY=https://mirror.example/{url}` 可替换传输镜像，设为
`off` 可禁用。不要把镜像提供的 checksum 当信任依据。

版本门禁的恢复顺序固定：

1. 运行不带 `--version` 的 `easyeda update`，把 CLI 和已安装 Skill 升到 latest。会话门禁
   不使用 `--preserve`，因为保留混合内容不能证明 Skill 与 Release 一致。
2. 停止旧 daemon，用升级后的 `easyeda daemon start` 重启。
3. 纯 patch 更新时保留现有 Connector，不升级插件市场版本，也不重开 EasyEDA。仅当
   Connector 与 latest 跨 minor/major 不兼容时，从 `update` 输出的 GitHub Release 地址取得
   对应 `.eext`；在扩展管理器卸载旧侧载项、导入新包，然后完全退出并重开 EasyEDA。
4. **结束当前 Agent 会话并新开会话。** 新会话重新运行 `easyeda update --check --exit-code`；
   只有输出 `READY` 且退出 0 才可继续。当前会话已经载入旧 Skill，禁止升级后原地继续。

在另一台机器或新的终端验证时，固定 Release 版本并使用独立目录，先检查
`easyeda --version`、`easyeda sch compose --help`、`easyeda blocks ls --json`。
这些命令无需 daemon；命令存在且离线规划成功后，再检查连接器与真实页面。
带 `-dirty` 或 git describe 后缀的版本是开发构建，不能作为正式 Release 安装验证的证据。

安装链的后续修复支持 `EASYEDA_INSTALL_DIR` 指定二进制目录，并遵循客户端的
`CODEX_HOME` / `CLAUDE_CONFIG_DIR`；未设置时仍用默认目录。需确认 `command -v easyeda`
指向刚安装的文件，必要时刷新 shell 命令缓存。Windows 下载
`easyeda_windows_amd64.exe` 并命名为 `easyeda.exe`，把所在目录加入 PATH，再运行
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
安装包。更换连接器后保存文档，完全退出并重开 EasyEDA，让所有旧页面运行时停止。
只重新导入包不保证已打开页面执行新代码。不要用 IndexedDB 覆写或清空站点数据作为
常规升级方式；它们绕过安装流程且可能破坏扩展或登录状态。

## 确认连接和目标文档

桌面版和网页版使用同一连接器。打开用户指定的宿主、账号和工程，在扩展设置启用
“允许外部交互”。可使用现有浏览器或桌面工具完成已授权的打开操作；只有登录、权限
或界面操作确实无法代办时才请用户介入，不因连接失败擅自换到另一个宿主。

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
- 已连接：核对目标工程/文档、连接器版本及 `versionGate`。`health` 只验证当前 CLI 与
  连接器的兼容关系，不能代替 GitHub latest 会话门禁。按 findings 的修复建议处理版本错位；
  `--skip-version-check` 不是常规升级或恢复方法。
- 写操作使用 `--project` 和 `--doc`，由 CLI 在派发前实时确认目标文档。没有独立的
  `easyeda context` 命令；`health` 显示连接状态，`doc ls/switch` 读取/切换实时文档。

## 上下文与缓存

`windowId` 会随重连变化，不作为项目或文档的持久身份。优先用项目和文档 UUID 路由。
daemon 接收心跳、context 和动作响应来更新窗口信息，过期连接会退休，同一
project/document/tab 的重复连接会去重；缓存清理不需要手工删历史 windowId。

`health` 中的连接上下文不能代替目标页的数据快照。切页、重连或 Apply 后，需要
读取相应文档的新数据；离线文件须记录其来源和采样阶段。要刷新编辑器文档状态时：

```bash
easyeda doc reload "<doc-name-or-uuid>" --project "<project>"
```

它先保存，再关闭并重开文档。PCB 若刷新了铜形或规则，之后运行 `pcb pour-rebuild`
再验证；`doc switch` 只切前台，不等于 reload。文档重载也不等于停止旧连接器运行时。

## 单连接恢复

同一目标页出现多个版本或 windowId、反复注册或写请求超时时，先暂停 Apply，并保留
health、journal 和日志。多个真实工程/窗口可以同时存在；要排除的是同一目标的旧运行时。

1. 先用回读确认最后一条写是否落地；能保存时保存。响应失败不一定代表内容未改变，
   不要直接重放整队列。
2. 检查扩展管理器只保留所选渠道的当前连接器，卸载重复旧项后完全退出并重启 EasyEDA。
   网页版若同一 tab 重载仍无法重连，保存后关闭该 tab，再打开目标工程。
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
