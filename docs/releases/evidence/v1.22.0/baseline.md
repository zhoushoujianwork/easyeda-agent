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

2026-11-28，#368、#370、#371、#372 合入 dev 之后，用户要求“准备发版本”。版本号不变（仍为 v1.22.0），沿用同一定向范围，
并在新的源码候选上补做第二轮验收：

- 全量自动回归重跑；
- 受 #370 影响的 `sch apply`：多单元器件绑定（F1/F2）与写前拒绝（N1）在新候选上现场重跑；
- 新增：playbook 断言预检与运行时路径提示（#370）及其 v1.21.0 负对照；一键布局 `pcb layout simulate --spread`
  （#368、#371、#372）的现场运行；`pcb auto-place --dry-run` 的现场规划；
- `autoroute --net`（F3）、`selection`（F4）与 #365 的 v1.21.0 负对照（E1）沿用第一轮现场结果：自 `d6efaf0c` 以来
  连接器、`cmd`、`pkg` 无改动，`internal` 只改了 `sch apply`、布局模拟与自动布局相关文件（见 R1）。

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

### 第二轮（2026-11-28，发布依据）

- 源码候选：`8594aa0eb0963717fe9f75a83cecc56e50efba37`（`origin/dev`，含 #368–#372）。发布 PR 的 head 为该修订加本证据与
  Changelog 文本（仅 `docs/releases/evidence/v1.22.0/` 与 `extension/CHANGELOG.md`）。
- 现场运行时：`scripts/build-local-snapshot.py --version 1.22.0-dev.10 --revision 8594aa0e…` 构建，`local-dev.json` 记录
  `sourceDirty=false`、`versionInjected=true`；`shasum -c checksums.txt` 11 项全部 OK。arm64 CLI SHA256
  `53049dc2d66a86b3086e9ab645a908b42c3b2da39ecde20aa02c6ecbac5f0af0`，连接器 `.eext` SHA256
  `be9033754ca2da7673caa6f809a59fb3d131b2915642c6c0c316ff505d7006a7`，`skills.tar.gz`
  `54f882974a4efe22a591d4a3b9a519d453af6d7b9706eea633684aca54a69e3c`。EdaDesk DEV 经调试通道校验该本地包（bundleSha
  `23de4cde…ff05`），用户在原生确认中批准安装；`active.json` 指向同一 CLI SHA256，daemon（PID 25074）与连接器回读
  `1.22.0-dev.10`（`r1-identity.log`）。
- 宿主：EdaDesk DEV（Desk `d9d70b97`，0.2.3）中的 EasyEDA Pro 4.1.105，窗口 `1ad62c3a…`，daemon 端点 `127.0.0.1:60833`。
  现场命令由执行 Agent 用运行时 CLI（`--ports 60833-60833`）直接下发；一键布局由 Desk 调试通道 `preview:layout` 与
  `layout:simulate` 触发（与页面按钮同一入口）。所有写入走 Cobra 子命令或 `easyeda call` 的类型化动作，没有 `exec_js`。
- 负对照：EdaDesk DEV runtime 中的 `v1.21.0` CLI，SHA256 `17be2b20…`，与 v1.21.0 Release 记录一致。
- 起点：测试前读取（`s0-start.log`）——`LED 指示` 只有图框，PCB1 17 个器件、7 个网络、长度均为 0；PCB1 语义哈希
  `a84bfd08030021e310254ead4331b82ca4a93b3ca063d59d67b00ee6776d88af`（`s1-preview-layout.json`）。这是 11-27 一键布局现场验证应用 17 项移动后的板子，与第一轮结束时的位姿不同；该次应用记录在
  EdaDesk 验证记录中，本证据目录未留存其日志。
- 平台：macOS arm64。

### 第一轮（2026-11-27）

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

- 十四个用例（M1、F1、F2、F3、F4、E1、E2、L1、N1、P1、S1、A1、R1 及 not-run 的 E2E）必须按本版定义完成；E2E 明确 not-run。
- 自动回归不得有失败；平台限定跳过如实保留。
- 只有被现场覆盖的行为可以写 pass：`selection` 在原理图页上的读取、读取中切页的 `page drift`、非原理图/PCB 文档的拒绝、
  空选区；多单元器件单元跨页时的拒绝；`autoroute --net` 越界新铜时失败；射频规则（ceshi 无天线）；导出顺序无关（无法在编辑器里重排 dump 顺序）；
  `auto-place` 的现场写入——这些只有自动测试或现场规划。
- `autoroute --net` 没有 v1.21.0 现场负对照（旧版本会布整板，属不必要的写入），不得写成已有负对照。
