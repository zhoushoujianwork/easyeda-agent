# 贡献电路块 —— Standard Circuit Blocks 共建指南

`references/blocks/` 是一个**社区共建、署名可追**的电路块库(**一块一文件**)。它把「固定
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
- **器件指回 `standard-parts.json`**:块不重复存 LCSC C 号,料号单一来源。

字段完整定义见 `references/blocks/_schema.json`。六段:`parts` / `internal_nets`
/ `ports` / `schematic_notes`(原理图链接注意)/ `pcb_layout`(PCB 电气特性)/ 元信息。

---

## 二、文件结构(一块一文件)

```
references/blocks/
  _schema.json                   # 共享 _doc / _schema / libraryUuid(下划线开头 = 非块)
  ch340c_usb_serial.json         # 一块一文件,文件名 = block.id 去掉 block. 前缀
  esp32_autodownload.json
  esp32s3_wroom1_module.json
```

- **文件名 = `id` 去掉 `block.`**:`ch340c_usb_serial.json` ↔ `"id":"block.ch340c_usb_serial"`。
  `blocks.py validate` 强校验这个约定。
- **为什么一块一文件**:社区模型是**一块一 PR**,文件边界切到块 = **零合并冲突** +
  **一文件一作者的干净 git-blame 署名**。`category` 只是**字段**(`blocks.py ls --category`
  过滤),不做目录边界。
- **`blocks.py` 是唯一 loader 接缝**:它 glob 这个目录、跳过 `_` 开头的文件、把所有块
  组装成库。使用方(agent、将来的 `sch block apply`)不感知库是多文件——所以以后再怎么
  组织目录都是内部变更,不破坏接口。
- **加新块 = 加一个新 `<id>.json` 文件**,不动别人的文件。

---

## 三、贡献门槛(硬标准 —— 达不到 PR 不合入)

1. **拓扑必须来自可信源,不凭记忆手写。** `source` 必填,取自:
   - 官方参考设计(`official-ref:<vendor>`)/ 器件手册应用电路(`datasheet:<mpn>`)
   - **验证过的开源板**(`oshwhub:<url>` —— 见 skill 的 oshwhub 抄图训练闭环)
2. **必须跑过一次全流程验证,`validated` 才能填、块才能标为「已入库」。**
   验证 = 在真实工程(用 `ceshi`)跑 `place → wire → sch check → DRC = 0`,并**读回网表逐网核实合并**,
   `validated` 记成 `ceshi <date> by @you: place→wire→check→DRC=0 + netlist proof`。
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
- [ ] 新块是 references/blocks/<id>.json 一个新文件,文件名 = id 去掉 block. 前缀
- [ ] source 填了,且是官方 ref / datasheet / 验证过的开源板(非凭记忆)
- [ ] 用到的新器件已进 standard-parts.json(带真实 LCSC C 号)
- [ ] 引脚全部用功能名,不含引脚编号(并在验证时用 sch read 核实真实符号脚名)
- [ ] 六段齐全;pcb_layout 是结构化规则(每条带 severity)
- [ ] 已在 ceshi 跑过 place→wire→sch check→DRC=0,validated 已填(或明确标 draft)
- [ ] author/added/updated 已填;改他人块时把自己加进 contributors
- [ ] 一个 PR 只含一个块
- [ ] `python3 scripts/blocks.py validate` 通过(校验 schema + 文件名↔id + parts 交叉引用)
```

---

## 六、上手最快的路径

照 skill 的 **oshwhub 抄图训练闭环**抄一块官方开源板:抄的过程本身就产出一个**已验证**
的块 —— 网表机械对照通过 + DRC=0,顺手加一个 `references/blocks/<id>.json`,一次训练
同时是一次贡献。这是本库最推荐的贡献来源。
