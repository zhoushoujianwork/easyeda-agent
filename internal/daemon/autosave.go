package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Daemon-level debounced autosave.
//
// `place`/`wire`/`modify` only mutate the in-memory EasyEDA document; without a
// save they never hit disk, so a window reload / daemon restart / EasyEDA crash
// silently loses the work (observed live: placed parts vanished after the daemon
// hot-reloaded). This is the infrastructure safety net — after any content-
// changing action the daemon arms a trailing-debounce timer and saves once the
// edits quiesce, so the agent doesn't have to remember to. Opt-in via
// Options.AutosaveDebounce (0 = off).

// mutatesAction maps each action name to whether it mutates the document, so the
// daemon fires an autosave after content-changing actions only.
var mutatesAction = func() map[string]bool {
	m := map[string]bool{}
	for _, a := range protocol.AllActions() {
		m[a.Name] = a.Mutates
	}
	return m
}()

// saveActionForDocType returns the typed save action for a documentType, or ""
// when none exists. Schematic and PCB both have a typed save; a PCB-mutating
// action therefore arms a debounced pcb.save the same way a schematic edit arms
// schematic.save.
func saveActionForDocType(docType string) string {
	switch docType {
	case "schematic":
		return "schematic.save"
	case "pcb":
		return "pcb.save"
	}
	return ""
}

// autosaver debounces per-window saves: a burst of edits on one window collapses
// into a single save fired `debounce` after the LAST edit (trailing debounce).
type autosaver struct {
	mu       sync.Mutex
	debounce time.Duration
	timers   map[string]*time.Timer
	save     func(windowID, saveAction string)
}

func newAutosaver(debounce time.Duration, save func(windowID, saveAction string)) *autosaver {
	return &autosaver{
		debounce: debounce,
		timers:   map[string]*time.Timer{},
		save:     save,
	}
}

// schedule (re)arms the trailing-debounce timer for windowID. Each call resets
// the timer, so N rapid mutations coalesce into one save `debounce` after the
// last. nil/zero-debounce receiver is a no-op (autosave disabled).
func (a *autosaver) schedule(windowID, saveAction string) {
	if a == nil || a.debounce <= 0 || windowID == "" || saveAction == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if t, ok := a.timers[windowID]; ok {
		t.Stop()
	}
	a.timers[windowID] = time.AfterFunc(a.debounce, func() {
		a.mu.Lock()
		delete(a.timers, windowID)
		a.mu.Unlock()
		a.save(windowID, saveAction)
	})
}

// stop cancels all pending timers (daemon shutdown). Pending edits are not force-
// flushed — flushing would race shutdown; the next session saves on first edit.
func (a *autosaver) stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.timers {
		t.Stop()
	}
	a.timers = map[string]*time.Timer{}
}

// maybeAutosave arms an autosave after a successful mutating action. It EXCLUDES
// the save action itself (schematic.save is Mutates=true) so a save never arms
// another save — no recursion.
func (s *Server) maybeAutosave(req *protocol.Request) {
	if s.autosave == nil || req == nil {
		return
	}
	if !mutatesAction[req.Action] {
		return
	}
	saveAction := saveActionForDocType(docTypeForAction(req.Action))
	if saveAction == "" || req.Action == saveAction {
		return
	}
	s.autosave.schedule(req.WindowID, saveAction)
}

// dispatchSave forwards the debounced save to the window's connector. Best-effort
// and fired from a timer (no HTTP caller): logged + audited, never surfaced.
func (s *Server) dispatchSave(windowID, saveAction string) {
	target, ok := s.hub.target(windowID)
	if !ok {
		return // window disconnected before the timer fired
	}
	req := protocol.Request{
		Envelope: protocol.Envelope{
			ID:        s.nextRequestID(),
			Type:      protocol.TypeRequest,
			Version:   "v1",
			WindowID:  windowID,
			CreatedAt: time.Now().UTC(),
		},
		Action: saveAction,
	}
	ctx, cancel := context.WithTimeout(s.connCtx, dispatchTimeout)
	defer cancel()
	started := time.Now().UTC()
	resp, err := target.dispatch(ctx, req)
	if err != nil {
		s.logf("autosave: %s on %s failed: %v", saveAction, windowID, err)
		return
	}
	s.audit.Append(fromResponse(started, &req, resp))
	s.logf("autosave: %s on %s (ok=%v)", saveAction, windowID, resp.OK)
}
