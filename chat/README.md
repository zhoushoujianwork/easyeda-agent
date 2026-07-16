# chat/ — 聊天与讨论归档

保留开发过程中**有价值的讨论、决策推演、方向评估**，按主题/功能分文件。
和 `docs/` 的区别：`docs/` 是沉淀后的权威文档（给读者看「结论是什么」），
`chat/` 是讨论现场（保留「为什么这么定、当时怎么推的、有哪些被否掉的路」）。

> 规则：一个主题一个文件；文件名用 `主题.md`，需要记时间线的用 `YYYY-MM-DD-主题.md`。
> 讨论收敛出权威结论后，把结论同步回 `docs/` 或对应 Skill，`chat/` 这里留推演脉络。

## 索引

| 文件 | 主题 |
|---|---|
| [2026-07-16-blocks-data-model.md](2026-07-16-blocks-data-model.md) | Blocks 数据结构缺陷、block-apply 价值验证(已通过)、反馈闭环定案(否决在线服务,走 GitHub Issue) |
| [2026-07-16-new-parts-research-archive.md](2026-07-16-new-parts-research-archive.md) | 四颗新器件一手源调研存档(CAN/WS2812B/EEPROM/蜂鸣器)——未落库,待需求触发 |
| [2026-06-30-skill-merge.md](2026-06-30-skill-merge.md) | 将 4 个 EasyEDA 技能合并为单一 `easyeda-agent` 技能的决策记录 |
| [项目方向评估.md](项目方向评估.md) | 整体态势、roadmap 取舍、高价值前沿在哪 |
| [pcb-自动布线方向.md](pcb-自动布线方向.md) | 自动布线能不能做、文件式往返 vs 提 issue vs 等 API |
| [对标-jlceda-mcp.md](对标-jlceda-mcp.md) | 第三方 MCP 路线对标，4 条对已实现部分的改进建议 |
