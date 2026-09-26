# 原理图写后发布夹具

`sch-p3-wire-publication.json` 来自 2026-09-27 ESP32 原始需求回归的 P3 第 21 步。
请求为 `(185,685)→(205,685)` raw；SDK 返回 PID `bdee3961676b43e4` 与反向等几何原始线段。
紧接着的官方 `components.list` 却为 `wires=[] / wiresAvailable=true`，稍后完整 fresh 读取才包含同 PID。

源审计三条原始记录保存在本地
`artifacts/release-v1.8.0-wire-settle-fix-20260927/initial-failure-audit.jsonl`，SHA-256
`5436f92c74447aa965234ad781591a3a2b9b6fa9d6ac82738be0c12d090249cb`。
后续回读来源为 `a01-schematic-completion-20260927/page-3/failure-full.json`；该批清单 SHA-256
`818e955feec36f15bc27c50d1e39ac51501b082da4ac2033b80a0b243507766d`。

夹具只投影七件对象的身份、类型、实测 bbox/姿态/引脚与原始线段；省略供应商资料、属性和网表值。
这不改变几何，不是预制布局答案。`before` / `immediateAfter` / `publishedAfter` 独立保留，
不能把后来可见的线倒填到即时回读中。夹具证明失败路径和有限读取修法，不认证实际宿主的发布延迟上限。
