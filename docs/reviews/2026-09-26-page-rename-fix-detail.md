# 页面改名失败传播修复详细记录

[简版结论](2026-09-26-page-rename-fix.md)。本轮证据为
`artifacts/cli-page-rename-fix-20260926/`，不覆盖 dev.7 B04 的原失败资料。

## 原失败

目标项目 `69fbbab3b80c4725be733af19d8f79ef`、页 `d8543fbb3f6a4d02`。
请求名称 `CLI_BASIC_DEV177`。dev.7 `04-rename.json` 外层 ok=true、CLI exit 0，
内部 ok=false/verified=false；save/reload 与约 90 秒后 fresh pages/doc ls 仍为 P2。
执行器最后的名称断言发现失败，B05 只有 3 次读取，没有 place。
调用记录器随后补齐 domain ok/verified 检查；审计 B00–B03 无相同内部 false 遗漏。

独立 B04 fail 报告 SHA-256：
`e49091a5e626d5d59f7c1f53dc9b2e4a4ffbfa3e3136842f16de601fe08a00fb`。
原3个真实源码反例为：false+旧名不抛、false+已同名误签 verified、true+读取失败被说成缓存。
详见 `review/rename-initial-independent-review.md`，SHA-256：
`52b77e630d2a2d323cda666b94b8030893cdf691fcf25790f76c57f8af557fa2`。

## 宿主只读诊断

官方 SDK 的双字符串签名与实际 wrapper 相符，内部 RPC 是 `{uuid,value}`，不能把内部
形状当作 CLI 改参依据。缓存官方 ui.js 的校验为 trim 后 1–128 字符，当前 16 字符合法。
旧 dev.21 成功例的 handler 与 CLI 签名相同，不能据旧结果补签此次失败。
当前文档 UUID/tab 一致、readonly=false、事务 idle；页名仍 P2。

官方实现先从 getLeftTreeContentData 找 UUID，再转到 leftTreeUpdataName；false 有多条分支，
原失败时没有逐分支日志。只读快照观察到 `SCH.D.leftTree.contentData()` 缺失，
manager.event.contentData 有当前项目的 7 个节点且包含 P2；这只是后续诊断，不能证明原时刻
是哪一分支。没有调用私有写入、render、展开项目树或修改其状态。
诊断回包 01–05 与官方源码摘录保留，不能代替 typed 工程验收。

## 候选行为

- 一次官方 mutation，不重试。false、非布尔成功或异常均失败，附 hostAccepted、请求 UUID/名称、
  fresh 观察状态。读取新名也不覆盖官方 false 的事实。
- 官方 true 后有限重读验证；页缺失、重复身份、读取错误/超时立即失败，仍旧名到期也失败。
- 单次证据读取采用 2 秒绝对期限与后台 deadline sweep，迟到结果不继续调用。
  未 settle 的 mutation 仍受 transport ActionQueue guard 约束；不自造提前释放写队列的超时。
- CLI 对 page.rename 要求 result.ok=true、verified=true、非 partial，保留原始 stdout 并返回非零。
- Skill 删除无根据的缓存归因与“触发任意写操作”建议。

独立复核发现初版候选证据读可无限 pending，已保留反例并补有界读取；其通过不签现场宿主恢复。
最终源码、包身份、测试和现场对照如下。

## 最终验证与安装身份

连接器全量 611/611、针对性 TS 12/12、Go 全量、typecheck、构建、本地包验证与
Skill 检查通过。独立候选复核另执行 11 个 VM 场景、12 项 TS、7 项 Go 子例，
覆盖宿主 false、假同名、未知结果、缺失/重复页面、读取失败和无限 pending。
候选报告 `review/rename-candidate-independent-review.md` SHA-256：
`b9227256a36a93318de55b898795c187282ea1474f89c90c6a31aab52c9092e5`。

dev.8 本地包目录 `dist/local-v1.7.1-dev.8/`；安装 CLI 与包内 darwin_arm64 字节相同，
SHA-256 `1ca46b9ffc2b2d0f235f06e0966e6b0c96961ab5667daccdd820ff69a1628092`。
构建 bundle、eext 内 bundle 与安装回包相同，797577 字节，SHA-256
`a33c220975e43489c2ced2e9d69f1c2ba42024c92bbf1e630b59a9e0f483a891`。
09 使用仓库授权的受限开发热更新脚本，验证旧 dev.7 哈希、已有 UUID/权限，再事务更新与读回，
未扩大权限；升级前显式保存，升级后完整 result/context 与升级前全等。
10 health 确认目标 `81d1f3ef-9113-49a4-a7fb-e6d76858e510` 的三方版本 dev.8、
宿主 4.1.60、目标工程/文档/tab。另一个 dev.1 空文档窗口不计入目标验证。

## 单次现场对照

18-dev8-rename-once.command.json 只执行一次正式 page-rename。CLI exit 1、外层 ok=false、
EDA_CALL_FAILED；detail 保留 hostAccepted=false、verified=false、准确 pageUuid/requestedName，
以及 observation different/name=P2。19 fresh pages 仍为 P2。
独立审计 16–19 期间 7 条 action 中只有 1 次 rename，没有重试或私有写入。
这验证失败传播已修复，不能签宿主实际改名通过。

安装与对照独立报告 `review/dev8-install-contrast-independent-review.md` SHA-256：
`eb1023b4684bd78f6459edc5973c4035164f95fb3c163ad420ca04cec1ae29ed`。

## 清理、恢复与最终状态

20–28 先读取并打开保留 P1，再 typed 精确删除测试 P2、save、真实 reload、fresh 回读。
28 持久化页面列表仅剩 P1。最初完整 result 全等断言因页数变化失败，保留失败记录；
独立逐字段比较确认只有图框两处 @Page Count 与其对应属性 Value 从 2 变 1，
其余全部字段严格相等、context 全等。未掩盖预期差分，也未将整页误记为全等。

29 project.open 因缺少显式 --allow-discard-unsaved 在 CLI 写前拒绝，该期间 audit 为 0。
29b 基于已保存重载证据携带标志，实际只执行一次 project.open。30 原授权工程
`0f46d4361c4741a9ab7a9ed62164ca9b` 的 SCH result/context 与 dev.7 B00 基线全等；
32 PCB 全 JSON 仅 capturedAt 改变，semanticSha256 仍为
`41b23a1b3e7c54f623efa678dea430033717968444fefee90e1ccbfebce0d527`。
33 回原 P1 `e453c0063919726b`，34 health 核对 dev.8 与该工程/文档身份。
20–34 共 83 条 action 均在目标窗口；保留 reload 过渡时一条 document.current /
No active document 记录，后续真实重载与回读完成，不隐藏该记录。

新个人容器 `69fbbab3b80c4725be733af19d8f79ef` 保留默认 P1/PCB/Panel；
无 typed 工程删除接口，不能称整个容器已删除。测试 P2 已消失，没有测试器件写入。
独立清理报告 `review/dev8-cleanup-independent-review.md` SHA-256：
`962e5553d2388d402fa4f3ff2333e8c0e2c48ff9b041975fddf333b930c895c6`。
对应输入清单 SHA-256：
`5ec3cc9403b2a69784462ad82e5a60f1d2507ee0a34a76b0c5afeb043211eaa6`。

冻结 `evidence-manifest.json` 绑定 100 个文件，独立核验 100/100 一致，SHA-256：
`de40fea43810b8c857fc90c9fd1286f02c7d9cb413c9ab5f69a07f5b2a6a7120`。
持续 daemon/hot-server 日志排除在冻结清单外；清理独立报告在冻结后新增，未改原文件。

最终仅签失败传播修复与清理恢复。dev.7 B04 仍 fail，dev.8 未重跑基础全集，
A00 blocked、A01–A06 not-run；根因尚未查明，不用私有工程写入或 GUI 绕过。

## 用户重新打开页面后的续测

新增证据 `artifacts/cli-resume-dev178-20260926/`；源码仍为 `f73b071`、运行包仍为 dev.8。
00 health 的原目标窗口 `81d1f3ef-9113-49a4-a7fb-e6d76858e510` 自 11:06 UTC 保持连接；
它与原授权工程和 P1 一致。03 完整 result/context 与前轮 30-original-sch 全等。

05 在原工程父原理图 `8e23e98d3613f6cd` 新建临时页 `2d7ae65a72f977d8`，
06/08 验证页身份和仅图框的空白状态。09 只执行一次改名为 CLI_RESUME_DEV178，
exit 1、EDA_CALL_FAILED、hostAccepted=false；10 fresh pages 仍为 P2。
没有重复改名、无关写入或用私有工程接口兜底。

只读排查收窄了候选：官方 ui.js 从 getLeftTreeContentData 的结果计算页索引；
sch-main.js 的 getter 返回 manager.event.contentData，实际改名 Qxe 却取
bi.contentData[index]。两份数组通过 renderToTree 同步，后者还可能被过滤。
02/12 看到 event 有目标而 rendered 不可用；这支持数据来源不一致的候选，
尚未证明原调用当刻的具体分支或消息总线如何把异常变为 false。

另发现可见页绑定问题：只读浏览器 tab 1 的 fresh URL/标题保持原 P1；
12/13 连接器诊断的 top window 则有 P1/PCB/P2 三个 tab、活动页 P2，且 sameTop=true。
因此本批只能签原连接窗口上的失败与恢复，不能声称已验证用户刚打开的可见页面。
可见页日志有 Get an illegal project 等错误，但未与该连接器调用建立时间/窗口关联，
不能据此推断改名根因。没有浏览器点击、导航、刷新或工程编辑；界面观察未替代 typed 回读。

07 doc open 的默认输出是文本，后处理最初按 JSON 解析失败；命令本身 exit 0，未重跑，
08 完整 list 确认身份。11 只读诊断误用不存在的 getAllOpenedDocuments 方法失败，
12 删除该调用后读取身份；没有工程 mutation。两项均保留原始证据与说明。

14–20 显式保存临时页、打开原 P1、精确删除本轮 P2、save、真实 reload、fresh list/pages。
19 的完整 result/context 与 03 全等，20 页面清单与 01 全等；本轮未修改 PCB。
冻结清单共 24 个文件，SHA-256：
`110f3e47d21bb4d5a7d7048671598f6bbca879fac257ba27a54dfa8aaa07ac28`。
可见浏览器记录是工具输出摘要，明确标注未冒充原始 console 导出。
清理后停止 EDA 调用，等待用户在当前可见页启动连接器并允许外部交互，届时重新绑定窗口。

独立复核 `review/resume-dev8-independent-review.md` 确认 24/24 冻结文件大小和哈希一致，
65 条审计均为原目标窗口，create/rename/delete 各一次。保留 rename、11 诊断和 reload
过渡的 No active document 失败，不补签 B04。报告 SHA-256：
`6982141224299cbe6ec5f13659a2008ec9a576442e2bbdf2ad2683babf5313a7`；输入清单：
`6429f4e85fc1cff0a8573cb0322c20b3707c094429bb59e276de25b7027872ab`。
冻结后新增 21-health-after-cleanup 仍只有原连接与无关旧版本窗口，不纳入此前清单和复核范围。
本轮仅更新报告及 Skill 的当前可见页绑定提醒，`make skill-check` 与差分检查通过，未改变运行代码。
