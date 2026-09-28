# SCH Apply 队列与状态守卫

设计与修复遵守 [数据驱动架构基准](../.agents/skills/easyeda-agent/references/schematic-data.md#数据驱动架构基准)。
队列是源目标经算法生成的派生产物，不能手改坐标/断言绕过失败；普通队列的低层能力
不扩大用户授权，也不能替代可重复的源数据闭环。

`easyeda sch apply` 顺序执行版本化 JSON 队列，等待每步 WebSocket 响应，捕获结果并记录
journal。布局计算和差异判断在生成侧完成；Apply 负责执行和回读验证，不提供事务回滚。
Lib 组合入口见 [单页组合](schematic-page-composition.md)。

## 命令与执行范围

```bash
easyeda sch apply steps.json --dry-run  # 本地预检并打印步骤，不写 EDA
easyeda sch apply steps.json --yes      # 执行已授权的完整队列

# 仅适用于未启用完整执行守卫的普通队列
easyeda sch apply steps.json --resume
easyeda sch apply steps.json --from 12 --to 30
```

`sch plan`、`sch compose` 等生成的受保护队列必须完整执行：
`requireFullExecution:true` 或包含 `expectedConnectivity` 时，禁止 `--resume`、
`--from/--to` 和更换目标工程/页面。失败后读取真实状态并重新生成完整队列，不能跳过前置检查。
复用已正确放好但未接线的器件也是重新规划，不是断点续跑。

普通队列的 `--project`、`--window`、`--doc` 可覆盖 `meta`，`--var K=V` 可重复设置变量。
步骤内执行策略优先于 `defaults`；不要通过目标覆盖或手改守卫将旧快照应用到其他页面。

## 文件格式 v1

```json
{
  "version": 1,
  "requireFullExecution": true,
  "meta": {
    "name": "power-composition",
    "project": "<project-uuid>",
    "doc": "<page-uuid>"
  },
  "defaults": {"timeoutSec": 90, "retry": 0, "continueOnError": false},
  "vars": {},
  "steps": [
    {
      "id": "verify-unwired-page",
      "action": "schematic.components.list",
      "payload": {"includePins": true, "includeWires": true, "includeConnectivitySummary": true},
      "assert": {
        "$.connectivitySummary.scope": "==activePage",
        "$.connectivitySummary.wires": "==0",
        "$.connectivitySummary.buses": "==0"
      }
    },
    {
      "id": "save",
      "action": "schematic.save",
      "assert": {"$.saved": "true"},
      "checkpoint": true
    }
  ]
}
```

上例展示格式和运行时计数检查；实际组合队列还必须包含下述完整器件、引脚与绘图守卫。
`meta.window` 可选。每步使用 `action` + `payload`、`run` + `flags/args` 或 `notify`
三者之一；`run` 是 CLI 子命令，例如 `sch gate`，不执行 shell。

| 步骤字段 | 用途 |
|---|---|
| `id` / `name` | 稳定步骤 ID / 可选说明；未给 ID 时使用步骤序号。 |
| `capture` | 将结果中的值存为变量，例如 `{"C1_PID":"$.primitiveId"}`。后续用 `${C1_PID}` 引用。 |
| `literalPayloadKeys` | 仅 `action` 步骤可用；列出顶层 `payload` 中应逐字传送的键，例如 `expectedPagePrimitives`。这些值不做 `${...}` 变量替换，用于保护包含用户原始属性的完整快照；缺失或重复键在 preflight 被拒绝。 |
| `assert` | 对结果断言；字段缺失或不满足条件即失败。 |
| `timeoutSec` / `retry` / `continueOnError` | 覆盖默认执行策略；变更动作失败不自动重试。 |
| `confirm` | 确认门控，`--yes` 放行；清除/删除等动作默认需要确认。 |
| `checkpoint` | 日志语义标记，本身不会保存；需要真实 `schematic.save` 步骤。 |
| `verify` | 普通步骤失败后执行的只读核对；成功可将原步骤记为 `ok(verified)`，不能绕过强制状态守卫。 |

`capture/assert` 路径相对于响应的 `result`，支持 `.key` 和 `[index]`，不支持筛选表达式。
判定式支持数值比较、`==字符串`、`exists`、`true/false` 和 `len==N/len>=N/len<=N` 等长度比较。
除显式 `literalPayloadKeys` 外，字符串值支持 `${变量}`；没有条件分支或循环，生成侧须先展开步骤。

## `expectSchematic`：完整回读约束

只用于 `schematic.components.list` 且要求 `includePins:true`。按位号和引脚编号匹配，
与数组返回顺序无关；每个被检查器件必须列齐全部引脚。

```json
{
  "action": "schematic.components.list",
  "payload": {"includePins": true, "includeBBox": true},
  "expectSchematic": {
    "exactParts": true,
    "parts": {
      "C1": {
        "primitiveId": "${C1_PID}", "x": 400, "y": 250,
        "rotation": 90, "mirror": false,
        "pins": {
          "1": {"x": 400, "y": 270, "net": "+3V3", "noConnected": false},
          "2": {"x": 400, "y": 230, "net": "GND", "noConnected": false}
        }
      }
    }
  }
}
```

| 字段 | 检查规则 |
|---|---|
| `exactParts:true` | 拒绝额外器件；sheet、flags 不计入器件集合。 |
| `parts` | 必填 map。可选 `primitiveId/x/y/rotation/mirror/bbox`；几何容差 `1e-6`，检查 bbox 时必须 `includeBBox:true`。 |
| `pins.*.net` | 省略表示本步不检查网络，可用于接线前几何检查。最终验收应逐脚写明 net/NC。 |
| `pins.*.noConnected` | 显式布尔值；已知空网 `net:""` 必须配合它。`true` 与非空 net 冲突，`net:null`、`noConnected:null` 无效。 |
| `absentParts` | 列出的位号必须在本次观察范围内不存在；不能为空位号、重复或与 `parts` 冲突。 |
| `drawing:{wires,flags}` | 比较真实导线路径、标记位置与方向，要求 `includeWires:true`；同网但绕线不同会失败。 |

空 `parts:{}` 有两种合法含义：

```json
{"exactParts": true, "parts": {}}
```

检查器件集合为空。清页仍须额外运行 `sch clear --dry-run --expect-empty`，检查导线、
标记、图形等全部图元；枚举失败或残余图元不能当空页。

```json
{"parts": {}, "absentParts": ["U1", "C1"]}
```

只要求这些位号不存在，允许其他器件存在。Compose 使用全页 inventory 检查新增位号未被
其他页占用，并保存目标页已有器件的完整基线；目标页另用 `exactParts:true` 严格核对。
既没有 `exactParts:true` 又没有非空 `absentParts` 的空 `parts` 会被拒绝。

`reuseUnwired` 同时要求完整的器件/引脚几何匹配、空 `drawing`、无冲突 NC，及活动页
`connectivitySummary.wires/buses` 均为零。执行时再次回读并断言 summary；缺失、范围错误
或出现新导线/总线即停止，不能只信编译时快照。CLI `sch list --include-wires` 同时读取 summary。

`expectSchematic` 的读取或比对失败必须停止并写 journal；`retry`、`verify`、
`onFail:continue` 和 `continueOnError` 不能绕过它。`expectedConnectivity` 同样是强制连接守卫。

## 失败与恢复

journal 默认写入 `<playbook>.journal.jsonl`，头部记录文件哈希与目标，逐步记录 ID、
状态、耗时、错误和捕获变量。执行日志可定位停止在哪一项；它不撤销已经落地的动作。

只读步骤可按策略重试临时故障；变更步骤超时可能已经生效，不自动重复写入。
先保存 journal、回读目标，确认器件、引脚、线和标记的真实状态，再决定修正数据或重新规划。
普通队列的 `--resume` 会恢复已成功步骤的捕获变量，并拒绝文件哈希变化；受保护队列始终
重新生成并完整执行。不要把离线预检成功、WebSocket 返回成功或保存成功单独写成最终验收通过。

器件期望可带 `device:{libraryUuid,uuid}`，需 `includeDeviceIdentity:true` 读取水合后的器件库身份；同符号不同料号不算匹配。`drawing` 还需 `includeConnectivitySummary:true`，当前页 buses/shortSymbols 必须明确为零。
