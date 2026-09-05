# EasyEDA Schematic — 摆放(三层布局体系 / autolayout / 自由排布)

> 从 [`schematic.md`](schematic.md) 拆出(RFC #178):Sheet→Zone→Group 三层、模块化自动布局、
`autoplace-free`。入口与电气铁律仍在 `schematic.md` —— **先读它**,再按需读本文件。
摆放的**规范判据**(朝向/间距/分区约定)在 [`schematic-layout-conventions.md`](schematic-layout-conventions.md),本文件是**命令**。

---

## 三层布局体系 — Sheet → Zone → Group(tidy + move 各层齐备)

已连线页的布局重构走**三层刚体体系**(契约 `docs/schematic-layout-hierarchy.md`),
每层都有 tidy(布局计算)+ move(刚移,携带下层全部内容:器件+桩线+旗+登记 note):

```bash
easyeda sch zone relayout --zone MCU --apply    # ★首选:placement-first 区级重排——锚 IC 不动,
                                                #   外围电容电阻**全员竖放同行平行对齐**(电上地下,
                                                #   netport 水平朝左引出),sweep 删净旧桩旗后一遍性
                                                #   重连。不搬带线图元,没有组刚移的 merge 撕裂问题
easyeda sch group tidy --group g5 --apply       # 组内:竖放/上电下地/文字朝外;--deep 连残线清扫
easyeda sch zone tidy --zone MCU --deep --apply # 区内增量:组间 pack(保持连线不重生成时用;
                                                #   组刚移的暂态 merge 短路由 move 内核对账抓住并自动恢复)
easyeda sch zone move --zone MCU --dx -510 --dy -95   # 区整体刚移(注册 note 随行,框自动重画)
easyeda sch sheet tidy --apply                  # Sheet 层:全部区当刚体依纸张排布(图签作障碍
                                                #   L 形避让;已达标幂等 no-op;完毕统一重画框)
```

**顺序铁则(用户拍板)**:布局混乱时**先 relayout 后连线语义自动跟上**——先确认
核心器件方向位置,外围电容电阻的方向/间隔纯计算,最后才生成连线;不要在已连线
的东西上打补丁式挪动。

**统一挪动内核(ADR-0004)**:上面所有挪动/重排命令(zone move / zone tidy /
zone relayout,以及 `sch group-move`、`zone-arrange --apply`、`destagger --apply`)
共用同一个安全 move 内核 —— 快照 → 整树删证回读 → 移动 → **合并早检**(删桩线
是共线合并的触发时刻,重连前就查一次全页网表,被合并吞掉的第三方 pin 当场修回)
→ 快照重连 → netlist+bridge 增量对账,**任一步失败自动恢复到快照重连**,输出
结构化 `moveReport`(moved/recovered/stillBroken/partial),**判据是电气对账不是
坐标**。恢复段辖区是**全页**而非移动集合:凡快照里有网名、现在断连或网名不符的
pin(包括共线合并吞掉的**第三方**器件的脚,esp32Mini P2 的 P0 缺陷),一律按
快照网名重连;灌错网的(如地脚被灌进 +3V3)走 replace(带回读验证的 disconnect
后重连)。不再存在「半途失败需手工重连」的形态;仅 `stillBroken` 非空才需要
手修 —— 条目格式 `REF→期望网`,可直接喂 `sch connect --pin REF --net 期望网`;
标注 `sch disconnect` 的(快照浮空却被灌进网)先手工拆。

**内核重连默认 preserve 桩线(2026-08-20)**:第 4 步对「调用方没给显式端子」的
pin,**先按移动前实测的桩方向/长度原样重建**(刚体平移的语义是几何不变),复现
不了的才退回 autoconnect 评分,而且带**桩长硬上限**(封住 `laneStepFor` 的标准
档位 —— netport 一档 ~89、三档 ~285)。此前一律走自由评分,于是 `sch group-move`
一次「挪一下让开」就把短桩换成长桩、把区框撑胖(真机 315×389 → 523×406,+208 ≈
两档),用户/agent 的直觉操作反而破坏收敛成果。

**恢复段也按已知几何重建(2026-08-20)**:桩线快照现在是**全页**的(不只移动集合),
恢复段/合并早检先用「计划端子 ∪ 移动前实测桩几何」把**单纯断连**的 pin 原样连回来,
复现不了的、以及被灌进别的网需要 replace 的才走自由评分。此前恢复段一律自由评分 ——
连接救回来了,几何却换了一套,一次火警就把 phase A 的收敛撤销大半,连**邻区**
(第三方 pin)一起变形。**恢复段仍有意不夹桩长上限**:那是火警现场,把连接接回来
优先于把框收窄。凡是最终仍走了自由落点的 pin,内核逐条点名进 `moveReport.FreeConnected`,
由 `zone-arrange --apply` 的断言③ 报出来 —— **偏差可以有,但必须可见**。

**`--zone`/`--group` 统一命名空间(ADR-0004 Decision 3)**:所有吃
`--zone`/`--group` 的命令(zone move/tidy/relayout、group-move、group tidy、
`sch note --zone`)走同一个解析器 —— 模块认领 + 虚拟组/子组投影成一张带
来源标签的布局对象表,匹配规则**精确名 > 大小写折叠 > 唯一前缀**,组 id(g1)
与块子组末段名(D_ESD)是别名;`sch zones status` 看全表(名字+来源+别名);
解析失败报错自带全量可用名,类型不适配会指路正确命令。「不同命令认不同名」
的老坑已根治(块页 `zone move --zone POWER` 不再隐身、`note --zone` 不再把
格位名当区名)。

硬知识(实测踩坑):
- **顺序**:先 `sheet tidy` 排开各区(给区生长空间),再逐区 `zone tidy --deep`,
  最后 `sheet tidy` 收尾(幂等,已达标不动)。区带装不下 ≠ 无解——常是邻区挡路,
  是 Sheet 层的活。
- **横竖分桶**:zone tidy 自动把竖放组(双电源旗去耦)与横放组(带 netport 的
  信号链——netport 竖排文字必折叠,只能水平)排**不同的行**,竖一排横一排;
  组的移动次序按暂态依赖自动排序(目标位压谁的原位谁先走)——平台会把暂态
  叠位的共点线 merge 成一根,乱序移动会撕出短路。apply 走统一 move 内核:
  自检红自动恢复到快照重连并复检,`moveReport` 如实报 `recovered`/`stillBroken`;
  仅 `stillBroken` 非空才按 findings 手修(multi-net wire → 删线 + 两端
  autoconnect 重连)。
- **组间 hGap 默认 117** = 两个相向水平 netport 标签实测最小距;压到 40 省空间的
  代价是 `marker-overlap` 一片(实测 3 处)。
- **区间 vGap 默认 90** = 两框 pad(24×2)+ 标题带(30)+ 缝(12)——区内容间距
  决定框间距,小于 78 相邻行的分区框必然相叠。
- **方位词**支持跨两列:`left-center` / `center-right` / `any`(超高主控锚+侧排
  外围的宽区,1/3 网格词罩不住)。方位词现在只影响 `sch autolayout` 的**落位目标格**
  —— 分区框的几何一律由活体模块 bbox 反推,与方位词无关。
- **说明文字必须 `sch note --zone <区名>` 登记**成区成员——自动落点才会瞄准该区
  说明带、zone/sheet move 才带它走;裸 `sch note` 放的文字在区移动后原地掉队。
  区名全名/末段短名/组 id/唯一前缀均可;区不在本页分区计划时 stderr 有 warning、
  输出带 `zoneMatched=false`(绝不静默整页兜底)。注意:登记的说明**不反哺分区框
  几何**(框由器件内容反推;说明住在框内说明带)——不会出现"框每重画一次向下
  长一截"的自增长。
  - **落点是"贴着框底"的,而且是一句算式**:`note.y = 分区框.minY + 16`。
    文字图元的锚点 `(x,y)` 是**块的左下角**、块向上生长(2026-08-20
    `getPrimitivesBBox` 实测 5/5 例 `bbox.minY == y`),所以贴底与行数、字号
    **无关** —— 4 行说明和 2 行说明给出同一个 y 偏移,同页所有说明底边齐平。
    (旧行为按"锚点=左上角"算 `y = 带底 + 块高 + 16`,块高整个变成了离框底的
    距离:2 行 42、3 行 55、4 行 68,行数越多飘得越高、下面白空一大截。)
    y **不吸格**(吸格会把固定内缩打散成 ±2.5 的抖动);x 仍吸 5 格。
  - **说明位置的预留是二维的,而且框会为说明扩边**:
    - **高**:带高按已登记说明的实际渲染高度预留(旧版写死单行 26,2~3 行说明
      结构上塞不进带、被踢到框外)。带高 = 底边内缩 16 + 块高,贴底放进去正好
      把带填满 —— 所以"贴底"永远不会顶穿带顶探进器件区。
    - **宽**:说明先按**框宽**折行(按它自己的 `--font-size` 量,与尺寸回读同一把
      尺),折完还比框宽就把框**横向扩边**。**窄框(如区里只有一个 2 脚接线端子,
      框宽 68)一律扩到最小可读宽度 120** —— 而不是"既装不进又永远报警"。
    - **带内占用**:邻区桩线/marker 伸进说明带时,框底**下探**到占用之下,说明
      **仍然贴着(新的)框底**——位置约束不因避让而放弃,代价由框承担(旧行为
      是把说明踢到"区外走廊",落在框外下方)。
    - 扩边/下探不越过纸边 / 图签安全带 / **邻区的基础框**(留一个 gutter),所以
      为说明扩边不会自己撑出 `partitionOverlap` 让 `zone-draw` 拒画。**顶到底线
      仍装不下时如实失败**(stderr 说清是哪一维不够 + 可执行的下一步),
      绝不为了贴底压到器件/marker 上。
  尺寸只从内容+字号推导(**不读落点 bbox**),幂等;规划器与 `sch note` 落点共用
  同一个预留函数,所以"planner 算的框"必然包住"note 落的点"。
  **放完说明要重跑 `sch zone-draw --mode partition`** —— 框可能已为说明扩边/下探,
  画布上的框是旧的;`sch note` 扩了边会在 stderr 明确提示。
  配套判据 **`note-outside-zone`**(`sch check`,WARN,`--strict` 阻塞):登记说明
  的 bbox 不在自己分区框内即报。文案分两档、**都可执行**:带装得下 → 直接给算好的
  贴底 `--x/--y` 坐标(**与自动落点求解器逐字相同**,而且保证落在**带内**;照抄
  即可,不会再落回原处);可扩边界内确实装不下 → 明说"别原样重跑",改为缩短文字/
  减小 `--font-size`,或 `sch group-move` 给这个区腾地方。
  > 处方曾经和带的定义分家过:带 `(36,12)..(204,70)`,处方却给 `--y 80`(80 >
  > 带顶 70),自己就把说明放到了带外。现在带的定义(`zoneNoteBand`)、落点求解、
  > note-outside-zone 的处方**是同一个函数链**,配对测试钉住。
- pin 号 ≠ 坐标序:`disconnect --pin X:2` 按**引脚号**解析(LED1 的 pin1 可能在
  右侧)。删桩前先 `autoconnect --dry-run` 核对该 pin 当前网名,防拆错脚。
- **tidy 流水线跑完必须 `sch export-image` 做一次视觉复查**——机械门(gate/
  score)只护「已建模的判据」;生成侧和校验侧共享同一份真值表时,表错则双双
  失明(实测:竖直旗 rotation 真值反了两个月,connect_pin 放倒挂旗、linter 判
  它正确,gate 全绿,用户肉眼才抓出)。视觉清单:① 同排竖放去耦顶线/底线双齐
  ② 旗向直立(3V3 朝上、GND 朝下,倒挂=旋转真值病,别只调单件)③ 行左对齐、
  无孤行大留白 ④ 说明文字不压器件、分区框完整包裹 ⑤ 各区风格统一 ⑥ 相邻旗
  文字不叠(`reversed-net-flag` / marker-overlap 文字带判据已下沉进 check,
  但新形态的拥挤仍先靠眼)。看出新的「不舒服」→ 翻译成几何判据下沉,别停在肉眼。
- **IC 多个相邻电源/地脚的画法**:同侧相邻 GND pin(如模组 pin1/40 相距 10)
  各自引旗必然文字互叠——合流:两桩引到同一竖线相接,再引出挂**一支**旗;
  EPAD 单独向下引旗。同侧密集异网旗(AMS1117 左侧 GND/3V3/+5V 三连)用阶梯
  offset 错列(20/50/80),`sch connect --offset` 显式给。
- **信号链末端的电源/地旗必须竖直**(power 上/gnd 下):横躺(left/right)的
  power/gnd 旗文字竖排侧向渲染(平台特性,极难看)。`sch group tidy` 的
  signal-row 会自动竖直化;手工 `sch connect` 时 power/gnd 一律 --direction
  up/down。**netport 顺着导线方向摆布**(2026-08-12 用户拍板,取代旧「永不
  竖放」铁则):竖放件的 netport 顺竖直引出(up=90/down=270 真值表)、横链
  netport 水平(left=180/right=0);拥挤由 marker-overlap 文字带判据管,
  不再单独报 folded。

## Module-aware autolayout — place parts by module zone

Where `autoconnect` is pin-level, **`sch autolayout` is module-level placement**:
it reads a `--spec` (page, sheet, modules with `zone`/`core`/`parts`, rules),
pulls the real geometry (anchors + bboxes + core pins + sheet bbox), partitions
the usable canvas into named zones (`left-top` / `left-bottom` / `center` /
`right` / `right-top` / `right-bottom` / …), places each module's **core IC near
its zone center**, fans the **peripherals around the core** with collision retry,
and keeps each core pin's **fanout channel** and the **A4 title-block** clear.
Same pure-scorer style as autoconnect: identical spec + input → identical
coordinates that pass `sch layout-lint`.

```bash
# preview proposed coordinates + warnings, mutate nothing (default)
easyeda sch autolayout --spec p1-layout.json --dry-run

# pin one page, move parts, read back complete geometry, then save
# safety gate: zero wires/buses/net markers + proven bbox/pins
easyeda sch autolayout --spec p1-layout.json --doc MCU_USB_STORAGE --apply

# structured report
easyeda sch autolayout --spec p1-layout.json --json

# platform FALLBACK engine (no spec): the official eda.sch_Document.autoLayout()
# @beta — a LONG op (~2min), rearranges the WHOLE active (foreground) page,
# connectivity-clustered/radial → messier than a template. For un-templated
# pages only; refine with `sch align`/`distribute` afterward.
easyeda sch autolayout --engine official --apply
```

Template `--apply` is deliberately **pre-wiring only**. It moves symbols via
`schematic.component.modify`, which does not carry attached wires or flags with
the symbol. The command resolves one immutable target page from `--doc` or
`spec.page` (both must agree when supplied), and verifies every response's
document UUID. Before planning and again immediately before the first move it
requires a fail-closed active-page inventory of **zero wires, buses, netflags,
netports, netlabels, and short-symbol markers**, complete finite anchors/bboxes, and explicitly
successful pin-array reads. The second snapshot must byte-for-geometry match the
planning input, otherwise the stale plan is refused. `--apply --all-pages` is
also refused because the proofs are active-page scoped (`--all-pages` remains
available for dry-run). There is no force override: `--rewire` is official-engine
only. After moving, every requested primitive ID/anchor is read back and
the complete baseline plus sheet/grid/spacing/overlap/pin/title-block rules are
rechecked before `schematic.save`; only explicit `saved:true` is success.
Any failure restores captured anchors in reverse order, reads them back (only
confirmed coordinates count as restored), and saves the rollback.

**Why the official engine is dangerous — three measured traps** (this is why the
wrapper refuses wired pages by default):

1. **It moves symbols but not wires** — running it on a wired page severs every
   connection (measured: 16 parts → 59 floating pins).
2. **It lands anchors off-grid** — downstream stubs then miss the pin.
3. **Its scatter makes short stubs collide into shorts** — `--replace` cannot
   pull them apart afterward.

`--rewire` is the only way to run it on a wired page: it snapshots the netlist
first, then after the layout it snaps anchors to the 5-unit grid, deletes the
severed wires, and reconnects from that snapshot. Self-check with `sch check`
(dangling/floating), not `layout-lint` alone. The op needs the target page in the
**foreground** and takes ~2 min (300 s timeout). **The official API has no
transactional rollback** — if the post-check fails the page is already mutated.

**Engine priority (iron rule):** block hit → `sch block-apply` template; else a
`--spec` → `--engine template` (default); only when neither exists → `--engine
official` fallback. The official engine graduated `@alpha→@beta` on 3.2.148 and
now runs, but it produces the scattered generic-algorithm layout our research
predicted — never prefer it over a template for a known block.

Spec JSON (`--spec`):

```json
{
  "page": "MCU_USB_STORAGE", "sheet": "A4",
  "modules": [
    {"name":"USB_HUB","zone":"left-top","core":"U10","parts":["J2","U10","X1","C30","R15"]},
    {"name":"MCU","zone":"center","core":"U1","parts":["U1","C18","C19","R6"]},
    {"name":"SD_NAND","zone":"right","core":"U8","parts":["U8","C28","R10"]}
  ],
  "rules": {"avoidTitleBlock":true,"preservePinFanout":true,
            "moduleGap":80,"routeChannelGap":40,
            "preferVerticalPeripheralPlacement":true}
}
```

The result reports each `placement` (designator / x / y / rotation / module), any
`warnings` (e.g. a peripheral forced into a fanout lane, or a spec part not yet
placed), and a `validation` summary (`partOverlaps` / `titleBlockHits` /
`fanoutKeepoutHits`). Notes:

- **v1 moves already-placed parts only** — it does NOT create missing parts; a
  spec part absent from the page is warned + skipped. Place the parts first
  (library-first), then `autolayout` arranges them.
- A **missing core** is a hard error for that module (clear diagnostic).
- When the **sheet bbox isn't exposed**, the title-block keep-out is reported as
  **provisional** and not geometrically enforced.
- `autolayout` solves **module placement, not routing** — follow it with
  `sch autoconnect` (power/ground/netport) + wiring, then the full per-page S5 gate
  (`sch gate --strict --doc <page>` + the `sch read` topology comparison).

### Deterministic zone layout plan — `sch zone-arrange` (A4-only)

### 1.4 数据驱动的人工式首屏布局

单页重建先把布局写入 connectivity 1.4 快照，再 Apply 到画布：核心器件从左上起点按 Z 字错行落位，外围件根据连接引脚的实际方向贴近对应侧，所有 authored anchor 吸附到 5-unit schematic grid。`placement.rotation` 与 `placement.mirror` 是可回放字段；`bbox` 和 pin 坐标只用于回读验证。这样移动器件不会依赖当前视图，也不会把旧导线几何误当成拓扑来源。Apply 后必须重新读取 pin→net，跑 `sch check`、`sch bridge-check`、`sch drc`，再保存。

排布功能区前先跑 `sch zone-arrange`(纯规划零改动,同一输入唯一输出):

```bash
easyeda sch zone-arrange --project <p> --doc <page>          # 人读
easyeda sch zone-arrange --project <p> --doc <page> --json   # 机器
```

两段流水线:**phase A 区内收敛**(跟随规则 R1-R5:卫星无源件竖放平行跟随锚件、
**端子朝向跟随实测引脚的朝外方向**、netport 恒水平、同件两旗**不得共线** + 同件
端子互不重叠是硬不变式)
→ **phase B 区间求解**(边归属 = 声明 > 质心回退 + 回退链;**每条边可开多层
货架**,回退链整轮走完还放不下才整体往里开第二层/第三层;放不下会**回溯**换上
一个区的候选,不是当场判死;5 格律、无随机)→ 复用 zone-plan 的
validatePartitions(同一把尺)→ 三态 verdict:

- `pass`:每区给出目标框 + 区内成员落位;人读输出里 `(第2层货架)` 表示这个区
  没贴到边、退到了本边第二列/第二行(正常,不是告警)。
- `blocked`:报出**是谁**排不下、**每条边各卡在谁身上**(`S(230)被U挡→
  W(266)被图签挡→…`)—— 出路是进一步收敛或 `sch page-new` 拆页。
  **A4-only:永不建议换纸。**
- `blocked` 且 JSON 里 `arrange.exhausted=true`:搜索预算跑满,**没有证明无解**,
  只是这一轮没搜到 —— 出路一样,但别把它当成「几何上不可能」。

> **phase A 不是优化,是 phase B 的前置条件。**排不下的往往是**形状**不是面积:
> 真机 P3_USB_DL 四区收敛后总面积只占可用面积 46% 却曾报 blocked,根因是老求解器
> 「一条边只能开一列 + 贪心不回头」(2026-08-19 已修)。所以看到 blocked 先回头看
> phase A 那一栏的 `框 A×B → C×D`:收敛没收下来,后面怎么排都白搭。

> **端子挂侧按「边界语义」判,不是「离本体中心哪个分量大」(2026-08-20 根因修复)。**
> phase A 判一支标签挂在器件的哪条边,首版用的是 marker 中心相对**本体中心**的
> 主轴(`|dx| ≥ |dy|` 才算左右)。那条判据隐含假设「本体近似方形」,在**高瘦符号**
> 上系统性翻车:ESP32-S3-WROOM-1 本体 71×421、41 脚**全在左右两条长边**,而标签
> 横向只探出百来个单位 —— 贴在上下两端行的标签 `|dy|` 反而更大,被判成 up/down,
> 于是进了「垂直梯次」(一支竖起来的 netport 就占 63 高,两支摞下来 161.5),
> 框当场从 `449×737` 变成 `244×863`,越过可用高 765,phase B 四条边全报「纸面放不下」。
>
> 现在的口径是 **marker 中心从本体 bbox 的哪条边探出去最多**
> (`outLeft = body.MinX − mcx` … 取四者 argmax;中心落在本体之内时退化成
> 「离哪条边最近」,同一个式子无特例)。它天然把本体长宽比算进去了。同一组几何:
> 首版 `230×897`(排不下)→ 边界语义 `325×556.5`(有落点,phase B 六区全落位)。
> **落地/回退侧的方向来自 `tidyStubDirection`(pin → 标记锚的实测位移),两把尺
> 必须给同一个答案** —— `TestZfSideOf_AgreesWithMeasuredStubDirection` 钉住。

> **计划端子逐 pin 折 + 引脚坐标进计划(2026-08-20 根因修复)。**
> 断言③(落地复判)曾在 `MCU_IO` / `USB_DEBUG` **每一页恒红**,报文点名的
> 「计划未覆盖」清单**全部是 GND 侧引脚**(`C4:2 C5:2 C6:2 LED1:2 SW1:2 SW2:2
> U2:1 U2:40 U2:41` / `C7:2 C8:2 D1:3 J1:8…U3:1`),连跑三轮 apply 区框重叠只从
> 29 收到 19、到不了 0。两条根因都在「规划器手里没有引脚坐标」:
>
> 1. **覆盖面**:端子首版逐 **marker** 折,而 L1 组的「专属 marker」规则不把
>    **共树** marker 算给本组 —— 共树 pin 在计划里根本不存在,而 `--apply` 是
>    逐 **pin** 重建的,漏掉的那几只落地走 autoconnect 自由落点,区框凭空胖一档。
> 2. **朝向**:两脚无源件的引脚位置靠**假定**(本体上下缘中线)+ R3(GND 派到
>    下端)。真机 `C4`/`C6` 是 rot 90 的电容,`+3V3` 脚在本体**下方**、`GND` 脚在
>    **上方**,正好反过来 —— 计划把 GND 端子映到物理上在上面的脚却给 `direction=down`,
>    把 +3V3 映到下面的脚却给 `direction=up`,两根桩线双双钻进本体、共线合并,
>    **GND 整张网并进 +3V3**(日志逐条 `[replaced net "+3V3"]`)。对账当场红 →
>    恢复段把全页地脚自由重连 → 报文那句「计划未覆盖」其实是这条路径的**产物**。
>
> 现在:端子一律**逐 pin** 从活体折出(与 `--apply` 的重建规格 `groupRebuildConnSpecs`
> 同源,计划集合 ⊇ 落地集合),并把引脚坐标折成本体局部坐标带进计划;桩线从**引脚
> 真实所在的地方**出,方向 = 引脚的朝外方向(与挂侧判定同一个函数)。
> 「电源上 / GND 下」回到它本来的身份 —— **推论**(竖放 + 旗顺引脚朝外 + rail 归位),
> 只在**要转竖的件**上仍然可执行(执行侧从 ±90° 两个候选里挑兑现它的那个);
> 已经竖着、不转的件按事实出桩。**硬不变式是「同件两旗异向」**,规划器单独校验
> (`zfCheckPassiveOpposed`,两支 netport 同朝右是 R4 的正常形态,不在此列)。

> **「同件两旗异向」判的是共线,不是同向(2026-08-20 回归修复)。**
> 首版拿「方向相等」当违规,`sch zone-arrange --apply` 因此在页 `POWER` 当场拒绝
> 执行:`J2`(`conn.screw_terminal_2p` / KF301-5.0-2P)两只脚**都在本体左缘外侧**
> (同为 x=50,y 分别 685 / 675),物理上只能都朝 `left` 出桩 —— 「异向」在这个符号上
> 做不到。而两根朝左的桩线一根躺在 y=685、一根躺在 y=675,**平行不共线,永远不会
> 合并**(该端子此前真机 `sch bridge-check` 0 real short、`sch nets` 里 `+5V` 与 `GND`
> 各自独立)。
>
> 真正的短路条件是**两根桩线共线** —— 共线 → 相接 → 平台把导线自动合并成一根 →
> 两张网并成一张。桩只能从 pin 沿 direction 直出、垂直于桩的那个坐标原样留在 pin 上,
> 所以判据是「同向 **且** 同轴」:
>
> | 出桩方向 | 桩线所在的轴 | 共线条件 |
> |---|---|---|
> | `left` / `right` | `y = PinY` | 两脚 **y** 相同 |
> | `up` / `down` | `x = PinX` | 两脚 **x** 相同 |
>
> 同轴容差直接用 `schMarkerOverlapEps=1`(仓库既有的几何噪声地板),**不用 5 网格**:
> 吸附只作用在沿桩方向那个坐标上,桩线所在的轴就是引脚坐标本身。引脚最小节距 10,
> 1 个单位既吃得下浮点噪声又留了 10 倍余量。
>
> 顺带修掉它背后的第二层:两脚同侧时两支标签必然压在一起(节距 10 < 网名带高 12),
> 会被同件端子重叠那条硬不变式判死。**同侧让位**因此从多脚件推广到两脚件 ——
> 两条路径共用同一个函数(`zfPlaceMeasuredTerms`),参与规则一字未改。
>
> **给 agent 的判读法**:看到 `两支旗同向且桩线共线` 别急着换符号,先照报文用
> `sch list --include-pins` 核对两只脚的实测坐标 —— 只有真同轴(符号引脚重合、
> 或同一只脚被折成了两支端子)才需要动;两脚同边但坐标错开是**合法形态**。
>
> 同侧多支标签的**让位**也从「无条件梯次」改成「按需」:撞不撞用的是 `sch check`
> 的 marker-overlap 那把尺(`schMarkerOverlapEps=1`,引脚节距 10 而标签高 11 时
> 必然擦过的那 1 个单位不算撞)。**参与规则沿用首版**:左右侧只有旗让位、且只跟
> 同侧的旗比(port 恒水平、保持短桩);上下侧所有 kind 都参与。
>
> 真机验收(ceshi,两页各一轮 apply):对账**首轮即绿**(无恢复段)、
> `FreeConnected` 为空(报文里那句「计划未覆盖」不再出现)、
> **断言③ 绿** —— 逐区实测框比规划框各小 10(= 落地余量 2×5),区框零重叠;
> 再跑一轮 9/10 件 no-op(收敛)。

> **域感知选形:phase A 选形状时要看空地长什么样(2026-08-20 真机取证)。**
> 收敛不是「把区做方」,是「把区做成这一页塞得进去的样子」。首版两条支路都域盲:
> 「无主导锚件」那条**连候选都没有**(全员单列是硬编码的),锚件那条只会
> `argmin max(w,h)`(求方)。真机 `MCU_IO`(可用域 `1110×765`,图签把它切成
> **左通道 396×765** + **上通道 1110×555**)因此排不下:
>
> - `wroom-passives` 5 个 0402/0805 小无源件被排成 `152×696` 的柱子 —— 高 696 > 555,
>   **只**进得了左通道;
> - `wroom-core` 单件 WROOM 模组 `325×556.5` —— 高 556.5 > 555,**也**只进得了左通道;
> - 两个区抢同一条 396 宽的道:并排 `325+152+12 > 396`、上下叠 `556+696+12 > 765`
>   → phase B `blocked`。而那 5 个小件排成 3+2 的货架只有 `261×352`,上通道轻松吃下。
>
> **注意两个区各自的 `fitRank` 都是 2**(都有落点)—— 「不得变差」门的掉档判据
> 结构上看不见这种病:病在**落点自由度**,不在「有没有落点」。所以选形用两把钥匙
> (都是同一份通道算术的投影,与 phase B 同源):
>
> 1. `fitRank` 三档可排布性;
> 2. `stripFits` = **本页有几条通道装得下这个框**。
>
> 规则:候选里存在装得进通道的形状时**绝不选装不进的**;两把钥匙平局时回到
> **原有紧凑性偏好**(首版会选中的那个形态排最前)—— 所以「本来就排得下、也已经
> 很紧凑」的区一个单位都不会动,不存在「永远选最扁」的退化。`zones[].mode` 尾巴
> 必带这句决策:`域感知选形(5 候选):改选本形态 — 档 2→2、通道 1→2(原偏好
> 「无主导锚件 → 全员单列(位号序)」152×696)`;一个候选都装不进通道时它会直说
> `没有一个装得进任何通道 …… phase B 必然 blocked:拆区或 sch page-new 拆页` ——
> **`blocked` 也是看过域之后的结论**,照着这句去拆,别去调纸张/带高。

> **「不得变差」门:收敛使本区更难排时保留原形(2026-08-20 真机两轮取证)。**
> phase A 对**大符号单件组 / 标签真的挂在上下两侧的宽体连接器**是负优化 ——
> 真机 `MCU_IO` 的 `esp32s3_wroom1_module` 第一轮 `433×541 → 244×767`(宽收 189、
> **高涨 226**,可用高只有 765)。现在 phase A 逐区加一道门:
>
> - 判据是**可排布性**,不是面积/周长 —— 上例宽度和面积都变小了,照样是负优化;
> - 可排布性是**三档阶梯**(`fitRank`),不是一个布尔:
>   `2` 本页有落点(图签也让开了)/ `1` 装得进可用域但被图签挡住(重排、拆页还有救)/
>   `0` 连可用域都装不下(结构上没救)。三档与 phase B 的逐边归因同源:
>   `2` ⟺ 单独放在空页上一定放得下,`0` ⟹ 四条边全报「纸面放不下」,
>   `1` ⟹ 报「被图签挡」。
> - **收敛后掉档 → 保留原形**(不重排、不重生桩,刚体平移到落位框);同档或升档
>   一律放行;两维都没变大时结构上不会掉档。
>   > 首版判据只有 `fits` 一个布尔,于是第二轮真机
>   > `449×737`(1 档:高 737 ≤ 765,只是 449 > 图签左侧通道 396)
>   > `→ 244×863`(0 档:高 863 > 可用高 765)**从门里漏了出去** ——
>   > 它走的是「原形本就排不下 → 收敛是唯一出路」那条放行分支,`retained=false`,
>   > phase B 拿着一个更没救的框去撞墙。别再把这两个「排不下」当成一回事。
> - **逐区独立**:一个区回退不影响其它区照常收敛;
> - **绝不静默**:人读输出该区行首是 `↩`,`zones[].mode` 尾巴与 JSON 的
>   `zones[].retained` / `zones[].retainWhy` 都带这句 ——
>   `收敛回退:高 737→863 后从「装得进可用域但被图签挡住」掉到「连可用域都装不下」(可用 1110×765;图签上方高 555、左侧宽 396)—— 保留原形 449×737`。
>
> 看到 `↩` 时**不要**去调 A4 尺寸/带高绕开它(那会毁掉「框 = L1 全图元并集 + 带」
> 这条不变式);正确的下一步是把这一区拆小(`sch zones set` 重新分组)或
> `sch page-new` 拆页。**两个形状都是 0 档时门不拦**(拦了只是把小框换成大框),
> 这时 phase B 照常 blocked,归因是「纸面放不下」—— 那是真的要拆。

区框口径 = 成员 L1 虚拟组**全图元并集**(标签必在框内)。导线读不到会直接报错
(端子归属靠导线,距离启发式必错)。

> **外框只有一个函数(2026-08-20 用户裁定)**:`frame = f(成员 L1 虚拟组全图元并集,
> 区名带, 说明带)`。`zone-plan` 的框、`zone-arrange` phase A 的现状框与收敛后框
> 走的是**同一个函数本体**。带高由**已登记说明的内容 + 字号**推导(不是常量、更
> 不读 note 的落点坐标)—— 所以 **phase A 收紧时 title/note 就已经在账里**,不再是
> 「按常量带收紧 → 画框 → 再放 note 装不下 → 说明探出框外」。改任一侧,
> `TestRuler_ZoneFrameSingleFunction` 会红。

> **分区归属也只有一个答案(2026-08-20 定案):一个虚拟组 / zone 认领 = 一个分区。**
> `zone-arrange` 一直是这么算的(phase B 每个区一个落位框,断言③ 逐区量实测框、
> 逐对判零重叠);`zone-plan` 此前却先把整页按模块间的自然空隙切成**列带/行带**,
> 再把落在同一格的区**并成一个分区** —— 两把尺。真机 ceshi / MCU_IO:
> `zone-arrange --apply` 断言③ 全绿(区框零重叠),紧接着 `zone-plan` 却把
> `led_indicator_gpio` 与 `tactile_boot_reset`(左列上下叠)并成一框,并集宽到
> x=274,与 229 起的 `wroom-passives` 撞出 45×362 → `partitionOverlap=1` →
> **zone-draw 拒绝画框**,而画分区框是铁律 15,交付被自己卡死。
> 网格带是首版遗留(那时框会被 clamp 到格子里),现已删除。两条推论:
>
> - **`zone-arrange` 断言③ 绿的页面,`zone-draw` 一定画得出来** —— 两边算的是同一批框。
> - **`partitionOverlap` 非 0 现在只有一个含义**:两个区的 L1 体积**真的**互相压。
>   出路是 `sch zone-arrange --apply` 重排或 `sch group-move` 挪件,**不是**调
>   `--gutter` / 也没有「合并成一个大框」这条退路了(合并只是把重叠藏起来)。
>
> 配对由 `TestRuler_ZonePartitionGroupingMatchesArrange` + 真机 fixture 的
> `cmd_sch_zone_partition_test.go` 钉住(含首版归组的常驻变异对照)。

> **桩线伸展只有一把尺(2026-08-20 定案)。** 之前同一件事有三套算法:phase A
> 自己拼端子盒、`--apply` 未被计划覆盖的 pin 走 autoconnect 自由评分、`group-move`
> 的重连也走自由评分。后果是**规划 pass → 落地 overlap 永不收敛**(真机连跑 4 轮,
> 每轮 dry-run 都 `verdict: pass`、validation 四项全 0,落地实测重叠 2/1/2 处;
> 规划 315×351 → 落地 353×382,而 gutter 只有 12)。现在三处共用落地那条真实链
> `connect_pin(direction, offset) → endpointFor(5 网格吸附) → predictedMarkerBBox
> (本体 ∪ 网名带)`,规划框里还含一格落地余量(桩端点的 5 网格吸附)。
>
> **但「规划框 = 落地框的上界」只在模型内成立,别当成真机保证**(2026-08-20 订正):
> 它成立的前提是「同一份 pin 坐标 + 同一份桩长」。真机上三处会打破它 ——
> ① 规划把无源件的 pin 假定在本体 bbox 上下缘中线(真符号未必);
> ② `markerBBoxProfile` 是 2026-06 的实测标定,不是平台契约;
> ③ 计划没覆盖到的 pin 会走 autoconnect **自由方向**落点。
> 真机 MCU_IO 六区实测偏差 `+141 / +126 / +82 / +56 / +26 / +10`,五个区超 gutter。
> 所以断言③ **不是「上界成立」的断言,而是「上界不成立时如实报出来」的机制**;
> 复判只判「落地比规划**胖**」这一边。三条推论:
>
> - **不要靠「多跑几遍」收敛** —— 已实测 4 轮不收敛(第 3 轮落位整体重排,J_USB
>   从 E 边跳到 N 边),那是追尾不是收敛。看 `断言③` 的复判表定位。
> - **`sch group-move` 是刚体平移,不再撑胖区框**:重连按移动前实测的桩方向/长度
>   原样重建(此前一次 `--dx 40` 把 U 组从 315×389 撑到 523×406,重叠从 1 处变 3 处)。
> - **`sch autoconnect` 仍是自由评分**(它的职责就是挑落点),所以在已收敛的区里
>   对单脚补连可能拉出更长的桩 —— 补完看一眼 `sch zone-plan` 的 `partitionOverlap`。

`--apply` 落地执行(断言① 删除集=重建集 → 页级深度清扫 → 逐件落位重连 →
断言② 曾连接 pin 仍连接 → 对账修复循环 → **假失败清创**(自动删同位重复/
同树冗余标记,复用 check 的 suggestDeleteIds 判据)→ bridge-check 红才整体
回滚 → save → **断言③ 落地复判**)。落地执行统一走 ADR-0004 move 内核(失败自动
恢复到快照重连,结构化 `moveReport`,判据是电气对账)。同侧多旗按**垂直梯次**
桩长错开(规划的 offset 直达 connect_pin;pin 再密也不竖叠);计划没覆盖到的 pin
走内核的 **preserve 桩线策略**(原样复现移动前的桩),兜底 autoconnect 带**桩长
硬上限** = 计划里最长的桩。

**断言①的几何形式(retain 刚体不变式,2026-08-20)**:phase A 行首打 `↩ 原形保留`
的区,`--apply` 在**执行前**逐 pin 比对执行指令与移动前快照的 `(方向, 桩长, 类型)`
—— 不一致就**拒绝整页、画布零改动**。「不动的东西真的没动」不依赖任何预测模型,
是本命令最强的可验证不变式;此前它只是一句输出文案:真机 U2 标着「不重排、不重生桩」,
落地后 L1 组却从 391×421 变成 391×562(宽度分毫不差、高度凭空 +141)。报错点名到
pin 与偏差量(`pin4 方向 right→up、桩长 84→20`),那是计划/映射缺陷不是画布问题。

**断言③(落地复判)**:save 之后重读一次真几何,按同一个外框函数算每区**实测框**,
与规划框逐区比。输出形如 `复判 U:实测框 353×382 / 规划框 315×351`。四类红:
① 偏差 > `--gutter`;② 实测区框互相重叠;③ **成员探出图纸可用区**(与
`sch clusters` 的 out-of-sheet 同一个常量,不必再等下一条命令来发现);
④ retain 区落地几何与「原形平移」不符(行尾打 `↩✗ 原形被改动`)。
另有一条独立条目:**走了自由落点的 pin**(计划没覆盖、内核也复现不出原桩,只能
让 autoconnect 挑方向和桩长)—— 它是「规划 pass → 落地胖一档」的唯一结构性来源,
逐条点名。任一条红 → **如实报并以非零退出**(电气与位姿仍已落地保存,不回滚)。
断言①②看电气、内核对账看网表、layout-lint 看器件两两重叠 —— 没有一条看得见
「区框胖了撞邻区」,断言③补的就是这条。**成员读不到时也判红**(unknown 不算过,
不许让一次读故障伪装成完美收敛)。
**真机注意**:连接器在持续变更负载下会停摆,停摆期「报失败的写可能已落地」
(假失败)——apply 已内置重试+对账+清创,但若结束仍报缺口,先用
`sch autoconnect --pin 位号:脚 --kind … --net …` 逐脚补(它幂等,already-connected
会跳过,不会造重复标记),再跑 `sch bridge-check` + `sch nets` 三验。
**不要陷入逐器件手工修补**:apply 报出的问题优先重跑一轮
`zone-arrange --apply`(两遍法,落地实测反哺规划),手写 exec 挪件是最后手段
(且必须 5 的倍数坐标 —— 件是格点公民,脱格 connect_pin 全灭)。

### Functional frames + text labels (multi-page safe)

`easyeda sch zones set --spec <spec.json>` persists `modules[].zone/parts/page`
by resolved schematic **document UUID**. Then draw one page at a time:

```bash
easyeda sch zone-plan --json --doc P1_MCU
easyeda sch zone-draw --mode partition --font-size 22 --doc P1_MCU
easyeda sch zone-plan --json --doc P2_POWER
easyeda sch zone-draw --mode partition --font-size 22 --doc P2_POWER
easyeda sch zone-plan --json --doc P3_PERIPHERAL
easyeda sch zone-draw --mode partition --font-size 22 --doc P3_PERIPHERAL
```

Rectangles are anchored at **`(MinX, MaxY)`** on the y-UP canvas and extend
downward by their height — treating `MinY` as the top-left y shifts the whole
frame down by one height and pushes it past the sheet/title-block edge.

Before drawing a partition, require all five `zone-plan` validation counters
(`sheetOverflow`, `partitionOverlap`, `titleBlockHits`, `moduleOutsideZone`,
`labelCollisions`) to be zero.

> **`moduleOutsideZone` 判的是 L1 虚拟组,而且判定侧独立重算(2026-08-20)。**
> 此前它复用生成侧那份模块 bbox —— 而那份 bbox 已被上游削过,于是「生成漏掉的
> 标签,判定也看不见」,判据结构上恒报 0(真机 POWER 页:8 个 L1 组里 5 个探出
> 框外,六项全绿)。现在判定侧从活体的 `sch clusters` 口径重算每个位号的 L1 组
> 体积再与框做包含判定,并逐条给出**是谁、超了多少、往哪超**。
>
> **降级恒定可见 + fail-closed**:JSON 里 `labelScopeDegraded` 与 `labelScope`
> **永远出现**(不再被 omitempty 抹掉)。归属做不成时(读不到导线 / 某件没有
> 引脚几何 / 某件没有 L1 组记录)`labelScope.degraded=true` 并点名位号,
> `moduleOutsideZone` 按「不可信」计数 —— **验不了就不许报绿**。看到降级先跑
> `easyeda sch clusters --members` 核对,别去调 `--gutter`。

Frames are **always data-driven**: whole-sheet partitions derived from live module
bboxes, 22pt titles by default. The old fixed nine-grid mode (`--mode zones`) is
**retired** — its rectangles had nothing to do with where the parts actually are,
so on a single-module page spanning the sheet the frame missed the circuit entirely.
With frames derived from the parts, `layout-lint`'s old `zone-violation` rule became
a tautology (the frame is drawn *around* those parts), so it is retired too; what
judges a partition now is `sch zone-plan`'s six pre-draw validations. Both modes share one page-scoped frame record, so changing mode replaces
that page's prior annotations without touching another page. Redraw/clear is
fail-closed: exact rectangle/text IDs are re-read after delete, survivors retain
their recovery record, draw counts must match 1:1, and partial creation is
compensated. Every successful draw or clear explicitly requires
`schematic.save` → `saved:true`.

**连接器负载退化下的韧性(2026-08 round2 新 3 修复)**:`zone-draw` 的创建路径
现在是**逐区推进**的:每个区的框线+区名合并成**单次 exec_js**(要么全成要么
全败,失败时 JS 内自清理),单区失败**不回滚**已画成的区。行为要点:

- **幂等重跑**:画前先轻读 survey 画布,已画好且与当前 plan 完全吻合的框
  (标题内容+锚点+配对矩形都在)直接保留 —— 重跑只补缺的区,一页已达标的
  框重跑是**零写操作**;plan 变了(模块挪过)才清旧重画。旧的「先清光旧框、
  再一次画全部」没有了,也就不再有「清旧成功+画新失败=页面从有框变无框」
  的净损失窗口。
- **假失败定律内建**:写报失败后先轻读复核 —— 复核出「其实已落地」就直接
  收编 id、绝不重发;确认没落地才 settle(~400ms)后重发一次;**复核不出来
  (读也失败/落地状态歧义)一律不重发**,可证的半成品 id 记入本页 frame
  record,`--clear` 或下次重画会回收。
- **partial 语义(#151)**:部分区画成时**exit 0**,stdout 报
  `partial: N/M zone(s) not applied` + 每区原因;全部区都失败且画布零变化才
  非零退出。看到 partial 就**重跑同一条命令**补缺,不要手工 exec_js 补框。
- daemon 侧配套:`easyeda health` 的 `writeHealth` 按窗口报最近 20 次转发动作的
  **效果失败率**(不是返回码失败率,见下条)+ 连败数 + 逐 action 分桶,
  `degraded:true` = 连接器在负载下劣化;此时**写**失败(以及「返回 ok 但被证明
  没落地」)的响应会带结构化 `result.degraded` + 「先轻读复核再考虑重试」的告诫。
  daemon 只对幂等导航动作(`document.open`/`schematic.page.open`)自动
  「轻读探测→settle→重发一次」,内容写永不 daemon 级重发。
- **writeHealth 读的是「写的效果」,不是「调用的返回码」**(2026-08-19 口径修订)。
  真机跑完一整场端到端时它曾全程 `failureRate 0.05 / degraded:false`,而同期画布
  上大面积的写根本没生效 —— 因为主要故障形态是**返回成功但画布没变**。现在:
  - 返回成功 + 回读证实没生效 → 计 failure,并记进 `fakeSuccesses`;
  - 返回失败 + 回读证实已落地 → **不**计 failure,单独记 `fakeFailures`
    (同样是不健康信号,但处置相反:假成功要补写,假失败绝不能重发);
  - `verified` = 有回读证据的样本数。**`verified` 很低而 `failureRate` 很绿,
    只能读成「没人核对过」,不是「全都好」**;
  - `actions{}` / `degradedActions[]` 是逐 action 分桶 —— 混合流量里
    「connect_pin 这一批 40% 失败」不会再被 20 样本的均值稀释成 5%,
    哪条路没在工作会被点名。
  证据两个来源:连接器在 result 里自带的回读结论(`partial` / `survivedTotal` /
  `notApplied`)由 daemon 直接内省;命令自己做的回读(block-apply 落地回读、
  `sch connect` 的 slow-landed 复核、zone-draw 的 landed-check)走
  `POST /writeverify` 回传。**新写带回读的命令时,把结论也回传一次**
  (`reportWriteVerified`),否则健康度看不见这条路的真实成色。

## Zone-less packing — `sch autoplace-free`

Where `autolayout` needs you to name zones, **`sch autoplace-free` finds the sheet's
blank space for you** and drops movable parts in, collision-free — the "把这些件塞进
纸面空白" case. Parts only (never wires/flags — that's `sch group-move`), so it's pure
CLI-side (reuses `components.list --include-bbox` + `component.modify`, no connector
handler). Deterministic top-left first-fit, anchors snapped to the 5-grid.

```bash
easyeda sch autoplace-free --dry-run                 # auto-pick messy parts, preview
easyeda sch autoplace-free --designators C1,C2,R4 --apply
easyeda sch autoplace-free --all --apply             # repack the whole page (tidy mode)
```

Move-set: **default** auto-selects parts currently OUTSIDE the usable area or
OVERLAPPING another (clean in-bounds parts stay put); `--designators A,B` targets
explicit parts; `--all` repacks everything. Fixed (non-moved) parts + the
title-block keep-out are obstacles it dodges. `--margin` (sheet-edge inset),
`--gap` (min edge-to-edge), `--grid-step`, `--no-avoid-titleblock`. `--apply`
moves via `component.modify` then self-checks with layout-lint. A big part on an
already-full page honestly reports **"no free slot"** rather than overlapping — use
`--all` so it gets first pick, or free up room. Verified live: 3 stacked parts →
`--apply` → 0 overlap.
