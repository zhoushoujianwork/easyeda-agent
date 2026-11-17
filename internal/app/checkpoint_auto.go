package app

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Automatic capture: before the first mutating action this process sends to a
// schematic page, read that page's source once and record it. A CLI command is
// therefore one rollback step, whatever it does internally. Capture is a
// safety net, not a gate: any failure is reported on stderr and the write
// proceeds without a checkpoint.

// checkpointNavigation are the actions after which the active page may differ,
// so the next write has to look again before deciding it is already covered.
var checkpointNavigation = map[string]bool{
	"document.open": true, "document.close": true, "schematic.page.open": true,
	"schematic.page.create": true, "schematic.page.delete": true,
}

type checkpointState struct {
	mu      sync.Mutex
	settled map[string]bool // window → the active page is known and handled
	covered map[string]bool // window+document → captured in this process
	busy    bool
}

var checkpointProcess = &checkpointState{settled: map[string]bool{}, covered: map[string]bool{}}

func payloadIsDryRun(payload any) bool {
	m, ok := payload.(map[string]any)
	return ok && m["dryRun"] == true
}

// autoCheckpoint runs inside postAction, before the request is sent.
func autoCheckpoint(cfg *appConfig, action, window string, payload any) {
	st := checkpointProcess
	st.mu.Lock()
	if checkpointNavigation[action] {
		delete(st.settled, window)
	}
	skip := st.busy || os.Getenv(envCheckpoint) == "off" || !actionMutates(action) || payloadIsDryRun(payload) || st.settled[window]
	if !skip {
		st.busy = true // the reads below re-enter postAction
	}
	st.mu.Unlock()
	if skip {
		return
	}
	defer func() {
		st.mu.Lock()
		st.busy, st.settled[window] = false, true
		st.mu.Unlock()
	}()
	entry, err := captureCheckpoint(cfg, action, window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠ no checkpoint before %s: %v (the write continues; this step cannot be rolled back)\n", action, err)
		return
	}
	if entry != nil {
		fmt.Fprintf(os.Stderr, "checkpoint %s (%s, %d bytes stored)\n", entry.ID, entry.Kind, entry.Stored)
	}
}

// captureCheckpoint returns nil without error when there is nothing to record:
// the active document is not a schematic page, or this process already
// recorded it.
func captureCheckpoint(cfg *appConfig, action, window string) (*checkpointEntry, error) {
	cur, err := requestAction(cfg, "document.current", window, nil)
	if err != nil {
		return nil, fmt.Errorf("active document unreadable: %w", err)
	}
	if cur.Context == nil || cur.Context.ProjectUUID == "" || cur.Context.DocumentUUID == "" {
		return nil, fmt.Errorf("active document has no readable identity")
	}
	if cur.Context.DocumentType != "schematic" {
		return nil, nil
	}
	project, document := cur.Context.ProjectUUID, cur.Context.DocumentUUID
	st := checkpointProcess
	key := window + "\x00" + document
	st.mu.Lock()
	done := st.covered[key]
	st.mu.Unlock()
	if done {
		return nil, nil
	}
	snapshot, err := readSourceSnapshot(cfg, window, project, document)
	if err != nil {
		return nil, err
	}
	store, err := openCheckpointStore(defaultCheckpointDir(), project, document)
	if err != nil {
		return nil, err
	}
	entry, err := store.add(checkpointEntry{Project: project, Document: document, Command: cfg.command, Action: action,
		Label: os.Getenv(envCheckpointLabel)}, snapshot.Source)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	st.covered[key] = true
	st.mu.Unlock()
	return &entry, nil
}

func readSourceSnapshot(cfg *appConfig, window, project, document string) (*documentSourceSnapshot, error) {
	res, err := requestActionTimed(cfg, "document.source.get", window, map[string]any{"projectUuid": project, "documentUuid": document}, 45*time.Second)
	if err != nil {
		return nil, err
	}
	return sourceSnapshotFromResult(res.Result, project, document)
}
