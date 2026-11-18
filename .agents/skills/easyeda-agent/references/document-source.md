# Web 文档源码探针

这是文件后端研究的第一层验证，不是通用源码 Apply 或 eprj3 工程导入。只读 `get` 可用于
用户授权查阅的已有 Web 工程；`roundtrip` 写入只使用用户授权的专用 Web 测试工程。
先由 `health.windows` 核对窗口、工程、文档和运行中的连接器。

```sh
easyeda doc source get --window <window> --project-uuid <project> --document-uuid <doc> --out before.json
easyeda doc source roundtrip --window <window> --from before.json --out dry-run.json --dry-run
easyeda doc source roundtrip --window <window> --from before.json --out roundtrip.json
```

Web 4.1.60 的非空原理图实测：只读 getter 会重新生成首行 DOCHEAD 的 `client`、
`updateTime` 和 `version`；默认逐字比较因此在 setter 前拒绝。保留该失败结果，不能
把原文 SHA 不同报告成逐字往返通过。为单独验证 setter，可显式选择实验比较规则：

```sh
easyeda doc source roundtrip --window <window> --from before.json --out dry-run.json --dry-run --comparison dochead-volatile-v1
easyeda doc source roundtrip --window <window> --from before.json --out roundtrip.json --comparison dochead-volatile-v1
```

`exact` 仍为默认。`dochead-volatile-v1` 仅接受已实测的 SCH_PAGE 首行字节语法、
16 位小写十六进制 client、13 位毫秒整数 updateTime 及与之相等的十进制 version 字符串。
首行必须匹配目标 UUID；header、docType、uuid、editVersion、换行和全部正文仍逐字保留。
额外字段、重复字段、另一 DOCHEAD 或不匹配此窄格式的输入均拒绝，不猜格式兼容性。
setter 仍只接收本次 fresh getter 的完整原文一次；不会把快照、比较投影或修改后的正文写入。
报告必须区分声明规则下的 `verified` 与原文相等，记录三个易变字段的前后值及原文 SHA；
不据实验规则将原文持久化相等标成通过。

命令不接受全局 `--project/--doc` 路由，避免切页与源码调用之间的隐式目标变化。输出路径
必须不存在，源码快照保留原文、目标、字节数和 SHA-256。快照中的源码不是可编辑的写入计划：
roundtrip 先检查文件哈希、目标和 fresh 源码按声明规则相等（默认 exact 为逐字相等），然后只调用一次 setter，参数是宿主
fresh 源码。dry-run 仅读取与比较。未知接口、空/超限源码、错页和 stale 快照均在写前拒绝。

该比较是写前检查，官方接口没有原子 compare-and-swap。验证期间保持专用页静止，不让用户
或其他客户端并行编辑；快照哈希仅用于检查文件完整性，不是宿主认证或电气正确性证明。

setter 的 false、异常、迟到或声明规则之外的回读变化都不算通过。不自动重试、不自动回滚、不以 GUI 或
`debug.exec_js` 恢复；未结束的 setter 保持连接器队列阻塞。保留原始快照和失败回包，从
真实状态决定下一步。命令的 `verified` 仅表示声明规则下立即源码相等，原文相等另报；`persistenceVerified` 保持
false；显式保存、typed 重载、fresh 源码和全部对象/引脚网络对账后才能判断持久化。
非 dry-run 的此探针只有取得独立认证的 `mutationContextAvailable:true` / `mutationContext`
时才触发 daemon 按该文档类型自动保存；身份缺测、漂移或旧 connector 则不启用这层安全网。
无论自动保存状态如何，仍须执行单独的 typed save 并留下保存重载证据。

测试时另留原理图 connectivity/list 或 PCB dump、DRC 与 typed 图像。同源 roundtrip 的目标是
保持设计，实际结果仍须对账；它不证明生成器、自动布局/布线、eprj3 folder 同步或 Web 工程导入可用。尚未完成
现场核查时保持 `offline-verified`，记录缺口；原始响应留本地忽略的 artifacts。

## 旧快照写回（实验性回退）

`restore` 把同一原理图页的一份较早快照写回一次，用于整页回退。它与 roundtrip 的区别是
setter 收到的是快照而不是 fresh 源码，因此必须同时给出“当前页”的快照作为写前检查：

```sh
easyeda doc source get --window <window> --project-uuid <project> --document-uuid <doc> --out now.json
easyeda doc source restore --window <window> --to before.json --expected now.json --out restore.json --dry-run
easyeda doc source restore --window <window> --to before.json --expected now.json --out restore.json
```

- fresh 源码须与 `--expected` 在 `dochead-volatile-v1` 下相等，否则写前拒绝；没有记录过的修改不会被覆盖。
  `now.json` 同时是撤销这次回退的唯一依据，必须保留。
- 仅支持原理图页，setter 只调用一次；不重试、不自动回滚、不保存、不重载。
- `verified` 只表示写后 fresh 源码在该规则下等于快照。回退后此前读到的图元 ID 全部作废，
  后续写入前必须重新回读；连接关系对账、typed 保存、重载后回读仍须单独执行。
- 现场结论（Web 4.1.105，edadesk DEV，connector 1.18.1-dev.1，专用工程一页 30 个图元、154 KiB）：
  - setter 接受与当前页不同的旧快照并返回 true，单次读取或写回均在 1 秒内。
  - 删除两个器件并新增一段文字后写回旧快照：器件数、全部图元 ID、typed 器件列表、连接关系
    与旧快照一致，文字消失；typed 保存并重载后仍一致（图框更新时间除外）。
  - 反方向（从旧快照回到“已删除”状态）在 `dochead-volatile-v1` 下源码逐字相等，`verified:true`。
  - 恢复被删器件时源码不逐字相等：宿主重新展开这两个器件的库属性（多出 37 条 ATTR），其
    `Device` 属性值由器件 UUID 变为空串，记录顺序与 ticket 重排；未改动的 28 个图元记录逐字相同。
    命令因此报告 `verified:false` 并非零退出，这是按源码规则的如实结果，不代表对象未恢复。
  - 结论：整页写回可用于原理图回退，但“回退成功”必须以 typed 回读（图元身份、器件列表、连接关系）
    判定，不能以源码逐字相等判定。PCB、多页、含导线与总线的页面、层次图未验证。
- 日常回退用下节的检查点命令；`doc source restore` 保留为底层探针。

## 写命令检查点与步骤级回退

每条会写原理图页的命令在第一次写入前自动记录该页源码，一条命令就是一个可回退的步骤，
不需要逐个命令适配。只读命令、dry-run、PCB 写入不记录。

```sh
easyeda sch checkpoint list --window <window>
easyeda sch checkpoint list --project-uuid <project>    # 离线列出该工程所有图页的检查点
easyeda sch checkpoint restore <checkpoint-id> --window <window> --dry-run
easyeda sch checkpoint restore <checkpoint-id> --window <window>
```

- 检查点是“该命令执行之前”的页面。回退到它会撤销这条命令及之后的全部命令，不能只抽掉中间一步。
- `list` 与 `restore` 默认针对当前激活的图页；带 `--doc <图页>` 时先切到该页并回读确认，再读取或回退。
- 回退前先把当前页记为新检查点，结果里的 `undoCheckpoint` 就是撤销这次回退的入口。
- 回退成功按内容判定：每个图元的类型与 ID、除库元数据 `attrs` 外的全部字段、每条属性的值都与
  检查点一致才算 `verified`。源码逐字是否相等另报 `sourceEqual`。宿主重建器件时新增的库属性计入
  `attributesAdded`，把 `Device` 属性清空计入 `deviceAttributeReset`，这两项不判失败。
- 页面内容已与检查点一致时不写入，返回 `alreadyAtCheckpoint:true`。
- 回退不保存。之后仍须 typed 保存、重载并回读；回退前读到的图元 ID 可能失效，继续写入前重新读取。
- 记录失败（页面超过 2 MiB、身份不可读等）只在 stderr 警告，写入照常进行，该步骤不可回退。
- 存储在 `~/.easyeda-agent/checkpoints/<project>/<document>/`，可用 `EASYEDA_CHECKPOINT_DIR` 改位置；
  每页保留最近约 300 条。首条存完整压缩副本，之后只存相对上一条的差异，内容未变只记一条索引。
  `EASYEDA_CHECKPOINT_LABEL` 的值随条目保存，供调用方把检查点对应到自己的操作记录；
  `EASYEDA_CHECKPOINT=off` 关闭自动记录。
- 现场验证（Web 4.1.105，专用工程一页 30 个图元）：三条写命令各自生成检查点（完整 14.8 KiB，
  差异约 0.6 KiB），含记录在内每条命令 0.25–0.75 秒；逐步回退、撤销回退、回到初始状态后保存重载，
  typed 器件列表与连接关系同测试前一致。
- 未验证：含导线或总线的页面、一条命令写多页、增删图页、层次图、超过 2 MiB 的页面。

## Web 4.1.60 现场结论

本地候选 `1.9.1-dev.2` 已在专用 Web 工程验证以下有限范围，不能据此扩大到其他宿主版本或设计：

| 项目 | 观测与边界 |
|---|---|
| 原理图 getter | `live-verified`；一个 10kΩ 电阻、两个端口和两条真实导线，保存所有原文及 SHA |
| 默认 exact | `live-verified` 写前拒绝；重复 getter 生成不同的三个 DOCHEAD 值，不能通过逐字比较 |
| 显式 v1 dry-run / 单次 setter | `live-verified`；旧空页快照仍拒绝；setter 返回 true、即时正文逐字相等、全部对象和真实 pin/net 回读严格相等；raw 相等为 false |
| save → typed reload | 正文逐字保持，连接保持，DRC 仍 0 fatal / 0 error / 2 warning（无逐项详情），整页 PNG 的 SHA 完全相同；不等于全部对象身份保持 |
| 原文与全量对象持久化 | 未通过严格相等：DOCHEAD 易变，图框 `@Update Time` 更新，19 个派生属性 ID 重建；不得把它们默默排除后称完整通过 |
| PCB getter | `live-verified` 空 PCB 及已有非空 PCB 的只读读取；头部也易变；v1 比较规则不支持 PCB，PCB setter 保持未验证 |

首次通过连接器升级重载新建样例时，宿主还物化了 `strikeout:false` 等默认字段并调整
zIndex；该变化发生在 setter 之前，已保存独立差异。随后以 fresh 已重载状态为基线验证
setter，不把初始快照覆盖或扩充易变字段名单。需要从新建样例验证持久化时，必须单独
报告这类初始化变化，不能沿用后续稳定基线的结论。

只有 getter 和这次非空同源探针获得现场证据；任意新源码 Apply、库容器生成、Web eprj3
导入和整板 E2E 均未验证。继续保留 canonical/参数化计算与 typed 回读主链，源码路径作为
候选后端研究。

## 已有 PCB 的依赖边界

Web 4.1.60 非空 PCB 的只读样例返回 89 个 COMPONENT、242 个 PAD_NET，却没有 PAD
或封装焊盘几何记录。COMPONENT 保留封装 UUID 引用及 `footprintPrimitives:true`；
这不是包含全部封装库的工程包。PAD_NET 可复核焊盘身份和网名，不能据它推导铜形状、
尺寸或钻孔。getter 成功、网表相等与几何采集完整分别报告；需要封装依赖时先通过
typed 库读取采集并核对实际实例，缺少依赖保持 unknown，不由截图或源码中的空库存补签。
