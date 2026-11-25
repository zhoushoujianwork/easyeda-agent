package protocol

import (
	"strings"
	"testing"
)

func TestPhase1ActionsHaveStableNames(t *testing.T) {
	actions := AllActions()
	if len(actions) == 0 {
		t.Fatal("expected actions")
	}

	seen := map[string]bool{}
	for _, action := range actions {
		if action.Name == "" {
			t.Fatalf("action has empty name: %#v", action)
		}
		if seen[action.Name] {
			t.Fatalf("duplicate action name: %s", action.Name)
		}
		seen[action.Name] = true
		if action.Phase < 1 {
			t.Fatalf("action %s has invalid phase %d", action.Name, action.Phase)
		}
	}

	for _, required := range []string{
		"system.health",
		"schematic.components.list",
		"schematic.component.place",
		"schematic.wire.create",
		"schematic.drc.check",
		"schematic.export.bom",
	} {
		if !seen[required] {
			t.Fatalf("missing required action: %s", required)
		}
	}
}

func TestProjectFindIsReadOnlyAndDocumentsScopedAbsence(t *testing.T) {
	for _, action := range AllActions() {
		if action.Name != "project.find" {
			continue
		}
		if action.Mutates || !action.NeedsWindow || action.Domain != DomainProject {
			t.Fatalf("project.find must be a window-scoped read: %+v", action)
		}
		if !strings.Contains(action.Description, "root folder") || !strings.Contains(action.Description, "Empty") {
			t.Fatalf("project.find scope/completeness contract missing: %s", action.Description)
		}
		return
	}
	t.Fatal("project.find action missing")
}

func TestConnectPinActionDocumentsYUpContract(t *testing.T) {
	var description string
	for _, action := range AllActions() {
		if action.Name == "schematic.power.connect_pin" {
			description = action.Description
			break
		}
	}
	if description == "" {
		t.Fatal("schematic.power.connect_pin action missing")
	}
	for _, want := range []string{"y-UP", "up moves the endpoint to a larger y", "down to a smaller y"} {
		if !strings.Contains(description, want) {
			t.Errorf("connect_pin description missing %q: %s", want, description)
		}
	}
	if strings.Contains(description, "y-DOWN") {
		t.Errorf("connect_pin description still advertises y-DOWN: %s", description)
	}
}

func TestComponentsListDocumentsReadOnlyPreflightContract(t *testing.T) {
	var spec *ActionSpec
	for _, action := range AllActions() {
		if action.Name == "schematic.components.list" {
			copy := action
			spec = &copy
			break
		}
	}
	if spec == nil {
		t.Fatal("schematic.components.list action missing")
	}
	if spec.Mutates {
		t.Fatal("schematic.components.list must remain read-only (Mutates=false)")
	}
	text := strings.Join(append(append([]string{spec.Description}, spec.Inputs...), spec.Outputs...), " ")
	for _, want := range []string{
		"includeConnectivitySummary",
		"active page",
		"wires",
		"buses",
		"netflags",
		"netports",
		"netlabels",
		"pinsAvailable",
		"pinsError",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("components.list contract missing %q: %s", want, text)
		}
	}
}

func TestDesignatorGeometryActionIsReadOnlyAndScoped(t *testing.T) {
	for _, action := range AllActions() {
		if action.Name != "schematic.designators.list" {
			continue
		}
		if action.Mutates || !action.NeedsWindow || action.Domain != DomainSchematic {
			t.Fatalf("Designator geometry must be a current-page read-only schematic action: %+v", action)
		}
		for _, phrase := range []string{"active schematic page", "getAll(parentId)", "getPrimitivesBBox", "missing/duplicate/hidden/invalid"} {
			if !strings.Contains(action.Description, phrase) {
				t.Fatalf("Designator action contract missing %q: %s", phrase, action.Description)
			}
		}
		return
	}
	t.Fatal("schematic.designators.list action missing")
}

func TestProtectedPinRepairIsCataloguedAsMutatingGeometryAction(t *testing.T) {
	var found *ActionSpec
	for _, action := range AllActions() {
		if action.Name == "schematic.pin.repair_marker" {
			copy := action
			found = &copy
			break
		}
	}
	if found == nil || !found.Mutates || !found.NeedsWindow || found.Domain != DomainSchematic {
		t.Fatalf("protected repair catalog contract missing: %+v", found)
	}
	if !SchematicGeometryGuarded(found.Name) {
		t.Fatal("protected repair lacks CLI/daemon geometry timeout sizing")
	}
}

func TestPcbOutlineAndOriginContracts(t *testing.T) {
	byName := map[string]ActionSpec{}
	for _, action := range AllActions() {
		byName[action.Name] = action
	}

	outline, ok := byName["pcb.outline.set"]
	if !ok || !outline.Mutates || outline.Domain != DomainPcb {
		t.Fatalf("pcb.outline.set catalog contract missing: %+v", outline)
	}
	outlineText := strings.Join(append(append([]string{outline.Description}, outline.Inputs...), outline.Outputs...), " ")
	for _, want := range []string{"ARC", "source", "lineWidth", "locked", "radius"} {
		if !strings.Contains(outlineText, want) {
			t.Errorf("pcb.outline.set contract missing %q: %s", want, outlineText)
		}
	}

	for _, name := range []string{"pcb.origin.get", "pcb.origin.set"} {
		action, ok := byName[name]
		if !ok || action.Domain != DomainPcb || !action.NeedsWindow {
			t.Fatalf("%s catalog contract missing: %+v", name, action)
		}
		if got, want := action.Mutates, name == "pcb.origin.set"; got != want {
			t.Errorf("%s Mutates=%v, want %v (set persists metadata; get is read-only)", name, got, want)
		}
		text := strings.Join(append(append([]string{action.Description}, action.Inputs...), action.Outputs...), " ")
		for _, want := range []string{"offsetX", "offsetY", "move"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s contract missing %q: %s", name, want, text)
			}
		}
	}
}

func TestProjectTransferCatalog(t *testing.T) {
	found := map[string]ActionSpec{}
	for _, a := range AllActions() {
		if a.Name == "project.open" || a.Name == "project.export" {
			found[a.Name] = a
		}
	}
	if len(found) != 2 {
		t.Fatal("missing project transfer actions")
	}
	for _, a := range found {
		if a.Domain != DomainProject || !a.NeedsWindow {
			t.Fatalf("bad routing: %+v", a)
		}
	}
	if !found["project.open"].NeedsConfirm || !found["project.open"].Mutates || found["project.export"].Mutates {
		t.Fatal("incorrect side-effect metadata")
	}
}

func TestPcbSilkDeleteIsCataloguedAsVerifiedDesignWrite(t *testing.T) {
	var found *ActionSpec
	for _, action := range AllActions() {
		if action.Name == "pcb.silk.delete" {
			copy := action
			found = &copy
		}
	}
	if found == nil {
		t.Fatal("pcb.silk.delete missing from the catalog")
	}
	if found.Access != accessDesignWrite || found.Domain != DomainPcb || !found.Mutates || !found.NeedsWindow || found.Phase != 2 {
		t.Fatalf("pcb.silk.delete catalog contract wrong: %+v", found)
	}
	if strings.Join(found.Inputs, " ") == "" || !strings.Contains(found.Inputs[0], "primitiveId") {
		t.Fatalf("pcb.silk.delete inputs: %v", found.Inputs)
	}
	if got := strings.Join(found.Outputs, ","); got != "primitiveId,text,layer,deleted,verified,partial" {
		t.Fatalf("pcb.silk.delete outputs: %s", got)
	}
	if len(found.VerifyWith) != 1 || found.VerifyWith[0] != "pcb.silk.list" {
		t.Fatalf("pcb.silk.delete verifyWith: %v", found.VerifyWith)
	}
	for _, phrase := range []string{"ONE", "ATTRIBUTE", "locked", "fresh", "partial"} {
		if !strings.Contains(found.Description, phrase) {
			t.Errorf("pcb.silk.delete description missing %q: %s", phrase, found.Description)
		}
	}
}
