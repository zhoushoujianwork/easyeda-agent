---
name: easyeda-schematic
description: EasyEDA schematic automation skill. Use when working with EasyEDA schematic pages through the easyeda-agent CLI or daemon, including reading schematic context, listing components, placing or modifying components, creating wires and net flags, selecting primitives, running schematic DRC, saving schematic changes, and exporting BOM or netlist artifacts.
---

# EasyEDA Schematic

Use `easyeda-agent` typed actions. Do not write raw EasyEDA JavaScript unless a typed action is missing and the user explicitly accepts a debug path.

## Workflow

1. Run `easyeda health`.
2. Read active project and schematic context.
3. Inspect before mutating.
4. Prefer small additive operations.
5. Verify each mutation by readback, snapshot, or DRC.
6. Ask before destructive operations, multi-step mutation plans, or saving.
7. Summarize changed primitives, warnings, and artifacts.

## Phase 1 Actions

Run `easyeda actions` for the current machine-readable action list.

Core schematic operations:

- `project.current`
- `document.current`
- `schematic.pages.list`
- `schematic.page.open`
- `schematic.components.list`
- `schematic.component.place`
- `schematic.component.modify`
- `schematic.component.delete`
- `schematic.wire.create`
- `schematic.netflag.create`
- `schematic.select`
- `schematic.snapshot`
- `schematic.drc.check`
- `schematic.save`
- `schematic.export.netlist`
- `schematic.export.bom`

## Guardrails

- Confirm before deleting primitives.
- Confirm before saving unless the user explicitly asked to save.
- Confirm before running a generated multi-step mutation plan.
- Do not claim completion after mutation until verification succeeds or the remaining risk is stated.
- Treat `File` and `Blob` outputs as artifacts.
- If DRC fails, report violations and propose the smallest repair step.

## EasyEDA Electrical Rules (load-bearing — DRC will fatal if ignored)

EasyEDA's DRC does **not** treat two primitives sharing the same coordinate as electrically connected. Every connection needs a real `schematic.wire.create` between them. Two concrete consequences:

1. **`schematic.netflag.create` MUST NOT be placed on the same point as a pin.** Placing a +3V3/GND/IN/OUT flag at the exact pin coordinate produces a DRC fatal: *"端点重叠且未连接 / endpoints overlap but not connected"*. The flag sits on top of the pin visually but EasyEDA treats them as two disjoint endpoints.

   Correct pattern: pin → short wire → netflag at the wire's far end. Typical offset: 20 grid units (EasyEDA uses 0.01 inch / grid unit on schematics). Example for `+3V3` on `R1.pin1 @(265, 440)`:

   ```text
   schematic.wire.create     points = [265,440, 245,440]   # pin to a free point
   schematic.netflag.create  x = 245, y = 440, kind=power, net="+3V3"
   ```

2. **Wires must have non-zero length.** A wire of `[x,y, x,y]` is silently ignored; a wire of `[x,y, x+0,y+0]` will not register a connection.

3. **NC pins still need explicit marking.** A pin without any wire/flag triggers a "悬空 / floating" warning even if your design intends it unused. Use a Non-Connected flag for those.

Apply this rule when generating any power/ground/port connection — emit the wire first, then place the flag at the wire's free endpoint.

## Missing Actions

When a needed operation has no typed action:

1. Decompose it into existing actions if possible.
2. Otherwise state the missing action name and expected inputs/outputs.
3. Use `debug.exec_js` (raw `eda.*` JavaScript) only as a temporary, user-confirmed debug escape hatch. Its result must be JSON-serializable — base64-encode any `Blob`/`File` inside the snippet.
4. Recommend promoting repeated debug code into a typed action.
