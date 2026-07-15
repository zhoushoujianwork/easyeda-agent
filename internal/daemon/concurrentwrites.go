package daemon

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Daemon-level concurrent-writer advisory (issue #108).
//
// Multiple clients (foreground CLI, background agents, raw HTTP callers) can
// drive the SAME window through one daemon with no mutex whatsoever — observed
// live on the 2026-07-15 esp32Mini regression, where a stopped background
// agent kept replaying a stale routing plan against the board a foreground
// session was fixing. This guard makes the collision visible: the daemon
// tracks the last successful mutating client per window and ANNOTATES (never
// blocks) a different client's mutating action with a top-level
// `concurrentWriter` field the CLI surfaces on stderr.
//
// State machine (per windowId, in-memory — same lifetime rationale as
// staleGuard: windowIds churn on reconnect and a fresh window starts clean):
//   RECORD — a mutating action (catalog Mutates=true, same source of truth as
//            autosave) with a non-empty ClientID succeeds → it becomes the
//            window's last writer.
//   WARN   — a mutating action arrives from a DIFFERENT non-empty ClientID
//            while the last writer is younger than concurrentWriterWindow.
//   EXPIRE — last writers older than concurrentWriterWindow never warn (the
//            sessions are no longer plausibly concurrent) and are dropped.
//
// Client identity vs session identity: ClientID is "<hostname>:<pid>[:<label>]"
// and the pid makes it precise for AUDIT attribution — but every one-shot CLI
// invocation is a fresh pid, so comparing full ClientIDs would flag an agent's
// own consecutive commands as "another client" (observed on the first live
// smoke: same label, new pid, spurious warning on every mutating step). The
// guard therefore compares SESSION identity (see sessionKey): hostname+label
// when a label is present, hostname alone when not. Consequences, deliberate:
//   - two sessions with different EASYEDA_CLIENT_LABEL values warn — this is
//     the issue-#108 mechanism (label your background agents);
//   - one labeled session warns against an unlabeled one (keys differ);
//   - two UNLABELED processes on the same host never warn: one-shot processes
//     give the daemon nothing to tell "same agent, next command" apart from
//     "second agent" — the audit log (with pids) remains the forensic tool,
//     and same-host multi-agent detection requires labels.
//
// Non-goals (deliberate): reads never warn (they cannot conflict), the same
// session writing repeatedly never warns, empty ClientIDs are neither recorded
// nor warned about (nothing to attribute), and nothing is ever blocked — a
// hard per-window lease stays future work until a real need shows up.

// concurrentWriterWindow bounds how long a past writer counts as "concurrent".
// Beyond this the earlier session has almost certainly finished; warning would
// be noise.
const concurrentWriterWindow = 10 * time.Minute

// lastWriter is the per-window record of the most recent successful mutation.
type lastWriter struct {
	clientID string
	action   string
	at       time.Time
}

// concurrentGuard is the per-window concurrent-writer state machine. Methods
// are safe for concurrent use.
type concurrentGuard struct {
	mu sync.Mutex
	// last maps windowId → the last successful mutating write (absent = none).
	last map[string]lastWriter
	// now is the clock, swappable in tests.
	now func() time.Time
}

func newConcurrentGuard() *concurrentGuard {
	return &concurrentGuard{last: map[string]lastWriter{}, now: time.Now}
}

// observe applies one completed action to the state machine: it may annotate
// resp with a concurrentWriter advisory (a different client's recent write)
// and, on success, records this client as the window's last writer. Call it
// with the connector's response before writing it to the caller.
func (g *concurrentGuard) observe(req *protocol.Request, resp *protocol.Response) {
	if g == nil || req == nil || resp == nil {
		return
	}
	// Reads never conflict and never move the state machine.
	if !mutatesAction[req.Action] {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()

	// Annotate first: the warning reflects the state BEFORE this write lands.
	if prev, ok := g.last[req.WindowID]; ok {
		age := now.Sub(prev.at)
		if age > concurrentWriterWindow {
			delete(g.last, req.WindowID) // expired — no longer plausibly concurrent
		} else if req.ClientID != "" && sessionKey(prev.clientID) != sessionKey(req.ClientID) {
			resp.ConcurrentWriter = concurrentWriterMessage(prev, age)
		}
	}

	// Only successful, attributable writes become the last writer.
	if resp.OK && req.ClientID != "" {
		g.last[req.WindowID] = lastWriter{clientID: req.ClientID, action: req.Action, at: now}
	}
}

// sessionKey reduces a "<hostname>:<pid>[:<label>]" ClientID to the identity
// that is stable across a session's one-shot CLI invocations: hostname+label
// when a label is present, hostname alone when not (the pid churns per
// command and must not count as a new client — see the package comment). IDs
// that don't match the convention (raw HTTP callers) fall through sensibly:
// no colon → the whole id is the key.
func sessionKey(clientID string) string {
	parts := strings.SplitN(clientID, ":", 3)
	if len(parts) == 3 && parts[2] != "" {
		return parts[0] + ":" + parts[2] // host + label
	}
	return parts[0] // host (or the whole id when there is no colon)
}

// concurrentWriterMessage builds the advisory: who wrote, what they ran, and
// how long ago — enough for the receiving agent to go coordinate (or check the
// audit log, which now carries clientId).
func concurrentWriterMessage(prev lastWriter, age time.Duration) string {
	secs := int(age.Round(time.Second) / time.Second)
	return fmt.Sprintf(
		"another client mutated this board %d seconds ago — coordinate or expect conflicts (issue #108): last writer %s ran %s",
		secs, prev.clientID, prev.action)
}
