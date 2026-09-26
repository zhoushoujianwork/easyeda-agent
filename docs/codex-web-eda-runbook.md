# Codex 内置浏览器：本地版本测试执行步骤

供 Codex Agent 在用户已登录的 **Web EasyEDA Pro** 中复测本仓库的本地开发版。当前工程操作
遵守根目录 [AGENTS.md](../AGENTS.md) 和公开 [easyeda-agent Skill](../.agents/skills/easyeda-agent/SKILL.md)：
工程读写只走 typed `easyeda` 命令或受保护 Apply；浏览器界面只用于打开工程、管理连接器
与只读观察。原理图的数据准备与转换细节见
[数据驱动架构基准](../.agents/skills/easyeda-agent/references/schematic-data.md#数据驱动架构基准)。

## 1. 准备可识别的本地版本

`make dev` 只热重建 Go CLI/daemon。连接器也有**开发专用热更新**：对已安装且授权外部交互
的同一 UUID，按 [开发环境 §5](dev-environment.md#5-hot-reload-the-connector-skip-uninstall--re-import)
核对旧/新 bundle 哈希后，经本地 WebSocket 原子替换 IndexedDB 中的连接器文件与版本，
再重载网页；这省去卸载和重新导入。它不是运行中 JavaScript 的即时替换，也不是普通
用户升级渠道。新安装、旧扩展已卸载或校验不通过时才走下方 `.eext` 导入流程；
两条路径都须以运行中的 `connectorVersion`、工程/文档身份和 fresh 对象回读验收。

1. 从源码选择新的 `vX.Y.Z-dev.N`，同步连接器、npm、Skill 元数据和 changelog；**每次源码改变
   递增 N**，不以同版不同内容充当独立验收候选。
2. 运行与改动相应的测试、`make skill-check` 和 `git diff --check`，审查属于本轮的源码；
   现场验证完成后提交相关代码与记录。
3. 构建可信本地包。命令里的版本和 `darwin_arm64` 按当前候选及本机平台替换。

```bash
make local-build VERSION=vX.Y.Z-dev.N DIST="$PWD/dist/local-vX.Y.Z-dev.N"
```

4. **切换运行时之前**，在旧 CLI/daemon/connector 精确同版的窗口里对每个已打开的测试页运行
   `easyeda sch save --project <project> --doc <page>`，逐页保留 `saved:true` 回包。版本不符
   或窗口未连接时停止，不能用 GUI 保存兜底。
5. 安装包里的 CLI 和 Skill，再用安装后的 CLI 重启 daemon；daemon 命令在单独终端保持运行。

```bash
dist/local-vX.Y.Z-dev.N/easyeda_darwin_arm64 update \
  --local-dir "$PWD/dist/local-vX.Y.Z-dev.N" \
  --binary "$(command -v easyeda)"
make local-daemon-restart LOCAL_EASYEDA="$(command -v easyeda)"
```

6. 已安装同 UUID 侧载连接器且仍启用外部交互时，**优先执行**
   [有界热更新](dev-environment.md#5-hot-reload-the-connector-skip-uninstall--re-import)：
   记录 team UUID、连接器 UUID/旧版本、已安装 bundle 哈希与新 bundle 哈希；保存当前文档，
   启动仓库自带 WS 文件服务器，通过正常 `debug exec` 运行已审阅的专用注入脚本。
   脚本只更新该连接器的两个 IndexedDB 记录，不改权限、工程对象或站点数据；身份、权限、
   哈希或原子写入校验失败就停止，不以任意 JS 兜底。脚本安排的网页重载完成后再核对运行态。
   用户明确要求检验卸载与导入流程时，直接执行第 7 步。
7. 首次安装、旧项已卸载或热更新前置校验不成立时，用户授权 Codex 管理侧载扩展后，
   可由 Codex 在当前内置浏览器完成[同 UUID 卸载与导入](#同-uuid-侧载扩展由-codex-轮换)。
   扩展列表显示新版本仍不证明当前网页运行新代码。
8. Web 端运行中已具备 `easyeda web reload` 时，用该 typed 命令保存当前文档、刷新整个页面，
   核对新 window ID、原工程/文档和 fresh 对象回读；其他已打开文档先逐页保存。
   如果旧连接器没有该动作，由用户刷新或重开当前 Web 页面。Agent 不用 GUI 刷新来恢复
   卡死工程或代替 typed 验证。热更新则等待专用脚本安排的重载。

### 同 UUID 侧载扩展由 Codex 轮换

仅在用户明确要求更新连接器、目标是用户已打开的 Web EDA 时使用。此处的浏览器操作
只管理扩展，不在 GUI 中创建、保存、修复或验证电路。2026-09-24 的
[实际轮换记录](reviews/2026-09-24-v1.6.0-dev17-connector-rotation.md)已用此流程将
`dev.15 → dev.16 → dev.17`，最终 `dev.17` 正式 `web reload` 通过。

1. `easyeda health` 确认目标窗口的工程/文档和旧连接器版本；核对新 `.eext` 的 manifest
   UUID、版本和包哈希。用旧连接器的 typed `sch save` / `pcb save` 逐一保存该窗口所有已打开文档。
2. 用 Codex 的内置浏览器控制绑定**已有** Web EDA 标签；进入 **高级 → 扩展管理器 → 已安装**，
   打开 EDA Agent Connector 详情，核对旧版本及 UUID。不要凭名称认定版本。
3. 点击该项的**卸载**，在确认框再次核对名称、版本、UUID，勾选“我已知悉”并确认。
   等待“旧版本已卸载”和已安装列表空态；然后点击**导入**。浏览器自动化在点击前先订阅
   `filechooser`，再用 `setFiles([<新包绝对路径>])` 选择 `.eext`，不依赖固定坐标或
   文件选择器的手工输入。接受导入后的安全提示。
4. 在新条目详情核对新版本和相同 UUID；打开**配置**，将**允许外部交互**从关闭设为开启，
   看到已启用提示并再次读取勾选状态。卸载重装会将此权限重置为关闭；只恢复之前对同一
   连接器的权限。首次授权仍按浏览器权限规则处理。
5. 关闭扩展管理器，运行 `easyeda health` 核对 CLI、daemon、运行中 connector 精确同版，
   且 `windows` 中目标工程和文档正确。若列表是新版本而运行中仍是旧版，先按第 8 步完成
   页面重载，再重新核对；绝不凭导入 toast 或新 window ID 单独判定成功。

浏览器可访问性索引会在弹框、页签切换后改变：每次操作后重新读取 UI 状态，按文本和版本
定位；点击复选框若工具报错但 UI 已勾选，先核对新状态，不盲点第二次。该流程属于用户授权
的本地开发包轮换，不是 daemon 的静默自动更新，也不授权 GUI 工程编辑。

## 2. 核对实际运行时与测试工程

```bash
easyeda health
easyeda update --local-dir "$PWD/dist/local-vX.Y.Z-dev.N" --check --exit-code
```

从 `health.windows` 精确核对目标 `projectUuid`、`documentUuid`、`documentType` 和宿主版本；
本地测试的 CLI、daemon、运行中的 `connectorVersion` 须是同一 `dev.N`。`windowId` 重连会变，
不作为持久身份。`update --check` 还核对安装的 Skill 与本地包内容；**导入成功、扩展列表版本、
新 windowId 或浏览器标签已打开都不替代这一步**。若页面仍报旧 connector，停写并保留
`health` 输出；由用户完成 Web 页面刷新/重开后再核对，不反复 Apply、清站点数据或切桌面版。
页面切换也可能重新加载旧连接器：**首次 `health` 同版后，切到目标页再查一次**。若版本
回退或 typed `document.open` 超时，立即停写，保存超时与新旧 `health`；请用户检查扩展管理
只启用目标 `.eext`，并关闭当前测试标签、从工程链接新开 Web 标签。若出现未保存提示先核实
现场状态。新标签再次精确同版、目标页 fresh 回读稳定之前，不进行测量页写入或 Apply。

用户要新测试工程时，在已连接的真实窗口通过 typed
`easyeda project create --window <health-window-id> --name ... --open` 创建（不要给尚未存在的
工程传 `--project` 或 `--doc`），记录返回的工程 UUID；新建原理图页后记录页 UUID，
再次核对 `health.windows`。不要把
旧项目 `ceshi` 或其他现场工程当作临时画布。单个 Web 窗口的 typed 调用串行执行，所有写命令
显式带 `--project` 和 `--doc`。

`project.create` 返回空 UUID 时先停下：通过
`easyeda project find --window <health-window-id> --name <完整友好名称> --team <目标团队UUID>`
只读核对团队根目录。只有清单 `complete:true` 且 `presence:"absent"` 才能认定该范围内
未创建；`unknown` 或同名多项不能重试。2026-09-24 本地 `dev.7` 实测：在已有工程的
team UUID 下显式传 `--team` 的创建返回空 UUID，经 50 项完整清单确认不存在后，
按首次成功的参数**省略 `--team`** 创建成功。不要把这一现场现象推广为所有团队的 API 规则；
新工程仍须核对返回 UUID、团队和空白页完整对象回读。

## 3. 让无历史上下文的 Codex 执行与独立验收

先按[基础 CLI 动作测试](cli-live-test.md)和[逐例前置条件](cli-live-test-detail.md)
确认 B00–B10 全部通过。以下步骤属于[高级 CLI 业务验收](cli-advanced-test.md)：
它验证客户需求驱动的设计，不能用于补签基础动作门禁。
用户已明确接受的版本范围例外按[高级进入条件](cli-advanced-test.md#进入条件)执行；
v1.8.0 不再因 #55、#260、#261 等待全部修复或重复基础全集，原结果仍如实保留。

给**新上下文**执行 subagent（`fork_turns: none`）只提供下列任务 prompt 和
[`esp32MiniRequire.md`「一、客户原始需求」](../esp32MiniRequire.md#一客户原始需求)。不提供
历史报告、加工后的 BOM/UUID/网表、预制布局或答案图；它自行选型和规划。

> 你是独立测试执行员。先读 AGENTS.md、公开 easyeda-agent Skill 和客户原始需求。
> 在已核对版本及工程/页 UUID 的专用 Web EDA 测试工程中，从原始快照构建参数化连接与
> 核心/外围归属，逐件测量唯一可见位号的官方 bbox，完成区内布局、整页布局、固定转换、
> 受保护 Apply。图签文本进入逐页源。对器件、物理引脚到网/NC、真实直连、位号、框和
> 导线逐对象回读；对“插上 USB 就能烧录”记录是否支持免手按键进入下载模式，
> 区分原理图可证明的控制路径与必须由实板证明的烧录行为。通过严格检查后显式保存、
> 真实重载、新鲜回读。随后只从保留源做一次
> 核心及专属外围的局部移动，并证明范围外对象不变；再测写前拒绝与重算幂等。保存所有
> 输入、哈希、计划、journal、错误和 readback。任何连接/加载/保存/回读失败立即停写，
> 不用 GUI 或任意 JS 补工程。逐项报告 pass、fail、blocked、not-run，不沿用旧结论。

每个离线布局候选以独立文件名保存原始输入和报告，禁止覆盖旧候选输入。提交评审前逐一
核对报告 `sourceSha256` 与其配对输入文件原始字节 SHA-256；不配对的旧报告只作为失败
发生过的记录，不能解释当前候选或进入 compose/Apply。成功页、重算页也适用此规则。

主 Agent 只协调该窗口；其他 subagent 可并行做离线源审查。需要独立验收时另开**新上下文**
评审 subagent（同样 `fork_turns: none`），只给冻结的输入、journal 与 fresh 证据，
不给执行员的自评结论；在主 Agent
暂停访问窗口时才允许只读现场核查。具体场景 M1/F1/F2/E1/L1/L2/N1/R1 和判据见
[1.6.0 测试用例](releases/evidence/v1.6.0/test-cases.md)；历史记录只作结果证据。

目标页尚未放置的器件若需官方位号 bbox，可用**专用临时原理图页**测量：先冻结目标页
fresh 对象快照，typed 创建临时页并保存返回 UUID，只放与源一致的 device/变体/旋转，
用 `sch designator-geometry` 和 fresh `sch list --include-pins` 逐件对齐位号、parent 与页身份。
按原始创建 UUID typed 删除临时页，再读页列表与原目标页完整对象并比较；保留创建/删除
请求和回包。即时相等只证明内存状态，未经过 save→reload→fresh readback 时，清理的
持久化仍标 `incomplete`。运行期间其他 Agent 不访问同一窗口。

正式纸张布局前核对 typed `sch sheet-geometry` 是否给出红色绘图区内框的精确边界。
只有外纸张尺寸和图签避让区时，保守工作矩形可供离线预览，但不能替代内框入页判据；
记录原响应并标 `unsupported`，补齐采集后再进入现场写前门禁。

逐件只允许一条 attachment 表达所属外围的主依附关系；同一器件分别用两个真实引脚
再声明一次，哪怕端点同网，也应在 `sch zone-review` / `sch layout-plan --zones` 被拒绝。
新增正式页面后先重新读取**所有目标页**的 fresh `sch list`，再编 guarded playbook：
图框的派生 `@Page Count` 会随页面数变化，使建页前的快照过期。逐页对比对象时可单独
报告这个派生字段，不能因此忽略器件、引脚、导线或实例身份的实际差异。
使用 `--replace` 清理已有页面时，队列必须在 `sch clear` **之前**核对该页全部将被删除的
图元清单及内容，至少覆盖器件、导线、网络标记和图形/图框；仅检查器件数量或执行清页后的
`--expect-empty` 不足以保护现场。对相同器件但额外一条导线的快照做负例 dry-run，确认
写前拒绝；独立评审通过后才执行。重编队列须以最终版本连接器的新鲜页快照为输入。

## 4. 判定与收尾

- 完整原理图须核对客户需求、全部物理引脚、网络/NC、所有权、位号实测、direct 线树、
  图签与几何；逐页严格检查和 DRC 后显式保存，再 `doc reload` 并 fresh 回读。截图只辅助找漏。
- 局部修改须由保留源重算，记录改变范围及范围外不变量；合法目标现场 Apply、保存重载后
  复核。受阻目标须在首次设计写入前拒绝，前后完整对象和语义哈希一致。旧线/旧标记修复
  只在真实缺陷可回读时执行。
- 失败的 Apply journal 不从失败步骤盲续跑。先记录 fresh 现场状态；无保存/重载证据就记
  `incomplete`。官方 DRC 只有聚合数时，不猜警告对象。
- 每轮冻结版本、输入 SHA-256、命令、计划、journal 和现场回读；用
  `easyeda audit cost --day ... --since ... --until ... --label ... --record` 记成本画像。
  测试结束检查 `git diff`、运行相应测试并提交仓库改动；不因本地测试创建正式发布标签。

这是原理图专项执行步骤（至 S6）。需要从客户需求跑到 PCB 成品时，按
[固定端到端验收](e2e-automation-acceptance.md)继续完整 S0–S6/P0–P10，仍只向执行 Agent
提供原始需求第一节。
