# PCB 功能支持全景(CLI 视角)

`easyeda pcb` 域(含兼容用 `workflow` 记录)的**当前能力清单 + 待支持路线**。定位:AI agent
从原理图同步到制造导出,全程 typed CLI 操作,每步可观测、可校验。

> 动作目录真值:`easyeda actions`;流程编排(P0–P10 何时用哪条)见
> [`design-flow.md`](../../.agents/skills/easyeda-agent/references/design-flow.md);
> 设计规范手册(线宽/间距/过孔/铺铜,DRC 报错的 `[规范 §N]` 指向)见
> [`pcb-design-rules.md`](../../.agents/skills/easyeda-agent/references/pcb-design-rules.md)。

## 一、已支持(按功能域)

### 1. 板与同步

| 能力 | 命令 | 说明 |
|---|---|---|
| 新建板并绑定 | `pcb new-board` | 从原理图建板+空 PCB 页并绑定(CLI 版「原理图转 PCB」);`--force` 对已绑板是破坏性操作 |
| 网表同步 | `pcb import-changes` | 原理图 → PCB 增量同步(平台对 API 新增器件是 no-op,首次同步前放完整电路) |
| 单件补挂 | `pcb add-component` | 往已有 PCB 加单个器件并连接焊盘网络(绕过失效的增量同步) |
| 文档/视图 | `doc reload` / `pcb snapshot` / `pcb view-mode` 相关 | 写后读取会标注 stale 风险；权威批次以 save → reload → readback 收尾 |

### 2. 布局

| 能力 | 命令 | 说明 |
|---|---|---|
| 板框与原点 | `pcb outline` / `outline-fit` / `outline-round` / `origin get/set` | `outline-round` 生成含原生 ARC 的闭合锁定板框；`outline-get` 回读中心线尺寸、半径、线宽、锁定与弧数；origin 只改显示坐标，不移动几何 |
| 角色感知自动摆放 | `pcb auto-place` / `place-constrained` | 卫星贴其所连芯片侧、2 脚件自动转向、间距规则感知;规划后按 blocking 复算合法化(重叠/短路/出框就地重定位) |
| 分区规划 | `pcb floorplan` / `pcb zones` | S0 spec 驱动的有序带切分(只读)+ 分区认领 |
| 分档记录 | `pcb stage confirm-tier 1-4` / `set-assembly` | 兼容保留孔→边缘件→主芯片+RF→卫星的历史记录；不决定后续动作能否执行 |
| 布局检查 | `pcb layout-lint` | 报告重叠/紧间距/可布性(飞线 MST+交叉)/手焊可达性(no-access)的具体事实 |
| 二层协同求解 | `pcb layout solve/check/render` | 纯离线：从完整 board dump 与参数请求生成/独立复验/渲染布局+共同路由候选；solve 输出基线、失败尝试、候选的整板与自动聚焦局部 SVG；固定装配面，支持 TOP/BOTTOM 与最多两个过孔；可用 `--apply-project/--apply-doc` 输出绑定目标工程/文档的 layout-only playbook，执行前仍须 fresh dump 核对候选来源哈希；本命令本身不写 EDA |
| 布局质量分 | `pcb layout-score` | 九维 0-100+逐器件归因(partition/flow-order/edge-io/protection/tidy/compact/rf/routable/clearance),blocking 一票否决;`--part` 器件聚焦视角;金标准五真板校准(`make layout-calibrate`) |
| 打分驱动精修 | `pcb refine` | 读归因对最弱维做确定性变换,每步复核可回滚;锁定件不动，历史签字档仅作提示 |
| 编组式移动 | `pcb components move` 类 | 无状态刚体移动(持久编组见路线 §1) |

### 3. 布线

| 能力 | 命令 | 说明 |
|---|---|---|
| 短线启发式 | `pcb route-short` | 每网 MST、规则感知线宽(按网络角色给宽)、障碍感知 L 朝向、默认跳电源/地(该铺铜) |
| 离线局部寻路 | `pcb route solve/check --board board.json --from request.json --out report.json` | 公共 Go 包 `pkg/pcbrouting`；TOP/BOTTOM 单层零过孔、直线/45°有界寻路。默认同时输出同名 SVG，显示整板障碍、搜索范围、路径线宽/净距和失败原因；`check` 另传 `--plan plan.json`，按独立需求重验；不连接编辑器、不是整板自动布线 |
| 布局/多网反馈闭环 | `pcb layout solve --board board.json --from request.json --out report.json` | `pkg/pcbsolve` 在每个完整布局投影上共同求解所有需求；先有界回溯路径/排网次序，实际阻挡再驱动完整组平移/声明旋转，总预算耗尽为 incomplete。`check` 从 move 重建候选；`render --from request.json` 核对板/请求来源并显示相同机械禁放区，无 `--from` 的旧调用仍兼容 |
| 关键网先行 | `pcb route-critical` | P7.0 一条命令:电源按层数走 planes/pour → 差分对双源识别成对布线+skew 实测 → 自动 `track-lock` |
| 逐焊盘铜路径核查 | `pcb net-path --from REF.PAD [--through REF.PAD] --to REF.PAD [--layer 1]` | 只读按支持的原始 pad shape + track/arc/via 构图；`--layer` 在受限图求路并排除物理过孔，回报 requestedLayer/连续路径/层/线宽/过孔数；未知焊盘几何、缺失 arc 回读或 ordered proof 的重叠铜返回 unknown/error，同网名不等于连通，铺铜/PLANE 明确排除 |
| 外部自动布线 | `pcb export-dsn` / `import-autoroute` / `pcb autoroute` | Specctra DSN 往返(带禁布区注入),Freerouting 兜底;稠密板默认交编辑器原生自动布线 |
| 拆线 | `pcb rip-up` | 按网/按范围拆 |
| 锁定 | `pcb track-lock` | 手布关键线锁死,防被自动布线/pour-rebuild 冲掉 |

二层请求可在 `layout.groups[]` 中添加 `translationSearch: {"step":10,"maxDistance":80}`（mil）；
这会按实际冲突动态搜索让位位置，旧 `offsets` 继续兼容。自动移动组的全部外部网络必须由
`routing.demands` 连成完整连接图；缺失时拒绝输入。输出含 `attempts[].conflicts`、失败时的
`partialRoutes`、合法候选的 `reservations`。它们是通道规划，layout Apply 只写器件位置；
已有外部铜的断连、未声明变换及被篡改的预留由 `check` 拒绝。

### 4. 铺铜与平面

| 能力 | 命令 | 说明 |
|---|---|---|
| 铺铜 | `pcb pour` / `pour-fit` / `pour-rebuild` | 规则感知内缩;`pour-fit --replace` 默认清跨层同网 pour(顶/底要显式关) |
| 4 层电源树 | `pcb power-planes` | GND+电源各占专用内平面+每焊盘过孔缝合;GND 内层翻成真 PLANE 的验证配方 |
| 缝合/填充 | `pcb via-stitch` / `pcb fill` | 接地缝合过孔阵 / 实心填充 |
| 禁布区 | `pcb region` / `pcb antenna-keepout` / `lib footprint region` | 板级禁铺/禁走线区；天线 keepout 按块库声明全层生成；封装副本可写 region 并保存回读，实例绑定仍须现场核对 |
| 挖槽 | `pcb slot` | 板内挖空(MULTI 层) |

### 5. 丝印与标注

| 能力 | 命令 | 说明 |
|---|---|---|
| 位号避让重排 | `pcb silk-align` | 位置感知:4 方向打分避开焊盘/器件体/禁区/板框/其它标签;挤死的如实报告 |
| 自由丝印 | `pcb silk-add` / `silk-set` | 板注/极性标记,层/字号/线宽/旋转/`--font-family` 可配并回读实际字体；`--align --ref` 对齐参考 |
| 非电气参考图片 | `pcb image create/list/modify/delete` | PNG/JPEG/SVG 嵌入 DOCUMENT 13；mil、左上角 anchor，创建须给宽或高；不是制造丝印 |
| 矢量图形 | `pcb silk-import-svg` | SVG(logo/品牌)转填充丝印图元,dry-run 预览 |

PNG/JPEG 转制造丝印需要人工调节图稿，不提供自动转换命令；已有 SVG 丝印导入按原契约使用。

### 6. 叠层、规则与制造

| 能力 | 命令 | 说明 |
|---|---|---|
| 叠层 | `pcb stackup` | 2–32 铜层 + 内层类型(信号↔内电层) |
| 规则 | `pcb drc-rules` / `drc-rules-set --from` / `net-class list/create` / `net-classes` | 完整规则与真实 EasyEDA 网络类可写入、回读和失败回滚；复数 `net-classes` 是路由器的启发式线宽表，不能冒充持久化网络类 |
| 检查 | `pcb drc` / `pcb check` / `pcb net-path` | 官方 DRC + 重建的逐项检查 + 指定焊盘间的只读铜路径证据；dangling pad anchor 按 shape/rotation，legacy 尺寸只用保守几何并在 `limitations` 说明(报错带 `[规范 §N]` 指向手册章节) |
| 历史流程记录 | `workflow status/advance` | 兼容读取/记录 `outline_confirmed`、`pre_route_passed`、`post_route_checked`；typed action 不再据此拒绝执行 |

## 二、待支持 / 路线

1. **持久化编组**(#173,用户点名):平台无编组 API(UI 组对扩展不可见)→ virtual group 按 documentUuid 持久化,`pcb group list/create/add/remove/ungroup/move`;布局动作默认把组当不可拆刚体。与原理图侧同方案同期实现。
2. **接插件逐脚丝印 `pcb silk-pins`**(P9):端子/排针按脚自动标网络简称,辅助接线;8 条设计标准即校验门(几何+语义双查)。见 [FEATURES.md](../FEATURES.md) roadmap。
3. **tidy-lint / tidy**(#153):布局/丝印一致性审计与一键 cleanup。
4. **2D/3D 渲染切换 `pcb view-mode`**(#169):snapshot 前确定性选视图。
5. **受控阻抗**:平台墙(叠层 Er/介质厚读不到);网长可读,等长/skew 报告可做。
6. **泪滴**:无 typed API,文档源注入路径未验证。
7. **四层联合求解**：公共层叠模型已表达有序 signal/plane 和 plane net；旧 dump 只有层数时不推断内层角色。内层平面、跨层过孔与回流共同求解留待下一阶段。

---

*本文只记最终功能形态;实现历史与状态细节见 [`docs/FEATURES.md`](../FEATURES.md)。
改动 PCB 命令后请同步本文。*
