# 样例索引

样例用于迁移做法，不是让 Agent 复制一组绝对坐标。先核对当前器件、封装、引脚、网络和机械
约束，再替换参数。原始题目、评分表和截图只提供需求证据；只有实际命令输出、官方回读和保存
重开证据，才能把状态从 `source-only` 提升为 `offline-verified` 或 `live-verified`。

## 选择顺序

1. 任务有固定板框、孔位或面板件坐标：先用固定机械样例，再排关键通道和外围。
2. 自建板没有固定尺寸：先组织功能模块、接口和关键通道，再由占地与布线空间确定板框。
3. 原理图与 PCB 使用同一循环：读取原始数据 → 替换参数 → 计划/执行 → 回读差异 → 修正 → 保存。
4. 相同做法只维护一个样例；某道题不同的尺寸、规则或例外留在该题参数记录里。

## 当前样例

| 样例 | 何时读取 | 状态 |
|---|---|---|
| [原理图部分写入后的精确恢复](schematic-partial-recovery/README.md) | fresh 核对失败对象、保护引脚属性、真实 unwired 重算及图签修正 | `partial-live-verified`；三页保存重载现场通过，独立复核中；不签 PCB/E2E |
| [已有相对布局的完整组移动](pcb-group-move/README.md) | 五成员整体平移/旋转、非对称 anchor、完整成员与固定件对账；开发验证场景起点 | `offline-verified`；五成员合成几何与 CLI 已验证，现场待验证 |
| [260919 AT32F415 总索引](260919-at32f415/index.md) | 69 个器件、15 个功能区、90×50 mm 两层板的完整 Demo | `partial-live-verified`；代表性步骤已验证，完整布线明确未完成 |
| [260919 机器可读样例目录](260919-at32f415/example-catalog.json) | SCH/PCB/LAY/RTE/FIN 共 36 个技术点的来源、参数、步骤、观测、错误修法和待验证项 | 15 项 `live-verified`，其余保持 `offline-verified` / `source-only` |
| [参数化初始布局](260919-at32f415/initial-placement.json) | 69 件布局顺序、模块关系、坐标、锁定、实际回读和已知例外 | `partial-live-verified` 初始布局；独立检查已记录布局缺陷，不声明关键网络已布通 |
| [现场验证摘要](260919-at32f415/live-validation.json) | 原理图、机械、规则、布局的保存后回读及真实工具故障 | `partial-live-verified`；逐项列出未声明完成的工作 |
| [关键网络规划与现场迭代](260919-at32f415/critical-routing.md) | 晶振与 CAN 的 topology-aware 计划，以及用真实飞线/绕行反例修正布局 | `partial-live-verified`；晶振次序与 CAN 第一轮负例已现场回读，关键铜仍未写入 |
| [模块候选 Layout](260919-at32f415/layout-candidates.md) | LED 板边、MCU 逐脚去耦、LDO 刚体变换；AI 定关系、算法生成多个完整坐标候选 | `offline-verified`；LED/MCU 待本轮 typed Apply、保存重开回读后升级 |
| [LDO 原理图、布局与回流](260919-at32f415/ldo-placement.md) | AMS1117 双 VOUT/TAB、输入输出电容顺序、顶层 GND 回流 | `partial-live-verified`；局部放置与15段铜已验证，整板主干待完成 |
| [板框、安装孔与固定器件](260919-at32f415/fixed-mechanics.md) | 固定尺寸题、坐标原点、圆角、锁定、固定/半固定器件 | `live-verified`；保存后对象回读保持；typed 重载修复仍需复测 |
| [模拟题与 7 套练习差异](exam-differences.md) | DCDC、Near pin、开尔文、传感器开槽、RF 净空等迁移题 | `source-only`，不是黄金答案 |

## 样例字段

每个样例必须保留以下字段，验证者据此判断能否照做：

- **来源**：文件名、页码、原理图区域或 BOM 行。
- **开始状态**：需要哪些页面、器件、网表和未布线条件。
- **参数**：可替换值、单位、坐标语义以及不可修改的题目值。
- **命令/步骤**：当前 typed CLI、`easyeda apply`，或明确标记为 `planned` / `unsupported`。
  GUI、CUA、鼠标、键盘、画布、属性面板和工程树都不是样例执行器。
- **观测**：应读取的数据对象与差异，不写“看起来正确”。
- **错误与修法**：真实或可复现的失败模式及回读后的修正路径。
- **验证状态**：`source-only` / `offline-verified` / `live-verified`，附证据边界。

结构化参数文件只是现有命令的输入记录，不是新 DSL。当前 260919 的参数总表见
[source-manifest.json](260919-at32f415/source-manifest.json)，逐技术点目录见
[example-catalog.json](260919-at32f415/example-catalog.json)。
