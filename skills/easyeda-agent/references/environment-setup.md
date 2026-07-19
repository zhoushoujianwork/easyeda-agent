# 环境自举 — agent 自己把「可用的 EasyEDA 环境」拉起来

`NO_CONNECTOR` / `windows: []` 不是终点。有 chrome-devtools MCP **且用户用的是网页版**
时,agent 可以**自己**完成:开 web 编辑器 → 打开目标工程 → 确认连接器附着 →
(需要时)热重载新连接器。全流程 2026-07-07 在 ceshi 工程真机跑通。没有浏览器控制
工具、**或用户用的是桌面客户端**时,才退回「请用户手工打开 / 切换 EasyEDA 工程」。

## 桌面版 vs 网页版 —— 自动开工程能力的边界(先判这一条)

**连接器本身对两者一视同仁**:`.eext` 装进 EasyEDA(桌面或网页都行),后台端口扫描
`49620-49629` 附着 daemon,附着后所有 `easyeda` typed action **完全一样**。区别只在
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

前提(一次性,人工):该 profile 里已装过连接器 —— **侧载** GitHub Release 的
`.eext`(与 CLI 严格同版)**或**从[立创官方插件市场](https://jlc-ext.com/item/zhoushoujian/easyeda-agent-connector)
一键装(平台可原地自动更新,但市场版本可能滞后 CLI);并开了 **允许外部交互**、
登录过嘉立创账号。之后每次自举都无人工步骤。

## 2. 热重载连接器(改了 extension/ 之后)

不卸载、不重导入、不弹文件对话框——直接覆写 IndexedDB 里的执行文件。
详细原理见仓库 `docs/dev-environment.md` §5;要点:

```
1. make eext                        # 产出 extension/dist/index.js(19 万字节级)
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
- **后台窗口 DRC 永不完成**:见 `pcb.md` DRC 条目——切前台单发,daemon 已防
  重入(`ACTION_BUSY`)。
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
  `easyeda sch prim-delete --ids '["abc"]'` 经 PowerShell 原生传参到 exe 变成
  `[abc]` → JSON 解析失败。所有吃 JSON 数组/对象的参数(`--ids`、`--spec` 等)
  同理。**修法任选**:① 用 `--%` 停止解析符:`easyeda --% sch prim-delete --ids ["a","b"]`;
  ② 反引号转义 `` `" ``;③ 改用 CSV 形式(接受 CSV 的参数如 `--ids a,b` 优先);
  ④ 换 cmd 或 PowerShell 7+(行为已修正)。Windows 中文环境另注意:调用 CLI 的
  外层脚本读输出必须显式 `encoding='utf-8'`(CLI 输出恒 UTF-8,系统默认 GBK
  会解码崩溃,#133 Bug 4,skill 自带脚本已修)。

## 4. 一次完整自举的实测时间线(2026-07-07,ceshi)

health(no windows)→ new_page #id 直达 → 25s 附着(0.8.4)→ make eext →
WS 服务器 + IndexedDB 覆写(199105 字节,0.8.4→0.8.9)→ reload → 30s 附着
0.8.9 → doc switch PCB1 → via-hop / via-delete / drc --json 全部真机验证 →
pour-rebuild 还原 DRC 基线。全程无人工。
