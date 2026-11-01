package protocol

import (
	"encoding/json"
	"testing"
)

func TestEveryActionDeclaresAccess(t *testing.T) {
	allowed := func(value string, values ...string) bool {
		for _, candidate := range values {
			if value == candidate {
				return true
			}
		}
		return false
	}
	for _, action := range AllActions() {
		t.Run(action.Name, func(t *testing.T) {
			a := action.Access
			if a.SchemaVersion != 1 || !allowed(a.Scope, "document", "project", "library", "workspace", "editor", "artifact", "system") ||
				!allowed(a.Effect, "read", "write", "view", "lifecycle", "export", "script") ||
				!allowed(a.Confirmation, "none", "design", "layout", "dedicated") ||
				!allowed(a.Verification, "none", "save-reload", "dedicated") || !allowed(a.Batch, "allowed", "isolated") {
				t.Fatalf("missing or invalid access declaration: %+v", a)
			}
			if a.Effect == "read" && action.Mutates || a.Effect == "write" && !action.Mutates {
				t.Fatalf("access effect contradicts mutates: %+v", action)
			}
			if a.Confirmation == "layout" && (a.Scope != "document" || a.Effect != "write" || a.Verification != "save-reload") {
				t.Fatalf("invalid layout grant: %+v", a)
			}
			if a.Effect == "write" && a.Confirmation == "none" {
				t.Fatal("writes must declare confirmation")
			}
			if a.Adapter != "" && (action.Name != "board.copy" || a.Adapter != "board-copy") {
				t.Fatal("unknown adapter")
			}
			encoded, err := json.Marshal(action)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ActionSpec
			if err = json.Unmarshal(encoded, &decoded); err != nil || decoded.Access != a {
				t.Fatal("access JSON roundtrip failed")
			}
		})
	}
}

func TestActionAccessDoesNotTreatViewAndLifecycleAsRead(t *testing.T) {
	byName := map[string]ActionAccess{}
	for _, action := range AllActions() {
		byName[action.Name] = action.Access
	}
	for _, name := range []string{"document.open", "document.close", "system.page_reload", "schematic.page.open", "library.cbb.open"} {
		if a := byName[name]; a.Effect != "lifecycle" || a.Confirmation != "dedicated" {
			t.Fatalf("%s must use lifecycle flow: %+v", name, a)
		}
	}
	if byName["library.symbol.get"] != accessLibraryRead {
		t.Fatal("symbol geometry must be readable")
	}
	if byName["pcb.route.auto"] != accessLayoutWrite {
		t.Fatal("routing must retain layout gate")
	}
	if byName["library.symbol.build"] != accessLibraryWrite {
		t.Fatal("library writes need asset confirmation")
	}
}
