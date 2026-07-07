package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Service is the identity string the connector and CLI use to confirm they are
// talking to an easyeda-agent daemon rather than some other local server.
const Service = "easyeda-agent"

// Options configures a daemon Server.
type Options struct {
	Host      string
	PortStart int
	PortEnd   int
	Version   string

	// ArtifactDir is the FALLBACK directory for inline artifact bytes from the
	// connector, used only when a request carries no outputDir. The CLI sends its
	// own working directory as outputDir, so artifacts normally land in
	// <cwd>/.easyeda/artifacts (see artifactDir). Defaults to "artifacts"
	// (relative to the daemon's working directory) when empty.
	ArtifactDir string

	// AuditDir is where per-day JSONL action logs are appended. Defaults to
	// ~/.easyeda-agent/audit/ when empty.
	AuditDir string

	// AutosaveDebounce, when > 0, enables daemon-level debounced autosave: after a
	// successful mutating action the daemon saves the window once edits quiesce for
	// this long (a burst coalesces into one save). 0 disables it. See autosave.go.
	AutosaveDebounce time.Duration
}

// Server is the long-running local HTTP server. It serves /health, accepts
// connector WebSockets on /connect, and forwards typed actions on /action.
// Artifact storage and audit logging come later.
type Server struct {
	opts     Options
	hub      *hub
	reqSeq   atomic.Uint64
	log      io.Writer
	audit    *auditWriter
	autosave *autosaver // nil when Options.AutosaveDebounce <= 0

	// inflight tracks non-reentrant actions currently forwarded, keyed
	// "<action>|<windowId>" — see acquireExclusive / nonReentrant.
	inflight sync.Map

	// connCtx is cancelled on shutdown so connector read loops unblock.
	connCtx    context.Context
	connCancel context.CancelFunc
}

// acquireExclusive claims the per-window slot for a non-reentrant action.
// The second return is false while another request holds the slot; on true,
// call release() when the action settles.
func (s *Server) acquireExclusive(action, windowID string) (release func(), acquired bool) {
	key := action + "|" + windowID
	if _, loaded := s.inflight.LoadOrStore(key, struct{}{}); loaded {
		return nil, false
	}
	return func() { s.inflight.Delete(key) }, true
}

// logf writes a diagnostic line to the server log, if one is set.
func (s *Server) logf(format string, args ...any) {
	if s.log != nil {
		fmt.Fprintf(s.log, "%s daemon: "+format+"\n", append([]any{Service}, args...)...)
	}
}

// New builds a Server. It does not bind a port until Run is called.
func New(opts Options) *Server {
	s := &Server{
		opts:  opts,
		hub:   newHub(),
		audit: newAuditWriter(opts.AuditDir),
	}
	if opts.AutosaveDebounce > 0 {
		s.autosave = newAutosaver(opts.AutosaveDebounce, s.dispatchSave)
	}
	return s
}

type health struct {
	Service string   `json:"service"`
	Version string   `json:"version"`
	Status  string   `json:"status"`
	Port    int      `json:"port"`
	Windows []Window `json:"windows"`
}

// routes builds the HTTP handlers. port is the bound port, reported in /health
// so callers can confirm which port in the range was selected.
func (s *Server) routes(port int) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(health{
			Service: Service,
			Version: s.opts.Version,
			Status:  "ok",
			Port:    port,
			Windows: s.hub.listAnnotated(s.opts.Version),
		})
	})
	mux.HandleFunc("/eda", s.handleConnect)
	mux.HandleFunc("/action", s.handleAction)
	return mux
}

// listen binds the first free port in [PortStart, PortEnd] on Host.
func (s *Server) listen() (net.Listener, int, error) {
	host := s.opts.Host
	if host == "" {
		host = "127.0.0.1"
	}
	if s.opts.PortStart <= 0 || s.opts.PortEnd <= 0 || s.opts.PortStart > s.opts.PortEnd {
		return nil, 0, fmt.Errorf("invalid port range %d-%d", s.opts.PortStart, s.opts.PortEnd)
	}

	var lastErr error
	for port := s.opts.PortStart; port <= s.opts.PortEnd; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		if err == nil {
			return ln, port, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("no free port in %d-%d: %w", s.opts.PortStart, s.opts.PortEnd, lastErr)
}

// Run binds a port, serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context, log io.Writer) error {
	listener, port, err := s.listen()
	if err != nil {
		return err
	}

	s.log = log
	s.connCtx, s.connCancel = context.WithCancel(context.Background())
	defer s.connCancel()

	httpServer := &http.Server{
		Handler:           s.routes(port),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()

	fmt.Fprintf(log, "%s daemon listening on http://%s:%d (health: /health, connector: /eda, action: /action)\n", Service, s.opts.Host, port)
	if s.autosave != nil {
		s.logf("autosave on (debounce %s)", s.opts.AutosaveDebounce)
	}

	select {
	case <-ctx.Done():
		fmt.Fprintf(log, "%s daemon shutting down\n", Service)
		s.autosave.stop()
		// Unblock connector read loops so their handlers return and Shutdown
		// does not wait the full timeout on long-lived WebSockets.
		s.connCancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
