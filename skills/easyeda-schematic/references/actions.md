# EasyEDA Action Reference

Run `easyeda actions` for the authoritative machine-readable list.

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
- `schematic.pin.set_no_connect` — 给引脚打/清「非连接标识」(NC, X 标记)，告诉 DRC 该脚是故意悬空。按 `--designator` + `--pin`（可多个）定位；`--clear` 清除。CLI：`easyeda sch no-connect --designator U1 --pin 23,24`
- `schematic.save` — 保存原理图（需确认）

## Library

- `schematic.library.search` — 自由文本搜索立创/EasyEDA 器件库，返回 libraryUuid + uuid

## Verify & Export

- `schematic.drc.check` — 调官方 `eda.sch_Drc.check` 作为 SDK DRC 门。当前 EasyEDA build 可能只返回 boolean/聚合结果,即使 `includeVerboseError=true` 也不保证有逐条 UI warning；CLI: `easyeda sch drc [--json]`。**不要单靠它宣称“官方 UI DRC 干净”**。
- `schematic.check` — 我们的逐条重建检查:从 primitives + 官方 `sch_ManufactureData.getNetlistFile()` 交叉校验,报告 net-marker/wire-name mismatch、multi-net wire、floating-pin、wire-crossing、wire-over-pin。CLI: `easyeda sch check [--json] [--strict]`。
- `schematic.export.netlist` — 导出网表为 artifact
- `schematic.export.bom` — 导出 BOM（csv 或 xlsx）为 artifact

## PCB（Phase 2，只读）

- `pcb.documents.list` — 工程内所有 PCB 文档（uuid + name）
- `pcb.components.list` — PCB 上的封装/器件（可含 pads）
- `pcb.layers.list` — PCB 层列表 + 当前层 + 铜层数
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
