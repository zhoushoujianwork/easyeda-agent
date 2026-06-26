---
name: easyeda-conventions
description: EasyEDA/EE design conventions and best-practice reference — schematic layout zones, module spacing, wire/right-angle routing, netflag/component orientation, PCB placement conventions, and LCSC/JLC part-selection criteria. Plus the canonical data both skills derive from — orientation.json (the flag rotation truth) and standard-parts.json (the curated standard library). Consult before placing, wiring, laying out, or selecting parts. This is reference knowledge (no actions); the operational easyeda-schematic / easyeda-pcb skills link here.
---

# EasyEDA Design Conventions

The **single source of truth** for *what a good EasyEDA design looks like* — the
tool-agnostic EE knowledge, separated from the operational skills so it can be
maintained and reviewed on its own. The operational skills
([`easyeda-schematic`](../easyeda-schematic/SKILL.md), `easyeda-pcb` later) **link**
to these references; they never copy the rules. Change a convention here, once.

This skill has **no actions** — it is reference material. The operational skills are
responsible for explicitly consulting it (e.g. "before placing, follow the layout
zones in easyeda-conventions").

## References

| File | What it governs |
|---|---|
| [`references/design-pre-analysis.md`](references/design-pre-analysis.md) | **事前分析 SOP**（动手前）— 像设计师那样先分析(重器件/电源树/功能分组+信号流/网络分类/BOM明细表/幅面分页),产出一份「布局计划」+ 12 项 Gate 自检。`auto-layout-sop` 的**上游**。**不出计划不落坐标。** |
| [`references/auto-layout-sop.md`](references/auto-layout-sop.md) | **自动布局执行 SOP**（动手时）— 照「布局计划」自顶向下落坐标:图纸自适应 → 主器件分区 → 去耦绑定 → 接线/朝向微调。`design-pre-analysis` 的**下游**。 |
| [`references/schematic-layout-conventions.md`](references/schematic-layout-conventions.md) | Schematic: 3×3 zone map, module spacing, wire stub lengths, right-angle routing, **netflag/component orientation**, decoupling placement. |
| [`references/pcb-layout-conventions.md`](references/pcb-layout-conventions.md) | PCB: placement priority, net-class line widths/vias, layer assignment, grid pitch, keep-outs, silkscreen — the PCB counterpart of the above. |
| [`references/part-selection.md`](references/part-selection.md) | LCSC/JLC 比对选型: data sources, the ranking (spec → buildable → basic → preferred → cheapest), and the standardization loop. |
| [`references/orientation.json`](references/orientation.json) | **Canonical** flag/port body-rotation truth — 4 facts (cycle + 3 anchors) that *derive* the 12-entry table. The linter's `orient.py` and the connector's `deriveBodyRotation` both derive from this; `make lint-test` asserts they agree. **Never hand-edit the derived numbers.** |
| [`references/standard-parts.json`](references/standard-parts.json) | **Canonical** curated standard library: category → `{ MPN, LCSC C-number, libraryUuid, deviceUuid, footprint }`. Place from here first; add new picks back. |

## How the split works (single source, link-don't-copy)

- **Pure EE design knowledge** (tool-agnostic — "a power flag points up", spacing,
  zones, selection criteria) lives **here**.
- **Tool mechanics & connector quirks** live with the operational skill / `CLAUDE.md`
  / memory — NOT here. The clearest example: *which way a flag should point* is a
  convention (here, in the orientation table); *that `createNetFlag` stores the
  rotation negated and `connect_pin` auto-compensates* is a connector quirk (operational).
- Canonical **data** files (`orientation.json`, `standard-parts.json`) are read across
  the skill boundary by `easyeda-schematic/scripts/{orient,bom-enrich}.py`; the paths
  are relative (`../../easyeda-conventions/references/…`).

## Contributing domain knowledge

**This skill is the right place to contribute EE expertise.** If you have hands-on
experience with EasyEDA Pro, JLC/LCSC sourcing, or PCB/schematic design conventions,
a focused PR that improves one of the reference files is the highest-leverage
contribution you can make — it propagates to every AI-assisted design immediately.

High-value contributions:

| What | Where |
|---|---|
| Schematic layout rules: zone map, spacing, flag orientation | [`references/schematic-layout-conventions.md`](references/schematic-layout-conventions.md) |
| PCB placement/routing rules: line widths, grid, keep-outs | [`references/pcb-layout-conventions.md`](references/pcb-layout-conventions.md) |
| Part selection criteria: ranking, sourcing, standardisation loop | [`references/part-selection.md`](references/part-selection.md) |
| New standard parts (MPN + LCSC C-number + libraryUuid) | [`references/standard-parts.json`](references/standard-parts.json) |

**PR guidelines:**
- One convention per PR — small and reviewable beats large and risky.
- Cite the *why*: a real failure mode, a JLC sourcing constraint, or a tested measurement.
- If you're updating `orientation.json`, run `make lint-test` first — it asserts the
  linter and connector derive the same rotation table from that file.
- `standard-parts.json` additions should include MPN, LCSC C-number, libraryUuid,
  deviceUuid, and package; verify the C-number is in stock on lcsc.com before PR.
