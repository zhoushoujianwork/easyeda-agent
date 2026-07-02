# 演示分镜 — ESP32 最小系统板(录 GIF 用)

> **用途**:今晚补录一组端到端演示 GIF。按 [`esp32MiniRequire.md`](../esp32MiniRequire.md)
> 的规格,把 agent 的能力**拆成 9 段独立可录的小例子**——每段一个 GIF,自成一个「爆点」,
> 又能串成一条完整的「从原理图到可制造 PCB」主线。
>
> 每段结构:**🎯 目标 · ⌨️ 命令 · 👀 看点(镜头对准哪) · ✅ 验收(录之前先确认能过)**。
> 全程 `--project ceshi`(一次性工程,可清空重来);最终成品对应固定回归板
> [`test-case-esp32-pcb.md`](test-case-esp32-pcb.md)。

## 规格回顾(来自 esp32MiniRequire.md)

- **4 层板** · **GND=内电层** · **VCC(3V3)=信号层电源平面** · 必含**丝印**
- LED 附近加 **`+` / `−` 极性丝印** · 一颗**点灯 LED**(R3 限流)
- 符合电气规范 / 信号完整性 / 散热(过孔缝合) / 必要接口

## 录制前置(不入镜或作为片头 3 秒)

```bash
easyeda daemon health                 # 确认 daemon + 连接器都在(status: found)
easyeda doc ls --project ceshi        # 确认目标工程/文档已打开
```

> EasyEDA 需开启「**允许外部交互**」。片头可停在 `daemon health` 的 `connected windows` 输出,
> 一句话点明「skill → Go daemon → 连接器 → `eda.*`」这条链路。

---

## 分镜 1 — 生成原理图(库优先放置 + 布线 + 校验)

🎯 从**真实立创/LCSC 库器件**建起 8 件最小系统,不是手绘符号。

```bash
# 从空白页开始;逐组放置(电源/MCU/点灯),设位号
easyeda sch autolayout --project ceshi           # 模块分区放置(确定性、lint 干净的坐标)
easyeda sch read --project ceshi                 # 一次调用读电路:器件 + 每脚权威 net + 悬空脚
easyeda sch autoconnect --project ceshi          # 电源/地脚出 flag,信号本地正交线
easyeda sch layout-lint --project ceshi          # 几何门:0 overlap
easyeda sch check --project ceshi                # 逐项设计检查:悬空脚 / 导线交叉 / 导线压脚
easyeda sch save --project ceshi
```

👀 **看点**:`sch read` 一屏打印 8 件 + 5 网络(3V3/GND/EN/IO0/BLINK)的连通表,电源地
`isGlobal` 标志正确;`sch check` 从红到全绿。

✅ **验收**:8 件位号齐(无 `R?`/`C?`)· `layout-lint` 0 overlap · `sch drc` 0 fatal ·
5 网络连通。(即 [`test-case-esp32-blink.md`](test-case-esp32-blink.md) 的验收标准。)

---

## 分镜 2 — 导入 PCB(原理图 → PCB,飞线生成)

🎯 把原理图的器件 + 网表同步到一块新 PCB(= 菜单「更新/转换原理图到 PCB」)。

```bash
easyeda board list --project ceshi               # 确认 Board 把原理图 ↔ PCB 绑定
easyeda pcb import-changes --project ceshi        # 8 件落到 PCB,生成飞线(ratsnest)
easyeda pcb list --project ceshi                  # 校验:8 件全部到位,位号齐
```

👀 **看点**:执行前 PCB 空白 → 执行后 8 个封装 + 一团飞线出现。

✅ **验收**:`pcb list` = 8,位号齐。
⚠️ **仅首次建板**:`import-changes` 对 API 增量加件是 no-op,后续单件补件用 `pcb add-component`(见 test-case-esp32-pcb.md #20)。

---

## 分镜 3 — PCB 设置(4 层叠层 + 板框)

🎯 按规格把板子设成 **4 层**,并给一个贴合的**圆角板框**。

```bash
easyeda pcb stackup set --layers 4 --project ceshi   # 2 → 4 层铜
easyeda pcb stackup show --project ceshi             # 读回:copperLayerCount=4 + 各层类型
easyeda pcb outline-round --project ceshi            # 圆角矩形板框
easyeda pcb drc-rules --project ceshi                # 读板子 live DRC 规则(线宽/间距/内缩)
```

👀 **看点**:层数从 2 变 4;板框出现;`drc-rules` 打印的规则值就是后面 `route-short`/`pour`
会自动遵循的那套(**规则感知**,不是硬编码)。

✅ **验收**:`pcb layers` 显示 4 层铜;板框闭合;`drc-rules` 有值。
> 内层类型的「GND→内电层」翻转**留到分镜 5** 的 `power-planes` 里做(必须先在信号层铺完再翻)。

---

## 分镜 4 — PCB 布局(模块感知自动布局 + 板框贴合)

🎯 卫星器件(电容/电阻/LED)自动贴到它所连芯片引脚那侧,板框收紧。

```bash
easyeda pcb auto-place --project ceshi           # 模块感知:去耦贴电源脚,LED 链贴 R3,间距规则感知
easyeda pcb outline-fit --project ceshi          # 板框贴合器件(利用率 17% → 71%)
easyeda pcb layout-lint --project ceshi          # 布局质量 + 可布性评分(飞线 MST + 跨网交叉),布线前预测
```

👀 **看点**:散乱器件「吸」到芯片周围;板框从大方框收缩到贴着器件;`layout-lint` 打出
routability 分数。这是**布局前后对比**的绝佳镜头(README 已有 outline-fit 前后图)。

✅ **验收**:0 器件重叠(DRC Safe-Spacing 无违规)· 去耦贴电源脚 · 板框利用率明显提升。

---

## 分镜 5 — 布线 + 4 层电源平面(规格核心:GND 内电层 / VCC 信号 plane)

🎯 短线自研布线跑信号,再用 `power-planes` 一键做出 **GND=内电层、VCC=信号层 plane** 的
4 层电源分配 —— 直接命中 esp32MiniRequire 的核心规格。

```bash
easyeda pcb route-short --project ceshi           # 信号网:每网 MST + L 形导线,规则感知线宽,跳电源/地
easyeda pcb power-planes --dry-run --project ceshi   # 先看计划:哪些网 → 哪层,pad 数
easyeda pcb power-planes --project ceshi          # GND→内电层(15) + VCC/3V3→信号plane(16) + 每焊盘过孔缝合 + 铺铜 + 翻内电层
easyeda pcb drc --project ceshi                   # 校验
```

👀 **看点**:`power-planes` 一条命令内部走完「过孔缝合 → 内层铺铜 → 翻 GND 为内电层 → 重灌」,
终端打印 **DRC 31 → 0、No-Connection → 0**。这是最有冲击力的一段。

✅ **验收**:`pcb drc` 0 fatal · No-Connection = 0 · `pcb layers` 里 GND 内层类型=内电层(PLANE)、
VCC 层=信号层。过孔缝合兼顾**散热**(规格要求)。

---

## 分镜 6 — 天线禁区 + PCB 检查(DFM 审计)

🎯 给 U1 集成天线建**逐层无铜 keep-out**,再跑 DRC + DFM 检查证明布局合规。

```bash
easyeda pcb region create --ref U1 --project ceshi     # 天线净空:no-components/no-wires/no-pours(顶+底);内层 no-inner-electrical
easyeda pcb region list --project ceshi                # 读回规则区
easyeda pcb pour-rebuild --project ceshi               # 重灌:铜皮避开天线禁区
easyeda pcb drc --project ceshi                        # 规则间距门:0 fatal
easyeda pcb check --project ceshi                      # DFM 审计:天线净空 / 丝印正反 / 走线压焊盘 / 锐角 / 悬空铜…
```

👀 **看点**:铺铜在天线下方出现**缺口**;`pcb check` 逐条打印 DFM 规则,`antenna-keepout` 从
WARN 变绿(每层都有净空)。

✅ **验收**:`pcb drc` 0 fatal · `pcb check` 0 ERROR(`antenna-keepout` 满足、`silkscreen-flipped=0`、
`track-over-pad=0`)。

---

## 分镜 7 — 丝印(位号避让 + LED 极性 + 板注)★今日新能力

🎯 位号碰撞避让重排 + 在 LED 旁加 **`+`/`−` 极性标记** + 加板注并对齐居中。
这段集中展示**今天新做的 silk 三件套**(`silk-align` v2 位置感知 · `silk-add` 自由丝印 · `silk-set` 对齐参考)。

```bash
# 1) 位号避让:位置感知(躲开别人焊盘/板框/其它标签),不再叠成一坨
easyeda pcb silk-align --project ceshi

# 2) LED 极性丝印:阳极旁 "+"、阴极旁 "−"(先放,再用 silk-set 贴齐 LED1)
easyeda pcb silk-add --text "+" --x <anodeX> --y <anodeY> --font-size 40 --project ceshi
easyeda pcb silk-add --text "-" --x <cathodeX> --y <cathodeY> --font-size 40 --project ceshi
#   取 LED1 焊盘坐标:easyeda pcb list --include-pads --project ceshi(找 LED1 的 A/K 脚)

# 3) 板注 + 对齐:加一条 credit,再居中到板框
easyeda pcb silk-add --text "auto created by easyeda-agent" --x 1850 --y -2455 --project ceshi
easyeda pcb silk-set --ids '["<creditId>"]' --ref board --align centerx --project ceshi   # 居中到板框
easyeda pcb check --project ceshi                 # 确认丝印无正反、位号正立
```

👀 **看点**:执行前位号叠在一起/压在焊盘上 → `silk-align` 后各自散开、无重叠;LED 旁清晰的
`+`/`−`;板注一条命令**吸附居中**到板框(`silk-set --align centerx` 的实时对齐)。

✅ **验收**:无重叠位号 · LED 极性标记清晰(对应实际 A/K 脚)· `pcb check` 的
`silkscreen-flipped=0`(位号正立、无放反)。
> ⚠️ **录制坑**:`silk-set` 改朝向后,重载文档**之前**的 `pcb snapshot` 会显示旧朝向(stale render)。
> 判定成功看 `pcb check` / silk list,别只信截图;录 GIF 时在 save+重载后再截终态。

---

## 分镜 8 — 挖孔 / 挖槽(机械开孔)

🎯 铣一个板级开槽(安装孔 / 天线隔离槽) —— MULTI 层填充,制造输出识别为 BoardCutout。

```bash
easyeda pcb slot --rect 2450,-1550,2700,-1400 --project ceshi   # 指定矩形挖槽
# 或按器件:easyeda pcb slot --ref ANT1 --margin 20 --project ceshi   # 天线下开隔离槽
easyeda pcb fill list --layer 12 --project ceshi               # MULTI 层确认槽已生成
```

👀 **看点**:板子上出现一个真实的开槽/开孔轮廓。

✅ **验收**:`pcb fill list --layer 12` 有该槽;`pcb drc` 不因它报 fatal。

---

## 分镜 9 — 收尾(快照 + 落盘)

🎯 出成品图,存盘。

```bash
easyeda pcb snapshot --project ceshi              # 成品 PNG(仅供肉眼看;判定以 drc/check 为准)
easyeda pcb save --project ceshi                  # 落盘(daemon 也有防抖 autosave 兜底)
```

👀 **看点**:一张 4 层电源平面 + 圆角板框 + 位号对齐 + LED 极性丝印 + 挖槽的成品板。

✅ **验收**:`pcb save` 审计里 `pcb.save ok=true`;成品对应 test-case-esp32-pcb.md 的 P6 全过。

---

## 串成主线(录完 9 段后的一句话叙事)

> 空白工程 →(1)库件原理图 →(2)导入 PCB →(3)4 层叠层+板框 →(4)模块布局 →
> (5)信号布线 + **GND 内电层 / VCC 信号 plane** →(6)天线禁区 + DFM 检查 →
> (7)位号避让 + LED 极性 + 板注 →(8)挖槽 →(9)快照落盘。
> **全程 typed action,每步都在真实 EasyEDA 画布上验证,没有一步是手绘或 mockup。**

## 录制小抄(避坑)

- **坐标 y-up**:PCB mil 坐标 +y 向上;LED 极性丝印取焊盘实测坐标(`pcb list --include-pads`),别猜。
- **顺序硬约束**:`power-planes` 必须在 `auto-place → outline-fit → route-short` 之后;内电层翻转在铺铜之后(命令内部已保证)。
- **stale render**:`silk-set`/旋转类改动后,重载前的 `snapshot` 显示旧态 —— 录终态前先 save+重载。
- **规则感知**:分镜 3 先 `drc-rules`,后续布线/铺铜的线宽间距都从它来 —— 镜头可回扣这点强调「不是硬编码」。
- **清理还原**:`ceshi` 一次性,录完可 `sch prim-delete` / `pcb rip-up` + 删器件还原(除非留存复核)。
</content>
</invoke>
