# 同文档连接互踢修复

状态：dev.9 连接身份修复已通过离线测试和现场定向恢复，完整 B00–B10 待重跑。dev.8 的 B00 仍为 blocked，高级验收未放行。

用户重新打开内置浏览器后，两条持续活跃的 connector 连接报告同一工程、文档和标签。
旧 daemon 将它们当成重复连接关闭其中一条；被关闭的一端重连后又替换另一端，导致
windowId 持续变化。这是连接身份判断错误，不能归因于用户未打开页面。

修复保留各条活跃连接。同文档多个候选也需要明确选择 `--window`，不按版本或连接先后
自动选择。过期 ID 仅保留诊断信息，不凭相同项目/文档转发动作；在途请求断线后不跨连接重放。
公开 Skill 与协议说明同步更新。

证据目录：`artifacts/cli-window-identity-fix-20260926/`。
只读冻结日志有 38 次 `duplicate connector session`，交替出现两组 activation/heartbeat；
能证明持续活跃的两个生产者，不能单凭日志判断它们属于两个物理浏览器页。

初审：`review/dev8-window-churn-independent-review.md`，SHA-256
`47caad28a42e14f59f46861ea7b5d076588877fa55027b1f94ad5ac74038396a`。
冻结输入 manifest SHA-256 `07007e4ab91e8139bfac75d6dc89da828eee83f6e3b3df18e067d3627d749d85`。

## 验证范围

- 真实双 WebSocket 交替发送同文档 context 与心跳，两条连接都保持在线。
- 未选窗和仅指定相同 project 的写请求拒绝，连接未收到动作；明确选窗分别回读。
- 新旧版本、先后连接都不改变歧义判断。
- 退休 ID 对同文档、同项目其他文档、同名不同项目均拒绝派发。
- 保存请求发出后连接断开，另一条同文档连接不接收重放。
- 原 context 身份校验、TTL 清理和窗口内请求处理保持回归覆盖。

离线通过不等于现场连接恢复，也不代替 dev.9 的完整基础与高级验收。B04 宿主改名失败仍需独立验证。

## 本地构建与定向现场恢复

- 完整 `go test ./...` 通过；窗口身份定向测试 `-race -count=5` 通过。
- 连接器 611/611 测试通过，typecheck、Skill 检查、五平台构建和本机离线包 smoke 通过。
- 独立离线复核 42 项（含子例）`-race` 通过；报告
  `review/window-identity-candidate-independent-review.md`，SHA-256
  `b227bfb4f008ea424ecb47a43bee4f17d29376b0524615d2b598a150590a8bae`。
- 新包 `dist/local-v1.7.1-dev.9`；实装 CLI SHA-256
  `149643c600c476e6b87e973722c5ff6f9a8f6bdefdee5ba92c7732b91899e4bf`；
  connector bundle SHA-256 `71b13d1edd4a57500c2934f1c86fc29006a46768fde6a219aea4cd06746bb304`。

旧 daemon 在互踢期间无法稳定选窗，因此先只替换 daemon，不刷新宿主。
临时 dev.9 daemon/dev.8 connector 仅用于诊断、选窗和保存，不签 B00。
双连接保持各自 windowId；DOM 只读对照显示旧页为 hidden 且 URL 含 PCB/P1，
可见页仅 P1，与 Codex 当前标签一致。明确绑定可见页，typed P1 保存返回 `saved:true` 后，
按开发热更新流程原子更新已安装同 UUID connector，保留权限并只重载可见页。
P1 reload 前后的完整 `sch list.result` 与 `context` 严格相同。

后台旧 dev.8 连接使首次全局包对账返回 exit 10（保留 14 号记录）。读取它的 shared runtime，
确认 implementation、windowId 和 hidden 状态后，仅调用本仓库已有 controller 的 `stop(false)`；
未刷新旧页、未改变工程或扩展权限。17/18 号记录确认唯一目标为 dev.9 且全包对账 `READY`。
稳定窗口为 `f7bf24e4-58a8-4a1b-b175-06f24baa48bc`，原工程/P1 UUID 不变。
这里证明连接恢复和本次 P1 保持，不补签其他基础项、PCB 数据或实际改名能力。

原始记录 07 使用了不存在的 `sch components`，CLI exit 1 且未派发；随后依据帮助改为 `sch list`。
这是执行命令错误，证据原样保留，不能计为通过的产品测试。

冻结证据 102 件，`evidence-manifest.json` SHA-256
`e5b578bcc84887e2eb2af8e3e74a7201f30fc537c2ddb46ca3565739f418870d`；
持续写入的 daemon 日志单独排除，复核使用 `dev9-daemon-snapshot.log`。
