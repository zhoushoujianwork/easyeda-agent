# 完整 CLI 用例的布局复现输入

这十份 JSON 是 2026-09-27 原始需求回归的离线区域输入，详见[失败报告](../../2026-09-27-cli-layout-regression.md)。
来源为 Web 4.1.60 / connector 1.7.1-dev.11 的真实单件 pins、bbox 和最终 Designator 测量，
加上本轮自主生成的网络、归属和姿态许可。`manifest.json` 保存原始字节哈希。

```bash
easyeda sch layout-plan --zones --from docs/reviews/fixtures/2026-09-27-cli-layout/usb-input.json \
  --out /tmp/cli-usb-layout.json --report /tmp/cli-usb-report.json
```

替换文件名可重现各区。dev.11 的 usb/mux/uart/boot/mcu 失败，input/slew/buck/detect/led 生成候选。
失败是待修复问题，不能用“预期失败”把完整用例签为通过。修复后应更新复测记录，保留这些原始输入。
全部命令只离线计算；这些坐标是测量来源，不是可直接写入工程的最终布局。
