# E2E 自动化验收标准(Automation Acceptance Standard)

本标准属于[高级 CLI 业务验收](cli-advanced-test.md)。先完成
[基础 CLI B00–B10](cli-live-test.md) 的同版真实链路门禁；基础动作仍有故障或缺测时，
不以本标准的求解、DRC 或设计结果补签基础层。
用户明确接受的本次已知限制按[高级准入范围](cli-advanced-test.md#进入条件)执行；
v1.8.0 的三项例外不免除客户需求、实际电气/几何质量、保存重载或独立复核。

## 默认收尾用例（2026-09-27）

用户已选择[ESP32-S3 最小点灯板](test-case-esp32-blink.md)第一节为默认输入：
板外稳压 3.3V、单页原理图、两层 PCB，按公开 Skill 的 S0–S6/P0–P10 完成。
具体步骤见[用例详细稿](test-case-esp32-blink-detail.md)，不喂预制 BOM/UUID/网表或坐标。

完成判据是需求与 pin→net/NC 正确、无重叠/越界、DRC 无错误且全部必接网络连通、
丝印极性正确、save→真实 reload→fresh readback 一致；布局的两轮自检及用户确认仍执行。
检查只报告当前事实，不新增阶段签字或评分门限。实板烧录/上电点灯不由软件回归代签。

原四层开发板和芯片级板为扩展回归，按任务或受影响能力补测；小板通过不证明它们通过。
新两层用例目前为 not-run，v1.8.0 的完整四层 E2E 仍为 in-progress。

## 现行原理图验收入口（2026-09-14）

统一遵守 [数据驱动架构基准](../.agents/skills/easyeda-agent/references/schematic-data.md#数据驱动架构基准)
和 [S0–S6 流程](../.agents/skills/easyeda-agent/references/design-flow.md)。原始客户需求仍为完整回归输入，
不能喂预制 BOM/UUID/网表；用户只要求原理图时止于 S6，不宣称 PCB 或整板通过。

- 保留原始快照、源目标的连接/核心外围归属/约束、参数/代码版本、哈希及生成记录。
- 数据校验覆盖唯一归属、外围跟随、真实直连、器件/引脚/导线/标记/位号/框和纸张边界。
  型号、参数、描述等非位号器件属性文字排除页面碰撞与框包络；位号不排除。
- 失败回改源数据、采集或算法并重算；不得手改队列、现场试摆或看截图补签缺测项。
- Apply 后按目标逐项回读、逐页 `sch gate --strict`，另核对覆盖缺口与生成溯源，最后确认
  `sch save` 返回 `saved:true`。同网/同框、单一 DRC 数字或高分均不证明全部通过。
- 发布、离线回归、安装版现场验证分别记录；新规则只在源码通过不能称安装版已验证。

## 历史记录

原 2026-07 四层回归、旧判据与能力缺口完整保留在[同名详细稿](e2e-automation-acceptance-detail.md)。
