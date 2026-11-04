package protocol

import (
	"testing"
	"time"
)

func TestSchematicGeometryOverhead(t *testing.T) {
	for _, tt := range []struct {
		name, action string
		budget, want time.Duration
	}{
		{"wire-default", "schematic.wire.create", 0, 42 * time.Second},
		{"wire-normal", "schematic.wire.create", 20 * time.Second, 42 * time.Second},
		{"wire-long", "schematic.wire.create", time.Minute, 42 * time.Second},
		{"connect", "schematic.power.connect_pin", time.Minute, 42 * time.Second},
		{"component", "schematic.component.modify", time.Minute, 40 * time.Second},
		{"marker-scene", "schematic.pin.repair_marker", time.Minute, 300 * time.Second},
		{"marker-diagnostic", "schematic.pin.repair_marker", 5 * time.Second, 10 * time.Second},
		{"read", "schematic.components.list", time.Minute, 0},
		{"short-diagnostic", "schematic.wire.create", DispatchResponseGrace, 0},
		{"bounded-wire", "schematic.wire.create", 5 * time.Second, 12 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := SchematicGeometryOverhead(tt.action, tt.budget); got != tt.want {
				t.Fatalf("overhead=%v want %v", got, tt.want)
			}
		})
	}
}
