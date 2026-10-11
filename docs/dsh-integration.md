# easyeda-agent × DeepSeek Harness (DSH) 集成

DSH（`@deepseek-ai/dsh`，Cordis 插件化框架）原生支持 skill 与 MCP client 两种
插件形态，easyeda-agent 恰好两种资产都已具备，所以接入是配置级工作而非开发级：

| DSH 形态 | 本项目资产 | 落地方式 | 开发量 |
|---|---|---|---|
| **Skill**（SKILL.md 自动发现） | `.agents/skills/easyeda-agent/SKILL.md` | 软链进 DSH skill 根 | 0 |
| **MCP client**（`dsh-mcp-client` 桥接） | `mcp/`（stdio MCP server，12 工具） | `cordis.patch.yml` 加一行插件实例 | 几行 YAML |
| **Bundle 插件包**（`dsh.bundle.patch` 声明） | 仓库根 `package.json` + `cordis.patch.yml` | `dsh plugin add github:zhoushoujianwork/easyeda-agent#<tag>` 一行装 | 已完成 |
| **原生 Cordis 插件**（`ctx.tools` / client-plugin UI） | 暂无 | 新建 npm 包，注册结构化工具 / daemon 状态面板 | 中等，跟 rc 版本 |

## 团队/他人接入（推荐：Bundle 一键安装）

先按[安装说明](quick-start.md)安装独立的 `easyeda` CLI，再安装包含修复的 Git 分发包：

```sh
dsh plugin --profile web add git+https://github.com/zhoushoujianwork/easyeda-agent.git
# 重启 dsh web 生效；升级/卸载走 Settings → Plugins
```

仓库根声明了 `dsh.bundle.patch`（见根 `cordis.patch.yml`），安装后自动成为
profile 的活跃 bundle 层，注入两个行：

1. `easyeda-mcp` —— `dsh-mcp-client` 实例（in-box 插件，走 fallback 解析），
   MCP server 路径由 `!!js` 表达式基于 loader 的 `ctx.baseUrl`（= profile 目录）
   定位包内 `mcp/src/server.mjs`；`EASYEDA_BIN` 默认取 PATH，可用环境变量覆盖。
2. `easyeda-skill-fs` —— 独立的 `dsh-skill-filesystem` 实例
   （`providerName: easyeda`、`includeDefaultRoots: false`），只扫包内
   `.agents/skills/easyeda-agent`，注册进 skill 注册表 global layer（web 下 host 的
   skill-filesystem 被官方 bundle 禁用、preset 自有发现，故用隔离实例，不冲突）。

两处文件路径都由 Node 内置 `fileURLToPath` 转换，不直接读取 URL 的 `pathname`。
后者在 Windows 会留下 `/C:/...`，导致 Node 启动 MCP 时报 `C:\C:\... MODULE_NOT_FOUND`
（[#201](https://github.com/zhoushoujianwork/easyeda-agent/issues/201)），还会丢失 UNC 的
服务器名。转换同时保留中文、空格、`#` 和 `%`。`!!js` 通过
`process.getBuiltinModule('node:url')` 访问内置模块，兼容本包最低 Node 20.17，
不依赖 loader 是否提供 `require`。已有安装需更新 bundle 并重启 DSH。

根包显式提供 `main: ./index.js`，兼容 DSH 安装器的插件入口检查
（[#274](https://github.com/zhoushoujianwork/easyeda-agent/issues/274)）。该 CommonJS
入口只导出空的 Cordis `apply`，不启动 MCP、不重复注册 Skill；实际功能由 bundle patch
注入。入口源码直接随 Git/npm 打包，无需 `prepare` 构建。MCP SDK 运行依赖也在根包声明，
Git 安装不需要另行进入 `mcp/` 安装依赖；包的 `files` 清单限定入口、patch、MCP 源码与
公开 Skill，避免把 Go/connector 源码和本地运行资料带入插件包。

遇到该报错时应安装包含修复的 Git ref；旧 release tag 内容保持不变。尚未集成到所用
ref 时，不要仅靠重复安装同一 tag 或在用户安装目录中手工创建 `index.js`。

**当前验证范围**：2026-10-11 在 macOS arm64 的独立 `DSH_HOME` 和全新 `web`
profile，验证 main 的 `a3f2b7e` Git 安装、Plugin Hub v1.4.8 原版装后入口检查，以及
官方 DSH profile 启动链中的 Web HTTP 200、12 个 MCP 工具、真实 CLI 的只读目录调用和
Skill provider `easyeda` 正文读取。CLI 为 0.1.5-rc.1，npm 版本范围将 MCP/Skill 模块解析为
0.1.5-rc.3，loader 为 1.0.3。Windows 原报障环境、Hub 网页完整安装和装前预检未计为通过；
详细结果与复测反馈见 [#274](https://github.com/zhoushoujianwork/easyeda-agent/issues/274#issuecomment-6104813413)。

Windows 安装器不自动修改 PATH；启动 DSH 的同一 PowerShell 中应检查 CLI 路径：

```powershell
$env:EASYEDA_BIN = Join-Path $env:USERPROFILE '.local\bin\easyeda.exe'
& $env:EASYEDA_BIN version
(& $env:EASYEDA_BIN actions | ConvertFrom-Json).Count
dsh --profile web --dump-config | Select-String 'easyeda-mcp|easyeda-skill-fs'
```

非默认安装时使用实际 `easyeda.exe` 路径。`dump-config` 只验证配置；重启 DSH 后还需
发现 `mcp__easyeda__easyeda_actions`、执行只读目录调用，并通过 Skill 功能读取
`easyeda-agent` 正文，才算运行加载通过。

**已验证（2026-08-14）**：`dsh plugin add file:...` 到 headless profile → 自动
提升为 bundle 层 → headless 会话实测模型可见全部 11 个 `mcp__easyeda__*` 工具
+ `easyeda-agent` skill。这是当时的本地 `file:` 安装结果，不覆盖后续 DSH 版本的
Git 分发入口检查；当前包内容以根 `package.json` 的 `files` 清单为准。

**版本同步**：根 `package.json` 的 `version` 应与 release tag 对齐（`make release`
目前不自动改它，发版前手动同步一次即可）。

## 团队/他人接入（兜底：一键脚本）

如果不想走 bundle（比如还没发版、想本地开发态接入），clone 后跑
`scripts/dsh-install.sh`（幂等；自动探测仓库路径、注入/更新 `cordis.patch.yml`、
防重复）：

```bash
git clone https://github.com/zhoushoujianwork/easyeda-agent.git
bash easyeda-agent/scripts/dsh-install.sh                  # profile 默认 web
# bash easyeda-agent/scripts/dsh-install.sh --profile headless
# DSH_HOME=/custom/.dsh bash easyeda-agent/scripts/dsh-install.sh
```

前提：已装 `easyeda` CLI（`curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | bash`）、
`web` profile 至少启动过一次（先 `dsh web` 初始化）。脚本会：①软链 skill
（watcher 即时发现）；②注入/更新 `cordis.patch.yml` 里的 `easyeda-mcp` 条目
（含 MCP server 与 `EASYEDA_BIN` 的绝对路径）；③打印验证与重启提示。之后
kill 当前 dsh 进程、在原目录重新 `dsh web`，MCP 工具即出现。

## 已落地（本机，2026-08）

### 路径 A — Skill（已生效，无需重启）

```bash
mkdir -p ~/.dsh/skills
ln -sfn <repo>/.agents/skills/easyeda-agent ~/.dsh/skills/easyeda-agent
```

DSH 的 skill-filesystem 提供者扫描根：`<projectRoot>/.dsh/skills`、
`<projectRoot>/.agents/skills`、`customSkillDirs`、`~/.dsh/skills`、
`~/.agents/skills`（只认一级目录 `SKILL.md` 或扁平 `.md`，名字必须 kebab-case）。
软链后 watcher 即时发现（无需重启），模型按 `skill` 工具加载，照旧通过 bash 调
`easyeda` CLI 干活——这正是本项目 Skill 的设计工作方式。

### 路径 B — MCP client（已配置，重启 dsh web 后生效）

在 `~/.dsh/profiles/<profile>/cordis.patch.yml`（本机为 `web`）追加：

```yaml
- insert:
    - id: easyeda-mcp
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: easyeda
        transport: stdio
        command: node
        args:
          - <repo>/mcp/src/server.mjs
        env:
          EASYEDA_BIN: /usr/local/bin/easyeda
```

生效后模型看到 `mcp__easyeda__easyeda_health`、`mcp__easyeda__easyeda_schematic`
等 11 个结构化工具（参数走 MCP 字段而非 shell 文本）。

**版本坑（已踩）**：DSH 的插件解析顺序是 profile 自己的 `node_modules` 优先，
其次才是 `$DSH_HOME/profiles/node_modules` 的扁平 fallback（由 `dsh` 启动时的
`healProfilesModuleFallback` 按安装清单重建的符号链接，指向 dsh 安装目录里的
in-box 插件）。`pnpm add @deepseek-ai/dsh-mcp-client` 会装到 registry 的
**latest（旧版 `0.0.1-rc.1`）**，遮蔽 fallback 里的 `0.1.0-rc.6`，导致插件
API 与当前 dsh 不匹配。**in-box 插件不需要装进 profile**——只写 `cordis.patch.yml`
即可从 fallback 解析；误装后用 `dsh plugin --profile web remove @deepseek-ai/dsh-mcp-client`
删掉。

### 验证

路径回归（无额外 npm 依赖、无需启动编辑器或 daemon）：

```bash
node --test scripts/tests/test_dsh_bundle.mjs
```

该测试执行 bundle 中实际的两条路径表达式，覆盖 Windows 盘符和 UNC、POSIX、
中文/空格/URL 转义；并在当前系统的临时 profile 中按解析路径启动 Node、读取 Skill。
Windows 路径转换可以在 Mac 上用 Node 的 Windows 转换模式验证，但不等同于
Windows DSH 实际启动。CI 的 macOS/Linux/Windows 原生安装矩阵均运行此测试。

分发回归从真实 npm tarball 安装到仓库外的临时 profile，校验入口、patch 与 Skill，
再通过安装包自己的 SDK 启动真实 stdio MCP，完成握手、工具枚举与离线 action 发现：

```bash
npm ci --ignore-scripts
npm run test:dsh
```

临时安装优先使用 npm 缓存，缺少包或 registry 元数据时正常联网解析依赖，不借用
checkout 的 `mcp/node_modules`。`npm ci` 只保证锁定 tarball 已下载，不保证新消费者安装
所需的 registry 元数据已缓存。协议测试使用最小 CLI 目录 fixture，不连接 daemon 或编辑器；
因此证明包可解析、依赖齐全和 MCP 可启动，不代替 DSH Web 或真实 EDA 的现场验收。

**路径修复已在 macOS 验证**：使用本机 DSH loader 1.0.2 解析实际 bundle YAML，
在含中文、空格、`#`、`%` 的临时 profile 中定位包目录，启动仓库真实 stdio MCP，
完成握手、11 个工具枚举和离线调用，并读取 Skill；未运行 Windows DSH。

```bash
# 配置合并树（不启动服务）：应出现 easyeda-mcp 条目
dsh --profile web --dump-config | grep -A 16 easyeda-mcp
```

host 插件（MCP client）改动需要**重启 dsh web** 才加载（HMR 只覆盖 client-plugin）；
skill 软链则由 watcher 即时发现。重启方式：找到 dsh 进程 kill 后，在原工作目录
重新 `dsh web`。

## 演进方向 — 路径 C（原生插件）

写一个 profile 本地插件 / npm 包：
- `ctx.tools.register()`：把 20 个 typed actions 直接映射成结构化 DSH 工具，
  摆脱 CLI 文本解析（比 MCP 更"第一方"，可拿 `ctx.skills`/approval/scope 集成）；
- `ctx.skills.register()`：runtime 注册 skill（rank 250，可被项目级覆盖）；
- client-plugin：daemon 状态 / 连接器健康 / `layout-lint` 结果 / audit 基线
  做成 Web UI 面板。

代价是跟随 `0.1.0-rc` 的 API 变动，建议 A+B 跑顺后再按需演进。
