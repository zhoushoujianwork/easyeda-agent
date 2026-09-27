# CLI Status

更新于 **2026-09-27**。本页维护当前可用范围和后续进度；历史参数、失败和证据留在对应报告。
本次测试运行态为 CLI/daemon `v1.7.1-dev.14`、connector `1.7.1-dev.14`，Web EasyEDA Pro 4.1.60。
正式版 [v1.8.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.8.0) 已按 CLI 修复范围发布；
以下保留实际现场运行版本及有限范围，不是正式包现场全集复测或实时健康检测。

**基础 CLI 集中检查已收尾，已覆盖的常用操作基本满足日常使用。** 剩余问题继续按 bug 跟踪。
高级完整设计例子仍在验收，尚不能承诺从需求到成品的全自动交付。

用户已于 2026-09-27 简化后续收尾：默认只跑[最小点灯板](test-case-esp32-blink.md)，
单页原理图、两层 PCB、板外稳压 3.3V。新用例尚未执行；旧四层工程保留为扩展回归。

## 当前进度

| 项目 | 状态与范围 |
|---|---|
| 基础 CLI 集中检查 | **已收尾**。按用户决定结束该轮检查；最近完整同包 dev.9 为 9 pass / 2 fail，dev.11 仅定向复测，历史失败保留。 |
| 默认端到端收尾 | **not-run**。下一轮使用最小点灯板，核对连接、几何、DRC 和保存重载；不要求先完成旧四层开发板。 |
| 三页原理图 | **有限通过**。51 件、195 脚的源数据、静态电气、图面、保存重载及独立复核通过。 |
| 局部修改、写前拒绝与中断恢复 | **有限通过**。签已记录子例；L1 仅点灯两件无铜移动/恢复，不外推带铜或其他模块。 |
| PCB 导入与布局 | **布局待确认**。51 件/203 焊盘、四 M3 孔和四层 RF 区已保存重载；#269/#270/#271 按实测范围关闭。14 功能标记、六电容修正后的连续两轮自检及独立布局复核通过，等待用户确认当前保存版。 |
| 最终四层叠层、布线与铜 | **未通过**。目前为内层 SIGNAL 中间态；最终 GND PLANE、整板布线、铜、DRC 与最终导出仍待完成。 |
| v1.8.0 发布 | **已发布**（2026-09-27）。CLI 修复范围独立复核、正式构建与离线 smoke 通过；GitHub 为非草稿，11 项远端资产大小及 SHA256 全部匹配。完整 ESP32 成品验收另行跟进。 |

本次接受 #55、#260、#261 为已知限制，移除其整体阻断；不改写严格基础结果，也不免除设计需求。
具体范围、发布结果与验收材料见[发布记录](releases/release-1.8.md)和[执行报告](releases/evidence/v1.8.0/test-report.md)。
当前 dev.14 升级及完整基线保持已独立复核通过；原运行态和旧失败记录保留。

## 日常可用范围

以下依据 [dev.9 基础检查](reviews/2026-09-26-v1.7.1-dev9-basic-cli.md)及
[dev.11 定向复测](reviews/2026-09-26-v1.7.1-dev11-pour-live.md)。适用于实测的个人测试工程和 Web 4.1.60。

| 功能 | 使用判断与边界 |
|---|---|
| 帮助、连接、目标路由、重连 | 可用；连接后仍核对实际工程和文档。 |
| 个人工程、页面创建/读取/删除 | 已测操作可用；改名存在失败，项目容器删除尚无 typed 入口。 |
| 原理图器件、导线、网络标记与连接读取 | 常用操作可用；修改、NC 清除和保留器件清页见下表，不外推全部复杂电路或块。 |
| PCB 器件、板框、导线、过孔、配置 | 已测基础操作可用；规则首次初始化仍有已知差异，整板设计质量另验。 |
| 区域与铺铜边界 | 有限制；显式线宽及优先级未兑现，失败创建不计成功。 |
| 保存、重载、BOM/网表/图像/原生归档导出 | 已测类型可用；外层 ok 不代替 saved:true，重要结果保存重载。不外推全部制造导出。 |

失败时保留输入和已写对象，从 fresh 回读决定下一步。团队空间、其他宿主及未列命令不推断已验证。
精确命令见[CLI 索引](cli/README.md)，实现与规划见[功能清单](FEATURES.md)。

## 未解决问题

| 问题 | 当前跟进 |
|---|---|
| [#55 页面改名](https://github.com/zhoushoujianwork/easyeda-agent/issues/55) | open；本次使用核对过的页面 UUID。 |
| [#256 原理图修改](https://github.com/zhoushoujianwork/easyeda-agent/issues/256) | open；按实际回读处理。 |
| [#257 NC 清除](https://github.com/zhoushoujianwork/easyeda-agent/issues/257) | open；清除结果待修复和复测。 |
| [#258 规则首次初始化](https://github.com/zhoushoujianwork/easyeda-agent/issues/258) | open；保留完整字段差分和 verified:false。 |
| [#260 区域/铺铜边界线宽](https://github.com/zhoushoujianwork/easyeda-agent/issues/260) | open；默认值范围已接受，显式请求未兑现。 |
| [#261 铺铜优先级](https://github.com/zhoushoujianwork/easyeda-agent/issues/261) | open；排序请求与回读不符，实际铜仍须验收。 |
| [#267 保留器件清页](https://github.com/zhoushoujianwork/easyeda-agent/issues/267) | open；可能误计划删除引脚子级属性，本批不用该路径，未发生损坏。 |
| [#268 后台 PCB 保存](https://github.com/zhoushoujianwork/easyeda-agent/issues/268) | open；后台 saved:false 原因待定位，显式保存重载已另行核对。 |

影响、复现、使用建议和关闭条件统一维护在[已知问题](cli-known-bugs.md)，本页只维护状态。
已关闭的 #262–#266、#269/#270/#271 均保留有限范围，详见[原理图记录](reviews/2026-09-27-cli-schematic-gate.md)、
[导入记录](reviews/2026-09-27-pcb-import-confirm-fix.md)、[投影修复](reviews/2026-09-27-pcb-polygon-projection-fix.md)。
最新位号修复、L1 与采用证据见[位号记录](reviews/2026-09-27-pcb-silk-align-fix.md)。
旧四层例子的当前布局、剩余项及确认点见[PCB 布局复核](reviews/2026-09-27-esp32-pcb-layout.md)，
只在继续该扩展用例时恢复，不作为默认收尾前置。

## 后续维护

- 修复后补对应正负例、保存重载和独立复核，再更新状态；未经复测不标已解决。
- 每次状态变化记录日期、版本、证据链接和 issue；历史报告不被新状态覆盖。
- 默认完整用例只从最小点灯板第一节执行；旧四层工程的复用及叠层 A 属该扩展例子，
  不套用到新的两层板。旧高级及 v1.8.0 未完成记录不补签。验收方法见[高级 CLI 检查](cli-advanced-test.md)。
