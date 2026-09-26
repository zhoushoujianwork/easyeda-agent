# 铺铜创建失败漏报修复：2026-09-26

`1.7.1-dev.11` 修复“边界参数未兑现却报告成功”：创建后按精确 ID 回读，
不符或不可读时保留对象 ID 与实际状态并失败退出。**离线验证通过；现场错误检测、退出和基线恢复
经独立复核通过，但本次定向计划未全部通过，成功创建路径仍未现场覆盖**。
本修复不解决宿主把请求 1 mil 读回为 0.2 mil 的原因，不补签
[dev.9 B08](2026-09-26-v1.7.1-dev9-B08.md) 或高级验收。

源码、Skill 与回归测试提交为 `07c3060`；修复验收阶段未推送或发布。
后续按用户要求登记线宽 [#260](https://github.com/zhoushoujianwork/easyeda-agent/issues/260) 和
优先级 [#261](https://github.com/zhoushoujianwork/easyeda-agent/issues/261)，开发提交与跟踪记录同步到 `dev`，不发布版本。

## 发现与边界

B08 的 no-pours、具名 follow-rule 区域及铺铜边界均出现 `1 → 0.2 mil`。
两个区域已正确返回 `verified:false/partial:true`、CLI exit 1；铺铜却返回请求参数、exit 0，
源码确认缺少创建后的 fresh 核对。三件失败对象均已精确清理，原基线恢复经独立复核通过。

只读诊断与下载的当前官方 API 脚本证明接口签名和连接器参数顺序相符；尚不能证明宿主
宽度差异的具体原因。原始材料在 `artifacts/cli-region-width-research-20260926/`：
`api.js` SHA-256 `37e674387ddbb7076e9e6218ab8cf3d6f48ef8566a0acbd083dcbd6cd0776430`；
诊断未编辑工程。`poured:false` 与实际铜列表为空独立保留，不据此推断铺铜算法或 DRC 故障。

## 修复行为

- 从 fresh 全清单匹配唯一创建 ID，核对网、层、填充方式、几何，以及明确请求的名称、优先级、线宽。
- 发现边界不符就停止；边界通过后才重建，并再次读取。`verified` 只表示边界参数，
  `poured`、`rebuildAttempted`、`rebuildError` 分别记录重建事实，不能替代实际铜清单和 DRC。
- CLI、内部调用及 Apply 拒绝未验证边界。pour-fit、power-pour、power-planes 保留部分结果，
  失败退出，停止后续铺铜、层转换或布线；不重试创建，也不自动删除可能已写入的对象。
  独立复核发现 dev.10 的 Apply 外层仍可用 verify/retry/continue 覆盖失败；该包保留但不安装。
  dev.11 使用专用错误贯穿 action/run 两入口，在外层恢复或继续策略之前停止，补完整队列反例。
- 同步公开 Skill、Cobra 帮助和 typed action 描述，保留当前宿主宽度能力边界。

## 验证记录

证据在 `artifacts/cli-pour-readback-fix-20260926/`。18 个连接器回归先在旧实现全部红灯，
修复后全部通过，涵盖宽度与其他参数错值、缺失/重复对象、回读异常、重建失败、重建后漂移
和等价多边形编码。Go HTTP fixture 同时证明直接命令、内部调用、Apply 和三个组合命令的
失败传播，不只测试解析函数。连接器全量 **629/629**、Go 全量、typecheck、Skill 检查通过；
dev.10 五平台本地包构建和资产校验通过，但因上述 Apply 漏洞未接受；最终 dev.11 的
30 个完整队列组合、Go 全量、Skill 检查、五平台构建及本地包 smoke 均通过。
其 TypeScript handler/test 与 629 项通过版本哈希相同；构建重新执行 typecheck。
没有创建 tag、推送或发布。

首轮 `go test ./...` 将忽略目录中的 Go 源码存档误当成独立包，失败原包保留；仅在本地
`artifacts/go.mod` 加存档边界后，全部实际 `cmd/internal/pkg` 包重跑通过，未修改冻结存档
或跳过实际源码测试。最终源码清单见 `candidate-source-manifest-v3.json`，SHA-256
`af3ff753d0a18de9c601e43f3f9dc5d7f6c70f409710111950ac38295d3ba660`。

dev.11 独立离线报告 `executor-review-dev11/findings.md`，SHA-256
`491544c91efcf6fdd890bb3cc12307476f8fcd8cf2b7eb3b455b26cd5ac6ce2b`；原来三个绕过反例
逐字复跑已关闭，action/run、退出码/输出及非铺铜控制均通过。该结论不代替安装和现场验证。

## dev.11 定向现场记录

CLI、daemon 与内置浏览器 connector 均已安装 `1.7.1-dev.11`。升级前后完整工程数据一致，
执行两个预先声明的独立输入，各创建一次：显式线宽 `1 → 0.2 mil`、未传线宽但优先级
`2 → 1` 均被 fresh 校验捕获，CLI exit 1，保留实际状态和精确 ID，未调用后续重建。
第二个用例原计划验证成功路径，但因优先级不符失败；不能称 `verified:true` 正路径已现场通过。
优先级变化的原因尚未确定，没有改参数或再次创建来取得通过。

首件在 `rebuildAttempted:false` 时已有与其 parent pour ID 对应的实际铜图元。
这只说明连接器未调用重建，不能说明现场没有铜；边界和实际铜必须分别读回。
该库存带 `staleRisk`，失败对象未保存重载，不据此验收铜的持久化。
两个失败边界及测试导线均已精确删除，保存重载后 PCB 完整数据只差采集时间，规则和 260 层
严格恢复；P1 完整数据与测试前一致。现场原始材料在
`artifacts/cli-pour-readback-fix-20260926/live-dev11/`。
112 件文件清单 SHA-256 `30a5f0a2aaadb2aa5b93751f6d8cf5708028309c1e0c8a5e2838e2b636f4b01e`，
完整命令和结果见 [dev.11 现场报告](2026-09-26-v1.7.1-dev11-pour-live.md)。
独立复核共 400 项证据检查通过；报告 `review/dev11-live-independent-review.md`，SHA-256
`478d2db894cecc96ffee8a8b63e1b1a5e655e6024a35b70a842e5cb730856e4a`。
其结论限定为错误检测/退出及清理恢复通过，第二用例失败、正向成功路径未覆盖；
不把证据完整性检查数当作设计用例通过数。

该阶段成本已单独写入台账一次：实际审计事件墙钟 8.18 分钟，daemon 累计 9.739 秒；
差值包含升级、执行和分析，不全是思考。368 条审计外层成功不覆盖两条 domain 失败；
未计量 token。源码和包身份的另一份独立预核报告为 `review/dev11-source-package-review.md`，
SHA-256 `3fde6d14ded53af0885ff03b24ef2f9864b7ceb7b682e7a5f9a457a8b0e56137`。
包内 Skill 绑定 `07c3060`；现场后补充的 Skill 限制单独提交，不修改已安装包或冻结源码清单。

[dev.9 B10](2026-09-26-v1.7.1-dev9-B10.md) 的恢复基线保留；页改名、边界线宽和本次
优先级不符均未解决。dev.11 完整 B00–B10 为 not-run，高级 A00 blocked、A01–A06 not-run；
不能沿用 dev.9 的通过项。
