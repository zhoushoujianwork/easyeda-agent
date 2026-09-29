package app

import (
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func peripheralDirectFixture() (*SchematicLayoutResult, []connectivity.Module) {
	layout := &SchematicLayoutResult{ComponentIDs: map[string]string{"U1": "core", "R1": "pullup", "C3": "filter"},
		Placements: []SchematicPlacement{
			{Designator: "U1", Pins: []SchematicPin{{Number: "EN", Net: "EN", X: 0, Y: 0}}},
			{Designator: "R1", Pins: []SchematicPin{{Number: "1", Net: "EN", X: 100, Y: 0}}},
			{Designator: "C3", Pins: []SchematicPin{{Number: "1", Net: "EN", X: 200, Y: 0}}},
		},
		Flags: []SchematicMarker{
			{Net: "EN", Kind: "net_port_bi", PinX: 0, PinY: 0, Direction: "up", Offset: 10},
			{Net: "EN", Kind: "net_port_bi", PinX: 100, PinY: 0, Direction: "up", Offset: 10},
			{Net: "EN", Kind: "net_port_bi", PinX: 200, PinY: 0, Direction: "up", Offset: 10},
		}}
	return layout, []connectivity.Module{{ID: "MCU", CoreComponents: []string{"core"}, PeripheralComponents: []string{"pullup", "filter"}}}
}

func TestPeripheralDirectRejectsNamedIslandsIncludingThirdComponent(t *testing.T) {
	layout, modules := peripheralDirectFixture()
	if err := ValidateSchematicPeripheralDirect(layout, modules); err == nil || !strings.Contains(err.Error(), "peripheral-direct-missing") || !strings.Contains(err.Error(), "C3") || !strings.Contains(err.Error(), "R1") {
		t.Fatalf("same net including a third component excused disconnected peripherals: %v", err)
	}
	// A real R/C island still does not attach the cluster to its functional core.
	layout.Wires = []SchematicWire{{Net: "EN", Points: [][2]float64{{100, 0}, {200, 0}}}}
	if err := ValidateSchematicPeripheralDirect(layout, modules); err == nil {
		t.Fatal("isolated peripheral cluster passed")
	}
	layout.Wires = append(layout.Wires, SchematicWire{Net: "EN", Points: [][2]float64{{0, 0}, {100, 0}}})
	if err := ValidateSchematicPeripheralDirect(layout, modules); err != nil {
		t.Fatal(err)
	}
}

func TestPeripheralDirectAcceptsSeriesDependencyNotElectricalBodyShort(t *testing.T) {
	layout, modules := peripheralDirectFixture()
	layout.Placements[1].Pins = append(layout.Placements[1].Pins, SchematicPin{Number: "2", Net: "FILTER", X: 120})
	layout.Placements[2].Pins[0].Net = "FILTER"
	layout.Flags = nil
	layout.Wires = []SchematicWire{
		{Net: "EN", Points: [][2]float64{{0, 0}, {100, 0}}},
		{Net: "FILTER", Points: [][2]float64{{120, 0}, {200, 0}}},
	}
	if err := ValidateSchematicPeripheralDirect(layout, modules); err != nil {
		t.Fatal(err)
	}
}

func TestPeripheralDirectCannotFollowForeignModuleCore(t *testing.T) {
	layout, modules := peripheralDirectFixture()
	modules[0].PeripheralComponents = []string{"pullup"}
	modules = append(modules, connectivity.Module{ID: "other", CoreComponents: []string{"filter"}})
	layout.Wires = []SchematicWire{{Net: "EN", Points: [][2]float64{{100, 0}, {200, 0}}}}
	if err := ValidateSchematicPeripheralDirect(layout, modules); err == nil || !strings.Contains(err.Error(), "R1") {
		t.Fatalf("foreign owner substituted for declared MCU core: %v", err)
	}
}

func TestPeripheralDirectOwnershipFailsClosed(t *testing.T) {
	for _, mode := range []string{"missing", "missing-core", "unowned", "duplicate", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			layout, modules := peripheralDirectFixture()
			switch mode {
			case "missing":
				modules = nil
			case "missing-core":
				modules[0].CoreComponents = nil
			case "unowned":
				modules[0].PeripheralComponents = []string{"pullup"}
			case "duplicate":
				modules[0].PeripheralComponents = append(modules[0].PeripheralComponents, "core")
			case "unknown":
				modules[0].CoreComponents = []string{"missing"}
			}
			if err := ValidateSchematicPeripheralDirect(layout, modules); err == nil || !strings.Contains(err.Error(), "peripheral-ownership-incomplete") {
				t.Fatal(err)
			}
		})
	}
}

func TestPeripheralDirectOverlappingPinsAreNotWire(t *testing.T) {
	layout, modules := peripheralDirectFixture()
	layout.Flags = nil
	for i := range layout.Placements {
		layout.Placements[i].Pins[0].X = 0
	}
	if err := ValidateSchematicPeripheralDirect(layout, modules); err == nil {
		t.Fatal("overlapping pins with no real wire passed")
	}
}

func TestComposePeripheralDirectRejectsCompleteButLabelOnlyDrawing(t *testing.T) {
	src := composeOriginalRefsFixture([]string{"U1", "R1"})
	src.Connectivity.Modules[0].CoreComponents = []string{src.Connectivity.Components[0].ID}
	src.Connectivity.Modules[0].PeripheralComponents = []string{src.Connectivity.Components[1].ID}
	// This input has complete identities, pins, named wire trees and geometry.
	// Its missing ownership edge used to pass compose and compile an Apply.
	if _, err := planSchComposition(src); err == nil || !strings.Contains(err.Error(), "peripheral-direct-missing") {
		t.Fatalf("compose accepted a label-only dedicated peripheral: %v", err)
	}
}

func TestComposeExplicitNetLabelsKeepsNamedLeadsAndOwnership(t *testing.T) {
	src := composeOriginalRefsFixture([]string{"U1", "R1"})
	src.Connectivity.Modules[0].CoreComponents = []string{src.Connectivity.Components[0].ID}
	src.Connectivity.Modules[0].PeripheralComponents = []string{src.Connectivity.Components[1].ID}
	src.ConnectionMode = "net-labels"
	plan, err := planSchComposition(src)
	if err != nil || plan.ConnectionMode != "net-labels" {
		t.Fatalf("explicit labelled composition rejected: %v", err)
	}
	if want := schCompositionExpectation(plan, true).Ownership.ConnectionMode; want != "net-labels" {
		t.Fatalf("Apply guard lost connection mode: %q", want)
	}
	src.Modules[0].Flags = src.Modules[0].Flags[:len(src.Modules[0].Flags)-1]
	if _, err := planSchComposition(src); err == nil || !strings.Contains(err.Error(), "named wire tree") {
		t.Fatalf("missing label lead passed: %v", err)
	}
	src.ConnectionMode = "other"
	if _, err := planSchComposition(src); err == nil || !strings.Contains(err.Error(), "connectionMode") {
		t.Fatalf("unknown connection mode passed: %v", err)
	}
}

func TestPeripheralDirectRailAttachmentDoesNotExcuseSplitEnablePins(t *testing.T) {
	layout, modules := peripheralDirectFixture()
	// The entire cluster is physically attached to U1 on +3V3, but every EN
	// endpoint is a separate named stub. Ownership reachability alone would pass.
	for i := range layout.Placements {
		layout.Placements[i].Pins = append(layout.Placements[i].Pins,
			SchematicPin{Number: "VCC", Net: "+3V3", X: float64(i) * 100, Y: -100})
	}
	layout.Wires = []SchematicWire{{Net: "+3V3", Points: [][2]float64{{0, -100}, {200, -100}}}}
	if err := ValidateSchematicPeripheralDirect(layout, modules); err == nil || !strings.Contains(err.Error(), "dedicated signal EN") {
		t.Fatalf("supply wire masked split EN branches: %v", err)
	}
	layout.Wires = append(layout.Wires, SchematicWire{Net: "EN", Points: [][2]float64{{0, 0}, {200, 0}}})
	if err := ValidateSchematicPeripheralDirect(layout, modules); err != nil {
		t.Fatal(err)
	}
}

func TestPeripheralDirectGroundCannotSubstituteForDecouplingSupply(t *testing.T) {
	layout, modules := peripheralDirectFixture()
	layout.Flags = nil
	for i := range layout.Placements {
		layout.Placements[i].Pins = []SchematicPin{
			{Number: "1", Net: "GND", X: float64(i) * 100},
			{Number: "2", Net: "+3V3", X: float64(i) * 100, Y: -100},
		}
	}
	layout.Wires = []SchematicWire{{Net: "GND", Points: [][2]float64{{0, 0}, {200, 0}}}}
	if err := ValidateSchematicPeripheralDirect(layout, modules); err == nil || !strings.Contains(err.Error(), "non-ground") {
		t.Fatalf("common GND hid missing dedicated supply wiring: %v", err)
	}
	layout.Wires = append(layout.Wires, SchematicWire{Net: "+3V3", Points: [][2]float64{{0, -100}, {200, -100}}})
	if err := ValidateSchematicPeripheralDirect(layout, modules); err != nil {
		t.Fatal(err)
	}
}

func TestPeripheralDirectExplicitNetRoleOverridesConventionalName(t *testing.T) {
	layout, modules := peripheralDirectFixture()
	for i := range layout.Placements {
		layout.Placements[i].Pins[0].Net = "VBUS"
		layout.Flags[i].Net = "VBUS"
		layout.Placements[i].Pins = append(layout.Placements[i].Pins,
			SchematicPin{Number: "supply", Net: "CUSTOM_RAIL", X: float64(i) * 100, Y: -100})
	}
	layout.Wires = []SchematicWire{{Net: "CUSTOM_RAIL", Points: [][2]float64{{0, -100}, {200, -100}}}}
	roles := map[string]string{"VBUS": "signal", "CUSTOM_RAIL": "power"}
	if err := ValidateSchematicPeripheralDirect(layout, modules, roles); err == nil || !strings.Contains(err.Error(), "dedicated signal VBUS") {
		t.Fatalf("conventional power-looking name overrode explicit signal role: %v", err)
	}
	roles["VBUS"] = "power"
	if err := ValidateSchematicPeripheralDirect(layout, modules, roles); err != nil {
		t.Fatal(err)
	}
}

func TestPeripheralSignalPolicyBecomesMandatoryWithinOwnedModule(t *testing.T) {
	layout, _ := peripheralDirectFixture()
	input := SchematicLayoutInput{CoreComponentID: "core", NetPolicies: map[string]string{"EN": "module_port"}}
	for _, c := range layout.Placements {
		input.Components = append(input.Components, SchematicLayoutComponent{ID: layout.ComponentIDs[c.Designator], Measurement: c})
	}
	roles := schematicMandatoryPeripheralSignalPolicies(&input)
	if input.NetPolicies["EN"] != "direct" || roles["EN"] != "signal" {
		t.Fatal("cross-module port policy could still split the local dedicated branch")
	}
}
