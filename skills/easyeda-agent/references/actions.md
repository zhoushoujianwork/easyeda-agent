# EasyEDA Action Reference

Run `easyeda actions` for the authoritative machine-readable list.

## Playbook 回放(`easyeda apply`)— 批量步骤的首选载体

> **多步批量操作(>~5 步)不要再写 shell/python 胶水脚本**——写成 playbook JSON,
> `easyeda apply` 按步执行,自带变量捕获、门禁、journal 断点续跑。
> 完整格式与错误处理语义见 `docs/design-apply-playbook.md`(单一真源)。

```bash
easyeda apply steps.json                    # 顺序执行(meta.project 定目标工程)
easyeda apply steps.json --dry-run          # 预检 + 打印计划,不执行
easyeda apply steps.json --project demo2    # CLI flag > 文件(同一份打到另一工程)
easyeda apply steps.json --var LIB=<uuid>   # 变量复写(参数化)
easyeda apply steps.json --resume           # 按 journal 跳过已完成步骤(恢复 captured 变量)
easyeda apply steps.json --from 12 --to 30  # 区间执行
easyeda apply steps.json --yes              # 放行确认门控(delete/clear/rip-up/import 类)
```

要点(实现与设计一致,已单测+真机验证):
- 每步 `action:`(typed action)或 `run:`(Cobra 子命令,如 `pcb auto-place`)二选一;
  `notify:` 弹 toast。`payload/flags/args` 内 `${VAR}` 替换。
- `capture: {"U1": "$.primitiveId"}` 把结果存变量给后步用(id 跨会话会变,这是复现的命门)。
- `assert: {"$.score": ">=95", "$.overlaps": "==0"}` = 门禁,不过即停(`onFail: stop` 默认)。
- **错误纪律**:失败即终止;只读步骤自动重试 2 次;**变更类步骤超时不自动重试**
  (mutation 可能已生效——先读回校验再 `--resume`);变更步骤可带 `verify:` 读回块自证。
- journal 头带 playbook sha,文件改动会拒绝 `--resume`(改用 `--from`)。

**录制导出**:`easyeda audit export --playbook --day 2026-07-03 --since 15:17 --until 15:19
-o replay.json` 把真实会话(审计日志)提取成 playbook——只留变更步骤、自动挤压 autosave
风暴、**自动接线 capture/${var}**(后步引用前步 result.primitiveId 时);引用「窗外出生」
裸 id 的步骤会标 `raw-id` 警告(只能对同一板态回放,先 review)。⚠️ 提取物可能含
rip-up/clear 等破坏性步骤——整册回放前先 `--dry-run` 看计划,或用 `--from/--to` 只放安全区间
(已实证:esp32 移件段 18 步区间回放,幂等,lint 保持 100)。

## Navigation

- `system.health` — daemon + connector 可用性，已连接窗口列表
- `project.current` — 当前工程 uuid / name / teamUuid
- `document.current` — 当前激活文档 uuid / tabId / documentType
- `document.open` — 按 UUID 打开任意文档（原理图或 PCB）
- `schematic.pages.list` — 工程内全部原理图及页面
- `schematic.page.open` — 切换到指定原理图页（兼容旧用法）

## Sheet / 图页管理 + 明细表（title block）

均映射 `eda.dmt_Schematic.*`。**注意：EasyEDA Pro 无设置纸张尺寸(A4/A3)的公开 API**；可编辑的「图纸」属性就是明细表(title block)。CLI：`easyeda sch …`。

- `schematic.titleblock.get` — 读当前（或指定 `pageUuid`）图页的明细表：`showTitleBlock` + 各字段 `titleBlockData`。**改前先 get 拿到字段 key** → `easyeda sch titleblock-get`
- `schematic.titleblock.modify` — 调整明细表：显隐 + 字段值（只传要改的项，未知 key 被忽略）→ `easyeda sch titleblock --show` / `--data '{"Title":{"value":"电源模块"}}'`
- `schematic.page.create` — 新建图页（`schematicUuid`）→ `easyeda sch page-new --schematic <uuid>`
- `schematic.page.rename` — 重命名图页 → `easyeda sch page-rename --page <uuid> --name ...`
- `schematic.page.delete` — 删除图页（**需确认**，无 undo）→ `easyeda sch page-delete --page <uuid>`
- `schematic.rename` — 重命名整张原理图文档（非单页；可能联动复用模块符号 + PCB）→ `easyeda sch rename --schematic <uuid> --name ...`

## View（画布视图快捷键，原理图 + PCB 通用）

作用于当前聚焦的画布，等价于编辑器工具栏/快捷键。CLI：`easyeda view …`。

- `view.fit` — 适应全部（`K` 快捷键）；缩放至显示全部图元 → `easyeda view fit`
- `view.fit_selection` — 适应选中；先 `schematic.select` 再缩放至选中图元 → `easyeda view fit-selection`
- `view.zoom` — 缩放到坐标/比例（x/y/scale，scale 为百分比，省略则保持当前值）→ `easyeda view zoom --scale 200`
- `view.region` — 缩放到矩形区域（left/right/top/bottom，单位：原理图 0.01inch、PCB mil）→ `easyeda view region --left 0 --right 1000 --top 1000 --bottom 0`

## Inspect Schematic

- `schematic.components.list` — 当前页（或全页）所有元件，可含 pins
- `schematic.select` — 按 primitiveId 选中图元
- `schematic.snapshot` — 截取当前渲染区域为 PNG artifact。**默认先「适应全部」再截**（整张图入画，无需另调 `view.fit`）；`easyeda sch snapshot --no-fit` 保留当前视口。**局部截图**：先 `easyeda view region --left --right --top --bottom`（或 `view zoom --x --y --scale`）框住目标区域，再 `easyeda sch snapshot --no-fit` 截该视口

## Mutate Schematic

- `schematic.component.place` — 从库放置元件（libraryUuid + uuid + x/y）
- `schematic.component.modify` — 修改位置、位号、BOM 属性等
- `schematic.component.delete` — 删除元件（需确认）
- `schematic.wire.create` — 创建导线折线
- `schematic.netflag.create` — 创建电源/地/网络端口/短路 flag
- `schematic.power.connect_pin` — 复合操作：从 pin 拉导线 + 在末端放 flag（防止 flag-on-pin DRC fatal）
- `schematic.pin.disconnect` — `connect_pin` 的逆操作：把某 pin 的 stub 导线**连同**末端 netflag/netport 一并删除，避免只删 flag 留下孤儿 stub（EasyEDA 会给残留 wire 分配 `$3N…` 自动网名，`sch check` 现已能识别报 WARN）。按 `--pin U1:5`、`pinX`/`pinY` 坐标(`sch autoconnect --replace` 换网时用)或 `--flag-id`/`--wire-id` 定位。CLI：`easyeda sch disconnect --pin U1:5`
- `schematic.pin.set_no_connect` — 给引脚打/清「非连接标识」(NC, X 标记)，告诉 DRC 该脚是故意悬空。按 `--designator` + `--pin`（可多个）定位；`--clear` 清除。CLI：`easyeda sch no-connect --designator U1 --pin 23,24`
- `schematic.rebind.footprint` — 换封装（五步绑定法）。`modify` 改不了已放置件的封装引用，故走 `lib_Device.modify → delete → create → 恢复位号/坐标/属性`；导入器件 `libraryUuid` 为空时先在工程库反查补齐。按封装名精确匹配（同名多命中或未命中会报错，可用 `--footprint-uuid` 直连）。**重建会换新 primitiveId，导线可能需重连——务必跑 `sch drc`/`sch check` 复核连通性。** CLI：`easyeda sch rebind-footprint --id <primitiveId> --footprint <name>`
- `schematic.rebind.symbol` — 换符号，机制同上（五步绑定法）。CLI：`easyeda sch rebind-symbol --id <primitiveId> --symbol <name>`
- `schematic.save` — 保存原理图（需确认）

## Library

- `schematic.library.search` — 自由文本搜索立创/EasyEDA 器件库，返回 libraryUuid + uuid。当 `query` 为纯 LCSC C 号（`^C\d+$`）时自动切换为精确模式，仅保留 `lcsc`/`supplierId` 严格相等的条目；无精确命中则报错。传 `allowFuzzy`（CLI `--allow-fuzzy`）可保留原模糊排序结果

## Verify & Export

- `schematic.drc.check` — 调官方 `eda.sch_Drc.check` 作为 SDK DRC 门。当前 EasyEDA build 可能只返回 boolean/聚合结果,即使 `includeVerboseError=true` 也不保证有逐条 UI warning；CLI: `easyeda sch drc [--json]`。**不要单靠它宣称“官方 UI DRC 干净”**。
- `schematic.check` — 我们的逐条重建检查:从 primitives + 官方 `sch_ManufactureData.getNetlistFile()` 交叉校验,报告 net-marker/wire-name mismatch、multi-net wire、floating-pin、wire-crossing、wire-over-pin。CLI: `easyeda sch check [--json] [--strict]`。
- `schematic.export.netlist` — 导出网表为 artifact
- `schematic.export.bom` — 导出 BOM（csv 或 xlsx）为 artifact。CLI `easyeda bom export --type csv` **默认在导出后就地补全 LCSC C 号**（按 Manufacturer Part 关联 `standard-parts.json`，把 `Supplier Part` 从 `<MPN>.1` 改写为可下单的 C 号）；`--enrich=false` 关闭，xlsx 不补全（二进制）。补全是 best-effort（缺 python3/脚本只告警、导出仍成功）。脚本自动解析顺序：`--script` > `$EASYEDA_SKILLS_DIR/easyeda-agent/scripts/bom-enrich.py` > 二进制/工作目录向上找 `skills/` > PATH；安装版二进制在 `/usr/local/bin` 时设 `EASYEDA_SKILLS_DIR` 最稳。

## PCB（Phase 2，只读）

- `pcb.documents.list` — 工程内所有 PCB 文档（uuid + name）
- `pcb.components.list` — PCB 上的封装/器件（可含 pads）
- `pcb.layers.list` — PCB 层列表 + 当前层 + 铜层数（会先激活 PCB tab 保证 `currentLayer` 可读回；无当前层时附带 `visibleLayers` 作为显示状态证据）→ `easyeda pcb layers`
- `pcb.layers.set_current` — 切换当前编辑层（`--layer` 接受 id|层名|top|bottom|inner1）→ `easyeda pcb layer-set --layer bottom`
- `pcb.layers.visibility` — 显示/隐藏/聚焦层做视觉 QA：`--preset top-only|bottom-only|copper-only|silk-only`，或 `--show/--hide`（可加 `--exclusive` 只留所选）→ `easyeda pcb layer-visibility --preset bottom-only`
- `pcb.view.side` — 切到顶面/底面视图（选该面铜层为当前层 + 聚焦该面铜+丝印），随后 `pcb snapshot` 即反映该面。注意：EasyEDA 无原生画布翻面 API，这是「层聚焦」近似而非物理翻板 → `easyeda pcb view-side --side bottom`
- `pcb.nets.list` — PCB 全部网络

## Board（板子/组合 — 原理图↔PCB 绑定）

一个 **Board = 1 张原理图 + 1 块 PCB**，原理图与 PCB 就是通过它「组合」在一起（`import_changes` 也沿此链接同步）。Board 以**名称**标识。CLI：`easyeda board …`。

- `board.list` / `board.current` — 列出全部组合（名称 + 原理图 + PCB）/ 当前组合
- `board.create` — 把原理图和/或 PCB 绑成新组合（`--schematic` / `--pcb`）；游离 PCB 在 `import_changes` 前的修复手段
- `board.rename` — 重命名组合（`--name` → `--new`）
- `board.copy` — 复制组合（连同原理图 + PCB）
- `board.delete` — 删除组合（**需确认**，无 undo）

## Confirmation Required

- `schematic.component.delete`
- `schematic.page.delete`（删除图页，无 undo）
- `board.delete`（删除组合/板子，无 undo）
- `schematic.save`（未明确要求保存时）
- 生成的多步 mutation 计划
- `debug.exec_js`（任何情况）
