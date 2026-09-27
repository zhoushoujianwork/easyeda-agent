# v1.8.0 CLI 修复发布

2026-09-27 用户已批准直接发布 v1.8.0，范围为**已验证的 CLI 修复**。
完整 ESP32 成品测试留待后续，在 [CLI Status](../cli-STATUS.md) 继续跟进；不再等待整板布线、最终 GND 内电层和 DRC 才能发版。

## 本次改动

- 修复连接器重连与窗口身份处理。
- 修复原理图规划、完整回读、图签属性、连接检查和延迟导线回读。
- 修复 PCB 导入重复确认、多边形焊盘投影及位号碰撞误报。
- 同步 Skill 操作说明、错误处理和真实回读经验。

三页原理图、单次 PCB 导入、位号修复与未布线布局已完成有限现场和独立验证。
局部修改、写前拒绝与受控恢复只签已记录子例，不外推带铜移动或整板成品。
已知问题仍在 [cli-known-bugs.md](../cli-known-bugs.md) 维护；#55/#260/#261 为本次已接受限制，历史失败不改判。

## 发布进度

**已正式发布：[GitHub v1.8.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.8.0)。**
发布时间为 2026-09-27 12:08:27（UTC+8）；annotated tag 对应提交 `2734b298b7dfd09d000b412d94420401248bc66c`，已集成到 main。
GitHub Release 为非草稿，五平台 CLI、连接器、Skill、双平台安装脚本、验收包和校验和共 11 项远端资产
名称、大小及 SHA256 全部匹配。[checksums.txt](https://github.com/zhoushoujianwork/easyeda-agent/releases/download/v1.8.0/checksums.txt)
包含十项载荷的摘要，校验和文件自身也已与 GitHub 摘要核对。

正式构建、release-check 和离线 smoke 通过；本机正式 CLI 的 15 项命令检查、201 文件 Skill 包及连接器版本/UUID 校验通过。
发布脚本测试 122 项、协作检查 13 项通过；运行逻辑与已采用的 `83a98b2` 一致。
其他平台仅编译与资产完整性检查，不声明跨平台运行或正式 1.8.0 现场全集复测。

验收材料使用 schema 3 `cli-fixes`，记录用户的版本与范围决定；完整 E2E 保持 in-progress。
此前完整设计报告原文保存在 [历史执行记录](evidence/v1.8.0/test-report-detail.md)，不补签完整设计通过。
独立范围复核见[复核报告](evidence/v1.8.0/independent-review.md)。
ClawHub 已成功发布 `easyeda-agent@1.8.0`，回执 ID 为 `k979dcfrqscpn9se5wqg0ks3g18f6460`。
skillhub.cn [自动发布任务](https://github.com/zhoushoujianwork/easyeda-agent/actions/runs/36293441739)已成功；平台审核与连接器市场人工提交另行跟进。

[CLI 修复报告](evidence/v1.8.0/test-report.md) · [基准](evidence/v1.8.0/baseline.md) ·
[用例](evidence/v1.8.0/test-cases.md) · [发布流程](../release-workflow.md)

## 已接受的已知限制

页面改名 #55 使用核对过的 UUID；区域/铺铜边界线宽 #260、铺铜优先级 #261 在无特定要求时使用宿主默认值。
这些字段的错值检测和失败退出继续保留；具体影响和剩余验证见基准，不声明其无功能影响。
用户此次批准的 CLI 修复发布范围不自动用于其他版本，也不改变后续工程写入与布局确认规则。
