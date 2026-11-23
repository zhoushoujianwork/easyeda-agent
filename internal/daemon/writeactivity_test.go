package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func activityReq(window, action string, payload map[string]any) *protocol.Request {
	return &protocol.Request{Envelope: protocol.Envelope{ID: "r", WindowID: window}, Action: action, Payload: payload}
}

func TestWriteActivityCountsOnlyForwardedWrites(t *testing.T) {
	clock := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	tr := newWriteActivityTracker()
	tr.now = func() time.Time { return clock }

	tr.begin(activityReq("w1", "schematic.components.list", nil))()                                        // read
	tr.begin(activityReq("w1", "schematic.primitives.delete", map[string]any{dryRunPayloadField: true}))() // preview
	tr.begin(activityReq("", "schematic.primitives.delete", nil))()                                        // no window
	if got := tr.all(); got != nil {
		t.Fatalf("reads, previews and windowless requests must not count: %+v", got)
	}

	first := tr.begin(activityReq("w1", "schematic.primitives.delete", nil))
	second := tr.begin(activityReq("w1", "schematic.component.modify", nil))
	got := tr.all()["w1"]
	if got.InFlight != 2 || got.LastAction != "schematic.component.modify" || got.LastWriteAt != clock.Format(time.RFC3339Nano) {
		t.Fatalf("two writes in flight: %+v", got)
	}

	// The recent-write window runs from the END of the last write.
	clock = clock.Add(7 * time.Second)
	first()
	first() // idempotent
	got = tr.all()["w1"]
	if got.InFlight != 1 || got.LastWriteAt != clock.Format(time.RFC3339Nano) {
		t.Fatalf("after one release: %+v", got)
	}
	second()
	if got = tr.all()["w1"]; got.InFlight != 0 {
		t.Fatalf("after both releases: %+v", got)
	}
}

func TestWriteActivityRetentionKeepsInFlightWrites(t *testing.T) {
	clock := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	tr := newWriteActivityTracker()
	tr.now = func() time.Time { return clock }

	tr.begin(activityReq("idle", "schematic.primitives.delete", nil))()
	stuck := tr.begin(activityReq("stuck", "schematic.primitives.delete", nil))

	clock = clock.Add(writeActivityRetention + time.Second)
	got := tr.all()
	if _, ok := got["idle"]; ok {
		t.Fatalf("idle window past retention must be dropped: %+v", got)
	}
	if got["stuck"].InFlight != 1 {
		t.Fatalf("a write still in flight is never dropped: %+v", got)
	}

	// A finish after a prune is reported again.
	stuck()
	if got = tr.all(); got["stuck"].InFlight != 0 || got["stuck"].LastWriteAt != clock.Format(time.RFC3339Nano) {
		t.Fatalf("finished write: %+v", got)
	}
}

func TestHealthEndpointExposesWriteActivity(t *testing.T) {
	s := New(Options{})
	release := s.writeActivity.begin(activityReq("w9", "schematic.primitives.delete", nil))
	defer release()
	rec := httptest.NewRecorder()
	s.routes(60832).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var h health
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatalf("bad health json: %v (%s)", err, rec.Body.String())
	}
	if wa := h.WriteActivity["w9"]; wa.InFlight != 1 || wa.LastAction != "schematic.primitives.delete" || wa.LastWriteAt == "" {
		t.Fatalf("writeActivity = %+v (%s)", wa, rec.Body.String())
	}

	quiet := httptest.NewRecorder()
	New(Options{}).routes(60832).ServeHTTP(quiet, httptest.NewRequest(http.MethodGet, "/health", nil))
	if strings.Contains(quiet.Body.String(), "writeActivity") {
		t.Fatalf("quiet daemon must omit writeActivity: %s", quiet.Body.String())
	}
}
