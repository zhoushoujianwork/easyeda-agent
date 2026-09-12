package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// clockTracker builds a tracker on a controllable clock so the tests assert the
// JUDGEMENT (probe outstanding longer than the grace = proven blocked), not a
// sleep.
func clockTracker(now *time.Time) *queueBlockTracker {
	t := newQueueBlockTracker()
	t.now = func() time.Time { return *now }
	return t
}

func TestQueueBlock_NotBlockedUntilTheProbeHasActuallyBeenWaiting(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := clockTracker(&now)

	if _, _, blocked := tr.blocked("w1"); blocked {
		t.Fatal("no probe in flight → nothing is proven; must not refuse")
	}

	if _, started := tr.beginProbe("w1", "schematic.power.connect_pin", "req_2675", now); !started {
		t.Fatal("first probe must be accepted")
	}
	// A probe that JUST went out proves nothing yet — the queue may already be free.
	if _, _, blocked := tr.blocked("w1"); blocked {
		t.Fatal("a fresh probe is not evidence; refusing here would false-positive every single timeout")
	}

	now = now.Add(queueBlockGrace + time.Millisecond)
	p, waited, blocked := tr.blocked("w1")
	if !blocked {
		t.Fatal("a light queued read unanswered past the grace IS the proof — must refuse now")
	}
	if p.blockerAction != "schematic.power.connect_pin" || p.blockerID != "req_2675" {
		t.Errorf("the refusal must be able to NAME the head, got %+v", p)
	}
	if waited < queueBlockGrace {
		t.Errorf("waited=%s must be at least the grace", waited)
	}
}

func TestQueueBlock_ProbeIsSingletonAndClearsOnAnswer(t *testing.T) {
	now := time.Unix(2000, 0)
	tr := clockTracker(&now)

	if _, started := tr.beginProbe("w1", "a", "req_1", now); !started {
		t.Fatal("first probe must start")
	}
	if _, started := tr.beginProbe("w1", "b", "req_2", now); started {
		t.Fatal("a second probe on the same window must be refused — the probe must not become part of the backlog it measures")
	}
	// Another window is independent.
	if _, started := tr.beginProbe("w2", "c", "req_3", now); !started {
		t.Fatal("probes are per-window")
	}

	now = now.Add(time.Minute)
	if _, _, blocked := tr.blocked("w1"); !blocked {
		t.Fatal("still blocked before the probe answers")
	}
	tr.endProbe("w1")
	if _, _, blocked := tr.blocked("w1"); blocked {
		t.Fatal("the moment the probe answers the queue is flowing — the refusal must stop immediately")
	}
	if _, _, blocked := tr.blocked("w2"); !blocked {
		t.Fatal("clearing one window must not clear another")
	}
}

func TestCheckQueueBlocked_BypassReadIsNeverRefused(t *testing.T) {
	now := time.Unix(3000, 0)
	s := &Server{queueBlocks: clockTracker(&now)}
	if _, started := s.queueBlocks.beginProbe("w1", "schematic.power.connect_pin", "req_9", now); !started {
		t.Fatal("probe must start")
	}
	now = now.Add(queueBlockGrace * 3)
	// A stalled queued read alone cannot prove bypass responsiveness.
	p := s.queueBlocks.probes["w1"]
	s.queueBlocks.recordBypass("w1", p, true)

	// document.current is the ONLY observation left during a wedge (it bypasses the
	// connector FIFO). Refusing it would blind the one instrument that works.
	if resp := s.checkQueueBlocked(&protocol.Request{
		Envelope: protocol.Envelope{ID: "r1", WindowID: "w1"},
		Action:   "document.current",
	}); resp != nil {
		t.Fatalf("the bypass read must always pass, got %+v", resp.Error)
	}

	resp := s.checkQueueBlocked(&protocol.Request{
		Envelope: protocol.Envelope{ID: "r2", WindowID: "w1"},
		Action:   "schematic.pages.list",
	})
	if resp == nil {
		t.Fatal("a FIFO action must be refused while the queue is proven blocked")
	}
	if resp.Error == nil || resp.Error.Code != "CONNECTOR_QUEUE_BLOCKED" {
		t.Fatalf("wrong error shape: %+v", resp.Error)
	}
	// The message has to carry an executable next step, not just a diagnosis:
	// the wedged handler is still running, so re-issuing the write makes duplicates.
	// The CLI's requestAction only forwards `message` upward (detail is dropped),
	// so every load-bearing fact has to be IN the message: who blocks, how long,
	// that nothing was sent, and that re-issuing is the wrong move.
	for _, want := range []string{"schematic.power.connect_pin", "req_9", "NOT sent", "do NOT re-issue"} {
		if !strings.Contains(resp.Error.Message, want) {
			t.Errorf("refusal message must mention %q; got: %s", want, resp.Error.Message)
		}
	}
	for _, want := range []string{"document.current", "FOREGROUND"} {
		if !strings.Contains(resp.Error.Detail, want) {
			t.Errorf("refusal detail must mention %q; got: %s", want, resp.Error.Detail)
		}
	}
}

func TestCheckQueueBlocked_OtherWindowsUnaffected(t *testing.T) {
	now := time.Unix(4000, 0)
	s := &Server{queueBlocks: clockTracker(&now)}
	s.queueBlocks.beginProbe("w1", "x", "req_1", now)
	now = now.Add(time.Minute)
	if resp := s.checkQueueBlocked(&protocol.Request{
		Envelope: protocol.Envelope{ID: "r", WindowID: "w2"},
		Action:   "schematic.pages.list",
	}); resp != nil {
		t.Fatal("a blocked window must not refuse traffic to a different window")
	}
}

func TestArmQueueProbe_OnlyOnTimeouts(t *testing.T) {
	now := time.Unix(5000, 0)
	s := &Server{queueBlocks: clockTracker(&now)}
	target := &conn{pending: map[string]chan *protocol.Response{}}
	req := &protocol.Request{Envelope: protocol.Envelope{ID: "req_1", WindowID: "w1"}, Action: "schematic.pages.list"}

	// A success (nil error) must never arm a probe.
	s.armQueueProbe(target, req, nil)
	if _, ok := s.queueBlocks.probes["w1"]; ok {
		t.Fatal("no failure → no probe")
	}
	// A transport write failure is the reconnect path's business, not this gate's.
	s.armQueueProbe(target, req, context.Cause(context.Background()))
	if _, ok := s.queueBlocks.probes["w1"]; ok {
		t.Fatal("a non-timeout error must not arm the queue probe")
	}
	// A bypass action timing out says nothing about the FIFO.
	s.armQueueProbe(target, &protocol.Request{
		Envelope: protocol.Envelope{ID: "req_2", WindowID: "w1"},
		Action:   "document.current",
	}, context.DeadlineExceeded)
	if _, ok := s.queueBlocks.probes["w1"]; ok {
		t.Fatal("a bypass timeout must not be read as a blocked FIFO")
	}
}

// The bypass list is mirrored from the connector. A drift in either direction is
// a real bug: too many entries push doomed requests at a stuck queue, too few
// blind the only instrument that still answers during a wedge.
func TestConnectorBypassListMatchesConnector(t *testing.T) {
	if len(connectorBypassActions) != 1 || !connectorBypassActions["document.current"] {
		t.Fatalf("connectorBypassActions must mirror extension/src/action-queue.ts BYPASS_ACTIONS exactly, got %v", connectorBypassActions)
	}
}
