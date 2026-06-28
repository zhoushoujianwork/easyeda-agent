package daemon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAutosaver_CoalescesBurst(t *testing.T) {
	var calls atomic.Int32
	var gotWindow, gotAction string
	var mu sync.Mutex
	a := newAutosaver(40*time.Millisecond, func(windowID, saveAction string) {
		calls.Add(1)
		mu.Lock()
		gotWindow, gotAction = windowID, saveAction
		mu.Unlock()
	})

	// A burst of 5 edits within the debounce window must collapse to ONE save.
	for range 5 {
		a.schedule("win-1", "schematic.save")
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(120 * time.Millisecond)

	if n := calls.Load(); n != 1 {
		t.Fatalf("expected exactly 1 coalesced save, got %d", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotWindow != "win-1" || gotAction != "schematic.save" {
		t.Errorf("save called with (%q,%q), want (win-1, schematic.save)", gotWindow, gotAction)
	}
}

func TestAutosaver_PerWindowIndependent(t *testing.T) {
	var calls atomic.Int32
	a := newAutosaver(30*time.Millisecond, func(_, _ string) { calls.Add(1) })
	a.schedule("win-1", "schematic.save")
	a.schedule("win-2", "schematic.save")
	time.Sleep(90 * time.Millisecond)
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected one save per window (2), got %d", n)
	}
}

func TestAutosaver_StopCancelsPending(t *testing.T) {
	var calls atomic.Int32
	a := newAutosaver(40*time.Millisecond, func(_, _ string) { calls.Add(1) })
	a.schedule("win-1", "schematic.save")
	a.stop() // cancel before it fires
	time.Sleep(80 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("stop() must cancel pending save, got %d calls", n)
	}
}

func TestAutosaver_DisabledIsNoop(t *testing.T) {
	var calls atomic.Int32
	a := newAutosaver(0, func(_, _ string) { calls.Add(1) }) // 0 = disabled
	a.schedule("win-1", "schematic.save")
	time.Sleep(40 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("zero-debounce autosaver must be a no-op, got %d calls", n)
	}
}

func TestSaveActionForDocType(t *testing.T) {
	if got := saveActionForDocType("schematic"); got != "schematic.save" {
		t.Errorf("schematic → %q, want schematic.save", got)
	}
	if got := saveActionForDocType("pcb"); got != "pcb.save" {
		t.Errorf("pcb → %q, want pcb.save", got)
	}
	if got := saveActionForDocType(""); got != "" {
		t.Errorf("unknown docType → want \"\", got %q", got)
	}
}

func TestMutatesActionMap(t *testing.T) {
	// Sanity: a known mutating action and a known read action are classified right.
	if !mutatesAction["schematic.component.place"] {
		t.Error("schematic.component.place should be a mutating action")
	}
	if mutatesAction["schematic.components.list"] {
		t.Error("schematic.components.list should NOT be a mutating action")
	}
	// schematic.save is itself Mutates=true — maybeAutosave must exclude it to
	// avoid recursion; that exclusion is asserted by the action==saveAction guard.
	if !mutatesAction["schematic.save"] {
		t.Error("schematic.save is expected to be Mutates=true (the recursion trap)")
	}
}
