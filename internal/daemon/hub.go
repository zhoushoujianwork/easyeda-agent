package daemon

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Window is a read-only snapshot of a connected EasyEDA window, used by /health
// and listings.
type Window struct {
	WindowID         string           `json:"windowId"`
	ConnectorVersion string           `json:"connectorVersion"`
	EasyEDAVersion   string           `json:"easyedaVersion"`
	Capabilities     []string         `json:"capabilities"`
	Context          protocol.Context `json:"context"`
	ConnectedAt      time.Time        `json:"connectedAt"`
	LastSeen         time.Time        `json:"lastSeen"`
}

// conn is a single connected connector. The /connect read loop owns reads;
// writes are serialized through writeMu so dispatch goroutines can send
// requests concurrently and safely.
type conn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex

	mu          sync.Mutex
	windowID    string
	connVersion string
	edaVersion  string
	caps        []string
	ctx         protocol.Context
	connectedAt time.Time
	lastSeen    time.Time

	pendingMu sync.Mutex
	pending   map[string]chan *protocol.Response
}

func newConn(ws *websocket.Conn, now time.Time) *conn {
	return &conn{
		ws:          ws,
		connectedAt: now,
		lastSeen:    now,
		pending:     map[string]chan *protocol.Response{},
	}
}

func (c *conn) applyRegister(msg protocol.Register, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.windowID = msg.WindowID
	c.connVersion = msg.ConnectorVersion
	c.edaVersion = msg.EasyEDAVersion
	c.caps = msg.Capabilities
	c.lastSeen = now
}

func (c *conn) applyContext(msg protocol.ContextMessage, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg.WindowID != "" {
		c.windowID = msg.WindowID
	}
	c.ctx = msg.Context()
	c.lastSeen = now
}

func (c *conn) touch(now time.Time) {
	c.mu.Lock()
	c.lastSeen = now
	c.mu.Unlock()
}

func (c *conn) id() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.windowID
}

func (c *conn) snapshot() Window {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Window{
		WindowID:         c.windowID,
		ConnectorVersion: c.connVersion,
		EasyEDAVersion:   c.edaVersion,
		Capabilities:     c.caps,
		Context:          c.ctx,
		ConnectedAt:      c.connectedAt,
		LastSeen:         c.lastSeen,
	}
}

func (c *conn) write(ctx context.Context, v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return wsjson.Write(ctx, c.ws, v)
}

// dispatch sends a request to the connector and waits for the matching response
// (correlated by request id) or until ctx is done.
func (c *conn) dispatch(ctx context.Context, req protocol.Request) (*protocol.Response, error) {
	ch := make(chan *protocol.Response, 1)
	c.pendingMu.Lock()
	c.pending[req.ID] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, req.ID)
		c.pendingMu.Unlock()
	}()

	if err := c.write(ctx, req); err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// deliver routes an inbound response to the goroutine waiting on its id.
func (c *conn) deliver(resp *protocol.Response) {
	c.pendingMu.Lock()
	ch, ok := c.pending[resp.ID]
	c.pendingMu.Unlock()
	if ok {
		select {
		case ch <- resp:
		default:
		}
	}
}

// hub tracks connected connectors keyed by windowId.
type hub struct {
	mu      sync.RWMutex
	windows map[string]*conn
}

func newHub() *hub {
	return &hub{windows: map[string]*conn{}}
}

func (h *hub) add(c *conn) {
	id := c.id()
	if id == "" {
		return
	}
	h.mu.Lock()
	h.windows[id] = c
	h.mu.Unlock()
}

func (h *hub) remove(windowID string) {
	if windowID == "" {
		return
	}
	h.mu.Lock()
	delete(h.windows, windowID)
	h.mu.Unlock()
}

func (h *hub) get(windowID string) (*conn, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.windows[windowID]
	return c, ok
}

// target resolves the connector for an action. An explicit windowID wins;
// otherwise, if exactly one connector is connected, it is used.
func (h *hub) target(windowID string) (*conn, bool) {
	if windowID != "" {
		return h.get(windowID)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.windows) != 1 {
		return nil, false
	}
	for _, c := range h.windows {
		return c, true
	}
	return nil, false
}

// windowForProject resolves a project name or uuid to a single connected window
// id, so callers can route by stable identity instead of the ephemeral windowId
// (which changes on every reconnect — multi-window/multi-agent routing).
//
// A project may legitimately be open in MORE THAN ONE window (e.g. a schematic
// window + a PCB window). When several match, disambiguate by preferDoc — the
// document type implied by the action's domain (pcb.* → "pcb", schematic.* →
// "schematic"). Returns (id, found, ambiguous): ambiguous is true only when the
// project still maps to multiple windows after the preferDoc filter, in which
// case the caller should fall back to an explicit --window.
func (h *hub) windowForProject(project, preferDoc string) (id string, found bool, ambiguous bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var matches []Window
	for _, c := range h.windows {
		w := c.snapshot()
		if w.Context.ProjectName == project || w.Context.ProjectUUID == project {
			matches = append(matches, w)
		}
	}
	switch len(matches) {
	case 0:
		return "", false, false
	case 1:
		return matches[0].WindowID, true, false
	}
	// Multiple windows for this project — narrow to the one whose active
	// document matches the action's domain.
	if preferDoc != "" {
		var narrowed []Window
		for _, w := range matches {
			if w.Context.DocumentType == preferDoc {
				narrowed = append(narrowed, w)
			}
		}
		if len(narrowed) == 1 {
			return narrowed[0].WindowID, true, false
		}
	}
	return "", false, true
}

func (h *hub) list() []Window {
	h.mu.RLock()
	conns := make([]*conn, 0, len(h.windows))
	for _, c := range h.windows {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	out := make([]Window, 0, len(conns))
	for _, c := range conns {
		out = append(out, c.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WindowID < out[j].WindowID })
	return out
}
