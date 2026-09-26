# EasyEDA Schematic — 连线(pin-aware autoconnect)

> 从 [`schematic.md`](schematic.md) 拆出(RFC #178)。入口、器件放置、Actions 目录、电气铁律仍在
`schematic.md` —— **先读它**,再按需读本文件。

---

## Pin-aware autoconnect — let the planner pick direction/offset

写线/接标记的 daemon 执行路径会校验引脚方向、零长/斜线和穿本体等非法输入，并做写后回读。
以下候选评分负责寻找合法形态，不能替代这些正确性检查。已有连通不证明布局合法；详见
schematic.md 的 typed 写入校验。

宿主可能在 create 返回后才把导线发布给 getAll。只有首轮完整回读的几何合法、但拟线段尚未
覆盖时，daemon 才在同一窗口串行锁内最多再读 4 次（间隔 250ms，总追加预算 2s，且服从
调用方期限）。每轮重新核对文档、递增 FIFO、无 abandoned 变化、库存和几何；线段完整覆盖
后还必须通过拓扑比较。缺测、新非法几何或到期响应立即失败。它不重发 mutation，
不凭 create 返回的 PID/line 放行。到期仍不可见就返回 partial/非零，保留
readbackAttempts/observations 及可用的原始审计；
调用方保存当前已知状态、fresh 回读后从参数源重新生成受保护计划，不能盲重放失败队列。

`connect_pin` (`sch connect`) keeps the connection **safe** (pin → short wire →
flag/netport, never a netflag on a bare pin), but it still makes YOU pick
`--direction` and `--offset`, so layout quality depends on judgment. **`sch
autoconnect` removes that judgment**: it pulls the real geometry (part bboxes,
pin coords, existing flag/port/label bboxes, title-block keep-out), scores every
`up/down/left/right × offset` candidate with a deterministic cost function
(flag-collision / through-part penalties, shortest-offset + outward-side +
kind-default bonuses), picks the lowest-cost one, and delegates the mutation to
`connect_pin`. Same schematic state + spec → same selection (deterministic).

`sch connect --kind net_label` 使用原生 `createNetLabel(x, y, net)`；方向和偏移只决定
桩线端点，该接口不接收旋转参数，因此不会创建 `__ROTPROBE__` 旋转校准旗。
电源、地和 netport 仍按原流程校准旋转。跳过无用探针不代表宿主已支持原生标签；
接口标注 EDA v4 起提供。Web 4.1.60 可能已创建标签却返回空值；连接器比较调用前后
新增的 Name 属性，并核对坐标、可见性、父导线及真实网名。仅唯一匹配才返回 `verified:true`。
读回不完整或匹配不唯一时返回 `partial:true, verified:false`，保留对象 ID，CLI 非零退出；
先保存并回读，不能盲重试或仅因返回空而删除桩线。
兼容性与验证说明见 [schematic.md](schematic.md#原生-net_label-超时191)。

**批次内互斥 (issue #138):** 同一批(--spec / 多 --pin)里**已规划的短桩会当作
既存导线注册回 scene**,后续连接对它做同样的异网硬拒——同器件相邻异网引脚
(隔离 DC-DC 的 B0512S 类四域脚)不再出现短桩共线相触被 EasyEDA 合并成隐性
短路;规划器会自动换方向/offset 错开,四向全堵时按 #64 语义响亮报
"no safe candidate" 拒绝落笔。多域脚器件仍建议 power 上/gnd 下方向分治,给
规划器留出错开空间。**标签 stagger 用真实 marker bbox 预测(#148 Phase-2):**
预测框按 family + direction 使用活体 `getPrimitivesBBox` 标定值,并相对连接端点朝
body 所在一侧偏移,不再用旧的端点居中 24×11 框:ground 为 10×21/21×10、
power 为 6×11/11×6;**netport 的长度跟网名走**(`6*len(net)+8`,下限 31)——
写死 31 会让任何长于 3 字符的网名少算,评分器于是算出「刚好不撞」而渲染出来擦在
一起。**预测框 = 符号本体 ∪ 文字带**:`sch check` 的 marker-overlap 判的就是合并
后的框(power/ground 的网名画在符号旁,长 `6*len(net)`、高 12),判定与生成必须
同一把尺,否则评分器挑的「干净」位置在 check 眼里照样重叠。故
**10-unit pitch 平行脚上相邻 marker 会触发 stagger,自动挑不同 offset 错开**;
候选打分与同批后续连接注册回 scene 使用同一预测函数。

**密集区会拉长桩线超出 `--offset-max`。** 常规档位里一个「既可选(未被 #64 硬
拒绝)又不撞 marker」的候选都没有时,规划器把候选范围扩到 **3×offsetMax** 继续
找干净位置 —— 人工画法本就如此(同侧密集旗阶梯 offset 错列)。所以看到某根桩线
明显比同页其他的长,那是**让开标签**的结果,不是失控;扩展候选照样过全部判据,
#64 短路保护不会被绕过。真机 ceshi 单块回归:markerOverlaps 12 → 3。
残留不可避免的密集重叠由 `sch check` 的 marker-overlap 门捕获(见 Actions)。

**Hard rejects (issue #64):** two hazards are never soft penalties — they make a
candidate *unusable* no matter the offset, because EasyEDA would silently merge
nets and the post-hoc DRC can't see it: (1) a stub whose endpoint or path touches
an existing **foreign-net wire** (endpoint-on-wire = junction = net merge), and
(2) a stub **crossing a non-target pin** (EasyEDA trims+connects there, and the
wire-over-pin rule exempts pin endpoints). autoconnect now pulls existing wire
geometry into the scene automatically; a wire already on the target net is fine
(that's the connection point). **Title-block intrusion is now a THIRD hard reject
(#147)**: a label landing in the A4 图签 keep-out steers to a safe direction, or —
when every candidate enters it — fails rather than dropping a netport on the
明细表 (which layout-lint, part-only, and the geometry-blind electrical check both
miss). If EVERY direction/offset is a
hard reject, autoconnect refuses to place the stub and reports the connection as
failed — resolve the layout (move the part / clear the wire / free the title-block
corner) and retry. **Create-after backstop (#147 DoD2):** the plan hard-rejects a
title-block hit using a NOMINAL box, but a marker whose real rendered width (net-name
text) still spills into the 图签 is caught by a post-batch real-bbox re-read — the
intruding wire+marker is DELETED and that pin failed, so the command never returns
success with a marker on the title block. **Partial-run bookkeeping (#146):** a batch interrupted mid-way
returns `partial:true` + `succeeded[]`/`failed[]` pin lists — **retry ONLY the
failed pins, never replay the whole spec** (a blind re-run stacks duplicate markers
on the already-connected pins, which `NetKnown=false` after a connector drop can't
detect). **Always run `sch check` right after a batch autoconnect** — its new
`duplicate-net-marker` rule is the guard that catches those stacked markers.

**带痕候选不再静默入选。** 硬拒之外的碰撞惩罚是软性累加,此前选中候选哪怕
score 上千(真机:score=1737 的长桩扎进邻组标签区)也照连且报告只显示落选项。
现在**选中候选** score 超过软阈值或 reasons 里含碰撞类惩罚时,结果行会带
`⚠ WARN`(默认档照连);**`--strict`** 则把这类连接直接判失败、不落地。
看到 WARN 的处方是**挪件腾位后重连**,不是忽略它。

**平台会随机吞掉一个连接(stuck-at-99%),autoconnect 现在自己救一次。** 实测 2821 次
connect_pin 里 57 次失败,其中 23 次是 netflag 卡在「请求被丢掉但平台不报错」——
它是随机的,同一脚重发通常就成。所以失败后**重试一次,但只在连接器明确声明回滚
之后**(netflag 失败时它会先删掉已建的桩线);`connector did not respond` 这类
**状态未知**的失败绝不重试 —— 那可能只是我们没等到回应而对方已经建好了,重试会得到
第二条桩线和第二面旗。被救回来的连接在报告里标 `retried`,**别忽略这个字段**:
它是平台在变差还是变好的唯一现场证据。

另外 connect_pin 用的是 **35s 专用预算**而非默认 20s(**裸 `sch connect` 也
已对齐**,此前它还吃 20s 默认值,慢速成功被报成失败):连接器内部最坏路径
(wire 7s + 重试 0.25s + wire 重试 7s + netflag 7s = 21.25s)本来就超过 20s,
默认预算会让 daemon 先于连接器放弃 —— 报「connector did not respond」而对方其实
已经把线和旗建完了(实测 57 次失败里 17 次是这么来的)。**`sch connect` 失败必须
非零退出**；不能仅因回读 pin 已在目标网就报告 `slowLanded` 成功，因为修复前
就可能已经同网但几何错误。明确的几何门拒绝保留原始错误；超时等未知结果须回读
导线、标记及方向后核实，不盲重试。这项错误处理修正尚须随下一开发包部署。

```bash
# single pin by designator:pin (number OR name)
easyeda sch autoconnect --pin U1:41 --kind gnd --net GND
easyeda sch autoconnect --pin U1:3V3 --kind power --net +3V3

# 同名多脚:尾缀 * 一次连全部(连接器的冗余 VBUS/GND/屏蔽脚,#145)。
# 光写功能名会被判歧义并拒绝——autoconnect 不该替你挑一个;* 是你说"全接"。
# 对单脚是恒等,所以电源/地/屏蔽脚可放心加星。
easyeda sch autoconnect --pin J1:VBUS* --kind power --net 5V   # → J1:A4B9 + J1:B4A9

# explicit coordinates (compat with existing flows)
easyeda sch autoconnect --x 720 --y 670 --kind gnd --net GND

# preview the plan + rejected options WITHOUT mutating
easyeda sch autoconnect --pin U1:41 --kind gnd --net GND --dry-run --json

# batch spec — clustered pins auto-stagger so labels don't stack
easyeda sch autoconnect --spec p1-connect.json

# re-run the SAME spec safely — pins already on the target net are skipped
easyeda sch autoconnect --spec p1-connect.json          # idempotent, no growth

# re-route pins currently on the WRONG net (delete old flag+wire, reconnect)
easyeda sch autoconnect --spec p1-connect.json --replace
```

**Idempotent (issue #50):** before connecting, autoconnect reads each pin's
current net (via `sch list --include-pins`, which now carries `net`) and classifies
every connection into three states — `new` (floating → connect), `already-connected`
(already on the target net → **skip**, no duplicate flag+wire), and `conflict` (on a
different net). A conflict is an error by default; pass `--replace` to delete the old
flag+wire (deleted **together**, so no orphan stub — see issue #51) and reconnect.
Re-running the same spec is therefore safe and never stacks duplicates. `--dry-run`
reports the three states without mutating.

Spec JSON (`--spec`): `{"connections":[{"pin":"U1:41","kind":"gnd","net":"GND"},
{"pin":"U1:3V3","kind":"power","net":"+3V3"}], "rules":{"avoidTitleBlock":true,
"avoidPinFanout":true,"staggerLabels":true,"offsetRange":[18,80],"offsetStep":6,
"minLabelGap":12}}`. Each result reports the `selected` candidate (direction /
offset / endPoint / score), the `rejected` alternatives with reasons, and the
`wirePrimitiveId` / `flagPrimitiveId`. The title-block keep-out comes from the
shared `sch sheet-geometry` derivation (issue #26) — when the sheet bbox isn't
exposed it is reported as **provisional** and not geometrically enforced (so a
guessed box can't corrupt scoring). **Prefer `sch autoconnect`
over hand-picking `sch connect --direction/--offset`** for power/ground/netport
stubs; `sch connect` stays for when you deliberately override the geometry.
