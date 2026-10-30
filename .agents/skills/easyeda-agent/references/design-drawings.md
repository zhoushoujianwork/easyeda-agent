# 清晰设计图交付

适用于给用户交付原理图、PCB 正反面和简短状态说明。设计方法由需求决定；图册只组合已读取
与已导出的事实，不选择器件、不布局，也不以图像替代网络、几何或 DRC 检查。

## 从设计到出图

1. 在源数据中明确功能、核心与专属外围、真实引脚连接、NC、机械和电源约束。优先库器件，
   但库名称与可放置不证明引脚、焊盘或孔槽正确；依真实资料核对。参数生成方法可选。
2. 保留原始快照；候选中的位置要声明单位、原点和 anchor/bbox 语义。PCB 的 SDK bbox 含
   渲染元素，不等于机械 body/courtyard；移动用真实 anchor 或经核实的位移，不能混用中心。
3. 检查连接、外围归属与预览后，经 typed 命令执行；若部分成功，先回读再重算，不重放旧队列。
4. 显式保存、`doc reload --json`、新回读、新导图。重载回执记录的是 CLI 主机观察时间，
   不是 EDA 自报的设计修改时间；`doc ready` 只证明当前清单可读，不能补签此前失败。
5. PCB 按[两轮布局自检](pcb-layout.md#layout-完成局部电源铜与用户确认)核对；任何修改重新开始。
   逐项报告身份、引脚网络、NC、板框、位置角度和文字差异，不以总数相等代替内容相等。
6. 使用同一回读版本出图、打包。候选、未布线或 DRC 失败也可交付图册，但必须保留这些状态。

## 图像来源与朝向

| 图 | 来源和用途 |
|---|---|
| 原理图 SVG/PNG，可选 PDF | `sch export-image` 的官方文档导出；SVG 是可缩放主文件 |
| PCB TOP/BOTTOM PNG | `pcb stage-snapshot --fit-mode board`；官方视口图，原字节保留 |
| PCB 实际正反面 SVG | `pcb layout render --actual`；由实际回读生成的辅助图 |
| PCB 候选 SVG | 原有 `pcb layout render --candidate`；仅表达未执行或待核对的候选 |

官方底层聚焦图不是画布翻板。装配底视绕实际板框中心镜像，不能假定原点为零。
回读图显示实际丝印坐标，不为整齐重新安置标签；位号、镜像、旋转、字号与笔画线宽均按可读
字段展示。未知几何、线宽或钻孔不得用默认值伪装成实测；包络不是器件精确外形。
SVG 支持放大；PNG 的清晰度受原始采集限制，放大位图不能增加真实细节。

```sh
easyeda sch export-image --scope page --format svg --page <page-uuid> --out schematic.svg
easyeda sch export-image --scope page --format png --page <page-uuid> --out schematic.png
easyeda pcb dump --include-copper --out board.json
easyeda pcb layout render --actual --board board.json --side top --out pcb-top.svg
easyeda pcb layout render --actual --board board.json --side bottom --out pcb-bottom.svg
easyeda project drawings --from drawings.json --out drawings
```

先用当前 `--help` 核对目标路由及参数。现场窗口按工程和文档精确绑定，同一窗口串行调用；
底层视图操作后恢复原视图并回读，再生成正式复核图。图册命令为纯离线操作，不补采缺失数据。

## 采集与打包

`pcb dump` 与 `stage-snapshot` 保留目标、请求时间和缺测范围。
`pcb dump --out board.json` 同时生成 `board.json.capture.json`，记录实际文件字节的 SHA256、
大小和采集证据；stdout 模式保持单份 JSON。stage manifest 为各文件记录哈希。
这些是逐次响应边界的身份核对，不宣称原子快照，也不能排除单个 handler 内未观测到的切换。图像不存在、无法解码、文件哈希
不符或采集中途发生目标漂移均不能当成功。某一类读取失败不等于该类对象为空。
新采集的图片允许与旧图字节相同；显式查重选项用于录制诊断，不用于判定重载持久化。

图册包含原图、派生 SVG、一页 `README.md` 和 `manifest.json`。输入指定图源、身份、SHA256、
回读证据和当前状态；文件哈希由打包器核实。缺少官方图到回读的采集绑定时，必须标为调用方
声明的关联，不能冒充已验证的采集身份。生成目录成功不改变设计验收结论。
输出使用新目录，不覆盖源图和历史图册。原生工程备份独立保留，导出 ZIP 完整不代表恢复测试通过。

公开贡献使用合成或可再分发样例；客户图册、工程标识、绝对路径和原始日志保留在私有工程。
不将某块板的坐标、特定器件数量或当前 DRC 问题写成通用验收答案。

## 图册输入

将随 Skill 提供的 [drawings-input.example.json](drawings-input.example.json) 复制到采集目录，
填写实际 UUID、版本及每个文件的 SHA256（例如 `shasum -a 256 board.json`）；模板中的占位符
不能直接执行。路径相对输入 JSON，所有文件必须存在；输出目录必须不存在。当前范围是单页
原理图和单块 PCB，多页需要分别生成图册。

- `readbacks` 必须指向原始 JSON，用 JSON Pointer 指定工程、文档和时间字段。
  PCB dump 使用 `/capture/projectUuid`、`/capture/documentUuid`、`/capturedAt`；普通 action
  envelope 使用 `/context/projectUuid`、`/context/documentUuid`、`/createdAt`。
- 六个必需 `role` 是 `schematic-svg`、`schematic-png`、`pcb-official-top-png`、
  `pcb-official-bottom-png`、`pcb-actual-top-svg`、`pcb-actual-bottom-svg`；可选 `schematic-pdf`。
  每项有 `path`、`sha256`。实际 SVG 必须由上述命令从同一份 PCB JSON 生成并带有效来源元数据。
- 官方图的 `evidence` 可绑定原始导出回执或 stage manifest，填写回执路径/哈希、身份与时间指针，
  `artifactSha256Pointer` 指向该图哈希。stage manifest 使用 `/capture/completedAt` 和 `/sha256`；
  其他回执按真实字段填写。没有可验证回执时省略 `evidence`，结果会明确保留关联未证实状态。
- `status.layout` 为 `candidate|verified`，`routing` 为 `unrouted|partial|routed|unknown`，
  `drcPassed` 为布尔或 `null`，`designAccepted` 为布尔，`unresolved` 为问题列表。
  它们是调用方声明，打包器不会代替设计审查；互相矛盾的验收声明会拒绝。
- `status.pcbReview` 独立声明 `labelDensity`、`textCollisions`、`copperGeometry`，各为
  `pending|pass|fail`。省略项默认 `pending`；`designAccepted=true` 要求三项明确 `pass`，
  并保留原有布局、布线、DRC 与未解决项约束。旧已验收输入需先补充真实复核结果，不能直接
  将模板值改为 pass。manifest 与 README 保留原声明，打包成功不代表自动检测或验收通过。

三项复核绑定同一保存 → 真实重载 → 新回读版本：网名密度在 TOP、BOTTOM、叠层及整板/局部
缩放下检查短线和转折处叠字；文字碰撞检查网名、位号、其他可见文字及交付裁切后的可读性；
铜几何按 PID、网络、层、端点及线宽核对，覆盖焊盘、过孔和铺铜，区分同网冗余与异层投影。
连通性和原生 DRC 独立报告；辅助 SVG 不一定含宿主原生网名，不能代签图面复核。
只调整显示时限于可读、可恢复的 typed 接口，恢复旧值并回读；隐藏网名不能证明布线修复。
缺少接口或现场证据时保持 pending，不能通过 GUI、任意 JS 或阶段签字补证。


官方图片按原字节复制。SVG 检查基本结构和主动/外部内容，PNG 实际解码；PDF 仅检查文件头尾，
不宣称 PDF 语法或渲染已核验。图册不证明电路正确、布局被接受、制造可行或实物通过测试。
