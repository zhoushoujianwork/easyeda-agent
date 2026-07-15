package daemon

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// TestAudit_EntryCarriesClientID pins the client-attribution field (issue
// #108): a request carrying a ClientID must land in the JSONL audit entry so
// multi-client incidents can be attributed from the log alone.
func TestAudit_EntryCarriesClientID(t *testing.T) {
	req := &protocol.Request{
		Envelope: protocol.Envelope{ID: "req_1", WindowID: "w1"},
		Action:   "pcb.via.create",
		ClientID: "mikas-mbp:12345:e2e-regression",
	}
	resp := &protocol.Response{OK: true}
	started := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	entry := fromResponse(started, req, resp)
	if entry.ClientID != "mikas-mbp:12345:e2e-regression" {
		t.Fatalf("fromResponse clientId = %q, want the request's", entry.ClientID)
	}

	// Round-trip through the writer: the JSONL row must carry clientId.
	w := newAuditWriter(t.TempDir())
	w.Append(entry)

	data, err := os.ReadFile(w.Path(started))
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatalf("parse audit row: %v", err)
	}
	if got := row["clientId"]; got != "mikas-mbp:12345:e2e-regression" {
		t.Errorf("audit row clientId = %v, want mikas-mbp:12345:e2e-regression", got)
	}
	if got := row["action"]; got != "pcb.via.create" {
		t.Errorf("audit row action = %v, want pcb.via.create", got)
	}
}

// TestAudit_NoClientIDOmitted verifies anonymous callers stay unattributed
// rather than getting a fabricated identity.
func TestAudit_NoClientIDOmitted(t *testing.T) {
	req := &protocol.Request{
		Envelope: protocol.Envelope{ID: "req_2"},
		Action:   "pcb.line.list",
	}
	entry := fromResponse(time.Now().UTC(), req, &protocol.Response{OK: true})
	if entry.ClientID != "" {
		t.Fatalf("clientId should be empty for anonymous callers, got %q", entry.ClientID)
	}
	b, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	var row map[string]any
	if err := json.Unmarshal(b, &row); err != nil {
		t.Fatalf("parse row: %v", err)
	}
	if _, present := row["clientId"]; present {
		t.Error("clientId must be omitted (omitempty) when the caller sent none")
	}
}
