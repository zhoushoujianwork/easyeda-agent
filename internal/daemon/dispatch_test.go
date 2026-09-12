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

func TestRequestTimeout(t *testing.T) {
	cases := []struct {
		name      string
		timeoutMs int
		want      time.Duration
	}{
		{"default when unset", 0, dispatchTimeout},
		{"default when negative", -5, dispatchTimeout},
		{"caller budget minus grace", 20000, 18 * time.Second},
		{"place execution window plus response grace", int((8*time.Second + protocol.DispatchResponseGrace).Milliseconds()), 8 * time.Second},
		{"clamped to minimum", 1000, minDispatchTimeout},
		{"clamped to maximum", int((11 * time.Minute).Milliseconds()), maxDispatchTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &protocol.Request{TimeoutMs: tc.timeoutMs}
			if got := requestTimeout(req); got != tc.want {
				t.Fatalf("requestTimeout(%d) = %v, want %v", tc.timeoutMs, got, tc.want)
			}
		})
	}
}

func TestActionRequestAllowsLarge3DModelPayload(t *testing.T) {
	// system.health is daemon-local, so this isolates the shared request decoder
	// without needing a live connector to consume the model payload.
	s := New(Options{})
	body := `{"action":"system.health","payload":{"dataBase64":"` + strings.Repeat("A", 2<<20) + `"}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(body))

	s.handleAction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("2 MiB base64 action payload rejected: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestActionRequestBodyLimitBoundary(t *testing.T) {
	const prefix = `{"action":"system.health","payload":{"dataBase64":"`
	const suffix = `"}}`
	for _, extra := range []int{0, 1} {
		body := prefix + strings.Repeat("A", actionRequestBodyLimit-len(prefix)-len(suffix)+extra) + suffix
		rec := httptest.NewRecorder()
		s := New(Options{})
		s.handleAction(rec, httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(body)))
		if extra == 0 {
			if rec.Code != http.StatusOK {
				t.Fatalf("exact limit rejected: %d %s", rec.Code, rec.Body.String())
			}
		} else {
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "http: request body too large") {
				t.Fatalf("oversize not rejected at ingress: %d %s", rec.Code, rec.Body.String())
			}
		}
	}
}

func TestAcquireExclusive(t *testing.T) {
	s := New(Options{})

	release, ok := s.acquireExclusive("pcb.drc.check", "w1")
	if !ok {
		t.Fatal("first acquire should succeed")
	}

	// Same action+window while held → busy.
	if _, ok := s.acquireExclusive("pcb.drc.check", "w1"); ok {
		t.Fatal("second acquire on the same window should be refused")
	}
	// Different window or different action → independent slots.
	if rel, ok := s.acquireExclusive("pcb.drc.check", "w2"); !ok {
		t.Fatal("other window should not be blocked")
	} else {
		rel()
	}
	if rel, ok := s.acquireExclusive("schematic.drc.check", "w1"); !ok {
		t.Fatal("other action on the same window should not be blocked")
	} else {
		rel()
	}

	// Released slot is reusable.
	release()
	rel2, ok := s.acquireExclusive("pcb.drc.check", "w1")
	if !ok {
		t.Fatal("acquire after release should succeed")
	}
	rel2()
}

func TestNonReentrantSet(t *testing.T) {
	// The guard exists for DRC (A4: background-window recompute never settles;
	// stacked retries make it worse). Guard both editors' DRC, nothing else.
	if !nonReentrant["pcb.drc.check"] || !nonReentrant["schematic.drc.check"] {
		t.Fatal("both DRC actions must be non-reentrant")
	}
	if nonReentrant["pcb.components.list"] {
		t.Fatal("reads must not be guarded")
	}
}

// ── stale windowId must not be reported as "no connector" ────────────────
//
// A page refresh mints a new windowId, so a caller's id silently dies. Saying
// "no EasyEDA connector is available" when the connector is up and healthy is
// what makes an agent go restart a daemon that was never down — the same class
// of defect as reporting an infra failure as a board failure.

func postActionTo(t *testing.T, s *Server, body string) (int, protocol.Response) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(body))
	s.handleAction(rec, req)
	var resp protocol.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response json: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, resp
}

func TestStaleWindowIsNotReportedAsNoConnector(t *testing.T) {
	s := New(Options{})
	s.hub.add(connWithDoc("live", "p", "ceshi", "doc-1", "schematic"))

	_, resp := postActionTo(t, s, `{"action":"schematic.components.list","windowId":"dead-id"}`)
	if resp.OK || resp.Error == nil {
		t.Fatal("expected an error response")
	}
	if resp.Error.Code != "STALE_WINDOW" {
		t.Fatalf("code = %q, want STALE_WINDOW (NO_CONNECTOR sends agents restarting a healthy daemon)", resp.Error.Code)
	}
	if strings.Contains(resp.Error.Message, "no EasyEDA connector is available") {
		t.Fatalf("message must not claim the connector is down: %q", resp.Error.Message)
	}
	// The caller must learn what IS connected and how to route stably.
	for _, want := range []string{"live", "ceshi", "--project"} {
		if !strings.Contains(resp.Error.Detail, want) {
			t.Fatalf("detail missing %q: %s", want, resp.Error.Detail)
		}
	}
}

func TestNoWindowsAtAllStillReportsNoConnector(t *testing.T) {
	// The honest case: nothing is connected. This one SHOULD say so.
	s := New(Options{})
	_, resp := postActionTo(t, s, `{"action":"schematic.components.list","windowId":"dead-id"}`)
	if resp.Error == nil || resp.Error.Code != "NO_CONNECTOR" {
		t.Fatalf("want NO_CONNECTOR, got %+v", resp.Error)
	}
}

func TestAmbiguousWindowWithoutAHintNamesTheCandidates(t *testing.T) {
	s := New(Options{})
	s.hub.add(connWithDoc("w1", "p1", "ceshi", "d1", "schematic"))
	s.hub.add(connWithDoc("w2", "p2", "motobox", "d2", "pcb"))

	_, resp := postActionTo(t, s, `{"action":"schematic.components.list"}`)
	if resp.Error == nil || resp.Error.Code != "AMBIGUOUS_WINDOW" {
		t.Fatalf("want AMBIGUOUS_WINDOW, got %+v", resp.Error)
	}
	for _, want := range []string{"ceshi", "motobox", "--project"} {
		if !strings.Contains(resp.Error.Detail, want) {
			t.Fatalf("detail missing %q: %s", want, resp.Error.Detail)
		}
	}
}
