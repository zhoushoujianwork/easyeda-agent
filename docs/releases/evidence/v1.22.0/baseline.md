# v1.22.0 定向验收基准

## 本版用户范围决定

2026-11-27，用户提出今天发版。被告知自 v1.21.0 以来 dev 含新增连接器动作（`selection.get`）及连接器运行时改动，
按版本规则须升中版本并提交现场验收与独立复核材料后，用户先要求修复 #365 并合入 #360，再要求“写 Changelog 准备发
v1.22.0”，并在两种范围中明确选择 **“定向验收，E2E 延期”**。本版因此采用 schema 3 `cli-fixes` 定向范围：

- 全量自动回归；
- 多单元器件位号绑定（#365）的现场写入、保存重载与新回读；
- `pcb autoroute --net` 只布指定网络的现场运行与新回读；
- `easyeda selection`（#360）在 PCB 上读取用户真实选区；
- v1.21.0 正式 CLI 的负对照；候选制品身份与运行时等价性；测试对象清理后的回读。

完整设计 E2E 延期至 follow-up。该决定只绑定 v1.22.0；报告里的 `pass` 只表示下列用例通过，
不表示电路、制造、硬件或完整设计验收通过，也不沿用旧版本的现场结论。

## 原始需求与起点

完整设计的默认原始需求仍是 `docs/test-case-esp32-blink.md` 第一节，本轮未执行该流程。现场没有输入预制 BOM、网表或坐标答案；
唯一的器件身份输入是 #365 报告中的器件型号 PI7C9X2G608GPBNJEX（LCSC C5116335），其库身份由 `lib search` 现场解析。

现场起点：一次性测试工程 `ceshi`（`68c21a0bda4a41c2acf02f77dfeda78d`）：

- 原理图页 `LED 指示`（`8fb55bd61d0986a9`）只有图框，无器件、无导线；
- 原理图页 `P1`（`6abf3fbf13dc52f0`）有 U1、U2、C1–C12、R1、R2、Y1 等既有器件，本轮不修改；
- `PCB1`（`f708451540690854`）有 17 个器件（uniqueId gge1–gge17），7 个网络（GND、+3V3、+5V、NRST、OSC_IN、OSC_OUT、BOOT0）
  长度均为 0、无走线。

起点状态来自执行者开测前的读取，未单独留存原始记录；只有 `f1-import-changes.json`（导入前 17 个器件）与 F3 回执（`before.tracks:0`）间接支持，属弱证明。
验收结束时两份文档按目标保存重载后新回读：`LED 指示` 只剩图框，PCB1 17 个器件（无 U90）、0 条走线、7 个网络长度 0（`c2-pcb-reload-readback.log`）。

## 源码与环境

- 发布候选：`51e483c80aa5f695190a45947867ff9294186427`（`origin/dev` `d6efaf0c` 加版本同步与 Changelog）。
- 现场运行时：`scripts/build-local-snapshot.py --version 1.22.0-dev.7` 由 dev `d6efaf0c7013fa95c3ae913f40127458b2b39e07`
  构建，`local-dev.json` 记录 `sourceDirty=false`、`versionInjected=true`；`shasum -c checksums.txt` 全部 OK。
  CLI SHA256 `2c802520ad7e792b40074d32048d354c37e4a1a8e77ad063da742e6d0c8202f7`，连接器 `.eext` SHA256
  `17555a4186dc4903972148159a5621bff23e52e0da63834afa7f0f2e339fce79`。用户在 EdaDesk DEV 中安装该本地包，
  `active.json` 指向同一 CLI SHA256；daemon 与连接器回读版本均为 `1.22.0-dev.7`（`r1-health.json`）。
- `d6efaf0c..51e483c8` 只改版本字符串、Skill 版本元数据与 Changelog；`extension/src`、`internal`、`cmd`、`pkg` 无差异。
  报告把现场身份记为「候选 `51e483c8` / 等价运行时 `1.22.0-dev.7`」。
- 宿主：EdaDesk DEV 中的 EasyEDA Pro 4.1.105；daemon 端点 `127.0.0.1:60833`。现场命令由执行 Agent 用运行时 CLI
  （`--ports 60833-60833`）直接下发，未经内置 AI；所有写入走 Cobra 子命令或 `easyeda call` 的类型化动作，没有 `exec_js`。
- 负对照：EdaDesk DEV runtime 中的 `v1.21.0` CLI，SHA256 `17be2b20019b102142b4d1bdab3f690aa95673e629c4b0e38228ee08b4a2693c`，
  与 v1.21.0 Release 记录（`docs/releases/release-1.21.md`）一致。
- 平台：macOS arm64。

## 通过阈值与不豁免项

- 十个用例（M1、F1、F2、F3、F4、E1、L1、N1、R1 及 not-run 的 E2E）必须按本版定义完成；E2E 明确 not-run。
- 自动回归不得有失败；平台限定跳过如实保留。
- 只有被现场覆盖的行为可以写 pass：`selection` 在原理图页上的读取、读取中切页的 `page drift`、非原理图/PCB 文档的拒绝、
  空选区；多单元器件单元跨页时的拒绝；`autoroute --net` 越界新铜时失败——这些只有自动测试。
- `autoroute --net` 没有 v1.21.0 现场负对照（旧版本会布整板，属不必要的写入），不得写成已有负对照。
