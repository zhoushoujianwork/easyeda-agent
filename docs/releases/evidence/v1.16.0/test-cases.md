# v1.16.0 验收用例（待范围确认）

| ID | 计划 | 输入、步骤与判据 |
|---|---|---|
| M1 | planned | 对固定候选运行 Go、Connector、MCP、typecheck/build、vet、发布脚本、agent/Skill 检查；记录日志和退出码，不用旧 CI 代替本轮构建。 |
| F1 | planned | 核对 Type-C 双 5.1kΩ 按钮模块的真实 block_symbol；两端接线一个未接与三个端口全部接线均须保存重开后网名正确，KEY 关联内部按钮。 |
| F2 | planned | 核对双 ARC 圆、ARC+L 半圆、混合轮廓和原 points 矩形的持久化原生边界与实际铜；开环与零弧度不写入。 |
| E1 | planned | 核对官方 Gerber mm/inch、坐标 CSV/XLSX、UTF-16、mm/mil 换算、PCB 身份与文件哈希，明确不含制造验收。 |
| L1 | planned | 核对 CBB ID 无实例、错误变体、非空符号和 stale 矩形拒绝；图形清理不得改变引脚、polygon 或原工程既有对象。 |
| N1 | planned | 核对官方 SVG 内嵌静态图、导图审查与布线验收的区分；宿主弹窗通过已有授权处理，未知框不盲目确认，不声称通用自动应答接口。 |
| R1 | planned | 固定候选与资产哈希，完整发布构建和隔离安装升级冒烟，核对共享服务未变及本轮临时资源清理，独立复核后冻结材料。 |
| E2E | pending-user-scope | 是否执行完整 ESP32-S3 最小点灯板端到端，待用户为 v1.16.0 明确选择；此前版本的延期不能自动继承。 |
