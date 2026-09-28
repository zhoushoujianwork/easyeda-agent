# CLI 大响应截断修复与续测

> 历史结论，仅为发布材料兼容保留；当前能力见 [CLI Status](../cli-STATUS.md)。
> 逐步运行记录见[固定版本原文](https://github.com/zhoushoujianwork/easyeda-agent/blob/3283c05f8e42cdf8694bedf58330b71b3f3c667c/docs/reviews/2026-09-27-cli-response-truncation.md)。

[#263](https://github.com/zhoushoujianwork/easyeda-agent/issues/263) 的根因是 CLI 静默截断动作 HTTP 响应。
修复 `d3409a0` 已完成离线测试和 P1 保存重载的有限现场复核；该结果不代表完整 E2E 通过。

## 原始失败

完整 ESP32 用例的 P1 Compose 前 272 步成功，第 273 步验证读取返回 JSON 意外结束。
独立读取也失败，stdout 为 1,048,577 字节（1 MiB 加换行）。当时 29 件及计划接线已写入，
typed save 成功但 `restoreVerified=false`，P2/P3 只有图框；没有重放旧队列。

## 修复与验证

公共 `postAction` 原先直接读取 `io.LimitReader(resp.Body, 1<<20)`，到上限不报错。
修复后动作 HTTP 响应上限 32 MiB，health 单独保留 1 MiB；多读一字节识别溢出，
超限或读取错误返回明确错误及 nil body，不输出部分响应、不自动重发动作。
连接器 WebSocket 限制是另一层约束，不宣称无限响应支持。

真实 HTTP 回归覆盖大响应的完整尾部、精确边界、超限和中途读失败；原截断与溢出误判
负例在旧代码下失败，修复后相关测试 7 项通过。当批全量 Go 测试为 3,924 pass、1 skip，
Skill 检查通过。这些数字只记录当批结果。

首次现场只读验证得到完整 1,388,244 字节响应，29 件、88 pins 及尾部器件的引脚/网络可读。
输出 SHA-256：`72e86a1102a7117b6cafe65b608c0054b28b596f34982eb434a000b5788f7acd`。
代码与该次只读结果的独立复核报告 SHA-256：
`418a75479ab7849db6272e8a8ba16ffb9687ecf6360e1b154828e20c827edab9`。
该轮复核未签整页连接或保存重载。

## 后续

随后基于 `d3409a0` 的 fresh 状态重新编译 15-step 恢复，完成 P1 显式保存重载和
逐件、逐脚、线树对账，经独立有限复核后关闭 #263。宿主隐藏属性的身份/位置归一化差异
仍保留，不声称完整 result 字节相同。范围和后续失败见[原理图恢复结论](2026-09-27-cli-schematic-gate.md)。

这次关闭不补签 F2/E1/SDK DRC/E2E，也不能作为发布已经完成的证据。
