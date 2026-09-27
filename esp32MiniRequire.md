# ESP32 模组设计要求

> 2026-09-27 起，本文件作为四层开发板**扩展回归**保留；日常测试收尾改用
> [最小点灯板](docs/test-case-esp32-blink.md)。第一节原始需求及历史结果不变，以下 runbook
> 只服务于明确选择本扩展用例的任务，不再要求每次收尾完整执行。

> **这份文件有两部分**：上半是**客户原始需求**（喂给 agent 的唯一输入），
> 下半是**怎么把这个 Demo 跑完**（给人看的 runbook）。
> 跑 Demo 时**只把「一、客户原始需求」交给 agent**——它故意不含 BOM / 器件 UUID /
> 网表 / 选型，喂加工过的答案就不叫真实场景了。

---

## 一、客户原始需求

### 设计规格
- **层数**: 4层板
- **丝印**: 必须包含丝印信息
- **GND层**: 2层接地（GND）
- **内电层**: 1层内电层
- **电源层**: 1层 VCC 电源层

### 供电与电源
- 板载 **5V 供电端子**（接线端子），支持外部 5V 输入
- 插 USB 时也能供电（与端子共用一路 5V 即可）
- 加一个**降压模块**，把 5V 降到 3.3V 给主控供电

### 下载与调试
- 板载 **CH340** USB 转串口，插上 USB 就能烧录固件 / 看串口
- 2 个基本按键：**BOOT**（进下载模式）和 **RESET**（复位）

### 点灯
- 要求能点灯，加一个 LED
- 在LED附近添加丝印标记，以清晰指示正负极 (+ 和 -)

### 结构固定
- 板子**四角各留一个 M3 螺丝孔**，方便固定安装

### 其他要求
- 设计应符合电气规范
- 确保良好的信号完整性
- 考虑散热设计
- 提供必要的接口和连接器

**Created by**: github:zhoushoujianwork/easyeda-agent

---
---

# 二、怎么跑完这个 Demo

这是本项目的**四层扩展端到端用例**：从上面客户原话出发，让 agent 自己选型 → 源连接/归属/约束
→ 区内及纸张计算 → Apply/数据验收 → 转 PCB → 布局/布线/铺铜 → DRC → 落盘，
完整回归跑 S0–S6 + P0–P10。原理图专项验收按当前任务止于 S6，不称整板通过。
日常默认回归使用最小点灯板；涉及本板对应能力的改动按仓库要求补扩展回归；
纯文档同步不冒充现场回归结果。

> **本节只写这个 Demo 特有的东西。** 通用规则（环境自举、铁律、阶段定义、停点、
> 档位默认、块地图、各命令签名）**正本都在 skill 里**，这里只给指针——照抄一份必然漂移。
> 入口：[`.agents/skills/easyeda-agent/SKILL.md`](.agents/skills/easyeda-agent/SKILL.md)

## 0. 环境（一次性）

三样东西缺一不可：**CLI/daemon**、**EasyEDA 里的连接器插件**、**外部交互权限**。
安装与版本对账见 [Skill 入口](.agents/skills/easyeda-agent/SKILL.md) 和
[environment-setup.md](.agents/skills/easyeda-agent/references/environment-setup.md)，不依赖旧章节编号。

只强调最容易翻车的一条：**sideload 的 `.eext` 同 uuid 更新必须先卸载旧的**，
且导入后要**完全退出重启 EasyEDA**——否则已开窗口还在跑旧代码并抢 daemon 的 socket。

```bash
easyeda health        # 检查连接与实际运行版本；有窗口不等于现场数据已验证
```

看到 `windows: []` 就是连接器没附上，回头查权限和重启，**别往下跑**。

## 1. 准备工程

在 EasyEDA Pro 里**新建一个空工程**（建议命名 `ceshi`，本仓库把它当一次性测试工程，
可随意清空/重建），打开它。工程里有一页空原理图和一块空 PCB 即可。

后续所有命令都带 `--project ceshi` 定位窗口——**别用 `--window <id>`**，windowId 每次
重连都会变。

## 2. 起跑

把**「一、客户原始需求」那一段**交给 agent，并要求它按
`.agents/skills/easyeda-agent/references/design-flow.md` 的流程脊柱走。一句话就够：

> 按 esp32MiniRequire.md 的客户原始需求，在工程 ceshi 上跑完整的 S0–S6 + P0–P10，
> 分段验收，每段过门后存盘。

## 3. 分段验收（**不要追求一次跑通**）

原理图阶段统一遵守 [数据驱动架构基准](.agents/skills/easyeda-agent/references/schematic-data.md#数据驱动架构基准)：
保留原始快照，目标副本表达连接/核心外围归属/约束，区内及纸张计算后固定转换与 Apply。
问题由数据检查发现，回改源数据/采集/算法再重算；位号参与、非位号属性文字排除页面布局检查。
本节仍是给人的 runbook，不进入第一节客户原始需求，也不提供预制器件/网表答案。

一个重操作跨越太多步时，单次失败的爆炸半径太大（实测出现过 `zone-arrange` 把页面
留在 26 脚断线状态）。按段走，**每段独立验收、独立存盘，段间可以中断**：

| 段 | 验收标准 | 存盘点 |
|---|---|---|
| S0 | `easyeda spec validate .easyeda/s0-ceshi.json` 无 ERROR，且方案书经你确认 | spec 落盘 |
| S1–S3 | 原始快照/源目标/参数/版本/哈希齐全；核心外围归属、真实直连、区内/纸张几何数据校验通过 | 保留完整计算输入/输出，不把离线通过当已落图 |
| S4–S6 | Apply 后逐项数据对账、逐页 `easyeda sch gate --strict --doc <页>` 为 pass；位号/框/标题和生成溯源检查齐全，缺测不放行 | 显式 save 并核实 `saved:true` |
| P0–P6 | 板框 + 四角 M3 孔 + 天线全层 keepout + `pcb layout-lint --gate` 通过 | 每档 `pcb stage confirm-tier`，四档齐后 `confirm-layout` |
| P7–P10 | 布线 + 4 层电源树 + 铺铜 + `pcb drc` 0 fatal + `pcb check` 无 ERROR | 每步 `pcb save` + `doc reload` |

### 一轮只记录不修

跑 → 发现问题 → 立刻修 → 重跑 → 又发现 → 又立刻修，**永远不收敛**。
发现阻塞立即停止后续写入，保留 finding/失败输入；可补只读诊断后集中决定源数据或算法修复。
重算并验证通过才恢复下游，不继续写完整段，也不在现场逐件试凑。挂账不降时检查根因与覆盖。

## 4. 你会被问到的决策（agent 猜不了，必须你拍板）

这些需求里没写死，是**真实权衡**，agent 应当摊开选项+坑+推荐让你选，而不是替你默认。
**这里只列题目，不给答案**——给了就等于喂加工过的答案：

| 停点 | 要你决定什么 |
|---|---|
| S0 · 降压拓扑 | 需求只说「加一个降压模块」，没指定 LDO 还是同步 buck |
| S0 · 5V 合轨 | 「与端子共用一路 5V 即可」——直接并联？二极管 OR？防倒灌那颗放哪一侧？ |
| S0 · 叠层与地策略 | 需求写「4层板 / 2层接地 / 1层内电层 / 1层VCC层」，4 层只有 2 个内层，**字面加起来超编**，要定一个解释 |
| S1–S3 · 分页 | 某一页装不下时，拆页是设计决策，工具只会停手不会自己拆 |
| P2 · 板框尺寸 | 需求没给尺寸。让 agent 按最小包络算，还是钉死一个常见尺寸？ |
| P2 · 装配工艺 | 单/双面、手焊还是回流——这决定 `layout-lint --gate` 的间距门档位 |
| P2 · 接口边序 | ESP32-S3-WROOM-1 的 PCB 天线必须独占一条边且全层禁铜，剩下三边怎么分 USB-C / 5V 端子 / 按键与 LED |
| P7 · 布线档 | 稠密板要不要停手让你在 EasyEDA 菜单里点原生自动布线 |

已确认选择与授权继续有效，不重复索取；缺失且实质影响设计时再问。
现行流程见 [design-flow.md](.agents/skills/easyeda-agent/references/design-flow.md)，
决策依据见 [design-decisions.md](.agents/skills/easyeda-agent/references/design-decisions.md)。
表里这几行只是「这块板会撞到哪几个」的索引。

## 5. 验收（需求条条落实）

跑完对着原始需求逐条核，**只看数据不看截图**：

```bash
easyeda sch nets --all --project ceshi        # 逐网成员：跨页是否真连上
easyeda sch gate --strict --doc <每一页> --project ceshi
easyeda pcb layout-lint --gate --project ceshi
easyeda pcb layers --project ceshi            # 4 层 + 内电层网络
easyeda pcb drc --project ceshi               # 0 fatal
easyeda pcb check --project ceshi             # 无 ERROR / power-not-poured / width-under-spec
easyeda call pcb.silk.list --project ceshi    # LED 旁 +/- 极性标记，且落在器件本体之外
```

> ⚠ 跑 `pcb *` 之前先确认**前台是 PCB**（`easyeda doc switch <pcbUuid> --project ceshi`）。
> 前台停在原理图页时，丝印类动作会报一句毫不相干的
> `Cannot read properties of null (reading 'map')`，极易被当成连接器崩溃去追。
> 另：`pcb.silk.list` 目前只有 typed action、没有 Cobra 子命令，所以走 `easyeda call`。

判据：**0 overlap、0 fatal、网络连通、丝印/极性正确、4 层电源树成立、已落盘**。

`sch nets` 那条最容易漏——**跨页网名不一致是隐形杀手**：块之间的默认网名并不统一
（有的出 `+3V3`、有的要 `3V3`），逐页判据结构上看不见，主控没接上电也照样全绿。
S0 阶段就该定一张唯一网名表，之后每次落块显式 `--bind` 到表里的名字。

## 6. 已知会撞上的坑（不是你操作错了）

跑之前先知道这几条，能省下大量排查：

- **页名改不动**：`sch page-rename` 在 EasyEDA 3.2.149 上恒失败（平台
  `dmt_Schematic` 改名族返 `false`，而 `dmt_Pcb` 改名正常）。页的功能身份改用 uuid 钉住。
- **`sch gate --strict` 过不了**：`missing-titleblock` 的唯一处方（写图签）当前被
  design-flow 禁用，平台 DRC 又只回聚合数没法清零。**用非 strict 档**，把这两类如实
  写进交付摘要。
- **P6 可布性门的交叉阈值**：默认 `--max-crossings 8`。这块板有交叉耦合的自动下载电路 +
  UART 收发对接 + USB-C 双侧 CC，**十来处交叉是拓扑性的、挪件消不掉**，要靠 4 层板换层在
  布线期解决。挪到收敛后仍超标时，可显式降级
  （`--max-crossings 16 --min-score 40`）——但**必须写进交付摘要**，别偷偷放行。
这几条**都是这块板/这个平台版本特有的**。属于通用纪律的那些不在这里重复，正本在
`SKILL.md`：PCB 改完必须 `doc reload` 再读 = **铁律 5**（机械强制，不 reload 就读会被拒）；
判对错只看 `list/check/drc/layout-lint` 不看截图 = **铁律 6**；天线 keepout 必须覆盖每一层
= **铁律 10**；门禁机械强制、拒绝消息自带下一步 = **铁律 14**；
「逐页 `sch gate` 一次跑四关，别单跑 `sch check`」= **②流程停点表的第 ② 个停点**。

完整的问题台账见 [`docs/reviews/e2e-round-2026-08-25-findings.md`](docs/reviews/e2e-round-2026-08-25-findings.md)。

## 7. 收尾

测试工程用完清理还原即可（`ceshi` 是一次性的，可直接清空/删除重建）：

```bash
easyeda pcb clear --project ceshi     # 破坏性，会先要确认
easyeda sch clear --project ceshi
```

**每跑完一场端到端记一笔成本画像**（墙钟 / daemon 侧机器时间 / 两者之差各自分开）：

```bash
easyeda audit cost --day <YYYY-MM-DD> --since HH:MM --until HH:MM \
  --label "esp32Mini E2E" --tokens <N> --record
easyeda audit cost --ledger           # 跨批次对比
```

## 8. 把路上撞到的问题反馈回仓库（跑完再统一提）

> **⚠ 本节是给跑测试的「人」看的，不是给 agent 的指令。**
> **agent 不得主动起草或提交 issue** —— 只有用户明确要求时才做。看到问题就往
> 记账文件里写，别提议开单。这是为了防止 issue 泛滥：一轮端到端能记出十几条，
> 全变成单子对维护者是负担不是帮助。
>
> （skill 里对**块库**另有一套已定的反馈闭环——`SKILL.md` 铁律 8 与
> `references/standard-blocks-contributing.md` §七，限 `block-gap` / `block-bug` /
> `block-contribution` 三类，同样必须经用户确认才 `gh issue create`。那套不受本节影响。）

「一轮只记录不修」的另一半是**记完要有人收**。跑的过程中**只记账**，
跑完了由**你**决定哪些值得提、提哪几条。

**提之前先过三条**：

1. **不自动上报**。没有遥测、不回传任何东西。
2. **先合并再提**。十几条挂账里多数是同一个根因的不同表现，按根因合并，别一条一单。
3. **带证据才提得动**。空口「不好用」没法修：贴命令原文、完整回执（含 `error.code` /
  `detail`）、`sch read` / `bridge-check` / `layout-lint` 的相关摘录，以及
  `easyeda health` 里的 CLI / daemon / connector / EasyEDA 四个版本号。

**你决定要提之后，提到哪儿**（仓库已有的模板在 `.github/ISSUE_TEMPLATE/`）：

| 撞到什么 | 用哪个 | label |
|---|---|---|
| 块用出问题（引脚名与 `sch read` 实测不符 / 拓扑错 / 器件停产 / 约束错） | `block-bug` 模板 | `block-bug` |
| 需要的块查不到（`easyeda blocks search` 三个维度都没中） | `block-gap` 模板 | `block-gap` |
| 自己搭了一块验证过的好电路想投稿 | `block-contribution` 模板 | `block-contribution` |
| CLI / daemon / 连接器本身的缺陷（命令报错、写了不回滚、报文指错方向、门禁判据不一致…） | 开普通 issue | `bug` |

普通 bug 的标题建议写成「**现象 + 触发条件**」而不是「XX 坏了」，例如
`sch group-move --ids 报电气自检失败却不回滚，留下悬空脚`。
能机械复现、不需要真机 DRC 验收的，可以再打 `ready-for-agent` 交自动化处理
（需要连着 EasyEDA 才能验收的**不要**打这个标签——见
[`docs/reviews/e2e-round-2026-08-25-findings.md`](docs/reviews/e2e-round-2026-08-25-findings.md) 的写法示例）。

```bash
gh issue create --repo zhoushoujianwork/easyeda-agent \
  --label bug --title "<现象 + 触发条件>" --body-file <草稿.md>
```

> 上游（嘉立创 `pro-api-sdk`）的问题另走一条路，登记在 `docs/upstream-issues.md`，
> **不要**直接往上游开单。
