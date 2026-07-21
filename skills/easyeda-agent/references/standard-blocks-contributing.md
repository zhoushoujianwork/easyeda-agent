# 贡献电路块 —— Standard Circuit Blocks 共建指南

`internal/blocks/data/`(repo 内,不随 skill 分发)是一个**社区共建、署名可追**的电路块库(**一块一文件**)。它把「固定
模块的外设电路」(CH340 USB 串口、ESP32 自动下载、ESP32-S3 模组、按键去抖、USB-HUB、
降压……)沉淀成**可直接照抄、只需重绑边界网络**的知识资产。

> **一次学习贡献,永久收益。** 你抄通、验证过的一个块,合入后带着你的 GitHub @handle
> 永久留在库里 —— 之后每一块板子的每一次复用,都是你这次贡献的收益。库会随器件、封装、
> 版本演进持续更新,但**署名不删**。

---

## 一、一个「块」是什么

块 = **固定内部拓扑** + **可重绑边界(ports)** + **器件角色** + **布局电气约束**。

- **内部拓扑固定**:块内 part↔part 的连线永不变(零参数)。
- **边界可重绑**:变的只是块对外的几根线接到主控哪个网络(改网络名,**不改引脚号**)。
- **引脚一律用功能名**(`CH340.TXD`),不用引脚编号 —— 符号级稳定,复用零改号。
- **同名多脚加 `*` 全并联**(`J.VBUS*` / `J.GND*` / `J.EP*`,#145):连接器天生同一功能占多脚
  —— USB-C 16P 有 2×VBUS、2×GND、4×EP,排针/屏蔽壳同理。**光写功能名是歧义的**,`sch autoconnect`
  会正确拒绝(它不该替你挑一个),曾因此让 `ch340c_usb_serial` 恒定漏连 5V 少 2 pin、GND 少 6 pin
  ——**VBUS 根本没接上,USB 口实际不供电**,而块还标着 verified。星号是块把「这几脚全并到这个网」
  说出口的方式(USB-C 双取向本就**要求** A/B 两侧都接),而不是让 planner 按网络 kind 去猜。
  **对单脚是恒等**(展开成 1 条),所以电源/地/屏蔽脚可以放心加星,不必先知道器件有几个同名脚。
- **器件指回 `standard-parts.json`**:块不重复存 LCSC C 号,料号单一来源。

字段完整定义见 `internal/blocks/data/_schema.json`。核心段:`parts` / `internal_nets`
/ `ports` / `schematic_notes`(原理图链接注意)/ 元信息,外加一族**可拓展的约束 map**:

- `schematic_layout` —— **原理图摆放模板**(`{note, roles: {<ROLE>: {dx,dy,rotation}}}`):
  role 相对块原点(`--at`)的偏移+朝向,y 向下为正,**dx/dy 必须落 5 格**、rotation 限 0/90/180/270、
  **必须覆盖块内全部 role**(部分覆盖是数据错误,`go test` 校验)。审美约定:信号流左入右出、
  电源上 GND 下、去耦贴主芯片电源脚。有模板的块 `sch block-apply` 直接按模板落件;没有则退回
  盲网格——**贡献新块请尽量带模板**(在真板上摆好一次,把实测相对坐标写回来)。
- `pcb_layout` —— 通用布局规则(列表 `{rule,target,constraint,value,severity}`)。
- `placement` —— **结构件摆放**(按 `<ROLE>` 键):连接器/端子/USB/天线/按键/指示灯等
  **要不要靠板边、靠哪条边(`edge`)、放哪个铜面(`side` top/bottom)**、朝向。
- `signals` —— **信号特性**(按信号组键):差分对/高速/RF/敏感网络的阻抗、等长、隔离等
  (如 USB D± 90Ω 差分、RS-485 A/B 120Ω、RF 50Ω)。
- **加新维度 = 加一张新顶层 map**(如将来 `thermal`/`emc`),同样按 role/net/signal 键、
  每条带 `severity`+`reason`。loader 前向兼容透传,未知 map 不报错;`go test` 校验已知 map。

---

## 二、文件结构(一块一文件)

```
internal/blocks/data/
  _schema.json                   # 共享 _doc / _schema / libraryUuid(下划线开头 = 非块)
  ch340c_usb_serial.json         # 一块一文件,文件名 = block.id 去掉 block. 前缀
  esp32_autodownload.json
  esp32s3_wroom1_module.json
```

- **文件名 = `id` 去掉 `block.`**:`ch340c_usb_serial.json` ↔ `"id":"block.ch340c_usb_serial"`。
  `go test ./internal/blocks/` 强校验这个约定。
- **为什么一块一文件**:社区模型是**一块一 PR**,文件边界切到块 = **零合并冲突** +
  **一文件一作者的干净 git-blame 署名**。`category` 只是**字段**(`easyeda blocks ls --category` 过滤),不做目录边界。
- **`internal/blocks`(go:embed)是唯一 loader 接缝**:它把 `data/` 编进 `easyeda`
  二进制,跳过 `_` 开头的文件、把所有块组装成库。使用方(agent 走 `easyeda blocks`、
  将来的 `sch block apply`)不感知库是多文件、也不必有 skill 文件——离线自包含。
- **加新块 = 加一个新 `<id>.json` 文件**,不动别人的文件。

---

## 三、贡献门槛(硬标准 —— 达不到 PR 不合入)

1. **拓扑必须来自可信源,不凭记忆手写。** `source` 必填,取自:
   - 官方参考设计(`official-ref:<vendor>`)/ 器件手册应用电路(`datasheet:<mpn>`)
   - **验证过的开源板**(`oshwhub:<url>` —— 见 skill 的 oshwhub 抄图训练闭环)
2. **必须跑过一次全流程验证,`validated` 才能填、块才能标为「已入库」。**
   验证 = 在真实工程(用 `ceshi`)跑 `place → wire → sch check → DRC = 0`,并**读回网表逐网核实合并**,
   `validated` 记成 `ceshi <date> by @you: place→wire→check→DRC=0 + netlist proof`。
   ⚠️ **验证必须由 `sch block-apply` 端到端产生,不能手工连线代替(#145 教训)。**
   手工连线是照着拓扑**用眼睛**连的,会绕过块自己的引脚引用,块数据里的错(引脚名歧义/错名/
   漏脚)因此全被掩盖 —— `ch340c_usb_serial` 就这样带着「VBUS 根本没接上」的缺陷挂了
   `verified` 十几天,直到首次用 `block-apply` 跑才现形。**手工验过的只能证明电路对,
   证明不了块数据对。**
   未验证的块允许提交但标 draft(`validated: null`):可带一份**来自官方 ref 的候选 `internal_nets`**
   (脚名待 `sch read` 核实),拓扑本身还没定就把 `internal_nets` 写成字符串 `"pending"`。
3. **器件先入 `standard-parts.json` 再进块。** 块里用到的新料,先补器件库(带真实 C 号),
   `parts.<ROLE>.part` 指向那个 role key。不允许块内内联裸料号。
4. **引脚用功能名,不用编号。**
5. **六段齐全,`pcb_layout` 必须是结构化规则**(`{rule,target,constraint,value,severity}`),
   不写成散文 —— 将来要喂给 `pcb check` 做块级布局校验。
6. **一块一个 PR。** 便于 review、便于署名、便于回滚。

---

## 四、署名与版本

| 字段 | 规则 |
|---|---|
| `author` | 首个贡献者的 GitHub @handle,**永不删除** |
| `contributors` | 后续修正/更新者的 @handle,追加不覆盖 |
| `added` | 首次入库的版本号(如 `v0.6.0`) |
| `updated` | 最后一次改动的版本号(器件/拓扑/规则变更时 bump) |

- **修别人的块**:把自己加进 `contributors`,更新 `updated`,`author` 保持不动。
- **器件停产/换封装**:更新 `parts`(优先用 `alt` 提供等价替代),bump `updated`,
  在 PR 说明里写清替换原因。

---

## 五、PR checklist(贴进 PR 描述)

```
- [ ] 新块是 internal/blocks/data/<id>.json 一个新文件,文件名 = id 去掉 block. 前缀
- [ ] source 填了,且是官方 ref / datasheet / 验证过的开源板(非凭记忆)
- [ ] 用到的新器件已进 standard-parts.json(带真实 LCSC C 号)
- [ ] 引脚全部用功能名,不含引脚编号(并在验证时用 sch read 核实真实符号脚名)
- [ ] 核心段齐全;pcb_layout 结构化(每条带 severity)
- [ ] 有结构件(连接器/端子/天线/按键/指示灯)的块填了 `placement`(板边+正反面)
- [ ] 有差分/高速/RF 网络的块填了 `signals`(阻抗/等长/隔离)
- [ ] 已在 ceshi 跑过 place→wire→sch check→DRC=0,validated 已填(或明确标 draft)
- [ ] author/added/updated 已填;改他人块时把自己加进 contributors
- [ ] 一个 PR 只含一个块
- [ ] `go test ./internal/blocks/` 通过(校验 文件名↔id + 署名 + parts 交叉引用;跟着 `make test`/CI 跑)
```

---

## 六、上手最快的路径

照 skill 的 **oshwhub 抄图训练闭环**抄一块官方开源板:抄的过程本身就产出一个**已验证**
的块 —— 网表机械对照通过 + DRC=0,顺手加一个 `internal/blocks/data/<id>.json`,一次训练
同时是一次贡献。这是本库最推荐的贡献来源。

---

## 七、GitHub Issue 反馈闭环(查 → 报 → 登记)

块库的反馈**不走任何自动上传** —— 一切经用户确认、以公开 issue 的形式进入仓库。
没有遥测:**无人反馈的块默认就处于「正常参考」阶段**,沉默不是异常。

### Agent 的三条纪律

1. **必查**:手工连任何已知外围前先 `easyeda blocks search/show`(SKILL 铁律 8,原有)。
2. **报缺陷**:块用出了问题(引脚名与 `sch read` 实测不符 / 拓扑错 / 器件停产 / 约束错),
   **起草**一份 `block-bug` issue(带证据:sch read 摘录、manifest、DRC 条目),
   **征得用户同意后**再 `gh issue create` 提交。上报是外发动作,永远先给用户看草稿。
3. **登记缺口**:`blocks search` 查不到需要的块时,把「需要什么、查过什么、期望边界」
   起草成 `block-gap` issue,同样经用户确认后提交。设计工作照常继续(手工连线不被阻塞),
   缺口登记是给库的需求地图,不是给用户设的门。

用户自己做出一块好电路想投稿、又不方便提 PR 时,agent 可代为起草 `block-contribution`
issue(块 JSON 草稿 + 一手来源 + 验证状态 + @handle 署名),经确认后提交,维护者代落库。

三类模板在 `.github/ISSUE_TEMPLATE/`:`block-gap` / `block-bug` / `block-contribution`。

### 维护者侧(仓库的策展权就是资产)

- 分诊三类 label;能机械修的挂 `ready-for-agent` 交自动化(操作端边界见
  operator 运行时验收约定:需要真机 DRC 验收的不挂,人工在实时会话处理)。
- `block-bug` 确认后:修块、bump `updated`、上报人进 `contributors` 署名。
- `block-gap` 聚成需求地图,决定下一批块的优先级。
- `block-contribution` 按第三节硬标准审:一手来源必查,验证状态如实标(draft 也收)。

### 块的生命周期(由 issue 驱动,不由遥测驱动)

```
draft(拓扑有一手源,脚名未核实)
  → ready(整板验证 + netlist 核实 —— 自己验的或 issue 带证据反馈的)
  → 正常参考(默认态:无人反馈 = 没出问题)
  → block-bug issue → 修订 → bump updated → 回到正常参考
```
