# 基础 CLI 真实链路测试

## 测什么

本测试只验证 `easyeda` 的基础操作是否可靠：命令解析、CLI → daemon → Web 连接器 →
官方 API 的调用、工程/页面路由、单个对象的增删改查、导出、保存重载和错误请求拒绝。
例如放置一个器件后能读回同一对象，或写错 `--doc` 时没有对象变化。

布局求解、Compose/Apply、自动布线、DRC 结果和需求到成品属于
[高级 CLI 业务验收](cli-advanced-test.md)。它们的成败**不计入基础 CLI 结论**。
基础用例必须全部通过，才开始高级验收；同一个 `sch`/`pcb` 命令域中也可能同时有两层能力。
分类依据见 [CLI 设计](cli-design.md#验收分层基础动作先行)。

## 怎么跑

先在专用 Web EDA 测试工程运行[只读预检脚本](../scripts/cli-live-smoke.py)：

```bash
python3 scripts/cli-live-smoke.py --expected-version vX.Y.Z-dev.N \
  --project <测试工程UUID> --doc <原理图页UUID> --type schematic \
  --out <本地证据目录>
```

预检通过后，Codex 执行员按[基础动作的前置条件与步骤](cli-live-test-detail.md)串行测试；
独立复核员只读核对原始命令回包、对象差分和保存重载后的新鲜回读。
`pass` 只授予实际完成的用例；空白页的成功列表回包仅证明路由可用，不证明对象写读。

**一次只推进一个 Bxx 单项**。每项开始前核对其前置条件，结束时立即产出一份独立报告，
写明版本/目标、执行命令、原始证据路径、通过判据、实际结果、故障归因的确定程度、
清理状态和下一步；随后在聊天中给出该项结论。总表只索引单项报告，不能用合并叙述
掩盖哪一步失败。某项在新版没有重跑，就写 `not-run`，不沿用旧版 `pass`。

## 放行条件

基础用例 B00–B10 均为 `pass`，且版本、目标页、对象差分、保存重载和保护负例有证据，
才可标记“基础 CLI 门禁通过”。`fail` 表示真实动作未满足契约，`blocked` 表示前置条件或
判据尚未满足，`not-run` 表示该版本未执行，`partial` 表示只完成部分步骤；四者都不放行，
但不能合并计作“四个产品故障”。离线 Go/连接器测试和 `--help` 检查不能代替真实调用链。

历史 [Codex subagent 原理图验收记录](reviews/2026-09-23-v1.6.0-schematic-acceptance.md)
及 [dev.14 测试重分类](reviews/2026-09-24-v1.6.0-dev14-cli-gates.md)、
[dev.14 基础现场复测](reviews/2026-09-24-v1.6.0-dev14-basic-cli-live.md)按各自版本和覆盖范围阅读，
不能把其中的求解或 DRC 结果合并进基础门禁。
