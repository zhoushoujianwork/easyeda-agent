package protocol

import "testing"

func TestSchematicTextCreateAccess(t *testing.T) {
	for _, action := range AllActions() {
		if action.Name != "schematic.text.create" {
			continue
		}
		if action.Access != accessDesignWrite || !action.Mutates || !action.NeedsWindow || action.Domain != DomainSchematic {
			t.Fatalf("incorrect text create access: %+v", action)
		}
		return
	}
	t.Fatal("schematic.text.create not registered")
}
