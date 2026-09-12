package app

import (
	"testing"
	"time"
)

func TestSchematicIdentityReadTimeout(t *testing.T) {
	for _, tc := range []struct {
		action          string
		payload         any
		requested, want time.Duration
	}{
		{"schematic.components.list", map[string]any{"includeDeviceIdentity": true}, 20 * time.Second, 60 * time.Second},
		{"schematic.components.list", map[string]any{"includeDeviceIdentity": false}, 20 * time.Second, 20 * time.Second},
		{"schematic.components.list", nil, 20 * time.Second, 20 * time.Second},
		{"schematic.components.list", map[string]any{"includeDeviceIdentity": "true"}, 20 * time.Second, 20 * time.Second},
		{"schematic.component.place", map[string]any{"includeDeviceIdentity": true}, 20 * time.Second, 20 * time.Second},
		{"schematic.components.list", map[string]any{"includeDeviceIdentity": true}, 5 * time.Second, 5 * time.Second},
		{"schematic.components.list", map[string]any{"includeDeviceIdentity": true}, 90 * time.Second, 90 * time.Second},
	} {
		if got := schematicIdentityReadTimeout(tc.action, tc.payload, tc.requested); got != tc.want {
			t.Fatalf("%+v got %s", tc, got)
		}
	}
}
