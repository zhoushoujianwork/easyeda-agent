# 原理图删除修复详细记录

用户可先读[简版结论](2026-09-26-sch-delete-fix.md)。本记录不替代高级验收。

## 旧版现场隔离

CLI/daemon/connector：`1.6.0-dev.21`；Web EDA：`4.1.60`。复用已授权测试工程，
P1 原始状态只有图框。参数来源为原始测量失败中的 C8：100 nF、C14663、旋转 0，
放置在纸张中心附近 `(585,410)`（原理图坐标）。位置用于固定对照，不是设计布局答案。

每轮按参数放置 → 指定查询 → 位号几何读取 → 按新 PID 删除 → 独立 fresh list 核对。
所有调用串行、显式绑定工程和页；首次最终删除失败即停止该组，不把恢复后的成功补签。

| 查询条件 | 已运行轮数 | 首次删除 partial | 400 ms 有界复核后仍失败 |
|---|---:|---:|---:|
| 原始完整测量（引脚、网络、身份、bbox、导线、页面图元） | 10 | 3 | 0 |
| 仅引脚与网络 | 2 | 2 | 1 |
| 仅 `sch netlist` 导出 | 2 | 1 | 1 |
| 仅跨页组件枚举 | 12 | 0 | 0 |
| 仅引脚，`includePinNets:false` | 12 | 0 | 0 |

两次最终失败均有独立对象回读确认，不是退出码或旧快照推断。冻结后 typed 保存、同页打开、
核对新鲜 ID、精确删除、保存重载并确认恢复图框基线。对照证明网表导出可以单独触发失败；
它不证明宿主内部具体原因，也不排除其他触发源。

原始命令、完整回包及结果位于本地忽略目录 `artifacts/cli-delete-fix-20260926/old/`；
`probe.py` 保存参数化流程，`cleanup.py` 保存独立恢复。历史失败不被候选回归覆盖。

## dev.1 候选与失败闭合（历史）

- 网表导出前捕获文档 UUID/tabId；导出成功或失败后均通过官方 `activateDocument`
  恢复原标签输入焦点。身份漂移、读取异常、激活失败均报错，不降为“无网络”。
  无重开、关闭、重载或新增删除重试；焦点恢复仍是待现场验证的候选。
- 删除回读采用 `deletedIds`、`survived`、`unverified`。异常、非数组、非法/重复 ID、
  页面漂移不证明消失；`partial:true/verified:false` 保留未知状态。每批删除前核对身份。
- 主器件未确认删除时不清理专属线树；级联和 disconnect 的未知结果不计入已删除。
  `prim-delete` 只按显式已删 ID 注销组成员，不对未知状态重试。
- CLI 输出合并后的最终结果并保留两次 `attempts`；第二次回包缺字段或覆盖不足仍失败。

## dev.1 离线检查与安装（历史）

| 检查 | 结果 |
|---|---|
| `make test` | Go 全量通过 |
| 删除最终 JSON、三态、有限复核及通用派发定向测试 | 42 项通过 |
| connector 全量测试 | 559 项通过 |
| 焦点恢复定向测试（增加读取/激活异常） | 8 项通过 |
| connector typecheck / build | 通过 |
| `make skill-check`、本地包校验、`git diff --check` | 通过 |
| 独立离线复核 | 支持进入现场复测，未签现场修复 |
| 候选删除现场复测 / 高级用例恢复 | blocked / not-run |

候选 `1.7.1-dev.1` 只在本机构建和安装，没有 tag、push 或发布。
连接器 bundle SHA-256：`81da73b6017bc618e60f68b3ffc185ea59e4c1ca4692e4b271d790d05d5fa4ec`。
通过仓库受保护热更新脚本核对旧版/新包哈希，原子更新同 UUID 的连接器记录，权限保持不变。
CLI、daemon、运行中连接器和已安装 Skill 的本地版本检查通过。

运行包切换前，已分别得到两个测试工程六份文档的 `saved:true`。原测试窗口未重连；
已更新的 Web 窗口通过 typed `project.open` 打开同一已授权工程，但调用在 58002 ms 后超时。
health 只给出目标工程，未给出 P1 文档；因此停止该窗口后续写入，不用 GUI 恢复，
不把版本一致当作文档可读。原始回包见 `setup/open-approved-project.command.json`。

独立报告在 `review/candidate-code-review.md`，SHA-256
`1856fd75a163822d1cdb00e0a9456bb6ec91553375c3a8418afa16581afb40f7`。
其中 synthetic 反例明确区分故障复现、候选边界检查和未执行的现场验收。

## dev.1 现场否定与同步事务对照

原目标随后重新连接；显式固定新窗口、确认图框基线后，第 1 轮单独网表导出/删除仍失败，
包括既有 400 ms 复核，fresh list 确认 C8 残留。焦点恢复不足以解决问题。约 95 秒后，
未重开或刷新文档，单次 typed 删除成功；因此历史“同页重开后成功”不证明重开是必要修法。

进一步只读探测发现失败删除前后宿主事务均为 `RealTimeSync/running/ignore`，文档相同且
非只读。另一轮按 200 ms 间隔观察：0.686 秒仍同步，0.956 秒空闲，然后单次删除成功，
fresh 回到原图框并保存。原记录分别在 `candidate-dev1/`、`transaction-probe/`、
`transaction-settle-duration/`；这些是故障定位，不是 dev.2 的通过证据。

运行页加载的官方前端文件为 `pro-ui/4.1.62.5e01457f/js/ui.js` 与
`pro-sch/4.1.54.bfdf9a4d/js/sch-main.js`（health 仍报告宿主 4.1.60）。只读源码分析确认：
官方 component delete → deleteObjs → actionRunner.run；已有事务且非 inherit 时直接返回 false。
网表导出会更新 Designator/Unique ID 并触发同步。本例 uniqueId 从空变为 gge1。
此机制有现场支持，但不宣称所有历史间歇失败均只有此因。

来源与哈希保存在 `api-research/` 和独立报告 `review/transaction-readiness-review.md`。
没有调用私有事务方法、改锁状态或通过 debug 写工程。

## dev.2–dev.4 最终修复范围

- 删除调用链的三态回读修复保留；撤回焦点恢复。
- 在网表导出前和相关删除入口冻结 UUID/tab、宿主 runner 与文档实例 token。
- 导出结束及 `component.delete`、`prim-delete`、`disconnect` 每批实际删除前等待
  `RealTimeSync` 结束，最多 5 秒。只读 adapter 验证宿主文档身份、实例连续性与可写状态。
- 其他事务、未知能力/状态、只读、实例更换、身份漂移或超时都失败；回调晚到不能继续写。
  deadline 同时采用 worker 扫描与显式到期比较。就绪错误不进入 CLI 的 survivor 复核。
- 官方 RPC 与就绪读取无法原子化；仍保留 fresh 三态和已有 confirmed-survivor 有界复核。
  其他 compound 旧清理分支尚未统一接入，不扩大覆盖结论。
- 使用内部只读兼容适配器是明确限制：未找到公开可靠的 transaction-ready API；
  宿主结构不匹配时返回不可用，工程写入仍仅经官方 API。

前一版安装时还发现 `web reload` 把预先存在的
无文档窗口误当新注册，连续探测 197 次后失败；这不是删除根因，后续删除回归显式固定
真正的新窗口。当时该独立缺陷未修复，原失败见 `review/web-reload-selection-review.md`；最终修复见下节。


## dev.3 删除现场回归

目标固定为测试工程 `0f46d4361c4741a9ab7a9ed62164ca9b` / P1 `e453c0063919726b`，
窗口 `e3795209-107a-45f1-8d3d-e17a7c91f936`。CLI/daemon/connector 均为 `1.7.1-dev.3`。
参数化脚本 `probe-ready.py` 遇到首次 partial、非零退出或残留即停止，不能靠重试签通过。

| 原始失败序列 | 轮数 | 首次 partial | 最终失败 / fresh 残留 |
|---|---:|---:|---:|
| 单独导出网表 → 位号几何 → 删除 → fresh | 20 | 0 | 0 / 0 |
| 引脚、网络、身份、bbox、导线、页面图元 → 位号几何 → 删除 → fresh | 20 | 0 | 0 / 0 |

独立复核逐条核对 200 条命令及 40 个唯一 PID，审计恰有 40 次 primitives.delete；
40 轮内没有 document.open、page_reload 或 debug.exec_js。最终 save → doc reload → fresh list
恢复原图框；全类别 `sch clear --dry-run --expect-empty` 为 remaining=0。以上是定向回归，
不是整板 E2E。原回包位于 `candidate-dev3/`，bundle SHA-256：
`95b5f02b685b292d31ced2692d299b9e8c7f00f92a69846e6cd63aedfc88e3b8`。

独立现场复核 `review/dev3-live-review.md` SHA-256：
`c28550d6d85fb91a2de3342772b89e6e572502de51e9eabf4c91558911fa7664`。
最终代码复核 `review/readiness-candidate-final-review.md` SHA-256：
`8c3d18b5949bd9114fce7574d5a83149b9ab4fac501d8f98a5140ba89c99115e`；
去除误加 PCB catch 的补充复核 `review/readiness-candidate-final-addendum.md` SHA-256：
`2e1d1c5214ae1cd015a13eb41147f2752b3e582fa540557963a7d6a924e0532b`。

## 最终包 dev.4 与 Web 刷新修复

Go 在刷新前冻结全部已连接 window IDs，只接受后来注册的同工程候选；优先探测已报告目标
文档的候选，避免已有空文档窗口抢占探测。仍须满足原有保存、稳定对象基线和最终文档核对。
多个同时新增的同工程同文档窗口仍缺少因果绑定，不能据此宣称任意多窗口重连均无歧义。

`TestWebReloadNeverUsesPreexistingSameProjectWindow` 断言既有同工程空窗口收到零次动作；
WebReload 14 项及 Go 全量通过。首次全量运行误把独立复核保存的 `.go` 源码快照当成包，
因此编译失败；给离线快照目录添加嵌套 go.mod 隔离后全量通过，原快照字节未改。
失败日志 `candidate4-go-tests.log` 与重跑日志 `candidate4-go-tests-recheck.log` 均保留。

现场仍保留同工程旧版无文档窗口 `fb05359f-ef05-4098-bed2-3cae5c26c40e`。
显式目标 `dcd72ee6-8e48-418b-859b-aa9f7a81793a` typed Web reload 后，新窗口
`db0fb7fd-68ea-4996-9d1c-cfaebcb8b822` 在 9638 ms 完成 stable-component-inventory。
记录在 `candidate4-setup/03-web-reload.command.json`。全局 health 的旧窗口版本告警保留；
目标窗口、CLI 与 daemon 精确 dev.4，不把全局状态写成全兼容。

最终 dev.4 bundle 仅版本字符串区别于 dev.3：
`1741f5551a2961362279eeb7f4f16db9f57b5d7974c63d9c45bf2817e274971b`。
最终包另跑 3 轮单独导出和 3 轮完整测量，全部首次删除成功、fresh 无残留；保存重载及
全类别空白核对记录在 `candidate-dev4/persistence/`。dev.3 的 40 轮不改签成 dev.4 的 40 轮。

连接器全量 579 项通过后，补一个私有 getter 异常负例，最终相关测试 257 项通过；不虚报
全量 580 项已重跑。typecheck/build、skill-check、agent-check、本地包校验通过。
离线检查不替代同包基础现场门禁；当前高级 A00 仍待最终包 B00–B10 全部通过后恢复。


## 采集脚本与成本记录

最终 dev.4 的 doc reload 正常返回平铺 JSON、exit 0；外围采集脚本随后误读 `result` 字段报
KeyError。保留原成功回包，随后仅继续 fresh 和 dry-run，没有重跑 reload 或改写旧证据。
安装检查曾误传 health 不支持的 `--json`，退出 1、没有现场动作；空输出保留，随后正常
`health` 的有效回包分别记录在 `health-after-restart-valid.json` 与 `health-final.json`。

本轮隔离、失败候选、调查、构建和回归的审计区间为 UTC 08:53:18–10:02:16：
墙钟 68.97 分钟，daemon 累积 6.68 分钟，差值 62.28 分钟。差值包含分析、编译和复核，
不是单独的模型思考实测。共 2273 次调用；202 个 error 主要是旧窗口误选导致 201 次
文档探测失败及一次工程打开超时。原始 partial 删除不都计为 daemon error，不能据此
把这个比例解释成设计成功率。token 未记录（JSON 的 0 是未自报默认值）。
成本已记入本机台账；原始命令见 `cost-final.command.json`，本轮不是完整 E2E。


最终包增补复核 `review/dev4-final-package-addendum.md` SHA-256：
`29f3be91a775a23543f65008ed05f20ef0bb3a7f50810e076339bc6072457c77`。
smoke 显式窗口复核 `review/smoke-explicit-window-review.md` SHA-256：
`8549266d5f62c941a36436ac25d4d17c8b2d9d8995eec3db2491a8f98cdfb793`。
最终包重载过渡期有一次 No active document，随后 typed reload 在期限内完成并 fresh 核实；
该中间失败保留在 audit，不覆盖为一次全无异常的调用链。

本地最终证据清单 `manifest-final.json` 覆盖 861 个文件，SHA-256：
`b4666daf6255a726881faa04a178297ffe593173a2663346a005d398cef87c10`。原历史 manifest 保留，不覆盖失败证据。
