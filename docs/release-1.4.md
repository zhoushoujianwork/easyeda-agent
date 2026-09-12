# 1.4 发布与验证

本页保留 1.4 系列的设计与历史验证记录。当前版本以
[GitHub Latest Release](https://github.com/zhoushoujianwork/easyeda-agent/releases/latest)
为准；1.5 的升级说明见 [1.5 发布与验证](release-1.5.md)，变更明细见
[Changelog](../extension/CHANGELOG.md)。

## 版本升级与保留规则

- 未修改连接器运行代码、构建配置、manifest 能力或 action 契约时，升 patch：
  `1.4.4` → `1.4.5`。
- 用户必须更新或重新导入连接器才能获得新行为时，升 minor 并把 patch 归零：
  `1.4.x` → `1.5.0`。破坏公共契约的变更仍升 major。
- CLI、daemon、连接器发布资产和 Skill 继续使用同一个完整版本号。连接器只差 patch
  时版本门会告警，差 minor 或更高时阻断，避免 CLI 调到旧插件不存在的 handler。
- 每条 minor 线只维护最新 patch，GitHub 的 `Latest` 和各 Skill 平台的 `latest` 指向
  最新版。已经发布的 tag、Release 与资产不删除；它们承担固定版本下载、回滚、校验和
  与审计职责。旧 patch 视为历史/superseded，不再补修；清理只针对本地 `dist/` 等
  可重建产物。

## 1.4.5 修复范围

本次维护版本落实 #203 的第一项建议：明确 Altium Designer `.SchDoc` / `.PcbDoc`
当前没有可用的 typed action 或 CLI 导入入口，迁移应使用 EasyEDA Pro 的
**文件 → 导入 → Altium Designer** GUI。导入后必须回读原理图连接、PCB 板框、层叠、
机械层和禁布区，并通过现有门禁。官方 beta API 返回 `undefined` 或未产生工程/文档
副作用时必须判失败；本版本没有宣称实现程序化 AD 导入或 preflight 命令。

## 1.4.4 修复范围

本次维护版本包含 PCB 文档定位/连续移动/保存重开和丝印修复、PowerShell 5.1
`--patch-file`、DSH Windows 路径转换，以及电阻参数来源核验和数值匹配。3D 请求体
上限提升至 32 MiB，并覆盖精确边界。已修复的 #190/#192/#201/#202 随发版关闭，
不等待报告者复验；PR #199 已按完整采纳关闭。

源代码 CI 与原生 Windows PowerShell/安装测试已通过；#191/#173/#43 继续开放。#200
此前已关闭并建议改用网页编辑器，其旧版宿主故障本轮未独立复现。关闭条目不增加
“原报告环境或整板已全部验收”的声明。

## 1.4 数据驱动原理图闭环

1.4 把原理图的权威来源下沉为本地 canonical connectivity JSON：稳定器件 ID、原始
位号、完整物理引脚、稳定网络 ID、pin→net 和显式 NC。功能名称单独存在 Role 中，不能
覆盖合法原始位号。

Agent 先在本地数据上设计功能 Lib、器件 XY/朝向和导线，再用 `sch compose` 组合页面并
生成受保护的 `sch apply` 队列。Lib 从左上开始按 Z 字排布，每框按自身内容独立收紧并
保留最小内边距；同行顶齐，下一行按本行最大高度推进。粉色虚线框及 0.2 inch 标题同样
由数据计算。GND/VCC 标志和短直引线用于减少环绕导线，端子引线可错落长度避免重合。

已有页面修改走 `sch design-diff`：比较器件身份、位号、引脚、网络、NC 和可证明的几何，
计算最小队列后顺序 Apply。只读快照没有提供的图面字段会标记为 `incomplete/unverified`，
不会被误报成同步。Apply 后逐脚回读，并运行 `layout-lint → check → bridge-check → DRC`、
显式保存和导图检查。

## 安装契约

```bash
curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | bash
easyeda update --check --exit-code
```

安装器更新 CLI/daemon 与已发现客户端中的 Skill，并打印同版连接器下载地址。
`easyeda update` 更新 CLI 与 Skill；侧载 `.eext` 需要从同一 Release 下载、卸载旧项后导入，
随后完全退出并重开 EasyEDA。立创插件市场支持原地更新，但审核版本可能滞后；严格同版
验证以 GitHub Release `.eext` 为准。完整步骤和 Agent 引导 Prompt 见
[快速开始](quick-start.md)。

## 可重复发行门禁

```bash
make test
make lint-test
make blocks-audit
npm --prefix extension test
make release-script-test
make release-check VERSION=v1.4.5
make release-build VERSION=v1.4.5
```

`release-build` 生成五平台 CLI、稳定 UUID 的连接器、`skills.tar.gz`、安装器和
`checksums.txt`，并核对版本、资产名、SHA-256、Skill 包内链接和本机 CLI。它不会创建
tag 或上传资产。正式发布由 `make release VERSION=v1.4.5` 完成；GitHub Release 发出后
触发 SkillHub CI，ClawHub 发布为 best-effort。

## 门禁控制板示例现场验证

两页原理图的真实 Apply/回读验证保留了 23 个器件、165 个物理引脚、原始位号、库身份
和稳定网络 ID。补入 RF/SD 8 条及 TALK I2S 4 条连接，解除 4 个错误 NC；最终本地数据为
28 个网络、98 条 pin→net。两页 `layout-lint`、clusters、`check`、`bridge-check` 均为
0 error、0 warn。

官方 DRC 仍报告 **3 WARN、0 fatal、0 error**，因此严格门禁没有通过。HostLink、PTT、
RF LED 缺件，TALK GPIO4 职责冲突没有猜接。导图仍有长型号/封装文字越框、局部文字拥挤
和少量竖排端口。guarded NC→connected 增量队列完成离线生成；现场因旧布局碰撞使用完整
compose。这些结果证明数据转换和 Apply 闭环可用，不代表整板电气、视觉或 PCB 已验收。

## 尚未完成的发布边界

- 本轮没有按 `esp32MiniRequire.md` 第一节重跑 `ceshi` 从原始需求到四层 PCB 的完整验收。
- 五平台资产会构建并做隔离 smoke；非 macOS 平台的原生执行以 GitHub Actions 为证据。
- 通用外围电路不会凭空推断；缺少数据手册、器件或职责冲突时必须保留未决状态。
- 单页放不下时允许由上层规划拆成两页，1.4.5 不宣称自动分页。
