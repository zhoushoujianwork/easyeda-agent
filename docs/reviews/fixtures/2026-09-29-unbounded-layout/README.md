# 无边界通道布局回归输入

来源：仓库 ESP32 测试工程在 2026-09-24 冻结的 P1/P2 符号测量，经
2026-09-29 放宽实验生成的源数据副本。仅保留稳定测试 ID、测量几何、引脚网络、
NC、功能归属与求解参数；不含工程 UUID、宿主图元 ID、库凭据、原始日志或完成布局。
坐标/间距单位均为 EasyEDA schematic raw。

- `P1-20k.json`：15 件、4 区；spacing=10，maxCandidates=20000。
- `P2-20k.json`：15 件、2 区；spacing=10，maxCandidates=20000。

开始状态为未求解的实测源输入。旧默认搜索均耗尽候选；新模式在命令行显式选择，
fixture 本身不嵌入模式或坐标答案，方便同源对照：

```sh
easyeda sch layout-plan --zones --unbounded --from P1-20k.json --out P1-layout.json --report P1-report.json
easyeda sch layout-render --from P1-layout.json --out P1.svg
```

P2 同理。预算错误仍非零退出，不产生或覆盖旧布局；错误引脚/异网 attachment、被文字
堵住的实测引脚出口仍须修源数据或采集，不能靠取消空间上限绕过。
验证状态：offline-verified；没有对新模式执行现场 Apply、保存重载或宿主交叉点回读。
详情与原始字节哈希见[实验报告](../../2026-09-29-schematic-layout-relaxation.md)。
