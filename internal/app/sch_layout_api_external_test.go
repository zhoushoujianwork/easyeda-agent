package app_test

import (
	"github.com/zhoushoujianwork/easyeda-agent/internal/app"
	"testing"
)

// Compile and call from outside package app: no private Lib types are needed.
func TestPublicSchematicLayoutAPI(t *testing.T) {
	in := app.SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: "one", Components: []app.SchematicLayoutComponent{{ID: "one", Measurement: app.SchematicPlacement{Designator: "X1", BBox: app.SchematicBox{MinX: -10, MinY: -10, MaxX: 10, MaxY: 10}, Pins: []app.SchematicPin{{Number: "1", X: 20, Y: 0}}}, PinStates: map[string]string{"1": "nc"}}}}
	out, err := app.PlanSchematicLayout(in)
	if err != nil || len(out.Placements) != 1 || out.PinStates["one"]["1"] != "nc" {
		t.Fatalf("standalone API failed: %v", err)
	}
}
