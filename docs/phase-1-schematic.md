# Phase 1: Schematic Automation

Phase 1 makes schematic work reliable enough for an AI agent to inspect, modify, verify, and export a schematic through EasyEDA.

## Goals

- Discover Go daemon and EasyEDA connector health.
- Select an active EasyEDA window.
- Read current project, document, schematic, and schematic page context.
- List schematic pages.
- List schematic components and pins.
- Place library-backed schematic components.
- Modify component position and common properties.
- Delete components with confirmation.
- Create wires and net markers.
- Select and inspect primitives.
- Capture viewport snapshots.
- Run schematic DRC.
- Save schematic changes.
- Export schematic netlist and BOM artifacts.

## Out of Scope

- PCB layout editing.
- Footprint and symbol authoring.
- Full library CRUD beyond resolving component identities needed for placement.
- Gerber, STEP, pick-and-place, and PCB manufacturing exports.
- Full undo/redo or transaction support.
- DOM or browser UI automation as the default path.

## First Action Set

| Action | Mutates | Purpose |
| --- | --- | --- |
| `system.health` | no | Check daemon, connector, active window |
| `project.current` | no | Read current project |
| `document.current` | no | Read active editor document |
| `schematic.pages.list` | no | List schematic pages |
| `schematic.page.open` | no | Open/activate a schematic page |
| `schematic.components.list` | no | List placed components |
| `schematic.component.place` | yes | Place a library component |
| `schematic.component.modify` | yes | Modify component state |
| `schematic.component.delete` | yes | Delete components after confirmation |
| `schematic.wire.create` | yes | Create schematic wire |
| `schematic.netflag.create` | yes | Create power/ground/net port flags |
| `schematic.select` | no | Select primitives |
| `schematic.snapshot` | no | Capture current viewport image |
| `schematic.drc.check` | no | Run schematic DRC |
| `schematic.save` | yes | Save schematic |
| `schematic.export.netlist` | no | Export netlist artifact |
| `schematic.export.bom` | no | Export BOM artifact |
| `debug.exec_js` | maybe | Run raw `eda.*` JavaScript (confirm-gated escape hatch) |

`debug.exec_js` is the deliberate escape hatch for operations without a typed action. It is confirmation-gated and not for normal workflows; repeated snippets should be promoted to typed actions.

Run `easyeda actions` to print the current machine-readable version of this list.

## Definition of Done

Phase 1 is complete when a Skill can perform this loop:

1. Check connection and active context.
2. Inspect schematic components and wires.
3. Place or modify a small set of schematic primitives.
4. Verify by readback and snapshot.
5. Run schematic DRC.
6. Save changes after user confirmation.
7. Export BOM/netlist artifacts.

## Confirmation Rules

Always ask the user before:

- deleting primitives
- saving a mutated document when the request did not explicitly ask to save
- running a generated multi-step mutation plan
- replacing netlist data

Small additive operations such as placing one component or creating one wire may proceed when they are clearly requested.
