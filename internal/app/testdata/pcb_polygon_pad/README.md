# 多边形焊盘投影回归

`usb-c-component.json` 保留 TYPE-C-31-M-12 的 4 个绝对 POLYGON 和 12 个相对尺寸焊盘。
来源为 2026-09-27 ESP32 原始需求用例中 dev.12 保存重载后的 typed PCB dump；
仅替换 runtime primitiveId，不改几何、网络或实例锚点。原生 footprint/实例对应和
失败 CLI 最小复现在 `artifacts/release-v1.8.0-e2e-20260927/a02-dev12-layout-20260927/`，
冻结清单 SHA256 `5a55f66bb4f3c0a92732a56184ca5d8d23470a3f37ba9b54140c6cec42eec455`。

输入是当前 source geometry，不是完成布局答案；测试覆盖路径/中心同一仿射变换、
尺寸参数保留与重复投影不污染来源。离线通过不代表现场布局或铜布线通过。
