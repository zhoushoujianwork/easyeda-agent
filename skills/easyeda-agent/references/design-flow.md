
# EasyEDA Design Flow — 首席EDA工程师流程脊柱

你是**首席EDA工程师**。整板/非平凡设计**不允许**「边想边随手摆」——那正是覆盖、外围乱飞、线压元件的根因。
按下面的**分阶段 + 硬门禁**走:每个阶段有明确产出和**过门条件**,**门不过不进下一阶段**。

> 这是**编排层**,不重复规则或动作细节:
> - 具体动作(place/wire/modify/move/align/…) → [schematic.md](./schematic.md) / [pcb.md](./pcb.md)
> - 设计规则(分区/间距/朝向/选型) → `easyeda-agent` references
> - 本 skill 只负责**顺序、分组、门禁、自调闭环**。

## 阶段目录(TOC)

深在某阶段想回看别阶段的约束/顺序,先扫这张目录定位(💾 = 该阶段过门后 save 检查点):

- **原理图 S0–S6**:S0 设计方案书 · S1 图纸/分页 💾 · S2 模块编组 · S3 按组摆放 💾(S3′ 分区收敛 `zone-arrange` 按需)· S4 通道布线 💾 · S5 校验门(gate 五关,含 clusters)· S6 调整闭环 💾 · 录制/演示模式(**固定步骤速查:下方「原理图 SOP 步骤卡」**)
- **PCB P0–P10**:P0 新板 · P1 导器件 · P2 摆放 · P3 板框 · P4 禁布区(靠前)· P5 丝印对齐(靠前)· P6 可布性门 · **P7 布线**(三档阶梯 + **P7.0 关键网先行** + **自动布线对话框清单** + P7.9 beautify)· P8 叠层+电源+铺铜 · P9 引脚级丝印/极性 · P10 DRC+check 门 💾 · 反模式

## 核心原则

1. **先规划,后落子。** 没分页、没编组之前,一个元件都不放。
2. **先确认图纸,默认 A4。** 生产级布局必须先有可读 sheet primitive。若 `easyeda sch sheet-geometry` 找不到图纸/边界,立即停止:让用户在 EasyEDA 选择/创建默认 A4 图纸,或明确批准 debug/临时路径。**无图纸不允许摆放、布线、autolayout apply。**
3. **按图纸容量分页。** 先按 A4 可用区评估模块面积和布线通道;放不下就自动分页、按模块拆页,不要把单页坐标外扩当作解决方案。
4. **按组摆,不按件摆。** 芯片和它的外围(去耦/晶振/上下拉/接口)是**一个整体**,一起定位、一起移动,组与组之间留出布线通道。
5. **每步可验证。** 摆放后用 `easyeda sch layout-lint` 拿**机械真值**判覆盖/间距,而不是靠肉眼或截图(截图可能 stale)。
6. **门不过就回退。** layout-lint 有 ERROR、DRC 有 fatal → 立刻调整,不带病往下走。
7. **交互密度按模式分档,不再默认每阶段都停。** 全自动/里程碑确认/逐步确认三档怎么选、哪些坑永远不问用户——见下方「交互模式(Interaction Modes)」一节。
8. **过一阶段就存盘(硬规则)。** `place`/`wire`/`modify` 只改 EasyEDA **内存**,不 save 就**不落盘**——窗口重载、daemon 重启、EasyEDA 崩溃都会**丢未保存的工作**。daemon 默认开了**防抖 autosave(3s)** 兜底(变更停 3s 自动 `schematic.save`),但它是安全网不是替代:① 防抖窗口内进程挂掉仍丢最后几笔;② autosave 可能被 `--autosave-debounce 0` 关。所以**每个阶段门通过后仍显式 `easyeda sch save` 一次**(见各阶段 💾),即时落到已知良好点。本流程里 save 是既定步骤;若当前是逐步确认模式,save 前也报告并等待确认。

## 交互模式(Interaction Modes)

「先想清楚、再动手」不等于「每一步都等用户确认」——那会拖垮回归基准和无人值守场景。
按用户意图/场景在下面三档里选一档,而不是让逐步确认变成默认行为:

| 模式 | 确认点 | 场景 |
|---|---|---|
| **全自动(auto)** | 0(仅破坏性操作确认) | 回归基准、CI、ClawFlow operator、录制脚本 |
| **里程碑确认(milestone,真实用户默认)** | 3 处:① S0 设计方案书确认 ② 原理图完成、转 PCB 前 ③ 发板/交付前 | 正常客户设计 |
| **逐步确认(step)** | 每个 S/P 阶段都停,只做读回/报告/建议,等确认再继续 | 教学、演示、用户显式要求「每一步等我确认」 |

**问不问的判据只有一条**:用户的回答会不会改变实际做法——会改变才问,不会改变就不问。据此把坑分两类:
- **Guardrail(不论哪个模式,永远不问用户)**:有唯一正确答案的坑——save 纪律、mutation 后 `doc reload`、layout-lint/DRC 硬门、天线 keepout 必须覆盖全层等——继续以机械门禁形式内置在流程里,不进入交互。
- **Decision(真实权衡,进设计方案书由用户拍板)**:叠层与层数、地策略、接口取向、选型成本档位等——见 [`design-decisions.md`](./design-decisions.md),在 S0 一次性摊开选项+坑+推荐,让用户选,而不是在 S2/P4/P8 执行时才发现是权衡。

## 阶段流水线(原理图)

```
S0 设计方案书 → S1 图纸/分页💾 → S2 模块编组 → S3 按组摆放💾 → S4 通道布线💾 → S5 校验门 → S6 调整闭环💾
                                            ↑___________________________________|
```
> 💾 = 该阶段通过后 `easyeda sch save` 存盘检查点(见原则 5)。整板放置时,S3 每放完几组(或每 ~10 件)就 save 一次,别等全放完——崩一次就白干。

### 原理图 SOP 步骤卡(固定步骤,执行时照表走;每步细节见下方对应 S 节)

| 步 | 做什么 | 固定命令 | 过门判据(不过不进下一步) |
|---|---|---|---|
| S0 | 方案书:选块选型、网名表、分页计划、架构决策 | `blocks ls/search/show` → spec 写盘 → `easyeda spec validate --strict`(落块后位号回填走 `easyeda spec backfill … --write`,或 `block-apply --spec`) | **首个 page-new/place/block-apply 前** validate 无 ERROR;milestone 档经用户确认 |
| S1 | 图纸/分页 reconcile 到模块计划 | `sch pages` → `page-rename`/`page-new`/`page-delete` → `sch sheet-geometry --json` | 页集合=模块计划;每页有 A4 sheet 💾 |
| S2 | 分区规划(只规划不落子) | 块路径读虚拟组;手工页 `sch zones set` → `sch zone-plan --json` | 六项 validation 全 0 **且 `labelScopeDegraded=false`**(降级=判据验不了,不是"没问题") |
| S3 | 按组摆放(块优先;命中块 S3+S4 一条命令) | `sch block-apply <id> --bind 端口=网名 --spec <s0.json>`(`--spec` 顺手回填位号)/ `sch autolayout --engine template` / `sch place`+`modify` | `sch gate --only layout-lint,clusters` 无 ERROR 💾。⛔ 报 `page-too-small` = **停手问用户**(独立成页/继续分页/收小组),工具不自动分页;人肉重试由 `--max-attempts`(默认 3)机械叫停 |
| S3′ | **分区收敛(按需)**:分区拥挤 / `partitionOverlap`>0 / 重整已放置页 | `sch zone-arrange`(纯规划,唯一解;phase B = 边归属+多层货架+回溯)→ `--apply`(断言①②+假失败清创+分级回滚+**断言③落地复判**) | verdict=pass 且断言①②③绿(③ = 落地实测框 vs 规划框偏差 ≤ gutter、区框零重叠、**无成员探出图纸**、**retain 区几何未被改动**、**无自由落点 pin**)。**断言③红时不要「多跑几遍」** —— 桩线伸展已统一为一把尺,再跑一轮只会追尾(真机 4 轮取证:每轮 dry-run pass、落地必重叠);按复判表定位是哪个区胖了。**这条现在有机械兜底**:`block-apply`/`destagger`/`group-move` 的 `--max-attempts`(默认 3,跨调用失败签名台账)会在同一个结果第 3 次出现时**停手并给结论**,画布零改动;而一旦读到 `page-too-small` 就**别再做任何几何微调**,直接进拆页决策(见 S3 停点)。注意「规划框 = 落地框上界」**只在模型内成立**(同一份 pin 坐标+桩长),真机 MCU_IO 六区实测偏差 +141/+126/+82/+56/+26/+10,断言③ 的职责就是把不成立的那几次报出来。报「N 只 pin 走了自由落点」= 计划根本没覆盖那几只脚,它们的方向/桩长不在规划里,先看是不是有 pin 靠普通导线/netlabel 连着。`blocked` 时先看 phase A 那一栏收敛了没(**排不下的是形状不是面积**)——phase A 现在会**域感知选形**(候选先比可排布档位、再比「本页有几条通道装得下」,平局才回到原有紧凑序),所以 `zones[].mode` 尾巴会直说它选了什么形态、为什么;读到「没有一个装得进任何通道」就是真的要拆(**别去调纸张/带高**),再考虑 `page-new` 拆页 —— A4-only,不换纸。phase A 行首的 `↩` = **「不得变差」门回退了这一区**(收敛会让它在本页**更难排**,于是保留原形;理由在 `zones[].retainWhy`)—— 那是保护不是故障,**别去改 A4 尺寸/带高绕开它**,要么把该区拆小要么拆页;门比的是**三档可排布性**(`2` 有落点 / `1` 只被图签挡 / `0` 连可用域都装不下),掉档才回退 —— 所以「原形也排不下」不等于放行,`1 → 0` 照样拦(2026-08-20 第二轮真机 `449×737 → 244×863` 就是从首版那个单布尔判据里漏掉的);两个形状都是 `0` 档时门不拦,phase B 报「纸面放不下」= 真的要拆。`↩` 区在 `--apply` 里受**刚体不变式**硬门保护(逐 pin 比对方向/桩长/类型,不一致就拒绝整页、画布零改动) |
| S4 | 通道布线(块外的连线) | `sch autoconnect`(电源/地/netport)/ `sch wire`(信号) | 无穿件压线 💾 |
| S5 | 校验门(机械真值) | 多页统一运行 `scripts/schematic-acceptance.py`(内部逐页 strict gate + 全工程 nets + 黄金对账) | 每页 verdict=pass;或仅显式挂账 v1.1.1 `missing-titleblock`;无网名变体/单引脚网;意图对账无差异 |
| S6 | 调整闭环 | 按页修一个 finding 类别 → 先跑便宜门 → 最终只重跑一次 acceptance | acceptance 通过 → `sch save` 确认 `saved:true` 💾 |
| S6′ | 交付三件套(默认必做) | `sch zone-draw --mode partition` + `sch note --zone <模块>` + ~~`sch titleblock`~~(⚠图签写入当前禁用:写路径损毁 sheet 引用→重启丢图框,见 actions.md;留白如实报) | `sch check` 无 missing-partition/note(titleblock 挂账) |

> `blocked` ≠ `fail`(检查器没跑成,先修环境别改电路);判状态看数据不看截图;每过门显式 save。

**随时问「我在哪一步」:`easyeda sch status --all-pages`**(加 `--gate` 连 S5 一起验)。
它**当场从画布算**每一格,不读任何记录 —— 因为记录会撒谎:`workflow status` 曾把
imported/placement_ready 打成实心圆,而那块 PCB 上一个器件都没有。四种状态里
**`?` unknown 是一等公民**:它是「本工具判不了」,不是委婉的「没做」。有页读不到时
**整张判定降级为 unknown**(同 gate 的 `blocked`:检查器没跑完 ≠ 板子没问题),
绝不拿读得到的那几页宣布「已就绪」。判不了的两格是有意留白的:S5 要跑 gate(`--gate`),
S6 平台不暴露脏标记(只能显式 `sch save` 并确认 `saved:true`)。

### S0 — 设计方案书(Design Proposal)
- **时序门**:S0 spec 必须在首个 `page-new`、`place` 或 `block-apply` 前落盘并通过
  `easyeda spec validate --strict`。先画后补 spec 只能算事后记录,不能阻止选型、分页和
  统一网名返工；不要靠创建 `*_NEW` 临时页代替容量规划。
- **做什么**:读懂设计——器件清单、电源树、功能模块划分、目标幅面;**并在放置第一个元件之前**,把覆盖原理图 + PCB 全程的架构决策一次性定下来,而不是让它们在 S2/P4/P8 执行到才被想起(实测最贵的返工正是决策后置:天线 keepout 后置逼已布线的模块重绕,地策略选错要重铺)。
- **怎么做(轻量摸底)**:见 conventions 的 `design-pre-analysis.md`(器件清单、电源树、功能分组、幅面估算)。`easyeda health` 确认已连接。
- **怎么做(架构决策)**:查 [`design-decisions.md`](./design-decisions.md) 里的决策点清单——叠层与层数、地策略(单 GND PLANE vs 分区 pour + 桥地)、接口取向(如 USB 单/双取向)、器件选型成本档位等。**每一条都要把该文件里的选项、已知坑、推荐方案摊开给用户看,由用户拍板,不能替用户默认选**——这是设计方案书阶段的核心产出,不是可选步骤。RF/天线 keepout 范围**不在**这份决策清单里——它是唯一正确答案的 guardrail(必须覆盖全层),不摊开选项;S0 只需把 RF 器件位号和 `"all"` 层范围写进 spec 的 `rf` 字段供 P4 直接读取执行。
- **产出**:模块清单(如 MCU、电源、USB、传感器、调试口…)+ 每个模块的器件归属 + 一份**机器可消费的设计方案书 spec(JSON)**。后续阶段(S2 模块分区、P4 禁布区、P8 叠层/电源/铺铜)从这份 spec **读取执行,不重新决策**——方案书写下的决策与执行阶段的做法不一致视为 bug。

  **方案书 spec 形状**(与 `sch autolayout --spec` 同一路数——一份可回读、可复用的 JSON 文件,不是写完即弃的长文;字段可按项目取舍,关键是稳定、后续阶段能直接引用):

    {
      "modules": [
        {"name": "POWER",   "kind": "POWER", "parts": ["U2","L1","C1","C2","F1"], "page": "POWER", "zone": "left"},
        {"name": "MCU",     "kind": "MCU",   "parts": ["U1","C18","C19","R6"],    "page": "MCU_USB", "zone": "center"},
        {"name": "USB_HUB", "kind": "USB",   "parts": ["J2","U10","X1","C30","R15"], "page": "MCU_USB", "zone": "left-top"},
        {"name": "ANT",     "kind": "ANT",   "parts": ["ANT1"], "page": "MCU_USB", "zone": "right"}
      ],
      "flow": ["POWER", "MCU", "USB", "ANT"],
      "flowAxis": "auto",
      "pages": [
        {"name": "MCU_USB", "sheet": "A4", "modules": ["MCU","USB_HUB","ANT"]}
      ],
      "stackup": {
        "layers": 4,
        "groundStrategy": "plane",
        "innerLayers": ["GND", "VCC_3V3"]
      },
      "assembly": {
        "profile": "hand-solder",
        "side": "top"
      },
      "rf": {
        "parts": ["U_WROOM1"],
        "keepoutLayers": "all"
      },
      "board": {
        "outline": "compact"
      },
      "interfaces": [
        {"name": "USB_C",      "ref": "J2", "edge": "bottom", "facing": "user-facing",
         "orientation": "dual", "plugWidthMm": 13.0},
        {"name": "备份电池座",  "ref": "J1", "internal": true},
        {"name": "IPEX 天线座", "ref": "E1", "edge": "any"}
      ],
      "costTier": "standard"
    }

  **逐字段说明不在这里** —— 字段有 Go 类型 + 校验命令,散文抄一遍只会和代码漂移:
  `easyeda spec show <file>` 看工具**实际读到的**归一化结果,`easyeda spec validate` 报哪写错了,
  决策项的选项/坑/推荐见 [`design-decisions.md`](./design-decisions.md)。这里只强调三条容易漏的:
  - `modules[].kind` 是**受控词汇**(POWER/MCU/RF/ANT/IO/…),`flow` 里出现的就是这些值;
    不写就拿 `name` 去词表碰一次,碰不上该模块不参与 P6 的 flow-order 打分(标 skipped,不是满分)。
  - `interfaces[].facing` 是**设计意图不是几何属性**:同一个 PH2.0-3P 接箱内电芯是 internal、
    接箱外传感器就是 user-facing,铜箔再精确也推不出来。写了才升 WARN,不写只能启发式推定报 INFO。
  - `board.outline` 没给尺寸就写 `"compact"` —— 无信息时紧凑是正确目标,不要摊大饼。

  **写完必须校验**(#167 起 spec 有 Go 类型 + 校验命令,写错不再静默):

  ```bash
  easyeda spec validate .easyeda/s0-<project>.json           # ERROR 才非零退出
  easyeda spec validate .easyeda/s0-<project>.json --strict   # 交付前用:WARN 也失败
  easyeda spec show     .easyeda/s0-<project>.json            # 看「工具实际读到的」归一化结果
  ```

  **位号回填进流程,别再「每次落块回头改 json」**(#181):`modules[].parts` 写的是
  **designator 字符串**,而平台会在 create 时按它自己的全局位号重编(计划 C1 → 落地
  C11,issue #144),我们照实回读 remap —— 画布和虚拟组都是 C11,只有手写的 spec 还是
  C1。**位号对不上不会报错**,只会让 `zones set --spec`、partition 打分、连接器规则
  **静默少算一个模块**,报告照样绿。两条路二选一:

  ```bash
  easyeda sch block-apply <块id> --spec .easyeda/s0-<project>.json   # 落块后自动回填
  easyeda spec backfill .easyeda/s0-<project>.json --project <project>          # 事后补;默认只预览
  easyeda spec backfill .easyeda/s0-<project>.json --project <project> --write  # 落盘
  ```

  **完全离线**(事实来源是 workflow 里的持久虚拟组,不需要连接器/不需要开 EasyEDA)。
  匹配规则**声明优先于猜测**:模块写了 `block` 就按块 id 认它的虚拟组(含全部功能子群),
  没写就按「组名末段 == 模块的 `zone` 或 `name`」;同一个块被两个模块声明 = 歧义,
  **两个都跳过并报出来**(不替你做设计决定)。写入是**外科手术**:只替换
  `modules[i].parts` 那一段字节,键序/缩进/未知字段/notes 结构一个都不动
  (整包 Unmarshal→Marshal 会静默丢掉它们);任一模块定位不到就**整体拒写**
  (半改的 spec 比没改的危险)。

  判定口径刻意宽松以兼容既有 spec:**ERROR** = 写了但写错(枚举外的 zone/kind/facing、flow 里重复或不存在的阶段、自相矛盾的 internal/facing);**WARN** = 缺了会让某维测不了;**INFO** = 能力降级。**既有 spec 全部继续能读**,缺新字段只报 WARN/INFO,不会一夜作废。
- **过门条件**:上面这份 spec 已经**过用户确认、落成了文件、并且 `easyeda spec validate` 无 ERROR**(与 `autolayout --spec` 文件同样对待——写到磁盘,可在后续阶段被引用,不是只停留在对话记录里);不再是「每个器件归到了某个模块」这么单薄。这正是「里程碑确认」模式的第一个确认点;若当前是「逐步确认」模式,同样在这里停住等确认(见「交互模式」一节);「全自动」模式下按已有 spec 或默认推荐值直接产出文件,不阻塞。

### S1 — 图纸 / 分页(先图纸,再分页!)
- **做什么**:确认当前页有图纸,默认 A4;再按模块/功能把设计**先分到几页**(电源一页、主控一页、接口一页…),别全堆一页。
- **怎么做**:`easyeda sch pages`(或 `doc ls`)读页结构 → `easyeda doc switch` 切目标页 → `easyeda sch sheet-geometry --json` 读 sheet/title-block。无 sheet 或 provenance 为 none 时停止,不要开始 place。
- **页面对齐模块计划(用户点名·必做)**:分页不是照单全收用户现有的页——**要主动把现有分页 reconcile 到模块计划**:①页名无意义(`P1`/`P2`/`Schematic1`/`page1`)或与模块不符 → `easyeda sch page-rename --page <uuid> --name <功能名>`(如 `POWER`/`MCU_ESP32`/`USB_DEBUG`)。⚠**页名只写功能域,禁止把 `P1`/`P2` 序号编进页名**(用户裁定):页签顺序由平台维护、且会变——删页重建时新页排到**末尾**,名字里的序号当场与实际页签顺序脱节(ceshi 实测踩到:页签位置 2 是 `P3_USB_DL`、位置 3 是 `P2_MCU_IO`,读图人先信哪个都不对)。顺序是页签的职责,名字只负责说清"这页是什么";②模块比页多 / 复杂模块要独立 → `easyeda sch page-new` 补页;③**多余空页 → `easyeda sch page-delete --page <uuid>` 删掉**(先确认该页无器件,`sch pages` + 逐页 `list` 核实;page-delete 无 undo,属破坏性,删前 inspect)。单页小板:把唯一那页也 `page-rename` 成有意义的名(别留 `Schematic1`),不必强行分多页。目标:**页集合 = 模块计划**,一页一功能域,跨页同名 `net_port` 接续。
- **💾 过门条件**:页集合与模块计划一致(该改名的已改、该补的已补、多余空页已删);每个目标页都有可读图纸(A4 默认)和明确职责;每页模块预计能落在可用区内,标题栏 keep-out 明确 → `easyeda sch save`。若用户要求逐步确认,保存/继续前停住。

### S2 — 模块编组
- **做什么**:在每页内,把「芯片 + 其外围电路」定义为一个**组**,并规划各组在页面上的**分区位置**
  (谁在左、谁在右、信号流向)。此阶段只规划分区,**先不落子**。
- **归属读 S0,不重新分配**:组的 page/zone 来自方案书 spec 的 `modules[].page` / `modules[].zone`,
  这里只是把已定的分区落成具体矩形。分区/信号流向规则见 conventions 的
  `schematic-layout-conventions.md`;zone 必须落在 S1 读到的 sheet 可用区内。
- **认领 + 画框**(动作细节见 [`schematic.md`](./schematic.md) 的 *Functional frames + text labels*):
  1. **模块归属不用手工认领** —— `sch block-apply` 落块时已按**功能子群**把件封成虚拟组
     (`flow` 的每一级 + 跟着它的 attach 去耦 / pair 并列组),`zone-plan` 直接读组:
     那是「哪几件是一个功能单元」的**单一事实来源**,不再抄第二份。
     `sch zones set` 只在两种情况下才需要:**手工搭的页**(没有块、没有组),
     或**布局之前**给 `sch autolayout` 指定模块该落在纸面的哪一格(那时件还没放,
     谈不上虚拟组)—— 它的 `zone` 名(left/center/right…)只有这一个消费者了。
  2. `easyeda sch zone-plan --json` 先出方案(纯计算、不落笔,六项 validation 必须全 0),
     再 `easyeda sch zone-draw` 落笔。**分区框一律数据驱动** —— 从活体模块 bbox 反推,
     按模块之间的自然空隙切分整纸、给右下角图签留缺口。固定九宫格模式已废弃
     (框的几何与电路实际位置无关:单模块页铺满整纸时框套不住电路、框里大半是空的)。
  3. 认领与框都**按页(documentUuid)持久化**,多页工程逐页 `--doc <页>` 画,
     绝不把 MCU 页的认领套到 Power 页。
  4. **`--mode partition`** 按真实 bbox 把整纸切开,并给右下角图签留缺口。
  5. **单页小板也要画区框** —— 「最小 / 单页」不是省略分区的借口。可以不分页,
     但区名框和电路说明照画。
- **⚠️ 只有 `sch autolayout --apply` 会自动画框** —— 手工 `sch block-apply` / `sch place`
  路径**不会**,必须显式补「画框 + 写说明」两步,否则等于没分区(最常见的漏)。
- **每模块配 1~3 行电路说明**(`sch note`):写**作用 + 关键参数**,例如
  「LDO: 5V→3V3 1A」「BOOT: GPIO0 拉低进烧录」。区名框只负责命名,说明才让人读懂;
  文字放模块框下/旁,别压电路(放完 `sch layout-lint` 复核)。**摆放没画框 + 没写说明
  = 布局未完成**,别当成品交付。
- **`zone-draw` 报 `partitionOverlap` 时怎么办**:一个虚拟组 / 一次认领 = 一个框
  (与 `zone-arrange` 同一把尺,不靠合并遮掩),非 0 就是**两个区的体积真的互相压**
  —— 跑 `sch zone-arrange --apply` 重排,或 `sch group-move` 挪件;调 `--gutter` 治不了。
  反过来:`zone-arrange` 断言③绿的页,一定画得出来。
- **机械兜底(别靠记忆)**:`sch check` 有 **`missing-partition`** 检查项——多器件页
  (parts ≥ 6)没有分区框就报 WARN,`sch gate --strict` 会因此 FAIL,挡下未分区的板。
  看到它就补「画框 + 写说明」再交付。
  **判据的证人改成了画布(#181,流程不变)**:认框靠页上那条**区标题文本**
  (内容恒为模块名拼接 `A / B`,与 `zone-draw` 生成标题用**同一个函数**;`sch check`
  本来就拉了整页 `text.list`,认框零新增 I/O),本地绘制记账仍读,**两个证人取大**。
  所以「换机器 / 清了 `~/.easyeda-agent` / `--project` 前后写的名字不一样导致读了
  另一份 state」这三条记账丢失路径,不再造成**画布上明明有框、check 永远说没有**的
  恒报。**口径没有放宽**:真没画框的页照报——有虚拟组**不免检**(铁律#15 要的是框,
  组只影响报文措辞),认出的区标题也会同时计进区名标签,不会顺手把 `missing-note`
  静默关掉。(挂账:`sch zones status` / `sch status` 仍只读记账。)
- **过门条件**:每个组有明确的目标矩形(已认领),组间预留通道(分区不重叠);
  模块太多就拆到下一页,而不是挤压本页。

### S3 — 按组摆放(芯片 + 外围一起)
- **做什么**:**逐组**放置——先放该组核心芯片,再把它的外围**就近**放在芯片周围(去耦贴电源脚、晶振贴时钟脚…),放完一组再下一组。
- **块优先(电路块库)—— 命中块时 S3 和 S4 合并成一条命令。**
  摆放/接线任何**标准外设模块**前先查块库(离线,不需要 daemon/窗口):
  `easyeda blocks ls` / `blocks search <关键词>`(按**功能 / 芯片 / 端口网**三维轮换搜,
  一词没中换维度)/ `blocks show <id>`。命中就 `easyeda sch block-apply <id> --bind PORT=网名`,
  它**一次做完**:放件 → 用锚件实测引脚求解其余件 → 实测推让(给 marker 腾通道)→
  **布线前硬门**(真实 bbox/引脚,重叠或引脚重合就回滚,绝不打印假绿)→ autoconnect 连线 →
  网表对账 → **虚拟组体检**(`clusters`)→ 登记虚拟组。所以命中块的模块**不用再走 S4**,
  直接进 S5 复验即可。
  - 块的 `parts` 直接给出 `standard-parts.json` 的 role,**选型这步都省了**;引脚按功能名引用,零改号。
  - 块的 `schematic_layout` 有两种形态:**关系形态(推荐)**只声明 `flow`/`attach`/`pair`,
    **一个坐标都不写**,几何由求解器按实测引脚 + 页面碰撞 + 图纸边界算(细节见
    [`schematic.md`](./schematic.md));**legacy 绝对偏移**仍支持但已废弃 —— 块作者写模板时
    根本不知道实例会落在页面哪里、图纸多大,手算必踩出界/顶图签。
  - 块还带 **PCB 多维约束 map**,各 P 阶段按 `target` 匹配读:`pcb_layout` / `placement` /
    `signals` / `silk`。无命中才手接;手接并验证过的新外设按
    [`standard-blocks-contributing.md`](./standard-blocks-contributing.md) 回流入库。
- **怎么做(非块路径)**:`easyeda sch place` + `sch modify`(设位号);坐标按 S2 的分区。
- **整组分区摆放优先用 `easyeda sch autolayout --engine template`**(默认引擎):把 S2 的分区写成
  `--spec`,它按真实 bbox 把核心放到分区中心、外围环绕、碰撞重试,保留引脚 fanout 通道 +
  图签 keep-out,**确定性产出可过 layout-lint 的坐标**。先 `--dry-run`,确认后 `--doc <页> --apply`。
  三条硬约束:**必须在布线前跑**(v1 是 parts-only,页上有任何 wire/bus/marker 就硬拒绝)、
  **必须有真实 sheet bbox**、**只移动已放置的器件**(不创建缺件)。`--apply` 成功后自动画分区框
  (#142,`--zone-draw=false` 关)。失败按规划快照逆序恢复并回读证明,不伪装事务。
- **兜底引擎 `--engine official`(块和 spec 都没覆盖的散乱页)**:官方
  `sch_Document.autoLayout()` @beta。⚠ **破坏性、只在布线前用、需目标页前台、~2min、
  没有事务回滚**。三个实测坑与 `--rewire` 安全管线见 [`schematic.md`](./schematic.md)。
- **优先级铁律**:命中块 → `block-apply`;有 spec → `--engine template`;都没有才 `official`,
  且**成品/已连线页一律别用它**。
- **摆完可自评质量**:`easyeda sch layout-score` 给布局可读性打五维分(标签折叠/标签反向/外围贴核心/长链挤压/版面整洁),低分维的归因**自带填好真实位号坐标的 fix 命令,照抄执行即可修**;它是诊断视角不是门(无 `--min-score` 永远 exit 0),门仍是下面的 layout-lint + check。
- **⛔ 停点:`page-too-small` = 停下来问用户,工具不自动分页(#181,S2/S3 都可能撞到)**。
  `sch block-apply` 落块后的虚拟组体检、以及 `sch clusters` 的 **`pageTooSmall`** 字段 /
  `BLOCKED page-too-small` 行,说的都是同一件事:**这个块/组本身就比整页可用区大**。
  - 判据没造新尺:直接调 `zone-plan` 在用的 `fitsAroundCorner`,可用区 = **实测**图框
    bbox 内缩 + 按实测长宽比匹配出的图签 keep-out(所以 A4 横版和 A3 天然不同)。
    形状是 **L 形不是矩形**(图签占右下角):框有两条活路——① 待在图签**左侧**的窄长条
    (宽 ≤ leftW,高不限);② 绕到图签**上方**的整幅(宽不限,高 ≤ aboveH)。两条都不
    成立才判 `page-too-small`。量不出可用区、或一个位号都没匹配上时**不下结论**
    (猜出来的 `fits` 会掩盖掉要报的那句话)。
  - **它和 `out-of-sheet`(探出图纸)是两种病**:那个是「摆得不好」,挪一挪能解;
    这个是「装不下」,**再挪、再压 `--per-row`、再调 margin/gutter 都不会让它变小**。
    读到它就**立刻停止几何微调**——继续微调正是复盘里 8+ 轮、~40% 活跃时间的来源。
  - **分页是设计决策,工具只停手不建页**:把三条出路摊给用户拍板 ——
    ① **独立成页**(`sch page-new --name <页名>` 后在新页 `block-apply`);
    ② **继续分页**(把本页其余模块搬走,腾出整幅给它);
    ③ **把组收小**(`sch clusters` 看「组高 vs 本体高」:被自己**竖排 marker** 撑大的脚用
    `sch disconnect --pin X:n` + `sch connect --pin X:n --direction left|right` 改标签朝向
    —— 实测本体 21 高的电容组高 134,改横向后 58)。**A4-only,不建议换纸**(平台也没有改
    图纸尺寸的 API)。
- **别人肉重试:`--max-attempts`(默认 3)会替你停手(#181)**。`sch block-apply` /
  `sch destagger` / `sch group-move` 各带一本**跨调用**的收敛台账
  (`~/.easyeda-agent/workflow/converge-<project>.json`,与工作流状态同目录但独立文件),
  只记**失败签名**:签名相同 = 原地打转 +1,签名一变(重叠 3→1、换了落点)或成功即
  **清零** —— 真在往前推的人永远撞不到上限。撞上限时命令**在任何 mutation 之前**停,
  **画布零改动**,并复述那句可执行的下一步(上一轮实测量到 `page-too-small` 时,连第一次
  都不放行)。`--max-attempts 0` 关掉上限(确认还要再试一次时才用)。
  代码里**没有无界循环**,这个上限治的是**人/agent 的重复调用**。
- **`sch destagger` 跑满 `--max-rounds` 的三档判词别混**:有进展→加轮数(不是停手)/
  一个都搬不动→停手换手段 / 被逐条跳过→按 `skips` 的理由处理。
- **💾 过门条件**:进入 S4 前跑 **`easyeda sch gate --only layout-lint,clusters --doc <页>`**
  (此时还没连线,电气三关跑了没意义;`--strict` 留到 S5 交付门,S3 阶段先把**硬伤**清零:
  重叠、引脚重合、出图纸)。**只看 layout-lint 是不够的** —— 它默认排除全部非 part
  图元,标签之间、标签压器件、标签探出图纸它**结构上看不见**,那一半在 `clusters`。
  两关都要求:本组无覆盖/引脚重合、锚点在 5-unit 网格、模块不越认领 zone、满足 `--min-gap`、
  没有因缺 bbox/pin 几何而跳过的器件;有 zone claim 时 sheet/zone check 必须可用。
  **默认非 strict 只供诊断,不能用 `0 overlap` 冒充布局已完成。**
- **有 ERROR 先回 S3 调整**:单件 `sch modify`;成排对齐 `sch align --mode centerx/top…`;
  等距摊开 `sch distribute --axis x/y`(均默认 dry-run,`--apply` 落地并自检 overlap);
  整簇平移 `sch group-move`;实在挤不下 `sch autoplace-free`。过了就 `easyeda sch save`
  (整板放置每 ~10 件存一次,别等全放完)。
- **分页 + 分区框 + 电路说明,三件套齐了才算原理图组织完成**(默认必做,不是装饰 ——
  用户反馈:成品图没分区没说明 = 交付可读性差):
  - 分区框:`sch autolayout --apply` 会自动画(#142);手工 `sch place`/`modify` 路径**不会**,
    须显式补 `sch zone-draw`(整纸版式 `--mode partition`),`sch zones status` 查是否已可视化;
  - 电路说明:每个模块 1~3 行(`sch note --text "LDO: 5V→3V3 1A\n输入/输出各 100nF" --x … --y …`),
    写作用 + 关键参数 + 设计要点,放模块框下/旁的空白,字号默认 10 低于区名;放完跑一次几何门
    核对无 overlap(`sch text-list` 枚举、`sch prim-delete` 清理)。
  - 单页/单模块小板可免分页,但区名框 + 电路说明仍要画。
  - **这三件现在是机械判据,不再靠自觉**:`sch check` 出
    `missing-partition`(没画分区框)/ `missing-note`(没有电路说明,区名标签不算)/
    `missing-titleblock`(图签的标题、设计者、板名空着或还是默认 `Board1`),
    在 **`sch gate --strict`** 下阻塞 —— 也就是交付门过不了。存在性之外还有
    **归属判据 `note-outside-zone`**(放对没有):每条 `--zone` 登记的说明,其渲染
    bbox 必须被该区分区框(zone-plan 规划框,与 zone-draw 画的实框同源)包含,
    框外报 WARN 并带说明坐标/框范围/可执行修法(`prim-delete` 旧说明后重跑
    `sch note --zone <区>` 自动落点,或显式 --x/--y),同样在 --strict 下阻塞。
    版式:**区名在框的左上角,电路说明在框的左下角,两者都在框内** —— 分区框顶部有
    标题带、底部有说明带,`sch note --zone <模块>` 会**贴着框底**落进说明带
    (`note.y = 框.minY + 16`,与行数/字号无关,同页所有说明底边齐平)。
    **说明带恒在框底,不会翻到框顶**(区名左上、说明左下是版式契约,同页所有说明底边
    齐平才读得下去)。框底被图签/纸边顶死、底带高度归零时 → **如实 blocked**,报文给出
    「区名 + 纵向差 + 两条出路(区内收敛 / 拆页)」,照做后 `sch prim-delete` 旧说明再重放
    —— **别原样重跑 `sch note`**。
    **别手填坐标**(手填必然飘;显式 `--x/--y` 会被逐字保留,不会被贴底覆盖)。`--zone` 的名字**全名/末段短名/组 id/唯一前缀都命中**(与
    zone move 同一个解析器);区不在本页分区计划里时**不会静默兜底**——stderr 出
    warning、输出带 `zoneMatched=false`(`--json` 亦有该字段),看到 false 就说明
    说明没落进带,先查区名/分区计划。框内挤不下时它也**不会把说明甩到页角**:自动沿框四周走廊
    (正下→正上→右→左)逐档找贴着本区的落点,**且绝不落进别的分区框里**(邻区
    矩形是硬障碍),整页兜底也按离区距离就近排序——
    读图时说明始终对得上自己的区,不需要再手动 `--x/--y` 补救。**说明带高度按该区
    已登记说明的实际渲染高度预留**(不再是写死的单行高):多行(1~3 行)说明放心写,
    带装不下时框向外扩(下探),器件区不挤;高度只从登记说明的**内容+字号**推导、
    绝不读落点坐标,所以重复跑 `zone-plan` 幂等收敛 —— 框几何只随「登记了哪些说明」
    变,不随「说明落在哪」变。**外框只有一个函数**(2026-08-20 用户裁定):
    `frame = f(成员 L1 虚拟组全图元并集, 区名带, 说明带)`,`zone-plan` 与
    `zone-arrange` phase A 共用同一本体、同一份带高 —— **收紧时 title/note 就在账里**,
    不再是「按常量带收紧 → 画框 → 再放 note 装不下」。登记的说明**不反哺
    分区框几何**(框由器件内容反推,说明的家是框内说明带)——重画分区框不会因为
    说明越画越大。说明只写**需要注意的**(关键参数、易错点),
    一到两行,不要复述电路。含 `~`/`+/-`/引号/`%` 的说明文字是安全的(经 JSON 转义
    进平台);平台偶发吞文本创建请求,`sch note` 已自动 settle 重试一次。
  - **分区粒度要细,而且不用手工分**:`sch block-apply` 现在按**功能子群**自动归组 ——
    块的关系数据本身就说明了哪几件是一个功能单元(`flow` 的每一级 + 跟着它的 `attach`
    去耦 / `pair` 并列组),CH340C 因此自动拆成 `J_USB`(Type-C + CC 下拉)/ `D_ESD` /
    `U`(桥芯片 + 去耦)三组。整块糊成一个大框等于没分区 —— 框会摊到大半张纸、
    里面大片空白,读图的人得不到任何信息。
  - **组间留通道用 `sch group-move --group <id>`**(每组一个抓手,平移后自带电气自检;
    同块多个子组要一起挪时用 `--groups g1,g2` 一次整体移动,不撕裂组间共享导线)。
    它是**刚体平移**:重连按移动前实测的桩方向/长度原样重建,组框尺寸不变
    (2026-08-20 修复前,一次 `--dx 40` 会把组框从 315×389 撑到 523×406,
    重叠从 1 处变 3 处 —— 「挪一下让开」反而毁掉收敛)。
    `sch zone-plan` 的 `partitionOverlap` 就是「通道没留够」的判据:它非 0 说明两个功能
    子群的虚拟组在版面上交叠 —— 那时**不存在**既框住各自内容又互不重叠的矩形,
    只能挪件,不能靠画框迁就。**归属是一个虚拟组 / 认领一个框**(与 `zone-arrange`
    的区一一对应,2026-08-20 定案):此前 `zone-plan` 会把「同一网格带」的两个区
    合并成一个大框,于是 `zone-arrange --apply` 断言③ 全绿(逐区框零重叠)的页面,
    `zone-plan` 反而报重叠、`zone-draw` 拒绝画框(真机 MCU_IO)。合并那条路已删 ——
    重叠只会如实报出来,修法是 `sch zone-arrange --apply` 重排或 `sch group-move` 挪件。
    框的有无有**两个证人,取大**(#181):页上的**区标题文本**(画布直接证据,平台不
    提供矩形枚举接口,标题是唯一能反认的痕迹)+ 工具自己的**绘制记账**;`sch clear` 会
    同时作废该页的记账,免得清了页而判据还以为画过。

### S4 — 通道布线(留距离,别压元件)
- **做什么**:在组**摆放并过完 S3 几何门之后**再布线——信号走元件间的**空通道**,不要让导线压在元件或外围上。
  **命中块的模块跳过本阶段**:`sch block-apply` 已经连完线并对过网表(见 S3),这里只处理块之外的连线
  (块与块之间的接口网、手工搭的外围)。
- **怎么做**:布线/flag/去耦规则见 conventions 的 `auto-layout-sop.md`(模块内信号=短正交线，跨模块/跨页或长距离=命名 netport 短桩，flag 仅电源地，绝不穿引脚)。
- **电源/地/netport stub 用 `easyeda sch autoconnect`**(别再手猜 `connect --direction/--offset`):它按真实 bbox/引脚/已有 flag 几何打分,确定性选 direction+offset 再委托 `connect_pin` 落地,批量 `--spec` 还会自动错开标签。先 `--dry-run` 看计划,满意再落地。
- **💾 过门后**:`easyeda sch save` 存盘,再进入 S5。

### S5 — 校验门(机械真值,不是肉眼)

**逐页跑 `easyeda sch gate --strict --doc <page>`,再做一次设计意图对账。** 不要用
`--all-pages` 的浅数据冒充逐页证明(`--strict` 与 `--all-pages` 不兼容,多页就循环 `--doc`)。
多页工程优先运行 `scripts/schematic-acceptance.py`,由它统一核对页集合、逐页 gate、全工程
nets、pin→net/NC 黄金表和最终导出,避免漏页或重复执行同一组慢检查。

1. **机械门(一条命令)** `easyeda sch gate --strict --doc <page>`

   gate 把五个检查器按**固定顺序**跑完并出一张报告 —— 顺序、阻塞判据、退出码都写在代码里,
   不再每次现场决定(此前各命令各跑各的,agent 每次都要自己拼,拼法不一致就是不稳定):

   > **为什么 layout-lint 之外还要 clusters**:layout-lint 默认排除全部非 part 图元,
   > 而「标签压标签 / 标签压器件 / 标签探出图纸」恰恰只发生在这些图元上 —— 实测同一张
   > 画布上有 11 处标签重叠,它照样报 `✓ placement gate passed`。这不是阈值松,是**结构上
   > 看不见**。两关口径互补:一个判本体,一个判「本体 ∪ 它自己的 marker」。

   | # | stage | 阻塞判据 |
   |---|---|---|
   | 1 | `layout-lint` | **器件本体**的 `overlap` / `pin-coincidence`;strict 下 spacing、off-grid、**out-of-sheet**、缺失/畸形几何、sheet check `unavailable` 同样阻断 |
   | 2 | `clusters` | **虚拟组体积**(器件 + 只挂在它自己引脚上的 marker/桩线/文字):组间**图元级**重叠、组探出图纸可用区;strict 下组间过近也阻断。另出 **`pageTooSmall`** 字段(文本模式 = `BLOCKED page-too-small` 行):这一组**本身比整页还大** —— 与 out-of-sheet **是两种病**(那个挪一挪能解,这个挪多少次都不会变),读到它去 S3 的 `page-too-small` 停点,别继续微调 |
   | 3 | `check` | fatal / error 级 finding(悬空脚、导线交叉/穿脚、网络标识不一致、零长/悬挂线、`duplicate-net-marker`、`titleblock-overlap`、`marker-overlap`) |

   > `marker-overlap` 一片时**别直接 `sch modify` 挪标识坐标**(会把它挪脱导线端点 → 断网)。
   > **首选重跑 `sch autoconnect`**:落点现在按「长短两档标准长度」循环排 lane(相邻脚的标签
   > 自动错开),而且判定含**网名的实际渲染宽度**(平台给 netport 的 bbox 只有裸六边形,
   > 名字画在外面 —— 这是三把尺曾经集体报 0 的原因)。重跑还不干净,再
   > 跑 `easyeda sch destagger`(#171)——它按文字带尺寸量算方向/桩长、
   > 只动能安全重连的短桩。**`--apply` 已解禁(ADR-0004)**:执行走统一安全 move 内核
   > (整树删证+快照重连+电气对账,失败自动恢复),不再需要按计划手工逐个改;
   > 仍推荐先 dry-run 预览再 `--apply`。

   | 4 | `bridge-check` | `wire-bridge` 真短路(一棵 wire tree 带多个网名);orphan stub/flag/**tree** 是告警,strict 下阻塞。`orphan-tree` = flag+桩线成树却**不触任何引脚**(挪件残留)或裸死线 —— 修法 `sch prim-delete` 整树(wireIds+flagIds)删净;需连接器 ≥0.26.1,旧连接器对此形态报不出来(结构性盲区,2026-08-18 真机定案) |
   | 5 | `drc` | 官方 SDK fatal。**放最后**:最慢、需窗口前台,且聚合结果最不可行动 |

   **verdict 三态,`blocked` ≠ `fail`**:
   - `pass` — 全过
   - `fail` — **板子有阻塞问题**,照报告「下一步」修,回 S3/S4
   - `blocked` — **检查器没跑起来**(连接器断、页没打开、返回结构异常),原理图**从未被完整判定**。
     此时后续 stage 会被跳过而不是继续撞同一堵墙。先 `easyeda health`、`easyeda doc ls` /
     `doc switch <page>` 修环境,再重跑 gate。**别把它当成板子的问题去改电路。**

   `--json` 带每个 stage 的完整原生报告(`stages[].detail`),是四个单命令 JSON 的超集,不用重跑。
   局部复查仍可直接用 `sch layout-lint` / `sch check` / `sch bridge-check` / `sch drc` 单命令
   (它们原样保留),但**交付门走 gate**。窗口不在前台时 `--skip drc` 先过前三关。

   **v1.1.1 标题栏例外(精确 allowlist,不是通用忽略 WARN):**`sch titleblock --data`
   会损坏 sheet 引用,而 strict gate 同时会报 `missing-titleblock` 并建议该危险命令。
   只有 CLI 与 connector 都是 1.1.1、失败 stage 仅为 `check`、finding 仅含
   `missing-titleblock`、其余 stage 全 pass 时,才可给 acceptance 脚本显式传
   `--allow-titleblock-gap` 挂账。任何其他版本、stage 或 finding 仍失败。

2. **跨页网名门(逐页命令看不见的那半)** `easyeda sch nets --strict`

   逐页跑完 gate 之后**必须再跑一次这个** —— 它审的是全工程网表,而 gate/check
   都是逐页的。真机现场:电源块落地出 `+3V3`/`+5V`,MCU 块与 CH340 块要 `3V3`/`5V`,
   **主控和它的稳压器根本没连在一起**,而没有任何既有判据会报 —— 每页各自只有一个
   变体(同页看不见)、两个网各自都完全合法(有 ≥2 脚、不悬空、名字有效)、
   `bridge-check` 找的又是相反的毛病(本该分开却连上了)。它当时只能靠人眼在
   block-apply 的输出里发现。

   两条判据:**网名变体**(归一化后同名、原名不同;只做书写惯例等价,
   `AGND`≠`GND`、`VDD`≠`VCC`)、**单引脚网**(那个引脚什么也没接上,`--strict` 阻塞)。
   修法是 `sch block-apply … --bind <端口>=<统一网名>`,而**根治在 S0**:
   方案书里就该定死全工程网名表,让每个块 apply 时绑到它。

3. **设计意图门(已机械化)** `easyeda sch reconcile`
   - 判「**接对了没有**」——机械门只判「接得合不合法」。
   - 凡是 `sch block-apply` 落地的模块,块库里写着它的 `internal_nets`,虚拟组记着这一实例的
     **role→位号**,所以任何时候都能**从块库重新推导本该怎么连**并与活体网表逐条对账。
     判据是**连通性不是网名**(实例网名 `<INSTANCE>_N<i>` 与 `--bind` 重绑的边界网名都会变,
     「这几个引脚必须在同一张网上」不会变)。三类差异:`split`(本该同网的分散在多张网 = 真缺陷)、
     `missing`(没连上)、`unresolved`(块数据与实际器件对不上)。有差异非零退出,可当门禁。
   - **手工搭的组没有拓扑来源**,命令会如实列出「对不了账」而不是假装通过 —— 那部分仍需
     `easyeda sch read --doc <page>` 人工对照 spec / 修改前保存的 `DESIGNATOR.pin → net` 黄金表
     与显式 NC 集合;任何差异都先修复,不能把「布局变化」变成静默改网。
   - 可再跑 `scripts/lint.sh <project>` 做数据 lint,但它不替代上面的活体门。
- ⚠️ **判状态看数据(`sch list` / `sch gate`),不看截图**(API 改动后画布可能不重绘 → 截图 stale)。

### S6 — 调整闭环(立刻调,再验)
- **先看 gate 报告的「下一步」** —— 每个失败 stage 自带规定的修法,别自己另发明一套。
- `layout-lint` 失败 → **成片的布局问题(分区拥挤/标签互叠/partitionOverlap)先跑
  `sch zone-arrange --apply`**(分区级确定性收敛;规划框是落地框的预测,**但只在
  「同一份 pin 坐标 + 同一份桩长」的模型内是上界**,真机会偏 —— 落地后自带
  **断言③复判**把偏差如实报出来)——**不要陷入逐器件手工修补**,但**也不要靠重跑收敛**:
  断言③红说明某个区实测比规划胖,重跑只是追尾(真机 4 轮不收敛),按复判表看是哪个区、
  差多少;报「自由落点 pin」就先查那几只脚为什么没进计划;只有孤立单件冲突才用 `sch modify`(单件)/`sch align`/
  `sch distribute`(成排)/`sch autoplace-free`(自动找空位)。**几何先修**:重叠会连锁出
  一堆电气误报,先治几何再看电气,能省掉大半来回。
- `check` / `bridge-check` / `drc` 失败 → 补线、拆桥、清孤儿或补 NC → **重跑 gate 并重新 `sch read` 对账**。
- `blocked` 不是修电路的信号 → 按 S5 的三态说明先修环境(health / doc switch),再重跑。
- **💾 循环直到 `sch gate` verdict=pass 且设计意图对账无差异，再 `easyeda sch save --doc <page>` 收尾并确认 `saved:true`**。这就是“调整后立刻验证”的闭环。
- **收尾回流(块库共建)**:本板若含手工搭建且已 `sch check` + 网表核实通过的标准外围(库里没有的),按 [`standard-blocks-contributing.md`](./standard-blocks-contributing.md) 顺手回流一个块(署名 + `validated`)——验证刚过正是入库时机。

## 录制 / 演示模式(Recording / Demo Mode)

⚠️ **触发词**:当用户显式说「我要录制 / 做演示 / 做教程 / 要截图 / 要过程图 / 出分阶段图」等,进入本模式。**它不改变数据门,而是在数据门之外再加一道「可视化产物门」**——因为此时截图不再只是判据,而是**交付物**。

**双门规则**:

| 门 | 判什么 | 用什么 | 何时看 |
|----|--------|--------|--------|
| **数据门(始终生效)** | 设计对不对 | `pcb list` / `track-list` / `via-list` / `pour-list` / `drc` / `check` / `layout-lint`(原理图侧 `sch list` / `layout-lint`) | 每阶段判正确性 |
| **可视化产物门(仅录制/演示)** | 每阶段有没有**非 blank、非 stale、且是对的文档**的原生截图 | `easyeda pcb stage-snapshot --stage … --previous-sha256 <上帧sha>`(自动把关);单帧 PCB 用 `pcb snapshot`,原理图用 `sch export-image` | 每阶段留档交付 |

**核心纪律——原生截图 ≠ 数据渲染图**:

1. **优先原生 EasyEDA 截图。** 每个阶段 PCB 用 `easyeda pcb snapshot` 抓画布(带 `sha256`/`capturedAt`);原理图用 `sch export-image`(渲染文档数据,无 stale)。
2. **两种坏帧要分开:STALE(冻结)vs BLANK(空白)。**
   - **STALE** = 与上一帧字节相同(EasyEDA 不在 API 改动后自动重绘)。传 `--previous-sha256 <上帧sha>`,connector 检测到即重试一次并回报 `stale=true`。
   - **BLANK** = 画布根本没渲染出内容(窗口最小化 / 在别的 Space / 被别的窗口挡住时,`getCurrentRenderedAreaImage` 回读一张平坦帧)。`snapshot` / `stage-snapshot` 现在会**在 CLI 侧读 PNG 判空**并告警(`primitiveCount>0` 却是平坦单色图 = 窗口没渲染,不是设计错)。
   - ⚠️ **关键实测(2026-07-03)**:窗口不在前台可见渲染时,**任何 API 手段都推不动重绘**——`view fit` / `zoomToAll` / `startCalculatingRatline` / `openDocument(当前doc)` / 切到别的 tab 再切回,实测全部无效。**唯一的修法是把 EasyEDA 切到前台、让目标文档成为可见的活动 tab,再抓。** 不要指望有 reload/refresh 命令能替你重绘隐藏窗口——没有。
3. **数据渲染图只能兜底/标注,不能冒充截图。** 由 `pcb list` 等数据生成的复现图(recap image)可作 fallback 或加注解,但**绝不能当作现场原生截图交付**。
4. **诚实报告。** 交付时逐阶段说明每张图是「原生 EasyEDA 截图」还是「数据渲染复现图」;任一帧 stale 或被数据图替换,必须显式点出。

**建议的 PCB 阶段产物**(每阶段同时保存:原生截图 + 数据快照文件 + 可选兜底 recap 图):

- P2/P3 布局+板框后、
- P4/P5 禁布区+丝印后、
- P7 信号布线后、
- P8 电源布线 / +5V 路径后、
- P8 铺铜/内电层后、
- P10 DRC 通过的最终态。

> **一条命令搞定:`easyeda pcb stage-snapshot --stage "P7 routing" [--out ./rec]`**
> 它在一次调用里:①抓原生 PCB 截图 → `<out>/<stage>/snapshot.png`;②批量落盘数据包
> (`components/tracks/vias/pours/nets/drc` .json);③写 `stage.json` 清单;④对帧**把关**。
> 把关规则(录制脚本 `set -e` 就能天然 gate):
> - **前台 tab 不是 PCB** → 非零退出(截图会是错文档,比如原理图)。先 `easyeda doc switch <pcb>` 切过去再抓。
> - **BLANK 帧**(窗口没渲染)→ 非零退出,提示把 EasyEDA 切前台重跑。
> - **STALE 帧**(传了 `--previous-sha256` 且字节相同)→ 非零退出;确要接受传 `--allow-stale` 降级为告警。
> - DRC 不干净 → 只告警不拦(照常留档)。
> 逐阶段串联:把上阶段输出的 `sha256` 用 `--previous-sha256` 传给下阶段,即可链式检测「这一阶段画布有没有真的变」。

## 切到 PCB — 阶段流水线(顺序是硬约束,实测踩过才定的)

原理图过门(DRC 干净 + 已保存)后,转 [`pcb.md`](./pcb.md)。**关键:禁布区和丝印对齐要在布线之前做**——布完线再加禁布区/挪丝印会逼你返工重布(实测:天线区后置,把已布的 BLINK 逼到重绕)。

```
P0 新板/切板 → P1 导器件 → P2 摆放(留装配位) → P3 板框 → P4 禁布区(靠前!)
→ P5 丝印对齐位号(靠前!) → P6 可布性门 → P7 布线 → P8 叠层+电源+铺铜
→ P9 极性/板注丝印 → P10 DRC+check 门 → 💾 save
```

**每过一个阶段发一条通知**(用户能实时跟进):`easyeda notify --message "完成 P7 布线,下一步 铺铜" --type success`。

> 🎬 **录制/演示模式下额外一步**:发通知的同时,在下列标了 📸 的阶段跑一条
> `easyeda pcb stage-snapshot --stage "<阶段名>" --out ./rec --previous-sha256 <上帧sha>`——
> 它一次性抓原生截图 + 落盘数据包并对帧把关(BLANK/STALE/错文档都会非零退出)。
> 前提:**EasyEDA 必须在前台、目标 PCB 是可见的活动 tab**,否则抓到的是空白/错帧(没有 API 能替你重绘隐藏窗口)。

- **P0 新板**:要全新 PCB 页用 `easyeda pcb new-board`(建 Board 壳→灌 PCB 两步,单 `createPcb` 是 no-op)。⚠️ 一个原理图只能属于一个 Board:若原理图**已绑板**,`new-board` 会**拒绝**(否则会把原理图搬进新板,旧板只剩 PCB=「原理图没了」)。既有板里直接布局即可;确要搬才加 `--force`。
- **P1 导器件**:`pcb import-changes` 会**弹 UI「应用修改」**(平台限制,无 headless apply)——要全自动改用 `pcb add-component` 逐件放。导完 notify。⚠️ **落件种子坐标决定板子大小**:`auto-place` 只把卫星吸附到主芯片边缘,**主芯片锚点原地不动**——spec `board` 为 `"compact"`(客户没给板框)时,主芯片必须按**紧凑网格**播种(模块中心距 ≈ 芯片包络 + 300~400mil 布线通道,别撒到 2000mil 开外),边缘件(USB/端子)直接种在预期板边线上。
- **板框顺序两条合法路径(#97 消歧)**:`place-constrained` 的贴边启发式**需要板框**才能定边,而"无客户尺寸时先布局再成框"要求先摆件——二者不矛盾,按有无机械约束分流:
  - **有机械尺寸/外壳约束**:P2 先据 spec `board` 建**粗板框**(`outline-set`/`outline-round`)→ 再布局(`place-constrained` 贴到真实边)→ 用户确认布局+板框。
  - **无机械尺寸**:P2 先**粗布局**并生成**临时大板框**(`outline-fit --margin` 给宽余量,让 `place-constrained` 有边可贴)→ 完成布局后 `outline-fit`/`outline-round` **收紧板框** → 用户确认。
  两条路径都以"布局确认(P2 停点)+ 板框确认(P3 停点)"收尾,再进 P6 可布性门。
- **P2 摆放 — 按优先级分档,每档过确认(2026-07-09 走查#1 用户反馈定型)**:
  **摆放前先问两个决策**(见 design-decisions.md #13/#14,里程碑档必问):① **单面还是双面布局**(SD 卡槽、去耦帽这类矮件适合底面,双面省板但双面贴装贵);② **焊接工艺**(产线贴片可用 0402;手工焊接封装下限 0603/0805,直接影响选型与间距)。
  **回答后立即落盘,不能只记在对话里**:`pcb stage set-assembly --profile hand-solder --min-gap 40 --large-pad-access 60`(或 `--profile reflow`)。手焊的 40mil 是普通器件外框间距地板;USB 外壳脚、SOT-223、模组大焊盘等至少留一个 60–80mil 烙铁进入方向——**这条 gate 已机械检查**(solder-access:每器件 bbox 四侧至少一侧 ≥ `largePadAccessMil` 净通道,四面被围 = gate 失败,`confirm-layout` 拒绝;进入方向是否合理仍截图复核)。电气 DRC clearance 不能替代此门。`pcb auto-place` 的 `--assembly-gap` 默认自动取项目 profile 的 min-gap(摆放与门用同一间距)。完成 P2 全布局后运行一次 `pcb layout-lint --gate`,通过才允许 `confirm-layout`(确认摘要会打印 profile/min-gap/tight/access 数);P3 改板框会使该结果失效,P6 必须在最终板框上重跑。(issue #99)
  **S0 有 `flow` 时,动手摆件之前先看一眼骨架(#167,只读)**:跑
  `easyeda pcb floorplan --spec <s0-spec.json>` —— 它把 `flow`(如 `["POWER","MCU","RF","ANT"]`)沿流向轴切成
  **有序功能带**(带宽按各段器件面积分配),并把 spec 里写了 `ref`+`edge` 的连接器钉到目标边,
  给出一张「板子该怎么分区」的骨架 + `unzoned[]`(哪些件还没归属)+ `warnings[]`。
  **⚠️ 它不搬器件**——落笔仍走下面的分档流程(`place-constrained`)。之所以先看一眼:floorplan 决定的是
  分区本身,这件事错了后面搬多少次件都是白搬。与 `pcb zones` 并存不互斥:zones 是固定九宫格
  (表达「MCU 在中间」这种位置意图),floorplan 表达它表达不了的**顺序 / 比例 / 段数**。
  方向不强制——已有器件更接近反向时它按反向切带(输出 `reversed=true`),不会把一块本来就摆对的板翻过来重排。
  **优先级档序(每档摆完→截图/坐标表向用户确认→锁定→`pcb stage confirm-tier <n> --parts …` 落档,再摆下一档;#125 起分档是机器状态不再靠自觉)**:
  每档确认记录该档器件清单+**姿态指纹**:档间递进强制(跳档被拒)、动了某档的件只作废该档及其后(前面档存活)、`--empty` 声明空档(如无 RF 板)、档 4 缺省=其余全部;**`confirm-layout` 拒绝在四档未齐/有无主件时封章**(`--force <理由>` 审计放行)。`pcb stage status` 看梯子。
  1. **安装孔/结构孔**(M3 四角等)——最先放+**锁定**,后续所有档避开垫圈净空(M3 头 Ø6mm ≈ R118mil);孔后置必然与边缘件冲突(实测:四角 IPEX/USB 全压在垫圈区上)。
  2. **边缘接口件**(有开口方向的:端子/USB/SD 卡槽/排针/按键/IPEX)——按 spec 的出边意图放到板边,开口朝外;这一档**必须用户确认**(朝向、边序是装配体验,agent 猜不了)。
  3. **主芯片 + RF 链**(QFN/SOP 锚点 + 天线馈线簇)。
  4. **卫星件**(去耦/上拉/RC)——只有这一档交给 `pcb auto-place`/合法化器;`--assembly-gap 40`(留烙铁位)。
  **分区先落盘(#126)**:P2 起手把 S0 spec 的 `modules[].zone` 灌进 PCB 侧——`easyeda pcb zones set --spec <s0-spec.json>`;之后 `place-constrained` 会把被 claim 的主芯片/卫星件**摆进对应分区**(边缘件豁免),`pcb check` 的 **zone-violation** 持续核查「S0 拍板的分区在布局里落实了没有」。**spec 没有 zone 的模块不用硬编**;分区违规=WARN 不阻断,但交付前应清零或说明。
  **一键分档布局**:`easyeda pcb place-constrained` 自动做档1-4——按**类别启发式**(board_edge/user-facing)把边缘件贴边+锁定、把非对称连接器(USB/SD/IPEX)几何化朝外→主芯片/晶振锚定→卫星合法化,确定性根治打地鼠(边缘件不会被卫星挤走)。**现已消费块 `placement.<ref>.edge` 语义**:`edge="user-facing"` 的连接器(USB/SD/端子/排针)会被**分组到同一条共享边并沿边居中紧凑排布**(≥2 件才触发,`:grouped` 标在 diag),外部 I/O 聚到一条可达边而非各贴最近边散开(修偏散板);`edge="any"`(RF天线/模组)保持各自最近边。⚠️ **仍不解析块 `placement` 里的自由文本 `orientation`**(如"IPEX 座置板边、天线走线短直"这类 per-role reason)——所以边缘件档仍要**先 `easyeda blocks show <id>` 读 `placement`(edge/side/orientation/reason)**,连同边序摊给用户确认(P2 停点,确认后 `easyeda pcb stage confirm-layout` 落 `placement_confirmed`;**移动器件会失效该确认,需重新确认**),不能靠工具代劳;卫星件贴脚距离读块 `pcb_layout` 的 `decap-adjacency`/`xtal-adjacency`(如去耦 ≤2mm)。跑完 `outline-fit`→放 M3 孔→复核净空。**每档动手前必读真实几何**(`pcb list --include-bbox`,bbox 含 courtyard 常比封装大 40%+,L501 类功率电感可达 558mil)——猜尺寸摆位必被 lint 打脸。RF/天线件周边别塞小件。**紧凑度自检**:板框内面积 / 器件 courtyard 总面积 明显 >3 = 太空,回 P1 收拢主芯片种子再来。
- **P3 板框**:`pcb outline-round --rect … --margin 120`(**默认圆角**,贴器件包络;半径 ≤ 四角 M3 孔外缘距板边、别切孔,无孔约束取 2–3mm,见 `pcb-layout-conventions.md §2.5`);spec `board:"compact"` 时 margin 收到 **50~120mil**,天线端板边贴模块天线区顶(天线本就该在板边,keepout 条越短越省板)。**插头受体连接器**(USB-C/DC jack)在直边段**突出板框 ~0.5–1mm**(§2.2,焊盘留板内),圆角只在四角不影响。**板框定稿并经用户确认后 `easyeda pcb stage confirm-outline` 落 `outline_confirmed`(需先有 `placement_confirmed`;`outline-fit`/`outline-round` 改框会失效它,须重新确认)。**📸 录制模式:布局+板框成型后抓一张阶段截图。
- **P4 禁布区(靠前!)**:天线/挖槽用**一个多层区域**即可——`pcb region create --layer 12(多层) --rule no-pours --rule no-wires --rule no-fills`,一个区域盖全铜层,**不用逐层建 4 个**;内层用「填充区域」禁止,不需要 no-inner-electrical。**删旧区域要「删完校验再建」**——delete 紧跟 create 同批次会竞态,删没生效就累积。RF/天线器件清单与禁布层范围读 S0 方案书 spec 的 `rf.parts` / `rf.keepoutLayers`,这里不重新判断该不该禁、禁哪些层。**RF 块的 `pcb_layout` `rf-keepout`/`balun-mirror`(severity=must)与 spec.rf 一并 `blocks show` 读。**
- **P5 丝印对齐(靠前!)**:`pcb silk-align`(位号摆正+位置感知+`--spacing` 装配间距)。导入的位号常 180° 倒置,这里一并摆正。放布线前,让布线避开丝印占位。📸 录制模式:禁布区+丝印就位后抓一张阶段截图。
- **P6 装配+可布性门(强制,#97/#99)**:P3 最终板框确认后重跑 `pcb layout-lint --gate`(P2 的初检会因改框失效)。门读取项目 assembly profile;同时要求 0 overlap、0 off-board、**0 tight spacing**、≥ `--min-score`、ratsnest 交叉 ≤ `--max-crossings`。手焊 profile 未设置或任何器件低于40mil时必须失败,不得进入布线。**通过才重新落 `pre_route_passed`**;P7 布线命令默认要求 `outline_confirmed` + `pre_route_passed`,否则拒绝;确需推进用 `--force <理由>` 显式授权并记入审计(**仅本次执行有效**——不落任何确认,下次无 `--force` 照样被拦)。用 `easyeda pcb stage status` 查装配档案与阶段。
  **诊断视角:`pcb layout-score`(#167)——只有一个门,别跑成两个门。** 两者分工是硬的:

  | | `layout-lint --gate` | `pcb layout-score` |
  |---|---|---|
  | 回答 | **能不能布线**(硬门) | **布得好不好**(质量表) |
  | 输出 | 单标量分 + pass/fail | 九维各自 0-100 + 加权综合 + **逐器件归因** |
  | 门禁 | **是**,落 `pre_route_passed` | **不是**,不落任何 workflow 确认 |
  | 何时跑 | P6 必跑,通过才进 P7 | 门挂了要**定位原因**、或想把板从"能布"提到"好看/好造"时跑 |

  用法:`easyeda pcb layout-score --spec <s0-spec.json>`(带 spec 才解锁 flow-order 与 internal 连接器判定;
  不带就是那两维 skipped)。**读报告先看三件事**:① `blocking` 有没有(短路/重叠/出板框 = 一票否决,
  跟 gate 同源);② 摘要里的 **`N skipped`**——skipped 是「没测」不是「满分」,别把「7 维 90 分」读成全面体检;
  ③ 最弱那几维的**归因列表**(报告里长这样:`↓ 齐整度(tidy)（80.0）拉低它的是: C2 −20.0 — anchor 离最近 5mil 格点 0.0015mil`),`penalty` 就是「先动谁」的排序。
  `--only tidy,compact --all` 只深挖两维;`--min-score 75` 才会让分数不达标非零退出(不设则只有 blocking 才非零)。
  **权重和阈值是待校准初值**,好板某维掉分优先怀疑度量而不是板子——用 `pcb dump` 把好板存成 fixture、
  `layout-score --from` 离线复现,再回改代码。
  **签字时自动留质量快照**:`pcb stage confirm-layout` 会 best-effort 拉一次 layout-score,把综合分+
  逐维分+skipped 数记进 workflow 状态(`GateSummary.Quality`)——打分失败只警告绝不阻断签字;
  `--min-score` 显式给了才当门(默认 0 只记录,理由:尺子未校准不担硬门)。
  **快照的消费侧在 `workflow status`**:普通 status 渲染上次快照(综合分+最弱三维+未测维数+记录时间,
  没记过会明说);`--reconcile` 时还会实时重打分做**逐维 diff**——掉 ≥5 分的维标 ⚠ 提示
  (「上次 confirm-layout 后 tidy 从 90 掉到 60——布局被谁动过?」),上次 scored 这次 skipped 报
  「该维失去可测性」而不是当掉到 0 分;实时打分不可得时明说没做对比(没测≠没变)。
  全部只提示不拦截——status 不是新的门。
  **精修**:tidy 类低分交 `pcb refine`(打分驱动、默认 dry-run、逐步回滚;唯一变换器 grid-snap,
  其余维和全部 blocking 报告会明确指回 place-constrained/手工)。
  **门禁的机械强制面(#97 后续,2026-07-12)**:① 状态**全局持久化**在 `~/.easyeda-agent/workflow/<project>.json`(换 cwd 跑 CLI 骗不过门;`EASYEDA_WORKFLOW_DIR` 可覆写);② **daemon 在 /action 派发层同样拦截** `pcb.line.create`/`pcb.via.create`/`pcb.import_autoroute`(raw HTTP 调用也绕不过),且任何摆放/板框类 action(component.modify/move/arrange/align/add/delete/import_changes/outline.set/clear)成功后**自动失效下游确认**并在响应 warning 里报 `workflow stage invalidated`;③ `confirm-layout`/`confirm-outline` 会把签核**指纹绑定**到当时的器件坐标/旋转/层与板框几何——GUI 拖动、`debug.exec_js`、其它 agent 的门外改动,会在下一次 gate 时指纹失配 → 自动失效并指回该重确认的阶段。

  **④ force 是分级的(#132)——别把 `--force` 当万能钥匙**:
  - `--force <理由>` 只放行**软缺口**(典型:布局已签、板框已签,只是 `pre_route_passed` 没重跑)。
  - **机械骨架全未确认**时(`placement_confirmed` 与 `outline_confirmed` **双缺**,或 state 读不出来)`--force` 会被**拒绝**——#116 实测:零确认板强行布线产出 257 条 track + 92 个 via,全部返工。真要在这种板上布线,用更高摩擦的 `--force-unsafe <理由>` 显式升级。
  - 两者都**只对本次调用有效**(不落任何确认,下一条无 force 的命令照样被拦),且**全部入审计**——连被拒的 `--force` 尝试也记一条 `force-refused`。

  **⑤ 被拦时不用猜下一步**:daemon 的 `STAGE_BLOCKED` 拒绝消息里直接带**该跑的那条命令**(最早未满足的那道门:tier 梯子 → `confirm-layout` → `confirm-outline` → `layout-lint --gate`),照抄执行即可;要看全局状态才用 `easyeda workflow status`。

  **⑥ 布完还有一道门**:布线不是终点,`post_route_checked`(布完必查)见 **P10** —— `workflow advance` 自动跑 `pcb check`,ERROR / power-not-poured / width-under-spec 必须清零才放行 P9 丝印与交付;任何布线类 mutation(track/via/pour/fill/beautify/import_autoroute)会自动把这道门重新关上。
- **任意阶段切入 / 会话恢复(workflow 命令族)**:换了模型、丢了上下文、或用户手改了板子,都不需要重走流程——
  1. `easyeda workflow status --reconcile`:拉实况(器件数/板框/已布线数)+ 校验指纹,自动失效漂移的确认,报告不一致(如「有走线但从未过门」);
  2. `easyeda workflow advance`:幂等推进——机械验收(layout-lint gate)直接代跑,人工签核点(confirm-layout/outline)停下并打印**下一条该执行的命令**(非零退出,脚本循环天然停在签核点);
  3. `easyeda workflow confirm layout|outline` = `pcb stage confirm-*` 同实现;`workflow init` 新板起手建 marker。
  每一步的 `next:` 输出就是「默认继续按 workflow 走」的入口,不依赖任何 agent 记忆。
- **P7 布线 — 三档阶梯(2026-07-09 定型)**:按密度选档,密度预算=layout-lint 的 ratsnest 长度/交叉数。
  > **档位铁律(= 顶层「档位默认」表的展开)**:稀疏板 → ① route-short;**稠密板默认 = ② 人机协作档(停下请用户点原生自动布线),不是 Freerouting**。③ Freerouting 只在**全 headless(无用户可点)**时兜底,**绝不拿它顶替 ② 去图 autonomous**——用户选了 ② 就按 ② 停手交回。(2026-07-09 实测踩过:图省事直接上 Freerouting = 违反本档。)
  **P7.0 关键网络先行(2026-07-10 定,先于把剩余交人工)** —— 自动布线器最不擅长的两类不丢给它、自己确定性布好并**锁定**,只把剩余普通信号交人工档 ②(是对 ② 的**增强**,不是替代)。
  **一条命令承载(#127)**:`easyeda pcb route-critical`(--dry-run 先看识别与计划)= 下面 1-4 步的机械化——电源按层数选 planes/pour → 差分对(块 signals+名字模式双源识别)45° 成对布线并**逐对实测 skew 报预算** → `pcb.track.lock` 锁定;之后只做第 5 步交人工。单步拆做仍可(--skip-power/--skip-diff/--no-lock 或下述手工命令):
  1. **识别关键网**:读块 `signals` map(`easyeda blocks show <id>` 的 `type:diff_pair` / `length_match_mm` / `impedance`,如 USB_D 90Ω、RS485_AB 120Ω)+ 电源网(5V/3V3/12V/VBUS)。
  2. **电源先铜(稳供、低阻)**:**2 层一键 `pcb power-pour`**(GND 双层 pour + 每条电源轨在自身 pad 区局部 pour,全动态铺铜不短路);4 层用 `power-planes` 内电层;主干/大电流也可手工用**大面积填充块** `pcb fill create --net 5V --layer 1`(net-bound,solid/mesh);GND 用 `pour`。**别拿细线穿焊盘阵布电源**(route-short 默认就跳过电源=对的;真要走线时 `route-short` 会**按 net-class 角色给规范宽**——支线 3V3≈10 / 主干 +5V≈15 / 大电流 VBUS≈20mil,见 `pcb net-classes`;`--width-power` 强制全电源同宽)。fill(静态硬块、实、后续信号要留 clearance、**异网相叠即短**)vs pour(会退让障碍、连通性靠 rebuild、异网不短)——大电流/参考平面用 fill/plane,一般电源/多轨并存用 pour。**兜底检查**:`pcb check` 的 **power-not-poured**(裸电源网没铺铜)+ **width-under-spec**(电源线偏细)会逮住漏网的。
  3. **差分/等长先布**:USB D±、RS485 A/B 成对布——本板这类很短(连接器→芯片),**成对并行、尽量短、≤5mil skew 即可,不用蛇形调谐**;`route-short` 或手工 `pcb track`。
  4. **锁定关键铜**:布完 `pcb track-lock --net 5V --net USB_DP --net USB_DM`(按网锁 track/**arc**/via/fill;或 `--all` 锁全部已布铜,`--ids` 锁指定,`--unlock` 解锁)把它们锁死,**否则人工自动布线器 / `pour-rebuild` 会把手布的关键线冲掉**——锁是本流程的地基。0.15.2 起走 typed action `pcb.track.lock`(从 debug-exec 版毕业,连 beautify 弧也锁得到);pour 永不锁(要 reflow)。
  5. **交人工档 ②** 布剩余普通信号(避开锁定铜)→ 最后 `pour-rebuild` 让 GND 退让全部已布铜。
  ① **启发式档** `pcb route-short`:稀疏板(esp32-mini 级,交叉 <100)一次布通;
  ② **原生 UI 自动布线(人机协作档,稠密板推荐默认)**:官方 autoRouting API 未放出前(pro-api-sdk #28 卡 web 版本),agent 备好布局/叠层/禁布/规则后**停下来,请用户在 EasyEDA 顶部菜单点「布线 → 自动布线」**,跑完 agent 接手验证(DRC/check)+铺铜+丝印——一次点击换全套官方路由器(推挤/撕绕/规则原生一致),省掉外部 DSN/SES 往返的全部坑;API 放出后此档自动升级为无人值守;
    > **交用户前必念的「自动布线对话框」提醒清单(做过 P7.0 关键网络先行/有锁定铜时尤其重要,漏一条会毁掉 P7.0)**:
    > 1. **「已有导线/过孔」必须选「保留」,绝不选「移除」** —— 移除会删掉已布好并锁定的电源内电层 / 缝合过孔 / 关键网,P7.0 全白做。
    > 2. **「布线图层」只勾「顶层+底层」,取消「内层1/内层2」** —— 4 层板内层是 GND 内电层 / VDD 电源层,信号布进去会和平面打架(单/双层板此条不适用)。
    > 3. **「忽略网络」加已在平面的电源网(GND、3V3/VDD 等 P7.0 已用内电层的网)** —— 它们已靠过孔连好,别让路由器再布成冗余线。
    > 4. 其余默认即可:所有网络 / 45°(或 90°)/ 完成度优先。做过 P7.0 差分先布时,把已布的差分网也加进「忽略网络」保护。
  ③ **外部迷宫档** `pcb autoroute`(Freerouting,需 JDK21):全 headless 场景的兜底。**教训**:rip-up 后必须 save→reload→验证 0 轨再导出 DSN(残留叠布=上一代轨与新轨 0mil 重叠,499 条 ClearanceError 实测);电源网别抢在迷宫档前用 power-planes 缝合(缝合孔会和密轨打架,161 条实测)——顺序=先全网迷宫布通,后 pour-rebuild 让面通过路由过孔接通。
  原 route-short 细则:**现在自带多层布线**(默认开):同层太长或跨层的 hop 不再推迷宫档,自动用 via 换到空闲对层走 trunk 再 via 回来(dogbone,via 偏离焊盘),铺得开的板也能一次布通信号(实测 esp32-mini 15 段+2 过孔,长 USB hop 走 L2)。`--no-multilayer` 退回旧的只布短同层线。手工换层仍可用 `pcb via-hop`;个别擦焊盘绕行用手工 `pcb track`;单颗错 via/track 用 `pcb via-delete/track-delete --ids` 精准删,别整网 `rip-up`。**手工修线三律**:① 优先多层——长网/交织网用 via 对借 L2 直跑,别死磕单层平面性(交织对在单层是拓扑无解,推演再久也无解);② 动笔前先 `via-list`/`track-list` 拉全量已有铜形(power-planes 缝合 via 的环晕 r≈12 在 L2 是硬障碍),按坐标排车道;③ 板边走廊记住铜-板框规则 0.3mm(11.8mil)。**注意 `rip-up` 会连电源缝合过孔一起删**——之后重跑 `power-planes` 补回。**⚠️ mutation(rip-up/route/delete)后先 `doc reload` 再读/判/DRC**——否则 line.list/DRC 读 stale(见 [[pcb-stale-reads-need-doc-reload]]);确定性复位=rip-up→save→reload。📸 录制模式:信号布线完成后抓一张阶段截图。
- **P7.9 走线美化(可选,布线定稿后)**:信号全布通并锁好关键网后,`pcb beautify` 把直角/锐角走线圆滑成圆弧(改善美观+可制造性,减少尖角蚀刻风险)。**先 `pcb beautify --dry-run` 预览**(只报 paths/arcs、不动板),满意再实跑。它自带安全网:删+重建轨迹→DRC 二分修复违规拐角(缩半径或退回直角)→自动 `pour-rebuild`(同网 GND 键合会 stale);差分/等长网走同心圆弧保护(该 build 暴露 `getAllDifferentialPairs` 时,否则保直角)。只动铜层、绝不碰丝印/板框。默认 `--radius-ratio 3`(半径=最大线宽×3),稠密板可调小(`--radius-ratio 2`)。**顺序**:放在 P7 布线之后、P8/出 Gerber 之前;跑完 `pcb save`。**上游告警照搬**:焊盘-走线连接偶需人工复核、RF/高速网建议排除全局美化(用 `--net` 单网做)、出 Gerber 前预览确认。能力吸收自开源扩展 Easy_EDA_PCB_Beautify(m-RNA,Apache-2.0)。
- **P8 叠层+电源+铺铜**:层数与地策略(单 GND PLANE 还是分区 pour + 桥地)读 S0 方案书 spec 的 `stackup` 字段,这里不重新选。**热焊盘处理**:读本板各块 `pcb_layout` 的 `ep-thermal-vias`/`ep-ground-stitch`,在 IC EP 焊盘下打散热 + 接地过孔阵列(≥4 vias)。**4 层层分配默认(客户常规叠层)**:Top(1)信号 / **Inner1(15,物理第2层)= GND 内电层(PLANE)自动铺铜** / **Inner2(16,物理第3层)= VDD/电源层** / Bottom(2)信号——`power-planes` 默认就这么分(`--gnd-layer 15 --power-layer 16 --gnd-plane`,自动灌铜)。多电源轨时**主轨(引脚最多,如 3V3)上电源内层**,零散轨(+5V/+12V/VBUS)走线或小 pour(不与主轨挤同一内层,否则冲突)。`pcb stackup set --layers 4` → `pcb power-planes`(GND内电层+VDD内层+缝合过孔)→ 顶/底 `pcb pour-fit --net GND --replace=false`(`--replace` 默认 true 会清跨层同网铺铜)→ `pour-rebuild`(退让禁布区)。**GND 内层的正确终态是 内电层/PLANE**——`power-planes` 默认(`--gnd-plane`)按已验证配方自动完成:先在 SIGNAL 态铺网络铜 → `stackup set --plane` 翻 PLANE → `pour-rebuild`,填充存活、DRC 干净(与 `pcb-layout-conventions.md` 口径一致)。**顺序不能反**:在已是 PLANE 的层上直接新灌铺铜会掉到 L1 且 netless(坏路径);翻回 SIGNAL 只是诊断手段,不是终态。⚠️ **PLANE 生成后别再打异网 via**——官方缺陷(easyeda/pro-api-sdk#32)新 via 不挖 anti-pad,DRC 报 Plane Zone to Via / Hole to Plane Zone 且 `pour-rebuild` 不补救;`pcb check` 的 **via-crosses-plane** 规则会标出,修法:优先删 via 改外层走线,或 `easyeda doc reload` 后 `pcb pour-rebuild` 再跑 DRC 确认。📸 录制模式:电源布线(+5V 路径)后、以及铺铜/内电层完成后,各抓一张阶段截图。
- **P9 引脚级丝印/极性/板注**:**先读块 `silk` map**(`easyeda blocks show <id>` 的 `pins`/`label`/`note`)——**逐脚标注**每个对外引脚:电源端子 **+/−**、总线 **A/B/G**、UART **RX/TX/GND**、极性件阴极 **K**,加功能名 + A/B 反向警示(如 SP3485 `A/B=IC`)。**硬规则——装配后不被遮住(= 顶层铁律 11)**:每个标记落在**器件本体/courtyard 之外、对齐各自焊盘**(端子塑料罩/卡座壳/按键帽会盖住其 footprint 内的丝印=等于没标);**per-pin 标记优先占位,功能标签再绕开**(先放功能标签会把 per-pin 挤脱位);边缘 header 头顶被占时脚名放本体的**板边侧**成一行。详见 `pcb-layout-conventions.md §9.4.1`。`pcb silk-add`(锚点=**左下角**;特殊字符如 `−`/`3V3` 渲染比 len 估宽宽,靠 `getPrimitivesBBox` 实测校正别信估算)+ 板名版本 + credit;`pcb silk-set --ref board --align centerx` 居中。⚠️ silk-add 在 **PCB 非前台**时报误导性「参数不正确」——写前先 `doc switch <PCB>`。
- **P10 门**:`pcb drc`(passed)+ `pcb check`(0 issue)双清零,再 `pcb save`。**其中 pcb check 侧已机械化为 `post_route_checked` 工作流阶段**——布线后 `easyeda workflow advance` 自动跑 check 门(ERROR / power-not-poured / width-under-spec 三项清零才确认;其余 WARN 报告不拦),任何布线类 mutation(track/via/pour/fill/beautify/import_autoroute)会自动失效此门,改完线必须重新过。next 指引会在此门未过时拒绝进入 P9/交付。**线宽/过孔/间距下限**别低于 `references/fab-rules-jlcpcb.json` 的 clamp floors(live `pcb.drc.rules` 优先)。**电源规范线宽 + 电源铺铜**由 `pcb check` 的 **width-under-spec**(电源线 < 其 net-class 规范宽)+ **power-not-poured**(电源网未铺铜)两条把关(WARN,`--strict` 计入门禁);电源该铺的用 `power-pour`(2层)/`power-planes`(4层)铺掉,该走线的让 `route-short` 按 `pcb net-classes` 角色宽走。DRC=0 且本板含手工搭建、验证过的标准外围时,按 `standard-blocks-contributing.md` 顺手回流入库。**两条硬注意**:① **手术后 GND 断连=铺铜 stale,不是真断**——删/改 via/track 后 DRC 冒一堆同网(多为 GND)Connection Error,是 pour 连通性 stale,跑 `pcb pour-rebuild` 让飞线重算即恢复(track↔via 本身导通,pro-api-sdk#31 误诊已订正);**别再无脑配键合 fill**。② **DRC 需前台**——后台/被遮挡窗口 DRC 重画布计算永不完成;超时就把 EasyEDA 切前台**单发一次**,绝不循环重试(daemon 已防重入,重复下发直接拒 `ACTION_BUSY`)。逐条修错用 `pcb drc --json` 的 `{rule,net,x,y,objs}` 定位,`objs` 直接喂 `via-delete`/`track-delete`。📸 录制模式:DRC 通过的最终态抓一张阶段截图,作为交付收尾图。

## 反模式(实测踩过的坑)
- ❌ 全堆一页、不分页 → S1 强制分页。
- ❌ 无图纸/无 A4 sheet 就开始摆 → S1 图纸门禁拦住。
- ❌ 用坐标外扩代替分页 → 按 A4 可用区拆模块分页。
- ❌ 一件一件随手摆、芯片和外围分家 → S2/S3 按组摆。
- ❌ 摆完不验就布线 → 元件覆盖、线压外围。S5 layout-lint 门拦住。
- ❌ 靠截图判断有没有覆盖 → 截图 stale。看 `layout-lint` 数据。
- ❌ **录制/演示时拿数据渲染复现图冒充原生截图** → 二者必须区分交付;原生截图 stale 时先重绘重抓,兜底用数据图必须明确标注(见「录制/演示模式」)。
- ❌ DRC 报错放着不管 → S6 闭环必须清零再 save。
- ❌ **放/连一大堆都不 save** → 窗口重载或 daemon 重启全丢(实测踩过)。每阶段门后存盘,整板放置每 ~10 件存一次。
