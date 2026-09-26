# 高级 CLI 业务验收：前置条件与步骤

[返回简版](cli-advanced-test.md)。高级用例只在同版
[基础 CLI B00–B10](cli-live-test-detail.md)全部通过后运行。这里的 `sch`/`pcb` 命令用于
跨对象规划和设计质量判定；同名命令域中的基础增删改查已在基础层单独验收。
用户明确接受的本次范围例外按[简版进入条件](cli-advanced-test.md#进入条件)执行，
包括 v1.8.0 的三项已知限制，不将该次准入误写成严格基础全通过。

## 全局前置条件

1. 保存基础门禁的 `pass` 报告，或用户明确接受的范围决定及其基础证据；记录安装包/源码 commit、CLI/daemon/连接器精确版本、
   目标工程/页 UUID，以及专用工程的原始快照和语义哈希。任一基础项回退为
   未被该次范围决定接受的 `fail`、`blocked` 或 `not-run`，停止相关现场用例并重新核对基础层。
2. 完整端到端回归只向无历史上下文的执行 Agent 提供
   [`esp32MiniRequire.md`「一、客户原始需求」](../esp32MiniRequire.md)；不喂 BOM、UUID、
   网表、预制布局或历史答案。单项算法回归可用冻结样例，但必须标明它不是完整端到端。
3. 原始快照、源数据、参数、单位及 SHA-256 成对冻结。每次执行保存命令、UTC 时间、
   stdout/stderr、退出码、候选报告、Apply journal 和新鲜对象回读。失败只修改源数据
   或算法后重算，不手改现场或绕过 typed 接口。
4. 主执行员串行访问 Web 窗口；独立 Codex subagent 在执行员停用窗口后只读复核。
   需要用户确认 PCB Layout 的回读版本时先展示事实与预览，确认前不进整板布线。

## 高级用例

| ID | 专项前置条件 | 操作 | 通过判据 |
|---|---|---|---|
| A00 需求与选型 | 本轮基础准入满足（严格通过或明确接受的限定范围）；原始需求或单项样例身份已冻结 | 从需求选择器件、封装、功能块和约束；核对库身份、引脚、外围归属与参数来源 | 需求逐条映射到可复算源数据；变体歧义和缺失数据明确阻断，不把基础 `lib search` 成功当选型通过 |
| A01 原理图规划与求解 | 原始快照、测量值和源数据 SHA-256 齐备 | `sch zone-review`、`layout-plan`、`layout-sheet-plan`、必要的 `compose`；分析候选、所有权和跨区关系 | 报告 `sourceSha256` 与原始字节匹配；唯一归属、核心外围跟随、direct 连接、位号与图纸边界判据满足。预算耗尽只说明本次搜索失败，不证明全局无解 |
| A02 保护执行与原理图质量 | A01 形成完整候选，页面身份和基线未变 | `sch apply --dry-run` 后执行受保护队列；fresh `sch list/read/connectivity`，`layout-lint/check/bridge-check/drc/gate --strict`；save→reload→fresh readback | journal 全成功，目标对象和网络逐项吻合，范围外不变；0 overlap、0 fatal，严格 gate 按当前规则通过；保存重载后仍一致 |
| A03 PCB 同步与规则 | A02 的原理图完整通过且已持久化 | `pcb new-board`、`board-info/docs/list/layers/nets`；按回读选择同步方式；设置叠层、板框、规则和功能约束 | board/schematic/PCB UUID 与 uniqueId、封装焊盘及网络逐项对应；四层与工艺规则由真实配置回读，不凭命令成功推断电源树 |
| A04 PCB 布局求解与确认 | A03 的真实封装、焊盘、板框和网络快照齐备 | 参数化 `pcb layout-plan` 或 `pcb layout solve/check`，候选 Apply；`dump/layout-lint/layout-score/stage-snapshot`；连续两轮整板自检，第 2 轮 save→reload→fresh dump→fresh render | 器件无越界/重叠，模块关系与要求一致，持久化对象和预览相符；向用户展示当前回读版本并等待明确确认 |
| A05 路由、铜与设计检查 | 用户已确认 A04 的持久化 Layout，线宽/间距/网络/keepout 参数齐备 | typed `track/via/region/pour` 或参数化 route 候选 Apply；fresh `track-list/via-list/pour-list/poured-list/net-path`、`pcb check/drc` | 网络连通、间距、禁布区和材料化铺铜逐对象对账；未声明外铜保持不变，DRC 与客户需求均达标；缺测不记通过 |
| A06 最终落盘与独立复核 | A05 所需事实全部通过 | `pcb save`→`doc reload`→fresh `pcb dump --include-copper`/DRC；导出与审计；复核员重算输入/报告哈希、journal、需求覆盖与成本记录 | 对象、网络、铺铜和规则持久化，导出文件可读；独立复核逐条给出结论，不用执行员自评或截图代替回读 |

## 结论边界

A00–A06 的某一步 `fail`、`blocked` 或 `not-run`，高级现场验收就不通过。
[固定 ESP32 原始需求 E2E](e2e-automation-acceptance.md)还须按公开 Skill 的 S0–S6/P0–P10
和仓库规则完整跑完。基础层只证明工具动作可靠，不能替代这些设计事实；高级层的求解、
评分或 DRC 结果也不能反过来补签基础层。
