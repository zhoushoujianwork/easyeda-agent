# #273 电源布局：展开通道与网络标签的离线复测

## 结论与范围

[#273](https://github.com/zhoushoujianwork/easyeda-agent/issues/273) 的 20 件降压、13 件升压
输入已通过两种新的离线布局模式。用户在提供 issue 后明确表示不再要求器件间画完整
实体导线，可以使用网络标签，因此本轮最终交付优先使用 `--net-labels`：各器件独立
标注网络，再按完整包络分行排布。它明显减少长导线和图幅，保留原网表、器件、实测
姿态、引脚、NC 及核心/外围归属。

这项用户选择替代本次绘图的 direct/attachment 实体路径要求，不是旧默认搜索算法已
恢复成功。默认 `lib-layout` 仍重现候选耗尽；issue 原文禁止标签替代的历史验收标准
没有被补签。新模式必须显式选择，报告与结果记录 `net-labels`，源 JSON 不修改。
本轮没有执行 EDA Apply、宿主网络标记回读、保存重载或电气设计审查。

## 输入与对照

输入取自用户提供的提交
[`a9a06ed511873683d3e30f63d95b3098fafa5db8`](https://github.com/zhoushoujianwork/easyeda-agent/tree/a9a06ed511873683d3e30f63d95b3098fafa5db8/docs/reviews/fixtures/2026-09-29-power-layout)，
三份文件原样纳入[回归 fixture](fixtures/2026-09-29-power-layout/README.md)，SHA-256
与该 README 一致。两份输入均保留 `maxCandidates:3000` 和原 1170×825 raw 纸张。
`layout-plan --lib` 复用 canonical/测量校验与适配器，仅输出多区局部几何，不做纸张
装箱，也不伪造更大的实测纸张；原 `lib-layout` 输出契约保持不变。

| 输入 / 模式 | 本机 CLI 墙钟 | 结果与计算消耗 | 内容包络（raw） |
|---|---:|---|---:|
| 降压 / 默认 lib-layout | 3.707 s | 3000 候选耗尽，20 回退、3 迁移，末次 R6 | 无完整结果 |
| 升压 / 默认 lib-layout | 0.212 s | 3000 候选耗尽，12 回退 | 无完整结果 |
| 降压 / --lib --unbounded | 0.533 s | 完整实体线树，77 次构造消耗 | 2728×1141 |
| 升压 / --lib --unbounded | 0.025 s | 完整实体线树，50 次构造消耗 | 1847×752 |
| 降压 / --lib --net-labels | 0.041 s | 20 件、47 个标记，933 次放置/命名消耗 | 772.9×769 |
| 升压 / --lib --net-labels | 0.024 s | 13 件、31 个标记，337 次放置/命名消耗 | 683×526.3 |

墙钟含本地进程启动和读写 JSON，不含编译；为单次观察，不是稳定性能分布。
不同模式的消耗单位不同，不能将数值当成相同搜索工作的加速比。每个网络标记均带
真实短引线；几何 JSON 中 wires 数量不包含由 flags 声明、转换时生成的标记引线。
包络不含框标题，不能由宽高直接认定原纸张可放下。展开通道结果在原纸张组合校验中
仍拒绝越界；无纸张局部预览完整通过，没有关闭短接/位号/引脚出口等检查。

## 使用与验证

```sh
easyeda sch layout-plan --lib --net-labels \
  --from docs/reviews/fixtures/2026-09-29-power-layout/input.json \
  --out /tmp/buck-labels.json --report /tmp/buck-labels-report.json
easyeda sch layout-render --from /tmp/buck-labels.json --out /tmp/buck-labels.svg
```

升压使用同目录 `boost-input.json`；保留实体路径的展开方案将 `--net-labels` 换为
`--unbounded`。两个标志互斥。普通单区/多区源也可设置 `layoutMode:"net-labels"`，
不自动改变默认算法。标签模式不与旋转优化混用，密集引脚命名仍有有限预算。

本地完整输入、命令输出、报告与 SVG/PNG 位于忽略目录 `artifacts/issue-273/`。
已目视检查两份标签预览：核心、外围和网络名完整，无明显遮挡；使用简化符号，不是
官方符号图或仿真结果，源中的属性占位表达式保留原状。

定向回归覆盖：原命令失败复现；新模式完整输出和原源哈希；器件/引脚/位号几何的
单次刚体变换保真；所有命名线岛有效；标签模式没有跨器件物理路径而默认仍保留实体
attachment；runtime 几何检查；固定无 sheet 渲染；非法引脚、异网 attachment、
被堵出口、预算、互斥选项与失败时保留旧输出。新输入还暴露了两次平移对小数 bbox
造成的舍入差异，已改为从原测量一次性计算最终位置，未放宽几何比较标准。

执行结果：`TestNetLabels*`、`TestPowerIssue273*`、`TestLibLocalCLI*` 共 17 项通过；
`make test` 全仓通过（app 50.027 s），`make skill-check` 通过（201 个 Skill 文件及
样例一致性），`git diff --check` 无错误。

当前结论是 `offline-verified`，不是现场成品验收或原默认求解器问题关闭。
