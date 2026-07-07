# 抄图训练 SOP — oshwhub 官方开源板闭环练习

用官方开源工程当「标准答案」训练 agent 的选型/摆放/连线全流程,用**逐 pin netlist
机械对照**做验收(不是人眼看像不像)。首个闭环案例:XDS110下载器
(`oshwhub.com/li-chuang-kai-fa-ban/xds110-xia-zai-qi`,45 器件/31 网络,
2026-07-07,174/174 pin 一致)。

## 流程

1. **打开官方工程**(oshwhub 项目页「打开设计图」直达 `pro.lceda.cn/editor#id=<uuid>`,
   已加载页面需 `navigate reload` 让 `#id=` 生效——见 environment-setup.md)。
2. **只读提取 golden**:`sch read` 拉全量 `{designator, footprint, supplierId(LCSC C号),
   pins:{number:net}}`,存成 `training/golden/<board>-spec.json`。**不 mutate、不 save
   官方工程**。
3. **批量选型**:golden 的 `supplierId`(LCSC C 号)逐个 `lib search --query "<C号>"`
   拿 `{uuid, libraryUuid}`。⚠️ **C 号搜索是模糊匹配,不保证唯一**(实测 C5665 命中了
   一个运放芯片而非目标 IDC 排针——两者恰好共享搜索关键字)。批量结果要抽查:
   footprint 类型对不上就手动改用 `lib search` 的品类关键词重搜,不要无脑信第一条。
4. **在 ceshi 重建**(golden 工程只读,重建做在 ceshi/`ceshi-project-disposable`):
   - `sch place` 逐件放置 + `sch modify` 设位号,**分散布局**(尤其密距头如 2.54mm
     排针单独放一角)——同一坐标窗口里堆太多小件,后续任何"删这个区域的 wire"
     式清理都会误伤邻居(见下方教训)。
   - `sch autoconnect --spec` 批量连接,**用 golden 的真实 pin→net,不要猜测网络名**
     (猜错了要返工删线删 flag,容易级联误删旁边器件的连接——本次训练真实踩过)。
   - **不同 symbol 变体的连接器,pin 编号体系可能不同**(如 USB-C:golden 用
     `usb-c-smd_type-c-6pin-2md-073` 单数字编号 1-14,标准库常用款是
     `type-c-16pin` 的 A1B12 式合并编号)——按物理引脚含义(GND/VBUS/D+/D-/CC)
     映射,不要按 pin 号硬套。
5. **机械验收**:`training/copy-check.py <golden-spec.json> <sch-read-output.json>`
   逐 designator 逐 pin 对照,`ok/total` 未到 100% 就看 issue 列表定点修。**目标
   100% 一致才算闭环**,不满足于"看起来差不多"。
6. **回归门禁**:`sch layout-lint`(0 overlap)+ `sch drc` + `sch check`(悬空 pin
   数应与 golden 的悬空模式吻合,不是越少越好——golden 本来就有大量 MCU 引脚
   未引出)。

## 已踩的坑(避免重犯)

- **批量连接前先冻结网络名表**,来自 golden 的真实读数,不要凭经验现编——编错了
  产生的返工(删 flag→留悬空 stub wire→区域查找误删邻居 wire)比多花 2 分钟读
  golden 贵得多。
- **autoconnect 对同一 spec 跑第二遍会在已连接 pin 上再叠一份 flag/wire**(不是
  幂等的)——发现连接有误时,先精确删除该 pin 现有的 flag+stub wire,再单独重连,
  不要对整个 spec 重跑。
- **删除 flag 不会连带删除它的 stub wire**——stub 留在 pin 上会变成自动命名的
  单 pin 网络(`$3N…`)。清理 flag 后必须同时找到并删除对应 wire(`debug exec`
  遍历 `sch_PrimitiveWire.getAll()` 按坐标窗口过滤是可行的应急手段,生产代码应
  走 typed action)。
- **按坐标窗口批量删除 wire 有误伤半径**——窗口内其他器件的线也会被扫进去。
  密集布局时优先精确到单个 primitiveId,不要图省事用坐标框。

## 产出去向

每次训练闭环后回灌三处:
- `docs/optimization-loop.md`:暴露的 CLI/连接器缺口。
- 本文件 / `conventions.md`:新发现的设计知识。
- `~/.claude/projects/.../memory/`:非显然的平台行为(如 C 号搜索模糊性、
  autoconnect 非幂等)。

见 `oshwhub-training-source` memory 记录训练进度与选板策略。
