# 官方 API issue 台账(easyeda/pro-api-sdk)

> 我们向官方 `easyeda/pro-api-sdk` 提交 / 跟踪的 issue 全量进度,方便后续回顾。
> 每条记录:挡住我们哪条能力线 → 官方最后回应 → 当前状态 → 我方 workaround / 待办。
> **最后核对:2026-08-22。** 更新方式见 memory `upstream-issues-watchlist`(每次任务分析必查)。
>
> 本地 EDA:**web 版(嘉立创EDA专业版 / JLCEDA Pro)**,版本 **3.2.149.88089769**(2026-08-22 实测)。
> 版本 API:`eda.sys_Environment.getEditorCurrentVersion()`。

## 汇总表

| # | 我方? | 提交 | 主题 | 状态 | 官方最后回应 | 我方 workaround / 待办 |
|---|:---:|---|---|---|---|---|
| [#27](https://github.com/easyeda/pro-api-sdk/issues/27) | 跟评 | 04-17(butterfly2sea) | sch DRC `includeVerboseError` 返回 boolean 与类型不符 | **open** | `includeVerboseError` 已在 **EDAv4.2** 支持,等升级(07-06);**另一用户 07-31 追问「一个月了 v4.2 还没见到」,官方未再回** | 等 v4.2;到手后 `sch check` 去掉几何重建([[schematic-drc-aggregate-only]])。**v4.2 迟迟不落 web 通道,别把任何能力押在它身上** |
| [#28](https://github.com/easyeda/pro-api-sdk/issues/28) | ✅ | 06-29 | `pcb_Document.autoRouting` 运行时 undefined(@alpha) | **CLOSED / completed** | 已在 **EDA v3.2.150** 添加(07-06) | **卡版本**:本地 web 3.2.148 < 3.2.150,`autoRouting` 仍 undefined → 升级后再 probe,可用则回收 Freerouting 外包 |
| [#29](https://github.com/easyeda/pro-api-sdk/issues/29) | ✅ | 06-29 | `getDsnFile` 导出 DSN 丢禁止区域 keep-out | **CLOSED / wontfix** | EDA 无 Keepout 层,DSN 用 **SMD 游离焊盘**表禁布区=正解;挡不住布线去 `easyeda-pcb-router` 扩展反馈(07-06) | 天线 keepout **每层独立 region** 校验([[pcb-antenna-keepout]])保留;单层游离焊盘挡不住多层净空,不依赖此链路 |
| [#30](https://github.com/easyeda/pro-api-sdk/issues/30) | ✅ | 07-03 | `sch_Netlist.getNetlist()` 悬空引脚下无限卡死 | **CLOSED / completed**,`seems like AI` | `getNetlist()` 是 v2.2 接口、**v3 已移除(@deprecated)**,用 `getNetlistFile()`;**并训:AI 提单请人工校对文档**(07-06) | 早已改用 `getNetlistFile()`([[programmatic-schematic-no-netlist]]),无剩余动作 |
| [#31](https://github.com/easyeda/pro-api-sdk/issues/31) | ✅ | 07-03 | 4 层板 track↔via 不连通,DRC 恒报 Connection Error | **CLOSED / not_planned** | 线上无法复现,只报网表不匹配;要原样代码(07-07) | **我方误诊,已闭环**:真机复测证明 track↔via 会连通(真身是 pour stale,pour-rebuild 即复原);删 via-bond 规则 + via-hop bondFill 改 opt-in,回帖关单([[pcb-via-track-bond-rules]]) |
| [#32](https://github.com/easyeda/pro-api-sdk/issues/32) | ✅ | 07-03 | PLANE 生成后新异网 via 不挖 anti-pad,重建铺铜不修复 | **open** | Xieguangyuan(07-08):**这是预期行为** —— 需在 PCB 设置里勾选**「自动重建铺铜区域」**(附截图) | `pcb check` **via-crosses-plane** 规则 + 修法保留;**待办**:查该设置有无 API,能否在 `power-planes` 流程里自动开 |
| [#33](https://github.com/easyeda/pro-api-sdk/issues/33) | ✅ | 07-03 | API 放置焊盘 number 读回 null + DRC 无结构化明细 | **open** | Xieguangyuan(07-08):**内部最新 3.2 已无此问题**,等新版上线后复测 | pad-number 恒 1 条 Netlist Error 白名单 + net degree 机械自证保留;**待办**:本地 3.2.149 复测 |
| [#34](https://github.com/easyeda/pro-api-sdk/issues/34) | ✅ | 07-06 | 新建未重载 PCB 的 reflow 用创建时规则快照 | **open**,`seems like AI` + `help wanted` | Xieguangyuan(07-08):**线上最新版已无此问题**,并附「覆写当前设计规则.js」官方示例 | 4 步配方([[pour-reflow-divergence-and-rules-api]])保留;**待办**:对照官方示例核对我们的规则覆写调用形状 |
| [#35](https://github.com/easyeda/pro-api-sdk/issues/35) | ✅ | 07-07 | **开放「组合 / 多通道复用(Reuse Block / Group ID / Channel ID)」读写 API** | **open**,`enhancement` + `seems like AI` | **零文字回复**,仅 07-08 打标签 | `schematic.group.move` 无状态虚拟分组([[easyeda-native-group-no-api]])顶着;**08-22 于 3.2.149 复验:三扇门仍全关**(读 3 路全失败 / 写静默丢弃 / `lib_Cbb.search('')`=0) |
| [#36](https://github.com/easyeda/pro-api-sdk/issues/36) | ✅ | 07-24 | `sch_PrimitiveText.delete` 返回成功但未入持久化事务,存盘重开文字复活 | **open** | 零回复 | 删文字后必须**存盘再回读**验证,不信 delete 回执 |

## 待定项(已登记,**未**提交)

踩到了、绕过去了、但**还没提也可能永远不提**的平台限制。放这里而不是直接开单,
是因为下面那节的三次 pushback + **#31 的教训:我方误诊提单,官方复现不了,最后自己关单**。

**每条五栏,缺一不许上报**:

| 栏 | 要求 |
|---|---|
| 现象 | 一句话 |
| **最小复现** | **纯 `eda.*`、不含我们任何代码**的脚本。这是能不能上报的**门槛**,不是可选项 |
| 我方绕行 | 现在怎么扛住的 |
| **绕行的残余风险** | 绕不掉的那部分 —— 这才是上报的正当理由 |
| 需要官方给什么 | **具体 API 形状**,不是"希望更稳定"这类诉求 |

**上报前置**:必须先排除我方竞态/误诊,并经用户过目后才 `gh issue create`。

---

### P-01 写操作没有「已提交」的可观测点(文档修订号缺失)

| 栏 | 内容 |
|---|---|
| 现象 | 一次变更类调用超时/失败后,**无法判断它到底落没落地**;一次回读说「那里什么都没有」,**无法判断是真没有还是读得太早**。真机实录:`place C8` 报 "connector did not respond",回读证实「(440,535) 没有新器件」,清完别的残件再读 —— C8 就躺在 (440,535)。 |
| 最小复现 | **❌ 还没有。** 现有复现全都掺着我们自己的两条竞态(见下),**在修掉它们之前,任何最小复现都不干净,不具备上报资格。** |
| 我方绕行 | ① 收编(`sch_place_adopt.go`,commit 785608f):超时后按「落地前快照差集 + 下发坐标 ±5」把已落地的孤儿认回来。② 门⓪新鲜度探针(4315ca7):空回读必须先自证新鲜才准下「没落地」的结论。③ **L1 连接器 FIFO + 完成序号**(进行中):把「读是否在写之后」从启发式变成算术。 |
| 绕行的残余风险 | L1 之后仍剩**一层**:`seq` 只能证明「W 的 handler 在 R 的 handler 开跑前 settle 了」,**不能**证明「文档已提交」—— `eda.*` 内部可能在 handler 返回后才落盘,我们对此**没有任何观测点**。 |
| 需要官方给什么 | **单调的文档修订号**:① `getAll()` / `components.list` 这类读的返回里带 `revision`;② 变更类调用返回「本次写产生的 revision」。有它之后「这次写落没落地」= `read.revision >= write.revision` 的算术判定,不再需要任何启发式。 |

**为什么现在不提**:三条成因里**两条是我们自己的代码** ——
① 连接器并发处理动作(`extension/src/transport.ts` 的 `await handleMessage` 在每条消息各自的回调里,`await` 不跨回调排队);
② daemon 不按窗口排序(`internal/daemon/dispatch.go` 的 `nonReentrant` 只有两条 DRC)。
**带着自己的竞态去提单就是 #31 的复演。**

**上报判定**:L1 落地并真机复测后重新评估 —— 若残余仍能用**纯 `eda.*`** 复现出
「handler 已 resolve 但读不到效果」,那时才够格开单,且必须附原样复现代码。

---

### P-02 批量 delete 静默 no-op 仍返 true

| 栏 | 内容 |
|---|---|
| 现象 | `delete(批量)` 返回 `true`,回读该图元仍在;逐个单发则 100% 成功。 |
| 最小复现 | **❌ 还没有干净的。** 观察到的场次全部发生在连接器队列 wedge 期,**极可能就是 P-01 的同一个根**(写被吞 + 读得太早),不是独立缺陷。 |
| 我方绕行 | `deleteVerifiedOneByOne`(逐个删 + 回读证实 + 重试一次)、`prim-delete` 的 settle 复核。 |
| 绕行的残余风险 | 逐个删把删 40 个图元的耗时放大一个数量级。 |
| 需要官方给什么 | 先别提 —— **等 L1 落地后复测**。若届时非 wedge 期也能稳定复现,再按 P-01 同样的规格立条目。 |

### P-03 3.2.174 上 `getNetlistFile()` 保存后仍返回空(用户报,我方无该版本环境)

| 栏 | 内容 |
|---|---|
| 现象 | 社区用户在 **EasyEDA Pro 3.2.174(Windows)** 上:器件/导线/netport 创建成功、`schematic.save` 返回 `saved:true`、几何与桥接检查干净,但 `getNetlistFile()` 返回空 → `sch netlist` 报 `Netlist export returned no file.`,`sch read --no-check` 得 `netCount:0` 且引脚 `net` 全 `null`。换独立 Board、完全重启平台与连接器均可复现(仓库 issue [#184](https://github.com/zhoushoujianwork/easyeda-agent/issues/184))。 |
| 最小复现 | **⚠️ 用户侧有完整复现步骤(空白 A3 页 + 真实库 R0603 + 两个短桩网),但仍是走我们 CLI 的形态,且我方无 3.2.174 环境。** 上报前需要用户把它压成**纯 `eda.*`** 脚本 —— 这正是本条留在待定区的原因。 |
| 我方绕行 | 网表读不到时退回 `sch read` / `sch check` / `bridge-check` 的**几何重建**连通性判断(本来就是我们为「平台 DRC 只回聚合数」造的那套)。 |
| 绕行的残余风险 | 拿不到**平台口径**的网名表:跨页网名审计(`sch nets`)、BOM/网表交付、以及一切以平台网表为准的对账都失效 —— 几何重建能证明「连没连」,不能替代平台网名。 |
| 需要官方给什么 | 定位并修复 3.2.174 上 `SCH_ManufactureData.getNetlistFile()` 返回空的回归(3.2.149 实测正常,回归区间在 3.2.150→3.2.174 之间)。 |

**我方复验(2026-08-25)**:在 **3.2.149** 上按用户步骤一比一照跑 —— `sch netlist` 正常写出
89,418 B artifact 且含 `CMDTEST_A`/`CMDTEST_B`,`sch read --no-check` 得 `netCount:2`、
引脚 net 正确。**故障与平台版本强相关,不是我方缺陷**。

**我方复验(2026-08-27,浏览器版 3.2.186)**:同一 `ceshi` 工程内新开一个浏览器窗口,版本
上报 `3.2.186`,与出问题的 `3.2.174` 同一台账、同一份复现步骤(R0603 + 两个短桩网 + save)
再跑一遍 —— `sch netlist` 正常导出 86,584 B artifact 且含测试网名,`sch read --no-check`
`netCount:7`,R199 两个引脚 `net` 字段均正确赋值。**回归区间进一步收窄:3.2.149 好、
3.2.174 坏、3.2.186 好** —— 故障窗口封闭在 3.2.150→3.2.174 之间,且已在更新版本上自愈,
判定**平台侧已修复**。已回帖 #184 并关闭。

**结案**:不再需要报告人向 pro-api-sdk 单独提单 —— 用「升级到 ≥3.2.186」代替「等官方修复」。

### P-04 `dmt_Schematic.modifySchematicPageName`：旧版失败，Web 4.1.78 定向复验通过 (#55)

| 栏 | 内容 |
|---|---|
| 历史现象 | Web 4.1.60 / 4.1.71 上对新建原理图页调用 `modifySchematicPageName(pageUuid, name)` 返回 `false`，fresh 页名保持 `P2`；切到目标页及 ASCII/中文名复测仍失败。保留当时失败，不将缓存猜测作为根因。 |
| 当前观察 | 2026-10-31，Web 4.1.78、CLI/daemon/connector `1.15.1-dev.5`：在专用测试工程新建临时页，经 `sch page-rename` 单次官方调用返回 `true`，typed 回读 `verified:true`、目标 UUID 名称匹配；显式 `saved:true` → 真实关闭重开 → fresh 页清单仍保持请求名称。当前版本该路径不再复现。 |
| 清理与范围 | 仅删除本轮临时页；页清单恢复原始两页，原页显式保存、真实重载后的 components/pins/wires 完整结果与基线一致，已切回原 PCB。仅签本次 ASCII 名称定向验证，未跑 B00–B10 全集，不补签旧版或完整设计验收。 |
| 当前用法 | 用 `sch page-rename --page <fresh-page-uuid> --name <name>`；仍必须官方返回 `true` 且 fresh 页名一致，拒绝/未知不得报成功或自动重试写入。图签 `@Page Name` 是平台投影字段，不作替代写入路径。 |
| 归因边界 | 未确定上游在哪个版本改变行为，也未证明旧失败的唯一根因；不宣称全部版本或名称组合已修复，无需为本次通过制造代码修复。 |

---

## 官方对「AI 提单」的态度(重要方法论)

维护者 **yanranxiaoxi** 已 **3 次**对 AI 味 issue pushback:

- **#30**(07-06,关单):"使用 AI 提单时请人工校对一遍文档的说明,AI 在阅读文档时喜欢偷懒,会漏掉部分内容" + `seems like AI` 标签。
- **#31**(07-07):"首先,你使用 AI 生成的案例是错误的,传参并不符合文档规定" + 给出正确调用。
- **#34**(07-07):`seems like AI` + `help wanted` 标签。

**今后提 issue 硬规矩**:(1) 复现代码必须**原样**——真机跑过的确切代码,**先核对官方 API 文档签名/@deprecated 标注**;(2) 贴 DRC 截图 / 线上可复现环境;(3) 去掉 AI 腔。见 memory `copy-training-bugs-filed` 的开票习惯需相应收紧。

## web 版本升级(#28 的前置)

本地是 **web 版**,版本由服务端下发,不能像客户端那样手动装包:

1. **硬刷新**拉取服务端当前部署版:macOS `Cmd+Shift+R`(绕过 HTTP 缓存)。
2. 若仍是旧版 → 服务端 web 通道**还没部署 3.2.150**(当前 web 构建日期 2026-06-01,落后约一个月)。此时刷新也拿不到,只能:**等 JLCEDA 推到 web 通道**,或改用**桌面客户端**(客户端通常领先于 web)。
3. ⚠️ **清 site data / IndexedDB 会连带清掉我们的连接器扩展**(它存在 IndexedDB,不是磁盘),需重新导入 + 重开「允许外部交互」——所以优先只做硬刷新,别轻易清站点数据。

升级到 ≥3.2.150 后 ping 一句,即可复验 `getEditorCurrentVersion()` + probe `pcb_Document.autoRouting` 是否真可用。
