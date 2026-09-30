# 原理图布局回归输入

这里保存自动测试读取的最小实测输入。JSON 保留原始字节，不保存生成布局、逐次日志或
本机路径；结果写入临时目录。测试件的来源几何不是可直接 Apply 的最终布局。

| 输入 | 覆盖的回归 | 自动测试 |
|---|---|---|
| [十个功能区](2026-09-27-cli-layout/README.md) | 默认求解、连接/NC、所有权及旋转许可 | `TestMeasuredCLIRegressionZones`、`TestUnboundedMeasuredZones` |
| [P1/P2 展开布局](2026-09-29-unbounded-layout/README.md) | 有限预算失败后的完整通道布局、内部 X 相交、无纸张渲染 | `TestUnboundedFormerlyExhaustedPages` |
| [#273 降压/升压](2026-09-29-power-layout/README.md) | canonical 输入保真、展开/标签模式和纸张边界仍有效 | `TestPowerIssue273LocalLayoutPreservesCanonicalEvidence`、`TestNetLabelsPowerIssue273` |

从仓库根目录运行相关回归：

```sh
go test ./internal/app -run 'Test(MeasuredCLIRegressionZones|Unbounded|PowerIssue273|NetLabels|LibLocalCLI)' -count=1
```

这些测试只证明各自声明的离线性质，不代表完整电路、宿主 Apply、保存重载或实板验收。
新增 fixture 应附公开来源、参数单位、复现命令和实际测试引用，不为每次执行另建报告。
