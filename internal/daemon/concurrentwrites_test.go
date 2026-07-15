package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// cwReq builds a minimal request for the concurrent-writer state machine.
func cwReq(action, windowID, clientID string) *protocol.Request {
	return &protocol.Request{
		Envelope: protocol.Envelope{WindowID: windowID},
		Action:   action,
		ClientID: clientID,
	}
}

// runCW feeds one action (with the given outcome) through the guard and
// returns the response so callers can assert on ConcurrentWriter.
func runCW(g *concurrentGuard, action, windowID, clientID string, ok bool) *protocol.Response {
	resp := &protocol.Response{OK: ok}
	g.observe(cwReq(action, windowID, clientID), resp)
	return resp
}

func TestConcurrentGuard_DifferentClientWriteWarns(t *testing.T) {
	g := newConcurrentGuard()
	runCW(g, "pcb.via.create", "w1", "hostA:1:agent-a", true)

	resp := runCW(g, "pcb.line.create", "w1", "hostB:2:agent-b", true)
	if resp.ConcurrentWriter == "" {
		t.Fatal("B's mutation after A's: want concurrentWriter advisory, got none")
	}
	if !strings.Contains(resp.ConcurrentWriter, "hostA:1:agent-a") {
		t.Errorf("advisory should name the previous writer, got %q", resp.ConcurrentWriter)
	}
	if !strings.Contains(resp.ConcurrentWriter, "pcb.via.create") {
		t.Errorf("advisory should name the previous action, got %q", resp.ConcurrentWriter)
	}
	if !strings.Contains(resp.ConcurrentWriter, "seconds ago") {
		t.Errorf("advisory should say how long ago, got %q", resp.ConcurrentWriter)
	}
	if !strings.Contains(resp.ConcurrentWriter, "issue #108") {
		t.Errorf("advisory should reference issue #108, got %q", resp.ConcurrentWriter)
	}
}

func TestConcurrentGuard_SameClientRepeatDoesNotWarn(t *testing.T) {
	g := newConcurrentGuard()
	runCW(g, "pcb.via.create", "w1", "hostA:1", true)
	runCW(g, "pcb.line.create", "w1", "hostB:2", true) // B takes over (warned)

	// B is now the last writer — B's next write must be silent.
	if resp := runCW(g, "pcb.via.create", "w1", "hostB:2", true); resp.ConcurrentWriter != "" {
		t.Errorf("B writing again after becoming last writer: want no advisory, got %q", resp.ConcurrentWriter)
	}
}

// TestConcurrentGuard_SessionIdentity pins the session-key semantics: the pid
// part of "<hostname>:<pid>[:<label>]" churns on every one-shot CLI
// invocation and must NOT count as a new client (first live smoke caught the
// same labeled session warning against itself); labels are the mechanism that
// distinguishes concurrent sessions on one host.
func TestConcurrentGuard_SessionIdentity(t *testing.T) {
	t.Run("same label new pid does not warn", func(t *testing.T) {
		g := newConcurrentGuard()
		runCW(g, "pcb.via.create", "w1", "mbp:100:e2e-regression", true)
		if resp := runCW(g, "pcb.line.create", "w1", "mbp:101:e2e-regression", true); resp.ConcurrentWriter != "" {
			t.Errorf("same session (label) across pids: want no advisory, got %q", resp.ConcurrentWriter)
		}
	})
	t.Run("same host unlabeled new pid does not warn", func(t *testing.T) {
		g := newConcurrentGuard()
		runCW(g, "pcb.via.create", "w1", "mbp:100", true)
		if resp := runCW(g, "pcb.line.create", "w1", "mbp:101", true); resp.ConcurrentWriter != "" {
			t.Errorf("unlabeled same-host processes are indistinguishable: want no advisory, got %q", resp.ConcurrentWriter)
		}
	})
	t.Run("different labels on one host warn", func(t *testing.T) {
		g := newConcurrentGuard()
		runCW(g, "pcb.via.create", "w1", "mbp:100:agent-a", true)
		resp := runCW(g, "pcb.line.create", "w1", "mbp:101:agent-b", true)
		if resp.ConcurrentWriter == "" {
			t.Fatal("two labeled sessions on one host: want advisory, got none")
		}
		if !strings.Contains(resp.ConcurrentWriter, "mbp:100:agent-a") {
			t.Errorf("advisory should carry the previous writer's FULL id, got %q", resp.ConcurrentWriter)
		}
	})
	t.Run("labeled vs unlabeled warn", func(t *testing.T) {
		g := newConcurrentGuard()
		runCW(g, "pcb.via.create", "w1", "mbp:100:agent-a", true)
		if resp := runCW(g, "pcb.line.create", "w1", "mbp:101", true); resp.ConcurrentWriter == "" {
			t.Error("labeled writer then unlabeled writer: want advisory, got none")
		}
	})
}

func TestSessionKey(t *testing.T) {
	cases := map[string]string{
		"mbp:123":             "mbp",             // unlabeled → host
		"mbp:123:e2e":         "mbp:e2e",         // labeled → host+label
		"mbp:123:e2e:phase-2": "mbp:e2e:phase-2", // colons in the label survive
		"raw-caller":          "raw-caller",      // no colon → whole id
		"mbp:":                "mbp",
	}
	for id, want := range cases {
		if got := sessionKey(id); got != want {
			t.Errorf("sessionKey(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestConcurrentGuard_ReadsNeverWarn(t *testing.T) {
	g := newConcurrentGuard()
	runCW(g, "pcb.via.create", "w1", "hostB:2", true)

	for _, read := range []string{"pcb.line.list", "pcb.drc.check", "schematic.components.list"} {
		if resp := runCW(g, read, "w1", "hostA:1", true); resp.ConcurrentWriter != "" {
			t.Errorf("read %s from another client: want no advisory, got %q", read, resp.ConcurrentWriter)
		}
	}
}

func TestConcurrentGuard_ExpiredWriterDoesNotWarn(t *testing.T) {
	g := newConcurrentGuard()
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	now := base
	g.now = func() time.Time { return now }

	runCW(g, "pcb.via.create", "w1", "hostA:1", true)

	// Beyond the window the old writer is no longer plausibly concurrent.
	now = base.Add(concurrentWriterWindow + time.Second)
	if resp := runCW(g, "pcb.line.create", "w1", "hostB:2", true); resp.ConcurrentWriter != "" {
		t.Errorf("write after the concurrency window expired: want no advisory, got %q", resp.ConcurrentWriter)
	}
}

func TestConcurrentGuard_WithinWindowStillWarns(t *testing.T) {
	g := newConcurrentGuard()
	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	now := base
	g.now = func() time.Time { return now }

	runCW(g, "pcb.via.create", "w1", "hostA:1", true)

	now = base.Add(9 * time.Minute)
	resp := runCW(g, "pcb.line.create", "w1", "hostB:2", true)
	if resp.ConcurrentWriter == "" {
		t.Fatal("write 9 minutes after another client's: want advisory, got none")
	}
	if !strings.Contains(resp.ConcurrentWriter, "540 seconds ago") {
		t.Errorf("advisory should carry the age in seconds, got %q", resp.ConcurrentWriter)
	}
}

func TestConcurrentGuard_FailedWriteDoesNotBecomeLastWriter(t *testing.T) {
	g := newConcurrentGuard()
	runCW(g, "pcb.via.create", "w1", "hostA:1", true)
	runCW(g, "pcb.line.create", "w1", "hostB:2", false) // B's write failed

	// A stays the last writer, so B's next (successful) write still warns
	// about A — not about B's own failed attempt.
	resp := runCW(g, "pcb.line.create", "w1", "hostB:2", true)
	if resp.ConcurrentWriter == "" {
		t.Fatal("failed write must not take over last-writer; want advisory about A, got none")
	}
	if !strings.Contains(resp.ConcurrentWriter, "hostA:1") {
		t.Errorf("advisory should still name A, got %q", resp.ConcurrentWriter)
	}
}

func TestConcurrentGuard_EmptyClientIDNeitherWarnsNorRecords(t *testing.T) {
	g := newConcurrentGuard()
	runCW(g, "pcb.via.create", "w1", "hostA:1", true)

	// A raw caller without identity: nothing to attribute, no warning …
	if resp := runCW(g, "pcb.line.create", "w1", "", true); resp.ConcurrentWriter != "" {
		t.Errorf("anonymous write: want no advisory, got %q", resp.ConcurrentWriter)
	}
	// … and it must not displace A as the recorded last writer.
	resp := runCW(g, "pcb.line.create", "w1", "hostB:2", true)
	if resp.ConcurrentWriter == "" || !strings.Contains(resp.ConcurrentWriter, "hostA:1") {
		t.Errorf("A should still be the last writer after an anonymous write, got %q", resp.ConcurrentWriter)
	}
}

func TestConcurrentGuard_PerWindowIsolation(t *testing.T) {
	g := newConcurrentGuard()
	runCW(g, "pcb.via.create", "w1", "hostA:1", true)

	if resp := runCW(g, "pcb.line.create", "w2", "hostB:2", true); resp.ConcurrentWriter != "" {
		t.Errorf("A's write on w1 must not flag B's write on w2, got %q", resp.ConcurrentWriter)
	}
}
