# Web 文档源码探针

这是文件后端研究的第一层验证，不是通用源码 Apply 或 eprj3 工程导入。只使用用户授权的
专用 Web 测试工程，先由 `health.windows` 核对窗口、工程、文档和运行中的连接器。

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
此探针不触发 daemon autosave；保存由单独的 typed 命令执行并留下独立证据。

测试时另留原理图 connectivity/list 或 PCB dump、DRC 与 typed 图像。同源 roundtrip 的目标是
保持设计，实际结果仍须对账；它不证明生成器、自动布局/布线、eprj3 folder 同步或 Web 工程导入可用。尚未完成
现场核查时保持 `offline-verified`，记录缺口；原始响应留本地忽略的 artifacts。

## Web 4.1.60 现场结论

本地候选 `1.9.1-dev.2` 已在专用 Web 工程验证以下有限范围，不能据此扩大到其他宿主版本或设计：

| 项目 | 观测与边界 |
|---|---|
| 原理图 getter | `live-verified`；一个 10kΩ 电阻、两个端口和两条真实导线，保存所有原文及 SHA |
| 默认 exact | `live-verified` 写前拒绝；重复 getter 生成不同的三个 DOCHEAD 值，不能通过逐字比较 |
| 显式 v1 dry-run / 单次 setter | `live-verified`；旧空页快照仍拒绝；setter 返回 true、即时正文逐字相等、全部对象和真实 pin/net 回读严格相等；raw 相等为 false |
| save → typed reload | 正文逐字保持，连接保持，DRC 仍 0 fatal / 0 error / 2 warning（无逐项详情），整页 PNG 的 SHA 完全相同；不等于全部对象身份保持 |
| 原文与全量对象持久化 | 未通过严格相等：DOCHEAD 易变，图框 `@Update Time` 更新，19 个派生属性 ID 重建；不得把它们默默排除后称完整通过 |
| PCB getter | `live-verified` 仅空 PCB，头部也易变；v1 比较规则不支持 PCB，setter 与非空 PCB 保持未验证 |

首次通过连接器升级重载新建样例时，宿主还物化了 `strikeout:false` 等默认字段并调整
zIndex；该变化发生在 setter 之前，已保存独立差异。随后以 fresh 已重载状态为基线验证
setter，不把初始快照覆盖或扩充易变字段名单。需要从新建样例验证持久化时，必须单独
报告这类初始化变化，不能沿用后续稳定基线的结论。

只有 getter 和这次非空同源探针获得现场证据；任意新源码 Apply、库容器生成、Web eprj3
导入和整板 E2E 均未验证。继续保留 canonical/参数化计算与 typed 回读主链，源码路径作为
候选后端研究。
