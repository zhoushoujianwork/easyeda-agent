package app

import (
	"strings"
	"testing"
)

// trackLockJS is a pure JS-string builder (dispatched via debug.exec_js), so it is
// unit-testable without a connector: assert the scope (nets/ids/all), the lock
// direction, and the fill inclusion are correctly embedded in the generated body.
func TestTrackLockJS(t *testing.T) {
	t.Run("by-net lowercases and locks", func(t *testing.T) {
		js := trackLockJS([]string{"5V", " USB_DP "}, nil, false, false, true)
		for _, want := range []string{
			`const NETS = new Set(["5v","usb_dp"])`, // lowercased + trimmed
			`const IDS = new Set([])`,
			`const ALL = false`,
			`const LOCK = true`,      // lock (not unlock)
			`const INCLUDE_FILLS = true`,
			`eda.pcb_PrimitiveLine.modify`,
			`eda.pcb_PrimitiveVia.modify`,
			`eda.pcb_PrimitiveFill.modify`,
		} {
			if !strings.Contains(js, want) {
				t.Errorf("by-net JS missing %q\n---\n%s", want, js)
			}
		}
	})

	t.Run("unlock flips LOCK to false", func(t *testing.T) {
		js := trackLockJS(nil, []string{"id1", "id2"}, false, true, true)
		if !strings.Contains(js, `const IDS = new Set(["id1","id2"])`) {
			t.Errorf("by-id JS missing ids set\n%s", js)
		}
		if !strings.Contains(js, `const LOCK = false`) {
			t.Errorf("--unlock should set LOCK=false\n%s", js)
		}
	})

	t.Run("all-with-no-fills", func(t *testing.T) {
		js := trackLockJS(nil, nil, true, false, false)
		if !strings.Contains(js, `const ALL = true`) {
			t.Errorf("--all should set ALL=true\n%s", js)
		}
		if !strings.Contains(js, `const INCLUDE_FILLS = false`) {
			t.Errorf("--no-fills should set INCLUDE_FILLS=false\n%s", js)
		}
	})
}
