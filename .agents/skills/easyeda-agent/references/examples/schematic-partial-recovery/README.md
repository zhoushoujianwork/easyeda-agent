# 原理图部分写入后的精确恢复

本例说明：写入返回失败时，先确认实际落地对象，再从真实状态重算。它不提供可直接重放的
器件、坐标或队列。先读 [原理图入口](../../schematic.md)及[数据驱动基准](../../schematic-data.md)。

**状态：`partial-live-verified`。** 2026-09-27 ESP32 三页原理图已完成现场保存重载和严格检查，
本批独立复核进行中；PCB、受控中断及完整 E2E 不由本例签通过。

## 来源与开始状态

从客户原始需求自主生成的 P3 有六个器件、51 个物理脚、27 个 NC。一次新导线 create 已返回 PID，
但即时完整读取尚无该线，受保护 Apply 因覆盖不足停止。稍后 fresh 确认只有该导线实际落地；
没有其他 wire、marker、bus 或绘图。P1/P2 电路及 PCB 保持原状态。

运行环境为 CLI/daemon `v1.7.0-45-g2c6cd35`、connector `1.7.1-dev.11`、Web 4.1.60。
原失败清单 SHA-256 `818e955feec36f15bc27c50d1e39ac51501b082da4ac2033b80a0b243507766d`；
新三页 146 文件清单 SHA-256 `1410c197c426231d34f8638146d516aa1d0ccbed9ca77dc3d37a0fee78567ed8`。
仓库证据索引为 [原理图续测记录](https://github.com/zhoushoujianwork/easyeda-agent/blob/dev/docs/reviews/2026-09-27-cli-schematic-gate.md)。

## 可迁移参数

| 参数 | 取得方式与限制 |
|---|---|
| 工程/页面 UUID | 本次 health 精确窗口及完整 fresh；不能从此例复制 |
| 失败批次范围 | 原始源、生成记录、journal、mutation 请求和 fresh 实际新增对象的对应关系 |
| 精确删除 ID | 临写前 fresh 重新核对的本批失败对象；不按类型批量删除 |
| 保护清单 | 完整 part→pin→attribute 父属、uniqueId、引脚/NC、属性状态、绘图和其他页面/PCB |
| 目标 composition/layout-page | 参数源和实测几何重新计算；原理图单位为 raw，y 向上 |
| before | 精确回退并 save→reload 后的真实完整回读；不能手改快照伪装空线 |
| 图签 | 本页 getter 字段及源中显式布尔；属性显隐与表格整体显示分别验证 |

## 实际步骤与命令形式

每条命令均带本次工程与页面路由。以下文件名只表示输入/输出职责，必须替换为本次新批次文件。

1. 停止失败队列，保存并导出当前状态。读取完整 `sch list --include-pins --include-bbox
   --include-wires --include-device-identity --include-page-primitives`，保留原始 JSON。
2. 比较候选与实际部分状态。本例只有一条本批新线，允许在声明范围内用
   `sch prim-delete --ids <fresh-confirmed-id>` 精确回退；随后 `sch save`、
   `doc reload <document-uuid> --json`，重新完整读取。
3. 验证保护对象。六件完整记录及 51 脚/NC 不变；被删导线的两个直属属性一同消失。
   394 个其余属性以唯一 parent+Key 配对，除运行期 primitiveId 外全部字段相同；
   171 个属性 runtime ID 重载时重铸，完整映射保留，不能称原始 record 全等。
4. 从实际 unwired 状态重新生成：

   ```bash
   easyeda sch compose --from composition.json --layout-page layout-page.json \
     --before fresh-unwired.json --replace --out plan.json --playbook apply.json
   easyeda sch apply apply.json --dry-run
   easyeda sch apply apply.json
   ```

   只有用户已授权重建该范围才使用 `--replace`。本例复用已放置的六件，生成队列没有 clear、
   删除器件或 place；73 步完整执行，不使用 resume/from/to，不改旧队列或源绑定。
5. 每页再次 save→reload→完整 fresh，逐脚网络/NC、真实线段、归属/direct 路径和官方位号 bbox
   对账；运行 `sch layout-lint --strict --json`、`sch check --strict --json`、
   `sch drc --strict --json`，用 `sch export-image --scope page --format svg --out page.svg`
   检查整页图面。图像不能替代对象回读。

## 错误与修法

- 部分电路尚未完成时，`--preserve-instances` 因 pin→net/NC 不同而拒绝是正确保护；
  普通 replace 也可能拒绝删除同绑定实例。不能把目标网改成当前错误状态，不能加 force 绕过。
- [#267](https://github.com/zhoushoujianwork/easyeda-agent/issues/267) 的保留器件清页未保护 pin-owned
  属性，本例没有调用该分支。只有完整保护证明成立，才可选择更窄的精确路径；否则保持未执行。
- 新 daemon 只对合法完整库存中的线段覆盖不足追加有界只读，不重复 mutation。
  本轮 P3 的 58 个唯一 wire/connect 请求中两次需要第二读，随后完整几何检查通过；
  预算、身份和拓扑要求仍按[连线规则](../../schematic-wiring.md)执行。等待到期仍失败就保存、fresh、重算。
- 图签 Drawed 属性值会额外显示作者时，在新源显式声明 false,false 后重新 Compose；
  本例三页黑色表格作者保留、额外蓝字消失。未知显隐不能猜 false，表格正文不能删除。

## 验证边界

新批三页共 51 件、195 脚（158 connected / 37 NC）、33 网、41 条外围归属、15 个 direct 物理树，
三个页面 strict 和 SDK DRC 均通过；原 PCB 184 条原生记录未变。原生备份 ZIP 有效但未验证重新导入。
这条精确恢复路径要求单一已知失败对象和完整范围外证明，不证明所有 partial 都能回退，
也不证明广义清页、带线模块移动、实板上电或整板布线已通过。
