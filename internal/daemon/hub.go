package daemon

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
	// ConnectorVersionOK is set only by /health: true/false when both the
	// connector and daemon report comparable semver (so a stale connector loaded
	// in an open window is visible), nil when either version is non-semver (dev
	// builds) and no verdict can be made.
	ConnectorVersionOK *bool `json:"connectorVersionOk,omitempty"`
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

// applyResponseContext refreshes the cached window context from the live context
// the connector attaches to every action response. This keeps /health and
// project routing current as the user switches documents — without it, the
// context stays frozen at the connect-time snapshot (so a window that opened on
// the home page reads as "home" forever). Non-empty fields are merged so a
// response that omits a field never clobbers a known value.
func (c *conn) applyResponseContext(ctx protocol.Context, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.ProjectUUID != "" {
		c.ctx.ProjectUUID = ctx.ProjectUUID
	}
	if ctx.ProjectName != "" {
		c.ctx.ProjectName = ctx.ProjectName
	}
	if ctx.DocumentUUID != "" {
		c.ctx.DocumentUUID = ctx.DocumentUUID
	}
	if ctx.DocumentType != "" {
		c.ctx.DocumentType = ctx.DocumentType
	}
	if ctx.TabID != "" {
		c.ctx.TabID = ctx.TabID
	}
	if ctx.Unit != "" {
		c.ctx.Unit = ctx.Unit
	}
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

// retiredWindow remembers the stable identity a windowId used to carry.
//
// The connector mints a FRESH windowId on every handshake (`crypto.randomUUID()`
// in transport.ts), so a plain page refresh silently invalidates whatever
// windowId the caller was holding. The old behaviour answered such a request
// with NO_CONNECTOR / "no EasyEDA connector is available" — which is false and
// actively misleading: the connector is up, only its id moved. Agents read that
// as "the link is down" and go restart the daemon or reopen the page.
//
// Keeping the retired identity lets the daemon re-route the request to the
// window that replaced it, and say so, instead of failing the caller.
type retiredWindow struct {
	ProjectUUID  string
	ProjectName  string
	DocumentUUID string
	DocumentType string
	RetiredAt    time.Time
}

// How long / how many retired windowIds stay resolvable. Long enough to cover a
// refresh mid-workflow, bounded so a long-lived daemon cannot grow unbounded.
const (
	retiredWindowTTL = 30 * time.Minute
	retiredWindowMax = 64
	// connectorTTL bounds stale WebSocket registrations. The connector sends
	// heartbeats frequently; a full minute without any frame means the browser
	// page or extension session is gone and must not remain routable.
	connectorTTL = 90 * time.Second
)

// hub tracks connected connectors keyed by windowId.
type hub struct {
	mu      sync.RWMutex
	windows map[string]*conn
	// retired maps a disconnected windowId → the identity it carried, so
	// resolveRetired can forward a stale-id request to its live successor.
	retired map[string]retiredWindow
}

func newHub() *hub {
	return &hub{windows: map[string]*conn{}, retired: map[string]retiredWindow{}}
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

// pruneStale removes registrations whose transport stopped sending frames.
// It is deliberately explicit instead of relying only on websocket close
// notifications: browser extension reloads can leave the old socket half-open
// for a while, which otherwise makes /health show phantom connector versions.
func (h *hub) pruneStale(now time.Time) int {
	h.mu.Lock()
	var stale []*conn
	for id, c := range h.windows {
		c.mu.Lock()
		last := c.lastSeen
		c.mu.Unlock()
		if !last.IsZero() && now.Sub(last) > connectorTTL {
			stale = append(stale, c)
			delete(h.windows, id)
			w := c.snapshot()
			h.retired[id] = retiredWindow{ProjectUUID: w.Context.ProjectUUID, ProjectName: w.Context.ProjectName, DocumentUUID: w.Context.DocumentUUID, DocumentType: w.Context.DocumentType, RetiredAt: now}
		}
	}
	h.pruneRetiredLocked()
	h.mu.Unlock()
	for _, c := range stale {
		if c.ws != nil {
			_ = c.ws.Close(websocket.StatusGoingAway, "stale connector session")
		}
	}
	return len(stale)
}

func (h *hub) remove(windowID string) {
	if windowID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Snapshot the identity BEFORE dropping the connection — after the delete
	// there is no way to learn what project/document this id stood for.
	if c, ok := h.windows[windowID]; ok {
		w := c.snapshot()
		h.retired[windowID] = retiredWindow{
			ProjectUUID:  w.Context.ProjectUUID,
			ProjectName:  w.Context.ProjectName,
			DocumentUUID: w.Context.DocumentUUID,
			DocumentType: w.Context.DocumentType,
			RetiredAt:    time.Now().UTC(),
		}
	}
	delete(h.windows, windowID)
	h.pruneRetiredLocked()
}

// pruneRetiredLocked drops expired entries, then the oldest ones if still over
// the cap. Caller must hold the write lock.
func (h *hub) pruneRetiredLocked() {
	now := time.Now().UTC()
	for id, r := range h.retired {
		if now.Sub(r.RetiredAt) > retiredWindowTTL {
			delete(h.retired, id)
		}
	}
	for len(h.retired) > retiredWindowMax {
		oldestID, oldestAt := "", time.Time{}
		for id, r := range h.retired {
			if oldestID == "" || r.RetiredAt.Before(oldestAt) {
				oldestID, oldestAt = id, r.RetiredAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(h.retired, oldestID)
	}
}

// resolveRetired maps a windowId that is no longer connected to the live window
// that replaced it, using the identity the dead id used to carry.
//
// The document uuid is the strongest signal — unlike windowId it survives a
// refresh (it identifies the schematic page / PCB itself), so a window showing
// the SAME document is unambiguously the successor. Project identity is the
// fallback when the successor happens to sit on a different page.
//
// Returns ok=false when the id is unknown/expired, or when the identity it
// carried no longer matches any connected window — the caller must then report
// a real error rather than silently retarget someone else's window.
func (h *hub) resolveRetired(windowID string) (newID string, prev retiredWindow, ok bool) {
	if windowID == "" {
		return "", retiredWindow{}, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	prev, known := h.retired[windowID]
	if !known || time.Since(prev.RetiredAt) > retiredWindowTTL {
		return "", retiredWindow{}, false
	}
	var byProject []Window
	for _, c := range h.windows {
		w := c.snapshot()
		if prev.DocumentUUID != "" && w.Context.DocumentUUID == prev.DocumentUUID {
			return w.WindowID, prev, true // same document — certain successor
		}
		sameProject := (prev.ProjectUUID != "" && w.Context.ProjectUUID == prev.ProjectUUID) ||
			(prev.ProjectName != "" && w.Context.ProjectName == prev.ProjectName)
		if sameProject {
			byProject = append(byProject, w)
		}
	}
	// Only redirect on an unambiguous project match: with two windows of the
	// same project open (schematic + PCB), guessing could land a mutation on the
	// wrong document — worse than an honest error.
	if len(byProject) == 1 {
		return byProject[0].WindowID, prev, true
	}
	return "", prev, false
}

// liveWindowSummary describes the currently connected windows for error text,
// so "your id is stale" can be answered with "…and here is what IS connected".
func (h *hub) liveWindowSummary() (count int, summary string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.windows) == 0 {
		return 0, ""
	}
	parts := make([]string, 0, len(h.windows))
	for _, c := range h.windows {
		w := c.snapshot()
		project := w.Context.ProjectName
		if project == "" {
			project = "(no project)"
		}
		doc := w.Context.DocumentType
		if doc == "" {
			doc = "?"
		}
		parts = append(parts, fmt.Sprintf("%s [project=%s doc=%s]", w.WindowID, project, doc))
	}
	sort.Strings(parts)
	return len(parts), strings.Join(parts, ", ")
}

func (h *hub) get(windowID string) (*conn, bool) {
	h.pruneStale(time.Now().UTC())
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
	h.pruneStale(time.Now().UTC())
	h.mu.RLock()
	defer h.mu.RUnlock()
	var matches []Window
	for _, c := range h.windows {
		matches = append(matches, c.snapshot())
	}
	if len(matches) == 1 {
		return h.windows[matches[0].WindowID], true
	}
	if newest, ok := newestExactDocumentDuplicate(matches); ok {
		return h.windows[newest.WindowID], true
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
	h.pruneStale(time.Now().UTC())
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
	// Multiple windows for this project — narrow to the ones whose active
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
		if len(narrowed) > 1 {
			matches = narrowed
		}
	}

	// A connector reconnect briefly leaves the old and new registrations alive
	// together. EasyEDA 3.2.175 can also activate one extension twice. If every
	// candidate points at the exact same document tab, they are transport
	// duplicates rather than distinct user windows; route to the newest one.
	if newest, ok := newestExactDocumentDuplicate(matches); ok {
		return newest.WindowID, true, false
	}
	return "", false, true
}

func newestExactDocumentDuplicate(matches []Window) (Window, bool) {
	if len(matches) < 2 {
		return Window{}, false
	}
	first := matches[0]
	if first.Context.ProjectUUID == "" ||
		first.Context.DocumentUUID == "" ||
		first.Context.DocumentType == "" ||
		first.Context.TabID == "" {
		return Window{}, false
	}
	newest := first
	for _, w := range matches[1:] {
		if w.Context.ProjectUUID != first.Context.ProjectUUID ||
			w.Context.DocumentUUID != first.Context.DocumentUUID ||
			w.Context.DocumentType != first.Context.DocumentType ||
			w.Context.TabID != first.Context.TabID {
			return Window{}, false
		}
		if w.ConnectedAt.After(newest.ConnectedAt) {
			newest = w
		}
	}
	return newest, true
}

// listAnnotated returns the window list with each window's ConnectorVersionOK
// computed, so /health surfaces a stale connector loaded in an open window.
func (h *hub) listAnnotated(daemonVersion string) []Window {
	out := h.list()
	// Newest connector semver among connected windows — a daemon-version-
	// independent reference, so staleness is still flagged when the daemon
	// version is non-semver (e.g. a `dev` build).
	newest := ""
	for _, w := range out {
		if c := semverCore(w.ConnectorVersion); c != "" && semverLess(newest, c) {
			newest = c
		}
	}
	for i := range out {
		out[i].ConnectorVersionOK = connectorVersionOK(out[i].ConnectorVersion, daemonVersion, newest)
	}
	return out
}

// connectorVersionOK decides whether a connector is up to date, from two
// independent signals:
//   - a connector strictly behind a peer window is definitely stale (→ false),
//     regardless of the daemon version — this catches an old window left open
//     after a re-import;
//   - otherwise, when both the connector and daemon parse as semver, it must
//     match the daemon (→ true/false).
//
// Returns nil (no verdict) when the connector is non-semver, or when it leads
// all peers and the daemon version is non-semver (a dev build, nothing to
// compare against).
func connectorVersionOK(connector, daemon, newestPeer string) *bool {
	cn := semverCore(connector)
	if cn == "" {
		return nil
	}
	if newestPeer != "" && semverLess(cn, newestPeer) {
		stale := false
		return &stale
	}
	// Only a CLEAN release tag (vX.Y.Z, no -suffix) yields a hard verdict against
	// the daemon. A dev daemon stamped by `git describe` (e.g. v0.5.1-19-ge9552d8)
	// or "dev" must NOT — its semver core ("0.5.1") is an old tag, not the real
	// code level, so comparing it to a newer connector would be a false mismatch.
	if isCleanRelease(daemon) {
		ok := cn == semverCore(daemon)
		return &ok
	}
	return nil
}

// staleConnectorNotice returns an actionable one-liner when a just-registered
// connector is behind the running daemon (both clean semver), or "" otherwise.
// The connector .eext has no sideload auto-update, so the daemon can only detect
// the mismatch and tell the user to re-import — it cannot swap it in place.
func staleConnectorNotice(connector, daemon string) string {
	cn, dn := semverCore(connector), semverCore(daemon)
	if cn == "" || dn == "" || !isCleanRelease(daemon) {
		return "" // dev build or unparseable — no hard verdict
	}
	if !semverLess(cn, dn) {
		return ""
	}
	return fmt.Sprintf("stale connector: v%s < daemon v%s — re-import the connector .eext "+
		"(uninstall old in 已安装 → import new → FULLY quit & relaunch EasyEDA). "+
		"Latest .eext: https://github.com/%s/releases/latest", cn, dn, releaseRepoSlug)
}

// releaseRepoSlug is the GitHub owner/repo that ships the connector .eext.
const releaseRepoSlug = "zhoushoujianwork/easyeda-agent"

// isCleanRelease reports whether v is a bare release tag (vX.Y.Z with no
// pre-release/build suffix) — i.e. its semver core is the whole string.
func isCleanRelease(v string) bool {
	core := semverCore(v)
	return core != "" && core == strings.TrimPrefix(v, "v")
}

// semverLess reports whether semver core a < b (both "x.y.z" or ""). An empty
// string is treated as lower than any real version.
func semverLess(a, b string) bool {
	if a == b {
		return false
	}
	if a == "" {
		return true
	}
	if b == "" {
		return false
	}
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := range 3 {
		x, _ := strconv.Atoi(ap[i])
		y, _ := strconv.Atoi(bp[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// semverCore extracts the "x.y.z" core from a version string, dropping a leading
// 'v' and any "-suffix". Returns "" if the string is not x.y.z.
func semverCore(v string) string {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return ""
	}
	for _, p := range parts {
		if p == "" {
			return ""
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return ""
			}
		}
	}
	return v
}

func (h *hub) list() []Window {
	h.pruneStale(time.Now().UTC())
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
