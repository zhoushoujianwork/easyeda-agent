# v1.16.0 有限专项验收报告

本版范围为用户本轮确认的 cli-fixes，完整小板 full-design-e2e 留待 follow-up。
产品源码基底为 origin/main a0aa9620；本版只新增版本元数据和验收材料，不夹带 dev 的其他提交。
本次重新执行自动测试，并由独立 Agent 从原始现场产物核对各专项；没有在新版本声明的连接器上
重放 EDA 写入。已冻结的现场源码与本版关系逐项证明，不能据此称任意工程或硬件通过。

## 用例结果

| ID | 结果 | 回读与证据 |
|---|---|---|
| M1 | pass | 新候选 Go 5829 项/18 包、Connector 1305 项/0 失败、发布脚本 123 项、MCP、typecheck/build、vet 和 agent/Skill 检查全部退出 0；release-v1.16.0/m1-* 保存本轮日志。 |
| F1 | pass | CBB 原始 native-pin-net-acceptance-result.json、canonical-reload.stdout 和 final-single-frame-target-fresh.stdout 证明真实 block_symbol、两接一未接与三端口接线的保存重开；VBUS/GND/KEY 分别读为 VBUS/GND/CBB244_KEY，authoritative-key-port-expanded-audit.stdout 关联内部 SW1。 |
| F2 | pass | ARC 原始 circle/half/mixed/points-create.json 均 verified/poured；after-reload-pours.json 按精确 ID 保留四轮廓，after-reload-copper.json 有四个实际铜，drc.json passed:true/0；负例开环和零 sweep 拒绝且不写入。priorityVerified:false 不外推为优先级验收。 |
| E1 | pass | 制造导出 gerber-final-mm/inch、placement-final-csv/xlsx/mil 的响应与实际字节可核对官方文件 SHA256、板语义哈希、Gerber MM/IN 头、CSV/XLSX 相同字段及 mm/mil 换算；UTF-16 源字节保留不改写。只验导出契约，不验制造适用性。 |
| L1 | pass | source-exact-rectangle-cleanup-once.stdout、source-single-frame-reopen-fresh.stdout 保留三个 pin 和同一 polygon、删除唯一显式矩形并 saved:true；original-pages-preservation.json 及 final-acceptance-assertions.json 对账原页与源电路；ID 无实例、歧义变体、非空目标和 stale 几何负例由当前自动回归覆盖。 |
| N1 | pass | 导图 drawings-from-original/schematic.svg 与原始导出字节相同，drawings-dev2/manifest.json 的六个图源与 spec/文件 SHA256 对应，designAccepted:false 和 PCB review pending 均保留；弹窗只验流程文档与已有 70% 关闭记录，不声称通用自动应答或已点击后来消失的同步框。 |
| R1 | pass | dev23 的冻结清单 527 个文件与本版 1043 个 internal/extension/src 文件集合/字节独立对齐；clean c1770c69 原生二进制 vcs.modified=false，临时目录 15 项离线 CLI 冒烟通过；共享服务未替换，验收范围限于已记录 fixture 保全与候选身份。 |
| E2E | not-run | 用户于本轮确认本版采用 cli-fixes，完整 ESP32-S3 最小点灯板需求到成品 E2E 延后 follow-up；本版没有重新执行全小板设计，也不改变旧未完成结果。 |

## 现场回读

本地原始证据集合如下；公开材料不打包客户/个人工程、凭据或原始私有日志。

- CBB：issue244-typec-cbb-20261031-r02。evidence-sha256-final.json 覆盖 527 文件，
  原始 save/reload/close/open 和 fresh 结果保留；最终 strict 检查为 0 findings。
  三端口连接未改变全局电源旗语义；KEY 需要外部上拉。原生 DRC passed，但仍有三个
  ONENETNAME/SHOWDANDLING advisory，不称完整硬件设计通过。
- ARC：easyeda-pr287-live。实际执行于独立空 PCB，不使用 cases.json 中计划中的 ceshi 身份
  代替现场上下文；真实执行身份在各响应中一致。原生返回反向绕行，实际铜及保存重开均保留。
  数组包装、重复闭合点仍仅离线覆盖；不证明任意板的优先级、回流或制造质量。
- 制造：issue284-manufacture-20261031。CSV 源数据为宿主原样返回的 UTF-16 表格，
  XLSX 字段和单位换算单独核对；单电阻独立 fixture，不称制造/装配验收。
- 导图：acceptance-pr288-pr289-20261031-r01/r02。原 SVG 字节、内嵌静态标志和六图来源
  独立对账；原 468 器件白屏与 EP01 成品验收仍未证明已解决。活动页重复打开修复只离线覆盖。
- 电源布局：issue273-bounded-repair-20261031。两个冻结输入的预算均为 3000 候选，
  修复前失败，修复后 buck/boost 分别 2966/1663 候选成功，路由节点低于 200000；
  当前全量回归覆盖该最小缺陷。本项是离线布局求解契约，现场 EDA not-run，不列为现场通过。

本版 source-comparison-to-live-dev23.json 对 1043 个 internal/extension/src 文件路径与 SHA256
逐项对照、0 差异；独立复核另以 git ls-files cmd pkg extension/config extension/build
go.mod go.sum Makefile 比较 69 个生产/构建文件，也为 0 差异。ARC pcbPourCreate 与 c72542e0 完全相同（独立单元 SHA256 记录在
arc-handler-source-alignment.json）；制造导出和 SHA256 模块与 61412ce4 相同；
导图实现与 1719ccd6 对应现场候选相同。仅凭文件关系不能推定新的未测路径已通过。

## 候选构建身份

干净原生候选来自 c1770c690703536a4f5aad98cc5f966f38cd8f22，版本串 v1.16.0，
Go 1.26.3 / macOS arm64，vcs.modified=false；SHA256
`e7158ccaa8b6e46d99540ad3a310054a74b931c8b6619974cf17ef358d067f17`。
该提交之后的报告修改仅为文档，最终正式资产将重新从完成材料的干净提交构建并单独核验。
早期 Git worktree 构建没有 VCS stamp，已如实记录，不拿它当上述干净构建。

## 独立复核

只读独立 Agent 已完成专项复核，结论 pass，仅限 schema 3 声明的 cli-fixes。
复核者未参与现场写入，直接核对原始文件与当前源码，不依据摘要中的通过标记。

- 独立重算 CBB 冻结清单 527 文件全部匹配；1043 个 runtime/internal 文件路径和 SHA256
  与 dev23 均一致；额外 69 个生产/构建文件也一致。
- 69 文件按排序后的 `path + NUL + sha256 + LF` 拼接，其整体 SHA256 为
  `cd4a90389291bd2dd8f847a6b84e12bad96400ecaa70f68f541ab6d59fb6c43f`。
- 从 CBB raw 回执确认已接网、未接对照、内部按钮关联、保存关闭重开和矩形清理保全。
- ARC 负例前有三个边界，拒绝两个非法输入后只增加 legacy points 的返回 ID；
  重开后四个 ID 恰好相符，实际铜四个且 DRC 0，不把 priorityVerified:false 改成优先级通过。
- 制造五份文件实字节与官方 SHA256 匹配；mm/inch Gerber 各九层的大小、哈希、MO 单位
  逐项核验；UTF-16 CSV 与 XLSX 字段相同，mil/mm 坐标换算重算一致。
- 两套导图各七项 fileHashes 全部一致，designAccepted:false 与三项 PCB review pending 保留。
- 原生二进制 SHA256、vcs.revision 与 vcs.modified=false 相符；需求原话的 SHA256 独立重算一致。
- 首轮发现基准缺原始需求哈希、专项 pass 与发布完成条件混淆，均已修正文档后重新核验。

此结论不覆盖尚未执行的完整发布构建和安装升级步骤；该部分完成后另做第二阶段核验。

## 发布资产与安装验证

正式全资产构建与安装升级验证在验收材料通过后继续执行，并单独记录结果；
未打标签、未上传、未发布。PR 通过不等于外部平台的公开包已经更新。
