# v1.20.0 定向修复验收基准

## 本版用户范围决定

2026-11-24，用户在得知「v1.19.0 已发布、DSH 插件市场的 tarball 资产只能随新版本产出」后，选择
**发 v1.20.0（需现场验收）**，并同意使用当前已连接的 EasyEDA 窗口做现场验收。本版因此采用
schema 3 `cli-fixes` 定向范围：

- 全量自动回归；
- 页面改名（同页加载恢复与宿主拒绝诊断）、PCB 孔/槽几何与板边测量、写前拒绝、写入检查点、
  候选制品身份的 DEV 现场验收；
- v1.19.0 正式 CLI 的负对照。

完整设计 E2E 延期至 follow-up。该决定只绑定 v1.20.0；报告里的 `pass` 只表示下列用例通过，
不表示电路、制造、硬件或完整设计验收通过，也不沿用任何旧版本的现场结论。

## 原始需求与起点

完整设计的默认原始需求仍是 `docs/test-case-esp32-blink.md` 第一节，本轮未执行该流程。现场没有
输入预制 BOM、UUID、网表或坐标答案；用例里的对象（`ceshi` 工程、PCB1、页面 P1 / LED 指示）都是
既有测试工程内容。

现场起点：一次性测试工程 `ceshi`，PCB 文档 `PCB1`（33 个器件，USB-C 槽孔 4 个），原理图页
`LED 指示`（`8fb55bd61d0986a9`）与 `P1`（`6abf3fbf13dc52f0`）。验收结束时 `P1` 已还原原名。

## 源码与环境

- 软件候选：`1.20.0-dev.1`，由 `scripts/build-local-snapshot.py` 从固定提交
  `529bd59d5f895e4e7f518f02a6756224911318ad`（origin/dev）构建，`local-dev.json` 记录
  `sourceDirty=false`、`versionInjected=true`；CLI SHA256
  `ac1626689fb736052f0c864f063b015ec19143d4473e314808cc16ea1e6f75f3`。
- Dev 宿主运行时：edadesk Dev 内的 `1.19.1-dev.4`，`sourceRevision`
  `fb80cb360c8d26ef516c552fcce189d17f474db1`，bundle SHA `ab33e0b6…`。
- `fb80cb36..529bd59d` 之间**没有运行时相关文件差异**：改动全部是 `docs/`、`package.json`、
  发版脚本与测试（`git diff --name-only` 过滤后为空）。因此 Dev 宿主运行时与候选的 EDA 行为一致；
  本版报告把现场运行的身份记为「候选 `1.20.0-dev.1` / 等价运行时 `1.19.1-dev.4`」。
- 宿主：edadesk Dev 0.2.0 中的 EasyEDA Pro 4.1.105；daemon 端点 `127.0.0.1:60833`，连接器
  `1.19.1-dev.4`，工程 `ceshi`。
- 负对照：从 GitHub Release `v1.19.0` 下载的 CLI，`easyeda-agent v1.19.0`，SHA256
  `919b059ef5eef8b3c1c4acf6eb61319b6e5efca39e725f765b0e630f4f7d0501`。
- 平台：macOS arm64。Go、Node、Python 版本与全量回归结果见报告。

## 通过阈值与不豁免项

- 七个用例（M1、F1、F2、E1、L1、N1、R1）必须按本版定义完成；E2E 明确 not-run。
- 自动回归不得有失败；平台限定跳过如实保留。
- 只有被本版用例覆盖到的行为可以写 pass；未被现场覆盖的改动（自动布线取消作业、部分删除路径、
  图签压线的检出路径）必须标为仅自动测试。
- 不因为其他页面、其他板或历史版本通过就推断本版通过；不把菜单、自动保存或旧读数当作验收证据。
