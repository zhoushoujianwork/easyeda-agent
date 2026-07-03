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

## 阶段二:PCB(182 步,约 10-20 分钟)

```bash
make replay-pcb                       # 默认 PROJECT=ceshi DOC=PCB1
```

放置(评审面板胜出的布局坐标)→ 4 层 → 板框/四角 M3/天线逐层禁铜 → 位号对齐 →
89 tracks + 37 vias(金板逐条导出)→ +5V via 桥键合 fill → 铺铜序列
(pour-while-SIGNAL → 翻内电层 → rebuild)→ LED 极性/板注丝印 → **lint≥95 门 +
`pcb check --strict` 门**。末步官方 DRC 需 EasyEDA 前台;剩 1 条 Netlist Error 为平台
[#33](https://github.com/easyeda/pro-api-sdk/issues/33),预期内。

⚠️ **uniqueId 对齐**:sch↔PCB 关联键。全新工程按放置顺序即 `gge1..gge19`(默认值);
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

> 已验证:阶段一在全新页上 47/47 步通过,重建网表与金板逐位一致(2026-07-04)。
