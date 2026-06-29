# 对标 JLCEDA-MCP（sengbin/JLCEDA-MCP）

> 记录：2026-06-29。本地克隆：`~/github/easyeda-upstream/JLCEDA-MCP`（Apache-2.0，思路可借鉴）。

## 是什么 / 架构对照

第三方 MCP 路线的嘉立创 EDA 自动化，双扩展：

```
EDA(mcp-bridge) ↔WS↔ VSCode/Cursor(mcp-hub) ↔MCP↔ Copilot/Cursor/Claude Code/Codex
```

| 维度 | JLCEDA-MCP | 我们 |
|---|---|---|
| 桥接 | mcp-bridge ↔ mcp-hub（WS） | connector ↔ daemon（WS）—**同构** |
| 对接 AI | **MCP-native**（工具直给 IDE 内 AI） | CLI + Skill（agent 跑 CLI） |
| 自治度 | **人在环**：选型/放置侧边栏确认，电源地手动 | **全自动** agent 跑完整流程 |
| 工具数 | 少而高层（4 核心 + 4 透传） | 72 typed action（细粒度） |
| 逃生舱 | `api_invoke` 透传任意 API | `easyeda call` / `debug.exec_js`—**同构** |

核心工具：`schematic_read`（一次拿全语义快照）、`schematic_review`（全工程多页网表）、
`component_select`（搜+人确认）、`component_place`（交互放置）；透传：`api_index`/`api_search`/`eda_context`/`api_invoke`。

## 它比我们已实现部分做得更好的几处（改进点来源）

### ① 一次调用拿「电路语义全快照」 — `schematic_read`
器件列表 + **引脚→网络名映射** + 网络连接关系 + DRC 结果，**一个 JSON 全给**。
我们是 `components.list` / `nets` / `sch check` / `drc.check` 分开多次调。

### ② 几何法重建网表：坐标-BFS + Math.round 去抖
`schematic-read-handler.ts` 的做法：
- 种子 = 网络标志(VCC/GND, `getState_Net`) + 带网名的导线端点；
- 坐标→邻接图（key = `Math.round(x)_Math.round(y)`，**消除 324.9999 vs 325 浮点抖动**）；
- **BFS 沿导线传播网名**到所有相连坐标 → 再映射到引脚坐标。
- **完全基于内存状态、不需生成网表文件 → 刚放置/刚改的器件立即可见。**

对我们的意义：比我们 `sch check` 现在「从 `sch_Netlist.getNetlist()` 重建」更鲁棒——
内存即时、天然处理 wire-merge（[[wire-routing-merge-traps]]）。和 task #1(A7, JSON 权威源)
**互补**：A7=JSON 权威，这个=几何 BFS，两路交叉降误报漏报。

### ③ API 发现暴露给 AI — `api_index` / `api_search`
内置离线 API 文档 `/resources/jlceda-pro-api-doc.json`，AI 可运行时搜 `eda.*` 命名空间 +
方法签名/参数。我们只有硬编码 72 action + 逃生舱，agent **发现不了**目录外的 API。

## 改进建议（针对已实现部分，排序见正文 / 项目方向评估）

1. 加 `sch read` 单调用语义快照（合并 components+net+DRC）——agent 少跑几轮。
2. 用坐标-BFS 几何法加固 `sch check` 网表重建（内存即时 + merge 鲁棒）。
3. ~~**审计我们坐标比较是否都做了 round/容差**~~ ✅ **已审计(2026-06-29, task #6)：干净**——
   connector CHECK_EPS=0.05 / autoconnect acCoordEps=0.01 / layout round2 / grid_snap round /
   python round-key 全有防护，全仓无裸 `.x===` 相等。小提醒：容差三处不统一(非bug)；
   移植 BFS 进 Go 用 eps-grid 吸附而非裸 round()，避 .5 边界拆点。
4. 加 `easyeda api search <kw>`（吃官方 api doc）——加速「debug.exec_js→typed action」开发环 + 找盲区。

> 透传逃生舱(`api_invoke`)、WS 桥接、自建 connector 我们都已有/同构 → 再次验证方向。
