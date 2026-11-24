# 上架 DSH 插件市场

DSH 的插件市场（`dshmarket`、`dshmarket.com`、`awesome-dsh-plugin.com`，以及 DSH 原生
`Plugins → Discover` 的索引源）都读取同一份**人工收录**目录，仓库里没有任何东西会被自动
发现。所以「市场搜不到我们」的充分条件就是：目录里没有 `zhoushoujianwork/easyeda-agent`
这一条。2026-11-24 核查：公开目录 `plugins.json` 12,960 条、npm 版索引 `index.json`
6,715 条，两条里都没有本仓库；目录仓库中本仓库的条目文件也没有任何历史提交，即从未收录过，
不是被下架。

## 两条读取链路

| 链路 | 地址 | 收录范围 | 我们能否出现 |
|---|---|---|---|
| 市场全量目录 | `https://awesome-dsh-plugin.com/plugins.json` | 人工收录的条目；`npm` 字段可为空（GitHub 源码安装） | 有目录条目即可 |
| DSH Discover 索引源 | `https://cdn.jsdelivr.net/npm/dsh-plugin-catalog/index.json` | 只含 **npm 已发布且有版本** 的条目 | 需在 npm 发布同名包 |

dshmarket 还强制「只允许安装已加载目录里的条目」，所以没收录时用户即使在聊天里贴
Git 地址也绕不过去，只能按 [dsh-integration.md](dsh-integration.md) 手动
`dsh plugin add`。

## 我们的目录条目

提交物只有一个文件：`data/plugins/zhoushoujianwork__easyeda-agent.yml`（文件名固定为
`<owner>__<repo>.yml`，放在
[awesome-dsh-plugin](https://github.com/awesome-dsh-plugin/awesome-dsh-plugin) 仓库，
不是 dsh-market 仓库）：

```yaml
url: https://github.com/zhoushoujianwork/easyeda-agent
name: zhoushoujianwork/easyeda-agent
category: tools
description:
  en: 'EasyEDA Pro (JLC EDA) automation for DSH: an MCP bridge with 12 tools plus an agent skill that drives the local easyeda CLI through typed schematic, PCB, project and system actions - inspect before mutate, save/reload/check gates and native DRC. Requires the easyeda CLI and the EDA connector extension.'
  zh: '给 DSH 的嘉立创 EDA 专业版（EasyEDA Pro）自动化：12 个工具的 MCP 桥接加一份 agent skill，经本地 easyeda CLI 执行类型化的原理图、PCB、工程与系统动作，先查后改，带保存/重载/检查门禁与原生 DRC。需先安装 easyeda CLI 与 EDA 连接器扩展。'
```

描述里的数字是可核对的事实（打包测试断言 12 个工具），审核会逐条对照代码；改动工具面时
同一 PR 里改这一行描述即可，不要夸大。分类选 `tools`：插件暴露的是操作能力，不是 UI、
模型或流程编排。

## 提交与核验

1. Fork `awesome-dsh-plugin/awesome-dsh-plugin`，从 `main` 建分支，只加上面那一个文件。
2. PR 标题 `Add zhoushoujianwork/easyeda-agent`，正文写：插件做什么、12 个工具与来源、
   安装前置（`easyeda` CLI + 连接器）、以及本仓库已声明 `dsh.bundle` 且带 `dsh-plugin`
   topic。CI 只校验形式（manifest、仓库年龄 ≥1 天、YAML、README 重新生成），合并前维护者
   会读代码核对描述。
3. 合并后目录由 CI 每日重建；先 `curl -s https://awesome-dsh-plugin.com/plugins.json |
   grep -c zhoushoujianwork` 应返回非 0，再在市场里搜 `easyeda` 或 `嘉立创` 确认命中，
   最后用市场的安装入口装一次并回读 `dsh` 插件清单。

收录不是永久的：仓库归档、长期停更或条目失效会被定期扫描移除；改了仓库名或分发方式要
同步改条目，且只改自己那条。

## npm 发布（推荐，非必需）

只提目录条目也能装（GitHub 源码安装，用户端需要构建/联网），但发布 npm 包会让安装跳过
构建授权，也才能进入 DSH Discover 的 npm 索引。本包已具备发布条件：

- 根 `package.json` 去掉 `private`，补 `keywords`（含 `dsh-plugin`）、`repository`、
  `homepage`、`bugs`；`files` 只带入口、patch、MCP 源码和公开 Skill。
- `easyeda-agent-dsh` 在 npm 上未被占用，版本与仓库发布标签同步（当前 1.13.1）。
- 发布属于版本发布动作，须按 [AGENTS.md](../AGENTS.md#发布) 先获用户对具体版本的批准，
  再由具备 npm 凭据的维护者执行 `npm publish`；不要把 `npm publish` 接进 CI 自动流程。
- 发布后目录条目会自动带上 npm 映射与版本，安装命令从 GitHub 源码切换为预构建包。

不想发 npm 时，可把 `npm pack` 产物作为 GitHub Release 资产，并在条目里加
`tarball:` 指向该 URL；市场会优先展示这个预构建 tarball，适用于无法从源码安装的网络。
