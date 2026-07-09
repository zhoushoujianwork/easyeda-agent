---
name: easyeda-agent
description: "Community EasyEDA Agent automation skill for EasyEDA Pro schematic and PCB work through the local easyeda-agent CLI/daemon/connector. Use when designing a board from scratch, editing or inspecting schematics, placing/wiring real LCSC/JLC library parts, syncing schematic changes into PCB, laying out PCB components, running EasyEDA DRC/check/layout-lint, exporting BOM/netlists/artifacts, or using the bundled EasyEDA scripts and design conventions. This is the merged public skill replacing easyeda-schematic, easyeda-pcb, easyeda-design-flow, and easyeda-conventions."
---

# EasyEDA Agent

Use the local `easyeda` CLI and daemon to operate EasyEDA Pro through typed,
observable actions. This is the community `easyeda-agent` workflow, not an official
EasyEDA skill; the suffix is intentional so users can distinguish it from upstream
EasyEDA tooling.

> **Source & docs:** https://github.com/zhoushoujianwork/easyeda-agent · Plugin
> marketplace listing coming soon. Install the CLI + connector per the repo README.

## Core Rules

1. Run `easyeda health` before any window-scoped action.
2. Use typed `easyeda` actions. Use raw `debug.exec_js` only when a typed action is
   missing and the user explicitly accepts a debug path.
3. Inspect before mutating: read docs/pages, components, pins, board/layers/nets, and
   relevant rules before placing, moving, wiring, syncing, or saving.
3a. **Netlist source of truth:** never call deprecated `eda.sch_Netlist.getNetlist()`
    for schematic reads or debug snippets. Official prodocs marks it obsolete and
    says to use `SCH_ManufactureData.getNetlistFile()`; issue easyeda/pro-api-sdk#30
    shows `getNetlist()` can hang indefinitely on schematics with floating pins.
    Use `easyeda sch read`, `easyeda sch check`, or `easyeda sch netlist/export`;
    if a raw debug path is unavoidable, call `eda.sch_ManufactureData.getNetlistFile(...)`
    and read the returned `File.text()`.
4. Confirm before destructive operations such as clear/delete/import bulk changes.
4a. `sch autoconnect` is idempotent (issue #50): before connecting it reads each
    pin's current net, SKIPS pins already on the target net (`already-connected`),
    and ERRORS on pins wired to a different net unless you pass `--replace` (which
    deletes the old flag+wire and reconnects). Re-running the same spec is safe and
    won't stack duplicate flags/wires. The lower-level `sch connect` is still NOT
    idempotent — for single ad-hoc stubs, verify with a read (`sch read`/`sch list`)
    rather than re-issuing the same call.
4b. **Block-first for standard peripherals (电路块库).** Before hand-selecting and
    hand-wiring a well-known peripheral subcircuit (CH340 USB-serial, ESP32
    auto-download, button de-bounce, USB-hub, buck …), check
    `references/standard-blocks.json` — a community-built library of KNOWN-GOOD,
    validated subcircuits you copy verbatim and only rebind the boundary nets
    (ports) + reallocate RefDes. Pins are referenced by FUNCTIONAL NAME, so reuse
    needs zero pin-renumbering; each block's `parts` point into `standard-parts.json`.
    Run `scripts/blocks.py ls` to browse and `scripts/blocks.py show <id>` for the
    full topology + `schematic_notes` (wiring gotchas) + `pcb_layout` (electrical
    constraints). Only fall back to hand-wiring when no block covers the need — and
    when you validate a new peripheral end-to-end, contribute it back per
    `references/standard-blocks-contributing.md` (署名 + `validated` gate).
5. For non-trivial boards, follow the gated flow: pre-analysis, sheet/page plan,
   module grouping, group placement, channel routing, DRC/check/layout-lint, adjust,
   save checkpoints. Interaction defaults to milestone-confirmation (three tiers:
   auto / milestone / step) — see `references/design-flow.md` → "交互模式(Interaction
   Modes)" for the full definition.
6. Persist good checkpoints with explicit `easyeda sch save` / PCB save workflows;
   debounced autosave is only a safety net.
7. Judge correctness from data (`list`, `check`, `drc`, `layout-lint`), not screenshots.
   A capture can be **stale** (byte-identical after an edit) or outright **blank** (the
   EasyEDA window isn't rendering the doc — minimized / backgrounded / behind other
   windows). A data↔screenshot divergence (e.g. `primitiveCount>0` but a flat blank
   frame) is a first-class signal that the *window isn't rendering*, not that the design
   is wrong. **No API call repaints a hidden window** — verified: `view fit` / zoomToAll /
   ratline / `openDocument` / tab-switch all fail; the only fix is bringing EasyEDA to the
   foreground on the target tab. **Exception — recording/demo mode:** when the user
   explicitly says they are recording, making a demo/tutorial, or wants screenshots,
   images become a *deliverable*. Use `easyeda pcb stage-snapshot --stage … [--previous-sha256 …]`
   — it captures a native snapshot + data bundle and **gates** on blank / stale / wrong-document
   frames (non-zero exit), so a `set -e` recording script halts instead of banking a bad
   frame. Never substitute a data-rendered recap image for a live screenshot without
   flagging it. See `references/design-flow.md` → "录制 / 演示模式".

## What To Read

- `easyeda health` shows `windows: []` / `NO_CONNECTOR`, or you changed the
  connector (`extension/`): read `references/environment-setup.md` — with a
  browser-control tool (chrome-devtools MCP) the agent bootstraps the whole live
  environment itself (open web editor → open project → verify attach → hot-reload
  connector via IndexedDB, no uninstall/re-import); only fall back to asking the
  user when no browser control is available.
- Whole board, from scratch, or >~10 parts: read `references/design-flow.md` first.
- Architecture trade-off pitfalls (genuine choices, not one right answer — stackup,
  ground strategy, connector orientation, part cost tier): read
  `references/design-decisions.md`; the S0 design-proposal stage produces a proposal
  from these for the user to confirm. (RF/antenna keepout is a guardrail with one
  correct answer — full-layer coverage — not a Decision; it stays out of this list.)
- Schematic work: read `references/schematic.md` and `references/actions.md`.
- PCB work: read `references/pcb.md`.
- New/uncertain raw `eda.*` API use: first run `easyeda api search/show`, then check
  the matching official prodocs reference page when the method is `@alpha`, `@beta`,
  `@deprecated`, or has a known upstream issue. Record the caveat in references before
  turning it into an agent workflow.
- Schematic layout rules: read `references/schematic-layout-conventions.md`.
- PCB placement/routing rules: read `references/pcb-layout-conventions.md`.
- CLI placement/routing hard pits and auto-layout/autoconnect SOP: read
  `references/auto-layout-sop.md`.
- Part selection, JLC/LCSC ranking, and standardization: read
  `references/part-selection.md` and use `references/standard-parts.json`.
- **Standard peripheral circuits (电路块库):** before hand-wiring a known peripheral,
  use `references/standard-blocks.json` (browse via `scripts/blocks.py ls/show`) —
  copy-verbatim topology + rebind ports. Contributing a new block:
  `references/standard-blocks-contributing.md`.
- Netflag/netport rotation truth: use `references/orientation.json`; never hand-edit
  derived rotation tables.
- Sheet/title-block geometry conventions: read `references/sheet-templates.json`.

## Bundled Scripts

Scripts live in `scripts/` and are intended to be run directly when useful:

- `scripts/lint.sh <project>`: live schematic lint with optional diff baseline.
- `scripts/tests/run.py`: linter rule-trust harness; run after changes to
  `orientation.json`, linter rules, fixtures, or connector orientation facts.
- `scripts/bom-enrich.py <bom.tsv/csv>`: fill EasyEDA BOM Supplier Part values from
  `standard-parts.json`.
- `scripts/parts-add.py`: append resolved library parts into `standard-parts.json`.
- `scripts/parts-select.py`: deterministic part-selection helper.
- `scripts/blocks.py`: standard circuit-block library — `ls` / `show <id>` /
  `validate`. Browse reusable peripheral subcircuits and lint `standard-blocks.json`
  against the schema + contribution rules (the PR gate).
- `scripts/calibrate.js`: live bbox calibration for netflag/netport orientation after
  importing a new connector build.

## Deliverables

Summarize changed primitives, commands run, DRC/check/lint status, saved checkpoints,
and artifact paths. If a gate cannot pass, stop at the failing data, explain the next
repair step, and do not claim the design is complete. In recording/demo mode, also list
each stage image and label it as a **native EasyEDA screenshot** or a **data-rendered
diagram**, and explicitly report any frame that was stale or substituted.
