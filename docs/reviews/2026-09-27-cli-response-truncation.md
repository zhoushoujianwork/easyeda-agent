# CLI 大响应截断修复与续测

2026-09-27 完整 ESP32 用例的 P1 最终回读发现 [#263](https://github.com/zhoushoujianwork/easyeda-agent/issues/263)：
CLI 只保留动作 HTTP 响应的前 1 MiB，造成完整页 JSON 解析失败。通用读取已修复，
现场完整只读响应已成功；**仍未签整页网络、保存重载或 E2E 通过**。

## 原始失败

CLI/daemon `v1.7.0-31-g8c4d3e4`，connector `1.7.1-dev.11`，Web 4.1.60。
三页规划采用本轮自主源、最终 NT 电感及实测旋转文字；真实页 Compose/dry-run 为 277/163/88 步。
P1 前 272 步成功，第 273 步 `verify-all-pins-nets-nc` 的 `schematic.components.list`
返回 `expectSchematic: decode schematic.components.list response: unexpected end of JSON input`。
一次独立完整读取仍失败，stdout 恰好 1,048,577 字节（1 MiB 加换行）。

前序 29 件及计划接线、图框/图签保留。typed save 返回 `saved:true`，原生导出 ZIP 完整，
`restoreVerified=false`；P2/P3 仅图框，PCB 184 原生记录与起点相等。没有重放旧队列或进入依赖写入。
本地批次 `artifacts/release-v1.8.0-e2e-20260927/a01-live-schematic-20260927/` 冻结 157 文件，
清单 SHA-256 `f32e7ce11f27d79305f7aa222a139792932c264a871093f717aa2992a628c6e3`；
原生包 SHA-256 `50f652abc7513f1897167e57d47bd2fa12885390fbf2eb55f2c730b803e4df55`。

## 修复与验证

公共 `postAction` 原先使用 `io.ReadAll(io.LimitReader(resp.Body, 1<<20))`，达到上限不报错。
修复后动作 HTTP 响应限 32 MiB，health 单独保留 1 MiB；读取多一字节检查溢出，超限/读错误
返回明确错误和 nil body，关闭响应，不输出部分内容、不自动重发动作。连接器的 WebSocket 限制是另一层约束；
这不是无限响应支持，也不改变连接、文档、器件身份、几何、网或 NC 守卫。

新增真实 HTTP 正负例在旧代码下三项均失败：2,673,265 字节整页被截断；超限返回 nil error 和
部分 body；合法 JSON 前缀的超限 health 被误判 found。修复后相关测试 7 pass（含子测试），
覆盖完整尾部器件/引脚/导线、明确溢出、精确边界与中途读失败。全量 Go 测试 3,924 pass / 0 fail /
1 skip，15 个包通过；Skill 检查和 diff 检查通过。

修复候选运行戳 `v1.7.0-32-g4d23925-dirty`（当前修复 diff），connector 不变。
唯一一次现场完整只读采集返回 1,388,244 字节，exit 0、JSON 完整，目标 project/doc 一致；
29 件、88 pins、`pinNetsAvailable` 与 `wiresAvailable` 可读。尾部 R14 引脚坐标/网络存在，
不是仅凭字节数判断成功。此时未再次写入、重载或运行原理图验收。
输出 SHA-256 `72e86a1102a7117b6cafe65b608c0054b28b596f34982eb434a000b5788f7acd`。
命令、stdout/stderr、测试日志保留 `artifacts/release-v1.8.0-response-fix-20260927/`。

P1 失败尝试另记成本台账，UTC 2026-09-26 请求区间 18:32:35–18:48:50，实际动作到 18:48:35。
墙钟约 16.00 分钟、daemon 2.56 分钟、差值 13.44 分钟，1,294 次调用；daemon failure=0
不包含 CLI 的截断/解析失败。token 未记录（JSON 中 0 不代表无消耗），未登记为 E2E 通过。

修复已提交 `d3409a0` 并推送 dev。重建后 CLI/daemon 均为 `v1.7.0-33-gd3409a0`，
health 精确核对测试工程/P1、connector dev.11 与 Web 4.1.60；执行员随后独占窗口开展恢复。
独立离线复核无阻塞 finding，三个受审文件的 SHA 绑定该提交；独立定向测试通过，并复计全量日志。
报告 `artifacts/release-v1.8.0-response-fix-20260927/independent-review.md` SHA-256
`418a75479ab7849db6272e8a8ba16ffb9687ecf6360e1b154828e20c827edab9`，输入记录 SHA-256
`9c38b973aa4fd3807787f91fc43a262890a7c991c825036e212095594406fa2c`。
该复核只签代码、离线回归和单次完整只读回包，不签整页连接、保存重载或发布；原基准和后补提交身份分开保留。

随后 d3409a0 的 P1 完成从 fresh 状态编译的 15-step 恢复、显式保存重载及完整逐件/逐脚/线树对账，
经过独立有限复核后关闭 #263；实际宿主隐藏属性 ID/位置归一化差分保留，不声称完整 result 字节相同。
具体复核范围、哈希以及新图签/严格检查失败见[原理图恢复记录](2026-09-27-cli-schematic-gate.md)。
这次关闭不补签 F2/E1/SDK DRC/E2E，不表示 v1.8.0 已发布。

## 后续

以修复后的完整 fresh-before 重新生成受保护 Compose；相同目标由生成器输出验证/图框步骤，
不使用 `--resume`、旧区间或手改队列绕过守卫。差异必须查明再修改源和重算。
完成 P1 网络/几何/保存重载后再继续 P2/P3、四层 PCB、两轮布局复核及用户布局确认。
#263 保持 open 等现场恢复与独立复核；正式发布和完整验收仍未完成。
