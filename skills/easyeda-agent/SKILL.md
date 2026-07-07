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
4. Confirm before destructive operations such as clear/delete/import bulk changes.
4a. `sch autoconnect` / `sch connect` are NOT idempotent — re-running the same spec
    on already-connected pins stacks a duplicate flag+wire rather than skipping or
    replacing. If you're unsure whether a batch connect landed, verify with a read
    (`sch read`/`sch list`), never by re-issuing the same connect call. If a
    connection came out wrong, delete that pin's existing flag+stub wire first,
    then reconnect it individually — do not rerun the whole spec.
5. For non-trivial boards, follow the gated flow: pre-analysis, sheet/page plan,
   module grouping, group placement, channel routing, DRC/check/layout-lint, adjust,
   save checkpoints.
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
- Schematic work: read `references/schematic.md` and `references/actions.md`.
- PCB work: read `references/pcb.md`.
- Schematic layout rules: read `references/schematic-layout-conventions.md`.
- PCB placement/routing rules: read `references/pcb-layout-conventions.md`.
- CLI placement/routing hard pits and auto-layout/autoconnect SOP: read
  `references/auto-layout-sop.md`.
- Part selection, JLC/LCSC ranking, and standardization: read
  `references/part-selection.md` and use `references/standard-parts.json`.
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
- `scripts/calibrate.js`: live bbox calibration for netflag/netport orientation after
  importing a new connector build.

## Deliverables

Summarize changed primitives, commands run, DRC/check/lint status, saved checkpoints,
and artifact paths. If a gate cannot pass, stop at the failing data, explain the next
repair step, and do not claim the design is complete. In recording/demo mode, also list
each stage image and label it as a **native EasyEDA screenshot** or a **data-rendered
diagram**, and explicitly report any frame that was stale or substituted.
