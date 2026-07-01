<p align="center">
  <img src="docs/assets/easyeda-agent-logo.png" width="96" alt="easyeda-agent logo" />
</p>

<h1 align="center">easyeda-agent</h1>

<p align="center">
  AI-native automation layer for EasyEDA.
</p>

<p align="center">
  <a href="https://github.com/zhoushoujianwork/easyeda-agent"><b>GitHub</b></a> ·
  <b>Plugin marketplace</b> <em>(coming soon)</em> ·
  <a href="README.zh-CN.md">中文</a>
</p>

![easyeda-agent workflow](docs/assets/easyeda-agent-workflow.svg)

`easyeda-agent` turns the official EasyEDA extension API into a typed, observable, Skill-friendly system. The EasyEDA plugin stays thin: it connects to the local agent and executes approved actions. The Go CLI/daemon owns protocol, state, artifacts, validation, and user-facing workflows.

## Why This Exists

The upstream `run-api-gateway` proves the important entry point: code can run inside EasyEDA with access to the official `eda` object. Its rough edge is that it exposes raw JavaScript execution as the main workflow. That is powerful, but brittle for agents.

The connector is real and working: it port-scans `49620-49629`, validates a handshake, self-heals its connection, and dispatches a typed action catalog to the official `eda.*` API. Raw JS survives only as the confirmation-gated `debug.exec_js` escape hatch. See [docs/FEATURES.md](docs/FEATURES.md) for the full feature/roadmap inventory.

This project moves the system into a better shape:

- Skill describes expert workflow and guardrails.
- Go CLI/daemon exposes stable typed actions.
- EasyEDA connector plugin only bridges to official `eda.*` APIs.
- Artifacts, screenshots, DRC results, and audit logs are first-class outputs.

## How It Works

`easyeda-agent` keeps the automation surface narrow and observable:

- A Skill or human runs an `easyeda` command.
- The Go CLI validates inputs and submits a typed action to the local daemon.
- The daemon tracks connected EasyEDA windows, routes each action over WebSocket, and records audit logs, artifacts, and validation results.
- The connector extension runs inside EasyEDA and calls the official `eda.*` API.
- Structured results flow back to the CLI and Skill, so the next step can be planned from real editor state.

The action catalog now spans schematic, PCB, document navigation, board binding, artifacts, and diagnostics. The current inventory and roadmap live in [docs/FEATURES.md](docs/FEATURES.md).

## Install Skills

Install the `easyeda` CLI/daemon first, then import the EasyEDA connector URL printed
by the installer:

```bash
curl -fsSL https://raw.githubusercontent.com/zhoushoujianwork/easyeda-agent/main/install.sh | sh
```

The published skill slug is `easyeda-agent` (suffix intentional: it distinguishes this
community automation layer from official EasyEDA tooling). To install only the skill
from a registry:

```bash
# ClawHub
clawhub install easyeda-agent

# 国内 SkillHub
skillhub install easyeda-agent --registry https://skillhub.cn
```

The old split skills (`easyeda-schematic`, `easyeda-pcb`, `easyeda-design-flow`,
`easyeda-conventions`) have been merged and removed from the repository.

## Demo Example

A board driven end-to-end through the typed-action + Skill workflow — placed
**entirely from real LCSC / 立创 library parts** (search → place by uuid → wire →
flag → DRC), not hand-drawn symbols. Layout follows the
[auto-layout SOP](skills/easyeda-agent/references/auto-layout-sop.md) distilled
from a 嘉立创 reference design: **flags only on power/ground rails; signals are real
local orthogonal wires; decoupling hugs each IC's VCC pad; multi-page by function.**

This is also the project's fixed end-to-end regression case — see
[docs/test-case-esp32-blink.md](docs/test-case-esp32-blink.md).

### ESP32-S3-WROOM-1 minimal system board

The board below was produced by the agent driving the full PCB flow — **auto-place →
outline-fit → rule-aware route → 4-layer power planes → collision-aware silk** — then
verified on the real EasyEDA canvas (DRC 31 → 3 violations, No-Connection → 0):

<p align="center">
  <img src="docs/assets/demo-esp32-board.png" width="560" alt="ESP32-S3 board the agent produced: 4-layer power planes, rounded outline, aligned designators" />
</p>

A few individual steps, each a real before/after on the same board:

| `pcb outline-fit` — tighten board to parts (17% → 71% utilization) | `pcb silk-align` — collision-aware designators |
|---|---|
| <img src="docs/assets/demo-outline-before.png" width="330" alt="before: oversized board outline"/> → <img src="docs/assets/demo-outline-after.png" width="330" alt="after: outline tightened to parts"/> | <img src="docs/assets/demo-silk-before.png" width="330" alt="before: scattered overlapping designators"/> → produces the aligned designators in the board above |

> A short screen-capture GIF of the end-to-end run will be added here. The images
> above are real `pcb snapshot` captures from the fixed regression board, not mockups.

## Repository Layout

```text
cmd/easyeda/                 CLI entrypoint used by humans and Skills
internal/app/                CLI command implementation
internal/daemon/             Local daemon: /health, /eda (connector WS), /action
internal/protocol/           Typed action protocol shared with connector (actions.go)
internal/version/            Build/version metadata
extension/                   EasyEDA connector (.eext) source + build (TypeScript → esbuild)
skills/easyeda-agent/        Merged public Skill: workflow, references, scripts, canonical data
docs/                        Architecture, protocol, features/roadmap, conventions, decisions
```

## Current Commands

```bash
go run ./cmd/easyeda version
go run ./cmd/easyeda actions
go run ./cmd/easyeda daemon start
go run ./cmd/easyeda daemon health
go run ./cmd/easyeda doc ls --project <name>
go run ./cmd/easyeda sch drc --project <name>
go run ./cmd/easyeda pcb drc --project <name>
go run ./cmd/easyeda board list --project <name>
go run ./cmd/easyeda call system.health
```

`daemon start` starts the local server. It binds the first free port in `127.0.0.1:49620-49629` and serves three endpoints, then runs until interrupted (Ctrl-C / SIGTERM):

- `GET /health` — service identity, version, and connected windows
- `GET /eda` — WebSocket the EasyEDA connector registers on (daemon sends a `handshake` on connect)
- `POST /action` — a typed action envelope to forward to a connected window

`daemon health` scans the same port range for an `easyeda-agent` daemon. With the daemon running it reports `status: found` and lists connected windows; otherwise a clean `not_found` result is expected.

`call <action>` finds the running daemon and posts a typed action to it. `system.health` is answered by the daemon itself (no connector required); window-scoped actions need a connected EasyEDA window and return `NO_CONNECTOR` until the connector extension is running.

Both sides of the action protocol are in place and working. The Go daemon owns the protocol, state, artifacts, and validation; the EasyEDA connector under `extension/` is a buildable `.eext` that dispatches typed actions to live `eda.*` calls (type-checked against `@jlceda/pro-api-types`). See [extension/README.md](extension/README.md).

## Capabilities

What the agent can drive today, via typed CLI subcommands (`easyeda <domain> <verb>`). Each is a typed action → connector → live `eda.*` call, verified on the fixed ESP32-S3 regression board.

**Schematic**
- Place real library/LCSC parts by uuid, then wire them (`sch` place/wire); power/ground **net-flags** via `connect_pin` (auto-compensates the rotation-store quirk).
- **DRC** (`sch drc`) + reconstructed per-item **design check** (`sch check` — floating pins, wire-crossing, wire-over-pin) + geometric **layout-lint** (overlap/spacing).
- Module-aware **auto-layout** (place → verify → adjust), one-call **`sch read`** (components + nets + floating pins + check), **BOM**/**netlist** export (BOM LCSC-enriched).

**PCB — placement**
- **`pcb auto-place`** — module-aware heuristic: satellites hug the chip pin they connect to, 2-pin parts re-oriented, multi-chip spread; **spacing is rule-aware** (derived from the live DRC clearance).
- **`pcb outline-fit`** (tighten board to parts) / **`pcb outline-round`** (rounded-rect board outline).
- **`pcb layout-lint`** — placement quality + **routability score** (ratsnest MST + cross-net crossings) *before* routing; gate-able.
- **`pcb silk-align`** — reposition designators with **collision avoidance** (no overlapping labels).
- **`pcb add-component`** — add one part to an existing PCB and net its pads (the working path around the broken incremental `import_changes`).

**PCB — routing & copper**
- **`pcb route-short`** — heuristic short-trace router: per-net MST, **rule-aware widths** (signal vs power), **obstacle-aware** L-orientation, **skips power/ground nets** (they belong in a pour).
- **`pcb pour`** (rule-aware copper-to-edge inset) / **`pcb pour-fit`** / **`pcb via-stitch`** / **`pcb rip-up`**.
- **`pcb power-planes`** — 4-layer power distribution: GND + power on **dedicated inner planes** + via-stitch each pad (drove the regression board's No-Connection to 0).
- **`pcb region`** (keep-out, incl. antenna no-copper) / **`pcb fill`** / **`pcb slot`** (挖槽 / board cutout on the MULTI layer).

**PCB — stackup, rules, fabrication**
- **`pcb stackup`** — set copper layer count (2/4/6…/32) + inner-layer type (signal↔plane/内电层).
- **Rule-aware everything** — the daemon reads the board's **live DRC rules** (`pcb drc-rules`) and conforms; falls back to a canonical **JLCPCB fab-rule reference** (real per-board-type exports). **`pcb drc`** runs the check.
- **`pcb export-dsn`** (Specctra DSN for external Freerouting, with keep-out injection) / **`pcb import-autoroute`** / **`pcb snapshot`**.

**Infrastructure**
- Typed action protocol (self-describing `--help`, `easyeda actions` catalog) with a `debug.exec_js` escape hatch for prototyping.
- Connector **auto-reconnect watchdog** (survives daemon restarts / window backgrounding) + daemon **debounced autosave**.

## Not Yet Supported / Platform Walls

Honest limits — some are our roadmap, some are hard `eda.*` API walls (no amount of connector work reaches them):

- **Maze-tier autorouting** (dense / any-distance / push-shove) — the daemon does *short, clear* heuristic routing only. Full routing is external **Freerouting** (the DSN round-trip building blocks exist); a turnkey integration is **deferred** (needs a Java runtime; waiting on the official EasyEDA autorouter maturing past `@alpha`).
- **Teardrops (泪滴)** — **platform wall**: `eda.*` exposes no create/apply-teardrop API (teardrops appear only as a manufacture-export object type). Apply by hand in the UI.
- **Controlled impedance / high-speed** — **platform wall**: stackup Er / dielectric height / copper weight aren't readable via `eda.*`, so trace-width-for-Z0 can't be computed; diff-pair / length-match constraint objects aren't exposed either.
- **Interactive routing menu** (single/multi/diff-pair *routing*, length-tuning/serpentine, fanout, remove-loops) — **no `eda.*` API**; UI-only.
- **No programmatic undo** — `eda.*` has no undo/redo; rollback is our own (data checkpoint + inverse ops).
- **Incremental `import_changes`** — a no-op for API-added parts (platform limit); place the whole circuit before the first import, or use `pcb add-component`.
- **Silkscreen density** — `silk-align` avoids label collisions where there's open space; a layout packed tighter than the labels can't be fully de-conflicted (it reports `unresolvedCollisions`) — loosen the placement.

See [`docs/FEATURES.md`](docs/FEATURES.md) for the full action inventory + status, and [`docs/ecosystem-survey.md`](docs/ecosystem-survey.md) for the `eda.*` API coverage map.

## Design Position

Raw JavaScript execution remains useful for debugging, but not as the primary AI surface. The default surface should be typed actions with explicit inputs, predictable outputs, artifact handling, and verification hooks.

See:

- [Feature inventory and roadmap](docs/FEATURES.md)
- [Architecture](docs/architecture.md)
- [Protocol](docs/protocol.md)
- [Skill design](docs/skill-design.md)
- [Historical Phase 1 schematic scope](docs/phase-1-schematic.md)
- [Historical Phase 2 PCB feasibility](docs/phase-2-pcb.md)

## Acknowledgments

Huge thanks to **嘉立创EDA / EasyEDA Pro (JLCPCB)** for opening up the extension
plugin channel and the official `eda.*` API. This entire automation layer is built
**on top of that open plugin platform** — it simply would not exist without it.
`easyeda-agent` stays a thin, well-behaved community citizen of the official plugin
system, and every capability here ultimately dispatches to JLC's own `eda.*` calls.
感谢嘉立创开放的 EDA 插件通道,让我们能做出这样一个好用的自动化插件。 🙏
