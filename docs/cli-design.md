# CLI Design — Cobra Subcommand Constraint

## 核心规则

所有明确的功能模块**必须以 Cobra 子命令方式暴露**，禁止把功能藏进全局 flag 或隐式行为里。

## 子命令层级

```
easyeda <domain> <action> [flags]
```

| 顶级子命令 | 职责 |
|---|---|
| `easyeda sch` | 原理图操作（connectivity / plan / apply / place / wire / drc / save / export …） |
| `easyeda pcb` | PCB 操作（layout / line / via / import / align …） |
| `easyeda pcb config` | 当前 PCB 配置：get / clearance / track / via / bind；局部参数修改、单位换算、dry-run 和真实回读 |
| `easyeda bom` | BOM 导出与补全 |
| `easyeda lib` | 器件库搜索、符号/封装/Device 资产创建与选型 |
| `easyeda daemon` | 守护进程管理（start / stop / restart / health；restart 与 start 同为前台阻塞） |
| `easyeda web` | Web 编辑器页面生命周期；`reload` 与 `doc reload` 区分，保存后等待新连接和同一文档可读 |
| `easyeda audit` | 操作日志查看 |
| `easyeda update` | 自更新（别名 `upgrade`）：CLI 二进制 + skill 目录 → latest；连接器只报不改 |
| `easyeda skill` | skill 目录单独管理（status / sync；`update` 已含其能力） |
| `easyeda debug` | 逃生舱（exec-js 等开发/调试命令） |

## 设计图交付接口

- `pcb export-3d --format obj|step --window <id> --out <新目录>`：官方整板模型，
  前后完整 `pcb dump --include-copper` 等价回读核对身份与版本，保留模型原文件、回读与
  manifest。显式窗口必需；只读，不隐式保存重载，默认不生成缺模型占位。模型绑定不证明导出覆盖。
- `pcb 3d models|transform|bind|export-shell`：器件 3D 模型清单、摆放修正、实例换绑与外壳导出，
  分阶段实施，见 [PCB 3D 建模设计](pcb-3d-design.md)。

- `pcb layout render --actual --board <dump.json> --side top|bottom|both --out <图.svg>`：实际回读辅助图；与候选输入互斥，不执行 EDA 动作。
- `project drawings --from <drawings.json> --out <新目录>`：纯离线组合一页原理图与一块 PCB 的图册，核对文件/回读绑定并保留状态声明。`status.pcbReview` 分开声明网名密度、文字碰撞与铜几何（`pending|pass|fail`，缺省 pending）；声明设计已验收须三项明确 pass，打包不自动检查重叠。
- `doc reload --json`、`pcb dump`、`pcb stage-snapshot` 保留各自职责；重载凭据、采集完整性和设计验收不是同一返回状态。

输入、图源与朝向见[清晰设计图交付](../.agents/skills/easyeda-agent/references/design-drawings.md)。

## 验收分层：基础动作先行

`sch`、`pcb` 等 Cobra 域同时包含基础动作和高级业务能力，不能按顶级命令名判断测试层级。

| 层级 | 验证的问题 | 例子 |
|---|---|---|
| 基础 CLI | 命令解析、版本/连接、工程和页面路由、单个 typed 对象的增删改查、导出、保存重载及错误页拒绝是否可靠 | `project/doc`、`lib search`、`sch place/list/modify/wire/save`、`pcb list/track/via/save` |
| 高级 CLI | 从需求和参数推导方案、跨对象规划/求解/Apply、设计正确性与整板质量是否达标 | `sch layout-plan/compose/apply/gate`、`pcb layout solve/route`、DRC、完整 S0–S6/P0–P10 |

同一基础命令可以在高级流程中复用，但基础验收只核对该动作的输入、回包、对象差分与持久化，
不以求解结果、DRC 警告或整板完成度判其成败。必须先让[基础 CLI 真实链路测试](cli-live-test.md)
全部通过，才进入[高级 CLI 业务验收](cli-advanced-test.md)；高级测试结果不能补签基础门禁。
用户明确接受的限定范围可以另行决定本次准入，记录方式见[基础验收放行条件](cli-live-test.md#放行条件)；
未解决问题继续如实列出，不能把“允许继续”写成对应基础动作已经通过。

## 线条形泪滴维护接口

线条形泪滴维护使用独立的离线子命令：`pcb teardrop-delete-plan --board <新回读> --from <旧计划> --out <删除计划>`、
`pcb teardrop-modify-plan --board <新回读> --from <旧计划> --length-scale <比例> --base-scale <比例> --out <新计划>`。
修改比例相对于当前焊盘/主走线推导的默认几何，保持线宽；只重建旧计划中指定的入口。
两者复用 typed 删除/创建剧本，计划匹配不完整、锁定或存在歧义时拒绝输出执行剧本；旧 `teardrop-plan --remove-from` 保留兼容。

## 设计约束

1. **接口优先**：新增功能先设计子命令签名（命令名 + flags + `--help` 示例），再写实现逻辑。
2. **`--help` 自描述**：`--help` 输出必须包含参数说明和调用示例，AI 读 `--help` 即可调用，无需看源码。
3. **Skill 同步**：子命令签名稳定后，对应 Skill 里的工具描述和示例必须同步更新。
4. **禁止隐式行为**：每一个明确的操作都是一条显式子命令；不允许通过全局 flag 或位置参数区分语义。

## 开发闭环

新功能按以下三步推进，不要求一次到位：

```
① debug.exec_js        →   ② typed action         →   ③ Cobra 子命令
  (探索/验证 API 行为)        (固化到 protocol/)          (--help 自描述)
```

- **① → ②**：确认 API 行为正确后，在 `internal/protocol/actions.go` 注册 typed action。
- **② → ③**：功能稳定后，包装成对应的 Cobra 子命令；Skill 描述同步更新。
- 允许功能停留在 ② 阶段通过 `easyeda call <action>` 裸调，但 ③ 是最终形态。

## 1.4 当前接口

原理图以 `sch connectivity/design-diff/designators/lib-layout/compose/frame/apply` 组织数据、规划与执行，
PCB 保持在 `pcb` 域。CLI → daemon → connector 是唯一运行链路，不设 Broker 层。
`easyeda actions` 与各子命令 `--help` 提供当前完整清单，不在文档重复登记数量。

数据转换和受保护队列的边界见
[原理图数据与 SCH Apply](../.agents/skills/easyeda-agent/references/schematic-data.md)。
新版本的 Skill、命令示例与实际参数必须一起核对；发布准备见
[1.4 发布准备](releases/release-1.4.md)。
