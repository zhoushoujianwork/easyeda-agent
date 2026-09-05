# 环境自举 — agent 自己把「可用的 EasyEDA 环境」拉起来

`NO_CONNECTOR` / `windows: []` 不是终点。有 chrome-devtools MCP **且用户用的是网页版**
时,agent 可以**自己**完成:开 web 编辑器 → 打开目标工程 → 确认连接器附着 →
(需要时)热重载新连接器。全流程 2026-07-07 在 ceshi 工程真机跑通。没有浏览器控制
工具、**或用户用的是桌面客户端**时,才退回「请用户手工打开 / 切换 EasyEDA 工程」。

## 桌面版 vs 网页版 —— 自动开工程能力的边界(先判这一条)

**连接器本身对两者一视同仁**:`.eext` 装进 EasyEDA(桌面或网页都行),后台端口扫描
`60832-60841`(`0xEDA0`-`0xEDA9`)附着 daemon,附着后所有 `easyeda` typed action **完全一样**。区别只在
**「打开 / 切换工程」这一步能不能自动化**:

| 宿主 | 自动开/切工程 | 说明 |
|---|---|---|
| **网页版**(Chrome 里的 `pro.lceda.cn/editor`) | ✅ 可自动 | chrome-devtools MCP `navigate_page` 改 `#id=<uuid>` + reload,连接器 15-30s 自附着;切文档再 `easyeda doc switch`。见下方 §1。 |
| **桌面客户端**(嘉立创EDA专业版 App) | ❌ 不能自动 | chrome-devtools MCP 控制的是浏览器,**够不到桌面 App 窗口**——没有 API 能让 agent 替用户点开工程。**必须请用户在 App 里手动打开/切换目标工程**,之后连接器照常附着,CLI 动作照常工作。 |

**判定**:`easyeda daemon health` 的 `windows[].easyedaVersion` 能连上但 `context` 不是
目标工程,且你**无法用 chrome-devtools 把它切过去**(navigate 无效果 = 大概率桌面版)
→ 停下来请用户手动切,别在桌面版上空跑 navigate。跨工程抄电路(如抄立创官方板的
自动下载电路)时尤其注意:桌面版下要让用户先把那块参考板打开。

## 0. 判定当前环境

```bash
easyeda daemon health
```

- `status: found` + `windows: []` → daemon 活着、没有编辑器。走下面的自举。
- 连 daemon 都没有 → 先 `make dev`(开发)或 `easyeda daemon start &`。
- `windows[]` 有条目 → 环境就绪,看 `context` 是否是目标工程/文档,不是就
  `easyeda doc switch <name> --project <name>`。

## 0.5 版本对齐 —— CLI / skill / 连接器三方同版

`connectorVersionOk:false`、动作报 `UNKNOWN_ACTION`、或用户问「怎么升级」时,**先对版本,
别怀疑电路**。一条命令看清三方:

```bash
easyeda update --check               # 只读:cli / skill:<client> / connector 三行对齐表
easyeda update --check --exit-code   # 有落后 → 退出码 10(可 gate,0=齐,1=检查本身失败)
easyeda update                       # 升 CLI 二进制 + skill 目录(--check 之外唯一会写盘的形态)
easyeda update --skill-only          # 只同步 skill(等价 easyeda skill sync)
easyeda update --version 0.25.0      # 钉版本(离线场景也可用它跳过 GitHub API)
```

逐行读法:

| 行 | 状态 | 该做什么 |
|---|---|---|
| `cli` | `behind` | `easyeda update` 原地换二进制(下载→有 `checksums.txt` 就 sha256 校验→跑一次确认版本→原子替换)。装在 root 目录时 `sudo easyeda update`。**升完 daemon 仍跑旧二进制,必须重启 daemon**。 |
| `cli` | `skipped (dev build)` | 开发环境的 git-describe 版本,**故意不覆盖**(air 下一次改 `.go` 就重建);要强升才 `--force`。 |
| `skill:<client>` | `behind` | `easyeda update` 一并同步(daemon 启动时也会自动同步);本地改过 skill 想保留加 `--preserve`。 |
| `skill:<client>` | `not-installed` | 该客户端没装过 → `easyeda update --create-missing`。 |
| `connector` | `behind` | **只能人工重装**——侧载 `.eext` 没有原地更新。按提示 URL 下载 → 扩展管理里**先卸载旧的**(平台按 uuid 去重,不卸载则导入静默失败)→ 导入新的 → **完全退出并重启 EasyEDA**(已开窗口会继续跑旧连接器代码并抢 daemon)。市场版可自动更新但滞后 CLI。**旧连接器不只是缺新动作**:它不带 `seq`/`seqAbandoned` 顺序证据,于是「写超时了到底落没落地」这类判定会**降级成弱证据**(报文里写着 `证据档:弱(探针启发式)`),而且动作在连接器里是**并发**跑的 —— 一个卡死的调用会静默吞掉后续的 `place`/`delete`/`document.open`。看到弱证据档就该升级。 |
| `connector` | `no-daemon` / `no-window` | 不是版本问题,是环境没起来 → 回 §0。 |

> `--check` 从不写盘;非 `--check` 形态只碰 CLI 二进制和 skill 目录,**永不动 EasyEDA 工程**。

### 版本一致性门(硬拒,分级是非对称的)—— issue #181

`easyeda health` 的报文里现在有一块 **`versionGate`**(`verdict` + 逐组件
`findings[].{severity,reason,fix}`),人话摘要与它同源。**任何派发 typed action 的命令**
在本进程第一次派发前判一次(`sync.Once`,复合命令不会重复刷屏);判据复用那次已经
发生的 `/health` 报文,**零额外往返**。`health` / `version` / `update` / `daemon start`
**都不经过这道门** —— 门拦住你的时候,诊断和修复路径照样能用。

| 组件 | 判据 | 为什么这么分 | 修法 |
|---|---|---|---|
| **daemon** | **任何差异都拒(含 patch)** | 它和 CLI 是同一份二进制、同一个版本号,发版时必然相等;不等只可能是**「老进程没重启」**这一种意外,修复代价≈0。而漏判的代价是**污染之后每一条排查**(「明明修过的 bug 又复现」——因为跑的是旧 daemon)。「这一版是纯文档发布、行为没变」不是豁免理由:版本不等就是「老进程在跑」的事实,下一版可能就有差异 | 开发中(air):切到跑 `make dev` 的终端,任何 `.go` 改动它都会自动重建+重启;它退了就重开一个。**别手动 kill air 下的 daemon**(会卡死连接器)。非开发:直接 `easyeda daemon start`(自动接管 60832 上的旧进程,不必先 kill)。确认:`easyeda health` 的 version == `easyeda version` |
| **connector** | 差 **minor 及以上 → 拒**;仅差 **patch → 只警告不拦** | 它走**另一条分发渠道**(jlc-ext 无发布 API,每次发版靠人工重投),「市场版落后一点」是多数用户的**常态**,而修复代价高(卸载→重导→**完全退出重启 EasyEDA**,可能丢未存盘编辑)。对常态化的 patch 落后一律拒 = 工具对多数人不可用;而 minor 以上意味着连接器可能**根本没有**这版 CLI 要调的 handler,静默走偏比打断更贵 | 同上表 `connector: behind` 行(下载同版 `.eext` → 先卸载旧的 → 导入 → 完全退出重启 EasyEDA) |
| 任一侧非 clean release tag | **不做硬判定**(dev 戳豁免) | `make dev` 的 git-describe 戳(`v1.1.1-19-g…-dirty`)的 semver core 是**旧 tag**,不代表真实代码水位 | 唯一例外:两边都是 dev 戳但**戳串不同** → warn(不拦)—— 那不是 false flag,是两个不同构建的实锤,多半是 daemon 没跟上最近一次重建 |

**逃生口**:全局 flag `--skip-version-check`,或 `EASYEDA_SKIP_VERSION_CHECK=1`
(给加不了 flag 的调用方:脚本 / MCP 适配层 / CI)。放行仍会把错位打到 stderr,并写
一行审计 `cli.version_check.skip`(伪动作名,**不污染真实动作的调用统计**,可 grep)。

> **撞上这道门时的正确反应**:先对版本,**别改电路、别怀疑「工具坏了」**。这道门存在
> 的全部理由就是——上报者烧掉一整轮排查,正是因为没人告诉他「你跑的是旧 daemon」。

## 1. 打开 web 编辑器 + 目标工程(chrome-devtools MCP)

桌面客户端没开时,web 编辑器 `https://pro.lceda.cn/editor` 是完全等价的宿主
(同一 Chromium webview,连接器装在浏览器 profile 的 IndexedDB 里,登录态也在
profile 里持久化)。

```
1. new_page → https://pro.lceda.cn/editor#id=<projectUuid>
   ⚠️ #id= 直达【只在全新页面加载时生效】——已加载的编辑器里改 hash / 再
   navigate 同页 都不会触发打开工程。
2. 不知道 projectUuid?先开裸编辑器,take_snapshot 首页,工程树里每个工程是
   link "名字" url="…#id=<uuid>" —— uuid 直接读出来。
   或者对树节点用 click(dblClick: true) 真实双击(合成 MouseEvent dispatch
   无效,框架不吃)。
3. 等连接器附着(编辑器 boot + 连接器握手要 15~30s):
   until easyeda daemon health | grep -q connectorVersion; do sleep 3; done
4. 附着后 context.documentType 是 "home"/"blank" —— 还要
   easyeda doc switch PCB1 --project <name> 切到目标文档。
```

**多页/切页防误操作用 `--doc <uuid|name>`(机制,别靠人工轮询)**:所有命令默认对
**当前前台文档**操作,而 `doc switch` 是异步的——长命令(autoLayout ~2min)或跨命令
时前台会漂移,编辑就落到**错误的页**(2026-07-20 P1/P2 反复被打散的祸根)。**修法是
机制不是记忆**:给任意变更命令加 `--doc <目标页 uuid 或名>`,分发咽喉点(`ensureActiveDoc`)
会在**变更动作落地前**切到该页并用**实时 `document.current`** 确认(不看缓存 /health),
确认不了就**拒绝**而不是编辑错页。例:`easyeda sch block-apply <blk> --doc P1 --project ceshi`
——前台就算停在 P2 也会自动切 P1 落子、P2 不受影响。多页工程/长操作**一律带 --doc**。

前提(一次性,人工):该 profile 里已装过连接器 —— **侧载** GitHub Release 的
`.eext`(与 CLI 严格同版)**或**从[立创官方插件市场](https://jlc-ext.com/item/zhoushoujian/easyeda-agent-connector)
一键装(平台可原地自动更新,但市场版本可能滞后 CLI);并开了 **允许外部交互**、
登录过嘉立创账号。之后每次自举都无人工步骤。

### 两个必踩的启动坑(2026-08-04 实测)

1. **`browser is already running for …/chrome-profile`** —— chrome-devtools MCP 的
   profile 被上次留下的孤儿实例占着(带 `--enable-automation` + `about:blank`,
   **不是**用户的主 Chrome,后者用默认 profile 不带 `--user-data-dir`)。修法:
   `pkill -f "chrome-devtools-mcp/chrome-profile"`,再调 `list_pages` 让 MCP 重开。

2. **页面显示「登录/注册」但账号数据其实还在 → 点一下「登录」即恢复,不用真的重登。**
   全新启动时页面可能先渲染成未登录态(`localStorage.isLogin` 里仍有
   `{username,uuid}`,IndexedDB 里 `User_<uuid>_v6` 扩展库也在)。**此时连接器不加载**
   ——扩展库按账号分,未登录就不挂载,`health` 的 windows 一直空。
   点击顶栏「登录」链接会触发 session 恢复,顶栏随即显示用户名,连接器几秒内附着并
   在页面上弹 `Connected to easyeda-agent (port …)`。
   判据:`evaluate_script` 读 `localStorage.getItem('isLogin')` —— 有 uuid 就说明只是
   渲染态没跟上,**别急着让用户重新扫码登录**。
   (开源工程未登录也能打开,所以「工程打开成功」不代表连接器会附着,别用它当判据。)

3. **daemon 重启后连接器不会自己回来,而且 `reload` 救不了 —— 必须关掉 tab 重开。**
   实测(2026-08-04):daemon 停掉再起来后,连接器持续扫端口但**永远连不上**,
   `navigate reload` 等 50s 无效;**关掉该 tab、`new_page` 开 `#id=` 直达页,5 秒就连上**。

   根因:`transport.ts` 的 `WS_ID = 'easyeda-agent'` 是**固定常量**,而
   `eda.sys_WebSocket.register()` 在同 id 连接仍被 EasyEDA 视为 "active" 时
   **静默忽略新 url/callback**(该坑代码里早有注释)。daemon 消失留下的半关连接
   把这个 id 卡住,而这个注册表活得比页面 reload 更久。**同族于**桌面版那条
   「re-import 不 reload 已开窗口、必须完全退出 EasyEDA」。

   **诊断三件套**(照顺序做,一次分清 daemon / 网络 / 连接器谁的问题):
   ```
   curl -s http://127.0.0.1:60832/health                      # ① daemon 活着?
   curl -i -N -H "Connection: Upgrade" -H "Upgrade: websocket" \
        -H "Sec-WebSocket-Version: 13" \
        -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
        http://127.0.0.1:60832/eda                            # ② WS 端点通? 期望 101
   # ③ 页面里 evaluate: new WebSocket('ws://127.0.0.1:60832/eda') 能 open?
   ```
   三步全绿却仍 `windows: 0` ⇒ **就是 WS_ID 卡住**,别再 reload,直接关 tab 重开。
   特征信号:console 里满屏 `WebSocket is closed before the connection is established`,
   **含本该成功的那个端口**;而 `ERR_CONNECTION_REFUSED` 只是扫到空端口的正常噪音,
   别被它带偏方向。

## 2. 热重载连接器(改了 extension/ 之后)

不卸载、不重导入、不弹文件对话框——直接覆写 IndexedDB 里的执行文件。
详细原理见仓库 `docs/dev-environment.md` §5;要点:

```
1. make eext                        # 产出 extension/dist/index.js(19 万字节级)
   ⚠️ 必须 `make eext`,**不能只 `npm run compile`**:后者不 bump extension.json 的
   version,IndexedDB 里字节确实换成了新的(读回验证 bundle 含新代码),但编辑器
   照旧加载旧扩展——现象是新 action 一直 UNKNOWN_ACTION、health 版本号也不变。
   version 变了才触发重新加载。
   ⚠️ 另:新增 typed action 时 **daemon 也要重启**(它有自己的 action catalog)。
   `UNKNOWN_ACTION` 的 detail 若是 "run `easyeda actions` for the supported set",
   那是 **daemon 拦的**,跟连接器无关——别再去折腾热重载。
2. 起本地 WS 文件服务器(编辑器是 HTTPS,fetch http://127.0.0.1 被
   mixed-content 拦,ws://127.0.0.1 放行——连接器本身就靠它):
   一个 ~30 行 node 脚本,收 {action:"getFile"} 回 {content:<base64>}。
3. evaluate_script 在编辑器页里执行:
   - DB = User_<teamUuid>_v6(teamUuid 从 easyeda project info 读)
   - store extensionsObjectStorage,key = <extensionUuid>|dist/index.js,
     把 record.source 换成 **new File([bytes],'index.js',{type:'text/javascript'})**
     ——**MIME 必须带**(0.11.4→0.12.1 实踩:空 type 的 File 扩展 loader 静默不执行,
     对照原生记录 README.md 是 text/markdown 才定位到)
   - store extensionsIndex,key = <extensionUuid>:
     ① config.version 改新版本号(isAllowExternalInteractions 别动,权限就是这个布尔)
     ② **顶层 fileSize 字段同步成新 blob 的字节数**(0.12.1 实踩:index 记录顶层有
     fileSize(旧值),与 ObjectStorage 里新 File.size 不一致时 boot 校验静默不加载
     ——两处都要写,漏 fileSize 就白灌)
   - 两个 store 都是 **in-line key**:put(record) 不带 key 参数(带了报 DataError)
4. navigate reload 页面(#id= 还在,工程随 boot 重开)。若 reload 后 health 仍空但
   页面 window._EXTAPI_SCRIPT_SPACES_ 里有本扩展 uuid = 代码已在跑、只是 WS 尚在
   端口扫描,再等 ≤60s;裸 /editor(无 #id)会反复报 "Get an illegal project!" 卡 boot
   ——直接开 #id=<uuid> 直达页
5. until … grep connectorVersion → 应显示新版本(版本号编译在 bundle 里,
   变了就是新代码在跑的铁证)
```

extensionUuid 在 `extension/extension.json`。IndexedDB 结构非官方稳定 API
(今天 `_v6`),schema 升版要重核对 store 名。

## 3. 已踩过的坑

### 3.1 旧连接器运行时残留，导致重复注册

扩展管理器里只剩一个 `.eext`，不代表已经打开的 EasyEDA 页面只运行一个
连接器。重新导入、更新扩展或重启 daemon 后，旧页面实例仍可能继续注册，
表现为 `health` 中同一工程/文档出现多个 `windowId`、多个
`connectorVersion`，以及写动作超时。详细的症状、根因、恢复和验证清单见
[`docs/connector-runtime-recovery.md`](../../../docs/connector-runtime-recovery.md)。

快速修复顺序固定为：停止 Apply → 卸载旧扩展并只保留一个安装项 → **完全
退出并重启 EasyEDA** → 必要时 `easyeda daemon start` → 重新打开工程 →
`easyeda health --project <project>` 确认只有一个目标窗口后再写。不要只
reload 同一个 tab，也不要清空站点数据；后者会把 IndexedDB 中的扩展安装记录
一起删掉。

- **chrome-devtools MCP 多实例抢 profile**:多个会话/IDE(Claude Code、
  VSCode、opencode…)各起一个 chrome-devtools-mcp,全都用同一个
  `~/.cache/chrome-devtools-mcp/chrome-profile`,同一时刻只有一个 Chrome 能
  持有 → 其余实例所有调用报 "The browser is already running"。**修法**:
  `pkill -f "user-data-dir=.../chrome-devtools-mcp/chrome-profile"` 杀掉占
  profile 的孤儿 Chrome,紧接着发一个工具调用让**本会话**实例重启拿回句柄。
  profile 持久:登录态、EasyEDA 扩展、IndexedDB 全保留,重启零损失。
  多人/多会话同时驱动同一 profile 没有仲裁机制——**约定串行使用**,并发必冲突。
- **编辑后同网大面积「断连」**:对布线/填充做手术式增删后,DRC 可能突然报一串
  同网(常见 GND)Connection Error——这是**铺铜介导的连通性失效**,不是真断。
  `easyeda pcb pour-rebuild` 重灌后复测即恢复(ceshi 实测 11→1)。
  via-hop / via-delete / track-delete / fill delete 之后,若 DRC 报同网断连,
  先 pour-rebuild 再判断。
- **后台窗口 DRC 永不完成**:见 `pcb.md` DRC 条目(入口)——切前台单发,daemon 已防
  重入(`ACTION_BUSY`)。
- **判连接器健不健康,看 `easyeda health` 的 `writeHealth`——但要按新口径读**
  (2026-08-19 修订)。它统计的是**写的效果**,不是调用的返回码:
  - `failureRate` 里已经含「返回 ok 但回读证明没落地」的那一类(`fakeSuccesses`);
    首版只数返回码,那场端到端里全程 0.05/绿灯,而画布上大面积的写没生效。
  - `fakeFailures` 是反向那一类(报失败但其实已落地)——**不**计入失败率,
    因为处置动作相反:看到它就绝不能重发(重发造重复旗)。
  - `verified` = 有回读证据的样本数。**`verified` 低 + `failureRate` 绿 = 没人核对过**,
    不能读成"一切正常"。
  - `degradedActions[]` / `actions{}` 是逐 action 分桶:某条路(如 `connect_pin`
    一批 40% 失败)在混合流量里不会再被均值稀释,会被直接点名。
  - **你自己参数写错不算连接器的账**(2026-08-26 修订):`PRECONDITION_REFUSED` /
    `MISSING_PAYLOAD_FIELD` / `UNKNOWN_ACTION` 这三类「请求本身讲不通、画布零变更」
    的失败**完全不进采样**(连分母都不占)。此前它们照常计失败,于是「网名连打错三次」
    就能把连接器染成 DEGRADED —— 而真停摆(socket 死了、register 被忽略)混在同一个
    `failureRate` 里被淹掉。**`INVALID_STATE` 仍然计入**:它可能是编辑器状态真坏了,
    豁免必须窄。**读法**:看到 `degraded` 就是真的有条路不通,不用再猜是不是自己参数写错。
  `degraded:true` 时的正确动作:插一次轻读 + 短暂 settle;**写**失败先轻读复核
  「是不是其实已经落地」再决定,持续不恢复就 `easyeda doc reload`。
  daemon 只自动重发幂等导航动作(`document.open` / `schematic.page.open`),
  内容写永不 daemon 级重发。
- **用户说「画面没更新」**:web 编辑器前台窗口对所有编辑类型**即时重绘**
  (2026-07-07 sha 比对实测:track/挪件/丝印/pour-rebuild 全即时,tab 切回也
  即时)——画面旧只发生在桌面客户端、OS 级最小化/遮挡恢复、或铺铜 reflow 几何
  过期(数据旧非画布旧)。**确定性修法一条:`easyeda doc reload`**(save→
  close→reopen,不丢工作);轻量替代:让用户点一下画布/缩放,或 agent 跑
  `easyeda view fit`(前台有效)。长任务时配合 `easyeda notify` 每阶段通知,
  用户就知道何时该看/刷。
- **headless 环境(CI / ClawFlow operator)不能做运行时验收**:没有编辑器就
  没有 DRC/check 的运行时产物;正确行为是失败并说明,绝不伪造通过。
- **Windows / PowerShell 5.1 会吞 JSON 参数的双引号**(issue #133 Bug 5):
  JSON 对象/数组参数(`--patch`、`--spec` 等)经 PowerShell 原生传参到 exe 时
  内层双引号被吃掉 → JSON 解析失败。(`--ids` 已不受此坑影响:它现在只吃 CSV,
  `--ids a,b` 无引号问题;JSON 数组形式已移除。)**修法任选**:① 用 `--%` 停止
  解析符:`easyeda --% sch modify --id x --patch {"x":100}`;② 反引号转义 `` `" ``;
  ③ 换 cmd 或 PowerShell 7+(行为已修正)。Windows 中文环境另注意:调用 CLI 的
  外层脚本读输出必须显式 `encoding='utf-8'`(CLI 输出恒 UTF-8,系统默认 GBK
  会解码崩溃,#133 Bug 4,skill 自带脚本已修)。

## 4. 一次完整自举的实测时间线(2026-07-07,ceshi)

health(no windows)→ new_page #id 直达 → 25s 附着(0.8.4)→ make eext →
WS 服务器 + IndexedDB 覆写(199105 字节,0.8.4→0.8.9)→ reload → 30s 附着
0.8.9 → doc switch PCB1 → via-hop / via-delete / drc --json 全部真机验证 →
pour-rebuild 还原 DRC 基线。全程无人工。
