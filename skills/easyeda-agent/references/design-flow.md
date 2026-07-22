
# EasyEDA Design Flow — 首席EDA工程师流程脊柱

你是**首席EDA工程师**。整板/非平凡设计**不允许**「边想边随手摆」——那正是覆盖、外围乱飞、线压元件的根因。
按下面的**分阶段 + 硬门禁**走:每个阶段有明确产出和**过门条件**,**门不过不进下一阶段**。

> 这是**编排层**,不重复规则或动作细节:
> - 具体动作(place/wire/modify/move/align/…) → [schematic.md](./schematic.md) / [pcb.md](./pcb.md)
> - 设计规则(分区/间距/朝向/选型) → `easyeda-agent` references
> - 本 skill 只负责**顺序、分组、门禁、自调闭环**。

## 阶段目录(TOC)

深在某阶段想回看别阶段的约束/顺序,先扫这张目录定位(💾 = 该阶段过门后 save 检查点):

- **原理图 S0–S6**:S0 设计方案书 · S1 图纸/分页 💾 · S2 模块编组 · S3 按组摆放 💾 · S4 通道布线 💾 · S5 校验门 · S6 调整闭环 💾 · 录制/演示模式
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

### S0 — 设计方案书(Design Proposal)
- **做什么**:读懂设计——器件清单、电源树、功能模块划分、目标幅面;**并在放置第一个元件之前**,把覆盖原理图 + PCB 全程的架构决策一次性定下来,而不是让它们在 S2/P4/P8 执行到才被想起(实测最贵的返工正是决策后置:天线 keepout 后置逼已布线的模块重绕,地策略选错要重铺)。
- **怎么做(轻量摸底)**:见 conventions 的 `design-pre-analysis.md`(器件清单、电源树、功能分组、幅面估算)。`easyeda health` 确认已连接。
- **怎么做(架构决策)**:查 [`design-decisions.md`](./design-decisions.md) 里的决策点清单——叠层与层数、地策略(单 GND PLANE vs 分区 pour + 桥地)、接口取向(如 USB 单/双取向)、器件选型成本档位等。**每一条都要把该文件里的选项、已知坑、推荐方案摊开给用户看,由用户拍板,不能替用户默认选**——这是设计方案书阶段的核心产出,不是可选步骤。RF/天线 keepout 范围**不在**这份决策清单里——它是唯一正确答案的 guardrail(必须覆盖全层),不摊开选项;S0 只需把 RF 器件位号和 `"all"` 层范围写进 spec 的 `rf` 字段供 P4 直接读取执行。
- **产出**:模块清单(如 MCU、电源、USB、传感器、调试口…)+ 每个模块的器件归属 + 一份**机器可消费的设计方案书 spec(JSON)**。后续阶段(S2 模块分区、P4 禁布区、P8 叠层/电源/铺铜)从这份 spec **读取执行,不重新决策**——方案书写下的决策与执行阶段的做法不一致视为 bug。

  **方案书 spec 形状**(与 `sch autolayout --spec` 同一路数——一份可回读、可复用的 JSON 文件,不是写完即弃的长文;字段可按项目取舍,关键是稳定、后续阶段能直接引用):

    {
      "modules": [
        {"name": "MCU", "parts": ["U1","C18","C19","R6"], "page": "P1_MCU_USB", "zone": "center"},
        {"name": "USB_HUB", "parts": ["J2","U10","X1","C30","R15"], "page": "P1_MCU_USB", "zone": "left-top"}
      ],
      "pages": [
        {"name": "P1_MCU_USB", "sheet": "A4", "modules": ["MCU","USB_HUB"]}
      ],
      "stackup": {
        "layers": 4,
        "groundStrategy": "plane",
        "innerLayers": ["GND", "VCC_3V3"]
      },
      "rf": {
        "parts": ["U_WROOM1"],
        "keepoutLayers": "all"
      },
      "board": {
        "outline": "compact"
      },
      "interfaces": [
        {"name": "USB_C", "orientation": "dual"}
      ],
      "costTier": "standard"
    }

  逐字段说明:
  - `modules[]` — `name`/`parts`/`page`/`zone`;S2 模块编组直接读 `page` + `zone`,不重新分区。**标准外设模块可直接引用电路块**:给该 module 记 `block`(如 `"block.ch340c_usb_serial"`),S3 摆放时照抄该块拓扑、只重绑 ports——先 `easyeda blocks ls` 看有没有现成块,能少写一整个模块的选型+接线。
  - `pages[]` — `name`/`sheet`(幅面,默认 `"A4"`)/`modules`;S1 分页直接读,不重新估算页数。
  - `stackup` — `layers`(层数)/`groundStrategy`(`"plane"` = 单 GND 内电层,或 `"signal-zones-with-pour"` = 分区 pour + 桥地)/`innerLayers`;P8 叠层+电源+铺铜直接读,不重新选地策略。
  - `rf` — `parts`(RF/天线器件位号列表)/`keepoutLayers`(如 `"all"` 或具体层号数组);P4 禁布区直接读作用范围,不重新判断该不该禁、禁哪些层。
  - `board` — 板框意图:客户给了尺寸/外形就原样记(如 `{"outline": "50x40mm"}`),**没给就写 `"compact"`(默认)**——紧凑是无信息时的正确目标(省板费+小体积),不要摊大饼;P1 落件与 P3 板框据此执行。
  - `interfaces[]` — `name`/`orientation`(如 USB 的 `"single"`/`"dual"` 取向);布线/丝印阶段直接读。
  - `costTier` — 选型成本档位(如 `"standard"`/`"premium"`),`parts-select.py` 选型时参考。
- **过门条件**:上面这份 spec 已经**过用户确认,并且落成了文件**(与 `autolayout --spec` 文件同样对待——写到磁盘,可在后续阶段被引用,不是只停留在对话记录里);不再是「每个器件归到了某个模块」这么单薄。这正是「里程碑确认」模式的第一个确认点;若当前是「逐步确认」模式,同样在这里停住等确认(见「交互模式」一节);「全自动」模式下按已有 spec 或默认推荐值直接产出文件,不阻塞。

### S1 — 图纸 / 分页(先图纸,再分页!)
- **做什么**:确认当前页有图纸,默认 A4;再按模块/功能把设计**先分到几页**(电源一页、主控一页、接口一页…),别全堆一页。
- **怎么做**:`easyeda doc ls` 读页结构 → `easyeda doc switch` 切目标页 → `easyeda sch sheet-geometry --json` 读 sheet/title-block。无 sheet 或 provenance 为 none 时停止,不要开始 place。需要多页时用 `easyeda sch page-new` / `page-rename`;复杂模块独立成页。
- **💾 过门条件**:每个目标页都有可读图纸(A4 默认)和明确职责;每页模块预计能落在可用区内,标题栏 keep-out 明确 → `easyeda sch save`。若用户要求逐步确认,保存/继续前停住。

### S2 — 模块编组
- **做什么**:在每页内,把「芯片 + 其外围电路」定义为一个**组**,并规划各组在页面上的**分区位置**(谁在左、谁在右、信号流向)。
- **怎么做**:分区/信号流向规则查 conventions 的 `schematic-layout-conventions.md`。此阶段只规划坐标分区,先不落子。布局 spec 里的 `sheet` 默认写 `"A4"`;zone 必须落在 S1 读到的 sheet 可用区内。**组的 page/zone 归属读 S0 方案书 spec 的 `modules[].page` / `modules[].zone`——这里只是把已定的分区落成具体矩形,不重新分配**。
- **分区落成可校验的认领(新)**:`easyeda sch zones set --spec <s0-spec.json>`(或 `--module NAME=ZONE:D1,D2`)把 modules[].zone 持久化进项目 workflow 状态——此后 **`sch layout-lint` 自动多一条 zone-violation WARN**(被认领的件 bbox 中心落在其分区矩形之外 = S0 拍板的分区没落实),`sch zones status` 随时看认领+活体违规。词汇同 autolayout/pcb zones(left/center/right × top/bottom;画布 y-UP,top=大 y=视觉上半区)。原理图和 PCB 的认领各自独立(同一模块在图纸和板上的分区可以不同)。
- **分区框可视化(新)**:`easyeda sch zone-draw` 把认领画成**虚线框+区名标签**(行业规范「先看区、再看线」;注释图元,非电气对象;真机验证过 `eda.sch_PrimitiveRectangle/Text` 全 CRUD)。框的几何与 zone-violation 用同一 `zoneRect`——**所见即所校验**。重跑自动先清旧框;`--clear` 移除;图元 id 记在 workflow 状态,绝不误删用户图形。
- **过门条件**:每个组有明确的目标矩形区域(已 `sch zones set` 认领),组间预留了通道(不重叠的分区);若模块太多,已经拆到下一页而不是挤压本页。

### S3 — 按组摆放(芯片 + 外围一起)
- **做什么**:**逐组**放置——先放该组核心芯片,再把它的外围**就近**放在芯片周围(去耦贴电源脚、晶振贴时钟脚…),放完一组再下一组。
- **块优先(电路块库)**:摆放/接线任何**标准外设模块**前,**先查块库 `easyeda blocks ls`**(离线,无需 daemon/窗口,块库编在二进制里)——现有 **20 块 / 11 类目**(power/rf/usb/usb-serial/comms/storage/sensing/mcu/mcu-support/indicator/button),如 CH340 串口、ESP32 自动下载、buck/LDO/充电、RS-485、GNSS/433M、USB-hub、microSD…;`easyeda blocks search <关键词>`(按**功能**/**芯片**/**端口网**三维轮换搜,一词没中换维度别急着手接)、`blocks ls --category <cat>` 浏览整类、`blocks show <id>` 看完整拓扑。命中就照抄内部网表、只重绑 ports 到主控网络 + 重排位号(引脚用功能名,零改号),按 `schematic_notes` 落线。块的 `parts` 直接给出 `standard-parts.json` 的 role,选型这步都省了。**摆放几何也机械化了**:带 `schematic_layout` 模板的块,`sch block-apply` 按模板的相对偏移+朝向落件(信号流左入右出、去耦贴芯片的几何人审一次终身复用);无模板的块退回网格铺列。两条路**原点都会避开已有器件和 A4 标题栏图签**(不显式传 `--at` 时,按真实 bbox + 标题栏 keep-out 螺旋找最近空位,块落纸面右下也不会压到图签/明细表;显式 `--at` 尊重你的坐标但碰撞会警告),落完还会**回读真实 bbox 把涉及本实例的 overlap 写进 manifest(`layoutOverlaps`)**——脏落地不会静默。**块还带 PCB 多维约束 map**,各 P 阶段按 `target` 匹配读:`pcb_layout`(`*-adjacency`→P2 贴脚距离、`ep-*`→P8 EP 热过孔/接地缝合、`rf-*`/`balun-mirror`→P4 keepout+P7 RF 布线)、`placement`→P2 板边/朝向、`signals`→P7.0 差分/阻抗、`silk`→P9 逐脚标注。无命中才手接;手接并端到端验证过的新外设,按 [`standard-blocks-contributing.md`](./standard-blocks-contributing.md) 回馈入库(署名 + `validated` 门禁)。**块没带 `schematic_layout` 模板时,别再肉眼盯板手写 dx/dy**——在真板上把该外设摆漂亮一次,`easyeda sch extract-layout <block-id> --from <该实例的位号>`(或 `--role ROLE=DESIG` 精确指定)反向导出模板 JSON(role→{dx,dy,rotation},锚点=芯片/首个 role,自动吸 5 格),复审后粘进 `internal/blocks/data/<id>.json` 的 `schematic_layout` 段、跑 `go test ./internal/blocks/...` 过校验(#140)。这样「摆好一次→固化模板」是数据管线而非手工反推。
- **怎么做**:`easyeda sch place` + `sch modify`(设位号);坐标按 S2 的分区。库优先、选型规则见 schematic.md / references。
- **整组分区摆放优先用 `easyeda sch autolayout`**(模块级放置规划器,`--engine template` 默认):把 S2 的分区写成 `--spec`(每个 module 给 `zone`/`core`/`parts` 与规则),它按真实 bbox 把核心芯片放到分区中心、外围环绕核心、碰撞自动重试,并保留引脚 fanout 通道 + A4 标题栏 keep-out,**确定性产出可过 layout-lint 的坐标**。先 `--dry-run` 看方案,确认后再 `--apply`(经 `component.modify` 落子并自检 overlap)。`--apply` 前必须有真实 sheet bbox;无 sheet 只能停在 dry-run/修图纸。**`--apply` 成功后自动画功能分区框(#142)**:按 `modules[].zone` 用 `zone-draw` 同几何画出虚线区域框+区名(电源区/MCU区/USB区…),「先看区、再看线」直接内建进放置流程,不用再手动 `sch zones set`+`zone-draw`;`--zone-draw=false` 关闭,`sch zone-draw --clear` 移除。**v1 只移动「已放置」的器件**,不创建缺件——所以先 `sch place` 把器件放上页,再用 autolayout 排布。手动 `sch place`/`modify` 仍是逐件微调的兜底。
- **兜底引擎 `--engine official`**:块/spec 都没覆盖的散乱页,可让**官方 `eda.sch_Document.autoLayout()`**(@beta,3.2.148 起可用)对整页粗排。⚠**它是破坏性的、只该布线前用**:实测三坑——① **只挪器件不挪导线** → 已连线页跑完连线全断(16件→59悬空);② **落点 off-grid** → 下游布线短桩落不到脚;③ **散布让短桩相撞短路**(--replace 也拆不开)。封装已加安全管线:**已连线页默认拒绝**(报 N 条线),`--rewire` 才允许(跑后自动:吸附5格→删断线→按跑前网表重连);**自检用 `sch check`(查断线/悬空)不止 layout-lint**;长操作 ~2min(300s 超时)、需目标页**前台**。**#143 兜底增强**:① `connect_pin` 网格判定容忍浮点残差(吸附后引脚坐标=anchor+旋转偏移会带入 649.9999999 之类 FP 噪声,旧严格判定误拒→现 round 到最近格再判并把桩起点吸到整格,官方 `--rewire` 可连干净);② 官方 `autoLayout` 喂 `designatorDeviceTypeMap`(按位号前缀分 resistor/capacitor/chip… 角色),布局更聚拢。即便如此,散布布局仍可能残留极少数短桩相撞的失败连接(命令会诚实报 floating/失败数,让你 `sch bridge-check` 核)。**优先级铁律:命中块→`block-apply` 模板;有 spec→`--engine template`;都没有才 `--engine official` 兜底,且成品/已连线页一律别用它。**
- **💾 过门条件**:进入 S4 前**必须先过 S5 的 layout-lint**——本组无覆盖、组内外围紧凑、组间不挤。**有 ERROR 先回 S3 调整**(单件 `sch modify`;成排对齐 `sch align --mode centerx/top…`;等距摊开 `sch distribute --axis x/y`,均默认 dry-run、`--apply` 落地并自检 overlap;整簇平移 `sch group-move`;实在挤不下 `sch autoplace-free`)。过了就 `easyeda sch save`(整板放置每 ~10 件存一次,别等全放完)。

### S4 — 通道布线(留距离,别压元件)
- **做什么**:在组**摆放并通过 layout-lint 之后**再布线——信号走元件间的**空通道**,不要让导线压在元件或外围上。
- **怎么做**:布线/flag/去耦规则见 conventions 的 `auto-layout-sop.md`(信号=本地正交线、flag 仅电源地、绝不穿引脚)。
- **电源/地/netport stub 用 `easyeda sch autoconnect`**(别再手猜 `connect --direction/--offset`):它按真实 bbox/引脚/已有 flag 几何打分,确定性选 direction+offset 再委托 `connect_pin` 落地,批量 `--spec` 还会自动错开标签。先 `--dry-run` 看计划,满意再落地。
- **💾 过门后**:`easyeda sch save` 存盘,再进入 S5。

### S5 — 校验门(机械真值,不是肉眼)
**两个门必须都过**,否则回 S3/S4:
1. **布局门** `easyeda sch layout-lint`(可加 `--min-gap`、`--all-pages`)
   - **任何 `overlap` ERROR = 必须修**(命令非零退出,可直接当 gate)。
   - `spacing` WARN = 评估是否太挤,外围贴芯片可接受、模块间过近要拉开。
   - `zone-violation` WARN = 被 `sch zones set` 认领的件跑出了它的功能分区(需先在 S2 落认领 + 页面有 sheet bbox;这是"分区计划落实没落实"的门,不是物理缺陷,不翻 ERROR)。
   - **默认只检真实器件**:图框/标题栏(sheet)与 netflag/netport 等非器件原语已自动排除,不会再误报"器件压图框"(issue #13);要连这些一起检查才加 `--include-non-parts`。
2. **电气门** `easyeda sch drc` + `easyeda sch check`(+ `scripts/lint.sh <project>` 数据 lint)
   - `sch drc` 调 EasyEDA SDK 的 `sch_Drc.check`;当前 EasyEDA build 可能只返回聚合/布尔结果,**不等于 UI DRC 面板的全部 warning**。
   - `sch check` 是对 UI 面板缺失项的重建式补强:悬空脚、导线交叉/穿脚、网络标识与导线名不一致、同一导线多网络名等。**生产门禁必须同时跑 `sch drc` 和 `sch check`——两引擎规则集不重叠,谁也不是谁的超集**(实证:「引脚端点重叠且未连接」是 DRC 独有;孤儿旗端点压 pin 会给 check 制造"已连接"假象,check 三页全绿时 DRC 仍报 6 致命)。更险的镜像形态:重合端点**有线**相连时两网真短路,DRC 反而不报——大修后建议加跑端点重合扫描(getAllPinsByPrimitiveId 读元件+flag 全端点→坐标聚类→跨 owner 重合点按有无 wire 分级)。
   - **`sch check` 现在还含三条 Go 侧几何 marker 规则(#146/#147/#148)——电气引擎的盲区**:`duplicate-net-marker`(同类同网同锚点的重合 netflag/netport;批量 autoconnect 中断重试会叠出重复 GND/电源标识,电气全绿但页面叠着一对——finding 直接给 `suggestKeepId`/`suggestDeleteIds` 喂 `sch prim-delete`)、`titleblock-overlap`(marker/part 侵入 A4 标题栏图签)、`marker-overlap`(marker body 压住 part 或另一 marker,不可读)。**批量 autoconnect / official `--rewire` 之后必须立刻跑一次 `sch check`**:它是抓「重复标识」的唯一门(#146),`layout-lint` 只检 part 看不见 marker。marker 几何是 WARN(默认不阻断,`--strict` 才 gate);`--overlap-eps` 调重叠噪声下限。
   - fatal/error 必须修;`net-marker-mismatch` / 不同网络名同线属于必须修;悬空 IO 只有明确设计为 NC/备用并记录后才可接受;供应商编号/标准化 warning 属 BOM 门禁,交付前修。
- ⚠️ **判状态看数据(`sch list` / layout-lint / drc),不看截图**(API 改动后画布可能不重绘 → 截图 stale)。

### S6 — 调整闭环(立刻调,再验)
- layout-lint 报覆盖 → `sch modify`(单件)/`sch align`/`sch distribute`(成排)/`sch autoplace-free`(自动找空位)把冲突元件挪开 → **重跑 layout-lint**。
- DRC 报错 → 补线/补 flag → **重跑 drc**。
- **💾 循环直到两个门都干净,再 `easyeda sch save` 收尾**。这就是「DRC 后立刻调整」要的闭环。
- **收尾回流(块库共建)**:本板若含手工搭建且已 `sch check` + 网表核实通过的标准外围(库里没有的),按 [`standard-blocks-contributing.md`](./standard-blocks-contributing.md) 顺手回流一个块(署名 + `validated`)——验证刚过正是入库时机。

## 录制 / 演示模式(Recording / Demo Mode)

⚠️ **触发词**:当用户显式说「我要录制 / 做演示 / 做教程 / 要截图 / 要过程图 / 出分阶段图」等,进入本模式。**它不改变数据门,而是在数据门之外再加一道「可视化产物门」**——因为此时截图不再只是判据,而是**交付物**。

**双门规则**:

| 门 | 判什么 | 用什么 | 何时看 |
|----|--------|--------|--------|
| **数据门(始终生效)** | 设计对不对 | `pcb list` / `track-list` / `via-list` / `pour-list` / `drc` / `check` / `layout-lint`(原理图侧 `sch list` / `layout-lint`) | 每阶段判正确性 |
| **可视化产物门(仅录制/演示)** | 每阶段有没有**非 blank、非 stale、且是对的文档**的原生截图 | `easyeda pcb stage-snapshot --stage … --previous-sha256 <上帧sha>`(自动把关);单帧用 `pcb/sch snapshot` | 每阶段留档交付 |

**核心纪律——原生截图 ≠ 数据渲染图**:

1. **优先原生 EasyEDA 截图。** 每个阶段用 `easyeda pcb snapshot`(原理图侧 `sch snapshot`)抓画布。响应里带 `sha256`/`capturedAt`。
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
  **门禁的机械强制面(#97 后续,2026-07-12)**:① 状态**全局持久化**在 `~/.easyeda-agent/workflow/<project>.json`(换 cwd 跑 CLI 骗不过门;`EASYEDA_WORKFLOW_DIR` 可覆写);② **daemon 在 /action 派发层同样拦截** `pcb.line.create`/`pcb.via.create`/`pcb.import_autoroute`(raw HTTP 调用也绕不过),且任何摆放/板框类 action(component.modify/move/arrange/align/add/delete/import_changes/outline.set/clear)成功后**自动失效下游确认**并在响应 warning 里报 `workflow stage invalidated`;③ `confirm-layout`/`confirm-outline` 会把签核**指纹绑定**到当时的器件坐标/旋转/层与板框几何——GUI 拖动、`debug.exec_js`、其它 agent 的门外改动,会在下一次 gate 时指纹失配 → 自动失效并指回该重确认的阶段。
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
