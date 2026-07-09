# v0.2 模组版 — 进行中笔记

## 2026-07-09 深夜首日(S0-S4 大部)

- **S0 拍板**(用户):WROOM-1U-N8(C2980297,IPEX 外引承接)+ 维持 4 层;
  其余决策承接 v0.1(贴片/SD 底面/USB 单取向/USB+12V/CC1101 方案)。spec.json 落档。
- **工程**:esp32-barrier-v02(uuid 7f864ed2076a461c83b11abb17ed3420),
  schematic 4fa895b5aeaafaab 三页 p1=e8cca22e56b45038 / p2=056ad9e011460821 /
  p3=2c3b22e2ffc2ae41,PCB 3f98c29aa09c2a5e。网表引擎探针 ✓(TESTNET)。
- **83 件放置+位号+autolayout 全绿**(v0.1 减 20 件)。U1 模组 41 pin 字典已读
  (GND=1/40/41,3V3=2,EN=3,IO4-7=4-7,IO15/16=8/9,IO17/18=10/11,IO8=12,
  IO10=18,IO21=23,IO48=25,IO0=27,IO38-42=31-35,RXD0/TXD0=36/37)。
- **布线战况**:P1 86/92(0 跨网)、P2 91/91 ✓ 收官、P3 69/72(1 装饰性交叉)。
- **v0.1 坑确定性复现实录**:同一 autolayout spec → P2 同坐标 (160,565) R702:2↔D701:1
  引脚重合(已用 v0.1 配方拆弹:R702→(330,565) 三针重画;教训:**搬家前先拆旧桩**,
  这次 orphan GND 旗又桥了一轮才想起);LED1:1↔R901:1 近距 5mil(P1,监视中)。
- **待办(下一会话)**:
  1. P1 剩 11 针:USB/模组巷道拥塞(J2:A7,B4A9 / U3:1,3,5,14,16 / U1:1,2,4,5)
     ——树感知结点直连需先把**备用 pin 也入障碍模型**(这次 wire-over-pin 教训);
     或清巷道(挪 C30x 行)后 fix_pins。
  2. P3 剩 3 针(U6:21/C692:1/X2:3):"already connected"假象=端点压 pin 类,
     用 endpoint_scan 定位删旗后重画。
  3. 全量网表核对(健康引擎!direct sch read 按名并网 vs spec)→ S5 三门
     (layout-lint + native DRC + check)→ 端点扫描 → 确认点②。
  4. PCB:分档确认制布局(孔→边缘→主芯片→卫星)→ P7 人机协作档
     (用户点原生自动布线)首验。
- CLI 票素材新增:autolayout 无 pin 间隙约束二次复现(同 spec 同坐标重合);
  lib search 返回值展示截断导致 uuid 抄错(应在 CLI 输出完整 uuid——已修 standard-parts)。

## 2026-07-09 上午:merge 修复验证 + P3 收官

- **五个 merge 修复实战验证**:①`sch bridge-check`(修补 daemon 目录漏注册后可用,
  三页 0 桥 0 孤儿);②layout-lint 异件引脚重合检测 ✓(P1 无误报);③multi-net 同名
  不再告警 ✓(P2 噪音消失);④check/drc --json 信封 ✓;⑤place --designator(未及用,
  下轮放置直接受益)。连接器 0.8.17→0.9.0 IndexedDB 热重载三连成(SOP 又验一遍;
  put 用 in-line key,别带 key 参数)。
- **发现工具盲区(票素材)**:「wire 端点压异网 pin」类(native DRC 六致命同类)
  bridge-check 和 check 双盲——bridge-check 的树模型没把 pin 挂进树;建议 bridgeCheck
  v2 把 pin 触点并入树聚合。实证:P3 三针(U6:21→CC_MOSI/C692:1→GND/X2:3→GND)
  两工具全绿但网表错。
- **P3 收官**:拆邻针重画 7 针+删 2 条手术残线(皱巴巴穿 pin 的 zigzag 是 disconnect
  合并残留),72/72 网名验证 ✓,0 悬空 0 桥。
- 剩:P1 尾巴 11 针(USB/模组巷道)→ 全量对账 → S5+确认点②。

## 2026-07-09 下午:原理图 S5 全门通过 —— 确认点②

**三页电气完整达成**(健康引擎直接对账,不再靠几何重建):
| 页 | 网表对账 | DRC | bridge | 端点扫描 | wire-cross |
|---|---|---|---|---|---|
| P1 | 92/92 ✓ | 0 fatal | 0 桥 | 0 无线致命 | 0 |
| P2 | 91/91 ✓ | 0 fatal | 0 桥 | 0 无线致命 | 0 |
| P3 | 72/72 ✓ | 0 fatal | 0 桥 | 0 无线致命 | 1(纯视觉,(175,710),不影响网表)|
**合计 255/255 连接全部落实。**

**关键武器固化 —— 全引脚障碍布线器 `route_pins.py`**(本轮真正解决 P1 巷道拥塞的东西):
把元件**全部引脚(含备用脚)**+ 现有线 + 异网旗全纳入障碍集,为目标 pin 找不碰任何
异网 pin/线/旗的 direction+offset。P1 拥塞 14 针一次 off=18 全落笔、0 交叉 0 穿脚——
这正是 v0.1「wire-over-pin」根因(旧障碍模型只含 spec'd pin,备用脚隐身)的根治。
已放 showcase/v0.2/;成熟后进 skills/scripts/。

**坑复现闭环**:P2 修 U2:1/J3:8 时 disconnect 又连带拆邻居 U2:2/J3:7 合并线(第 N 次
「搬家/拆针拆邻居」),补两针即闭环——教训已在 skill,这次十分钟内平掉。

**下一步(用户确认后)**:PCB 阶段——分档确认制布局(孔→边缘→主芯片→卫星,每档确认)
→ P7 人机协作档(用户点原生自动布线)首验 → 铺铜/丝印 → 确认点③。
