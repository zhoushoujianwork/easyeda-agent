# 原理图连接数据模型（1.4）

1.4 将电气连接事实与图面布局分离。器件、引脚、网络及引脚到网络的关系稳定；坐标、旋转、线段、框线和文字可以重排。

## Connectivity IR

- `component`：实例 ID、位号、`libraryUuid`、`deviceUuid`、封装和属性；
- `pin`：所属器件、pin number、名称、方向和电气类型；
- `net`：项目内稳定 `netId`、规范名称、作用域和角色；
- `pin_net`：`{componentId,pinNumber,netId,connectionKind}`；
- `edge`：真实 wire、端口、标签等图面证据；
- `module`：可复用 Lib 的核心器件、外围器件、内部网和对外端口。

`primitiveId` 和几何数据属于布局层，不得成为连接依据。保存、导入 PCB、DRC 和回归测试均以 `pin_net` 对账。

## Lib 复用

每个框选功能电路登记为一个 Lib 实例：核心器件、外围器件、内部真实连线和 typed ports。人体存在板可拆成 Type‑C、反倒灌、DC‑DC、ESP32、传感器、按键、RGB/蜂鸣器等模块。复制时只生成新的实例 ID，拓扑模板和 pin role 保持一致。

## 标签政策与参考

真实 wire/pin 连接优先；netlabel/netflag 仅作跨模块、跨页或电源语义辅助，不能替代显式 `pin_net`。参考 KiCad 的 netlist/UUID/hierarchical sheet 思路，但采用本项目版本化 IR，并以适配层连接 EasyEDA primitive；不复制 KiCad 文件格式。

## 验收

布局或复用前后 component/pin/net 数量、每个 pin 的 `netId`、模块端口映射和网络编号必须一致；缺少引脚数据时 fail-closed。
