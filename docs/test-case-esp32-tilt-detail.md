# 体感小板：块映射、埋雷与回放

[返回主文档](test-case-esp32-tilt.md)。本页给测试协调者和工具维护者使用；第一节原始需求以外的内容
不得交给执行 Agent。状态：planned / not-run。

## 参考块映射

下表是协调者核对用的参考答案，不是执行输入；Agent 可做不同但合理的选型，以需求和验收为准。
块状态取自 2026-11-26 `internal/blocks/data`。

| 功能 | 参考块 | 件数 | 块当前状态 |
|---|---|---|---|
| USB-C 供电 + 双取向数据 | `usbc_dual_orientation_data` | 5 | 原理图 passed（孤立单放）；PCB/上电 pending |
| 5V→3V3 | `ams1117_ldo_3v3` | 4 | 随整板布线验证；非孤立单放 |
| 主控最小系统（含 3V3 去耦、EN RC） | `esp32s3_wroom1_module` | 6 | 随整板验证 |
| BOOT / RESET | `tactile_boot_reset` | 2 | 随整板布线验证 |
| 6 轴 IMU | `bmi270_imu_i2c` | 4 | 原理图 passed；PCB/上电 pending |
| I²C 上拉 | 块外补 2 只电阻 | 2 | `bmi270_imu_i2c` 端口说明上拉在主控侧，本块不含 |
| 电源灯、用户灯 | `led_indicator_gpio` ×2 | 4 | 随整板验证；需极性丝印 |
| 调试排针 | `uart_power_header_1x4` | 1 | 无验证记录 |

合计约 28 件。注意：

- 原生 USB 下载：USB-C 的 `USB_DP`/`USB_DM` 直连模组 GPIO20/GPIO19，以 `sch read` 回读的真实脚名为准。
  `esp32s3_pico_native_usb` 是 PICO SiP 的最小系统，不适用于 WROOM，不要套用。
- 电源灯可直接挂 3V3，不占 GPIO；用户灯接一个普通 GPIO，避开 strapping 脚和 USB 脚。
- `usbc_ufp_power_or` 适用于多路 5V 并联，本板单一 USB 供电不需要 OR 二极管。
- 跑完后把 USB、IMU 两块的 `pcb_drc` 状态按实际证据回填；未上电保持 `bringup: pending`。

## 埋雷清单

埋雷版从已通过的完成版派生，每次只记录植入前后的对象差异。“候选检查”是预期能查出该雷的工具，
实施第 3 步逐雷实测后，再把结果填入“现状”列（detected / missed / partial）。

| # | 缺陷 | 植入方式 | 标准答案 | 候选检查 | 现状 |
|---|---|---|---|---|---|
| M1 | I²C 上拉缺失 | 删除 2 只上拉电阻 | SCL/SDA 各 4.7k～10k 上拉到 3V3 | `sch check` | not-run |
| M2 | 电源网名分裂 | 一部分 3V3 改成 `+3V3` | 统一为一个网名 | `sch nets` 跨页/同页网名比对 | not-run |
| M3 | USB-C 单侧 CC | 删除一只 CC 下拉 | CC1、CC2 各 5.1k 到地 | 需新增规则 | not-run |
| M4 | 只接一面 USB 数据 | 断开 B 面 DP2/DN2 | A/B 两面并联 | 块引脚真值表对账 | not-run |
| M5 | EN 缺 RC | 删除 EN 电容 | 10k 上拉 + 1µF 到地 | 需新增规则 | not-run |
| M6 | LED 极性丝印反 | 交换 +/- 丝印 | 丝印与焊盘极性一致 | `pcb check` / `silk-list` | not-run |
| M7 | 天线下方有铜 | 铺铜覆盖天线区 | 全层禁铜 | `pcb check` | not-run |
| M8 | 网络未连通 | 删除一段导线 | 所有必接网络连通 | PCB DRC Connection | not-run |

漏报项先补成机械检查规则：规则说明写进对应参考文档并带 § 引用，最小复现放进 `testdata/`，
同时提供正对照（完成版不误报）和负对照（埋雷版必报）。规则输出统一 JSON 报告：
严重度、对象、位置、依据、建议修法，供 EdaDesk 只渲染不重算。

## 演示回放

- 只使用一个固定的“演示”工程，每次清页后回放；SDK 的 `deleteProject` 删不掉工程，不为演示反复新建。
- 不依赖导入工程文件：导入会切换工程并重载，连接断开后结果未知。改为用户打开该工程后，
  用 `audit export --playbook` 导出的 playbook 执行 `easyeda apply`。
- 回放成功判据与完成版相同：逐件位置、逐引脚网表一致，DRC 无错误，保存重载后回读一致。
- playbook 绑定具体 CLI/连接器版本；版本升级后先重跑一次回放，失败则重新录制，不改判据。

## 验收与记录

完成版沿用[最小点灯板详细稿](test-case-esp32-blink-detail.md)的五步与最少记录，另外核对：

- USB 双取向数据、两只 CC 下拉、GPIO19/20 直连；
- I²C 两线有上拉，IMU 地址脚与 CSB 状态符合块说明；
- 两颗 LED 极性丝印与焊盘一致，天线禁铜区覆盖全层；
- 结束时用 `easyeda audit cost --record` 记录成本，未知 token 不编造。

工程使用 `ceshi` 或获准复用的专用工程；完成版和埋雷版的 playbook、报告放在忽略的 `artifacts/`，
可复用的结论和最小复现才进入仓库。
