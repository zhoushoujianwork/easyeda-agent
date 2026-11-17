package daemon

import (
	"context"
	"fmt"
	"sort"
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

// dryRunPayloadField is the payload key every dry-runnable action uses to mark a
// request as a PREVIEW. It is a project-wide convention, not a per-action one:
// the actions that forward a preview flag to the connector (pcb.page.clear,
// schematic.page.clear, pcb.beautify) all send exactly `dryRun`, and every other
// `--dry-run` CLI flag (mount-holes / power-planes / pour-fit / route-short /
// autoconnect …) short-circuits inside the CLI and never dispatches a mutating
// action at all. Keeping one key means the daemon needs no per-action catalog
// entry to tell a preview from a write (issue #112).
const dryRunPayloadField = "dryRun"

// isDryRunRequest reports whether a request is a preview that changes nothing.
// Strictly a JSON `true` — an unparseable/absent flag counts as a real write,
// which is the safe direction to err (a missed preview only costs a redundant
// save; a misread write would lose the safety net entirely).
func isDryRunRequest(req *protocol.Request) bool {
	if req == nil {
		return false
	}
	v, ok := req.Payload[dryRunPayloadField]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// requestMutates reports whether a request actually changes the document: the
// catalog's Mutates flag (action-name granularity) MINUS dry-run previews, which
// the catalog cannot see. `pcb.page.clear --dry-run` only enumerates, so it must
// neither arm autosave nor the stale-read guard (issue #112).
func requestMutates(req *protocol.Request) bool {
	return req != nil && mutatesAction[req.Action] && !isDryRunRequest(req)
}

// sourceWriter names the document-domain actions that write a whole page whose
// save target is the certified document type, not the action's own domain.
func sourceWriter(action string) bool {
	return action == "document.source.roundtrip" || action == "document.source.restore"
}

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

// autosaveMaxDefer bounds how long a save may keep being pushed back because the
// window is busy (see deferAutosave). The safety net must stay a safety net: past
// this the save goes in regardless, accepting the head-of-line cost rather than
// letting a long busy stretch mean "never saved".
const autosaveMaxDefer = 60 * time.Second

// autosaveTarget freezes the document that was actually mutated. Window identity
// alone cannot distinguish two pages open in one connector.
type autosaveTarget struct {
	WindowID   string           `json:"windowId"`
	SaveAction string           `json:"action"`
	Context    protocol.Context `json:"context"`
}

func (t autosaveTarget) key() string {
	return t.WindowID + "|" + t.Context.ProjectUUID + "|" + t.Context.DocumentUUID + "|" + t.Context.DocumentType
}

func (t autosaveTarget) valid() bool {
	return t.WindowID != "" && t.Context.ProjectUUID != "" && t.Context.DocumentUUID != "" &&
		t.SaveAction != "" && t.SaveAction == saveActionForDocType(t.Context.DocumentType)
}

type autosaveTicket struct {
	autosaveTarget
	generation uint64
}

type autosaveEntry struct {
	ticket        autosaveTicket
	timer         *time.Timer
	scheduledAt   time.Time
	deferredSince time.Time
}

// AutosaveStatus is observational evidence, never a permission gate. Failed or
// skipped saves remain visible even when their outer request returned OK.
type AutosaveStatus struct {
	autosaveTarget
	State     string    `json:"state"`
	Saved     bool      `json:"saved"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type autosaver struct {
	mu         sync.Mutex
	debounce   time.Duration
	generation uint64
	stopped    bool
	timers     map[string]*autosaveEntry
	statuses   map[string]AutosaveStatus
	save       func(autosaveTicket)
}

func newAutosaver(debounce time.Duration, save func(autosaveTicket)) *autosaver {
	return &autosaver{debounce: debounce, timers: map[string]*autosaveEntry{}, statuses: map[string]AutosaveStatus{}, save: save}
}

func (a *autosaver) setStatusLocked(target autosaveTarget, state, err string) {
	a.statuses[target.key()] = AutosaveStatus{autosaveTarget: target, State: state, Saved: state == "saved", Error: err, UpdatedAt: time.Now().UTC()}
	// Bound completed history, retaining every still-pending document.
	if len(a.statuses) > 128 {
		var oldest string
		var at time.Time
		for key, status := range a.statuses {
			if _, pending := a.timers[key]; pending || key == target.key() {
				continue
			}
			if oldest == "" || status.UpdatedAt.Before(at) {
				oldest, at = key, status.UpdatedAt
			}
		}
		if oldest != "" {
			delete(a.statuses, oldest)
		}
	}
}

func (a *autosaver) snapshot() []AutosaveStatus {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AutosaveStatus, 0, len(a.statuses))
	for _, status := range a.statuses {
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func (a *autosaver) armLocked(e *autosaveEntry) {
	ticket := e.ticket
	e.timer = time.AfterFunc(a.debounce, func() {
		a.mu.Lock()
		current := a.timers[ticket.key()]
		if a.stopped || current != e {
			a.mu.Unlock()
			return
		}
		e.timer = nil
		a.mu.Unlock()
		a.save(ticket)
	})
}

// Each new mutation gets a generation. A fired callback cannot remove or rearm
// a newer timer, even when Timer.Stop reports that the old callback has started.
func (a *autosaver) schedule(target autosaveTarget) {
	if a == nil || a.debounce <= 0 || !target.valid() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return
	}
	key := target.key()
	var deferredSince time.Time
	if old := a.timers[key]; old != nil {
		if old.timer != nil {
			old.timer.Stop()
		}
		deferredSince = old.deferredSince
	}
	a.generation++
	e := &autosaveEntry{ticket: autosaveTicket{autosaveTarget: target, generation: a.generation}, scheduledAt: time.Now(), deferredSince: deferredSince}
	a.timers[key] = e
	a.setStatusLocked(target, "pending", "")
	a.armLocked(e)
}

func (a *autosaver) current(ticket autosaveTicket) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.timers[ticket.key()]
	return !a.stopped && e != nil && e.ticket.generation == ticket.generation
}

func (a *autosaver) finish(ticket autosaveTicket, state, err string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.timers[ticket.key()]
	if e == nil || e.ticket.generation != ticket.generation {
		return
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	delete(a.timers, ticket.key())
	a.setStatusLocked(ticket.autosaveTarget, state, err)
}

// deferBusy only rearms the still-current generation. New edits retain the
// original deferral clock, so sustained activity cannot postpone saves forever.
func (a *autosaver) deferBusy(ticket autosaveTicket, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.timers[ticket.key()]
	if a.stopped || e == nil || e.ticket.generation != ticket.generation {
		return true
	}
	if e.deferredSince.IsZero() {
		e.deferredSince = now
	}
	if now.Sub(e.deferredSince) >= autosaveMaxDefer {
		e.deferredSince = time.Time{}
		return false
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	a.armLocked(e)
	return true
}

// Confirmed explicit saves cancel only timers older than the request. A newer
// edit that completed while that save was in flight must keep its safety net.
func (a *autosaver) cancelSaved(target autosaveTarget, started time.Time) {
	if a == nil || !target.valid() {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.timers[target.key()]; e != nil {
		if e.scheduledAt.After(started) {
			return
		}
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(a.timers, target.key())
	}
	a.setStatusLocked(target, "saved", "")
}

func (a *autosaver) stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopped = true
	for _, e := range a.timers {
		if e.timer != nil {
			e.timer.Stop()
		}
	}
	a.timers = map[string]*autosaveEntry{}
}

func confirmedSave(resp *protocol.Response) bool {
	if resp == nil || !resp.OK {
		return false
	}
	saved, ok := resp.Result["saved"].(bool)
	partial, _ := resp.Result["partial"].(bool)
	return ok && saved && !partial
}

// Preserve the native save result but propagate unconfirmed persistence through
// every caller, including typed HTTP/apply and write-health observers.
func normalizeSaveResponse(req *protocol.Request, resp *protocol.Response) {
	if req == nil || resp == nil || !resp.OK || (req.Action != "schematic.save" && req.Action != "pcb.save") {
		return
	}
	if !confirmedSave(resp) {
		resp.OK = false
		resp.Error = &protocol.ErrorInfo{Code: "SAVE_UNCONFIRMED", Message: "save did not confirm boolean saved:true"}
	}
}

func certifiedAutosaveTarget(req *protocol.Request, resp *protocol.Response) (autosaveTarget, string) {
	target := autosaveTarget{WindowID: req.WindowID, SaveAction: saveActionForDocType(docTypeForAction(req.Action))}
	if resp.MutationContext != nil {
		target.Context = *resp.MutationContext
	}
	// This typed source writer is the one document-domain action with a known
	// document save target. Do not infer autosave for other generic domains.
	if sourceWriter(req.Action) {
		target.SaveAction = saveActionForDocType(target.Context.DocumentType)
	}
	if resp.MutationContextAvailable == nil {
		return target, "connector omitted mutationContextAvailable; upgrade the connector for document-bound autosave"
	}
	if !*resp.MutationContextAvailable {
		reason := "connector could not certify mutation/save document identity"
		if resp.MutationContextError != "" {
			reason += ": " + resp.MutationContextError
		}
		return target, reason
	}
	if !target.valid() {
		return target, "certified mutation context did not identify the expected project/document/type"
	}
	return target, ""
}

func (s *Server) autosaveUnavailable(target autosaveTarget, resp *protocol.Response, reason string) {
	s.autosave.mu.Lock()
	s.autosave.setStatusLocked(target, "unavailable", reason)
	s.autosave.mu.Unlock()
	resp.Warnings = append(resp.Warnings, reason)
	s.logf("autosave: %s on %s unavailable: %s", target.SaveAction, target.WindowID, reason)
}

func (s *Server) maybeAutosave(req *protocol.Request, resp *protocol.Response, started time.Time) {
	if s.autosave == nil || req == nil || resp == nil || !resp.OK {
		return
	}
	action := saveActionForDocType(docTypeForAction(req.Action))
	if action == "" && !sourceWriter(req.Action) {
		return
	}
	if req.Action == action {
		if confirmedSave(resp) {
			target, reason := certifiedAutosaveTarget(req, resp)
			if reason != "" {
				s.autosaveUnavailable(target, resp, reason+"; pending autosave timers were not canceled")
			} else {
				s.autosave.cancelSaved(target, started)
			}
		}
		return
	}
	if !requestMutates(req) {
		return
	}
	target, reason := certifiedAutosaveTarget(req, resp)
	if reason != "" {
		s.autosaveUnavailable(target, resp, reason+"; autosave was not armed")
		return
	}
	s.autosave.schedule(target)
}

func (s *Server) deferAutosave(ticket autosaveTicket) bool {
	if s.autosave == nil {
		return false
	}
	busy := s.clientActionsInFlight(ticket.WindowID) > 0
	if s.queueBlocks != nil {
		if _, _, blocked := s.queueBlocks.blocked(ticket.WindowID); blocked {
			busy = true
		}
	}
	if !busy {
		return false
	}
	return s.autosave.deferBusy(ticket, time.Now())
}

func autosaveRequest(ticket autosaveTicket, id string) protocol.Request {
	expected := map[string]any{"projectUuid": ticket.Context.ProjectUUID, "documentUuid": ticket.Context.DocumentUUID, "documentType": ticket.Context.DocumentType}
	if ticket.Context.TabID != "" {
		expected["tabId"] = ticket.Context.TabID
	}
	return protocol.Request{
		Envelope: protocol.Envelope{ID: id, Type: protocol.TypeRequest, Version: "v1", WindowID: ticket.WindowID, CreatedAt: time.Now().UTC()},
		Action:   ticket.SaveAction, Payload: map[string]any{"expectedContext": expected},
	}
}

// dispatchSave is target-bound and fail-closed. The connector checks identity
// inside its FIFO, closing the race between a cached context and actual save.
func (s *Server) dispatchSave(ticket autosaveTicket) {
	if !s.autosave.current(ticket) {
		return
	}
	target, ok := s.hub.target(ticket.WindowID)
	if !ok {
		s.autosave.finish(ticket, "failed", "connector disconnected before save")
		return
	}
	if s.deferAutosave(ticket) || !s.autosave.current(ticket) {
		return
	}
	req := autosaveRequest(ticket, s.nextRequestID())
	ctx, cancel := context.WithTimeout(s.connCtx, dispatchTimeout)
	defer cancel()
	started := time.Now().UTC()
	resp, err := target.dispatch(ctx, req)
	if err != nil {
		resp = &protocol.Response{OK: false, Error: &protocol.ErrorInfo{Code: "AUTOSAVE_FAILED", Message: err.Error()}}
	} else if !confirmedSave(resp) {
		detail := "save did not confirm boolean saved:true"
		if resp != nil && resp.Error != nil {
			detail = fmt.Sprintf("%s: %s", resp.Error.Code, resp.Error.Message)
		}
		if resp == nil {
			resp = &protocol.Response{}
		}
		resp.OK = false
		if resp.Error == nil {
			resp.Error = &protocol.ErrorInfo{Code: "SAVE_UNCONFIRMED", Message: detail}
		}
	}
	identityReason := ""
	if confirmedSave(resp) {
		certified, reason := certifiedAutosaveTarget(&req, resp)
		identityReason = reason
		if identityReason == "" && (certified.key() != ticket.key() || certified.Context.TabID != ticket.Context.TabID) {
			identityReason = "certified save context did not match the frozen autosave target"
		}
		if identityReason != "" {
			resp.Warnings = append(resp.Warnings, identityReason+"; saved:true was retained but target persistence is unavailable")
		}
	}
	s.audit.Append(fromResponse(started, &req, resp))
	if identityReason != "" {
		s.autosave.finish(ticket, "unavailable", identityReason)
	} else if confirmedSave(resp) {
		s.autosave.finish(ticket, "saved", "")
	} else {
		reason := "save was not confirmed"
		if resp.Error != nil {
			reason = resp.Error.Code + ": " + resp.Error.Message
		}
		s.autosave.finish(ticket, "failed", reason)
	}
	s.logf("autosave: %s on %s doc=%s (saved=%v)", ticket.SaveAction, ticket.WindowID, ticket.Context.DocumentUUID, confirmedSave(resp))
}
