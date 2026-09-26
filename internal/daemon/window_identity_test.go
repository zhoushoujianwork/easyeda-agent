package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Flush a connection's inbound frames and fail if any unexpected action arrived.
func windowIdentityBarrier(t *testing.T, ctx context.Context, c *websocket.Conn, id string) {
	t.Helper()
	if err := wsjson.Write(ctx, c, protocol.Ping{Type: protocol.TypePing, ID: id}); err != nil {
		t.Fatal(err)
	}
	for {
		var frame struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := wsjson.Read(ctx, c, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type == protocol.TypeHandshake {
			continue
		}
		if frame.Type != protocol.TypePong || frame.ID != id {
			t.Fatalf("unexpected frame at barrier: %+v", frame)
		}
		return
	}
}

func TestSameDocumentWebSocketsRemainStableAndRouteExplicitly(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first := dialConnector(t, base, "first")
	defer first.CloseNow()
	second := dialConnector(t, base, "second")
	defer second.CloseNow()
	peers := []*websocket.Conn{first, second}
	ids := []string{"first", "second"}
	for round := 0; round < 10; round++ {
		for i, c := range peers {
			if err := wsjson.Write(ctx, c, protocol.ContextMessage{
				Type: protocol.TypeContext, WindowID: ids[i], ProjectUUID: "p", ProjectName: "same",
				DocumentUUID: "d", DocumentType: "schematic", TabID: "tab",
			}); err != nil {
				t.Fatal(err)
			}
			windowIdentityBarrier(t, ctx, c, fmt.Sprintf("%s-%d", ids[i], round))
		}
		if windows := getHealth(t, base).Windows; len(windows) != 2 {
			t.Fatalf("live connections changed: %+v", windows)
		}
	}
	for _, tc := range []struct{ body, code string }{
		{`{"action":"schematic.save"}`, "AMBIGUOUS_WINDOW"},
		{`{"action":"schematic.save","project":"p"}`, "AMBIGUOUS_PROJECT"},
	} {
		resp := postAction(t, base, tc.body)
		if resp.OK || resp.Error == nil || resp.Error.Code != tc.code {
			t.Fatalf("expected %s: %+v", tc.code, resp)
		}
	}
	for i, c := range peers {
		windowIdentityBarrier(t, ctx, c, ids[i]+"-no-dispatch")
	}

	var counts [2]atomic.Int32
	for i, c := range peers {
		go func() {
			for {
				var req protocol.Request
				if err := wsjson.Read(ctx, c, &req); err != nil {
					return
				}
				if req.Type != protocol.TypeRequest {
					continue
				}
				counts[i].Add(1)
				_ = wsjson.Write(ctx, c, protocol.Response{
					Envelope: protocol.Envelope{Type: protocol.TypeResponse, ID: req.ID}, OK: true,
					Result: map[string]any{"window": ids[i]},
				})
			}
		}()
	}
	for _, id := range ids {
		body, _ := json.Marshal(map[string]string{"action": "schematic.components.list", "windowId": id})
		resp := postAction(t, base, string(body))
		if !resp.OK || resp.Result["window"] != id {
			t.Fatalf("explicit route changed: %+v", resp)
		}
	}
	if counts[0].Load() != 1 || counts[1].Load() != 1 {
		t.Fatal("explicit requests crossed connections")
	}
	if err := first.CloseNow(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(getHealth(t, base).Windows) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("disconnected window did not retire")
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp := postAction(t, base, `{"action":"schematic.save","windowId":"first","project":"p"}`)
	if resp.OK || resp.Error == nil || resp.Error.Code != "STALE_WINDOW" {
		t.Fatalf("stale route transferred: %+v", resp)
	}
	if counts[1].Load() != 1 {
		t.Fatal("stale write reached the other connection")
	}
}
