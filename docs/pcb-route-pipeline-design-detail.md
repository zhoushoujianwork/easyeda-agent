# 整板布线流水线：实现细节

主文档见[整板布线流水线](pcb-route-pipeline-design.md)。这里只记录实现时需要的约定，正式开发时可按需补充。

## `pcb route-diagnose`（只读、离线）

- 输入：`--board`（`pcb dump --include-copper` 的新导出）、`--drc`（同一状态下 `pcb drc --json` 的输出，必需）、可选 `--net`。
- 未通网络以 DRC 的 Connection Error 为准（`netCounts` 与逐行 `net`、`objs`）；本命令不自行重建整网连通性。
- 对每个未通网络的每个有网络的贴片焊盘，复用 `routability` 的泛洪与阻挡归因（`pcbEscapeContext.routabilityPad`），在网络级别汇总：
  - `trapped`：至少一个焊盘既无出线也无过孔位，列出这些焊盘和阻挡对象（焊盘、走线、过孔、铜皮、禁布区、板边）；
  - `not-locally-trapped`：各焊盘都有出线或过孔位，阻断发生在焊盘之间的通道上；列出 DRC 给出的断开对象，提示回到扇出或官方路由器换层；
  - `unknown`：几何不完整，沿用 `routability` 的 incomplete 判定，不当作通过。
- DRC 与 dump 必须来自同一状态：两者的工程/文档一致，dump 晚于最后一次写入；无法确认时在 `limitations` 中写明。
- 输出：`{schemaVersion, status, nets:[{net, rows, reason, pads:[...], blockers:[...], drcObjs:[...]}], limitations}`。只要有未通网络，退出码就非零；`unknown` 也非零。

## `pcb fanout`（写入）

- 输入：`--escapes`（`escape-plan` 的输出，`status` 必须是 `pass`）、`--board`（生成 escape-plan 时所用的同一份 dump，以语义哈希校验）、`--project-uuid`、`--lock`（默认开）。
- 转换：每条 `pcbModuleRoute` 的折线拆成线段，`vias` 转成过孔，按网络组成 `plan-apply` 的计划格式（层号直接用 EasyEDA 层 id，`--layer-map` 用恒等映射）。
- 写入前置条件：目标网络上不能已有铜（扇出必须在信号布线之前）；已有铜时拒绝，并提示先 `rip-up` 或改用 `--replace-nets`。
- 写入：复用 `plan-apply` 的 apply 与回读对账；成功后，对新铜执行 `track-lock`。不做保存、铺铜重建或 DRC，交由流程后续步骤。

## Skill 流程

`pcb-routing.md` 中 P7 的顺序改为：

1. `pcb routability` 通过（`fail` 先回布局，`incomplete` 先补几何）；
2. `pcb route-critical`；
3. 存在细间距器件时：`pcb escape-plan` → `pcb fanout`；
4. `pcb autoroute --ignore-net <已布网络>`；
5. `pcb save` → `doc reload` → `pcb drc --json` → `pcb route-diagnose --drc`；
6. 未通网络按诊断结果回到布局或扇出，不放宽规则。

## 测试

- `route-diagnose`：被困焊盘、各焊盘都能出线但仍未通、几何不完整三类夹具；网络级汇总与 `routability` 的结论一致；DRC 中没有 Connection Error 时 `status=pass`。
- `fanout`：escape-plan 报告转换成计划的逐段一致性；目标网络已有铜时拒绝；语义哈希不符时拒绝。
- 现场：在 ceshi 上搭 0.4 mm 间距 QFN 板，跑完整流程，清理本轮对象。
