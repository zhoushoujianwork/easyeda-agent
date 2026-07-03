
# EasyEDA Design Flow — 首席EDA工程师流程脊柱

你是**首席EDA工程师**。整板/非平凡设计**不允许**「边想边随手摆」——那正是覆盖、外围乱飞、线压元件的根因。
按下面的**分阶段 + 硬门禁**走:每个阶段有明确产出和**过门条件**,**门不过不进下一阶段**。

> 这是**编排层**,不重复规则或动作细节:
> - 具体动作(place/wire/modify/move/align/…) → [`easyeda-agent`](./schematic.md) / [`easyeda-agent`](./pcb.md)
> - 设计规则(分区/间距/朝向/选型) → `easyeda-agent` references
> - 本 skill 只负责**顺序、分组、门禁、自调闭环**。

## 核心原则

1. **先规划,后落子。** 没分页、没编组之前,一个元件都不放。
2. **先确认图纸,默认 A4。** 生产级布局必须先有可读 sheet primitive。若 `easyeda sch sheet-geometry` 找不到图纸/边界,立即停止:让用户在 EasyEDA 选择/创建默认 A4 图纸,或明确批准 debug/临时路径。**无图纸不允许摆放、布线、autolayout apply。**
3. **按图纸容量分页。** 先按 A4 可用区评估模块面积和布线通道;放不下就自动分页、按模块拆页,不要把单页坐标外扩当作解决方案。
4. **按组摆,不按件摆。** 芯片和它的外围(去耦/晶振/上下拉/接口)是**一个整体**,一起定位、一起移动,组与组之间留出布线通道。
5. **每步可验证。** 摆放后用 `easyeda sch layout-lint` 拿**机械真值**判覆盖/间距,而不是靠肉眼或截图(截图可能 stale)。
6. **门不过就回退。** layout-lint 有 ERROR、DRC 有 fatal → 立刻调整,不带病往下走。
7. **用户要求逐步确认时,每阶段后停住。** 若用户说“每一步等我确认”或类似要求,每个 S 阶段只做读回/报告/建议,等待用户确认后再进入下一阶段;不要连续清页、摆件、布线。
8. **过一阶段就存盘(硬规则)。** `place`/`wire`/`modify` 只改 EasyEDA **内存**,不 save 就**不落盘**——窗口重载、daemon 重启、EasyEDA 崩溃都会**丢未保存的工作**。daemon 默认开了**防抖 autosave(3s)** 兜底(变更停 3s 自动 `schematic.save`),但它是安全网不是替代:① 防抖窗口内进程挂掉仍丢最后几笔;② autosave 可能被 `--autosave-debounce 0` 关。所以**每个阶段门通过后仍显式 `easyeda sch save` 一次**(见各阶段 💾),即时落到已知良好点。本流程里 save 是既定步骤;若用户开启逐步确认,save 前也报告并等待确认。

## 阶段流水线(原理图)

```
S0 预分析 → S1 图纸/分页💾 → S2 模块编组 → S3 按组摆放💾 → S4 通道布线💾 → S5 校验门 → S6 调整闭环💾
                                            ↑___________________________________|
```
> 💾 = 该阶段通过后 `easyeda sch save` 存盘检查点(见原则 5)。整板放置时,S3 每放完几组(或每 ~10 件)就 save 一次,别等全放完——崩一次就白干。

### S0 — 预分析(摸底)
- **做什么**:读懂设计——器件清单、电源树、功能模块划分、目标幅面。
- **怎么做**:见 conventions 的 `design-pre-analysis.md`(轻量摸底)。`easyeda health` 确认已连接。
- **产出**:模块清单(如 MCU、电源、USB、传感器、调试口…)+ 每个模块的器件归属。
- **过门条件**:每个器件都归到了某个模块;已估算 A4 页数和每页模块归属。若用户要求逐步确认,在这里停住给出计划。

### S1 — 图纸 / 分页(先图纸,再分页!)
- **做什么**:确认当前页有图纸,默认 A4;再按模块/功能把设计**先分到几页**(电源一页、主控一页、接口一页…),别全堆一页。
- **怎么做**:`easyeda doc ls` 读页结构 → `easyeda doc switch` 切目标页 → `easyeda sch sheet-geometry --json` 读 sheet/title-block。无 sheet 或 provenance 为 none 时停止,不要开始 place。需要多页时用 `easyeda sch page-new` / `page-rename`;复杂模块独立成页。
- **💾 过门条件**:每个目标页都有可读图纸(A4 默认)和明确职责;每页模块预计能落在可用区内,标题栏 keep-out 明确 → `easyeda sch save`。若用户要求逐步确认,保存/继续前停住。

### S2 — 模块编组
- **做什么**:在每页内,把「芯片 + 其外围电路」定义为一个**组**,并规划各组在页面上的**分区位置**(谁在左、谁在右、信号流向)。
- **怎么做**:分区/信号流向规则查 conventions 的 `schematic-layout-conventions.md`。此阶段只规划坐标分区,先不落子。布局 spec 里的 `sheet` 默认写 `"A4"`;zone 必须落在 S1 读到的 sheet 可用区内。
- **过门条件**:每个组有明确的目标矩形区域,组间预留了通道(不重叠的分区);若模块太多,已经拆到下一页而不是挤压本页。

### S3 — 按组摆放(芯片 + 外围一起)
- **做什么**:**逐组**放置——先放该组核心芯片,再把它的外围**就近**放在芯片周围(去耦贴电源脚、晶振贴时钟脚…),放完一组再下一组。
- **怎么做**:`easyeda sch place` + `sch modify`(设位号);坐标按 S2 的分区。库优先、选型规则见 schematic.md / references。
- **整组分区摆放优先用 `easyeda sch autolayout`**(模块级放置规划器):把 S2 的分区写成 `--spec`(每个 module 给 `zone`/`core`/`parts` 与规则),它按真实 bbox 把核心芯片放到分区中心、外围环绕核心、碰撞自动重试,并保留引脚 fanout 通道 + A4 标题栏 keep-out,**确定性产出可过 layout-lint 的坐标**。先 `--dry-run` 看方案,确认后再 `--apply`(经 `component.modify` 落子并自检 overlap)。`--apply` 前必须有真实 sheet bbox;无 sheet 只能停在 dry-run/修图纸。**v1 只移动「已放置」的器件**,不创建缺件——所以先 `sch place` 把器件放上页,再用 autolayout 排布。手动 `sch place`/`modify` 仍是逐件微调的兜底。
- **💾 过门条件**:进入 S4 前**必须先过 S5 的 layout-lint**——本组无覆盖、组内外围紧凑、组间不挤。**有 ERROR 先回 S3 调整**(`sch move`/`align`/`distribute`)。过了就 `easyeda sch save`(整板放置每 ~10 件存一次,别等全放完)。

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
   - **默认只检真实器件**:图框/标题栏(sheet)与 netflag/netport 等非器件原语已自动排除,不会再误报"器件压图框"(issue #13);要连这些一起检查才加 `--include-non-parts`。
2. **电气门** `easyeda sch drc` + `easyeda sch check`(+ `scripts/lint.sh <project>` 数据 lint)
   - `sch drc` 调 EasyEDA SDK 的 `sch_Drc.check`;当前 EasyEDA build 可能只返回聚合/布尔结果,**不等于 UI DRC 面板的全部 warning**。
   - `sch check` 是对 UI 面板缺失项的重建式补强:悬空脚、导线交叉/穿脚、网络标识与导线名不一致、同一导线多网络名等。**生产门禁必须同时跑 `sch drc` 和 `sch check`**。
   - fatal/error 必须修;`net-marker-mismatch` / 不同网络名同线属于必须修;悬空 IO 只有明确设计为 NC/备用并记录后才可接受;供应商编号/标准化 warning 属 BOM 门禁,交付前修。
- ⚠️ **判状态看数据(`sch list` / layout-lint / drc),不看截图**(API 改动后画布可能不重绘 → 截图 stale)。

### S6 — 调整闭环(立刻调,再验)
- layout-lint 报覆盖 → `sch move`/`align`/`distribute` 把冲突元件挪开 → **重跑 layout-lint**。
- DRC 报错 → 补线/补 flag → **重跑 drc**。
- **💾 循环直到两个门都干净,再 `easyeda sch save` 收尾**。这就是「DRC 后立刻调整」要的闭环。

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
- **P1 导器件**:`pcb import-changes` 会**弹 UI「应用修改」**(平台限制,无 headless apply)——要全自动改用 `pcb add-component` 逐件放。导完 notify。
- **P2 摆放**:`pcb auto-place`,默认 `--assembly-gap 40`(留烙铁焊接位;纯布线间距 ~28mil 太挤焊不了)。RF/天线件周边别塞小件。
- **P3 板框**:`pcb outline-round --rect … --margin 120`(圆角,贴器件包络)。📸 录制模式:布局+板框成型后抓一张阶段截图。
- **P4 禁布区(靠前!)**:天线/挖槽用**一个多层区域**即可——`pcb region create --layer 12(多层) --rule no-pours --rule no-wires --rule no-fills`,一个区域盖全铜层,**不用逐层建 4 个**;内层用「填充区域」禁止,不需要 no-inner-electrical。**删旧区域要「删完校验再建」**——delete 紧跟 create 同批次会竞态,删没生效就累积。
- **P5 丝印对齐(靠前!)**:`pcb silk-align`(位号摆正+位置感知+`--spacing` 装配间距)。导入的位号常 180° 倒置,这里一并摆正。放布线前,让布线避开丝印占位。📸 录制模式:禁布区+丝印就位后抓一张阶段截图。
- **P6 可布性门**:`pcb layout-lint`(≥ 目标分、0 overlap、ratsnest 交叉可控)。
- **P7 布线**:`pcb route-short`;个别擦焊盘的手工 `pcb track` 绕行。**注意 `rip-up` 会连电源缝合过孔一起删**——之后重跑 `power-planes` 补回。📸 录制模式:信号布线完成后抓一张阶段截图。
- **P8 叠层+电源+铺铜**:`pcb stackup set --layers 4` → `pcb power-planes`(GND内电层+VCC内层+缝合过孔)→ 顶/底 `pcb pour-fit --net GND --replace=false`(`--replace` 默认 true 会清跨层同网铺铜)→ `pour-rebuild`(退让禁布区)。**内层平面用 SIGNAL+铺铜**,别翻 PLANE 类型——PLANE 负平面不自动退让禁布区会报 Plane Zone 错;SIGNAL 铺铜物理等价且自动退让。📸 录制模式:电源布线(+5V 路径)后、以及铺铜/内电层完成后,各抓一张阶段截图。
- **P9 极性/板注**:`pcb silk-add` 加 LED +/− 极性标(锚点=左下角,放器件外的空白处别压焊盘/本体)、板注 credit;`pcb silk-set --ref board --align centerx` 居中。
- **P10 门**:`pcb drc`(passed)+ `pcb check`(0 issue)双清零,再 `pcb save`。📸 录制模式:DRC 通过的最终态抓一张阶段截图,作为交付收尾图。

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
