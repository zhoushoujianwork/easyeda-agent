# PCB 设计规范手册（指针）

正本在 Skill 里（Skill 优先铁律 — 发布包必须自带规范）：

➡️ [`skills/easyeda-agent/references/pcb-design-rules.md`](../skills/easyeda-agent/references/pcb-design-rules.md)

内容：线宽/间距、过孔、布局、走线、铺铜、电源与地、差分对、Mark 点、
工艺边/拼板、丝印、叠层、DRC 三级清单（FATAL/ERROR/WARN）。
基于 JLC 工艺能力 + IPC-2221。

**消费方**：

- `pcb check` 新增 5 条规则的报错信息直接引用该手册章节号（`docs/pcb-design-rules.md §N`）：
  `silk-over-pad` §11.2 / `decap-too-far` §3.1 / `via-in-pad` §2.3 /
  `copper-near-edge` §5.1 / `fiducial-missing` §9（`internal/app/pcb_check_dfm2.go`）。
- 数值型工艺极限的机器可读版：`skills/easyeda-agent/references/fab-rules-jlcpcb.json`
  （daemon 的 DRC fallback 基线，`internal/app/pcb_rules.go`）。
- net-class 线宽阶梯的代码正本：`internal/app/pcb_netclass.go`。
