# Skills

Two kinds of skill, deliberately split so each can be maintained on its own while
staying tightly cross-referenced.

| Skill | Kind | Holds |
|---|---|---|
| [`easyeda-conventions`](easyeda-conventions/SKILL.md) | **Reference** (no actions) | The tool-agnostic EE design truth — schematic/PCB layout conventions, part-selection criteria, and the canonical data (`orientation.json`, `standard-parts.json`). |
| [`easyeda-schematic`](easyeda-schematic/SKILL.md) | **Operational** | How to drive `easyeda-agent`: the typed-action workflow, scripts (`lint`, `bom-enrich`, `parts-select`, `calibrate`), and guardrails. (`easyeda-pcb` will join later.) |

## Why split

1. **Different change cadence & reviewer.** Conventions are EE domain knowledge
   (stable, reviewed by a hardware engineer). The operational skill changes with the
   tool (new actions, daemon/connector behavior). Separating them lets each evolve
   without churning the other.
2. **Shared across operational skills.** Schematic and (emerging) PCB both consume the
   same design conventions. One conventions skill keeps them DRY instead of letting
   `schematic-*` and `pcb-*` rules duplicate and drift.
3. **We paid the drift tax.** The flag-rotation truth once lived in `SKILL.md` context
   + four docs and had to be corrected in all of them. A single canonical home ends
   that.

## The boundary (what goes where)

- **Pure EE design knowledge** → `easyeda-conventions`. *Which way a flag points*,
  zone map, spacing, decoupling, selection criteria.
- **Tool mechanics & connector quirks** → `easyeda-schematic` / `CLAUDE.md` / memory.
  *That `createNetFlag` stores rotation negated and `connect_pin` compensates* is a
  connector quirk, not a design convention.

## The one rule that makes "fused but separate" work

**Single source — link, don't copy.** A convention or canonical datum lives in exactly
one place (`easyeda-conventions`); the operational skills `[link]` to it. The canonical
JSON is read across the skill boundary by the operational scripts
(`easyeda-schematic/scripts/{orient,bom-enrich}.py` →
`../../easyeda-conventions/references/…`), and `make lint-test` asserts the connector
and linter still derive the same orientation table.
