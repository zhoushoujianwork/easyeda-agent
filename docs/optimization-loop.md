# easyeda-agent 闭环优化路线(真实设计任务作探针)

> **机制**:用完整真实任务(如 [`esp32MiniRequire.md`](../esp32MiniRequire.md) 4 层板端到端)当探针
> → 暴露缺口 → 归类(官方 bug / CLI-daemon / 检查器 / skill 知识)→ 修复并写回
> (typed action / references / memory)→ **重跑探针回归** → 发现下一轮缺口。
> 本文档滚动更新;每轮探针跑完必须回填。

## 探针轮次 #1 — esp32MiniRequire 4 层板(2026-07-03/04)

**结果**:原理图 13 网络全通 0 fatal;PCB 经 5 轮修复:DRC Connection 50→**0**、
Clearance 26→**0**、`pcb check` **0**、`layout-lint` **100/100**。残留 1 条
"Netlist Error"(已机械证明两侧网表电气一致,系焊盘编号元数据缺失,见 A3)。
布局采用多智能体评审面板(3 设计师×偏好 + 对抗评审 + 裁判)产出的 signal-flow 方案,
2600×1500mil,机械校验器验证 0 违例。

### A. 官方 EasyEDA API 疑似 bug(最小复现 → 提 issue)

| # | 现象 | 证据 | 复现要点 |
|---|------|------|---------|
| A1 | ~~**track↔via 连通性不注册**(4 层板/曾有 PLANE 层之后)~~ **❌ 2026-07-07 订正:误诊,#31 已关单**。真机原样复测:track↔via **会**注册连通(两同网 pad + via 桥 → ratline 清零,4 层/PLANE 各状态皆然)。原始"+5V/U0TXD 浮空"真身是**铺铜连通性 stale**,`pcb pour-rebuild` 即复原;当初 fill 键合的功劳被错记。 | ~~+5V/U0TXD 三轮重铺全部浮空;4 片 fill 清零~~ 复测详见 memory `pcb-via-track-bond-rules`(订正版) | 两个同网 SMD pad 造 ratline → `track(L1)→via→track(L2)→via→track(L1)` 桥 → `startCalculatingRatline` → `pcb_Drc.check` 看 Connection Error 是否清零 |
| A2 | **PLANE 类型层存在时,新建异网 via 不被内电层挖 anti-pad**(Plane-Zone-to-Via + Hole-to-Plane 成对报错;pour-rebuild 不补救) | R2 轮 U0TXD 两颗 via 贴死内电层(anti-pad=0);翻回 SIGNAL 重建再翻 PLANE 也无效 | modifyLayer→PLANE 后 create via(异网)→ DRC |
| A3 | **`SCH→PCB 器件` API 放置(我们经 add_component/importChanges 路径)后 pad number=None**,直到文档重载;连带 DRC 恒报 1 条 "Netlist Error"(diff 只能 UI 看) | J1 重加后 16 pad 全 None;net degree 机械比对 100% 一致仍报 | add_component 放一件 → `pcb_SelectControl`/`getAllPads` 读 number |
| A4 | (约束非 bug)**后台/被遮挡窗口重画布计算永不完成**:`pcb_Drc.check` 超时,轻 API 正常;客户端重试会在 webview 堆积任务恶化 | 5 连超时;窗口切前台后第 1 次尝试即完成 | — 文档化即可 |
| A5 | (量纲)DRC 明细叶子 x/y 单位是 **mil/10** | 全部 leaf 需 ×10 才对齐 mil 坐标 | — 文档化 |

> **已提交官方仓库 `easyeda/pro-api-sdk`(2026-07-04,用户授权)**:
> A1 → [#31](https://github.com/easyeda/pro-api-sdk/issues/31) · A2 → [#32](https://github.com/easyeda/pro-api-sdk/issues/32) · A3 → [#33](https://github.com/easyeda/pro-api-sdk/issues/33)。
> A4/A5 属约束/量纲,不提 issue,文档化于 memory + references。历史 issue:#27(sch_Drc verbose)、#28(autoRouting @alpha)、#29(DSN 丢 keepout)、#30(getNetlist 卡死;官方 prodocs 已标 `sch_Netlist.getNetlist()` obsolete,用 `sch_ManufactureData.getNetlistFile()` 替代)。

### B. CLI / daemon 改进

**P0(直接阻塞本轮的)**
- [x] **`easyeda apply <playbook.json>` 声明式步骤回放**(设计见
  [`design-apply-playbook.md`](./design-apply-playbook.md))——**已落地(2026-07-04)**:
  action/run/notify 步骤、`capture`/`${}`、assert 门禁、journal+`--resume`、
  分类重试+verify 块、复写优先级;12 单测 + 真机三路径。
- [x] **`audit export --playbook` 录制导出**——已落地:变更步骤过滤、autosave 挤压、
  capture 自动接线、raw-id 边界警告;真机全环验证(昨日会话 1920 行 → 27 步提取 →
  18 步区间回放,lint 保持 100)。首个样例 `examples/esp32-mini/moves.playbook.json`。
  后续小改进:exporter stamp `meta.doc`;`pcb via-hop` 宏步骤仍待做(见下)。
> **B/P0 四项真机验证通过(2026-07-07,ceshi/PCB1,agent 全程自驱)**:环境由 agent 用
> chrome-devtools MCP 自举(开 web 编辑器 → #id 直达开工程 → IndexedDB 热重载连接器
> 0.8.4→0.8.9,零人工);`drc --json` 出扁平明细并当场用于定位 10 条 GND 断连;并发 DRC
> 第二发被拒 `ACTION_BUSY`;via-hop 造 3 track+2 via+4 键合 fill 后由 via-delete(kind
> 守卫负向验证通过)/track-delete/fill delete 精准清场,图元数还原 37/89;**新发现**:
> 手术增删后同网(GND)大面积 Connection Error 是铺铜连通性失效,`pour-rebuild` 后
> DRC 11→1(基线,剩 A3 假阳性)。自举流程已沉淀为
> `skills/easyeda-agent/references/environment-setup.md`。

- [x] `pcb drc --timeout <s>` + **忙时防重入**——**已落地+真机验证(2026-07-07)**:
  CLI `--timeout`(默认 60s)经协议新字段 `timeoutMs` 传导给 daemon,daemon 提前
  2s 出结构化 DISPATCH_FAILED(不再两头各自傻等);超时提示「切前台单发,勿循环重试」;
  daemon 对 `pcb/schematic.drc.check` 按 window 防重入(重复下发拒 `ACTION_BUSY` 409)。
- [x] **`pcb via-hop` 复合命令**——**已落地+真机验证(2026-07-07)**:
  `pcb.route.via_hop` = stub + via + 对层 track + via + stub + **自动 4 片键合 fill**
  (两 via × 两层,默认 20×20mil,`--no-bond` 关),via 距端点 `--stub`(默认 20mil)
  防压焊盘,中途失败整体回滚。封 A1 坑。
- [x] `pcb via-delete --ids` / `pcb track-delete --ids`——**已落地+真机验证(2026-07-07)**:
  `pcb.route.delete` 按 primitiveId 精准删,kind 守卫防贴错 id,`removed[]` 回显完整
  before-state(audit 可重建)。
- [x] `pcb drc --json`——**已落地(2026-07-07,单测覆盖真实叶子样本)**:扁平
  `{rule,objType,ruleName,net,x,y,layer,objs,message}`,坐标 ×10 → mil(用 4mil
  clearance 规则交叉验证);`objs` 直接喂 `via-delete`/`track-delete`。

**P1**
- [ ] `pcb netlist-diff`:sch↔pcb 逐网 degree 机械比对(EPAD 1-pin↔N-pads 感知),把 "Netlist Error" 定性成可交付结论。
- [ ] `pcb floorplan --spec`:PCB 模块级布局规划器(`sch autolayout` 的 PCB 版),内置本轮校验器的约束(贴边件朝向/天线区/M3 四角/装配间隙/布线通道预留)。本轮用 3-designer workflow 临时完成,应产品化。
- [ ] `route-short --nets <list>`:网络过滤(现在全有或全无)。
- [x] ~~`pcb region create --layers 1,2`:一次建多层同形 region~~ 已有更简做法:单个 `--layer 12`(多层)region 一次覆盖全部铜层,`pcb check` antenna-keepout 认可(#129/#43 R2 真机验证),无需逐层建。

**P2**
- [ ] `pcb stage-snapshot` 的前台检测提示统一接入所有重操作(DRC/rebuild)。
- [ ] `easyeda call --timeout`(与 `debug exec --timeout` 对齐)。

### C. 检查器(Go 侧)改进 — **三项全落地+真机验证(2026-07-07,ceshi)**
- [x] `pcb check` dangling-end **面积锚定**:同网 pad 铜面内(30mil 容差,先前已有)+ 同网 via **Dia/2 面积**锚定(新);异网保持严格圆心 eps。真机:结点端不再误报,只报真自由端。
- [x] ~~`pcb check` 新规则 **via-bond**:4 层/PLANE 板上裸 track↔via 结点 = 不导通(#31)→ **ERROR**~~ **❌ 2026-07-07 已移除**:#31 证实为误诊(track↔via 会导通),此规则是误报源,连同 `via-hop` 的 bondFill 默认一并回收(bondFill 改为 opt-in `--bond-fill`,默认关)。dangling-end 的 via 面积锚定(上一条)保留有效。
- [x] `pcb check` **floating-track-island**:≥2 段互锚成组、无任一端锚 pad 的铜岛(dangling 盲区)→ WARN,列全成员 id 直接喂 `track-delete`;同网铺铜豁免;单段留给 dangling。真机:双段+via 桥岛 1 条命中。

### D. Skill / references 更新
- [x] `references/pcb.md`:「连通性键合真值表」小节 + via 桥 SOP(fill 法 / `via-hop`)已加(2026-07-07);PLANE 翻转后禁新建异网 via 已在 P8 + via-crosses-plane 规则覆盖;`pcb drc` 条目含前台约束 + `--json`/`--timeout`。
- [x] `references/design-flow.md`:P7 加 via-hop/精准删;P10 加"via 桥必须配 fill"与"DRC 需前台(daemon 已防重入)"两条硬注意(2026-07-07)。
- [x] `references/pcb-layout-conventions.md`:USB-C tie 拓扑已按**官方板对标**落地(§7.8,2026-07-07):A6+B6/A7+B7 双取向 tie 是正解(官方 ESP32S3R8N8 实测),单取向仅降级手段;NPTH 槽坑轮次#3 亲验(via 打上去三连报)。对标还带回:双 PLANE 叠层、线宽分级 6/9.5/15/20、via 12/20+GND 缝合 135 颗量级、顶底三网分区 pour、Q1/Q2 自动下载、功能丝印、CHIP_PU 命名——全在 §7.8 对标表。
- [ ] `standard-parts.json` 已入库 CH340C(C84681)、KF301-5.0-2P(C474881)✓(已提交 3eed339)。

### E. 回归基准
- SCH 侧:`docs/test-case-esp32-blink.md`(既有)。
- **PCB 侧(本轮新立)**:esp32MiniRequire 全流程 = P0→P10 + 5 轮修复知识;
  验收线:DRC Connection=0 ∧ Clearance=0、`pcb check`=0、`layout-lint`≥95、BOM 全 C 号、已 save。
  Netlist Error≤1 且 `netlist-diff` 判定一致时视为通过(直至 A3 修复)。
- **抄图训练(新增,2026-07-07)**:oshwhub 官方开源板逐 pin netlist 机械对照,
  `training/copy-check.py` 判 100% 一致。首个闭环 XDS110下载器(见
  `skills/easyeda-agent/references/copy-training.md`)。

### F. 抄图训练暴露的 CLI/连接器缺口(2026-07-07,XDS110)—— 已开 issue,交 ClawFlow
- [x] [#49](https://github.com/zhoushoujianwork/easyeda-agent/issues/49) **`lib search` 按 LCSC C 号查询是模糊匹配,不保证精确命中**——
  `--query "C5665"` 命中了运放芯片而非目标排针。
- [x] [#50](https://github.com/zhoushoujianwork/easyeda-agent/issues/50) **`sch autoconnect --spec` 对同一 spec 重跑不是幂等的**——
  已连接 pin 会再叠一份新 flag+stub wire。
- [x] [#51](https://github.com/zhoushoujianwork/easyeda-agent/issues/51) **删除 netflag/netport 不连带删除 stub wire**,
  `dangling-wire` 规则也抓不到残留孤儿线。
- [x] [#52](https://github.com/zhoushoujianwork/easyeda-agent/issues/52) `schematic.components.list` 不暴露器件
  device/symbol uuid(只有 footprint uuid)——同 LCSC C 号可能对应多个 pin 编号
  体系不同的 symbol 变体。**已修复**:`serializeComponent` 新增结构化 `device:{libraryUuid,uuid,name}`
  字段(取自 `getState_Component()`,即 rebind 路径复用的 device-library 身份),
  暴露器件本身的 device 身份;导入器件 `libraryUuid` 可能为空,如实返回并在 CLI/文档标注需先经
  `lib search`/`lib by-lcsc` 补齐再喂给 `sch place`。真机复放语义待人工回归确认。
- [ ] 缺 **按坐标区域批量查询/删除图元**的 typed action(训练中用
  `debug exec` 遍历 `sch_PrimitiveWire.getAll()` 按 xy 过滤应急)——暂未开 issue,
  优先级低于上面四项。

### G. 多页原理图训练暴露的缺口(2026-07-07,XDS110 单页→多页重排)—— 已开 issue

把同一 45 件设计从单张 A3 拆成两张 A4(USB/电源/调试 15 件 + 主控 30 件)验证「多
页跨页同名网络自动合并」的机制,过程暴露:

- [x] [#53](https://github.com/zhoushoujianwork/easyeda-agent/issues/53) **`sch autoconnect --all-pages` 跨页落笔失败且报错含糊**——
  应提示切页而非笼统"not placed"。
- [x] [#54](https://github.com/zhoushoujianwork/easyeda-agent/issues/54) **`sch read` / `sch layout-lint --all-pages` 对非激活页
  数据残缺**(pins 空数组 / bbox 被跳过)且无文档说明。
- [x] [#55](https://github.com/zhoushoujianwork/easyeda-agent/issues/55) **`sch page-rename` 返回 `ok:true` 但 `doc ls` 立即
  读取仍是旧名**,需等待不确定的刷新触发(2026-07-07 现场复现:重命名后 `doc ls`
  读到旧名,跑一次无关的 `sch clear` 后才刷新)。
- [x] **再次印证 autoconnect 非幂等的代价有多大**:调试期间因为不确定连接是否
  生效而多跑了几次同一 spec,导致同一批 pin 上堆了 4 层重复 flag(124 netflag+
  124 netport,应为约 60),电气上不影响正确性但视觉/后续维护是灾难。已写入
  `SKILL.md` Core Rule 4a(操作纪律,不是训练方法论)。

## 探针轮次 #3 — esp32MiniRequire 从零重跑(2026-07-07,Board2/PCB2,agent 全程自驱)

**结果:验收线全达标。** 原理图 20 件/13 网络(比前轮 +C7 EN-RC):layout-lint 0 overlap、
sch check 除设计内 NC 外 0、13 网络逐一对齐设计。PCB:4 层(Inner1=GND 内电层/PLANE +
Inner2=+3V3/+5V 分割电源层 + 顶/底 GND 铺铜)、M3 四角挖孔+禁布环、天线区多层 keepout、
LED +/− 极性丝印、**DRC 1(仅 A3 已知假阳性)、pcb check 0 ERROR、lint 0 overlap、
BOM 13/13 全 C 号、已落盘**。环境(编辑器/工程/连接器 0.8.11)由 agent 用
environment-setup.md SOP 自举,零人工。

### 新暴露缺口(轮次 #3)
- [x] **autolayout×connect_pin 网格契约**:分区居中产出分数坐标 → 引脚离 5 网格 →
  批量 autoconnect 53/64 失败。已修(c0d6e80):锚点 snap 5 网格 + 连接器可行动报错。
- [x] **geom-net-mismatch 对 netflag 全量误报**(#45 交叉校验过宽):flag 在本 build 有
  pin"1" 但网表无此件 → 64/64 假警。已修(c0d6e80):designator 为空静音网表判据。
- [ ] **auto-place 跨组卫星不查碰撞**:3 对 overlap(C1↔U2 等)在 gap 调大后依旧原样
  ——卫星只对自家主芯片避让。修法归入 B/P1 `pcb floorplan`;本轮以脚本找空位手工过门。
- [ ] **power-planes 缝合 via 落点缺陷**:打在 SMD pad 正中(via-on-pad 不导通)+ 距
  TH 孔 <11.8mil(Hole-to-Hole)+ 压在信号线上。本轮 via-delete 手术清除 7 颗重打。
  应修 power-planes 的落点选择(避 pad/避孔/避线)。
- [ ] **Inner1 GND pour 在 reload/rebuild 链后静默丢失**(pcb check via-crosses-plane
  的"PLANE 无网络铺铜"分支抓到)——配方重跑(SIGNAL→pour→PLANE→rebuild)即复,根因待查。
- USB-C 16P 隐藏 NPTH 槽(±98,+43)坑亲验:via 打上去 = Slot-to-Via/Hole 三连;绕行即可。
- via-crosses-plane 对合法 via 21 条假阳性属文档已知(best-effort,DRC 仲裁)。

### 新工具链实战验证(全程使用)
`drc --json`(定位每一轮修复)· `via-delete`/`track-delete`(手术十余次,kind 守卫零
误删)· `via-hop`(UART L2 对角跳线×2,键合 fill 自动)· `via-bond` 检查器(终态 0 ——
+5V L2 树 12 片手工键合全过)· `ACTION_BUSY` 防重入 · pour-rebuild SOP(断连暴增即重灌)。

## 第三大块:外壳设计(调研 2026-07-04,待支持)

集齐「原理图 → PCB → **外壳**」三大块。官方 API 现状(`easyeda api search 外壳`):

| API(均 @beta) | 能力 | 判定 |
|---|---|---|
| `eda.pcb_ManufactureData.get3DShellFile(fileName?, 'stl'\|'step'\|'obj')` | 获取平台**自动生成**的 3D 外壳文件 | ✅ 出口已有 |
| `eda.pcb_ManufactureData.place3DShellOrder(interactive?, ignoreWarning?)` | 3D 外壳**直接下单**(interactive=false 或可 headless) | ✅ 制造闭环已有 |
| 参数化配置(壁厚/高度/开孔/螺柱) | **无 API**——应在 UI 弹窗 | ❌ 平台墙候选 |
| (相邻)`get3DFile(step/obj, 元件/过孔/丝印可选)` | 整板 3D 模型导出 | ✅ 可一并封装 |

**待办(等 apply 落地后排期)**:
- [ ] 真机实测 `get3DShellFile` 默认外壳质量(对 ceshi 板导 STL 目检:板框贴合度/按键-USB 开孔/M3 螺柱)。
- [ ] 封装 typed action + 子命令:`pcb shell-export [--type stl|step|obj]`、`pcb shell-order [--yes]`、`pcb 3d-export`。
- [ ] 若默认外壳不可参数化 → 记平台墙,评估「导 STEP → OpenSCAD/CadQuery 后处理开孔」的自研补位;必要时向官方提 feature request(参数化外壳 API)。

## 待办勾稽
- 探针轮次 #2 触发条件:B 列 P0(首位 = `easyeda apply`)全部落地后重跑 esp32MiniRequire 全流程。
- 外壳三件套(上节)在 apply 之后排期。
