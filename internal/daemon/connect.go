package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// handleConnect accepts the connector WebSocket and runs the read loop until the
// connection drops or the daemon shuts down.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The daemon is localhost-only; the connector runs inside EasyEDA whose
		// origin differs from the daemon host.
		// TODO: replace with an explicit origin allowlist once the EasyEDA
		// extension origin is known.
		InsecureSkipVerify: true,
		// EasyEDA's WebSocket client does not interoperate with permessage-deflate
		// as negotiated by this library; disable compression for compatibility.
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		s.logf("connector accept failed from %s: %v", r.RemoteAddr, err)
		return
	}
	// Snapshots and netlist/BOM artifacts are inlined as base64 in response
	// frames and routinely exceed coder/websocket's 32 KiB default read limit.
	// 32 MiB comfortably covers schematic PNGs and large BOM/netlist files.
	ws.SetReadLimit(32 << 20)
	s.logf("connector connected from %s", r.RemoteAddr)

	// Reads use the daemon's connection context so shutdown unblocks the loop.
	ctx := s.connContext(r)
	c := newConn(ws, time.Now().UTC())
	remote := r.RemoteAddr
	origin := r.Header.Get("Origin")
	ua := r.Header.Get("User-Agent")
	s.logf("connector %s upgraded (origin=%q ua=%q)", remote, origin, ua)
	defer func() {
		s.hub.remove(c.id())
		ws.Close(websocket.StatusNormalClosure, "closing")
		s.logf("connector %s closed", remote)
	}()

	// Identify ourselves first so the connector can verify it reached an
	// easyeda-agent daemon before it registers.
	if err := c.write(ctx, protocol.Handshake{
		Type:    protocol.TypeHandshake,
		Service: Service,
		Version: s.opts.Version,
	}); err != nil {
		s.logf("handshake write failed to %s: %v", r.RemoteAddr, err)
		return
	}
	s.logf("handshake sent to %s", r.RemoteAddr)

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			s.logf("connector %s read error: %v", remote, err)
			return
		}
		preview := string(data)
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		s.logf("connector %s frame (%d bytes): %s", remote, len(data), preview)
		s.handleFrame(ctx, c, data)
	}
}

// handleFrame decodes one connector frame by its "type" discriminator and acts
// on it. Malformed or unknown frames are ignored rather than dropping the
// connection.
func (s *Server) handleFrame(ctx context.Context, c *conn, data []byte) {
	now := time.Now().UTC()

	var typed protocol.Typed
	if err := json.Unmarshal(data, &typed); err != nil {
		return
	}

	switch typed.Type {
	case protocol.TypeRegister:
		var msg protocol.Register
		if err := json.Unmarshal(data, &msg); err != nil || msg.WindowID == "" {
			return
		}
		c.applyRegister(msg, now)
		s.hub.add(c)
		s.logf("connector registered windowId=%s", msg.WindowID)

	case protocol.TypeContext:
		var msg protocol.ContextMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		c.applyContext(msg, now)

	case protocol.TypePing:
		c.touch(now)
		var ping protocol.Ping
		if err := json.Unmarshal(data, &ping); err != nil {
			return
		}
		_ = c.write(ctx, protocol.Pong{Type: protocol.TypePong, ID: ping.ID})

	case protocol.TypeResponse:
		var resp protocol.Response
		if err := json.Unmarshal(data, &resp); err != nil {
			return
		}
		c.touch(now)
		if resp.Context != nil {
			c.applyResponseContext(*resp.Context, now)
		}
		c.deliver(&resp)

	case protocol.TypeLog:
		// Diagnostic line emitted by the connector; surface it in the daemon log
		// so connector-internal events (heartbeat/reconnect) are visible here.
		var msg struct {
			Msg string `json:"msg"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		c.touch(now)
		s.logf("connector LOG: %s", msg.Msg)
	}
}

// connContext returns the context used for connector reads: the daemon's
// shutdown context, or the request context when no daemon context is set (e.g.
// in tests that exercise the handler directly).
func (s *Server) connContext(r *http.Request) context.Context {
	if s.connCtx != nil {
		return s.connCtx
	}
	return r.Context()
}
