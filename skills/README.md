# Skills

> ⚠️ **AI 或人在新增/修改 `easyeda-agent/` 下任何文件前,必须先读本文件末尾的
> 「编写与维护规约」一节并遵守。** 那是本承重公开
> 技能的编写红线,防止后续被不守规则的写入污染。本 README 在技能目录**之外**,不随
> `make release`/clawhub 打包分发(打包只含 `easyeda-agent/` 子目录),既是护栏又不占技能上下文。

The public package is now one merged skill:

| Skill | Holds |
|---|---|
| [`easyeda-agent`](easyeda-agent/SKILL.md) | The EasyEDA Agent workflow spine, schematic and PCB operational guidance, shared design conventions, canonical data (`orientation.json`, `standard-parts.json`), and bundled scripts. |

The `easyeda-agent` suffix is intentional. It distinguishes this community
automation layer from official EasyEDA tooling while matching the repository, CLI,
daemon, and connector name.

## Install

Install the `easyeda` CLI/daemon first, then import the EasyEDA connector URL printed
by the installer:

```bash
curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | sh
```

The installer auto-detects your AI clients and installs/updates the
`easyeda-agent` skill into each: Codex (`~/.codex/skills/easyeda-agent`) and
Claude Code (`~/.claude/skills/easyeda-agent`). Set
`EASYEDA_INSTALL_SKILLS=codex,claude` to force targets, `none` to skip, or
`EASYEDA_SKILL_PRESERVE=1` to keep local edits during an update.

To install only the skill from a registry:

```bash
# ClawHub
clawhub install easyeda-agent

# 国内 SkillHub
skillhub install easyeda-agent --registry https://skillhub.cn
```

## Internal Layout

The merged skill keeps the old separation internally through progressive disclosure:

- `SKILL.md`: 顶层抗遗忘扫读区(铁律 / 停点·档位·块地图速查 / 顺序硬约束)+ 加载触发索引(What To Read)。
- `references/design-flow.md`: whole-board staged workflow(S0–S6 / P0–P10,顶部有阶段 TOC)。
- `references/schematic.md` / `references/pcb.md`: schematic / PCB actions, guardrails, workflow details。
- `references/*-conventions.md`: schematic/PCB layout rules and SOPs。
- `references/design-decisions.md`: S0 方案书要摊给用户拍板的真实权衡清单。
- `references/fab-rules-jlcpcb.json`: DRC 制造规则地板 + fallback seed。
- `references/orientation.json`: netflag/netport rotation truth。
- `references/standard-parts.json`: curated standard parts library。
- `scripts/`: lint, BOM enrichment, part cache write-back, selection, calibration tools。
- **电路块库不在技能里** —— 20 个块住在 CLI(`easyeda blocks ls/show/search`,`internal/blocks/data/*.json`
  经 go:embed,离线、零 daemon 依赖);技能只**指向**它(铁律 8 + 块地图速查)。别到 `references/` 找块 JSON。

## Removed Split Directories

The old split skill directories (`easyeda-design-flow`, `easyeda-schematic`,
`easyeda-pcb`, `easyeda-conventions`) were merged into `easyeda-agent` and removed.
New releases and registry publishing use `easyeda-agent` only.

---

## 编写与维护规约 (⚠️ 改 `easyeda-agent/` 前必读)

`easyeda-agent` 是**已上线、承重的公开技能**,里面很多是**实测踩坑换来的硬知识**。任何 AI 或人在
新增/修改它之前**必须遵守以下规约**,违反 = 污染技能。标尺来自官方 `create-agent-skills` + 本项目
实战沉淀。

### 0. 三条最高红线

1. **不删实测硬知识。** references 里全是 load-bearing gotchas。"精简"**只允许** = 去重 / 下沉到
   reference / 结构化;**绝不允许** = 丢细节。删任何一句前,先确认它在别处保留或确实冗余。
2. **信息只有一个家。** 同一规则/事实只在 SKILL.md 或某个 reference **之一**,不许两处各写一份。
   **尤其别再往 SKILL.md 加与铁律重复的第二份规则表** —— 2026-07 删掉的英文 "Core Rules" 就是这种
   中英双份、改一处漏一处的漂移源,别重蹈。倾向下沉到 reference,SKILL.md 只留指针。
3. **改底层必同步改 Skill。** 改了 typed action / daemon / CLI / 连接器 / 块库,**必须同步**更新
   SKILL.md、references 里对应的描述、示例、触发、注意事项(项目第一准则:Skill 优先)。

### 1. 结构(create-agent-skills 官方标准)

- 技能 = `SKILL.md`(必需,frontmatter **只有** `name` + `description` 两个字段)+ `references/`
  (按需加载的文档)+ `scripts/`(可执行,可不读进上下文)+ `assets/`(进最终产物)。
- **不放** README / CHANGELOG / 安装指南 / 测试说明 到 `easyeda-agent/` **里面** —— 那是给人看的过程
  文档,污染上下文(本 charter 放在技能目录**外**正是为此)。
- **不留** `__pycache__` / `*.pyc` / 任何编译产物在技能目录。
- 渐进式披露三层:frontmatter(常驻,~100 词)→ SKILL.md body(触发后加载,精简、目标 <500 行)
  → references(按需读)/ scripts(可直接执行不读)。

### 2. SKILL.md 信息架构(已定型,别打乱)

顺序固定为四个带职责的区,新增内容**归入对应区**、别插在中间打断扫读:

1. **① 铁律(抗遗忘扫读区)** —— **单一权威中文表**,每条一行祈使句 + `→` 指到细节文件。
   不许再加第二份规则表(中/英都不许)。
2. **② 停点 / 档位 / 块地图 三张速查** —— 执行前定位「哪阶段 / 停不停 / 走哪档 / 读哪张块 map」。
3. **③ 顺序硬约束表** —— "反了必返工" 的强顺序约束汇总。
4. **What To Read(load-more 加载触发索引)** → **Bundled Scripts** → **Deliverables**。

### 3. 必须保持的三个核心行为(别削弱)

- **抗遗忘**:任何「违反 = 返工/坏板」的承重硬约束,必须能从**顶层扫读区触达**。顶层放**压缩指针**,
  细节留 design-flow —— 别把整段细节抄到顶层(又制造副本)。深流程步骤用**低自由度清单**(可勾选),
  别写成长散文。
- **加载更多(渐进式披露)**:每个 reference 在 What To Read 里都要有**精确、锚定到决策/执行点**的
  加载触发(不是「做 X 就读 X」的宽触发);**>100 行 reference 顶部必须有 TOC**;引用**只一层深**
  (都从 SKILL.md 直链);**链接锚文本 = 目标文件名**(别用 `easyeda-agent` 之类同名锚指向不同文件)。
- **查块**:任何「搭已知外围」的执行点(S0 选型 / S3 接线 / P2 摆放 / P7.0 关键网 / P9 丝印 / BOM)
  都要提示**先查块库**;块住在 CLI(`easyeda blocks`,go:embed,离线),**不在技能文件里**;类目/字段名
  以 `easyeda blocks ls --json` / `show --json` **实测为准,别硬编可能过时的枚举**。

### 4. 语言约定

- **AI 触发元数据用英文**:skill `name`、`description`(可加少量中文锚点词)、action 名、`eda.*` 字面量。
- **body 内的抗遗忘扫读面(铁律 / 速查 / 顺序约束)用中文单一语言**(2026-07 决定:同一规则别中英各
  一份 = 漂移)。
- references 正文各自语言内部自洽即可,**跨文件不重复同一事实**。

### 5. 改动前后必跑的验证门

markdown / frontmatter 改动以事实回归为主:

- `go test ./internal/blocks/`(块库校验,防块 JSON / 交叉引用坏)
- `make lint-test`(原理图 lint 规则信任)
- frontmatter 合法且**只有** name + description;description `< ~1024` 字符
- grep 自查:无跨文件同一规则的重复副本(如 "Core Rules" 不该复活)
- 所有相对链接目标存在;What To Read 引用的 reference 都真实存在
- SKILL / design-flow 里硬编的块类目 slug 与 `blocks ls --json` 一致
- **大改后**按 CLAUDE.md 铁律用 `esp32MiniRequire.md` 端到端真机回归(需连 EasyEDA,工程用 `ceshi`)

### 6. 分发边界

- `make release` 的 tar 用 `-C skills easyeda-agent`、clawhub `publish .../skills/easyeda-agent`
  —— **只打包 `easyeda-agent/` 子目录**。本 `skills/README.md` 不进包,是纯 repo 内 maintainer/AI 规约。
- 贡献新电路块走 `references/standard-blocks-contributing.md`(留在技能内 + 收尾 nudge,已定案)。
