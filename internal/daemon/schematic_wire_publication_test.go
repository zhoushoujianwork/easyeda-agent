package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestSchematicWirePublicationHTTPReadsOnly(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()
	connector := dialConnector(t, base, "publication-window")
	defer connector.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	before, after := geometryFixture(t), geometryFixture(t)
	after["wires"] = []any{map[string]any{"x0": 620., "y0": 1020., "x1": 590., "y1": 1020.}}
	var writes, reads atomic.Int32
	go func() {
		seq := 0
		for {
			var req protocol.Request
			if err := wsjson.Read(ctx, connector, &req); err != nil {
				return
			}
			if req.Type != protocol.TypeRequest {
				continue
			}
			seq++
			n, abandoned := seq, 0
			result := map[string]any{"primitiveId": "wire"}
			if req.Action == "schematic.components.list" {
				result = before
				if writes.Load() > 0 && reads.Add(1) > 1 {
					result = after
				}
			} else if req.Action == "schematic.wire.create" {
				writes.Add(1)
			} else {
				return // A polling implementation must not invent another action.
			}
			_ = wsjson.Write(ctx, connector, protocol.Response{
				Envelope: protocol.Envelope{ID: req.ID, Type: protocol.TypeResponse}, OK: true, Result: result,
				Context: &protocol.Context{ProjectUUID: "p1", DocumentUUID: "d1", DocumentType: "schematic"}, Seq: &n, SeqAbandoned: &abandoned})
		}
	}()
	for len(getHealth(t, base).Windows) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("connector never registered")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	resp, err := http.Post("http://"+base+"/action", "application/json", strings.NewReader(`{"action":"schematic.wire.create","windowId":"publication-window","payload":{"points":[[590,1020],[620,1020]]}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result protocol.Response
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || writes.Load() != 1 || reads.Load() != 2 {
		t.Fatalf("HTTP→WS publication verification: writes=%d reads=%d result=%+v", writes.Load(), reads.Load(), result)
	}
}

func TestSchematicWirePublicationReadback(t *testing.T) {
	for _, scenario := range []string{"delayed", "never-visible", "malformed", "unavailable", "wrong-doc", "wrong-write-doc", "old-seq", "abandoned", "new-invalid-geometry", "cancelled", "cancelled-visible", "deadline-visible", "cancelled-later-visible"} {
		t.Run(scenario, func(t *testing.T) {
			data, err := os.ReadFile("testdata/sch-p3-wire-publication.json")
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Proposed       map[string]any
				Before         map[string]any
				ImmediateAfter map[string]any
				PublishedAfter map[string]any
			}
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if scenario == "deadline-visible" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
			}
			defer cancel()
			writes, reads, seq := 0, 0, 0
			dispatch := func(_ context.Context, req protocol.Request) (*protocol.Response, error) {
				seq++
				n, abandoned := seq, 0
				res := &protocol.Response{OK: true, Seq: &n, SeqAbandoned: &abandoned,
					Context: &protocol.Context{ProjectUUID: "p1", DocumentUUID: "d1", DocumentType: "schematic"}}
				if req.Action != "schematic.components.list" {
					writes++
					res.Result = map[string]any{"primitiveId": "bdee3961676b43e4", "line": []any{[]any{205., 685., 185., 685.}}}
					if scenario == "wrong-write-doc" {
						res.Context.DocumentUUID = "other"
					}
					return res, nil
				}
				if writes == 0 {
					res.Result = fixture.Before
					return res, nil
				}
				reads++
				res.Result = fixture.PublishedAfter
				if (reads == 1 && scenario != "cancelled-visible" && scenario != "deadline-visible") || scenario == "never-visible" || scenario == "cancelled" {
					res.Result = fixture.ImmediateAfter
				}
				if reads > 1 {
					switch scenario {
					case "malformed":
						res.Result["wires"] = []any{map[string]any{"x0": "unknown"}}
					case "unavailable":
						res.Result["wiresAvailable"] = false
					case "wrong-doc":
						res.Context.DocumentUUID = "other"
					case "old-seq":
						n = 3 // Equal to the preceding after read, despite being after the write.
					case "abandoned":
						abandoned = 1
					case "new-invalid-geometry":
						res.Result["wires"] = []any{map[string]any{"x0": 145., "y0": 640., "x1": 260., "y1": 640.}}
					}
				}
				if scenario == "cancelled" || scenario == "cancelled-visible" || (scenario == "cancelled-later-visible" && reads > 1) {
					cancel()
				}
				if scenario == "deadline-visible" {
					time.Sleep(50 * time.Millisecond) // A dispatcher returning a late success despite its context.
				}
				return res, nil
			}
			s := New(Options{})
			res, err := s.forwardSchematicGeometry(ctx,
				protocol.Request{Envelope: protocol.Envelope{ID: "wire-publication", WindowID: "w1"}, Action: "schematic.wire.create", Payload: fixture.Proposed}, dispatch)
			if err != nil || writes != 1 {
				t.Fatalf("one mutation required: writes=%d reads=%d result=%+v err=%v", writes, reads, res, err)
			}
			if scenario == "delayed" {
				if !res.OK || reads != 2 {
					t.Fatalf("later authoritative geometry rejected: reads=%d result=%+v", reads, res)
				}
				guard := res.Result["geometryGuard"].(map[string]any)
				observations, _ := guard["observations"].([]map[string]any)
				if guard["readbackAttempts"] != 2 || len(observations) != 2 || observations[0]["coverageError"] == nil || observations[1]["coverageError"] != nil {
					t.Fatalf("publication observations missing: %+v", guard)
				}
				return
			}
			if res.OK || res.Result["partial"] != true {
				t.Fatalf("unknown/invalid landing accepted: reads=%d result=%+v", reads, res)
			}
			wantReads := 2
			if scenario == "never-visible" {
				wantReads = 5
				if !strings.Contains(res.Error.Detail, "not fully present") {
					t.Fatalf("lost coverage failure: %+v", res)
				}
			}
			if scenario == "cancelled" || scenario == "cancelled-visible" || scenario == "deadline-visible" || scenario == "wrong-write-doc" {
				wantReads = 1
			}
			if reads != wantReads {
				t.Fatalf("read bound/stop condition: got %d want %d", reads, wantReads)
			}
		})
	}
}

func TestSchematicConnectPinPublicationAndUnverifiedWrite(t *testing.T) {
	for _, verified := range []bool{true, false} {
		t.Run(map[bool]string{true: "delayed-wire", false: "unverified-mutation"}[verified], func(t *testing.T) {
			before, after := geometryFixture(t), geometryFixture(t)
			after["wires"] = []any{map[string]any{"x0": 620., "y0": 1020., "x1": 590., "y1": 1020.}}
			writes, reads, seq := 0, 0, 0
			dispatch := func(_ context.Context, req protocol.Request) (*protocol.Response, error) {
				seq++
				n, abandoned := seq, 0
				res := &protocol.Response{OK: true, Seq: &n, SeqAbandoned: &abandoned,
					Context: &protocol.Context{ProjectUUID: "p1", DocumentUUID: "d1", DocumentType: "schematic"}}
				if req.Action == "schematic.components.list" {
					res.Result = before
					if writes > 0 {
						reads++
						if reads > 1 {
							res.Result = after
						}
					}
				} else {
					writes++
					res.Result = map[string]any{"verified": verified, "primitiveId": "stub"}
				}
				return res, nil
			}
			s := New(Options{})
			res, err := s.forwardSchematicGeometry(context.Background(), protocol.Request{
				Envelope: protocol.Envelope{ID: "connect-publication"}, Action: "schematic.power.connect_pin",
				Payload: map[string]any{"pinX": 590., "pinY": 1020., "kind": "net_port_bi", "direction": "right", "offset": 30.}}, dispatch)
			if err != nil || writes != 1 || res.OK != verified {
				t.Fatalf("writes=%d reads=%d result=%+v err=%v", writes, reads, res, err)
			}
			if verified && reads != 2 || !verified && (reads != 0 || res.Result["partial"] != true) {
				t.Fatalf("unverified result must not enter settling: reads=%d result=%+v", reads, res)
			}
		})
	}
}

func TestSchematicWirePublicationHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	before := geometryFixture(t)
	writes := 0
	tick := time.Now()
	s := New(Options{})
	res, err := s.forwardSchematicGeometry(ctx, geometryRequest(`[[590,1020],[620,1020]]`), geometryDispatcher(before, before, &writes, false))
	if err != nil || writes != 1 || res.OK || res.Result["partial"] != true || !strings.Contains(res.Error.Detail, "deadline exceeded") {
		t.Fatalf("deadline lost: writes=%d result=%+v err=%v", writes, res, err)
	}
	if time.Since(tick) > time.Second {
		t.Fatal("read settling ignored caller deadline")
	}
}
