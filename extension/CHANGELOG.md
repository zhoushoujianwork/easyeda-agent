# Changelog

## [1.4.1] — 2026-09-05

### Fixed

- Reject placed-instance UUIDs before schematic materialization so an invalid
  16-character ID cannot hang the EasyEDA connector queue (#196).
- Preserve Blob, ArrayBuffer, and typed-array results from `debug.exec_js` as
  explicit base64 envelopes, keeping Gerber/ZIP bytes intact (#195).
- Run external PCB autorouters through the native Windows shell on Windows,
  preserving DSN/SES paths, and enforce a bounded router timeout (#194).
- Add the 1.4 schematic connectivity snapshot/diff/plan/apply/materialize
  baseline with page-scoped reads and guarded write verification.

## [1.3.0] — 2026-09-01

- Add typed `library.model3d.search` and `library.model3d.copy` actions for selecting existing library models without UI automation.
- Add `library.device.set_model3d` with exact Device-name preflight, model existence checks, bind/replace/clear support, and association readback verification.
- Expose the workflow through `easyeda lib model3d search/copy` and `easyeda lib device model3d`.

## [1.2.14] — 2026-09-01

- Add `circles[]` to typed symbol authoring for pin-1 and polarity markers.
- Correct the SD-card example to use inward-facing pin labels and a pin-1 ring.
- Add one-shot CLI `lib device build --spec`: create and bind Symbol + Footprint + optional 3D, with reverse-order rollback on failure.

## [1.2.13] — 2026-09-01

- Add typed symbol geometry authoring (`library.symbol.build`) with validation and readback.
- Add typed 3D model import/read/delete backed by the official `lib_3DModel` API.
- Allow CLI Device creation to bind Symbol + Footprint + 3D Model.

All notable changes to the **EDA Agent Connector** (the easyeda-agent project's connector extension) are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/); versions
follow [SemVer](https://semver.org/).

## [1.2.12] — 2026-09-01

### Added — 可复用库命名与无损封装复制

- 新建 Symbol、Footprint、Device 统一使用跨项目 `EA_AGENT__<ASSET>` 命名。
- Device 搜索支持指定 `libraryUuid`，可精确限定个人库。
- 新增 `library.footprint.copy` / `lib footprint copy`，通过官方库 API 无损复制
  复杂封装并立即回读验证；适合含弧线、区域、文字和机械细节的卡座类封装。
- 新增 Symbol、Footprint、Device 的名称防误删 typed delete，并适配库删除后的缓存行为。

## [1.2.11] — 2026-08-31

### Added — 单器件库封装创作

- 新增个人库、工程库与系统库标识查询。
- 新增封装、符号和 Device 的创建与读取命令，Device 可显式绑定符号、封装和
  3D 模型引用。
- 新增 JSON 驱动的封装构建：一次创建焊盘与图形线，保存后逐图元回读验证；
  中途失败会返回结构化 partial/rollback 结果，防止盲目重试。
- 新建封装、符号和 Device 自动使用跨项目可复用的 `EA_AGENT__<资产>` 命名，和
  用户原有库资产隔离；项目来源放扩展属性而不是名字。新增三类资产的安全删除。
- 封装丝印使用编辑器实际支持的 Polygon + Polyline 图元路径；已在 EasyEDA Pro
  3.2.186 中以 R0603（2 焊盘、4 条丝印）热加载验收通过。

## [1.2.10] — 2026-08-27

### Fixed — README 演示图改回包内相对路径(1.2.9 换版本号重传未解决,定位到真因)

1.2.9 曾假设「市场服务端处理 README 偶发失败,纯换版本号重传即可」——**这个
诊断错了**。1.2.9 重传后逐字节 diff 市场存的 readme 字段与本地文件,发现除
三张图片行被整行删除外**其余文字一字不差**,不是粗暴的解析失败,是精确命中
这三行。扫了市场上 100+ 其它插件的 README(同款 `image.lceda.cn` 图床、同款
排版),`image.lceda.cn` 绝对链接普遍工作正常,排除了「服务端偶发抽风」。

真因转向:README 里硬编码的三个 `image.lceda.cn` URL 不是本次上传产生的
——是半年前 0.23.0 那次转存时铸造的老链接,此后一直复用到 1.2.9。怀疑市场
对图片 URL 做了版本归属绑定,渲染当前版本时把「挂在旧版本记录下」的图片
引用当滤除对象。改回包内相对路径 `images/demo-*.png|gif`,让市场重新执行
「README 抽图 → 转存图床 → 改写链接」流程,为当前版本铸造全新 URL。

> 与 0.24.0 那次「相对路径抽图失败」的教训不冲突:那次是抽图**步骤本身**
> 出错(readme 字段整体缺失);这次绝对链接**能正常返回图片**,只是被
> 精确摘除三行,指向的是版本归属过滤而非抽图失败。若这次相对路径重新
> 抽图又失败,再退回绝对链接但换成全新图床 URL(而非复用旧链接)。

## [1.2.9] — 2026-08-27

### Fixed — README 重新提交(诊断有误,见 1.2.10 订正)

无代码/行为变更,纯 bump 版本号重传。原以为是市场服务端处理 `.eext` 时
偶发失败——**订正见 1.2.10**:实际是精确摘除三张图片行,不是偶发解析
失败。

## [1.2.8] — 2026-08-27

### Fixed — 旋转探测旗删不掉就漏在画布上,拖垮 `sch gate --strict`

`detectRotationNegation()` 为测平台的旋转语义(createNetFlag 在某些 build 上**存储
取负**)会在离画布极远处造一支一次性探测旗 `__ROTPROBE__`,读回 rotation 再删掉。

那句 `delete` **没有回读验证** —— 而「删除撒谎」是本仓在别处早已按批处理 + 回读
兜住的平台已知病(delete 返回真值而图元仍在);delete 抛错还会被 `catch` 吞掉。
一旦撒谎,探测旗就永久留在画布上。

代价不在电路,在**判据**:`sch bridge-check` 把它算成一条 orphan-flag,于是
`sch gate --strict`(S5 逐页门用的就是它)在一块电路完全正确的板子上 FAIL
(2026-08-26 esp32MiniRequire 端到端实测,POWER 页)。

现在 delete → 回读 → 重试(至多 3 轮),仍在就经 `eda.sys_Log` 明着记一条并附上
清理命令 —— 沙箱里 `console` 是死代码,sys_Log 是唯一诊断出口。

> daemon 侧另有一层收口(`sch_tool_probe_residue.go`):按网名把探测残留归类成
> 「工具自己的垃圾」,不计进板子的 orphan 账但照常报出 + 给清理命令。那一层对
> **已经装好的**旧连接器立即生效;这一层才是治本。

## [1.2.7] — 2026-08-26

### Fixed — 图签**其实一直能写**:是回读太早把成功报成了失败(#186)

承接 1.2.6。修完损毁问题后实测「文本项写不进去」,一度以为是平台限制 —— **错了**。

真机复验:把 `Name` 写成 `"TB-BOOL-TEST"` 的调用回执报
`nothing was applied: Name`(硬失败),**三秒后再读,值好端端在那儿**;单写
`Drawed` 同样如此。也就是说**平台提交明细表是异步的,写完立刻回读拿到的是旧值**,
handler 把一次成功的写判成了彻底失败。

这条误报的代价远超它自己:它让「图签写不进去」变成流程里的既定结论 ——
design-flow 因此禁用图签写入、`sch gate --strict` 的 `missing-titleblock`
被认定为结构性不可达。**实际上那条路是通的。**

修法:回读改成轮询到落定(命中即返回,常见路径零额外延迟;最多 4 次 × 250ms 退避)。
仍对不上才判 `notApplied` —— 那时它是真的没生效,不能拿等待去粉饰。

两个新单测钉正反两面:慢落定的写不再被误报(且**只写一次** —— 重试的是读不是写);
始终不生效的项轮询完仍如实报失败。

## [1.2.6] — 2026-08-26

### Fixed — 明细表写入不再损毁图框(#186,社区报告 + 真机复现)

`schematic.titleblock.modify` 把调用方给的 `titleBlockData` **原样**下发给平台。
于是最自然的用法——`titleblock.get` 拿完整数据 → 只改一个文本项 → 整包传回——
会把 `Device`/`Symbol` 的 value(`"Drawing-Symbol_A4"`,那是**符号的名字**)一起写下去,
平台把它灌进了 sheet 的 **component/device/symbol UUID 引用位**:

```
修改前  component.uuid = 2cdbe1a3210dccf2   device.uuid = 2cdbe1a3210dccf2   symbol.uuid = bffa68140727fa20
修改后  component.uuid = Drawing-Symbol_A4  device.uuid = Drawing-Symbol_A4  symbol.uuid = Drawing-Symbol_A4
        Border 1→0    Title Block 1→0
```

EasyEDA 当场报「发现异常数据,以下元件的 器件/符号 属性有误」,保存后**重启即拒载 = 图框丢失**。
而动作本身还返回 `ok:false`(「nothing was applied: Name」)—— **报失败、画布却已被写坏**,
是最坏的一种组合。这与「库占位 Designator 被灌进 otherProperty」是同一类事故:
**读回来的投影字段不许原样写回去**。

修法:下发前按结构键过滤。

- **黑名单而非白名单**:明细项的键名**由图框模板决定**(默认 A4 是 `Name`/`Drawed`,
  另一些模板是 `Title`/`Designer`),白名单会把自定义图框的合法字段全拒掉;
  而结构键是图框**数据模型**的固有部分,与模板无关,可以稳定枚举 ——
  图框身份(`Device`/`Symbol`/`ID`)、纸张几何(`Size`/`Page Size`/`Width`/`Height`/
  `Blade Width`/`Region Start`/`X·Y Region Count`/`Title Block Position`)、
  开关(`Border`/`Title Block`/`Color`),外加 `@` 前缀的平台投影项。
- **原样带回来的结构键静默丢弃**(值与画布一致 = 调用方并不想改它),回执给 `ignoredKeys`;
- **真想改结构键则零变异拒绝**(`PRECONDITION_REFUSED`),报文点名字段并给出正路
  (换图框走 `prim-delete --allow-sheet` + `place`)。审计日志里那次「拿明细表当纸张属性写」
  (`Size`/`Width`)的真实失败,现在**在下发之前**就被拦住,而不是写下去再报「没生效」。

两个新单测钉住:结构键拒绝时**一次平台调用都不许发出**;报告人的整包回传用法
下发 payload 里**一个结构键都没有**、只有那个真正要改的文本项。

## [1.2.5] — 2026-08-26

### Fixed — 「用户参数写错」不再把连接器染成 DEGRADED

新增错误码 **`PRECONDITION_REFUSED`**:handler 在**动手之前**拒绝、**画布一个字节都没改**
的那类失败。daemon 的写健康度(`writehealth.go`)**完全不采样**这个码。

起因是 1.2.4 的差分对命令加了三处前置校验(网名不在板上 / 同名约束内容不同 / 正负网填成
同一条)。真机连打几次错网名之后,`daemon health` 就把连接器判成 **DEGRADED** 并点名
`pcb.differential_pair.create` 是「最差路」—— 而连接器与平台全程健康。根因:健康度的
`failed()` 判据是「`ok:false` 且未证实落地 → 算失败」,它分不清**「这条路不通」**和
**「你给的参数不对」**。

误报的代价不是难看,是**把真信号淹掉**:同一个 `failureRate` 里既有「socket 死了、
register 被静默忽略」(issue #185 那类真停摆),也有「网名打错了」。

**豁免必须窄**,只收三个码:`PRECONDITION_REFUSED` / `MISSING_PAYLOAD_FIELD` /
`UNKNOWN_ACTION`。**`INVALID_STATE` 故意不在其列** —— 它既可能是「你要的事讲不通」,
也可能是「编辑器状态真的坏了」,一刀切会把真故障一起吞掉;要豁免的 handler 应显式改用
新码,而不是放宽白名单。使用新码的前提是**零变异**:已经写了东西就不许再用它
(该走 partial / 结构化成功,#151)。

差分对与等长组的 8 处前置拒绝已全部迁移到新码。四个 Go 单测钉住:拒绝零采样、
真故障码(含 `INVALID_STATE`、空码)照常计入、拒绝穿插在真失败之间不稀释失败率。

## [1.2.4] — 2026-08-26

### Added — 差分对 / 等长网络组终于能建了(#176)

`pcb report` 一直能**测**差分对 skew 与等长组 spread,但没有任何路径能**建**这两类约束
——于是纯 CLI 驱动的板子上,那两个数组永远是空的,测量能力等于空转。平台其实早就给了
全套 `@beta` API(真机 probe:create/delete/getAll 都返回 true 且回读得到),是我们没做。

新 7 个 typed action + 两组 Cobra 子命令:

```
easyeda pcb diff-pair create --name USB0 --positive USB_DP --negative USB_DM
easyeda pcb diff-pair list | rename --name USB0 --to USB | delete --name USB
easyeda pcb eq-group  create --name DDR_ADDR --nets A0,A1,A2
easyeda pcb eq-group  list | add --name DDR_ADDR --nets A3,A4 | delete --name DDR_ADDR
```

每个 mutation 都按本仓库的规矩来:

- **前置校验网名**:约束指向一个板上不存在的网,平台照收不误但等于没建 —— 现在动手前
  先比对 `pcb_Net.getAllNetsName()`,对不上**一个字节都不写**就拒绝并列出缺失网名;
- **写后回读**:平台返回的 boolean 不算证据,每次写完重读 `getAll` 比对,回执给 `verified`;
  回读对不上直接报错而不是假报成功;
- **幂等**:同名同内容重建 = `alreadyExists`(可重放);同名不同内容 = 明确拒绝并给出下一步
  (改名 / 先删 / 用 `eq-group add` 扩展),不静默覆盖;
- **部分应用**:`eq-group add` 若有的网落地有的没落地,回 `partial` + `notApplied`(画布已变
  就不抛错,#151 约定),不整批回滚。

读侧统一走 `constraintList` 归一化 —— 平台自 v3.4 起 `getAllDifferentialPairs` 可能返回
**对象 map 而非数组**(官方标注的破坏性变更),两种形态都得当成同一份清单,否则形态一变
就会读成「板上没有约束」。

## [1.2.3] — 2026-08-25

> 1.2.2 只在 dev 循环里打过包(未发版),它的条目并入本版。

### Fixed — 放置器件时就把属性值带上,不再等到 PCB 侧 `sync-attrs`(#186)

`sch_PrimitiveComponent.create` 把 device 记录的 otherProperty **键结构**复制到实例上,
**但值全空**。真机实测一颗 C0805:实例上 `Value` / `Tolerance` / `Voltage Rating` /
`Datasheet` / `Description` 键都在、全是 `""`,而同一刻 `lib_Device.get` 返回的记录里
`Value: "10uF"`、`Tolerance: "±10%"`、`Voltage Rating: "50V"` 一应俱全。后果是 BOM 的
值列与器件标准化面板一路空着,要等流程很后面的 PCB `sync-attrs` 才补 —— **而那份记录
在放置当刻就在手里**。

现在 place 落件后立刻回填(同 #157 supplierId 回填的 best-effort 契约:回填失败绝不让
放置失败),回执多一个 `otherPropertyBackfilled: [...]`。

判据与 PCB 侧 `sync-attrs` **收成同一把尺**(`PROJECTED_STATE_KEYS` +
`planOtherPropertyBackfill` 提到模块级共享,原先只存在于 PCB handler 内部):

- **投影键永不写**:库记录自带占位 `Designator: "C?"` 与模板 `Name: "={Value}"`,
  而平台会把 `otherProperty.Designator` 同步进位号 —— 这正是曾把 166/166 真位号
  洗成 `U?/C?/RF?` 的那条路;写入时同一 call 重新断言真实位号,双保险;
- **只填实例上已有的键**:平台 create 已经把「这颗件该有哪些属性」的键集复制好了,
  不新增键 ⇒ 结构上不可能把库占位漏到新实例;
- **不覆盖已有值**:手改与后续标准化都优先于库记录;
- **幂等**:填过一遍第二遍零动作。

**同一 call 重新断言 `designator` + `supplierId`**:整包 otherProperty 写会让平台
**重新投影顶层字段**。designator 那条是已知的(上面 166/166);`supplierId` 这条是本次
真机回读**当场抓到的回归** —— 第一版只断言了 designator,结果实例的 C 号被打回平台默认
`GRM21BR61H106KE43L.1`(正是 #157 要消灭的不可下单值),**而回执还印着
`supplierIdBackfilled: C440198`**。同一块板上的前后对照:C99(旧构建)`supplierId=<MPN>.1`,
C98(修复后)`supplierId=C440198`,两者 `Value` 都是 `10uF`。
**教训照旧:回执不算数,回读才算数。**

7 个新离线单测钉住上述每一条(含 166/166 位号洗白的回归、陈旧占位 Designator 清洗)。

## [1.2.1] — 2026-08-25

### Added

- **`sch check` 新规则 `polarity-convention-outlier`(WARN,#183 第一阶段)—— 同页电容电源脚约定一致性检查**。
  一颗钽电容正极接 GND 曾带着 51 条 WARN 顺利过 gate,打样上电约 10 秒后热失控才发现——DRC 看不见极性、
  静态测量兆欧级测不出、现象带延迟。该规则的判据**不需要器件极性领域知识**:同页同类两脚电容中,
  「一脚电源轨 + 一脚 GND」的多数派对"电源侧在哪个 pin 号"有强约定,违背多数派的那颗就是 #183 描述的
  8:1 强信号。保守阈值防误报:命中样本 <3 不报(无约定可言)、多数派并列不报(歧义)、多数派占比 <75%
  不报(`--strict` 会把 WARN 升级为阻塞,不允许掷硬币);串联/信号电容(两脚都不是电源地)天然排除,
  候选口径 C+数字编号(CN 端子/CR 二极管不进票仓),地网分类含全拼 GROUND;`totalMatched`
  只计真正进入统计的器件。已知限制:`--all-pages` 下候选跨页池化成一个约定而非逐页统计
  (gate 默认单页不受影响),该模式下 finding message 会注明。finding 带 `pins`(电源脚,地脚)+ `nets` + 多数派归因,message
  明确提示 MLCC 无极性可忽略;summary 新增 `polarityConventionOutliers`,Go 渲染端新增计数槽与修复提示行。
  纯函数 `detectPolarityConventionOutliers` / `isGroundLikeNet` / `isPowerRailNet` 可离线单测
  (8 个新用例含 #183 九电容真机复现)。ERROR 级极性判定(需封装/符号证据)留待 #183 第二阶段。

## [1.2.0] — 2026-08-25

> 1.1.2 从未发版(无 tag),它的条目并入本版。
> **升级方式**:CLI/skill 跑 `easyeda update`;**连接器 sideload 必须手工重导入**
> (同 uuid 更新要先在「已安装」里卸载旧的,导入后完全退出并重启 EasyEDA)。

### Added — 版本一致性门:工具链错位当场拒绝并给出修法

CLI / daemon / 连接器三方版本对不上时,`easyeda health` 给出 `versionGate` 判定,
后续动作在 `/action` 层被直接拒绝并附修法,而不是让人先怀疑电路或工具坏了。
明知故犯的逃生口是 `--skip-version-check`(会写审计)。

### Added — S0 spec 位号回填进流程,不用再「每次落块回头改 json」

`modules[].parts` 写的是 designator,而平台会在 create 时按自己的全局位号重编
(计划 C1 → 落地 C11)。位号对不上**不会报错**,只会让分区打分/连接器规则静默少算
一个模块。现在 `sch block-apply --spec <s0.json>` 落块即回填,或事后
`easyeda spec backfill … --write`;写入是外科手术(只替换那一段字节,键序/缩进/
未知字段一个不动),任一模块定位不到就整体拒写。

### Added — `--force-stale-read`:STALE_READ 的逃生口从「不存在的 flag」变成真的

此前拒绝消息里印着一个并不存在的 flag,照抄跑不通。现在它是真 flag,带理由入审计,
且**只放 PCB 读**、解不开布线阶段门。

## [1.1.2] — 2026-08-24

### Fixed — 超时守卫改由 worker tick 兜底:队首卡死不再拖死整条队列

真机连续四次「连接器在负载下停摆」查到底了,**根因不在重连、不在 WebSocket、也不在
daemon**,而在**主线程 setTimeout 在后台窗口里被节流/冻结**,于是连接器所有基于
setTimeout 的守卫(FIFO 的放弃闸 22s、每次平台调用的 `withTimeout` 7s)到点不响。

证据(2026-08-24 三份 daemon 日志 + 审计逐条对齐):

- 09:10:47 一条 `schematic.power.connect_pin` 进入 FIFO 队首,队列**一动不动 6 分钟**;
- 同一时间**心跳一拍不落**(退化前后都是 3.00s/次,它由 Web Worker tick 驱动),
  **旁路读 `document.current` 每次 12ms 就回** —— WS、主线程、`eda.*` 桥全都活着;
- 排在后面的 12 条 `schematic.pages.list`(本身 p50 只有 15ms)**每条各烧满 18s**
  才报 "connector did not respond",合计白等 216 秒;
- 队首终于 settle 后,积压的 12 条**按 FIFO 顺序一次性冲出来,全部 ok:true**;
- 队首那条的错误码是 `EDA_*` 而**不是 `ACTION_ABANDONED`** —— 三份日志与全部审计
  记录里 `ACTION_ABANDONED` 出现 **0 次**:放弃闸自上线以来一次都没响过。

修法:新增 `src/deadlines.ts`,把所有「到点必须发生」的守卫登记成**绝对时刻**,
`setTimeout` 只当快路径,**worker tick 的 `sweepDeadlines()` 是保底路径** —— 那是本
进程里唯一被真机证明不受后台节流影响的时基(看门狗当初正是为此搬进 Worker 的,
只是队列和 per-op 守卫被落在了老路上)。`action-queue` 的放弃闸与 `actions.ts` 的
`withTimeout` 一并改用它。保底路径的分辨率是一拍(3s),所以短守卫最坏晚一拍触发
——远好过晚六分钟。

**行为变化**:队首卡死时,连接器现在会在 `timeoutMs + 2s` 真的放弃它并回
`ACTION_ABANDONED`,队列继续流动。收到这个码 = 那次写的效果**可能稍后才落地**,
关于它的任何结论都不成立(`seqAbandoned` 已递增),必须回读复核后再决定,**不要盲重试**。

### Fixed — 两处「两把尺」:state 身份靠名字背 / block-apply 落点只看器件本体

- **state 身份**:工程被同名删除重建后,旧工程的页记账会继续参与跨页匹配,让
  `spec backfill` / 分区打分把死页算进分母。新增 `easyeda workflow pages --reap`
  (拿活体页表核销)与 `--prune`(清数据);核销后的页会被 WARN 点名并**明确排除出分母**。
- **block-apply 落点**:落点搜索此前只把「器件本体」当障碍,不算 marker 晕圈,于是
  同一页先落主控再落 LED 会撞出图元重叠。现在为 marker 预留约 105 单位——
  **副作用是落点可能离 `--at` 更远**,挤的时候跑 `sch clusters` 看组间压没压到。

### Fixed — zone-draw 拒画:规划器自己造的违规先让掉,拒了也要说得出挪多少

两个内容分得开、只是版面余量对撞的区,现在直接画得出框。真画不出时报文给出逐条
最小位移(`sch zone move --zone X --dy +N`)。版面知识:**竖着叠的两个功能区之间要留
≥96 单位**(24 边距 + 42 说明带 + 30 区名带),横着排的贴着放也画得出框。

### Fixed — 电路说明不再被甩出分区框;`missing-partition` 改看画布而非本地记账

说明带的三把尺收敛成一把;`sch check` 的分区判据改由**画布上的区标题文本**作证
(与 `zone-draw` 生成标题同一个函数),消除「画布上明明有框、check 永远说没有」的恒报。
同时新增 `page-too-small` 判据:块/组比整页可用区还大时**停手交回用户**(拆页是设计决策,
工具不自动分页)。

### Known issues — 随版本如实公布

一次广度优先的端到端(esp32Mini 固定用例)记了 19 条挂账,完整台账见仓库
`docs/e2e-round-2026-08-25-findings.md`。**升级前值得先知道的三条**:

- **`sch group-move --ids` 报「电气自检失败」却不回滚**:位移照样落地,留下悬空脚 +
  悬空树。看到那个 `✗` **不要当作没发生**,先 `sch bridge-check` 复核画布。
  单件带线搬请改用 `sch group create` + `--group`,或 `disconnect → modify → connect`。
- **`sch destagger --apply` 在相邻引脚场景仍会共线合并成短路**,且「已按快照自动恢复」
  这句话在没真恢复时也会打印。跑完必须 `sch bridge-check` 验一遍。
- **`sch gate --strict` 目前无法出 `verdict=pass`**:`missing-titleblock` 的唯一处方
  (写图签)当前被 design-flow 禁用,平台 DRC 又只回聚合数没法清零。**用非 strict 档**,
  把这两类如实写进交付摘要。

## [1.1.1] — 2026-08-21

### Docs — SKILL.md 顶部写清「这个 skill 单独装上没用」+ 连接器下载地址

从 skill 市场(skillhub / ClawHub)装到这份 skill 的人,拿到的只是**指挥说明**——
真正干活的是本机 `easyeda` CLI/daemon 和 EasyEDA Pro 里的**连接器插件**,两个外部件
一个都不能少。此前顶部只在一句话里夹带了插件市场链接,没有可直接照做的安装步骤。

现在写成三步:① 一行装 CLI;② 连接器 `.eext` **两个下载入口**——立创EDA官方插件市场
(一键装、可原地自动更新,但**版本可能滞后** CLI)与 GitHub Release 直下(与 CLI **严格
同版**,四件套同版以它为准),并写明「同 uuid 更新必须先卸载旧的、导入后必须完全退出重启
EasyEDA」这两个静默失败点;③ 开「允许外部交互」并用 `easyeda health` 验证。

CLI / daemon / 连接器行为**无改动**,纯文档版本。

## [1.1.0] — 2026-08-21

本版把 `1.0.3`–`1.0.5` 三个未发布的中间版本一并放出。**minor 而非 patch,因为下面
第一条是行为破坏性变更** —— 升级后一批此前「能跑」的 PCB 读会开始报错。

### ⚠️ BREAKING — 铁律 5 从劝告升成机械门:PCB 脏读现在被**拒绝**

PCB mutation(rip-up / route / delete / via / track / pour)之后、`easyeda doc reload`
之前的 PCB 读(list / DRC / report / nets …),会被 daemon 在 `/action` 派发层**拒绝**,
错误码 `STALE_READ`。此前它只是一行非阻塞的 stderr 警告。

**为什么升门(有实测,不是拍脑袋)**:把 49 天 / 171554 条审计记录里每条铁律的**遵守率**
和它在 SKILL.md 里占的**篇幅**对齐,两者完全反相关 —— 有 daemon 拦截的阶段门(铁律 14)
7 次跳步全被拦、0 漏网;而只发警告的铁律 5 被违反 **1780 次(18.1%)**。文字铁律的
天花板约 82%,机械门是 100%。

**拒绝消息自带能直接执行的下一步**,不只指路:

```
pcb.components.list —— PCB 自 pcb.line.create 后未 reload,读到的是旧引擎状态。
下一步: easyeda doc reload --project ceshi
(绕过: --force-reason "<理由>",入审计)
```

- **升级后要注意的**:布完线直接跑 `easyeda workflow advance` / `pcb check` /
  `layout-score` 会撞 `STALE_READ`。**这不是命令坏了** —— 在旧引擎状态上判 check 门
  本来就是假绿灯。先 `easyeda doc reload` 再跑。
- **豁免**:`pcb.save` / `pcb.pour.rebuild` / 任何 `--dry-run` 预览 / 只改视图的
  `view-side`·`layers set-current`·`layers visibility` / `pcb.snapshot`(它仍只带
  advisory —— 截图白帧的修法是**切前台**而不是 reload,拒它等于递一条修不好问题的命令)。
- **绕过**:`--force-reason "<理由>"`,审计里记成 `daemon.stale_read.force` 伪动作行。
- 内部的「写后回读验证」类命令(`pcb refine` / `sync-designators` / `import-changes` /
  `power-planes` / `power-pour` / `route-critical` / `autoroute` / `pcb clear` /
  playbook `verify:` 块)已逐条加上**窄到一次调用**的放行位,不受影响;而 `refine`
  下一轮开头的规划读等**仍然被拦** —— 那正是这道门要防的。

### Changed — `STAGE_BLOCKED` 的拒绝消息也带上补门命令

此前只说「see `easyeda workflow status`」,现在直接给出该跑哪条:
`pcb stage confirm-tier` / `confirm-outline` / `pcb layout-lint --gate` /
`workflow advance`,按缺的是哪道门决定。

### Fixed — `copperLayerCount` 读不到时不再静默按 2 层降级

板子若在命令开跑前就脏,一块 4 层板会被当成 2 层,于是走 `power-pour` 而不是
`power-planes`,两条电源轨挤在同一层 —— 正是内电层要解决的那个冲突。现在会把降级
明确说出来。

### Changed — 连接器端口钉死 60832 + 指数退避

见下方 `[1.0.5]` 条目全文。摘要:daemon 重启后的重连从「十几秒」降到 **~2-3s**;
顺带修掉「`SLOW_RETRY_DELAY_MS = 10s` 因为看门狗每 3s 抢先调用而从未真正生效」。

### Added — skill 发布链路

- 适配 **Agent Skills 官方规范**:补齐 `license` / `compatibility` / `metadata`
  三个字段,`skills-ref validate` 通过;版本号随 `make release` 自动同步。
- **skillhub.cn 走 CI 自动发布**(订正旧结论「skillhub 无 CLI」——现在有真 CLI):
  `release: published` → `.github/workflows/publish-skill.yml`。slug 为
  `eda-agent-connector`,与立创插件市场的连接器条目同名。

## [1.0.5] — 2026-08-20

### Changed — 端口钉死 60832 + 指数退避重连(不再横扫 10 个口)

**daemon 重启后的重连从「十几秒」变成「秒级」。** daemon 早就只绑 **一个** 固定端口
60832 且**从不外溢**(`internal/app/cmd_daemon.go`:60832 被占就替换/拒绝,绝不换个口
悄悄再起一个),但连接器这头还在扫 `60832-60841` 全段。60833-60841 **结构上不可能**
有 daemon,扫它们是纯空转 —— 而 `eda.sys_WebSocket.register()` **从不报告「连接被拒」**,
每个死端口都要烧满整个 `CONNECTION_TIMEOUT_MS`。用户真实插件日志(2026-08-20 23:41)
里,光走 5 个死端口就花了 7 秒,且 `make dev`(air)**每改一次 `.go` 就要重连一次**。

- **只试 60832**:默认端口表长度固定为 1。
- **指数退避**:首次立即,失败后 0.5s → 1s → 2s → 4s → 8s 封顶,各带 ±25% 抖动。
  一次尝试成本 ≈ 200ms(REGISTER_DELAY_MS)+ 1500ms(CONNECTION_TIMEOUT_MS),
  所以 daemon 重启这种「一两秒就回来」的常见场景稳定在 **~2-3s 内重连**;真的长时间
  没有 daemon 时则退到 8s 一次的安静轮询,对 EasyEDA 共享 socket 表的压力比原来固定
  3s 轻约 3 倍,也比原来 10s 的慢轮询恢复更快。
- **退避是真的生效的**:新增 `nextAttemptAt` 时间闸。看门狗每 3s 就会调一次重连,
  原来的 `SLOW_RETRY_DELAY_MS = 10s` 因此**从来没有真正慢下来过**(定时器排的时间被
  看门狗抢先)。现在所有非强制入口都要过这道闸;**手动重连 / 窗口回到前台 / 退避定时器
  到点**这三条是强制路径,不受闸限制。
- **换 wsId 的阈值 2 → 4**:一次「失败」过去是 ~18s 的整轮扫描,现在是 ~1.7s 的单次
  尝试。阈值不动会把换 id 的节奏压到几秒一次,在 EasyEDA 共享的 socket 表里堆死 id
  —— 正是这段逻辑当初要躲的 race。
- **逃生口(没有焊死)**:扩展用户配置 `daemonPorts` 可覆盖端口 ——
  `eda.sys_Storage.setExtensionUserConfig('daemonPorts', '60832-60841')`、
  `'60840,60832'`、`60900` 都认;每次尝试开始时**实时读取**,改完下一轮重试就生效,
  不必重装 `.eext`、不必重启 EasyEDA。坏值一律退回钉死的默认;覆盖列表封顶 12 个口
  (手滑写 `1-65535` 不能把重连变成无尽扫描)。`60832-60841`(`0xEDA0`-`0xEDA9`)
  仍是我们保留的专属段,依旧刻意远离官方 `eext-run-api-gateway` 的 `49620-49629`。
- 回归测试 `src/transport-ports.test.ts`(离线,12 条)钉住:默认表长度必须为 1、
  退避阶梯与封顶、抖动边界、逃生口解析与去重上限、多端口时 lastGood 优先。

## [1.0.4] — 2026-08-20

### Changed — 动作串行化(FIFO)+ 放弃机制 + 顺序证据三字段

**行为变化,读写对齐的地基。** 此前连接器**并发处理动作**:
`transport.ts` 把每条 WebSocket 消息交给各自的 onMessage 回调,而 `await`
不跨回调排队,于是两条动作可以同时在飞。用真 transport.ts + 假 `eda` 全局跑的
探针实测(2026-08-20):写还没 settle,读的 handler 已经开跑,响应也先发了出去。
结果就是「先发写、再发读」在连接器里**不构成任何先后关系** —— 一次回读报
「那里什么都没有」,既可能是真的没有,也可能是读得太早,两者在观测上完全等价。

- **真 FIFO**:所有动作走 `handleRequest` 这一个咽喉,每窗口一条显式 promise 链,
  一次只跑一个 handler。回归测试 `src/transport-fifo.test.ts` 驱动真 transport
  钉住这一条。
- **放弃机制**(与 FIFO 同等重要):队首超过**请求自带的 `timeoutMs`**(+2s 宽限,
  晚于 daemon 自己的超时)仍未 settle,就放弃等待它、`seqAbandoned++`、队列继续
  流动,并回一条 `ACTION_ABANDONED`。**截止时间绝不写死常数** —— `sch check`/DRC
  这类合法长操作能跑 60s+,固定门会把它们全误杀。没有这条,一个永不 resolve 的
  handler 会吞掉后续一切(真机见过:一次卡死让接下来 4.5 分钟的
  place/delete/document.open 全部静默消失,而轻读照常)。
- **顺序证据三字段**:每一条 response frame 新增 `seq`(已完成动作数,本动作完成
  之后的值,单调递增)、`seqAbandoned`(累计被放弃数)、`unordered`(可选,true =
  这条响应走了旁路通道、其 seq 不构成顺序证据),外加便利字段 `abandonedIds`
  (最近 ≤32 条被放弃的 request id,让判定能点名而不只是数数)。
- **旁路通道**:`document.current` 一条(名单短、理由写在 `action-queue.ts`)。
  纯读、代价恒定、且「读得太早」的失败方向安全(它只会报旧页面让 `--doc` 门重试,
  不可能谎报已切到目标页)。wedge 期「轻读还能用」是唯一的观测手段,不能因为串行化
  而失去。旁路响应**必须**打 `unordered`。
- **溢出保护**:队列积压上限 64,满了回 `QUEUE_OVERFLOW`(该动作**没有执行**),
  不无限堆积。

**seq 证明什么、不证明什么(别越界)**:它只证明「W 的 handler 在 R 的 handler
开跑前就 settle 了」——**不是**「文档已提交」。`eda.*` 可能在 handler 返回后才落盘,
那一层我们没有观测点。CLI 侧所有基于 seq 的措辞都停在这条边界内。

**版本搭配**:
- **老 CLI + 新连接器** —— 正常。三个字段是**新增的顶层字段**,老 daemon 解析响应
  时直接忽略;FIFO/放弃对老 CLI 是纯粹的行为改善(不再有并发写读交错,卡死的队首
  不再吞掉后续动作)。唯一可见差异:极端积压时会收到 `QUEUE_OVERFLOW`,老 CLI 把它
  当普通失败报出。
- **老连接器 + 新 CLI** —— 正常,但**判定降级**。`sch block-apply` 的 place 超时
  收编会发现响应不带 `seq`/`seqAbandoned`,自动退回原来的探针启发式,并在报文里
  打上「证据档:弱(探针启发式)」+ 升级连接器的下一步。**绝不会**因为缺字段就默认
  「新鲜」。侧载的 `.eext` 与 CLI 严格同版;市场装的那份可能滞后若干 minor,那就是
  会落到这一档的典型情形。

### 随版 CLI 侧修复(连接器无关)

本条记录随版 CLI 侧对「删除/组注册」三缺陷(esp32Mini E2E
交接报告 §六 缺陷 2/3/4)的修复,便于版本对照:

- **删器件级联删组注册(缺陷 2,P1)**:虚拟组注册表存在 **Go 侧**
  (`workflow.State.GroupsByPage`,`~/.easyeda-agent/workflow/<project>.json`),
  连接器的 component.delete 级联(ADR-0004 Decision 5,只清桩线/flag)够不到它。
  现在 `sch prim-delete` 与 block-apply 回滚在**回读证实删除成功后**同步摘除该
  位号的成员记录(指向死位号的 role 一并摘,组删净则删组)——位号复用不再被
  陈旧组吃掉。
- **删除不可靠→逐个删+回读证实+重试一次(缺陷 3,P1)**:平台批量 delete 静默
  no-op 仍返 true(真机:zone-draw 删旧框 survived=4、回滚 deleted=false,逐个删
  100% 成功)。Go 侧新增 `deleteVerifiedOneByOne` 统一语义;block-apply 回滚改
  逐个 `component.delete`,zone-draw 删旧框/绘制回滚的 exec_js 内联同一套
  逐个删+重试。判定只信回读。
- **`sch group create --block-id/--instance/--roles`(缺陷 4,P1)**:组注册损坏后
  可手工重登块溯源(与 block-apply 自动登记同一批字段),`sch reconcile` 恢复
  机械对账(reconcile 需要 --block-id **加** --roles ROLE=位号)。

## [1.0.2] - 2026-08-19

ADR-0004「挪动收敛为单一安全 move 内核」首批随版(源自 issue #181 两份 E2E
复盘)。CLI 侧:schMoveKernel 五步管线(快照→删证回读→snap 移动→快照重连→
电气对账,失败自动恢复、目标页失效 fail-closed),group-move/zone move/
zone-arrange/relayout/**destagger(解禁)**五命令收敛为内核调用方,
`group-move --groups` 多子组一次整体移动;`--zone/--group` 统一命名空间
(一张注册表+来源标签,报错列全量可用名);dry-run 纯计算机械保证(派发层
拒 Mutates)。连接器侧两条见下。

### Fixed
- **`pcb.component.modify` 解锁假成功(#174)**:平台 `modify()` 的真实锁字段是
  `primitiveLock`,而我们的读侧(`pcb list`)一直报 `locked`,调用方自然传
  `{"locked":false}` —— 平台**静默忽略未知键仍返回成功**(真机 22/22 报 ok、
  reload 后 22/22 仍锁定)。现在:(1) patch 归一化,`locked`/`lock` 别名映射到
  官方键 `primitiveLock`,契约外的未知键**硬报错**(不再允许静默 no-op);
  (2) 每次写后**新鲜回读**逐字段核对(modify 返回对象会回显输入,不可信),结果
  带 `verified` + `applied`/`notApplied`/`unverified`;(3) 回读发现锁写被丢时
  自动改走 `setState_PrimitiveLock` + `done()` 写路径(`pcb.track.lock` 验证过
  的模式)重试并再核对(`lockFallback:true`);(4) 按 #151 部分应用约定:画布
  已变(部分字段落地)返回 ok + 结构化 `notApplied`,**零字段落地才报 ERROR**。

### Added
- **`schematic.component.delete` 级联清理(ADR-0004 Decision 5)**:删件后自动
  找出**只**挂在被删件 pin 上的桩线树(union-find 共点归树 + 点到线段锚定,与
  bridge-check 同一套判定)以及挂在这些树上的 netflag/netport/netlabel,一并
  删除并回读证实。树若还触及任何**存活**器件的 pin 即视为共享,绝不删;被删件
  自身残留(删除撒谎)时其树也整体跳过。结果新增
  `cascaded: {wires:[ids], flags:[ids]}`(只列**回读证明已删**的 id);级联删除
  撒谎的存活 id 按 #151 部分应用约定计入 `notApplied`(ok 保持 true + warning)。
  根治「删件残留桩线/旗被后放件静默继承网名」的幽灵连接(v1.0.1 的 orphan-tree
  判据只能事后抓,现在事前防)。CLI 渲染点(prim-delete / block-apply 回滚)
  带一行「级联清理 N 桩线 M 旗」。
- **`schematic.component.delete` 新 payload 字段 `cascade:false`(退路)**:保持
  旧行为(只删组件本体,不碰桩线/旗)——自己管整树删除的调用方(如 ADR-0004
  的 move 内核)必须传它,避免级联与内核的整树管理相互踩踏。默认 `cascade:true`。

- **`pcb.component.lock` — 批量组件锁定/解锁(#174)**:输入 `primitiveIds[]` +
  `locked`,走专用 `setState_PrimitiveLock` + `done()` 写路径,写后新鲜回读逐件
  核对;结果 `applied`/`alreadyInState`/`notApplied`/`missing` + `verified`。
  幂等(已在目标状态的只计数不重写);全部未落地时报 ERROR 而非假成功。CLI:
  `easyeda pcb lock --ids id1,id2 [--unlock]` / `easyeda pcb lock --all --unlock`
  (整板批量释放,--all 由 CLI 读活板 component 列表展开)。

## [1.0.1] - 2026-08-18

连接器侧一条修复(见下);CLI 侧同批落地:artifact 目录递归嵌套归一、
`--doc` 对读命令生效、`board.rebind` 目录补登记(UNKNOWN_ACTION 出口补审计)、
裸 `sch connect` 35s 预算 + 慢速落地复核(slowLanded)、`sch group-move`
半途失败自动重连恢复、`sch note --zone` 框满走廊落点、`sch autoconnect`
带痕候选 WARN + `--strict`。

### Fixed
- **`schematic.pin.disconnect` 假成功**:此前删除只调 delete 后不回读,平台对
  并入共享树/共线段的桩线**静默 no-op 仍返 true**,动作照样报
  `disconnected:true`(真机复现:R5:1 / R5:2 / C4:2 断开后网表仍连)。且坐标
  定位只取第一根触脚导线就 break,同脚多桩场景天然漏删。现在:(1) 收集**全部**
  触及目标 pin 端点的导线一起删;(2) 删除后 getAll 回读验证,存活即按部分应用
  约定返回结构化 partial(`ok:true` + `partial:true` + `notApplied`/
  `survivedIds`,`disconnected` 仅在全部证实消失时为 true,`deletedWires`/
  `deletedFlags` 只计证实删除的 id)并附 warnings;CLI `sch disconnect` 把
  partial 渲染为醒目 stderr 警告(不再静默成功)。

## [1.0.0] - 2026-08-18

**里程碑:原理图功能正式上线(S0–S6 可交付)。** 版本从 0.26.x 跃迁至 1.0.0,
连接器与 CLI/skill 同版(四件套同版约定)。0.26.1 未单独发布,其连接器变更
(`schematic.bridgeCheck` 的 `ORPHAN_TREE` 悬空树判据,见下方 0.26.1 条目)随本版
一并发布。1.0.0 的主体变更在 CLI/skill 侧:三层布局体系(Sheet→Zone→Group,
分区框/区名/电路说明算法落位,生成与校验同一把尺)、`sch gate --strict` 五关
机械门禁、跨页网名审计 `sch nets --strict`、块对账 `sch reconcile`、netlist
黄金表逐脚比对;esp32Mini 真机端到端回归通过(3 页 / 26 件 / 18 网全对)。
连接器 0.26.x 与本版 CLI 协议兼容,但 `orphan-tree` 判据需本版连接器才生效。

## [0.26.1] - 2026-08-18

### Added
- **`schematic.bridgeCheck` 新判据 `ORPHAN_TREE`(悬空树)**:wire 树不触任何
  引脚 —— 挪件残留(flag+桩线随器件移动被遗留原地)或纯裸死线。此前
  `ORPHAN`(要求树触到引脚)与 `ORPHAN_FLAG`(要求 flag 不挨任何导线)对这种
  形态**结构性盲区**:真机 2026-08-18 P2_MCU 页两棵 GND flag+桩线残留树,
  bridge-check 连报 0 orphan,靠渲染图人工数 flag 才抓到。summary 新增
  `orphanTrees` 计数;CLI ≥ 同版把它渲染为 `orphan-tree`(WARN,gate `--strict`
  阻塞)。旧 CLI 读新连接器只是忽略新字段,不破坏兼容。

## [0.26.0] - 2026-08-17

**连接器侧无代码变更** —— 版本号随 CLI 同步(四件套同版约定)。本版全部变更在
CLI/skill 侧:`sch zone-arrange`(功能区确定性布局两段求解 + `--apply` 断言门)、
`sch status`/`sch nets`/`sch reconcile`/`sch clusters`/`audit cost` 新子命令、
图框(sheet)误删双层守卫与 `sch titleblock --data` 写入禁用令(写路径会损毁
sheet 符号引用,重启后图框丢失 —— 修复处方见 skill actions.md)、原理图 SOP
步骤卡。连接器 0.25.x 与本版 CLI 完全兼容。

### Changed
- **许可证正式落地为 MIT**：此前仓库根本没有 `LICENSE` 文件(GitHub 视角 = 版权
  全保留,严格讲不是开源),声明只散落在 `NOTICE`/清单里且互相打架(根
  `package.json` 写 MIT、连接器写 Apache-2.0)。现在补齐根 `LICENSE`(MIT),
  `extension.json` 的 `license` 随之改为 `MIT`、`package.json` 改为 SPDX 复合
  表达式 `(MIT AND Apache-2.0)`。**例外**:`src/beautify/` 下四个文件移植自
  Easy_EDA_PCB_Beautify(m-RNA),继续沿用 Apache-2.0 —— 无权改,也不必改(两者
  兼容)。许可证全文放在 `src/beautify/LICENSE`,`.eext` 包内新增 `extension/
  LICENSE` 承载 MIT 全文 + 第三方告知(`dist/` 里含 beautify 的编译产物)。

## [0.25.1] - 2026-08-13

修复立创插件市场审核驳回「README图片未加载成功，请检查存储路径」(0.24.0 被拒)。

### Fixed
- **README 演示图改用平台图床绝对链接**：三张演示图从包内相对路径
  `images/demo-*.png|gif` 改为 `https://image.lceda.cn/extensions/images/...`。
  根因不在路径写法——0.23.0 用同一份 README 过审，包内容三版字节级一致；
  真因是市场服务端解析 `.eext` 时的「README 抽图 → 转存图床 → 改写链接」
  那一步偶发失败：过审的 0.23.0 库里 readme 字段是 `image.lceda.cn` 绝对
  链接，被拒的 0.24.0 则没有 readme 记录，审核员打开就是三个裂图。改用
  平台自家图床的绝对链接后不再依赖那一步（链接本身正是平台为 0.23.0
  转存生成的，字节与包内图片一致）。图片文件仍随包发布（`images/` 保留，
  logo 走 manifest 的 `./images/logo.jpg` 不变）。

## [0.25.0] - 2026-08-12

原理图布局体系定版:placement-first 区级重排(`sch zone relayout`)+ 串联链
共线串接 + 全员竖放平行对齐 + 顺方向 netport + 竖直旗真值表修正 + 三个新判据
(reversed-net-flag / marker-overlap 文字带 / redundant-net-marker)。
连接器含 0.24.1–0.24.5 全部修复(合并线段对解析三处统一/orphan-stub 直连豁免)。

### Added(CLI 侧,串联链 chain——用户拍板「一串完成不折弯」)
- **relayout 识别 pin-to-pin 直连链**(如 GND—LED—R—netport):全链共线横放
  串接,相邻 pin 短直线、零折弯;地端在左(旗竖直向下)、netport 端在右
  (水平顺链向);极性件(LED)rot 按「链左 pin 实测在左」消解。链宽超区带
  报错提示手动分段(断点两侧同名 netport 对、2+2 分行)——能一串就一串,
  不主动加标签。
- **band 生长障碍钳制修复**:旧判据只认「完全在外侧」的障碍,初始 inflate
  相交的邻区被忽略 → 生长穿透邻区腹地(实测 LED band 穿进 MCU 区,链件摆进
  别人家)。凡与 band 正交区间相交且伸过边缘的障碍一律钳制,已相交钳在原地。

### Changed(CLI 侧,netport 顺方向摆布——用户拍板,取代「永不竖放」铁则)
- **netport 顺导线方向摆布**:竖放件的 netport 顺竖直引出(rotation 走
  orientation port 真值表 up=90/down=270),不再强制水平——水平引出的 L 形
  正是件间距不等的根源;现在全员回归 60 等距完全平行。旧铁则「netport 永不
  竖放」的真实痛点是密集 pin 列上侧向标签互相堆叠——那是拥挤,由
  marker-overlap(netport 平台 bbox 天然含文字)管;folded-net-label 判据
  恒零(函数与 summary 字段保留,报表兼容),layout-score folded 维恒满分。
- 相关测试翻转为语义变更回归钉(竖放必须不再报);宿主解析测试改为直测
  hostByNearestPin/hostByWireTrace 纯函数(原借 folded 归因当载体)。

### Changed(CLI 侧,relayout 全员平行布局——用户拍板)
- **外围电容电阻全员竖放、同一行、平行对齐**:此前去耦竖放/信号链横放两种姿势
  (同是电容 C4 竖 C5 横,不工整)。现在 signal 件也转竖放——电源旗端朝上/地旗
  端朝下(与双旗件同轴),netport 从另一端**水平朝左**引出(netport 永不竖放
  不变);全员器件锚 y 相同 = 本体/顶旗/底旗三条线全对齐。间距:纯双旗件 60,
  带 netport 件 110(容水平文字)。双 netport 件(无电源轴)保留横放 fallback。

### Added(CLI 侧,锚 IC 电源旗也守「电上地下」)
- **relayout 收尾把锚 IC 的横躺电源/地旗竖直化**(用户点名:外围都守约定,中心
  器件不能例外)。安全判:竖直桩不穿本件相邻 pin(列顶电源脚/列底地脚才安全),
  不安全的保持横躺并报告。横躺判据用旗**自身 rot**(0/180=竖直)而非 pin→锚
  方向——L 形合流树(pin 横引再竖下挂旗)的竖直旗按方向近似会被误判横躺、
  重连拆散合流(实测 U2 双 GND 合流被误报)。

### Added(CLI 侧,`sch zone relayout` —— placement-first 区级重排,用户点名的正确顺序)
- **新命令 `sch zone relayout --zone X [--apply]`**:与 zone tidy(挪带线的组)
  的根本差别是顺序——①锚 IC 定位定向(V1 不动)→ ②外围器件按角色**纯计算**
  终局(去耦竖放锚右同顶等距 60、信号链横放其下同行基线共线、链宽按 pin 实测
  +网名估)→ ③deep sweep 删净旧桩旗 → 逐件落位 → 一遍性重连(真值表)。
  全程不搬带线的图元,组刚移的 merge 撕裂整类问题不存在。ceshi MCU 区实测:
  任意乱局一条命令到终局,自检绿,gate PASS。
- buildTidyPlan 加 forceAll(全员出计划,幂等 no-op 不短路);tidySignalPlan
  加 HasPose(signal 件执行时先落位再重连)。
- **zoneTidyGrowBand 双序生长**:生长顺序决定 L 形空间里矩形的形状(先纵后横
  把下方长满、右缘被低处图签卡死只剩 404 宽;先横后纵长到 806)。两种顺序都
  算取面积大者;relayout 的 band 无条件生长(分区 rect 是「当前内容」的函数,
  不生长就是鸡生蛋——上轮排成一列,band 缩窄,这轮还是一列)。

### Added(CLI 侧,signal-row 链端电源旗竖直化)
- **group tidy signal-row 升级**:链末端横躺的 power/gnd netflag(left/right)
  一律重连竖直(power 上/gnd 下)——横躺旗的文字竖排侧向渲染(平台特性),
  用户点名三处「文字竖着难看」;竖直化后与去耦列同款风格。已竖直 = no-op。
- **修 signal-row 重建覆盖缺口**:deep sweep 删的是全件的树(两端),但重建
  计划只含被修 pin——netport 端删了没重建 → 悬空(实测 C5/R1/R2 三件同炸)。
  现在有任何修复目标时全部 netport pin 都进重建计划(已水平的按原语义重建)。
- **staging park 位从带右外 +200 改 +500(出纸)**:+200 曾把组 park 进右邻
  功能区腹地(band 右外 ≠ 空地),park 桩与邻区线共点 merge 出跨区短路且回滚
  撕裂扩散到邻区(实测两次,第二次伤到 POWER 的 C3)。纸外无内容可 merge。

### Added(CLI 侧,zone tidy 行内电气基线对齐)
- **横放信号链行内基线共线**:行内对齐从「组 bbox 顶」改为「电气基线」(器件
  锚 y = 桩线 y;行首组定行基线,后续组共线)。bbox 顶对齐时两组上伸量不同
  (GND 旗符号 vs 3V3 旗文字)会让同行两条链的走线错开 5-10 单位(用户点名
  「横竖不齐」第三轮)。竖放组不变(总高已统一,顶齐即底齐);sheet 层无基线,
  行为不变。

## [0.24.5] - 2026-08-12

### Fixed
- **wire-crossing 对平台合并线(segment-array)的伪交叉误报**:collectWireSegments
  把段数组当折线读,在无关段端点之间捏造对角伪段——4 段正交 GND 合流树被报
  「自己跟自己交叉」(伪对角中点)。与 0.24.1 dangling 修复同款解析:偶数顶点
  ≥4 按段对(stride 4),奇数回退折线(stride 2)。wire-over-pin 同管道受益。

### Added(CLI 侧,check 补两个视觉盲区判据——用户第二轮肉眼抓出)
- **reversed-net-flag**:netflag 的 stored rotation 与桩线方向按 orientation
  真值表不符(倒挂/侧翻)→ WARN。此前朝向判据只活在 layout-score(非门),
  gate 的 check 关抓不到反旗;Go 侧表与 orientation.json 的一致性单测钉死
  (再分叉 = 再度双盲)。现场首跑即抓到 EPAD 的 GND 倒挂旗(块 wiring 老表遗毒)。
- **marker-overlap 文字带建模**:平台 getPrimitivesBBox 只包旗符号本体(实测
  GND 旗 10×21,"GND" 文字不在内)——两支旗符号相切、文字互叠时 overlap=0
  静默(U2 双 GND pin 挤成一坨、POWER 区 U1 左三支旗互叠共 4 处全漏)。netflag
  判定 bbox 现 = 符号 ∪ 文字带(位置按朝向:水平旗文字在锚内侧线上方,竖旗
  文字在符号外端居中;宽 6/字符)。现场首跑抓全 4 处。

## [0.24.4] - 2026-08-12

### Fixed
- **竖直旗 rotation 真值修正(2026-06-29 校准错了两个月)**:当时把 cycle 方向
  搞反、用 anchor 对调补救——水平值恰好凑对,竖直值一直反(connect_pin 放的
  朝上 3V3 存 180 渲染倒挂,而 linter 用同一份错表把它判「正确」——生成侧和
  校验侧共享同一个错,机械门全绿、视觉不合格,用户肉眼抓出)。五点真机实测
  (power up=0/right=270/left=90,ground down=0/left=270)确认逆时针 cycle
  `up→left→down→right` + 自然锚 {power:up, ground:down, port:right} 复现全部
  12 个数;相对旧表只有 up/down 翻转,left/right 逐字节不变。orientation.json
  (SSOT)/actions.ts/linter fixtures+goldens 三处同步,derive 一致性单测护住。

### Added(CLI 侧,竖放视觉规整)
- **竖放去耦统一总高**:group tidy 的 power-updown 桩长按 pin 距补偿
  (offset=(100−pin距)/2)——同排组高不一(块 wiring 桩 30 vs autoconnect 默认
  桩 20)顶对齐后底不齐。现在同排竖放组顶线/底线双齐。
- **竖放桶行内间距 40**:竖放组左右无标签文字,117(相向水平标签安全距)的
  语义不适用,排出来松散。

### Added(CLI 侧,zone tidy 组间布局细化)
- **同形态分桶分行**:竖放组(双电源旗去耦,上电下地)与横放组(带 netport 的
  信号链,netport 只能水平)不再按面积混排一行——桶切换强制换行,竖的一排、
  横的一排(用户点名「横着和竖着不规范」;混排顶对齐后高矮参差)。
- **移动次序依赖排序**:组 i 的目标位压着未移组 j 的原位 → j 先走(Kahn,
  有环带外 staging 两跳)。平台会把暂态叠位的共点线 MERGE 成一根,之后再移
  就撕出短路(实测:C5 落到未搬走的 R2 原位,EN/IO0 树合并 multi-net,回滚
  更撕成同点双 netport)。
- **回滚后强制复检 bridge-check**:回滚也是刚移,merge 过的线逆移同样撕——
  红则大字报「板已受损+修复路径」,不再静默留坏板(此前提示人工复核,实测
  漏过一次坏板直到下轮 gate 才发现)。
- zone 方位词 `left-center`/`center-right`/`any` 同步进 `internal/spec.ZoneNames`
  (契约侧;跨包一致性单测抓出的分叉)。

## [0.24.3] - 2026-08-12

### Fixed
- **bridge-check orphan-stub 误报 pin-to-pin 直连线**:无标线树只要触及 ≥2 个
  不同器件 pin 就是合法直连(网得自动名如 `$2N1792`),不再报 orphan——只有
  真·单 pin 空桩才是 dangling(实测:LED 直连线替换相向 netport 对后被误伤,
  `sch gate --strict` 因此挂红)。

### Added(CLI 侧,Sheet 层 + 布局算法通用化)
- **`sch sheet tidy`** Sheet 层布局:全部功能区当刚体依据纸张排布(shelf 行排,
  图签当**障碍物**右跳避让 = L 形可用区,非整条底带让位);band 按分区框最终
  占位收缩(内容外 pad 24 + 标题带 30),区间 vGap 默认 90(两框 pad+标题带
  =78,+缝 12);现状已达标时幂等 no-op;--apply 逐区 zone move + 统一重画框。
- **zone tidy band 自动生长**:区带装不下时向纸面空地四向夹逼生长(避开其他
  分区+图签+纸边,只长不缩)——区带锁死旧分区 rect 会把「区内容要长大」判成
  无解(超高模组锚在 326 高旧带里永远装不下)。
- **zone pack 策略 B(锚侧行排)**:锚下装不下时其余组行排到锚右侧子带——
  40 脚模组这类「超高锚」吃满带高时,去耦/外围贴芯片侧是电气+几何双正确形状。
- **zone 方位词新增 `left-center` / `center-right` / `any`**:跨两列的宽区
  (超高主控锚+侧排外围)原有 1/3 网格词罩不住,逐件 zone-violation 误报。

## [0.24.2] - 2026-08-12

### Fixed
- **group.move 搬迁平台合并线(segment-array)时崩溃**:平台会把共端点同网线
  合并为一条 line=(x1,y1,x2,y2)×N 的多段图元,原样喂回 `create()` 被拒——旧线
  已删、新线没建,组半搬(实测 sheet tidy 首跑 MCU 区在 LED_CTRL 三段合并线上
  阵亡)。现按段拆成 N 次单段 create(平台自行再合并),0 长度填充段跳过,
  分段失败报出已建段数。

### Added(CLI 侧,三层布局体系 —— docs/schematic-layout-hierarchy.md)
- **`sch group tidy`** 组内布局计算:双电源旗电容自动竖放+上电下地+文字朝外
  (真机校准 rotation 表:power up=0/gnd down=0);实测 pin 旋转二义消解、
  stale 双读防线、未建模第三连接拒绝(3-pin 馈通不被扯断)、disconnect 连带
  断开即错、自检红即逐步回滚。
- **`sch zone move`** 功能区整体刚移(带组带件带 note,分区框自动重画):
  全区一份展开治"区内直连线被判跨区留守"的刚体撕裂;重画前几何指纹 settle。
- **`sch zone tidy`** 组间叠加布局:锚组+上下堆叠(hGap=117 可调),装不下
  给最小尺寸诊断;双认领图元差集过滤(正向/回滚对称)。
- 三命令均经独立交叉评审(2 FAIL 修复转绿 + 1 PASS 加固)与 ceshi 真机三层
  联动验收(乱排→tidy 复原/整区 move 单程 check 0 dangling/装不下诊断)。

## [0.24.1] - 2026-08-12

### Fixed
- **dangling 检测对平台合并线(segment-array)误报**:自由端 = 线段图的度 1
  顶点(偶数 ≥4 顶点按线段对解析,奇数回退折线链;闭环退化取首尾)——此前
  把多段合并线当折线读,3 段 L 形合并线的中间顶点被误判成自由端。

## [0.24.0] - 2026-08-12

原理图能力大版:持久化编组 + 布局质量打分 + CLI 参数收敛(含 BREAKING)。
连接器侧含 0.23.1–0.23.3 的全部修复(见下)加本版的 group.move flag 支持与
schematic.snapshot 移除;CLI 侧新增见 repo commit 历史(feat/sch-zoned-layout-opt)。

### Added
- **原理图持久化编组(virtual group)**:`sch group create/list/add/remove/ungroup`
  按 documentUuid 持久化(平台无编组 API,真机探测+UI 实建 Group1 差分复核:
  扩展 API 零可见);`sch group-move --group` 成员桩线+标志自动展开随组刚移
  (完整性预检拒半搬、共线残骸判据、"树终止于异脚"方向判据,live dump 全量
  回放测试);`align/distribute` 部分组硬拒绝(`--break-group` 放行)。
- **`sch layout-score` 布局质量五维打分**:折叠/反向/贴芯片距离/长链/框贴合,
  逐项归因**带可执行 fix 命令**;模块认领感知(zones claims 围栏)、电源网豁免、
  宿主 pin 走导线端点匹配。实战:ceshi 重排 86.2→96.0 [excellent]。
- **`sch check` 新增机械检查**:`missing-partition`(多器件页未画分区框/说明,
  铁律#15 兜底)与 `folded-net-label`(netport 竖排折叠);autoconnect 打分器
  新增竖排折叠惩罚(密集引脚列标签保持水平)。
- 分区框几何修正:图签 keepout 校准(HeightFrac 0.24)+ 抬升/校验共用安全余量
  + 框贴合模块内容 + 贴边校验,六项 validation 全 0 才许画。

### Changed / BREAKING(CLI 与手册配套发版,不留兼容)
- `--ids` 全域改 **CSV**(JSON 数组字符串不再支持);
- `sch delete` 移除(`prim-delete` 唯一删除入口);
- `sch snapshot` / `schematic.snapshot` 移除(出图统一 `sch export-image`,
  选区导出与文档数据逐件核对验证);
- `sch connect` 新增 `--pin 位号:脚号`(与 `--x/--y` 互斥);`sch modify` 新增
  `--x/--y/--rotation/--designator` 快捷 flags。

## [0.23.3] - 2026-08-12

### Fixed
- **`schematic.group.move` 对 netflag/netport 半途崩溃 —— flag 改走 delete+recreate。**
  平台 `sch_PrimitiveComponent.modify` 仅限元件(「仅当器件类型为元件时允许使用该函数
  进行修改」),旧实现把 flag 与元件同锅 modify:碰到第一个 flag 即抛错中断,而平台无
  事务——**此前已 modify 的成员留在新位、其余全没动**(live 实测:R1 连续三次半搬、
  C5 与全部导线原地不动)。修复:预检阶段先把 wantIds 分类并解析每个 flag 的 recreate
  参数(identification/direction 从符号名推:power-*/ground-*/netport-bi|in|out;
  net/rotation/mirror 读实例),**任一成员不可解析(含 netlabel/short_symbol 无
  create API)→ 零变更拒绝**;执行阶段元件 modify、flag delete+createNetFlag/Port
  (rotation 经 appliedRotation 补偿负存储 build)、wire delete+recreate。result 新增
  `movedFlags`。111 单测过。

## [0.23.2] - 2026-08-11

- 版本对齐 bump(hot-reload 部署 0.23.1 修复集时占用的序号),无独立代码变更。

## [0.23.1] - 2026-08-11

### Fixed
- **`sch modify` 只 patch 顶层字段(如 supplierId)会把自定义属性整体静默清空(#175)。**
  根因:平台 `eda.sch_PrimitiveComponent.modify` 对 `otherProperty` 是**整体重写**
  语义 —— patch 不带 `otherProperty` 时平台直接把现有自定义属性清成空,而 handler
  只在 patch 含 `customAttributes/otherProperty` 时才做 read-merge-write,顶层字段
  补丁原样透传 → 166 件填好的 Value 被一条 `{"supplierId":"C..."}` 全清成 ""、
  `ok=true` 零告警(CLI 文档承诺的 MERGE 语义只兑现了一半)。修复:顶层字段补丁
  同样先回读现有 `otherProperty` 并在**同一次 modify 里原样写回**;全保住时
  `result.propertiesPreserved` + `propertiesBefore` 显式回报被连带重写的键,平台仍
  丢的键走 `partial:true` + `notApplied`(CLI 非零退出)绝不静默;现有属性为空则不
  加 `otherProperty` 键(不做无谓整体写,避开 attrs_backfill 记录过的投影键副作用)。
  验证:单测把平台整体重写语义建进 stub(不带 otherProperty ⇒ 清空)+ 4 个新用例
  (保留写回 / 空属性不写 / 平台仍丢键报 partial / 回读失败降级 verified:false),
  连接器 111 单测全过、`tsc --noEmit` 干净。
- **`export-image` 后编辑器残留「卡在 99%」进度条 —— 导出完成后主动 teardown。**
  根因:导出走 `sch_ManufactureData.getExportDocumentFile`(唯一的矢量+选区+后台
  出图路径,无替代 API),该制造数据管线会弹 BOM/器件库进度条,且**成功返回后
  平台不销毁它**——命令 2-3s 成功、文件正常,但 GUI 的 99% toast 挂着要手动关
  (实测 2/2 复现)。修复:`schematicExportImage` 的导出 `finally` 里延迟 400ms
  best-effort 调 `sys_LoadingAndProgressBar.destroyProgressBar()/destroyLoading()`
  (@public 幂等,已 debug exec 真机验证 showProgressBar(99)→destroy 可清);
  成功场景清 99% 残留,超时场景连卡 1% 的一并清。纯 GUI 清理,不改任何输出。
- **原理图布局「卡进度到 99%」间歇挂死 —— `connect_pin` 平台变异调用补单步超时。**
  根因:EasyEDA 平台 API 偶发**吞掉创建请求但既不 resolve 也不 reject**(与
  `schematicExportImage` 早已用 `withTimeout` 兜的 SCH_EXPORT「platform drops the
  request without rejecting → stuck progress toast」同一失效模式)。`schematic.power.connect_pin`
  的三个平台调用(`sch_PrimitiveWire.create` / `createNetFlag` / `createNetPort`)**没套超时**,
  那句 `await` 就永久挂住;daemon 只能在 ~18s dispatch 预算处以「connector did not respond」
  杀掉该请求,于是 `block-apply` 批量按脚布线时某一脚冻 18s → 用户看到布局进度卡在 ~99%
  (audit 实测 ≈<1/400 次 connect_pin;挂住的那次有时还迟到落盘留下 `$…N…` 孤儿网)。修复:
  三个变异调用各 `withTimeout(7000ms)`(远低于 18s dispatch,`Promise.resolve` 归一重载联合类型),
  超时即快速 reject → 流入已有的 wire 重试 / rollback 路径,batch 立即继续、不再冻 18s、不留迟到孤儿。
  连接器 107 单测全过。**注意:平台偶发假死本身仍在,本修复是把「无限挂死」降级为「快速干净失败可重试」。**

## [0.23.0] - 2026-08-10

插拔类器件特性版(用户在车机真板复测中逐条点名):打分骂什么、规划器就修什么,
两边口径锁死不分家。全部真机验证(车机 J2 Type-C 从缩板内 129mil → 齐边 →
外突 40.7mil,板 74.0→75.4 [good] 0 blocking)。

### Added
- **`pcb layout-score --part J2,U1` —— 器件聚焦视角**。整体归因是「维→器件」,
  用户真实工作流是反向的(整体打分 → 点名要优化的器件):聚焦卡汇总该件的直接
  扣分、**关联提及**(TVS 离 J2 太远,扣的是 TVS 的分但提及 J2 —— 动哪个由人定)、
  blocking、几何现状(坐标/装配面/离板边距离)。位号词边界匹配(C1 不误配 C10)。
  顺带修掉 `--all` 的数据层谎言:routable 在 scorer 内截前 12,现在 `--all`/
  `--part` 时各维保留全量归因。
- **插接面贴边规则(plug-face-not-flush,edge-io 维)**。Type-C/USB/SD/耳机口
  (PJ-3xx/3.5mm)类**水平插拔件**的器件特性:插头从板外水平进入,插接面必须与
  板边齐平甚至**外突**(off-board 判据用焊盘不用 bbox,正是为了放行外突)。
  此前 300mil 边带内一律算"在边"——车机 J2 缩板内 129mil 读成无异常,实际
  插不了。缩进按深度线性扣(≤25mil 容差齐平,外突负 gap 合法)。
- **插拔通道禁布(connector-mating-blocked)**。内部卧贴插口(FPC/内部 Type-C/
  卡座)开口**前方** 250mil×口宽的通道里不许压器件 —— 压了插头进不来。开口
  方向只认块库 openings 声明(判不了绝不猜);贴边件通道在板外天然免检。
  check 规则(WARN)+ edge-io 扣分(归因落**遮挡件**,每件 10 封顶 30)+
  规划器 T2 落位后把通道登记为占用区(卫星永不被规划进通道)。

### Fixed
- **T2 贴边对插拔类器件边距=0 齐平**。规划器原统一留 45mil 边距、且
  「edgeMargin+30 内算就位」—— 插拔件永远差一截还不被挪(车机 J2 缩板内的
  规划侧根因)。插拔类(与 plug-face 规则同一正则)贴边边距=0,就位判据同步收紧。

## [0.22.0] - 2026-08-10

PCB 布局能力重构版(#167/#168/#153):多维打分 → 精修 → 确认门的闭环 + 规划器
合法化 + 五块嘉立创开源真板校准。

### Added
- **`pcb layout-score` —— 布局质量九维打分(#167 DETECT 层)**。partition/flow-order/
  edge-io/protection/tidy/compact/rf/routable/clearance 九维各 0-100 + 逐器件归因
  (penalty=「先动谁」),硬错(短路/重叠/出板框)单列 blocking 一票否决不进加权。
  三条硬约定:skipped=「没测」≠满分;verdict 单一产出点(计数与判定绝不分家);
  归因恒等式 Σpenalty=100−该维分。`--spec` 解锁 flow-order 与 internal 连接器判定;
  `--from <dump>` 离线复现;默认只有 blocking 非零退出(`--min-score` 显式给才当门)。
- **`pcb refine` —— 打分驱动的精修环(#167 ACHIEVE 层,#153 护栏)**。读逐维归因对
  最弱维下确定性变换,每步复核:check finding 上升/分数下降/check 读不到都回滚该步
  (逐步回滚,回读证实才算 restored)。默认 dry-run;不可动集合(锁定件+已签字
  tier1/2)、位移预算超限剔除不截断。blocking 不归它管 —— 开跑即报 ⛔ 指路。
- **`pcb floorplan` / `easyeda spec validate·show` —— S0 spec 类型化(#167 KNOWLEDGE
  层)**。flow/flowAxis/modules[].kind/interfaces[].{edge,facing,internal,plugWidthMm}
  受控词表校验,拼错枚举从静默失灵变 ERROR;floorplan 按 flow 切有序带(只读)。
- **place-constrained 合法化阶段**:规划完用 layout-score/lint 同一纯核对虚拟落子
  复算 blocking,新引入的重叠/短路/出板框就地螺旋重定位、无解弃子(保原位),弃伙伴
  连带弃跟随者(FollowsID 血缘)。落点统一吸 5mil 锚点格。**车机真板 benchmark:
  规划器首次全面反超人类基线(综合 53.8→64.3,阻塞 1→0,protection +16.9)**。
- **保护件归因对齐**:跟随伙伴=打分器同规则选出的端口件(isProtectionIdent/
  isPortIdent 共享);伙伴没动就不跟;落点按「离伙伴焊盘更近」双候选择优。
- **#168 两条连接器规则**:internal-on-edge / connector-plug-clearance 进 `pcb check`
  (+插头护套包络表 `_plug_envelope.json`),与 edge-io 维共用判定;位号优先于器件名
  (USBLC6/SMAJ 不再被误判成连接器)。
- **`workflow status` 消费质量快照**:confirm-layout 落的多维快照渲染 + `--reconcile`
  逐维退化对比(掉 ≥5 分标 ⚠;scored→skipped 报「失去可测性」绝不当 0 分)。
- **金标准校准体系(#167 LEARNING 层)**:`make layout-calibrate` 正负对照 fixture +
  **五块开源真板入库**(实战派ESP32-S3 [maxBlocking=0 旗舰锚点]/庐山派K230/
  RK3568四层/MIPI扩展板/BBClaw),好板不掉分+blocking 不误报回归机械可查。
- **`pcb.outline.get` 返回板框真多边形 `points` + `outlineFormat`(#167,连接器侧)**。
  异形板「到板边距离」不再按 AABB 错算;`points`=折线中心线(铣刀路径=真板边),
  bbox 是渲染范围含线宽。多条/含弧退化 bbox 并自述原因(消费方自动回落标 degraded)。

### Fixed
- **五板校准修掉的六处度量失真**:渲染 bbox 当 courtyard(实战派S3 曾被误报 104 条
  overlap → 重叠/间距改**焊盘并集**本体代理);圆盘按矩形算(角上假铜误报短路 →
  圆-圆/圆-矩真实几何);clearance 阈值拿错规则(4mm 板框类值当装配间距 → >20mil
  退回 7.87mil 规范下限)+ 密度归一渐近曲线(好板 86-95,负对照仍必响);同网集堆叠
  豁免(装配选项/并联不可能跨网短路);连接器壳下垫件豁免(焊盘不接触只有并集相交);
  板框内切割误当外边界护栏(MIPI utilization 曾 1679%)。blocking 误报 267→11,
  好板综合分聚拢 59.9~67.7,BBClaw 哨兵全程无误杀。
- **refine 环 check 读失败保守回滚**:countGateableFindings 返 -1 时旧护栏恒放行,
  现按「无法复核=回滚」处理;refine 环本体补 8 个假 daemon E2E(dry-run/apply/
  回滚/回读漂移/tier 保护/中途失败)。

## [0.21.7] - 2026-08-09

### Added
- **`sch note` —— 电路说明文本(纯 CLI,zone-draw 同款内部 exec_js 惯例)**。
  用户反馈:agent 产出的原理图常一页塞满、无分区框、无文字说明,电气对但交付
  可读性差。Skill 布局默认升级为「分页分区+电路说明」三件套:S1 分页(每页一个
  功能域)、S2 分区框+区名(`sch zones set`→`zone-draw`)、S3 **每模块 1~3 行电路
  说明**(`sch note --text "LDO: 5V→3V3 1A\n输入/输出各 100nF" --x … --y …`,
  `\n` 换行,字号默认 10/灰色低于区名标签)。创建后回读 primitiveId 验证 + 显式
  save;`sch text-list` 枚举、`sch prim-delete` 清理。真机验证(ceshi):多行内容
  完整落盘读回。约束落点:SKILL.md 档位默认表+停点② · design-flow S3 过门条件 ·
  schematic-layout-conventions §1.5(含合格说明正反例)。

### Fixed
- **`attrs_backfill` 灌库占位 `Designator` 键灭掉全板位号 —— 根因修复(此前误诊为平台行为)**。
  一块 166 器件的板 `pcb import-changes` 后位号 166/166 全变占位符(`U?`/`C?`/`RF?`),
  最初归因于平台导入——**错**。真机控制变量二分(ceshi 六件小板)钉死真凶:裸
  `eda.pcb_Document.importChanges()` + 手动点「应用修改」位号全对(平台无辜,还顺带
  给两侧铸造 `uniqueId`);毁位号的是我们自己的 `pcb.component.attrs_backfill`
  (import-changes 自动跑):**器件库记录的 otherProperty 自带 `Designator: "C?"`
  (库自己的占位位号),merge「填空值」把它灌进实例,平台把 otherProperty.Designator
  同步成图元位号** —— 安静板上单独跑一次 sync-attrs 即 100% 复现,与时序无关,每件
  变成各自库记录的占位前缀。修法三件套:merge **剔除平台投影键**(`Designator` /
  `Unique ID` / `Name` / `Add into BOM` / `Manufacturer*` / `Supplier*` —— 它们存在
  顶层图元状态里,写进 otherProperty 要么被同步成状态(毁位号)要么被静默丢弃(报告
  年年撒谎「6/6 backfilled」且永不幂等));同一 modify **显式回传当前位号**兜底;
  **清洗**老版本已漏进实例的占位 Designator 键。修后实测:全流程 E2E 位号全真、
  sync-attrs 幂等(第二轮 0/6)、真实属性照常回填(Value/Tolerance/Voltage Rating)。
- **`pcb.components.list` 补 `uniqueId` + CLI `pcb sync-designators` 存量修复**。
  位号是这套工具链几乎所有规则的输入 —— S0 spec 模块归属、保护件前缀(F*/D*/TVS*)、
  去耦判定、`pcb check` 定位、BOM,位号一丢它们不是失灵,而是静默按错误分类算出一份
  看着正常的报告。被旧版毁过的板用 `easyeda pcb sync-designators` 修:按 `uniqueId`
  (`gge*`,平台首次 sch→PCB 导入时铸造、**跨文档同一命名空间**,实测 166/166 匹配;
  primitiveId 每个文档各 mint 一套对不上)从原理图回填,**只动占位符位号**(PCB 手工
  设过的真实位号绝不覆盖),每笔写入回读验证(平台 modify 有静默 no-op 前科),修完
  立落 `pcb.save` 检查点;`import-changes` 之后自动殿后跑(排在 attrs 同步**之后**,
  任何整包 otherProperty 写入若再毁位号都能当场修回),`--no-sync-designators` 可关。
  原理图侧位号也是占位符的件单独归类提示「先标注原理图」,不再误报成 unmatched。
- **`tagComponentPages` 页级隔离 + 前台必恢复**。此前中途任一页 `openDocument` 失败
  会整体 abort 且跳过前台恢复 —— 前台留在随机原理图页,随后的 PCB 批量写全落错前台。
  现在单页失败只跳过该页,`finally` 无条件恢复调用前的前台文档。
- **`pcb.import_changes` 后续同步只在 `confirm=applied` 时执行**(CLI 侧)。#124 语义:
  importChanges 的 promise 会在「确认导入信息」弹框**刚打开**时就 resolve true ——
  `imported=true` 不等于器件已落地。此前 CLI 拿它当门,弹框未确认时照跑同步、读到
  导入前的板面、静默空跑;现在未确认落地会明确警告并指引手动补跑。

### Added
- **`schematic.export.image` —— 选区/整页导出 SVG·PNG·PDF(#166)**。
  `easyeda sch export-image --ids '[...]' --out block.svg` 把**指定图元**单独渲染出来
  (自动选中并把画布裁到选区:实测 3 个器件 → 283×155,整页 1191×846),省略 `--ids`
  导整页;`--format svg|png|pdf`、`--scope selection|page|project`、`--page`、`--out`。
  **这是 agent「看局部原理图」的可靠通路** —— 老路子 `view region` + `snapshot --no-fit`
  依赖视口重绘,**标签页不在前台时 rAF 不触发、画布不重画,于是静默截回上一帧整页**
  (#166 报的「仍截整页」真因在此,不是 #20 回归);新命令不走视口、不需前台、不弹框,
  SVG 还是矢量。
  ⚠️ **底层 `getExportDocumentFile` 的 `object` 字面量,官方类型定义三个值全是错的**
  (`'All Schematic'`/`'Current Schematic'`/`'Current Schematic Page'`),真值是
  `'Current Page'`/`'Current Page Selected Items'`/`'Project'`(读 `sch-main.js` 源码实证)。
  传错**不报错**:内部 `Z.pureSchematics[<非法key>].sort` 抛 TypeError、空 reject 无人接,
  **外层 promise 既不 resolve 也不 reject**,编辑器卡在 1% 进度条(实测两次卡死)。
  handler 固化真值并加 30s 超时兜底,绝不再让动作队列被悬着的 promise 占死。

## [0.21.4] - 2026-08-07

### Fixed
- **(CLI/daemon) `go test` 不再把 fixture 写进用户真实审计日志(#159)**:
  `newAuditWriter("")` 兜底到 `~/.easyeda-agent/audit`,而 daemon 测试里的
  `New(Options{})` 都不设 `AuditDir` —— 每个走 `handleAction` 的测试都往生产日志
  追加假窗口 `w1`/`w2`、假工程 `motobox`(已污染 33 条,巡检时被当成真实的连日
  `schematic.components.list` 失败)。新增 `EASYEDA_AUDIT_DIR` 覆写(与
  `EASYEDA_WORKFLOW_DIR` 同款约定,**读写两侧同源** —— `audit tail`/`audit export`
  与 skill 的 `audit-baseline.py` 都认),并在 `testing.Testing()` 下**禁用**默认
  兜底路径:忘了传 `AuditDir` 的新测试再也污染不到用户日志(结构性防漏,不靠纪律)。
- **(CLI) `waitDocSettle` 对 PCB 文档不再空转 8s(#161)**:settle 探针写死
  `schematic.components.list`,`--page` 指向 PCB 时探针必然失败,而失败被吞成
  「继续轮询」→ 审计日志 **21 连发** `EDA_CALL_FAILED`、`ready` 恒 false。现在
  探针按文档类型选(`pcb` → `pcb.components.list`),守卫**下沉到探针本身**而不是
  指望每个调用方记得判断(`doc switch` 有守卫、`switchToPage` 没有,漏一个就复现);
  连续 3 次探针失败即止损。实测 21 连发 → 0,8s → 3.4s。
- **(CLI) `sch zone-draw` 框贴图纸边缘并压标题栏 keep-out(#163)**:zones 模式在
  **原始 sheet bbox** 上分格且只内缩 4 单位,底排落在标题栏 x 范围内的格直接压进
  明细表 —— zone-draw 画的框会触发我们自己的 `titleblock-overlap` 规则,而 keep-out
  几何一直是现成的(`deriveSheetGeometry`,partition 模式和 `sch check` 都在用),
  zones 模式只是没消费它。现在先按 margin(默认 20)内缩出可用区**再分格**,并把与
  keep-out 相交的框底边抬到上沿 + 8;标签移入框内不再压框线;收缩后过短的格跳过
  不画残条。真机(A4 1170×825 / keepout [468,0→1170,165]):整体由 `x[4..1166]
  y[4..821]` 收到 `x[24..1146] y[24..801]`,底排底边 4 → 173,`titleblockOverlaps: 0`。
- **`errorDetail` 不再被折叠成 `[object Object]`(#160)**:`edaError` 用
  `String(err)` 渲染抛出物,平台抛**裸对象**时根因整条丢失(实测 `debug.exec_js`
  两条只剩「有个对象」)。而 `errorDetail` 存在的**唯一理由**就是承载 `eda.*` 的
  原始抛出物(titleblock.modify 0/32 那次复盘只能靠 payload 反推)。新增纯函数
  `describeThrown`:Error→message(空则 name)、对象→`[code] message | {json}`
  (可读部分在前,截断也留得住)、循环引用标 `[Circular]`、无可枚举属性直说
  ——替换 `actions.ts` 9 处 + `transport.ts` 1 处同款三元(共享出口覆盖 140 个
  `edaError` 调用点)。离线单测 10 条。
- **原理图图元删除改走 `sch_PrimitiveObject`,文本删除终于真落盘(#164)**:
  `sch_PrimitiveText.delete()` **只从内存/渲染索引摘除,从不进持久化模型** ——
  删完 getAll 说没了、`save` 也说没了,`doc reload` 后**原 id 全部复活**。于是
  zone-draw 的「删旧+重画」实为只加不减(车机V2 P5 累积到 56 个标签)。真机定因:
  同一次 save 里新建的文本**落盘了**、删掉的文本**回来了** —— 不是没标脏,是
  delete 压根没进模型。两处订正原 issue:**矩形/导线的 per-class delete 其实是
  落盘的**(只有文本坏);**文本的 `modify` 同样被丢弃**(改内容 reload 后回退),
  等于文本一经创建就冻结。修法=页面图元统一走**通用图元类**
  `eda.sch_PrimitiveObject.delete(ids)`,它跨类型且真持久化(混批 6 文本+1 矩形
  +1 导线,reload 后全零,连历史遗留的孤儿标签一并清掉);旧平台无此类时回退
  per-class。`sch zone-draw --clear` 与创建失败回滚路径同步改走通用类。
- **`schematic.primitives.delete` 改为回读验证计数(#164)**:此前
  `deleted[key] = ids.length` **直接来自请求数**,从不回读 —— 与 `page.clear`
  修过的「把枚举数当删除数报」是同一个坑,只是没修到这里。现在删完重新枚举:
  `deleted`/`total` 只计真正消失的,有幸存则按 #151 约定返回 `partial:true` +
  `survived` + warning(画布已变不抛错),CLI 侧 `sch prim-delete` 随之非零退出。
  **同时记一条判据教训**:立即回读**证明不了持久化** —— 文本删除的立即回读一直
  是"已删",`zone-draw --clear` 那套 fail-closed 校验因此报了一路"cleared 6"、
  实则 3 个标签全在。凡涉及删除是否落盘,唯一可信判据是 **`doc reload` 后复查**。
- **跨页 `primitiveId` 不再被误报「不存在」(#162)**:`getComponentOrThrow` 只调
  活动页作用域的 `get()`,查不到就抛 `No schematic component found` —— 但
  `delete` 走全页 `getAll`,**同一个 id 同一个窗口**,`replace` 说找不到、17 秒后
  `delete` 却删得掉(审计实证:切页后同 id 即成功)。现在 miss 走一次全页判定:
  在别页 → 报出**它到底在哪页**并给 `easyeda doc switch <page>` 指引;确实不存在
  → 文案明确为「on any page」。收敛 modify / rebind / replace 5 个调用点。
- **`pcb check` silk-over-pad 误报根修(#155)**:真机裁决 —— `pcb silk-align` 的
  落点**一直是对的**(报告中心 = 实测渲染 bbox 中心,分毫不差),issue 观测的
  「实际落点偏左下半文本」是把**存储的左下角锚点**误读成落点。真凶在 check 侧:
  silk-over-pad 把 silk.list 的 x/y(左下角锚点)**当文本中心**用,判定框整体偏移
  半个文本 → 对 silk-align 验证过干净的位置误报压焊盘(「换个焊盘继续压」= 判定框
  恒在真实文本左下方)。修:`pcb.silk.list` 每条文本附**真实渲染 bbox**
  (getPrimitivesBBox),check 直接矩形相交 —— 锚点/字宽/旋转全不用猜;旧连接器
  回退近似也改为从锚点正确起箱。真机双向验收:silk-align 摆好 → check 0;故意压
  焊盘 → check 1。

### Added
- **`schematic.component.resolve_lcsc`(#158)—— 确定性「已放置器件→真 C 号」批量解析**。
  精确匹配链(实例 C 号 → MPN 严格相等 → 工程库名),匹配的**封装必须与实例一致**
  (唯一命中但封装不同 = 封装变体不符,照样 unresolved)——绝不模糊兜底
  (真机事故:裸 search("U.FL-R-SMT-1(01)") 的 r[0] 是 C1017 磁珠)。默认 dry-run;
  `apply` 把 C 号写回 supplierId 非 C 号形状的实例(整板修复一条命令,166 件场景
  免手工)。unresolved 附候选;同 MPN+封装缓存。replace/rebind 的身份解析同步加固
  (唯一命中也强制封装一致)。CLI `easyeda sch resolve-lcsc [--apply] [--page]`。
  真机验:坏 supplierId 件 via=mpn 解析+写回,已带真 C 号件 via=instance 不重写。

## [0.21.2] - 2026-08-05

### Changed
- **显示名改为 EDA Agent Connector 后重传立创市场(包名与 uuid 均不变)**。
  按市场管理规范,`displayName` 去除 "easyeda" 字样;经与市场管理员确认,内部
  包名 `easyeda-agent-connector` 与 uuid **无需改动**,原条目撤销已解除,同一
  条目重新上传即可——已装用户的原地自动更新不受影响,无需任何操作。
  README(中/英)/包内 README/install.sh 均已附更名说明。

## [0.21.1] - 2026-08-05

### Fixed
- **`schematic.read` 的 components 输出漏了 `primitiveId`(真机事故,motobox)**:
  read 只输出 `uniqueId`(`gge…`,sch↔PCB 关联键),agent 抓它当改动句柄喂给
  select / delete / modify → 全部 notFound/空选择,看似「任何器件都改不动」。
  现在每条 components 带 **`primitiveId`(16 位 hex,select/modify/delete/replace/
  rebind 的唯一合法句柄)**,uniqueId 注释明确标注「非 primitiveId」。
  真机验:place 返回的 id 与 read 输出一致,按它删除成功。

## [0.21.0] - 2026-08-05

### Added
- **`schematic.component.replace` 已随 0.20.0 发布;本版补齐它牵出的整条「器件标准化」链路**(见下)。
- **`pcb.component.attrs_backfill` / `easyeda pcb sync-attrs` —— PCB 器件属性回填**:
  平台 sch→PCB 导入只搬顶层身份字段,otherProperty **键在值空**(Value/耐压/精度/
  Datasheet/… 全 ""),PCB 侧器件标准化面板整列空白;且**原理图实例的属性值
  save/reload 后同样为空**(真机实证:place 后即读有值,重开后为空)——原理图不可作
  同步源。唯一稳定载体是 **device 库记录**:按实例 C 号(#157 回填保证是真 C 号)
  `getByLcscIds` 批量解析,只填 PCB 侧空值键(手改值优先,`--overwrite` 强制),
  全程 PCB 前台零切页。`pcb import-changes` 成功后自动跑(`--no-sync-attrs` 关)。
  真机验:import 后 PCB 件 Value/耐压/精度/Datasheet 等 9+ 字段从空到齐。
- **`schematic.text.list`(#156)—— 只读枚举激活页全部文本图元**(id/content/x/y/
  rotation/fontSize/color),配 `sch prim-delete --ids` 清理孤儿 zone-draw 标签,
  不再需要 `debug.exec_js` 逃生舱。页懒加载定律适用(只见激活页);CLI
  `easyeda sch text-list [--page <p>]`。真机验:建孤儿标签 → text-list 枚举 →
  prim-delete 清零。

### Fixed
- **放置器件 supplierId 默认成 subPartName(`<MPN>.1`)而非真 C 号(#157)**。
  平台 `sch_PrimitiveComponent.create` 的原生行为,导致官方「器件标准化」面板全板
  标红、BOM Supplier Part 不可下单。现在 place / replace / rebind 重放置后自动从
  device 库记录回填**真立创 C 号**(`^C\d+$` 才写;device 无 C 号的外采占位件不动;
  回填失败降级 warning 不阻断)。place 结果新增 `supplierIdBackfilled` 字段。
  真机验:place C1525 → 实例 supplierId=C1525(此前=CL05B104KO5NNNC.1)。
- **rebind(换封装/换符号)对系统库器件从未工作过,现已修复**。三层根因真机逐一定位:
  ① 绑定链用的 `getState_Component().uuid` 是 16 位符号实例 id,`lib_Device.modify/create`
  一律拒收 → 复用 replace 的 `resolvePlacedDeviceIdentity`(C 号→MPN→工程库名)解析真
  32 位身份;② **系统库 device 记录平台层面只读**(32 位 uuid 也 modify=false)→ 新增
  **个人库克隆回退**:复制/复用同名副本 → 副本绑新封装/符号 → 重放置,结果
  `mode='cloned-to-personal-library'` + `clonedDevice`,回滚删副本;③ 克隆的 copy 与
  modify 都会弹「符号/封装另存为」冲突框且 **promise 挂死到有人点击**(#124 家族),
  而后台 tab 的 setTimeout 轮询被 Chrome 节流到 ~1次/分 → 改用 **MutationObserver
  自动点「确认」**(默认选项「使用已有的库」即所需语义),护航整个克隆段。
  全链 sys_Log 埋点(`[rebind]`)可回读诊断;CLI rebind/replace 超时提至 90s
  (串行库链在线搜索+克隆最坏情况超默认 20s)。ceshi 真机:系统库电容
  C0603→C0805 换封装 4.1s 完成,位号保留,弹框自动确认(dialog clicks: 1)。

## [0.20.0] - 2026-08-05

### Added
- **`schematic.component.replace` —— 整器件替换(换型号)**,官方「器件标准化」面板
  「使用推荐器件」的 API 等价物(该面板仅暴露打开面板的枚举,零数据/操作 API)。
  官方无 rebind-device 原语,走 delete → 同位姿 create → 恢复位号 + uniqueId
  (保 uniqueId 使 sch→PCB import-changes 走 UPDATE);器件身份字段(name/制造商/
  供应商/LCSC)**不带过去**,跟新 device 走,`keepProperties` 才连带旧自定义属性。
  目标三选一:`lcsc`(须唯一)/ `deviceUuid+deviceLibraryUuid` / `query`(须唯一命中);
  delete 后失败回滚重建原器件(含完整身份)。返回 **pinDiff**(同位姿按 pinNumber 对比
  removed/added/moved)——非空即旧导线对不上,须重接线并 `sch drc`/`sch check`。
  CLI:`easyeda sch replace --id <pid> --lcsc C14663`。真机验证(ceshi,双向
  C14663↔C1525 + same-device 拒绝)并顺带修复两坑:回滚身份不再信 16 位的
  `getState_Component().uuid`(`resolvePlacedDeviceIdentity` 走 C 号→MPN+封装名解析
  32 位真身份,解析不出动画布前 abort);响应改用 modify 返回的图元序列化
  (create 对象立即回读是恢复前的陈旧值)。

### Fixed
- **manifest 全面去 "easyeda" 字样 —— 治市场下架**(管理员原话:「发布者信息和扩展名称
  请移除easyeda」)。`publisher` `easyeda-agent`→`zhoushoujian`;`name`
  `easyeda-agent-connector`→`eda-agent-connector`;`displayName` →"EDA Agent Connector";
  description 与编辑器菜单标题同步改写;仅保留 GitHub 仓库 URL(事实链接)。
  **uuid 不变**,已装用户仍走原地更新;v0.19.0 GitHub Release `.eext` 资产已替换为修正版
  (回验:包内除 URL 外 0 处 easyeda 字样)。打包产物文件名随 name 变为
  `eda-agent-connector_v*.eext`(Release 资产名保持 `easyeda-agent-connector.eext`
  不变,install.sh 依赖它)。


## [0.19.0] - 2026-08-04

> 🙏 感谢 [@NeoSpecies](https://github.com/NeoSpecies) 的 [PR #154](https://github.com/zhoushoujianwork/easyeda-agent/pull/154)
> —— 首个外部贡献:定位并修复了「同窗口重复激活互踢 socket」的重连风暴,并带来了 MCP stdio 适配层。
> 本版连接侧的三层修复(activation-scoped id / 失败轮换 / 端口记忆)正是在这个 PR 的基础上叠加完成的。

### Added
- **`pcb silk-zone-outline` —— 按模块在丝印层画区域轮廓 + 区名标注**:读模块内器件的
  真实渲染矩形生成正交阶梯包络,避让穿越轮廓的外部器件,自动选不压器件的位置放区名标签。
- **`sch gate` —— S5 校验门收成一条固定流水线**:`layout-lint → check → bridge-check → drc`
  按固定顺序跑完出一张报告,verdict 三态(`pass`/`fail`/**`blocked`**——检查器没跑成 ≠ 板子有
  问题,报告直接指向 health/doc 修复而不是让 agent 改电路);每个失败 stage 按**实际失败原因**
  带规定的下一步(`blockingReasons`),check 的阻塞项直接给规则类型直方图;`--json` 是四个
  单命令 JSON 的超集(`stages[].detail`)。四个单命令保留作局部复查,交付门走 gate。
- **daemon 对失效 windowId 自动重定向 + 错误分型**:页面刷新会换 windowId,旧 id 的调用
  此前被误报成 `NO_CONNECTOR`「没有连接器」。现在 daemon 记住退役窗口的稳定身份
  (documentUUID 最强判据/project 兜底),自动路由到接替窗口并在 warnings 里告知新 id;
  无法重定向时报 **`STALE_WINDOW`**(明说连接器是好的,列出在线窗口,建议 `--project`)
  或 **`AMBIGUOUS_WINDOW`**(列候选),`NO_CONNECTOR` 只留给真没连接的情况。
- **`scripts/audit-baseline.py` —— 暴露面健康度离线体检**:读 audit log 出「调用分布+失败率 /
  错路回退 / 逐日多样性」三张表;首测揪出 `titleblock.modify` 32 次调用 0 次成功。
- **新增本地 stdio MCP 适配层**(来自 PR #154,@NeoSpecies):`mcp/` 将现有 `easyeda` CLI/daemon 的连接健康、action
  发现、7 个安全 action domain、电路块和 guarded workflow 暴露为 `easyeda_*` tools。
  MCP 不直连 EasyEDA、不绕过 typed action/审计/workflow gate,并明确不暴露任意
  JavaScript 的 `debug.exec_js`;mutation 仍要求同时提供 project 与 doc。

### Fixed
- **修复 EasyEDA Pro 3.2.175 重复激活同一扩展时的永久重连风暴**(来自 PR #154,@NeoSpecies):旧连接器的所有激活
  实例共用固定 host WebSocket id,会互相 close/register 同一 socket,表现为交错 heartbeat、
  windowId 持续变化、`AMBIGUOUS_PROJECT` 和 action response 丢失。现在每次激活生成独立
  socket id;daemon 仅在 project/document/type/tab 四项完整且完全相同时把连接视为 transport
  duplicate,并路由到最新连接。真实不同 tab 或身份不完整仍保持 ambiguous,不会静默误路由。
- **连接器卡死后能自己爬回来:扫描连续失败会轮换 websocket id**(叠加在同版
  activation-scoped id 之上)。`eda.sys_WebSocket.register()` 在同 id 连接仍被 EasyEDA 视为
  "active" 时会**静默忽略新的 url/callback**(pro-api-types `index.d.ts:21025`)。
  activation-scoped id 解决的是「同窗口多激活互踢」,解决不了「**本激活自己的 id 被判为
  active 后 register 全被忽略**」——连续 `WS_ID_ROTATE_AFTER_FAILED_SCANS`(2)轮全端口
  扫描失败后换成 `<base>-r<n>`,全新 id 在 EasyEDA 侧没有记录,register 必然生效。
  happy path(daemon 在线、首轮即连上)始终用基础 id 不受影响;daemon 真不在时轮换也无副作用。
  **真机 soak 实测(2026-08-04,web 编辑器;停 daemon 后不关 tab、不 reload,看能否自愈)**:

  | 版本 | 停 45s | 停 60s | 停 75s |
  |---|---|---|---|
  | 仅 activation-scoped | ✅ 5s | ❌ 210s 未自愈 | — |
  | + 轮换 | ✅ 5s | ✅ 5s | ❌ 120s 未自愈 / 同条件重测 ✅ 90s |

  **两者都只是改善概率,都没根治** —— 真实形态不是「永不恢复」而是「**恢复耗时不可预测**」
  (5s / 90s / 210s+)。根因待查,而排查被下面这条可观测性缺陷挡住。
- **连接器诊断日志此前在最需要时不可见,现改走 `eda.sys_Log`**:`diag()` 原本只经
  WebSocket 发给 daemon —— **断线时恰好发不出去**,而断线正是唯一需要它的时刻;这正是
  重连 bug 长期只能黑盒试的结构性原因。排查中实证了两件事(EasyEDA Pro 3.2.175):
  ① **扩展里的 `console.*` 是死代码** —— 沙箱发给扩展的 `console` 每个方法都literally
  是 `()=>{}`(经 `_EXTAPI_SCRIPT_SPACES_[uuid].console.log.toString()` 验证),所以先前
  想补的 console 输出一行也到不了 DevTools;② 沙箱把 `window`/`document`/`localStorage`/
  `indexedDB`/`postMessage` 一律置为 `undefined`,**`eda.*` 是唯一的对外通道**。
  因此主 sink 改为 `eda.sys_Log.add()`:断线时照写、用户在编辑器「日志」面板直接可见、
  且 `eda.sys_Log.sort()` 可编程读回 —— 这才让离线 soak 变得可诊断。WebSocket 发送保留
  为在线路径。同时在每轮扫描起手打 `scan start session/retryCount/wsId`。
  **首次拿到断线期日志即暴露真问题**:一轮全端口扫描要 **18 秒**(每个端口都死等满
  `CONNECTION_TIMEOUT_MS=1500`,说明 `register()` 对连不上的端口**不回调失败**、只能靠
  超时兜底),叠加 `retryCount>5` 后的 10s 慢重试 → 一个周期 ~28s。这就是「恢复耗时
  不可预测」的直接来源,也是下一步要收的口子。

- **重连不再每次都全端口重扫:优先重试上次成功的端口**(`scanOrder`)。断线期日志显示
  一轮全扫要 ~18s,因为 `eda.sys_WebSocket.register()` **从不报告"连接被拒"**,每个空端口
  都得烧满 `CONNECTION_TIMEOUT_MS`。而重启的 daemon 实际上总是重新绑到同一个端口
  (它按序取范围内第一个空闲端口),所以先试上次成功的端口能把常见重连从「整轮扫描」
  变成「一次尝试」。4 个单测钉住顺序、去重、越界历史值与全覆盖。
  ⚠️ **同期试过把 `CONNECTION_TIMEOUT_MS` 1500→600 加速全扫,实测是负优化并已回退**:
  soak 从 `45s✅/60s✅/75s❌` 退到 `45s✅/60s❌/75s❌`,有一轮扫到 `session=58` 仍未重连。
  更快的扫描 = 单位时间内 close()/register() 循环翻倍,而 `REGISTER_DELAY_MS` 那 200ms
  的 id 释放窗口本就紧张 —— **瓶颈是 EasyEDA 那张共享 socket 表的状态机,不是延迟**,
  加速只会喂大它输掉的那场竞争。理由已写进常量注释,免得下次有人再"优化"一遍。

- **`sch gate` 真机验出的三个报告缺陷已修**:strict 档下 summary 缺判据字段导致「全 0 却
  FAIL」自相矛盾;blocker 从 error 计数拼出「0 个阻塞项」却判失败;建议按 stage 名给,
  0 bridge 却教「拆真短路」。现在 status/blocker 均由具名 `blockingReasons` 决定,建议按
  失败原因关键词匹配,绝不把 agent 指向板子没有的问题。
- **`schematic.titleblock.modify` 从「32 次调用 0 次成功」修到有回读验证** —— 这条是
  audit log 离线体检(`scripts/audit-baseline.py`)抓出来的:该 action 历史上被调用 32 次,
  **成功 0 次**,却一直挂在 skill 文档里。根因不在我们的调用姿势,而在旧实现**直接透传平台的
  `ok`**:官方 @beta remarks 原文写着「任何无法识别的明细项将被忽略」且「如若存在无法识别的
  明细项但程序并未出错,将返回 `true` 的结果」——**这个 API 对写不进去的字段报成功**,与
  「删除 API 撒谎」同族。于是「改了个根本不存在的明细项」会被报成功,而真正抛错的那 20 次
  (payload 是拿 `Size`/`Width`/`Height`/`Page Size` 当纸张属性写)既无法证伪也说不出原因。
  现在走 #151 的三态契约:**改前快照 → 写 → 回读逐项比对**,产出 `applied` / `alreadySet` /
  `notApplied` / `unknownKeys`。全部落空 ⇒ ERROR(附「先跑 titleblock-get 看可用 key」的指路);
  部分落空 ⇒ `partial:true` + warnings + CLI 非零退出(已写进画布的子集是既成事实,`ok:true`
  让 autosave 照常落盘);回读不可用 ⇒ `verified:false` 而**绝不**降级成 `ok:false`。
  `alreadySet`(改前就等于期望值)不计入 `applied`、**也不豁免全失败硬门**。
  `unknownKeys` 是本 action 特有的诊断:那些根本不是明细项,**修法是换 key 而不是重试**——
  明细表改不了纸张尺寸。SDK 显式返回 `false` 也不再被吞。9 个单测覆盖上述每一条。
- **audit log 记录 `errorDetail`** —— 协议 `Error.Detail`(连接器捕获的平台原始报错)此前
  被丢弃,日志里只剩我们自己的包装文案(如 "Failed to modify schematic page title block."),
  事后定位根因无从谈起:上面那条 titleblock 的调查只能从 payload 反推。现在原始错误一并落盘。
## [0.18.3] - 2026-08-01

### Added
- **`pcb layout-lint` 新增跨网短路检测(`short` ERROR)—— 从「靠太近」升级到「这两网短路」**:
  两器件 bbox 相交时进一步比**焊盘铜皮矩形**,若两块铜在**共享层**上真的压在一起且**分属不同
  网络**,报 `ERROR short  C2.1[VBAT_RAW] ↔ D2.2[SW1_NODE]` —— 与 KiCad 的 `shorting_items`
  对齐(此前同一份数据上 KiCad 定性成短路,我们只报几何重叠)。short 与 overlap 同级致命
  (`ok=false`、score 归零、verdict `short`、`--gate` 直接失败)。**短路按焊盘层判而不是装配面**:
  两个异面 SMD 焊盘永不短路,但**通孔焊盘(层 12=multi)贯穿所有层**,能跟对面焊盘真短 ——
  这是唯一不吃「同面才比」规则的地方。焊盘取不到尺寸(多边形焊盘)或无网络时**跳过而不猜**。
  与下面的层感知一样**没有用到新 API**,现有 `pcb.components.list --include-pads` 的数据就够算。

### Fixed
- **`pcb layout-lint` 的 overlap 判定不再「层盲」,双面贴片板上的数字终于可信**:它比的是
  **不分层**的渲染 bbox 两两相交,于是**顶层器件与底层器件落在同一 XY 也被判成 overlap**,
  而那在物理上是完全合法的顶底对穿。真实案例:box-v2 rev-a(166 器件 / 642 焊盘,85×45mm
  四层双面贴片)跑出 **116 条 overlap**,人工按层分组重算的真值是 **0**,该项目 README 只能
  专门写一段警告绕开这个数字。对照实验(本机 KiCad 10.0.1 实跑):两个 `C_0805` 放完全相同的
  XY、一个 F.Cu 一个 B.Cu,`kicad-cli pcb drc` 报 0 violations —— KiCad 比的是**分层**的
  courtyard(`F.CrtYd`/`B.CrtYd`),天生按层分组。现在器件按**装配面**(`pcb.components.list`
  本就返回的 `layer`,1=顶 2=底)分组后才两两比,**overlap / tight spacing / 手焊烙铁通道**
  三项同改(底面邻居堵不住顶面的烙铁);未知面(`layer` 缺失)保守地与两面都比,缺字段永远
  不会**掩盖**真重叠。每条 finding 带 `side`,报告头带 `sides` 分布(如 `[bottom 134 / top 32]`)。
  同板复跑:**overlap 116 → 0、tight 7 → 3**,与人工重算真值一致。
- **安装脚本撞上 GitHub API 限流时不再只丢一句 `Could not determine latest release
  version`**:`install.sh` 解析 latest release 走匿名 `api.github.com`(每 IP 每小时 60 次),
  公司出口 / NAT / CI 很容易撞满并拿到 `403`。现在(1)有 token 就带上 —— 依次取
  `GITHUB_TOKEN` / `GH_TOKEN`,都没有则回落到已登录的 `gh auth token`;(2)新增
  `EASYEDA_VERSION=<tag>` 直接锁版本、完全跳过 API 查询(裸 `0.18.2` 会自动补 `v`);
  (3)失败信息可执行 —— 403/429 明确说是限流并给出「设 token / `gh auth login`」与
  「`EASYEDA_VERSION=<tag>`」两条出路,401(token 失效)、404(无 latest release)、
  网络不可达也各有独立提示。README(中/英)与 `docs/quick-start.md` 同步补充。

## [0.18.2] - 2026-07-29

### Fixed
- **安装脚本更新 skill 不再产生 `.bak` 备份、污染 skills 目录**:`install.sh` 的
  `install_skill_to` 从「先 `mv` 成 `easyeda-agent.bak.<时间戳>` 再拷新版本」改为
  **干净替换**——检测到本地 skill 与 release 不一致时直接 `rm -rf` 旧目录再 `cp` 新版本，
  不再在 `~/.claude/skills` / `~/.codex/skills` 下留一堆过期备份副本（备份目录会被 skill
  加载器扫到、误加载旧内容）。需要保留本地改动的用户仍可用 `EASYEDA_SKILL_PRESERVE=1`
  跳过覆盖。
- **多页原理图功能框/标题不再串页或只留内存**:`sch zones` 认领与 fixed/partition/
  autolayout 三条绘框路径统一按 documentUuid 持久化（旧 `schZones`/单 frame 记录仍可读）；
  fixed 模式开放 `--font-size`（默认 14pt）。重画/清除会回读 rectangle/text ID，任何 survivor
  都失败并保留恢复记录；创建数量须 1:1，部分创建自清理；成功画/清均显式校验
  `schematic.save(saved:true)`，避免孤儿文本和跨页误删。

## [0.18.1] - 2026-07-28

### Added
- **电路块库吸收 M5Stack StickS3(K150)—— 11 个新块 + 2 个新类目**:从官方公开原理图
  (V0.6)对标提取,填补库里完全缺失的品类。新增 `audio`(`es8311_codec_i2s` /
  `aw8737_classd_spk` / `mems_mic_analog`)、`display`(`st7789_spi_lcd_btb`)两个 category
  (schema enum + `validate.go` 同步),以及 `esp32s3_pico_native_usb`(🔥 S3-PICO 原生 USB
  下载,无 CH340 桥)、`bmi270_imu_i2c`、`ir_txrx_remote`、`sy7088_boost_5v`、
  `lgs4056_liion_charge_path`、`i2c_isolation_2n7002dw`、`usbc_dual_orientation_data`。
  standard-parts.json 补 25 个新料。**全部经真机验证升 `verified`**:deviceUuid/LCSC
  100% 解析(`lib search`+`by-lcsc`)、`--probe` 刷真实符号脚、`blocks-pin-audit` 0 fanout/
  0 missing、`sch block-apply` 孤立单放 reconciled + `bridge-check` 0/0/0。多层校验抓修一批
  静默错接:`USB_D±`实为模组脚 `GPIO20/19`、BMI270 真符号脚 `SCX/SDX`(图片是 SCx/SDx)、
  mic 供电 R52=100R 非 100K、ESP32-S3-PICO **无 GPIO47/48 脚**(电源脚多名/地=EP)、
  AXE512127D 连接器纯数字 pad、双 MOSFET 库符号为单管(CJ3439 拆两放置)。conventions 侧吸收:
  `pcb-layout-conventions §7.10`(StickS3 第 4 块对标)、`design-decisions`(USB 架构/自动
  下载各加「纯原生 USB 无桥」第三选项)、`docs/board-absorption-sticks3.md`(含 PY32-PMIC
  多域电源架构参考)。

### Fixed
- **autoconnect 改用方向感知的真实 marker 外形，消除“规划通过、落图重叠”**:旧规划器把
  netport/netflag 一律近似成端点居中的 `24×11` 方框，既低估 31-unit 长端口，又虚构端点
  后方占位，导致密集引脚处错判。现在按 netport/ground/power 家族和四个方向使用真机标定
  bbox，打分与批内占位共用同一预测器；StickS3 外设页实测 marker overlap `17→0`，
  45-pin canonical topology 修复前后哈希完全一致。
- **模板布局链路 fail-closed，不再把“散件摊开”误报成已完成**:`components.list` 提供
  页级 connectivity summary 与 pins 证明；模板引擎遇到活动连接或缺失几何即拒绝运行，
  应用过程支持事务回滚、回读与保存校验，并以 strict layout/check 门收尾。本次 StickS3
  外设页按 LCD / IMU / 按键三块重排后，器件、图签、引脚、分区冲突均为 0。
- **`sch block-apply` 位号前缀映射缺 `mosfet`/`sensor`/`mic` 命名空间**:StickS3 吸收引入
  这三类新料后,block-apply 因 `bapPrefixes` 无对应前缀而硬报错拒放(设计上不猜前缀免误配)。
  补 `mosfet→Q` / `sensor→U` / `mic→MK`(IEEE-315 类),受影响的 6 个块(BMI270/IR/音频/
  I2C 隔离/锂电充电/SPI 屏)现可正常孤立单放并验证。
- **`schematic.component.modify` 自定义属性静默 no-op**:CLI 文档使用
  `customAttributes`,但 EasyEDA SDK 实际只接受 `otherProperty`,导致命令返回成功却不更新
  `Value` 等属性。连接器现在将兼容别名转换为 SDK 字段、与现有属性合并后写入,并用新器件
  句柄逐字段回读验证;平台再次静默忽略时会明确报错,不再假报成功。(#150, 感谢 @zhiqiangme 贡献)
- **`schematic.component.modify` 部分应用丢 autosave(#150 的回读门收尾,#151)**:
  回读门从「任何字段未生效即抛错」改为**分级语义** — SDK 部分应用时抛错会让 daemon
  (只对 ok:true 排 autosave)跳过落盘,已写进画布的子集只留内存、重试面对部分变异的
  文档。现在:全部生效=ok;**部分生效=ok + `result.{partial,applied,notApplied,
  propertiesBefore}` + warnings**(已应用子集照常 autosave,`propertiesBefore` 支撑
  重放恢复与审计 before/after,CLI `sch modify` 非零退出保住错误信号);纯属性补丁
  0 字段生效仍报错(画布确未变,回读铁律不倒退)。另:**未知顶层 patch 键前置拒绝**
  (逐字抄自 pro-api-types 的 modify 14 键签名,SDK 对未知键静默丢弃、事后无从归因);
  回读值比较 String() 强转容忍(平台把数字属性规范化为字符串,不再误报 partial);
  modify 成功后**回读通道本身失败**降级为 `verified:false` + warning 而非抛错
  (250ms 重试一次;抛错同样会丢 autosave)。CLI 侧新增连接器 warnings 的 stderr
  choke-point 渲染(对齐 staleRisk/concurrentWriter)。对抗性评审加固:`applied`
  只计**回读可证明**的写入(期望值与原值本就相同的键归 `alreadySet`,不豁免
  全失败硬门——否则「一个已满足键+其余全丢」可绕过假成功检测);**新增键**
  merge 语义下重放 `propertiesBefore` 删不掉,结构化暴露为 `addedKeys` 且文案
  如实说明;partial 警告文案带 primitiveId(CLI 全局按文本 dedup,不同组件不
  互吞);`propertiesBefore` 三条返回路径全带(审计 before/after);playbook
  重放(`easyeda apply`)与 `sch no-connect` 同步接 partial 失败门;
  `schematic.page.rename` 的降级 warning 同步提升到顶层 warnings(吃到 stderr
  渲染,`result.warning` 保留兼容)。

## [0.18.0] - 2026-07-22

### Added — `sch check` 三条几何 marker 规则 (issue #146/#147/#148)
- 电气检查(连接器 `schematicCheck`)看不见纯几何缺陷。新增三条 **Go 侧**规则,消费
  `components.list --include-bbox` 已流入的真实 bbox/锚点(零连接器改动、纯离线单测可验收):
  - **`duplicate-net-marker`**(#146):同类型+同网+量化锚点的重合 netflag/netport ≥2 报 WARN,
    finding 带全部 `primitiveIds` + `suggestKeepId`/`suggestDeleteIds`,直接喂 `sch prim-delete`;
    量化容忍 1384.9999 类浮点漂移,跨页/跨网/跨类型不误合并。
  - **`titleblock-overlap`**(#147):part/marker bbox 正面积侵入 A4 图签 keep-out。
  - **`marker-overlap`**(#148):marker×part / marker×marker 正面积相交(part×part 是 layout-lint
    的活),`--overlap-eps` 调噪声下限(默认 0.5),抑制已被 duplicate 规则报过的重合对。
  真机验证:ceshi P1 真实板报出 28 组 marker 重叠(含平行同侧端口 31×1 天然相交)。

### Added — 数据驱动 A4 分区规划器 `sch zone-plan` / `zone-draw --mode partition` (issue #149)
- 固定九宫格 `zoneRect` 表达不了「按整纸切功能区 + 右下图签留缺口」的行业版式。新 planner
  从活体几何求解:usable sheet(减 margin)按**模块 bbox 之间的自然空隙**切列/行分割线
  (edge-gap 而非中心 midpoint——高模块跨过中心线会跑出分区;空隙需 ≥ gutter),每个分区抬到
  图签 keep-out 之上、顶部留 title band 放大字号区名。模块 bbox = `sch zones` 认领各件 live bbox
  并集。`zone-plan --json` 落盘前出方案 + validation 五项(sheetOverflow/partitionOverlap/
  titleBlockHits/moduleOutsideZone/labelCollisions)。partition frame id **按 documentUuid 分页
  持久化**(`SchZoneFrameIdsByPage`,逐页 redraw/clear 不串页)。真机 validation 全 0 + 落笔 bbox
  与 plan 一致。纯函数 `planPartitions` 用 issue 真实 6 模块 A4 数据单测。

### Fixed — autoconnect 标题栏硬约束 + 创建后兜底 + partial + 真实尺寸 stagger (issue #146/#147/#148)
- **标题栏软惩罚→硬拒绝**(#147):`scoreCandidate` 里图签命中从 `costTitleBlock=10000` 改
  `costHardReject`,四方向全进图签时明确失败,否则主动转向安全方向(不再把 netport 落进明细表)。
- **创建后真实 bbox 兜底**(#147 DoD2):批量后回读真实 bbox,凡侵入图签 keep-out 的 marker 连
  wire 一并删除并判失败——绝不「返回成功且图签上留 marker」。
- **partial 报告**(#146):`acReport` 加 `partial`/`succeeded`/`failed`,批量中断后只补失败 pin,
  不重放整份 spec(避免在已连 pin 上叠重复标识)。
- **真实尺寸 stagger**(#148 Phase-2):打分用的 `labelBox` 从固定 8×8 换成贴近实测的 marker 框
  (~24×11);旧 8×8 在 10-pitch 平行脚永不与邻居相交、stagger 从不触发,真实 11-高框相交 →
  规划器自动挑不同 offset 错开。真机 ch340c:相邻 GND 脚 offset 交替 18/36,marker-overlap 24-28→9。

### Fixed — A4 图签 keep-out 只圈住右半的根因 + A4 优先标定
- **根因**:`deriveSheetGeometry` 的 A4 横版比例只有 `{0.22,0.14}` → keep-out `(912.6,0)..(1170,115.5)`
  只覆盖图签**右半的日期列**,左半(原理图/Schematic1/Board1/ceshi)全露在外。所有吃这个 keep-out
  的检查(#147 硬拒绝/门、#149 分区抬升、#141 避让)一致误报「0 覆盖」而真实图签左半持续被压。
- 真机 overlay 标定实测真实图签 ≈ 宽 60%×高 20%,改 `{0.6,0.2}` → `(468,0)..(1170,165)` 完整框住整表。
- **A4 优先**:图签是**定尺寸表格**不随页面等比缩放,故该比例只对 A4 准。新增 `isA4LandscapeSize`,
  非 A4 A 系列横版(A3+)仍给 best-effort keep-out 但**降级 `source:fallback-ratio` + warn「calibrated
  for A4 landscape only」**,让非 A4 的 keep-out 永不被当硬门静默信任。

### Fixed — 批量删除静默失效:`sch clear` 一直在谎报清空
- **根因是平台的 `eda.sch_Primitive*.delete(ids)` 在大批量上静默 no-op 并返回 `true`。**
  实测同一页:1/5/20/50 个 → 全删掉;58 个 → 全删掉;**134 个一次调用 → 一个没删,返回值照样 `true`**。
  是**尺寸上限**而非「某个图元删不掉」——那次失败的 134 里剩下的 58 个单独删就干净。
- **`schematic.page.clear` 此前把「枚举到 N 个」当成「删除了 N 个」上报**:一次 232 图元的 clear
  报 `total: 232` 成功,实际只删掉 5 个、227 个存活。调用方合理地把成功读成「页面已空」,
  于是一个逐块回归脚本在 clear 之间静默累积了三个块的元件,污染了整轮测量数据。
- 三处修复:① `deleteSchGroup` **分批 50/批**(clear 与所有图元类删除共用);
  ② `schematic.page.clear` 改为 delete→**重新枚举**→循环收敛(上限 6 轮,无进展即停),
  并诚实上报 `enumerated` / `total`(真正消失的数量)/ `remaining` / `passes`,没清干净时带 warning;
  ③ `schematic.component.delete` 同样分批,并**回读校验**,返回 `requested` / `removed` / `survived`
  而不是那个会撒谎的布尔。真机验证:538 图元一次 clear → `passes: 1, remaining: 0`。
- **通用教训**:平台的删除类 API 返回值不可信,批量操作后必须回读确认。

### Added — `sch extract-layout` 反向导出块模板 (issue #140)
- **`easyeda sch extract-layout <block-id>`**:block-apply 模板步骤的**逆操作**——读活动页上一个摆好的
  块实例,按每个 role 的实测 anchor + rotation 反算出块的 `schematic_layout` 模板(role→{dx,dy,rotation},
  相对确定性锚点,自动吸 5 格、rotation 归一到 {0,90,180,270})。把「在真板上把外设摆漂亮一次→固化模板」
  从肉眼盯板手写 dx/dy 变成数据管线(20 块里 18 块还缺模板就是因为纯手工)。**两个设计决策确定性落定**:
  ① role→designator 用 `--role ROLE=DESIG` 显式(优先)+ `--from D1,D2,…` 按**唯一前缀**自动匹配,歧义/未命中
  直接报错绝不瞎猜;② 锚点=按 role 名排序后首个前缀为 "U"(芯片/MCU)的 role,否则排序首个 role,重复导出稳定不漂。
  **v1 只 PRINT 模板 JSON 供复审**(不写回 go:embed 数据——回写要保 JSON 键序,Go map 序列化会打乱全部键→不可读 diff),
  复审后粘进 `internal/blocks/data/<id>.json` 再跑 `go test ./internal/blocks/...` 过全 role 覆盖 + on-grid + 合法
  rotation 校验。纯函数单测覆盖(前缀提取/rotation 归一/role 反查歧义与缺失/相对偏移计算);`--write` 就地回写待
  保序写入器就位后加。真机 extract→回写→新页 block-apply→layout-lint 一致性待活体编辑器验收。

### Added — 分区自动画框接入自动放置流程 (issue #142)
- **`sch autolayout --engine template --apply` 落完自动 `zone-draw`**:此前功能分区可视化
  (虚线区域框+区名)是 `sch zones set` + `sch zone-draw` 两步手动跟进;现在模板引擎 `--apply`
  成功后,按 `--spec` 的 `modules[].zone` 自动持久化分区认领(`SetSchZones`,`sch zones status`/
  layout-lint 可见)并用**与 zone-violation 同一 `zoneRect` 几何**画出区域框+区名——「先看区、
  再看线」成为放置流程一等公民。复用既有 `buildZoneDrawJS`,frame primitive id 记入 workflow
  state,`sch zone-draw --clear` 精确移除、不碰用户图形。新 flag `--zone-draw`(默认开,
  `--zone-draw=false` 关闭);无 sheet bbox / 无 zoned module 时静默跳过,best-effort 不影响布局。
  纯函数单测覆盖(`TestBuildAutolayoutZoneClaims`);真机「多块页自动出现分区框」待活体编辑器验收。
  block-apply 的 category→zone 自动分区 + 去耦分组文本注释(需先定 category→zone 映射/`default_zone`
  方案)留作后续,不在本次范围。

### Fixed — `--engine official` 兜底增强 (issue #143)
- **`connect_pin` 网格判定容忍浮点残差**:吸附把件 anchor 修到整 5 格,但引脚坐标 = anchor +
  旋转偏移,旋转数学引入 FP 噪声(引脚落 649.9999999 而非 650),旧的严格相等网格判定把合法引脚
  误判为 off-grid 而拒连(官方 `--rewire` 残留 ~7 条失败连接)。现引入 `GRID_EPS=0.01`:先 round
  到最近格、与网格点距离 <0.01 即视为 on-grid;并把**桩的引脚侧顶点吸到整格**(`pinGX/pinGY`),使
  短桩真正轴对齐(0.0001 的斜桩会让 flag 悬空/EasyEDA 拒建)同时仍 <0.01 贴合真实引脚→照常连通。
  `GRID_EPS` 远 < 半格,真正 off-grid(半格 2.5)仍正确拒绝。顺带修好 `offset==0` 重叠检查(旧逻辑
  在 FP 引脚下漏判)。
- **官方 `autoLayout` 喂 `designatorDeviceTypeMap`**:此前裸调无参;现从页面真实位号前缀分类
  (R→resistor / C→capacitor / L·FB→inductive / D·LED→diode / Q→triode / Y·X→oscillator /
  U·IC→chip / 其余→otherDevice)构建 designator→type map 注入,官方算法按角色更聚拢地摆放。空 map
  降级为裸调;传多余 props 对忽略它的旧构建无害。Go 单测覆盖分类器 + map 构建;真机连通率/布局效果
  待活体编辑器验收。

### Fixed — block-apply 消费标题栏 keep-out (issue #141)
- **`sch block-apply` 原点避碰纳入 A4 标题栏图签**:此前 `bapResolveOrigin` 只把已有器件的
  真实 bbox 当障碍,块落在纸面偏右下会压到 A4 右下角图签/明细表。现在从 `bapResolveOrigin`
  的同一次 `components.list(includeBBox)` 里顺手取 `"sheet"` bbox,复用 `titleBlockKeepout`
  (autoconnect/autolayout 共用的 keep-out 单一几何源)派生标题栏矩形,追加进螺旋找空位的障碍集
  ——不显式 `--at` 时自动避开图签;显式 `--at` 仍尊重坐标但碰撞(器件或图签)会 warn。无 sheet
  bbox 时降级为不强制(与其它调用方一致)。纯函数单测覆盖(`TestPlanBlockApplyOriginDodgesTitleBlock`
  / `…ExplicitAtOverTitleBlock`);真机 0-overlap 且块 bbox 不与图签相交待活体编辑器验收。

## [0.17.0] - 2026-07-21

### Added — PCB SVG 丝印导入 (issue #139)
- **`pcb.silk.import_svg` / `pcb silk-import-svg`**:把 SVG(LOGO / 品牌标 / 图形)作为**填充**
  丝印图元导入(`eda.pcb_PrimitiveImage.create`)——**无需 `debug.exec_js`** 的 typed 路径。新增
  Go 侧 SVG 解析器 `internal/pcb/svgimport`:解析 path(`M/L/H/V/C/S/Q/T/A/Z`)、`polygon`/
  `polyline`/`rect`/`circle`/`ellipse`/`line`、嵌套 `transform`、viewBox,**曲线全部扁平化为线段**,
  输出 EDA 复杂多边形命令数组(轮廓 + **even-odd 挖孔**,LOGO 的孔洞如 "o" 中空自动镂空)。
  Flag:`--file`/`--svg`、`--x/--y`(或 `--at`)=图形左上角、`--width`/`--height`/`--keep-aspect`、
  `--layer`(3 顶 / 4 底自动镜像)、`--rotation`/`--mirror`,以及 **`--dry-run`**(CLI 侧,打印目标
  bbox / 轮廓数 / 顶点数 / 最小特征 + 低于 `--min-line-width`≈6mil 的 DFM 告警)。**ceshi 真机验证**:
  顶/底丝印可建、孔洞镂空、旋转+镜像生效、**扛过 `doc reload` + `pcb save`**(同 primitiveId/bbox)、
  `pcb check` 干净。关键判据:`pcb_PrimitiveImage` 直接吃丝印层的复杂多边形,填充 LOGO **无需**描边化
  或光栅化(推翻初始范围风险)。填充规则为 even-odd,纯描边图形按填充处理。星火计划 LOGO 品牌素材
  示例待授权确认(下载链接 + SHA-256,不再分发)。

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
