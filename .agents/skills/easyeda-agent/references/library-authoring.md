# 数据手册驱动的自动建库

目标是让用户只提供准确型号或 PDF：Agent 通读手册、提取带页码的证据、生成确定性几何，
再由 CLI 校验并写入 EasyEDA。不要让用户重复选择手册中已经明确的参数；只有封装后缀、
多种 land pattern 或手册缺失等真实歧义才询问用户。

## 工作流

1. 先 `lib search` / `lib by-lcsc` 查现有 Device。型号、封装和引脚定义完全匹配时优先复用，
   不重复建库。
2. 获取原厂 PDF 并通读目录、订购信息、pin table、package drawing、recommended land pattern。
   扫描件先 OCR；表格与尺寸图同时看文字层和页面图。必须锁定完整 MPN 与封装后缀，不能把
   同系列其他 variant 的引脚或尺寸混入。
3. 写一份完整 Device JSON。`evidence` 记录厂家、MPN、封装变体、手册定位信息，以及引脚表、
   外形图、推荐焊盘各自的 1-based PDF 页码。可保存 SHA-256，防止同 URL 文档静默换版。
4. Symbol 使用真实 physical pin number。可按功能分组排布，名称和电气类型来自手册；不因
   多个 GND 同名而合并编号。Footprint 单位为 mil，尺寸换算固定为 `1 mm = 39.37007874 mil`。
5. Land pattern 的依据只能是：`datasheet`（厂家推荐图）、`calculated`（按明确标准/公式计算，
   在 note 写标准、目标密度和关键假设）、`copied-verified`（复制已有封装并在 note 写来源与
   尺寸对账）。器件 body/lead 尺寸不能直接冒充铜焊盘尺寸。
6. 运行 `easyeda lib device validate --spec device.json`。默认要求 symbol pin 与 footprint pad
   编号集合完全相等；机械焊盘等例外必须在 `pinMapping.footprintOnly` / `symbolOnly` 逐项声明。
7. 门禁通过后运行 `easyeda lib device build --spec device.json`。该命令在任何 EasyEDA 写入前
   重跑同一校验，然后创建 Symbol、Footprint、可选 3D Model 和 Device；失败会尝试回滚。
8. 回读 Device 绑定、symbol pins 与 footprint pads，放置一个实例核对 Pin-1/极性/旋转，运行
   原理图检查和 PCB DRC 并保存。`verified:true` 只证明对象成功创建，不证明封装电气/机械正确。

## 规格契约

从 [library-device-example.json](library-device-example.json) 复制骨架。关键字段：

- `evidence.datasheet.locator` 可为原厂 URL 或本地 PDF 路径；`pinoutPages`、
  `packageDrawingPages` 至少各一页。
- `landPattern.basis=datasheet` 必须给 `pages`；`calculated` / `copied-verified` 必须给 `note`。
- `symbol.geometry` 直接传给 `library.symbol.build`：`outline[]` 至少四个 x/y 点，`pins[]` 的
  `number/name/x/y` 必填。
- `footprint.geometry` 直接传给 `library.footprint.build`：每个 pad 的 `number/layer/x/y/shape`
  必填，`shape` 为官方 pad tuple，类型必须是字符串：`["RECT", 宽, 高, 圆角%]`、
  `["ELLIPSE"|"OVAL"|"NGON", 宽, 高]` 或 `["POLYGON", 多边形源]`；数字类型（如 `[1, w, h]`）
  宿主会在写入中途拒绝，现已在写入前拦截。图形线为 `lines[]`。
- `--library` 用 `lib symbol/footprint create` 回包**顶层**的 `libraryUuid`；工程库资产自身记录里的
  `libraryUuid` 会写成个人库 uuid，照抄它会打不开编辑器（CLI 会拒绝并给出正确值）。
- 桌面本地库可能以库名作为 `libraryUuid`，资产记录该字段为空，而编辑器报告内部 UUID。
  通用打开接口无标签页时，连接器回退到对应资产的 `openInEditor`；别名匹配仍须核对
  已读到的资产、完整 `uuid@库名` 标签页和文档类型，不仅凭同名或打开返回值允许写入。
- 封装建在**个人库**（`lib footprint create --scope personal`）：Web 4.1.71 的 API 打不开工程库封装的
  编辑器，build 会被拒绝；符号可建在工程库或个人库。
- pad 可声明 `solderMaskAndPasteMaskExpansion`，仅支持官方 `topSolderMask`、
  `bottomSolderMask`、`topPasteMask`、`bottomPasteMask` 四个有限数值字段，单位 mil、每侧扩展；
  `0` 表示零扩展，省略表示不指定。当前公开 SDK 无法独立回读规则策略，因此拒绝 `null`。
  按焊盘类型及所在面使用支持的字段，
  不把通孔焊盘的钢网字段当作有效开窗。示例：`{"topSolderMask":2,"topPasteMask":0}`。
  普通 `get()` 会把开窗舍入到 0.1 mil；逐 pad 新鲜读取后用公开 `toAsync()` / `reset()`
  恢复精确值并验证请求字段，明确保存后再次验证；字段丢失或 save 未返回布尔 true
  时停止并保留 partial/rollback 证据。`apertureVerifiedPads` 只计明确指定并回读通过的焊盘。
- `library.symbol.build` 与 `library.footprint.build` 只写入刚创建且可证明为空的目标资产。
  Connector 打开编辑器后、创建任何图元前读取目标图元 inventory；发现已有几何或无法完整读取
  inventory 时以 `PRECONDITION_REFUSED` 零写入拒绝。不要对同一 UUID 重放 build；当前不提供
  replace/清空模式，需要重建时删除并重新 create 一个资产，再用新 UUID build。
- 重复 pin/pad number、非有限坐标、非正焊盘尺寸、缺页码证据或未声明的映射差异都会在
  离线阶段拒绝，不打开 EasyEDA。

## 图形与实际接线复核

资产创建和编号校验不等于符号可用。建库后按源规格回读并检查：

- 矩形 outline 显式重复首点闭合；检查引脚线落到边框、名称在框内，不能只核对 rotation 数值。
- 引脚线形与电气类型是两个字段。用户要求普通线时用 `shape: "None"`；要求未定义类型时
  用 `pinType: "Undefined"`。不要把此要求推广为所有器件的默认电气类型，也不要因名称含
  CLK/CS 就擅自添加箭头或反相圆圈。低有效语义、1 脚定位标记应分别表达。
- 1 脚定位圆应为独立图形，放在 1 脚一侧；不是把 GND 设置成 `Inverted`。
- NC 不以单个 `noConnected:false` 字段或渲染图推断最终状态。若用户发现实例无法接线，
  先保留用户已完成的 NC 清理，再检查实际实例与接线行为；未验证实例时明确保留验收缺口。
  不把用户确认的 NC 叉号解释成普通连接点，不自动恢复用户删除的标记。
- “装上模块后仍可读”的丝印按模块覆盖范围避让，不只避开焊盘；用实际文字 bbox 检查
  框外位置、行列对齐及文字间距。源规格同步最终文字位置，并说明额外脚本步骤是否被 build 支持。

AS07-M1101D-SMA 现场案例曾出现边框未闭合、引脚朝外、时钟/反相装饰不符用户要求及
NC 误判；最终按用户要求使用普通线与未定义类型。该案例完成库级回读与官方渲染复核，
尚未完成放置实例接线、PCB DRC 或实物装配验证，不能将其视为这些项目已通过。

## 必须暂停的歧义

完整 MPN 对应多个封装、订购后缀无法确定、pin table 与 package 图冲突、推荐焊盘不存在且
计算依据不明确、EP/NC/DNU 的电气处理互相矛盾时，列出候选与证据页再询问用户。图像模糊时
换原厂 PDF 或更高分辨率页面，不凭轮廓猜尺寸。

官方 `eext-ai-library-builder` 的可吸收点是 PDF 转页图、自动定位 pin/package 区域和按封装类型
提取参数；其默认尺寸、一次视觉结果和人工框选都不能作为我们的最终真值。Agent 负责语义
理解，CLI 负责确定性校验和写入。
