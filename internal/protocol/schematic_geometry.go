package protocol

import "time"

const SchematicGeometryReadBudget = 20 * time.Second
const SchematicWirePublicationBudget = 2 * time.Second

// SchematicGeometryOverhead keeps CLI and daemon end-to-end budgets aligned.
// Short diagnostic deadlines do not acquire a publication-wait allowance.
func SchematicGeometryOverhead(action string, actionBudget time.Duration) time.Duration {
	if !SchematicGeometryGuarded(action) {
		return 0
	}
	readBudget := SchematicGeometryReadTimeout(actionBudget)
	overhead := 2 * readBudget
	if readBudget > 0 && (action == "schematic.wire.create" || action == "schematic.power.connect_pin") {
		overhead += SchematicWirePublicationBudget
	}
	return overhead
}

// Honor deliberately short caller budgets (including diagnostic deadlines).
func SchematicGeometryReadTimeout(actionBudget time.Duration) time.Duration {
	// A deadline shorter than the response grace is an end-to-end probe, not a
	// request for extra execution time. Preserve its explicit fail-fast contract.
	if actionBudget > 0 && actionBudget <= DispatchResponseGrace {
		return 0
	}
	if actionBudget > 0 && actionBudget < SchematicGeometryReadBudget {
		return actionBudget
	}
	return SchematicGeometryReadBudget
}

// SchematicGeometryGuarded is shared by daemon enforcement and CLI timeout
// sizing. No caller payload or force flag can disable it.
func SchematicGeometryGuarded(action string) bool {
	switch action {
	case "schematic.wire.create", "schematic.power.connect_pin",
		"schematic.pin.repair_marker",
		"schematic.component.place", "schematic.component.modify",
		"schematic.component.replace", "schematic.group.move", "schematic.rebind.symbol", "schematic.rebind.footprint":
		return true
	}
	return false
}
