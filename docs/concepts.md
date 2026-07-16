# 核心概念与对象(拉通认知)

> 本文是 easyeda-agent 布局/布线域的**共享词汇表**——把散在代码注释、memory、
> 对话里的概念统一到项目层,让后续会话、贡献者、Skill 用**同一套心智模型**。
> 新概念先落这里,再在代码/Skill/memory 里引用。相关:[cli-design.md](./cli-design.md) ·
> [e2e-automation-acceptance.md](./e2e-automation-acceptance.md) ·
> `skills/easyeda-agent/references/design-flow.md`(流程脊柱)。

---

## 一、电气层

### 网(net)
一条把若干**引脚(pad)**连在一起的电气网络。原理图的连接、PCB 的连通性都以网为单位。
一个器件的每个 pad 带一个 `net` 名(如 `USB_DP`、`GND`、`3V3`);同名即同网、电气相连。

### local net vs global net —— `isGlobalNet`
- **global(全局网)**:`GND` / `VCC` / `3V3` / `5V` / `VBUS`… 电源与地,**几乎每颗芯片都连**。
- **local(局部/信号网)**:`USB_DP` / `MCU_RX` / `EN` / `IO0`… 把**特定两三颗器件**绑在一起的信号网。
- **为什么区分**:判断「谁是谁的电气搭档」只能看 **local 网**——global 网谁都连,拿它找搭档
  会选到「几何上近但电气无关」的芯片。工具里 `isGlobalNet()` 就是这条线;net-aware 决策
  一律 **local 优先,无 local 才退回全部网**。

### net-class(网络角色分档)—— `netRole` / 规范线宽 / 电源铺铜
`isGlobalNet` 只把网二分成「电源地 vs 信号」;**net-class** 在电源侧再细分成**载流角色**,
驱动两件事:走线该多宽、该不该走铺铜。分类器 `netRole()`(`internal/app/pcb_netclass.go`)
按**网名/电压**启发式给角色(块声明的 per-net 宽度可覆盖):

| role | 判据(net 名) | 规范宽(§7.8,seed 自 live 规则) | 铺铜倾向 |
|---|---|---|---|
| `gnd` | 含 `gnd` | (走线 20mil,但**优先 pour**) | 全板 pour / 内电层 |
| `high-current` | `VBUS`/`VIN`/`VBAT`/`VSYS` 或 ≥9V | ~20mil | pour / fill |
| `power-trunk` | 5–9V(如 `+5V`) | ~15mil | pour |
| `power-branch` | <5V 或无电压名(`3V3`/`1V8`/`VCC`/`VDD`) | ~10mil | pour(局部) |
| `signal` | 其余(非 global) | live 默认(细间距可收窄到最小合规宽) | 走线 |

- **消费点**:`route-short` 按角色查 `netClassWidthTable()` 给宽(不再是 20/10 二分桶);
  `pcb net-classes` 打印当前表;`pcb check` 的 **width-under-spec**(电源线 < 规范宽)+
  **power-not-poured**(电源网未铺铜)把关;2 层电源铺铜 `pcb power-pour`、4 层 `power-planes`。
- **铁律**:width 维度与「该不该 pour」维度**共用同一个 `netRole` 定义**——否则「电源该多宽」
  与「电源该 pour」两套判据漂移。规范线宽是**设计惯例非制造规则**(live DRC 规则只给单一默认宽),
  故内联 Go 为真值 + `fab-rules-jlcpcb.json` 文档镜像;精确值由块 `signals.track_width_mil` 声明覆盖
  (`improvements-sink-to-blocks`,消费待 block-apply)。

### 网感知(net-aware) vs 几何(geometric)—— 本项目的核心决策二分
| | 依据 | 例子(连接器分组选边) |
|---|---|---|
| **几何** | 只看 X/Y 坐标 | 「这些连接器质心离哪条板边最近 → 就贴那条边」——不问它们连到谁 |
| **网感知** | 看电气连接(net) | 「这些连接器主要连到哪颗芯片(local 网)→ 贴那颗芯片所在的边」 |

**判据一句话:让「谁挨着谁」由电路连接决定,而不是由当前谁离谁近决定。**
几何决策会把 USB 拉到离它搭档 CH340 很远的边 → 差分对横穿全板 → 交叉暴涨;网感知把 USB
放到 CH340 同侧 → 短、少交叉。实测(ceshi 同种子 A/B):几何 61 交叉 → 网感知 28 交叉。

---

## 二、布局对象与分档(placement tiers)

`pcb place-constrained` 按**固定的四档序**布局,高档先锁死、低档只能在其空隙里填:

| 档 | 对象 | 处理 |
|---|---|---|
| **T1 孔** | M3 安装孔 / 挖槽(layer-12 fill) | 障碍,永不移动;其他件避让 +60mil 垫圈净空 |
| **T2 边缘件** | 连接器 / 模组 / 天线(`board_edge=true`) | 贴板边 + 锁定;**user-facing 分组、any 就近**(见下)|
| **T3 主芯片** | ≥8 脚芯片 / 晶振 / `anchor=true` | 保持种子位、冻结(**工具不做主芯片 floorplan——种子由 agent 给**)|
| **T4 卫星** | 去耦/上下拉/LED/按键 | 螺旋合法化,**net-aware 吸到搭档芯片**;不冲突的保持原位(不扰手调)|

### edge role —— 边缘件的两种边语义(块声明)
块 `placement.<ref>.edge` 声明边缘件的**边角色**,工具据此分流(`edgeRoleOf`):
- **`user-facing`**:USB / SD / 螺钉端子 / 排针——用户插拔的外部 I/O。**≥2 件时分组到同一条
  共享边、沿边居中紧凑排布**;共享边由 **net-aware** 选(搭档芯片所在边),外部 I/O 聚一处
  又不拉长网。diag 标 `:grouped`。
- **`any`**:RF 天线 / 无线模组——必须在**某条**边(缩短天线走线),但哪条都行 → 保持各自最近边。

匹配:优先读块 hint(独特 designator 前缀 JP/SW/LED/ANT),通用前缀 J*/U* 走 device-name
regex 兜底(regex 是块数据的镜像)。**仍不解析自由文本 `orientation`**——那要 `blocks show` 摊给用户。

### partner chip(搭档芯片)
一个连接器/卫星**电气驱动的那颗固定主芯片**(经共享 local 网)。net-aware 布局的目标点:
卫星吸到它、连接器组贴它所在的边。工具里用 `mainNetPads`(net→主芯片焊盘)+ `nearestPad` 找。

---

## 三、块(block)—— 本项目定义的一等功能概念

### 块是什么
**块 = 一段已在真板上端到端验证过的「外围功能子电路」,作为可复用单元。**(CH340 USB 转串口、
ESP32 双三极管自动下载、SY8089 buck、RS-485、GNSS 前端、microSD…)。不是零散的器件、也不是
一整块板,而是**功能粒度**的电路积木——「点灯」「USB 烧录」「5V→3V3」各是一个块。

**当前定位**:blocks 是轻量拓扑 manifest(`parts/internal_nets/ports`)加 Agent 可读设计手册。现阶段主要
能力是离线检索、复用已证拓扑和给少量 PCB 消费器提供声明；完整 `block apply` 尚未实现,所以不能把
“JSON 中有字段”理解成“工具已执行该约束”,也不把复杂 block IR 视为已经证明价值的旗舰能力。

### 三层库(器件 → 块 → 流程)
- **器件层** `standard-parts.json`:role → LCSC/UUID,选型单一源。
- **块层** `internal/blocks/data/*.json`:把器件按功能组成子电路 + 携带多维放置/信号/丝印知识。
- **流程层** design-flow(S0–S6/P0–P10):把块编排进整板流程。
块 `parts.<role>` 指回器件层、`block` 引用被流程层的 S0 方案书 module 引用——三层串起来。

### `verification` 门(块的分项可信判据)

`schema_version: 2` 起将验证拆成 `schematic`、`component_selection`、`pcb_drc`、`bringup`
四个独立阶段,每阶段记录 `status` + evidence/issues。只有四项均为 `passed` 且显式
`production_ready: true` 才显示 `ready`;原理图拓扑通过但尚未投产就绪时显示 `verified`,不能把
"拓扑可复用"误读为"选型和实板均可靠"。

迁移期间保留 legacy `validated` 字符串兼容旧块;一旦 block 存在结构化 `verification`,它就优先决定
状态。新贡献不得只依赖非空字符串声明 ready。拓扑证据允许免去重复推导,但跨块边界仍需核实;
器件选型、PCB 和 bring-up 是否可信必须分别读取对应 stage。

机器约束由 `internal/blocks/data/_block.schema.json` 和 `go test ./internal/blocks/` 共同执行:前者约束
稳定字段类型/必填项/枚举,后者检查 standard-parts 外键及 role/port/internal_nets 引用闭合。

### 消费与贡献
- **消费**:`easyeda blocks ls/show/search`(go:embed 进二进制,**离线、无需 daemon/窗口**);手工接任何
  已知外围**前先查块**(铁律 8)。
- **贡献**:手接并端到端验证过的新外围可回流入库,但优先保持核心 manifest 简洁；只有已有或即将实现的
  消费器需要某项约束时才新增结构字段,解释、经验和项目复盘留作文档内容。
  朝向/贴边/间距等改进若已有确定性消费者,沉淀成声明式数据；否则先作为手册说明,不为未来猜测提前扩 schema。

### 块数据模型(多维 map)

`internal/blocks/data/*.json` 是块拓扑的声明式来源,同时暂存部分设计说明。下表必须区分“工具已消费”与
“仅供 Agent/人工阅读”;未消费字段不是机械保证:

| 字段 | 内容 | 谁消费 |
|---|---|---|
| `parts.<role>` | role → `standard-parts.json` 的 LCSC/UUID | Agent/人工选型入口；仍按 verification、供应状态和项目适用性判断 |
| `internal_nets` / `ports` | 块内网表 + 边界端口(功能名) | 原理图实例化(`verification.schematic=passed` 时复用已证拓扑,仍验跨块重绑)|
| `placement.<ref>` | `board_edge` / `edge` / `side` / `orientation` / `severity` / `reason` | T2 边缘件(edge 已消费;side/orientation 待补)|
| `openings` | `[{match, local}]` 连接器开口本地方向 | T2 朝向(旋到开口朝板外)|
| `pcb_layout` | `*-adjacency`(去耦/晶振贴脚)/ `rf-keepout` / `ep-*` | T4 贴脚(待消费)/ P4 禁布 / P8 热焊盘 |
| `signals` | 差分对 / 阻抗 / 等长 | P7.0 关键网先行 |
| `silk` | 逐脚标注 | P9 丝印 |

**source-of-truth 分层**:选型 = standard-parts;拓扑 = internal_nets;外部电气事实 = datasheet/官方参考;
板级工艺参数 = 当前项目 stackup/fab profile。blocks 对外部手册多为摘要和引用,不是这些事实的最终来源。

**投资门槛**:在继续扩 schema 或全量迁移前,先用一个简单块完成最小 `sch block-apply` 闭环
(放置→连内部网→绑定 ports→check→实例 manifest)。只有该闭环能稳定减少操作和返工,才继续投入稳定
net ID、全量 verification、provenance 和复杂 PCB constraints；否则保持“简洁 manifest + 手册”。

---

## 四、可信判据(reliable oracles)——判对错只信这些

| 判什么 | ✅ 唯一可信 | ❌ 不可信 |
|---|---|---|
| 网络连通 | `pcb drc` 的 **Connection Error 数**(0=通) | `pcb track-list` 计数(#103:已布线板读 0)|
| 器件覆盖/间距 | `pcb/sch layout-lint` | 截图(stale/blank)|
| 手焊可达 | `layout-lint --gate` #99(每件 ≥1 侧 ≥60mil)| — |
| 阶段门 | `workflow` 机械强制(指纹绑定,mutation 自动失效)| 人肉记状态 |
| 布线间距 | `pcb drc` 的 **Clearance Error 数** | — |
| 读 mutation 后的状态 | 先 `doc reload` 再读(见 [[pcb-stale-reads-need-doc-reload]])| mutation 后第一次读 |

---

## 五、已消化 vs 待补(工具消费块数据的进度)

- ✅ **T2 edge 语义**:user-facing 分组 + net-aware 选边(commit 1dbc065 / e94275d)。
- ⬜ **T2 `side`** → 器件贴装层(top/bottom);**`orientation` 自由文本**仍要人读。
- ⬜ **T4 `pcb_layout` adjacency** → 卫星贴脚距离硬约束。
- ⬜ **T3 主芯片信号流 floorplan**:块**不编码**主芯片相对位置 → 板子大小主要由 agent 的种子决定,
  这是「偏散板」的更大根因(见 [e2e-automation-acceptance.md](./e2e-automation-acceptance.md) §5)。
- ⬜ **末端 headless 干净布线**:route-short 稀疏板启发式,非平凡板需 tier② 原生自动布线。

### 4 层叠层规格(客户确认 2026-07-13)= GND 主导
- **L1 顶层**:信号 + **GND 铺铜**(首层) · **L2 Inner1**:**GND 内电层 PLANE** · **L3 Inner2**:信号层**主走电源**(3V3+5V 埋这层) · **L4 底层**:信号 + **GND 铺铜**(尾层)。
- GND 在 顶/内层1/底 三层,电源(3V3+5V)埋内层2。**必须一次 `power-planes` 在干净板上做对**——**别反复 pour/翻层**:实测反复操作会累积 netless 死铜(挡顶层铺铜填充)+ 把 PLANE 绑定 pour-rebuild 降级成 netless@L1(L15 绑不上),把板子搞脏。CLI **无 via-create**:电源缝合过孔靠 `power-planes`(plane 网)或 `route-short --route-power`(信号层电源,会打缝合过孔,但顺带画冗余走线)。

### Type-C 端子突出板框(P3 一次做对)
受体连接器(USB-C / DC jack)mating 面**突出板框 ~0.5–1mm**(焊盘留板内,板框在端子正下方内缩让位)——P3 定板框时做,别等布线后再改(返工)。见 `pcb-layout-conventions.md §2.2`。

### 布线排序铁律(2026-07-13 实测踩过)
- **电源先于信号**(P7.0):次级电源轨(如 5V)**必须在信号自动布线之前**先布好并锁定——留到最后布,信号已把板占满(全锁),散布的电源焊盘会被**围死**,route-short 和原生自动布线都布不动。
- **散布电源轨走信号层铺铜,不穿细线**:8 个散布 5V 焊盘无法用细线干净连;焊盘在顶层就**顶层铺一片 net-bound pour** 直连(`pour-fit --net 5V --layer 1`)。**同层多网 pour 靠优先级**:顶层 GND pour 优先级高会把 5V 挤没 → **删掉/降低竞争的 GND 顶层 pour** 让 5V 独占该层(「2 层接地」由 GND 内电层 + 底层 GND pour 兜)。
- **tier② 原生自动布线** 布信号:route-short 84 clearance 的板,原生路由器 Track-to-Track 降到个位数(推挤/撕绕/规则原生一致);前提=P7.0 关键网/电源先布并 `track-lock`,对话框选「保留 + 只顶底层 + 忽略已在平面的电源网」。
- **孤立铜清理**:被 `track-lock --all` 锁住的残留短轨桩 rip 不掉 → 先 `track-lock --net X --unlock` 再 `rip-up --net X`。
