package protocol

import "testing"

func TestProjectDeletionAndPreviewHaveSeparateMutationContracts(t *testing.T) {
	found := 0
	for _, a := range AllActions() {
		if a.Name != "project.delete" && a.Name != "project.delete_preview" {
			continue
		}
		found++
		if !a.NeedsWindow || a.Domain != DomainProject {
			t.Fatal(a)
		}
		if a.Name == "project.delete" && (!a.Mutates || !a.NeedsConfirm) {
			t.Fatal("delete lacks mutation/confirmation classification")
		}
		if a.Name == "project.delete_preview" && (a.Mutates || a.NeedsConfirm) {
			t.Fatal("preview is not a read")
		}
	}
	if found != 2 {
		t.Fatalf("registered %d deletion actions", found)
	}
}
