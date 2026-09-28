# 2026-09-29 原理图布局放宽实验

## 结论

在 `dev` 的 `0cb3a09` 源码构建 CLI，复用 2026-09-24 已冻结的 P1/P2/P3 实测几何做纯离线实验。P1、P2 输入均不含纸张边界；P1 已是最小 `spacing:10`。P1 USB 模块改成完全不含 `zones` 的单区输入后，与原四区输入的第一个 USB 区同样在 20,000 候选耗尽。取消纸张或功能框设置不能解除这次区内阻塞。

P2 收紧区域间距并允许外围旋转后，100,000 候选仍没有完整布局。P3 的两件 UART 区在不指定纸张时完成布局与 SVG 预览。结果只说明本批输入的候选搜索行为，不证明 P1/P2 几何不可解，也不代表现场 Apply、保存重载或最终纸张验收。
此处“去掉 `zones`”仅将失败的 USB 功能组按同一核心和连接策略送入单区入口；没有把 P1 全页 15 件合成一个搜索区。后者会改变跨区连接和外围归属，需要独立设计与验证。

| 输入与单一实验意图 | 结果 | 本机墙钟时间 |
|---|---|---:|
| P1 原四区副本，USB 区先求解，20,000 候选 | USB 区 `candidate-budget-exhausted`；未输出完整布局 | 23.68 s |
| P1 仅 USB 七件，去掉 `zones` 和 `spacing`，其余区内数据/预算不变 | 同为 20,000 候选、10 次回退、3 次迁移尝试后耗尽 | 23.12 s |
| P2 原两区副本，`spacing` 从 30 改成 10，20,000 候选 | MCU 区 `candidate-budget-exhausted`；末次记录 R5 放置冲突 | 2.55 s |
| P2 同一 10 raw 副本，允许电容/电阻四向、晶体管/开关两向，100,000 候选 | 17 个姿态尝试均失败；原姿态用 75,000，余下尝试共用 25,000 | 7.66 s |
| P3 两件 UART 区，原输入直接 `layout-plan --zones` → `layout-render`，不提供 sheet | 完整局部布局与 SVG 均成功；无合页检查 | 0.11 s + 0.02 s |

P2 旋转实验只为测搜索可达性；旋转后的文字与引脚在真实编辑器写入前仍须重新量测。17 次姿态尝试中的后期单次额度只有约 833 候选，不能据此断言这些姿态无解。旧 `dev.14` 冻结报告还记录 P1 200,000、P2 最小间距 500,000 候选耗尽，结论边界相同。

原理图内部 X 交叉已经按不导通处理，`TestProperCrossingDoesNotMergePhysicalIslandsOrRenderDot` 与 `TestWireTContactForeignRejectedAndMergePreservesJunction` 在当前源码均通过。端点/T 接和共线重叠仍构成实体接触；穿过器件、其他网络引脚或位号也会被拒绝。旧大预算报告最后观察到的冲突分别是 GND 与 UART0_RX 缺少安全命名引线，不是“原理图必须像 PCB 一样绕开每个 X 交叉”。`direct` 的真实线树要求增加了耦合，但本实验没有量出各阶段分别耗去多少候选，不能把预算耗尽全部归因于 `direct`。

另一个 2026-09-27 [十区复测](2026-09-27-cli-layout-regression.md#同日开发修复离线复测)在当时开发候选上全部得到离线布局，包括 buck 区；本次 P1/P2 负例不覆盖该输入，也不代表所有复杂模块都失败。

## 可复核输入与命令

原始测量源位于本机忽略目录 `artifacts/easyeda-v160-schematic-clean-20260924/`；这次生成的输入、完整报告、P3 布局和 SVG 留在本机忽略目录 `artifacts/easyeda-layout-eval-20260929/`，不提交私有原始几何。由当前源码 `go build -o /tmp/easyeda-layout-eval ./cmd/easyeda` 构建；未连接 EDA。输入变换仅如上表所示。复核命令形式：

```sh
/tmp/easyeda-layout-eval sch layout-plan --zones --from <P1-or-P2-input.json> --out <layout.json> --report <report.json>
/tmp/easyeda-layout-eval sch layout-plan --from <P1-USB-no-zones-input.json> --out <layout.json> --report <report.json>
/tmp/easyeda-layout-eval sch layout-render --from <P3-layout.json> --out <P3-no-sheet.svg>
```

生成输入的原始字节 SHA-256：P1 四区 `83eb03a03933755a6ff6db6a828fe02fc8db5f48196ee14b5fdd0a18715a3fed`；P1 无 zones `4f9486acb9d36a433da511cb59add988b82d31c75d3b85f0536b83cd10be16c9`；P2 10 raw `01d3005443695fea3b9eb76e6a5b851a2a45d96d4b0cba325c9105fe01ecaf7c`；P2 旋转 `f402060dd9d07245475289b1e939c798892cc5ff3629dffe7fd40ee070bbf6f1`。P3 原输入 SHA-256 为 `5a155f5d21f7a4891ae8dc2500d900965c9d9ca137f1c1fba557c6c2db2fb502`。

## 同日新增：无边界通道模式

新增 `layout-plan --unbounded`，源 JSON 等价设置为 `layoutMode:"unbounded"`。保留实测
姿态与核心/外围归属，分列摆件，每网独立水平通道，标记放到通道末端；不做区域宽高
和纸张装箱搜索。导线内部 X 相交沿用原语义，端点/T/共线短接、符号/位号/引脚出口
检查不变。源输入不用改线网或减器件；默认搜索行为保持不变。

使用相同的 20,000 候选源输入，增加 CLI 选项；耗时为本机单次 CLI 墙钟，含启动和
写 JSON，不含编译，不是基准分布或性能承诺：

| 输入 | 无边界模式 | 消耗单位 | 图面与验证 |
|---|---|---:|---|
| P1 四区、15 件 | 0.032 s 完成 | 80 | 全部布局与无 sheet SVG 成功 |
| P2 两区、15 件 | 0.034 s 完成 | 70 | 全部布局与无 sheet SVG 成功 |
| P2 旋转许可副本 | 0.035 s 完成，仍保留实测姿态 | 70 | 不触发姿态搜索；无 sheet SVG 成功 |

消耗单位在新模式是每器件、每连网引脚、每网通道一次，不等同于旧算法的空间候选
质量指标。P1 USB 内容包络为 1275×476 raw，P2 MCU 为 1981×1711 raw；展开图线长、
交叉与留白较多。已目视检查 P2 预览：能显示完整器件与连接，但不能称为紧凑美观排版。
这解决了上述冻结样例的求解耗尽，尚不能推断截图中的降压/升压输入也通过；待用户 issue
提供那份源数据后复测。

为避免负例只保存在本机，本次将不含工程/宿主身份的 P1/P2 源副本纳入
[回归 fixture](fixtures/2026-09-29-unbounded-layout/README.md)，字节哈希与上文相同。
原始工程、日志及完整现场快照仍不提交。新模式另测十区实测 fixture、同源确定性、
刚体几何/引脚/NC 保真、真实交叉与 runtime guard、固定无纸张渲染、非法 attachment、
堵塞引脚出口、显式锚点、模式冲突及耗尽时不覆盖旧输出。

验证完成：`TestUnbounded*` 与既有 X/T 接触定向测试共 27 项通过；`make test`
全仓通过，`make skill-check` 通过（201 个 Skill 文件及样例一致性），`git diff --check`
无错误。P1/P2 报告的源哈希均与原始输入字节吻合。

本轮仅离线实现和验证，没有改用户工程或宿主纸张设置。现场交叉行为、Apply 与保存
重载没有补签；不能把离线渲染称为已完成现场原理图。
