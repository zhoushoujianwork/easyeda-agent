# E2E 自动化验收标准(Automation Acceptance Standard)

> 来源:2026-07-13 `esp32MiniRequire.md` 全流程回归(`--project ceshi`,CLI
> v0.11.3-4-ga02770a,connector 0.11.0,EasyEDA 3.2.148)。三块新特性
> (`place-constrained` 布局约束 / #97 workflow 状态机 / #99 手焊铁路门)**真机验证通过**,
> 但**未达 DRC=0**。本文把「卡点根因 + blocks 已消除的验证/思考 + 未来自动化评判标准」固化下来,
> 作为以后每次回归的判据。

## 1. 卡点:为什么没到 DRC=0(两层结构性阻塞)

达标 = `esp32MiniRequire.md` 条条落实 + 0 overlap + **0 fatal(DRC=0)** + 网络连通 + 丝印/极性 + 4 层电源树 + 已落盘。本次卡在 **DRC=0**,84 个 Clearance Error。根因**不是**新特性,而是:

1. **布局非确定 → 布线难。** `place-constrained` 是**分档精修**(贴边/朝向/合法化/net-aware),
   **不是信号流 floorplan**,且**不消费 block 里已编码的 `placement`(edge/side/orientation)
   与 `pcb_layout`(adjacency)数据**。于是主芯片落件坐标只能**手工试凑**(本次 rescale 两次
   0.55/1.42 找间距甜点,白耗 ~6–8min),产出**偏散板**(3033×2985mil,40 ratsnest 交叉)。

2. **末端布线无 headless 干净解。** `route-short` 是**稀疏板短网启发式**(无 clearance-aware
   推挤/撕绕),在非平凡 4 层板(29 件/17 信号网/40 交叉)上产 **84 Clearance**(Track-to-Track 44、
   Pad/Via 间距、轨穿 M3 孔)。design-flow 对此的正解是 **tier② 原生自动布线(需人点)**或
   tier③ Freerouting(需 JDK21 + 导入坑)。**当前没有能对 30 件级 4 层板产 DRC=0 的全 headless 路由器。**

> **结论:全 headless DRC=0 目前不可达。** 达标路径 = 好布局 + **tier② 原生自动布线(human-in-loop)**。
> 信号连通性本次已 100% 完成(DRC Connection=0),4 层电源树已通(Connection 52→0);差的只有布线**间距**。

## 2. blocks 已减少的重复推导——按 verification 和消费者能力使用

blocks 当前主要提供可检索的已证拓扑和设计提示,尚无完整 `block apply`。它可以减少重复推导,但不能因为
某项知识已写入 JSON 就假定选型、布局或实板均已机械验证:

| 环节 | blocks 提供 | 可减少的工作 / 仍需验证 |
|---|---|---|
| 选型 | `parts` → `standard-parts.json` 的 libraryUuid+deviceUuid+LCSC | `component_selection=passed` 时复用已证选择;仍检查供应状态和项目适用性 |
| 块内拓扑 | `internal_nets` + `ports`(引脚功能名,已 sch read 核实) | `verification.schematic=passed` 时不重复推导块内网表;只复用已证拓扑并核实跨块边界 |
| 布局/朝向知识 | `placement`(edge/side/orientation/reason)+ `pcb_layout`(decap/xtal adjacency) | 已有消费者的字段机械执行;其余仍是 Agent/人工提示 |
| 丝印 | `silk`(pins/label/note) | 作为标注清单复用;未有消费者时仍须人工落位和检查遮挡 |

始终需要验证跨块边界重绑(`port` → 本板网络名),并按 verification stage 判断器件选型、PCB 和 bring-up
是否真的通过。blocks 不替代最终 `sch check` / DRC / 实板验证。

## 3. 仍需机械验证的「真门」——用对判据(别省、别信错的读)

| 门 | **唯一可信判据** | ❌ 不可信 |
|---|---|---|
| 网表连通 | 跨块边界重绑**逐网核实** + `pcb drc` 的 **Connection Error 数**(0=通) | `pcb track-list` 计数(#103:已布线板读回 0,reload 不救) |
| 布局覆盖/间距 | `sch/pcb layout-lint`(0 overlap / 0 tight) | 截图(stale/blank) |
| 手焊可达 | `pcb layout-lint --gate` 的 #99(每件 ≥1 侧 ≥60mil 净通道) | — |
| 阶段门 | `workflow` 机械强制(指纹绑定,mutation 自动失效) | 记忆 / 人肉记状态 |
| 布线质量 | `pcb drc` 的 **Clearance Error 数**(0=干净) | — |

## 4. 自动化评判标准(未来每次回归照此判)

按 design-flow S0–S6 + P0–P10 跑,**每关用 §3 的判据机械判定**,人不介入判断:

- [ ] **S0** 方案书 spec 落成磁盘文件(modules/stackup/rf/board/interfaces);标准外设**已映射到 block**
- [ ] **S1** A4 sheet 存在(`sheet-geometry` provenance≠none)
- [ ] **S3** `sch layout-lint` = 0 overlap
- [ ] **S5** ① netlist **逐网精确匹配**意图(本次 20/20)且 0 意外合并;② `sch drc` 0 fatal;③ `sch check` 仅有意未用脚
- [ ] **P1** 器件全部上板带网(**用 `add-component`,不用 import-changes**——对 API 件 no-op #20,且会失效 workflow 授权链)
- [ ] **P2** `set-assembly hand-solder` 落盘;`place-constrained` 后 `layout-lint --gate`:0 overlap / 0 off-board / 0 tight / **0 iron-access-blocked(#99)**
- [ ] **P2** 主芯片**紧凑网格播种**(模块中心距≈包络+300–400mil,别撒 2000mil 外;compact 板利用率<3×)
- [ ] **P3** 板框圆角、M3 四角孔;**孔要在 place-constrained 前放**(否则不避让;当前 slot 孔检测不到=#104)
- [ ] **P6** workflow 链全绿(placement_confirmed→outline_confirmed→pre_route_passed→routing authorized)
- [ ] **P7** 信号 `route-short` → **`pcb drc` Connection 只剩 power 网**(=信号 100% 布通)
- [ ] **P8** `power-planes` → **Connection=0**(4 层电源树通:GND 内层 PLANE + 主轨内层 + 次级 routeAsTracks + 顶/底 GND pour)
- [ ] **P10 达标门** `pcb drc` **Clearance=0 且 Connection=0**(唯一未过项);`pcb check` 0;已 `pcb save`

> **判定规则**:S/P 每关**非零即停**,停在失败数据。DRC=0 是硬门。
> **Netlist Error=1** 若来自 `add-component` 焊盘 net=None,属已知底噪,单独核实非真断即可豁免。

## 5. 达标路径 & 待补工具缺口

**要让上面这套跑到 DRC=0,当前必须:**
- **布局**:在工具消费 block 数据前,由 agent 按 block `placement`/`pcb_layout` **确定性紧凑播种**(别 trial-and-error 缩放);
- **布线**:非平凡板走 **tier② 原生自动布线**(rip-up 信号 → `track-lock` 电源平面/缝合过孔 → 人点「布线→自动布线」并念 4 条对话框提醒 → agent 接手验 DRC+铺铜)。

**待补(补齐后可逼近全 headless 达标)**:
1. `place-constrained` **消费 block `placement`/`pcb_layout`**(edge/side/orientation/adjacency)→ 确定性紧凑布局,消灭手工试凑 + 给路由器更好起点。改进应**沉淀进 block 声明式数据**,不做成工具启发式(误伤别人板)。
   - ✅ **已落地(commit 1dbc065 + e94275d)**:消费 `placement.<ref>.edge` —— `edge="user-facing"` 连接器**分组到同一条共享边**并沿边紧凑排布,`edge="any"`(RF/模组)保持最近边;共享边由 **net-aware** 选(贴连接器电气搭档的主芯片,非几何质心)。真机 A/B(ceshi 同种子):几何选边 61 交叉 → 网感知 28 交叉(USB 贴 CH340),ratsnest 18305→11668mil。概念见 [concepts.md](./concepts.md)。
   - ⬜ **待补**:`side`(top/bottom 贴装面)→ 器件层;`pcb_layout` adjacency(去耦/晶振贴脚距离)→ 卫星就近约束;主芯片**信号流 floorplan**(blocks 未编码,是偏散板的更大根因)。
2. **headless clearance-aware 路由器**(或把 Freerouting 导入坑填平)—— 末端全自动 DRC=0 的前提。
3. 已开票工具 bug:**#103**(track-list 读 0)、**#104**(place-constrained 漏检 slot 孔);feat **#102**(M3 孔自动放置)。

## 6. 时间画像(本次总墙钟 ~91min,主动执行 ~55min)

| 段 | 主动耗时 | 备注 |
|---|---|---|
| S0 选型+方案书 | ~5min | blocks 使选型近零思考;28min 墙钟里大头是 workflow 理解并行 + 2 次里程碑确认等待 |
| S1–S6 原理图(摆+90脚 autoconnect+验) | ~13min | netlist 生成半手工(缺 `sch block apply`,phase-2) |
| P1 清板+add-component×29 | ~8min | import-changes 对 API 件 no-op → add-component |
| **P2 布局** | **~11min(内含 ~6–8min 试凑浪费)** | **最大低效点**:place-constrained 不吃 block 布局数据 → 手工 rescale |
| P7 布线 | ~7min(~3min 绕行浪费) | track-list stale + import-changes 失效授权链两次绕行 |
| P8 电源树 | ~5min | power-planes 一把过 |

> **可消除的低效**:P2 手工试凑(~7min)+ P7 两次绕行(~3min)≈ **10min**,补齐 §5.1/§5.3 后可省。
> 但**即使全省,也到不了 DRC=0**——那是 §1.2 的路由器结构缺口,非效率问题。
