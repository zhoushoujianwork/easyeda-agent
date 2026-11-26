# PCB 考试推荐流程（嘉立创 PCB Layout 工程师考证）

按评分细则出板时照此顺序执行。题面数值（板框、坐标、线宽、字号）以当次考试说明为准，下文用
260915 模拟考试的数值举例；原理图、器件选型等前置工作见 [设计流程](design-flow.md)。
顺序本身就是要求：先布信号再补规则、先布信号再打 GND 扇孔，都在现场造成过整板返工。

每一步都要留证据：写入后 save → `doc reload` → fresh 回读（`pcb dump`、DRC、`pcb check`），
前一步不过不进下一步。DRC 必须 `passed:true`（考试有错即 0 分），`pcb check` 必须 0 ERROR。

## 0. 先读评分细则，列扣分项

考试说明的评分表就是验收清单。PCB 整体设计一项按处扣分：直角/锐角走线、断头线、导线未从焊盘末端引出、
从阻容焊盘中间穿过、线宽大于焊盘宽度、过孔打在焊盘上、丝印压焊盘、丝印重叠。对应 `pcb check` 的
`acute`/`nonOrtho`/`dangling`/`overPad`/`viaInPad`/`silkOverPad` 计数和 `silk-plan` 的重叠判据，逐项清零。

## 1. 设计规则（第一件事）

```bash
easyeda pcb config get --project ceshi --doc PCB1 > config-before.json
easyeda pcb config clearance --name copperThickness1oz --track-to-track 6 --project ceshi --doc PCB1
easyeda pcb config track --name copperThickness1oz --min 10 --default 10 --project ceshi --doc PCB1
easyeda pcb config track --name PWR --copy-from copperThickness1oz --min 10 --default 20 --project ceshi --doc PCB1
easyeda pcb config via --name viaSize --min-outer 24 --min-hole 12 --project ceshi --doc PCB1
easyeda pcb net-class create --name PWR_Class --net GND --net 3V3 --project ceshi --doc PCB1   # 全部电源网，按网表对账
easyeda pcb config bind --class PWR_Class --track-rule PWR --project ceshi --doc PCB1
```

保存重载后 `pcb config get` 回读。规则先于布线，路由器才按默认线宽出线。
细间距例外（[配置](pcb-config.md)）：0.4 mm QFN 焊盘 7.9 mil，两根 10 mil 并行出线只剩 5.75 mil，信号与 PWR 最小线宽
降到 8 并记录理由；扇出最密的网单独建 `SIG8`（8/8）绑类。考题 LQFP 不需要这个例外。

## 2. 板框、原点、固定件

题目给尺寸时：`pcb outline-set`/`outline-round` 画板框、`pcb origin set` 设左下原点、锁定；螺丝孔、排针、
屏幕座、按键、主控按题面坐标与角度 typed 放置并锁定（[板框与固定件样例](examples/260919-at32f415/fixed-mechanics.md)）。

## 3. 布局

按 [布局规则](pcb-layout.md)：板边接口朝外、滤波电容先大后小贴电源脚、DC-DC 输入电容近 IN、反馈分压近 FB、
电感近 LX；**无源晶振近单片机、不放板边，负载电容夹在芯片振荡脚与晶振之间**
（[晶振样例](examples/esp32s3r8-crystal/README.md)）。全部器件在顶层、不重叠、不出板。
连续两轮自检（第二轮含保存重载），形成复核包后按[写入授权](../SKILL.md#设计与执行边界)进入布线。

## 4. 关键网手布并锁定

晶振、RF 馈线、USB 等不能交给路由器的网，按参数数据逐段 `pcb track` 写入，`pcb net-path --through`
证明拓扑（如芯片脚 → 负载电容焊盘 → 晶振焊盘），然后 `pcb track-lock --net …`。
USB：顶层、不换层、类差分成对；晶振：顶层、不打孔、短直。

## 5. 晶振包地净空（考试 6 分项）

「避免底层走线，顶层做包地净空处理」：

1. 顶层 GND 包地线围住晶振 + 负载电容，芯片侧留 XTAL 进线缺口；缺口两端 45° 收进负载电容 GND 焊盘（不留断头、不回折成锐角）。
2. 晶振、电容的 GND 焊盘短桩接包地线。
3. 包地线上一圈 GND 过孔：`pcb via-fence --dry-run` 预检，挤不下的边只走线。
4. 环内顶层 `pcb region create --rule no-pours`；晶振下方底层 `--rule no-wires`（底层 GND 铺铜保持完整）。
5. 全部 `track-lock`。

## 6. GND 扇孔（先于普通信号）

每个需要的 GND 焊盘打 dog-bone 过孔：焊盘外、0/45/90° 短桩、24/12 mil（按规则），锁定。先布信号会把去耦 GND
焊盘围死，现场只能拆线重来。

## 7. 官方自动布线

```bash
easyeda pcb autoroute --project ceshi --doc PCB1 --layers 1,2 --ignore-net GND,XTAL_P,XTAL_N,RF_ANT
```

走官方 `autoRouting`（与「布线 → 自动布线」同一引擎，保留已有铜，报告锁定铜是否丢失），不走 Freerouting。
失败网：拆掉同组后先布它；稠密扇出先近列后远列；仍不通参数化手布（信号换层 ≤ 2 孔）。
`success:true` 或「1/1 布通」不代表每个连接都布了——逐个 `pcb net-path` + DRC，多引脚电源网常漏去耦直连。

## 8. 铺铜

```bash
easyeda pcb pour-fit --project ceshi --doc PCB1 --net GND --layer 1
easyeda pcb pour-fit --project ceshi --doc PCB1 --net GND --layer 2
easyeda pcb pour-rebuild --project ceshi --doc PCB1
```

保存重载后 DRC；仍孤立的 GND 焊盘补扇孔。

## 9. 缩颈与泪滴

```bash
easyeda pcb dump --project ceshi --doc PCB1 --include-copper --out board.json
easyeda pcb neckdown-plan --board board.json --width 8 --out neck.json --apply-project ceshi --apply-doc PCB1
easyeda apply neck.playbook.json --yes
# save → reload → 新 dump
easyeda pcb teardrop-plan --board board.json --exclude-net RF_ANT,XTAL_P,XTAL_N --out tear.json --apply-project ceshi --apply-doc PCB1
easyeda apply tear.playbook.json --yes
easyeda pcb pour-rebuild --project ceshi --doc PCB1      # 考试：添加泪滴后必须重建铺铜
```

`neckdown-plan` 修「线宽大于焊盘」；`teardrop-plan` 画考试允许的线条形泪滴（原生泪滴对象无公开接口，
交付时如实说明）。维护时取得新 dump，用 `teardrop-delete-plan --from tear.json` 删除，或
`teardrop-modify-plan --from tear.json --length-scale 0.8 --base-scale 0.8` 重算尺寸；均须指定 `--board` 和新的 `--out`。
匹配不完整或锁定时停止，细节见 [线条形泪滴维护](pcb-layout.md)。

## 10. 丝印

```bash
easyeda pcb silk-set --project ceshi --doc PCB1 --ids <全部位号> --font-family Arial --font-size 45 --rotation 0
easyeda pcb dump --project ceshi --doc PCB1 --include-copper --out board.json
easyeda pcb silk-plan --board board.json --allow-90 --out silk.json --apply-project ceshi --apply-doc PCB1
easyeda apply silk.playbook.json --yes
easyeda pcb silk-plan --board board.json --only NONE \
  --hint J1.1=3V3 --hint J1.2=GND --hint J1.3=TX --hint J1.4=RX --hint SW1.1=BOOT \
  --note '姓名@x,y' --note '客户编号@x,y' --note 'CH32V305开发板@x,y' \
  --out hints.json --apply-project ceshi --apply-doc PCB1
easyeda apply hints.playbook.json --yes
```

要求：Arial、字高 ≥ 45 mil、顶层丝印、朝向 ≤ 2 个方向、不压焊盘、不重叠、接口与按键有功能提示、
空白处写姓名/客户编号/板名。放不下的位号 `silk-plan` 会列出，不强塞——用 `--only` 与相关提示一起重排。

## 11. 终检与提交

save → `doc reload` → DRC `passed:true` → `pcb check` 0 ERROR 且扣分计数为 0 → `pcb layout-lint` →
实际回读图（`pcb layout render --actual`）或 `pcb stage-snapshot` 目视。记录未达项与理由（例如细间距最小线宽
例外），不把 WARN 当成通过。

## 现场来源

ESP32-S3R8 两层板（ceshi/PCB1，2026-10-17）按本流程现场完成：规则→关键网锁定→晶振包地→GND 扇孔→
官方布线（12 网）→铺铜→缩颈 12 段→泪滴 14 处→丝印 32 条；保存重载后 DRC `passed:true`、`pcb check` 0 ERROR、
0 断头/锐角/非 45°/压焊盘。考题 CH32V305 板未按此流程完整重跑，题面专属步骤（排母符号、DC-DC、USB）以
[260919 样例](examples/260919-at32f415/index.md)为准。
