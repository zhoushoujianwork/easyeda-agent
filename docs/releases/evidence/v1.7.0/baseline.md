# v1.7.0 基础 CLI 发布基准

## 范围决定

2026-09-25 用户明确“发布新版本，然后高级 CLI 下个版本再验证”；此前已明确“团队先不管”。
本次验收范围为 basic-cli：B00–B10，个人空间及已有自定义规则配置。团队创建、系统预设首次
转自定义规则、高级 CLI A00–A06 均不在本轮通过结论内。三个已知 bug #256/#257/#258 保留跟踪。
本次不是客户需求到成品的设计 E2E，不使用预制答案冒充需求输入。
下一版本高级流程只接收 esp32MiniRequire.md 第一节原始需求，按 A00–A06 执行。

## 运行时与源码对应

现场运行 CLI/daemon/connector 1.6.0-dev.21，Web EasyEDA Pro 4.1.60；主执行员串行 typed CLI，
独立 offline_review 只读冻结证据。实现归档于 Git 1414784，测试前后运行包的 SHA-256 一致：

- CLI：6a26c240dcb6838843836e313ea0dd747c0dfd43ddf548d685628f1cdf0251c1
- connector .eext：e8fd7e6758211409e628f988c24b67cb18ea66902c6fdc21141a52f37f7a4880
- connector JS：511add6882444ec7a80c5369fc9f59c8792de61bbd8c1c37f3632ec96e673193

v1.7.0 是上述候选的正式版本准备；产品 Go/TypeScript 源码未改，版本标识、发布工具和文档
另行核对。不能把 dev.21 的现场时间和回包改写成 v1.7.0 现场重跑；正式资产有独立构建与 smoke，
对应结果见 test-report.md。原始现场数据留在本地忽略目录，不随发布上传私有工程原件。

## 输入和开始状态

- 实际用例规范：Git 1414784 的 docs/cli-live-test-detail.md；11 份逐项报告保存在同提交 docs/reviews/。
- 原理图：新个人测试工程 ceshi-cli-basic-dev21-20260925，创建空页后使用官方 C25744 器件、实测 pin。
- PCB：已核对的空白专用板，已有自定义配置11；量纲 mil，实际 footprint/pad/net 读回后作为单动作输入。
- 每次显式 project/doc，变更前后冻结完整快照；saved:true 后 typed doc reload，再 fresh 读取。
- 不使用 GUI 修改工程；临时对象和页 typed 精确清理。无 typed 项目删除，因此保留测试容器和默认页。

## 判据

命令/路由/目标身份准确；对象只按输入增删改；保存重载后语义一致；宿主属性重铸 ID 必须提供
完整父对象/键值/位置/可见性映射及电气/渲染证据。错误、过期 ID、缺连接器必须明确拒绝，
原始 JSON 保留，失败退出非零。范围外对象及原工程恢复须有完整回读。独立复核不能只读执行员摘要。

## 冻结证据身份

原始目录 artifacts/cli-basic-dev21-20260925/。下面分别为每项 evidence-sha256.json 和
对应已提交单项报告的 SHA-256；252 份冻结文件逐项核对通过。B02 的持续 daemon 日志不算冻结件，
其启动日志与命令快照已单独冻结。

| ID | 证据清单 SHA-256 | 单项报告 SHA-256 |
|---|---|---|
| B00 | 25f14984e22c1f16fd17eaa8c7e86fad95b35dc79b86dbac69223965668147f8 | d2eaa936c85050ae513ace9ddce3ab15420832448c4914d8c4c9e0df49758ada |
| B01 | 05c337e591c16bea051a66fa37a791b1455295a7e581e3a68243ccc79799d7b2 | e6b8e7235ed9bab23a8e73fd6ff12c1630402188b05eee50f9a7082c489ffc19 |
| B02 | 15653ddda2992afd2f0238139f9b9b38272a6f3b5429eccb0dcf1ca3dcc27441 | 903dddbbccd4f8392b5d3d6bc20cc57f17f6c3ff880c00305a60c46091da3450 |
| B03 | d193cf85444be478cac88bb28e66d4bc9f0ea519413e8217c7ec7317b286a4d2 | 58cc643c5173af40584f35a48fc431252855484d95756d2c2bfcd7fda35bcb38 |
| B04 | 0559a79c299bffcf2c961579d4b885605b435e30b6df116a2b6e353f5bc1b44b | 58aa5277663c5c1aae6a778e196a0c9789e2810d2283574043a916893b0d9a30 |
| B05 | 9c06845ecf1da892bb918ac73d0addff59cc76884cb9c260d6c511076243d7a7 | e19e3155eb85d1531797738716bbd12352c8b570ca708f10b6c02d03600d8292 |
| B06 | d9f9378632655cc02e41e42dc6cb22762c6af3e6d270fa688a2e79f951dc0a51 | 520cdd85d63f40f2982be45594d7ad2da12d127f5a3c715a9f81ed37a08913c1 |
| B07 | 64ac3c3db9a0b44fb4232f92c6e7dfd62211a9663ebf81160f64f5ffa267eddd | eb8e64fb9dbc67bbff8d578ad84237da8e21f3eec484b57bb48adb13beb930c5 |
| B08 | 4ef85bb25f53e2c178135a3b6a81d04848a2164c73d99ac652d96a59abeee68d | 8999e27a29bf5e5c9b26980c1e497e426856a9f6ba2a059da9adc92671ba629d |
| B09 | 1d41e2ba151295442306cb173e140857e08c040c42fbd73ba6971ccbddb78be7 | 4516ca729de112209ea4ac3bcd100e85c4c6cd5547599a4edb98321b46f5dda9 |
| B10 | bb628697f8556e0317a443b491f2cf814a4ff015fa72b786ca30f31523f1e55d | 285bdaef08853b57252d1ea96afc4e4a78fd1202079db78fab53a46a85b8b8fb |
