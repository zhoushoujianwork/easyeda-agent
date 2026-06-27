package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// startDaemon runs a daemon on a free port and returns its host:port and a
// cleanup func that shuts it down.
func startDaemon(t *testing.T) (string, func()) {
	t.Helper()
	port := freePort(t)
	srv := New(Options{Host: "127.0.0.1", PortStart: port, PortEnd: port, Version: "0.1.0-test", ArtifactDir: t.TempDir()})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, io.Discard) }()

	base := fmt.Sprintf("127.0.0.1:%d", port)
	waitForHealth(t, "http://"+base+"/health")

	return base, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("daemon did not shut down within timeout")
		}
	}
}

func dialConnector(t *testing.T, base, windowID string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, "ws://"+base+"/eda", nil)
	if err != nil {
		t.Fatalf("dial connector: %v", err)
	}

	reg := protocol.Register{
		Type:             protocol.TypeRegister,
		WindowID:         windowID,
		ConnectorVersion: "0.1.0",
		EasyEDAVersion:   "test",
		Capabilities:     []string{"schematic.v1"},
	}
	if err := wsjson.Write(ctx, c, reg); err != nil {
		t.Fatalf("send register: %v", err)
	}
	return c
}

// echoRequests replies to every inbound request with an ok response that echoes
// the action name, until ctx is cancelled or the connection drops.
func echoRequests(ctx context.Context, c *websocket.Conn) {
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var typed protocol.Typed
		if json.Unmarshal(data, &typed) != nil || typed.Type != protocol.TypeRequest {
			continue
		}
		var req protocol.Request
		if json.Unmarshal(data, &req) != nil {
			continue
		}
		resp := protocol.Response{
			Envelope: protocol.Envelope{ID: req.ID, Type: protocol.TypeResponse, Version: "v1"},
			OK:       true,
			Result:   map[string]any{"echo": req.Action},
		}
		_ = wsjson.Write(ctx, c, resp)
	}
}

func getHealth(t *testing.T, base string) health {
	t.Helper()
	resp, err := http.Get("http://" + base + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	var h health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	return h
}

func waitForWindow(t *testing.T, base, windowID string) Window {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, w := range getHealth(t, base).Windows {
			if w.WindowID == windowID {
				return w
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("window %q never appeared in /health", windowID)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func postAction(t *testing.T, base, body string) protocol.Response {
	t.Helper()
	resp, err := http.Post("http://"+base+"/action", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post action: %v", err)
	}
	defer resp.Body.Close()
	var out protocol.Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode action response: %v", err)
	}
	return out
}

func TestConnectorRegistersAndContextAppearsInHealth(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c := dialConnector(t, base, "win-1")
	defer c.Close(websocket.StatusNormalClosure, "")

	ctxMsg := protocol.ContextMessage{
		Type:         protocol.TypeContext,
		WindowID:     "win-1",
		ProjectUUID:  "proj-9",
		ProjectName:  "demo",
		DocumentType: "schematic",
	}
	if err := wsjson.Write(ctx, c, ctxMsg); err != nil {
		t.Fatalf("send context: %v", err)
	}

	win := waitForWindow(t, base, "win-1")
	if win.ConnectorVersion != "0.1.0" {
		t.Fatalf("unexpected connector version: %q", win.ConnectorVersion)
	}

	// Context delivery is async relative to register; poll for it.
	deadline := time.Now().Add(3 * time.Second)
	for win.Context.ProjectUUID == "" && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		win = waitForWindow(t, base, "win-1")
	}
	if win.Context.ProjectUUID != "proj-9" || win.Context.DocumentType != "schematic" {
		t.Fatalf("context not reflected in health: %+v", win.Context)
	}
}

func TestActionDispatchToConnector(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()

	c := dialConnector(t, base, "win-1")
	defer c.Close(websocket.StatusNormalClosure, "")
	go echoRequests(t.Context(), c)

	waitForWindow(t, base, "win-1")

	resp := postAction(t, base, `{"action":"schematic.components.list","windowId":"win-1"}`)
	if !resp.OK {
		t.Fatalf("expected ok response, got error: %+v", resp.Error)
	}
	if got, _ := resp.Result["echo"].(string); got != "schematic.components.list" {
		t.Fatalf("expected echoed action, got %v", resp.Result["echo"])
	}
	if resp.ID == "" {
		t.Fatal("expected daemon-assigned request id")
	}
}

func TestSystemHealthActionNeedsNoConnector(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()

	resp := postAction(t, base, `{"action":"system.health"}`)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %+v", resp.Error)
	}
	if svc, _ := resp.Result["service"].(string); svc != Service {
		t.Fatalf("expected service %q, got %v", Service, resp.Result["service"])
	}
}

func TestActionWithoutConnector(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()

	resp := postAction(t, base, `{"action":"schematic.components.list"}`)
	if resp.OK {
		t.Fatal("expected error when no connector is connected")
	}
	if resp.Error == nil || resp.Error.Code != "NO_CONNECTOR" {
		t.Fatalf("expected NO_CONNECTOR, got %+v", resp.Error)
	}
}

func TestUnknownActionRejected(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()

	resp := postAction(t, base, `{"action":"bogus.thing"}`)
	if resp.OK || resp.Error == nil || resp.Error.Code != "UNKNOWN_ACTION" {
		t.Fatalf("expected UNKNOWN_ACTION, got ok=%v err=%+v", resp.OK, resp.Error)
	}
}

func TestDaemonSendsHandshakeOnConnect(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, "ws://"+base+"/eda", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	var hs protocol.Handshake
	if err := wsjson.Read(ctx, c, &hs); err != nil {
		t.Fatalf("read handshake: %v", err)
	}
	if hs.Type != protocol.TypeHandshake || hs.Service != Service {
		t.Fatalf("expected handshake for %q, got %+v", Service, hs)
	}
}

func TestPingGetsPong(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()

	c := dialConnector(t, base, "win-1")
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := t.Context()
	if err := wsjson.Write(ctx, c, protocol.Ping{Type: protocol.TypePing, ID: "hb-7"}); err != nil {
		t.Fatalf("send ping: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("no pong received")
		}
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var typed protocol.Typed
		if json.Unmarshal(data, &typed) != nil || typed.Type != protocol.TypePong {
			continue // skip the handshake frame and anything else
		}
		var pong protocol.Pong
		if err := json.Unmarshal(data, &pong); err != nil {
			t.Fatalf("decode pong: %v", err)
		}
		if pong.ID != "hb-7" {
			t.Fatalf("pong id mismatch: %q", pong.ID)
		}
		return
	}
}

func TestLogFrameIsHandledGracefully(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()

	c := dialConnector(t, base, "win-1")
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := t.Context()

	// A connector diagnostic log frame must be accepted without dropping the
	// connection. Send one, then prove the socket still serves ping/pong — i.e.
	// the read loop survived the log frame.
	logFrame := struct {
		Type string `json:"type"`
		Msg  string `json:"msg"`
	}{Type: protocol.TypeLog, Msg: "liveness lost: 3 pings unanswered"}
	if err := wsjson.Write(ctx, c, logFrame); err != nil {
		t.Fatalf("send log frame: %v", err)
	}

	if err := wsjson.Write(ctx, c, protocol.Ping{Type: protocol.TypePing, ID: "hb-after-log"}); err != nil {
		t.Fatalf("send ping: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("no pong after log frame — log handling broke the connection")
		}
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var typed protocol.Typed
		if json.Unmarshal(data, &typed) != nil || typed.Type != protocol.TypePong {
			continue
		}
		var pong protocol.Pong
		if err := json.Unmarshal(data, &pong); err != nil {
			t.Fatalf("decode pong: %v", err)
		}
		if pong.ID == "hb-after-log" {
			return
		}
	}
}

func TestActionArtifactPersisted(t *testing.T) {
	base, cleanup := startDaemon(t)
	defer cleanup()

	c := dialConnector(t, base, "win-1")
	defer c.Close(websocket.StatusNormalClosure, "")

	const payload = "PNG-BYTES"

	// Connector that answers any request with an inline-base64 artifact.
	go func() {
		ctx := t.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var typed protocol.Typed
			if json.Unmarshal(data, &typed) != nil || typed.Type != protocol.TypeRequest {
				continue
			}
			var req protocol.Request
			if json.Unmarshal(data, &req) != nil {
				continue
			}
			_ = wsjson.Write(ctx, c, protocol.Response{
				Envelope: protocol.Envelope{ID: req.ID, Type: protocol.TypeResponse, Version: "v1"},
				OK:       true,
				Artifacts: []protocol.Artifact{{
					ID:           "art_snap",
					Kind:         "schematic_snapshot",
					MimeType:     "image/png",
					FileName:     "snap.png",
					InlineBase64: base64.StdEncoding.EncodeToString([]byte(payload)),
				}},
			})
		}
	}()

	waitForWindow(t, base, "win-1")

	// Send a CLI cwd via outputDir; artifacts must land under its hidden dir.
	outDir := t.TempDir()
	resp := postAction(t, base, fmt.Sprintf(`{"action":"schematic.snapshot","windowId":"win-1","outputDir":%q}`, outDir))
	if !resp.OK || len(resp.Artifacts) != 1 {
		t.Fatalf("expected one artifact, got ok=%v artifacts=%d err=%+v", resp.OK, len(resp.Artifacts), resp.Error)
	}

	a := resp.Artifacts[0]
	if a.InlineBase64 != "" {
		t.Fatal("inlineBase64 should be stripped after persistence")
	}
	if a.Path == "" {
		t.Fatal("expected persisted artifact path")
	}
	if a.Size != int64(len(payload)) {
		t.Fatalf("expected size %d, got %d", len(payload), a.Size)
	}
	want := sha256.Sum256([]byte(payload))
	if a.SHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("sha256 mismatch: %s", a.SHA256)
	}
	got, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatalf("read persisted artifact: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("persisted bytes mismatch: %q", string(got))
	}

	// Lands in the CLI cwd's hidden .easyeda/artifacts dir...
	wantDir := filepath.Join(outDir, ".easyeda", "artifacts")
	if dir := filepath.Dir(a.Path); dir != wantDir {
		t.Fatalf("artifact dir = %s, want %s", dir, wantDir)
	}
	// ...with a sortable, findable name: <YYYYMMDD-HHMMSS>-<kind>-<short>.png
	bn := filepath.Base(a.Path)
	if !strings.Contains(bn, "schematic_snapshot") || !strings.HasSuffix(bn, ".png") {
		t.Fatalf("unexpected artifact name: %s", bn)
	}
	if _, err := time.Parse("20060102-150405", bn[:15]); err != nil {
		t.Fatalf("name not timestamp-prefixed: %s (%v)", bn, err)
	}
}
