# 下一版本发布评估

当前正式版为 [v1.9.0](https://github.com/zhoushoujianwork/easyeda-agent/releases/tag/v1.9.0)。
本轮准备 **v1.9.1**，只纳入 main 已集成的 DSH 分发修复与对应说明；用户已批准发布该版本。
发布规则见 [发布流程](../release-workflow.md)，既有设计验收范围见
[v1.9.0 发布记录](release-1.9.md)，日常开发状态见 [CLI Status](../cli-STATUS.md)。

## 修复与用户收益

- #274：根包提供声明的 `index.js` 入口和 MCP SDK，Git 安装不再缺入口或嵌套依赖。
- 分发白名单限制为公开入口、bundle patch、MCP 源码与 Skill，排除本地运行资料及无关代码。
- npm tarball 在仓库外安装、SDK/MCP 握手、12 个工具与目录发现回归覆盖三平台 CI；
  `--prefer-offline` 允许缺失元数据时联网，不声明严格断网安装。
- DSH 安装文档补齐独立 CLI、Windows `EASYEDA_BIN`、配置与运行检查的区别。

入口修复为 [4391cfd](https://github.com/zhoushoujianwork/easyeda-agent/commit/4391cfd2a5188689d26da5eba80fe54177c1e998)，
干净缓存回归修复为 [b535c26](https://github.com/zhoushoujianwork/easyeda-agent/commit/b535c26c4047fcdce982c4565f6c9ae6a59c5084)。
本版不改变连接器运行逻辑或动作契约，按 patch 递增，全部分发组件仍统一为 1.9.1。

## 验证状态与边界

- main `a3f2b7e` 的最新 CI 通过；Windows/macOS/Linux 的 npm 分发测试均为 8/8。
- macOS 独立 DSH_HOME、全新 web profile 的真实 Git 安装、Hub v1.4.8 装后入口检查、
  官方 DSH Web 启动、12 个 MCP 工具、真实 CLI 目录调用和 Skill provider 正文读取通过。
  CLI 0.1.5-rc.1 的 MCP/Skill 依赖解析为 rc.3，loader 为 1.0.3；
  [完整反馈](https://github.com/zhoushoujianwork/easyeda-agent/issues/274#issuecomment-6104813413)保留精确范围。
- v1.9.1 候选的版本一致性、打包/MCP 回归、发布脚本与自更新定向测试、发布构建通过；
  9 项资产 SHA256、15 项本机 CLI 冒烟及 201 个 Skill 文件与安装后链接核对通过。
- macOS 隔离目录中，真实安装脚本读取候选资产字节，首次安装和从已发布 v1.9.0 升级通过；
  下载端点使用本地替身。候选本地 Git ref 的 DSH 安装、Web/MCP/Skill 加载也通过。
  这些结果不冒充远端 v1.9.1 或 `easyeda update` 联网升级已通过。
- 原 Windows 报障环境、Hub 网页完整安装流程仍待复测，#274 保持 open。
  发布修复包不自动关闭用户故障；远端发布资产只能在实际上传后核验。
- 完整设计 E2E 保持原状态。本次不运行全集，不把离线或 DSH 加载成功补签为 EDA 设计通过。

## 其他近期 Issue

#273 的显式标签/展开布局已在 v1.8.1 提供离线替代路径，默认搜索耗尽仍未修复；
#267 在 dev 已提交的 CLI 保留器件重建有有限防护，但广义连接器清理和现场验收仍未完成；
#268 的后台保存改动仍在未提交开发工作中。它们不计为 v1.9.1 已解决，也不夹带入此分发补丁。

v1.8.0 的历史发布评估与有限验收范围保留在 [对应发布记录](release-1.8.md)，
不作为新版本的默认豁免。
