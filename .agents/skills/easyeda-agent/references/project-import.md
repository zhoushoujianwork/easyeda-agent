# 工程创建、打开、导出与迁移

按任务读取创建与定位、原生导出或外部工程迁移；这些能力分别验证。

## 新建工程与身份确认

从首页调用 MCP `project.create` 时，先通过 `easyeda_health` 选择真实窗口，把新名称放在
`payload.friendlyName`，可用 `payload.open` 请求打开。此动作只创建工程容器，必须提供
`window`，不要传 `project` 或 `doc`；拟建名称不是已有工程，首页标签不是原理图页面。
创建后检查 `created` / `opened` / `partial` 并读回工程身份，再处理文档创建。部分成功时
先用 `easyeda project find --window <id> --name <完整友好名称> --team <teamUuid>`
按友好名称和团队精确查找，不盲目重复创建。它只调用官方项目 UUID 枚举与逐项详情读取；
查找默认等待 90 秒，可显式加 `--timeout 120s`（范围 5 秒到 10 分钟）。超时仍是未知，
不能据此创建同名工程；先保存其他打开文档，用户要求时 typed `web reload` 后再有界重试。
`found` 可用于核对已有工程，`unknown`（例如 UUID 清单为空、详情缺失或枚举报错）不能当作
不存在；只有 `enumeration.complete:true` 且 `presence:"absent"` 才能说明指定团队**根文件夹**内
没有匹配（SDK 不保证递归子文件夹）。本例未传 `folderUuid`，故先查目标团队根文件夹。
结果仍需核对 UUID 和团队，不因同名自动打开或重试创建。无 `--team` 的查找仅
用于发现匹配，不证明全局不存在。返回 `UNKNOWN_ACTION` 时检查连接器是否实现此动作；健康检查的
版本兼容不能证明 handler 存在。其他 MCP 写操作仍要求真实 `project` 和 `doc`；不得推广此例外。

**创建目标要区分个人空间和团队。** 创建个人工程省略 `--team`；`project info` 中可用于
`find --team` 枚举的归属 UUID，不能直接假定也可传给 `create --team`。Web V4.1.60 现场
同名、同参数对照中，显式传个人空间的归属 UUID 立即返回无 UUID；完整回查确认未创建后，
只省略该参数则创建成功，并回查到原归属 UUID。团队工程仍须明确目标团队，失败时不能自动
改为个人工程。遇无 UUID 先完整回查，再根据已确定的目标空间修正参数；不能盲目重试或改归属。

工程级跨项目打开及原生 `.epro2` 导出见 [工程操作](#工程级打开与原生导出)；页面打开不替代工程切换。

## 文档重载与只读就绪核查

`easyeda doc reload <uuid> --project <project> --json` 显式保存、确认关页后只重开一次。
重开使用 30 秒连接器预算（另留响应宽限），成功后在 10 秒内核对活动工程／文档／类型／tab
以及连续稳定的完整组件清单。保存须返回布尔 `saved:true`；关闭、重开或对象读取失败均停止，
不自动重发 open，不用 GUI 刷新兜底。

JSON 回执保留 `saved/reloaded/documentType/activeRestored`，并记录目标、阶段请求 ID、CLI
观察起止时间和 readiness。失败也输出已发生阶段，进程仍非零退出；`saved:true` 而重开失败
是 partial，不等于重载通过。重载非活动文档后，恢复旧活动页必须新读确认；恢复失败单独
报告，已完成的重载事实不丢失。主机观察时间与响应 `createdAt` 分别保留。

超时后可执行纯只读核查：

```bash
easyeda doc ready <expected-document-uuid> --project <project> --timeout 10s --json
```

它不保存、关闭、打开或切页；指定目标必须已活动，省略 UUID 则检查当前页。每次组件读取前后
核对同一上下文，按 `primitiveId` 排序完整组件记录计算指纹；非空清单须连续两次相同，空页
须三次。相同数量而内容不同不算稳定，缺清单、缺身份、重复 ID、上下文漂移或超时均拒绝。
显式 `partial`／`incomplete`、非空 `captureErrors`、不可用或错型的采集状态同样拒绝；
PCB 未请求的焊盘／包络可保留 `not-requested`，该命令不扩大为几何验收。
`--timeout` 为选定窗口后的总读取预算，范围 1 秒至 1 分钟；不会等待队列阻塞自动重试。

`ready:true` 只证明当前组件清单稳定可读，**不证明先前超时 handler 已结束，也不替代 reload
成功、完整网络／导线／几何回读或持久化验收**。保留原失败及保存、关闭、关闭后目标离开活动态
的证据；另作完整新回读并与已保存目标对账。恢复后的新验收周期只执行一次显式 save/reload，
新周期仍失败就停止，不循环重开；不得把原失败改写成成功。

## Altium Designer 工程：当前 `unsupported`

当前没有经过验证的 typed action 或 CLI 能把 `.SchDoc` / `.PcbDoc` 程序化导入
EasyEDA Pro。Agent 不得用交互界面、CUA、`debug.exec_js`、分块 base64 上传或私有消息总线
兜底。需要导入时停止现场写入，将能力标为 `unsupported`，先实现并验证 typed import。

如果目标工程已经由用户独立提供，easyeda-agent 只从现有工程开始读取与核验：

1. `easyeda health --project <project>` 与 `easyeda doc ls --project <project>`：确认连接器、
   目标工程，以及导入所得的原理图/PCB 文档。
2. 对每张原理图读取 `easyeda sch connectivity --page <name-or-uuid> --project <project>`，
   核对器件身份、全部物理引脚、网络、NC、分层页面与位号；再逐页运行适用的
   `layout-lint`、`sch check`、`bridge-check` 和 SDK DRC，分别保存结果。
3. 对 PCB 核对 Board 绑定、器件与焊盘网络、板框、层叠、机械层/禁布区、关键网和
   丝印；运行 `easyeda pcb layout-lint`、`easyeda pcb check --strict` 与
   `easyeda pcb drc --strict`。AD 机械层到 EasyEDA
   层的映射必须在原生画布和数据回读中确认，不能只看工程已出现在目录里。
4. 记录导入前后的数量与关键不变量。缺页、空读、板框/机械层变化、网络不一致或检查
   未运行时，均不能报告迁移成功；修复完成后显式保存。

## 为什么不能调用名义上的导入 API

官方 beta 方法 `eda.sys_FileManager.importProjectByProjectFile` 的签名列出了
`'Altium Designer'` / `'Protel'` 类型，但在 issue #203 的 EasyEDA Pro 桌面端
3.2.149（Windows x64，本地工作区）实测中，对 `.epro2`、`.eprj2` 和真实 `.SchDoc`
都会快速 resolve 为 `undefined`，不抛错，也没有创建文档或改变工程。

因此：

- 方法存在、Promise resolve 或返回 `undefined` 均不算成功；不得继续后续自动化。
- `getAllProjectsUuid()` 在本地工作区可能返回 `[]`，不能单独作为导入副作用判据。
- `eda.sys_FormatConversion.convertAltiumDesignerLibrariesToEasyEDA*` 只转换
  `.SchLib` / `.PcbLib` 库文件，不转换 `.SchDoc` / `.PcbDoc` 工程文档。
- 当前不封装 `import.ad` typed action；原生归档导入见下文 `project import`，状态 `planned`。将来只有在受支持宿主上证明真实副作用，并能在
  返回值缺失、超时和部分导入时明确失败，才可进入 CLI/action 实现。

未来接口封装至少要在写入前记录目标工程指纹，并在返回后核对工程/文档身份、文档清单、
原理图连接数据及 PCB 板框/机械层；不得把 `undefined`、无变化或单一 UUID 清单当成功。

来源与完整探测矩阵见 [GitHub issue #203](https://github.com/zhoushoujianwork/easyeda-agent/issues/203)。


## 工程级打开与原生导出

`project open --uuid` 是旧的文档打开别名，只能用于当前工程内页面，不是跨工程打开。
跨工程时先保存所有未保存文档，再显式调用：

```bash
easyeda project open --window <window-id> --project-uuid <project-uuid> --page-uuid <page-uuid> --allow-discard-unsaved
easyeda project export --window <window-id> --project-uuid <project-uuid> --out ./deliverable.epro2
```

打开命令使用官方 `dmt_Project.openProject`；该 API 可能丢弃未保存数据，因此标志是明确确认，
不是自动保存。命令核对打开后的真实工程身份；超时或失败先读回，不盲目重复。
导出使用官方 `sys_FileManager.getProjectFile`，要求目标工程已激活，前后检查 UUID，
拒绝覆盖文件，校验原生 ZIP 完整性并输出字节数/SHA-256。输出 `restoreVerified=false`：
ZIP 校验和导出成功不代表重新导入验证通过。请在导出前显式保存所有文档。
底层调用正式 typed action `project.open` / `project.export`，需要注册这两个 action 的新版 daemon 及包含 handler 的新版连接器。旧 daemon 或连接器拒绝 unknown action / UNKNOWN_ACTION 时停止并升级；禁止回退到 debug.exec_js。
直接调用 `project.open` 同样必须传 `allowDiscardUnsaved:true`；`project.export` 不接受页面路由。
限制：归档最大 16 MiB，解压验证上限 128 MiB，超限明确失败。

MCP 使用 `easyeda_project_transfer`，`operation` 为 `open` 或 `export`，均需 `window`、
`projectUuid`；打开另需 `allowDiscardUnsaved=true`，导出另需新 `out` 路径。不传文档路由。
此工具需要含上述 CLI 命令的匹配构建；不要仅替换 MCP 而仍使用旧 CLI。

工程身份可能先于文档树就绪。需直接进入原理图时，打开命令同时传 `--page-uuid`（MCP `pageUuid`），等待目标页面出现在树中后只打开一次，并核对工程和页面身份。省略此参数只保证工程身份，不保证页面已加载。

## 原生归档导入

```bash
easyeda project import --window <window-id> --file ./design.epro2 --name <新工程友好名>
```

调用 `project.import`（官方 @beta `importProjectByProjectFile`，类型 `EasyEDA Pro`），只创建
**新工程**，不写入已有工程。CLI 先校验 ZIP/CRC（`.epro2` 须含 `.epru`），上限同导出；
团队默认取当前工程的团队，可用 `--team-uuid` 指定；`--extract-libraries` 才把内嵌库提取到团队库。

Web 4.1.71 现场结果（2026-10-13，连接器 1.10.5）：

| 输入 | 结果 |
|---|---|
| 编辑器导出的 `.epro2`（118 个有效文档） | 9 秒建成新工程；打开后再导出，逐文档记录（忽略 ticket）118/118 相同，`live-verified` |
| eprj3 文件夹打成 `.zip`（含或不含顶层目录） | 立即返回空，完整枚举确认未建工程：`unsupported` |
| eprj3 文档重新封装为 `project2.json` + 单个 `.epru` | 建成新工程，但宿主随即切到新工程并重载页面，调用以“连接器断开”结束 |

因此导入可能**切走当前窗口的活动工程**：先保存所有文档，且不要在别的会话正在使用的窗口里导入。
断开、空返回或超时都是**结果未知**（参考 [#203](https://github.com/zhoushoujianwork/easyeda-agent/issues/203)
桌面端空返回）：先 `project find --name <名称> --team <teamUuid>`，不得盲目重导入；结束后用
`project open` 切回原工程并核对。回执 `contentVerified:false`：新工程必须打开并回读原理图、PCB 与 DRC。

## 经授权清理测试工程

只有用户明确要求删除时才清理工程。先从创建回执建立 UUID 与名称清单，不只按名称前缀
选择；保留原报告，使用另一个已观察的参考工程承载调用，拒绝删除当前参考工程。
运行 `easyeda project delete --window <参考窗口> --project-uuid <目标UUID> --expected-name <精确名称> --dry-run`，
核对目标、团队与 API 支持；实际删除再加 `--expected-team <预检团队> --yes`。
预检与删除为不同的只读/写入动作，不能用 dry-run 绕过确认。官方方法 `dmt_Project.deleteProject`
在 SDK 中标为 internal，运行时缺失或读回不完整即停止，不用 GUI、debug.exec_js 或直接改库兜底。
方法存在只证明可调用，不保证宿主实现了删除。Web 4.1.78 现场预检可通过，但官方方法返回
`false`，真实重载后目标仍存在，当前该宿主的整工程删除未验证成功；不得标为 `live-verified`。
删除只发一次，要求 deleted:true、verified:true、partial:false；仍须真实重载后重新查工程清单。
关闭标签、删除本地最近记录或清空 Board 不等于删除云端工程；SDK 删除不等于已证明回收站永久清空。
共享 daemon 不支持新动作时，保留原实例，只能使用显式隔离验收服务及获准的连接器环境更新，
独立端口/状态目录并恢复配置，不能重启或替换共享 daemon。
