# Changelog

All notable changes to the **EasyEDA Agent Connector** are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/); versions
follow [SemVer](https://semver.org/).

## [Unreleased]

## [0.16.0] - 2026-07-20

### Added — 原理图放置方法论(一套自己的摆放方法)
- **块 `schematic_layout` 模板**:电路块 JSON 新增 `schematic_layout`(role → `{dx,dy,rotation}`
  相对坐标模板,y-UP、5 格对齐、须覆盖全部 role,schema + `go test` 双校验)。`sch block-apply`
  优先按模板落件(去耦贴电源脚一字排开/上拉靠引脚/晶振·FLASH 分列,信号流左入右出,人审一次
  终身复用),无模板才退回网格;**原点自动避碰**(不显式 `--at` 时按已有器件真实 bbox 螺旋找空位)、
  落后回读真实 bbox 把 overlap 写进 manifest。esp32s3r8_chip_minsys / led_indicator_gpio 首批带模板。
- **`sch zones` 功能分区认领 + `layout-lint` zone-violation**:S0 spec 的 `modules[].zone` 持久化,
  `sch layout-lint` 新增"认领件落在分区矩形外"的 WARN(与 PCB zones 独立)。
- **`sch zone-draw` 分区框可视化**:把认领画成虚线区域框 + 区名文本(`eda.sch_PrimitiveRectangle/Text`),
  与 zone-violation 同一几何,所见即所校验;`--clear` 精确移除,不碰用户图形。
- **`sch align` / `sch distribute`**:按真实渲染 bbox 对齐(left/right/top/bottom/centerx/centery)/
  等距摊开,默认 dry-run、`--apply` 落地自检;补齐 design-flow S6 一直引用却不存在的命令。
- **`sch autolayout --engine official` 官方 autoLayout 兜底引擎**:包装平台 `eda.sch_Document.autoLayout()`
  (@beta,3.2.148 起可用)。它对已连线页是**破坏性**的(移件不移线 → 断线、落 off-grid),故加安全管线:
  **已连线守卫**(无 `--rewire` 拒绝)、**跑后吸附 5 格**、`--rewire`(跑前捕获网表 → autoLayout → 吸附 →
  删断线 → 重连)、自检用 `sch check`(查断线)不止 `layout-lint`(查重叠)。定位:模板未命中页的兜底起点。

### Added — 机制
- **`--doc <uuid|name>` 全局 flag**:根治 doc-switch racing。所有命令默认对"当前前台文档"操作而
  `doc switch` 异步,长命令(autoLayout ~2min)/跨命令时前台漂移会把编辑落到错误的页。`--doc` 在
  `postAction` 咽喉点加守卫:变更动作(catalog `Mutates`)落地前 `ensureActiveDoc` 切目标页并用**实时
  `document.current`** 确认(不看缓存 /health),确认不了**拒绝**而非编辑错页;导航动作豁免防递归。
  真机验证:前台停 P2 时 `block-apply --doc P1` 稳稳落 P1、P2 不动。**多页/长操作一律带 `--doc`。**

### Fixed
- **原理图坐标系 y-UP 定音**:双探针文本实测 3.2.148 画布为 y-UP(y 大=视觉上方),修正 `zoneRect`
  的 top/bottom 映射(此前按 y-DOWN 写反,autolayout/zone-violation/zone-draw 上下翻转)、标题栏
  keep-out 锚点(此前保护右上角,实际标题栏在右下)、`sch align --mode top/bottom` 语义。

## [0.15.2] - 2026-07-19

### Added
- **#127 `pcb.track.lock`**:按 net(string 或 string[])和/或 primitiveIds 批量**锁定/解锁**
  铜皮布线图元(track/arc/via)。P7.0 关键网络先行流程的最后一步——电源+差分先布好后锁定,
  后续自动布线/rip-up 动不了(rip_up 本就跳过 locked)。拒绝空过滤(不隐式锁全板);幂等
  (已处于目标状态的只计数不重写);逐图元 `setState_PrimitiveLock` + `done()`(#134 教训)。
  CLI 侧配套:`pcb track-lock` 子命令 + `pcb route-critical`(电源铺铜→差分成对布线+skew
  实测→锁定,一条命令承载 design-flow P7.0)。

## [0.15.1] - 2026-07-19

### Fixed
- **#135 `schematic.bridgeCheck` 线段级锚定**:flag/pin 归树从「顶点邻近」改为「点到线段距离」。
  EasyEDA 把两条重叠共线 stub 合并成一条线后,被吞的 flag 落在**线段中段**,顶点判定永远锚不上
  ——一树双网的真短路因此漏报为 0 findings(ina226 块验证实录)。同时新增 **ORPHAN_FLAG** 检测:
  不挨任何导线的 netflag/netport(删合并线留下的孤儿)单列上报,防止新画的线静默继承其网名。
- **#136 `schematic.components.list` 跨页撞号免疫**:同一 designator 在文档内解析出多个不同
  device 身份时(跨页撞号;子部件 U1.A/U1.B 同身份不误伤),该件 pin 的 net 强制置 null 并标
  `netAmbiguous:true`——netlist 按 designator.pin 全文档取网,撞号时归属被毒化,给错网比不给更糟。
  CLI 侧配套:`sch block-apply` 分配代号改查**全文档**(不再只看当前页);`sch autoconnect` 对撞号
  件显式告警;block-apply 收尾新增 **netlist↔plan 对账门**(#135),不一致非零退出。
- **#137 `schematic.power.connect_pin` 瞬态重试 + 回滚**:建 stub 线瞬态失败自动重试一次(250ms),
  终错带端点坐标;flag 创建失败时**回滚已建的线**,不再留无 flag 孤儿桩。
- **#137 `schematic.pin.disconnect` 合并树感知**:定位到的线可能是合并树——flag 搜索从「仅两端点」
  扩到**全折线(顶点+中段)**,一并删除失宿 flag;新增 `alsoDisconnectedPins` 返回字段,列出因删线
  被连带断开的其它 pin,调用方可据此重连。
- **`sch autoconnect` 同批次短桩互斥**(#138,自 #133 Bug 1 拆出):此前每个连接
  只对「既存」图元评分,同一批里刚规划的短桩互相不可见——同器件相邻异网引脚
  (隔离 DC-DC B0512S 类四域脚)会选出共线相触的短桩,被 EasyEDA 合并成隐性
  多网短路(真机:CS/CS1/CS2 地网全部并入 GND)。现在每个已规划短桩立即以
  目标网注册回 scene 当作既存导线,后续连接沿用 #64 的异网触碰硬拒——自动换
  方向/offset 错开,四向全堵时响亮报 "no safe candidate" 拒绝落笔而非放置
  短路桩。两条 httptest 回归覆盖「转向错开」与「全堵响亮失败」。

## [0.15.0] - 2026-07-19

### Changed
- **⚠BREAKING:连接器扫描端口段从 `49620-49629` 迁移到 `60832-60841`(`0xEDA0`-`0xEDA9`)**。
  旧段是当初照抄官方 `eext-run-api-gateway` 的约定(docs/ecosystem-survey.md),导致
  官方生态的外部工具和我们的 daemon **抢同一个端口绑定**(先起的占 49620,后起的挤走
  或抖动)。新段把 "EDA" 直接写进十六进制端口号,专属、好记、零冲突。
  **升级须知**:daemon(CLI)与连接器必须**同时**升到本版本——新连接器只扫新段,
  旧 daemon(≤0.14.x,监听 49620)将永远连不上,反之亦然;`--ports` 手动指定旧段
  可临时兼容旧连接器。

## [0.14.1] - 2026-07-19

### Fixed
- **`schematic.pin.set_no_connect` 真正生成非连接 X 标识(订正 0.5.14 的平台 no-op 误诊)**:
  真机复现确认 `setState_NoConnected(...)` 只修改当前 pin 句柄的待提交状态,必须再
  `await pin.done()` 才会写回画布;此前 handler 漏掉 `done()`,fresh readback 因而恢复
  `false`,被误判成 EasyEDA Pro 3.2.x 平台限制。现在改走
  `sch_PrimitiveComponent.get(id) → component.getAllPins()`,逐脚 setter + `done()`,再用
  新器件实例回读验证。Pro 3.2.149 真机已确认绿色 X 出现、`noConnected:true`,且
  `sch check` 的 floating-pin 计数同步减少;`--clear` 同路径持久化清除。
  (#133 Bug 3 / #134, 感谢 @zhiqiangme 贡献)
- **skill 脚本 Windows 中文环境 GBK 解码崩溃**(#133 Bug 4):bulk-connect /
  bulk-place / sch / diff 四脚本的 `subprocess.run` 显式 `encoding='utf-8',
  errors='replace'`——CLI 输出恒为 UTF-8,此前 `text=True` 在 Windows 中文环境
  按系统 GBK 解码,器件描述含中文时 `UnicodeDecodeError` 崩溃。
- **文档**:environment-setup.md 新增 PowerShell 5.1 吞 JSON 参数双引号的说明
  与绕行(`--%` / 反引号转义 / CSV 形式 / PowerShell 7+)(#133 Bug 5)。

## [0.14.0] - 2026-07-18

**「真机可信化」版**:一天内 16 个 issue 闭环的集中发布。三大主题:
① **via/导入可信化**——EPAD 内嵌热过孔的删除欺骗与赋网易失被彻底摸清(删不掉:
假成功+立即 getAll 也骗+reload 原 id 复活;赋网只活到 reload),`pcb.route.delete`
前置拒删、`pcb.add_component` 放置即键合、CLI 新增 `pcb via-bond` 幂等重键合 +
`pcb check` netless-via-in-pad 触发器;**import-changes 十七天误诊破案**——它从来
不是 no-op,是「确认导入信息」对话框没人点,现在 handler 自动点「应用修改」,
clear→reimport 往返打通。② **布线器硬否决**——异网走线/slot 从代价升级为
hopFeasible 硬门(R2 两条真交叉短路的根治),mount-holes 反查既有铜皮。
③ **门禁与工具诚实化**——power-not-poured 对 GND 内电层死锁解除(PlanePouredNets
状态互通)、`--force` 分级放行(零确认机械骨架要 `--force-unsafe`)、`pcb clear`
默认 verify 复合流程、高 pin 连接器不再抢 main、陶瓷贴片天线 keepout、
根级 `easyeda health` 别名。

### Fixed
- **`pcb.import_changes` 假 no-op 根因修复(#124,订正 #20 诊断)**:importChanges 一直
  都能正确算出变更清单——它弹「确认导入信息」对话框等人点「应用修改」,API 返回 true 只
  代表**对话框弹出**;headless 没人点,看起来就是静默 no-op(「不支持增量导入」是误诊)。
  真机实证:点击后 20 件全部落板。handler 现在自动等待对话框并点「应用修改」
  (`confirm:false` 保留人工审查),且 importChanges 的 promise **不再串行 await**
  (实测某些状态下永不 resolve,会卡死连接器整个动作队列)——改为并发点击 + 12s 超时
  兜底,以器件计数差(componentsBefore/After)为落板真值。
- **`pcb.route.delete` 假报成功修复(#120,真机订正)**:SDK 的 `delete()` 对**封装内嵌
  via**(QFN EPAD 热过孔是 component 的一部分)返回 `true` 且**立即 getAll 也显示已删**,
  但 save/reload 后从封装定义原 id 复活(ceshi 真机实证)——纯 readback 会被骗。handler
  现在**前置结构判定**:via id 以某器件 primitiveId 为前缀 = 内嵌,直接拒删进
  `notDeletable[]`(附父器件 + 指引 `pcb via-bond`),`ok:false`;其余照常删除并
  readback 兜底(`removed`/`count` 只统计真正消失的,不可归因的幸存者进 `notDeleted`)。
- **`pcb.add_component` 内嵌 via 赋网(#118)**:封装内嵌的 EPAD 热过孔 `net=""`,
  EPAD 永远焊不上 GND 平面且每颗报一条 same-footprint SMD Pad to Via。现在赋完
  pad 网后,枚举落在本器件已赋网 pad 铜皮矩形内的无网 via,用
  `pcb_PrimitiveVia.modify`(@beta)赋成该 pad 的网,并 readback 验证(#120 教训:
  SDK 布尔不可信);结果新增 `embeddedVias {assigned, verified, failed}`。
  ⚠️ 真机实证:该赋网**不能活过 doc reload**(平台每次都把内嵌 via 重物化为无网)——
  CLI 侧配套 `pcb via-bond`(exec_js 路线,旧连接器即可用)负责 reload 后重键合,
  `pcb check` 新规则 netless-via-in-pad 是触发器。

## [0.13.0] - 2026-07-16

**「规范进代码」版**:PCB 设计规范从「文档等 AI 自觉去读」变成**机器强制**——26 条
`pcb check` 规则的报错自带 `[规范 §N]` 章节引用、布完线必须过 `post_route_checked`
门才能进交付、电源线宽按公制阶梯自动给宽。配套补齐芯片级(ESP32-S3 裸片)选型与电路块,
并新增 `pcb clear` / `pcb mount-holes` 两条命令。

### Added
- **`pcb.page.clear` — 一键整版清空 PCB**(`easyeda pcb clear`),`schematic.page.clear`
  的 PCB 对称版。一次删掉所有板级内容:器件 + 布线(轨/弧/过孔)+ 铺铜/填充 + keep-out/规则区域
  + 自由丝印(丝印层 3/4 的字符串**及线/弧图形**,不误删铜层文字或机械/装配图元)。
  `pcb.component.delete` 只删器件、布线/铺铜会静默残留;这个才是真正的清板重来。
  **默认保留锁定图元 + 板框(layer 11)**;`--only components,routing,copper,regions,silk` 收窄、
  `--no-preserve-outline` 连板框删、`--include-locked` 连锁定件删。`--dry-run` 只统计不删。
  复用 `rip_up` 的 copper-only 规则,布线永不误伤丝印/板框。无 undo,确认门控。
  **内部枚举→删除→再枚举循环到 0**(上限 5 轮,返回 `rounds`)——首轮枚举可能读到 stale
  引擎态漏项(实测 153 轨清完仍剩 8),循环补清等价于用户手工再跑一遍;dry-run 不循环;
  撞轮次上限仍有残留则追加 warning 提示 save+reload 再跑,绝不假报干净(#112)。
- **`easyeda pcb mount-holes` — 四角 M3 安装孔自动放置**(#102):读板框 bbox 四角内缩放孔
  (`--dia` 默认 126mil=Ø3.2mm / `--inset` 197mil≈5mm / `--corners tl,tr,bl,br` 子集 /
  `--dry-run`)。孔形态 = layer-12 多边形 fill(与 `pcb slot` 同原语,零新增 action);
  keep-out = max(孔半径+40, 垫圈118mil),圆-矩形相交逐角查器件 bbox,**冲突警告+跳过,
  绝不压件**;已有孔报 `exists`(幂等重跑)。
- **`post_route_checked` 阶段门 —— 「布完必查」机械化**(#97 续):`workflow advance` 在
  布线后自动跑 `pcb check`,**ERROR + power-not-poured + width-under-spec 三项清零**才放行
  丝印/交付;其余 WARN 报告不拦。13 个布线类 action 标 `InvalidatesStage` → 改线后门
  自动重新关上。拒门时逐条打印 blocking finding(自带 `[规范 §N]` 引用)。
- **`easyeda pcb modify --center`**(#105):`--x/--y` 解释为**期望的 bbox 中心**而非器件
  锚点(锚点常偏中心,实测 ESP32 模组偏 135mil,旋转件更甚);`pcb list --include-bbox`
  输出注入 `center` 字段。默认语义不变。
- **`pcb fill create --at x,y --size w,h`** 别名(#109):消除 `--rect` = 两角点的歧义。
- **audit 客户端归因 + 并发写 advisory**(#108):Request 带 `clientId`
  (`<hostname>:<pid>[:EASYEDA_CLIENT_LABEL]`),audit JSONL 每行可归因;不同会话 10 分钟内
  写同一板 → 响应附 `concurrentWriter` 警告(不阻断,CLI 打 stderr)。
- **`staleRisk` advisory —— 铁律 5 机械化**:PCB mutation 后未 `doc reload` 就读/DRC →
  daemon 在响应附警告(CLI stderr),`pour-rebuild`/reload 后自动解除。
- **芯片级(ESP32-S3 裸片)物料链**:`standard-parts.json` 补 6 类选型(#106,S3R8 裸片
  C2913194 内封 8MB PSRAM / W25Q64 / APS6404L / 40MHz 晶振 / 2.4G 陶瓷天线 / π 匹配);
  电路块库新增 2 个 draft 块 `block.esp32s3r8_chip_minsys` + `block.ant_2g4_ceramic_pi`
  (共 23 块:20 ready / 3 draft)。
- **`pcb.components.list --include-pads` 返回焊盘真实铜皮 `width`/`height`**、
  **`pcb.silk.list` 返回 `fontSize`**(0.12.1 起):clearance/DFM/避障从名义常量升级实测值。
- **PCB 设计规范手册**(`skills/easyeda-agent/references/pcb-design-rules.md`):13 章,
  JLC 工艺 + IPC-2221;`pcb check` 报错的 `[规范 §N]` 即指向此手册章节。
- **`sch bridge-check` 规则类型化**:`wire-bridge`(ERROR)/`orphan-stub`(WARN),
  JSON 可按类型 gate,对齐 `pcb check` 强制力。

### Changed
- **`pcb check` 新增 5 条 DFM 规则**(共 26 条),全部自带 `[规范 §N]` 引用:
  `silk-over-pad`(§11.2 丝印压焊盘)、`decap-too-far`(§3.1 去耦电容离 IC >2.5mm)、
  `via-in-pad`(§2.3 同网过孔打在焊盘上)、`copper-near-edge`(§5.1 铜距板边)、
  `fiducial-missing`(§9 SMT 板缺 Mark 点,INFO)。旧的 11 类规则也补上了章节引用。
- **net-class 线宽阶梯改公制圆整**(规范 §1.2):电源分档从 mil 碎值(10/15/20mil =
  0.254/0.381/0.508mm)改为公制推荐值 **branch 0.25mm / trunk 0.4mm / high-current 0.5mm**
  (9.84/15.75/19.69mil);signal 仍取板的 live 规则值。存量按旧阶梯布的板零追溯告警。
- **`easyeda pcb power-pour`**:2 层板电源自动铺铜(`power-planes` 的 2 层版)——GND 全板
  pour + 各电源轨局部**动态 pour**(非 static fill,防异网短路)。
- **删除类命令统一收 CSV 与 JSON 数组**(#109):`pcb delete` / `pour-delete` /
  `region delete` / `fill delete` / `track-delete` / `via-delete` —— `pcb drc --json`
  的 `objs` 数组现在可直接粘贴。

### Fixed
- **clearance 判据改铜皮边缘距**:track↔pad / via↔pad 原按径向 max(w,h)/2 判(USB-C 长条
  焊盘旁的合法走线被误报 21 条);track↔via / track↔track 原按**中心线**距判并打印,导致
  「runs 16.9mil — under the 6mil rule」自相矛盾文案,且**两条 10mil 线中心距 8mil(铜皮
  已重叠)竟放行 = 漏报短路**。现全部按真实矩形/半宽算边缘距。
- **route-short detour 段 fine-pitch 收窄改子段级**(#107):原为整 hop 级,任一端点落在
  密脚场就把整条多层绕行(含对侧层电源 trunk)连坐收窄到 6mil,载流严重不足。
- **workflow 指纹 reload 后误报 placement drift**(#100):坐标取整放粗到 1mil + 压平
  -0.0、旋转折进 [0,360)、layer 数字与名称统一映射;顺带修掉 `asString` 读数字 layer 恒
  空串导致**翻面对指纹不可见**的隐藏 bug。
- **`place-constrained` 检测不到 slot 挖的 M3 孔**(#104,holes 恒 0 → Tier-1 避让失明):
  根因是解析 `pcb.fill.list` 的 `points` 字段,而连接器只在 `includeBBox` 里给几何。
- **`via-crosses-plane` 的「plane 无网」分支降为 INFO**(#110):实证 PLANE 层 pour 在
  `doc reload` 后被装进负片存储、扩展 API 无任何读取路径(平台行为,非本项目 bug),
  该分支在 reload 后必然假阳性 → 不再计入 warnings/拦 `--strict`,message 指引以
  `pcb drc` Connection=0 为准。
- **`pcb.silk_netnames` 碰撞检测**从硬编码 50×50mil 改真实焊盘 extent。
- **`--dry-run` 预览不再被当成 mutation**(#112):daemon 侧统一按 payload 的 `dryRun`
  标志把预览排除出 `Mutates` 判定 —— 之前只跑 `pcb clear --dry-run`(不改板)也会 arm
  `staleRisk` 并触发 autosave;现在 staleRisk / autosave / concurrentWriter 三处一致。
- **`workflow advance` 门失败时非零退出**(#113):post_route_checked 拒门(或门跑不起来)
  时 exit≠0,脚本 `set -e` / CI 循环终于拦得住;与既有「阻塞在人工签核时非零」OR 合并。
- **门与 `power-planes` 不再自相矛盾**(#114):`power-planes` 判定「内层已被占用 → 该网
  改走线」(`routeAsTracks`)的电源网记进 workflow state,post_route_checked 门豁免其
  `power-not-poured`(仍打印并标注 exempt)。此前两个工具互相打架:去铺撞已有平面、
  不铺过不了门。豁免跟着**布局**失效(placement_confirmed 及更早)而非布线。
- **`bom export` 定位 `bom-enrich.py`**(#115):六级探测(`--script` → `EASYEDA_SKILLS_DIR`
  → 已安装 skill 目录(复用 `skill status` 同一份逻辑) → 可执行文件兄弟路径 → cwd → `$PATH`),
  非仓库 cwd 下不再找不到;找不到时列出**每一条**探测过的路径。

## [0.12.1] - 2026-07-14

结束「pad 尺寸靠猜」时代:焊盘/丝印把**真实几何**送到 Go 侧,所有 clearance/DFM/避障
规则从名义常量升级到实测值。

### Added
- **`pcb.components.list --include-pads` 返回每个 pad 的真实铜皮尺寸 `width`/`height`**
  (mil,轴对齐,已按 pad rotation 换轴)——从 `getState_Pad()` 形状元组提取
  (ELLIPSE/OVAL/RECT/NGON;复杂多边形 pad 无廉价 extent,省略字段,消费方回退名义值)。
  Go 侧 `pcb check` 的 clearance / via-in-pad / silk-over-pad 与 `route-short` 避障
  即刻消费:大焊盘(USB 壳/散热盘)不再漏报,0201 小盘不再误报。
- **`pcb.silk.list` 返回每条丝印文本的 `fontSize`**(mil;attribute 与 free string 都带)——
  `pcb check` silk-over-pad 的文本 extent 从「40mil 假设」升级为真实字号估算。

### Changed
- `pcb.silk_netnames` 的碰撞检测 pad 尺寸从硬编码 50×50 改为真实 extent(同上回退)。

## [0.12.0] - 2026-07-14

本版把**项目工作流机械化**(#97)、补上**手焊可达门**(#99)、让 `place-constrained`
**网感知分组连接器**,并修掉三处在 esp32Mini 端到端回归里暴露的工具张力(Type-C 突出板框、
beautify 圆角与 `pcb check` 的弧不感知)。

### Added
- **项目 design-flow 状态机机械强制(#97)**:新增 `easyeda workflow`(init / status /
  advance / confirm / reset)——6 段门(imported→placement_ready→placement_confirmed→
  outline_confirmed→pre_route_passed→routing_authorized)。daemon 在 `/action` 层**拦截
  未授权布线**(`pcb.line.create` / `pcb.via.create` / `pcb.import_autoroute`,fail-closed);
  布局/朝向类 action 携带指纹,任何 mutation **自动失效**下游确认并全链回退。
- **手焊铁路门(#99)**:`pcb layout-lint --gate` 增加**手焊可达**检查——每个器件至少一侧有
  ≥60mil 净通道(否则「四面被围」判 fail);配 `pcb stage set-assembly hand-solder`
  落盘的装配档(min-gap 40 / large-pad 60)。
- **`pcb.line.list` 返回 copper 弧(`arcs`)**:与 `lines` 并列返回圆弧图元的端点,向后兼容
  (旧 CLI 忽略新字段)。让 headless 检查能把「track 端接在弧端点」识别为已连,而非悬空。

### Changed
- **`place-constrained` 网感知分组**:`edge="user-facing"` 的连接器(USB / SD / 端子 / 排针)
  **分组到同一条共享边**并沿边紧凑排布;共享边由**网感知**选(贴连接器电气搭档主芯片的那条边,
  经共享 local 网,而非几何质心)——USB 贴 CH340 同侧,缩短差分对、少交叉(实测同种子 61→28 交叉)。
  `edge="any"`(RF/模组)保持各自最近边。

### Fixed
- **`layout-lint` off-board 改按焊盘中心判**:连接器身/courtyard 突出板框(Type-C mating 面、
  卡座、螺钉端子)但**焊盘全在框内**属有意贴边,不再误判 off-board、不再卡死 confirm-layout
  授权链;无焊盘件兜底 bbox(焊盘边到板框净空仍归 DRC)。
- **`pcb check` 弧感知(消除 beautify 圆角伪报)**:`beautify` 把拐角圆化成 track→arc→track 后,
  `dangling-end` 与 `floating-track-island` 两个检测因**不认弧**而爆假阳性(实测一块板 dangling
  0→130、island 0→10,而官方 DRC 报 0 断)。现 `dangling-end` 认「同层弧端点=track 端已连」、
  `floating-track-island` 用**弧桥接** union(镜像过孔桥接)——实测两者双双归零。

## [0.11.3] - 2026-07-12

守护进程端口稳定性(补丁):

### Fixed
- **多 daemon 抢端口根治**:daemon 改为**单一固定端口 49620**(不再顺移到 49621…)。旧行为下
  第二个 `daemon start` 会悄悄占下一个端口,而连接器扫整段 49620–49629 会**在多个 daemon 间抖**
  (陈年 `make dev`/air 实例的典型症状);旧 PID 文件兜底不可靠。新的 `ensurePortAvailable`:
  空闲→绑;被**我方 daemon** 占(`/health` 探测确认)→**自动替换**(端口级,可靠);被**别的
  程序**占→交互终端问 `[y/N]`、headless(air/nohup 无 TTY)报清晰错误退出——**绝不静默杀外部
  进程**。连接器侧不用改(仍扫 49620–49629,先命中 49620)。

## [0.11.2] - 2026-07-12

本版聚焦 **PCB 布局智能** 与 **电路块库扩张**,并将连接器上架官方插件市场。

### Added
- **电路块库扩张(~15 个新拓扑块 + 两批器件入库)**:sy8089 3V3 同步 buck、tps63802
  buck-boost、usbc_ufp_power_or(USB-C 设备口 + VBUS 二极管 OR)、ch334f USB2.0 四口 hub、
  bq24074 power-path 充电、vehicle_input 车载 12–24V 前端 + tps54360 车规 buck、
  pmos_highside 高侧软启动、opto_acc_ign 车载 ACC/点火检测、axp2101 PMU 等。
- **`easyeda pcb antenna-keepout`(新命令)**:按块声明(`keepout.end_frac`)自动为
  RF/天线器件在**每个铜层**(MULTI 层)生成 no-copper 禁铜区——只盖模组**无焊盘的天线端**
  (不孤立接地焊盘)、幂等;`--dry-run` / `--pad-clearance`。
- **边缘端子自动定向**:块声明连接器开口方向(`openings:[{match,local}]`),
  `pcb place-constrained` 据此把螺钉端子等**开口自动转向板外**;焊盘对称且块未声明的
  连接器则**显式提示手工确认**(不凭焊盘几何乱猜)。
- **连接器上架[立创EDA官方插件市场](https://jlc-ext.com/item/zhoushoujian/easyeda-agent-connector)**
  —— 新增市场一键安装通道,平台可**原地自动更新**;侧载 GitHub Release `.eext` 仍与 CLI
  **严格同版**,是四件套对齐的权威来源(市场版本可能滞后 CLI,每次发版需网页端手动重新提审)。

### Changed / Fixed — PCB 布局智能(place-constrained 大修)
- 器件分类改**消费块 placement 数据**(位号前缀)而非硬编码正则 + 新增显式 `anchor` 档;
  分类字符串改用真 `manufacturerId` 而非 `"={Manufacturer Part}"` 模板(修 WROOM 被误判为
  主芯片、连接器按名认不出)。
- 边缘吸附读**真板框**(`pcb.outline.get`)而非件云 bbox;Tier-4 卫星**按共网聚类**到
  所属芯片(优先局部信号网)。
- **对抗审查修 6 个潜在 bug**:天线 keepout 不再压焊盘(`--pad-clearance` 让开焊盘本体)、
  块 `_doc` 键不再丢整块 placement、天线幂等收紧(只认 MULTI 层全铜禁铜)、netSeed 优先
  局部网、天线器件识别在 `pcb check` 与生成器两侧对齐。

### Verified
- `go test ./...` 全绿;**ceshi 真机逐条验证**(WROOM 模组 `main`→`edge`、J1 螺钉端子开口
  朝外、天线 keepout `pcb check` loop 0→1→0);两轮多 agent **对抗审查**复核 5 个 review 修复
  (0 新增回归)。

## [0.11.1] - 2026-07-10

`pcb beautify` 打磨(补丁):

### Added
- **多网美化**:`pcb beautify --net` 现在**可重复**——`--net USB_DP --net USB_DM`
  一次只美化这几个网(连接器新增 `nets: []string` payload)。密板上**首选按网做**
  而非整板一把梭:blast radius 小、每网可 dry-run + DRC 逐个验收、出问题好定位。

### Fixed
- **计数虚高**:`arcsCreated`/`linesCreated`/`cornersRounded` 之前在 DRC 二分修复
  的每一轮都累加,导致 `drcRounds>0` 时 summary 远大于最终几何(实测 4 拐角报 21 弧)。
  改为按路径记最终态、末尾汇总,数字现与落盘几何一致。

### Verified
- **连通性不被破坏**(控制实验,ceshi):对已知连通的多拐角网做 beautify,前后均为
  **单一连通分量、端点不变**(即使 DRC 修复把部分拐角退回直角)。据此判定此前 esp32
  真实板上 SD_* 的「断连」是那块 v0.2 板**原有**的未布通,非 beautify 切断。
  教训固化进 `references/pcb.md`:密板/未 DRC-clean 的板优先 `--net` 按网做、先测基线。

## [0.11.0] - 2026-07-10

功能版本(minor):**PCB 走线美化 `easyeda pcb beautify` 上线**——吸收自开源扩展
[`m-RNA/Easy_EDA_PCB_Beautify`](https://github.com/m-RNA/Easy_EDA_PCB_Beautify)
(Apache-2.0),补齐布线定稿后的美化后处理档,接在 design-flow **P7.9**(P7 布线之后、
P8 铺铜/出 Gerber 之前)。

### Added
- **`easyeda pcb beautify` — 走线美化(拐角圆弧化)**:新 typed action `pcb.beautify`。
  把已布好的直角/锐角铜走线圆滑成圆弧(改善美观 + 可制造性,减少尖角蚀刻风险)。
  - `--scope all|selected`(默认 all)/ `--net` / `--layer` 过滤;`--selected` 只处理
    EasyEDA 里框选的走线。
  - **拐角圆弧化**:把同网同层相接的线段串成多段线,每个内拐角按 `--radius-ratio`
    (默认 3,半径=最大线宽×3)生成 fillet 圆弧,替换原线段为「截断直线 + 圆弧」。
  - **差分/等长同心圆弧**:成对/等长网的拐角走同心圆弧保护——feature-detect
    `pcb_Drc.getAllDifferentialPairs` / `getAllEqualLengthNetGroups`,该 build 无此 API
    时**降级为保直角**(不阻断)。`--no-protect` 关闭。
  - **自带安全网**:美化会 delete 原线段 + create line/arc,故内建 **DRC 二分修复**
    (`--drc-retry`,默认 4:缩半径或退回直角修违规拐角)+ **自动重铺覆铜**(同网 GND
    键合会 stale,复用 `pour-rebuild`)。`--no-drc` / `--no-pour-rebuild` 可关。
  - **`--dry-run`**:只计算规划(paths / lines / arcs)、**不动板**——可在任意真实板上
    安全预览。**只处理铜层,绝不碰丝印/板框**,跳过锁定铜。
  - 其它:`--force-arc`(线段太短也生成截断圆弧)、`--merge-u`(紧凑 U 型弯合并为
    单个大圆弧)。
- **几何库移植**:`extension/src/beautify/{math,arcGeometry,drc}.ts` 从上游纯几何
  verbatim 移植(无 eda.* 依赖),`index.ts` 为 headless 编排(去掉上游自研快照/撤销
  与 iframe 设置面板,改 payload 驱动、结果结构化返回)。
- **接入两个上游 DRC API**:`pcb_Drc.getAllDifferentialPairs` /
  `getAllEqualLengthNetGroups`(差分对/等长组读取)。

### Docs
- `references/design-flow.md` 新增 **P7.9 走线美化档**(dry-run 先行 + 上游告警清单:
  焊盘-走线连接需人工复核、RF/高速网排除全局美化、出 Gerber 前预览);
  `references/pcb.md` 加 `pcb beautify` 命令条目;`docs/ecosystem-survey.md` /
  `docs/marketplace-coverage.md` absorb-list 标记已吸收(#1c)。
- **署名**:新增仓库根 `NOTICE`,记录 Apache-2.0 第三方来源、原作者 m-RNA、逐文件
  映射与相对上游的改动;几何文件头保留出处注释。

### Known limitations
- **线宽贝塞尔平滑**(上游 widthTransition)本版未移植——follow-up。
- 运行时已核:`pcb_PrimitiveArc.create` 在当前 web build **确认落笔提交**(ceshi 真机
  探针:create→getAll 命中→delete 净零还原,`err:null`);此前 `route-short --corner
  round` 的「native arc 不提交」笔记指的是 outline 的 MathPolygon 分段弧,与 primitive
  arc 无关。整链路端到端(路径链接/DRC 二分/重铺)首用建议仍在 ceshi 跑一遍确认。

## [0.10.0] - 2026-07-10

功能版本(minor):**电路块库 `easyeda blocks` 上线**——从「器件→块→流程」三层
库的拓扑层落地为可离线查询的旗舰能力,配套 skill 自动同步、PCB 引脚级丝印批注、
整板批量落图脚本与切页竞态收口。

### Added
- **`easyeda blocks` — 离线电路块库查询(embedded)**:`ls` / `show <id>` /
  `search <query>`,块库 JSON 用 `go:embed` 编进二进制,**零 daemon / 零窗口 /
  零 skill 文件**——异地/裸机 `easyeda` 安装即可查已验证外设电路(CH340/自动下载/
  buck/RS485/CC1101/microSD…),不必下载 GitHub 库。skill 的 `references/blocks/*.json`
  仍是社区源(PR 进这里),`make sync-blocks` 构建前拷进 `internal/blocks/data/`
  再 embed;`internal/blocks` 带漂移守卫测试(embed 副本 ≠ skill 源即 CI 失败)。
  本次同时从 case001 提炼 4 新块入库(XL1509 buck / SP3485 RS485 / CC1101 巴伦 /
  microSD),再补最小系统 3 块(AMS1117 LDO 5→3.3 / BOOT+RESET 按键 / GPIO LED 指示),
  块库达 **10 ready**——esp32MiniRequire 最小系统已 100% 块覆盖(模组+CH340+自动下载+
  LDO+按键+LED)。
- **Skill 目录自动同步 + 连接器落后提示**(免手动升级):`daemon start` 默认带
  `--auto-update-skill`,启动时后台把已存在的 skill 目录(`~/.claude`、`~/.codex`)
  拉齐到最新 release 并逐步打日志(尊重 `EASYEDA_SKILL_PRESERVE`)。新增
  `easyeda skill status` / `easyeda skill sync`(`--version`/`--preserve`/`--client`/
  `--create-missing`/`--json`)手动查看与触发同一机制。连接器一注册即比对版本,落后
  时打**可操作日志**(重导 `.eext` + 彻底重启 EasyEDA)——sideload `.eext` 无原地
  自动更新(市场专属能力),故只检测+提示、不静默替换。install.sh 装完 skill 写
  `.version` 标记与 daemon 对齐避免重复下载。**连接器代码未变,升级无需重导 `.eext`。**

- **PCB 丝印批量标注**:`pcb silk-netnames`(`pcb.silk.netnames`)按矩形区域为网络
  名自动生成免碰撞丝印;`pcb silk-label-pads`(`pcb.silk.label_pads`)为器件焊盘按
  引脚号/网络名批量标注,支持 X/Y 轴贴齐(`--align-axis`)与朝向自动判定,便于给
  排针/连接器逐脚标功能。
- **`easyeda doc open <name|uuid>`**:`doc switch` 的语义化别名(open 一个文档),
  与 `doc ls`/`switch`/`reload` 风格一致。
- **整板批量落图脚本**:`scripts/bulk-place.py`(manifest→放置+位号回写)与
  `scripts/bulk-connect.py`(连接 spec→autoconnect+期望网表验证门+悬空脚修复循环),
  沉淀自 box-v2 139 件整板实测;`references/auto-layout-sop.md` 补 5 条实测经验
  (netport-first、引脚重合盲区、切页 settle 竞态、check 裸对象信封、验证门)。

### Fixed
- **切页竞态收口(#67)**:`document.open` / `schematic.page.open` 现在在返回前
  轮询活动页器件数直到 settle(连续两次相同即视为装载完成,`0 → N` 的装载中态
  不会被误判为空页),并在 result 中带 `ready:true/false`。修复「`doc switch`
  返回成功后立即 `sch check` 拿到空 findings、隔 2-4 秒才完整」的问题。PCB 无
  components 可轮询,乐观返回 `ready:true`。

## [0.9.0] - 2026-07-08

自 0.8.3 以来的整体收口版本:PCB 布线/DRC 从「能放」走到「能连、能查、能救」,
原理图从零建图闭环幂等化,并沉淀了「抄官方板 → 自主设计」的训练方法论与
ADR-0002 前置设计交互。以下按主题汇总 0.8.4–0.8.13 各 dev 迭代的用户可见变化
(逐条明细见下方各历史条目)。

### Added
- **PCB 布线手术刀 + 换层跳线**:`pcb.route.delete`(按 primitiveId 精准删
  track/arc/via,rip_up 不再整网重铺)、`pcb.route.via_hop`(入口 stub→via→对层
  track→via→出口 stub 的复合换层,默认两层各放同网键合 fill 桥接 track↔via 裸结点)、
  `pcb.route.route_short` 多层布线(长/跨层 hop 用 via 换层,不再全推迷宫档)。
- **headless DRC 盲区补齐**:`pcb check` 新增通用间距规则(抓导线短接)、via-bond
  (ERROR:track↔via 裸结点未被键合 fill 覆盖)、floating-track-island、dangling-end
  (面积锚定)、缝合过孔间距、挖槽/过孔间距感知;`pcb drc` 支持 `--json`/`--timeout`
  + daemon 侧防重入。DRC 实测 66→31、Connection 9→0。
- **单网内电层**:同一 GND 网可独占内电层填充(via-stitch → pour)。
- **原理图分组与断连**:`sch group-move`(器件+周边 stub 导线/flag 刚性整体平移,
  无状态虚拟分组)、`schematic.pin.disconnect` 支持 `pinX`+`pinY` 坐标定位。
- **`sch autoplace-free`**:零区域自动布局,把件塞进纸面空白处。
- 标准器件库扩充:芯片级设计 11 件 + case001 通用料 24 件(`references/standard-parts.json`)。
- `pcb.silk.label_pads` 新增 `alignAxis` / `--align-axis` 参数:可选择按 X 轴或 Y
  轴贴齐标注(`x`/`y`/`auto`),并在结果中返回 `align_axis_chosen`。适合排针/连接器
  等不同朝向封装,让丝印标注沿焊盘阵列形成整齐队列。

### Changed
- **`sch autoconnect` 幂等化(issue #50)**:`components.list --include-pins` 的每个
  pin 附带权威网表来源的 `net` 字段,autoconnect 据此三态判定 pin 是否已连目标网,
  重跑不再叠加 flag。
- **Skill 方法论沉淀**:ADR-0002 前置设计方案书(S0)+ 三档交互模式(决策交互化、
  执行自动化);紧凑布局设为默认 + 手工修线三律;官方板对标 §7.8–7.9 地策略选择判据
  (单 GND→双 PLANE;多地域→全 SIGNAL 分区 pour);抄图训练闭环(XDS110 174/174 pin
  一致 + 多页拆分)固化为验收方法。

### Fixed
- **`sch page.rename` 写后自校验(issue #55)**:改名后 `doc ls` 读到旧页名 → 短间隔
  重试读回确认,命中 `verified:true`,超时如实返回 `verified:false`+warning。
- **`--all-pages` 非激活页壳数据告警(issue #54)**:`sch read/list/check/layout-lint`
  对非激活页如实警告 pins/bbox 可能为空,`layout-lint` 把 skip 升为醒目 WARN。
- **`sch check` geom-net-mismatch 对 netflag/netport 静音**:designator 为空的原语
  网表交叉校验静音,几何单独判(64/64 误报→0)。
- **`sch autolayout` 锚点 snap 到 5 网格**:分区居中产出的分数坐标(206.25)不再致
  connect_pin 批量失败;`connect_pin` 引脚离网格时快速失败给可行动报错。
- **lib search 对 LCSC C 号精确匹配**:不再模糊命中错料。

## [0.8.9] - 2026-07-07

闭环优化 B/P0 收尾:布线手术刀 + 换层跳线复合动作(封 pro-api-sdk#31 track↔via
不导通坑)。配套 daemon 侧 DRC 防重入 + 超时预算传导(CLI `pcb drc --json/--timeout`)。

### Added
- `pcb.route.delete` — 按 primitiveId 精准删 track/arc/via(rip_up 是整网粒度,
  一颗错 via 不再重铺全网)。`kind` 守卫拒绝贴错类别的 id;锁定跳过、陈旧 id 报
  `notFound`;`removed[]` 回显每个被删图元的完整 before-state(net/layer/几何),
  audit log 足以重建。
- `pcb.route.via_hop` — 复合换层跳线:入口 stub → via → 对层 track → via → 出口
  stub,**默认在两颗 via 的两层各放一片同网键合 fill**(4 层/曾有 PLANE 板上裸
  track↔via 结点不注册导通,fill 面重叠是唯一可靠桥接)。via 距端点 `stub`(默认
  20mil)防压焊盘;中途失败整体回滚。

## [0.8.8] - 2026-07-07

插件市场审核修复（承接 0.8.4–0.8.7 的图片系列问题，最终定案）。逐个把关卡打通：
(1) 0.8.3 外链 jsDelivr → 判"README 图片未显示"；改成打进包。
(2) 写 `./images/…`（带 `./`）→ 上传弹"图片未通过审核"；对拆 4 个高装机成功插件
    发现它们一律写 `images/xxx`（无 `./`），去掉 `./`。
(3) 仍被服务端错误码 `101019`（"图片未通过审核"，英文 "Image failed moderation"）
    拦。**直接打平台 `/api/v1/images/upload` 逐张实测**：`logo.jpg`、
    `demo-pcb-layout.gif`、`demo-esp32-board.png` 全过；唯独
    `demo-schematic-generation.gif` 恒返回 101019（鉴审 GIF 解码路径异常，非内容
    违规——其静态帧 PNG 实测过审）。**故把这张动图换成静态 PNG（完整原理图那一帧）**。
最终演示图集：原理图静态 PNG + PCB 布局 GIF（保留一张会动的）+ ESP32 成品板 PNG，
全部经 `/api/v1/images/upload` 实测过审。纯市场展示层，连接器代码无变化，无需重连/重启。

### Fixed
- 市场错误码 101019“上传的图片未通过审核”：`demo-schematic-generation.gif`
  的动图被鉴审解码器判异常 → 换为同内容的静态 PNG（已逐张实测过审）。
- （承接）README 图片路径去掉 `./` 前缀，写 `images/…`，对齐成功插件一致写法。

## [0.8.3] - 2026-07-06

Typed PCB layer/view switching for bottom-side visual QA (#40) + release-flow
ClawHub integration. Connector code changed (`extension/src/actions.ts`) — this
release **requires a connector re-import** (uninstall old → import new .eext →
fully quit & relaunch EasyEDA). Version 0.8.2 is skipped: it was burned on
ClawHub by a stale-content skill upload (clawhub workdir trap; versions are
immutable there), so 0.8.3 keeps CLI/connector/skill aligned.

### Added
- **Typed PCB layer/view actions** (no more manual UI clicks for bottom-side
  checks): `pcb.layers.set_current` (`pcb layer-set`, accepts
  id|name|top|bottom|inner1), `pcb.layers.visibility` (`pcb layer-visibility`,
  presets top-only|bottom-only|copper-only|silk-only or explicit show/hide),
  `pcb.view.side` (`pcb view-side`) — selects the side's copper and focuses its
  copper/silk layers so the next snapshot reflects that side. No native canvas
  flip API exists, so view-side is a layer-focus approximation, not a physical
  board flip.
- **`make release` now publishes the skill to ClawHub** at the same version
  (best-effort — a hub failure doesn't block the release; retry with
  `make publish-skill VERSION=…`). Uses an absolute path to dodge the clawhub
  global-workdir trap.

### Fixed
- **`currentLayer:null` readback (#40)** — `pcb.layers.list` now activates the
  PCB tab before `getCurrentLayer` and returns `visibleLayers` as display-state
  evidence when `currentLayer` is empty.
- **README install commands** — removed the non-working skillhub.cn CLI command
  (web-only community, no `/api/cli/v1` endpoint); ClawHub + GitHub Release
  `skills.tar.gz` are the supported skill-install paths.

## [0.8.1] - 2026-07-04

Fresh-PCB pour-reflow fix, solidified end to end (root cause pinned → commands →
playbook → fresh-board replay-verified: DRC 55 → 1). Go/CLI only — no connector
code change, no re-import needed.

**Root cause pinned**: a PCB document created in the current session and never
reloaded computes pour reflow from a **creation-time rules snapshot** — rule
writes (readback shows them), `pour-rebuild`, and tab-switching have NO effect
until the document is really closed and reopened; already-reloaded documents
honor rule writes immediately. On top of that, the fresh-board reflow runs ~3%
under the configured clearance (10mil → ~9.7mil) and skips thermal spokes
(suspected platform issue, to be reported upstream).

### Added
- **`easyeda doc reload [name|uuid]`** — save + close + reopen a document (a
  real reload; `doc switch` only changes the foreground tab). Refreshes the
  per-document reflow rules snapshot; run `pcb pour-rebuild` after reloading a
  PCB. Saves first (typed `schematic.save`/`pcb.save` by document type), so no
  edits are lost.
- **`easyeda pcb drc-rules-set --pour-clearance <mil>`** — the write side of
  `drc-rules` (v1 knob: pour/plane copper clearance, **raise-only** — never
  loosens a stricter board). Patches `Plane` `lineClearance` (copperRegion both
  pad models + innerPlane) of the current rule configuration, writes it back
  (bare-config API shape — the `{name, config}` wrapper silently no-ops),
  verifies by re-read; a write on a system preset turns it into a per-board
  自定义配置 copy, as the platform requires.
- **esp32-mini playbook: `rules-pour-margin` + `reload-pcb` + `pour-rebuild-2`
  steps** (182→186) — margin 10→12mil before pouring, then a document reload +
  second rebuild after, so a replay on a **freshly created PCB** passes DRC
  directly. Verified end to end on a fresh board (ceshi/Board4): 47 + 186 steps
  green, official DRC = 1 (the known #33 add-component netlist false positive).

## [0.8.0] - 2026-07-03

Recording/demo-mode reliability — a one-call **stage capture** that gates on the
frame, plus **blank-frame detection** — and a **netless-pour** cleanup, on top of
the accumulated connector fixes.

### Added
- **`easyeda pcb stage-snapshot --stage "…"`** — one-call recording/demo STAGE
  capture: native PCB snapshot + a data bundle (components/tracks/vias/pours/nets/drc)
  + a `stage.json` manifest, **GATED on the frame** — a BLANK / STALE / wrong-document
  (foreground tab not a PCB) capture exits non-zero, so a `set -e` recording script
  halts instead of banking a bad frame. Go/CLI only, no re-import.
- **Blank-frame detection on `pcb/sch snapshot`.** The capture reads the FOREGROUND
  tab's rendered canvas, which comes back BLANK when the window isn't visibly rendering
  (minimized / backgrounded). The CLI decodes the PNG and WARNs on a blank frame —
  distinct from a STALE (byte-identical) one. Verified: no API call (view fit / zoom /
  ratline / openDocument / tab-switch) repaints a hidden window; the only fix is
  bringing EasyEDA to the foreground. Go/CLI only.
- **`easyeda pcb pour-clean --netless [--dry-run]`** — remove copper pours bound to
  no net (dead copper that `pour-fit --replace` can't clear — it only matches same-net
  pours). Surfaced by the new `pcb check` **netless-pour** rule. Go/CLI only. (#34)
- **`easyeda debug exec --timeout <sec>`** — override the 20s round-trip default for
  slow eda.* calls (e.g. `sch_Netlist.getNetlist()`, which can loop >90s on a schematic
  with floating pins — prefer `getNetlistFile()`, which `sch read` uses and never hangs).
  Go/CLI only, no re-import.

### Fixed
- **`connect_pin`/`sch autoconnect` now actually FORM the net (grid-snap fix).** The stub
  endpoint (grid-aligned pin + a non-grid offset like 18 → 338) landed off the 10-unit
  schematic grid, but a created netflag/netport SNAPS to the nearest grid point (340) — so
  the stub ended a grid-step short of the flag's connection pin, the flag floated
  unconnected, its net NAME never applied, and same-named flags NEVER merged (every pin
  became its own auto-named 1-pin net `$1N…`). connect_pin now snaps its stub endpoint to
  the grid so it coincides with the snapped flag pin. Verified: two `--net MERGE` netports
  now read back as one net `MERGE deg 2 [R1.2, R2.1]` (was two `$1N` singletons). Unblocks
  building a netlisted schematic from scratch via the API. (Re-import needed.)
- **`pcb.pour.create` refuses a netless pour.** An empty/absent `net` used to be
  silently coerced to `''`, creating dead copper (a net:"" pour) that `pour-fit
  --replace` can't clear — the #34 confusion. It now errors `net is required`. The CLI
  (`pcb pour` / `pour-fit`) already fails fast; this is the connector-side backstop for
  raw `debug.exec_js` / other callers. (Takes effect after re-importing the connector.)
- **`pcb new-board` no longer silently steals the schematic.** A schematic can belong
  to only ONE Board in EasyEDA Pro, so `createBoard(schematicUuid)` on an already-bound
  schematic *moves* it into the new board — leaving the old board with just its PCB
  ("原理图没了"). `board.new_pcb` now detects an already-bound schematic and refuses with
  a clear error naming the owning board; pass `--force` (`force: true`) to move it
  deliberately.
- **`board list` / `pcb board-info` no longer crash on a PCB-only or schematic-only
  Board.** `serializeBoard` read `board.schematic.uuid` / `board.pcb.uuid`
  unconditionally, throwing `Cannot read properties of undefined (reading 'uuid')` for
  any board missing one side (exactly the orphaned boards the old `new-board` produced).
  It now emits `null` for the missing side.

## [0.7.0] - 2026-07-02

The market-ready PCB pass since v0.6.0 — a reconstructed **PCB DFM audit** (`pcb check`),
a full **silkscreen suite**, the verified **4-layer inner-plane** recipe, and a reconnect
UX fix. (Consolidates the dev-loop releases 0.6.1–0.6.7 below.)

### Added
- **`pcb check` — reconstructed DFM audit** (the PCB sibling of `sch check`; catches the
  design-for-manufacture problems the native `pcb drc` clearance check does NOT flag).
  Copper rules compute purely Go-side from placed copper and never mutate:
  **dangling-end** (a track end anchored to nothing → floating copper), **acute-angle**
  (same-net segments bending <90° → acid trap), **non-orthogonal** (a track off the
  0/45/90° grid → free-angle routing), **track-over-pad** (a track crossing a pad it
  doesn't terminate on: cross-net = ERROR short), **overlapping-via** / **single-layer-via**,
  **width-mismatch**, **duplicate-segment**, and **3W parallel-coupling**; plus
  **silkscreen-flipped** (a designator on the wrong silk layer / mirrored / non-upright)
  and per-layer **antenna-keepout** (an antenna module lacking a no-copper keep-out on
  every copper layer). `--strict` exits non-zero on any WARN/ERROR (gate-able).
- **Silkscreen suite** — `pcb silk-add` (a FREE silkscreen string: board credit / LED
  `+`/`−` polarity marks; configurable layer/font/stroke/rotation, JLCPCB-legible
  defaults), `pcb silk-set` (batch-adjust existing silk + an **align-to-reference**
  shortcut: center a board credit, align a label to a component/board/fill edge), and the
  read handlers `pcb.silk.list` (text layer/mirror/reverse/rotation) + `pcb.region.list`
  bbox that feed the DFM checks.
- **`easyeda pcb new-board` (`board.new_pcb`)** — create a brand-new board (板) with a
  fresh EMPTY PCB page bound to a schematic (CLI equivalent of the UI 新建PCB /
  原理图转PCB), then `pcb import-changes` to lay it out from scratch. Distinct from
  `board.create` (link-only). Runs the required 2-step SDK sequence that is otherwise a
  silent no-op — `createBoard(schematicUuid)` mints a board shell, then
  `createPcb(boardName)` adds the PCB INTO it — with shell rollback on failure.
  `--schematic` defaults to the current board's schematic.
- **`easyeda notify` (`system.notify`)** — show a non-blocking toast INSIDE the EasyEDA
  window so the design flow can announce each stage live ("完成 布线,下一步 铺铜").
  `--type info|success|warn|error|question`, `--duration`.

### Changed
- **`pcb silk-align` → position-aware (v2)** — ranks each designator's 4 sides by local
  free space + board position + a crowd-axis bonus, and avoids **other parts' pads**,
  bodies, keep-out regions, the board outline (now resolves rounded/polyline outlines),
  and other labels; keeps assembly clearance around each footprint (`--spacing`); a
  boxed-in part is reported (`unresolved`), never shoved onto a pad.
- **`pcb power-planes` flips the GND inner layer to 内电层/PLANE** after pouring (verified
  pour-while-SIGNAL → flip-type → rebuild recipe, DRC clean), matching the common customer
  stackup GND=内电层 / VCC=信号层. Drove the ESP32 regression board DRC 31→0, No-Connection→0.
- **`pcb auto-place --assembly-gap` (default 40 mil)** floors the chip-to-satellite gap at a
  hand-SOLDER clearance, not just the DRC routing clearance (~28 mil packed too tight to
  reach with an iron). **`pcb check` antenna-keepout now recognizes a single MULTI-layer(12)
  region** as covering every copper layer — one 多层 keep-out replaces the per-layer set.
  design-flow.md PCB pipeline reordered so keep-out regions + silk-align run BEFORE routing
  (post-hoc keep-outs forced re-routing).

### Fixed
- **Reconnect toast dedup** — one toast per daemon outage instead of one on every 3s retry
  (they were stacking and covering UI options during an outage).

### Docs
- README split into a Chinese homepage (`README.md`) + English (`README.en.md`); new demo
  recording storyboard `docs/demo-storyboard-esp32-mini.md`; FEATURES action count 85→88;
  official-marketplace coverage survey (`docs/marketplace-coverage.md`).

## [0.6.7] - 2026-07-02
### Fixed
- **silk-align: labels no longer crowd their OWN pads** — the body used for the offset
  is inflated by an assembly-clearance floor (Cassembly=10 mil) and own-pad overlap now
  carries a penalty, so a designator keeps solder-iron room around its footprint instead
  of touching the copper. New **`spacing`** coefficient (default 1.5, `--spacing`) scales
  the label drift for more/less assembly room; base offset default 12→15; other-pad
  margin Cpad 8→12.
- **`--ref board`/`outline` now resolves rounded/closed outlines** — the board outline is
  often a single `pcb_PrimitivePolyline` on layer 11, which the Line/Arc-only resolver
  missed (silk-set align + silk-align safeArea both use the new shared `boardOutlineIds`).

## [0.6.6] - 2026-07-02
### Changed
- **`pcb.silk.align` is now POSITION-AWARE** (designed via a 3-lens workflow). It ranks
  each designator's 4 sides by local free space + board position (edge parts pulled
  inward, never off-board) + a crowd-axis bonus (dense stacks pushed perpendicular),
  and — the core fix — avoids **other parts' PADS** (a label over exposed copper is
  fab-clipped; this is why C1's designator no longer lands on C2's pad), bodies,
  keep-out regions, the board outline, and other labels. Most-constrained-first order;
  bottom parts → bottom silk + mirror; a boxed-in part is left + reported (unresolved)
  rather than moved onto a pad. New outputs: warned / unresolved.
### Added
- **`pcb.silk.set` gains an ALIGN shortcut** — `align` (center|mid|centerx|centery|
  left|right|top|bottom) + `ref` (a component designator, "board"/"outline", or "fill")
  positions each silk relative to that reference bbox (e.g. center the board credit,
  align a label to a component edge), computed from the silk's own bbox.

## [0.6.5] - 2026-07-02
### Fixed
- **Reconnect toast spam / UI obscuring.** During a daemon outage the connector
  toasted "Daemon not found, retrying (n/5)" on EVERY fast retry (every 3s), so the
  toasts stacked and covered UI options ("one starts before the last ends"). Now it
  toasts **once per outage** (on the first failed scan) and retries **silently** in
  the background; the retry cadence (fast 3s → slow 10s) and reconnect speed are
  unchanged, and the eventual reconnect still announces once.

## [0.6.4] - 2026-07-02
### Added
- **`pcb.silk.add`** — create a FREE silkscreen STRING (board marking / credit / note)
  at (x,y) with config: layer (3=top / 4=bottom), fontSize, lineWidth, rotation.
  Legible JLCPCB-safe defaults (font 40 / stroke 6) — a small font with a thick stroke
  smears the glyphs. Returns primitiveId + rendered bbox.
- **`pcb.silk.set`** — batch-reconfigure existing silkscreen primitives (designator/
  value ATTRIBUTES + free STRINGS): primitiveIds[] + any of x/y/rotation/fontSize/
  lineWidth/text; only the given keys change. Uses the reliable `.modify(id,props)`
  (setState_Rotation alone does NOT persist). The batch position/orientation/size fixer
  behind correcting badly-placed or non-upright silk.

## [0.6.3] - 2026-07-02
### Changed
- **`pcb.region.list`** now emits each region's **`bbox`** (`getPrimitivesBBox`), so
  the daemon's new `pcb check` **antenna-keepout** rule can test whether a no-copper
  keep-out region actually overlaps an antenna module's footprint. Rule types are
  already reported, so the check reads no-copper = any of no-wires/no-fills/no-pours/
  no-inner-electrical.

## [0.6.2] - 2026-07-02
### Changed
- **`pcb.silk.list`** now also emits each text's **`reverse`** (`getState_Reverse` —
  left/right reversed reading) and **`rotation`** (`getState_Rotation`, degrees).
  `getState_Mirror` alone missed real "放反" cases: a designator rotated 180°
  (upside-down) or 90/270° (sideways) has `mirror=false` but doesn't read upright.
  The daemon's `pcb check` **silkscreen-flipped** rule now flags a reference
  designator (`key == "Designator"`) whose orientation isn't upright, and treats
  `mirror OR reverse` as "reads backwards". (`getState_HorizonMirror` does not exist
  on text primitives — confirmed via runtime probe.)

## [0.6.1] - 2026-07-02
### Added
- **`pcb.silk.list`** — read-only enumeration of every SILKSCREEN TEXT primitive:
  component designator/value ATTRIBUTES (`pcb_PrimitiveAttribute`) plus free STRINGS
  (`pcb_PrimitiveString`), each with its silk layer (3=TOP_SILKSCREEN /
  4=BOTTOM_SILKSCREEN), mirror flag, text, position, and (for attributes) the parent
  component's id + side (TOP/BOTTOM). Feeds the daemon's `pcb check`
  **silkscreen-flipped** rule — top silk must read un-mirrored, bottom silk must be
  mirrored, and a designator's silk side must match its component's side; a mismatch
  is a flipped/back-side silkscreen (丝印放反). The PCB component primitive itself has
  no `getState_Mirror`, so orientation is read from the text primitives, not the
  component.

## [0.6.0] - 2026-07-01
> PCB automation milestone (tasks #21–#32). Connector-side changes below; the bulk
> of the release is DAEMON-side (Go CLI) PCB automation, summarized under "Daemon".
### Added
- **`pcb.silk.align`** (task #30) — reposition each component's DESIGNATOR silkscreen
  with COLLISION AVOIDANCE: searches candidate slots around each footprint (preferred
  `side` first, then other directions at increasing distance) and takes the first that
  hits no other component body and no already-placed label — dense-cluster designators
  get pushed into open space instead of piling up. The designator is a component-bound
  attribute (pcb_PrimitiveString is empty), repositioned via
  `pcb_PrimitiveAttribute.getAllPrimitiveId(componentId)` + `.modify(id,{x,y})`.
  Reports `unresolvedCollisions`. CLI: `pcb silk-align`.
- **`pcb.stackup.set`** (task #26) — configure the board stackup: set the copper
  layer count (2/4/6/…/32 via `setTheNumberOfCopperLayers`) and/or set inner layers'
  type SIGNAL↔PLANE (内电层, via `modifyLayer`). A PLANE inner layer gives GND/power
  a dedicated plane on 4+ layer boards — the clean fix for the 2-layer pour conflict
  where two power nets can't both connect on one shared layer. Read via
  `pcb.layers.list`. CLI: `pcb stackup set --layers 4 --plane 15 --plane 16`.

### Fixed
- **Connector auto-reconnect wedge (需要重开窗口才恢复)** — after a daemon restart
  (dev hot-reload) or a long window-backgrounding, `isConnecting` could leak `true`
  and freeze EVERY reconnect path at once: the watchdog tick, the port scan, AND the
  focus/online/visibility wake listeners all early-returned on `isConnecting`, so
  only fully reopening the EasyEDA window recovered. Now (1) the watchdog
  force-resets a connect flow still unsettled after ~24s (`STUCK_CONNECTING_TICKS`),
  and (2) the foreground/online wake forces a clean reconnect *through* a stuck
  `isConnecting` (`cancelConnectionFlow()` first) instead of being blocked by it.

### Daemon (CLI) — PCB automation pass
All real-machine verified on the ESP32 regression board; each `easyeda pcb …` subcommand:
- **Rule-aware** `route-short` / `auto-place` / `pour` — read the board's live DRC rule
  (`pcb drc-rules`) and conform (widths/clearance/via/copper-to-edge) instead of hardcoding;
  fall back to a canonical **JLCPCB fab-rule reference** (real per-board-type exports). (#22/#32)
- `route-short` **v2**: obstacle-aware L-orientation, and **skips power/ground nets** by
  default (they belong in a pour — routing 3V3 as thin tracks was the #1 DRC source). (#23)
- `pcb outline-fit` (tighten to parts) / `pcb outline-round` (rounded-rect outline). (#21/#29)
- `pcb layout-lint` — placement quality + **routability score** (ratsnest MST + crossings). (#25)
- `pcb power-planes` — **4-layer** power distribution: GND + power on dedicated inner planes
  + via-stitch each pad (drove the regression board's No-Connection to 0). (#26)
- `pcb region` / `fill` / `slot` — antenna keep-out (禁铺铜) & board cutout (挖槽). (#28)
- Confirmed platform walls (no `eda.*` API): teardrops, controlled-impedance, interactive routing.

## [0.5.30] - 2026-06-30
### Added
- **`pcb.add_component`** (task #20) — add ONE part to an EXISTING PCB and wire it,
  the working alternative to `pcb.import_changes` (which is a no-op for API-added
  parts). Places the footprint (`pcb_PrimitiveComponent.create`), links it to its
  schematic twin (uniqueId + designator), assigns each pad's net from a caller-
  supplied `nets` map (`pcb_PrimitivePad.modify` — the step that actually wires it,
  since net→pad assignment is otherwise part of the broken import flow), and
  recomputes ratlines. CLI: `easyeda pcb add-component`. `schematic.read` now also
  returns each component's `uniqueId` (the sch↔PCB link key to pass in).
### Investigated
- `eda.pcb_Document.importChanges` does NOT sync API-added components to an existing
  PCB (returns true, count unchanged) — root-caused to incremental-add being a
  platform no-op; superseded by `pcb.add_component`.

## [0.5.29] - 2026-06-30
### Added
- **One-call circuit snapshot** (task #7): `schematic.read` returns a coherent
  semantic model in a single round-trip — components (each pin tagged with its
  JSON-authoritative net from `getNetlistFile`), nets (net → connected pins +
  degree + power/ground flag), floating pins, and the geometric design check
  (`includeCheck:false` to skip). Replaces the agent stitching `components.list` +
  `netlist` + `check`. CLI: `easyeda sch read` (`--all-pages`, `--no-check`).

## [0.5.28] - 2026-06-30
### Fixed
- **Auto-reconnect no longer needs a window "nudge".** The heartbeat/reconnect loop
  ran on a main-thread `setInterval`, which EasyEDA's webview freezes when the
  window is backgrounded — so after a daemon restart (e.g. `make dev` rebuild) the
  connector stayed dead until the user focused the window. A new **watchdog** drives
  both the heartbeat and reconnect from a **Web Worker** timer (which keeps firing
  while backgrounded); it falls back to a main-thread interval + `focus`/`online`
  listeners if the webview blocks workers. An explicit Stop now sets a `suspended`
  flag so the always-on watchdog doesn't reconnect behind the user's back.

## [0.5.27] - 2026-06-30
### Added
- **Net-bound filled region** (task #17): `pcb.fill.create` / `pcb.fill.list` /
  `pcb.fill.delete` (`eda.pcb_PrimitiveFill.*`) — a STATIC filled polygon bound to a
  net (3V3/RF-ground patch, thermal copper, odd-shaped plane). `fillMode = solid
  (default) | mesh | inner`. Distinct from `pcb.pour.create` (覆铜, reflows around
  obstacles) and `pcb.region.create` (keep-out, no net). CLI: `easyeda pcb fill
  create / list / delete`.

## [0.5.26] - 2026-06-30
### Added
- **DSN keep-out injection** (task #17): `pcb.export.dsn` now splices keep-out
  regions (禁止区域) back into the exported DSN by default — `getDsnFile` DROPS
  `pcb_PrimitiveRegion`, so a raw export had `keepout = 0` and Freerouting would
  route under the antenna. Each routing region (no-wires/no-fills/no-pours) becomes
  a Specctra `(keepout (polygon …))` in the `(structure)` section. Transform is a
  verified pure translation (1:1 mil, no flip; offset = DSN-boundary-min −
  outline-bbox-min). Result reports `keepouts = N`. CLI `easyeda pcb export-dsn`
  gains `--raw` for the unmodified export.

## [0.5.25] - 2026-06-30
### Added
- **PCB keep-out / rule regions** (task #11): `pcb.region.create` / `pcb.region.list`
  / `pcb.region.delete` (`eda.pcb_PrimitiveRegion.*`). A polygon carrying rule types
  — `no-components(2)` / `no-wires(5)` / `no-fills(6)` / `no-pours(7)` /
  `no-inner-electrical(8)` / `follow-rule(9)`; default is a hard keep-out
  `[no-components, no-wires, no-pours]` for antenna clearance / board-edge inset.
  NOT net-bound filled copper (that's `pcb.pour.create`). CLI: `easyeda pcb region
  create / list / delete`. (DSN keep-out injection for the Freerouting maze tier is a
  separate follow-up — `getDsnFile` drops regions.)

## [0.5.24] - 2026-06-29
### Added
- **Freerouting round-trip building blocks** (task #5): `pcb.export.dsn`
  (`getDsnFile` → Specctra DSN artifact, the autorouter input), `pcb.import_autoroute`
  (`importAutoRouteSesFile`/`importAutoRouteJsonFile`, base64 in, recomputes ratlines),
  and `pcb.snapshot` (`getCurrentRenderedAreaImage` for the PCB canvas — the PCB
  counterpart to `schematic.snapshot`). Enables the file-based autoroute workflow
  `pcb export-dsn` → run Freerouting → `pcb import-autoroute route.ses` without the
  @alpha `autoRouting()`. CLI: `easyeda pcb export-dsn / import-autoroute / snapshot`.

## [0.5.23] - 2026-06-29
### Added
- `schematic.check` now reports **stray wires** the SDK DRC and layout-lint both
  miss: `dangling-wire` (a segment whose vertices touch no pin, net-flag/port/label,
  or other wire — e.g. a stub left behind when its pin/flag was deleted) and
  `zero-length-wire`. Each finding carries the `wirePrimitiveId` so it can be
  removed with `sch prim-delete`. Summary gains `zeroLengthWires` / `danglingWires`.

## [0.5.22] - 2026-06-29
### Fixed
- Net-flag/net-port **vertical (up/down) body orientation** on the y-DOWN build:
  `connect_pin --direction down` ground (and `--direction up` power) flags rendered
  their body toward the pin instead of away. Root cause was the orientation table's
  up/down entries being derived in a y-UP frame; `ROTATION_CYCLE` is now
  `up→right→down→left` with power/ground anchors swapped (left/right unchanged).
  Verified via `getPrimitivesBBox` on real placed flags + `calibrate.js` (whose own
  y-frame was fixed). See `orientation.json` _doc.

## [0.5.20] - 2026-06-29
### Fixed
- `schematic.drc.check` now treats boolean SDK results as first-class normalized
  output instead of assuming the verbose overload always returns an array. This
  matches current EasyEDA runtime behavior for `SCH_Drc.check`.
- `schematic.check` now reconstructs additional UI-like warnings for schematic
  validation: net-marker/wire-name mismatches and multi-net wires.
- Floating-pin detection now cross-checks the official manufacture netlist JSON
  (`sch_ManufactureData.getNetlistFile`) before reporting a pin as floating.
- Net-marker checks now dedupe repeated wire/marker segment matches and only treat
  a marker as attached when it touches a wire vertex, reducing false positives from
  malformed merged polylines.

### Changed
- CLI and skill docs now distinguish the official SDK DRC gate (`sch drc`) from
  the reconstructed per-item checker (`sch check`).

## [0.5.18] - 2026-06-28
### Added
- **PCB routing roadmap R1 (copper pour) + R2 (rip-up/list)** from
  `docs/ecosystem-survey.md §7` — 8 new actions, d.ts-grounded + adversarially reviewed:
  - `pcb.pour.create` / `pcb.pour.list` / `pcb.pour.delete` / `pcb.pour.rebuild` —
    **copper pour (铺铜)**. create takes raw `points`; the connector builds the
    `IPCB_Polygon` via `pcb_MathPolygon.createPolygon` (the missing piece behind the old
    "无法创建覆铜边框图元" failures — you must pass a polygon object, not raw points),
    then `rebuildCopperRegion()` computes the fill. `fill = solid|grid|grid45`. CLI
    `easyeda pcb pour / pour-list / pour-delete / pour-rebuild`.
  - `pcb.route.rip_up` — **reliable rip-up** (getAll → filter → delete on stable
    primitive APIs, the official kirouting pattern). Deletes tracks+arcs+vias on
    **copper layers only** (TOP/BOTTOM/INNER) — never the board outline,
    silkscreen/assembly/mechanical artwork, or **locked** primitives. `--net` scopes;
    omit = all. CLI `easyeda pcb rip-up`.
  - `pcb.line.list` / `pcb.via.list` — read routed tracks/vias. CLI `pcb track-list` /
    `pcb via-list`.
  - `pcb.clear_routing` — wraps native `clearRouting` (`@alpha`, may be undefined;
    prefer `pcb.route.rip_up`). CLI `easyeda pcb clear-routing`.
  - Smart/interactive routing (single/multi/diff routing, stretch, optimize,
    length-tuning, fanout) has NO `eda.*` API — documented as a hard boundary (§7).
- **Five actions absorbed from the official open-source extension ecosystem**
  (see `docs/ecosystem-survey.md`), each grounded in `pro-api-types` signatures:
  - `schematic.library.get_by_lcsc` — resolve LCSC C-numbers directly to
    `{libraryUuid, uuid}` via `eda.lib_Device.getByLcscIds` (deterministic, no
    free-text ranking; reports `notFound`). CLI `easyeda lib by-lcsc --lcsc C…`.
  - `pcb.line.create` — create a copper track via `eda.pcb_PrimitiveLine.create`
    (mutating). CLI `easyeda pcb track`.
  - `pcb.via.create` — place a via via `eda.pcb_PrimitiveVia.create` (mutating).
    CLI `easyeda pcb via`.
  - `pcb.report` — read-only design report (per-net length, net-class totals,
    differential-pair skew, equal-length spread) over `eda.pcb_Net.getNetLength` +
    `eda.pcb_Drc.getAll{NetClasses,DifferentialPairs,EqualLengthNetGroups}`. CLI
    `easyeda pcb report`.
  - `pcb.drc.rules` — read `eda.pcb_Drc.getCurrentRuleConfiguration` without
    running a check. CLI `easyeda pcb drc-rules`.
  - **Live-verified on a real board (PCB1, connector 0.5.15):** A1 resolves
    C6186→AMS1117-3.3 identity; A5 returns the full rule config; A3 reports 4 nets
    with length/net-class/diff/equal-length; A2 creates a GND track (net length
    read back 0→500, confirming it bound to the right net); `pcb drc` + save pass.
- **`pcb.save` — save the active PCB to disk** (`eda.pcb_Document.save`), the PCB
  counterpart to `schematic.save`. CLI `easyeda pcb save`. **PCB autosave is now
  on:** the daemon's debounced autosave fires `pcb.save` after a PCB-mutating
  action, closing the in-memory-edit data-loss gap that previously only schematic
  edits were protected from (`saveActionForDocType` now maps `pcb`→`pcb.save`).

### Fixed
- **`pcb.outline.set` now creates the REAL board-outline object (类型=板框), not loose
  lines.** Root cause of "the outline vanished when I cleared routing" + "DRC doesn't
  flag out-of-board": the outline was drawn as N separate `pcb_PrimitiveLine`s on
  layer 11. A loose line on the board-outline layer is just a wire that happens to sit
  there — EasyEDA does NOT treat it as the board boundary (DRC ignores it for
  enclosure, the UI "清除布线 / clear routing" deletes it). Compared a UI-drawn 板框
  against ours: the real outline is ONE `pcb_PrimitivePolyline` whose `polygon` is an
  `IPCB_Polygon`. Fix: build the closed-polygon source `[x0,y0,'L',…,x0,y0]` →
  `eda.pcb_MathPolygon.createPolygon` → `eda.pcb_PrimitivePolyline.create('', 11,
  polygon, lineWidth, /*lock*/true)` — one locked polyline. `pcb.outline.get/clear`
  updated to read/delete the polyline (bbox from its rendered extent; legacy lines
  still handled). Default lineWidth 10mil. Returns `outlineId`. Create flow verified
  live (createPolygon + polyline produced a 类型=板框 object matching the UI's).
- **`view region` + `schematic.snapshot --no-fit` now reliably captures the
  requested local region (issue #20).** Three coordinated fixes: (1) the snapshot
  handler now waits for the canvas to repaint (two `requestAnimationFrame`s with a
  timeout fallback) BEFORE reading the frame, so a preceding `view region` viewport
  has actually landed — previously `--no-fit` grabbed the pre-region frame because
  EasyEDA does not synchronously repaint after `eda.*` view calls (the `--fit` path
  only "worked" by accident, since `zoomToAllPrimitives` nudged a redraw). (2)
  Built-in stale-frame detection: the snapshot result now exposes the frame
  `sha256`; thread it back via `sch snapshot --previous-sha256 <sha>` and the
  connector detects a byte-identical (stale) frame, retries once after another
  redraw, and reports `stale`/`staleRetry`. (3) `view.region` now normalizes the
  rectangle (sorts each axis to min/max) and rejects a zero-area box, so a
  reversed/degenerate bound no longer renders as a tiny sliver in a blank frame;
  `view region` CLI help documents the y-DOWN schematic axis semantics and units.
- **`schematic.power.connect_pin` (`sch connect`) `--direction up/down` no longer
  inverts the stub/netport endpoint.** EasyEDA Pro schematic coords are y-DOWN (a
  larger stored y renders LOWER on screen, verified on 3.2.121, issue #19), but the
  endpoint math assumed y-UP, so `--direction up` pushed a top-pin stub DOWN into the
  IC body and `--direction down` pushed a bottom-pin stub UP — visually wrong even
  when DRC was clean. `up` now decreases y (visually higher) and `down` increases y
  (visually lower). The flag-rotation table is unchanged: it is calibrated against
  real rendered bbox and already keyed to visual directions, so the corrected
  endpoint and the flag orientation now agree (callers no longer need the
  `--direction down --rotation 90` workaround to get a visually-upward netport).
- **`schematic.check` no longer false-flags merged-stub endpoints as `wire-over-pin`,
  and floating-pin findings now carry component-level detail.** A pin coincident with
  a wire endpoint or a netflag/netport/netlabel anchor is the legitimate terminus of
  its own `sch connect` stub; when EasyEDA auto-merges collinear touching stubs into
  one long wire an inner pin lands in that wire's interior and was wrongly reported as
  a through-pin short (the official DRC stays clean). Rule 3 now excludes pins that
  coincide with a wire vertex or a net-marker anchor. Floating-pin findings now include
  `primitiveId` and a `pinDetails[]` array (`number`, `name`, `x`, `y`) so the `--json`
  report identifies the component and pin without a second lookup; the text report
  prints the per-pin name + coordinates and falls back to `primitiveId` when the
  designator is empty.

## [0.5.14] - 2026-06-28
### Fixed
- **`schematic.pin.set_no_connect` no longer reports a false success.** On EasyEDA
  Pro 3.2.x, `pin.setState_NoConnected` is a **no-op** — the pin primitive has no
  `noConnected` field (verified by re-pull, DRC re-run, and a canvas snapshot: no
  非连接标识 is ever placed and DRC still treats the pin as floating). The setter is
  typed `@public`, so the prior implementation compiled and returned `ok` while
  silently doing nothing. The handler now **verifies** the write and fails with
  `EDA_CALL_FAILED`, naming it as an EasyEDA platform limitation (not a connector
  defect) and returning `notApplied[]`. It auto-passes if a future build makes the
  setter real. There is no public `eda.*` API to place a 非连接标识 on this version —
  use `schematic.check` to enumerate floating pins.
- **`schematic.wire.create` now normalizes nested `points` (issue #5).** EDA's
  `eda.sch_PrimitiveWire.create` only accepts a **flat** `number[]`
  (`[x1,y1,x2,y2,…]`); a nested `[[x,y],…]` payload failed with
  `EDA_CALL_FAILED / "create failed!"`. The connector now flattens nested points
  at a single source of truth (`normalizeWirePoints` in `util.ts`), so CLI /
  `call` / sch.py / `debug.exec_js` all accept either form. Also validates the
  list is an even-length (`≥4`) run of finite numbers. CLI `sch wire --help` and
  `auto-layout-sop.md` updated to document both forms.

### Added
- **`schematic.check` — reconstructed per-item design check + routing-quality
  rules.** The EDA schematic DRC API (`eda.sch_Drc.check`) returns only an aggregate
  `{count,type}` and `layout-lint` only sees component bbox overlap; this fills both
  gaps by computing findings geometrically from primitives. Rules: (1) **floating
  pins** — a pin is connected iff a wire touches its coordinate (NC-marked excluded),
  grouped by component as `{designator, pins[]}` (the exact input
  `schematic.pin.set_no_connect` takes); (2) **wire-crossing** — two wire segments
  cross in their interiors (a routing tangle; shared endpoints/junctions excluded),
  reported with the intersection point; (3) **wire-over-pin** — a pin sits in a
  wire's interior (EasyEDA trims+connects there → unintended short; enforces the SOP
  "chain pin→pin, don't run a wire through a pin"). Returns `{passed,
  summary{floatingPins,wireCrossings,wireOverPins,…}, findings[]}`. CLI:
  `easyeda sch check` (`--json`, `--strict`, `--all-pages`). Verified live via a
  detect→fix→re-check loop on an ESP32-S3: 2 wire-crossings found and driven to 0.
- **`schematic.drc.check` now returns per-violation detail.** Normalizes the SDK
  result into `{passed, fatal, summary, violations[]}` — each violation projects
  `{level, rule, message, primitiveIds, designators, x, y}` (raw kept) plus a
  severity summary and a `fatal` count for the design-flow S5 gate. CLI `sch drc`
  prints one line per violation and exits non-zero only when `fatal > 0`. NOTE: the
  schematic SDK only provides an aggregate, so detail degrades honestly to
  "N issue(s) — EDA returned no per-item detail" (use `schematic.check` for the
  itemized floating-pin findings).
- **`schematic.snapshot` anti-stale metadata (issue #2).** The snapshot result now
  carries `primitiveCount` (live components + page primitives on the current page),
  `capturedAt` (ISO timestamp), and a `stale` advisory string. EasyEDA does not
  auto-redraw after `eda.*` edits, so `getCurrentRenderedAreaImage` can return a
  byte-identical STALE frame; callers compare `primitiveCount` across two snapshots
  to detect when the image didn't change but the page did. Judge state by data, use
  the screenshot for layout only.
- **`schematic.page.clear` — one-shot page reset.** Deletes every page-level
  primitive on the active page (components, net flags/ports/labels, wires, buses,
  and graphics — arcs/circles/rectangles/polygons/text), not just components.
  `preserveSheet` (default true) keeps the sheet/title block; `dryRun` reports
  per-type counts without deleting. Returns `{deleted:{...}, total, deletedIds}`.
  Fixes the trap where `schematic.component.delete` left wires/buses behind while
  `components.list` reported a clean page, forcing a fall back to raw
  `debug.exec_js`.
- **`schematic.primitives.delete` — generalized, any-type delete.** Routes each
  requested id to its owning `sch_Primitive*` class so wires/buses/graphics/flags
  can be deleted alongside components; omit `primitiveIds` to delete the current
  selection (select-all → delete). Reports `notFound` ids.

## [0.5.8] - 2026-06-27
### Changed
- **Version bump to pair with the daemon/CLI artifact-path change.** No connector
  behavior change vs 0.5.7. The CLI now sends its working directory and the daemon
  writes artifacts (snapshots, netlist/BOM exports) under `<cwd>/.easyeda/artifacts`
  with sortable timestamped names (`<YYYYMMDD-HHMMSS>-<kind>-<short>.ext`) instead
  of a flat `artifacts/art_<uuid>` in the daemon's cwd. Released together to keep
  CLI and connector on the same version.

## [0.5.7] - 2026-06-27
### Added
- **Heartbeat-carried context.** The connector now re-reads the active
  project/document on each heartbeat (~3s) and pushes it to the daemon only when
  it changed. `easyeda daemon health` (and project routing) now reflect a UI
  tab-switch within one interval — previously context refreshed only on connect
  or as a side effect of running an action, so it lagged the UI until the next
  command. The initial post-connect push is unconditional; reconnects reset the
  change-detection signature so they always re-push.

## [0.5.6] - 2026-06-27
### Changed
- **Rebuild to pair with the daemon's live-context + `doc` work.** No connector
  behavior change vs 0.5.5 — this build exists so a window stuck on a stale
  connector can be re-imported to pick up real version reporting + port-scan
  (49620–49629). The daemon now refreshes each window's context from every action
  response (so `health` no longer reads `home` forever) and `easyeda doc ls/switch`
  drives the discover→switch loop on top of the existing `document.open` action.

## [0.5.5] - 2026-06-27
### Fixed
- **Handshake reports the real connector version.** `connectorVersion` was a
  hardcoded `0.1.0`, so `easyeda daemon health` could not reveal which build a
  window was actually running — useless for spotting a stale open window. esbuild
  now injects `extension.json`'s version at build time (`__CONNECTOR_VERSION__`).

## [0.5.4] - 2026-06-27
### Added
- **Board (板子/组合) management** — `board.list`, `board.current`, `board.create`,
  `board.rename`, `board.copy`, `board.delete`. A Board binds one schematic + one
  PCB; these expose `eda.dmt_Board.*` so the schematic↔PCB grouping is editable
  (and a floating PCB can be linked before `import_changes`).

## [0.5.3] - 2026-06-27
### Added
- **Schematic page management** — `schematic.page.create`, `schematic.page.rename`,
  `schematic.page.delete`, `schematic.rename` (`eda.dmt_Schematic.*`).
- **明细表 (title block)** — `schematic.titleblock.get` / `schematic.titleblock.modify`
  to read and adjust the drawing-sheet title block (the editable "图纸" surface;
  EasyEDA Pro exposes no set-paper-size API).

## [0.5.2] - 2026-06-27
### Added
- **Editor view shortcuts** — `view.fit` (适应全部 / `K`), `view.fit_selection`
  (适应选中), `view.zoom`, `view.region` via `eda.dmt_EditorControl.*`; act on the
  focused canvas, shared by schematic and PCB.

## [0.5.1] - 2026-06-26
### Added
- **PCB layout intelligence** — `pcb.components.arrange` (cluster / grid auto-layout
  seed) and rendered bounding boxes in `pcb.components.list`.
- **PCB layout adjustment** — `pcb.align`, `pcb.distribute`, `pcb.grid_snap`,
  `pcb.components.move`.
- **Board outline (板框)** — `pcb.outline.set` / `pcb.outline.get` / `pcb.outline.clear`.
- **PCB DRC** — `pcb.drc.check`, normalized to `{passed, violations}`.

## [0.4.10] - 2026-06-26
### Added
- `homepage` pointing at the GitHub repository (open-source link for the listing),
  as a plain URL (no `#readme` fragment).

## [0.4.9] - 2026-06-26
### Fixed
- Marketplace manifest finalized: `repository.type` is `github` (per the official
  `eext-extension-demo`); removed the optional `bugs`/`homepage` fields — the
  marketplace flagged the `bugs` content and neither field is required. No email
  or other private data ships in the `.eext`.

## [0.4.5] - 2026-06-26
### Added
- `repository` field in the manifest and this `CHANGELOG.md` (marketplace
  submission requirements).

## [0.4.4] - 2026-06-26
### Changed
- Release tooling keeps a **stable UUID** by default, so a new version updates in
  place (uninstall the old entry, then import); a fresh-UUID build is now an
  explicit fallback. No change to the connector's runtime behaviour.

## [0.4.2] - 2026-06-26
### Fixed
- **Self-healing reconnection.** The connector no longer permanently gives up
  after a few failed retries. After the initial fast attempts it falls back to a
  quiet background poll, so a daemon that is started or restarted *after* the
  editor auto-connects with no manual **Reconnect**. A connection lost to a daemon
  restart also recovers on its own.

## [0.4.0] - 2026-06-26
### Fixed
- `.eext` packaging so the extension installs reliably. Bundled a JPEG logo.

## [0.3.0] - 2026-06-26
### Fixed
- Netflag / netport **orientation**: corrected for EasyEDA's y-up coordinate
  system and fixed rotation handling in `connect_pin` (reverted a wrong rotation
  negation).

## [0.2.0] - 2026-06-25
### Added
- Initial connector: a WebSocket bridge (port-scans 49620–49629) to the
  easyeda-agent Go daemon, dispatching typed schematic actions to the official
  `eda.*` API, with auto-reconnect and a heartbeat.
- `connect_pin` composite action and the netflag/netport orientation convention.
- Header menu: **Reconnect**, **Stop**, **Toggle Auto-Connect**, **About**.
