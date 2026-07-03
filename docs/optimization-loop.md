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
| A1 | **track↔via 连通性不注册**(4 层板/曾有 PLANE 层之后)。track 端点在 via 圆心、via 压 track 身体中段均不导通;唯一可靠桥接 = net-bound fill/pour 面重叠 | +5V/U0TXD 三轮重铺(20/12/10mil 全试)DRC 恒报浮空;4 片 20×20mil fill 盖住 via+track 后立即清零 | 4 层板,`pcb_Layer.setTheNumberOfCopperLayers(4)` 后:pad→track→via→底层 track→via→track→pad,跑 `pcb_Drc.check` 看 Connection Error;对照 2 层新板(老会话同 pattern 曾正常) |
| A2 | **PLANE 类型层存在时,新建异网 via 不被内电层挖 anti-pad**(Plane-Zone-to-Via + Hole-to-Plane 成对报错;pour-rebuild 不补救) | R2 轮 U0TXD 两颗 via 贴死内电层(anti-pad=0);翻回 SIGNAL 重建再翻 PLANE 也无效 | modifyLayer→PLANE 后 create via(异网)→ DRC |
| A3 | **`SCH→PCB 器件` API 放置(我们经 add_component/importChanges 路径)后 pad number=None**,直到文档重载;连带 DRC 恒报 1 条 "Netlist Error"(diff 只能 UI 看) | J1 重加后 16 pad 全 None;net degree 机械比对 100% 一致仍报 | add_component 放一件 → `pcb_SelectControl`/`getAllPads` 读 number |
| A4 | (约束非 bug)**后台/被遮挡窗口重画布计算永不完成**:`pcb_Drc.check` 超时,轻 API 正常;客户端重试会在 webview 堆积任务恶化 | 5 连超时;窗口切前台后第 1 次尝试即完成 | — 文档化即可 |
| A5 | (量纲)DRC 明细叶子 x/y 单位是 **mil/10** | 全部 leaf 需 ×10 才对齐 mil 坐标 | — 文档化 |

> 提交渠道待确认(jlceda 官方仓库 / EasyEDA 论坛);已具备复现设计,**发布前需用户确认**。

### B. CLI / daemon 改进

**P0(直接阻塞本轮的)**
- [ ] `pcb drc --timeout <s>` + **忙时防重入**(daemon 侧:上一个 DRC 未返回不再下发;超时报错附「把 EasyEDA 切前台」提示)。
- [ ] **`pcb via-hop` 复合命令**:stub + via + 对层 track + via + stub + **自动 4 片键合 fill**,一条命令封掉 A1 坑。
- [ ] `pcb via-delete --ids` / `pcb track-delete --ids`(现只能整网 `rip-up`,单颗错 via 要重铺全网)。
- [ ] `pcb drc --json`:扁平明细 `{rule,net,x,y}`(坐标 ×10 换算成 mil)。本轮全靠临时 python 解析。

**P1**
- [ ] `pcb netlist-diff`:sch↔pcb 逐网 degree 机械比对(EPAD 1-pin↔N-pads 感知),把 "Netlist Error" 定性成可交付结论。
- [ ] `pcb floorplan --spec`:PCB 模块级布局规划器(`sch autolayout` 的 PCB 版),内置本轮校验器的约束(贴边件朝向/天线区/M3 四角/装配间隙/布线通道预留)。本轮用 3-designer workflow 临时完成,应产品化。
- [ ] `route-short --nets <list>`:网络过滤(现在全有或全无)。
- [ ] `pcb region create --layers 1,2`:一次建多层同形 region(天线 keepout 每层独立才过 `pcb check`)。

**P2**
- [ ] `pcb stage-snapshot` 的前台检测提示统一接入所有重操作(DRC/rebuild)。
- [ ] `easyeda call --timeout`(与 `debug exec --timeout` 对齐)。

### C. 检查器(Go 侧)改进
- [ ] `pcb check` dangling-end:按**面积相交**识别「端点在 pad 铜面内」「via 在 track 身体上」(现按圆心;本轮对已通过官方 DRC 的 stub 短暂误报)。
- [ ] `pcb check` 新规则 **via-bond**:检出 track-endpoint-on-via / via-on-track(在本平台=不导通)→ ERROR + 建议 fill 修法。
- [ ] `pcb check` netless-pour 已有;补 **floating-track-island**(整段无 pad 锚的铜,同 dangling 但成组)。

### D. Skill / references 更新
- [ ] `references/pcb.md`:新增「连通性键合真值表」小节 + via 桥 SOP(fill 法);PLANE 翻转后禁新建异网 via;后台窗口重计算节流。
- [ ] `references/design-flow.md` P8:铺铜/内电层步骤加"via 桥必须配 fill"与"DRC 需前台"两条硬注意。
- [ ] `references/pcb-layout-conventions.md`:USB-C 16P 双极性 tie 拓扑(DN 对南区 tie 过 A6 焊盘下方 + DP 对东绕;16P 脚下隐藏 NPTH 槽 ≈ (±98, +43) 相对锚点)。
- [ ] `standard-parts.json` 已入库 CH340C(C84681)、KF301-5.0-2P(C474881)✓(已提交 3eed339)。

### E. 回归基准
- SCH 侧:`docs/test-case-esp32-blink.md`(既有)。
- **PCB 侧(本轮新立)**:esp32MiniRequire 全流程 = P0→P10 + 5 轮修复知识;
  验收线:DRC Connection=0 ∧ Clearance=0、`pcb check`=0、`layout-lint`≥95、BOM 全 C 号、已 save。
  Netlist Error≤1 且 `netlist-diff` 判定一致时视为通过(直至 A3 修复)。

## 待办勾稽
- 探针轮次 #2 触发条件:B 列 P0 全部落地后重跑 esp32MiniRequire 全流程。
