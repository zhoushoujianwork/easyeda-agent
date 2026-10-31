# v1.16.0 发布准备

候选基于已合并的 `origin/main`（`a0aa9620`），不纳入本地 dev 上的其他开发提交。
版本元数据与发布说明已准备；本节是准备状态，不是正式发布记录。

## 本版范围

- 原生 CBB 的官方库查询、身份核对、归档导出、受控符号创建与实际放置；
  按当前工程本地 type 17 符号证明变体，修复展开网表缺席导致的误报，保留真悬空判据。
- CBB 默认矩形库存与精确清理，保存重开后保留引脚和自定义 polygon。
- 原生 ARC 铺铜轮廓，及经过实际内容与官方文件哈希核对的 Gerber/坐标文件导出。
- 官方 SVG 内嵌静态 SVG、PCB 图面审查、批量活动文档读取与密集电源布局求解修复。
- 宿主弹窗感知与恢复工作流；没有通用自动弹窗应答 CLI。

连接器运行时代码相对 v1.15.0 已变化，因此按发布契约递增 minor 至 v1.16.0；
用户需更新连接器才能得到本版行为。CLI/daemon、连接器、npm/lock 与 Skill 使用同一版本。

## 验收与发布状态

用户于本轮确认 v1.16.0 按 cli-fixes 专项验收，完整小板 full-design-e2e 留待 follow-up；
只读独立 Agent 已重算原始证据与源码对应，专项结论 pass。验收材料见 evidence/v1.16.0。
不把先前专项通过写成完整小板端到端通过，不把发布回执当作外部平台公开可安装。
自动检查已通过：Go 5829 项、Connector 1305 项、发布脚本 123 项，另含 MCP、typecheck/build、vet 和 agent/Skill 检查。
干净 c1770c69 原生候选的 15 项离线 CLI 冒烟通过；标准本地发布构建、全资产校验与隔离安装升级验证均已完成；第二阶段只读独立核验 pass：资产字节、二进制身份、冻结验收包、Skill/连接器、安装脚本与三轮安装结果均独立核对一致。
尚未打标签、上传或发布；原始记录在本地 release-v1.16.0 证据目录。


## 本地发布资产与安装验证

标准 `make release-check VERSION=v1.16.0` 通过。独立干净 clone 基于
`a5f948b7` 执行 `make release-build VERSION=v1.16.0`，生成五平台 CLI、连接器、
192 文件 Skill、两种安装脚本、验收包与 checksums 共十一文件。
十个资产 SHA256 全部匹配，五个二进制 build info 均为同一源码提交、v1.16.0、
`vcs.modified=false`。其中 macOS arm64 CLI SHA256 为
`db16f4ffff109973adf7951feb8ca91f4889a6d25b9b724f2fca51113e5a4a4a`。
完整 `make release-smoke` 通过，包含资产/验收包验证与本机十五项离线 CLI 冒烟。
其他平台构建完成不等于这些发布二进制已在对应平台运行。

原样 `install.sh` 在临时 bin 和临时 Claude Skill 目录执行，未修改 HOME/CODEX_HOME，
没有启动 daemon 或更换系统工具。三轮分别为首次安装 v1.16.0、安装已发布 v1.15.0、
从该版本升级 v1.16.0；每轮 CLI 实字节与输入资产一致、版本正确，192 个 Skill 文件逐字节
匹配，升级移除了旧包哨兵文件。v1.15.0 的二进制与 Skill 从 GitHub 下载后先与官方
checksums 比对；v1.16.0 使用本地候选资产。下载通道为仅允许对应精确 URL 的本地 curl
替身，不把此测试称为尚未发布的 v1.16.0 远端下载或 `easyeda update` 联网升级验收。

原始日志与哈希记录保存在本地 release-v1.16.0 中的 release-build.log、release-smoke.log、
release-assets-identity.json、installer-smoke-result.json 和三次安装日志。
验收四份材料保持构建时已审核内容，发布包中 test-evidence.zip 与其逐字节对应。
本节记录的是候选 a5f948b7 的本地资产验证；正式发布须从最终已审阅提交重新构建、
核验附注标签、远端资产与公开下载，不能把准备完成当成已经发布。


## 正式发布结果

用户于 2026-10-31 明确要求“开始发布”。标准 `make release VERSION=v1.16.0`
从最终合并提交 `b82be9c32404e164864e07901dd97a1bb1ad11dc` 重新构建并发布；
附注标签 v1.16.0 精确绑定该提交，GitHub Release 于 2026-10-31 14:09:14 UTC
转为非草稿、非预发布：[v1.16.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.16.0)。
十一份远端文件全部实际回下载，名称、字节、SHA256 与本轮最终资产一致；
下载包通过 release-smoke，验收四文件与 Git 源逐字节对应，不能与准备阶段 a5f948b7 的
二进制混用。发布范围仍为 cli-fixes，完整小板/制造/硬件 E2E 未补签。

真实 GitHub 首次安装在临时 bin 与 Claude Skill 目录通过，192 个文件逐字节匹配。
已发布的 v1.15.0 CLI 在独立目录执行 `easyeda update --cli-only --version 1.16.0
--restart-daemon=false`，完成真实下载、校验和原子替换；新文件与 GitHub 下载资产一致。
没有替换系统 CLI、Skill 或重启正在运行的 EDA daemon；运行态仍需另行升级 CLI/连接器。

SkillHub [发布任务](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/41208802312)
成功，回执 `skillId=163177 status=None`；发布后公开下载仍为 1.15.0，
因此仅记已提交，不称市场前台已经更新。ClawHub 原发布步骤因本机无命令失败，
改用官方 CLI 补发后遇到写入限流；其公开状态以最后查询结果为准，不能用上传尝试替代可安装验证。
立创连接器市场未提交新版；用户可直接从 GitHub Release 下载 1.16.0 连接器。

原始发布日志、远端下载文件、SHA256、真实首次安装与升级输出保存在本地
release-v1.16.0-published 证据目录，未覆盖此前准备阶段记录或冻结验收材料。

---

## v1.15.0 发布准备

用户于 2026-10-24 要求按 v1.15.0、有限验收范围准备发布：先修 CI 版本不一致，再写本文、跑现场验收、
独立复核，最后再确认是否执行 `make release`（尚未批准执行）。连接器运行时自 v1.14.0 起有改动（新增
`pcb.model3d.*`、`pcb.export.3d`/`3d_shell`、`board.new`、`pcb.document.delete`、`editor.panels`、
`pcb.route.auto`，焊盘回读带钻孔，网表导出后连续空闲等待，上报宿主 UA），按版本规则递增 minor；
CLI/daemon、连接器包、npm/lock 与 Skill 统一为 1.15.0（准备提交 `e26c821f`），用户需更新连接器。
验收中发现并修复组合改名误报（`aa806afa`、`9fda0e2b`），产品源码候选为 `9fda0e2b`。

## 版本范围（v1.14.0..9fda0e2b，43 个提交）

- PCB 3D 与外壳：`pcb 3d models|transform|bind|export-shell`、`pcb export-3d`；离线参数化外壳
  `pcb enclosure plan|build`（上下壳/推盖、卡扣、螺丝柱按真实钻孔、开孔合并、预览图、STL/STEP）。
- 工程结构：`board new`、`board delete` 连带删除 PCB 与原理图并刷新工程树、`pcb doc-delete`。
- 编辑器与宿主：`editor panels`；health 区分 edadesk/浏览器窗口，同文档跨宿主不再静默择一。
- PCB：`pcb autoroute` 默认官方路由器；`pour-fit --replace` 只清同网同层；`rip-up`/`clear` 不碰内嵌过孔；
  via-in-pad 按焊盘矩形；出板收尾规划（缩颈、线条形泪滴及修改、丝印）；实际回读图文字锚点。
- 原理图：网表导出后连续空闲再返回（#256/#257）；多套原理图工程的撞号判断改按当前原理图网表。
- 库：桌面本地库符号编辑器打开修复（PR #281）；块库新增天线关系布局与 1×4 排针块（draft）。
- 组合：改名以回读为准——`board new --name` 未生效时报 partial（`notApplied:["name"]`）并非零退出，`board rename` 只有回读
  出现新名才成功（验收发现：Web 4.1.78 `modifyBoardName` 对新建组合返回过 false，也返回过 true 而名字未变）。

## 验收范围

有限范围（schema 3，`acceptanceScope=cli-fixes`），完整设计 E2E 延期至 follow-up。现场复测本轮新增的
3D/外壳、工程结构、宿主识别和多原理图撞号修复；其余改动以其提交时的现场记录与自动测试为准，不在本轮
重复写板（官方自动布线、泪滴、铺铜等）。用例、结果与独立复核见 `docs/releases/evidence/v1.15.0/`。

已知未覆盖：STEP 未经 CAD 导入；外壳螺丝柱正向识别仅单测（ceshi 无安装孔）；外壳未实际打印；
宿主拒绝组合改名的原因待查。

## 发布前核验（本地，未发布）

验收材料提交为 `95034970`（manifest：result/independentReview 均为 pass）。`make release-check VERSION=v1.15.0` 通过；
`make release-build VERSION=v1.15.0` 通过：五平台 CLI、连接器 1.15.0、`skills.tar.gz`（192 个文件）、安装脚本、
`test-evidence.zip` 与 `checksums.txt` 生成并核对，本机 `easyeda_darwin_arm64 --version` 为 v1.15.0。尚未打 tag、未上传。
dev CI：`e26c821f` 一次连接器测试偶发失败（#256/#257 静默窗口时序测试 `not ok 940`），`aa806afa`、`9fda0e2b` 均通过，
本地多次 M1 通过；按偶发记录，不改判。

## 发布结果

用户于 2026-10-25 批准执行 `make release VERSION=v1.15.0`。v1.15.0 于 2026-10-25 02:05:18 UTC 正式发布，非草稿、非预发布，
附注标签绑定提交 `d93d3dd`（产品源码同候选 `9fda0e2b`，其后仅发布文档），main 已快进到该提交。十一项资产（含 checksums.txt）
全部下载核对 SHA256 一致，下载的 darwin_arm64 报 v1.15.0；升级冒烟：已发布的 v1.14.0 二进制在临时目录经
`easyeda update --cli-only --version 1.15.0` 下载、校验并原子替换为 v1.15.0。
外部 Skill 平台须分别核验：后续公开接口复查时，ClawHub 的 `latest` 仍为 1.14.0，
查询 1.15.0 返回不存在，原“同步成功”记录不能作为当前可安装证据。
授权账号补发 1.15.0 后，发布接口返回 `Version 1.15.0 already exists`；
同账号的 inspect/verify 与官方 `skill tag ... 1.15.0 --tag latest` 均返回
`Version not found`。平台侧版本记录存在矛盾，尚未恢复公开可安装，不能靠重复上传或擅自升版补证。
SkillHub 原自动发布目标 `eda-agent-connector` 的公开下载返回 404；现已修正为
`@user_6b95f8df/easyeda-agent-connector`，并通过
[补发任务](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/40595144961)
提交 1.15.0（`skillId=163177 status=None`）。补发后立即下载的公开包仍为 1.2.10；
审核状态未返回，不能声明最新版已公开可安装，须待公开下载包更新后再验。
连接器 1.15.0 需用户更新后才能用到本版连接器行为。

## v1.14.0 历史记录

用户于 2026-10-17 批准发布中版本 v1.14.0。连接器运行时自 v1.13.1 起有改动（`connect_pin` 的 `via`、
`schematic.group.move` 重挂网络标签、原理图同步等待按请求剩余预算），按版本规则递增 minor；CLI/daemon、
连接器包、npm/lock 与 Skill 统一为 1.14.0，用户需要更新连接器才能用到这些行为。

## 版本范围

- 原理图功能框（#43）：`schematic_layout.tiles`、`<id>#<框>` 派生、逐框 `block-apply`、阅读顺序排框、`--tile-at`、
  分区框标题；ESP32-S3R8 芯片级块改为 5 框形态。
- 网络标签默认用于信号网，一列统一桩长不错开，网名不重复显示；竖脚端口拐弯挂横标签；三项新的标签/桩线重叠检查。
- 整体移动保留框内直连导线与网络标签；分区框间距；后台标签页不再要求切前台。

## 验收范围

用户选择沿用有限范围（schema 3，`acceptanceScope=cli-fixes`），只验本次改动；完整设计 E2E（#43，PCB 阶段未开始）
保持 in-progress，留待 follow-up。用例与结果见 `docs/releases/evidence/v1.14.0/`。

v1.14.0 于 2026-10-17 07:22:58 UTC 正式发布，非草稿、非预发布，附注标签绑定提交 `7cde1ae`，main 已快进到该提交。
十一项资产（含 checksums.txt）全部下载核对 SHA256 一致；升级冒烟：已发布的 v1.13.1 二进制在临时目录经
`easyeda update --cli-only --version 1.14.0` 下载、校验并原子替换为 v1.14.0。ClawHub 同步成功（easyeda-agent@1.14.0），
SkillHub 自动发布工作流成功。连接器 1.14.0 需用户更新后才能用到本版连接器行为。

## v1.13.1 历史记录

用户于 2026-10-16 批准发布小版本 v1.13.1。连接器运行时自 v1.13.0 起无改动（`extension/src` 无差异），按版本规则递增 patch；
CLI/daemon、连接器包、npm/lock 与 Skill 版本统一为 1.13.1，已导入的 1.13.0 连接器无需重新导入。

## 版本范围

- `easyeda update` 换完二进制后自动在后台重启仍在跑旧版本的已安装 daemon（`--restart-daemon`，默认开启）；任何命令发现
  已安装的 daemon 比 CLI 旧时也自动重启。`make dev` 等源码构建的 daemon 不动，旧 CLI 不降级新 daemon。思路来自 #278。

## 测试范围

按小版本规则，不跑全量需求到成品 E2E，也不重跑 B00–B10 / A00–A06。受影响自动测试：Go 全仓通过，新增
`TestStaleInstalledDaemon`、`TestShouldRestartDaemonAfterUpdate`、`TestRestartInstalledDaemonSkipsWhenAlreadyReplaced`、
`TestUpdateNotesReportDaemonRestart`。定向回归（本机测试端口 61900，不影响开发 daemon）：v1.12.0 daemon 遇 v1.13.0 CLI 的
`doc ls` 自动重启为 v1.13.0；v1.12.0 CLI 不降级；`update --cli-only` 在二进制已最新、daemon 落后时报告
`daemonRestart: restarted` 且 daemon 状态 current。不涉及 EDA 读写。版本一致性、release-check、release-build、资产 SHA256、
安装与升级冒烟及远端资产照常核验。完整设计 E2E（#43）保持 in-progress。

v1.13.1 于 2026-10-16 03:52:48 UTC 正式发布，非草稿、非预发布，附注标签绑定提交 `9e589cf`，main 已快进到该提交且 CI 通过。
九项资产（含 checksums.txt）全部下载核对 SHA256 一致；release-smoke 离线冒烟通过；升级冒烟：已发布的 v1.13.0 二进制经
`easyeda update --cli-only --version 1.13.1` 从 GitHub 下载、校验通过并原子替换为 v1.13.1。ClawHub 同步成功
（easyeda-agent@1.13.1），SkillHub 自动发布工作流成功。

## v1.13.0 历史记录

用户于 2026-10-16 批准发布中版本 v1.13.0（选择"先修再发"：S1 现场发现的 zone move 重画框间歇失败修复后重跑全部用例），
沿用有限范围。连接器运行时自 v1.12.0 起有改动（库资产 build 核对与焊盘元组校验、`pcb.component.modify` 层名、
保留器件清页的引脚闭包、无网表实例的引脚网解析、规则回读标准形式、typed `schematic.text.move`、
`pcb.route.delete` 内嵌对象判据），按版本规则递增 minor；CLI/daemon、连接器和 Skill 统一为 1.13.0。

## 版本范围

- 库：资产 build 先核对 `--library`（#279）、焊盘形状按官方元组离线校验、工程库封装明确拒绝、删除回读等待晚落地。
- 原理图：保留器件清页保护引脚属性（#267）、无网表实例按导线解析引脚网（#244）、typed 文本平移（#212）、
  几何守卫按差分判定、分区框清除改走 typed 删除并等落定、clear/export 派发预算、export-image 按扩展名推断格式、
  关系块预留包络折返与锚件平移（引脚回读不再导出网表）。
- PCB：`modify --layer` 层名换数字 id 并回读（#275）、规则回读标准形式（#258）、`pcb check` 插口开口朝内（#231）、
  PCB 虚拟编组 `pcb group`（#173）、route delete 内嵌对象判据。
- daemon：`health` 写入健康度带 `lastFailureError`；几何守卫读取失败报宿主错误。

## 发布范围与状态

候选产品源码为 dev 提交 `e5ed0fb`。[有限验收报告](evidence/v1.13.0/test-report.md) 通过 schema 3
记录绑定本版本的用户范围决定；M1/F1/F2/E1/L1/N1/R1 与 S1/K1/P1 在冻结候选上、EDA 标签页后台（hidden）状态下现场执行并经独立复核。
完整设计 E2E（#43 ESP32-S3 芯片级 N8R8）进行中，记 in-progress，留待 follow-up；外围实线直连仍未达标。

升级 CLI/Skill 使用 `easyeda update`；本版连接器有运行时改动，需重新导入 1.13.0 连接器 .eext。

v1.13.0 于 2026-10-16 02:55:21 UTC 正式发布，非草稿、非预发布，附注标签绑定提交 `aa6a533`，main 已快进到该提交且 CI 通过。
十项资产加 checksums.txt 共十一份文件，远端 checksums 与本地资产逐项一致，抽查下载的连接器与 Skill 包哈希一致。
ClawHub 同步成功（easyeda-agent@1.13.0）；SkillHub 自动发布工作流成功（Skill 包 189 个文件，低于平台 200 上限）。
#279 以本版发布链接关闭。

## v1.12.0 历史记录

用户于 2026-10-14 批准发布 v1.12，本轮重点为稳定性、易用性与降低接入门槛，沿用有限范围。连接器运行时
自 v1.11.0 起有改动（后台安全等待与导入判定、未知器件拒绝、无活动文档语义、connect_pin 晚到标识、
可见文字 bbox 动作、silk-align 归属判据），按版本规则递增 minor；CLI/daemon、连接器和 Skill 统一为 1.12.0。

## 版本范围

- 后台标签页：读写、导入、`doc reload`、DRC 与原理图图片导出在 EDA 窗口最小化时照常完成；仅 PCB 画布截图需前台。
- 接入：无 daemon 时自动后台启动；`health` 逐项体检清单；无活动文档不再误标连接器降级。
- 修复：connect_pin 晚到标识（#208）、后台重载（#276）、未知器件放置永久挂起、silk-align 误放与误报、
  auto-place 压禁布区、可见属性越出纸张无人报告、读取调用数过高。

## 发布范围与状态

候选产品源码为 dev 提交 `fd334d9`。[有限验收报告](evidence/v1.12.0/test-report.md) 通过 schema 3
记录绑定本版本的用户范围决定；M1/F1/F2/E1/L1/N1/R1 在冻结候选上、EDA 窗口最小化状态下重新现场执行并经独立复核。
完整设计 E2E 本版未重跑，记 not-run，留待 follow-up。

升级 CLI/Skill 使用 `easyeda update`；本版连接器有运行时改动，需重新导入 1.12.0 连接器 .eext。

v1.12.0 于 2026-10-14 10:26:02 UTC 正式发布，非草稿、非预发布，附注标签绑定提交 `6fb8926`，main 已快进到该提交。
十项资产加 checksums.txt 共十一份文件，远端核对后才转正式。ClawHub 同步成功（easyeda-agent@1.12.0）。
main 的 CI 首次运行 Windows 安装面一项在 npm 本地包安装环节偶发失败（同一提交在 dev 上全绿），重跑后五项全部通过。
SkillHub [自动发布](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/38549536013) 仍被平台
"最多 200 个文件"限制拒绝（本包 205 个），未为通过上传删减设计知识。立创连接器市场需人工提交，未在本轮更新。
#208、#276 已用本版发布链接关闭。

## v1.11.0 历史记录

用户于 2026-10-14 选择按有限范围发布 v1.11.0。连接器运行时自 v1.10.0 起有改动（页面可见性、
删除级联与显式 id、bridge-check 孤桩判据、导入空操作核验），按版本规则递增 minor；
CLI/daemon、连接器和 Skill 统一为 1.11.0。

## 版本范围

- 原理图：外围实线直连门禁、页面边界统一检查、块 attach 贴脚成排、`compose` 状态与工程名统一。
- PCB：导入默认不回填库属性（消除 DRC Netlist Error）、陈旧分区认领整表判废、天线禁铜整板宽、
  `silk-off-board`、dump 语义哈希浮点归一、已同步导入判为核验通过的空操作。
- 安全：删除必须显式 id、`page-delete` 回读判定、文档类型不符给出 `--doc` 提示。

## 发布范围与状态

候选产品源码为 dev 提交 `699d140`。[有限验收报告](evidence/v1.11.0/test-report.md) 通过 schema 3
记录绑定本版本的用户范围决定；M1/F1/F2/E1/L1/N1/R1 在冻结候选上重新现场执行并经独立复核。
ESP32-S3 最小点灯板已在开发过程中从原始需求跑到两层 PCB 布线完成、原生 DRC passed，但期间多次
修改源码与热重载连接器，完整设计 E2E 记 in-progress，留待 follow-up。

升级 CLI/Skill 使用 `easyeda update`；本版新增连接器动作字段，需重新导入 1.11.0 连接器 .eext。

v1.11.0 于 2026-10-13 18:07:43 UTC 正式发布，非草稿、非预发布，附注标签绑定提交 `1f6c920`，main 已快进到该提交。
十项资产加 checksums.txt 共十一份文件，远端核对后才转正式。ClawHub 同步成功（easyeda-agent@1.11.0）。
SkillHub [自动发布](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/38444234203) 仍被平台
"最多 200 个文件"限制拒绝（本包 205 个），未为通过上传删减设计知识。立创连接器市场需人工提交，未在本轮更新。

## v1.10.0 历史记录


当前公开正式版为 [v1.10.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.10.0)。
用户已批准 v1.10.0，指示复用刚完成的验证并直接发布；本轮按明确的有限 CLI/工作流范围交付。
新动作与字段要求重新导入连接器，按版本规则递增 minor，CLI/daemon、连接器和 Skill 统一为 1.10.0。

## 版本范围

- 实际 PCB 正反面 SVG 与离线图册，保留官方图片及明确的缺测、镜像、未布线和问题状态。
- 保存重载阶段回执、文档就绪读取、PCB 采集身份/哈希/丝印读取状态与截图判定。
- dev 已提交的原理图保留、布局与公共库解析、库身份/开窗核对及实验性同源回写。
- 排除工作区其他未提交修改、私有设计数据与未完成的封装修补。

## 发布范围与状态

候选产品源码为 `46dc97bf`。其 v1.10.0-dev.1 的 Go、连接器、Skill、发布脚本、五平台交叉构建
和原生离线资产 smoke 已通过；现场三次保存重载、实测丝印与采集哈希通过。
本次不重复这些测试，不跑完整基础批次或 ESP32-S3 需求到成品回归。
[有限验收报告](evidence/v1.10.0/test-report.md) 通过 schema 3 记录绑定本版本的用户范围决定，
完整设计 E2E 留待 follow-up；已有工程创建无 UUID、官方截图空白、测试夹具 Netlist Error
及导航超时等失败继续保留，不把工具发布当作电路或制造验收。

v1.10.0 于 2026-10-11 14:02:54 UTC 正式发布，非草稿、非预发布。
附注标签绑定提交 `66b44aab4d833cd0bb6afb6d3b1daa3ad7408817`；正式版本构建与 release-check 通过，
五平台 CLI、连接器、Skill、安装脚本及验收说明共十项资产，加 checksums.txt 共十一份文件，
远端名称、大小及 SHA256 全部匹配。GitHub 包保留 205 个 Skill 文件。
本次只执行正式打包与资产核对，没有重跑最近的工程测试、现场回归或完整 E2E；
完整验收与失败状态保持冻结报告中的原结论。

SkillHub [自动发布](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/38145602735)
被平台“最多 200 个文件”限制拒绝，未同步成功；没有为通过上传而删除设计知识或改写正式包。
ClawHub 因本机没有 clawhub 命令而未同步。立创连接器市场仍需要人工提交，未在本轮更新。
上述平台状态与已核验的 GitHub Release 分别记录。

升级 CLI/Skill 使用 `easyeda update`；新增 EDA 动作需重新导入本版连接器 .eext。
历史有限范围不成为下一版本的默认豁免，完整设计后续仍按仓库验收流程执行。

## v1.9.1 历史记录

# v1.9.1 发布评估

当前正式版为 [v1.9.1](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.9.1)，已获用户批准并正式发布。
本版只纳入 main 已集成的 DSH 分发修复与对应说明，附注标签绑定提交 `b2522c8701367da31e3af4519be68a04ff58af7b`。
发布规则见 [发布流程](../release-workflow.md)，既有设计验收范围见
[v1.9.0 发布记录](release-1.9.md)，日常开发状态见 [CLI Status](../cli-STATUS.md)。

## 修复与用户收益

- #274：根包提供声明的 `index.js` 入口和 MCP SDK，Git 安装不再缺入口或嵌套依赖。
- 分发白名单限制为公开入口、bundle patch、MCP 源码与 Skill，排除本地运行资料及无关代码。
- npm tarball 在仓库外安装、SDK/MCP 握手、12 个工具与目录发现回归覆盖三平台 CI；
  `--prefer-offline` 允许缺失元数据时联网，不声明严格断网安装。
- DSH 安装文档补齐独立 CLI、Windows `EASYEDA_BIN`、配置与运行检查的区别。

入口修复为 [4391cfd](https://github.com/zhoushoujianwork/easyeda-agent/commit/4391cfd2a5188689d26da5eba80fe54177c1e998)，
干净缓存回归修复为 [b535c26](https://github.com/zhoushoujianwork/easyeda-agent/commit/b535c26c4047fcdce982c4565f6c9ae6a59c5084)。
本版不改变连接器运行逻辑或动作契约，按 patch 递增，全部分发组件仍统一为 1.9.1。

## 验证状态与边界

- 发布提交 `b2522c8` 的 [CI](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/38118649015)
  全部通过；Windows/macOS/Linux 的 npm 分发测试均为 8/8，CI 没有运行 DSH 安装器。
- macOS 独立 DSH_HOME、全新 web profile 的真实 Git 安装、Hub v1.4.8 装后入口检查、
  官方 DSH Web 启动、12 个 MCP 工具、真实 CLI 目录调用和 Skill provider 正文读取通过。
  CLI 0.1.5-rc.1 的 MCP/Skill 依赖解析为 rc.3，loader 为 1.0.3；
  [完整反馈](https://github.com/zhoushoujianwork/easyeda-agent/issues/274#issuecomment-6104813413)保留精确范围。
- v1.9.1 候选的版本一致性、打包/MCP 回归、发布脚本与自更新定向测试、发布构建通过；
  9 项资产 SHA256、15 项本机 CLI 冒烟及 201 个 Skill 文件与安装后链接核对通过。
- 发布前，macOS 隔离目录中的真实安装脚本读取候选资产字节，首次安装和从 v1.9.0 升级通过，
  下载端点使用本地替身；候选本地 Git ref 的 DSH 安装、Web/MCP/Skill 加载也通过。
- 发布后，GitHub 非草稿、非预发布的 v1.9.1 已成为 latest；9 项资产及 `checksums.txt`
  全部实际下载，并核对名称、大小、SHA256 与本地发布资产一致。
- macOS 独立目录中，从真实 GitHub latest 首次安装和从 v1.9.0 执行 `easyeda update`
  联网升级通过；CLI、Skill 版本与 201 个 Skill 文件内容全部对账，没有替换下载端点。
  测试限定安装目录与 Skill 目标，没有启动 daemon 或操作 EDA。
- 独立 DSH_HOME 中的正式 `#v1.9.1` Git 安装解析到上述发布提交；真实 Web 返回 HTTP 200，
  12 个 MCP 工具、CLI 离线目录调用及 1.9.1 Skill 正文读取通过。
  Hub v1.4.8 原版函数按已安装包名检查入口通过；按带标签 Git URL 解析依赖时跳过检查，
  此跳过不计为通过，完整 Hub 网页流程仍未验证。
- 原 Windows 报障环境、Hub 网页完整安装流程仍待复测，#274 保持 open。
  发布修复包不自动关闭用户故障。
- 完整设计 E2E 保持原状态。本次不运行全集，不把离线或 DSH 加载成功补签为 EDA 设计通过。

SkillHub 的 [自动发布](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/38118948996)
成功，平台回执 `skillId=173258` 未返回审核状态，不声明市场前台已可安装。
ClawHub 本机未登录，本次未同步；已有发布授权下可在登录后补发，不影响已核验的 GitHub Release。

## 其他近期 Issue

#273 的显式标签/展开布局已在 v1.8.1 提供离线替代路径，默认搜索耗尽仍未修复；
#267 在 dev 已提交的 CLI 保留器件重建有有限防护，但广义连接器清理和现场验收仍未完成；
#268 的后台保存改动仍在未提交开发工作中。它们不计为 v1.9.1 已解决，也不夹带入此分发补丁。

v1.8.0 的历史发布评估与有限验收范围保留在 [对应发布记录](release-1.8.md)，
不作为新版本的默认豁免。
