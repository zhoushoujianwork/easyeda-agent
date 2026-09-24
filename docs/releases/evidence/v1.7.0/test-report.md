# v1.7.0 基础 CLI 发布测试报告

## 结论与边界

本次基础范围 pass：B00–B10 在 dev.21 真实链路全部通过，个人空间及既有自定义规则夹具。
高级 A00–A06 本轮 not-run，用户已决定下一版本验证。已知 bug #256/#257/#258 保持开放。
候选升为 v1.7.0 的版本对应与输入见 [baseline.md](baseline.md)，步骤见 [test-cases.md](test-cases.md)。
报告中的原始命令和 UTC 均来自 dev.21，不声明正式版本现场重跑或完整设计 E2E 通过。

| ID | 结果 | 回读 / 证据 |
|---|---|---|
| B00 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B00/evidence-sha256.json，清单哈希见 baseline.md。 |
| B01 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B01/evidence-sha256.json，清单哈希见 baseline.md。 |
| B02 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B02/evidence-sha256.json，清单哈希见 baseline.md。 |
| B03 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B03/evidence-sha256.json，清单哈希见 baseline.md。 |
| B04 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B04/evidence-sha256.json，清单哈希见 baseline.md。 |
| B05 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B05/evidence-sha256.json，清单哈希见 baseline.md。 |
| B06 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B06/evidence-sha256.json，清单哈希见 baseline.md。 |
| B07 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B07/evidence-sha256.json，清单哈希见 baseline.md。 |
| B08 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B08/evidence-sha256.json，清单哈希见 baseline.md。 |
| B09 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B09/evidence-sha256.json，清单哈希见 baseline.md。 |
| B10 | pass | 本文件后附该项全部实际命令与 fresh 回读结论；本机 artifacts/cli-basic-dev21-20260925/B10/evidence-sha256.json，清单哈希见 baseline.md。 |
| A00 | not-run | 用户在 2026-09-25 明确延期至下一版本验证；本轮没有执行该高级用例，不用历史部分结果补签。 |
| A01 | not-run | 用户在 2026-09-25 明确延期至下一版本验证；本轮没有执行该高级用例，不用历史部分结果补签。 |
| A02 | not-run | 用户在 2026-09-25 明确延期至下一版本验证；本轮没有执行该高级用例，不用历史部分结果补签。 |
| A03 | not-run | 用户在 2026-09-25 明确延期至下一版本验证；本轮没有执行该高级用例，不用历史部分结果补签。 |
| A04 | not-run | 用户在 2026-09-25 明确延期至下一版本验证；本轮没有执行该高级用例，不用历史部分结果补签。 |
| A05 | not-run | 用户在 2026-09-25 明确延期至下一版本验证；本轮没有执行该高级用例，不用历史部分结果补签。 |
| A06 | not-run | 用户在 2026-09-25 明确延期至下一版本验证；本轮没有执行该高级用例，不用历史部分结果补签。 |

## 现场回读

11 项各有原始 stdout/stderr、退出码、UTC 和完整对象快照，252 个冻结文件哈希核对通过。
B09 的 PCB 仅 capturedAt 变化；原理图重铸属性 ID 经逐项映射、完整 SVG 结构和网表同字节验证。
B08 保存重载后规则名称与数值完全恢复，仅网络规则枚举顺序变化；没有放宽真实数值差异。
B10 所有临时 PCB 对象清理后，保存重载与初始完整 dump 对应；临时页删除并恢复原工程 P3。
基础阶段墙钟21.8分钟，218条独立CLI调用95.8秒，daemon审计75.9秒；成本已记录。
完整逐项报告如下，冻结源是 Git 1414784，保留测试输入错误及修正记录。

## 独立复核

独立 offline_review 已只读复核 dev.21 的 B00–B10 原始证据、完整差分和提示/失败退出实现，
支持本轮基础范围 pass。B03 不包含团队，B08 不包含首次规则初始化；B10 主窗口与原工程恢复
有回读，但冻结材料没有 daemon.pid 文件前后快照，不额外宣称该磁盘文件已被独立证明。
本次材料与候选源码对应经同一独立复核员复查通过：3 份材料哈希、11 份证据清单及11份报告哈希一致；
逐项嵌入内容与原报告一致，802 份产品源码与 1414784 相同。此结论支持 manifest 的范围内独立复核 pass。

## 正式包校验

已通过：Go 全量、连接器543项、MCP10项、发布脚本106项、Agent入口13项、Skill、lint与模块目录检查。
正式资产已通过 make release-build / release-check / release-smoke：五平台编译、10项资产哈希、
200份Skill文件与218个包内链接、本机临时CLI的15条离线命令均通过。其他平台仅交叉构建，未声称实机运行。
本机记录在 artifacts/release-v1.7.0/ 的 build-result.json、smoke-result.json、对应日志与源码对比文件。
802份产品源与1414784逐字节一致；连接器bundle仅替换2处版本字符串后与dev.21完全相同，
正式JS SHA-256为14ccc6eb4306639868505ec354ec21cac2adf65c150ffcdba694afbed119de47。
这些离线检查不替代高级现场测试。

## 逐项实际执行记录

### B00：版本、路由与只读预检

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

三方精确 dev.21；独立预检 pass。工程身份、当前文档、原理图与 PCB 列表返回正确 context；前后唯一连接窗口。包与执行文件 SHA-256 已冻结。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B00/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-health.json | 2026-09-24T16:56:29.938472+00:00 | 0.013 | 0 | `/usr/local/bin/easyeda health` |
| 02-version.json | 2026-09-24T16:56:30.034086+00:00 | 0.011 | 0 | `/usr/local/bin/easyeda --version` |
| 03-actions.json | 2026-09-24T16:56:30.152607+00:00 | 0.014 | 0 | `/usr/local/bin/easyeda actions` |
| 04-project.json | 2026-09-24T16:56:30.253635+00:00 | 0.017 | 0 | `/usr/local/bin/easyeda project info --project 0f46d4361c4741a9ab7a9ed62164ca9b` |
| 05-doc.json | 2026-09-24T16:56:30.354983+00:00 | 0.016 | 0 | `/usr/local/bin/easyeda project doc --project 0f46d4361c4741a9ab7a9ed62164ca9b` |
| 06-docs.json | 2026-09-24T16:56:30.453580+00:00 | 0.023 | 0 | `/usr/local/bin/easyeda doc ls --json --project 0f46d4361c4741a9ab7a9ed62164ca9b` |
| 07-sch.json | 2026-09-24T16:56:30.561044+00:00 | 0.026 | 0 | `/usr/local/bin/easyeda sch list --include-wires --include-page-primitives --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 08-pcb.json | 2026-09-24T16:56:30.668826+00:00 | 3.501 | 0 | `/usr/local/bin/easyeda pcb list --include-pads --include-bbox --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 09-open-sch.json | 2026-09-24T16:56:34.270340+00:00 | 0.782 | 0 | `/usr/local/bin/easyeda doc open e453c0063919726b --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 10-health.json | 2026-09-24T16:56:35.171992+00:00 | 0.014 | 0 | `/usr/local/bin/easyeda health` |
| 00-version.json | 2026-09-24T16:56:44.226247+00:00 | 0.012 | 0 | `easyeda --version` |
| 01-health-before.json | 2026-09-24T16:56:44.239312+00:00 | 0.014 | 0 | `easyeda health` |
| 02-actions.json | 2026-09-24T16:56:44.254140+00:00 | 0.014 | 0 | `easyeda actions` |
| 03-project-info.json | 2026-09-24T16:56:44.269232+00:00 | 0.02 | 0 | `easyeda project info --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 04-project-doc.json | 2026-09-24T16:56:44.290019+00:00 | 0.017 | 0 | `easyeda project doc --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 05-sch-list.json | 2026-09-24T16:56:44.307566+00:00 | 0.021 | 0 | `easyeda sch list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 06-health-after.json | 2026-09-24T16:56:44.329308+00:00 | 0.013 | 0 | `easyeda health` |

#### 清理、归因与边界

仅验证基础路由与读取，空页未用于补签对象写读。保留专用夹具工程，接续 B01。

### B01：命令契约与写前参数拒绝

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

75 个命令帮助、160 个 typed action、104 处 Skill 参数引用通过。未知命令和无效 flag 均非零退出；检查前后完整审计哈希相同，零调度。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B01/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|


#### 清理、归因与边界

没有访问或改动设计对象；签名检查不替代现场参数语义测试。

### B02：daemon 自动重连与数据保持

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

原理图与 PCB 均先 saved:true；执行 `/usr/local/bin/easyeda daemon start --auto-update-skill=false` 替换同版 daemon。PID 改变，连接器自动回到原工程/P1，首轮有界查询即精确 dev.21。原理图完整 result 相等；PCB dump 仅 capturedAt 不同，其余全部字段含 semanticSha256 相等。再次切回 P1，最终 health 身份正确。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B02/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-pcb-save.json | 2026-09-24T16:57:30.812263+00:00 | 1.474 | 0 | `/usr/local/bin/easyeda pcb save --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 02-pcb-before.json | 2026-09-24T16:57:32.400733+00:00 | 0.164 | 0 | `/usr/local/bin/easyeda pcb dump --include-copper --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B02/pcb-before.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 03-sch-save.json | 2026-09-24T16:57:32.659905+00:00 | 1.634 | 0 | `/usr/local/bin/easyeda sch save --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 04-sch-before.json | 2026-09-24T16:57:34.405658+00:00 | 0.035 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 05-health-before.json | 2026-09-24T16:57:34.505190+00:00 | 0.012 | 0 | `/usr/local/bin/easyeda health` |
| 07-wait-00.json | 2026-09-24T16:58:01.099365+00:00 | 0.014 | 0 | `/usr/local/bin/easyeda health` |
| 08-sch-after.json | 2026-09-24T16:58:01.186107+00:00 | 0.032 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 09-pcb-after.json | 2026-09-24T16:58:01.290904+00:00 | 1.549 | 0 | `/usr/local/bin/easyeda pcb dump --include-copper --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B02/pcb-after.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 10-return.json | 2026-09-24T16:58:02.925842+00:00 | 0.853 | 0 | `/usr/local/bin/easyeda doc open e453c0063919726b --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 11-health-final.json | 2026-09-24T16:58:03.895117+00:00 | 0.018 | 0 | `/usr/local/bin/easyeda health` |

#### 清理、归因与边界

daemon 持续运行；重启启动记录和启动日志已冻结。此前升级期间旧连接器短暂登记仅列入 setup，不作为本项自动重连失败。

冻结边界：`restart-command-snapshot.json` 与 `daemon-start.log` 保存启动时的命令和日志并列入哈希；持续运行的 `daemon.log` / `06-restart.json` 属运行中记录，不属于本项冻结证据集。

### B03：个人工程创建、查找、打开与导出

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

用户明确“团队先不管”，本轮 B03 范围限定个人空间。个人工程创建成功，UUID 与 info/open/find 一致；find 完整枚举 54 项且精确找到唯一目标；P1 可读、saved:true，原生工程导出 6905 字节，SHA-256 58a7720fbf52636b59d3f0643de0756518e2f8956fd5e8cab0fb637bf2f9b1c4。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B03/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-libraries.json | 2026-09-24T16:58:58.893287+00:00 | 0.032 | 0 | `/usr/local/bin/easyeda lib libraries --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 02-create.json | 2026-09-24T16:58:59.029375+00:00 | 4.624 | 0 | `/usr/local/bin/easyeda project create --window 6e80d20d-2fed-407f-9c25-d6c02719a9d3 --name ceshi-cli-basic-dev21-20260925 --description 'dev.21 基础 CLI 单动作测试专用容器' --open` |
| 03-info.json | 2026-09-24T16:59:26.801118+00:00 | 0.018 | 0 | `/usr/local/bin/easyeda project info --project 7186b88d407441ac83d003db71448462` |
| 04-health.json | 2026-09-24T16:59:26.907382+00:00 | 0.012 | 0 | `/usr/local/bin/easyeda health` |
| 05-docs.json | 2026-09-24T16:59:26.997003+00:00 | 0.017 | 0 | `/usr/local/bin/easyeda doc ls --json --project 7186b88d407441ac83d003db71448462` |
| 06-pages.json | 2026-09-24T16:59:27.085922+00:00 | 0.014 | 0 | `/usr/local/bin/easyeda sch pages --project 7186b88d407441ac83d003db71448462` |
| 07-find.json | 2026-09-24T16:59:27.165474+00:00 | 14.359 | 0 | `/usr/local/bin/easyeda project find --window 6e80d20d-2fed-407f-9c25-d6c02719a9d3 --name ceshi-cli-basic-dev21-20260925 --team f60b2174579745258ada9b72bbe2f52b --timeout 120s` |
| 08-open.json | 2026-09-24T16:59:57.427225+00:00 | 0.749 | 0 | `/usr/local/bin/easyeda project open --window 6e80d20d-2fed-407f-9c25-d6c02719a9d3 --project-uuid 7186b88d407441ac83d003db71448462 --page-uuid a07f2d2566a5eab9 --allow-discard-unsaved` |
| 09-empty.json | 2026-09-24T16:59:58.273825+00:00 | 0.03 | 0 | `/usr/local/bin/easyeda sch list --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 10-save.json | 2026-09-24T16:59:58.413700+00:00 | 0.029 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 11-export.json | 2026-09-24T16:59:58.570011+00:00 | 0.298 | 0 | `/usr/local/bin/easyeda project export-source --uuid 7186b88d407441ac83d003db71448462 --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B03/project.epro2 --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |

#### 清理、归因与边界

保留新容器供后续页测试；当前没有 typed 工程删除命令。团队创建是用户指定的范围外未验证项；本次 pass 不表示团队功能已验收。最初 partial 的命令证据保留，状态变化来自用户明确范围决定。

### B04：页面新建、重命名与保存重载

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

新页 UUID、父原理图与 CLI_BASIC_DEV21 名称一致；typed open 成功，空白页完整回包在 save→reload 后严格相等，页列表与 doc ls 均包含正确页面。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B04/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-pages-before.json | 2026-09-24T17:00:06.691730+00:00 | 0.147 | 0 | `/usr/local/bin/easyeda sch pages --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 02-new.json | 2026-09-24T17:00:06.946469+00:00 | 0.89 | 0 | `/usr/local/bin/easyeda sch page-new --schematic d3e1bbd76496c4a8 --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 03-open.json | 2026-09-24T17:00:31.905629+00:00 | 0.72 | 0 | `/usr/local/bin/easyeda doc open d81c0aa4c66b2e62 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 04-rename.json | 2026-09-24T17:00:32.736687+00:00 | 0.417 | 0 | `/usr/local/bin/easyeda sch page-rename --page d81c0aa4c66b2e62 --name CLI_BASIC_DEV21 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 05-before.json | 2026-09-24T17:00:33.283693+00:00 | 0.031 | 0 | `/usr/local/bin/easyeda sch list --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 06-save.json | 2026-09-24T17:00:33.395910+00:00 | 0.018 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 07-reload.json | 2026-09-24T17:00:33.498665+00:00 | 1.336 | 0 | `/usr/local/bin/easyeda doc reload d81c0aa4c66b2e62 --json --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 08-after.json | 2026-09-24T17:00:34.950753+00:00 | 0.029 | 0 | `/usr/local/bin/easyeda sch list --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 09-pages.json | 2026-09-24T17:00:35.133026+00:00 | 0.044 | 0 | `/usr/local/bin/easyeda sch pages --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 10-docs.json | 2026-09-24T17:00:35.285731+00:00 | 0.024 | 0 | `/usr/local/bin/easyeda doc ls --json --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |

#### 清理、归因与边界

专用临时页保留给 B05/B06/B09，B10 按该页 UUID 精确删除。

### B05：库读取、器件放置与单件修改

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

C25744 搜索/解析和 device/symbol/footprint 读取成功。放置回包的 ID 与 fresh 器件匹配；R1 x=300→320、R2 x=500→520，bbox/引脚同步移动，显式 uniqueId 回读正确。修改 R2 后 R1 的完整记录严格相等；图框只更新宿主派生 @Update Time 及对应属性值，其他字段保持。stderr 给出 #256，stdout 原始 JSON 可解析，正确目标操作均退出 0。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B05/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-before.json | 2026-09-24T17:01:14.205858+00:00 | 0.041 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 02-search.json | 2026-09-24T17:01:14.331526+00:00 | 0.311 | 0 | `/usr/local/bin/easyeda lib search --query C25744 --limit 5 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 03-by-lcsc.json | 2026-09-24T17:01:14.757699+00:00 | 0.12 | 0 | `/usr/local/bin/easyeda lib by-lcsc --lcsc C25744 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 04-device.json | 2026-09-24T17:01:22.029007+00:00 | 0.3 | 0 | `/usr/local/bin/easyeda lib device get --library 0819f05c4eef4c71ace90d822a990e87 --uuid c3b9baa5ef2e4070a4c0f9e9cd04fe6e --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 05-symbol.json | 2026-09-24T17:01:38.060715+00:00 | 0.137 | 0 | `/usr/local/bin/easyeda lib symbol get --library 0819f05c4eef4c71ace90d822a990e87 --uuid b4bb0b94f5d04a92a4e5542845335d53 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 06-footprint.json | 2026-09-24T17:01:38.278282+00:00 | 0.633 | 0 | `/usr/local/bin/easyeda lib footprint get --library 0819f05c4eef4c71ace90d822a990e87 --uuid df9dfd54cabd403f88cae9927dcd418d --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 07-place.json | 2026-09-24T17:01:38.992767+00:00 | 1.092 | 0 | `/usr/local/bin/easyeda sch place --lib 0819f05c4eef4c71ace90d822a990e87 --uuid c3b9baa5ef2e4070a4c0f9e9cd04fe6e --x 300 --y 400 --designator R1 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 08-read.json | 2026-09-24T17:01:40.178640+00:00 | 1.903 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 09-modify.json | 2026-09-24T17:01:42.196206+00:00 | 0.03 | 1 | `/usr/local/bin/easyeda sch modify --id 884559b0e73a3371 --x 320 --y 400 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 11-identity.json | 2026-09-24T17:01:57.174128+00:00 | 0.022 | 1 | `/usr/local/bin/easyeda sch modify --id 884559b0e73a3371 --patch '{"uniqueId": "cli-dev21-r1"}' --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 14-after-invalid-target.json | 2026-09-24T17:02:23.160080+00:00 | 0.061 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 15-modify-R1.json | 2026-09-24T17:02:56.526325+00:00 | 0.038 | 0 | `/usr/local/bin/easyeda sch modify --id f521bbe995077052 --x 320 --y 400 --patch '{"uniqueId": "cli-dev21-r1"}' --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 16-read-R1.json | 2026-09-24T17:02:56.690616+00:00 | 0.197 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 17-save.json | 2026-09-24T17:02:57.043954+00:00 | 0.025 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 18-place-R2.json | 2026-09-24T17:03:25.938369+00:00 | 0.582 | 0 | `/usr/local/bin/easyeda sch place --lib 0819f05c4eef4c71ace90d822a990e87 --uuid c3b9baa5ef2e4070a4c0f9e9cd04fe6e --x 500 --y 400 --designator R2 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 19-before-R2.json | 2026-09-24T17:03:26.626224+00:00 | 1.089 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 20-modify-R2.json | 2026-09-24T17:03:27.857869+00:00 | 0.045 | 0 | `/usr/local/bin/easyeda sch modify --id 18130fbd346f48e1 --x 520 --patch '{"uniqueId": "cli-dev21-r2"}' --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 21-final.json | 2026-09-24T17:03:28.010943+00:00 | 0.217 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |

#### 清理、归因与边界

09/11 是测试脚本误选列表首项图框，被宿主拒绝；已改为按创建回包 ID 匹配，失败证据保留。首次自动 uniqueId=gge1 赋值时机仍是观察项，随后按输入显式设置唯一链接键。主测试的图框更新时间变化逐项记录，不将其当成用户对象变更。#256 未声称修复；器件保留供 B06/B09，B10 清理。

### B06：NC、导线、标记与精确删除

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

NC 设置及清除各 fresh 核对一次，R1.1 最终 noConnected:false，#257 警告仅清除时出现在 stderr。两器件通过 CLI21_NET 真导线连接；GND 和 CLI21_FLAG 标记均经实测端点导线连接，四引脚回读网络符合输入。原生 net_label 返回 fresh Name 属性，父导线、网名、位置、可见性均正确。临时导线按创建 ID 删除后，原三导线和全部非图框组件完整记录恢复基线。saved:true。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B06/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-before.json | 2026-09-24T17:03:49.074810+00:00 | 0.061 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 02-nc-set.json | 2026-09-24T17:03:49.229330+00:00 | 0.031 | 0 | `/usr/local/bin/easyeda sch no-connect --designator R1 --pin 1 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 03-nc-set-read.json | 2026-09-24T17:03:49.421899+00:00 | 0.175 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 04-nc-clear.json | 2026-09-24T17:03:49.716194+00:00 | 0.037 | 0 | `/usr/local/bin/easyeda sch no-connect --designator R1 --pin 1 --clear --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 05-nc-clear-read.json | 2026-09-24T17:03:49.881058+00:00 | 0.109 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 06-wire.json | 2026-09-24T17:03:50.242426+00:00 | 0.046 | 0 | `/usr/local/bin/easyeda sch wire --points '[[340, 400], [500, 400]]' --net CLI21_NET --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 07-wire-read.json | 2026-09-24T17:03:50.360094+00:00 | 0.1 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 08-ground-wire.json | 2026-09-24T17:04:52.963593+00:00 | 0.059 | 0 | `/usr/local/bin/easyeda sch wire --points '[[300, 400], [240, 400], [240, 350]]' --net GND --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 09-ground.json | 2026-09-24T17:04:53.189548+00:00 | 0.53 | 0 | `/usr/local/bin/easyeda sch netflag --kind ground --net GND --x 240 --y 350 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 10-port-wire.json | 2026-09-24T17:04:53.853285+00:00 | 0.045 | 0 | `/usr/local/bin/easyeda sch wire --points '[[540, 400], [560, 400], [560, 450], [600, 450]]' --net CLI21_FLAG --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 11-port.json | 2026-09-24T17:04:54.004365+00:00 | 0.319 | 0 | `/usr/local/bin/easyeda sch netflag --kind net_port_bi --net CLI21_FLAG --x 600 --y 450 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 12-label.json | 2026-09-24T17:04:54.414273+00:00 | 0.05 | 0 | `/usr/local/bin/easyeda sch netflag --kind net_label --net CLI21_FLAG --x 600 --y 450 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 13-read.json | 2026-09-24T17:04:54.563953+00:00 | 0.205 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 14-save.json | 2026-09-24T17:04:54.896709+00:00 | 0.022 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 15-temp-wire.json | 2026-09-24T17:05:30.201945+00:00 | 0.058 | 0 | `/usr/local/bin/easyeda sch wire --points '[[680,500],[720,500]]' --net CLI21_TEMP --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 16-temp-read.json | 2026-09-24T17:05:30.407223+00:00 | 0.355 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 17-temp-delete.json | 2026-09-24T17:05:30.899746+00:00 | 0.054 | 0 | `/usr/local/bin/easyeda sch prim-delete --ids 6c5a4f3ac8b6786c --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 18-delete-read.json | 2026-09-24T17:05:31.050033+00:00 | 0.177 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 19-save.json | 2026-09-24T17:05:31.332204+00:00 | 0.021 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |

#### 清理、归因与边界

没有同页 reopen 或自动重试，NC 连续路径本次通过；#257 间歇问题保持 open。功能图元保留供 B09 持久化，B10 按记录 ID/页清理。

### B07：PCB 身份、器件放置、移动与删除

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

专用既有 PCB1 身份正确、初始零器件。R9 放置回包与 fresh 回读的 ID/位号/uniqueId 一致，两焊盘分别 GND/CLI21_NET。单件 x=3500→3600 后焊盘与 bbox 同步平移。另放 R10 并按其 ID 删除，剩余完整器件列表严格等于仅 R9 的基线；saved:true。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B07/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-save-new-P1.json | 2026-09-24T17:06:04.251684+00:00 | 1.657 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 02-save-new-test.json | 2026-09-24T17:06:05.974257+00:00 | 1.614 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 03-open-fixture.json | 2026-09-24T17:06:07.666034+00:00 | 3.399 | 0 | `/usr/local/bin/easyeda project open --window 6e80d20d-2fed-407f-9c25-d6c02719a9d3 --project-uuid 0f46d4361c4741a9ab7a9ed62164ca9b --page-uuid e453c0063919726b --allow-discard-unsaved` |
| 04-docs.json | 2026-09-24T17:06:11.143299+00:00 | 0.015 | 0 | `/usr/local/bin/easyeda pcb docs --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc e453c0063919726b` |
| 05-info.json | 2026-09-24T17:06:11.228415+00:00 | 3.125 | 0 | `/usr/local/bin/easyeda pcb board-info --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 06-before.json | 2026-09-24T17:06:14.453305+00:00 | 0.043 | 0 | `/usr/local/bin/easyeda pcb list --include-pads --include-bbox --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 07-add-R9.json | 2026-09-24T17:06:14.583256+00:00 | 0.597 | 0 | `/usr/local/bin/easyeda pcb add-component --library 0819f05c4eef4c71ace90d822a990e87 --uuid c3b9baa5ef2e4070a4c0f9e9cd04fe6e --x 3500 --y 3500 --designator R9 --unique-id cli-dev21-pcb-r9 --nets '{"1":"GND","2":"CLI21_NET"}' --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 08-add-read.json | 2026-09-24T17:06:15.273417+00:00 | 0.026 | 0 | `/usr/local/bin/easyeda pcb list --include-pads --include-bbox --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 09-move.json | 2026-09-24T17:06:15.393926+00:00 | 0.06 | 0 | `/usr/local/bin/easyeda pcb modify --id 2ee571e3de3f3ed4 --patch '{"x":3600,"y":3500}' --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 10-move-read.json | 2026-09-24T17:06:15.553293+00:00 | 0.033 | 0 | `/usr/local/bin/easyeda pcb list --include-pads --include-bbox --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 11-add-delete-probe.json | 2026-09-24T17:06:42.116398+00:00 | 0.181 | 0 | `/usr/local/bin/easyeda pcb add-component --library 0819f05c4eef4c71ace90d822a990e87 --uuid c3b9baa5ef2e4070a4c0f9e9cd04fe6e --x 3900 --y 3500 --designator R10 --unique-id cli-dev21-pcb-r10 --nets '{"1":"GND","2":"CLI21_NET"}' --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 12-probe-read.json | 2026-09-24T17:06:42.383932+00:00 | 0.02 | 0 | `/usr/local/bin/easyeda pcb list --include-pads --include-bbox --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 13-delete.json | 2026-09-24T17:06:42.496782+00:00 | 0.061 | 0 | `/usr/local/bin/easyeda pcb delete --ids e05c6049a117c7ac --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 14-delete-read.json | 2026-09-24T17:06:42.676345+00:00 | 0.026 | 0 | `/usr/local/bin/easyeda pcb list --include-pads --include-bbox --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 15-save.json | 2026-09-24T17:06:42.785859+00:00 | 0.034 | 0 | `/usr/local/bin/easyeda pcb save --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |

#### 清理、归因与边界

使用前已保存个人工程两页，并 typed 切换到独立夹具 PCB。保留 R9 供 B08/B09，删除的 R10 ID 保留用于 B10 stale-ID 负例。

### B08：PCB 规则、叠层和几何原语

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

从已存在的自定义配置11开始，线宽 min 6→7 mil 验证通过，再恢复。保存重载后配置名称和 ruleConfiguration 完全相等；netRules 仅枚举顺序交换，按唯一网络名逐项严格一致。内存阶段22个单元仅差1个 binary64 ULP，既有机器舍入校验未改；#258 的 0.0000006 mm 负对照仍被拒绝。2→4→2 层完整恢复。1200×1000 mil 板框回读正确；8 mil track、12/24 mil via、no-pours 区域、具名 follow-rule 区域和铺铜边界的 ID/层/网/几何符合输入。四类图元逐个创建、精确删除后列表恢复各自基线，再留一组用于持久化。写规则有 #258 提示，dry-run 无该提示。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B08/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-config-before.json | 2026-09-24T17:07:06.646548+00:00 | 0.045 | 0 | `/usr/local/bin/easyeda pcb config get --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 02-stackup-before.json | 2026-09-24T17:07:06.802546+00:00 | 0.083 | 0 | `/usr/local/bin/easyeda pcb stackup show --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 03-config-dry.json | 2026-09-24T17:07:07.039429+00:00 | 0.026 | 0 | `/usr/local/bin/easyeda pcb config track --name copperThickness1oz --min 7 --dry-run --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 04-config-write.json | 2026-09-24T17:07:07.147850+00:00 | 0.073 | 0 | `/usr/local/bin/easyeda pcb config track --name copperThickness1oz --min 7 --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 05-config-read.json | 2026-09-24T17:07:07.413607+00:00 | 0.025 | 0 | `/usr/local/bin/easyeda pcb config get --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 06-config-restore.json | 2026-09-24T17:07:07.525121+00:00 | 0.08 | 0 | `/usr/local/bin/easyeda pcb drc-rules-set --from /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B08/restore-config.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 07-config-restored.json | 2026-09-24T17:07:07.766350+00:00 | 0.025 | 0 | `/usr/local/bin/easyeda pcb config get --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 07a-config-save.json | 2026-09-24T17:07:55.038615+00:00 | 0.035 | 0 | `/usr/local/bin/easyeda pcb save --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 07b-config-reload.json | 2026-09-24T17:07:55.241083+00:00 | 2.95 | 0 | `/usr/local/bin/easyeda doc reload 3037fc1dfa4965d2 --json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 07c-config-persisted.json | 2026-09-24T17:07:58.322948+00:00 | 0.024 | 0 | `/usr/local/bin/easyeda pcb config get --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 08-stackup-four.json | 2026-09-24T17:09:02.901221+00:00 | 0.175 | 0 | `/usr/local/bin/easyeda pcb stackup set --layers 4 --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 09-stackup-four-read.json | 2026-09-24T17:09:03.217053+00:00 | 0.08 | 0 | `/usr/local/bin/easyeda pcb stackup show --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 10-stackup-two.json | 2026-09-24T17:09:03.415926+00:00 | 0.174 | 0 | `/usr/local/bin/easyeda pcb stackup set --layers 2 --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 11-stackup-restored.json | 2026-09-24T17:09:03.768206+00:00 | 0.091 | 0 | `/usr/local/bin/easyeda pcb stackup show --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 12-config-save.json | 2026-09-24T17:09:03.931423+00:00 | 0.347 | 0 | `/usr/local/bin/easyeda pcb save --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 13-outline-before.json | 2026-09-24T17:09:04.382930+00:00 | 0.129 | 0 | `/usr/local/bin/easyeda pcb outline-get --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 14-outline-set.json | 2026-09-24T17:09:04.623502+00:00 | 0.087 | 0 | `/usr/local/bin/easyeda pcb outline-set --points '[[3000,3000],[4200,3000],[4200,4000],[3000,4000]]' --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 15-outline-read.json | 2026-09-24T17:09:04.798443+00:00 | 0.029 | 0 | `/usr/local/bin/easyeda pcb outline-get --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-before.json | 2026-09-24T17:10:22.860682+00:00 | 0.02 | 0 | `/usr/local/bin/easyeda pcb track-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-create.json | 2026-09-24T17:10:22.969473+00:00 | 0.062 | 0 | `/usr/local/bin/easyeda pcb track --x1 3617 --y1 3500 --x2 3700 --y2 3500 --layer 1 --width 8 --net CLI21_NET --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-delete.json | 2026-09-24T17:10:23.318856+00:00 | 0.055 | 0 | `/usr/local/bin/easyeda pcb track-delete --ids d0a4c762c4e8cf3f --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-deleted.json | 2026-09-24T17:10:23.461185+00:00 | 0.022 | 0 | `/usr/local/bin/easyeda pcb track-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-final.json | 2026-09-24T17:10:23.717482+00:00 | 0.018 | 0 | `/usr/local/bin/easyeda pcb track-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-keep.json | 2026-09-24T17:10:23.562358+00:00 | 0.04 | 0 | `/usr/local/bin/easyeda pcb track --x1 3617 --y1 3500 --x2 3700 --y2 3500 --layer 1 --width 8 --net CLI21_NET --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-read.json | 2026-09-24T17:10:23.215698+00:00 | 0.029 | 0 | `/usr/local/bin/easyeda pcb track-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-before.json | 2026-09-24T17:10:23.817773+00:00 | 0.019 | 0 | `/usr/local/bin/easyeda pcb via-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-create.json | 2026-09-24T17:10:23.919585+00:00 | 0.046 | 0 | `/usr/local/bin/easyeda pcb via --x 3700 --y 3500 --hole 12 --diameter 24 --net CLI21_NET --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-delete.json | 2026-09-24T17:10:24.152888+00:00 | 0.05 | 0 | `/usr/local/bin/easyeda pcb via-delete --ids ad2a3d8e7e7e3b23 --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-deleted.json | 2026-09-24T17:10:24.288451+00:00 | 0.022 | 0 | `/usr/local/bin/easyeda pcb via-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-final.json | 2026-09-24T17:10:24.504421+00:00 | 0.018 | 0 | `/usr/local/bin/easyeda pcb via-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-keep.json | 2026-09-24T17:10:24.381256+00:00 | 0.041 | 0 | `/usr/local/bin/easyeda pcb via --x 3700 --y 3500 --hole 12 --diameter 24 --net CLI21_NET --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-read.json | 2026-09-24T17:10:24.060975+00:00 | 0.019 | 0 | `/usr/local/bin/easyeda pcb via-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-before.json | 2026-09-24T17:10:24.608886+00:00 | 0.019 | 0 | `/usr/local/bin/easyeda pcb region list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-create.json | 2026-09-24T17:10:24.707371+00:00 | 0.059 | 0 | `/usr/local/bin/easyeda pcb region create --rect 3900,3700,4000,3800 --layer 1 --rule no-pours --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-delete.json | 2026-09-24T17:10:24.970686+00:00 | 0.055 | 0 | `/usr/local/bin/easyeda pcb region delete --ids 726def801754306c --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-deleted.json | 2026-09-24T17:10:25.097231+00:00 | 0.021 | 0 | `/usr/local/bin/easyeda pcb region list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-final.json | 2026-09-24T17:10:25.315749+00:00 | 0.107 | 0 | `/usr/local/bin/easyeda pcb region list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-keep.json | 2026-09-24T17:10:25.182058+00:00 | 0.052 | 0 | `/usr/local/bin/easyeda pcb region create --rect 3900,3700,4000,3800 --layer 1 --rule no-pours --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-read.json | 2026-09-24T17:10:24.891374+00:00 | 0.021 | 0 | `/usr/local/bin/easyeda pcb region list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-before.json | 2026-09-24T17:10:25.511808+00:00 | 0.022 | 0 | `/usr/local/bin/easyeda pcb pour-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-create.json | 2026-09-24T17:10:25.619369+00:00 | 0.524 | 0 | `/usr/local/bin/easyeda pcb pour --points '[[3550,3450],[3750,3450],[3750,3550],[3550,3550]]' --net CLI21_NET --layer 1 --name CLI21_POUR --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-delete.json | 2026-09-24T17:10:26.325930+00:00 | 0.053 | 0 | `/usr/local/bin/easyeda pcb pour-delete --ids 097f2eb59bf288e3 --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-deleted.json | 2026-09-24T17:10:26.464156+00:00 | 0.02 | 0 | `/usr/local/bin/easyeda pcb pour-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-final.json | 2026-09-24T17:10:26.979322+00:00 | 0.025 | 0 | `/usr/local/bin/easyeda pcb pour-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-keep.json | 2026-09-24T17:10:26.573950+00:00 | 0.292 | 0 | `/usr/local/bin/easyeda pcb pour --points '[[3550,3450],[3750,3450],[3750,3550],[3550,3550]]' --net CLI21_NET --layer 1 --name CLI21_POUR --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-read.json | 2026-09-24T17:10:26.220382+00:00 | 0.022 | 0 | `/usr/local/bin/easyeda pcb pour-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 30-save.json | 2026-09-24T17:10:27.082760+00:00 | 0.039 | 0 | `/usr/local/bin/easyeda pcb save --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 31-dump.json | 2026-09-24T17:10:27.196475+00:00 | 0.245 | 0 | `/usr/local/bin/easyeda pcb dump --include-copper --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B08/geometry-final.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 32-named-region.json | 2026-09-24T17:11:09.250362+00:00 | 0.063 | 0 | `/usr/local/bin/easyeda pcb region create --rect 3900,3850,4000,3950 --layer 1 --rule follow-rule --name CLI21_REGION --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 33-named-read.json | 2026-09-24T17:11:09.582778+00:00 | 0.029 | 0 | `/usr/local/bin/easyeda pcb region list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 34-named-delete.json | 2026-09-24T17:11:09.688999+00:00 | 0.06 | 0 | `/usr/local/bin/easyeda pcb region delete --ids e026413622231f5b --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 35-named-deleted.json | 2026-09-24T17:11:09.839338+00:00 | 0.023 | 0 | `/usr/local/bin/easyeda pcb region list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 36-save.json | 2026-09-24T17:11:09.952715+00:00 | 0.169 | 0 | `/usr/local/bin/easyeda pcb save --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |

#### 清理、归因与边界

这是基础写读测试，没有用铺铜或 DRC 结果判断电路质量。首次系统预设转自定义路径保留 #258，按既定夹具前置条件不纳入本项。R9/板框/四图元保留供 B09，B10 清理。

### B09：保存重载、对象保持与导出

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

PCB save→reload→fresh dump 除 capturedAt 外全部字段严格相等，含 R9、焊盘、铜/区域/板框及 semanticSha256。原理图组件、引脚、导线和其他图元完整相等；133 个属性按全部非 ID 字段一一对应，父对象/键/值/可见性/坐标均保留（映射见 attribute-identity-map.json）。SVG 共341元素，只重铸38个没有引用关系的 id，所有几何、样式、文本和层级一致；前后原始网表 SHA-256 完全一致，分别证明渲染和电气效果未变。SVG、网表、BOM 和 PCB DSN 导出均按回包路径、大小、SHA-256 实文件核验并复制到证据目录。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B09/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-pcb-save.json | 2026-09-24T17:11:50.897893+00:00 | 0.039 | 0 | `/usr/local/bin/easyeda pcb save --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 02-pcb-before.json | 2026-09-24T17:11:51.029453+00:00 | 0.437 | 0 | `/usr/local/bin/easyeda pcb dump --include-copper --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B09/pcb-before.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 03-pcb-reload.json | 2026-09-24T17:11:51.601219+00:00 | 2.966 | 0 | `/usr/local/bin/easyeda doc reload 3037fc1dfa4965d2 --json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 04-pcb-after.json | 2026-09-24T17:11:54.657085+00:00 | 0.177 | 0 | `/usr/local/bin/easyeda pcb dump --include-copper --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B09/pcb-after.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 05-pcb-export.json | 2026-09-24T17:11:54.931035+00:00 | 0.166 | 0 | `/usr/local/bin/easyeda pcb export-dsn --name cli-basic-dev21.dsn --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 06-open-sch-project.json | 2026-09-24T17:11:55.183513+00:00 | 3.146 | 0 | `/usr/local/bin/easyeda project open --window 6e80d20d-2fed-407f-9c25-d6c02719a9d3 --project-uuid 7186b88d407441ac83d003db71448462 --page-uuid d81c0aa4c66b2e62 --allow-discard-unsaved` |
| 07-sch-save.json | 2026-09-24T17:11:58.421491+00:00 | 0.027 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 08-sch-netlist-before.json | 2026-09-24T17:11:58.530965+00:00 | 0.7 | 0 | `/usr/local/bin/easyeda sch netlist --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 09-sch-svg-before.json | 2026-09-24T17:11:59.324486+00:00 | 0.081 | 0 | `/usr/local/bin/easyeda sch export-image --format svg --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B09/sch-before.svg --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 10-sch-before.json | 2026-09-24T17:11:59.514444+00:00 | 0.241 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 11-sch-reload.json | 2026-09-24T17:11:59.838773+00:00 | 1.406 | 0 | `/usr/local/bin/easyeda doc reload d81c0aa4c66b2e62 --json --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 12-sch-after.json | 2026-09-24T17:12:01.345442+00:00 | 0.288 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 13-sch-svg-after.json | 2026-09-24T17:12:01.714587+00:00 | 0.088 | 0 | `/usr/local/bin/easyeda sch export-image --format svg --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B09/sch-after.svg --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 14-sch-netlist-after.json | 2026-09-24T17:12:01.884938+00:00 | 0.031 | 0 | `/usr/local/bin/easyeda sch netlist --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 15-bom.json | 2026-09-24T17:12:02.006255+00:00 | 0.037 | 0 | `/usr/local/bin/easyeda bom export --type csv --enrich=false --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |

#### 清理、归因与边界

未用“剔除 ID”单独放行：逐属性映射 + 渲染结构 + 同字节网表同时核对。保留设计夹具进入 B10 保护负例与最终清理；没有高级布局、布线质量或 DRC 验收。

### B10：保护负例、精确清理与独立复核

#### 结论

**pass**。

#### 前置条件

- CLI/daemon/connector dev.21，Web 4.1.60；源码基于 0365c10 加本轮补丁，运行时哈希见本轮 setup/runtime.json。
- 本项工程/文档以命令表显式 UUID 及原始响应 context 为准；仅使用专用测试页。
- 串行 typed 调用；已知 bug 警告不改变通过判据。

#### 检查结果

原理图 stale ID/无效 patch、PCB 错 doc/stale 修改删除/无效宽度/非法区域名称均 exit 1，前后完整对象相等。60942 上真实同版隔离 daemon 的 windows=[]，写请求在 doc guard 拒绝；隔离 daemon 已停止，主窗口状态不变。临时 PCB 器件、铜、区域、板框按 fresh 清单清除，save→reload 后完整 dump 与 B02 初始基线仅 capturedAt 不同。临时原理图页按 UUID 删除、保存重载，剩余 P1 仅图框页数 2→1。原工程 P3 的13组件、15导线和其他图元精确相同，267属性的全部非 ID 字段逐项对应相同。独立 offline_review 复核原始回包、完整差分及57项哈希全部通过。

#### 原始证据

本机 `artifacts/cli-basic-dev21-20260925/B10/`；JSON 保存完整 stdout/stderr、命令、UTC 与退出码，evidence-sha256.json 保存哈希。

| 记录 | UTC | 秒 | 退出码 | 命令 |
|---|---|---:|---:|---|
| 01-sch-before.json | 2026-09-24T17:14:44.567684+00:00 | 0.626 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 02-sch-stale.json | 2026-09-24T17:14:45.274431+00:00 | 0.028 | 1 | `/usr/local/bin/easyeda sch modify --id ffffffffffffffff --x 600 --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 03-sch-bad-patch.json | 2026-09-24T17:14:45.404579+00:00 | 0.025 | 1 | `/usr/local/bin/easyeda sch modify --id f521bbe995077052 --patch '{"unsupportedField":1}' --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 04-sch-after.json | 2026-09-24T17:14:45.510975+00:00 | 0.353 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 05-sch-save.json | 2026-09-24T17:14:45.927595+00:00 | 0.019 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 06-open-pcb-project.json | 2026-09-24T17:14:46.018848+00:00 | 3.46 | 0 | `/usr/local/bin/easyeda project open --window 6e80d20d-2fed-407f-9c25-d6c02719a9d3 --project-uuid 0f46d4361c4741a9ab7a9ed62164ca9b --page-uuid e453c0063919726b --allow-discard-unsaved` |
| 07-pcb-before.json | 2026-09-24T17:14:49.551609+00:00 | 3.465 | 0 | `/usr/local/bin/easyeda pcb dump --include-copper --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B10/pcb-before.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 08-wrong-doc.json | 2026-09-24T17:14:53.113891+00:00 | 0.025 | 1 | `/usr/local/bin/easyeda pcb track --x1 3617 --y1 3500 --x2 3700 --y2 3500 --width 8 --net CLI21_NET --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 0000000000000000` |
| 09-stale-modify.json | 2026-09-24T17:14:53.218474+00:00 | 0.019 | 1 | `/usr/local/bin/easyeda pcb modify --id e05c6049a117c7ac --patch '{"x":3900}' --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 10-stale-delete.json | 2026-09-24T17:14:53.316687+00:00 | 0.023 | 1 | `/usr/local/bin/easyeda pcb delete --ids e05c6049a117c7ac --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 11-invalid-width.json | 2026-09-24T17:14:53.413285+00:00 | 0.011 | 1 | `/usr/local/bin/easyeda pcb track --x1 3617 --y1 3500 --x2 3700 --y2 3500 --width abc --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 12-invalid-region.json | 2026-09-24T17:14:53.506097+00:00 | 0.039 | 1 | `/usr/local/bin/easyeda pcb region create --rect 3900,3700,4000,3800 --rule no-pours --name INVALID --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 13-pcb-after.json | 2026-09-24T17:14:53.622741+00:00 | 0.167 | 0 | `/usr/local/bin/easyeda pcb dump --include-copper --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B10/pcb-after.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 14-isolated-health-0.json | 2026-09-24T17:15:31.332751+00:00 | 0.018 | 0 | `/usr/local/bin/easyeda health --ports 60942-60942` |
| 15-no-connector.json | 2026-09-24T17:15:31.430602+00:00 | 0.015 | 1 | `/usr/local/bin/easyeda pcb track --ports 60942-60942 --x1 3617 --y1 3500 --x2 3700 --y2 3500 --width 8 --net CLI21_NET --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 16-primary-health.json | 2026-09-24T17:15:31.530649+00:00 | 0.014 | 0 | `/usr/local/bin/easyeda health` |
| 17-post-isolation.json | 2026-09-24T17:15:31.629173+00:00 | 0.437 | 0 | `/usr/local/bin/easyeda pcb dump --include-copper --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B10/pcb-after-isolation.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-after-clean.json | 2026-09-24T17:16:12.596403+00:00 | 0.024 | 0 | `/usr/local/bin/easyeda pcb track-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-before-clean.json | 2026-09-24T17:16:12.244709+00:00 | 0.031 | 0 | `/usr/local/bin/easyeda pcb track-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 20-track-clean.json | 2026-09-24T17:16:12.363782+00:00 | 0.067 | 0 | `/usr/local/bin/easyeda pcb track-delete --ids 3647a38c5b59fda0 --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-after-clean.json | 2026-09-24T17:16:12.989204+00:00 | 0.021 | 0 | `/usr/local/bin/easyeda pcb via-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-before-clean.json | 2026-09-24T17:16:12.739085+00:00 | 0.022 | 0 | `/usr/local/bin/easyeda pcb via-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 21-via-clean.json | 2026-09-24T17:16:12.837358+00:00 | 0.066 | 0 | `/usr/local/bin/easyeda pcb via-delete --ids ca27ba30774af9a8 --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-after-clean.json | 2026-09-24T17:16:13.375605+00:00 | 0.019 | 0 | `/usr/local/bin/easyeda pcb region list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-before-clean.json | 2026-09-24T17:16:13.095618+00:00 | 0.027 | 0 | `/usr/local/bin/easyeda pcb region list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 22-region-clean.json | 2026-09-24T17:16:13.214479+00:00 | 0.064 | 0 | `/usr/local/bin/easyeda pcb region delete --ids 68206c696f3ed260 --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-after-clean.json | 2026-09-24T17:16:13.734129+00:00 | 0.022 | 0 | `/usr/local/bin/easyeda pcb pour-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-before-clean.json | 2026-09-24T17:16:13.498173+00:00 | 0.021 | 0 | `/usr/local/bin/easyeda pcb pour-list --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 23-pour-clean.json | 2026-09-24T17:16:13.597639+00:00 | 0.058 | 0 | `/usr/local/bin/easyeda pcb pour-delete --ids c256812341729c7b --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 24-components-before-clean.json | 2026-09-24T17:16:13.832233+00:00 | 0.022 | 0 | `/usr/local/bin/easyeda pcb list --include-pads --include-bbox --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 25-component-clean.json | 2026-09-24T17:16:13.940235+00:00 | 0.061 | 0 | `/usr/local/bin/easyeda pcb delete --ids 2ee571e3de3f3ed4 --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 26-outline-before-clean.json | 2026-09-24T17:16:14.085054+00:00 | 0.018 | 0 | `/usr/local/bin/easyeda pcb outline-get --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 27-outline-clean.json | 2026-09-24T17:16:14.183180+00:00 | 0.056 | 0 | `/usr/local/bin/easyeda pcb outline-clear --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 28-pcb-clean-save.json | 2026-09-24T17:16:14.338403+00:00 | 0.04 | 0 | `/usr/local/bin/easyeda pcb save --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 29-pcb-clean-reload.json | 2026-09-24T17:16:14.483782+00:00 | 3.343 | 0 | `/usr/local/bin/easyeda doc reload 3037fc1dfa4965d2 --json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 30-pcb-clean-read.json | 2026-09-24T17:16:17.938490+00:00 | 0.148 | 0 | `/usr/local/bin/easyeda pcb dump --include-copper --out /Users/mikas/github/easyeda-agent/artifacts/cli-basic-dev21-20260925/B10/pcb-clean.json --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 31-config-clean.json | 2026-09-24T17:16:18.173902+00:00 | 0.101 | 0 | `/usr/local/bin/easyeda pcb config get --project 0f46d4361c4741a9ab7a9ed62164ca9b --doc 3037fc1dfa4965d2` |
| 32-open-sch-project.json | 2026-09-24T17:17:09.385388+00:00 | 2.548 | 0 | `/usr/local/bin/easyeda project open --window 6e80d20d-2fed-407f-9c25-d6c02719a9d3 --project-uuid 7186b88d407441ac83d003db71448462 --page-uuid d81c0aa4c66b2e62 --allow-discard-unsaved` |
| 33-page-before-clean.json | 2026-09-24T17:17:12.061574+00:00 | 1.0 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 33b-page-same-options.json | 2026-09-24T17:18:10.164771+00:00 | 0.425 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc d81c0aa4c66b2e62` |
| 34-P1-before-clean.json | 2026-09-24T17:18:10.689358+00:00 | 2.123 | 0 | `/usr/local/bin/easyeda sch list --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 35-pages-before-clean.json | 2026-09-24T17:18:12.917995+00:00 | 0.019 | 0 | `/usr/local/bin/easyeda sch pages --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 36-page-delete.json | 2026-09-24T17:18:13.021891+00:00 | 0.129 | 0 | `/usr/local/bin/easyeda sch page-delete --page d81c0aa4c66b2e62 --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 37-P1-save.json | 2026-09-24T17:18:13.239283+00:00 | 0.022 | 0 | `/usr/local/bin/easyeda sch save --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 38-P1-reload.json | 2026-09-24T17:18:13.544624+00:00 | 1.386 | 0 | `/usr/local/bin/easyeda doc reload a07f2d2566a5eab9 --json --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 39-pages-clean.json | 2026-09-24T17:18:15.059317+00:00 | 0.032 | 0 | `/usr/local/bin/easyeda sch pages --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 40-P1-clean.json | 2026-09-24T17:18:15.186739+00:00 | 0.03 | 0 | `/usr/local/bin/easyeda sch list --include-wires --include-page-primitives --project 7186b88d407441ac83d003db71448462 --doc a07f2d2566a5eab9` |
| 41-restore-original.json | 2026-09-24T17:18:15.305811+00:00 | 2.877 | 0 | `/usr/local/bin/easyeda project open --window 6e80d20d-2fed-407f-9c25-d6c02719a9d3 --project-uuid 4037dd22434c412fa75d7f2d855a2ab4 --page-uuid a6db7873157483f0 --allow-discard-unsaved` |
| 42-original-read.json | 2026-09-24T17:18:18.297455+00:00 | 1.171 | 0 | `/usr/local/bin/easyeda sch list --include-pins --include-bbox --include-wires --include-page-primitives --project 4037dd22434c412fa75d7f2d855a2ab4 --doc a6db7873157483f0` |
| 43-final-health.json | 2026-09-24T17:18:19.555026+00:00 | 0.013 | 0 | `/usr/local/bin/easyeda health` |

#### 清理、归因与边界

测试项目容器保留（没有 typed 项目删除），只剩默认 P1/PCB1。33首次清理前快照漏 include-bbox，33b 用一致参数补测并通过；原记录保留。恢复原工程时宿主重铸118个属性ID，但父对象、键、值、位置、可见性等非ID字段完整对应；没有把它写成属性ID相同。最终 health 唯一目标为原工程/P3，运行时 dev.21。
