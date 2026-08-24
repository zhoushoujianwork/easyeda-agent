# 原理图高质量布局 SOP（CLI）

> 本文只写可重复执行的 CLI 流程与硬坑。分区、间距、方向等视觉约定读
> [`schematic-layout-conventions.md`](./schematic-layout-conventions.md)；整板阶段门读
> [`design-flow.md`](./design-flow.md)。

## 核心原则

**按功能分页、按模块成簇、模块内短正交线、跨模块/跨页用命名 netport、电源地用
netflag、去耦贴 IC、框和文字按页保存。**

- 模块内部相邻引脚优先真实正交线；长距离、跨模块和跨页信号使用命名
  netport 短桩，避免长线穿越器件或与异网共线合并。
- netflag 只用于电源/地轨；普通信号不要伪装成电源 flag。
- 未使用引脚显式 `sch no-connect`，不要造零长 wire。
- 截图只做视觉终检；正确性以 `sch read/check/bridge-check/layout-lint` 为准。

## 先判断页面类型

### 新建或尚未布线的页面

1. **先落盘并严格校验 S0 spec，再创建/放置页面。** spec 至少冻结器件选型、统一网名、
   页面集合、模块归属和接口方向；文件时间必须早于首个 placement。不要用 `_NEW` 页面
   作为容量规划手段。
2. `easyeda sch sheet-geometry --json --doc <page>`：必须有真实 sheet bbox。
3. 按功能拆页并生成 module spec；`easyeda sch zones set --spec <spec>` 持久化
   （认领现在**只给布局前的 `sch autolayout` 指定落位格位**；分区框/说明的成员归属
   读的是虚拟组 —— `sch block-apply` 落块时已按功能子群自动归组）
   `modules[].page/zone/parts`。
4. 放置全部器件并读回真实 bbox/pins。优先 `sch block-apply`；其次在无
   wire/bus/marker 的页面运行 `sch autolayout --engine template --dry-run`，确认后
   `--apply --doc <page>`。
5. 每页通过 `sch layout-lint --strict --doc <page>` 后才布线。
6. 模块内部画短正交线；电源/地/netport 短桩优先用 `sch autoconnect`。
7. 逐页完成下方“功能框 + 文字标注”和“最终验证门”。

### 已连线、待整理的页面

**禁止运行 template autolayout；成品页也不要运行 official autolayout。** 移动器件而不
同步导线会破坏连通，自动重连也不能代替设计意图。

1. 固定目标页：所有读写命令都显式带 `--project` 与 `--doc <page>`。
2. 保存基线：
   - `sch read --doc <page>` 导出每个 `DESIGNATOR.pin → net`；
   - 记录显式 NC 引脚；
   - `sch check --json` 的 findings 从 `result.findings` 读取；
   - 保存当前 `sch bridge-check --json` 与 `sch layout-lint --json` 报告。
   - `sch read` 与 `sch list` 默认直接输出结构化 JSON，二者都没有 `--json` flag；
     只有 `sch check`、`bridge-check`、`layout-lint`、`zone-plan` 等命令显式使用
     `--json`。
3. 清理旧的 agent 功能框时只用 `sch zone-draw --clear --doc <page>`；不要按图元类型
   批量删除用户文字/图形。
4. 按模块小批移动：
   - 整簇优先 `sch group-move`，保持器件、局部线和 marker 的相对关系；同块多个
     子组用 `--groups g1,g2` 一次整体移动（逐子组移动会撕裂组间共享导线，已根治
     为一次内核调用；失败自动恢复到快照重连）；
   - 单件用 `sch modify`，成排用 `sch align`/`sch distribute`；
   - 需要断开时用 `sch disconnect`，处理返回的 `alsoDisconnectedPins[]` 后逐脚重连。
5. 每批修改后立即 `sch read` 对照黄金表；任何 pin→net、NC 集合变化都先修复，不带病
   进入下一批。每个通过的批次显式 `sch save`。
6. 全页完成后运行最终验证门；只有拓扑完全一致才可宣布“只是布局变化”。

### 每页事务与失败预算

每页只走一条状态线：`baseline → mutate → readback → cheap gate → save`。未过的页不进入
下一页，也不把多页问题混在一次大修里。

- 中间检查只跑 `sch gate --only layout-lint,check,bridge-check --doc <page>`；官方 DRC
  放到该页完成或最终验收时跑，避免每挪一个标签都重复最慢检查。
- `connect_pin`、`disconnect`、move、delete 等非幂等动作 fail/partial 后先读回活体；
  只重试明确失败项一次。第二次同类失败立即停止，保留输入、stderr 和 readback。
- `zone-arrange`、`destagger` 先 dry-run 一次，apply 最多一次；断言、网表或 bridge 变差
  时停止，不重放整批。
- 一个页面若需要第二次整体迁移，回到 S0/S1 重算分页，不再追加临时 `_NEW` 页。

## 功能框 + 文字标注（逐页）

先认领，再规划，再画：

```bash
easyeda sch zones set --spec s0.json --project <project>
easyeda sch zone-plan --json --doc <page> --project <project>
easyeda sch zone-draw --mode partition --font-size 22 --doc <page> --project <project>
```

`zone-plan` 的 `sheetOverflow`、`partitionOverlap`、`titleBlockHits`、
`moduleOutsideZone`、`labelCollisions` 必须全为 0 才落笔。多页逐页运行，绝不复用前台
页状态。`zone-draw` 的 rectangle/text ID 按 document UUID 记录；重画/清除只影响该页，
且成功必须返回 `schematic.save(saved:true)`。

## 画线与短桩硬规则

- 真实导线只走水平/垂直；不对齐用 L 路径
  `[x1,y1, x2,y1, x2,y2]`。
- 导线不得穿过无关引脚；EasyEDA 会在经过点截断并连接。
- 多脚网用 pin→pin 链式或命名 netport，不要把多条线汇到无锚点的空中 junction。
- 电源/地用 `sch connect`/`sch autoconnect` 生成“pin→非零短线→flag”，禁止 flag
  与 pin 坐标重合。
- 批量页面内部信号优先命名 netport 短桩；不要画贯穿整页的长线。
- 去耦先读 VCC/GND pin，再把电容放到 VCC 附近，以极短线连接，另一端接 GND。

## 多页和长流水线

- `doc switch` 返回不代表数据已稳定。读操作用 `sch read --page <page>`；所有 mutation
  用全局 `--doc <page>`，让 CLI 切页、确认当前文档并 fail closed。
- 每批重新读取 primitive ID；不要跨页复用 ID 或依赖隐式活动页。
- 超过约 50 次 mutation 时使用 typed `easyeda` action、`easyeda apply`、
  `scripts/bulk-place.py` 或 `scripts/bulk-connect.py` 分批执行并增量保存。
- 只有 typed action 缺失且用户明确接受时，才把 `debug.exec_js` 当临时调试逃生口；
  不把 raw JavaScript 写进生产 SOP。

## 数据与截图闭环

布局闭环：

`sch list/read → layout-lint --strict → modify/group-move → readback → save`

截图闭环：

`sch export-image`(渲染文档数据,无视口/stale 问题)。若导出失败,只报告图不可用,
不要据此否定数据结果。

`sch check --json` 使用统一信封
`{id,type,version,ok,result}`，findings 在 `result.findings`。不要按旧版裸对象解析。

## 最终验证门（每页）

多页工程优先运行：

```bash
python3 <skill>/scripts/schematic-acceptance.py \
  --project <project> --spec <s0.json> --golden <pin-net.json> \
  --export --artifacts .easyeda/artifacts
```

脚本只读取一次页表，随后串行显式打开每页，执行 strict gate、权威 `sch read`、全工程
`sch nets --strict`、黄金 pin→net/NC 对账和可选导出，避免手工循环漏页。

1. `easyeda sch gate --strict --doc <page>`：通常 **`verdict` 必须是 `pass`**。一条命令按固定顺序
   跑完四关(layout-lint 0 overlap/0 pin-coincidence + check 结构与 marker findings 0 +
   bridge-check 0 bridge、orphan 解释或清理 + drc fatal/error 0),报告里带每关的计数;
   DRC 聚合 WARN 仍需审阅并报告。
   - `verdict=blocked` **不是板子的问题**——检查器没跑起来(连接器断/页没打开),
     先 `easyeda health` + `doc switch <page>` 修环境再重跑,别去改电路。
   - 窗口不在前台时先 `--skip drc` 过前三关,DRC 单独补跑。
   - CLI/connector v1.1.1 若仅被 `missing-titleblock` 阻塞，可在 acceptance 脚本上显式加
     `--allow-titleblock-gap` 挂账；脚本会拒绝任何其他版本、stage 或 finding。不要执行
     gate 输出里的 `sch titleblock --data`，该写路径会损坏 sheet 引用。
2. `easyeda sch read --doc <page>`：与设计 spec 或整理前黄金
   `DESIGNATOR.pin → net`、显式 NC 集合逐项一致。**gate 判「合不合法」,这步判「对不对」**,
   两者都要。
3. `easyeda sch save --doc <page>` 明确返回 `saved:true`。

任何一门失败都回到布局/布线阶段修复，不用截图“看起来正常”替代门禁。
