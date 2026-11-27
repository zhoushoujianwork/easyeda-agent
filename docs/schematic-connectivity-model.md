# 原理图连接数据模型（1.4）

本页定义电气事实；设计、布局和修复流程统一遵守
[数据驱动架构基准](../.agents/skills/easyeda-agent/references/schematic-data.md#数据驱动架构基准)。
原始快照保留，目标副本明确核心/外围所有权；网表一致不等于归属、真实直连或布局已验证。

1.4 将电气连接事实与图面布局分离。器件、引脚、网络及引脚到网络的关系稳定；坐标、旋转、线段、框线和文字可以重排。

## Connectivity IR

- `component`：稳定实例 `id`、显示位号 `ref`、可选功能 `role`、`libraryUuid`、`deviceUuid`、封装和属性；
  多单元器件的各已放置单元记录在 `subParts` 中；
- `pin`：所属器件、pin number、名称、方向和电气类型；明确 NC 为 `noConnected:true`，已知悬空为 `connectionState:"unconnected"`；
- `net`：项目内稳定 `netId`、规范名称、作用域和角色；1.4 快照会把电源/地识别为
  `scope=global`（`role=power|ground`），普通信号识别为 `scope=local`、
  `role=signal`。这是规划提示，不改变 `pin_net` 的权威性；未知命名仍保守归入
  `local/signal`。
- `pin_net`：`{componentId,pinNumber,netId,connectionKind}`；
- `edge`：真实 wire、端口、标签等图面证据；
- `module`：可复用 Lib 的核心器件、外围器件、内部网和对外端口。

`primitiveId` 和几何数据属于布局层，不得成为连接依据。保存、导入 PCB、DRC 和回归测试均以 `pin_net` 对账。

无损重建可保留已知悬空脚：只有原生快照明确给出空字符串网络与 `noConnected:false`
才自动记录该状态；它与连接和 NC 互斥。悬空警告仍保留，缺失证据仍拒绝，
数据快照完整不等于电气设计完成。详见[已知悬空与未知引脚](concepts.md#已知悬空与未知引脚)。

`id`、`ref`、`role` 的含义见[共享词汇表](concepts.md#器件身份位号与功能角色)。
实例通过 `otherProperty["EasyEDA Agent Component ID"]` 保存 canonical ID，通过
`otherProperty["EasyEDA Agent Role"]` 保存可选功能角色；重新导出优先读取绑定。
未绑定的历史图兼容读取旧 `cmp-<ref>` ID，首次修正位号时先保存该 ID，之后不得
因显示位号变化而重建它。原生 `uniqueId` 不作为可覆盖字段。

历史非标准位号在 JSON 快照中产生 `nonstandard-designator` 诊断，仍允许读取和修复。
`compose` 与 `materialize` 在生成放置队列前拒绝此类位号及未分配的 `?`，防止把
功能名称再次写回画布。修复以全工程数据分配编号：按官方库前缀、已有数字占用和
输入声明顺序，只修复非标准项；不得全量重新编号正常器件。

一颗多单元器件在 EasyEDA Pro 里被放置成多条记录：每个功能单元一条 `subPartName`，
它们共享原生 `uniqueId`、重复同一个显示位号，PCB 上只对应一个封装。因此位号字符串
不能单独标识器件实例，也不能据此判重。

- 一个器件实例导出为一个 canonical component：引脚取各单元的并集，引脚编号在实例内
  必须唯一；每个已放置单元的 `primitiveId`、`subPartName`、`uniqueId` 与几何保留在
  `component.subParts`，器件级 `placement` 是首个已放置单元的位置。
- 实例归并按原生 `uniqueId` 或 agent 写入的 canonical 绑定进行；两者都缺失的记录
  各自独立，不会与其它记录合并。
- 工程级位号唯一性按器件实例判定：同一实例重复位号不是冲突，两台不同器件共用一个
  位号仍然是冲突，一个器件实例也不得跨页。

详见[多单元器件（子部件）](concepts.md#多单元器件子部件)。

## Lib 复用

每个框选功能电路登记为一个 Lib 实例：核心器件、外围器件、内部真实连线和 typed ports。人体存在板可拆成 Type‑C、反倒灌、DC‑DC、ESP32、传感器、按键、RGB/蜂鸣器等模块。复制时只生成新的实例 ID，拓扑模板和 pin role 保持一致。

## 标签政策与参考

真实 wire/pin 连接优先；netlabel/netflag 仅作跨模块、跨页或电源语义辅助，不能替代显式 `pin_net`。参考 KiCad 的 netlist/UUID/hierarchical sheet 思路，但采用本项目版本化 IR，并以适配层连接 EasyEDA primitive；不复制 KiCad 文件格式。

## 1.4 导线物化算法

快照只保存稳定的器件、引脚和 `pin_net`。Apply 时再根据引脚坐标和模块边界推导
图面 edge，避免把历史导线当成设计事实：

1. 对每个 net 建立 terminal 集合，先按 `componentId/pinNumber` 去重；任何缺失引脚、
   重复引脚或未知 net 都 fail-closed。
2. 同一模块内、距离较近的 terminal 优先生成真实正交 wire。候选边按
   `总长度 → 拐点数 → 穿 pin/穿已有边惩罚` 排序，选无环的最小连接森林；禁止为了
   统一网名生成跨页面大环线。
3. `global/power` 与 `global/ground` 允许在每个局部 terminal 放置多个同名 VCC/GND
   符号。局部短边（例如 LDO VIN/VOUT 与其输入/输出电容）仍保留真实 wire；未被
   短边森林覆盖的连通分量用一个就近电源/地符号收口。这样既保持电气同网，也避免
   一根环绕整页的电源母线。
4. `local/signal` 只有在同模块可读直连时画 wire；跨模块、跨页或长距离连接改用
   同名 netport/netlabel，但须源数据明确声明边界策略；距离长本身不能授权拆开 `direct`
   或原直连引脚组。标签只表达边界语义，不能用来掩盖缺失的 `pin_net`。
5. 每条 wire 必须非零、水平/竖直、端点落在 pin 或 marker 上；Apply 后依次运行
   `sch read`（pin→net）、`sch check`、`sch bridge-check` 和 DRC。任何短路、孤儿桩、
   多网导线或拓扑偏差都停止队列，不继续补线。

优化目标按优先级固定为：**拓扑正确 > 无短路/无悬空 > 少 wire 数 > 少总长度 > 少拐点**。
布局坐标、符号朝向和导线形状都可以重新计算，但 `component/pin/net/pin_net` 与网络
编号不得因重排改变。

## 验收

布局或复用前后 component/pin/net 数量、每个 pin 的 `netId`、模块端口映射和网络编号必须一致；缺少引脚数据时 fail-closed。
