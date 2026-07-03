# 设计:`easyeda apply` — 声明式步骤回放(playbook)

> **动机**(esp32MiniRequire 探针轮次 #1 实证):完整画一块板,agent 写了 10 个一次性
> bash 脚本,内容全是同构胶水——循环调 `easyeda` 子命令、记日志、防超时、断点续跑。
> 这层编排该内置:**一份 JSON 步骤文件 + `easyeda apply` 按步执行**,用户/agent 不再写
> 任何 shell/python 组合脚本;同一份文件即是「复现脚本 + 回归用例 + 教学示例」。

## 命令接口

```bash
easyeda apply steps.json                    # 顺序执行,自动写 journal,失败即停
easyeda apply steps.json --dry-run          # 只校验格式/变量/动作名,打印计划
easyeda apply steps.json --resume           # 按 journal 跳过已完成步骤,断点续跑
easyeda apply steps.json --from 12 --to 30  # 区间执行(调试单段)
easyeda apply steps.json --yes              # 放行确认门控步骤(delete/clear/import)
easyeda audit export --playbook > replay.json   # ★ 从真实会话的审计日志生成 playbook
```

## 文件格式(v1,定稿)

### 顶层结构

```jsonc
{
  "version": 1,                       // 必填,格式版本
  "meta": {
    "name": "esp32-mini-pcb",         // 必填,journal/报告里的标识
    "description": "P0-P10 全流程",    // 可选
    "project": "ceshi",               // 目标工程(名字或 uuid)——可被 CLI --project 复写
    "window": "",                     // 可选,窗口 id(细控)——可被 --window 复写
    "doc": "PCB1"                     // 可选,开跑前先 doc switch 到此文档
  },
  "defaults": {                       // 可选,所有步骤的默认执行策略
    "timeoutSec": 20, "retry": 0, "continueOnError": false
  },
  "vars": { "LIB": "0819f05c…" },     // 变量表——可被 CLI --var K=V 复写/新增
  "steps": [ /* 见下 */ ]
}
```

### 复写优先级(用户显式要求,定稿)

**CLI flag > playbook 文件 > 内置默认**,逐项:

| 项 | 文件里 | CLI 复写 | 说明 |
|---|---|---|---|
| 目标工程 | `meta.project` | `--project` | 同一份 playbook 可打到不同工程(复现/回归的关键) |
| 窗口 | `meta.window` | `--window` | 细控场景 |
| 变量 | `vars.*` | `--var K=V`(可重复) | **参数化 playbook**:坐标偏移、器件选型都可做成变量 |
| 步骤策略 | `defaults.*` / 步内字段 | `--timeout` `--retry` | 步内字段 > CLI > defaults(步内是作者意图,最高) |
| 确认门控 | 步内 `confirm:true` | `--yes` 整册放行 | 与现有 CLI destructive 门控语义一致 |
| journal 路径 | — | `--journal <path>` | 默认 `<playbook>.journal.jsonl` |

### 步骤字段参考

```jsonc
{
  "id": "place-u1",          // 可选;缺省 = "s<序号>"。resume/区间执行按它定位
  "name": "放置主控",         // 可选,人读注释
  // ↓ 二选一(互斥)
  "action": "schematic.component.place",   // typed action(daemon 校验 payload)
  "run": "pcb auto-place",                  // Cobra 子命令(复合工具层)
  // ↓ 参数(均支持 ${var} 替换;action 用 payload,run 用 flags/args)
  "payload": { "libraryUuid": "${LIB}", "x": 760, "y": 430 },
  "flags":   { "assembly-gap": 40, "dry-run": false },   // → --assembly-gap 40
  "args":    ["P1"],                                      // 位置参数(如 doc switch P1)
  // ↓ 结果取值 → 变量(JSONPath,作用于该步 JSON 结果的 result 体)
  "capture": { "U1": "$.primitiveId" },
  // ↓ 门禁(JSONPath: 判定式;全过才算步骤成功)
  "assert":  { "$.overlaps": "==0", "$.score": ">=95" },
  "onFail":  "stop",         // stop(默认)| continue | prompt(交互询问)
  // ↓ 执行策略(覆盖 defaults)
  "timeoutSec": 60, "retry": 2, "continueOnError": false,
  // ↓ 其他
  "confirm": true,           // 执行前询问(--yes 放行);delete/clear/import 类自动置真
  "checkpoint": true,        // 语义标记:此步后进度已落盘(报告里高亮)
  "notify": "P3 完成"        // 纯提示步(与 action/run 互斥):easyeda notify 弹给用户
}
```

**判定式语法**:`"==N" "!=N" ">=N" "<=N" ">N" "<N" "==字符串" "exists" "true" "false"`。
**变量替换**:任何字符串值里的 `${NAME}`;未定义即该步硬错(`--dry-run` 预检能查出
纯静态未定义;依赖 capture 的推迟到运行时)。**无条件分支、无循环**(见设计决策 1)。

### journal 与断点续跑

`<playbook>.journal.jsonl`,首行头 `{playbookSha256, startedAt, project}`,其后每步一行:

```json
{"idx":3,"id":"place-u1","status":"ok","ms":1240,"captured":{"U1":"be7f…"},"digest":"…"}
```

- `--resume`:跳过 journal 中 `ok` 的步骤,**captured 变量从 journal 恢复**(否则后步引用断链);
- playbook 内容变更(sha 不匹配)→ 拒绝 resume,提示 `--from` 手动定位;
- 退出码:0 全过;1 有步骤失败;2 格式/预检错误。执行中每步打印
  `[3/60] place-u1 … ok (1.2s)`,`--quiet` 静默,收尾输出汇总(含 journal 路径)。

### 完整示例(本轮实战原理图阶段的等价物,节选)

```jsonc
{
  "version": 1,
  "meta": { "name": "esp32-mini-sch", "project": "ceshi", "doc": "P1" },
  "vars":  { "LIB": "0819f05c4eef4c71ace90d822a990e87" },
  "steps": [
    // ① typed action + 结果取值存变量
    { "id": "place-u1", "action": "schematic.component.place",
      "payload": { "libraryUuid": "${LIB}", "uuid": "ebc5227e…", "x": 760, "y": 430 },
      "capture": { "U1": "$.primitiveId" } },
    // ② 引用前步捕获的变量(id 每次会话会变,静态脚本无法跨会话复现)
    { "id": "desig-u1", "action": "schematic.component.modify",
      "payload": { "primitiveId": "${U1}", "patch": { "designator": "U1" } } },
    // ③ CLI 复合命令层
    { "id": "autoconnect", "run": "sch autoconnect",
      "flags": { "spec": "connect.json" }, "timeoutSec": 300 },
    // ④ 门禁:失败即停
    { "id": "gate-lint", "run": "sch layout-lint", "flags": { "json": true },
      "assert": { "$.overlaps": "==0" }, "onFail": "stop" },
    // ⑤ 检查点存盘 + 提示
    { "id": "save-1", "action": "schematic.save", "checkpoint": true },
    { "id": "note", "notify": "S3 放置完成,进入布线" }
  ]
}
```

调用与复写示例:

```bash
easyeda apply sch.playbook.json                       # 按 meta.project=ceshi 跑
easyeda apply sch.playbook.json --project demo2       # 同一份打到另一工程
easyeda apply sch.playbook.json --var LIB=其他库uuid   # 参数化复写
easyeda apply pcb.playbook.json --resume              # 断点续跑
```

## 关键设计决策

1. **刻意不做编程语言**——无条件分支、无循环。60 行数据就是 60 步。生成侧(agent/
   audit 导出)负责展开循环;回放侧保持傻瓜化、可 diff、可断点。这是与"再写一门脚本
   语言"的本质区别。
2. **双层寻址**:`action:`(typed action,daemon 校验 payload)+ `run:`(Cobra 子命令
   层)。缺一不可——复合工具都在 CLI 层。
3. **变量捕获是复现的命门**:primitiveId/坐标每次会话都变(load-bearing gotcha:
   pull fresh pids before mutating),`capture` + `${}` 替换让同一份文件跨会话可复现。
4. **journal 即状态**(`<file>.journal.jsonl`,每步一行 id/status/耗时/结果摘要):
   `--resume` 跳过已完成;超时/崩溃后原地续跑——本轮 place 阶段 2 分钟超时被迫改后台
   脚本的问题从根上消失。
5. **审计日志 → playbook 导出**是杀手级闭环:探索性会话跑完,
   `easyeda audit export --playbook` 直接得到干净步骤文件 → 提交为回归用例。
   esp32 案例可固化为 `examples/esp32-mini/{schematic,pcb}.playbook.json`。
6. **确认门控延续**:destructive 步骤(delete/clear/import_changes)默认逐步询问,
   `--yes` 整册放行(与现有 CLI 门控语义一致)。
7. **平台坑封装成"宏步骤"**:如 `run: pcb via-hop`(#31 的 fill 键合 workaround)、
   PLANE 翻转配方——playbook 引用宏,不要求用户知道坑。

## 实现落点

- `internal/app/cmd_apply.go`:解析 + 变量替换 + journal + 逐步分发(action → daemon
  `/action`;run → 进程内调用对应 Cobra 命令,复用既有实现,零重复)。
- `internal/app/cmd_audit.go`:`audit export --playbook`(审计条目 → 步骤,过滤只读
  action,合并 save)。
- Skill 同步:`references/actions.md` 增补 apply 章节;design-flow 各阶段附
  「可导出为 playbook」提示。
- 回归:`make lint-test` 加 playbook 格式 fixture;examples/ 下放 esp32 案例双文件。

## 验收(探针轮次 #2 的一部分)

用两份 playbook(sch + pcb)从零重放 esp32MiniRequire 全程,人工零脚本,
门禁步骤全过 → 即宣告 B 列「批量编排」缺口关闭。
