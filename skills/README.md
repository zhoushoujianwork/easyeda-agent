# Skills

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

- `SKILL.md`: short routing layer and non-negotiable workflow rules.
- `references/design-flow.md`: whole-board staged workflow.
- `references/schematic.md`: schematic actions, guardrails, and workflow details.
- `references/pcb.md`: PCB actions, guardrails, and workflow details.
- `references/*-conventions.md`: schematic/PCB layout rules and SOPs.
- `references/orientation.json`: netflag/netport rotation truth.
- `references/standard-parts.json`: curated standard parts library.
- `scripts/`: lint, BOM enrichment, part cache write-back, selection, and calibration tools.

## Removed Split Directories

The old split skill directories (`easyeda-design-flow`, `easyeda-schematic`,
`easyeda-pcb`, `easyeda-conventions`) were merged into `easyeda-agent` and removed.
New releases and registry publishing use `easyeda-agent` only.

## Authoring Conventions

- Write AI-facing routing metadata in English: skill `name`, `description`, action
  names, and navigational headers.
- Keep detailed guidance in `references/` and keep `SKILL.md` lean.
- Cite reference files by bare name in prompts and code comments when possible, such
  as `orientation.json` or `pcb-layout-conventions.md`.
- Keep each rule or workflow in one home. Link to it instead of growing duplicate
  copies across code comments, docs, and prompts.
