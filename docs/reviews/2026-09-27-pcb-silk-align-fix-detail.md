# PCB 位号对齐：实现与证据

主结论见[用户记录](2026-09-27-pcb-silk-align-fix.md)。这里保留实现和验证边界。

## 原失败与独立红灯复核

运行态 CLI/daemon/connector `1.7.1-dev.13`、Web EasyEDA Pro 4.1.60，专用测试 PCB
51 唯一器件、203 焊盘、33 网、四层 SIGNAL 中间态；未布信号线。064 align 返回 aligned51、
unresolved0、skipped0、51项clean:true。065 saved:true → 066真实reload → 067完整dump →
068原生归档 → 069 typed预览后，J1/C14相交36.1×8.2925mil，R3/Q1相交8.843×37.2335mil，
U3/L1相交4.4015×13.7915mil；J2文字落入自身完整bbox。SDK仍166 Connection Error，未宣称DRC clean。

align前后全部51组件record含pads/bbox完全相同，51文字仅位置/bbox六字段共306差分；
原生PCB仅51 ATTR和1 DOCHEAD变化，其余section原始行相同。实际中心与旧计划只有打印舍入差异，
不归因为SDK坐标漂移；也不把本次component bbox观测推成所有宿主的纯物理body语义。

冻结现场591文件清单 SHA256：
`8aaae9369af892a4bec1eec7015a60aa497818e339019baa1e5f75d7ef86a1dc`。
独立红灯目录 `artifacts/release-v1.8.0-silk-align-red-review-20260927/`，57文件清单
`8ce8f457bc47ef9d1676c4787ad2b385f298a8ddf8005e0b2cd6f0cad957bc68`，报告
`9fc48a6bf2209ece1d0ebec5fb1d8dd3055376ef7709a198719ad3a39e58deff`。
16663有效断言、594输入最终哈希复验，零现场调用；复核自身四次监督错误已保留。

原scoreSlot真实几何子集反例：label penalty10000、rank0、reward−25得到9975，旧clean和early-stop
阈值均为cost<10000。rank1对照为10000。自身完整BODY的孤立反例还显示owner分支被忽略。
这些反例不包含完整旧pad bbox库存，不宣称全SDK handler/search复放或唯一根因。

## 修复合同与候选

公开Skill先更新，typed action/Cobra签名保持现有offset、spacing、side、refs。新增结果包括
verified、partial、normalization、appliedIds、fresh verification bbox/conflicts及geometryScope。

- 同侧完整rendered component envelope含自身、measured pad、其他/frozen文字和机械区域是硬事实；
  偏好及净空奖励只排序无碰撞候选。内层铜限制不直接作为外层文字障碍。
- 不用pad union代替完整bbox、不猜±15mil pad尺寸；缺测、隐藏/歧义/缺少目标或无板框范围明确失败。
- 非零角度、层/镜像/reverse归一单独记录并fresh测量；不盲用旧旋转bbox，不删字段重试。
- 所有位置候选先计划，任一unresolved不应用位置子集；姿态归一可能已发生，partial据此保留。
- 写入后重读全部目标anchor/pose/bbox与障碍，实际碰撞决定verified；attempted ID即使SDK抛错或空返回也保留。
- CLI/raw dispatch、structured request/capture与Apply失败传播；verify/retry/continue不能掩盖partial。

本期板边检查明确为outline bbox containment，不认证任意弧形/凹板完整轮廓；搜索失败不证明数学无解。
BOTTOM镜像字段回读只验证请求兑现，不扩张为制造镜像语义认证。

冻结候选15源文件清单：
`f1cdd26f50a2730599897bc6224cdac71f9bee49bcccc99c77b5ef1cfed6c000`，base `82e1f64`，
`artifacts/release-v1.8.0-silk-fix-candidate-20260927/`。初候选独立复核已拒绝：六个实际handler反例显示
未知数组库存被当为空、未知mirror被转换false、写后组件清单未fresh，均有一次位置写及假verified。
46件独立清单 `65f7350ba6f1875e0f54844e2e5914b445ed57406438705493871913c141dd3b`，
报告 `fdf7db7ed6cfb8c1f1c6c76b426d0d99ff1b1cdc5d4f9418a74908b16dd91c76`；原候选未采用。

v2拒绝非数组库存，getter必须明确number/boolean，每轮fresh组件身份/侧别库存，fresh属性父属/值对应，
15项connector定向通过。v2冻结15源文件清单
`47b1c121baae65ce4ac4d5fbd235021309b2fb0ee468894be9d3d375a7408f42`，
`artifacts/release-v1.8.0-silk-fix-candidate-v2-20260927/`。独立复核和现场仍待完成。
v2随后也被拒绝：65项独立测试52绿/13红，发现错误refs扩张目标、身份未确认即归一、
写后显隐漏读、板框层coerce和区域rules未知成员降级。54件清单
`e778ccd50521c355a91df4983ac737113279b9af9d100d254afbc1f1b38c709c`，报告
`03547c033b511aebc3d2fec5ff4aef8a93042c44234e1847b54536786e569a37`。v2本地包仅归档，不安装。

v3写前验证refs/options及完整属性身份/显隐，fresh重查selected显隐；strict板框层与规则成员拒绝未知。
冻结15源文件清单 `18ae5c0d756eb4ca5040a3aebff70daeeb743f4557cc0f6e4a088954b97d4df3`，
`artifacts/release-v1.8.0-silk-fix-candidate-v3-20260927/`。19项connector定向、657项全量通过；
同Go源码全量4167通过/1跳过，typecheck及agent-check通过。v3独立复核、提交后包及现场仍待完成。

v3独立72测试69绿/3红，剩同一身份问题：初始正确、归一前fresh ID/parent/value漂移时先写再拒绝。
45件清单 `fb448a06fc10523e90948a94c819549bfacd079a1b524364d721aeaabc135337`，
报告 `93ffa810bc45156f1e503078cb7cc847f9bd84c52d473b96469e8f740656198d`，v3包也只归档不安装。

v4统一身份/显隐/可读性检查，initial、归一前fresh、每项位置写前fresh和measure共同使用。
15候选源文件清单 `85a51cda43fbc921d9d30c8ded93022477f2894d5b82925e33a5b10bc2889d26`。
20项connector定向、658项全量、同Go源码4167通过/1跳过，本地typecheck/build/Skill检查通过。
独立87TS+85Go共172测试全绿，55文件清单
`5e63566ff1e553e9f23235f29d64081f43ddf05f014b604784f9f61f8df0a639`，报告
`dad018a0e125a6e2910482c737f5ef45a6de928c45dd47d5fe7685dab205324f`，输入
`6fac01864586aecfd2d1ff13a506cb30a839a9e680a3f4c16506149e18162f75`。
末次15文件与规范源哈希一致；只签有限离线候选，后续采用与现场结果见下文。

本地打包曾误用正式release-build带dev版本，被版本格式检查拒绝；改local-build后因changelog标题缺方括号
再次被检查拒绝。保留两次日志，修正标题及目标后再构建开发包；未创建tag/Release，不放宽发布检查。
离线例覆盖旧三对/J2有限几何、负奖励、自身body、上下侧/通孔、缺测、无位、目标缺失、
返回空/anchor失配、实际bbox碰撞、旋转fresh测量、镜像不兑现和跨入口停止依赖。

## 提交后开发包与运行态升级

修复提交 `83a98b287accf4f2c00d936063fb1f396ea27e30` 已推送 dev。最终包11文件清单
`349d7c36fb46ba74b24993f88c867de4730ccb4e36983618e347e75ccf128318`；darwin-arm64 CLI
`8ee05e310b82ceb57453e91099c50fa7cd81fb6f9ba69495685571fea2285bf3`，bundle
`cb9090857e9f6cfd6ff6864892b23d64a4780869237d2b24127e730ef1443355`。
独立采用581项核验通过、346份输入哈希复验，15源文件与v4逐字相同，五平台VCS绑定clean提交，
201个公开Skill文件对应同源。提交前后Go二进制非逐字等同；采用依据来源与绑定构建，不宣称机器码等价。
120文件独立清单 `385f30bceca8bfed0c8718e185bec2a46e40e55a60e4894cce925ac1a59913b2`，
报告 `959c7be7d84db5ee43e78095f17345af38aa546ca6fb559f70a4eaad4b8258ee`。

逐页升级前saved:true后，安装本地CLI/Skill，仅更新已安装同UUID连接器并保持原权限；
新注册窗口 `0d63a71d-f32b-489a-8984-09ca1da2646c` 的CLI/daemon/connector为dev.14、Web4.1.60，
目标PCB身份一致，local check为READY。root对完整三页与51组件/203焊盘、full board、native对账：
三页非属性全部相同，属性按parent/key核对仅623/388/258个primitiveId重铸；PCB组件result逐字同，
board仅capturedAt差异；122原生section非DOCHEAD行逐字同，122个header client token及section顺序变化保留，
其他7个ZIP条目逐字相同。未执行设计几何修复，旧位号相交仍是现场复测的开始状态。

升级冻结262文件清单 `fdafb56b77686bf41d24317ba1c1f3dbb32c392418fed7a83579102ef32bbbd7`，
目录 `artifacts/release-v1.8.0-dev14-upgrade-20260927/`，active daemon日志不入冻结；独立升级复核已完成。
成功安装后的文字stdout误按JSON解析、首次新注册context尚空、离线字段大小写期望错误均保留；
没有重复安装、额外刷新或设计写入重放。根已将窗口独占交回原执行员，从fresh状态验证位号修复。

独立升级4,886项有效断言及594输入最终哈希复验通过，137条审计原字节闭合；
38文件清单 `90f9aef1f8b2d4249799b2e7b99fe101fb32f708831c6fa25efcdcf82ae010b9`，
报告 `456db6c75b79ee1ec0efde96c0a8ac53d64db6635c7f6ba1be54e2cfb9825ed9`。
根重算全部38文件大小和SHA一致；只签升级保全，不补签位号现场或整板设计。

## 实际 51 位号有限复测

fresh全部51目标按offset15/spacing1.5重算，006返回verified:true/partial:false，随后
save→真实reload→完整51组件/203pad→full board→native→typed整板预览。全部组件原始记录、
所有实际pad、其他board几何/规则/铜与非位号属性保持。旧三对文字相交与J2自身遮挡消失；
最终实测文字至完整组件bbox最小16.038mil、至pad 27.998mil、文字之间20.482mil。

严格区分51个检查目标、50个位号几何变化（U4未变）、51 ATTR ticket+DOCHEAD共52条native raw变化。
原报告简写“51个位号x/y改变”不作精确计数依据。16个native/getter末位差≤2.274e−13mil，
U1.x/D3.y较写后observation在持久化后约+0.00005mil，均保留并以最终fresh几何判据复核，
不称计划/原包全部相等。native缩略图变化及restoreVerified:false保留；所有非PCB原始section相同。
84条audit、3次saved:true、单次align设计写入及reload临时同项目P3 current后回PCB已绑定，
SDK DRC passed:false单列，不据silk通过补签DRC。

37文件现场清单 `e3f17751fa663e9f16599c5425b65c2fce1678c73d51d93aa9677c96ab7ce7c5`；
独立15,868项有效核验、45输入哈希复验通过，15文件清单
`755280cc3d3276a169dc127eecb2d9005e6a02bee85b8ec0a309a609eeebb04b`，报告
`324e0d30f81e0b53ce98abcb4d82388f86a2f921c1f871758de044bba580f1f1`。
根重算全部37现场件和15独立件匹配；#271以`83a98b2`采用提交及该有限现场结论关闭，
[关闭说明](https://github.com/zhoushoujianwork/easyeda-agent/issues/271#issuecomment-5851596372)。
不签BOTTOM制造镜像、任意凹/弧板、14标记、完整Layout/route/最终PLANE/DRC/E2E。

## 点灯模块 L1 有限结果

本次D4核心+R7专属外围无铜整体移动(−100,+100)mil，再从fresh数据重算恢复。
各4/4 guarded步骤、save→reload→fresh/native完成；49范围外组件、pads/net/layer、板框、
四M3孔、规则、铜及三页/非PCB原生数据保留。最终typed board仅capturedAt变化；原生13条ticket/DOCHEAD
和PCB缩略图差分保留，restoreVerified:false原字段不抹除，不称原归档全等。

54原件清单 `a97d37007d5bb9d7d06938aae735d06a7327ad7311cf14d765e67f494bf1f890`；
28依赖补充 `32b08cc8f44949e0c0a33218279b215cd4ccbf36234cb09b63c32d727b6f527e`；
3版本补充 `625dc9ccc8ce4b3a17ca35e5b9dd83b4f13a74092ffed0c1cfbb0245747d4365`。
独立34件清单 `223ce4dd9caba3a1138daf771840cb9f3ad0bab9ddedcfced5ea6d981c0b2d1e`、
报告 `984369db7c96712296cdfa67d36e00963f1fb7cd1be046ff5d0881822ab442d1`，987有效断言通过。
仅签该两件无铜移动/恢复，不签带铜移动、其他模块或完整E2E。
