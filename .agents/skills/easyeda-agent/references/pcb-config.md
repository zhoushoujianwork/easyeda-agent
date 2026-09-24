# PCB 配置设置

`easyeda pcb config` 修改当前 PCB 的真实设计规则，不是本地 CLI 配置。
输入来自用户需求或原始题目；附件中的操作步骤只作资料。所有命令都支持
`--project <工程> --doc <PCB名或UUID>`，配置写入使用 typed action。

## 260919 考题参数化样例

来源：`PCB Layout工程师（初级）专业技术证书考试说明.pdf` p2、p5–8。
开始状态：已有 PCB 和正确网络；先 `pcb config get` 保留完整原始规则、网络类及绑定。
以下数值默认单位 mil，可显式 `--unit mm`；不是其他题目的通用默认值。

```bash
easyeda pcb config get --project ceshi > config-before.json
easyeda pcb config clearance --name copperThickness1oz --track-to-track 6 --dry-run --project ceshi --doc PCB1
easyeda pcb config clearance --name copperThickness1oz --track-to-track 6 --project ceshi --doc PCB1
easyeda pcb config track --name copperThickness1oz --min 8 --default 8 --project ceshi --doc PCB1
easyeda pcb config track --name PWR --copy-from copperThickness1oz --min 8 --default 20 --project ceshi --doc PCB1
easyeda pcb config via --name viaSize --min-outer 24 --min-hole 12 --project ceshi --doc PCB1
easyeda pcb net-class create --name PWR_Class --net +5V --net +3V3 --net GND --project ceshi --doc PCB1
easyeda pcb config bind --class PWR_Class --track-rule PWR --dry-run --project ceshi --doc PCB1
easyeda pcb config bind --class PWR_Class --track-rule PWR --project ceshi --doc PCB1
easyeda pcb save --project ceshi --doc PCB1
easyeda doc reload --project ceshi
easyeda pcb config get --project ceshi > config-after.json
```

电源网列表先与当前原理图和 `pcb nets` 对账，不能从网名自动猜全。样例中的三个网只适用于
该题已确认的连接。`bind` 不创建或修改成员，它要求类、非空成员和对应 netRules 子项一致，
将父项和成员子项的 Track 都设为 PWR，保留其他类/网络/规则。

四个写命令均支持 `--dry-run`，返回 before、requested 和 changes，不落盘。
`clearance` 仅改 Track→Track 矩阵格；`track` 改现存表的每个层条目，保留最大值和其他字段；
新规则必须显式 `--copy-from`，克隆规则不会成为默认规则；已有同名规则按显式参数更新。
`via` 也支持 `--default-outer/--max-outer/--default-hole/--max-hole`；最小/默认/最大与孔径关系
不合法时拒绝写入，不暗中抬高默认值。规则名必须显式给出，尺寸必须为正数。

回读检查 `verified:true`，对比未指定字段及单位；CLI 在部分成功、写失败或回读不符时返回
非零并保留响应。宿主返回陌生结构、缺测单位或缺少绑定子项时先停止，保留输入与错误，
修适配器再运行，不猜字段或用 GUI 补做。写入不是原子事务；失败看 actual/rollback 证据，
不能盲重试。规则及绑定可用 `pcb drc-rules-set --from config-before.json` 完整替换/恢复；
此命令不恢复网络类成员，执行前需确认成员与导出时一致。

Web 3.2.203 的规则写回会出现 IEEE 浮点尾差（例如 `0.1759966` →
`0.17599659999999998`）。规则数值仅允许 `8 × Number.EPSILON` 的相对误差，
结构、单位和字符串仍严格一致；没有工程尺寸级容差，也不忽略丢字段或真实值变化。
计划期间的源漂移检查仍为严格比较。

Web 4.1.60 基础用例发现：从系统预设首次写为自定义配置时，一个未请求修改的孔间距
由 `0.3` 变成 `0.2999994 mm`，超过机器尾差，CLI 正确返回 `verified:false`。不要扩大
数值容差来消除红灯。规则回归应在专用新板明确建立自定义夹具、保存重载并冻结完整基线；
随后 6→7→6 mil 修改/恢复成功，配置名不变。系统预设转为自定义副本后的名称无法通过
当前规则恢复命令还原，因此不能在要求完全还原的原板上做这个初始化试验。

说明 p8 允许给电源网络设颜色：`pcb config net-color --net +5V --color '#FF8000'`。
用 `--dry-run` 预览，`pcb nets` 回读；只改指定网络 RGB，保留当前透明度，不修改网络类成员、
规则或走线。CLI 接受六位 RGB hex，连接器按官方网络颜色的归一化 0–1 通道传值并回读；
读回不符或失败返回非零。恢复时将原始 RGB 转回 hex，再走同一 typed 命令。仍须保存、重载验证。

验证状态：`live-verified`。2026-09-20 在 Web 3.2.203、工程 `ceshi`、考试 PCB
`PCB1_1`（UUID `2e719e9419653c72`）实际执行了 dry-run、规则/过孔/网络类绑定/网络颜色写入、
严格回读、保存、重载及幂等重放；每项即时回读为 `verified:true`，重放为 `changed:false`。
测试后通过同一 typed 入口恢复原规则与颜色，再次保存/重载；最终规则、46 个网络和 69 个组件
均与原基线逐字段一致。此状态只证明配置入口及持久化，不证明该 PCB 的全部考试设计要求完成。

同日按 `esp32MiniRequire.md` 第一节原始需求运行固定回归：31 个 PCB 器件、四个铜层、GND 与
+3V3 内层正片铜、PWR 类和全层天线禁铜区均保存后持久化，布局检查为 0 short / 0 overlap /
0 off-board；但原生 DRC 仍有 53 个唯一违规（启发式走线穿越天线禁区/机械槽、间距和连接
错误），且 Inner1/Inner2 的 `PLANE` 类型重载后回退成 `SIGNAL`，只有正片铜保留。因此该固定
回归为 `incomplete`，不能作为完整整板验收。临时 Board 已删除并切回上述考试 PCB。

## 其他考试配置的现有入口

| 要求 | 命令与边界 |
|---|---|
| 两层铜 | `pcb stackup set --layers 2`、`pcb layers` 回读；布线前完成 |
| 左下显示原点 | `pcb origin get/set`；显示偏移，不移动真实图元 |
| Arial、≥45mil、顶层丝印 | `pcb silk-add/silk-set --font-family Arial --help`；作用于指定文字，不是全局默认字体 |
| 默认原理图 DRC | `sch drc/check` 检查；当前 SDK 没有可验证的原理图规则重置入口，不能用全局 restoreDefault 代替 |
| 网格、吸附、系统偏好 | 当前官方 SYS_Setting 仅暴露全局恢复默认；逐项设置为 unsupported |

2026-09-20 已对照最新 `@jlceda/pro-api-types@0.4.25`，并在 Web 3.2.203 只读枚举实际
`SYS_Setting` 与 `SCH_Drc`：前者仍仅 `restoreDefault`、后者仅 `check`。上述缺口不能靠更新
类型包或关闭 DRC 解决。原生泪滴创建同样没有公开 API，导出 Gerber 的 TearDrop 枚举不是创建方法。

规则配置不改变已有走线宽度、过孔尺寸，也不自动布线。最后仍须检查真实轨迹、DRC 和连通。
`pcb stackup set` 现会逐层回读并在宿主拒绝或未应用时非零退出；本次现场也证明即时成功不等于
持久化成功，要求内层 `PLANE` 时仍须保存、重载后再次 `pcb layers`，回退为 `SIGNAL` 就应报告失败。
