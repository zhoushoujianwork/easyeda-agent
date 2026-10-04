package daemon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
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

func TestDocumentSourceProbeDoesNotAutosave(t *testing.T) {
	// Persistence is a separate explicit probe step, including for a real
	// same-source setter. Future action routing must not silently save it.
	s := &Server{autosave: newAutosaver(time.Hour, func(_, _ string) { t.Error("unexpected source autosave") })}
	defer s.autosave.stop()
	for _, req := range []*protocol.Request{
		{Envelope: protocol.Envelope{WindowID: "w"}, Action: "document.source.get"},
		{Envelope: protocol.Envelope{WindowID: "w"}, Action: "document.source.roundtrip", Payload: map[string]any{"dryRun": true}},
		{Envelope: protocol.Envelope{WindowID: "w"}, Action: "document.source.roundtrip", Payload: map[string]any{"dryRun": false}},
	} {
		s.maybeAutosave(req)
	}
	if len(s.autosave.timers) != 0 {
		t.Fatal("source probe armed autosave")
	}
	if requestMutates(&protocol.Request{Action: "document.source.roundtrip", Payload: map[string]any{"dryRun": true}}) {
		t.Fatal("source preview was marked as a mutation")
	}
	if !requestMutates(&protocol.Request{Action: "document.source.roundtrip"}) {
		t.Fatal("source write lost mutation classification")
	}
}

// TestMaybeAutosave_DryRunDoesNotArm pins issue #112b on the autosave side: a
// `--dry-run` preview writes nothing, so there is nothing to save — arming the
// debounce would fire a pointless save (and, on pcb.page.clear, one that looks
// to the user like the preview touched the board).
func TestMaybeAutosave_DryRunDoesNotArm(t *testing.T) {
	var armed atomic.Int32
	s := &Server{autosave: newAutosaver(10*time.Millisecond, func(_, _ string) { armed.Add(1) })}

	s.maybeAutosave(&protocol.Request{
		Envelope: protocol.Envelope{WindowID: "w1"},
		Action:   "pcb.page.clear",
		Payload:  map[string]any{"dryRun": true},
	})
	time.Sleep(40 * time.Millisecond)
	if n := armed.Load(); n != 0 {
		t.Errorf("a dry-run preview must not arm autosave, fired %d save(s)", n)
	}

	// The same action for real still arms it — the fix must not disable autosave.
	s.maybeAutosave(&protocol.Request{
		Envelope: protocol.Envelope{WindowID: "w1"},
		Action:   "pcb.page.clear",
		Payload:  map[string]any{"dryRun": false},
	})
	time.Sleep(40 * time.Millisecond)
	if n := armed.Load(); n != 1 {
		t.Errorf("a real pcb.page.clear must arm exactly 1 autosave, got %d", n)
	}
}

// ── autosave 必须落在空档里(2026-08-24 真机四次超时簇的直接回归) ────────────
//
// 连接器每窗口只有一条 FIFO;schematic.save 在真板上 p95=6.8s、max=59.4s。计时器
// 到点就发 = 把一条几十秒的阻塞插进一批动作中间,后面所有动作(含 --doc 门那条
// 15ms 的 pages.list)排在它后面一起超时。判据只有一条:窗口正忙就重新防抖。

func TestDeferAutosave_BusyWindowPushesTheSaveBack(t *testing.T) {
	var saved atomic.Int32
	s := &Server{queueBlocks: newQueueBlockTracker()}
	s.autosave = newAutosaver(10*time.Millisecond, func(_, _ string) { saved.Add(1) })

	release := s.beginClientAction("w1")
	if got := s.clientActionsInFlight("w1"); got != 1 {
		t.Fatalf("in-flight count = %d, want 1", got)
	}
	if !s.deferAutosave("w1", "schematic.save") {
		t.Fatal("a save must never be injected while a client action is in flight on that window")
	}

	// It was re-armed, not dropped: once the window goes idle the save happens.
	release()
	if got := s.clientActionsInFlight("w1"); got != 0 {
		t.Fatalf("release must decrement, got %d", got)
	}
	time.Sleep(60 * time.Millisecond)
	if n := saved.Load(); n != 1 {
		t.Fatalf("deferring must RE-ARM the debounce, not cancel the safety net; saves=%d", n)
	}
}

func TestDeferAutosave_IdleWindowSavesImmediately(t *testing.T) {
	s := &Server{queueBlocks: newQueueBlockTracker()}
	s.autosave = newAutosaver(10*time.Millisecond, func(_, _ string) {})
	if s.deferAutosave("w1", "schematic.save") {
		t.Fatal("an idle window must save right away — this gate must not slow the normal path")
	}
}

func TestDeferAutosave_BlockedQueueAlsoDefers(t *testing.T) {
	now := time.Unix(6000, 0)
	s := &Server{queueBlocks: clockTracker(&now)}
	s.autosave = newAutosaver(10*time.Millisecond, func(_, _ string) {})
	s.queueBlocks.beginProbe("w1", "schematic.power.connect_pin", "req_1", now)
	now = now.Add(queueBlockGrace * 2)
	if !s.deferAutosave("w1", "schematic.save") {
		t.Fatal("pushing a save at a queue that is already proven blocked only lengthens the backlog")
	}
}

func TestDeferAutosave_CapKeepsItASafetyNet(t *testing.T) {
	s := &Server{queueBlocks: newQueueBlockTracker()}
	s.autosave = newAutosaver(10*time.Millisecond, func(_, _ string) {})
	release := s.beginClientAction("w1")
	defer release()

	if !s.deferAutosave("w1", "schematic.save") {
		t.Fatal("first call defers")
	}
	// Rewind the deferral clock past the cap: a long busy stretch must not mean
	// "never saved" — past autosaveMaxDefer the save goes in regardless.
	s.autosave.mu.Lock()
	s.autosave.deferredSince["w1"] = time.Now().Add(-autosaveMaxDefer - time.Second)
	s.autosave.mu.Unlock()

	if s.deferAutosave("w1", "schematic.save") {
		t.Fatal("past autosaveMaxDefer the save must go through even on a busy window")
	}
	s.autosave.mu.Lock()
	_, stillDeferred := s.autosave.deferredSince["w1"]
	s.autosave.mu.Unlock()
	if stillDeferred {
		t.Error("the deferral clock must reset once a save is let through")
	}
}

func TestBeginClientAction_ReleaseIsIdempotent(t *testing.T) {
	s := &Server{}
	release := s.beginClientAction("w1")
	release()
	release()
	if got := s.clientActionsInFlight("w1"); got != 0 {
		t.Fatalf("a double release must not drive the counter negative, got %d", got)
	}
	// An unrouted request (no windowId) must not blow up or leak a counter.
	s.beginClientAction("")()
	if got := s.clientActionsInFlight(""); got != 0 {
		t.Fatalf("empty windowId must stay a no-op, got %d", got)
	}
}

func TestRequestMutates(t *testing.T) {
	mutating := &protocol.Request{Action: "pcb.page.clear"}
	if !requestMutates(mutating) {
		t.Error("pcb.page.clear without a payload is a real mutation")
	}
	preview := &protocol.Request{Action: "pcb.page.clear", Payload: map[string]any{"dryRun": true}}
	if requestMutates(preview) {
		t.Error("pcb.page.clear --dry-run must not count as a mutation")
	}
	read := &protocol.Request{Action: "pcb.line.list"}
	if requestMutates(read) {
		t.Error("a read is never a mutation")
	}
	if requestMutates(nil) {
		t.Error("nil request must not count as a mutation")
	}
}
