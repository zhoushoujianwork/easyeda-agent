# 连接器旧运行时残留：诊断与恢复

## 症状

重新导入或升级连接器后，扩展管理器里看起来只剩一个版本，但
`easyeda health` 仍然可能列出多个窗口或多个连接器版本。典型表现是：

- 同一个 EasyEDA 工程/文档出现多个 `windowId`；
- 日志里先后出现旧版和新版 `register`；
- `system.health`、`document.current` 等读操作正常，但
  `schematic.component.place`、`delete`、`document.open` 等写操作超时；
- 一次失败的写操作卡在连接器 FIFO 队首，后续动作跟着超时；
- 重新导入 `.eext` 后问题仍在，甚至重载页面也不一定消失。

## 根因

`.eext` 的导入和扩展管理器列表不是编辑器页面中已经运行的 JavaScript
实例。旧实例会继续留在已打开的 EasyEDA 页面里，并使用原来的 WebSocket
身份重新连接 daemon。于是同一个文档可能同时有 1.3.x 和 1.4.x 连接器
注册。旧实例不会因为扩展列表里被删除而自动停止，页面 reload 也可能受
EasyEDA 的 `sys_WebSocket` 固定身份和异常关闭状态影响，无法真正替换旧实例。

因此，看到多个版本时不要把它判断成多个 daemon。先看 `health` 的
`windows[]` 和监听端口；通常只有一个 daemon，问题在编辑器页面内的旧
连接器运行时。

## 用户侧恢复步骤

按下面的顺序操作，避免在连接器状态不确定时继续发写请求：

1. 停止当前批量 Apply 或脚本，保存能够保存的 EasyEDA 工程。
2. 在扩展管理器中卸载旧的侧载连接器，再导入目标版本的 `.eext`。同一
   UUID 只保留一个安装项；市场版和侧载版不要同时留在同一 profile 中。
3. **完全退出 EasyEDA Pro**，确认所有编辑器窗口都已退出后再重新启动。
   仅 reload 页面或重新导入扩展不足以停止已经运行的旧实例。
4. 如果 daemon 在此前的失败请求中留下了旧队列，重启 daemon：

   ```bash
   easyeda daemon start
   ```

   开发环境使用 `make dev` 时，不要手工 kill air 管理的 daemon。

5. 重新打开目标工程和文档，等待连接器完成注册，然后检查：

   ```bash
   easyeda health --project "宏恩门禁底座载板"
   ```

   通过条件是：目标工程只有一个窗口、文档 UUID 正确、
   `connectorVersion` 是期望版本。随后先执行一个读操作（例如
   `easyeda sch list --doc <doc-uuid>`），读操作正常后再发最小写操作。

网页版如果 daemon 重启后一直没有重新注册，关闭目标 tab，再用工程 UUID
重新打开一个 tab；不要反复 reload 同一个 tab。不要为了清理连接器而清空
站点数据或 IndexedDB，那会连同扩展安装记录一起删除。

## daemon 侧防护

daemon 已加入两层防护，用户恢复后不必手工清理历史 `windowId`：

- 连接器超过心跳 TTL 未更新时自动退休；
- 同一 project/document/tab 的重复注册只保留最新连接，并主动关闭旧连接；
- CLI 路由重复连接时优先选择最新、版本较新的连接。

这些机制只能防止旧连接继续被选中，不能停止 EasyEDA 页面里仍在运行的旧
JavaScript。因此版本升级后的“完全退出并重启 EasyEDA”仍是必要的恢复步骤。

## 验证清单

恢复后至少记录以下结果：

```bash
easyeda health --project "<project>"
easyeda sch list --doc <doc-uuid>
easyeda sch check --doc <doc-uuid>
```

`health` 应只有一个目标窗口；`sch list` 应能稳定返回同一个文档上下文；
`sch check` 若有问题，应区分真实原理图 finding 和连接器超时，不能因为一次
写超时就重复 Apply。若再次出现多窗口，先保存 `health` 输出和 daemon 日志，
再关闭所有 EasyEDA 窗口重启，不要继续堆积请求。

## 本次现场证据

宏恩门禁现场恢复后，60832 只有一个 daemon 监听，`health` 只列出一个窗口，
连接器为 1.4.0，`schematic.components.list` 正常返回。此前同一页面曾同时
出现 1.3.1、1.3.2 和 1.4.0 的注册，且放置动作在连接器 FIFO 队首超时；这与
旧页面运行时残留的特征一致。

