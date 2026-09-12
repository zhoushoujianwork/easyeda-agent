package app

import (
	"encoding/json"
	"time"
)

// Shared by CLI reads and Apply guards. Do not raise ordinary reads/writes or
// override a caller's explicitly different deadline.
func schematicIdentityReadTimeout(action string, payload any, timeout time.Duration) time.Duration {
	if action != "schematic.components.list" || timeout != defaultActionTimeout {
		return timeout
	}
	var requested struct {
		IncludeDeviceIdentity bool `json:"includeDeviceIdentity"`
	}
	raw, err := json.Marshal(payload)
	if err == nil && json.Unmarshal(raw, &requested) == nil && requested.IncludeDeviceIdentity {
		return 60 * time.Second
	}
	return timeout
}
