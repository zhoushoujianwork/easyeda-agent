package daemon

import (
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestRequestTimeout(t *testing.T) {
	cases := []struct {
		name      string
		timeoutMs int
		want      time.Duration
	}{
		{"default when unset", 0, dispatchTimeout},
		{"default when negative", -5, dispatchTimeout},
		{"caller budget minus grace", 20000, 18 * time.Second},
		{"clamped to minimum", 1000, minDispatchTimeout},
		{"clamped to maximum", int((11 * time.Minute).Milliseconds()), maxDispatchTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &protocol.Request{TimeoutMs: tc.timeoutMs}
			if got := requestTimeout(req); got != tc.want {
				t.Fatalf("requestTimeout(%d) = %v, want %v", tc.timeoutMs, got, tc.want)
			}
		})
	}
}

func TestAcquireExclusive(t *testing.T) {
	s := New(Options{})

	release, ok := s.acquireExclusive("pcb.drc.check", "w1")
	if !ok {
		t.Fatal("first acquire should succeed")
	}

	// Same action+window while held → busy.
	if _, ok := s.acquireExclusive("pcb.drc.check", "w1"); ok {
		t.Fatal("second acquire on the same window should be refused")
	}
	// Different window or different action → independent slots.
	if rel, ok := s.acquireExclusive("pcb.drc.check", "w2"); !ok {
		t.Fatal("other window should not be blocked")
	} else {
		rel()
	}
	if rel, ok := s.acquireExclusive("schematic.drc.check", "w1"); !ok {
		t.Fatal("other action on the same window should not be blocked")
	} else {
		rel()
	}

	// Released slot is reusable.
	release()
	rel2, ok := s.acquireExclusive("pcb.drc.check", "w1")
	if !ok {
		t.Fatal("acquire after release should succeed")
	}
	rel2()
}

func TestNonReentrantSet(t *testing.T) {
	// The guard exists for DRC (A4: background-window recompute never settles;
	// stacked retries make it worse). Guard both editors' DRC, nothing else.
	if !nonReentrant["pcb.drc.check"] || !nonReentrant["schematic.drc.check"] {
		t.Fatal("both DRC actions must be non-reentrant")
	}
	if nonReentrant["pcb.components.list"] {
		t.Fatal("reads must not be guarded")
	}
}
