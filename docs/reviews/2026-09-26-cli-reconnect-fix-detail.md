# CLI 重连修复详细记录

[简版结论](2026-09-26-cli-reconnect-fix.md)。本轮与已通过的删除定向修复分开，不修改其冻结证据。
所有新证据在本地忽略目录 `artifacts/cli-reconnect-fix-20260926/`。

## dev.4 B02 现场事实

重启前 P1 `e453c0063919726b` 与 PCB `3037fc1dfa4965d2` 均 typed save=true。
测试工程 `0f46d4361c4741a9ab7a9ed62164ca9b`，旧目标窗口 `db0fb7fd-68ea-4996-9d1c-cfaebcb8b822`。
只重启同版 `/usr/local/bin/easyeda` daemon，未改包和工程。13 次 health 在有界观察中未见目标。
完整约 177.919 秒新 daemon 日志只见 dev.1 无文档窗口；相应期间 audit 为 0 action。
没有同页重开、浏览器刷新或 GUI 工程操作，B02 记 blocked，不把原因猜成已证明的宿主故障。
原始日志在 `artifacts/cli-basic-dev174-20260926/B02/`。

独立复核 `review/B02-independent-review.md` SHA-256：
`5ed4879ec2d5c760ec72a7af079ef4d07673294ff3c21bfd83c26bb2d617e588`。
测试 daemon 已精确停止以保存日志；之后 root 单独启动 dev.4 诊断 daemon，
只读取 health 和另一个 dev.1 窗口的日志。这些不是 B02 重跑或目标窗口证据。

## 离线反例与修复

1. 原代码的 worker 每 3 秒驱动 watchdog，但注册前 200 ms 释放等待和 1.5 秒握手期限仍依赖
   普通 setTimeout。测试冻结全部主线程定时器，仅推进 worker：旧代码 90 秒注册 0 次、
   watchdog 强制重置 3 次；每次只排入另一个不会触发的注册定时器。首个失败断言及原测试
   保存在 `pre-fix-background-test.log`、`pre-fix-test-source.txt`。
2. 改用现有绝对截止时间登记器，由普通 timer 与 worker 扫描共同驱动一次性等待/截止。
   取消操作同时解除 pending attempt；到期、旧 session、取消后的回调不能注册或执行请求。
   即使超时 callback 尚未运行，超过绝对期限的 handshake 也拒绝；重复 handshake 不改 windowId。
3. sendContext 在 await 前绑定 session、windowId 和 socket ID，完成后漂移则丢弃。
   发布序号阻止同连接晚到旧读覆盖已完成的新读，包含新读与当前签名相同而无需发帧的情况。
   独立反例曾得到 `window-2 → window-1` 的发送顺序，daemon 原逻辑会被回写成旧身份；
   失败证据在 `review/transport-independent-harness.command.json`，不覆盖。
4. daemon 的 context 仅能更新该连接当前已注册的精确 windowId；未注册、空 ID 或错配 ID
   不改变身份、项目、存活时间，也不进入重复连接清理。合法同 ID 切页仍须测试通过。

这些修复不访问私有 EDA 工程接口，不改变用户权限或设计对象。worker 自身不可运行或整个
renderer 被暂停的情形不由这套定时器兜底保证；原目标后来自动恢复，后续日志进一步确认了下述时钟创建失败。

## dev.5/dev.6 离线检查

- 新的真实 transport 定向测试 10 项通过，包括 worker-only 重连、截止、取消、迟到回调、
  重复/错误 handshake 和两类上下文乱序。
- connector 全量 590 项通过，typecheck/build 和 skill-check 通过。
- 独立候选复核另有 12 项通过与 1 项旧版停滞负对照，详见新增增补报告。
- dev.5 只完成离线构建，未安装；dev.6 追加 daemon 的精确上下文保护，也只完成离线构建。两包都未包含后续真实沙箱时钟修复，均未安装。
- 不把 dev.3 的 40 轮或 dev.4 的 6 轮删除现场结果移签为 dev.6，也不移签 B00/B01。

后续现场须核对精确新版本、目标工程/文档、fresh 对象基线，再执行重启与回读。
当前高级 A00 继续 blocked，A01–A06 not-run；叠层 A 不是 PCB Layout 确认。


独立候选增补 `review/transport-context-fixed-review.md` SHA-256：
`1ffa1bbbc1e8e806c05d97fbbb4b4832d67bec4808e70a309fca4f1c842846da`。
冻结 transport 源码 SHA-256：
`4aaaa72f3762c4dd5c5e47b7cff1276b88652e8f72e83d02bb7947f080e6468c`。
该报告只签离线候选逻辑；现场失联的诊断和新包加载须另行记录。


## 真实沙箱时钟补充

10:14:36 UTC 目标自行恢复注册，非 GUI 或刷新恢复；不回填历史 B02。
`target-runtime-late-reconnect-logs.command.json` 记录此前 worker unavailable，
10:10:36 发起连接却到 10:11:36 才 register，下一次同样延迟一分钟。
`target-clock-capabilities.command.json` 证明扩展词法 Worker/Blob/URL 为 undefined，
浏览器全局对象有这些构造器；官方 sys_Timer 只是普通 interval 包装。
无工程写入的短寿命探针两次回调相隔约 1.5 秒，并终止 worker/撤销 URL。

新增 watchdog-clock 使用 globalThis 的构造器与方法接收者，只运行固定 3 秒时钟脚本。
部分创建失败和运行时 worker error 回收资源后仅降级一次；降级仍可能被后台节流，日志明确说明。
queued tick/error 在 stop 后失效。连接 stop 保留 action deadline sweep，deactivate/控制器替换
才完整释放 worker、URL、fallback interval、唤醒监听；再次 reconnect 可重新建立时钟。
源代码 VM 测试将裸名称置空，避免普通 Node 全局环境掩盖宿主问题。

独立时钟现场诊断复核 SHA-256：
`cd07ead5e6a9b016cd0d75f08b8007231e72060225c02ab5240942975987f4fd`。
最终候选须记录新包身份和现场结果，不能把探针成功称为连接器已修复。

最终构建候选为 `1.7.1-dev.7`。该源码 connector 全量 **599/599** 通过
（包括时钟 8 项与 transport 后台 11 项）；Go 全量及 daemon race 已通过。
新包现场结果在后续段落记录，以上结论仍为离线验证。


## dev.7 安装与初步现场回读

开发包 `dist/local-v1.7.1-dev.7` 已通过 typecheck/build、本地打包校验与 Skill/Agent 检查，
并通过本地 update 安装 CLI/Skill。未推送、未打 tag、未发布。
受限热更新核对同 UUID、旧 dev.4 bundle 哈希与现有权限后原子写入；返回 permissionsPreserved=true。
新 bundle SHA-256 为 `58c88d9edd000596bd9445ff6492053520f0f25de13244d578b2c3ac35b68066`。

CLI/daemon/目标 connector 均为 dev.7，Web 4.1.60。新目标窗口
`d8d8ad86-ae7a-4ddc-9c3b-43cb3c61664e`，项目/文档仍为原基线。
`dev7-setup/16-runtime-logs.command.json` 首次明确记录 `host worker ticker started`；
启动后一次 liveness lost 的重连从发起到 register 约 648 ms，随后完成注册。
这不是独立的 B02 正式复跑，完整基础门禁另开批次。

P1 升级前后完整 result/context 全等；PCB semanticSha256 同为
`41b23a1b3e7c54f623efa678dea430033717968444fefee90e1ccbfebce0d527`。
空板没有元件与板框，原 partial 保留；不宣称板边/尺寸几何已经验证。
逐命令原始记录及基线对比见 `dev7-setup/`。

旧版本控制器若没有 dispose 方法，兼容替换只能 stop，无法回收其旧 interval；本次安装走
已保存文档后的受限整页重载，旧 JS 运行时退出。新控制器之间的替换有完整资源回收验证。

最终源码与测试日志副本位于 `final-fix-snapshot/`，18 项 manifest SHA-256：
`161ba3d03d35661a6714472d2458e5ce5123beda7858d40c509d3f2e4047382f`。
已安装 CLI 与包内 darwin_arm64 字节相同，SHA-256：
`2d54f0aebb59abc8e0a6eaba32a583252ee841b658f11bd0b455af8927529d72`。

独立真实源码 VM 复核最终报告 SHA-256：
`8531105c5c05b982e7178da0117cb634f48e8095e7a8f2e7a142fbd1c1ea91a5`；
13 个候选场景通过，另有旧源码词法遮蔽负对照。绑定清单 SHA-256：
`504119af3004bfda613fe20243586b49853e5c0f71c20f1df7adf501756ecffd`。
报告位于 `review/global-clock-independent-review-final.md`，只签离线范围。


## dev.7 B02 正式复跑与独立结论

代码提交 `011a631`，包内 CLI SHA 与冻结源码身份保持一致。正式新批次在
`artifacts/cli-basic-dev177-20260926/B02/`；所有测试页先 saved:true，停止 PID1943
后由同版 CLI 启动 PID12826。2026-09-26 10:46:58.373870 UTC 新目标
`7db4d79b-75b5-4c36-9f89-591ba40f6e90` 注册，距旧进程停止 **10.277034 秒**。
未刷新、GUI 或重开恢复；停机到 ready health 的审计区间为 0 调用。
SCH result 完整相同，PCB 全部 JSON 仅 capturedAt 不同；最终回 P1。
执行及独立 B02 均 pass，旧 dev.4 B02 仍 blocked。

独立 B02 报告 SHA-256：
`5cbd12e8427f249d6c31d407df956a74f53c9993d483b54c6be0c3bd6ebee902`。
同包 B00/B01 亦独立 pass，但 B03–B10 当时尚未全部完成，不提前签基础总门禁或高级。
新 daemon 继续服务后续用例，随后于 dev.8 升级时停止；执行器已补 B02/
06-new-daemon-exit.json，UTC 11:06:47 完成、exit 0、完整 stdout/stderr 保留。

安装独立复核 `review/dev7-setup-independent-review.md` SHA-256：
`a136be80337e45b5e0cac48208f17bf5e2403088251a7fc069478f548a106018`。
其结论不依赖持续变化的 setup daemon.log；该文件停止后是 RTK 摘要，不作为完整流证据。
