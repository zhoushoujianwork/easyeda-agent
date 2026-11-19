package app

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildOfficialAutoroutePayload(t *testing.T) {
	got, err := buildOfficialAutoroutePayload(nil, []string{"GND", "XTAL_P"}, []int{1, 2}, "45", "completion")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"cornerStyle": "45", "optimization": "completion", "ignoreNets": []string{"GND", "XTAL_P"}, "layers": []int{1, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload=%v want %v", got, want)
	}
	if _, err := buildOfficialAutoroutePayload(nil, nil, nil, "30", "completion"); err == nil {
		t.Fatal("corner 30 accepted")
	}
	if _, err := buildOfficialAutoroutePayload(nil, nil, nil, "45", "best"); err == nil {
		t.Fatal("optimize best accepted")
	}
}

// fakeJobHost scripts the host side of the auto-route job model.
type fakeJobHost struct {
	clock       time.Time
	calls       []string
	runningFor  int // status polls answered "running" before the job completes on its own
	cancelAfter int // status polls after a cancel before the host reports canceled (-1 = never)
	canceledAt  int
	final       map[string]any
}

func (h *fakeJobHost) follow(timeout time.Duration) autorouteFollow {
	return autorouteFollow{
		call:        h.call,
		timeout:     timeout,
		interval:    5 * time.Second,
		cancelGrace: 10 * time.Second,
		now:         func() time.Time { return h.clock },
		sleep:       func(d time.Duration, _ <-chan os.Signal) bool { h.clock = h.clock.Add(d); return true },
	}
}

func (h *fakeJobHost) call(action string, payload map[string]any) (map[string]any, error) {
	h.calls = append(h.calls, action)
	switch action {
	case "pcb.route.auto.cancel":
		h.canceledAt = len(h.calls)
		return map[string]any{"jobId": payload["jobId"], "cancelRequested": true}, nil
	case "pcb.route.auto.status":
		polls := 0
		for _, c := range h.calls {
			if c == "pcb.route.auto.status" {
				polls++
			}
		}
		if h.canceledAt > 0 {
			after := len(h.calls) - h.canceledAt
			if h.cancelAfter >= 0 && after > h.cancelAfter {
				out := map[string]any{"jobId": payload["jobId"], "state": "canceled", "terminal": true, "tracked": true,
					"created": map[string]any{"tracks": 2.0, "arcs": 0.0, "vias": 0.0}, "partial": true}
				return out, nil
			}
			return map[string]any{"jobId": payload["jobId"], "state": "running", "terminal": false}, nil
		}
		if polls > h.runningFor {
			return h.final, nil
		}
		return map[string]any{"jobId": payload["jobId"], "state": "running", "terminal": false, "hostProgress": 40.0,
			"engine": map[string]any{"routabitity": 0.5}}, nil
	}
	return nil, fmt.Errorf("unexpected %s", action)
}

func TestAutorouteJobCompletesWithProgressEvents(t *testing.T) {
	h := &fakeJobHost{runningFor: 3, final: map[string]any{"jobId": "j1", "state": "completed", "terminal": true, "tracked": true,
		"created": map[string]any{"tracks": 5.0, "arcs": 0.0, "vias": 1.0}}}
	var out, errb bytes.Buffer
	if err := h.follow(time.Minute).follow("j1", &out, &errb); err != nil {
		t.Fatalf("completed job failed: %v\n%s", err, errb.String())
	}
	if n := strings.Count(errb.String(), "autoroute job j1: running"); n != 3 {
		t.Fatalf("want 3 progress events, got %d:\n%s", n, errb.String())
	}
	if !strings.Contains(errb.String(), "host progress 40") || !strings.Contains(errb.String(), "engine routability 0.500") {
		t.Fatalf("progress must carry host-reported values:\n%s", errb.String())
	}
	if strings.Contains(strings.Join(h.calls, ","), "cancel") {
		t.Fatal("a completed job must not be cancelled")
	}
}

func TestAutorouteJobTimeoutCancelsAndReportsCopper(t *testing.T) {
	h := &fakeJobHost{runningFor: 1000, cancelAfter: 1}
	var out, errb bytes.Buffer
	err := h.follow(20*time.Second).follow("j2", &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "--timeout 20s reached") || !strings.Contains(err.Error(), "2 new copper") {
		t.Fatalf("timeout must cancel and report created copper, got %v\n%s", err, errb.String())
	}
	if !strings.Contains(errb.String(), "host confirmed job j2 canceled") {
		t.Fatalf("cancel must be confirmed by host state:\n%s", errb.String())
	}
	if !strings.Contains(out.String(), `"partial": true`) {
		t.Fatalf("final job report missing on stdout: %s", out.String())
	}
}

func TestAutorouteJobUnconfirmedCancelIsNotReportedAsStopped(t *testing.T) {
	h := &fakeJobHost{runningFor: 1000, cancelAfter: -1}
	var out, errb bytes.Buffer
	start := h.clock
	err := h.follow(10*time.Second).follow("j3", &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "did not confirm cancellation") || !strings.Contains(err.Error(), "--job j3") {
		t.Fatalf("unconfirmed cancel must be an explicit error, got %v", err)
	}
	if waited := h.clock.Sub(start); waited > 10*time.Second+10*time.Second+2*time.Second {
		t.Fatalf("cancel grace not bounded: %v", waited)
	}
}

func TestAutorouteJobCompletedWithoutCopperFails(t *testing.T) {
	h := &fakeJobHost{runningFor: 0, final: map[string]any{"jobId": "j4", "state": "completed", "terminal": true, "tracked": true,
		"created": map[string]any{"tracks": 0.0, "arcs": 0.0, "vias": 0.0}}}
	var out, errb bytes.Buffer
	if err := h.follow(time.Minute).follow("j4", &out, &errb); err == nil || !strings.Contains(err.Error(), "added no copper") {
		t.Fatalf("zero-copper completion must fail, got %v", err)
	}
}

func TestAutorouteJobUnsupportedFallsBack(t *testing.T) {
	if !autorouteJobUnsupported(&actionError{Code: "UNKNOWN_ACTION"}) {
		t.Fatal("old connector must fall back")
	}
	if !autorouteJobUnsupported(&actionError{Code: "PRECONDITION_REFUSED", Message: "unsupported: eda.pcb_Document.startAutoRouting is not available"}) {
		t.Fatal("old host must fall back")
	}
	if autorouteJobUnsupported(&actionError{Code: "PRECONDITION_REFUSED", Message: "\"layers\" must be ..."}) {
		t.Fatal("bad input must not silently switch to the blocking router")
	}
}
