# v1.21.0 定向验收基准

## 本版用户范围决定

2026-11-25，用户要求发布 `pcb.silk.delete`（#341）。被告知该改动新增连接器动作、按版本规则须升中版本并提交
现场验收与独立复核材料后，用户选择 **发 v1.21.0**，以当天在 EdaDesk DEV 上的现场验证加独立 Agent 复核为准。
本版因此采用 schema 3 `cli-fixes` 定向范围：

- 全量自动回归；
- `pcb.silk.delete` 的现场写入（内置 AI 按 ID 删除自己添加的自由丝印）、保存重载与新回读、写前拒绝；
- v1.20.0 正式 CLI 的负对照；候选制品身份与运行时等价性。

完整设计 E2E 延期至 follow-up。该决定只绑定 v1.21.0；报告里的 `pass` 只表示下列用例通过，
不表示电路、制造、硬件或完整设计验收通过，也不沿用旧版本的现场结论。

## 原始需求与起点

完整设计的默认原始需求仍是 `docs/test-case-esp32-blink.md` 第一节，本轮未执行该流程。现场没有输入预制 BOM、
UUID、网表或坐标答案：唯一写入对象是测试中新建、随后删除的自由丝印文字 `DEL341`。

现场起点：一次性测试工程 `ceshi` 的 PCB 文档 `PCB1`（`f708451540690854`）。本轮 PCB1 是空板（两次重载就绪回执
`componentCount:0`），与 v1.20.0 基准中 33 个器件的 PCB1 状态不同；起点自由丝印 0 条（出自 AI 回读回复，见报告），文档源哈希
`dd146105fc12b4a298afdd58d4e36a990797c75b3c12a39353f7cb0640121035`。验收结束时回到同一源哈希、0 条自由丝印。

## 源码与环境

- 发布候选：`55fef8f4e0acbf0de7e1f24f40c349f5496ae3d8`（`origin/dev` `1516cc8c` 加版本同步与 Changelog）。
- 现场运行时：`scripts/build-local-snapshot.py --version 1.20.1-dev.2` 由 dev `1516cc8c2fef1e63a16fca3ce796c5553326bb46`
  构建，`local-dev.json` 记录 `sourceDirty=false`、`versionInjected=true`；CLI SHA256
  `fba9e991d62cdfa1daafcfe668c8a57eb9b407b50e350e4d65cd423892f1f5c6`，连接器 `.eext` SHA256
  `9939a8ec43985bc1784a2f9456388d2b47cfda601284896cd3552e57dcfcb32b`；daemon 与连接器回读版本均为 `1.20.1-dev.2`。
- `1516cc8c..55fef8f4` 只改版本字符串、Skill 版本元数据与 Changelog；`extension/src`、`internal`、`cmd`、`pkg`
  无差异。现场运行时与候选的 EDA 行为因此等价，报告把现场身份记为「候选 `55fef8f4` / 等价运行时 `1.20.1-dev.2`」。
- 宿主：EdaDesk DEV 0.2.1 中的 EasyEDA Pro 4.1.105（`r1-health.json`）；daemon 端点 `127.0.0.1:60833`。
  内置 AI 的模型请求为 `edadesk-gpt-6.1-sol`，提示含 `[edadesk 自动模式]`（Auto），宿主 CLI 事实为
  `local-c0976fb7…`、`1.20.1-dev.2`、SHA256 `fba9e991…`（`f1-session-context.log`）。EdaDesk 应用的具体 dev 修订号
  不在原始日志中，不作为证据。
- 负对照：GitHub Release `v1.20.0` 的 `easyeda_darwin_arm64`，SHA256
  `5ab32d282820735e993564d03af093a00032c26a27e7bbe8856a8b6fede49777`（与该 Release 的 `checksums.txt` 一致）。
- 平台：macOS arm64；Go、Node 版本记在 M1 日志头。

## 通过阈值与不豁免项

- 七个用例（M1、F1、F2、E1、L1、N1、R1）必须按本版定义完成；E2E 明确 not-run。
- 自动回归不得有失败；平台限定跳过如实保留。
- 只有被现场覆盖的行为可以写 pass：锁定文字、非丝印层文字、属性 ID 的拒绝与“清单读不到 → partial”只有自动测试。
- PCB 文档不做自动检查点（设计如此，见 L1），不得写成已有 PCB 检查点；回退证据以现场源哈希恢复为准。
