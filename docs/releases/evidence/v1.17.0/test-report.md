# v1.17.0 软件专项验收报告

日期：2026-11-07。范围由本版用户明确决定，详见 baseline.md。
结论仅覆盖下列实际完成的软件自动回归与开发候选验证；真实 EDA 现场验收及完整设计 E2E 未执行。

## 用例结果

| ID | 结果 | 实际证据与边界 |
| --- | --- | --- |
| M1 | pass | Go 原始 JSON 日志重新统计 6104 项通过、1 项跳过，16 测试包通过、2 包无测试；action_access、compose schema 与协议目录测试包含在该结果中，go vet 通过。 |
| F1 | pass | Connector 1379 项全部通过，包含 schematic-geometry-read/actions 夹具；Go schematic_geometry 与 budget 回归通过，独立源码复核确认当前页唯一目标、完整 marker 指纹和 mutationApplied 分阶段语义保持。 |
| F2 | pass | schematic-text-create、cmd_sch_text 和 actions_schematic_text 的回归包含在全量通过日志中；独立复核确认单次 create、knownID 新 getAll、中文/LF及部分失败保护，不宣称真实宿主文本已落盘。 |
| E1 | pass | sch_preserve、图签、schematic-native-attributes 和 attribute-move 测试包含在通过日志中；核验原生属性归属、引脚保留和受保护文字移动合同，未把夹具回读称为真实 EDA 重载。 |
| L1 | pass | owned/isolated daemon 自动测试通过，包含父进程崩溃关闭管道；dev.3 本机实际二进制另验双实例、占用端口拒绝、错误预期版本拒绝和各自 EOF 退出，transport/日志回归亦通过。 |
| N1 | pass | MCP 10 项、发布脚本 123 项、Agent 13 项及 192 文件 Skill 检查通过；GitHub 五 job 含 Windows/macOS/Linux native install 成功。Windows 用户报障环境与 Desk 安装仍未验收。 |
| R1 | pass | dev.3 五平台二进制均绑定 95e5e4ed、vcs.modified=false；九项资产 SHA256、15 项本机 CLI 冒烟、首次/升级安装 CLI 与 192 文件字节匹配，旧哨兵已移除；下载通道使用本地精确 URL 替身。 |
| E2E | not-run | 本版用户在被告知缺少现场验收后要求直接发布，完整设计及真实 EDA 保存重载新回读延期 follow-up；没有执行最小点灯板全流程，也不沿用旧版本现场通过结论。 |

Go 的 1 项 skip 为本机不具备 Windows PowerShell 5.1 的平台限定测试；不把跳过计为通过。
五平台 CLI 构建不等于这些候选发布二进制已在所有平台实机运行。候选制品与正式包分别核验。

## 可追溯证据

产品源码基准：`95e5e4edc40a255d4ca9941c30614b91d1c0702c`；正式元数据准备：`a1a0889b41f25e06b3089fe036f8b725215740bc`。
`ea61da6b..95e5e4ed` 的连接器运行时代码没有变化，因此原始 Connector 日志对应同一实现。
[集成 CI](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/42379344150) 对应 `e93c3217`，五项任务全部成功；
[正式元数据 CI](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/42381076820) 对应 `a1a0889b`，亦全部成功。
运行流水留在本地忽略的 artifacts，公开包不包含个人路径、现场工程身份或私有原始数据。

| 原始记录 | SHA256 |
| --- | --- |
| dev.3 go-test.jsonl | ae7257a49a1a785076930cd613e8fcff4693f08750aa02bad3d683ca99eba792 |
| dev.2 connector-test.log | ac0e020e702f0bf1cf7efa067ab01a6b3948b8bd3a631be245d756f5acbf9fd5 |
| dev.2 mcp-test.log | 41748aec8b6c76540516fca46f657b2260c220a143a82fd93d070ea2634bc819 |
| dev.3 binary-identities.json | 50a17d9c7c5355a345f2e418411bbbe649f0454572e418031c5ac6ffe456742f |
| dev.3 owned-binary-smoke-result.json | d458cb3d03410e851e17326b4725f086f7ae138edde96906ebbe9e9497f49499 |
| dev.3 installer-smoke-result.json | a022ebc677b53886a4af9e5cbfdf50f8a62eee111a125812ec0e23999f4d49fe |
| dev.3 local-dev-smoke.log | e0999035fd6a9d1a474422cc06fb5c4f43209f60fbc34da5b014dfa063f7d30b |
| v1.17.0 release-script-test.log | a5a2b75c26d03b00b61fab110c821b110e368ac7be972b346bf3a9763e2cb246 |

## 现场回读

状态：**not-run**。浏览器控制宿主不可用；当前候选的真实几何读取、文本创建、NC 写入、显式保存、
真实重载及新回读未完成。无现场 saved:true、DRC 或落盘一致性结论，不把离线夹具、历史测试或 PR 作者记录替代现场证据。
Windows 自动安装 Access denied 仍是作者报告的未解决边界；CI 安装面通过不代表该用户环境已修复。
本次发布不自动更新正在使用的 CLI、daemon、连接器或 Desk，也不代表立创连接器市场已经更新。

## 独立复核

第一阶段独立 Agent 已只读核对源码、重算原始测试日志、验证五个平台二进制身份和九项资产哈希，
并重新逐字节比较隔离安装的 CLI 与 192 个 Skill 文件，未发现源码发布阻断问题。
第二阶段独立 Agent 已只读复核本版基准、用例、报告及 manifest，核对本版用户范围决定、源码对应关系、
用例结果、原始记录与材料 SHA256，并确认正式元数据 CI 成功。材料与实际完成的软件验证一致，
独立复核结论为 pass，仅适用于 v1.17.0 声明的软件专项范围；真实 EDA 现场验收及完整设计 E2E 仍为 not-run。
正式资产须另从固定、干净 main 构建，并完成后续资产及安装独立复核；对应结果另记发布准备文档，
不把未发生的正式构建、安装或远端发布写入本冻结报告。
