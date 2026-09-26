# CLI 功能索引

`easyeda` CLI 的功能地图入口——**只记最终功能形态**,按域分文档;每个动作都以 typed
Cobra 子命令暴露(`--help` 自描述),机器可读真值是 `easyeda actions` / `make actions`。

当前日常可用范围、已知限制和检查进度见 [CLI Status](../STATUS.md)；本索引的“已支持”表示有实现，
不表示该领域全部命令已现场验收。

本索引按 `sch`/`pcb` 等命令域组织，不表示验收层级。单对象增删改查、路由与持久化先按
[基础 CLI 测试](../cli-live-test.md)验收；求解、Compose/Apply、DRC 和整板结果在基础门禁
全部通过后按[高级 CLI 验收](../cli-advanced-test.md)检查。

| 域 | 状态 | 文档 | 一句话 |
|---|---|---|---|
| **原理图**(`easyeda sch` + `blocks`) | ✅ 已支持(40+ 子命令) | [schematic.md](./schematic.md) | 器件/连线/布局/持久编组/分区三件套/校验门/电路块库/导出,含布局质量五维打分(归因带可执行 fix) |
| **PCB**(`easyeda pcb` + `workflow`) | ✅ 已支持(50+ 子命令) | [pcb.md](./pcb.md) | 同步/布局/布线/铺铜/丝印/叠层规则/制造导出,九维布局诊断 + 兼容流程记录 |

## 通用约定(全域一致)

- **路由**:`--project <名>`(推荐,窗口重连不失效)或 `--window <id>`(同项目多窗口时必须);`--doc <页>` 钉住目标页防错页落子。
- **判对错看数据不看截图**:`list / check / drc / layout-lint / layout-score` 是判据;截图会 stale。
- **变更即校验**:mutate 前 inspect,mutate 后跑对应 lint/check；输出具体对象和差异，由 Agent 修正。
- **保存**:编辑只在内存,daemon 有防抖 autosave 兜底；稳定检查点仍需显式 `save`，最终 reload 后回读。

> 设计流程(何时用哪个命令、样例执行顺序)见
> [`.agents/skills/easyeda-agent/references/design-flow.md`](../../.agents/skills/easyeda-agent/references/design-flow.md);
> 全域 action 清单与实现状态见 [`docs/FEATURES.md`](../FEATURES.md)。
