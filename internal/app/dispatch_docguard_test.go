package app

import "testing"

// TestDocGuardTriggersOnMutation: the --doc guard must fire for mutating actions
// and stay out of the way for reads — and CRUCIALLY must NOT gate its own
// navigation tools (document.open/current), or it would recurse forever.
func TestDocGuardTriggersOnMutation(t *testing.T) {
	// A representative mutating action gates.
	if !actionMutates("schematic.component.place") {
		t.Error("schematic.component.place must be treated as mutating (guard would skip a real edit)")
	}
	// The guard's own tools are NON-mutating, so the guard skips them (no recursion).
	for _, a := range []string{"document.current", "document.open", "schematic.pages.list"} {
		if actionMutates(a) {
			t.Errorf("%s must be non-mutating so the --doc guard never recurses through it", a)
		}
	}
	// Belt-and-suspenders: they are also on the explicit exempt list.
	for _, a := range []string{"document.current", "document.open", "schematic.page.open"} {
		if !docGuardExempt[a] {
			t.Errorf("%s must be in docGuardExempt (recursion guard even if it flips to mutating)", a)
		}
	}
	// A read action is not gated.
	if actionMutates("schematic.components.list") {
		t.Error("schematic.components.list is a read — must not trigger the --doc guard")
	}
}
