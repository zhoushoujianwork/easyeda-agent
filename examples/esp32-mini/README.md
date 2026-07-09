# esp32-mini 全流程回放(可验证样例)

一份 30 行需求([`esp32MiniRequire.md`](../../esp32MiniRequire.md))对应的**完整可回放
工程**:两阶段 playbook,任何人拿到仓库即可在自己的 EasyEDA Pro 里从零重建这块四层板,
并跑过全部门禁。效果图/复盘见 [`docs/showcase-esp32-mini.md`](../../docs/showcase-esp32-mini.md)。

## 前置

1. 安装 CLI + 连接器(仓库 README),EasyEDA Pro 开启「允许外部交互」,`make dev` 起 daemon;
2. EasyEDA 打开一个**测试工程**(如 `ceshi`),含一个空白原理图页(默认 `P1`)和
   一个绑定的空 PCB(默认 `PCB1`,Board 已关联);
3. `easyeda daemon health` 确认窗口已连接。

## 阶段一:原理图(47 步,约 3-8 分钟)

```bash
make replay-sch                       # 默认 PROJECT=ceshi DOC=P1
make replay-sch PROJECT=xx DOC=P2     # 或指定工程/页
# 等价裸命令:
easyeda apply examples/esp32-mini/schematic.playbook.json --project xx --doc P2 --yes
```

清页(确认门控)→ 19 器件库放置+位号(capture 接线)→ `autoconnect --spec` 落 64 个
网络标志 → **layout-lint 门 + DRC 门内嵌**,不过即停。完成后可用
`easyeda sch read` 自验:19 parts / 13 nets(GND=23、+3V3=11、+5V=5…)。

## 阶段二:PCB(186 步,约 10-20 分钟)

```bash
make replay-pcb                       # 默认 PROJECT=ceshi DOC=PCB1
```

放置(评审面板胜出的布局坐标)→ 4 层 → 板框/四角 M3/天线逐层禁铜 → 位号对齐 →
89 tracks + 37 vias(金板逐条导出)→ +5V via 桥键合 fill → **铺铜间距余量
10→12mil**(`rules-pour-margin` 步)→ 铺铜序列(pour-while-SIGNAL → 翻内电层 →
rebuild)→ **`doc reload` + 二次 rebuild**(`reload-pcb`/`pour-rebuild-2` 步,
新建 PCB 的 reflow 规则快照只在文档重开后刷新,见下方已知问题)→ LED 极性/板注
丝印 → **lint≥95 门 + `pcb check --strict` 门**。末步官方 DRC 需 EasyEDA 前台;剩 1 条 Netlist Error 为平台
[#33](https://github.com/easyeda/pro-api-sdk/issues/33),预期内。

注意:**uniqueId 对齐**:sch↔PCB 关联键。全新工程按放置顺序即 `gge1..gge19`(默认值);
复用过的工程先 `easyeda sch read` 看真实 uniqueId,再 `--var UID_U1=ggeNN` 逐个覆写。

## 其他玩法

```bash
easyeda apply <playbook> --dry-run          # 只看计划
easyeda apply <playbook> --resume           # 中断后续跑(含变量恢复)
easyeda apply <playbook> --step-delay 1     # 逐步慢放(演示/录屏)
make demo-replay                            # 挪乱4件→观察→回放归位 演示
```

## 文件

| 文件 | 说明 |
|---|---|
| `schematic.playbook.json` | 阶段一(生成物,勿手改) |
| `pcb.playbook.json` | 阶段二(生成物,勿手改) |
| `sch-connect.spec.json` | autoconnect 批量连接 spec(位号:引脚级,天然可复现) |
| `generate_playbooks.py` | 生成器(改布局/布线后重新生成,头部有用法) |
| `moves.playbook.json` | `audit export --playbook` 的录制样例(demo-replay 用) |
| `demo-replay.sh` | 挪乱→回放演示脚本 |

> 已验证(2026-07-04):
> - **阶段一**:两次全新环境(新页 + 新板)47/47 通过,重建网表与金板**逐位一致**,
>   uniqueId 确定性成立(fresh 板 = 默认 gge1..19,零覆写)。
> - **阶段二**:182/182 步执行成功;铜几何与金板逐条一致(89 tracks / 37 vias / 5 pours),
>   `layout-lint` 100/100、`pcb check` 0。

## 已知问题:新 PCB 上铺铜 reflow 行为不一致(已固化 workaround)

在新建 PCB 上回放后,官方 DRC 报 GND 热焊盘未生成(No Connection)+ 铺铜到焊盘
~9.7mil(<10 规则)。**已排除**:DRC 规则(与金板逐键一致,仅浮点尾数差)、叠层
(4 层 + L15 PLANE 相同)、pour 图元属性(fill/priority/silos 相同)、创建时机
(同规则下重新 pour-fit 复现)。金板同期复测依旧全绿。

**根因(2026-07-04 探针轮次#3 定位)**:**新建(本次会话内创建、从未重载过的)PCB
文档,铺铜 reflow 用的是创建时的规则快照** ——之后写规则(读回已生效)、重灌铺铜、
tab 切走切回,统统不影响 reflow 结果;只有**真正关闭+重开文档**
(`dmt_EditorControl.closeDocument` + `openDocument`,即 `easyeda doc reload`)后,
reflow 才按当前规则计算(间距与热焊盘同时恢复正常)。已重载过的文档(如经历过
EasyEDA 重启的 PCB2)写规则即时生效,无需重载 —— 这解释了此前"同工程两块板行为
不同"的全部现象。仍属平台缺陷(候选官方 issue:快照不随规则写入失效)。

**已固化的 workaround(playbook 已内置,全新板回放直接过)**:
1. `rules-pour-margin` 步:`easyeda pcb drc-rules-set --pour-clearance 12`
   (raise-only)把 `Plane` 的 `lineClearance` 10→12mil,给打折的 reflow 留余量;
2. `reload-pcb` 步:`easyeda doc reload`(内部先 save,不丢编辑)刷新规则快照;
3. `pour-rebuild-2` 步:重载后二次重灌,此时 reflow 按 12mil + 正常生成热焊盘。
实测:ceshi/PCB3 全新回放,重载前 DRC 55(21 间距 + 33 开路 + 1 网表),重载+重灌后
**DRC 1**(仅剩已知 add-component 网表常驻误报 #33)。
两个 API 陷阱:①`overwriteCurrentRuleConfiguration` 必须传**裸 config 内容**
(`getCurrentRuleConfiguration()` 返回的 `{name, config}` 整个传入会**静默失败**,
resolve `undefined` 且读回不变);②系统预设 `JLCPCB Capability(...)` 不可修改,
写入成功后当前配置自动变为「自定义配置」。
