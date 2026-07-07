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

> **已提交官方仓库 `easyeda/pro-api-sdk`(2026-07-04,用户授权)**:
> A1 → [#31](https://github.com/easyeda/pro-api-sdk/issues/31) · A2 → [#32](https://github.com/easyeda/pro-api-sdk/issues/32) · A3 → [#33](https://github.com/easyeda/pro-api-sdk/issues/33)。
> A4/A5 属约束/量纲,不提 issue,文档化于 memory + references。历史 issue:#27(sch_Drc verbose)、#28(autoRouting @alpha)、#29(DSN 丢 keepout)、#30(getNetlist 卡死)。

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
- [x] `pcb drc --timeout <s>` + **忙时防重入**——**代码已落地(2026-07-07,真机待验)**:
  CLI `--timeout`(默认 60s)经协议新字段 `timeoutMs` 传导给 daemon,daemon 提前
  2s 出结构化 DISPATCH_FAILED(不再两头各自傻等);超时提示「切前台单发,勿循环重试」;
  daemon 对 `pcb/schematic.drc.check` 按 window 防重入(重复下发拒 `ACTION_BUSY` 409)。
- [x] **`pcb via-hop` 复合命令**——**代码已落地(2026-07-07,真机待验)**:
  `pcb.route.via_hop` = stub + via + 对层 track + via + stub + **自动 4 片键合 fill**
  (两 via × 两层,默认 20×20mil,`--no-bond` 关),via 距端点 `--stub`(默认 20mil)
  防压焊盘,中途失败整体回滚。封 A1 坑。
- [x] `pcb via-delete --ids` / `pcb track-delete --ids`——**代码已落地(2026-07-07,真机待验)**:
  `pcb.route.delete` 按 primitiveId 精准删,kind 守卫防贴错 id,`removed[]` 回显完整
  before-state(audit 可重建)。
- [x] `pcb drc --json`——**已落地(2026-07-07,单测覆盖真实叶子样本)**:扁平
  `{rule,objType,ruleName,net,x,y,layer,objs,message}`,坐标 ×10 → mil(用 4mil
  clearance 规则交叉验证);`objs` 直接喂 `via-delete`/`track-delete`。

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
- [x] `references/pcb.md`:「连通性键合真值表」小节 + via 桥 SOP(fill 法 / `via-hop`)已加(2026-07-07);PLANE 翻转后禁新建异网 via 已在 P8 + via-crosses-plane 规则覆盖;`pcb drc` 条目含前台约束 + `--json`/`--timeout`。
- [x] `references/design-flow.md`:P7 加 via-hop/精准删;P10 加"via 桥必须配 fill"与"DRC 需前台(daemon 已防重入)"两条硬注意(2026-07-07)。
- [ ] `references/pcb-layout-conventions.md`:USB-C 16P 双极性 tie 拓扑(DN 对南区 tie 过 A6 焊盘下方 + DP 对东绕;16P 脚下隐藏 NPTH 槽 ≈ (±98, +43) 相对锚点)。
- [ ] `standard-parts.json` 已入库 CH340C(C84681)、KF301-5.0-2P(C474881)✓(已提交 3eed339)。

### E. 回归基准
- SCH 侧:`docs/test-case-esp32-blink.md`(既有)。
- **PCB 侧(本轮新立)**:esp32MiniRequire 全流程 = P0→P10 + 5 轮修复知识;
  验收线:DRC Connection=0 ∧ Clearance=0、`pcb check`=0、`layout-lint`≥95、BOM 全 C 号、已 save。
  Netlist Error≤1 且 `netlist-diff` 判定一致时视为通过(直至 A3 修复)。

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
