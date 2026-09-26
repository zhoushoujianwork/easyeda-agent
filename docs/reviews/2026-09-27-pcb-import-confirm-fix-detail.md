# PCB 导入失败、清理及候选证据

[简版结论](2026-09-27-pcb-import-confirm-fix.md)。现场均为 Web 4.1.60、connector 1.7.1-dev.11、
CLI/daemon v1.7.0-45-g2c6cd35；新 dev.12 只做离线验证，不能覆盖这些旧回包。

## 原始失败

源三页 29/16/6 共 51 个 part，uniqueId 无重复；目标 PCB
`3037fc1dfa4965d2` 原为空。一次 `pcb.import_changes` req_1253 后，回包
imported:true、confirm:applied、componentsBefore=0、componentsAfter=101；属性回填自动处理 101 件。
完整即时/持久化数据证明 101 个不同 PID，50 个位号及 uniqueId 成对，仅 U6/gge10 单件。
原生 COMPONENT=101、ATTR=307、PAD_NET=416。源三页整个 result 及所有非 PCB 原生 section 相同。
额外 `60ed54656218bed8` 只含 DOCHEAD/DELETE_DOC/META，没有器件。

失败目录 `artifacts/release-v1.8.0-e2e-20260927/a02-pcb-layout-20260927`，53 文件 manifest
SHA-256 `09f176be28b6144a3ec4eb949e2ef93bf72e924a79d615d06bdabffd7ada3329`；根任务全部重算匹配。
独立离线现场证据 446 项通过，107 条 audit 只有一次 typed import 和一次属性回填，
不能用 audit 次数推算内部 DOM 点击次数。

## 代码红例与修复

旧 clickImportConfirm 首次 step() 点击后，又在关闭轮询中调用含 btn.click 的 step()。
独立隔离 VM/DOM/时钟测试中，750ms 延迟关闭得到 4 次 click（0/0/250/500ms）；
永不关闭得到 41 次 click，并在 10s 后返回 applied。立即关闭与无弹框/按钮对照也保留。
初版独立报告 SHA-256 `20d6896c8ce37b6e9bc332c6beb1a3287018f900ec4a34b707bffb25a5fe272b`，
manifest `2fc0d2d9d4f146dbce5a3537d9aec80774d842e13a08a07253d486796d4eb81e`。
该复现证明代码缺陷，不证明实际 0→101 的唯一因果链。

候选将 apply 和 observe 探测分开，关闭超时为 dialog-open；回读非空 uniqueId 分组，
重复返回全部 PID，读失败保留 unknown。未验证时 outer ok 保留已写入部分以供保存，
result.partial/verified:false 明确业务失败，飞线计算、自动属性回填与位号修复停止。
CLI 命令、raw dispatch、request 和 Apply 直连/子命令传播专用错误，
在通用 verify/retry/continue/prompt 之前停止，不隐藏原回包或重试导入。

初稿测试曾引用不存在的 LookupAction，随后 Apply 直连负例揭示遗漏的失败传播；
修正后定向、全量 Go 与连接器检查通过。Go 4108 pass/0 fail/1 skip，连接器 638 pass/0 fail；
测试日志及包核验在 `artifacts/release-v1.8.0-pcb-import-fix-20260927`。
actions.ts 候选 SHA-256 `64d6d75e4abcfa18fc6b3a58e26277d7d04f0c81da3dd1292bb8c87ab0c99ef7`。
独立离线复核通过，0 阻断；新增 VM/DOM/时钟场景、12 项 TS 定向测试及 80 项 Go 定向检查均通过，
其中 Go 新增 33 项，其余覆盖已有失败传播。五份生产代码的复核前后哈希一致。
独立测试自身的边界提取和同步 fake timer 错误保留并修正，不计产品缺陷。
报告 SHA-256 `1a7dad21ec2a41ae31b24af457a74af9b51e8aea210a30a4a18c7f208470f2bf`，
输入 `6d93036e5c837e312fcc784916d70dec2dd5ef94b021514897915c0873c9a65f`，
40 文件清单 `959a661c1fff478fe1a84a6264a4001f82d4014846d7b65f504530ef5d2aab54`。
代码已提交并推送 `698980e`；运行态升级已完成，导入现场复验仍待完成，#269 不关闭。

## 精确清理与原生边界

另批 fresh 核对失败批全部 101 PID 后一次精确删除；没有 clear、手挑副本、重复 import 或 GUI。
原始守卫发现 netRules 的导入默认映射而写前拒绝，保留失败；v2 只接受已核 33 个源网名的精确默认映射，
未知规则/范围外变化负例仍拒绝。显式 save req_1395、真实 reload 内 save req_1400 均 saved:true。
晚期后台 req_1460 saved:false 单列在 #268，不计保存成功。

最终 0 components/0 copper/0 region，typed config 主体/层原值、netRules=[]，语义
SHA-256 `41b23a1b3e7c54f623efa678dea430033717968444fefee90e1ccbfebce0d527`。
三页整个 result、其他原生 sections 与其他 7 个 ZIP 条目原字节相同。
原生 PCB 从 184 增至 648 条，新增 464 条空 payload NET/PAD_NET/RULE_SELECTOR 历史记录，
最终 39/416/44 条此类记录全部空 payload；旧空网/GND 两条及两条 selector 票号也变化。
导入完成态的默认值不等于清理后状态；不能称原生逐字恢复或重新导入恢复已验证。

清理目录 `artifacts/release-v1.8.0-e2e-20260927/a02-import-cleanup-20260927`，46 文件 manifest
SHA-256 `63f3e0365f672b71e4dd5c3b9937d0b188ac9c2e753e4caeacc81efca937dba1`，根任务全部重算匹配。
最后显式调用结束于 2026-09-26T21:54:33.989776Z；后台 req_1460 于 21:54:35.998273Z 返回 saved:false。
完整 audit 为 138 条，成本区间为 137 次（不含该更晚后台请求）；Go 修改在释放窗口后才进行。
独立清理复核 680 项断言通过，只签精确几何清理及范围外保护，不签原生逐字恢复。
报告 SHA-256 `5e484779804d352db23dae2b747bc7cc0e7ad6c793f8a09a1e8cc17ddbb7b24a`，
输入 `c9a8d9dd491cc459622b136cf8e506dc3adb23fdef2f027b74d49906a755483b`，
清单 `4b949c6fa4ca62b46bdfc05357cc46f54f883b4a1eda4b8f4c0a90b20fbdbf0b`。

## R1 缺口

126 文件受控中断批 manifest `f3dc0396d1a045ef37a5bd8d1ca587aee4f33d6b2fc97fa5c9520f6fb2a3c290`。
独立 236 原始证据断言和 15 次隔离重算检查通过；实际生成脚本各重算两次，源与队列原字节一致。
仅 ready/recovery-parameters 的 at 字段按明示采集时间处理。
受控 SIGINT、A-only fresh、同源 B-only 恢复和最终 cleanup 保存重载分别通过。
恢复完成的 A+B 在清理之前未真实 reload，因此整项为 partial / recovery-reload-not-run。
最终空板 reload 不反向证明中间恢复态持久化；旧清单不改。

独立报告 SHA-256 `b282cbc69a57fc77492a8f2445c9d0c38aa12a6c724587079a10d1bb69e752ca`，
输入 `b0f1ed3aed32aff2d2bd8b0a1cad2c11e159734c1ca6fb3d39152c008f7c4bb7`，
清单 `3cca01c90ba9d581bfcb4b092b7c99570890a53eb29f56901cd42d43b4cdc304`。
新 runtime 上另跑：恢复 A+B 后先 save→reload→fresh/native 证明两件准确存在，再精确清理和再次保存重载。

## dev.12 运行态升级

完整本地包构建后，最终 `.eext` SHA-256
`a836acc5d7990073bb67966deb658b80bd06a5a594e2243255be94da7098eab3`；ZIP时间戳导致与第一包哈希不同，
其可执行 bundle 同为 `002b24f969f22092c0a5b2265ed219bceff1ace3ea2539cd321af572ff4661e1`。
CLI包、bin和PATH均为 `fa12e2dd8295953f2b594536b4927ce63e46b811db945369a3f9f856fe15ae6c`。

四页 typed 保存均 saved:true；旧 CLI/daemon 为 git-describe dev 戳，与旧 connector dev.11 的
精确版本不同，差异保留。只读核对已安装 UUID、原权限、旧 bundle 与独立 dev.11 包哈希一致后，
通过仓库已提交的专用热更新脚本原子替换同 UUID 两个扩展记录，保持权限并安排网页重载。
新注册 `51b3eeb2-6cc7-457a-b3c5-02a454da312f`、目标工程/PCB、connector dev.12 与本地 package check READY 对齐；
daemon PID 14017，CLI/daemon 精确版本 v1.7.1-dev.12。没有 GUI 工程写入或任意 JS 设计动作。

重载后三页全部非属性 result 原值一致。global 属性按 ParentPrimitiveId+Key 配对，字段完全相同，
仅 623/388/258 个 runtime ID 重铸及数组排序变化；全部原始差分保留，不称 raw 全等。
原生 122 个 section 的 DOCHEAD 与顺序变化，但除 DOCHEAD 外逐行原字节相同，其他 7 个 ZIP 条目全等。
PCB fresh dump 与 clean 基线除 capturedAt 全等，仍 648 条、无几何；partial 仍列空板无元件/无板框。

190 文件冻结清单 SHA-256 `1f4536894405594015de50898a1e461e06c5325a279e186afc9e47a9c002c97d`，
目录 `artifacts/release-v1.8.0-dev12-upgrade-20260927`；活动 daemon log 排除，启动 log 的冻结副本保留。
核验辅助脚本的 dump 文本解析、空板 partial 和 120/122 section 数量三次监督断言错误保留，
不作为产品失败。独立升级复核 4468 项证据断言通过，仅签升级/fresh。报告 SHA-256
`ec39ce6bfd8a877d2f2850d5acc309eec6f06c8bc7c666bf7816f8f680ee9e78`，
inputs-v3 `abfbabcd88774886bdfae363296d2041adafd6bdbb21044759cab9923f025d73`，
checklist-v3 `1f778aa001c6b3524e5a2ba2c8c7a9550c26c9d5dc03a296ca5f0de9c0c76e3b`，
21文件清单 `e5b50afbad0231f6d9cc6459e571ffae001312a639d4bfc2d8f182349511a8c6`。
122个DOCHEAD差异为client122、ticket61、user59，116个section换序；这些原始差分保留。
不据升级结论签导入或 E2E。

## 新 dev.12 R1 与单次导入

新 R1 136 件清单 `62e7ba7d751af19c4b7f077703e8e9afb6324126b9f6f1f3aec641c63f258efd`，
根任务逐件大小/hash 匹配。048a–e 的 A+B 显式保存→真实 reload→完整 fresh/native，确在 050/051 清理之前。
实际 SIGINT/exit130、A-only fresh、同源 B-only 重算、完成态持久化、精确清理及第二次持久化闭合。
8 次保存均 saved:true；三页本批前后 whole result 全等，PCB 几何恢复本批起点，原生仍 648 条。
仅签两个临时非铜 region 场景；#260 默认实际 0.2mil 及 restoreVerified=false 保留。

独立 3770 项证据断言通过，报告 `cafcd2ca95c0e871d62b16068daed1294570709ca668cd55a89e2879709cf33d`，
输入 `416f483cc54115170a43a4b158a891fe73436d6545975cc0b4033211ba9adadc`，
48 件清单 `478083176bff4609106e3086811ea66d087805168b509a201b67f5abe5ce41ef`。
目录 `artifacts/release-v1.8.0-e2e-20260927/r1-dev12-independent-review`；旧 126 partial、80 监督失败不改。
根任务首轮匹配新独立清单误把仓库相对路径再次拼接目录，修正路径解析后48件匹配；非产品错误。

新导入 46 件清单 `f0b0a4e721d9bfb52e6ed0cf95a3a83742ae325cbaaa653e1016f9f7cf5bcacf`，
目录 `artifacts/release-v1.8.0-e2e-20260927/a02-dev12-import-20260927`。仅一次 typed import req_425：
confirmationClickCount=1、components 0→51、identityInventoryAvailable=true、duplicateUniqueIds=[]、verified=true。
51 个唯一位号/uniqueId/PID，全部 195 源脚（158 网络/37NC）与 203 实际 pad 双向对账；
额外8个来自U1.39共有9个GND散热分块焊盘，每个net/native均核对。
33 网与源一致；原生当前活跃 owner PAD_NET=203，历史416空记录另列，不能将总619当当前pad。
012显式saved:true→013真实reload→014/015/016/017fresh/native闭合，三页whole result不变。
仍2层、无板框/铜，独立导入复核待完成，不签四层/布局/E2E。
