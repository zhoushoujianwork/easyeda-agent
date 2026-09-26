# 2026-09-27 完整 CLI 用例：离线布局未通过

本轮从 ESP32 客户第一节原始需求开始，在 `1.7.1-dev.11` 上完成选型与单件测量，
生成 51 件、195 个符号引脚、33 网、10 功能区的源数据。原理图布局尚未生成完整合法候选，
因此 A01 失败，未执行完整设计 Apply、PCB 转换或最终验收。此前接受的三个基础问题不涉及此次新失败。
跟踪：[bug #262](https://github.com/zhoushoujianwork/easyeda-agent/issues/262)，保持 open 待修复。

## 可复现输入

[十个独立功能区输入](fixtures/2026-09-27-cli-layout/README.md)由同一次真实几何测量生成，
包含原始尺寸、最终位号 bbox、引脚、明确连接/NC、归属与网策略；未复制历史完成态布局。
这些输入不访问 EDA，无需打开工程：

```bash
easyeda sch layout-plan --zones \
  --from docs/reviews/fixtures/2026-09-27-cli-layout/usb-input.json \
  --out /tmp/cli-usb-layout.json --report /tmp/cli-usb-report.json
```

CLI 非零退出并输出失败报告，不应生成可 Apply 的完整候选。USB 6 件输入取自完整源的第一区，
仅删除其余区域；它是功能区级复现，不声称已缩减成最少器件的数学反例。

## 实际结果

| 输入 | 离线结果 | 诊断 |
|---|---|---|
| usb | failed | GND/USB_VBUS 物理线岛缺少安全命名引线，候选预算耗尽 |
| mux | failed | 数据开关外围放置与姿态搜索耗尽候选预算 |
| uart | failed | 串口外围放置失败；姿态菜单耗尽，报告仍归为 candidate-budget-exhausted |
| boot | failed | BOOT/RESET 区候选预算耗尽 |
| mcu | failed | MCU/GPIO 区外围放置与姿态搜索耗尽候选预算 |
| input / slew / buck / detect / led | planned | 五个对照区可生成离线候选；不等于现场、电气或完整设计通过 |

完整源 v1 使用实测 0°、20,000 候选；v2 允许源数据明确授权的无极性 R/C/L 正交旋转、
总预算 100,000，均在 USB 区失败。v2 实际进行了 17 次姿态尝试，原姿态分到 75,000，
其他姿态共用剩余预算；最后一次仅 1,563。这个分配值得后续定位，但**尚未证明它就是根因**。
根侧另按引脚朝向计算外围面对宿主的候选姿态，同样在 100,000 预算下失败；
没有将该计算姿态冒充新测量，没有修改生产求解器、网策略或失败退出。

所有失败均保留 `no global feasibility proof`：当前有界算法没有找到结果，不能声称电路布局无解。
单纯放大预算或允许更多姿态尚不足以形成可靠修复；不得用手工坐标、同名标签岛、删除冲突检查补签。

这些冻结布局输入仍使用最初测量的 L1 MT 变体。之后发现其资料与 NT 型号不一致，
已另行核对并补测 NT，生成 `canonical-final.json`；没有重写旧输入或把原 buck 区结果当作新变体布局通过。

## 原始证据与边界

- 第一节输入 996 字节，SHA-256 `e62ebb05ecedb82e103d81b31da2bbf26aefe0669e580961c3aae87fef7d74d0`。
- 本地本轮目录：`artifacts/release-v1.8.0-e2e-20260927/a00-a01-design-source/`。
  `measurement-artifact-manifest.json` 冻结 744 份测量材料，SHA-256
  `5c2dc1594977a58dda1f0169e94588e0cca8df7e34d68752ec5a49d7c003ec64`。
- `layout-failure-artifact-manifest.json` 冻结两轮完整源、命令与诊断，SHA-256
  `1dd9a2dda9756f8d3a136ad49039447aae9776f1d3f33666ea32f3d3ce2821ec`。
  分区重放原包另存 `artifacts/release-v1.8.0-e2e-20260927/layout-fixture-replay/`。
- 测量后精确清理并保存重载；原理图仍只有图框，PCB 原生 184 条记录未变。
  原生归档已校验，未执行重新导入恢复，`restoreVerified=false`。
- 51 件静态身份已独立核对；完整测量复核与电感型号资料冲突的后续处理另见
  [发布执行报告](../releases/evidence/v1.8.0/test-report.md)。本报告不签完整 M1 或 E2E。

## 后续修复与关闭条件

从上述原始几何重现定位放置、物理线树、命名出口与姿态预算的相互影响，先补通用修复和正负例。
保留所有显式归属、旋转授权、NC、真实 direct 连通、文字/本体/导线碰撞和共享预算约束。
修复后重放十区及完整源，验证合法图形、实际线树与源数据不变，再继续纸张布局、Compose/Apply、
保存重载及完整 E2E。未修复前保持问题 open，基础 CLI 其他功能按各自实测范围使用。
