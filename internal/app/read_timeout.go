package app

import (
	"encoding/json"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// These are connector-wait budgets, not the HTTP round trip. Keep the current
// wire contract (daemon subtracts response grace) explicit at this boundary.
const (
	documentOpenReadBudget = 30 * time.Second
	pourOrderReadBudget    = 60 * time.Second
)

// All CLI dispatch paths converge in postAction, including Apply and layout.
// A deliberately short diagnostic deadline remains short; legacy 90s pin reads
// get the same floor as reads that previously inherited the default 20s.
func effectiveReadTimeout(action string, payload any, requested time.Duration) time.Duration {
	if requested < defaultActionTimeout {
		return requested
	}
	budget := time.Duration(0)
	switch action {
	case "schematic.attribute.move":
		// Before and after may each need a bounded 15s native visibility read.
		budget = 45 * time.Second
	case "document.open", "schematic.page.open":
		budget = documentOpenReadBudget
	case "schematic.components.list":
		budget = protocol.SchematicComponentsReadBudget
	case "pcb.pour.create":
		var fields map[string]json.RawMessage
		data, err := json.Marshal(payload)
		if err == nil && json.Unmarshal(data, &fields) == nil && fields["priority"] != nil {
			// Four bounded source reads plus the awaited order setter/rebuild.
			budget = pourOrderReadBudget
		}
	}
	if budget > 0 && requested < budget+protocol.DispatchResponseGrace {
		requested = budget + protocol.DispatchResponseGrace
	}
	return schematicIdentityReadTimeout(action, payload, requested)
}
