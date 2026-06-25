# schematic-lint — data-only schematic checker

Find layout/connectivity problems in a live EasyEDA schematic **from data, not
screenshots**. One `getAll` + `wire.getAll` pull returns the entire global
layout (≈600ms even for a 53-part board); everything else is local analysis.

```bash
make build
bin/easyeda daemon &                  # connector must be connected
tools/schematic-lint/lint.sh ceshi          # full lint (or DIFF if a baseline exists)
tools/schematic-lint/lint.sh ceshi --save   # full lint + record current as baseline
tools/schematic-lint/lint.sh ceshi --all    # force a full global report
```

After `--save`, later runs show only what changed (see **Diff-aware lint** below).

## Why data, not screenshots

`getCurrentRenderedAreaImage` is for final human eyeballing. For *finding*
problems it is slow and lossy. The connector can return the full primitive set
(components, pins, bboxes, netflags, netports, wires with coordinates) in a
single dispatch, and a geometry/union-find pass finds the issues deterministically
with exact coordinates. Screenshots are only used to confirm a fix at the end —
and for that, use `eda.dmt_EditorControl.zoomToAllPrimitives()` /
`zoomToSelectedPrimitives()` before snapshotting (NOT `navigateToRegion`, which
does not zoom the rendered-area image).

## Checks

| id | severity | what |
|---|---|---|
| `flag_on_pin` | 🔴 | netflag/netport at the exact pin coordinate → DRC fatal (EasyEDA needs a real wire, never overlap) |
| `zero_wire` | 🔴 | zero-length wire segment |
| `dangling_wire` | 🔴 | wire endpoint (degree 1) with no pin/flag — 空连 |
| `floating_pin` | 🟠 | pin with no wire and no flag |
| `single_pin_net` | 🟠 | a pin whose net has only itself and no power/ground/label |
| `flag_no_wire` | 🟠 | netflag/netport with no wire connected |
| `orientation` | 🟡 | flag rotation not 顺着导线 (body should point outward along the stub) |
| `bbox_overlap` | 🟠 | two parts whose pin-bboxes overlap |
| `dup_designator` | 🟠 | duplicate designator |
| `netport_hop` | 🟡 | same-net net-ports < 300u apart on one page (should be a wire/label) |
| `collinear_flags` | 🟡 | different-net flags collinear through a component (visual false-short) |
| `unnamed_net` | 🔵 | multi-pin signal net with no label/rail |
| `off_grid` | 🔵 | coordinate not on the 5-unit grid |

## How the orientation check works

The flag body must point outward along the stub direction. The whole rotation
table is **derived from four facts** — the `up → left → down → right` +90° cycle
and the body direction at rot 0 per family (power=up, ground=down, net_port=right).
Those four facts live in [`orientation.json`](orientation.json), the **single
source of truth**: `orient.py` derives the table for this linter, and the
connector's `connect_pin` ([actions.ts](../../extension/src/actions.ts)
`deriveBodyRotation()`) derives the *same* table for what it writes. They can't
drift — the harness asserts it. See
[docs/schematic-layout-conventions.md §3.5](../../docs/schematic-layout-conventions.md).

## Rule-trust harness — `make lint-test`

A data-driven linter is only as trustworthy as its rules; a wrong rule is itself
a bug. Two guards keep verdicts honest (run `make lint-test` or
`python3 tools/schematic-lint/tests/run.py`):

1. **Orientation consistency** — `orientation.json` must derive back to its own
   `frozenTable`, the +90° cycle law must hold, AND the connector's hand-written
   facts in [actions.ts](../../extension/src/actions.ts) (`ROTATION_CYCLE` +
   `BODY_ANCHOR_AT_ROT0`) must equal the spec — so the Python check and the TS
   writer can't silently diverge (a drift = connect_pin writes a rotation the
   linter then flags wrong). To re-validate against *live* ground truth, run
   [`calibrate.js`](calibrate.js) (READ-ONLY) via `debug.exec_js` against a
   connected window: for every real placed flag it checks the body points
   opposite its wire (顺着导线) and that `(family, body) → rotation` matches
   `orientation.json`. For **power/ground** (label-box symbols) a disagreement is
   a hard rule-bug signal — verified on ceshi: all 10 agree. For **net_port** (an
   ARROW symbol) the bbox center does NOT track the arrow direction, so calibrate
   reports a port disagreement as an informational **WARN**, never a hard bug.
   (The ESP32 reference showed 12 such port WARNs; the `port` row was then
   VISUALLY CONFIRMED correct — a `connect_pin` `direction=right` port renders
   pointing right — so those WARNs are bbox artifacts, not a table bug. The
   table/connect_pin were left unchanged.) Do NOT validate by *creating* a flag
   and reading its bbox immediately — a freshly-created isolated flag's bbox
   flips horizontally vs a settled on-canvas one; only real, wired, rendered
   flags are ground truth. And note `getCurrentRenderedAreaImage` can return a
   **stale/cached** render (doesn't follow zoom or reflect just-made edits), so
   don't trust a screenshot for confirmation without first proving it refreshed.
2. **Fixture goldens** — every layout under `tests/fixtures/` is linted and
   diffed against `tests/golden/`. `clean_board.json` MUST stay clean (the
   false-positive net); each bad fixture MUST still fire its rule. After an
   intentional rule change, re-freeze with `tests/run.py --update`.

> Notes: `createNetFlag`/`createNetPort` rotation is **identity** (read back ===
> written; no negation — an earlier "negation" finding was wrong). EasyEDA is
> **y-up** (+y renders upward), so `direction()` treats `dy>0` as up. Ground-truth
> a flag's body direction with `sch_Primitive.getPrimitivesBBox([pid])` — the bbox
> center's offset from the placement point is the body direction (pure data).

## Diff-aware lint (只看变更)

After `--save` records a baseline, the next `lint.sh` run diffs the fresh layout
against it and buckets every finding:

- **🔴 NEW** — problems this edit introduced. The only thing you must look at.
- **✅ FIXED** — problems this edit removed. Confirmation your change worked.
- **🔵 PRE-EXISTING** — untouched problems that were already there. Folded by
  default (`--all` to list them) — this is the "没动过的地方不用看" part.

It also lists the **changed primitives** (added / removed / moved / rotated /
rewired, by `PrimitiveId`) so you know which regions to eyeball. `diff.py` exits
non-zero when the edit introduced a NEW problem (handy in scripts).

**Why we don't skip rules by region.** Connectivity is global — a wire added in
one corner can merge two nets across the page, so "this primitive didn't change"
≠ "its verdict didn't change". We therefore always run *all* rules on the *full*
board (lint.py is ~milliseconds even on 53 parts) and diff the **output**. The
speed-up is for your attention, not the linter's CPU. `--all` forces the full
ungrouped report any time.

**Baseline store + git.** Snapshots live in
`${EASYEDA_LINT_DIR:-~/.easyeda-agent/lint}/<project>/` (next to the daemon's
audit log). The schematic lives in EasyEDA's webview, not on disk, so we version
the *layout snapshot*: every `--save` writes `snapshot.json` plus a timestamped
`history/` copy, and — if you ran `lint.sh <project> --init-git` once — commits
it, giving you `git log` / `git blame` over the schematic's state over time.

## Files

- `probe.js` — the one-shot data pull (runs via `debug.exec_js`)
- `diff.py` — baseline-vs-current diff (NEW / FIXED / PRE-EXISTING + changed primitives)
- `lint.py` — the analyzer (`lint.py <layout.json> [--json]`)
- `lint.sh` — resolves the live window, pulls, and lints/diffs/saves the baseline
- `orientation.json` — canonical orientation facts (single source of truth)
- `orient.py` — derives the body-rotation table from the spec
- `calibrate.js` — live bbox ground-truth check for the orientation anchors
- `tests/run.py` + `tests/fixtures/` + `tests/golden/` — the rule-trust harness

This is a candidate to promote into a typed `schematic.lint` action.
