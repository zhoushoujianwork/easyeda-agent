package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// stalledWriteConnector reproduces issue #317's shape: the host never settles
// createNetFlag, while every other request (reads, probes) answers at once.
func stalledWriteConnector(ctx context.Context, c *websocket.Conn) {
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var req protocol.Request
		if json.Unmarshal(data, &req) != nil || req.Type != protocol.TypeRequest {
			continue
		}
		if req.Action == "schematic.netflag.create" {
			continue // the host call hangs: no response, ever
		}
		_ = wsjson.Write(ctx, c, protocol.Response{
			Envelope: protocol.Envelope{ID: req.ID, Type: protocol.TypeResponse, Version: "v1"},
			OK:       true,
			Result:   map[string]any{"echo": req.Action},
		})
	}
}

func writeStallOf(t *testing.T, resp protocol.Response) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(resp.Result["writeStall"])
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func TestWriteTimeoutCarriesBudgetAndRecovery(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()
	c := dialConnector(t, base, "win-stall")
	defer c.Close(websocket.StatusNormalClosure, "")
	go stalledWriteConnector(t.Context(), c)
	waitForWindow(t, base, "win-stall")

	write := `{"action":"schematic.netflag.create","windowId":"win-stall","timeoutMs":3000,"payload":{"kind":"Power","net":"VCC","x":0,"y":0}}`

	started := time.Now()
	first := postAction(t, base, write)
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("hung write was not bounded by its budget: %v", elapsed)
	}
	if first.OK || first.Error == nil || first.Error.Code != "DISPATCH_FAILED" {
		t.Fatalf("hung write must fail with DISPATCH_FAILED, got %+v", first)
	}
	ws := writeStallOf(t, first)
	if ws["writeState"] != "unknown" || ws["suspected"] != "write-timeout" || ws["budgetMs"].(float64) <= 0 {
		t.Fatalf("first timeout advisory = %v", ws)
	}

	// Reads still answer — and must never be reported as write evidence.
	read := postAction(t, base, `{"action":"schematic.components.list","windowId":"win-stall"}`)
	if !read.OK || read.Result["writeStall"] != nil {
		t.Fatalf("read during stall: ok=%v writeStall=%v err=%+v", read.OK, read.Result["writeStall"], read.Error)
	}

	second := postAction(t, base, write)
	ws = writeStallOf(t, second)
	if ws["suspected"] != "host-write-path-stalled" || ws["consecutiveWriteTimeouts"].(float64) != 2 || ws["readsAnsweredSince"].(float64) < 1 {
		t.Fatalf("second timeout advisory = %v", ws)
	}
	if !strings.Contains(strings.Join(second.Warnings, "\n"), "restart EasyEDA Pro") {
		t.Fatalf("stall warning lacks recovery step: %v", second.Warnings)
	}
}

func TestWriteStallRunEndsAtAnAnsweredWrite(t *testing.T) {
	st := &windowHealthState{}
	push := func(mutates, ok, timedOut bool) {
		st.push(&healthSample{mutates: mutates, ok: ok, timedOut: timedOut})
	}
	push(false, true, false) // read before the run: not evidence
	push(true, true, false)  // answered write ends the run
	push(false, true, false) // read before the first timeout: not counted
	push(true, false, true)
	push(false, true, false)
	push(true, false, true)
	if timeouts, reads := writeStallLocked(st); timeouts != 2 || reads != 1 {
		t.Fatalf("writeStallLocked = (%d, %d), want (2, 1)", timeouts, reads)
	}
	push(true, false, false) // a non-timeout failure also ends the run
	if timeouts, _ := writeStallLocked(st); timeouts != 0 {
		t.Fatalf("non-timeout write must end the run, got %d", timeouts)
	}
}

// EDAD-10: a caller that hung up (CLI interrupted → request context canceled)
// is not evidence that the host write path stalled. Only a spent budget is.
func TestClientCancelIsNotAWriteTimeout(t *testing.T) {
	req := &protocol.Request{Action: "schematic.netflag.create"}
	canceled := outcomeOf(req, nil, context.Canceled)
	if canceled.TimedOut || !canceled.ClientCanceled {
		t.Fatalf("canceled caller classified as timeout: %+v", canceled)
	}
	deadline := outcomeOf(req, nil, context.DeadlineExceeded)
	if !deadline.TimedOut || deadline.ClientCanceled {
		t.Fatalf("deadline not classified as timeout: %+v", deadline)
	}

	tr := newWriteHealthTracker()
	for i := 0; i < 3; i++ {
		tr.observe("w", canceled)
	}
	tr.mu.Lock()
	st := tr.byWindow["w"]
	tr.mu.Unlock()
	if st != nil && len(st.recent) != 0 {
		t.Fatalf("client-canceled outcomes must not enter write health: %d samples", len(st.recent))
	}
	tr.observe("w", deadline)
	tr.mu.Lock()
	timeouts, _ := writeStallLocked(tr.byWindow["w"])
	tr.mu.Unlock()
	if timeouts != 1 {
		t.Fatalf("deadline write should count once, got %d", timeouts)
	}
}
