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
- `schematic.snapshot` — 截取当前渲染区域为 PNG artifact（`easyeda sch snapshot --fit` 先适应全部再截，整张图入画）

## Mutate Schematic

- `schematic.component.place` — 从库放置元件（libraryUuid + uuid + x/y）
- `schematic.component.modify` — 修改位置、位号、BOM 属性等
- `schematic.component.delete` — 删除元件（需确认）
- `schematic.wire.create` — 创建导线折线
- `schematic.netflag.create` — 创建电源/地/网络端口/短路 flag
- `schematic.power.connect_pin` — 复合操作：从 pin 拉导线 + 在末端放 flag（防止 flag-on-pin DRC fatal）
- `schematic.save` — 保存原理图（需确认）

## Library

- `schematic.library.search` — 自由文本搜索立创/EasyEDA 器件库，返回 libraryUuid + uuid

## Verify & Export

- `schematic.drc.check` — 运行 DRC，返回 passed + violations
- `schematic.export.netlist` — 导出网表为 artifact
- `schematic.export.bom` — 导出 BOM（csv 或 xlsx）为 artifact

## PCB（Phase 2，只读）

- `pcb.documents.list` — 工程内所有 PCB 文档（uuid + name）
- `pcb.components.list` — PCB 上的封装/器件（可含 pads）
- `pcb.layers.list` — PCB 层列表 + 当前层 + 铜层数
- `pcb.nets.list` — PCB 全部网络

## Confirmation Required

- `schematic.component.delete`
- `schematic.page.delete`（删除图页，无 undo）
- `schematic.save`（未明确要求保存时）
- 生成的多步 mutation 计划
- `debug.exec_js`（任何情况）
