# EasyEDA Pro V4 适配开发台账

> 主线宿主：EasyEDA Pro V4  
> 最低基线：4.0.0  
> 推荐并已读验证：4.1.60（2026-09-21）  
> 当前结论：`read-compatible / targeted-write-verified / full-E2E-pending`

本文只跟踪 V4 产品版本带来的兼容、开发与现场验证。历史 V3 证据继续保留在原报告中，
但不再作为新功能完成态的宿主基线。V4 官方更新来源：
[专业版更新记录](https://pro.lceda.cn/page/update-record)。

## 版本边界

- 用户运行现场 EDA 自动化前必须升级到 V4；推荐使用 4.1.60 或更新的 V4 构建。
- `easyeda health` 的 `hostCompatibility` 独立检查产品版本：V3 为 `block` 诊断，较老 V4 为
  `warn`，4.1.60+ V4 为 `ok`；它不靠版本号替代动作回读，也不改变普通 action 的许可语义。
- `extension/extension.json` 的 `engines.eda: ~3.2.0` 是扩展 API 引擎版本。官方 V4 SDK 模板仍
  使用 3.2 API 线，不能把它误改成产品版本 4.x。
- Connector 开发类型基线升级到 `@jlceda/pro-api-types ^0.4.25`。

## V4.1.60 初始兼容矩阵

| 域 | 能力 | 状态 | 证据/后续 |
|---|---|---|---|
| 连接 | Connector 注册、项目/文档识别 | read-verified | Web V4.1.60、Connector 1.5.1 |
| 原理图读 | 器件、引脚、bbox、文本、网络 | read-verified | 42 个器件页实读；V4 pin `otherProperty` 已进入快照 |
| PCB 读 | 器件、层、网络、板框、配置、规则、报告 | read-verified | 69 器件考试板实读 |
| 类型 | Connector 对官方 0.4.25 类型 | offline-verified | `npm run typecheck` |
| 写入 | PCB typed mutation + save/reload | live-verified | 显示原点 `0,0→10,20→save/reload→10,20→0,0→save/reload→0,0`，几何不变且基线恢复 |
| 默认整板收尾 | [最小点灯板](test-case-esp32-blink.md) S0–S6/P0–P10 | not-run | 单页、两层、板外稳压 3.3V；只交第一节原始需求；保留基线并清理本轮对象 |
| 四层扩展整板 | `esp32MiniRequire.md` S0–S6/P0–P10 | pending | 保留旧状态，涉及对应能力时续测；不作为默认收尾前置 |

## P0 门禁

| P0 项 | 实现状态 | 完成判据 |
|---|---|---|
| V4 主线宿主声明 | implemented | health JSON 与人类摘要区分产品版本/API 引擎/项目版本 |
| 用户升级要求 | implemented | Skill 首检 V4；V3 停止现场写入并要求升级；推荐 4.1.60+ |
| 官方类型升级 | implemented | 0.4.25 typecheck + Connector 测试通过 |
| 引脚文本属性保真 | implemented | `pins[].otherProperty` 回读及单测 |
| 多变体 fail-closed | implemented | 复数 symbol/device/footprint 输入或运行时关联在 mutation 前拒绝；传统 subPart 不误伤 |
| 自定义位号 | implemented | 显式锚定 pattern；验证/保留；allocator 不猜 V4 递增序列 |
| V4 现场写入烟测 | live-verified | 4.1.60，PCB `2e719e9419653c72`；可逆原点经过两次 save/reload 并恢复 0,0 |

P0 已完成，但不等于 V4 完整兼容。2026-09-27 用户将后续默认整板回归改为最小点灯板；
其通过只证明单页、两层范围。四层扩展仍保留 `full-E2E-pending`，不得把兼容烟测或小板写成四层整板通过。

### P0 现场证据（2026-09-21）

- 环境：Web EasyEDA Pro 4.1.60；项目 `ceshi`；PCB `2e719e9419653c72`；Connector 1.5.1；
  CLI/daemon `v1.5.1-12-gd884e13-dirty`（同一 dev stamp）。
- `easyeda health`：`hostCompatibility.verdict=ok`，baseline 4.0.0，recommended 4.1.60。
- 写前：`pcb origin get` 回读 `offsetX=0, offsetY=0`。
- 写入：`pcb origin set --x 10 --y 20 --project ceshi --doc 2e719e9419653c72` 返回
  `changed=true, verified=true, affectsGeometry=false`；显式 `pcb save` 后 `doc reload`，再次回读 10/20。
- 恢复：同一 pinned 文档写回 0/0，显式 save/reload 后回读 0/0；`project doc` 最终仍为原 PCB UUID。
- 该动作只改变显示坐标原点，不移动 PCB 数据坐标和图元，适合作为 V4 持久化链路烟测；它不覆盖
  原理图写入、多变体、pad stack、布线或整板 DRC。

## 发布验收门禁（P0 之后）

默认回归必须落实最小点灯板需求、0 overlap/越界、DRC 无错误、全部必接网络连通、丝印极性和已落盘证据。
输入只使用[最小点灯板](test-case-esp32-blink.md)第一节，使用 `ceshi` 或已获准复用的专用工程，
完成后清理本轮对象。四层电源树、USB/降压和多页按相关变更补扩展回归，发布仍以明确批准范围为准。

## P1/P2 队列

### P1 数据完整性

- 逐层 pad stack、圆角矩形槽孔和完整 hole geometry。
- 3D body、自定义层及新封装图元的通用 inventory；修正“只有 pads/polylines 才算非空”。
- 探测原生半自动布局/布线、自动位号丝印、面积统计、高度约束区的公开 API；只通过 typed
  action/Cobra 暴露。
- 差分对、网络类和时间/逻辑等长规则做写后保存、重载和幂等验证。

### P2 采购、仿真与界面能力

- BOM 价格/库存/采购字段适配。
- DISA 网表与插件化仿真。
- 纯 GUI 体验项只记录，不为它们新增画布自动化。

## 每轮记录模板

每次更新本表需写明：V4 精确版本、CLI/daemon/Connector 版本、来源状态、命令、写前快照、
动作回执、save/reload 后原始回读、恢复结果、失败的第一条事实及日志路径。状态只使用
`planned`、`offline-verified`、`read-verified`、`live-verified`、`unsupported`；没有保存重载
证据的写操作不能标 `live-verified`。
