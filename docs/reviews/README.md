# 历史验收引用

本目录仅保留发布材料仍引用的有限结论，不再按每次执行新增报告。
当前用法和能力见[文档导航](../README.md)与[CLI Status](../cli-STATUS.md)。

| 保留原因 | 结论入口 |
|---|---|
| v1.6 原理图未通过 | [原理图专项](2026-09-23-v1.6.0-schematic-acceptance.md) |
| v1.8 基础与高级测试边界 | [高级 CLI](2026-09-26-advanced-cli.md)、[铺铜回读](2026-09-26-v1.7.1-dev11-pour-live.md) |
| v1.8 原理图有限修复 | [布局回归](2026-09-27-cli-layout-regression.md)、[响应截断](2026-09-27-cli-response-truncation.md)、[原理图恢复](2026-09-27-cli-schematic-gate.md) |
| v1.8 PCB 有限修复 | [布局](2026-09-27-esp32-pcb-layout.md)、[导入](2026-09-27-pcb-import-confirm-fix.md)、[多边形投影](2026-09-27-pcb-polygon-projection-fix.md)（[细节](2026-09-27-pcb-polygon-projection-fix-detail.md)）、[丝印](2026-09-27-pcb-silk-align-fix.md)（[细节](2026-09-27-pcb-silk-align-fix-detail.md)） |
| v1.8.1 布局离线回归 | [#273 降压与升压](2026-09-29-issue-273-net-labels.md) |

可复现输入已移至[原理图布局 testdata](../../internal/app/testdata/schematic-layout/README.md)，
由自动测试读取；逐次命令、进程状态和临时产物留在本地忽略的 `artifacts/`。
新结论应更新对应的现行文档、测试或正式发布证据。

[发布证据](../releases/evidence/README.md)中的冻结文件及哈希保持原样，失败和未运行项不补签。
删除的过程记录仍可从[清理前固定提交](https://github.com/zhoushoujianwork/easyeda-agent/tree/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews)追溯；清理当前目录不等于擦除 Git 历史。
