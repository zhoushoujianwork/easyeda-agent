# 多页原理图高效收敛流程

用于新建多页原理图、已连线页面重排和跨 agent 接力。目标不是减少必要验证，而是把验证
安排在能最早发现问题、又不重复付费的位置。

## 1. S0 冻结门

首个 `page-new`、`place` 或 `block-apply` 之前，必须有一份已落盘并通过
`easyeda spec validate --strict` 的 S0 spec。至少包含：

- 已确认的器件和封装；
- 全工程统一网名；
- 页面集合、模块归属和页面职责；
- 接口朝向、板框、层数、天线方案等会影响后续布局的决策。

spec 文件时间应早于首个 placement。若先画后补，它只是事后记录，不能阻止返工。容量
判断先用真实 sheet、器件 bbox 和 `zone-plan` 六项 validation；不能确认可读性时先拆页，
不要创建 `*_NEW` 页面搬家后再删旧页。

## 2. 一页一个事务

每页按固定顺序收敛：

1. `sch read --page <uuid> --stay` 保存 pin→net 和 NC 基线。
2. 只修改这一页；所有 mutation 显式 `--doc <uuid>`。
3. `sch read --page <uuid> --stay --no-check` 对照基线或黄金表。
4. 跑便宜门：`sch gate --only layout-lint,check,bridge-check --doc <uuid>`。
5. 通过后 `sch save --doc <uuid>`，再进入下一页。

官方 DRC 放在该页完成时或最终验收时跑。不要在每次移动标签后重复跑全 gate。

## 3. 批量操作停止条件

`connect_pin`、`disconnect`、move、delete 和 autoconnect 不是可盲目重放的事务：

- fail/partial 后先 `sch read`，区分成功项、失败项和附带断开的引脚；
- 只重试明确失败项一次；
- 同类失败第二次立即停止，保存输入、stderr、活体 read 和 bridge 报告；
- 不把整个 spec 再提交一遍，否则可能叠加 marker、合并线树或制造短路。

`zone-arrange` 和 `destagger` 都执行 `dry-run 一次 → apply 最多一次`。任何落地断言、黄金
网表或 bridge 变差都停止。重复运行相同变换器不是收敛策略。

## 4. 多页读取和交付

非活动页的 `--all-pages` 数据可能缺 pins/bbox，不能用于严格证明。逐页用
`sch read --page <uuid> --stay`；导出前显式 `sch open --page <uuid>`。页面清单命令默认
已输出 JSON envelope，`sch pages` 没有 `--json` flag。

最终使用：

```bash
python3 <skill>/scripts/schematic-acceptance.py \
  --project <project> \
  --spec .easyeda/s0-project.json \
  --golden .easyeda/pin-net-golden.json \
  --export --artifacts .easyeda/artifacts
```

该脚本一次完成：页集合对账、逐页 strict gate、全工程 `nets --strict`、黄金 pin→net/NC
对账和最终图片导出。失败报告已包含 stage 详情，不要为查看同一 finding 再跑四个单命令。

## 5. v1.1.1 已知缺陷

### 标题栏不可达门

CLI/connector v1.1.1 同时存在三个矛盾行为：

- `sch titleblock --data` 会损坏 sheet 符号引用，保存重启后可能丢图框；
- strict gate 对空标题栏报 `missing-titleblock`，并建议执行上述危险命令；
- `check.summary.missingPartitions` 会把这个 finding 错计为 1。

默认仍将 strict gate 失败视为失败。只有失败 stage 仅为 `check`、finding 仅含
`missing-titleblock`、其余 stage 全 pass，并显式传入 `--allow-titleblock-gap` 时才允许
挂账。这不是忽略其他 WARN 的通用开关，且脚本会核对 CLI 和 connector 都是 1.1.1。

### 容量布尔值误报

若 `zone-plan.capacity.fits` 与 `needW/haveW`、`needH/haveH` 自相矛盾，不要围绕单个布尔值
反复搬动。以六项 validation、实际 bbox、区框重叠和图纸越界为门，并保留矛盾 JSON。

### 审计证据不完整

daemon audit 的失败记录可能只有 action/errorCode，没有输入和错误正文；playbook journal
也只记录顶层 step，可能看不到内部 action 失败。复盘时同时保留 CLI stderr、journal、最终
`sch read` 和 gate JSON，不把 journal 全绿当作电路全绿。

## 6. 跨 agent 接力包

交给另一个 agent 前只提供以下稳定材料：

- 当前页表及 UUID；
- S0 spec、器件 manifest、黄金 pin→net/NC；
- 每页最近一次 gate JSON 和最终导出图；
- 禁止动作与已知挂账；
- 明确的单页 ownership。

接手 agent 首先运行 acceptance 的只读部分。未对齐黄金表前不得修改；修改后只交回该页
的差异和验收结果。
