# v1.18.0 定向修复验收报告

日期：2026-11-14。范围由本版用户明确决定，详见 baseline.md。结论只覆盖下列实际完成的自动回归与
#299、#303 现场定向验收；完整设计 E2E 未执行。

## 用例结果

| ID | 结果 | 实际证据与边界 |
| --- | --- | --- |
| M1 | pass | Go 原始 JSON 日志统计 6145 项通过、1 项跳过、0 失败，16 个测试包通过、2 包无测试；go vet 通过。连接器 1400 项全部通过，tsc typecheck 无错误。测试于 2026-11-13 在 dev 的 c2e2aee6 上运行，当时工作区无未提交修改（版本元数据在 11-14 才修改）。 |
| F1 | pass | 拖动前（写后即时回读，未重载）零长度导线 0、wireContacts 0。GUI 左移 R102 后保存并重载，新回读 R102 引脚为 (160,300)/(120,300)，导线 b9af4d19bc40e829 为 (120,300)→(120,300)；sch check 报 zeroLengthWires 1、wireContacts 0，sch read 无 wire-contact，bridge-check 0 wire-bridge。追加 NET_D 后仍为 0（写后即时回读）。只验证修复版在触发几何下不误报，旧版误报由离线回归覆盖。 |
| F2 | pass | 设计规则对话框切为 mil 后保存重载，pcb drc-rules 回读 Track、Via Size、Safe Spacing 均为 unit=mil（Track 10、过孔 12.0078/24.0158/393.7008、净距 5.9843），与问题报告原始存储一致。route-short --dry-run 在数值等价的 mm 系统预设与 mil 自定义配置13 下输出逐字节相同：clearanceMil 5.98、trackWidthMil 10、powerWidth 20、规划 1 段。实际布 1 段 10 mil 线并重载后，pcb check 的 clearance、widthUnderSpec、copperNearEdge、widthMismatches 均为 0。 |
| E1 | pass | v1.17.0 标签构建的 CLI 在已布线后的同一 mil 板上 route-short --dry-run 读出 clearanceMil 235.6、trackWidthMil/signalWidth/powerWidth 393.7，约为 5.9843/10 mil 的 39.37 倍，与 #303 报告一致，负对照成立。该次唯一网络已布线，其 0 段/1 段跳过（already routed）不作为"无法布线"的证据。旧版 pcb check 在该板同样为 0（唯一走线附近无异网焊盘、无板框），pcb check 只证明修复后无误报，不复现旧误报。 |
| L1 | pass | 连接器 identity lcsc/mpn/project-name 永不返回三项及"回退共享剩余预算"一项通过，迟到结果不能写入；Go TestBoardSnapshotSemanticHashIgnoresRecomputedPourFills 等语义哈希回归通过。替换查询超时与真实铺铜重载场景未做现场复现。 |
| N1 | pass | e0a731f8 新增的 daemon 自动替换身份保护 5 项（含 TestListWindows* 4 项）及 owned daemon 回归通过；make agent-check、skill-check（192 个已跟踪文件，链接不越界）与 release-script-test（136 项）全部通过，退出码均为 0。 |
| R1 | pass | build-local-snapshot.py 从 c2e2aee6 构建 1.18.0-dev.1，构建时 local-dev release-check 通过（该次控制台输出未留存，checksums.txt 10 项已复算一致，local-dev.json sourceRevision 为 c2e2aee6、sourceDirty false）；热重载注入核对旧 bundle f494b910… 与新 bundle 18c16893…，保留权限（daemon 审计日志有记录），health 快照显示新 windowId 053bd4d2 与 connectorVersion 1.18.0-dev.1。 |
| E2E | not-run | 本版用户在被告知完整点灯板 E2E 未执行后选择定向修复范围，完整设计及其 DRC、保存重载、新回读延期 follow-up；不沿用旧版本现场结论。 |

Go 的 1 项跳过是本机不具备 Windows PowerShell 5.1 的平台限定测试，不计为通过。

## 可追溯证据

源码基准 `c2e2aee6a2329509f1e07e8faa6473c5fa2dab88`。运行流水留在本地忽略的 `artifacts/v1.18.0-logs/`，
公开包不含个人路径、账号或私有原始数据。

| 原始记录 | SHA256 |
| --- | --- |
| go-test.jsonl | 81f3ded0b3b8b016664fba8ed16f5c4a5bfc9f9d37512cf55cd7d66879f28a23 |
| connector-test.log | 0f22575176b554227c0d9f6af080907bae0281da22de3cf8bdb81120112e224f |
| repo-checks.log | 46cb30862d3fa66ddd530185da9819c190203b95730758f41841eafc4832b611 |
| 299-before-check.json | 15e9901434537096850b2afd022ab4e918a69174dc22c1c74c69272e8d5fef31 |
| 299-after-geometry.json | 8f8c7592e9a5ffdb31d33220042dcedf4da3577453e1e16accc4524db7850e42 |
| 299-after-check.json | 264ea9aeb4f0bfb007ca0ecc55c30f98ab6b1307c5430f8c68dcf46f9c629ebd |
| 299-after-read.json | 0c6b28cb439cc8d200f6710fd380137622a70081c9f9215a2dc6f0f942fc3803 |
| 299-after-bridge.json | 1f34eaf12b2e99c3d3a55889c13364de858b0e47b83b2bfdb8b7c8625e589e78 |
| 299-neg-check.json | e4d70d1ae17ccc75cad83a17defac951276f4ae7e2a881cd773361369e22bff2 |
| 303-mil-drc-rules.json | 0d40cae19f9af9d893cfa05095367d944593daa9161b30975c48ffa1b17cd2b8 |
| 303-mm-route-short.json / 303-mil-route-short.json | 6aa9aea8dd60d163080b9bbdb09177e23972b539ca50559fc1f8931eb170a36f |
| 303-mil-route-short-OLD-v1.17.0.json | c53b412412ae17ebbc8f4de4d7412ece8816101d12a92b08059c23335e7d7b85 |
| 303-mil-check-routed.json | 199c3d64c0a22948e316ad3170919d4a2dd2a1e2ffd1994dd42534be4f485503 |
| health-ceshi-1.18.0-dev.1.txt | 9e69e3595732686b5828aa6d402631ba178e55886504e8c12ab18f29eae27c39 |

## 现场回读

宿主：edadesk 中的 EasyEDA Pro 4.1.100，测试工程 `ceshi`，连接器 `1.18.0-dev.1`。F1 拖动后（after-*）的回读，
以及 F2 的规则回读、新旧版 route-short、布线后 pcb check，均在显式保存 → `doc reload` → `doc switch` 之后取得，
不使用自动保存或旧读状态。拖动前基线和追加 NET_D / NET_E 后的检查为写后即时回读，未重载。

- F1 的拖动、F2 的单位切换按用户本轮明确要求由 Agent 通过 GUI 完成，仅用于制造前置条件；判定只依据重载后的 CLI 回读。
- CLI 写规则（`pcb drc-rules-set --from` mil 配置）时，宿主立即把三个节点存回 `mm`，工具如实返回
  `verified: false`；因此 `mil` 存储只能由对话框产生，这与问题报告人的手工配置路径一致。
- 正对照未能在现场构造：用 CLI 让 NET_E 与 NET_C 真实 T 接时，宿主把新导线并入 NET_C（返回同一 primitiveId），
  不存在异网接触。真实 T 接与共线重叠的报警能力由离线回归覆盖。合并后的 NET_C 为多段折线，未被误报异网接触，
  符合同图元折线豁免。
- `bridge-check` 的 3 个 orphan-tree 来自复现用例本身（三条带网名导线不连接引脚），与修复无关。
- `pcb check` 的 15 条发现为导入后未整理布局产生的丝印压焊盘、天线净空、缺基准点和连接器配合，与规则单位无关；
  USB-C 多边形焊盘不受 net-path 支持，报告标记 incomplete。
- Windows 4.1.60 报障环境未复验；本次发布不会自动更新正在使用的 CLI、daemon、连接器或 Desk，也不代表立创连接器市场已更新。

## 独立复核

独立 Agent 已只读复核本版基准、用例与报告，并复核了修正后的版本。复核时从 go-test.jsonl、connector-test.log、repo-checks.log 和 summary.txt 重新统计各项数字与退出码，逐项重算 14 份原始记录（含 health 快照）的 SHA256，确认均与报告一致。复核还对照 daemon 审计日志核实了 #299、#303 现场回读的操作顺序与保存重载节点，确认报告对即时回读与重载后回读的区分属实。源码方面，检查了 v1.17.0..c2e2aee6 中 `pcb_rules.go` 按节点单位归一化、`schematic.check` 统一使用剔除零长度导线后的数组这两处修复，并确认旧版负对照源码与 v1.17.0 标签一致。DEV 候选包 checksums、local-dev 的 sourceRevision、热重载新旧 bundle 哈希（18c16893… / f494b910…）及 health 中的 connectorVersion 均已核对。独立复核结论为 pass，仅适用于 v1.18.0 声明的 cli-fixes 定向范围：#299 现场只验证了修复版在触发几何下不误报，旧版误报由离线回归覆盖；#303 的旧版负对照以 39.37 倍误读值成立，pcb check 未复现旧误报。两处前置条件由 Agent 应用户要求经 GUI 制造；Windows 报障环境未复验；完整设计 E2E 仍为 not-run。
