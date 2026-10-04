# Web 文档源码探针

这是文件后端研究的第一层验证，不是通用源码 Apply 或 eprj3 工程导入。只使用用户授权的
专用 Web 测试工程，先由 `health.windows` 核对窗口、工程、文档和运行中的连接器。

```sh
easyeda doc source get --window <window> --project-uuid <project> --document-uuid <doc> --out before.json
easyeda doc source roundtrip --window <window> --from before.json --out dry-run.json --dry-run
easyeda doc source roundtrip --window <window> --from before.json --out roundtrip.json
```

命令不接受全局 `--project/--doc` 路由，避免切页与源码调用之间的隐式目标变化。输出路径
必须不存在，源码快照保留原文、目标、字节数和 SHA-256。快照中的源码不是可编辑的写入计划：
roundtrip 先检查文件哈希、目标和 fresh 源码逐字相等，然后只调用一次 setter，参数是宿主
fresh 源码。dry-run 仅读取与比较。未知接口、空/超限源码、错页和 stale 快照均在写前拒绝。

该比较是写前检查，官方接口没有原子 compare-and-swap。验证期间保持专用页静止，不让用户
或其他客户端并行编辑；快照哈希仅用于检查文件完整性，不是宿主认证或电气正确性证明。

setter 的 false、异常、迟到或回读变化都不算通过。不自动重试、不自动回滚、不以 GUI 或
`debug.exec_js` 恢复；未结束的 setter 保持连接器队列阻塞。保留原始快照和失败回包，从
真实状态决定下一步。命令的 `verified` 仅表示立即源码相等，`persistenceVerified` 保持
false；显式保存、typed 重载、fresh 源码和全部对象/引脚网络对账后才能判断持久化。
此探针不触发 daemon autosave；保存由单独的 typed 命令执行并留下独立证据。

测试时另留原理图 connectivity/list 或 PCB dump、DRC 与 typed 图像。原样 roundtrip 不改变
设计，不证明生成器、自动布局/布线、eprj3 folder 同步或 Web 工程导入可用。尚未完成
现场核查时保持 `offline-verified`，记录缺口；原始响应留本地忽略的 artifacts。
