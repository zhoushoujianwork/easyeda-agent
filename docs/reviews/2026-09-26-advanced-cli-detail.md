# 高级 CLI 续测证据与实现细节

[返回主报告](2026-09-26-advanced-cli.md)。原始数据留在本机忽略目录
`artifacts/cli-advanced-dev21-20260926/`；命令记录包含 UTC、完整 stdout/stderr、退出码和耗时。
本文不替代 A00–A06 的现场设计验收，也不更新 v1.7.0 已冻结发布证据。

## 运行身份与基础门禁

现场三方为 dev.21、宿主 4.1.60；产品实现归档于 `1414784`，当前工作树基于 `884a875`。
两提交间产品 Go/TypeScript 源码未变；发布脚本、测试、版本和文档有变化。

| 文件 | SHA-256 |
|---|---|
| 已安装 CLI | `6a26c240dcb6838843836e313ea0dd747c0dfd43ddf548d685628f1cdf0251c1` |
| dev.21 连接器安装包 | `e8fd7e6758211409e628f988c24b67cb18ea66902c6fdc21141a52f37f7a4880` |
| 该安装包内 JS | `511add6882444ec7a80c5369fc9f59c8792de61bbd8c1c37f3632ec96e673193` |

`preflight/basic-evidence-integrity.json` 保存 11 项基础清单与 252 件文件的逐项检查。
独立复核同时检查已提交的 11 份单项报告哈希及 B02/B10 的完整 PCB 清理差分。
当前 `extension/dist/index.js` 与 dev.21 包内 JS 仅有两处版本文字变为 1.7.0；不能用构建目录
判断宿主安装状态。本轮没有重新采集宿主内存 JS 哈希，运行连接器版本由 health 回包确认。

原基础测试使用的是明确的夹具集，B03 本身创建新工程，并非所有动作都在单一 UUID。
本轮不能把这组历史证据套给任意新工程；选定目标后仍须检查目标页、当前基线及缺失基础能力。

## 目标与现场保护

- 原工程 `4037dd22434c412fa75d7f2d855a2ab4` 的三页原理图和 PCB 已逐页读取并 typed 保存；
  四个保存回包均 `saved:true`。记录为 `preflight/original-*-read/save.json`。
- `project find ceshi` 完整枚举 54/54，命中 `475cc0f773ed4a6fb7a02336c8a6a67f`。
  它是仓库记录的 AT32F415 基线，本轮未打开或写入该工程。
- 基础夹具 `0f46d4361c4741a9ab7a9ed62164ca9b`，名称 `ceshi-cli-basic-20260924`；
  P1 `e453c0063919726b` 只有图框、无导线；PCB `3037fc1dfa4965d2` 无器件、无板框。
- `preflight/fixture-pcb.json` 与历史 `B10/pcb-clean.json` 仅 `capturedAt` 不同，语义哈希为
  `41b23a1b3e7c54f623efa678dea430033717968444fefee90e1ccbfebce0d527`。
  空 PCB 的 `partial` 明确报告无器件/无板框，不能当作高级几何输入完整。
- 本轮没有新建/删除页或电路对象，没有安装连接器、重建运行二进制或重启 daemon。

## 预检误拦的复现与修复

`preflight/basic-fixture/01-health-before.json` 同时出现：目标工程的 dev.21 窗口，以及另一工程
的 1.2.8 窗口。全局 `versionGate.verdict` 为 block，旧脚本因此提前退出；目标本身仍精确同版。
旧失败证据保留，之后的两次目标快照均为只读诊断，不能写成旧预检通过。

修复 `scripts/cli-live-smoke.py`：CLI 与 daemon 精确版本仍逐项核对；按 project/doc/type
唯一匹配窗口，再检查该 connector 的精确版本和该 windowId 唯一的兼容宿主 finding。
完整 health 保留其他窗口的警告，不改变 daemon 或普通动作的执行规则。
`preflight/basic-fixture-targeted/summary.json` 记录修复后的真实目标 PCB 预检 pass。

回归覆盖：单目标、无关旧连接器/旧宿主、目标旧连接器、CLI/daemon 变化、缺失/重复目标、
错误工程/页/类型、缺失/重复/失败宿主 finding、缺失 windowId、daemon 不可用。
`offline/05-smoke-target-tests.json` 记录 9 项通过；`offline/06-skill-check.json` 记录公开 Skill 检查通过。

## 离线回归

| 命令 | 原始记录 | 结果 |
|---|---|---|
| `go test ./...` | `offline/01-go-tests.json` | pass；部分包使用 Go 缓存 |
| `make layout-calibrate` | `offline/02-layout-calibrate.json` | pass，7 个好板/负对照基准 |
| `make lint-test` | `offline/03-lint-test.json` | pass，朝向、连接规则和正负夹具 |
| `make blocks-audit` | `offline/04-blocks-audit.json` | 1028 unique；missing/unknown/fanout 均 0 |

这些测试不调用现场设计写入，不能补签原理图 gate、四层配置、实际铜、DRC 或最终保存回读。

另用**已安装 CLI**运行纯离线合成输入，记录位于 `offline-cli/`。输入来自源码单元测试的
最小模型，明确标为 synthetic，没有真实库身份或编辑器测量，不交给 ESP32 执行 Agent 当答案。

| 专项 | 实际检查 |
|---|---|
| 原理图分区与区内求解 | `zone-review`、`layout-plan --zones` 成功；源字节哈希、唯一归属和引脚状态保留 |
| 纸张与固定转换 | `layout-sheet-plan`、选定 `pages[0]` 后 `layout-render`、20 raw 间距的 `compose --layout-page` 成功 |
| Compose | 两模块正例通过；缺失 NC/未连接意图、引脚未到命名导线树、缺失 titleX 均拒绝 |
| 原理图其他负例 | 缺 mirror、双重归属、搜索预算耗尽、页面越界均明确失败；已有合法输出未覆盖 |
| PCB 二层联合布局 | `solve/check/render` 成功，1 个候选、18/200000 搜索状态；组内成员共同移动 60 mil |
| PCB 负例 | 篡改路由端点、错误 request provenance、过期 semanticSha256 均拒绝；错误渲染未覆盖原 SVG |
| 四层能力边界 | 联合求解返回 incomplete、exit 1，明确四层尚不支持；不能据二层成功签四层通过 |

保留两次测试输入错误：第一次将整份 sheet 包装报告传给 renderer，按 help 改为选中页后成功；
固定 Compose 的首次转换遗漏源数据已声明的 power/ground 角色，工具正确拒绝不完整连接。
仅在新的源副本补齐已声明角色后重算成功，没有修改产品或放宽检查。
独立复核还重跑 9 项 smoke 测试，并将保存的多窗口 health 按各自明确目标重放验证。
最终共 34 条命令记录：12 个正例、11 个预期拒绝负例、9 条版本/帮助检查，以及上述 2 次
输入转换错误。原理图源哈希、刚体几何、网络、归属和 NC 对账通过；17 个 SVG 可解析。
`offline-cli/README.md` 是命令表，`verification.json` 是数据校验结果，
`smoke-independent-review.json` 是预检独立复核。106 件文件的 `evidence-sha256.json` 哈希为
`942b4c00587c776129c91db3c7fabc6bac379cd0ce2534c7ee1b34d8ce80889d`。

`cost/preparation.json` 已记录本次准备/只读预检成本，不是完整 E2E 成本：daemon 审计
84 次调用、31.0 秒；首次至末次审计动作跨度约 9.36 分钟。报告整理和之后的纯离线命令不在
该动作跨度内；token 未记录，不能把审计中的 0 写成真实 token 消耗为零。

## 需求输入与未决项

执行 Agent 未继承对话历史，只读取 `esp32MiniRequire.md` 第一节及通用 Skill/器件官方资料。
提取的需求 SHA-256 为 `a7b3a16850d393e8d122b13b2c54b7ceebc3ed99ae4a1c594d0b83fb6678cd90`。
`A00/proposal.md` 与 `proposal-detail.md` 保存用户方案及来源，器件仍为候选，未声称已完成库核验。

工程选择和叠层含义已询问用户，尚无答复不能视为接受默认方案；原始需求、B00–B10 的范围
及 PCB Layout 用户确认要求均保持。后续每个 Axx 只按本轮实际证据给结论。
