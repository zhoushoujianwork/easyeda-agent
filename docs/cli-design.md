# CLI Design — Cobra Subcommand Constraint

## 核心规则

所有明确的功能模块**必须以 Cobra 子命令方式暴露**，禁止把功能藏进全局 flag 或隐式行为里。

## 子命令层级

```
easyeda <domain> <action> [flags]
```

| 顶级子命令 | 职责 |
|---|---|
| `easyeda sch` | 原理图操作（place / wire / netflag / drc / save / export …） |
| `easyeda pcb` | PCB 操作（layout / line / via / import / align …） |
| `easyeda bom` | BOM 导出与补全 |
| `easyeda lib` | 器件库搜索与选型 |
| `easyeda daemon` | 守护进程管理（start / health） |
| `easyeda audit` | 操作日志查看 |
| `easyeda debug` | 逃生舱（exec-js 等开发/调试命令） |

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

## 现状（截至 2026-06）

| 阶段 | 状态 |
|---|---|
| ① `debug.exec_js` | 完整支持（逃生舱始终可用） |
| ② Typed actions | 34 个：原理图 20 + PCB 13 + debug 1 |
| ③ Cobra 子命令 | **尚未建立**，所有功能目前通过 `easyeda call <action>` 裸调 |
