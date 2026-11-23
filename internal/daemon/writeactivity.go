package daemon

import (
	"sync"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Per-window write activity for hosts that must not quit or restart in the
// middle of someone's batch (edadesk#59). An external agent writing hundreds of
// copper objects through this daemon is invisible to the host app otherwise:
// writeHealth is a 20-sample outcome ring without timestamps, so "is anything
// writing right now / did it write a moment ago" cannot be derived from it.
//
// Only document-changing requests count (requestMutates: catalog Mutates minus
// dry-run previews), and only those that are actually forwarded — a refusal
// before dispatch changed nothing. lastWriteAt moves both when a write starts
// and when it finishes, so a host's "recently written" window runs from the
// end of the last write, not its start.
//
// Entries are keyed by windowId and deliberately survive the window's
// disconnect: a batch interrupted by a reload is exactly what a host should
// still see. Idle entries are dropped after writeActivityRetention.

const writeActivityRetention = 10 * time.Minute

// WindowWriteActivity is the /health-exposed snapshot of one window's writes.
type WindowWriteActivity struct {
	// InFlight is how many forwarded writes are awaiting the connector's reply.
	InFlight int `json:"inFlight"`
	// LastWriteAt is when a write last started or finished (RFC 3339, UTC).
	LastWriteAt string `json:"lastWriteAt"`
	// LastAction is that write's action name.
	LastAction string `json:"lastAction"`
}

type windowWriteActivity struct {
	inFlight   int
	lastAt     time.Time
	lastAction string
}

type writeActivityTracker struct {
	mu       sync.Mutex
	byWindow map[string]*windowWriteActivity
	now      func() time.Time
}

func newWriteActivityTracker() *writeActivityTracker {
	return &writeActivityTracker{byWindow: map[string]*windowWriteActivity{}, now: time.Now}
}

// begin records a forwarded request; for a write it returns the release to call
// once the reply (or failure) is in. Reads return a no-op.
func (t *writeActivityTracker) begin(req *protocol.Request) (release func()) {
	if t == nil || req == nil || req.WindowID == "" || !requestMutates(req) {
		return func() {}
	}
	t.mu.Lock()
	st := t.byWindow[req.WindowID]
	if st == nil {
		st = &windowWriteActivity{}
		t.byWindow[req.WindowID] = st
	}
	st.inFlight++
	st.lastAt, st.lastAction = t.now().UTC(), req.Action
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if st.inFlight > 0 {
				st.inFlight--
			}
			st.lastAt = t.now().UTC()
			// Re-attach if a prune dropped the entry meanwhile: the finish is news.
			t.byWindow[req.WindowID] = st
		})
	}
}

// all returns every window with a write in flight or within the retention.
func (t *writeActivityTracker) all() map[string]WindowWriteActivity {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	out := map[string]WindowWriteActivity{}
	for id, st := range t.byWindow {
		if st.inFlight == 0 && now.Sub(st.lastAt) > writeActivityRetention {
			delete(t.byWindow, id)
			continue
		}
		out[id] = WindowWriteActivity{InFlight: st.inFlight, LastWriteAt: st.lastAt.Format(time.RFC3339Nano), LastAction: st.lastAction}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
