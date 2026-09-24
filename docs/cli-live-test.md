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

## 放行条件

基础用例 B00–B10 均为 `pass`，且版本、目标页、对象差分、保存重载和保护负例有证据，
才可标记“基础 CLI 门禁通过”。任一项为 `fail`、`blocked` 或 `not-run`，
停止高级 CLI 的现场检查。离线 Go/连接器测试和 `--help` 检查不能代替真实调用链。

历史 [Codex subagent 原理图验收记录](reviews/2026-09-23-v1.6.0-schematic-acceptance.md)
及 [dev.14 测试记录](reviews/2026-09-24-v1.6.0-dev14-cli-gates.md)按各自版本和覆盖范围阅读，
不能把其中的求解或 DRC 结果合并进基础门禁。
